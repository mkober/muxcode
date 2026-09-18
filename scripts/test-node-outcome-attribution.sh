#!/usr/bin/env bash
# Integration test for node-outcome attribution (MUX-148, Phase 5).
#
# The unit suite proves the predicates — rowAttributesTo, deriveSendOutcome,
# parseExitSentinel, seedVerdictToken. This proves the ROAD: that a history row
# minted by a real `muxcode log`, a real bus reply and a real daemon executor
# resolve to the outcome the predicates promise, and that a node whose outcome
# cannot be established stops the pipeline instead of advancing it.
#
# The defect this closes (2026-09-03): a node asked to reply to PR comments was
# recorded `success` because the commit role had run some git command after
# dispatch — "a git command ran" standing in for "the task was done".
#
# Covers:
#   1. commit-pr-review-loop's template shape: a commit node between `c` and
#      `d`, gated, with `d` told to cite the sha (Phase 4's push-fixes).
#   2. The 2026-09-03 shape live: an agent that declines AFTER a successful
#      read-only command does not route as success — it holds, logs
#      graph-outcome-untied and graph-unverified-hold, and its successor
#      never dispatches.
#   3. Negative control: identical evidence, a genuine completion (EXIT=0)
#      routes success and the successor DOES dispatch.
#   4. No prose parsing: declines worded differently — including one whose
#      prose contains "successfully" — are caught the same way.
#   5. The mirror (Defect 4): a recognised failing first attempt plus an
#      unrecognised passing re-run is NOT recorded as failure, with a
#      recognised passing re-run as the negative control.
#   6. The cite-shape road: gate2 -> c -> push-fixes -> d, where `c` is
#      attributed by its seeded token and `push-fixes` by a real git-commit
#      row, so `d` is reached with a citable commit. The seed is asserted in
#      both directions on the live dispatch.
#
# ISOLATION: scratch BUS_SESSION under /tmp, scratch HOME, scratch repo dir,
# lifecycle log in a temp dir, empty muxcode config. The scratch HOME is what
# isolates gate authority — GateAuthorityConfigured walks a FIXED path list and
# never consults $MUXCODE_CONFIG, and the daemon seals the list at startup, so
# the config must be written before the daemon launches. We also run from the
# scratch dir because that path list consults a CWD-relative .muxcode/config
# first.
#
# REQUIRES: installed muxcode >= v0.1.0, tmux, python3. Run ./build.sh first —
# this tests the INSTALLED binary, so an unbuilt tree tests the previous fix.
#
# Usage: bash scripts/test-node-outcome-attribution.sh
#        MUXCODE_TEST_KEEP=1 bash scripts/test-node-outcome-attribution.sh
set -uo pipefail

MUX="${MUXCODE_BIN:-muxcode}"
MUX="$(command -v "$MUX" 2>/dev/null || echo "$MUX")"
if [ ! -x "$MUX" ]; then
  echo "  FAIL  cannot resolve muxcode binary ('$MUX') — set MUXCODE_BIN"
  exit 1
fi
command -v tmux >/dev/null 2>&1 || { echo "  FAIL  tmux not available"; exit 1; }
command -v python3 >/dev/null 2>&1 || { echo "  FAIL  python3 required"; exit 1; }
. "$(dirname "${BASH_SOURCE[0]}")/lib/muxcode-version.sh"
require_muxcode_version "$MUX" v0.1.0 MUX-148 || { echo "  FAIL  binary precondition not met"; exit 1; }

GREEN=$'\033[0;32m'; RED=$'\033[0;31m'; NC=$'\033[0m'
pass=0; fail=0
ok()  { echo "  ${GREEN}PASS${NC}  $*"; pass=$((pass + 1)); }
bad() { echo "  ${RED}FAIL${NC}  $*"; fail=$((fail + 1)); }

# Two identities: `auto` creates every run, `test-approver` is the only role in
# the authority list, so an approval is never a self-approval.
as_creator()  { AGENT_ROLE=auto "$MUX" "$@"; }
as_approver() { AGENT_ROLE=test-approver "$MUX" "$@"; }

# --- Isolation -------------------------------------------------------------
export BUS_SESSION="node-outcome-test-$$"
BD="/tmp/muxcode-bus-${BUS_SESSION}"
WORK="/tmp/node-outcome-work-$$"
REPO="$WORK/repo"
mkdir -p "$REPO"
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
  cd / 2>/dev/null # we run from $WORK; do not rm the directory we stand in
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

# --- Helpers ---------------------------------------------------------------
cat > "$WORK/gq.py" <<'PY'
import json, sys

data = json.load(sys.stdin)
mode = sys.argv[1]
nodes = data.get("nodes") or {}
graph = data.get("graph") or {}
if mode == "node-outcome":
    print((nodes.get(sys.argv[2]) or {}).get("outcome", ""))
elif mode == "node-state":
    print((nodes.get(sys.argv[2]) or {}).get("state", ""))
elif mode == "run-state":
    print((data.get("run") or {}).get("state", ""))
elif mode == "def-field":
    defs = {n["id"]: n for n in graph.get("nodes") or []}
    print((defs.get(sys.argv[2]) or {}).get(sys.argv[3], ""))
elif mode == "has-edge":
    for e in graph.get("edges") or []:
        if e.get("from") == sys.argv[2] and e.get("to") == sys.argv[3]:
            sys.exit(0)
    sys.exit(1)
PY

gq() { local rid="$1"; shift; "$MUX" graph status "$rid" --json 2>/dev/null | python3 "$WORK/gq.py" "$@"; }
node_outcome() { gq "$1" node-outcome "$2"; }
node_state()   { gq "$1" node-state "$2"; }
run_state()    { gq "$1" run-state; }

# start_run takes a fixture path or a builtin template name — a path carries a
# slash, a template name never does.
start_run() {
  local out
  case "$1" in
    */*) out="$(as_creator graph run --file "$1" 2>&1)" ;;
    *)   out="$(as_creator graph run "$1" 2>&1)" ;;
  esac
  printf '%s' "$out" | grep -o 'Started run [^ ]*' | awk '{print $3}'
}

# settle gives the executor one poll interval to finish routing before an inbox
# is drained, so a dispatch in flight cannot land after the drain and pollute
# the next section's first assertion.
settle() { sleep 2.5; }

inbox_requests() { AGENT_ROLE="$1" "$MUX" inbox --peek 2>/dev/null | grep -c 'Type: request' || true; }
peek()           { AGENT_ROLE="$1" "$MUX" inbox --peek 2>/dev/null; }
drain()          { AGENT_ROLE="$1" "$MUX" inbox >/dev/null 2>&1 || true; }

wait_dispatch() {
  local role="$1" i
  for i in $(seq 1 40); do
    [ "$(inbox_requests "$role")" -ge 1 ] && return 0
    sleep 0.5
  done
  return 1
}

wait_node_outcome() {
  local rid="$1" node="$2" want="$3" i
  for i in $(seq 1 40); do
    [ "$(node_outcome "$rid" "$node")" = "$want" ] && return 0
    sleep 0.5
  done
  return 1
}

wait_node_state() {
  local rid="$1" node="$2" want="$3" i
  for i in $(seq 1 40); do
    [ "$(node_state "$rid" "$node")" = "$want" ] && return 0
    sleep 0.5
  done
  return 1
}

wait_lifecycle() {
  local pat="$1" i
  for i in $(seq 1 40); do
    grep -q "\"event\":\"$pat\"" "$LIFELOG" 2>/dev/null && return 0
    sleep 0.5
  done
  return 1
}

# answer_as consumes the role's dispatch and replies with the given payload.
# The reply goes to edit because daemon normalizes to edit, and correlation is
# by --reply-to, which is what the executor reads.
answer_as() {
  local role="$1" payload="$2" out rid
  out="$(AGENT_ROLE="$role" "$MUX" inbox 2>/dev/null || true)"
  rid="$(printf '%s' "$out" | grep -o -- '--reply-to [A-Za-z0-9-]*' | tail -1 | awk '{print $2}')"
  [ -z "$rid" ] && return 1
  AGENT_ROLE="$role" "$MUX" send edit response "$payload" --type response --reply-to "$rid" >/dev/null 2>&1
}

# A row minted the way a self-reporting agent mints one. WriteHookHistory is the
# one road history rows travel, and `muxcode log` is the CLI on it.
mint_row() { "$MUX" log "$1" "$2" --exit-code "$3" --command "$4" >/dev/null 2>&1; }

# --- Fixtures --------------------------------------------------------------
# The declining node's action is pr-read: read-only (so nodeRequiresGate exempts
# it), in actionsWithoutCommandEvidence, and the exact action of the 2026-09-03
# node. Its successor is an ordinary send node, not a gate — successorsAllHumanGates
# deliberately skips the hold when the gate is next anyway.
cat > "$WORK/decline.json" <<'EOF'
{"name": "decline-shape", "start": "answer",
 "nodes": [
   {"id": "answer", "type": "send", "role": "commit", "action": "pr-read",
    "message": "Reply to the PR review comments"},
   {"id": "after", "type": "send", "role": "build", "action": "build",
    "message": "Build once the comments are answered"}],
 "edges": [{"from": "answer", "to": "after"}]}
EOF

cat > "$WORK/mirror.json" <<'EOF'
{"name": "mirror-shape", "start": "suite",
 "nodes": [
   {"id": "suite", "type": "send", "role": "test", "action": "test",
    "message": "Run the suite and report"},
   {"id": "after", "type": "send", "role": "review", "action": "review",
    "message": "Review once the suite is green"}],
 "edges": [{"from": "suite", "to": "after"}]}
EOF

# The cite shape, mirroring commit-pr-review-loop's gate2 -> c -> push-fixes -> d.
# `c` is role review rather than edit: an edit-role node cannot be answered in a
# test, because Send drops a self-addressed reply (isLoopingSelfSend). What the
# node needs is an action no command evidences, which `review` is.
cat > "$WORK/cite.json" <<'EOF'
{"name": "cite-shape", "start": "gate2",
 "nodes": [
   {"id": "gate2", "type": "wait_human",
    "message": "Approve addressing the feedback, committing and pushing it, and replying"},
   {"id": "c", "type": "send", "role": "review", "action": "review",
    "message": "Address the PR review comments"},
   {"id": "push", "type": "send", "role": "commit", "action": "commit",
    "message": "Stage and commit the review-feedback changes, push them, and report the commit sha"},
   {"id": "d", "type": "send", "role": "commit", "action": "comment",
    "message": "Reply to the PR comments, citing the commit sha reported upstream"}],
 "edges": [
   {"from": "gate2", "to": "c"},
   {"from": "c", "to": "push"},
   {"from": "push", "to": "d"}]}
EOF

# --- 1. The template shape Phase 4 fixed -----------------------------------
# Run the builtin BEFORE the daemon exists: run state is created, nothing
# dispatches, and `graph status --json` hands back the resolved template.
"$MUX" graph validate commit-pr-review-loop >/dev/null 2>&1 \
  && ok "commit-pr-review-loop validates" \
  || bad "commit-pr-review-loop failed validation"

TPL_RID="$(start_run commit-pr-review-loop)"
if [ -z "$TPL_RID" ]; then
  bad "could not start commit-pr-review-loop to read its resolved shape"
else
  [ "$(gq "$TPL_RID" def-field push-fixes role)" = "commit" ] \
    && ok "push-fixes is a commit-role node" \
    || bad "push-fixes role is $(gq "$TPL_RID" def-field push-fixes role), want commit"

  [ "$(gq "$TPL_RID" def-field push-fixes action)" = "commit" ] \
    && ok "push-fixes carries the commit action (it is the node that mints the sha)" \
    || bad "push-fixes action is $(gq "$TPL_RID" def-field push-fixes action), want commit"

  gq "$TPL_RID" has-edge c push-fixes \
    && ok "edge c -> push-fixes (the commit sits between c and d)" \
    || bad "no c -> push-fixes edge — the template gap is back"

  gq "$TPL_RID" has-edge push-fixes d \
    && ok "edge push-fixes -> d" \
    || bad "no push-fixes -> d edge"

  [ "$(gq "$TPL_RID" def-field gate2 type)" = "wait_human" ] \
    && ok "push-fixes sits in gate2's territory (gate2 is the wait_human above c)" \
    || bad "gate2 is $(gq "$TPL_RID" def-field gate2 type), want wait_human"

  gq "$TPL_RID" def-field d message | grep -qi 'sha' \
    && ok "d is told to cite the commit sha" \
    || bad "d's message does not mention the sha it is supposed to cite"

  "$MUX" graph cancel "$TPL_RID" >/dev/null 2>&1
fi

# --- Daemon ----------------------------------------------------------------
"$MUX" watch "$BUS_SESSION" --poll 2 >"$WORK/daemon.log" 2>&1 &
DPID=$!
sleep 1
kill -0 "$DPID" 2>/dev/null \
  && ok "scratch daemon running (pid $DPID)" \
  || bad "scratch daemon exited immediately: $(tail -3 "$WORK/daemon.log" 2>/dev/null)"

# --- 2. The 2026-09-03 shape: decline after a successful read-only command --
RID="$(start_run "$WORK/decline.json")"
[ -n "$RID" ] && ok "decline run started: $RID" || bad "decline run failed to start"

wait_dispatch commit \
  && ok "answer node dispatched to commit" \
  || bad "answer node never dispatched"

peek commit | grep -q 'verdict token' \
  && ok "the dispatch seeds the verdict token (pr-read has no command that could evidence it)" \
  || bad "no seeded verdict token on an unevidenced action — seedVerdictToken is not on this road"

# The successful read-only command the agent really ran. Unattributable to
# pr-read by construction: no command can evidence "the comments were answered".
mint_row commit "checked the branch state" 0 "git status --short" \
  && ok "minted a successful read-only git row for commit" \
  || bad "could not mint the read-only row"

answer_as commit "I could not reply to the PR comments — the branch has no open PR, so there was nothing to answer." \
  && ok "agent declined the task (no verdict token in the reply)" \
  || bad "could not answer the answer node's dispatch"

wait_node_outcome "$RID" answer unknown \
  && ok "declining node routed unknown, not success — a git row did not answer for the task" \
  || bad "answer node outcome is $(node_outcome "$RID" answer), want unknown — the 2026-09-03 defect is live"

wait_lifecycle 'graph-outcome-untied' \
  && ok "lifecycle records graph-outcome-untied for the row that could not testify" \
  || bad "no graph-outcome-untied row — the untied row was accepted silently"

wait_lifecycle 'graph-unverified-hold' \
  && ok "the unattributed node held for a human rather than advancing" \
  || bad "no graph-unverified-hold row — an unverified node advanced on its own"

[ "$(inbox_requests build)" -eq 0 ] \
  && ok "successor never dispatched behind the held node" \
  || bad "the build successor dispatched despite an unestablished outcome"

[ "$(run_state "$RID")" != "complete" ] \
  && ok "run did not complete behind the held node" \
  || bad "run completed with an unattributed node — the hold is inert"

"$MUX" graph cancel "$RID" >/dev/null 2>&1
drain build

# --- 3. Negative control: a genuine completion still routes success --------
# Identical graph, identical minted row. Only the reply differs, so a failure
# here is the attribution refusing legitimate work rather than a graph defect.
CTRL_RID="$(start_run "$WORK/decline.json")"
[ -n "$CTRL_RID" ] && ok "control run started: $CTRL_RID" || bad "control run failed to start"

wait_dispatch commit \
  && ok "control answer node dispatched" \
  || bad "control answer node never dispatched"

mint_row commit "checked the branch state" 0 "git status --short"
answer_as commit "$(printf 'Replied to all four review comments on the PR.\nEXIT=0')" \
  && ok "agent completed the task and emitted its verdict token" \
  || bad "could not answer the control dispatch"

wait_node_outcome "$CTRL_RID" answer success \
  && ok "genuine completion routed success on its own token (negative control)" \
  || bad "control node outcome is $(node_outcome "$CTRL_RID" answer), want success — attribution is refusing real work"

wait_dispatch build \
  && ok "successor dispatched behind the completed node — the pipeline still advances" \
  || bad "successor never dispatched behind a legitimate success"

"$MUX" graph cancel "$CTRL_RID" >/dev/null 2>&1
settle
drain build
drain commit

# --- 4. No prose parsing ---------------------------------------------------
# Two declines worded nothing like section 2's. The second contains
# "successfully" and "completed" precisely because prose parsing would read it
# as a success; only the absent token decides.
decline_case() {
  local label="$1" payload="$2" rid
  rid="$(start_run "$WORK/decline.json")"
  if [ -z "$rid" ]; then bad "prose run ($label) failed to start"; return; fi
  if ! wait_dispatch commit; then bad "prose run ($label) never dispatched"; return; fi
  mint_row commit "read the diff" 0 "git diff --stat"
  answer_as commit "$payload" >/dev/null
  if wait_node_outcome "$rid" answer unknown; then
    ok "decline caught regardless of wording ($label)"
  else
    bad "prose variant ($label) routed $(node_outcome "$rid" answer), want unknown"
  fi
  "$MUX" graph cancel "$rid" >/dev/null 2>&1
  drain commit
}

decline_case "plain refusal" \
  "Skipping this one. The comment thread is resolved already and I am not going to post anything."
decline_case "success words in the prose" \
  "Successfully read the PR and completed my analysis of every thread, but I did not post any reply."

# --- 5. The mirror: a genuine pass must not be recorded as failure ---------
# pnpm test fails, the re-run passes under a command the classifier does not
# recognise (node_modules/.bin/jest — no runner prefix, no head match), and the
# agent reports EXIT=0. The recognised failure is the only attributable row, so
# it contradicts the token: unknown and a hold, never failure.
MIR_RID="$(start_run "$WORK/mirror.json")"
[ -n "$MIR_RID" ] && ok "mirror run started: $MIR_RID" || bad "mirror run failed to start"

wait_dispatch test \
  && ok "mirror suite node dispatched to test" \
  || bad "mirror suite node never dispatched"

mint_row test "suite failed" 1 "pnpm test"
mint_row test "suite passed on re-run" 0 "node_modules/.bin/jest --runInBand"
answer_as test "$(printf 'The first invocation was a typo; the suite passes — 2998 pass, 0 fail.\nEXIT=0')" \
  && ok "agent reported the passing re-run with its verdict token" \
  || bad "could not answer the mirror dispatch"

wait_node_outcome "$MIR_RID" suite unknown \
  && ok "mirror shape resolved unknown — a green suite was not recorded as failure" \
  || bad "mirror node outcome is $(node_outcome "$MIR_RID" suite), want unknown"

[ "$(node_outcome "$MIR_RID" suite)" != "failure" ] \
  && ok "mirror node is not failure (Defect 4 does not reproduce)" \
  || bad "mirror node recorded failure — Defect 4 is live"

[ "$(node_state "$MIR_RID" suite)" = "done" ] \
  && ok "mirror node terminal state is done, not failed" \
  || bad "mirror node state is $(node_state "$MIR_RID" suite), want done"

wait_lifecycle 'graph-outcome-conflict' \
  && ok "lifecycle records graph-outcome-conflict for the contradicting signals" \
  || bad "no graph-outcome-conflict row — the contradiction was resolved silently"

"$MUX" graph cancel "$MIR_RID" >/dev/null 2>&1
drain test
drain review

# Mirror negative control: when the re-run IS recognised, the newest
# attributable row agrees with the token and the node routes success. Without
# this, a deriveSendOutcome that always held would pass every check above.
MCTL_RID="$(start_run "$WORK/mirror.json")"
[ -n "$MCTL_RID" ] && ok "mirror control run started: $MCTL_RID" || bad "mirror control failed to start"
wait_dispatch test >/dev/null
mint_row test "suite failed" 1 "pnpm test"
mint_row test "suite passed on re-run" 0 "pnpm test"
answer_as test "$(printf 'Re-ran the suite; green.\nEXIT=0')" >/dev/null

wait_node_outcome "$MCTL_RID" suite success \
  && ok "recognised passing re-run routes success (mirror negative control)" \
  || bad "mirror control outcome is $(node_outcome "$MCTL_RID" suite), want success — the rule holds everything"

"$MUX" graph cancel "$MCTL_RID" >/dev/null 2>&1
settle
drain test
drain review

# --- 6. The cite road: c -> push-fixes -> d with a real commit -------------
"$MUX" graph validate "$WORK/cite.json" >/dev/null 2>&1 \
  && ok "cite-shape validates (its commit nodes are gated)" \
  || bad "cite-shape failed validation"

CITE_RID="$(start_run "$WORK/cite.json")"
[ -n "$CITE_RID" ] && ok "cite run started: $CITE_RID" || bad "cite run failed to start"

wait_node_state "$CITE_RID" gate2 waiting \
  && ok "gate2 reached waiting" \
  || bad "gate2 never reached waiting: $(node_state "$CITE_RID" gate2)"

as_approver graph approve "$CITE_RID" gate2 >/dev/null 2>&1 \
  && ok "authorized non-creator released gate2" \
  || bad "the gate approval was refused"

wait_dispatch review \
  && ok "c dispatched after the approval" \
  || bad "c never dispatched behind an approved gate"

peek review | grep -q 'verdict token' \
  && ok "c's dispatch seeds the verdict token (review is an unevidenced action)" \
  || bad "c's dispatch carries no seeded token — c would hold on every run"

answer_as review "$(printf 'Addressed all three review comments in the working tree.\nEXIT=0')" >/dev/null

wait_node_outcome "$CITE_RID" c success \
  && ok "c routed success on its seeded token, with no command to evidence it" \
  || bad "c outcome is $(node_outcome "$CITE_RID" c), want success"

wait_dispatch commit \
  && ok "push node dispatched after c" \
  || bad "push node never dispatched"

peek commit | grep -q 'verdict token' \
  && bad "the commit dispatch was seeded a token — a commit IS evidenced by its row (seed must not fire here)" \
  || ok "the commit dispatch is not seeded (negative control: a row can evidence it)"

# The commit really happens, so the row is attributable to the commit action —
# this is the citable sha, not a git command that merely ran.
mint_row commit "committed and pushed the review fixes" 0 "git commit -m 'Address review feedback'" \
  && ok "minted the git-commit row that evidences the push node" \
  || bad "could not mint the commit row"

answer_as commit "$(printf 'Committed and pushed as 4f2a91c.\nEXIT=0')" >/dev/null

wait_node_outcome "$CITE_RID" push success \
  && ok "push routed success on a row attributable to the commit action" \
  || bad "push outcome is $(node_outcome "$CITE_RID" push), want success"

wait_dispatch commit \
  && ok "d reached with a citable commit upstream" \
  || bad "d never dispatched — the c -> commit -> d road does not carry a sha to cite"

peek commit | grep -qi 'sha' \
  && ok "d's dispatch carries its cite-the-sha instruction" \
  || bad "d's dispatch does not mention the sha"

"$MUX" graph cancel "$CITE_RID" >/dev/null 2>&1

# --- Coverage floor --------------------------------------------------------
# 7 template + 1 daemon + 10 decline + 5 control + 2 prose + 9 mirror
# + 13 cite = 47. A section that dies early runs fewer, and a green summary
# over a short run is exactly the failure this floor catches — so the number is
# the arithmetic, not a comfortable margin under it.
total=$((pass + fail))
if [ "$total" -ge 47 ]; then
  ok "coverage floor met ($total checks executed)"
else
  bad "coverage floor NOT met — only $total checks executed, want >= 47 (a skipped section must not report green)"
fi

# --- Summary ---------------------------------------------------------------
echo ""
echo "  ${pass} passed, ${fail} failed"
[ "$fail" -eq 0 ] || exit 1
exit 0
