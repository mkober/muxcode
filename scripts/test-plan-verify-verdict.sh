#!/usr/bin/env bash
# Integration test for the spec-verification verdict gate (MUX-172).
#
# The daemon used to ask NotifyPlanOn about a literal "success", so every
# review reply sent plan a verify-spec — must-fix reviews included. It now
# reads the verdict from the reply's own last EXIT=<n> sentinel
# (bus.ReviewReplyOutcome) and withholds when the verdict fails, is missing,
# or contradicts the reply's must-fix count. Drives a real `muxcode watch`
# daemon over the real bus CLI, in order:
#   A. a reply ending EXIT=1 (no history row exists) → withheld, a
#      `plan-verify-withheld failure <id>` row; the reviewed marker and the
#      `reviewed` workflow state still move
#   B. a reply with no sentinel → withheld at warn, reason "unknown verdict"
#   C. a passing review history row written just before a sentinel-less reply
#      → still withheld: the row is not read
#   D. EXIT=0 beside a findings line counting a must-fix → withheld,
#      "conflicting verdict"
#   E. negative control: a passing reply fires exactly one verify-spec naming
#      the spec. The scratch repo is committed, so the movement gate has a
#      fingerprint: had any withheld fire written the movement marker, this
#      review would be suppressed as "nothing moved" — so E firing also proves
#      A–D left the marker unwritten
#
# Fire counts come from the append-only bus log (log.jsonl); plan's inbox
# proves delivery. Each case waits for its own withheld row, which the daemon
# writes in the same pass that would have sent, so "no verify-spec" is never
# asserted before the gate ran.
#
# ISOLATION: scratch BUS_SESSION, scratch git repo pinned via
# MUXCODE_SESSION_REPO_DIR, lifecycle log in a temp dir, empty config,
# force-respond ladder disabled.
#
# REQUIRES: an installed muxcode carrying MUX-172 Phase 1 (run ./build.sh
# first) and tmux.
#
# Usage: bash scripts/test-plan-verify-verdict.sh
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
. "$(dirname "${BASH_SOURCE[0]}")/lib/muxcode-version.sh"
. "$(dirname "${BASH_SOURCE[0]}")/lib/muxcode-bus-dir.sh"
require_muxcode_version "$MUX" v0.1.0 MUX-172 || { echo "  FAIL  binary precondition not met"; exit 1; }

GREEN=$'\033[0;32m'; RED=$'\033[0;31m'; NC=$'\033[0m'
pass=0; fail=0
ok()  { echo "  ${GREEN}PASS${NC}  $*"; pass=$((pass + 1)); }
bad() { echo "  ${RED}FAIL${NC}  $*"; fail=$((fail + 1)); }

# --- Isolation -------------------------------------------------------------
export BUS_SESSION="verify-verdict-test-$$"
BD="$(muxcode_bus_dir "$MUX" "$BUS_SESSION")" || exit 1
WORK="/tmp/verify-verdict-work-$$"
REPO="$WORK/repo"
mkdir -p "$REPO/docs/requirements/drafts"
export MUXCODE_LIFECYCLE_LOG_DIR="$WORK/lifecycle"
: > "$WORK/empty-config"
export MUXCODE_CONFIG="$WORK/empty-config"
# MUXCODE_CONFIG does not reach muxcode.json (LoadConfig: the cwd's
# .muxcode/muxcode.json, then $MUXCODE_CONFIG_DIR's): an inherited
# notify_plan_on would decide case A. Pin both — the daemon starts in $WORK.
mkdir -p "$WORK/config-dir"
export MUXCODE_CONFIG_DIR="$WORK/config-dir"
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
MOVEMENT="$BD/verify-movement.last"

tmux new-session -d -s "$BUS_SESSION" -n edit -x 120 -y 30
"$MUX" init >/dev/null 2>&1

# --- Helpers ---------------------------------------------------------------
vs_count() {
  local c
  c="$(grep -c '"action":"verify-spec"' "$BD/log.jsonl" 2>/dev/null)"
  echo "${c:-0}"
}

pv_count() {
  local c
  c="$(grep -c '"event":"plan-verify"' "$LIFELOG" 2>/dev/null)"
  echo "${c:-0}"
}

marker() { cat "$MARKER" 2>/dev/null | tr -d '[:space:]'; }

newest_review_id() {
  grep '"from":"review"' "$BD/inbox/edit.jsonl" 2>/dev/null \
    | grep '"to":"edit"' | tail -1 \
    | grep -o '"id":"[^"]*"' | head -1 | cut -d'"' -f4
}

# withheld_row <review-id> — the plan-verify-withheld row naming that review.
withheld_row() {
  grep '"event":"plan-verify-withheld"' "$LIFELOG" 2>/dev/null | grep -F "$1" | tail -1
}

wait_withheld() {
  local id="$1" i
  for i in $(seq 1 30); do
    [ -n "$(withheld_row "$id")" ] && return 0
    sleep 0.5
  done
  return 1
}

wait_vs_count() {
  local want="$1" i
  for i in $(seq 1 30); do
    [ "$(vs_count)" -ge "$want" ] && return 0
    sleep 0.5
  done
  return 1
}

review_reply() {
  AGENT_ROLE=review "$MUX" send edit review-result "$1" --type response >/dev/null 2>&1
  newest_review_id
}

# expect_withheld <case> <review-id> <outcome-word> <level> <evidence-substring>
expect_withheld() {
  local name="$1" id="$2" outcome="$3" level="$4" evidence="$5" row
  if [ -z "$id" ]; then
    bad "$name: review→edit reply missing from edit's inbox"
    return
  fi
  if ! wait_withheld "$id"; then
    bad "$name: no plan-verify-withheld row for $id: $(tail -3 "$WORK/daemon.log" 2>/dev/null)"
    return
  fi
  row="$(withheld_row "$id")"
  echo "$row" | grep -qF "\"detail\":\"$outcome $id (" \
    && ok "$name: withheld row names outcome '$outcome' and the review id" \
    || bad "$name: withheld row detail wrong: $row"
  echo "$row" | grep -qF "$evidence" \
    && ok "$name: withheld row carries its evidence ('$evidence')" \
    || bad "$name: withheld row lacks '$evidence': $row"
  echo "$row" | grep -q "\"level\":\"$level\"" \
    && ok "$name: withheld row logged at $level" \
    || bad "$name: withheld row level wrong (want $level): $row"
  [ "$(marker)" = "$id" ] \
    && ok "$name: reviewed marker still records the reply — transition unaffected" \
    || bad "$name: reviewed marker is '$(marker)', want '$id'"
  [ "$(vs_count)" -eq 0 ] \
    && ok "$name: no verify-spec sent" \
    || bad "$name: verify-spec fired on a withheld verdict (count $(vs_count))"
}

# --- Fixtures --------------------------------------------------------------
SPEC="docs/requirements/drafts/verdict-fixture-spec.md"
cat > "$REPO/$SPEC" <<'EOF'
# Verdict Fixture Spec

## Requirements

- [ ] an open item so the spec is plausibly in progress

## Status

In Progress
EOF
printf 'package fixture\n' > "$REPO/keep.go"
git init -q "$REPO" 2>/dev/null
git -C "$REPO" add -A 2>/dev/null
git -C "$REPO" -c user.email=t@t -c user.name=t commit -q -m base 2>/dev/null
git -C "$REPO" rev-parse -q HEAD >/dev/null 2>&1 \
  && ok "fixture repo committed — the movement gate has a fingerprint" \
  || bad "fixture repo failed to commit"

(cd "$REPO" && "$MUX" spec set "$SPEC" >/dev/null 2>&1) \
  && ok "active spec set to the fixture" \
  || bad "spec set failed for the fixture"

# --- Daemon ----------------------------------------------------------------
(cd "$WORK" && exec "$MUX" watch "$BUS_SESSION" --poll 1) >"$WORK/daemon.log" 2>&1 &
DPID=$!
sleep 1
kill -0 "$DPID" 2>/dev/null && ok "scratch daemon running (pid $DPID)" \
  || bad "scratch daemon exited immediately: $(tail -3 "$WORK/daemon.log" 2>/dev/null)"

# --- A. EXIT=1, no history row → withheld -----------------------------------
[ ! -s "$BD/review-history.jsonl" ] \
  && ok "A: no review history row exists — the sentinel is the only evidence" \
  || bad "A: a review history row already exists"
ID_A="$(review_reply "Review: 2 must-fix, 1 should-fix, 0 nits. EXIT=1")"
expect_withheld "A (EXIT=1)" "$ID_A" failure info "EXIT sentinel"
grep -q '"state":"reviewed"' "$BD/workflow-state.json" 2>/dev/null \
  && ok "A: workflow moved to reviewed on a failing review" \
  || bad "A: workflow state not reviewed: $(cat "$BD/workflow-state.json" 2>/dev/null)"
grep -q '"action":"verify-spec"' "$BD/inbox/plan.jsonl" 2>/dev/null \
  && bad "A: plan's inbox holds a verify-spec" \
  || ok "A: plan's inbox holds no verify-spec"

# --- B. No sentinel → unknown, withheld -------------------------------------
ID_B="$(review_reply "Review complete: looks fine to me")"
expect_withheld "B (no sentinel)" "$ID_B" unknown warn "unknown verdict: the reply carries no EXIT sentinel"

# --- C. Passing history row beside a sentinel-less reply → withheld ---------
AGENT_ROLE=review "$MUX" log review "Review: 0 must-fix, 0 should-fix, 0 nits" --exit-code 0 >/dev/null 2>&1
tail -1 "$BD/review-history.jsonl" 2>/dev/null | grep -q '"outcome":"success"' \
  && ok "C: a passing review history row is the newest row" \
  || bad "C: passing history row not written: $(tail -1 "$BD/review-history.jsonl" 2>/dev/null)"
ID_C="$(review_reply "Review complete: second look, all good")"
expect_withheld "C (history row, no sentinel)" "$ID_C" unknown warn "unknown verdict"

# --- D. EXIT=0 beside a must-fix count → conflicting, withheld --------------
ID_D="$(review_reply "Review: 1 must-fix, 0 should-fix, 0 nits. EXIT=0")"
expect_withheld "D (conflicting)" "$ID_D" unknown warn "conflicting verdict"

[ ! -f "$MOVEMENT" ] \
  && ok "no withheld fire wrote the movement marker" \
  || bad "movement marker written by a withheld fire: $(cat "$MOVEMENT" 2>/dev/null)"
[ "$(pv_count)" -eq 0 ] \
  && ok "no plan-verify row across four withheld reviews" \
  || bad "plan-verify rows: $(pv_count), want 0"

# --- E. Negative control: a passing review fires ----------------------------
ID_E="$(review_reply "Review: 0 must-fix, 0 should-fix, 0 nits. EXIT=0")"
if wait_vs_count 1; then
  sleep 2
  [ "$(vs_count)" -eq 1 ] \
    && ok "E (EXIT=0): exactly one verify-spec fired after four withheld reviews" \
    || bad "E: verify-spec over-fired: count $(vs_count), want 1"
else
  bad "E: passing review never fired (suppressed: $(grep -c plan-verify-suppressed "$LIFELOG" 2>/dev/null)): $(tail -3 "$WORK/daemon.log" 2>/dev/null)"
fi
grep -q '"action":"verify-spec"' "$BD/inbox/plan.jsonl" 2>/dev/null \
  && ok "E: verify-spec delivered to plan's inbox" \
  || bad "E: plan's inbox has no verify-spec request"
grep '"action":"verify-spec"' "$BD/log.jsonl" 2>/dev/null | grep -q "$SPEC" \
  && ok "E: verify-spec names the active spec" \
  || bad "E: verify-spec does not name the active spec"
[ -z "$(withheld_row "$ID_E")" ] \
  && ok "E: no plan-verify-withheld row for the passing review" \
  || bad "E: passing review withheld: $(withheld_row "$ID_E")"
[ "$(pv_count)" -eq 1 ] && [ -f "$MOVEMENT" ] \
  && ok "E: one plan-verify row and the movement marker written by the fire" \
  || bad "E: plan-verify rows $(pv_count) (want 1), movement marker present: $([ -f "$MOVEMENT" ] && echo yes || echo no)"
[ "$(marker)" = "$ID_E" ] \
  && ok "E: reviewed marker records the passing reply" \
  || bad "E: reviewed marker is '$(marker)', want '$ID_E'"

# --- Coverage floor --------------------------------------------------------
total=$((pass + fail))
if [ "$total" -ge 35 ]; then
  ok "coverage floor met ($total checks executed)"
else
  bad "coverage floor NOT met — only $total checks executed, want >= 35 (a skipped run must not report green)"
fi

# --- Summary ---------------------------------------------------------------
echo ""
echo "  ${pass} passed, ${fail} failed"
[ "$fail" -eq 0 ] || exit 1
exit 0
