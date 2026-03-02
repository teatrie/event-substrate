---
name: gemini
description: Direct Gemini API access with context caching and 2M token support. Use for mega-context analysis and cached multi-review sessions.
---
# Gemini Direct API

Direct Gemini API access with context caching and 2M token support. Complements `ask-gemini` MCP (OAuth, free) for cases requiring explicit caching or >1M context.

**Input:** $ARGUMENTS

## When to Use `/gemini` vs `ask-gemini`

| Scenario | Tool | Auth | Why |
|----------|------|------|-----|
| One-off query, ≤1M context | `ask-gemini` MCP | OAuth (free) | Simpler, no API key needed |
| Multi-review epic (3+ reviews sharing context) | `gemini-api.py` | API key | 90% cache discount |
| Full codebase analysis (>1M tokens) | `gemini-api.py` | API key | Only `gemini-3.1-pro-preview` supports 2M |
| Cache management (create/list/delete) | `gemini-api.py` | API key | MCP has no caching |

## Setup

Requires `GEMINI_API_KEY` env var. Get a free key at [Google AI Studio](https://aistudio.google.com).

```bash
export GEMINI_API_KEY=your-key-here
```

No pip install needed — `uv run --with google-genai` handles dependencies.

## Parse Arguments

Parse `$ARGUMENTS` prefix to choose mode:

| Prefix | Mode | Action |
|--------|------|--------|
| `cache:create ...` | Cache management | Run cache create subcommand |
| `cache:list` | Cache management | Run cache list subcommand |
| `cache:delete ...` | Cache management | Run cache delete subcommand |
| `2m: <prompt>` | 2M mega-context | Auto-select `gemini-3.1-pro-preview`, pack full project |
| _(no prefix)_ | Direct query | Select cheapest model, run query |

## Mode 1: Direct Query (no prefix)

1. **Cost analysis** — assess query complexity, select cheapest effective model (see `/deep-research` Step 1 for model table).
2. **Identify context files** — determine which files/dirs are relevant.
3. **Execute:**

```bash
uv run --with google-genai .claude/scripts/gemini-api.py query \
  --model MODEL \
  --files FILE... \
  --prompt "the user's question"
```

4. **Validate** results per `/deep-research` Step 4 (spot-check file paths, names, claims).
5. **Present** findings to user.

## Mode 2: Cached Session (cache: prefix)

For multi-review epics where 3+ queries share the same context:

### Create cache
```bash
uv run --with google-genai .claude/scripts/gemini-api.py cache create \
  --model gemini-2.5-flash \
  --files CLAUDE.md docs/architecture.md avro/ \
  --name "epic-feature-name" \
  --ttl 7200
```

### Query against cache
```bash
uv run --with google-genai .claude/scripts/gemini-api.py query \
  --model gemini-2.5-flash \
  --cache "cachedContents/abc123" \
  --prompt "Check domain boundaries for the new processor"
```

### List/delete caches
```bash
uv run --with google-genai .claude/scripts/gemini-api.py cache list
uv run --with google-genai .claude/scripts/gemini-api.py cache delete cachedContents/abc123
```

Cache is model-locked — if the epic mixes models, create separate caches per model.

## Mode 3: 2M Mega-Context (2m: prefix)

For full-codebase analysis exceeding 1M tokens. Only `gemini-3.1-pro-preview` supports 2M context.

```bash
uv run --with google-genai .claude/scripts/gemini-api.py query \
  --model gemini-3.1-pro-preview \
  --files . \
  --prompt "the user's question"
```

If the packed context exceeds 2M tokens, scope `--files` to relevant subdirectories.

## Validate and Present

After any query mode:

1. **Spot-check** Gemini's output per `/deep-research` Step 4 — verify file paths, names, claims.
2. **Summarize** findings — don't dump raw output.
3. **Report token usage** from stderr (especially cached_tokens for cache sessions).
