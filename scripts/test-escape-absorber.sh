#!/usr/bin/env bash
# Integration test for the MUX-163 escape-absorber preamble (TmuxDismissOverlay
# / TmuxResubmitEnter). It drives the two typed shapes — a literal payload and a
# re-submit Enter — at a receiver that MODELS a TUI's pending-ESC parser
# (scripts/lib/escape-chord-receiver.py), the receiver a `cat` pane cannot be
# (cat echoes every byte, so a first character fused into a Meta chord still
# reads back whole). Each absorbed shape has its pre-fix negative control driven
# by hand, so the receiver is proven able to SEE the defect it guards against.
#
# Mechanical section (python3 + tmux, no model): eleven chord-receiver checks —
# a multi-character payload, a single-character one (which unabsorbed vanishes
# entirely rather than losing its first byte), a re-submit Enter and the
# clear-composer preamble (whose C-u must arrive plain or the clear silently
# never happens), each with its pre-fix negative control — plus three that pin
# classify_probe's exit-code branches against stub probes, so the live
# section's skip/fail handling is verified on a machine that never runs it.
#
# The receiver models a key PARSER, not a composer: it holds no text and no
# cursor, so it can prove which keys arrived plain versus fused, never the
# resulting buffer. Buffer emptiness belongs to the live matrix section.
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

# ── 3. Single-character payload: the worst case ──────────────
# A one-character payload has no second byte to survive on: unabsorbed, the
# ESC fuses with `x` and the payload vanishes ENTIRELY rather than losing a
# first character (live matrix shape a3). This is the post-fix half AC1 was
# missing — the matrix only ever recorded the pre-fix loss.
LOG="$WORK/single-char-fixed.log"
start_receiver "$LOG"
$T send-keys -t "$PANE" Escape
sleep 0.1
$T send-keys -t "$PANE" C-e
sleep 0.1
$T send-keys -t "$PANE" -l -- 'x'
sleep 0.5
if has_line "key x" "$LOG" && ! has_line "chord M-x" "$LOG"; then
  ok "single-character payload arrives whole (key x), not fused into M-x"
else
  fail "single-character payload was fused or lost; log: $(tr '\n' '|' <"$LOG")"
fi

# Negative control: the same single char with no absorber must vanish into a
# chord, proving the check above can fail.
LOG="$WORK/single-char-defect.log"
start_receiver "$LOG"
$T send-keys -t "$PANE" Escape
$T send-keys -t "$PANE" -l -- 'x'
sleep 0.5
if has_line "chord M-x" "$LOG" && ! has_line "key x" "$LOG"; then
  ok "negative control: pre-fix Escape→'x' swallows the payload whole (chord M-x)"
else
  fail "the receiver must SEE a single-char payload vanish pre-fix; log: $(tr '\n' '|' <"$LOG")"
fi

# ── 4. Absorbed re-submit Enter: Enter arrives plain ─────────
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

# ── 5. Negative control: no absorber → Meta-Enter ────────────
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

# classify_probe runs a probe script and maps its exit code to a verdict:
# 0 = ran, 2 = a prerequisite skip (claude missing, composer never appeared),
# anything else = a real failure. Collapsing them all to skip would let a
# broken live matrix read green.
#
# The `|| rc=$?` is load-bearing, not style: under `set -e` a bare command
# that exits non-zero ends the SCRIPT, so a following `rc=$?` never runs and
# the skip/fail branches below it are unreachable.
#
# Echoes `ran`, `skip` or `fail:<rc>`; the probe's own output lands in $3.
classify_probe() {
  local probe=$1 out=$2 log=$3
  local rc=0
  bash "$probe" --out "$out" >"$log" 2>&1 || rc=$?
  case "$rc" in
    0) echo "ran" ;;
    2) echo "skip" ;;
    *) echo "fail:$rc" ;;
  esac
}

# keep_log copies a probe log out of the scratch dir, which cleanup() removes
# on exit — without this the reason a live probe failed dies with it. Echoes
# the retained path.
keep_log() {
  local kept="${TMPDIR:-/tmp}/escabs-probe-$$.log"
  cp "$1" "$kept" 2>/dev/null && echo "$kept"
}

# ── 6. TmuxClearComposer: both clearing keys must survive ────
# The slash-command preamble is Escape → C-e → C-e → C-u. C-u cannot be its own
# absorber: a pending ESC fuses it into M-C-u, the composer discards that, and
# the clear silently never happens. The first C-e takes the fusion instead —
# which means it moves no cursor — so a SECOND C-e is what actually reaches the
# composer and sends the cursor to end of line, letting C-u kill the whole line
# rather than only the prefix before a mid-line cursor.
#
# SCOPE: the receiver is a key-event model, not a composer — it holds no text
# and no cursor. These checks prove key DELIVERY (which keys arrived plain vs
# fused), not the resulting buffer state. Emptiness with parked text and a
# mid-line cursor needs a real composer; it is not claimed here.
LOG="$WORK/clear-composer-fixed.log"
start_receiver "$LOG"
$T send-keys -t "$PANE" Escape
sleep 0.1
$T send-keys -t "$PANE" C-e
sleep 0.1
$T send-keys -t "$PANE" C-e
$T send-keys -t "$PANE" C-u
sleep 0.1
$T send-keys -t "$PANE" -l -- '/clear'
sleep 0.5
if has_line "key C-u" "$LOG" && ! has_line "chord M-C-u" "$LOG"; then
  ok "clearing C-u is delivered plain, not fused into M-C-u"
else
  fail "the C-u was fused (M-C-u) and would not clear; log: $(tr '\n' '|' <"$LOG")"
fi
if has_line "chord M-C-e" "$LOG" && has_line "key C-e" "$LOG"; then
  ok "one C-e absorbs the ESC and a second is delivered plain to move the cursor"
else
  fail "expected an absorbed M-C-e AND a plain key C-e; log: $(tr '\n' '|' <"$LOG")"
fi
if has_line "key /" "$LOG"; then
  ok "the slash command's first character survives the clear preamble"
else
  fail "slash command first char lost; log: $(tr '\n' '|' <"$LOG")"
fi

# Negative control: C-u used AS the absorber — the shape this helper must not
# have. The clear is swallowed into a chord, which is the silent failure.
LOG="$WORK/clear-composer-defect.log"
start_receiver "$LOG"
$T send-keys -t "$PANE" Escape
$T send-keys -t "$PANE" C-u
sleep 0.1
$T send-keys -t "$PANE" -l -- '/clear'
sleep 0.5
if has_line "chord M-C-u" "$LOG" && ! has_line "key C-u" "$LOG"; then
  ok "negative control: Escape→C-u swallows the clear into M-C-u (composer never empties)"
else
  fail "the receiver must SEE the clear key vanish when C-u is the absorber; log: $(tr '\n' '|' <"$LOG")"
fi

# ── 7. classify_probe itself, against controlled outcomes ────
# The live section below runs at most one probe, and on this machine usually
# none — so its three branches are unverified unless they are driven directly.
# Stub probes with known exit codes do that, and the run reaching the end is
# itself the proof that a non-zero probe no longer kills the script.
STUB="$WORK/stub-probe.sh"
for pair in "0 ran" "2 skip" "1 fail:1"; do
  code=${pair%% *}
  want=${pair##* }
  printf '#!/usr/bin/env bash\nexit %s\n' "$code" >"$STUB"
  got=$(classify_probe "$STUB" "$WORK/stub-out.txt" "$WORK/stub.log")
  if [ "$got" = "$want" ]; then
    ok "probe exit $code classifies as '$want'"
  else
    fail "probe exit $code classified as '$got', want '$want'"
  fi
done

# ── 8. Opt-in live matrix against a real Claude composer ─────
if [ "${MUXCODE_ESC_PROBE_LIVE:-0}" = "1" ]; then
  echo "-- live matrix (MUXCODE_ESC_PROBE_LIVE=1)"
  MATRIX="$WORK/matrix.txt"
  verdict=$(classify_probe "$SCRIPT_DIR/probe-escape-matrix.sh" "$MATRIX" "$WORK/probe.log")
  if [ "$verdict" = "skip" ]; then
    skip "live matrix: probe prerequisite absent (claude/composer) — $(tail -1 "$WORK/probe.log" 2>/dev/null)"
  elif [ "$verdict" != "ran" ]; then
    fail "live matrix probe FAILED (${verdict/fail:/exit }), log kept at $(keep_log "$WORK/probe.log"): $(tail -2 "$WORK/probe.log" 2>/dev/null | tr '\n' '|')"
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
# Coverage floor: the fourteen mechanical checks (eleven chord, three
# exit-code) must all run — a run that skipped below this is reporting
# silence, not health (python3 absent exits 2 above, before any check).
[ "$PASS" -ge 14 ] || { echo "FAIL: coverage floor not met ($PASS < 14)"; exit 1; }
[ "$FAIL" -eq 0 ] || exit 1
if [ "$SKIP" -gt 0 ]; then
  echo "OK (with $SKIP skipped — live matrix did not run on this machine)"
else
  echo "OK"
fi
