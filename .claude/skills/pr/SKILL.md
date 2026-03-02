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

## Step 4: CI Check & Merge

### Step 4a: Determine if CI will run

Check whether the changed files match any CI path filter that triggers build/test jobs. The CI path filters are defined in `.github/workflows/ci.yml` under `dorny/paths-filter`. The current trigger paths are:

- `go-services/**` — Go tests + Docker builds
- `pyspark_apps/**`, `Dockerfile.spark.base` — Spark tests + Docker builds
- `pyflink_jobs/**`, `Dockerfile.pyflink` — PyFlink Docker build
- `charts/**` — Helm lint
- `flink_jobs/**` — Flink SQL changes

Files that trigger **lint-only** CI jobs (no build/test, but still must pass):

- `**/*.md` (triggers `lint-markdown` via `any-docs` filter) — includes `docs/`, `.claude/skills/`, `README.md`, `CLAUDE.md`
- `.markdownlint-cli2.yaml` (triggers `lint-markdown`)
- `docs/architecture/*.mmd` (triggers `lint-mermaid` via `any-mermaid` filter)

Files that do NOT trigger any CI job (truly CI-silent):

- `avro/**` (schemas — no CI job yet)
- `airflow/**` (DAGs — no CI job yet)
- `supabase/**` (migrations — no CI job yet)
- `scripts/**`, `telemetry/**`, `kubernetes/**` (infra config)
- Root config files (`Taskfile.yml`, `docker-compose.yml`, etc.)
- `.claude/**` non-markdown files (hooks, scripts, settings)

Compare the list of changed files against the trigger paths. If **none** of the changed files match a trigger path, CI will only run the `detect-changes` job (~5 seconds) and skip all build/test/push jobs.

### Step 4b: Ask user

**If CI will run** (changed files match trigger paths), ask the user:

- **"Wait for CI, then merge"** — poll CI status, then merge on success
- **"Merge now"** — merge immediately without waiting
- **"Leave open"** — leave the PR open for manual review

**If only lint CI jobs will run** (only markdown/mermaid files changed), tell the user:
> "Only lint jobs will trigger for these changes. The pre-push lint gate already validated them. Merging directly."

Then merge immediately: `gh pr merge <number> --merge --delete-branch`

**If CI is truly silent** (no trigger paths matched at all), tell the user:
> "No CI jobs will trigger for these changes. Merging directly."

Then merge immediately: `gh pr merge <number> --merge --delete-branch`

### If "Wait for CI, then merge"

```bash
gh pr checks <number> --watch --fail-fast
```

- If all checks pass: `gh pr merge <number> --merge --delete-branch`, then `git checkout main && git pull`
- If any check fails: report the failure, do NOT merge, suggest the user inspect the logs

### If "Merge now"

`gh pr merge <number> --merge --delete-branch`, then `git checkout main && git pull`

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
