#!/bin/bash
# muxcode-diff-cleanup.sh — Close stale diff preview in nvim
#
# Runs as a PreToolUse hook on Read/Bash/Grep/Glob. If a diff preview
# is still open from a rejected edit, this cleans it up immediately.

SESSION=$(tmux display-message -p '#S' 2>/dev/null) || exit 0
WINDOW_NAME=$(tmux display-message -t "${TMUX_PANE:-}" -p '#W' 2>/dev/null) || exit 0
[ "$WINDOW_NAME" = "edit" ] || exit 0

TEMP_FILE="/tmp/muxcode-preview-${SESSION}.tmp"
[ -f "$TEMP_FILE" ] || exit 0

PANE="$SESSION:edit.0"

# Dismiss any pending "Press ENTER" prompt and ensure normal mode
tmux send-keys -t "$PANE" Enter 2>/dev/null
sleep 0.05
tmux send-keys -t "$PANE" Escape Escape
sleep 0.05
tmux send-keys -t "$PANE" ":sil! exe 'b!'.get(g:,'_mux_buf',bufnr()) | sil! diffoff! | sil! only | sil! set number" Enter
rm -f "$TEMP_FILE"
