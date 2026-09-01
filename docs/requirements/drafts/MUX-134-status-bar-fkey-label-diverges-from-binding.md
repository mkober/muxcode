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

- [x] Reproduce: park a non-spawn window at index ≥ 11, start one spawn, assert the rendered label and
      the working key disagree — `TestWindowFKey_RawIndexDivergence`
      (`provider_options_test.go:187`) stubs `research` at 11 with the sole spawn at 12 and asserts
      the raw-index answer `F12` is **not** returned, so the divergence itself stays pinned rather
      than only the corrected `F11`; the non-spawn at 11 must render empty, since a label there lies
- [x] Unit-pin `WindowFKey` for the divergent shape (non-contiguous spawn indices) so the intended
      answer is fixed before the renderer changes — `TestWindowFKey_ByIndexNotPosition`
      (`provider_options_test.go:159`) covers spawns at 14/11/17 listed out of order, pinning
      F11/F12/empty by ascending index, which is what catches a hardcoded 11/12 mapping

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
- [x] Confirm no path leaves a stale `@muxcode_fkey` on a window whose slot changed — two independent
      legs. **Sole writer:** the only `set-option … @muxcode_fkey` in the repo is
      `provider_options.go:280`, inside `RefreshWindowFKeyLabels`; every other occurrence is a read
      site (`launcher.go:950,959`, `tmux.conf:131,133`) or a comment, so no second path can strand a
      label. **Slot change, not just a lost key:**
      `TestRefreshWindowFKeyLabels_SlotShiftAfterCleanup` (`provider_options_test.go:246`) stubs a
      surviving spawn at index 12 still carrying a stale `F12` and asserts it is rewritten to `F11`,
      with `n == 1` and the already-correct `plan` window untouched — a sweep that blindly rewrote
      every window would fail that second assertion

### Phase 4: Negative controls

- [x] Indices 1–10 unchanged — `TestWindowFKey_ByIndexNotPosition` pins F1/F2/F5/F10 across the
      common range (its stub parks `build` at index 3 but asserts no key for it — **F3 is pinned by
      `TestWindowFKey_NoHoldWindow`**, not here), which also pins the identity mapping when no index-0
      hold window exists (catching a fix that merely subtracted one from position), and
      `TestRefreshWindowFKeyLabels_DiffsOnly` asserts the already-correct `plan` window at F1 is
      **never rewritten** — the no-churn half of "unchanged", which a value-only assertion misses
- [x] Spawn cleanup re-labels remaining spawns — `TestRefreshWindowFKeyLabels_SlotShiftAfterCleanup`
      (survivor shifts F12→F11) plus the `DiffsOnly` census, where `research` clears its stale F11 in
      the same pass that the spawn gains it
- [x] Beyond-F12 window renders the honest fallback — `DiffsOnly` stubs `13:F13:my:notes`, a window
      past the last binding **carrying a lying F13**, and asserts it is cleared to `""`; with the
      option empty the conditional's empty arm renders the name alone. `assertFKeyLabelFormat` also
      pins that the arms carry no `#[]` styles, which is what keeps tmux parsing the empty arm at all.
      **Boundary:** this is option-level and format-level evidence. The tmux *render* itself is not
      executed by any unit test — that is Phase 5's job, and the honest fallback is only fully proven
      there
- [x] Confirm each control fails when its fix is reverted — three-round mutation run in worktree
      `spawn-c413916e`: (A) raw-index labels → 4 pins fail including spawn@12 = F12 want F11; (B)
      diff-check dropped → only the no-churn assertions fail while the value assertions stay green;
      (C) `F#I` restored in both formats → both format tests fail. The mutations were restored and a
      full uncached run returned to green. **How this was verified:** the experiment is transient and
      unreproducible from the repo, so each leg was checked *structurally* against the assertions that
      would catch it — `RawIndexDivergence` errors on `got == "F12"` (A); `DiffsOnly` carries exactly
      two no-churn assertions plus an `n != 4` count check, separate from its value map (B); and
      `assertFKeyLabelFormat`, shared by both format tests, errors on `F#I` being present (C). Result
      (B) is the discriminating one: predicting that the value assertions survive while only the
      no-churn ones fire matches the test's structure precisely

### Phase 5: Integration test

- [ ] Add to a `scripts/test-*.sh`: construct the divergent layout (non-spawn window at index ≥ 11 plus
      one spawn), assert the rendered label matches `WindowFKey`
- [ ] **Send the labelled key and assert the labelled window becomes active** — the format string
      matching is necessary but not sufficient; the reported symptom is a keypress doing nothing
- [ ] Assert the 1–10 case is untouched
- [ ] Coverage floor set to the achievable maximum so a skipped section cannot report green
- [ ] Run it and record passed/failed/exit code here

## Time Tracking

| Branch | Active time | Last updated |
|--------|-------------|--------------|
| MUX-132-graph-retry-launders-gate-approval | 8h 31m | 2026-09-01 16:57 |

## Status

In Progress — moved to `drafts/` 2026-09-01. **Implementation landed the same night**: both format
sites now render `#{?@muxcode_fkey,…}` fed by a diff-only daemon sweep, pinned by tests that assert
`F#I` is absent rather than merely that the conditional is present.

| Phase | | Evidence |
|---|---|---|
| 1 · Pin the divergence | **2/2** | `TestWindowFKey_RawIndexDivergence` (new) + `TestWindowFKey_ByIndexNotPosition` (existing) |
| 2 · Carry the key to the status bar | **3/3** | Both `F#I` sites replaced; absence asserted, not just presence of the conditional |
| 3 · Keep it fresh | **2/2** | Sole writer proven by repo-wide grep; slot-shift F12→F11 pinned with a diff-only negative control |
| 4 · Negative controls | **4/4** | Controls pinned by existing tests; revert-confirmation verified structurally against the catching assertions |
| 5 · Integration test | 0/5 | |

Phase 1 was closed 2026-09-01 against a `-count=1` run of `./bus/` **in the main checkout** — 1825
passed / 0 failed, `--- PASS: TestWindowFKey_RawIndexDivergence`. The work reached the branch as an
uncommitted harvest from spawn worktree `spawn-c413916e`; the repo copy of `provider_options_test.go`
was confirmed byte-identical to the worktree's before anything was ticked, because a checkbox here
describes the repo, never a worktree. An earlier `2243 pass` figure for the same work is **not** the
basis for this close: it was a *cached* run, and the uncached one is the only evidence that binds.

**No acceptance criterion is ticked yet, deliberately.** Every criterion describes rendered or pressed
behaviour, while Phases 1–2 so far prove the *derivation*. The criterion that matters — **send the
labelled key and assert the labelled window activates** — is Phase 5 work: because the bindings end
`>/dev/null 2>&1 || true`, a format-string assertion alone would pass while the reported symptom (a
keypress doing nothing) survived untouched.

Phase 3 was closed the same day on the same standard: `go vet` clean and an uncached `-count=1 ./bus/`
**in the main checkout** — 1826 passed / 0 failed, `--- PASS:
TestRefreshWindowFKeyLabels_SlotShiftAfterCleanup`. The count moved 1825 → 1826, exactly the +1 the
one new test should produce; that delta is the check that catches a test which silently never runs, so
it is recorded rather than the bare "0 failed". The spawn's own `2073 pass` figure ran inside
worktree `spawn-c413916e` and is again not the basis for the close.

Phase 4 added **no code and no tests** — `git status tools/` was empty and all four relevant files were
byte-identical to the worktree's, so the phase is confirmation only and the 1826-pass run above still
covers this exact tree (Phase 3 landed as `aefcd05`). No fresh run was requested for it, deliberately:
re-running an unchanged tree would have produced a green number that proved nothing new.

The revert-confirmation deserves its caveat. Mutation testing is transient by nature — the mutations
existed only inside worktree `spawn-c413916e` and cannot be inspected from the repo. Rather than accept
a reported pass/fail count, each of the three legs was checked against the assertion that would catch
it, so what is recorded is the *mechanism*, not a tally.

Phase 4's ticks were **re-verified independently** rather than accepted. The graph run that requested
this update (`req-code-pr-9c76e908`) never delivered its `update-spec` dispatch — that node failed
`undeliverable: plan never received the dispatch after 3 redrives` — yet the ticks were already on disk
when plan restarted, so their authorship could not be assumed. Each was rechecked against the repo, not
the prose: all six named tests exist, `git status tools/` is clean at `aefcd05`, and the structural
claims hold (`RawIndexDivergence` errors on `got == "F12"`; `DiffsOnly` carries exactly two no-churn
assertions plus an `n != 4` count check; `assertFKeyLabelFormat` is shared by both format tests and
errors on `F#I`). One attribution was wrong and is corrected above — `ByIndexNotPosition` does **not**
pin F3; `TestWindowFKey_NoHoldWindow` does. **Phase 4 is not committed**: its commit gate never fired,
because the run died at the undelivered node.

Phase 5 stays **0/5** deliberately. A `verify-spec` pass at 17:26 (run `req-code-pr-9c76e908`, retried
`--from update-spec`) asked for Phase 5 to be closed; the retry skips the `implement` node, so no Phase 5
work was ever produced, and the repo confirms it — no `scripts/test-*.sh` mentions `@muxcode_fkey`,
`WindowFKey` or the F-key label, and the working tree holds no new files. Nothing was ticked. The
behavioural criterion (**send the labelled key, assert the labelled window activates**) remains the one
that actually retires this defect, and it is still unwritten.

Open: Phase 5 (5), plus all 7 acceptance criteria.
