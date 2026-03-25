# Hooks

## Overview

Muxcode uses Claude Code's hook system to integrate the AI agent with tmux and neovim. Hooks run before or after tool execution, receiving the tool event as JSON on stdin. Most hooks are implemented as subcommands of `muxcode hook` (Go binary); two remain as shell scripts for tmux/vim timing-sensitive operations.

All hooks are **async** — they do not block the AI agent from continuing.

## Hook Configuration

Hooks are configured in `.claude/settings.json` in your project:

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [{"type": "command", "command": "muxcode hook guard"}]
      },
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
        "hooks": [{"type": "command", "command": "muxcode hook analyze", "async": true}]
      },
      {
        "matcher": "Bash",
        "hooks": [{"type": "command", "command": "muxcode hook bash", "async": true}]
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

### hook guard (edit guard)

**Command:** `muxcode hook guard`
**Phase:** PreToolUse
**Trigger:** Bash
**Mode:** sync (blocks tool execution)
**Window:** edit only

Blocks prohibited commands in the edit window (build, test, deploy, git commands) and returns delegation instructions. This is the only **sync** hook — it runs before the tool executes and can reject the command.

**What it blocks:**
- Build commands: `./build.sh`, `make`, `go build`, `pnpm build`, `cargo build`
- Test commands: `./test.sh`, `go test`, `jest`, `pytest`
- Deploy commands: `cdk`, `terraform`, `pulumi`
- Git commands: all `git` subcommands
- Log tailing: `tail -f`, `aws logs`, `kubectl logs`, `docker logs`

When a command is blocked, the hook returns a rejection with instructions to delegate via the message bus instead.

### muxcode-preview-hook.sh

**Phase:** PreToolUse
**Trigger:** Write, Edit, NotebookEdit
**Window:** edit only (detected via `tmux display-message -p '#W'`; exits immediately if the current window is not `edit`)

Opens the target file in nvim and shows a diff preview of the proposed change before the user accepts or rejects it.

**What it does:**
1. Dismisses any pending "Press ENTER" prompt and ensures normal mode
2. Cleans stale diff from a previously rejected edit (skips if temp file < 3s old — concurrent invocation guard)
3. Opens the file at the line about to be changed (folds open, search highlight cleared)
4. For Edit tool: generates a temp file with the proposed change via `python3` (required — no diff without it)
5. Opens a horizontal diff split with `scrollbind` (original below, proposed above), syntax matching the file type
6. Jumps to the changed line after a 150ms delay — sent as a separate `tmux send-keys` so scrollbind is fully active before the jump

**Implementation details:**
- Each nvim command in a `|` pipe chain needs its own `sil!` prefix — the modifier only suppresses the immediately following command, not the full chain. Without this, errors like E35 cause "Press ENTER" prompts that break subsequent commands.
- The jump-to-line uses `norm! {LINE}Gzz` (not `:N`) because `norm!` properly triggers scrollbind sync between both diff panes.
- Concurrent invocations (from global + project `.claude/settings.json` both firing the hook) are handled via temp file age detection — if the temp file is < 3 seconds old, the second invocation exits immediately.

**Customization:**
- `MUXCODE_PREVIEW_SKIP` — space-separated substrings of file paths to skip (default: `/.claude/settings.json /.claude/CLAUDE.md /.muxcode/`)

### muxcode-diff-cleanup.sh

**Phase:** PreToolUse
**Trigger:** Read, Bash, Grep, Glob
**Window:** edit only

Lightweight cleanup hook. If a diff preview is still open from a previously rejected edit, this closes it before the next tool runs.

### hook analyze (analyze hook)

**Command:** `muxcode hook analyze`
**Phase:** PostToolUse
**Trigger:** Write, Edit, NotebookEdit

Signals that a file was edited. Performs three tasks:

1. **Workflow transition**: Transitions the [workflow state machine](architecture.md#workflow-state-machine) to `editing` (clears outcomes if regressing from a later state)
2. **Trigger file**: Appends the edited file path to the trigger file for the bus watcher
3. **Event routing**: Sends file-change events to appropriate agents based on file type (uses `--no-notify` — no status bar flash for file-change events)
4. **Diff cleanup**: In the edit window, waits ~1s for the async preview hook to finish, then closes the diff preview and reloads the file at the changed line. The delay prevents the cleanup from racing ahead of the preview setup.

**NotebookEdit:** For `NotebookEdit` tool events, `file_path` is extracted from `tool_input.notebook_path`. The diff preview opens the `.ipynb` file at the raw JSON level.

**File routing rules** (configurable via `MUXCODE_ROUTE_RULES`):
- Test/spec files -> test agent
- Infrastructure files (cdk, terraform, pulumi, stack, construct) -> deploy agent
- Source files (.ts, .js, .py, .go, .rs) -> build agent

**Matching mechanics:** Rules are evaluated in order (first match wins). Each rule's pattern is `|`-separated substrings matched case-sensitively against the full file path. Files matching no rule skip routing silently.

### hook bash (bash hook)

**Command:** `muxcode hook bash`
**Phase:** PostToolUse
**Trigger:** Bash

Detects build, test, deploy, and git commands, drives event chains, transitions the [workflow state machine](architecture.md#workflow-state-machine), and logs history with error extraction:

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

Also sends events to the analyst for analysis (conditional on outcome — build/test only notify analyst on failure or unknown exit codes, deploy notifies on all outcomes).

After the primary chain action, the hook fires event subscriptions — matching `subscriptions.jsonl` entries by event+outcome pattern and sending fan-out messages via `SendNoCC()` (no auto-CC to edit). Use `muxcode subscribe add` to configure.

**Customization:**
- `MUXCODE_BUILD_PATTERNS` — pipe-separated patterns for build command detection
- `MUXCODE_TEST_PATTERNS` — pipe-separated patterns for test command detection
- `MUXCODE_DEPLOY_PATTERNS` — pipe-separated patterns for deploy command detection (all deploy commands)
- `MUXCODE_DEPLOY_APPLY_PATTERNS` — pipe-separated patterns for deploy-apply commands that trigger the verify chain

**Error extraction:** For failed build and test commands, the hook extracts error-relevant lines from tool output into an `errors` field in the history JSONL. The regex matches common error patterns: `error:`, `ERR!`, `failed`, `fatal`, `panic`, `FAIL:`, `not found`, `undefined`, `syntax error`, `permission denied`, etc. Test patterns additionally match `assert` and `expect`. The left-pane log views prefer the `errors` field over raw `output` when displaying failures, surfacing diagnostic information instead of noise like "Exit code: 1".

**JSON parsing:** Implemented in Go using `encoding/json` — no external dependencies (`jq`/`python3` not required). The preview hook (`muxcode-preview-hook.sh`) still uses `python3` for generating proposed file content; without it, no split diff appears in nvim.

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
2. `hook bash` detects build command completed
3. If exit code 0: hook sends `request:test` to test agent
4. Test agent runs tests
5. Hook detects test command completed
6. If exit code 0: hook sends `request:review` to review agent
7. Review agent reviews `git diff`, replies with findings

On failure at any step, the hook notifies edit directly with the error details.

Each chain step also transitions the [workflow state machine](architecture.md#workflow-state-machine): build success → `testing` (with `build_outcome: success`), test success → `reviewing` (with `test_outcome: success`), failures → `build-failed` or `test-failed`. File edits during or after the chain regress the state to `editing` and clear all accumulated outcomes.

**Key property:** Agents are NOT responsible for chaining. They only run their command and reply. The hook guarantees the chain fires deterministically based on exit codes.

## Deploy-Verify Chain

When a deploy-apply command succeeds, the hook triggers a verification self-loop:

1. Deploy agent runs `cdk deploy` (or `terraform apply`, `pulumi up`, etc.)
2. `hook bash` detects deploy-apply command completed
3. If exit code 0: hook sends `request:verify` back to deploy agent
4. Deploy agent runs verification checks (AWS resource health, HTTP smoke tests, CloudWatch alarms/logs)
5. Deploy agent reports PASS/FAIL results to edit

Preview commands (`cdk diff`, `terraform plan`) are logged to deploy history but do **not** trigger the verify chain. Deploy failures transition the workflow state to `deploy-failed`. See [Deploy verify plan](plan-deploy-verify.md) for full details.

## Testing

The diff preview integration can be tested with `scripts/test-diff-split.sh`:

```bash
bash scripts/test-diff-split.sh
```

**Requirements:** Running muxcode session with nvim in `edit.0`.

**Phases:**
1. **Setup** — creates a test file, verifies nvim opens it
2. **PreToolUse (Edit)** — simulates preview hook, verifies diff split (2 windows, diffmode on)
3. **PostToolUse (accepted)** — simulates analyze hook, verifies cleanup (1 window, temp file removed)
4. **Stale cleanup** — simulates rejected edit, ages the temp file, verifies stale diff is cleaned on next preview
5. **Skip patterns** — verifies `MUXCODE_PREVIEW_SKIP` skips matching files
6. **Write tool** — verifies Write tool opens file without diff split (no `old_string`)

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
