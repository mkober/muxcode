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
PANE="$SESSION:edit.0"

# Dismiss any pending "Press ENTER" prompt and ensure normal mode
tmux send-keys -t "$PANE" Enter 2>/dev/null
sleep 0.05
tmux send-keys -t "$PANE" Escape Escape
sleep 0.05

# Clean up any stale diff from a previous rejected edit
if [ -f "$TEMP_FILE" ]; then
  tmux send-keys -t "$PANE" ":sil! exe 'b!'.get(g:,'_mux_buf',bufnr()) | sil! diffoff! | sil! only | sil! set number" Enter
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

# Open file at the changed line (foldlevel=99 keeps all folds open, nohlsearch clears stale matches)
# Each command needs sil! — the modifier only applies to the immediately following command, not the full | chain
tmux send-keys -t "$PANE" ":sil! exe 'e! +$LINE $ESCAPED_PATH' | sil! setlocal foldlevel=99 | sil! set number | sil! nohlsearch | sil! let @/=''" Enter

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
    sleep 0.2
    # Dismiss any prompt from file-open autocmds before sending diff command
    tmux send-keys -t "$PANE" Enter 2>/dev/null
    sleep 0.05
    tmux send-keys -t "$PANE" Escape Escape
    sleep 0.05
    tmux send-keys -t "$PANE" ":sil! let g:_mux_buf=bufnr() | sil! let g:_pft=&ft | sil! diffthis | sil! new | sil! setlocal buftype=nofile bufhidden=wipe number | sil! let &l:ft=g:_pft | sil! read $TEMP_FILE | sil! 1delete _ | sil! diffthis | sil! setlocal foldlevel=99 | sil! norm! zR | sil! noautocmd wincmd p | sil! setlocal foldlevel=99 | sil! norm! zR" Enter
    # Jump to changed line after diff is fully rendered — separate command so scrollbind is active
    sleep 0.15
    tmux send-keys -t "$PANE" ":sil! exe 'norm! ${LINE}Gzz'" Enter
  fi
fi
