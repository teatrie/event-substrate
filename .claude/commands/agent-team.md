---
name: agent-team
description: Cost-effective multi-agent orchestration with model selection by complexity and escalation on failure.
---
# Agent Team Orchestration Protocol

You (the orchestrator) remain on the highest-capability model (Opus). When spawning subagents via the Task tool, follow these rules:

## Model Selection by Complexity

Evaluate each subagent task and assign the most cost-effective model:

| Complexity | Model | Examples |
|------------|-------|----------|
| Simple | `haiku` | Config changes, file searches, grep/glob, doc formatting, simple test fixes |
| Medium | `sonnet` | Standard code implementation, unit tests, refactoring, bug fixes with clear scope |
| Complex | `opus` | Architectural decisions, multi-file refactors, subtle bugs, novel integration work |

When in doubt, start one tier lower — it's cheaper to escalate than to overspend.

## Failure Escalation Protocol

1. A subagent must **stop after 3 failed attempts** at resolving errors or getting unstuck.
2. On stopping, it must report back with: what it tried, what failed, and its best hypothesis.
3. The orchestrator then spawns a **new subagent at the next higher model tier** with the failure context.
4. If an Opus subagent still fails after 5 attempts, the orchestrator reports to the user with a full status summary.

## Subagent Instructions

When spawning any subagent, always include this directive in the prompt:

> If you get stuck or encounter unresolved errors, stop after 3 failed attempts. Report back with: (1) what you tried, (2) what failed, (3) your best hypothesis for the root cause. Do not loop indefinitely.

## Goal

Deliver the best solution at the lowest token cost. Prefer parallelism for independent tasks. Escalate promptly rather than burning tokens on repeated failures.
