#!/usr/bin/env bash
# Integration test for the force-respond escalation ladder (MUX-105).
#
# Hermetic: a scratch bus session, a scratch tmux session whose role pane
# is a shell with the Claude idle glyph as PS1 (the MUX-103 pattern that
# satisfies provider idle detection), and a real scratch daemon running
# the ladder. Two daemon phases: first with the ladder disabled (opt-out
# proof), then enabled (the full ladder walk, the in-flight catch-22, the
# responding-agent negative control, and the skip-is-not-a-redrive event).
#
# Requires installed muxcode >= v0.1.0, which shipped MUX-105 (run ./build.sh first).
set -euo pipefail

PASS=0
FAIL=0

command -v tmux >/dev/null 2>&1 || { echo "SKIP: tmux is required"; exit 2; }
command -v muxcode >/dev/null 2>&1 || { echo "SKIP: muxcode not installed"; exit 2; }
MUX=$(command -v muxcode)
. "$(dirname "${BASH_SOURCE[0]}")/lib/muxcode-version.sh"
require_muxcode_version "$MUX" v0.1.0 MUX-105 || { echo "  FAIL  binary precondition not met"; exit 1; }

SESSION="force-respond-test-$$"
export BUS_SESSION="$SESSION"
export AGENT_ROLE=edit BUS_ROLE=edit
WORK=$(mktemp -d /tmp/force-respond-XXXXXX)
export MUXCODE_LIFECYCLE_LOG_DIR="$WORK/lifecycle"
export MUXCODE_AGENT_CLI=claude MUXCODE_RUN_CLI=claude MUXCODE_TEST_CLI=claude
export MUXCODE_FORCE_RESPOND_SECS=3
export MUXCODE_FORCE_RESPOND_RUNG_SECS=3
export MUXCODE_TMP_CLEANUP_THRESHOLD=0
BUSDIR="/tmp/muxcode-bus-$SESSION"

DPID=""
cleanup() {
  [ -n "$DPID" ] && kill "$DPID" 2>/dev/null || true
  pkill -f "watch $SESSION" 2>/dev/null || true
  tmux kill-session -t "$SESSION" 2>/dev/null || true
  rm -rf "$BUSDIR" "$WORK"
}
trap cleanup EXIT

ok()   { PASS=$((PASS + 1)); echo "  ok: $1"; }
fail() { FAIL=$((FAIL + 1)); echo "  FAIL: $1"; }

events_for() { # role — force-respond lifecycle events, oldest first
  "$MUX" lifecycle show "$SESSION" --source daemon 2>/dev/null |
    grep -E "force-respond|delivery-gap-skip" | grep -F "$1" || true
}

echo "=== force-respond escalation integration test (MUX-105) ==="

# Scratch session: run + test windows, agent pane .1 = shell with the
# idle glyph PS1 so Claude-provider idle detection reads it as a prompt.
tmux new-session -d -s "$SESSION" -n run -x 140 -y 35
tmux split-window -h -t "$SESSION:run"
tmux new-window -t "$SESSION" -n test
tmux split-window -h -t "$SESSION:test"
for w in run test; do
  tmux send-keys -t "$SESSION:$w.1" -l -- 'PS1="❯ "; clear'
  tmux send-keys -t "$SESSION:$w.1" Enter
done
sleep 1
"$MUX" init "$SESSION" >/dev/null 2>&1 || true

# ── Phase A: opt-out — a disabled ladder never fires ─────────

echo "-- opt-out"
MUXCODE_FORCE_RESPOND_DISABLE=1 "$MUX" watch "$SESSION" --poll 2 >"$WORK/daemon-a.log" 2>&1 &
DPID=$!
sleep 1
kill -0 "$DPID" 2>/dev/null && ok "scratch daemon (disabled ladder) running" || fail "scratch daemon started"

"$MUX" send run run "stale request for the opt-out phase" >/dev/null
sleep 12 # several sweeps past the 3s threshold

if [ -z "$(events_for run)" ]; then
  ok "disabled ladder fired no force-respond events"
else
  fail "disabled ladder fired events: $(events_for run | head -2)"
fi
kill "$DPID" 2>/dev/null || true
wait "$DPID" 2>/dev/null || true
DPID=""

# ── Phase B: the ladder walks its rungs ──────────────────────

echo "-- ladder walk"
# A concurrent ./build.sh runs `muxcode upgrade-daemons`, which re-execs
# daemons across ALL sessions — including this scratch one — reviving the
# Phase-A daemon and lock-blocking ours. Clear any daemon for our session
# before starting.
pkill -f "watch $SESSION" 2>/dev/null || true
sleep 1
"$MUX" watch "$SESSION" --poll 2 >"$WORK/daemon-b.log" 2>&1 &
DPID=$!
sleep 1
kill -0 "$DPID" 2>/dev/null && ok "scratch daemon (ladder enabled) running" || fail "scratch daemon started"

# The negative control first: a request to test that gets CONSUMED (an
# ack receipt) — a responding agent, however slow, must stay invisible.
"$MUX" send test test "consumed request - responding agent" >/dev/null
BUS_ROLE=test AGENT_ROLE=test "$MUX" inbox --role test >/dev/null 2>&1 || true

# The stale request for run is already in the inbox from Phase A and is
# well past the threshold; the pane shell never consumes it.
deadline=$((SECONDS + 90))
while [ $SECONDS -lt $deadline ]; do
  if events_for run | grep -q "force-respond-alert"; then
    break
  fi
  sleep 3
done

walk=$(events_for run)
for rung in force-respond-notify force-respond-deliver force-respond-override force-respond-alert; do
  if printf '%s' "$walk" | grep -q "$rung"; then
    ok "ladder fired $rung"
  else
    fail "ladder fired $rung (events: $(printf '%s' "$walk" | tr '\n' ' '))"
  fi
done

# Rungs must appear in ladder order.
order=$(printf '%s\n' "$walk" | grep -oE "force-respond-(notify|deliver|override|alert)" | head -4 | tr '\n' '>' || true)
if [ "$order" = "force-respond-notify>force-respond-deliver>force-respond-override>force-respond-alert>" ]; then
  ok "rungs fired in ladder order"
else
  fail "rungs fired in ladder order (got: $order)"
fi

# The final alert reaches edit with the escalation history.
alert=$("$MUX" inbox --role edit --peek 2>/dev/null | grep -A2 "force-respond" | head -5 || true)
if printf '%s' "$alert" | grep -q "force-respond-notify"; then
  ok "edit alert carries the escalation history"
else
  fail "edit alert carries the escalation history (got: $alert)"
fi

# Negative control: the responding agent never escalated.
if [ -z "$(events_for test)" ]; then
  ok "responding agent never escalates (negative control)"
else
  fail "responding agent never escalates (got: $(events_for test | head -2))"
fi

# ── Phase C: the in-flight catch-22 is broken ────────────────

echo "-- in-flight catch-22"
# Kill the Phase-B daemon first: at --poll 2 it delivers the pending
# message during the aging sleep, so the non-forced deliver below would
# find nothing and never reach the skip path (found by the first live
# run — a harness race, not a product bug).
kill "$DPID" 2>/dev/null || true
wait "$DPID" 2>/dev/null || true
DPID=""
# An opencode-pinned role with an aged in-flight task: the exact
# 2026-08-26 shape. deliver --force must bypass the skip and inject.
# Fresh actions for the Phase-C sends: the in-flight send dedup
# suppresses a second (role,action) tuple BY DESIGN, and Phases A/B left
# run:run and test:test tasks in flight — a suppressed send silenced with
# >/dev/null let every later check false-fail on an empty inbox. Distinct
# actions sidestep the guard instead of fighting it; the preconditions
# pin that the messages actually landed.
export MUXCODE_RUN_CLI=opencode
# --no-notify: the send's own notify wake would verified-inject and
# CONSUME the message from the scratch pane immediately, leaving nothing
# pending by deliver time — the message must still be in the inbox for
# the deliver checks to mean anything.
"$MUX" send run catch22 "catch-22 payload mux105" --no-notify --track >/dev/null
if "$MUX" inbox --role run --peek 2>/dev/null | grep -qF "catch-22 payload mux105"; then
  ok "catch-22 message landed in the run inbox"
else
  fail "catch-22 message landed in the run inbox — send was suppressed"
fi
sleep 6 # age the in-flight task past the 5s guard window

deliver_out=$("$MUX" deliver run --force 2>&1 || true)
if printf '%s' "$deliver_out" | grep -q "woke run"; then
  ok "deliver --force bypasses the in-flight skip"
else
  fail "deliver --force bypasses the in-flight skip (got: $deliver_out)"
fi
if printf '%s' "$deliver_out" | grep -q "skipping run injection"; then
  fail "no skip message under force"
else
  ok "no skip message under force"
fi

# Non-forced delivery against the same in-flight task must surface the
# skip — never a silent success (the sentinel, end to end).
export MUXCODE_TEST_CLI=opencode
"$MUX" send test skipcheck "skip surfacing payload" --no-notify --track >/dev/null
if "$MUX" inbox --role test --peek 2>/dev/null | grep -qF "skip surfacing payload"; then
  ok "skip-surfacing message landed in the test inbox"
else
  fail "skip-surfacing message landed in the test inbox — send was suppressed"
fi
sleep 6
nonforce_out=$("$MUX" deliver test 2>&1 || true)
if printf '%s' "$nonforce_out" | grep -qE "skip|in-flight"; then
  ok "non-forced deliver surfaces the skip"
else
  fail "non-forced deliver surfaces the skip (got: $nonforce_out)"
fi

# ── Summary ──────────────────────────────────────────────────

echo ""
echo "=== $PASS passed, $FAIL failed ==="
[ "$PASS" -ge 13 ] || { echo "FAIL: coverage floor not met ($PASS < 13)"; exit 1; }
[ "$FAIL" -eq 0 ] || exit 1
echo "OK"
