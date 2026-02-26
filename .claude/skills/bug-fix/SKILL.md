---
name: bug-fix
description: Orchestrated bug/issue diagnosis and fix with cost-effective model selection, review gates, and domain-aware execution.
---
# Bug Fix Orchestration Protocol

You (the orchestrator) remain on the highest-capability model (Opus). Your role is **pure coordination and review** — you must NEVER diagnose or fix bugs directly. All work is delegated to subagents via the Task tool.

**Always set `mode: "bypassPermissions"` on every Task tool invocation.**

## Model Self-Check

Before starting, verify you are on the highest-capability model (currently Opus 4.6). If not, **stop and remind the user** to switch to Opus before proceeding.

---

## PHASE 1: Triage & Diagnostic

When a bug or issue is reported (error output, failing test, unexpected behavior):

### Step 1: Assess Diagnostic Complexity

Before spawning the diagnostic subagent, classify the investigation:

| Complexity | Model | Indicators |
|------------|-------|------------|
| Simple | `haiku` | Single file, obvious error message, config typo, missing import |
| Medium | `sonnet` | Multi-file trace needed, unclear root cause, requires reading 3-5 files |
| Complex | `opus` | Cross-service issue, race condition, architectural regression, requires understanding data flow across domains |

When in doubt, start one tier lower — it's cheaper to escalate than to overspend.

### Step 2: Spawn Diagnostic Subagent

Spawn a **read-only** subagent (`subagent_type: "Explore"`) with:
- The full bug report (error message, test output, user description)
- Instructions to investigate root cause WITHOUT making any code changes
- The escalation directive (see below)

The diagnostic subagent must return:
1. **Root cause** — the exact file(s), line(s), and mechanism causing the failure
2. **Impact scope** — which domains/services/tests are affected
3. **Regression source** — what change introduced the bug (git blame/diff if applicable)
4. **Proposed fix** — a clear description of what needs to change (not the code itself)

### Step 3: Review Analysis

You (the orchestrator) review the diagnostic report for soundness:
- Does the root cause explain the observed symptoms?
- Is the impact scope complete (no missed side effects)?
- Is the proposed fix minimal and correct?

**If the analysis is sound and the fix is low-risk** (single file, obvious correction, no architectural implications): present the analysis to the user with a recommendation to proceed, then move to Phase 2 upon approval.

**If the analysis is uncertain, high-risk, or architecturally significant** (cross-domain changes, schema modifications, breaking changes): present the analysis to the user and **wait for explicit approval** before proceeding.

**If the analysis is unsound**: escalate to a higher-tier diagnostic subagent with the previous report as context.

---

## PHASE 2: Fix Planning & Execution

Once the user approves proceeding with the fix:

### Step 4: Assess Fix Complexity

Classify the fix into one of:

**Single-domain fix** (one subagent):
- Fix is contained to 1 file or 1 tightly-coupled set of files
- No cross-service implications
- Spawn a single `general-purpose` subagent at the appropriate model tier

**Multi-domain fix** (agent team with waves):
- Fix spans multiple services, languages, or architectural layers
- Requires coordinated changes (e.g., schema + handler + test + config)
- Follow the `/agent-team` protocol: domain decomposition → wave assignment → R-G-R per domain

### Step 5: Execute Fix

**For single-domain fixes:**
1. Spawn a fix subagent with the diagnostic report, proposed fix, and R-G-R instructions
2. The subagent must: write/update failing tests (RED) → implement the fix (GREEN) → refactor (REFACTOR)
3. Review the result — verify all tests pass and the fix addresses the root cause

**For multi-domain fixes:**
1. Decompose into domains with explicit dependency ordering
2. Organize into waves (independent domains parallel, dependent domains sequential)
3. Spawn per-domain subagents at cost-effective model tiers
4. Gate each wave on completion of the previous wave
5. Each domain follows R-G-R: failing test → fix → refactor

### Step 6: Verification

After all fix subagents complete:
1. Verify the originally reported test/error now passes
2. Verify no regressions in related test suites
3. Report the fix summary to the user

---

## Model Selection for Fix Subagents

| Fix Type | Model | Examples |
|----------|-------|---------|
| Simple | `haiku` | Update a count assertion, fix a config value, restore a deleted line |
| Medium | `sonnet` | Restore removed SQL statements, fix handler wiring, update test expectations |
| Complex | `opus` | Architectural fixes spanning multiple services, subtle race conditions, schema migrations |

## Failure Escalation Protocol

Same as `/agent-team`:
1. Subagent stops after **3 failed attempts**
2. Returns escalation report: exact error, 3 strategies tried, root cause hypothesis
3. Orchestrator spawns new subagent at **next higher model tier** with full context
4. If Opus subagent fails after 3 attempts, report to user with full status

## Subagent Instructions

Always include this directive in every subagent prompt:

> If you get stuck or encounter unresolved errors, stop after 3 failed attempts. Do not loop indefinitely. Return an escalation report with: (1) the exact error, (2) the 3 strategies you tried and why each failed, (3) your best hypothesis for the root cause. End your response with: `ESCALATION_REQUIRED: <one-line reason>`.

## Goal

Diagnose accurately, fix minimally, verify thoroughly — at the lowest token cost. The diagnostic phase is cheap insurance against wasted fix attempts.
