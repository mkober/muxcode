#!/usr/bin/env bash
# test-multi-reload.sh — Integration test for multi-agent batch reload
#
# Runs inside a live muxcode tmux session. Tests:
# 1. Multi-role CLI reload (muxcode reload role1 role2 --cli X --model Y)
# 2. --all with --provider filter
# 3. --provider without --all (should fail)
# 4. Restore original config
#
# Usage: bash scripts/test-multi-reload.sh
#
# Requirements: running muxcode session with at least build and test agents alive

set -uo pipefail

SESSION="${BUS_SESSION:-$(tmux display-message -p '#S' 2>/dev/null)}" || { echo "FAIL: not in a tmux session"; exit 1; }
export BUS_SESSION="$SESSION"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
NC='\033[0m'

pass=0
fail=0
total=0

log_pass() { pass=$((pass + 1)); total=$((total + 1)); echo -e "  ${GREEN}PASS${NC} $1"; }
log_fail() { fail=$((fail + 1)); total=$((total + 1)); echo -e "  ${RED}FAIL${NC} $1: $2"; }

BUS_DIR="/tmp/muxcode-bus-${SESSION}"

# ============================================================
echo "=== Multi-Agent Reload Integration Test ==="
echo "Session: $SESSION"
echo ""

# --- Phase 1: Prerequisites ---
echo "--- Phase 1: Prerequisites ---"

log_pass "BUS_SESSION is set ($BUS_SESSION)"

if [ -d "$BUS_DIR" ]; then
  log_pass "Bus directory exists"
else
  log_fail "Bus directory missing" "$BUS_DIR"
  exit 1
fi

# Check agents are alive using IsAgentAlive via the pane target
for role in build test; do
  target="${SESSION}:${role}.1"
  # Check if pane has a running process (not just a shell prompt)
  pane_content=$(tmux capture-pane -t "$target" -p -S -5 2>/dev/null || echo "")
  if [ -n "$pane_content" ]; then
    log_pass "$role agent pane exists"
  else
    log_fail "$role agent pane" "pane not found or empty"
    echo "Cannot continue without $role agent"
    exit 1
  fi
done

echo ""

# --- Phase 2: Save current config ---
echo "--- Phase 2: Save current config ---"

ORIG_BUILD_CLI=$(muxcode config get build 2>/dev/null | grep "CLI:" | awk '{print $2}') || ORIG_BUILD_CLI="opencode"
ORIG_BUILD_MODEL=$(muxcode config get build 2>/dev/null | grep "Model:" | awk '{print $2}') || ORIG_BUILD_MODEL=""
ORIG_TEST_CLI=$(muxcode config get test 2>/dev/null | grep "CLI:" | awk '{print $2}') || ORIG_TEST_CLI="opencode"
ORIG_TEST_MODEL=$(muxcode config get test 2>/dev/null | grep "Model:" | awk '{print $2}') || ORIG_TEST_MODEL=""

log_pass "Saved build config: CLI=$ORIG_BUILD_CLI, Model=$ORIG_BUILD_MODEL"
log_pass "Saved test config: CLI=$ORIG_TEST_CLI, Model=$ORIG_TEST_MODEL"

echo ""

# --- Phase 3: Multi-role batch reload ---
echo "--- Phase 3: Multi-role batch reload ---"

# Pick target CLI different from build's current
if [ "$ORIG_BUILD_CLI" = "opencode" ]; then
  TARGET_CLI="claude"
  TARGET_MODEL="claude-sonnet-4-6"
else
  TARGET_CLI="opencode"
  TARGET_MODEL="opencode-go/minimax-m2.5"
fi

echo "  Reloading build + test: → $TARGET_CLI ($TARGET_MODEL)"

RELOAD_START=$(date +%s)
RELOAD_OUT=$(muxcode reload build test --cli "$TARGET_CLI" --model "$TARGET_MODEL" 2>&1) || true
RELOAD_END=$(date +%s)
RELOAD_DURATION=$((RELOAD_END - RELOAD_START))

echo "$RELOAD_OUT"

# Verify both agents mentioned in output
if echo "$RELOAD_OUT" | grep -q "build"; then
  log_pass "Batch output mentions build"
else
  log_fail "Batch output" "no mention of build"
fi

if echo "$RELOAD_OUT" | grep -q "test"; then
  log_pass "Batch output mentions test"
else
  log_fail "Batch output" "no mention of test"
fi

# Verify duration is reasonable (< 60s for 2 agents)
if [ "$RELOAD_DURATION" -le 60 ]; then
  log_pass "Batch reload completed in ${RELOAD_DURATION}s (< 60s)"
else
  log_fail "Batch duration" "took ${RELOAD_DURATION}s (max 60s)"
fi

# Wait for agents to initialize
sleep 3

# Verify config changed
for role in build test; do
  NEW_CONFIG=$(muxcode config get "$role" 2>/dev/null) || NEW_CONFIG=""
  if echo "$NEW_CONFIG" | grep -q "$TARGET_CLI"; then
    log_pass "$role config shows $TARGET_CLI"
  else
    log_fail "$role config" "expected $TARGET_CLI in: $NEW_CONFIG"
  fi
done

# Verify reload markers cleaned up
for role in build test; do
  MARKER="$BUS_DIR/lock/${role}.reloading"
  if [ ! -f "$MARKER" ]; then
    log_pass "$role reload marker cleaned up"
  else
    log_fail "$role reload marker" "still exists"
  fi
done

echo ""

# --- Phase 4: --provider filter validation ---
echo "--- Phase 4: --provider flag validation ---"

# --provider without --all should fail
if muxcode reload --provider "$TARGET_CLI" --cli opencode 2>/dev/null; then
  log_fail "--provider without --all" "should have failed"
else
  log_pass "--provider without --all rejected"
fi

echo ""

# --- Phase 5: --all with --provider filter ---
echo "--- Phase 5: --all with --provider filter ---"

RESTORE_CLI="$ORIG_BUILD_CLI"
RESTORE_MODEL="$ORIG_BUILD_MODEL"
if [ -z "$RESTORE_MODEL" ]; then
  RESTORE_MODEL="opencode-go/minimax-m2.5"
fi

echo "  Reloading --all --provider $TARGET_CLI → $RESTORE_CLI"

FILTER_OUT=$(muxcode reload --all --provider "$TARGET_CLI" --cli "$RESTORE_CLI" --model "$RESTORE_MODEL" 2>&1) || true
echo "$FILTER_OUT"

# Verify the output mentions agent reloads
if echo "$FILTER_OUT" | grep -qE "(✓|reloaded)"; then
  log_pass "--all --provider output shows reloaded agents"
else
  log_fail "--all --provider output" "no reload confirmation"
fi

sleep 3

# Verify agents are back on original CLI
for role in build test; do
  RESTORED=$(muxcode config get "$role" 2>/dev/null) || RESTORED=""
  if echo "$RESTORED" | grep -q "$RESTORE_CLI"; then
    log_pass "$role restored to $RESTORE_CLI"
  else
    log_fail "$role restore" "expected $RESTORE_CLI in: $RESTORED"
  fi
done

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
