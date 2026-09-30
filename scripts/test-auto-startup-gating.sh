#!/usr/bin/env bash
# Integration test for MUX-141 — only a user-initiated launch seeds the auto
# agent's Jira startup task; every other launch seeds the context-restoration
# startup instead, and a run the agent then starts names what triggered it.
#
# Hermetic: scratch bus + private tmux server + scratch daemon, a muxcode
# built from THIS tree (the installed binary is never run), and
# scripts/fixtures/claude-stub built into the scratch bin as `claude`, so the
# real launcher runs PreLaunchSetup and execs the stub with the real flag set.
# The real agents/autonomous-agent.md is installed, so the definition delivered
# at launch is the one the payloads must agree with.
#
# Scope: launcher and message delivery. The stub drains its inbox and prints
# prompts; it never interprets the definition, searches Jira or starts a run.
# So this script proves which startup each launch road seeds, that it reaches
# the live agent, and what text the agent is launched with — not how a real
# agent acts on it. Agent behaviour (a restored agent stays idle, a user-started
# one searches Jira) is outstanding: no hermetic check here can observe it.
#
# Sections, each read from the bus log (log.jsonl), the lifecycle `launch` row
# and the agent's own inbox:
#   (A) mode cycle — `mode switch auto` creates the hold window and launches
#       with --reason mode-cycle: a context-restoration startup, no task
#   (B) reload — `muxcode reload auto`: the same, reason reload
#   (C) omitted reason — a bare `muxcode agent launch auto`: the same, the
#       lifecycle row reading reason=unset
#   (D) daemon auto-restart — the stub is killed and the daemon relaunches it
#       with --reason restart: the same
#   (E) provenance — a run started as auto after the restart reads its
#       trigger as the startup of a restart, marked not established
#   (F) regression control — `--reason user` seeds the Jira task, the live
#       agent's listener consumes it with a receipt, and the definition in the
#       launch argv contains both payload openings, so payload and definition
#       text still agree
#   (G) negative control — MUXCODE_AUTO_STARTUP_TASK=0 with --reason user
#       seeds no task
# A coverage floor keeps a skipped section from reporting green.
# Requires go, tmux, jq.
set -euo pipefail

PASS=0
FAIL=0
for t in tmux go jq; do
  command -v "$t" >/dev/null 2>&1 || { echo "SKIP: $t is required"; exit 2; }
done

REPO=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
WORK=$(mktemp -d /tmp/auto-startup-XXXXXX)
SESSION="auto-startup-$$"
BUSDIR="/tmp/muxcode-bus-$SESSION"
LOG="$BUSDIR/log.jsonl"

# The script is itself run by an agent: an inherited AGENT_ROLE would reach
# every pane the private server creates and the launched auto agent would read
# that role's inbox instead of its own.
unset AGENT_ROLE BUS_ROLE TMUX MUXCODE_AUTO_STARTUP_TASK
export BUS_SESSION="$SESSION"
export HOME="$WORK/home"
export TMUX_TMPDIR="$WORK/tmux"
export MUXCODE_LIFECYCLE_LOG_DIR="$WORK/lifecycle"
export MUXCODE_INSTALL_DIR="$WORK/install"
export MUXCODE_AGENT_CLI=claude MUXCODE_AUTO_CLI=claude
export MUXCODE_AGENT_HEALTH_CHECK_SECS=3 MUXCODE_DEFINITION_CHECK_SECS=3 MUXCODE_AGENT_HEARTBEAT=86400
export MUXCODE_EDIT_AUTO_RESTART_DISABLE=1
export MUXCODE_CONTROL_PANE_DISABLE=1 MUXCODE_FORCE_RESPOND_DISABLE=1 MUXCODE_AGENTDEFS_WATCH_DISABLE=1
export MUXCODE_TMP_CLEANUP_THRESHOLD=0 MUXCODE_ACTIVE_WATCHDOG_SECS=0 MUXCODE_PROMPT_AGENT_DISABLE=1
export MUXCODE_BRANCH_TIME_DISABLE=1 MUXCODE_STUCK_RELOAD_DISABLE=1
export PATH="$WORK/bin:$PATH"
mkdir -p "$WORK/bin" "$WORK/home" "$WORK/tmux" "$WORK/install/agents" "$WORK/project"

DPID=""
# cleanup reaps the daemon before removing the tree and never fails the exit
# status. Only the private tmux server is killed.
cleanup() {
  set +e
  stop_daemon
  tmux kill-server 2>/dev/null
  sleep 1
  rm -rf "$BUSDIR" "$WORK" 2>/dev/null || { sleep 2; rm -rf "$BUSDIR" "$WORK" 2>/dev/null; }
  true
}
trap cleanup EXIT

ok()   { PASS=$((PASS + 1)); echo "  ok: $1"; }
fail() { FAIL=$((FAIL + 1)); echo "  FAIL: $1"; }
check() { local desc=$1; shift; if "$@" >/dev/null 2>&1; then ok "$desc"; else fail "$desc"; fi; }

events()      { "$MUX" lifecycle show "$SESSION" --limit 0 2>/dev/null | grep -E -- "$1" || true; }
event_count() { events "$1" | wc -l | tr -d ' '; }
has_event()   { [ -n "$(events "$1")" ]; }
pane_pid()    { tmux display-message -p -t "$SESSION:$1.1" '#{pane_pid}'; }
stub_pid()    { pgrep -P "$(pane_pid "$1" 2>/dev/null || echo 0)" -x claude 2>/dev/null | head -1 || true; }
stub_argv()   { local p; p=$(stub_pid "$1"); [ -n "$p" ] && ps -ww -o command= -p "$p" 2>/dev/null || true; }
stub_up()     { local p; p=$(stub_pid "$1"); [ -n "$p" ] && [ "$p" != "${2:-}" ]; }
stub_gone()   { [ -z "$(stub_pid "$1")" ]; }
type_in()     { tmux send-keys -t "$SESSION:$1.1" -l -- "$2"; sleep 0.1; tmux send-keys -t "$SESSION:$1.1" Enter; }

# The auto agent's startup requests, oldest first, as the bus log holds them.
auto_startups() { jq -c 'select(.to == "auto" and .type == "request" and .action == "startup")' "$LOG" 2>/dev/null || true; }
last_startup()  { auto_startups | tail -1; }
startup_field() { last_startup | jq -r "$1"; }
task_count()    { auto_startups | jq -s '[.[] | select(.payload | startswith("Agent started —"))] | length'; }
# startup_shape — "<from>|<first three words of the payload>" of the last startup.
startup_shape() { last_startup | jq -r '"\(.from)|\(.payload | split(" ")[0:3] | join(" "))"'; }
launch_rows_are() { [ "$(event_count "launch.*role=auto cli=claude reason=$1")" -eq "$2" ]; }
window_exists() { tmux list-windows -t "$SESSION" -F '#W' 2>/dev/null | grep -qx "$1"; }
# startup_consumed — the agent's own listener took the startup out of its inbox.
startup_consumed() { ! jq -e 'select(.action == "startup")' "$BUSDIR/inbox/auto.jsonl" >/dev/null 2>&1; }
receipt_acked() {
  local s
  s=$(jq -r '.status' "$BUSDIR/delivery/$1.status" 2>/dev/null || true)
  [ "$s" = acked ] || [ "$s" = responded ]
}
# kill_auto — SIGTERM the live stub (its teardown leaves a bare shell) and
# print its pid; prints nothing when no stub is up, so the caller's wait fails.
kill_auto() {
  local p
  p=$(stub_pid auto)
  [ -n "$p" ] && kill -TERM "$p" 2>/dev/null
  echo "$p"
}

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
  echo "  [pane auto]"; tmux capture-pane -p -t "$SESSION:auto.1" -S -12 2>/dev/null | grep -v '^$' | tail -8 | sed 's/^/    /'
  echo "  [auto startups]"; auto_startups | tail -3 | sed 's/^/    /'
  echo "  [lifecycle tail]"; "$MUX" lifecycle show "$SESSION" 2>/dev/null | tail -8 | sed 's/^/    /'
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

# restores_context LABEL REASON — the launch just made carried REASON and
# seeded the context-restoration startup, no task, and the live agent's
# listener consumed it. REASON "unset" means the startup carries no
# launch_reason.
restores_context() {
  local label=$1 reason=$2 want_field=$2
  [ "$reason" = unset ] && want_field=""
  check "$label: startup carries launch_reason '${want_field:-<none>}'" \
    test "$(startup_field '.launch_reason // ""')" = "$want_field"
  check "$label: startup is the self-addressed context restoration" \
    test "$(startup_shape)" = "auto|Session started —"
  check "$label: no Jira task seeded" test "$(task_count)" -eq 0
  wait_for 15 "$label: the agent's listener consumed the startup" startup_consumed
}

echo "=== MUX-141 auto startup gating integration test ==="

(cd "$REPO/tools/muxcode" && go build -buildvcs=false -o "$WORK/bin/muxcode" .) \
  || { echo "SKIP: go build muxcode failed"; exit 2; }
(cd "$REPO/scripts/fixtures/claude-stub" && go build -buildvcs=false -o "$WORK/bin/claude" .) \
  || { echo "SKIP: go build claude-stub failed"; exit 2; }
MUX="$WORK/bin/muxcode"
ok "scratch muxcode and claude-stub built from this tree"

cp "$REPO/agents/autonomous-agent.md" "$WORK/install/agents/autonomous-agent.md"
printf -- '---\ndescription: Editor\n---\nEdit code.\n' >"$WORK/install/agents/code-editor.md"
cat >"$WORK/hold.json" <<'EOF'
{"name": "hold", "start": "gate",
 "nodes": [{"id": "gate", "type": "wait_human", "message": "hold for the test"}],
 "edges": []}
EOF

# Every shell the private server starts is a non-login bash with no rc files:
# no profile can reorder PATH ahead of the scratch bin, and its "$" prompt
# reads as a bare shell once the stub exits.
tmux new-session -d -s "$SESSION" -n edit -x 160 -y 40 -c "$WORK/project" "/bin/bash --noprofile --norc"
tmux set-option -g default-command "/bin/bash --noprofile --norc"
tmux split-window -h -t "$SESSION:edit" -c "$WORK/project"
"$MUX" init "$SESSION" >/dev/null 2>&1 || true
start_daemon
kill -0 "$DPID" 2>/dev/null && ok "scratch daemon running" || fail "scratch daemon started"

# ── A: first mode cycle to auto ──────────────────────────────────
echo "-- A: mode cycle"
"$MUX" mode switch auto --window edit >/dev/null 2>&1 || true
wait_for 15 "mode-cycle: auto hold window created" window_exists auto
wait_for 20 "mode-cycle: launch recorded with reason=mode-cycle" has_event "launch.*role=auto cli=claude reason=mode-cycle"
wait_for 20 "mode-cycle: auto agent up" stub_up auto
restores_context "mode-cycle" mode-cycle

# ── B: reload ────────────────────────────────────────────────────
echo "-- B: reload"
P=$(stub_pid auto)
"$MUX" reload auto >"$WORK/reload.out" 2>&1 && ok "reload auto exits 0" || fail "reload auto failed: $(tail -2 "$WORK/reload.out")"
wait_for 20 "reload: launch recorded with reason=reload" has_event "launch.*role=auto cli=claude reason=reload"
wait_for 20 "reload: auto agent back up" stub_up auto "$P"
restores_context "reload" reload

# ── C: omitted reason ────────────────────────────────────────────
echo "-- C: omitted reason"
"$MUX" agent-health --stop auto >/dev/null 2>&1 || true
P=$(kill_auto)
wait_for 10 "omitted: agent exited" stub_gone auto
type_in auto "muxcode agent launch auto"
wait_for 20 "omitted: launch recorded with reason=unset" has_event "launch.*role=auto cli=claude reason=unset"
wait_for 20 "omitted: auto agent up" stub_up auto "$P"
restores_context "omitted" unset

# ── D: daemon auto-restart ───────────────────────────────────────
echo "-- D: daemon auto-restart"
"$MUX" agent-health --start auto >/dev/null 2>&1 || true
P=$(kill_auto)
wait_for 30 "restart: daemon restart attempt recorded" has_event "agent-restart.*auto attempt 1/3"
wait_for 15 "restart: relaunch typed with --reason restart" has_event "agent-relaunch.*auto: muxcode agent launch auto --reason restart"
wait_for 20 "restart: launch recorded with reason=restart" has_event "launch.*role=auto cli=claude reason=restart"
wait_for 20 "restart: auto agent back up" stub_up auto "$P"
restores_context "restart" restart
RESTORE_OPENING=$(startup_field '.payload' | cut -d' ' -f1-3)

# ── E: provenance of a run started after the restart ─────────────
echo "-- E: run trigger after a restart"
RID=$(AGENT_ROLE=auto BUS_ROLE=auto "$MUX" graph run --file "$WORK/hold.json" 2>&1 | grep -o 'Started run [^ ]*' | awk '{print $3}' || true)
check "provenance: a run started as auto (${RID:-none})" test -n "$RID"
RJ="$BUSDIR/graphs/$RID/run.json"
check "provenance: run.json records trigger startup, launch reason restart, inferred" \
  jq -e '.trigger == "startup" and .trigger_detail == "restart" and .trigger_inferred == true' "$RJ"
STATUS=$("$MUX" graph status "$RID" 2>/dev/null | sed $'s/\x1b\\[[0-9;]*[A-Za-z]//g' | grep 'Triggered by:' || true)
check "provenance: graph status names the restart's startup" \
  bash -c '[[ "$1" == *"its startup message (launch reason: restart)"* ]]' _ "$STATUS"
check "provenance: graph status says the trigger is not established" \
  bash -c '[[ "$1" == *"not established"* ]]' _ "$STATUS"
check "provenance: graph-run-created names the trigger" has_event "graph-run-created.*$RID.*triggered by: not established.*launch reason: restart"
AGENT_ROLE=auto BUS_ROLE=auto "$MUX" graph cancel "$RID" >/dev/null 2>&1 || true

# ── F: regression control — a user start still seeds the task ───
echo "-- F: user-initiated launch"
"$MUX" agent-health --stop auto >/dev/null 2>&1 || true
P=$(kill_auto)
wait_for 10 "user: agent exited" stub_gone auto
type_in auto "muxcode agent launch auto --reason user"
wait_for 20 "user: launch recorded with reason=user" launch_rows_are user 1
wait_for 20 "user: auto agent up" stub_up auto "$P"
check "user: startup carries launch_reason user" test "$(startup_field '.launch_reason // ""')" = user
check "user: startup is the Jira task, sent as edit" test "$(startup_shape)" = "edit|Agent started —"
check "user: startup asks for the Jira search" bash -c '[[ "$1" == *"search Jira"* ]]' _ "$(startup_field '.payload')"
check "user: exactly one task seeded across every launch" test "$(task_count)" -eq 1
TASK_ID=$(startup_field '.id')
wait_for 15 "user: the agent's listener consumed the task" startup_consumed
wait_for 10 "user: the task carries an acked receipt" receipt_acked "$TASK_ID"
TASK_OPENING=$(startup_field '.payload' | cut -d' ' -f1-3)
ARGV=$(stub_argv auto)
check "user: the definition text in the launch argv contains the task payload's opening ($TASK_OPENING)" \
  bash -c '[[ "$1" == *"$2"* ]]' _ "$ARGV" "$TASK_OPENING"
check "user: the definition text in the launch argv contains the restore payload's opening ($RESTORE_OPENING)" \
  bash -c '[[ "$1" == *"$2"* ]]' _ "$ARGV" "$RESTORE_OPENING"
check "user: the definition text in the launch argv contains the Jira-search instruction" bash -c '[[ "$1" == *"Immediately search Jira"* ]]' _ "$ARGV"

# ── G: negative control — the opt-out ────────────────────────────
echo "-- G: MUXCODE_AUTO_STARTUP_TASK=0"
P=$(kill_auto)
wait_for 10 "opt-out: agent exited" stub_gone auto
type_in auto "MUXCODE_AUTO_STARTUP_TASK=0 muxcode agent launch auto --reason user"
wait_for 20 "opt-out: second user launch recorded" launch_rows_are user 2
wait_for 20 "opt-out: auto agent up" stub_up auto "$P"
check "opt-out: startup still carries launch_reason user" test "$(startup_field '.launch_reason // ""')" = user
check "opt-out: startup is the context restoration, not the task" test "$(startup_shape)" = "auto|Session started —"
check "opt-out: no further task seeded" test "$(task_count)" -eq 1
wait_for 15 "opt-out: the agent's listener consumed the startup" startup_consumed

echo "=== $PASS passed, $FAIL failed ==="
[ "$PASS" -ge 55 ] || { echo "FAIL: coverage floor not met ($PASS < 55)"; exit 1; }
[ "$FAIL" -eq 0 ] || exit 1
echo "PASS"
