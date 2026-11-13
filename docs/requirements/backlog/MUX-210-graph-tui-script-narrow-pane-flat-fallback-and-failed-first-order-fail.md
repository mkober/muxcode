# MUX-210: `test-graph-tui.sh` Fails — a Narrow Pane Renders No Flat Fallback, and a Failed Node Lists After a Done One

**Tracking:** [mkober/muxcode#181](https://github.com/mkober/muxcode/issues/181)

`scripts/test-graph-tui.sh` fails two checks in its "wide fallback" section. Rendered at
`--width 60`, a deliberately wide graph's frame does not contain the `flat view` notice, and its
failed node appears one line **below** its done node, where the script expects failed first. The
wide-pane negative control still passes. Whether the renderer changed or the fixture stopped being
wider than 60 columns is this spec's first question.

## Context

### Source and standard of evidence

Filed 2026-11-13 on the user's instruction relayed by edit, while closing out
[MUX-208](../completed/MUX-208-durable-bus-state-in-tmp-deleted-by-macos-tmp-cleaner.md), whose "every
`scripts/test-*.sh` still passes" criterion excludes this script as a failure it did not cause.
Observed by plan in the run agent's logs; nothing here was re-run by plan, and the failing frame
itself was not captured. Code read at `b8bfcab` plus the uncommitted MUX-208 tree.

### Observation — verified

| Fact | Where |
|------|-------|
| The script failed in two runs on 2026-11-13: the 15:11 `bash scripts/test-all.sh` and the 15:38 rerun | proc logs `1794600689-proc-f90797b3` and `1794602297-proc-b8c4f9fe`; `/tmp/mux208-rerun-logs/test-graph-tui.log` |
| `FAIL: narrow pane renders the flat fallback (missing: flat view)` | same log |
| `FAIL: failed node lists before done node (failed=9 done=8)` — `stage-bravo-compile` (failed) on line 9, `stage-alpha-preflight` (done) on line 8 | same log |
| `ok: wide pane renders the grid (negative control)` and every other section pass | same log |
| The failing sequence: `start_run wide.json`, three `set_node` calls (done, failed, waiting), `muxcode graph ui "$WIDE_RUN" --render-once --width 60`, then `assert_contains … "flat view"` and a line-number comparison | `scripts/test-graph-tui.sh:208-224` |
| The notice is still in the renderer: `(graph wider than pane — flat view)` | `tools/muxcode/tui/graph.go:1952` |
| Binary under test: `v0.1.39-2-gb8bfcab-dirty`, installed 15:10 from the MUX-208 tree | `muxcode version` |

### Why this is not MUX-208's — verified

| Fact | Where |
|------|-------|
| No non-test file under `tools/muxcode/tui/` is modified in the MUX-208 working tree; the one change is `tui/main_test.go` pinning `XDG_STATE_HOME` for the package's tests | `git status --short`, `git diff` |
| The script's only MUX-208 change is how it computes its scratch bus directory (`muxcode_bus_dir`) | `git diff -- scripts/test-graph-tui.sh` |

The script was **not** run against `main`'s own binary, so "fails the same way before MUX-208" is
inferred from the unchanged renderer, not observed.

### Cause — not determined

Candidates, none verified:

- A renderer change. The graph TUI was last touched by `28eaad8` (PR #178, "Stop TUI frames scrolling
  the pane") and `db97d7e` (PR #163, redraw-over-overlay); either may have changed when the grid
  degrades, or how `--render-once --width` is honoured.
- The fixture no longer overflows 60 columns, so the grid fits and the fallback is correctly not
  taken. The line order `failed=9 done=8` is what a grid in topological order would give, which is
  consistent with the grid having rendered — and the Go tests that assert `flat view`
  (`tui/graph_test.go:221,544,568`) pass in the suite.

If the second holds, the script has been asserting nothing about overflow — the case
[`docs/tui-style.md`](../../tui-style.md) names: a fixture that never overflows cannot test
overflow.

### Blast radius

- `bash scripts/test-all.sh` exits 1 on every run, so any spec with an "all integration scripts
  pass" criterion must carve this script out by hand, as MUX-208 did.
- If the first candidate holds, a graph wider than its pane overflows instead of degrading — the
  MUX-031 clamp-to-pane defect, back.

## Requirements

### Acceptance criteria

- [ ] The cause is recorded here: renderer change or a fixture that no longer overflows — with the
  introducing commit and the captured `--width 60` frame
- [ ] A graph wider than the pane renders the flat fallback with its notice; no line of the frame
  exceeds the requested width
- [ ] In the flat fallback a failed node lists before a done one
- [ ] The script's fixture provably overflows the width it is rendered at — the script asserts the
  grid's width exceeds the pane before asserting the fallback
- [ ] Negative control kept: the same graph at a wide pane renders the grid, not the fallback
- [ ] `bash scripts/test-graph-tui.sh` exits 0 with every check passing

### Technical approach

Phase 1 captures the failing frame and measures it: if no line exceeds 60 cells and the grid is
drawn, the fixture is the defect; if a line overflows, the renderer is. Run the script against a
binary built from `main`, then against `28eaad8^`, to place the change.

### Key files

| File | Purpose |
|------|---------|
| `scripts/test-graph-tui.sh:208-224` | The failing section and its `wide.json` fixture |
| `tools/muxcode/tui/graph.go` | Grid layout, the width check and the flat fallback (`:1952`) |
| `tools/muxcode/tui/graph_test.go` | Unit tests that pin the fallback and pass today |
| `docs/tui-style.md` | Clamp-to-pane and negative-control rules |

## Implementation

### Phase 1: Establish the cause

- [ ] Capture `muxcode graph ui <wide run> --render-once --width 60` and record the frame, its widest
  line and whether the grid or the list was drawn
- [ ] Run `bash scripts/test-graph-tui.sh` against a binary built from `main`, and from `28eaad8^`;
  record pass/fail for the two checks
- [ ] Decide: renderer or fixture; record it here

### Phase 2: Fix

- [ ] Renderer: restore the degradation, with a Go test whose fixture overflows and a sibling that
  does not
- [ ] Fixture: widen `wide.json` (or narrow the pane) until the grid provably overflows, and assert
  that precondition in the script
- [ ] Failed-before-done ordering holds in the flat list, pinned by a test

### Phase 3: Integration test

- [ ] `bash scripts/test-graph-tui.sh` through the run agent: every check passes, exit 0; record the
  counts here
- [ ] Negative control: with the fixture reduced to fit the pane, the fallback assertion fails —
  proving it exercises the width it claims to
- [ ] `bash scripts/test-all.sh` no longer lists `test-graph-tui` as failed

## Related

- [MUX-208](../completed/MUX-208-durable-bus-state-in-tmp-deleted-by-macos-tmp-cleaner.md) — the
  close-out that excluded this script
- [MUX-031](../completed/MUX-031-graph-run-tui.md) — the spec the script belongs to, and the
  clamp-to-pane defect it pinned
- [MUX-151](./MUX-151-display-width-runes-not-cells.md) — width measured in runes, not cells

## Status

Backlog

Filed 2026-11-13 on the user's instruction relayed by edit. The two failures are observed in two
runs; the cause is **not determined**, the failing frame was not captured, and that the failure
predates MUX-208 is inferred, not observed. Not started.
