# Shared TUI Component Kit

Extract the layout primitives now duplicated across `tui/graph.go`, `tui/graph_ui.go`, `tui/remote.go`, and `tui/model.go` into a small shared kit — tab bar, footer, list, confirm, empty state — with pinning tests, so the conventions in [`docs/tui-style.md`](../../tui-style.md) are enforced by code rather than by review diligence.

## Context

### Why now

Three TUIs were built in quick succession (dashboard, remote browser, graph surfaces) and each grew its own copy of the same four or five patterns. They agree today only because the same conventions were applied by hand each time. That is exactly the state in which drift begins, and drift in a TUI is invisible until a user is confused by it.

The style guide written alongside this spec documents the conventions. **A guide is a promise; a shared component is a guarantee.** The failures it catalogues — a `height` parameter accepted and ignored, a popup title stale after a state change, a blank frame where an empty state belonged — are all cases where following the convention was optional and someone reasonably didn't.

### What is duplicated today

| Pattern | Where it appears | Divergence risk |
|---------|------------------|-----------------|
| Footer key hints | `graph_ui.go` (per-view), `remote.go`, `model.go` | Spacing, key color, and which keys get advertised are re-decided each time |
| Tab bar | `graph.go` `renderSurfaceTabs()` | Only one exists; a second surface family would copy it |
| Scrollable/clamped list | `graph.go` fallback list, `remote.go` agent rows, `model.go` rows | Clamping and selection-cursor rendering re-implemented per site |
| Confirm prompt | `graph_ui.go` `renderConfirmFrame`, `remote.go` `renderForceRespondConfirm` | Both gate on `y`, both swallow other keys — by convention, not by construction |
| Empty state | three literals in `graph.go` | Easy to omit entirely, which is the failure mode |
| Header band | every surface | Margin and separator width re-specified each time |

### Scope boundary

This is a **refactor with pinning tests**, not a redesign. Rendered output should be byte-identical before and after wherever the current output is already correct. Where two call sites currently differ, the kit adopts the better one and the change is called out — silently normalising a difference is how a refactor becomes a regression.

## Requirements

### Acceptance criteria

- [ ] A `tui/kit.go` (or `tui/components/`) provides: tab bar, footer, list body, confirm prompt, empty state, header band
- [ ] Every component is a **pure function** — snapshot in, string out — consistent with the render-once seam
- [ ] Components take explicit `width` and `height` and clamp to them; none can be called in a way that overflows the pane
- [ ] The empty state is **not optional**: rendering a list with zero items produces the empty-state band, so omitting it is impossible rather than merely discouraged
- [ ] Footer takes key/label pairs as data, not a preformatted string, so spacing and key color cannot drift between call sites
- [ ] Confirm prompt takes the action description and an optional warning line, and the component owns the `y`/`n`/swallow-everything-else contract
- [ ] Pinning tests capture current rendered output for each existing call site **before** the refactor, and assert byte-identical output after
- [ ] Any intentional difference is listed in the spec with a before/after and a reason — no silent normalisation
- [ ] `graph_ui.go`, `remote.go`, and `model.go` are migrated to the kit; no local copy of a kit pattern survives
- [ ] `scripts/test-graph-tui.sh` (46 checks) still passes unchanged — it is the existing end-to-end guard on graph frames
- [ ] Stdlib only, matching the rest of `tui/`

### Technical approach

- **Pin first, refactor second.** Capture golden frames from every current call site as test fixtures, land those tests green against today's code, *then* extract. A refactor whose safety net is written afterwards is a rewrite.
- **Data in, string out.** `Footer([]KeyHint{{"Enter","Open"}, …}, width)` rather than a `fmt.Sprintf` per view. The formatting decision moves from six sites to one.
- **Make the omission impossible.** `List(items, …)` renders the empty state when `len(items) == 0`. The style guide's rule 5 becomes unforgettable rather than well-known.
- **Height is a parameter, not a suggestion.** Every component signature takes it, and the list component clamps. This is the MUX-031 `height`-ignored defect made structurally unrepeatable.
- **Migrate one TUI at a time**, each with its pinning tests green before the next, so a regression is attributable.

### Key files

| File | Change |
|------|--------|
| `tui/kit.go` | New — the components |
| `tui/kit_test.go` | New — component unit tests + golden frames |
| `tui/graph.go`, `tui/graph_ui.go` | Migrate tab bar, footer, list, confirm, empty state |
| `tui/remote.go` | Migrate footer, list, confirm |
| `tui/model.go` | Migrate footer, list, header |
| `docs/tui-style.md` | Point each convention at its enforcing component |

## Implementation

### Phase 1: Pin current behaviour

- [ ] Golden-frame fixtures for every current call site (graph runs / DAG / node / templates / gates, remote list + detail + confirm, dashboard)
- [ ] Tests assert those frames against today's unrefactored code and pass
- [ ] Record any place two call sites already disagree — these are the decisions Phase 2 must make explicitly

### Phase 2: Build the kit

- [ ] `Header`, `TabBar`, `List`, `Footer`, `Confirm`, `EmptyState` as pure functions taking width and height
- [ ] `List` renders the empty state itself when there are no items
- [ ] `Footer` takes `[]KeyHint` data
- [ ] Unit tests per component, including clamping at absurd widths/heights and a zero-item case

### Phase 3: Migrate

- [ ] Migrate `graph.go`/`graph_ui.go`; golden frames must match byte-for-byte or the difference is documented
- [ ] Migrate `remote.go`
- [ ] Migrate `model.go`
- [ ] Verify no local copy of a kit pattern remains (enumerate, do not grep for a guessed name)

### Phase 4: Integration test

- [ ] `scripts/test-graph-tui.sh` passes unchanged — 46 checks, no edits to the script
- [ ] Extend it with one assertion per kit component reachable via `--render-once`
- [ ] Test: a zero-item list renders the empty-state band, header and footer intact
- [ ] Test: a component clamped to a tiny pane produces no line wider than the pane
- [ ] Run the script and verify all checks pass

## Open questions

- **Package or file?** A `tui/components/` package forces exported names and a clean boundary; a `tui/kit.go` file is less ceremony. The package is probably right if the kit outgrows five components.
- **Does the dashboard's live-refresh model fit?** `model.go` re-renders on a tick with different data shapes; it may need a component variant rather than the same one.
- **Should the kit own colors?** If components take a semantic role (`RoleWaiting`) rather than a raw constant, the palette becomes swappable — but it adds indirection for a codebase with one theme.

## Sources

- [`docs/tui-style.md`](../../tui-style.md) — the conventions this spec makes enforceable
- `tools/muxcode/tui/{styles,graph,graph_ui,remote,model}.go`
- [`MUX-031`](../completed/MUX-031-graph-run-tui.md) — `height`-ignored defect, empty-state and negative-control lessons
- [`MUX-105`](../drafts/MUX-105-force-respond-escalation.md) — selection-by-id, stale-title, and escape-sequence lessons

## Provenance

Filed by the plan agent on 2026-08-26 at the user's request, alongside `docs/tui-style.md` and the `tui-style` checklist skill. The three artifacts split by enforcement strength: the skill is a prompt-time reminder, the guide is a written rule, and this spec makes the rules structural.

## Status

Backlog
