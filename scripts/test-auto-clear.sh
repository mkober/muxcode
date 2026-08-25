#!/usr/bin/env bash
# Integration test for auto-clear between tasks (MUX-103).
#
# Exercises the real pipeline end to end: a scratch tmux session with a fake
# idle agent pane (a shell whose prompt is the Claude ❯ glyph), a real
# `muxcode watch` daemon with auto-clear enrolled, and real bus traffic driving
# task completion. Asserts that /clear is injected into the pane exactly once
# per completed task, that the marker and lifecycle event are written, and that
# every guard scenario (pending inbox, edit exclusion, unenrolled role) holds.
#
# ISOLATION: everything runs in a scratch BUS_SESSION under /tmp with
# MUXCODE_LIFECYCLE_LOG_DIR pinned to a temp dir and MUXCODE_CONFIG pointed at
# an empty file, so no live muxcode session is needed, the real config is never
# read, and ~/.config/muxcode/logs is left untouched (asserted at exit).
# MUXCODE_TMP_CLEANUP_THRESHOLD=0 keeps the scratch daemon's disk-pressure
# check (and its CleanupStale) fully disabled.
#
# REQUIRES: the installed muxcode binary must include MUX-103 (run ./build.sh
# first), and tmux must be available.
#
# Usage: bash scripts/test-auto-clear.sh
set -uo pipefail

MUX="${MUXCODE_BIN:-muxcode}"
MUX="$(command -v "$MUX" 2>/dev/null || echo "$MUX")"
if [ ! -x "$MUX" ]; then
  echo "  FAIL  cannot resolve muxcode binary ('$MUX') — set MUXCODE_BIN"
  exit 1
fi
if ! command -v tmux >/dev/null 2>&1; then
  echo "  FAIL  tmux not available"
  exit 1
fi

GREEN=$'\033[0;32m'; RED=$'\033[0;31m'; NC=$'\033[0m'
pass=0; fail=0
ok()  { echo "  ${GREEN}PASS${NC}  $*"; pass=$((pass + 1)); }
bad() { echo "  ${RED}FAIL${NC}  $*"; fail=$((fail + 1)); }

# --- Isolation -------------------------------------------------------------
export BUS_SESSION="auto-clear-test-$$"
BD="/tmp/muxcode-bus-${BUS_SESSION}"
WORK="/tmp/auto-clear-work-$$"
mkdir -p "$WORK"
export MUXCODE_LIFECYCLE_LOG_DIR="$WORK/lifecycle"
: > "$WORK/empty-config"
export MUXCODE_CONFIG="$WORK/empty-config"
REAL_LOG_DIR="${HOME}/.config/muxcode/logs"
REAL_LOGS_BEFORE="$(ls -1 "$REAL_LOG_DIR" 2>/dev/null | sort)"

# Feature under test: review enrolled, edit deliberately listed to prove the
# hard exclusion, zero quiet window so the trigger arms immediately.
export MUXCODE_AUTO_CLEAR_ROLES="review,edit"
export MUXCODE_AUTO_CLEAR_QUIET_SECS=0
export MUXCODE_REVIEW_CLI="claude"
# Keep scratch-daemon side machinery quiet and safe.
export MUXCODE_TMP_CLEANUP_THRESHOLD=0
export MUXCODE_BRANCH_TIME_DISABLE=1
export MUXCODE_DEDUP_WINDOW=0

DPID=""
cleanup() {
  [ -n "$DPID" ] && kill "$DPID" 2>/dev/null
  tmux kill-session -t "$BUS_SESSION" 2>/dev/null
  rm -rf "$BD" "$WORK"
}
trap cleanup EXIT

LIFELOG="$MUXCODE_LIFECYCLE_LOG_DIR/${BUS_SESSION}.log"
MARKER="$BD/auto-clear-review.last"
PANE="$BUS_SESSION:review.1"

plain() { sed $'s/\x1b\\[[0-9;]*[A-Za-z]//g'; }

# clear_events <pattern> → count of auto-clear lifecycle rows matching pattern.
clear_events() {
  if [ -s "$LIFELOG" ]; then
    grep '"event":"auto-clear"' "$LIFELOG" 2>/dev/null | grep -c -- "$1"
  else
    echo 0
  fi
}

# complete_work <to> <from> <action> — a full request/response round-trip: the
# recipient consumes its inbox for the reply-to id (as a real agent would) and
# answers with --type response, which marks the delivery status responded —
# the completion signal the auto-clear trigger observes.
complete_work() {
  local to="$1" from="$2" action="$3" rid="" i out
  # Flush stale rows first so the reply-to grep below can only match the fresh
  # request — an earlier response sitting in the inbox carries its own
  # "To reply" line and would win the head -1 otherwise.
  AGENT_ROLE="$to" "$MUX" inbox >/dev/null 2>&1
  AGENT_ROLE="$from" "$MUX" send "$to" "$action" "work for $to" >/dev/null 2>&1
  for i in $(seq 1 20); do
    out="$(AGENT_ROLE="$to" "$MUX" inbox 2>/dev/null || true)"
    rid="$(printf '%s' "$out" | grep -o -- '--reply-to [A-Za-z0-9-]*' | head -1 | awk '{print $2}')"
    [ -n "$rid" ] && break
    sleep 0.3
  done
  [ -z "$rid" ] && return 1
  AGENT_ROLE="$to" "$MUX" send "$from" "$action" "done" --type response --reply-to "$rid" >/dev/null 2>&1
}

# --- Scratch session: fake idle Claude pane for review ---------------------
tmux new-session -d -s "$BUS_SESSION" -n review -x 200 -y 50
tmux split-window -h -t "$BUS_SESSION:review"
# A shell whose prompt renders the Claude idle glyph makes provider.IsIdle see
# an idle agent. Text and Enter as separate calls per the dropped-Enter pitfall.
tmux send-keys -t "$PANE" -l "PS1='❯ ' ; PROMPT='❯ ' ; clear"
sleep 0.2
tmux send-keys -t "$PANE" "Enter"
sleep 1

"$MUX" init >/dev/null 2>&1

if tmux capture-pane -t "$PANE" -p -S -8 | plain | grep -q '❯'; then
  ok "scratch review pane shows the idle prompt"
else
  bad "scratch review pane never reached the idle prompt — remaining checks would be vacuous"
fi

# --- Negative baselines ----------------------------------------------------
if tmux capture-pane -t "$PANE" -p -S -50 | plain | grep -q '/clear'; then
  bad "pane contains /clear before any trigger (baseline broken)"
else
  ok "baseline: no /clear in pane"
fi
[ -f "$MARKER" ] && bad "baseline: marker exists before any clear" || ok "baseline: no marker"
[ "$(clear_events 'role=review')" -eq 0 ] && ok "baseline: no auto-clear lifecycle rows" \
  || bad "baseline: unexpected auto-clear lifecycle rows"

# --- Guard checks via the manual path (deterministic, no daemon needed) ----
if "$MUX" clear edit >/dev/null 2>"$WORK/clear-edit.err"; then
  bad "muxcode clear edit succeeded — edit must be hard-excluded"
else
  grep -q "hard-excluded" "$WORK/clear-edit.err" \
    && ok "muxcode clear edit refused: hard-excluded" \
    || bad "muxcode clear edit failed for the wrong reason: $(cat "$WORK/clear-edit.err")"
fi

# A pending, unconsumed request must block the clear.
AGENT_ROLE=edit "$MUX" send review review "pending guard probe" >/dev/null 2>&1
if "$MUX" clear review >/dev/null 2>"$WORK/clear-pending.err"; then
  bad "muxcode clear review succeeded with a pending actionable message"
else
  grep -q "pending actionable inbox" "$WORK/clear-pending.err" \
    && ok "pending actionable inbox blocks the clear" \
    || bad "clear blocked for the wrong reason: $(cat "$WORK/clear-pending.err")"
fi

# Resolve the pending request the way an agent would — this round-trip is also
# the completed work that arms the daemon trigger for scenario 1.
rid="$(AGENT_ROLE=review "$MUX" inbox 2>/dev/null | grep -o -- '--reply-to [A-Za-z0-9-]*' | head -1 | awk '{print $2}')"
if [ -n "$rid" ]; then
  AGENT_ROLE=review "$MUX" send edit review "probe done" --type response --reply-to "$rid" >/dev/null 2>&1
  ok "pending request consumed and answered"
else
  bad "could not resolve pending request reply-to id"
fi

# Completed work for the hard-excluded and unenrolled negatives, set up before
# the daemon starts so one observation window covers them all.
complete_work edit review edit-task || bad "round-trip for edit completion failed"
complete_work plan edit plan-task || bad "round-trip for plan completion failed"

# --- Scenario 1: daemon fires exactly one clear ----------------------------
"$MUX" watch "$BUS_SESSION" --poll 2 >"$WORK/daemon.log" 2>&1 &
DPID=$!
sleep 1
if kill -0 "$DPID" 2>/dev/null; then
  ok "scratch daemon running (pid $DPID)"
else
  bad "scratch daemon exited immediately: $(tail -3 "$WORK/daemon.log" 2>/dev/null)"
fi

fired=""
for i in $(seq 1 45); do
  [ -f "$MARKER" ] && fired=1 && break
  sleep 1
done
if [ -n "$fired" ]; then
  ok "auto-clear fired: marker written"
else
  bad "auto-clear never fired within 45s (daemon log: $(tail -3 "$WORK/daemon.log" 2>/dev/null))"
fi
sleep 1
if tmux capture-pane -t "$PANE" -p -S -50 | plain | grep -q '/clear'; then
  ok "/clear injected into the review pane"
else
  bad "/clear never reached the review pane"
fi
[ "$(clear_events 'role=review')" -eq 1 ] && ok "exactly one auto-clear lifecycle row for review" \
  || bad "auto-clear lifecycle rows for review = $(clear_events 'role=review'), want 1"
clear_events 'trigger=task-completed' | grep -q '^[1-9]' \
  && ok "lifecycle row carries the task-completed trigger context" \
  || bad "lifecycle row missing trigger context"

# --- Scenario 4: repeated poll cycles do not re-clear ----------------------
marker_t1="$(cat "$MARKER" 2>/dev/null)"
sleep 35
if kill -0 "$DPID" 2>/dev/null; then
  ok "daemon still alive through the observation window"
else
  bad "daemon died during the observation window — idempotence check vacuous"
fi
[ "$(cat "$MARKER" 2>/dev/null)" = "$marker_t1" ] && ok "marker unchanged across poll cycles" \
  || bad "marker moved without new completed work"
[ "$(clear_events 'role=review')" -eq 1 ] && ok "still exactly one clear for review" \
  || bad "duplicate clears: $(clear_events 'role=review') lifecycle rows"

# --- Scenario 3: edit never cleared, even when enrolled --------------------
[ -f "$BD/auto-clear-edit.last" ] && bad "edit was cleared despite hard exclusion" \
  || ok "edit untouched despite being listed in MUXCODE_AUTO_CLEAR_ROLES"
[ "$(clear_events 'role=edit')" -eq 0 ] && ok "no auto-clear lifecycle rows for edit" \
  || bad "lifecycle shows a clear for edit"

# --- Scenario 5: unenrolled role untouched ---------------------------------
[ -f "$BD/auto-clear-plan.last" ] && bad "unenrolled plan was cleared" \
  || ok "unenrolled plan untouched despite completed work"

# --- Manual path succeeds once eligible, and re-arms the marker ------------
# Stop the daemon first: the manual-probe round-trip below is new completed
# work, and a still-running daemon would race the manual clear for it, adding
# a lifecycle row the counts here don't expect.
kill "$DPID" 2>/dev/null
wait "$DPID" 2>/dev/null
DPID=""

complete_work review edit manual-probe || bad "round-trip for manual-clear setup failed"
if "$MUX" clear review >"$WORK/clear-manual.out" 2>&1; then
  ok "muxcode clear review succeeded when eligible"
else
  bad "manual clear failed: $(cat "$WORK/clear-manual.out")"
fi
marker_t2="$(cat "$MARKER" 2>/dev/null)"
if [ -n "$marker_t2" ] && [ "$marker_t2" != "$marker_t1" ]; then
  ok "manual clear advanced the marker"
else
  bad "manual clear did not advance the marker"
fi
[ "$(clear_events 'role=review')" -eq 2 ] && ok "manual clear logged its own lifecycle row" \
  || bad "lifecycle rows for review = $(clear_events 'role=review'), want 2"

# --- Real install untouched ------------------------------------------------
REAL_LOGS_AFTER="$(ls -1 "$REAL_LOG_DIR" 2>/dev/null | sort)"
[ "$REAL_LOGS_BEFORE" = "$REAL_LOGS_AFTER" ] && ok "real lifecycle log dir untouched" \
  || bad "real lifecycle log dir changed during the test"

echo
echo "  $pass passed, $fail failed"
[ "$fail" -eq 0 ]
