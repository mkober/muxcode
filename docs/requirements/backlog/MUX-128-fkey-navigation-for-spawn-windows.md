# F11 and F12 Navigate to Spawned Worker Windows

`config/tmux.conf` binds F1–F10 to the ten agent windows and nothing beyond. The default layout
consumes every one of them: agents at `window_index` 1–10, plus the `research` hold window at index
0. **Every spawned worker lands at index 11 or higher and is unreachable by any F-key** — structural,
not situational. Today the only routes are `prefix + w` or `prefix + '` then the index.

This spec binds **F11 to the first spawn window and F12 to the second**.

Tracking: _(no GitHub issue yet)_

## Context

### Why an index binding is the wrong shape

The obvious implementation — `bind -n F11 select-window -t:11` — is wrong for the same reason
[MUX-117](../completed/MUX-117-pane-targeting-by-identity.md) exists. Spawn windows are **dynamic**: they are
created and destroyed as work is delegated, there is no cap on how many exist, and the index a given
worker occupies depends on how many preceded it. Within one session today the spawn window has
already been `spawn-a279185a` and then `spawn-ea720538`, both at index 11.

A binding pinned to `11` therefore selects whatever happens to sit at that index — a different worker
than intended, or nothing at all. **Resolve by identity (the `spawn-` name prefix), then select.**
That is MUX-117's thesis applied to window addressing rather than pane addressing.

### The `F11` risk is environmental, not a code problem

**F11 is bound to fullscreen in iTerm2, Terminal.app, and most terminal emulators**, which intercept
it before tmux sees it. A correct binding can still be completely inert. This must be **verified
empirically in the user's terminal** before the work is called done — no implementation detail works
around a key that never arrives.

F12 is conflicted far less often. If F11 proves unreachable, the fallback is F12 for the first worker
and a chord or `prefix`-prefixed key for the second, rather than shipping a binding that does
nothing.

### Open decision: do slots follow the worker, or the position?

With two workers alive, F11 → worker A and F12 → worker B. **When worker A exits, does worker B
become F11?**

| Semantics | Behaviour | Cost |
|-----------|-----------|------|
| **Positional** (recommended) | F11 = lowest-indexed live spawn, F12 = next | Target shifts under the user when an earlier worker exits |
| **Pinned slots** | A worker keeps its key from creation until it dies | Needs persisted slot state; a key can be dead while a worker is live |

Positional is recommended: it needs no state, always reaches *a* live worker, and matches how
`prefix + w` already presents windows. The shift is worth calling out in the footer/help text rather
than engineering away.

## Requirements

### Acceptance criteria

- [ ] F11 selects the first spawn window (lowest `window_index` among `spawn-*`) when one exists
- [ ] F12 selects the second spawn window when one exists
- [ ] Both are **no-ops when the slot is empty** — no error, no popup, no status-line noise
- [x] Selection resolves by the `spawn-` name prefix, **never** by a hardcoded index — *`NthSpawnWindowIndex` prefix-matches and sorts by index; no literal 11/12 in either the resolver or `WindowFKey`*
- [x] **Negative control:** with two spawn windows at non-contiguous indices, F11/F12 still reach the
      correct two — a fix that hardcodes `11`/`12` cannot pass — *`TestNthSpawnWindowIndex` uses
      indices 11/14/17 listed out of order, so it fails both a hardcoded index **and** an
      implementation ordering by listing position*
- [x] Behaviour is correct when a spawn window exists at an index other than 11 (e.g. after a window
      is closed and indices shift) — *covered by the same 11/14/17 case; slot 2 → 14, slot 3 → 17*
- [ ] F11 is confirmed to actually reach tmux in the user's terminal, or the key choice is revised
      and this criterion updated to name the key actually used
- [x] The `# --- F-key window switching (F1-F10) ---` header comment is updated — it is stale the
      moment this lands — *now `(F1-F12)`, with the terminal-fullscreen caveat noted inline*
- [ ] More than two spawn windows: the first two are reachable, the rest documented as `prefix + w`
- [ ] `scripts/test-spawn-fkeys.sh` passes

### Technical approach

A `run-shell` binding that lists windows, filters to the `spawn-` prefix, sorts by index, and selects
the nth — failing silently when absent:

```
bind -n F11 run-shell "muxcode spawn select 1 --session '#{session_name}' >/dev/null 2>&1 || true"
bind -n F12 run-shell "muxcode spawn select 2 --session '#{session_name}' >/dev/null 2>&1 || true"
```

Putting the resolution behind a **muxcode subcommand rather than an inline shell pipeline** is
preferred: it is testable in Go, keeps `tmux.conf` readable, and gives one place to define ordering
and the empty-slot no-op. An inline `list-windows | grep | cut` chain works but is untestable and
duplicates ordering logic in a config file.

*(As implemented the subcommand is `spawn select <n> [--session <s>]`, not the
`window select-spawn <n>` this spec first sketched — it belongs with the other spawn subcommands.)*

### Decision: the binding must pass `--session` explicitly

**`run-shell` does not carry `BUS_SESSION`.** Without an explicit session the subcommand falls back
to the environment, and that fallback can resolve a spawn window **in a different session** — the key
would select a real window, in the wrong place, reporting success. So the bindings pass
`--session '#{session_name}'`, taking the session from tmux itself at the moment the key is pressed.

Two reasons this is recorded rather than left as a code detail:

- It is the same failure MUX-117 exists to eliminate, one level up: *addressing that resolves to a
  plausible wrong target instead of failing.* Pane index → wrong pane; ambient session → wrong
  session.
- The bindings end in `>/dev/null 2>&1 || true` so an empty slot is a clean no-op. That guard would
  equally have swallowed a session-resolution failure, leaving F11/F12 silently inert with nothing
  in any log. The `|| true` is correct for the empty-slot case and is precisely why the session must
  be passed rather than discovered.

Pinned by `TestNthSpawnWindowIndex_QueriesGivenSession` (added from a review should-fix): it captures
the arguments handed to tmux and asserts the census carries `-t other-session`, so a regression to an
ambient lookup fails rather than silently querying the wrong session.

### Key files

| File | Purpose |
|------|---------|
| `config/tmux.conf` | The F11/F12 bindings and the stale header comment |
| `tools/muxcode/cmd/spawn.go` | `spawn select <n> [--session <s>]` subcommand |
| `tools/muxcode/bus/spawn.go` | `NthSpawnWindowIndex()` — prefix match, index-ordered |
| `tools/muxcode/bus/provider_options.go` | `WindowFKey` returns `""` for index > 10 — revisit if spawn windows should now report F11/F12 |

Note the last row: `WindowFKey` was just corrected (2026-08-31) to return `""` outside 1–10. If spawn
windows gain F-keys, that bound becomes wrong in the other direction, and its tests
(`TestWindowFKey_ByIndexNotPosition` asserts `spawn-ab12cd34` → `""`) will need updating **together
with** this change, not after.

## Implementation

### Phase 1: Resolver

- [x] Implement spawn-window resolution by `spawn-` prefix, ordered by `window_index` ascending —
      *`NthSpawnWindowIndex` (`bus/spawn.go:229`)*
- [x] Empty slot returns cleanly with no output and no error — *returns `(0, false)`; the bindings
      wrap it in `>/dev/null 2>&1 || true`*
- [x] Unit tests: zero spawns, one spawn, two spawns, three spawns, non-contiguous indices —
      *`TestNthSpawnWindowIndex` covers zero (slot 1 empty), one (slot 1 filled, slot 2 empty), three
      at non-contiguous indices, and `n < 1`. **The three-spawn case is the discriminating one**: the
      census lists them out of order (`14`, `11`, `17`) so it fails both a hardcoded `11`/`12` and an
      implementation that orders by listing position rather than index. Verified green, not asserted:
      `./test.sh` → **2182 PASS / 0 FAIL / 1 SKIP**, exit 0, with this test named*

### Phase 2: Bindings

- [x] Bind F11 and F12 in `config/tmux.conf` — *`bind -n F11 run-shell "muxcode spawn select 1
      >/dev/null 2>&1 || true"`, F12 selects slot 2*
- [x] Update the stale `(F1-F10)` header comment — *now `(F1-F12)`, and carries the fullscreen
      caveat inline*
- [ ] Verify F11 actually arrives in the user's terminal; revise the key choice if it does not —
      **outstanding, and only the user can close it.** Requires `tmux source-file` on the updated
      config, then pressing F11 with a spawn window live. If the terminal claims the key for
      fullscreen, everything above is correct and inert, and the fallback (F12 + a chord) applies

### Phase 3: Label consistency

- [x] Decide whether `WindowFKey` should report `F11`/`F12` for spawn windows — *yes; it now derives
      slots from the same ascending-index order as `NthSpawnWindowIndex`, so labels and bindings
      agree by construction rather than by coincidence*
- [x] If yes, update it and `TestWindowFKey_ByIndexNotPosition` in the same change — *done in one
      change, as required. The test stubs spawns out of order (14, 11, 17) and asserts
      `spawn-aaa11111`→`F11`, `spawn-bbb22222`→`F12` ("second slot despite later listing"),
      `spawn-ccc33333`→`""`. Verified green: `./test.sh` → **2183 PASS / 0 FAIL / 1 SKIP***

### Phase 4: Integration test

- [ ] Create `scripts/test-spawn-fkeys.sh` — hermetic: isolated tmux socket, scratch session
- [ ] With no spawn window, F11 and F12 are no-ops and leave the current window unchanged
- [ ] With one spawn window, F11 selects it and F12 is a no-op
- [ ] With two spawn windows, F11 and F12 select the correct one each
- [ ] **Negative control:** two spawn windows at non-contiguous indices (e.g. 11 and 14) — a
      hardcoded `-t:11`/`-t:12` implementation fails here
- [ ] Coverage floor so a skipped run cannot report green
- [ ] Run the script and confirm all checks pass

## Risks

| Risk | Why it matters | Mitigation |
|------|----------------|------------|
| Terminal intercepts F11 | The whole feature is inert and looks like a code bug | Empirical check is an acceptance criterion, not an afterthought |
| Hardcoded index implementation | Reaches the wrong worker, silently | Non-contiguous-index negative control in Phase 4 |
| `WindowFKey` bound left stale | UI labels disagree with the bindings — exactly the defect just fixed | Phase 3 pairs the change with its test update |
| Positional slots surprise the user | F11's target moves when an earlier worker exits | Documented; revisit only if it proves annoying in practice |
| `\|\| true` masks a real failure | The empty-slot no-op guard also swallows session-resolution or binary-missing errors, leaving the keys silently inert | Session is passed explicitly rather than discovered; anything else that can fail here needs a louder path than the binding provides |

## Status

Backlog
