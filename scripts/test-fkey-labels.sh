#!/usr/bin/env bash
# Integration test for MUX-134 — the status-bar F-key label matches the
# key that actually selects the window.
#
# Constructs the observed divergent layout (a non-spawn window parked at
# index 11, the sole spawn at 12, a beyond-F12 window at 13 carrying a
# lying label) and proves, against a real `muxcode watch` daemon sweep:
#   A. @muxcode_fkey reconciles to WindowFKey's answer — the spawn at 12
#      gets F11 (not the raw-index F12), non-spawn 11+ windows and the
#      beyond-F12 window are cleared to the honest empty label, and
#      windows 1-10 keep the identity mapping.
#   B. The rendered window-status-format carries that label — and no
#      F-key at all for unbound windows.
#   C. Pressing the labelled key selects the labelled window. Keys are
#      sent through a nested attached client (send-keys to a pane
#      bypasses the key table; only a client exercises `bind -n`), so
#      this drives the real F11/F12 run-shell bindings end to end —
#      including the empty-slot F12 press being a no-op, the reported
#      symptom shape.
#   D. Freshness: killing the spawn and starting another at a new index
#      re-labels it F11 within one sweep, and F11 then selects it.
#
# Hermetic: private tmux servers under a scratch TMUX_TMPDIR (the inner
# default socket hosts the layout + daemon; a second -L socket hosts the
# driver client), scratch BUS_SESSION, lifecycle log and repo dir in a
# temp dir. No live muxcode session is touched.
#
# REQUIRES: tmux, and the installed muxcode binary to include MUX-134
# (run ./build.sh first).
#
# Usage: bash scripts/test-fkey-labels.sh
set -uo pipefail

PASS=0
FAIL=0
ok()   { PASS=$((PASS + 1)); echo "  ok: $1"; }
fail() { FAIL=$((FAIL + 1)); echo "  FAIL: $1"; }

command -v tmux >/dev/null 2>&1 || { echo "SKIP: tmux is required"; exit 2; }
MUX="${MUXCODE_BIN:-muxcode}"
MUX="$(command -v "$MUX" 2>/dev/null || echo "$MUX")"
[ -x "$MUX" ] || { echo "SKIP: muxcode not installed — set MUXCODE_BIN"; exit 2; }
if ! grep -aq "@muxcode_fkey" "$MUX"; then
  echo "SKIP: installed muxcode lacks MUX-134 F-key labels — run ./build.sh"
  exit 2
fi
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CONF="$ROOT/config/tmux.conf"
[ -f "$CONF" ] || { echo "SKIP: $CONF not found"; exit 2; }

# --- Isolation -------------------------------------------------------------
SESSION="fkey-labels-$$"
export BUS_SESSION="$SESSION"
export AGENT_ROLE=edit BUS_ROLE=edit
WORK=$(mktemp -d /tmp/fkey-labels-XXXXXX)
mkdir -p "$WORK/tmux" "$WORK/repo"
# Private tmux server: every plain `tmux` call here AND in the daemon
# resolves to the scratch default socket under this dir — but only with
# $TMUX unset, or the CLI would follow it back to the live session.
export TMUX_TMPDIR="$WORK/tmux"
unset TMUX
export MUXCODE_LIFECYCLE_LOG_DIR="$WORK/lifecycle"
: > "$WORK/empty-config"
export MUXCODE_CONFIG="$WORK/empty-config"
export MUXCODE_SESSION_REPO_DIR="$WORK/repo"
export MUXCODE_TMP_CLEANUP_THRESHOLD=0
export MUXCODE_BRANCH_TIME_DISABLE=1
export MUXCODE_CONTROL_PANE_DISABLE=1
export MUXCODE_FORCE_RESPOND_DISABLE=1
OUTSOCK="fkey-drv-$$"
BUSDIR="/tmp/muxcode-bus-$SESSION"
LIFELOG="$MUXCODE_LIFECYCLE_LOG_DIR/$SESSION.log"

DPID=""
cleanup() {
  [ -n "$DPID" ] && kill "$DPID" 2>/dev/null
  # Kill sessions, not servers: the sourced tmux.conf aliases kill-server
  # to a confirm-before prompt. A server exits with its last session.
  tmux -L "$OUTSOCK" kill-session -t driver 2>/dev/null
  tmux kill-session -t "$SESSION" 2>/dev/null
  sleep 0.3
  rm -rf "$BUSDIR" "$WORK"
}
trap cleanup EXIT

fkey_opt()   { tmux show-options -wqv -t "$SESSION:$1" @muxcode_fkey 2>/dev/null; }
active_idx() { tmux list-windows -t "$SESSION" -F '#{window_active} #{window_index}' 2>/dev/null | awk '$1==1{print $2}'; }
render()     { tmux display-message -p -t "$SESSION:$1" "$2" 2>/dev/null; }
press()      { tmux -L "$OUTSOCK" send-keys -t driver:0 "$1"; }
wait_opt() { # <window-index> <want> <secs>
  local i
  for ((i = 0; i < $3 * 2; i++)); do
    [ "$(fkey_opt "$1")" = "$2" ] && return 0
    sleep 0.5
  done
  return 1
}
wait_active() { # <window-index> <secs>
  local i
  for ((i = 0; i < $2 * 2; i++)); do
    [ "$(active_idx)" = "$1" ] && return 0
    sleep 0.5
  done
  return 1
}

echo "=== F-key label integration test (MUX-134) ==="

# --- Divergent layout: the observed incident shape ------------------------
# hold@0, first@1, third@3, parked@11 (non-spawn past F10), the sole
# spawn at 12, notes@13 beyond the last binding. The spawn and notes are
# pre-seeded with the lying raw-index labels the fix must correct.
tmux -f /dev/null new-session -d -s "$SESSION" -n hold -x 120 -y 30
tmux source-file "$CONF"
tmux new-window -d -t "$SESSION:1" -n first
tmux new-window -d -t "$SESSION:3" -n third
tmux new-window -d -t "$SESSION:11" -n parked
tmux new-window -d -t "$SESSION:12" -n spawn-alpha
tmux new-window -d -t "$SESSION:13" -n notes
tmux set-option -w -t "$SESSION:12" @muxcode_fkey F12
tmux set-option -w -t "$SESSION:13" @muxcode_fkey F13

echo "-- preconditions"
# The stale seeds must be readable back, or the clear/correct assertions
# below could false-pass against options that were never set.
if [ "$(fkey_opt 12)" = "F12" ]; then
  ok "stale lying F12 seeded on spawn-alpha@12"
else
  fail "stale lying F12 seeded on spawn-alpha@12 (got: '$(fkey_opt 12)')"
fi
if [ "$(fkey_opt 13)" = "F13" ]; then
  ok "stale lying F13 seeded on notes@13"
else
  fail "stale lying F13 seeded on notes@13 (got: '$(fkey_opt 13)')"
fi

"$MUX" init "$SESSION" >/dev/null 2>&1 || true
"$MUX" watch "$SESSION" --poll 2 >"$WORK/daemon.log" 2>&1 &
DPID=$!
disown "$DPID" # keep the cleanup kill out of job-control output

# --- A. Daemon sweep reconciles the options to WindowFKey -----------------
echo "-- option reconciliation (daemon sweep)"
if wait_opt 12 "F11" 20; then
  ok "spawn-alpha@12 relabelled F11 — the ordinal key, not the raw index"
else
  fail "spawn-alpha@12 relabelled F11 (got: '$(fkey_opt 12)')"
fi
if [ "$(fkey_opt 11)" = "" ]; then
  ok "parked@11 (non-spawn past F10) carries no label"
else
  fail "parked@11 carries no label (got: '$(fkey_opt 11)')"
fi
if [ "$(fkey_opt 13)" = "" ]; then
  ok "notes@13 lying F13 cleared — beyond-F12 honest fallback"
else
  fail "notes@13 lying F13 cleared (got: '$(fkey_opt 13)')"
fi
if [ "$(fkey_opt 1)" = "F1" ]; then
  ok "first@1 keeps the identity label F1"
else
  fail "first@1 keeps the identity label F1 (got: '$(fkey_opt 1)')"
fi
if [ "$(fkey_opt 3)" = "F3" ]; then
  ok "third@3 keeps the identity label F3"
else
  fail "third@3 keeps the identity label F3 (got: '$(fkey_opt 3)')"
fi

# --- B. The status-bar format renders that label --------------------------
echo "-- rendered labels (window-status-format)"
FMT=$(tmux show-options -gv window-status-format)
FMTCUR=$(tmux show-options -gv window-status-current-format)
r12=$(render 12 "$FMT")
if printf '%s' "$r12" | grep -qF 'F11 ' && ! printf '%s' "$r12" | grep -qF 'F12'; then
  ok "spawn window renders F11 and no trace of the raw-index F12"
else
  fail "spawn window renders F11, not F12 (got: $r12)"
fi
# Uppercase F+digit only ever appears as a key label in the rendered
# string — the style escapes are lowercase hex — so this greps renders,
# not colors.
r11=$(render 11 "$FMT")
if printf '%s' "$r11" | grep -qE 'F[0-9]'; then
  fail "unbound parked@11 renders no F-key at all (got: $r11)"
else
  ok "unbound parked@11 renders no F-key at all"
fi
r3=$(render 3 "$FMT")
if printf '%s' "$r3" | grep -qF 'F3 '; then
  ok "third@3 renders F3 — the 1-10 render is untouched"
else
  fail "third@3 renders F3 (got: $r3)"
fi

# --- C. Pressing the labelled key selects the labelled window -------------
echo "-- behavioural keypress (nested client)"
tmux -L "$OUTSOCK" -f /dev/null new-session -d -s driver -x 140 -y 42 \
  "env -u TMUX tmux attach -t $SESSION"
attached=""
for ((i = 0; i < 30; i++)); do
  n=$(tmux list-sessions -F '#{session_name} #{session_attached}' 2>/dev/null |
    awk -v s="$SESSION" '$1 == s {print $2}')
  if [ -n "$n" ] && [ "$n" -ge 1 ] 2>/dev/null; then attached=1; break; fi
  sleep 0.5
done
if [ -n "$attached" ]; then
  ok "driver client attached to the layout session"
else
  fail "driver client attached to the layout session"
fi

tmux select-window -t "$SESSION:1"
sleep 0.5
press F12
sleep 2
if [ "$(active_idx)" = "1" ]; then
  ok "F12 (advertised by no window) is a no-op — active stays 1"
else
  fail "F12 is a no-op (active: $(active_idx), want 1)"
fi
press F11
if wait_active 12 10; then
  ok "F11 — the rendered label — selects the spawn window it labels"
else
  fail "F11 selects spawn-alpha@12 (active: $(active_idx))"
fi
rcur=$(render 12 "$FMTCUR")
if printf '%s' "$rcur" | grep -qF 'F11*'; then
  ok "current-format renders F11* for the active spawn"
else
  fail "current-format renders F11* for the active spawn (got: $rcur)"
fi
press F3
if wait_active 3 10; then
  ok "F3 still selects third@3 — the 1-10 keypress is untouched"
else
  fail "F3 selects third@3 (active: $(active_idx))"
fi

# --- D. Cleanup freshness: the label follows the spawn --------------------
echo "-- spawn cleanup freshness"
tmux kill-window -t "$SESSION:12"
tmux new-window -d -t "$SESSION:14" -n spawn-beta
if wait_opt 14 "F11" 15; then
  ok "replacement spawn-beta@14 picks up F11 within a sweep"
else
  fail "spawn-beta@14 picks up F11 (got: '$(fkey_opt 14)')"
fi
press F11
if wait_active 14 10; then
  ok "F11 now selects the replacement spawn — label and key moved together"
else
  fail "F11 selects spawn-beta@14 (active: $(active_idx))"
fi
if [ "$(fkey_opt 1)" = "F1" ]; then
  ok "first@1 still F1 after every sweep — no churn of the 1-10 range"
else
  fail "first@1 still F1 after every sweep (got: '$(fkey_opt 1)')"
fi
if grep -q "fkey-labels" "$LIFELOG" 2>/dev/null; then
  ok "daemon lifecycle records the fkey-labels sweep"
else
  fail "daemon lifecycle records the fkey-labels sweep"
fi

# --- Summary --------------------------------------------------------------
if [ "$FAIL" -gt 0 ]; then
  echo "  [diag] windows:"
  tmux list-windows -t "$SESSION" -F '    #{window_index}:#{window_name} fkey=#{@muxcode_fkey} active=#{window_active}' 2>/dev/null || true
  echo "  [diag] daemon.log tail:"
  tail -5 "$WORK/daemon.log" 2>/dev/null | sed 's/^/    | /' || true
fi
echo ""
echo "=== $PASS passed, $FAIL failed ==="
# Coverage floor: the achievable maximum — a skipped section cannot green.
[ "$PASS" -ge 19 ] || { echo "FAIL: coverage floor not met ($PASS < 19)"; exit 1; }
[ "$FAIL" -eq 0 ] || exit 1
echo "OK"
