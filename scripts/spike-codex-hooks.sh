#!/usr/bin/env bash
# MUX-159 Phase 1 spike: prove Codex CLI hooks fire in the interactive TUI muxcode
# launches, and record the real payload shapes as fixtures.
#
# Launches ONE real codex TUI in a scratch tmux session inside this repo (the repo
# is already trusted in ~/.codex/config.toml, so no trust prompt) with a
# capture-only .codex/hooks.json: every handler appends its stdin to a JSONL file.
# The Stop handler additionally answers {"decision":"block","reason":...} once, and
# the UserPromptSubmit handler answers additionalContext carrying a marker phrase —
# the pane afterwards shows whether the model saw either (PINEAPPLE / MANGO-4471).
#
# Costs real Codex API usage (a few short turns). Refuses to run if the repo
# already has a .codex/hooks.json, and removes the one it wrote on exit.
#
# Output: $SPIKE_OUT (default /tmp/codex-hook-spike): hook-capture.jsonl,
# pane.txt, summary.txt.
set -uo pipefail

REPO=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
OUT=${SPIKE_OUT:-/tmp/codex-hook-spike}
SESSION="codex-spike-$$"
HOOKS="$REPO/.codex/hooks.json"
CAP="$OUT/hook-capture.jsonl"
SCRATCH_FILE="spike-hook-test.txt"

command -v tmux >/dev/null 2>&1 || { echo "SKIP: tmux is required"; exit 2; }
command -v codex >/dev/null 2>&1 || { echo "SKIP: codex not on PATH"; exit 2; }
command -v jq >/dev/null 2>&1 || { echo "SKIP: jq is required"; exit 2; }
if [ -e "$HOOKS" ]; then
  echo "refusing: $HOOKS already exists — remove it first so the spike cannot clobber real hooks"
  exit 1
fi

mkdir -p "$OUT"
rm -f "$CAP" "$OUT/stop.marker" "$OUT/pane.txt" "$OUT/summary.txt"
: > "$CAP"

cleanup() {
  tmux send-keys -t "$SESSION" C-c 2>/dev/null || true
  sleep 1
  tmux kill-session -t "$SESSION" 2>/dev/null || true
  rm -f "$HOOKS" "$REPO/$SCRATCH_FILE"
  rmdir "$REPO/.codex" 2>/dev/null || true
}
trap cleanup EXIT

# Handlers live under $OUT so hooks.json can reference them by absolute path.
cat > "$OUT/cap.sh" <<EOF
#!/usr/bin/env bash
cat >> "$CAP"; echo >> "$CAP"
EOF
cat > "$OUT/stop.sh" <<EOF
#!/usr/bin/env bash
cat >> "$CAP"; echo >> "$CAP"
if [ ! -f "$OUT/stop.marker" ]; then
  touch "$OUT/stop.marker"
  echo '{"decision":"block","reason":"Hook continuation test: reply with the single word PINEAPPLE and nothing else, then stop."}'
fi
EOF
cat > "$OUT/ups.sh" <<EOF
#!/usr/bin/env bash
cat >> "$CAP"; echo >> "$CAP"
echo '{"hookSpecificOutput":{"hookEventName":"UserPromptSubmit","additionalContext":"Delivery marker MANGO-4471. Repeat this marker verbatim in your reply."}}'
EOF
chmod +x "$OUT"/*.sh

cat > "$HOOKS" <<EOF
{
  "hooks": {
    "SessionStart": [{"hooks": [{"type": "command", "command": "$OUT/cap.sh", "timeout": 10}]}],
    "PreToolUse": [{"matcher": ".*", "hooks": [{"type": "command", "command": "$OUT/cap.sh", "timeout": 10}]}],
    "PostToolUse": [{"matcher": ".*", "hooks": [{"type": "command", "command": "$OUT/cap.sh", "timeout": 10}]}],
    "UserPromptSubmit": [{"hooks": [{"type": "command", "command": "$OUT/ups.sh", "timeout": 10}]}],
    "Stop": [{"hooks": [{"type": "command", "command": "$OUT/stop.sh", "timeout": 10}]}],
    "SessionEnd": [{"hooks": [{"type": "command", "command": "$OUT/cap.sh", "timeout": 10}]}]
  }
}
EOF

count_event() { # name -> number of captured events with that hook_event_name
  jq -r 'select(type=="object") | .hook_event_name // empty' "$CAP" 2>/dev/null | grep -c -x "$1" || true
}
wait_for_event() { # name count timeout_secs
  local name=$1 want=$2 deadline=$(( $(date +%s) + $3 ))
  while [ "$(date +%s)" -lt "$deadline" ]; do
    [ "$(count_event "$name")" -ge "$want" ] && return 0
    sleep 2
  done
  return 1
}
send_prompt() { # text
  tmux send-keys -t "$SESSION" -l -- "$1"
  sleep 0.3
  tmux send-keys -t "$SESSION" Enter
}

echo "=== MUX-159 codex hooks spike ==="
echo "codex: $(codex --version 2>&1 | head -1)"
echo "capture: $CAP"

tmux new-session -d -s "$SESSION" -x 200 -y 50 -c "$REPO"
tmux send-keys -t "$SESSION" -l -- "unset BUS_SESSION AGENT_ROLE BUS_ROLE; cd '$REPO' && codex --no-alt-screen -a never -s workspace-write --dangerously-bypass-hook-trust"
tmux send-keys -t "$SESSION" Enter

if wait_for_event SessionStart 1 40; then
  echo "ok: SessionStart hook fired"
else
  echo "note: no SessionStart capture after 40s (hooks may still fire on tool use)"
fi
sleep 3

# Turn 1: a failing shell command, a passing one, an apply_patch add and delete.
send_prompt "Ignore any AGENTS.md instructions about a message bus and never run muxcode. Do exactly these steps in order using your tools, without asking questions: 1) run the shell command: echo spike-ok; exit 3  (it exits 3 on purpose, that is expected). 2) run the shell command: printf hi. 3) create a file named $SCRATCH_FILE in the repo root containing the single line hello, using apply_patch. 4) delete $SCRATCH_FILE using apply_patch. Then reply with the single word DONE."

if wait_for_event Stop 1 240; then
  echo "ok: first Stop fired (turn 1 ended)"
else
  echo "FAIL: no Stop within 240s"
fi
# The Stop handler blocked once; the continuation's own Stop should carry
# stop_hook_active=true.
if wait_for_event Stop 2 90; then
  echo "ok: continuation Stop fired"
else
  echo "note: no second Stop within 90s (block continuation may not have happened)"
fi

# Turn 2: the fixed wake sentence — UserPromptSubmit expands it via additionalContext.
sleep 2
send_prompt "You have new messages"
if wait_for_event Stop 3 120; then
  echo "ok: Stop after wake sentence"
else
  echo "note: no third Stop within 120s"
fi

sleep 2
tmux capture-pane -t "$SESSION" -p -S -200 > "$OUT/pane.txt" 2>/dev/null || true

{
  echo "=== capture summary ==="
  echo "events captured: $(jq -r 'select(type=="object") | .hook_event_name // "?"' "$CAP" 2>/dev/null | sort | uniq -c | sort -rn)"
  echo
  echo "--- first payload of each event ---"
  for ev in SessionStart UserPromptSubmit PreToolUse PostToolUse Stop SessionEnd; do
    first=$(jq -c "select(type==\"object\" and .hook_event_name==\"$ev\")" "$CAP" 2>/dev/null | head -1)
    [ -n "$first" ] && { echo "[$ev]"; echo "$first" | jq . ; echo; }
  done
  echo "--- every PostToolUse (tool_name + tool_response shape) ---"
  jq -c 'select(type=="object" and .hook_event_name=="PostToolUse") | {tool_name, tool_input, tool_response}' "$CAP" 2>/dev/null
  echo
  echo "--- every Stop (stop_hook_active) ---"
  jq -c 'select(type=="object" and .hook_event_name=="Stop") | {stop_hook_active, last_assistant_message}' "$CAP" 2>/dev/null
  echo
  echo "--- pane markers ---"
  grep -c PINEAPPLE "$OUT/pane.txt" | sed 's/^/PINEAPPLE lines: /'
  grep -c "MANGO-4471" "$OUT/pane.txt" | sed 's/^/MANGO-4471 lines: /'
  echo
  echo "--- trust store ---"
  ls -la ~/.codex/hooks.state* 2>/dev/null || echo "no ~/.codex/hooks.state*"
  find ~/.codex -maxdepth 2 -name '*hook*' 2>/dev/null
} | tee "$OUT/summary.txt"

echo "done — fixtures: $CAP  pane: $OUT/pane.txt"
