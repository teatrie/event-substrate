---
name: agent-team
description: Cost-effective multi-agent orchestration with model selection by complexity and escalation on failure.
---
# Agent Team Orchestration Protocol

You (the orchestrator) remain on the highest-capability model (Opus). When spawning subagents via the Task tool, follow these rules:

## Model Selection by Complexity

Before invoking a subagent, perform a **Tier Selection Analysis**:

| Complexity | Model | Examples |
|------------|-------|----------|
| Simple | `haiku` | Config changes, file searches, grep/glob, doc formatting, simple test fixes |
| Medium | `sonnet` | Standard code implementation, unit tests, refactoring, bug fixes with clear scope |
| Complex | `opus` | Architectural decisions, multi-file refactors, subtle bugs, novel integration work |

When in doubt, start one tier lower — it's cheaper to escalate than to overspend.

## Failure Escalation Protocol

1. A subagent must **stop after 3 failed attempts** at resolving errors or getting unstuck.
2. On stopping, it must return an **escalation report** (in its response message) containing:
   - The exact error or blocker
   - The 3 strategies attempted and why each failed
   - Its best hypothesis for the root cause
   - The exit phrase: `ESCALATION_REQUIRED: <one-line reason>`
3. The orchestrator reads the report and spawns a **new subagent at the next higher model tier**, passing the full escalation report as context so it doesn't start from zero.
4. If an Opus subagent still fails after 3 attempts, the orchestrator reports to the user with a full status summary.

## Subagent Instructions

Always include this directive in every subagent prompt:

> If you get stuck or encounter unresolved errors, stop after 3 failed attempts. Do not loop indefinitely. Return an escalation report with: (1) the exact error, (2) the 3 strategies you tried and why each failed, (3) your best hypothesis for the root cause. End your response with: `ESCALATION_REQUIRED: <one-line reason>`.

## Goal

Deliver the best solution at the lowest token cost. Prefer parallelism for independent tasks. Escalate promptly rather than burning tokens on repeated failures.
