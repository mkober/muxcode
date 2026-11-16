#!/usr/bin/env bash
# Integration test for MUX-145: messages to a role nothing can read are refused
# at Send, diagnose names the absence, and a windowless role is never reloaded.
#
# On 2026-09-03 the daemon routed 11 analyze events over 4.6 hours to a role
# with no window; diagnose called the pile a receipt-gap and prescribed
# `muxcode deliver analyze --force` into a pane that did not exist, and on
# 2026-09-08 a reload of the same role failed "did not exit after 12 seconds".
# Everything below runs a tree-built muxcode against a scratch daemon on a
# private tmux server whose session has windows edit, test (a ❯ stand-in that
# reads nothing) and build (a claude-stub agent with its own listener), the
# agent in pane 1 as LaunchSession lays it out:
#
#   A. No consumer for analyze: two edit-stabilization cycles leave analyze's
#      inbox empty with one send-no-consumer row and one REFUSED line in the
#      daemon log; `muxcode send analyze …` prints NOT SENT and exits 1.
#   B. Negative control — an analyze window whose pane reads the inbox: the
#      next cycle's event lands and is consumed, receipted by analyze.
#   C. Negative control — no analyze window but its own `inbox --poll --loop`
#      listener: a send lands and the listener consumes it.
#   D. diagnose analyze, stranded events backdated: no-consumer naming the
#      launch and stop-routing remediations, no receipt-gap, no deliver.
#   E. Negative control — diagnose test, a windowed role holding un-receipted
#      events: still receipt-gap prescribing deliver, no no-consumer.
#   F. reload analyze: fails at once naming the absence, no reload marker.
#   G. Negative control — reload build: the claude-stub is stopped and
#      relaunched (a launch row with reason=reload), its listener back.
#   H. A mode role gets no exception (the user's decision of 2026-11-16):
#      research, never cycled to, refuses a send (NOT SENT, exit 1, nothing
#      written); once its hold window exists — the window mode cycling
#      creates and keeps by name — the same send lands.
#   I. Negative control — a real launch: `muxcode launch` (LaunchSession) of a
#      Go project with windows edit, build and test, claude-stub agents and
#      the guard on, seeds every role's startup and edit's project-init — sent
#      before the tmux session exists — with no send-no-consumer row and no
#      refusal. The launcher's final attach fails with no terminal, after the
#      launch is done.
#
# ISOLATION: scratch HOME, lifecycle log, install dir and project; muxcode and
# the claude-stub built from this tree; a private tmux server (TMUX_TMPDIR in
# the work dir, TMUX unset, killed on exit). The analyze trigger file lives in
# /tmp by design (bus.TriggerFile) and is removed on exit. MUXCODE_CONSUMER_GUARD
# is unset: the refusal is what this script tests.
#
# REQUIRES: go, tmux, jq. ~1 min.
#
# Usage: bash scripts/test-windowless-routing.sh
set -uo pipefail

FLOOR=25
PASS=0
FAIL=0
for t in tmux go jq; do
  command -v "$t" >/dev/null 2>&1 || { echo "SKIP: $t is required"; exit 2; }
done

REPO=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
WORK=$(mktemp -d /tmp/windowless-routing-XXXXXX)
SESSION="windowless-routing-$$"

unset AGENT_ROLE BUS_ROLE TMUX TMUX_PANE MUXCODE_CONFIG MUXCODE_CONSUMER_GUARD MUXCODE_BUS_BASE MUXCODE_BUS_DIR
export BUS_SESSION="$SESSION"
export HOME="$WORK/home"
export TMUX_TMPDIR="$WORK/tmux"
export MUXCODE_LIFECYCLE_LOG_DIR="$WORK/lifecycle"
export MUXCODE_INSTALL_DIR="$WORK/install"
export BUS_MEMORY_DIR="$WORK/memory" BUS_GLOBAL_MEMORY_DIR="$WORK/global-memory"
export MUXCODE_AGENT_CLI=claude MUXCODE_BUILD_CLI=claude MUXCODE_TEST_CLI=claude MUXCODE_ANALYZE_CLI=opencode
export MUXCODE_AGENT_HEALTH_CHECK_SECS=86400 MUXCODE_AUTO_RESUME_DISABLE=1 MUXCODE_AGENT_HEARTBEAT=86400
export MUXCODE_EDIT_AUTO_RESTART_DISABLE=1 MUXCODE_CONTROL_PANE_DISABLE=1 MUXCODE_FORCE_RESPOND_DISABLE=1
export MUXCODE_TMP_CLEANUP_THRESHOLD=0 MUXCODE_ACTIVE_WATCHDOG_SECS=0 MUXCODE_PROMPT_AGENT_DISABLE=1
export MUXCODE_BRANCH_TIME_DISABLE=1 MUXCODE_STUCK_RELOAD_DISABLE=1 MUXCODE_DEDUP_WINDOW=0
export MUXCODE_AGENTDEFS_WATCH_DISABLE=1
export PATH="$WORK/bin:$PATH"
mkdir -p "$WORK/bin" "$WORK/home" "$WORK/tmux" "$WORK/lifecycle" "$WORK/install/agents" "$WORK/project"
LIFELOG="$MUXCODE_LIFECYCLE_LOG_DIR/$SESSION.log"
TRIGGER="/tmp/muxcode-analyze-$SESSION.trigger"
BUSDIR=""

DPID="" LPID=""
cleanup() {
  [ -n "$LPID" ] && kill "$LPID" 2>/dev/null
  [ -n "$DPID" ] && kill "$DPID" 2>/dev/null
  [ -n "${L:-}" ] && pkill -f "muxcode (watch|launch --resize|launch --auto-accept)( --monitor)? $L" 2>/dev/null
  tmux kill-server 2>/dev/null
  rm -f "$TRIGGER"
  rm -rf ${BUSDIR:+"$BUSDIR"} ${LBUS:+"$LBUS"} "$WORK" 2>/dev/null
  true
}
trap cleanup EXIT

ok()   { PASS=$((PASS + 1)); echo "  ok: $1"; }
fail() { FAIL=$((FAIL + 1)); echo "  FAIL: $1"; }

# wait_for SECS CHECK [ARGS...] — polls every 0.25 s; status 0 once CHECK holds.
wait_for() {
  local n=$(($1 * 4)) i; shift
  for ((i = 0; i < n; i++)); do "$@" >/dev/null 2>&1 && return 0; sleep 0.25; done
  return 1
}
pane_pid()       { tmux display-message -p -t "$SESSION:$1.1" '#{pane_pid}' 2>/dev/null; }
stub_pid()       { pgrep -P "$(pane_pid "$1" || echo 0)" -x claude 2>/dev/null | head -1 || true; }
stub_up()        { local p; p=$(stub_pid "$1"); [ -n "$p" ] && [ "$p" != "${2:-}" ]; }
listener_alive() { local pid; pid=$(cat "$BUSDIR/polling-$1.marker" 2>/dev/null) && [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; }
no_listener()    { ! listener_alive "$1"; }
type_in()        { tmux send-keys -t "$SESSION:$1.1" -l -- "$2"; sleep 0.1; tmux send-keys -t "$SESSION:$1.1" Enter; }
pane_env()       { echo "export AGENT_ROLE=$1 BUS_ROLE=$1 BUS_SESSION=$SESSION HOME=$HOME TMUX_TMPDIR=$TMUX_TMPDIR MUXCODE_LIFECYCLE_LOG_DIR=$MUXCODE_LIFECYCLE_LOG_DIR MUXCODE_INSTALL_DIR=$MUXCODE_INSTALL_DIR MUXCODE_AGENT_CLI=claude MUXCODE_BUILD_CLI=claude MUXCODE_TEST_CLI=claude PATH=$PATH; PS1='\$ '; clear"; }
rows()           { jq -r --arg e "$1" 'select(.event == $e) | .detail' "$LIFELOG" 2>/dev/null | grep -E -- "$2" || true; }
row_count()      { rows "$1" "$2" | grep -c . || true; }
count_in()       { local n; n=$(grep -c -- "$1" "$2" 2>/dev/null); echo "${n:-0}"; }
inbox_rows()     { count_in . "$BUSDIR/inbox/$1.jsonl"; }
cycles()         { count_in 'Edits stabilized' "$WORK/daemon.log"; }
# edit_cycle — one edit-stabilization cycle: a trigger line, then the route.
edit_cycle() {
  local before; before=$(cycles)
  echo "$(date +%s) $WORK/project/file$RANDOM.go" >>"$TRIGGER"
  wait_for 15 eval '[ "$(cycles)" -gt '"$before"' ]'
  sleep 0.5
}
# backdate ROLE SECS — move every message in ROLE's inbox SECS into the past.
backdate() {
  local f="$BUSDIR/inbox/$1.jsonl"
  jq -c --argjson s "$2" '.ts -= $s' "$f" >"$f.tmp" && mv "$f.tmp" "$f"
}

echo "=== MUX-145 windowless routing ==="
(cd "$REPO/tools/muxcode" && go build -buildvcs=false -o "$WORK/bin/muxcode" .) \
  && (cd "$REPO/scripts/fixtures/claude-stub" && go build -buildvcs=false -o "$WORK/bin/claude" .) \
  && ok "muxcode and claude-stub built from this tree" || { fail "build failed"; exit 1; }
MUX="$WORK/bin/muxcode"
BUSDIR=$("$MUX" bus-dir "$SESSION") || exit 1
printf -- '---\ndescription: Builder\n---\nBuild things.\n' >"$WORK/install/agents/code-builder.md"

tmux new-session -d -s "$SESSION" -n edit -x 160 -y 40 -c "$WORK/project" "/bin/bash --noprofile --norc"
tmux set-option -g default-command "/bin/bash --noprofile --norc"
tmux new-window -d -t "$SESSION" -n test -c "$WORK/project"
tmux split-window -d -h -t "$SESSION:test" -c "$WORK/project" "printf '\n❯ '; exec cat"
tmux new-window -d -t "$SESSION" -n build -c "$WORK/project"
tmux split-window -d -h -t "$SESSION:build" -c "$WORK/project"
type_in build "$(pane_env build)"
sleep 1
"$MUX" init "$SESSION" >/dev/null 2>&1 || true
(cd "$WORK/project" && exec "$MUX" watch "$SESSION" --poll 1 --debounce 1 >>"$WORK/daemon.log" 2>&1) &
DPID=$!
sleep 1
kill -0 "$DPID" 2>/dev/null && ok "scratch daemon running" || fail "daemon exited: $(tail -3 "$WORK/daemon.log")"
type_in build "muxcode agent launch build --reason user"
wait_for 20 stub_up build && ok "build agent (claude-stub) up" || fail "build agent never came up"
wait_for 15 listener_alive build && ok "build agent's listener running" || fail "build listener never started"

# ── A: no consumer for analyze ──────────────────────────────────────
echo "-- A: no consumer — the analyze route and the CLI are refused"
edit_cycle; edit_cycle
[ "$(cycles)" = 2 ] && [ "$(inbox_rows analyze)" = 0 ] && ok "A: two edit cycles routed, analyze's inbox stays empty" \
  || fail "A: cycles $(cycles), analyze inbox rows $(inbox_rows analyze)"
[ "$(row_count send-no-consumer '^analyze:')" = 1 ] && ok "A: one send-no-consumer row for both refusals" \
  || fail "A: $(row_count send-no-consumer '^analyze:') send-no-consumer rows"
[ "$(grep -c 'REFUSED .*analyze has no consumer' "$WORK/daemon.log")" = 1 ] && ! grep -q 'failed to send analyze event' "$WORK/daemon.log" \
  && ok "A: one REFUSED line in the daemon log, no route failure line" || fail "A: daemon log REFUSED lines: $(grep -c 'REFUSED .*analyze' "$WORK/daemon.log")"
out=$(AGENT_ROLE=edit "$MUX" send analyze analyze "explain the change" 2>&1); rc=$?
[ $rc = 1 ] && grep -q 'NOT SENT' <<<"$out" && [ "$(inbox_rows analyze)" = 0 ] && ok "A: muxcode send prints NOT SENT, exit 1, nothing written" \
  || fail "A: CLI exit $rc: $out"

# ── B: an analyze window whose pane reads the inbox ─────────────────
echo "-- B: negative control — an analyze window that reads"
tmux new-window -d -t "$SESSION" -n analyze -c "$WORK/project" \
  "$(pane_env analyze); while :; do muxcode inbox >>'$WORK/analyze-read.out' 2>/dev/null; sleep 1; done"
edit_cycle
read_event() { grep -q 'Claude edited files' "$WORK/analyze-read.out" 2>/dev/null; }
wait_for 10 read_event && ok "B: the event routed to analyze and its pane read it" || fail "B: the analyze pane never read the event"
rid=$(jq -r 'select(.to == "analyze" and .type == "event") | .id' "$BUSDIR/log.jsonl" 2>/dev/null | tail -1)
[ -n "$rid" ] && [ "$(jq -r '.acked_by' "$BUSDIR/delivery/$rid.status" 2>/dev/null)" = analyze ] && [ "$(inbox_rows analyze)" = 0 ] \
  && ok "B: its receipt names analyze, inbox empty" || fail "B: event ${rid:-none}, receipt $(cat "$BUSDIR/delivery/$rid.status" 2>/dev/null)"
tmux kill-window -t "$SESSION:analyze"

# ── C: no window, but analyze's own listener ────────────────────────
echo "-- C: negative control — a windowless analyze with its own listener"
(AGENT_ROLE=analyze exec "$MUX" inbox --poll --loop >"$WORK/listener.out" 2>&1) &
LPID=$!
wait_for 10 listener_alive analyze
out=$(AGENT_ROLE=edit "$MUX" send analyze analyze "listener please" --no-notify 2>&1); rc=$?
[ $rc = 0 ] && ok "C: the send to a listening windowless role is delivered" || fail "C: send exit $rc: $out"
cid=$(jq -r 'select(.to == "analyze" and .payload == "listener please") | .id' "$BUSDIR/log.jsonl" 2>/dev/null | tail -1)
heard() { grep -q 'listener please' "$WORK/listener.out"; }
wait_for 10 heard && grep -q "\"acked_pid\":$LPID[,}]" "$BUSDIR/delivery/$cid.status" 2>/dev/null \
  && ok "C: the listener consumed it, receipt naming pid $LPID" || fail "C: listener output '$(head -2 "$WORK/listener.out")', receipt $(cat "$BUSDIR/delivery/$cid.status" 2>/dev/null)"
wait_for 10 no_listener analyze; LPID=""

# ── D: diagnose a role with no consumer ─────────────────────────────
echo "-- D: diagnose analyze — no-consumer, not receipt-gap"
for i in 1 2; do AGENT_ROLE=edit MUXCODE_CONSUMER_GUARD=0 "$MUX" send analyze analyze "stranded $i" --type event --no-notify >/dev/null 2>&1; done
backdate analyze 400
out=$("$MUX" diagnose analyze 2>&1); rc=$?
grep -q 'no-consumer' <<<"$out" && ! grep -q 'receipt-gap' <<<"$out" && ! grep -q 'muxcode deliver' <<<"$out" && [ $rc != 0 ] \
  && ok "D: no-consumer (critical, exit $rc), no receipt-gap, no deliver" || { fail "D: diagnose exit $rc"; echo "$out" | sed 's/^/      /' | head -25; }
grep -q 'MUXCODE_WINDOWS' <<<"$out" && grep -qi 'stop routing' <<<"$out" && ok "D: remediation names launching the role and stopping the routing" \
  || fail "D: remediation lacks launch / stop-routing"

# ── E: diagnose a windowed role with un-receipted messages ──────────
echo "-- E: negative control — diagnose test keeps receipt-gap"
for i in 1 2; do AGENT_ROLE=edit "$MUX" send test test "unread $i" --type event --no-notify >/dev/null 2>&1; done
backdate test 400
out=$("$MUX" diagnose test 2>&1); rc=$?
grep -q 'receipt-gap' <<<"$out" && grep -q 'muxcode deliver test' <<<"$out" && ! grep -q 'no-consumer' <<<"$out" \
  && ok "E: receipt-gap with deliver, no no-consumer (exit $rc)" || { fail "E: diagnose test exit $rc"; echo "$out" | sed 's/^/      /' | head -25; }

# ── F: reload a windowless role ─────────────────────────────────────
echo "-- F: reload analyze fails fast"
start=$SECONDS
out=$("$MUX" reload analyze 2>&1); rc=$?
elapsed=$((SECONDS - start))
[ $rc != 0 ] && [ "$elapsed" -le 3 ] && grep -q 'has no window' <<<"$out" && ! grep -q 'did not exit' <<<"$out" \
  && ok "F: refused in ${elapsed}s, exit $rc, naming the absence" || fail "F: exit $rc after ${elapsed}s: $out"
[ ! -e "$BUSDIR/lock/analyze.reloading" ] && ok "F: no reload marker written" || fail "F: a reload marker was written"

# ── G: reload a windowed role ───────────────────────────────────────
echo "-- G: negative control — reload build relaunches its agent"
old=$(stub_pid build)
out=$("$MUX" reload build 2>&1); rc=$?
[ $rc = 0 ] && wait_for 20 eval '[ "$(row_count launch "role=build .*reason=reload")" -ge 1 ]' \
  && ok "G: reload build exit 0, relaunched (reason=reload)" || fail "G: reload build exit $rc: $out"
wait_for 20 stub_up build "$old" && wait_for 15 listener_alive build && [ ! -e "$BUSDIR/lock/build.reloading" ] \
  && ok "G: a new claude-stub (was $old, now $(stub_pid build)) with its listener, marker released" || fail "G: stub $(stub_pid build) (was $old), marker $(ls "$BUSDIR/lock" 2>/dev/null)"

# ── H: a mode role never cycled to ──────────────────────────────────
echo "-- H: research never cycled to is refused; its hold window admits it"
out=$(AGENT_ROLE=edit "$MUX" send research research "look this up" --no-notify 2>&1); rc=$?
[ $rc = 1 ] && grep -q 'NOT SENT' <<<"$out" && [ "$(inbox_rows research)" = 0 ] \
  && ok "H: never cycled to — NOT SENT, exit 1, nothing in research's inbox" || fail "H: never-cycled send exit $rc: $out"
tmux new-window -d -t "$SESSION" -n research -c "$WORK/project"
out=$(AGENT_ROLE=edit "$MUX" send research research "look this up" --no-notify 2>&1); rc=$?
[ $rc = 0 ] && [ "$(inbox_rows research)" = 1 ] && ok "H: with its hold window present the send lands" \
  || fail "H: send with the research window exit $rc, inbox rows $(inbox_rows research): $out"

# ── I: a real session launch is never refused ───────────────────────
echo "-- I: negative control — LaunchSession seeds every role, nothing refused"
L="windowless-launch-$$"
LBUS=$("$MUX" bus-dir "$L")
LLOG="$MUXCODE_LIFECYCLE_LOG_DIR/$L.log"
mkdir -p "$WORK/launchproj"
printf 'module launchproj\n\ngo 1.22\n' >"$WORK/launchproj/go.mod"
printf -- '---\ndescription: Editor\n---\nEdit things.\n' >"$WORK/install/agents/code-editor.md"
printf -- '---\ndescription: Tester\n---\nRun the suite.\n' >"$WORK/install/agents/test-runner.md"
(cd "$WORK/launchproj" && MUXCODE_WINDOWS="edit build test" BUS_SESSION="$L" EDITOR=true \
  "$MUX" launch "$WORK/launchproj" "$L" >"$WORK/launch.out" 2>&1 </dev/null)
startups() { jq -r 'select(.type == "request" and .action == "startup") | .to' "$LBUS/log.jsonl" 2>/dev/null | sort -u | tr '\n' ' '; }
all_seeded() { [ "$(startups)" = "build edit test " ]; }
lw=$(tmux list-windows -t "$L" -F '#W' 2>/dev/null | sort | tr '\n' ' ')
[ "$lw" = "build edit test " ] && ok "I: the launcher created the session with windows $lw" \
  || fail "I: launched windows '$lw': $(tail -3 "$WORK/launch.out")"
wait_for 40 all_seeded && ok "I: every role's startup was seeded: $(startups)" || fail "I: startups seeded to '$(startups)'"
jq -e 'select(.type == "request" and .action == "project-init" and .to == "edit")' "$LBUS/log.jsonl" >/dev/null 2>&1 \
  && ok "I: edit's project-init, sent before the tmux session existed, landed" || fail "I: no project-init request reached edit"
nref=$(jq -r 'select(.event == "send-no-consumer") | .detail' "$LLOG" 2>/dev/null | grep -c . || true)
[ "${nref:-0}" = 0 ] && ! grep -q 'NOT SENT\|no consumer' "$WORK/launch.out" \
  && ok "I: no send-no-consumer row and no refusal during the launch" || fail "I: $nref send-no-consumer rows: $(jq -r 'select(.event == "send-no-consumer") | .detail' "$LLOG" | head -3)"
pkill -f "muxcode (watch|launch --resize|launch --auto-accept)( --monitor)? $L" 2>/dev/null
tmux kill-session -t "$L" 2>/dev/null

total=$((PASS + FAIL))
echo
echo "=== test-windowless-routing: $PASS passed, $FAIL failed ($total checks, floor $FLOOR) ==="
[ "$total" -lt "$FLOOR" ] && { echo "  FAIL: only $total checks ran (floor $FLOOR) — a section was skipped"; exit 1; }
[ "$FAIL" = 0 ]
