#!/usr/bin/env bash
# Integration test for MUX-182: a cancelled run stops working, provenance is
# readable, only the user stops the user's work, and no channel states a
# conclusion it did not reach.
#
# Covers, on a real scratch daemon and real tmux windows:
#   1. Cancelling a run with a live spawn kills the worker — proved by window
#      and process absence, not by run state — and the worker mutates no file
#      after the cancel returns (its heartbeat file grew before, and stops).
#   2. A worker that cannot be stopped makes the cancel refuse: the run stays
#      canceling, the refusal names the survivor and its stop command, and the
#      survivor really is alive. Once it can be stopped, a re-run cancel
#      completes. The un-stoppable worker is a decoy window sharing its name,
#      which makes tmux's kill-window by name ambiguous.
#   3. A user-launched run reads as the user's in run.json, graph status <id>,
#      the graph status list and graph-run-created; an auto-launched run reads
#      autonomous in all four (negative control).
#   4. An agent's cancel of the user's run is refused and changes nothing; the
#      user's own cancel succeeds; an agent-launched run with no spawns is
#      cancelled freely and cleanly (negative control), and graph-run-canceled
#      names who cancelled.
#   5. The watch chain's notices state an exit code, never a health finding.
#   6. The daemon's idle rescue never delivers a pane scrape as a response:
#      it arrives as event:no-answer from daemon and the task is timed out.
#
# THE USER IDENTITY IS REAL, NOT CLAIMED. BusActorVerified overrules a missing
# or "user" AGENT_ROLE by walking the process ancestry for an agent runtime,
# so a script run by an agent cannot become the user by unsetting a variable.
# as_user therefore runs its command through `tmux run-shell`: the tmux server
# daemonized away from whoever started it, so the command's ancestry holds no
# agent runtime. If ps cannot be read there, the creator resolves to
# "unknown" and section 3 fails loudly rather than passing on a guess.
#
# ISOLATION: scratch BUS_SESSION, bus dir, HOME, config, lifecycle log and
# repo dir. The worker and the build role run a stub agent that prints the ❯
# the injection guard needs and appends a heartbeat line per second to
# $REPO/beats/<window>.beat — the file-mutation observable. No real AI CLI is
# ever launched.
#
# REQUIRES: installed muxcode >= v0.1.0 built from this tree, and tmux. Run
# ./build.sh first — this tests the INSTALLED binary. Section 6 waits up to
# ~150s for the idle rescue's two grace periods. The stall, stuck-reload,
# permission-block and force-respond watchdogs are disabled for that section.
#
# Usage: bash scripts/test-cancel-provenance.sh
#        MUXCODE_TEST_KEEP=1 bash scripts/test-cancel-provenance.sh   # keep WORK
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
require_muxcode_version "$MUX" v0.1.0 MUX-182 || { echo "  FAIL  binary precondition not met"; exit 1; }

GREEN=$'\033[0;32m'; RED=$'\033[0;31m'; NC=$'\033[0m'
pass=0; fail=0
ok()  { echo "  ${GREEN}PASS${NC}  $*"; pass=$((pass + 1)); }
bad() { echo "  ${RED}FAIL${NC}  $*"; fail=$((fail + 1)); }
check() { local msg="$1"; shift; if "$@"; then ok "$msg"; else bad "$msg"; fi; }

# --- Isolation -------------------------------------------------------------
export BUS_SESSION="cancel-prov-test-$$"
BD="/tmp/muxcode-bus-${BUS_SESSION}"
WORK="/tmp/cancel-prov-work-$$"
REPO="$WORK/repo"
BEATS="$REPO/beats"
mkdir -p "$REPO" "$BEATS"
cd "$WORK" || { echo "  FAIL  cannot cd to scratch dir $WORK"; exit 1; }

export HOME="$WORK/home"
mkdir -p "$HOME/.config/muxcode/agents"
: > "$HOME/.config/muxcode/config"
# agent launch refuses a role whose definition resolves at no tier, and the
# scratch HOME has none — the first run left every worker pane at that error.
printf -- '---\ndescription: Fixture worker for test-cancel-provenance\n---\nStub.\n' \
  > "$HOME/.config/muxcode/agents/code-editor.md"
export MUXCODE_LIFECYCLE_LOG_DIR="$WORK/lifecycle"
: > "$WORK/empty-config"
export MUXCODE_CONFIG="$WORK/empty-config"
export MUXCODE_TMP_CLEANUP_THRESHOLD=0
export MUXCODE_BRANCH_TIME_DISABLE=1
export MUXCODE_DEDUP_WINDOW=0
export MUXCODE_SESSION_REPO_DIR="$REPO"
export MUXCODE_TASK_STALL_DISABLE=1
export MUXCODE_STUCK_RELOAD_DISABLE=1
export MUXCODE_PERMBLOCK_WATCHDOG_DISABLE=1
export MUXCODE_FORCE_RESPOND_DISABLE=1
LIFELOG="$MUXCODE_LIFECYCLE_LOG_DIR/${BUS_SESSION}.log"

cat > "$WORK/stub-agent" <<STUB
#!/usr/bin/env bash
w="\$(tmux display-message -p -t "\$TMUX_PANE" '#W' 2>/dev/null || echo unknown)"
echo \$\$ > "$BEATS/\$w.pid"
n=0
while :; do
  printf '\n❯ '
  n=\$((n + 1)); echo "\$n" >> "$BEATS/\$w.beat"
  read -r -t 1 _ || sleep 0.2
done
STUB
chmod +x "$WORK/stub-agent"
export MUXCODE_EDIT_CLI="$WORK/stub-agent"
export MUXCODE_BUILD_CLI="$WORK/stub-agent"

DPID=""
cleanup() {
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

tmux new-session -d -s "$BUS_SESSION" -n edit -x 160 -y 40
for v in BUS_SESSION HOME MUXCODE_CONFIG MUXCODE_LIFECYCLE_LOG_DIR MUXCODE_EDIT_CLI MUXCODE_BUILD_CLI \
         MUXCODE_SESSION_REPO_DIR MUXCODE_TMP_CLEANUP_THRESHOLD MUXCODE_BRANCH_TIME_DISABLE MUXCODE_DEDUP_WINDOW; do
  tmux set-environment -t "$BUS_SESSION" "$v" "${!v}"
done
"$MUX" init >/dev/null 2>&1

# --- Helpers ---------------------------------------------------------------
strip() { sed $'s/\x1b\\[[0-9;]*[A-Za-z]//g'; }
run_state() { "$MUX" graph status "$1" 2>/dev/null | strip | head -1 | sed 's/.*\[\([a-z]*\)\].*/\1/'; }
started_id() { grep -o 'Started run [^ ]*' | awk '{print $3}'; }
as_agent() { local role="$1"; shift; AGENT_ROLE="$role" "$MUX" "$@"; }

# as_user runs muxcode where no agent runtime is an ancestor — see header.
# Prints the command's output; returns its exit code.
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

lifecycle_row() { grep "\"event\":\"$1\"" "$LIFELOG" 2>/dev/null | grep -- "$2"; }
has_row() { lifecycle_row "$1" "$2" >/dev/null; }

SPAWN_ROLE=""; SPAWN_ID=""; SPAWN_WIN=""
spawn_for_run() {
  local line
  line="$(grep "\"run_id\":\"$1\"" "$BD/spawn.jsonl" 2>/dev/null | tail -1)"
  [ -n "$line" ] || return 1
  SPAWN_ROLE="$(printf '%s' "$line" | grep -o '"spawn_role":"[^"]*"' | cut -d'"' -f4)"
  SPAWN_ID="$(printf '%s' "$line" | grep -o '"id":"[^"]*"' | head -1 | cut -d'"' -f4)"
  SPAWN_WIN="$(printf '%s' "$line" | grep -o '"window":"[^"]*"' | cut -d'"' -f4)"
  [ -n "$SPAWN_ROLE" ]
}
spawn_status() { grep "\"id\":\"$1\"" "$BD/spawn.jsonl" 2>/dev/null | tail -1 | grep -o '"status":"[^"]*"' | cut -d'"' -f4; }
window_exists() { tmux list-windows -t "$BUS_SESSION" -F '#W' 2>/dev/null | grep -qx "$1"; }
stub_pid() { cat "$BEATS/$1.pid" 2>/dev/null; }
stub_alive() { local p; p="$(stub_pid "$1")"; [ -n "$p" ] && kill -0 "$p" 2>/dev/null; }
beats() { if [ -f "$BEATS/$1.beat" ]; then wc -l < "$BEATS/$1.beat" | tr -d ' '; else echo 0; fi; }
beats_at_least() { [ "$(beats "$1")" -ge "$2" ]; }

# dump_panes <window> — the window's pane tails, so a worker that never
# booted is diagnosable from the failure output alone.
dump_panes() {
  local p
  for p in $(tmux list-panes -t "$BUS_SESSION:$1" -F '#{pane_id}' 2>/dev/null); do
    echo "    --- pane $p of $1:"
    tmux capture-pane -p -t "$p" -S -15 2>/dev/null | strip | grep -v '^$' | sed 's/^/    | /'
  done
}
stub_running_or_dump() { wait_until 60 stub_alive "$1" || { dump_panes "$1"; return 1; }; }

# edit_block <pattern> — the whole message block in edit's inbox containing it.
edit_block() {
  as_agent edit inbox --peek 2>/dev/null | strip | awk -v pat="$1" '
    /^--- Message from / { if (blk ~ pat) print blk; blk = "" }
    { blk = blk $0 "\n" }
    END { if (blk ~ pat) print blk }'
}

# --- Fixtures --------------------------------------------------------------
cat > "$WORK/spawn.json" <<'EOF'
{"name": "cancel-spawn", "start": "w",
 "nodes": [{"id": "w", "type": "spawn", "role": "edit", "message": "implement the fixture"}],
 "edges": []}
EOF
cat > "$WORK/hold.json" <<'EOF'
{"name": "hold", "start": "gate",
 "nodes": [{"id": "gate", "type": "wait_human", "message": "hold for the test"}],
 "edges": []}
EOF

# --- Daemon ----------------------------------------------------------------
"$MUX" watch "$BUS_SESSION" --poll 2 >"$WORK/daemon.log" 2>&1 &
DPID=$!
sleep 1
kill -0 "$DPID" 2>/dev/null && ok "scratch daemon running (pid $DPID)" \
  || bad "scratch daemon exited immediately: $(tail -3 "$WORK/daemon.log" 2>/dev/null)"

# --- 1. A live spawn dies with its run, and stops writing -------------------
echo "--- 1. cancel kills the live worker"
RID_A="$(as_agent auto graph run --file "$WORK/spawn.json" 2>&1 | started_id)"
check "spawn run started: ${RID_A:-none}" test -n "$RID_A"
check "worker registered for the run" wait_until 60 spawn_for_run "$RID_A"
WIN_A="$SPAWN_WIN"; ID_A="$SPAWN_ID"
check "worker stub agent running in $WIN_A" stub_running_or_dump "$WIN_A"
check "worker is mutating files before the cancel (live observable)" wait_until 20 beats_at_least "$WIN_A" 3
PANES_A="$(tmux list-panes -t "$BUS_SESSION:$WIN_A" -F '#{pane_pid}' 2>/dev/null | tr '\n' ' ')"
PID_A="$(stub_pid "$WIN_A")"

as_agent edit graph cancel "$RID_A" >"$WORK/cancel-a.out" 2>&1
check "graph cancel returned success" test $? -eq 0
BEATS_AT_CANCEL="$(beats "$WIN_A")"
check "run state canceled" test "$(run_state "$RID_A")" = canceled
check "worker window is gone" bash -c "! tmux list-windows -t '$BUS_SESSION' -F '#W' 2>/dev/null | grep -qx '$WIN_A'"
check "worker stub process (pid ${PID_A:-unknown}) is dead" bash -c "[ -n '$PID_A' ] && ! kill -0 '$PID_A' 2>/dev/null"
PANES_ALIVE=0
for p in $PANES_A; do kill -0 "$p" 2>/dev/null && PANES_ALIVE=1; done
check "worker pane processes ($PANES_A) are dead" test "$PANES_ALIVE" -eq 0
check "spawn entry $ID_A recorded stopped" test "$(spawn_status "$ID_A")" = stopped
check "graph-cancel-spawn-stopped row names the worker" has_row graph-cancel-spawn-stopped "$WIN_A"
sleep 3
check "no file mutation after the cancel returned ($BEATS_AT_CANCEL beats, then $(beats "$WIN_A"))" \
  bash -c "[ '$BEATS_AT_CANCEL' -ge 3 ] && [ '$(beats "$WIN_A")' = '$BEATS_AT_CANCEL' ]"

# --- 2. A worker that will not die makes the cancel refuse -----------------
echo "--- 2. an unstoppable worker fails the cancel closed"
RID_B="$(as_agent auto graph run --file "$WORK/spawn.json" 2>&1 | started_id)"
check "second spawn run started: ${RID_B:-none}" test -n "$RID_B"
check "worker registered for the second run" wait_until 60 spawn_for_run "$RID_B"
WIN_B="$SPAWN_WIN"; ROLE_B="$SPAWN_ROLE"; ID_B="$SPAWN_ID"
check "worker stub agent running in $WIN_B" stub_running_or_dump "$WIN_B"
DECOY="$(tmux new-window -d -P -F '#{window_id}' -t "$BUS_SESSION" -n "$WIN_B" 'sleep 600' 2>/dev/null)"
check "decoy window sharing the worker's name created ($DECOY)" test -n "$DECOY"

if as_agent edit graph cancel "$RID_B" >"$WORK/cancel-b.out" 2>&1; then
  bad "cancel reported success while the worker could not be stopped"
else
  ok "cancel refused to report success"
fi
check "refusal names the survivor and its stop command" \
  bash -c "grep -q '$ROLE_B' '$WORK/cancel-b.out' && grep -q 'muxcode spawn stop $ID_B' '$WORK/cancel-b.out'"
check "run left canceling, not canceled" test "$(run_state "$RID_B")" = canceling
check "graph-cancel-incomplete row names the survivor" has_row graph-cancel-incomplete "$ROLE_B"
check "the named survivor really is alive" stub_alive "$WIN_B"

tmux kill-window -t "$DECOY" 2>/dev/null
check "re-run cancel succeeds once the worker can be stopped" as_agent edit graph cancel "$RID_B"
check "run state now canceled" test "$(run_state "$RID_B")" = canceled
PID_B="$(stub_pid "$WIN_B")"
check "the survivor (pid ${PID_B:-unknown}) is now dead" bash -c "[ -n '$PID_B' ] && ! kill -0 '$PID_B' 2>/dev/null"

# --- 3. Provenance reads the same on every surface --------------------------
echo "--- 3. provenance on all four surfaces"
RID_U="$(as_user graph run --file "$WORK/hold.json" | started_id)"
check "user-launched run started: ${RID_U:-none}" test -n "$RID_U"
check "run.json records created_by user (real ancestry, not a claimed variable)" \
  grep -q '"created_by": *"user"' "$BD/graphs/$RID_U/run.json"
RID_Q="$(as_agent auto graph run --file "$WORK/hold.json" 2>&1 | started_id)"
check "auto-launched run started: ${RID_Q:-none}" test -n "$RID_Q"

# surfaces <rid> — one labelled line per surface, for assertions to read.
surfaces() {
  printf 'run.json\t%s\n' "$(grep -o '"provenance": *"[^"]*"' "$BD/graphs/$1/run.json" 2>/dev/null)"
  printf 'graph status <id>\t%s\n' "$("$MUX" graph status "$1" 2>/dev/null | strip | grep 'Launched by:')"
  printf 'graph status list\t%s\n' "$("$MUX" graph status 2>/dev/null | strip | grep -- "$1")"
  printf 'graph-run-created\t%s\n' "$(lifecycle_row graph-run-created "$1")"
}
surface_reads() { # <rid> <surface> <want> <must-not>
  local line
  line="$(surfaces "$1" | awk -F'\t' -v s="$2" '$1 == s { print $2 }')"
  [[ "$line" == *"$3"* && "$line" != *"$4"* ]]
}
for s in "run.json" "graph status <id>" "graph status list" "graph-run-created"; do
  check "user run reads 'the user, by hand' in $s, never autonomous" surface_reads "$RID_U" "$s" "the user, by hand" "autonomous"
  check "auto run reads 'auto (autonomous)' in $s, never the user (negative control)" surface_reads "$RID_Q" "$s" "auto (autonomous)" "the user"
done

# --- 4. Only the user stops the user's work --------------------------------
echo "--- 4. cancel authority"
if as_agent edit graph cancel "$RID_U" >"$WORK/cancel-u.out" 2>&1; then
  bad "edit cancelled a run the user launched by hand"
else
  ok "edit's cancel of the user's run refused"
fi
check "refusal hands the decision to the user" grep -q 'let the user cancel it' "$WORK/cancel-u.out"
check "the user's run is still running" test "$(run_state "$RID_U")" = running
check "graph-cancel-refused row sourced edit" bash -c "grep '\"event\":\"graph-cancel-refused\"' '$LIFELOG' | grep '$RID_U' | grep -q '\"source\":\"edit\"'"
check "the user's own cancel succeeds" as_user graph cancel "$RID_U"
check "graph-run-canceled names the user" has_row graph-run-canceled "$RID_U canceled by user"

as_agent edit graph cancel "$RID_Q" >"$WORK/cancel-q.out" 2>&1
check "an agent-launched run with no spawns cancels freely (negative control)" test $? -eq 0
check "that run state canceled" test "$(run_state "$RID_Q")" = canceled
check "graph-run-canceled names edit" has_row graph-run-canceled "$RID_Q canceled by edit"
check "no graph-cancel-incomplete for the spawn-less run" bash -c "! grep '\"event\":\"graph-cancel-incomplete\"' '$LIFELOG' | grep -q '$RID_Q'"

# --- 5. The watch notices state exit codes, not findings -------------------
echo "--- 5. watch notices"
watch_hook() {
  printf '{"hook_event_name":"PostToolUse","tool_name":"Bash","tool_input":{"command":"%s"},"exit_code":%s}' "$1" "$2" \
    | AGENT_ROLE=watch MUXCODE_WATCH_CLI=claude "$MUX" hook bash >/dev/null 2>&1
}
watch_hook "aws logs tail /aws/lambda/fixture-ok --since 5m" 0
check "watch exit 0 notice arrived and states the exit code" wait_until 20 bash -c "[ -n \"\$(AGENT_ROLE=edit '$MUX' inbox --peek 2>/dev/null | grep 'Watch command exited 0' | grep 'fixture-ok')\" ]"
check "the exit 0 notice asserts no health finding" bash -c "! AGENT_ROLE=edit '$MUX' inbox --peek 2>/dev/null | grep 'fixture-ok' | grep -qi 'healthy'"
watch_hook "aws logs tail /aws/lambda/fixture-bad --since 5m" 1
check "watch exit 1 notice arrived and states FAILED (exit 1)" wait_until 20 bash -c "[ -n \"\$(AGENT_ROLE=edit '$MUX' inbox --peek 2>/dev/null | grep 'Watch command FAILED (exit 1)' | grep 'fixture-bad')\" ]"
check "the exit 1 notice claims no detected errors" bash -c "! AGENT_ROLE=edit '$MUX' inbox --peek 2>/dev/null | grep 'fixture-bad' | grep -qi 'detected errors'"

# --- 6. A pane scrape is never a response ----------------------------------
echo "--- 6. idle rescue sends a non-answer (waits up to ~150s)"
tmux new-window -d -t "$BUS_SESSION" -n build "$WORK/stub-agent"
tmux split-window -h -d -t "$BUS_SESSION:build" "$WORK/stub-agent"
check "build window shows an idle stub agent" wait_until 20 bash -c "tmux capture-pane -p -t '$BUS_SESSION:build.1' | grep -q '❯'"
SEND_OUT="$(as_agent edit send build build "compile the rescue fixture" --track 2>&1)"
TASK="$(printf '%s' "$SEND_OUT" | grep -o 'Tracking task [^ ]*' | awk '{print $3}')"
check "tracked request sent to build (task ${TASK:-none})" test -n "$TASK"
check "the rescue delivered a no-answer notice" wait_until 300 bash -c "AGENT_ROLE=edit '$MUX' inbox --peek 2>/dev/null | grep -q 'NOT A RESPONSE'"
NOTICE="$(edit_block 'NOT A RESPONSE')"
check "the notice is Type event from daemon, action no-answer" \
  bash -c "printf '%s' \"\$1\" | grep -q '^--- Message from daemon' && printf '%s' \"\$1\" | grep -q 'Type: event  Action: no-answer'" _ "$NOTICE"
check "no pane scrape arrived as Type: response from build" \
  bash -c "! AGENT_ROLE=edit '$MUX' inbox --peek 2>/dev/null | grep -A1 '^--- Message from build' | grep -q 'Type: response'"
check "the task is timed-out, not completed as answered" grep -q '"status": *"timed-out"' "$BD/tasks/$TASK.json"

# --- Coverage floor --------------------------------------------------------
# 1 daemon + 12 live-spawn cancel + 12 unstoppable worker + 11 provenance
# + 10 authority + 4 watch + 6 idle rescue = 56. A section that dies early
# runs fewer, and a green summary over a short run is the failure this floor
# catches — so it is the arithmetic, not a margin under it.
total=$((pass + fail))
if [ "$total" -ge 56 ]; then
  ok "coverage floor met ($total checks executed)"
else
  bad "coverage floor NOT met — only $total checks executed, want >= 56 (a skipped section must not report green)"
fi

echo ""
echo "  ${pass} passed, ${fail} failed"
[ "$fail" -eq 0 ] || exit 1
exit 0
