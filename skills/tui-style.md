---
name: tui-style
description: TUI review checklist — pure renderers, clamp to pane, negative-control tests, explicit empty states, confirm-then-recheck
roles: [edit, review, plan]
tags: [tui, style, rendering, testing]
---

## TUI checklist

Full conventions: [`docs/tui-style.md`](../docs/tui-style.md). This is the pass you run on every TUI change. Each item exists because its absence shipped a defect.

> **The mechanical checks — no judgment involved.**
> 1. **`grep '\\033\['` in the diff.** Any inline escape outside `tui/styles.go` is a violation. Colors come from the constants.
> 2. **Does the renderer's signature take `height`? Then `grep` the body for it.** Accepting a dimension and never reading it is the MUX-031 defect verbatim.
> 3. **Every degradation test needs its opposite.** A test asserting "falls back" with no sibling asserting "does *not* fall back" proves nothing — a renderer that always falls back passes it.

### Rendering

- Colors from `styles.go` constants, never inline escapes
- State readable **without** color — glyph or text carries identity, color carries urgency
- Renderer is pure: snapshot in, string out, no I/O, reachable via `--render-once`
- Both `width` and `height` honored; oversized content degrades to a simpler form rather than overflowing

### Content

- Empty state is explicit and keeps header and footer — never a blank body
- Selection restored by item **id**, not row index, with first-row fallback
- Titles accurate for every state the surface can reach; if a surface can change what it shows, the title is generic and the frame names the surface
- Footer advertises every key the surface accepts

### Actions

- Mutating keys are confirm-gated
- The confirm states the consequence, including downstream effects worth flagging
- **Re-check state at execution** — between rendering the prompt and the keypress the world can change; a stale confirm must never act on what it no longer describes
- Escape sequences disambiguated: `Esc`, arrows, and `Shift-Tab` all start with byte 27

### Tests

- Negative control for every degradation and every flagging behavior
- Assertions exercise the parameter they claim to test — a fixture that never overflows cannot test overflow
- A **pass count is not coverage**: check that the discriminating assertion was reachable, not just that the suite is green

### When reviewing, ask

- Which of these did the change *not* need, and is that deliberate?
- Is there a green test here that would still pass if the feature were deleted?
- Would this frame be comprehensible piped through `StripAnsi`?
