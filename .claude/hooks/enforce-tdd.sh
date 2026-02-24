#!/bin/bash

# 1. Lockfile Check: Only enforce rules if /tdd-execute is running
if [ ! -f ".tdd-active" ]; then
    exit 0
fi

# 2. Read the hook context from Claude Code
PAYLOAD=$(cat)
AGENT_TYPE=$(echo "$PAYLOAD" | jq -r '.agent_type // "main"')

# 3. Allow RED (writes tests), GREEN and REFACTOR subagents to edit files
if [[ "$AGENT_TYPE" == "tdd-red" || "$AGENT_TYPE" == "tdd-green" || "$AGENT_TYPE" == "tdd-refactor" ]]; then
    exit 0
fi

# 4. Block any other agent (including Main) from writing files during TDD loop
cat <<EOF
{
  "hookSpecificOutput": {
    "hookEventName": "PreToolUse",
    "permissionDecision": "deny",
    "permissionDecisionReason": "TDD Policy Violation: You are inside a /tdd-execute loop but are not the designated implementer. You are forbidden from modifying files directly. Hand this task off to the correct subagent."
  }
}
EOF

exit 0
