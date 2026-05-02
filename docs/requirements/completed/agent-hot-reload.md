# Agent hot reload

Change the CLI provider (Claude Code, OpenCode, Codex) and model for any agent at runtime without restarting the muxcode session. Includes a `muxcode reload` CLI command for programmatic use and a provider selector modal (triggered from the tmux bottom menu) that presents available providers and models in an interactive TUI — the user selects a configuration and the currently active agent window reloads with it.

## Context

### Current state

| Aspect | Current behavior |
|--------|-----------------|
| CLI selection | Resolved at launch via `ResolveProviderCLI()`: `MUXCODE_{ROLE}_CLI` > `MUXCODE_AGENT_CLI` > `roleDefaultCLI()` |
| Model selection | Resolved at launch via `RoleClaudeModelDefault()` / `RoleOpenCodeModelDefault()` / `MUXCODE_{ROLE}_CLAUDE_MODEL` / `MUXCODE_{ROLE}_MODEL` |
| Agent launch | `muxcode agent launch <role>` → `ResolveLaunchConfig()` → `BuildExecArgs()` → `syscall.Exec()` |
| Agent restart | `RestartLocalAgent()` in daemon health checks: `C-c` → 500ms wait → `muxcode agent launch <role>` |
| Mode cycling | F1/F2 swap panes between agents in the same window (plan↔research, edit↔auto) — different agents, not reconfiguring the same agent |
| Config reloading | No runtime config reload — env vars read once at launch, cached in process memory |
| Provider config | `WriteAgentConfig()` generates `.opencode/agents/<role>.md` or `.codex/AGENTS.md` at launch time only |

### Problem

Changing an agent's CLI or model requires restarting the entire muxcode session (`muxcode` or `tmux kill-session`). This is disruptive:

1. **All agents restart** — not just the one being reconfigured
2. **Conversation context lost** — Claude Code and OpenCode conversations are ephemeral (memory persists but active conversation is lost)
3. **Workflow state reset** — in-progress workflows restart from scratch
4. **Slow iteration** — testing a different model for a role requires a full session restart (~15 seconds)

Users want to:
- Try DeepSeek V4 Pro on the build agent without disrupting the edit agent
- Switch the edit agent from Claude Code to OpenCode mid-session
- Upgrade a model (e.g., Sonnet 4 → Opus 4.6) for a specific role without restarting
- Downgrade a struggling agent to a cheaper/faster model on the fly

### Goal

1. A `muxcode reload` CLI command that stops a single agent, re-resolves configuration, regenerates provider-specific files, and relaunches — preserving inbox, memory, workflow state, and bus identity
2. A **provider selector modal** (`prefix + R`) that shows available providers and models in an interactive TUI, targeting the currently active agent window
3. A `muxcode config set` companion to persist changes without immediate reload
4. Works for all providers: Claude Code, OpenCode, Codex, local LLM harness

## Design

### 1. `muxcode reload` command

New CLI subcommand that performs an atomic stop-reconfigure-relaunch cycle:

```bash
# Reload with current config (re-reads env vars)
muxcode reload build

# Reload with CLI override
muxcode reload build --cli opencode

# Reload with model override
muxcode reload build --model opencode-go/deepseek-v4-pro

# Reload with both
muxcode reload edit --cli opencode --model opencode-go/deepseek-v4-pro

# Reload all agents (careful — reloads every active agent sequentially)
muxcode reload --all
```

**Execution sequence**:

```
muxcode reload build --cli opencode --model opencode-go/deepseek-v4-pro
      │
      ▼
1. Validate role exists and is alive
      │
      ▼
2. Save session compact for the agent (optional, --compact flag)
      │  └─ muxcode session compact "Hot reload: switching build from claude to opencode"
      │
      ▼
3. Write override env vars to runtime config file
      │  └─ /tmp/muxcode-bus-{session}/config/build.env
      │     MUXCODE_BUILD_CLI=opencode
      │     MUXCODE_BUILD_MODEL=opencode-go/deepseek-v4-pro
      │
      ▼
4. Stop the agent process
      │  └─ tmux send-keys -t {target} C-c
      │  └─ Wait for process exit (poll pane content, max 5s)
      │
      ▼
5. Regenerate provider config
      │  └─ WriteAgentConfig(role) — generates .opencode/agents/ or .codex/AGENTS.md
      │
      ▼
6. Relaunch agent
      │  └─ tmux send-keys -t {target} "muxcode agent launch {role}" Enter
      │
      ▼
7. Verify launch (poll for alive signal, max 15s)
      │
      ▼
8. Log lifecycle event + notify edit agent
```

### 2. Runtime config overrides

Override files persist CLI/model changes for the duration of the session. They are read by `ResolveProviderCLI()` and model resolution functions before falling through to env vars.

**Storage**: `/tmp/muxcode-bus-{session}/config/{role}.env`

**Format**: Shell-sourceable key=value pairs:

```bash
MUXCODE_BUILD_CLI=opencode
MUXCODE_BUILD_MODEL=opencode-go/deepseek-v4-pro
```

**Resolution chain update** (highest priority first):

| Priority | Source | Scope |
|----------|--------|-------|
| 1 | Runtime override (`/tmp/muxcode-bus-{session}/config/{role}.env`) | Per-role, session-scoped |
| 2 | `MUXCODE_{ROLE}_CLI` / `MUXCODE_{ROLE}_MODEL` env var | Per-role, shell-scoped |
| 3 | `MUXCODE_AGENT_CLI` env var | Session-wide |
| 4 | `roleDefaultCLI()` / `RoleClaudeModelDefault()` | Built-in default |

Runtime overrides are ephemeral — they live in `/tmp/` and are cleaned up with the session. To persist across sessions, use `muxcode config set` (see below).

### 3. `muxcode config set` — persistent config changes

Companion command that writes to the shell-sourceable config file without triggering an immediate reload:

```bash
# Set CLI for a role (persists to ~/.config/muxcode/config or .muxcode/config)
muxcode config set build.cli opencode

# Set model for a role
muxcode config set build.model opencode-go/deepseek-v4-pro

# Set and reload in one step
muxcode config set build.cli opencode --reload

# View current effective config for a role
muxcode config get build
```

**Output of `muxcode config get build`**:

```
=== build ===
CLI:     opencode  (runtime override)
Model:   opencode-go/deepseek-v4-pro  (env: MUXCODE_BUILD_MODEL)
Default: claude / claude-sonnet-4-6
```

### 4. Graceful stop with context preservation

The stop phase must be graceful — give the agent a chance to save state before killing:

```go
func GracefulStop(session, role string, compact bool) error {
    target := PaneTarget(session, role)
    provider := ResolveProvider(role)

    // Optional: trigger compact before stopping
    if compact {
        _ = provider.Compact(session, role, target)
        time.Sleep(2 * time.Second)
    }

    // Send C-c to interrupt
    tmuxSendKeys(target, "C-c")

    // Poll for process exit (max 5s)
    for i := 0; i < 10; i++ {
        time.Sleep(500 * time.Millisecond)
        if !provider.IsAlive(session, role) {
            return nil
        }
    }

    // Force kill if still running
    tmuxSendKeys(target, "C-c")
    time.Sleep(1 * time.Second)

    if provider.IsAlive(session, role) {
        return fmt.Errorf("agent %s did not exit after 6 seconds", role)
    }
    return nil
}
```

### 5. Environment injection for relaunch

The relaunched `muxcode agent launch` must pick up runtime overrides. Two approaches:

**Option A: Env export before exec** (preferred):
```bash
# In the tmux pane, before relaunch:
tmux send-keys "export MUXCODE_BUILD_CLI=opencode MUXCODE_BUILD_MODEL=opencode-go/deepseek-v4-pro && muxcode agent launch build" Enter
```

**Option B: `ResolveLaunchConfig` reads override file**:
```go
func ResolveLaunchConfig(role string) *LaunchConfig {
    // Load runtime overrides first
    loadRuntimeOverrides(role)  // reads /tmp/muxcode-bus-{session}/config/{role}.env
    // ... existing resolution logic (now sees injected env vars)
}
```

Option B is cleaner — the override file is read automatically, no shell-level env export needed. The `loadRuntimeOverrides()` function calls `os.Setenv()` for each key-value pair in the override file before the existing resolution chain runs.

### 6. Mode-cycled agents

Agents on mode-cycled windows (edit↔auto on F2, plan↔research on F1) need special handling:

- **Active mode agent**: reload directly in the host window pane
- **Inactive mode agent**: reload in the holding window pane (hidden at index 0)
- **Mode state preserved**: `ModeCycleState` is unchanged — same agents, same indices

```go
func ReloadTarget(session, role string) string {
    // Check if role is in a mode cycle
    for _, window := range []string{"edit", "plan"} {
        state, err := ReadModeCycleState(session, window)
        if err != nil {
            continue
        }
        for _, agent := range state.Agents {
            if agent.Role == role {
                if agent.Index == state.Current {
                    // Active — target the host window
                    return PaneTarget(session, window)
                }
                if agent.HoldWindow != "" {
                    // Inactive — target the holding window
                    return session + ":" + agent.HoldWindow + ".1"
                }
            }
        }
    }
    // Standard agent — use normal pane target
    return PaneTarget(session, role)
}
```

### 7. Daemon awareness

The daemon must know about reloads to avoid interfering:

- **Health check suppression**: after a reload, suppress health checks for the role for 30 seconds (the agent needs time to start up)
- **Reload marker**: write `/tmp/muxcode-bus-{session}/lock/{role}.reloading` during reload, removed after verification
- **Idle agent check**: skip the role while the reloading marker exists

```go
func ReloadMarkerPath(session, role string) string {
    return filepath.Join(BusDir(session), "lock", role+".reloading")
}

func IsReloading(session, role string) bool {
    _, err := os.Stat(ReloadMarkerPath(session, role))
    return err == nil
}
```

The daemon's `checkAgentHealth()` and `checkIdleAgents()` skip roles with an active reload marker.

### 8. Lifecycle logging and notifications

Every reload is logged and notified:

```go
LogLifecycle(session, "info", "user", "agent-reload",
    fmt.Sprintf("%s: %s→%s, model: %s", role, oldCLI, newCLI, newModel))

// Notify edit agent
msg := NewMessage("daemon", "edit", "event", "agent-reloaded",
    fmt.Sprintf("Agent %s reloaded: CLI=%s, Model=%s", role, newCLI, newModel), "")
Send(session, msg)
```

### 9. Provider selector modal

An interactive TUI modal triggered from the tmux bottom menu (`prefix + b → Provider` or `prefix + R`) that lets the user visually pick a provider and model for the currently active agent window.

#### Trigger

Added to the MuxCode quick menu (`prefix + b`) in `config/tmux.conf`:

```
"Provider"            R "run-shell 'muxcode modal open provider'"
```

Direct keybinding (`prefix + R`):

```
bind R run-shell 'muxcode modal open provider'
```

#### Modal registration

Registered in `DefaultModalConfigs()` alongside the existing `api` modal:

```go
{
    Name:    "provider",
    Title:   " Provider Selector ",
    Width:   "50%",
    Height:  "60%",
    Command: "muxcode provider-select",
    Sizes: map[string][2]string{
        "compact": {"40%", "50%"},
        "full":    {"60%", "70%"},
    },
},
```

No split pane — the TUI fills the entire modal. No bus role — this is a user-facing tool, not an agent.

#### TUI layout

The `muxcode provider-select` command renders a Dracula-themed interactive TUI inside the modal popup:

```
┌─────────── Provider Selector ───────────┐
│                                         │
│  Agent: build (F3)                      │
│  Current: opencode / deepseek-v4-pro    │
│                                         │
│  ─── Provider ──────────────────────    │
│                                         │
│    ● Claude Code                        │
│    ○ OpenCode                           │
│    ○ Codex                              │
│    ○ Local (Ollama)                     │
│                                         │
│  ─── Model ─────────────────────────    │
│                                         │
│    ○ claude-opus-4-6                    │
│    ● claude-sonnet-4-6                  │
│    ○ claude-haiku-4-0                   │
│    ○ custom...                          │
│                                         │
│  ─── Options ───────────────────────    │
│                                         │
│    [ ] Compact before reload            │
│    [ ] Persist to config                │
│                                         │
│  ┌──────────┐  ┌──────────┐            │
│  │  Reload   │  │  Cancel  │            │
│  └──────────┘  └──────────┘            │
│                                         │
│  ↑↓ Navigate  ␣ Select  ⏎ Reload       │
│  Tab Switch section  q Quit             │
└─────────────────────────────────────────┘
```

#### Target resolution

The modal targets the **currently active agent window** at the time of launch. Resolved via tmux:

```go
func resolveActiveAgentWindow(session string) (string, string, error) {
    // Get the active tmux window name
    out, _ := exec.Command("tmux", "display-message", "-p", "#{window_name}").Output()
    window := strings.TrimSpace(string(out))

    // Resolve the active role for mode-cycled windows
    role, err := ActiveModeRole(session, window)
    if err != nil {
        role = window // fallback to window name
    }

    return window, role, nil
}
```

If the active window is `plan` and research mode is active, the modal targets the `research` role. If the active window is `edit` and auto mode is active, it targets the `auto` role.

#### Provider and model lists

Each provider has a set of known models. The TUI dynamically populates the model list when the user selects a provider:

```go
type ProviderOption struct {
    Name     string   // display name
    CLI      string   // cli identifier ("claude", "opencode", "codex", "local")
    Models   []string // known model IDs
    Default  string   // default model for this provider
    Installed bool    // true if the CLI binary is found on PATH
}

func AvailableProviders() []ProviderOption {
    return []ProviderOption{
        {
            Name:    "Claude Code",
            CLI:     "claude",
            Models:  []string{"claude-opus-4-6", "claude-sonnet-4-6", "claude-haiku-4-0"},
            Default: "claude-sonnet-4-6",
        },
        {
            Name:    "OpenCode",
            CLI:     "opencode",
            Models:  []string{"opencode-go/deepseek-v4-pro", "opencode-go/gpt-4.1", "opencode-go/gemini-2.5-pro"},
            Default: "opencode-go/deepseek-v4-pro",
        },
        {
            Name:    "Codex",
            CLI:     "codex",
            Models:  []string{"codex-mini-latest", "o4-mini", "o3"},
            Default: "codex-mini-latest",
        },
        {
            Name:    "Local (Ollama)",
            CLI:     "local",
            Models:  []string{}, // populated dynamically from `ollama list`
            Default: "",
        },
    }
}
```

For the local provider, models are populated dynamically by running `ollama list` and parsing the output. The "custom..." option in the model list allows the user to type a freeform model ID.

#### Installed detection

The TUI checks if each provider's CLI binary is available on `PATH` using `exec.LookPath()`. Unavailable providers are shown greyed out with "(not installed)" — selectable but with a warning.

#### Action on "Reload"

When the user presses Enter or selects "Reload":

1. Write runtime override file (same as `muxcode reload --cli --model`)
2. Close the modal (exit the TUI process — the popup auto-closes)
3. Execute `muxcode reload <role> --cli <cli> --model <model>` in the background

The reload happens after the modal closes so the user sees the agent window directly. The modal writes a trigger file that the daemon picks up, or uses `tmux run-shell` to execute the reload command asynchronously.

```go
// On user confirmation:
func executeReload(session, role, cli, model string, compact, persist bool) {
    if persist {
        // Write to persistent config
        persistConfig(role, cli, model)
    }

    // Build reload command
    args := []string{"muxcode", "reload", role, "--cli", cli, "--model", model}
    if compact {
        args = append(args, "--compact")
    }

    // Write command to trigger file for post-modal execution
    triggerPath := filepath.Join(BusDir(session), "reload-trigger")
    os.WriteFile(triggerPath, []byte(strings.Join(args, " ")), 0644)
}
```

The modal's wrapper script (in `BuildModalCommand`) executes the trigger file after the TUI exits:

```bash
muxcode provider-select; \
  if [ -f /tmp/muxcode-bus-$MUXCODE_SESSION/reload-trigger ]; then \
    bash /tmp/muxcode-bus-$MUXCODE_SESSION/reload-trigger; \
    rm -f /tmp/muxcode-bus-$MUXCODE_SESSION/reload-trigger; \
  fi
```

#### Keyboard navigation

| Key | Action |
|-----|--------|
| `↑` / `k` | Move cursor up within current section |
| `↓` / `j` | Move cursor down within current section |
| `Tab` | Switch between Provider / Model / Options sections |
| `Space` | Select item (radio for provider/model, checkbox for options) |
| `Enter` | Confirm and reload |
| `q` / `Esc` | Cancel and close modal |
| `/` | Filter models (type-ahead search) |

#### TUI implementation

Built with Go stdlib — no external TUI libraries (consistent with the existing `tui/` package and harness TUI). Uses raw terminal mode, ANSI escape codes, and the Dracula color palette already defined in `tui/styles.go`.

```go
// In bus/provider_select.go or a new tui/provider_select.go

type ProviderSelectUI struct {
    session     string
    role        string
    window      string
    providers   []ProviderOption
    selectedCLI int    // index into providers
    selectedMdl int    // index into current provider's models
    section     int    // 0=provider, 1=model, 2=options
    compact     bool
    persist     bool
    width       int
    height      int
}
```

### Architecture diagram

```
muxcode reload build --cli opencode --model deepseek-v4-pro
      │
      ├─ Write runtime override file
      │   └─ /tmp/muxcode-bus-{session}/config/build.env
      │
      ├─ GracefulStop(session, "build")
      │   ├─ C-c → poll for exit
      │   └─ Write reload marker (lock/build.reloading)
      │
      ├─ loadRuntimeOverrides("build")
      │   └─ os.Setenv("MUXCODE_BUILD_CLI", "opencode")
      │   └─ os.Setenv("MUXCODE_BUILD_MODEL", "deepseek-v4-pro")
      │
      ├─ WriteAgentConfig("build")
      │   └─ .opencode/agents/build.md (new provider config)
      │
      ├─ tmux send-keys "muxcode agent launch build" Enter
      │
      ├─ Poll IsAgentAlive (max 15s)
      │   └─ Remove reload marker on success
      │
      ├─ LogLifecycle + notify edit
      │
      └─ Print summary to stdout
          "✓ build reloaded: claude → opencode (deepseek-v4-pro)"
```

### Relationship to existing features

| Feature | Interaction |
|---------|------------|
| `RestartLocalAgent()` | Reload subsumes restart — same stop+relaunch but with config changes. Restart becomes `Reload(session, role, nil, nil)` (no overrides). |
| Mode cycling (F1/F2) | Reload operates on individual roles within mode cycles. Modal resolves the active role via `ActiveModeRole()`. Mode state is preserved. |
| Agent health monitoring | Health checks suppressed during reload via marker file. Recovery detection fires normally after reload. |
| `muxcode agent launch` | Reload calls launch after injecting overrides. Launch itself is unchanged. |
| `PreLaunchSetup()` | Runs on relaunch — writes startup inbox message, lifecycle log. |
| `WriteAgentConfig()` | Called during reload to regenerate provider-specific config for the new CLI. |
| Console viewers | Console (pane 0) is unaffected — only the agent pane (pane 1) is restarted. |
| Modal window manager | Provider selector registered as a modal — uses existing `OpenModal()`, `display-popup`, toggle behavior, Dracula popup styling. |
| Quick menu (`prefix + b`) | Provider selector added as a menu entry alongside existing entries (Dashboard, API Testing, Agent Status, etc.). |

## Implementation

### Phase 1: Runtime config overrides and resolution

New files:

| File | Purpose |
|------|---------|
| `bus/override.go` | Runtime override file read/write, `LoadRuntimeOverrides()`, `WriteRuntimeOverride()`, `ReadRuntimeOverrides()`, `ClearRuntimeOverrides()`, `RuntimeOverridePath()` |
| `bus/override_test.go` | Tests for override read/write, env var injection, precedence over defaults |

Updated files:

| File | Change |
|------|--------|
| `bus/config.go` | Add `RuntimeConfigDir()` path helper |
| `bus/provider.go` | Update `ResolveProviderCLI()` to call `LoadRuntimeOverrides(role)` before env var check |
| `bus/launch.go` | Update `ResolveLaunchConfig()` to call `LoadRuntimeOverrides(role)` at start |

Success criteria:
- [ ] `WriteRuntimeOverride("build", "MUXCODE_BUILD_CLI", "opencode")` creates override file
- [ ] `LoadRuntimeOverrides("build")` sets env vars from override file
- [ ] `ResolveProviderCLI("build")` returns `"opencode"` when runtime override is set (even if env var says `"claude"`)
- [ ] `ClearRuntimeOverrides("build")` removes override file
- [ ] Override files are in `/tmp/muxcode-bus-{session}/config/` (session-scoped, ephemeral)
- [ ] Existing resolution chain works unchanged when no override file exists

### Phase 2: Graceful stop and reload command

New files:

| File | Purpose |
|------|---------|
| `cmd/reload.go` | `Reload()` command handler — parses flags, orchestrates stop→config→relaunch |
| `bus/reload.go` | `GracefulStop()`, `ReloadAgent()`, `ReloadMarkerPath()`, `IsReloading()`, `ReloadTarget()` |
| `bus/reload_test.go` | Tests for graceful stop, reload marker, mode-aware target resolution |

Updated files:

| File | Change |
|------|--------|
| `main.go` | Add `"reload"` case to subcommand dispatch |
| `bus/agent_health.go` | `IsAgentHealthExcluded()` also returns true when `IsReloading()` is true |

Success criteria:
- [ ] `muxcode reload build` stops the build agent and relaunches with current config
- [ ] `muxcode reload build --cli opencode` writes runtime override, relaunches on OpenCode
- [ ] `muxcode reload build --model deepseek-v4-pro` writes model override, relaunches with new model
- [ ] `GracefulStop()` waits up to 5s for process exit, force-kills after
- [ ] Reload marker written during reload, removed after successful launch verification
- [ ] `ReloadTarget()` resolves correct pane for mode-cycled agents (active vs holding window)
- [ ] Agent health checks suppressed during reload (marker-based)
- [ ] Lifecycle event logged for every reload
- [ ] Edit agent notified of reload events

### Phase 3: `muxcode config` command

New files:

| File | Purpose |
|------|---------|
| `cmd/config.go` | `Config()` command handler — `set`, `get`, `list` subcommands |

Updated files:

| File | Change |
|------|--------|
| `main.go` | Add `"config"` case to subcommand dispatch (if not already present) |
| `bus/launch.go` | Add `EffectiveConfig(role)` function that returns resolved CLI, model, and source for each |

Success criteria:
- [ ] `muxcode config set build.cli opencode` writes to shell-sourceable config file
- [ ] `muxcode config set build.cli opencode --reload` writes config and triggers reload
- [ ] `muxcode config get build` shows effective CLI, model, and resolution source
- [ ] `muxcode config list` shows all roles with their effective CLI and model
- [ ] Config changes persist across session restarts (written to `~/.config/muxcode/config`)

### Phase 4: Daemon integration and `--all` support

Updated files:

| File | Change |
|------|--------|
| `daemon/daemon.go` | `checkAgentHealth()` — skip roles with reload marker. `checkIdleAgents()` — skip roles with reload marker. |
| `cmd/reload.go` | Add `--all` flag support — iterate `KnownRoles`, reload each sequentially with 5s gap |
| `bus/reload.go` | Add `ReloadAll()` function, `IsReloadMarkerStale()` cleanup (>60s = stale) |

Success criteria:
- [ ] Daemon skips reloading agents in health checks and idle checks
- [ ] `muxcode reload --all` reloads every active agent sequentially
- [ ] Stale reload markers (>60s) are auto-cleaned by the daemon
- [ ] No race conditions between reload and daemon health checks
- [ ] Edit agent excluded from `--all` reload (interactive session — require explicit `muxcode reload edit`)

### Phase 5: Provider selector modal

New files:

| File | Purpose |
|------|---------|
| `cmd/provider_select.go` | `ProviderSelect()` command handler — entry point for `muxcode provider-select` |
| `tui/provider_select.go` | `ProviderSelectUI` TUI — raw terminal, ANSI rendering, keyboard navigation, Dracula theme |
| `bus/provider_options.go` | `AvailableProviders()`, `ProviderOption` struct, installed detection, dynamic Ollama model list |
| `bus/provider_options_test.go` | Tests for provider list, installed detection, model population |

Updated files:

| File | Change |
|------|--------|
| `bus/modal.go` | Add `provider` modal to `DefaultModalConfigs()` — 50%×60%, command `muxcode provider-select`, no split, no bus role |
| `config/tmux.conf` | Add `"Provider"` entry to `prefix + b` menu. Add `bind R run-shell 'muxcode modal open provider'` keybinding. |
| `main.go` | Add `"provider-select"` case to subcommand dispatch |

Success criteria:
- [ ] `prefix + R` opens the provider selector modal as a tmux popup
- [ ] `prefix + b → Provider` opens the same modal from the quick menu
- [ ] Modal shows the currently active agent window's role, current CLI, and current model
- [ ] Provider list shows Claude Code, OpenCode, Codex, Local with installed status
- [ ] Model list updates dynamically when switching providers
- [ ] "custom..." option allows freeform model ID entry
- [ ] Local (Ollama) models populated from `ollama list` output
- [ ] Arrow keys / j/k navigate, Space selects, Tab switches sections, Enter confirms
- [ ] "Compact before reload" and "Persist to config" checkboxes functional
- [ ] On confirm: modal closes, `muxcode reload` executes with selected CLI + model
- [ ] Unavailable providers shown greyed out with "(not installed)"
- [ ] Mode-cycled windows resolve to the active role (e.g., research on F1, auto on F2)
- [ ] TUI uses Dracula palette from `tui/styles.go`, no external dependencies

### Phase 6: Integration tests and docs

New files:

| File | Purpose |
|------|---------|
| `scripts/test-hot-reload.sh` | Integration test: reload build agent from claude to opencode, verify config, verify alive |

Updated files:

| File | Change |
|------|--------|
| `CLAUDE.md` | Add `muxcode reload` to command table, document runtime overrides, add `prefix + R` keybinding |
| `docs/agents.md` | Add hot reload section: usage, examples, mode-cycled agent behavior, provider selector modal |
| `docs/configuration.md` | Add runtime override resolution chain, `muxcode config set/get` reference |
| `docs/agent-bus.md` | Add `reload` and `provider-select` subcommand references |

Success criteria:
- [ ] Integration test passes: reload build agent between providers
- [ ] `muxcode reload build --cli opencode` completes in <20 seconds
- [ ] Provider selector modal opens, selects, and reloads successfully
- [ ] Inbox messages preserved across reload (bus identity unchanged)
- [ ] Memory preserved across reload (separate from agent process)
- [ ] Workflow state preserved (daemon-managed, not process-managed)
- [ ] Console viewer (pane 0) unaffected by reload
- [ ] Documentation updated with examples, keybindings, and resolution chain

## Configuration

| Env var | Default | Description |
|---------|---------|-------------|
| `MUXCODE_{ROLE}_CLI` | varies by role | CLI provider for a role (claude, opencode, codex, local) |
| `MUXCODE_{ROLE}_MODEL` | varies by provider | Model override for a role on the active provider |
| `MUXCODE_{ROLE}_CLAUDE_MODEL` | varies by role | Claude Code model for a role (when CLI is claude) |
| `MUXCODE_AGENT_CLI` | (none) | Session-wide CLI default |

**Quick start**:

```bash
# Open the provider selector modal (visual — targets active window)
# prefix + R  (or prefix + b → Provider)

# CLI: switch build agent to OpenCode + DeepSeek mid-session
muxcode reload build --cli opencode --model opencode-go/deepseek-v4-pro

# CLI: switch edit agent to Claude Opus 4.7
muxcode reload edit --model claude-opus-4-7

# Persist a change for future sessions
muxcode config set build.cli opencode
muxcode config set build.model opencode-go/deepseek-v4-pro

# View effective config
muxcode config get build
```

## Known limitations

| Limitation | Impact | Mitigation |
|------------|--------|------------|
| Conversation context lost | Active Claude Code / OpenCode conversation is not preserved across reload | Use `--compact` flag or "Compact before reload" checkbox; session memory is preserved |
| Brief agent downtime | Agent is unavailable for 5-15 seconds during reload | Messages received during reload are queued in inbox; daemon suppresses health alerts |
| Console viewer not restarted | Console in pane 0 keeps running with old role config | Console auto-detects role from pane — no restart needed |
| `--all` is sequential | Reloading all agents takes N × 15s | Intentional — parallel reload could overwhelm the system |
| No rollback | If the new config fails to launch, manual intervention is needed | Verify launch (15s timeout) and alert on failure; original config is still in env vars |
| Edit agent caution | Reloading edit agent loses the orchestrator's conversation context | Require explicit `muxcode reload edit` (excluded from `--all`); recommend `--compact` |
| Static model lists | Provider model lists are hardcoded (except Ollama) | "custom..." option allows freeform model entry; update model lists in code as new models release |
| Modal targets active window | If user switches windows between opening modal and confirming, the wrong agent reloads | Resolve target at modal open time and display it prominently; the role is locked once the TUI starts |

## Dependencies

| Dependency | Purpose | Status |
|------------|---------|--------|
| `bus/provider.go` | Provider resolution, `ResolveProviderCLI()` | Existing (needs update) |
| `bus/launch.go` | Launch config resolution | Existing (needs update) |
| `bus/config.go` | Path helpers | Existing (needs update) |
| `bus/agent_health.go` | Health check exclusions | Existing (needs update) |
| `bus/health.go` | `RestartLocalAgent()` — reused/extended by reload | Existing |
| `bus/mode.go` | Mode cycle state, `ActiveModeRole()` for target resolution | Existing (read only) |
| `bus/modal.go` | Modal registration and popup management | Existing (needs update) |
| `tui/styles.go` | Dracula color palette for provider selector TUI | Existing (read only) |
| `daemon/daemon.go` | Health check and idle check loops | Existing (needs update) |
| `cmd/launch.go` | `Launch()` — reused by reload for relaunch | Existing |
| `config/tmux.conf` | Status bar menu and keybindings | Existing (needs update) |

## Status

Draft
