#!/usr/bin/env bash
# Integration test for the multi-phase sequential graph (MUX-121).
#
# Exercises the real daemon executor end to end: a 3-phase fixture spec is
# walked to completion in ONE run — per-phase human-gated commits in phase
# order, the spec updated before each commit, stateless derivation starting
# at the first OPEN phase, loop termination with no hardcoded count, and
# nothing pushing before the final gate. A stuck phase (spec never updated)
# declines its commit into the stuck gate. Negative controls pin that an
# ungated commit and an uncapped loop edge still fail validation.
#
# DEVIATION from the spec checklist: the fixture graph mirrors the builtin
# req-code-pr's exact shape with SEND nodes in place of its two spawns —
# StartSpawn launches real agent windows and AI CLIs, which a hermetic
# script must not do (same deviation as test-graph-orchestrator.sh). The
# builtin's own shape is pinned by TestReqCodePRMultiPhaseLoop, and this
# script validates the real builtin live.
#
# Sections 6-8 (MUX-131) lift that deviation for the implement node only:
# Defect A lives in the spawn dispatch/harvest path itself, so a send-node
# stand-in cannot reach it. Hermeticity holds because the scratch session's
# environment points the spawn role's CLI at a binary that cannot exist —
# `agent launch` exits before exec, the pane's shell survives (which is all
# worker-reuse liveness requires), and this script plays the worker over
# the bus. Covered end to end: worktree output ported to the checkout
# UNCOMMITTED before build dispatches, one worker reused across phases
# (spawn count from the store), the no-op iteration, worktree advance
# after the gated commit, and a clobber conflict failing the spawn node
# itself — before build, not at commit after a human gate.
#
# ISOLATION: scratch BUS_SESSION, scratch repo dir via
# MUXCODE_SESSION_REPO_DIR, lifecycle log in a temp dir, empty config.
#
# REQUIRES: the installed muxcode binary must include MUX-121 and the
# MUX-131 spawn-harvest machinery (worker reuse + port-on-completion —
# run ./build.sh first), and tmux must be available.
#
# Usage: bash scripts/test-multi-phase-graph.sh
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
bad() { echo "  ${RED}FAIL${NC}  $*"; fail=$((fail + 1)); dump_diag; }

# dump_diag — on the FIRST failure, dump every run's node states and the
# task store before the scratch session is torn down: it is the only way
# to tell "answer never correlated" from "routing fired but the target
# never armed" (plan postmortem 2026-08-28).
DUMPED=0
dump_diag() {
  [ "$DUMPED" -eq 1 ] && return
  DUMPED=1
  echo "  --- diagnostic dump (first failure) ---"
  local rid
  for rid in ${RID:-} ${RID2:-} ${RID3:-} ${RID_S:-} ${RID_B:-}; do
    "$MUX" graph status "$rid" 2>/dev/null | sed $'s/\x1b\\[[0-9;]*[A-Za-z]//g'
  done
  "$MUX" tasks 2>/dev/null | head -12
  echo "  --- end diagnostic ---"
}

# --- Isolation -------------------------------------------------------------
export BUS_SESSION="multiphase-test-$$"
BD="/tmp/muxcode-bus-${BUS_SESSION}"
WORK="/tmp/multiphase-work-$$"
REPO="$WORK/repo"
mkdir -p "$REPO/docs/requirements/drafts"
# The fixture repo is a REAL git repo: spawn nodes cut worktrees from the
# daemon's CWD repo, and the harvest diffs/applies between worktree and
# checkout. Worktrees land under the OS temp dir (SpawnWorktreeBase).
git -C "$REPO" init -q
git -C "$REPO" config user.email "muxcode-test@example.invalid"
git -C "$REPO" config user.name "muxcode-test"
echo "fixture" > "$REPO/README.md"
git -C "$REPO" add README.md
git -C "$REPO" commit -q -m "fixture base"
WTBASE="${TMPDIR:-/tmp}"; WTBASE="${WTBASE%/}/muxcode-spawn-${BUS_SESSION}"
export MUXCODE_LIFECYCLE_LOG_DIR="$WORK/lifecycle"
: > "$WORK/empty-config"
export MUXCODE_CONFIG="$WORK/empty-config"
export MUXCODE_TMP_CLEANUP_THRESHOLD=0
export MUXCODE_BRANCH_TIME_DISABLE=1
export MUXCODE_DEDUP_WINDOW=0
export MUXCODE_SESSION_REPO_DIR="$REPO"

DPID=""
cleanup() {
  [ -n "$DPID" ] && kill "$DPID" 2>/dev/null
  tmux kill-session -t "$BUS_SESSION" 2>/dev/null
  rm -rf "$BD" "$WORK" "$WTBASE"
}
trap cleanup EXIT

tmux new-session -d -s "$BUS_SESSION" -n edit -x 120 -y 30
# Session env reaches every pane the daemon later creates (spawn windows).
# The spawn's `agent launch edit` must fail fast instead of booting a real
# AI CLI: an unresolvable CLI binary errors before exec, leaving the pane's
# shell alive — which is all worker-reuse liveness requires. The script
# itself plays the worker over the bus.
tmux set-environment -t "$BUS_SESSION" BUS_SESSION "$BUS_SESSION"
tmux set-environment -t "$BUS_SESSION" MUXCODE_CONFIG "$WORK/empty-config"
tmux set-environment -t "$BUS_SESSION" MUXCODE_EDIT_CLI "muxcode-hermetic-absent-cli"
"$MUX" init >/dev/null 2>&1

CAPTURED=""
REQ_ID=""

# wait_request <role> <action> [exclude-reply-id] — wait for a matching
# REQUEST without consuming it, capturing ITS payload into CAPTURED and
# ITS correlation id into REQ_ID. Block-scoped on the request message:
# the inbox also accumulates this script's own "done" responses (graph
# replies route to edit), and a whole-inbox tail-1 capture grabbed a
# stale answer and replied to the wrong id — stalling phase 2 and, worse,
# capable of false PASSES (plan postmortem 2026-08-28). The split from
# answering exists for the spawn sections: the script must act as the
# worker (write worktree files) or the commit agent (git commit) BETWEEN
# a request arriving and its answer, or the daemon's next hop races the
# side effect. The exclusion id skips a previous iteration's
# already-answered seed.
wait_request() {
  local role="$1" action="$2" not="${3:-}" i out block rid
  for i in $(seq 1 60); do
    out="$(AGENT_ROLE="$role" "$MUX" inbox --peek 2>/dev/null || true)"
    # Match on the EXPECTED action, not any request: sequential
    # any-request waits desynced one message from phase 2 on (14/12 run —
    # commit captured verify-spec's text) because backstop re-drives and
    # clutter can put more than one request shape in an inbox.
    block="$(printf '%s\n' "$out" | awk -v RS='--- Message' -v a="Action: $action" \
      'index($0, "Type: request") && index($0, a) {blk=$0} END{print blk}')"
    if [ -n "$block" ]; then
      rid="$(printf '%s\n' "$block" | grep -o -- '--reply-to [A-Za-z0-9-]*' | head -1 | awk '{print $2}')"
      if [ -n "$rid" ] && [ "$rid" != "$not" ]; then
        REQ_ID="$rid"
        CAPTURED="$(printf '%s\n' "$block" | grep '^Content:' | head -1)"
        return 0
      fi
    fi
    sleep 0.5
  done
  return 1
}

# answer_request <role> [text] — consume the role's inbox (clutter
# included) and reply to the request wait_request captured.
answer_request() {
  AGENT_ROLE="$1" "$MUX" inbox >/dev/null 2>&1
  AGENT_ROLE="$1" "$MUX" send edit response "${2:-done}" --type response --reply-to "$REQ_ID" >/dev/null 2>&1
}

# wait_and_answer <role> <action> — wait_request + immediate answer, for
# hops with no side effect between arrival and reply.
wait_and_answer() {
  wait_request "$1" "$2" || return 1
  answer_request "$1"
}

# spawn_for_run <run-id> — poll the spawn store for the run's worker,
# setting SPAWN_ROLE and SPAWN_WT from the newest matching entry. Field
# extraction must yield EMPTY on a missing worktree (grep -o, not sed
# substitution, which passes the whole line through on no-match) so the
# caller's worktree assertion cannot false-pass.
SPAWN_ROLE=""
SPAWN_WT=""
spawn_for_run() {
  local rid="$1" i line
  for i in $(seq 1 60); do
    line="$(grep "\"run_id\":\"$rid\"" "$BD/spawn.jsonl" 2>/dev/null | tail -1)"
    if [ -n "$line" ]; then
      SPAWN_ROLE="$(printf '%s' "$line" | grep -o '"spawn_role":"[^"]*"' | cut -d'"' -f4)"
      SPAWN_WT="$(printf '%s' "$line" | grep -o '"worktree":"[^"]*"' | cut -d'"' -f4)"
      [ -n "$SPAWN_ROLE" ] && return 0
    fi
    sleep 0.5
  done
  return 1
}

# run_spawn_count <run-id> — worker entries the store holds for a run.
run_spawn_count() {
  local cnt
  cnt="$(grep -c "\"run_id\":\"$1\"" "$BD/spawn.jsonl" 2>/dev/null || true)"
  echo "${cnt:-0}"
}

run_state()  { "$MUX" graph status "$1" 2>/dev/null | sed $'s/\x1b\\[[0-9;]*[A-Za-z]//g' | head -1 | sed 's/.*\[\([a-z]*\)\].*/\1/'; }
node_state() { "$MUX" graph status "$1" 2>/dev/null | sed $'s/\x1b\\[[0-9;]*[A-Za-z]//g' | awk -v n="$2" '$1==n {print $2}'; }

wait_node_state() {
  local rid="$1" node="$2" want="$3" i
  # 40s: reaching a gate stacks five daemon-tick hops (harvest, route,
  # arm, dispatch, wait) — a 20s window timed out on a healthy run.
  for i in $(seq 1 80); do
    [ "$(node_state "$rid" "$node")" = "$want" ] && return 0
    sleep 0.5
  done
  return 1
}

# complete_current_phase — check off the first open item in the fixture
# spec (each phase carries exactly one, so this closes the current phase).
SPEC_FILE="$REPO/docs/requirements/drafts/fixture-spec.md"
complete_current_phase() {
  perl -i -pe 's/- \[ \]/- [x]/ && ($done=1) unless $done' "$SPEC_FILE"
}

write_spec() {
  cat > "$SPEC_FILE" <<EOF
# Fixture Spec

### Phase 1: First
- [$1] step one

### Phase 2: Second
- [$2] step two

### Phase 3: Third
- [$3] step three
EOF
  (cd "$REPO" && "$MUX" spec set docs/requirements/drafts/fixture-spec.md >/dev/null 2>&1)
}

# The fixture graph: the builtin's exact shape, send nodes for the spawns.
# Actions are g-* NAMESPACED: real action names (review, verify-spec, …)
# collide with the daemon's event-chain vocabulary — the scratch daemon's
# workflow chains fired their own daemon→plan verify-spec off the fake
# review responses, aliasing this script's captures and correlations
# (run-3 postmortem: every node completed outcome=unknown in 2s without
# an answer while the pipeline sprinted three phases ahead of the waits).
cat > "$WORK/multiphase.json" <<'EOF'
{"name": "multiphase", "start": "implement",
 "nodes": [
   {"id": "implement", "type": "send", "role": "edit", "action": "g-edit", "message": "Implement ${current_phase}"},
   {"id": "build", "type": "send", "role": "build", "action": "g-build", "message": "build"},
   {"id": "test", "type": "send", "role": "test", "action": "g-test", "message": "test"},
   {"id": "fix", "type": "send", "role": "edit", "action": "g-edit", "message": "fix"},
   {"id": "review", "type": "send", "role": "review", "action": "g-review", "message": "review"},
   {"id": "update-spec", "type": "send", "role": "plan", "action": "g-verify", "message": "Check off ${current_phase}"},
   {"id": "phase-gate", "type": "wait_human", "message": "Approve committing ${completed_phase} (commit only)"},
   {"id": "commit", "type": "send", "role": "commit", "action": "g-commit", "guard": "phase-progress", "message": "Commit ${completed_phase}"},
   {"id": "loop-check", "type": "condition", "conditions": {"spec_phases_remaining": true}},
   {"id": "stuck-gate", "type": "wait_human", "message": "Phase incomplete — approve retrying the commit-withheld phase"},
   {"id": "final-gate", "type": "wait_human", "message": "All phases done — approve push and PR"},
   {"id": "push-pr", "type": "send", "role": "commit", "action": "g-commit", "message": "Push and open the PR"}],
 "edges": [
   {"from": "implement", "to": "build"},
   {"from": "build", "to": "test"},
   {"from": "build", "to": "fix", "outcome": "failure"},
   {"from": "test", "to": "review"},
   {"from": "test", "to": "fix", "outcome": "failure"},
   {"from": "fix", "to": "build", "max_iterations": 3},
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

# --- 1. Validation: fixture, real builtin, negative controls ---------------
"$MUX" graph validate "$WORK/multiphase.json" >/dev/null 2>&1 \
  && ok "multi-phase fixture graph validates" \
  || bad "fixture graph failed validation"

"$MUX" graph validate req-code-pr >/dev/null 2>&1 \
  && ok "real req-code-pr builtin validates" \
  || bad "req-code-pr builtin failed validation"

sed 's/"guard": "phase-progress", //; s/{"id": "phase-gate", "type": "wait_human".*/{"id": "phase-gate", "type": "send", "role": "review", "action": "review", "message": "not a gate"},/' \
  "$WORK/multiphase.json" > "$WORK/ungated.json"
if "$MUX" graph validate "$WORK/ungated.json" >/dev/null 2>&1; then
  bad "NEGATIVE CONTROL: ungated commit accepted by validate"
else
  ok "negative control: ungated commit still fails validate"
fi

sed 's/, "max_iterations_from_spec": true//g' "$WORK/multiphase.json" > "$WORK/uncapped.json"
if "$MUX" graph validate "$WORK/uncapped.json" >/dev/null 2>&1; then
  bad "NEGATIVE CONTROL: uncapped loop edge accepted by validate"
else
  ok "negative control: uncapped loop edge still fails validation"
fi

# --- 2. Daemon -------------------------------------------------------------
# CWD = fixture repo: createSpawnWorktree resolves HEAD from the daemon's
# working directory, so starting anywhere else would cut spawn worktrees
# from the WRONG repo (this checkout).
(cd "$REPO" && exec "$MUX" watch "$BUS_SESSION" --poll 2 >"$WORK/daemon.log" 2>&1) &
DPID=$!
sleep 1
kill -0 "$DPID" 2>/dev/null && ok "scratch daemon running (pid $DPID)" \
  || bad "scratch daemon exited: $(tail -3 "$WORK/daemon.log" 2>/dev/null)"

# --- 3. Headline: 3 open phases, one run, ordered gated commits ------------
write_spec " " " " " "
RID="$("$MUX" graph run --file "$WORK/multiphase.json" "walk the fixture spec" 2>&1 | grep -o 'Started run [^ ]*' | awk '{print $3}')"
[ -n "$RID" ] && ok "headline run started: $RID" || bad "headline run failed to start"

COMMITS=()
for phase in 1 2 3; do
  wait_and_answer edit g-edit || bad "phase $phase: implement never dispatched"
  case "$CAPTURED" in
    *"Phase $phase:"*) ok "phase $phase: implement targeted the derived phase" ;;
    *) bad "phase $phase: implement message wrong: $CAPTURED" ;;
  esac
  wait_and_answer build g-build || bad "phase $phase: build never dispatched"
  wait_and_answer test g-test || bad "phase $phase: test never dispatched"
  wait_and_answer review g-review || bad "phase $phase: review never dispatched"
  # plan's turn: the spec is updated BEFORE answering, as the real plan does
  complete_current_phase
  wait_and_answer plan g-verify || bad "phase $phase: update-spec never dispatched"
  wait_node_state "$RID" phase-gate waiting || bad "phase $phase: gate never waited"
  # Per-commit approval is real: each pass must demand its own approval.
  "$MUX" graph approve "$RID" phase-gate >/dev/null 2>&1 || bad "phase $phase: approve failed"
  wait_and_answer commit g-commit || bad "phase $phase: commit never dispatched"
  COMMITS+=("$CAPTURED")
done

for i in 1 2 3; do
  case "${COMMITS[$((i-1))]:-}" in
    *"Phase $i:"*) ok "commit $i names Phase $i — phases committed in order" ;;
    *) bad "commit $i wrong phase: ${COMMITS[$((i-1))]:-<missing>}" ;;
  esac
done

wait_node_state "$RID" final-gate waiting \
  && ok "loop terminated to the final gate with no hardcoded count" \
  || bad "final gate never waited: $(run_state "$RID")"

push_reqs="$(AGENT_ROLE=commit "$MUX" inbox --peek 2>/dev/null | grep -c 'Push and open' || true)"
[ "$push_reqs" -eq 0 ] && ok "nothing pushed before the final gate" \
  || bad "push dispatched before final-gate approval"

"$MUX" graph approve "$RID" final-gate >/dev/null 2>&1 || bad "final-gate approve failed"
wait_and_answer commit g-commit || bad "push-pr never dispatched after final approval"
case "$CAPTURED" in
  *"Push and open"*) ok "final approval released push+PR" ;;
  *) bad "post-final dispatch wrong: $CAPTURED" ;;
esac

done_ok=0
for i in $(seq 1 40); do
  [ "$(run_state "$RID")" = "complete" ] && done_ok=1 && break
  sleep 0.5
done
[ "$done_ok" -eq 1 ] && ok "one run walked all 3 phases to complete" \
  || bad "headline run state: $(run_state "$RID")"

# --- 4. Start-at-Phase-2: completed phase is never re-implemented ----------
write_spec x " " " "
RID2="$("$MUX" graph run --file "$WORK/multiphase.json" 2>&1 | grep -o 'Started run [^ ]*' | awk '{print $3}')"
wait_and_answer edit g-edit || bad "start-at-2 implement never dispatched"
case "$CAPTURED" in
  *"Phase 2:"*) ok "run with Phase 1 complete started at Phase 2 (re-implementation guard)" ;;
  *) bad "start-at-2 targeted: $CAPTURED" ;;
esac
"$MUX" graph cancel "$RID2" >/dev/null 2>&1

# --- 5. Stuck phase: spec never updated → commit declines into the gate ----
write_spec " " " " " "
RID3="$("$MUX" graph run --file "$WORK/multiphase.json" 2>&1 | grep -o 'Started run [^ ]*' | awk '{print $3}')"
wait_and_answer edit g-edit; wait_and_answer build g-build; wait_and_answer test g-test; wait_and_answer review g-review
wait_and_answer plan g-verify   # answered WITHOUT checking anything off
wait_node_state "$RID3" phase-gate waiting || bad "stuck run: gate never waited"
"$MUX" graph approve "$RID3" phase-gate >/dev/null 2>&1
if wait_node_state "$RID3" stuck-gate waiting; then
  ok "incomplete phase declined its commit into the stuck gate (gate-and-ask)"
else
  bad "stuck-gate never armed: commit=$(node_state "$RID3" commit)"
fi
commit_reqs="$(AGENT_ROLE=commit "$MUX" inbox --peek 2>/dev/null | grep -c 'Type: request' || true)"
[ "$commit_reqs" -eq 0 ] && ok "withheld commit never reached the commit role" \
  || bad "commit dispatched despite incomplete phase"
"$MUX" graph cancel "$RID3" >/dev/null 2>&1

# --- 6. Spawn fixture: implement is a REAL spawn node (MUX-131) ------------
# Derived from the base fixture by rewriting ONLY the implement node, so
# the two graphs stay in lockstep everywhere else; the grep below makes a
# silently-failed derivation loud instead of quietly re-testing sends.
sed 's/"type": "send", "role": "edit", "action": "g-edit", "message": "Implement/"type": "spawn", "role": "edit", "message": "Implement/' \
  "$WORK/multiphase.json" > "$WORK/spawnphase.json"
if grep -q '"type": "spawn"' "$WORK/spawnphase.json" \
  && "$MUX" graph validate "$WORK/spawnphase.json" >/dev/null 2>&1; then
  ok "spawn-implement fixture derived and validates"
else
  bad "spawn-implement fixture failed derivation or validation"
fi

# write_spawn_spec — 2-phase variant: two iterations exercise worker reuse
# and the no-op pass without a third full round-trip.
write_spawn_spec() {
  cat > "$SPEC_FILE" <<EOF
# Fixture Spec

### Phase 1: First
- [$1] step one

### Phase 2: Second
- [$2] step two
EOF
  (cd "$REPO" && "$MUX" spec set docs/requirements/drafts/fixture-spec.md >/dev/null 2>&1)
}

# --- 7. Spawn run: port before build, one worker, no-op pass ---------------
write_spawn_spec " " " "
BASE_SHA="$(git -C "$REPO" rev-parse HEAD)"
RID_S="$("$MUX" graph run --file "$WORK/spawnphase.json" "spawn harvest walk" 2>&1 | grep -o 'Started run [^ ]*' | awk '{print $3}')"
[ -n "$RID_S" ] && ok "spawn run started: $RID_S" || bad "spawn run failed to start"

if spawn_for_run "$RID_S" && [ -n "$SPAWN_WT" ]; then
  ok "worker created with an isolated worktree ($SPAWN_ROLE)"
else
  bad "spawn worker or worktree never appeared for $RID_S"
fi
if wait_request "$SPAWN_ROLE" spawn-task; then
  ok "iteration-1 task seeded to the worker"
else
  bad "iteration-1 seed never arrived in $SPAWN_ROLE inbox"
fi
SEED1="$REQ_ID"

# The worker's phase-1 output goes only to the isolated worktree — before
# MUX-131 exactly the work that never reached the branch.
echo "phase-1 implementation" > "$SPAWN_WT/impl-phase1.txt"
answer_request "$SPAWN_ROLE" "implemented phase 1"

if wait_and_answer build g-build; then
  ok "build dispatched after the harvest"
else
  bad "build never dispatched after iteration 1"
fi
grep -q '"output":"ported ' "$BD/graphs/$RID_S/nodes/implement.json" 2>/dev/null \
  && ok "implement node recorded the port" \
  || bad "implement did not record a port: $(cat "$BD/graphs/$RID_S/nodes/implement.json" 2>/dev/null | head -c 300)"
[ "$(cat "$REPO/impl-phase1.txt" 2>/dev/null)" = "phase-1 implementation" ] \
  && ok "build sees the ported file on the branch (Defect A pinned)" \
  || bad "ported file missing from checkout at build time — spawn output stranded"
git -C "$REPO" status --porcelain | grep -q '?? impl-phase1.txt' \
  && ok "port landed uncommitted (working tree only)" \
  || bad "ported file not uncommitted: $(git -C "$REPO" status --porcelain | head -3)"
[ "$(git -C "$REPO" rev-parse HEAD)" = "$BASE_SHA" ] \
  && ok "HEAD unchanged by the port — the daemon created no commit" \
  || bad "HEAD moved during port — daemon-side committing reintroduced"
[ "$(cat "$SPAWN_WT/impl-phase1.txt" 2>/dev/null)" = "phase-1 implementation" ] \
  && ok "durability: worktree keeps its copy while the port is uncommitted" \
  || bad "worktree copy discarded while the port is uncommitted"

wait_and_answer test g-test || bad "spawn run: test never dispatched"
wait_and_answer review g-review || bad "spawn run: review never dispatched"
complete_current_phase
wait_and_answer plan g-verify || bad "spawn run: update-spec never dispatched"
wait_node_state "$RID_S" phase-gate waiting || bad "spawn run: gate never waited"
"$MUX" graph approve "$RID_S" phase-gate >/dev/null 2>&1 || bad "spawn run: approve failed"
if wait_request commit g-commit; then
  # The gated commit ships EXACTLY the ported file. Committing more (the
  # dirty fixture spec) would leave the tip a superset of the worktree,
  # the advance-at-reseed containment check would refuse, and iteration 2
  # would re-port content the tip already holds.
  git -C "$REPO" add impl-phase1.txt
  git -C "$REPO" commit -q -m "ship phase 1"
  answer_request commit "committed"
else
  bad "spawn run: phase-1 commit never dispatched"
fi
[ "$(git -C "$REPO" rev-parse HEAD)" != "$BASE_SHA" ] \
  && ok "gated commit shipped the ported file (script-side, behind the gate)" \
  || bad "phase-1 commit did not land"

if wait_request "$SPAWN_ROLE" spawn-task "$SEED1"; then
  ok "iteration 2 reseeded into the SAME worker"
else
  bad "iteration-2 seed never arrived — worker not reused"
fi
[ "$(run_spawn_count "$RID_S")" = "1" ] \
  && ok "one worker across iterations (spawn count from the store == 1)" \
  || bad "spawn store shows $(run_spawn_count "$RID_S") workers mid-run — worker churn (Defect B)"
if [ -z "$(git -C "$SPAWN_WT" status --porcelain 2>/dev/null)" ] \
  && [ "$(git -C "$SPAWN_WT" rev-parse HEAD 2>/dev/null)" = "$(git -C "$REPO" rev-parse HEAD)" ]; then
  ok "worktree advanced to the shipped tip at reseed"
else
  bad "worktree not advanced after the commit: head=$(git -C "$SPAWN_WT" rev-parse HEAD 2>/dev/null) dirty=$(git -C "$SPAWN_WT" status --porcelain 2>/dev/null | head -2)"
fi
# Iteration 2 is a verify-only pass: the worker writes NOTHING.
answer_request "$SPAWN_ROLE" "phase already covered — nothing to implement"

if wait_and_answer build g-build; then
  ok "no-op spawn iteration completed and dispatched build"
else
  bad "no-op spawn iteration stalled — nothing-to-port became a failure"
fi
grep -q '"output":"nothing to port"' "$BD/graphs/$RID_S/nodes/implement.json" 2>/dev/null \
  && ok "no-op iteration recorded 'nothing to port'" \
  || bad "no-op output wrong: $(cat "$BD/graphs/$RID_S/nodes/implement.json" 2>/dev/null | head -c 300)"

wait_and_answer test g-test || bad "spawn run: phase-2 test never dispatched"
wait_and_answer review g-review || bad "spawn run: phase-2 review never dispatched"
complete_current_phase
wait_and_answer plan g-verify || bad "spawn run: phase-2 update-spec never dispatched"
wait_node_state "$RID_S" phase-gate waiting || bad "spawn run: phase-2 gate never waited"
"$MUX" graph approve "$RID_S" phase-gate >/dev/null 2>&1 || bad "spawn run: phase-2 approve failed"
wait_and_answer commit g-commit || bad "spawn run: phase-2 commit never dispatched"
wait_node_state "$RID_S" final-gate waiting || bad "spawn run: final gate never waited"
"$MUX" graph approve "$RID_S" final-gate >/dev/null 2>&1 || bad "spawn run: final approve failed"
wait_and_answer commit g-commit || bad "spawn run: push-pr never dispatched"

done_ok=0
for i in $(seq 1 40); do
  [ "$(run_state "$RID_S")" = "complete" ] && done_ok=1 && break
  sleep 0.5
done
[ "$done_ok" -eq 1 ] && ok "spawn-backed run walked both phases to complete" \
  || bad "spawn run state: $(run_state "$RID_S")"
[ "$(run_spawn_count "$RID_S")" = "1" ] \
  && ok "multi-phase run created ONE implement worker total (Defect B end-to-end)" \
  || bad "run created $(run_spawn_count "$RID_S") workers — one-per-iteration churn is back"

# --- 8. Stranded output fails LOUDLY at the spawn node, before build -------
# The original incident: implement reported success with its work stranded
# in the worktree, and four downstream nodes plus a human gate burned
# before the phase-progress guard noticed at commit. A refused port must
# fail the spawn node itself, with neither side auto-resolved.
write_spawn_spec " " " "
RID_B="$("$MUX" graph run --file "$WORK/spawnphase.json" "stranded output control" 2>&1 | grep -o 'Started run [^ ]*' | awk '{print $3}')"
[ -n "$RID_B" ] && ok "conflict-control run started: $RID_B" || bad "conflict-control run failed to start"

if spawn_for_run "$RID_B" && [ -n "$SPAWN_WT" ]; then
  ok "conflict-control worker created with a worktree"
else
  bad "conflict-control worker or worktree never appeared"
fi
if wait_request "$SPAWN_ROLE" spawn-task; then
  ok "conflict-control task seeded"
else
  bad "conflict-control seed never arrived"
fi

# A human edit lands in the checkout at the same path while the worker
# works — the port must refuse rather than overwrite either side.
echo "human edit in progress" > "$REPO/impl-conflict.txt"
echo "worker version" > "$SPAWN_WT/impl-conflict.txt"
answer_request "$SPAWN_ROLE" "implemented into a conflicted path"

if wait_node_state "$RID_B" implement failed; then
  ok "stranded output failed the spawn node itself"
else
  bad "implement did not fail on the refused port: $(node_state "$RID_B" implement)"
fi
grep -q 'impl-conflict.txt' "$BD/graphs/$RID_B/nodes/implement.json" 2>/dev/null \
  && ok "refusal names the conflicting path" \
  || bad "failure detail does not name the path: $(cat "$BD/graphs/$RID_B/nodes/implement.json" 2>/dev/null | head -c 300)"
sleep 2
breq="$(AGENT_ROLE=build "$MUX" inbox --peek 2>/dev/null | grep -c 'Type: request' || true)"
[ "${breq:-0}" -eq 0 ] && ok "build never dispatched — failure landed before build, not at commit" \
  || bad "build dispatched despite stranded output"
for i in $(seq 1 20); do
  [ "$(run_state "$RID_B")" = "failed" ] && break
  sleep 0.5
done
[ "$(run_state "$RID_B")" = "failed" ] && ok "conflict-control run failed loudly" \
  || bad "conflict-control run state: $(run_state "$RID_B")"
[ "$(cat "$REPO/impl-conflict.txt" 2>/dev/null)" = "human edit in progress" ] \
  && ok "human's checkout edit preserved byte-identical" \
  || bad "checkout edit clobbered: $(cat "$REPO/impl-conflict.txt" 2>/dev/null)"
[ "$(cat "$SPAWN_WT/impl-conflict.txt" 2>/dev/null)" = "worker version" ] \
  && ok "worker's copy preserved in its worktree after the refusal" \
  || bad "worktree copy lost after the refused port"

# --- Coverage floor --------------------------------------------------------
# A clean full pass emits exactly 46 checks: 4 validation + daemon +
# headline start + 3 per-phase implement targets + 3 ordered commits +
# 4 termination/push + start-at-2 + 2 stuck-phase, plus the MUX-131
# spawn sections: 1 fixture + 17 spawn-run + 9 conflict-control. The
# floor guards against a short-circuited run, not an imagined larger
# count (run 4/5 postmortem: a floor of 25 failed every genuinely
# complete run).
total=$((pass + fail))
if [ "$total" -ge 46 ]; then
  ok "coverage floor met ($total checks executed)"
else
  bad "coverage floor NOT met — only $total checks executed, want >= 46 (a skipped run must not report green)"
fi

# --- Summary ---------------------------------------------------------------
echo ""
echo "  ${pass} passed, ${fail} failed"
[ "$fail" -eq 0 ] || exit 1
exit 0
