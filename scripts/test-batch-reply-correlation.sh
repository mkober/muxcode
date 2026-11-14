#!/usr/bin/env bash
# Integration test for MUX-189 — every request in a batched wake-up must get
# its own reply instruction, so every task the batch carries can be answered.
#
# On the listenerless roads (OpenCode, scrape-road Codex) the wake-up types the
# whole inbox as one payload. Before the fix its reply instruction named only
# the last request, so an agent handed requests A and B answered B and A's
# task expired at task-timeout with the answer on screen (2026-09-18). The fix
# (bus/provider.go withReplyInstructions) lists one reply command per request,
# each to its own reply target with its own --reply-to.
#
# Hermetic: scratch bus, private tmux server, muxcode built from THIS tree.
# The wake is a `muxcode deliver build --force` after the requests were queued
# with --no-notify, so they arrive as one batch; a scratch daemon starts only
# once the batch is answered, to complete the tracked tasks as it does live. build is an
# OpenCode role whose pane runs a fake agent: it draws an OpenCode-shaped
# composer, logs every line typed into it, and executes every reply command
# the payload lists, as written — an agent that does exactly what it is told.
# Each request is tracked (the send default), so its task records whether it
# was answered.
#
# The pre-fix build is this tree with withReplyInstructions cut down to the
# batch's last request in a scratch copy, so the control differs from the fix
# by that one line.
#
# Sections:
#   A  requests from edit and plan in one batch: one typed payload naming both
#      ids, each reply command to its own target, before and after the
#      payload; both tasks complete and both requests read responded
#   B  negative control — a single request: one reply command, in the
#      unchanged single-request form, and its task completes
#   C  control — the A batch on the pre-fix build: only the last request is
#      named, and the first one's task is still in flight after the agent
#      answered everything it was told to — the 2026-09-18 timeout
# A coverage floor keeps a skipped section from reporting green.
# Requires go, tmux, jq.
set -uo pipefail

PASS=0
FAIL=0
EXPECTED_PASS=23
for t in tmux go jq; do
  command -v "$t" >/dev/null 2>&1 || { echo "SKIP: $t is required"; exit 2; }
done

REPO=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
WORK=$(mktemp -d /tmp/batch-reply-XXXXXX)
. "$REPO/scripts/lib/muxcode-bus-dir.sh"
SESSIONS=()

unset AGENT_ROLE BUS_ROLE BUS_SESSION TMUX MUXCODE_CONFIG MUXCODE_AGENT_CLI MUXCODE_SEND_DEFAULT_FIRE_AND_FORGET
export HOME="$WORK/home"
export XDG_STATE_HOME="$WORK/home/.local/state"
export TMUX_TMPDIR="$WORK/tmux"
export MUXCODE_LIFECYCLE_LOG_DIR="$WORK/lifecycle"
export MUXCODE_BUILD_CLI=opencode
export MUXCODE_DEDUP_WINDOW=0 MUXCODE_RELAY_SUPPRESS_THRESHOLD=1000 MUXCODE_BRANCH_TIME_DISABLE=1
export MUXCODE_EDIT_AUTO_RESTART_DISABLE=1 MUXCODE_FORCE_RESPOND_DISABLE=1 MUXCODE_CONTROL_PANE_DISABLE=1
export MUXCODE_AGENTDEFS_WATCH_DISABLE=1 MUXCODE_ACTIVE_WATCHDOG_SECS=0 MUXCODE_STUCK_RELOAD_DISABLE=1
export MUXCODE_TMP_CLEANUP_THRESHOLD=0 MUXCODE_PROMPT_AGENT_DISABLE=1
mkdir -p "$WORK/bin" "$WORK/bin-pre" "$WORK/home" "$WORK/tmux" "$WORK/lifecycle" "$WORK/project"
cd "$WORK/project" || exit 1

DPID=""
# start_daemon BIN — a scratch daemon for the current session, started only
# once its batch was typed and answered: tracked tasks are completed by the
# daemon (checkTrackedTasks), and a daemon running while the requests queue
# could wake build between them and split the batch.
start_daemon() {
  (exec "$1" watch "$SESSION" --poll 1 >>"$WORK/daemon-$SESSION.log" 2>&1) &
  DPID=$!
}
stop_daemon() {
  if [ -n "$DPID" ]; then kill "$DPID" 2>/dev/null; wait "$DPID" 2>/dev/null; DPID=""; fi
}
cleanup() {
  local s
  stop_daemon
  tmux kill-server 2>/dev/null
  for s in "${SESSIONS[@]:-}"; do
    [ -n "$s" ] && rm -rf "$(muxcode_bus_dir "$MUX" "$s" 2>/dev/null)"
  done
  rm -rf "$WORK"
  true
}
trap cleanup EXIT

ok()   { PASS=$((PASS + 1)); echo "  ok: $1"; }
fail() { FAIL=$((FAIL + 1)); echo "  FAIL: $1"; }

(cd "$REPO/tools/muxcode" && go build -buildvcs=false -o "$WORK/bin/muxcode" .) \
  || { echo "SKIP: go build muxcode failed"; exit 2; }
MUX="$WORK/bin/muxcode"
ok "scratch muxcode built from this tree"

mkdir -p "$WORK/pre"
cp -R "$REPO/tools/muxcode" "$WORK/pre/"
sed -i.orig 's/	cmds := make(\[\]string, len(requests))/	requests = requests[len(requests)-1:]; cmds := make([]string, len(requests))/' \
  "$WORK/pre/muxcode/bus/provider.go"
if [ "$(grep -c 'requests = requests\[len(requests)-1:\]; cmds' "$WORK/pre/muxcode/bus/provider.go")" = 1 ] \
  && (cd "$WORK/pre/muxcode" && go build -buildvcs=false -o "$WORK/bin-pre/muxcode" .); then
  ok "pre-fix muxcode built: withReplyInstructions names the batch's last request only"
else
  fail "pre-fix build: the last-request patch did not apply exactly once, or the copy failed to build"
fi
MUX_PRE="$WORK/bin-pre/muxcode"

# fake-agent SESSION BIN LOG — an OpenCode-shaped composer that logs each typed
# line, redraws at once (so the injection confirm sees the composer empty),
# then executes every distinct reply command the line lists, target and id as
# written.
cat >"$WORK/fake-agent.sh" <<'EOF'
#!/usr/bin/env bash
session=$1 bin=$2 log=$3
export BUS_SESSION=$session
frame() {
  printf '\n%.0s' $(seq 1 45)
  printf '╭────────────────────────────────╮\n│ >                              │\n╰────────────────────────────────╯\n'
}
: >"$log"
# Non-canonical: a canonical tty splits an injected prompt longer than
# MAX_CANON (1024 bytes on macOS) into several reads.
stty -icanon min 1 time 0 2>/dev/null
frame
while IFS= read -r line; do
  frame
  [ -n "$line" ] || continue # the injection confirm's bare Enter
  printf '%s\n' "$line" >>"$log"
  printf '%s' "$line" \
    | grep -oE 'muxcode send [a-z-]+ response "<your one-line summary>" --type response --reply-to [0-9A-Za-z_-]+' \
    | sort -u | while read -r _ _ target _ _ _ _ _ _ _ id; do
        AGENT_ROLE=build "$bin" send "$target" response "Done. EXIT=0" --type response --reply-to "$id" >/dev/null 2>&1
      done
done
EOF
chmod +x "$WORK/fake-agent.sh"

# new_session NAME BIN — a private tmux session whose build window runs the
# fake agent in its agent pane (.1); sets SESSION, BUSDIR and TYPED.
new_session() {
  SESSION="batch-$1-$$"
  SESSIONS+=("$SESSION")
  export BUS_SESSION="$SESSION"
  BUSDIR="$(muxcode_bus_dir "$MUX" "$SESSION")" || exit 1
  TYPED="$WORK/typed-$1.log"
  "$2" init "$SESSION" >/dev/null 2>&1 || true
  tmux new-session -d -s "$SESSION" -n edit -x 200 -y 40 -c "$WORK/project" "sleep 100000"
  tmux new-window -t "$SESSION" -n build -c "$WORK/project" "sleep 100000"
  tmux split-window -h -t "$SESSION:build" -c "$WORK/project" "bash $WORK/fake-agent.sh $SESSION $2 $TYPED"
  sleep 1
}

# queue BIN FROM ACTION PAYLOAD — a tracked request to build, no wake; prints its id.
queue() {
  AGENT_ROLE=$2 "$1" send build "$3" "$4" --no-notify >/dev/null 2>&1
  jq -r --arg p "$4" 'select(.payload == $p) | .id' "$BUSDIR/inbox/build.jsonl" 2>/dev/null | tail -1
}
typed_lines()   { wc -l <"$TYPED" 2>/dev/null | tr -d ' '; }
typed_once()    { [ "$(typed_lines)" -ge 1 ]; }
task_status()   { jq -r '.status' "$BUSDIR/tasks/$1.json" 2>/dev/null; }
task_done()     { [ "$(task_status "$1")" = completed ]; }
responded()     { [ "$(jq -r '.status' "$BUSDIR/delivery/$1.status" 2>/dev/null)" = responded ]; }
reply_cmd()     { printf 'muxcode send %s response "<your one-line summary>" --type response --reply-to %s' "$1" "$2"; }
count_in_typed() { grep -oF -- "$1" "$TYPED" 2>/dev/null | wc -l | tr -d ' '; }
distinct_replies() { grep -oE -- '--reply-to [0-9A-Za-z_-]+' "$TYPED" 2>/dev/null | sort -u | wc -l | tr -d ' '; }

wait_for() {
  local secs=$1 desc=$2 i=0
  shift 2
  while [ "$i" -lt $((secs * 2)) ]; do
    if "$@" >/dev/null 2>&1; then ok "$desc"; return 0; fi
    sleep 0.5; i=$((i + 1))
  done
  fail "$desc (timeout ${secs}s)"
  return 1
}

echo "=== MUX-189 batch reply correlation ==="

# ── A: two requests, one batch, this build ─────────────────────────
echo "-- A: requests from edit and plan delivered in one batch"
new_session a "$MUX"
ID1=$(queue "$MUX" plan docs "Rebuild the docs site BATCH-FIRST")
ID2=$(queue "$MUX" edit build "Run the build BATCH-SECOND")
[ -n "$ID1" ] && [ -n "$ID2" ] && task_status "$ID1" >/dev/null && [ "$(task_status "$ID1")" = in-flight ] \
  && [ "$(task_status "$ID2")" = in-flight ] && ok "A: two tracked requests queued for build ($ID1, $ID2)" \
  || fail "A: queued '$ID1' '$ID2', tasks '$(task_status "$ID1")' '$(task_status "$ID2")'"
"$MUX" deliver build --force >"$WORK/deliver-a.out" 2>&1
wait_for 15 "A: the batch was typed into build's pane" typed_once
[ "$(typed_lines)" = 1 ] && grep -q BATCH-FIRST "$TYPED" && grep -q BATCH-SECOND "$TYPED" \
  && ok "A: both requests arrived in one typed payload" || fail "A: typed: $(cat "$TYPED")"
[ "$(count_in_typed "$(reply_cmd plan "$ID1")")" = 2 ] \
  && ok "A: plan's request has its own reply command, to plan, before and after the payload" \
  || fail "A: plan's reply command appears $(count_in_typed "$(reply_cmd plan "$ID1")") time(s)"
[ "$(count_in_typed "$(reply_cmd edit "$ID2")")" = 2 ] \
  && ok "A: edit's request has its own reply command, to edit, before and after the payload" \
  || fail "A: edit's reply command appears $(count_in_typed "$(reply_cmd edit "$ID2")") time(s)"
grep -q "This message holds 2 requests" "$TYPED" && ok "A: the payload says how many replies it owes" \
  || fail "A: no request count in: $(cat "$TYPED")"
both_responded() { responded "$ID1" && responded "$ID2"; }
wait_for 15 "A: both requests read responded" both_responded
start_daemon "$MUX"
wait_for 20 "A: plan's task completed" task_done "$ID1"
wait_for 20 "A: edit's task completed" task_done "$ID2"
stop_daemon
inflight=$("$MUX" tasks 2>/dev/null | grep -cE "$ID1|$ID2" || true)
[ "$inflight" = 0 ] && ok "A: muxcode tasks lists neither as in flight" || fail "A: muxcode tasks: $("$MUX" tasks 2>&1)"

# ── B: negative control — one request ──────────────────────────────
echo "-- B: a single request (negative control)"
new_session b "$MUX"
ID=$(queue "$MUX" edit build "Run the build SINGLE")
"$MUX" deliver build --force >"$WORK/deliver-b.out" 2>&1
wait_for 15 "B: the request was typed into build's pane" typed_once
[ "$(distinct_replies)" = 1 ] && [ "$(count_in_typed "$(reply_cmd edit "$ID")")" = 2 ] \
  && ok "B: exactly one reply command, naming its request, before and after the payload" \
  || fail "B: typed: $(cat "$TYPED")"
grep -qF "IMPORTANT: After completing this task, you MUST run this bash command: $(reply_cmd edit "$ID") — " "$TYPED" \
  && ! grep -q "holds" "$TYPED" && ok "B: the single-request form is unchanged" || fail "B: typed: $(cat "$TYPED")"
wait_for 15 "B: its request read responded" responded "$ID"
start_daemon "$MUX"
wait_for 20 "B: its task completed" task_done "$ID"
stop_daemon

# ── C: control — the pre-fix build names the last request alone ────
echo "-- C: control — the A batch on the pre-fix build"
new_session c "$MUX_PRE"
ID1=$(queue "$MUX_PRE" plan docs "Rebuild the docs site BATCH-FIRST")
ID2=$(queue "$MUX_PRE" edit build "Run the build BATCH-SECOND")
[ -n "$ID1" ] && [ -n "$ID2" ] && ok "C: two tracked requests queued for build" || fail "C: queued '$ID1' '$ID2'"
"$MUX_PRE" deliver build --force >"$WORK/deliver-c.out" 2>&1
wait_for 15 "C: the batch was typed into build's pane" typed_once
grep -q BATCH-FIRST "$TYPED" && [ "$(count_in_typed "--reply-to $ID1")" = 0 ] && [ "$(distinct_replies)" = 1 ] \
  && ok "C: the payload carried the first request but named only the last" || fail "C: typed: $(cat "$TYPED")"
wait_for 15 "C: the last request read responded" responded "$ID2"
start_daemon "$MUX_PRE"
wait_for 20 "C: the last request's task completed" task_done "$ID2"
sleep 3 # a daemon pass or two with the first task still open
stop_daemon
[ "$(task_status "$ID1")" = in-flight ] && ! responded "$ID1" \
  && ok "C: the first request's task is still in flight though the agent did all it was told — the 2026-09-18 timeout" \
  || fail "C: first task '$(task_status "$ID1")'"

echo
echo "=== results: $PASS passed, $FAIL failed (floor $EXPECTED_PASS) ==="
if [ "$PASS" -ne "$EXPECTED_PASS" ]; then
  echo "  FAIL  coverage floor: expected exactly $EXPECTED_PASS passes, got $PASS — a section was skipped or double-counted"
  exit 1
fi
[ "$FAIL" -eq 0 ] || exit 1
exit 0
