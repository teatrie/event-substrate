---
name: pr
description: Push current changes to a branch, open a PR, and wait for CI before merging.
---
# PR — Push, Verify, Merge

Single-purpose skill: push uncommitted and/or unpushed changes to a branch, open a PR against `main`, and optionally wait for CI to pass before merging.

Unlike `/ship`, this does NOT group changes into multiple PRs. It treats the working tree as a single unit.

**User hints:** $ARGUMENTS

---

## Step 1: Inventory

Run `git status` and `git log --oneline origin/main..HEAD` to understand:

- Are there uncommitted changes?
- Are there unpushed commits?
- What's the total diff?

## Step 2: Branch & Commit

If there are uncommitted changes:

1. Create a branch: `git checkout -b pr/<short-name>` (derive name from changes or user hint)
2. Stage relevant files (`git add` — never add secrets, binaries, or cache dirs)
3. Commit with a conventional commit message ending with:

   ```text
   Co-Authored-By: Claude <model> <noreply@anthropic.com>
   ```

   (Use the actual model name — Opus 4.6, Sonnet 4.6, or Haiku 4.5)

If there are only unpushed commits (clean working tree):

1. Create a branch from HEAD: `git checkout -b pr/<short-name>`
   (The branch carries the unpushed commits)

## Step 2b: Pre-Push Lint Gate

Before pushing, run lint checks relevant to the changed files:

1. **If any `*.md` files changed:** `task lint:markdown`
2. **If any `docs/architecture/*.mmd` files changed:** `task lint:mermaid`
3. **If any Go files changed:** `task lint:go`
4. **If any Python files changed:** `task lint:python:flink` and/or `task lint:python:spark` (match the runtime)
5. **If any frontend files changed:** `task lint:frontend`
6. **If any YAML files changed:** `task lint:yaml`
7. **If any K8s manifests changed:** `task lint:k8s`

If any lint check fails, **fix the issues before proceeding**. Do not push broken files.

## Step 3: Push & Create PR

```bash
git push -u origin pr/<short-name>
```

Create the PR:

```bash
gh pr create --base main --title "<title>" --body "$(cat <<'EOF'
## Summary
<1-3 bullet points>

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

## Step 4: Wait for CI & Merge

Every PR triggers the `CI Gate` required status check (aggregates all conditional lint/test/type-check jobs). Always wait for it.

```bash
gh pr checks <number> --watch --fail-fast
```

**If CI passes**, ask the user:

- **"Merge"** — merge and clean up
- **"Leave open"** — leave the PR open for manual review

**If CI fails**, report the failure. Do NOT merge. Suggest the user inspect the logs.

### If "Merge"

```bash
gh pr merge <number> --merge --delete-branch
git checkout main && git pull
```

### If "Leave open"

Report the PR URL and return to `main`: `git checkout main`

## Step 5: Clean Up

After merge (if applicable):

```bash
git checkout main
git pull origin main
```

Confirm clean state with `git status`.

---

## Rules

- **Never push directly to main.** Always use a branch + PR.
- **Never force-push.**
- **CI awareness:** Check repo visibility (`gh api /repos/{owner}/{repo} --jq '.private'`). If private, warn about Free tier CI minute limits before merging.
- **Return to main** after completion.
- **Respect user hints** in `$ARGUMENTS` for branch name or PR title.
