#!/usr/bin/env bash
# Integration test for MUX-155: `muxcode send`'s in-flight dedup keys on the
# sender, and a send the bus did not write says so.
#
# On 2026-09-08 plan's `send edit notify` printed "already tracking" and wrote
# nothing, because watch had an unanswered notify to edit: the guard keyed on
# (to, action) alone. A duplicate reply printed "Sent response:…" the same way.
# Everything below runs the real CLI against a scratch bus:
#
#   A. Cross-sender: watch→edit notify in flight, then plan→edit notify — plan's
#      is delivered (a row in edit's inbox, exit 0) and tracked as its own task,
#      and watch's task stays in flight.
#   B. Same sender (negative control — the guard cannot go inert): plan's notify
#      re-sent while in flight is NOT SENT, exit 1, naming its task; one inbox
#      row, one task. --force still delivers it.
#   C. Duplicate reply: edit's `--wait` request is answered by plan, which
#      completes the task through the real wait road; plan's second reply is NOT
#      SENT, exit 1, "already answered", and written nowhere.
#   D. Control: a muxcode built from a copy of this tree with both fixes
#      reverted (the sender comparison removed from the in-flight lookups,
#      acceptReply's suppression back to nil) fails A and C under the same
#      checks — A's send is dropped and C's reply prints "Sent response" while
#      landing nowhere, so a green A and C prove the checks can see the defect.
#
# ISOLATION: builds muxcode from the tree (MUXCODE_SEND_DEDUP_BIN overrides the
# fixed binary); scratch HOME, XDG_STATE_HOME, lifecycle log, memory and config,
# one BUS_SESSION per section; no daemon, no tmux (TMUX unset, TMUX_TMPDIR in
# the work dir). Dedup window and relay guard are off so only the in-flight
# guard and reply claim decide.
#
# REQUIRES: go. ~30 s, mostly the two builds.
#
# Usage: bash scripts/test-send-dedup-sender.sh
#        MUXCODE_TEST_KEEP=1 bash scripts/test-send-dedup-sender.sh   # keep WORK
set -uo pipefail

FLOOR=20
REPO_ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)

GREEN=$'\033[0;32m'; RED=$'\033[0;31m'; NC=$'\033[0m'
pass=0; fail=0
ok()  { echo "  ${GREEN}PASS${NC}  $*"; pass=$((pass + 1)); }
bad() { echo "  ${RED}FAIL${NC}  $*"; fail=$((fail + 1)); }

WORK=$(mktemp -d "${TMPDIR:-/tmp}/send-dedup-sender-XXXXXX")
WAITPID=""
cleanup() {
  [ -n "$WAITPID" ] && kill "$WAITPID" 2>/dev/null
  if [ "${MUXCODE_TEST_KEEP:-0}" = "1" ]; then echo "  (kept for diagnosis: $WORK)"; else rm -rf "$WORK"; fi
}
trap cleanup EXIT

command -v go >/dev/null 2>&1 || { echo "  FAIL  go is required"; exit 1; }
GOCACHE="$(go env GOCACHE)"; GOPATH="$(go env GOPATH)"
export GOCACHE GOPATH

# --- Isolation -------------------------------------------------------------
export HOME="$WORK/home"
export XDG_STATE_HOME="$HOME/.local/state"
export TMUX_TMPDIR="$WORK/tmux"
mkdir -p "$HOME/.config/muxcode" "$WORK/lifecycle" "$TMUX_TMPDIR"
: > "$WORK/empty-config"
export MUXCODE_CONFIG="$WORK/empty-config"
export MUXCODE_CONFIG_DIR="$WORK/config-dir"
export MUXCODE_LIFECYCLE_LOG_DIR="$WORK/lifecycle"
export BUS_MEMORY_DIR="$WORK/memory"
export BUS_GLOBAL_MEMORY_DIR="$WORK/global-memory"
export MUXCODE_DEDUP_WINDOW=0
export MUXCODE_RELAY_SUPPRESS_THRESHOLD=0
export MUXCODE_WAIT_DEGRADE_SECS=20
unset TMUX TMUX_PANE MUXCODE_BUS_BASE MUXCODE_BUS_DIR AGENT_ROLE BUS_ROLE BUS_SESSION MUXCODE_SEND_DEFAULT_FIRE_AND_FORGET
mkdir -p "$MUXCODE_CONFIG_DIR"

# --- Binaries --------------------------------------------------------------
echo "--- binaries"
if [ -n "${MUXCODE_SEND_DEDUP_BIN:-}" ]; then
  MUX="$MUXCODE_SEND_DEDUP_BIN"; ok "fixed binary from MUXCODE_SEND_DEDUP_BIN: $MUX"
else
  MUX="$WORK/muxcode"
  (cd "$REPO_ROOT/tools/muxcode" && go build -buildvcs=false -o "$MUX" .) >"$WORK/build.log" 2>&1 \
    && ok "built muxcode from the tree" || { bad "go build failed: $(tail -3 "$WORK/build.log")"; exit 1; }
fi

CTL_TREE="$WORK/ctl-tree"
cp -R "$REPO_ROOT/tools/muxcode" "$CTL_TREE"
perl -pi -e 's/t\.From == from && //' "$CTL_TREE/bus/dedup.go"
perl -pi -e 's/return fmt\.Errorf\("%s→%s reply to %s: %w", m\.From, m\.To, m\.ReplyTo, ErrReplySuppressed\)/return nil/' "$CTL_TREE/bus/inbox.go"
CTL="$WORK/muxcode-prefix"
if ! grep -q 't.From == from' "$CTL_TREE/bus/dedup.go" && ! grep -q 'ErrReplySuppressed)$' "$CTL_TREE/bus/inbox.go" \
  && (cd "$CTL_TREE" && go build -buildvcs=false -o "$CTL" .) >"$WORK/ctl-build.log" 2>&1; then
  ok "built the control: sender comparison removed, duplicate reply back on nil"
else
  bad "control build or revert failed: $(tail -3 "$WORK/ctl-build.log" 2>/dev/null)"
fi

# --- Helpers ---------------------------------------------------------------
# send_as <bin> <session> <role> <args...> — sets OUT and CODE.
send_as() {
  local bin=$1 session=$2 role=$3; shift 3
  OUT=$(BUS_SESSION="$session" AGENT_ROLE="$role" "$bin" send "$@" --no-notify 2>&1)
  CODE=$?
}
bus_dir() { "$MUX" bus-dir "$1"; }
# inbox_rows <session> <role> <payload> — rows in role's inbox carrying payload.
inbox_rows() { count_in "$(bus_dir "$1")/inbox/$2.jsonl" "\"payload\":\"$3\""; }
# count_in <file> <fixed-string> — matching lines, 0 when the file is absent.
count_in() {
  local n; n=$(grep -cF "$2" "$1" 2>/dev/null); echo "${n:-0}"
}
# tasks <session> <from> <to> <action> <status> — matching task ids, one a line.
tasks() {
  local f
  for f in "$(bus_dir "$1")"/tasks/*.json; do
    [ -f "$f" ] || continue
    grep -qF "\"from\":\"$2\"" "$f" && grep -qF "\"to\":\"$3\"" "$f" \
      && grep -qF "\"action\":\"$4\"" "$f" && grep -qF "\"status\":\"$5\"" "$f" \
      && basename "$f" .json
  done
}
init_session() { BUS_SESSION="$1" "$MUX" init --memory-dir "$WORK/memory-$1" >/dev/null 2>&1; }

# cross_sender <bin> <session> — scenario A; sets A_CODE A_OUT A_ROWS A_PLAN A_WATCH.
cross_sender() {
  local bin=$1 s=$2
  init_session "$s"
  send_as "$bin" "$s" watch edit notify "watch: CI green"
  A_WATCH_SENT=$CODE
  send_as "$bin" "$s" plan edit notify "plan: MUX-153 updated"
  A_CODE=$CODE; A_OUT=$OUT
  A_ROWS=$(inbox_rows "$s" edit "plan: MUX-153 updated")
  A_PLAN=$(tasks "$s" plan edit notify in-flight | wc -l | tr -d ' ')
  A_WATCH=$(tasks "$s" watch edit notify in-flight | wc -l | tr -d ' ')
}

# reply_twice <bin> <session> — scenario C; sets C_* from the real --wait road.
reply_twice() {
  local bin=$1 s=$2 bd id="" i
  init_session "$s"; bd=$(bus_dir "$s")
  ( BUS_SESSION="$s" AGENT_ROLE=edit "$bin" send plan update-docs "add the override row" --wait --no-notify \
      >"$WORK/wait-$s.out" 2>&1; echo $? >"$WORK/wait-$s.rc" ) &
  WAITPID=$!
  for i in $(seq 1 40); do
    id=$(grep -F '"payload":"add the override row"' "$bd/inbox/plan.jsonl" 2>/dev/null | head -1 \
      | sed -E 's/.*"id":"([^"]+)".*/\1/')
    [ -n "$id" ] && break
    sleep 0.25
  done
  C_ID=$id
  send_as "$bin" "$s" plan edit update-docs "row added" --type response --reply-to "$id"
  C_FIRST_CODE=$CODE; C_FIRST_OUT=$OUT
  for i in $(seq 1 40); do [ -f "$WORK/wait-$s.rc" ] && break; sleep 0.25; done
  wait "$WAITPID" 2>/dev/null; WAITPID=""
  C_WAIT_RC=$(cat "$WORK/wait-$s.rc" 2>/dev/null || echo none)
  C_STATUS=$(grep -o '"status":"[^"]*"' "$bd/tasks/$id.json" 2>/dev/null)
  send_as "$bin" "$s" plan edit update-docs "row added again" --type response --reply-to "$id"
  C_CODE=$CODE; C_OUT=$OUT
  C_ROWS=$(inbox_rows "$s" edit "row added again")
  C_LOGGED=$(count_in "$bd/log.jsonl" '"payload":"row added again"')
}

# --- A. Cross-sender ---------------------------------------------------------
echo "--- A. another sender's in-flight task does not drop plan's notify"
cross_sender "$MUX" "dedup-a-$$"
[ "$A_WATCH_SENT" = 0 ] && [ "$A_CODE" = 0 ] && grep -q "Sent request:notify to edit" <<<"$A_OUT" \
  && ok "plan's notify sent, exit 0, while watch's is in flight" \
  || bad "plan's notify: watch exit $A_WATCH_SENT, plan exit $A_CODE: $A_OUT"
[ "$A_ROWS" = 1 ] && ok "edit's inbox holds plan's notify" || bad "edit's inbox holds $A_ROWS copies of plan's notify, want 1"
[ "$A_PLAN" = 1 ] && ok "plan's send is tracked as its own in-flight task" || bad "plan has $A_PLAN in-flight edit:notify tasks, want 1"
[ "$A_WATCH" = 1 ] && ok "watch's task is still in flight" || bad "watch has $A_WATCH in-flight edit:notify tasks, want 1"

# --- B. Same sender ---------------------------------------------------------
echo "--- B. the same sender's retry is still deduplicated (negative control)"
SB="dedup-b-$$"; init_session "$SB"
send_as "$MUX" "$SB" plan edit notify "plan: report"
first_task=$(tasks "$SB" plan edit notify in-flight)
[ "$CODE" = 0 ] && [ -n "$first_task" ] && ok "first send delivered and tracked ($first_task)" || bad "first send exit $CODE: $OUT"
send_as "$MUX" "$SB" plan edit notify "plan: report"
[ "$CODE" = 1 ] && grep -q "NOT SENT" <<<"$OUT" && grep -qF "$first_task" <<<"$OUT" && ! grep -q "already tracking" <<<"$OUT" \
  && ok "retry: NOT SENT naming $first_task, exit 1" || bad "retry exit $CODE: $OUT"
[ "$(inbox_rows "$SB" edit "plan: report")" = 1 ] && ok "retry wrote no second inbox row" \
  || bad "edit's inbox holds $(inbox_rows "$SB" edit "plan: report") copies after the retry"
[ "$(tasks "$SB" plan edit notify in-flight | wc -l | tr -d ' ')" = 1 ] && ok "retry created no second task" \
  || bad "plan holds $(tasks "$SB" plan edit notify in-flight | wc -l | tr -d ' ') in-flight tasks after the retry"
send_as "$MUX" "$SB" plan edit notify "plan: report" --force
[ "$CODE" = 0 ] && [ "$(inbox_rows "$SB" edit "plan: report")" = 2 ] && ok "--force delivers past the guard" \
  || bad "--force exit $CODE, rows $(inbox_rows "$SB" edit "plan: report"): $OUT"

# --- C. Duplicate reply -----------------------------------------------------
echo "--- C. a second reply to an answered task is reported, not 'Sent'"
reply_twice "$MUX" "dedup-c-$$"
[ -n "$C_ID" ] && ok "edit's --wait request reached plan ($C_ID)" || bad "edit's --wait request never reached plan's inbox"
[ "$C_FIRST_CODE" = 0 ] && grep -q "Sent response:update-docs to edit" <<<"$C_FIRST_OUT" \
  && ok "plan's first reply sent, exit 0" || bad "first reply exit $C_FIRST_CODE: $C_FIRST_OUT"
[ "$C_WAIT_RC" = 0 ] && grep -q "row added" "$WORK/wait-dedup-c-$$.out" \
  && ok "edit's --wait received the answer and exited 0" || bad "--wait exit $C_WAIT_RC: $(tail -3 "$WORK/wait-dedup-c-$$.out")"
[ "$C_STATUS" = '"status":"completed"' ] && ok "the task is completed" || bad "task status: ${C_STATUS:-missing}"
[ "$C_CODE" = 1 ] && grep -q "NOT SENT" <<<"$C_OUT" && grep -q "already answered" <<<"$C_OUT" && ! grep -q "Sent response" <<<"$C_OUT" \
  && ok "second reply: NOT SENT, already answered, exit 1" || bad "second reply exit $C_CODE: $C_OUT"
[ "$C_ROWS" = 0 ] && [ "$C_LOGGED" = 0 ] && ok "the second reply is in neither edit's inbox nor the log" \
  || bad "second reply written: $C_ROWS inbox rows, $C_LOGGED log rows"

# --- D. Control: both fixes reverted -----------------------------------------
echo "--- D. control: the pre-fix build fails A and C under the same checks"
if [ -x "$CTL" ]; then
  cross_sender "$CTL" "dedup-ctl-a-$$"
  [ "$A_CODE" != 0 ] && [ "$A_ROWS" = 0 ] \
    && ok "control drops plan's notify behind watch's task (exit $A_CODE, 0 rows): $(head -1 <<<"$A_OUT")" \
    || bad "control delivered plan's notify (exit $A_CODE, $A_ROWS rows) — A cannot see the defect"
  reply_twice "$CTL" "dedup-ctl-c-$$"
  [ "$C_CODE" = 0 ] && grep -q "Sent response" <<<"$C_OUT" \
    && ok "control prints '$(grep -o 'Sent response[^ ]*' <<<"$C_OUT")' and exits 0 for the second reply" \
    || bad "control second reply exit $C_CODE: $C_OUT — C cannot see the defect"
  [ "$C_ROWS" = 0 ] && [ "$C_LOGGED" = 0 ] \
    && ok "control's 'Sent' reply landed nowhere — the silent loss C detects" \
    || bad "control's second reply was written ($C_ROWS inbox, $C_LOGGED log) — not the reported defect"
else
  bad "control binary missing — D skipped"
fi

# --- Summary ---------------------------------------------------------------
echo
echo "=== Summary: $pass passed, $fail failed (floor $FLOOR) ==="
[ "$fail" -ne 0 ] && exit 1
if [ "$pass" -lt "$FLOOR" ]; then
  echo "  FAIL  only $pass checks passed — below the coverage floor of $FLOOR"
  exit 1
fi
exit 0
