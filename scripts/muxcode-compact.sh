#!/usr/bin/env bash
# muxcode-compact.sh — Wait for agent idle, then inject /compact via tmux send-keys.
# Called as a background process after saving session context to memory.
# Usage: muxcode-compact.sh [role]
#   role defaults to AGENT_ROLE or "edit"

role="${1:-${AGENT_ROLE:-edit}}"
session="${BUS_SESSION:-$(tmux display-message -p '#S' 2>/dev/null)}"

if [ -z "$session" ]; then
  exit 0
fi

# Resolve pane target — agent runs in pane 1 (right pane)
target="${session}:${role}.1"

# Wait for the agent to reach idle (❯ prompt), max 30 seconds
for i in $(seq 1 30); do
  if tmux capture-pane -t "$target" -p -S -8 2>/dev/null | grep -q '❯'; then
    break
  fi
  sleep 1
done

# Verify idle before sending
if ! tmux capture-pane -t "$target" -p -S -8 2>/dev/null | grep -q '❯'; then
  exit 0
fi

# Clear any residual input
tmux send-keys -t "$target" "Escape" 2>/dev/null
sleep 0.1
tmux send-keys -t "$target" "C-u" 2>/dev/null
sleep 0.1

# Inject /compact + Enter (separate calls per tmux send-keys convention)
tmux send-keys -t "$target" "/compact" 2>/dev/null
sleep 0.2
tmux send-keys -t "$target" "Enter" 2>/dev/null
