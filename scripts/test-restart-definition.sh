#!/usr/bin/env bash
# Integration test for MUX-136 — a restart restores the agent definition, a
# bare resume is refused, an unresolvable definition never comes up.
#
# Hermetic: scratch bus + private tmux server + scratch daemon, a muxcode
# built from THIS tree (the installed binary is never run), and a
# Claude-SHAPED agent — scripts/fixtures/claude-stub built into the scratch
# bin as `claude`. The real launcher execs it with the real flag set, so the
# real ProbeAgentDefinition runs against a real process tree (argv[0] is
# "claude"; a shell script cannot be, its process is the interpreter). The
# stub prints the idle glyph, starts `muxcode inbox --poll --loop` when its
# prompt tells it to, prints Claude's resume banner on a bare --resume, and
# exits on /exit and Ctrl-C.
#
# Sections: (A) launch with definition — probe present, listener running,
# `tools:` carried in the --agents JSON, no downgrade alert; (B) kill —
# strike-2 forensic snapshot, relaunch, agent-recovered only after the probe
# is present, detector silent (negative control); (C) bare resume —
# agent-definitionless raised, edit alerted, definition-reload, restored;
# (D) unresolvable definition — launch-refused, no agent, no recovery.
# A coverage floor keeps a skipped section from reporting green.
# Requires go, tmux, jq.
set -euo pipefail

PASS=0
FAIL=0
for t in tmux go jq; do
  command -v "$t" >/dev/null 2>&1 || { echo "SKIP: $t is required"; exit 2; }
done

REPO=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
WORK=$(mktemp -d /tmp/restart-def-XXXXXX)
SESSION="restart-def-$$"
BUSDIR="/tmp/muxcode-bus-$SESSION"

export BUS_SESSION="$SESSION" AGENT_ROLE=edit BUS_ROLE=edit
export HOME="$WORK/home" # empty user tier — definitions come only from the scratch install dir
export TMUX_TMPDIR="$WORK/tmux"
unset TMUX
export MUXCODE_LIFECYCLE_LOG_DIR="$WORK/lifecycle"
export MUXCODE_INSTALL_DIR="$WORK/install"
export MUXCODE_AGENT_CLI=claude MUXCODE_PLAN_CLI=claude MUXCODE_RUN_CLI=claude
export MUXCODE_AGENT_HEALTH_CHECK_SECS=3 MUXCODE_DEFINITION_CHECK_SECS=3
export MUXCODE_CONTROL_PANE_DISABLE=1 MUXCODE_FORCE_RESPOND_DISABLE=1 MUXCODE_AGENTDEFS_WATCH_DISABLE=1
export MUXCODE_TMP_CLEANUP_THRESHOLD=0 MUXCODE_ACTIVE_WATCHDOG_SECS=0 MUXCODE_PROMPT_AGENT_DISABLE=1
export PATH="$WORK/bin:$PATH"
mkdir -p "$WORK/bin" "$WORK/home" "$WORK/tmux" "$WORK/install/agents" "$WORK/project"

DPID=""
# cleanup reaps the daemon before removing the tree — a SIGTERM'd daemon still
# finishing a tick writes into WORK and the rm races it — and never fails the
# exit status: the verdict is the tally, not the teardown. Only the private
# tmux server is killed; the stubs' orphaned listeners exit on their own once
# `has-session` fails, and a pattern kill would reach the machine's real
# listeners.
cleanup() {
  set +e
  if [ -n "$DPID" ]; then kill "$DPID" 2>/dev/null; wait "$DPID" 2>/dev/null; fi
  pkill -f "watch $SESSION" 2>/dev/null
  tmux kill-server 2>/dev/null
  sleep 1
  rm -rf "$BUSDIR" "$WORK" 2>/dev/null || { sleep 2; rm -rf "$BUSDIR" "$WORK" 2>/dev/null; }
  true
}
trap cleanup EXIT

ok()   { PASS=$((PASS + 1)); echo "  ok: $1"; }
fail() { FAIL=$((FAIL + 1)); echo "  FAIL: $1"; }

events() { "$MUX" lifecycle show "$SESSION" 2>/dev/null | grep -E "$1" || true; }
pane_pid() { tmux display-message -p -t "$SESSION:$1.1" '#{pane_pid}'; }
stub_pid() { pgrep -P "$(pane_pid "$1" || echo 0)" -x claude 2>/dev/null | head -1 || true; }
probe() { "$MUX" agent definition "$1" 2>/dev/null || true; }
type_in() { tmux send-keys -t "$SESSION:$1.1" -l -- "$2"; tmux send-keys -t "$SESSION:$1.1" Enter; }

# wait_for SECS DESCRIPTION CHECK-FUNCTION [ARGS...] — polls once a second.
# Always returns 0: a timeout is a recorded FAIL plus a state dump, never an
# abort under set -e, so later sections still run and the tally is honest.
wait_for() {
  local secs=$1 desc=$2 i=0
  shift 2
  while [ "$i" -lt "$secs" ]; do
    if "$@" >/dev/null 2>&1; then ok "$desc"; return 0; fi
    sleep 1; i=$((i + 1))
  done
  fail "$desc (timeout ${secs}s)"
  dump_state
  return 0
}

# dump_state prints what a failed wait needs: both agent panes, the bus dir,
# the listeners, and the daemon log tail.
dump_state() {
  echo "  --- state dump ---"
  for w in plan run; do
    echo "  [pane $w]"; tmux capture-pane -p -t "$SESSION:$w.1" -S -12 2>/dev/null | sed 's/^/    /'
  done
  echo "  [bus dir]"; ls "$BUSDIR" 2>/dev/null | tr '\n' ' ' | sed 's/^/    /'; echo
  echo "  [listeners]"; ps -axo pid=,command= | grep -E "muxcode inbox --poll" | grep -v -- "--agents" | cut -c1-100 | sed 's/^/    /' || true
  echo "  [daemon log tail]"; tail -5 "$WORK/daemon.log" 2>/dev/null | sed 's/^/    /'
  echo "  --- end dump ---"
}
probe_is()       { [ "$(probe "$1")" = "$2" ]; }
has_event()      { [ -n "$(events "$1")" ]; }
event_count_ge() { [ "$(events "$1" | wc -l | tr -d ' ')" -ge "$2" ]; }
pane_has()       { tmux capture-pane -p -t "$SESSION:$1.1" -S -50 | grep -q -- "$2"; }
listener_alive() { local pid; pid=$(cat "$BUSDIR/polling-$1.marker" 2>/dev/null) && [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; }
edit_alerted()   { "$MUX" inbox --role edit --peek 2>/dev/null | grep -q "agent-definitionless"; }

echo "=== MUX-136 restart-restores-definition integration test ==="

(cd "$REPO/tools/muxcode" && go build -buildvcs=false -o "$WORK/bin/muxcode" .) \
  || { echo "SKIP: go build muxcode failed"; exit 2; }
(cd "$REPO/scripts/fixtures/claude-stub" && go build -buildvcs=false -o "$WORK/bin/claude" .) \
  || { echo "SKIP: go build claude-stub failed"; exit 2; }
MUX="$WORK/bin/muxcode"
ok "scratch muxcode and claude-stub built from this tree"

printf -- '---\ndescription: Docs\ntools: Read, Edit\n---\nMaintain docs.\n' >"$WORK/install/agents/planner.md"
printf -- '---\ndescription: Runner\n---\nRun things.\n' >"$WORK/install/agents/command-runner.md"

# Private tmux server; plan + run windows, agent pane .1 is a plain shell whose
# prompt ends in "$" so a dead agent reads as a shell prompt. Each pane gets
# its OWN role identity: the server inherits this script's AGENT_ROLE=edit,
# and RunAgentLaunch respects a pre-set AGENT_ROLE (the spawn contract), so
# without this the stub's listener would run as edit.
pane_env() {
  echo "export AGENT_ROLE=$1 BUS_ROLE=$1 BUS_SESSION=$SESSION HOME=$HOME TMUX_TMPDIR=$TMUX_TMPDIR MUXCODE_LIFECYCLE_LOG_DIR=$MUXCODE_LIFECYCLE_LOG_DIR MUXCODE_INSTALL_DIR=$MUXCODE_INSTALL_DIR MUXCODE_AGENT_CLI=claude MUXCODE_PLAN_CLI=claude MUXCODE_RUN_CLI=claude PATH=$PATH; PS1='\$ '; clear"
}
tmux new-session -d -s "$SESSION" -n plan -x 160 -y 40 -c "$WORK/project"
tmux split-window -h -t "$SESSION:plan" -c "$WORK/project"
tmux new-window -t "$SESSION" -n run -c "$WORK/project"
tmux split-window -h -t "$SESSION:run" -c "$WORK/project"
for w in plan run; do type_in "$w" "$(pane_env "$w")"; done
sleep 1
"$MUX" init "$SESSION" >/dev/null 2>&1 || true
"$MUX" watch "$SESSION" --poll 2 >"$WORK/daemon.log" 2>&1 &
DPID=$!
sleep 1
kill -0 "$DPID" 2>/dev/null && ok "scratch daemon running" || fail "scratch daemon started"

# ── A: launch with the definition ────────────────────────────────
echo "-- A: launch with definition"
type_in plan "muxcode agent launch plan"
type_in run "muxcode agent launch run"
wait_for 15 "plan: claude-stub up with the launcher's flag pair" pane_has plan "agent=true agents=true resume=false"
wait_for 10 "plan: live probe reads the process as present" probe_is plan present
wait_for 10 "run: live probe reads the process as present" probe_is run present
if ps -o command= -p "$(stub_pid plan)" | grep -q '"tools":\["Read","Edit"\]'; then
  ok "plan: --agents JSON carries tools: [Read, Edit] from the definition"
else
  fail "plan: tools restriction missing from --agents JSON"
fi
wait_for 15 "plan: inbox listener running (polling marker names a live pid)" listener_alive plan
sleep 7 # two definition sweeps
if [ -z "$(events 'agent-definitionless|definition-reload')" ]; then
  ok "healthy launch: no downgrade alert (negative control)"
else
  fail "healthy launch raised a downgrade alert: $(events 'agent-definitionless|definition-reload' | head -1)"
fi

# ── B: kill the agent, the daemon restores it with its definition ─
echo "-- B: kill and restart"
P1=$(stub_pid plan)
L1=$(cat "$BUSDIR/polling-plan.marker" 2>/dev/null || echo none)
# SIGTERM, not SIGKILL: the stub tears its screen down like Claude's TUI, so
# the shell prompt is what the health sweep sees. A SIGKILLed agent leaves
# its prompt glyph in the pane and the liveness heuristic reads it as alive.
kill -TERM "$P1"
wait_for 20 "plan: strike-2 forensic snapshot taken before the relaunch" has_event "agent-down-snapshot.*plan"
snapdir=$(ls -d "$MUXCODE_LIFECYCLE_LOG_DIR"/snapshots/"$SESSION"-plan-* 2>/dev/null | head -1 || true)
if [ -n "$snapdir" ] && grep -q "claude-stub" "$snapdir/pane.txt" 2>/dev/null && [ -s "$snapdir/lifecycle.log" ] && [ -s "$snapdir/procs.txt" ]; then
  ok "snapshot bundle holds the dead pane's scrollback, the lifecycle log, and the process table"
else
  fail "snapshot bundle incomplete: $snapdir"
fi
wait_for 30 "plan: daemon relaunched through the launcher (second launch event)" event_count_ge "launch.*role=plan" 2
wait_for 20 "plan: agent-recovered after the relaunch" has_event "agent-recovered.*plan"
P2=$(stub_pid plan)
if [ -n "$P2" ] && [ "$P2" != "$P1" ] && probe_is plan present; then
  ok "plan: a new claude-stub carries the flag pair (probe present)"
else
  fail "plan: relaunched process missing or without its definition (pid=$P2 probe=$(probe plan))"
fi
if "$MUX" lifecycle show "$SESSION" | grep -n -E "launch.*role=plan|agent-recovered.*plan" | tail -2 | awk -F: 'NR==1{l=$1} NR==2{r=$1} END{exit !(r>l)}'; then
  ok "plan: recovery was announced only after the relaunch"
else
  fail "plan: agent-recovered preceded the relaunch"
fi
wait_for 15 "plan: listener re-established under the new agent" listener_alive plan
L2=$(cat "$BUSDIR/polling-plan.marker" 2>/dev/null || echo none)
[ "$L2" != "$L1" ] && ok "plan: the new listener took over the marker (newest wins)" || fail "plan: polling marker unchanged ($L1)"
if [ -z "$(events 'agent-definitionless|definition-reload')" ]; then
  ok "healthy restart: definition detector silent (negative control)"
else
  fail "healthy restart tripped the detector: $(events 'agent-definitionless|definition-reload' | head -1)"
fi

# ── C: a bare resume is refused ──────────────────────────────────
echo "-- C: bare resume"
type_in plan "/exit"
sleep 1
type_in plan "claude --resume 0f3a"
wait_for 10 "plan: bare resume prints Claude's banner" pane_has plan "tool restrictions no longer apply"
wait_for 10 "plan: live probe reads the bare resume as missing" probe_is plan missing
wait_for 25 "plan: agent-definitionless raised" has_event "agent-definitionless.*plan"
wait_for 10 "edit alerted with an agent-definitionless event" edit_alerted
wait_for 30 "plan: definition-reload fired (refuse, not quarantine)" has_event "definition-reload.*plan"
wait_for 45 "plan: back with its definition (probe present)" probe_is plan present
wait_for 20 "plan: definition-restored logged" has_event "definition-restored.*plan"
wait_for 15 "plan: listener running again after the refuse-reload" listener_alive plan

# ── D: an unresolvable definition never comes up ────────────────
echo "-- D: unresolvable definition"
rm -f "$WORK/install/agents/command-runner.md"
kill -TERM "$(stub_pid run)"
wait_for 30 "run: launcher refused the relaunch (launch-refused)" has_event "launch-refused.*run"
wait_for 10 "run: refusal printed in the pane" pane_has run "refusing to launch"
sleep 4
[ "$(probe run)" = unknown ] && ok "run: no agent process came up (probe unknown)" || fail "run: probe=$(probe run), want unknown"
[ -z "$(events 'agent-recovered.*run')" ] && ok "run: agent-recovered withheld" || fail "run: agent-recovered announced for a refused launch"
if "$MUX" inbox --role edit --peek 2>/dev/null | grep -q 'run: definition "command-runner"'; then
  ok "run: edit alerted with the refusal"
else
  fail "run: edit not alerted with the refusal"
fi

echo "=== $PASS passed, $FAIL failed ==="
[ "$PASS" -ge 30 ] || { echo "FAIL: coverage floor not met ($PASS < 30)"; exit 1; }
[ "$FAIL" -eq 0 ] || exit 1
echo "PASS"
