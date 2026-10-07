#!/usr/bin/env bash
# Integration test for MUX-179: what reaches <role>-history.jsonl, and the
# console pane drawn from it, is redacted for the role — credentials for every
# role, PII as well for api/run/runner/watch.
#
# Hermetic: a scratch bus session driven through the installed binary. The
# scratch agent is its tool results and replies, fed to the real writers —
# `muxcode hook bash` (PostToolUse capture), `muxcode log` (self-report), and
# an edit `send --wait` that plan answers with `--reply-to` (the synthesized
# reply row, the road a plan diagnostic takes into plan-history.jsonl). Each
# fake key is asserted absent from its row and, where the role has a console,
# from `muxcode console <role> --once`. Negative controls: clean output, and a
# commit's author email outside the PII roles, are stored byte-identical with
# no notice, and the pane shows them — so every pane check can fail.
#
# Not covered, by design: the agent's own conversation, which PostToolUse
# cannot reach (docs/agents.md → Coverage by road).
#
# Coverage floor: the exact pass count. Requires a muxcode built from MUX-179
# (run ./build.sh first).
set -uo pipefail

PASS=0
FAIL=0
EXPECTED_PASS=17

command -v muxcode >/dev/null 2>&1 || { echo "SKIP: muxcode not installed"; exit 2; }
command -v jq >/dev/null 2>&1 || { echo "SKIP: jq is required"; exit 2; }
MUX=$(command -v muxcode)
. "$(dirname "${BASH_SOURCE[0]}")/lib/muxcode-version.sh"
require_muxcode_version "$MUX" v0.1.0 MUX-179 || { echo "  FAIL  binary precondition not met"; exit 1; }

SESSION="pii-scrub-roles-$$"
BUSDIR="/tmp/muxcode-bus-$SESSION"
WORK=$(mktemp -d /tmp/pii-scrub-roles-XXXXXX)
WAIT_PID=""

cleanup() {
  if [ -n "$WAIT_PID" ]; then
    kill "$WAIT_PID" 2>/dev/null || true
  fi
  rm -rf "$BUSDIR" "$WORK"
}
trap cleanup EXIT

ok()   { PASS=$((PASS + 1)); echo "  ok: $1"; }
fail() { FAIL=$((FAIL + 1)); echo "  FAIL: $1"; }

export BUS_SESSION="$SESSION"
export MUXCODE_LIFECYCLE_LOG_DIR="$WORK/lifecycle"
export MUXCODE_DEDUP_WINDOW=0
export MUXCODE_RUN_CLI=claude MUXCODE_COMMIT_CLI=claude MUXCODE_PLAN_CLI=claude MUXCODE_EDIT_CLI=claude
export COLUMNS=200 LINES=100
unset MUXCODE_AGENT_CLI 2>/dev/null || true

BANNER='[muxcode pii-scrub:'
KEY_API="sk-fake-0123456789abcdef"
KEY_TOKEN="fake-token-0123456789abcdef"
ESC=$(printf '\033')

# hook_bash ROLE COMMAND STDOUT — a Claude-shaped PostToolUse Bash event into the real hook.
hook_bash() {
  jq -n --arg cmd "$2" --arg out "$3" \
    '{hook_event_name:"PostToolUse",tool_name:"Bash",tool_input:{command:$cmd},tool_response:{stdout:$out,stderr:"",interrupted:false}}' \
    | AGENT_ROLE="$1" "$MUX" hook bash >/dev/null 2>&1
}

last_row() { tail -1 "$BUSDIR/$1" 2>/dev/null; }

# pane ROLE — one frame of the role's console, ANSI stripped.
pane() { "$MUX" console "$1" --once </dev/null 2>/dev/null | sed "s/${ESC}\[[0-9;?]*[A-Za-z]//g"; }

opens_with_banner() { jq -e --arg b "$BANNER" '.output | startswith($b)' <<<"$1" >/dev/null 2>&1; }

echo "=== PII scrub by role integration test (MUX-179) ==="
"$MUX" init "$SESSION" >/dev/null 2>&1 || true

echo "-- hook road, PII role: a key in a run agent's ps eww dump"
hook_bash run "ps eww -p 21146" $'PID TT STAT TIME COMMAND\n21146 s001 S 0:00.01 muxcode inbox --poll\nMUXCODE_OPENCODE_API_KEY='"$KEY_API"$'\nPATH=/usr/bin'
row=$(last_row run-history.jsonl)
if [ -n "$row" ] && ! grep -qF "$KEY_API" <<<"$row"; then ok "run-history row holds no key"; else fail "key reached run-history, or no row: $row"; fi
if opens_with_banner "$row"; then ok "run row opens with the pii-scrub notice"; else fail "run row lacks the notice: $row"; fi
frame=$(pane run)
if grep -qF "SECRET_REDACTED" <<<"$frame" && ! grep -qF "$KEY_API" <<<"$frame"; then
  ok "run console pane shows the placeholder, not the key"
else
  fail "run pane: $frame"
fi

echo "-- hook road: the label on the line the 15-line tail drops"
out=$'password=\nSuperSecret123'
for i in $(seq -w 1 14); do out+=$'\n'"line_$i"; done
hook_bash run "cat creds.txt" "$out"
row=$(last_row run-history.jsonl)
if [ -n "$row" ] && ! grep -qF "SuperSecret123" <<<"$row"; then ok "secret split from its label by the tail is still redacted"; else fail "secret survived the tail: $row"; fi

echo "-- hook road, every role: a token in plan's git output"
hook_bash plan "git push" $'remote: GITHUB_TOKEN='"$KEY_TOKEN"$'\nTo github.com:example/repo.git\n   872f4b0..d573410  main -> main'
row=$(last_row commit-history.jsonl)
if [ -n "$row" ] && ! grep -qF "$KEY_TOKEN" <<<"$row"; then ok "plan's git row holds no token"; else fail "token reached commit-history, or no row: $row"; fi
if opens_with_banner "$row"; then ok "plan's git row opens with the pii-scrub notice"; else fail "plan's git row lacks the notice: $row"; fi
if ! grep -qF "$KEY_TOKEN" <<<"$(pane commit)"; then ok "commit console pane holds no token"; else fail "token shown in the commit pane"; fi

echo "-- self-report road: plan logs a diagnostic"
AGENT_ROLE=plan "$MUX" log plan "env of 21146: API_KEY=$KEY_API" --output $'AGENT_ROLE=plan\nMUXCODE_OPENCODE_API_KEY='"$KEY_API" >/dev/null 2>&1
row=$(last_row plan-history.jsonl)
if [ -n "$row" ] && ! grep -qF "$KEY_API" <<<"$row"; then ok "plan self-report holds no key in output or summary"; else fail "key reached plan-history, or no row: $row"; fi
if opens_with_banner "$row"; then ok "plan self-report opens with the pii-scrub notice"; else fail "plan self-report lacks the notice: $row"; fi

echo "-- reply road: plan answers an edit --wait with a diagnostic"
MUXCODE_WAIT_DEGRADE_SECS=30 MUXCODE_INBOX_POLL_TIMEOUT=30 AGENT_ROLE=edit \
  "$MUX" send plan diagnose "Whose listener is pid 21146?" --wait --no-notify >"$WORK/wait.out" 2>&1 &
WAIT_PID=$!
req_id=""
for _ in $(seq 1 40); do
  req_id=$(jq -r 'select(.type=="request" and .action=="diagnose") | .id' "$BUSDIR/inbox/plan.jsonl" 2>/dev/null | tail -1)
  [ -n "$req_id" ] && break
  sleep 0.25
done
if [ -n "$req_id" ]; then ok "edit's --wait request reached plan"; else fail "no diagnose request in plan's inbox"; fi
AGENT_ROLE=plan "$MUX" send edit diagnose "Listener 21146 is plan's: MUXCODE_OPENCODE_API_KEY=$KEY_API AGENT_ROLE=plan" \
  --type response --reply-to "$req_id" --no-notify >/dev/null 2>&1
for _ in $(seq 1 60); do
  kill -0 "$WAIT_PID" 2>/dev/null || break
  sleep 0.25
done
row=$(jq -c 'select(.source=="bus-response")' "$BUSDIR/plan-history.jsonl" 2>/dev/null | tail -1)
if [ -n "$row" ] && ! grep -qF "$KEY_API" <<<"$row"; then ok "plan's reply row holds no key"; else fail "key reached plan's reply row, or no row: $row"; fi
if opens_with_banner "$row" && jq -e '.summary | startswith("Listener 21146")' <<<"$row" >/dev/null 2>&1; then
  ok "reply row opens with the notice, its summary the scrubbed reply line"
else
  fail "reply row notice or summary wrong: $row"
fi

echo "-- negative control: clean output is stored and shown unchanged"
clean=$'total 2\nclean-marker-file_a.go\nfile_b.go'
hook_bash run "ls -l" "$clean"
row=$(last_row run-history.jsonl)
if jq -e --arg c "$clean" '.output == $c' <<<"$row" >/dev/null 2>&1; then ok "clean run row stored byte-identical, no notice"; else fail "clean run row altered: $row"; fi
frame=$(pane run)
if grep -qF "clean-marker-file_a.go" <<<"$frame" && ! grep -qF "$BANNER" <<<"$frame"; then
  ok "run pane shows the clean output with no notice"
else
  fail "run pane for clean output: $frame"
fi

echo "-- negative control: PII outside the PII roles is kept"
commit_out=$'[main 3107fbb] gofmt the bus package\n Author: Jane Doe <jane.doe@example.com>\n 1 file changed, 2 insertions(+)'
hook_bash commit "git commit -m gofmt" "$commit_out"
row=$(last_row commit-history.jsonl)
if jq -e --arg c "$commit_out" '.output == $c' <<<"$row" >/dev/null 2>&1; then ok "commit row keeps its author email byte-identical"; else fail "commit row altered: $row"; fi
if grep -qF "jane.doe@example.com" <<<"$(pane commit)"; then ok "commit pane renders the row's output"; else fail "commit pane does not show the row's output"; fi

echo "-- negative control: a clean self-report is stored as given"
AGENT_ROLE=plan "$MUX" log plan "env of 21146 read" --output "AGENT_ROLE=plan" >/dev/null 2>&1
row=$(last_row plan-history.jsonl)
if jq -e '.output == "AGENT_ROLE=plan" and .summary == "env of 21146 read"' <<<"$row" >/dev/null 2>&1; then
  ok "clean self-report stored byte-identical, no notice"
else
  fail "clean self-report altered: $row"
fi

echo
echo "=== results: $PASS passed, $FAIL failed (floor $EXPECTED_PASS) ==="
if [ "$FAIL" -gt 0 ]; then
  exit 1
fi
if [ "$PASS" -ne "$EXPECTED_PASS" ]; then
  echo "  FAIL  coverage floor: expected exactly $EXPECTED_PASS passes, got $PASS — a section was skipped or double-counted"
  exit 1
fi
exit 0
