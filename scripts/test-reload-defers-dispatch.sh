#!/usr/bin/env bash
# Integration test for MUX-217 — a reload must never clear a graph dispatch.
#
# On 2026-11-13 a build installed an edited test-runner definition. The
# daemon's watchdog reloaded the test agent in the same poll iteration the
# graph dispatched its test node, the relaunch cleared that in-flight task, and
# the node failed three seconds in with no suite run. The fix has two layers:
# the watchdog defers a reload while a running run owns, readies or is about
# to feed a send node to the role (bus.GraphNeedsRole), and reserves the reload
# marker at its check, while the executor holds a send to a role under a live
# marker (roleReloading in bus/graph_exec.go).
#
# Hermetic: scratch bus, private tmux server, scratch daemon, muxcode and
# scripts/fixtures/claude-stub built from THIS tree, agent definitions in a
# scratch install dir, every command run from an empty scratch project so no
# repository .claude/agents shadows them. The stubs are real launched agents —
# the launcher stamps their definition hash and reloads go through the real
# relaunch — but they never answer, so the script answers each graph dispatch
# as its agent would, with an EXIT=0 reply.
#
# The pre-fix build is this tree with both MUX-217 guards patched out of a
# scratch copy (the GraphNeedsRole clause in AutoReloadSafe and the
# roleReloading hold in dispatchNode); every other line is the tree's own, so
# the controls differ from the fix by those two guards alone.
#
# Sections:
#   A  a build → test graph; while build's node runs, test's installed
#      definition changes: the watchdog logs agent-defs-reload-deferred naming
#      the run and node b, and does not reload test
#   B  test's dispatch is answered and node b succeeds, with a watchdog check
#      passing while it is in flight; no cleared-inflight-tasks row for test;
#      the run completes
#   C  the deferred reload lands after the run completes and finishes
#   D  deferral control — the pre-fix daemon reloads test on the same change
#      while build's node still runs, and logs no deferral
#   E  the 2026-11-13 failure, reproduced on the pre-fix build: the stub holds
#      /exit (CLAUDE_STUB_EXIT_DELAY_FILE), so a reload's stop-to-relaunch
#      window stays open; a test dispatch lands inside it, the relaunch clears
#      its task and the node fails — its output naming the relaunch and the
#      allowance, never a bare "task timed-out"
#   F  the same window on this build: the node is held ready with nothing
#      sent and a graph-dispatch-deferred row, dispatched once the relaunch
#      releases test, answered and successful, with no task cleared
# A coverage floor keeps a skipped section from reporting green.
# Requires go, tmux, jq.
set -uo pipefail

PASS=0
FAIL=0
EXPECTED_PASS=46
for t in tmux go jq; do
  command -v "$t" >/dev/null 2>&1 || { echo "SKIP: $t is required"; exit 2; }
done

REPO=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
WORK=$(mktemp -d /tmp/reload-defers-XXXXXX)
SESSION="reload-defers-$$"
. "$REPO/scripts/lib/muxcode-bus-dir.sh"
BUSDIR="" LOG=""

# The script is itself run by an agent: an inherited AGENT_ROLE would reach
# every pane the private server creates.
unset AGENT_ROLE BUS_ROLE TMUX MUXCODE_CONFIG MUXCODE_AGENTDEFS_WATCH_DISABLE
export BUS_SESSION="$SESSION"
export HOME="$WORK/home"
export TMUX_TMPDIR="$WORK/tmux"
export MUXCODE_LIFECYCLE_LOG_DIR="$WORK/lifecycle"
export MUXCODE_INSTALL_DIR="$WORK/install"
export MUXCODE_AGENT_CLI=claude MUXCODE_BUILD_CLI=claude MUXCODE_TEST_CLI=claude
export MUXCODE_AGENT_HEALTH_CHECK_SECS=3 MUXCODE_DEFINITION_CHECK_SECS=3 MUXCODE_AGENT_HEARTBEAT=86400
export MUXCODE_EDIT_AUTO_RESTART_DISABLE=1 MUXCODE_CONTROL_PANE_DISABLE=1 MUXCODE_FORCE_RESPOND_DISABLE=1
export MUXCODE_TMP_CLEANUP_THRESHOLD=0 MUXCODE_ACTIVE_WATCHDOG_SECS=0 MUXCODE_PROMPT_AGENT_DISABLE=1
export MUXCODE_BRANCH_TIME_DISABLE=1 MUXCODE_STUCK_RELOAD_DISABLE=1 MUXCODE_DEDUP_WINDOW=0
export PATH="$WORK/bin:$PATH"
mkdir -p "$WORK/bin" "$WORK/bin-pre" "$WORK/home" "$WORK/tmux" "$WORK/lifecycle" "$WORK/install/agents" "$WORK/project"
LIFELOG="$MUXCODE_LIFECYCLE_LOG_DIR/$SESSION.log"
EXIT_DELAY="$WORK/exit-delay"
: >"$EXIT_DELAY"

DPID=""
# start_daemon [BIN] — a scratch daemon from the empty project, the tree's
# muxcode unless BIN names the pre-fix one.
start_daemon() {
  local bin=${1:-$MUX}
  (cd "$WORK/project" && exec "$bin" watch "$SESSION" --poll 2 >>"$WORK/daemon.log" 2>&1) &
  DPID=$!
  sleep 1
}
stop_daemon() {
  if [ -n "$DPID" ]; then kill "$DPID" 2>/dev/null; wait "$DPID" 2>/dev/null; DPID=""; fi
}
cleanup() {
  stop_daemon
  tmux kill-server 2>/dev/null
  sleep 1
  rm -rf ${BUSDIR:+"$BUSDIR"} "$WORK" 2>/dev/null
  true
}
trap cleanup EXIT

ok()   { PASS=$((PASS + 1)); echo "  ok: $1"; }
fail() { FAIL=$((FAIL + 1)); echo "  FAIL: $1"; }

pane_pid()       { tmux display-message -p -t "$SESSION:$1.1" '#{pane_pid}' 2>/dev/null; }
stub_pid()       { pgrep -P "$(pane_pid "$1" || echo 0)" -x claude 2>/dev/null | head -1 || true; }
stub_up()        { local p; p=$(stub_pid "$1"); [ -n "$p" ] && [ "$p" != "${2:-}" ]; }
listener_alive() { local pid; pid=$(cat "$BUSDIR/polling-$1.marker" 2>/dev/null) && [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; }
type_in()        { tmux send-keys -t "$SESSION:$1.1" -l -- "$2"; sleep 0.1; tmux send-keys -t "$SESSION:$1.1" Enter; }

# rows EVENT DETAIL-REGEX — lifecycle rows of EVENT whose detail matches, one per line.
rows()      { jq -r --arg e "$1" 'select(.event == $e) | .detail' "$LIFELOG" 2>/dev/null | grep -E -- "$2" || true; }
row_count() { rows "$1" "$2" | grep -c . || true; }
has_row()   { [ "$(row_count "$1" "$2")" -ge 1 ]; }
# first_ts EVENT DETAIL-REGEX — unix time of the first matching row.
first_ts()  { jq -r --arg e "$1" --arg d "$2" 'select(.event == $e and (.detail | test($d))) | .ts' "$LIFELOG" 2>/dev/null | head -1; }
reloads_of_test() { row_count launch 'role=test .*reason=reload'; }
reloaded_twice()  { [ "$(reloads_of_test)" -ge 2 ]; }
cleared_test()    { row_count cleared-inflight-tasks '^test:'; }
marker_held()     { [ -e "$BUSDIR/lock/test.reloading" ]; }
# reload_finished — the relaunch released its marker and test's listener is
# back. Stopping a daemon mid-reload kills the reload holding the marker, which
# then keeps the next daemon off test until it goes stale at 60s.
reload_finished() { ! marker_held && listener_alive test; }

dispatch_id() {
  jq -r --arg r "$1" --arg run "$2" --arg n "$3" \
    'select(.type == "request" and .to == $r and .graph_run == $run and .graph_node == $n) | .id' "$LOG" 2>/dev/null | tail -1
}
dispatched() { [ -n "$(dispatch_id "$1" "$2" "$3")" ]; }
answer() {
  local id
  id=$(dispatch_id "$1" "$2" "$3")
  AGENT_ROLE="$1" "$MUX" send edit report "Status reported. EXIT=0" --type response --reply-to "$id" --no-notify >/dev/null 2>&1
}
graph_line() { "$MUX" graph status "$1" 2>/dev/null | sed $'s/\x1b\\[[0-9;]*[A-Za-z]//g'; }
node_state() { graph_line "$1" | awk -v n="$2" '$1==n {print $2}'; }
node_has()   { graph_line "$1" | awk -v n="$2" '$1==n' | grep -q -- "$3"; }
run_state()  { graph_line "$1" | head -1 | sed 's/.*\[\([a-z]*\)\].*/\1/'; }
node_is()    { [ "$(node_state "$1" "$2")" = "$3" ]; }
run_is()     { [ "$(run_state "$1")" = "$2" ]; }
# start_run FILE [BIN] — starts a run of FILE and prints its id.
start_run()  { "${2:-$MUX}" graph run --file "$1" 2>&1 | grep -o 'Started run [^ ]*' | awk '{print $3}'; }

# wait_for SECS DESCRIPTION CHECK [ARGS...] — polls once a second; a timeout is
# a recorded FAIL plus a state dump.
wait_for() {
  local secs=$1 desc=$2 i=0
  shift 2
  while [ "$i" -lt "$secs" ]; do
    if "$@" >/dev/null 2>&1; then ok "$desc"; return 0; fi
    sleep 1; i=$((i + 1))
  done
  fail "$desc (timeout ${secs}s)"
  dump_state
  return 1
}
dump_state() {
  echo "  --- state dump ---"
  echo "  [lifecycle tail]"; jq -r '"\(.ts) \(.event) \(.detail // "")"' "$LIFELOG" 2>/dev/null | tail -10 | sed 's/^/    /'
  echo "  [daemon log tail]"; tail -5 "$WORK/daemon.log" 2>/dev/null | sed 's/^/    /'
  echo "  --- end dump ---"
}

echo "=== MUX-217 reload defers a graph dispatch ==="

(cd "$REPO/tools/muxcode" && go build -buildvcs=false -o "$WORK/bin/muxcode" .) \
  || { echo "SKIP: go build muxcode failed"; exit 2; }
(cd "$REPO/scripts/fixtures/claude-stub" && go build -buildvcs=false -o "$WORK/bin/claude" .) \
  || { echo "SKIP: go build claude-stub failed"; exit 2; }
MUX="$WORK/bin/muxcode"
BUSDIR="$(muxcode_bus_dir "$MUX" "$SESSION")" || exit 1
LOG="$BUSDIR/log.jsonl"
ok "scratch muxcode and claude-stub built from this tree"

PRE="$WORK/pre/muxcode"
mkdir -p "$WORK/pre"
cp -R "$REPO/tools/muxcode" "$WORK/pre/"
sed -i.orig 's/GraphNeedsRole(session, role); ok {/GraphNeedsRole(session, role); false \&\& ok {/' "$PRE/bus/agentdefs.go"
sed -i.orig 's/if roleReloading(session, n.Role) {/if false \&\& roleReloading(session, n.Role) {/' "$PRE/bus/graph_exec.go"
if grep -q 'GraphNeedsRole(session, role); false && ok {' "$PRE/bus/agentdefs.go" \
  && grep -q 'if false && roleReloading(session, n.Role) {' "$PRE/bus/graph_exec.go" \
  && (cd "$PRE" && go build -buildvcs=false -o "$WORK/bin-pre/muxcode" .); then
  ok "pre-fix muxcode built: the tree with both MUX-217 guards patched out"
else
  fail "pre-fix build: a guard patch did not apply or the copy failed to build"
fi
MUX_PRE="$WORK/bin-pre/muxcode"
cd "$WORK/project" || exit 1

printf -- '---\ndescription: Builder\n---\nBuild things.\n' >"$WORK/install/agents/code-builder.md"
printf -- '---\ndescription: Tester\n---\nRun the suite.\n' >"$WORK/install/agents/test-runner.md"
cat >"$WORK/graph.json" <<'EOF'
{"name": "mux217-reload", "start": "a",
 "nodes": [{"id": "a", "type": "send", "role": "build", "action": "report", "message": "Report your status"},
           {"id": "b", "type": "send", "role": "test", "action": "report", "message": "Report your status"}],
 "edges": [{"from": "a", "to": "b"}]}
EOF
cat >"$WORK/window.json" <<'EOF'
{"name": "mux217-window", "start": "t",
 "nodes": [{"id": "t", "type": "send", "role": "test", "action": "report", "message": "Report your status"}],
 "edges": []}
EOF

# Each agent pane is a non-login bash with its own role identity.
pane_env() {
  echo "export AGENT_ROLE=$1 BUS_ROLE=$1 BUS_SESSION=$SESSION HOME=$HOME TMUX_TMPDIR=$TMUX_TMPDIR MUXCODE_LIFECYCLE_LOG_DIR=$MUXCODE_LIFECYCLE_LOG_DIR MUXCODE_INSTALL_DIR=$MUXCODE_INSTALL_DIR MUXCODE_AGENT_CLI=claude MUXCODE_BUILD_CLI=claude MUXCODE_TEST_CLI=claude CLAUDE_STUB_EXIT_DELAY_FILE=$EXIT_DELAY PATH=$PATH; PS1='\$ '; clear"
}
tmux new-session -d -s "$SESSION" -n build -x 160 -y 40 -c "$WORK/project" "/bin/bash --noprofile --norc"
tmux set-option -g default-command "/bin/bash --noprofile --norc"
tmux split-window -h -t "$SESSION:build" -c "$WORK/project"
tmux new-window -t "$SESSION" -n test -c "$WORK/project"
tmux split-window -h -t "$SESSION:test" -c "$WORK/project"
for w in build test; do type_in "$w" "$(pane_env "$w")"; done
sleep 1
"$MUX" init "$SESSION" >/dev/null 2>&1 || true
start_daemon
kill -0 "$DPID" 2>/dev/null && ok "scratch daemon running" || fail "scratch daemon exited: $(tail -3 "$WORK/daemon.log")"
type_in build "muxcode agent launch build --reason user"
type_in test "muxcode agent launch test --reason user"
wait_for 20 "build agent (claude-stub) up" stub_up build
wait_for 20 "test agent (claude-stub) up" stub_up test
wait_for 15 "build agent's inbox listener running" listener_alive build
wait_for 15 "test agent's inbox listener running" listener_alive test

# ── A: the definition changes while build's node runs ──────────────
echo "-- A: test's definition changes while build's node runs"
RID=$(start_run "$WORK/graph.json")
[ -n "$RID" ] && ok "A: graph run started ($RID)" || fail "A: graph run did not start"
wait_for 20 "A: build's node a dispatched" dispatched build "$RID" a
printf '\nAlso report flaky tests.\n' >>"$WORK/install/agents/test-runner.md"
wait_for 35 "A: the watchdog deferred test's reload naming run $RID and node b" \
  has_row agent-defs-reload-deferred "^test: graph run $RID needs test as node b$"
[ "$(reloads_of_test)" = 0 ] && ok "A: test was not reloaded while the run needed it" \
  || fail "A: test reloaded $(reloads_of_test) time(s) while the run needed it"

# ── B: test's dispatch is answered and the run completes ───────────
echo "-- B: test's dispatch survives and is answered"
answer build "$RID" a
wait_for 20 "B: test's node b dispatched" dispatched test "$RID" b
sleep 12 # one watchdog check with b in flight
[ "$(reloads_of_test)" = 0 ] && ok "B: no reload of test across a watchdog check with its dispatch in flight" \
  || fail "B: test reloaded with b in flight"
answer test "$RID" b
wait_for 20 "B: node b done" node_is "$RID" b done
node_has "$RID" b 'outcome=success' && ok "B: node b succeeded on its answered dispatch" \
  || fail "B: node b: $(graph_line "$RID" | awk '$1=="b"')"
wait_for 20 "B: the run completed" run_is "$RID" complete
[ "$(cleared_test)" = 0 ] && ok "B: no cleared-inflight-tasks row for test" \
  || fail "B: $(rows cleared-inflight-tasks '^test:')"

# ── C: the deferred reload lands after the run ─────────────────────
echo "-- C: the deferred reload fires once the run releases test"
wait_for 35 "C: agent-defs-reload issued for test" has_row agent-defs-reload '^test definition changed'
wait_for 20 "C: test relaunched with reason=reload" has_row launch 'role=test .*reason=reload'
wait_for 30 "C: the reload finished and released test's marker" reload_finished
done_ts=$(first_ts graph-run-complete "$RID")
reload_ts=$(first_ts agent-defs-reload '^test definition changed')
[ -n "$done_ts" ] && [ -n "$reload_ts" ] && [ "$reload_ts" -ge "$done_ts" ] \
  && ok "C: the reload ($reload_ts) came after the run completed ($done_ts)" \
  || fail "C: reload at '${reload_ts}', run complete at '${done_ts}'"

# ── D: deferral control on the pre-fix daemon ──────────────────────
echo "-- D: deferral control — the pre-fix daemon on the same change"
stop_daemon
start_daemon "$MUX_PRE"
kill -0 "$DPID" 2>/dev/null && ok "D: pre-fix daemon running" || fail "D: pre-fix daemon exited"
RID2=$(start_run "$WORK/graph.json" "$MUX_PRE")
[ -n "$RID2" ] && ok "D: second run started ($RID2)" || fail "D: second run did not start"
wait_for 20 "D: build's node a dispatched" dispatched build "$RID2" a
printf '\nAlso report slow tests.\n' >>"$WORK/install/agents/test-runner.md"
wait_for 35 "D: test reloaded while the run still needed it" reloaded_twice
node_is "$RID2" a running && ok "D: build's node a was still running at that reload" \
  || fail "D: node a state $(node_state "$RID2" a) — the reload did not land inside the run"
[ "$(row_count agent-defs-reload-deferred "graph run $RID2 ")" = 0 ] && ok "D: no deferral logged for $RID2" \
  || fail "D: $(rows agent-defs-reload-deferred "graph run $RID2 ")"
# The pre-fix executor has no hold either: answer build only once the reload is
# done, so b cannot land in the reload's tail — that is section E's subject.
wait_for 30 "D: the reload finished before build is answered" reload_finished
answer build "$RID2" a
dispatched_b2() { dispatched test "$RID2" b && answer test "$RID2" b; }
wait_for 30 "D: node b dispatched and answered" dispatched_b2
wait_for 20 "D: the second run completed" run_is "$RID2" complete

# ── E: the 2026-11-13 failure on the pre-fix build ─────────────────
echo "-- E: pre-fix — a dispatch inside the reload window is cleared and its node fails"
stop_daemon
start_daemon "$MUX_PRE"
kill -0 "$DPID" 2>/dev/null && ok "E: pre-fix daemon running" || fail "E: pre-fix daemon exited"
CLEARED=$(cleared_test)
echo 7 >"$EXIT_DELAY"
"$MUX_PRE" reload test >"$WORK/reload-e.out" 2>&1 &
RPID=$!
wait_for 5 "E: the reload holds test's marker while the stub delays /exit" marker_held
RID3=$(start_run "$WORK/window.json" "$MUX_PRE")
[ -n "$RID3" ] && ok "E: window run started ($RID3)" || fail "E: window run did not start"
in_window_e() { dispatched test "$RID3" t && marker_held; }
wait_for 6 "E: the pre-fix executor dispatched test inside the stop-to-relaunch window" in_window_e
echo 0 >"$EXIT_DELAY"
wait "$RPID" 2>/dev/null
cleared_more() { [ "$(cleared_test)" -gt "$CLEARED" ]; }
wait_for 20 "E: the relaunch cleared test's in-flight task (cleared-inflight-tasks)" cleared_more
node_failed_e() { node_has "$RID3" t 'outcome=failure'; }
wait_for 20 "E: node t failed on the cleared dispatch — the 2026-11-13 outcome" node_failed_e
E_OUT=$(jq -r '.output // ""' "$BUSDIR/graphs/$RID3/nodes/t.json" 2>/dev/null)
case $E_OUT in
  *"dispatch cleared by a relaunch — test relaunched (reason=reload)"*"allowance"*)
    ok "E: the failure names the relaunch that cleared it, not a bare timed-out" ;;
  *) fail "E: node t output '$E_OUT' does not name the relaunch" ;;
esac
wait_for 30 "E: the reload finished" reload_finished

# ── F: the same window on this build ───────────────────────────────
echo "-- F: this build — the dispatch is held until the relaunch, then succeeds"
stop_daemon
start_daemon
kill -0 "$DPID" 2>/dev/null && ok "F: daemon running on this build" || fail "F: daemon exited"
CLEARED=$(cleared_test)
echo 7 >"$EXIT_DELAY"
"$MUX" reload test >"$WORK/reload-f.out" 2>&1 &
RPID=$!
wait_for 5 "F: the reload holds test's marker while the stub delays /exit" marker_held
RID4=$(start_run "$WORK/window.json")
[ -n "$RID4" ] && ok "F: window run started ($RID4)" || fail "F: window run did not start"
sleep 4 # two executor ticks inside the window
if marker_held && ! dispatched test "$RID4" t && node_is "$RID4" t ready; then
  ok "F: node t held ready inside the window — nothing sent to test"
else
  fail "F: node t $(node_state "$RID4" t), dispatched=$(dispatch_id test "$RID4" t), marker held=$(marker_held && echo yes || echo no)"
fi
has_row graph-dispatch-deferred "^$RID4: t waits — agent reload in progress: test$" \
  && ok "F: graph-dispatch-deferred names the run, the node and the reload" \
  || fail "F: graph-dispatch-deferred rows: $(rows graph-dispatch-deferred .)"
echo 0 >"$EXIT_DELAY"
wait "$RPID" 2>/dev/null
wait_for 25 "F: node t dispatched once the relaunch released test" dispatched test "$RID4" t
answer test "$RID4" t
node_ok_f() { node_has "$RID4" t 'outcome=success'; }
wait_for 20 "F: node t succeeded on its dispatch" node_ok_f
[ "$(cleared_test)" = "$CLEARED" ] && ok "F: the relaunch cleared no in-flight task" \
  || fail "F: cleared-inflight-tasks rows for test rose to $(cleared_test)"

# ── Coverage floor ─────────────────────────────────────────────────
echo
echo "=== results: $PASS passed, $FAIL failed (floor $EXPECTED_PASS) ==="
if [ "$PASS" -ne "$EXPECTED_PASS" ]; then
  echo "  FAIL  coverage floor: expected exactly $EXPECTED_PASS passes, got $PASS — a section was skipped or double-counted"
  exit 1
fi
[ "$FAIL" -eq 0 ] || exit 1
exit 0
