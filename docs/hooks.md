# Hooks

## Overview

Muxcode uses Claude Code's hook system to integrate the AI agent with tmux and neovim. Hooks are shell scripts that run before or after tool execution, receiving the tool event as JSON on stdin.

All hooks are **async** — they do not block the AI agent from continuing.

## Hook Configuration

Hooks are configured in `.claude/settings.json` in your project:

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Write|Edit|NotebookEdit",
        "hooks": [{"type": "command", "command": "muxcode-preview-hook.sh", "async": true}]
      },
      {
        "matcher": "Read|Bash|Grep|Glob",
        "hooks": [{"type": "command", "command": "muxcode-diff-cleanup.sh", "async": true}]
      }
    ],
    "PostToolUse": [
      {
        "matcher": "Write|Edit|NotebookEdit",
        "hooks": [{"type": "command", "command": "muxcode-analyze-hook.sh", "async": true}]
      },
      {
        "matcher": "Bash",
        "hooks": [{"type": "command", "command": "muxcode-bash-hook.sh", "async": true}]
      }
    ]
  }
}
```

You can copy a pre-configured template:
```bash
cp ~/.config/muxcode/settings.json .claude/settings.json
```

## Hook Descriptions

### muxcode-preview-hook.sh

**Phase:** PreToolUse
**Trigger:** Write, Edit, NotebookEdit
**Window:** edit only (detected via `tmux display-message -p '#W'`; exits immediately if the current window is not `edit`)

Opens the target file in nvim and shows a diff preview of the proposed change before the user accepts or rejects it.

**What it does:**
1. Opens the file at the line about to be changed
2. Creates a temp file with the proposed version
3. Opens a horizontal diff split (original below, proposed above)
4. Sets syntax highlighting to match the file type

**Customization:**
- `MUXCODE_PREVIEW_SKIP` — space-separated substrings of file paths to skip (default: `/.claude/settings.json /.claude/CLAUDE.md /.muxcode/`)

### muxcode-diff-cleanup.sh

**Phase:** PreToolUse
**Trigger:** Read, Bash, Grep, Glob
**Window:** edit only

Lightweight cleanup hook. If a diff preview is still open from a previously rejected edit, this closes it before the next tool runs.

### muxcode-analyze-hook.sh

**Phase:** PostToolUse
**Trigger:** Write, Edit, NotebookEdit

Signals that a file was edited. Performs three tasks:

1. **Trigger file**: Appends the edited file path to the trigger file for the bus watcher
2. **Event routing**: Sends file-change events to appropriate agents based on file type
3. **Diff cleanup**: In the edit window, closes the diff preview and reloads the file at the changed line

**NotebookEdit:** For `NotebookEdit` tool events, `file_path` is extracted from `tool_input.notebook_path`. The diff preview opens the `.ipynb` file at the raw JSON level.

**File routing rules** (configurable via `MUXCODE_ROUTE_RULES`):
- Test/spec files -> test agent
- Infrastructure files (cdk, terraform, pulumi, stack, construct) -> deploy agent
- Source files (.ts, .js, .py, .go, .rs) -> build agent

**Matching mechanics:** Rules are evaluated in order (first match wins). Each rule's pattern is `|`-separated substrings matched case-sensitively against the full file path. Files matching no rule skip routing silently.

### muxcode-bash-hook.sh

**Phase:** PostToolUse
**Trigger:** Bash

Detects build, test, and deploy commands and drives event chains:

```
Build success        → trigger test agent
Test success         → trigger review agent
Deploy-apply success → trigger verify (self-loop to deploy agent)
Any failure          → notify edit agent
```

Deploy commands are split into two categories:
- **Deploy patterns** (`MUXCODE_DEPLOY_PATTERNS`): all deploy commands — logged to deploy history
- **Deploy-apply patterns** (`MUXCODE_DEPLOY_APPLY_PATTERNS`): mutation-only commands (deploy, destroy, apply) — trigger the verify chain

Preview commands (`cdk diff`, `terraform plan`, `pulumi preview`) match deploy patterns for history logging but do **not** trigger verification.

Also sends events to the analyst for analysis.

**Customization:**
- `MUXCODE_BUILD_PATTERNS` — pipe-separated patterns for build command detection
- `MUXCODE_TEST_PATTERNS` — pipe-separated patterns for test command detection
- `MUXCODE_DEPLOY_PATTERNS` — pipe-separated patterns for deploy command detection (all deploy commands)
- `MUXCODE_DEPLOY_APPLY_PATTERNS` — pipe-separated patterns for deploy-apply commands that trigger the verify chain

**JSON parsing:** Uses `jq` by default with a `python3` fallback. If neither `jq` nor `python3` is available, the `command` and `exit_code` fields will be empty and the hook exits silently — the build-test-review chain will not trigger. The preview hook uses `python3` specifically for generating proposed file content; without it, no split diff appears in nvim.

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
2. `muxcode-bash-hook.sh` detects build command completed
3. If exit code 0: hook sends `request:test` to test agent
4. Test agent runs tests
5. Hook detects test command completed
6. If exit code 0: hook sends `request:review` to review agent
7. Review agent reviews `git diff`, replies with findings

On failure at any step, the hook notifies edit directly with the error details.

**Key property:** Agents are NOT responsible for chaining. They only run their command and reply. The hook guarantees the chain fires deterministically based on exit codes.

## Deploy-Verify Chain

When a deploy-apply command succeeds, the hook triggers a verification self-loop:

1. Deploy agent runs `cdk deploy` (or `terraform apply`, `pulumi up`, etc.)
2. `muxcode-bash-hook.sh` detects deploy-apply command completed
3. If exit code 0: hook sends `request:verify` back to deploy agent
4. Deploy agent runs verification checks (AWS resource health, HTTP smoke tests, CloudWatch alarms/logs)
5. Deploy agent reports PASS/FAIL results to edit

Preview commands (`cdk diff`, `terraform plan`) are logged to deploy history but do **not** trigger the verify chain. See [Deploy verify plan](plan-deploy-verify.md) for full details.

## Creating Custom Hooks

You can add project-specific hooks alongside the muxcode hooks in `.claude/settings.json`. Hooks are additive — multiple hooks can match the same tool.

Example: add a linting hook that runs after file edits:

```json
{
  "matcher": "Write|Edit",
  "hooks": [
    {"type": "command", "command": "my-lint-hook.sh", "async": true}
  ]
}
```
