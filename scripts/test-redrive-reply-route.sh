#!/usr/bin/env bash
# Integration test for MUX-232 — a re-driven request carries what the agent
# needs to answer it, and the agent's reply closes the node.
#
# On 2026-11-17 the tibo session's commit agent consumed a graph dispatch with
# its own listener (a true ack receipt) and never acted on it. The stall
# watchdog re-drove it twice, but the re-drive was the wake notification: the
# payload cut at 80 runes, the sender named `daemon`, no message id and no reply
# command — the agent could do the work and not answer it. Two defect-loop runs
# failed at 600 s each, and `diagnose` read the state as healthy.
#
# Hermetic: private tmux server, scratch daemons, muxcode and
# scripts/fixtures/claude-stub built from THIS tree, agent definitions in a
# scratch install dir. The stub's real `muxcode inbox --poll --loop` listener
# consumes the dispatch and the stub never answers it; it logs every typed line
# (CLAUDE_STUB_TYPED_LOG) and, while CLAUDE_STUB_REPLY_FILE holds 1, runs the
# `To reply:` command a typed line carries, as written.
#
# Sections:
#   A  A one-node graph run dispatches a 300-character request to watch; the
#      stub's listener acks it and nothing answers. With replies held, the
#      first re-drive arrives carrying the whole payload, the id and a reply
#      command to edit, and `diagnose watch` reports consumed-unanswered naming
#      the task; with replies on, the next re-drive is answered by running its
#      command and the node succeeds inside its 300 s cap
#   B  control — a build whose re-drive is the old wake notification: the
#      re-drive arrives cut at 80 runes with no reply command, the stub has
#      nothing to run, and the node is still running on an in-flight task
# A coverage floor keeps a skipped section from reporting green.
# Requires go, tmux, jq. ~5 min: the stall watchdog checks every 30 s and
# re-drives on its second at-rest sighting.
set -uo pipefail
export MUXCODE_CONSUMER_GUARD=0

PASS=0
FAIL=0
EXPECTED_PASS=21
for t in tmux go jq; do
  command -v "$t" >/dev/null 2>&1 || { echo "SKIP: $t is required"; exit 2; }
done

REPO=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
WORK=$(mktemp -d /tmp/redrive-route-XXXXXX)
. "$REPO/scripts/lib/muxcode-bus-dir.sh"

unset AGENT_ROLE BUS_ROLE BUS_SESSION TMUX TMUX_PANE MUXCODE MUXCODE_CONFIG MUXCODE_BUS_BASE MUXCODE_BUS_DIR
export HOME="$WORK/home"
export XDG_STATE_HOME="$WORK/home/.local/state"
export TMUX_TMPDIR="$WORK/tmux"
export MUXCODE_LIFECYCLE_LOG_DIR="$WORK/lifecycle"
export MUXCODE_INSTALL_DIR="$WORK/install"
export BUS_MEMORY_DIR="$WORK/memory" BUS_GLOBAL_MEMORY_DIR="$WORK/global-memory"
export MUXCODE_AGENT_CLI=claude MUXCODE_WATCH_CLI=claude
export MUXCODE_AGENT_HEALTH_CHECK_SECS=3 MUXCODE_DEFINITION_CHECK_SECS=3 MUXCODE_AGENT_HEARTBEAT=86400
export MUXCODE_EDIT_AUTO_RESTART_DISABLE=1 MUXCODE_CONTROL_PANE_DISABLE=1 MUXCODE_FORCE_RESPOND_DISABLE=1
export MUXCODE_TMP_CLEANUP_THRESHOLD=0 MUXCODE_ACTIVE_WATCHDOG_SECS=0 MUXCODE_PROMPT_AGENT_DISABLE=1
export MUXCODE_BRANCH_TIME_DISABLE=1 MUXCODE_STUCK_RELOAD_DISABLE=1 MUXCODE_DEDUP_WINDOW=0
export MUXCODE_AGENTDEFS_WATCH_DISABLE=1
export MUXCODE_TASK_STALL_SECS=6 # a graph dispatch stalls at half: 3 s
mkdir -p "$WORK/bin" "$WORK/bin-pre" "$WORK/home" "$WORK/tmux" "$WORK/lifecycle" "$WORK/install/agents" "$WORK/project"

DPIDS=""
cleanup() {
  local p
  for p in $DPIDS; do kill "$p" 2>/dev/null; done
  tmux kill-server 2>/dev/null
  sleep 1
  rm -rf "$WORK" 2>/dev/null
  true
}
trap cleanup EXIT

ok()   { PASS=$((PASS + 1)); echo "  ok: $1"; }
fail() { FAIL=$((FAIL + 1)); echo "  FAIL: $1"; }
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
  echo "  --- state dump ($SESSION) ---"
  jq -r '"\(.ts) \(.event) \(.detail // "")"' "$LIFELOG" 2>/dev/null | tail -10 | sed 's/^/    /'
  echo "  [typed]"; tail -3 "$TYPED" 2>/dev/null | cut -c1-240 | sed 's/^/    /'
  echo "  --- end dump ---"
}

echo "=== MUX-232 a re-drive the agent can answer ==="

(cd "$REPO/tools/muxcode" && go build -buildvcs=false -o "$WORK/bin/muxcode" .) \
  || { echo "SKIP: go build muxcode failed"; exit 2; }
(cd "$REPO/scripts/fixtures/claude-stub" && go build -buildvcs=false -o "$WORK/bin/claude" .) \
  || { echo "SKIP: go build claude-stub failed"; exit 2; }
cp "$WORK/bin/claude" "$WORK/bin-pre/claude"
ok "scratch muxcode and claude-stub built from this tree"

mkdir -p "$WORK/pre"
cp -R "$REPO/tools/muxcode" "$WORK/pre/"
perl -0pi -e 's/(func redriveNotification\(session string, msgs \[\]Message\) \(text string, shown int\) \{\n).*?\n\}\n/$1\treturn "Re-drive (consumed but never completed): " + BuildCombinedNotification(msgs), len(msgs)\n}\n/s' \
  "$WORK/pre/muxcode/bus/deliver.go"
if grep -q 'return "Re-drive (consumed but never completed): " + BuildCombinedNotification(msgs), len(msgs)' "$WORK/pre/muxcode/bus/deliver.go" \
  && (cd "$WORK/pre/muxcode" && go build -buildvcs=false -o "$WORK/bin-pre/muxcode" .); then
  ok "pre-fix muxcode built: the re-drive is the wake notification again"
else
  fail "pre-fix build: the redriveNotification patch did not apply, or the copy failed to build"
fi

printf -- '---\ndescription: Watcher\n---\nWatch CI.\n' >"$WORK/install/agents/log-watcher.md"
PAYLOAD="Preflight check, read-only: git status --porcelain and git branch --show-current"
while [ "${#PAYLOAD}" -lt 300 ]; do PAYLOAD="$PAYLOAD and report the tree, branch and upstream"; done
PAYLOAD="${PAYLOAD:0:300}"
printf '{"name": "mux232-redrive", "start": "preflight", "nodes": [{"id": "preflight", "type": "send", "role": "watch", "action": "watch", "message": "%s", "timeout_secs": 300}], "edges": []}\n' \
  "$PAYLOAD" >"$WORK/graph.json"

# start_section NAME BINDIR — a private tmux session whose watch pane runs the
# claude-stub as a launched agent on BINDIR's muxcode, and a scratch daemon on
# the same binary. Sets SESSION, BUSDIR, LIFELOG, TYPED, REPLY, MUX_NOW.
start_section() {
  SESSION="redrive-$1-$$"
  MUX_NOW="$2/muxcode"
  BUSDIR="$(muxcode_bus_dir "$MUX_NOW" "$SESSION")" || exit 1
  LIFELOG="$MUXCODE_LIFECYCLE_LOG_DIR/$SESSION.log"
  TYPED="$WORK/typed-$1.log" REPLY="$WORK/reply-$1"
  : >"$TYPED"; echo 0 >"$REPLY"
  export BUS_SESSION="$SESSION"
  tmux new-session -d -s "$SESSION" -n watch -x 200 -y 50 -c "$WORK/project" "/bin/bash --noprofile --norc"
  tmux set-option -g default-command "/bin/bash --noprofile --norc"
  tmux split-window -h -t "$SESSION:watch" -c "$WORK/project"
  type_in "export AGENT_ROLE=watch BUS_ROLE=watch BUS_SESSION=$SESSION HOME=$HOME XDG_STATE_HOME=$XDG_STATE_HOME TMUX_TMPDIR=$TMUX_TMPDIR MUXCODE_LIFECYCLE_LOG_DIR=$MUXCODE_LIFECYCLE_LOG_DIR MUXCODE_INSTALL_DIR=$MUXCODE_INSTALL_DIR MUXCODE_AGENT_CLI=claude MUXCODE_WATCH_CLI=claude MUXCODE_CONSUMER_GUARD=0 CLAUDE_STUB_TYPED_LOG=$TYPED CLAUDE_STUB_REPLY_FILE=$REPLY PATH=$2:$PATH; PS1='\$ '; clear"
  sleep 1
  "$MUX_NOW" init >/dev/null 2>&1 || true
  (cd "$WORK/project" && exec "$MUX_NOW" watch "$SESSION" --poll 1 >>"$WORK/daemon-$1.log" 2>&1) &
  DPIDS="$DPIDS $!"
  sleep 1
  type_in "muxcode agent launch watch --reason user"
}
type_in()      { tmux send-keys -t "$SESSION:watch.1" -l -- "$1"; sleep 0.1; tmux send-keys -t "$SESSION:watch.1" Enter; }
stub_up()      { [ -n "$(cat "$BUSDIR/polling-watch.marker" 2>/dev/null)" ]; }
start_run()    { (cd "$WORK/project" && "$MUX_NOW" graph run --file "$WORK/graph.json" 2>&1) | grep -o 'Started run [^ ]*' | awk '{print $3}'; }
dispatch_id()  { jq -r --arg r "$RID" 'select(.type == "request" and .to == "watch" and .graph_run == $r) | .id' "$BUSDIR/log.jsonl" 2>/dev/null | tail -1; }
dispatched()   { [ -n "$(dispatch_id)" ]; }
receipted()    { jq -e '(.acked_at // 0) > 0' "$BUSDIR/delivery/$ID.status" >/dev/null 2>&1; }
redriven()     { grep -q 'Re-drive (consumed but never completed)' "$TYPED" 2>/dev/null; }
redrive_line() { grep 'Re-drive (consumed but never completed)' "$TYPED" 2>/dev/null | head -1; }
node_field()   { jq -r ".$1 // \"\"" "$BUSDIR/graphs/$RID/nodes/preflight.json" 2>/dev/null; }
node_success() { [ "$(node_field state)" = done ] && [ "$(node_field outcome)" = success ]; }
task_status()  { jq -r '.status' "$BUSDIR/tasks/$ID.json" 2>/dev/null; }

# ── A: this build ──────────────────────────────────────────────────
echo "-- A: the re-drive carries the payload, id and reply command"
start_section a "$WORK/bin"
wait_for 30 "A: watch agent (claude-stub) up with its inbox listener" stub_up
RID=$(start_run)
START=$(date +%s)
wait_for 20 "A: the run dispatched its 300-character request" dispatched
ID=$(dispatch_id)
wait_for 20 "A: the stub's listener consumed it with a receipt" receipted
wait_for 120 "A: the stall watchdog re-drove it" redriven
LINE=$(redrive_line)
case $LINE in *"$PAYLOAD"*) ok "A: the re-drive carries the whole 300-character payload" ;; *) fail "A: payload missing: ${LINE:0:240}" ;; esac
case $LINE in *"[$ID]"*) ok "A: the re-drive names the message id" ;; *) fail "A: id $ID missing" ;; esac
case $LINE in *"To reply: muxcode send edit response"*"--reply-to $ID"*) ok "A: the reply command addresses edit with the id" ;;
  *) fail "A: no reply command to edit: ${LINE:0:240}" ;; esac
case $LINE in *"muxcode send daemon"*) fail "A: the reply command addresses daemon" ;; *) ok "A: no reply command addresses daemon" ;; esac
"$MUX_NOW" diagnose watch --json >"$WORK/diagnose-a.json" 2>/dev/null
jq -e --arg id "$ID" '.findings[] | select(.failure_mode == "consumed-unanswered" and (.summary | contains($id)))' "$WORK/diagnose-a.json" >/dev/null 2>&1 \
  && ok "A: diagnose reports consumed-unanswered naming the task, before any reply" \
  || fail "A: diagnose findings: $(jq -c '[.findings[].failure_mode]' "$WORK/diagnose-a.json" 2>/dev/null)"
check "A: the node is still running while replies are held" [ "$(node_field state)" = running ]
echo 1 >"$REPLY"
wait_for 150 "A: the next re-drive's reply command, run as written, succeeds the node" node_success
ELAPSED=$(( $(date +%s) - START ))
check "A: inside its 300 s cap (${ELAPSED}s)" [ "$ELAPSED" -lt 300 ]
case $(node_field output) in *"Re-drive answered"*) ok "A: the node's output is the stub's reply" ;; *) fail "A: node output: $(node_field output)" ;; esac

# ── B: control — the old re-drive text ─────────────────────────────
echo "-- B: control — the re-drive as the wake notification"
start_section b "$WORK/bin-pre"
echo 1 >"$REPLY"
wait_for 30 "B: watch agent up" stub_up
RID=$(start_run)
wait_for 20 "B: dispatched" dispatched
ID=$(dispatch_id)
wait_for 20 "B: consumed with a receipt" receipted
wait_for 120 "B: re-driven" redriven
LINE=$(redrive_line)
case $LINE in *"To reply:"* | *"$PAYLOAD"*) fail "B: the old re-drive carried a reply route or the whole payload" ;;
  *) ok "B: the old re-drive is cut short with no reply command — nothing for the stub to run" ;; esac
sleep 10
still_waiting() { [ "$(node_field state)" = running ] && [ "$(task_status)" = in-flight ]; }
check "B: the node is still running on an in-flight task — left to its cap" still_waiting

# ── Coverage floor ─────────────────────────────────────────────────
echo
echo "=== results: $PASS passed, $FAIL failed (floor $EXPECTED_PASS) ==="
if [ "$PASS" -ne "$EXPECTED_PASS" ]; then
  echo "  FAIL  coverage floor: expected exactly $EXPECTED_PASS passes, got $PASS — a section was skipped or double-counted"
  exit 1
fi
[ "$FAIL" -eq 0 ] || exit 1
exit 0
