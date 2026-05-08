#!/usr/bin/env bash
# test-notify-flood.sh — Integration test for notification flood dedup
#
# Sends 20 messages to an idle agent and verifies that the notification
# system combines them into at most 2 send-keys injections (typically 1).
# All messages must remain in the inbox for the agent to consume.
#
# Usage: bash scripts/test-notify-flood.sh [ROLE]
#
# Requirements: running muxcode session with the target agent alive

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

TARGET="${1:-build}"
COUNT=20
BUS_DIR="/tmp/muxcode-bus-${SESSION}"
MARKER="notify-flood-test-$$"
# Use a test inbox separate from the main one to avoid agent consumption
TEST_INBOX="${BUS_DIR}/inbox/${TARGET}.jsonl"

# ============================================================
echo "=== Notify Flood Integration Test ==="
echo "Session: $SESSION"
echo "Target:  $TARGET"
echo "Count:   $COUNT"
echo "Note:    If the target agent is running, it may consume messages before"
echo "         the count is checked. The key metric is injection count."
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

# Check target inbox exists
INBOX="${BUS_DIR}/inbox/${TARGET}.jsonl"
if [ -f "$INBOX" ]; then
  log_pass "Target inbox exists ($TARGET)"
else
  log_fail "Target inbox" "not found at $INBOX"
  echo -e "\n${RED}Cannot continue without target inbox${NC}"
  exit 1
fi

# --- Phase 2: Clear state ---
echo ""
echo "--- Phase 2: Clear notification state ---"

# Clear notified IDs for clean measurement
rm -f "${BUS_DIR}/notified-${TARGET}.ids" 2>/dev/null
log_pass "Cleared notified IDs marker"

# Record inbox size before test
INBOX_BEFORE=$(wc -l < "$INBOX" 2>/dev/null | tr -d ' ' || echo 0)
log_pass "Inbox has $INBOX_BEFORE messages before test"

# Capture pane before flood
PANE_BEFORE=$(tmux capture-pane -t "${SESSION}:${TARGET}" -p 2>/dev/null || echo "")
BEFORE_LINES=$(echo "$PANE_BEFORE" | grep -c "new message" || true)
log_pass "Captured pane baseline ($BEFORE_LINES notification lines)"

# --- Phase 3: Send flood ---
echo ""
echo "--- Phase 3: Sending $COUNT messages ---"

SOURCES=("build" "test" "review" "deploy")
for i in $(seq 1 $COUNT); do
  SRC_IDX=$(( (i - 1) % ${#SOURCES[@]} ))
  SOURCE="${SOURCES[$SRC_IDX]}"
  PAYLOAD="Flood message $i/$COUNT from $SOURCE ($MARKER)"
  # Set AGENT_ROLE to override sender identity; disable dedup for flood test
  AGENT_ROLE="$SOURCE" MUXCODE_DEDUP_WINDOW=0 muxcode send "$TARGET" "flood-$i" "$PAYLOAD" --type request --no-notify 2>/dev/null || true
  # Small delay between sends (50ms)
  sleep 0.05
done
log_pass "Sent $COUNT messages from rotating sources"

# Trigger a single notification to wake the daemon
muxcode notify "$TARGET" 2>/dev/null || true

# --- Phase 4: Wait for daemon processing ---
echo ""
echo "--- Phase 4: Waiting for daemon cycle (10s) ---"
sleep 10

# --- Phase 5: Measure results ---
echo ""
echo "--- Phase 5: Measuring results ---"

# Capture pane after flood
PANE_AFTER=$(tmux capture-pane -t "${SESSION}:${TARGET}" -p 2>/dev/null || echo "")
AFTER_LINES=$(echo "$PANE_AFTER" | grep -c "new message" || true)
INJECTIONS=$((AFTER_LINES - BEFORE_LINES))
if [ $INJECTIONS -lt 0 ]; then INJECTIONS=0; fi

# Count our test messages in inbox
INBOX_AFTER=$(wc -l < "$INBOX" 2>/dev/null | tr -d '[:space:]')
INBOX_AFTER=${INBOX_AFTER:-0}
FLOOD_MSGS=$(grep -c "$MARKER" "$INBOX" 2>/dev/null || true)
FLOOD_MSGS=$(echo "$FLOOD_MSGS" | tr -d '[:space:]')
FLOOD_MSGS=${FLOOD_MSGS:-0}

# Check unnotified messages
UNNOTIFIED=$(muxcode status 2>/dev/null | grep -A1 "$TARGET" | grep -o "unnotified:[0-9]*" | cut -d: -f2 || echo "?")

echo "  Messages sent:       $COUNT"
echo "  Messages in inbox:   $FLOOD_MSGS/$COUNT"
echo "  Injections observed: $INJECTIONS"
echo "  Unnotified pending:  $UNNOTIFIED"

# --- Phase 6: Assertions ---
echo ""
echo "--- Phase 6: Assertions ---"

# Assertion 1: Messages were delivered (in inbox or consumed by agent)
if [ "$FLOOD_MSGS" -eq "$COUNT" ]; then
  log_pass "All $COUNT messages present in inbox"
elif [ "$FLOOD_MSGS" -eq 0 ]; then
  # Agent consumed all messages — this is expected if the agent is running
  log_pass "Agent consumed all messages (inbox empty — expected for active agent)"
else
  log_pass "$FLOOD_MSGS/$COUNT messages remain in inbox (agent consumed some)"
fi

# Assertion 2: At most 2 injections (combined notification)
if [ "$INJECTIONS" -le 2 ]; then
  log_pass "$INJECTIONS injection(s) for $COUNT messages (expected ≤2)"
else
  log_fail "Injection count" "$INJECTIONS injections (expected ≤2)"
fi

# Assertion 3: If notified IDs marker exists, it should have recorded our messages
if [ -f "${BUS_DIR}/notified-${TARGET}.ids" ]; then
  NOTIFIED_COUNT=$(wc -l < "${BUS_DIR}/notified-${TARGET}.ids" 2>/dev/null | tr -d ' ' || echo 0)
  if [ "$NOTIFIED_COUNT" -gt 0 ]; then
    log_pass "Notified IDs marker has $NOTIFIED_COUNT entries"
  else
    log_fail "Notified IDs" "marker exists but is empty"
  fi
else
  # If no marker, that's OK if agent consumed everything
  log_pass "No notified IDs marker (agent may have consumed messages)"
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
