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
