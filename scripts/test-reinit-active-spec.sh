#!/usr/bin/env bash
# Integration test for MUX-168: a session re-init keeps the active-spec
# pointer, and every change to it through `muxcode spec` leaves a lifecycle row.
#
# Covers:
#   1. A fresh bus dir: `muxcode init` creates no pointer.
#   2. `spec set` as plan: the pointer is written and a `spec-set` row names
#      actor=plan and the path.
#   3. Re-init (`muxcode init` over the existing bus dir), twice: `spec get`
#      still prints the path and each re-init writes `spec-kept <path>
#      live-runs=0`.
#   4. `spec clear` with no role: the pointer goes and a `spec-clear` row names
#      actor=user and the path it removed.
#   5. Negative controls — re-init preserves, it never resurrects or invents:
#      re-init after the clear leaves no pointer and writes `spec-none`, not
#      `spec-kept`; a second session that never held a pointer, re-inited
#      twice, has none and no `spec-kept` row.
#
# The live-run count beside a kept pointer, the daemon's spec-unset-live-run
# alert and loop-check across a re-init are pinned by Go tests
# (TestInit_ReInit_CountsLiveRunsBesideKeptSpec,
# TestCheckActiveSpec_UnsetUnderLiveRunAlertedOnce,
# TestSpecToPRLoopCheckSurvivesReInit).
#
# ISOLATION: builds muxcode from the tree (MUXCODE_REINIT_SPEC_BIN overrides);
# scratch HOME, XDG_STATE_HOME, lifecycle log dir, config and BUS_SESSION, so
# no live bus or log is touched. No daemon, no tmux.
#
# REQUIRES: go (unless MUXCODE_REINIT_SPEC_BIN is set). A few seconds.
#
# Usage: bash scripts/test-reinit-active-spec.sh
#        MUXCODE_TEST_KEEP=1 bash scripts/test-reinit-active-spec.sh   # keep WORK
set -uo pipefail

REPO_ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)

GREEN=$'\033[0;32m'; RED=$'\033[0;31m'; NC=$'\033[0m'
pass=0; fail=0
ok()  { echo "  ${GREEN}PASS${NC}  $*"; pass=$((pass + 1)); }
bad() { echo "  ${RED}FAIL${NC}  $*"; fail=$((fail + 1)); }
check() { local msg="$1"; shift; if "$@"; then ok "$msg"; else bad "$msg"; fi; }
same() { [ "$1" = "$2" ] || { echo "        got: '$1'  want: '$2'"; return 1; }; }

SESSION="reinit-spec-$$"
FRESH="reinit-nospec-$$"
WORK=$(mktemp -d "${TMPDIR:-/tmp}/reinit-spec-XXXXXX")
REPO="$WORK/repo"
SPEC="docs/requirements/drafts/MUX-168-fixture.md"

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
mkdir -p "$HOME/.config/muxcode" "$WORK/lifecycle" "$REPO/$(dirname "$SPEC")"
: > "$WORK/empty-config"
export MUXCODE_CONFIG="$WORK/empty-config"
export MUXCODE_LIFECYCLE_LOG_DIR="$WORK/lifecycle"
export MUXCODE_SESSION_REPO_DIR="$REPO"
unset MUXCODE_BUS_BASE MUXCODE_BUS_DIR AGENT_ROLE BUS_ROLE

if [ -n "${MUXCODE_REINIT_SPEC_BIN:-}" ]; then
  MUX="$MUXCODE_REINIT_SPEC_BIN"
else
  command -v go >/dev/null 2>&1 || { echo "SKIP: go is required (or set MUXCODE_REINIT_SPEC_BIN)"; exit 2; }
  MUX="$WORK/muxcode"
  (cd "$REPO_ROOT/tools/muxcode" && go build -buildvcs=false -o "$MUX" .) \
    || { echo "SKIP: go build muxcode failed"; exit 2; }
fi

printf '# MUX-168 fixture\n\n### Phase 1: A\n- [ ] open\n' > "$REPO/$SPEC"
cd "$REPO" || exit 1

mux()      { BUS_SESSION="$1" "$MUX" "${@:2}"; }
reinit()   { mux "$1" init --memory-dir "$WORK/memory-$1" >/dev/null 2>&1; }
spec_get() { mux "$1" spec get 2>/dev/null; }
# rows <session> <event> — the details of that session's rows for one event.
rows() {
  mux "$1" lifecycle show "$1" --event "$2" --limit 1000 2>/dev/null \
    | grep -F "  $2" | sed -E "s/^.*  $2 +//"
}
row_count() { rows "$1" "$2" | grep -c . ; }
has_row()   { rows "$1" "$2" | grep -qxF "$3"; }
no_marker() { [ ! -e "$(mux "$1" bus-dir "$1" 2>/dev/null)/active-spec" ]; }

echo "MUX-168 re-init keeps the active-spec pointer (binary: $MUX)"

# --- 1. Fresh bus dir ------------------------------------------------------
check "first init succeeds" reinit "$SESSION"
check "fresh bus dir has no active-spec marker" no_marker "$SESSION"
check "spec get on a fresh bus reports none" same "$(spec_get "$SESSION")" "No active spec set"

# --- 2. spec set -----------------------------------------------------------
check "spec set succeeds as plan" bash -c "BUS_SESSION='$SESSION' BUS_ROLE=plan '$MUX' spec set '$SPEC' >/dev/null 2>&1"
check "spec get prints the set path" same "$(spec_get "$SESSION")" "$SPEC"
check "spec-set row names actor=plan and the path" has_row "$SESSION" spec-set "actor=plan path=$SPEC"

# --- 3. Re-init keeps the pointer, twice ----------------------------------
check "re-init succeeds" reinit "$SESSION"
check "spec get after re-init prints the same path" same "$(spec_get "$SESSION")" "$SPEC"
check "re-init wrote spec-kept with the path and live-runs=0" has_row "$SESSION" spec-kept "$SPEC live-runs=0"
check "second re-init succeeds" reinit "$SESSION"
check "spec get after the second re-init still prints the path" same "$(spec_get "$SESSION")" "$SPEC"
check "each re-init wrote one spec-kept row" same "$(row_count "$SESSION" spec-kept)" 2

# --- 4. spec clear ---------------------------------------------------------
check "spec clear succeeds with no role" bash -c "BUS_SESSION='$SESSION' '$MUX' spec clear >/dev/null 2>&1"
check "spec get after clear reports none" same "$(spec_get "$SESSION")" "No active spec set"
check "spec-clear row names actor=user and the cleared path" has_row "$SESSION" spec-clear "actor=user path=$SPEC"

# --- 5. Negative controls --------------------------------------------------
check "re-init after the clear succeeds" reinit "$SESSION"
check "a cleared pointer stays cleared across re-init" same "$(spec_get "$SESSION")" "No active spec set"
check "no marker resurrected by the re-init" no_marker "$SESSION"
check "re-init after the clear wrote spec-none" same "$(row_count "$SESSION" spec-none)" 1
check "re-init after the clear wrote no further spec-kept" same "$(row_count "$SESSION" spec-kept)" 2

check "never-pointed session: first init" reinit "$FRESH"
check "never-pointed session: first re-init" reinit "$FRESH"
check "never-pointed session: second re-init" reinit "$FRESH"
check "never-pointed session has no marker after two re-inits" no_marker "$FRESH"
check "never-pointed session reports none" same "$(spec_get "$FRESH")" "No active spec set"
check "never-pointed session wrote spec-none per re-init" same "$(row_count "$FRESH" spec-none)" 2
check "never-pointed session wrote no spec-kept" same "$(row_count "$FRESH" spec-kept)" 0
check "never-pointed session wrote no spec-set" same "$(row_count "$FRESH" spec-set)" 0

# --- Coverage floor --------------------------------------------------------
# 1: 3; 2: 3; 3: 6; 4: 3; 5: 5 + 8 = 28.
FLOOR=28
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
