#!/usr/bin/env bash
# Integration test for MUX-192: a stale in-flight task never starves a codex
# agent's wake, and diagnose names the task instead of a constant IsIdle.
#
# On 2026-09-25 the codex review agent sat idle at its composer for minutes
# with a request pending: SendWakeUp refused every wake while any in-flight task
# to the role was over 5s old — including edit's review task, answered under the
# chain's request id and so never closed — and the receipt-gap backstop called
# the same refusable wake every poll. `diagnose` reported "IsAgentIdle: false",
# a constant for codex, and never named the task.
#
# Hermetic: a scratch bus session, a scratch tmux session and (section 5) a
# real scratch daemon. Each fake agent pane is static and non-echoing
# (`stty -echo -icanon; cat <frame>; exec cat -u >> typed-<role>`), so the pane
# shows exactly the fixture frame while every byte typed into it lands in a
# file: an injection is proven by the file, never by redrawing the pane.
#
#   1  unforced wake (CLI Notify) past a stale, answered in-flight task → typed
#   2  negative control: a fresh unanswered task (< grace) → skipped, one row
#   3  negative control: a mid-turn pane → nothing typed, unforced or forced
#   4  diagnose --json: the starved state names the task, no IsAgentIdle; a
#      fresh blocking task yields wake-blocked-by-task naming it
#   5  daemon: the answered task leaves in-flight (task-answered-elsewhere),
#      an unrelated-action reply does not close its task; the backstop skips a
#      mid-turn pane with one row per episode, then delivers within one
#      interval once the pane is idle
#
# Requires installed muxcode with MUX-192 Phase 3 (run ./build.sh first).
set -uo pipefail

PASS=0
FAIL=0
EXPECTED_PASS=36

command -v tmux >/dev/null 2>&1 || { echo "SKIP: tmux is required"; exit 2; }
command -v jq >/dev/null 2>&1 || { echo "SKIP: jq is required"; exit 2; }
command -v perl >/dev/null 2>&1 || { echo "SKIP: perl is required"; exit 2; }
command -v muxcode >/dev/null 2>&1 || { echo "SKIP: muxcode not installed"; exit 2; }
MUX=$(command -v muxcode)
REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REPO_BEFORE=$(ls -A "$REPO")
. "$(dirname "${BASH_SOURCE[0]}")/lib/muxcode-version.sh"
require_muxcode_version "$MUX" v0.1.8 MUX-192 || { echo "  FAIL  binary precondition not met"; exit 1; }

SESSION="codex-idle-test-$$"
export BUS_SESSION="$SESSION"
export AGENT_ROLE=edit BUS_ROLE=edit
WORK=$(mktemp -d /tmp/codex-idle-XXXXXX)
export MUXCODE_LIFECYCLE_LOG_DIR="$WORK/lifecycle"
export MUXCODE_SESSION_REPO_DIR="$WORK/repo"
# Per-role CLIs are pinned: a caller's live-session MUXCODE_<ROLE>_CLI outranks
# MUXCODE_AGENT_CLI. test is a non-hook provider so its chain request is not
# refused by the hook-road send policy.
export MUXCODE_AGENT_CLI=claude MUXCODE_EDIT_CLI=claude MUXCODE_RUN_CLI=claude MUXCODE_PLAN_CLI=claude
export MUXCODE_REVIEW_CLI=codex MUXCODE_BUILD_CLI=codex MUXCODE_TEST_CLI=opencode
export MUXCODE_CODEX_HOOKS=0 MUXCODE_REVIEW_CODEX_HOOKS=0 MUXCODE_BUILD_CODEX_HOOKS=0
export MUXCODE_DEDUP_WINDOW=0
export MUXCODE_TMP_CLEANUP_THRESHOLD=0
export MUXCODE_SEND_DEFAULT_FIRE_AND_FORGET=1
BUSDIR="/tmp/muxcode-bus-$SESSION"

DPID=""
cleanup() {
  [ -n "$DPID" ] && kill "$DPID" 2>/dev/null || true
  pkill -f "watch $SESSION" 2>/dev/null || true
  tmux kill-session -t "$SESSION" 2>/dev/null || true
  rm -rf "$BUSDIR" "$WORK"
}
trap cleanup EXIT

ok()   { PASS=$((PASS + 1)); echo "  ok: $1"; }
fail() { FAIL=$((FAIL + 1)); echo "  FAIL: $1"; }
check() { if eval "$2"; then ok "$1"; else fail "$1"; fi; }

# lifecycle_row <event> <detail-substring> — rows of exactly that event.
lifecycle_row() { cat "$WORK/lifecycle"/* 2>/dev/null | grep -F "\"event\":\"$1\"" | grep -cF -- "$2"; }
task_status() { sed -n 's/.*"status": *"\([^"]*\)".*/\1/p' "$BUSDIR/tasks/$1.json" 2>/dev/null | head -1; }
in_inbox() { "$MUX" inbox --role "$1" --peek 2>/dev/null | grep -q -- "$2"; }
typed() { grep -q -- "$2" "$WORK/typed-$1.txt" 2>/dev/null; }
typed_bytes() { wc -c <"$WORK/typed-$1.txt" 2>/dev/null | tr -d ' '; }

# render <role> <line>... — replace the role's agent pane with a static frame
# that records every typed byte to typed-<role>.txt.
render() {
  local role=$1; shift
  printf '%s\n' "$@" >"$WORK/pane-$role.txt"
  tmux respawn-pane -k -t "$SESSION:$role.1" -c "$WORK" \
    "stty -echo -icanon; cat '$WORK/pane-$role.txt'; exec cat -u >> '$WORK/typed-$role.txt'"
  sleep 0.3
}

# send_as <role> <send args...> — a bus send under another role's identity.
send_as() { local who=$1; shift; AGENT_ROLE=$who BUS_ROLE=$who "$MUX" send "$@"; }
# track_as <role> <send args...> — a tracked request; prints the task id.
track_as() { send_as "$@" --track 2>/dev/null | awk '/Tracking task/ {print $3}'; }
# consume_as <role> — the agent reads its whole inbox.
consume_as() { AGENT_ROLE=$1 BUS_ROLE=$1 "$MUX" inbox --role "$1" >/dev/null 2>&1; }

# backdate_task <id> <secs> / backdate_msg <role> <marker> <secs> — age a
# fixture without sleeping: sent_at and ts are what every age check reads.
backdate_task() {
  perl -pi -e 's/"sent_at":(\d+)/"\"sent_at\":".($1-'"$2"')/e' "$BUSDIR/tasks/$1.json"
}
backdate_msg() {
  MARK="$2" perl -pi -e 'if (index($_, $ENV{MARK}) >= 0) { s/"ts":(\d+)/"\"ts\":".($1-'"$3"')/e }' "$BUSDIR/inbox/$1.jsonl"
}

diagnose_json() { "$MUX" diagnose "$1" --json 2>/dev/null || true; }

IDLE_1058=("• The inbox is empty; nothing is pending." "" "  Worked for 1m 7s · 11:01 AM" ""
  "› Ask Codex to do anything" ""
  "  GPT-6-Astra medium · ~/Repos/mkober/muxcode · Review new messages ⚠ 1 warning · f2 to view")
# Nothing above the composer: the running daemon's scrape reads no result here.
IDLE_BARE=("› Ask Codex to do anything" ""
  "  GPT-6-Astra medium · ~/work · Review new messages ⚠ 1 warning · f2 to view")
WORKING=("• Working (12s • esc to interrupt)" "" "› Ask Codex to do anything" ""
  "  GPT-6-Astra medium · ~/work")

echo "=== a stale in-flight task never starves a codex wake (MUX-192) ==="

mkdir -p "$WORK/repo"
# Panes rooted in $WORK, never the checkout (see test-stall-redrive-busy.sh).
tmux new-session -d -s "$SESSION" -n review -x 200 -y 40 -c "$WORK"
tmux split-window -h -t "$SESSION:review" -c "$WORK"
tmux new-window -t "$SESSION" -n build -c "$WORK"
tmux split-window -h -t "$SESSION:build" -c "$WORK"
"$MUX" init "$SESSION" >/dev/null 2>&1 || true

if ! diagnose_json review | jq -e 'has("in_flight_tasks")' >/dev/null 2>&1; then
  echo "  FAIL  installed muxcode predates MUX-192 Phase 3 (no in_flight_tasks in diagnose --json) — run ./build.sh"
  exit 1
fi

# ── 1: the unforced wake passes a stale, answered task ───────────
echo "-- 1: stale answered task, unforced wake"
render review "${IDLE_1058[@]}"
CHAIN_ID=$(track_as test review review "MUX192-chain-request" --no-notify)
T1=$(track_as edit review review "MUX192-edit-review" --force --no-notify)
if [ -z "$CHAIN_ID" ] || [ -z "$T1" ]; then
  echo "  FAIL  could not create the fixture requests (chain=$CHAIN_ID edit=$T1)"
  exit 1
fi
consume_as review
backdate_task "$T1" 300
send_as review edit review-complete "MUX192 review done: 0 must-fix" --type response --reply-to "$CHAIN_ID" --no-notify >/dev/null 2>&1
check "fixture: edit's task is answered in fact and in flight on disk" '[ "$(task_status "$T1")" = in-flight ]'

wake1=$(send_as edit review review "MUX192-pending-one" --force 2>&1)
sleep 1
check "the pending request is typed into the idle pane" 'typed review MUX192-pending-one'
check "the request left the inbox" '! in_inbox review MUX192-pending-one'
check "no [wakeup] skipping line" '! printf "%s" "$wake1" | grep -q "wakeup\] skipping"'
check "no wake-skipped row for review" '[ "$(lifecycle_row wake-skipped "review:")" = 0 ]'
check "the wake went through with the task still in flight (no daemon closed it)" '[ "$(task_status "$T1")" = in-flight ]'

# ── 2: a fresh unanswered task still holds the wake ──────────────
echo "-- 2: fresh unanswered task (negative control)"
render build "${IDLE_BARE[@]}"
T2=$(track_as edit build build "MUX192-fresh-task" --force --no-notify)
consume_as build
wake2=$(send_as edit build build "MUX192-pending-two" --force 2>&1)
sleep 1
check "the wake is skipped naming the in-flight task" 'printf "%s" "$wake2" | grep -q "skipping build injection — in-flight task"'
check "nothing typed into the build pane" '! typed build MUX192-pending-two'
check "the request stays in the inbox" 'in_inbox build MUX192-pending-two'
check "one wake-skipped row naming the task" '[ "$(lifecycle_row wake-skipped "$T2")" = 1 ]'

# ── 3: a mid-turn pane is never typed into, on either road ───────
echo "-- 3: mid-turn pane (negative control)"
render build "${WORKING[@]}"
# Past the grace, so the task gate cannot be what holds the wake.
sleep 6
before3=$(typed_bytes build)
send_as edit build build "MUX192-pending-three" --force >/dev/null 2>&1
sleep 1
check "unforced road: one wake-skipped row naming mid-turn" '[ "$(lifecycle_row wake-skipped "build: agent is mid-turn")" = 1 ]'
deliver3=$("$MUX" deliver build --force 2>&1)
check "forced road: deliver --force refuses the mid-turn pane" 'printf "%s" "$deliver3" | grep -q "mid-turn"'
check "nothing typed into the mid-turn pane on either road" '[ "$(typed_bytes build)" = "$before3" ]'
check "both requests stay in the inbox" 'in_inbox build MUX192-pending-two && in_inbox build MUX192-pending-three'
consume_as build

# ── 4: diagnose names the task, not a constant ───────────────────
echo "-- 4: diagnose --json"
render review "${IDLE_1058[@]}"
send_as edit review review "MUX192-starved-request" --force --no-notify >/dev/null 2>&1
backdate_msg review MUX192-starved-request 181
diag=$(diagnose_json review)
check "in_flight_tasks names edit's task" 'printf "%s" "$diag" | jq -e --arg id "$T1" ".in_flight_tasks[] | select(.id == \$id)" >/dev/null'
check "the task carries its answer (answered_by)" 'printf "%s" "$diag" | jq -e --arg id "$T1" ".in_flight_tasks[] | select(.id == \$id) | .answered_by | length > 0" >/dev/null'
check "the answered task does not block the wake" 'printf "%s" "$diag" | jq -e --arg id "$T1" ".in_flight_tasks[] | select(.id == \$id) | .blocks_wake == false" >/dev/null'
check "no active-with-stale-messages finding" '! printf "%s" "$diag" | jq -e ".findings[] | select(.failure_mode == \"active-with-stale-messages\")" >/dev/null'
check "no IsAgentIdle evidence anywhere" '! printf "%s" "$diag" | grep -q IsAgentIdle'
check "the stuck request is still explained (receipt-gap)" 'printf "%s" "$diag" | jq -e ".findings[] | select(.failure_mode == \"receipt-gap\")" >/dev/null'

# A fresh unanswered task whose request left the inbox is a real blocker; the
# whole block runs inside the 5s grace.
T4=$(track_as edit review review "MUX192-blocking-task" --force --no-notify)
consume_as review
send_as edit review review "MUX192-blocked-request" --force --no-notify >/dev/null 2>&1
backdate_msg review MUX192-blocked-request 181
diag4=$(diagnose_json review)
check "wake-blocked-by-task names the fresh task" 'printf "%s" "$diag4" | jq -e --arg id "$T4" ".findings[] | select(.failure_mode == \"wake-blocked-by-task\") | .summary | contains(\$id)" >/dev/null'
check "its remediation names deliver --force" 'printf "%s" "$diag4" | jq -e ".findings[] | select(.failure_mode == \"wake-blocked-by-task\") | .remediation | join(\" \") | contains(\"muxcode deliver review --force\")" >/dev/null'
check "blocking report carries no IsAgentIdle evidence" '! printf "%s" "$diag4" | grep -q IsAgentIdle'

# ── 5: the daemon closes the stale task and the backstop recovers ─
echo "-- 5: daemon"
render review "${WORKING[@]}"
T5=$(track_as edit review security-review "MUX192-unrelated-task" --force --no-notify)
send_as review edit plan "MUX192 unrelated reply" --type response --reply-to "$CHAIN_ID" --no-notify >/dev/null 2>&1
check "fixture: edit's review task still in flight before the daemon" '[ "$(task_status "$T1")" = in-flight ]'
before5=$(typed_bytes review)

(cd "$WORK/repo" && exec "$MUX" watch "$SESSION" --poll 2) >>"$WORK/daemon.log" 2>&1 &
DPID=$!
sleep 10
kill -0 "$DPID" 2>/dev/null && ok "scratch daemon running" || fail "scratch daemon started"
check "edit's answered task left in-flight (completed)" '[ "$(task_status "$T1")" = completed ]'
check "task-answered-elsewhere row names the task" '[ "$(lifecycle_row task-answered-elsewhere "$T1")" = 1 ]'
check "control: the unrelated-action reply closes nothing" '[ "$(task_status "$T5")" = in-flight ] && [ "$(lifecycle_row task-answered-elsewhere "$T5")" = 0 ]'

# Two more 15s backstop sweeps against the mid-turn pane.
sleep 30
check "backstop: nothing typed into the mid-turn pane" '[ "$(typed_bytes review)" = "$before5" ]'
check "backstop: one delivery-gap-skip row for the episode" '[ "$(lifecycle_row delivery-gap-skip "review:")" = 1 ]'
check "backstop: the blocked request still pending" 'in_inbox review MUX192-blocked-request'

render review "${IDLE_BARE[@]}"
# One backstop interval (15s) plus a poll tick.
sleep 18
check "backstop: the request is typed once the pane is idle" 'typed review MUX192-blocked-request'
check "backstop: the request left the inbox" '! in_inbox review MUX192-blocked-request'
check "backstop: delivered by force-deliver" '[ "$(lifecycle_row force-deliver "review:")" -ge 1 ]'
check "backstop: still at most one delivery-gap-skip row" '[ "$(lifecycle_row delivery-gap-skip "review:")" -le 1 ]'

if [ "$(ls -A "$REPO")" = "$REPO_BEFORE" ]; then
  ok "the checkout is unchanged"
else
  fail "the checkout gained entries: $(comm -13 <(printf '%s\n' "$REPO_BEFORE" | sort) <(ls -A "$REPO" | sort) | tr '\n' ' ')"
fi

if [ "$FAIL" -ne 0 ]; then
  echo "--- daemon log tail ---"
  tail -20 "$WORK/daemon.log" 2>/dev/null
  echo "--- lifecycle events ---"
  cat "$WORK/lifecycle"/* 2>/dev/null | grep -E "wake-|task-|deliver|delivery-gap" | tail -25
fi

echo
echo "=== results: $PASS passed, $FAIL failed (floor $EXPECTED_PASS) ==="
if [ "$FAIL" -ne 0 ]; then
  exit 1
fi
if [ "$PASS" -ne "$EXPECTED_PASS" ]; then
  echo "  FAIL  coverage floor: expected exactly $EXPECTED_PASS passes, got $PASS — a section was skipped or double-counted"
  exit 1
fi
echo "PASS"
