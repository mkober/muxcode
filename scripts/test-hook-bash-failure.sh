#!/usr/bin/env bash
# Integration test for MUX-204: a Bash call that exits non-zero on a Claude
# agent arrives as PostToolUseFailure — no tool_response, its status on the
# first line of `error` — and must reach `muxcode hook bash` as a failure.
#
# Hermetic section: a scratch bus session driven through the installed binary,
# chain config pinned to the defaults (MUXCODE_CONFIG_DIR is scratch), Claude
# payloads fed to the real `muxcode hook bash`. Asserts, for Claude agents:
#   failure      — `Exit code 3` on a run agent: run-history row exit 3 /
#                  failure, the console row `FAIL … exit 3`, edit notified
#                  "Run FAILED (exit 3)", and no watch request
#   success      — the PostToolUse control: row 0 / success, console `OK`, the
#                  watch request, and no notice to edit — success fires success only
#   no exit line — a shell that never started records exit 1 and the failure
#                  edge, never 0
#   build chain  — a build agent's failing ./build.sh records exit 2 and
#                  notifies edit with no test request; its success requests test
# Coverage floor: the exact hermetic pass count.
#
# Live section: `--live` or MUXCODE_HOOK_BASH_FAILURE_LIVE=1 with `claude` on
# PATH — one real `claude -p` call, outside the floor, skipped with a reason
# otherwise. The run agent's hook entries are taken from config/settings.json
# (the Bash `PostToolUse` and `PostToolUseFailure` registrations, nothing else)
# and passed with --settings; the model runs a script that exits 3, then one
# that exits 0. The failing call must leave a run-history row with exit 3 and
# fire the failure edge; the passing call is the control. Both rows and the
# failure notice are polled for: `hook bash` is registered async, and it writes
# the row before it sends the chain's message. The user's own ~/.claude/settings.json
# hooks still load beside --settings, so a row may be written twice; every live
# check asks for at least one.
#
# Requires a muxcode built from MUX-204 Phase 2 (run ./build.sh first).
set -uo pipefail

PASS=0
FAIL=0
LIVE_PASS=0
EXPECTED_PASS=13

command -v muxcode >/dev/null 2>&1 || { echo "SKIP: muxcode not installed"; exit 2; }
command -v jq >/dev/null 2>&1 || { echo "SKIP: jq is required"; exit 2; }
MUX=$(command -v muxcode)
MUXDIR=$(dirname "$MUX")
. "$(dirname "${BASH_SOURCE[0]}")/lib/muxcode-version.sh"
require_muxcode_version "$MUX" v0.1.0 MUX-204 || { echo "  FAIL  binary precondition not met"; exit 1; }

LIVE=0
if [ "${1:-}" = --live ] || [ "${MUXCODE_HOOK_BASH_FAILURE_LIVE:-}" = 1 ]; then
  LIVE=1
fi

REPO=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
SESSION="hook-bash-failure-$$"
BUSDIR="/tmp/muxcode-bus-$SESSION"
WORK=$(mktemp -d /tmp/hook-bash-failure-XXXXXX)
PROJ="$WORK/project"
mkdir -p "$PROJ" "$WORK/config" "$WORK/lifecycle"

cleanup() { rm -rf "$BUSDIR" "$WORK"; }
trap cleanup EXIT

ok()   { PASS=$((PASS + 1)); echo "  ok: $1"; }
fail() { FAIL=$((FAIL + 1)); echo "  FAIL: $1"; }
lok()  { LIVE_PASS=$((LIVE_PASS + 1)); echo "  ok (live): $1"; }

export BUS_SESSION="$SESSION"
export MUXCODE_CONFIG_DIR="$WORK/config"
export MUXCODE_LIFECYCLE_LOG_DIR="$WORK/lifecycle"
export MUXCODE_DEDUP_WINDOW=0
export MUXCODE_RUN_CLI=claude MUXCODE_BUILD_CLI=claude
export COLUMNS=200 LINES=100
unset MUXCODE_AGENT_CLI MUXCODE_BUILD_PATTERNS MUXCODE_TEST_PATTERNS MUXCODE_TEST_PRECHECK_PATTERNS 2>/dev/null || true
cd "$WORK" || exit 1
ESC=$(printf '\033')

# claude_fail ROLE COMMAND ERROR — a Claude PostToolUseFailure Bash event into the real hook.
claude_fail() {
  jq -nc --arg c "$2" --arg e "$3" \
    '{hook_event_name:"PostToolUseFailure",tool_name:"Bash",tool_input:{command:$c},tool_use_id:"toolu_fail",error:$e,is_interrupt:false}' \
    | AGENT_ROLE="$1" "$MUX" hook bash >/dev/null 2>&1
}

# claude_ok ROLE COMMAND STDOUT — the PostToolUse success shape for the same call.
claude_ok() {
  jq -nc --arg c "$2" --arg o "$3" \
    '{hook_event_name:"PostToolUse",tool_name:"Bash",tool_input:{command:$c},tool_use_id:"toolu_ok",tool_response:{stdout:$o,stderr:"",interrupted:false,isImage:false}}' \
    | AGENT_ROLE="$1" "$MUX" hook bash >/dev/null 2>&1
}

# row_for HISTORY NEEDLE — the last row whose command contains NEEDLE.
row_for() { jq -c --arg n "$2" 'select(.command | contains($n))' "$BUSDIR/$1" 2>/dev/null | tail -1; }

# inbox_count ROLE TYPE ACTION NEEDLE... — messages in ROLE's inbox of TYPE and
# ACTION whose payload contains every NEEDLE.
inbox_count() {
  local role=$1 type=$2 action=$3
  shift 3
  jq -c --arg t "$type" --arg a "$action" --args \
    'select(.type == $t and .action == $a and (.payload as $p | $ARGS.positional | all(. as $n | $p | contains($n))))' \
    "$@" 2>/dev/null <"$BUSDIR/inbox/$role.jsonl" | wc -l | tr -d ' '
}

# pane ROLE — one frame of the role's console, ANSI stripped.
pane() { "$MUX" console "$1" --once </dev/null 2>/dev/null | sed "s/${ESC}\[[0-9;?]*[A-Za-z]//g"; }

echo "=== hook bash failure-event integration test (MUX-204) ==="
"$MUX" init "$SESSION" >/dev/null 2>&1 || true

RED="bash scripts/red.sh"
echo "-- run agent, failure: PostToolUseFailure with an Exit code line"
claude_fail run "$RED" $'Exit code 3\nFAIL: 2 of 9 checks'
row=$(row_for run-history.jsonl "$RED")
if jq -e '.exit_code == "3" and .outcome == "failure"' <<<"$row" >/dev/null 2>&1; then
  ok "run-history row: exit 3, outcome failure"
else
  fail "failure row wrong or missing — an installed muxcode predating MUX-204 writes none, or exit 0 (run ./build.sh): ${row:-<no row>}"
fi
frame=$(pane run)
if grep -qE 'FAIL +bash scripts/red\.sh +exit 3' <<<"$frame"; then ok "run console row: FAIL … exit 3"; else fail "run console: $frame"; fi
n=$(inbox_count edit event notify "Run FAILED (exit 3): $RED")
if [ "$n" = 1 ]; then ok "failure edge: edit notified Run FAILED (exit 3)"; else fail "edit failure notices for $RED: $n, want 1"; fi
n=$(inbox_count watch request watch "$RED")
if [ "$n" = 0 ]; then ok "the failure requests no watch"; else fail "the failure requested watch $n time(s)"; fi

GREEN="bash scripts/green.sh"
echo "-- run agent, negative control: the same kind of call as a PostToolUse success"
claude_ok run "$GREEN" "PASS: 9 of 9 checks"
row=$(row_for run-history.jsonl "$GREEN")
if jq -e '.exit_code == "0" and .outcome == "success"' <<<"$row" >/dev/null 2>&1; then ok "success row: exit 0, outcome success"; else fail "success row: ${row:-<no row>}"; fi
frame=$(pane run)
if grep -qE 'OK +bash scripts/green\.sh' <<<"$frame"; then ok "run console row: OK"; else fail "run console: $frame"; fi
n=$(inbox_count watch request watch "Run succeeded ($GREEN)")
if [ "$n" = 1 ]; then ok "success edge: watch requested"; else fail "watch requests for $GREEN: $n, want 1"; fi
n=$(inbox_count edit event notify "$GREEN")
if [ "$n" = 0 ]; then ok "the success sends edit no notice — success fires success only"; else fail "edit notified $n time(s) for the success"; fi

NOSTART="bash scripts/nostart.sh"
echo "-- run agent: a failure with no Exit code line (the shell never started)"
claude_fail run "$NOSTART" "spawn /bin/bash ENOENT"
row=$(row_for run-history.jsonl "$NOSTART")
if jq -e '.exit_code == "1" and .outcome == "failure"' <<<"$row" >/dev/null 2>&1; then ok "no exit line records exit 1, never 0"; else fail "no-exit-line row: ${row:-<no row>}"; fi
n=$(inbox_count edit event notify "Run FAILED (exit 1): $NOSTART")
if [ "$n" = 1 ]; then ok "no exit line fires the failure edge"; else fail "edit failure notices for $NOSTART: $n, want 1"; fi

echo "-- build agent: the build chain's failure edge, then its success"
claude_fail build "./build.sh" $'Exit code 2\n./main.go:3:2: undefined: Foo'
row=$(row_for build-history.jsonl "./build.sh")
if jq -e '.exit_code == "2" and .outcome == "failure"' <<<"$row" >/dev/null 2>&1; then ok "build-history row: exit 2, outcome failure"; else fail "build failure row: ${row:-<no row>}"; fi
notices=$(inbox_count edit event notify "Build FAILED (exit 2): ./build.sh")
tests=$(inbox_count test request test "./build.sh")
if [ "$notices" = 1 ] && [ "$tests" = 0 ]; then
  ok "a failing build notifies edit and requests no test"
else
  fail "failing build: $notices edit notice(s), $tests test request(s); want 1 and 0"
fi
claude_ok build "./build.sh" "ok"
row=$(row_for build-history.jsonl "./build.sh")
tests=$(inbox_count test request test "Build succeeded (./build.sh)")
if jq -e '.exit_code == "0"' <<<"$row" >/dev/null 2>&1 && [ "$tests" = 1 ]; then
  ok "a passing build (control) records 0 and requests test"
else
  fail "passing build: row ${row:-<no row>}, $tests test request(s)"
fi

# ── Live: a real Claude failure ───────────────────────────────────
if [ "$LIVE" != 1 ]; then
  echo "-- live section skipped (pass --live or set MUXCODE_HOOK_BASH_FAILURE_LIVE=1; spends a real model call)"
elif ! command -v claude >/dev/null 2>&1; then
  echo "-- live: Claude skipped (claude not on PATH)"
else
  echo "-- live: Claude"
  id="$$x$RANDOM"
  red="red-$id.sh"
  green="green-$id.sh"
  printf '#!/usr/bin/env bash\necho "FAIL: live red"\nexit 3\n' >"$PROJ/$red"
  printf '#!/usr/bin/env bash\necho "PASS: live green"\n' >"$PROJ/$green"
  live_settings=$(jq -c '{hooks: {PostToolUse: [.hooks.PostToolUse[] | select(.matcher == "Bash")], PostToolUseFailure: (.hooks.PostToolUseFailure // [])}}' "$REPO/config/settings.json")
  if ! jq -e '[.hooks.PostToolUseFailure[] | select(.matcher == "Bash") | .hooks[] | select(.command == "muxcode hook bash")] | length > 0' <<<"$live_settings" >/dev/null 2>&1; then
    fail "live: config/settings.json registers no PostToolUseFailure → muxcode hook bash"
  else
    ask="Use the Bash tool to run exactly this command: bash $red   Then use the Bash tool to run exactly this command: bash $green   Then reply with the single word done."
    log="$WORK/claude-live"
    (cd "$PROJ" && PATH="$MUXDIR:$PATH" AGENT_ROLE=run \
      claude -p "$ask" --model "${MUXCODE_HOOK_BASH_FAILURE_LIVE_MODEL:-haiku}" --settings "$live_settings" --allowedTools Bash \
      --output-format stream-json --verbose >"$log.jsonl" 2>"$log.err" </dev/null)
    run_status=$?
    tool_results=$(jq -s '[.[] | select(.type == "user") | .message.content[]? | select(.type == "tool_result")] | length' "$log.jsonl" 2>/dev/null)
    errored=$(jq -s '[.[] | select(.type == "user") | .message.content[]? | select(.type == "tool_result" and .is_error == true)] | length' "$log.jsonl" 2>/dev/null)
    if [ "$run_status" != 0 ] || [ "${tool_results:-0}" -lt 2 ] || [ "${errored:-0}" -lt 1 ]; then
      fail "live: incomplete run (CLI exit $run_status, tool results ${tool_results:-0}, tool errors ${errored:-0}); stderr: $(head -c 300 "$log.err")"
    else
      lok "claude ran both calls, and the failing one came back as a tool error"
      red_row=""
      green_row=""
      notices=0
      for _ in $(seq 1 40); do
        red_row=$(row_for run-history.jsonl "$red")
        green_row=$(row_for run-history.jsonl "$green")
        notices=$(inbox_count edit event notify "Run FAILED (exit 3)" "$red")
        [ -n "$red_row" ] && [ -n "$green_row" ] && [ "${notices:-0}" -ge 1 ] && break
        sleep 0.5
      done
      if jq -e '.exit_code == "3" and .outcome == "failure"' <<<"$red_row" >/dev/null 2>&1; then
        lok "the failing call reached hook bash: run-history row exit 3, outcome failure"
      else
        fail "live: no exit-3 row for the failing call — PostToolUseFailure did not reach hook bash: ${red_row:-<no row>}"
      fi
      if jq -e '.exit_code == "0" and .outcome == "success"' <<<"$green_row" >/dev/null 2>&1; then
        lok "control: the passing call's row reads 0"
      else
        fail "live: passing call's row: ${green_row:-<no row>}"
      fi
      if [ "${notices:-0}" -ge 1 ]; then lok "the failure edge fired from a real Claude failure"; else fail "live: edit got no Run FAILED (exit 3) notice for $red"; fi
    fi
  fi
fi

echo
echo "=== results: $PASS passed, $FAIL failed (floor $EXPECTED_PASS), $LIVE_PASS live ==="
if [ "$FAIL" -gt 0 ]; then
  exit 1
fi
if [ "$PASS" -ne "$EXPECTED_PASS" ]; then
  echo "  FAIL  coverage floor: expected exactly $EXPECTED_PASS passes, got $PASS — a section was skipped or double-counted"
  exit 1
fi
exit 0
