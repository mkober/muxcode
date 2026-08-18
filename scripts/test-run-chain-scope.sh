#!/usr/bin/env bash
# Integration test for the run chain watch-scope allowlist.
#
# Drives the real chain resolver via `muxcode chain run success --dry-run`
# and asserts which command shapes fire the run→watch request. --dry-run
# resolves the action without sending anything, so this is safe in a live
# session. Exit codes from the resolver are discriminated explicitly:
# 0 = watch action resolved, 2 = no action (nothing fires), anything else
# = resolver error, which fails the test loudly — a stale binary or a
# usage mistake must not pass as "no fire".
set -uo pipefail

MUX="${MUXCODE_BIN:-muxcode}"
MUX="$(command -v "$MUX" 2>/dev/null || echo "$MUX")"
if [ ! -x "$MUX" ]; then
  echo "  FAIL  cannot resolve muxcode binary ('$MUX') — set MUXCODE_BIN"
  exit 1
fi
pass=0; fail=0
ok()  { echo "  PASS  $*"; pass=$((pass + 1)); }
bad() { echo "  FAIL  $*"; fail=$((fail + 1)); }

echo "=== run chain watch-scope integration test ==="

# fires <command> → 0 if the watch action resolves, 1 if no action, 2 on error.
fires() {
  local rc
  "$MUX" chain run success --command "$1" --dry-run --files x --branch main \
    >/dev/null 2>&1
  rc=$?
  [ "$rc" -eq 0 ] && return 0
  [ "$rc" -eq 2 ] && return 1
  return 2
}

expect_fire() {
  fires "$2"; local rc=$?
  if [ "$rc" -eq 0 ]; then ok "$1 fires watch"; else bad "$1 should fire watch (resolver rc=$rc)"; fi
}

expect_no_fire() {
  fires "$2"; local rc=$?
  if [ "$rc" -eq 1 ]; then ok "$1 fires nothing"; else bad "$1 should fire nothing (resolver rc=$rc, want 1)"; fi
}

# --- 1. Incidental reads must not fire (the overfire storm shapes) -----------
expect_no_fire "cat task output"  "cat /private/tmp/muxcode-bus-s/tasks/42.output 2>&1"
expect_no_fire "ls"               "ls -la"
expect_no_fire "grep"             "grep -rn foo ."
expect_no_fire "jq"               "jq . /tmp/out.json"

# --- 1b. Reads of a .sh path are NOT executions (first-token anchoring) ------
expect_no_fire "cat a sh file"    "cat /tmp/x.sh"
expect_no_fire "ls sh with trailing arg" "ls -la scripts/test-run-chain-scope.sh 2>&1"
expect_no_fire "grep sh file"     "grep -n foo scripts/x.sh"
expect_no_fire "echo about bash script" "echo bash x.sh"

# --- 2. muxcode bus commands must never fire ---------------------------------
expect_no_fire "muxcode inbox"    "muxcode inbox"
expect_no_fire "muxcode with sh arg" "muxcode run something.sh"
expect_no_fire "muxcode send quoting sh" 'muxcode send watch watch "bash /tmp/x.sh"'

# --- 3. Verification-run shapes must still fire ------------------------------
expect_fire "aws lambda invoke"   "aws lambda invoke --function-name test"
expect_fire "aws s3 cp"           "aws s3 cp s3://bucket/a.json /tmp/a.json"
expect_fire "aws stepfunctions"   "aws stepfunctions start-execution --state-machine arn:aws:states:x"
expect_fire "bash script no args" "bash scripts/test-install.sh"
expect_fire "sh script"           "sh scripts/deploy.sh"
expect_fire "direct script"       "./scripts/test-modal-size.sh"
expect_fire "absolute script"     "/tmp/verify.sh"
expect_fire "absolute script args" "/tmp/verify.sh --flag x"
expect_fire "script with args"    "scripts/deploy.sh prod"
expect_fire "script redirection"  "bash /tmp/verify.sh > /tmp/out.log 2>&1"

echo ""
echo "=== $pass passed, $fail failed ==="
[ "$fail" -eq 0 ]
