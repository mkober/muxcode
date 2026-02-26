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

tmux send-keys -t "$SESSION:edit.0" Escape Escape
sleep 0.1
tmux send-keys -t "$SESSION:edit.0" ":exe 'sil! b!'.get(g:,'_mux_buf',bufnr()) | sil! diffoff! | sil! only" Enter
rm -f "$TEMP_FILE"
