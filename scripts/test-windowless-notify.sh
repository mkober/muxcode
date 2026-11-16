#!/usr/bin/env bash
# Integration test for MUX-180: the daemon stops notifying a role nothing can
# read for, stops routing work to it, and every inbox is bounded at 50.
#
# On 2026-09-10/11 a windowless analyze drew an injection-refused row every
# ~51 s for 28 h, moved the workflow into `analyzing` each cycle, and held 109
# messages. MUX-145's scripts/test-windowless-routing.sh already covers the
# send refusal and its single row; this script adds the notify road, the
# workflow route, the recovery and the cap. A tree-built daemon runs on a
# private tmux server, with analyze on opencode (the road that injects):
#
#   A. No consumer: a stranded backlog (a request, which the daemon notifies
#      for, and two events, sent with the guard off) and two
#      edit-stabilization cycles over ~30 s give zero injection-refused rows,
#      no trigger-analyze.notify, no `analyzing` transition, and exactly one
#      notify-no-consumer and one route-no-consumer row.
#   B. Negative control — the window appears with a pane that reads analyze's
#      inbox: the backlog drains, receipted by analyze, and the next cycle
#      routes again — an `analyzing` transition, the trigger written, the
#      event consumed. Nothing is permanently blacklisted.
#   C. Negative control — no window but analyze's own `inbox --poll --loop`
#      listener: an edit cycle routes, the trigger is written and the listener
#      consumes the event. A window is not the test.
#   D. The cap: 50 sends to build land with one inbox-full row and one alert
#      to edit; the 51st prints NOT SENT and exits 1; all 50 are still queued.
#
# ISOLATION: muxcode built from the tree; scratch HOME, state, lifecycle log,
# memory and config; a private tmux server (TMUX_TMPDIR in the work dir, TMUX
# unset, killed on exit); the analyze trigger files live in /tmp by design and
# are removed on exit. MUXCODE_CONSUMER_GUARD is unset but where a backlog is
# stranded on purpose.
#
# REQUIRES: go, tmux, jq. ~1.5 min. Run in the foreground (MUX-171).
#
# Usage: bash scripts/test-windowless-notify.sh
set -uo pipefail

FLOOR=10
PASS=0
FAIL=0
ok()   { PASS=$((PASS + 1)); echo "  ok: $1"; }
fail() { FAIL=$((FAIL + 1)); echo "  FAIL: $1"; }
for t in go tmux jq; do command -v "$t" >/dev/null 2>&1 || { echo "SKIP: $t is required"; exit 2; }; done

REPO=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
WORK=$(mktemp -d /tmp/windowless-notify-XXXXXX)
mkdir -p "$WORK/tmux"
export TMUX_TMPDIR="$WORK/tmux"
unset TMUX TMUX_PANE AGENT_ROLE BUS_ROLE BUS_SESSION MUXCODE_BUS_BASE MUXCODE_BUS_DIR MUXCODE_CONSUMER_GUARD MUXCODE_CONFIG
PIDS=""
SESSIONS=""
cleanup() {
  local p s
  for p in $PIDS; do kill "$p" 2>/dev/null; done
  tmux kill-server 2>/dev/null
  for s in $SESSIONS; do rm -f "/tmp/muxcode-analyze-$s.trigger"; done
  rm -rf "$WORK"
}
trap cleanup EXIT
GOCACHE="$(go env GOCACHE)"; GOMODCACHE="$(go env GOMODCACHE)"
export GOCACHE GOMODCACHE
export HOME="$WORK/home" XDG_STATE_HOME="$WORK/home/.local/state" MUXCODE_LIFECYCLE_LOG_DIR="$WORK/lifecycle"
export MUXCODE_CONFIG="$WORK/empty-config" BUS_MEMORY_DIR="$WORK/memory" BUS_GLOBAL_MEMORY_DIR="$WORK/global-memory"
export MUXCODE_ANALYZE_CLI=opencode MUXCODE_TMP_CLEANUP_THRESHOLD=0 MUXCODE_BRANCH_TIME_DISABLE=1 MUXCODE_DEDUP_WINDOW=0
export MUXCODE_AGENT_HEALTH_CHECK_SECS=86400 MUXCODE_AUTO_RESUME_DISABLE=1 MUXCODE_PROMPT_AGENT_DISABLE=1 MUXCODE_EDIT_AUTO_RESTART_DISABLE=1
mkdir -p "$HOME" "$WORK/lifecycle"; : >"$WORK/empty-config"

echo "=== MUX-180 windowless notify, route and inbox cap ==="
MUX="$WORK/muxcode"
(cd "$REPO/tools/muxcode" && go build -buildvcs=false -o "$MUX" .) >"$WORK/build.log" 2>&1 \
  && ok "built muxcode from the tree" || { fail "build: $(tail -3 "$WORK/build.log")"; exit 1; }

# session NAME — a fresh bus, a tmux session holding only edit, and a daemon.
session() {
  export BUS_SESSION="windowless-notify-$1-$$"; SESSIONS="$SESSIONS $BUS_SESSION"
  BD=$("$MUX" bus-dir "$BUS_SESSION"); LL="$MUXCODE_LIFECYCLE_LOG_DIR/$BUS_SESSION.log"
  "$MUX" init --memory-dir "$WORK/mem-$1" >/dev/null 2>&1
  tmux new-session -d -s "$BUS_SESSION" -n edit -c "$WORK"
  (cd "$WORK" && exec "$MUX" watch "$BUS_SESSION" --poll 1 --debounce 1 >"$WORK/daemon-$1.log" 2>&1) &
  DPID=$!; PIDS="$PIDS $DPID"
  sleep 1.5
}
rows() { jq -r --arg e "$1" 'select(.event == $e) | .detail' "$LL" 2>/dev/null | grep -c . || true; }
inbox_rows() { local n; n=$(grep -c . "$BD/inbox/$1.jsonl" 2>/dev/null); echo "${n:-0}"; }
cycles() { local n; n=$(grep -c 'Edits stabilized' "$WORK/daemon-$1.log" 2>/dev/null); echo "${n:-0}"; }
# edit_cycle NAME — one edit-stabilization cycle, waiting for the route.
edit_cycle() {
  local before i; before=$(cycles "$1")
  echo "$(date +%s) $WORK/file$RANDOM.go" >>"/tmp/muxcode-analyze-$BUS_SESSION.trigger"
  for i in $(seq 1 60); do [ "$(cycles "$1")" -gt "$before" ] && break; sleep 0.25; done
  sleep 1
}
wait_for() { local n=$(($1 * 4)) i; shift; for ((i = 0; i < n; i++)); do "$@" && return 0; sleep 0.25; done; return 1; }

# ── A ────────────────────────────────────────────────────────────────
echo "-- A: no consumer for analyze — not notified, not routed, reported once"
session a
kill -0 "$DPID" 2>/dev/null && ok "A: scratch daemon running" || fail "A: daemon exited: $(tail -3 "$WORK/daemon-a.log")"
AGENT_ROLE=edit MUXCODE_CONSUMER_GUARD=0 "$MUX" send analyze analyze "explain the change" --no-notify >/dev/null 2>&1 # a request: what the daemon notifies for
for i in 1 2; do AGENT_ROLE=edit MUXCODE_CONSUMER_GUARD=0 "$MUX" send analyze analyze "stranded $i" --type event --no-notify >/dev/null 2>&1; done
edit_cycle a; edit_cycle a
sleep 25
[ "$(rows injection-refused)" = 0 ] && [ ! -e "$BD/trigger-analyze.notify" ] \
  && ok "A: ~30 s with a stranded backlog — zero injection-refused, no trigger file" \
  || fail "A: injection-refused $(rows injection-refused), trigger file $([ -e "$BD/trigger-analyze.notify" ] && echo present || echo absent)"
[ "$(cycles a)" = 2 ] && [ "$(rows analyzing)" = 0 ] && ok "A: two edit cycles, no analyzing transition" \
  || fail "A: cycles $(cycles a), analyzing transitions $(rows analyzing)"
[ "$(rows notify-no-consumer)" = 1 ] && [ "$(rows route-no-consumer)" = 1 ] && ok "A: one notify-no-consumer and one route-no-consumer row" \
  || fail "A: notify-no-consumer $(rows notify-no-consumer), route-no-consumer $(rows route-no-consumer)"

# ── B ────────────────────────────────────────────────────────────────
echo "-- B: negative control — the window appears and the backlog drains"
backlog=$(jq -r 'select(.to == "analyze") | .id' "$BD/inbox/analyze.jsonl" 2>/dev/null)
tmux new-window -d -t "$BUS_SESSION" -n analyze -c "$WORK" \
  "export AGENT_ROLE=analyze BUS_SESSION=$BUS_SESSION HOME=$HOME XDG_STATE_HOME=$XDG_STATE_HOME MUXCODE_CONFIG=$MUXCODE_CONFIG TMUX_TMPDIR=$TMUX_TMPDIR; while :; do '$MUX' inbox >>'$WORK/analyze-read.out' 2>/dev/null; sleep 1; done"
drained() { [ "$(inbox_rows analyze)" = 0 ]; }
receipted=0
if wait_for 15 drained; then
  for id in $backlog; do [ "$(jq -r '.acked_by // ""' "$BD/delivery/$id.status" 2>/dev/null)" = analyze ] && receipted=$((receipted + 1)); done
fi
[ -n "$backlog" ] && [ "$receipted" = "$(wc -w <<<"$backlog" | tr -d ' ')" ] && ok "B: the backlog drained, all $receipted receipted by analyze" \
  || fail "B: inbox $(inbox_rows analyze), receipted $receipted of $(wc -w <<<"$backlog")"
edit_cycle a
read_event() { grep -q 'Claude edited files' "$WORK/analyze-read.out" 2>/dev/null; }
[ "$(rows analyzing)" -ge 1 ] && [ -e "$BD/trigger-analyze.notify" ] && wait_for 10 read_event \
  && ok "B: the next cycle routed — analyzing transition, trigger written, event consumed" \
  || fail "B: analyzing $(rows analyzing), trigger $([ -e "$BD/trigger-analyze.notify" ] && echo yes || echo no), event read $(read_event && echo yes || echo no)"

# ── C ────────────────────────────────────────────────────────────────
echo "-- C: negative control — a windowless analyze with its own listener"
session c
(AGENT_ROLE=analyze exec "$MUX" inbox --poll --loop >"$WORK/listener-c.out" 2>&1) &
LPID=$!; PIDS="$PIDS $LPID"
listening() { [ "$(cat "$BD/polling-analyze.marker" 2>/dev/null)" = "$LPID" ]; }
wait_for 10 listening
edit_cycle c
heard() { grep -q 'Claude edited files' "$WORK/listener-c.out" 2>/dev/null; }
[ "$(rows analyzing)" -ge 1 ] && [ "$(rows route-no-consumer)" = 0 ] && wait_for 10 heard \
  && ok "C: routed with no window — analyzing transition, no skip row, the listener consumed the event" \
  || fail "C: analyzing $(rows analyzing), route-no-consumer $(rows route-no-consumer), heard $(heard && echo yes || echo no)"

# ── D ────────────────────────────────────────────────────────────────
echo "-- D: the inbox cap"
export BUS_SESSION="windowless-notify-d-$$"
BD=$("$MUX" bus-dir "$BUS_SESSION"); LL="$MUXCODE_LIFECYCLE_LOG_DIR/$BUS_SESSION.log"
"$MUX" init --memory-dir "$WORK/mem-d" >/dev/null 2>&1
codes=""
for i in $(seq 1 50); do AGENT_ROLE=edit "$MUX" send build note "queued $i" --type event --no-notify >/dev/null 2>&1; codes="$codes$?"; done
alerts=$(jq -r 'select(.action == "inbox-full") | .id' "$BD/inbox/edit.jsonl" 2>/dev/null | grep -c . || true)
[ "$codes" = "$(printf '0%.0s' $(seq 1 50))" ] && [ "$(rows inbox-full)" = 1 ] && [ "${alerts:-0}" = 1 ] \
  && ok "D: 50 sends land with one inbox-full row and one alert to edit" || fail "D: codes $codes, inbox-full rows $(rows inbox-full), alerts $alerts"
out=$(AGENT_ROLE=edit "$MUX" send build note "number 51" --type event --no-notify 2>&1); rc=$?
[ $rc = 1 ] && grep -q 'NOT SENT' <<<"$out" && [ "$(inbox_rows build)" = 50 ] && [ "$(rows inbox-full)" = 1 ] \
  && ok "D: the 51st prints NOT SENT, exit 1; all 50 still queued, no second row" || fail "D: 51st exit $rc, inbox $(inbox_rows build): $out"

total=$((PASS + FAIL))
echo
echo "=== test-windowless-notify: $PASS passed, $FAIL failed ($total checks, floor $FLOOR) ==="
[ "$total" -lt "$FLOOR" ] && { echo "  FAIL: only $total checks ran (floor $FLOOR) — a section was skipped"; exit 1; }
[ "$FAIL" = 0 ]
