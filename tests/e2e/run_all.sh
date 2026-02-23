#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
cd "$SCRIPT_DIR"

# Auto-detect Supabase keys if not set (must run from project root)
if [ -z "${SUPABASE_ANON_KEY:-}" ]; then
  export SUPABASE_ANON_KEY=$(cd "$PROJECT_ROOT" && supabase status -o json 2>/dev/null | jq -r .ANON_KEY)
fi
if [ -z "${SUPABASE_SERVICE_ROLE_KEY:-}" ]; then
  export SUPABASE_SERVICE_ROLE_KEY=$(cd "$PROJECT_ROOT" && supabase status -o json 2>/dev/null | jq -r .SERVICE_ROLE_KEY)
fi

if [ -z "${SUPABASE_ANON_KEY:-}" ] || [ "$SUPABASE_ANON_KEY" = "null" ]; then
  echo "ERROR: Could not auto-detect SUPABASE_ANON_KEY. Is Supabase running? (task start)"
  echo "  Or set manually: export SUPABASE_ANON_KEY=\$(cd $PROJECT_ROOT && supabase status -o json | jq -r .anon_key)"
  exit 1
fi

echo "=== Event Substrate E2E Tests ==="
echo ""

# Clean up stale test data from previous failed runs
node cleanup_stale.mjs

TESTS=(
  test_signup_flow.mjs
  test_login_flow.mjs
  test_signout_flow.mjs
  test_message_flow.mjs
  test_rls_isolation.mjs
)

PASSED=0
FAILED=0

for test in "${TESTS[@]}"; do
  if node "$test"; then
    PASSED=$((PASSED + 1))
  else
    FAILED=$((FAILED + 1))
    echo "STOPPING: $test failed"
    break
  fi
done

echo ""
echo "=== Results: ${PASSED} passed, ${FAILED} failed ==="
exit $FAILED
