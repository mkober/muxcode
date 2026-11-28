#!/usr/bin/env bash
# Integration test for MUX-130 — spec phase parsing semantics, end to end.
#
# A real daemon walks a fixture spec through per-phase human-gated commits. The
# fixture holds what MUX-130 fixed: a `## Verification notes` section naming a
# phase with a checkbox (Defect B — it used to parse as a phase and could make
# a commit ready), a duplicate phase number (Defect C — warned at dispatch,
# never refused), and one item left open on purpose (decision 4 — it holds the
# loop, and the stuck gate says so).
#
# The fixture graph is the builtin 50-spec-to-pr shape with send nodes for its
# spawns (as test-multi-phase-graph.sh does) and WITHOUT its phase-check
# condition: phase-check routes an open phase to the stuck gate before a human
# is asked, so the phase-progress guard itself is never reached. Here a gate
# approval runs into the guard, which must decline for real. Sections:
#   A  the fixture graph validates
#   B  Phase 1 implement names Phase 1 and carries the duplicate-number warning
#      with a spec-phase-numbers row; plan closes Phase 1; ${completed_phase}
#      in the gate names Phase 1, the guard allows and the commit names it
#   C  Phase 2: plan ticks only the verification-notes box. The guard declines
#      — that heading is no phase — and the decline and the stuck gate name
#      Phase 2, its deliberately open item and the deliberate hold
#   D  negative control — the duplicate removed and the item ticked: the retry
#      dispatches with no warning, the gate names Phase 2, the guard allows,
#      the loop ends at the final gate with no hold wording, and the run
#      completes
# A coverage floor keeps a skipped section from reporting green.
#
# ISOLATION: scratch BUS_SESSION, HOME, repo dir via MUXCODE_SESSION_REPO_DIR,
# lifecycle log, private tmux server, empty config.
#
# REQUIRES: tmux, git, jq; a muxcode with MUX-130 — the installed one, or
# MUXCODE_BIN.
#
# Usage: bash scripts/test-spec-phase-parsing.sh
set -uo pipefail
export MUXCODE_CONSUMER_GUARD=0 # agents here are played by reading their inboxes (MUX-145)

MUX="${MUXCODE_BIN:-muxcode}"
MUX="$(command -v "$MUX" 2>/dev/null || echo "$MUX")"
[ -x "$MUX" ] || { echo "  FAIL  cannot resolve muxcode binary ('$MUX') — set MUXCODE_BIN"; exit 1; }
for t in tmux git jq; do
  command -v "$t" >/dev/null 2>&1 || { echo "SKIP: $t is required"; exit 2; }
done
. "$(dirname "${BASH_SOURCE[0]}")/lib/muxcode-bus-dir.sh"

GREEN=$'\033[0;32m'; RED=$'\033[0;31m'; NC=$'\033[0m'
pass=0; fail=0
EXPECTED=34
ok()  { echo "  ${GREEN}PASS${NC}  $*"; pass=$((pass + 1)); }
bad() { echo "  ${RED}FAIL${NC}  $*"; fail=$((fail + 1)); }
check() { local desc=$1; shift; if "$@" >/dev/null 2>&1; then ok "$desc"; else bad "$desc"; fi; }
contains() { case $1 in *"$2"*) return 0 ;; esac; return 1; }
lacks() { case $1 in *"$2"*) return 1 ;; esac; return 0; }

# --- Isolation -------------------------------------------------------------
export BUS_SESSION="phaseparse-test-$$"
WORK="/tmp/phaseparse-work-$$"
REPO="$WORK/repo"
mkdir -p "$REPO/docs/requirements/drafts"
export HOME="$WORK/home"
mkdir -p "$HOME/.config/muxcode"
echo "MUXCODE_GATE_AUTHORITY_ROLES=test-approver" > "$HOME/.config/muxcode/config"
git -C "$REPO" init -q
git -C "$REPO" config user.email "muxcode-test@example.invalid"
git -C "$REPO" config user.name "muxcode-test"
echo fixture > "$REPO/README.md"
git -C "$REPO" add README.md && git -C "$REPO" commit -q -m "fixture base"
export MUXCODE_LIFECYCLE_LOG_DIR="$WORK/lifecycle"
: > "$WORK/empty-config"
export MUXCODE_CONFIG="$WORK/empty-config"
export MUXCODE_TMP_CLEANUP_THRESHOLD=0 MUXCODE_BRANCH_TIME_DISABLE=1 MUXCODE_DEDUP_WINDOW=0
export MUXCODE_SESSION_REPO_DIR="$REPO"
BD="$(muxcode_bus_dir "$MUX" "$BUS_SESSION")" || exit 1
LIFELOG="$MUXCODE_LIFECYCLE_LOG_DIR/$BUS_SESSION.log"

DPID=""
. "$(dirname "${BASH_SOURCE[0]}")/lib/private-tmux.sh"
private_tmux_init "$WORK/tmux"
cleanup() {
  [ -n "$DPID" ] && kill "$DPID" 2>/dev/null
  private_tmux_kill
  rm -rf "$BD" "$WORK"
}
trap cleanup EXIT
tmux new-session -d -s "$BUS_SESSION" -n edit -c "$WORK" "cat > /dev/null"
"$MUX" init >/dev/null 2>&1

approve_gate() { AGENT_ROLE=test-approver "$MUX" graph approve "$@" >/dev/null 2>&1; }
node_state() { "$MUX" graph status "$1" 2>/dev/null | sed $'s/\x1b\\[[0-9;]*[A-Za-z]//g' | awk -v n="$2" '$1==n {print $2}'; }
run_state()  { "$MUX" graph status "$1" 2>/dev/null | sed $'s/\x1b\\[[0-9;]*[A-Za-z]//g' | head -1 | sed 's/.*\[\([a-z]*\)\].*/\1/'; }
wait_node_state() {
  local i
  for i in $(seq 1 80); do [ "$(node_state "$1" "$2")" = "$3" ] && return 0; sleep 0.5; done
  return 1
}

CAPTURED=""; REQ_ID=""
# wait_request ROLE ACTION — the newest matching request, by its reply id; see
# test-multi-phase-graph.sh for why it is block-scoped on the request.
wait_request() {
  local i out block rid
  for i in $(seq 1 60); do
    out="$(AGENT_ROLE="$1" "$MUX" inbox --peek 2>/dev/null || true)"
    block="$(printf '%s\n' "$out" | awk -v RS='--- Message' -v a="Action: $2" \
      'index($0, "Type: request") && index($0, a) {blk=$0} END{print blk}')"
    rid="$(printf '%s\n' "$block" | grep -o -- '--reply-to [A-Za-z0-9-]*' | head -1 | awk '{print $2}')"
    if [ -n "$rid" ]; then
      REQ_ID="$rid"
      CAPTURED="$(printf '%s\n' "$block" | grep '^Content:' | head -1)"
      return 0
    fi
    sleep 0.5
  done
  return 1
}
answer_request() {
  AGENT_ROLE="$1" "$MUX" inbox >/dev/null 2>&1
  AGENT_ROLE="$1" "$MUX" send edit response "${2:-done} EXIT=0" --type response --reply-to "$REQ_ID" >/dev/null 2>&1
}
wait_and_answer() { wait_request "$1" "$2" && answer_request "$1"; }
# gate_text GATE — the newest graph-approval prompt edit received for GATE.
gate_text() {
  jq -r --arg g "$1" 'select(.action == "graph-approval" and (.payload | contains("human gate \"" + $g + "\""))) | .payload' \
    "$BD/log.jsonl" 2>/dev/null | tail -1
}
rows() { local c; c="$(grep -c "\"event\":\"$1\"" "$LIFELOG" 2>/dev/null)"; echo "${c:-0}"; }
decline_rows() { jq -r 'select(.event == "graph-guard-declined") | .detail' "$LIFELOG" 2>/dev/null; }

SPEC_REL="docs/requirements/drafts/fixture-spec.md"
SPEC="$REPO/$SPEC_REL"
# write_spec P1 P2 NOTE DUP — the fixture's three boxes, and whether the
# duplicate Phase 2 heading is present.
write_spec() {
  {
    printf '# Fixture Spec\n\n## Requirements\n\n- [x] criteria are verified per phase\n\n## Implementation\n\n'
    printf '### Phase 1: First\n\n- [%s] step one\n\n' "$1"
    printf '### Phase 2: Second\n\n- [%s] keep open on purpose\n\n' "$2"
    [ "$4" = dup ] && printf '### Phase 2: Duplicate numbering\n\n- [x] a duplicate section\n\n'
    printf '## Verification notes\n\n### Phase 3 (2026-11-27) — verified\n\n- [%s] verification note\n' "$3"
  } > "$SPEC"
}
commit_spec() { git -C "$REPO" add "$SPEC_REL" && git -C "$REPO" commit -q --allow-empty -m "$1"; }

cat > "$WORK/phaseparse.json" <<'EOF'
{"name": "phaseparse", "start": "implement",
 "nodes": [
   {"id": "implement", "type": "send", "role": "edit", "action": "g-edit", "message": "Implement ${current_phase}"},
   {"id": "review", "type": "send", "role": "review", "action": "g-review", "message": "review"},
   {"id": "update-spec", "type": "send", "role": "plan", "action": "g-verify", "message": "Check off ${current_phase}"},
   {"id": "phase-gate", "type": "wait_human", "message": "Approve committing ${completed_phase}"},
   {"id": "commit", "type": "send", "role": "commit", "action": "g-commit", "guard": "phase-progress", "message": "Commit ${completed_phase}"},
   {"id": "loop-check", "type": "condition", "conditions": {"spec_phases_remaining": true}},
   {"id": "stuck-gate", "type": "wait_human", "message": "The current phase did not complete. ${phase_hold} Approve retrying it"},
   {"id": "final-gate", "type": "wait_human", "message": "All phases done. ${phase_hold} Approve push and PR"},
   {"id": "push-pr", "type": "send", "role": "commit", "action": "g-commit", "message": "Push and open the PR"}],
 "edges": [
   {"from": "implement", "to": "review"},
   {"from": "review", "to": "update-spec"},
   {"from": "update-spec", "to": "phase-gate"},
   {"from": "phase-gate", "to": "commit"},
   {"from": "commit", "to": "loop-check"},
   {"from": "commit", "to": "stuck-gate", "outcome": "failure"},
   {"from": "stuck-gate", "to": "implement", "max_iterations_from_spec": true},
   {"from": "loop-check", "to": "implement", "max_iterations_from_spec": true},
   {"from": "loop-check", "to": "final-gate", "outcome": "failure"},
   {"from": "final-gate", "to": "push-pr"}]}
EOF

echo "=== MUX-130 spec phase parsing, end to end ==="

# --- A. validation ---------------------------------------------------------
check "A: the fixture graph validates" "$MUX" graph validate "$WORK/phaseparse.json"

write_spec " " " " " " dup
commit_spec "fixture spec baseline"
(cd "$REPO" && "$MUX" spec set "$SPEC_REL" >/dev/null 2>&1) && ok "A: active spec set" || bad "A: spec set failed"
(cd "$REPO" && exec "$MUX" watch "$BUS_SESSION" --poll 1 >"$WORK/daemon.log" 2>&1) &
DPID=$!
sleep 1
kill -0 "$DPID" 2>/dev/null && ok "A: scratch daemon running" || bad "A: daemon exited: $(tail -3 "$WORK/daemon.log")"

RID="$("$MUX" graph run --file "$WORK/phaseparse.json" "walk the fixture spec" 2>&1 | grep -o 'Started run [^ ]*' | awk '{print $3}')"
[ -n "$RID" ] && ok "A: run started ($RID)" || bad "A: run failed to start"

# --- B. Phase 1: warning, then a legitimate commit ---------------------------
echo "-- B: Phase 1"
check "B: implement dispatched" wait_request edit g-edit
check "B: it names Phase 1 (got: ${CAPTURED:0:120})" contains "$CAPTURED" "Phase 1: First"
check "B: it carries the duplicate-number warning" contains "$CAPTURED" "phase number(s) 2 repeat or are out of order"
check "B: a spec-phase-numbers row was logged" [ "$(rows spec-phase-numbers)" -ge 1 ]
answer_request edit
check "B: review answered" wait_and_answer review g-review
write_spec x " " " " dup
check "B: update-spec answered after plan closed Phase 1" wait_and_answer plan g-verify
check "B: the phase gate waits" wait_node_state "$RID" phase-gate waiting
check "B: \${completed_phase} in the gate names Phase 1" contains "$(gate_text phase-gate)" "Approve committing Phase 1: First"
approve_gate "$RID" phase-gate
if wait_request commit g-commit; then
  check "B: the guard allowed it and the commit names Phase 1" contains "$CAPTURED" "Commit Phase 1: First"
  commit_spec "ship phase 1"
  answer_request commit committed
else
  bad "B: the commit was never dispatched"
fi

# --- C. Phase 2: only the verification note is ticked -------------------------
echo "-- C: Phase 2, its item left open on purpose"
check "C: implement dispatched for Phase 2" wait_and_answer edit g-edit
check "C: review answered" wait_and_answer review g-review
write_spec x " " x dup
check "C: update-spec answered after plan ticked only the verification note" wait_and_answer plan g-verify
check "C: the phase gate waits" wait_node_state "$RID" phase-gate waiting
approve_gate "$RID" phase-gate
check "C: the guard declined into the stuck gate" wait_node_state "$RID" stuck-gate waiting
DECLINE="$(decline_rows | tail -1)"
check "C: the decline names Phase 2 and its open item" contains "$DECLINE" "Phase 2: Second still has 1 open item(s): keep open on purpose"
check "C: the decline says the hold is deliberate" contains "$DECLINE" "left open on purpose holds the loop"
STUCK="$(gate_text stuck-gate)"
check "C: the stuck gate names Phase 2 and its open item" contains "$STUCK" "Phase 2: Second still has 1 open item(s): keep open on purpose"
check "C: the stuck gate says the hold is deliberate" contains "$STUCK" "until it is ticked or moved under ### Deferred"
check "C: no commit was dispatched for the verification-notes heading" \
  [ "$(AGENT_ROLE=commit "$MUX" inbox --peek 2>/dev/null | grep -c 'Commit Phase 3')" -eq 0 ]

# --- D. negative control: duplicate removed, item ticked --------------------
echo "-- D: negative control — the item ticked, the duplicate removed"
WARN_ROWS=$(rows spec-phase-numbers)
write_spec x " " x nodup
commit_spec "remove the duplicate"
approve_gate "$RID" stuck-gate
check "D: the retry dispatched implement" wait_request edit g-edit
check "D: with no duplicate-number warning" lacks "$CAPTURED" "repeat or are out of order"
answer_request edit
check "D: review answered" wait_and_answer review g-review
write_spec x x x nodup
check "D: update-spec answered after plan ticked the item" wait_and_answer plan g-verify
check "D: the phase gate waits" wait_node_state "$RID" phase-gate waiting
check "D: \${completed_phase} in the gate names Phase 2" contains "$(gate_text phase-gate)" "Approve committing Phase 2: Second"
approve_gate "$RID" phase-gate
if wait_request commit g-commit; then
  commit_spec "ship phase 2"
  answer_request commit committed
  check "D: the final gate waits with no hold wording" wait_node_state "$RID" final-gate waiting
  FINAL="$(gate_text final-gate)"
  check "D: the final gate reads honestly complete (got: ${FINAL:0:120})" contains "$FINAL" "Nothing in the spec is open."
  check "D: and carries no hold wording" lacks "$FINAL" "holds the loop"
  approve_gate "$RID" final-gate
  wait_and_answer commit g-commit
  done_ok=1
  for i in $(seq 1 40); do [ "$(run_state "$RID")" = complete ] && break; sleep 0.5; [ "$i" -eq 40 ] && done_ok=0; done
  check "D: the run completes" [ "$done_ok" -eq 1 ]
else
  bad "D: the commit for Phase 2 was never dispatched"
fi
check "D: no further spec-phase-numbers row once the duplicate is gone" [ "$(rows spec-phase-numbers)" -eq "$WARN_ROWS" ]

# --- Coverage floor ----------------------------------------------------------
echo ""
echo "=== $pass passed, $fail failed (floor $EXPECTED) ==="
if [ "$pass" -ne "$EXPECTED" ]; then
  echo "  ${RED}FAIL${NC}  coverage floor: expected exactly $EXPECTED passes, got $pass — a section was skipped or double-counted"
  exit 1
fi
[ "$fail" -eq 0 ]
