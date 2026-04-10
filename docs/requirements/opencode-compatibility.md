# OpenCode compatibility

Enable swapping Claude Code with [OpenCode](https://opencode.ai/) (github.com/anomalyco/opencode) on a per-agent basis. Each tmux window/tab runs one agent, and each agent can independently use either Claude Code or OpenCode as its AI CLI. A single muxcode session can mix providers — e.g. Claude Code for the edit agent (full hook support) and OpenCode for the build agent (multi-provider LLM access). Provider assignment is per-agent at launch time via environment variables. This requires abstracting the coupling points between muxcode and Claude Code behind a provider interface.

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

1. **TUI as autonomous agent** — OpenCode's TUI manages its own context, tools, and compaction. Agents can be more autonomous, reducing muxcode's orchestration burden for non-critical roles.
2. **Per-agent permissions in config** — OpenCode's `permission` block per agent aligns well with muxcode's tool profile concept. Permissions can be pre-configured in `opencode.json` without runtime flags.
3. **Custom agents in markdown** — OpenCode's `.opencode/agents/*.md` format is similar to muxcode's `agents/*.md`. The agent definition could be shared or adapted.
4. **Multi-provider** — OpenCode supports Anthropic, OpenAI, Google, Groq, Bedrock, etc. An agent using OpenCode could use any provider.
5. **Future server mode** — `opencode serve` runs a headless API server. If programmatic control is needed later, server mode can be added as an optional enhancement (Phase 5).

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

TUI mode — OpenCode runs as an interactive TUI in the tmux pane, the same way the beta window works today. The agent is a full TUI session that the user can interact with directly. Muxcode treats it as a semi-autonomous agent with graceful degradation:

- `BuildExecArgs`: bare `opencode` binary (no flags) — launches TUI in the tmux pane
- `IdlePrompt`: `""` (TUI — cannot reliably detect idle state via pane capture)
- `DetectIdle`: `false` (always treated as active — TUI has no stable prompt character)
- `SendMessage`: tmux `display-message` notification only (best-effort — stdin piping breaks the TUI, github issue #3871)
- `AcceptStartup`: detect TUI frame via box-drawing characters (─, │, ╭, ╰); no startup prompts to dismiss
- `CompactSession`: no-op (TUI manages its own context and auto-compacts)
- `SupportsHooks`: `false`
- `SupportsToolPermissions`: `true` (via `permission` blocks in agent config)
- `WriteAgentConfig`: write to `.opencode/agents/<role>.md` with permissions in frontmatter
- `SystemPromptMethod`: `"file"` (agent markdown body is the system prompt)
- `MemoryFile`: `"AGENTS.md"`

The TUI approach means OpenCode agents are more autonomous than Claude Code agents — they manage their own context window, compaction, and tool execution. Muxcode's role is limited to: launching the TUI, detecting whether it's alive, and providing configuration (agent definitions, permissions, system prompts). Hook-driven chains and idle-based wake-up notifications are handled via graceful degradation (see below).

### Per-agent provider assignment

Each tmux window runs one agent, and each agent independently resolves its AI CLI provider. This means a single session can have Claude Code in one tab and OpenCode in another — the bus handles cross-provider messaging transparently.

**Resolution order** (first non-empty wins):

1. `MUXCODE_{ROLE}_CLI` — per-agent override (e.g. `MUXCODE_BUILD_CLI=opencode`)
2. `MUXCODE_AGENT_CLI` — session-wide default
3. `claude` — built-in fallback

**Examples:**

```bash
# All agents use Claude Code (default — no config needed)
muxcode

# All agents use OpenCode
MUXCODE_AGENT_CLI=opencode muxcode

# Mixed session: edit stays on Claude Code, build + test use OpenCode
MUXCODE_BUILD_CLI=opencode MUXCODE_TEST_CLI=opencode muxcode

# Mixed session via config file (~/.config/muxcode/config):
#   MUXCODE_AGENT_CLI=claude
#   MUXCODE_BUILD_CLI=opencode
#   MUXCODE_TEST_CLI=opencode
#   MUXCODE_BETA_CLI=opencode
muxcode
```

**How it maps to tmux tabs:**

| F-key | Window | Role | Default CLI | Override env var |
|-------|--------|------|-------------|-----------------|
| F1 | edit | edit | claude | `MUXCODE_EDIT_CLI` |
| F2 | build | build | claude | `MUXCODE_BUILD_CLI` |
| F3 | test | test | claude | `MUXCODE_TEST_CLI` |
| F4 | review | review | claude | `MUXCODE_REVIEW_CLI` |
| F5 | deploy | deploy | claude | `MUXCODE_DEPLOY_CLI` |
| F6 | analyze | analyze | claude | `MUXCODE_ANALYZE_CLI` |
| F7 | commit | commit | claude | `MUXCODE_COMMIT_CLI` |
| F8 | watch | watch | claude | `MUXCODE_WATCH_CLI` |
| F10 | beta | beta | opencode | `MUXCODE_BETA_CLI` |

Each agent resolves independently — the bus doesn't care which CLI runs in the pane, only that the agent reads its inbox and replies via `muxcode send`.

**Provider resolution in Go:**

```go
func resolveProvider(role string) Provider {
    cli := roleCliVar(role)  // MUXCODE_{ROLE}_CLI
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

**Constraints:**
- Provider is fixed for the session lifetime — no mid-session switching (see resolved question #5)
- The edit agent should remain on Claude Code for full hook support (chains, guard, workflow state)
- OpenCode agents degrade gracefully on hook-dependent features (see graceful degradation)

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

| Feature | Claude Code | OpenCode (TUI) | Degradation |
|---------|-------------|----------------|-------------|
| Build/test/review chains | Hook-driven | No hooks | Chains disabled for that agent; system prompt instructs agent to send bus messages after commands |
| Edit guard | PreToolUse hook blocks commands | No hooks | Guard disabled; rely on OpenCode permission `deny` rules |
| Workflow state transitions | Hooks fire transitions | No hooks | State transitions skipped for that agent |
| Idle detection | `❯` prompt match | Not detectable (TUI has no stable prompt) | Always treated as "active" — watcher skips idle-based notifications |
| Wake-up notifications | Send-keys "You have new messages" | Display-message only (best-effort) | User sees tmux status bar flash; agent does not auto-process |
| Auto-accept | Dismiss Claude Code startup prompts | TUI frame detection (box-drawing chars) | Provider-specific startup handling |
| Tool permissions | `--allowedTools` patterns | Per-agent `permission` blocks | Translated from tool profiles to OpenCode format |
| Compact | `/compact` via send-keys | No-op (TUI auto-compacts) | TUI manages its own context window |

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

    // Provider handles wake-up in its own way
    provider.SendWakeUp(session, role)
}
```

- **Claude Code**: `SendWakeUp` injects "You have new messages" via `tmux send-keys` when idle at `❯` prompt; falls back to `display-message`.
- **OpenCode TUI**: `SendWakeUp` sends `tmux display-message` only — the user sees a status bar flash but the TUI agent does not auto-process. This is by design: TUI agents are user-driven, not watcher-driven.

## Implementation

### Phase 0a: install.sh provider selection ✅

Replace the current hard requirement on `claude` with an interactive AI CLI provider selection flow. The user chooses which providers to install and configure during `install.sh`. At least one provider must be selected. This must land first so that OpenCode is available on the system before any subsequent phases.

Updated files:

| File | Change |
|------|--------|
| `install.sh` | Replace `claude` prerequisite with interactive provider selection; add OpenCode detection, install, version check, default provider prompt, config generation |

#### Provider selection prompt

After checking core prerequisites (tmux, go, jq, nvim, fzf), present a provider selection menu:

```bash
# --- AI CLI provider selection ---
echo ""
info "Which AI CLI providers do you want to use?"
echo ""
echo "  MuxCode supports multiple AI CLI providers. Each agent window can use"
echo "  a different provider. Select all providers you want available."
echo ""

# Detect what's already installed
# Add known install locations to PATH (non-interactive shells don't source .bashrc)
[ -d "$HOME/.opencode/bin" ] && [[ ":$PATH:" != *":$HOME/.opencode/bin:"* ]] && export PATH="$HOME/.opencode/bin:$PATH"

has_claude=false
has_opencode=false
command -v claude   >/dev/null 2>&1 && has_claude=true
command -v opencode >/dev/null 2>&1 && has_opencode=true

# Claude Code
if $has_claude; then
  ok "Claude Code found ($(claude --version 2>/dev/null || echo 'version unknown'))"
  use_claude=true
else
  read -rp "  Install Claude Code? (recommended) [Y/n] " ans
  if [[ ! "$ans" =~ ^[Nn]$ ]]; then
    use_claude=true
    info "Installing Claude Code..."
    if npm install -g @anthropic-ai/claude-code 2>/dev/null; then
      ok "Claude Code installed"
    elif brew install claude-code 2>/dev/null; then
      ok "Claude Code installed via Homebrew"
    else
      warn "Auto-install failed. Install manually:"
      echo "    npm install -g @anthropic-ai/claude-code"
      echo "    — or —"
      echo "    brew install claude-code"
      echo ""
      read -rp "  Continue without Claude Code? [y/N] " skip_ans
      [[ "$skip_ans" =~ ^[Yy]$ ]] && use_claude=false || fail "Install Claude Code and re-run"
    fi
  else
    use_claude=false
  fi
fi

# OpenCode
if $has_opencode; then
  oc_version=$(opencode --version 2>/dev/null || echo "version unknown")
  ok "OpenCode found ($oc_version)"
  # Version check — require >= 1.4.0
  oc_major=$(echo "$oc_version" | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' | head -1)
  if [ -n "$oc_major" ]; then
    oc_minor=$(echo "$oc_major" | cut -d. -f2)
    if [ "${oc_minor:-0}" -lt 4 ]; then
      warn "OpenCode $oc_major found but >= 1.4.0 required (server API support)"
      warn "Update with: curl -fsSL https://opencode.ai/install | bash"
    fi
  fi
  use_opencode=true
else
  read -rp "  Install OpenCode? (alternative AI CLI) [y/N] " ans
  if [[ "$ans" =~ ^[Yy]$ ]]; then
    use_opencode=true
    info "Installing OpenCode..."
    if curl -fsSL https://opencode.ai/install | bash 2>/dev/null; then
      ok "OpenCode installed"
    elif brew install opencode 2>/dev/null; then
      ok "OpenCode installed via Homebrew"
    else
      warn "Auto-install failed. Install manually:"
      echo "    curl -fsSL https://opencode.ai/install | bash"
      echo "    — or —"
      echo "    brew install opencode"
      echo ""
      read -rp "  Continue without OpenCode? [y/N] " skip_ans
      [[ "$skip_ans" =~ ^[Yy]$ ]] && use_opencode=false || fail "Install OpenCode and re-run"
    fi
  else
    use_opencode=false
  fi
fi

# Validate: at least one provider selected
if ! $use_claude && ! $use_opencode; then
  fail "At least one AI CLI provider is required. Re-run and select Claude Code or OpenCode."
fi
```

#### Provider-specific configuration

After the build step, configure each selected provider:

**Claude Code** (existing behavior, gated by `$use_claude`):
- Merge hooks/permissions into `~/.claude/settings.json` (current logic, unchanged)

**OpenCode** (new, gated by `$use_opencode`):
```bash
if $use_opencode; then
  info "Configuring OpenCode..."

  # OpenCode provider config — API key setup
  if [ -z "${ANTHROPIC_API_KEY:-}" ] && [ -z "${OPENAI_API_KEY:-}" ]; then
    echo ""
    info "OpenCode needs at least one API key configured."
    echo "  Set one of these in your shell profile or .env:"
    echo "    export ANTHROPIC_API_KEY=sk-ant-..."
    echo "    export OPENAI_API_KEY=sk-..."
    echo ""
    warn "No API key detected — OpenCode agents will fail until configured"
  else
    ok "API key found for OpenCode"
  fi

  ok "OpenCode configured"
fi
```

#### Default provider selection

If both providers are installed, ask which should be the default:

```bash
if $use_claude && $use_opencode; then
  echo ""
  info "Both Claude Code and OpenCode are available."
  echo "  Which should be the default for new agent windows?"
  echo ""
  echo "    1) Claude Code (recommended — full hook support)"
  echo "    2) OpenCode (TUI mode — multi-provider LLM support)"
  echo ""
  read -rp "  Default provider [1]: " default_choice
  if [[ "$default_choice" == "2" ]]; then
    default_cli="opencode"
    info "Default: OpenCode (override per-role with MUXCODE_{ROLE}_CLI=claude)"
  else
    default_cli="claude"
    info "Default: Claude Code (override per-role with MUXCODE_{ROLE}_CLI=opencode)"
  fi
elif $use_opencode; then
  default_cli="opencode"
else
  default_cli="claude"
fi
```

#### Config file generation

Write the default provider to the muxcode config file:

```bash
CONFIG_FILE="$HOME/.config/muxcode/config"
if [ -f "$CONFIG_FILE" ]; then
  # Update existing config — set MUXCODE_AGENT_CLI if not already present
  if ! grep -q '^MUXCODE_AGENT_CLI=' "$CONFIG_FILE"; then
    echo "" >> "$CONFIG_FILE"
    echo "# Default AI CLI provider (claude, opencode, local)" >> "$CONFIG_FILE"
    echo "MUXCODE_AGENT_CLI=$default_cli" >> "$CONFIG_FILE"
    ok "Added MUXCODE_AGENT_CLI=$default_cli to config"
  fi
else
  mkdir -p "$(dirname "$CONFIG_FILE")"
  cat > "$CONFIG_FILE" << EOF
# MuxCode configuration
# See: docs/configuration.md

# Default AI CLI provider (claude, opencode, local)
MUXCODE_AGENT_CLI=$default_cli

# Per-role overrides (uncomment to customize):
# MUXCODE_BUILD_CLI=opencode
# MUXCODE_TEST_CLI=opencode
# MUXCODE_BETA_CLI=opencode
EOF
  ok "Created config at $CONFIG_FILE"
fi
```

#### Updated prerequisites check

The `claude` binary moves from required to conditional — it's required only if no other provider is available:

```bash
# Core prerequisites (always required)
missing=()
command -v tmux   >/dev/null 2>&1 || missing+=("tmux (>= 3.0)")
command -v go     >/dev/null 2>&1 || missing+=("go (>= 1.22)")
command -v jq     >/dev/null 2>&1 || missing+=("jq")
command -v nvim   >/dev/null 2>&1 || missing+=("nvim")
command -v fzf    >/dev/null 2>&1 || missing+=("fzf")
# Note: claude is no longer in the required list — handled by provider selection
```

#### Next steps output

Update the final "Next steps" banner to reflect the installed providers:

```bash
echo ""
echo -e "${GREEN}Installation complete!${NC}"
echo ""
echo "Installed providers:"
$use_claude   && echo "  ✓ Claude Code (default: $( [[ $default_cli == claude ]] && echo 'yes' || echo 'no' ))"
$use_opencode && echo "  ✓ OpenCode (default: $( [[ $default_cli == opencode ]] && echo 'yes' || echo 'no' ))"
echo ""
echo "Next steps:"
echo ""
echo "  1. Edit your config (optional):"
echo "     \$EDITOR ~/.config/muxcode/config"
echo ""
echo "  2. Launch a session:"
echo "     muxcode"
echo ""
if $use_opencode; then
  echo "  3. Switch beta window to OpenCode:"
  echo "     MUXCODE_BETA_CLI=opencode muxcode"
  echo ""
fi
```

Success criteria:
- [x] `claude` no longer a hard prerequisite — interactive provider selection instead
- [x] Detects existing Claude Code and OpenCode installs
- [x] Offers to install missing providers (npm/brew for Claude Code, curl/brew for OpenCode)
- [x] OpenCode version check (>= 1.4.0) with upgrade guidance
- [x] Default provider prompt when both are available
- [x] Writes `MUXCODE_AGENT_CLI` to `~/.config/muxcode/config`
- [x] Claude Code hooks merge gated by `$use_claude`
- [x] At least one provider required — fails if none selected
- [x] Skips OpenCode install prompt when Claude Code already available (no re-ask on repeat runs)
- [x] Adds `~/.opencode/bin` to script PATH before detection (non-interactive shells don't source `.bashrc`)

### Phase 0b: testbed window (F10) ✅

Add a dedicated `beta` tmux window at position 10, bound to F10. Runs Claude Code initially (standard agent launch) so existing functionality is unaffected. Phase 0c adds default routing so the beta window launches the OpenCode binary without requiring explicit env var overrides.

> **Note**: Originally named `opencode`, renamed to `beta` to reflect its broader purpose as a testbed for new agents and features — not limited to OpenCode.

New files:

| File | Purpose |
|------|---------|
| `agents/beta-agent.md` | Agent definition — beta testbed role for new agents/features |

Updated files:

| File | Change |
|------|--------|
| `config/tmux.conf` | Add `bind -n F10 select-window -t:10` and update window order comment |
| `muxcode.sh` | Add `beta` to `WINDOWS` default list and `SPLIT_LEFT` list |
| `scripts/muxcode-agent.sh` | Add `beta` case to `agent_name()` (→ `beta-agent`), `role_cli_var()` (→ `MUXCODE_BETA_CLI`), `role_model_var()` (→ `MUXCODE_BETA_MODEL`) |
| `tools/muxcode/bus/config.go` | Add `"beta"` to `KnownRoles`, add to `splitLeftWindows` |
| `tools/muxcode/bus/launch.go` | Add `"beta"` case to `AgentFileName()` (→ `beta-agent`), `RoleCLIEnvVar()` (→ `MUXCODE_BETA_CLI`), `RoleClaudeModelEnvVar()` (→ `MUXCODE_BETA_CLAUDE_MODEL`), `RoleClaudeModelDefault()` (→ `claude-sonnet-4-5`) |
| `tools/muxcode/bus/launcher.go` | Add `"beta"` to `DefaultLauncherConfig()` `Windows` and `SplitLeft` lists; add `"beta"` to `HasConsoleView()` switch case so the left pane runs `muxcode console beta` |
| `tools/muxcode/bus/launcher_test.go` | Add `"beta"` to `TestHasConsoleView` expected list |
| `tools/muxcode/bus/hook.go` | Extend `CmdUnknown` block in `ProcessBashHook` to log history for `beta` role (alongside `run`) — writes command, exit code, and output to `beta-history.jsonl` for console display |
| `tools/muxcode/bus/profile.go` | Add `beta` tool profile in `DefaultConfig()` — include bus/readonly/common groups, plus `Bash(opencode *)`, `Bash(curl *)`, `Bash(jq *)`, `Write`, `Edit` |
| `tools/muxcode/bus/console.go` | Add `beta` console config entry using `renderDeployRunner` (shows command, exit code, summary, output) |

Success criteria:
- [x] `muxcode.sh` creates window 10 named `beta` with split-left layout
- [x] Go launcher (`launcher.go`) includes `beta` in default `Windows` and `SplitLeft`
- [x] F10 switches to the beta window
- [x] Agent launches with Claude Code (default) and responds to bus messages
- [x] `muxcode status` shows the beta agent
- [x] Beta window has a console view (left pane) showing command history via `renderDeployRunner`
- [x] Hook history logging records beta commands to `beta-history.jsonl` for console display
- [x] No impact on existing windows 1-9

### Phase 0c: OpenCode default routing ✅

The beta role should default to the `opencode` binary (standalone TUI) without requiring an explicit `MUXCODE_BETA_CLI=opencode` env var. Phase 0b launched Claude Code in the beta window because `ResolveLaunchConfig` fell through to the global `MUXCODE_AGENT_CLI` default (`claude`). This phase adds role-specific routing so the beta window launches OpenCode out of the box.

Updated files:

| File | Change |
|------|--------|
| `tools/muxcode/bus/launch.go` | Add `IsBetaCLI` field to `LaunchConfig`; add beta routing block in `ResolveLaunchConfig` — when `role == "beta"`, default CLI to `opencode` binary and short-circuit Claude-specific config (model flags, tool profiles, agent JSON, permissions, shared prompt); add `buildBetaCLIExecArgs()` method returning bare binary with no flags; add beta path in `BuildExecArgs()` dispatch |
| `tools/muxcode/bus/agent_health.go` | Add `"opencode"` and `"beta"` to pane capture alive detection (alongside `"muxcode-agent"` and `"claude"`) |

#### Beta window routing logic

```go
// In ResolveLaunchConfig, after local LLM check:
if role == "beta" {
    if roleCLI == "" {
        cfg.CLI = "opencode"
    } else {
        cfg.CLI = roleCLI
    }
    if cfg.CLI != "claude" {
        cfg.IsBetaCLI = true
        return cfg
    }
}
```

**Resolution for the beta role:**
- `MUXCODE_BETA_CLI` unset → defaults to `opencode` binary, `IsBetaCLI=true`, returns early (no Claude flags)
- `MUXCODE_BETA_CLI=opencode` → same as above
- `MUXCODE_BETA_CLI=claude` → falls through to Claude Code config (user explicitly wants Claude Code in the beta window)
- `MUXCODE_BETA_CLI=local` → caught earlier by the local LLM routing check

**`BuildExecArgs` dispatch:**
```go
func (c *LaunchConfig) BuildExecArgs() (string, []string) {
    if c.IsLocal {
        return c.buildLocalExecArgs()
    }
    if c.IsBetaCLI {
        return c.buildBetaCLIExecArgs()
    }
    return c.buildClaudeExecArgs()
}

func (c *LaunchConfig) buildBetaCLIExecArgs() (string, []string) {
    return c.CLI, nil
}
```

Success criteria:
- [x] `muxcode agent launch beta` launches the `opencode` binary (not Claude Code) by default
- [x] OpenCode TUI renders in the F10 window pane
- [x] `MUXCODE_BETA_CLI=claude` overrides back to Claude Code
- [x] Agent health check recognizes `"opencode"` and `"beta"` in pane capture as alive
- [x] No impact on other roles — all non-beta roles still resolve to Claude Code by default
- [x] Build passes (gofmt clean, no compile errors)
- [x] All existing tests pass

### Phase 0d: role mimicry and takeover for testbed window

The beta testbed window (F10) should be able to mimic or take over the role of another agent — e.g., act as the build agent with the build console on the left pane and OpenCode on the right. Two modes:

1. **Mimic mode** (default) — read-only: uses the mimicked role's console, prompt, tools, and context, but keeps its own `beta` bus identity. The real agent continues running. Good for observing how OpenCode handles a role's workload without disrupting the session.
2. **Takeover mode** — the beta window assumes the mimicked role's bus identity. The real agent for that role is stopped, and all bus messages for that role are delivered to the F10 window. This is the primary testing mode for verifying that OpenCode can receive and send bus messages as a real agent.

#### Configuration

| Variable | Default | Effect |
|----------|---------|--------|
| `MUXCODE_BETA_ROLE` | (unset) | Beta window runs as its own `beta` role — no console view, standalone TUI |
| `MUXCODE_BETA_ROLE=build` | — | Mimic mode: left pane shows the build console; agent gets the build role's shared prompt, tool profile, and context; bus identity stays `beta` |
| `MUXCODE_BETA_TAKEOVER=true` | `false` | Combined with `MUXCODE_BETA_ROLE`: enables takeover mode — stops the real agent and assumes its bus identity |

Any role with a console view can be mimicked: `build`, `test`, `review`, `deploy`, `run`, `watch`, `commit`, `analyze`.

#### Mimic mode behavior

When `MUXCODE_BETA_ROLE` is set and `MUXCODE_BETA_TAKEOVER` is unset or `false`:

1. **Left pane (console)**: runs `muxcode console <mimicked-role>` — shows that role's history, stats, and live updates.
2. **Right pane (agent)**: runs OpenCode (or Claude Code if `MUXCODE_BETA_CLI=claude`) with the mimicked role's:
   - Shared prompt (`muxcode prompt <mimicked-role>`)
   - Tool profile (`muxcode tools <mimicked-role>`)
   - Agent context files (`muxcode context prompt <mimicked-role>`)
   - Skills (`muxcode skill prompt <mimicked-role>`)
3. **Bus identity**: remains `beta` — messages sent to the mimicked role still go to the real agent.
4. **Console history**: the beta agent writes to its own history, not the mimicked role's.

#### Takeover mode behavior

When both `MUXCODE_BETA_ROLE=<role>` and `MUXCODE_BETA_TAKEOVER=true` are set:

1. **Stop real agent**: runs `muxcode agent-health --stop <role>` to gracefully stop the real agent and set its stopped marker (prevents auto-restart by the watcher).
2. **Assume bus identity**: the agent in the F10 pane sets `AGENT_ROLE=<mimicked-role>` so all bus operations (`muxcode send`, `muxcode inbox`, notifications) use the mimicked role's inbox and identity.
3. **Left pane (console)**: runs `muxcode console <mimicked-role>` — same as mimic mode.
4. **Right pane (agent)**: launches with the mimicked role's full config (prompt, tools, context, agent definition). The startup message is delivered to the mimicked role's inbox so the agent picks it up on boot.
5. **Message routing**: messages sent to the mimicked role (e.g. `muxcode send build build "Run ./build.sh"`) are delivered to the mimicked role's inbox, which the F10 agent is now reading. Replies from F10 carry the mimicked role as sender.
6. **Notification targeting**: the watcher's idle check and `Notify()` target the `beta` pane (F10) for the mimicked role — since the real agent's pane is stopped, the watcher's `PaneTarget` resolution must account for takeover.

#### Takeover lifecycle

```
┌─ User sets env vars ─────────────────────────────────────┐
│  MUXCODE_BETA_ROLE=build  MUXCODE_BETA_TAKEOVER=true     │
└──────────────────────────────────────────────────────────┘
                          │
                          ▼
┌─ Agent launch (muxcode agent launch beta) ───────────────┐
│  1. Read MUXCODE_BETA_ROLE → "build"                     │
│  2. Read MUXCODE_BETA_TAKEOVER → true                    │
│  3. muxcode agent-health --stop build (stop real agent)  │
│  4. Export AGENT_ROLE=build (bus identity override)       │
│  5. Resolve config as if role=build (prompt, tools, ctx)  │
│  6. Launch OpenCode/Claude in F10 pane                   │
└──────────────────────────────────────────────────────────┘
                          │
                          ▼
┌─ Testing ────────────────────────────────────────────────────┐
│  • edit agent sends: muxcode send build build "Run build"    │
│  • Message lands in build inbox → F10 agent picks it up      │
│  • F10 agent replies as "build" → edit receives response     │
│  • Left pane shows build console with history                │
└──────────────────────────────────────────────────────────────┘
                          │
                          ▼
┌─ Teardown (restore real agent) ──────────────────────────────┐
│  muxcode agent-health --start build                          │
│  (clears stopped marker, watcher auto-restarts real agent)   │
└──────────────────────────────────────────────────────────────┘
```

#### Restore command

After testing, restore the real agent:

```bash
# Stop the beta testbed (or let it keep running as standalone)
muxcode agent-health --stop beta

# Restart the real build agent
muxcode agent-health --start build
```

Or from the edit agent via bus:

```bash
muxcode send commit commit "Run: muxcode agent-health --start build"
```

A convenience alias could be added later:

```bash
muxcode testbed release build   # stops takeover, restarts real agent
```

#### Implementation

Updated files:

| File | Change |
|------|--------|
| `tools/muxcode/bus/launch.go` | Add `MimicRole` and `Takeover` fields to `LaunchConfig`; read `MUXCODE_BETA_ROLE` and `MUXCODE_BETA_TAKEOVER` in `ResolveLaunchConfig`; in takeover mode, set `Role` to the mimicked role (not `beta`) for bus identity; resolve agent config (prompt, tools, context, agent definition) using the mimicked role |
| `tools/muxcode/bus/launcher.go` | In `LaunchWindow`, when window is `beta` and `MUXCODE_BETA_ROLE` is set, pass the mimicked role to `HasConsoleView` check and `muxcode console` command for the left pane |
| `tools/muxcode/bus/config.go` | Add `MimicRoleEnvVar` and `TakeoverEnvVar` constants |
| `tools/muxcode/bus/agent_health.go` | In takeover mode, `PaneTarget` for the mimicked role resolves to the `beta` window's pane (so watcher notifications reach F10) |
| `scripts/muxcode-agent.sh` | Read `MUXCODE_BETA_ROLE` and `MUXCODE_BETA_TAKEOVER`; in takeover mode, run `agent-health --stop` on the real agent before launch, export `AGENT_ROLE=<mimicked-role>`, and use the mimicked role for `build_shared_prompt`, `build_flags`, and startup message |

**LaunchConfig changes:**

```go
type LaunchConfig struct {
    Role       string // Agent role — bus identity (mimicked role in takeover mode)
    MimicRole  string // Role to mimic (e.g. "build") — for prompt/tools/context
    Takeover   bool   // True if taking over the mimicked role's bus identity
    CLI        string
    IsLocal    bool
    IsBetaCLI  bool
    // ...
}
```

**ResolveLaunchConfig routing (in the beta block):**

```go
// --- Beta window routing ---
if role == "beta" {
    if roleCLI == "" {
        cfg.CLI = "opencode"
    } else {
        cfg.CLI = roleCLI
    }

    // Role mimicry / takeover
    if mimicRole := os.Getenv("MUXCODE_BETA_ROLE"); mimicRole != "" {
        cfg.MimicRole = mimicRole
        if os.Getenv("MUXCODE_BETA_TAKEOVER") == "true" {
            cfg.Takeover = true
            cfg.Role = mimicRole // bus identity becomes the mimicked role
        }
    }

    if cfg.CLI != "claude" {
        cfg.IsBetaCLI = true
        return cfg
    }
    // Falls through to Claude Code config using MimicRole for prompt/tools
}
```

**Pre-launch takeover (in PreLaunchSetup or muxcode-agent.sh):**

```bash
# Takeover: stop the real agent before launching
if [ "$MUXCODE_BETA_TAKEOVER" = "true" ] && [ -n "$MUXCODE_BETA_ROLE" ]; then
    muxcode agent-health --stop "$MUXCODE_BETA_ROLE" 2>/dev/null || true
    export AGENT_ROLE="$MUXCODE_BETA_ROLE"
fi
```

**Launcher left-pane console selection (same for both modes):**

```go
// In LaunchWindow, for split-left windows:
consoleRole := win
if win == "beta" {
    if mr := os.Getenv("MUXCODE_BETA_ROLE"); mr != "" && HasConsoleView(mr) {
        consoleRole = mr
    }
}
if HasConsoleView(consoleRole) {
    sendCommand(target, "muxcode console "+consoleRole)
}
```

**Shared prompt resolution uses MimicRole when set:**

```go
func (c *LaunchConfig) promptRole() string {
    if c.MimicRole != "" {
        return c.MimicRole
    }
    return c.Role
}
```

**Watcher PaneTarget override for takeover:**

```go
// When a role is taken over, the watcher needs to know that the
// mimicked role's agent is running in the beta pane, not its
// original pane.
func PaneTargetWithTakeover(session, role string) string {
    if isTakenOver(session, role) {
        return PaneTarget(session, "beta")
    }
    return PaneTarget(session, role)
}
```

The `isTakenOver` check reads a marker file (e.g. `takeover-{role}` in the bus lock dir) written during pre-launch setup and cleared on restore.

#### Constraints

- **Mimic mode**: beta keeps its own bus identity — never impersonates the mimicked role
- **Takeover mode**: beta assumes the mimicked role's bus identity — the real agent MUST be stopped first to avoid two agents reading the same inbox
- Console view is shared: in both modes, the left pane shows the mimicked role's console
- Only one role can be mimicked/taken over at a time (single env var)
- The mimicked role must have a console view (`HasConsoleView` returns true) for the left pane to show a console; otherwise the left pane is empty
- Takeover does not persist across session restarts — if muxcode is relaunched, the beta window returns to its default behavior (env vars must be re-set)
- OpenCode TUI mode (default) does not use shared prompts or tool profiles — mimic mode is most useful with `MUXCODE_BETA_CLI=claude`; takeover mode works with both OpenCode and Claude Code since bus identity is the key feature
- Restore is manual — the user must explicitly restart the real agent after testing

Success criteria:

**Mimic mode:**
- [ ] `MUXCODE_BETA_ROLE=build` shows the build console in the beta window's left pane
- [ ] Claude Code in the beta window (via `MUXCODE_BETA_CLI=claude MUXCODE_BETA_ROLE=build`) receives the build role's shared prompt, tool profile, and context
- [ ] OpenCode TUI launches normally when `MUXCODE_BETA_ROLE` is set (mimicry affects console only in TUI mode)
- [ ] The beta agent's bus identity remains `beta` — messages to `build` are not intercepted

**Takeover mode:**
- [ ] `MUXCODE_BETA_TAKEOVER=true` with `MUXCODE_BETA_ROLE=build` stops the real build agent via `agent-health --stop`
- [ ] The F10 agent reads from the build inbox and replies as `build`
- [ ] `muxcode send build build "Run ./build.sh"` is received by the F10 agent (not the stopped real agent)
- [ ] Watcher notifications for the mimicked role target the F10 pane
- [ ] `muxcode agent-health --start build` restores the real build agent after testing
- [ ] Takeover marker file is written on takeover and cleared on restore

**Both modes:**
- [ ] Console history from the beta agent does not leak into the mimicked role's history (mimic mode only — takeover mode writes to the mimicked role's history intentionally)
- [ ] Unset `MUXCODE_BETA_ROLE` preserves existing Phase 0c behavior (no console, standalone TUI)
- [ ] Build passes (gofmt clean, no compile errors)
- [ ] All existing tests pass

### Phase 1: provider interface and Claude Code extraction ✅

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

Design:

`Provider` interface in `bus/provider.go`:
```go
type Provider interface {
    Name() string
    ConfigureLaunch(cfg *LaunchConfig, role string)
    BuildExecArgs(cfg *LaunchConfig) (string, []string)
    IsIdle(session, role string) bool
    IsAlive(session, role string) bool
    ClassifyPane(content string) PaneState
    AcceptStartup(session, pane string, state PaneState) bool
    SendWakeUp(session, role string) error
    Compact(session, role, target string) error
    SupportsHooks() bool
    IdlePromptChar() string
    WriteAgentConfig(role string) error
}
```

Resolution via `ResolveProvider(role)` — checks `MUXCODE_{ROLE}_CLI` → `MUXCODE_AGENT_CLI` → `"claude"`. Beta defaults to `"opencode"` when no per-role override.

Three implementations:
- `ClaudeCodeProvider` — full implementation wrapping all existing behavior
- `OpenCodeProvider` — Phase 2 stub (bare binary launch, no idle/compact/hooks)
- `LocalProvider` — wraps existing harness logic

`LaunchConfig.BuildExecArgs()` delegates to `Provider.BuildExecArgs()` with legacy fallback for manually constructed configs (tests).

Success criteria:
- [x] All existing behavior unchanged (pure refactor)
- [x] Claude Code provider passes all existing tests (150+ tests pass)
- [x] Provider resolved per-role via `MUXCODE_{ROLE}_CLI` env var
- [x] Provider interface covers: exec args, idle detection, send message, startup, compact, hooks, permissions, agent config

### Phase 2: OpenCode provider (TUI mode) ✅

Upgrade the `OpenCodeProvider` stub (created in Phase 1 inside `bus/provider.go`) into a full TUI-based implementation. Phase 0c established bare TUI launch for the beta role — this phase extends TUI mode to all OpenCode roles and adds agent config generation, tool profile translation, and pane-based detection.

New files:

| File | Purpose |
|------|---------|
| `bus/provider_opencode.go` | Full OpenCode provider — moves stub from `provider.go`, implements TUI mode for all roles |
| `bus/provider_opencode_test.go` | Unit tests |

All OpenCode roles launch the bare `opencode` binary in TUI mode (same as beta):

| Operation | Implementation |
|-----------|---------------|
| Launch | bare `opencode` binary in tmux pane (TUI mode, no flags) |
| Alive detection | Pane capture — look for "opencode" text or box-drawing characters; shell prompt = dead |
| Idle detection | Not supported — TUI has no stable prompt character; always returns false |
| Wake-up | `tmux display-message` notification (best-effort, user-visible) |
| Startup detection | Box-drawing characters (─, │, ╭, ╰) in pane indicate TUI has rendered |
| Compact | No-op — TUI manages its own context and auto-compacts at 95% |
| Agent config | Pre-generated `.opencode/agents/<role>.md` with permissions and system prompt |

> **Note**: Phase 1 added `IsBetaCLI` to `LaunchConfig` for Phase 0c bare launch routing. Phase 2 removes it — all OpenCode launch logic moves into `OpenCodeProvider.BuildExecArgs()` and `ConfigureLaunch()`.

Updated files:

| File | Change |
|------|--------|
| `bus/provider.go` | Removed `OpenCodeProvider` stub (moved to dedicated file), removed unused `strings` import |
| `bus/launch.go` | Removed `IsBetaCLI` field from `LaunchConfig`, removed legacy `IsBetaCLI` dispatch in `BuildExecArgs()` |
| `bus/provider_test.go` | Removed stub-specific tests (replaced by `provider_opencode_test.go`) |

Design:

`OpenCodeProvider` in `bus/provider_opencode.go`:

**TUI launch** — all OpenCode roles launch the bare `opencode` binary with no flags. The TUI renders in the tmux pane and the user (or bus-initiated notifications) drives interaction. No server process, no HTTP API, no port management.

**Alive detection** — pane capture only. Looks for "opencode" text or box-drawing characters (TUI frame). If the pane shows a bare shell prompt (`$`, `%`, `❯`), the agent is dead. Indeterminate defaults to alive.

**Idle detection** — not supported. TUI has no stable prompt character that can be matched via pane capture. `IsIdle` always returns false, so the watcher treats OpenCode agents as always active and skips idle-based notifications.

**Wake-up notifications** — `tmux display-message` only. The TUI's input model doesn't support programmatic text injection (stdin piping breaks the UI). Display-message shows a flash in the tmux status bar — the user sees it but the agent does not auto-process inbox messages.

**Pane classification** — `ClassifyPane` detects box-drawing characters (─, │, ╭, ╰, ┌, └) as ready (TUI rendered). "Error"/"FATAL" in pane content indicates not ready.

**Agent config generation** — `WriteAgentConfig(role)` writes `.opencode/agents/<role>.md` with YAML frontmatter:
- `description` from source agent definition
- `mode: primary`
- `model` mapped from Claude model names to `anthropic/<model>` format (override via `MUXCODE_{ROLE}_MODEL`)
- `permission` block translated from tool profiles: `Bash(pattern)` → bash allow, `!Bash(pattern)` → bash deny, `Write`/`Edit` → edit allow

**Tool profile translation** — `translateToolProfile(role)` converts muxcode tool profiles to OpenCode permission YAML:
- `Bash(pattern)` → `"pattern": allow` in bash permission
- `!Bash(pattern)` → `"pattern": deny` in bash permission
- `Write`, `Edit` → `edit: allow`
- `Read`, `Grep`, `Glob` → implicitly allowed (no permission needed)

**Model mapping** — `resolveOpenCodeModel(role)` maps Claude model names to OpenCode provider format. Check `MUXCODE_{ROLE}_MODEL` env var first, then map Claude defaults: `claude-sonnet-4-5` → `anthropic/claude-sonnet-4-5`.

Success criteria:
- [x] Any role with `MUXCODE_{ROLE}_CLI=opencode` launches the bare `opencode` binary in TUI mode
- [x] Beta role defaults to `opencode` TUI without explicit env var (preserves Phase 0c behavior)
- [x] Agent config generated in `.opencode/agents/<role>.md` with correct frontmatter
- [x] Tool profiles translated to OpenCode `permission` format
- [x] System prompt written as agent markdown body
- [x] Pane classification detects TUI frame via box-drawing characters
- [x] Alive detection works via pane capture (opencode text + box-drawing chars)
- [x] Idle detection gracefully returns false (TUI limitation)
- [x] Wake-up uses display-message (best-effort notification)
- [x] Compact is no-op (TUI auto-manages context)
- [x] `IsBetaCLI` removed from `LaunchConfig` — provider dispatch handles all routing
- [x] All existing tests pass (300+), new tests cover all provider methods

### Phase 3: graceful degradation ✅

Ensure muxcode operates correctly when OpenCode TUI agents are present alongside Claude Code agents. Since TUI agents cannot be driven programmatically (no hooks, no idle detection, no reliable input injection), the system must degrade gracefully — skipping hook-driven features for those agents and relying on pre-configured permissions and system prompt instructions instead.

Updated files:

| File | Change |
|------|--------|
| `cmd/hook.go` | All 4 hook functions (`hookBash`, `hookGuard`, `hookAnalyze`, `hookInboxPoll`) check `provider.SupportsHooks()` — early return for non-hook providers |
| `bus/prompt.go` | `SharedPrompt()` adds "Manual Bus Messaging" section for non-hook, non-edit roles — explicit instructions to send build/test/deploy results via `muxcode send` |
| `watcher/watcher.go` | `checkIdleAgents()` skips non-hook providers early (avoids unnecessary pane captures); falls back to `provider.SendWakeUp()` (display-message) |
| `bus/provider_test.go` | Tests for `SupportsHooks()` gating, `ResolveProvider` hook support by env var, `IsIdle` always-false for OpenCode/local |
| `bus/prompt_test.go` | Tests for Manual Bus Messaging section presence/absence by provider and role |

Key degradation behaviors:

| Feature | Claude Code | OpenCode TUI | How it degrades |
|---------|-------------|--------------|-----------------|
| Build→test→review chain | Hook fires automatically | No hooks | Chain does not fire; system prompt instructs agent to send bus messages manually |
| Edit guard | PreToolUse blocks dangerous commands | No hooks | Guard skipped; `permission.bash` deny rules in agent config block commands at OpenCode's level |
| Workflow state | Hooks transition state | No hooks | State transitions skipped for that agent |
| Watcher wake-up | Send-keys when idle | Cannot detect idle | Watcher skips; user interacts with TUI directly |
| Compact trigger | `/compact` injected via send-keys | TUI auto-compacts | No-op from muxcode; TUI handles internally |

Success criteria:
- [x] Agents with OpenCode provider run without errors when hooks are absent
- [x] Build/test/review chains disabled for non-hook agents (no spurious chain fires)
- [x] Edit guard disabled for non-hook agents (no PreToolUse errors)
- [x] Watcher skips idle check for OpenCode roles (no false positives)
- [x] System prompt includes bus message instructions for non-hook providers
- [x] OpenCode `permission.bash` deny rules replace edit guard for dangerous commands
- [x] Workflow state transitions skipped for non-hook agents without errors

### Phase 4: mixed-provider session testing

End-to-end validation that a session with both Claude Code and OpenCode TUI agents works correctly. The edit agent stays on Claude Code (full hook support), while one or more other roles run OpenCode TUI. The bus handles cross-provider messaging transparently — both providers read from and write to the same file-based inbox.

Test scenarios:

1. **Basic mixed session**: Claude Code edit + OpenCode beta — verify both agents launch, respond to bus messages, and coexist without errors.
2. **Role takeover**: Beta window takes over a Claude Code role (e.g. build) — verify bus messages route correctly to the F10 pane.
3. **Multi-OpenCode**: Multiple roles on OpenCode (e.g. `MUXCODE_BUILD_CLI=opencode MUXCODE_TEST_CLI=opencode`) — verify each gets its own agent config and TUI instance.
4. **Config coexistence**: `.claude/agents/` and `.opencode/agents/` directories coexist without conflicts.

Success criteria:
- [ ] Session with mixed providers (Claude Code edit + OpenCode beta TUI) launches without errors
- [ ] F1/F10 toggle between edit (Claude Code) and beta (OpenCode TUI) works
- [ ] Bus messaging works between providers (edit sends to beta, beta replies via `muxcode send`)
- [ ] Claude Code agents compact via `/compact`; OpenCode TUI agents auto-compact (no muxcode intervention)
- [ ] `muxcode status` shows provider per agent
- [ ] `.claude/` and `.opencode/` directories coexist without conflicts
- [ ] Agent config generated correctly in `.opencode/agents/` for each OpenCode role
- [ ] Watcher does not error on OpenCode roles (skips idle check, display-message notifications)

### Phase 5: server mode (future/optional)

Programmatic interaction via `opencode serve` HTTP API for environments that need reliable idle detection, automated message sending, and deterministic wake-up. Server mode is not required for basic OpenCode integration — TUI mode (Phases 2-4) covers the primary use case. Server mode adds value for fully automated pipelines where the user does not interact with the TUI directly.

Server mode would launch `opencode serve --port <port>` instead of the bare TUI binary, then drive interaction via REST API:

| Endpoint | Purpose |
|----------|---------|
| `POST /session` | Create session |
| `POST /session/{id}/message` | Send prompt (streams response) |
| `POST /session/{id}/prompt_async` | Send prompt (fire-and-forget, returns 204) |
| `GET /session/{id}/status` | Idle/busy state |
| `GET /global/event` | SSE event stream |

This would enable:
- **Reliable idle detection** via `GET /session/{id}/status` (replaces TUI's `return false`)
- **Programmatic message sending** via API (replaces TUI's display-message-only)
- **Automated wake-up** — send prompts to idle agents without user interaction
- **Session management** — create/list/delete sessions via API

Success criteria:
- [ ] `MUXCODE_OPENCODE_MODE=server` launches `opencode serve --port <port>` in the agent pane
- [ ] Session created via `POST /session` on startup
- [ ] Messages sent via API (`POST /session/{id}/message` or `/prompt_async`)
- [ ] Idle detection works via `GET /session/{id}/status`
- [ ] Port management: unique port per role via `OpenCodePort(role)`
- [ ] Auth support via `OPENCODE_SERVER_PASSWORD` env var
- [ ] Documented as optional enhancement over TUI mode

## Configuration

### Environment variables

| Env var | Default | Description |
|---------|---------|-------------|
| `MUXCODE_AGENT_CLI` | `claude` | Default AI CLI for all agents (session-wide) |
| `MUXCODE_EDIT_CLI` | (falls back to default) | AI CLI for the edit agent |
| `MUXCODE_BUILD_CLI` | (falls back to default) | AI CLI for the build agent |
| `MUXCODE_TEST_CLI` | (falls back to default) | AI CLI for the test agent |
| `MUXCODE_REVIEW_CLI` | (falls back to default) | AI CLI for the review agent |
| `MUXCODE_DEPLOY_CLI` | (falls back to default) | AI CLI for the deploy agent |
| `MUXCODE_ANALYZE_CLI` | (falls back to default) | AI CLI for the analyze agent |
| `MUXCODE_COMMIT_CLI` | (falls back to default) | AI CLI for the commit agent |
| `MUXCODE_WATCH_CLI` | (falls back to default) | AI CLI for the watch agent |
| `MUXCODE_BETA_CLI` | (falls back to default) | AI CLI for the beta testbed agent |
| `MUXCODE_BETA_ROLE` | (unset) | Role to mimic in beta window (e.g. `build`, `test`) |
| `MUXCODE_BETA_TAKEOVER` | `false` | Enable takeover mode (assume mimicked role's bus identity) |
| `MUXCODE_OPENCODE_MODE` | `tui` | OpenCode interaction mode (`tui`, `server`). Server mode is future/optional (Phase 5) |
| `MUXCODE_OPENCODE_PORT` | `4096` | Port for OpenCode server mode (only used when `MUXCODE_OPENCODE_MODE=server`) |

All `MUXCODE_{ROLE}_CLI` vars accept: `claude`, `opencode`, or `local`. Resolution: per-role → session default → `claude`.

## Dependencies

| Dependency | Required | Install |
|------------|----------|---------|
| tmux >= 3.0 | Yes | `brew install tmux` |
| Go >= 1.22 | Yes (build) | `brew install go` |
| jq | Yes | `brew install jq` |
| Neovim | Yes | `brew install neovim` |
| fzf | Yes | `brew install fzf` |
| Claude Code | At least one provider | `npm install -g @anthropic-ai/claude-code` or `brew install claude-code` |
| OpenCode >= 1.4.0 | At least one provider | `curl -fsSL https://opencode.ai/install \| bash` or `brew install opencode` |
| Ollama | Optional (local LLM) | `brew install ollama` |

## Resolved questions

### 1. TUI mode is the primary integration path

**Decision**: TUI mode (bare `opencode` binary) is the primary path. Server mode (`opencode serve`) is a future/optional enhancement.

**Rationale**: TUI mode provides the simplest integration with the least coupling. OpenCode agents run as interactive TUI sessions in tmux panes — users can interact with them directly, and the TUI manages its own context window, tool execution, and compaction. This matches the beta window pattern (Phase 0c) that's already proven to work.

The trade-off is reduced programmability: muxcode cannot reliably inject text into the TUI (stdin piping breaks the UI, github issue #3871), detect idle state, or trigger compaction. But these limitations are acceptable because:

1. **OpenCode agents are semi-autonomous** — they handle their own tool execution and context management without muxcode orchestration
2. **The edit agent stays on Claude Code** — hook-driven chains, edit guard, and workflow state transitions are only needed on the orchestrator
3. **Bus messaging still works** — OpenCode agents read their inbox via `muxcode inbox` and send replies via `muxcode send`, same as Claude Code agents
4. **Graceful degradation is well-defined** — each hook-dependent feature has a documented fallback (permission deny rules, system prompt instructions, auto-compact)

Server mode (`opencode serve`) remains available as Phase 5 for environments that need reliable idle detection and programmatic message sending. The TUI approach is sufficient for the primary use case: mixed-provider sessions where OpenCode agents handle specific roles alongside Claude Code.

### 2. Accept prompt-based hook replacement; custom commands cannot substitute

**Decision**: Accept the limitation. System prompt instructions are the hook replacement for non-hook providers.

**Rationale**: OpenCode's custom commands (`.opencode/commands/*.md`) are prompt templates, not lifecycle callbacks. They can execute shell scripts via backtick syntax (`` !`command` ``), but only as one-off AI interactions — not as post-command hooks that fire automatically after bash execution. There is no `tool.execute.after` event system or equivalent to Claude Code's PreToolUse/PostToolUse hooks.

The system prompt approach ("after running a build command, send `muxcode send edit build-complete ...`") is best-effort but sufficient because:
- Non-edit agents (build, test, deploy) have narrow responsibilities — their system prompts are focused and the LLM reliably follows simple post-command instructions
- The edit agent (which drives chains) stays on Claude Code where hooks work deterministically
- OpenCode agents are most likely used for creative/design roles where hook-driven chains are less critical
- OpenCode's per-agent `permission` blocks in the agent config replace the edit guard (deny dangerous commands at the permission level instead of intercepting via hooks)

No changes needed to the design — the graceful degradation section already handles this correctly.

### 3. Muxcode manages both config directories; no conflicts

**Decision**: Muxcode generates and manages both `.claude/` and `.opencode/` directories as needed. No user maintenance required.

**Rationale**: The directories serve different tools and don't conflict:
- `.claude/agents/*.md` — read only by Claude Code agents
- `.opencode/agents/*.md` — read only by OpenCode agents
- `opencode.json` — read only by OpenCode

Muxcode already generates Claude Code agent configs via `WriteAgentConfig`. The same function, dispatched through the provider interface, generates OpenCode configs. Each provider writes only to its own config directory.

Both directories should be added to `.gitignore` (they're generated artifacts, not source). The `WriteAgentConfig` method on each provider handles cleanup on session teardown.

### 4. Pin to minimum v1.4.0; isolate version-specific behavior

**Decision**: Require OpenCode >= v1.4.0. Check at launch, warn if below minimum.

**Rationale**: OpenCode is at v1.4.2 (758+ releases) with active development and breaking changes at minor version bumps. There is no formal semver contract (a JSON Schema versioning proposal exists but is not implemented). Recent breaking changes in v1.4.0 affected SDK response metadata.

The provider interface already isolates all OpenCode-specific behavior behind `Provider` methods, so version-specific differences are contained. The minimum version check goes in the provider's initialization:

```go
func (p *OpenCodeProvider) CheckVersion() error {
    // opencode --version → parse semver
    // require >= 1.4.0 (server API stability baseline)
}
```

Version check runs at agent launch time (in `ResolveLaunchConfig`), not at install time. `install.sh` only checks whether `opencode` binary exists (already in the design).

### 5. Per-role at launch only; no mid-session switching

**Decision**: Provider is set per-role at session launch via env vars. No mid-session switching.

**Rationale**: Mid-session provider switching (`muxcode provider set designer opencode`) would require:
- Killing the running agent process
- Regenerating agent config in the new provider's format
- Re-launching with different CLI, flags, and startup sequence
- Migrating conversation context (not portable between providers)

This complexity has no clear use case — the provider choice is a session configuration decision, not a runtime toggle. The env var pattern (`MUXCODE_DESIGNER_CLI=opencode`) is set before `muxcode.sh` launches and applies for the session lifetime.

If a user wants to change providers, they restart the session with updated env vars. This is consistent with how `MUXCODE_{ROLE}_CLAUDE_MODEL` works today.
