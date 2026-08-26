# The MuxCode Control Pane

A permanent full-width pane at the bottom of **every** agent window — the standing home for global muxcode TUIs, starting with the graph UI. Run state, launches, and waiting gates become *ambient* rather than something you must remember to open, and the pane becomes the place future control surfaces live rather than each one inventing its own popup.

> **Reframed 2026-08-26.** This began as a graph strip on the `edit` window and was widened, after
> a live rollout to all 12 windows, into a general control surface. The change is not cosmetic: the
> default inverts from opt-in to **opt-out**, the pane-index contract now binds on every window
> rather than one, and the pane acquires a second job — hosting surfaces beyond the graph UI. The
> spec is written to the new scope; the filename slug changed with it while the id stayed fixed.

## Context

### The request

Prototyped live on 2026-08-26:

```bash
tmux split-window -vf -l 14 -t <session>:edit "muxcode graph ui"
```

Pane 2, with indices 0 (nvim) and 1 (agent) untouched **because the pane is created last**.

### Why a permanent pane rather than a popup

A popup answers "what is happening?" only when you already suspect something is. The motivating failure for the gate auto-show ([`MUX-105`](../completed/MUX-105-force-respond-escalation.md)) was a gate sitting unnoticed for 37 minutes — the run had paused correctly and the notification had been sent; nobody was looking. Auto-show fixed *that* gate by forcing a popup open. A permanent pane removes the class: the surface is already on screen.

### The pane-index contract is the load-bearing constraint

`AgentPane(window)` returns a hardcoded **`"1"`**, and `PaneTarget()` composes `session:window.1`. Everything that reaches an agent — `Notify`, `ClearAgent`, `deliver`, wake-up injection, the mode cycler — resolves through it.

**Creation order is therefore the contract.** The control pane must be created *after* panes 0 and 1, every time, on every window. A pane created earlier, or a layout operation that renumbers panes, silently repoints every agent message at the wrong pane.

**The reframe multiplies this risk by twelve.** As a single `edit` strip, a creation-order slip broke one window. Rolled out to all 12 agent windows, the same slip breaks *every* agent's delivery at once — and the symptom is not a crash but messages landing in an nvim buffer. The pane-order assertion is therefore not a nicety in the test phase; it is the single check that protects the whole delivery layer, and it must run for **every** window the pane is created on, not a sampled one.

> **Related unfiled lesson.** The handoff cites this as "the MUX-104 rotate-window lesson", but MUX-104 is the `send-keys` dash-payload bug — the rotate-window finding is **not filed in any spec**. It was established during the tmux-bindings work: `select-layout` preserves pane indices, but `rotate-window` does **not** (it flips pane 1 from the agent to nvim, breaking `PaneTarget`/`Notify`/`ClearAgent`/`deliver`). That is why `rotate` was never bound. It survives only in agent memory, which is a poor home for a constraint this load-bearing — **this spec should either carry it or a follow-up should file it**, because the next person to add a layout binding will not know.

### Interplay with gate auto-show

On a window **with** a control pane, forcing a popup open is redundant and intrusive. Instead the pane should **switch itself to the Pending Gates surface** when a new gate appears — a tick observing a waiting gate absent from the previous tick. The popup path remains the fallback for excluded windows and detached sessions.

## Requirements

### Acceptance criteria

- [ ] `LaunchSession` creates the control pane on **every** agent window, **always after panes 0 and 1**, so `AgentPane() == "1"` holds everywhere
- [ ] Default is **on for all windows**; `MUXCODE_CONTROL_PANE_EXCLUDE` names windows to opt out (empty = all windows get one)
- [ ] `MUXCODE_CONTROL_PANE_DISABLE=1` turns the feature off wholesale, restoring the two-pane layout and the popup workflow
- [ ] Height via `MUXCODE_CONTROL_PANE_HEIGHT` (default 14)
- [ ] Border styling is set **globally** in `config/tmux.conf` (`pane-border-status top`, `pane-border-format`, `pane-border-style`), not per-window at creation — 12 windows must not each re-apply it
- [ ] A killed control pane is respawned under the same supervision model as the left-pane pollers; a binary hot-reload restarts it
- [ ] The pane hosts **switchable surfaces**, with the graph UI as the first; adding a second surface must not require a second pane
- [ ] The pane runs the **same** `muxcode graph ui` binary — no forked renderer. The existing height clamp and flat-list fallback already handle 14 rows (pinned by `TestRenderGraphFrame_DeepGraphFallsBack`)
- [ ] On a window **with** a control pane, a newly-waiting gate switches it to the Pending Gates surface and the auto-show popup does **not** also fire
- [ ] On an **excluded** window (or with the feature disabled), auto-show popup behaviour is unchanged
- [ ] `pane-border-format` shows a title only for titled panes; border style is Dracula `colour141`
- [ ] Pane titles are **ALL CAPS**: pane 0 `" NVIM "`, pane 2 `" GRAPH "`
- [ ] **Pane 1's title is never set by the launcher** — the agent CLI self-manages it, live-updating a state glyph (observed on the prototype). Writing a title there would overwrite a live signal
- [ ] The agent pane nonetheless *displays* in the same ALL-CAPS style, via a `pane-border-format` substitution (`s/code-editor/CODE-EDITOR/`) that transforms the CLI's raw title **at render time only**

  > This is the right shape for the problem. The launcher and the CLI both want to own that
  > title; a launcher that sets it wins briefly and then loses every state update, while
  > leaving it raw breaks the visual convention the other two panes establish. Transforming at
  > display keeps the CLI authoritative over content and the launcher authoritative over
  > presentation — neither has to yield, and the live glyph survives.
- [ ] `prefix + b` graph popups keep working for excluded windows and disabled sessions
- [ ] `config/tmux.conf` and `README.md` document the control pane, its config, and the popup fallback

### Technical approach

- **Creation order, enforced not assumed.** The split happens at the end of window setup, and a test asserts pane order `0=nvim, 1=agent, 2=control` **on every window**, rather than trusting the call site to stay last.
- **Respawn.** Mirror the left-pane poller supervision already in the daemon; a dead control pane is a respawn, not an alert. With 12 panes the supervision cost is 12× — worth measuring before assuming it is free.
- **Gate switching.** The pane's tick already re-reads the run store every 2s. Detecting "a waiting gate that was not waiting last tick" is a diff over that existing read — no new polling.
- **Suppressing the redundant popup.** `graph_exec.go`'s auto-show needs to know whether the target window has a control pane. Derive it from the pane's own configuration rather than a second env var, so the two cannot disagree.
- **Borders are global, not per-window.** Set `pane-border-status`/`pane-border-format`/`pane-border-style` once in `config/tmux.conf`. Applying them at creation would re-run 12 times and leave excluded windows inconsistent; a format that shows a title only for titled panes is already inert on two-pane windows.

### Key files

| File | Change |
|------|--------|
| `bus/launch.go` | Create the control pane last on every window; set pane titles |
| `bus/config.go` | `ControlPaneEnabled()`, `ControlPaneExclude()`, `ControlPaneHeight()` |
| `daemon/daemon.go` | Control-pane respawn supervision across all windows |
| `bus/graph_exec.go` | Suppress auto-show popup on control-pane windows |
| `tui/graph_ui.go` | Switch to gates surface on a newly-waiting gate |
| `config/tmux.conf` | Global border status/format/style; document the pane |
| `README.md` | Document the control pane + keybind fallback |
| `scripts/test-control-pane.sh` | Integration test |

## Implementation

### Phase 1: Pane creation and configuration

- [ ] `ControlPaneEnabled()` / `ControlPaneExclude()` / `ControlPaneHeight()` accessors with defaults (on, none excluded, 14)
- [ ] `LaunchSession` creates the pane after panes 0/1 on every non-excluded window
- [ ] Pane titles (` NVIM `, ` GRAPH `); pane 1's title never written by the launcher
- [ ] Global border status/format/style in `config/tmux.conf`, including the `s/code-editor/CODE-EDITOR/` display substitution
- [ ] Unit tests: pane-order assertion **per window**; `MUXCODE_CONTROL_PANE_DISABLE=1` creates no pane anywhere; an excluded window keeps two panes while its siblings get three

### Phase 2: Supervision

- [ ] Respawn a dead control pane under the poller supervision model
- [ ] Binary hot-reload restarts the pane
- [ ] Unit tests: respawn triggers on a missing pane; disabled/excluded windows never respawn
- [ ] Measure supervision cost at 12 panes before assuming it is negligible

### Phase 3: Gate interplay

- [ ] Pane switches to Pending Gates when a gate becomes waiting (tick diff)
- [ ] Auto-show popup suppressed where a control pane exists, retained on excluded windows
- [ ] Unit tests: the switch fires once per new gate, not once per tick while it waits

### Phase 4: Surface hosting

- [ ] The pane's surface is selectable rather than hard-wired to the graph UI, so a second control surface needs no second pane
- [ ] Unit tests: an unknown surface name degrades to the graph UI rather than an empty pane

### Phase 5: Docs

- [ ] `config/tmux.conf` and `README.md` cover the control pane, its config, and the popup fallback
- [ ] `docs/tui-style.md` layout anatomy gains the control pane

### Phase 6: Integration test

- [ ] Create `scripts/test-control-pane.sh` (scratch session)
- [ ] Test: pane order is `0/1/2` on **every** window, and `AgentPane`-targeted delivery reaches the agent on each — the check that protects the delivery layer
- [ ] Test: killing a control pane respawns it
- [ ] Test: a gate appearing switches the pane to Pending Gates, verified by `capture-pane`
- [ ] Test: **negative control** — with the feature disabled, no third pane exists anywhere and popup auto-show still fires
- [ ] Test: **negative control** — an excluded window keeps two panes while a sibling has three
- [ ] Test: pane titles render as ` NVIM ` / ` GRAPH `; pane 1's raw title is **not** set by the launcher, while its border displays uppercased
- [ ] Test: a change to the CLI-managed pane-1 title still shows through the substitution — proving the transform is at display and does not pin the value
- [ ] Run the script and verify all checks pass

## Open questions

- **Is 12 panes the right default?** The rollout was to all windows, so that is the spec'd default — but it means 12 `muxcode graph ui` processes, 12 supervised panes, and three panes competing for height on a small terminal. `MUXCODE_CONTROL_PANE_EXCLUDE` exists for that, though a default that many users immediately narrow is a default worth revisiting. Measure process and redraw cost before treating it as settled.
- **Does every window want the *graph* surface?** A control pane on `watch` or `commit` showing graph runs may be less useful than one showing that window's own domain. This is the argument for Phase 4's selectable surface arriving early rather than late.
- **Does 14 rows suit a wide DAG?** The flat-list fallback triggers on height, so a deep graph in a 14-row pane shows the list rather than the grid. That is correct behaviour but may be the common case rather than the exception — worth watching whether the strip is mostly a list in practice.
- **Should the pane be focusable?** A read-only pane cannot approve a gate, which undercuts the point. If focusable, `prefix`-navigation into pane 2 must not disturb the agent pane's own key handling — and with 12 of them, the navigation story needs to be consistent across windows.

## Sources

- User request and live prototype, relayed by the edit agent, 2026-08-26, with the handoff at `/tmp/mux-graph-strip-spec.md` plus three addenda (border/title styling, per-pane titling, ALL-CAPS casing — the last superseding the initial mixed-case proposal)
- Pane-index contract verified in `bus/config.go` (`AgentPane` returns a hardcoded `"1"`)
- [`MUX-031`](../completed/MUX-031-graph-run-tui.md) — the graph TUI surfaces the pane hosts
- [`MUX-105`](../completed/MUX-105-force-respond-escalation.md) — gate auto-show, whose popup this partially replaces
- [`MUX-107`](../backlog/MUX-107-tui-component-kit.md) — the component kit the pane's surfaces should consume once extracted

## Provenance

Filed by the plan agent on 2026-08-26 from a user-requested feature prototyped live in this session. The handoff attributed the pane-index lesson to MUX-104; that attribution is corrected above — the rotate-window finding is unfiled and should be captured before it is lost.

## Status

In Progress
