#!/usr/bin/env bash
# Integration test for the branch time-tracking feature.
#
# Hermetic: uses a throwaway git repo and a temp ledger file
# (MUXCODE_BRANCH_TIME_FILE) so it never touches the real ~/.config ledger or
# the current repo. Exercises the CLI surfaces end-to-end plus a real commit
# through the prepare-commit-msg trailer hook.
#
# Usage: bash scripts/test-branch-time.sh
set -euo pipefail

MUXCODE_BIN="${MUXCODE_BIN:-muxcode}"
PASS=0
FAIL=0
GREEN=$'\033[0;32m'; RED=$'\033[0;31m'; DIM=$'\033[2m'; NC=$'\033[0m'

pass() { PASS=$((PASS+1)); echo "  ${GREEN}✓${NC} $1"; }
fail() { FAIL=$((FAIL+1)); echo "  ${RED}✗${NC} $1"; }
check() { # check "desc" "expected-substring" "actual"
  if printf '%s' "$3" | grep -qF -- "$2"; then pass "$1"; else
    fail "$1"; echo "    ${DIM}expected to contain: $2${NC}"; echo "    ${DIM}actual: $3${NC}"; fi
}
check_empty() { # check_empty "desc" "actual"
  if [ -z "$(printf '%s' "$2" | tr -d '[:space:]')" ]; then pass "$1"; else
    fail "$1"; echo "    ${DIM}expected empty, got: $2${NC}"; fi
}

# --- prerequisites ---
command -v "$MUXCODE_BIN" >/dev/null 2>&1 || { echo "muxcode not on PATH (set MUXCODE_BIN)"; exit 1; }
command -v git >/dev/null 2>&1 || { echo "git required"; exit 1; }

# --- hermetic sandbox ---
WORK="$(mktemp -d /tmp/mux-branchtime-test-XXXXXX)"
export MUXCODE_BRANCH_TIME_FILE="$WORK/branch-time.json"
REPO="$WORK/repo"
cleanup() { rm -rf "$WORK"; }
trap cleanup EXIT

mkdir -p "$REPO"
cd "$REPO"
git init -q
git config user.email "test@example.com"
git config user.name "Test"
git config commit.gpgsign false
git checkout -q -b PROJ-123-add-feature
echo "seed" > f.txt && git add f.txt && git commit -q -m "seed commit" --no-verify

echo "Branch time-tracking integration test"
echo "  repo: $REPO"
echo "  ledger: $MUXCODE_BRANCH_TIME_FILE"
echo

# 1. Accumulation grows the branch total.
"$MUXCODE_BIN" branch-time --add 3600 >/dev/null
out="$("$MUXCODE_BIN" branch-time)"
check "accumulate 1h → shows 1h 0m" "1h 0m" "$out"
"$MUXCODE_BIN" branch-time --add 720 >/dev/null   # +12m
out="$("$MUXCODE_BIN" branch-time)"
check "accumulate grows to 1h 12m" "1h 12m" "$out"

# 2. --status compact shape.
out="$("$MUXCODE_BIN" branch-time --status)"
check "--status is compact with clock glyph" "1h12m" "$out"

# 3. --status is empty when disabled.
out="$(MUXCODE_BRANCH_TIME_DISABLE=1 "$MUXCODE_BIN" branch-time --status)"
check_empty "--status empty when MUXCODE_BRANCH_TIME_DISABLE=1" "$out"

# 4. --all lists the branch.
out="$("$MUXCODE_BIN" branch-time --all)"
check "--all lists the branch" "PROJ-123-add-feature" "$out"

# 5. --trailer emits a Time-spent line.
out="$("$MUXCODE_BIN" branch-time --trailer)"
check "--trailer emits Time-spent:" "Time-spent: 1h 12m" "$out"

# 6. log-jira --dry-run computes the un-logged delta (all 4320s = 1h 12m un-logged).
out="$("$MUXCODE_BIN" branch-time log-jira --dry-run)"
check "log-jira --dry-run reports PROJ-123 key" "PROJ-123" "$out"
check "log-jira --dry-run computes full delta (1h 12m)" "1h 12m" "$out"

# 7. Commit through the prepare-commit-msg hook carries the trailer.
#    Install a hook mirroring the shipped one (bus/git_hooks.go), with the
#    resolved $MUXCODE_BIN substituted in so a custom binary path is honored.
#    Runtime shell vars ($1/$2/$tr) are escaped so only $MUXCODE_BIN expands now.
cat > .git/hooks/prepare-commit-msg <<HOOK
#!/bin/sh
# muxcode: branch-time-trailer
case "\$2" in
  merge|squash|commit) exit 0 ;;
esac
tr=\$($MUXCODE_BIN branch-time --trailer 2>/dev/null)
[ -n "\$tr" ] || exit 0
if command -v git >/dev/null 2>&1; then
  git interpret-trailers --if-exists doNothing --trailer "\$tr" "\$1" > "\$1.mux.tmp" 2>/dev/null && mv "\$1.mux.tmp" "\$1" || rm -f "\$1.mux.tmp"
fi
HOOK
chmod +x .git/hooks/prepare-commit-msg
echo "change" >> f.txt && git add f.txt && git commit -q -m "PROJ-123 make a change"
msg="$(git log -1 --pretty=%B)"
check "commit carries Time-spent: trailer" "Time-spent: 1h 12m" "$msg"

# 8. reset zeroes the counter.
"$MUXCODE_BIN" branch-time reset >/dev/null
out="$("$MUXCODE_BIN" branch-time)"
check "reset zeroes the branch total" "0m" "$out"
out="$("$MUXCODE_BIN" branch-time --status)"
check_empty "--status empty after reset (no accrued time)" "$out"

echo
echo "  ${GREEN}${PASS} passed${NC}, ${RED}${FAIL} failed${NC}"
[ "$FAIL" -eq 0 ]
