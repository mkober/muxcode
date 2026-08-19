# Modal Auto-Size

## Context

### Problem

Every muxcode popup is sized as a percentage of the tmux client. On a wide
terminal that produces modals several times wider than their content. Measured
live on a **317-column** client:

| Modal | Current size | Actual cols | Content needs |
|-------|--------------|-------------|---------------|
| Session picker (`prefix + C`) | `-w 60%` | 190 | ~63 (longest project path 55 + chrome) |
| Agent Status | `-w 70%` | 222 | ~74 (table width) |
| Sessions / Memory / Edit Config | `-w 80%` | 254 | varies |

Reported by the user against the session picker; it affects "most modal windows
in muxcode".

### Hard constraint (verified from `man tmux`)

`display-popup` has **no** content-fit option. `-w`/`-h` accept an absolute
cell count or a percentage; if omitted, half the terminal is used. A popup also
cannot be resized once open. Therefore any fitting must be computed by muxcode
*before* it invokes `display-popup`, and the size is final at open time. tmux
accepts absolute column counts — that is the mechanism this feature uses.

### Two sizing systems exist today

| System | Where | Coverage |
|--------|-------|----------|
| Go modal registry | `tools/muxcode/bus/modal.go` — `ModalConfig` (`Width`, `Height`, `Sizes` presets), `ResolveSize()` 4-tier precedence, `BuildPopupArgs()` (pure, unit-tested in `bus/modal_test.go`) | 2 modals (`api`, `provider`) |
| Static tmux.conf binds | `config/tmux.conf` — 12 `popup -w N% -h N%` invocations via the `popup` command-alias (line 32) | The majority, including the session picker (lines 40 and 45). Sizes in use: 4× `70%x50%`, 3× `60%x50%`, 2× `80%x80%`, 2× `80%x70%`, 1× `70%x60%` |

The static binds are the ones the user is hitting — they never touch
`ResolveSize()` today.

### Chosen approach — hybrid fit + cap

Decided with the user. Two tiers:

- **Measured fit** — for modals whose content muxcode itself generates and can
  measure cheaply without side effects: session picker (project list), agent
  status, proc list, cron list, sessions/remote browser, memory context. Size
  from the longest rendered line plus chrome.
- **Clamped cap** — for modals running arbitrary or interactive commands whose
  width is unknowable ahead of time: `nvim` (Edit Config), agent history,
  spawn agent, external scripts. These keep their percentage but are clamped
  to an absolute maximum column count.

**Explicitly rejected**: probing arbitrary commands by running them to sample
output. It can block, prompt, or cause side effects before the popup appears.
A cap-tier modal must never execute its command to measure it.

## Requirements

### Acceptance criteria

- [x] Session picker on a 317-col client opens at content width plus chrome (~65 cols), not 190 — measured live at 60 cols
- [x] All 12 static `popup -w N% -h N%` binds in `config/tmux.conf` route through the new `muxcode popup <name>` subcommand
- [x] Explicit `--size WxH` flag and named presets still override auto-fit (precedence tiers 1–2 unchanged)
- [x] `MUXCODE_MODAL_SIZE_<NAME>` still overrides auto-fit (precedence tier 3 unchanged)
- [x] `FitSize()` never returns a width or height larger than the client
- [x] Popup width is never smaller than its `-T` title width + 2, unless the client itself is smaller
- [x] `MUXCODE_MODAL_MIN_COLS` / `MUXCODE_MODAL_MAX_COLS` retune the clamps without a rebuild
- [x] Cap-tier modals never execute their command to measure content
- [x] Unresolvable client size falls back to the existing percentage defaults (current behavior)
- [x] All existing `bus/modal_test.go` tests pass unchanged (`TestResolveSize_*`, `TestBuildPopupArgs_*`, `TestBuildModalCommand_*`)
- [x] tmux >= 3.3 popup styling (`PopupStyleArgs`, `config/tmux.conf` line 31) is not regressed

### Sizing precedence

Auto-fit **replaces the config-default tier**; it must never outrank the
user's explicit choice. `ResolveSize()` becomes a 5-tier chain:

| Tier | Source | Status |
|------|--------|--------|
| 1 | CLI `--size WxH` (explicit dimensions) | Unchanged |
| 2 | CLI `--size <preset>` (named preset from `Sizes` map) | Unchanged |
| 3 | `MUXCODE_MODAL_SIZE_<NAME>` env var (WxH) | Unchanged |
| 4 | **Auto-fit** — measured fit or clamped cap, per the modal's tier | New |
| 5 | Config default (`Width`, `Height`) | Fallback when auto-fit is unavailable (no measurer, unresolvable client) |

### Chrome accounting

Exact allowances, defined as named constants in `bus/modal.go`:

| Constant | Value | Rationale |
|----------|-------|-----------|
| `PopupChromeCols` | 2 | Rounded border, one cell each side |
| `PopupChromeRows` | 2 | Rounded border, one row top and bottom |
| Title floor | visible title width + 2 | The `-T` title (e.g. `" New Session "`) renders in the top border between the corner cells; width must be >= title + 2 corners |

Fitted width = `contentCols + PopupChromeCols`, then raised to the title floor,
then clamped (below). The title floor loses only to the client cap — a client
narrower than the title still bounds the popup at client width.

### Clamp constants and overrides

| Clamp | Default | Override | Applies to |
|-------|---------|----------|------------|
| Minimum columns | 40 | `MUXCODE_MODAL_MIN_COLS` | Both tiers |
| Maximum columns | 160 | `MUXCODE_MODAL_MAX_COLS` | Both tiers (fit result and cap-tier percentage alike) |
| Max percent of client | 90 (fixed constant) | none — retune via the env clamps | Both tiers, width and height |
| Minimum rows | 10 (fixed constant) | none | Both tiers |

Env values are parsed as positive integers; invalid or non-positive values
fall back to the defaults. Row clamps are deliberately not env-tunable in this
iteration — height overshoot is far less visible than width overshoot.

### Degenerate clients

- [x] Content wider than the client: result clamps to client width (never exceeds it); the min-cols floor and title floor both lose to the client cap
- [x] Client size cannot be resolved (no attached client, tmux query fails): auto-fit tier is skipped and tier 5 (percentage defaults) applies — identical to today's behavior
- [x] Zero or negative measured content (empty list, measurer error): treat as unavailable — fall through to tier 5

### Technical approach

1. **Pure sizing helper** in `bus/modal.go`:

   ```go
   // FitSize clamps a measured content size to the client, floors, and caps.
   // Returns (0, 0) when clientW or clientH is unresolvable (<= 0) — the
   // caller falls back to percentage defaults.
   func FitSize(contentW, contentH, clientW, clientH int) (w, h int)
   ```

   Unit-testable with no tmux dependency, alongside `BuildPopupArgs()`.

2. **Content measurers** — per fit-tier modal, a pure function that renders
   (or re-uses the existing renderer for) the modal's content and returns the
   longest line width and line count. No command execution, no side effects —
   measurers read the same data sources the modal itself renders (project
   list, agent status table, proc/cron lists, memory context, remote
   sessions).

3. **`ModalConfig` extension** — a fit-tier field (fit vs cap) and an optional
   measurer hook, consumed by `ResolveSize()` at tier 4.

4. **New `muxcode popup <name> [arg...]` subcommand** — resolves the size
   through the full 5-tier chain, then invokes `display-popup` with absolute
   dimensions. Trailing args fill placeholders in the registered command for
   binds that use tmux `command-prompt` interpolation (agent history's role,
   spawn's role + task).

5. **Route the 12 static binds** in `config/tmux.conf` through
   `muxcode popup <name>`, registering each as a `ModalConfig` with its tier.

### Bind-to-tier assignment

| tmux.conf line | Modal | Current size | Tier |
|----------------|-------|--------------|------|
| 40 | Session picker (`prefix + C`) | 60%×50% | Fit (project list) |
| 45 | Menu: New Session | 60%×50% | Fit (project list) |
| 46 | Menu: Switch Session (`muxcode-switch-session.sh`) | 60%×50% | Cap (external script renders the content) |
| 50 | Agent Status | 70%×60% | Fit (status table) |
| 51 | Agent History | 80%×70% | Cap (arbitrary history payloads) |
| 52 | Memory Context | 80%×70% | Fit (memory render) |
| 54 | Spawn Agent | 70%×50% | Cap (interactive, arbitrary output) |
| 55 | List Processes | 70%×50% | Fit (proc + spawn lists) |
| 56 | Cron Jobs | 70%×50% | Fit (cron list) |
| 58 | Sessions (remote browser) | 80%×80% | Fit (session/agent tables) |
| 60 | Save Memory | 70%×50% | Fit (memory render) |
| 63 | Edit Config (`nvim`) | 80%×80% | Cap (arbitrary interactive editor) |

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/modal.go` | `FitSize()`, chrome constants, clamp env parsing, `ResolveSize()` tier 4, `ModalConfig` fit fields |
| `tools/muxcode/bus/modal_test.go` | Existing assertions (must keep passing) + new `FitSize`/tier-4 unit tests |
| `tools/muxcode/bus/measure.go` | `ContentMeasurer` type, `MeasureText`/`MeasureLines`, 6 fit-tier measurers |
| `tools/muxcode/bus/measure_test.go` | Measurer unit tests (ANSI strip, rune count, sentinel) |
| `tools/muxcode/cmd/popup.go` (new) | `muxcode popup <name> [arg...]` subcommand |
| `tools/muxcode/main.go` | Subcommand registration |
| `config/tmux.conf` | 12 binds routed through `muxcode popup` |
| `scripts/test-modal-size.sh` (new) | Integration test |

### Dependencies

| Dependency | Note |
|------------|------|
| tmux >= 3.3 | Already required for popup styling (`PopupStyleArgs`, `config/tmux.conf` line 31) — do not regress |
| [`modal-window-manager`](../completed/MUX-069-modal-window-manager.md) | The Go modal registry this feature extends |

## Implementation

### Phase 1: FitSize helper and clamp constants

- [x] Add `PopupChromeCols` / `PopupChromeRows` constants to `bus/modal.go`
- [x] Implement `FitSize(contentW, contentH, clientW, clientH int) (w, h int)` — chrome add, title floor handled by caller, min/max cols clamp, 90% ceiling, client cap, `(0, 0)` on unresolvable client
- [x] Parse `MUXCODE_MODAL_MIN_COLS` / `MUXCODE_MODAL_MAX_COLS` with defaults 40/160 and fallback on invalid values
- [x] Unit tests: fit within clamps, cap on wide client, floor on narrow client, client cap beats floor, unresolvable client sentinel, env overrides

Implementation notes (verified 2026-08-18): the `(0, 0)` sentinel also fires on
non-positive *content* dimensions, not just an unresolvable client; inverted
env clamps (`MIN_COLS` > `MAX_COLS`) resolve cap-wins
(`TestFitSize_InvertedClampsCapWins`); the 90% client ceiling is applied last
so it outranks both floors.

### Phase 2: Content measurers

- [x] Implement measurers for the fit-tier modals: project list, agent status, proc + spawn lists, cron list, memory context, remote sessions
- [x] Measurers are pure — no command execution, no side effects; reuse the modals' existing renderers or data sources
- [x] Unit tests: longest-line measurement, empty content returns unavailable

Implementation notes (verified 2026-08-18): measurers live in `bus/measure.go`
as `ContentMeasurer func(session string) (cols, rows int)` with a `(0, 0)`
unavailable sentinel. Width is measured in **visible columns** —
`MeasureText`/`MeasureLines` strip ANSI colour (the fit-tier renderers emit
Dracula escapes) and count runes, not bytes. Measurers report the **widest
state** the content can reach, since a popup cannot resize once open — the
project picker is sized for the unfiltered list plus 3 header rows
(`pickerHeaderRows`).

### Phase 3: ResolveSize tier 4 integration

- [x] Extend `ModalConfig` with fit-tier field and optional measurer hook
- [x] Insert auto-fit as tier 4 in `ResolveSize()`; config default becomes tier 5 fallback
- [x] Title floor applied against the modal's `-T` title (visible width + 2)
- [x] Cap tier: percentage-of-client converted to absolute cols, clamped to `MUXCODE_MODAL_MAX_COLS`
- [x] All existing `bus/modal_test.go` tests pass unchanged; new tests for tier ordering (flag > preset > env > fit > default)

Implementation notes (verified 2026-08-18): tier 4 landed as a new
session-aware entry point `ResolveSizeIn(cfg, sizeFlag, session)` rather than
a signature change to `ResolveSize()` — legacy callers and tests stay intact,
and `BuildPopupArgs()` routes through the new chain. Auto-sizing is **explicit
opt-in** via `ModalConfig.Measurer` (fit tier) / `ModalConfig.AutoCap` (cap
tier); with neither set, behavior is exactly the legacy percentage path. A
fit-tier modal whose measurement comes back empty degrades to `capSize()`
before falling back to percentages. `MUXCODE_MODAL_CLIENT_SIZE` provides a
client-dimensions test seam so sizing is assertable without an attached tmux
client.

### Phase 4: muxcode popup subcommand

- [x] Add `cmd/popup.go` — `muxcode popup <name> [arg...]` resolves size and invokes `display-popup` with absolute dimensions
- [x] Register the 12 static binds' modals in the registry with titles, commands, tiers, and percentage fallbacks matching today's sizes
- [x] Trailing-arg placeholder substitution for command-prompt binds (history role, spawn role + task)
- [x] Register subcommand in `main.go`; `muxcode popup` with no args lists registered popups

Implementation notes (verified 2026-08-18): the 12 binds map to **11**
registry configs in the new `bus/popup.go` (`prefix + C` and the menu's New
Session share `session-picker`; `save-memory`/`memory-context` are distinct
configs sharing `MeasureMemoryContext`). Placeholders are `{1}`/`{2}`,
substituted in both command and title (` History: {1} `). `--dry-run` prints
the resolved tmux argument vector one-per-line — the integration test's seam.
Review note (advisory should-fix, deferred): dedupe the shared
`display-popup` arg prefix between `BuildPopupArgs` and `BuildPopupCommand`.

### Phase 5: Route tmux.conf binds

- [x] Replace all 12 `popup -w N% -h N%` invocations in `config/tmux.conf` with `muxcode popup <name>` (preserving `-T` titles, `TMUX_POPUP=1`, and command-prompt interpolation)
- [x] Preserve the `popup` command-alias (line 32) for any user-defined binds
- [x] Verify Dracula border styling still applies on tmux >= 3.3

Implementation notes (verified 2026-08-18): zero hardcoded `-w N%` sizes
remain in `config/tmux.conf` — binds are now `run-shell 'muxcode popup
<name>'`; titles and `TMUX_POPUP=1` moved into the registry configs, and the
border styling flows through the same `popupFrameArgs()` path as registered
modals. Installer regression suite passed 26/26 after the rewiring.

### Phase 6: Integration test

- [x] Create `scripts/test-modal-size.sh` with automated verification
- [x] Test: fit sizing returns content width plus chrome, not a percentage of the client
- [x] Test: the cap holds on a wide client — no modal exceeds `MUXCODE_MODAL_MAX_COLS` at a simulated 317-col client
- [x] Test: the floor holds on a narrow client, and the result never exceeds the client size
- [x] Test: an explicit `MUXCODE_MODAL_SIZE_<NAME>` still overrides auto-fit
- [x] Test: width is never smaller than the popup title width + 2
- [x] Run the integration test and verify all checks pass

Implementation notes (verified 2026-08-18): the script drives the real
resolver via `muxcode popup --dry-run` with `MUXCODE_MODAL_CLIENT_SIZE`
standing in for an attached client, so it passes on any terminal and in CI.
Beyond the five mandated assertions it also covers the unresolvable-client
percentage fallback and that every popup named in `tmux.conf` is registered.
Run agent executed it green — 8/8 PASS — followed by a full sweep (gofmt,
build, vet, full unit suite, installer regression 26/26) at 11:35.

## Status

Complete — all 6 phases implemented and verified 2026-08-18 (unit suite green,
integration test 8/8, installer regression 26/26, review LGTM w/notes). Ready
to move to `completed/` on a user-initiated commit. One advisory should-fix
deferred: dedupe the `display-popup` arg prefix between `BuildPopupArgs` and
`BuildPopupCommand`.
