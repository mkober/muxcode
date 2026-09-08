# MUX-151: Display Width Is Measured in Runes, Not Terminal Cells

Every width calculation in the tree counts runes and assumes one rune renders in one terminal
cell. Wide characters — CJK, most emoji — occupy **two** cells, so any string containing them is
measured at half its rendered width and overflows the surface it was fitted to. Review's concrete
case: twenty CJK characters at budget 20 are returned unchanged and occupy 40 columns.

Raised twice by the review agent as a should-fix during the New Session banner restyle on
2026-09-08 (branch `MUX-144-wait-human-gate-openable-by-any-agent`), and **deferred here
deliberately rather than patched** — see *Why it was deferred*. Filed by plan from edit's handoff;
the three sites below were re-verified against the tree at filing.

## Context

### Confirmed sites

| Site | Code | Verified |
|------|------|----------|
| `VisibleWidth` — the shared definition | `tui/styles.go:104` — `return len([]rune(StripAnsi(s)))` | Yes |
| Popup auto-fit measurers | `bus/measure.go:40` — `utf8.RuneCountInString(StripANSI(ln))` in `MeasureLines`, which `MeasureText` and the per-popup measurers build on | Yes |
| `fitPath` — the site review flagged | `cmd/launcher.go:138` — `r := []rune(s)` then a rune-sliced suffix `"…" + string(r[len(r)-(w-1):])` | Yes — **uncommitted** on the MUX-144 branch at filing (`git show HEAD:` has no `fitPath`), so its line number will move |

`VisibleWidth` is the base the other helpers stand on — `Pad` (`tui/styles.go:45`), `TruncateAnsi`
(`:66`), `wrapPlain` (`tui/prompt.go:453`), `padVisible` (`:478`) — so the error is **systemic**, not
local to any one frame. Phase 1 confirms the inheritance per helper rather than taking it on trust.

### Why it was deferred, not fixed in place

1. **A local fix moves the overflow rather than removing it.** Making `fitPath` cell-accurate while
   the frame math around it and the popup measurer that sizes the surface stay rune-based only
   relocates the miscount. The fix is meaningful only when applied at `VisibleWidth` **and** the
   measurers together.
2. **Stdlib-only.** Both Go modules have no external dependencies (a hard rule in `CLAUDE.md`), so
   `runewidth` / `x/text` are unavailable and a hand-rolled East Asian Width table is needed. That
   is a real piece of work with its own correctness surface, not a line change.
3. **Scope.** It surfaced during a banner restyle. Folding a tree-wide width change into that diff
   would have made the diff unreviewable.

### Severity

**Low in practice** for this user base — paths, session names, branches, spec titles and labels are
ASCII today, and no live incident has been observed. It is a **latent correctness gap** that bites
the first time any of those carries a wide character. Ranked accordingly; not urgent.

### Relationship to existing rules and specs

| Reference | Relation |
|-----------|----------|
| [`docs/tui-style.md`](../../tui-style.md) rule 2, *Clamp to the pane; fall back rather than overflow* | This defect is that rule failing silently: a renderer honors `width` and still overflows because its ruler is wrong |
| [`docs/tui-style.md`](../../tui-style.md) rule 4, *Measurers read content, never run the command* | The measurers are correctly content-driven and still mis-size, for the same reason |
| [`MUX-107`](./MUX-107-tui-component-kit.md) | A shared component kit is the natural home for the one width function; if MUX-107 lands first, this work goes into the kit rather than beside it |
| [`MUX-031`](../completed/MUX-031-graph-run-tui.md) | Established the clamp-to-pane discipline this defect undermines |

## Requirements

### Acceptance criteria

- [ ] A **single** width function measures terminal cells, with wide ranges handled, and
  `VisibleWidth` becomes that function or delegates to it
- [ ] `bus/measure.go` popup measurers use the same definition, so a popup sized to content stays
  sized to content with wide characters present
- [ ] `fitPath`, `Pad`, `TruncateAnsi`, `padVisible` and `wrapPlain` inherit it rather than each
  counting for themselves
- [ ] No external dependency is introduced
- [ ] **Negative control:** ASCII strings measure exactly as they do today, so no existing frame
  shifts by a column
- [ ] A fixture frame containing CJK and emoji renders within its declared width, with an ASCII
  control proving the assertion is not vacuous

### Technical approach

Hand-roll the width table in one place (`tui/styles.go` or a new `tui/width.go`), stdlib only:

- Zero-width: combining marks (`unicode.Mn`, `unicode.Me`), zero-width joiner/non-joiner, variation
  selectors, and C0/C1 controls — measure 0
- Wide (2 cells): the East Asian Width `W` and `F` ranges — Hangul Jamo, CJK Unified Ideographs and
  extensions, Hiragana/Katakana, fullwidth forms, and the emoji presentation blocks. A range table
  in source, with the Unicode version it was generated from recorded in a comment
- Everything else: 1 cell

`VisibleWidth` becomes `sum over runes of cellWidth(r)` after `StripAnsi`. `bus/measure.go` cannot
import `tui` (dependency direction: `tui` imports `bus`), so the cell-width primitive lives in `bus`
(next to `StripANSI`, which `measure.go` already uses) and `tui` delegates to it — one table, two
callers.

Truncation and suffix-fitting helpers (`TruncateAnsi`, `fitPath`) must cut on **cell** budget, not
rune count, and must not split a wide rune across the boundary — when the budget lands mid-cell,
fit one cell short.

### Key files

| File | Relevance |
|------|-----------|
| `tools/muxcode/tui/styles.go` | `VisibleWidth` (`:104`), `Pad` (`:45`), `TruncateAnsi` (`:66`) |
| `tools/muxcode/tui/prompt.go` | `wrapPlain` (`:453`), `padVisible` (`:478`) |
| `tools/muxcode/bus/measure.go` | `MeasureText` (`:30`), `MeasureLines` (`:40`) and the per-popup measurers |
| `tools/muxcode/cmd/launcher.go` | `fitPath` (`:138` in the working tree at filing) |
| `docs/tui-style.md` | Rules 2 and 4 — add the cell-width rule once this lands |

## Implementation

### Phase 1: Pin the miscount

- [ ] Unit test: `VisibleWidth` of twenty CJK characters returns 40 (fails today with 20)
- [ ] Unit test: `MeasureLines` of a CJK line returns its cell width (fails today)
- [ ] Unit test: `fitPath` of a CJK path at budget 20 returns a string whose cell width is ≤ 20
  (fails today)
- [ ] Confirm per helper that `Pad`, `TruncateAnsi`, `padVisible`, `wrapPlain` route through
  `VisibleWidth`, and record any that count on their own

### Phase 2: One cell-width primitive

- [ ] Add the range table and `CellWidth(r rune) int` in `bus`, with the Unicode version recorded
- [ ] Add `bus.DisplayWidth(s string) int` = strip ANSI, sum cell widths
- [ ] Unit tests: zero-width, wide, narrow, and mixed strings; ASCII negative control measures
  identically to `len`

### Phase 3: Route every site through it

- [ ] `tui.VisibleWidth` delegates to `bus.DisplayWidth`
- [ ] `bus/measure.go` measurers use `bus.DisplayWidth`
- [ ] `TruncateAnsi` and `fitPath` cut on cell budget and never split a wide rune
- [ ] `Pad`, `padVisible`, `wrapPlain` verified to inherit (Phase 1's list)
- [ ] Negative control: the existing golden/`--render-once` frames are byte-identical for ASCII
  content

### Phase 4: Integration test

- [ ] Create `scripts/test-display-width.sh` — `--render-once` fixture frames through the real
  binary, no live session needed
- [ ] Test: a launcher frame whose project path contains CJK renders every line within the declared
  width (`StripAnsi` + cell count per line ≤ width)
- [ ] Test: a popup measured over CJK content is sized to its cell width, not its rune count
- [ ] Test: an emoji-bearing spec title in the graph launch frame stays within width
- [ ] **Negative control:** the same frames with ASCII content are byte-identical to today's output
- [ ] Coverage floor so a skipped section cannot report green
- [ ] Run the script and verify all checks pass
- [ ] Add the cell-width rule to `docs/tui-style.md`

## Open decisions

- **Ambiguous-width characters** (East Asian Width `A` — box drawing, some Greek/Cyrillic, `…`):
  terminals disagree on 1 vs 2 cells. Treating them as 1 matches most Western terminal defaults and
  the project's existing frames, which already use `─` and `…` in width math. Recommend 1; not
  chosen here.
- **Where the table lives** if [`MUX-107`](./MUX-107-tui-component-kit.md) lands first: in the kit.
  Otherwise `bus`, per the dependency direction above.

## Out of scope

- Grapheme clustering (a flag emoji is two runes rendering as one 2-cell glyph; a ZWJ family is
  several). Cell-summing over runes gets these slightly wrong in the *over*-estimating direction,
  which fits rather than overflows. Acceptable for a first pass; record if it proves otherwise.
- Terminal capability detection. The table is static.

## Status

Draft — filed 2026-09-08 from the review finding on the banner restyle, via edit's handoff
(`/tmp/muxcode-display-width-defect.md`). No implementation has started; `fitPath` itself is still
uncommitted on the branch that surfaced it.
