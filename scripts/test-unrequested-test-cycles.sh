#!/usr/bin/env bash
# Integration test for MUX-213: test runs nobody requested fire the review
# chain, and every review is answered twice.
#
# On 2026-11-13 a Codex test agent woken by each review reply re-ran the suite
# (its definition said "run tests on ANY message"); the passing run fired
# test→review, review answered test again, and the loop ran until the 30 s send
# dedup or a failure broke it — while edit received every review twice. The fix
# has three parts this script drives through the real binary, built from the
# tree: a test-chain review request names edit as its reply target
# (reply_role), a passing test run fires the chain only when a request stood
# behind it as it started (the PreToolUse guard records it, hook bash reads it
# back by tool_use_id), and the definitions run tests only on a request.
#
# The agents are stubs that act through the real hooks and bus CLI:
#   test stub   — reads its inbox with `muxcode inbox`; per message it runs the
#                 suite as a Claude agent would: PreToolUse `hook guard`, then
#                 its reply, then the async PostToolUse `hook bash` (so the
#                 reply always lands first — the review's race must-fix).
#                 "fixed" runs only for a request; "legacy" for ANY message.
#   review stub — "fixed" answers the role its request's `To reply` line
#                 names; "legacy" answers the sender and also sends edit, as
#                 the chain text told it to.
#
# Sections:
#   A  a review-complete reply delivered to the test agent: a fixed stub runs
#      nothing; a legacy stub's run sends review nothing (chain-withheld)
#   B  one test request, fixed stubs, scratch daemon: one run, one review
#      request (reply_role edit), one review, one notice to edit, nothing to
#      test, one daemon reviewed transition
#   C  negative control — the fix reverted in configuration: legacy stubs and
#      delivery-ack off (the gate's documented fail-open): the loop reproduces
#      and edit hears each review more than once
#   D  legacy stubs with delivery-ack on: the binary's gate alone stops the loop
#   E  a graph test node: it completes on the stub's reply, and with the async
#      hook landing after the node completed, the run stays graph-owned (the
#      ownership recorded at PreToolUse) — no test→review chain escapes
#   F  the live evaluation fails closed, run against a stub codex: a turn that
#      exits non-zero, is killed by the watchdog, emits no turn.completed or
#      emits turn.failed fails its case; only a completed turn is evidence
#   G  (MUX-216) a build→test request auto-CC'd to edit: edit consuming its
#      copy after test read the request leaves the receipt test's, and the run
#      still yields one review
#   H  (MUX-216) two runs under one open request: one review and one
#      chain-withheld naming the request; a distinct second request still gets
#      its own review; a failing run under spent requests still tells edit
#      Tests FAILED, requests no review and is not withheld. H-neg: with the spent marker removed between runs the
#      second review fires — the check can see it
#
# Live section (opt-in: --live or MUXCODE_UNREQUESTED_LIVE=1; spends model
# usage; outside the floor): a real `codex exec` acting as the test agent —
# role instructions from agents/test-runner.md, prompt from the real
# `hook prompt-submit` — in a scratch repo whose ./test.sh logs each run. Each
# case counts only once the turn completed (exit 0, no watchdog kill, a
# turn.completed event and no turn.failed). A request must start a run (positive control: the check can see
# one); a review reply, a no-answer timeout notice and an idle wake must start
# none.
#
# Usage: bash scripts/test-unrequested-test-cycles.sh [--live]
#   MUXCODE_UNREQUESTED_BIN=<path> tests a prebuilt binary instead of the tree.
set -uo pipefail

PASS=0
FAIL=0
LIVE_PASS=0
LIVE_FAIL=0
EXPECTED_PASS=55
LIVE=0
if [ "${1:-}" = --live ] || [ "${MUXCODE_UNREQUESTED_LIVE:-}" = 1 ]; then
  LIVE=1
fi
REAL_CODEX_HOME=${CODEX_HOME:-$HOME/.codex}

command -v jq >/dev/null 2>&1 || { echo "SKIP: jq is required"; exit 2; }
command -v tmux >/dev/null 2>&1 || { echo "SKIP: tmux is required"; exit 2; }

REPO=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
. "$REPO/scripts/lib/muxcode-bus-dir.sh"
WORK=$(mktemp -d /tmp/unrequested-cycles-XXXXXX)
SESSIONS=()
DPID=""

cleanup() {
  [ -n "$DPID" ] && kill "$DPID" 2>/dev/null
  local s
  for s in "${SESSIONS[@]:-}"; do
    [ -n "$s" ] || continue
    tmux kill-session -t "$s" 2>/dev/null
    rm -rf "$(muxcode_bus_dir "$MUX" "$s" 2>/dev/null)"
  done
  rm -rf "$WORK"
}
trap cleanup EXIT

if [ -n "${MUXCODE_UNREQUESTED_BIN:-}" ]; then
  MUX=$MUXCODE_UNREQUESTED_BIN
else
  command -v go >/dev/null 2>&1 || { echo "SKIP: go is required to build muxcode from the tree"; exit 2; }
  mkdir -p "$WORK/bin"
  (cd "$REPO/tools/muxcode" && go build -buildvcs=false -o "$WORK/bin/muxcode" .) \
    || { echo "SKIP: go build muxcode failed"; exit 2; }
  MUX="$WORK/bin/muxcode"
fi

ok()   { PASS=$((PASS + 1)); echo "  ok: $1"; }
fail() { FAIL=$((FAIL + 1)); echo "  FAIL: $1"; }

mkdir -p "$WORK/home" "$WORK/config" "$WORK/lifecycle"
export HOME="$WORK/home"
export XDG_STATE_HOME="$WORK/home/.local/state"
export MUXCODE_CONFIG_DIR="$WORK/config"
export MUXCODE_LIFECYCLE_LOG_DIR="$WORK/lifecycle"
export MUXCODE_DEDUP_WINDOW=0
export MUXCODE_RELAY_SUPPRESS_THRESHOLD=1000
export MUXCODE_FORCE_RESPOND_DISABLE=1
export MUXCODE_TMP_CLEANUP_THRESHOLD=0
export MUXCODE_BRANCH_TIME_DISABLE=1
export MUXCODE_TEST_CLI=claude MUXCODE_REVIEW_CLI=claude
unset MUXCODE_CONFIG MUXCODE_AGENT_CLI MUXCODE_TEST_PATTERNS MUXCODE_TEST_PRECHECK_PATTERNS \
  MUXCODE_DELIVERY_ACK MUXCODE_DELIVERY_ACK_DISABLE 2>/dev/null || true
cd "$WORK" || exit 1

# new_session NAME — a fresh scratch bus; sets BUS_SESSION, BUSDIR, LIFELOG.
new_session() {
  export BUS_SESSION="unrequested-$1-$$"
  SESSIONS+=("$BUS_SESSION")
  BUSDIR="$(muxcode_bus_dir "$MUX" "$BUS_SESSION")" || exit 1
  LIFELOG="$MUXCODE_LIFECYCLE_LOG_DIR/${BUS_SESSION}.log"
  "$MUX" init >/dev/null 2>&1
}

N=0
BETWEEN_HOOKS=""
# run_suite REQUEST_ID REPLY_TARGET [EXIT] — one suite run by the test stub; the
# reply (when there is a request to answer) lands before the async hook bash,
# and $BETWEEN_HOOKS, when set, runs in that gap. A non-zero EXIT completes the
# call as Claude does a failure: PostToolUseFailure, its error led by
# "Exit code N".
run_suite() {
  N=$((N + 1))
  local tid="toolu_mux213_$N" out
  out=$(jq -nc --arg t "$tid" '{hook_event_name:"PreToolUse",tool_name:"Bash",tool_use_id:$t,tool_input:{command:"./test.sh"}}' \
    | AGENT_ROLE=test "$MUX" hook guard 2>/dev/null)
  [ -n "$out" ] && echo "    (guard answered: $out)"
  if [ -n "$1" ]; then
    AGENT_ROLE=test "$MUX" send "$2" test "Tests passed: 2 modules, 0 failures. EXIT=0" \
      --type response --reply-to "$1" --no-notify >/dev/null 2>&1
  fi
  [ -n "$BETWEEN_HOOKS" ] && "$BETWEEN_HOOKS"
  if [ "${3:-0}" = 0 ]; then
    jq -nc --arg t "$tid" '{hook_event_name:"PostToolUse",tool_name:"Bash",tool_use_id:$t,tool_input:{command:"./test.sh"},
        tool_response:{stdout:"=== Summary ===\nmodules=2 ran=2 passed=2 failed=0",stderr:"",interrupted:false}}' \
      | AGENT_ROLE=test "$MUX" hook bash >/dev/null 2>&1
  else
    jq -nc --arg t "$tid" --arg e "Exit code $3"$'\n''=== Summary ===
modules=2 ran=2 passed=1 failed=1' \
      '{hook_event_name:"PostToolUseFailure",tool_name:"Bash",tool_use_id:$t,tool_input:{command:"./test.sh"},error:$e}' \
      | AGENT_ROLE=test "$MUX" hook bash >/dev/null 2>&1
  fi
}

# read_inbox ROLE — consume ROLE's inbox as its agent does, one TSV row per
# message: from, type, action, reply target (the `To reply` line), message id.
read_inbox() {
  AGENT_ROLE="$1" "$MUX" inbox 2>/dev/null | awk '
    /^--- Message from /     { from = $4 }
    /^Type: /                { type = $2; action = $4 }
    /^To reply: muxcode send / { print from "\t" type "\t" action "\t" $5 "\t" $NF }'
}

test_turn() {
  local mode=$1 from type action target id
  while IFS=$'\t' read -r from type action target id; do
    if [ "$type" = request ]; then
      run_suite "$id" "$target"
    elif [ "$mode" = legacy ]; then
      run_suite "" ""
    fi
  done < <(read_inbox test)
}

review_turn() {
  local mode=$1 from type action target id body="Review: 0 must-fix, 0 should-fix, 0 nits — LGTM EXIT=0"
  while IFS=$'\t' read -r from type action target id; do
    [ "$type" = request ] || continue
    if [ "$mode" = fixed ]; then
      AGENT_ROLE=review "$MUX" send "$target" review-complete "$body" --type response --reply-to "$id" --no-notify >/dev/null 2>&1
    else
      AGENT_ROLE=review "$MUX" send "$from" review-complete "$body" --type response --reply-to "$id" --no-notify >/dev/null 2>&1
      [ "$from" = edit ] || AGENT_ROLE=review "$MUX" send edit review-complete "$body (to edit)" \
        --type response --reply-to "$id" --no-notify >/dev/null 2>&1
    fi
  done < <(read_inbox review)
}

# cycles MODE COUNT — alternate test and review turns, as the two agents would.
cycles() {
  local i
  for i in $(seq 1 "$2"); do
    test_turn "$1"
    review_turn "$1"
  done
}

count_log() { jq -c "select($1)" "$BUSDIR/log.jsonl" 2>/dev/null | wc -l | tr -d ' '; }
runs()          { jq -c 'select(.command == "./test.sh")' "$BUSDIR/test-history.jsonl" 2>/dev/null | wc -l | tr -d ' '; }
review_reqs()   { count_log '.from == "test" and .to == "review" and .type == "request"'; }
reviews()       { count_log '.from == "review" and .type == "response" and .action == "review-complete"'; }
edit_reviews()  { jq -c 'select(.from == "review")' "$BUSDIR/inbox/edit.jsonl" 2>/dev/null | wc -l | tr -d ' '; }
withheld_rows() { grep '"event":"chain-withheld"' "$LIFELOG" 2>/dev/null | grep -c 'no unanswered request'; }
ask_test()      { AGENT_ROLE=edit "$MUX" send test test "Run ./test.sh and report" --no-notify >/dev/null 2>&1; }
review_reply_to_test() {
  AGENT_ROLE=review "$MUX" send test review-complete "Review: 0 must-fix, 0 should-fix, 0 nits — LGTM EXIT=0" \
    --type response --reply-to "1794610293-test-2d1b85a4" --no-notify >/dev/null 2>&1
}

echo "=== MUX-213: unrequested test cycles ==="

# ── A: a review reply delivered to the test agent ──────────────────
echo "-- A: review-complete reply delivered to the test agent"
new_session a
review_reply_to_test
test_turn fixed
[ "$(runs)" = 0 ] && ok "A: a request-only test agent starts no run for a review reply" \
  || fail "A: fixed stub ran $(runs) time(s) for a reply"
review_reply_to_test
test_turn legacy
[ "$(runs)" = 1 ] && ok "A: the legacy stub ran for the reply, as the Codex agent did (the fixture reaches the gate)" \
  || fail "A: legacy stub runs = $(runs), want 1"
[ "$(review_reqs)" = 0 ] && ok "A: that unrequested run sent review nothing" \
  || fail "A: unrequested run requested $(review_reqs) review(s)"
[ "$(withheld_rows)" = 1 ] && ok "A: chain-withheld row names the missing request" \
  || fail "A: chain-withheld rows = $(withheld_rows), want 1"
[ ! -s "$BUSDIR/inbox/review.jsonl" ] && ok "A: review's inbox stayed empty" \
  || fail "A: review inbox holds $(wc -l < "$BUSDIR/inbox/review.jsonl") message(s)"

# ── B: one test request, fixed stubs, scratch daemon ───────────────
echo "-- B: one test request with fixed stubs and a scratch daemon"
new_session b
tmux new-session -d -s "$BUS_SESSION" -n edit -x 120 -y 30
"$MUX" watch "$BUS_SESSION" --poll 1 >"$WORK/daemon-b.log" 2>&1 &
DPID=$!
sleep 1
kill -0 "$DPID" 2>/dev/null && ok "B: scratch daemon running (pid $DPID)" \
  || fail "B: scratch daemon exited: $(tail -3 "$WORK/daemon-b.log" 2>/dev/null)"
ask_test
cycles fixed 3
[ "$(runs)" = 1 ] && ok "B: one request → one run" || fail "B: runs = $(runs), want 1"
[ "$(review_reqs)" = 1 ] && ok "B: one review request — the reply landing before the async hook did not withhold it" \
  || fail "B: review requests = $(review_reqs), want 1"
[ "$(count_log '.from == "test" and .to == "review" and .reply_role == "edit"')" = 1 ] \
  && ok "B: the chain's review request names edit as its reply target" \
  || fail "B: review request lacks reply_role edit: $(jq -c 'select(.to == "review")' "$BUSDIR/log.jsonl" | head -1)"
[ "$(reviews)" = 1 ] && ok "B: one review" || fail "B: reviews = $(reviews), want 1"
[ "$(count_log '.from == "review" and .to == "edit"')" = 1 ] && ok "B: the review answered edit" \
  || fail "B: review→edit replies = $(count_log '.from == "review" and .to == "edit"')"
[ "$(count_log '.from == "review" and .to == "test"')" = 0 ] && ok "B: nothing from review reached test" \
  || fail "B: review→test messages = $(count_log '.from == "review" and .to == "test"')"
[ "$(edit_reviews)" = 1 ] && ok "B: edit holds the review exactly once (no auto-CC copy)" \
  || fail "B: edit holds $(edit_reviews) review message(s), want 1"
[ "$(withheld_rows)" = 0 ] && ok "B: no chain-withheld row for the requested run" \
  || fail "B: requested run withheld ($(withheld_rows) row(s))"
reviewed=0
for _ in $(seq 1 20); do
  reviewed=$(grep '"event":"reviewed"' "$LIFELOG" 2>/dev/null | grep -c 'daemon:review-complete')
  [ "$reviewed" -ge 1 ] && break
  sleep 0.5
done
sleep 2
reviewed=$(grep '"event":"reviewed"' "$LIFELOG" 2>/dev/null | grep -c 'daemon:review-complete')
[ "$reviewed" = 1 ] && ok "B: the daemon credited one review completion" \
  || fail "B: daemon reviewed transitions = $reviewed, want 1: $(tail -3 "$WORK/daemon-b.log" 2>/dev/null)"
kill "$DPID" 2>/dev/null; wait "$DPID" 2>/dev/null; DPID=""

# ── C: negative control — fix reverted in configuration ────────────
echo "-- C: negative control — legacy stubs and delivery-ack off (the gate fails open)"
new_session c
export MUXCODE_DELIVERY_ACK=off
ask_test
cycles legacy 4
unset MUXCODE_DELIVERY_ACK
[ "$(runs)" -ge 4 ] && ok "C: the test agent kept re-running ($(runs) runs for one request)" \
  || fail "C: runs = $(runs), want >= 4 — the script cannot see the loop"
[ "$(review_reqs)" -ge 3 ] && ok "C: the loop reproduces: $(review_reqs) review requests for one test request" \
  || fail "C: review requests = $(review_reqs), want >= 3 — the script cannot see the loop"
[ "$(count_log '.from == "review" and .to == "test"')" -ge 3 ] && ok "C: each review answered test, which started the next run" \
  || fail "C: review→test replies = $(count_log '.from == "review" and .to == "test"')"
[ "$(edit_reviews)" -gt "$(count_log '.from == "review" and .to == "edit"')" ] \
  && ok "C: edit holds more review messages ($(edit_reviews)) than review sent it — the auto-CC duplicate" \
  || fail "C: edit holds $(edit_reviews), review→edit sends $(count_log '.from == "review" and .to == "edit"')"
[ "$(withheld_rows)" = 0 ] && ok "C: with receipts off the gate fails open (no chain-withheld)" \
  || fail "C: chain-withheld rows = $(withheld_rows) with delivery-ack off"

# ── D: legacy stubs, delivery-ack on — the binary's gate alone ─────
echo "-- D: legacy stubs with delivery-ack on — the gate alone stops the loop"
new_session d
ask_test
cycles legacy 4
[ "$(review_reqs)" = 1 ] && ok "D: one review request despite a test agent that runs on every message" \
  || fail "D: review requests = $(review_reqs), want 1"
[ "$(runs)" -ge 2 ] && ok "D: the legacy stub still re-ran ($(runs) runs) — the gate, not the stub, held" \
  || fail "D: runs = $(runs), want >= 2 (the fixture never re-ran)"
[ "$(withheld_rows)" -ge 1 ] && ok "D: each unrequested rerun logged chain-withheld ($(withheld_rows))" \
  || fail "D: chain-withheld rows = $(withheld_rows)"
[ "$(count_log '.from == "review" and .to == "test"')" -ge 1 ] && ok "D: the legacy reviewer did answer test (the trigger was present)" \
  || fail "D: legacy reviewer sent test nothing"

# ── E: a graph test node ───────────────────────────────────────────
# The stub replies, then waits for the node to complete before its async hook
# bash fires — the worst order: the reply has closed the node's task, so only
# the ownership recorded at PreToolUse can keep the run out of the hook chain.
echo "-- E: a graph test node completes on its reply and its run stays graph-owned"
new_session e
tmux new-session -d -s "$BUS_SESSION" -n edit -x 120 -y 30
cat > "$WORK/graph-test.json" <<'EOF'
{"name": "mux213-graph-test", "start": "t",
 "nodes": [{"id": "t", "type": "send", "role": "test", "action": "test",
            "message": "Run ./test.sh as one standalone command and report"}],
 "edges": []}
EOF
RID=$("$MUX" graph run --file "$WORK/graph-test.json" 2>&1 | grep -o 'Started run [^ ]*' | awk '{print $3}')
[ -n "$RID" ] && ok "E: graph run started ($RID)" || fail "E: graph run did not start"
"$MUX" watch "$BUS_SESSION" --poll 1 >"$WORK/daemon-e.log" 2>&1 &
DPID=$!
sleep 1
kill -0 "$DPID" 2>/dev/null && ok "E: scratch daemon running (pid $DPID)" \
  || fail "E: scratch daemon exited: $(tail -3 "$WORK/daemon-e.log" 2>/dev/null)"

graph_line()  { "$MUX" graph status "$RID" 2>/dev/null | sed $'s/\x1b\\[[0-9;]*[A-Za-z]//g'; }
node_state()  { graph_line | awk -v n="$1" '$1==n {print $2}'; }
run_state()   { graph_line | head -1 | sed 's/.*\[\([a-z]*\)\].*/\1/'; }
DONE_BEFORE_HOOK=0
wait_node_done() {
  local i
  for i in $(seq 1 40); do
    if [ "$(node_state t)" = done ] && [ "$(run_state)" = complete ]; then
      DONE_BEFORE_HOOK=1
      return 0
    fi
    sleep 0.5
  done
  return 1
}
dispatched=0
for _ in $(seq 1 40); do
  AGENT_ROLE=test "$MUX" inbox --peek 2>/dev/null | grep -q 'Type: request' && dispatched=1 && break
  sleep 0.5
done
[ "$dispatched" = 1 ] && ok "E: the graph dispatched its test node to the test agent" \
  || fail "E: no test request dispatched: $(tail -3 "$WORK/daemon-e.log" 2>/dev/null)"
BETWEEN_HOOKS=wait_node_done
test_turn fixed
BETWEEN_HOOKS=""
[ "$DONE_BEFORE_HOOK" = 1 ] && ok "E: the run had completed before the async hook fired — the live ownership check could no longer see it" \
  || fail "E: the run was still live when the hook fired, so this section proves nothing about recorded ownership"
[ "$(node_state t)" = done ] && ok "E: the test node completed on the stub's EXIT=0 reply" \
  || fail "E: node t state $(node_state t), want done"
for _ in $(seq 1 20); do [ "$(run_state)" = complete ] && break; sleep 0.5; done
[ "$(run_state)" = complete ] && ok "E: the run completed" || fail "E: run state $(run_state), want complete"
[ "$(runs)" = 1 ] && ok "E: one run for the node" || fail "E: runs = $(runs), want 1"
[ "$(review_reqs)" = 0 ] && ok "E: no test→review chain escaped, though the hook landed after the node completed" \
  || fail "E: $(review_reqs) hook-chain review request(s) escaped graph ownership"
grep '"event":"chain-suppressed"' "$LIFELOG" 2>/dev/null | grep -q "graph run $RID owns test as node t" \
  && ok "E: chain-suppressed names the run and node that owned the test agent as the run started" \
  || fail "E: no chain-suppressed row for $RID"
[ "$(withheld_rows)" = 0 ] && ok "E: graph ownership, not the request gate, held the chain" \
  || fail "E: chain-withheld rows = $(withheld_rows)"
kill "$DPID" 2>/dev/null; wait "$DPID" 2>/dev/null; DPID=""

spent_rows() { grep '"event":"chain-withheld"' "$LIFELOG" 2>/dev/null | grep -c "request $1 already dispatched its review"; }
all_spent_rows() { grep '"event":"chain-withheld"' "$LIFELOG" 2>/dev/null | grep -c "already dispatched its review"; }
take_request() { read_inbox test | head -1 | cut -f5; }

# ── G: edit consumes the auto-CC copy before the run starts ────────
echo "-- G: edit consumes its auto-CC copy of the request before the run starts"
new_session g
AGENT_ROLE=build "$MUX" send test test "Run ./test.sh and report" --no-notify >/dev/null 2>&1
GID=$(jq -r 'select(.from == "build" and .to == "test") | .id' "$BUSDIR/log.jsonl" | head -1)
jq -e --arg id "$GID" 'select(.id == $id)' "$BUSDIR/inbox/edit.jsonl" >/dev/null 2>&1 \
  && ok "G: edit holds the auto-CC copy of build's request, under the same id" \
  || fail "G: no auto-CC copy of $GID in edit's inbox — the section proves nothing"
GROWS=$(read_inbox test)
read_inbox edit >/dev/null
[ "$(jq -r '.acked_by' "$BUSDIR/delivery/$GID.status" 2>/dev/null)" = test ] \
  && ok "G: edit's consume left the receipt test's" \
  || fail "G: receipt acked_by $(jq -r '.acked_by' "$BUSDIR/delivery/$GID.status" 2>/dev/null), want test"
while IFS=$'\t' read -r _ _ _ target id; do run_suite "$id" "$target"; done <<<"$GROWS"
review_turn fixed
[ "$(runs)" = 1 ] && ok "G: one run" || fail "G: runs = $(runs), want 1"
[ "$(review_reqs)" = 1 ] && ok "G: the requested run still requested one review" \
  || fail "G: review requests = $(review_reqs), want 1"
[ "$(withheld_rows)" = 0 ] && ok "G: no chain-withheld row — the request still stood behind the run" \
  || fail "G: chain-withheld rows = $(withheld_rows)"

# ── H: two runs under one open request ─────────────────────────────
# The agent reruns the suite before replying; review answers between runs, so
# its inbox holds no identical request that would mask a second dispatch.
echo "-- H: two runs under one open request yield one review"
new_session h
ask_test
HID=$(take_request)
run_suite "" ""
review_turn fixed
run_suite "" ""
review_turn fixed
[ "$(runs)" = 2 ] && ok "H: two runs under request $HID" || fail "H: runs = $(runs), want 2"
[ "$(review_reqs)" = 1 ] && ok "H: one review request for one test request" \
  || fail "H: review requests = $(review_reqs), want 1"
[ "$(spent_rows "$HID")" = 1 ] && ok "H: the second run logged chain-withheld naming request $HID" \
  || fail "H: spent chain-withheld rows for $HID = $(spent_rows "$HID"), want 1"
[ "$(withheld_rows)" = 0 ] && ok "H: neither run read as unrequested" \
  || fail "H: no-request chain-withheld rows = $(withheld_rows)"
# --force: edit's first request is still an in-flight task, which refuses a second test:test send.
H2SEND=$(AGENT_ROLE=edit "$MUX" send test test "Run ./test.sh for the follow-up change and report" --no-notify --force 2>&1 | tr '\n' ' ')
H2LOG=$(jq -c 'select(.to == "test") | [.from, .type, .action, .id]' "$BUSDIR/log.jsonl" 2>/dev/null | tail -2 | tr '\n' ' ')
H2ID=$(take_request)
run_suite "" ""
[ -n "$H2ID" ] && [ "$H2ID" != "$HID" ] && [ "$(review_reqs)" = 2 ] \
  && ok "H: a second, distinct request ($H2ID) still requested its own review" \
  || fail "H: second request '$H2ID' (send: $H2SEND; log to test: $H2LOG), review requests = $(review_reqs), want 2; spent: $(ls "$BUSDIR/spent-requests" 2>/dev/null | tr '\n' ' '); rows: $(grep -E '"event":"(chain-[a-z]+|workflow[a-z-]*)"' "$LIFELOG" 2>/dev/null | tail -3 | jq -r '.event + " " + .detail' 2>/dev/null | tr '\n' '|')"
# Failure control: every open request has now spent its review; a failing run
# under them must still reach edit, and the spent-request guard must not see it.
run_suite "" "" 3
[ "$(count_log '.from == "test" and .to == "edit" and .action == "notify" and (.payload | contains("Tests FAILED (exit 3)"))')" = 1 ] \
  && ok "H: a failing run under spent requests still told edit Tests FAILED (exit 3)" \
  || fail "H: Tests FAILED (exit 3) notices to edit = $(count_log '.from == "test" and .to == "edit" and .action == "notify"')"
[ "$(review_reqs)" = 2 ] && ok "H: the failing run requested no review" \
  || fail "H: review requests = $(review_reqs) after the failing run, want 2"
[ "$(all_spent_rows)" = 1 ] && ok "H: the failure was not withheld by the spent-request guard" \
  || fail "H: spent-request chain-withheld rows = $(all_spent_rows), want 1 (run 2's alone)"

# ── H negative control: the spent marker removed between runs ──────
echo "-- H-neg: negative control — the spent-request marker removed between runs"
new_session hn
ask_test
HNID=$(take_request)
run_suite "" ""
review_turn fixed
rm -rf "$BUSDIR/spent-requests"
run_suite "" ""
[ "$(review_reqs)" = 2 ] && ok "H-neg: without the spent marker the second run requests a second review — the check can see it" \
  || fail "H-neg: review requests = $(review_reqs), want 2 — the script cannot see a second dispatch"
[ "$(spent_rows "$HNID")" = 0 ] && ok "H-neg: and no spent chain-withheld row" \
  || fail "H-neg: spent rows = $(spent_rows "$HNID")"

# ── Live: a real codex test agent ──────────────────────────────────
lok()   { LIVE_PASS=$((LIVE_PASS + 1)); echo "  ok (live): $1"; }
lfail() { LIVE_FAIL=$((LIVE_FAIL + 1)); echo "  FAIL (live): $1"; }

# live_context CASE — what the real prompt-submit hook hands a codex test
# agent woken for CASE, from a fresh scratch bus on the codex hook road.
live_context() {
  new_session "live-$1"
  local env=(env MUXCODE_TEST_CLI=codex MUXCODE_CODEX_HOOKS=1 CODEX_HOME="$WORK/codex-ctx" AGENT_ROLE=test)
  (cd "$WORK/ctx" && "${env[@]}" "$MUX" agent config test >/dev/null 2>&1)
  case $1 in
    request)  AGENT_ROLE=edit "$MUX" send test test "Run ./test.sh as one standalone command and report" --no-notify >/dev/null 2>&1 ;;
    response) review_reply_to_test ;;
    notice)   AGENT_ROLE=daemon "$MUX" send test no-answer \
                "[daemon — NOT A RESPONSE: deploy went idle without answering task 1794602233-test-90c2a979]" \
                --type event --no-notify >/dev/null 2>&1 ;;
    idle)     ;;
  esac
  jq -nc '{hook_event_name:"UserPromptSubmit",prompt:"You have new messages"}' \
    | (cd "$WORK/ctx" && "${env[@]}" "$MUX" hook prompt-submit 2>/dev/null) \
    | jq -r '.hookSpecificOutput.additionalContext // empty'
}

LIVE_WATCHDOG=240

# live_setup — the scratch repo the codex test agent works in: a ./test.sh that
# logs each run, and the test-runner definition as its role file.
live_setup() {
  rm -rf "$WORK/live"
  mkdir -p "$WORK/ctx" "$WORK/live/.codex/test"
  : > "$WORK/live/ran.log"
  printf '#!/usr/bin/env bash\necho run >> "$(dirname "$0")/ran.log"\nprintf "=== Summary ===\\nmodules=1 ran=1 passed=1 failed=0\\n"\nexit 0\n' > "$WORK/live/test.sh"
  chmod +x "$WORK/live/test.sh"
  awk 'BEGIN{n=0} /^---$/ && n<2 {n++; next} n>=2' "$REPO/agents/test-runner.md" > "$WORK/live/.codex/test/AGENTS.md"
  git -C "$WORK/live" init -q && git -C "$WORK/live" add -A \
    && git -C "$WORK/live" -c user.email=t@t -c user.name=t commit -q -m base
}

# live_runs CASE — "ok <runs>" once the codex test agent completed its turn for
# CASE (exit 0, not killed by the watchdog, a `turn.completed` event and no
# `turn.failed` on its --json stream), with how many times it ran ./test.sh;
# otherwise "error <reason>". A turn that never completed decided nothing, so
# its run count is not evidence either way. A final message is not the test: a
# completed turn may end on a tool call with an empty one.
live_runs() {
  local ctx before after pid watcher rc want
  ctx=$(live_context "$1")
  case $1 in
    request)  want="Action: test" ;;
    response) want="Action: review-complete" ;;
    notice)   want="Action: no-answer" ;;
    idle)     want="Nothing is pending" ;;
  esac
  printf '%s' "$ctx" > "$WORK/live-$1.context"
  grep -qF "$want" <<<"$ctx" || { echo "error context lacks '$want'"; return; }
  before=$(wc -l < "$WORK/live/ran.log" | tr -d ' ')
  rm -f "$WORK/live-$1.last" "$WORK/live-$1.timedout"
  env -u TMUX -u TMUX_PANE BUS_SESSION="unrequested-live-exec-$$" AGENT_ROLE=test CODEX_HOME="$REAL_CODEX_HOME" \
    codex exec --json --skip-git-repo-check --ephemeral -C "$WORK/live" -s workspace-write -o "$WORK/live-$1.last" \
    "You have new messages"$'\n\n'"$ctx" >"$WORK/live-$1.log" 2>&1 &
  pid=$!
  ( sleep "$LIVE_WATCHDOG" && touch "$WORK/live-$1.timedout" && kill "$pid" 2>/dev/null ) &
  watcher=$!
  wait "$pid" 2>/dev/null
  rc=$?
  kill "$watcher" 2>/dev/null
  pkill -P "$watcher" 2>/dev/null
  after=$(wc -l < "$WORK/live/ran.log" | tr -d ' ')
  if [ -f "$WORK/live-$1.timedout" ]; then
    echo "error killed by the ${LIVE_WATCHDOG}s watchdog"
  elif [ "$rc" -ne 0 ]; then
    echo "error codex exited $rc"
  elif grep -q '"type":"turn.failed"' "$WORK/live-$1.log"; then
    echo "error the turn failed"
  elif ! grep -q '"type":"turn.completed"' "$WORK/live-$1.log"; then
    echo "error no turn.completed event — the turn did not complete"
  else
    echo "ok $((after - before))"
  fi
}

# live_verdict CASE — "pass <why>" or "fail <why>": a request must run the
# suite, every other case must complete its turn and run nothing.
live_verdict() {
  local r n
  r=$(live_runs "$1")
  case $r in
    "ok "*)
      n=${r#ok }
      if [ "$1" = request ]; then
        [ "$n" -ge 1 ] && echo "pass ran the suite ($n)" || echo "fail completed without running the suite"
      else
        [ "$n" = 0 ] && echo "pass completed its turn and ran nothing" || echo "fail ran the suite $n time(s)"
      fi ;;
    *) echo "fail ${r#error }" ;;
  esac
}

# ── F: the live evaluation fails closed (hermetic, stub codex) ──────
# A stub codex on PATH stands in for the model: it answers --version (so the
# hook road activates) and, per STUB_MODE, completes running the suite,
# completes running nothing, exits non-zero, hangs past the watchdog, exits 0
# with no turn.completed event, or reports turn.failed. Only a completed turn
# may count as evidence.
echo "-- F: the live evaluation counts only completed turns (stub codex)"
mkdir -p "$WORK/stubbin"
cat > "$WORK/stubbin/codex" <<'EOF'
#!/usr/bin/env bash
[ "${1:-}" = --version ] && { echo "codex-cli 0.162.1"; exit 0; }
out="" dir=""
while [ $# -gt 0 ]; do
  case $1 in
    -o) out=$2; shift 2 ;;
    -C) dir=$2; shift 2 ;;
    *) shift ;;
  esac
done
echo '{"type":"turn.started"}'
case ${STUB_MODE:-} in
  run)    "$dir/test.sh" >/dev/null; echo "Tests passed. EXIT=0" > "$out"; echo '{"type":"turn.completed"}' ;;
  norun)  : > "$out"; echo '{"type":"turn.completed"}' ;;
  fail)   echo "error: 401 Unauthorized" >&2; exit 3 ;;
  hang)   sleep 30 ;;
  silent) ;;
  failed) echo '{"type":"turn.failed","error":{"message":"stream disconnected"}}' ;;
esac
EOF
chmod +x "$WORK/stubbin/codex"
SAVED_PATH=$PATH
SAVED_WATCHDOG=$LIVE_WATCHDOG
export PATH="$WORK/stubbin:$PATH"
LIVE_WATCHDOG=3
live_setup
stub_verdict() { STUB_MODE=$1; export STUB_MODE; live_verdict "$2"; unset STUB_MODE; }
v=$(stub_verdict run request)
[ "${v%% *}" = pass ] && ok "F: a completed request turn that ran the suite passes ($v)" || fail "F: request control: $v"
v=$(stub_verdict norun idle)
[ "${v%% *}" = pass ] && ok "F: a completed turn that ran nothing passes ($v)" || fail "F: clean negative: $v"
v=$(stub_verdict fail notice)
case $v in "fail codex exited 3"*) ok "F: a codex that exits non-zero fails the case, not passes it ($v)" ;;
  *) fail "F: a failed codex counted as a pass: $v" ;; esac
v=$(stub_verdict hang response)
case $v in "fail killed by the"*) ok "F: a turn killed by the watchdog fails the case ($v)" ;;
  *) fail "F: a timed-out codex counted as a pass: $v" ;; esac
v=$(stub_verdict silent idle)
case $v in "fail no turn.completed"*) ok "F: exit 0 with no turn.completed event fails the case ($v)" ;;
  *) fail "F: an incomplete turn counted as a pass: $v" ;; esac
v=$(stub_verdict failed notice)
case $v in "fail the turn failed"*) ok "F: a turn.failed event fails the case ($v)" ;;
  *) fail "F: a failed turn counted as a pass: $v" ;; esac
export PATH="$SAVED_PATH"
LIVE_WATCHDOG=$SAVED_WATCHDOG

if [ "$LIVE" = 1 ]; then
  echo "-- live: a real codex test agent (spends model usage)"
  if ! command -v codex >/dev/null 2>&1; then
    lfail "codex is not on PATH"
  else
    live_setup
    for c in request response notice idle; do
      v=$(live_verdict "$c")
      if [ "${v%% *}" = pass ]; then
        lok "$c: ${v#pass }"
      else
        lfail "$c: ${v#fail } — $(tail -5 "$WORK/live-$c.log" 2>/dev/null | tr '\n' ' ')"
      fi
    done
  fi
else
  echo "  SKIP live: pass --live or set MUXCODE_UNREQUESTED_LIVE=1 to run a real codex test agent (spends model usage)"
fi

# ── Coverage floor ─────────────────────────────────────────────────
echo
echo "=== results: $PASS passed, $FAIL failed (floor $EXPECTED_PASS), $LIVE_PASS live passed, $LIVE_FAIL live failed ==="
[ "$LIVE_FAIL" -eq 0 ] || exit 1
if [ "$PASS" -ne "$EXPECTED_PASS" ]; then
  echo "  FAIL  coverage floor: expected exactly $EXPECTED_PASS passes, got $PASS — a section was skipped or double-counted"
  exit 1
fi
[ "$FAIL" -eq 0 ] || exit 1
exit 0
