# API Agent Modal Window

Convert the API agent from a dedicated tmux window to an on-demand modal managed by a new modal window manager. The modal manager is a general-purpose system for launching overlay windows — the API agent is its first consumer, but the infrastructure supports future muxcode features that benefit from on-demand modal windows.

## Context

The API window is one of 10 windows created at session launch. It follows the standard split-left layout: console view (left) + Claude Code agent (right). Unlike build/test/review/commit which participate in automated chains, the API agent is only invoked on-demand via `muxcode send api api "..."`.

### Current state

| Aspect | Current behavior |
|--------|-----------------|
| Window | `api` — permanent, created at launch |
| Layout | Split-left: `muxcode console api` (left) + `muxcode agent launch api` (right) |
| F-key | Occupies F2 slot in window order |
| Agent lifecycle | Always running, consuming Claude Code session |
| Console view | `renderAPI` in console.go — shows request history, status codes, durations |
| History | `.muxcode/api/history.jsonl` — persists across sessions |
| Tool profile | `api` in profile.go — curl, wget, jq, python, PII scrubbing |

### Problems

1. **Resource waste** — the API agent runs continuously but is used rarely; Claude Code sessions have cost/concurrency limits
2. **Window clutter** — 10 windows is a lot to navigate; removing one improves F-key ergonomics
3. **No chain integration** — API never participates in build/test/review chains, making it a natural candidate for on-demand lifecycle
4. **No modal infrastructure** — several future features (log viewers, memory browsers, interactive tools) would benefit from managed modal windows, but there's no reusable system for them

## Design

### Modal window manager

A new `bus/modal.go` module that manages tmux modal lifecycles. Modals are defined declaratively via `ModalConfig` structs and launched/tracked by the manager.

```go
type ModalConfig struct {
  Name    string // unique identifier (e.g. "api", "logs", "memory")
  Title   string // tmux modal title bar text
  Width   string // modal width (e.g. "85%")
  Height  string // modal height (e.g. "80%")
  Command string // primary command to run in the modal
  Split   *ModalSplit // optional pane split
  Sizes   map[string][2]string // size presets: "compact"->["60%","50%"], "full"->["95%","95%"]
  Role    string // bus role name for inbox routing (empty = no bus integration)
}

type ModalSplit struct {
  Direction string // "v" (vertical) or "h" (horizontal)
  Size      string // size of the secondary pane (e.g. "20%")
  Command   string // command for the secondary pane
  Primary   string // "top" or "bottom" / "left" or "right" — which pane gets focus
}
```

#### Manager responsibilities

| Capability | Description |
|------------|-------------|
| Registry | Named modal configs registered at init — `RegisterModal(config)` |
| Launch | `OpenModal(session, name)` — opens the modal via `tmux display-popup` |
| Toggle | `OpenModal()` acts as toggle — closes if already open, opens if closed |
| Bus integration | `OpenModalWithInbox(session, name)` — delivers pending inbox then opens modal |
| Detection | `IsModalOpen(session, name)` — checks if a modal is currently displayed |
| Promotion | `prefix + !` inside modal promotes it to a full tmux window; manager tracks promoted state |
| Fallback | `OpenOrSpawn(session, name)` — opens modal if client attached, spawns headless if not |
| Listing | `ListModals()` — returns all registered modal configs |

#### CLI subcommand

```
muxcode modal open <name>           # open (or toggle closed) a registered modal
muxcode modal open <name> --size compact  # open with size preset
muxcode modal list                  # list registered modals
muxcode modal status <name>         # check if modal is open/promoted/closed
```

### API modal layout

Vertical split: agent on top (80%), console on bottom (20%). Default size follows the golden ratio (~62%) relative to the terminal, centering the modal over the underlying window so the parent context remains visible around the edges.

```
+--------------------------------------------------------------+
| underlying tmux window (visible border)                      |
|                                                              |
|   +--------------------------------------------------+       |
|   |  tmux display-popup -E -w 62% -h 62%            |       |
|   |  -T ' API Testing '                              |       |
|   |                                                   |       |
|   |  +----------------------------------------------+ |       |
|   |  | claude agent (api role)                      | |       |
|   |  |                                              | |       |
|   |  |                                              | |       |
|   |  |                                              | |       |
|   |  +----------------------------------------------+ |       |
|   |  | console (request history, status)            | |       |
|   |  +----------------------------------------------+ |       |
|   +--------------------------------------------------+       |
|                                                              |
+--------------------------------------------------------------+
```

Registered as:
```go
RegisterModal(ModalConfig{
  Name:    "api",
  Title:   " API Testing ",
  Width:   "62%",
  Height:  "62%",
  Command: "muxcode agent launch api",
  Split: &ModalSplit{
    Direction: "v",
    Size:      "20%",
    Command:   "muxcode console api",
    Primary:   "top",
  },
  Sizes: map[string][2]string{
    "compact": {"50%", "40%"},
    "full":    {"95%", "95%"},
  },
  Role: "api",
})
```

### Usability features

These features apply to all modals managed by the window manager, not just the API modal.

#### Toggle open/close

Pressing the same keybinding that opened a modal closes it. `muxcode modal open api` acts as a toggle — if the API modal is already open, it closes it instead of opening a second instance. This makes keybindings feel natural (press `prefix + i` to open, press again to dismiss).

#### Promote to window

`prefix + !` inside a modal (tmux's default break-pane) promotes the modal to a full tmux window. Useful when the user needs more space or wants to keep the agent running while switching to other windows. The modal manager tracks the promotion so `IsModalOpen()` still returns true and bus routing continues to work.

#### Configurable size

Modal size is resolved in priority order:

1. **CLI flag** — `muxcode modal open api --size 80%x70%` (explicit WxH)
2. **CLI preset** — `muxcode modal open api --size full` (named preset)
3. **Environment variable** — `MUXCODE_MODAL_SIZE_API=80%x70%` (per-modal override)
4. **Config default** — `Width` and `Height` from the `ModalConfig` struct

The API modal defaults to the golden ratio (~62% width, ~62% height), keeping the parent window visible around all edges for context.

Built-in presets per modal are defined in the `Sizes` map. API defaults:

| Preset | Width | Height | Use case |
|--------|-------|--------|----------|
| `default` | 62% | 62% | Golden ratio — parent window visible |
| `compact` | 50% | 40% | Quick lookups |
| `full` | 95% | 95% | Extended sessions |

#### Dracula-themed border

Modal borders use the Dracula purple (`colour141`) to match the existing console theme. Set via `display-popup -b rounded -S fg=colour141` on tmux 3.3+, with fallback to default borders on older versions.

#### Pane zoom

`prefix + z` (tmux's built-in pane zoom) toggles the focused pane to fill the entire modal. In a split modal, this lets the user zoom the agent pane to full modal size for extended interaction, then `prefix + z` again to restore the split. Works natively — no custom implementation needed.

#### Pane focus switching

Inside a split modal, `prefix + Up/Down` switches focus between the primary and secondary panes (agent and console). Standard tmux pane navigation (`Ctrl-j/k` via vim-tmux-navigator) also works within the modal.

#### Activity indicator

When a modal role has unread inbox messages and the modal is closed, the status bar shows a notification indicator. Uses the existing `display-message` notification path — the indicator appears briefly, not persistently, matching how other agent notifications work.

#### Scroll support

The console pane (secondary) supports tmux copy-mode scrollback. Users can enter scroll mode with `prefix + [` while focused on the console pane to review older request history beyond what the console renderer shows.

#### Environment variable

`MUXCODE_MODAL=1` is set inside modal shells, similar to `TMUX_POPUP=1` for popups. Allows commands to detect they're running inside a modal and adjust behavior (e.g. disable nested modals, use inline selection for fzf).



1. **Menu / keybinding** — user opens modal manually for interactive API work
2. **Auto-modal on delegation** — edit agent sends `muxcode send api api "..."`, modal opens automatically with the request pre-loaded in inbox
3. **Headless fallback** — if no tmux client is attached, falls back to headless spawn

---

### Phase 1: Modal window manager

**Goal:** Build the reusable modal manager infrastructure.

#### New files

| File | Purpose |
|------|---------|
| `bus/modal.go` | `ModalConfig`, `ModalSplit`, registry, `OpenModal()`, `IsModalOpen()`, `OpenOrSpawn()`, `ListModals()` |
| `bus/modal_test.go` | Unit tests for registry, config validation, command building |
| `cmd/modal.go` | CLI handler for `muxcode modal open|list|status` |

#### Changes

| File | Change |
|------|--------|
| `main.go` | Add `modal` subcommand dispatch |

#### Implementation notes

- `OpenModal()` builds the `tmux display-popup -E` command from the config
- For split modals: the command is a shell script that runs the primary command, splits, runs the secondary command, and selects the primary pane
- `IsModalOpen()` tracks state via PID file at `BusDir/modals/<name>.pid` — tmux doesn't expose popup state directly
- Registry populated in `DefaultModalConfigs()` — returns built-in configs (api initially), extensible for future modals

### Phase 2: API agent migration

**Goal:** Remove `api` from the default window list, wire it to the modal manager.

#### Changes

| File | Change |
|------|--------|
| `bus/launcher.go` | Remove `api` from `DefaultLauncherConfig().Windows` and `SplitLeft` |
| `bus/modal.go` | Register API modal config in `DefaultModalConfigs()` |
| `config/tmux.conf` | Add "API Testing" menu entry via `muxcode modal open api`; add `bind i` shortcut |
| `bus/config.go` | Add `IsModalRole()` — checks if role is registered as a modal |

#### Menu placement

Insert after Dashboard, before separator:

```
"Dashboard"               t "display-popup -E -w 80% -h 80% -T ' Dashboard ' 'muxcode dashboard'"
"API Testing"             i "run-shell 'muxcode modal open api'"
""
```

#### F-key reflow

With `api` removed, the default window order becomes:
```
edit(1) build(2) test(3) review(4) deploy(5) run(6) watch(7) commit(8) analyze(9)
```

#### Keybinding

```
bind i run-shell 'muxcode modal open api'
```

### Phase 3: Auto-modal on bus delegation

**Goal:** When the edit agent sends `muxcode send api api "..."`, automatically open the API modal with the request delivered to the agent's inbox.

#### Changes

| File | Change |
|------|--------|
| `cmd/send.go` | For modal roles: deliver message to inbox, then call `OpenOrSpawn()` to open modal or spawn headless |
| `bus/notify.go` | For modal roles with no active window: call `OpenModal()` instead of `send-keys` wake-up |

#### Auto-modal flow

When `muxcode send api api "Run the get-users request"` is sent:
1. Write message to `api` inbox (normal path)
2. Check if modal is already open → if yes, notify agent normally
3. If not open → `OpenOrSpawn(session, "api")`:
   - Client attached → open modal, agent starts and reads inbox
   - No client → `StartSpawn("api", message)` for headless execution
4. Result delivered to sender's inbox as a response
5. Modal stays open for further interaction (user can close with Escape)

### Phase 4: Documentation + agent updates

**Goal:** Update docs and agent definitions to reflect the new modal system.

#### Changes

| File | Change |
|------|--------|
| `CLAUDE.md` | Add `bus/modal.go` to code reference, update window list, note modal manager |
| `docs/agent-bus.md` | Add `modal` subcommand documentation |
| `docs/architecture.md` | Add modal manager to system diagram |
| `agents/code-editor.md` | Update API delegation notes — behavior unchanged but routing is now modal-aware |

---

## Success criteria

### Phase 1
- [ ] `ModalConfig` and `ModalSplit` structs defined
- [ ] Registry with `RegisterModal()`, `DefaultModalConfigs()`, `ListModals()`
- [ ] `OpenModal()` builds and executes correct `tmux display-popup` command
- [ ] Split modals create correct pane layout (primary on top, secondary on bottom)
- [ ] `OpenOrSpawn()` falls back to headless spawn when no client attached
- [ ] `muxcode modal open|list|status` CLI works
- [ ] Toggle behavior: `muxcode modal open` closes if already open
- [ ] Size resolution: CLI `--size WxH` > CLI `--size preset` > `MUXCODE_MODAL_SIZE_<NAME>` env > config default
- [ ] Dracula purple border on tmux 3.3+, graceful fallback on older
- [ ] `MUXCODE_MODAL=1` set inside modal shells
- [ ] Unit tests for config validation, command building, registry, toggle logic

### Phase 2
- [ ] `api` window is not created at session launch
- [ ] Bottom menu has "API Testing" entry that opens modal via manager
- [ ] `prefix + i` opens API modal directly
- [ ] Modal runs agent (top, 80%) + console (bottom, 20%)
- [ ] F-key mapping shifts: build=F2, test=F3, etc.
- [ ] Existing `MUXCODE_WINDOWS` override still works (users can add `api` back)

### Phase 3
- [ ] `muxcode send api api "..."` from edit agent auto-opens the API modal
- [ ] Agent processes the request from inbox after modal opens
- [ ] Result returned to sender's inbox via normal response flow
- [ ] If modal is already open, message routes to existing agent
- [ ] Headless fallback via spawn when no tmux client is attached

### Phase 4
- [ ] CLAUDE.md code reference updated
- [ ] agent-bus.md documents modal subcommand
- [ ] architecture.md includes modal manager

## Risks

| Risk | Mitigation |
|------|------------|
| Users with `api` in custom `MUXCODE_WINDOWS` | No breakage — window creation still works if explicitly listed |
| Agent startup time in modal | Claude Code starts in ~2-3s, acceptable for on-demand use |
| Modal closed mid-request | Agent process terminates — same as closing any tmux window; history already written |
| Auto-modal interrupts user flow | Modal is an overlay — dismissible with Escape, doesn't change active window |
| No attached client for auto-modal | Headless spawn fallback ensures bus delegation always works |
| tmux modal state detection | Track via PID files since tmux doesn't expose popup state natively |

## Future modal candidates

The modal manager is designed to support additional features beyond the API agent:

| Modal | Use case |
|-------|----------|
| Log viewer | On-demand CloudWatch / container log tailing |
| Memory browser | Interactive memory search and editing |
| History viewer | Full-screen agent history with filtering |
| Webhook monitor | Live webhook request inspector |
| Cron manager | Interactive cron job management |

## Out of scope

- Converting core agent windows to modals (build, test, review, commit — these participate in chains)
- Persistent modal sessions (modals are ephemeral by design)
- API agent changes (tool profile, agent definition, history format all unchanged)
- Implementing future modal candidates (listed for context only)
