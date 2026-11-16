#!/usr/bin/env bash
# Integration test for MUX-170: a graph dispatch suppressed by another run's
# in-flight request waits for it instead of adopting it.
#
# Every graph dispatch is sent as `daemon`, so run B's `request:test` was
# suppressed by run A's still-unanswered one and B's node adopted A's task —
# then completed on a reply to a request it never sent. Everything below runs
# two real `30-build-test-review` runs on a scratch daemon built from the tree;
# this script plays build, test and review over the bus (replies carry EXIT=0;
# no hooks run).
#
#   A. Negative control — no other run: A's test node dispatches as soon as
#      build answers, with no graph-dispatch-deferred row.
#   B. Two runs: with A's test dispatch held unanswered, B reaches its test
#      node and stays ready, deferred on A's task id, with one row naming it
#      across several ticks and no second daemon→test request; answering A's
#      test releases B, whose own request lands with its own task id; B is not
#      completed by A's reply and completes only on its own.
#   C. Negative control (Decision 1) — edit's own in-flight `request:test`
#      does not defer a run: its dispatch is delivered beside edit's request
#      with no deferred row, edit's task left in flight.
#   D. Control: a muxcode built from a copy of this tree with the ownership
#      check removed (any daemon duplicate adopted) fails B under the same
#      checks — B's node takes A's task id with no deferred row and is done on
#      A's reply — so a green B proves the checks can see the defect.
#
# ISOLATION: builds muxcode from the tree (MUXCODE_GRAPH_FOREIGN_BIN overrides
# the fixed binary); scratch HOME, XDG_STATE_HOME, lifecycle log, config and
# repo, one BUS_SESSION and daemon per section pair; a private tmux server
# (TMUX_TMPDIR in the work dir, TMUX unset, killed on exit).
#
# REQUIRES: go, tmux, jq. ~1 min, mostly the two builds.
#
# Usage: bash scripts/test-graph-foreign-dispatch.sh
#        MUXCODE_TEST_KEEP=1 bash scripts/test-graph-foreign-dispatch.sh   # keep WORK
set -uo pipefail

FLOOR=24
REPO_ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)

GREEN=$'\033[0;32m'; RED=$'\033[0;31m'; NC=$'\033[0m'
pass=0; fail=0
ok()  { echo "  ${GREEN}PASS${NC}  $*"; pass=$((pass + 1)); }
bad() { echo "  ${RED}FAIL${NC}  $*"; fail=$((fail + 1)); }

for tool in go tmux jq; do
  command -v "$tool" >/dev/null 2>&1 || { echo "  FAIL  $tool is required"; exit 1; }
done

WORK=$(mktemp -d "${TMPDIR:-/tmp}/graph-foreign-XXXXXX")
mkdir -p "$WORK/tmux"
export TMUX_TMPDIR="$WORK/tmux"
unset TMUX TMUX_PANE
DPIDS=""
cleanup() {
  local p
  for p in $DPIDS; do kill "$p" 2>/dev/null; done
  tmux kill-server 2>/dev/null
  if [ "${MUXCODE_TEST_KEEP:-0}" = "1" ]; then echo "  (kept for diagnosis: $WORK)"; else rm -rf "$WORK"; fi
}
trap cleanup EXIT
GOCACHE="$(go env GOCACHE)"; GOPATH="$(go env GOPATH)"
export GOCACHE GOPATH

# --- Isolation -------------------------------------------------------------
export HOME="$WORK/home"
export XDG_STATE_HOME="$HOME/.local/state"
mkdir -p "$HOME/.config/muxcode" "$WORK/lifecycle" "$WORK/config-dir" "$WORK/repo"
: > "$WORK/empty-config"
export MUXCODE_CONFIG="$WORK/empty-config"
export MUXCODE_CONFIG_DIR="$WORK/config-dir"
export MUXCODE_LIFECYCLE_LOG_DIR="$WORK/lifecycle"
export BUS_MEMORY_DIR="$WORK/memory"
export BUS_GLOBAL_MEMORY_DIR="$WORK/global-memory"
export MUXCODE_SESSION_REPO_DIR="$WORK/repo"
export MUXCODE_DEDUP_WINDOW=0
export MUXCODE_RELAY_SUPPRESS_THRESHOLD=0
export MUXCODE_TMP_CLEANUP_THRESHOLD=0
export MUXCODE_BRANCH_TIME_DISABLE=1
unset MUXCODE_BUS_BASE MUXCODE_BUS_DIR AGENT_ROLE BUS_ROLE BUS_SESSION MUXCODE_AGENT_CLI
git -C "$WORK/repo" init -q 2>/dev/null

# --- Binaries --------------------------------------------------------------
echo "--- binaries"
if [ -n "${MUXCODE_GRAPH_FOREIGN_BIN:-}" ]; then
  MUX="$MUXCODE_GRAPH_FOREIGN_BIN"; ok "fixed binary from MUXCODE_GRAPH_FOREIGN_BIN: $MUX"
else
  MUX="$WORK/muxcode"
  (cd "$REPO_ROOT/tools/muxcode" && go build -buildvcs=false -o "$MUX" .) >"$WORK/build.log" 2>&1 \
    && ok "built muxcode from the tree" || { bad "go build failed: $(tail -3 "$WORK/build.log")"; exit 1; }
fi
CTL_TREE="$WORK/ctl-tree"
cp -R "$REPO_ROOT/tools/muxcode" "$CTL_TREE"
perl -pi -e 's/if !t\.ownedByNode\(run\.ID, n\.ID\) \{/if false {/' "$CTL_TREE/bus/graph_exec.go"
CTL="$WORK/muxcode-prefix"
if ! grep -q 'if !t.ownedByNode' "$CTL_TREE/bus/graph_exec.go" \
  && (cd "$CTL_TREE" && go build -buildvcs=false -o "$CTL" .) >"$WORK/ctl-build.log" 2>&1; then
  ok "built the control: ownership check removed"
else
  bad "control build or revert failed: $(tail -3 "$WORK/ctl-build.log" 2>/dev/null)"
fi

# --- Helpers ---------------------------------------------------------------
# start_session <bin> <name> — a fresh bus, its tmux session and a daemon on bin.
start_session() {
  export BUS_SESSION="graph-foreign-$2-$$"
  BD="$("$1" bus-dir "$BUS_SESSION")" || exit 1
  LIFELOG="$MUXCODE_LIFECYCLE_LOG_DIR/$BUS_SESSION.log"
  "$1" init --memory-dir "$WORK/memory-$2" >/dev/null 2>&1
  tmux new-session -d -s "$BUS_SESSION" -n edit 2>/dev/null
  "$1" watch "$BUS_SESSION" --poll 1 >"$WORK/daemon-$2.log" 2>&1 &
  DPIDS="$DPIDS $!"
}
# start_run <bin> <intent> — prints the new run's id.
start_run() {
  (cd "$WORK/repo" && "$1" graph run 30-build-test-review "$2" 2>&1) | grep -o 'Started run [^ ]*' | awk '{print $3}'
}
node_field() { jq -r ".$3 // \"\"" "$BD/graphs/$1/nodes/$2.json" 2>/dev/null; }
run_state() { jq -r '.state // ""' "$BD/graphs/$1/run.json" 2>/dev/null; }
# request_id <role> <run> — the pending request in role's inbox dispatched by run.
request_id() {
  jq -r --arg r "$2" 'select(.type == "request" and .graph_run == $r) | .id' "$BD/inbox/$1.jsonl" 2>/dev/null | head -1
}
daemon_requests() { jq -c 'select(.type == "request" and .from == "daemon")' "$BD/inbox/$1.jsonl" 2>/dev/null | grep -c .; }
deferred_rows() { grep -F '"graph-dispatch-deferred"' "$LIFELOG" 2>/dev/null | grep -cF "$1:"; }
# wait_until <secs> <cmd...> — polls cmd every 0.25 s.
wait_until() {
  local n=$(($1 * 4)) i; shift
  for ((i = 0; i < n; i++)); do "$@" && return 0; sleep 0.25; done
  return 1
}
has_request() { [ -n "$(request_id "$1" "$2")" ]; }
node_is() { [ "$(node_field "$1" "$2" state)" = "$3" ]; }
run_is() { [ "$(run_state "$1")" = "$2" ]; }
# answer <role> <run> <text> — the agent replies to the run's request.
answer() {
  local id; id=$(request_id "$1" "$2")
  [ -n "$id" ] || return 1
  AGENT_ROLE="$1" "$MUX_NOW" send edit response "$3 EXIT=0" --type response --reply-to "$id" >/dev/null 2>&1
}
test_scheduled() { local s; s=$(node_field "$1" test state); [ -n "$s" ] && [ "$s" != pending ]; }
# A review node is decided by the findings line its reply starts with.
REVIEW_CLEAN=$'0 must-fix, 0 should-fix, 0 nits\n'
# build_answered <run> — answers the run's build and waits for its test node to leave pending.
build_answered() {
  wait_until 20 has_request build "$1" && answer build "$1" "build ok" && wait_until 20 test_scheduled "$1"
}

# two_runs <bin> <name> — section B's setup on bin: A held at test, B at its
# test node. Sets RA RB A_TASK.
two_runs() {
  MUX_NOW=$1
  start_session "$1" "$2"
  RA=$(start_run "$1" "run A"); RB=""; A_TASK=""
  build_answered "$RA" && wait_until 20 node_is "$RA" test running
  A_TASK=$(node_field "$RA" test task_id)
  RB=$(start_run "$1" "run B")
  build_answered "$RB"
  sleep 4
}

# --- A + B. Two runs on the fixed build ------------------------------------
echo "--- A. one run alone dispatches its test at once"
two_runs "$MUX" fixed
[ -n "$RA" ] && [ -n "$A_TASK" ] && node_is "$RA" test running && ok "A's test dispatched ($A_TASK)" \
  || bad "A's test: run '$RA' state '$(node_field "$RA" test state)' task '$A_TASK' ($(tail -2 "$WORK/daemon-fixed.log"))"
[ "$(deferred_rows "$RA")" = 0 ] && ok "no graph-dispatch-deferred row for A" || bad "$(deferred_rows "$RA") deferred rows for A"
[ "$(jq -r --arg r "$RA" 'select(.id == "'"$A_TASK"'") | .graph_run' "$BD/inbox/test.jsonl" 2>/dev/null)" = "$RA" ] \
  && ok "A's request in test's inbox carries run A" || bad "A's request is not in test's inbox under run A"

echo "--- B. a second run waits behind A's request"
[ -n "$RB" ] && node_is "$RB" test ready && [ -z "$(node_field "$RB" test task_id)" ] \
  && ok "B's test node is ready with no task after four ticks" \
  || bad "B's test: state '$(node_field "$RB" test state)' task '$(node_field "$RB" test task_id)'"
case "$(node_field "$RB" test deferred_on)" in
  *"$A_TASK"*) ok "B is deferred on A's task: $(node_field "$RB" test deferred_on)" ;;
  *) bad "B deferred_on '$(node_field "$RB" test deferred_on)', want A's $A_TASK" ;;
esac
rows=$(deferred_rows "$RB")
[ "$rows" = 1 ] && grep -F '"graph-dispatch-deferred"' "$LIFELOG" | grep -F "$RB:" | grep -qF "$A_TASK" \
  && ok "one graph-dispatch-deferred row for B, naming A's task" || bad "$rows deferred rows for B"
[ "$(daemon_requests test)" = 1 ] && ok "test's inbox holds one daemon request — no second dispatch" \
  || bad "test's inbox holds $(daemon_requests test) daemon requests"

answer test "$RA" "A's suite passed"
wait_until 20 node_is "$RA" test done && ok "answering A's test finishes A's test node" || bad "A's test node: $(node_field "$RA" test state)"
wait_until 20 node_is "$RB" test running
B_TASK=$(node_field "$RB" test task_id)
[ -n "$B_TASK" ] && [ "$B_TASK" != "$A_TASK" ] && [ "$(request_id test "$RB")" = "$B_TASK" ] \
  && ok "B's own request landed after A's cleared ($B_TASK)" || bad "B after A cleared: state '$(node_field "$RB" test state)' task '$B_TASK'"
[ -z "$(node_field "$RB" test deferred_on)" ] && ok "B's hold is cleared" || bad "B still deferred: $(node_field "$RB" test deferred_on)"
wait_until 20 has_request review "$RA" && answer review "$RA" "$REVIEW_CLEAN A reviewed" && wait_until 20 run_is "$RA" complete \
  && ok "run A completes" || bad "run A state '$(run_state "$RA")'"
node_is "$RB" test running && ok "B's test node is still running — A's reply did not complete it" \
  || bad "B's test node is '$(node_field "$RB" test state)' after A's reply"
answer test "$RB" "B's suite passed"
wait_until 20 node_is "$RB" test done && ok "B's test completes on its own reply" || bad "B's test node: $(node_field "$RB" test state)"
wait_until 20 has_request review "$RB" && answer review "$RB" "$REVIEW_CLEAN B reviewed" && wait_until 20 run_is "$RB" complete \
  && ok "run B completes" || bad "run B state '$(run_state "$RB")'"

# --- C. Decision 1: another sender's request -------------------------------
echo "--- C. edit's own in-flight test request does not defer a run"
EDIT_OUT=$(AGENT_ROLE=edit "$MUX" send test test "edit's own suite run" --no-notify 2>&1)
EDIT_ID=$(jq -r 'select(.from == "edit" and .type == "request") | .id' "$BD/inbox/test.jsonl" 2>/dev/null | tail -1)
[ -n "$EDIT_ID" ] && [ "$(jq -r '.status' "$BD/tasks/$EDIT_ID.json" 2>/dev/null)" = in-flight ] \
  && ok "edit's request:test is pending and tracked ($EDIT_ID)" || bad "edit's request: $EDIT_OUT"
RC=$(start_run "$MUX" "run C")
build_answered "$RC" && wait_until 20 node_is "$RC" test running
C_TASK=$(node_field "$RC" test task_id)
[ -n "$C_TASK" ] && [ "$C_TASK" != "$EDIT_ID" ] && [ "$(request_id test "$RC")" = "$C_TASK" ] \
  && ok "C's request is delivered beside edit's ($C_TASK)" || bad "C's test: state '$(node_field "$RC" test state)' task '$C_TASK'"
[ "$(deferred_rows "$RC")" = 0 ] && ok "no graph-dispatch-deferred row for C" || bad "$(deferred_rows "$RC") deferred rows for C"
[ "$(jq -r '.status' "$BD/tasks/$EDIT_ID.json" 2>/dev/null)" = in-flight ] && ok "edit's task is left in flight" \
  || bad "edit's task status: $(jq -r '.status' "$BD/tasks/$EDIT_ID.json" 2>/dev/null)"

# --- D. Control: ownership check removed -----------------------------------
echo "--- D. control: a build that adopts any daemon duplicate"
if [ -x "$CTL" ]; then
  two_runs "$CTL" control
  [ -n "$A_TASK" ] && node_is "$RA" test running && ok "control: A's test dispatched ($A_TASK)" \
    || bad "control: A's test state '$(node_field "$RA" test state)'"
  [ "$(node_field "$RB" test task_id)" = "$A_TASK" ] && node_is "$RB" test running \
    && ok "control: B adopted A's task — the defect B's checks catch" \
    || bad "control: B state '$(node_field "$RB" test state)' task '$(node_field "$RB" test task_id)' — section B could not see the defect"
  [ "$(deferred_rows "$RB")" = 0 ] && ok "control: no deferred row for B" || bad "control: $(deferred_rows "$RB") deferred rows for B"
  answer test "$RA" "A's suite passed"
  wait_until 20 node_is "$RB" test done && ok "control: B's test is done on A's reply" \
    || bad "control: B's test '$(node_field "$RB" test state)' after A's reply"
else
  bad "control binary missing — section D skipped"
fi

# --- Summary ----------------------------------------------------------------
total=$((pass + fail))
echo
echo "=== test-graph-foreign-dispatch: $pass passed, $fail failed ($total checks, floor $FLOOR) ==="
if [ "$total" -lt "$FLOOR" ]; then
  echo "  ${RED}FAIL${NC}  only $total checks ran (floor $FLOOR) — a section was skipped"
  exit 1
fi
[ "$fail" = 0 ]
