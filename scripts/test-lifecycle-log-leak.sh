#!/usr/bin/env bash
# Regression test for the lifecycle-log test leak
# (docs/requirements/backlog/lifecycle-log-test-leak.md, Phase 2).
#
# THE BUG: lifecycle logging is a side effect of a great many code paths, and it
# writes one persistent file per session name into ~/.config/muxcode/logs. Tests
# use synthetic session names ("test-cron-exec", "test-<random>"), so every full
# `go test ./...` deposited a fresh pile of stray log files into the user's real
# install — 41,789 of them had accumulated before anyone noticed.
#
# THE FIX: LifecycleLogDir() honors MUXCODE_LIFECYCLE_LOG_DIR, and the TestMain
# of every package that logs (bus, cmd, daemon) pins it to a temp dir for the
# package run.
#
# WHY THIS SCRIPT REDIRECTS HOME: the naive check — snapshot the real log dir,
# run the suite, compare — only detects a leak by *allowing* it to happen, which
# is precisely the damage being prevented. Instead the suite runs under a
# throwaway HOME, so the HOME-derived log path is a temp dir. If any package
# ever loses its pin, the files land there and this test fails, while the user's
# real install is never written to either way. The real dir is still snapshotted
# as a belt-and-braces check that the redirect itself held.
#
# Usage: bash scripts/test-lifecycle-log-leak.sh
set -uo pipefail

MUX="${MUXCODE_BIN:-muxcode}"
MUX="$(command -v "$MUX" 2>/dev/null || echo "$MUX")"
if [ ! -x "$MUX" ]; then
  echo "  FAIL  cannot resolve muxcode binary ('$MUX') — set MUXCODE_BIN"
  exit 1
fi

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MODULE="$REPO/tools/muxcode"

GREEN=$'\033[0;32m'; RED=$'\033[0;31m'; YELLOW=$'\033[0;33m'; NC=$'\033[0m'
pass=0; fail=0
ok()  { echo "  ${GREEN}PASS${NC}  $*"; pass=$((pass + 1)); }
bad() { echo "  ${RED}FAIL${NC}  $*"; fail=$((fail + 1)); }

REAL_LOG_DIR="${HOME}/.config/muxcode/logs"
REAL_BEFORE="$(ls -1 "$REAL_LOG_DIR" 2>/dev/null | sort)"

# Go's caches live under the real HOME. They must be resolved and exported
# BEFORE HOME is redirected, or every run below rebuilds from a cold cache in a
# throwaway dir — slow, and a cache write failure would look like a test failure.
export GOCACHE="$(cd "$MODULE" && go env GOCACHE)"
export GOMODCACHE="$(cd "$MODULE" && go env GOMODCACHE)"
export GOPATH="$(cd "$MODULE" && go env GOPATH)"

FAKE_HOME="$(mktemp -d /tmp/lifecycle-leak-home-XXXXXX)"
PINNED="$(mktemp -d /tmp/lifecycle-leak-pinned-XXXXXX)"
SCRATCH_BUS=""

cleanup() {
  rm -rf "$FAKE_HOME" "$PINNED"
  [ -n "$SCRATCH_BUS" ] && rm -rf "$SCRATCH_BUS"
}
trap cleanup EXIT

# count_logs <home-dir> → number of files in that HOME's lifecycle log dir
count_logs() { ls -1 "$1/.config/muxcode/logs" 2>/dev/null | wc -l | tr -d ' '; }

echo "=== lifecycle log leak regression test ==="
echo ""

# --- 1. Negative control: the leak path is real and still live -------------
# Before asserting "no files appear", prove that files WOULD appear without the
# pin. Otherwise a passing test proves nothing — the code could have stopped
# logging entirely and this would still look green.
echo "-- negative control (is the assertion sensitive?) --"

SCRATCH_BUS="/tmp/muxcode-bus-lifecycle-leak-unpinned-$$"
env -u MUXCODE_LIFECYCLE_LOG_DIR \
    HOME="$FAKE_HOME" \
    BUS_SESSION="lifecycle-leak-unpinned-$$" \
    "$MUX" init >/dev/null 2>&1

if [ "$(count_logs "$FAKE_HOME")" -gt 0 ]; then
  ok "unpinned lifecycle write lands in \$HOME/.config/muxcode/logs (leak path live)"
else
  bad "unpinned write produced no log — this test can no longer detect a leak"
fi

# Reset the fake HOME so the pinned checks start from a known-clean state.
rm -rf "${FAKE_HOME:?}/.config"

# --- 2. The pin redirects the write ----------------------------------------
echo ""
echo "-- the MUXCODE_LIFECYCLE_LOG_DIR pin --"

MUXCODE_LIFECYCLE_LOG_DIR="$PINNED" \
    HOME="$FAKE_HOME" \
    BUS_SESSION="lifecycle-leak-pinned-$$" \
    "$MUX" init >/dev/null 2>&1

if [ "$(count_logs "$FAKE_HOME")" -eq 0 ]; then
  ok "pinned write leaves \$HOME/.config/muxcode/logs untouched"
else
  bad "pin did not redirect: $(ls -1 "$FAKE_HOME/.config/muxcode/logs" | head -3 | tr '\n' ' ')"
fi

if [ "$(ls -1 "$PINNED" 2>/dev/null | wc -l | tr -d ' ')" -gt 0 ]; then
  ok "pinned write landed in the override dir"
else
  bad "pinned write went nowhere — override dir is empty"
fi

rm -rf "${FAKE_HOME:?}/.config"
rm -rf "$SCRATCH_BUS" "/tmp/muxcode-bus-lifecycle-leak-pinned-$$"

# --- 3. The regression itself: a full test run must not leak ---------------
# This is the acceptance criterion. Every package TestMain pins the override; if
# any one of them loses it, its synthetic session names land under this HOME.
echo ""
echo "-- full test suite under a throwaway HOME --"

SUITE_OUT="/tmp/lifecycle-leak-suite-$$.out"
( cd "$MODULE" && env -u MUXCODE_LIFECYCLE_LOG_DIR HOME="$FAKE_HOME" \
    go test ./... -count=1 ) >"$SUITE_OUT" 2>&1
SUITE_RC=$?

LEAKED="$(count_logs "$FAKE_HOME")"
if [ "$LEAKED" -eq 0 ]; then
  ok "full go test ./... run leaked 0 lifecycle log files"
else
  bad "full test run leaked $LEAKED log files into \$HOME/.config/muxcode/logs"
  ls -1 "$FAKE_HOME/.config/muxcode/logs" | head -10 | sed 's/^/        /'
  echo "        (a package TestMain is missing the MUXCODE_LIFECYCLE_LOG_DIR pin)"
fi

# "0 leaks" is only meaningful if the suite actually RAN. A build failure or an
# instant crash would leak nothing and look identical to a clean pass, so the
# trustworthiness guard is coverage, not greenness.
#
# Greenness is deliberately NOT asserted here. Some tests legitimately read the
# real HOME (TestOpenCodeModelsExist reads the OpenCode model config), so they
# fail under redirection for reasons that have nothing to do with logging.
# Demanding a green suite would make this script fail forever on an unrelated
# test — and tempt someone to weaken the leak check to get it passing again.
# Suite health belongs to ./test.sh; this script owns the leak.
if grep -q 'build failed' "$SUITE_OUT"; then
  bad "suite failed to build under redirected HOME — leak result is meaningless"
  grep -E 'build failed|cannot find|undefined' "$SUITE_OUT" | head -5 | sed 's/^/        /'
elif [ "$(grep -cE '^(ok|FAIL)[[:space:]]+github' "$SUITE_OUT")" -ge 3 ]; then
  ok "suite executed $(grep -cE '^(ok|FAIL)[[:space:]]+github' "$SUITE_OUT") packages — leak result is trustworthy"
else
  bad "suite ran too few packages to trust the leak result"
  head -15 "$SUITE_OUT" | sed 's/^/        /'
fi

# Unrelated failures are surfaced, never fatal — they are someone else's bug,
# but a silent one here would be confusing when reading a green report.
if [ "$SUITE_RC" -ne 0 ]; then
  echo "  ${YELLOW}NOTE${NC}  suite was not green under redirected HOME (rc=$SUITE_RC):"
  grep -E '^--- FAIL' "$SUITE_OUT" | head -5 | sed 's/^/          /'
  echo "          expected for tests that read the real HOME; not a leak"
fi
rm -f "$SUITE_OUT"

# --- 4. Every package that runs tests carries the pin ----------------------
# Structural guard: the leak returns the moment a new test package logs without
# a TestMain pin. Catching that at the source is cheaper than waiting for a
# package to actually start logging.
echo ""
echo "-- every test package pins the override --"

missing=""
for pkg in bus cmd daemon tui watcher; do
  ls "$MODULE/$pkg"/*_test.go >/dev/null 2>&1 || continue
  if ! grep -qs 'MUXCODE_LIFECYCLE_LOG_DIR' "$MODULE/$pkg"/main_test.go 2>/dev/null; then
    missing="$missing $pkg"
  fi
done

if [ -z "$missing" ]; then
  ok "all test-bearing packages pin MUXCODE_LIFECYCLE_LOG_DIR in TestMain"
else
  # Not a hard failure while those packages provably leak nothing (check 3 is
  # the authority). Reported so the gap is visible before it bites.
  echo "  ${YELLOW}NOTE${NC}  test packages with no TestMain pin:$missing"
  echo "          harmless only while they never trigger lifecycle logging"
fi

# --- 5. The real install was never touched ---------------------------------
echo ""
echo "-- real install untouched --"

REAL_AFTER="$(ls -1 "$REAL_LOG_DIR" 2>/dev/null | sort)"
if [ "$REAL_BEFORE" = "$REAL_AFTER" ]; then
  ok "~/.config/muxcode/logs unchanged across the whole run"
else
  bad "this test leaked into the real log dir"
  diff <(printf '%s\n' "$REAL_BEFORE") <(printf '%s\n' "$REAL_AFTER") | head -10 | sed 's/^/        /'
fi

echo ""
echo "=== $pass passed, $fail failed ==="
[ "$fail" -eq 0 ]
