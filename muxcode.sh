#!/bin/bash
# muxcode.sh - Launch a tmux editor session with per-window AI agents
#
# Usage:
#   muxcode                         # Interactive project picker
#   muxcode <project-path>          # Use specified project directory
#   muxcode <path> <name>           # Use specified path and session name
#
# The edit window gets a vertical split: editor (left) + agent (right).
# Split-left windows get: tool (left) + agent (right).
# Other agent windows split: terminal (left) + agent (right).
# The status window runs the agent dashboard TUI.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# --- Load configuration ---
load_config() {
  local config_file=""
  if [ -n "${MUXCODE_CONFIG:-}" ] && [ -f "$MUXCODE_CONFIG" ]; then
    config_file="$MUXCODE_CONFIG"
  elif [ -f "./.muxcode/config" ]; then
    config_file="./.muxcode/config"
  elif [ -f "$HOME/.config/muxcode/config" ]; then
    config_file="$HOME/.config/muxcode/config"
  fi
  [ -n "$config_file" ] && source "$config_file"
}

load_config

# Configuration with defaults
PROJECTS_DIR="${MUXCODE_PROJECTS_DIR:-$HOME}"
SCAN_DEPTH="${MUXCODE_SCAN_DEPTH:-3}"
WINDOWS="${MUXCODE_WINDOWS:-edit build test review deploy run commit analyze docs research status}"
ROLE_MAP="${MUXCODE_ROLE_MAP:-run=runner commit=git analyze=analyst docs=docs research=research}"
SPLIT_LEFT="${MUXCODE_SPLIT_LEFT:-edit analyze commit}"
SHELL_INIT="${MUXCODE_SHELL_INIT:-}"
EDITOR="${MUXCODE_EDITOR:-nvim}"
AGENT_CLI="${MUXCODE_AGENT_CLI:-claude}"

# Ensure local bins are in PATH (display-popup skips shell profile)
case "$(uname -s)" in
  Darwin) export PATH="/opt/homebrew/bin:/opt/homebrew/sbin:$HOME/.local/bin:$PATH" ;;
  *)      export PATH="$HOME/.local/bin:$PATH" ;;
esac

# --- Dependency checks ---
if ! command -v tmux &>/dev/null; then
  echo "Error: tmux is required" >&2
  exit 1
fi

# --- Project selection ---
if [ $# -ge 1 ]; then
  PROJECT_DIR="$(cd "$1" && pwd)"
else
  # Build project list from configured directories
  PROJECTS=()
  IFS=',' read -ra PROJ_DIRS <<< "$PROJECTS_DIR"
  for pdir in "${PROJ_DIRS[@]}"; do
    pdir="$(echo "$pdir" | xargs)" # trim whitespace
    [ -d "$pdir" ] || continue
    while IFS= read -r dir; do
      PROJECTS+=("$dir")
    done < <(find "$pdir" -maxdepth "$SCAN_DEPTH" -name .git -type d 2>/dev/null \
      | sed 's|/\.git$||' | sort)
  done

  if [ ${#PROJECTS[@]} -eq 0 ]; then
    echo "No git projects found in $PROJECTS_DIR" >&2
    exit 1
  fi

  # Inside a tmux popup: use inline fzf (popups can't nest)
  # Inside tmux (no popup): use fzf --tmux for a centered popup
  # Outside tmux: use inline fzf with limited height
  if [ -n "${TMUX_POPUP:-}" ]; then
    FZF_TMUX_OPTS="--layout=reverse"
  elif [ -n "${TMUX:-}" ]; then
    FZF_TMUX_OPTS="--tmux center,60%,50%"
  else
    FZF_TMUX_OPTS="--height=40%"
  fi
  PROJECT_DIR=$(printf '%s\n' "${PROJECTS[@]}" \
    | fzf $FZF_TMUX_OPTS \
        --prompt="  Project: " --reverse --border \
        --header="Select a project · ESC to cancel" \
        --bind="esc:abort" \
    || true)

  if [ -z "${PROJECT_DIR:-}" ]; then
    exit 0
  fi
fi

# --- Session name ---
PROJECT_NAME="$(basename "$PROJECT_DIR")"
if [ $# -ge 2 ]; then
  SESSION="$2"
else
  SESSION="$PROJECT_NAME"
fi

echo ""
echo "  Project:  $PROJECT_DIR"
echo "  Session:  $SESSION"
echo ""

# --- Parse window list ---
read -ra WIN_ARRAY <<< "$WINDOWS"

# --- Map window name to agent role ---
agent_role() {
  local win="$1"
  for mapping in $ROLE_MAP; do
    local key="${mapping%%=*}"
    local val="${mapping#*=}"
    if [ "$win" = "$key" ]; then
      echo "$val"
      return
    fi
  done
  echo "$win"
}

# --- Check if window is split-left ---
is_split_left() {
  for w in $SPLIT_LEFT; do
    [ "$w" = "$1" ] && return 0
  done
  return 1
}

# --- Resolve agent launcher ---
find_agent_launcher() {
  if command -v muxcode-agent.sh &>/dev/null; then
    echo "muxcode-agent.sh"
  elif [ -f "$SCRIPT_DIR/scripts/muxcode-agent.sh" ]; then
    echo "$SCRIPT_DIR/scripts/muxcode-agent.sh"
  elif [ -f "$SCRIPT_DIR/muxcode-agent.sh" ]; then
    echo "$SCRIPT_DIR/muxcode-agent.sh"
  else
    echo "muxcode-agent.sh"
  fi
}

AGENT_LAUNCHER="$(find_agent_launcher)"

# --- Kill existing session if any ---
tmux kill-session -t "$SESSION" 2>/dev/null || true

# --- Clear stale session-created hook from any running tmux server ---
tmux set-hook -gu session-created 2>/dev/null || true

# --- Initialize agent bus ---
export BUS_SESSION="$SESSION"
(cd "$PROJECT_DIR" && muxcode-agent-bus init)

# --- Helper: send shell init to a pane ---
send_init() {
  local target="$1"
  if [ -n "$SHELL_INIT" ]; then
    tmux send-keys -t "$target" "$SHELL_INIT" Enter
  fi
}

# --- Create session with first window ---
FIRST_WIN="${WIN_ARRAY[0]}"
tmux new-session -d -s "$SESSION" -n "$FIRST_WIN" -c "$PROJECT_DIR"
tmux set-environment -t "$SESSION" BUS_SESSION "$SESSION"
tmux set-environment -t "$SESSION" MUXCODE 1

if [ "$FIRST_WIN" = "edit" ]; then
  send_init "$SESSION:$FIRST_WIN"
  tmux send-keys -t "$SESSION:$FIRST_WIN" "MUXCODE=1 $EDITOR" Enter
  tmux split-window -h -t "$SESSION:$FIRST_WIN" -c "$PROJECT_DIR"
  send_init "$SESSION:$FIRST_WIN.1"
  tmux send-keys -t "$SESSION:$FIRST_WIN.1" "$AGENT_LAUNCHER edit" Enter
  tmux select-pane -t "$SESSION:$FIRST_WIN.0"
fi

# --- Create remaining windows ---
for WIN in "${WIN_ARRAY[@]:1}"; do
  ROLE="$(agent_role "$WIN")"

  if [ "$WIN" = "status" ]; then
    # Status window: dashboard TUI
    tmux new-window -t "$SESSION" -n "$WIN" -c "$PROJECT_DIR"
    send_init "$SESSION:$WIN"
    tmux send-keys -t "$SESSION:$WIN" "muxcode-agent-bus dashboard" Enter
  elif [ "$WIN" = "edit" ]; then
    # Edit window (if not first): editor + agent
    tmux new-window -t "$SESSION" -n "$WIN" -c "$PROJECT_DIR"
    send_init "$SESSION:$WIN"
    tmux send-keys -t "$SESSION:$WIN" "MUXCODE=1 $EDITOR" Enter
    tmux split-window -h -t "$SESSION:$WIN" -c "$PROJECT_DIR"
    send_init "$SESSION:$WIN.1"
    tmux send-keys -t "$SESSION:$WIN.1" "$AGENT_LAUNCHER edit" Enter
    tmux select-pane -t "$SESSION:$WIN.0"
  elif [ "$WIN" = "commit" ] && is_split_left "$WIN"; then
    # Commit window: git status poller (left) + agent (right)
    tmux new-window -t "$SESSION" -n "$WIN" -c "$PROJECT_DIR"
    send_init "$SESSION:$WIN"
    if command -v muxcode-git-status.sh &>/dev/null; then
      tmux send-keys -t "$SESSION:$WIN" "muxcode-git-status.sh" Enter
    fi
    tmux split-window -h -t "$SESSION:$WIN" -c "$PROJECT_DIR"
    send_init "$SESSION:$WIN.1"
    tmux send-keys -t "$SESSION:$WIN.1" "$AGENT_LAUNCHER $ROLE" Enter
    tmux select-pane -t "$SESSION:$WIN.1"
  elif [ "$WIN" = "analyze" ] && is_split_left "$WIN"; then
    # Analyze window: bus watcher (left) + analyst agent (right)
    tmux new-window -t "$SESSION" -n "$WIN" -c "$PROJECT_DIR"
    send_init "$SESSION:$WIN"
    tmux send-keys -t "$SESSION:$WIN" "muxcode-agent-bus watch $SESSION" Enter
    tmux split-window -h -t "$SESSION:$WIN" -c "$PROJECT_DIR" -l 75%
    send_init "$SESSION:$WIN.1"
    tmux send-keys -t "$SESSION:$WIN.1" "$AGENT_LAUNCHER $ROLE" Enter
    tmux select-pane -t "$SESSION:$WIN.1"
  elif is_split_left "$WIN"; then
    # Custom split-left window: terminal (left) + agent (right)
    tmux new-window -t "$SESSION" -n "$WIN" -c "$PROJECT_DIR"
    send_init "$SESSION:$WIN"
    tmux split-window -h -t "$SESSION:$WIN" -c "$PROJECT_DIR"
    send_init "$SESSION:$WIN.1"
    tmux send-keys -t "$SESSION:$WIN.1" "$AGENT_LAUNCHER $ROLE" Enter
    tmux select-pane -t "$SESSION:$WIN.1"
  else
    # Standard agent window: terminal (left) + agent (right)
    tmux new-window -t "$SESSION" -n "$WIN" -c "$PROJECT_DIR"
    send_init "$SESSION:$WIN"
    tmux split-window -h -t "$SESSION:$WIN" -c "$PROJECT_DIR"
    send_init "$SESSION:$WIN.1"
    tmux send-keys -t "$SESSION:$WIN.1" "$AGENT_LAUNCHER $ROLE" Enter
    tmux select-pane -t "$SESSION:$WIN.1"
  fi
done

# --- Start on edit window, cursor in agent pane ---
tmux select-window -t "$SESSION:edit" 2>/dev/null || tmux select-window -t "$SESSION:${WIN_ARRAY[0]}"
tmux select-pane -t "$SESSION:edit.1" 2>/dev/null || true

echo "  Session '$SESSION' ready"
echo ""

# --- Register cleanup hook for bus directory ---
tmux set-hook -t "$SESSION" session-closed \
  "run-shell 'muxcode-agent-bus cleanup $SESSION'"

# Force all windows to resize to the client's terminal dimensions after attaching.
(
  sleep 1
  tmux list-windows -t "$SESSION" -F '#I' 2>/dev/null | while read -r idx; do
    tmux resize-window -t "$SESSION:$idx" -A 2>/dev/null
  done
) &

# Switch to new session (works inside tmux) or attach from outside
if [ -n "${TMUX:-}" ]; then
  tmux switch-client -t "$SESSION"
else
  tmux attach-session -t "$SESSION"
fi
