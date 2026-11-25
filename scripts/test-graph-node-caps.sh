#!/usr/bin/env bash
# Integration test for graph node time limits (MUX-162).
#
# On 2026-09-09 a PR-review fix step was a send to edit on the 600 s default
# cap: it expired at 602 s mid-fix and failed a run whose fix then shipped.
# Its successor, 80-pr-review-fix, does the fix as a spawn, which has no task
# clock. This script proves that end to end on a scratch daemon:
#
#   1. The documented cap table (docs/agent-bus.md, "Node time limits") equals
#      what every builtin template carries: each node with timeout_secs, and
#      each spawn/map node, read from `muxcode graph export`. Negative
#      controls: a doctored table (a changed cap, a dropped row) must differ.
#   2. The real 80-pr-review-fix run: the fix worker, consumed its seed and
#      backdated 700 s, stays running across daemon ticks; its late reply
#      finishes the node and dispatches build.
#   3. Control: the old shape — a send to edit on the default cap — is still
#      running when backdated 300 s and expires when backdated 700 s, failing
#      its run and logging task-timeout.
#   4. Isolation (MUX-226): during the run the private server holds the session
#      and the user's server does not; the fix worker's launcher is
#      $WORK/bin/muxcode and resolves to the binary under test (negative
#      control: resolved with the scratch bin off PATH, a decoy leading it, the
#      check fails); after the run the user's server holds no scratch session
#      and the real HOME gained no lifecycle log.
#
# Time is simulated by backdating the node's started_at and the task's
# sent_at on disk; the daemon re-reads both each tick.
#
# ISOLATION: scratch BUS_SESSION, HOME, repo, lifecycle log and empty config;
# the spawn worker's CLI is a stub that shows an idle prompt (no AI CLI, no
# network) and this script plays every agent over the bus. The session lives
# on a private tmux server under $WORK (TMUX_TMPDIR), killed on exit, so its
# panes inherit this script's environment rather than the user's server's —
# before MUX-226 a worker pane ran with the real HOME and left a lifecycle log
# in ~/.config/muxcode/logs. $WORK/bin/muxcode links to the binary under test
# and leads PATH for the script, the server and the session, so every muxcode a
# worker resolves (findMuxcodeBinary: ~/.local/bin, then PATH) is that binary.
#
# REQUIRES: installed muxcode >= v0.1.0, tmux, jq.
#
# Usage: bash scripts/test-graph-node-caps.sh
set -uo pipefail
export MUXCODE_CONSUMER_GUARD=0 # agents here are played by reading their inboxes, which no window or listener shows (MUX-145)

FLOOR=37
DOCS="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/docs/agent-bus.md"

MUX="${MUXCODE_BIN:-muxcode}"
MUX="$(command -v "$MUX" 2>/dev/null || echo "$MUX")"
# Absolute, so the $WORK/bin/muxcode symlink below resolves from any directory
# — a relative MUXCODE_BIN would leave it dangling.
case "$MUX" in
  /*) ;;
  *) [ -e "$MUX" ] && MUX="$(cd "$(dirname "$MUX")" && pwd)/$(basename "$MUX")" ;;
esac
if [ ! -x "$MUX" ]; then
  echo "  FAIL  cannot resolve muxcode binary ('$MUX') — set MUXCODE_BIN"
  exit 1
fi
for tool in tmux jq; do
  command -v "$tool" >/dev/null 2>&1 || { echo "  FAIL  $tool not available"; exit 1; }
done
. "$(dirname "${BASH_SOURCE[0]}")/lib/muxcode-version.sh"
. "$(dirname "${BASH_SOURCE[0]}")/lib/muxcode-bus-dir.sh"
require_muxcode_version "$MUX" v0.1.0 MUX-162 || { echo "  FAIL  binary precondition not met"; exit 1; }

GREEN=$'\033[0;32m'; RED=$'\033[0;31m'; NC=$'\033[0m'
pass=0; fail=0
ok()  { echo "  ${GREEN}PASS${NC}  $*"; pass=$((pass + 1)); }
bad() { echo "  ${RED}FAIL${NC}  $*"; fail=$((fail + 1)); }

# --- Isolation -------------------------------------------------------------
# The caller's tmux server, HOME and PATH, kept to prove nothing reaches them.
# The server is the one $TMUX names (a -L/-S server included), else the default
# socket, and is probed by -S alone: tmux falls back to the default socket when
# a socket directory is missing, which would test the wrong server.
REAL_HOME="$HOME"
ORIG_PATH="$PATH"
if [ -n "${TMUX:-}" ]; then
  REAL_SOCK="${TMUX%%,*}"
  CALLER_SESSION="$(tmux display-message -p '#S' 2>/dev/null || true)"
else
  REAL_SOCK="${TMUX_TMPDIR:-/tmp}/tmux-$(id -u)/default"
  CALLER_SESSION=""
fi
real_tmux() { env -u TMUX -u TMUX_TMPDIR tmux -S "$REAL_SOCK" "$@"; }
export BUS_SESSION="nodecaps-test-$$"
WORK="/tmp/nodecaps-work-$$"
REPO="$WORK/repo"
mkdir -p "$REPO"
export HOME="$WORK/home"
mkdir -p "$HOME/.config/muxcode"
# Releasing fix-gate needs an authorized actor that did not create the run
# (MUX-144); the daemon seals this list at startup.
echo "MUXCODE_GATE_AUTHORITY_ROLES=test-approver" > "$HOME/.config/muxcode/config"
git -C "$REPO" init -q -b main
# A fresh origin/main, so the fix's freshness check (MUX-166) passes.
git -C "$REPO" -c user.name=t -c user.email=t@t -c commit.gpgsign=false commit -q --allow-empty -m base
git init -q --bare -b main "$WORK/origin.git"
git -C "$REPO" remote add origin "$WORK/origin.git"
git -C "$REPO" push -q -u origin main
export MUXCODE_LIFECYCLE_LOG_DIR="$WORK/lifecycle"
: > "$WORK/empty-config"
export MUXCODE_CONFIG="$WORK/empty-config"
export MUXCODE_TMP_CLEANUP_THRESHOLD=0
export MUXCODE_BRANCH_TIME_DISABLE=1
export MUXCODE_DEDUP_WINDOW=0
export MUXCODE_SESSION_REPO_DIR="$REPO"
unset TMUX
export TMUX_TMPDIR="$WORK/tmux"
mkdir -p "$TMUX_TMPDIR" "$WORK/bin"
ln -sf "$MUX" "$WORK/bin/muxcode"
export PATH="$WORK/bin:$PATH"
BD="$(muxcode_bus_dir "$MUX" "$BUS_SESSION")" || exit 1
LIFELOG="$MUXCODE_LIFECYCLE_LOG_DIR/$BUS_SESSION.log"

DPID=""
cleanup() {
  [ -n "$DPID" ] && kill "$DPID" 2>/dev/null
  tmux kill-server 2>/dev/null
  [ "${MUXCODE_TEST_KEEP:-}" = "1" ] && { echo "  (kept for diagnosis: $WORK)"; return; }
  rm -rf "$BD" "$WORK"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

approve_gate() { AGENT_ROLE=test-approver "$MUX" graph approve "$@"; }
run_state()  { "$MUX" graph status "$1" 2>/dev/null | sed $'s/\x1b\\[[0-9;]*[A-Za-z]//g' | head -1 | sed 's/.*\[\([a-z]*\)\].*/\1/'; }
node_state() { "$MUX" graph status "$1" 2>/dev/null | sed $'s/\x1b\\[[0-9;]*[A-Za-z]//g' | awk -v n="$2" '$1==n {print $2}'; }

wait_node_state() {
  local i
  for i in $(seq 1 80); do
    [ "$(node_state "$1" "$2")" = "$3" ] && return 0
    sleep 0.5
  done
  return 1
}

wait_run_state() {
  local i
  for i in $(seq 1 80); do
    [ "$(run_state "$1")" = "$2" ] && return 0
    sleep 0.5
  done
  return 1
}

# wait_request <role> <action> [exclude-id] — wait for a matching request
# without consuming it; sets REQ_ID to its reply id.
REQ_ID=""
wait_request() {
  local role="$1" action="$2" not="${3:-}" i out block rid
  for i in $(seq 1 60); do
    out="$(AGENT_ROLE="$role" "$MUX" inbox --peek 2>/dev/null || true)"
    block="$(printf '%s\n' "$out" | awk -v RS='--- Message' -v a="Action: $action" \
      'index($0, "Type: request") && index($0, a) {blk=$0} END{print blk}')"
    if [ -n "$block" ]; then
      rid="$(printf '%s\n' "$block" | grep -o -- '--reply-to [A-Za-z0-9-]*' | head -1 | awk '{print $2}')"
      if [ -n "$rid" ] && [ "$rid" != "$not" ]; then
        REQ_ID="$rid"
        return 0
      fi
    fi
    sleep 0.5
  done
  return 1
}

# answer_request <role> <text> — consume the role's inbox and reply to REQ_ID
# with the EXIT=0 verdict the executor reads (these fake agents run no hooks).
answer_request() {
  AGENT_ROLE="$1" "$MUX" inbox >/dev/null 2>&1
  AGENT_ROLE="$1" "$MUX" send edit response "$2 EXIT=0" --type response --reply-to "$REQ_ID" >/dev/null 2>&1
}

# backdate <json-file> <field> <secs> — move a timestamp into the past.
backdate() {
  local tmp="$1.tmp.$$"
  jq --arg k "$2" --argjson s "$3" '.[$k] -= $s' "$1" > "$tmp" && mv "$tmp" "$1"
}

# doc_caps <agent-bus.md> — the "Node time limits" table as sorted
# "template node type cap" lines; cap is seconds or "none".
doc_caps() {
  awk '
    /^#### Node time limits/ { on = 1; next }
    on && /^#/ { on = 0 }
    on && /^\| *`[0-9]+-/ {
      split($0, c, "|")
      tpl = c[2]; gsub(/[` ]/, "", tpl)
      type = c[4]; sub(/^ +/, "", type); split(type, tw, " "); type = tw[1]
      cap = c[5]; gsub(/^ +| +$/, "", cap); sub(/ s$/, "", cap)
      k = split(c[3], ns, ",")
      for (i = 1; i <= k; i++) { nd = ns[i]; gsub(/[` ]/, "", nd); print tpl, nd, type, cap }
    }' "$1" | sort
}

# actual_caps — every builtin node that is off the 600 s send default.
actual_caps() {
  local tpl
  for tpl in $BUILTINS; do
    (cd "$REPO" && "$MUX" graph export "$tpl" 2>/dev/null) | jq -r --arg t "$tpl" '
      .nodes[] | select(((.timeout_secs // 0) > 0) or .type == "spawn" or .type == "map")
      | "\($t) \(.id) \(.type) \(if (.timeout_secs // 0) > 0 then .timeout_secs else "none" end)"'
  done | sort
}

# --- 1. The documented cap table matches every builtin ---------------------
echo "--- 1. documented caps vs builtin templates"
BUILTINS="$(cd "$REPO" && "$MUX" graph list 2>/dev/null | awk '$2 == "builtin" {print $1}')"
shadowed="$(cd "$REPO" && "$MUX" graph list 2>/dev/null | awk 'NR > 1 && NF > 1 && $2 != "builtin" {print $1}')"
nb="$(printf '%s\n' "$BUILTINS" | grep -c .)"
[ "$nb" -ge 13 ] && [ -z "$shadowed" ] \
  && ok "$nb builtin templates listed, none shadowed" \
  || bad "builtin listing: $nb templates, shadowed: ${shadowed:-none}"

DOC="$(doc_caps "$DOCS")"
ACT="$(actual_caps)"
nd="$(printf '%s\n' "$DOC" | grep -c .)"
[ "$nd" -ge 10 ] && ok "doc table parsed: $nd node rows" || bad "doc table parsed only $nd rows from $DOCS"
[ -n "$ACT" ] && ok "templates export $(printf '%s\n' "$ACT" | grep -c .) capped or spawn nodes" || bad "no capped or spawn node exported"
if [ "$DOC" = "$ACT" ]; then
  ok "doc cap table equals every builtin's timeout_secs and spawn nodes"
else
  bad "doc cap table drifted from the templates (< doc only, > templates only):"
  diff <(printf '%s\n' "$DOC") <(printf '%s\n' "$ACT") | grep '^[<>]' | sed 's/^/        /'
fi
awk '/^#### Node time limits/{on=1;next} on&&/^#/{on=0} on' "$DOCS" | grep -q 'every other `send` node.*600 s default' \
  && ok "doc table names the 600 s default for every other send" \
  || bad "doc table lacks the 600 s default row"

sed 's/| 5400 s |/| 600 s |/' "$DOCS" > "$WORK/doctored-cap.md"
[ "$(doc_caps "$WORK/doctored-cap.md")" != "$ACT" ] \
  && ok "negative control: a changed cap in the table is detected" \
  || bad "negative control: a doctored cap still matched — the comparison cannot fail"
grep -v '^| `80-pr-review-fix`' "$DOCS" > "$WORK/doctored-row.md"
[ "$(doc_caps "$WORK/doctored-row.md")" != "$ACT" ] \
  && ok "negative control: a dropped row is detected" \
  || bad "negative control: a dropped row still matched"

# --- 2. Scratch daemon -----------------------------------------------------
echo "--- 2. 80-pr-review-fix: a fix answered after 700 s"
tmux new-session -d -s "$BUS_SESSION" -n edit -x 120 -y 30
cat > "$WORK/stub-agent" <<'STUB'
#!/usr/bin/env bash
while :; do
  printf '\n❯ '
  read -r _ || sleep 5
done
STUB
chmod +x "$WORK/stub-agent"
tmux set-environment -t "$BUS_SESSION" PATH "$PATH"
tmux set-environment -t "$BUS_SESSION" BUS_SESSION "$BUS_SESSION"
tmux set-environment -t "$BUS_SESSION" MUXCODE_CONFIG "$WORK/empty-config"
tmux set-environment -t "$BUS_SESSION" MUXCODE_EDIT_CLI "$WORK/stub-agent"
"$MUX" init >/dev/null 2>&1

(cd "$REPO" && exec "$MUX" watch "$BUS_SESSION" --poll 2 >"$WORK/daemon.log" 2>&1) &
DPID=$!
sleep 1
kill -0 "$DPID" 2>/dev/null && ok "scratch daemon running (pid $DPID)" \
  || bad "scratch daemon exited: $(tail -3 "$WORK/daemon.log" 2>/dev/null)"

RID="$(cd "$REPO" && "$MUX" graph run 80-pr-review-fix "late fix" 2>&1 | grep -o 'Started run [^ ]*' | awk '{print $3}')"
[ -n "$RID" ] && ok "80-pr-review-fix run started: $RID" || bad "80-pr-review-fix run failed to start"

if wait_request commit pr-read; then
  answer_request commit "PR-CONFIRMED #78 https://example.test/pull/78"
  ok "find-pr answered"
else
  bad "find-pr never dispatched"
fi
FIND_ID="$REQ_ID"
if wait_request commit pr-read "$FIND_ID"; then
  answer_request commit "Unresolved: comment 2987 at bus/graph_exec.go:412 asks to stream the rollout scan"
  ok "read-comments answered with an actionable comment"
else
  bad "read-comments never dispatched"
fi
wait_node_state "$RID" fix-gate waiting && ok "fix-gate waiting" || bad "fix-gate state $(node_state "$RID" fix-gate), want waiting"
approve_gate "$RID" fix-gate >/dev/null 2>&1 && ok "fix-gate approved" || bad "fix-gate approval refused"

SPAWN_ROLE=""
for _ in $(seq 1 60); do
  SPAWN_ROLE="$(grep "\"run_id\":\"$RID\"" "$BD/spawn.jsonl" 2>/dev/null | tail -1 | grep -o '"spawn_role":"[^"]*"' | cut -d'"' -f4)"
  [ -n "$SPAWN_ROLE" ] && break
  sleep 0.5
done
[ -n "$SPAWN_ROLE" ] && ok "fix worker spawned: $SPAWN_ROLE" || bad "fix worker never spawned"

# MUX-226: the session, worker window included, is on the private server only.
tmux has-session -t "$BUS_SESSION" 2>/dev/null \
  && ok "the private server holds the scratch session (the next check can fail)" \
  || bad "the private server does not list $BUS_SESSION"
# Sentinel: the probe reaches the caller's server — its own session is visible
# through it — so the absence checks below look at the right server.
if [ -n "$CALLER_SESSION" ]; then
  real_tmux has-session -t "=$CALLER_SESSION" 2>/dev/null \
    && ok "the probe reaches the caller's tmux server ($REAL_SOCK holds $CALLER_SESSION)" \
    || bad "the probe at $REAL_SOCK does not see the caller's session $CALLER_SESSION"
elif real_tmux list-sessions >/dev/null 2>&1; then
  ok "the probe reaches the caller's default tmux server ($REAL_SOCK)"
else
  ok "no caller tmux server at $REAL_SOCK — nothing to leak onto"
fi
real_tmux has-session -t "=$BUS_SESSION" 2>/dev/null \
  && bad "the user's tmux server holds $BUS_SESSION during the run" \
  || ok "the user's tmux server holds no scratch session during the run"

# The launcher the daemon resolved for the worker (findMuxcodeBinary) is typed
# into the worker's console pane as "<launcher> console <role>".
resolves_to_mux() { [ -n "$1" ] && [ "$(realpath "$1" 2>/dev/null)" = "$(realpath "$MUX")" ]; }
LAUNCHER=""
for _ in $(seq 1 20); do
  LAUNCHER="$(for p in $(tmux list-panes -t "$BUS_SESSION:$SPAWN_ROLE" -F '#{pane_index}' 2>/dev/null); do
      tmux capture-pane -p -J -t "$BUS_SESSION:$SPAWN_ROLE.$p" -S -50 2>/dev/null
    done | grep -oE "[^ ]*/muxcode console $SPAWN_ROLE" | head -1 | awk '{print $1}')"
  [ -n "$LAUNCHER" ] && break
  sleep 0.5
done
[ "$LAUNCHER" = "$WORK/bin/muxcode" ] \
  && ok "the worker's launcher is the scratch bin's muxcode" \
  || bad "the worker's launcher is '${LAUNCHER:-none}', want $WORK/bin/muxcode"
resolves_to_mux "$LAUNCHER" \
  && ok "the worker's launcher is the binary under test ($(realpath "$MUX"))" \
  || bad "the worker's launcher resolves to '$(realpath "$LAUNCHER" 2>/dev/null)', want $(realpath "$MUX")"

# Negative control: the launcher findMuxcodeBinary would pick with the scratch
# bin left off PATH ($HOME/.local/bin, then PATH). A decoy leads that PATH, so
# the control discriminates whichever muxcode the caller has installed.
mkdir -p "$WORK/decoy"
printf '#!/bin/sh\nexit 0\n' > "$WORK/decoy/muxcode"
chmod +x "$WORK/decoy/muxcode"
if [ -x "$HOME/.local/bin/muxcode" ]; then
  OFF_PATH_LAUNCHER="$HOME/.local/bin/muxcode"
else
  OFF_PATH_LAUNCHER="$(PATH="$WORK/decoy:$ORIG_PATH" command -v muxcode)"
fi
resolves_to_mux "$OFF_PATH_LAUNCHER" \
  && bad "negative control: with the scratch bin off PATH the launcher '$OFF_PATH_LAUNCHER' still passed" \
  || ok "negative control: with the scratch bin off PATH the binary check fails ($OFF_PATH_LAUNCHER)"
if [ -n "$SPAWN_ROLE" ] && wait_request "$SPAWN_ROLE" spawn-task; then
  ok "fix seed delivered to the worker"
else
  bad "fix seed never reached the worker"
fi
SEED_ID="$REQ_ID"

# The worker reads its seed and works: no stall to redrive, only the clock.
AGENT_ROLE="$SPAWN_ROLE" "$MUX" inbox >/dev/null 2>&1
FIXNODE="$BD/graphs/$RID/nodes/fix.json"
backdate "$FIXNODE" started_at 700
sleep 8
[ "$(node_state "$RID" fix)" = "running" ] \
  && ok "fix still running 700 s after dispatch — a spawn has no clock" \
  || bad "fix state $(node_state "$RID" fix) at 700 s, want running"
age=$(( $(date +%s) - $(jq -r '.started_at' "$FIXNODE") ))
[ "$age" -ge 600 ] && ok "the daemon ticked over a fix ${age}s old" || bad "fix started_at only ${age}s old — the backdate did not hold"
grep -q "$RID: fix -> failure" "$LIFELOG" 2>/dev/null \
  && bad "fix finished as a failure before its reply: $(grep "$RID: fix" "$LIFELOG" | tail -1)" \
  || ok "no failure recorded for fix before its reply"

REQ_ID="$SEED_ID"
answer_request "$SPAWN_ROLE" "Comment 2987: streamed the rollout scan (bus/graph_exec.go:412)"
wait_request build build && ok "build dispatched on the late reply" || bad "build never dispatched after the late reply"
wait_node_state "$RID" fix done && ok "fix done" || bad "fix state $(node_state "$RID" fix), want done"
jq -e '.outcome == "success"' "$FIXNODE" >/dev/null 2>&1 \
  && ok "fix outcome success" || bad "fix outcome $(jq -r '.outcome' "$FIXNODE" 2>/dev/null), want success"

# --- 3. Control: the old send-to-edit shape expires at the default cap ----
echo "--- 3. control: a send on the 600 s default"
cat > "$WORK/old-fix.json" <<'JSON'
{
  "name": "old-fix-send",
  "description": "MUX-162 control: the 2026-09-09 fix step as a send on the default cap",
  "start": "c",
  "nodes": [
    {"id": "c", "type": "send", "role": "edit", "action": "edit", "message": "Address the PR review comments"},
    {"id": "d", "type": "send", "role": "build", "action": "build", "message": "Run ./build.sh and report results"}
  ],
  "edges": [{"from": "c", "to": "d"}]
}
JSON
RIDC="$(cd "$REPO" && "$MUX" graph run --file "$WORK/old-fix.json" "control" 2>&1 | grep -o 'Started run [^ ]*' | awk '{print $3}')"
[ -n "$RIDC" ] && ok "control run started: $RIDC" || bad "control run failed to start"
wait_node_state "$RIDC" c running && ok "control c running" || bad "control c state $(node_state "$RIDC" c), want running"
CNODE="$BD/graphs/$RIDC/nodes/c.json"
TASK_ID="$(jq -r '.task_id // empty' "$CNODE" 2>/dev/null)"
TASKF="$BD/tasks/$TASK_ID.json"
[ -n "$TASK_ID" ] && [ -f "$TASKF" ] && ok "control dispatch tracked as task $TASK_ID" || bad "control task not found (${TASK_ID:-no id})"

backdate "$TASKF" sent_at 300
sleep 6
[ "$(node_state "$RIDC" c)" = "running" ] \
  && ok "control still running at 300 s — under the cap" \
  || bad "control c state $(node_state "$RIDC" c) at 300 s, want running"

backdate "$TASKF" sent_at 400
wait_run_state "$RIDC" failed && ok "control run failed at 700 s — the default cap expired" \
  || bad "control run state $(run_state "$RIDC") at 700 s, want failed"
jq -e '.outcome == "failure"' "$CNODE" >/dev/null 2>&1 \
  && ok "control c outcome failure ($(jq -r '.output' "$CNODE" 2>/dev/null | head -c 60))" \
  || bad "control c outcome $(jq -r '.outcome' "$CNODE" 2>/dev/null), want failure"
logged=0
for _ in $(seq 1 20); do
  grep -q '"task-timeout"' "$LIFELOG" 2>/dev/null && { logged=1; break; }
  sleep 1
done
[ "$logged" -eq 1 ] && ok "task-timeout logged for the expired dispatch" || bad "no task-timeout row in $LIFELOG"
[ "$(node_state "$RIDC" d)" != "running" ] && ok "control d never dispatched" || bad "control d dispatched after an expired c"

# --- 4. Nothing reached the user's server or HOME (MUX-226) ---------------
echo "--- 4. isolation after the run"
[ -n "$DPID" ] && kill "$DPID" 2>/dev/null && DPID=""
tmux kill-server 2>/dev/null
real_tmux has-session -t "=$BUS_SESSION" 2>/dev/null \
  && bad "the user's tmux server holds $BUS_SESSION after the run" \
  || ok "the user's tmux server holds no scratch session after the run"
[ -e "$REAL_HOME/.config/muxcode/logs/$BUS_SESSION.log" ] \
  && bad "a lifecycle log for $BUS_SESSION was written to the real HOME" \
  || ok "no lifecycle log for the scratch session in the real HOME"

# --- Summary ---------------------------------------------------------------
echo
echo "=== Summary: $pass passed, $fail failed (floor $FLOOR) ==="
if [ "$fail" -ne 0 ]; then
  exit 1
fi
if [ "$pass" -lt "$FLOOR" ]; then
  echo "  FAIL  only $pass checks passed — below the coverage floor of $FLOOR"
  exit 1
fi
exit 0
