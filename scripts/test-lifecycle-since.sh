#!/usr/bin/env bash
# Integration test for MUX-124: `lifecycle show --since` answers the question
# it was asked, and any cut result says so on stderr.
#
# Covers:
#   1. `--since 8h` over a window holding 70 rows (more than the default 50)
#      prints all 70 and nothing on stderr.
#   2. `--since 8h --limit 10` prints 10 rows and `showing last 10 of 70` on
#      stderr; stdout holds no notice text.
#   3. Unscoped `show` prints 50 rows and `showing last 50 of 90`; stdout holds
#      no notice text. A cut `--source`, `--event` (default limit) and
#      `--level` result each get the same stderr notice and a clean stdout.
#   4. Negative controls: a window holding no rows exits 0 and prints nothing
#      on either stream; a result within the default limit prints no notice.
#      Every query's exit status is asserted, so a crash cannot pass as empty.
#   5. Control: a build from a copy of the tree with the `--since` rule removed
#      from resolveShowLimit returns 50 for check 1, so that assertion can fail.
#
# ISOLATION: builds muxcode from the tree (MUXCODE_LIFECYCLE_SINCE_BIN overrides
# the binary under test); a scratch HOME, config and lifecycle log dir holding
# a hand-written fixture log. No daemon, no tmux, nothing written into the
# checkout — the control build compiles a copy under the work dir.
#
# REQUIRES: go (the control build always needs it). A few seconds.
#
# Usage: bash scripts/test-lifecycle-since.sh
#        MUXCODE_TEST_KEEP=1 bash scripts/test-lifecycle-since.sh   # keep WORK
set -uo pipefail

REPO_ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)

GREEN=$'\033[0;32m'; RED=$'\033[0;31m'; NC=$'\033[0m'
pass=0; fail=0
ok()  { echo "  ${GREEN}PASS${NC}  $*"; pass=$((pass + 1)); }
bad() { echo "  ${RED}FAIL${NC}  $*"; fail=$((fail + 1)); }
check() { local msg="$1"; shift; if "$@"; then ok "$msg"; else bad "$msg"; fi; }
same() { [ "$1" = "$2" ] || { echo "        got: '$1'  want: '$2'"; return 1; }; }
lacks() { ! grep -qF "$2" "$1"; }
empty_file() { [ ! -s "$1" ] || { echo "        unexpected: $(head -c 200 "$1")"; return 1; }; }

SESSION="lifecycle-since-$$"
WORK=$(mktemp -d "${TMPDIR:-/tmp}/lifecycle-since-XXXXXX")

cleanup() {
  cd / 2>/dev/null
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
mkdir -p "$HOME/.config/muxcode" "$WORK/lifecycle"
: > "$WORK/empty-config"
export MUXCODE_CONFIG="$WORK/empty-config"
export MUXCODE_LIFECYCLE_LOG_DIR="$WORK/lifecycle"
export BUS_SESSION="$SESSION"
unset MUXCODE_BUS_BASE MUXCODE_BUS_DIR AGENT_ROLE BUS_ROLE

have_go=0
command -v go >/dev/null 2>&1 && have_go=1

if [ -n "${MUXCODE_LIFECYCLE_SINCE_BIN:-}" ]; then
  MUX="$MUXCODE_LIFECYCLE_SINCE_BIN"
else
  [ "$have_go" = 1 ] || { echo "SKIP: go is required (or set MUXCODE_LIFECYCLE_SINCE_BIN)"; exit 2; }
  MUX="$WORK/muxcode"
  (cd "$REPO_ROOT/tools/muxcode" && go build -buildvcs=false -o "$MUX" .) \
    || { echo "SKIP: go build muxcode failed"; exit 2; }
fi

# --- Fixture ---------------------------------------------------------------
# 20 launcher rows 20 h ago (outside an 8 h window), then 70 daemon task-stall
# rows 1 h ago (inside it, but outside a 30 m window). 90 rows in all.
NOW=$(date +%s)
LOG="$WORK/lifecycle/$SESSION.log"
for _ in $(seq 1 20); do
  printf '{"ts":%d,"level":"info","source":"launcher","session":"%s","event":"old-event"}\n' $((NOW - 72000)) "$SESSION"
done > "$LOG"
for i in $(seq 1 70); do
  printf '{"ts":%d,"level":"warn","source":"daemon","session":"%s","event":"task-stall","detail":"n=%d"}\n' $((NOW - 3600)) "$SESSION" "$i"
done >> "$LOG"

# show <bin> <name> <args...> — runs `lifecycle show` for the fixture session,
# stdout to $WORK/<name>.out, stderr to $WORK/<name>.err, and asserts it exited
# 0: a crashed query prints nothing too, so an empty stream proves nothing alone.
show() {
  local bin="$1" name="$2"; shift 2
  "$bin" lifecycle show "$SESSION" "$@" >"$WORK/$name.out" 2>"$WORK/$name.err"
  check "$name: lifecycle show $* exits 0" same "$?" 0
}
lines() { grep -c . "$1"; }

echo "MUX-124 lifecycle show --since honours its window (binary: $MUX)"

# --- 1. --since shows the whole window -------------------------------------
show "$MUX" since --since 8h
check "--since 8h prints all 70 in-window rows" same "$(lines "$WORK/since.out")" 70
check "--since 8h rows are all task-stall" same "$(grep -c 'task-stall' "$WORK/since.out")" 70
check "--since 8h prints no out-of-window row" lacks "$WORK/since.out" "old-event"
check "--since 8h prints nothing on stderr" empty_file "$WORK/since.err"

# --- 2. --since with an explicit --limit -----------------------------------
show "$MUX" since-limit --since 8h --limit 10
check "--since 8h --limit 10 prints 10 rows" same "$(lines "$WORK/since-limit.out")" 10
check "--since 8h --limit 10 keeps the last rows" same "$(tail -n 1 "$WORK/since-limit.out" | grep -o 'n=[0-9]*')" "n=70"
check "--since 8h --limit 10 notice on stderr" same "$(cat "$WORK/since-limit.err")" "showing last 10 of 70"
check "--since 8h --limit 10 stdout holds no notice" lacks "$WORK/since-limit.out" "showing last"

# --- 3. Unscoped and filtered truncation -----------------------------------
show "$MUX" unscoped
check "unscoped show prints the default 50 rows" same "$(lines "$WORK/unscoped.out")" 50
check "unscoped show notice on stderr" same "$(cat "$WORK/unscoped.err")" "showing last 50 of 90"
check "unscoped show stdout holds no notice" lacks "$WORK/unscoped.out" "showing last"

show "$MUX" source --source launcher --limit 5
check "--source launcher --limit 5 prints 5 rows" same "$(lines "$WORK/source.out")" 5
check "--source launcher --limit 5 notice on stderr" same "$(cat "$WORK/source.err")" "showing last 5 of 20"
check "--source launcher --limit 5 stdout holds no notice" lacks "$WORK/source.out" "showing last"

show "$MUX" event --event task-stall
check "--event task-stall prints the default 50 rows" same "$(lines "$WORK/event.out")" 50
check "--event task-stall notice on stderr" same "$(cat "$WORK/event.err")" "showing last 50 of 70"
check "--event task-stall stdout holds no notice" lacks "$WORK/event.out" "showing last"

show "$MUX" level --level warn --limit 7
check "--level warn --limit 7 prints 7 rows" same "$(lines "$WORK/level.out")" 7
check "--level warn --limit 7 notice on stderr" same "$(cat "$WORK/level.err")" "showing last 7 of 70"
check "--level warn --limit 7 stdout holds no notice" lacks "$WORK/level.out" "showing last"

# --- 4. Negative controls --------------------------------------------------
show "$MUX" empty --since 30m
check "an empty window prints nothing on stdout" empty_file "$WORK/empty.out"
check "an empty window prints nothing on stderr" empty_file "$WORK/empty.err"

show "$MUX" within --event old-event
check "a result within the limit prints every row" same "$(lines "$WORK/within.out")" 20
check "a result within the limit prints no notice" empty_file "$WORK/within.err"

# --- 5. Control: the --since rule reverted ---------------------------------
CONTROL="$WORK/muxcode-control"
if [ "$have_go" = 1 ]; then
  mkdir -p "$WORK/control-src"
  cp -R "$REPO_ROOT/tools/muxcode" "$WORK/control-src/"
  CSRC="$WORK/control-src/muxcode/cmd/lifecycle.go"
  awk '/case since > 0:/ { skip = 1; next } skip { skip = 0; next } { print }' "$CSRC" > "$CSRC.new" \
    && mv "$CSRC.new" "$CSRC"
  if grep -q 'case since > 0:' "$CSRC"; then
    bad "control: the --since rule could not be removed from the copy"
  elif (cd "$WORK/control-src/muxcode" && go build -buildvcs=false -o "$CONTROL" .) 2>"$WORK/control-build.err"; then
    show "$CONTROL" control --since 8h
    check "control: without the rule --since 8h is cut to 50 rows" same "$(lines "$WORK/control.out")" 50
    check "control: the cut is announced on stderr" same "$(cat "$WORK/control.err")" "showing last 50 of 70"
  else
    bad "control: build of the reverted copy failed ($(head -c 200 "$WORK/control-build.err"))"
  fi
else
  bad "control: go is required to build the reverted copy"
fi

# --- Coverage floor --------------------------------------------------------
# 1: 4; 2: 4; 3: 12; 4: 4; 5: 2 = 26, plus one exit-status check per show
# call (9) = 35.
FLOOR=35
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
