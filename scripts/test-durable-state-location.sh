#!/usr/bin/env bash
# Integration test for MUX-208: durable bus state lives in the per-user state
# directory, not /tmp, where macOS's tmp_cleaner deletes files untouched for
# three days.
#
# Covers:
#   1. One resolver: `muxcode bus-dir`, the daemon's re-export into the tmux
#      session env, $MUXCODE_BUS_DIR in a launched pane and the daemon
#      monitor's keepalive path agree byte for byte — the pane's value
#      corrected over a stale /tmp one its session env still carries.
#   2. A scratch daemon, a `muxcode send`, a `graph run` and a `spawn start`
#      write under the scratch HOME's state directory and leave nothing
#      session-named under /tmp or $TMPDIR (the search proven able to see a
#      planted probe).
#   3. Simulated cleaner: a run parked on a wait_human gate, every file
#      backdated five days, then the cleaner's `find -type f -mtime +3 -delete`
#      over every /tmp location the session could use — the run still steps,
#      `graph status` and the TUI list it, `graph approve` completes it; a
#      second run, parked and backdated the same way, `graph cancel`s normally.
#   4. Negative control: the same run under MUXCODE_BUS_BASE=/tmp loses
#      graph.json to the same find and the daemon logs graph-step-error.
#   5. Migration: a /tmp/muxcode-bus-<scratch> holding an inbox, a history row
#      and a parked run is moved on daemon start with one bus-dir-migrated row;
#      the inbox still delivers and the run still steps; a second start moves
#      nothing.
#
# The cleaner's find is never run over the whole of /tmp: -delete there would
# remove the developer's own files. It runs over the paths this session's
# state could occupy under /tmp and $TMPDIR, which is the cleaner's entire
# reach over it.
#
# ISOLATION: builds muxcode from the tree into the scratch HOME; scratch HOME
# and XDG_STATE_HOME, so the real ~/.local/state/muxcode is never touched;
# scratch tmux session, lifecycle log and config; automatic /tmp cleanup off.
# The negative-control and migration fixtures create /tmp/muxcode-bus-<name>
# dirs under unique names, removed on exit.
#
# REQUIRES: go, tmux, git. ~1 minute.
#
# Usage: bash scripts/test-durable-state-location.sh
#        MUXCODE_TEST_KEEP=1 bash scripts/test-durable-state-location.sh   # keep WORK
set -uo pipefail

for tool in go tmux git; do
  command -v "$tool" >/dev/null 2>&1 || { echo "SKIP: $tool is required"; exit 2; }
done

REPO_ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
. "$REPO_ROOT/scripts/lib/muxcode-bus-dir.sh"

GREEN=$'\033[0;32m'; RED=$'\033[0;31m'; NC=$'\033[0m'
pass=0; fail=0
ok()  { echo "  ${GREEN}PASS${NC}  $*"; pass=$((pass + 1)); }
bad() { echo "  ${RED}FAIL${NC}  $*"; fail=$((fail + 1)); }
check() { local msg="$1"; shift; if "$@"; then ok "$msg"; else bad "$msg"; fi; }

SESSION="durable-state-$$"
NEG="durable-neg-$$"
MIG="durable-mig-$$"
WORK=$(mktemp -d /tmp/durable-work-XXXXXX)
CTL="$WORK/ctl"
REPO="$WORK/repo"
NBD="/tmp/muxcode-bus-$NEG"
LEG="/tmp/muxcode-bus-$MIG"
TMPROOT="${TMPDIR:-/tmp}"; TMPROOT="${TMPROOT%/}"
DPID=""; NPID=""; MPID=""

cleanup() {
  local p
  for p in $DPID $NPID $MPID; do kill "$p" 2>/dev/null; done
  tmux kill-session -t "$SESSION" 2>/dev/null
  cd / 2>/dev/null
  rm -rf "$NBD" "$LEG" "/tmp/muxcode-probe-$SESSION"
  if [ "${MUXCODE_TEST_KEEP:-0}" = "1" ]; then
    echo "  (kept for diagnosis: $WORK)"
  else
    rm -rf "$WORK"
  fi
}
trap cleanup EXIT

# --- Isolation -------------------------------------------------------------
export HOME="$WORK/home"
export XDG_STATE_HOME="$HOME/.local/state"
STATE="$XDG_STATE_HOME/muxcode"
EXPECT_BD="$STATE/bus/muxcode-bus-$SESSION"
mkdir -p "$HOME/.local/bin" "$HOME/.config/muxcode/agents" "$CTL" "$REPO" "$WORK/lifecycle"

# Built into the scratch HOME's install dir: findMuxcodeBinary checks
# ~/.local/bin first, so a spawn worker launches this binary, not an install.
(cd "$REPO_ROOT/tools/muxcode" && go build -buildvcs=false -o "$HOME/.local/bin/muxcode" .) \
  || { echo "SKIP: go build muxcode failed"; exit 2; }
MUX="$HOME/.local/bin/muxcode"
export PATH="$HOME/.local/bin:$PATH"

unset MUXCODE_BUS_BASE MUXCODE_BUS_DIR AGENT_ROLE BUS_ROLE MUXCODE_SPAWN_MAX_WORKERS
export BUS_SESSION="$SESSION"
# Gate authority reads fixed paths under HOME, never MUXCODE_CONFIG (see test-gate-authority.sh).
echo "MUXCODE_GATE_AUTHORITY_ROLES=test-approver" > "$HOME/.config/muxcode/config"
: > "$WORK/empty-config"
export MUXCODE_CONFIG="$WORK/empty-config"
export MUXCODE_LIFECYCLE_LOG_DIR="$WORK/lifecycle"
export MUXCODE_TMP_CLEANUP_THRESHOLD=0
export MUXCODE_BRANCH_TIME_DISABLE=1
export MUXCODE_DEDUP_WINDOW=0
export MUXCODE_SESSION_REPO_DIR="$REPO"
export MUXCODE_STUCK_RELOAD_DISABLE=1
export MUXCODE_PERMBLOCK_WATCHDOG_DISABLE=1
export MUXCODE_FORCE_RESPOND_DISABLE=1
printf -- '---\ndescription: Fixture worker for test-durable-state-location\n---\nStub.\n' \
  > "$HOME/.config/muxcode/agents/code-researcher.md"

# The stub worker records the MUXCODE_BUS_DIR its launch handed it, then idles at a ❯.
cat > "$WORK/stub-agent" <<STUB
#!/usr/bin/env bash
printf '%s' "\${MUXCODE_BUS_DIR:-<unset>}" > "$CTL/pane-busdir"
while :; do printf '\n❯ '; sleep 1; done
STUB
chmod +x "$WORK/stub-agent"
export MUXCODE_RESEARCH_CLI="$WORK/stub-agent"

git -C "$REPO" init -q && git -C "$REPO" -c user.name=t -c user.email=t@example.invalid commit -q --allow-empty -m init \
  || { echo "  FAIL  could not create the scratch repo"; exit 1; }

cd "$WORK" || exit 1
cat > "$WORK/gate.json" <<'EOF'
{"name": "durable-gate", "start": "gate",
 "nodes": [{"id": "gate", "type": "wait_human", "message": "Approve the parked run"}],
 "edges": []}
EOF

# --- Helpers ---------------------------------------------------------------
plain() { sed $'s/\x1b\\[[0-9;]*[A-Za-z]//g'; }
run_state()  { "$MUX" graph status "$1" 2>/dev/null | plain | head -1 | sed 's/.*\[\([a-z]*\)\].*/\1/'; }
node_state() { "$MUX" graph status "$1" 2>/dev/null | plain | awk -v n="$2" '$1==n {print $2}'; }
start_run()  { AGENT_ROLE=auto "$MUX" graph run --file "$WORK/gate.json" 2>&1 | grep -o 'Started run [^ ]*' | awk '{print $3}'; }
approve()    { AGENT_ROLE=test-approver "$MUX" graph approve "$1" gate >/dev/null 2>&1; }
cancel_run() { AGENT_ROLE=auto "$MUX" graph cancel "$1" >/dev/null 2>&1; }

wait_until() {
  local tries="$1"; shift
  local i
  for i in $(seq 1 "$tries"); do
    "$@" && return 0
    sleep 0.5
  done
  return 1
}
node_is() { [ "$(node_state "$1" "$2")" = "$3" ]; }
run_is()  { [ "$(run_state "$1")" = "$2" ]; }
events()  { grep -c "\"event\":\"$2\"" "$MUXCODE_LIFECYCLE_LOG_DIR/$1.log" 2>/dev/null || true; }
has_event() { [ "$(events "$1" "$2")" -ge 1 ]; }
events_at_least() { [ "$(events "$1" "$2")" -ge "$3" ]; }
alive()   { kill -0 "$1" 2>/dev/null; }
dead()    { ! kill -0 "$1" 2>/dev/null; }
same()    { [ "$1" = "$2" ]; }
file_has() { grep -qF "$2" "$1" 2>/dev/null; }

five_days_ago() { date -v-5d +%Y%m%d%H%M 2>/dev/null || date -d '5 days ago' +%Y%m%d%H%M; }
two_days_ago()  { date -v-2d +%Y%m%d%H%M 2>/dev/null || date -d '2 days ago' +%Y%m%d%H%M; }
backdate() { find "$@" -exec touch -t "$(five_days_ago)" {} + 2>/dev/null; }
tmp_cleaner() { find "$@" -type f -mtime +3 -delete 2>/dev/null; true; }

tmux_env() { tmux show-environment -t "$SESSION" MUXCODE_BUS_DIR 2>/dev/null | sed 's/^MUXCODE_BUS_DIR=//'; }
tmux_env_is() { [ "$(tmux_env)" = "$1" ]; }

# monitor_keepalive [bus-dir] — the path scripts/muxcode-daemon-monitor.sh
# computes, from its own resolver block, with MUXCODE_BUS_DIR set or unset.
monitor_keepalive() {
  local block
  block=$(sed -n '1,/^KEEPALIVE=/p' "$REPO_ROOT/scripts/muxcode-daemon-monitor.sh")
  if [ $# -gt 0 ]; then
    MUXCODE_BUS_DIR="$1" bash -c "$block"$'\nprintf %s "$KEEPALIVE"' _ "$SESSION"
  else
    env -u MUXCODE_BUS_DIR bash -c "$block"$'\nprintf %s "$KEEPALIVE"' _ "$SESSION"
  fi
}

# leaks — session-named paths under /tmp or $TMPDIR newer than the marker,
# minus the scratch WORK and the two files that stay in /tmp by design. find
# exits 1 on other users' unreadable /tmp entries, which pipefail would carry.
leaks() {
  { find /tmp/ /private/tmp/ "$TMPROOT/" -maxdepth 2 -newer "$WORK/marker" -name "*$SESSION*" 2>/dev/null || true; } \
    | sed 's#//*#/#g' \
    | grep -vF "$(basename "$WORK")" \
    | grep -v -e "muxcode-analyze-$SESSION.trigger" -e "muxcode-preview-$SESSION.tmp" \
    | sort -u
}

# --- 1. One resolver -------------------------------------------------------
echo "--- 1. one resolver"
: > "$WORK/marker"
sleep 1
BD="$(muxcode_bus_dir "$MUX" "$SESSION")" || { bad "muxcode bus-dir did not resolve"; BD="$EXPECT_BD"; }
check "muxcode bus-dir resolves under the scratch state dir ($BD)" same "$BD" "$EXPECT_BD"
# The scratch HOME itself sits in /tmp, so this asserts the base is the state
# home, not the legacy namespace; TestBusDirNotUnderTmp pins the real default.
case "$BD" in
  "$XDG_STATE_HOME"/muxcode/bus/*) ok "bus dir is under \$XDG_STATE_HOME, not the legacy /tmp namespace" ;;
  *) bad "bus dir $BD is not under \$XDG_STATE_HOME/muxcode/bus" ;;
esac

tmux new-session -d -s "$SESSION" -n edit -x 200 -y 50
for v in BUS_SESSION HOME XDG_STATE_HOME PATH MUXCODE_CONFIG MUXCODE_LIFECYCLE_LOG_DIR MUXCODE_RESEARCH_CLI \
         MUXCODE_SESSION_REPO_DIR MUXCODE_TMP_CLEANUP_THRESHOLD MUXCODE_BRANCH_TIME_DISABLE MUXCODE_DEDUP_WINDOW; do
  tmux set-environment -t "$SESSION" "$v" "${!v}"
done
for v in MUXCODE_BUS_BASE MUXCODE_BUS_DIR AGENT_ROLE BUS_ROLE; do
  tmux set-environment -t "$SESSION" -r "$v" 2>/dev/null
done
"$MUX" init >/dev/null 2>&1

"$MUX" watch "$SESSION" --poll 2 >"$WORK/daemon.log" 2>&1 &
DPID=$!
check "scratch daemon running (pid $DPID)" wait_until 6 alive "$DPID"
check "daemon re-exported MUXCODE_BUS_DIR into the tmux session env" wait_until 20 tmux_env_is "$BD"
check "daemon keepalive written at \$BD/daemon.keepalive" wait_until 20 test -s "$BD/daemon.keepalive"
check "monitor keepalive path via 'muxcode bus-dir' matches byte for byte" same "$(monitor_keepalive)" "$BD/daemon.keepalive"
check "monitor keepalive path via \$MUXCODE_BUS_DIR matches byte for byte" same "$(monitor_keepalive "$BD")" "$BD/daemon.keepalive"

# --- 2. Activity lands in the state dir ------------------------------------
echo "--- 2. daemon, send, graph run and spawn start write under the state dir"
AGENT_ROLE=edit "$MUX" send build build "durable-state fixture" --no-notify >/dev/null 2>&1
check "muxcode send wrote the inbox under the state dir" file_has "$BD/inbox/build.jsonl" "durable-state fixture"

RID="$(start_run)"
check "graph run started ($RID)" test -n "$RID"
check "run parked on its wait_human gate" wait_until 40 node_is "$RID" gate waiting
check "run's frozen graph.json is under the state dir" test -s "$BD/graphs/$RID/graph.json"

# A session launched before the install: its tmux env names the legacy /tmp dir.
tmux set-environment -t "$SESSION" MUXCODE_BUS_DIR "/tmp/muxcode-bus-$SESSION"
(cd "$REPO" && AGENT_ROLE=edit "$MUX" spawn start research "durable-state spawn task" >"$WORK/spawn.out" 2>&1)
check "spawn start launched a worker" grep -q '^Started spawn: ' "$WORK/spawn.out"
check "the spawn registry is under the state dir" test -s "$BD/spawn.jsonl"
WT=$(find "$STATE/spawn/$SESSION" -mindepth 1 -maxdepth 1 -type d -name 'spawn-*' 2>/dev/null | head -1)
check "the spawn worktree is under the state dir ($WT)" test -e "${WT:-/nonexistent}/.git"
check "launched pane saw a MUXCODE_BUS_DIR" wait_until 40 test -s "$CTL/pane-busdir"
check "pane's \$MUXCODE_BUS_DIR equals muxcode bus-dir byte for byte, over the stale /tmp value" \
  same "$(cat "$CTL/pane-busdir" 2>/dev/null)" "$BD"

touch "/tmp/muxcode-probe-$SESSION"
probe_seen() { leaks | grep -qF "muxcode-probe-$SESSION"; }
check "leak search sees a planted session-named /tmp file (negative control)" probe_seen
rm -f "/tmp/muxcode-probe-$SESSION"
LEAKS="$(leaks)"
if [ -z "$LEAKS" ]; then
  ok "nothing session-named written under /tmp or \$TMPDIR"
else
  bad "session state written under /tmp or \$TMPDIR: $(echo "$LEAKS" | tr '\n' ' ')"
fi
check "no legacy /tmp/muxcode-bus-$SESSION exists" test ! -e "/tmp/muxcode-bus-$SESSION"

# --- 3. Simulated cleaner over the parked run ------------------------------
echo "--- 3. simulated tmp_cleaner"
backdate "$BD" "$STATE/spawn/$SESSION"
check "backdated graph.json qualifies for the cleaner's -mtime +3" \
  bash -c "find '$BD/graphs/$RID' -type f -name graph.json -mtime +3 | grep -q ."
tmp_cleaner "/tmp/muxcode-bus-$SESSION" "/private/tmp/muxcode-bus-$SESSION" \
  "$TMPROOT/muxcode-spawn-$SESSION" "/tmp/muxcode-spawn-$SESSION"
check "graph.json survives the cleaner pass" test -s "$BD/graphs/$RID/graph.json"
sleep 5
check "daemon logged no graph-step-error after the pass" same "$(events "$SESSION" graph-step-error)" 0
check "graph status still lists the run running" run_is "$RID" running
check "its gate is still waiting" node_is "$RID" gate waiting
"$MUX" graph ui --render-once --width 200 2>/dev/null | plain > "$WORK/tui.txt"
check "the graph TUI lists the run" grep -qF "$RID" "$WORK/tui.txt"
check "graph approve accepted" approve "$RID"
check "the run completes after approval" wait_until 40 run_is "$RID" complete

CRID="$(start_run)"
check "a second run parked on its gate ($CRID)" wait_until 40 node_is "$CRID" gate waiting
backdate "$BD/graphs/$CRID"
tmp_cleaner "/tmp/muxcode-bus-$SESSION" "/private/tmp/muxcode-bus-$SESSION" \
  "$TMPROOT/muxcode-spawn-$SESSION" "/tmp/muxcode-spawn-$SESSION"
check "its backdated graph.json survives the cleaner pass" test -s "$BD/graphs/$CRID/graph.json"
check "graph cancel accepted after the pass" cancel_run "$CRID"
check "the run reaches canceled" wait_until 40 run_is "$CRID" canceled

# --- 4. Negative control: the same state under /tmp ------------------------
echo "--- 4. negative control: bus base /tmp"
neg() { MUXCODE_BUS_BASE=/tmp BUS_SESSION="$NEG" "$@"; }
check "MUXCODE_BUS_BASE=/tmp resolves to $NBD" same "$(neg "$MUX" bus-dir "$NEG" 2>/dev/null)" "$NBD"
neg "$MUX" init >/dev/null 2>&1
MUXCODE_BUS_BASE=/tmp "$MUX" watch "$NEG" --poll 2 >"$WORK/neg-daemon.log" 2>&1 &
NPID=$!
NRID="$(neg start_run)"
check "negative-control run parked on its gate" wait_until 40 neg node_is "$NRID" gate waiting
# As in the incident, run.json is younger than graph.json: it was rewritten when the run parked.
backdate "$NBD"
touch -t "$(two_days_ago)" "$NBD/graphs/$NRID/run.json"
tmp_cleaner "$NBD"
check "the cleaner pass deleted its graph.json" test ! -e "$NBD/graphs/$NRID/graph.json"
check "the daemon logs graph-step-error for it" wait_until 40 has_event "$NEG" graph-step-error
kill "$NPID" 2>/dev/null; NPID=""

# --- 5. Migration of a legacy /tmp bus -------------------------------------
echo "--- 5. migration"
# Never resolved through the default base before the daemon starts: BusDir()
# itself migrates on first resolution, which would pre-empt the daemon road.
NEWMIG="$STATE/bus/muxcode-bus-$MIG"
mig() { MUXCODE_BUS_BASE=/tmp BUS_SESSION="$MIG" "$@"; }
mig "$MUX" init >/dev/null 2>&1
mig env AGENT_ROLE=edit "$MUX" send build build "migration fixture inbox" --no-notify >/dev/null 2>&1
mig "$MUX" log build "migration fixture history" --exit-code 0 >/dev/null 2>&1
MUXCODE_BUS_BASE=/tmp "$MUX" watch "$MIG" --poll 2 >"$WORK/mig-legacy-daemon.log" 2>&1 &
MPID=$!
MRID="$(mig start_run)"
check "legacy run parked on its gate under /tmp" wait_until 40 mig node_is "$MRID" gate waiting
kill "$MPID" 2>/dev/null
check "legacy daemon stopped" wait_until 20 dead "$MPID"
MPID=""
check "legacy bus seeded: inbox, history row and run under /tmp" \
  bash -c "grep -qF 'migration fixture inbox' '$LEG/inbox/build.jsonl' && grep -qF 'migration fixture history' '$LEG/build-history.jsonl' && test -s '$LEG/graphs/$MRID/graph.json'"
check "no state-dir bus exists for it yet" test ! -e "$NEWMIG"

BUS_SESSION="$MIG" "$MUX" watch "$MIG" --poll 2 >"$WORK/mig-daemon.log" 2>&1 &
MPID=$!
check "daemon start logged bus-dir-migrated" wait_until 40 has_event "$MIG" bus-dir-migrated
check "exactly one bus-dir-migrated row" same "$(events "$MIG" bus-dir-migrated)" 1
check "the /tmp bus is gone" test ! -e "$LEG"
check "inbox, history row and run moved into the state dir" \
  bash -c "grep -qF 'migration fixture inbox' '$NEWMIG/inbox/build.jsonl' && grep -qF 'migration fixture history' '$NEWMIG/build-history.jsonl' && test -s '$NEWMIG/graphs/$MRID/graph.json'"
check "muxcode bus-dir names the migrated directory" same "$(BUS_SESSION="$MIG" "$MUX" bus-dir "$MIG" 2>/dev/null)" "$NEWMIG"
BUS_SESSION="$MIG" AGENT_ROLE=build "$MUX" inbox > "$WORK/mig-inbox.out" 2>&1
check "the migrated inbox still delivers" grep -qF "migration fixture inbox" "$WORK/mig-inbox.out"
mstat() { BUS_SESSION="$MIG" "$@"; }
check "the migrated run is still parked on its gate" wait_until 20 mstat node_is "$MRID" gate waiting
check "graph approve accepted on the migrated run" mstat approve "$MRID"
check "the migrated run completes" wait_until 40 mstat run_is "$MRID" complete

STARTED=$(events "$MIG" started)
kill "$MPID" 2>/dev/null
wait_until 20 dead "$MPID"
BUS_SESSION="$MIG" "$MUX" watch "$MIG" --poll 2 >"$WORK/mig-daemon2.log" 2>&1 &
MPID=$!
check "second daemon start" wait_until 40 events_at_least "$MIG" started $((STARTED + 1))
sleep 2
check "second start moved nothing (still one bus-dir-migrated row)" same "$(events "$MIG" bus-dir-migrated)" 1
check "second start logged no bus-dir-conflict" same "$(events "$MIG" bus-dir-conflict)" 0
check "no legacy /tmp bus reappeared" test ! -e "$LEG"

# --- Coverage floor --------------------------------------------------------
# 1: 2 resolver + 5 daemon/monitor; 2: 1 send + 3 run + 5 spawn + 3 leak;
# 3: 8 cleaner + 4 cancel; 4: 4 negative; 5: 4 seed + 9 migrate + 4 second
# start = 52.
FLOOR=52
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
