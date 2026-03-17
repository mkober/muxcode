#!/usr/bin/env bash
# test-diff-split.sh — Integration test for nvim diff split preview
#
# Runs inside a live muxcode tmux session. Creates a test file, simulates
# the PreToolUse (preview) and PostToolUse (analyze) hook events, and
# verifies nvim state at each stage.
#
# Phases: setup → preview → analyze → stale cleanup → skip patterns → write tool → teardown
#
# Usage: bash scripts/test-diff-split.sh
#
# Requirements: running muxcode session with nvim in edit.0

set -euo pipefail

SESSION=$(tmux display-message -p '#S' 2>/dev/null) || { echo "FAIL: not in a tmux session"; exit 1; }
PANE="$SESSION:edit.0"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
TEMP_FILE="/tmp/muxcode-preview-${SESSION}.tmp"
TEST_FILE="/tmp/muxcode-diff-test-$$-file.txt"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

pass=0
fail=0
total=0

NVIM_OUT="/tmp/muxcode-diff-test-$$-nvim-out"

log_pass() { ((pass++)); ((total++)); echo -e "  ${GREEN}PASS${NC} $1"; }
log_fail() { ((fail++)); ((total++)); echo -e "  ${RED}FAIL${NC} $1: $2"; }

# Evaluate a vim expression and return the result via temp file (more reliable than pane capture)
nvim_eval() {
  rm -f "$NVIM_OUT"
  tmux send-keys -t "$PANE" ":call writefile([string($1)], '$NVIM_OUT')" Enter
  sleep 0.3
  [ -f "$NVIM_OUT" ] && cat "$NVIM_OUT" || echo "(no output)"
}

nvim_window_count() {
  tmux send-keys -t "$PANE" Escape Escape
  sleep 0.1
  nvim_eval "winnr('$')"
}

nvim_diff_mode() {
  nvim_eval "&diff"
}

cleanup() {
  echo ""
  echo "--- Cleanup ---"
  # Ensure nvim is back to a clean state
  tmux send-keys -t "$PANE" Escape Escape
  sleep 0.1
  tmux send-keys -t "$PANE" ":sil! exe 'b!'.get(g:,'_mux_buf',bufnr()) | sil! diffoff! | sil! only | sil! set number" Enter
  sleep 0.3
  rm -f "$TEST_FILE" "$TEMP_FILE" "$NVIM_OUT"
  echo "  Cleaned up test files and nvim state"
}

trap cleanup EXIT

# ============================================================
echo "=== Diff Split Integration Test ==="
echo "Session: $SESSION"
echo ""

# --- Setup: create a test file ---
cat > "$TEST_FILE" << 'TESTEOF'
package main

import "fmt"

func hello() {
    fmt.Println("Hello, World!")
}

func goodbye() {
    fmt.Println("Goodbye, World!")
}
TESTEOF

echo "--- Phase 0: Setup ---"
echo "  Created test file: $TEST_FILE"

# Open the test file in nvim first
tmux send-keys -t "$PANE" Escape Escape
sleep 0.1
tmux send-keys -t "$PANE" ":e! $TEST_FILE" Enter
sleep 0.5

# Verify file is open
buf_name=$(nvim_eval "expand('%:p')")
if [[ "$buf_name" == *"$TEST_FILE"* ]]; then
  log_pass "Test file opened in nvim"
else
  log_fail "Test file opened in nvim" "got: $buf_name"
fi

# ============================================================
echo ""
echo "--- Phase 1: PreToolUse Preview Hook (Edit tool) ---"

# Simulate an Edit tool event
EDIT_EVENT=$(cat << EVENTEOF
{
  "tool_name": "Edit",
  "tool_input": {
    "file_path": "$TEST_FILE",
    "old_string": "    fmt.Println(\"Hello, World!\")",
    "new_string": "    fmt.Println(\"Hello, MuxCode!\")\n    fmt.Println(\"Welcome aboard!\")"
  }
}
EVENTEOF
)

# Remove any stale temp file
rm -f "$TEMP_FILE"

# Run the preview hook
echo "$EDIT_EVENT" | TMUX_PANE=$(tmux display-message -t "$PANE" -p '#{pane_id}') bash "$SCRIPT_DIR/muxcode-preview-hook.sh"

# Wait for all the sleeps in the hook to complete
sleep 1.5

# Test 1: Temp file should exist
if [ -f "$TEMP_FILE" ]; then
  log_pass "Temp preview file created"
else
  log_fail "Temp preview file created" "file not found at $TEMP_FILE"
fi

# Test 2: Temp file should contain the new string
if [ -f "$TEMP_FILE" ] && grep -q "Hello, MuxCode" "$TEMP_FILE"; then
  log_pass "Temp file contains proposed change"
else
  log_fail "Temp file contains proposed change" "new_string not found in temp file"
fi

# Test 3: Temp file should NOT contain the old string
if [ -f "$TEMP_FILE" ] && ! grep -q 'Hello, World' "$TEMP_FILE"; then
  log_pass "Temp file replaced old string"
else
  log_fail "Temp file replaced old string" "old_string still present"
fi

# Test 4: nvim should have 2 windows (diff split)
win_count=$(nvim_window_count)
if [ "$win_count" = "2" ]; then
  log_pass "Diff split opened (2 windows)"
else
  log_fail "Diff split opened (2 windows)" "window count: $win_count"
fi

# Test 5: diff mode should be active
diff_on=$(nvim_diff_mode)
if [ "$diff_on" = "1" ]; then
  log_pass "Diff mode is active"
else
  log_fail "Diff mode is active" "diff mode: $diff_on"
fi

# ============================================================
echo ""
echo "--- Phase 2: PostToolUse Analyze Hook (accepted edit) ---"

# First, apply the edit to the actual file so the analyze hook can find the new content
sed -i '' 's/Hello, World!/Hello, MuxCode!/' "$TEST_FILE"
# Add the extra line
sed -i '' '/Hello, MuxCode/a\
    fmt.Println("Welcome aboard!")' "$TEST_FILE"

# Simulate the PostToolUse event (accepted edit)
ANALYZE_EVENT=$(cat << EVENTEOF
{
  "tool_name": "Edit",
  "tool_input": {
    "file_path": "$TEST_FILE",
    "old_string": "    fmt.Println(\"Hello, World!\")",
    "new_string": "    fmt.Println(\"Hello, MuxCode!\")\n    fmt.Println(\"Welcome aboard!\")"
  }
}
EVENTEOF
)

echo "$ANALYZE_EVENT" | TMUX_PANE=$(tmux display-message -t "$PANE" -p '#{pane_id}') bash "$SCRIPT_DIR/muxcode-analyze-hook.sh"

# Wait for the analyze hook (has a 1s sleep + commands)
sleep 2

# Test 6: Temp file should be cleaned up
if [ ! -f "$TEMP_FILE" ]; then
  log_pass "Temp preview file cleaned up"
else
  log_fail "Temp preview file cleaned up" "file still exists"
fi

# Test 7: nvim should be back to 1 window
win_count=$(nvim_window_count)
if [ "$win_count" = "1" ]; then
  log_pass "Diff split closed (1 window)"
else
  log_fail "Diff split closed (1 window)" "window count: $win_count"
fi

# Test 8: diff mode should be off
diff_on=$(nvim_diff_mode)
if [ "$diff_on" = "0" ]; then
  log_pass "Diff mode is off"
else
  log_fail "Diff mode is off" "diff mode: $diff_on"
fi

# ============================================================
echo ""
echo "--- Phase 3: Stale diff cleanup (rejected edit simulation) ---"

# Re-open the test file fresh
tmux send-keys -t "$PANE" Escape Escape
sleep 0.1
tmux send-keys -t "$PANE" ":e! $TEST_FILE" Enter
sleep 0.3

# Run preview hook again to create a new diff
EDIT_EVENT2=$(cat << EVENTEOF
{
  "tool_name": "Edit",
  "tool_input": {
    "file_path": "$TEST_FILE",
    "old_string": "    fmt.Println(\"Goodbye, World!\")",
    "new_string": "    fmt.Println(\"See you later!\")"
  }
}
EVENTEOF
)

rm -f "$TEMP_FILE"
echo "$EDIT_EVENT2" | TMUX_PANE=$(tmux display-message -t "$PANE" -p '#{pane_id}') bash "$SCRIPT_DIR/muxcode-preview-hook.sh"
sleep 1.5

# Verify diff is open
win_count=$(nvim_window_count)
if [ "$win_count" = "2" ]; then
  log_pass "Second diff split opened for rejection test"
else
  log_fail "Second diff split opened for rejection test" "window count: $win_count"
fi

# Now simulate a Read tool event (which should trigger diff cleanup)
# Artificially age the temp file so the cleanup detects it as stale
touch -t 202001010000 "$TEMP_FILE" 2>/dev/null || true

READ_EVENT='{"tool_name": "Read", "tool_input": {"file_path": "/tmp/anything.txt"}}'
echo "$READ_EVENT" | TMUX_PANE=$(tmux display-message -t "$PANE" -p '#{pane_id}') bash "$SCRIPT_DIR/muxcode-diff-cleanup.sh"
sleep 0.5

# Test 9: Diff cleanup should have closed the diff
win_count=$(nvim_window_count)
if [ "$win_count" = "1" ]; then
  log_pass "Stale diff cleaned up by diff-cleanup hook"
else
  log_fail "Stale diff cleaned up by diff-cleanup hook" "window count: $win_count"
fi

# Test 10: Temp file should be removed
if [ ! -f "$TEMP_FILE" ]; then
  log_pass "Stale temp file removed by diff-cleanup hook"
else
  log_fail "Stale temp file removed by diff-cleanup hook" "file still exists"
fi

# ============================================================
echo ""
echo "--- Phase 4: Skip patterns ---"

# Test that skip patterns prevent preview
SKIP_EVENT=$(cat << EVENTEOF
{
  "tool_name": "Edit",
  "tool_input": {
    "file_path": "/Users/test/.claude/settings.json",
    "old_string": "\"key\": \"old\"",
    "new_string": "\"key\": \"new\""
  }
}
EVENTEOF
)

rm -f "$TEMP_FILE"
echo "$SKIP_EVENT" | TMUX_PANE=$(tmux display-message -t "$PANE" -p '#{pane_id}') bash "$SCRIPT_DIR/muxcode-preview-hook.sh"
sleep 0.3

# Test 11: Temp file should NOT exist (skipped)
if [ ! -f "$TEMP_FILE" ]; then
  log_pass "Skip pattern prevented preview for settings.json"
else
  log_fail "Skip pattern prevented preview for settings.json" "temp file was created"
  rm -f "$TEMP_FILE"
fi

# ============================================================
echo ""
echo "--- Phase 5: Write tool (no diff, just open file) ---"

WRITE_EVENT=$(cat << EVENTEOF
{
  "tool_name": "Write",
  "tool_input": {
    "file_path": "$TEST_FILE",
    "content": "package main\n\nfunc main() {}\n"
  }
}
EVENTEOF
)

rm -f "$TEMP_FILE"
echo "$WRITE_EVENT" | TMUX_PANE=$(tmux display-message -t "$PANE" -p '#{pane_id}') bash "$SCRIPT_DIR/muxcode-preview-hook.sh"
sleep 0.8

# Test 12: No temp file for Write (no old_string)
if [ ! -f "$TEMP_FILE" ]; then
  log_pass "Write tool: no diff preview (expected)"
else
  log_fail "Write tool: no diff preview (expected)" "temp file was created"
  rm -f "$TEMP_FILE"
fi

# Test 13: nvim should have 1 window (no diff split for Write)
win_count=$(nvim_window_count)
if [ "$win_count" = "1" ]; then
  log_pass "Write tool: single window (no split)"
else
  log_fail "Write tool: single window (no split)" "window count: $win_count"
fi

# ============================================================
echo ""
echo "=========================================="
echo -e "Results: ${GREEN}$pass passed${NC}, ${RED}$fail failed${NC}, $total total"
echo "=========================================="

[ "$fail" -eq 0 ] && exit 0 || exit 1
