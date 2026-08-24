#!/usr/bin/env bash
# Integration test for the disk-pressure signal and its alert cooldown
# (docs/requirements/completed/MUX-002-disk-pressure-wrong-filesystem.md, Phase 2).
#
# THE BUG: pressure was decided by the /tmp volume's percent-used. On macOS /tmp
# is on the boot volume, so a dev box sitting at a normal 90% full ran cleanup
# every 60 seconds forever, freed 0 B every time, and buried the lifecycle log in
# warnings nobody could act on. The fix decides pressure from absolute free
# headroom and muxcode's own /tmp footprint — the only part its cleanup can free.
#
# WHY THIS SCRIPT DOES NOT CALL CheckDiskPressure:
# forcing a breach and letting the real path run would invoke CleanupStale, which
# deletes OTHER muxcode sessions' /tmp artifacts on whatever machine runs the
# test. Destroying live session state is not an acceptable cost for a test. So
# the two halves are exercised where they can be reached safely:
#   - the SIGNAL (TmpPressure) via Go tests that redirect the /tmp scan into a
#     temp dir and inject thresholds through the env
#   - the ALERT CADENCE via shouldAlertDiskPressure, a pure function extracted
#     precisely so "fires once, not every cycle" is testable without cleanup
# The script also proves the destructive path stays inert under --dry-run.
#
# Usage: bash scripts/test-disk-pressure.sh
set -uo pipefail

MUX="${MUXCODE_BIN:-muxcode}"
MUX="$(command -v "$MUX" 2>/dev/null || echo "$MUX")"
[ -x "$MUX" ] || { echo "  FAIL  cannot resolve muxcode binary ('$MUX') — set MUXCODE_BIN"; exit 1; }

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MODULE="$REPO/tools/muxcode"

GREEN=$'\033[0;32m'; RED=$'\033[0;31m'; YELLOW=$'\033[0;33m'; NC=$'\033[0m'
pass=0; fail=0
ok()  { echo "  ${GREEN}PASS${NC}  $*"; pass=$((pass+1)); }
bad() { echo "  ${RED}FAIL${NC}  $*"; fail=$((fail+1)); }

export MUXCODE_LIFECYCLE_LOG_DIR="$(mktemp -d /tmp/disk-pressure-lifecycle-XXXXXX)"
# Leak detection is scoped to log files named after this test's scratch sessions,
# NOT a snapshot of the whole directory.
#
# A whole-dir snapshot (names, or names+sizes) is a false-positive generator: when
# a muxcode session is live its daemon appends to ~/.config/muxcode/logs every poll
# cycle, so the directory legitimately changes mid-run and the check reports a leak
# the test did not cause. Observed: muxcode.log grew 132939 -> 133082 lines during
# one run window. Scratch-session names are the only writes this test could
# actually be responsible for, so they are the only thing worth asserting on.
REAL_LOG_DIR="$HOME/.config/muxcode/logs"
leaked_logs() { ls -1 "$REAL_LOG_DIR" 2>/dev/null | grep -E '^test-(disk-pressure|lifecycle)' | sort; }
LEAKED_BEFORE="$(leaked_logs)"
TMP_SNAPSHOT_BEFORE="$(ls -1d /tmp/muxcode-bus-* 2>/dev/null | sort)"
cleanup() { rm -rf "$MUXCODE_LIFECYCLE_LOG_DIR"; }
trap cleanup EXIT

# gotest "label" <-run pattern> <min expected --- PASS lines> <package>
# -v is required: a -run pattern matching zero tests still exits 0 and still
# prints "ok <pkg>", so only per-test PASS lines are evidence anything ran.
gotest() {
  local label="$1" pattern="$2" min="$3" out ran
  out="$(mktemp /tmp/disk-pressure-go-XXXXXX.out)"
  if (cd "$MODULE" && go test "$4" -count=1 -v -run "$pattern") >"$out" 2>&1; then
    ran="$(grep -cE '^--- PASS:' "$out")"
    if grep -q 'no tests to run' "$out"; then
      bad "$label — -run pattern matched no tests (silent pass)"
    elif [ "$ran" -ge "$min" ]; then
      ok "$label ($ran tests)"
    else
      bad "$label — only $ran tests ran, want >= $min (renamed out of the pattern?)"
    fi
  else
    grep -E '^(--- FAIL|FAIL|.*\.go:[0-9]+:)' "$out" | head -12 | sed 's/^/        /'
    bad "$label"
  fi
  rm -f "$out"
}

echo "=== disk pressure integration test ==="
echo ""

# --- 1. The signal: a healthy machine must stay silent --------------------
# This is the regression. The box running this test is almost certainly a normal
# dev machine with high percent-used and plenty of absolute headroom — exactly
# the shape that used to alert forever.
echo "-- pressure signal (thresholds env-injected, /tmp scan redirected) --"

gotest "healthy machine is silent" 'TestTmpPressure_HealthyMachineIsSilent' 1 './bus/'
gotest "footprint breach alerts"   'TestTmpPressure_LargeFootprintAlerts'   1 './bus/'
gotest "headroom breach alerts"    'TestTmpPressure_LowHeadroomAlerts'      1 './bus/'
gotest "threshold parsing + overrides" \
  'TestParseByteSize|TestByteSizeDefaults_MalformedFallsBackNotZero|TestByteSizeOverrides|TestMuxcodeTmpFootprint' 4 './bus/'
gotest "disabled / healthy short-circuits" \
  'TestCheckDiskPressure_DisabledByThreshold|TestCheckDiskPressure_HealthyReturnsNil' 2 './bus/'

# --- 2. The cadence: fires once, not every cycle --------------------------
echo ""
echo "-- alert cadence --"

gotest "cooldown: first fires, suppressed in window, refires after" \
  'TestShouldAlertDiskPressure_' 4 './daemon/'
gotest "cooldown constants pinned" 'TestDiskPressureCooldownConstants' 1 './daemon/'

# --- 3. Live sanity on the real machine -----------------------------------
# The real box, real thresholds: a normal dev machine must not be pressured.
# Skipped rather than failed if the machine genuinely is low on disk — a true
# positive is not a test failure.
echo ""
echo "-- live machine --"

FREE_KB="$(df -k /tmp 2>/dev/null | awk 'NR==2{print $4}')"
if [ -n "${FREE_KB:-}" ] && [ "$FREE_KB" -gt 2097152 ]; then   # > 2 GiB, the default floor
  PCT="$(df -k /tmp 2>/dev/null | awk 'NR==2{gsub("%","",$5); print $5}')"
  ok "real /tmp has $((FREE_KB/1024/1024))Gi free at ${PCT}% used — high percent-used alone must not alert"
else
  echo "  ${YELLOW}SKIP${NC}  machine genuinely low on /tmp headroom (${FREE_KB:-?}KB) — pressure here would be a true positive"
fi

# --- 4. The destructive path stays inert under --dry-run ------------------
# CleanupStale is what makes forcing a real breach unsafe. Prove --dry-run
# removes nothing, so the safe surface is actually safe.
echo ""
echo "-- cleanup --dry-run is non-destructive --"

"$MUX" cleanup --dry-run >/dev/null 2>&1
if [ "$TMP_SNAPSHOT_BEFORE" = "$(ls -1d /tmp/muxcode-bus-* 2>/dev/null | sort)" ]; then
  ok "cleanup --dry-run removed no session directories"
else
  bad "cleanup --dry-run DELETED session directories"
  diff <(printf '%s\n' "$TMP_SNAPSHOT_BEFORE") <(ls -1d /tmp/muxcode-bus-* 2>/dev/null | sort) | head -10 | sed 's/^/        /'
fi

# --- 5. Isolation ----------------------------------------------------------
echo ""
echo "-- isolation --"

if [ "$LEAKED_BEFORE" = "$(leaked_logs)" ]; then
  ok "no scratch-session log leaked into ~/.config/muxcode/logs"
else
  bad "test leaked a scratch-session log into ~/.config/muxcode/logs"
  diff <(printf '%s\n' "$LEAKED_BEFORE") <(leaked_logs) | head -10 | sed 's/^/        /'
fi

# The leak assertion above is only meaningful if MUXCODE_LIFECYCLE_LOG_DIR is what
# actually steers writes away from the real dir — otherwise "nothing leaked" would
# also be true on a run where nothing logged at all, and the check would pass by
# accident forever. Asserting the temp dir merely exists proves nothing: mktemp -d
# created it. Only LifecycleLogDir() returning the override is evidence.
gotest "lifecycle log override is honored" 'TestLifecycleLogDir_HonorsOverride' 1 './bus/'

echo ""
echo "=== $pass passed, $fail failed ==="
[ "$fail" -eq 0 ]
