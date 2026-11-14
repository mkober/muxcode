#!/usr/bin/env bash
# Integration test for MUX-006 — `muxcode diagnose` must never call a dead
# agent healthy.
#
# On 2026-08-13 the plan agent died and `muxcode diagnose plan` printed
# "State: dead" in its evidence, then "✅ No issues detected", exit 0: no
# detector read AgentState.IsAlive. checkAgentDown (bus/diagnose.go) is that
# detector — critical, so the exit code and the --all summary carry it.
#
# Hermetic: scratch bus, private tmux server, scratch daemon, muxcode and
# scripts/fixtures/claude-stub built from THIS tree, agent definitions in a
# scratch install dir, every command run from an empty scratch project. The
# build agent is a real launched claude-stub; its death is a SIGTERM, after
# which the stub tears its screen down like Claude's TUI and leaves the pane at
# a shell prompt — the evidence the liveness probe reads. The restart is the
# relaunch diagnose names (`muxcode resume`), run before the daemon's own
# health sweep reaches its third strike; first-sighting restarts are off so a
# dead agent stays dead for the strike window the diagnosis runs inside.
#
# The pre-fix build is this tree with checkAgentDown patched out of
# diagnosticChecks in a scratch copy; every other line is the tree's own.
#
# That restarts are off is checked, not assumed: the scratch daemon's own
# environment must hold MUXCODE_RESUME_FIRST_SIGHTING=0 and the 8s interval as
# two variables. Until MUX-221 the export line fused them into one, so neither
# took effect and the strike window was a race this script happened to win; a
# decoy started with that fused line is the check's negative control.
#
# Sections:
#   A  a live, idle agent with an empty inbox: exit 0, no findings, "No issues
#      detected", and its --all row healthy
#   B  negative control — an intentionally stopped agent (agent-health --stop)
#      is down on purpose: no agent-down finding
#   C  the same agent, its stop marker cleared, is a crash: exit 1, a leading
#      critical agent-down finding naming it dead, `muxcode resume build` and
#      never the no-op `agent-health --start`, the inbox's survival and the
#      requests queued while it was down; its --all row is dead and critical
#      and --all exits 1
#   D  the named remediation, run as written, relaunches it: the two requests
#      queued while it was dead are delivered to the new agent, and diagnose is
#      back to exit 0 and clean
#   E  the 2026-08-13 verdict reproduced on the pre-fix build: a crashed agent
#      diagnosed "No issues detected", exit 0 — while this build, on the same
#      pane at the same moment, exits 1 naming it dead
# A coverage floor keeps a skipped section from reporting green.
# Requires go, tmux, jq.
set -uo pipefail

PASS=0
FAIL=0
EXPECTED_PASS=38
for t in tmux go jq; do
  command -v "$t" >/dev/null 2>&1 || { echo "SKIP: $t is required"; exit 2; }
done

REPO=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
WORK=$(mktemp -d /tmp/diagnose-dead-XXXXXX)
SESSION="diagnose-dead-$$"
. "$REPO/scripts/lib/muxcode-bus-dir.sh"
BUSDIR=""

# The script is itself run by an agent: an inherited AGENT_ROLE would reach
# every pane the private server creates.
unset AGENT_ROLE BUS_ROLE TMUX MUXCODE_CONFIG
export BUS_SESSION="$SESSION"
export HOME="$WORK/home"
export TMUX_TMPDIR="$WORK/tmux"
export MUXCODE_LIFECYCLE_LOG_DIR="$WORK/lifecycle"
export MUXCODE_INSTALL_DIR="$WORK/install"
export MUXCODE_AGENT_CLI=claude MUXCODE_BUILD_CLI=claude
export MUXCODE_AGENT_HEALTH_CHECK_SECS=8 MUXCODE_RESUME_FIRST_SIGHTING=0 MUXCODE_AGENT_HEARTBEAT=86400
export MUXCODE_AGENTDEFS_WATCH_DISABLE=1 MUXCODE_EDIT_AUTO_RESTART_DISABLE=1 MUXCODE_CONTROL_PANE_DISABLE=1
export MUXCODE_FORCE_RESPOND_DISABLE=1 MUXCODE_TMP_CLEANUP_THRESHOLD=0 MUXCODE_ACTIVE_WATCHDOG_SECS=0
export MUXCODE_PROMPT_AGENT_DISABLE=1 MUXCODE_BRANCH_TIME_DISABLE=1 MUXCODE_STUCK_RELOAD_DISABLE=1
export MUXCODE_DEDUP_WINDOW=0
export PATH="$WORK/bin:$PATH"
mkdir -p "$WORK/bin" "$WORK/bin-pre" "$WORK/home" "$WORK/tmux" "$WORK/lifecycle" "$WORK/install/agents" "$WORK/project"
LIFELOG="$MUXCODE_LIFECYCLE_LOG_DIR/$SESSION.log"

DPID=""
cleanup() {
  if [ -n "$DPID" ]; then kill "$DPID" 2>/dev/null; wait "$DPID" 2>/dev/null; fi
  tmux kill-server 2>/dev/null
  sleep 1
  rm -rf ${BUSDIR:+"$BUSDIR"} "$WORK" 2>/dev/null
  true
}
trap cleanup EXIT

ok()   { PASS=$((PASS + 1)); echo "  ok: $1"; }
fail() { FAIL=$((FAIL + 1)); echo "  FAIL: $1"; }

pane_pid()       { tmux display-message -p -t "$SESSION:$1.1" '#{pane_pid}' 2>/dev/null; }
stub_pid()       { pgrep -P "$(pane_pid "$1" || echo 0)" -x claude 2>/dev/null | head -1 || true; }
stub_up()        { local p; p=$(stub_pid "$1"); [ -n "$p" ] && [ "$p" != "${2:-}" ]; }
listener_alive() { local pid; pid=$(cat "$BUSDIR/polling-$1.marker" 2>/dev/null) && [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; }
type_in()        { tmux send-keys -t "$SESSION:$1.1" -l -- "$2"; sleep 0.1; tmux send-keys -t "$SESSION:$1.1" Enter; }
reads_dead()     { [ "$(diag_json "$MUX" "$1" '.agent_state.is_alive')" = false ]; }
launches()       { jq -r 'select(.event == "launch") | .detail' "$LIFELOG" 2>/dev/null | grep -c "role=$1 " || true; }
inbox_empty()    { [ -z "$(AGENT_ROLE=$1 "$MUX" inbox --peek 2>/dev/null | grep 'Message from')" ]; }
acked()          { jq -e '(.acked_at // 0) > 0' "$BUSDIR/delivery/$1.status" >/dev/null 2>&1; }

# diag BIN ROLE [ARGS...] — the stripped report in $OUT, its exit code in $RC.
OUT="" RC=0
diag() {
  local bin=$1
  shift
  "$bin" diagnose "$@" >"$WORK/diag.out" 2>&1
  RC=$?
  OUT=$(sed $'s/\x1b\\[[0-9;]*[A-Za-z]//g' "$WORK/diag.out")
}
# JSON report of BIN for ROLE; jq FILTER answers yes/no.
diag_json() { "$1" diagnose "$2" --json 2>/dev/null | jq -r "$3" 2>/dev/null; }
all_row() { printf '%s\n' "$OUT" | awk -v r="$1" '$2 == r'; }

wait_for() {
  local secs=$1 desc=$2 i=0
  shift 2
  while [ "$i" -lt "$secs" ]; do
    if "$@" >/dev/null 2>&1; then ok "$desc"; return 0; fi
    sleep 1; i=$((i + 1))
  done
  fail "$desc (timeout ${secs}s)"
  echo "  [lifecycle tail]"; jq -r '"\(.ts) \(.event) \(.detail // "")"' "$LIFELOG" 2>/dev/null | tail -8 | sed 's/^/    /'
  return 1
}
# crash ROLE — SIGTERM the stub and wait until the probe reads it dead.
crash() {
  local p
  p=$(stub_pid "$1")
  [ -n "$p" ] && kill -TERM "$p"
  wait_for 15 "$2" reads_dead "$1"
}

echo "=== MUX-006 diagnose names a dead agent ==="

(cd "$REPO/tools/muxcode" && go build -buildvcs=false -o "$WORK/bin/muxcode" .) \
  || { echo "SKIP: go build muxcode failed"; exit 2; }
(cd "$REPO/scripts/fixtures/claude-stub" && go build -buildvcs=false -o "$WORK/bin/claude" .) \
  || { echo "SKIP: go build claude-stub failed"; exit 2; }
MUX="$WORK/bin/muxcode"
BUSDIR="$(muxcode_bus_dir "$MUX" "$SESSION")" || exit 1
ok "scratch muxcode and claude-stub built from this tree"

PRE="$WORK/pre/muxcode"
mkdir -p "$WORK/pre"
cp -R "$REPO/tools/muxcode" "$WORK/pre/"
sed -i.orig '/^var diagnosticChecks = /,/^}/{/^	checkAgentDown,$/d;}' "$PRE/bus/diagnose.go"
if ! sed -n '/^var diagnosticChecks = /,/^}/p' "$PRE/bus/diagnose.go" | grep -q 'checkAgentDown' \
  && grep -q '^	checkAgentDown,$' "$REPO/tools/muxcode/bus/diagnose.go" \
  && (cd "$PRE" && go build -buildvcs=false -o "$WORK/bin-pre/muxcode" .); then
  ok "pre-fix muxcode built: the tree with checkAgentDown patched out of diagnosticChecks"
else
  fail "pre-fix build: the registry patch did not apply or the copy failed to build"
fi
MUX_PRE="$WORK/bin-pre/muxcode"
cd "$WORK/project" || exit 1

printf -- '---\ndescription: Builder\n---\nBuild things.\n' >"$WORK/install/agents/code-builder.md"
pane_env() {
  echo "export AGENT_ROLE=$1 BUS_ROLE=$1 BUS_SESSION=$SESSION HOME=$HOME TMUX_TMPDIR=$TMUX_TMPDIR MUXCODE_LIFECYCLE_LOG_DIR=$MUXCODE_LIFECYCLE_LOG_DIR MUXCODE_INSTALL_DIR=$MUXCODE_INSTALL_DIR MUXCODE_AGENT_CLI=claude MUXCODE_BUILD_CLI=claude PATH=$PATH; PS1='\$ '; clear"
}
tmux new-session -d -s "$SESSION" -n build -x 160 -y 40 -c "$WORK/project" "/bin/bash --noprofile --norc"
tmux set-option -g default-command "/bin/bash --noprofile --norc"
tmux split-window -h -t "$SESSION:build" -c "$WORK/project"
type_in build "$(pane_env build)"
sleep 1
"$MUX" init "$SESSION" >/dev/null 2>&1 || true
(cd "$WORK/project" && exec "$MUX" watch "$SESSION" --poll 2 >>"$WORK/daemon.log" 2>&1) &
DPID=$!
sleep 1
kill -0 "$DPID" 2>/dev/null && ok "scratch daemon running" || fail "scratch daemon exited: $(tail -3 "$WORK/daemon.log")"

# restarts_controlled PID — the process's environment holds the health-check
# interval and first-sighting restarts off as two variables. Read through
# pii-scrub, never a bare environment dump (MUX-179).
restarts_controlled() {
  local env
  env=$(ps eww -p "$1" 2>/dev/null | "$MUX" pii-scrub | tr ' ' '\n')
  grep -qx 'MUXCODE_AGENT_HEALTH_CHECK_SECS=8' <<<"$env" && grep -qx 'MUXCODE_RESUME_FIRST_SIGHTING=0' <<<"$env"
}
restarts_controlled "$DPID" \
  && ok "the scratch daemon runs with first-sighting restarts off and an 8s health check (MUX-221)" \
  || fail "the scratch daemon's environment lacks MUXCODE_RESUME_FIRST_SIGHTING=0 or MUXCODE_AGENT_HEALTH_CHECK_SECS=8"
# Negative control: the fused export line this replaced — one variable whose
# value swallowed the second — must fail the same check.
env MUXCODE_AGENT_HEALTH_CHECK_SECS=8MUXCODE_RESUME_FIRST_SIGHTING=0 sleep 30 &
FUSED=$!
sleep 0.5
restarts_controlled "$FUSED" \
  && fail "control: the fused export line passed — the check above cannot see the defect" \
  || ok "control: the fused export line's environment fails the same check"
kill "$FUSED" 2>/dev/null; wait "$FUSED" 2>/dev/null
type_in build "muxcode agent launch build --reason user"
wait_for 20 "build agent (claude-stub) up" stub_up build
wait_for 15 "build agent's inbox listener running" listener_alive build

# ── A: a live, idle agent is clean ─────────────────────────────────
echo "-- A: a live agent diagnoses clean"
diag "$MUX" build
[ "$RC" = 0 ] && ok "A: diagnose build exits 0" || fail "A: diagnose build exited $RC: $(printf '%s' "$OUT" | grep -E '^\s*(CRITICAL|WARNING|INFO|\[)' | head -3)"
printf '%s' "$OUT" | grep -q "No issues detected" && ok "A: the verdict is 'No issues detected'" \
  || fail "A: verdict: $(printf '%s' "$OUT" | tail -15)"
[ "$(diag_json "$MUX" build '.findings | length')" = 0 ] && ok "A: no findings" \
  || fail "A: findings: $(diag_json "$MUX" build '[.findings[].failure_mode] | join(",")')"
diag "$MUX" --all
all_row build | grep -q 'healthy' && ok "A: the --all row reads healthy" || fail "A: --all row: '$(all_row build)'"

# ── B: an intentional stop is not a fault ──────────────────────────
echo "-- B: an intentionally stopped agent (negative control)"
"$MUX" agent-health --stop build >/dev/null
crash build "B: the stopped agent's process is gone"
for n in 1 2; do
  sent=$(AGENT_ROLE=plan "$MUX" send build report "Report your status MUX006-$n" --no-notify --force 2>&1) \
    || echo "  send $n refused: $sent"
done
MSG_IDS=$(jq -r 'select(.from == "plan" and (.payload | test("MUX006-"))) | .id' "$BUSDIR/inbox/build.jsonl" 2>/dev/null)
[ "$(printf '%s\n' "$MSG_IDS" | grep -c .)" = 2 ] && ok "B: two requests queued for the down agent" \
  || fail "B: queued ids: '$MSG_IDS'; inbox: $(jq -c '{id, from, type, action}' "$BUSDIR/inbox/build.jsonl" 2>/dev/null | tr '\n' ' ')"
[ "$(diag_json "$MUX" build '[(.findings // [])[] | select(.failure_mode == "agent-down")] | length')" = 0 ] \
  && ok "B: no agent-down finding for an agent stopped on purpose" \
  || fail "B: agent-down fired for a stopped agent"
[ "$(diag_json "$MUX" build '.agent_state.is_stopped')" = true ] && ok "B: the report records the stop" \
  || fail "B: is_stopped not recorded"

# ── C: the same death without the stop marker is a crash ───────────
echo "-- C: a crashed agent is critical"
L0=$(launches build)
"$MUX" agent-health --start build >/dev/null
diag "$MUX" build
CRASH_RC=$RC CRASH_OUT=$OUT
JSON=$("$MUX" diagnose build --json 2>/dev/null)
diag "$MUX" --all
ALL_RC=$RC
[ "$(launches build)" = "$L0" ] && reads_dead build && ok "C: diagnosed while still dead (no relaunch yet)" \
  || fail "C: the daemon relaunched build before diagnose ran — the window closed"
[ "$CRASH_RC" = 1 ] && ok "C: diagnose build exits 1" || fail "C: diagnose build exited $CRASH_RC"
[ "$(printf '%s' "$JSON" | jq -r '.findings[0].failure_mode + "/" + .findings[0].severity')" = "agent-down/critical" ] \
  && ok "C: the leading finding is critical agent-down" \
  || fail "C: findings: $(printf '%s' "$JSON" | jq -c '[.findings[] | .failure_mode + "/" + .severity]')"
printf '%s' "$CRASH_OUT" | grep -q "Agent is dead" && ok "C: the report names the agent dead" \
  || fail "C: no 'Agent is dead' in the report"
REMEDY=$(printf '%s' "$CRASH_OUT" | grep -o 'Relaunch: muxcode resume build$' | head -1)
[ -n "$REMEDY" ] && ! printf '%s' "$CRASH_OUT" | grep -q "agent-health --start" \
  && ok "C: remediation names muxcode resume build, not the no-op agent-health --start" \
  || fail "C: remediation: $(printf '%s' "$JSON" | jq -c '.findings[0].remediation')"
printf '%s' "$CRASH_OUT" | grep -q "Inbox is on disk and survives" && ok "C: remediation states the inbox survives" \
  || fail "C: inbox-survival line missing"
QUEUED=$(jq -s '[.[] | select(.type == "request")] | length' "$BUSDIR/inbox/build.jsonl" 2>/dev/null)
echo "  queued requests: $(jq -r 'select(.type == "request") | "\(.from)>\(.action)"' "$BUSDIR/inbox/build.jsonl" 2>/dev/null | tr '\n' ' ')"
[ "${QUEUED:-0}" -ge 2 ] && printf '%s' "$CRASH_OUT" | grep -q "$QUEUED actionable message(s) waiting" \
  && ok "C: the evidence counts the $QUEUED requests waiting" \
  || fail "C: actionable count (inbox holds $QUEUED): $(printf '%s' "$JSON" | jq -c '.findings[0].evidence')"
printf '%s' "$CRASH_OUT" | grep -q "No issues detected" && fail "C: a dead agent was reported 'No issues detected'" \
  || ok "C: the verdict is not 'No issues detected'"
all_row build | grep -q 'dead' && all_row build | grep -q 'critical' && ok "C: the --all row reads dead and critical" \
  || fail "C: --all row: '$(all_row build)'"
[ "$ALL_RC" = 1 ] && ok "C: diagnose --all exits 1" || fail "C: diagnose --all exited $ALL_RC"

# ── D: the named remediation relaunches it; the queued requests survive
echo "-- D: run the remediation — the inbox survives and the verdict clears"
CMD=${REMEDY#Relaunch: muxcode }
# shellcheck disable=SC2086 # the remediation's own words, as a user would type them
[ -n "$REMEDY" ] && "$MUX" $CMD >"$WORK/resume.out" 2>&1 && ok "D: '$REMEDY' ran as written" \
  || fail "D: '$REMEDY' failed: $(cat "$WORK/resume.out")"
# resume resumes the banner's session or, when it cannot read one, launches fresh.
jq -e 'select(.source == "manual" and (.event | test("^agent-re(sume|launch)$")) and (.detail | test("^build")))' "$LIFELOG" >/dev/null 2>&1 \
  && ok "D: the relaunch was the remediation's (a manual agent-resume or agent-relaunch row), not the daemon's" \
  || fail "D: no manual relaunch row: $(jq -c 'select(.event | test("resume|launch")) | {event, source, detail}' "$LIFELOG" 2>/dev/null | tail -3 | tr '\n' ' ')"
wait_for 20 "D: build relaunched" stub_up build
wait_for 15 "D: the new agent's inbox listener running" listener_alive build
wait_for 20 "D: the queued requests were consumed by the new agent" inbox_empty build
n_acked=0
for id in $MSG_IDS; do acked "$id" && n_acked=$((n_acked + 1)); done
[ "$n_acked" = 2 ] && ok "D: both requests queued while dead carry a receipt from the new agent" \
  || fail "D: $n_acked of 2 receipted"
sleep 3
diag "$MUX" build
[ "$RC" = 0 ] && ok "D: diagnose build exits 0 after the restart" \
  || fail "D: exited $RC: $(diag_json "$MUX" build '[.findings[] | .failure_mode + "/" + .severity] | join(",")')"
printf '%s' "$OUT" | grep -q "No issues detected" && ok "D: the verdict is clean again" \
  || fail "D: findings: $(diag_json "$MUX" build '[.findings[].failure_mode] | join(",")')"

# ── E: the 2026-08-13 verdict, reproduced on the pre-fix build ─────
echo "-- E: the pre-fix build calls a dead agent healthy"
L1=$(launches build)
crash build "E: build crashed again"
diag "$MUX_PRE" build
PRE_RC=$RC PRE_OUT=$OUT
diag "$MUX" build
[ "$(launches build)" = "$L1" ] && reads_dead build && ok "E: both builds diagnosed the agent while still dead" \
  || fail "E: the daemon relaunched build before both diagnoses ran"
printf '%s' "$PRE_OUT" | grep -qE 'Health:.*dead|State:.*dead' && ok "E: the pre-fix report's evidence shows the agent dead" \
  || fail "E: pre-fix evidence: $(printf '%s' "$PRE_OUT" | grep -E 'State|Health' | head -2)"
[ "$PRE_RC" = 0 ] && printf '%s' "$PRE_OUT" | grep -q "No issues detected" \
  && ok "E: and still concludes 'No issues detected', exit 0 — the 2026-08-13 report" \
  || fail "E: pre-fix exited $PRE_RC: $(printf '%s' "$PRE_OUT" | tail -6)"
[ "$RC" = 1 ] && printf '%s' "$OUT" | grep -q "Agent is dead" \
  && ok "E: this build, same pane, same moment: exit 1 naming it dead" \
  || fail "E: this build exited $RC"

echo
echo "=== results: $PASS passed, $FAIL failed (floor $EXPECTED_PASS) ==="
if [ "$PASS" -lt "$EXPECTED_PASS" ]; then
  echo "FAIL: coverage floor — $PASS passed, expected at least $EXPECTED_PASS"
  exit 1
fi
[ "$FAIL" = 0 ]
