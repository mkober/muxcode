#!/bin/bash
# muxcode-inbox-poll.sh — PostToolUse hook for Bash (edit window only)
#
# After the edit agent sends a message via the bus, polls the inbox
# every 2 seconds until a response arrives. Outputs consumed messages
# so Claude sees them naturally as part of the tool result — no text
# injection via tmux send-keys.
#
# Synchronous hook (not async) — blocks until response arrives or timeout.
# Configurable via MUXCODE_INBOX_POLL_TIMEOUT (default: 120 seconds).
#
# Receives tool event JSON on stdin.

# Must be inside a tmux session
SESSION=$(tmux display-message -p '#S' 2>/dev/null) || exit 0
export BUS_SESSION="$SESSION"

# Only run on the edit window
if [ -n "$TMUX_PANE" ]; then
  WINDOW_NAME=$(tmux display-message -t "$TMUX_PANE" -p '#W' 2>/dev/null) || exit 0
else
  WINDOW_NAME=$(tmux display-message -p '#W' 2>/dev/null) || exit 0
fi
[ "$WINDOW_NAME" = "edit" ] || exit 0
export AGENT_ROLE="edit"

# Read event JSON from stdin
EVENT="$(cat)"
[ -z "$EVENT" ] && exit 0

# Extract the command
COMMAND=$(printf '%s' "$EVENT" | jq -r '.tool_input.command // empty' 2>/dev/null)
[ -z "$COMMAND" ] && exit 0

# Only trigger for bus send commands
case "$COMMAND" in
  muxcode-agent-bus\ send\ *|agent-bus\ send\ *) ;;
  *) exit 0 ;;
esac

# Don't poll for sends to self
case "$COMMAND" in
  *\ send\ edit\ *) exit 0 ;;
esac

# Don't poll for fire-and-forget messages (events, notifications)
case "$COMMAND" in
  *--type\ event*|*--type\ notification*|*--no-notify*) exit 0 ;;
esac

# Poll inbox every 2 seconds until messages arrive or timeout
POLL_INTERVAL=2
TIMEOUT="${MUXCODE_INBOX_POLL_TIMEOUT:-120}"
INBOX_PATH="/tmp/muxcode-bus-${SESSION}/inbox/edit.jsonl"
ELAPSED=0

while [ "$ELAPSED" -lt "$TIMEOUT" ]; do
  sleep "$POLL_INTERVAL"
  ELAPSED=$((ELAPSED + POLL_INTERVAL))

  # Check if inbox has content
  if [ -f "$INBOX_PATH" ] && [ -s "$INBOX_PATH" ]; then
    # Consume and output messages
    muxcode-agent-bus inbox
    exit 0
  fi
done

# Timeout — no response received
echo "No response received within ${TIMEOUT}s. Check manually: muxcode-agent-bus inbox --peek"
exit 0
