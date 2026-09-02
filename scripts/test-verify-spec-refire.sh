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
# Phase 3 sections (changed-files provenance and relevance):
#   - out-of-repo paths (a credentials-shaped config, a spawn-worktree copy
#     of a repo file) are never presented to the verifier, while the repo's
#     own copy of the same relative path is — scoping keys on location
#   - the movement gate: no fire when nothing moved; the verifier's own spec
#     edit is not movement; a run-state transition with a docs-only file
#     change still fires (fire-11); a source change touching the spec still
#     fires; an unrelated in-repo write alone never fires
#   - the active-spec pointer follows a drafts/ → completed/ move, and a
#     dangling pointer is detected and reported to edit exactly once
#   - re-init purges both MUX-007 gate markers
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
# REQUIRES: installed muxcode >= v0.1.0, which shipped MUX-007 Phase 1 (run
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
. "$(dirname "${BASH_SOURCE[0]}")/lib/muxcode-version.sh"
require_muxcode_version "$MUX" v0.1.0 MUX-007 || { echo "  FAIL  binary precondition not met"; exit 1; }

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

# ===========================================================================
# Phase 3: changed-files provenance, the movement gate, and the active-spec
# pointer (MUX-007 Phase 3). Sections 1-4 ran against a non-git repo — no
# movement evidence, so the gate fails open and their behavior is unchanged.
# From here the repo is a git repo and a graph-run fixture exists, so every
# fire carries a fingerprint and suppression has evidence to key on.
# ===========================================================================

# --- 5. Provenance: only repo files are ever presented ---------------------
# The graph run is created in a TERMINAL state so the executor never steps
# it, and BEFORE the next fire so its state is part of the recorded
# fingerprint (section 7 then moves only that state).
git init -q "$REPO" 2>/dev/null
mkdir -p "$REPO/bus" "$REPO/config"
printf 'package bus\n' > "$REPO/bus/pane_test.go"
printf 'package bus\n' > "$REPO/bus/keep.go"
printf '# tmux fixture\n' > "$REPO/config/tmux.conf"
git -C "$REPO" add -A 2>/dev/null
git -C "$REPO" -c user.email=t@t -c user.name=t commit -q -m base 2>/dev/null
git -C "$REPO" rev-parse -q HEAD >/dev/null 2>&1 \
  && ok "fixture repo committed — movement evidence exists from here" \
  || bad "fixture repo failed to commit"

mkdir -p "$BD/graphs/fixture-run"
cat > "$BD/graphs/fixture-run/run.json" <<'EOF'
{"id":"fixture-run","template":"t","state":"complete","created_at":1,"updated_at":1}
EOF

# The raw changed-files signal carries writes from anywhere on disk: a
# credentials-shaped config, a spawn-worktree copy of a file the repo also
# contains (census B1), and the repo's own copy of that same relative path.
mkdir -p "$WORK/outside" "$WORK/spawnwt/bus"
printf 'JIRA_API_TOKEN=fake\n' > "$WORK/outside/muxcode-config"
printf 'package bus\n' > "$WORK/spawnwt/bus/pane_test.go"
cat > "$BD/workflow-state.json" <<EOF
{"state":"editing","prev_state":"idle","since":1,"updated":1,"trigger":"seed","files_changed":3,"last_files":["$WORK/outside/muxcode-config","$WORK/spawnwt/bus/pane_test.go","$REPO/bus/pane_test.go"]}
EOF

AGENT_ROLE=plan "$MUX" inbox >/dev/null 2>&1
grep -q '"action":"verify-spec"' "$BD/inbox/plan.jsonl" 2>/dev/null \
  && bad "plan's inbox still holds a verify-spec before the provenance fire" \
  || ok "plan's inbox drained before the provenance fire"

AGENT_ROLE=review "$MUX" send edit review-result "Review complete: third pass" --type response >/dev/null 2>&1
wait_vs_count 3 \
  && ok "completion with movement evidence fired (first fingerprinted fire)" \
  || bad "no fire with movement evidence: $(tail -3 "$WORK/daemon.log" 2>/dev/null)"
sleep 1

last_vs="$(grep '"action":"verify-spec"' "$BD/log.jsonl" 2>/dev/null | tail -1)"
echo "$last_vs" | grep -q "$WORK/outside" \
  && bad "out-of-repo credentials-shaped path presented to the verifier" \
  || ok "out-of-repo path never presented to the verifier"
echo "$last_vs" | grep -q "spawnwt" \
  && bad "spawn-worktree path presented to the verifier" \
  || ok "spawn-worktree path rejected"
echo "$last_vs" | grep -q "bus/pane_test.go" \
  && ok "same relative path from inside the repo presented — scoping keys on location, not name" \
  || bad "repo's own copy of the file missing from the message"

# --- 6. Movement gate: nothing moved → no fire ------------------------------
AGENT_ROLE=plan "$MUX" inbox >/dev/null 2>&1
grep -q '"action":"verify-spec"' "$BD/inbox/plan.jsonl" 2>/dev/null \
  && bad "plan's inbox not drained before the suppression checks" \
  || ok "plan's inbox drained before the suppression checks"

AGENT_ROLE=review "$MUX" send edit review-result "Review complete: fourth pass, nothing changed since" --type response >/dev/null 2>&1
sleep 3
[ "$(vs_count)" -eq 3 ] \
  && ok "no verify-spec when nothing moved" \
  || bad "fired with a byte-identical tree and unchanged run state (count $(vs_count), want 3)"
grep -q '"event":"plan-verify-suppressed"' "$LIFELOG" 2>/dev/null \
  && ok "suppression visible as a plan-verify-suppressed lifecycle row" \
  || bad "no plan-verify-suppressed lifecycle row"

# Item 10's closed loop: the verifier's own spec edit must not read as
# movement — re-verifying it would manufacture the next echo.
echo "- [x] checked by plan" >> "$REPO/$SPEC"
AGENT_ROLE=review "$MUX" send edit review-result "Review complete: fifth pass after spec edit" --type response >/dev/null 2>&1
sleep 3
[ "$(vs_count)" -eq 3 ] \
  && ok "verifier's own spec edit is not movement" \
  || bad "spec-only edit re-fired verify-spec (count $(vs_count), want 3)"

# --- 7. Fire-11 shape: spec-only file change + run-state movement fires -----
cat > "$BD/graphs/fixture-run/run.json" <<'EOF'
{"id":"fixture-run","template":"t","state":"failed","created_at":1,"updated_at":2}
EOF
echo "- [ ] reopened" >> "$REPO/$SPEC"
AGENT_ROLE=review "$MUX" send edit review-result "Review complete: sixth pass after run failure" --type response >/dev/null 2>&1
wait_vs_count 4 \
  && ok "run-state transition fired despite a docs-only file change (fire-11 shape)" \
  || bad "fire-11 shape suppressed — a filename or tree-only rule cannot pass this"
sleep 1

# --- 8. Negative control: source change + spec touch fires ------------------
AGENT_ROLE=plan "$MUX" inbox >/dev/null 2>&1
echo "// change" >> "$REPO/bus/keep.go"
echo "- [x] closed" >> "$REPO/$SPEC"
AGENT_ROLE=review "$MUX" send edit review-result "Review complete: seventh pass with source change" --type response >/dev/null 2>&1
wait_vs_count 5 \
  && ok "genuine completion changing source and touching the spec fired" \
  || bad "suppressed on 'spec was touched' rather than 'nothing moved'"
sleep 1

# --- 9. An unrelated in-repo write alone never fires ------------------------
# The 10:23 echo shape: a write to an in-repo file from an unrelated task,
# plus edit-inbox growth, with the last review message still unconsumed.
#
# The gate re-check and the edit inbox-notify row share one growth branch in
# checkInboxes, so a NEW edit row proves the gate re-evaluated this growth —
# without it the no-fire assertion passes vacuously. Two delivery hazards
# make this send non-trivial: bare requests default to --track, so section
# 3's build-status left a 600s in-flight task and every later send of that
# tuple is "already tracking" — suppressed, no growth (found live in this
# test); and a message landing between checkInboxes and a later same-tick
# inbox refresh is absorbed rowlessly. --force bypasses the first; the
# retry loop covers the second.
echo "# tweak" >> "$REPO/config/tmux.conf"
edit_notify_count() { grep '"event":"inbox-notify"' "$LIFELOG" 2>/dev/null | grep -c '"edit"'; }
notify_before="$(edit_notify_count)"
observed=0
: > "$WORK/send9.log"
for attempt in 1 2 3 4 5; do
  AGENT_ROLE=build "$MUX" send edit build-status "Unrelated request ${attempt}: tmux config tweaked" --force >>"$WORK/send9.log" 2>&1
  echo "attempt ${attempt} exit=$?" >>"$WORK/send9.log"
  for i in $(seq 1 6); do
    if [ "$(edit_notify_count)" -gt "${notify_before:-0}" ]; then observed=1; break; fi
    sleep 0.5
  done
  [ "$observed" -eq 1 ] && break
done
[ "$observed" -eq 1 ] \
  && ok "daemon observably re-evaluated the gate on growth after the unrelated write" \
  || bad "unrelated growth never observed (no new edit inbox-notify row): $(tr '\n' ' ' < "$WORK/send9.log" | tail -c 500)"
sleep 2
[ "$(vs_count)" -eq 5 ] \
  && ok "unrelated in-repo write did not fire against the active spec" \
  || bad "unrelated in-repo write fired (count $(vs_count), want 5) — the 10:23 echo shape"

# --- 10. Active-spec pointer: follows the close-out move; dangles loudly ----
mkdir -p "$REPO/docs/requirements/completed"
mv "$REPO/$SPEC" "$REPO/docs/requirements/completed/refire-fixture-spec.md"
for i in $(seq 1 30); do
  grep -q "completed/refire-fixture-spec.md" "$BD/active-spec" 2>/dev/null && break
  sleep 0.5
done
grep -q "completed/refire-fixture-spec.md" "$BD/active-spec" 2>/dev/null \
  && ok "pointer followed the drafts→completed move" \
  || bad "pointer still names the old path: $(cat "$BD/active-spec" 2>/dev/null)"
grep -q '"event":"spec-repoint"' "$LIFELOG" 2>/dev/null \
  && ok "repoint visible as a spec-repoint lifecycle row" \
  || bad "no spec-repoint lifecycle row"

# A pointer with no counterpart anywhere is detected and reported, once.
printf 'docs/requirements/drafts/never-existed.md\n' > "$BD/active-spec"
for i in $(seq 1 30); do
  grep -q '"event":"spec-dangling"' "$LIFELOG" 2>/dev/null && break
  sleep 0.5
done
grep -q '"event":"spec-dangling"' "$LIFELOG" 2>/dev/null \
  && ok "dangling pointer detected (spec-dangling lifecycle row)" \
  || bad "dangling pointer never detected"
grep -q '"action":"spec-dangling"' "$BD/log.jsonl" 2>/dev/null \
  && ok "dangling pointer reported to edit" \
  || bad "no spec-dangling alert reached the bus"
sleep 3
[ "$(grep -c '"action":"spec-dangling"' "$BD/log.jsonl" 2>/dev/null)" -eq 1 ] \
  && ok "dangling alert fired once, not per poll" \
  || bad "dangling alert spammed: $(grep -c '"action":"spec-dangling"' "$BD/log.jsonl" 2>/dev/null) rows"

# --- 11. Re-init purges the MUX-007 gate markers ----------------------------
# Daemon stopped first: re-init truncates the very files it polls.
kill "$DPID" 2>/dev/null
wait "$DPID" 2>/dev/null
DPID=""
[ -f "$MARKER" ] \
  && ok "reviewed marker present before re-init" \
  || bad "reviewed marker missing before re-init"
[ -f "$BD/verify-movement.last" ] \
  && ok "verify-movement marker present before re-init" \
  || bad "verify-movement marker missing before re-init"
"$MUX" init >/dev/null 2>&1
[ ! -f "$MARKER" ] \
  && ok "re-init purged the reviewed marker" \
  || bad "reviewed marker survived re-init"
[ ! -f "$BD/verify-movement.last" ] \
  && ok "re-init purged the verify-movement marker" \
  || bad "verify-movement marker survived re-init"

# --- Coverage floor --------------------------------------------------------
total=$((pass + fail))
if [ "$total" -ge 42 ]; then
  ok "coverage floor met ($total checks executed)"
else
  bad "coverage floor NOT met — only $total checks executed, want >= 42 (a skipped run must not report green)"
fi

# --- Summary ---------------------------------------------------------------
echo ""
echo "  ${pass} passed, ${fail} failed"
[ "$fail" -eq 0 ] || exit 1
exit 0
