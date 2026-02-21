#!/bin/bash
# muxcode-bash-hook.sh — PostToolUse hook for Bash commands
#
# Detects build and test commands and drives the build->test->review chain:
#   build success -> trigger test agent
#   test success  -> trigger review agent
#   any failure   -> notify edit agent
# Also sends events to the analyst. Configured in settings.json as an async hook.
#
# Receives tool event JSON on stdin.

# Must be inside a tmux session
SESSION=$(tmux display-message -p '#S' 2>/dev/null) || exit 0
export BUS_SESSION="$SESSION"

# Detect role from tmux window name (use TMUX_PANE for correct pane targeting)
if [ -n "$TMUX_PANE" ]; then
  AGENT_ROLE=$(tmux display-message -t "$TMUX_PANE" -p '#W' 2>/dev/null) || exit 0
else
  AGENT_ROLE=$(tmux display-message -p '#W' 2>/dev/null) || exit 0
fi
export AGENT_ROLE

# Read event JSON from stdin
EVENT="$(cat)"
[ -z "$EVENT" ] && exit 0

# Extract command and exit code using jq (with python3 fallback)
if command -v jq &>/dev/null; then
  COMMAND=$(printf '%s' "$EVENT" | jq -r '.tool_input.command // empty' 2>/dev/null)
  EXIT_CODE=$(printf '%s' "$EVENT" | jq -r '
    (.tool_response // .tool_result // {}) as $r |
    if (.exit_code // $r.exit_code // "") != "" then (.exit_code // $r.exit_code)
    elif $r.interrupted then "1"
    elif ($r.stderr // "" | startswith("Error:")) then "1"
    else "0"
    end
  ' 2>/dev/null)
else
  COMMAND=$(printf '%s' "$EVENT" | python3 -c "
import json, sys
try:
    d = json.load(sys.stdin)
    print(d.get('tool_input', {}).get('command', ''))
except: pass
" 2>/dev/null)
  EXIT_CODE=$(printf '%s' "$EVENT" | python3 -c "
import json, sys
try:
    d = json.load(sys.stdin)
    r = d.get('tool_response', d.get('tool_result', {}))
    code = r.get('exit_code', d.get('exit_code', ''))
    if code == '':
        if r.get('interrupted'): code = '1'
        elif r.get('stderr', '').strip().startswith('Error:'): code = '1'
        else: code = '0'
    print(code)
except: pass
" 2>/dev/null)
fi

[ -z "$COMMAND" ] && exit 0

# Skip bus commands to prevent false positives
case "$COMMAND" in
  muxcode-agent-bus*|agent-bus*) exit 0 ;;
esac

# Extract the first real command (strip cd prefix, env vars, etc.)
FIRST_CMD=$(printf '%s' "$COMMAND" | sed 's/^cd [^;&|]* *[;&|]* *//' | sed 's/^[A-Z_]*=[^ ]* *//')

# Configurable patterns
BUILD_PATTERNS="${MUXCODE_BUILD_PATTERNS:-./build.sh|pnpm*build|go*build|make|cargo*build|cdk*synth|tsc}"
TEST_PATTERNS="${MUXCODE_TEST_PATTERNS:-./test.sh|jest|pnpm*test|pytest|go*test|go*vet|cargo*test|vitest}"

# Detect build commands
is_build=0
IFS='|' read -ra BPATS <<< "$BUILD_PATTERNS"
for pat in "${BPATS[@]}"; do
  case "$FIRST_CMD" in
    $pat*|bash*${pat##*/}*|sh*${pat##*/}*) is_build=1; break ;;
  esac
done

# Detect test commands
is_test=0
IFS='|' read -ra TPATS <<< "$TEST_PATTERNS"
for pat in "${TPATS[@]}"; do
  case "$FIRST_CMD" in
    $pat*|bash*${pat##*/}*|sh*${pat##*/}*|npx*${pat##*/}*) is_test=1; break ;;
  esac
done

# Route events and drive the build->test->review chain
if [ "$is_build" -eq 1 ]; then
  if [ -z "$EXIT_CODE" ]; then
    muxcode-agent-bus send analyze notify "Build completed (exit code unknown): $COMMAND" --type event --no-notify
  elif [ "$EXIT_CODE" = "0" ]; then
    muxcode-agent-bus send analyze notify "Build succeeded: $COMMAND" --type event --no-notify
    muxcode-agent-bus send test test "Build succeeded — run tests and report results" --type request
  else
    muxcode-agent-bus send analyze notify "Build FAILED (exit $EXIT_CODE): $COMMAND" --type event --no-notify
    muxcode-agent-bus send edit notify \
      "Build FAILED (exit $EXIT_CODE): $COMMAND — check build window" --type event
  fi
fi

if [ "$is_test" -eq 1 ]; then
  if [ -z "$EXIT_CODE" ]; then
    muxcode-agent-bus send analyze notify "Tests completed (exit code unknown): $COMMAND" --type event --no-notify
  elif [ "$EXIT_CODE" = "0" ]; then
    muxcode-agent-bus send analyze notify "Tests passed: $COMMAND" --type event --no-notify
    muxcode-agent-bus send review review "Tests passed — review the changes and report results to edit" --type request
  else
    muxcode-agent-bus send analyze notify "Tests FAILED (exit $EXIT_CODE): $COMMAND" --type event --no-notify
    muxcode-agent-bus send edit notify \
      "Tests FAILED (exit $EXIT_CODE): $COMMAND — check test window" --type event
  fi
fi
