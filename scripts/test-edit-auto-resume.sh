#!/usr/bin/env bash
# Integration test for MUX-126 — a dead edit agent is auto-restarted with both
# its conversation (--resume <id>) and its full launch flags.
#
# Hermetic: scratch bus + private tmux server + scratch daemon, a muxcode
# built from THIS tree (the installed binary is never run), and
# scripts/fixtures/claude-stub built into the scratch bin as `claude`, so the
# real launcher execs it with the real flag set and `ps` shows the real argv.
# The stub's exit banner offers the session id held in CLAUDE_STUB_SESSION_FILE
# (per role), so a section switches between a resumable UUID and no scrapeable
# id without touching the pane.
#
# Sections: (A) launch + exclusion surfaces — webhook excluded, edit monitored
# on --check and status, the opt-out excludes edit only; (B) kill edit → the
# relaunch argv carries --resume <id> AND --dangerously-skip-permissions,
# --agent, --agents, --allowedTools, --append-system-prompt; the scrape row
# precedes the relaunch row; attempt 1/3, agent-down and agent-restarting sent
# to edit. The argv check is what catches a post-relaunch scrape: that capture
# reads the typed launch line after the banner, classifies it stale and
# relaunches fresh, which fails here rather than passing on "agent came back";
# (C) no scrapeable id → fresh FLAGGED launch, and no --resume anywhere in the
# pane's process (never a flagless resume); (D) alive idle and busy agents are
# never struck; (E) stop marker, then reload marker, suppress a dead edit's
# restart; lifting them restarts it resuming the newer banner; (F) a daemon
# started with MUXCODE_EDIT_AUTO_RESTART_DISABLE=1 leaves a dead edit alone
# while restarting a dead plan (which resumes too), and a daemon without it
# restarts edit (negative control); (G) with the daemon stopped, the manual
# `muxcode resume` refuses a live edit and a non-Claude plan, typing nothing,
# then resumes a dead edit with its full flag set, its rows sourced "manual"
# and naming the user, and its reload marker released. A coverage floor keeps
# a skipped section from reporting green. Requires go, tmux, jq.
set -euo pipefail

PASS=0
FAIL=0
for t in tmux go jq; do
  command -v "$t" >/dev/null 2>&1 || { echo "SKIP: $t is required"; exit 2; }
done

REPO=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
WORK=$(mktemp -d /tmp/edit-resume-XXXXXX)
SESSION="edit-resume-$$"
BUSDIR="/tmp/muxcode-bus-$SESSION"

UUID1=1a2b3c4d-0000-4000-8000-00000000e001
UUID2=1a2b3c4d-0000-4000-8000-00000000e002
UUID3=1a2b3c4d-0000-4000-8000-00000000a003
UUID4=1a2b3c4d-0000-4000-8000-00000000e004
UUID5=1a2b3c4d-0000-4000-8000-00000000e005

export BUS_SESSION="$SESSION" AGENT_ROLE=edit BUS_ROLE=edit
export HOME="$WORK/home"
export TMUX_TMPDIR="$WORK/tmux"
unset TMUX MUXCODE_EDIT_AUTO_RESTART_DISABLE
export MUXCODE_LIFECYCLE_LOG_DIR="$WORK/lifecycle"
export MUXCODE_INSTALL_DIR="$WORK/install"
export MUXCODE_AGENT_CLI=claude MUXCODE_EDIT_CLI=claude MUXCODE_PLAN_CLI=claude MUXCODE_RUN_CLI=claude
export MUXCODE_AGENT_HEALTH_CHECK_SECS=3 MUXCODE_DEFINITION_CHECK_SECS=3
export MUXCODE_CONTROL_PANE_DISABLE=1 MUXCODE_FORCE_RESPOND_DISABLE=1 MUXCODE_AGENTDEFS_WATCH_DISABLE=1
export MUXCODE_TMP_CLEANUP_THRESHOLD=0 MUXCODE_ACTIVE_WATCHDOG_SECS=0 MUXCODE_PROMPT_AGENT_DISABLE=1
export PATH="$WORK/bin:$PATH"
mkdir -p "$WORK/bin" "$WORK/home" "$WORK/tmux" "$WORK/install/agents" "$WORK/project"

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
pane_pid() { tmux display-message -p -t "$SESSION:$1.1" '#{pane_pid}'; }
stub_pid() { pgrep -P "$(pane_pid "$1" || echo 0)" -x claude 2>/dev/null | head -1 || true; }
stub_argv() { local p; p=$(stub_pid "$1"); [ -n "$p" ] && ps -o command= -p "$p" 2>/dev/null || true; }
type_in() { tmux send-keys -t "$SESSION:$1.1" -l -- "$2"; tmux send-keys -t "$SESSION:$1.1" Enter; }
check_out() { "$MUX" agent-health --check "$1" 2>/dev/null || true; }
status_health() { "$MUX" status --json 2>/dev/null | jq -r --arg r "$1" '.. | objects | select(.role? == $r) | .health' | head -1; }
bus_alert() { jq -e --arg a "$1" 'select(.to == "edit" and .action == $a and (.payload | contains("edit")))' "$BUSDIR/log.jsonl" >/dev/null 2>&1; }

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
  for w in edit plan; do
    echo "  [pane $w]"; tmux capture-pane -p -t "$SESSION:$w.1" -S -10 2>/dev/null | grep -v '^$' | tail -8 | sed 's/^/    /'
  done
  echo "  [lifecycle tail]"; "$MUX" lifecycle show "$SESSION" 2>/dev/null | tail -8 | sed 's/^/    /'
  echo "  [daemon log tail]"; tail -5 "$WORK/daemon.log" 2>/dev/null | sed 's/^/    /'
  echo "  --- end dump ---"
}
has_event()      { [ -n "$(events "$1")" ]; }
event_count_ge() { [ "$(event_count "$1")" -ge "$2" ]; }
pane_has()       { tmux capture-pane -p -J -t "$SESSION:$1.1" -S -80 | grep -q -- "$2"; }
# stub_reports ROLE PATTERN — the LIVE stub's startup line (anchored on its
# pid, so an earlier launch's line in scrollback cannot satisfy it) matches.
stub_reports()   { local p; p=$(stub_pid "$1"); [ -n "$p" ] && pane_has "$1" "claude-stub pid=$p .*$2"; }
stub_up()        { [ -n "$(stub_pid "$1")" ] && [ "$(stub_pid "$1")" != "$2" ]; }
listener_alive() { local pid; pid=$(cat "$BUSDIR/polling-$1.marker" 2>/dev/null) && [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; }

# line_before A B — the last lifecycle row matching A precedes the last matching B.
line_before() {
  "$MUX" lifecycle show "$SESSION" --limit 0 2>/dev/null | awk -v a="$1" -v b="$2" '$0 ~ a {la=NR} $0 ~ b {lb=NR} END {exit !(la > 0 && lb > la)}'
}

# quiet_for SECS ROLE — no new health-fail, restart, or scrape row for ROLE.
quiet_for() {
  local secs=$1 role=$2 pat before
  pat="(agent-health-fail|agent-restart|resume-scrape-[a-z]+|agent-relaunch).*$role"
  before=$(event_count "$pat")
  sleep "$secs"
  [ "$(event_count "$pat")" -eq "$before" ]
}

start_daemon() {
  "$MUX" watch "$SESSION" --poll 2 >>"$WORK/daemon.log" 2>&1 &
  DPID=$!
  sleep 1
}
stop_daemon() {
  if [ -n "$DPID" ]; then kill "$DPID" 2>/dev/null || true; wait "$DPID" 2>/dev/null || true; DPID=""; fi
}

# flag_check ROLE FLAG — the role's live agent argv carries FLAG.
flag_check() {
  if stub_argv "$1" | grep -q -- "$2"; then ok "$1 relaunch argv carries $2"; else fail "$1 relaunch argv missing $2"; fi
}

echo "=== MUX-126 edit resume-aware auto-restart integration test ==="

(cd "$REPO/tools/muxcode" && go build -buildvcs=false -o "$WORK/bin/muxcode" .) \
  || { echo "SKIP: go build muxcode failed"; exit 2; }
(cd "$REPO/scripts/fixtures/claude-stub" && go build -buildvcs=false -o "$WORK/bin/claude" .) \
  || { echo "SKIP: go build claude-stub failed"; exit 2; }
MUX="$WORK/bin/muxcode"
ok "scratch muxcode and claude-stub built from this tree"

printf -- '---\ndescription: Editor\n---\nEdit code.\n' >"$WORK/install/agents/code-editor.md"
printf -- '---\ndescription: Docs\n---\nMaintain docs.\n' >"$WORK/install/agents/planner.md"

# Each agent pane gets its own role identity and stub session file; the shell
# prompt ends in "$" so a dead agent reads as a bare shell. The run window's
# agent pane is a busy process showing no prompt at all.
pane_env() {
  echo "export AGENT_ROLE=$1 BUS_ROLE=$1 BUS_SESSION=$SESSION HOME=$HOME TMUX_TMPDIR=$TMUX_TMPDIR MUXCODE_LIFECYCLE_LOG_DIR=$MUXCODE_LIFECYCLE_LOG_DIR MUXCODE_INSTALL_DIR=$MUXCODE_INSTALL_DIR MUXCODE_AGENT_CLI=claude MUXCODE_EDIT_CLI=claude MUXCODE_PLAN_CLI=claude CLAUDE_STUB_SESSION_FILE=$WORK/$1.sid PATH=$PATH; PS1='\$ '; clear"
}
tmux new-session -d -s "$SESSION" -n edit -x 160 -y 40 -c "$WORK/project"
tmux split-window -h -t "$SESSION:edit" -c "$WORK/project"
tmux new-window -t "$SESSION" -n plan -c "$WORK/project"
tmux split-window -h -t "$SESSION:plan" -c "$WORK/project"
tmux new-window -t "$SESSION" -n run -c "$WORK/project"
tmux split-window -h -t "$SESSION:run" -c "$WORK/project" "printf 'Running integration suite… (esc to interrupt)\n'; sleep 100000"
for w in edit plan; do type_in "$w" "$(pane_env "$w")"; done
sleep 1
"$MUX" init "$SESSION" >/dev/null 2>&1 || true
start_daemon
kill -0 "$DPID" 2>/dev/null && ok "scratch daemon running" || fail "scratch daemon started"

# ── A: launch + exclusion surfaces ───────────────────────────────
echo "-- A: launch and exclusion surfaces"
type_in edit "muxcode agent launch edit"
type_in plan "muxcode agent launch plan"
wait_for 15 "edit: fresh launch carries the definition and skip-perms" stub_reports edit "agent=true agents=true resume=false skip-perms=true"
wait_for 15 "plan: fresh launch up" stub_reports plan "agent=true agents=true resume=false skip-perms=true"
wait_for 15 "edit: inbox listener running" listener_alive edit
case "$(check_out webhook)" in *excluded*) ok "webhook stays excluded (agent-health --check)" ;; *) fail "webhook not excluded: $(check_out webhook)" ;; esac
case "$(check_out edit)" in *excluded*) fail "edit still excluded by default: $(check_out edit)" ;; *alive*) ok "edit monitored by default (agent-health --check reads alive)" ;; *) fail "edit --check: $(check_out edit)" ;; esac
h=$(status_health edit)
[ "$h" != excluded ] && [ -n "$h" ] && ok "edit monitored by default (status health=$h)" || fail "edit status health=$h"
out=$(MUXCODE_EDIT_AUTO_RESTART_DISABLE=1 "$MUX" agent-health --check edit 2>/dev/null || true)
case "$out" in *excluded*) ok "opt-out =1 excludes edit on --check" ;; *) fail "opt-out ignored on --check: $out" ;; esac
h=$(MUXCODE_EDIT_AUTO_RESTART_DISABLE=1 status_health edit)
[ "$h" = excluded ] && ok "opt-out =1 excludes edit on status" || fail "opt-out ignored on status: health=$h"
out=$(MUXCODE_EDIT_AUTO_RESTART_DISABLE=1 "$MUX" agent-health --check plan 2>/dev/null || true)
case "$out" in *excluded*) fail "opt-out leaked to plan: $out" ;; *) ok "opt-out excludes edit only (plan still monitored)" ;; esac

# ── B: dead edit resumes with its full flag set ──────────────────
echo "-- B: resume with full flags"
echo "$UUID1" >"$WORK/edit.sid"
P1=$(stub_pid edit)
kill -TERM "$P1"
wait_for 10 "edit: exit banner offers $UUID1" pane_has edit "claude --resume $UUID1"
wait_for 20 "edit: strike-2 agent-down alert sent to edit" bus_alert agent-down
wait_for 20 "edit: restart attempt 1/3" has_event "agent-restart.*edit attempt 1/3"
wait_for 10 "edit: agent-restarting alert sent to edit" bus_alert agent-restarting
wait_for 10 "edit: resume-scrape-hit names $UUID1" has_event "resume-scrape-hit.*edit: session $UUID1"
wait_for 10 "edit: relaunch typed with --resume $UUID1" has_event "agent-relaunch.*edit: muxcode agent launch edit --reason restart --resume $UUID1"
if line_before "resume-scrape-hit.*edit" "agent-relaunch.*edit"; then
  ok "edit: scrape recorded before the relaunch"
else
  fail "edit: relaunch row precedes the scrape row"
fi
[ -z "$(events 'resume-scrape-(miss|stale).*edit')" ] && ok "edit: no stale/miss fallback (a post-relaunch scrape would read stale)" \
  || fail "edit: scrape fell back: $(events 'resume-scrape-(miss|stale).*edit' | head -1)"
wait_for 20 "edit: new stub up" stub_up edit "$P1"
wait_for 10 "edit: stub reports resume of $UUID1 with skip-perms" stub_reports edit "resume=true skip-perms=true resume-id=$UUID1"
flag_check edit "--resume $UUID1"
flag_check edit "--dangerously-skip-permissions"
flag_check edit "--agent[[:space:]]code-editor"
flag_check edit "--agents"
flag_check edit "--allowedTools"
flag_check edit "--append-system-prompt"
wait_for 20 "edit: agent-recovered after the resume" has_event "agent-recovered.*edit"
wait_for 15 "edit: listener re-established" listener_alive edit

# ── C: no scrapeable id → fresh flagged launch ──────────────────
echo "-- C: no scrapeable id"
: >"$WORK/edit.sid"
P2=$(stub_pid edit)
kill -TERM "$P2"
wait_for 10 "edit: exit banner offers only a malformed id" pane_has edit "claude --resume 0f3a"
wait_for 25 "edit: resume-scrape-miss recorded" has_event "resume-scrape-miss.*edit"
wait_for 10 "edit: restart attempt 2/3" has_event "agent-restart.*edit attempt 2/3"
# The relaunch row lands after the interrupt delay, behind the attempt row.
for _ in 1 2 3 4 5 6 7 8 9 10; do event_count_ge "agent-relaunch.*edit" 2 && break; sleep 1; done
last=$(events "agent-relaunch.*edit" | tail -1)
case "$last" in
  *"muxcode agent launch edit"*--resume*) fail "edit: fallback relaunch still resumes: $last" ;;
  *"muxcode agent launch edit"*) ok "edit: fallback relaunch is a plain agent launch" ;;
  *) fail "edit: no fallback relaunch row: $last" ;;
esac
wait_for 20 "edit: fallback stub up" stub_up edit "$P2"
wait_for 10 "edit: fallback reports fresh launch with skip-perms" stub_reports edit "resume=false skip-perms=true resume-id=$"
argv=$(stub_argv edit)
case "$argv" in *--resume*) fail "edit: flagless-resume guard — fallback argv carries --resume" ;; *) ok "edit: fallback argv carries no --resume" ;; esac
case "$argv" in *--dangerously-skip-permissions*--allowedTools*) ok "edit: fallback argv keeps the full flag set" ;; *) fail "edit: fallback argv lost its flags" ;; esac
wait_for 20 "edit: recovered after the fallback" event_count_ge "agent-recovered.*edit" 2

# ── D: alive agents are never struck ─────────────────────────────
echo "-- D: busy and alive agents"
if quiet_for 15 edit; then ok "edit: idle live agent not struck over 5 sweeps"; else fail "edit: live agent was struck"; fi
[ -z "$(events 'agent-health-fail.*run')" ] && ok "run: busy pane never struck" || fail "run: busy pane struck: $(events 'agent-health-fail.*run' | head -1)"
[ -z "$(events 'agent-health-fail.*plan')" ] && ok "plan: idle live agent never struck" || fail "plan: live agent struck"

# ── E: stop and reload markers suppress, lifting them restarts ───
echo "-- E: stop and reload markers"
echo "$UUID2" >"$WORK/edit.sid"
"$MUX" agent-health --stop edit >/dev/null
P3=$(stub_pid edit)
kill -TERM "$P3"
wait_for 10 "edit: exit banner offers $UUID2" pane_has edit "claude --resume $UUID2"
if quiet_for 15 edit; then ok "edit: stop marker suppresses the restart"; else fail "edit: restarted through the stop marker"; fi
case "$(check_out edit)" in *"intentionally stopped"*) ok "edit: --check reports stopped" ;; *) fail "edit --check: $(check_out edit)" ;; esac
mkdir -p "$BUSDIR/lock"
touch "$BUSDIR/lock/edit.reloading"
"$MUX" agent-health --start edit >/dev/null
if quiet_for 15 edit; then ok "edit: reload marker suppresses the restart"; else fail "edit: restarted through the reload marker"; fi
case "$(check_out edit)" in *excluded*) ok "edit: --check reports excluded while reloading" ;; *) fail "edit --check: $(check_out edit)" ;; esac
rm -f "$BUSDIR/lock/edit.reloading"
wait_for 25 "edit: markers lifted → restart attempt 3/3" has_event "agent-restart.*edit attempt 3/3"
wait_for 10 "edit: resumes the newer banner $UUID2 after a fresh launch" has_event "resume-scrape-hit.*edit: session $UUID2"
wait_for 20 "edit: stub reports resume of $UUID2" stub_reports edit "resume=true skip-perms=true resume-id=$UUID2"

# ── F: env opt-out on the daemon, with plan as the live counterexample ─
echo "-- F: MUXCODE_EDIT_AUTO_RESTART_DISABLE on the daemon"
stop_daemon
MUXCODE_EDIT_AUTO_RESTART_DISABLE=1 start_daemon
kill -0 "$DPID" 2>/dev/null && ok "opt-out daemon running" || fail "opt-out daemon started"
echo "$UUID4" >"$WORK/edit.sid"
echo "$UUID3" >"$WORK/plan.sid"
edit_before=$(event_count "(agent-health-fail|agent-restart|resume-scrape-[a-z]+|agent-relaunch).*edit")
kill -TERM "$(stub_pid edit)"
kill -TERM "$(stub_pid plan)"
wait_for 30 "plan: restarted by the opt-out daemon" has_event "agent-restart.*plan attempt 1/3"
wait_for 10 "plan: resume-scrape-hit names $UUID3" has_event "resume-scrape-hit.*plan: session $UUID3"
wait_for 20 "plan: stub reports resume of $UUID3 with skip-perms" stub_reports plan "resume=true skip-perms=true resume-id=$UUID3"
flag_check plan "--dangerously-skip-permissions"
sleep 6
edit_after=$(event_count "(agent-health-fail|agent-restart|resume-scrape-[a-z]+|agent-relaunch).*edit")
[ "$edit_after" -eq "$edit_before" ] && ok "edit: opt-out daemon never probed or restarted it" || fail "edit: opt-out daemon acted on edit ($edit_before → $edit_after)"
[ -z "$(stub_pid edit)" ] && ok "edit: pane left at the shell under the opt-out" || fail "edit: an agent came back under the opt-out"
stop_daemon
start_daemon
kill -0 "$DPID" 2>/dev/null && ok "default daemon running again" || fail "default daemon restarted"
wait_for 30 "edit: default daemon restarts it (negative control of the opt-out)" has_event "resume-scrape-hit.*edit: session $UUID4"
wait_for 20 "edit: resumed $UUID4 with skip-perms" stub_reports edit "resume=true skip-perms=true resume-id=$UUID4"

# ── G: manual `muxcode resume`, daemon held off ──────────────────
echo "-- G: muxcode resume"
stop_daemon
manual_events() { "$MUX" lifecycle show "$SESSION" --source manual --limit 0 2>/dev/null | grep -E -- "$1" || true; }
# as_user runs muxcode with no agent identity, as a person at a shell would.
as_user() { env -u AGENT_ROLE -u BUS_ROLE "$MUX" "$@"; }

P5=$(stub_pid edit)
if out=$(as_user resume edit 2>&1); then
  fail "resume of a live edit succeeded: $out"
else
  case "$out" in *"is running"*--force*) ok "live edit refused without --force" ;; *) fail "live edit refusal unclear: $out" ;; esac
fi
[ "$(stub_pid edit)" = "$P5" ] && ok "live edit untouched by the refusal" || fail "live edit's agent changed after a refusal"
MANUAL_ROWS="resume-scrape-[a-z]+|agent-relaunch"
[ -z "$(manual_events "$MANUAL_ROWS")" ] && ok "refusal typed nothing (no manual rows)" || fail "refusal left manual rows: $(manual_events "$MANUAL_ROWS" | head -1)"

P6=$(stub_pid plan)
if out=$(MUXCODE_PLAN_CLI=opencode as_user resume plan 2>&1); then
  fail "resume of a non-Claude plan succeeded: $out"
else
  case "$out" in *opencode*"muxcode reload plan"*) ok "non-Claude plan refused, naming reload" ;; *) fail "non-Claude refusal unclear: $out" ;; esac
fi
[ "$(stub_pid plan)" = "$P6" ] && [ -z "$(manual_events "($MANUAL_ROWS).*plan")" ] && ok "non-Claude refusal typed nothing" || fail "non-Claude refusal touched plan"

echo "$UUID5" >"$WORK/edit.sid"
kill -TERM "$P5"
wait_for 10 "edit: exit banner offers $UUID5" pane_has edit "claude --resume $UUID5"
if out=$(as_user resume edit 2>&1); then ok "muxcode resume edit exits 0"; else fail "muxcode resume edit failed: $out"; fi
[ -n "$(manual_events "resume-scrape-hit.*edit: session $UUID5 \(by user\)")" ] && ok "manual scrape-hit row names $UUID5 and the user" \
  || fail "no manual scrape-hit row for $UUID5: $(manual_events "$MANUAL_ROWS" | tail -2)"
[ -n "$(manual_events "agent-relaunch.*muxcode agent launch edit --reason resume --resume $UUID5 \(by user\)")" ] && ok "manual relaunch row resumes $UUID5 by the user" \
  || fail "no manual relaunch row for $UUID5"
[ ! -e "$BUSDIR/lock/edit.reloading" ] && ok "reload marker released after the resume" || fail "reload marker outlived muxcode resume"
wait_for 20 "edit: manually resumed stub up" stub_up edit "$P5"
wait_for 10 "edit: stub reports resume of $UUID5 with skip-perms" stub_reports edit "resume=true skip-perms=true resume-id=$UUID5"
flag_check edit "--resume $UUID5"
flag_check edit "--dangerously-skip-permissions"
flag_check edit "--agent[[:space:]]code-editor"
flag_check edit "--agents"
flag_check edit "--allowedTools"
flag_check edit "--append-system-prompt"

echo "=== $PASS passed, $FAIL failed ==="
[ "$PASS" -ge 77 ] || { echo "FAIL: coverage floor not met ($PASS < 77)"; exit 1; }
[ "$FAIL" -eq 0 ] || exit 1
echo "PASS"
