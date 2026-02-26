---
name: feature-epic
description: Acts as a Product Manager to break down a large feature into architectural domains and executes the TDD loop for each.
---
# Epic Feature Orchestration

You are acting as the Lead Technical Product Manager and Orchestrator. I am requesting a new multi-component feature: 
$ARGUMENTS

Do not write any implementation code yourself. Execute this in two distinct phases:

## PHASE 1: Domain Decomposition & Planning
1. Break the requested feature down into strictly isolated architectural domains based on the logical boundaries of the system (e.g., Flink job, Kafka consumer, database schema).
2. For each domain, write a brief implementation plan and explicitly define the data contracts/interfaces between them.
3. **Mandatory test domains** — the following must always be included as explicit domains in the plan (not afterthoughts):
   - **E2E pipeline tests** (`tests/e2e/`): Backend end-to-end tests validating the full event flow (API → Kafka → Flink → DB). These use Node.js scripts against a running platform.
   - **Browser integration tests** (Playwright): If the feature has any user-facing UI changes, include a domain for Playwright tests that exercise the actual browser UX — DOM states, loading/waiting indicators, notification rendering, error handling, and the complete user journey. Unit tests with mocked fetch are not a substitute for browser-level validation.
4. **Mandatory deployment domain** — if ANY domain introduces a new service, Docker image, or deployable artifact, the plan MUST include a deployment domain covering:
   - **Dockerfile**: Exists and builds cleanly
   - **Docker build task**: Entry in `Taskfile.yml` (e.g., `build:my-service`) following existing patterns, wired into the `build` aggregate task
   - **K8s manifest**: Deployment (+ Service if needed) YAML in `kubernetes/`, with all env vars from config. Must use `imagePullPolicy: Never` for local dev (OrbStack shares Docker daemon — `Never` uses local images directly, `Always` fails on registry pull, `IfNotPresent` caches stale images)
   - **Taskfile wiring**: Build task runs before deployment (`k8s:*` depends on `build:*`), and both are reachable from `task init`/`task start`
   - **Image rebuild for modified Dockerfiles**: If an existing Dockerfile is changed (e.g., adding a pip dependency), the image MUST be rebuilt — local `go build` or `pytest` passing does NOT validate the container
   - Code is not complete until it can be deployed with `task init` and runs as a pod. Local build/test alone is insufficient.
5. **STOP AND ASK FOR APPROVAL.** Present the domains and contracts to me. Do not proceed to Phase 2 until I explicitly approve.

## PHASE 2: Sequential TDD Execution
Once I approve the Phase 1 plan, you will execute the implementation for EACH domain sequentially. Do not mix contexts between domains.

**Execution:**
For the first domain, read the standard operating procedure located at `.claude/skills/tdd-execute/tdd-protocol.md` and execute the 5-step loop. 

Once the first domain is completely finished (Step 5 is complete), repeat the exact loop for the next domain, until all planned domains are implemented.

## PHASE 3: Cold-Start Regression Gate

After ALL domains are implemented, run the **full cold-start regression** to catch both code regressions and deployment regressions in a single pass:

1. Run all unit/integration test suites across every service that was touched or that depends on changed infrastructure (Flink SQL, Kafka topics, shared schemas).
2. Run `task test:cold-start`. This purges all state, rebuilds the platform from scratch (`task clean && task purge && task init`), runs all E2E pipeline tests (`task test:e2e` with warmup gate), then runs all Playwright browser tests (`task test:browser`). This validates:
   - `task init` still works after all changes (Dockerfiles, configmaps, K8s manifests)
   - The Flink warmup gate passes (all pipeline checkpoints initialize correctly)
   - All 13+ E2E tests pass against a cold platform (not just a warm dev environment)
   - All browser UI tests pass (frontend renders correctly with the new backend changes)
3. **If any pre-existing test fails**, treat it as a regression bug. Diagnose the root cause before declaring the epic complete. A subagent modifying shared infrastructure (e.g., Flink SQL, Kafka schemas, shared configs) must verify that ALL consumers of that infrastructure still work, not just the new feature's consumers.

**Critical rule:** Subagents must NEVER weaken or remove pre-existing test assertions to make their changes pass. If an existing test contradicts the new design, the subagent must flag it to the orchestrator for review — not silently update the assertion.
