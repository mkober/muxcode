#!/usr/bin/env bash
# Integration test for MUX-188 — a spawn worker's launch leaves its base role
# alone.
#
# A worker runs its base role's launch (`AGENT_ROLE=spawn-… muxcode agent
# launch edit --reason spawn`). Every launch step keyed on the role used to act
# on the live edit agent: a stray request:startup in edit's inbox (2026-09-24,
# 2026-11-27), edit's own unread startup retired, edit's read-but-unanswered
# tasks cleared, edit's agent-defs stamp rewritten.
#
# Hermetic: muxcode built from THIS tree (MUXCODE_SPAWN_STARTUP_BIN overrides),
# a private tmux server, scratch HOME, bus, lifecycle log and config; workers
# and agents are a stub that prints ❯ and idles. No daemon: `spawn start`
# launches and seeds the worker itself, and every effect checked happens at
# launch. Each section has its own session and bus.
#
# Sections:
#   A  `spawn start edit` (owner plan): the launch row names the spawn role;
#      no request:startup to or from edit, edit's unread startup still queued,
#      edit's consumed tracked task still in flight, edit's stamp untouched;
#      the worker's inbox holds exactly its spawn-task seed
#   B  positive control — `muxcode agent launch edit --reason user`: one new
#      startup to edit replacing the unread one, the task cleared, the stamp
#      rewritten
#   C  negative control — a build from a copy of the tree with the spawn
#      guard removed sends the stray startup, clears the task and restamps,
#      so section A's assertions can fail
# A coverage floor keeps a skipped section from reporting green.
# Requires go, tmux, jq. ~30 s.
set -uo pipefail
export MUXCODE_CONSUMER_GUARD=0 # agents are stubs that never listen (MUX-145)

PASS=0
FAIL=0
EXPECTED_PASS=22
for t in tmux go jq; do
  command -v "$t" >/dev/null 2>&1 || { echo "SKIP: $t is required"; exit 2; }
done

REPO=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
WORK=$(mktemp -d /tmp/spawn-startup-XXXXXX)
. "$REPO/scripts/lib/muxcode-bus-dir.sh"
. "$REPO/scripts/lib/private-tmux.sh"

unset AGENT_ROLE BUS_ROLE BUS_SESSION MUXCODE MUXCODE_BUS_BASE MUXCODE_BUS_DIR
export HOME="$WORK/home"
export XDG_STATE_HOME="$WORK/home/.local/state"
export MUXCODE_LIFECYCLE_LOG_DIR="$WORK/lifecycle"
export MUXCODE_INSTALL_DIR="$WORK/install"
export MUXCODE_CONFIG="$WORK/config"
export BUS_MEMORY_DIR="$WORK/memory" BUS_GLOBAL_MEMORY_DIR="$WORK/global-memory"
export MUXCODE_EDIT_CLI="$WORK/stub-agent"
export MUXCODE_BRANCH_TIME_DISABLE=1 MUXCODE_DEDUP_WINDOW=0 MUXCODE_TMP_CLEANUP_THRESHOLD=0
export MUXCODE_SESSION_REPO_DIR="$WORK/project"
mkdir -p "$WORK/bin" "$WORK/home" "$WORK/lifecycle" "$WORK/install/agents" "$WORK/project"
: >"$MUXCODE_CONFIG"
cd "$WORK/project" || exit 1

private_tmux_init "$WORK/tmux"
cleanup() {
  private_tmux_kill
  cd / 2>/dev/null
  rm -rf "$WORK" 2>/dev/null
  true
}
trap cleanup EXIT

ok()   { PASS=$((PASS + 1)); echo "  ok: $1"; }
fail() { FAIL=$((FAIL + 1)); echo "  FAIL: $1"; }
check() { local desc=$1; shift; if "$@" >/dev/null 2>&1; then ok "$desc"; else fail "$desc"; fi; }
wait_for() {
  local secs=$1 desc=$2 i=0
  shift 2
  while [ "$i" -lt "$((secs * 2))" ]; do
    if "$@" >/dev/null 2>&1; then ok "$desc"; return 0; fi
    sleep 0.5; i=$((i + 1))
  done
  fail "$desc (timeout ${secs}s)"
  return 1
}

echo "=== MUX-188 a spawn worker's launch leaves its base role alone ==="

if [ -n "${MUXCODE_SPAWN_STARTUP_BIN:-}" ]; then
  cp "$MUXCODE_SPAWN_STARTUP_BIN" "$WORK/bin/muxcode-fix"
else
  (cd "$REPO/tools/muxcode" && go build -buildvcs=false -o "$WORK/bin/muxcode-fix" .) \
    || { echo "SKIP: go build muxcode failed"; exit 2; }
fi
ok "muxcode under test built"

GUARD='if worker := os.Getenv("AGENT_ROLE"); IsSpawnRole(worker) {'
mkdir -p "$WORK/pre"
cp -R "$REPO/tools/muxcode" "$WORK/pre/"
if [ "$(grep -cF "$GUARD" "$WORK/pre/muxcode/bus/launch.go")" = 1 ] \
  && perl -pi -e 's/IsSpawnRole\(worker\) \{/false \&\& IsSpawnRole(worker) {/ if /^\tif worker := os.Getenv\("AGENT_ROLE"\)/' "$WORK/pre/muxcode/bus/launch.go" \
  && grep -qF 'false && IsSpawnRole(worker) {' "$WORK/pre/muxcode/bus/launch.go" \
  && (cd "$WORK/pre/muxcode" && go build -buildvcs=false -o "$WORK/bin/muxcode-pre" .); then
  ok "pre-fix muxcode built: RunAgentLaunchResume's spawn guard removed"
else
  fail "pre-fix build: the guard was not found exactly once, or the copy failed to build"
fi

printf -- '---\ndescription: Fixture editor for test-spawn-startup-bootstrap\n---\nStub.\n' \
  >"$WORK/install/agents/code-editor.md"
cat >"$WORK/stub-agent" <<'STUB'
#!/usr/bin/env bash
while :; do printf '\n❯ '; read -r -t 5 _ || true; done
STUB
chmod +x "$WORK/stub-agent"

# start_session NAME BUILD — a fresh session and bus on BUILD (fix|pre), its
# muxcode linked as $WORK/bin/muxcode so a worker's launcher resolves it. Seeds
# edit's inbox with an unread startup, a consumed tracked task from plan and a
# stale agent-defs stamp. Sets SESSION, MUX, BD, LIFELOG, UNREAD, TASK.
start_session() {
  SESSION="spawn-startup-$1-$$"
  ln -sf "$WORK/bin/muxcode-$2" "$WORK/bin/muxcode"
  MUX="$WORK/bin/muxcode"
  export PATH="$WORK/bin:${PATH#"$WORK/bin:"}"
  export BUS_SESSION="$SESSION"
  BD="$(muxcode_bus_dir "$MUX" "$SESSION")" || exit 1
  LIFELOG="$MUXCODE_LIFECYCLE_LOG_DIR/$SESSION.log"
  tmux new-session -d -s "$SESSION" -n edit -x 200 -y 50 -c "$WORK/project" "$WORK/stub-agent"
  for v in BUS_SESSION HOME XDG_STATE_HOME MUXCODE_LIFECYCLE_LOG_DIR MUXCODE_INSTALL_DIR MUXCODE_CONFIG \
           BUS_MEMORY_DIR BUS_GLOBAL_MEMORY_DIR MUXCODE_EDIT_CLI MUXCODE_BRANCH_TIME_DISABLE \
           MUXCODE_DEDUP_WINDOW MUXCODE_TMP_CLEANUP_THRESHOLD MUXCODE_SESSION_REPO_DIR MUXCODE_CONSUMER_GUARD PATH; do
    tmux set-environment -t "$SESSION" "$v" "${!v}"
  done
  tmux set-environment -t "$SESSION" -r AGENT_ROLE
  "$MUX" init >/dev/null 2>&1 || true
  # A launch retires an unread startup; edit's own earlier one stands in for it.
  UNREAD="$(jq -nc --arg id "unread-$$-$1" --argjson ts "$(date +%s)" \
    '{id: $id, ts: $ts, from: "edit", to: "edit", type: "request", action: "startup", payload: "earlier launch"}')"
  mkdir -p "$BD/inbox"
  printf '%s\n' "$UNREAD" >>"$BD/inbox/edit.jsonl"
  UNREAD="unread-$$-$1"
  TASK="$(AGENT_ROLE=plan "$MUX" send edit update-docs "being worked on" --track --no-notify 2>&1 \
    | sed -n 's/^Tracking task \([^ ]*\).*/\1/p')"
  AGENT_ROLE=edit "$MUX" inbox --from plan >/dev/null 2>&1
  printf 'stale' >"$BD/agentdef-edit.hash"
}

startups_edit() { jq -rc 'select(.action == "startup" and (.from == "edit" or .to == "edit")) | .id' "$BD/log.jsonl" 2>/dev/null | grep -v -- "$UNREAD" | grep -c . || true; }
inbox_ids()     { jq -r 'select(.type == "request" and .action == "startup") | .id' "$BD/inbox/$1.jsonl" 2>/dev/null; }
unread_kept()   { inbox_ids edit | grep -qxF -- "$UNREAD"; }
not_kept()      { ! unread_kept; }
only_startup()  { [ "$(inbox_ids edit | grep -c .)" = 1 ]; }
task_status()   { jq -r '.status' "$BD/tasks/$TASK.json" 2>/dev/null; }
stamp()         { cat "$BD/agentdef-edit.hash" 2>/dev/null; }
launch_row()    { jq -r 'select(.event == "launch") | .detail' "$LIFELOG" 2>/dev/null | grep -E -- "$1"; }
worker_launched() { launch_row '^role=edit .*reason=spawn' >/dev/null; }
setup_ok() {
  [ -n "$TASK" ] && [ "$(task_status)" = in-flight ] && unread_kept && [ "$(stamp)" = stale ] \
    && ! grep -q '"update-docs"' "$BD/inbox/edit.jsonl" 2>/dev/null
}
seeded_ok() { # the worker's inbox holds exactly one message, its spawn-task seed
  local f="$BD/inbox/$1.jsonl"
  [ "$(grep -c . "$f" 2>/dev/null)" = 1 ] && [ "$(jq -r '.action' "$f")" = spawn-task ]
}

# ── A: the fix ─────────────────────────────────────────────────────
echo "-- A: spawn start edit on the fix"
start_session a fix
check "A: setup — edit holds an unread startup, a consumed in-flight task and a stale stamp" setup_ok
AGENT_ROLE=plan "$MUX" spawn start edit "stub task" --no-worktree >"$WORK/start-a.out" 2>&1
SPAWN_ROLE="$(sed -n 's/.*Spawn Role: \([^ ]*\).*/\1/p' "$WORK/start-a.out")"
check "A: spawn start launched a worker ($SPAWN_ROLE)" grep -q '^Started spawn: ' "$WORK/start-a.out"
wait_for 20 "A: the worker's launch ran — its launch row is written" worker_launched
check "A: the launch row names the spawn role (as=$SPAWN_ROLE)" launch_row "reason=spawn as=$SPAWN_ROLE\$"
sleep 2
check "A: no request:startup to or from edit was logged" [ "$(startups_edit)" = 0 ]
check "A: edit's inbox holds one startup request" only_startup
check "A: edit's unread startup is its own earlier one" unread_kept
check "A: edit's consumed task is still in flight" [ "$(task_status)" = in-flight ]
check "A: edit's agent-defs stamp is untouched" [ "$(stamp)" = stale ]
check "A: the worker's inbox holds exactly its spawn-task seed" seeded_ok "$SPAWN_ROLE"

# ── B: positive control — edit's own launch ────────────────────────
echo "-- B: positive control — muxcode agent launch edit"
tmux new-window -t "$SESSION" -n edit-relaunch -c "$WORK/project" "env -u AGENT_ROLE '$MUX' agent launch edit --reason user"
edit_launched() { launch_row '^role=edit .*reason=user' >/dev/null; }
wait_for 20 "B: edit's own launch ran" edit_launched
check "B: one request:startup to edit was logged" [ "$(startups_edit)" = 1 ]
replaced() { not_kept && only_startup; }
check "B: the unread startup was retired for a new one" replaced
check "B: edit's consumed task was cleared" [ "$(task_status)" = timed-out ]
check "B: edit's agent-defs stamp was rewritten" [ "$(stamp)" != stale ]

# ── C: negative control — the guard removed ────────────────────────
echo "-- C: negative control — spawn start on a build without the guard"
start_session c pre
AGENT_ROLE=plan "$MUX" spawn start edit "stub task" --no-worktree >"$WORK/start-c.out" 2>&1
check "C: spawn start launched a worker" grep -q '^Started spawn: ' "$WORK/start-c.out"
stray() { [ "$(startups_edit)" -ge 1 ]; }
wait_for 20 "C: the worker's launch sent edit a request:startup — the defect" stray
check "C: edit's unread startup was retired by the worker's launch" not_kept
check "C: edit's consumed task was cleared by the worker's launch" [ "$(task_status)" = timed-out ]
check "C: edit's agent-defs stamp was rewritten by the worker's launch" [ "$(stamp)" != stale ]

# ── Coverage floor ─────────────────────────────────────────────────
echo
echo "=== results: $PASS passed, $FAIL failed (floor $EXPECTED_PASS) ==="
if [ "$PASS" -ne "$EXPECTED_PASS" ]; then
  echo "  FAIL  coverage floor: expected exactly $EXPECTED_PASS passes, got $PASS — a section was skipped or double-counted"
  exit 1
fi
[ "$FAIL" -eq 0 ] || exit 1
exit 0
