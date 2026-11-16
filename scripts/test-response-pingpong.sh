#!/usr/bin/env bash
# Integration test for MUX-175: two agents acknowledging each other forever are
# stopped by the bus, without a human.
#
# On 2026-09-10 build and test traded `Acknowledged.` every 4–5 s; the daemon's
# DetectMessageLoop alerted and nothing stopped it until a human told both to
# stop. bus.Send now refuses a response repeating the last one delivered on its
# (from, to, action). Everything below drives the real CLI against scratch
# buses; the two "agents" are shell loops that answer every message they read
# with `Acknowledged.` — the worst case, agents that reply to anything.
#
#   A. The ack loop, seeded once: it stops by itself — at most 3 responses
#      delivered and none after the first seconds — the repeat's sender read
#      NOT SENT with exit 1, and a response-suppressed row names the pair.
#   B. Control: the same agents on a muxcode built from a copy of this tree with
#      the brake's result ignored keep ping-ponging (at least 8 deliveries in
#      the same time), so a green A proves the checks can see the defect.
#   C. Negative control — a substantive plan↔edit verify-spec exchange, new
#      text each round, and `10/23` then `11/23` back to back: every response
#      delivered, exit 0.
#   D. Negative control — a reply to an open tracked request is delivered
#      though identical to the last response on its pair; a later untracked
#      repeat is still refused.
#
# ISOLATION: muxcode built from the tree (MUXCODE_RESPONSE_PINGPONG_BIN
# overrides the fixed binary); scratch HOME, state, lifecycle log and config,
# one BUS_SESSION per section; no daemon and no tmux server (TMUX unset,
# TMUX_TMPDIR in the work dir), so MUX-145's consumer check reads as
# indeterminate. The CLI's own window dedup is off (MUXCODE_DEDUP_WINDOW=0) so
# only the brake decides.
#
# REQUIRES: go, jq. ~45 s.
#
# Usage: bash scripts/test-response-pingpong.sh
set -uo pipefail

FLOOR=8
PASS=0
FAIL=0
ok()   { PASS=$((PASS + 1)); echo "  ok: $1"; }
fail() { FAIL=$((FAIL + 1)); echo "  FAIL: $1"; }
for t in go jq; do command -v "$t" >/dev/null 2>&1 || { echo "SKIP: $t is required"; exit 2; }; done

REPO=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
WORK=$(mktemp -d /tmp/response-pingpong-XXXXXX)
AGENT_PIDS=""
cleanup() {
  touch "$WORK/stop" 2>/dev/null
  local p; for p in $AGENT_PIDS; do kill "$p" 2>/dev/null; done
  rm -rf "$WORK"
}
trap cleanup EXIT
GOCACHE="$(go env GOCACHE)"; GOMODCACHE="$(go env GOMODCACHE)"
export GOCACHE GOMODCACHE
export HOME="$WORK/home" XDG_STATE_HOME="$WORK/home/.local/state"
export MUXCODE_LIFECYCLE_LOG_DIR="$WORK/lifecycle" MUXCODE_CONFIG="$WORK/empty-config"
export BUS_MEMORY_DIR="$WORK/memory" BUS_GLOBAL_MEMORY_DIR="$WORK/global-memory" TMUX_TMPDIR="$WORK/tmux"
export MUXCODE_DEDUP_WINDOW=0
unset TMUX TMUX_PANE AGENT_ROLE BUS_ROLE BUS_SESSION MUXCODE_BUS_BASE MUXCODE_BUS_DIR MUXCODE_RESPONSE_SUPPRESS_WINDOW MUXCODE_CONSUMER_GUARD
mkdir -p "$HOME" "$WORK/lifecycle" "$WORK/tmux"; : >"$WORK/empty-config"

echo "=== MUX-175 response ping-pong brake ==="
if [ -n "${MUXCODE_RESPONSE_PINGPONG_BIN:-}" ]; then
  MUX="$MUXCODE_RESPONSE_PINGPONG_BIN"
else
  MUX="$WORK/muxcode"
  (cd "$REPO/tools/muxcode" && go build -buildvcs=false -o "$MUX" .) >"$WORK/build.log" 2>&1 || { fail "build: $(tail -3 "$WORK/build.log")"; exit 1; }
fi
cp -R "$REPO/tools/muxcode" "$WORK/ctl-tree"
perl -pi -e 's/if err := brakeRepeatedResponse\(session, m\); err != nil \{/if err := brakeRepeatedResponse(session, m); false \&\& err != nil {/' "$WORK/ctl-tree/bus/response_brake.go"
CTL="$WORK/muxcode-nobrake"
grep -q 'false && err != nil' "$WORK/ctl-tree/bus/response_brake.go" \
  && (cd "$WORK/ctl-tree" && go build -buildvcs=false -o "$CTL" .) >"$WORK/ctl-build.log" 2>&1 \
  && ok "built muxcode and the control (brake result ignored)" || fail "control build: $(tail -3 "$WORK/ctl-build.log")"

new_session() { export BUS_SESSION="pingpong-$1-$$"; BD=$("$2" bus-dir "$BUS_SESSION"); "$2" init --memory-dir "$WORK/mem-$1" >/dev/null 2>&1; }
# acks_between — responses delivered between build and test, from the bus log.
acks_between() { jq -c 'select(.type == "response" and ((.from == "build" and .to == "test") or (.from == "test" and .to == "build")))' "$BD/log.jsonl" 2>/dev/null | grep -c . || true; }
# agent BIN ROLE — reads ROLE's inbox and answers every message with
# `Acknowledged.`, logging "<exit>\t<the NOT SENT line, else the first line>"
# per send (stderr is captured too, and the bus's own notice comes first).
agent() {
  local bin=$1 role=$2 line from action out rc
  while [ ! -e "$WORK/stop" ]; do
    AGENT_ROLE=$role "$bin" inbox --raw 2>/dev/null | while IFS= read -r line; do
      from=$(jq -r .from <<<"$line"); action=$(jq -r .action <<<"$line")
      out=$(AGENT_ROLE=$role "$bin" send "$from" "$action" "Acknowledged." --type response --no-notify 2>&1); rc=$?
      printf '%s\t%s\n' "$rc" "$(grep -m1 'NOT SENT' <<<"$out" || head -1 <<<"$out")" >>"$WORK/$BUS_SESSION-$role.sends"
    done
    sleep 0.3
  done
}
# ack_loop BIN NAME — seeds one ack from build to test, runs both agents 8 s;
# sets AT5 and AT8, the build↔test responses delivered after 5 s and 8 s.
ack_loop() {
  new_session "$2" "$1"; rm -f "$WORK/stop"
  AGENT_ROLE=build "$1" send test test "Acknowledged." --type response --no-notify >/dev/null 2>&1
  agent "$1" build & AGENT_PIDS="$AGENT_PIDS $!"
  agent "$1" test & AGENT_PIDS="$AGENT_PIDS $!"
  sleep 5; AT5=$(acks_between)
  sleep 3; AT8=$(acks_between)
  touch "$WORK/stop"; wait 2>/dev/null; AGENT_PIDS=""
}

# ── A ────────────────────────────────────────────────────────────────
echo "-- A: an ack loop stops by itself"
ack_loop "$MUX" a
[ "$AT8" -le 3 ] && [ "$AT5" = "$AT8" ] && ok "A: $AT8 acks delivered, none after 5 s — the loop stopped without intervention" \
  || fail "A: $AT5 acks at 5 s, $AT8 at 8 s"
refused=$(cat "$WORK/$BUS_SESSION"-*.sends 2>/dev/null | grep -c $'^1\tNOT SENT' || true)
[ "${refused:-0}" -ge 1 ] && ok "A: the repeat's sender read NOT SENT, exit 1 ($refused refusals): $(grep -h $'^1\tNOT SENT' "$WORK/$BUS_SESSION"-*.sends | head -1 | cut -f2 | cut -c1-110)" \
  || fail "A: no sender read NOT SENT with exit 1: $(cat "$WORK/$BUS_SESSION"-*.sends 2>/dev/null | head -3)"
jq -r 'select(.event == "response-suppressed") | .detail' "$MUXCODE_LIFECYCLE_LOG_DIR/$BUS_SESSION.log" 2>/dev/null | grep -qE '^(build→test|test→build):test ' \
  && ok "A: a response-suppressed row names the pair and the action" || fail "A: no response-suppressed row naming build↔test:test"

# ── B ────────────────────────────────────────────────────────────────
echo "-- B: control — the same agents with the brake ignored"
if [ -x "$CTL" ]; then
  ack_loop "$CTL" b
  [ "$AT8" -ge 8 ] && ok "B: the loop runs on without the brake ($AT8 acks in 8 s) — A's checks can see the defect" \
    || fail "B: only $AT8 acks without the brake — the loop never formed, so A proves nothing"
else
  fail "B: control binary missing"
fi

# ── C ────────────────────────────────────────────────────────────────
echo "-- C: negative control — a substantive exchange is never braked"
new_session c "$MUX"
exchange=(
  "plan|edit|Phase 2: 10/23 ticked — build 12:01 exit 0, test 12:03 exit 0"
  "edit|plan|Phase 2 review ran at 12:02, before the test — re-check the order"
  "plan|edit|Corrected: review 12:05 after test 12:03"
  "edit|plan|Accepted — the corrected order holds"
  "plan|edit|Phase 4: 10/23 ticked"
  "plan|edit|Phase 4: 11/23 ticked"
)
codes=""
for row in "${exchange[@]}"; do
  IFS='|' read -r from to text <<<"$row"
  AGENT_ROLE=$from "$MUX" send "$to" verify-spec "$text" --type response --no-notify >/dev/null 2>&1; codes="$codes$?"
done
[ "$codes" = "000000" ] && ok "C: all ${#exchange[@]} substantive responses delivered, including 10/23 then 11/23" \
  || fail "C: send exit codes $codes, want all 0"

# ── D ────────────────────────────────────────────────────────────────
echo "-- D: negative control — a reply to an open tracked request"
new_session d "$MUX"
AGENT_ROLE=build "$MUX" send edit build "Acknowledged." --type response --no-notify >/dev/null 2>&1
AGENT_ROLE=edit "$MUX" send build build "run the build" --no-notify >/dev/null 2>&1
rid=$(jq -r 'select(.type == "request" and .from == "edit" and .to == "build") | .id' "$BD/log.jsonl" 2>/dev/null | tail -1)
out=$(AGENT_ROLE=build "$MUX" send edit build "Acknowledged." --type response --reply-to "$rid" --no-notify 2>&1); rc=$?
[ -n "$rid" ] && [ $rc = 0 ] && ok "D: the identical reply to open request $rid was delivered" || fail "D: reply to ${rid:-<none>} exit $rc: $out"
out=$(AGENT_ROLE=build "$MUX" send edit build "Acknowledged." --type response --no-notify 2>&1); rc=$?
[ $rc = 1 ] && grep -q 'NOT SENT' <<<"$out" && ok "D: an untracked repeat after it is still NOT SENT, exit 1" || fail "D: untracked repeat exit $rc: $out"

total=$((PASS + FAIL))
echo
echo "=== test-response-pingpong: $PASS passed, $FAIL failed ($total checks, floor $FLOOR) ==="
[ "$total" -lt "$FLOOR" ] && { echo "  FAIL: only $total checks ran (floor $FLOOR) — a section was skipped"; exit 1; }
[ "$FAIL" = 0 ]
