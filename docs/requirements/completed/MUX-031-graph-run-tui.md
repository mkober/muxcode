# Graph Agent Management TUIs

Reclaim the `prefix + b` quick menu for graph-orchestrator management ([`MUX-014`](./MUX-014-graph-agent-orchestrator.md)) — retire six low-value popup entries and replace them with three interactive surfaces: a **run browser** (run list → layered live DAG → node detail), a **template launcher**, and a **pending-gate queue** where `wait_human` approvals are collected from a human at the keyboard.

## Context

### Why a dedicated TUI

The DAG *is* the feature: MUX-014's value over chains and LLM routing is shape — fan-out, joins, branches, capped loops. `graph status` text and lifecycle JSONL flatten that shape back out. A live node grid answers at a glance what the flat forms cannot: where is the run, what is blocked on what, which gate is waiting on me. MUX-014 Phase 5 sketches only a minimal run view; this spec is the dedicated interactive surface, and MUX-014's Phase 5 item shrinks to wiring its status renderer into this TUI.

### Why the menu entries go

MUX-014 shipped an entire control plane with **no interactive surface at all** — every graph operation is a CLI invocation against a run id you must first find. Meanwhile the quick menu spends two of its five groups on popups that wrap a single CLI command in a "press any key" pager. The menu is prime real estate keyed off one keystroke; graph management earns it, and these six do not.

| Removed entry | Popup | Still reachable via | Why it goes |
|---------------|-------|---------------------|-------------|
| Agent Status `(a)` | `agent-status` | `muxcode status`, dashboard TUI | The dashboard TUI already renders this live and better |
| Agent History `(h)` | `agent-history` | `muxcode history <role>` | Needs a role typed at a prompt before it shows anything — no faster than the CLI |
| Memory Context `(m)` | `memory-context` | `muxcode memory context` | A pager over text agents read, not humans |
| Spawn Agent `(p)` | `spawn-agent` | `muxcode spawn start <role> "<task>"` | Two-field prompt for a one-line command; graph `spawn` nodes now cover the orchestrated case |
| List Processes `(l)` | `proc-list` | `muxcode proc list`, `muxcode spawn list` | Static snapshot behind a keystroke |
| Cron Jobs `(c)` | `cron-list` | `muxcode cron list` | Static snapshot behind a keystroke |

Every removed capability stays reachable. Nothing here is a feature deletion — it is a menu-slot reallocation.

### Scoping boundary: popups go, CLI stays

**Only the tmux menu entries and their popup registry wrappers are removed. The underlying `muxcode` subcommands are untouched.**

This boundary is not cosmetic. `muxcode memory context`, `muxcode spawn`, `muxcode proc`, and `muxcode cron` are load-bearing for *agents*, not humans — agent role instructions call them directly, `bus/graph_exec.go` dispatches spawn nodes through `StartSpawn()`, and the daemon polls proc/spawn/cron state every cycle. Deleting those commands to "remove the function" would break the orchestrator this spec exists to serve. The popups are thin `command + press-any-key` wrappers over them; that wrapper layer is what has no value.

### Interaction with three existing modal specs

Three backlog specs propose replacing the popups this spec retires with *better* modals, and two of them carry an acceptance criterion that this removal invalidates:

| Spec | Proposes | Criterion affected |
|------|----------|--------------------|
| [`MUX-023`](../backlog/MUX-023-modal-cron-manager.md) | Interactive cron manager modal | "Replaces existing static *Cron Jobs* menu entry" — after MUX-031 there is no entry to replace |
| [`MUX-024`](../backlog/MUX-024-modal-history-viewer.md) | History browser modal with filtering/search | "Replaces existing static *Agent History* menu entry" — same |
| [`MUX-026`](../backlog/MUX-026-modal-memory-browser.md) | Memory browser modal with BM25 search | No menu criterion; unaffected in wording, but the same slot question applies |

This is not a contradiction — all three argue the *static popup* is the weak part, which is exactly why it is being removed here. The order simply matters: MUX-031 lands first and frees the slot; if any of the three is later built, it re-enters the menu on its own merits as an interactive modal rather than inheriting a slot. Those two criteria should be reworded from "replaces" to "adds" whenever their specs are next touched. All three are Low priority and unstarted, so nothing is in flight.

### What exists to build on

| Asset | Reuse |
|-------|-------|
| `tui/remote.go` (`RemoteUI`) | The exact navigation pattern needed: list view → detail view → content view, key-driven, alternate screen |
| `tui/model.go` dashboard | Render tick loop reading state files from disk — no daemon coupling |
| `tui/provider_select.go` | Confirm-flow pattern for destructive actions (cancel, retry) |
| `bus/popup.go` + `config/tmux.conf` | Modal window manager: named popup configs behind tmux keybinds ([`MUX-069`](./MUX-069-modal-window-manager.md)) |
| MUX-014 run store (`graphs/<run-id>/`) | The single data source: per-node status, timestamps, inputs/outputs, outcomes |
| `bus/graph.go` `ListGraphTemplates()` | Template launcher inventory, already resolving project > user > builtin |

### Dependency on MUX-014

Requires MUX-014 Phase 1 (graph model) and Phase 2 (durable run store format) — the TUI is a pure reader of the run store plus a caller of `graph approve|cancel|retry|run`. It does **not** require the Phase 3 executor to be built against: fixture run directories (hand-written `graphs/<run-id>/` state) are enough to develop and test every view. MUX-014 is now complete, so this spec is unblocked.

### Authority note

Approving a `wait_human` gate from this TUI **is** the explicit user approval MUX-014's gate rule demands — a human at the keyboard pressing a confirm key is the consent the gate exists to collect. This is categorically different from an agent sending `graph approve` over the bus; the TUI action path must be indistinguishable from the user typing `muxcode graph approve <run-id> <node>` themselves, and no bus-message path may reach it.

This applies with full force to the new **pending-gate queue**, which makes approval faster and therefore easier to do carelessly. The queue must show *what is being approved* — the gate's node id, the run it belongs to, and the downstream nodes the approval releases — before the confirm key is accepted. A gate whose downstream includes a commit or Atlassian node must say so in the confirm prompt, since that is precisely the authority boundary MUX-014's validator defers to a human.

## Requirements

### Acceptance criteria

#### Menu reclamation

- [x] The six entries above are removed from the `prefix + b` menu in `config/tmux.conf`, along with their `bus/popup.go` registry configs
- [x] Orphaned measurers (`MeasureAgentStatus`, `MeasureProcList`, `MeasureCronList`) are removed; measurers still in use by surviving popups are untouched
- [x] Every removed capability verified still reachable by its CLI command — `muxcode status`, `history`, `memory context`, `spawn start`, `proc list`, `spawn list`, `cron list` all unchanged
- [x] Menu gains a graph group on freed keys: `Graph Runs (g)`, `Launch Graph (G)`, `Pending Gates (a)`
- [x] `config/tmux.conf` parses clean — verified with `tmux source-file` and a `list-keys` count, **not** by eye (an unquoted `}` once aborted this file at line 37 and silently killed 25 later directives)
- [x] `README.md` keybinding table reflects the new menu group

> **Correction (2026-08-25):** this spec originally named **four** orphaned measurers,
> including `MeasureMemoryContext`. That was wrong. `MeasureMemoryContext` is still used by
> the surviving `save-memory` popup (`bus/popup.go`), so removing it would have broken a
> live surface. Three measurers were orphaned and three were removed; retaining the fourth
> is correct and matches the second half of this criterion.

#### Run browser and DAG view

- [x] `muxcode graph ui [run-id]` opens the TUI — run list when no id given, straight into that run's DAG view when given
- [x] Run list view: all runs from the run store (live and completed), showing run id, template, state, elapsed, and node progress (`4/9 done`); completed runs open as post-mortem views from persisted state
- [x] DAG view: topologically layered node grid — per-node state glyph + name colored by state (`pending`/`ready`/`running`/`done`/`failed`/`skipped`/`waiting`) in Dracula palette, box-drawing edges, active edges highlighted, capped loop edges annotated (`↺ ×N`)
- [x] Live refresh: the view re-reads the run store on a tick and reflects a node transition within 2s — reader only, no daemon coupling
- [ ] Node detail view: status, timestamps, outcome, input/output preview, correlated task/message ids, worktree path for worker nodes — renders status/`StartedAt`/`Outcome`/`Output`/`TaskID`/`Worktree`; **`EndedAt`, message id, and the input preview are absent**
- [x] Graphs wider or deeper than the pane degrade gracefully: past a threshold the view falls back to a flat node list ordered by state (failed/waiting first) — both axes now covered (see Phase 2 notes; the grid falls back rather than scrolling, which satisfies the intent)

#### Template launcher

- [x] `Launch Graph` opens a template picker listing `ListGraphTemplates()` results with their source tier (project / user / builtin) and description
- [x] Selecting a template runs `Validate()` before launch and refuses with the validation error rendered in place — a template that fails the uncapped-cycle or ungated-authority rules never starts a run
- [x] Launch transitions directly into the new run's DAG view
- [x] Templates requiring arguments prompt for them in the TUI rather than failing after launch

#### Pending-gate queue

- [x] `Pending Gates` lists every `wait_human` node in `waiting` state across **all** in-flight runs, newest first, with run id, node id, and elapsed wait
- [x] Selecting a gate shows what the approval releases: the downstream node ids and their types
- [x] A gate whose downstream contains a commit or Atlassian node is flagged in both the list and the confirm prompt
- [x] Approving releases the gate via the same code path as `muxcode graph approve` — no bus-message path can trigger it
- [x] Empty state renders as an explicit "no gates waiting", never a blank frame

#### Cross-cutting

- [x] `wait_human` gate nodes are visually prominent in the DAG view (distinct glyph + color); pressing `a` on a waiting gate opens the same confirm prompt as the queue
- [x] `c` (cancel run) and `r` (retry from selected node) work from the DAG view, each behind a confirm prompt, delegating to the MUX-014 cancel/retry paths
- [x] Retry-from a node at or upstream of a gate re-arms that gate — the confirm prompt says so, since MUX-014 purges the approval marker and a fresh approval will be demanded
- [x] Rendering is pure: layout and frame generation are functions of a run-store snapshot, unit-testable without a terminal
- [x] `muxcode graph ui --render-once [run-id]` prints a single frame to stdout and exits — the scriptable seam for integration tests
- [x] Modal integration: `bus/popup.go` config entries and tmux keybinds open the three surfaces as display-popups, consistent with the modal window manager conventions
- [x] Stdlib only — no external TUI dependencies, matching the existing `tui/` package

### Technical approach

- **`tui/graph.go`** — `GraphUI` modeled on `RemoteUI`: views for runs / dag / node / templates / gates, key-driven navigation, alternate screen buffer, tick-driven re-read of the run store.
- **Layout engine** — Kahn topological layering: layer index = column, nodes within a layer stacked; edges drawn with box-drawing connectors between adjacent layers, non-adjacent edges routed with elbow glyphs. Capped loop edges render as a back-edge annotation rather than a drawn cycle. Layout is a pure function `LayoutGraph(run) → grid` so it is testable as string comparison.
- **Frame rendering** — pure `RenderGraphFrame(grid, width, height, selection) → string` reusing the Dracula constants already in `tui/`; `--render-once` calls exactly this and prints.
- **Gate queue** — a scan over `ScanInFlightGraphRuns()` filtering nodes by type `wait_human` + status `waiting`; downstream impact computed from the frozen `graph.json` edge set, so the queue reports what the *run* will do, not what the current template file says.
- **Actions** — approve/cancel/retry/run call the same `bus` functions the `cmd/graph.go` CLI handlers use (never `exec` of the CLI, never a bus message), keeping the TUI path identical to the user typing the command.
- **Fallback list** — reuses the run list renderer with per-node rows; threshold on computed grid width vs pane width.
- **Menu removal** — delete the six `DefaultPopupConfigs()` entries and their `config/tmux.conf` lines; the `agent-history` string in `popup_test.go` is an inline `ModalConfig` literal, not a registry lookup, so it survives removal — rename its fixture to avoid implying a live config.

### Key files

| File | Change |
|------|--------|
| `tui/graph.go` | New — `GraphUI`, layout engine, frame renderer, action handlers, gate queue |
| `tui/graph_test.go` | New — layout/render pure-function tests over fixture runs |
| `cmd/graph.go` | Extended (MUX-014 file) — `graph ui` subcommand + `--render-once` |
| `bus/popup.go` | Remove 6 configs; add graph TUI popup entries |
| `bus/measure.go` | Remove 3 orphaned measurers (`MeasureMemoryContext` stays — `save-memory` uses it) |
| `bus/popup_test.go` | Rename `agent-history` fixture name |
| `config/tmux.conf` | Remove 6 menu lines; add graph menu group |
| `README.md` | Keybinding table update |
| `docs/agent-bus.md`, `docs/architecture.md` | CLI reference + observability section cross-links |

## Implementation

### Phase 1: Menu reclamation

- [x] Remove the six popup configs from `DefaultPopupConfigs()` and the six menu lines from `config/tmux.conf`
- [x] Remove the three orphaned measurers from `bus/measure.go` (see the correction above — `MeasureMemoryContext` is not orphaned)
- [x] Rename the `agent-history` fixture in `popup_test.go` to a neutral name (`fixture-history`)
- [x] Verify each removed capability still works from the CLI (`status`, `history`, `memory context`, `spawn start`, `proc list`, `spawn list`, `cron list`)
- [x] Verify `config/tmux.conf` sources clean and the surviving bindings still resolve via `tmux list-keys` count comparison before/after
- [x] Update the `README.md` keybinding table

### Phase 2: Layout engine and frame renderer

- [x] Kahn layering over `Graph`/run-store snapshot; elbow routing for non-adjacent edges; loop back-edge annotation
- [x] Pure `RenderGraphFrame()` with Dracula state colors, selection highlight, active-edge highlight
- [x] Fallback flat-list renderer + width threshold
- [x] Unit tests: string-comparison layout tests over fixture runs (linear, fan-out/join, capped loop, wide graph fallback)

### Phase 3: Interactive views

- [x] `GraphUI` on the `RemoteUI` pattern: run list → DAG → node detail, tick-driven store re-read
- [x] `graph ui` subcommand + `--render-once` (source wiring; not yet executable — see Phase 3 notes)
- [x] Post-mortem rendering of completed runs
- [x] Unit tests: view navigation state machine, tick refresh picks up a store change

### Phase 4: Template launcher

- [x] Template picker view over `ListGraphTemplates()` with source tier and description
- [x] Pre-launch `Validate()` with in-place error rendering
- [x] Argument prompting for templates that need it; launch transitions into the new run's DAG view
- [x] Unit tests: invalid template refused before any run directory is created

### Phase 5: Gate queue and actions

- [x] Cross-run `wait_human` queue with elapsed wait, downstream impact, and commit/Atlassian flagging
- [x] Approve (`a`), cancel (`c`), retry-from (`r`) with confirm prompts, calling the MUX-014 bus paths directly
- [x] Retry-from warns when it re-arms a gate downstream
- [x] Unit tests: action dispatch gated on confirm; approve refused on non-waiting nodes; downstream-impact computed from the frozen run definition, not the template file

### Phase 6: Modal integration

- [x] `bus/popup.go` entries + tmux keybinds for the three surfaces; auto-size per the modal window manager conventions
- [x] Docs: `docs/agent-bus.md` CLI section, `docs/architecture.md` observability cross-link, MUX-014 Phase 5 pointer

### Phase 7: Integration test

- [x] Create `scripts/test-graph-tui.sh` (hermetic — fixture run store under a scratch `BUS_SESSION`, no live session needed)
- [x] Test: `--render-once` on a fixture fan-out/join run → frame contains every node name, correct state glyphs, and the join barrier
- [x] Test: fixture with a `waiting` gate node → frame shows the gate prominently; run list frame carries the gate badge
- [x] Test: gate queue frame over two fixture runs each holding a waiting gate → both listed, downstream impact shown, commit-downstream gate flagged
- [x] Test: wide fixture graph → output is the flat fallback list, failed/waiting nodes first
- [x] Test: completed fixture run → post-mortem frame renders with final states
- [ ] Test: the six removed popup names return an unknown-popup error from `muxcode popup`, while their underlying CLI commands still succeed — **popup half covered by the script; the CLI half is not asserted anywhere** (verified by hand instead, see Phase 1 notes)
- [x] Run the script and verify all checks pass

## Verification notes

### Phase 1 (2026-08-25) — 4/6 steps, evidence recorded

| Claim | Evidence |
|-------|----------|
| 6 popup configs removed | `git diff bus/popup.go` — all six literals deleted; registry now holds `session-picker`, `switch-session`, `remote-sessions`, `save-memory`, `edit-config` |
| 6 menu lines removed | `config/tmux.conf` menu block re-read in full; repo and installed `~/.config/muxcode/tmux.conf` are byte-identical |
| 3 measurers removed | `git diff bus/measure.go` — `MeasureAgentStatus`/`MeasureProcList`/`MeasureCronList` deleted, `measure_test.go` map entries dropped in step |
| Fixture renamed | `popup_test.go` `agent-history` → `fixture-history`; it is an inline `ModalConfig` literal, never a registry lookup, so the rename is cosmetic-only as predicted |
| CLI capabilities intact | All six **executed**, not grepped: `status`, `memory context`, `proc list`, `spawn list`, `cron list` exit 0 with real output; `history plan` returns records |
| Removed popups rejected | `muxcode popup agent-status\|memory-context\|cron-list` → `Error: unknown popup: … (known: …)` — fails closed with a discoverable list |

**Two steps deliberately left open:**

- **`tmux.conf` parses clean** — *not* verified. The running session still has the **old** menu loaded (`tmux list-keys` still shows `Agent Status`, `Cron Jobs`, `Memory Context`), so a `list-keys` check right now measures the pre-change config and proves nothing about the new file. This needs a `source-file` against the installed path before it can be checked off. Deleting lines carries far less parse risk than the unquoted-`}` incident, but "low risk" is not evidence.
- **`README.md` keybinding table** — untouched, and correctly so for now: its only row is the generic `Prefix + b → Open MuxCode quick menu`, which is still accurate. It gains content once the graph group lands.

### Phase 2 (2026-08-25) — 4/4 steps, tests green

`tui/graph.go` (15KB) and `tui/graph_test.go` (10KB), both still untracked. The daemon's
changed-file list named only the test — as with `popup.go` in Phase 1, it is partial, so both
ends were checked directly rather than trusted.

| Claim | Evidence |
|-------|----------|
| Kahn layering | `LayoutGraph()` builds an `indeg` map and drains a zero-indegree queue — genuine Kahn, not a naive depth walk |
| Elbow routing | `drawSkipEdge()` for non-adjacent layers, `drawAdjacentEdge()` for neighbours |
| Loop annotation | `loopAnnotation()` emits the `↺ ×N` badge; `TestRenderGraphFrame_LoopBadge` covers it |
| Dracula colors | `stateColor()` returns the shared `styles.go` constants (`Green`/`Red`/`Cyan`/`Yellow`/`Purple`/`Comment`) — palette reused, not redefined |
| Gate prominence | `nodeGlyph()` special-cases `NodeWaitHuman` → `⚑`, `Yellow + Bold` when waiting |
| Selection + active edges | `writeCursorAndLabel(selected)`, `edgeActive()` |
| Width fallback | `gridW > width` → `renderGraphFallback()`; `fallbackStateOrder` ranks failed/waiting ahead of done, asserted by `TestRenderGraphFrame_NarrowPaneFallsBack` |
| Tests green | Delegated to the test agent: **63 top-level tests, 0 FAIL**, no compile errors; all 16 new tests pass (`TestLayoutGraph` ×4, `TestRenderGraphFrame` ×12) |

All seven node states get a distinct glyph, including `skipped` (`○`) versus `pending` (`·`).
The two share the `Comment` color — reasonable, since both mean "not active", and the glyph
disambiguates — but worth knowing the criterion's "colored by state" is satisfied by glyph,
not hue, for that one pair.

#### Gap: the `height` parameter is accepted and never used

`RenderGraphFrame(snap, width, height, selection, now)` takes `height` — and `height` appears
**exactly once in the file, in the signature**. Nothing clips, scrolls, or falls back on
vertical overflow. The consequence is asymmetric degradation: a **wide** graph falls back to the
flat list correctly, while a **deep** graph (many nodes in one layer) renders past the bottom of
the pane with no fallback and no scroll.

No test caught this because every one of the 16 passed `40` as height — a value large enough
that no fixture overflows. **A green suite is not a covered criterion**; this is the same shape
as the MUX-014 vacuous pass, where assertions succeeded because the code path was never reached.

**Closed the same session.** `RenderGraphFrame` now reads
`gridW > width || gridH+skipLanes+headerLines > height` (`graph.go:353`), so vertical overflow
falls back on the same path as horizontal. `TestRenderGraphFrame_DeepGraphFallsBack` covers it
with an 8-wide fan-out at width 200 — wide enough that width cannot be the trigger — asserting
fallback at `height 12` **and the grid at `height 40`**. That second assertion is what makes the
test non-vacuous: without it, a pass could come from any incidental fallback rather than from the
height rule. The criterion is checked off on that evidence, noting the implementation *falls back*
rather than *scrolls* — a defensible reading of "degrade gracefully", but not literally the
scrolling the criterion's wording implies.

### Phase 3 (2026-08-25) — 4/4 steps

New `tui/graph_ui.go` plus `cmd/graph.go` changes. The daemon again named a partial file set
(`cmd/graph.go` only on the first pass), so both ends were checked directly.

| Claim | Evidence |
|-------|----------|
| Three views | `GraphUI.view` over `viewGraphDAG` et al., with `rows`/`runIdx` (list), `snap`/`order`/`nodeIdx` (DAG + node); `enter()`, `goBack()`, `moveSelection()` form the state machine |
| Tick-driven re-read | `Run()` selects on `time.After(graphTickInterval)`; `graphTickInterval = 2 * time.Second` |
| Subcommand wired | `cmd/graph.go` `case "ui"` → `graphUI()` → `tui.GraphRenderOnce()` or `tui.NewGraphUI().Run()`; usage line added |
| Run list fields | `RunListRow{ID, Template, State, Done, Total, Elapsed, GateWaiting}` — exactly the criterion's fields, plus the gate badge Phase 5 will need |
| Post-mortem | `LoadRunListRows` includes completed runs; `TestRenderGraphFrame_PostMortemElapsedFrozen` and `TestLoadGraphSnapshot_PostMortem` cover frozen elapsed |
| Unit tests | `graph_ui_test.go` — 9 tests including `TestGraphUI_NavigationStateMachine` and `TestGraphUI_RefreshPicksUpStoreChange`, the two the step names |

#### Blocker: the installed binary is stale, so no runtime criterion is verified

`muxcode graph ui --render-once` returns **`Unknown graph subcommand: ui`**, and `ui` is missing
from the printed usage — while the source has both. The installed binary timestamps at **14:19**
against source at **14:22**: it predates the subcommand. This is a stale-binary artifact, not
half-wiring, confirmed by reading the dispatch through to `graphUI()`.

Consequence: every criterion that requires *executing* the TUI stays unchecked — `graph ui` opens,
live refresh within 2s, `--render-once` prints a frame. Source-level steps are checked; runtime
criteria are not. A `./build.sh` (which also runs `upgrade-daemons`) unblocks them.

This is the MUX-103 lesson repeating: a suite scored 19/22 against a stale binary and 22/22 after
the rebuild. **Test the installed path, because that is what a user runs.**

#### A correction I had to make mid-pass

I recorded "`graph_ui_test.go` does not exist" and it existed four minutes later — it landed
between my check and my write-up. Same shape as MUX-014, where `graph_run_test.go` appeared
between an "absent" finding and the next message. An echo-looking message is never proof the tree
is unchanged; the finding is corrected above rather than left standing.

### Phase 4 (2026-08-25) — 4/4 steps

| Claim | Evidence |
|-------|----------|
| Picker with tier + description | `RenderTemplateListFrame(infos []bus.GraphTemplateInfo, width, sel, errMsg)` renders `t.Source` (project / user / builtin) and `t.Description`; `TestRenderTemplateListFrame_TiersAndEmpty` covers both populated and empty |
| Validate before launch | `graph_ui.go:269` `if v := g.Validate(); !v.OK()` sits **above** `bus.CreateGraphRun` at `:274` — ordering verified by line number, not by comment |
| In-place error | the `errMsg` parameter on the picker frame |
| Intent prompting | `TemplateNeedsIntent()` + a dedicated `viewGraphIntent`; `TestTemplateNeedsIntent`, `TestGraphUI_IntentPromptFlow` |
| Launch → DAG | `TestGraphUI_LaunchTransitionsToDAG` |
| Refusal is pre-side-effect | `TestGraphUI_InvalidTemplateRefusedBeforeRunDir` — asserts no run directory exists, which is the property that matters, not merely that an error was returned |
| CLI entry | `cmd/graph.go` `ui --templates` |

Views are now `viewGraphRuns` / `viewGraphDAG` / `viewGraphNode` / `viewGraphTemplates` /
`viewGraphIntent`. Suite at **74 top-level tests, 0 FAIL**.

#### A false negative in my own verification

I first reported "Phases 4/5 not started" from a grep for
`viewTemplates|viewGates|TemplateRow|LoadTemplateRows|…` — none of which match the real symbol
`RenderTemplateListFrame`. Phase 4 was fully implemented at the time I said it was absent; only the
test agent naming `TestGraphUI_InvalidTemplateRefusedBeforeRunDir` exposed the mistake.

This is the **fifth** pattern-match false negative recorded across these specs (after `]( \./`
missing a bare relative link, `list-keys` escaping `}`, rendering `C-h` bare, and a link checker
treating `#anchor` as part of a filename). The rule that keeps being relearned: **enumerate what
exists and read it, never assert absence from a constructed pattern.** A grep can only prove
presence; proving absence needs an enumeration.

### Phase 5 (2026-08-26) — 4/4 steps, and the authority path holds

| Claim | Evidence |
|-------|----------|
| Cross-run queue | `LoadPendingGates()` iterates `bus.ScanInFlightGraphRuns()`, keeping only `NodeWaitHuman` nodes in `GraphNodeWaiting`; `TestLoadPendingGates_CrossRunAndFlags`, `…_IgnoresFinishedRuns` |
| **Frozen definition** | downstream comes from `bus.ReadGraphRunGraph(session, run.ID)` — the run's own `graph.json`, **not** `ResolveGraphTemplate` — so a template edited after launch cannot change what the queue reports |
| Mutation flagging | `GateImpact.Mutating`; the confirm frame prints `⚠ this approval releases a git/Atlassian mutation` in `Red+Bold` |
| No predicate drift | `bus/graph.go` exports `NodeRequiresGate()` wrapping the validator's own `nodeRequiresGate`, and `tui/graph.go:704` calls it — the queue flags approvals with **the same predicate the `wait_human` validation rule applies**, so the two cannot diverge |
| Re-arm warning | `GatesRearmedByRetry()` wired into the retry confirm at `graph_ui.go:398` (checked at the call site, not just the definition) |
| Empty state | `No gates waiting` literal; `TestRenderGateQueueFrame_EmptyState` |
| Tests | **83 top-level, 0 FAIL**; 9 new gate tests |

**The authority criterion is genuinely satisfied, and it was the one worth checking hardest.**
`executeAction()` calls `bus.ApproveGraphGate` / `CancelGraphRun` / `RetryGraphRun` directly — the
*same* functions `cmd/graph.go` invokes at lines 69 / 52 / 153. A grep for `bus.Send`, `SendNoCC`,
and `exec.Command("muxcode")` across all of `tui/` returns **nothing**, so there is no
bus-message path and no CLI shell-out into the approval. Pressing the key is the human at the
keyboard, exactly as the spec requires.

One detail beyond what was asked: approve **re-reads node status at execution time** and refuses
if the gate left `waiting` between the confirm render and the keypress
(`TestGraphUI_ApproveRefusedOnNonWaitingNode`). That closes a TOCTOU window where a stale confirm
frame could approve something other than what it described.

#### Open question: gate queue ordering may be backwards

`LoadPendingGates` sorts `gates[i].Waiting < gates[j].Waiting` — **shortest wait first**, which
does match this spec's literal "newest first". But for a queue whose purpose is "what is blocked
on me", the gate waiting three hours matters more than the one waiting ten seconds, and the flat
fallback list already sorts by urgency (failed/waiting first). The implementation follows the
criterion as written; the criterion may be the thing that is wrong. Flagged rather than changed —
it is a UX call for the user, not a defect.

### Phase 6 (2026-08-26) — 2/2 steps, and the menu is whole again

| Claim | Evidence |
|-------|----------|
| Popup configs | Registry went 5 → 8: `graph-runs`, `graph-launch`, `graph-gates`, each `AutoCap: true` per the modal convention for self-sizing TUIs |
| Menu group | `config/tmux.conf:59-61` — `Graph Runs (g)`, `Launch Graph (G)`, `Pending Gates (a)`, exactly the freed keys this spec reserved |
| README | keybinding row now names the graph group |

**The net −6 sequencing gap is closed.** Phase 1 removed six entries and Phase 6 adds three; the
menu is no longer a pure loss, so the branch is safe to merge on that count.

### Runtime criteria — partially verifiable now

The installed binary advanced to the Phase 3 era, so some runtime behavior is finally checkable
by execution rather than by reading:

- `muxcode graph ui --render-once --width 90` **renders a real frame** — header, rule, and an
  explicit `No graph runs` empty state — then exits. Criterion checked off on live output.
- `--gates` and `--templates` are **still not in the binary**: `--gates` is swallowed as a run id
  (`unknown run "--gates"`), proving the binary predates both the flags and the unknown-flag
  rejection now in source. The popups fail the same way — `muxcode popup graph-runs` returns
  `unknown popup`, listing only the five pre-MUX-031 entries.

So the three new popups and the menu group are **correct in source but not yet live**. A
`./build.sh` makes them real.

### Phase 7: the script exists and has never been run

`scripts/test-graph-tui.sh` was created at 09:32 and there is **no run log anywhere**. Existence
is not execution — the MUX-014 integration script sat unrun in exactly this state while its spec
read 30/32, and running it then surfaced two production defects that all 15 executor unit tests
had passed straight over.

**Order matters here, and getting it backwards wastes the run.** The script's own CLAUDE.md entry
says it *requires the installed binary to include MUX-031* — and the binary currently does not
have `--gates`, `--templates`, or the popups. Running it now would fail on features that are
present in source, producing a false red exactly as MUX-103 produced a false 19/22 against a stale
binary. **Build first, then run.**

### Rebuild landed 09:42 — the runtime surface is live

The stale-binary blocker that ran through Phases 3–6 is cleared. Every behavior documented in
`docs/agent-bus.md` now verifies by execution:

| Behavior | Result |
|----------|--------|
| `graph ui --gates --render-once` | renders the `Pending Gates` frame |
| `graph ui --templates --render-once` | `Error: --templates has no --render-once form` — the documented refusal, not a crash |
| `graph ui --bogus` | `Error: unknown flag "--bogus" for graph ui` |
| `muxcode popup graph-runs` | registered; no longer `unknown popup` |

That the two *error* paths behave exactly as documented matters as much as the success paths — it
confirms the docs describe the built binary rather than the source I read.

### Phase 7 blocked: the integration script has never executed a single check

First run reported `exit 0` with output `SKIP: installed muxcode lacks MUX-031 graph ui — run
./build.sh`. The binary **does** have `graph ui`. Two independent defects, both in the script:

**1. The capability probe always false-skips.** `scripts/test-graph-tui.sh:20` reads:

```bash
if ! muxcode graph 2>&1 | grep -q ' ui '; then   # under set -euo pipefail
```

`muxcode graph` with no args prints usage and **exits 1**. Under the script's `set -euo pipefail`
the pipeline inherits that 1 even when `grep` matches, so `!` inverts it and the SKIP branch is
taken unconditionally. Verified both ways:

| Shell | Result |
|-------|--------|
| no `pipefail` | probe passes |
| `set -euo pipefail` | **SKIP taken, always** |

Fix: capture first, test second — `out=$(muxcode graph 2>&1 || true); case "$out" in *" ui "*) ;; *) skip ;; esac`.

**2. A total skip exits 0.** Zero checks executed is reported as success. In CI or a casual run
that reads as "the integration test passed" — strictly worse than a failure, because it is
indistinguishable from green. A skip should exit non-zero, or print a count that makes `0 checks`
impossible to miss.

**Correction to my own diagnosis.** I first attributed the skip to a race, having noticed the
binary was rewritten at 09:45 — the same minute the probe ran — and my own shell test of the probe
passed. Both observations were real; the inference was wrong. The run agent supplied a testable
mechanism, and it reproduced deterministically. My test passed only because my interactive shell
has no `pipefail`, so **I tested the probe in a different environment from the one that runs it** —
the same class of error as testing a repo file when the installed file is what executes.

**Resolved 09:51.** Both fixes verified in the script by reading it, not by report: the guard now
captures `graph_usage=$(muxcode graph 2>&1 || true)` before grepping (re-tested under
`set -euo pipefail` — the probe passes), and all three SKIP paths `exit 2`, so zero-checks can
never read as green again.

**The run itself: `42 passed, 0 failed`, exit 0** — taken firsthand from the run agent rather than
from the relay that reported it, since the only messages the run agent had sent *me* were the two
failures. Confirming a green result through the agent that produced it costs one message and is
the difference between evidence and hearsay.

**Coverage, checked against the criteria rather than the count.** The 42 include real
discriminators, not just positive assertions: *"benign gate is **not** flagged"* guards against a
queue that flags everything, and *"wide pane renders the grid (negative control)"* guards against a
fallback that always fires. Failed-before-done ordering is asserted by comparing line numbers
(script lines 225–230) — an `ok`/`fail` pair rather than an `assert_contains`, so enumerating
assertion names alone would have missed it.

**One half-covered criterion.** The popup step asserts the six removed names return
`unknown popup`, but **nothing asserts their CLI commands still succeed**. That half was verified
by hand during Phase 1 (all six executed, exit 0, real output) and is not in the script. It stays
unchecked here, because a criterion covered only by a manual run should not read the same as one
the suite defends.

### Final pass (2026-08-26) — 58/60, two knowingly unmet

All seven phases are complete. Evidence base: **83 unit tests** (0 fail) and **42 integration
checks** (0 fail, exit 0, confirmed firsthand with the run agent), plus live CLI probes of every
documented flag and error path.

`config/tmux.conf` parse-clean was verified **on an isolated tmux server** (`tmux -L parseprobe`),
not the live session: sourcing an unverified config into the user's running session is the very
failure the criterion guards against — an unquoted `}` once aborted this file and silently killed
25 later directives. Sourcing it to test it would have risked exactly that. The isolated server
sourced without error, bound `Graph Runs`, and kept all post-menu directives (320 bindings,
matching the live count).

**Note for whoever merges:** the live session still runs the pre-MUX-031 menu — the config on disk
is correct but was never re-sourced here. `prefix + b → Reload Config` (or a new session) picks up
the graph group.

#### The two unmet items, and why they are not being checked

| Item | Status |
|------|--------|
| Node detail: `EndedAt`, message id, input preview | **Genuinely absent.** Status, `StartedAt`, `Outcome`, `Output`, `TaskID`, and `Worktree` render; three named fields do not. Small, real, and worth a follow-up rather than a silent tick |
| Removed-popup criterion, CLI half | The script asserts the six popup names return `unknown popup`, but **nothing asserts their CLI commands still succeed**. Verified by hand in Phase 1 (all six ran, exit 0, real output). A criterion held up only by a manual run should not read identically to one the suite defends |

Neither blocks merge. Both are recorded so the next reader inherits the truth rather than a clean
scoreboard — the same reason MUX-014 closed at 31/32 with a *Known gaps* table instead of a tidy
100%.

### Spec sequencing flaw found during verification

The acceptance criterion *"Menu gains a graph group on freed keys"* sits in the **Menu reclamation** group, but it cannot be satisfied in Phase 1 — the three surfaces it points at do not exist until Phases 3–5, and the keybinds land in Phase 6. Phase 1 therefore leaves the menu with a **net loss** of six entries and no replacement, which is a real intermediate state a user will see if this branch is merged before Phase 6. Either the phases stay sequential and that gap is accepted knowingly, or Phase 1 should be held back and landed together with Phase 6.

## Open questions

- **Edge routing limits** — box-drawing routing degrades on dense non-planar graphs; is the fallback threshold on width alone, or also on edge-crossing count?
- **Watch mode for `--render-once`** — a `--watch` flag (frame per transition, for piping to logs) is cheap once rendering is pure; worth it in v1?
- ~~**Gate notification interplay**~~ — **Resolved 2026-08-26 by user decision, in [`MUX-105`](./MUX-105-force-respond-escalation.md).** Neither replace nor complement: dispatching a `wait_human` node **auto-opens the Pending Gates popup** (best effort, opt-out `MUXCODE_GATE_AUTOSHOW_DISABLE`, pinned by `TestExecHumanGateAutoShowsGatesPopup`). The edit notification is kept. Motivated by a demo gate that sat unnoticed for 37 minutes — a flash says *that* something waits, the popup shows **what** you are approving and what it releases, which is the surface this spec already built.
- **Menu vs modal for the run browser** — `Graph Runs` as a popup is consistent with the menu, but a long-lived DAG watch may want a registered modal (toggle, PID-tracked) like the API and provider surfaces. Popup first, promote if it gets used as a watch window?

## Sources

- [`MUX-014-graph-agent-orchestrator.md`](./MUX-014-graph-agent-orchestrator.md) — run store, node states, gate semantics
- [`../completed/MUX-069-modal-window-manager.md`](./MUX-069-modal-window-manager.md) — modal conventions
- `tools/muxcode/tui/{remote,model,provider_select}.go` — navigation, tick, confirm patterns
- `tools/muxcode/bus/{popup,measure}.go`, `config/tmux.conf` — the menu and popup surface being reclaimed

## Provenance

Filed by the plan agent on 2026-08-24 from a user request to visualize the graph-agent feature in a TUI. Rescoped 2026-08-25 on user request: the `prefix + b` menu's agent/process groups were judged low-value and are retired here, with their slots reallocated to graph management surfaces.

## Deferred gaps at completion

Closed at **58/60**. Two criteria are knowingly unmet and were not checked off; both are small,
neither blocks the feature, and both are recorded here so the next reader inherits the truth
rather than a clean scoreboard.

| Gap | Why it matters | Evidence status |
|-----|----------------|-----------------|
| Node detail omits `EndedAt`, the correlated **message id**, and the **input preview** | A finished node shows when it started but not when it ended, so duration is not readable from the view that exists to explain a node. The message id is the thread back to the bus record | Verified absent: `RenderNodeDetailFrame` renders status, `StartedAt`, `Outcome`, `Output`, `TaskID`, `Worktree` and nothing else |
| Removed-popup criterion, CLI half | `scripts/test-graph-tui.sh` asserts the six removed popup names return `unknown popup`, but **nothing asserts their underlying CLI commands still succeed**. The whole justification for retiring those menu entries was that the capability survives on the CLI — that half is undefended by the suite | Verified by hand during Phase 1 (all six executed, exit 0, real output). A criterion held up by one manual run should not read the same as one the suite defends |

A third item is a **decision, not a defect**: the gate queue sorts shortest-wait-first, which
matches this spec's literal "newest first". For a queue answering *"what is blocked on me"*, the
gate waiting three hours arguably outranks the one waiting ten seconds, and the flat fallback list
already sorts by urgency. The implementation follows the criterion as written; the criterion may be
what is wrong. Left for the user.

## Time Tracking

| Branch | Active time | Last updated |
|--------|-------------|--------------|
| MUX-031-graph-run-tui | 1h 25m | 2026-08-26 10:00 |

## Status

Complete
