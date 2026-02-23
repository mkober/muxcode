#!/bin/bash
# muxcode-bash-hook.sh — PostToolUse hook for Bash commands
#
# Detects build, test, deploy, and git commands:
#   build success  -> trigger test agent
#   test success   -> trigger review agent
#   deploy commands -> log to deploy history + trigger chain
#   any failure    -> notify edit agent
#   git commands   -> log to commit history (no chain trigger)
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
  DESCRIPTION=$(printf '%s' "$EVENT" | jq -r '.tool_input.description // empty' 2>/dev/null)
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
  DESCRIPTION=$(printf '%s' "$EVENT" | python3 -c "
import json, sys
try:
    d = json.load(sys.stdin)
    print(d.get('tool_input', {}).get('description', ''))
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
DEPLOY_PATTERNS="${MUXCODE_DEPLOY_PATTERNS:-cdk*diff|cdk*deploy|cdk*destroy|cdk*synth*--all|terraform*plan|terraform*apply|terraform*destroy|pulumi*up|pulumi*destroy|pulumi*preview|sam*deploy|sam*package|cloudformation*deploy|cloudformation*create-stack|cloudformation*update-stack}"
DEPLOY_APPLY_PATTERNS="${MUXCODE_DEPLOY_APPLY_PATTERNS:-cdk*deploy|cdk*destroy|terraform*apply|terraform*destroy|pulumi*up|pulumi*destroy|sam*deploy|cloudformation*deploy|cloudformation*create-stack|cloudformation*update-stack}"
GIT_PATTERNS="${MUXCODE_GIT_PATTERNS:-git*commit|git*push|git*merge|git*rebase|git*tag|git*cherry-pick|gh*pr*create|gh*pr*merge|gh*pr*close|gh*release*create}"

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

# Detect deploy commands
is_deploy=0
IFS='|' read -ra DPATS <<< "$DEPLOY_PATTERNS"
for pat in "${DPATS[@]}"; do
  case "$FIRST_CMD" in
    $pat*|bash*${pat##*/}*|sh*${pat##*/}*) is_deploy=1; break ;;
  esac
done

# Detect deploy-apply commands (subset of deploy that triggers verify chain)
is_deploy_apply=0
if [ "$is_deploy" -eq 1 ]; then
  IFS='|' read -ra DAPATS <<< "$DEPLOY_APPLY_PATTERNS"
  for pat in "${DAPATS[@]}"; do
    case "$FIRST_CMD" in
      $pat*|bash*${pat##*/}*|sh*${pat##*/}*) is_deploy_apply=1; break ;;
    esac
  done
fi

# Detect git commands
is_git=0
IFS='|' read -ra GPATS <<< "$GIT_PATTERNS"
for pat in "${GPATS[@]}"; do
  case "$FIRST_CMD" in
    $pat*) is_git=1; break ;;
  esac
done

# Route events via config-driven event chains
chain_outcome() {
  if [ -z "$EXIT_CODE" ]; then
    echo "unknown"
  elif [ "$EXIT_CODE" = "0" ]; then
    echo "success"
  else
    echo "failure"
  fi
}

if [ "$is_build" -eq 1 ]; then
  # Append to build history for left-pane display
  HISTORY_FILE="/tmp/muxcode-bus-${SESSION}/build-history.jsonl"
  BUILD_TS=$(date +%s)
  BUILD_OUTCOME=$(chain_outcome)

  # Capture build output from tool response
  BUILD_OUTPUT=""
  if command -v jq &>/dev/null; then
    BUILD_OUTPUT=$(printf '%s' "$EVENT" | jq -r '
      (.tool_response // .tool_result // {}) as $r |
      if ($r | type) == "string" then $r
      elif ($r.stdout // "") != "" then $r.stdout
      elif ($r.content // "") != "" then $r.content
      else ""
      end
    ' 2>/dev/null | sed 's/\x1b\[[0-9;]*[A-Za-z]//g' | tail -15)
  else
    BUILD_OUTPUT=$(printf '%s' "$EVENT" | python3 -c "
import json, sys, re
try:
    d = json.load(sys.stdin)
    r = d.get('tool_response', d.get('tool_result', {}))
    out = ''
    if isinstance(r, str): out = r
    elif isinstance(r, dict): out = r.get('stdout', r.get('content', ''))
    out = re.sub(r'\x1b\[[0-9;]*[A-Za-z]', '', out)
    lines = out.strip().split('\n')
    print('\n'.join(lines[-15:]))
except: pass
" 2>/dev/null)
  fi
  # Truncate to max 1000 chars
  if [ ${#BUILD_OUTPUT} -gt 1000 ]; then
    BUILD_OUTPUT="${BUILD_OUTPUT:0:997}..."
  fi
  # Replace HOME with ~ for readability
  BUILD_OUTPUT="${BUILD_OUTPUT//$HOME/\~}"

  # Capture short change summary from git
  BUILD_CHANGES=""
  if command -v git &>/dev/null && git rev-parse --is-inside-work-tree &>/dev/null; then
    CHANGED_FILES=$(git diff --name-only HEAD 2>/dev/null | head -10)
    if [ -z "$CHANGED_FILES" ]; then
      # Check staged files if no unstaged diff
      CHANGED_FILES=$(git diff --name-only --cached HEAD 2>/dev/null | head -10)
    fi
    if [ -n "$CHANGED_FILES" ]; then
      FILE_COUNT=$(printf '%s\n' "$CHANGED_FILES" | wc -l | tr -d ' ')
      # Use basenames for brevity, show up to 3
      SHORT_NAMES=$(printf '%s\n' "$CHANGED_FILES" | while read -r f; do basename "$f"; done | head -3 | paste -sd ', ' -)
      if [ "$FILE_COUNT" -gt 3 ]; then
        REMAINING=$(( FILE_COUNT - 3 ))
        BUILD_CHANGES="${FILE_COUNT} files: ${SHORT_NAMES}, +${REMAINING} more"
      else
        BUILD_CHANGES="${FILE_COUNT} files: ${SHORT_NAMES}"
      fi
    fi
  fi

  # Append + rotate under flock to prevent concurrent hook races
  # flock is optional — not available on stock macOS
  (
    command -v flock &>/dev/null && flock -n 9
    if command -v jq &>/dev/null; then
      jq -nc --arg ts "$BUILD_TS" --arg cmd "$COMMAND" --arg desc "${DESCRIPTION:-}" --arg ec "${EXIT_CODE:-0}" --arg outcome "$BUILD_OUTCOME" --arg changes "$BUILD_CHANGES" --arg output "$BUILD_OUTPUT" \
        '{ts:($ts|tonumber),command:$cmd,description:$desc,exit_code:$ec,outcome:$outcome,changes:$changes,output:$output}' \
        >> "$HISTORY_FILE" 2>/dev/null || true
    else
      python3 -c '
import json, sys
entry = {"ts": int(sys.argv[1]), "command": sys.argv[2], "description": sys.argv[3], "exit_code": sys.argv[4], "outcome": sys.argv[5], "changes": sys.argv[6], "output": sys.argv[7]}
print(json.dumps(entry, ensure_ascii=False))
' "$BUILD_TS" "$COMMAND" "${DESCRIPTION:-}" "${EXIT_CODE:-0}" "$BUILD_OUTCOME" "$BUILD_CHANGES" "$BUILD_OUTPUT" \
        >> "$HISTORY_FILE" 2>/dev/null || true
    fi

    # Rotate history: keep last 100 entries
    MAX_HISTORY=100
    LINE_COUNT=$(wc -l < "$HISTORY_FILE" 2>/dev/null || echo 0)
    if [ "$LINE_COUNT" -gt "$MAX_HISTORY" ]; then
      tail -n "$MAX_HISTORY" "$HISTORY_FILE" > "${HISTORY_FILE}.tmp" 2>/dev/null \
        && mv "${HISTORY_FILE}.tmp" "$HISTORY_FILE" 2>/dev/null || true
    fi
  ) 9>"${HISTORY_FILE}.lock"

  muxcode-agent-bus chain build "$(chain_outcome)" \
    --exit-code "${EXIT_CODE:-}" --command "$COMMAND" 2>/dev/null || true
fi

if [ "$is_test" -eq 1 ]; then
  # Append to test history for left-pane display
  TEST_HISTORY_FILE="/tmp/muxcode-bus-${SESSION}/test-history.jsonl"
  TEST_TS=$(date +%s)
  TEST_OUTCOME=$(chain_outcome)

  # Capture test output from tool response
  TEST_OUTPUT=""
  if command -v jq &>/dev/null; then
    TEST_OUTPUT=$(printf '%s' "$EVENT" | jq -r '
      (.tool_response // .tool_result // {}) as $r |
      if ($r | type) == "string" then $r
      elif ($r.stdout // "") != "" then $r.stdout
      elif ($r.content // "") != "" then $r.content
      else ""
      end
    ' 2>/dev/null | sed 's/\x1b\[[0-9;]*[A-Za-z]//g' | tail -15)
  else
    TEST_OUTPUT=$(printf '%s' "$EVENT" | python3 -c "
import json, sys, re
try:
    d = json.load(sys.stdin)
    r = d.get('tool_response', d.get('tool_result', {}))
    out = ''
    if isinstance(r, str): out = r
    elif isinstance(r, dict): out = r.get('stdout', r.get('content', ''))
    out = re.sub(r'\x1b\[[0-9;]*[A-Za-z]', '', out)
    lines = out.strip().split('\n')
    print('\n'.join(lines[-15:]))
except: pass
" 2>/dev/null)
  fi
  # Truncate to max 1000 chars
  if [ ${#TEST_OUTPUT} -gt 1000 ]; then
    TEST_OUTPUT="${TEST_OUTPUT:0:997}..."
  fi
  # Replace HOME with ~ for readability
  TEST_OUTPUT="${TEST_OUTPUT//$HOME/\~}"

  # Append + rotate under flock to prevent concurrent hook races
  (
    command -v flock &>/dev/null && flock -n 9
    if command -v jq &>/dev/null; then
      jq -nc --arg ts "$TEST_TS" --arg cmd "$COMMAND" --arg desc "${DESCRIPTION:-}" --arg ec "${EXIT_CODE:-0}" --arg outcome "$TEST_OUTCOME" --arg output "$TEST_OUTPUT" \
        '{ts:($ts|tonumber),command:$cmd,description:$desc,exit_code:$ec,outcome:$outcome,output:$output}' \
        >> "$TEST_HISTORY_FILE" 2>/dev/null || true
    else
      python3 -c '
import json, sys
entry = {"ts": int(sys.argv[1]), "command": sys.argv[2], "description": sys.argv[3], "exit_code": sys.argv[4], "outcome": sys.argv[5], "output": sys.argv[6]}
print(json.dumps(entry, ensure_ascii=False))
' "$TEST_TS" "$COMMAND" "${DESCRIPTION:-}" "${EXIT_CODE:-0}" "$TEST_OUTCOME" "$TEST_OUTPUT" \
        >> "$TEST_HISTORY_FILE" 2>/dev/null || true
    fi

    # Rotate history: keep last 100 entries
    MAX_HISTORY=100
    LINE_COUNT=$(wc -l < "$TEST_HISTORY_FILE" 2>/dev/null || echo 0)
    if [ "$LINE_COUNT" -gt "$MAX_HISTORY" ]; then
      tail -n "$MAX_HISTORY" "$TEST_HISTORY_FILE" > "${TEST_HISTORY_FILE}.tmp" 2>/dev/null \
        && mv "${TEST_HISTORY_FILE}.tmp" "$TEST_HISTORY_FILE" 2>/dev/null || true
    fi
  ) 9>"${TEST_HISTORY_FILE}.lock"

  muxcode-agent-bus chain test "$(chain_outcome)" \
    --exit-code "${EXIT_CODE:-}" --command "$COMMAND" 2>/dev/null || true
fi

if [ "$is_git" -eq 1 ]; then
  # Append to commit history for left-pane display
  GIT_HISTORY_FILE="/tmp/muxcode-bus-${SESSION}/commit-history.jsonl"
  GIT_TS=$(date +%s)
  GIT_OUTCOME=$(chain_outcome)

  # Capture git output from tool response
  GIT_OUTPUT=""
  if command -v jq &>/dev/null; then
    GIT_OUTPUT=$(printf '%s' "$EVENT" | jq -r '
      (.tool_response // .tool_result // {}) as $r |
      if ($r | type) == "string" then $r
      elif ($r.stdout // "") != "" then $r.stdout
      elif ($r.content // "") != "" then $r.content
      else ""
      end
    ' 2>/dev/null | sed 's/\x1b\[[0-9;]*[A-Za-z]//g' | tail -15)
  else
    GIT_OUTPUT=$(printf '%s' "$EVENT" | python3 -c "
import json, sys, re
try:
    d = json.load(sys.stdin)
    r = d.get('tool_response', d.get('tool_result', {}))
    out = ''
    if isinstance(r, str): out = r
    elif isinstance(r, dict): out = r.get('stdout', r.get('content', ''))
    out = re.sub(r'\x1b\[[0-9;]*[A-Za-z]', '', out)
    lines = out.strip().split('\n')
    print('\n'.join(lines[-15:]))
except: pass
" 2>/dev/null)
  fi
  # Truncate to max 1000 chars
  if [ ${#GIT_OUTPUT} -gt 1000 ]; then
    GIT_OUTPUT="${GIT_OUTPUT:0:997}..."
  fi
  # Replace HOME with ~ for readability
  GIT_OUTPUT="${GIT_OUTPUT//$HOME/\~}"

  # Extract short commit info (hash + message) from output when available
  GIT_SUMMARY=""
  if [[ "$FIRST_CMD" == git*commit* ]]; then
    # Try to extract "[branch hash] message" pattern from git commit output
    GIT_SUMMARY=$(printf '%s' "$GIT_OUTPUT" | grep -oE '\[[^ ]+ [a-f0-9]+\] .+' | head -1 2>/dev/null || true)
    if [ -z "$GIT_SUMMARY" ]; then
      # Fallback: extract short hash from first line mentioning a hash
      GIT_SUMMARY=$(printf '%s' "$GIT_OUTPUT" | grep -oE '[a-f0-9]{7,}' | head -1 2>/dev/null || true)
    fi
  elif [[ "$FIRST_CMD" == git*push* ]]; then
    # Extract remote and branch from push output
    GIT_SUMMARY=$(printf '%s' "$GIT_OUTPUT" | grep -oE '[a-z]+/[^ ]+\.\.[a-f0-9]+' | head -1 2>/dev/null || true)
    if [ -z "$GIT_SUMMARY" ]; then
      GIT_SUMMARY="push"
    fi
  elif [[ "$FIRST_CMD" == gh*pr*create* ]]; then
    # Extract PR URL from gh pr create output
    GIT_SUMMARY=$(printf '%s' "$GIT_OUTPUT" | grep -oE 'https://github\.com/[^ ]+/pull/[0-9]+' | head -1 2>/dev/null || true)
    if [ -z "$GIT_SUMMARY" ]; then
      GIT_SUMMARY="pr create"
    fi
  elif [[ "$FIRST_CMD" == gh*pr*merge* ]]; then
    GIT_SUMMARY="pr merge"
  elif [[ "$FIRST_CMD" == gh*pr*close* ]]; then
    GIT_SUMMARY="pr close"
  elif [[ "$FIRST_CMD" == gh*release*create* ]]; then
    GIT_SUMMARY="release create"
  fi

  # Append + rotate under flock to prevent concurrent hook races
  (
    command -v flock &>/dev/null && flock -n 9
    if command -v jq &>/dev/null; then
      jq -nc --arg ts "$GIT_TS" --arg cmd "$COMMAND" --arg desc "${DESCRIPTION:-}" --arg ec "${EXIT_CODE:-0}" --arg outcome "$GIT_OUTCOME" --arg summary "$GIT_SUMMARY" --arg output "$GIT_OUTPUT" \
        '{ts:($ts|tonumber),command:$cmd,description:$desc,exit_code:$ec,outcome:$outcome,summary:$summary,output:$output}' \
        >> "$GIT_HISTORY_FILE" 2>/dev/null || true
    else
      python3 -c '
import json, sys
entry = {"ts": int(sys.argv[1]), "command": sys.argv[2], "description": sys.argv[3], "exit_code": sys.argv[4], "outcome": sys.argv[5], "summary": sys.argv[6], "output": sys.argv[7]}
print(json.dumps(entry, ensure_ascii=False))
' "$GIT_TS" "$COMMAND" "${DESCRIPTION:-}" "${EXIT_CODE:-0}" "$GIT_OUTCOME" "$GIT_SUMMARY" "$GIT_OUTPUT" \
        >> "$GIT_HISTORY_FILE" 2>/dev/null || true
    fi

    # Rotate history: keep last 100 entries
    MAX_HISTORY=100
    LINE_COUNT=$(wc -l < "$GIT_HISTORY_FILE" 2>/dev/null || echo 0)
    if [ "$LINE_COUNT" -gt "$MAX_HISTORY" ]; then
      tail -n "$MAX_HISTORY" "$GIT_HISTORY_FILE" > "${GIT_HISTORY_FILE}.tmp" 2>/dev/null \
        && mv "${GIT_HISTORY_FILE}.tmp" "$GIT_HISTORY_FILE" 2>/dev/null || true
    fi
  ) 9>"${GIT_HISTORY_FILE}.lock"

  # No chain trigger — git commands don't start a build->test->review chain
fi

if [ "$is_deploy" -eq 1 ]; then
  # Append to deploy history for left-pane display
  DEPLOY_HISTORY_FILE="/tmp/muxcode-bus-${SESSION}/deploy-history.jsonl"
  DEPLOY_TS=$(date +%s)
  DEPLOY_OUTCOME=$(chain_outcome)

  # Capture deploy output from tool response
  DEPLOY_OUTPUT=""
  if command -v jq &>/dev/null; then
    DEPLOY_OUTPUT=$(printf '%s' "$EVENT" | jq -r '
      (.tool_response // .tool_result // {}) as $r |
      if ($r | type) == "string" then $r
      elif ($r.stdout // "") != "" then $r.stdout
      elif ($r.content // "") != "" then $r.content
      else ""
      end
    ' 2>/dev/null | sed 's/\x1b\[[0-9;]*[A-Za-z]//g' | tail -15)
  else
    DEPLOY_OUTPUT=$(printf '%s' "$EVENT" | python3 -c "
import json, sys, re
try:
    d = json.load(sys.stdin)
    r = d.get('tool_response', d.get('tool_result', {}))
    out = ''
    if isinstance(r, str): out = r
    elif isinstance(r, dict): out = r.get('stdout', r.get('content', ''))
    out = re.sub(r'\x1b\[[0-9;]*[A-Za-z]', '', out)
    lines = out.strip().split('\n')
    print('\n'.join(lines[-15:]))
except: pass
" 2>/dev/null)
  fi
  # Truncate to max 1000 chars
  if [ ${#DEPLOY_OUTPUT} -gt 1000 ]; then
    DEPLOY_OUTPUT="${DEPLOY_OUTPUT:0:997}..."
  fi
  # Replace HOME with ~ for readability
  DEPLOY_OUTPUT="${DEPLOY_OUTPUT//$HOME/\~}"

  # Append + rotate under flock to prevent concurrent hook races
  (
    command -v flock &>/dev/null && flock -n 9
    if command -v jq &>/dev/null; then
      jq -nc --arg ts "$DEPLOY_TS" --arg cmd "$COMMAND" --arg desc "${DESCRIPTION:-}" --arg ec "${EXIT_CODE:-0}" --arg outcome "$DEPLOY_OUTCOME" --arg output "$DEPLOY_OUTPUT" \
        '{ts:($ts|tonumber),command:$cmd,description:$desc,exit_code:$ec,outcome:$outcome,output:$output}' \
        >> "$DEPLOY_HISTORY_FILE" 2>/dev/null || true
    else
      python3 -c '
import json, sys
entry = {"ts": int(sys.argv[1]), "command": sys.argv[2], "description": sys.argv[3], "exit_code": sys.argv[4], "outcome": sys.argv[5], "output": sys.argv[6]}
print(json.dumps(entry, ensure_ascii=False))
' "$DEPLOY_TS" "$COMMAND" "${DESCRIPTION:-}" "${EXIT_CODE:-0}" "$DEPLOY_OUTCOME" "$DEPLOY_OUTPUT" \
        >> "$DEPLOY_HISTORY_FILE" 2>/dev/null || true
    fi

    # Rotate history: keep last 100 entries
    MAX_HISTORY=100
    LINE_COUNT=$(wc -l < "$DEPLOY_HISTORY_FILE" 2>/dev/null || echo 0)
    if [ "$LINE_COUNT" -gt "$MAX_HISTORY" ]; then
      tail -n "$MAX_HISTORY" "$DEPLOY_HISTORY_FILE" > "${DEPLOY_HISTORY_FILE}.tmp" 2>/dev/null \
        && mv "${DEPLOY_HISTORY_FILE}.tmp" "$DEPLOY_HISTORY_FILE" 2>/dev/null || true
    fi
  ) 9>"${DEPLOY_HISTORY_FILE}.lock"

  # Only trigger verify chain for actual deployments (not diff/plan)
  if [ "$is_deploy_apply" -eq 1 ]; then
    muxcode-agent-bus chain deploy "$(chain_outcome)" \
      --exit-code "${EXIT_CODE:-}" --command "$COMMAND" 2>/dev/null || true
  fi
fi
