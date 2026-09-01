# TUI Style Guide

Conventions for muxcode's terminal interfaces — the dashboard, the remote session browser, the graph surfaces, and every modal. The palette and vocabulary sections are house style. The **structural rules** are not: each one exists because its absence caused a real defect, and each defect is cited.

Source of truth for the code: `tools/muxcode/tui/{styles,graph,graph_ui,remote,model}.go` and `tools/muxcode/bus/{popup,measure}.go`.

## Palette

All colors live in `tui/styles.go` as 256-color escapes. **Never write an escape inline** — import the constant, so a palette change is one edit and `StripAnsi` stays able to strip everything.

| Constant | Role |
|----------|------|
| `FG` | Default body text |
| `Comment` | Secondary text, separators, inactive items, empty-state copy |
| `Purple` | Active/selected emphasis, headings |
| `Cyan` | In-progress, running |
| `Green` | Success, done |
| `Red` | Failure, destructive warning |
| `Yellow` | Attention, waiting, **key hints** |
| `Orange`, `Pink` | Reserved accents — use only when an existing role genuinely does not fit |
| `Bold`, `Dim` | Weight modifiers, always combined with a color |
| `RST` | Reset — every colored span closes with it |

### Color carries state, glyphs carry identity

Color is the *fast* channel and glyphs are the *precise* one. A reader scanning for trouble finds red; a reader identifying a specific state reads the glyph. Both must be present, because color alone fails for the colorblind and in `StripAnsi` output.

The graph node vocabulary, from `nodeGlyph()`:

| State | Glyph | Color |
|-------|-------|-------|
| done | `✓` | Green |
| failed | `✗` | Red |
| running | `●` | Cyan |
| waiting | `◐` | Yellow |
| ready | `◆` | Purple |
| skipped | `○` | Comment |
| pending | `·` | Comment |
| `wait_human` gate | `⚑` | Yellow + Bold when waiting |
| `condition` took its false branch | `◇` | Comment |

Note `skipped` and `pending` share `Comment` — both mean "not active", and the **glyph** is what distinguishes them. That is the rule working as intended, not an oversight.

**The branch-taken form is not a failure state.** A `condition` node that evaluates correctly and takes its false edge is persisted as `failed` with `outcome=failure`, because that outcome is the routing key the false edge matches ([MUX-133](requirements/drafts/MUX-133-condition-false-branch-renders-as-failure.md)). Rendering it red made ordinary control flow look identical to a broken node. Every surface therefore branches on `bus.ConditionTookBranch(nodeType, state)` — one shared predicate, so the surfaces cannot drift:

| Surface | Form |
|---------|------|
| TUI DAG / run-list | `◇` in Comment, and excluded from the failed cell |
| `graph status` | the word `branched` in dim, in place of `failed` |
| `graph status --json` | `"branched": true` on the node status |

Red `✗` stays reserved for genuine dispatch or execution errors — including a condition whose *evaluation* actually errors.

**Watch the fill.** `◆` (ready, Purple) and `◇` (branched, Comment) differ only by fill once color is stripped. That is thinner than the rest of this vocabulary and is worth revisiting if a third diamond is ever needed.

Other glyphs in use: `↺ ×N` (capped loop edge), `⇥` (Tab affordance), `─ │ ┌ ┐ └ ┘` (box drawing), `→ ←` (direction).

## Layout anatomy

Every full-screen surface renders in the same four bands, top to bottom:

```
  Prompt / Launch Graph / Graph Runs / Pending Gates  ⇥ Tab: next     ← tab bar (multi-surface only)
  Run abc123  req-code-pr  running  4/9 done  2m14s                     ← header: what am I looking at
  ────────────────────────────────────────────────────────────────
  ● build      ✓ test       ⚑ await-review                            ← body
  · deploy
  ────────────────────────────────────────────────────────────────
  ↑↓/jk Navigate  Enter Open  R Refresh  q Quit                       ← footer: what can I do
```

- **Tab bar** — only when a surface belongs to a cycle. Active entry in `Purple+Bold`, others `Comment`, affordance appended in `Comment`. Drill-in views keep the bar and highlight their *parent* surface, so it never flickers.
- **Header** — identity and state. Two-space left margin, matching the body.
- **Body** — the content. Selection marked with a cursor, never by color alone.
- **Footer** — `%sKEY%s Label` pairs, key in `Yellow`, two spaces between pairs, two-space indent. The footer is the only discoverability surface most users will read.

### Keys

| Key | Meaning | Notes |
|-----|---------|-------|
| `j`/`k`, `↑`/`↓` | Move selection | Both always; footers advertise `↑↓/jk` |
| `Enter` | Descend | |
| `q`, `Esc` | Back; quit from top level | |
| `R` | Force refresh | Capital — lowercase `r` is retry |
| `Tab` / `Shift-Tab` | Cycle surfaces | Inert in drill-ins and confirms |
| `y` / `n` | Confirm / cancel | Confirm views swallow all other keys |

A key that mutates state is **always** confirm-gated. A key means the same thing in every surface, or it gets a different key.

### Where a surface lives: the control pane

Full-screen surfaces render inside the **control pane** — a permanent full-width pane at the bottom
of every agent window ([`MUX-108`](requirements/completed/MUX-108-control-pane.md)), created *after*
panes 0 and 1 so `AgentPane()`'s hardcoded `"1"` delivery contract holds.

Two consequences for anything rendered there:

- **The height is small and fixed** (14 rows by default). A surface that needs more must degrade,
  not scroll off — this is rule 2 with teeth, and it is why the graph DAG shows its flat-list
  fallback in the pane far more often than in a full-screen popup.
- **The pane is ambient, not summoned.** It is on screen whether or not anyone asked, so a surface
  must be readable at a glance and must not demand attention it has not earned. The graph popups
  it replaced could afford to be busy because you opened them deliberately; a permanent pane
  cannot.

Surface selection is shared across panes through a `control-pane-surface` file, so switching in one
converges the rest — **one-way and non-destructive**: a pane drilled into a DAG or a half-entered
prompt is never yanked out by another pane's switch. That asymmetry is the whole design: converge
the idle, never interrupt the engaged.

## Structural rules

These are load-bearing. Each cites the incident that produced it.

### 1. Renderers are pure; `--render-once` is the seam

Layout and frame generation take a state snapshot and return a string. No I/O, no terminal, no globals. Every surface exposes a `--render-once` path that prints one frame and exits.

**Why:** this is what makes a TUI testable at all. `scripts/test-graph-tui.sh` asserts 46 checks against real frames with no terminal, no session, and no daemon — including gate flagging and fallback ordering. Without the seam those behaviors are verifiable only by a human squinting at a pane.

### 2. Clamp to the pane; fall back rather than overflow

A renderer receives width **and height** and must honor both. Content that cannot fit degrades to a simpler representation — never runs off the edge.

**Why (MUX-031):** `RenderGraphFrame` accepted `height` and used it *nowhere* — the word appeared exactly once in the file, in the signature. Wide graphs fell back correctly; deep graphs rendered straight past the bottom of the pane. **All sixteen tests passed**, because every one of them passed `height=40`, a value no fixture could overflow. A green suite is not coverage when the parameter is never exercised.

The fix: `gridW > width || gridH+skipLanes+headerLines > height` → flat list.

### 3. Test the negative case, or the test proves nothing

Every degradation assertion needs its opposite. "Narrow pane falls back" must be paired with "wide pane renders the grid".

**Why (MUX-031, MUX-014):** a fallback that *always* fires passes the positive assertion. A gate queue that flags *everything* passes "commit-downstream gate is flagged". The discriminators are `wide pane renders the grid (negative control)` and `benign gate is not flagged`. In MUX-014, two assertions passed because the code path was never reached at all — the pass count was 15/22 and the failure was invisible in the count.

### 4. Measurers read content, never run the command

A `ContentMeasurer` reports the widest state content can reach, side-effect free. Popups cannot be resized after opening, so it sizes for the worst case, not the current view.

**Why (MUX-031):** when six popups were retired, three measurers were orphaned and removed — but `MeasureMemoryContext` was **kept**, because `save-memory` still uses it. The spec said "remove four" and was wrong. Delete a measurer only after enumerating its callers.

### 5. Empty states are explicit and carry the affordance

Never render a blank body. Say what is absent, in `Comment`: `No gates waiting`, `No graph runs`, `No graph templates`. Keep the header and footer.

**Why:** a blank frame is indistinguishable from a broken renderer. `TestRenderGateQueueFrame_EmptyState` pins this. The gate queue's empty state is the one a user sees most often — it must read as "nothing is waiting on you", not as a failure to load.

### 6. Restore selection by identity, not index

When a list is re-read, restore the cursor by matching the previously selected item's **id**. Default to the first row; a missing item is the ordinary case, not an error branch.

**Why (MUX-105):** `Tab` cycling re-reads the store on entry. Rows shift and vanish between reads, so a stored index points at a different item — or off the end.

### 7. Titles and labels must survive state changes

A tmux popup title is fixed once opened. If a surface can change what it shows, the title must be generic and the *frame* must name the surface.

**Why (MUX-105):** with `Tab` cycling, a popup opened as `Graph Runs` could be displaying the gate queue. The graph popups were all retitled ` Graph `, leaving the tab bar to name the live surface. Those popups have since been retired for the control pane ([MUX-108](requirements/completed/MUX-108-control-pane.md)), which is why the rule now reads on the pane's own tab bar — and it carries more weight there, since one pane cycles four surfaces and is switched by a gate arriving rather than by the user.

### 8. Mutating actions confirm, and re-check at execution

A confirm prompt states what will happen, including downstream consequences. Between rendering the prompt and the keypress, the world can change — **re-read state before acting**.

**Why (MUX-031):** approving a `wait_human` gate releases downstream work that may include a commit or Atlassian write, so the prompt flags that explicitly. And `executeAction` re-reads node status at keypress, refusing if the gate left `waiting` — a stale confirm frame must never approve something other than what it described.

### 9. Escape sequences are ambiguous — disambiguate deliberately

`Esc`, arrow keys, and `Shift-Tab` all begin with byte 27. A handler that treats 27 as "back" will make arrows and `Shift-Tab` exit the view.

**Why (MUX-105):** `Shift-Tab` is `ESC [ Z`. `handleEscapeSequence()` peeks the following bytes and falls back to bare-Escape on timeout, which is also what lets unit tests (no key channel) treat `27` as Escape.

## Checklist

Before merging a TUI change:

- [ ] No inline escapes — colors come from `styles.go`
- [ ] State readable without color (glyph or text)
- [ ] Renderer is pure; reachable via `--render-once`
- [ ] Both `width` **and** `height` honored, with a fallback
- [ ] Degradation tests include the negative control
- [ ] Empty state is explicit, with header and footer intact
- [ ] Selection restored by id, first-row fallback
- [ ] Title accurate for every state the surface can reach
- [ ] Mutating keys confirm-gated **and** re-check state at execution
- [ ] Footer advertises every key the surface accepts

## See also

- [Architecture](architecture.md) — modal and popup dispatch
- [Agent Bus CLI](agent-bus.md#muxcode-graph) — the graph TUI surfaces and keys
- [`MUX-031`](requirements/completed/MUX-031-graph-run-tui.md), [`MUX-105`](requirements/completed/MUX-105-force-respond-escalation.md) — the specs these rules were extracted from
