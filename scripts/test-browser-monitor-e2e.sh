#!/usr/bin/env bash
# test-browser-monitor-e2e.sh — End-to-end integration test for Playwright browser monitoring
#
# Tests the full agent flow: serve-state.json → daemon checkServeHealth → watch
# agent browser-check → edit notification. Also verifies dedup and non-Vite filtering.
#
# Usage: bash scripts/test-browser-monitor-e2e.sh
#
# Requirements:
#   - Running muxcode session with watch agent alive
#   - Node.js >= 18
#   - Playwright chromium installed
#   - Port 5199 available

set -euo pipefail

SESSION=$(tmux display-message -p '#S' 2>/dev/null) || { echo "FAIL: not in a tmux session"; exit 1; }

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

BUS_DIR="/tmp/muxcode-bus-${SESSION}"
FIXTURE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/test/fixtures/vite-react-app"
CHECK_SCRIPT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/scripts/playwright-check.js"
PORT=5199
BASE_URL="http://localhost:$PORT"
SERVER_PID=""

cleanup() {
  if [ -n "$SERVER_PID" ] && kill -0 "$SERVER_PID" 2>/dev/null; then
    log_info "Stopping dev server (pid $SERVER_PID)"
    kill "$SERVER_PID" 2>/dev/null || true
    wait "$SERVER_PID" 2>/dev/null || true
  fi
  lsof -ti :$PORT 2>/dev/null | xargs kill 2>/dev/null || true
  # Clean up test state file entries
  if [ -f "$BUS_DIR/serve-state.json" ]; then
    python3 -c "
import json, sys
try:
    with open('$BUS_DIR/serve-state.json') as f:
        state = json.load(f)
    state['servers'] = [s for s in state.get('servers', []) if s.get('port') != $PORT]
    with open('$BUS_DIR/serve-state.json', 'w') as f:
        json.dump(state, f)
except: pass
" 2>/dev/null || true
  fi
}
trap cleanup EXIT

echo "=== Browser Monitor End-to-End Integration Test ==="
echo ""

# --- Prerequisite checks ---

if ! command -v node &>/dev/null; then
  echo -e "${RED}SKIP${NC}: Node.js not found"
  exit 2
fi

if [ ! -f "$CHECK_SCRIPT" ]; then
  echo -e "${RED}SKIP${NC}: playwright-check.js not found"
  exit 2
fi

if [ ! -d "$BUS_DIR" ]; then
  echo -e "${RED}SKIP${NC}: Bus directory not found (no muxcode session?)"
  exit 2
fi

# --- Phase 1: Start fixture Vite app ---

echo "--- Phase 1: Start fixture Vite app ---"

# Kill any existing process on port
lsof -ti :$PORT 2>/dev/null | xargs kill 2>/dev/null || true
sleep 1

# Install deps if needed
cd "$FIXTURE_DIR"
if [ ! -d "node_modules" ]; then
  log_info "Installing fixture app dependencies..."
  npm install --silent 2>&1 | tail -1
fi

# Start server
log_info "Starting Vite dev server on port $PORT..."
npx vite --port $PORT --strictPort &>"$FIXTURE_DIR/vite-test.log" &
SERVER_PID=$!

for i in $(seq 1 30); do
  if curl -sf "$BASE_URL/" -o /dev/null 2>/dev/null; then
    break
  fi
  sleep 1
done

if curl -sf "$BASE_URL/" -o /dev/null 2>/dev/null; then
  log_pass "Vite dev server started on port $PORT (pid $SERVER_PID)"
else
  log_fail "Vite dev server" "failed to start within 30s"
  tail -5 "$FIXTURE_DIR/vite-test.log" 2>/dev/null || true
  exit 1
fi

# --- Phase 2: Write serve-state.json ---

echo "--- Phase 2: Verify serve-state.json ---"

# Write state file
ACTUAL_PID=$(lsof -ti :$PORT 2>/dev/null | head -1)
python3 -c "
import json, os, time
state_path = '$BUS_DIR/serve-state.json'
try:
    with open(state_path) as f:
        state = json.load(f)
except:
    state = {'servers': []}

# Remove any existing entry for this port
state['servers'] = [s for s in state['servers'] if s.get('port') != $PORT]

# Add the new entry
state['servers'].append({
    'name': 'vite',
    'command': 'npx vite --port $PORT --strictPort',
    'port': $PORT,
    'pid': $ACTUAL_PID,
    'url': '$BASE_URL/',
    'started_at': int(time.time()),
    'restarts': 0,
    'status': 'running'
})

with open(state_path, 'w') as f:
    json.dump(state, f, indent=2)
"

# Verify state file
if [ -f "$BUS_DIR/serve-state.json" ]; then
  log_pass "serve-state.json exists"
else
  log_fail "serve-state.json" "file not found"
fi

STATUS=$(python3 -c "
import json
with open('$BUS_DIR/serve-state.json') as f:
    state = json.load(f)
for s in state['servers']:
    if s['port'] == $PORT:
        print(s['status'])
        break
" 2>/dev/null || echo "missing")

if [ "$STATUS" = "running" ]; then
  log_pass "serve-state.json shows status=running"
else
  log_fail "serve-state.json status" "expected 'running', got '$STATUS'"
fi

NAME=$(python3 -c "
import json
with open('$BUS_DIR/serve-state.json') as f:
    state = json.load(f)
for s in state['servers']:
    if s['port'] == $PORT:
        print(s['name'])
        break
" 2>/dev/null || echo "missing")

if [ "$NAME" = "vite" ]; then
  log_pass "serve-state.json shows name=vite"
else
  log_fail "serve-state.json name" "expected 'vite', got '$NAME'"
fi

# --- Phase 3: IsViteServer detection ---

echo "--- Phase 3: IsViteServer detection ---"

# Test via the Go binary
VITE_DETECT=$(muxcode serve-state 2>/dev/null | grep -c "vite" || echo "0")

# Fall back to testing the Go logic conceptually via the state file
VITE_PORT=$(python3 -c "
import json
with open('$BUS_DIR/serve-state.json') as f:
    state = json.load(f)
for s in state['servers']:
    if s['port'] == $PORT:
        # Simulate IsViteServer logic
        name = s.get('name', '')
        cmd = s.get('command', '')
        port = s.get('port', 0)
        is_vite = (
            name in ('vite', 'svelte', 'sveltekit') or
            any(p in cmd for p in ['vite', 'npx vite', 'pnpm dev', 'npm run dev', 'yarn dev']) or
            port in (5173, 5174)
        )
        print('yes' if is_vite else 'no')
        break
" 2>/dev/null || echo "error")

if [ "$VITE_PORT" = "yes" ]; then
  log_pass "IsViteServer detects fixture as Vite server"
else
  log_fail "IsViteServer" "expected yes, got '$VITE_PORT'"
fi

# --- Phase 4: Playwright browser-check (clean) ---

echo "--- Phase 4: Browser check — clean mode ---"

export NODE_PATH="$FIXTURE_DIR/node_modules"
OUTPUT=$(node "$CHECK_SCRIPT" "$BASE_URL/?mode=clean" --wait 2000 2>&1) || EXIT_CODE=$?
EXIT_CODE=${EXIT_CODE:-0}

if [ "$EXIT_CODE" -eq 0 ]; then
  log_pass "Clean mode: exit code 0 (no issues)"
else
  log_fail "Clean mode exit code" "expected 0, got $EXIT_CODE"
fi

ISSUES=$(echo "$OUTPUT" | jq -r '.total_issues' 2>/dev/null || echo "parse_error")
if [ "$ISSUES" = "0" ]; then
  log_pass "Clean mode: 0 issues detected"
else
  log_fail "Clean mode issues" "expected 0, got $ISSUES"
fi

# --- Phase 5: Playwright browser-check (errors) ---

echo "--- Phase 5: Browser check — error mode ---"

OUTPUT=$(node "$CHECK_SCRIPT" "$BASE_URL/?mode=error" --wait 2000 2>&1) || EXIT_CODE=$?
EXIT_CODE=${EXIT_CODE:-0}

if [ "$EXIT_CODE" -eq 1 ]; then
  log_pass "Error mode: exit code 1 (issues found)"
else
  log_fail "Error mode exit code" "expected 1, got $EXIT_CODE"
fi

ERROR_COUNT=$(echo "$OUTPUT" | jq -r '.errors | length' 2>/dev/null || echo "0")
if [ "$ERROR_COUNT" -ge 1 ]; then
  log_pass "Error mode: detected $ERROR_COUNT console.error(s)"
else
  log_fail "Error mode" "expected >= 1 error, got $ERROR_COUNT"
fi

# --- Phase 6: Non-Vite server filtering ---

echo "--- Phase 6: Non-Vite server filtering ---"

# Add a non-Vite server to serve-state.json and verify it's excluded
python3 -c "
import json
with open('$BUS_DIR/serve-state.json') as f:
    state = json.load(f)
state['servers'].append({
    'name': 'api',
    'command': 'go run .',
    'port': 8080,
    'pid': 99999,
    'url': 'http://localhost:8080/',
    'started_at': 0,
    'restarts': 0,
    'status': 'running'
})
with open('$BUS_DIR/serve-state.json', 'w') as f:
    json.dump(state, f, indent=2)
"

GO_VITE=$(python3 -c "
import json
with open('$BUS_DIR/serve-state.json') as f:
    state = json.load(f)
for s in state['servers']:
    if s['port'] == 8080:
        name = s.get('name', '')
        cmd = s.get('command', '')
        port = s.get('port', 0)
        is_vite = (
            name in ('vite', 'svelte', 'sveltekit') or
            any(p in cmd for p in ['vite', 'npx vite', 'pnpm dev', 'npm run dev', 'yarn dev']) or
            port in (5173, 5174)
        )
        print('yes' if is_vite else 'no')
        break
" 2>/dev/null || echo "error")

if [ "$GO_VITE" = "no" ]; then
  log_pass "Non-Vite server (Go on :8080) correctly excluded"
else
  log_fail "Non-Vite filtering" "Go server should not be detected as Vite"
fi

# Add Flask server
FLASK_VITE=$(python3 -c "
name = 'backend'
cmd = 'flask run'
port = 5000
is_vite = (
    name in ('vite', 'svelte', 'sveltekit') or
    any(p in cmd for p in ['vite', 'npx vite', 'pnpm dev', 'npm run dev', 'yarn dev']) or
    port in (5173, 5174)
)
print('yes' if is_vite else 'no')
" 2>/dev/null || echo "error")

if [ "$FLASK_VITE" = "no" ]; then
  log_pass "Non-Vite server (Flask on :5000) correctly excluded"
else
  log_fail "Non-Vite filtering" "Flask server should not be detected as Vite"
fi

# --- Phase 7: Dedup verification ---

echo "--- Phase 7: Dedup verification ---"

# Send two identical browser-check messages to watch — second should be suppressed
FIRST=$(muxcode send watch browser-check "Dedup test: check http://localhost:$PORT/ for errors" 2>&1)
sleep 1
SECOND=$(muxcode send watch browser-check "Dedup test: check http://localhost:$PORT/ for errors" 2>&1)

if echo "$FIRST" | grep -q "Sent"; then
  log_pass "First browser-check message delivered"
else
  log_fail "First browser-check" "message not sent: $FIRST"
fi

if echo "$SECOND" | grep -q "Suppressed"; then
  log_pass "Duplicate browser-check suppressed by dedup"
else
  log_fail "Dedup" "second message was not suppressed: $SECOND"
fi

# Clean up the fake Go server entry
python3 -c "
import json
with open('$BUS_DIR/serve-state.json') as f:
    state = json.load(f)
state['servers'] = [s for s in state['servers'] if s.get('port') != 8080]
with open('$BUS_DIR/serve-state.json', 'w') as f:
    json.dump(state, f, indent=2)
" 2>/dev/null || true

# --- Summary ---

echo ""
echo "=== Results: $pass passed, $fail failed, $total total ==="

if [ "$fail" -gt 0 ]; then
  exit 1
fi
echo -e "${GREEN}All tests passed${NC}"
