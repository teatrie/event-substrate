---
name: ship
description: Group dirty working tree into sequential, logically-cohesive PRs and create them via gh CLI.
---
# Ship — Group & PR Uncommitted Changes

You are a single subagent (Sonnet-tier). Your job is to analyze the dirty working tree, group changes into logical PRs, get user approval, then create the PRs sequentially.

**User hints:** $ARGUMENTS

---

## PHASE 1 — Analysis & Grouping Plan

### Step 1: Inventory Changes

Run `git status` and `git diff --stat` to see all modified, untracked, and deleted files. For each changed file, read the diff (`git diff <file>` for tracked files, `cat` for untracked) to understand the nature of the change.

### Step 1b: Ignore File Audit

Before grouping, perform a two-part audit:

**Part A — Check untracked files against known patterns:**

- **Compiled binaries** (Go binaries, `.pyc`, `node_modules/`, `dist/`) — must be in `.gitignore`
- **Cache/temp directories** (`.pytest_cache/`, `__pycache__/`, `.task/`, `tmp/`, `temp/`) — must be in `.gitignore`
- **Auto-generated agent state** (`.claude/projects/`, `.claude/history/`) — must be in `.gitignore`
- **Large non-essential files** (`go.sum`, vendor locks, binary assets) — should be in `.claudeignore` (context optimization, still tracked by git)

**Part B — Scan the repo for new artifact types introduced by the current changes:**

Run a broad search for common build/runtime artifacts that may not yet be in the ignore files:

```bash
# Find artifacts that might need ignoring
find . -maxdepth 4 \( \
  -name "*.egg-info" -o -name "*.pyc" -o -name "__pycache__" -o \
  -name ".pytest_cache" -o -name "*.class" -o -name "*.jar" -o \
  -name "derby.log" -o -name "metastore_db" -o -name "spark-warehouse" -o \
  -name "*.log" -o -name "*.tmp" -o -name "*.swp" -o \
  -name ".ivy2" -o -name ".m2" -o -name "target" -o \
  -name "*.o" -o -name "*.so" -o -name "*.dylib" \
\) -not -path "./.git/*" 2>/dev/null
```

For each artifact found, verify it's covered by `.gitignore`. If a new technology was introduced (e.g., Spark, Airflow, Marquez, a new language runtime), check for its common artifacts:

| Technology | Common artifacts to ignore |
|-----------|--------------------------|
| PySpark | `derby.log`, `metastore_db/`, `spark-warehouse/`, `*.egg-info/` |
| Java/Maven | `target/`, `.m2/`, `*.class`, `*.jar` (local builds) |
| Airflow | `airflow.db`, `airflow-webserver.pid`, `logs/` |
| Docker | `*.tar`, dangling layers (not in repo, but check for exported images) |
| Terraform/Pulumi | `.terraform/`, `*.tfstate`, `Pulumi.*.yaml` (secrets) |

If any artifact is present but not ignored, add the pattern to `.gitignore` (if it should never be tracked) or `.claudeignore` (if tracked by git but wasteful for AI context). Include ignore file updates in the first PR or a dedicated housekeeping PR.

### Step 2: Group into PRs

Group files into logical PRs based on:

1. **Domain cohesion** — files in the same architectural domain together (e.g., all Flink SQL changes, all Go service changes, all frontend changes)
2. **Functional cohesion** — files that implement the same logical change together (e.g., a feature + its test + its docs update)
3. **Dependency order** — PRs that must merge before others (e.g., Avro schemas before the service consuming them, infra/config before app code)

Keep the number of PRs reasonable (2-6 typically). Don't over-split — a PR with 3-5 related files is better than 5 PRs with 1 file each.

### Step 3: Present the Plan

Output a numbered plan like:

```text
PR 1: "feat(schemas): add media upload/download Avro schemas"
  Files: avro/public/media/upload.approved.avsc, avro/public/media/upload.rejected.avsc, ...
  Depends on: (none — merges first)

PR 2: "feat(media-service): implement media upload handler"
  Files: go-services/media-service/main.go, go-services/media-service/handler.go, ...
  Depends on: PR 1

PR 3: ...
```

### Step 4: Wait for Approval

**Stop and ask the user** to confirm the grouping using the AskUserQuestion tool. Present clear options:

- "Looks good, proceed and merge" — create PRs and merge them sequentially (delete branch after each merge)
- "Looks good, create PRs only" — create PRs but do not merge
- "I want to adjust the grouping" (let the user describe changes, then re-plan)

**Do NOT create any branches or PRs until the user approves.**

**If the user selects "proceed and merge":** after creating each PR, immediately merge it with `gh pr merge <number> --merge --delete-branch`, then pull `origin/main` before branching for the next PR. This collapses the stacked-PR complexity — each PR branches from and merges to `main` in sequence.

---

## PHASE 2 — Sequential Branch/Commit/PR Creation

For each PR **in dependency order**:

### Step 1: Create Branch

Branch from the **tip of the previous PR's branch** (or from `main` for the first PR). This keeps each PR's diff clean — it only shows that group's changes.

```bash
git checkout -b ship/<short-name> <base>
```

Use the prefix `ship/` for all branches (e.g., `ship/media-schemas`, `ship/media-service`).

### Step 2: Stage & Commit

Stage **only** the files belonging to this PR group. For untracked files, `git add <file>`. For modifications, `git add <file>`. Write a descriptive conventional commit message.

End every commit message with:

```text
Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>
```

### Step 3: Push & Create PR

```bash
git push -u origin ship/<short-name>
```

Create the PR with `gh pr create`. Set the **base branch** to the previous PR's branch (or `main` for the first PR). This ensures each PR only shows its own diff.

Use this format:

```bash
gh pr create --base <base-branch> --title "<title>" --body "$(cat <<'EOF'
## Summary
<1-3 bullet points describing what this PR contains>

## Merge Order
This is PR N of M. Merge in order: PR 1 → PR 2 → ... → PR N.

<If this depends on a previous PR, note: "Depends on #<number>">

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

### Step 4: Record PR URL

Save each PR's URL and number for the summary and for setting base branches on subsequent PRs.

---

## PHASE 3 — Summary

After all PRs are created, output a final summary:

```text
## Ship Complete 🚢

Merge these PRs in order:

1. #101 — feat(schemas): add media Avro schemas
   https://github.com/user/repo/pull/101
2. #102 — feat(media-service): implement upload handler
   https://github.com/user/repo/pull/102
3. ...

After merging PR 1, update PR 2's base to `main` (GitHub may do this automatically).
```

---

## Rules

- **Only merge if the user chose "proceed and merge".** If they chose "create PRs only", never merge.
- **Never force-push.** If something goes wrong, report it and stop.
- **Clean up on failure.** If a step fails mid-execution, report what was created so the user can clean up.
- **Return to main.** After all PRs are created (and merged, if applicable), `git checkout main` so the working tree is back to the starting branch.
- **Respect user hints.** If `$ARGUMENTS` contains grouping preferences (e.g., "keep docs separate", "group all infra"), honor them.
- **Audit ignore files.** Never commit compiled binaries, cache dirs, or agent state. If `.gitignore` or `.claudeignore` need updates, include them in the first PR.
- **CI minutes awareness.** Before presenting the plan, check repo visibility (`gh api /repos/{owner}/{repo} --jq '.private'`). If the repo is **private**, warn the user: "This repo is private — GitHub Free tier has 2,000 CI minutes/month. Each merged PR triggers CI builds. Consider batching PRs (max 1-2 merges/day) or choosing 'create PRs only' and merging them together." If public, no warning needed (unlimited free minutes).
