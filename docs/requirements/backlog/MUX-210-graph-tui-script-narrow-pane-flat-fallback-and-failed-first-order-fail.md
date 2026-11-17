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

- [x] The cause is recorded here: renderer change or a fixture that no longer overflows — with the
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

- [x] Capture `muxcode graph ui <wide run> --render-once --width 60` and record the frame, its widest
  line and whether the grid or the list was drawn
- [x] Run `bash scripts/test-graph-tui.sh` against a binary built from `main`, and from `28eaad8^`;
  record pass/fail for the two checks
- [x] Decide: renderer or fixture; record it here — **neither**, as recorded below: the renderer
  wraps by design and the fixture still overflows

### Results for Phase 1

Recorded 2026-11-17 from the worker's findings (`/tmp/mux210-phase1-findings.md`). Plan looked up
both commits, read the renderer's comments and the run agent's history row for the bisect. Boxes are
ticked at `verify-spec`, not here.

**The captured frame** (`HEAD` `b43b5f4`, the script's `wide.json` and node states; run task
`1794951229-spawn-26088880-bac85a95`; full text in `/tmp/mux210-phase1.out`):

| Width | What is drawn |
|-------|---------------|
| `--width 60` | 19 lines, no `flat view`. The nodes are drawn as **wrapped layers in step order** — alpha ✓, bravo ✗, charlie ◐, delta, echo, each followed by `─→` — so the done node (line 7) precedes the failed one (line 8). The widest line is **89 cells**: the run header, `<run-id>  [running]  <fixture path>  1/5 done  0s`, which is not clamped |
| `--width 250` | The grid on one row, 201 cells, no `flat view` — the negative control holds |

The `--width 60` frame as captured, colour stripped by the capture (the run id and scratch path are
the capture's own):

```

  Prompt / Launch Graph / Graph Runs / Pending Gates   ⇥ Tab
  1794951239-wide-fixture-532b1801  [running]  /tmp/mux210-Ng3xAA/wide.json  1/5 done  0s
  fixture
  launched by: run (autonomous)


  ✓ stage-alpha-preflight build:build ─→
  ✗ stage-bravo-compile build:build ─→
  ◐ stage-charlie-verify test:test ─→
  · stage-delta-inspect review:review ─→
  · stage-echo-publish review:review

  ✓ stage-alpha-preflight build:build
  ✗ stage-bravo-compile build:build
  ◐ stage-charlie-verify test:test
  · stage-delta-inspect review:review
  · stage-echo-publish review:review
```

The second line is the 89-cell one. The script's line numbers count from the first node: `done`
on 7, `failed` on 8.

**The bisect.** Each commit ran its own script against its own binary.

| Commit | Result |
|--------|--------|
| `2d8653e`, `c3faf7e`, `d44ead6` (`28eaad8^`) and `HEAD` | Exit 1 — 44 passed, 2 failed, the same two `FAIL`s. `28eaad8` and `db97d7e`, this spec's candidates, are not the cause |
| `6853b74` (2026-08-28) — last passing | 46 passed, 0 failed |
| `72e5fa2` (2026-08-28), "Wrap a wide single-row DAG chain instead of falling back to flat" — first failing | 44 passed, 2 failed. `tui/graph.go` +71 lines, with `graph_test.go` |

Found over 62 candidates from `304dde8`, where the script was added (run task
`1794951332-…`; the run agent's history row gives the steps and the two ends).

**Decision: neither candidate as this spec wrote them.**

| This spec's candidate | Finding |
|-----------------------|---------|
| A renderer regression | No. Width overflow **wraps by deliberate design** since `72e5fa2`; `c3faf7e` (2026-09-18, MUX-148) extended it from single-row chains to every too-wide graph |
| A fixture that stopped overflowing | No. The grid still overflows 60 cells; the renderer wraps it |

The script's premise — width overflow gives the flat list, failed before done — has been stale
since `72e5fa2`. The renderer says why in its own words (`renderWrappedLayers`, `tui/graph.go`): the
flat list "sorts by urgency and so destroys the one thing a reader needs from a pipeline — the order
the steps run in (user request 2026-09-18)", and "Width overflow wraps; the flat list is for height
overflow". Plan read both comments; the attribution to the user is the code's, and plan has no
other record of that request.

**A real defect, found on the way.** The run header line overflows the requested width — 89 cells
at `--width 60`, its length following the run id and the fixture path. That is the clamp-to-pane
rule broken, whatever is decided below. Whether it was already so at `6853b74` is not established:
the script never measured line widths.

**For the user — criteria 2 and 3 cannot be met as written, and plan has not restated them.**

| Criterion as written | Why it conflicts |
|----------------------|------------------|
| 2 — "A graph wider than the pane renders the flat fallback with its notice; no line of the frame exceeds the requested width" | Its first half asks for exactly what the 2026-09-18 change removed. Its second half stands, and fails today on the header |
| 3 — "In the flat fallback a failed node lists before a done one" | The flat fallback is no longer what width overflow produces |

Plan wrote those criteria on 2026-11-13 from the script's own assertions, without knowing the wrap
was deliberate. The worker's suggested restatement, **pending the user's word**: width overflow
renders the wrapped layers in step order with no line wider than the pane, header included; the
flat fallback, failed before done, is pinned through **height** overflow — a wrap too tall to fit;
the wide-pane negative control stays.

**Phase 2 should not start until that is answered.** Its steps are written against the same two
candidates and would have a worker choosing between the script's expectation and a recorded design
decision. `docs/tui-style.md` also still describes width overflow as giving the flat list (its
MUX-031 passage); it wants the same answer before plan touches it.

Verified and ticked 2026-11-17. The phase changed no code; its evidence is the captured frame and
the bisect, which plan read in the worker's output file and the run agent's history, and the two
commits and renderer comments plan looked up. The graph's `./build.sh` (16:37:44) and `./test.sh`
(16:37:57) exited 0; the review at 16:38:37 returned 0 must-fix, 0 should-fix and 1 nit — this
spec's Status and activation text still said the cause was undetermined and the frame uncaptured;
corrected. The review approved the diagnosis only, and said so. The first criterion is ticked on
this section: the cause, the introducing commit and the frame are here.

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

### Findings at activation (2026-11-17)

Activated against the tree at `b43b5f4`. Plan did not re-run the script; whether it still fails, and
in the same way, is Phase 1's first step.

| Spec says | Tree today |
|-----------|------------|
| The notice `(graph wider than pane — flat view)` at `tools/muxcode/tui/graph.go:1952` | Same line |
| The renderer was last touched by `28eaad8` and `db97d7e` | `graph.go`'s newest commit is `b8bfcab` — the tree this spec was filed against. Nothing has changed it since |
| Go tests assert `flat view` and pass | `tui/graph_test.go` holds eight mentions |

At activation plan wrote here that nothing in this spec waited on a user decision — its either/or
would be settled by the frame and the bisect, as in
[MUX-209](../completed/MUX-209-force-respond-script-non-forced-deliver-no-longer-surfaces-in-flight-skip.md).
**Phase 1 showed that was wrong.** The answer is neither branch, and criteria 2 and 3 conflict with
a recorded design decision; see [Results for Phase 1](#results-for-phase-1).

## Status

**Backlog** — parked 2026-11-17 after Phase 1, on edit's instruction (`1794951592-edit-4357a68e`) following plan's recommendation; the user has not yet answered. 4/15 — Phase 1 complete and criterion 1 met. Activated the same day by `55-defect-backlog-loop` run `1794626267-55-defect-backlog-loop-15547e2e` (Defects rank 33).

**Why parked: Phase 2 waits on the user.** Criteria 2 and 3 ask for the flat fallback on width
overflow, which the renderer has deliberately not given since `72e5fa2`. The proposed restatement
is under [Results for Phase 1](#results-for-phase-1) and is not adopted: restating the criteria is
the user's call. Nothing of Phase 2 was built under this spec.

**What would unpark it:** the user's answer on criteria 2 and 3 — and, with it, whether the header
overflow found in Phase 1 is fixed here or in a spec of its own.

Filed 2026-11-13 on the user's instruction relayed by edit. As filed, the cause was not determined,
the failing frame was not captured, and that the failure predates MUX-208 was inferred. All three
are settled since 2026-11-17: the script's premise is stale, the frame is recorded, and the failure
dates from `72e5fa2` (2026-08-28).
