# The /tmp Go Caches the Disk-Pressure Sweep Counts but Cannot Clear

Agents create their own Go build caches under `/tmp` to get past the Codex sandbox. The
disk-pressure footprint **counts** those directories — they are what trips the 1 GiB limit — and
**no stage of the cleanup ladder can remove them**, so pressure fires every 60 seconds, runs the full
ladder, frees nothing, and fires again. Fourteen lifecycle rows on 2026-09-08 read
`gocache=0 freed=0 B` while 4.4 GB of Go cache sat in `/tmp`; the footprint climbed from 1067.8 MB to
4407.8 MB across the evening.

The leak and the sweep feed each other: the caches inflate the footprint that triggers the sweep that
cannot clear the caches. And the sweep is the same `CleanupStale` that, until `dba6f93` (20:13),
was deleting the **live session's bus directory** every few minutes — the identity probe was fixed,
the motor that invokes it was not.

Tracking: _(no GitHub issue yet)_

## Context

### Observed (2026-09-08 — measured by edit at 22:30, re-read by plan at 22:35)

| Measurement | Value |
|-------------|-------|
| Directories | 27 under `/tmp`: `muxcode-go-cache` (11:27), `muxcode-go-build-cache` (13:25), `muxcode-gocache` (19:07), and **24 `muxcode-gocache.XXXXXX`** created between **19:40:50 and 20:10:16** — one every 30–90 s, ~149 MB each |
| Size | daemon footprint `4407.8 MB` (its `dirSize`, apparent bytes); `du -sc` at 22:35 reports **9.0 GiB of blocks** for the same set — a Go cache is many small files, so the footprint under-reports the disk it actually costs by about half |
| Toolchain cache | `go env GOCACHE` = `~/Library/Caches/go-build` — a different path, which is the only one the purge stage looks at |
| Lifecycle | 14 `disk-pressure` rows that day; the last four: `footprint=4362.0 → 4362.0 → 4407.7 → 4407.8 MB`, every one `stale=12 claude=0 artifacts=0 gocache=0 freed=0 B`, `/tmp free` ≈ 52 GB throughout — headroom was never the trigger, the footprint was |
| Creator | verbatim from the test agent's pane: `mkdir -p /tmp/muxcode-go-cache && GOCACHE=/tmp/muxcode-go-cache go test ./bus`. Nothing in the repo creates these names — edit grepped the tree with no extension filter, `strings` on both binaries, `~/.config/muxcode`, the settings files and the shell profiles |

The 24 suffixed directories are a `mktemp -d` per `go test` invocation, and their window is the test
agent's source-authoring episode recorded in
[MUX-157](./MUX-157-role-boundary-an-agent-can-ignore.md) — each run of the suite it should not
have been running left a fresh 149 MB behind.

### Mechanism — verified in code

1. **The footprint counts them.** `MuxcodeTmpFootprint` (`bus/cleanup.go:446`) sums every `/tmp`
   entry whose name starts with `muxcode-`; `TmpPressure` (`:477`) declares pressure when that exceeds
   `defaultTmpFootprintLimit` = 1 GiB (`bus/config.go`). 4.4 GB of caches is four times the limit.
2. **No stage removes them.** `CheckDiskPressure` (`:507`) runs: stage 1 `CleanupStale` (`:50`) —
   only `muxcode-bus-`, `muxcode-preview-`, `muxcode-analyze-`, `muxcode-spawn-` and `muxcode-log-`
   entries, keyed on session liveness; stage 2 Claude Code `/tmp` sessions older than 7 days; stage 3
   the repo's regenerable build output; stage 4 `PurgeGoBuildCache` (`bus/artifacts.go:186`), whose
   target is `goCacheDir()` = `go env GOCACHE` (`:165`) — the toolchain's configured cache, never a
   per-agent `/tmp` copy. The signal and the cleanup disagree about what "muxcode's footprint" is.
3. **Stage 4 aims at the wrong directory.** On macOS `GoCacheRelievesTmp` is true (`/tmp` and
   `~/Library/Caches` share one APFS volume), so once the user's real cache passes the 1 GiB floor it
   is `go clean -cache`d to relieve a pressure it did not cause — slowing every Go build on the
   machine and freeing nothing that the footprint measures. `gocache=0` on every row tonight means the
   real cache was under the floor, not that the stage is safe.
4. **`stale=12` every cycle defeats the alert contract.** `CleanupStale` reports twelve items on every
   cycle while the footprint keeps rising — either the same twelve are re-counted (a removal failing
   silently) or they are re-created inside 60 s; **not established**. Either way the daemon's
   `ineffective` (`daemon/daemon.go`, `checkDiskPressure`) is `staleCleaned == 0 && …`, so a non-zero
   stale count makes every cycle read *effective*: the row is logged every minute and the alert runs
   on the short cooldown. The "alerts once, not every cycle" contract
   (`TestShouldAlertDiskPressure_FiresOncePerWindowNotEvery…`) is bypassed by a count that never
   reaches zero. `totalFreed` also excludes stale bytes, which is why `stale=12` and `freed=0 B` sit
   on the same line.
5. **Why agents create them.** `codexWritableRoots` (`bus/provider_codex.go:91`) grants the Go caches
   (`goToolchainRoots`, `:153`) to **the build role only**. The test role runs under the default
   sandbox, where `~/Library/Caches/go-build` is unwritable, so `go test` fails on the cache path and
   the model routes around it with `GOCACHE=/tmp/…` — the
   [MUX-153](./MUX-153-codex-test-agent-cannot-run-the-suite.md) shape, an agent working around the
   sandbox, producing disk this time instead of silence.

### Why this is worse than wasted disk

`CleanupStale` is invoked by every pressure cycle. Before `dba6f93` its liveness probe read any
non-zero `tmux has-session` exit as "gone", and a sandboxed daemon that could not reach the tmux
socket deleted the attached session's bus directory — inboxes, tracked tasks, delivery receipts, the
active-spec pointer, `graphs/` — repeatedly through the evening. `dba6f93` fixed the probe. It did not
change the fact that an unclearable footprint keeps the sweep running forever, so the next regression
in that guard is armed by default rather than dormant. Removing the pressure is what makes the sweep
rare again.

### Relationship

| Spec / commit | Relationship |
|---------------|--------------|
| [`MUX-002`](../completed/MUX-002-disk-pressure-wrong-filesystem.md) | The signal redesign — pressure from free headroom and muxcode's own footprint instead of percent-used. This is its ladder missing the dominant consumer of that footprint; its own test header describes the exact symptom ("ran cleanup every 60 seconds forever, freed 0 B every time") returning by another road |
| [`MUX-153`](./MUX-153-codex-test-agent-cannot-run-the-suite.md) | The sandbox limitation the caches work around; Phase 2's cache grant is the next step that spec needs anyway |
| [`MUX-157`](./MUX-157-role-boundary-an-agent-can-ignore.md) | The 24-directory burst is that incident's footprint on disk |
| [`MUX-147`](./MUX-147-process-leak-and-memory-footprint.md) | Same family — a resource the daemon can count but not reap |
| `dba6f93` | Fixed the sweep's identity guard; this spec fixes the pressure that invokes the sweep |

## Requirements

### Acceptance criteria

- [ ] The footprint and the cleanup agree: every directory the footprint counts is one some stage can remove, or the pressure row names it as unclearable — never a silent `freed=0 B`
- [ ] A stage removes muxcode-created Go caches under the `/tmp` prefix (`muxcode-gocache*`, `muxcode-go-cache`, `muxcode-go-build-cache`) — subject to an age floor — and reports their bytes in `freed`
- [ ] Negative control: the toolchain's `go env GOCACHE` is never deleted to relieve a footprint made of `/tmp` copies — stage 4 runs only when the footprint is still over the limit **after** the `/tmp` caches are gone, and only under the existing same-device guard
- [ ] Negative control: a live session's bus directory is never swept — pins `dba6f93` on this path
- [ ] `ineffective` is decided on **bytes freed**, and `freed` includes stale bytes; a `stale=N` count can no longer make a zero-byte cycle read as effective
- [ ] Three consecutive zero-byte cycles under pressure raise a distinct `disk-pressure-unclearable` lifecycle event naming the largest unclearable paths, and the ladder backs off until the footprint changes — `CleanupStale` is not invoked every 60 s against a footprint it cannot move
- [ ] The footprint measures allocated blocks, not apparent size, so the number in the row is the disk the directories actually cost
- [ ] The codex test role receives the Go cache roots (Decision 1 settles which other roles), so the `/tmp` workaround is never needed — negative control: a full codex build+test cycle creates no new `/tmp/muxcode-go*` directory
- [ ] `scripts/test-disk-pressure.sh` covers the new stage, the two negative controls, the byte-based `ineffective`, and the back-off, behind its coverage floor
- [ ] Docs: `configuration.md` (the new event, the age floor, the back-off), `architecture.md` (the ladder), `CLAUDE.md` (the Codex sandbox note gains the cache grant)

### Technical approach

- **Prevent before cleaning.** The cheapest fix removes the creator: `codexWritableRoots` grants
  `goToolchainRoots()` to every codex role that compiles, not build alone. Once `go test` can write
  the real cache, no agent has a reason to invent a `/tmp` one. This is MUX-153's next step regardless.
- **Own the path when a sandboxed role must have one.** Rather than leaving `GOCACHE` to the model,
  muxcode sets `GOCACHE`/`GOMODCACHE` in the launch environment of sandboxed codex roles to a
  session-scoped directory it names (`/tmp/muxcode-gocache-<session>-<role>`), so stage 1 removes it
  with the session exactly as it removes the bus directory — cleanup keyed on liveness, no age
  guessing (Decision 2).
- **Clean what is counted.** A new stage between 1 and 2 removes `/tmp/muxcode-go*cache*` entries
  older than the age floor (default 10 min — derived state, so a cache deleted under a running
  `go test` costs one rebuild, never data), reporting bytes in `freed`. Stage 4 stays last, behind
  `GoCacheRelievesTmp`, and additionally behind "still over the limit after the `/tmp` caches".
- **Honest accounting.** `freed` includes stale bytes; `ineffective := freed == 0`; the pressure row
  lists the three largest counted paths so it is actionable; after three zero-byte cycles the daemon
  logs `disk-pressure-unclearable` once and backs off (Decision 3) until `MuxcodeTmpFootprint` changes.
- **Measure blocks.** `dirSize` sums `st_blocks × 512` where available, apparent size otherwise, so
  the footprint matches `du`.

### Key files

| File | Change |
|------|--------|
| `tools/muxcode/bus/cleanup.go` | `MuxcodeTmpFootprint` (`:446`) block-based and reporting top paths; new `CleanupTmpGoCaches`; `CheckDiskPressure` (`:507`) gains the stage and the post-stage gate for stage 4 |
| `tools/muxcode/bus/artifacts.go` | `PurgeGoBuildCache` (`:186`) unchanged in target, gated on the post-cleanup footprint |
| `tools/muxcode/daemon/daemon.go` | `checkDiskPressure`: byte-based `ineffective`, stale bytes in `freed`, zero-cycle counter, `disk-pressure-unclearable`, back-off |
| `tools/muxcode/bus/provider_codex.go` | `codexWritableRoots` (`:91`) grants the Go caches per Decision 1; launch env sets a session-scoped `GOCACHE` for sandboxed roles |
| `tools/muxcode/bus/config.go` | `MUXCODE_TMP_GOCACHE_AGE`, `MUXCODE_DISK_PRESSURE_BACKOFF` |
| `scripts/test-disk-pressure.sh` | Extended (Phase 6) |
| `docs/configuration.md`, `docs/architecture.md`, `CLAUDE.md` | Phase 5 |

### Decisions — open, the user's call

| # | Question | Options | Recommendation |
|---|----------|---------|----------------|
| 1 | Which codex roles get the Go cache grant | build only (today) · build + test · every role that may compile (build, test, run) | **build + test + run** — the roles whose definitions run Go; review and analyze are `-a on-request` read-only roles and should not compile at all |
| 2 | How `/tmp` caches are recognised for removal | age floor over the `muxcode-go*cache*` names · muxcode-owned session-scoped `GOCACHE` cleaned with the session · both | **Both** — the owned path is the durable answer and keys on liveness; the age rule mops up the ad-hoc names that already exist and any a model still invents |
| 3 | Back-off after unclearable cycles | fixed 30 min · exponential from 1 min to 30 min · stop until the footprint changes | **Stop until the footprint changes**, with a 30 min ceiling re-check — the sweep should not run against a number that has not moved |

## Implementation

### Phase 1: Pin

- [ ] Characterization: a scratch `/tmp` (via `busDirOverride`) holding a `muxcode-gocache.X` dir over the limit → `TmpPressure` true, the ladder runs, `freed=0`; failure message names Phase 3
- [ ] Characterization: `ineffective` reads false with `stale=1, freed=0` — pins the accounting defect for Phase 4
- [ ] Establish what the recurring `stale=12` is: re-counted or re-created — read from a live daemon with `CleanupStale(dryRun=true)`; record the answer in this spec
- [ ] Measure `dirSize` against `du` on one real cache directory and record the ratio

### Phase 2: Stop the creation

- [ ] `codexWritableRoots` grants `goToolchainRoots()` per Decision 1; `TestCodexBuildExecArgs_BuildGetsInstallRoots` gains its sibling for test
- [ ] Sandboxed codex roles launch with `GOCACHE`/`GOMODCACHE` set to a session-scoped muxcode-owned path (Decision 2), created at launch, removed by stage 1 with the session
- [ ] Negative control: the build role's roots are unchanged; a non-compiling role gets no cache roots
- [ ] Live check: a full codex build+test cycle leaves no new `/tmp/muxcode-go*` directory

### Phase 3: Clean what is counted

- [ ] `CleanupTmpGoCaches`: `/tmp/muxcode-go*cache*` older than the age floor, bytes reported; wired as the stage after `CleanupStale`
- [ ] `MuxcodeTmpFootprint` measures blocks and returns the three largest entries for the row
- [ ] Stage 4 gated on the post-cleanup footprint as well as `GoCacheRelievesTmp`
- [ ] Negative controls: `go env GOCACHE` untouched with `/tmp` caches present; a live session's bus dir untouched (`dba6f93` pin on this path); a cache younger than the floor survives
- [ ] Tests for each, in `cleanup_test.go` / `artifacts_test.go` under the scratch-tmp override — never the real `/tmp`

### Phase 4: Honest accounting and back-off

- [ ] `freed` includes stale bytes; `ineffective := freed == 0`
- [ ] Zero-byte cycle counter; on the third, `disk-pressure-unclearable` with the top paths, then back-off per Decision 3
- [ ] `shouldAlertDiskPressure` and the new back-off pinned in `disk_pressure_alert_test.go`: once per window, and not at all while backed off
- [ ] Negative control: a cycle that frees bytes resets the counter and the back-off

### Phase 5: Docs

- [ ] `docs/configuration.md`: `MUXCODE_TMP_GOCACHE_AGE`, `MUXCODE_DISK_PRESSURE_BACKOFF`, the `disk-pressure-unclearable` event, block-based footprint
- [ ] `docs/architecture.md`: the ladder with its new stage and the stage-4 gate
- [ ] `CLAUDE.md`: the Codex sandbox constraint names which roles hold the cache grant and the owned `GOCACHE`

### Phase 6: Integration test

- [ ] Extend `scripts/test-disk-pressure.sh` (hermetic: scratch tmp via the override, never the real `/tmp`): a `muxcode-gocache.X` fixture over the limit is removed and reported in `freed`; one under the age floor survives; a fake `GOCACHE` on the same device is untouched; a live bus dir is untouched
- [ ] Accounting: `stale=N, freed=0` reads ineffective; three zero-byte cycles emit `disk-pressure-unclearable` once and the fourth cycle does not run the ladder
- [ ] Sandbox: launch a scratch codex test agent (skipped **with reason** without codex) → `go env GOCACHE` writable from its pane, no `/tmp/muxcode-go*` created
- [ ] Coverage floor keeps a skipped live section from reading green
- [ ] Run the script and record pass/fail counts in this spec

## Notes

- Filed 2026-09-08 22:40 by plan from edit's handoff (`gocache-leak-filing.md`), on the user's
  request. Edit's measurements were re-read live by plan: the 27 directories, their creation
  timestamps (`ls -ldT`), the daemon's own `disk-pressure` rows (`muxcode lifecycle show --event
  disk-pressure`, 14 that day), and `du -sc` for the block count. The creation burst's alignment
  with the MUX-157 window, the `stale=12`/`ineffective` interaction, and the apparent-vs-block
  under-count are plan's additions.
- Immediate relief is manual and safe: the directories are derived state, so `rm -rf
  /tmp/muxcode-go*cache*` costs one rebuild per agent and stops the 60-second cycle tonight — a user
  action, not one this spec automates ahead of Phase 3.

## Status

**Backlog** — filed 2026-09-08 on the user's request. Not started. 0/35 items.
