# OpenCode + DeepSeek V4 Pro as editor agent

Run the editor agent on OpenCode with DeepSeek V4 Pro instead of Claude Code, while retaining the edit agent's role as primary orchestrator of all muxcode agents. The edit agent must continue to delegate to build, test, review, deploy, run, watch, and commit agents via the bus, enforce its delegation rules without hook-based guards, and drive the build->test->review workflow through prompt-driven orchestration rather than PostToolUse hooks.

## Context

### Current state

| Aspect | Current behavior |
|--------|-----------------|
| Edit agent CLI | Claude Code (`claude`) — hardcoded default via `roleDefaultCLI("edit")` |
| Edit agent model | `claude-opus-4-6` via `RoleClaudeModelDefault("edit")` |
| Tool profile | `bus` + `readonly` + `common` groups — no `Write`/`Edit` tools (Claude's built-in tools aren't gated by `--allowedTools`) |
| Delegation enforcement | PreToolUse hook (`muxcode hook guard`) blocks prohibited commands |
| Event chains | PostToolUse bash hook fires build->test->review chain automatically |
| Workflow transitions | PostToolUse hooks transition `StateEditing`, `StateBuilding`, etc. |
| Analyze trigger | PostToolUse Write/Edit hook writes trigger file for analyze agent |
| Inbox polling (`--wait`) | PostToolUse hook polls inbox after `muxcode send --wait` |
| Compact | `/compact` slash command (Claude Code native) |
| Startup context | Self-addressed inbox message restored on launch |
| Agent definition | `agents/code-editor.md` — references hooks as enforcement mechanism |
| OpenCode edit config | Does not exist — no `.opencode/agents/edit.md` |

### Problem

The edit agent is tightly coupled to Claude Code. Ten integration points assume hook support, Claude-native tools, or Claude-specific commands. Running the edit agent on OpenCode with DeepSeek V4 Pro requires changes across the tool profile, agent definition, provider adaptation, workflow state management, and daemon behavior — without breaking the existing Claude Code path.

### Goal

A fully functional edit agent running on OpenCode with DeepSeek V4 Pro that:
1. Reads and writes files (Write/Edit tools in the tool profile)
2. Enforces delegation rules via OpenCode permission denies (replacing the hook guard)
3. Orchestrates the build->test->review chain via prompt-driven `muxcode send` commands (replacing hook-driven chains)
4. Triggers workflow state transitions and analyze notifications via the daemon (replacing hook-driven transitions)
5. Supports `--wait` inbox polling natively (replacing hook-driven polling)
6. Is selectable via `MUXCODE_EDIT_CLI=opencode` with zero other config changes required
7. Preserves the Claude Code edit path as the default — no regressions

## Compatibility analysis

### Edit agent functions and hook dependencies

Every function the edit agent performs today, its current mechanism, and whether it works on OpenCode:

| Function | Current mechanism | Works on OpenCode? | Required change |
|----------|------------------|--------------------|-----------------|
| Read files | `Read` tool in `readonly` shared group | Yes | None |
| Search files | `Glob`, `Grep` tools in `readonly` shared group | Yes | None |
| Write files | Claude Code built-in `Write` tool (not in `--allowedTools`) | **No** — `translateToolProfile` produces no `edit: allow` | Add `Write`, `Edit` to edit tool profile |
| Edit files | Claude Code built-in `Edit` tool (not in `--allowedTools`) | **No** — same as above | Add `Write`, `Edit` to edit tool profile |
| Block prohibited commands | PreToolUse hook (`muxcode hook guard`) | **No** — OpenCode has no PreToolUse hooks | Add explicit `deny` entries to OpenCode permission block |
| Delegate to bus agents | `Bash(muxcode send *)` in `bus` shared group | Yes | None |
| Read inbox | `Bash(muxcode inbox *)` in `bus` shared group | Yes | None |
| `--wait` inline polling | PostToolUse hook (`hookInboxPoll`) polls inbox after send | **No** — `--wait` CLI flag works (it's in the Go binary), but the hook-based poll that returns the response inline to the LLM doesn't fire | `--wait` works natively in the bash binary — OpenCode's bash tool returns stdout including the polled response. No change needed. |
| Build->test->review chain | PostToolUse bash hook resolves `EventChains["build"]` etc. | **No** — no hooks | Edit agent body already instructs manual chain orchestration. Add `adaptBodyForNonHookProvider` rewrite for edit role. |
| Workflow state: `StateEditing` | PostToolUse Write/Edit hook calls `TransitionWorkflow` | **No** — no hook fires on file edits | Daemon-side file watcher or periodic pane-capture heuristic |
| Analyze trigger (file changes) | PostToolUse Write/Edit hook writes trigger file | **No** — no hook fires | Daemon-side: detect file changes via git status polling |
| Workflow state: `StateBuilding` etc. | PostToolUse bash hook on build/test/deploy commands | N/A — edit agent delegates these; the target agent's hooks handle them | None — build/test agents handle their own transitions |
| Workflow state: `StateReviewed` | Daemon detects review->edit inbox message | Yes — daemon-driven, provider-independent | None |
| PR review (two-step) | Prompt-driven: commit fetches, review analyzes | Yes — bus messaging, no hooks involved | None |
| Jira/Confluence | `muxcode skill load` + `muxcode atlassian` commands | Yes — bash commands in bus group | None |
| Context restoration on startup | Self-addressed inbox message via `PreLaunchSetup` | **Partial** — message is written but `SendWakeUp` filters self-messages | Deliver startup message via OpenCode's `--agent` initial prompt or skip self-filter for startup |
| Compact conversation | `/compact` Claude Code slash command | **No** — not available in OpenCode | Use `muxcode session compact` for memory save; OpenCode has `compaction.autocontinue` |
| Git operations (read-only) | Not in edit tool profile — delegated to commit agent | Yes — delegation unchanged | None |
| tmux pane capture | `Bash(tmux capture-pane *)` in edit tools | Yes | None |
| Bash timeout on `--wait` | Agent sets `timeout: 300000` on Bash tool calls | **Partial** — OpenCode bash tool may have different timeout semantics | Verify OpenCode bash timeout; add `BashTimeout: 300` to edit profile |

### Functions that work without changes (11 of 18)

- Read files, Search files, Delegate to bus, Read inbox, `--wait` polling, Build->test->review chain (already prompt-driven in edit body), Workflow `StateReviewed`, PR review two-step, Jira/Confluence, tmux pane capture, Git delegation

### Functions requiring changes (7 of 18)

1. **Write/Edit files** — tool profile update
2. **Block prohibited commands** — OpenCode permission deny rules
3. **Agent body adaptation** — `adaptBodyForNonHookProvider` for edit role
4. **Workflow `StateEditing` transition** — daemon-side detection
5. **Analyze trigger** — daemon-side file change detection
6. **Startup context restoration** — alternative delivery mechanism
7. **Compact** — OpenCode-compatible compaction

## Design

### 1. Tool profile: add Write/Edit for edit role

The edit tool profile currently omits `Write` and `Edit` because Claude Code's built-in tools bypass `--allowedTools`. OpenCode gates all tools through its permission system, so they must be explicit.

**Change** in `bus/profile.go` `DefaultConfig()`:

```go
"edit": {
  Include:     []string{"bus", "readonly", "common"},
  CdPrefix:    false,
  BashTimeout: 300,
  Tools: []string{
    "Write", "Edit",  // NEW — required for OpenCode; no-op for Claude Code
    "Bash(tree *)", "Bash(python3*)", "Bash(jq*)",
    "Bash(tmux capture-pane *)", "Bash(tmux display-message *)",
  },
},
```

**Impact on Claude Code**: none. Claude Code's `--allowedTools` is additive — adding `Write`/`Edit` to the explicit list doesn't restrict anything (Claude already has them implicitly). The `BashTimeout: 300` addition prevents `--wait` timeouts for long operations.

### 2. OpenCode permission denies: delegation enforcement

The PreToolUse guard hook blocks 20+ command prefixes for the edit agent. On OpenCode, this must be replicated via `permission.bash.deny` rules in the generated `.opencode/agents/edit.md`.

**Change** in `bus/provider_opencode.go` `translateToolProfile()`:

Add a new `editDenyRules()` function that returns explicit deny patterns for the edit role:

```go
func editDenyRules() []string {
  return []string{
    "gh *", "git commit*", "git push*", "git pull*", "git rebase*",
    "git checkout*", "git branch*", "git merge*", "git stash*", "git tag*",
    "git reset*", "git cherry-pick*", "git revert*", "git am*",
    "./build.sh*", "pnpm build*", "pnpm run build*", "make*",
    "pnpm test*", "jest*", "pytest*", "go test*", "go build*",
    "cargo test*", "cargo build*",
    "cdk synth*", "cdk diff*", "cdk deploy*",
    "aws logs*", "tail -f*", "kubectl logs*", "docker logs*", "stern*",
    "aws lambda*", "aws stepfunctions*", "aws s3*", "aws s3api*",
    "aws glue*", "aws dynamodb*", "aws kinesis*", "aws firehose*",
    "aws events*", "aws sqs*", "aws sns*", "aws ssm*", "aws ecs*",
    "aws secretsmanager*", "aws cloudformation*", "aws appflow*",
    "curl*",
  }
}
```

`translateToolProfile("edit")` calls `editDenyRules()` and emits them as `deny` entries in the OpenCode permission block. Other roles are unaffected.

**Generalization**: store deny rules in the tool profile config rather than hardcoding. Add a `DenyTools []string` field to `ToolProfile`:

```go
type ToolProfile struct {
  Include     []string `json:"include,omitempty"`
  Tools       []string `json:"tools,omitempty"`
  DenyTools   []string `json:"deny_tools,omitempty"` // NEW
  CdPrefix    bool     `json:"cd_prefix,omitempty"`
  BashTimeout int      `json:"bash_timeout,omitempty"`
}
```

The edit profile gets the deny list. `translateToolProfile` emits them as OpenCode `deny` rules. Claude Code ignores `DenyTools` (the hook guard handles enforcement). This enables any role to carry deny rules for non-hook providers.

### 3. Agent body adaptation for edit role

`adaptBodyForNonHookProvider()` currently only handles `build` and `test` roles. The edit agent body contains:

> "A PreToolUse hook (`muxcode hook guard`) enforces this at the tool level — prohibited commands are blocked before execution. Always delegate on the first attempt."

This is false on OpenCode. The function must rewrite this for the edit role.

**Change** in `bus/provider_opencode.go` `adaptBodyForNonHookProvider()`:

Add `"edit"` to the replacements map:

```go
"edit": {
  "A PreToolUse hook (`muxcode hook guard`) enforces this at the tool level — prohibited commands are blocked before execution. Always delegate on the first attempt.":
    "Your CLI does not have a PreToolUse hook to enforce this. The permission system blocks prohibited commands, but you MUST self-enforce delegation rules. Never attempt a prohibited command — delegate on the first attempt.",
  "The automated chain stops at review.":
    "There is no automated hook chain. After code changes, you MUST manually orchestrate: (1) send build, (2) on success send test, (3) on success send review. The chain stops at review — wait for the user before committing.",
},
```

### 4. Workflow state transitions without hooks

Two workflow transitions are hook-driven and don't fire on OpenCode:

**`StateEditing`** — currently triggered by PostToolUse Write/Edit hook. On OpenCode, the daemon can detect file edits via periodic `git status` or `git diff --stat` checks. The daemon already runs every 5 seconds.

**Analyze trigger** — currently triggered by the same PostToolUse hook writing a trigger file. On OpenCode, the daemon can write the trigger file when it detects new file changes.

**Change** in `watcher/watcher.go`:

Add `checkNonHookEdits()` to the daemon's poll loop. For the edit role on a non-hook provider:

1. Run `git diff --stat` every 10 seconds (debounced — skip if last check was <10s ago)
2. Compare changed file list against last-known set
3. If new files changed: transition to `StateEditing`, write the analyze trigger file
4. Track `last-edit-check` timestamp and `last-edit-files` hash in state files

This is intentionally lightweight — `git diff --stat` is fast (<10ms on typical repos) and only runs when the edit agent is on a non-hook provider.

### 5. Startup context restoration

`PreLaunchSetup("edit")` writes a self-addressed inbox message, but `SendWakeUp` filters self-messages to prevent echo loops. For OpenCode, the startup message should be delivered as part of the initial agent prompt.

**Change** in `bus/provider_opencode.go` `ConfigureLaunch()`:

When `role == "edit"`, append the startup context instruction to `cfg.SharedPrompt`:

```go
if role == "edit" {
  cfg.SharedPrompt += "\n\n## Startup\n\nOn startup, review last saved context from memory to restore session state: `muxcode memory context`"
}
```

This replaces the self-addressed inbox message for OpenCode. Claude Code's `PreLaunchSetup` continues to use the inbox message approach.

### 6. OpenCode-compatible compaction

Claude Code uses `/compact` for conversation compression. OpenCode has auto-compaction via `compaction.autocontinue`. The edit agent body and SharedPrompt contain Claude-specific compact instructions.

**Change** in `bus/profile.go` `BuildSharedPrompt()`:

Gate the compact instructions on the provider. For non-hook providers, replace `/compact` references with:

```markdown
## Session management

When context is getting long, save important learnings:
1. `muxcode session compact "<summary>"`
2. OpenCode handles conversation compaction automatically.
```

### 7. DeepSeek V4 Pro model configuration

**Change** in `bus/launch.go` `RoleOpenCodeModelDefault()`:

Add `edit` to the defaults:

```go
case "edit":
  return "opencode-go/deepseek-v4-pro"
```

This is the default when `MUXCODE_EDIT_CLI=opencode` and no `MUXCODE_EDIT_MODEL` override is set. Users can override with any model via `MUXCODE_EDIT_MODEL`.

**Note**: The exact OpenCode model ID for DeepSeek V4 Pro depends on the provider configured in OpenCode. If the user has a different provider setup, `MUXCODE_EDIT_MODEL` overrides. The `opencode-go/` prefix routes through OpenCode's built-in provider.

### 8. SharedPrompt: manual chain orchestration for edit

The edit role's `SharedPrompt` currently omits the "Manual Bus Messaging (no hook support)" section because it's gated on `role != "edit"`. For OpenCode, the edit agent needs explicit chain orchestration instructions.

**Change** in `bus/profile.go` `BuildSharedPrompt()`:

Change the gate from `!provider.SupportsHooks() && role != "edit"` to `!provider.SupportsHooks()`. This adds manual bus messaging instructions to the edit agent on OpenCode. Claude Code edit is unaffected (it has hooks).

### Architecture: provider-aware edit agent

```
MUXCODE_EDIT_CLI=opencode
      │
      ▼
ResolveProviderCLI("edit") → "opencode"
ResolveProvider("edit")    → OpenCodeProvider{}
      │
      ▼
ConfigureLaunch()
├── ResolveAgentFile("code-editor") → agents/code-editor.md
├── adaptBodyForNonHookProvider(body, "edit") → rewrites hook references
├── BuildSharedPrompt("edit") → includes manual chain instructions
└── Append startup context instruction
      │
      ▼
WriteAgentConfig("edit")
├── translateToolProfile("edit") → Write, Edit, bus, readonly, common allows + deny rules
└── Writes .opencode/agents/edit.md with DeepSeek V4 Pro model
      │
      ▼
BuildExecArgs() → ["opencode", "--agent", "edit", "--model", "opencode-go/deepseek-v4-pro"]
      │
      ▼
Daemon poll loop
├── checkIdleAgents() → SendWakeUp with message injection (60s cooldown)
├── checkNonHookEdits() → git diff polling → StateEditing + analyze trigger
├── checkNonHookTasks() → DetectTaskCompletion for --wait responses
└── checkInboxes() → StateReviewed (unchanged, provider-independent)
```

### Relationship to Claude Code edit path

All changes are provider-gated. The Claude Code path is the default (`roleDefaultCLI("edit")` returns `"claude"`) and requires `MUXCODE_EDIT_CLI=opencode` to switch. No existing behavior changes:

| Change | Claude Code impact |
|--------|--------------------|
| `Write`/`Edit` in edit tool profile | No-op — Claude already has these implicitly |
| `DenyTools` in `ToolProfile` | Ignored — hook guard handles enforcement |
| `adaptBodyForNonHookProvider` for edit | Only fires when `provider != ClaudeCode` |
| `checkNonHookEdits()` in daemon | Only runs when edit provider is non-hook |
| `BashTimeout: 300` on edit profile | Beneficial — prevents `--wait` timeout on long operations |
| Startup prompt append | Only fires in `OpenCodeProvider.ConfigureLaunch` |
| SharedPrompt manual chain gate change | Only fires when `!provider.SupportsHooks()` |

## Implementation

### Phase 1: Tool profile and deny rules

New files:

| File | Purpose |
|------|---------|
| `bus/profile_test.go` (new tests) | Tests for `DenyTools` field, `ResolveTools` with denies, edit profile resolution |

Updated files:

| File | Change |
|------|--------|
| `bus/profile.go` | Add `DenyTools` field to `ToolProfile` struct. Add `Write`, `Edit`, `BashTimeout: 300` to edit profile. Add deny rules for edit role (all prohibited command prefixes from `code-editor.md`). Update `mergeConfigs` to merge `DenyTools`. |
| `bus/provider_opencode.go` | Update `translateToolProfile()` to emit `DenyTools` as OpenCode `deny` entries in the permission block. |
| `bus/profile_test.go` | Tests for edit profile containing Write/Edit, DenyTools resolution, translateToolProfile deny output. |

Success criteria:
- [ ] `ResolveTools("edit")` includes `Write` and `Edit`
- [ ] Edit `ToolProfile.DenyTools` contains all prohibited prefixes from `code-editor.md`
- [ ] `translateToolProfile("edit")` produces OpenCode permission with `edit: allow` and bash deny rules
- [ ] `mergeConfigs` correctly merges `DenyTools` from override configs
- [ ] Claude Code edit agent is unaffected (existing tests pass)
- [ ] `BashTimeout` on edit profile is 300 seconds

### Phase 2: Agent body adaptation and model config

Updated files:

| File | Change |
|------|--------|
| `bus/provider_opencode.go` | Add `"edit"` replacements to `adaptBodyForNonHookProvider()`: rewrite hook guard reference, rewrite automated chain reference. Update `ConfigureLaunch()` to append startup context instruction for edit role. |
| `bus/launch.go` | Add `case "edit": return "opencode-go/deepseek-v4-pro"` to `RoleOpenCodeModelDefault()`. |
| `bus/provider_opencode_test.go` | Tests for edit body adaptation (hook reference replaced, chain reference replaced). Test for `resolveOpenCodeModel("edit")` returning DeepSeek V4 Pro. |

Success criteria:
- [ ] `adaptBodyForNonHookProvider(editBody, "edit")` replaces hook guard reference with self-enforcement instruction
- [ ] `adaptBodyForNonHookProvider(editBody, "edit")` replaces automated chain reference with manual orchestration steps
- [ ] `resolveOpenCodeModel("edit")` returns `"opencode-go/deepseek-v4-pro"`
- [ ] `MUXCODE_EDIT_MODEL` env var overrides the default
- [ ] Startup context instruction appended to SharedPrompt for OpenCode edit
- [ ] Claude Code edit body is unchanged (no `"edit"` key in replacements for ClaudeCodeProvider)

### Phase 3: SharedPrompt and compact instructions

Updated files:

| File | Change |
|------|--------|
| `bus/profile.go` | In `BuildSharedPrompt()`: remove `role != "edit"` gate from manual bus messaging section — emit for all non-hook roles. Gate compact instructions on provider: Claude Code gets `/compact`, non-hook gets `muxcode session compact` + auto-compaction note. |
| `bus/profile_test.go` | Tests for SharedPrompt output with non-hook edit: contains manual bus instructions, contains OpenCode compact instructions, does not contain `/compact`. |

Success criteria:
- [ ] `BuildSharedPrompt("edit")` on OpenCode includes "Manual Bus Messaging" section
- [ ] `BuildSharedPrompt("edit")` on OpenCode includes OpenCode-compatible compact instructions
- [ ] `BuildSharedPrompt("edit")` on Claude Code is unchanged (no manual bus section, has `/compact`)
- [ ] Non-edit roles on OpenCode are unaffected

### Phase 4: Daemon-side workflow transitions

Updated files:

| File | Change |
|------|--------|
| `watcher/watcher.go` | Add `checkNonHookEdits()`: runs every 10 seconds when edit provider is non-hook. Executes `git diff --stat` to detect file changes. On new changes: `TransitionWorkflow(StateEditing)` + write analyze trigger file. Track state in `edit-diff-hash` file. |
| `bus/config.go` | Add `EditDiffHashPath()` helper for the state file path. |
| `watcher/watcher_test.go` | Tests for `checkNonHookEdits()`: detects new changes, ignores unchanged, debounce interval, trigger file written. |

Success criteria:
- [ ] Daemon detects file edits made by non-hook edit agent via `git diff --stat`
- [ ] `StateEditing` workflow transition fires within 10 seconds of a file edit
- [ ] Analyze trigger file written with changed file paths
- [ ] No `git diff` polling when edit agent is on Claude Code (hook-driven)
- [ ] `git diff --stat` execution is <50ms on a typical repo
- [ ] Debounce prevents redundant transitions on rapid edits

### Phase 5: Integration testing and docs

New files:

| File | Purpose |
|------|---------|
| `scripts/test-opencode-edit.sh` | Integration test: launches edit on OpenCode, verifies agent config generation, permission block, model resolution |

Updated files:

| File | Change |
|------|--------|
| `CLAUDE.md` | Add OpenCode edit configuration section: env vars, model override, known limitations |
| `docs/agents.md` | Add OpenCode edit subsection: how to enable, model defaults, delegation enforcement differences |
| `docs/configuration.md` | Add `MUXCODE_EDIT_CLI`, `MUXCODE_EDIT_MODEL` documentation, DeepSeek V4 Pro model ID |

Success criteria:
- [ ] Integration test passes: edit agent launches on OpenCode with DeepSeek V4 Pro
- [ ] Generated `.opencode/agents/edit.md` has correct permissions, model, and adapted body
- [ ] Edit agent can read, write, and edit files through OpenCode
- [ ] Edit agent delegates build/test/review/commit via bus (prohibited commands blocked by deny rules)
- [ ] `--wait` returns responses inline (OpenCode bash tool captures stdout)
- [ ] Workflow state transitions fire via daemon polling
- [ ] Analyze agent receives file change notifications
- [ ] Claude Code edit path has zero regressions (all existing tests pass)
- [ ] Documentation updated

## Configuration

| Env var | Default | Description |
|---------|---------|-------------|
| `MUXCODE_EDIT_CLI` | `claude` | Set to `opencode` to run edit agent on OpenCode |
| `MUXCODE_EDIT_MODEL` | (none — falls through to `opencode-go/deepseek-v4-pro` when CLI is opencode) | Override model for edit agent on OpenCode |
| `MUXCODE_AGENT_CLI` | (none) | Session-wide CLI default — overridden by `MUXCODE_EDIT_CLI` |

**Quick start**:
```bash
export MUXCODE_EDIT_CLI=opencode
# Optional: override model
export MUXCODE_EDIT_MODEL=opencode-go/deepseek-v4-pro
muxcode
```

## Known limitations

| Limitation | Impact | Mitigation |
|------------|--------|------------|
| No real-time delegation enforcement | DeepSeek V4 Pro could attempt prohibited commands if it ignores deny rules | OpenCode permission deny blocks execution; agent body strongly instructs delegation |
| No nvim diff preview cleanup | PostToolUse hook that cleans diff splits doesn't fire | Diff previews must be manually closed; could add daemon-side cleanup |
| No file-level workflow precision | `git diff --stat` polling detects changes at file level, not edit-by-edit | Acceptable — workflow state is coarse-grained anyway |
| `IsIdle` always false | Daemon can't detect when OpenCode edit is idle at the prompt | 60-second cooldown on `SendWakeUp` prevents spam; future: OpenCode idle detection via `▣` marker |
| No inline `--wait` hook polling | `--wait` works via the Go binary's built-in polling, not the hook | Response appears in bash stdout — OpenCode captures this. Functionally equivalent. |
| OpenCode auto-compact timing | OpenCode decides when to compact, not the user | `compaction.autocontinue` config controls behavior |

## Dependencies

| Dependency | Purpose | Status |
|------------|---------|--------|
| `bus/profile.go` | Tool profiles, SharedPrompt | Existing (needs update) |
| `bus/provider_opencode.go` | OpenCode provider, body adaptation, tool translation | Existing (needs update) |
| `bus/launch.go` | Model defaults, CLI resolution | Existing (needs update) |
| `watcher/watcher.go` | Daemon poll loop | Existing (needs update) |
| `bus/config.go` | Path helpers | Existing (needs update) |
| `agents/code-editor.md` | Agent definition | Existing (no change — adaptation is provider-side) |
| OpenCode CLI | Must be installed and configured with a DeepSeek V4 Pro provider | External prerequisite |

## Status

Draft
