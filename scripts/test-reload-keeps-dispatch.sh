#!/usr/bin/env bash
# Integration test for MUX-196 — relaunching an agent must not expire a graph
# dispatch it has not read.
#
# On 2026-09-28 a ci-watch node (timeout_secs 3600) dispatched to a watch agent
# that was down; the request sat unread in its inbox. `muxcode reload watch`
# relaunched it, the launch timed out every in-flight task addressed to watch,
# and the node failed "task timed-out" 250 s into its hour — while the
# relaunched agent read the request and answered it a minute later. The fix:
# a launch keeps a task whose request has no receipt and still waits in the
# inbox (inflight-task-kept), clears the rest (inflight-task-cleared), and the
# executor re-sends a cleared dispatch on the allowance left
# (graph-dispatch-resent) instead of failing the node.
#
# Hermetic: scratch bus, private tmux server, scratch daemon, muxcode and
# scripts/fixtures/claude-stub built from THIS tree, agent definitions in a
# scratch install dir. The stub runs a real `muxcode inbox --poll --loop`
# listener; freezing it (SIGSTOP) leaves a dispatch unread with the agent up,
# as a wedged or dead agent leaves it. The script answers each dispatch as the
# agent would, with an EXIT=0 reply.
#
# Sections — every relaunch road funnels into RunAgentLaunchResume, so each is
# driven once; the stuck-provider reload is ReloadAgent, A's road:
#   A  `muxcode reload` with the dispatch unread: inflight-task-kept names it
#      consumed=no, no row clears it, the relaunched agent reads it, its task
#      stays in flight, the answer succeeds the node and the run completes
#   B  the daemon's health restart of a crashed (SIGTERMed) agent, the same way
#   C  `muxcode resume --force`, the same way
#   D  a dispatch the live agent consumed and never answered is still cleared
#      on reload (inflight-task-cleared consumed=yes) and re-sent
#      (graph-dispatch-resent); the answer to the re-send succeeds the node
#   E  negative control — a kept dispatch nobody answers still fails at its own
#      timeout_secs, not before and not on the relaunch
#   F  control — this tree with the MUX-196 keep and re-send patched out of a
#      scratch copy reproduces 2026-09-28: A's reload clears the unread
#      dispatch and the node fails naming the relaunch
# A coverage floor keeps a skipped section from reporting green.
# Requires go, tmux, jq. ~3 min.
set -uo pipefail
export MUXCODE_CONSUMER_GUARD=0 # dispatches must land while the agent is frozen (MUX-145)

PASS=0
FAIL=0
EXPECTED_PASS=60
for t in tmux go jq; do
  command -v "$t" >/dev/null 2>&1 || { echo "SKIP: $t is required"; exit 2; }
done

REPO=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
WORK=$(mktemp -d /tmp/reload-keeps-XXXXXX)
SESSION="reload-keeps-$$"
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
export MUXCODE_AGENT_CLI=claude MUXCODE_WATCH_CLI=claude
export MUXCODE_AGENT_HEALTH_CHECK_SECS=3 MUXCODE_DEFINITION_CHECK_SECS=3 MUXCODE_AGENT_HEARTBEAT=86400
export MUXCODE_EDIT_AUTO_RESTART_DISABLE=1 MUXCODE_CONTROL_PANE_DISABLE=1 MUXCODE_FORCE_RESPOND_DISABLE=1
export MUXCODE_TMP_CLEANUP_THRESHOLD=0 MUXCODE_ACTIVE_WATCHDOG_SECS=0 MUXCODE_PROMPT_AGENT_DISABLE=1
export MUXCODE_BRANCH_TIME_DISABLE=1 MUXCODE_STUCK_RELOAD_DISABLE=1 MUXCODE_DEDUP_WINDOW=0
export MUXCODE_TASK_STALL_SECS=600 # no stall redrive may deliver a frozen agent's dispatch for it
export PATH="$WORK/bin:$PATH"
mkdir -p "$WORK/bin" "$WORK/bin-pre" "$WORK/home" "$WORK/tmux" "$WORK/lifecycle" "$WORK/install/agents" "$WORK/project"
LIFELOG="$MUXCODE_LIFECYCLE_LOG_DIR/$SESSION.log"
EXIT_DELAY="$WORK/exit-delay"
: >"$EXIT_DELAY"

DPID=""
start_daemon() {
  (cd "$WORK/project" && exec "$MUX" watch "$SESSION" --poll 2 >>"$WORK/daemon.log" 2>&1) &
  DPID=$!
  sleep 1
}
stop_daemon() {
  if [ -n "$DPID" ]; then kill "$DPID" 2>/dev/null; wait "$DPID" 2>/dev/null; DPID=""; fi
}
cleanup() {
  [ -n "${FROZEN:-}" ] && kill -CONT "$FROZEN" 2>/dev/null
  stop_daemon
  tmux kill-server 2>/dev/null
  sleep 1
  rm -rf ${BUSDIR:+"$BUSDIR"} "$WORK" 2>/dev/null
  true
}
trap cleanup EXIT

ok()   { PASS=$((PASS + 1)); echo "  ok: $1"; }
fail() { FAIL=$((FAIL + 1)); echo "  FAIL: $1"; }

pane_pid()       { tmux display-message -p -t "$SESSION:watch.1" '#{pane_pid}' 2>/dev/null; }
stub_pid()       { pgrep -P "$(pane_pid || echo 0)" -x claude 2>/dev/null | head -1 || true; }
stub_up()        { local p; p=$(stub_pid); [ -n "$p" ] && [ "$p" != "${1:-}" ]; }
listener_pid()   { cat "$BUSDIR/polling-watch.marker" 2>/dev/null; }
listener_alive() { local pid; pid=$(listener_pid) && [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; }
type_in()        { tmux send-keys -t "$SESSION:$1.1" -l -- "$2"; sleep 0.1; tmux send-keys -t "$SESSION:$1.1" Enter; }
watch_ready()    { stub_up && listener_alive && [ ! -e "$BUSDIR/lock/watch.reloading" ]; }
# freeze — SIGSTOP watch's listener so a dispatch stays unread while the agent
# is up; its relaunch kills the frozen process.
FROZEN=""
freeze() { FROZEN=$(listener_pid) && [ -n "$FROZEN" ] && kill -STOP "$FROZEN"; }

rows()      { jq -r --arg e "$1" 'select(.event == $e) | .detail' "$LIFELOG" 2>/dev/null | grep -E -- "$2" || true; }
row_count() { rows "$1" "$2" | grep -c . || true; }
has_row()   { [ "$(row_count "$1" "$2")" -ge 1 ]; }
launches()  { row_count launch '^role=watch '; }
relaunched_since() { [ "$(launches)" -gt "$1" ]; }

# dispatch_id RUN — the latest request the run's ci-watch node sent watch.
dispatch_id() {
  jq -r --arg run "$1" 'select(.type == "request" and .to == "watch" and .graph_run == $run and .graph_node == "ci-watch") | .id' "$LOG" 2>/dev/null | tail -1
}
dispatch_count() {
  jq -r --arg run "$1" 'select(.type == "request" and .to == "watch" and .graph_run == $run) | .id' "$LOG" 2>/dev/null | grep -c . || true
}
dispatched() { [ -n "$(dispatch_id "$1")" ]; }
receipted()  { jq -e '(.acked_at // 0) > 0 or .status == "responded"' "$BUSDIR/delivery/$1.status" >/dev/null 2>&1; }
queued()     { jq -c --arg id "$1" 'select(.id == $id)' "$BUSDIR/inbox/watch.jsonl" 2>/dev/null | grep -q .; }
unread()     { queued "$1" && ! receipted "$1"; }
task_status() { jq -r '.status' "$BUSDIR/tasks/$1.json" 2>/dev/null; }
answer() {
  AGENT_ROLE=watch "$MUX" send edit watch "CI-GREEN $2 EXIT=0" --type response --reply-to "$1" --no-notify >/dev/null 2>&1
}
node_json()   { cat "$BUSDIR/graphs/$1/nodes/ci-watch.json" 2>/dev/null; }
node_field()  { node_json "$1" | jq -r ".$2 // \"\""; }
node_outcome_is() { [ "$(node_field "$1" outcome)" = "$2" ]; }
graph_line()  { "$MUX" graph status "$1" 2>/dev/null | sed $'s/\x1b\\[[0-9;]*[A-Za-z]//g'; }
run_is()      { [ "$(graph_line "$1" | head -1 | sed 's/.*\[\([a-z]*\)\].*/\1/')" = "$2" ]; }
start_run()   { "$MUX" graph run --file "$1" 2>&1 | grep -o 'Started run [^ ]*' | awk '{print $3}'; }

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
check() { local desc=$1; shift; if "$@" >/dev/null 2>&1; then ok "$desc"; else fail "$desc"; dump_state; fi; }
dump_state() {
  echo "  --- state dump ---"
  echo "  [lifecycle tail]"; jq -r '"\(.ts) \(.event) \(.detail // "")"' "$LIFELOG" 2>/dev/null | tail -12 | sed 's/^/    /'
  echo "  [daemon log tail]"; tail -5 "$WORK/daemon.log" 2>/dev/null | sed 's/^/    /'
  echo "  --- end dump ---"
}

# kept_unread SECTION RUN ID BEFORE — the relaunch after BEFORE launches kept
# ID: its row names it unconsumed, no row clears it, the relaunched agent
# reads it with its task still in flight, and its answer succeeds the node.
kept_unread() {
  local s=$1 rid=$2 id=$3 before=$4
  wait_for 40 "$s: watch relaunched" relaunched_since "$before"
  check "$s: inflight-task-kept names the unread dispatch consumed=no" \
    has_row inflight-task-kept "task $id daemon→watch:watch kept — consumed=no"
  check "$s: no row clears it" [ "$(row_count inflight-task-cleared "task $id ")" = 0 ]
  wait_for 20 "$s: the relaunched agent read the kept dispatch" receipted "$id"
  check "$s: its task stayed in flight on its own clock" [ "$(task_status "$id")" = in-flight ]
  answer "$id" "$s"
  wait_for 20 "$s: ci-watch succeeded on the relaunched agent's answer" node_outcome_is "$rid" success
  wait_for 10 "$s: the run completed" run_is "$rid" complete
  case $(node_field "$rid" output) in
    *timed-out* | *"cleared by a relaunch"*) fail "$s: node output blames the relaunch: $(node_field "$rid" output)" ;;
    *) ok "$s: the node output names no timeout or relaunch" ;;
  esac
}

echo "=== MUX-196 a relaunch keeps an unread graph dispatch ==="

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
sed -i.orig 's/task.ClearedBy != "" \&\& continueAfterRelaunch(/task.ClearedBy != "" \&\& false \&\& continueAfterRelaunch(/' "$PRE/bus/graph_exec.go"
sed -i.orig 's/if !consumed \&\& queued\[t.ID\] {/if false \&\& !consumed \&\& queued[t.ID] {/' "$PRE/bus/task.go"
if grep -q 'task.ClearedBy != "" && false && continueAfterRelaunch(' "$PRE/bus/graph_exec.go" \
  && grep -q 'if false && !consumed && queued\[t.ID\] {' "$PRE/bus/task.go" \
  && (cd "$PRE" && go build -buildvcs=false -o "$WORK/bin-pre/muxcode" .); then
  ok "pre-fix muxcode built: the tree with the MUX-196 keep and re-send patched out"
else
  fail "pre-fix build: a patch did not apply or the copy failed to build"
fi
cd "$WORK/project" || exit 1

printf -- '---\ndescription: Watcher\n---\nWatch CI.\n' >"$WORK/install/agents/log-watcher.md"
graph_file() {
  printf '{"name": "mux196-%s", "start": "ci-watch", "nodes": [{"id": "ci-watch", "type": "send", "role": "watch", "action": "watch", "message": "Wait for CI", "timeout_secs": %s}], "edges": []}\n' "$1" "$2" >"$WORK/$1.json"
}
graph_file hour 3600
graph_file short 25

pane_env() {
  echo "export AGENT_ROLE=$1 BUS_ROLE=$1 BUS_SESSION=$SESSION HOME=$HOME TMUX_TMPDIR=$TMUX_TMPDIR MUXCODE_LIFECYCLE_LOG_DIR=$MUXCODE_LIFECYCLE_LOG_DIR MUXCODE_INSTALL_DIR=$MUXCODE_INSTALL_DIR MUXCODE_AGENT_CLI=claude MUXCODE_WATCH_CLI=claude CLAUDE_STUB_EXIT_DELAY_FILE=$EXIT_DELAY PATH=$PATH; PS1='\$ '; clear"
}
tmux new-session -d -s "$SESSION" -n watch -x 160 -y 40 -c "$WORK/project" "/bin/bash --noprofile --norc"
tmux set-option -g default-command "/bin/bash --noprofile --norc"
tmux split-window -h -t "$SESSION:watch" -c "$WORK/project"
type_in watch "$(pane_env watch)"
sleep 1
"$MUX" init "$SESSION" >/dev/null 2>&1 || true
start_daemon
check "scratch daemon running" kill -0 "$DPID"
type_in watch "muxcode agent launch watch --reason user"
wait_for 20 "watch agent (claude-stub) up with its inbox listener" watch_ready

# ── A: muxcode reload ──────────────────────────────────────────────
echo "-- A: muxcode reload with the dispatch unread"
freeze
RID=$(start_run "$WORK/hour.json")
wait_for 15 "A: ci-watch dispatched" dispatched "$RID"
ID=$(dispatch_id "$RID")
check "A: the dispatch sits unread — queued with no receipt" unread "$ID"
BEFORE=$(launches)
"$MUX" reload watch >"$WORK/reload-a.out" 2>&1
kept_unread A "$RID" "$ID" "$BEFORE"
wait_for 20 "A: watch ready again" watch_ready

# ── B: the daemon's health restart ─────────────────────────────────
echo "-- B: health restart of a crashed agent with the dispatch unread"
freeze
RID=$(start_run "$WORK/hour.json")
wait_for 15 "B: ci-watch dispatched" dispatched "$RID"
ID=$(dispatch_id "$RID")
check "B: the dispatch sits unread" unread "$ID"
BEFORE=$(launches)
# SIGTERM, not SIGKILL: the stub's teardown scrolls its ❯ out of the pane,
# which IsAgentAlive reads as alive anywhere it shows, and kills the listener.
kill -TERM "$(stub_pid)" 2>/dev/null
FROZEN=""
kept_unread B "$RID" "$ID" "$BEFORE"
wait_for 20 "B: watch ready again" watch_ready

# ── C: muxcode resume ──────────────────────────────────────────────
echo "-- C: muxcode resume --force with the dispatch unread"
freeze
RID=$(start_run "$WORK/hour.json")
wait_for 15 "C: ci-watch dispatched" dispatched "$RID"
ID=$(dispatch_id "$RID")
check "C: the dispatch sits unread" unread "$ID"
stop_daemon # the health check must not race the manual resume
BEFORE=$(launches)
RESUMES=$(row_count launch '^role=watch .*reason=resume')
"$MUX" resume watch --force >"$WORK/resume-c.out" 2>&1
FROZEN=""
start_daemon
kept_unread C "$RID" "$ID" "$BEFORE"
check "C: the relaunch took the resume road (a new launch reason=resume)" \
  [ "$(row_count launch '^role=watch .*reason=resume')" -gt "$RESUMES" ]
wait_for 20 "C: watch ready again" watch_ready

# ── D: a consumed dispatch is cleared and re-sent ──────────────────
echo "-- D: a dispatch the agent read and never answered is cleared and re-sent"
RID=$(start_run "$WORK/hour.json")
wait_for 15 "D: ci-watch dispatched" dispatched "$RID"
ID=$(dispatch_id "$RID")
wait_for 15 "D: the live agent read it" receipted "$ID"
"$MUX" reload watch >"$WORK/reload-d.out" 2>&1
check "D: inflight-task-cleared names it consumed=yes" \
  has_row inflight-task-cleared "task $ID daemon→watch:watch cleared — consumed=yes"
wait_for 20 "D: graph-dispatch-resent names the run and the relaunch" \
  has_row graph-dispatch-resent "^$RID: ci-watch — agent relaunched: watch relaunched \(reason=reload\); dispatch $ID cleared"
resent() { [ "$(dispatch_count "$RID")" = 2 ]; }
wait_for 15 "D: a second dispatch reached watch" resent
ID2=$(dispatch_id "$RID")
answer "$ID2" D
wait_for 20 "D: ci-watch succeeded on the answer to the re-send" node_outcome_is "$RID" success
check "D: the run completed" run_is "$RID" complete
wait_for 20 "D: watch ready again" watch_ready

# ── E: negative control — an unanswered dispatch still times out ───
echo "-- E: a kept dispatch nobody answers fails at its own timeout_secs"
freeze
RID=$(start_run "$WORK/short.json")
wait_for 15 "E: ci-watch dispatched" dispatched "$RID"
ID=$(dispatch_id "$RID")
BEFORE=$(launches)
"$MUX" reload watch >"$WORK/reload-e.out" 2>&1
FROZEN=""
wait_for 20 "E: watch relaunched" relaunched_since "$BEFORE"
check "E: the relaunch kept the unread dispatch" has_row inflight-task-kept "task $ID "
check "E: ci-watch still running after the relaunch" [ "$(node_field "$RID" state)" = running ]
wait_for 45 "E: ci-watch failed unanswered" node_outcome_is "$RID" failure
E_START=$(node_field "$RID" started_at) E_DONE=$(node_field "$RID" done_at)
check "E: it failed only after its 25s allowance ($E_START → $E_DONE)" [ $((E_DONE - E_START)) -ge 25 ]
case $(node_field "$RID" output) in
  *"cleared by a relaunch"*) fail "E: the failure blames the relaunch: $(node_field "$RID" output)" ;;
  *timeout* | *expired*) ok "E: the failure names its own clock: $(node_field "$RID" output)" ;;
  *) fail "E: unexpected failure output: $(node_field "$RID" output)" ;;
esac
answer "$ID" E-late # its task would otherwise hold F's dispatch behind it (MUX-170)
wait_for 20 "E: watch ready again" watch_ready

# ── F: control — the pre-fix build ─────────────────────────────────
echo "-- F: pre-fix control — the reload clears the unread dispatch and fails the node"
stop_daemon
rm -f "$MUX" && cp "$WORK/bin-pre/muxcode" "$MUX" # a new inode: running listeners keep theirs
start_daemon
check "F: daemon running on the pre-fix build" kill -0 "$DPID"
freeze
RID=$(start_run "$WORK/hour.json")
wait_for 15 "F: ci-watch dispatched" dispatched "$RID"
ID=$(dispatch_id "$RID")
check "F: the dispatch sits unread" unread "$ID"
"$MUX" reload watch >"$WORK/reload-f.out" 2>&1
FROZEN=""
wait_for 20 "F: ci-watch failed on the relaunch — the 2026-09-28 outcome" node_outcome_is "$RID" failure
case $(node_field "$RID" output) in
  *"dispatch cleared by a relaunch — watch relaunched (reason=reload)"*) ok "F: the failure names the relaunch that cleared it" ;;
  *) fail "F: node output '$(node_field "$RID" output)' does not name the relaunch" ;;
esac
check "F: no row kept the unread dispatch" [ "$(row_count inflight-task-kept "task $ID ")" = 0 ]

# ── Coverage floor ─────────────────────────────────────────────────
echo
echo "=== results: $PASS passed, $FAIL failed (floor $EXPECTED_PASS) ==="
if [ "$PASS" -ne "$EXPECTED_PASS" ]; then
  echo "  FAIL  coverage floor: expected exactly $EXPECTED_PASS passes, got $PASS — a section was skipped or double-counted"
  exit 1
fi
[ "$FAIL" -eq 0 ] || exit 1
exit 0
