#!/usr/bin/env bash
# Integration test for MUX-177: the watch chain notifies edit only for a log
# read, and no chain message carries a multi-line or oversized body.
#
# Hermetic: muxcode built from the tree (MUXCODE_WATCH_CHAIN_GATE_BIN overrides),
# a scratch bus session, HOME and chain config, Claude PostToolUse and
# PostToolUseFailure payloads fed to the real `muxcode hook bash`. Asserts:
#   silent       — scratch calls in the watch pane (ps, ls, the 2026-09-10 heredoc,
#                  the 2026-09-22 nohup listener, a cd-prefixed muxcode send, a code
#                  grep that found nothing, quoted `&& tail` text) each write a
#                  watch-history row — proof the hook ran — and send edit and
#                  analyze nothing; a bare bus call writes no row and sends nothing
#   negative     — a log read still notifies edit: `tail` on exit 0, a log `grep`
#   control        on exit 1 (and analyze, which the watch chain tells of failures),
#                  a cd-prefixed `kubectl logs`
#   bounded      — a multi-line block opening with `tail` (the gate passes it on its
#                  first statement) and a failing run-pane heredoc reach edit and
#                  analyze on one line, joined with ⏎ and visibly truncated
#   hygiene      — no message in any inbox holds a newline or exceeds 500 bytes,
#                  validatePayload()'s limits
# Coverage floor: the exact pass count.
set -uo pipefail

PASS=0
FAIL=0
EXPECTED_PASS=18

command -v jq >/dev/null 2>&1 || { echo "SKIP: jq is required"; exit 2; }

REPO=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
SESSION="watch-chain-gate-$$"
BUSDIR="/tmp/muxcode-bus-$SESSION"
WORK=$(mktemp -d /tmp/watch-chain-gate-XXXXXX)

cleanup() { rm -rf "$BUSDIR" "$WORK"; }
trap cleanup EXIT

if [ -n "${MUXCODE_WATCH_CHAIN_GATE_BIN:-}" ]; then
  MUX=$MUXCODE_WATCH_CHAIN_GATE_BIN
else
  command -v go >/dev/null 2>&1 || { echo "SKIP: go is required to build muxcode from the tree"; exit 2; }
  mkdir -p "$WORK/bin"
  (cd "$REPO/tools/muxcode" && go build -buildvcs=false -o "$WORK/bin/muxcode" .) \
    || { echo "SKIP: go build muxcode failed"; exit 2; }
  MUX="$WORK/bin/muxcode"
fi

ok()   { PASS=$((PASS + 1)); echo "  ok: $1"; }
fail() { FAIL=$((FAIL + 1)); echo "  FAIL: $1"; }

mkdir -p "$WORK/home" "$WORK/config" "$WORK/lifecycle"
export HOME="$WORK/home"
export BUS_SESSION="$SESSION"
export MUXCODE_CONFIG_DIR="$WORK/config"
export MUXCODE_LIFECYCLE_LOG_DIR="$WORK/lifecycle"
export MUXCODE_DEDUP_WINDOW=0
export MUXCODE_WATCH_CLI=claude MUXCODE_RUN_CLI=claude
unset MUXCODE_CONFIG MUXCODE_AGENT_CLI MUXCODE_BUILD_PATTERNS MUXCODE_TEST_PATTERNS MUXCODE_TEST_PRECHECK_PATTERNS 2>/dev/null || true
cd "$WORK" || exit 1

# claude_ok ROLE COMMAND — a finished Claude PostToolUse Bash success into the real hook.
claude_ok() {
  jq -nc --arg c "$2" \
    '{hook_event_name:"PostToolUse",tool_name:"Bash",tool_input:{command:$c},tool_use_id:"toolu_ok",tool_response:{stdout:"output",stderr:"",interrupted:false,isImage:false}}' \
    | AGENT_ROLE="$1" "$MUX" hook bash >/dev/null 2>&1
}

# claude_fail ROLE COMMAND EXIT — the PostToolUseFailure shape, status on the first line of error.
claude_fail() {
  jq -nc --arg c "$2" --arg e "Exit code $3"$'\nsomething failed' \
    '{hook_event_name:"PostToolUseFailure",tool_name:"Bash",tool_input:{command:$c},tool_use_id:"toolu_fail",error:$e,is_interrupt:false}' \
    | AGENT_ROLE="$1" "$MUX" hook bash >/dev/null 2>&1
}

# lines FILE — line count, 0 when the file does not exist.
lines() { if [ -f "$1" ]; then wc -l <"$1" | tr -d ' '; else echo 0; fi; }

# inbox_count ROLE NEEDLE... — event:notify messages in ROLE's inbox whose payload contains every NEEDLE.
inbox_count() {
  local role=$1
  shift
  [ -f "$BUSDIR/inbox/$role.jsonl" ] || { echo 0; return; }
  jq -c --args 'select(.type == "event" and .action == "notify" and (.payload as $p | $ARGS.positional | all(. as $n | $p | contains($n))))' \
    "$@" <"$BUSDIR/inbox/$role.jsonl" 2>/dev/null | wc -l | tr -d ' '
}

# silent LABEL OUTCOME COMMAND [EXIT] — the call must reach the hook (one more
# watch-history row) and leave edit's and analyze's inboxes empty.
silent() {
  local label=$1 outcome=$2 command=$3 before after
  before=$(lines "$BUSDIR/watch-history.jsonl")
  if [ "$outcome" = ok ]; then claude_ok watch "$command"; else claude_fail watch "$command" "$4"; fi
  after=$(lines "$BUSDIR/watch-history.jsonl")
  local edit analyze
  edit=$(lines "$BUSDIR/inbox/edit.jsonl")
  analyze=$(lines "$BUSDIR/inbox/analyze.jsonl")
  if [ "$after" -eq $((before + 1)) ] && [ "$edit" = 0 ] && [ "$analyze" = 0 ]; then
    ok "silent: $label"
  else
    fail "silent: $label — history rows $before→$after (want +1), edit inbox $edit, analyze inbox $analyze (want 0, 0)"
  fi
}

echo "=== watch chain gate integration test (MUX-177) ==="
"$MUX" init "$SESSION" >/dev/null 2>&1 || true

echo "-- watch pane: calls that are not log reads notify nobody"
silent "ps piped to grep" ok "ps aux | grep muxcode"
silent "ls of a bus dir" ok "ls -la /tmp/muxcode-bus-x"
silent "the 2026-09-10 scratch heredoc" ok "cat > /tmp/probe.sh <<'EOF'"$'\n'"#!/usr/bin/env bash"$'\n'"ps -o pid,ppid,command -p \$PPID"$'\n'"EOF"$'\n'"bash /tmp/probe.sh"
silent "the 2026-09-22 nohup listener" ok "nohup muxcode inbox --poll --loop > /tmp/watch-listener.log 2>&1 & disown"
silent "cd-prefixed muxcode send" ok "cd /repo && muxcode send edit notify \"watch done\""
silent "a code grep that found nothing (exit 1)" fail "grep -n FireWatch bus/hook.go" 1
silent "quoted '&& tail' text after a cd" ok "cd /repo && echo \"example && tail app.log\""

before=$(lines "$BUSDIR/watch-history.jsonl")
claude_ok watch "muxcode inbox --peek"
after=$(lines "$BUSDIR/watch-history.jsonl")
edit=$(lines "$BUSDIR/inbox/edit.jsonl")
analyze=$(lines "$BUSDIR/inbox/analyze.jsonl")
if [ "$after" = "$before" ] && [ "$edit" = 0 ] && [ "$analyze" = 0 ]; then
  ok "a bare bus call writes no row and notifies nobody"
else
  fail "bare bus call: history rows $before→$after, edit inbox $edit, analyze inbox $analyze (want no row, 0, 0)"
fi

echo "-- negative control: a log read still notifies edit"
claude_ok watch "tail -n 200 /var/log/app.log"
n=$(inbox_count edit "Watch command exited 0 (tail -n 200 /var/log/app.log)")
if [ "$n" = 1 ]; then ok "tail on exit 0 notifies edit"; else fail "edit notices for tail: $n, want 1 — an installed binary predating MUX-177 has no gate"; fi
claude_fail watch "grep ERROR logs/app.log" 1
n=$(inbox_count edit "Watch command FAILED (exit 1): grep ERROR logs/app.log")
if [ "$n" = 1 ]; then ok "a log grep on exit 1 notifies edit"; else fail "edit notices for the log grep: $n, want 1"; fi
n=$(inbox_count analyze "Watch FAILED (exit 1): grep ERROR logs/app.log")
if [ "$n" = 1 ]; then ok "the log grep's failure reaches analyze"; else fail "analyze notices for the log grep: $n, want 1"; fi
claude_ok watch "cd /srv/app && kubectl logs deploy/api --since=10m"
n=$(inbox_count edit "Watch command exited 0 (cd /srv/app && kubectl logs deploy/api --since=10m)")
if [ "$n" = 1 ]; then ok "a cd-prefixed kubectl logs notifies edit"; else fail "edit notices for kubectl logs: $n, want 1"; fi

echo "-- bounded: multi-line commands reach edit and analyze on one line"
block="tail -n 50 /var/log/app.log"
for i in $(seq 1 30); do block+=$'\n'"grep -c ERROR /var/log/app.log.$i"; done
claude_fail watch "$block" 2
n=$(inbox_count edit "Watch command FAILED (exit 2): tail -n 50 /var/log/app.log ⏎ grep -c ERROR" "[truncated ")
if [ "$n" = 1 ]; then ok "watch: a tail-led block notifies edit joined with ⏎ and truncated"; else fail "bounded watch notices to edit: $n, want 1"; fi
n=$(inbox_count analyze "Watch FAILED (exit 2): tail -n 50 /var/log/app.log ⏎ grep -c ERROR" "[truncated ")
if [ "$n" = 1 ]; then ok "watch: analyze's copy is bounded the same way"; else fail "bounded watch notices to analyze: $n, want 1"; fi

heredoc="bash scripts/probe.sh <<'EOF'"
for i in $(seq 1 40); do heredoc+=$'\n'"echo scratch investigation line $i"; done
heredoc+=$'\n'"EOF"
claude_fail run "$heredoc" 4
n=$(inbox_count edit "Run FAILED (exit 4): bash scripts/probe.sh <<'EOF' ⏎ echo scratch" "[truncated ")
if [ "$n" = 1 ]; then ok "run: a failing heredoc notifies edit bounded"; else fail "bounded run notices to edit: $n, want 1"; fi
n=$(inbox_count analyze "Run FAILED (exit 4): bash scripts/probe.sh <<'EOF' ⏎ echo scratch" "[truncated ")
if [ "$n" = 1 ]; then ok "run: analyze's copy is bounded too"; else fail "bounded run notices to analyze: $n, want 1 — the analyst notice is built from the raw command"; fi

echo "-- hygiene: every message in every inbox (at least the 5 edit and 3 analyze notices above)"
total=$(cat "$BUSDIR"/inbox/*.jsonl 2>/dev/null | jq -s 'length')
multiline=$(cat "$BUSDIR"/inbox/*.jsonl 2>/dev/null | jq -s '[.[] | select(.payload | test("[\r\n]"))] | length')
oversized=$(cat "$BUSDIR"/inbox/*.jsonl 2>/dev/null | jq -s '[.[] | select((.payload | utf8bytelength) > 500)] | length')
if [ "${total:-0}" -ge 8 ] && [ "$multiline" = 0 ]; then
  ok "no message body holds a newline ($total messages)"
else
  fail "hygiene: $multiline of ${total:-0} messages hold a newline (want 0 of at least 8)"
fi
if [ "${total:-0}" -ge 8 ] && [ "$oversized" = 0 ]; then
  ok "no message body exceeds 500 bytes"
else
  fail "hygiene: $oversized of ${total:-0} messages exceed 500 bytes"
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
