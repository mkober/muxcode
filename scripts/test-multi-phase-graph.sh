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
# spec-to-pr's exact shape with SEND nodes in place of its two spawns —
# StartSpawn launches real agent windows and AI CLIs, which a hermetic
# script must not do (same deviation as test-graph-orchestrator.sh). The
# builtin's own shape is pinned by TestReqCodePRMultiPhaseLoop, and this
# script validates the real builtin live.
#
# Sections 6-8 (MUX-131) lift that deviation for the implement node only:
# Defect A lives in the spawn dispatch/harvest path itself, so a send-node
# stand-in cannot reach it. Hermeticity holds because the scratch session's
# environment points the spawn role's CLI at a local stub that prints an
# idle `❯` prompt and blocks — no AI CLI and no network, while the pane
# stays alive for the worker-reuse checks — and this script plays the worker
# over the bus. Graph workers take no worktree (2026-09-03, MUX-142 § "The
# third tree"): the worker writes straight into the session checkout. Covered
# end to end: the worker's output present and UNCOMMITTED in the checkout
# when build dispatches, shipped by the gated phase commit, one worker reused
# across phases (spawn count from the store), and the no-op iteration.
# Section 8 (MUX-178) pins that a worker recorded completed with no seed
# parks the spawn node unknown instead of crediting success; it replaces the
# MUX-131 clobber-conflict control, which needs a worktree to conflict with.
#
# Section 9 (MUX-131 Phase 5) extends the SAME harness rather than
# forking a second script: a replacement control kills the worker between
# iterations to prove the retention observable (the worker window's pane
# PIDs) distinguishes a reused worker from a replaced one — without that
# control, the section-7 retention pin could pass vacuously. (The former
# section 9 walked story-lifecycle, the other spawn builtin; that template
# was removed 2026-09-02 as a duplicate of spec-to-pr's arc.)
#
# ISOLATION: scratch BUS_SESSION, scratch repo dir via
# MUXCODE_SESSION_REPO_DIR, lifecycle log in a temp dir, empty config.
#
# REQUIRES: installed muxcode >= v0.1.0, which shipped MUX-121 and the
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
. "$(dirname "${BASH_SOURCE[0]}")/lib/muxcode-version.sh"
require_muxcode_version "$MUX" v0.1.0 MUX-121 || { echo "  FAIL  binary precondition not met"; exit 1; }

GREEN=$'\033[0;32m'; RED=$'\033[0;31m'; NC=$'\033[0m'
pass=0; fail=0
ok()  { echo "  ${GREEN}PASS${NC}  $*"; pass=$((pass + 1)); }
bad() { echo "  ${RED}FAIL${NC}  $*"; fail=$((fail + 1)); dump_diag; }

# Releasing a gate needs an authorized actor that did not create the run
# (MUX-144). This stands in for the human at the CLI, under an identity no agent
# can hold, so the self-approval rule cannot collide with whoever runs the script.
# The opt-in itself goes into a scratch HOME below, not this environment: the
# authority is read from the config file, and the daemon seals it at startup.
approve_gate() { AGENT_ROLE=test-approver "$MUX" graph approve "$@"; }

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
  for rid in ${RID:-} ${RID2:-} ${RID3:-} ${RID_S:-} ${RID_U:-} ${RID_L:-} ${RID_R:-}; do
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
export HOME="$WORK/home"
mkdir -p "$HOME/.config/muxcode"
echo "MUXCODE_GATE_AUTHORITY_ROLES=test-approver" > "$HOME/.config/muxcode/config"
# The fixture repo is a REAL git repo: the phase predicate reads HEAD's copy
# of the spec, and the spawn sections assert what the gated commit ships.
git -C "$REPO" init -q
git -C "$REPO" config user.email "muxcode-test@example.invalid"
git -C "$REPO" config user.name "muxcode-test"
echo "fixture" > "$REPO/README.md"
git -C "$REPO" add README.md
git -C "$REPO" commit -q -m "fixture base"
export MUXCODE_LIFECYCLE_LOG_DIR="$WORK/lifecycle"
: > "$WORK/empty-config"
export MUXCODE_CONFIG="$WORK/empty-config"
export MUXCODE_TMP_CLEANUP_THRESHOLD=0
export MUXCODE_BRANCH_TIME_DISABLE=1
export MUXCODE_DEDUP_WINDOW=0
export MUXCODE_SESSION_REPO_DIR="$REPO"

DPID=""
# cleanup tears down the scratch session and bus dir. Set
# MUXCODE_TEST_KEEP=1 to preserve $WORK instead: a failing spawn section is
# undiagnosable once its lifecycle log and spawn store are deleted.
cleanup() {
  [ -n "$DPID" ] && kill "$DPID" 2>/dev/null
  tmux kill-session -t "$BUS_SESSION" 2>/dev/null
  [ "${MUXCODE_TEST_KEEP:-}" = "1" ] && { echo "  (kept for diagnosis: $WORK)"; return; }
  rm -rf "$BD" "$WORK"
}
trap cleanup EXIT

tmux new-session -d -s "$BUS_SESSION" -n edit -x 120 -y 30
# Session env reaches every pane the daemon later creates (spawn windows).
# The spawn's `agent launch edit` must boot no real AI CLI, and must not
# leave a bare shell either: captureInjectionTarget (MUX-164) refuses to
# type into a pane whose last line ends in `$`, `%`, `->` or a short `>`.
# The earlier fixture named a binary that cannot exist, so `agent launch`
# died and left the worker pane at a shell prompt — every worker seed was
# then refused with `injection-refused`, the worker never ran, and the
# harvest reported "nothing to port" while 18 spawn checks failed. This
# stub stands in as an idle agent instead: it shows the `❯` the guard
# requires and blocks, keeping the pane alive for the worker-reuse checks.
# The script itself plays the worker over the bus, so whatever the stub
# reads is inert.
cat > "$WORK/stub-agent" <<'STUB'
#!/usr/bin/env bash
while :; do
  printf '\n❯ '
  read -r _ || sleep 5
done
STUB
chmod +x "$WORK/stub-agent"
tmux set-environment -t "$BUS_SESSION" BUS_SESSION "$BUS_SESSION"
tmux set-environment -t "$BUS_SESSION" MUXCODE_CONFIG "$WORK/empty-config"
tmux set-environment -t "$BUS_SESSION" MUXCODE_EDIT_CLI "$WORK/stub-agent"
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
# worker (write checkout files) or the commit agent (git commit) BETWEEN
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
#
# The EXIT=0 sentinel is not decoration, it is the verdict. deriveSendOutcome
# accepts exactly three: a response with action "error", an authoritative
# history row for the role newer than dispatch, or an EXIT=<n> sentinel in the
# payload. These fake agents run no hooks, so they write no authoritative row —
# leaving the sentinel as the only verdict available to them. Without it every
# send node finishes outcome=unknown, and an unknown outcome with no "unknown"
# edge parks the node on an unverified hold: the run stalls at the FIRST send
# node with no failure and nothing to approve. That is why this script never
# recorded a clean run.
#
# Appended rather than replacing $2 so a caller's own text survives, and last
# in the payload because parseExitSentinel takes the LAST match (MUX-154).
answer_request() {
  AGENT_ROLE="$1" "$MUX" inbox >/dev/null 2>&1
  AGENT_ROLE="$1" "$MUX" send edit response "${2:-done} EXIT=0" --type response --reply-to "$REQ_ID" >/dev/null 2>&1
}

# wait_and_answer <role> <action> — wait_request + immediate answer, for
# hops with no side effect between arrival and reply.
wait_and_answer() {
  wait_request "$1" "$2" || return 1
  answer_request "$1"
}

# spawn_for_run <run-id> — poll the spawn store for the run's worker,
# setting SPAWN_ROLE, SPAWN_WIN and SPAWN_LINE (the raw store entry) from
# the newest matching entry. Field extraction uses grep -o, not sed
# substitution, which passes the whole line through on no-match.
SPAWN_ROLE=""
SPAWN_WIN=""
SPAWN_LINE=""
spawn_for_run() {
  local rid="$1" i line
  for i in $(seq 1 60); do
    line="$(grep "\"run_id\":\"$rid\"" "$BD/spawn.jsonl" 2>/dev/null | tail -1)"
    if [ -n "$line" ]; then
      SPAWN_LINE="$line"
      SPAWN_ROLE="$(printf '%s' "$line" | grep -o '"spawn_role":"[^"]*"' | cut -d'"' -f4)"
      SPAWN_WIN="$(printf '%s' "$line" | grep -o '"window":"[^"]*"' | cut -d'"' -f4)"
      [ -n "$SPAWN_ROLE" ] && return 0
    fi
    sleep 0.5
  done
  return 1
}

# worker_pane_pids <window> — the worker's own pane PIDs, sorted. The
# retention observable (MUX-131 Phase 5): a reused worker keeps its shell
# processes across a reseed, so the PIDs are stable; ANY replacement — a
# fresh window or a respawned pane — changes them. Empty when the window
# is gone. The control pane (`muxcode graph ui`, MUX-108) is excluded:
# EnsureControlPane adds it asynchronously, so a window can gain it between
# two captures — read as a replacement, failing the retention pin three
# runs in a row (2026-10-04) while both worker PIDs were unchanged.
worker_pane_pids() {
  tmux list-panes -t "$BUS_SESSION:$1" -F '#{pane_pid} #{pane_start_command}' 2>/dev/null \
    | grep -v '^[0-9]* ["'\'']*muxcode graph ui' | awk '{print $1}' | sort | tr '\n' ' '
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
  git -C "$REPO" add docs/requirements/drafts/fixture-spec.md
  git -C "$REPO" commit -q --allow-empty -m "fixture spec baseline"
  (cd "$REPO" && "$MUX" spec set docs/requirements/drafts/fixture-spec.md >/dev/null 2>&1)
}

# commit_spec — what the real commit agent does behind a phase gate: the
# phase's spec update lands at HEAD. The phase predicate reads HEAD's copy
# (MUX-183), so a fake commit that shipped nothing would leave every earlier
# phase uncommitted and each lap would name Phase 1 again.
commit_spec() {
  git -C "$REPO" add docs/requirements/drafts/fixture-spec.md
  git -C "$REPO" commit -q --allow-empty -m "ship $1"
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
   {"id": "phase-check", "type": "condition", "conditions": {"spec_phase_committable": "commit"}},
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
   {"from": "update-spec", "to": "phase-check"},
   {"from": "phase-check", "to": "phase-gate"},
   {"from": "phase-check", "to": "stuck-gate", "outcome": "failure"},
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

"$MUX" graph validate 50-spec-to-pr >/dev/null 2>&1 \
  && ok "real 50-spec-to-pr builtin validates" \
  || bad "50-spec-to-pr builtin failed validation"

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
# CWD = fixture repo, so nothing the daemon resolves from its working
# directory can reach this checkout instead.
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
  approve_gate "$RID" phase-gate >/dev/null 2>&1 || bad "phase $phase: approve failed"
  if wait_request commit g-commit; then
    commit_spec "phase $phase"
    answer_request commit "committed"
  else
    bad "phase $phase: commit never dispatched"
  fi
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

approve_gate "$RID" final-gate >/dev/null 2>&1 || bad "final-gate approve failed"
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

# --- 5. Incomplete phase: routed to the stuck gate WITHOUT asking a human --
# MUX-167. Before the phase-check condition an open phase still reached
# phase-gate, a human approved the commit, and the phase-progress guard
# declined it a second later — four such approvals on run 1788966148.
# Now phase-check routes an open phase straight to stuck-gate, so a human
# is asked to approve a commit only when the guard will accept it.
write_spec " " " " " "
RID3="$("$MUX" graph run --file "$WORK/multiphase.json" 2>&1 | grep -o 'Started run [^ ]*' | awk '{print $3}')"
wait_and_answer edit g-edit; wait_and_answer build g-build; wait_and_answer test g-test; wait_and_answer review g-review
wait_and_answer plan g-verify   # answered WITHOUT checking anything off
if wait_node_state "$RID3" stuck-gate waiting; then
  ok "open phase routed straight to the stuck gate (phase-check)"
else
  bad "stuck-gate never armed: phase-check=$(node_state "$RID3" phase-check) phase-gate=$(node_state "$RID3" phase-gate)"
fi
# The discriminating assertion. A run that REACHED phase-gate reproduces the
# defect even if it later declined, so this checks the gate never armed at
# all: neither marker file exists (.pending while blocking, .approved once
# released), and the node never entered waiting.
pg_state="$(node_state "$RID3" phase-gate)"
if [ "$pg_state" != "waiting" ] \
  && [ ! -e "$BD/graphs/$RID3/approvals/phase-gate.pending" ] \
  && [ ! -e "$BD/graphs/$RID3/approvals/phase-gate.approved" ]; then
  ok "no human was asked to approve the withheld commit (phase-gate=${pg_state:-unvisited}, no marker)"
else
  bad "phase-gate armed for an open phase (state=$pg_state) — the pre-MUX-167 ask-then-decline"
fi
commit_reqs="$(AGENT_ROLE=commit "$MUX" inbox --peek 2>/dev/null | grep -c 'Type: request' || true)"
[ "$commit_reqs" -eq 0 ] && ok "withheld commit never reached the commit role" \
  || bad "commit dispatched despite incomplete phase"
"$MUX" graph cancel "$RID3" >/dev/null 2>&1

# --- 5b. Positive control: the SAME fixture with the phase CLOSED ----------
# Without this pair, section 5 passes on a graph that could never reach
# phase-gate under any conditions.
write_spec " " " " " "
RID3B="$("$MUX" graph run --file "$WORK/multiphase.json" 2>&1 | grep -o 'Started run [^ ]*' | awk '{print $3}')"
wait_and_answer edit g-edit; wait_and_answer build g-build; wait_and_answer test g-test; wait_and_answer review g-review
complete_current_phase          # close the phase BEFORE plan answers
wait_and_answer plan g-verify
if wait_node_state "$RID3B" phase-gate waiting; then
  ok "positive control: a closed phase still reaches phase-gate"
else
  bad "closed phase never reached phase-gate: phase-check=$(node_state "$RID3B" phase-check) stuck=$(node_state "$RID3B" stuck-gate)"
fi

# --- 5c. Backstop: the guard still declines if the spec re-opens ----------
# phase-check is an EARLIER ask, not a replacement for the phase-progress
# guard. Re-opening the spec after the condition passed must still be
# caught at the commit, or the fix has traded one hole for another.
perl -i -pe 's/- \[x\]/- [ ]/ && ($u=1) unless $u' "$SPEC_FILE"
approve_gate "$RID3B" phase-gate >/dev/null 2>&1
if wait_node_state "$RID3B" stuck-gate waiting; then
  ok "negative control: spec re-opened after the gate — phase-progress guard still declined"
else
  bad "guard backstop never fired after the spec re-opened: commit=$(node_state "$RID3B" commit)"
fi
"$MUX" graph cancel "$RID3B" >/dev/null 2>&1

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

if spawn_for_run "$RID_S" && ! printf '%s' "$SPAWN_LINE" | grep -q '"worktree":"[^"]'; then
  ok "worker created in the session checkout, no worktree ($SPAWN_ROLE)"
else
  bad "spawn worker never appeared, or was cut a worktree: ${SPAWN_LINE:-<none>}"
fi
if wait_request "$SPAWN_ROLE" spawn-task; then
  ok "iteration-1 task seeded to the worker"
else
  bad "iteration-1 seed never arrived in $SPAWN_ROLE inbox"
fi
SEED1="$REQ_ID"
PIDS_IT1="$(worker_pane_pids "$SPAWN_WIN")"

# The worker writes its phase-1 output straight into the session checkout —
# the tree build, test, review and commit all use.
echo "phase-1 implementation" > "$REPO/impl-phase1.txt"
answer_request "$SPAWN_ROLE" "implemented phase 1"

if wait_and_answer build g-build; then
  ok "build dispatched after the worker answered"
else
  bad "build never dispatched after iteration 1"
fi
grep -q '"output":"[^"]*no worktree (session checkout)' "$BD/graphs/$RID_S/nodes/implement.json" 2>/dev/null \
  && ok "implement recorded the session-checkout harvest" \
  || bad "implement output does not name the session checkout: $(cat "$BD/graphs/$RID_S/nodes/implement.json" 2>/dev/null | head -c 300)"
[ "$(cat "$REPO/impl-phase1.txt" 2>/dev/null)" = "phase-1 implementation" ] \
  && ok "build sees the worker's file in the checkout" \
  || bad "worker output missing from the checkout at build time"
git -C "$REPO" status --porcelain | grep -q '?? impl-phase1.txt' \
  && ok "worker output uncommitted at build time (working tree only)" \
  || bad "worker output not uncommitted: $(git -C "$REPO" status --porcelain | head -3)"
[ "$(git -C "$REPO" rev-parse HEAD)" = "$BASE_SHA" ] \
  && ok "HEAD unchanged before the gate — the daemon created no commit" \
  || bad "HEAD moved before the gate — daemon-side committing reintroduced"

wait_and_answer test g-test || bad "spawn run: test never dispatched"
wait_and_answer review g-review || bad "spawn run: review never dispatched"
complete_current_phase
wait_and_answer plan g-verify || bad "spawn run: update-spec never dispatched"
wait_node_state "$RID_S" phase-gate waiting || bad "spawn run: gate never waited"
approve_gate "$RID_S" phase-gate >/dev/null 2>&1 || bad "spawn run: approve failed"
if wait_request commit g-commit; then
  git -C "$REPO" add impl-phase1.txt
  git -C "$REPO" commit -q -m "ship phase 1"
  answer_request commit "committed"
else
  bad "spawn run: phase-1 commit never dispatched"
fi
if [ "$(git -C "$REPO" rev-parse HEAD)" != "$BASE_SHA" ] \
  && git -C "$REPO" show --name-only --format= HEAD | grep -qx 'impl-phase1.txt'; then
  ok "gated phase-1 commit shipped the worker's file"
else
  bad "phase-1 commit did not land the worker's file: $(git -C "$REPO" show --stat --format=%s HEAD | head -3)"
fi

if wait_request "$SPAWN_ROLE" spawn-task "$SEED1"; then
  ok "iteration 2 reseeded into the SAME worker"
else
  bad "iteration-2 seed never arrived — worker not reused"
fi
# Retention pin (MUX-131 Phase 5): reuse is only worth having if the
# process survives — a silent per-iteration restart would keep the spawn
# count at 1 while re-paying boot and losing the conversation. Section 9
# proves this observable changes when a worker really is replaced.
PIDS_IT2="$(worker_pane_pids "$SPAWN_WIN")"
if [ -n "$PIDS_IT1" ] && [ "$PIDS_IT1" = "$PIDS_IT2" ]; then
  ok "worker process retained across iterations — conversation kept, no re-boot"
else
  bad "worker process changed across iterations ('$PIDS_IT1' vs '$PIDS_IT2') — reuse without retention"
fi
[ "$(run_spawn_count "$RID_S")" = "1" ] \
  && ok "one worker across iterations (spawn count from the store == 1)" \
  || bad "spawn store shows $(run_spawn_count "$RID_S") workers mid-run — worker churn (Defect B)"
# Iteration 2 is a verify-only pass: the worker writes NOTHING.
answer_request "$SPAWN_ROLE" "phase already covered — nothing to implement"

if wait_and_answer build g-build; then
  ok "no-op spawn iteration completed and dispatched build"
else
  bad "no-op spawn iteration stalled — nothing-to-port became a failure"
fi
grep -q '"output":"[^"]*no worktree (session checkout) — nothing to port"' "$BD/graphs/$RID_S/nodes/implement.json" 2>/dev/null \
  && ok "no-op iteration recorded the session-checkout summary" \
  || bad "no-op output wrong: $(cat "$BD/graphs/$RID_S/nodes/implement.json" 2>/dev/null | head -c 300)"

wait_and_answer test g-test || bad "spawn run: phase-2 test never dispatched"
wait_and_answer review g-review || bad "spawn run: phase-2 review never dispatched"
complete_current_phase
wait_and_answer plan g-verify || bad "spawn run: phase-2 update-spec never dispatched"
wait_node_state "$RID_S" phase-gate waiting || bad "spawn run: phase-2 gate never waited"
approve_gate "$RID_S" phase-gate >/dev/null 2>&1 || bad "spawn run: phase-2 approve failed"
wait_and_answer commit g-commit || bad "spawn run: phase-2 commit never dispatched"
wait_node_state "$RID_S" final-gate waiting || bad "spawn run: final gate never waited"
approve_gate "$RID_S" final-gate >/dev/null 2>&1 || bad "spawn run: final approve failed"
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

# --- 8. A worker completed with no seed parks unknown (MUX-178) ------------
# On 2026-09-11 implement recorded success two seconds after start for a
# worker that never answered, and build dispatched on a checkout that had
# received nothing. replaceLostWorkers catches a seeded worker that ends
# unanswered (section 9); what it cannot see is an entry recorded completed
# with NO seed. No live road produces that on demand, so the store entry is
# rewritten to that shape — it is only rewritten on a running entry's state
# change, and this worker stays running and unanswered until the edit.
# Replaces the MUX-131 clobber-conflict control: with no worktree there is
# no port to refuse.
write_spawn_spec " " " "
RID_U="$("$MUX" graph run --file "$WORK/spawnphase.json" "unanswered seed control" 2>&1 | grep -o 'Started run [^ ]*' | awk '{print $3}')"
[ -n "$RID_U" ] && ok "unanswered-seed run started: $RID_U" || bad "unanswered-seed run failed to start"

spawn_for_run "$RID_U" || bad "unanswered-seed worker never appeared"
if wait_request "$SPAWN_ROLE" spawn-task; then
  ok "unanswered-seed worker seeded"
else
  bad "unanswered-seed seed never arrived"
fi

perl -i -pe 'if (/"spawn_role":"\Q'"$SPAWN_ROLE"'\E"/) { s/"status":"running"/"status":"completed"/; s/,?"seed_msg_id":"[^"]*"//; }' "$BD/spawn.jsonl"
SPAWN_LINE="$(grep "\"spawn_role\":\"$SPAWN_ROLE\"" "$BD/spawn.jsonl" | tail -1)"
if printf '%s' "$SPAWN_LINE" | grep -q '"status":"completed"' \
  && ! printf '%s' "$SPAWN_LINE" | grep -q '"seed_msg_id"'; then
  ok "store entry rewritten to completed with no seed"
else
  bad "store entry not in the no-seed shape: $SPAWN_LINE"
fi

held=0
for i in $(seq 1 80); do
  grep -q '"outcome":"unknown"' "$BD/graphs/$RID_U/nodes/implement.json" 2>/dev/null && held=1 && break
  sleep 0.5
done
[ "$held" -eq 1 ] && ok "no-seed worker parked implement unknown — never credited success" \
  || bad "implement not unknown: $(cat "$BD/graphs/$RID_U/nodes/implement.json" 2>/dev/null | head -c 300)"
grep -q "ended without answering its seed: $SPAWN_ROLE (no seed recorded)" "$BD/graphs/$RID_U/nodes/implement.json" 2>/dev/null \
  && ok "hold output names the worker and the missing seed" \
  || bad "hold output gives no reason: $(cat "$BD/graphs/$RID_U/nodes/implement.json" 2>/dev/null | head -c 300)"
grep -rq "graph-spawn-unanswered.*$SPAWN_ROLE" "$WORK/lifecycle" 2>/dev/null \
  && ok "lifecycle row graph-spawn-unanswered names the worker" \
  || bad "no graph-spawn-unanswered lifecycle row for $SPAWN_ROLE"
sleep 2
breq="$(AGENT_ROLE=build "$MUX" inbox --peek 2>/dev/null | grep -c 'Type: request' || true)"
[ "${breq:-0}" -eq 0 ] && ok "build never dispatched behind the held node" \
  || bad "build dispatched despite an unanswered worker"
"$MUX" graph cancel "$RID_U" >/dev/null 2>&1

# --- 9. Replacement control: a dead worker must not read as retained -------
# The vacuity control for section 7's retention pin: kill the worker
# between iterations, so re-entry falls back to a fresh start. The same
# pane-PID observable must now read DIFFERENT — an observable that reads
# equal for a genuinely replaced worker would make the retention pin pass
# vacuously forever.
write_spawn_spec " " " "
RID_R="$("$MUX" graph run --file "$WORK/spawnphase.json" "replacement control" 2>&1 | grep -o 'Started run [^ ]*' | awk '{print $3}')"
[ -n "$RID_R" ] && ok "replacement-control run started: $RID_R" || bad "replacement-control run failed to start"

spawn_for_run "$RID_R" || bad "replacement-control worker never appeared"
WIN_R1="$SPAWN_WIN"; ROLE_R1="$SPAWN_ROLE"
if wait_request "$ROLE_R1" spawn-task; then
  PIDS_R1="$(worker_pane_pids "$WIN_R1")"
  ok "replacement-control worker seeded (pane pids captured)"
else
  bad "replacement-control seed never arrived"
fi
# Iteration 1 is a verify-only pass; the chain walks to its gated commit.
answer_request "$ROLE_R1" "phase already covered — nothing to implement"
wait_and_answer build g-build || bad "replacement-control: build never dispatched"
wait_and_answer test g-test || bad "replacement-control: test never dispatched"
wait_and_answer review g-review || bad "replacement-control: review never dispatched"
complete_current_phase
wait_and_answer plan g-verify || bad "replacement-control: update-spec never dispatched"
wait_node_state "$RID_R" phase-gate waiting || bad "replacement-control: gate never waited"
approve_gate "$RID_R" phase-gate >/dev/null 2>&1 || bad "replacement-control: approve failed"
wait_request commit g-commit || bad "replacement-control: commit never dispatched"
# Kill the worker BEFORE the commit answer releases the loop back into
# implement: re-entry then deterministically finds a dead worker.
tmux kill-window -t "$BUS_SESSION:$WIN_R1" 2>/dev/null
answer_request commit "committed"

fresh=0
for i in $(seq 1 60); do
  [ "$(run_spawn_count "$RID_R")" = "2" ] && fresh=1 && break
  sleep 0.5
done
spawn_for_run "$RID_R"   # newest store entry = the fresh worker
if [ "$fresh" -eq 1 ] && [ "$SPAWN_WIN" != "$WIN_R1" ] && wait_request "$SPAWN_ROLE" spawn-task; then
  ok "dead worker fell back to a fresh start — iteration-2 seed reached a new worker"
else
  bad "no fresh worker after the kill: count=$(run_spawn_count "$RID_R") win=$SPAWN_WIN"
fi
[ "$(run_spawn_count "$RID_R")" = "2" ] \
  && ok "replacement is real: the store shows two workers for the run" \
  || bad "spawn store shows $(run_spawn_count "$RID_R") workers — replacement did not happen"
PIDS_R2="$(worker_pane_pids "$SPAWN_WIN")"
if [ -n "$PIDS_R1" ] && [ -n "$PIDS_R2" ] && [ "$PIDS_R1" != "$PIDS_R2" ]; then
  ok "replaced worker does NOT read as retained — the observable distinguishes reuse from replacement"
else
  bad "vacuity: replaced worker pane pids read equal ('$PIDS_R1' vs '$PIDS_R2')"
fi
"$MUX" graph cancel "$RID_R" >/dev/null 2>&1

# --- Coverage floor --------------------------------------------------------
# Floor == max (MUX-131 Phase 5): a clean full pass emits EXACTLY 51
# checks: 4 validation + daemon + headline start + 3 per-phase implement
# targets + 3 ordered commits + 4 termination/push + start-at-2 +
# 5 phase-check routing (3 open-phase + positive control + guard
# backstop, MUX-167) + 1 spawn fixture + 16 spawn-run + 7 unanswered-seed
# (MUX-178) + 5 replacement-control. Equality, not >=: a floor
# below max lets newly added checks silently raise max above the floor,
# and a partially short-circuited run can then still report green. It
# counts checks EXECUTED (pass + fail); a failing run exits 1 regardless
# of this check, so equality only ever gates green runs.
total=$((pass + fail))
if [ "$total" -eq 51 ]; then
  ok "coverage floor met and equals max ($total checks executed)"
else
  bad "coverage floor mismatch — $total checks executed, want exactly 51 (floor == max; a skipped or drifted run must not report green)"
fi

# --- Summary ---------------------------------------------------------------
echo ""
echo "  ${pass} passed, ${fail} failed"
[ "$fail" -eq 0 ] || exit 1
exit 0
