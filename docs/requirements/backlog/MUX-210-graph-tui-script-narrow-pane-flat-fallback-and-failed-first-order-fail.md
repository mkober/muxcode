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

## Decisions (user, 2026-11-24)

Relayed by edit. **The restatement proposed under [Results for Phase 1](#results-for-phase-1) is
adopted.** The wrap is the design; the script and the criteria were stale, not the renderer.

| Point | Decision |
|-------|----------|
| Width overflow | Renders the **wrapped layers in step order**. No flat list, no `flat view` notice |
| Clamp to pane | **No line of the frame is wider than the pane, header included.** The header clamp — the 89-cell run line found in Phase 1 — is in this spec's scope, not a spec of its own |
| Flat fallback | Still exists, failed before done, and is pinned through **height** overflow: a wrap too tall to fit |
| Negative control | The wide pane still renders the grid |
| Script and docs | The script's `wide` fixture and assertions are rewritten to the above; `docs/tui-style.md`'s MUX-031 passage, which still says width overflow gives the flat list, is updated |

Checked in the tree on 2026-11-24: `RenderGraphFrame` (`tui/graph.go:414-424`) takes the wrap when
the grid is wider than the pane and the wrap fits the height, and the flat list when the wrap does not
fit or the grid is too tall; the flat list's notice reads `(graph wider than pane — flat view)` for
both causes (`:1952`). `muxcode graph ui --render-once` accepts `--width` and has **no `--height`**
(`cmd/graph.go:330`), so the script cannot ask for a short pane today.

## Requirements

### Acceptance criteria

Criteria 2 and 3 were rewritten on 2026-11-24 to the decision above; as filed they asked for the flat
fallback on width overflow.

- [x] The cause is recorded here: renderer change or a fixture that no longer overflows — with the
  introducing commit and the captured `--width 60` frame
- [ ] A graph wider than the pane renders its layers **wrapped, in step order** — a done node before
  the failed node that follows it in the pipeline — with no `flat view` notice
- [ ] **No line of that frame is wider than the requested width**, the run header included, measured
  in display cells on the colour-stripped frame; a long run id or graph path is truncated, not
  overflowed
- [ ] A graph whose wrap is too **tall** for the pane renders the flat fallback with its notice, and in
  it a failed node lists before a done one
- [ ] The flat fallback's notice names the cause it was taken for — it does not say "wider than pane"
  when the pane was too short
- [ ] The script's fixture provably overflows what it is rendered at — it asserts the grid's width
  exceeds the pane before asserting the wrap, and the wrap's height exceeds the pane before asserting
  the fallback
- [ ] Negative control kept: the same graph at a wide pane renders the grid, neither the wrap nor the
  fallback
- [ ] `bash scripts/test-graph-tui.sh` exits 0 with every check passing

### Technical approach

Phase 1 captured the failing frame and placed the change (`72e5fa2`); see its results below. What is
left is three things: clamp the header, give the script a way to ask for a short pane, and rewrite
the script's `wide` section to what the renderer is meant to do.

The header (`renderGraphHeader`, `tui/graph.go:807`) takes `width` already; its run line — run id,
state, graph path, progress, age — is written unclamped. Truncate the path first (it is the long and
least identifying part), then the run id, keeping state and progress whole. Widths are measured in
cells; [MUX-151](./MUX-151-display-width-runes-not-cells.md) owns the rune-versus-cell question and
this spec uses whatever measure the frame already uses.

`--render-once` gains `--height` beside `--width` so the script can render a pane too short for the
wrap. The Go tests that pin `flat view` (`tui/graph_test.go`) already pass a height; the script is
the surface that could not.

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

> Answered 2026-11-24: the user adopted the restatement below, header clamp included — see
> [Decisions](#decisions-user-2026-11-24). The rest of this section is the 2026-11-17 record, kept as
> written.

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

### Phase 2: Clamp the header, rewrite the script

Rewritten 2026-11-24 to the user's decision. As filed this phase restored the flat fallback on width
overflow; that is not wanted. The renderer change, the flag and the script's rewritten section land in
one change, so `./test.sh` stays green and the script goes from two failures to none without an
intermediate state that fails differently.

- [ ] `renderGraphHeader` clamps every header line to `width`, truncating the graph path and then the
  run id; Go test with a fixture whose run line overflows 60 cells, and a sibling whose run line fits
  and is rendered whole (negative control)
- [ ] Go test: every line of the wrapped frame at a narrow width is within the width, header included;
  and a wrap too tall for the height gives the flat list with failed before done — or name the
  existing tests that already pin each
- [ ] The flat fallback's notice names its cause: wider than the pane, or taller
- [ ] `muxcode graph ui --render-once` accepts `--height`; documented in `docs/agent-bus.md`
- [ ] Rewrite the script's `wide` section: at `--width 60` assert the grid would overflow, then the
  wrap — step order (done before failed), no `flat view`, and no line wider than 60 cells
- [ ] Add a height section: the same graph at a width that forces the wrap and a `--height` too short
  for it asserts the wrap would overflow, then `flat view` and failed before done
- [ ] Keep the wide-pane negative control: the grid, no `flat view`
- [ ] Raise the script's coverage floor by the checks added
- [ ] Update [`docs/tui-style.md`](../../tui-style.md)'s MUX-031 passage (the "Why (MUX-031)" paragraph
  and "The fix: `gridW > width || …` → flat list" line): width overflow wraps in step order, the flat
  list is for height overflow, and the header is clamped like every other line

### Phase 3: Integration test

- [ ] `bash scripts/test-graph-tui.sh` through the run agent: every check passes, exit 0; record the
  counts here
- [ ] Negative control: with the fixture reduced to fit the pane, the script's overflow preconditions
  fail — proving each assertion exercises the dimension it claims to
- [ ] Negative control: against a binary built from a copy of the tree with the header clamp removed,
  the line-width check fails on the run line
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

**Backlog** — unparked 2026-11-24 on the user's decision, relayed by edit and recorded under
[Decisions](#decisions-user-2026-11-24): the proposed restatement is adopted, with the header clamp in
scope. Phase 1 is complete and criterion 1 met; criteria 2–3 and Phases 2–3 are rewritten to the
decision. Nothing of Phase 2 has been built.

History: activated 2026-11-17 by `55-defect-backlog-loop` run
`1794626267-55-defect-backlog-loop-15547e2e` (Defects rank 33) and parked the same day after Phase 1,
on edit's instruction (`1794951592-edit-4357a68e`), because criteria 2 and 3 asked for the flat
fallback on width overflow, which the renderer has deliberately not given since `72e5fa2`.

Filed 2026-11-13 on the user's instruction relayed by edit. As filed, the cause was not determined,
the failing frame was not captured, and that the failure predates MUX-208 was inferred. All three
are settled since 2026-11-17: the script's premise is stale, the frame is recorded, and the failure
dates from `72e5fa2` (2026-08-28).
