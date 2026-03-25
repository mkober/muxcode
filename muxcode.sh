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
  if [ -n "$config_file" ]; then
    set -a  # auto-export all vars defined during source
    source "$config_file"
    set +a
  fi
}

load_config

# Configuration with defaults
PROJECTS_DIR="${MUXCODE_PROJECTS_DIR:-$HOME}"
SCAN_DEPTH="${MUXCODE_SCAN_DEPTH:-3}"
WINDOWS="${MUXCODE_WINDOWS:-edit api build test review deploy run watch commit analyze}"
ROLE_MAP="${MUXCODE_ROLE_MAP:-run=runner commit=git analyze=analyst}"
SPLIT_LEFT="${MUXCODE_SPLIT_LEFT:-edit api build test review deploy run analyze commit watch}"
SHELL_INIT="${MUXCODE_SHELL_INIT:-}"
EDITOR="${MUXCODE_EDITOR:-nvim}"
NVIM_APPNAME="${MUXCODE_NVIM_APPNAME:-muxcode/nvim}"
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

# --- Lifecycle logging helper ---
# Writes persistent lifecycle events to ~/.config/muxcode/logs/{session}.log
# via the bus binary. Failures are silently ignored (|| true).
lifecycle_log() {
  local level="$1" source="$2" event="$3" detail="${4:-}"
  local args=("$SESSION" "$level" "$source" "$event")
  [ -n "$detail" ] && args+=(--detail "$detail")
  muxcode lifecycle log "${args[@]}" 2>/dev/null || true
}

lifecycle_log "info" "launcher" "session-start" "Project: $PROJECT_DIR"

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

# --- Agent launcher (Go binary handles config, model, tools, prompt resolution) ---
AGENT_LAUNCHER="muxcode agent launch"

# --- Kill existing session if any ---
tmux kill-session -t "$SESSION" 2>/dev/null || true

# --- Clear stale session-created hook from any running tmux server ---
tmux set-hook -gu session-created 2>/dev/null || true

# --- Clean up stale preview temp files from previous sessions ---
rm -f "/tmp/muxcode-preview-${SESSION}.tmp"

# --- Initialize agent bus ---
export BUS_SESSION="$SESSION"
(cd "$PROJECT_DIR" && muxcode init)
lifecycle_log "info" "launcher" "bus-init"

# --- Start bus watcher in background (loop detection, compaction alerts) ---
# Kill any stale watcher processes from previous sessions with the same name.
# Watchers are background processes detached from tmux — tmux kill-session
# does not stop them, so they accumulate and cause duplicate notifications.
# nohup + disown: subsessions use switch-client (non-blocking), so the parent
# shell exits and the terminal closes — background processes receive SIGHUP.
# disown alone is insufficient; nohup prevents SIGHUP from killing the process.
# Anchor patterns with $ to avoid "watch SESSION" matching "watch --monitor SESSION"
pkill -f "muxcode watch --monitor ${SESSION}$" 2>/dev/null && \
  lifecycle_log "info" "launcher" "stale-kill" "Killed stale monitor for $SESSION"
pkill -f "muxcode watch ${SESSION}$" 2>/dev/null && \
  lifecycle_log "info" "launcher" "stale-kill" "Killed stale watcher for $SESSION"
sleep 0.1  # let old processes exit before starting the new one
nohup muxcode watch "$SESSION" &>/dev/null &
WATCHER_PID=$!
disown
lifecycle_log "info" "launcher" "watcher-start" "PID: $WATCHER_PID"
nohup muxcode watch --monitor "$SESSION" &>/dev/null &
MONITOR_PID=$!
disown
lifecycle_log "info" "launcher" "monitor-start" "PID: $MONITOR_PID"

# --- Ensure Ollama is running if any role uses local LLM ---
ensure_ollama() {
  local url="${MUXCODE_OLLAMA_URL:-http://localhost:11434}"
  # Check if any role is configured for local LLM
  local needs_ollama=false
  for var in $(compgen -v | grep -E '^MUXCODE_.*_CLI$'); do
    if [ "${!var}" = "local" ]; then
      needs_ollama=true
      break
    fi
  done
  $needs_ollama || return 0

  # Already running?
  if curl -s --max-time 2 "${url}/api/tags" >/dev/null 2>&1; then
    return 0
  fi

  # Start Ollama in background
  if command -v ollama &>/dev/null; then
    echo "  Starting Ollama..."
    ollama serve &>/dev/null &
    # Wait up to 10s for it to become ready
    local i=0
    while [ $i -lt 20 ]; do
      if curl -s --max-time 1 "${url}/api/tags" >/dev/null 2>&1; then
        echo "  Ollama ready"
        return 0
      fi
      sleep 0.5
      i=$((i + 1))
    done
    echo "  Warning: Ollama did not become ready in 10s (agents will fall back to Claude Code)" >&2
  else
    echo "  Warning: Ollama not installed but MUXCODE_*_CLI=local is set (agents will fall back to Claude Code)" >&2
  fi
}

ensure_ollama

# --- Helper: send shell init to a pane ---
send_init() {
  local target="$1"
  if [ -n "$SHELL_INIT" ]; then
    tmux send-keys -t "$target" "$SHELL_INIT" Enter
  fi
}

# --- Create session with first window ---
FIRST_WIN="${WIN_ARRAY[0]}"

# When launching from inside tmux, capture client dimensions so the detached
# session starts at the correct size.  Without this, `new-session -d` uses a
# small default geometry and Neovim's start screen renders off-center.
NEW_SESSION_SIZE=""
if [ -n "${TMUX:-}" ]; then
  _client_w="$(tmux display-message -p '#{client_width}')"
  _client_h="$(tmux display-message -p '#{client_height}')"
  if [ -n "$_client_w" ] && [ -n "$_client_h" ]; then
    NEW_SESSION_SIZE="-x $_client_w -y $_client_h"
  fi
fi

tmux new-session -d -s "$SESSION" -n "$FIRST_WIN" -c "$PROJECT_DIR" $NEW_SESSION_SIZE
tmux set-environment -t "$SESSION" BUS_SESSION "$SESSION"
tmux set-environment -t "$SESSION" MUXCODE 1
lifecycle_log "info" "launcher" "session-create" "Windows: ${WINDOWS}"

if [ "$FIRST_WIN" = "edit" ]; then
  send_init "$SESSION:$FIRST_WIN"
  tmux send-keys -t "$SESSION:$FIRST_WIN" "MUXCODE=1 NVIM_APPNAME=$NVIM_APPNAME $EDITOR" Enter
  tmux split-window -h -t "$SESSION:$FIRST_WIN" -c "$PROJECT_DIR"
  send_init "$SESSION:$FIRST_WIN.1"
  tmux send-keys -t "$SESSION:$FIRST_WIN.1" "$AGENT_LAUNCHER edit" Enter
  tmux select-pane -t "$SESSION:$FIRST_WIN.0"
fi

# --- Create remaining windows ---
for WIN in "${WIN_ARRAY[@]:1}"; do
  ROLE="$(agent_role "$WIN")"

  if [ "$WIN" = "edit" ]; then
    # Edit window (if not first): editor + agent
    tmux new-window -t "$SESSION" -n "$WIN" -c "$PROJECT_DIR"
    send_init "$SESSION:$WIN"
    tmux send-keys -t "$SESSION:$WIN" "MUXCODE=1 NVIM_APPNAME=$NVIM_APPNAME $EDITOR" Enter
    tmux split-window -h -t "$SESSION:$WIN" -c "$PROJECT_DIR"
    send_init "$SESSION:$WIN.1"
    tmux send-keys -t "$SESSION:$WIN.1" "$AGENT_LAUNCHER edit" Enter
    tmux select-pane -t "$SESSION:$WIN.0"
  elif is_split_left "$WIN"; then
    # Split-left window: console status display (left) + agent (right)
    # Uses muxcode console <role> for all known roles (build, test,
    # review, deploy, run, watch, commit, analyze, api). Falls back to an
    # empty terminal for custom split-left windows.
    tmux new-window -t "$SESSION" -n "$WIN" -c "$PROJECT_DIR"
    send_init "$SESSION:$WIN"
    if command -v muxcode &>/dev/null; then
      # Check if window has a console view (all standard windows do)
      # Use $WIN not $ROLE — console configs are keyed by window name
      case "$WIN" in
        build|test|review|deploy|run|watch|commit|analyze|api)
          tmux send-keys -t "$SESSION:$WIN" "muxcode console $WIN" Enter
          ;;
      esac
    fi
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

# --- Add menu hint and hamburger icon to status bar ---
# Prepend a tooltip-style hint before the existing status-right content
_sr="$(tmux show-options -gv status-right 2>/dev/null)"
# Remove all powerline arrows (thin U+E0B3 and filled U+E0B2)
_sr="$(echo "$_sr" | sed $'s/\xee\x82\xb3//g; s/\xee\x82\xb2//g')"
# Strip the green arrow color block and the green music segment (unused)
_sr="$(echo "$_sr" | sed 's/#\[fg=#00ff00, bg=#282a36\] //')"
_sr="$(echo "$_sr" | sed 's|#\[fg=#282a36, bg=#00ff00\] #(~/dotfiles/tmux_scripts/music.sh) ||')"
# Restyle date/time: tab-color bg for date, comment-color bg for time, with powerline arrows
_sr="$(echo "$_sr" | sed $'s/#\\[fg=#6272a4, bg=#282a36\\]/#[fg=#44475a, bg=#282a36]\xee\x82\xb2#[fg=#f8f8f2, bg=#44475a]/')"
_sr="$(echo "$_sr" | sed $'s/#\\[fg=#50fa7b\\]/#[fg=#6272a4, bg=#44475a]\xee\x82\xb2#[fg=#f8f8f2, bg=#6272a4]/')"
# Add padding around date and time text
_sr="$(echo "$_sr" | sed "s/%b/ %b/; s/'%y/'%y /; s/%H:%M/ %H:%M:%S /")"
tmux set-option -t "$SESSION" status-right "${_sr}"
# Capitalize window labels in the status bar (internal names stay lowercase for bus routing).
# Set explicit Dracula-style formats with powerline arrows and awk capitalize.
# Uses awk (not ${var^}) because macOS ships bash 3.2 which lacks that syntax.
# Must use global (-g) because Dracula's #{T:window-status-format} only resolves globals.
_cap="awk '{print toupper(substr(\$0,1,1)) substr(\$0,2)}'"
tmux set-option -g window-status-format \
  $'#[fg=#282a36,bg=#44475a,noitalics]\xee\x82\xb0#[fg=#f8f8f2,bg=#44475a] F#I#[fg=#f8f8f2, bg=#44475a] #(echo #W | '"${_cap}"$') #[fg=#44475a, bg=#282a36]\xee\x82\xb0'
tmux set-option -g window-status-current-format \
  $'#[fg=#282a36, bg=#00ff00]\xee\x82\xb0#[fg=#44475a, bg=#00ff00] F#I*#[fg=#44475a, bg=#00ff00, bold] #(echo #W | '"${_cap}"$') #[fg=#00ff00, bg=#282a36]\xee\x82\xb0'
# Replace the Dracula session icon (❐) with hamburger (☰) in status-left
_sl="$(tmux show-options -gv status-left 2>/dev/null)"
_sl_with_icon="$(echo "$_sl" | sed 's/❐/☰/')"
tmux set-option -t "$SESSION" status-left "$_sl_with_icon"

# --- Register cleanup hook for bus directory ---
tmux set-hook -t "$SESSION" session-closed \
  "run-shell 'muxcode cleanup $SESSION'"

# Force all windows to resize to the client's terminal dimensions after attaching.
# nohup/trap SIGHUP so the subshell survives terminal close on subsession switch.
(
  trap '' HUP
  sleep 1
  tmux list-windows -t "$SESSION" -F '#I' 2>/dev/null | while read -r idx; do
    tmux resize-window -t "$SESSION:$idx" -A 2>/dev/null
  done
) &>/dev/null &
disown

# --- Auto-accept startup prompts & wake edit/analyze agents ---
# Claude Code may show two prompts on launch:
#   1. "Yes, I trust this folder" — workspace trust (new workspaces)
#   2. "Bypass Permissions mode" — dangerous-skip-permissions warning (non-edit agents)
# Poll agent panes and dismiss each prompt as it appears.
# Startup messages (edit, analyze) are pre-populated in the inbox by
# muxcode-agent.sh. Once those agents reach idle, this loop directly
# injects "You have new messages" + Enter to ensure they process it —
# does not rely solely on the watcher's checkStartupNotifications().
(
  trap '' HUP
  accepted=""
  woken=""
  for attempt in $(seq 1 30); do
    all_done=true
    for WIN in "${WIN_ARRAY[@]}"; do
      # Skip already-accepted panes
      echo "$accepted" | grep -qw "$WIN" && continue
      pane="$SESSION:$WIN.1"
      content="$(tmux capture-pane -t "$pane" -p 2>/dev/null)" || continue
      if echo "$content" | grep -q "trust this folder"; then
        # Trust prompt — default selection is already correct, just confirm
        tmux send-keys -t "$pane" Enter 2>/dev/null
        lifecycle_log "info" "auto-accept" "trust-prompt" "$WIN"
        all_done=false  # bypass prompt may follow
      elif echo "$content" | grep -q "Bypass Permissions"; then
        # Bypass permissions prompt — move to "Yes, I accept" and confirm
        tmux send-keys -t "$pane" Down 2>/dev/null
        sleep 0.2
        tmux send-keys -t "$pane" Enter 2>/dev/null
        lifecycle_log "info" "auto-accept" "bypass-prompt" "$WIN"
        accepted="$accepted $WIN"
      elif echo "$content" | grep -q '❯'; then
        # Agent at Claude Code idle prompt — past all startup prompts.
        lifecycle_log "info" "auto-accept" "agent-ready" "$WIN"
        accepted="$accepted $WIN"

        # Wake edit and analyze agents that have pre-populated startup
        # messages. Check if "You have new messages" is already in the
        # pane (from watcher) — if so just send Enter, otherwise inject
        # the full text + Enter. Only attempt once per agent.
        if [ "$WIN" = "edit" ] || [ "$WIN" = "analyze" ]; then
          if ! echo "$woken" | grep -qw "$WIN"; then
            woken="$woken $WIN"
            # Brief stabilization delay — let TUI finish rendering
            sleep 1
            if echo "$content" | grep -q "You have new messages"; then
              # Text already present — just submit it
              tmux send-keys -t "$pane" Enter 2>/dev/null
              lifecycle_log "info" "auto-accept" "startup-wake-enter" "$WIN"
            else
              # Inject the wake-up text, wait for it to render, then Enter
              tmux send-keys -t "$pane" "You have new messages" 2>/dev/null
              for _poll in $(seq 1 10); do
                sleep 0.1
                _cap="$(tmux capture-pane -t "$pane" -p -S -3 2>/dev/null)" || break
                echo "$_cap" | grep -q "You have new messages" && break
              done
              tmux send-keys -t "$pane" Enter 2>/dev/null
              lifecycle_log "info" "auto-accept" "startup-wake-full" "$WIN"
            fi
          fi
        fi
      else
        all_done=false
      fi
    done
    $all_done && break
    sleep 2
  done
  lifecycle_log "info" "auto-accept" "complete" "All agents accepted"
) &>/dev/null &
disown

lifecycle_log "info" "launcher" "session-ready"

# Switch to new session (works inside tmux) or attach from outside
if [ -n "${TMUX:-}" ]; then
  tmux switch-client -t "$SESSION"
else
  tmux attach-session -t "$SESSION"
fi
