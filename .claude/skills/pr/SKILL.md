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
   ```
   Co-Authored-By: Claude <model> <noreply@anthropic.com>
   ```
   (Use the actual model name — Opus 4.6, Sonnet 4.6, or Haiku 4.5)

If there are only unpushed commits (clean working tree):
1. Create a branch from HEAD: `git checkout -b pr/<short-name>`
   (The branch carries the unpushed commits)

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

## Step 4: Wait for CI

Ask the user what to do next using AskUserQuestion:
- **"Wait for CI, then merge"** — poll CI status with `gh pr checks <number> --watch`, then merge on success
- **"Merge now"** — merge immediately without waiting (user has verified manually)
- **"Leave open"** — leave the PR open for manual review

### If "Wait for CI, then merge":
```bash
gh pr checks <number> --watch --fail-fast
```
- If all checks pass: `gh pr merge <number> --merge --delete-branch`, then `git checkout main && git pull`
- If any check fails: report the failure, do NOT merge, suggest the user inspect the logs

### If "Merge now":
`gh pr merge <number> --merge --delete-branch`, then `git checkout main && git pull`

### If "Leave open":
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
