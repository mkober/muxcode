# Graph Run TUI

An interactive Dracula-themed TUI that visualizes graph-orchestrator runs ([`MUX-014`](./MUX-014-graph-agent-orchestrator.md)) as a live DAG: run browser → layered node grid with state colors and active edges → node detail, with `wait_human` gate approval, cancel, and retry-from actions performed directly from the view.

## Context

### Why a dedicated TUI

The DAG *is* the feature: MUX-014's value over chains and LLM routing is shape — fan-out, joins, branches, capped loops. `graph status` text and lifecycle JSONL flatten that shape back out. A live node grid answers at a glance what the flat forms cannot: where is the run, what is blocked on what, which gate is waiting on me. MUX-014 Phase 5 sketches only a minimal run view; this spec is the dedicated interactive surface, and MUX-014's Phase 5 item shrinks to wiring its status renderer into this TUI.

### What exists to build on

| Asset | Reuse |
|-------|-------|
| `tui/remote.go` (`RemoteUI`) | The exact navigation pattern needed: list view → detail view → content view, key-driven, alternate screen |
| `tui/model.go` dashboard | Render tick loop reading state files from disk — no daemon coupling |
| `tui/provider_select.go` | Confirm-flow pattern for destructive actions (cancel, retry) |
| `bus/popup.go` + `config/tmux.conf` | Modal window manager: named popup configs behind tmux keybinds ([`MUX-069`](../completed/MUX-069-modal-window-manager.md)) |
| MUX-014 run store (`graphs/<run-id>/`) | The single data source: per-node status, timestamps, inputs/outputs, outcomes |

### Dependency on MUX-014

Requires MUX-014 Phase 1 (graph model) and Phase 2 (durable run store format) — the TUI is a pure reader of the run store plus a caller of `graph approve|cancel|retry`. It does **not** require the Phase 3 executor to be built against: fixture run directories (hand-written `graphs/<run-id>/` state) are enough to develop and test every view. This spec should not start before MUX-014 Phase 2 lands, but can proceed in parallel with Phases 3–5.

### Authority note

Approving a `wait_human` gate from this TUI **is** the explicit user approval MUX-014's gate rule demands — a human at the keyboard pressing a confirm key is the consent the gate exists to collect. This is categorically different from an agent sending `graph approve` over the bus; the TUI action path must be indistinguishable from the user typing `muxcode graph approve <run-id> <node>` themselves, and no bus-message path may reach it.

## Requirements

### Acceptance criteria

- [ ] `muxcode graph ui [run-id]` opens the TUI — run list when no id given, straight into that run's DAG view when given
- [ ] Run list view: all runs from the run store (live and completed), showing run id, template, state, elapsed, and node progress (`4/9 done`); completed runs open as post-mortem views from persisted state
- [ ] DAG view: topologically layered node grid — per-node state glyph + name colored by state (`pending`/`ready`/`running`/`done`/`failed`/`skipped`/`waiting`) in Dracula palette, box-drawing edges, active edges highlighted, capped loop edges annotated (`↺ ×N`)
- [ ] Live refresh: the view re-reads the run store on a tick and reflects a node transition within 2s — reader only, no daemon coupling
- [ ] Node detail view: status, timestamps, outcome, input/output preview, correlated task/message ids, worktree path for worker nodes
- [ ] `wait_human` gate nodes are visually prominent (distinct glyph + color); pressing `a` on a waiting gate opens a confirm prompt and releases the gate via the same code path as `muxcode graph approve` — no bus-message path can trigger this
- [ ] `c` (cancel run) and `r` (retry from selected node) work from the DAG view, each behind a confirm prompt, delegating to the MUX-014 cancel/retry paths
- [ ] Graphs wider or deeper than the pane degrade gracefully: the grid scrolls; past a threshold the view falls back to a flat node list ordered by state (failed/waiting first)
- [ ] Rendering is pure: layout and frame generation are functions of a run-store snapshot, unit-testable without a terminal
- [ ] `muxcode graph ui --render-once [run-id]` prints a single frame to stdout and exits — the scriptable seam for integration tests
- [ ] Modal integration: a `bus/popup.go` config entry and tmux keybind open the TUI as a display-popup, consistent with the modal window manager conventions
- [ ] Stdlib only — no external TUI dependencies, matching the existing `tui/` package

### Technical approach

- **`tui/graph.go`** — `GraphUI` modeled on `RemoteUI`: three views (runs / dag / node), key-driven navigation, alternate screen buffer, tick-driven re-read of the run store.
- **Layout engine** — Kahn topological layering: layer index = column, nodes within a layer stacked; edges drawn with box-drawing connectors between adjacent layers, non-adjacent edges routed with elbow glyphs. Capped loop edges render as a back-edge annotation rather than a drawn cycle. Layout is a pure function `LayoutGraph(run) → grid` so it is testable as string comparison.
- **Frame rendering** — pure `RenderGraphFrame(grid, width, height, selection) → string` reusing the Dracula constants already in `tui/`; `--render-once` calls exactly this and prints.
- **Actions** — approve/cancel/retry call the same `bus` functions the `cmd/graph.go` CLI handlers use (never `exec` of the CLI, never a bus message), keeping the TUI path identical to the user typing the command.
- **Fallback list** — reuses the run list renderer with per-node rows; threshold on computed grid width vs pane width.

### Key files

| File | Change |
|------|--------|
| `tui/graph.go` | New — `GraphUI`, layout engine, frame renderer, action handlers |
| `tui/graph_test.go` | New — layout/render pure-function tests over fixture runs |
| `cmd/graph.go` | Extended (MUX-014 file) — `graph ui` subcommand + `--render-once` |
| `bus/popup.go` | New popup config entry for the graph TUI |
| `config/tmux.conf` | Keybind for the graph modal |
| `docs/agent-bus.md`, `docs/architecture.md` | CLI reference + observability section cross-links |

## Implementation

### Phase 1: Layout engine and frame renderer

- [ ] Kahn layering over `Graph`/run-store snapshot; elbow routing for non-adjacent edges; loop back-edge annotation
- [ ] Pure `RenderGraphFrame()` with Dracula state colors, selection highlight, active-edge highlight
- [ ] Fallback flat-list renderer + width threshold
- [ ] Unit tests: string-comparison layout tests over fixture runs (linear, fan-out/join, capped loop, wide graph fallback)

### Phase 2: Interactive views

- [ ] `GraphUI` on the `RemoteUI` pattern: run list → DAG → node detail, tick-driven store re-read
- [ ] `graph ui` subcommand + `--render-once`
- [ ] Post-mortem rendering of completed runs
- [ ] Unit tests: view navigation state machine, tick refresh picks up a store change

### Phase 3: Gate actions

- [ ] Approve (`a`), cancel (`c`), retry-from (`r`) with confirm prompts, calling the MUX-014 bus paths directly
- [ ] `wait_human` prominence: distinct glyph/color, run list badge when any gate is waiting
- [ ] Unit tests: action dispatch gated on confirm; approve refused on non-waiting nodes

### Phase 4: Modal integration

- [ ] `bus/popup.go` entry + tmux keybind; auto-size per the modal window manager conventions
- [ ] Docs: `docs/agent-bus.md` CLI section, `docs/architecture.md` observability cross-link, MUX-014 Phase 5 pointer

### Phase 5: Integration test

- [ ] Create `scripts/test-graph-tui.sh` (hermetic — fixture run store under a scratch `BUS_SESSION`, no live session needed)
- [ ] Test: `--render-once` on a fixture fan-out/join run → frame contains every node name, correct state glyphs, and the join barrier
- [ ] Test: fixture with a `waiting` gate node → frame shows the gate prominently; run list frame carries the gate badge
- [ ] Test: wide fixture graph → output is the flat fallback list, failed/waiting nodes first
- [ ] Test: completed fixture run → post-mortem frame renders with final states
- [ ] Run the script and verify all checks pass

## Open questions

- **Edge routing limits** — box-drawing routing degrades on dense non-planar graphs; is the fallback threshold on width alone, or also on edge-crossing count?
- **Watch mode for `--render-once`** — a `--watch` flag (frame per transition, for piping to logs) is cheap once rendering is pure; worth it in v1?
- **Gate notification interplay** — MUX-014 already notifies edit on `wait_human`; should the TUI's gate badge replace or complement a tmux status-bar flash?

## Sources

- [`MUX-014-graph-agent-orchestrator.md`](./MUX-014-graph-agent-orchestrator.md) — run store, node states, gate semantics
- [`../completed/MUX-069-modal-window-manager.md`](../completed/MUX-069-modal-window-manager.md) — modal conventions
- `tools/muxcode/tui/{remote,model,provider_select}.go` — navigation, tick, confirm patterns

## Provenance

Filed by the plan agent on 2026-08-24 from a user request to visualize the graph-agent feature in a TUI.

## Status

Backlog
