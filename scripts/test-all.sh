#!/usr/bin/env bash
# Runs every integration script (scripts/test-*.sh) one at a time and prints
# a pass/fail summary. The 60-integration-suite graph's single command.
#
# Serial on purpose: most scripts start a scratch daemon and tmux session,
# and two running at once contend for them.
#
# Scripts that need a live muxcode session (not a scratch one) are skipped
# unless MUXCODE_TEST_LIVE=1: test-diff-split, test-hot-reload,
# test-resize-hook.
#
# Usage: bash scripts/test-all.sh [name-filter]
#        MUXCODE_TEST_LIVE=1 bash scripts/test-all.sh
# Exit:  0 when every script that ran passed, 1 otherwise.

set -uo pipefail

cd "$(dirname "$0")/.." || exit 1

filter="${1:-}"
live_only=" test-diff-split test-hot-reload test-resize-hook "
logdir="${TMPDIR:-/tmp}/muxcode-test-all-$$"
mkdir -p "$logdir"

passed=()
failed=()
skipped=()

for script in scripts/test-*.sh; do
  name="$(basename "$script" .sh)"
  [ "$name" = "test-all" ] && continue
  if [ -n "$filter" ] && [[ "$name" != *"$filter"* ]]; then
    continue
  fi
  if [[ "$live_only" == *" $name "* ]] && [ "${MUXCODE_TEST_LIVE:-0}" != "1" ]; then
    skipped+=("$name")
    continue
  fi
  log="$logdir/$name.log"
  printf '%-44s ' "$name"
  if bash "$script" >"$log" 2>&1; then
    passed+=("$name")
    echo "PASS"
  else
    failed+=("$name")
    echo "FAIL  (log: $log)"
    grep -E 'FAIL' "$log" | sed 's/\x1b\[[0-9;]*m//g' | head -5 | sed 's/^/    /'
  fi
done

echo
echo "passed: ${#passed[@]}  failed: ${#failed[@]}  skipped: ${#skipped[@]} (live-session, set MUXCODE_TEST_LIVE=1)"
if [ "${#failed[@]}" -gt 0 ]; then
  echo "FAILED: ${failed[*]}"
  exit 1
fi
exit 0
