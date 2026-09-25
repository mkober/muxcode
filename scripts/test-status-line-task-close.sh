#!/usr/bin/env bash
# Integration test for MUX-154: a status line never closes a tracked task.
#
# On 2026-09-08 a pane-scraped `• Working (13s • esc to interrupt)` line (and,
# later, codex's 158-dash turn separator) was synthesized as the response to
# real requests: the task read as succeeded, MarkResponded drained the request,
# verify-spec fired on a review nobody had written, and `deliver --force` then
# reported nothing pending — the recovery disarmed by the defect it recovers.
#
# Hermetic: a scratch bus session, a scratch tmux session and a real scratch
# daemon. Each fake agent pane is a static, non-echoing process (`stty -echo;
# cat <fixture>; sleep`), so the pane shows exactly the fixture and a delivered
# payload can neither execute nor redraw it. Roles:
#
#   review  codex scrape road  the 14:12:55 working line above a › composer
#   build   codex scrape road  the 20:31:51 rule line above a › composer
#   test    opencode           the working line above a ▣ stop marker — detection
#                              calls it complete, so only the daemon's consumer
#                              refusal (task-nonresult-ignored) stands between it
#                              and a synthesized response
#
# Phase A: the non-result panes leave every task in flight, the request in the
# inbox, and the review chain (StateReviewed → verify-spec) unfired.
# Phase B: `deliver --force` still has the request to deliver.
# Phase C (negative control — the guard cannot go inert): genuine panes complete
# every task and a genuine review reply fires verify-spec.
#
# Requires installed muxcode >= v0.1.8 (run ./build.sh first).
set -uo pipefail

PASS=0
FAIL=0
EXPECTED_PASS=19

command -v tmux >/dev/null 2>&1 || { echo "SKIP: tmux is required"; exit 2; }
command -v muxcode >/dev/null 2>&1 || { echo "SKIP: muxcode not installed"; exit 2; }
MUX=$(command -v muxcode)
REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REPO_BEFORE=$(ls -A "$REPO")
. "$(dirname "${BASH_SOURCE[0]}")/lib/muxcode-version.sh"
require_muxcode_version "$MUX" v0.1.8 MUX-154 || { echo "  FAIL  binary precondition not met"; exit 1; }

SESSION="status-line-test-$$"
export BUS_SESSION="$SESSION"
export AGENT_ROLE=edit BUS_ROLE=edit
WORK=$(mktemp -d /tmp/status-line-XXXXXX)
export MUXCODE_LIFECYCLE_LOG_DIR="$WORK/lifecycle"
export MUXCODE_SESSION_REPO_DIR="$WORK/repo"
# Per-role CLIs are pinned: a caller's live-session MUXCODE_<ROLE>_CLI outranks
# MUXCODE_AGENT_CLI and would judge these fixtures with the wrong detector.
export MUXCODE_AGENT_CLI=claude MUXCODE_EDIT_CLI=claude MUXCODE_RUN_CLI=claude MUXCODE_PLAN_CLI=claude
export MUXCODE_REVIEW_CLI=codex MUXCODE_BUILD_CLI=codex MUXCODE_TEST_CLI=opencode
export MUXCODE_CODEX_HOOKS=0 MUXCODE_REVIEW_CODEX_HOOKS=0 MUXCODE_BUILD_CODEX_HOOKS=0
export MUXCODE_DEDUP_WINDOW=0
export MUXCODE_TMP_CLEANUP_THRESHOLD=0
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

lifecycle_row() { cat "$WORK/lifecycle"/* 2>/dev/null | grep -- "$1" | grep -c -- "$2"; }
task_status() { sed -n 's/.*"status": *"\([^"]*\)".*/\1/p' "$BUSDIR/tasks/$1.json" 2>/dev/null | head -1; }
in_inbox() { "$MUX" inbox --role "$1" --peek 2>/dev/null | grep -q -- "$2"; }
verify_spec_count() { "$MUX" inbox --role plan --peek 2>/dev/null | grep -c "verify-spec"; }

# render <role> <line>... — replace the role's agent pane with a static frame.
render() {
  local role=$1; shift
  printf '%s\n' "$@" >"$WORK/pane-$role.txt"
  tmux respawn-pane -k -t "$SESSION:$role.1" -c "$WORK" \
    "stty -echo; cat '$WORK/pane-$role.txt'; exec sleep 100000"
}

# track <role> <marker> — a tracked edit→role request; prints the task id.
track() {
  "$MUX" send "$1" "$1" "$2" --track --no-notify 2>/dev/null | awk '/Tracking task/ {print $3}'
}

# start_daemon runs watch from the scratch repo: MUXCODE_SESSION_REPO_DIR does
# not move the process cwd, and a non-hook edit CLI makes checkNonHookEdits
# git-diff whatever checkout the daemon sits in. exec keeps DPID the daemon.
start_daemon() {
  # A concurrent ./build.sh runs upgrade-daemons, which re-execs every daemon.
  pkill -f "watch $SESSION" 2>/dev/null || true
  sleep 1
  (cd "$WORK/repo" && exec "$MUX" watch "$SESSION" --poll 2) >>"$WORK/daemon.log" 2>&1 &
  DPID=$!
  sleep 1
}

WORKING="• Working (13s • esc to interrupt)"
RULE=$(printf '─%.0s' $(seq 158))
STOP="▣  Test · gpt-5 · 3.2s"

echo "=== status line never closes a tracked task (MUX-154) ==="

mkdir -p "$WORK/repo/docs/requirements/drafts"
printf '# Fixture Spec\n\n- [ ] item\n' >"$WORK/repo/docs/requirements/drafts/fixture-spec.md"

# Panes rooted in $WORK, never the checkout (see test-stall-redrive-busy.sh).
tmux new-session -d -s "$SESSION" -n review -x 160 -y 40 -c "$WORK"
tmux split-window -h -t "$SESSION:review" -c "$WORK"
for role in build test; do
  tmux new-window -t "$SESSION" -n "$role" -c "$WORK"
  tmux split-window -h -t "$SESSION:$role" -c "$WORK"
done
"$MUX" init "$SESSION" >/dev/null 2>&1 || true
(cd "$WORK/repo" && "$MUX" spec set docs/requirements/drafts/fixture-spec.md >/dev/null 2>&1) || true

render review "$WORKING" "" "› "
render build "Running gofmt" "$RULE" "" "› "
render test "$WORKING" "$STOP"

REVIEW_ID=$(track review "MUX154-review-request")
BUILD_ID=$(track build "MUX154-build-request")
TEST_ID=$(track test "MUX154-test-request")
if [ -z "$REVIEW_ID" ] || [ -z "$BUILD_ID" ] || [ -z "$TEST_ID" ]; then
  echo "  FAIL  could not create tracked requests (review=$REVIEW_ID build=$BUILD_ID test=$TEST_ID)"
  exit 1
fi
# Past the providers' 5s in-flight skip before the daemon first looks, so its
# ordinary wake-up cannot inject (and consume) the requests under test.
sleep 6

# ── Phase A: non-result panes close nothing ──────────────────────
echo "-- progress line and rule line"
start_daemon
kill -0 "$DPID" 2>/dev/null && ok "scratch daemon running" || fail "scratch daemon started"
# Grace (5s from send, 3s from first sighting) plus two 5s scrape ticks.
sleep 16

check "review task (working line) still in flight" '[ "$(task_status "$REVIEW_ID")" = in-flight ]'
check "review request still in the inbox" 'in_inbox review MUX154-review-request'
check "no task-detected row for review" '[ "$(lifecycle_row task-detected "review task")" = 0 ]'
check "build task (rule line) still in flight" '[ "$(task_status "$BUILD_ID")" = in-flight ]'
check "no task-detected row for build" '[ "$(lifecycle_row task-detected "build task")" = 0 ]'
check "test task (opencode, stop marker) still in flight" '[ "$(task_status "$TEST_ID")" = in-flight ]'
check "task-nonresult-ignored row written for test" '[ "$(lifecycle_row task-nonresult-ignored "test task")" -ge 1 ]'
check "review chain not fired: no verify-spec at plan" '[ "$(verify_spec_count)" = 0 ]'
check "review chain not fired: no plan-verify row" '[ "$(lifecycle_row "\"event\":\"plan-verify\"" "fixture-spec")" = 0 ]'

# ── Phase B: the recovery path is still armed ────────────────────
echo "-- deliver --force"
# An idle codex frame: nothing composed above the composer, so the running
# daemon still reads it as not complete.
render review "› Ask Codex to do anything" "  gpt-5 medium · ~/work"
sleep 1
deliver_out=$("$MUX" deliver review --force 2>&1)
check "deliver --force found the request pending" 'printf "%s" "$deliver_out" | grep -q "woke review with 1 pending"'
check "delivered from the inbox (force-deliver), not re-driven from a drained task" \
  '[ "$(lifecycle_row force-deliver "review:")" -ge 1 ] && [ "$(lifecycle_row force-redrive "review:")" = 0 ]'

# ── Phase C: genuine replies still complete (negative control) ───
echo "-- genuine replies"
render review "Review finished: 0 must-fix, 0 should-fix, 0 nits EXIT=0" "Sent response:response to edit" "› "
render build "Build finished: EXIT=0" "Sent response:response to edit" "› "
render test "go test ./... passed EXIT=0" "$STOP"
sleep 16

check "control: review task completed" '[ "$(task_status "$REVIEW_ID")" = completed ]'
check "control: build task completed" '[ "$(task_status "$BUILD_ID")" = completed ]'
check "control: test task completed" '[ "$(task_status "$TEST_ID")" = completed ]'
check "control: task-detected row for review" '[ "$(lifecycle_row task-detected "review task")" -ge 1 ]'
check "control: genuine review reply fired verify-spec at plan" '[ "$(verify_spec_count)" -ge 1 ]'
check "control: plan-verify row written" '[ "$(lifecycle_row "\"event\":\"plan-verify\"" "fixture-spec")" -ge 1 ]'

if [ "$(ls -A "$REPO")" = "$REPO_BEFORE" ]; then
  ok "the checkout is unchanged"
else
  fail "the checkout gained entries: $(comm -13 <(printf '%s\n' "$REPO_BEFORE" | sort) <(ls -A "$REPO" | sort) | tr '\n' ' ')"
fi

if [ "$FAIL" -ne 0 ]; then
  echo "--- daemon log tail ---"
  tail -20 "$WORK/daemon.log" 2>/dev/null
  echo "--- lifecycle events ---"
  cat "$WORK/lifecycle"/* 2>/dev/null | grep -E "task-|plan-verify|deliver|redrive" | tail -20
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
