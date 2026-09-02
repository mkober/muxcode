#!/usr/bin/env bash
# Integration test for the graph-agent orchestrator (MUX-014).
#
# Exercises the real pipeline end to end: a scratch bus session, a real
# `muxcode watch` daemon executing graph runs, and fake agents answering
# graph-originated sends over the real bus (consume inbox, reply with
# --reply-to). Covers: validation (uncapped cycle, ungated commit, builtin
# templates), async run start, a linear send→condition→send run with
# lifecycle events, the single-completion-wake guarantee, fan-out/join
# barrier semantics, daemon kill/restart resume, retry --from, and the
# MUX-132 stale-approval re-arm: a retry below a satisfied wait_human
# gate resumes AT the gate with the old approval purged and a fresh
# graph-approval demanded, while an ungated retry never prompts. A
# coverage floor at the end keeps a skipped section from reporting green.
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

# answer_role_with <role> <text> — answer like answer_role but control the
# reply body, so a downstream output_contains condition can be steered
# per iteration (MUX-133 section 10).
answer_role_with() {
  local role="$1" text="$2" out rid target
  out="$(AGENT_ROLE="$role" "$MUX" inbox 2>/dev/null || true)"
  rid="$(printf '%s' "$out" | grep -o -- '--reply-to [A-Za-z0-9-]*' | tail -1 | awk '{print $2}')"
  target="$(printf '%s' "$out" | grep -o 'muxcode send [a-z-]*' | tail -1 | awk '{print $3}')"
  [ -z "$rid" ] && return 1
  AGENT_ROLE="$role" "$MUX" send "${target:-edit}" response "$text" --type response --reply-to "$rid" >/dev/null 2>&1
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

# fail_role <role> — consume like answer_role but reply with action
# "error", which deriveSendOutcome maps to a failure outcome, failing
# the node.
fail_role() {
  local role="$1" out rid target
  out="$(AGENT_ROLE="$role" "$MUX" inbox 2>/dev/null || true)"
  rid="$(printf '%s' "$out" | grep -o -- '--reply-to [A-Za-z0-9-]*' | tail -1 | awk '{print $2}')"
  target="$(printf '%s' "$out" | grep -o 'muxcode send [a-z-]*' | tail -1 | awk '{print $3}')"
  [ -z "$rid" ] && return 1
  AGENT_ROLE="$role" "$MUX" send "${target:-edit}" error "step failed" --type response --reply-to "$rid" >/dev/null 2>&1
}

# Run state comes from the plain header line "Run <id>  [state]  ..." —
# the --json object nests node "state" fields before the run's in marshal
# order, so a first-match JSON grep would read a node instead.
run_state()  { "$MUX" graph status "$1" 2>/dev/null | sed $'s/\x1b\\[[0-9;]*[A-Za-z]//g' | head -1 | sed 's/.*\[\([a-z]*\)\].*/\1/'; }
node_state() { "$MUX" graph status "$1" 2>/dev/null | sed $'s/\x1b\\[[0-9;]*[A-Za-z]//g' | awk -v n="$2" '$1==n {print $2}'; }

# wait_node_state <run> <node> <state> / wait_run_state <run> <state> —
# poll the run store up to 20s.
wait_node_state() {
  local i
  for i in $(seq 1 40); do
    [ "$(node_state "$1" "$2")" = "$3" ] && return 0
    sleep 0.5
  done
  return 1
}
wait_run_state() {
  local i
  for i in $(seq 1 40); do
    [ "$(run_state "$1")" = "$2" ] && return 0
    sleep 0.5
  done
  return 1
}

# approval_count — graph-approval requests sitting in edit's inbox.
approval_count() { AGENT_ROLE=edit "$MUX" inbox --peek 2>/dev/null | grep -c 'Action: graph-approval' || true; }

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
for tpl in build-test-review spec-to-pr story-to-spec commit-pr-review-loop pr-local-review update-spec-docs deploy-verify; do
  "$MUX" graph validate "$tpl" >/dev/null 2>&1 || { builtin_fail=1; bad "builtin template $tpl failed validation"; }
done
[ "$builtin_fail" -eq 0 ] && ok "all 7 builtin templates validate"

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
if retry7_out="$("$MUX" graph retry "$RID3" --from cond 2>&1)"; then
  ok "retry --from cond accepted on a complete run"
else
  bad "retry --from cond refused: $retry7_out"
fi
# Ungated negative control (MUX-132): the linear graph has no gate, so
# the retry must resume at the requested node with no re-arm note.
case "$retry7_out" in
  *re-armed*) bad "ungated retry re-targeted: $retry7_out" ;;
  *"retrying from cond"*) ok "ungated retry resumed at the requested node" ;;
  *) bad "ungated retry output unexpected: $retry7_out" ;;
esac
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

# No graph ran so far has a gate — nothing may have prompted edit.
[ "$(approval_count)" -eq 0 ] && ok "no graph-approval demanded anywhere on the ungated path" \
  || bad "graph-approval reached edit during ungated runs: $(approval_count)"

# --- 8. Gated retry re-arms the gate, never consumes a stale approval ------
# (MUX-132) The 2026-08-31 incident shape: approve the gate, fail the
# node behind it, change the tree, retry --from the failed node. The
# retry must resume AT the gate with the old approval purged and demand
# a fresh one — never fire the gated node on an approval granted for
# different content.
cat > "$WORK/gated.json" <<'EOF'
{"name": "gated", "start": "a",
 "nodes": [
   {"id": "a", "type": "send", "role": "build", "action": "build", "message": "pre-gate work"},
   {"id": "gate", "type": "wait_human", "message": "approve the gated step"},
   {"id": "c", "type": "send", "role": "test", "action": "test", "message": "gated step"}],
 "edges": [
   {"from": "a", "to": "gate"},
   {"from": "gate", "to": "c"}]}
EOF
RID4="$("$MUX" graph run --file "$WORK/gated.json" 2>&1 | grep -o 'Started run [^ ]*' | awk '{print $3}')"
[ -n "$RID4" ] && ok "gated run started: $RID4" || bad "gated run failed to start"

wait_for_request build && answer_role build || bad "gated run node a never dispatched"
wait_node_state "$RID4" gate waiting && ok "gate reached waiting" \
  || bad "gate state $(node_state "$RID4" gate), want waiting"
[ "$(approval_count)" -eq 1 ] && ok "edit received the first graph-approval request" \
  || bad "graph-approval count $(approval_count), want 1"

# Consume edit's inbox as the real edit agent does when acting on the
# gate. Left pending, the first request would make the bus's duplicate
# guard (HasPendingInboxRequest) suppress the re-armed gate's identical
# second request — and it also makes the later count a proof that a
# FRESH request arrived, not a stale leftover.
AGENT_ROLE=edit "$MUX" inbox >/dev/null 2>&1

"$MUX" graph approve "$RID4" gate >/dev/null 2>&1 || bad "graph approve failed"
wait_for_request test && ok "approval released the gate — c dispatched" \
  || bad "c never dispatched after approval"
fail_role test || bad "could not fail c"
wait_run_state "$RID4" failed && ok "run failed at c behind the satisfied gate" \
  || bad "run state $(run_state "$RID4"), want failed"

# The tree changes between approval and retry — the incident's essence.
# The re-arm decision is content-independent (the executor never reads
# the tree); the step keeps the scenario honest to the incident shape.
echo "post-approval change" >> "$WORK/tree-change"

APPROVED_MARKER="$BD/graphs/$RID4/approvals/gate.approved"
[ -e "$APPROVED_MARKER" ] || bad "precondition: approved marker missing before retry"
retry8_out="$("$MUX" graph retry "$RID4" --from c 2>&1)" || bad "gated retry refused: $retry8_out"
case "$retry8_out" in
  *'satisfied human gate "gate"'*'re-armed'*) ok "retry announced the re-arm, naming the gate" ;;
  *) bad "retry output missing the re-arm note: $retry8_out" ;;
esac
case "$retry8_out" in
  *'(approved '[0-9]*) ok "retry names the original approval time" ;;
  *) bad "retry output missing the approval time: $retry8_out" ;;
esac
case "$retry8_out" in
  *"retrying from gate"*) ok "run visibly resumes at the gate, not the requested node" ;;
  *) bad "retry did not re-target to the gate: $retry8_out" ;;
esac
[ ! -e "$APPROVED_MARKER" ] && ok "stale approved marker purged by retry" \
  || bad "approved marker survived the retry"

wait_node_state "$RID4" gate waiting && ok "re-armed gate dispatched back to waiting" \
  || bad "gate state $(node_state "$RID4" gate) after retry, want waiting"

# Give a broken implementation every chance to mis-fire c on the stale
# approval before asserting it did not (two-plus daemon ticks).
sleep 5
if AGENT_ROLE=test "$MUX" inbox --peek 2>/dev/null | grep -q 'Type: request'; then
  bad "c fired without fresh approval — stale approval consumed"
else
  ok "c held back until fresh approval"
fi
# The inbox was drained above, so exactly one graph-approval here is a
# fresh post-retry request — the re-armed gate asked again.
[ "$(approval_count)" -eq 1 ] && ok "edit received a second, fresh graph-approval request" \
  || bad "fresh graph-approval count $(approval_count) after re-arm, want 1"

"$MUX" graph status "$RID4" 2>/dev/null | sed $'s/\x1b\\[[0-9;]*[A-Za-z]//g' | grep -q 'Retry: .*re-armed at gate' \
  && ok "graph status shows the re-target decision" \
  || bad "graph status missing the retry note"
grep -q '"event":"graph-retry-regated"' "$LIFELOG" \
  && ok "lifecycle records graph-retry-regated" \
  || bad "no graph-retry-regated lifecycle row"

"$MUX" graph approve "$RID4" gate >/dev/null 2>&1 || bad "second graph approve failed"
if wait_for_request test; then
  ok "fresh approval releases the gate — c re-dispatched"
  answer_role test
else
  bad "c never dispatched after fresh approval"
fi
wait_run_state "$RID4" complete && ok "gated run completed after fresh approval" \
  || bad "gated run state $(run_state "$RID4"), want complete"

# --- 9. Condition false branch renders as a branch, not a failure (MUX-133) -
# One frame must carry both readings at once: a condition that chose its
# false edge, and a node that genuinely failed. Before option B they were
# the same red "failed" row, so an operator scanning for trouble could
# not tell control flow from breakage.
cat > "$WORK/branchview.json" <<'EOF'
{"name": "branchview", "start": "a",
 "nodes": [
   {"id": "a", "type": "send", "role": "build", "action": "build", "message": "go"},
   {"id": "cond", "type": "condition", "conditions": {"env_set": "MUXCODE_GRAPH_TEST_ABSENT"}},
   {"id": "yes", "type": "send", "role": "deploy", "action": "deploy", "message": "true branch"},
   {"id": "no", "type": "send", "role": "test", "action": "test", "message": "false branch"},
   {"id": "recover", "type": "send", "role": "run", "action": "run", "message": "recover"}],
 "edges": [
   {"from": "a", "to": "cond"},
   {"from": "cond", "to": "yes", "outcome": "success"},
   {"from": "cond", "to": "no", "outcome": "failure"},
   {"from": "no", "to": "recover", "outcome": "failure"}]}
EOF
RUN5="$("$MUX" graph run --file "$WORK/branchview.json" branch view 2>&1)"
RID5="$(printf '%s' "$RUN5" | grep -o 'Started run [^ ]*' | awk '{print $3}')"
[ -n "$RID5" ] || bad "branchview run produced no id: $RUN5"

wait_for_request build && answer_role build || bad "branchview: node a never dispatched"

# The false edge must still route — the routing key is unchanged by the split.
if wait_for_request test; then
  ok "condition false edge still routes after the state/outcome split"
  fail_role test || bad "could not fail node no"
else
  bad "false edge never fired — the split broke condition routing"
fi

# The true branch must not have fired.
if [ "$(node_state "$RID5" yes)" = "pending" ]; then
  ok "true branch never dispatched on a false predicate"
else
  bad "node yes state $(node_state "$RID5" yes), want pending"
fi

wait_for_request run && answer_role run || bad "branchview: recover never dispatched"
wait_run_state "$RID5" complete \
  && ok "run completes with a branched condition and a recovered failure" \
  || bad "branchview run state $(run_state "$RID5"), want complete"

# --- The single frame carrying both readings ---
FRAME="$("$MUX" graph status "$RID5" 2>/dev/null | sed $'s/\x1b\\[[0-9;]*[A-Za-z]//g')"
COND_WORD="$(printf '%s' "$FRAME" | awk '$1=="cond" {print $2}')"
NO_WORD="$(printf '%s' "$FRAME" | awk '$1=="no" {print $2}')"

[ "$COND_WORD" = "branched" ] \
  && ok "condition false branch renders as 'branched'" \
  || bad "condition rendered as '$COND_WORD', want 'branched'"
[ "$COND_WORD" != "failed" ] \
  && ok "condition false branch is not rendered as a failure" \
  || bad "condition still renders as failed — the MUX-133 defect"
[ "$NO_WORD" = "failed" ] \
  && ok "a genuinely failed node still renders 'failed' in the same frame" \
  || bad "failed node rendered as '$NO_WORD', want 'failed' — the fix must not make failures unreadable"

# --- The run store must agree with the frame (the point of option B) ---
COND_JSON="$BD/graphs/$RID5/nodes/cond.json"
if [ -f "$COND_JSON" ]; then
  grep -q '"state"[ ]*:[ ]*"done"' "$COND_JSON" \
    && ok "run store persists the branched condition as done, not failed" \
    || bad "run store still persists state=failed: $(cat "$COND_JSON")"
  grep -q '"outcome"[ ]*:[ ]*"failure"' "$COND_JSON" \
    && ok "run store retains outcome=failure — the routing key is intact" \
    || bad "run store lost outcome=failure — capped loops would stop terminating"
else
  bad "run store node file missing: $COND_JSON"
fi

# --- The JSON surface must agree too ---
JSON_OUT="$("$MUX" graph status "$RID5" --json 2>/dev/null)"
printf '%s' "$JSON_OUT" | grep -q '"branched"[ ]*:[ ]*true' \
  && ok "graph status --json marks the condition branched" \
  || bad "--json missing branched:true — machine consumers still read a bare failure"

# --- 10. Capped-loop terminating condition renders as a branch (MUX-133) ---
# The loop-check shape from spec-to-pr: a condition whose TRUE edge loops
# back and whose FALSE edge ends the loop. The false branch is how a
# capped loop is supposed to finish, so rendering it red made every
# normal termination look like a break. Driven through a real iteration
# so the check is about the exiting pass, not a loop that never ran.
cat > "$WORK/loopbranch.json" <<'EOF'
{"name": "loopbranch", "start": "work",
 "nodes": [
   {"id": "work", "type": "send", "role": "build", "action": "build", "message": "iterate"},
   {"id": "again", "type": "condition", "conditions": {"output_contains": "AGAIN"}},
   {"id": "finish", "type": "send", "role": "test", "action": "test", "message": "wrap up"}],
 "edges": [
   {"from": "work", "to": "again"},
   {"from": "again", "to": "work", "outcome": "success", "max_iterations": 3},
   {"from": "again", "to": "finish", "outcome": "failure"}]}
EOF
RUN6="$("$MUX" graph run --file "$WORK/loopbranch.json" loop branch 2>&1)"
RID6="$(printf '%s' "$RUN6" | grep -o 'Started run [^ ]*' | awk '{print $3}')"
[ -n "$RID6" ] || bad "loopbranch run produced no id: $RUN6"

# Iteration 1: reply AGAIN so the condition takes its TRUE edge and loops.
wait_for_request build && answer_role_with build "AGAIN please" \
  || bad "loopbranch: work never dispatched on iteration 1"

# Iteration 2: work must be re-dispatched by the loop edge.
if wait_for_request build; then
  ok "capped loop iterated — the true edge re-armed the loop body"
  answer_role_with build "STOP now" || bad "could not answer iteration 2"
else
  bad "loop never iterated — the condition's true edge did not re-arm work"
fi

# Now the condition goes false and must END the loop via its false edge.
wait_for_request test && answer_role test \
  || bad "loop never terminated — the false edge did not route to finish"
wait_run_state "$RID6" complete \
  && ok "capped loop terminated via its false edge and the run completed" \
  || bad "loopbranch run state $(run_state "$RID6"), want complete"

LOOP_WORD="$("$MUX" graph status "$RID6" 2>/dev/null | sed $'s/\x1b\\[[0-9;]*[A-Za-z]//g' | awk '$1=="again" {print $2}')"
[ "$LOOP_WORD" = "branched" ] \
  && ok "loop-terminating condition renders 'branched' on the exiting pass" \
  || bad "loop-terminating condition rendered '$LOOP_WORD', want 'branched' — a normal loop exit must not read as a break"

AGAIN_JSON="$BD/graphs/$RID6/nodes/again.json"
if [ -f "$AGAIN_JSON" ]; then
  grep -q '"state"[ ]*:[ ]*"done"' "$AGAIN_JSON" \
    && ok "loop condition persists state=done after terminating the loop" \
    || bad "loop condition persisted as failed: $(cat "$AGAIN_JSON")"
else
  bad "run store node file missing: $AGAIN_JSON"
fi

# --- Coverage floor --------------------------------------------------------
# A full run produces exactly this many passing checks. Fewer means a
# section was skipped or short-circuited — a partial run must not report
# green (MUX-132 Phase 4).
FLOOR=60
[ "$pass" -ge "$FLOOR" ] \
  || bad "coverage floor: $pass checks passed, floor $FLOOR — a skipped section cannot report green"

# --- Summary ---------------------------------------------------------------
echo ""
echo "  ${pass} passed, ${fail} failed"
[ "$fail" -eq 0 ] || exit 1
exit 0
