#!/usr/bin/env bash
# Integration test for MUX-225: a reply whose --reply-to names no message is
# never silent, and on the 2026-11-14 shape it closes the node it meant to
# answer instead of leaving it to wait out its 600 s cap.
#
# On 2026-11-14 a Codex test agent answered a graph test dispatch with
# `--reply-to 1794683890-da0a89855` — six characters dropped from
# 1794683890-daemon-30a89855. `send` printed "Sent", the reply correlated to
# nothing, and the node failed at 600 s on a passing suite. Decision 1 (the
# user's): one open request from the reply's requester → attach the reply to it
# (reply-to-repaired); zero or several → refuse with a reply command for each
# (reply-to-unknown); edit is told once either way.
#
#   A. A real one-node graph `test` run on a scratch daemon; the script plays
#      the test agent, reads the dispatch and replies to edit with the id
#      mangled exactly as on 2026-11-14: send exits 0 naming the repair, one
#      reply-to-repaired row names both ids, edit holds one notice, and the
#      node and run complete well inside 600 s, with no reply-to-unknown row.
#   B. Control: the same run answered with the right id completes with no
#      reply-to-repaired or reply-to-unknown row and no notice to edit.
#   C. Refusals, no daemon: holding no open request, and holding two from edit,
#      a mangled reply is NOT SENT (exit 1) with one reply command per open
#      request, addressed to edit, and a reply-to-unknown row; edit is told.
#   D. Control: a muxcode built from a copy of this tree with the resolver
#      bypassed (every --reply-to passes through) sends the 2026-11-14 reply
#      silently and the node is still running 15 s later — so a green A proves
#      the checks can see the defect.
#
# ISOLATION: muxcode built from the tree (MUXCODE_REPLY_TO_UNKNOWN_BIN overrides
# the fixed binary); scratch HOME, XDG_STATE_HOME, lifecycle log, config,
# memory and repo, one BUS_SESSION and daemon per section; a private tmux
# server (TMUX_TMPDIR in the work dir, TMUX unset, killed on exit).
#
# REQUIRES: go, tmux, jq. ~1 min, mostly the two builds.
#
# Usage: bash scripts/test-reply-to-unknown.sh
#        MUXCODE_TEST_KEEP=1 bash scripts/test-reply-to-unknown.sh   # keep WORK
set -uo pipefail
export MUXCODE_CONSUMER_GUARD=0 # agents here are played by reading their inboxes, which no window or listener shows (MUX-145)

FLOOR=22
REPO_ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)

GREEN=$'\033[0;32m'; RED=$'\033[0;31m'; NC=$'\033[0m'
pass=0; fail=0
ok()  { echo "  ${GREEN}PASS${NC}  $*"; pass=$((pass + 1)); }
bad() { echo "  ${RED}FAIL${NC}  $*"; fail=$((fail + 1)); }

for tool in go tmux jq; do
  command -v "$tool" >/dev/null 2>&1 || { echo "  FAIL  $tool is required"; exit 1; }
done

WORK=$(mktemp -d "${TMPDIR:-/tmp}/reply-to-unknown-XXXXXX")
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
export MUXCODE_FORCE_RESPOND_DISABLE=1 MUXCODE_EDIT_AUTO_RESTART_DISABLE=1 MUXCODE_CONTROL_PANE_DISABLE=1
export MUXCODE_AGENTDEFS_WATCH_DISABLE=1 MUXCODE_STUCK_RELOAD_DISABLE=1 MUXCODE_PROMPT_AGENT_DISABLE=1
unset MUXCODE_BUS_BASE MUXCODE_BUS_DIR AGENT_ROLE BUS_ROLE BUS_SESSION MUXCODE_AGENT_CLI MUXCODE
git -C "$WORK/repo" init -q 2>/dev/null

# --- Binaries --------------------------------------------------------------
echo "--- binaries"
if [ -n "${MUXCODE_REPLY_TO_UNKNOWN_BIN:-}" ]; then
  MUX="$MUXCODE_REPLY_TO_UNKNOWN_BIN"; ok "fixed binary from MUXCODE_REPLY_TO_UNKNOWN_BIN: $MUX"
else
  MUX="$WORK/muxcode"
  (cd "$REPO_ROOT/tools/muxcode" && go build -buildvcs=false -o "$MUX" .) >"$WORK/build.log" 2>&1 \
    && ok "built muxcode from the tree" || { bad "go build failed: $(tail -3 "$WORK/build.log")"; exit 1; }
fi
CTL_TREE="$WORK/ctl-tree"
cp -R "$REPO_ROOT/tools/muxcode" "$CTL_TREE"
perl -pi -e 's/if msgType != "response" \|\| replyTo == "" \|\| ReplyToKnown\(session, replyTo\) \{/if true {/' "$CTL_TREE/bus/reply_to.go"
CTL="$WORK/muxcode-prefix"
if ! grep -q 'ReplyToKnown(session, replyTo) {' "$CTL_TREE/bus/reply_to.go" \
  && (cd "$CTL_TREE" && go build -buildvcs=false -o "$CTL" .) >"$WORK/ctl-build.log" 2>&1; then
  ok "built the control: every --reply-to passes through unresolved"
else
  bad "control build or revert failed: $(tail -3 "$WORK/ctl-build.log" 2>/dev/null)"
fi

# --- Helpers ---------------------------------------------------------------
# start_session <bin> <name> [daemon] — a fresh bus and tmux session; with
# "daemon", a scratch daemon on bin.
start_session() {
  export BUS_SESSION="reply-unknown-$2-$$"
  BD="$("$1" bus-dir "$BUS_SESSION")" || exit 1
  LIFELOG="$MUXCODE_LIFECYCLE_LOG_DIR/$BUS_SESSION.log"
  "$1" init >/dev/null 2>&1
  tmux new-session -d -s "$BUS_SESSION" -n edit 2>/dev/null
  if [ "${3:-}" = daemon ]; then
    "$1" watch "$BUS_SESSION" --poll 1 >"$WORK/daemon-$2.log" 2>&1 &
    DPIDS="$DPIDS $!"
  fi
}
cat > "$WORK/graph-test.json" <<'EOF'
{"name": "mux225-reply-to-unknown", "start": "t",
 "nodes": [{"id": "t", "type": "send", "role": "test", "action": "test",
            "message": "Run ./test.sh and report"}],
 "edges": []}
EOF
# start_run <bin> — prints the new run's id.
start_run() {
  (cd "$WORK/repo" && "$1" graph run --file "$WORK/graph-test.json" 2>&1) | grep -o 'Started run [^ ]*' | awk '{print $3}'
}
node_field() { jq -r ".$2 // \"\"" "$BD/graphs/$1/nodes/t.json" 2>/dev/null; }
run_state()  { jq -r '.state // ""' "$BD/graphs/$1/run.json" 2>/dev/null; }
dispatch_id() { jq -r --arg r "$1" 'select(.type == "request" and .graph_run == $r) | .id' "$BD/inbox/test.jsonl" 2>/dev/null | head -1; }
rows()        { grep -F "\"$1\"" "$LIFELOG" 2>/dev/null | grep -c . ; }
edit_events() { jq -c --arg a "$1" 'select(.action == $a)' "$BD/inbox/edit.jsonl" 2>/dev/null | grep -c . ; }
wait_until() {
  local n=$(($1 * 4)) i; shift
  for ((i = 0; i < n; i++)); do "$@" && return 0; sleep 0.25; done
  return 1
}
has_dispatch() { [ -n "$(dispatch_id "$1")" ]; }
node_is() { [ "$(node_field "$1" state)" = "$2" ]; }
run_is()  { [ "$(run_state "$1")" = "$2" ]; }
# mangle <id> — the 2026-11-14 mangling: six characters dropped after "<ts>-da".
mangle() { printf '%s%s' "${1:0:13}" "${1:19}"; }
# play_test <bin> <run> <reply-to> — the test agent reads the dispatch and
# replies to edit; sets SEND_RC and SEND_OUT.
play_test() {
  AGENT_ROLE=test "$1" inbox >/dev/null 2>&1
  SEND_OUT=$(AGENT_ROLE=test "$1" send edit response "All 4 packages passed. EXIT=0" --type response --reply-to "$3" 2>&1)
  SEND_RC=$?
}

# --- A. The 2026-11-14 shape, repaired --------------------------------------
echo "--- A. a mangled reply to the one open dispatch is repaired"
start_session "$MUX" a daemon
RID=$(start_run "$MUX")
wait_until 20 has_dispatch "$RID" && DISPATCH=$(dispatch_id "$RID") \
  && ok "the run dispatched its test node ($DISPATCH)" || bad "no dispatch: run '$RID' ($(tail -2 "$WORK/daemon-a.log"))"
MANGLED=$(mangle "${DISPATCH:-x}")
START=$(date +%s)
play_test "$MUX" "$RID" "$MANGLED"
[ "$SEND_RC" -eq 0 ] && printf '%s' "$SEND_OUT" | grep -q "attached to $DISPATCH" \
  && ok "send exits 0 and names the repair: $MANGLED → $DISPATCH" || bad "send exit $SEND_RC: $SEND_OUT"
[ "$(rows reply-to-repaired)" = 1 ] && grep -F '"reply-to-repaired"' "$LIFELOG" | grep -qF "$MANGLED" \
  && grep -F '"reply-to-repaired"' "$LIFELOG" | grep -qF "repaired to $DISPATCH" \
  && ok "one reply-to-repaired row names $MANGLED and $DISPATCH" || bad "reply-to-repaired rows: $(grep -F reply-to-repaired "$LIFELOG")"
[ "$(rows reply-to-unknown)" = 0 ] && ok "no reply-to-unknown row" || bad "$(rows reply-to-unknown) reply-to-unknown rows"
[ "$(edit_events reply-to-repaired)" = 1 ] && ok "edit holds one reply-to-repaired notice" \
  || bad "edit holds $(edit_events reply-to-repaired) reply-to-repaired notices"
done_ok() { node_is "$RID" done && run_is "$RID" complete; }
if wait_until 30 done_ok; then
  ELAPSED=$(( $(date +%s) - START ))
  ok "the node and run completed ${ELAPSED}s after the reply — not at 600 s"
else
  bad "node '$(node_field "$RID" state)', run '$(run_state "$RID")' 30 s after the reply"
fi
[ "$(node_field "$RID" outcome)" = success ] && ok "the node's outcome is success, the reply's EXIT=0" \
  || bad "node outcome '$(node_field "$RID" outcome)'"
[ "$(jq -r '.status' "$BD/tasks/$DISPATCH.json" 2>/dev/null)" = completed ] \
  && ok "the dispatch's task is completed" || bad "dispatch task: $(jq -c . "$BD/tasks/$DISPATCH.json" 2>/dev/null)"

# --- B. Control: the right id -----------------------------------------------
echo "--- B. control — the right id"
start_session "$MUX" b daemon
RID=$(start_run "$MUX")
wait_until 20 has_dispatch "$RID" && DISPATCH=$(dispatch_id "$RID")
play_test "$MUX" "$RID" "${DISPATCH:-x}"
[ "$SEND_RC" -eq 0 ] && ! printf '%s' "$SEND_OUT" | grep -q 'attached to' \
  && ok "the right id sends with no repair note" || bad "send exit $SEND_RC: $SEND_OUT"
wait_until 30 done_ok && ok "the node and run complete" || bad "node '$(node_field "$RID" state)', run '$(run_state "$RID")'"
[ "$(rows reply-to-repaired)" = 0 ] && [ "$(rows reply-to-unknown)" = 0 ] \
  && ok "no reply-to-repaired or reply-to-unknown row" || bad "rows: $(grep -E 'reply-to-(repaired|unknown)' "$LIFELOG")"
[ "$(edit_events reply-to-repaired)" = 0 ] && [ "$(edit_events reply-to-unknown)" = 0 ] \
  && ok "edit was told nothing" || bad "edit notices present"

# --- C. Refusals -------------------------------------------------------------
echo "--- C. refused with no single open request"
start_session "$MUX" c
play_test "$MUX" "" "1794683890-da0a89855"
[ "$SEND_RC" -eq 1 ] && printf '%s' "$SEND_OUT" | grep -q 'NOT SENT' && printf '%s' "$SEND_OUT" | grep -q 'you hold no open request' \
  && ok "holding none: NOT SENT, exit 1, saying no request is open" || bad "send exit $SEND_RC: $SEND_OUT"
[ "$(rows reply-to-unknown)" = 1 ] && ok "one reply-to-unknown row" || bad "$(rows reply-to-unknown) reply-to-unknown rows"
for n in 1 2; do
  AGENT_ROLE=edit "$MUX" send test test "Run the suite, request $n" --track --force --no-notify >/dev/null 2>&1
done
OPEN=$(jq -r 'select(.type == "request" and .from == "edit") | .id' "$BD/inbox/test.jsonl" 2>/dev/null)
play_test "$MUX" "" "1794683890-da0a89856"
cmds=$(printf '%s' "$SEND_OUT" | grep -c 'muxcode send edit response')
all_named=1
for id in $OPEN; do printf '%s' "$SEND_OUT" | grep -q -- "--reply-to $id" || all_named=0; done
[ "$SEND_RC" -eq 1 ] && [ "$cmds" = 2 ] && [ "$all_named" = 1 ] && [ "$(echo "$OPEN" | grep -c .)" = 2 ] \
  && ok "holding two from edit: NOT SENT with a reply command to edit for each" || bad "send exit $SEND_RC, $cmds commands: $SEND_OUT"
[ "$(rows reply-to-unknown)" = 2 ] && [ "$(rows reply-to-repaired)" = 0 ] \
  && ok "a second reply-to-unknown row, and no guess" || bad "rows: $(grep -E 'reply-to-(repaired|unknown)' "$LIFELOG")"
[ "$(edit_events reply-to-unknown)" = 2 ] && ok "edit was told once per refused id" \
  || bad "edit holds $(edit_events reply-to-unknown) reply-to-unknown notices"

# --- D. Control: the pre-fix build -------------------------------------------
echo "--- D. control — the resolver bypassed"
start_session "$CTL" d daemon
RID=$(start_run "$CTL")
wait_until 20 has_dispatch "$RID" && DISPATCH=$(dispatch_id "$RID")
play_test "$CTL" "$RID" "$(mangle "${DISPATCH:-x}")"
[ "$SEND_RC" -eq 0 ] && ! printf '%s' "$SEND_OUT" | grep -q 'attached to' \
  && ok "the pre-fix send accepts the mangled id silently — the 2026-11-14 'Sent'" || bad "send exit $SEND_RC: $SEND_OUT"
sleep 15
node_is "$RID" running && [ "$(jq -r '.status' "$BD/tasks/$DISPATCH.json" 2>/dev/null)" = in-flight ] \
  && ok "15 s later the node is still running on an in-flight task — left to its 600 s cap" \
  || bad "node '$(node_field "$RID" state)', task '$(jq -r '.status' "$BD/tasks/$DISPATCH.json" 2>/dev/null)'"
[ "$(rows reply-to-repaired)" = 0 ] && [ "$(rows reply-to-unknown)" = 0 ] \
  && ok "and nothing was logged" || bad "rows: $(grep -E 'reply-to-(repaired|unknown)' "$LIFELOG")"

# --- Coverage floor ---------------------------------------------------------
echo
echo "=== Summary: $pass passed, $fail failed (floor $FLOOR) ==="
if [ "$pass" -lt "$FLOOR" ]; then
  echo "  ${RED}FAIL${NC}  coverage floor: $pass passes is below $FLOOR — a section was skipped"
  exit 1
fi
[ "$fail" -eq 0 ] || exit 1
exit 0
