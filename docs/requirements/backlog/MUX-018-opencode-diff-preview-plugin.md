# OpenCode diff preview plugin

Restore nvim diff preview functionality when OpenCode is used as the F2 Edit agent's CLI provider. Currently, the diff preview (showing proposed file changes in a split nvim view) only works with Claude Code because it relies on `PreToolUse` and `PostToolUse` hooks in `settings.json`. OpenCode has no equivalent config-based hook system — but it does have a first-class plugin system with `tool.execute.before` and `tool.execute.after` hooks that can achieve the same result.

## Context

### Current state

| Aspect | Claude Code (working) | OpenCode (broken) |
|--------|----------------------|-------------------|
| Pre-edit preview | `PreToolUse` hook on `Write\|Edit\|NotebookEdit` → `muxcode-preview-hook.sh` | No equivalent — edits happen silently |
| Diff cleanup | `PreToolUse` hook on `Read\|Bash\|Grep\|Glob` → `muxcode-diff-cleanup.sh` | No cleanup — stale diffs accumulate |
| Post-edit analyze | `PostToolUse` hook on `Write\|Edit\|NotebookEdit` → `muxcode hook analyze` | Partially covered by `checkNonHookEdits()` daemon polling (10s delay) |
| Hook mechanism | Shell commands in `.claude/settings.json`, JSON stdin, exit codes | Plugin system: TypeScript in `.opencode/`, `@opencode-ai/plugin` SDK |

### Problem

When the edit agent runs on OpenCode (via `muxcode reload edit --cli opencode` or `MUXCODE_EDIT_CLI=opencode`), the nvim diff preview in pane 0 of the edit window is completely non-functional:

1. **No pre-edit preview** — the user cannot see proposed changes before they're applied
2. **No diff cleanup** — stale diff views remain visible after moving to other tools
3. **Delayed analyze trigger** — `checkNonHookEdits()` polls git diff every 10s vs immediate PostToolUse fire
4. **No skip pattern support** — Claude Code hooks skip `.claude/settings.json` and `.muxcode/` paths; no equivalent filtering exists

The diff preview is a core UX feature — it shows the user exactly what the LLM is about to write, in full file context with syntax highlighting, before the edit lands. Losing it when switching to OpenCode makes the provider swap significantly worse.

### Goal

1. An OpenCode plugin (`.opencode/plugin.ts`) that replicates the Claude Code hook behavior using `tool.execute.before` and `tool.execute.after`
2. Same visual result: nvim opens the target file, shows a diff split with proposed changes, jumps to the changed line
3. Same cleanup behavior: diff view dismissed when non-edit tools fire
4. Same analyze trigger: immediate trigger file write on successful edits (not 10s polling)
5. Works for the edit role only (plugin checks `BUS_SESSION` and window name)

### OpenCode plugin API (relevant hooks)

From `@opencode-ai/plugin` v1.4.6 (`dist/index.d.ts`):

```typescript
"tool.execute.before"?: (
  input: { tool: string; sessionID: string; callID: string },
  output: { args: any }
) => Promise<void>;

"tool.execute.after"?: (
  input: { tool: string; sessionID: string; callID: string; args: any },
  output: { title: string; output: string; metadata: any }
) => Promise<void>;

event?: (input: { event: Event }) => Promise<void>;
```

Key differences from Claude Code hooks:
- Plugin runs **inside** the OpenCode process (Bun runtime), not as an external shell command
- `tool.execute.before` receives tool args in `output.args` (mutable — for arg rewriting)
- `tool.execute.after` receives tool args in `input.args` and result in `output`
- No stdin JSON — data passed as typed objects
- Blocking via `throw` (cancels tool execution) vs exit code 2
- No async fire-and-forget — hooks block until the promise resolves (must be fast)

## Design

### 1. Plugin structure

```
.opencode/
├── package.json          # existing — @opencode-ai/plugin dependency
├── plugin.ts             # NEW — muxcode diff preview plugin
└── agents/               # existing — agent definitions
```

The plugin exports a `PluginModule` with a `server` function that returns hooks for `tool.execute.before`, `tool.execute.after`, and optionally `event`.

### 2. Pre-edit preview (`tool.execute.before`)

When OpenCode is about to execute a Write or Edit tool:

1. Check environment: `BUS_SESSION` must be set, window must be `edit`
2. Extract file path and old_string/new_string from `output.args`
3. Apply skip patterns (same as Claude Code: `.claude/settings.json`, `.muxcode/`)
4. Skip new files (same as Claude Code: avoids W13 "file created after editing" dialog)
5. Generate temp file with proposed changes (same Python/JS logic as `muxcode-preview-hook.sh`)
6. Send tmux keys to nvim (pane 0) to open file and show diff

**Critical constraint**: the hook must complete quickly. The diff preview is visual-only — it should not block the tool execution for more than ~200ms. Since tmux send-keys is fire-and-forget, the heavy lifting (nvim rendering) happens asynchronously in the editor pane.

### 3. Diff cleanup (`tool.execute.before` on non-edit tools)

When any non-edit tool fires (Read, Bash, Grep, Glob, etc.):

1. Check if a preview temp file exists (`/tmp/muxcode-preview-{session}.tmp`)
2. If it exists and is older than 2s, send cleanup keys to nvim
3. Remove the temp file

This replicates `muxcode-diff-cleanup.sh` behavior.

### 4. Post-edit analyze (`tool.execute.after`)

When Write/Edit/NotebookEdit completes successfully:

1. Clean up the preview temp file
2. Write the analyze trigger file (`/tmp/muxcode-analyze-{session}.trigger`) with the changed file path
3. Transition workflow state to `StateEditing` (or delegate to daemon)

This replaces the slow `checkNonHookEdits()` 10-second polling with immediate notification.

### 5. Tmux interaction from TypeScript

The plugin runs inside Bun. It can execute shell commands via:

```typescript
import { $ } from "bun:shell";

// Or use the plugin context's $ (BunShell):
async function tmuxSendKeys(pane: string, keys: string) {
  await ctx.$`tmux send-keys -t ${pane} ${keys}`;
}
```

For the preview, this means:
- Open file: `tmux send-keys -t "$SESSION:edit.0" ":sil! set shortmess+=A | ..." Enter`
- Show diff: `tmux send-keys -t "$SESSION:edit.0" ":sil! let g:_mux_buf=bufnr() | ..." Enter`
- Jump to line: (after 150ms delay) `tmux send-keys -t "$SESSION:edit.0" ":sil! exe 'norm! ${LINE}Gzz'" Enter`

### 6. File path and line detection

Same logic as `muxcode-preview-hook.sh`:
- File path: `args.file_path` or `args.notebook_path`
- Line detection: grep for first non-blank line of `old_string` in the file
- Temp file generation: read original file, apply replacement, write to `/tmp/muxcode-preview-{session}.tmp`

### 7. Guard hook equivalent

The Claude Code guard hook (`muxcode hook guard`) blocks prohibited commands on the edit agent. OpenCode already handles this via `DenyTools` in the tool profile (emitted as OpenCode `deny` permission rules). The plugin does NOT need to replicate guard behavior — it's already enforced at the permission layer.

However, the plugin could optionally enhance enforcement by throwing on prohibited patterns in `tool.execute.before` for Bash tools — this provides defense-in-depth alongside DenyTools.

### 8. Plugin activation gating

The plugin should only activate for the edit role on a muxcode session:

```typescript
const session = process.env.BUS_SESSION;
if (!session) return {}; // no-op outside muxcode

// Check we're in the edit window
const window = await $`tmux display-message -p '#W'`.text();
if (window.trim() !== "edit") return {}; // no-op for non-edit windows
```

This prevents the plugin from interfering with other OpenCode instances (standalone use, other agent windows).

### Architecture diagram

```
OpenCode (edit agent, F2 pane 1)
      │
      ├─ tool.execute.before("edit", {args: {file_path, old_string, new_string}})
      │       │
      │       ├─ Skip if not muxcode edit window
      │       ├─ Skip if file matches skip patterns
      │       ├─ Skip if file doesn't exist (new file)
      │       ├─ Write temp file with proposed change
      │       ├─ tmux send-keys → nvim (edit.0): open file at line
      │       ├─ tmux send-keys → nvim (edit.0): diffthis + show temp
      │       └─ tmux send-keys → nvim (edit.0): jump to line (after 150ms)
      │
      ├─ tool.execute.before("bash"|"read"|"grep"|"glob", ...)
      │       │
      │       └─ Cleanup stale diff (if temp file >2s old)
      │
      └─ tool.execute.after("edit", {args: {file_path, ...}})
              │
              ├─ Remove temp preview file
              ├─ Write analyze trigger file
              └─ (Daemon picks up trigger → routes to analyze agent)
```

### Relationship to existing features

| Feature | Interaction |
|---------|------------|
| `muxcode-preview-hook.sh` | Plugin replicates this logic in TypeScript — same tmux commands, same temp file path, same skip patterns |
| `muxcode-diff-cleanup.sh` | Plugin replicates cleanup — same temp file check, same nvim commands |
| `muxcode hook analyze` | Plugin writes the same trigger file format — daemon's existing `checkAnalyzeTrigger()` picks it up |
| `checkNonHookEdits()` | Can be skipped when OpenCode plugin is active (plugin provides immediate triggers) |
| DenyTools / permission deny | Plugin doesn't replace guard — DenyTools already handles command blocking |
| Mode cycling (F2) | Plugin checks window name at startup — only activates in `edit` window |
| Hot reload | After `muxcode reload edit --cli opencode`, the new OpenCode process loads the plugin fresh |
| Console viewer (pane 0) | Plugin targets `edit.0` — same pane the console/nvim runs in |

## Implementation

### Phase 1: Core plugin with pre-edit preview

New files:

| File | Purpose |
|------|---------|
| `.opencode/plugin.ts` | Main plugin entry — exports PluginModule with tool hooks |

Success criteria:
- [ ] Plugin loads when OpenCode starts in the edit window (`BUS_SESSION` set)
- [ ] `tool.execute.before` fires for Edit/Write tools
- [ ] Temp file created with proposed changes at `/tmp/muxcode-preview-{session}.tmp`
- [ ] Nvim opens the target file at the correct line
- [ ] Nvim shows diff split (original left, proposed right) with syntax highlighting
- [ ] Jump-to-line fires after 150ms delay via separate tmux send-keys
- [ ] Skip patterns respected (`.claude/settings.json`, `.muxcode/`)
- [ ] New files skipped (no preview for files that don't exist yet)
- [ ] Plugin is no-op outside muxcode sessions (no `BUS_SESSION` → empty hooks)
- [ ] Plugin is no-op for non-edit windows
- [ ] Hook execution completes in <300ms (tmux send-keys is non-blocking)

### Phase 2: Diff cleanup on non-edit tools

Updated files:

| File | Change |
|------|--------|
| `.opencode/plugin.ts` | Add cleanup logic to `tool.execute.before` for non-edit tools |

Success criteria:
- [ ] Stale diff preview cleaned up when Read/Bash/Grep/Glob tools fire
- [ ] Cleanup only fires when temp file exists and is >2s old
- [ ] Concurrent hook invocations handled (same race protection as bash hook)
- [ ] Nvim returns to normal single-pane view with line numbers after cleanup

### Phase 3: Post-edit analyze trigger

Updated files:

| File | Change |
|------|--------|
| `.opencode/plugin.ts` | Add `tool.execute.after` hook for Write/Edit/NotebookEdit |

Success criteria:
- [ ] Analyze trigger file written immediately after successful edit
- [ ] Trigger file format matches daemon expectation (`<timestamp> <filepath>` per line)
- [ ] Preview temp file cleaned up after edit completes
- [ ] Daemon's `checkAnalyzeTrigger()` picks up the trigger and routes to analyze agent
- [ ] Faster than `checkNonHookEdits()` 10s polling (immediate)

### Phase 4: Guard enhancement (optional defense-in-depth)

Updated files:

| File | Change |
|------|--------|
| `.opencode/plugin.ts` | Add optional Bash command blocking in `tool.execute.before` |

Success criteria:
- [ ] Prohibited commands (git, gh, build, test, deploy) blocked via throw in hook
- [ ] Provides defense-in-depth alongside DenyTools permission rules
- [ ] Block message explains delegation requirement (same as `muxcode hook guard` stderr)
- [ ] Can be disabled via `MUXCODE_PLUGIN_GUARD=0` env var

### Phase 5: Daemon integration and `checkNonHookEdits` optimization

Updated files:

| File | Change |
|------|--------|
| `tools/muxcode/daemon/daemon.go` | Skip `checkNonHookEdits()` when OpenCode plugin is detected (check for `.opencode/plugin.ts` existence) |
| `tools/muxcode/bus/provider_opencode.go` | Add `HasDiffPlugin()` method to detect plugin presence |

Success criteria:
- [ ] `checkNonHookEdits()` skipped when plugin provides immediate triggers
- [ ] Fallback: if plugin file doesn't exist, keep 10s polling as backup
- [ ] Plugin detection is file-existence check (fast, no process inspection)

### Phase 6: Integration test and documentation

New files:

| File | Purpose |
|------|---------|
| `scripts/test-opencode-diff.sh` | Integration test: simulate OpenCode plugin hook events, verify nvim diff behavior |

Updated files:

| File | Change |
|------|--------|
| `docs/agents.md` | Add OpenCode diff preview section under Hot reload or as separate section |
| `docs/configuration.md` | Document plugin setup and env vars |
| `CLAUDE.md` | Add `.opencode/plugin.ts` to directory structure |

Success criteria:
- [ ] Integration test verifies: preview opens, cleanup fires, analyze triggers
- [ ] Plugin works after hot reload (`muxcode reload edit --cli opencode`)
- [ ] Plugin silent/no-op when `muxcode reload edit --cli claude` switches back
- [ ] Documentation covers plugin setup, skip patterns, guard behavior

## Configuration

| Env var | Default | Description |
|---------|---------|-------------|
| `BUS_SESSION` | (set by muxcode) | Session name — plugin activation gate |
| `MUXCODE_PREVIEW_SKIP` | `/.claude/settings.json /.claude/CLAUDE.md /.muxcode/` | Space-separated path patterns to skip preview |
| `MUXCODE_PLUGIN_GUARD` | `1` | Enable/disable Bash command guard in plugin (`0` to disable) |

## Known limitations

| Limitation | Impact | Mitigation |
|------------|--------|------------|
| Plugin runs inside Bun runtime | Cannot reuse bash scripts directly — must reimplement in TypeScript | Logic is straightforward (file read/write, string replace, tmux send-keys) |
| `tool.execute.before` blocks tool execution | Hook must be fast (<300ms) or tool execution is delayed | tmux send-keys is fire-and-forget; only file I/O adds latency |
| No tool args filtering in hook signature | Must check tool name manually (no matcher pattern like Claude Code) | Simple string comparison in the hook body |
| Plugin loaded per-process | Each OpenCode instance loads the plugin — window name check gates activation | Cheap check at startup, empty hooks returned for non-edit |
| MCP tools may not trigger hooks | Known OpenCode issue #2319 — MCP tool calls might not fire `tool.execute.before` | Edit agent primarily uses built-in tools (Edit, Write, Bash) |
| No exit-code-2 blocking protocol | Cannot inject stderr back into conversation like Claude Code hooks | Guard uses `throw` which blocks but doesn't inject context |
| Subagent tools don't trigger parent hooks | OpenCode issue #5894 — if edit spawns sub-agents, their edits won't preview | Edit agent doesn't use subagents in normal operation |

## Dependencies

| Dependency | Purpose | Status |
|------------|---------|--------|
| `@opencode-ai/plugin` v1.4.6 | Plugin SDK types and runtime | Installed (`.opencode/package.json`) |
| `muxcode-preview-hook.sh` | Reference implementation (bash) — logic ported to TypeScript | Existing |
| `muxcode-diff-cleanup.sh` | Reference implementation (bash) — logic ported to TypeScript | Existing |
| `bus/provider_opencode.go` | OpenCode provider — needs `HasDiffPlugin()` method | Existing (needs update) |
| `daemon/daemon.go` | `checkNonHookEdits()` — skip when plugin active | Existing (needs update) |
| Bun runtime | OpenCode's plugin runner — executes `.opencode/plugin.ts` | Provided by OpenCode |

## Status

Backlog
