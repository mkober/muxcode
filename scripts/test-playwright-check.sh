#!/usr/bin/env bash
# test-playwright-check.sh — Integration test for Playwright browser monitoring
#
# Starts the test Vite+React fixture app, runs playwright-check.js against it
# in each mode (clean, error, warning, exception), and verifies correct detection.
#
# Usage: bash scripts/test-playwright-check.sh
#
# Requirements:
#   - Node.js >= 18
#   - Playwright chromium browser (auto-installed if missing)
#   - Port 5199 available

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
FIXTURE_DIR="$REPO_DIR/test/fixtures/vite-react-app"
CHECK_SCRIPT="$REPO_DIR/scripts/playwright-check.js"
PORT=5199
BASE_URL="http://localhost:$PORT"
SERVER_PID=""

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

pass=0
fail=0
total=0

log_pass() { ((pass++)); ((total++)); echo -e "  ${GREEN}PASS${NC} $1"; }
log_fail() { ((fail++)); ((total++)); echo -e "  ${RED}FAIL${NC} $1: $2"; }
log_info() { echo -e "  ${YELLOW}INFO${NC} $1"; }

cleanup() {
  if [ -n "$SERVER_PID" ] && kill -0 "$SERVER_PID" 2>/dev/null; then
    log_info "Stopping dev server (pid $SERVER_PID)"
    kill "$SERVER_PID" 2>/dev/null || true
    wait "$SERVER_PID" 2>/dev/null || true
  fi
  # Kill any lingering process on the port
  lsof -ti :$PORT 2>/dev/null | xargs kill 2>/dev/null || true
}
trap cleanup EXIT

echo "=== Playwright Browser Check Integration Test ==="
echo ""

# --- Prerequisite checks ---

if ! command -v node &>/dev/null; then
  echo -e "${RED}SKIP${NC}: Node.js not found"
  exit 2
fi

NODE_VERSION=$(node -v | sed 's/v//' | cut -d. -f1)
if [ "$NODE_VERSION" -lt 18 ]; then
  echo -e "${RED}SKIP${NC}: Node.js >= 18 required (found v$NODE_VERSION)"
  exit 2
fi

if lsof -ti :$PORT &>/dev/null; then
  echo -e "${RED}SKIP${NC}: Port $PORT already in use"
  exit 2
fi

if [ ! -f "$CHECK_SCRIPT" ]; then
  echo -e "${RED}SKIP${NC}: playwright-check.js not found at $CHECK_SCRIPT"
  exit 2
fi

# --- Install dependencies ---

log_info "Installing fixture app dependencies..."
cd "$FIXTURE_DIR"
if [ ! -d "node_modules" ]; then
  npm install --silent 2>&1 | tail -1
fi

# Install Playwright chromium if not present
log_info "Ensuring Playwright chromium is installed..."
npx playwright install chromium 2>&1 | tail -1 || true

# Export NODE_PATH so playwright-check.js can resolve the playwright module
# from the fixture's node_modules regardless of working directory
export NODE_PATH="$FIXTURE_DIR/node_modules"

# --- Start dev server ---

log_info "Starting Vite dev server on port $PORT..."
npx vite --port $PORT --strictPort &>"$FIXTURE_DIR/vite-test.log" &
SERVER_PID=$!

# Wait for server to be ready
for i in $(seq 1 30); do
  if curl -sf "$BASE_URL/" -o /dev/null 2>/dev/null; then
    break
  fi
  if ! kill -0 "$SERVER_PID" 2>/dev/null; then
    echo -e "${RED}FAIL${NC}: Dev server exited unexpectedly"
    tail -10 "$FIXTURE_DIR/vite-test.log" 2>/dev/null || true
    exit 1
  fi
  sleep 1
done

if ! curl -sf "$BASE_URL/" -o /dev/null 2>/dev/null; then
  echo -e "${RED}FAIL${NC}: Dev server failed to start within 30s"
  tail -10 "$FIXTURE_DIR/vite-test.log" 2>/dev/null || true
  exit 1
fi

log_info "Dev server ready at $BASE_URL (pid $SERVER_PID)"
echo ""

# --- Test 1: Clean mode (no issues) ---

echo "--- Test: clean mode ---"
cd "$REPO_DIR"
OUTPUT=$(node "$CHECK_SCRIPT" "$BASE_URL/?mode=clean" --wait 3000 2>&1) || EXIT_CODE=$?
EXIT_CODE=${EXIT_CODE:-0}

if [ "$EXIT_CODE" -eq 0 ]; then
  log_pass "Clean mode exits 0"
else
  log_fail "Clean mode exit code" "expected 0, got $EXIT_CODE"
fi

ISSUES=$(echo "$OUTPUT" | jq -r '.total_issues' 2>/dev/null || echo "parse_error")
if [ "$ISSUES" = "0" ]; then
  log_pass "Clean mode reports 0 issues"
else
  log_fail "Clean mode issue count" "expected 0, got $ISSUES"
fi

STATUS=$(echo "$OUTPUT" | jq -r '.status' 2>/dev/null || echo "parse_error")
if [ "$STATUS" = "200" ]; then
  log_pass "Clean mode HTTP status 200"
else
  log_fail "Clean mode HTTP status" "expected 200, got $STATUS"
fi

# --- Test 2: Error mode ---

echo "--- Test: error mode ---"
OUTPUT=$(node "$CHECK_SCRIPT" "$BASE_URL/?mode=error" --wait 3000 2>&1) || EXIT_CODE=$?
EXIT_CODE=${EXIT_CODE:-0}

if [ "$EXIT_CODE" -eq 1 ]; then
  log_pass "Error mode exits 1"
else
  log_fail "Error mode exit code" "expected 1, got $EXIT_CODE"
fi

ERROR_COUNT=$(echo "$OUTPUT" | jq -r '.errors | length' 2>/dev/null || echo "parse_error")
if [ "$ERROR_COUNT" -ge 1 ]; then
  log_pass "Error mode detects console.error ($ERROR_COUNT found)"
else
  log_fail "Error mode error count" "expected >= 1, got $ERROR_COUNT"
fi

ERROR_TEXT=$(echo "$OUTPUT" | jq -r '.errors[0].text' 2>/dev/null || echo "")
if echo "$ERROR_TEXT" | grep -q "something went wrong"; then
  log_pass "Error mode captures error message text"
else
  log_fail "Error mode error text" "expected 'something went wrong', got '$ERROR_TEXT'"
fi

# --- Test 3: Warning mode ---

echo "--- Test: warning mode ---"
OUTPUT=$(node "$CHECK_SCRIPT" "$BASE_URL/?mode=warning" --wait 3000 2>&1) || EXIT_CODE=$?
EXIT_CODE=${EXIT_CODE:-0}

if [ "$EXIT_CODE" -eq 1 ]; then
  log_pass "Warning mode exits 1"
else
  log_fail "Warning mode exit code" "expected 1, got $EXIT_CODE"
fi

WARN_COUNT=$(echo "$OUTPUT" | jq -r '.warnings | length' 2>/dev/null || echo "parse_error")
if [ "$WARN_COUNT" -ge 1 ]; then
  log_pass "Warning mode detects console.warn ($WARN_COUNT found)"
else
  log_fail "Warning mode warning count" "expected >= 1, got $WARN_COUNT"
fi

# --- Test 4: Exception mode ---

echo "--- Test: exception mode ---"
OUTPUT=$(node "$CHECK_SCRIPT" "$BASE_URL/?mode=exception" --wait 3000 2>&1) || EXIT_CODE=$?
EXIT_CODE=${EXIT_CODE:-0}

if [ "$EXIT_CODE" -eq 1 ]; then
  log_pass "Exception mode exits 1"
else
  log_fail "Exception mode exit code" "expected 1, got $EXIT_CODE"
fi

EXCEPTION_COUNT=$(echo "$OUTPUT" | jq -r '.exceptions | length' 2>/dev/null || echo "parse_error")
if [ "$EXCEPTION_COUNT" -ge 1 ]; then
  log_pass "Exception mode detects uncaught exception ($EXCEPTION_COUNT found)"
else
  log_fail "Exception mode exception count" "expected >= 1, got $EXCEPTION_COUNT"
fi

EXCEPTION_MSG=$(echo "$OUTPUT" | jq -r '.exceptions[0].message' 2>/dev/null || echo "")
if echo "$EXCEPTION_MSG" | grep -q "uncaught runtime error"; then
  log_pass "Exception mode captures exception message text"
else
  log_fail "Exception mode exception text" "expected 'uncaught runtime error', got '$EXCEPTION_MSG'"
fi

# --- Test 5: All mode (errors + warnings + exceptions) ---

echo "--- Test: all mode ---"
OUTPUT=$(node "$CHECK_SCRIPT" "$BASE_URL/?mode=all" --wait 3000 2>&1) || EXIT_CODE=$?
EXIT_CODE=${EXIT_CODE:-0}

if [ "$EXIT_CODE" -eq 1 ]; then
  log_pass "All mode exits 1"
else
  log_fail "All mode exit code" "expected 1, got $EXIT_CODE"
fi

ALL_ISSUES=$(echo "$OUTPUT" | jq -r '.total_issues' 2>/dev/null || echo "parse_error")
if [ "$ALL_ISSUES" -ge 3 ]; then
  log_pass "All mode detects multiple issue types ($ALL_ISSUES total)"
else
  log_fail "All mode total issues" "expected >= 3, got $ALL_ISSUES"
fi

# --- Test 6: Invalid URL (exit code 2) ---

echo "--- Test: invalid URL ---"
OUTPUT=$(node "$CHECK_SCRIPT" "http://localhost:19999/" --timeout 3000 2>&1) || EXIT_CODE=$?
EXIT_CODE=${EXIT_CODE:-0}

if [ "$EXIT_CODE" -eq 2 ]; then
  log_pass "Invalid URL exits 2"
else
  log_fail "Invalid URL exit code" "expected 2, got $EXIT_CODE"
fi

LAUNCH_ERROR=$(echo "$OUTPUT" | jq -r '.launch_error' 2>/dev/null || echo "false")
if [ "$LAUNCH_ERROR" = "true" ]; then
  log_pass "Invalid URL sets launch_error flag"
else
  log_fail "Invalid URL launch_error flag" "expected true, got $LAUNCH_ERROR"
fi

# --- Test 7: JSON output structure ---

echo "--- Test: JSON structure ---"
OUTPUT=$(node "$CHECK_SCRIPT" "$BASE_URL/?mode=clean" --wait 1000 2>&1) || true

for field in url status errors warnings exceptions total_issues checked_at; do
  VALUE=$(echo "$OUTPUT" | jq -r ".$field" 2>/dev/null || echo "missing")
  if [ "$VALUE" != "missing" ] && [ "$VALUE" != "null" ]; then
    log_pass "JSON has '$field' field"
  else
    log_fail "JSON field" "missing '$field'"
  fi
done

# --- Summary ---

echo ""
echo "=== Results: $pass passed, $fail failed, $total total ==="

if [ "$fail" -gt 0 ]; then
  exit 1
fi
echo -e "${GREEN}All tests passed${NC}"
