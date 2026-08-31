# Gemini CLI provider

Add Google Gemini CLI (`gemini`) as a fifth provider in muxcode's multi-provider architecture. Gemini CLI is structurally the closest to Claude Code of any alternative — it has a full hook system (`BeforeTool`/`AfterTool`), JSON stdin/stdout protocol, exit-code-2 blocking, inline TUI without alternate screen, and MCP support. This makes it the first alternative provider that can run with `SupportsHooks() = true`, enabling deterministic chains, diff preview, edit guard, and workflow state transitions without degradation.

## Context

### Current providers

| Provider | CLI | Hooks | Chains | Diff preview | Guard | Idle detection |
|----------|-----|-------|--------|--------------|-------|----------------|
| Claude Code | `claude` | Full (`settings.json`) | Deterministic (hook-driven) | PreToolUse → bash script | PreToolUse exit-code-2 | `❯` prompt |
| OpenCode | `opencode` | None (plugin system exists) | Prompt-based (degraded) | Broken (requires plugin) | DenyTools + pane audit | Not supported (TUI) |
| Codex CLI | `codex` | None | Prompt-based (degraded) | None | Prompt instructions | Heuristic (`>` / "Summarize") |
| Local LLM | `local` | None | Prompt-based (degraded) | None | `IsToolAllowed()` in Go | Not supported (daemon-driven) |

### Current failure mode

Until this spec lands, `MUXCODE_AGENT_CLI=gemini` (or a per-role `MUXCODE_{ROLE}_CLI=gemini`) silently launches the wrong provider: `ResolveProvider()` in `bus/provider.go` recognizes only `opencode`, `codex`, and `local` — any other value falls through the `default:` case to `ClaudeCodeProvider`, so an agent configured for Gemini launches Claude Code with no warning. `install.sh` deliberately omits Gemini from its provider catalogue for exactly this reason (comment above `provider_label()`) — that omission and its comment are removed by this spec's installer phase.

### Gemini CLI capabilities

| Aspect | Gemini CLI | Notes |
|--------|-----------|-------|
| Binary | `gemini` | npm: `@google/gemini-cli`, also Homebrew |
| Hooks | `BeforeTool` / `AfterTool` in `settings.json` | JSON stdin/stdout, exit code 2 = block, matcher patterns |
| Approval | `--approval-mode yolo` | Auto-approve all tools for unattended operation |
| TUI | Inline (no alt screen) | Pane capture works, no `--no-alt-screen` flag needed |
| Models | `gemini-2.5-pro`, `gemini-2.5-flash`, `gemini-2.5-flash-lite` | Aliases: `pro`, `flash`, `auto` |
| Agent files | Not directly equivalent | Context via `GEMINI.md` and `.gemini/system.md` |
| Tool names | `run_shell_command`, `read_file`, `write_file`, `replace`, `grep_search`, `glob` | Different from Claude Code |
| Config | `.gemini/settings.json` (project), `~/.gemini/settings.json` (user) | Similar structure to Claude Code |
| MCP | Full support via `mcpServers` in settings | Same pattern as Claude Code |
| Session resume | `gemini -r "latest"` | Checkpoint-based |
| System prompt | `.gemini/system.md` or `GEMINI_SYSTEM_MD` env var | File-based injection |
| Sandbox | `--sandbox` (Docker/Podman) | Optional isolation |
| Auth | `GEMINI_API_KEY` or Vertex AI (`GOOGLE_APPLICATION_CREDENTIALS`) | Multiple auth paths |

### Why add Gemini CLI

1. **Hook parity** — only alternative provider with a full hook system. Diff preview, edit guard, chains, and workflow state transitions work natively without degradation.
2. **Google models** — access to Gemini 2.5 Pro (1M context, strong coding) without routing through OpenCode/OpenRouter.
3. **Cost efficiency** — Gemini 2.5 Flash for command-execution roles (build, test, deploy) at fraction of Claude/GPT cost.
4. **Tool diversity** — web search and web fetch are built-in tools (no MCP server needed).
5. **Free tier** — generous free API quota for experimentation.

### Goal

1. A `GeminiProvider` implementing the `Provider` interface with `SupportsHooks() = true`
2. Hook configuration in `.gemini/settings.json` mirroring Claude Code's `config/settings.json`
3. Agent context injection via `.gemini/system.md` (equivalent to `--append-system-prompt`)
4. Full integration: hot reload, provider selector modal, `muxcode config set`, mode cycling
5. Works for all roles — edit (with full diff preview/guard), build, test, review, etc.

## Design

### 1. Provider implementation

New `GeminiProvider` struct implementing the full `Provider` interface:

```go
type GeminiProvider struct{}

func (p *GeminiProvider) Name() string           { return "gemini" }
func (p *GeminiProvider) SupportsHooks() bool    { return true }
func (p *GeminiProvider) IdlePromptChar() string { return ">" }
```

**Key method behaviors**:

| Method | Behavior |
|--------|----------|
| `ConfigureLaunch` | Resolve model, write `.gemini/system.md`, configure approval mode |
| `BuildExecArgs` | Returns `gemini` with `--approval-mode yolo -m <model>` |
| `IsIdle` | Detect Gemini's input prompt in pane (TBD — needs testing) |
| `IsAlive` | Check pane for running gemini process |
| `ClassifyPane` | Detect startup states (trust prompt, API key prompt) |
| `AcceptStartup` | Handle trust/auth prompts |
| `SendWakeUp` | Inject "You have new messages" via send-keys (same as Claude Code) |
| `Compact` | Send `/compact` or equivalent (TBD — check if Gemini has compaction) |
| `WriteAgentConfig` | Write `.gemini/settings.json` with hooks + `.gemini/system.md` with prompt |
| `DetectTaskCompletion` | Not needed — hooks handle this |

### 2. Hook configuration

Gemini CLI hooks use the same conceptual model as Claude Code but different JSON structure. `WriteAgentConfig` generates `.gemini/settings.json`:

```json
{
  "hooks": {
    "BeforeTool": [
      {
        "matcher": "run_shell_command",
        "hooks": [{"type": "command", "command": "muxcode hook guard"}]
      },
      {
        "matcher": "write_file|replace",
        "hooks": [{"type": "command", "command": "muxcode-preview-hook.sh"}]
      },
      {
        "matcher": "read_file|run_shell_command|grep_search|glob",
        "hooks": [{"type": "command", "command": "muxcode-diff-cleanup.sh"}]
      }
    ],
    "AfterTool": [
      {
        "matcher": "write_file|replace",
        "hooks": [{"type": "command", "command": "muxcode hook analyze"}]
      },
      {
        "matcher": "run_shell_command",
        "hooks": [{"type": "command", "command": "muxcode hook bash"}]
      }
    ]
  },
  "tools": {
    "allowed": ["read_file", "read_many_files", "grep_search", "glob", "list_directory"]
  }
}
```

### 3. Hook JSON translation

Gemini's hook JSON format differs from Claude Code's. The hook scripts (`muxcode-preview-hook.sh`, `muxcode hook guard`, `muxcode hook analyze`) currently parse Claude Code's JSON format. They need adaptation to also handle Gemini's format.

**Claude Code hook JSON (stdin)**:
```json
{
  "tool_name": "Edit",
  "tool_input": {
    "file_path": "/path/to/file",
    "old_string": "...",
    "new_string": "..."
  }
}
```

**Gemini CLI hook JSON (stdin)** (expected — needs verification):
```json
{
  "tool": "replace",
  "args": {
    "file_path": "/path/to/file",
    "old_string": "...",
    "new_string": "..."
  }
}
```

A translation layer (or detection in hook scripts) normalizes the format:

```bash
# In muxcode-preview-hook.sh — detect provider format
TOOL_NAME=$(echo "$EVENT_JSON" | jq -r '.tool_name // .tool // empty')
FILE_PATH=$(echo "$EVENT_JSON" | jq -r '.tool_input.file_path // .args.file_path // empty')
OLD_STRING=$(echo "$EVENT_JSON" | jq -r '.tool_input.old_string // .args.old_string // empty')
```

### 4. Tool name mapping

Gemini uses different tool names than Claude Code. The tool profile system needs mapping:

| Claude Code tool | Gemini CLI tool | Notes |
|-----------------|-----------------|-------|
| `Bash(*)` | `run_shell_command` | Shell execution |
| `Read(*)` | `read_file` / `read_many_files` | File reading |
| `Write(*)` | `write_file` | Full file write |
| `Edit(*)` | `replace` | In-place edit (old_string/new_string) |
| `Grep(*)` | `grep_search` | Search |
| `Glob(*)` | `glob` | File pattern matching |
| `WebSearch(*)` | `google_web_search` | Built-in (no MCP needed) |
| `WebFetch(*)` | `web_fetch` | Built-in (no MCP needed) |

The `ResolveTools()` function in `bus/profile.go` currently returns Claude Code glob patterns. For Gemini, it needs to translate these into Gemini tool names for the `tools.allowed` / `tools.exclude` config.

### 5. Agent context injection

Claude Code uses `--append-system-prompt` for shared bus instructions. Gemini uses `.gemini/system.md`:

```go
func (p *GeminiProvider) WriteAgentConfig(role string) error {
    // Write system prompt with bus instructions
    prompt := BuildSharedPrompt(role)
    agentBody := resolveAgentBody(role)  // from agent definition file
    
    systemMd := agentBody + "\n\n" + prompt
    os.WriteFile(".gemini/system.md", []byte(systemMd), 0644)
    
    // Write settings.json with hooks
    settings := buildGeminiSettings(role)
    os.WriteFile(".gemini/settings.json", []byte(settings), 0644)
    
    return nil
}
```

### 6. Approval mode

For unattended agent operation, Gemini needs `--approval-mode yolo`:

```go
func (p *GeminiProvider) BuildExecArgs(cfg *LaunchConfig) (string, []string) {
    args := []string{"--approval-mode", "yolo"}
    
    // Model selection
    if cfg.Model != "" {
        args = append(args, "-m", cfg.Model)
    }
    
    // Skip workspace trust check
    args = append(args, "--skip-trust")
    
    return "gemini", args
}
```

Read-only roles (review, analyze) can use `--approval-mode auto_edit` instead — auto-approves reads and edits but prompts for shell commands.

### 7. Idle detection

Gemini CLI renders an inline prompt when idle. The exact character/pattern needs testing, but initial approach:

```go
func (p *GeminiProvider) IsIdle(session, role string) bool {
    target := PaneTarget(session, role)
    cmd := exec.Command("tmux", "capture-pane", "-t", target, "-p", "-S", "-5")
    out, err := cmd.Output()
    if err != nil {
        return false
    }
    // Look for Gemini's input prompt indicator
    lines := strings.Split(strings.TrimSpace(string(out)), "\n")
    for i := len(lines) - 1; i >= 0; i-- {
        trimmed := strings.TrimSpace(lines[i])
        if trimmed == ">" || strings.HasSuffix(trimmed, ">") {
            return true
        }
    }
    return false
}
```

### 8. Wake-up notifications

Since Gemini CLI has hooks and an inline prompt (similar to Claude Code), wake-up uses the same send-keys pattern:

```go
func (p *GeminiProvider) SendWakeUp(session, role string) error {
    target := PaneTarget(session, role)
    msg := latestInboxMessage(session, role)
    if msg == "" {
        msg = "You have new messages"
    }
    return tmuxSendKeys(target, msg)
}
```

### 9. Provider selector integration

Add Gemini to `AvailableProviders()`:

```go
{
    Name:    "Gemini CLI",
    CLI:     "gemini",
    Models:  []string{"gemini-2.5-pro", "gemini-2.5-flash", "gemini-2.5-flash-lite"},
    Default: "gemini-2.5-flash",
},
```

### 10. Configuration resolution

| Priority | Source | Example |
|----------|--------|---------|
| 1 | Runtime override | `/tmp/muxcode-bus-{session}/config/build.env` → `MUXCODE_BUILD_CLI=gemini` |
| 2 | Per-role env var | `MUXCODE_BUILD_CLI=gemini` |
| 3 | Global env var | `MUXCODE_AGENT_CLI=gemini` |
| 4 | Config file | `~/.config/muxcode/config` → `MUXCODE_BUILD_CLI=gemini` |
| 5 | Built-in default | `roleDefaultCLI()` (won't return gemini unless changed) |

### Architecture diagram

```
muxcode reload build --cli gemini --model gemini-2.5-flash
      │
      ├─ Write runtime override: MUXCODE_BUILD_CLI=gemini, MUXCODE_BUILD_MODEL=gemini-2.5-flash
      │
      ├─ GracefulStop("build")
      │
      ├─ GeminiProvider.WriteAgentConfig("build")
      │   ├─ .gemini/settings.json (hooks: guard, preview, analyze, bash chain)
      │   └─ .gemini/system.md (agent body + shared prompt + bus instructions)
      │
      ├─ GeminiProvider.BuildExecArgs()
      │   └─ "gemini --approval-mode yolo -m gemini-2.5-flash --skip-trust"
      │
      ├─ tmux send-keys "muxcode agent launch build" Enter
      │
      └─ Verify alive → remove reload marker → notify edit
```

### Relationship to existing features

| Feature | Interaction |
|---------|------------|
| Diff preview hooks | Works natively — `BeforeTool` matcher on `write_file\|replace` fires `muxcode-preview-hook.sh` |
| Edit guard | Works natively — `BeforeTool` matcher on `run_shell_command` fires `muxcode hook guard` |
| Event chains | Works natively — `AfterTool` fires chain hooks (build→test→review) |
| Workflow state | `AfterTool` on `write_file\|replace` transitions to `StateEditing` |
| Hot reload | Full support — `muxcode reload <role> --cli gemini` |
| Provider selector | Listed in modal with model options |
| Mode cycling | Works for any role slot (edit, research, auto, etc.) |
| `muxcode config set` | Persistent: `muxcode config set build.cli gemini` |
| `checkNonHookEdits()` | Skipped — `SupportsHooks()` returns true |
| Agent health | Standard idle/alive detection |
| OpenCode diff plugin | Independent — Gemini uses hook scripts directly |

## Implementation

### Phase 1: Provider struct and launch

New files:

| File | Purpose |
|------|---------|
| `bus/provider_gemini.go` | `GeminiProvider` struct implementing `Provider` interface |
| `bus/provider_gemini_test.go` | Tests for launch args, model resolution, idle detection |

Updated files:

| File | Change |
|------|--------|
| `bus/provider.go` | Add `case "gemini"` to `ResolveProvider()` switch |
| `bus/provider_options.go` | Add Gemini entry to `AvailableProviders()` |
| `bus/launch.go` | Add Gemini model resolution helpers |

Success criteria:
- [ ] `ResolveProvider("build")` returns `&GeminiProvider{}` when `MUXCODE_BUILD_CLI=gemini`
- [ ] `BuildExecArgs` returns `gemini --approval-mode yolo -m <model> --skip-trust`
- [ ] `SupportsHooks()` returns `true`
- [ ] Model resolution: `MUXCODE_{ROLE}_MODEL` → `MUXCODE_GEMINI_MODEL` → `gemini-2.5-flash` default
- [ ] Provider selector shows "Gemini CLI" with installed detection (`which gemini`)
- [ ] Read-only roles use `--approval-mode auto_edit` instead of `yolo`

### Phase 2: Hook configuration and `WriteAgentConfig`

Updated files:

| File | Change |
|------|--------|
| `bus/provider_gemini.go` | Implement `WriteAgentConfig()` — generate `.gemini/settings.json` and `.gemini/system.md` |

Success criteria:
- [ ] `.gemini/settings.json` generated with `BeforeTool`/`AfterTool` hooks for guard, preview, cleanup, analyze, bash
- [ ] `.gemini/system.md` generated with agent body + shared prompt (bus instructions, delegation rules)
- [ ] Hook matchers use Gemini tool names (`run_shell_command`, `write_file|replace`, etc.)
- [ ] `tools.allowed` populated from tool profile (translated to Gemini tool names)
- [ ] Settings regenerated on `muxcode reload <role> --cli gemini`
- [ ] Previous `.gemini/settings.json` backed up or overwritten cleanly

### Phase 3: Hook script adaptation

Updated files:

| File | Change |
|------|--------|
| `scripts/muxcode-preview-hook.sh` | Detect Gemini JSON format, normalize field names |
| `scripts/muxcode-diff-cleanup.sh` | No change needed (doesn't parse tool JSON) |
| `tools/muxcode/cmd/hook.go` | Update `guard` and `analyze` subcommands to handle Gemini JSON format |

Success criteria:
- [ ] `muxcode-preview-hook.sh` handles both Claude Code and Gemini JSON formats
- [ ] `muxcode hook guard` blocks prohibited commands from Gemini's `run_shell_command` tool
- [ ] `muxcode hook analyze` triggers workflow transition from Gemini's `AfterTool` events
- [ ] Hook scripts detect provider via JSON structure (presence of `tool_name` vs `tool` field)
- [ ] Exit code 2 correctly blocks tool execution in Gemini CLI

### Phase 4: Idle detection and wake-up

Updated files:

| File | Change |
|------|--------|
| `bus/provider_gemini.go` | Implement `IsIdle()`, `IsAlive()`, `ClassifyPane()`, `AcceptStartup()`, `SendWakeUp()` |

Success criteria:
- [ ] `IsIdle()` correctly detects Gemini's idle prompt character
- [ ] `IsAlive()` detects running gemini process in pane
- [ ] `ClassifyPane()` identifies trust prompt and API key prompt states
- [ ] `AcceptStartup()` handles workspace trust prompt (send `y` + Enter)
- [ ] `SendWakeUp()` injects message text at idle prompt
- [ ] Daemon wake-up cycle works (idle detection → inbox check → wake-up)

### Phase 5: Context compaction and session management

Updated files:

| File | Change |
|------|--------|
| `bus/provider_gemini.go` | Implement `Compact()` — trigger Gemini's compaction if available |

Success criteria:
- [ ] `Compact()` sends compaction command (TBD: `/compact`, `/summarize`, or session restart)
- [ ] Compaction alert from daemon triggers Gemini compaction
- [ ] If Gemini lacks native compaction, graceful no-op with log
- [ ] Session continuity preserved where possible

### Phase 6: Tool profile translation

New files:

| File | Purpose |
|------|---------|
| `bus/profile_gemini.go` | Tool name translation functions (Claude Code patterns → Gemini tool names) |
| `bus/profile_gemini_test.go` | Tests for translation |

Updated files:

| File | Change |
|------|--------|
| `bus/profile.go` | Add `TranslateToolsForGemini(role)` called by `WriteAgentConfig` |

Success criteria:
- [ ] `Bash(git *)` → `run_shell_command` with pattern note in system prompt
- [ ] `Read(*)` → `read_file`, `read_many_files`
- [ ] `Write(*)` / `Edit(*)` → `write_file`, `replace`
- [ ] `Grep(*)` → `grep_search`
- [ ] `Glob(*)` → `glob`
- [ ] DenyTools translated to `tools.exclude` in `.gemini/settings.json`
- [ ] CdPrefix handled via system prompt instruction (Gemini doesn't have workdir flag)

### Phase 7: Integration testing and documentation

New files:

| File | Purpose |
|------|---------|
| `scripts/test-gemini-provider.sh` | Integration test: launch on Gemini, verify hooks fire, verify idle detection |

Updated files:

| File | Change |
|------|--------|
| `CLAUDE.md` | Add `gemini` to provider list, add `.gemini/` to directory structure |
| `docs/agents.md` | Add Gemini row to providers table, document hook equivalence |
| `docs/configuration.md` | Add `GEMINI_API_KEY`, `MUXCODE_{ROLE}_CLI=gemini` examples |
| `docs/agent-bus.md` | Add Gemini notes to `reload` and `provider-select` sections |
| `docs/architecture.md` | Add "Gemini CLI Agent Flow" section (like Codex CLI Agent Flow) |
| `install.sh` | Add Gemini to the AI CLI provider catalogue (see below) |

**Installer catalogue entry** — `install.sh` maintains a provider catalogue (`provider_label`, `provider_desc`, `provider_installed`, `install_provider` case blocks, `use_*` flags, and per-CLI detection rows). Gemini is currently excluded on purpose because the backend doesn't exist; once Phases 1–6 land:

- [ ] Add `gemini` cases to `provider_label` ("Gemini CLI"), `provider_desc`, `provider_installed`, and `install_provider` (`npm install -g @google/gemini-cli`, Homebrew fallback `brew install gemini-cli`)
- [ ] Add `use_gemini` flag and an installed-version detection row (`command -v gemini` + `gemini --version`)
- [ ] Remove the "Gemini is deliberately absent from this catalogue" comment above `provider_label()`
- [ ] Prompt for/mention `GEMINI_API_KEY` in the install flow notes (auth is required for first launch)
- [ ] Verify `scripts/test-install.sh` passes with the new catalogue entry

Success criteria:
- [ ] Integration test passes: agent launches on Gemini, processes inbox message, replies
- [ ] Diff preview works when edit agent runs on Gemini CLI
- [ ] Guard hook blocks prohibited commands on Gemini
- [ ] Build→test→review chain fires via AfterTool hooks
- [ ] Hot reload between Claude Code and Gemini preserves inbox/memory
- [ ] Provider selector shows Gemini with correct models
- [ ] `install.sh` offers Gemini in the provider catalogue and the deliberate-absence comment is gone
- [ ] Documentation covers setup, auth, model selection, hook equivalence

## Configuration

| Env var | Default | Description |
|---------|---------|-------------|
| `GEMINI_API_KEY` | (required) | Google AI API key for Gemini models |
| `GOOGLE_APPLICATION_CREDENTIALS` | (optional) | Vertex AI service account for enterprise |
| `MUXCODE_{ROLE}_CLI` | varies | Set to `gemini` to use Gemini CLI for a role |
| `MUXCODE_{ROLE}_MODEL` | `gemini-2.5-flash` | Model selection when CLI is gemini |
| `MUXCODE_GEMINI_MODEL` | `gemini-2.5-flash` | Default model for all Gemini roles |
| `GEMINI_CLI_TRUST_WORKSPACE` | `true` (set by muxcode) | Skip folder trust check in unattended mode |

**Quick start**:

```bash
# Set API key
export GEMINI_API_KEY="your-key-here"

# Switch build agent to Gemini Flash (cheap, fast)
muxcode reload build --cli gemini --model gemini-2.5-flash

# Switch edit agent to Gemini Pro (strong coding, 1M context)
muxcode reload edit --cli gemini --model gemini-2.5-pro

# Persist for future sessions
muxcode config set build.cli gemini
muxcode config set build.model gemini-2.5-flash

# Via provider selector modal
# prefix + R → select "Gemini CLI" → select model → Reload
```

## Known limitations

| Limitation | Impact | Mitigation |
|------------|--------|------------|
| Hook JSON format differs | Hook scripts must handle both Claude Code and Gemini formats | Auto-detect via JSON structure (`tool_name` vs `tool` field) |
| No `--append-system-prompt` flag | Cannot pass prompt inline on CLI | Use `.gemini/system.md` file (written by `WriteAgentConfig`) |
| Tool names differ | Tool profiles need translation layer | `TranslateToolsForGemini()` maps Claude Code patterns to Gemini names |
| Idle prompt character unknown | May not reliably detect idle state | Needs empirical testing; fallback to task-completion heuristic |
| No agent file equivalent | Cannot use `.claude/agents/` pattern directly | Agent body injected via `.gemini/system.md` |
| `.gemini/` directory shared | Multiple Gemini roles in same project would conflict on settings | Resolve via role-specific subdirs or write settings per-launch (overwrite pattern) |
| `--approval-mode yolo` not settable in config | Must pass on CLI every time | `BuildExecArgs` always includes it; no config-file workaround needed |
| Gemini's `GEMINI.md` context | Gemini auto-loads `GEMINI.md` from project root (like `CLAUDE.md`) | Write muxcode-aware content or let it coexist with `CLAUDE.md` |
| Workspace trust prompt | Gemini prompts for trust on first run in a directory | `--skip-trust` flag + `GEMINI_CLI_TRUST_WORKSPACE=true` env var |
| Session compaction unclear | Gemini may not have `/compact` equivalent | Test empirically; fallback to session restart if no compaction command |
| Single `.gemini/settings.json` per project | All roles using Gemini share the same hooks config | Guard hook is role-aware (reads `BUS_ROLE`); preview hook checks window name |

## Dependencies

| Dependency | Purpose | Status |
|------------|---------|--------|
| `gemini` CLI binary | The provider runtime | External (npm/brew install) |
| `bus/provider.go` | Provider interface, `ResolveProvider()` switch | Existing (needs `"gemini"` case) |
| `bus/provider_options.go` | Provider selector list | Existing (needs Gemini entry) |
| `bus/profile.go` | Tool profile resolution | Existing (needs Gemini translation) |
| `bus/launch.go` | Launch config, model resolution | Existing (needs Gemini model helpers) |
| `bus/reload.go` | Hot reload | Existing (works — just needs Gemini in provider switch) |
| `scripts/muxcode-preview-hook.sh` | Diff preview | Existing (needs dual-format JSON parsing) |
| `tools/muxcode/cmd/hook.go` | Guard and analyze hooks | Existing (needs dual-format JSON parsing) |
| `config/tmux.conf` | No change needed | Existing |
| `daemon/daemon.go` | Skips `checkNonHookEdits()` for hook providers | Existing (works — Gemini returns `SupportsHooks()=true`) |
| `install.sh` | AI CLI provider catalogue | Existing (needs `gemini` catalogue entry; deliberate-absence comment removed) |

## Open questions

1. **Idle prompt pattern** — What exact character/string does Gemini CLI render when idle? Needs empirical testing with `tmux capture-pane`.
2. **Compaction** — Does Gemini CLI have a `/compact` or `/summarize` slash command? Or does it auto-compact? Need to check session management docs.
3. **Hook JSON format** — Exact structure of JSON passed to `BeforeTool`/`AfterTool` hooks via stdin. Need to verify against Gemini CLI source or by running with a logging hook.
4. **Multiple roles conflict** — If build and test both use Gemini, they share `.gemini/settings.json`. Is this a problem? (Hooks are role-agnostic guard/preview scripts, so likely fine.)
5. **Context file interaction** — Does `GEMINI.md` in project root cause issues? Should muxcode create one, ignore it, or symlink to `CLAUDE.md`?

## Status

Backlog
