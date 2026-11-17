#!/usr/bin/env bash
# Integration test for MUX-227 — reloading a hosted role is refused and leaves
# its host untouched.
#
# A hosted role has no window: docs runs inside plan's pane. `muxcode reload
# docs` resolved docs to plan's window, stopped the planner and relaunched the
# pane as the docs agent — the planner and its conversation gone — while
# `muxcode resume docs` refused and named the host. Both reload roads now share
# resume's refusal (hostedRoleRefusal).
#
# Hermetic: private tmux server, muxcode and scripts/fixtures/claude-stub built
# from THIS tree, agent definitions in a scratch install dir; the stub stands in
# for plan's Claude agent with a real inbox listener. Never run against a live
# session — the pre-fix control replaces the host it reloads.
#
# Sections:
#   A  `muxcode reload docs` exits non-zero naming `muxcode reload plan`; the
#      planner's process is the same one afterwards, no docs launch is logged
#      and no reload marker is left on docs or plan
#   B  control — `muxcode reload plan` relaunches the planner: a new process
#      and a reload launch row for plan
#   C  control — a pre-fix build (the guard removed from ReloadAgent in a copy
#      of the tree) replaces the planner on `reload docs`: a new process, and
#      a launch row for docs in plan's pane
# A coverage floor keeps a skipped section from reporting green.
# Requires go, tmux, jq. ~1 min.
set -uo pipefail
export MUXCODE_CONSUMER_GUARD=0

PASS=0
FAIL=0
EXPECTED_PASS=13
for t in tmux go jq; do
  command -v "$t" >/dev/null 2>&1 || { echo "SKIP: $t is required"; exit 2; }
done

REPO=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
WORK=$(mktemp -d /tmp/reload-hosted-XXXXXX)
. "$REPO/scripts/lib/muxcode-bus-dir.sh"

unset AGENT_ROLE BUS_ROLE BUS_SESSION TMUX TMUX_PANE MUXCODE MUXCODE_CONFIG MUXCODE_BUS_BASE MUXCODE_BUS_DIR
export HOME="$WORK/home"
export XDG_STATE_HOME="$WORK/home/.local/state"
export TMUX_TMPDIR="$WORK/tmux"
export MUXCODE_LIFECYCLE_LOG_DIR="$WORK/lifecycle"
export MUXCODE_INSTALL_DIR="$WORK/install"
export BUS_MEMORY_DIR="$WORK/memory" BUS_GLOBAL_MEMORY_DIR="$WORK/global-memory"
export MUXCODE_AGENT_CLI=claude MUXCODE_PLAN_CLI=claude MUXCODE_DOCS_CLI=claude
export MUXCODE_BRANCH_TIME_DISABLE=1 MUXCODE_DEDUP_WINDOW=0
mkdir -p "$WORK/bin" "$WORK/bin-pre" "$WORK/home" "$WORK/tmux" "$WORK/lifecycle" "$WORK/install/agents" "$WORK/project"

cleanup() {
  tmux kill-server 2>/dev/null
  sleep 1
  rm -rf "$WORK" 2>/dev/null
  true
}
trap cleanup EXIT

ok()   { PASS=$((PASS + 1)); echo "  ok: $1"; }
fail() { FAIL=$((FAIL + 1)); echo "  FAIL: $1"; }
wait_for() {
  local secs=$1 desc=$2 i=0
  shift 2
  while [ "$i" -lt "$secs" ]; do
    if "$@" >/dev/null 2>&1; then ok "$desc"; return 0; fi
    sleep 1; i=$((i + 1))
  done
  fail "$desc (timeout ${secs}s)"
  return 1
}
check() { local desc=$1; shift; if "$@" >/dev/null 2>&1; then ok "$desc"; else fail "$desc"; fi; }

echo "=== MUX-227 reloading a hosted role ==="

(cd "$REPO/tools/muxcode" && go build -buildvcs=false -o "$WORK/bin/muxcode" .) \
  || { echo "SKIP: go build muxcode failed"; exit 2; }
(cd "$REPO/scripts/fixtures/claude-stub" && go build -buildvcs=false -o "$WORK/bin/claude" .) \
  || { echo "SKIP: go build claude-stub failed"; exit 2; }
cp "$WORK/bin/claude" "$WORK/bin-pre/claude"
ok "scratch muxcode and claude-stub built from this tree"

mkdir -p "$WORK/pre"
cp -R "$REPO/tools/muxcode" "$WORK/pre/"
perl -0pi -e 's/\tif err := hostedRoleRefusal\(role, "reload"\); err != nil \{\n\t\treturn err\n\t\}\n//' "$WORK/pre/muxcode/bus/reload.go"
if [ "$(grep -c 'hostedRoleRefusal(role, "reload")' "$WORK/pre/muxcode/bus/reload.go")" = 1 ] \
  && (cd "$WORK/pre/muxcode" && go build -buildvcs=false -o "$WORK/bin-pre/muxcode" .); then
  ok "pre-fix muxcode built: ReloadAgent's hosted-role guard removed"
else
  fail "pre-fix build: the guard removal did not apply exactly once, or the copy failed to build"
fi

printf -- '---\ndescription: Planner\n---\nPlan.\n' >"$WORK/install/agents/planner.md"
printf -- '---\ndescription: Doc writer\n---\nWrite docs.\n' >"$WORK/install/agents/doc-writer.md"

# start_session NAME BINDIR — a private tmux session whose plan window runs the
# claude-stub as plan's launched agent on BINDIR's muxcode. Sets SESSION,
# BUSDIR, LIFELOG, MUX_NOW.
start_session() {
  SESSION="reload-hosted-$1-$$"
  MUX_NOW="$2/muxcode"
  BUSDIR="$(muxcode_bus_dir "$MUX_NOW" "$SESSION")" || exit 1
  LIFELOG="$MUXCODE_LIFECYCLE_LOG_DIR/$SESSION.log"
  export BUS_SESSION="$SESSION"
  tmux new-session -d -s "$SESSION" -n plan -x 160 -y 40 -c "$WORK/project" "/bin/bash --noprofile --norc"
  tmux set-option -g default-command "/bin/bash --noprofile --norc"
  tmux split-window -h -t "$SESSION:plan" -c "$WORK/project"
  type_in "export AGENT_ROLE=plan BUS_ROLE=plan BUS_SESSION=$SESSION HOME=$HOME XDG_STATE_HOME=$XDG_STATE_HOME TMUX_TMPDIR=$TMUX_TMPDIR MUXCODE_LIFECYCLE_LOG_DIR=$MUXCODE_LIFECYCLE_LOG_DIR MUXCODE_INSTALL_DIR=$MUXCODE_INSTALL_DIR MUXCODE_AGENT_CLI=claude MUXCODE_PLAN_CLI=claude MUXCODE_DOCS_CLI=claude MUXCODE_CONSUMER_GUARD=0 PATH=$2:$PATH; PS1='\$ '; clear"
  sleep 1
  "$MUX_NOW" init >/dev/null 2>&1 || true
  type_in "muxcode agent launch plan --reason user"
}
type_in()   { tmux send-keys -t "$SESSION:plan.1" -l -- "$1"; sleep 0.1; tmux send-keys -t "$SESSION:plan.1" Enter; }
pane_pid()  { tmux display-message -p -t "$SESSION:plan.1" '#{pane_pid}' 2>/dev/null; }
stub_pid()  { pgrep -P "$(pane_pid || echo 0)" -x claude 2>/dev/null | head -1 || true; }
stub_up()   { [ -n "$(stub_pid)" ] && [ -n "$(cat "$BUSDIR/polling-plan.marker" 2>/dev/null)" ]; }
stub_replaced() { local p; p=$(stub_pid); [ -n "$p" ] && [ "$p" != "$1" ]; }
launch_rows() { jq -r 'select(.event == "launch") | .detail' "$LIFELOG" 2>/dev/null | grep -E -- "$1" | grep -c . || true; }
reload_marker() { [ -e "$BUSDIR/lock/$1.reloading" ]; }

# ── A: reload docs is refused ──────────────────────────────────────
echo "-- A: muxcode reload docs"
start_session a "$WORK/bin"
wait_for 30 "A: plan's agent (claude-stub) up with its inbox listener" stub_up
P1=$(stub_pid)
OUT=$("$MUX_NOW" reload docs 2>&1); RC=$?
[ "$RC" -ne 0 ] && printf '%s' "$OUT" | grep -qF 'docs runs inside plan'"'"'s pane — use `muxcode reload plan`' \
  && ok "A: reload docs exits $RC naming \`muxcode reload plan\`" || fail "A: exit $RC: $OUT"
sleep 5
check "A: the planner is the same process ($P1) five seconds later" [ "$(stub_pid)" = "$P1" ]
check "A: no docs launch was logged" [ "$(launch_rows '^role=docs ')" = 0 ]
no_markers() { ! reload_marker docs && ! reload_marker plan; }
check "A: no reload marker is left on docs or plan" no_markers

# ── B: control — reload plan relaunches the host ───────────────────
echo "-- B: control — muxcode reload plan"
BEFORE=$(launch_rows '^role=plan .*reason=reload')
"$MUX_NOW" reload plan >"$WORK/reload-plan.out" 2>&1; RC=$?
check "B: reload plan exits 0" [ "$RC" -eq 0 ]
wait_for 30 "B: the planner was relaunched — a new process" stub_replaced "$P1"
check "B: a reload launch row for plan was logged" [ "$(launch_rows '^role=plan .*reason=reload')" -gt "$BEFORE" ]

# ── C: control — the pre-fix build replaces the host ───────────────
echo "-- C: control — reload docs on a build without the guard"
start_session c "$WORK/bin-pre"
wait_for 30 "C: plan's agent up" stub_up
P2=$(stub_pid)
"$MUX_NOW" reload docs >"$WORK/reload-docs-pre.out" 2>&1
wait_for 30 "C: the planner's process was replaced — the host is gone" stub_replaced "$P2"
docs_launched() { [ "$(launch_rows '^role=docs ')" -ge 1 ]; }
wait_for 15 "C: a docs agent was launched into plan's pane — the defect" docs_launched

# ── Coverage floor ─────────────────────────────────────────────────
echo
echo "=== results: $PASS passed, $FAIL failed (floor $EXPECTED_PASS) ==="
if [ "$PASS" -ne "$EXPECTED_PASS" ]; then
  echo "  FAIL  coverage floor: expected exactly $EXPECTED_PASS passes, got $PASS — a section was skipped or double-counted"
  exit 1
fi
[ "$FAIL" -eq 0 ] || exit 1
exit 0
