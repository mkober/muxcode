#!/usr/bin/env bash
# Integration test for MUX-181: a graph-dispatched tracker write is judged at
# the send on the wait_human gate that released it, not on configuration or on
# the "The user approved…" sentence its template writes.
#
# Hermetic: muxcode built from the tree (MUXCODE_ATLASSIAN_GATE_BIN overrides
# the binary under test), scratch HOME holding the gate authority, scratch
# lifecycle log, scratch daemons on a private tmux server. plan is played by
# reading its inbox. A hand-written `daemon` dispatch is sent through the real
# send road by a small program built against a copy of the tree — the CLI
# normalizes a `daemon` sender to edit, so it cannot forge one.
#
#   1  validateGates still rejects an ungated jira-write graph and accepts the
#      gated one
#   2  retry --from the write node below a gate nobody approved: the dispatch
#      is refused, the node fails, plan's inbox stays empty, and
#      tracker-write-refused names run and node
#   3  an authorized non-creator approval releases the gate: plan's inbox holds
#      the dispatch, its Origin line naming the run
#   4  hand-written daemon jira-writes — one naming that real, approved run and
#      node with a different payload, one carrying no run or node — are each
#      refused with a tracker-write-refused row naming why
#   5  negative control: an edit-sent jira-write is delivered untouched
#   6  control: a muxcode built from a copy with the send-time check removed
#      delivers section 2's unapproved dispatch
#
# Usage: bash scripts/test-atlassian-gate.sh
set -uo pipefail
export MUXCODE_CONSUMER_GUARD=0 # plan is played by reading its inbox, which no window or listener shows (MUX-145)

PASS=0
FAIL=0
EXPECTED_PASS=21

command -v jq >/dev/null 2>&1 || { echo "SKIP: jq is required"; exit 2; }
command -v go >/dev/null 2>&1 || { echo "SKIP: go is required"; exit 2; }
command -v tmux >/dev/null 2>&1 || { echo "SKIP: tmux is required"; exit 2; }

REPO=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
WORK=$(mktemp -d /tmp/atlassian-gate-XXXXXX)
. "$REPO/scripts/lib/private-tmux.sh"
private_tmux_init "$WORK/tmux"
DPIDS=""
cleanup() {
  local p
  for p in $DPIDS; do kill "$p" 2>/dev/null; done
  private_tmux_kill
  cd / 2>/dev/null
  rm -rf "$WORK"
}
trap cleanup EXIT
export GOCACHE="$(go env GOCACHE)" GOMODCACHE="$(go env GOMODCACHE)" GOTOOLCHAIN=local

ok()   { PASS=$((PASS + 1)); echo "  ok: $1"; }
fail() { FAIL=$((FAIL + 1)); echo "  FAIL: $1"; }

# --- Builds ----------------------------------------------------------------
mkdir -p "$WORK/bin"
if [ -n "${MUXCODE_ATLASSIAN_GATE_BIN:-}" ]; then
  MUX="$(cd "$(dirname "$MUXCODE_ATLASSIAN_GATE_BIN")" && pwd)/$(basename "$MUXCODE_ATLASSIAN_GATE_BIN")"
else
  (cd "$REPO/tools/muxcode" && go build -buildvcs=false -o "$WORK/bin/muxcode" .) \
    || { echo "SKIP: go build muxcode failed"; exit 2; }
  MUX="$WORK/bin/muxcode"
fi

# forge SESSION TO ACTION PAYLOAD RUN NODE — send a raw daemon request through
# bus.SendNoCC, as the executor does; exits 1 and prints the refusal if refused.
cp -R "$REPO/tools/muxcode" "$WORK/src"
mkdir -p "$WORK/src/forgecmd"
cat > "$WORK/src/forgecmd/main.go" <<'GO'
package main

import (
	"fmt"
	"os"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

func main() {
	a := os.Args[1:]
	m := bus.NewMessage("daemon", a[1], "request", a[2], a[3], "")
	m.GraphRun, m.GraphNode = a[4], a[5]
	if err := bus.SendNoCC(a[0], m); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
GO
(cd "$WORK/src" && go build -buildvcs=false -o "$WORK/bin/forge" ./forgecmd) 2>"$WORK/forge-build.log" \
  || { echo "SKIP: building the forge helper failed: $(tail -3 "$WORK/forge-build.log")"; exit 2; }

# --- Isolation -------------------------------------------------------------
cd "$WORK" || exit 1
export HOME="$WORK/home"
mkdir -p "$HOME/.config/muxcode"
echo "MUXCODE_GATE_AUTHORITY_ROLES=test-approver" > "$HOME/.config/muxcode/config"
export XDG_STATE_HOME="$HOME/.local/state"
export MUXCODE_LIFECYCLE_LOG_DIR="$WORK/lifecycle"
: > "$WORK/empty-config"
export MUXCODE_CONFIG="$WORK/empty-config"
export MUXCODE_TMP_CLEANUP_THRESHOLD=0 MUXCODE_BRANCH_TIME_DISABLE=1 MUXCODE_DEDUP_WINDOW=0
export MUXCODE_SESSION_REPO_DIR="$WORK"
unset MUXCODE_ATLASSIAN_AUTHORITY_ROLES MUXCODE_COMMIT_AUTHORITY_ROLES 2>/dev/null || true

JIRA_MSG="The user approved the tracker update: sync PROMGT-118 to the spec"
cat > "$WORK/gated.json" <<EOF
{"name": "gated-jira", "start": "gate",
 "nodes": [
   {"id": "gate", "type": "wait_human", "message": "Approve the tracker update"},
   {"id": "j", "type": "send", "role": "plan", "action": "jira-write", "message": "$JIRA_MSG"}],
 "edges": [{"from": "gate", "to": "j"}]}
EOF
cat > "$WORK/ungated.json" <<EOF
{"name": "ungated-jira", "start": "j",
 "nodes": [{"id": "j", "type": "send", "role": "plan", "action": "jira-write", "message": "$JIRA_MSG"}],
 "edges": []}
EOF

# use_bus NAME BIN — a scratch session with its own BIN daemon; sets
# BUS_SESSION, BIN and LOG.
use_bus() {
  BIN="$2"
  export BUS_SESSION="atl-gate-$1-$$"
  LOG="$MUXCODE_LIFECYCLE_LOG_DIR/$BUS_SESSION.log"
  tmux new-session -d -s "$BUS_SESSION" -n edit -x 120 -y 30 2>/dev/null
  "$BIN" init >/dev/null 2>&1
  "$BIN" watch "$BUS_SESSION" --poll 1 >"$WORK/daemon-$1.log" 2>&1 &
  DPIDS="$DPIDS $!"
}

node_state() { "$BIN" graph status "$1" 2>/dev/null | sed $'s/\x1b\\[[0-9;]*[A-Za-z]//g' | awk -v n="$2" '$1==n {print $2}'; }
wait_node_state() {
  local i
  for i in $(seq 1 40); do
    [ "$(node_state "$1" "$2")" = "$3" ] && return 0
    sleep 0.5
  done
  return 1
}
start_run() { AGENT_ROLE=auto "$BIN" graph run --file "$WORK/gated.json" 2>&1 | grep -o 'Started run [^ ]*' | awk '{print $3}'; }
plan_requests() { AGENT_ROLE=plan "$BIN" inbox --peek 2>/dev/null | grep -c 'Type: request' || true; }
drain_plan() { AGENT_ROLE=plan "$BIN" inbox >/dev/null 2>&1; }
refused() { grep '"event":"tracker-write-refused"' "$LOG" 2>/dev/null | grep -q -- "$1"; }

# unapproved_retry — section 2's road: a run whose gate nobody approves,
# canceled, then retried from its write node. Sets RID.
unapproved_retry() {
  RID="$(start_run)"
  wait_node_state "$RID" gate waiting || return 1
  "$BIN" graph cancel "$RID" >/dev/null 2>&1 || return 1
  "$BIN" graph retry "$RID" --from j >/dev/null 2>&1
}

# --- 1. The structural rule ------------------------------------------------
echo "-- 1: validateGates"
"$MUX" graph validate "$WORK/ungated.json" >"$WORK/ungated.out" 2>&1 \
  && fail "1: an ungated jira-write graph validated" \
  || { grep -q wait_human "$WORK/ungated.out" && ok "1: an ungated jira-write graph is rejected, naming wait_human" \
         || fail "1: the rejection does not name wait_human: $(head -2 "$WORK/ungated.out")"; }
"$MUX" graph validate "$WORK/gated.json" >/dev/null 2>&1 && ok "1: the gated jira-write graph validates" \
  || fail "1: the gated graph failed validation"

# --- 2. Unapproved gate: refused at the send -------------------------------
echo "-- 2: retry --from the write node below an unapproved gate"
use_bus main "$MUX"
if unapproved_retry; then
  ok "2: retry --from j accepted below the unapproved gate"
  wait_node_state "$RID" j failed && ok "2: the write node failed — its dispatch was refused" \
    || fail "2: node j is $(node_state "$RID" j), want failed"
  [ "$(plan_requests)" -eq 0 ] && ok "2: plan's inbox is empty" || fail "2: an unapproved jira-write reached plan"
  refused "run=$RID node=j" && ok "2: tracker-write-refused names the run and node" \
    || fail "2: no tracker-write-refused row naming run $RID node j"
else
  fail "2: could not reach retry --from below the gate"; fail "2: (skipped)"; fail "2: (skipped)"; fail "2: (skipped)"
fi

# --- 3. Authorized non-creator approval releases it -----------------------
echo "-- 3: an authorized approval releases the dispatch"
RID2="$(start_run)"
if wait_node_state "$RID2" gate waiting; then
  AGENT_ROLE=test-approver "$BIN" graph approve "$RID2" gate >/dev/null 2>&1 \
    && ok "3: the authorized non-creator approval is accepted" || fail "3: the approval was refused"
  for _ in $(seq 1 40); do [ "$(plan_requests)" -ge 1 ] && break; sleep 0.5; done
  inbox=$(AGENT_ROLE=plan "$BIN" inbox --peek 2>/dev/null)
  grep -q "Action: jira-write" <<<"$inbox" && grep -q "PROMGT-118" <<<"$inbox" \
    && ok "3: plan's inbox holds the node's jira-write" || fail "3: plan's inbox: $(head -5 <<<"$inbox")"
  grep -q "Origin: graph run $RID2" <<<"$inbox" && ok "3: its Origin line names the run" \
    || fail "3: no 'Origin: graph run $RID2' line"
  refused "run=$RID2" && fail "3: the approved dispatch was logged refused" || ok "3: no refusal row for the approved dispatch"
  drain_plan
else
  fail "3: the second run never reached its gate"; fail "3: (skipped)"; fail "3: (skipped)"; fail "3: (skipped)"
fi

# --- 4. Hand-written daemon dispatches -------------------------------------
echo "-- 4: hand-written daemon jira-writes"
"$WORK/bin/forge" "$BUS_SESSION" plan jira-write "Transition every open story to Done" "$RID2" j >"$WORK/forge1.out" 2>&1 \
  && fail "4: a forged payload naming the real approved run and node was delivered" \
  || ok "4: a forged payload naming the real approved run and node is refused"
refused "payload is not what node" && ok "4: its row names the payload mismatch" || fail "4: no row naming the payload mismatch"
"$WORK/bin/forge" "$BUS_SESSION" plan jira-write "$JIRA_MSG" "" "" >"$WORK/forge2.out" 2>&1 \
  && fail "4: a daemon jira-write with no run or node was delivered" \
  || ok "4: a daemon jira-write with no run or node is refused"
refused "no graph provenance" && ok "4: its row names the missing provenance" || fail "4: no row naming the missing provenance"
[ "$(plan_requests)" -eq 0 ] && ok "4: neither forgery reached plan" || fail "4: a forged jira-write reached plan"

# --- 5. Negative control: edit relays the user -----------------------------
echo "-- 5: an edit-sent jira-write"
AGENT_ROLE=edit "$BIN" send plan jira-write "User asked: update PROMGT-118 to reference the spec" >/dev/null 2>&1 \
  && ok "5: edit's jira-write is sent" || fail "5: edit's jira-write was refused"
AGENT_ROLE=plan "$BIN" inbox --peek 2>/dev/null | grep -q "User asked: update PROMGT-118" \
  && ok "5: plan's inbox holds edit's relay untouched" || fail "5: edit's relay did not reach plan"
drain_plan

tmux list-sessions -F '#S' 2>/dev/null | grep -q "^atl-gate-main-$$" \
  && ok "the scratch session lives on the private tmux server" || fail "the private tmux server does not hold the scratch session"

# --- 6. Control: the check removed -----------------------------------------
echo "-- 6: control — the send-time check removed"
cp -R "$REPO/tools/muxcode" "$WORK/ctrl-src"
perl -pi -e 's/if deny := CheckTrackerWriteForMessage\(session, m\); deny != "" \{/if deny := ""; deny != "" {/' "$WORK/ctrl-src/bus/inbox.go"
grep -q 'CheckTrackerWriteForMessage(session, m)' "$WORK/ctrl-src/bus/inbox.go" \
  && fail "6: the patch did not remove the check — sendMessage changed shape" \
  || ok "6: the send-time check removed from a copy of the tree"
if (cd "$WORK/ctrl-src" && go build -buildvcs=false -o "$WORK/bin/muxcode-ctrl" .) 2>"$WORK/ctrl-build.log"; then
  ok "6: the control muxcode builds"
  use_bus ctrl "$WORK/bin/muxcode-ctrl"
  if unapproved_retry && wait_node_state "$RID" j running; then
    [ "$(plan_requests)" -ge 1 ] && ok "6: without the check the unapproved dispatch reaches plan — the defect section 2 catches" \
      || fail "6: node running but plan's inbox is empty"
  else
    fail "6: the control run's write node is $(node_state "$RID" j), want running"
  fi
else
  fail "6: building the control muxcode failed: $(tail -3 "$WORK/ctrl-build.log")"; fail "6: (skipped)"
fi

# ── Coverage floor ─────────────────────────────────────────────────
echo
echo "=== results: $PASS passed, $FAIL failed (floor $EXPECTED_PASS) ==="
if [ "$PASS" -ne "$EXPECTED_PASS" ]; then
  echo "  FAIL  coverage floor: expected exactly $EXPECTED_PASS passes, got $PASS — a section was skipped or double-counted"
  exit 1
fi
[ "$FAIL" -eq 0 ] || exit 1
exit 0
