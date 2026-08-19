#!/usr/bin/env bash
# Integration test for the branch-time requirements-doc sink
# (docs/requirements/backlog/branch-time-tracking.md, Phase 3).
#
# Companion to scripts/test-branch-time.sh: that script covers the accumulator
# and CLI surfaces, this one covers the *recording* delta — the JSON read path
# and the `## Time Tracking` sink the plan agent writes during verify-spec.
#
# WHAT IS SIMULATED, AND WHY: the real writer is the plan agent, an LLM, which a
# script cannot invoke deterministically. What it CAN do is execute the exact
# mechanical contract agents/planner.md specifies — read `--json`, compare
# against the doc row, reconcile, upsert keyed by branch, mark via `record` —
# and assert the resulting file. That pins the behaviours the spec's acceptance
# criteria name (idempotency, never-regress, degrade-quietly) against the real
# ledger and the real CLI. It does not prove the agent follows the contract;
# it proves the contract itself holds and the CLI supports it.
#
# RECONCILIATION USES `seed`, NOT `--add`. The spec's Phase 3 checkbox used to
# say `--add`; it was stale text left behind by the reconciliation revision and
# is corrected to `seed` in the same change that added this script. The
# distinction is load-bearing, not cosmetic: `--add` is additive and
# double-counts whenever the ledger is non-zero, while `seed` is a floor that
# only ever raises. The "repeated reconciliation does not inflate the ledger"
# assertion below exists specifically to fail if anyone reintroduces `--add`.
#
# Hermetic: throwaway git repo, temp ledger (MUXCODE_BRANCH_TIME_FILE), scratch
# BUS_SESSION, and a pinned MUXCODE_LIFECYCLE_LOG_DIR so the run cannot leak
# into ~/.config/muxcode/logs.
#
# Usage: bash scripts/test-branch-time-recording.sh
set -uo pipefail

MUX="${MUXCODE_BIN:-muxcode}"
MUX="$(command -v "$MUX" 2>/dev/null || echo "$MUX")"
[ -x "$MUX" ] || { echo "  FAIL  cannot resolve muxcode binary ('$MUX') — set MUXCODE_BIN"; exit 1; }
command -v git >/dev/null 2>&1 || { echo "  FAIL  git required"; exit 1; }
command -v python3 >/dev/null 2>&1 || { echo "  FAIL  python3 required"; exit 1; }

GREEN=$'\033[0;32m'; RED=$'\033[0;31m'; DIM=$'\033[2m'; NC=$'\033[0m'
pass=0; fail=0
ok()  { echo "  ${GREEN}PASS${NC}  $*"; pass=$((pass+1)); }
bad() { echo "  ${RED}FAIL${NC}  $*"; fail=$((fail+1)); }

# --- hermetic sandbox ------------------------------------------------------
WORK="$(mktemp -d /tmp/mux-bt-recording-XXXXXX)"
export MUXCODE_BRANCH_TIME_FILE="$WORK/branch-time.json"
export MUXCODE_LIFECYCLE_LOG_DIR="$WORK/logs"
export BUS_SESSION="bt-recording-test-$$"
REAL_LOGS_BEFORE="$(ls -1 "$HOME/.config/muxcode/logs" 2>/dev/null | sort)"
REPO="$WORK/repo"
SPEC="$REPO/docs/requirements/drafts/MUX-999-scratch.md"
BRANCH="MUX-999-scratch"

cleanup() { rm -rf "$WORK" "/tmp/muxcode-bus-$BUS_SESSION"; }
trap cleanup EXIT

mkdir -p "$REPO/docs/requirements/drafts"
cd "$REPO" || exit 1
git init -q . 2>/dev/null
git config user.email t@t.t; git config user.name t
git checkout -q -b "$BRANCH" 2>/dev/null
printf '# MUX-999 scratch\n\n## Status\n\nIn Progress\n' > "$SPEC"
git add -A >/dev/null 2>&1; git commit -qm init >/dev/null 2>&1

# --- the plan agent's documented recording contract, mechanized ------------
cat > "$WORK/record.py" <<'PYEOF'
"""Upsert one `## Time Tracking` row, applying never-regress reconciliation.

Mirrors agents/planner.md step-for-step. Prints two lines:
  WROTE <seconds>     total actually written into the doc row
  RESEED <seconds>    emitted only when the doc was ahead of the ledger
"""
import re, sys, datetime

spec, branch, ledger_secs, ledger_fmt = sys.argv[1], sys.argv[2], int(sys.argv[3]), sys.argv[4]
text = open(spec).read()

def fmt_to_secs(s):
    """Parse '12h 34m' / '45m' / '0m' back to seconds."""
    total = 0
    for val, unit in re.findall(r'(\d+)\s*([hms])', s):
        total += int(val) * {'h': 3600, 'm': 60, 's': 1}[unit]
    return total

# Existing row for THIS branch, if any.
row_re = re.compile(r'^\|\s*' + re.escape(branch) + r'\s*\|\s*([^|]+?)\s*\|\s*([^|]*?)\s*\|\s*$', re.M)
m = row_re.search(text)
doc_secs = fmt_to_secs(m.group(1)) if m else 0

# Never-regress: the doc must never show less than it already showed.
if ledger_secs >= doc_secs:
    write_secs, write_fmt, reseed = ledger_secs, ledger_fmt, None
else:
    write_secs, write_fmt, reseed = doc_secs, m.group(1), doc_secs

stamp = datetime.datetime.fromtimestamp(1787000000).strftime('%Y-%m-%d %H:%M')
row = f'| {branch} | {write_fmt} | {stamp} |'

if m:
    text = row_re.sub(row, text, count=1)          # replace in place, never append
elif '## Time Tracking' in text:
    text = re.sub(r'(\|-+\|-+\|-+\|\n)', r'\1' + row + '\n', text, count=1)
else:
    text = text.rstrip('\n') + (
        '\n\n## Time Tracking\n\n'
        '| Branch | Active time | Last updated |\n'
        '|--------|-------------|--------------|\n'
        + row + '\n'
    )

open(spec, 'w').write(text)
print(f'WROTE {write_secs}')
if reseed is not None:
    print(f'RESEED {reseed}')
PYEOF

jget() { python3 -c "import json,sys; print(json.load(sys.stdin).get('$1',''))"; }

# simulate_verify_spec <spec-or-empty> — the mechanical half of plan's pass.
# An empty spec path means "no active spec": the ledger still accumulates but
# nothing is written, which is the documented degrade-quietly behaviour.
simulate_verify_spec() {
  local target="$1" js secs fmt out wrote reseed
  js="$("$MUX" branch-time show --branch "$BRANCH" --json 2>/dev/null)"
  secs="$(printf '%s' "$js" | jget seconds)"
  fmt="$(printf '%s' "$js" | jget formatted)"
  [ -n "$target" ] || return 0
  out="$(python3 "$WORK/record.py" "$target" "$BRANCH" "$secs" "$fmt")"
  wrote="$(printf '%s\n' "$out" | awk '/^WROTE/{print $2}')"
  reseed="$(printf '%s\n' "$out" | awk '/^RESEED/{print $2}')"
  [ -n "$reseed" ] && "$MUX" branch-time seed --secs "$reseed" --branch "$BRANCH" >/dev/null 2>&1
  "$MUX" branch-time record --secs "$wrote" --branch "$BRANCH" >/dev/null 2>&1
}

rows_for_branch() { grep -c "^| $BRANCH |" "$SPEC" 2>/dev/null || echo 0; }

echo "=== branch-time recording integration test ==="
echo ""

# --- 1. JSON read path ------------------------------------------------------
echo "-- --json output shape --"

JS="$("$MUX" branch-time show --branch "$BRANCH" --json 2>/dev/null)"
missing=""
for f in repoKey branch seconds formatted unrecordedSeconds lastRecordedSeconds \
         lastRecordedAt lastJiraLoggedSeconds updated current ignored; do
  printf '%s' "$JS" | python3 -c "
import json,sys
sys.exit(0 if '$f' in json.load(sys.stdin) else 1)" 2>/dev/null || missing="$missing $f"
done
if [ -z "$missing" ]; then
  ok "--json carries every documented field"
else
  bad "--json missing field(s):$missing"
  echo "    ${DIM}got: $JS${NC}"
fi

# A fresh branch must read as zero, not error — plan's read runs before any
# time has accrued and must not fail there.
if [ "$(printf '%s' "$JS" | jget seconds)" = "0" ]; then
  ok "fresh branch reports seconds 0 rather than erroring"
else
  bad "fresh branch did not report 0 (got '$(printf '%s' "$JS" | jget seconds)')"
fi

if "$MUX" branch-time --all --json 2>/dev/null | python3 -c "
import json,sys; sys.exit(0 if isinstance(json.load(sys.stdin), list) else 1)" 2>/dev/null; then
  ok "--all --json emits a JSON array"
else
  bad "--all --json did not emit an array"
fi

# --- 2. First recording creates the section --------------------------------
echo ""
echo "-- first recording --"

"$MUX" branch-time --add 4500 >/dev/null 2>&1   # 1h 15m
simulate_verify_spec "$SPEC"

if grep -q '^## Time Tracking' "$SPEC"; then
  ok "Time Tracking section created on first record"
else
  bad "Time Tracking section was not created"
fi

LEDGER_FMT="$("$MUX" branch-time show --branch "$BRANCH" --json 2>/dev/null | jget formatted)"
if grep -q "^| $BRANCH | $LEDGER_FMT |" "$SPEC"; then
  ok "row carries the ledger's absolute total ($LEDGER_FMT)"
else
  bad "row does not match ledger total '$LEDGER_FMT'"
  echo "    ${DIM}$(grep "^| $BRANCH" "$SPEC")${NC}"
fi

if [ "$(rows_for_branch)" -eq 1 ]; then
  ok "exactly one row for the branch"
else
  bad "expected 1 row, found $(rows_for_branch)"
fi

# --- 3. Idempotency ---------------------------------------------------------
echo ""
echo "-- idempotent re-record --"

BEFORE="$(grep "^| $BRANCH |" "$SPEC")"
simulate_verify_spec "$SPEC"
AFTER="$(grep "^| $BRANCH |" "$SPEC")"

if [ "$BEFORE" = "$AFTER" ]; then
  ok "re-recording rewrites an identical row (absolute totals, not deltas)"
else
  bad "row changed on re-record"
  echo "    ${DIM}before: $BEFORE${NC}"
  echo "    ${DIM}after:  $AFTER${NC}"
fi

if [ "$(rows_for_branch)" -eq 1 ]; then
  ok "re-record replaced in place — no duplicate row appended"
else
  bad "re-record produced $(rows_for_branch) rows for one branch"
fi

# --- 4. Never-regress -------------------------------------------------------
# Simulate a lost/reset ledger: the doc now shows more than the store does.
echo ""
echo "-- never-regress reconciliation --"

DOC_BEFORE="$(grep "^| $BRANCH |" "$SPEC")"
"$MUX" branch-time reset "$BRANCH" >/dev/null 2>&1
"$MUX" branch-time --add 60 >/dev/null 2>&1     # ledger now far below the doc
LOW="$("$MUX" branch-time show --branch "$BRANCH" --json 2>/dev/null | jget seconds)"

simulate_verify_spec "$SPEC"
DOC_AFTER="$(grep "^| $BRANCH |" "$SPEC")"

if [ "$DOC_BEFORE" = "$DOC_AFTER" ]; then
  ok "doc keeps its larger value when the ledger regressed"
else
  bad "doc value regressed"
  echo "    ${DIM}was: $DOC_BEFORE${NC}"
  echo "    ${DIM}now: $DOC_AFTER${NC}"
fi

RESEEDED="$("$MUX" branch-time show --branch "$BRANCH" --json 2>/dev/null | jget seconds)"
if [ "$RESEEDED" -gt "$LOW" ]; then
  ok "ledger re-seeded up to the doc total ($LOW → $RESEEDED)"
else
  bad "ledger was not re-seeded (still $RESEEDED)"
fi

# seed is a floor, so re-running must not inflate it further — this is the
# check that would catch someone swapping `seed` back to the additive `--add`.
simulate_verify_spec "$SPEC"
AGAIN="$("$MUX" branch-time show --branch "$BRANCH" --json 2>/dev/null | jget seconds)"
if [ "$AGAIN" = "$RESEEDED" ]; then
  ok "repeated reconciliation does not inflate the ledger (seed is a floor, not an add)"
else
  bad "ledger inflated on repeat: $RESEEDED → $AGAIN (is reconciliation using --add?)"
fi

# --- 5. No active spec — degrade quietly ------------------------------------
echo ""
echo "-- no active spec --"

SPEC_SNAPSHOT="$(cat "$SPEC")"
BEFORE_SECS="$("$MUX" branch-time show --branch "$BRANCH" --json 2>/dev/null | jget seconds)"
"$MUX" branch-time --add 120 >/dev/null 2>&1
simulate_verify_spec ""                          # no active spec → no doc write
AFTER_SECS="$("$MUX" branch-time show --branch "$BRANCH" --json 2>/dev/null | jget seconds)"

if [ "$SPEC_SNAPSHOT" = "$(cat "$SPEC")" ]; then
  ok "no active spec → spec file untouched"
else
  bad "spec was written despite no active spec"
fi

if [ "$AFTER_SECS" -gt "$BEFORE_SECS" ]; then
  ok "ledger still accumulates with no active spec ($BEFORE_SECS → $AFTER_SECS)"
else
  bad "ledger stopped accumulating without a spec"
fi

# --- 6. Isolation -----------------------------------------------------------
echo ""
echo "-- isolation --"

if [ "$REAL_LOGS_BEFORE" = "$(ls -1 "$HOME/.config/muxcode/logs" 2>/dev/null | sort)" ]; then
  ok "real lifecycle log dir untouched"
else
  bad "test leaked into ~/.config/muxcode/logs"
fi

echo ""
echo "=== $pass passed, $fail failed ==="
[ "$fail" -eq 0 ]
