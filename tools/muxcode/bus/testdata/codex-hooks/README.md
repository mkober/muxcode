# Codex hook payload fixtures

Captured live from `codex-cli 0.153.4` on 2026-09-08 by `scripts/spike-codex-hooks.sh`
(MUX-159 Phase 1), home and repo paths rewritten to `/home/dev`. Each `*.json` is one
event exactly as the hook received it on stdin; `transcript.jsonl` holds the
`item_completed` rollout records for the same four tool calls, which is where a
shell command's real `exit_code` lives — the `PostToolUse` payload for `Bash` carries
only stdout.

| File | Event | What it pins |
|------|-------|--------------|
| `session-start.json` | SessionStart | common fields, `source` |
| `user-prompt-submit.json` | UserPromptSubmit | `prompt` field |
| `user-prompt-submit-wake.json` | UserPromptSubmit | the fixed wake sentence |
| `pre-tool-use-bash.json` | PreToolUse | `tool_name: Bash`, `tool_input.command` string, `tool_use_id` |
| `post-tool-use-bash-exit3.json` | PostToolUse | bare-string `tool_response`, **no exit code** for `exit 3` |
| `post-tool-use-bash-ok.json` | PostToolUse | bare-string `tool_response` for a passing command |
| `pre-tool-use-apply-patch.json` | PreToolUse | `tool_name: apply_patch`, patch text in `tool_input.command`, absolute path |
| `post-tool-use-apply-patch-add.json` | PostToolUse | exec-style `Exit code: 0` response |
| `post-tool-use-apply-patch-delete.json` | PostToolUse | `*** Delete File:` patch |
| `stop.json` | Stop | `stop_hook_active: false`, `last_assistant_message` |
| `stop-continuation.json` | Stop | `stop_hook_active: true` after a `decision: block` |
| `session-end.json` | SessionEnd | `reason` |
| `transcript.jsonl` | rollout | `item_completed` with `exit_code` 3 / 0 and `FileChange` status |
