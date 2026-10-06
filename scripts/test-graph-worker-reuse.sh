#!/usr/bin/env bash
# Integration test for MUX-195: one worker per graph run, one per spawning
# agent, a bounded pool, and no stranded workers.
#
# Covers, on a real scratch daemon and real tmux windows:
#   1. A run with two spawn nodes (implement → fix) holds one worker: the fix
#      node reuses it (graph-spawn-reuse), and the finished run releases it to
#      the idle pool (spawn-idle, `spawn status` reads idle).
#   2. A second run adopts that idle worker instead of launching one, and the
#      graph-spawn-adopted row names old and new owner. After adoption the
#      worker answers to the NEW run: the run is the user's, so an agent's
#      `spawn stop` is refused (it would pass under the old, agent-launched
#      run), and the worker's build:build request is refused by
#      CheckGraphNodeAuthority naming the new run (the old run is finished and
#      owns nothing). Negative control: its request to a peer the run does not
#      own still delivers.
#   3. Negative controls on the graph road: a busy worker is not adopted — the
#      second run launches its own worker within the cap; past the cap a third
#      run's node waits ready (spawn-cap-refused, graph-spawn-deferred) and no
#      worker is launched; lowering the cap stops no live worker; once a worker
#      frees, the waiting run adopts it — adoption is never refused by the cap.
#   4. The agent road: two `spawn start`s from one owner use one worker
#      ("Reused your idle spawn"); a start against that worker while busy
#      queues behind it ("Queued on your busy spawn") and every queued task is
#      answered; another owner past the cap is refused (spawn-cap-refused,
#      exit 1) without adopting the busy worker, and within the cap it starts
#      its own ("Started spawn").
#   5. A finished run's worker whose seed delivery record is removed stays
#      idle — the log fallback still reads it as answered — and is reaped when
#      its quiet window closes, never stranded running.
#   6. A lost worker (window killed mid-task) is replaced once, and a registry
#      sampler running throughout never sees two live entries for the run.
#
# ISOLATION: scratch BUS_SESSION, bus dir, HOME, config file, lifecycle log
# and repo dir. Workers run a stub agent that prints the ❯ the injection
# guard needs and answers each spawn-task seed with EXIT=0 — unless
# $CTL/hold exists, which keeps every stub busy (seeds left unanswered). The
# graph road uses base role edit and the agent road base role research, so
# the two idle pools never meet. The cap and idle window are read from the
# scratch config file (env then config), so the script retunes the live
# daemon by rewriting it. No real AI CLI is ever launched.
#
# REQUIRES: installed muxcode built from this tree with MUX-195 Phase 4, tmux
# and jq. Run ./build.sh first — this tests the INSTALLED binary. ~3 minutes.
#
# Usage: bash scripts/test-graph-worker-reuse.sh
#        MUXCODE_TEST_KEEP=1 bash scripts/test-graph-worker-reuse.sh   # keep WORK
set -uo pipefail

MUX="${MUXCODE_BIN:-muxcode}"
MUX="$(command -v "$MUX" 2>/dev/null || echo "$MUX")"
if [ ! -x "$MUX" ]; then
  echo "  FAIL  cannot resolve muxcode binary ('$MUX') — set MUXCODE_BIN"
  exit 1
fi
for tool in tmux jq; do
  command -v "$tool" >/dev/null 2>&1 || { echo "  FAIL  $tool not available"; exit 1; }
done
JQ="$(command -v jq)"
. "$(dirname "${BASH_SOURCE[0]}")/lib/muxcode-version.sh"
require_muxcode_version "$MUX" v0.1.0 MUX-195 || { echo "  FAIL  binary precondition not met"; exit 1; }
if ! grep -aq 'Reused your idle spawn' "$MUX"; then
  echo "  FAIL  installed muxcode predates MUX-195 Phase 4 (no agent-road reuse) — run ./build.sh"
  exit 1
fi

GREEN=$'\033[0;32m'; RED=$'\033[0;31m'; NC=$'\033[0m'
pass=0; fail=0
ok()  { echo "  ${GREEN}PASS${NC}  $*"; pass=$((pass + 1)); }
bad() { echo "  ${RED}FAIL${NC}  $*"; fail=$((fail + 1)); }
check() { local msg="$1"; shift; if "$@"; then ok "$msg"; else bad "$msg"; fi; }

# --- Isolation -------------------------------------------------------------
export BUS_SESSION="worker-reuse-test-$$"
BD="/tmp/muxcode-bus-${BUS_SESSION}"
WORK="/tmp/worker-reuse-work-$$"
REPO="$WORK/repo"
CTL="$WORK/ctl"
mkdir -p "$REPO" "$CTL"
cd "$WORK" || { echo "  FAIL  cannot cd to scratch dir $WORK"; exit 1; }

export HOME="$WORK/home"
mkdir -p "$HOME/.config/muxcode/agents"
: > "$HOME/.config/muxcode/config"
for def in code-editor code-researcher; do
  printf -- '---\ndescription: Fixture worker for test-graph-worker-reuse\n---\nStub.\n' \
    > "$HOME/.config/muxcode/agents/$def.md"
done
export MUXCODE_LIFECYCLE_LOG_DIR="$WORK/lifecycle"
export MUXCODE_CONFIG="$WORK/config"
export MUXCODE_TMP_CLEANUP_THRESHOLD=0
export MUXCODE_BRANCH_TIME_DISABLE=1
export MUXCODE_DEDUP_WINDOW=0
export MUXCODE_SESSION_REPO_DIR="$REPO"
export MUXCODE_STUCK_RELOAD_DISABLE=1
export MUXCODE_PERMBLOCK_WATCHDOG_DISABLE=1
export MUXCODE_FORCE_RESPOND_DISABLE=1
unset MUXCODE_SPAWN_MAX_WORKERS MUXCODE_SPAWN_IDLE_SECS AGENT_ROLE BUS_ROLE
LIFELOG="$MUXCODE_LIFECYCLE_LOG_DIR/${BUS_SESSION}.log"

# set_config <max-workers> <idle-secs> — the daemon re-reads it on every use.
set_config() {
  printf 'MUXCODE_SPAWN_MAX_WORKERS=%s\nMUXCODE_SPAWN_IDLE_SECS=%s\n' "$1" "$2" > "$MUXCODE_CONFIG"
}
set_config 3 600

cat > "$WORK/stub-agent" <<STUB
#!/usr/bin/env bash
w="\$(tmux display-message -p -t "\$TMUX_PANE" '#W' 2>/dev/null || echo unknown)"
echo \$\$ > "$CTL/\$w.pid"
while :; do
  printf '\n❯ '
  if [ ! -e "$CTL/hold" ]; then
    "$MUX" inbox --raw 2>/dev/null \\
      | "$JQ" -rR 'fromjson? | select(.type == "request" and .action == "spawn-task") | "\(.id) \(.from)"' \\
      | while read -r id from; do
          to="\$from"; [ "\$from" = daemon ] && to=edit
          "$MUX" send "\$to" spawn-task "stub \$AGENT_ROLE answered \$id EXIT=0" \\
            --type response --reply-to "\$id" --no-notify >/dev/null 2>&1 \\
            && echo "\$id" >> "$CTL/\$w.answered"
        done
  fi
  read -r -t 1 _ || sleep 0.2
done
STUB
chmod +x "$WORK/stub-agent"
export MUXCODE_EDIT_CLI="$WORK/stub-agent"
export MUXCODE_RESEARCH_CLI="$WORK/stub-agent"

DPID=""; SAMPLER=""
cleanup() {
  [ -n "$SAMPLER" ] && kill "$SAMPLER" 2>/dev/null
  [ -n "$DPID" ] && kill "$DPID" 2>/dev/null
  tmux kill-session -t "$BUS_SESSION" 2>/dev/null
  cd / 2>/dev/null
  if [ "${MUXCODE_TEST_KEEP:-0}" = "1" ]; then
    echo "  (kept for diagnosis: $WORK)"
    rm -rf "$BD"
  else
    rm -rf "$BD" "$WORK"
  fi
}
trap cleanup EXIT

tmux new-session -d -s "$BUS_SESSION" -n edit -x 200 -y 50
for v in BUS_SESSION HOME MUXCODE_CONFIG MUXCODE_LIFECYCLE_LOG_DIR MUXCODE_EDIT_CLI MUXCODE_RESEARCH_CLI \
         MUXCODE_SESSION_REPO_DIR MUXCODE_TMP_CLEANUP_THRESHOLD MUXCODE_BRANCH_TIME_DISABLE MUXCODE_DEDUP_WINDOW; do
  tmux set-environment -t "$BUS_SESSION" "$v" "${!v}"
done
for v in MUXCODE_SPAWN_MAX_WORKERS MUXCODE_SPAWN_IDLE_SECS AGENT_ROLE; do
  tmux set-environment -t "$BUS_SESSION" -r "$v"
done
"$MUX" init >/dev/null 2>&1

# --- Helpers ---------------------------------------------------------------
started_id() { grep -o 'Started run [^ ]*' | awk '{print $3}'; }
as_agent() { local role="$1"; shift; AGENT_ROLE="$role" "$MUX" "$@"; }

# as_user runs muxcode through `tmux run-shell`, whose ancestry holds no agent
# runtime, so the run it creates is really the user's (BusActorVerified walks
# the process tree; see test-cancel-provenance.sh). Prints the output and
# returns the command's exit code.
as_user() {
  local out="$WORK/as-user.out"
  rm -f "$out" "$out.rc"
  tmux run-shell -t "$BUS_SESSION" "cd '$WORK' && env -u AGENT_ROLE -u BUS_ROLE HOME='$HOME' \
MUXCODE_CONFIG='$MUXCODE_CONFIG' MUXCODE_LIFECYCLE_LOG_DIR='$MUXCODE_LIFECYCLE_LOG_DIR' \
BUS_SESSION='$BUS_SESSION' MUXCODE_SESSION_REPO_DIR='$REPO' MUXCODE_DEDUP_WINDOW=0 \
MUXCODE_BRANCH_TIME_DISABLE=1 MUXCODE_TMP_CLEANUP_THRESHOLD=0 '$MUX' $* >'$out' 2>&1; echo \$? >'$out.rc'"
  cat "$out" 2>/dev/null
  return "$(cat "$out.rc" 2>/dev/null || echo 99)"
}

wait_until() {
  local tries="$1"; shift
  local i
  for i in $(seq 1 "$tries"); do
    "$@" && return 0
    sleep 0.5
  done
  return 1
}

hold()    { touch "$CTL/hold"; }
release() { rm -f "$CTL/hold"; }

# The registry is rewritten in place, so a read can catch a torn line:
# fromjson? skips it rather than aborting the stream.
spawn_rows() { "$JQ" -cR 'fromjson? // empty' "$BD/spawn.jsonl" 2>/dev/null; }
workers_of() { spawn_rows | "$JQ" -r --arg r "$1" 'select(.role == $r) | .spawn_role' | wc -l | tr -d ' '; }
field_of() { spawn_rows | "$JQ" -r --arg s "$1" --arg f "$2" 'select(.spawn_role == $s) | .[$f] // ""' | tail -1; }
live_for_run() {
  spawn_rows | "$JQ" -r --arg r "$1" 'select(.run_id == $r and (.status == "running" or .status == "starting")) | .spawn_role' \
    | wc -l | tr -d ' '
}
worker_is() { [ "$(field_of "$1" status)" = "$2" ]; }
owner_is() { [ "$(field_of "$1" owner)" = "$2" ]; }
idle_on_stamp() { [ "$(field_of "$1" idle_since)" = "$2" ] && worker_is "$1" running; }
run_state() { "$JQ" -r '.state // ""' "$BD/graphs/$1/run.json" 2>/dev/null; }
run_is() { [ "$(run_state "$1")" = "$2" ]; }
run_settled() { local s; s="$(run_state "$1")"; [ -n "$s" ] && [ "$s" != running ]; }
node_field() { "$JQ" -r --arg f "$3" '.[$f] // ""' "$BD/graphs/$1/nodes/$2.json" 2>/dev/null; }
node_task() { node_field "$1" "$2" task_id; }
node_task_is() { [ "$(node_task "$1" "$2")" = "$3" ]; }
node_is() { [ "$(node_field "$1" "$2" state)" = "$3" ]; }
node_deferred_on() { node_is "$1" "$2" ready && node_field "$1" "$2" deferred_on | grep -qF -- "$3"; }
node_running_on_new_worker() { # <rid> <node> <not-this-worker>
  local t; t="$(node_task "$1" "$2")"
  node_is "$1" "$2" running && [ -n "$t" ] && [ "$t" != "$3" ]
}
not() { ! "$@"; }

life() { "$JQ" -rR --arg e "$1" 'fromjson? | select(.event == $e) | .detail // ""' "$LIFELOG" 2>/dev/null; }
# has_life <event> <substring>... — some row of the event contains every substring.
has_life() {
  local rows ev="$1"; shift
  rows="$(life "$ev")"
  local s
  for s in "$@"; do rows="$(printf '%s\n' "$rows" | grep -F -- "$s")"; done
  [ -n "$rows" ]
}
count_life() { life "$1" | grep -cF -- "$2"; }

# The bus log, not the stub's own record, is the evidence a task was answered:
# seed_for_task finds the spawn-task request carrying a task's text, and
# worker_replied finds that worker's response correlated to it.
seed_for_task() {
  "$JQ" -rR --arg w "$1" --arg t "$2" \
    'fromjson? | select(.to == $w and .type == "request" and .action == "spawn-task" and (.payload | contains($t))) | .id' \
    "$BD/log.jsonl" 2>/dev/null | tail -1
}
worker_replied() {
  "$JQ" -rR --arg w "$1" --arg s "$2" 'fromjson? | select(.from == $w and .type == "response" and .reply_to == $s) | .id' \
    "$BD/log.jsonl" 2>/dev/null | grep -q .
}
task_replied() { local s; s="$(seed_for_task "$1" "$2")"; [ -n "$s" ] && worker_replied "$1" "$s"; }

windows() { tmux list-windows -t "$BUS_SESSION" -F '#W' 2>/dev/null; }
window_live() { windows | grep -qx "$1"; }
spawn_windows() { windows | grep -c '^spawn-'; }
stub_alive() { local p; p="$(cat "$CTL/$1.pid" 2>/dev/null)"; [ -n "$p" ] && kill -0 "$p" 2>/dev/null; }
answered() { if [ -f "$CTL/$1.answered" ]; then wc -l < "$CTL/$1.answered" | tr -d ' '; else echo 0; fi; }
answered_at_least() { [ "$(answered "$1")" -ge "$2" ]; }
idle_stamped() { [ "$(field_of "$1" idle_since)" != "" ] && [ "$(field_of "$1" idle_since)" != 0 ]; }
shows_idle() { as_agent edit spawn status "$(field_of "$1" id)" 2>/dev/null | grep -Eq 'Status: +idle'; }
start_field() { grep -m1 -E '^(Started|Reused|Queued|Adopted) [a-z ]*spawn: ' "$1" | sed 's/^[^:]*: \([^ ]*\).*/\1/'; }
start_role() { grep -o 'Spawn Role: [^ ]*' "$1" | awk '{print $3}'; }

dump_panes() {
  local p
  for p in $(tmux list-panes -t "$BUS_SESSION:$1" -F '#{pane_id}' 2>/dev/null); do
    echo "    --- pane $p of $1:"
    tmux capture-pane -p -t "$p" -S -15 2>/dev/null | sed $'s/\x1b\\[[0-9;]*[A-Za-z]//g' | grep -v '^$' | sed 's/^/    | /'
  done
}
stub_running_or_dump() { wait_until 60 stub_alive "$1" || { dump_panes "$1"; return 1; }; }

# --- Fixtures --------------------------------------------------------------
cat > "$WORK/two.json" <<'EOF'
{"name": "reuse-two-nodes", "start": "implement",
 "nodes": [{"id": "implement", "type": "spawn", "role": "edit", "message": "implement the fixture"},
           {"id": "fix", "type": "spawn", "role": "edit", "message": "fix the fixture"}],
 "edges": [{"from": "implement", "to": "fix"}]}
EOF
cat > "$WORK/owned.json" <<'EOF'
{"name": "reuse-owned", "start": "implement",
 "nodes": [{"id": "implement", "type": "spawn", "role": "edit", "message": "implement for the user"},
           {"id": "build", "type": "send", "role": "build", "action": "build", "message": "build the fixture", "timeout_secs": 3}],
 "edges": [{"from": "implement", "to": "build"}]}
EOF
cat > "$WORK/one.json" <<'EOF'
{"name": "reuse-one-node", "start": "implement",
 "nodes": [{"id": "implement", "type": "spawn", "role": "edit", "message": "implement one step"}],
 "edges": []}
EOF

# --- Daemon ----------------------------------------------------------------
"$MUX" watch "$BUS_SESSION" --poll 2 >"$WORK/daemon.log" 2>&1 &
DPID=$!
sleep 1
kill -0 "$DPID" 2>/dev/null && ok "scratch daemon running (pid $DPID)" \
  || bad "scratch daemon exited immediately: $(tail -3 "$WORK/daemon.log" 2>/dev/null)"

# --- 1. One run, two spawn nodes, one worker -------------------------------
echo "--- 1. implement and fix share the run's one worker"
R1="$(as_agent auto graph run --file "$WORK/two.json" 2>&1 | started_id)"
check "run R1 started: ${R1:-none}" test -n "$R1"
check "R1 completes" wait_until 120 run_is "$R1" complete
W1="$(node_task "$R1" implement)"
check "R1's implement ran on a worker (${W1:-none})" test -n "$W1"
check "fix ran on the same worker as implement" test "$(node_task "$R1" fix)" = "$W1"
check "one edit worker launched for the whole run (registry holds $(workers_of edit))" test "$(workers_of edit)" -eq 1
check "graph-spawn-reuse row: fix reused $W1 after implement" has_life graph-spawn-reuse "$R1: fix reused worker $W1 (last node implement)"
check "one worker window in the session ($(spawn_windows))" test "$(spawn_windows)" -eq 1
check "finished run released $W1 to the idle pool (spawn-idle names run and node)" \
  wait_until 30 has_life spawn-idle "$W1: idle — released by run $R1 node fix"
check "spawn status reads idle" shows_idle "$W1"

# --- 2. A second run adopts it, and authority follows the new owner --------
echo "--- 2. sequential runs: adoption, and ownership moves with it"
hold
R2="$(as_user graph run --file "$WORK/owned.json" | started_id)"
check "user-launched run R2 started: ${R2:-none}" test -n "$R2"
check "R2 records created_by user (real ancestry)" test "$("$JQ" -r '.created_by // ""' "$BD/graphs/$R2/run.json" 2>/dev/null)" = user
check "graph-spawn-adopted: R2 adopted $W1, old owner R1/fix, new owner R2/implement, context kept" \
  wait_until 60 has_life graph-spawn-adopted "adopted idle worker $W1" "old owner run $R1 node fix" \
  "new owner run $R2 node implement" "context kept"
check "still one edit worker after the second run ($(workers_of edit))" test "$(workers_of edit)" -eq 1
check "$W1's registry entry now names run R2 node implement" \
  test "$(field_of "$W1" run_id)/$(field_of "$W1" node_id)" = "$R2/implement"
check "R2's implement node holds $W1" wait_until 20 node_task_is "$R2" implement "$W1"

W1_ID="$(field_of "$W1" id)"
if as_agent edit spawn stop "$W1_ID" >"$WORK/stop.out" 2>&1; then
  bad "edit stopped $W1 — authority still read the old, agent-launched run"
else
  ok "edit's spawn stop of $W1 refused"
fi
check "the refusal names the new run R2, not R1" \
  bash -c "grep -qF '$R2' '$WORK/stop.out' && ! grep -qF '$R1' '$WORK/stop.out'"
check "spawn-stop-refused row names R2" has_life spawn-stop-refused "graph run $R2"
check "$W1 is still running" worker_is "$W1" running
check "$W1's window still live" window_live "$W1"

if AGENT_ROLE="$W1" MUXCODE_CROSS_TREE_GUARD=0 "$MUX" send build build "worker build after adoption" \
   --force --no-notify >"$WORK/send-build.out" 2>&1; then
  bad "$W1's build:build request delivered — node authority still read the finished run"
else
  ok "$W1's build:build request refused"
fi
check "CheckGraphNodeAuthority names R2's build node, not R1" \
  bash -c "grep -qF 'graph run $R2 owns the build:build work as node \"build\"' '$WORK/send-build.out' && ! grep -qF '$R1' '$WORK/send-build.out'"
check "graph-authority-refused row sourced from $W1" has_life graph-authority-refused "$W1"
check "negative control: $W1's request to plan (not R2's work) is accepted" \
  bash -c "AGENT_ROLE='$W1' '$MUX' send plan update-docs 'worker note after adoption' --force --no-notify >/dev/null 2>&1"
release
check "R2 settles once $W1 answers" wait_until 90 run_settled "$R2"
check "$W1 released to the idle pool again, by R2" \
  wait_until 30 has_life spawn-idle "$W1: idle — released by run $R2 node implement"

# --- 3. Busy workers, the cap, and adoption at the cap ----------------------
echo "--- 3. a busy worker is not adopted; the cap refuses a launch, never a reuse"
hold
R3="$(as_agent auto graph run --file "$WORK/one.json" 2>&1 | started_id)"
check "run R3 started: ${R3:-none}" test -n "$R3"
check "R3 adopted idle $W1" wait_until 60 has_life graph-spawn-adopted "$W1" "new owner run $R3 node implement"
R4="$(as_agent auto graph run --file "$WORK/one.json" 2>&1 | started_id)"
check "run R4 started: ${R4:-none}" test -n "$R4"
check "R4 runs on a worker of its own, not busy $W1" wait_until 60 node_running_on_new_worker "$R4" implement "$W1"
W2="$(node_task "$R4" implement)"
check "R4 did not adopt the busy worker" not has_life graph-spawn-adopted "new owner run $R4"
check "the second demand spawned within the cap: two edit workers ($(workers_of edit))" test "$(workers_of edit)" -eq 2
check "$W1 still belongs to R3" test "$(field_of "$W1" run_id)" = "$R3"
check "R4's worker $W2 booted" stub_running_or_dump "$W2"

set_config 2 600
R5="$(as_agent auto graph run --file "$WORK/one.json" 2>&1 | started_id)"
check "run R5 started at the cap: ${R5:-none}" test -n "$R5"
check "spawn-cap-refused row names R5" wait_until 30 has_life spawn-cap-refused "$R5: implement"
check "graph-spawn-deferred row says R5 waits on the cap" has_life graph-spawn-deferred "$R5: implement waits" "spawn cap reached"
check "R5's node waits ready, deferred on MUXCODE_SPAWN_MAX_WORKERS=2" node_deferred_on "$R5" implement 'MUXCODE_SPAWN_MAX_WORKERS=2'
check "no worker launched past the cap ($(workers_of edit) edit workers)" test "$(workers_of edit)" -eq 2

set_config 1 600
sleep 5
check "lowering the cap to 1 stopped no live worker: $W1 running" worker_is "$W1" running
check "lowering the cap to 1 stopped no live worker: $W2 running" worker_is "$W2" running
check "both worker windows still live" bash -c "tmux list-windows -t '$BUS_SESSION' -F '#W' | grep -qx '$W1' && tmux list-windows -t '$BUS_SESSION' -F '#W' | grep -qx '$W2'"
release
check "R3 completes" wait_until 90 run_is "$R3" complete
check "R4 completes" wait_until 90 run_is "$R4" complete
check "R5 completes on an adopted worker while the cap is 1" wait_until 90 run_is "$R5" complete
check "R5's worker came by adoption (graph-spawn-adopted names R5)" has_life graph-spawn-adopted "new owner run $R5 node implement"
check "still two edit workers — none launched for R5 ($(workers_of edit))" test "$(workers_of edit)" -eq 2
set_config 3 600

# --- 4. The agent road -----------------------------------------------------
echo "--- 4. spawn start: one worker per owner, queue on busy, the cap"
as_agent edit spawn start research "agent task one" --no-worktree >"$WORK/start1.out" 2>&1
check "first spawn start launched a worker" grep -q '^Started spawn: ' "$WORK/start1.out"
A_ID="$(start_field "$WORK/start1.out")"; A="$(start_role "$WORK/start1.out")"
check "agent worker $A answered and reads idle" wait_until 60 shows_idle "$A"
as_agent edit spawn start research "agent task two" --no-worktree >"$WORK/start2.out" 2>&1
check "second spawn start says it reused $A_ID" grep -qF "Reused your idle spawn: $A_ID" "$WORK/start2.out"
check "one research worker for both starts ($(workers_of research))" test "$(workers_of research)" -eq 1
check "spawn-reused row names $A for agent edit" has_life spawn-reused "$A: reused for agent edit"
check "spawn list shows one research worker" \
  test "$(as_agent edit spawn list 2>/dev/null | grep -c ' research ')" -eq 1
check "task two answered before the queue test" wait_until 60 answered_at_least "$A" 2
wait_until 30 shows_idle "$A"

hold
as_agent edit spawn start research "agent task three" --no-worktree >"$WORK/start3.out" 2>&1
check "third start reseeds the idle worker" grep -qF "Reused your idle spawn: $A_ID" "$WORK/start3.out"
as_agent edit spawn start research "agent task four" --no-worktree >"$WORK/start4.out" 2>&1
check "fourth start, worker busy, says it queued on $A_ID" grep -qF "Queued on your busy spawn: $A_ID" "$WORK/start4.out"
check "queueing launched nothing ($(workers_of research) research worker)" test "$(workers_of research)" -eq 1

if AGENT_ROLE=plan MUXCODE_SPAWN_MAX_WORKERS=1 "$MUX" spawn start research "plan task at cap" --no-worktree >"$WORK/cap.out" 2>&1; then
  bad "plan's start past the cap succeeded"
else
  ok "plan's start past the cap (1) exits non-zero"
fi
check "the refusal names the cap" bash -c "grep -qF 'spawn cap reached' '$WORK/cap.out' && grep -qF 'MUXCODE_SPAWN_MAX_WORKERS=1' '$WORK/cap.out'"
check "spawn-cap-refused row names agent plan" has_life spawn-cap-refused "agent plan"
check "plan did not adopt edit's busy worker" owner_is "$A" edit
check "the refused start launched nothing ($(workers_of research) research worker)" test "$(workers_of research)" -eq 1
as_agent plan spawn start research "plan task within cap" --no-worktree >"$WORK/start-plan.out" 2>&1
check "within the cap, plan's start says it started its own worker" grep -q '^Started spawn: ' "$WORK/start-plan.out"
check "two research workers now, one per owner ($(workers_of research))" test "$(workers_of research)" -eq 2
release
check "every task given to $A was answered, the queued one included" wait_until 60 answered_at_least "$A" 4
check "$A answered exactly four tasks ($(answered "$A"))" test "$(answered "$A")" -eq 4
for n in one two three four; do
  check "bus log: $A replied to the seed of agent task $n" task_replied "$A" "agent task $n"
done
SEEDS_A="$(for n in one two three four; do seed_for_task "$A" "agent task $n"; done | sort -u | grep -c .)"
check "the four tasks rode four distinct seeds ($SEEDS_A)" test "$SEEDS_A" -eq 4

# --- 5. A lost delivery record never strands a finished run's worker -------
echo "--- 5. delivery record removed: idle, then reaped — never stranded"
R6="$(as_agent auto graph run --file "$WORK/one.json" 2>&1 | started_id)"
check "run R6 started: ${R6:-none}" test -n "$R6"
check "R6 completes" wait_until 90 run_is "$R6" complete
W6="$(node_task "$R6" implement)"
check "R6's worker $W6 released idle" wait_until 30 has_life spawn-idle "$W6: idle — released by run $R6 node implement"
check "$W6 carries an idle stamp" wait_until 20 idle_stamped "$W6"
IDLE_AT="$(field_of "$W6" idle_since)"
SEED6="$(field_of "$W6" seed_msg_id)"
check "$W6's seed $SEED6 had a delivery record" test -f "$BD/delivery/$SEED6.status"
rm -f "$BD/delivery/$SEED6.status"
check "the record is gone" test ! -e "$BD/delivery/$SEED6.status"
sleep 6
check "three ticks later $W6 is still idle on the same stamp (not misread as busy)" idle_on_stamp "$W6" "$IDLE_AT"
check "spawn status still reads idle" shows_idle "$W6"
set_config 3 3
check "spawn-reaped row: $W6 reaped, last owner R6, new owner none" \
  wait_until 40 has_life spawn-reaped "$W6: reaped — last owner run $R6 node implement, new owner none"
check "$W6 is completed — not stranded running" worker_is "$W6" completed
check "$W6's window is gone" not window_live "$W6"
set_config 3 600

# --- 6. A lost worker is replaced once, never doubled -----------------------
echo "--- 6. lost worker: one replacement, never two live entries"
hold
R7="$(as_agent auto graph run --file "$WORK/one.json" 2>&1 | started_id)"
check "run R7 started: ${R7:-none}" test -n "$R7"
check "R7's node is running on a worker" wait_until 60 node_is "$R7" implement running
W7="$(node_task "$R7" implement)"
check "R7's worker $W7 booted" stub_running_or_dump "$W7"

# sample_live <rid> <out> <stop> — highest live-entry count seen for the run, and samples taken.
sample_live() {
  local max=0 n samples=0
  while [ ! -e "$3" ]; do
    n="$(live_for_run "$1")"
    [ "$n" -gt "$max" ] && max="$n"
    samples=$((samples + 1))
    echo "$max $samples" > "$2"
    sleep 0.2
  done
}
sample_live "$R7" "$WORK/sample.out" "$WORK/sample.stop" &
SAMPLER=$!
tmux kill-window -t "$BUS_SESSION:$W7"
check "graph-spawn-replaced row: $W7 replaced" wait_until 60 has_life graph-spawn-replaced "$R7: implement worker $W7"
check "R7's node now runs on a replacement" wait_until 30 node_running_on_new_worker "$R7" implement "$W7"
W7B="$(node_task "$R7" implement)"
check "the lost entry $W7 is no longer live" not worker_is "$W7" running
release
check "R7 completes on the replacement $W7B" wait_until 90 run_is "$R7" complete
touch "$WORK/sample.stop"
wait "$SAMPLER" 2>/dev/null
SAMPLER=""
read -r MAX_LIVE SAMPLES < "$WORK/sample.out"
check "registry never showed two live entries for R7 (max ${MAX_LIVE:-?} over ${SAMPLES:-0} samples)" \
  bash -c "[ '${SAMPLES:-0}' -ge 10 ] && [ '${MAX_LIVE:-9}' -le 1 ]"
check "replaced exactly once ($(count_life graph-spawn-replaced "$R7"))" test "$(count_life graph-spawn-replaced "$R7")" -eq 1

# --- Coverage floor --------------------------------------------------------
# 1 daemon + 9 one-run + 17 adoption/authority + 21 busy/cap + 24 agent road
# + 11 record removed + 9 lost worker = 92. A section that dies early runs
# fewer, and a green summary over a short run is the failure this floor
# catches — so it is the arithmetic, not a margin under it.
FLOOR=92
total=$((pass + fail))
if [ "$total" -ge "$FLOOR" ]; then
  ok "coverage floor met ($total checks executed)"
else
  bad "coverage floor NOT met — only $total checks executed, want >= $FLOOR (a skipped section must not report green)"
fi

echo ""
echo "  ${pass} passed, ${fail} failed"
[ "$fail" -eq 0 ] || exit 1
exit 0
