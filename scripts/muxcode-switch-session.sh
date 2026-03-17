#!/usr/bin/env bash
# muxcode-switch-session.sh — interactive session switcher popup
# Lists tmux sessions with metadata, uses fzf for selection.
# Falls back to a numbered menu if fzf is not available.

CURRENT_SESSION=$(tmux display-message -p '#S')

# Build session list with metadata
# Format: session_name | windows | created | (attached)
build_list() {
  tmux list-sessions -F '#{session_name}|#{session_windows}|#{session_created}|#{?session_attached,attached,}' 2>/dev/null | while IFS='|' read -r name windows created attached; do
    created_fmt=$(date -r "$created" '+%b %d %H:%M' 2>/dev/null || date -d "@$created" '+%b %d %H:%M' 2>/dev/null || echo "unknown")
    marker=""
    if [ "$name" = "$CURRENT_SESSION" ]; then
      marker=" ●"
    fi
    if [ -n "$attached" ] && [ "$name" != "$CURRENT_SESSION" ]; then
      marker=" ◆"
    fi
    printf "  %-20s  %2d windows  %s%s\n" "$name" "$windows" "$created_fmt" "$marker"
  done
}

# Extract session name from formatted line
extract_name() {
  echo "$1" | awk '{print $1}'
}

if command -v fzf >/dev/null 2>&1; then
  # fzf mode
  selection=$(build_list | fzf \
    --ansi \
    --no-multi \
    --cycle \
    --layout=reverse \
    --prompt="Switch to: " \
    --header="● current  ◆ attached elsewhere" \
    --header-first \
    --no-info \
    --pointer="▶" \
    --color="bg+:#44475a,fg+:#f8f8f2,hl:#50fa7b,hl+:#50fa7b,pointer:#ff79c6,prompt:#bd93f9,header:#6272a4")

  [ -z "$selection" ] && exit 0
  target=$(extract_name "$selection")
else
  # Fallback: numbered menu
  echo "● current  ◆ attached elsewhere"
  echo ""
  count=0
  build_list | while IFS= read -r line; do
    count=$((count + 1))
    printf "  %d) %s\n" "$count" "$line"
  done
  count=$(build_list | wc -l | tr -d ' ')
  echo ""
  printf "Switch to [1-%d]: " "$count"
  read -r choice
  [ -z "$choice" ] && exit 0
  if ! echo "$choice" | grep -qE '^[0-9]+$' || [ "$choice" -lt 1 ] || [ "$choice" -gt "$count" ]; then
    echo "Invalid choice"
    exit 1
  fi
  target=$(build_list | sed -n "${choice}p" | awk '{print $1}')
fi

# Don't switch to current session
if [ "$target" = "$CURRENT_SESSION" ]; then
  exit 0
fi

# Switch client to selected session
tmux switch-client -t "$target"
