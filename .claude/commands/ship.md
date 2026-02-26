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

### Step 2: Group into PRs

Group files into logical PRs based on:

1. **Domain cohesion** — files in the same architectural domain together (e.g., all Flink SQL changes, all Go service changes, all frontend changes)
2. **Functional cohesion** — files that implement the same logical change together (e.g., a feature + its test + its docs update)
3. **Dependency order** — PRs that must merge before others (e.g., Avro schemas before the service consuming them, infra/config before app code)

Keep the number of PRs reasonable (2-6 typically). Don't over-split — a PR with 3-5 related files is better than 5 PRs with 1 file each.

### Step 3: Present the Plan

Output a numbered plan like:

```
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
- "Looks good, proceed"
- "I want to adjust the grouping" (let the user describe changes, then re-plan)

**Do NOT create any branches or PRs until the user approves.**

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
```
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

```
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

- **Never auto-merge.** Create PRs only — the user reviews and merges.
- **Never force-push.** If something goes wrong, report it and stop.
- **Clean up on failure.** If a step fails mid-execution, report what was created so the user can clean up.
- **Return to main.** After all PRs are created, `git checkout main` so the working tree is back to the starting branch.
- **Respect user hints.** If `$ARGUMENTS` contains grouping preferences (e.g., "keep docs separate", "group all infra"), honor them.
