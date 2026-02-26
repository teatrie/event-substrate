# TDD Protocol — Red-Green-Refactor with Git Safety Net

This protocol is referenced by both `/tdd-execute` and `/feature-epic` for the strict TDD loop.

## The 5-Step TDD Loop

### Step 1 — PRE: Record domain start SHA

```bash
DOMAIN_START_SHA=$(git rev-parse HEAD)
touch .tdd-active
```

### Step 2 — RED: Write failing tests

- Spawn `tdd-red` subagent with the domain plan
- Agent writes exhaustive tests, confirms they **FAIL**
- Tests must fail for the **RIGHT reason** (missing implementation, not syntax errors)

### Step 3 — GREEN: Make tests pass with minimal code

- Spawn `tdd-green` subagent with failing tests + target files
- Agent writes **minimum code** to pass ALL tests
- Confirm ALL tests pass

**Git checkpoint (unconditional):**
```bash
git add -A && git commit -m "chore: GREEN checkpoint — <domain>"
```

### Step 4 — REFACTOR: Clean the code (guarded by checkpoint)

**Refactor scope (priority order):**
1. Standards & conventions — match project patterns, language idioms
2. Simplicity — remove unnecessary complexity, dead code, premature abstractions
3. Readability & maintainability — clear naming, obvious flow
4. Performance — only for obvious inefficiencies, not speculative

**Constraints:**
- ONLY touch files written/modified in this domain
- Do NOT add abstractions, helpers, or "improvements" beyond scope
- Do NOT touch unrelated files

**Escalation chain:**

| Attempt | Agent | On Failure |
|---------|-------|------------|
| 1 | `tdd-refactor` (current model) | `git reset --hard HEAD` (back to GREEN checkpoint) |
| 2 | `tdd-refactor` (current model, different approach) | `git reset --hard HEAD` |
| 3 | `tdd-refactor` (escalate to HIGHER model) | `git reset --hard HEAD` — skip refactor, code is already green |

- Escalated agent gets **GREEN checkpoint code** + refactor criteria (NOT the failed diff)
- Tests pass → continue to Step 5
- All 3 fail → ship the GREEN code — working beats perfect

### Step 5 — POST: Soft reset and domain commit

```bash
rm .tdd-active
git reset --soft $DOMAIN_START_SHA
# All domain changes now staged (no checkpoint clutter)
git commit -m "feat(<domain>): <description>"
```

## Rules

- GREEN checkpoint is **UNCONDITIONAL** — always commit before refactor
- Never `--amend` the checkpoint — it's immutable until soft reset
- Escalated agent gets clean slate (GREEN code), not the failed attempt's diff
- If all 3 refactor attempts fail, ship the GREEN code — working beats perfect
