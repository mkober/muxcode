#!/usr/bin/env bash
# Integration test for MUX-229: the codexTestHome tests leave nothing behind in
# their temporary HOME.
#
# Under a fresh HOME, the `go env` that build and test launch args run
# (goToolchainRoots) starts Go's detached telemetry child, which keeps writing
# $HOME/Library/Application Support/go/telemetry after go returns. It usually
# recreates its files after t.TempDir's cleanup — 171 leftover trees for each of
# two tests piled up in TMPDIR — and twice in 87 suite runs did so during it,
# failing TestCodexBuildExecArgs_NoShimWithoutInstallDir "directory not empty".
# codexTestHome now writes telemetry mode "off" for the temp HOME.
#
#   A. Fixed tree: the TestCodex* tests at -count=COUNT under a scratch TMPDIR
#      pass with zero `TempDir RemoveAll cleanup` failures, leave zero
#      TestCodex* trees behind, and TestCodexTestHomeStartsNoGoTelemetry ran
#      every iteration (not skipped).
#   B. Control: a throwaway copy with goTelemetryOff's call removed from
#      codexTestHome fails TestCodexTestHomeStartsNoGoTelemetry and leaves
#      TestCodex* trees behind at the same count — the child outliving the test,
#      which the cleanup race needs. The rare race failure itself is reported,
#      not required: Phase 1 saw it 0 times in 25,800 isolated runs, so the
#      script says whether this run reproduced it rather than counting on it.
#
# COUNT defaults to 20 (Phase 2: 20 leftover trees without the fix, 0 with it);
# MUXCODE_CODEX_HOME_COUNT overrides it.
#
# ISOLATION: two copies of the tree in a scratch dir (no .git), each with its
# own scratch TMPDIR. The real HOME is kept, so the go commands this script
# runs may still write the user's Go build/module caches and telemetry; only
# the tests' own temp HOMEs are scratch. REQUIRES: go. ~3 min.
#
# Usage: bash scripts/test-codex-home-cleanup.sh
set -uo pipefail

FLOOR=6
COUNT="${MUXCODE_CODEX_HOME_COUNT:-20}"
REPO=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
PASS=0
FAIL=0
ok()   { PASS=$((PASS + 1)); echo "  ok: $1"; }
fail() { FAIL=$((FAIL + 1)); echo "  FAIL: $1"; }

command -v go >/dev/null 2>&1 || { echo "SKIP: go is required"; exit 2; }
command -v rsync >/dev/null 2>&1 || { echo "SKIP: rsync is required"; exit 2; }
WORK=$(mktemp -d /tmp/codex-home-cleanup-XXXXXX)
trap 'rm -rf "$WORK"' EXIT
GOCACHE="$(go env GOCACHE)"; GOMODCACHE="$(go env GOMODCACHE)"
export GOCACHE GOMODCACHE
unset TMUX TMUX_PANE BUS_SESSION AGENT_ROLE BUS_ROLE

# leftovers DIR — TestCodex* temp trees left in a scratch TMPDIR.
leftovers() { local n; n=$(find "$1" -maxdepth 1 -type d -name 'TestCodex*' 2>/dev/null | grep -c .); echo "${n:-0}"; }
# run_codex TREE NAME — TestCodex* at -count=COUNT, verbose, under its own TMPDIR.
run_codex() {
  mkdir -p "$WORK/tmp-$2"
  (cd "$1/tools/muxcode" && TMPDIR="$WORK/tmp-$2" go test -count="$COUNT" -v -run '^TestCodex' ./bus >"$WORK/$2.log" 2>&1)
  RC=$?
  sleep 3 # a child still running when go test returns finishes its writes
}
count_in() { local n; n=$(grep -c -- "$1" "$2" 2>/dev/null); echo "${n:-0}"; }

echo "=== MUX-229 codexTestHome leaves nothing behind (count $COUNT) ==="
for d in fixed control; do rsync -a --exclude .git --exclude node_modules "$REPO/" "$WORK/$d/"; done
perl -pi -e 's/^\tgoTelemetryOff\(t\)\n//' "$WORK/control/tools/muxcode/bus/provider_codex_test.go"
if ! grep -q '^	goTelemetryOff(t)$' "$WORK/control/tools/muxcode/bus/provider_codex_test.go" \
  && grep -q '^	goTelemetryOff(t)$' "$WORK/fixed/tools/muxcode/bus/provider_codex_test.go" \
  && (cd "$WORK/control/tools/muxcode" && go vet ./bus >/dev/null 2>&1); then
  ok "two copies; the control has goTelemetryOff's call removed and builds"
else
  fail "copy or control patch failed"
fi

echo "-- A: the fixed tree"
run_codex "$WORK/fixed" fixed
ran=$(count_in '^--- PASS: TestCodexTestHomeStartsNoGoTelemetry ' "$WORK/fixed.log")
[ "$RC" = 0 ] && [ "$ran" = "$COUNT" ] \
  && ok "A: TestCodex* pass, TestCodexTestHomeStartsNoGoTelemetry ran $ran/$COUNT" \
  || { fail "A: exit $RC, the new test passed $ran/$COUNT"; grep -E -- '--- (FAIL|SKIP)|_test.go:[0-9]+:' "$WORK/fixed.log" | head -5; }
[ "$(count_in 'TempDir RemoveAll cleanup' "$WORK/fixed.log")" = 0 ] && ok "A: zero TempDir RemoveAll cleanup failures" \
  || fail "A: $(count_in 'TempDir RemoveAll cleanup' "$WORK/fixed.log") cleanup failures"
lf=$(leftovers "$WORK/tmp-fixed")
[ "$lf" = 0 ] && ok "A: no TestCodex* tree left in TMPDIR" || fail "A: $lf TestCodex* trees left behind"

echo "-- B: control — the fix reverted"
run_codex "$WORK/control" control
grep -q -- '^--- FAIL: TestCodexTestHomeStartsNoGoTelemetry ' "$WORK/control.log" \
  && ok "B: TestCodexTestHomeStartsNoGoTelemetry fails without the fix" || fail "B: the deterministic test did not fail without the fix"
lc=$(leftovers "$WORK/tmp-control")
[ "$lc" -gt 0 ] && ok "B: $lc TestCodex* trees left behind — the child outlives the test" \
  || fail "B: no tree left without the fix — the control could not see the child this run"
races=$(count_in 'TempDir RemoveAll cleanup' "$WORK/control.log")
if [ "$races" -gt 0 ]; then
  echo "  note: the control also reproduced the cleanup race $races time(s)"
else
  echo "  note: the control did not reproduce the cleanup race itself in $COUNT runs (seen 0 in 25,800 isolated runs, Phase 1); the leftover trees are the reachable evidence"
fi

total=$((PASS + FAIL))
echo
echo "=== test-codex-home-cleanup: $PASS passed, $FAIL failed ($total checks, floor $FLOOR) ==="
[ "$total" -lt "$FLOOR" ] && { echo "  FAIL: only $total checks ran (floor $FLOOR) — a section was skipped"; exit 1; }
[ "$FAIL" = 0 ]
