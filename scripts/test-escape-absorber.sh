#!/usr/bin/env bash
# Integration test for the MUX-163 escape-absorber preamble (TmuxDismissOverlay
# / TmuxResubmitEnter). It drives the two typed shapes — a literal payload and a
# re-submit Enter — at a receiver that MODELS a TUI's pending-ESC parser
# (scripts/lib/escape-chord-receiver.py), the receiver a `cat` pane cannot be
# (cat echoes every byte, so a first character fused into a Meta chord still
# reads back whole). Each absorbed shape has its pre-fix negative control driven
# by hand, so the receiver is proven able to SEE the defect it guards against.
#
# Mechanical section (python3 + tmux, no model): the four chord-receiver checks.
# Opt-in live section (MUXCODE_ESC_PROBE_LIVE=1): the Phase 1 matrix against a
# real Claude composer via scripts/probe-escape-matrix.sh.
#
# Hermetic: an isolated tmux server (-L) and a scratch dir; nothing touches a
# live muxcode session. Skipped WITH REASON when python3 is absent, and the
# coverage floor guarantees a skip cannot read as green.
set -euo pipefail

PASS=0
FAIL=0
SKIP=0

ok()   { PASS=$((PASS + 1)); echo "  ok: $1"; }
fail() { FAIL=$((FAIL + 1)); echo "  FAIL: $1"; }
skip() { SKIP=$((SKIP + 1)); echo "  SKIP: $1"; }

command -v tmux >/dev/null 2>&1 || { echo "SKIP: tmux is required"; exit 2; }

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
RECEIVER="$SCRIPT_DIR/lib/escape-chord-receiver.py"

echo "=== MUX-163 escape-absorber integration test ==="

if ! command -v python3 >/dev/null 2>&1; then
  echo "SKIP: python3 is required for the chord receiver — no mechanical checks can run"
  exit 2
fi
[ -f "$RECEIVER" ] || { echo "SKIP: $RECEIVER missing"; exit 2; }

SOCK="escabs-$$"
T="tmux -L $SOCK"
WORK=$(mktemp -d /tmp/escabs-XXXXXX)
PANE=""
cleanup() {
  $T kill-server 2>/dev/null || true
  rm -rf "$WORK"
}
trap cleanup EXIT

# start_receiver respawns a fresh raw-mode receiver writing to $1, so each shape
# gets its own clean log. First call creates the session; later calls replace
# the process in place. The pane target is resolved once — base-index may be 1.
start_receiver() {
  local log=$1
  if [ -z "$PANE" ]; then
    $T new-session -d -s esc -x 80 -y 24 "python3 '$RECEIVER' '$log'"
    PANE=$($T list-panes -t esc -F '#{session_name}:#{window_index}.#{pane_index}' | head -1)
  else
    $T respawn-pane -k -t "$PANE" "python3 '$RECEIVER' '$log'"
  fi
  sleep 0.8 # let python3 enter raw mode before any key is sent
}

# A log line is `key <name>`, `chord M-<name>`, or `seq <text>`. has_line greps
# the whole word so `chord M--` never matches inside `chord M-C-e`.
has_line() { grep -qxF "$1" "$2"; }

# ── 1. Absorbed literal payload: first character survives ─────
# The wake/inject preamble shape: Escape → gap → C-e (absorber) → gap → payload.
LOG="$WORK/payload-fixed.log"
start_receiver "$LOG"
$T send-keys -t "$PANE" Escape
sleep 0.1
$T send-keys -t "$PANE" C-e
sleep 0.1
$T send-keys -t "$PANE" -l -- '- dash inject probe'
sleep 0.5
if has_line "chord M-C-e" "$LOG"; then
  ok "absorber fuses with the pending ESC (chord M-C-e), not the payload"
else
  fail "expected the ESC to fuse with the C-e absorber (chord M-C-e); log: $(tr '\n' '|' <"$LOG")"
fi
if has_line "key -" "$LOG" && ! has_line "chord M--" "$LOG"; then
  ok "payload first character arrives plain (key -), not fused"
else
  fail "payload first character was fused; log: $(tr '\n' '|' <"$LOG")"
fi

# ── 2. Negative control: no absorber → the defect is visible ──
LOG="$WORK/payload-defect.log"
start_receiver "$LOG"
$T send-keys -t "$PANE" Escape
$T send-keys -t "$PANE" -l -- '- dash inject probe'
sleep 0.5
if has_line "chord M--" "$LOG"; then
  ok "negative control: pre-fix Escape→payload fuses the first char (chord M--)"
else
  fail "the receiver must SEE the defect on the pre-fix shape; log: $(tr '\n' '|' <"$LOG")"
fi

# ── 3. Absorbed re-submit Enter: Enter arrives plain ─────────
# TmuxResubmitEnter's shape: Escape → gap → C-e → gap → Enter.
LOG="$WORK/enter-fixed.log"
start_receiver "$LOG"
$T send-keys -t "$PANE" Escape
sleep 0.1
$T send-keys -t "$PANE" C-e
sleep 0.1
$T send-keys -t "$PANE" Enter
sleep 0.5
if has_line "key Enter" "$LOG" && ! has_line "chord M-Enter" "$LOG"; then
  ok "re-submit Enter arrives plain (key Enter), not Meta-Enter"
else
  fail "re-submit Enter was fused; log: $(tr '\n' '|' <"$LOG")"
fi

# ── 4. Negative control: no absorber → Meta-Enter ────────────
LOG="$WORK/enter-defect.log"
start_receiver "$LOG"
$T send-keys -t "$PANE" Escape
sleep 0.05
$T send-keys -t "$PANE" Enter
sleep 0.5
if has_line "chord M-Enter" "$LOG"; then
  ok "negative control: pre-fix Escape→Enter fuses into Meta-Enter"
else
  fail "the receiver must SEE the defect on the pre-fix Enter shape; log: $(tr '\n' '|' <"$LOG")"
fi

# ── 5. Opt-in live matrix against a real Claude composer ─────
if [ "${MUXCODE_ESC_PROBE_LIVE:-0}" = "1" ]; then
  echo "-- live matrix (MUXCODE_ESC_PROBE_LIVE=1)"
  MATRIX="$WORK/matrix.txt"
  # The probe's exit code is load-bearing: 0 = ran, 2 = a prerequisite skip
  # (claude missing, composer never appeared), anything else = a real failure.
  # Collapsing them all to skip would let a broken live matrix read green.
  bash "$SCRIPT_DIR/probe-escape-matrix.sh" --out "$MATRIX" >"$WORK/probe.log" 2>&1
  rc=$?
  if [ "$rc" -eq 2 ]; then
    skip "live matrix: probe prerequisite absent (claude/composer) — $(tail -1 "$WORK/probe.log" 2>/dev/null)"
  elif [ "$rc" -ne 0 ]; then
    fail "live matrix probe FAILED (exit $rc): $(tail -2 "$WORK/probe.log" 2>/dev/null | tr '\n' '|')"
  else
    if grep -q '^a  Escape → text: first-char LOST' "$MATRIX"; then
      ok "live: today's bare Escape→text loses the first character (defect reproduced)"
    else
      fail "live: expected shape a to lose the first character; matrix: $(tr '\n' '|' <"$MATRIX")"
    fi
    if grep -q '^c .*first-char KEPT' "$MATRIX"; then
      ok "live: the absorber preamble (shape c) keeps the first character"
    else
      fail "live: expected shape c to keep the first character; matrix: $(tr '\n' '|' <"$MATRIX")"
    fi
  fi
else
  skip "live matrix not run (set MUXCODE_ESC_PROBE_LIVE=1 to enable)"
fi

echo ""
echo "=== $PASS passed, $FAIL failed, $SKIP skipped ==="
# Coverage floor: the five mechanical chord checks must all run — a run that
# skipped below this is reporting silence, not health (python3 absent exits 2
# above, before any check).
[ "$PASS" -ge 5 ] || { echo "FAIL: coverage floor not met ($PASS < 5)"; exit 1; }
[ "$FAIL" -eq 0 ] || exit 1
if [ "$SKIP" -gt 0 ]; then
  echo "OK (with $SKIP skipped — live matrix did not run on this machine)"
else
  echo "OK"
fi
