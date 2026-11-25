# The /tmp Go Caches the Disk-Pressure Sweep Counts but Cannot Clear

**Tracking:** [mkober/muxcode#77](https://github.com/mkober/muxcode/issues/77)

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
[MUX-157](../completed/MUX-157-role-boundary-an-agent-can-ignore.md) — each run of the suite it should not
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
   [MUX-153](../completed/MUX-153-codex-test-agent-cannot-run-the-suite.md) shape, an agent working around the
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
| [`MUX-153`](../completed/MUX-153-codex-test-agent-cannot-run-the-suite.md) | The sandbox limitation the caches work around; Phase 2's cache grant is the next step that spec needs anyway |
| [`MUX-157`](../completed/MUX-157-role-boundary-an-agent-can-ignore.md) | The 24-directory burst is that incident's footprint on disk |
| [`MUX-147`](../completed/MUX-147-process-leak-and-memory-footprint.md) | Same family — a resource the daemon can count but not reap |
| `dba6f93` | Fixed the sweep's identity guard; this spec fixes the pressure that invokes the sweep |

## Decisions (user, 2026-11-24)

The user's decisions, relayed by edit. The spec was parked on three decisions it marked "open, the
user's call"; all three are settled and the scope is cut to what the evidence still supports.

| # | Question | Decision |
|---|----------|----------|
| 1 | Which codex roles get the Go cache grant | **Settled, nothing to build.** The test role already holds the grant — `codexWritableRoots` (`bus/provider_codex.go`) gives `goToolchainRoots()` to build and test — and since 2026-11-02 every Codex role launches with no sandbox by default (`codexSandboxMode` returns `danger-full-access`), so `go test` writes the real cache. The reason agents invented `/tmp` caches is gone on the default road |
| 2 | How `/tmp` caches are recognised for removal | **Age floor only**, over the `muxcode-go*cache*` names. No muxcode-owned session-scoped `GOCACHE` |
| 3 | Back-off after unclearable cycles | **Stop until the footprint changes**, with a 30 min ceiling re-check |

**Scope, as decided.** Ship the accounting fix now — `freed` includes stale bytes and
`ineffective := freed == 0` — together with the `disk-pressure-unclearable` event and the Decision 3
back-off. Then **re-measure before building more**: the `/tmp` cache-removal stage, the block-based
footprint and the stage-4 gate are built only if the re-measure shows the caches are still being
created or still present.

**State at the decision** (read by plan, 2026-11-24): `ls /tmp | grep -c muxcode-go` reports `0` — no
muxcode Go cache directory exists under `/tmp` on this machine today. The accounting defect is
unchanged in code: `checkDiskPressure` (`daemon/daemon.go`) still computes
`totalFreed := claudeFreed + artifactFreed + goCacheFreed` (no stale bytes) and
`ineffective := staleCleaned == 0 && claudeCleaned == 0 && artifactCleaned == 0 && goCacheCleaned == 0`
(counts, not bytes).

## Requirements

### Acceptance criteria

- [ ] `ineffective` is decided on **bytes freed**, and `freed` includes stale bytes; a `stale=N` count can no longer make a zero-byte cycle read as effective
- [ ] Three consecutive zero-byte cycles under pressure raise a distinct `disk-pressure-unclearable` lifecycle event naming the largest counted paths, and the ladder stops until the footprint changes, re-checking at most every 30 min — `CleanupStale` is not invoked every 60 s against a footprint it cannot move
- [ ] Negative control: a cycle that frees bytes resets the zero-cycle counter and the back-off
- [ ] Negative control: a live session's bus directory is never swept — pins `dba6f93` on this path
- [x] The codex test role receives the Go cache roots, so the `/tmp` workaround is not needed — shipped: `codexWritableRoots` grants `goToolchainRoots()` to test (`bus/provider_codex.go`), and Codex runs unsandboxed by default (`codexSandboxMode`, 2026-11-02). Decision 1
- [ ] The re-measure is recorded in this spec: whether any `/tmp/muxcode-go*cache*` directory exists or is created across a full codex build+test cycle, and what the recurring `stale=N` count is
- [ ] If the re-measure finds such caches: a stage removes them (`muxcode-gocache*`, `muxcode-go-cache`, `muxcode-go-build-cache`) subject to the age floor and reports their bytes in `freed`; the toolchain's `go env GOCACHE` is never deleted to relieve a footprint made of `/tmp` copies; the footprint measures allocated blocks. If it finds none, this item is ticked with the measurement and the stage is not built
- [ ] `scripts/test-disk-pressure.sh` covers the byte-based `ineffective`, the unclearable event, the back-off and its reset, behind its coverage floor
- [ ] Docs: `configuration.md` (the new event, the back-off), `architecture.md` (the ladder's accounting and back-off)

### Technical approach

- **Honest accounting.** `freed` includes stale bytes; `ineffective := freed == 0`; the pressure row
  lists the three largest counted paths so it is actionable.
- **Back-off (Decision 3).** After three zero-byte cycles the daemon logs `disk-pressure-unclearable`
  once and stops running the ladder until `MuxcodeTmpFootprint` changes, re-checking at a 30 min
  ceiling.
- **Re-measure, then decide.** With the grant and the unsandboxed default in place, the creator of
  the `/tmp` caches may be gone. One codex build+test cycle and a look at `/tmp` settle whether the
  removal stage is still worth building.
- **Clean what is counted — only if the re-measure calls for it (Decision 2).** A stage between 1
  and 2 removes `/tmp/muxcode-go*cache*` entries older than the age floor (default 10 min — derived
  state, so a cache deleted under a running `go test` costs one rebuild, never data), reporting
  bytes in `freed`. Stage 4 stays last, behind `GoCacheRelievesTmp`, and additionally behind "still
  over the limit after the `/tmp` caches". `dirSize` sums `st_blocks × 512` where available.

### Key files

| File | Change |
|------|--------|
| `tools/muxcode/daemon/daemon.go` | `checkDiskPressure`: byte-based `ineffective`, stale bytes in `freed`, zero-cycle counter, `disk-pressure-unclearable`, back-off |
| `tools/muxcode/bus/cleanup.go` | `CleanupStale` reports the bytes it removed; `MuxcodeTmpFootprint` returns the largest entries for the row. Phase 4 only: `CleanupTmpGoCaches`, block-based `dirSize`, the stage-4 gate |
| `tools/muxcode/bus/config.go` | `MUXCODE_DISK_PRESSURE_BACKOFF`; Phase 4 only: `MUXCODE_TMP_GOCACHE_AGE` |
| `scripts/test-disk-pressure.sh` | Extended (Phase 6) |
| `docs/configuration.md`, `docs/architecture.md` | Phase 5 |

## Implementation

### Phase 1: Pin

- [ ] Characterization: `ineffective` reads false with `stale=1, freed=0` — pins the accounting defect for Phase 2; the test asserts today's behaviour, so the suite stays green until Phase 2 flips its expectation
- [ ] Establish what the recurring `stale=N` is: re-counted or re-created — read from a live daemon with `CleanupStale(dryRun=true)`; record the answer in this spec

### Phase 2: Honest accounting and back-off

- [ ] `CleanupStale` reports bytes removed; `freed` includes them; `ineffective := freed == 0`; Phase 1's characterization flips in the same change
- [ ] The pressure row names the three largest counted paths
- [ ] Zero-byte cycle counter; on the third, `disk-pressure-unclearable` with the top paths, then the ladder stops until the footprint changes, with a 30 min ceiling re-check (Decision 3)
- [ ] `shouldAlertDiskPressure` and the back-off pinned in `disk_pressure_alert_test.go`: once per window, and not at all while backed off
- [ ] Negative control: a cycle that frees bytes resets the counter and the back-off
- [ ] Negative control: a live session's bus directory is untouched (`dba6f93` pin on this path), under the scratch-tmp override — never the real `/tmp`

### Phase 3: Re-measure

- [ ] Run one full codex build+test cycle on the default (unsandboxed) road and list `/tmp/muxcode-go*`: record the count before and after, and each directory's size by `dirSize` and by `du`
- [ ] Repeat under `MUXCODE_CODEX_SANDBOX=workspace-write`, the opt-out where the grant is what matters; record the same
- [ ] Record the verdict here: caches still created (Phase 4 proceeds) or not (Phase 4's steps are ticked as not built, each annotated with this measurement)

### Phase 4: Clean what is counted — only if Phase 3 finds caches

- [ ] `CleanupTmpGoCaches`: `/tmp/muxcode-go*cache*` older than the age floor (`MUXCODE_TMP_GOCACHE_AGE`, default 10 min), bytes reported; wired as the stage after `CleanupStale` (Decision 2 — age floor only)
- [ ] `MuxcodeTmpFootprint` measures allocated blocks
- [ ] Stage 4 gated on the post-cleanup footprint as well as `GoCacheRelievesTmp`
- [ ] Negative controls: `go env GOCACHE` untouched with `/tmp` caches present; a cache younger than the floor survives; tests under the scratch-tmp override

### Phase 5: Docs

- [ ] `docs/configuration.md`: `MUXCODE_DISK_PRESSURE_BACKOFF`, the `disk-pressure-unclearable` event, and — if Phase 4 was built — `MUXCODE_TMP_GOCACHE_AGE` and the block-based footprint
- [ ] `docs/architecture.md`: the ladder's byte-based accounting and back-off, and the new stage if built

### Phase 6: Integration test

- [ ] Extend `scripts/test-disk-pressure.sh` (hermetic: scratch tmp via the override, never the real `/tmp`): `stale=N, freed=0` reads ineffective; stale bytes appear in `freed`
- [ ] Three zero-byte cycles emit `disk-pressure-unclearable` once and the fourth cycle does not run the ladder; a footprint change runs it again
- [ ] Negative control: a cycle that frees bytes emits no `disk-pressure-unclearable` and leaves no back-off; a live bus dir is untouched
- [ ] If Phase 4 was built: a `muxcode-gocache.X` fixture over the limit is removed and reported in `freed`, one under the age floor survives, a fake `GOCACHE` on the same device is untouched
- [ ] Coverage floor keeps a skipped section from reading green
- [ ] Run the script and record pass/fail counts in this spec

## Notes

- Filed 2026-09-08 22:40 by plan from edit's handoff (`gocache-leak-filing.md`), on the user's
  request. Edit's measurements were re-read live by plan: the 27 directories, their creation
  timestamps (`ls -ldT`), the daemon's own `disk-pressure` rows (`muxcode lifecycle show --event
  disk-pressure`, 14 that day), and `du -sc` for the block count. The creation burst's alignment
  with the MUX-157 window, the `stale=12`/`ineffective` interaction, and the apparent-vs-block
  under-count are plan's additions.
- The Mechanism section describes the code as filed. Item 5 ("grants the Go caches to the build
  role only") is no longer true — see Decision 1. Items 1–4 were not re-read line by line on
  2026-11-24 beyond the accounting lines quoted under Decisions; their line numbers are September's.
- Rewritten 2026-11-24 to the user's decisions: the owned session-scoped `GOCACHE`, the grant work
  and the `CLAUDE.md` step are dropped; the removal stage is conditional on Phase 3.
- Immediate relief, if the caches reappear, is manual and safe: the directories are derived state,
  so `rm -rf /tmp/muxcode-go*cache*` costs one rebuild per agent — a user action.

## Status

**Backlog** — unparked 2026-11-24 on the user's decisions. Not started; one criterion ticked as
already shipped.
