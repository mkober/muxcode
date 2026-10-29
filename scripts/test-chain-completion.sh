#!/usr/bin/env bash
# Integration test for MUX-176: a Bash call that had not finished when its hook
# fired is not chain evidence. Claude completes a run_in_background call at
# dispatch and fires PostToolUse then, with a backgroundTaskId and no exit
# status, and fires no hook when the task ends — so before MUX-176 `hook bash`
# recorded the launch as exit 0 and the run chain told watch a script had
# passed minutes before it failed.
#
# Hermetic section: a scratch bus session against the installed binary, chain
# config pinned to the defaults (MUXCODE_CONFIG_DIR is scratch), Claude
# payloads fed to the real `muxcode hook bash`. Asserts:
#   build agent       — a backgrounded ./build.sh moves the workflow to building
#                       but writes no row and requests no test; the foreground
#                       build that follows (control) records 0 and requests test
#   background launch — the payload measured on 2026-10-22, on a run agent: no
#                       run-history row, no chain message at all, and one
#                       chain-withheld row naming the run chain, the task and
#                       the command, read through `lifecycle show` (the
#                       documented road)
#   timed out         — a foreground call Claude moved to the background (task
#                       id, no flag): withheld the same way
#   unreadable        — a PostToolUse with neither tool_response nor exit_code
#                       fails closed: withheld, task "unknown"
#   finished control  — the same kind of call completing in the foreground:
#                       row 0, the watch request, and no chain-withheld row
# Coverage floor: the exact hermetic pass count.
#
# Live section: `--live` or MUXCODE_CHAIN_COMPLETION_LIVE=1 with `claude` on
# PATH — one real `claude -p` call, outside the floor, skipped with a reason
# otherwise. config/settings.json's Bash PostToolUse and PostToolUseFailure
# entries are passed with --settings, and --setting-sources project keeps the
# user's own hooks from firing beside them. The model backgrounds a script that
# sleeps and exits 3, runs a passing script in the foreground, then runs a
# waiter that returns only once the backgrounded script has ended — so a hook
# Claude fired at the task's end would have landed before the checks. That the
# end fell inside the session is asserted, not assumed: the end marker must
# exist when claude returns, and the transcript must show the waiter ran in the
# foreground and reported the end. The
# backgrounded call must leave no row and no watch request, and a
# chain-withheld row carrying the task id Claude reported; the foreground call
# is the control. A model that does not background the call fails the section,
# since it would prove nothing.
#
# Requires a muxcode built from MUX-176 Phase 2 (run ./build.sh first).
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
require_muxcode_version "$MUX" v0.1.0 MUX-176 || { echo "  FAIL  binary precondition not met"; exit 1; }

LIVE=0
if [ "${1:-}" = --live ] || [ "${MUXCODE_CHAIN_COMPLETION_LIVE:-}" = 1 ]; then
  LIVE=1
fi

REPO=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
SESSION="chain-completion-$$"
BUSDIR="/tmp/muxcode-bus-$SESSION"
WORK=$(mktemp -d /tmp/chain-completion-XXXXXX)
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
unset MUXCODE_AGENT_CLI MUXCODE_BUILD_PATTERNS MUXCODE_TEST_PATTERNS MUXCODE_TEST_PRECHECK_PATTERNS 2>/dev/null || true
cd "$WORK" || exit 1

# post ROLE COMMAND FLAG RESPONSE — a Claude PostToolUse Bash event into the
# real hook. FLAG is the raw run_in_background JSON and RESPONSE the raw
# tool_response JSON; "" leaves the key out.
post() {
  jq -nc --arg c "$2" --arg flag "$3" --arg resp "$4" '
    {hook_event_name: "PostToolUse", tool_name: "Bash", tool_use_id: "toolu_mux176",
     tool_input: ({command: $c} + (if $flag == "" then {} else {run_in_background: ($flag | fromjson)} end))}
    + (if $resp == "" then {} else {tool_response: ($resp | fromjson)} end)' \
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

# tool_result LOG NEEDLE BACKGROUND — the text a stream-json LOG shows Claude got
# back from its Bash calls whose command contains NEEDLE and whose
# run_in_background is BACKGROUND (true or false).
tool_result() {
  jq -rs --arg s "$2" --argjson bg "$3" '
    [.[] | select(.type == "assistant") | .message.content[]?
      | select(.type == "tool_use" and .name == "Bash" and (.input.command // "" | contains($s))
               and ((.input.run_in_background // false) == $bg)) | .id] as $ids
    | [.[] | select(.type == "user") | .message.content[]?
      | select(.type == "tool_result" and (.tool_use_id as $t | $ids | index($t)))
      | .content | if type == "array" then map(.text // "") | join("") else . end] | join("\n")' "$1" 2>/dev/null
}

# withheld NEEDLE... — `lifecycle show --event chain-withheld` lines holding every NEEDLE.
withheld() {
  "$MUX" lifecycle show "$SESSION" --event chain-withheld 2>/dev/null \
    | jq -R --args 'select(. as $l | $ARGS.positional | all(. as $n | $l | contains($n)))' "$@" \
    | wc -l | tr -d ' '
}

STARTED='{"stdout":"","stderr":"","interrupted":false,"isImage":false}'
STALE="an installed muxcode predating MUX-176 records the launch as exit 0 (run ./build.sh)"

echo "=== chain completion integration test (MUX-176) ==="
"$MUX" init "$SESSION" >/dev/null 2>&1 || true

echo "-- build agent: a backgrounded ./build.sh, then the foreground build (control)"
post build "./build.sh" true '{"stdout":"","stderr":"","interrupted":false,"isImage":false,"backgroundTaskId":"bbuild001"}'
row=$(row_for build-history.jsonl "./build.sh")
tests=$(inbox_count test request test "./build.sh")
if [ -z "$row" ] && [ "$tests" = 0 ]; then
  ok "backgrounded build: no build-history row, no test request"
else
  fail "backgrounded build: row ${row:-<none>}, $tests test request(s); want none — $STALE"
fi
state=$("$MUX" workflow 2>/dev/null)
held=$(withheld "build chain not fired for build" "(background task bbuild001)" "./build.sh")
if grep -q 'state: building' <<<"$state" && [ "$held" = 1 ]; then
  ok "the workflow moved to building, and chain-withheld names the build chain and task"
else
  fail "backgrounded build: workflow '$state', $held matching chain-withheld row(s); want building and 1"
fi
post build "./build.sh" "" '{"stdout":"ok","stderr":"","interrupted":false,"isImage":false}'
row=$(row_for build-history.jsonl "./build.sh")
tests=$(inbox_count test request test "Build succeeded (./build.sh)")
if jq -e '.exit_code == "0" and .outcome == "success"' <<<"$row" >/dev/null 2>&1 && [ "$tests" = 1 ]; then
  ok "control: the foreground build records 0 and requests test"
else
  fail "foreground build: row ${row:-<none>}, $tests test request(s); want exit 0 and 1"
fi

SLOW="bash scripts/slow.sh"
echo "-- run agent: the background launch measured on 2026-10-22"
post run "$SLOW" true '{"stdout":"","stderr":"","interrupted":false,"isImage":false,"noOutputExpected":false,"backgroundTaskId":"bhermetic1"}'
row=$(row_for run-history.jsonl "$SLOW")
if [ -z "$row" ]; then ok "no run-history row for the launch"; else fail "launch wrote a row: $row — $STALE"; fi
watch=$(inbox_count watch request watch "$SLOW")
notices=$(inbox_count edit event notify "$SLOW")
if [ "$watch" = 0 ] && [ "$notices" = 0 ]; then
  ok "no chain message at all: no watch request, no edit notice"
else
  fail "launch fired the chain: $watch watch request(s), $notices edit notice(s)"
fi
held=$(withheld "run chain not fired for run" "(background task bhermetic1)" "$SLOW")
if [ "$held" = 1 ]; then ok "chain-withheld names the run chain, the task and the command"; else fail "chain-withheld rows for $SLOW: $held, want 1"; fi

TIMED="bash scripts/timed.sh"
echo "-- run agent: a foreground call Claude moved to the background on timeout"
post run "$TIMED" "" '{"stdout":"","stderr":"","interrupted":false,"isImage":false,"backgroundTaskId":"btimed002","timedOutAfterMs":120000}'
row=$(row_for run-history.jsonl "$TIMED")
watch=$(inbox_count watch request watch "$TIMED")
if [ -z "$row" ] && [ "$watch" = 0 ]; then ok "no row and no watch request without the flag"; else fail "timed-out call: row ${row:-<none>}, $watch watch request(s)"; fi
held=$(withheld "run chain not fired for run" "(background task btimed002)" "$TIMED")
if [ "$held" = 1 ]; then ok "chain-withheld carries the timed-out call's task id"; else fail "chain-withheld rows for $TIMED: $held, want 1"; fi

BLIND="bash scripts/blind.sh"
echo "-- run agent: completion that cannot be read fails closed"
post run "$BLIND" "" ""
row=$(row_for run-history.jsonl "$BLIND")
watch=$(inbox_count watch request watch "$BLIND")
if [ -z "$row" ] && [ "$watch" = 0 ]; then ok "no tool_response and no exit_code: no row, no watch request"; else fail "unreadable call: row ${row:-<none>}, $watch watch request(s)"; fi
held=$(withheld "run chain not fired for run" "(background task unknown)" "$BLIND")
if [ "$held" = 1 ]; then ok "chain-withheld names the task as unknown"; else fail "chain-withheld rows for $BLIND: $held, want 1"; fi

GREEN="bash scripts/green.sh"
echo "-- run agent, negative control: the same kind of call completing in the foreground"
post run "$GREEN" "" "$STARTED"
row=$(row_for run-history.jsonl "$GREEN")
if jq -e '.exit_code == "0" and .outcome == "success"' <<<"$row" >/dev/null 2>&1; then ok "finished call: row exit 0, outcome success"; else fail "finished call's row: ${row:-<none>} — the guard is withholding finished calls"; fi
watch=$(inbox_count watch request watch "Run succeeded ($GREEN)")
if [ "$watch" = 1 ]; then ok "finished call: the success edge requests watch"; else fail "watch requests for $GREEN: $watch, want 1"; fi
held=$(withheld "$GREEN")
if [ "$held" = 0 ]; then ok "finished call: no chain-withheld row"; else fail "a finished call logged chain-withheld $held time(s)"; fi

# ── Live: a real Claude background task ───────────────────────────
if [ "$LIVE" != 1 ]; then
  echo "-- live section skipped (pass --live or set MUXCODE_CHAIN_COMPLETION_LIVE=1; spends a real model call)"
elif ! command -v claude >/dev/null 2>&1; then
  echo "-- live: Claude skipped (claude not on PATH)"
else
  echo "-- live: Claude"
  # The hermetic control left the workflow in watching, where triggerChain suppresses a run success.
  "$MUX" workflow reset >/dev/null 2>&1
  id="$$x$RANDOM"
  slow="slow-$id.sh"
  green="green-$id.sh"
  waiter="wait-$id.sh"
  printf '#!/usr/bin/env bash\ntouch %s.started\nsleep %s\ntouch %s.ended\nexit 3\n' \
    "$id" "${MUXCODE_CHAIN_COMPLETION_LIVE_SLEEP:-12}" "$id" >"$PROJ/$slow"
  printf '#!/usr/bin/env bash\necho "PASS: live green"\n' >"$PROJ/$green"
  printf '#!/usr/bin/env bash\nfor _ in $(seq 1 120); do [ -e %s.ended ] && break; sleep 0.5; done\nsleep 3\n[ -e %s.ended ] || { echo "background script never ended"; exit 1; }\necho "background script ended"\n' \
    "$id" "$id" >"$PROJ/$waiter"
  live_settings=$(jq -c '{hooks: {PostToolUse: [.hooks.PostToolUse[] | select(.matcher == "Bash")], PostToolUseFailure: (.hooks.PostToolUseFailure // [])}}' "$REPO/config/settings.json")
  ask="Use the Bash tool with run_in_background set to true to run exactly this command: bash $slow   Then use the Bash tool, not in the background, to run exactly this command: bash $green   Then use the Bash tool, not in the background, to run exactly this command: bash $waiter   Do not read any output file. Then reply with the single word done."
  log="$WORK/claude-live"
  (cd "$PROJ" && unset TMUX TMUX_PANE && PATH="$MUXDIR:$PATH" AGENT_ROLE=run \
    claude -p "$ask" --model "${MUXCODE_CHAIN_COMPLETION_LIVE_MODEL:-haiku}" --settings "$live_settings" \
    --setting-sources project --allowedTools Bash --output-format stream-json --verbose \
    >"$log.jsonl" 2>"$log.err" </dev/null)
  run_status=$?
  ended_at_return=0
  [ -e "$PROJ/$id.ended" ] && ended_at_return=1
  bg_id=$(tool_result "$log.jsonl" "$slow" true | grep -oE 'background with ID: [A-Za-z0-9]+' | head -1 | awk '{print $NF}')
  waited=$(tool_result "$log.jsonl" "$waiter" false)
  if [ "$run_status" != 0 ] || [ -z "$bg_id" ]; then
    fail "live: the model did not background $slow (CLI exit $run_status, no background task id in its result) — the section proves nothing; stderr: $(head -c 300 "$log.err")"
  else
    lok "claude backgrounded $slow as task $bg_id"
    green_row=""
    green_watch=0
    held=0
    for _ in $(seq 1 40); do
      green_row=$(row_for run-history.jsonl "$green")
      green_watch=$(inbox_count watch request watch "Run succeeded (" "$green")
      held=$(withheld "run chain not fired for run" "(background task $bg_id)" "$slow")
      [ -n "$green_row" ] && [ "${green_watch:-0}" -ge 1 ] && [ "${held:-0}" -ge 1 ] && break
      sleep 0.5
    done
    if [ "$ended_at_return" = 1 ] && grep -q 'background script ended' <<<"$waited"; then
      lok "the backgrounded script ended (exit 3) inside the session: the foreground waiter saw it before claude returned"
    else
      fail "live: no proof the backgrounded script ended while the session was open (marker at CLI return: $ended_at_return; foreground waiter result: ${waited:-<none>})"
    fi
    row=$(row_for run-history.jsonl "$slow")
    watch=$(inbox_count watch request watch "$slow")
    if [ -z "$row" ] && [ "$watch" = 0 ]; then
      lok "no run-history row and no watch request for the backgrounded call, after it ended"
    else
      fail "live: backgrounded call has row ${row:-<none>} and $watch watch request(s) — exit 0 is the MUX-176 defect; a non-zero code means Claude now fires a hook at task end, so revisit the spec"
    fi
    if [ "${held:-0}" -ge 1 ]; then lok "chain-withheld names the run chain and Claude's task id $bg_id"; else fail "live: no chain-withheld row for $slow with task $bg_id"; fi
    if jq -e '.exit_code == "0" and .outcome == "success"' <<<"$green_row" >/dev/null 2>&1 && [ "${green_watch:-0}" -ge 1 ]; then
      lok "control: the foreground call recorded 0 and requested watch"
    else
      fail "live: foreground control row ${green_row:-<none>}, ${green_watch:-0} watch request(s)"
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
