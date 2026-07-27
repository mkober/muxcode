# Multi-agent reload

Extend the Provider Selector modal and `muxcode reload` CLI to support switching multiple agents to a different provider and model in a single operation. When a provider goes down (API outage, rate limiting, service degradation), the user needs to migrate affected agents to a working provider without opening the modal on each window individually.

## Context

### Current state

| Aspect | Current behavior |
|--------|-----------------|
| Provider selector modal | Targets the single currently active agent window — resolved via `tmux display-message -p #{window_name}` at modal open time |
| `muxcode reload <role>` | Reloads one agent at a time with optional `--cli` and `--model` overrides |
| `muxcode reload --all` | Reloads all active agents sequentially (5s gap) but does **not** support `--cli` or `--model` flags — only re-reads existing config |
| Bulk provider switching | Not supported — requires opening the modal on each window or running N separate `muxcode reload` commands |
| Provider outage response | Manual per-agent reload — 10+ agents means 10+ modal interactions or CLI commands |

### Problem

When a provider goes down, every agent using that provider becomes non-functional. The current system requires per-agent intervention:

1. **Slow recovery** — switching 8 agents from Claude to OpenCode requires 8 separate modal interactions or 8 CLI commands, each taking 10-20 seconds for the reload cycle
2. **Error-prone** — easy to miss an agent or misconfigure one when doing repetitive per-agent reloads under time pressure
3. **No visibility** — no single view showing which agents are on which provider, making it hard to assess the blast radius of an outage
4. **`--all` is incomplete** — `ReloadAll()` iterates agents but doesn't accept CLI/model overrides, so it can't switch providers — it only refreshes the current config

Users need to:
- See at a glance which agents are running on which provider/model
- Select multiple agents and switch them all to a different provider/model in one action
- Quickly respond to provider outages by migrating all affected agents in bulk
- Filter agent selection by current provider (e.g., "select all Claude agents")

### Goal

1. Extend the Provider Selector modal with a multi-agent selection section — checkboxes for each active agent showing their current provider/model, with select-all and select-by-provider shortcuts
2. Extend `muxcode reload` to accept multiple roles and support `--cli`/`--model` with `--all`
3. Show reload progress in the modal as agents are switched sequentially
4. Provide a CLI equivalent for scripted bulk reloads (`muxcode reload build test review --cli opencode --model ...`)

## Design

### 1. Extended provider selector TUI

The existing Provider Selector modal gains a new **Agents** section between the Options and Buttons sections. The flow becomes: select a provider → select a model → select which agents to reload → set options → confirm.

#### TUI layout

```
┌─────────── Provider Selector ───────────┐
│                                         │
│  Current window: build (F3)             │
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
│    ○ claude-opus-5                    │
│    ● claude-sonnet-5                  │
│    ○ claude-haiku-4-5                   │
│    ○ custom...                          │
│                                         │
│  ─── Agents ────────────────────────    │
│                                         │
│    [ ] Plan      claude / opus-5   F1 │
│    [ ] Research  opencode / deepseek F1 │
│    [ ] Edit      claude / opus-5   F2⚠│
│    [ ] Auto      claude / sonnet-5 F2⚠│
│    [x] Build     claude / sonnet-5 F3 │
│    [ ] Test      opencode / minimax  F4 │
│    [ ] Serve     opencode / minimax  F5 │
│    [ ] Review    claude / opus-5   F6 │
│    [ ] Deploy    opencode / minimax  F7 │
│    [ ] Run       opencode / minimax  F8 │
│    [ ] Watch     opencode / minimax  F9 │
│    [ ] Commit    opencode / minimax  F10│
│    ─────────────────────────────────    │
│    (a) All  (p) By provider  (n) None   │
│                                         │
│  ─── Options ───────────────────────    │
│                                         │
│    [ ] Compact before reload            │
│    [ ] Persist to config                │
│                                         │
│  [ Reload 1 agent ]  [ Cancel ]         │
│                                         │
│  ↑↓ Navigate  ␣ Select  ⏎ Reload       │
│  Tab Section  a All  p Provider  q Quit │
└─────────────────────────────────────────┘
```

When multiple agents are selected, the Reload button updates dynamically: `[ Reload 5 agents ]`.

#### Agent list population

The Agents section lists all active (alive) agents in the session, excluding only:
- Hosted roles (`docs`, `pr-read`) — share their host's process, not independently reloadable

All other agents are shown, including mode-cycled agents (`plan`, `research`, `edit`, `auto`). `ReloadTarget()` already handles mode-cycled agents correctly — active mode targets the host window pane, inactive mode targets the hold window.

**Safety indicators**: `edit` and `auto` (the orchestrator and its autonomous mode) are shown with a `⚠` warning indicator since reloading them disrupts the active conversation. They are selectable but excluded from `a` (select all) — the user must explicitly check them. This replaces the previous hard exclusion from `ReloadAll` with a soft guardrail that doesn't prevent the user from switching them during an outage.

Each agent row shows:
- Checkbox (`[x]` / `[ ]`) for selection
- Role name (padded to 8 chars)
- Current CLI / abbreviated model (e.g., `claude / sonnet-5`)
- `⚠` suffix for edit/auto (orchestrator warning)
- `(dead)` suffix for agents that are not alive (greyed out, not selectable)

The current window's agent is pre-selected by default (preserves existing single-agent workflow).

#### Agent selection shortcuts

| Key | Action | Context |
|-----|--------|---------|
| `a` | Select all agents (excludes edit/auto — must be checked individually) | Agents section only |
| `n` | Deselect all agents | Agents section only |
| `p` | Toggle all agents matching the selected provider's CLI | Agents section — e.g., if Claude is the selected provider, `p` selects all agents currently on Claude |
| `Space` | Toggle individual agent | Agents section |

The `p` shortcut is the key workflow accelerator: user selects the target provider (e.g., OpenCode), selects the model, tabs to Agents, presses `p` to select all agents currently on the failing provider (e.g., all Claude agents), then confirms. This covers the "provider outage" scenario in 5 keypresses.

#### Backward compatibility

When only one agent is selected (the default — the current window's agent), the behavior is identical to the current single-agent reload. The Agents section is pre-collapsed to a single line showing the current agent, expanding on Tab/Enter. This keeps the common case fast.

### 2. Extended `muxcode reload` CLI

The CLI gains multi-role support and `--cli`/`--model` with `--all`:

```bash
# Existing (unchanged)
muxcode reload build                                    # reload one agent
muxcode reload build --cli opencode --model deepseek    # reload one with overrides

# New: multiple roles
muxcode reload build test review --cli opencode --model opencode-go/minimax-m2.5

# New: --all with overrides
muxcode reload --all --cli opencode --model opencode-go/minimax-m2.5

# New: --provider filter (only reload agents currently on the specified CLI)
muxcode reload --all --provider claude --cli opencode --model opencode-go/minimax-m2.5
```

#### `--provider` filter

The `--provider` flag filters the agent list to only those currently running on the specified CLI. Combined with `--all`, this enables targeted bulk migration:

```bash
# Switch all Claude agents to OpenCode (leaves OpenCode agents untouched)
muxcode reload --all --provider claude --cli opencode --model opencode-go/minimax-m2.5
```

Without `--provider`, `--all` reloads every active agent (existing behavior, now with override support).

### 3. Batch reload execution

Sequential reload with configurable gap, progress reporting, and failure isolation:

```go
// ReloadBatch reloads multiple agents sequentially with CLI/model overrides.
// Returns per-agent results. Continues on individual failures.
func ReloadBatch(session string, roles []string, cli, model string, compact bool) []ReloadResult {
    var results []ReloadResult
    for i, role := range roles {
        if i > 0 {
            time.Sleep(3 * time.Second) // reduced from 5s for batch speed
        }
        err := ReloadAgent(session, role, cli, model, compact)
        results = append(results, ReloadResult{
            Role:    role,
            Success: err == nil,
            Error:   err,
        })
    }
    return results
}

type ReloadResult struct {
    Role    string
    Success bool
    Error   error
    OldCLI  string
    OldModel string
    NewCLI  string
    NewModel string
}
```

Key behaviors:
- **Sequential** — parallel reload risks overwhelming the system and creates race conditions with daemon health checks
- **Failure isolation** — one agent failing doesn't abort the batch; all agents are attempted
- **Reduced gap** — 3 seconds between agents (down from 5s in `ReloadAll`) since the user has explicitly requested the batch
- **Progress callback** — the TUI receives progress updates to show a live progress indicator

### 4. TUI progress view

After the user confirms a multi-agent reload, the modal transitions from the selection view to a progress view:

```
┌─────────── Provider Selector ───────────┐
│                                         │
│  Reloading 7 agents → opencode          │
│  Model: opencode-go/minimax-m2.5        │
│                                         │
│  ─── Progress ──────────────────────    │
│                                         │
│    ✓ build     claude → opencode   3s   │
│    ✓ test      claude → opencode   4s   │
│    ✓ review    claude → opencode   3s   │
│    ✓ plan      claude → opencode   4s   │
│    ⟳ research  opencode (no change) ... │
│    ○ commit    opencode → opencode      │
│    ○ deploy    opencode (skip)          │
│                                         │
│  ━━━━━━━━━━━━━━━━━░░░░░░░░  5/7         │
│                                         │
│  Press q to close (reload continues)    │
└─────────────────────────────────────────┘
```

Status icons:
- `✓` — reload succeeded (green)
- `✗` — reload failed (red, with error on hover/expand)
- `⟳` — currently reloading (yellow, animated)
- `○` — pending (dim)

Agents already on the target provider/model can be shown as `(skip)` or `(no change)` — the user can decide at selection time whether to include them.

The progress view runs the reload in a goroutine and updates the TUI on each completion. Pressing `q` closes the modal but does **not** cancel in-progress reloads — the `ReloadBatch` function continues in the background.

### 5. Agent status data layer

New function to build the agent list for the TUI:

```go
// AgentReloadStatus describes an agent's current provider/model for the selector TUI.
type AgentReloadStatus struct {
    Role         string
    Window       string
    CLI          string  // current provider CLI
    Model        string  // current model
    Alive        bool    // true if agent process is alive
    Orchestrator bool    // true for edit/auto — shown with ⚠, excluded from "select all"
    FKey         string  // F-key label (e.g., "F3")
}

// ActiveAgentStatuses returns reload status for all agents in the session.
// Includes all agents: standard, mode-cycled (plan, research, edit, auto).
// Excludes only hosted roles (docs, pr-read) which share their host's process.
func ActiveAgentStatuses(session string) []AgentReloadStatus {
    var statuses []AgentReloadStatus
    for _, role := range reloadableRoles() {
        window := WindowForRole(role)
        cli := ResolveProviderCLI(role)
        rc := EffectiveConfig(role)
        alive := IsAgentAlive(session, role)
        fkey := WindowFKey(session, window)

        statuses = append(statuses, AgentReloadStatus{
            Role:         role,
            Window:       window,
            CLI:          cli,
            Model:        rc.Model,
            Alive:        alive,
            Orchestrator: role == "edit" || role == "auto",
            FKey:         fkey,
        })
    }
    return statuses
}

// reloadableRoles returns roles eligible for reload.
// Includes all agents including mode-cycled (plan, research, edit, auto).
// Excludes only hosted roles (docs, pr-read) which share their host's process.
func reloadableRoles() []string {
    var roles []string
    for _, role := range KnownRoles {
        if WindowForRole(role) != role {
            continue // hosted role (docs, pr-read)
        }
        roles = append(roles, role)
    }
    return roles
}
```

### 6. Model abbreviation

Full model IDs are long (`opencode-go/minimax-m2.5`, `claude-sonnet-5`). The agent list needs abbreviated display:

```go
// AbbreviateModel shortens a model ID for compact display.
// "claude-sonnet-5" → "sonnet-5"
// "opencode-go/minimax-m2.5" → "minimax-m2.5"
// "gpt-5.5" → "gpt-5.5" (already short)
func AbbreviateModel(model string) string {
    // Strip common prefixes
    if i := strings.LastIndex(model, "/"); i >= 0 {
        return model[i+1:]
    }
    if strings.HasPrefix(model, "claude-") {
        return model[len("claude-"):]
    }
    return model
}
```

### Architecture diagram

```
User presses prefix + R (or prefix + b → Provider)
      │
      ▼
┌──────────────────────────────────────────────────┐
│  Provider Selector Modal (tmux display-popup)    │
│                                                  │
│  1. Select target provider (radio list)          │
│  2. Select target model (radio list + custom)    │
│  3. Select agents to reload (checkbox list)      │
│     └─ Pre-populated with ActiveAgentStatuses()  │
│     └─ Current window agent pre-selected         │
│     └─ Shortcuts: (a)ll, (p)rovider, (n)one      │
│  4. Options: compact, persist                    │
│  5. Confirm → Reload                             │
└──────────────┬───────────────────────────────────┘
               │
               ▼
         ┌─────────────┐
         │ Single agent?│
         └──┬───────┬───┘
          Yes       No
            │       │
            ▼       ▼
    ExecuteReload  ReloadBatch(session, roles, cli, model)
    (existing)       │
                     ├─ ReloadAgent(role1) → result
                     │   sleep 3s
                     ├─ ReloadAgent(role2) → result
                     │   sleep 3s
                     ├─ ReloadAgent(role3) → result
                     │   ...
                     ▼
                  Progress view (TUI updates live)
                     │
                     ▼
                  Summary + close modal
```

```
CLI equivalent:

muxcode reload build test review --cli opencode --model opencode-go/minimax-m2.5
      │
      ├─ Parse multiple roles from args
      ├─ Validate all roles exist and are alive
      │
      ▼
  ReloadBatch(session, ["build","test","review"], "opencode", "minimax-m2.5", false)
      │
      ├─ ReloadAgent("build")  → ✓ claude → opencode  (3s)
      ├─ sleep 3s
      ├─ ReloadAgent("test")   → ✓ claude → opencode  (4s)
      ├─ sleep 3s
      ├─ ReloadAgent("review") → ✓ claude → opencode  (3s)
      │
      ▼
  Print summary table to stdout
```

### Relationship to existing features

| Feature | Interaction |
|---------|------------|
| Single-agent reload (`muxcode reload <role>`) | Unchanged — single role with no agent section behaves identically to current modal |
| `ReloadAll()` | Refactored to use `ReloadBatch()` internally — gains `--cli`/`--model` support |
| Provider selector modal | Extended with Agents section — backward compatible (single agent pre-selected by default) |
| `ExecuteReload()` in TUI | Extended to handle multi-agent case — delegates to `ReloadBatch()` when >1 agent selected |
| `ReloadAgent()` | Unchanged — `ReloadBatch()` calls it per-agent |
| Daemon health checks | Already suppressed via reload markers — works for batch (each agent gets its own marker) |
| `EffectiveConfig()` / `ResolveProviderCLI()` | Read by `ActiveAgentStatuses()` to populate the agent list |
| Lifecycle logging | Each individual reload is logged (existing behavior) — batch adds a summary lifecycle event |
| `--compact` flag | Applied to each agent in the batch individually |
| Mode-cycled agents | Included in agent list — `ReloadTarget()` handles active vs hold window targeting. `edit`/`auto` shown with `⚠` warning and excluded from "select all" shortcut |

## Implementation

### Phase 1: Agent status data layer and model abbreviation

New files:

| File | Purpose |
|------|---------|
| `bus/reload_batch.go` | `AgentReloadStatus`, `ActiveAgentStatuses()`, `reloadableRoles()`, `ReloadResult`, `ReloadBatch()`, `AbbreviateModel()` |
| `bus/reload_batch_test.go` | Tests for `reloadableRoles()`, `AbbreviateModel()`, `ReloadResult` formatting |

Updated files:

| File | Change |
|------|--------|
| `bus/reload.go` | Refactor `ReloadAll()` to delegate to `ReloadBatch()` with empty cli/model (backward compatible) |

Success criteria:
- [x] `reloadableRoles()` returns all agent roles excluding only hosted roles (docs, pr-read)
- [x] `ActiveAgentStatuses()` returns current CLI/model/alive/orchestrator status for all reloadable agents including plan, research, edit, auto
- [x] `AbbreviateModel("claude-sonnet-5")` returns `"sonnet-5"`
- [x] `AbbreviateModel("opencode-go/minimax-m2.5")` returns `"minimax-m2.5"`
- [x] `ReloadBatch()` calls `ReloadAgent()` sequentially with 3s gap, returns per-agent results
- [x] `ReloadBatch()` continues on individual failures (failure isolation)
- [x] `ReloadAll()` delegates to `ReloadBatch()` (no behavior change)

### Phase 2: CLI multi-role and `--provider` filter

Updated files:

| File | Change |
|------|--------|
| `cmd/reload.go` | Accept multiple role positional args, add `--provider` flag to filter by current CLI |
| `bus/reload.go` | Update `ReloadAll()` to accept `cli`, `model`, and `providerFilter` parameters |

Success criteria:
- [x] `muxcode reload build test review --cli opencode` reloads three agents with CLI override
- [x] `muxcode reload --all --cli opencode --model minimax` reloads all active agents with overrides
- [x] `muxcode reload --all --provider claude --cli opencode` reloads only agents currently on Claude
- [x] `--provider` with no `--all` is rejected with an error message
- [x] Progress output printed to stdout: per-agent status line as each completes
- [x] Summary line at end: `"Reloaded 5/5 agents successfully (opencode / minimax-m2.5)"`
- [x] Failed agents reported with error reason, non-zero exit code if any failed

### Phase 3: TUI agents section

Updated files:

| File | Change |
|------|--------|
| `tui/provider_select.go` | Add `sectionAgents` (index 2, shifting Options to 3, Buttons to 4). Add `agentChecks []bool`, `agents []AgentReloadStatus` fields. Implement agent list rendering, checkbox toggle, select-all/by-provider/none shortcuts. Update `numSections` to 5. Dynamic Reload button text (`Reload N agents`). |
| `tui/provider_select.go` | Update `moveUp()`, `moveDown()`, `selectCurrent()`, `syncCursorToSection()` for the new section. Add `handleAgentKey()` for `a`/`p`/`n` shortcuts. |
| `bus/provider_options.go` | Add `AbbreviateModel()` (if not placed in `reload_batch.go`) |

Success criteria:
- [x] Agents section shows all reloadable agents including plan, research, edit, auto with current CLI / abbreviated model
- [x] `edit` and `auto` shown with `⚠` warning indicator (orchestrator)
- [x] Current window's agent pre-selected by default
- [x] `Space` toggles individual agent checkbox (including edit/auto)
- [x] `a` selects all agents except edit/auto (orchestrator safety), `n` deselects all
- [x] `p` toggles all agents currently on the same CLI as the selected provider (including edit/auto if they match)
- [x] Reload button shows dynamic count: `[ Reload 1 agent ]` / `[ Reload 5 agents ]`
- [x] Dead agents shown greyed out with `(dead)` suffix, not selectable
- [x] Mode-cycled agents (plan↔research, edit↔auto) reload correctly via `ReloadTarget()`
- [x] Tab/Shift-Tab navigates through all 5 sections
- [x] Arrow keys cross section boundaries correctly

### Phase 4: TUI progress view

Updated files:

| File | Change |
|------|--------|
| `tui/provider_select.go` | Add `renderProgress()` method, `reloadResults` channel, progress state tracking. Transition from selection view to progress view on confirm. Run `ReloadBatch()` in goroutine, update TUI on each result. |
| `tui/provider_select.go` | Update `Run()` to return after progress view completes (all reloads done or user presses `q`). When `q` pressed during progress, close modal but don't cancel reloads. |

Success criteria:
- [x] On confirm with >1 agent, TUI transitions to progress view
- [x] Progress view shows per-agent status: `✓` success, `✗` failure, `⟳` in-progress, `○` pending
- [x] Progress bar shows `N/M` completion count
- [x] Each agent result appears as soon as its reload completes (live update)
- [x] `q` closes the modal without canceling in-progress reloads
- [x] On confirm with exactly 1 agent, behavior is unchanged (existing `ExecuteReload`)
- [x] Error details shown inline for failed agents

### Phase 5: Integration tests and docs

New files:

| File | Purpose |
|------|---------|
| `scripts/test-multi-reload.sh` | Integration test: reload 3 agents in batch via CLI, verify all switch providers |

Updated files:

| File | Change |
|------|--------|
| `CLAUDE.md` | Add multi-role reload syntax, `--provider` flag, batch reload TUI description |
| `docs/agents.md` | Add bulk reload section with examples and TUI screenshot description |
| `docs/agent-bus.md` | Update `reload` subcommand reference with multi-role and `--provider` syntax |

Success criteria:
- [x] Integration test passes: `muxcode reload build test review --cli opencode` completes <60s
- [x] `muxcode reload --all --provider claude --cli opencode` correctly filters and reloads
- [x] Provider selector modal opens with Agents section, multi-select works, progress view renders
- [x] Single-agent workflow unchanged (backward compatible)
- [x] Documentation updated with new CLI syntax and TUI screenshots

## Configuration

No new env vars required. Existing configuration is sufficient:

| Aspect | Mechanism |
|--------|-----------|
| Batch gap timing | Hardcoded 3s (reduced from `ReloadAll`'s 5s) — could be configurable via `MUXCODE_RELOAD_BATCH_GAP` if needed |
| Reloadable roles | Derived from `KnownRoles` minus edit, auto, hosted — no config needed |
| `--provider` filter | Reads current CLI from `ResolveProviderCLI()` — uses existing resolution chain |
| Persist to config | Existing `SetShellConfigValue()` — writes each role's override to the config file |

## Known limitations

| Limitation | Impact | Mitigation |
|------------|--------|------------|
| Sequential reload | Batch of 8 agents takes ~40s (8 × 3s gap + reload time) | Intentional — parallel reload risks race conditions with daemon, tmux, and provider-specific config files |
| Edit/auto soft guardrail | `a` (select all) skips edit and auto to prevent accidental orchestrator reload | User can still explicitly check edit/auto — the `⚠` indicator and select-all exclusion are soft guardrails, not hard blocks |
| No rollback on batch failure | If agent 4 of 8 fails, agents 1-3 are already on the new provider | Each agent reload is atomic — failed agents stay on their old config. User can re-run the batch or fix individually |
| Modal size | More agents = taller modal, may not fit in small terminals | Scrollable agent list if >8 agents; modal height auto-adjusts up to 80% |
| Progress view is fire-and-forget | Closing the modal doesn't cancel reloads | Intentional — reloads are fast and idempotent, canceling mid-batch would leave agents in mixed state |

## Dependencies

| Dependency | Purpose | Status |
|------------|---------|--------|
| `bus/reload.go` | `ReloadAgent()` — called per-agent by `ReloadBatch()` | Existing |
| `bus/provider.go` | `ResolveProviderCLI()` — reads current provider for agent list and `--provider` filter | Existing |
| `bus/launch.go` | `EffectiveConfig()` — reads current model for agent list display | Existing |
| `bus/agent_health.go` | `IsAgentAlive()` — checks agent liveness for agent list | Existing |
| `bus/config.go` | `KnownRoles`, `WindowForRole()` — determines reloadable roles | Existing |
| `bus/provider_options.go` | `WindowFKey()`, `ResolveActiveAgentWindow()` — agent list metadata | Existing |
| `tui/provider_select.go` | Provider selector TUI — extended with Agents section and progress view | Existing (needs update) |
| `tui/styles.go` | Dracula palette, `ClearFrame()`, `Pad()`, `TruncateAnsi()` | Existing |
| `cmd/reload.go` | Reload CLI command — extended with multi-role args and `--provider` flag | Existing (needs update) |

## Status

Complete — all phases implemented, build passing, 171 tests green
