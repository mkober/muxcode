#!/usr/bin/env bash
# Integration test for the verify-spec stale review refire fix (MUX-007).
#
# Exercises the daemon's once-per-completion reviewed-transition gate end to
# end: a scratch bus session, a real `muxcode watch` daemon, and messages sent
# over the real bus CLI. Covers, in order:
#   - an auto-CC'd review→test response growing edit's inbox never fires the
#     gate (the addressee filter — the original false trigger)
#   - one review→edit response produces exactly ONE verify-spec at plan, with
#     the reviewed marker recording that message's ID
#   - unrelated inbox growth (a plan→edit reply — the original failure's exact
#     shape — then an actionable build→edit request the daemon observably
#     processes) re-fires nothing: count, marker, and lifecycle all unchanged
#   - after plan drains its inbox (as a live plan agent does), a second
#     review→edit response fires a second verify-spec and rotates the marker
#     to the new message ID
#
# Fire counts are read from the append-only bus log (log.jsonl), which no
# delivery machinery ever consumes; plan's inbox proves delivery; lifecycle
# plan-verify rows stand in 1:1 for TransitionWorkflow(StateReviewed), which
# shares the gate's else-branch in checkInboxes.
#
# ISOLATION: scratch BUS_SESSION under /tmp, scratch repo dir pinned via
# MUXCODE_SESSION_REPO_DIR, lifecycle log in a temp dir, empty config,
# force-respond ladder disabled so escalation cannot inject or consume
# mid-test.
#
# REQUIRES: the installed muxcode binary must include MUX-007 Phase 1 (run
# ./build.sh first), and tmux must be available.
#
# Usage: bash scripts/test-verify-spec-refire.sh
set -uo pipefail

MUX="${MUXCODE_BIN:-muxcode}"
MUX="$(command -v "$MUX" 2>/dev/null || echo "$MUX")"
if [ ! -x "$MUX" ]; then
  echo "  FAIL  cannot resolve muxcode binary ('$MUX') — set MUXCODE_BIN"
  exit 1
fi
if ! command -v tmux >/dev/null 2>&1; then
  echo "  FAIL  tmux not available"
  exit 1
fi

GREEN=$'\033[0;32m'; RED=$'\033[0;31m'; NC=$'\033[0m'
pass=0; fail=0
ok()  { echo "  ${GREEN}PASS${NC}  $*"; pass=$((pass + 1)); }
bad() { echo "  ${RED}FAIL${NC}  $*"; fail=$((fail + 1)); }

# --- Isolation -------------------------------------------------------------
export BUS_SESSION="verify-refire-test-$$"
BD="/tmp/muxcode-bus-${BUS_SESSION}"
WORK="/tmp/verify-refire-work-$$"
REPO="$WORK/repo"
mkdir -p "$REPO/docs/requirements/drafts"
export MUXCODE_LIFECYCLE_LOG_DIR="$WORK/lifecycle"
: > "$WORK/empty-config"
export MUXCODE_CONFIG="$WORK/empty-config"
export MUXCODE_TMP_CLEANUP_THRESHOLD=0
export MUXCODE_BRANCH_TIME_DISABLE=1
export MUXCODE_DEDUP_WINDOW=0
export MUXCODE_SESSION_REPO_DIR="$REPO"
export MUXCODE_FORCE_RESPOND_DISABLE=1

DPID=""
cleanup() {
  [ -n "$DPID" ] && kill "$DPID" 2>/dev/null
  tmux kill-session -t "$BUS_SESSION" 2>/dev/null
  rm -rf "$BD" "$WORK"
}
trap cleanup EXIT

LIFELOG="$MUXCODE_LIFECYCLE_LOG_DIR/${BUS_SESSION}.log"
MARKER="$BD/reviewed-transition.last"

tmux new-session -d -s "$BUS_SESSION" -n edit -x 120 -y 30
"$MUX" init >/dev/null 2>&1

# --- Helpers ---------------------------------------------------------------
# verify-spec fires, counted from the append-only bus log — never consumed,
# so the count is monotonic and immune to any delivery-side draining.
vs_count() {
  local c
  c="$(grep -c '"action":"verify-spec"' "$BD/log.jsonl" 2>/dev/null)"
  echo "${c:-0}"
}

# plan-verify lifecycle rows — logged in the same else-branch as
# TransitionWorkflow(StateReviewed), so the count doubles as transition count.
pv_count() {
  local c
  c="$(grep -c '"event":"plan-verify"' "$LIFELOG" 2>/dev/null)"
  echo "${c:-0}"
}

marker() { cat "$MARKER" 2>/dev/null | tr -d '[:space:]'; }

# Newest unconsumed review→edit message ID, straight from edit's inbox JSONL —
# the same row NewestMessageIDFrom matches.
newest_review_id() {
  grep '"from":"review"' "$BD/inbox/edit.jsonl" 2>/dev/null \
    | grep '"to":"edit"' | tail -1 \
    | grep -o '"id":"[^"]*"' | head -1 | cut -d'"' -f4
}

wait_vs_count() {
  local want="$1" i
  for i in $(seq 1 30); do
    [ "$(vs_count)" -ge "$want" ] && return 0
    sleep 0.5
  done
  return 1
}

wait_lifecycle() {
  local pattern="$1" extra="$2" i
  for i in $(seq 1 30); do
    if grep "$pattern" "$LIFELOG" 2>/dev/null | grep -q "$extra"; then
      return 0
    fi
    sleep 0.5
  done
  return 1
}

# --- Fixtures --------------------------------------------------------------
SPEC="docs/requirements/drafts/refire-fixture-spec.md"
cat > "$REPO/$SPEC" <<'EOF'
# Refire Fixture Spec

## Requirements

- [ ] an open item so the spec is plausibly in progress

## Status

In Progress
EOF

(cd "$REPO" && "$MUX" spec set "$SPEC" >/dev/null 2>&1) \
  && ok "active spec set to the fixture" \
  || bad "spec set failed for the fixture"

# --- Daemon ----------------------------------------------------------------
"$MUX" watch "$BUS_SESSION" --poll 1 >"$WORK/daemon.log" 2>&1 &
DPID=$!
sleep 1
kill -0 "$DPID" 2>/dev/null && ok "scratch daemon running (pid $DPID)" \
  || bad "scratch daemon exited immediately: $(tail -3 "$WORK/daemon.log" 2>/dev/null)"

# --- 1. Negative control: auto-CC'd review→test response never fires -------
# The CC copy lands in edit's inbox with its original To=test intact — the
# exact shape that satisfied the pre-fix From-only check. Runs first, against
# an empty marker, so nothing but the addressee filter can be suppressing it.
AGENT_ROLE=review "$MUX" send test tested "CC control: review response to test" --type response >/dev/null 2>&1
sleep 3

[ "$(vs_count)" -eq 0 ] \
  && ok "auto-CC'd review→test response fired no verify-spec" \
  || bad "verify-spec fired on a CC copy (count $(vs_count), want 0) — addressee filter not enforced"

[ ! -f "$MARKER" ] \
  && ok "no reviewed marker written for the CC copy" \
  || bad "reviewed marker written for a CC copy: $(marker)"

# --- 2. One review completion → exactly one verify-spec --------------------
AGENT_ROLE=review "$MUX" send edit review-result "Review complete: LGTM, no issues found" --type response >/dev/null 2>&1
FIRST_ID="$(newest_review_id)"
[ -n "$FIRST_ID" ] \
  && ok "review→edit response sits unconsumed in edit's inbox ($FIRST_ID)" \
  || bad "review→edit response missing from edit's inbox"

if wait_vs_count 1; then
  sleep 2
  [ "$(vs_count)" -eq 1 ] \
    && ok "exactly one verify-spec fired for one review completion" \
    || bad "verify-spec over-fired: count $(vs_count), want 1"
else
  bad "no verify-spec fired within timeout: $(tail -3 "$WORK/daemon.log" 2>/dev/null)"
fi

grep -q '"action":"verify-spec"' "$BD/inbox/plan.jsonl" 2>/dev/null \
  && ok "verify-spec delivered to plan's inbox" \
  || bad "plan's inbox has no verify-spec request"

grep '"action":"verify-spec"' "$BD/log.jsonl" 2>/dev/null | grep -q "$SPEC" \
  && ok "verify-spec names the active spec path" \
  || bad "verify-spec does not name the active spec"

[ "$(marker)" = "$FIRST_ID" ] \
  && ok "reviewed marker records the completion's message ID" \
  || bad "marker is '$(marker)', want '$FIRST_ID'"

[ "$(pv_count)" -eq 1 ] \
  && ok "one plan-verify lifecycle row — StateReviewed transitioned once" \
  || bad "plan-verify lifecycle rows: $(pv_count), want 1"

# --- 3. Unrelated growth does not re-fire ----------------------------------
# The review response is still unconsumed in edit's inbox — the pre-fix gate
# re-fired on every growth in exactly this state. First the original failure's
# shape: plan replying to edit while the review message sits there.
AGENT_ROLE=plan "$MUX" send edit verified "Verification summary: no changes needed" --type response >/dev/null 2>&1
sleep 3

[ "$(vs_count)" -eq 1 ] \
  && ok "plan's reply to edit did not re-fire verify-spec" \
  || bad "plan's reply re-fired: count $(vs_count), want 1 — the original storm"

# Then actionable growth with a deterministic daemon-observed signal: an
# inbox-notify row for edit proves the poll processed this growth before we
# assert nothing re-fired on it.
AGENT_ROLE=build "$MUX" send edit build-status "Unrelated request: build finished" >/dev/null 2>&1
wait_lifecycle '"event":"inbox-notify"' '"edit"' \
  && ok "daemon observably processed the unrelated actionable growth" \
  || bad "no inbox-notify row for edit — unrelated growth never observed"
sleep 2

[ "$(vs_count)" -eq 1 ] \
  && ok "observed unrelated growth did not re-fire verify-spec" \
  || bad "unrelated growth re-fired: count $(vs_count), want 1"

[ "$(marker)" = "$FIRST_ID" ] \
  && ok "reviewed marker unchanged by unrelated growth" \
  || bad "marker moved to '$(marker)' on unrelated growth"

[ "$(pv_count)" -eq 1 ] \
  && ok "still one plan-verify lifecycle row after unrelated growth" \
  || bad "plan-verify lifecycle rows: $(pv_count), want 1"

# --- 4. A genuine second completion still fires ----------------------------
# A live plan agent consumes its verify-spec request before the next review
# completes; model that by draining plan's inbox. Without the drain, the
# send-side inbox-stacking guard (sendMessage's HasPendingInboxRequest — a
# different guard than the MUX-007 gate) correctly suppresses an identical
# daemon→plan verify-spec while the first sits pending, and the gate looks
# inert when it actually fired (the marker rotates either way).
AGENT_ROLE=plan "$MUX" inbox >/dev/null 2>&1
grep -q '"action":"verify-spec"' "$BD/inbox/plan.jsonl" 2>/dev/null \
  && bad "plan's inbox still holds a pending verify-spec after the drain" \
  || ok "plan's inbox drained (models a live plan consuming its request)"

AGENT_ROLE=review "$MUX" send edit review-result "Review complete: second pass, one nit" --type response >/dev/null 2>&1
SECOND_ID="$(newest_review_id)"

if wait_vs_count 2; then
  sleep 2
  [ "$(vs_count)" -eq 2 ] \
    && ok "second review completion fired exactly one more verify-spec" \
    || bad "second completion over-fired: count $(vs_count), want 2"
else
  bad "second review completion never fired (count $(vs_count)) — gate went inert: $(tail -3 "$WORK/daemon.log" 2>/dev/null)"
fi

[ "$(marker)" = "$SECOND_ID" ] && [ "$SECOND_ID" != "$FIRST_ID" ] \
  && ok "reviewed marker rotated to the second completion's ID" \
  || bad "marker is '$(marker)', want '$SECOND_ID' (≠ '$FIRST_ID')"

[ "$(pv_count)" -eq 2 ] \
  && ok "two plan-verify lifecycle rows — one transition per completion" \
  || bad "plan-verify lifecycle rows: $(pv_count), want 2"

# --- Coverage floor --------------------------------------------------------
total=$((pass + fail))
if [ "$total" -ge 19 ]; then
  ok "coverage floor met ($total checks executed)"
else
  bad "coverage floor NOT met — only $total checks executed, want >= 19 (a skipped run must not report green)"
fi

# --- Summary ---------------------------------------------------------------
echo ""
echo "  ${pass} passed, ${fail} failed"
[ "$fail" -eq 0 ] || exit 1
exit 0
