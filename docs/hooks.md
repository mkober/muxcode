# Hooks

## Overview

Muxcoder uses Claude Code's hook system to integrate the AI agent with tmux and neovim. Hooks are shell scripts that run before or after tool execution, receiving the tool event as JSON on stdin.

All hooks are **async** — they do not block the AI agent from continuing.

## Hook Configuration

Hooks are configured in `.claude/settings.json` in your project:

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Write|Edit|NotebookEdit",
        "hooks": [{"type": "command", "command": "muxcoder-preview-hook.sh", "async": true}]
      },
      {
        "matcher": "Read|Bash|Grep|Glob",
        "hooks": [{"type": "command", "command": "muxcoder-diff-cleanup.sh", "async": true}]
      }
    ],
    "PostToolUse": [
      {
        "matcher": "Write|Edit|NotebookEdit",
        "hooks": [{"type": "command", "command": "muxcoder-analyze-hook.sh", "async": true}]
      },
      {
        "matcher": "Bash",
        "hooks": [{"type": "command", "command": "muxcoder-bash-hook.sh", "async": true}]
      }
    ]
  }
}
```

You can copy a pre-configured template:
```bash
cp ~/.config/muxcoder/settings.json .claude/settings.json
```

## Hook Descriptions

### muxcoder-preview-hook.sh

**Phase:** PreToolUse
**Trigger:** Write, Edit, NotebookEdit
**Window:** edit only

Opens the target file in nvim and shows a diff preview of the proposed change before the user accepts or rejects it.

**What it does:**
1. Opens the file at the line about to be changed
2. Creates a temp file with the proposed version
3. Opens a horizontal diff split (original below, proposed above)
4. Sets syntax highlighting to match the file type

**Customization:**
- `MUXCODER_PREVIEW_SKIP` — space-separated substrings of file paths to skip (default: `/.claude/settings.json /.claude/CLAUDE.md /.muxcoder/`)

### muxcoder-diff-cleanup.sh

**Phase:** PreToolUse
**Trigger:** Read, Bash, Grep, Glob
**Window:** edit only

Lightweight cleanup hook. If a diff preview is still open from a previously rejected edit, this closes it before the next tool runs.

### muxcoder-analyze-hook.sh

**Phase:** PostToolUse
**Trigger:** Write, Edit, NotebookEdit

Signals that a file was edited. Performs three tasks:

1. **Trigger file**: Appends the edited file path to the trigger file for the bus watcher
2. **Event routing**: Sends file-change events to appropriate agents based on file type
3. **Diff cleanup**: In the edit window, closes the diff preview and reloads the file at the changed line

**File routing rules** (configurable via `MUXCODER_ROUTE_RULES`):
- Test/spec files -> test agent
- Infrastructure files (cdk, terraform, pulumi, stack, construct) -> deploy agent
- Source files (.ts, .js, .py, .go, .rs) -> build agent

### muxcoder-bash-hook.sh

**Phase:** PostToolUse
**Trigger:** Bash

Detects build and test commands and drives the build-test-review chain:

```
Build success → trigger test agent
Test success  → trigger review agent
Any failure   → notify edit agent
```

Also sends events to the analyst for analysis.

**Customization:**
- `MUXCODER_BUILD_PATTERNS` — pipe-separated patterns for build command detection
- `MUXCODER_TEST_PATTERNS` — pipe-separated patterns for test command detection

**JSON parsing:** Uses `jq` by default with a `python3` fallback.

## Hook Event Format

Hooks receive JSON on stdin with this structure:

```json
{
  "tool_name": "Edit",
  "tool_input": {
    "file_path": "/path/to/file.ts",
    "old_string": "original code",
    "new_string": "modified code"
  },
  "tool_response": {
    "exit_code": 0,
    "stdout": "...",
    "stderr": ""
  }
}
```

PreToolUse hooks receive `tool_input` only (no response yet).
PostToolUse hooks receive both `tool_input` and `tool_response`.

## Build-Test-Review Chain

The chain is **hook-driven**, ensuring deterministic behavior:

1. Build agent runs `./build.sh` (or configured build command)
2. `muxcoder-bash-hook.sh` detects build command completed
3. If exit code 0: hook sends `request:test` to test agent
4. Test agent runs tests
5. Hook detects test command completed
6. If exit code 0: hook sends `request:review` to review agent
7. Review agent reviews `git diff`, replies with findings

On failure at any step, the hook notifies edit directly with the error details.

**Key property:** Agents are NOT responsible for chaining. They only run their command and reply. The hook guarantees the chain fires deterministically based on exit codes.

## Creating Custom Hooks

You can add project-specific hooks alongside the muxcoder hooks in `.claude/settings.json`. Hooks are additive — multiple hooks can match the same tool.

Example: add a linting hook that runs after file edits:

```json
{
  "matcher": "Write|Edit",
  "hooks": [
    {"type": "command", "command": "my-lint-hook.sh", "async": true}
  ]
}
```
