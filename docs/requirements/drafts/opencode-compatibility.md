# OpenCode compatibility

Enable swapping Claude Code with [OpenCode](https://opencode.ai/) (github.com/anomalyco/opencode) on a per-agent basis. Any window/role can be configured to use an alternative AI CLI while the rest of the session continues using Claude Code. This requires abstracting the coupling points between muxcode and Claude Code behind a provider interface.

## Context

### What is OpenCode

OpenCode is an open-source AI coding agent (131K GitHub stars, v1.3.3, MIT licensed) built with TypeScript on a client/server architecture. It supports terminal TUI, desktop app, and IDE extensions.

Key capabilities relevant to muxcode integration:

| Feature | OpenCode | Claude Code |
|---------|----------|-------------|
| Runtime | TypeScript (Bun), client/server | Node.js |
| Interface | Full TUI (terminal) + desktop + IDE | Line-based CLI with inline rendering |
| Providers | Multi-provider (Anthropic, OpenAI, Google, Groq, Bedrock, etc.) | Anthropic only |
| Agent system | Built-in agents (build, plan) + custom agents via markdown/JSON | Agent definitions via markdown |
| Agent config | `opencode.json` or `.opencode/agents/*.md` with YAML frontmatter | `.claude/agents/*.md` with YAML frontmatter |
| System prompts | Per-agent `prompt` field, file refs via `{file:...}` | `--append-system-prompt` CLI flag |
| Tool permissions | Per-agent `permission` block (allow/ask/deny per tool, glob patterns for bash) | `settings.json` allow/deny lists, `--allowedTools` flag |
| Custom commands | `/command` slash syntax, markdown templates in `.opencode/commands/` | `/compact`, `/help` built-in only |
| Non-interactive | `opencode run "prompt"` with `--agent`, `--model`, `--session`, `--continue` flags | `claude -p "prompt"` |
| Headless server | `opencode serve` (API server), `opencode web` (browser UI) | Not available |
| LSP | Built-in | Not available |
| Memory file | `AGENTS.md` (via `/init`) | `CLAUDE.md` |
| Hooks | No PreToolUse/PostToolUse hook system | Full hook system via `settings.json` |
| Compact | Auto-compact at 95% context, configurable | `/compact` slash command |
| Config format | `opencode.json` / `opencode.jsonc` (JSON with comments) | `settings.json` |
| Config merge | Remote → global → env → project → `.opencode/` → inline | Global → project |

### Current muxcode coupling to Claude Code

| Coupling point | Current implementation | Claude Code specific |
|---------------|----------------------|---------------------|
| Agent invocation | `claude --agent <name> --agents <json> --append-system-prompt <text> --allowedTools <patterns>` | CLI flags unique to Claude Code |
| Hook system | `settings.json` PreToolUse/PostToolUse hooks with JSON stdin/stdout | Claude Code hook protocol |
| Permissions | `--dangerously-skip-permissions`, `settings.json` allow/deny lists | Claude Code permission model |
| Auto-accept | Detect "trust this folder" + "Bypass Permissions" prompts, dismiss via send-keys | Claude Code startup prompts |
| Idle detection | Capture tmux pane, match `❯` prompt character | Claude Code prompt character |
| Notifications | Send-keys "You have new messages" + Enter when idle at `❯` | Requires line-based input |
| Compact | Inject `/compact` slash command via send-keys | Claude Code slash command |
| Model selection | `--model` flag, `MUXCODE_{ROLE}_CLAUDE_MODEL` env vars | Claude Code model names |
| Agent definitions | Markdown files in `.claude/agents/` with YAML frontmatter | Claude Code agent directory |
| Settings merge | `install.sh` merges hooks/permissions into `~/.claude/settings.json` | Claude Code config path |

### Key challenges

1. **Full TUI vs line-based** — OpenCode's TUI cannot be interacted with via simple `tmux send-keys` the way Claude Code's line-based prompt can. Idle detection, wake-up notifications, and text input all work differently.
2. **No hook system** — muxcode's build→test→review chains, edit guard, file-change routing, and workflow state transitions are all driven by Claude Code hooks. OpenCode has no equivalent.
3. **Different agent config paths** — Claude Code uses `.claude/agents/`, OpenCode uses `.opencode/agents/`. The YAML frontmatter format is similar but the fields differ (OpenCode has `mode`, `permission`, `temperature`; Claude Code has `description` only).
4. **Different permission model** — Claude Code uses `--allowedTools` patterns and `settings.json` allow/deny lists. OpenCode uses per-agent `permission` blocks with allow/ask/deny per tool and glob patterns for bash commands.
5. **Non-interactive mode difference** — `opencode run` supports `--continue`, `--session`, `--agent`, `--model` for persistent sessions. This is more capable than Claude Code's `-p` flag and could enable a different integration pattern.

### Opportunities

OpenCode's architecture offers integration paths that Claude Code doesn't:

1. **Headless server mode** — `opencode serve` runs a headless API server. Agents could potentially be driven via HTTP API instead of tmux send-keys, bypassing TUI interaction entirely.
2. **Per-agent permissions in config** — OpenCode's `permission` block per agent aligns well with muxcode's tool profile concept. Permissions can be pre-configured in `opencode.json` without runtime flags.
3. **Custom agents in markdown** — OpenCode's `.opencode/agents/*.md` format is similar to muxcode's `agents/*.md`. The agent definition could be shared or adapted.
4. **`opencode run` with `--continue`** — non-interactive mode with session continuation could enable a bus-driven interaction model without TUI interaction.
5. **Multi-provider** — OpenCode supports Anthropic, OpenAI, Google, Groq, Bedrock, etc. An agent using OpenCode could use any provider.

## Design

### Provider interface

A new `Provider` abstraction in `bus/provider.go` that encapsulates all CLI-specific behavior:

```go
type Provider interface {
    // Name returns the provider identifier (e.g. "claude", "opencode")
    Name() string

    // BuildExecArgs constructs the CLI invocation for launching an agent.
    // Returns the binary path and argument list.
    BuildExecArgs(cfg *LaunchConfig) (string, []string)

    // IdlePrompt returns the string pattern that indicates the agent is idle.
    // Empty string means idle detection is not supported.
    IdlePrompt() string

    // DetectIdle checks if the agent pane is idle given captured pane content.
    DetectIdle(paneContent string) bool

    // SendMessage sends a text message to the agent's input.
    // For line-based CLIs: send-keys + Enter.
    // For TUI CLIs: enter editor mode, type, submit.
    // For API-driven: HTTP POST to server endpoint.
    SendMessage(session, paneTarget, message string) error

    // AcceptStartup handles any startup prompts (trust dialogs, permission prompts).
    // Returns true when the agent is ready for input.
    AcceptStartup(session, paneTarget string) (ready bool, err error)

    // CompactSession triggers session compaction if supported.
    CompactSession(session, paneTarget string) (supported bool, err error)

    // SupportsHooks returns whether this provider supports Pre/PostToolUse hooks.
    SupportsHooks() bool

    // SupportsToolPermissions returns whether tool permissions can be pre-configured.
    SupportsToolPermissions() bool

    // WriteAgentConfig writes provider-specific agent configuration to the project.
    // For Claude Code: agent definition in .claude/agents/
    // For OpenCode: agent config in .opencode/agents/ + opencode.json permissions
    WriteAgentConfig(cfg *LaunchConfig, projectDir string) error

    // SystemPromptMethod returns how the provider receives system prompts:
    // "flag" (CLI flag), "file" (reads a file), "config" (in config file)
    SystemPromptMethod() string

    // MemoryFile returns the filename the provider reads for project context
    // (e.g. "CLAUDE.md", "AGENTS.md")
    MemoryFile() string
}
```

### Provider implementations

**Claude Code provider** (`bus/provider_claude.go`):
- Wraps all existing behavior — no functional change
- `BuildExecArgs`: current `--agent`, `--agents`, `--append-system-prompt`, `--allowedTools`, `--model` flag construction
- `IdlePrompt`: `"❯"`
- `DetectIdle`: match `❯` in last 8 lines of pane capture
- `SendMessage`: `tmux send-keys` text + Enter (with 100ms delay)
- `AcceptStartup`: detect "trust this folder" / "Bypass Permissions", dismiss via send-keys
- `CompactSession`: inject `/compact` via send-keys
- `SupportsHooks`: `true`
- `SupportsToolPermissions`: `true`
- `WriteAgentConfig`: write to `.claude/agents/`
- `SystemPromptMethod`: `"flag"`
- `MemoryFile`: `"CLAUDE.md"`

**OpenCode provider** (`bus/provider_opencode.go`):

Two interaction modes, selected via `MUXCODE_OPENCODE_MODE`:

**Mode 1: TUI mode** (default) — OpenCode runs as interactive TUI in the tmux pane:
- `BuildExecArgs`: `opencode --agent <role>` with config in `opencode.json`
- `IdlePrompt`: `""` (TUI — cannot reliably detect via pane capture)
- `DetectIdle`: `false` (always treated as active)
- `SendMessage`: TUI key sequence — focus input, type, submit
- `AcceptStartup`: handle "Initialize Project" dialog if present
- `CompactSession`: auto-compact is configured in `opencode.json` (`compaction.auto: true`)
- `SupportsHooks`: `false`
- `SupportsToolPermissions`: `true` (via `permission` blocks in agent config)
- `WriteAgentConfig`: write to `.opencode/agents/<role>.md` with permissions in frontmatter
- `SystemPromptMethod`: `"file"` (agent markdown body is the system prompt)

**Mode 2: Server mode** — OpenCode runs as headless API server, muxcode drives it via HTTP:
- `BuildExecArgs`: `opencode serve --port <port>` in one pane, TUI or log view in the other
- `SendMessage`: HTTP POST to `localhost:<port>` API endpoint
- `DetectIdle`: HTTP health check or session status endpoint
- More reliable than TUI interaction but requires exploring the API surface

### Per-role provider configuration

Extend the existing `MUXCODE_{ROLE}_CLI` env var pattern:

```bash
# Use Claude Code for all agents (default)
MUXCODE_AGENT_CLI=claude

# Use OpenCode for the designer agent only
MUXCODE_DESIGNER_CLI=opencode

# Use OpenCode for build and test agents
MUXCODE_BUILD_CLI=opencode
MUXCODE_TEST_CLI=opencode
```

Provider resolved per-role in `ResolveLaunchConfig()`:

```go
func resolveProvider(role string) Provider {
    cli := roleCliVar(role)
    if cli == "" {
        cli = os.Getenv("MUXCODE_AGENT_CLI")
    }
    if cli == "" {
        cli = "claude"
    }
    switch cli {
    case "opencode":
        return &OpenCodeProvider{}
    case "local":
        return &HarnessProvider{}
    default:
        return &ClaudeProvider{}
    }
}
```

### Agent config generation

When launching an OpenCode agent, muxcode generates the agent config in OpenCode's format:

**`.opencode/agents/<role>.md`**:
```markdown
---
description: Build agent — runs build commands and reports results
mode: primary
model: anthropic/claude-sonnet-4-20250514
permission:
  bash:
    "*": allow
    "git *": deny
  edit: allow
---

You are the build agent in a muxcode multi-agent session...
[assembled system prompt content]
```

**`opencode.json`** (per-agent overrides):
```json
{
  "agent": {
    "build": {
      "description": "Build agent",
      "model": "anthropic/claude-sonnet-4-20250514",
      "permission": {
        "bash": { "*": "allow", "git *": "deny" },
        "edit": "allow"
      }
    }
  },
  "compaction": { "auto": true }
}
```

Muxcode's tool profiles (`bus/profile.go`) are translated to OpenCode's permission format:
- `Bash(muxcode *)` → `"muxcode *": "allow"` in bash permission
- `Read(*)` → read tool implicitly allowed (no permission needed in OpenCode)
- `Write(*)`, `Edit(*)` → `"edit": "allow"`

### Graceful degradation

When a provider doesn't support a feature, muxcode degrades gracefully:

| Feature | Claude Code | OpenCode | Degradation |
|---------|-------------|----------|-------------|
| Build/test/review chains | Hook-driven | No hooks | Chains disabled for that agent; system prompt instructs agent to send bus messages after commands |
| Edit guard | PreToolUse hook blocks commands | No hooks | Guard disabled; rely on OpenCode permission `deny` rules |
| Workflow state transitions | Hooks fire transitions | No hooks | State transitions skipped for that agent |
| Idle detection | `❯` prompt match | TUI (not detectable) | Agent always treated as "active"; display-message notifications only |
| Wake-up notifications | Send-keys "You have new messages" | TUI interaction unreliable | Notification skipped; system prompt instructs periodic inbox polling |
| Auto-accept | Dismiss Claude Code startup prompts | Different/no startup prompts | Provider-specific startup handling |
| Tool permissions | `--allowedTools` patterns | Per-agent `permission` blocks | Translated from tool profiles to OpenCode format |

### Hook replacement for non-hook providers

For providers without hooks, the system prompt instructs agents to replicate chain behavior via bus messages:

```
After completing a build command, run: muxcode send edit build-complete "Build finished with exit code $?"
After completing a test command, run: muxcode send edit test-complete "Tests finished with exit code $?"
When you finish a task, run: muxcode inbox --peek to check for new messages.
```

This is best-effort — the LLM may not always follow instructions — but provides a reasonable fallback.

### Notify behavior per provider

```go
func Notify(session, role, message string) {
    provider := resolveProvider(role)

    if provider.DetectIdle(capturedPane) {
        provider.SendMessage(session, paneTarget, message)
    } else {
        notifyDisplayMessage(session, role, message)
    }
}
```

For OpenCode TUI mode (`DetectIdle` returns false), notifications are always passive display-message.

## Implementation

### Phase 0: provider interface and Claude Code extraction

Extract current Claude Code behavior into the provider interface without changing any functionality.

New files:

| File | Purpose |
|------|---------|
| `bus/provider.go` | `Provider` interface definition |
| `bus/provider_claude.go` | Claude Code provider — wraps all existing behavior |
| `bus/provider_test.go` | Interface contract tests |

Updated files:

| File | Change |
|------|--------|
| `bus/launch.go` | `ResolveLaunchConfig` uses provider to build exec args |
| `bus/launcher.go` | `AutoAccept` delegates to provider |
| `bus/notify.go` | `Notify` / `IsAgentIdle` delegates to provider |
| `cmd/compact.go` | Compact delegates to provider |
| `bus/agent_health.go` | Idle/alive detection delegates to provider |

Success criteria:
- [ ] All existing behavior unchanged (pure refactor)
- [ ] Claude Code provider passes all existing tests
- [ ] Provider resolved per-role via `MUXCODE_{ROLE}_CLI` env var
- [ ] Provider interface covers: exec args, idle detection, send message, startup, compact, hooks, permissions, agent config

### Phase 1: OpenCode provider (TUI mode)

New files:

| File | Purpose |
|------|---------|
| `bus/provider_opencode.go` | OpenCode provider implementation (TUI mode) |
| `bus/provider_opencode_test.go` | Unit tests |

Success criteria:
- [ ] `MUXCODE_DESIGNER_CLI=opencode` launches `opencode` in the agent pane
- [ ] Agent config generated in `.opencode/agents/<role>.md` with correct frontmatter
- [ ] Tool profiles translated to OpenCode `permission` format
- [ ] System prompt written as agent markdown body
- [ ] Notifications degrade to display-message only
- [ ] Agent health check works (process alive detection)

### Phase 2: graceful degradation

Updated files:

| File | Change |
|------|--------|
| `cmd/hook.go` | Chain triggers check `provider.SupportsHooks()` — skip if false |
| `bus/hook.go` | Guard check skips for non-hook providers |
| `watcher/watcher.go` | Notification path uses provider |
| `bus/workflow.go` | Workflow transitions gated by provider capability |
| `bus/prompt.go` | Shared prompt includes inbox polling instructions for non-hook providers |

Success criteria:
- [ ] Agents with OpenCode provider run without errors when hooks are absent
- [ ] Build/test/review chains disabled for non-hook agents
- [ ] Edit guard disabled for non-hook agents
- [ ] System prompt includes bus polling instructions for non-hook providers
- [ ] OpenCode `permission.bash` deny rules replace edit guard for dangerous commands

### Phase 3: mixed-provider session testing

Success criteria:
- [ ] Session with mixed providers (Claude Code edit + OpenCode designer) works end-to-end
- [ ] F1 toggle between edit (Claude Code) and designer (OpenCode) works
- [ ] Bus messaging works between providers (edit sends to designer, designer replies)
- [ ] Session compact works for Claude Code agents, auto-compact handles OpenCode agents
- [ ] `muxcode status` shows provider per agent
- [ ] Config files don't conflict (`.claude/` and `.opencode/` coexist)

### Phase 4: server mode (future)

Explore OpenCode's `opencode serve` headless API as an alternative to TUI interaction.

Success criteria:
- [ ] `MUXCODE_OPENCODE_MODE=server` launches OpenCode in headless mode
- [ ] Messages sent via HTTP API instead of tmux send-keys
- [ ] Idle detection via API health/status endpoint
- [ ] More reliable than TUI interaction for unattended agents

## Configuration

### Environment variables

| Env var | Default | Description |
|---------|---------|-------------|
| `MUXCODE_AGENT_CLI` | `claude` | Default AI CLI for all agents |
| `MUXCODE_{ROLE}_CLI` | (falls back to default) | Per-role AI CLI override (`claude`, `opencode`, `local`) |
| `MUXCODE_OPENCODE_MODE` | `tui` | OpenCode interaction mode (`tui`, `server`) |
| `MUXCODE_OPENCODE_PORT` | `4096` | Port for OpenCode server mode |

### install.sh changes

Add OpenCode to the optional tools check:

```bash
# --- Check optional: OpenCode ---
if command -v opencode >/dev/null 2>&1; then
  ok "opencode found (alternative AI CLI available)"
else
  info "opencode not found (optional — install from https://opencode.ai/)"
fi
```

## Dependencies

| Dependency | Purpose | Install |
|------------|---------|---------|
| OpenCode | Alternative AI CLI | `curl -fsSL https://opencode.ai/install \| bash` or `brew install opencode` |

## Open questions

1. **TUI interaction reliability** — sending keystrokes to OpenCode's TUI via tmux is timing-dependent. The server mode API may be more reliable but needs API surface exploration. Which should be the primary integration path?
2. **Hook replacement completeness** — system prompt instructions ("after build, send bus message") are best-effort. Should we accept this limitation or explore OpenCode's custom command system as a hook alternative?
3. **Config file conflicts** — a project could have both `.claude/` and `.opencode/` directories. Should muxcode manage both, or should the user maintain them separately?
4. **OpenCode version compatibility** — OpenCode is actively developed (741 releases). Should we pin to a minimum version or track the latest?
5. **Provider per-window vs per-session** — current design allows per-role provider selection. Should we also support changing providers mid-session (e.g. `muxcode provider set designer opencode`)?
