# Persistent Graph Strip Pane

Move the graph TUI from a transient popup to a fixed full-width strip at the bottom of agent windows, with the existing editor and agent panes above it — so run state, launches, and waiting gates are *ambient* rather than something you must remember to open.

## Context

### The request

Prototyped live on 2026-08-26:

```bash
tmux split-window -vf -l 14 -t <session>:edit "muxcode graph ui"
```

Pane 2, with indices 0 (nvim) and 1 (agent) untouched **because the strip is created last**.

### Why a strip rather than a popup

A popup answers "what is happening?" only when you already suspect something is. The motivating failure for the gate auto-show ([`MUX-105`](../drafts/MUX-105-force-respond-escalation.md)) was a gate sitting unnoticed for 37 minutes — the run had paused correctly and the notification had been sent; nobody was looking. Auto-show fixed *that* gate by forcing a popup open. A strip removes the class: the surface is already on screen.

### The pane-index contract is the load-bearing constraint

`AgentPane(window)` returns a hardcoded **`"1"`**, and `PaneTarget()` composes `session:window.1`. Everything that reaches an agent — `Notify`, `ClearAgent`, `deliver`, wake-up injection, the mode cycler — resolves through it.

**Creation order is therefore the contract.** The strip must be created *after* panes 0 and 1, every time, on every window that gets one. A strip created earlier, or a layout operation that renumbers panes, silently repoints every agent message at the wrong pane.

> **Related unfiled lesson.** The handoff cites this as "the MUX-104 rotate-window lesson", but MUX-104 is the `send-keys` dash-payload bug — the rotate-window finding is **not filed in any spec**. It was established during the tmux-bindings work: `select-layout` preserves pane indices, but `rotate-window` does **not** (it flips pane 1 from the agent to nvim, breaking `PaneTarget`/`Notify`/`ClearAgent`/`deliver`). That is why `rotate` was never bound. It survives only in agent memory, which is a poor home for a constraint this load-bearing — **this spec should either carry it or a follow-up should file it**, because the next person to add a layout binding will not know.

### Interplay with gate auto-show

On a window **with** a strip, forcing a popup open is redundant and intrusive. Instead the strip should **switch itself to the Pending Gates surface** when a new gate appears — a tick observing a waiting gate absent from the previous tick. The popup path remains the fallback for stripless windows and detached sessions.

## Requirements

### Acceptance criteria

- [ ] `LaunchSession` creates the strip on configured windows, **always after panes 0 and 1**, so `AgentPane() == "1"` holds
- [ ] Default configuration: `edit` window only
- [ ] `MUXCODE_GRAPH_STRIP_WINDOWS` (comma list; empty disables entirely) and `MUXCODE_GRAPH_STRIP_HEIGHT` (default 14)
- [ ] A killed strip pane is respawned under the same supervision model as the left-pane pollers; a binary hot-reload restarts it
- [ ] The strip runs the **same** `muxcode graph ui` binary — no forked renderer. The existing height clamp and flat-list fallback already handle 14 rows (pinned by `TestRenderGraphFrame_DeepGraphFallsBack`)
- [ ] On a strip window, a newly-waiting gate switches the strip to the Pending Gates surface; the auto-show popup does **not** also fire
- [ ] On a stripless window, auto-show popup behaviour is unchanged
- [ ] Pane borders: `pane-border-status top` with a `pane-border-format` that shows a title only for titled panes, Dracula `colour141` border, set by the launcher when it creates the strip
- [ ] Pane titles are **ALL CAPS**: pane 0 `" NVIM "`, pane 2 `" GRAPH "`
- [ ] **Pane 1's title is never set by the launcher** — the agent CLI self-manages it, live-updating a state glyph (observed on the prototype). Writing a title there would overwrite a live signal
- [ ] The agent pane nonetheless *displays* in the same ALL-CAPS style, via a `pane-border-format` substitution (`s/code-editor/CODE-EDITOR/`) that transforms the CLI's raw title **at render time only**

  > This is the right shape for the problem. The launcher and the CLI both want to own that
  > title; a launcher that sets it wins briefly and then loses every state update, while
  > leaving it raw breaks the visual convention the other two panes establish. Transforming at
  > display keeps the CLI authoritative over content and the launcher authoritative over
  > presentation — neither has to yield, and the live glyph survives.
- [ ] `prefix + b` graph popups keep working for sessions with the strip disabled
- [ ] `config/tmux.conf` and `README.md` document the strip and its config

### Technical approach

- **Creation order, enforced not assumed.** The strip split happens at the end of window setup. A test asserts pane order `0=nvim, 1=agent, 2=strip` rather than trusting the call site to stay last.
- **Respawn.** Mirror the left-pane poller supervision already in the daemon; a dead strip is a respawn, not an alert.
- **Gate switching.** The strip's tick already re-reads the run store every 2s. Detecting "a waiting gate that was not waiting last tick" is a diff over that existing read — no new polling.
- **Suppressing the redundant popup.** `graph_exec.go`'s auto-show needs to know whether the target window has a strip. Prefer deriving it from the strip's own configuration rather than a second env var, so the two cannot disagree.
- **Borders.** Set `pane-border-status`/`pane-border-format` at strip creation, scoped to the window, so sessions without a strip are visually unchanged.

### Key files

| File | Change |
|------|--------|
| `bus/launch.go` | Create the strip last; set pane titles and border options |
| `bus/config.go` | `GraphStripWindows()`, `GraphStripHeight()` |
| `daemon/daemon.go` | Strip respawn supervision |
| `bus/graph_exec.go` | Suppress auto-show popup on strip windows |
| `tui/graph_ui.go` | Switch to gates surface on a newly-waiting gate |
| `config/tmux.conf`, `README.md` | Document strip + keybind fallback |
| `scripts/test-graph-strip.sh` | Integration test |

## Implementation

### Phase 1: Strip creation and configuration

- [ ] `MUXCODE_GRAPH_STRIP_WINDOWS` / `_HEIGHT` config accessors with defaults
- [ ] `LaunchSession` creates the strip after panes 0/1 on configured windows
- [ ] Pane titles (` NVIM `, ` GRAPH `), pane 1 left untitled
- [ ] Border status/format with Dracula `colour141`, scoped to strip windows
- [ ] Unit tests: pane-order assertion; empty config creates no strip and touches no border options

### Phase 2: Supervision

- [ ] Respawn a dead strip pane under the poller supervision model
- [ ] Binary hot-reload restarts the strip
- [ ] Unit tests: respawn triggers on a missing pane; disabled config never respawns

### Phase 3: Gate interplay

- [ ] Strip switches to Pending Gates when a gate becomes waiting (tick diff)
- [ ] Auto-show popup suppressed on strip windows, retained elsewhere
- [ ] Unit tests: the switch fires once per new gate, not once per tick while it waits

### Phase 4: Docs

- [ ] `config/tmux.conf` and `README.md` cover the strip, its config, and the popup fallback
- [ ] `docs/tui-style.md` gains the strip to its layout anatomy

### Phase 5: Integration test

- [ ] Create `scripts/test-graph-strip.sh` (scratch session)
- [ ] Test: pane order is `0/1/2` after launch, and `AgentPane`-targeted delivery still reaches the agent
- [ ] Test: killing the strip pane respawns it
- [ ] Test: a gate appearing switches the strip to Pending Gates, verified by `capture-pane`
- [ ] Test: **negative control** — with `MUXCODE_GRAPH_STRIP_WINDOWS` empty, no third pane exists and popup auto-show still fires
- [ ] Test: pane titles render as ` NVIM ` / ` GRAPH `; pane 1's raw title is **not** set by the launcher, while its border displays uppercased
- [ ] Test: a change to the CLI-managed pane-1 title still shows through the substitution — proving the transform is at display and does not pin the value
- [ ] Run the script and verify all checks pass

## Open questions

- **Which windows beyond `edit`?** Default is `edit` only. A strip on `plan` would surface gates to the docs agent's operator, but three panes on every window may crowd smaller terminals.
- **Does 14 rows suit a wide DAG?** The flat-list fallback triggers on height, so a deep graph in a 14-row strip shows the list rather than the grid. That is correct behaviour but may be the common case rather than the exception — worth watching whether the strip is mostly a list in practice.
- **Should the strip be focusable?** A read-only strip cannot approve a gate, which undercuts the point. If focusable, `prefix`-navigation into pane 2 must not disturb the agent pane's own key handling.

## Sources

- User request and live prototype, relayed by the edit agent, 2026-08-26, with the handoff at `/tmp/mux-graph-strip-spec.md` plus three addenda (border/title styling, per-pane titling, ALL-CAPS casing — the last superseding the initial mixed-case proposal)
- Pane-index contract verified in `bus/config.go` (`AgentPane` returns a hardcoded `"1"`)
- [`MUX-031`](../completed/MUX-031-graph-run-tui.md) — the graph TUI surfaces the strip hosts
- [`MUX-105`](../drafts/MUX-105-force-respond-escalation.md) — gate auto-show, whose popup this partially replaces
- [`MUX-107`](./MUX-107-tui-component-kit.md) — the component kit the strip should consume once extracted

## Provenance

Filed by the plan agent on 2026-08-26 from a user-requested feature prototyped live in this session. The handoff attributed the pane-index lesson to MUX-104; that attribution is corrected above — the rotate-window finding is unfiled and should be captured before it is lost.

## Status

Backlog
