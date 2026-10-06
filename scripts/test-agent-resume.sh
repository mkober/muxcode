#!/usr/bin/env bash
# Integration test for MUX-139 — Claude agents come back from a mass exit by
# resuming their own conversations, with their definitions, and the exits are
# reported as one correlated event.
#
# Hermetic: scratch bus + private tmux server + scratch daemon, a muxcode built
# from THIS tree (the installed binary is never run), and
# scripts/fixtures/claude-stub built into the scratch bin as `claude`, so the
# real launcher execs it with the real flag set and `ps` shows the real argv.
# Each pane's stub reads its exit banner's session id from its own
# CLAUDE_STUB_SESSION_FILE, which is how every role gets a distinct id and the
# id-to-role pairing can be asserted rather than "some --resume appeared".
#
# Sections:
#   A  fresh launch of edit, plan, commit with their definitions
#   B  mass exit — three agents SIGTERMed inside one second: exactly ONE
#      mass-agent-exit event (bus + lifecycle) naming all three; each agent
#      relaunched with --resume <its own id> plus --agent/--agents (MUX-136
#      guard); edit resumed, never fresh-launched
#   C  negative controls on the daemon road: a single death outside the
#      correlation window raises no second mass event, and a pane with no
#      resumable id relaunches fresh with the logged reason (resume-scrape-miss)
#   D  the transcript fallback declines a shared cwd even with two candidate
#      transcripts for the agent (the 2026-09-02 shape newest-wins would get
#      wrong), launching fresh with the reason logged
#   E  MUXCODE_AUTO_RESUME_DISABLE=1 restores today's fresh relaunch despite a
#      resumable banner
#   F  a graph spawn worker with a running node, killed before answering, is
#      resumed in its own pane with --resume <its id> and its definition, then
#      verified and reseeded; the run keeps running
#   G  Restart Agents (`reload --all --provider claude --resume`, the modal's
#      CLI form) with every agent dead: the opencode filter and plain
#      `reload --all` touch nothing (Decision 2 negative controls); the claude
#      restart brings back every dead Claude agent — edit via resume — each
#      with its own id and definition, daemon-verified, and writes no
#      --cli/--model override file
#   H  live tools-restored check against a REAL Claude — gated by
#      MUXCODE_AGENT_RESUME_LIVE=1, never counted toward the floor (see there)
#
# A coverage floor keeps a skipped section from reporting green. Requires go,
# tmux, jq. Runs ~4–5 minutes.
set -euo pipefail

PASS=0
FAIL=0
for t in tmux go jq; do
  command -v "$t" >/dev/null 2>&1 || { echo "SKIP: $t is required"; exit 2; }
done

REPO=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
WORK=$(mktemp -d /tmp/agent-resume-XXXXXX)
SESSION="agent-resume-$$"
# The bus lives under a scratch base (MUXCODE_BUS_BASE), exported before the
# private tmux server starts so the daemon, every CLI call and every pane's
# agent inherit it: mass-exit correlation scans every bus directory it can
# discover, so a fixture under /tmp would read live sessions' exits and be read
# by them. OUTSIDE stands in for the shared namespace, holding a decoy session
# with fresh exit rows that must never enter the fixture's bursts.
export MUXCODE_BUS_BASE="$WORK/bus"
BUSDIR="$MUXCODE_BUS_BASE/muxcode-bus-$SESSION"
OUTSIDE="$WORK/outside"
DECOY="agent-resume-decoy-$$"
mkdir -p "$MUXCODE_BUS_BASE" "$OUTSIDE/muxcode-bus-$DECOY"

ID_EDIT=1a2b3c4d-0000-4000-8000-0000000b0e01
ID_PLAN=1a2b3c4d-0000-4000-8000-0000000b0a02
ID_COMMIT=1a2b3c4d-0000-4000-8000-0000000b0c03
ID_OPTOUT=1a2b3c4d-0000-4000-8000-0000000e0a04
ID_SPAWN=1a2b3c4d-0000-4000-8000-0000000f05a5
ID_TX1=1a2b3c4d-0000-4000-8000-0000000d0001
ID_TX2=1a2b3c4d-0000-4000-8000-0000000d0002
G_EDIT=1a2b3c4d-0000-4000-8000-0000000a0e11
G_PLAN=1a2b3c4d-0000-4000-8000-0000000a0a12
G_COMMIT=1a2b3c4d-0000-4000-8000-0000000a0c13

# Provider/model settings inherited from the caller's session would decide which
# provider each role resolves to; only the ones set below may apply.
for v in $(env | grep -oE '^MUXCODE_[A-Z0-9_]+_(CLI|MODEL)=' | tr -d '='); do unset "$v"; done
export BUS_SESSION="$SESSION" AGENT_ROLE=edit BUS_ROLE=edit
export HOME="$WORK/home"
export TMUX_TMPDIR="$WORK/tmux"
unset TMUX MUXCODE_EDIT_AUTO_RESTART_DISABLE MUXCODE_AUTO_RESUME_DISABLE MUXCODE_RESUME_FIRST_SIGHTING
export MUXCODE_LIFECYCLE_LOG_DIR="$WORK/lifecycle"
export MUXCODE_INSTALL_DIR="$WORK/install"
: >"$WORK/empty-config"
export MUXCODE_CONFIG="$WORK/empty-config"
export MUXCODE_SESSION_REPO_DIR="$WORK/project"
export MUXCODE_AGENT_CLI=claude MUXCODE_EDIT_CLI=claude MUXCODE_PLAN_CLI=claude MUXCODE_COMMIT_CLI=claude
export MUXCODE_AGENT_HEALTH_CHECK_SECS=3 MUXCODE_DEFINITION_CHECK_SECS=3 MUXCODE_MASS_EXIT_WINDOW_SECS=15
export MUXCODE_CONTROL_PANE_DISABLE=1 MUXCODE_FORCE_RESPOND_DISABLE=1 MUXCODE_AGENTDEFS_WATCH_DISABLE=1
export MUXCODE_TMP_CLEANUP_THRESHOLD=0 MUXCODE_ACTIVE_WATCHDOG_SECS=0 MUXCODE_PROMPT_AGENT_DISABLE=1
export MUXCODE_BRANCH_TIME_DISABLE=1 MUXCODE_TASK_STALL_DISABLE=1 MUXCODE_STUCK_RELOAD_DISABLE=1
export MUXCODE_PERMBLOCK_WATCHDOG_DISABLE=1 MUXCODE_DEDUP_WINDOW=0
export CLAUDE_STUB_SESSION_FILE="$WORK/spawn.sid"
REAL_CLAUDE=$(command -v claude 2>/dev/null || true)
export PATH="$WORK/bin:$PATH"
mkdir -p "$WORK/bin" "$WORK/home" "$WORK/tmux" "$WORK/install/agents" "$WORK/project" "$WORK/lifecycle"
ROLES="edit plan commit"

DPID=""
# cleanup reaps the daemon before removing the tree and never fails the exit
# status. Only the private tmux server is killed — a pattern kill would reach
# the machine's real listeners.
cleanup() {
  set +e
  stop_daemon
  pkill -f "watch $SESSION" 2>/dev/null
  tmux kill-server 2>/dev/null
  sleep 1
  rm -rf "$BUSDIR" "$WORK" 2>/dev/null || { sleep 2; rm -rf "$BUSDIR" "$WORK" 2>/dev/null; }
  true
}
trap cleanup EXIT

ok()   { PASS=$((PASS + 1)); echo "  ok: $1"; }
fail() { FAIL=$((FAIL + 1)); echo "  FAIL: $1"; }

events() { "$MUX" lifecycle show "$SESSION" --limit 0 2>/dev/null | grep -E -- "$1" || true; }
event_count() { events "$1" | wc -l | tr -d ' '; }
has_event() { [ -n "$(events "$1")" ]; }
event_count_ge() { [ "$(event_count "$1")" -ge "$2" ]; }
bus_count() { jq -c --arg a "$1" 'select(.to == "edit" and .action == $a)' "$BUSDIR/log.jsonl" 2>/dev/null | wc -l | tr -d ' '; }
pane_pids() { tmux list-panes -t "$SESSION:$1" -F '#{pane_pid}' 2>/dev/null || true; }
# stub_pid WINDOW — the live claude-stub under any pane of the window.
stub_pid() {
  local p c
  for p in $(pane_pids "$1"); do
    c=$(pgrep -P "$p" -x claude 2>/dev/null | head -1 || true)
    [ -n "$c" ] && { echo "$c"; return 0; }
  done
  return 0
}
stub_argv() { local p; p=$(stub_pid "$1"); [ -n "$p" ] && ps -o command= -p "$p" 2>/dev/null || true; }
type_in() { tmux send-keys -t "$SESSION:$1.1" -l -- "$2"; tmux send-keys -t "$SESSION:$1.1" Enter; }
pane_has() { tmux capture-pane -p -J -t "$SESSION:$1" -S -120 2>/dev/null | grep -q -- "$2"; }
# stub_reports WINDOW PATTERN — the LIVE stub's startup line (anchored on its
# pid, so an earlier launch's line in scrollback cannot satisfy it) matches.
stub_reports() { local p; p=$(stub_pid "$1"); [ -n "$p" ] && pane_has "$1" "claude-stub pid=$p .*$2"; }
stub_up() { local p; p=$(stub_pid "$1"); [ -n "$p" ] && [ "$p" != "${2:-}" ]; }
stub_gone() { [ -z "$(stub_pid "$1")" ]; }

# wait_for SECS DESCRIPTION CHECK-FUNCTION [ARGS...] — polls once a second.
# A timeout is a recorded FAIL plus a state dump, never an abort under set -e.
wait_for() {
  local secs=$1 desc=$2 i=0
  shift 2
  while [ "$i" -lt "$secs" ]; do
    if "$@" >/dev/null 2>&1; then ok "$desc"; return 0; fi
    sleep 1; i=$((i + 1))
  done
  fail "$desc (timeout ${secs}s)"
  dump_state
  return 0
}

dump_state() {
  echo "  --- state dump ---"
  local w
  for w in $ROLES ${SPAWN_WIN:-}; do
    echo "  [pane $w]"; tmux capture-pane -p -t "$SESSION:$w" -S -10 2>/dev/null | grep -v '^$' | tail -6 | sed 's/^/    /'
  done
  echo "  [lifecycle tail]"; "$MUX" lifecycle show "$SESSION" 2>/dev/null | tail -10 | sed 's/^/    /'
  echo "  [daemon log tail]"; tail -5 "$WORK/daemon.log" 2>/dev/null | sed 's/^/    /'
  echo "  --- end dump ---"
}

start_daemon() {
  "$MUX" watch "$SESSION" --poll 2 >>"$WORK/daemon.log" 2>&1 &
  DPID=$!
  sleep 1
}
stop_daemon() {
  if [ -n "$DPID" ]; then kill "$DPID" 2>/dev/null || true; wait "$DPID" 2>/dev/null || true; DPID=""; fi
}
restart_daemon() { stop_daemon; start_daemon; kill -0 "$DPID" 2>/dev/null && ok "$1" || fail "$1"; }

# flag_check WINDOW FLAG [LABEL] — the window's live agent argv carries FLAG.
flag_check() {
  if stub_argv "$1" | grep -q -- "$2"; then ok "${3:-$1} argv carries $2"; else fail "${3:-$1} argv missing $2: $(stub_argv "$1")"; fi
}
# resumed_own WINDOW ID — the live stub resumed exactly ID, with its definition.
resumed_own() {
  wait_for 25 "$1: stub reports resume of its own id $2" stub_reports "$1" "agent=true agents=true resume=true skip-perms=true resume-id=$2"
  flag_check "$1" "--resume $2"
  flag_check "$1" "--agent[[:space:]]"
  flag_check "$1" "--agents"
}
# as_user runs muxcode with no agent identity, as a person at a shell would.
as_user() { env -u AGENT_ROLE -u BUS_ROLE "$MUX" "$@"; }
# decoy_exit records a Claude exit "now" in the outside decoy session.
decoy_exit() {
  printf '{"session":"%s","role":"plan","at":%s}\n' "$DECOY" "$(date +%s)" >>"$OUTSIDE/muxcode-bus-$DECOY/agent-exits.jsonl"
}
# lists_session BASE SESSION — discovery run against BASE (unset: the
# production default) lists SESSION.
lists_session() {
  if [ -n "$1" ]; then
    MUXCODE_BUS_BASE="$1" "$MUX" remote list 2>/dev/null | grep -q -- "$2"
  else
    env -u MUXCODE_BUS_BASE "$MUX" remote list 2>/dev/null | grep -q -- "$2"
  fi
}

echo "=== MUX-139 agent auto-resume integration test ==="

(cd "$REPO/tools/muxcode" && go build -buildvcs=false -o "$WORK/bin/muxcode" .) \
  || { echo "SKIP: go build muxcode failed"; exit 2; }
(cd "$REPO/scripts/fixtures/claude-stub" && go build -buildvcs=false -o "$WORK/bin/claude" .) \
  || { echo "SKIP: go build claude-stub failed"; exit 2; }
MUX="$WORK/bin/muxcode"
ok "scratch muxcode and claude-stub built from this tree"

printf -- '---\ndescription: Editor\n---\nEdit code.\n' >"$WORK/install/agents/code-editor.md"
printf -- '---\ndescription: Docs\n---\nMaintain docs.\n' >"$WORK/install/agents/planner.md"
printf -- '---\ndescription: Git\n---\nManage git.\n' >"$WORK/install/agents/git-manager.md"

# Each agent pane gets its own role identity and stub session file; the shell
# prompt ends in "$" so a dead agent reads as a bare shell.
pane_env() {
  echo "export AGENT_ROLE=$1 BUS_ROLE=$1 CLAUDE_STUB_SESSION_FILE=$WORK/$1.sid; PS1='\$ '; clear"
}
tmux new-session -d -s "$SESSION" -n edit -x 160 -y 40 -c "$WORK/project"
tmux split-window -h -t "$SESSION:edit" -c "$WORK/project"
for w in plan commit; do
  tmux new-window -t "$SESSION" -n "$w" -c "$WORK/project"
  tmux split-window -h -t "$SESSION:$w" -c "$WORK/project"
done
for w in $ROLES; do type_in "$w" "$(pane_env "$w")"; done
sleep 1
"$MUX" init "$SESSION" >/dev/null 2>&1 || true
start_daemon
kill -0 "$DPID" 2>/dev/null && ok "scratch daemon running" || fail "scratch daemon started"

# ── Isolation: the fixture and the shared namespace cannot see each other ──
echo "-- isolation: scratch bus base"
decoy_exit
[ -d "$BUSDIR" ] && [ ! -e "/tmp/muxcode-bus-$SESSION" ] \
  && ok "fixture bus is under the scratch base, nothing under /tmp" || fail "fixture bus location: $BUSDIR, /tmp copy present=$([ -e "/tmp/muxcode-bus-$SESSION" ] && echo yes || echo no)"
lists_session "$MUXCODE_BUS_BASE" "$SESSION" && ok "fixture discovery lists the fixture session" || fail "fixture discovery misses its own session"
lists_session "$OUTSIDE" "$DECOY" && ok "the decoy is a discoverable session in its own base (control is not vacuous)" || fail "the decoy is not discoverable even from its own base"
lists_session "$MUXCODE_BUS_BASE" "$DECOY" && fail "fixture discovery reached the outside decoy" || ok "fixture discovery does not reach the outside decoy"
lists_session "" "$SESSION" && fail "production discovery (/tmp) lists the fixture session" || ok "production discovery (/tmp) cannot see the fixture"

# ── A: fresh launches ────────────────────────────────────────────
echo "-- A: fresh launch with definitions"
for w in $ROLES; do type_in "$w" "muxcode agent launch $w"; done
for w in $ROLES; do
  wait_for 20 "$w: fresh launch carries --agent/--agents" stub_reports "$w" "agent=true agents=true resume=false skip-perms=true"
done

# ── B: mass exit ─────────────────────────────────────────────────
echo "-- B: three agents exit inside one second"
echo "$ID_EDIT" >"$WORK/edit.sid"
echo "$ID_PLAN" >"$WORK/plan.sid"
echo "$ID_COMMIT" >"$WORK/commit.sid"
P_EDIT=$(stub_pid edit); P_PLAN=$(stub_pid plan); P_COMMIT=$(stub_pid commit)
decoy_exit # a fresh outside exit inside the burst's window
kill -TERM "$P_EDIT" "$P_PLAN" "$P_COMMIT" 2>/dev/null || true
for w in $ROLES; do
  id_var="ID_$(echo "$w" | tr '[:lower:]' '[:upper:]')"
  wait_for 10 "$w: exit banner offers ${!id_var}" pane_has "$w" "claude --resume ${!id_var}"
done
wait_for 30 "mass-agent-exit lifecycle row recorded" has_event "mass-agent-exit"
for w in $ROLES; do
  id_var="ID_$(echo "$w" | tr '[:lower:]' '[:upper:]')"
  wait_for 30 "$w: resume-scrape-hit names its own id" has_event "resume-scrape-hit.*$w: session ${!id_var}"
  resumed_own "$w" "${!id_var}"
done
sleep 8 # sweeps past the burst: any second event would land here
n=$(bus_count mass-agent-exit)
[ "$n" -eq 1 ] && ok "exactly one mass-agent-exit event sent to edit" || fail "mass-agent-exit events to edit = $n, want 1"
n=$(event_count "mass-agent-exit")
[ "$n" -eq 1 ] && ok "exactly one mass-agent-exit lifecycle row" || fail "mass-agent-exit rows = $n, want 1"
row=$(events "mass-agent-exit" | tail -1)
for w in $ROLES; do
  case "$row" in *"$w"*) ok "mass-agent-exit names $w" ;; *) fail "mass-agent-exit omits $w: $row" ;; esac
done
case "$row" in *"$SESSION"*) ok "mass-agent-exit names the session" ;; *) fail "mass-agent-exit omits the session: $row" ;; esac
case "$row" in *"$DECOY"*) fail "the outside decoy's exit entered the fixture burst: $row" ;; *) ok "the outside decoy's fresh exit stayed out of the burst" ;; esac
fresh_edit=$(events "agent-relaunch.*edit: muxcode agent launch edit" | grep -v -- "--resume" || true)
[ -z "$fresh_edit" ] && ok "edit: every daemon relaunch was a resume, never fresh" || fail "edit fresh-launched: $fresh_edit"
for w in $ROLES; do
  wait_for 20 "$w: agent-recovered after the resume" has_event "agent-recovered.*$w"
done

# ── C: single death, no resumable id ─────────────────────────────
echo "-- C: single death outside the window, no hint → fresh with reason"
sleep 16 # past MUXCODE_MASS_EXIT_WINDOW_SECS since the burst
: >"$WORK/plan.sid"
P_PLAN=$(stub_pid plan)
decoy_exit # discovered, it would pair with this single death into a second event
kill -TERM "$P_PLAN"
wait_for 10 "plan: exit banner offers only a malformed id" pane_has plan "claude --resume 0f3a"
wait_for 25 "plan: resume-scrape-miss logged as the fresh reason" has_event "resume-scrape-miss.*plan"
wait_for 20 "plan: fresh stub up" stub_up plan "$P_PLAN"
wait_for 10 "plan: fresh relaunch, no --resume, definition carried" stub_reports plan "agent=true agents=true resume=false skip-perms=true resume-id=$"
case "$(stub_argv plan)" in *--resume*) fail "plan: no-hint relaunch carries --resume" ;; *) ok "plan: no-hint relaunch argv has no --resume" ;; esac
sleep 6
n=$(bus_count mass-agent-exit)
[ "$n" -eq 1 ] && ok "a single death raised no second mass-agent-exit" || fail "mass-agent-exit events = $n after a single death"

# ── D: transcript fallback declines a shared cwd ─────────────────
echo "-- D: ambiguous transcript cwd declines"
stop_daemon
TXDIR="$HOME/.claude/projects/$(cd "$WORK/project" && pwd -P | sed 's/[^a-zA-Z0-9]/-/g')"
mkdir -p "$TXDIR"
for id in "$ID_TX1" "$ID_TX2"; do
  printf '{"type":"user","agentSetting":"planner","sessionId":"%s"}\n' "$id" >"$TXDIR/$id.jsonl"
done
P_PLAN=$(stub_pid plan)
kill -TERM "$P_PLAN"
wait_for 10 "plan: at the shell before the bare --resume" stub_gone plan
tmux send-keys -t "$SESSION:plan.1" C-l
sleep 1 # the clear must land before the history wipe, or it scrolls the banner back in
tmux clear-history -t "$SESSION:plan.1"
pane_has plan "claude --resume" && fail "plan: banner survived the clear — D would test the pane road" || ok "plan: pane holds no banner"
type_in plan "muxcode agent launch plan --resume"
wait_for 15 "plan: resume-fresh names the shared-cwd decline" has_event "resume-fresh.*plan: .*transcript: cwd is not a worktree this agent owns alone"
wait_for 15 "plan: launched fresh" stub_reports plan "agent=true agents=true resume=false"
case "$(stub_argv plan)" in
  *"$ID_TX1"*|*"$ID_TX2"*) fail "plan resumed a candidate transcript from a shared cwd" ;;
  *) ok "plan: neither candidate transcript was resumed" ;;
esac
[ -z "$(events "resume-found.*plan")" ] && ok "plan: no resume-found row for the shared cwd" || fail "plan: resume-found: $(events 'resume-found.*plan' | tail -1)"

# ── E: opt-out ───────────────────────────────────────────────────
echo "-- E: MUXCODE_AUTO_RESUME_DISABLE=1 relaunches fresh"
MUXCODE_AUTO_RESUME_DISABLE=1 start_daemon
kill -0 "$DPID" 2>/dev/null && ok "opt-out daemon running" || fail "opt-out daemon started"
echo "$ID_OPTOUT" >"$WORK/plan.sid"
P_PLAN=$(stub_pid plan)
kill -TERM "$P_PLAN"
wait_for 10 "plan: exit banner offers $ID_OPTOUT" pane_has plan "claude --resume $ID_OPTOUT"
wait_for 25 "plan: opt-out records resume-disabled" has_event "resume-disabled.*plan"
wait_for 20 "plan: opt-out relaunch up" stub_up plan "$P_PLAN"
wait_for 10 "plan: opt-out relaunch is fresh with its definition" stub_reports plan "agent=true agents=true resume=false skip-perms=true"
[ -z "$(events "resume-scrape-hit.*plan: session $ID_OPTOUT")" ] && ok "plan: opt-out never scraped the banner" || fail "plan: opt-out resumed $ID_OPTOUT"
restart_daemon "default daemon running after the opt-out"

# ── F: graph spawn worker resumed ────────────────────────────────
echo "-- F: spawn worker with a running node"
echo "$ID_SPAWN" >"$WORK/spawn.sid"
cat >"$WORK/spawn.json" <<'EOF'
{"name": "resume-spawn", "start": "w",
 "nodes": [{"id": "w", "type": "spawn", "role": "edit", "message": "implement the fixture"}],
 "edges": []}
EOF
RID=$("$MUX" graph run --file "$WORK/spawn.json" 2>&1 | grep -o 'Started run [^ ]*' | awk '{print $3}' || true)
[ -n "$RID" ] && ok "spawn run started: $RID" || fail "spawn run did not start"
SPAWN_WIN=""; SPAWN_ROLE=""
spawn_registered() {
  local line
  line=$(grep "\"run_id\":\"$RID\"" "$BUSDIR/spawn.jsonl" 2>/dev/null | tail -1)
  SPAWN_WIN=$(printf '%s' "$line" | grep -o '"window":"[^"]*"' | cut -d'"' -f4)
  SPAWN_ROLE=$(printf '%s' "$line" | grep -o '"spawn_role":"[^"]*"' | cut -d'"' -f4)
  [ -n "$SPAWN_WIN" ]
}
wait_for 60 "worker registered for the run" spawn_registered
wait_for 30 "worker: fresh launch carries its definition" stub_reports "$SPAWN_WIN" "agent=true agents=true resume=false"
spawn_seeded() { grep "\"run_id\":\"$RID\"" "$BUSDIR/spawn.jsonl" 2>/dev/null | tail -1 | grep -q '"seed_msg_id":"[^"]'; }
wait_for 30 "worker: seed recorded (only a seeded, unanswered worker is resumable)" spawn_seeded
P_SPAWN=$(stub_pid "$SPAWN_WIN")
kill -TERM "$P_SPAWN" 2>/dev/null || true
wait_for 10 "worker: exit banner offers $ID_SPAWN" pane_has "$SPAWN_WIN" "claude --resume $ID_SPAWN"
wait_for 60 "worker: graph-spawn-resumed names its session" has_event "graph-spawn-resumed.*$SPAWN_ROLE.*$ID_SPAWN"
resumed_own "$SPAWN_WIN" "$ID_SPAWN"
wait_for 60 "worker: resume verified and reseeded" has_event "graph-spawn-resume-verified.*$SPAWN_ROLE"
st=$("$MUX" graph status "$RID" 2>/dev/null | sed $'s/\x1b\\[[0-9;]*[A-Za-z]//g' | head -1 || true)
case "$st" in *"[running]"*) ok "run still running after the resume — no stall, no failure" ;; *) fail "run state after the resume: $st" ;; esac
"$MUX" graph cancel "$RID" >/dev/null 2>&1 || true

# ── G: Restart Agents with every agent dead ──────────────────────
echo "-- G: operator restart (reload --all --provider claude --resume)"
stop_daemon
MUXCODE_AGENT_HEALTH_CHECK_SECS=100000 start_daemon
kill -0 "$DPID" 2>/dev/null && ok "daemon with the health sweep parked running" || fail "parked daemon started"
echo "$G_EDIT" >"$WORK/edit.sid"
echo "$G_PLAN" >"$WORK/plan.sid"
echo "$G_COMMIT" >"$WORK/commit.sid"
P_EDIT=$(stub_pid edit); P_PLAN=$(stub_pid plan); P_COMMIT=$(stub_pid commit)
kill -TERM "$P_EDIT" "$P_PLAN" "$P_COMMIT" 2>/dev/null || true
for w in $ROLES; do wait_for 10 "$w: dead at the shell" stub_gone "$w"; done
relaunch_rows() { event_count "agent-relaunch|agent-reload"; }

before=$(relaunch_rows)
out=$(as_user reload --all --provider opencode --resume 2>&1 || true)
case "$out" in *"0/0 agents restarted"*) ok "opencode filter selects no Claude agent" ;; *) fail "opencode filter output: $out" ;; esac
[ "$(relaunch_rows)" -eq "$before" ] && ok "opencode filter relaunched nothing" || fail "opencode filter relaunched an agent"

# Windowless roles read "alive" to ReloadAll (IsAgentAlive's fail-safe) and get
# a config-only pass — pre-existing and out of scope; the dead windowed agents
# and edit are what Decision 2 says plain reload --all must still skip.
out=$(as_user reload --all 2>&1 || true)
if printf '%s\n' "$out" | grep -qE '^[[:space:]]*[✓✗] (edit|plan|commit)[[:space:]]'; then
  fail "plain reload --all acted on a dead windowed agent or edit: $out"
else
  ok "plain reload --all still skips every dead agent and edit"
fi
[ "$(relaunch_rows)" -eq "$before" ] && ok "plain reload --all relaunched nothing (dead agents and edit skipped)" || fail "plain reload --all relaunched an agent"
for w in $ROLES; do stub_gone "$w" && ok "$w still down after the negative controls" || fail "$w came back before the restart"; done

if out=$(as_user reload --all --provider claude --resume 2>&1); then ok "claude restart exits 0"; else fail "claude restart failed: $out"; fi
case "$out" in *"3/3 agents restarted"*) ok "every dead Claude agent restarted (3/3)" ;; *) fail "claude restart output: $out" ;; esac
for w in $ROLES; do
  id_var="G_$(echo "$w" | tr '[:lower:]' '[:upper:]')"
  [ -n "$(events "agent-relaunch.*$w: muxcode agent launch $w --reason restart --resume ${!id_var}")" ] \
    && ok "$w: operator relaunch row resumes its own id" || fail "$w: no operator relaunch row for ${!id_var}"
  resumed_own "$w" "${!id_var}"
  has_event "agent-restart-verified.*$w" && ok "$w: daemon verified the restart" || fail "$w: no agent-restart-verified row"
done
overrides=$(ls "$BUSDIR/config/"*.env 2>/dev/null || true)
[ -z "$overrides" ] && ok "no --cli/--model override file written for any role" || fail "override files written: $overrides"
[ -n "$(events "agents-restart.*3/3 restarted")" ] && ok "agents-restart lifecycle row records 3/3" || fail "no agents-restart 3/3 row"

# ── H: live tools-restored check (gated) ─────────────────────────
echo "-- H: live tools-restored check"
if [ "${MUXCODE_AGENT_RESUME_LIVE:-0}" = "1" ]; then
  # A real Claude cannot run under the scratch HOME (no auth), so this section
  # prints the procedure for the human observation the spec records instead of
  # asserting — see MUX-139 Phase 6.
  echo "  LIVE: real claude at ${REAL_CLAUDE:-<not found>}. In a real session:"
  echo "    1. muxcode agent launch plan ; give it a turn ; /exit"
  echo "    2. muxcode agent launch plan --resume"
  echo "    3. confirm the conversation is back and NO 'no longer available' /"
  echo "       'tool restrictions no longer apply' banner is shown; record it in the spec"
else
  echo "  SKIP (not counted): set MUXCODE_AGENT_RESUME_LIVE=1 for the human-observed procedure"
fi

echo "=== $PASS passed, $FAIL failed ==="
FLOOR=104 # every ok on a full pass; H is never counted
[ "$PASS" -ge "$FLOOR" ] || { echo "FAIL: coverage floor not met ($PASS < $FLOOR)"; exit 1; }
[ "$FAIL" -eq 0 ] || exit 1
echo "PASS"
