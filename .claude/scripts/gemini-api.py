#!/usr/bin/env python3
# /// script
# requires-python = ">=3.10"
# dependencies = ["google-genai"]
# ///
"""Direct Gemini API CLI with context caching and 2M token support.

Auth: GEMINI_API_KEY env var (from https://aistudio.google.com)
Deps: google-genai (declared via PEP 723 inline metadata, resolved by uv)

Usage:
    uv run .claude/scripts/gemini-api.py query --model MODEL --prompt "..." [--files FILE...] [--cache NAME] [--system "..."]
    uv run .claude/scripts/gemini-api.py pack --files FILE... [--repomix] [--output FILE]
    uv run .claude/scripts/gemini-api.py cache create --model MODEL --files FILE... --name NAME [--ttl SECS] [--system "..."]
    uv run .claude/scripts/gemini-api.py cache list
    uv run .claude/scripts/gemini-api.py cache get NAME
    uv run .claude/scripts/gemini-api.py cache extend NAME --ttl SECONDS
    uv run .claude/scripts/gemini-api.py cache delete NAME
"""

import argparse
import glob as globmod
import os
import subprocess
import sys
from pathlib import Path


def get_client():
    """Create a google-genai client, validating the API key."""
    api_key = os.environ.get("GEMINI_API_KEY")
    if not api_key:
        print(
            "Error: GEMINI_API_KEY not set.\n"
            "Get a free API key at https://aistudio.google.com\n"
            "Then: export GEMINI_API_KEY=your-key-here",
            file=sys.stderr,
        )
        sys.exit(1)

    from google import genai

    return genai.Client(api_key=api_key)


# --- Pack logic ---


def is_binary(filepath: str, block_size: int = 8192) -> bool:
    """Detect binary files via null-byte check."""
    try:
        with open(filepath, "rb") as f:
            chunk = f.read(block_size)
            return b"\x00" in chunk
    except OSError:
        return True


def git_ls_files(directory: str) -> list[str]:
    """List tracked files in a directory using git ls-files."""
    try:
        result = subprocess.run(
            ["git", "ls-files", directory],
            capture_output=True,
            text=True,
            check=True,
        )
        return [f for f in result.stdout.strip().split("\n") if f]
    except subprocess.CalledProcessError:
        # Fallback: walk directory
        files = []
        for root, _dirs, filenames in os.walk(directory):
            for fname in filenames:
                files.append(os.path.join(root, fname))
        return files


def expand_paths(file_args: list[str]) -> list[str]:
    """Expand file arguments (paths, dirs, globs) into individual files."""
    files = []
    for arg in file_args:
        # Glob expansion
        expanded = globmod.glob(arg, recursive=True)
        if not expanded:
            # Literal path
            expanded = [arg]

        for path in expanded:
            if os.path.isdir(path):
                files.extend(git_ls_files(path))
            elif os.path.isfile(path):
                files.append(path)
            else:
                print(f"Warning: skipping {path} (not found)", file=sys.stderr)

    # Deduplicate preserving order
    seen = set()
    unique = []
    for f in files:
        if f not in seen:
            seen.add(f)
            unique.append(f)
    return unique


def pack_files(file_args: list[str], use_repomix: bool = False) -> str:
    """Pack files into a single string with headers."""
    if use_repomix:
        return pack_with_repomix(file_args)

    files = expand_paths(file_args)
    parts = []
    skipped = 0

    for filepath in files:
        if is_binary(filepath):
            skipped += 1
            continue
        try:
            with open(filepath) as f:
                content = f.read()
            parts.append(f"=== FILE: {filepath} ===\n{content}")
        except OSError as e:
            print(f"Warning: could not read {filepath}: {e}", file=sys.stderr)

    if skipped:
        print(f"Skipped {skipped} binary files", file=sys.stderr)

    print(f"Packed {len(parts)} files", file=sys.stderr)
    return "\n\n".join(parts)


def pack_with_repomix(file_args: list[str]) -> str:
    """Pack files using repomix CLI."""
    try:
        cmd = ["repomix", "--style", "plain"] + file_args
        result = subprocess.run(cmd, capture_output=True, text=True, check=True)
        return result.stdout
    except FileNotFoundError:
        print("Error: repomix not found. Install with: npm i -g repomix", file=sys.stderr)
        sys.exit(1)
    except subprocess.CalledProcessError as e:
        print(f"Error running repomix: {e.stderr}", file=sys.stderr)
        sys.exit(1)


# --- Subcommands ---


def cmd_query(args):
    """Send a prompt to Gemini with optional file context or cache."""
    client = get_client()

    # Read prompt from stdin if piped
    prompt = args.prompt
    if not prompt and not sys.stdin.isatty():
        prompt = sys.stdin.read().strip()
    if not prompt:
        print("Error: --prompt is required (or pipe via stdin)", file=sys.stderr)
        sys.exit(1)

    # Build contents
    contents = []

    if args.cache:
        # Using cached content — files are already in the cache
        config = {"cached_content": args.cache}
        if args.system:
            print(
                "Warning: --system is ignored when using --cache (system instruction is in the cache)",
                file=sys.stderr,
            )
    else:
        config = {}
        if args.system:
            config["system_instruction"] = args.system

        # Pack files as inline context
        if args.files:
            packed = pack_files(args.files)
            if packed:
                contents.append(packed)

    contents.append(prompt)

    generate_config = {"temperature": args.temperature}

    try:
        from google.genai import types

        response = client.models.generate_content(
            model=args.model,
            contents=contents,
            config=types.GenerateContentConfig(
                temperature=args.temperature,
                cached_content=config.get("cached_content"),
                system_instruction=config.get("system_instruction"),
            ),
        )

        # Output response
        print(response.text)

        # Token usage to stderr
        if response.usage_metadata:
            meta = response.usage_metadata
            parts = [
                f"prompt_tokens: {meta.prompt_token_count}",
                f"response_tokens: {meta.candidates_token_count}",
                f"total_tokens: {meta.total_token_count}",
            ]
            if meta.cached_content_token_count:
                parts.append(f"cached_tokens: {meta.cached_content_token_count}")
            print(" | ".join(parts), file=sys.stderr)

    except Exception as e:
        print(f"Error: {e}", file=sys.stderr)
        sys.exit(1)


def cmd_pack(args):
    """Pack files for inspection or piping."""
    packed = pack_files(args.files, use_repomix=args.repomix)

    if args.output:
        with open(args.output, "w") as f:
            f.write(packed)
        print(f"Written to {args.output}", file=sys.stderr)
    else:
        print(packed)


def cmd_cache_create(args):
    """Create a CachedContent from files."""
    client = get_client()
    from google.genai import types

    packed = pack_files(args.files, use_repomix=args.repomix)
    if not packed:
        print("Error: no files to cache", file=sys.stderr)
        sys.exit(1)

    print(f"Uploading content to cache '{args.name}'...", file=sys.stderr)

    try:
        cache_config = {
            "displayName": args.name,
            "contents": [types.Content(parts=[types.Part(text=packed)], role="user")],
            "ttl": f"{args.ttl}s",
        }
        if args.system:
            cache_config["systemInstruction"] = args.system

        cache = client.caches.create(
            model=args.model,
            config=types.CreateCachedContentConfig(**cache_config),
        )

        print(f"cache_name: {cache.name}")
        print(f"model: {cache.model}")
        print(f"display_name: {cache.display_name}")
        if cache.usage_metadata:
            print(f"token_count: {cache.usage_metadata.total_token_count}")
        print(f"expires: {cache.expire_time}")

    except Exception as e:
        print(f"Error creating cache: {e}", file=sys.stderr)
        sys.exit(1)


def cmd_cache_list(_args):
    """List active caches."""
    client = get_client()

    try:
        caches = list(client.caches.list())
        if not caches:
            print("No active caches.")
            return

        print(f"{'Name':<45} {'Display Name':<25} {'Model':<30} {'Expires'}")
        print("-" * 130)
        for cache in caches:
            print(
                f"{cache.name:<45} {(cache.display_name or '-'):<25} "
                f"{(cache.model or '-'):<30} {cache.expire_time}"
            )

    except Exception as e:
        print(f"Error listing caches: {e}", file=sys.stderr)
        sys.exit(1)


def cmd_cache_get(args):
    """Show details for a single cache."""
    client = get_client()

    try:
        cache = client.caches.get(name=args.name)
        print(f"cache_name: {cache.name}")
        print(f"model: {cache.model}")
        print(f"display_name: {cache.display_name}")
        if cache.usage_metadata:
            print(f"token_count: {cache.usage_metadata.total_token_count}")
        print(f"create_time: {cache.create_time}")
        print(f"expires: {cache.expire_time}")

    except Exception as e:
        print(f"Error getting cache: {e}", file=sys.stderr)
        sys.exit(1)


def cmd_cache_extend(args):
    """Extend a cache's TTL."""
    client = get_client()
    from google.genai import types

    try:
        cache = client.caches.update(
            name=args.name,
            config=types.UpdateCachedContentConfig(ttl=f"{args.ttl}s"),
        )
        print(f"Extended. New expiry: {cache.expire_time}")

    except Exception as e:
        print(f"Error extending cache: {e}", file=sys.stderr)
        sys.exit(1)


def cmd_cache_delete(args):
    """Delete a cache."""
    client = get_client()

    try:
        client.caches.delete(name=args.name)
        print(f"Deleted: {args.name}")

    except Exception as e:
        print(f"Error deleting cache: {e}", file=sys.stderr)
        sys.exit(1)


# --- CLI ---


def main():
    parser = argparse.ArgumentParser(
        description="Direct Gemini API CLI with context caching and 2M token support.",
        formatter_class=argparse.RawDescriptionHelpFormatter,
    )
    subparsers = parser.add_subparsers(dest="command", required=True)

    # query
    p_query = subparsers.add_parser("query", help="Send a prompt to Gemini")
    p_query.add_argument("--model", required=True, help="Gemini model ID")
    p_query.add_argument("--files", nargs="+", help="Files/dirs/globs to include as context")
    p_query.add_argument("--prompt", help="The question/task (also accepts stdin)")
    p_query.add_argument("--cache", help="CachedContent name to use instead of --files")
    p_query.add_argument("--system", help="System instruction")
    p_query.add_argument("--temperature", type=float, default=0.2, help="Temperature (default 0.2)")
    p_query.set_defaults(func=cmd_query)

    # pack
    p_pack = subparsers.add_parser("pack", help="Pack files for inspection or piping")
    p_pack.add_argument("--files", nargs="+", required=True, help="Files/dirs/globs to pack")
    p_pack.add_argument("--repomix", action="store_true", help="Use repomix CLI instead of built-in walker")
    p_pack.add_argument("--output", help="Write to file instead of stdout")
    p_pack.set_defaults(func=cmd_pack)

    # cache subcommands
    p_cache = subparsers.add_parser("cache", help="Manage cached content")
    cache_sub = p_cache.add_subparsers(dest="cache_command", required=True)

    # cache create
    p_cc = cache_sub.add_parser("create", help="Create a cached content")
    p_cc.add_argument("--model", required=True, help="Model to bind cache to (immutable)")
    p_cc.add_argument("--files", nargs="+", required=True, help="Files/dirs/globs to pack and upload")
    p_cc.add_argument("--name", required=True, help="Display name for the cache")
    p_cc.add_argument("--ttl", type=int, default=3600, help="TTL in seconds (default 3600)")
    p_cc.add_argument("--system", help="System instruction to cache")
    p_cc.add_argument("--repomix", action="store_true", help="Use repomix for packing")
    p_cc.set_defaults(func=cmd_cache_create)

    # cache list
    p_cl = cache_sub.add_parser("list", help="List active caches")
    p_cl.set_defaults(func=cmd_cache_list)

    # cache get
    p_cg = cache_sub.add_parser("get", help="Show cache details")
    p_cg.add_argument("name", help="Cache name (cachedContents/...)")
    p_cg.set_defaults(func=cmd_cache_get)

    # cache extend
    p_ce = cache_sub.add_parser("extend", help="Extend cache TTL")
    p_ce.add_argument("name", help="Cache name (cachedContents/...)")
    p_ce.add_argument("--ttl", type=int, required=True, help="New TTL in seconds")
    p_ce.set_defaults(func=cmd_cache_extend)

    # cache delete
    p_cd = cache_sub.add_parser("delete", help="Delete a cache")
    p_cd.add_argument("name", help="Cache name (cachedContents/...)")
    p_cd.set_defaults(func=cmd_cache_delete)

    args = parser.parse_args()
    args.func(args)


if __name__ == "__main__":
    main()
