#!/usr/bin/env bash
# Integration test for the close-spec completion guard (MUX-114).
#
# Exercises the daemon-side spec-complete dispatch guard end to end: a
# scratch bus session, a real `muxcode watch` daemon executing a gated
# close-spec graph, and scratch spec fixtures under a scratch repo dir.
# Covers: an open spec declines at dispatch (node failed, run failed,
# count + item names reported, nothing sent to plan), commit-spec never
# fires after a decline, the spec file is untouched, the fully-checked
# negative control dispatches and the run completes, and the decline is
# visible in the lifecycle log.
#
# DEVIATION from the spec checklist: "a fully-checked spec closes out and
# moves normally" is proven at the graph level — the guard lets close-spec
# dispatch and the chain completes. The actual status flip and file move
# are the plan agent's work; a hermetic script has no live plan agent, so
# fake agents answer over the real bus as in test-graph-orchestrator.sh.
#
# ISOLATION: scratch BUS_SESSION under /tmp, scratch repo dir pinned via
# MUXCODE_SESSION_REPO_DIR, lifecycle log in a temp dir, empty config.
#
# REQUIRES: the installed muxcode binary must include MUX-114 (run
# ./build.sh first), and tmux must be available.
#
# Usage: bash scripts/test-close-spec-guard.sh
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
export BUS_SESSION="close-guard-test-$$"
BD="/tmp/muxcode-bus-${BUS_SESSION}"
WORK="/tmp/close-guard-work-$$"
REPO="$WORK/repo"
mkdir -p "$REPO/docs/requirements/drafts"
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
  rm -rf "$BD" "$WORK"
}
trap cleanup EXIT

LIFELOG="$MUXCODE_LIFECYCLE_LOG_DIR/${BUS_SESSION}.log"

tmux new-session -d -s "$BUS_SESSION" -n edit -x 120 -y 30
"$MUX" init >/dev/null 2>&1

answer_role() {
  local role="$1" out rid target
  out="$(AGENT_ROLE="$role" "$MUX" inbox 2>/dev/null || true)"
  rid="$(printf '%s' "$out" | grep -o -- '--reply-to [A-Za-z0-9-]*' | tail -1 | awk '{print $2}')"
  target="$(printf '%s' "$out" | grep -o 'muxcode send [a-z-]*' | tail -1 | awk '{print $3}')"
  [ -z "$rid" ] && return 1
  AGENT_ROLE="$role" "$MUX" send "${target:-edit}" response "done" --type response --reply-to "$rid" >/dev/null 2>&1
}

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

run_state()  { "$MUX" graph status "$1" 2>/dev/null | sed $'s/\x1b\\[[0-9;]*[A-Za-z]//g' | head -1 | sed 's/.*\[\([a-z]*\)\].*/\1/'; }
node_state() { "$MUX" graph status "$1" 2>/dev/null | sed $'s/\x1b\\[[0-9;]*[A-Za-z]//g' | awk -v n="$2" '$1==n {print $2}'; }

wait_run_state() {
  local rid="$1" want="$2" i
  for i in $(seq 1 40); do
    [ "$(run_state "$rid")" = "$want" ] && return 0
    sleep 0.5
  done
  return 1
}

# wait_node_state — gate approval markers are purged at gate dispatch
# (single-use rule), so approving before the gate reaches waiting would be
# silently discarded.
wait_node_state() {
  local rid="$1" node="$2" want="$3" i
  for i in $(seq 1 40); do
    [ "$(node_state "$rid" "$node")" = "$want" ] && return 0
    sleep 0.5
  done
  return 1
}

# --- Fixtures --------------------------------------------------------------
OPEN_SPEC="docs/requirements/drafts/open-spec.md"
cat > "$REPO/$OPEN_SPEC" <<'EOF'
# Open Spec

## Requirements

- [x] a finished thing
- [ ] first open item
- [ ] second open item
- [ ] third open item

## Status

In Progress
EOF

DONE_SPEC="docs/requirements/drafts/done-spec.md"
cat > "$REPO/$DONE_SPEC" <<'EOF'
# Done Spec

## Requirements

- [x] everything
- [x] is finished

## Status

In Progress
EOF

cat > "$WORK/close-guard.json" <<'EOF'
{"name": "close-guard", "start": "gate",
 "nodes": [
   {"id": "gate", "type": "wait_human", "message": "Approve the spec close-out with its commit and push"},
   {"id": "close-spec", "type": "send", "role": "plan", "action": "update-docs", "guard": "spec-complete", "message": "Close out the active requirements doc"},
   {"id": "commit-spec", "type": "send", "role": "commit", "action": "commit", "message": "Commit the spec move"}],
 "edges": [
   {"from": "gate", "to": "close-spec"},
   {"from": "close-spec", "to": "commit-spec"}]}
EOF
"$MUX" graph validate "$WORK/close-guard.json" >/dev/null 2>&1 \
  && ok "close-guard graph validates (gated commit, known guard)" \
  || bad "close-guard graph failed validation"

# --- Daemon ----------------------------------------------------------------
"$MUX" watch "$BUS_SESSION" --poll 2 >"$WORK/daemon.log" 2>&1 &
DPID=$!
sleep 1
kill -0 "$DPID" 2>/dev/null && ok "scratch daemon running (pid $DPID)" \
  || bad "scratch daemon exited immediately: $(tail -3 "$WORK/daemon.log" 2>/dev/null)"

# --- 1. Open spec: guard declines at dispatch ------------------------------
(cd "$REPO" && "$MUX" spec set "$OPEN_SPEC" >/dev/null 2>&1) \
  && ok "active spec set to the open fixture" \
  || bad "spec set failed for the open fixture"

RID="$("$MUX" graph run --file "$WORK/close-guard.json" 2>&1 | grep -o 'Started run [^ ]*' | awk '{print $3}')"
[ -n "$RID" ] && ok "run started: $RID" || bad "run failed to start"

wait_node_state "$RID" gate waiting \
  && ok "gate reached waiting" || bad "gate never reached waiting: $(node_state "$RID" gate)"
"$MUX" graph approve "$RID" gate >/dev/null 2>&1 \
  && ok "gate approved" || bad "gate approve failed"

wait_run_state "$RID" failed \
  && ok "run failed at the guard (open spec must not close)" \
  || bad "run state $(run_state "$RID"), want failed"

[ "$(node_state "$RID" close-spec)" = "failed" ] \
  && ok "close-spec node failed at dispatch" \
  || bad "close-spec state $(node_state "$RID" close-spec), want failed"

STATUS_OUT="$("$MUX" graph status "$RID" 2>/dev/null | sed $'s/\x1b\\[[0-9;]*[A-Za-z]//g')"
printf '%s' "$STATUS_OUT" | grep -q '3 open items' \
  && ok "decline reports the open-item count" \
  || bad "no open-item count in status output"
printf '%s' "$STATUS_OUT" | grep -q 'first open item' \
  && ok "decline names the open items" \
  || bad "open item names missing from status output"

plan_reqs="$(AGENT_ROLE=plan "$MUX" inbox --peek 2>/dev/null | grep -c 'Type: request' || true)"
[ "$plan_reqs" -eq 0 ] && ok "declined dispatch never reached plan" \
  || bad "plan received $plan_reqs requests despite the decline"

[ "$(node_state "$RID" commit-spec)" = "pending" ] \
  && ok "commit-spec held back (never dispatched after decline)" \
  || bad "commit-spec state $(node_state "$RID" commit-spec), want pending"
commit_reqs="$(AGENT_ROLE=commit "$MUX" inbox --peek 2>/dev/null | grep -c 'Type: request' || true)"
[ "$commit_reqs" -eq 0 ] && ok "commit inbox empty — nothing to commit was requested" \
  || bad "commit received $commit_reqs requests after the decline"

[ -f "$REPO/$OPEN_SPEC" ] && grep -q 'In Progress' "$REPO/$OPEN_SPEC" \
  && ok "spec file untouched (still in drafts/, status unchanged)" \
  || bad "spec file moved or status changed after the decline"

grep -q '"event":"graph-guard-declined"' "$LIFELOG" 2>/dev/null \
  && ok "lifecycle records graph-guard-declined" \
  || bad "no graph-guard-declined lifecycle row"

# --- 2. Negative control: fully-checked spec dispatches and completes ------
(cd "$REPO" && "$MUX" spec set "$DONE_SPEC" >/dev/null 2>&1) \
  && ok "active spec switched to the fully-checked fixture" \
  || bad "spec set failed for the done fixture"

RID2="$("$MUX" graph run --file "$WORK/close-guard.json" 2>&1 | grep -o 'Started run [^ ]*' | awk '{print $3}')"
[ -n "$RID2" ] && ok "negative-control run started: $RID2" || bad "negative-control run failed to start"
wait_node_state "$RID2" gate waiting || bad "negative-control gate never reached waiting"
"$MUX" graph approve "$RID2" gate >/dev/null 2>&1 || bad "negative-control gate approve failed"

if wait_for_request plan; then
  ok "fully-checked spec dispatched close-spec to plan (guard not inert)"
  answer_role plan || bad "could not answer plan request"
else
  bad "close-spec never dispatched with a fully-checked spec — guard went inert"
fi

if wait_for_request commit; then
  ok "commit-spec dispatched after close-spec completed"
  answer_role commit || bad "could not answer commit request"
else
  bad "commit-spec never dispatched on the negative control"
fi

wait_run_state "$RID2" complete \
  && ok "negative-control run reached complete" \
  || bad "negative-control run state $(run_state "$RID2"), want complete"

# --- Coverage floor --------------------------------------------------------
total=$((pass + fail))
if [ "$total" -ge 18 ]; then
  ok "coverage floor met ($total checks executed)"
else
  bad "coverage floor NOT met — only $total checks executed, want >= 18 (a skipped run must not report green)"
fi

# --- Summary ---------------------------------------------------------------
echo ""
echo "  ${pass} passed, ${fail} failed"
[ "$fail" -eq 0 ] || exit 1
exit 0
