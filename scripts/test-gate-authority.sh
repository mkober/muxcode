#!/usr/bin/env bash
# Integration test for wait_human gate authority and the runtime commit
# backstop (MUX-144, Phases 2-4).
#
# Exercises the whole control end to end on a real daemon: a scratch bus
# session, a real `muxcode watch` daemon executing gated graphs, and a scratch
# repo dir. The unit suite proves the predicates; this proves the ROAD — that
# an approval written by the CLI survives to the dispatch the backstop reads,
# which no unit test can establish.
#
# Covers:
#   1. validateGates still rejects an ungated commit graph (negative control),
#      and accepts the gated one (positive control).
#   2. An unauthorized role's `graph approve` is refused, the gate stays
#      waiting, and the commit node never dispatches.
#   3. Self-approval by the run's creator is refused, even though that same
#      identity is in the authority list.
#   4. An authorized approval by a non-creator releases the gate, the commit
#      node dispatches, and the run proceeds.
#   5. A commit dispatch with no valid human approval is refused at the
#      backstop — driven through `retry --from <commit-node>` past a gate
#      nobody ever approved, which is the one live path that reaches a commit
#      dispatch without an approval (MUX-144 Phase 4).
#   6. graph-run-created and graph-gate-approved both appear naming identities.
#
# ISOLATION: scratch BUS_SESSION under /tmp, scratch HOME, scratch repo dir,
# lifecycle log in a temp dir, empty muxcode config.
#
# The scratch HOME is what isolates the gate authority, and nothing else can:
# GateAuthorityConfigured walks a FIXED path list and never consults
# $MUXCODE_CONFIG (31a2ca4, MUX-144 Phase 2 — an agent could otherwise
# self-authorize by pointing the process at a file it wrote). So the
# MUXCODE_CONFIG export below is inert for gate authority by design; removing
# the scratch HOME would silently fall through to the developer's own
# ~/.config/muxcode/config and make this script pass or fail on their machine's
# settings. The daemon also seals the list at startup, so it must start AFTER
# the config is written.
#
# REQUIRES: installed muxcode >= v0.1.0, and tmux. Run ./build.sh first — this
# tests the INSTALLED binary, so an unbuilt tree tests the previous fix.
#
# Usage: bash scripts/test-gate-authority.sh
#        MUXCODE_TEST_KEEP=1 bash scripts/test-gate-authority.sh   # keep WORK
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
. "$(dirname "${BASH_SOURCE[0]}")/lib/muxcode-version.sh"
require_muxcode_version "$MUX" v0.1.0 MUX-144 || { echo "  FAIL  binary precondition not met"; exit 1; }

GREEN=$'\033[0;32m'; RED=$'\033[0;31m'; NC=$'\033[0m'
pass=0; fail=0
ok()  { echo "  ${GREEN}PASS${NC}  $*"; pass=$((pass + 1)); }
bad() { echo "  ${RED}FAIL${NC}  $*"; fail=$((fail + 1)); }

# Three identities, deliberately distinct:
#   test-approver — the stand-in for the human at the CLI, the only role in the
#                   authority list, under a name no real agent holds.
#   auto          — creates the runs, so approval by test-approver is never a
#                   self-approval.
#   build         — an ordinary agent, in no list: the unauthorized case.
as_approver() { AGENT_ROLE=test-approver "$MUX" "$@"; }
as_creator()  { AGENT_ROLE=auto "$MUX" "$@"; }

# --- Isolation -------------------------------------------------------------
export BUS_SESSION="gate-authority-test-$$"
BD="/tmp/muxcode-bus-${BUS_SESSION}"
WORK="/tmp/gate-authority-work-$$"
REPO="$WORK/repo"
mkdir -p "$REPO"

# Run from the scratch dir, not the checkout. defaultGateAuthorityConfigPaths
# consults the CWD-relative ".muxcode/config" BEFORE the one under HOME
# (gate_authority.go), so a developer whose repo-local config happens to set
# MUXCODE_GATE_AUTHORITY_ROLES would shadow the scratch HOME and run sections
# 2-4 against their own value. This script passes in this checkout only because
# our repo file omits that key — isolation that rests on a file the test does
# not control is not isolation. The daemon seals from its own CWD too, so this
# must precede its launch.
cd "$WORK" || { echo "  FAIL  cannot cd to scratch dir $WORK"; exit 1; }

export HOME="$WORK/home"
mkdir -p "$HOME/.config/muxcode"
echo "MUXCODE_GATE_AUTHORITY_ROLES=test-approver" > "$HOME/.config/muxcode/config"
export MUXCODE_LIFECYCLE_LOG_DIR="$WORK/lifecycle"
: > "$WORK/empty-config"
export MUXCODE_CONFIG="$WORK/empty-config"
export MUXCODE_TMP_CLEANUP_THRESHOLD=0
export MUXCODE_BRANCH_TIME_DISABLE=1
export MUXCODE_DEDUP_WINDOW=0
export MUXCODE_SESSION_REPO_DIR="$REPO"

DPID=""
cleanup() {
  [ -n "$DPID" ] && kill "$DPID" 2>/dev/null
  tmux kill-session -t "$BUS_SESSION" 2>/dev/null
  cd / 2>/dev/null # we run from $WORK; do not rm the directory we are standing in
  if [ "${MUXCODE_TEST_KEEP:-0}" = "1" ]; then
    echo "  (kept for diagnosis: $WORK)"
    rm -rf "$BD"
  else
    rm -rf "$BD" "$WORK"
  fi
}
trap cleanup EXIT

LIFELOG="$MUXCODE_LIFECYCLE_LOG_DIR/${BUS_SESSION}.log"

tmux new-session -d -s "$BUS_SESSION" -n edit -x 120 -y 30
"$MUX" init >/dev/null 2>&1

run_state()  { "$MUX" graph status "$1" 2>/dev/null | sed $'s/\x1b\\[[0-9;]*[A-Za-z]//g' | head -1 | sed 's/.*\[\([a-z]*\)\].*/\1/'; }
node_state() { "$MUX" graph status "$1" 2>/dev/null | sed $'s/\x1b\\[[0-9;]*[A-Za-z]//g' | awk -v n="$2" '$1==n {print $2}'; }

wait_node_state() {
  local rid="$1" node="$2" want="$3" i
  for i in $(seq 1 40); do
    [ "$(node_state "$rid" "$node")" = "$want" ] && return 0
    sleep 0.5
  done
  return 1
}

wait_run_state() {
  local rid="$1" want="$2" i
  for i in $(seq 1 40); do
    [ "$(run_state "$rid")" = "$want" ] && return 0
    sleep 0.5
  done
  return 1
}

commit_requests() { AGENT_ROLE=commit "$MUX" inbox --peek 2>/dev/null | grep -c 'Type: request' || true; }

wait_lifecycle() {
  local pat="$1" i
  for i in $(seq 1 40); do
    grep -q "\"event\":\"$pat\"" "$LIFELOG" 2>/dev/null && return 0
    sleep 0.5
  done
  return 1
}

start_run() {
  as_creator graph run --file "$WORK/gated.json" 2>&1 | grep -o 'Started run [^ ]*' | awk '{print $3}'
}

# answer_commit drains and answers the commit node's dispatch so the run can
# finish. Draining also matters between sections: a message left in the inbox
# would make the next section's "inbox empty" assertion pass or fail on the
# wrong message.
answer_commit() {
  local out rid
  out="$(AGENT_ROLE=commit "$MUX" inbox 2>/dev/null || true)"
  rid="$(printf '%s' "$out" | grep -o -- '--reply-to [A-Za-z0-9-]*' | tail -1 | awk '{print $2}')"
  [ -z "$rid" ] && return 1
  AGENT_ROLE=commit "$MUX" send edit response "committed" --type response --reply-to "$rid" >/dev/null 2>&1
}

# --- Fixtures --------------------------------------------------------------
COMMIT_MSG="Stage and commit the gate-authority fixture"

# The gate is the start node, as in test-close-spec-guard.sh. A send node ahead
# of it would dispatch to a role with no agent behind it in a hermetic session,
# leaving the run parked before the gate and every assertion here cascading off
# a graph that never reached the behaviour under test.
cat > "$WORK/gated.json" <<EOF
{"name": "gated-commit", "start": "gate",
 "nodes": [
   {"id": "gate", "type": "wait_human", "message": "Approve the commit"},
   {"id": "ship", "type": "send", "role": "commit", "action": "commit", "message": "$COMMIT_MSG"}],
 "edges": [
   {"from": "gate", "to": "ship"}]}
EOF

cat > "$WORK/ungated.json" <<EOF
{"name": "ungated-commit", "start": "ship",
 "nodes": [
   {"id": "ship", "type": "send", "role": "commit", "action": "commit", "message": "$COMMIT_MSG"}],
 "edges": []}
EOF

# --- 1. validateGates: the structural half must not regress ----------------
if "$MUX" graph validate "$WORK/ungated.json" >"$WORK/ungated.out" 2>&1; then
  bad "an ungated commit graph validated — the structural gate rule has regressed"
else
  ok "ungated commit graph rejected by validateGates"
fi
grep -q 'wait_human' "$WORK/ungated.out" 2>/dev/null \
  && ok "the rejection names the missing wait_human gate" \
  || bad "rejection did not mention wait_human: $(head -2 "$WORK/ungated.out" 2>/dev/null)"

"$MUX" graph validate "$WORK/gated.json" >/dev/null 2>&1 \
  && ok "gated commit graph validates (positive control — the rule is not refusing everything)" \
  || bad "the gated graph failed validation"

# --- Daemon ----------------------------------------------------------------
"$MUX" watch "$BUS_SESSION" --poll 2 >"$WORK/daemon.log" 2>&1 &
DPID=$!
sleep 1
kill -0 "$DPID" 2>/dev/null && ok "scratch daemon running (pid $DPID)" \
  || bad "scratch daemon exited immediately: $(tail -3 "$WORK/daemon.log" 2>/dev/null)"

# --- 2. An unauthorized role may not open a gate ---------------------------
RID="$(start_run)"
[ -n "$RID" ] && ok "run started: $RID" || bad "run failed to start"

wait_node_state "$RID" gate waiting \
  && ok "gate reached waiting" \
  || bad "gate never reached waiting: $(node_state "$RID" gate)"

if AGENT_ROLE=build "$MUX" graph approve "$RID" gate >"$WORK/unauth.out" 2>&1; then
  bad "an unauthorized role (build) opened the gate"
else
  ok "unauthorized role refused at graph approve"
fi

[ "$(node_state "$RID" gate)" = "waiting" ] \
  && ok "gate still waiting after the refused approval" \
  || bad "gate state $(node_state "$RID" gate) after a refused approval, want waiting"

[ "$(commit_requests)" -eq 0 ] \
  && ok "commit node never dispatched behind the refused approval" \
  || bad "commit received a request despite the gate being refused"

grep -q '"event":"graph-gate-approval-refused"' "$LIFELOG" 2>/dev/null \
  && ok "lifecycle records graph-gate-approval-refused" \
  || bad "no graph-gate-approval-refused lifecycle row"

grep -q 'build' <(grep '"event":"graph-gate-approval-refused"' "$LIFELOG" 2>/dev/null) \
  && ok "the refusal names the actor that was refused" \
  || bad "the refusal row does not name the refused actor"

# --- 3. The run's creator may not approve its own gate ---------------------
SELF_RID="$(as_approver graph run --file "$WORK/gated.json" 2>&1 | grep -o 'Started run [^ ]*' | awk '{print $3}')"
[ -n "$SELF_RID" ] && ok "self-approval run started by test-approver: $SELF_RID" \
  || bad "self-approval run failed to start"

wait_node_state "$SELF_RID" gate waiting \
  && ok "self-approval run reached its gate" \
  || bad "self-approval run never reached its gate"

if as_approver graph approve "$SELF_RID" gate >/dev/null 2>&1; then
  bad "the run's creator approved its own gate — the self-approval rule is not holding"
else
  ok "self-approval refused even though the actor is in the authority list"
fi

[ "$(node_state "$SELF_RID" gate)" = "waiting" ] \
  && ok "gate still waiting after the refused self-approval" \
  || bad "gate opened despite the refused self-approval"

# --- 4. An authorized non-creator releases the gate ------------------------
as_approver graph approve "$RID" gate >/dev/null 2>&1 \
  && ok "authorized non-creator approval accepted" \
  || bad "an authorized approval was refused"

DISPATCHED=0
for _ in $(seq 1 40); do
  [ "$(commit_requests)" -ge 1 ] && { DISPATCHED=1; break; }
  sleep 0.5
done
[ "$DISPATCHED" -eq 1 ] \
  && ok "commit node dispatched after the authorized approval" \
  || bad "commit node never dispatched despite a valid approval — the backstop is refusing legitimate work"

AGENT_ROLE=commit "$MUX" inbox --peek 2>/dev/null | grep -q "$COMMIT_MSG" \
  && ok "the dispatch carries the node's own payload" \
  || bad "commit's request does not carry the node's payload"

answer_commit && ok "commit answered its dispatch" || bad "could not answer the commit dispatch"

# The run deliberately does NOT complete here, and asserting that it did would
# demand the exact behaviour that shipped an unverified commit on 2026-09-03.
# A hermetic session has no hook road, so the commit node finishes with
# outcome=unknown, and routeFinishedNodes holds an unknown-outcome node for
# human approval rather than assuming success (graph_exec.go:1682). What the
# gate authority owes us is that the dispatch HAPPENED on a real approval —
# asserted above — not that the run ran to the end without a person.
wait_lifecycle 'graph-unverified-hold' \
  && ok "unverified commit node held for approval, not assumed successful" \
  || bad "no graph-unverified-hold row — an unverified commit node advanced on its own"

[ "$(run_state "$RID")" != "complete" ] \
  && ok "run correctly did not complete behind an unverified commit node" \
  || bad "run completed with an unverified commit node — the hold is inert"

# --- 5. The backstop: a commit dispatch with no approval is refused --------
# retry --from ship re-targets the commit node directly, below a gate that was
# never approved. MUX-132 re-arms only STALE approvals, so a gate that never
# opened holds nothing to purge and the retry walks straight past it — the one
# live path that reaches a commit dispatch with no human approval behind it.
[ "$(commit_requests)" -eq 0 ] \
  && ok "commit inbox drained before the backstop case" \
  || bad "commit inbox not empty before the backstop case — the next assertion would be meaningless"

"$MUX" graph cancel "$SELF_RID" >/dev/null 2>&1 \
  && ok "never-approved run canceled, ready to retry below its gate" \
  || bad "could not cancel the never-approved run"

"$MUX" graph retry "$SELF_RID" --from ship >/dev/null 2>&1 \
  && ok "retry --from ship accepted (targets the commit node below an unopened gate)" \
  || bad "retry --from ship was rejected"

wait_node_state "$SELF_RID" ship failed \
  && ok "commit dispatch refused at the backstop (node failed, nothing committed)" \
  || bad "ship node state $(node_state "$SELF_RID" ship), want failed — an unapproved commit was dispatched"

[ "$(commit_requests)" -eq 0 ] \
  && ok "refused dispatch never reached the commit agent" \
  || bad "an unapproved commit request reached commit's inbox"

grep -q '"event":"commit-authority-refused"' "$LIFELOG" 2>/dev/null \
  && ok "lifecycle records commit-authority-refused" \
  || bad "no commit-authority-refused lifecycle row for the unapproved dispatch"

# --- 6. The control plane is attributable ----------------------------------
grep -q '"event":"graph-run-created"' "$LIFELOG" 2>/dev/null \
  && ok "lifecycle records graph-run-created" \
  || bad "no graph-run-created lifecycle row"

grep -q 'auto' <(grep '"event":"graph-run-created"' "$LIFELOG" 2>/dev/null) \
  && ok "graph-run-created names its creator" \
  || bad "graph-run-created does not name the creating actor"

grep -q '"event":"graph-gate-approved"' "$LIFELOG" 2>/dev/null \
  && ok "lifecycle records graph-gate-approved" \
  || bad "no graph-gate-approved lifecycle row"

grep -q 'test-approver' <(grep '"event":"graph-gate-approved"' "$LIFELOG" 2>/dev/null) \
  && ok "graph-gate-approved names its approver" \
  || bad "graph-gate-approved does not name the approving actor"

# --- Coverage floor --------------------------------------------------------
# 3 validate + 1 daemon + 7 unauthorized + 4 self-approval + 6 authorized
# + 6 backstop + 4 attribution = 31. A section that dies early runs fewer,
# and a green summary over a short run would be the failure this floor exists
# to catch — so the number is the arithmetic, not a comfortable margin under it.
total=$((pass + fail))
if [ "$total" -ge 31 ]; then
  ok "coverage floor met ($total checks executed)"
else
  bad "coverage floor NOT met — only $total checks executed, want >= 31 (a skipped section must not report green)"
fi

# --- Summary ---------------------------------------------------------------
echo ""
echo "  ${pass} passed, ${fail} failed"
[ "$fail" -eq 0 ] || exit 1
exit 0
