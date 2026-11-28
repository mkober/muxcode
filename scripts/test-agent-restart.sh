#!/usr/bin/env bash
# Integration test for MUX-008 — the daemon's agent restart is verified: the
# launch line is typed only once the old process has left the pane, and the
# restart counts only once a started agent is in it.
#
# Hermetic: a muxcode and scripts/fixtures/claude-stub built from THIS tree
# (the stub as `claude`, so tmux names it `claude` in the pane's foreground),
# a scratch HOME, bus and lifecycle log, a private tmux server
# (scripts/lib/private-tmux.sh) and a scratch daemon with a 3 s health sweep.
# The stub's wedge mode (CLAUDE_STUB_WEDGE_FILE) is the field report's shape:
# a SIGTERM draws Claude's teardown and a bare "$ " prompt — the sweep reads
# the pane dead — while the process stays in the foreground; a Ctrl-C then
# exits after CLAUDE_STUB_EXIT_DELAY_FILE seconds (-1: never), and lines typed
# meanwhile are logged and swallowed.
#
# Sections: (A) launch plan and run; (B) negative control — a normally
# exiting plan is restarted, comes back started, receipts a request sent
# afterwards, with no failure row; (C) a slow-exiting plan (3 s) — the launch
# line is not swallowed and the agent comes back; (D) a run that never exits —
# the restart is logged failed, no launch line is typed, no agent-recovered;
# (E) control — a daemon built from a copy of the tree with both waits
# disabled swallows the launch line in C's slow-exit case. A coverage floor
# keeps a skipped section from reporting green. Requires go, tmux, jq.
set -euo pipefail
export MUXCODE_CONSUMER_GUARD=0 # edit is played by this script, with no window or listener (MUX-145)

PASS=0
FAIL=0
for t in tmux go jq; do
  command -v "$t" >/dev/null 2>&1 || { echo "SKIP: $t is required"; exit 2; }
done

REPO=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
WORK=$(mktemp -d /tmp/agent-restart-XXXXXX)
SESSION="agent-restart-$$"
. "$REPO/scripts/lib/muxcode-bus-dir.sh"
. "$REPO/scripts/lib/private-tmux.sh"
private_tmux_init "$WORK/tmux"
BUSDIR=""

export BUS_SESSION="$SESSION" AGENT_ROLE=edit BUS_ROLE=edit
export HOME="$WORK/home"
export XDG_STATE_HOME="$WORK/state"
export MUXCODE_LIFECYCLE_LOG_DIR="$WORK/lifecycle"
export MUXCODE_INSTALL_DIR="$WORK/install"
export MUXCODE_AGENT_CLI=claude MUXCODE_PLAN_CLI=claude MUXCODE_RUN_CLI=claude
export MUXCODE_AGENT_HEALTH_CHECK_SECS=3 MUXCODE_DEFINITION_CHECK_SECS=3
export MUXCODE_CONTROL_PANE_DISABLE=1 MUXCODE_FORCE_RESPOND_DISABLE=1 MUXCODE_AGENTDEFS_WATCH_DISABLE=1
export MUXCODE_TMP_CLEANUP_THRESHOLD=0 MUXCODE_ACTIVE_WATCHDOG_SECS=0 MUXCODE_PROMPT_AGENT_DISABLE=1
export PATH="$WORK/bin:$PATH"
mkdir -p "$WORK/bin" "$WORK/bin-mut" "$WORK/home" "$WORK/state" "$WORK/install/agents" "$WORK/project"

DPID=""
# cleanup stops the daemon, the private tmux server and any wedged stub (a
# never-exiting one ignores Ctrl-C), then removes the tree; it never fails the
# exit status — the verdict is the tally.
cleanup() {
  set +e
  if [ -n "$DPID" ]; then kill "$DPID" 2>/dev/null; wait "$DPID" 2>/dev/null; fi
  for r in plan run; do [ -n "$(stub_pid "$r" 2>/dev/null)" ] && kill -KILL "$(stub_pid "$r")" 2>/dev/null; done
  private_tmux_kill
  sleep 1
  rm -rf ${BUSDIR:+"$BUSDIR"} "$WORK" 2>/dev/null || { sleep 2; rm -rf ${BUSDIR:+"$BUSDIR"} "$WORK" 2>/dev/null; }
  true
}
trap cleanup EXIT

ok()   { PASS=$((PASS + 1)); echo "  ok: $1"; }
fail() { FAIL=$((FAIL + 1)); echo "  FAIL: $1"; }

events()     { "$MUX" lifecycle show "$SESSION" --limit 1000 2>/dev/null | grep -E "$1" || true; }
n_events()   { events "$1" | grep -c . || true; }
has_event()  { [ -n "$(events "$1")" ]; }
pane_pid()   { tmux display-message -p -t "$SESSION:$1.1" '#{pane_pid}'; }
stub_pid()   { pgrep -P "$(pane_pid "$1" || echo 0)" -x claude 2>/dev/null | head -1 || true; }
stub_up()    { local p; p=$(stub_pid "$1"); [ -n "$p" ] && [ "$p" != "${2:-}" ]; }
pane_has()   { tmux capture-pane -p -t "$SESSION:$1.1" -S -50 | grep -q -- "$2"; }
type_in()    { tmux send-keys -t "$SESSION:$1.1" -l -- "$2"; tmux send-keys -t "$SESSION:$1.1" Enter; }
listener_alive() { local pid; pid=$(cat "$BUSDIR/polling-$1.marker" 2>/dev/null) && [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; }
inbox_empty() { [ -z "$(AGENT_ROLE=$1 "$MUX" inbox --peek 2>/dev/null | grep 'Message from')" ]; }
acked()       { jq -e '(.acked_at // 0) > 0' "$BUSDIR/delivery/$1.status" >/dev/null 2>&1; }
typed_launch() { grep -q "agent launch" "$WORK/typed-$1.log" 2>/dev/null; }
event_count_ge() { [ "$(n_events "$1")" -ge "$2" ]; }
wedged_dead() { tmux capture-pane -p -t "$SESSION:$1.1" | grep -v '^ *$' | tail -1 | grep -qx '\$ *' && kill -0 "$2"; }

# wait_for SECS DESCRIPTION CHECK [ARGS...] — polls once a second; a timeout is
# a recorded FAIL with a state dump, never an abort, so the tally stays honest.
wait_for() {
  local secs=$1 desc=$2 i=0
  shift 2
  while [ "$i" -lt "$secs" ]; do
    if "$@" >/dev/null 2>&1; then ok "$desc"; return 0; fi
    sleep 1; i=$((i + 1))
  done
  fail "$desc (timeout ${secs}s)"
  echo "  --- state dump ---"
  for w in plan run; do echo "  [pane $w]"; tmux capture-pane -p -t "$SESSION:$w.1" -S -8 2>/dev/null | sed 's/^/    /'; done
  events 'agent-(restart|relaunch|recovered|health-fail)' | tail -6 | sed 's/^/    /'
  echo "  --- end dump ---"
  return 0
}

# start_daemon BIN — a scratch daemon from BIN, replacing any running one.
start_daemon() {
  if [ -n "$DPID" ]; then kill "$DPID" 2>/dev/null || true; wait "$DPID" 2>/dev/null || true; fi
  "$1" watch "$SESSION" --poll 2 >>"$WORK/daemon.log" 2>&1 &
  DPID=$!
  sleep 1
}

# wedge ROLE DELAY — the next SIGTERM leaves ROLE's stub in the foreground,
# exiting DELAY seconds after a Ctrl-C (-1: never).
wedge() { echo 1 >"$WORK/wedge-$1"; echo "$2" >"$WORK/delay-$1"; : >"$WORK/typed-$1.log"; }
unwedge() { rm -f "$WORK/wedge-$1" "$WORK/delay-$1"; }

echo "=== MUX-008 verified agent restart integration test ==="

(cd "$REPO/tools/muxcode" && go build -buildvcs=false -o "$WORK/bin/muxcode" .) \
  || { echo "SKIP: go build muxcode failed"; exit 2; }
(cd "$REPO/scripts/fixtures/claude-stub" && go build -buildvcs=false -o "$WORK/bin/claude" .) \
  || { echo "SKIP: go build claude-stub failed"; exit 2; }
MUX="$WORK/bin/muxcode"
BUSDIR="$(muxcode_bus_dir "$MUX" "$SESSION")" || exit 1
ok "scratch muxcode and claude-stub built from this tree"

# The control daemon: this tree with both waits in scrapeAndRelaunch disabled.
cp -R "$REPO/tools/muxcode" "$WORK/mut"
perl -0pi -e 's/\tif !pollUntil\(restartExitWait, heartbeat, func\(\) bool \{/\tif false \&\& !pollUntil(restartExitWait, heartbeat, func() bool {/; s/\tif !pollUntil\(restartReadyWait, heartbeat,/\tif false \&\& !pollUntil(restartReadyWait, heartbeat,/' "$WORK/mut/bus/health.go"
if [ "$(grep -c 'if false && !pollUntil' "$WORK/mut/bus/health.go")" = 2 ] \
  && (cd "$WORK/mut" && go build -buildvcs=false -o "$WORK/bin-mut/muxcode" .); then
  ok "control daemon built from a copy with both waits disabled"
else
  fail "control daemon: the revert did not apply or the copy did not build"
fi

printf -- '---\ndescription: Docs\n---\nMaintain docs.\n' >"$WORK/install/agents/planner.md"
printf -- '---\ndescription: Runner\n---\nRun things.\n' >"$WORK/install/agents/command-runner.md"

# Each agent pane carries its own role and its own stub control files.
pane_env() {
  echo "export AGENT_ROLE=$1 BUS_ROLE=$1 BUS_SESSION=$SESSION HOME=$HOME XDG_STATE_HOME=$XDG_STATE_HOME TMUX_TMPDIR=$TMUX_TMPDIR MUXCODE_LIFECYCLE_LOG_DIR=$MUXCODE_LIFECYCLE_LOG_DIR MUXCODE_INSTALL_DIR=$MUXCODE_INSTALL_DIR MUXCODE_AGENT_CLI=claude MUXCODE_PLAN_CLI=claude MUXCODE_RUN_CLI=claude CLAUDE_STUB_WEDGE_FILE=$WORK/wedge-$1 CLAUDE_STUB_EXIT_DELAY_FILE=$WORK/delay-$1 CLAUDE_STUB_TYPED_LOG=$WORK/typed-$1.log PATH=$PATH; PS1='\$ '; clear"
}
tmux new-session -d -s "$SESSION" -n plan -x 160 -y 40 -c "$WORK/project"
tmux split-window -h -t "$SESSION:plan" -c "$WORK/project"
tmux new-window -t "$SESSION" -n run -c "$WORK/project"
tmux split-window -h -t "$SESSION:run" -c "$WORK/project"
for w in plan run; do type_in "$w" "$(pane_env "$w")"; done
sleep 1
"$MUX" init "$SESSION" >/dev/null 2>&1 || true
start_daemon "$MUX"
kill -0 "$DPID" 2>/dev/null && ok "scratch daemon running" || fail "scratch daemon started"

# ── A: launch ────────────────────────────────────────────────────
echo "-- A: launch plan and run"
type_in plan "muxcode agent launch plan"
type_in run "muxcode agent launch run"
wait_for 15 "plan: claude-stub up" stub_up plan
wait_for 15 "run: claude-stub up" stub_up run
wait_for 15 "plan: inbox listener running" listener_alive plan

# ── B: negative control — a normal exit restarts as before ──────
echo "-- B: kill plan (normal exit) — restarted, started, receipts a request"
P1=$(stub_pid plan)
kill -TERM "$P1"
wait_for 30 "plan: daemon relaunched it (agent-relaunch)" has_event "agent-relaunch.*plan: muxcode agent launch plan"
wait_for 20 "plan: a new claude-stub in the pane" stub_up plan "$P1"
wait_for 20 "plan: agent-recovered" has_event "agent-recovered.*plan"
[ "$(n_events 'agent-relaunch-unverified|agent-restart-failed')" = 0 ] \
  && ok "B: no failure row for a normal restart" || fail "B: failure rows: $(events 'agent-relaunch-unverified|agent-restart-failed' | head -2)"
wait_for 15 "plan: listener re-established" listener_alive plan
AGENT_ROLE=edit "$MUX" send plan ping "MUX008-B ping after restart" >/dev/null 2>&1 || true
B_ID=$(jq -r 'select(.to == "plan" and (.payload | test("MUX008-B"))) | .id' "$BUSDIR/log.jsonl" 2>/dev/null | tail -1)
[ -n "$B_ID" ] && ok "B: a request sent to plan after the restart" || fail "B: the request was not sent"
wait_for 20 "B: the restarted agent consumed it" inbox_empty plan
acked "$B_ID" && ok "B: the request carries the new agent's receipt" || fail "B: no receipt for $B_ID"

# ── C: slow exit — the launch line waits for the process to leave ─
echo "-- C: plan slow to exit (3 s after Ctrl-C)"
wedge plan 3
P2=$(stub_pid plan)
kill -TERM "$P2"
wait_for 10 "C: the wedged pane reads as a bare prompt with the stub still running" wedged_dead plan "$P2"
wait_for 40 "C: plan restarted (a second agent-relaunch)" event_count_ge "agent-relaunch.*plan: muxcode agent launch plan" 2
wait_for 20 "C: a new claude-stub replaced the wedged one" stub_up plan "$P2"
typed_launch plan && fail "C: the launch line reached the dying stub's stdin (swallowed)" \
  || ok "C: the launch line was not typed into the dying process"
wait_for 20 "C: agent-recovered after the slow exit" event_count_ge "agent-recovered.*plan" 2
[ "$(n_events 'agent-relaunch-unverified.*plan|agent-restart-failed.*plan')" = 0 ] \
  && ok "C: no failure row — the 3 s exit fit the 5 s bound" || fail "C: failure rows: $(events 'unverified.*plan|restart-failed.*plan' | head -2)"
unwedge plan

# ── D: never exits — failed, nothing typed, never recovered ──────
echo "-- D: run never exits"
wedge run -1
R1=$(stub_pid run)
kill -TERM "$R1"
wait_for 40 "D: agent-restart-failed names the attempt and the process left running" \
  has_event "agent-restart-failed.*run attempt 1/3: restart unverified: run: old process still in the pane .*foreground claude.*no launch line typed"
has_event "agent-relaunch-unverified.*run" && ok "D: agent-relaunch-unverified written" || fail "D: no agent-relaunch-unverified row"
kill -0 "$R1" 2>/dev/null && ok "D: the never-exiting stub is still the pane's process" || fail "D: the wedged stub exited"
typed_launch run && fail "D: a launch line was typed into the process that never exited" || ok "D: no launch line typed"
has_event "agent-relaunch.*run: muxcode agent launch" && fail "D: an agent-relaunch row for run" || ok "D: no agent-relaunch row for run"
sleep 8 # three more sweeps
has_event "agent-recovered.*run" && fail "D: agent-recovered announced for a failed restart" || ok "D: agent-recovered withheld"
kill -KILL "$R1" 2>/dev/null || true
unwedge run

# ── E: control — both waits disabled swallow the launch line ─────
echo "-- E: control — a daemon without the waits, plan slow to exit"
start_daemon "$WORK/bin-mut/muxcode"
kill -0 "$DPID" 2>/dev/null && ok "E: control daemon running" || fail "E: control daemon started"
wait_for 20 "E: plan up before the wedge" stub_up plan
wedge plan 3
P3=$(stub_pid plan)
BEFORE=$(n_events "agent-relaunch.*plan: muxcode agent launch plan")
kill -TERM "$P3"
wait_for 40 "E: the control daemon relaunched plan" event_count_ge "agent-relaunch.*plan: muxcode agent launch plan" $((BEFORE + 1))
wait_for 10 "E: the launch line reached the dying stub's stdin — swallowed, the defect" typed_launch plan
unwedge plan

echo "=== $PASS passed, $FAIL failed ==="
[ "$PASS" -ge 30 ] || { echo "FAIL: coverage floor not met ($PASS < 30)"; exit 1; }
[ "$FAIL" -eq 0 ] || exit 1
echo "PASS"
