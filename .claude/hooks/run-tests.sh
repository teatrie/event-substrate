#!/bin/bash

# 1. Lockfile Check: Only run automated tests if /tdd-execute is running
if [ ! -f ".tdd-active" ]; then
    exit 0
fi

# 2. Read the JSON payload from Claude Code
PAYLOAD=$(cat)
AGENT_TYPE=$(echo "$PAYLOAD" | jq -r '.agent_type // "main"')

# 3. Only run tests to verify GREEN and REFACTOR subagents' work
if [[ "$AGENT_TYPE" != "tdd-green" && "$AGENT_TYPE" != "tdd-refactor" ]]; then
    exit 0
fi

# 4. Auto-Detect the Build Tool / Language
if [ -n "$TDD_TEST_CMD" ]; then
    TEST_CMD="$TDD_TEST_CMD"
elif [ -f "Makefile" ]; then
    TEST_CMD="make test"
elif [ -f "go.mod" ]; then
    TEST_CMD="go test ./... -v"
elif [ -f "pyproject.toml" ] || [ -f "requirements.txt" ]; then
    TEST_CMD="pytest"
elif [ -f "package.json" ]; then
    TEST_CMD="npm test"
elif [ -f "pom.xml" ]; then
    TEST_CMD="mvn test"
else
    >&2 echo "TDD Enforcement Failed: Could not automatically detect the test framework."
    >&2 echo "Please define TDD_TEST_CMD in your .env file or add a Makefile."
    exit 2
fi

# 5. Run the detected test command
TEST_OUTPUT=$(eval "$TEST_CMD" 2>&1)
TEST_EXIT_CODE=$?

# 6. Evaluate the results
if [ $TEST_EXIT_CODE -eq 0 ]; then
    exit 0 
else
    # Block the AI and feed the output back so it can fix the code
    >&2 echo "TDD Enforcement Failed: The tests did not pass after your edit."
    >&2 echo "Command run: $TEST_CMD"
    >&2 echo "Here is the test output you must fix:"
    >&2 echo "$TEST_OUTPUT"
    
    exit 2
fi
