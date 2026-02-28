---
name: agent-team
description: Cost-effective multi-agent orchestration with model selection by complexity and escalation on failure.
---
# Agent Team Orchestration Protocol

You (the orchestrator) remain on the highest-capability model (Opus). When spawning subagents via the Task tool, follow these rules:

## Orchestrator Role

The orchestrator (you) is a **pure coordinator and reviewer**. You must NEVER execute implementation tasks directly — always spawn a subagent via the Task tool for any work (code, tests, research, file edits, docs, infra, config). Even "quick" fixes like bug patches, YAML edits, or doc updates must go to a subagent. Your responsibilities are limited to: planning, spawning subagents with appropriate prompts, reviewing their output, and escalating failures.

## Subagent Permissions

**Always set `mode: "bypassPermissions"` on every Task tool invocation.** Without this, subagents will prompt the user for approval on file writes/edits, breaking autonomous execution. This applies to all `subagent_type` values (`general-purpose`, `tdd-red`, `tdd-green`, `tdd-refactor`, `Explore`, `Plan`, etc.).

## Model Self-Check

Before starting any planning or execution phase, verify which model you are running on. If you are NOT on the highest-capability model (currently Opus 4.6 / `claude-opus-4-6`), **stop and remind the user** to switch you to Opus before proceeding. The orchestrator and planner must always run on the highest-capability model for reliable coordination and review.

---

## Execution Framework

### Step 1: Systems Analysis

When given a feature request (or a set of remaining domains from an epic), analyze:

1. **Domain inventory:** List each distinct architectural component that needs work (e.g., Go service, PyFlink job, frontend, SQL migration, Avro schemas).
2. **Language/service boundaries:** Identify which domains are in isolated codebases or languages.
3. **Dependency graph:** Map which domains produce artifacts consumed by others (e.g., schemas, events, APIs).
4. **Shared-file conflicts:** Flag domains that modify the same files or packages.
5. **Deployment inventory:** For every new or modified service/image, verify the full deployment chain exists:
   - Dockerfile → `build:*` task in Taskfile → K8s manifest in `kubernetes/` → wired into `task init`/`task start`
   - If ANY link is missing, add a **deployment domain** to the plan. Code that passes local build/test but has no deployment path is incomplete.
   - If an existing Dockerfile is modified (e.g., adding a pip/apt dependency), the image must be rebuilt — local test passing does NOT validate the container.

### Step 2: Team Formation (Dynamic)

Based on the systems analysis, organize work into **waves** of parallel teams:

**Wave assignment rules:**

- Domains with **no cross-dependencies** and **no shared files** can run in the same wave (parallel).
- A domain that **consumes output from another domain** must be in a later wave (sequential).
- **Test domains are mandatory and always in the final implementation wave** (they require all code complete):
  - **E2E pipeline tests** (`tests/e2e/`): Backend end-to-end tests validating the full event flow (API → Kafka → Flink → DB).
  - **Browser integration tests** (Playwright): If the feature has any user-facing UI changes, Playwright tests must exercise the actual browser UX with real backend data flowing through. Tests must drive the full user journey — signup, wait for Realtime-driven UI updates (credits, notifications), trigger actions (upload, download, delete), and assert on durable outcomes (file appears/disappears in media browser, credit count changes). **Anti-patterns to avoid:** asserting on source code strings, checking only static DOM structure without data, or testing with mocked fetch — none of these catch Realtime integration bugs or notification matching issues.
- Documentation is always last.

**Team structure:**

- **1 team per domain** within a wave. Each team gets a strict domain role (e.g., "Go media-service handler", "PyFlink processor", "Frontend component").
**TeamCreate is MANDATORY for parallel work:**
- ALWAYS call `TeamCreate` before spawning any parallel teammates.
- Spawn teammates via `Task` with the `team_name` parameter — this gives each agent its own tmux window, providing the user visibility into agent progress.
- NEVER use bare `Task` with `run_in_background` for parallel orchestration — those agents run invisibly with no user monitoring capability.
- For sequential single-domain work (1 subagent at a time), plain `Task` is acceptable.
- Each teammate runs its own R-G-R loop independently.

**When NOT to use teams:**

- If only 1 domain remains, use a single sequential subagent (no team overhead).
- If all remaining domains are tightly coupled (shared files, sequential dependencies), run them sequentially with one subagent at a time.

### Step 3: Enforce the R-G-R Loop

Each team (or sequential subagent) must execute strict Red-Green-Refactor:

1. **RED:** Spawn `tdd-red` subagent → write exhaustive failing tests. Run the test runner and verify failure. Report back to orchestrator.
2. **GREEN:** Spawn `tdd-green` subagent → write minimum implementation to make tests pass. Run the test runner and verify all green. Report back.
3. **REFACTOR:** Spawn `tdd-refactor` subagent → clean up code, maintain passing tests. Run full test suite. Report back.

**Do not ask the user for permission between R-G-R phases.** The orchestrator autonomously drives RED → GREEN → REFACTOR for each domain.

### Step 4: Synchronization

**Cross-team contracts:**

- Before Wave 1 begins, all shared contracts (Avro schemas, Protobuf definitions, interface types) must be locked. If schemas were defined in an earlier domain (e.g., D1), they are already locked.
- If a wave requires new cross-domain contracts, define them first as a pre-wave task before any team starts RED.

**Wave gating:**

- A wave does NOT start until ALL teams in the previous wave have completed their R-G-R loops.
- The orchestrator verifies each team's completion (all tests green, refactor done) before advancing to the next wave.

**Intra-domain parallelism:**

- Within a single domain, independent sub-tasks (e.g., download handler vs delete handler) may run RED in parallel if they produce **separate test files**.
- GREEN and REFACTOR for sub-tasks within the same package should run **sequentially** to avoid file conflicts.

### Step 5: Regression Gate

After ALL waves are complete, run the **full existing test suite** — not just the new tests:

1. Run all unit/integration test suites across every service that was touched or that depends on changed infrastructure (Flink SQL, Kafka topics, shared schemas).
2. If `task test:e2e` is available, run it. E2E tests validate end-to-end flows that unit tests cannot cover.
3. **If any pre-existing test fails**, treat it as a regression. Diagnose root cause before declaring complete.

**Critical rule:** Subagents must NEVER weaken or remove pre-existing test assertions to make their changes pass. If an existing test contradicts the new design, the subagent must flag it to the orchestrator for review — not silently update the assertion.

---

## Model Selection by Complexity

Before invoking a subagent, perform a **Tier Selection Analysis**:

| Complexity | Model | Examples |
|------------|-------|----------|
| Simple | `haiku` | Config changes, file searches, grep/glob, doc formatting, simple test fixes, refactoring |
| Medium | `sonnet` | Standard code implementation, unit tests, bug fixes with clear scope |
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
