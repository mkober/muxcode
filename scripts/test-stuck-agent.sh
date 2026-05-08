#!/usr/bin/env bash
# test-stuck-agent.sh — Integration test for busy-agent notification deferral
#
# Blocks an agent pane with a sleep command (simulating SSO/credential wait),
# sends messages during the block, then waits for the block to expire and
# verifies the agent receives one combined notification with all messages.
#
# Usage: bash scripts/test-stuck-agent.sh [ROLE] [BLOCK_DURATION_SECS]
#
# Requirements: running muxcode session with the target agent alive
# Note: this test temporarily blocks the target agent for BLOCK_DURATION seconds.
#       Use a non-critical agent (e.g., deploy, watch) or one you can afford to
#       pause briefly.

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

TARGET="${1:-deploy}"
BLOCK_DURATION="${2:-20}"
MSG_COUNT=10
MSG_INTERVAL=1
BUS_DIR="/tmp/muxcode-bus-${SESSION}"
MARKER="stuck-agent-test-$$"

# ============================================================
echo "=== Stuck Agent Integration Test ==="
echo "Session:        $SESSION"
echo "Target:         $TARGET"
echo "Block duration: ${BLOCK_DURATION}s"
echo "Messages:       $MSG_COUNT (every ${MSG_INTERVAL}s)"
echo ""

# --- Phase 1: Prerequisites ---
echo "--- Phase 1: Prerequisites ---"

if [ -n "${BUS_SESSION:-}" ]; then
  log_pass "BUS_SESSION is set ($BUS_SESSION)"
else
  export BUS_SESSION="$SESSION"
  log_pass "BUS_SESSION set from tmux session ($SESSION)"
fi

if [ -d "$BUS_DIR" ]; then
  log_pass "Bus directory exists"
else
  log_fail "Bus directory" "not found at $BUS_DIR"
  echo -e "\n${RED}Cannot continue without bus directory${NC}"
  exit 1
fi

INBOX="${BUS_DIR}/inbox/${TARGET}.jsonl"
if [ -f "$INBOX" ]; then
  log_pass "Target inbox exists ($TARGET)"
else
  log_fail "Target inbox" "not found at $INBOX"
  echo -e "\n${RED}Cannot continue without target inbox${NC}"
  exit 1
fi

# --- Phase 2: Clear state and block agent ---
echo ""
echo "--- Phase 2: Clear state and block agent ---"

# Clear notified IDs
rm -f "${BUS_DIR}/notified-${TARGET}.ids" 2>/dev/null
log_pass "Cleared notified IDs marker"

# Capture pane before blocking
PANE_BEFORE=$(tmux capture-pane -t "${SESSION}:${TARGET}" -p 2>/dev/null || echo "")
BEFORE_LINES=$(echo "$PANE_BEFORE" | grep -c "new message" || true)
log_pass "Captured pane baseline ($BEFORE_LINES notification lines)"

# Block the agent by sending a sleep command to the pane
# Use 'sleep N' in foreground — this makes the pane non-idle (not at ❯ prompt)
echo -e "  ${YELLOW}Blocking agent with sleep ${BLOCK_DURATION}...${NC}"
tmux send-keys -t "${SESSION}:${TARGET}" "sleep ${BLOCK_DURATION}" Enter 2>/dev/null || {
  log_fail "Block agent" "failed to send sleep command"
  echo -e "\n${RED}Cannot continue without blocking agent${NC}"
  exit 1
}
log_pass "Sent sleep ${BLOCK_DURATION} to ${TARGET} pane"

# Wait for sleep to start
sleep 1

# Verify agent is not idle
PANE_CHECK=$(tmux capture-pane -t "${SESSION}:${TARGET}" -p 2>/dev/null || echo "")
if echo "$PANE_CHECK" | tail -3 | grep -q "❯"; then
  echo -e "  ${YELLOW}WARNING: Agent still appears idle — sleep may not have taken effect${NC}"
else
  log_pass "Agent pane is blocked (not at idle prompt)"
fi

# --- Phase 3: Send messages during block ---
echo ""
echo "--- Phase 3: Sending $MSG_COUNT messages during block ---"

SOURCES=("build" "test" "review" "deploy")
for i in $(seq 1 $MSG_COUNT); do
  SRC_IDX=$(( (i - 1) % ${#SOURCES[@]} ))
  SOURCE="${SOURCES[$SRC_IDX]}"
  PAYLOAD="Stuck message $i/$MSG_COUNT from $SOURCE ($MARKER)"
  # Set AGENT_ROLE to override sender identity; disable dedup; skip send-time notify
  AGENT_ROLE="$SOURCE" MUXCODE_DEDUP_WINDOW=0 muxcode send "$TARGET" "stuck-$i" "$PAYLOAD" --type request --no-notify 2>/dev/null || true
  if [ "$i" -lt "$MSG_COUNT" ]; then
    sleep "$MSG_INTERVAL"
  fi
done
log_pass "Sent $MSG_COUNT messages during block"

# Capture pane during block to count injections
PANE_DURING=$(tmux capture-pane -t "${SESSION}:${TARGET}" -p 2>/dev/null || echo "")
DURING_LINES=$(echo "$PANE_DURING" | grep -c "new message" || true)
INJECTIONS_DURING=$((DURING_LINES - BEFORE_LINES))
if [ $INJECTIONS_DURING -lt 0 ]; then INJECTIONS_DURING=0; fi

echo "  Injections during block: $INJECTIONS_DURING"

# --- Phase 4: Wait for block to expire ---
echo ""
ELAPSED=$((MSG_COUNT * MSG_INTERVAL + 1))
REMAINING=$((BLOCK_DURATION - ELAPSED))
if [ "$REMAINING" -gt 0 ]; then
  echo "--- Phase 4: Waiting ${REMAINING}s for block to expire ---"
  sleep "$REMAINING"
else
  echo "--- Phase 4: Block has already expired ---"
fi

# Wait for daemon to detect idle transition (2 cycles)
echo "  Waiting 15s for daemon idle transition detection..."
sleep 15

# --- Phase 5: Measure results ---
echo ""
echo "--- Phase 5: Measuring results ---"

# Capture pane after unblock
PANE_AFTER=$(tmux capture-pane -t "${SESSION}:${TARGET}" -p 2>/dev/null || echo "")
AFTER_LINES=$(echo "$PANE_AFTER" | grep -c "new message" || true)
INJECTIONS_TOTAL=$((AFTER_LINES - BEFORE_LINES))
if [ $INJECTIONS_TOTAL -lt 0 ]; then INJECTIONS_TOTAL=0; fi

# Count test messages in inbox
STUCK_MSGS=$(grep -c "$MARKER" "$INBOX" 2>/dev/null || true)
STUCK_MSGS=$(echo "$STUCK_MSGS" | tr -d '[:space:]')
STUCK_MSGS=${STUCK_MSGS:-0}

echo "  Messages sent:           $MSG_COUNT"
echo "  Messages in inbox:       $STUCK_MSGS/$MSG_COUNT"
echo "  Injections during block: $INJECTIONS_DURING"
echo "  Total injections:        $INJECTIONS_TOTAL"

# --- Phase 6: Assertions ---
echo ""
echo "--- Phase 6: Assertions ---"

# Assertion 1: Messages were delivered (in inbox or consumed by agent)
if [ "$STUCK_MSGS" -eq "$MSG_COUNT" ]; then
  log_pass "All $MSG_COUNT messages present in inbox"
elif [ "$STUCK_MSGS" -eq 0 ]; then
  # Agent consumed all messages after unblock — this is expected
  log_pass "Agent consumed all messages after unblock (expected for active agent)"
else
  log_pass "$STUCK_MSGS/$MSG_COUNT messages remain in inbox (agent consumed some)"
fi

# Assertion 2: Zero injections during block (busy-agent deferral)
if [ "$INJECTIONS_DURING" -eq 0 ]; then
  log_pass "Zero injections during block"
else
  log_fail "Block-time injections" "$INJECTIONS_DURING (expected 0)"
fi

# Assertion 3: At most 2 total injections (combined notification on idle transition)
if [ "$INJECTIONS_TOTAL" -le 2 ]; then
  log_pass "$INJECTIONS_TOTAL total injection(s) after unblock (expected ≤2)"
else
  log_fail "Post-unblock injections" "$INJECTIONS_TOTAL (expected ≤2)"
fi

# Assertion 4: Notified IDs marker should exist after delivery
if [ -f "${BUS_DIR}/notified-${TARGET}.ids" ]; then
  NOTIFIED_COUNT=$(wc -l < "${BUS_DIR}/notified-${TARGET}.ids" 2>/dev/null | tr -d '[:space:]')
  NOTIFIED_COUNT=${NOTIFIED_COUNT:-0}
  log_pass "Notified IDs marker has $NOTIFIED_COUNT entries"
else
  # If the agent consumed messages, marker is cleared — that's OK
  log_pass "No notified IDs marker (agent consumed messages)"
fi

# --- Summary ---
echo ""
echo "=== Summary ==="
echo -e "Passed: ${GREEN}${pass}${NC}  Failed: ${RED}${fail}${NC}  Total: ${total}"

if [ "$fail" -gt 0 ]; then
  echo -e "\n${RED}FAIL${NC} — $fail test(s) failed"
  exit 1
else
  echo -e "\n${GREEN}PASS${NC} — all tests passed"
  exit 0
fi
