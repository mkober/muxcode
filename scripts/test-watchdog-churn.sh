#!/usr/bin/env bash
# Integration verification for the long-active watchdog token-churn fix.
#
# The fix is daemon-internal (nudge cap, wake-up-text tiering, force-wake
# self-heal), so end-to-end coverage lives in the Go test suite. This script
# runs exactly the tests that exercise fixes A/B/C and asserts they pass —
# giving Phase 4 a single runnable command with a clean exit code.
#
# Usage: bash scripts/test-watchdog-churn.sh
set -euo pipefail

cd "$(git rev-parse --show-toplevel 2>/dev/null || echo .)/tools/muxcode"

GREEN=$'\033[0;32m'; RED=$'\033[0;31m'; NC=$'\033[0m'
fail=0

run() { # run "label" <go test args...>
  local label="$1"; shift
  echo "→ ${label}"
  if go test "$@" >/tmp/wchurn-$$.out 2>&1; then
    grep -E '^(--- PASS|ok|PASS)' /tmp/wchurn-$$.out | sed 's/^/    /'
    # Guard: a -run pattern matching zero tests still exits 0. Require at least
    # one actual PASS so a future test rename can't silently pass this check.
    if ! grep -qE '^--- PASS' /tmp/wchurn-$$.out; then
      echo "  ${RED}✗ ${label} — no tests matched the -run pattern (silent pass)${NC}"
      fail=1
    else
      echo "  ${GREEN}✓ ${label}${NC}"
    fi
  else
    grep -E '^(--- FAIL|FAIL|.*\.go:[0-9]+:)' /tmp/wchurn-$$.out | sed 's/^/    /'
    echo "  ${RED}✗ ${label}${NC}"
    fail=1
  fi
  rm -f /tmp/wchurn-$$.out
}

# Fix B — wake-up notification tiering (short form >3, bounded enumerated form).
run "notify: BuildCombinedNotification tiering" ./bus/ -count=1 -v \
  -run 'TestBuildCombinedNotification'

# Fix A — nudge cap env + reset wiring; Fix C — force-wake cap reset + map init.
run "daemon: watchdog nudge cap + churn-guard reset" ./daemon/ -count=1 -v \
  -run 'TestActiveWatchdogMaxNudges|TestResetChurnGuard|TestDaemon_NewInitializesWatchdogFields|TestCheckActiveWatchdog'

if [ "$fail" -eq 0 ]; then
  echo "${GREEN}watchdog-churn: all checks passed${NC}"
else
  echo "${RED}watchdog-churn: FAILURES above${NC}"
fi
exit "$fail"
