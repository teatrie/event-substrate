# CONDITIONAL DIRECTIVE: TDD ORCHESTRATION (/tdd-execute)

**DEFAULT BEHAVIOR:** Act as a standard, helpful coding assistant. You may write implementation code directly unless the user explicitly invokes the TDD macro.

**THE TRIGGER:**
If the user's prompt contains the exact string `/tdd-execute`, you MUST instantly switch into Strict TDD Mode and follow the Red-Green-Refactor protocol below. You are forbidden from writing implementation code first.

## Strict TDD Protocol (Only active when triggered)

**Phase 0: The Task List Artifact**

1. Enter Planning Mode and generate a Task List Artifact separating the Red, Green, and Refactor steps. Await user approval.

**Phase 1: RED (Test Writing)**

1. Write the tests for the requested feature. Do NOT write any implementation logic.
2. Execute the test suite in the integrated terminal.
3. **HALT CONDITION:** Verify the test FAILS in the terminal output. Attach the failing logs to the Artifact.

**Phase 2: GREEN (Implementation)**

1. Write the absolute minimal code required to make the failing test pass.
2. Execute the test suite again.
3. **HALT CONDITION:** Verify the test PASSES. Attach the passing logs to the Artifact.

**Phase 3: REFACTOR (Cleanup)**

1. Refactor the code for idiomatic style (e.g., Effective Go, Python PEP 8) and performance.
2. Run the tests one final time to ensure they remain GREEN.

**VIOLATION PROTOCOL:**
If the user comments "TDD Violation" on your Artifact, immediately revert your last change and return to Phase 1.

---

# CONDITIONAL DIRECTIVE: FEATURE EPIC (/feature-epic)

**THE TRIGGER:** If the user's prompt contains `/feature-epic`, switch into Epic Orchestration Mode. You are forbidden from writing implementation code until Phase 1 is approved.

## Phase 1: Domain Decomposition (requires approval before proceeding)

1. Break the feature into strictly isolated architectural domains based on logical system boundaries (e.g., Kafka schema, Go handler, frontend, E2E tests).
2. For each domain, write a brief implementation plan and define data contracts/interfaces between them.
3. **E2E tests must be an explicit domain** — never deferred to after implementation.
4. **STOP:** Present the domain plan to the user. Do not proceed until explicitly approved.

## Phase 2: Sequential TDD Execution

For each domain in order, execute the `/tdd-execute` protocol (Red → Green → Refactor). Complete one domain fully before starting the next.

---

# CONDITIONAL DIRECTIVE: AGENT TEAM (/agent-team)

**THE TRIGGER:** If the user's prompt contains `/agent-team` (alone or combined with `/feature-epic`), apply the cost-effective orchestration protocol to all sub-agent invocations.

## Model Selection

Evaluate complexity before spawning any sub-agent:

| Complexity | Model | Examples |
|------------|-------|----------|
| Simple | Gemini Flash / Haiku | Config, search, doc formatting, simple fixes |
| Medium | Sonnet | Standard implementation, unit tests, refactoring |
| Complex | Opus | Architecture, multi-file refactors, subtle bugs |

Start one tier lower when in doubt — escalate rather than overspend.

## Escalation Protocol

1. A sub-agent must **stop after 3 failed attempts** on the same error.
2. On stopping, return an escalation report:
   - The exact error
   - The 3 strategies attempted and why each failed
   - Best hypothesis for the root cause
   - Exit phrase: `ESCALATION_REQUIRED: <one-line reason>`
3. Spawn a new sub-agent at the **next higher model tier**, passing the full escalation report as context.
4. If an Opus sub-agent fails after 3 attempts, report to the user with full status summary.
