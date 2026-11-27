#!/usr/bin/env bash
# Integration test for MUX-190 — a Codex agent drops a consumed request after a
# guard denial.
#
# On 2026-09-23 `hook guard` refused a codex build agent's bundled `./build.sh`;
# the agent ended its turn with its request already consumed and receipted,
# nothing re-drove it, and the graph node waited for its clock.
#
# The assertions are the spec's acceptance — one re-drive carrying the denial
# text, a `guard-denied-redrive` row, an alert to edit when a re-drive cannot
# help, a node output naming the denial. Before the Phase 2 fix
# (bus/guard_denial.go) section A's three failed: that was the Phase 1
# reproduction, and section P keeps it as the control. What happened is also
# printed as BOUNDARY lines, not asserted.
#
# Hermetic: private tmux server, scratch daemons, muxcode and
# scripts/fixtures/claude-stub built from THIS tree, agent definitions in a
# scratch install dir. The stub stands in for the Codex agent's behaviour, not
# its provider: its real `muxcode inbox --poll --loop` listener consumes the
# dispatch with an ack receipt and it never answers — the turn-ended state —
# unless CLAUDE_STUB_REPLY_FILE holds 1, when it runs a re-drive's reply
# command; while CLAUDE_STUB_BUSY_FILE holds 1 its pane shows Claude's working
# spinner. The denial is a real `muxcode hook guard` PreToolUse call as build,
# whose bundled `./build.sh; muxcode send …` the evidence guard refuses,
# writing the real `guard-denied` row. Codex's own turn-ending on a denied
# call is observed (2026-09-23), not pinned: no stub can show provider
# behaviour. A Codex pane itself gets the alert, not the re-drive — the redrive
# road needs a provider that polls its own inbox — which the unit tests cover.
#
# Sections (stall watchdog off unless stated):
#   A  a denial on an in-flight graph dispatch and an idle pane → one re-drive
#      carrying the denial within one poll and a `guard-denied-redrive` row; a
#      second denial on the task → one alert to edit naming task and node, no
#      second re-drive; the node, unanswered, ends at its 30 s cap naming the
#      denial
#   N  negative control — a denial with no in-flight request re-drives nothing
#      and writes no re-drive or alert row
#   B  stall watchdog on (MUX-232): the denied request is re-driven exactly
#      once across the watchdog's re-drive window, carrying the denial text —
#      the guard road owns it, so the stall road adds none
#   C  the stub answers the re-drive: the node succeeds, its output naming the
#      denial
#   D  a busy pane: nothing typed and a `guard-denied-redrive-deferred` row;
#      once it idles unanswered, one alert to edit naming task and node, and
#      still nothing typed
#   P  control — a build from a copy of the tree without the watch (the hook's
#      filing and the daemon's check removed): nothing typed, and the node
#      expires at its cap with no denial in its output
# A coverage floor keeps a skipped section from reporting green.
# Requires go, tmux, jq. ~6 min.
set -uo pipefail
export MUXCODE_CONSUMER_GUARD=0

PASS=0
FAIL=0
EXPECTED_PASS=57
for t in tmux go jq perl; do
  command -v "$t" >/dev/null 2>&1 || { echo "SKIP: $t is required"; exit 2; }
done

REPO=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
WORK=$(mktemp -d /tmp/guard-redrive-XXXXXX)
. "$REPO/scripts/lib/muxcode-bus-dir.sh"
. "$REPO/scripts/lib/private-tmux.sh"
private_tmux_init "$WORK/tmux"

unset AGENT_ROLE BUS_ROLE BUS_SESSION MUXCODE MUXCODE_CONFIG MUXCODE_BUS_BASE MUXCODE_BUS_DIR
export HOME="$WORK/home"
export XDG_STATE_HOME="$WORK/home/.local/state"
export MUXCODE_LIFECYCLE_LOG_DIR="$WORK/lifecycle"
export MUXCODE_INSTALL_DIR="$WORK/install"
export BUS_MEMORY_DIR="$WORK/memory" BUS_GLOBAL_MEMORY_DIR="$WORK/global-memory"
export MUXCODE_AGENT_CLI=claude MUXCODE_BUILD_CLI=claude
export MUXCODE_AGENT_HEALTH_CHECK_SECS=3 MUXCODE_DEFINITION_CHECK_SECS=3 MUXCODE_AGENT_HEARTBEAT=86400
export MUXCODE_EDIT_AUTO_RESTART_DISABLE=1 MUXCODE_CONTROL_PANE_DISABLE=1 MUXCODE_FORCE_RESPOND_DISABLE=1
export MUXCODE_TMP_CLEANUP_THRESHOLD=0 MUXCODE_ACTIVE_WATCHDOG_SECS=0 MUXCODE_PROMPT_AGENT_DISABLE=1
export MUXCODE_BRANCH_TIME_DISABLE=1 MUXCODE_STUCK_RELOAD_DISABLE=1 MUXCODE_DEDUP_WINDOW=0
export MUXCODE_AGENTDEFS_WATCH_DISABLE=1 MUXCODE_CROSS_TREE_GUARD=0
export MUXCODE_TASK_STALL_SECS=6 # a graph dispatch stalls at half: 3 s
export MUXCODE_TASK_STALL_DISABLE=1
mkdir -p "$WORK/bin" "$WORK/bin-pre" "$WORK/home" "$WORK/lifecycle" "$WORK/install/agents" "$WORK/project"

DPIDS=""
cleanup() {
  local p
  for p in $DPIDS; do kill "$p" 2>/dev/null; done
  private_tmux_kill
  sleep 1
  rm -rf "$WORK" 2>/dev/null
  true
}
trap cleanup EXIT

ok()   { PASS=$((PASS + 1)); echo "  ok: $1"; }
fail() { FAIL=$((FAIL + 1)); echo "  FAIL: $1"; }
boundary() { echo "  BOUNDARY: $1"; }
wait_for() {
  local secs=$1 desc=$2 i=0
  shift 2
  while [ "$i" -lt "$secs" ]; do
    if "$@" >/dev/null 2>&1; then ok "$desc"; return 0; fi
    sleep 1; i=$((i + 1))
  done
  fail "$desc (timeout ${secs}s)"
  return 1
}
check() { local desc=$1; shift; if "$@" >/dev/null 2>&1; then ok "$desc"; else fail "$desc"; fi; }

echo "=== MUX-190 a guard denial on an in-flight request ==="

(cd "$REPO/tools/muxcode" && go build -buildvcs=false -o "$WORK/bin/muxcode" .) \
  || { echo "SKIP: go build muxcode failed"; exit 2; }
(cd "$REPO/scripts/fixtures/claude-stub" && go build -buildvcs=false -o "$WORK/bin/claude" .) \
  || { echo "SKIP: go build claude-stub failed"; exit 2; }
cp "$WORK/bin/claude" "$WORK/bin-pre/claude"
ok "scratch muxcode and claude-stub built from this tree"

mkdir -p "$WORK/pre"
cp -R "$REPO/tools/muxcode" "$WORK/pre/"
perl -0pi -e 's/\n\t\t_ = bus\.RecordGuardDenial\(session, role, ev\.ToolName, d\.Reason\)//' "$WORK/pre/muxcode/cmd/hook.go"
perl -0pi -e 's/\n\t\td\.checkGuardDenials\(\)//' "$WORK/pre/muxcode/daemon/daemon.go"
if ! grep -q 'RecordGuardDenial(' "$WORK/pre/muxcode/cmd/hook.go" \
  && ! grep -q 'd\.checkGuardDenials()$' "$WORK/pre/muxcode/daemon/daemon.go" \
  && (cd "$WORK/pre/muxcode" && go build -buildvcs=false -o "$WORK/bin-pre/muxcode" .); then
  ok "pre-fix muxcode built: the hook files no denial and the daemon checks none"
else
  fail "pre-fix build: a patch did not apply, or the copy failed to build"
fi

printf -- '---\ndescription: Builder\n---\nBuild the project.\n' >"$WORK/install/agents/code-builder.md"

# write_graph SECS — a one-node run sending build a build request capped at SECS.
write_graph() {
  printf '{"name": "mux190-guard", "start": "build-node", "nodes": [{"id": "build-node", "type": "send", "role": "build", "action": "build", "message": "Run ./build.sh and report the result", "timeout_secs": %s}], "edges": []}\n' \
    "$1" >"$WORK/graph.json"
}

# start_section NAME BINDIR — a private tmux session whose build pane runs the
# claude-stub as a launched agent, and a scratch daemon, both on BINDIR's
# muxcode and the environment as it stands. Sets SESSION, MUX, BUSDIR,
# LIFELOG, TYPED, REPLY, BUSY.
start_section() {
  SESSION="guard-$1-$$"
  MUX="$2/muxcode"
  BUSDIR="$(muxcode_bus_dir "$MUX" "$SESSION")" || exit 1
  LIFELOG="$MUXCODE_LIFECYCLE_LOG_DIR/$SESSION.log"
  TYPED="$WORK/typed-$1.log" REPLY="$WORK/reply-$1" BUSY="$WORK/busy-$1"
  : >"$TYPED"; echo 0 >"$REPLY"; echo 0 >"$BUSY"
  export BUS_SESSION="$SESSION"
  tmux new-session -d -s "$SESSION" -n build -x 200 -y 50 -c "$WORK/project" "/bin/bash --noprofile --norc"
  tmux set-option -g default-command "/bin/bash --noprofile --norc"
  tmux split-window -h -t "$SESSION:build" -c "$WORK/project"
  type_in "export AGENT_ROLE=build BUS_ROLE=build BUS_SESSION=$SESSION HOME=$HOME XDG_STATE_HOME=$XDG_STATE_HOME TMUX_TMPDIR=$TMUX_TMPDIR MUXCODE_LIFECYCLE_LOG_DIR=$MUXCODE_LIFECYCLE_LOG_DIR MUXCODE_INSTALL_DIR=$MUXCODE_INSTALL_DIR MUXCODE_AGENT_CLI=claude MUXCODE_BUILD_CLI=claude MUXCODE_CONSUMER_GUARD=0 CLAUDE_STUB_TYPED_LOG=$TYPED CLAUDE_STUB_REPLY_FILE=$REPLY CLAUDE_STUB_BUSY_FILE=$BUSY PATH=$2:$PATH; PS1='\$ '; clear"
  sleep 1
  "$MUX" init >/dev/null 2>&1 || true
  (cd "$WORK/project" && exec "$MUX" watch "$SESSION" --poll 1 >>"$WORK/daemon-$1.log" 2>&1) &
  DPIDS="$DPIDS $!"
  sleep 1
  type_in "muxcode agent launch build --reason user"
}
type_in()      { tmux send-keys -t "$SESSION:build.1" -l -- "$1"; sleep 0.1; tmux send-keys -t "$SESSION:build.1" Enter; }
stub_up()      { [ -n "$(cat "$BUSDIR/polling-build.marker" 2>/dev/null)" ]; }
start_run()    { (cd "$WORK/project" && "$MUX" graph run --file "$WORK/graph.json" 2>&1) | grep -o 'Started run [^ ]*' | awk '{print $3}'; }
dispatch_id()  { jq -r --arg r "$RID" 'select(.type == "request" and .to == "build" and .graph_run == $r) | .id' "$BUSDIR/log.jsonl" 2>/dev/null | tail -1; }
dispatched()   { [ -n "$(dispatch_id)" ]; }
receipted()    { jq -e '(.acked_at // 0) > 0' "$BUSDIR/delivery/$ID.status" >/dev/null 2>&1; }
node_field()   { jq -r ".$1 // \"\"" "$BUSDIR/graphs/$RID/nodes/build-node.json" 2>/dev/null; }
node_done()    { [ "$(node_field state)" = done ] || [ "$(node_field state)" = failed ]; }
node_success() { [ "$(node_field state)" = done ] && [ "$(node_field outcome)" = success ]; }
task_status()  { jq -r '.status' "$BUSDIR/tasks/$ID.json" 2>/dev/null; }
task_timed_out() { [ "$(task_status)" = timed-out ]; }
# no_task_in_flight — the session holds no in-flight task, so N's denial can interrupt nothing.
no_task_in_flight() { ! grep -lq '"status":"in-flight"' "$BUSDIR"/tasks/*.json 2>/dev/null; }
a_settled()    { task_timed_out && no_task_in_flight; }
# bare_timeout — the node failed at its cap, its output the bare "node timeout".
bare_timeout() { [ "$(node_field outcome)" = failure ] && [ "$(node_field output)" = "node timeout" ]; }
denial_typed() { grep -q 'hook-road evidence' "$TYPED" 2>/dev/null; }
redrives()     { grep -c 'Re-drive (consumed but never completed)' "$TYPED" 2>/dev/null; }
typed_lines()  { wc -l <"$TYPED" | tr -d ' '; }
event_rows()   { jq -r --arg e "$1" 'select(.event == $e) | .detail // ""' "$LIFELOG" 2>/dev/null; }
event_count()  { event_rows "$1" | grep -c .; }
row_naming()   { event_rows "$1" | grep -qF "$2"; }
output_names_denial() { case $(node_field output) in *guard-denied*"hook-road evidence"*) return 0 ;; *) return 1 ;; esac; }
# edit_alerted — edit received a guard-denied alert naming the task, the run and the node.
edit_alerted() {
  jq -r 'select(.to == "edit" and .action == "guard-denied") | .payload' "$BUSDIR/log.jsonl" 2>/dev/null \
    | grep -F "$ID" | grep -F "$RID" | grep -qF "build-node"
}

# setup_run NAME BINDIR SECS — a section's agent, run and consumed dispatch.
setup_run() {
  write_graph "$3"
  start_section "$1" "$2"
  local L
  L=$(printf %s "$1" | tr '[:lower:]' '[:upper:]')
  wait_for 30 "$L: build agent (claude-stub) up with its inbox listener" stub_up
  RID=$(start_run)
  START=$(date +%s)
  wait_for 20 "$L: the run dispatched its build request" dispatched
  ID=$(dispatch_id)
  wait_for 20 "$L: the stub's listener consumed it with a receipt" receipted
}

# deny — a real PreToolUse `hook guard` call as build: the evidence guard
# refuses `./build.sh` bundled with an ack. Prints the guard's answer.
deny() {
  jq -nc --arg c './build.sh; muxcode send edit ack "building"' \
    '{hook_event_name:"PreToolUse",tool_name:"Bash",tool_use_id:"toolu_mux190",tool_input:{command:$c}}' \
    | (cd "$WORK/project" && AGENT_ROLE=build BUS_ROLE=build BUS_SESSION="$SESSION" MUXCODE_BUILD_CLI=claude "$MUX" hook guard 2>/dev/null)
}

# deny_checked LABEL — deny, asserting the guard refused the call and wrote one
# new guard-denied row naming build, so a no-op hook call cannot pass as a denial.
deny_checked() {
  local before
  before=$(event_rows guard-denied | grep -cF "build: Bash")
  case $(deny) in *"hook-road evidence"*) ok "$1: hook guard refused the bundled ./build.sh" ;; *) fail "$1: hook guard did not refuse the bundled call" ;; esac
  check "$1: one new guard-denied row names build" [ "$(event_rows guard-denied | grep -cF "build: Bash")" -eq $((before + 1)) ]
}

# ── A: re-drive, then a second denial ──────────────────────────────
echo "-- A: denial on an in-flight graph dispatch, idle pane"
setup_run a "$WORK/bin" 30
TYPED_BEFORE=$(typed_lines)
deny_checked A
wait_for 10 "A: within one poll a re-drive carries the denial text into the pane" denial_typed
wait_for 10 "A: a guard-denied-redrive row names the task" row_naming guard-denied-redrive "$ID"
deny_checked A2
wait_for 10 "A2: a second denial writes a guard-denied-alert row naming the task" row_naming guard-denied-alert "$ID"
check "A2: edit is alerted, naming task, run and node" edit_alerted
check "A2: no second re-drive (re-drives: $(redrives))" [ "$(redrives)" -eq 1 ]
wait_for 45 "A: the node ends at its 30 s cap" node_done
check "A: the node's output names guard-denied and the guard's reason" output_names_denial
boundary "A: lines typed after the denial: $(( $(typed_lines) - TYPED_BEFORE )); node state=$(node_field state) outcome=$(node_field outcome) after $(( $(date +%s) - START ))s; task $(task_status)"
boundary "A: rows: $(jq -r 'select(.event | test("guard-denied|redrive|task-stall")) | .event' "$LIFELOG" 2>/dev/null | sort | uniq -c | tr -s ' \n' ' ')"

# ── N: negative control — no in-flight request ─────────────────────
echo "-- N: negative control — a denial with no in-flight request"
wait_for 15 "N: A's task has settled timed-out and nothing is in flight" a_settled
TYPED_BEFORE=$(typed_lines)
REDRIVE_ROWS=$(event_count guard-denied-redrive)
ALERT_ROWS=$(event_count guard-denied-alert)
deny_checked N
sleep 8
check "N: nothing typed into the pane" [ "$(typed_lines)" -eq "$TYPED_BEFORE" ]
check "N: no further re-drive row" [ "$(event_count guard-denied-redrive)" -eq "$REDRIVE_ROWS" ]
check "N: no further guard-denied-alert row" [ "$(event_count guard-denied-alert)" -eq "$ALERT_ROWS" ]

# ── B: one re-drive with the stall watchdog on ─────────────────────
# The watchdog checks every 30 s and re-drives on its second at-rest sighting,
# so 100 s after the denial spans the window in which it would re-drive again.
echo "-- B: stall watchdog on — the denied request is re-driven once, by either road"
unset MUXCODE_TASK_STALL_DISABLE
setup_run b "$WORK/bin" 120
deny_checked B
sleep 100
check "B: exactly one re-drive in 100 s (got $(redrives))" [ "$(redrives)" -eq 1 ]
check "B: it carries the denial text" denial_typed
boundary "B: rows: $(jq -r 'select(.event | test("guard-denied|redrive|task-stall")) | .event' "$LIFELOG" 2>/dev/null | sort | uniq -c | tr -s ' \n' ' ')"
export MUXCODE_TASK_STALL_DISABLE=1

# ── C: the re-drive is answered ────────────────────────────────────
echo "-- C: the stub answers the re-drive"
setup_run c "$WORK/bin" 60
echo 1 >"$REPLY"
deny_checked C
wait_for 30 "C: the stub ran the re-drive's reply command and the node succeeded" node_success
check "C: the node's output names guard-denied and the guard's reason" output_names_denial

# ── D: a busy pane ─────────────────────────────────────────────────
echo "-- D: denial while the pane is working"
setup_run d "$WORK/bin" 120
echo 1 >"$BUSY"
sleep 3
TYPED_BEFORE=$(typed_lines)
deny_checked D
wait_for 10 "D: the re-drive is withheld (guard-denied-redrive-deferred names the task)" row_naming guard-denied-redrive-deferred "$ID"
sleep 5
check "D: nothing typed while the pane works" [ "$(typed_lines)" -eq "$TYPED_BEFORE" ]
check "D: no alert while the pane works" [ "$(event_count guard-denied-alert)" -eq 0 ]
echo 0 >"$BUSY"
wait_for 40 "D: idle and unanswered, a guard-denied-alert row names the task" row_naming guard-denied-alert "$ID"
check "D: edit is alerted, naming task, run and node" edit_alerted
check "D: still nothing typed — the alert replaced the re-drive" [ "$(typed_lines)" -eq "$TYPED_BEFORE" ]

# ── P: control — no watch ──────────────────────────────────────────
echo "-- P: control — a build without the watch"
setup_run p "$WORK/bin-pre" 30
TYPED_BEFORE=$(typed_lines)
deny_checked P
sleep 10
check "P: nothing typed into the pane" [ "$(typed_lines)" -eq "$TYPED_BEFORE" ]
wait_for 45 "P: the node ends at its 30 s cap" node_done
check "P: it failed with output exactly 'node timeout' — no denial named (got: $(node_field output))" bare_timeout
wait_for 15 "P: its task settled timed-out" task_timed_out
check "P: no guard-denied-redrive row" [ "$(event_count guard-denied-redrive)" -eq 0 ]

# ── Coverage floor ─────────────────────────────────────────────────
echo
echo "=== results: $((PASS + FAIL)) checks, $PASS passed, $FAIL failed (floor $EXPECTED_PASS) ==="
if [ "$PASS" -ne "$EXPECTED_PASS" ]; then
  echo "  FAIL  coverage floor: expected exactly $EXPECTED_PASS passes, got $PASS — a section was skipped, double-counted, or the fix is not in"
  exit 1
fi
[ "$FAIL" -eq 0 ] || exit 1
exit 0
