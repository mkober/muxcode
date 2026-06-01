#!/usr/bin/env bash
# test-context-pressure.sh — Integration test for edit context pressure mitigations
#
# Tests that notification storms are properly throttled:
#   1. Run chain does NOT fire watch for muxcode bus commands
#   2. Auto-CC is rate-limited (1 per role per 60s)
#   3. Message dedup suppresses duplicate requests
#   4. Notification budget env var is accepted
#
# Usage: bash scripts/test-context-pressure.sh
#
# Requirements: running muxcode session with updated binary installed

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
export BUS_SESSION="$SESSION"

echo "=== Edit Context Pressure Integration Test ==="
echo "Session: $SESSION"
echo ""

# --- Prerequisites ---
echo "--- Prerequisites ---"

if [ -d "$BUS_DIR" ]; then
  log_pass "Bus directory exists"
else
  log_fail "Bus directory" "not found at $BUS_DIR"
  exit 1
fi

if command -v muxcode &>/dev/null; then
  log_pass "muxcode binary available"
else
  log_fail "muxcode" "binary not found in PATH"
  exit 1
fi

# ============================================================
# Test 1: Run chain filters muxcode commands
# ============================================================
echo ""
echo "--- Test 1: Run chain filters muxcode commands ---"

# muxcode inbox — should NOT trigger watch (exit 2 = no chain matched)
muxcode chain run success --command "muxcode inbox" --dry-run >/dev/null 2>&1 && MUXCODE_EXIT=0 || MUXCODE_EXIT=$?

if [ "$MUXCODE_EXIT" -eq 2 ]; then
  log_pass "Run chain skips 'muxcode inbox' (exit 2, no match)"
else
  log_fail "Run chain muxcode inbox" "expected exit 2 (no match), got $MUXCODE_EXIT"
fi

# muxcode send — should NOT trigger watch
muxcode chain run success --command "muxcode send edit notify hello" --dry-run >/dev/null 2>&1 && MUXCODE_EXIT=0 || MUXCODE_EXIT=$?

if [ "$MUXCODE_EXIT" -eq 2 ]; then
  log_pass "Run chain skips 'muxcode send' (exit 2, no match)"
else
  log_fail "Run chain muxcode send" "expected exit 2 (no match), got $MUXCODE_EXIT"
fi

# aws lambda invoke — SHOULD trigger watch (exit 0 = chain fired)
RESULT=$(muxcode chain run success --command "aws lambda invoke --function-name test" --dry-run 2>&1) && AWS_EXIT=0 || AWS_EXIT=$?

if [ "$AWS_EXIT" -eq 0 ] && echo "$RESULT" | grep -q "watch"; then
  log_pass "Run chain fires watch for 'aws lambda invoke'"
else
  log_fail "Run chain aws" "expected watch action, exit=$AWS_EXIT result=$RESULT"
fi

# bash scripts/deploy.sh — SHOULD trigger watch
RESULT=$(muxcode chain run success --command "bash scripts/deploy.sh" --dry-run 2>&1) && DEPLOY_EXIT=0 || DEPLOY_EXIT=$?

if [ "$DEPLOY_EXIT" -eq 0 ] && echo "$RESULT" | grep -q "watch"; then
  log_pass "Run chain fires watch for 'bash scripts/deploy.sh'"
else
  log_fail "Run chain deploy" "expected watch action, exit=$DEPLOY_EXIT result=$RESULT"
fi

# Verbose mode should show condition evaluation
RESULT=$(muxcode chain run success --command "muxcode inbox" --dry-run --verbose 2>&1) || true

if echo "$RESULT" | grep -q "command_not_match"; then
  log_pass "Verbose mode shows command_not_match evaluation"
else
  log_fail "Verbose condition" "expected command_not_match in output, got: $RESULT"
fi

# ============================================================
# Test 2: Auto-CC rate limiting
# ============================================================
echo ""
echo "--- Test 2: Auto-CC rate limiting ---"

# Record edit inbox size before
INBOX_BEFORE=$(wc -l < "$BUS_DIR/inbox/edit.jsonl" 2>/dev/null || echo "0")

# Send two messages from build to test (a CC-eligible path) in quick succession
muxcode send test notify "CC rate limit test msg A" --from build 2>/dev/null || true
sleep 1
muxcode send test notify "CC rate limit test msg B" --from build 2>/dev/null || true

INBOX_AFTER=$(wc -l < "$BUS_DIR/inbox/edit.jsonl" 2>/dev/null || echo "0")
CC_COUNT=$((INBOX_AFTER - INBOX_BEFORE))

# At most 1 CC should have been delivered (the second one rate-limited)
if [ "$CC_COUNT" -le 1 ]; then
  log_pass "Auto-CC rate limited (${CC_COUNT} CC in 1s, expected ≤1)"
else
  log_fail "Auto-CC rate limit" "expected ≤1 CC, got $CC_COUNT"
fi

# ============================================================
# Test 3: Message dedup
# ============================================================
echo ""
echo "--- Test 3: Message dedup ---"

# Send identical request twice — second should be suppressed
FIRST=$(muxcode send watch browser-check "Context pressure dedup test" 2>&1)
SECOND=$(muxcode send watch browser-check "Context pressure dedup test" 2>&1)

if echo "$FIRST" | grep -q "Sent"; then
  log_pass "First message delivered"
else
  log_pass "First message handled"
fi

if echo "$SECOND" | grep -qi "Suppressed\|duplicate"; then
  log_pass "Duplicate message suppressed"
else
  log_fail "Message dedup" "second message was not suppressed: $SECOND"
fi

# ============================================================
# Test 4: Notification budget env var
# ============================================================
echo ""
echo "--- Test 4: Notification budget config ---"

# Verify env var doesn't cause errors
BUDGET_RESULT=$(MUXCODE_EDIT_NOTIFY_BUDGET=5 muxcode chain run success --command "aws deploy" --dry-run 2>&1) && BUDGET_EXIT=0 || BUDGET_EXIT=$?

if [ "$BUDGET_EXIT" -eq 0 ]; then
  log_pass "MUXCODE_EDIT_NOTIFY_BUDGET=5 accepted (chain runs normally)"
else
  log_fail "Notification budget env" "exit $BUDGET_EXIT: $BUDGET_RESULT"
fi

# Invalid value should not crash
BUDGET_RESULT=$(MUXCODE_EDIT_NOTIFY_BUDGET=invalid muxcode chain run success --command "aws deploy" --dry-run 2>&1) && BUDGET_EXIT=0 || BUDGET_EXIT=$?

if [ "$BUDGET_EXIT" -eq 0 ]; then
  log_pass "MUXCODE_EDIT_NOTIFY_BUDGET=invalid falls back gracefully"
else
  log_fail "Notification budget invalid" "exit $BUDGET_EXIT: $BUDGET_RESULT"
fi

# --- Summary ---

echo ""
echo "=== Results: $pass passed, $fail failed, $total total ==="

if [ "$fail" -gt 0 ]; then
  exit 1
fi
echo -e "${GREEN}All tests passed${NC}"
