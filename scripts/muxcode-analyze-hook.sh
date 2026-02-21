#!/bin/bash
# muxcode-analyze-hook.sh — PostToolUse hook for Write/Edit/NotebookEdit
#
# Signals the bus watcher that a file was edited. Routes file-change events
# to appropriate agents based on file type. Cleans up nvim diff preview
# after an accepted edit.
#
# Receives tool event JSON on stdin.

# Must be inside a tmux session
SESSION=$(tmux display-message -p '#S' 2>/dev/null) || exit 0

# Read the full event JSON once
EVENT_JSON=$(cat)

# Extract file path
FILE_PATH=$(echo "$EVENT_JSON" | jq -r '.tool_input.file_path // .tool_input.notebook_path // empty' 2>/dev/null)
[ -z "$FILE_PATH" ] && exit 0

# Skip agent state files
[[ "$FILE_PATH" == */.claude/* ]] && exit 0
[[ "$FILE_PATH" == */.muxcode/* ]] && exit 0

# Append to the trigger file for the watcher
echo "$(date +%s) $FILE_PATH" >> "/tmp/muxcode-analyze-${SESSION}.trigger"

# Clean up nvim diff preview, reload file, and jump to the change
WINDOW_NAME="$(tmux display-message -p '#W' 2>/dev/null)"
TEMP_FILE="/tmp/muxcode-preview-${SESSION}.tmp"
if [ "$WINDOW_NAME" = "edit" ] && [ -f "$TEMP_FILE" ]; then
  LINE=1
  NEEDLE=$(echo "$EVENT_JSON" | jq -r '.tool_input.new_string // empty' 2>/dev/null \
    | sed '/^[[:space:]]*$/d' | head -1)
  if [ -n "$NEEDLE" ] && [ -f "$FILE_PATH" ]; then
    MATCH=$(grep -nF -- "$NEEDLE" "$FILE_PATH" 2>/dev/null | head -1 | cut -d: -f1)
    [ -n "$MATCH" ] && LINE="$MATCH"
  fi
  ESCAPED_PATH="${FILE_PATH// /\\ }"
  tmux send-keys -t "$SESSION:edit.0" Escape Escape
  sleep 0.1
  tmux send-keys -t "$SESSION:edit.0" ":diffoff! | only | e! +$LINE $ESCAPED_PATH" Enter
  rm -f "$TEMP_FILE"
fi

# Route file-change events through the agent bus
BUS_DIR="/tmp/muxcode-bus-${SESSION}"
if [ -d "$BUS_DIR" ]; then
  export BUS_SESSION="$SESSION"
  export AGENT_ROLE="$(tmux display-message -p '#W' 2>/dev/null || echo 'edit')"

  # Configurable routing rules
  ROUTE_RULES="${MUXCODE_ROUTE_RULES:-test|spec=test cdk|stack|construct|terraform|pulumi=deploy .ts|.js|.py|.go|.rs=build}"

  routed=0
  IFS=' ' read -ra RULES <<< "$ROUTE_RULES"
  for rule in "${RULES[@]}"; do
    local_pattern="${rule%%=*}"
    local_target="${rule#*=}"
    IFS='|' read -ra PATS <<< "$local_pattern"
    for pat in "${PATS[@]}"; do
      if [[ "$FILE_PATH" == *"$pat"* ]]; then
        muxcode-agent-bus send "$local_target" notify "File changed: $FILE_PATH" --type event --no-notify
        routed=1
        break 2
      fi
    done
  done
fi
