# Status-Bar F-Key Label Diverges From the Actual Binding

The tmux status bar labels every window `F#I` — the **raw window index**. The F11/F12 spawn bindings
resolve **positionally** (first and second spawn window, by ordinal). When those disagree, the status
bar names a key that does not select that window, and the key it names silently does nothing.

Tracking: _(no GitHub issue yet)_

## Context

### Observed 2026-09-01 (user screenshot)

The research window occupies index 11 and the session's only spawn lands at index 12:

| | Renders / does |
|---|---|
| Status-bar label for the spawn window | **`F12`** (raw index) |
| `F12` binding | `muxcode spawn select 2` → there is no second spawn → **silent no-op** |
| `F11` binding | `muxcode spawn select 1` → **actually selects it** |
| `WindowFKey` (Go) | returns **`F11`** — already correct |

So the label says one key, a different key works, and pressing the labelled key does nothing at all.

### Mechanism — verified end to end

**The binding is ordinal** (`config/tmux.conf:25-26`):

```
bind -n F11 run-shell "muxcode spawn select 1 --session '#{session_name}' >/dev/null 2>&1 || true"
bind -n F12 run-shell "muxcode spawn select 2 --session '#{session_name}' >/dev/null 2>&1 || true"
```

`spawn select N` takes the **Nth spawn**, not window index N.

**The label is the raw index**, hardcoded as the literal `F#I` in **two** places:

| Site | Line |
|---|---|
| `config/tmux.conf` | `:125` (inactive), `:127` (active) |
| `bus/launcher.go` `WindowStatusFormat()` | `:943` |

Neither consults `WindowFKey`.

**`WindowFKey` already computes the right answer** (`bus/provider_options.go:193`): indices 1–10 map to
`F{index}`; above 10 it sorts the spawn indices and returns `F{11+slot}` for the window's **ordinal
slot** among spawns. The correct label exists in Go and never reaches the status bar.

### Why it stays invisible

The bindings end in `>/dev/null 2>&1 || true`. A `spawn select` against an empty slot produces no
error, no message, no bell — the keypress is indistinguishable from a key that isn't bound. Nothing
tells the user the label was wrong; the window simply doesn't change.

### When it diverges

Any time a non-spawn window sits at index ≥ 11, or spawn indices are non-contiguous — which happens
whenever a spawn is cleaned up and a later one takes a higher index. Windows at indices 1–10 are
unaffected, because there `F{index}` and the positional key coincide.

### Why it matters

The status bar is the only affordance telling a user which key reaches a window. A label that is
confidently wrong is worse than no label: the user presses it, nothing happens, and the natural
inference is that spawn switching is broken rather than that the label lied. Same family as
[`MUX-124`](../backlog/MUX-124-lifecycle-since-truncated-by-limit.md) and
[`MUX-006`](../backlog/MUX-006-diagnose-false-clean-verdict.md) — *the instrument misreports its own subject*.

Related: [`MUX-128`](../backlog/MUX-128-fkey-navigation-for-spawn-windows.md) covers F-key navigation for spawn
windows generally; this is the label half specifically.

## Requirements

### Acceptance criteria

- [ ] The status-bar label for a window names the key that actually selects it, for every window
      including spawns at non-contiguous indices
- [ ] The label is derived from the **same** logic as the binding — one source of truth, not a second
      implementation that can drift from `WindowFKey`
- [ ] Both `F#I` sites are fixed together (`config/tmux.conf` **and** `bus/launcher.go`
      `WindowStatusFormat`); fixing one leaves the other authoritative in some launch paths
- [ ] A window with no valid F-key (e.g. a third spawn, beyond F12) renders an honest fallback rather
      than a key that does nothing
- [ ] **Negative control:** windows at indices 1–10 keep their existing labels unchanged — the fix must
      not churn the common case where index and key already coincide
- [ ] **Negative control:** a spawn cleaned up mid-session re-labels the remaining spawns correctly,
      rather than leaving a stale option value behind
- [ ] Pressing the labelled key selects the labelled window — verified by actually sending the key, not
      by reading the format string

### Key files

| File | Purpose |
|------|---------|
| `config/tmux.conf` | `F11`/`F12` bindings (:25-26); `window-status-format` literals (:125, :127) |
| `tools/muxcode/bus/launcher.go` | `WindowStatusFormat()` (:943) — the second `F#I` site |
| `tools/muxcode/bus/provider_options.go` | `WindowFKey()` (:193) — the correct logic, currently unused by the status bar |
| `tools/muxcode/bus/spawn.go` | Spawn create/cleanup — where a per-window option would be set and refreshed |

## Implementation

### Phase 1: Pin the divergence

- [ ] Reproduce: park a non-spawn window at index ≥ 11, start one spawn, assert the rendered label and
      the working key disagree
- [ ] Unit-pin `WindowFKey` for the divergent shape (non-contiguous spawn indices) so the intended
      answer is fixed before the renderer changes

### Phase 2: Carry the computed key to the status bar

- [x] Set a `@muxcode_fkey` window option from `WindowFKey` — `RefreshWindowFKeyLabels`
      (`provider_options.go:256`) is a **diff-only** sweep driven from `daemon.go:2618`, so it covers
      create, cleanup and re-index without a per-event hook
- [x] Render `#{@muxcode_fkey}` in **both** format sites — `config/tmux.conf:131,133` and
      `launcher.go:950` now use `#{?@muxcode_fkey,…}`. `F#I` survives only in explanatory comments;
      `launcher_test.go:488` asserts it is **absent** from the format, not merely that the conditional
      is present
- [x] Decide and record the render for a window with no valid key — the conditional's empty arm renders
      **nothing**, so a window with no valid F-key shows only its name rather than a dead key

### Phase 3: Keep it fresh

- [x] Refresh the option when spawn membership changes — the daemon sweep re-derives every window's
      label each pass and writes only differences, so cleanup re-labels the survivors
- [ ] Confirm no path leaves a stale `@muxcode_fkey` on a window whose slot changed

### Phase 4: Negative controls

- [ ] Indices 1–10 unchanged
- [ ] Spawn cleanup re-labels remaining spawns
- [ ] Beyond-F12 window renders the honest fallback
- [ ] Confirm each control fails when its fix is reverted

### Phase 5: Integration test

- [ ] Add to a `scripts/test-*.sh`: construct the divergent layout (non-spawn window at index ≥ 11 plus
      one spawn), assert the rendered label matches `WindowFKey`
- [ ] **Send the labelled key and assert the labelled window becomes active** — the format string
      matching is necessary but not sufficient; the reported symptom is a keypress doing nothing
- [ ] Assert the 1–10 case is untouched
- [ ] Coverage floor set to the achievable maximum so a skipped section cannot report green
- [ ] Run it and record passed/failed/exit code here

## Status

In Progress — moved to `drafts/` 2026-09-01. **Implementation landed the same night**: both format
sites now render `#{?@muxcode_fkey,…}` fed by a diff-only daemon sweep, pinned by tests that assert
`F#I` is absent rather than merely that the conditional is present.

Phases 4 (remaining negative controls) and 5 (integration test) are open. The integration criterion
that matters is **send the labelled key and assert the labelled window activates** — because the
bindings end `>/dev/null 2>&1 || true`, a format-string assertion alone would pass while the reported
symptom (a keypress doing nothing) survived untouched.
