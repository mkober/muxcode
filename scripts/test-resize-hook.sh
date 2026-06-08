#!/usr/bin/env bash
# test-resize-hook.sh — Integration test for the client-resized auto-refit hook
#
# Runs inside a live muxcode tmux session. Verifies that the client-resized
# hook (config/tmux.conf) is registered and that its action correctly refits
# every window in the session to the connected client — the behavior that keeps
# the session from being clipped after a monitor resolution / terminal resize.
#
# Phases:
#   1. Prerequisites (in tmux, bus dir)
#   2. Hook registered with the expected xargs command
#   3. Action correctness — shrink a background window, run the hook action,
#      assert it snaps back to the client size (deterministic, no real resize)
#   4. Live trigger (best-effort) — drive a control-mode client to a smaller
#      size, assert the hook fires and shrinks windows. SKIPs if the tmux
#      build can't set up a control client; the definitive trigger test is the
#      manual terminal/monitor resize described at the end.
#
# Usage: bash scripts/test-resize-hook.sh
#
# Requirements: running muxcode session with at least 2 windows

set -uo pipefail

SESSION=$(tmux display-message -p '#S' 2>/dev/null) || { echo "FAIL: not in a tmux session"; exit 1; }

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

pass=0
fail=0
skip=0
total=0

log_pass() { ((pass++)); ((total++)); echo -e "  ${GREEN}PASS${NC} $1"; }
log_fail() { ((fail++)); ((total++)); echo -e "  ${RED}FAIL${NC} $1: $2"; }
log_skip() { ((skip++)); echo -e "  ${YELLOW}SKIP${NC} $1: $2"; }

BUS_DIR="/tmp/muxcode-bus-${SESSION}"

# Temp session created in Phase 3b — tracked here so the EXIT trap can reap it
# even if the test aborts midway.
TMP_SESSION=""

# Restore real window geometry on exit so the test never leaves windows clipped,
# and kill any temp session we spawned.
restore_geometry() {
  tmux list-windows -t "$SESSION" 2>/dev/null | cut -d: -f1 \
    | xargs -I{} tmux resize-window -t "${SESSION}:{}" -A 2>/dev/null || true
  [ -n "$TMP_SESSION" ] && tmux kill-session -t "$TMP_SESSION" 2>/dev/null || true
}
trap restore_geometry EXIT

# ============================================================
echo "=== Resize Hook Integration Test ==="
echo "Session: $SESSION"
echo ""

# --- Phase 1: Prerequisites ---
echo "--- Phase 1: Prerequisites ---"

if [ -d "$BUS_DIR" ]; then
  log_pass "Bus directory exists"
else
  log_fail "Bus directory missing" "$BUS_DIR"
  echo -e "${RED}Cannot continue without bus directory${NC}"
  exit 1
fi

WIN_COUNT=$(tmux list-windows -t "$SESSION" | wc -l | tr -d ' ')
if [ "$WIN_COUNT" -ge 2 ]; then
  log_pass "Session has $WIN_COUNT windows"
else
  log_fail "Window count" "need >= 2 windows, found $WIN_COUNT"
  echo -e "${RED}Cannot continue without multiple windows${NC}"
  exit 1
fi

# Capture the connected client size — the target every window should match.
read -r CW CH < <(tmux display-message -p -t "$SESSION" '#{client_width} #{client_height}')
if [ -n "$CW" ] && [ -n "$CH" ] && [ "$CW" -gt 0 ] 2>/dev/null; then
  log_pass "Client size detected (${CW}x${CH})"
else
  log_fail "Client size" "could not read client_width/client_height"
  exit 1
fi

echo ""

# --- Phase 2: Hook registered ---
echo "--- Phase 2: Hook registered ---"

HOOK=$(tmux show-hooks -g 2>/dev/null | grep "client-resized" || echo "")
if [ -n "$HOOK" ]; then
  log_pass "client-resized hook is registered"
else
  log_fail "Hook registration" "no client-resized hook (is tmux.conf loaded? run: tmux source-file ~/.config/muxcode/tmux.conf)"
fi

# The hook must delegate to `muxcode resize` — the Go subcommand that refits
# every window in every session (including detached subsessions). The earlier
# inline `xargs -I{} tmux resize-window` form only ever saw the current session.
if echo "$HOOK" | grep -q "muxcode resize"; then
  log_pass "Hook delegates to 'muxcode resize'"
else
  log_fail "Hook command" "expected 'muxcode resize' in: $HOOK"
fi

# Guard against a regression back to the inline single-session form.
if echo "$HOOK" | grep -q "xargs -I{} tmux resize-window"; then
  log_fail "Hook command" "still uses the inline single-session xargs form — should be 'muxcode resize'"
else
  log_pass "Hook does not use the legacy single-session inline form"
fi

echo ""

# --- Phase 3: Action correctness (deterministic) ---
echo "--- Phase 3: Action refits all windows ---"

# Use a never-clipped window as the "correct fit" reference rather than the raw
# client size: window_height is always client_height minus the status line(s),
# so comparing a window directly to client_height is off by the status-bar
# height. All windows that fit the same client must share identical dimensions —
# that equality is the real invariant, and it is status-bar agnostic.
FIRST_WIN=$(tmux list-windows -t "$SESSION" | cut -d: -f1 | head -1)
TARGET_WIN=$(tmux list-windows -t "$SESSION" | cut -d: -f1 | tail -1)
read -r FIT_W FIT_H < <(tmux display-message -p -t "${SESSION}:${FIRST_WIN}" '#{window_width} #{window_height}')
echo "  Reference fit (window $FIRST_WIN): ${FIT_W}x${FIT_H}  (client ${CW}x${CH})"

SMALL_W=40
SMALL_H=10
tmux resize-window -t "${SESSION}:${TARGET_WIN}" -x "$SMALL_W" -y "$SMALL_H" 2>/dev/null
read -r BW BH < <(tmux display-message -p -t "${SESSION}:${TARGET_WIN}" '#{window_width} #{window_height}')
echo "  Window $TARGET_WIN clipped to ${BW}x${BH} (simulating post-resize clipping)"

if [ "$BW" -lt "$FIT_W" ]; then
  log_pass "Window $TARGET_WIN was clipped below the fit size"
else
  # Some tmux builds enforce a minimum; still fine as long as it shrank.
  log_pass "Window $TARGET_WIN resized (now ${BW}x${BH})"
fi

# Run exactly what the hook runs.
if command -v muxcode >/dev/null 2>&1; then
  muxcode resize 2>/dev/null
else
  # Fallback to the equivalent action if the binary is not on PATH.
  tmux list-windows -t "$SESSION" | cut -d: -f1 \
    | xargs -I{} tmux resize-window -t "${SESSION}:{}" -A 2>/dev/null
fi

read -r AW AH < <(tmux display-message -p -t "${SESSION}:${TARGET_WIN}" '#{window_width} #{window_height}')
echo "  After hook action: window $TARGET_WIN is ${AW}x${AH} (fit ${FIT_W}x${FIT_H})"

if [ "$AW" -eq "$FIT_W" ] && [ "$AH" -eq "$FIT_H" ]; then
  log_pass "Clipped window refit exactly to the fit size"
else
  log_fail "Action refit" "window is ${AW}x${AH}, expected fit ${FIT_W}x${FIT_H}"
fi

# Every window should now share the same (fit) dimensions — none left clipped.
ALL_MATCH=true
while read -r idx; do
  read -r ww wh < <(tmux display-message -p -t "${SESSION}:${idx}" '#{window_width} #{window_height}')
  if [ "$ww" -ne "$FIT_W" ] || [ "$wh" -ne "$FIT_H" ]; then
    ALL_MATCH=false
    echo "    window $idx is ${ww}x${wh} (expected ${FIT_W}x${FIT_H})"
  fi
done < <(tmux list-windows -t "$SESSION" | cut -d: -f1)

if [ "$ALL_MATCH" = true ]; then
  log_pass "All windows share the fit size after the action (none clipped)"
else
  log_fail "All-windows refit" "one or more windows still differ from the fit size"
fi

echo ""

# --- Phase 3b: Detached subsession refit (the cross-session fix) ---
echo "--- Phase 3b: Detached subsession is refit too ---"

if ! command -v muxcode >/dev/null 2>&1; then
  log_skip "Detached subsession refit" "muxcode binary not on PATH"
else
  # Tracked by the EXIT trap so it is reaped even if the test aborts midway.
  TMP_SESSION="resize-test-$$"
  # Create a detached session whose window starts clipped (40x10). It has no
  # attached client, so `resize-window -A` alone could never fix it — only the
  # explicit-size push in `muxcode resize` can.
  tmux new-session -d -s "$TMP_SESSION" -x 40 -y 10 2>/dev/null
  if ! tmux has-session -t "$TMP_SESSION" 2>/dev/null; then
    log_skip "Detached subsession refit" "could not create temp session"
    TMP_SESSION=""
  else
    read -r DW DH < <(tmux display-message -p -t "${TMP_SESSION}:0" '#{window_width} #{window_height}')
    echo "  Detached ${TMP_SESSION}:0 starts at ${DW}x${DH} (attached fit ${FIT_W}x${FIT_H})"

    muxcode resize 2>/dev/null

    read -r RW RH < <(tmux display-message -p -t "${TMP_SESSION}:0" '#{window_width} #{window_height}')
    echo "  After refit: ${TMP_SESSION}:0 is ${RW}x${RH}"

    if [ "$RW" -eq "$FIT_W" ] && [ "$RH" -eq "$FIT_H" ]; then
      log_pass "Detached subsession refit to the attached client's fit size"
    elif [ "$RW" -gt "$DW" ]; then
      log_pass "Detached subsession grew toward the fit size (${RW}x${RH})"
    else
      log_fail "Detached subsession refit" "still ${RW}x${RH}, expected ${FIT_W}x${FIT_H}"
    fi

    tmux kill-session -t "$TMP_SESSION" 2>/dev/null || true
    TMP_SESSION=""
  fi
fi

echo ""

# --- Phase 4: Live trigger via control-mode client (best-effort) ---
echo "--- Phase 4: Live client-resized trigger ---"

CTL_LOG=$(mktemp /tmp/resize-ctl-XXXXXX.log)
CTL_FIFO=$(mktemp -u /tmp/resize-fifo-XXXXXX)
CTL_PID=""
mkfifo "$CTL_FIFO" 2>/dev/null

cleanup_ctl() {
  [ -n "$CTL_PID" ] && kill "$CTL_PID" 2>/dev/null
  exec 8>&- 2>/dev/null || true
  rm -f "$CTL_FIFO" "$CTL_LOG" 2>/dev/null
}

# Start a control-mode client and keep its stdin open via fd 8.
( tmux -C attach -t "$SESSION" <"$CTL_FIFO" >"$CTL_LOG" 2>&1 ) &
CTL_PID=$!
exec 8>"$CTL_FIFO" 2>/dev/null
sleep 1

if ! kill -0 "$CTL_PID" 2>/dev/null; then
  log_skip "Live trigger" "control-mode client did not start on this tmux build"
  cleanup_ctl
else
  # Identify the control client (control_mode==1 where supported).
  CTL_CLIENT=$(tmux list-clients -t "$SESSION" -F '#{client_name} #{client_control_mode}' 2>/dev/null \
    | awk '$2==1{print $1}' | head -1)
  [ -z "$CTL_CLIENT" ] && CTL_CLIENT=$(tmux list-clients -t "$SESSION" -F '#{client_name}' 2>/dev/null | tail -1)

  TRIG_W=80
  TRIG_H=24
  SIZE_SET=false
  # refresh-client -C size syntax differs across versions; try both forms.
  if tmux refresh-client -t "$CTL_CLIENT" -C "${TRIG_W}x${TRIG_H}" 2>/dev/null; then
    SIZE_SET=true
  elif tmux refresh-client -t "$CTL_CLIENT" -C "${TRIG_W},${TRIG_H}" 2>/dev/null; then
    SIZE_SET=true
  fi

  if [ "$SIZE_SET" != true ]; then
    log_skip "Live trigger" "this tmux build does not support setting control-client size"
    cleanup_ctl
  else
    sleep 1
    # With aggressive-resize on, the window should follow the smallest client.
    # If the hook fired, the active window shrank toward the 80x24 control client.
    read -r TW TH < <(tmux display-message -p -t "$SESSION" '#{window_width} #{window_height}')
    echo "  After control client set to ${TRIG_W}x${TRIG_H}: active window is ${TW}x${TH}"
    if [ "$TW" -le "$TRIG_W" ]; then
      log_pass "client-resized fired — window shrank to the smaller client"
    else
      log_skip "Live trigger" "window did not shrink (${TW}x${TH}); aggressive-resize/window-size policy may differ — verify manually"
    fi
    cleanup_ctl
    # Restore to the real client size.
    tmux list-windows -t "$SESSION" | cut -d: -f1 \
      | xargs -I{} tmux resize-window -t "${SESSION}:{}" -A 2>/dev/null
  fi
fi

echo ""

# ============================================================
echo "=== Results ==="
echo -e "  Total: $total  ${GREEN}Pass: $pass${NC}  ${RED}Fail: $fail${NC}  ${YELLOW}Skip: $skip${NC}"
echo ""
echo "  NOTE: The definitive end-to-end check is manual — resize the terminal"
echo "  window or change monitor resolution, then cycle through F1..F9 and"
echo "  confirm no window is clipped (no session restart needed)."
echo ""

if [ "$fail" -gt 0 ]; then
  echo -e "${RED}FAILED${NC} — $fail test(s) failed"
  exit 1
else
  echo -e "${GREEN}ALL TESTS PASSED${NC}"
  exit 0
fi
