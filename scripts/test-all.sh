#!/usr/bin/env bash
# Runs every integration script (scripts/test-*.sh) one at a time and prints
# a pass/fail summary. The 60-integration-suite graph's single command.
#
# Serial on purpose: most scripts start a scratch daemon and tmux session,
# and two running at once contend for them.
#
# Scripts that drive the live muxcode session (not a scratch one) are skipped
# unless MUXCODE_TEST_LIVE=1 — every script resolving its session from
# `tmux display-message -p '#S'`. Run by default they flooded the live edit
# inbox, messaged the live watch agent and reloaded live build and test
# agents onto another provider (2026-11-13).
#
# A script must leave the checkout as it found it: `git status` is compared
# before and after each one, and a change — a new untracked file, a modified
# or rewritten tracked one — is reported by script and path and fails the run.
# Runs of 2026-11-18 left probe], probe2] and two modified tracked files, and
# a ship committed the latter (MUX-236). Outside a git checkout the check is
# skipped with a note.
#
# Usage: bash scripts/test-all.sh [name-filter]
#        MUXCODE_TEST_LIVE=1 bash scripts/test-all.sh
# Exit:  0 when every script that ran passed and the tree is unchanged, 1 otherwise.

set -uo pipefail

cd "$(dirname "$0")/.." || exit 1

# tree_state — one line per `git status` entry: status, a hash of the file's
# content (so rewriting an already-dirty file is a change too) and the path.
# Read from `-z` records, whose paths are literal — the human-readable form
# quotes non-ASCII and control characters and prints a rename as
# `old -> new`, neither of which names a file to hash. A rename or copy
# carries its source as a second record, skipped: the destination is hashed.
# The path is escaped for display only when it holds a non-printable byte.
tree_state() {
  local rec src xy p h
  git status --porcelain=v1 -z --untracked-files=all 2>/dev/null | while IFS= read -r -d '' rec; do
    xy="${rec:0:2}"; p="${rec:3}"
    case "$xy" in R* | C*) IFS= read -r -d '' src ;; esac
    h="-"
    [ -f "$p" ] && h="$(shasum <"$p" | cut -c1-12)"
    case "$p" in *[![:print:]]*) p="$(printf '%q' "$p")" ;; esac
    printf '%s\t%s\t%s\n' "$xy" "$h" "$p"
  done
}
tree_check=0
git rev-parse --is-inside-work-tree >/dev/null 2>&1 && tree_check=1
dirtied=()

filter="${1:-}"
live_only=" test-browser-monitor-e2e test-context-pressure test-diff-split test-hot-reload test-modal-size test-multi-reload test-notify-flood test-resize-hook test-stuck-agent "
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
  [ "$tree_check" = 1 ] && tree_state >"$logdir/$name.tree-before"
  printf '%-44s ' "$name"
  if bash "$script" >"$log" 2>&1; then
    passed+=("$name")
    echo "PASS"
  else
    failed+=("$name")
    echo "FAIL  (log: $log)"
    grep -E 'FAIL' "$log" | sed 's/\x1b\[[0-9;]*m//g' | head -5 | sed 's/^/    /'
  fi
  if [ "$tree_check" = 1 ]; then
    tree_state >"$logdir/$name.tree-after"
    changed="$(diff "$logdir/$name.tree-before" "$logdir/$name.tree-after" | sed -n 's/^[<>] //p' | cut -f3- | sort -u)"
    if [ -n "$changed" ]; then
      dirtied+=("$name")
      echo "    changed the checkout:"
      printf '%s\n' "$changed" | sed 's/^/      /'
    fi
  fi
done

echo
echo "passed: ${#passed[@]}  failed: ${#failed[@]}  skipped: ${#skipped[@]} (live-session, set MUXCODE_TEST_LIVE=1)"
[ "$tree_check" = 1 ] || echo "tree check: skipped (not a git checkout)"
status=0
if [ "${#failed[@]}" -gt 0 ]; then
  echo "FAILED: ${failed[*]}"
  status=1
fi
if [ "${#dirtied[@]}" -gt 0 ]; then
  echo "CHANGED THE CHECKOUT: ${dirtied[*]}"
  status=1
fi
exit "$status"
