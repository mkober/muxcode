#!/usr/bin/env bash
# Integration test for the graph-agent orchestrator (MUX-014).
#
# Exercises the real pipeline end to end: a scratch bus session, a real
# `muxcode watch` daemon executing graph runs, and fake agents answering
# graph-originated sends over the real bus (consume inbox, reply with
# --reply-to). Covers: validation (uncapped cycle, ungated commit, builtin
# templates), async run start, a linear send→condition→send run with
# lifecycle events, the single-completion-wake guarantee, fan-out/join
# barrier semantics, daemon kill/restart resume, and retry --from.
#
# DEVIATION from the spec checklist: the fan-out/join graph uses two send
# nodes rather than spawn workers — StartSpawn launches real agent tmux
# windows (and an AI CLI), which a hermetic script must not do. The spawn
# and map dispatch paths are unit-tested with a fake dispatcher in
# bus/graph_exec_test.go; the join barrier under test here is identical
# for both node types.
#
# ISOLATION: scratch BUS_SESSION under /tmp, lifecycle log pinned to a
# temp dir, empty MUXCODE_CONFIG, disk-pressure cleanup disabled. No live
# muxcode session is needed.
#
# REQUIRES: the installed muxcode binary must include MUX-014 (run
# ./build.sh first), and tmux must be available.
#
# Usage: bash scripts/test-graph-orchestrator.sh
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
export BUS_SESSION="graph-test-$$"
BD="/tmp/muxcode-bus-${BUS_SESSION}"
WORK="/tmp/graph-test-work-$$"
mkdir -p "$WORK"
export MUXCODE_LIFECYCLE_LOG_DIR="$WORK/lifecycle"
: > "$WORK/empty-config"
export MUXCODE_CONFIG="$WORK/empty-config"
export MUXCODE_TMP_CLEANUP_THRESHOLD=0
export MUXCODE_BRANCH_TIME_DISABLE=1
export MUXCODE_DEDUP_WINDOW=0
export MUXCODE_GRAPH_TEST_FLAG=yes

DPID=""
cleanup() {
  [ -n "$DPID" ] && kill "$DPID" 2>/dev/null
  tmux kill-session -t "$BUS_SESSION" 2>/dev/null
  rm -rf "$BD" "$WORK"
}
trap cleanup EXIT

LIFELOG="$MUXCODE_LIFECYCLE_LOG_DIR/${BUS_SESSION}.log"

# Bare session so daemon tmux paths have something to talk to; no agents run.
tmux new-session -d -s "$BUS_SESSION" -n edit -x 120 -y 30
"$MUX" init >/dev/null 2>&1

# answer_role <role> — consume the role's inbox like a real agent and reply
# to the newest request, sending to the target the "To reply" instruction
# names (exactly what a real agent would do). Returns 1 if no request was
# found or the reply send failed.
answer_role() {
  local role="$1" out rid target
  out="$(AGENT_ROLE="$role" "$MUX" inbox 2>/dev/null || true)"
  rid="$(printf '%s' "$out" | grep -o -- '--reply-to [A-Za-z0-9-]*' | tail -1 | awk '{print $2}')"
  target="$(printf '%s' "$out" | grep -o 'muxcode send [a-z-]*' | tail -1 | awk '{print $3}')"
  [ -z "$rid" ] && return 1
  AGENT_ROLE="$role" "$MUX" send "${target:-edit}" response "done" --type response --reply-to "$rid" >/dev/null 2>&1
}

# wait_for_request <role> — poll until the role's inbox holds a request.
wait_for_request() {
  local role="$1" i
  for i in $(seq 1 40); do
    if AGENT_ROLE="$role" "$MUX" inbox --peek 2>/dev/null | grep -q 'Type: request'; then
      return 0
    fi
    sleep 0.5
  done
  return 1
}

# Run state comes from the plain header line "Run <id>  [state]  ..." —
# the --json object nests node "state" fields before the run's in marshal
# order, so a first-match JSON grep would read a node instead.
run_state()  { "$MUX" graph status "$1" 2>/dev/null | sed $'s/\x1b\\[[0-9;]*[A-Za-z]//g' | head -1 | sed 's/.*\[\([a-z]*\)\].*/\1/'; }
node_state() { "$MUX" graph status "$1" 2>/dev/null | sed $'s/\x1b\\[[0-9;]*[A-Za-z]//g' | awk -v n="$2" '$1==n {print $2}'; }

# --- 1. Validation ---------------------------------------------------------
cat > "$WORK/uncapped-cycle.json" <<'EOF'
{"name": "cycle", "start": "a",
 "nodes": [
   {"id": "a", "type": "send", "role": "build", "action": "build", "message": "go"},
   {"id": "b", "type": "send", "role": "test", "action": "test", "message": "go"}],
 "edges": [
   {"from": "a", "to": "b"},
   {"from": "b", "to": "a", "outcome": "failure"}]}
EOF
if "$MUX" graph validate "$WORK/uncapped-cycle.json" >/dev/null 2>&1; then
  bad "validate accepted an uncapped cycle"
else
  ok "validate rejects an uncapped cycle"
fi

cat > "$WORK/ungated-commit.json" <<'EOF'
{"name": "ungated", "start": "a",
 "nodes": [
   {"id": "a", "type": "send", "role": "build", "action": "build", "message": "go"},
   {"id": "c", "type": "send", "role": "commit", "action": "commit", "message": "ship"}],
 "edges": [{"from": "a", "to": "c"}]}
EOF
if "$MUX" graph validate "$WORK/ungated-commit.json" >/dev/null 2>&1; then
  bad "validate accepted an ungated commit node"
else
  ok "validate rejects an ungated commit node"
fi

builtin_fail=0
for tpl in build-test-review req-code-pr story-lifecycle story-to-spec commit-pr-review-loop pr-local-review review-spec-docs deploy-verify; do
  "$MUX" graph validate "$tpl" >/dev/null 2>&1 || { builtin_fail=1; bad "builtin template $tpl failed validation"; }
done
[ "$builtin_fail" -eq 0 ] && ok "all 8 builtin templates validate"

# --- 2. Async start: graph run returns before any node executes ------------
# Daemon not started yet, so nothing can execute behind our back.
cat > "$WORK/linear.json" <<'EOF'
{"name": "linear", "start": "a",
 "nodes": [
   {"id": "a", "type": "send", "role": "build", "action": "build", "message": "step one: ${intent}"},
   {"id": "cond", "type": "condition", "conditions": {"env_set": "MUXCODE_GRAPH_TEST_FLAG"}},
   {"id": "b", "type": "send", "role": "test", "action": "test", "message": "step two"}],
 "edges": [
   {"from": "a", "to": "cond"},
   {"from": "cond", "to": "b"}]}
EOF
RUN_OUT="$("$MUX" graph run --file "$WORK/linear.json" integration probe 2>&1)"
RID="$(printf '%s' "$RUN_OUT" | grep -o 'Started run [^ ]*' | awk '{print $3}')"
if [ -n "$RID" ]; then
  ok "graph run returned immediately with run id $RID"
else
  bad "graph run produced no run id: $RUN_OUT"
fi
if [ "$(node_state "$RID" a)" = "ready" ] && [ -z "$(AGENT_ROLE=build "$MUX" inbox --peek 2>/dev/null | grep 'Type: request' || true)" ]; then
  ok "no node executed before the daemon tick (a=ready, build inbox empty)"
else
  bad "run executed before the daemon started"
fi

# --- 3. Linear run executes via the daemon ---------------------------------
"$MUX" watch "$BUS_SESSION" --poll 2 >"$WORK/daemon.log" 2>&1 &
DPID=$!
sleep 1
kill -0 "$DPID" 2>/dev/null && ok "scratch daemon running (pid $DPID)" \
  || bad "scratch daemon exited immediately: $(tail -3 "$WORK/daemon.log" 2>/dev/null)"

if wait_for_request build; then
  ok "node a dispatched a real bus request to build"
  msg="$(AGENT_ROLE=build "$MUX" inbox --peek 2>/dev/null | grep 'step one' | head -1)"
  case "$msg" in
    *"integration probe"*) ok "\${intent} interpolated into the node message" ;;
    *) bad "intent not interpolated: $msg" ;;
  esac
  answer_role build || bad "could not answer build request"
else
  bad "node a never dispatched"
fi

if wait_for_request test; then
  ok "condition passed and node b dispatched (env_set condition evaluated in daemon)"
  answer_role test || bad "could not answer test request"
else
  bad "node b never dispatched — condition or routing failed"
fi

done_ok=0
for i in $(seq 1 40); do
  [ "$(run_state "$RID")" = "complete" ] && done_ok=1 && break
  sleep 0.5
done
[ "$done_ok" -eq 1 ] && ok "linear run reached complete" || bad "linear run state: $(run_state "$RID")"

for n in a cond b; do
  st="$(node_state "$RID" "$n")"
  [ "$st" = "done" ] && ok "node $n done" || bad "node $n state $st, want done"
done

if [ -s "$LIFELOG" ]; then
  grep -q '"event":"graph-node-start"' "$LIFELOG" && ok "lifecycle records graph-node-start" \
    || bad "no graph-node-start lifecycle rows"
  grep -q '"event":"graph-run-complete"' "$LIFELOG" && ok "lifecycle records graph-run-complete" \
    || bad "no graph-run-complete lifecycle row"
else
  bad "lifecycle log missing"
fi

# --- 4. Single completion wake, zero per-node wakes ------------------------
EDIT_INBOX="$(AGENT_ROLE=edit "$MUX" inbox --peek 2>/dev/null || true)"
wakes="$(printf '%s' "$EDIT_INBOX" | grep -c 'Action: graph-complete' || true)"
[ "$wakes" -eq 1 ] && ok "edit received exactly one graph-complete wake" \
  || bad "edit received $wakes graph-complete wakes, want 1"
per_node="$(printf '%s' "$EDIT_INBOX" | grep -c 'graph-node' || true)"
[ "$per_node" -eq 0 ] && ok "zero per-node wakes reached edit" \
  || bad "$per_node per-node wakes reached edit"

# --- 5. Fan-out / join all: barrier holds until both branches complete -----
cat > "$WORK/fanout.json" <<'EOF'
{"name": "fanout", "start": "a",
 "nodes": [
   {"id": "a", "type": "send", "role": "build", "action": "build", "message": "root"},
   {"id": "w1", "type": "send", "role": "test", "action": "test", "message": "worker one"},
   {"id": "w2", "type": "send", "role": "review", "action": "review", "message": "worker two"},
   {"id": "j", "type": "join", "join": "all"},
   {"id": "z", "type": "send", "role": "deploy", "action": "deploy", "message": "after join"}],
 "edges": [
   {"from": "a", "to": "w1"},
   {"from": "a", "to": "w2"},
   {"from": "w1", "to": "j"},
   {"from": "w2", "to": "j"},
   {"from": "j", "to": "z"}]}
EOF
RID2="$("$MUX" graph run --file "$WORK/fanout.json" 2>&1 | grep -o 'Started run [^ ]*' | awk '{print $3}')"
[ -n "$RID2" ] && ok "fan-out run started: $RID2" || bad "fan-out run failed to start"

wait_for_request build && answer_role build || bad "fan-out root never dispatched"

# Both branches dispatch in parallel; answer only w1 and verify the barrier.
wait_for_request test || bad "worker w1 never dispatched"
wait_for_request review || bad "worker w2 never dispatched"
answer_role test || bad "could not answer w1"
sleep 5
jst="$(node_state "$RID2" j)"
if [ "$jst" = "pending" ]; then
  ok "join barrier held with one of two branches complete"
else
  bad "join state $jst with only one branch complete, want pending"
fi
zst="$(node_state "$RID2" z)"
[ "$zst" = "pending" ] && ok "post-join node z held back" || bad "z state $zst, want pending"

answer_role review || bad "could not answer w2"
if wait_for_request deploy; then
  ok "join released after both branches — z dispatched"
  answer_role deploy
else
  bad "z never dispatched after join"
fi

# --- 6. Daemon kill/restart mid-run resumes from persisted state -----------
RID3="$("$MUX" graph run --file "$WORK/linear.json" resume probe 2>&1 | grep -o 'Started run [^ ]*' | awk '{print $3}')"
wait_for_request build || bad "resume-test node a never dispatched"

kill "$DPID" 2>/dev/null; wait "$DPID" 2>/dev/null; DPID=""
ok "daemon killed mid-run"

# Answer while the daemon is down — the response persists in the store.
answer_role build || bad "could not answer build while daemon down"

"$MUX" watch "$BUS_SESSION" --poll 2 >>"$WORK/daemon.log" 2>&1 &
DPID=$!
sleep 1

if wait_for_request test; then
  ok "restarted daemon resumed the run from persisted state"
  answer_role test
else
  bad "run did not resume after daemon restart"
fi
resumed=0
for i in $(seq 1 40); do
  [ "$(run_state "$RID3")" = "complete" ] && resumed=1 && break
  sleep 0.5
done
[ "$resumed" -eq 1 ] && ok "resumed run reached complete" || bad "resumed run state: $(run_state "$RID3")"

# --- 7. retry --from re-executes only downstream ---------------------------
if "$MUX" graph retry "$RID3" --from cond >/dev/null 2>&1; then
  ok "retry --from cond accepted on a complete run"
else
  bad "retry --from cond refused"
fi
[ "$(node_state "$RID3" a)" = "done" ] && ok "upstream node a preserved by retry" \
  || bad "retry reset upstream node a: $(node_state "$RID3" a)"

if wait_for_request test; then
  ok "retry re-dispatched only downstream (b), not a"
  answer_role test
else
  bad "retry never re-dispatched b"
fi
retried=0
for i in $(seq 1 40); do
  [ "$(run_state "$RID3")" = "complete" ] && retried=1 && break
  sleep 0.5
done
[ "$retried" -eq 1 ] && ok "retried run reached complete again" || bad "retried run state: $(run_state "$RID3")"

# A retry must not have re-sent to build (a stayed done).
build_reqs="$(AGENT_ROLE=build "$MUX" inbox --peek 2>/dev/null | grep -c 'Type: request' || true)"
[ "$build_reqs" -eq 0 ] && ok "no re-dispatch to build after retry" \
  || bad "build received $build_reqs new requests after retry"

# --- Summary ---------------------------------------------------------------
echo ""
echo "  ${pass} passed, ${fail} failed"
[ "$fail" -eq 0 ] || exit 1
exit 0
