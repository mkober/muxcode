#!/usr/bin/env bash
# Integration test for the per-phase fix budget (MUX-193).
#
# 50-spec-to-pr caps fix->build at three, and that counter used to live for
# the whole run: run 1790608128 failed at Phase 4 with its budget spent by
# Phases 1 and 2. The phase loop-back edges now declare
# "resets_iterations": ["fix->build"], so each phase starts with three fixes.
#
# Drives a real scratch daemon through the template's inner cycle and phase
# loop with fake agents that fail review a scripted number of times per phase:
#   1. validation — fixture and builtin validate; a reset naming no edge, a
#      self-reset and an uncapped cycle are rejected
#   2. a five-phase spec whose Phases 1 and 4 each fail review twice reaches
#      close-out, writing one reset row per phase entered, each naming it
#   3. negative control — one phase failing review four times exhausts
#      fix->build and fails the run; Phase 2 is never dispatched
#
# DEVIATION: the fixture keeps the template's implement/build/test/review/
# fix cycle and its spec_phases_remaining loop-check, but drops the commit
# gates (update-spec routes straight to loop-check) and uses send nodes for
# the spawns — the budget lives entirely in the cycle, and gates would add
# human approvals and git commits that test nothing here. The implement and
# fix stand-ins run as analyze: a fake edit worker's reply to edit would be
# dropped as a self-send. Unlike test-multi-phase-graph.sh it has no spawn
# sections, which are red there under MUX-178.
#
# ISOLATION: scratch BUS_SESSION, scratch repo via MUXCODE_SESSION_REPO_DIR,
# lifecycle log in a temp dir, empty config.
#
# REQUIRES: installed muxcode carrying MUX-193 (run ./build.sh first) — the
# precondition is probed in section 1, since the change is not yet tagged.
#
# Usage: bash scripts/test-fix-loop-cap.sh
set -uo pipefail

MUX="${MUXCODE_BIN:-muxcode}"
MUX="$(command -v "$MUX" 2>/dev/null || echo "$MUX")"
if [ ! -x "$MUX" ]; then
  echo "  FAIL  cannot resolve muxcode binary ('$MUX') — set MUXCODE_BIN"
  exit 1
fi
. "$(dirname "${BASH_SOURCE[0]}")/lib/muxcode-version.sh"
require_muxcode_version "$MUX" v0.1.0 MUX-193 || { echo "  FAIL  binary precondition not met"; exit 1; }

GREEN=$'\033[0;32m'; RED=$'\033[0;31m'; NC=$'\033[0m'
pass=0; fail=0
ok()  { echo "  ${GREEN}PASS${NC}  $*"; pass=$((pass + 1)); }
bad() { echo "  ${RED}FAIL${NC}  $*"; fail=$((fail + 1)); dump_diag; }

DUMPED=0
dump_diag() {
  [ "$DUMPED" -eq 1 ] && return
  DUMPED=1
  echo "  --- diagnostic dump (first failure) ---"
  local rid
  for rid in ${RID:-} ${RID_N:-}; do
    "$MUX" graph status "$rid" 2>/dev/null | sed $'s/\x1b\\[[0-9;]*[A-Za-z]//g'
  done
  grep -h '"event":"graph-' "$LOG" 2>/dev/null | tail -8
  echo "  --- end diagnostic ---"
}

# --- Isolation -------------------------------------------------------------
export BUS_SESSION="fixloop-test-$$"
BD="/tmp/muxcode-bus-${BUS_SESSION}"
WORK="/tmp/fixloop-work-$$"
REPO="$WORK/repo"
mkdir -p "$REPO/docs/requirements/drafts"
export HOME="$WORK/home"
mkdir -p "$HOME/.config/muxcode"
git -C "$REPO" init -q
export MUXCODE_LIFECYCLE_LOG_DIR="$WORK/lifecycle"
LOG="$MUXCODE_LIFECYCLE_LOG_DIR/${BUS_SESSION}.log"
: > "$WORK/empty-config"
export MUXCODE_CONFIG="$WORK/empty-config"
export MUXCODE_TMP_CLEANUP_THRESHOLD=0
export MUXCODE_BRANCH_TIME_DISABLE=1
export MUXCODE_DEDUP_WINDOW=0
export MUXCODE_SESSION_REPO_DIR="$REPO"

DPID=""
cleanup() {
  [ -n "$DPID" ] && kill "$DPID" 2>/dev/null
  [ "${MUXCODE_TEST_KEEP:-}" = "1" ] && { echo "  (kept for diagnosis: $WORK)"; return; }
  rm -rf "$BD" "$WORK"
}
trap cleanup EXIT

"$MUX" init >/dev/null 2>&1

CAPTURED=""
REQ_ID=""

# wait_request <role> <action> — wait for a matching REQUEST without
# consuming it, capturing its payload and correlation id. Block-scoped on the
# request message: edit's inbox also collects this script's own replies.
wait_request() {
  local role="$1" action="$2" i out block rid
  for i in $(seq 1 80); do
    out="$(AGENT_ROLE="$role" "$MUX" inbox --peek 2>/dev/null || true)"
    block="$(printf '%s\n' "$out" | awk -v RS='--- Message' -v a="Action: $action" \
      'index($0, "Type: request") && index($0, a) {blk=$0} END{print blk}')"
    if [ -n "$block" ]; then
      rid="$(printf '%s\n' "$block" | grep -o -- '--reply-to [A-Za-z0-9-]*' | head -1 | awk '{print $2}')"
      if [ -n "$rid" ]; then
        REQ_ID="$rid"
        CAPTURED="$(printf '%s\n' "$block" | grep '^Content:' | head -1)"
        return 0
      fi
    fi
    sleep 0.5
  done
  return 1
}

# answer <role> <exit> [text] — consume the role's inbox and reply to the
# captured request. The EXIT sentinel is the verdict: these fake agents run
# no hooks, so no history row testifies for them, and EXIT=1 is how review
# reports findings that route to fix.
answer() {
  AGENT_ROLE="$1" "$MUX" inbox >/dev/null 2>&1
  AGENT_ROLE="$1" "$MUX" send edit response "${3:-done} EXIT=$2" --type response --reply-to "$REQ_ID" >/dev/null 2>&1
}

wait_and_answer() {
  wait_request "$1" "$2" || return 1
  answer "$1" 0
}

run_state() { "$MUX" graph status "$1" 2>/dev/null | sed $'s/\x1b\\[[0-9;]*[A-Za-z]//g' | head -1 | sed 's/.*\[\([a-z]*\)\].*/\1/'; }

wait_run_state() {
  local i
  for i in $(seq 1 80); do
    [ "$(run_state "$1")" = "$2" ] && return 0
    sleep 0.5
  done
  return 1
}

# events <event> <run-id> — lifecycle rows of one event for one run, oldest
# first. Read through the CLI: the log file JSON-escapes the `>` in edge keys.
events() {
  "$MUX" lifecycle show "$BUS_SESSION" --all --event "$1" 2>/dev/null \
    | sed $'s/\x1b\\[[0-9;]*[A-Za-z]//g' | grep -F "$2" || true
}

SPEC_FILE="$REPO/docs/requirements/drafts/fixture-spec.md"

# write_spec <n> — a spec of n open phases, set active.
write_spec() {
  local i
  { echo "# Fixture Spec"; for i in $(seq 1 "$1"); do printf '\n### Phase %d: Step %d\n- [ ] work %d\n' "$i" "$i" "$i"; done; } > "$SPEC_FILE"
  (cd "$REPO" && "$MUX" spec set docs/requirements/drafts/fixture-spec.md >/dev/null 2>&1)
}

# tick_phase — what plan's verify-spec does: close the current phase.
tick_phase() { perl -i -pe 's/- \[ \]/- [x]/ && ($done=1) unless $done' "$SPEC_FILE"; }

# drive_phase <n> <review-failures> — walk one phase, failing its review the
# given number of times and answering each failure with a fix.
drive_phase() {
  local n="$1" k="$2" i
  wait_request analyze g-implement || { bad "phase $n: implement never dispatched"; return 1; }
  case "$CAPTURED" in
    *"Phase $n:"*) ok "phase $n: implement targeted Phase $n" ;;
    *) bad "phase $n: implement message wrong: $CAPTURED" ;;
  esac
  answer analyze 0 implemented
  for ((i = 0; ; i++)); do
    wait_and_answer build g-build || { bad "phase $n: build $((i + 1)) never dispatched"; return 1; }
    wait_and_answer test g-test || { bad "phase $n: test $((i + 1)) never dispatched"; return 1; }
    wait_request review g-review || { bad "phase $n: review $((i + 1)) never dispatched"; return 1; }
    [ "$i" -eq "$k" ] && { answer review 0 "0 findings"; break; }
    answer review 1 "1 must-fix"
    wait_and_answer analyze g-fix || { bad "phase $n: fix $((i + 1)) never dispatched"; return 1; }
  done
  ok "phase $n: $k review failures fixed within its own budget"
  tick_phase
  wait_and_answer plan g-verify || { bad "phase $n: update-spec never dispatched"; return 1; }
}

cat > "$WORK/fixloop.json" <<'EOF'
{"name": "fixloop", "start": "implement",
 "nodes": [
   {"id": "implement", "type": "send", "role": "analyze", "action": "g-implement", "message": "Implement ${current_phase}"},
   {"id": "build", "type": "send", "role": "build", "action": "g-build", "message": "build"},
   {"id": "test", "type": "send", "role": "test", "action": "g-test", "message": "test"},
   {"id": "fix", "type": "send", "role": "analyze", "action": "g-fix", "message": "fix ${current_phase}"},
   {"id": "review", "type": "send", "role": "review", "action": "g-review", "message": "review"},
   {"id": "update-spec", "type": "send", "role": "plan", "action": "g-verify", "message": "Check off ${current_phase}"},
   {"id": "loop-check", "type": "condition", "conditions": {"spec_phases_remaining": true}},
   {"id": "close-spec", "type": "send", "role": "plan", "action": "g-close", "message": "close out"}],
 "edges": [
   {"from": "implement", "to": "build"},
   {"from": "build", "to": "test"},
   {"from": "build", "to": "fix", "outcome": "failure"},
   {"from": "test", "to": "review"},
   {"from": "test", "to": "fix", "outcome": "failure"},
   {"from": "fix", "to": "build", "max_iterations": 3},
   {"from": "review", "to": "update-spec"},
   {"from": "review", "to": "fix", "outcome": "failure"},
   {"from": "update-spec", "to": "loop-check"},
   {"from": "loop-check", "to": "implement", "max_iterations_from_spec": true, "resets_iterations": ["fix->build"]},
   {"from": "loop-check", "to": "close-spec", "outcome": "failure"}]}
EOF

# --- 1. Validation ---------------------------------------------------------
# The unknown-edge control doubles as the binary precondition: a muxcode
# predating MUX-193 ignores resets_iterations and accepts it.
sed 's/"resets_iterations": \["fix->build"\]/"resets_iterations": ["fix->nowhere"]/' \
  "$WORK/fixloop.json" > "$WORK/unknown-reset.json"
if "$MUX" graph validate "$WORK/unknown-reset.json" >/dev/null 2>&1; then
  bad "reset naming no edge accepted — installed muxcode predates MUX-193 (run ./build.sh)"
  echo ""; echo "  ${pass} passed, ${fail} failed"; exit 1
fi
ok "negative control: a reset naming no edge fails validation"

"$MUX" graph validate "$WORK/fixloop.json" >/dev/null 2>&1 \
  && ok "fixture with a per-phase reset validates" \
  || bad "fixture failed validation: $("$MUX" graph validate "$WORK/fixloop.json" 2>&1 | tail -3)"

"$MUX" graph validate 50-spec-to-pr >/dev/null 2>&1 \
  && ok "real 50-spec-to-pr builtin validates with its resets" \
  || bad "50-spec-to-pr builtin failed validation"

sed 's/"resets_iterations": \["fix->build"\]/"resets_iterations": ["loop-check->implement"]/' \
  "$WORK/fixloop.json" > "$WORK/self-reset.json"
"$MUX" graph validate "$WORK/self-reset.json" >/dev/null 2>&1 \
  && bad "NEGATIVE CONTROL: an edge resetting its own budget accepted" \
  || ok "negative control: a self-reset fails validation"

sed 's/, "max_iterations": 3//' "$WORK/fixloop.json" > "$WORK/uncapped.json"
"$MUX" graph validate "$WORK/uncapped.json" >/dev/null 2>&1 \
  && bad "NEGATIVE CONTROL: uncapped fix loop accepted" \
  || ok "negative control: an uncapped cycle still fails validation"

# --- 2. Daemon -------------------------------------------------------------
(cd "$REPO" && exec "$MUX" watch "$BUS_SESSION" --poll 2 >"$WORK/daemon.log" 2>&1) &
DPID=$!
sleep 1
kill -0 "$DPID" 2>/dev/null && ok "scratch daemon running (pid $DPID)" \
  || bad "scratch daemon exited: $(tail -3 "$WORK/daemon.log" 2>/dev/null)"

# --- 3. Headline: five phases, Phases 1 and 4 fail review twice ------------
# Four fixes in one run: under the run-wide budget Phase 4's second fix was
# the fourth and failed the run.
write_spec 5
RID="$("$MUX" graph run --file "$WORK/fixloop.json" "walk five phases" 2>&1 | grep -o 'Started run [^ ]*' | awk '{print $3}')"
[ -n "$RID" ] && ok "headline run started: $RID" || bad "headline run failed to start"

phase=0
for failures in 2 0 0 2 0; do
  phase=$((phase + 1))
  drive_phase "$phase" "$failures" || break
done

wait_and_answer plan g-close \
  && ok "close-out dispatched after all five phases" \
  || bad "close-spec never dispatched: $(run_state "$RID")"
wait_run_state "$RID" complete \
  && ok "run completed with four fixes spent across two phases" \
  || bad "headline run state: $(run_state "$RID")"

[ -z "$(events graph-loop-exhausted "$RID")" ] \
  && ok "no loop cap exhausted on the headline run" \
  || bad "headline run exhausted a cap: $(events graph-loop-exhausted "$RID")"

resets="$(events graph-loop-budget-reset "$RID")"
count="$(printf '%s' "$resets" | grep -c . || true)"
[ "$count" -eq 4 ] && ok "one reset row per phase entered by the loop-back (4)" \
  || bad "want 4 reset rows, got $count: $resets"

named=1
for n in 2 3 4 5; do
  line="$(printf '%s\n' "$resets" | sed -n "$((n - 1))p")"
  case "$line" in
    *"entering Phase $n:"*"fix->build:success"*) ;;
    *) named=0; bad "reset row $((n - 1)) does not name Phase $n and fix->build: $line" ;;
  esac
done
[ "$named" -eq 1 ] && ok "each reset row names the phase entered and the edge cleared, in order"

# The discriminating assertion: each reset clears the phase just finished —
# 2, 0, 0, 2 — where a run-wide counter would read 2, 2, 2, 4.
cleared="$(printf '%s\n' "$resets" | grep -o '(was [0-9]*)' | tr -d '()was ' | tr '\n' ' ')"
[ "$cleared" = "2 0 0 2 " ] \
  && ok "each reset cleared only the phase just finished (2 0 0 2)" \
  || bad "reset counts '$cleared', want '2 0 0 2 ' — the budget is not per phase"

# --- 4. Negative control: one phase needs a fourth fix ---------------------
write_spec 2
RID_N="$("$MUX" graph run --file "$WORK/fixloop.json" "exhaust one phase" 2>&1 | grep -o 'Started run [^ ]*' | awk '{print $3}')"
[ -n "$RID_N" ] && ok "negative-control run started: $RID_N" || bad "negative-control run failed to start"

neg_ok=1
wait_and_answer analyze g-implement || { neg_ok=0; bad "negative control: implement never dispatched"; }
for i in 1 2 3 4; do
  [ "$neg_ok" -eq 1 ] || break
  wait_and_answer build g-build || { neg_ok=0; bad "negative control: build $i never dispatched"; break; }
  wait_and_answer test g-test || { neg_ok=0; bad "negative control: test $i never dispatched"; break; }
  wait_request review g-review || { neg_ok=0; bad "negative control: review $i never dispatched"; break; }
  answer review 1 "1 must-fix"
  wait_and_answer analyze g-fix || { neg_ok=0; bad "negative control: fix $i never dispatched"; break; }
done

wait_run_state "$RID_N" failed \
  && ok "a fourth fix in one phase fails the run" \
  || bad "negative-control run state: $(run_state "$RID_N")"

case "$(events graph-loop-exhausted "$RID_N")" in
  *"fix->build:success exhausted after 3"*) ok "graph-loop-exhausted names fix->build at its cap of 3" ;;
  *) bad "no graph-loop-exhausted row naming fix->build: $(events graph-loop-exhausted "$RID_N")" ;;
esac

[ -n "$(events graph-run-failed "$RID_N")" ] \
  && ok "graph-run-failed recorded for the exhausted phase" \
  || bad "no graph-run-failed row for $RID_N"

sleep 3
if AGENT_ROLE=analyze "$MUX" inbox --peek 2>/dev/null | grep -q 'Action: g-implement'; then
  bad "Phase 2 was dispatched after the run failed"
else
  ok "Phase 2 never dispatched"
fi

[ -z "$(events graph-loop-budget-reset "$RID_N")" ] \
  && ok "no reset row — the exhausted run never entered another phase" \
  || bad "negative-control run wrote a reset row: $(events graph-loop-budget-reset "$RID_N")"

# --- Coverage floor --------------------------------------------------------
# A clean pass executes EXACTLY 29 checks: 5 validation + daemon + 17
# headline (start, 2 per phase, close-out, complete, no exhaustion, 4 rows,
# rows named, cleared counts) + 6 negative control. Equality, not >=, so a
# short-circuited run cannot report green.
total=$((pass + fail))
if [ "$total" -eq 29 ]; then
  ok "coverage floor met and equals max ($total checks executed)"
else
  bad "coverage floor mismatch — $total checks executed, want exactly 29"
fi

echo ""
echo "  ${pass} passed, ${fail} failed"
[ "$fail" -eq 0 ] || exit 1
exit 0
