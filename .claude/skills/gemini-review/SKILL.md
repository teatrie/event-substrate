---
name: gemini-review
description: Independent architectural review via Gemini. Checks domain boundaries, schema consistency, dependency drift, and structural compliance.
---
# Gemini System Architect Review

You are running an independent architectural review using Gemini as a structurally different reviewer. Gemini sees the forest (global consistency, structural compliance), Claude sees the tree (implementation correctness, test coverage).

**Input:** $ARGUMENTS

## Step 1: Detect Review Type

Parse `$ARGUMENTS` to determine the review mode:

| Prefix | Review Type | Protocol Section |
|--------|-------------|------------------|
| `plan:` | Plan Audit | CRITICAL — full domain decomposition review |
| `diff:` or `green:` | Boundary Guard | HIGH — cross-service structural compliance |
| `refactor:` | Idiom Check | MEDIUM — language idioms + duplication scan |
| `bug:` | Bug Diagnostic Cross-Validation | LOW — root cause agreement check |
| _(no prefix)_ | General Architectural Review | Combines Plan Audit + Boundary Guard checks |

## Step 2: Build Context

Assemble the `@` file references for `ask-gemini` based on review type. Always include project conventions:

**Base context (all review types):**
- `@CLAUDE.md` — project conventions, domain boundaries, deployment checklist
- `@docs/architecture.md` — data flow and architecture overview

**Additional context by type:**

| Review Type | Additional Context |
|-------------|-------------------|
| Plan Audit | `@docs/architecture/overview.mmd @avro/` + the plan text from arguments |
| Boundary Guard | `@avro/` + changed files (from arguments or `git diff --name-only`) |
| Idiom Check | Refactored files from arguments + broader repo context for duplication scan |
| Bug Diagnostic | Error/symptoms + relevant code files from arguments |
| General | `@docs/architecture/overview.mmd @avro/` + files from arguments |

## Step 3: Select Model and Run Review

Refer to the review type definitions in `.claude/skills/gemini-review/gemini-review-protocol.md` for model selection:

| Review Type | Model | Rationale |
|-------------|-------|-----------|
| Plan Audit | `gemini-3.1-pro-preview` | Architectural reasoning needs thinking mode |
| Boundary Guard | `gemini-2.5-flash` | Fast structural scan |
| Idiom Check | `gemini-2.5-flash` | Pattern matching, not deep reasoning |
| Bug Diagnostic | `gemini-2.5-flash-lite` | Quick sanity check |
| General | `gemini-2.5-flash` | Balanced cost/capability |

Call `ask-gemini` with the assembled context and the appropriate prompt template from the protocol document.

**Prompt structure:**
```
You are a System Architect reviewer for an event-driven microservices platform.
Your role is structural compliance, NOT line-level code review.

Review type: {TYPE}
Project conventions: {from @CLAUDE.md}

Check the following:
{checklist from protocol doc for this review type}

For each check, report:
- PASS: compliant
- ADVISORY: non-blocking suggestion
- FAIL: structural violation that must be addressed

{the actual content to review}
```

## Step 4: Validate Results

Before presenting findings, spot-check Gemini's output (per `/deep-research` Step 4 pattern):

- **File paths** — do referenced files actually exist? (`ls` or `glob` to verify)
- **Names** — do referenced topics, schemas, services match actual codebase names?
- **Convention claims** — do cited rules match what's in `CLAUDE.md`?

Flag any hallucinated references before presenting to the user.

## Step 5: Present Findings

Format the output as a findings table:

```
## Gemini Architectural Review — {Review Type}

Model: {model used}
Context: {files included}

| # | Check | Result | Finding |
|---|-------|--------|---------|
| 1 | Topic naming | PASS | — |
| 2 | Schema ownership | FAIL | `avro/public/media/...` missing for new topic |
| 3 | Cross-domain writes | ADVISORY | Consider extracting shared type |

### Summary
- PASS: N | ADVISORY: N | FAIL: N
- {Overall assessment}

### Action Items (FAIL only)
1. {What needs to change and why}
```

If any FAIL results exist, clearly state they should be addressed before proceeding.
If all PASS, note the clean bill of health.
ADVISORY items are informational — report but don't block.
