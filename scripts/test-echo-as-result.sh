#!/usr/bin/env bash
# Integration test for the echo-as-result fix
# (docs/requirements/backlog/echo-as-result.md, Phase 4).
#
# THE BUG: a bus *response payload* was recorded in console history as a command
# result. A non-hook agent's completion detection fires while the agent is still
# booting, so its launch banner became the "result" — written as
# command:"<role>", exit_code:"0", outcome:"success" and rendered as a green
# pass. Builds and tests that never ran showed up as passing, and the review
# console showed fabricated LGTMs.
#
# THE FIX has three parts, and this script covers all three end to end:
#   1. bus.NewBusResponseEntry is the SINGLE constructor for every synthesized
#      entry. It drops pure TUI chrome outright and, for anything it does keep,
#      emits a verdict-free row (empty exit code, outcome "unknown",
#      source "bus-response", action kept out of the command field). It refuses
#      to produce a success in any case.
#   2. `muxcode log` no longer defaults exit_code to "0" — a call carrying no
#      verdict records "unknown", not a pass.
#   3. The console model classifies three ways (IsPass / IsFail / IsUnverified)
#      so unverified rows are visible but counted as neither pass nor fail.
#
# ISOLATION: every check runs in a scratch BUS_SESSION under /tmp, with
# MUXCODE_LIFECYCLE_LOG_DIR pinned to a temp dir. The script therefore needs no
# running muxcode session, never touches the real session's console history, and
# leaves ~/.config/muxcode/logs untouched — which it asserts before exiting.
#
# Usage: bash scripts/test-echo-as-result.sh
set -uo pipefail

MUX="${MUXCODE_BIN:-muxcode}"
MUX="$(command -v "$MUX" 2>/dev/null || echo "$MUX")"
if [ ! -x "$MUX" ]; then
  echo "  FAIL  cannot resolve muxcode binary ('$MUX') — set MUXCODE_BIN"
  exit 1
fi

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

GREEN=$'\033[0;32m'; RED=$'\033[0;31m'; NC=$'\033[0m'
pass=0; fail=0
ok()  { echo "  ${GREEN}PASS${NC}  $*"; pass=$((pass + 1)); }
bad() { echo "  ${RED}FAIL${NC}  $*"; fail=$((fail + 1)); }

# --- Isolation -------------------------------------------------------------
export BUS_SESSION="echo-as-result-test-$$"
BD="/tmp/muxcode-bus-${BUS_SESSION}"
export MUXCODE_LIFECYCLE_LOG_DIR="/tmp/echo-as-result-lifecycle-$$"
REAL_LOG_DIR="${HOME}/.config/muxcode/logs"
REAL_LOGS_BEFORE="$(ls -1 "$REAL_LOG_DIR" 2>/dev/null | sort)"

cleanup() { rm -rf "$BD" "$MUXCODE_LIFECYCLE_LOG_DIR"; }
trap cleanup EXIT

"$MUX" init >/dev/null 2>&1
HIST="$BD/build-history.jsonl"

# strip ANSI so assertions match on plain text. The escape is expanded by bash
# ($'...') rather than left for sed: GNU sed understands \x1b, BSD sed is not
# guaranteed to, and a sed that passes it through literally would silently stop
# stripping — turning every assertion below into a false negative.
plain() { sed $'s/\x1b\\[[0-9;]*[A-Za-z]//g'; }

# rows <role> → number of history rows currently recorded.
# Written as an explicit if/else: `grep -c . f || echo 0` prints "0" AND exits 1
# on a file of only blank lines, so the fallback fires too and the function
# returns "0\n0", which breaks every -eq comparison that uses it.
rows() {
  local f="$BD/$1-history.jsonl"
  if [ -s "$f" ]; then grep -c . "$f"; else echo 0; fi
}

# wait_roundtrip <to-role> <action> <response-payload>
# Drives a real `--wait` send and answers it the way an agent does: the
# responder reads its own inbox for the reply-to id and replies with
# --type response. Exercises the genuine logWaitResponseToHistory path.
# Returns 0 on a completed round-trip, 1 if the reply-to id never appeared.
wait_roundtrip() {
  local to="$1" action="$2" payload="$3" rid="" i out
  ( AGENT_ROLE=edit "$MUX" send "$to" "$action" "request for $action" --wait \
      >"$BD/wait-$action.out" 2>&1 ) &
  local wpid=$!
  for i in $(seq 1 30); do
    out="$(AGENT_ROLE="$to" "$MUX" inbox 2>/dev/null || true)"
    rid="$(printf '%s' "$out" | grep -o -- '--reply-to [A-Za-z0-9-]*' | head -1 | awk '{print $2}')"
    [ -n "$rid" ] && break
    sleep 0.3
  done
  # Kill the `muxcode send --wait` child before the subshell that owns it.
  # Killing only $wpid leaves the child orphaned for the full 90s degrade
  # window, still writing into $BD after the trap has removed it.
  if [ -z "$rid" ]; then
    pkill -P "$wpid" 2>/dev/null
    kill "$wpid" 2>/dev/null
    wait "$wpid" 2>/dev/null
    return 1
  fi
  AGENT_ROLE="$to" "$MUX" send edit "$action" "$payload" \
    --type response --reply-to "$rid" >/dev/null 2>&1
  wait "$wpid" 2>/dev/null
  return 0
}

echo "=== echo-as-result integration test ==="
echo ""

# --- 1. The authoritative path stays intact --------------------------------
# The fix must not cost real evidence its verdict. A self-logged command with a
# real exit code is the one thing that IS proof, and it must still read as one.
echo "-- authoritative path (real muxcode log) --"

"$MUX" log build "real build run" --command ./test.sh --exit-code 0 >/dev/null 2>&1
if grep -q '"exit_code":"0"' "$HIST" && grep -q '"outcome":"success"' "$HIST"; then
  ok "real --exit-code 0 records a success verdict"
else
  bad "real --exit-code 0 did not record success"
fi

"$MUX" log build "real build failure" --command ./build.sh --exit-code 1 >/dev/null 2>&1
if grep -q '"exit_code":"1"' "$HIST" && grep -q '"outcome":"failure"' "$HIST"; then
  ok "real --exit-code 1 records a failure verdict"
else
  bad "real --exit-code 1 did not record failure"
fi

# The root of the family: no exit code must mean "unknown", never a pass.
"$MUX" log build "observation with no verdict" >/dev/null 2>&1
if tail -1 "$HIST" | grep -q '"outcome":"unknown"' && tail -1 "$HIST" | grep -q '"exit_code":""'; then
  ok "log with no --exit-code records unknown, not a pass"
else
  bad "log with no --exit-code should record unknown (exit_code no longer defaults to 0)"
fi

# --- 2. Console classification and counter math ----------------------------
# Read back through the real renderer, not the JSONL: the pane is what a human
# and a pane-scraping agent actually believe.
echo ""
echo "-- console rendering and counters --"

SUMMARY="$("$MUX" console build --once 2>&1 | plain)"
if echo "$SUMMARY" | grep -qE 'pass 1 +fail 1 +unverified 1'; then
  ok "counters over mixed history: pass 1, fail 1, unverified 1"
else
  bad "counter math wrong — got: $(echo "$SUMMARY" | grep -E 'total' | head -1)"
fi

if echo "$SUMMARY" | grep -q 'OK' && echo "$SUMMARY" | grep -q 'FAIL'; then
  ok "real rows still render with OK / FAIL verdicts"
else
  bad "real verdict rows missing from console output"
fi

if echo "$SUMMARY" | grep -q '····'; then
  ok "unverified row renders with the no-verdict marker"
else
  bad "unverified row not visually distinguished"
fi

# --- 3. The --wait path: chrome is dropped outright ------------------------
# The exact observed shape: a reply that is nothing but a launch banner.
echo ""
echo "-- --wait round-trip (synthesized path) --"

BEFORE_ROWS="$(rows test)"
if wait_roundtrip test test "MuxCode agent launch: test"; then
  if [ "$(rows test)" -eq "$BEFORE_ROWS" ]; then
    ok "--wait launch-banner payload records no history row at all"
  else
    bad "--wait launch-banner payload wrote a row: $(tail -1 "$BD/test-history.jsonl")"
  fi
else
  bad "--wait round-trip (banner) did not complete — no reply-to id observed"
fi

# A real-looking reply must still be RECORDED (the pane must not go empty) but
# must never be a pass. This is the visibility half of the fix: the reason the
# synthesized row exists at all is that non-hook panes would otherwise be blank.
BEFORE_ROWS="$(rows review)"
if wait_roundtrip review review "Reviewed 4 files, no blocking issues found."; then
  if [ "$(rows review)" -eq "$((BEFORE_ROWS + 1))" ]; then
    ok "--wait real payload records exactly one row (pane visibility preserved)"
  else
    bad "--wait real payload wrote $(( $(rows review) - BEFORE_ROWS )) rows, want 1"
  fi
  ROW="$(tail -1 "$BD/review-history.jsonl" 2>/dev/null)"
  if echo "$ROW" | grep -q '"source":"bus-response"'; then
    ok "synthesized row carries bus-response provenance"
  else
    bad "synthesized row missing source=bus-response: $ROW"
  fi
  if echo "$ROW" | grep -q '"outcome":"unknown"' && echo "$ROW" | grep -q '"exit_code":""'; then
    ok "synthesized row claims no verdict (unknown, empty exit code)"
  else
    bad "synthesized row carries a verdict it did not earn: $ROW"
  fi
  # The action must never masquerade as a shell command someone ran.
  if echo "$ROW" | grep -q '"command":""'; then
    ok "bus action is not written into the command field"
  else
    bad "bus action leaked into the command field: $ROW"
  fi
  # The observed bug was the review pane showing "clean 2" — two LGTMs for
  # reviews that never happened. The review renderer words its counters
  # clean/issues rather than pass/fail, so assert in its own vocabulary.
  REVIEW_OUT="$("$MUX" console review --once 2>&1 | plain)"
  if echo "$REVIEW_OUT" | grep -qE 'clean 0' && echo "$REVIEW_OUT" | grep -qE 'unverified 1'; then
    ok "review console shows clean 0 / unverified 1 (no fabricated LGTM)"
  else
    bad "review console miscounted — got: $(echo "$REVIEW_OUT" | grep -E 'total' | head -1)"
  fi
  if echo "$REVIEW_OUT" | grep -qi 'no review verdict'; then
    ok "review console states the reply carried no verdict"
  else
    bad "review console did not mark the reply as verdict-free"
  fi
else
  bad "--wait round-trip (real payload) did not complete — no reply-to id observed"
fi

# The bus: namespacing is the build/test renderer's job — it labels rows by
# Label(), where a synthesized entry must show its action as "bus:<action>" so
# an action can never be misread as a shell command that ran. (The review
# renderer labels by summary instead, so this is asserted where it applies.)
if wait_roundtrip build build "Compiled 2 modules, 0 errors."; then
  if "$MUX" console build --once 2>&1 | plain | grep -q 'bus:build'; then
    ok "synthesized row renders namespaced as bus:build"
  else
    bad "synthesized row not rendered as bus:<action>"
  fi
else
  bad "--wait round-trip (build label) did not complete — no reply-to id observed"
fi

# --- 4. The --track mirror cannot outlive the fix --------------------------
# The --track completion path lives in the daemon, which needs a live session to
# drive end to end. What this asserts instead is the structural invariant the
# spec actually cared about: the hand-copied mirror was DELETED and every
# synthesis path now routes through the one constructor, so the defect cannot be
# fixed in one copy and regrow in another.
echo ""
echo "-- --track path routes through the shared constructor --"

SEND_GO="$REPO/tools/muxcode/cmd/send.go"
DAEMON_GO="$REPO/tools/muxcode/daemon/daemon.go"

if grep -q 'bus.NewBusResponseEntry' "$SEND_GO"; then
  ok "--wait path (cmd/send.go) builds entries via NewBusResponseEntry"
else
  bad "cmd/send.go no longer routes through NewBusResponseEntry"
fi

if grep -q 'bus.NewBusResponseEntry' "$DAEMON_GO"; then
  ok "--track path (daemon.go) builds entries via NewBusResponseEntry"
else
  bad "daemon.go no longer routes through NewBusResponseEntry"
fi

if grep -q 'func logTrackedTaskToHistory' "$DAEMON_GO"; then
  bad "the deleted hand-copy mirror logTrackedTaskToHistory has regrown"
else
  ok "hand-copy mirror logTrackedTaskToHistory stays deleted"
fi

# The keyword-absence oracle is the specific heuristic that manufactured passes.
if grep -nE '"(failed|error:)"' "$SEND_GO" "$DAEMON_GO" | grep -qi 'strings.Contains'; then
  bad "keyword-absence success oracle reintroduced in a synthesis path"
else
  ok "no keyword-absence success oracle in either synthesis path"
fi

# --- 5. Constructor + console unit coverage --------------------------------
echo ""
echo "-- Go unit coverage (constructor, counters, outcome model) --"

# -v is required, not cosmetic: a -run pattern matching zero tests still exits 0
# AND still prints "ok <pkg>", so grepping for ^ok proves nothing. Only the
# per-test "--- PASS:" lines, which appear solely under -v, are evidence that
# named tests actually ran. Counting them means a future rename that stops
# matching the pattern fails here instead of silently testing nothing —
# the same "reads as a pass while carrying no evidence" defect this whole
# spec is about.
UNIT_MIN=8
UNIT_OUT="/tmp/echo-as-result-unit-$$.out"
if (cd "$REPO/tools/muxcode" && go test ./bus/ -v \
      -run 'TestNewBusResponseEntry|TestLooksLikeNonResult|TestConsoleEntry_|TestCountOutcomes_|TestHookOutcome_' \
      -count=1) >"$UNIT_OUT" 2>&1; then
  RAN="$(grep -cE '^--- PASS:' "$UNIT_OUT")"
  if grep -q 'no tests to run' "$UNIT_OUT"; then
    bad "unit -run pattern matched no tests (silent pass)"
  elif [ "$RAN" -ge "$UNIT_MIN" ]; then
    ok "provenance and outcome-model unit tests pass ($RAN tests ran)"
  else
    bad "only $RAN unit tests ran, want >= $UNIT_MIN — did a test get renamed out of the pattern?"
  fi
else
  grep -E '^(--- FAIL|FAIL|.*\.go:[0-9]+:)' "$UNIT_OUT" | head -20 | sed 's/^/        /'
  bad "provenance / outcome-model unit tests failed"
fi
rm -f "$UNIT_OUT"

# --- 6. The test itself must not leak --------------------------------------
# MUX-004's pin is what keeps a scratch session out of the real log dir. If this
# regresses, this script becomes a leak of its own.
echo ""
echo "-- isolation --"

REAL_LOGS_AFTER="$(ls -1 "$REAL_LOG_DIR" 2>/dev/null | sort)"
if [ "$REAL_LOGS_BEFORE" = "$REAL_LOGS_AFTER" ]; then
  ok "real lifecycle log dir untouched (MUXCODE_LIFECYCLE_LOG_DIR honored)"
else
  bad "scratch session leaked into $REAL_LOG_DIR"
fi

echo ""
echo "=== $pass passed, $fail failed ==="
[ "$fail" -eq 0 ]
