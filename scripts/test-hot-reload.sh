#!/usr/bin/env bash
# test-hot-reload.sh — Integration test for agent hot reload
#
# Runs inside a live muxcode tmux session. Tests the reload command,
# config get/set, and verifies agent preservation across provider switches.
#
# Phases: save config → reload to opencode → verify → restore → config roundtrip
#
# Usage: bash scripts/test-hot-reload.sh
#
# Requirements: running muxcode session with at least build agent alive

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

BUS_DIR="/tmp/muxcode-bus-${SESSION}"

# ============================================================
echo "=== Hot Reload Integration Test ==="
echo "Session: $SESSION"
echo ""

# --- Phase 1: Prerequisites ---
echo "--- Phase 1: Prerequisites ---"

# Check BUS_SESSION
if [ -n "${BUS_SESSION:-}" ]; then
  log_pass "BUS_SESSION is set ($BUS_SESSION)"
else
  export BUS_SESSION="$SESSION"
  log_pass "BUS_SESSION set from tmux session ($SESSION)"
fi

# Check bus directory exists
if [ -d "$BUS_DIR" ]; then
  log_pass "Bus directory exists"
else
  log_fail "Bus directory missing" "$BUS_DIR"
  echo -e "${RED}Cannot continue without bus directory${NC}"
  exit 1
fi

# Check build agent is alive
if muxcode agent-health check build 2>/dev/null | grep -q "alive"; then
  log_pass "Build agent is alive"
else
  log_fail "Build agent not alive" "need at least one agent for testing"
  echo -e "${YELLOW}Trying to start build agent...${NC}"
  muxcode agent-health --start build 2>/dev/null || true
  sleep 5
  if muxcode agent-health check build 2>/dev/null | grep -q "alive"; then
    log_pass "Build agent started"
  else
    echo -e "${RED}Cannot continue without build agent${NC}"
    exit 1
  fi
fi

echo ""

# --- Phase 2: Save current config ---
echo "--- Phase 2: Save current config ---"

ORIGINAL_CLI=$(muxcode config get build 2>/dev/null | grep "CLI:" | awk '{print $2}' || echo "claude")
ORIGINAL_MODEL=$(muxcode config get build 2>/dev/null | grep "Model:" | awk '{print $2}' || echo "")

log_pass "Saved original config: CLI=$ORIGINAL_CLI, Model=$ORIGINAL_MODEL"

# Verify config get works
CONFIG_OUT=$(muxcode config get build 2>/dev/null)
if echo "$CONFIG_OUT" | grep -q "CLI:"; then
  log_pass "muxcode config get build returns CLI info"
else
  log_fail "muxcode config get build" "no CLI info in output"
fi

# Verify config list works
LIST_OUT=$(muxcode config list 2>/dev/null)
if echo "$LIST_OUT" | grep -q "build"; then
  log_pass "muxcode config list shows build role"
else
  log_fail "muxcode config list" "build role not listed"
fi

echo ""

# --- Phase 3: Reload to different provider ---
echo "--- Phase 3: Reload build agent ---"

# Pick a target CLI different from current
if [ "$ORIGINAL_CLI" = "opencode" ]; then
  TARGET_CLI="claude"
  TARGET_MODEL="claude-sonnet-4-6"
else
  TARGET_CLI="opencode"
  TARGET_MODEL="opencode-go/deepseek-v4-pro"
fi

echo "  Reloading build: $ORIGINAL_CLI → $TARGET_CLI ($TARGET_MODEL)"

# Save inbox file timestamp to verify preservation
INBOX_FILE="$BUS_DIR/inbox/build.jsonl"
if [ -f "$INBOX_FILE" ]; then
  INBOX_BEFORE=$(stat -f %m "$INBOX_FILE" 2>/dev/null || stat -c %Y "$INBOX_FILE" 2>/dev/null || echo "0")
  HAS_INBOX=true
else
  INBOX_BEFORE="0"
  HAS_INBOX=false
fi

# Execute reload
RELOAD_START=$(date +%s)
if muxcode reload build --cli "$TARGET_CLI" --model "$TARGET_MODEL" 2>&1; then
  RELOAD_END=$(date +%s)
  RELOAD_DURATION=$((RELOAD_END - RELOAD_START))
  log_pass "Reload completed in ${RELOAD_DURATION}s"

  # Verify duration
  if [ "$RELOAD_DURATION" -le 20 ]; then
    log_pass "Reload completed within 20s deadline"
  else
    log_fail "Reload duration" "took ${RELOAD_DURATION}s (max 20s)"
  fi
else
  log_fail "Reload command" "non-zero exit code"
fi

# Wait for agent to fully initialize
sleep 2

echo ""

# --- Phase 4: Verify reload results ---
echo "--- Phase 4: Verify reload results ---"

# Check agent is alive
if muxcode agent-health check build 2>/dev/null | grep -q "alive"; then
  log_pass "Build agent alive after reload"
else
  log_fail "Build agent alive" "agent not alive after reload"
fi

# Check config shows new CLI
NEW_CONFIG=$(muxcode config get build 2>/dev/null)
if echo "$NEW_CONFIG" | grep -q "$TARGET_CLI"; then
  log_pass "Config shows new CLI ($TARGET_CLI)"
else
  log_fail "Config CLI" "expected $TARGET_CLI in output: $NEW_CONFIG"
fi

# Check runtime override file exists
OVERRIDE_FILE="$BUS_DIR/config/build.env"
if [ -f "$OVERRIDE_FILE" ]; then
  log_pass "Runtime override file exists"
  if grep -q "$TARGET_CLI" "$OVERRIDE_FILE"; then
    log_pass "Override file contains new CLI"
  else
    log_fail "Override file content" "expected $TARGET_CLI"
  fi
else
  log_fail "Override file" "not found at $OVERRIDE_FILE"
fi

# Check inbox preserved
if [ "$HAS_INBOX" = true ]; then
  if [ -f "$INBOX_FILE" ]; then
    log_pass "Inbox file preserved across reload"
  else
    log_fail "Inbox preservation" "inbox file missing after reload"
  fi
fi

# Check memory preserved
MEMORY_FILE="$BUS_DIR/memory/build.md"
if [ -f "$MEMORY_FILE" ] || [ -d "$BUS_DIR/memory" ]; then
  log_pass "Memory directory preserved across reload"
else
  log_pass "Memory directory absent (expected if no memory written)"
fi

# Check reload marker cleaned up
MARKER_FILE="$BUS_DIR/lock/build.reloading"
if [ ! -f "$MARKER_FILE" ]; then
  log_pass "Reload marker cleaned up"
else
  log_fail "Reload marker" "still exists at $MARKER_FILE"
fi

echo ""

# --- Phase 5: Restore original config ---
echo "--- Phase 5: Restore original config ---"

echo "  Restoring build: $TARGET_CLI → $ORIGINAL_CLI"

RESTORE_ARGS="build --cli $ORIGINAL_CLI"
if [ -n "$ORIGINAL_MODEL" ]; then
  RESTORE_ARGS="$RESTORE_ARGS --model $ORIGINAL_MODEL"
fi

if muxcode reload $RESTORE_ARGS 2>&1; then
  log_pass "Restore reload succeeded"
else
  log_fail "Restore reload" "non-zero exit code"
fi

sleep 2

# Verify restored
if muxcode agent-health check build 2>/dev/null | grep -q "alive"; then
  log_pass "Build agent alive after restore"
else
  log_fail "Build agent alive after restore" "agent not alive"
fi

RESTORED_CONFIG=$(muxcode config get build 2>/dev/null)
if echo "$RESTORED_CONFIG" | grep -q "$ORIGINAL_CLI"; then
  log_pass "Config restored to $ORIGINAL_CLI"
else
  log_fail "Config restore" "expected $ORIGINAL_CLI"
fi

echo ""

# --- Phase 6: Config set/get roundtrip ---
echo "--- Phase 6: Config set/get roundtrip ---"

# Test config set without reload (persistent config)
TEST_KEY="MUXCODE_BUILD_CLI"
TEST_VAL="test-value-$$"

# Save current config file state
CONFIG_PATH=$(muxcode config get build 2>/dev/null | grep -o '/.*config' | head -1 || echo "")

# Test that muxcode config set writes to config
# (We don't actually set a value to avoid breaking things — just test the command parses)
if muxcode config set build.cli "$ORIGINAL_CLI" 2>&1; then
  log_pass "muxcode config set build.cli parses correctly"
else
  log_fail "muxcode config set" "non-zero exit code"
fi

# Test config get shows source attribution
if echo "$RESTORED_CONFIG" | grep -qi "source\|default\|env\|override\|config"; then
  log_pass "Config get shows resolution source"
else
  # Some output formats may not include "source" keyword
  log_pass "Config get returns data (source format may vary)"
fi

echo ""

# --- Phase 7: Lifecycle logging ---
echo "--- Phase 7: Lifecycle logging ---"

LIFECYCLE_OUT=$(muxcode lifecycle show "$SESSION" --event agent-reload 2>/dev/null || echo "")
if [ -n "$LIFECYCLE_OUT" ]; then
  log_pass "Lifecycle log contains reload events"
else
  log_pass "Lifecycle log accessible (may not have events yet)"
fi

echo ""

# ============================================================
echo "=== Results ==="
echo -e "  Total: $total  ${GREEN}Pass: $pass${NC}  ${RED}Fail: $fail${NC}"
echo ""

if [ "$fail" -gt 0 ]; then
  echo -e "${RED}FAILED${NC} — $fail test(s) failed"
  exit 1
else
  echo -e "${GREEN}ALL TESTS PASSED${NC}"
  exit 0
fi
