#!/bin/bash
# muxcode-preview-hook.sh — PreToolUse hook for Write/Edit/NotebookEdit
#
# Opens the target file in nvim and shows a diff preview of the
# proposed change, so you can review full context before accepting.
# Only activates in the edit window (pane 0 = editor).

SESSION=$(tmux display-message -p '#S' 2>/dev/null) || exit 0
WINDOW_NAME=$(tmux display-message -t "${TMUX_PANE:-}" -p '#W' 2>/dev/null) || exit 0
[ "$WINDOW_NAME" = "edit" ] || exit 0

EVENT_JSON=$(cat)

FILE_PATH=$(echo "$EVENT_JSON" | jq -r '.tool_input.file_path // .tool_input.notebook_path // empty' 2>/dev/null)
[ -z "$FILE_PATH" ] && exit 0

# Configurable skip patterns
SKIP_PATTERNS="${MUXCODE_PREVIEW_SKIP:-/.claude/settings.json /.claude/CLAUDE.md /.muxcode/}"
for pat in $SKIP_PATTERNS; do
  [[ "$FILE_PATH" == *"$pat"* ]] && exit 0
done

TEMP_FILE="/tmp/muxcode-preview-${SESSION}.tmp"

# Clean up any stale diff from a previous rejected edit
if [ -f "$TEMP_FILE" ]; then
  tmux send-keys -t "$SESSION:edit.0" Escape Escape
  sleep 0.1
  tmux send-keys -t "$SESSION:edit.0" ":exe 'sil! b!'.get(g:,'_mux_buf',bufnr()) | diffoff! | only" Enter
  sleep 0.2
  rm -f "$TEMP_FILE"
fi

# Find the line of old_string (the code about to be changed)
LINE=1
OLD_STRING=$(echo "$EVENT_JSON" | jq -r '.tool_input.old_string // empty' 2>/dev/null)
NEEDLE=$(echo "$OLD_STRING" | sed '/^[[:space:]]*$/d' | head -1)
if [ -n "$NEEDLE" ] && [ -f "$FILE_PATH" ]; then
  MATCH=$(grep -nF -- "$NEEDLE" "$FILE_PATH" 2>/dev/null | head -1 | cut -d: -f1)
  [ -n "$MATCH" ] && LINE="$MATCH"
fi

# Escape spaces for nvim command-line
ESCAPED_PATH="${FILE_PATH// /\\ }"

# Open file at the changed line (foldlevel=99 keeps all folds open persistently)
tmux send-keys -t "$SESSION:edit.0" Escape Escape
sleep 0.1
tmux send-keys -t "$SESSION:edit.0" ":e! +$LINE $ESCAPED_PATH | setlocal foldlevel=99" Enter

# For Edit tool: create temp file with proposed change and open diff
if [ -n "$OLD_STRING" ] && [ -f "$FILE_PATH" ]; then
  echo "$EVENT_JSON" | python3 -c "
import json, sys
event = json.load(sys.stdin)
ti = event.get('tool_input', {})
old = ti.get('old_string', '')
new = ti.get('new_string', '')
fp = ti.get('file_path', '') or ti.get('notebook_path', '')
if old and fp:
    with open(fp) as f:
        content = f.read()
    with open(sys.argv[1], 'w') as f:
        f.write(content.replace(old, new, 1))
" "$TEMP_FILE" 2>/dev/null

  if [ -f "$TEMP_FILE" ]; then
    sleep 0.1
    tmux send-keys -t "$SESSION:edit.0" ":let g:_mux_buf=bufnr() | let g:_pft=&ft | diffthis | new | setlocal buftype=nofile bufhidden=wipe number | let &l:ft=g:_pft | silent read $TEMP_FILE | 1delete _ | diffthis | setlocal foldlevel=99 | norm! zR | noautocmd wincmd p | setlocal foldlevel=99 | norm! zR" Enter
  fi
fi
