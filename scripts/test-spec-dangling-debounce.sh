#!/usr/bin/env bash
# Integration test for MUX-228 — `spec-dangling` waits out a transient gap.
#
# On 2026-11-14 a ship checked out main a few seconds before the pull brought
# a closed-out spec back, and the daemon told edit the active spec pointer
# named a missing file. checkActiveSpec now reports a dangling pointer only
# once it has stayed dangling across consecutive checks for
# MUXCODE_SPEC_DANGLING_DEBOUNCE_SECS; a present file or a changed pointer
# restarts the wait.
#
# Hermetic: muxcode built from this tree (MUXCODE_SPEC_DANGLING_BIN
# overrides), scratch HOME, state, repo and lifecycle log, a private tmux
# server under the work dir (killed on exit), a scratch daemon polling every
# second. ~1 min.
#
# Sections:
#   0  control — debounce 0 (the pre-MUX-228 behaviour): a 3 s gap raises a
#      row, so the gaps below are seen by the daemon and their silence is
#      the debounce, not a missed check
#   1  debounce 6 s: a 3 s gap raises no row and no event to edit
#   2  two 4 s gaps 1 s apart — 8 s absent in all, neither past 6 s — raise
#      nothing: the file's return restarts the wait
#   3  negative control — the file removed for good: nothing before the
#      debounce, then exactly one row and one event, and the pointer kept
# A coverage floor keeps a skipped section from reporting green.
#
# Usage: bash scripts/test-spec-dangling-debounce.sh
set -uo pipefail
export MUXCODE_CONSUMER_GUARD=0 # edit is played by reading its inbox, which no window or listener shows (MUX-145)

FLOOR=14
DEBOUNCE=6
for t in go tmux; do
  command -v "$t" >/dev/null 2>&1 || { echo "SKIP: $t is required"; exit 2; }
done

SRC="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORK="$(mktemp -d /tmp/spec-dangling-XXXXXX)"
. "$SRC/scripts/lib/muxcode-bus-dir.sh"

# The private tmux server is fixed before any trap can run: cleanup kills only
# this socket, by path, so a failure at any point never reaches the caller's
# server.
unset TMUX
export TMUX_TMPDIR="$WORK/tmux"
mkdir -p "$TMUX_TMPDIR"
PRIVATE_SOCK="$TMUX_TMPDIR/tmux-$(id -u)/default"

GREEN=$'\033[0;32m'; RED=$'\033[0;31m'; NC=$'\033[0m'
pass=0; fail=0
ok()  { echo "  ${GREEN}PASS${NC}  $*"; pass=$((pass + 1)); }
bad() { echo "  ${RED}FAIL${NC}  $*"; fail=$((fail + 1)); }

DPID=""
cleanup() {
  [ -n "$DPID" ] && kill "$DPID" 2>/dev/null
  [ -S "$PRIVATE_SOCK" ] && tmux -S "$PRIVATE_SOCK" kill-server 2>/dev/null
  rm -rf "$WORK"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

if [ -n "${MUXCODE_SPEC_DANGLING_BIN:-}" ]; then
  MUX="$MUXCODE_SPEC_DANGLING_BIN"
  # Absolute: the script runs muxcode from inside the scratch repo too.
  case "$MUX" in
    /*) ;;
    */*) [ -e "$MUX" ] && MUX="$(cd "$(dirname "$MUX")" && pwd)/$(basename "$MUX")" ;;
    *) MUX="$(command -v "$MUX" 2>/dev/null || echo "$MUX")" ;;
  esac
else
  MUX="$WORK/bin/muxcode"
  go -C "$SRC/tools/muxcode" build -buildvcs=false -o "$MUX" . \
    || { echo "SKIP: go build muxcode failed"; exit 2; }
fi
[ -x "$MUX" ] && ok "muxcode under test: $MUX" || { bad "no executable muxcode at $MUX"; exit 1; }

# --- Isolation -------------------------------------------------------------
unset AGENT_ROLE BUS_ROLE
export HOME="$WORK/home" XDG_STATE_HOME="$WORK/home/.local/state"
export MUXCODE_LIFECYCLE_LOG_DIR="$WORK/lifecycle"
mkdir -p "$HOME" "$MUXCODE_LIFECYCLE_LOG_DIR"
: > "$WORK/empty-config"
export MUXCODE_CONFIG="$WORK/empty-config"
export MUXCODE_TMP_CLEANUP_THRESHOLD=0 MUXCODE_BRANCH_TIME_DISABLE=1 MUXCODE_DEDUP_WINDOW=0
export MUXCODE_FORCE_RESPOND_DISABLE=1
REPO="$WORK/repo"
mkdir -p "$REPO/docs/requirements/drafts"
export MUXCODE_SESSION_REPO_DIR="$REPO"
export BUS_SESSION="spec-dangling-$$"
BD="$(muxcode_bus_dir "$MUX" "$BUS_SESSION")" || exit 1
LIFELOG="$MUXCODE_LIFECYCLE_LOG_DIR/$BUS_SESSION.log"

tmux new-session -d -s "$BUS_SESSION" -n edit -x 120 -y 30
"$MUX" init >/dev/null 2>&1

SPEC="docs/requirements/drafts/dangling-fixture.md"
printf '# Dangling Fixture\n\n- [ ] open\n\n## Status\n\nIn Progress\n' > "$REPO/$SPEC"
cp "$REPO/$SPEC" "$WORK/spec.copy"
(cd "$REPO" && "$MUX" spec set "$SPEC" >/dev/null 2>&1) \
  && ok "active spec set to the fixture" || bad "spec set failed"

rows()   { local c; c="$(grep -c '"event":"spec-dangling"' "$LIFELOG" 2>/dev/null)"; echo "${c:-0}"; }
events() { local c; c="$(grep -c '"action":"spec-dangling"' "$BD/log.jsonl" 2>/dev/null)"; echo "${c:-0}"; }
gap()    { rm -f "$REPO/$SPEC"; sleep "$1"; cp "$WORK/spec.copy" "$REPO/$SPEC"; }

# start_daemon SECS — a scratch daemon with that debounce, polling every second.
start_daemon() {
  if [ -n "$DPID" ]; then kill "$DPID" 2>/dev/null; wait "$DPID" 2>/dev/null; fi
  MUXCODE_SPEC_DANGLING_DEBOUNCE_SECS="$1" "$MUX" watch "$BUS_SESSION" --poll 1 >>"$WORK/daemon.log" 2>&1 &
  DPID=$!
  sleep 2
}

# --- 0. Control: with no debounce the same gap is reported ------------------
echo "--- 0. control: debounce 0 reports a 3 s gap"
start_daemon 0
kill -0 "$DPID" 2>/dev/null && ok "scratch daemon running (debounce 0)" \
  || bad "scratch daemon exited: $(tail -3 "$WORK/daemon.log")"
gap 3
sleep 1
[ "$(rows)" -ge 1 ] && ok "control: the 3 s gap was seen and reported ($(rows) row)" \
  || bad "control: no row for a 3 s gap with no debounce — the gaps below would prove nothing"
BASE_ROWS="$(rows)" BASE_EVENTS="$(events)"

# --- 1. A transient gap raises nothing ---------------------------------------
echo "--- 1. debounce ${DEBOUNCE}s: a 3 s gap"
start_daemon "$DEBOUNCE"
kill -0 "$DPID" 2>/dev/null && ok "scratch daemon restarted (debounce ${DEBOUNCE}s)" \
  || bad "scratch daemon exited: $(tail -3 "$WORK/daemon.log")"
gap 3
sleep $((DEBOUNCE + 3))
[ "$(rows)" = "$BASE_ROWS" ] && ok "no spec-dangling row for a 3 s gap" \
  || bad "a 3 s gap raised $(( $(rows) - BASE_ROWS )) row(s)"
[ "$(events)" = "$BASE_EVENTS" ] && ok "no spec-dangling event to edit for a 3 s gap" \
  || bad "a 3 s gap sent edit $(( $(events) - BASE_EVENTS )) event(s)"

# --- 2. The file's return restarts the wait ----------------------------------
echo "--- 2. two 4 s gaps, 1 s apart"
gap 4
sleep 1
gap 4
sleep $((DEBOUNCE + 3))
[ "$(rows)" = "$BASE_ROWS" ] && ok "no row: 8 s absent in all, the wait restarted at the return" \
  || bad "two short gaps raised $(( $(rows) - BASE_ROWS )) row(s) — the wait did not restart"
[ "$(events)" = "$BASE_EVENTS" ] && ok "no event to edit across the two gaps" \
  || bad "two short gaps sent edit $(( $(events) - BASE_EVENTS )) event(s)"

# --- 3. Negative control: a file gone for good is reported once -------------
echo "--- 3. negative control: the file removed for good"
rm -f "$REPO/$SPEC"
sleep 3
[ "$(rows)" = "$BASE_ROWS" ] && ok "nothing reported 3 s in, inside the debounce" \
  || bad "reported $(( $(rows) - BASE_ROWS )) row(s) inside the debounce"
for _ in $(seq 1 30); do [ "$(rows)" -gt "$BASE_ROWS" ] && break; sleep 0.5; done
sleep 3
[ "$(( $(rows) - BASE_ROWS ))" = 1 ] && ok "exactly one spec-dangling row past the debounce" \
  || bad "$(( $(rows) - BASE_ROWS )) rows past the debounce, want 1"
[ "$(( $(events) - BASE_EVENTS ))" = 1 ] && ok "exactly one spec-dangling event to edit" \
  || bad "$(( $(events) - BASE_EVENTS )) events to edit, want 1"
grep -q "$SPEC" "$BD/active-spec" 2>/dev/null && ok "the dangling pointer is kept, not cleared" \
  || bad "pointer is now '$(cat "$BD/active-spec" 2>/dev/null)'"
grep '"event":"spec-dangling"' "$LIFELOG" | tail -1 | grep -q "$SPEC" \
  && ok "the row names the missing spec" || bad "row: $(grep '"event":"spec-dangling"' "$LIFELOG" | tail -1)"

# --- Summary ---------------------------------------------------------------
echo
echo "=== Summary: $pass passed, $fail failed (floor $FLOOR) ==="
[ "$fail" -eq 0 ] || exit 1
if [ "$pass" -lt "$FLOOR" ]; then
  echo "  FAIL  only $pass checks passed — below the coverage floor of $FLOOR"
  exit 1
fi
exit 0
