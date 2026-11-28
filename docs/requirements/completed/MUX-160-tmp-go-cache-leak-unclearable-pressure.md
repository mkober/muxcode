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

- [x] `ineffective` is decided on **bytes freed**, and `freed` includes stale bytes; a `stale=N` count can no longer make a zero-byte cycle read as effective — Phase 2, unit tests
- [x] Three consecutive zero-byte cycles under pressure raise a distinct `disk-pressure-unclearable` lifecycle event naming the largest counted paths, and the ladder stops until the footprint changes, re-checking at most every 30 min — `CleanupStale` is not invoked every 60 s against a footprint it cannot move — Phase 2, unit tests; "changes" is at least 1 % and 1 MiB, see [Findings for Phase 2](#findings-for-phase-2)
- [x] Negative control: a cycle that frees bytes resets the zero-cycle counter and the back-off — Phase 2, unit tests
- [x] Negative control: a live session's bus directory is never swept — pins `dba6f93` on this path — Phase 2, unit test under the scratch override
- [x] The codex test role receives the Go cache roots, so the `/tmp` workaround is not needed — shipped: `codexWritableRoots` grants `goToolchainRoots()` to test (`bus/provider_codex.go`), and Codex runs unsandboxed by default (`codexSandboxMode`, 2026-11-02). Decision 1
- [x] The re-measure is recorded in this spec: whether any `/tmp/muxcode-go*cache*` directory exists or is created across a full codex build+test cycle, and what the recurring `stale=N` count is — none exists or is created; `stale=N` is 11, re-counted. [Findings for Phase 3](#findings-for-phase-3)
- [x] If the re-measure finds such caches: a stage removes them (`muxcode-gocache*`, `muxcode-go-cache`, `muxcode-go-build-cache`) subject to the age floor and reports their bytes in `freed`; the toolchain's `go env GOCACHE` is never deleted to relieve a footprint made of `/tmp` copies; the footprint measures allocated blocks. If it finds none, this item is ticked with the measurement and the stage is not built — **it found none (0 on both roads, 2026-11-27); the stage is not built.** Left as they were: stage 4 may still purge the real cache past its floor, and the footprint sums apparent bytes
- [x] `scripts/test-disk-pressure.sh` covers the byte-based `ineffective`, the unclearable event, the back-off and its reset, behind its coverage floor — 16 passed, 0 failed, floor 16, 2026-11-27 19:03
- [x] Docs: `configuration.md` (the new event, the back-off), `architecture.md` (the ladder's accounting and back-off) — Phase 5, written by plan from the code. Both links to this spec were retargeted to `requirements/completed/` at close-out

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

- [x] Characterization: `ineffective` reads false with `stale=1, freed=0` — pins the accounting defect for Phase 2; the test asserts today's behaviour, so the suite stays green until Phase 2 flips its expectation — `TestDiskPressureIneffective_StaleCountReadsEffective` on `diskPressureIneffective`; `./test.sh` passed 2026-11-27 18:32, review 0/0/0
- [x] Establish what the recurring `stale=N` is: re-counted or re-created — read from a live daemon with `CleanupStale(dryRun=true)`; record the answer in this spec — today's set is **re-counted**: the same dead-session directories, days old. What September's twelve were is not recoverable; see [Findings for Phase 1](#findings-for-phase-1)

### Findings for Phase 1

Recorded 2026-11-27 from the Phase 1 worker's notes (`/tmp/mux160-phase1-findings.md`, run
`1795811929`). Plan re-ran `muxcode cleanup --dry-run` and got the same 11 items; the code and test
were read in the tree. The characterization test first ran under `./test.sh` at 18:32, and step 1
was ticked then, at the run's verification.

| Finding | Detail |
|---------|--------|
| The expression is now a function | `diskPressureIneffective(stale, claude, artifacts, gocache int) bool` in `daemon/daemon.go`, extracted with behaviour unchanged (counts, not bytes) and called from `checkDiskPressure` |
| The characterization | `TestDiskPressureIneffective_StaleCountReadsEffective` (`daemon/disk_pressure_alert_test.go`): `stale=1` with nothing else reads **effective** — today's defect; controls: all-zero reads ineffective, one removed artifact reads effective. Phase 2 flips the first case in the same change |
| Today's `stale=N` is 11, and re-counted | Three dry runs (18:26:59, 18:27:43, and plan's) list the identical set: 10 dead sessions' bus directories under `~/.local/state/muxcode/bus/` (`app-data-services`, `dotfiles`, `is-admissions-gateway`, `is-advising-gateway`, `is-auth-services`, `is-lms-gateway`, `lifecycle-leak-pinned-95115`, `lifecycle-leak-unpinned-95115`, `tibo`, `tmux-resurrect`), born 2026-11-18 to 2026-11-24, none on the tmux server; and one trigger file, `/tmp/muxcode-analyze-test-analyze-trigger.trigger`, born today 17:02 — re-created, by a test going by its name |
| **What that does and does not establish** | A dry run removes nothing, so an identical set across dry runs shows these items persist — not that a real cycle fails to remove them. A real cycle was not run: it would delete other sessions' bus directories. No `disk-pressure` row exists in the last 72 h, so no real cycle has run against them either. September's `stale=12` — a removal failing silently, or re-creation inside 60 s — stays **not established** |
| The stale items are outside `/tmp` and still counted | Since MUX-208 a bus directory lives under `~/.local/state`, and `MuxcodeTmpFootprint` (`bus/cleanup.go:629`) follows it: it sums the `muxcode-*` directories under the legacy `/tmp` root **and** under `BusDirBase()`, plus the whole `StateDir()/spawn` tree. So a stale bus directory's bytes are in the footprint, and removing one moves it — Phase 2's "`freed` includes stale bytes; `ineffective := freed == 0`" stands as written. An earlier version of this row, by plan, said the footprint summed `/tmp` only and drew a Phase 2 design question from that; it was wrong, corrected on the Phase 1 review's should-fix |
| The ladder is not firing here today | `ls /tmp/muxcode-go*` → 0; `/tmp/muxcode-*` → 12 entries; no `disk-pressure` row in 72 h |
| Side observation, cause not established | Two dead directories (`dotfiles`, `is-auth-services`) carry a 2026-11-27 15:49 modification time: something wrote into a dead session's bus directory today |

### Phase 2: Honest accounting and back-off

- [x] `CleanupStale` reports bytes removed; `freed` includes them; `ineffective := freed == 0`; Phase 1's characterization flips in the same change — `StaleResult.BytesFreed` (measured before each successful removal, 0 on a dry run); `diskPressureFreed` sums every stage; the characterization is now `TestDiskPressureFreed_StaleCountWithoutBytesFreesNothing`
- [x] The pressure row names the three largest counted paths — `top=` on the `disk-pressure` row, from `LargestFootprintEntries` / `FormatFootprintEntries`
- [x] Zero-byte cycle counter; on the third, `disk-pressure-unclearable` with the top paths, then the ladder stops until the footprint changes, with a 30 min ceiling re-check (Decision 3) — `diskPressureBackoff` (`daemon/disk_pressure.go`); ceiling `MUXCODE_DISK_PRESSURE_BACKOFF`, default 1800 s
- [x] `shouldAlertDiskPressure` and the back-off pinned in `disk_pressure_alert_test.go`: once per window, and not at all while backed off — `TestDiskPressureBackoff_ThirdZeroCycleIsUnclearableOnceThenHolds`, `TestCheckDiskPressure_RepeatedRemovalErrorsBecomeUnclearableThenHold` (one event, three ladder runs, then held — no cleanup, row or alert)
- [x] Negative control: a cycle that frees bytes resets the counter and the back-off — `TestDiskPressureBackoff_FreedBytesReset`; partial freed bytes beside an error reset it too
- [x] Negative control: a live session's bus directory is untouched (`dba6f93` pin on this path), under the scratch-tmp override — never the real `/tmp` — `TestCleanupStaleReportsBytesFreedAndSparesALiveSession` (`bus/disk_pressure_accounting_test.go`)

### Findings for Phase 2

Recorded 2026-11-27 from the Phase 2 `implement` and `fix` nodes of run `1795811929`; code read by
plan. `./test.sh` passed 18:44; the review returned should-fixes twice (a result returned beside an
error was dropped, so a persistent removal error never read as unclearable; then its regression test
bypassed `checkDiskPressure`) and passed 0/0/0 at 18:45. `scripts/test-disk-pressure.sh`, run as a
regression only — Phase 6 extends it — 11 passed, 0 failed.

| Finding | Detail |
|---------|--------|
| **"The footprint changes" has a threshold** | `footprintMoved` reads a change as at least 1 % of the footprint **and** at least 1 MiB. Decision 3 says "until the footprint changes"; the worker's reason is that live sessions' bus directories grow by kilobytes a minute, so any-byte change would never hold. A judgment call inside the decision, not the user's wording |
| What the hold skips | The whole cycle: no cleanup, no `disk-pressure` row, no alert. Only `tmpPressureFn` is read, to see whether pressure lifted or the footprint moved |
| What resets the back-off | A cycle that frees any byte; pressure lifting while backed off; a healthy check (no result, no error) |
| A failing stage still counts | `checkDiskPressure` no longer returns on an error when a result came with it (`handleDiskPressureResult`), so three cycles whose removals fail read as unclearable rather than retrying every 60 s unrecorded — the first review's should-fix |
| `MUXCODE_DISK_PRESSURE_BACKOFF=0` | Disables the hold: every cycle runs the ladder as before; the `disk-pressure-unclearable` row is still written once |
| The seam | `checkDiskPressureFn` and `tmpPressureFn` are package variables so a test never runs the real ladder, which deletes other sessions' artifacts on the machine running it |

### Phase 3: Re-measure

- [x] Run one full codex build+test cycle on the default (unsandboxed) road and list `/tmp/muxcode-go*`: record the count before and after, and each directory's size by `dirSize` and by `du` — 0 before, 0 after; no directory, so no size to record
- [x] Repeat under `MUXCODE_CODEX_SANDBOX=workspace-write`, the opt-out where the grant is what matters; record the same — 0 before, 0 after the build and 0 after the test, across a full cycle: `make build` and the whole `./test.sh`, each a real `codex exec` under its role's opt-out flags
- [x] Record the verdict here: caches still created (Phase 4 proceeds) or not (Phase 4's steps are ticked as not built, each annotated with this measurement) — **not created on either road; Phase 4 is not built**

### Findings for Phase 3

Recorded 2026-11-27 from the Phase 3 worker's notes (`/tmp/mux160-phase3-findings.md`, run
`1795811929`). Plan checked the run agent's history, the script `/tmp/mux160-remeasure.sh`, the build
and test agents' rows, `muxcode agent sandbox-args test`, and counted `/tmp/muxcode-go*` again: 0.

| Road | What ran | `/tmp/muxcode-go*` |
|------|----------|--------------------|
| Default (unsandboxed) | The session's live Codex build, test and review agents all run `-s danger-full-access` (`muxcode agent sandbox-args test` → `-a never -s danger-full-access`). Full cycles this session: the test agent's `./test.sh` at 18:43 (exit 1 on a fixture bug in a new test, since fixed — the whole suite compiled and ran) and at 18:44 (passed); the build agent's `./build.sh` at 18:44 (exit 0); the MUX-190 cycles earlier in the evening | 0 at 18:26, 0 at 18:46 and 18:48 |
| Opt-out (`MUXCODE_CODEX_SANDBOX=workspace-write`), full cycle, 18:51–18:54 | One real `codex exec` (codex-cli 0.162.1) per role under that role's opt-out launch flags (`muxcode agent sandbox-args <role>`; `-s workspace-write` with `--add-dir` for the lifecycle log, memory and bus directories, `go env GOCACHE`, `~/go/pkg/mod` and the SwiftPM cache; `-a` dropped, since `exec` never asks), each told to work around sandbox, cache or permission failures without editing the repo. **Build:** `make build`, run once, exit 0 — not `./build.sh`, which also installs and restarts every running daemon mid graph run; the compile and cache path is the same. **Test:** the full `./test.sh`, every module, ran to the end and exited 1 with 27 failing tests in the `bus` package (below); the model then only read the test log and a test file — no cache created, no workaround attempted. Script `/tmp/mux160-remeasure-full.sh`, log `/tmp/mux160-remeasure-full.log` | 0 before, 0 after the build, 0 after the test |
| Opt-out, first cut, 18:48 | One `codex exec` as the test role ran `go -C tools/muxcode test -count=1 -run '^$' ./bus`, exit 0, into the real `GOCACHE`. Compile-only; the review asked for the full cycle above, which supersedes it | 0 before, 0 after |

| Finding | Detail |
|---------|--------|
| **Verdict** | No `/tmp` Go cache is created on either road. The reason agents invented them — a cache path the sandbox refused — is gone: no sandbox by default, and under the opt-out the grant lets `go` write the real cache |
| The suite ran under the sandbox, and 27 tests fail there | Counted by plan from the log's top-level `--- FAIL:` lines (`/tmp/muxcode-test-logs/20261127-185146-70535/muxcode.log`): **27, all in the `bus` package** — 22 `TestCancel*` (graph cancel and spawn-worker stop), `TestCleanupHoldsAnUnresolvedSessionWhole`, `TestExecCancelMidRun`, `TestExecRetryBelowNeverApprovedGateUnaffected`, `TestExecHoldingOwnerLockSurvivesExec` and `TestReapOrphansTerminatesTheWholeProcGroup`. The root, `cmd`, `daemon` and `tui` packages passed. On the default road the same tree's `bus` package passes: the 18:50 suite log (`20261127-185022-61181`) holds no `--- FAIL:` line and reads `ok … bus (cached)` — a cached pass, so the fresh run behind it is an earlier one on identical inputs. **Cause not established per test.** They are the tests that start, signal and supervise processes, which fits a `workspace-write` Seatbelt policy restricting process control, but only one message was read (`TestCleanupHoldsAnUnresolvedSessionWhole`: "the proc tree never started its TERM-ignoring child"). **A candidate defect — the suite does not pass on the Codex opt-out road — unfiled; the user's call.** This is also the first recorded full `./test.sh` under the Codex sandbox, which [MUX-153](../completed/MUX-153-codex-test-agent-cannot-run-the-suite.md) notes had never run there. An earlier version of this row said one test failed: the worker's notes repeated the run agent's one-line summary, and plan recorded it without opening the log |
| The build half used `make build` | `./build.sh` was not run under the sandbox: it installs and restarts the daemons. Compilation and the Go cache path are the same; the install half is not measured |
| `codex sandbox` could not replay the launch | 0.162.1 needs `--permission-profile`, and its built-in `:workspace` profile ignores `-c sandbox_workspace_write.writable_roots` — a `touch` into `GOCACHE` was refused with and without it. Hence the real `codex exec` |
| The recurring `stale=N` | 11 today, re-counted — [Findings for Phase 1](#findings-for-phase-1) |

### Phase 4: Clean what is counted — only if Phase 3 finds caches

Not built. Each step is ticked as **not built**, on Phase 3's measurement: 0 `/tmp/muxcode-go*`
directories across Codex build and test on the default road and under `workspace-write`, 2026-11-27.
If such caches reappear, these four steps are the work, and
`rm -rf /tmp/muxcode-go*cache*` is the safe manual relief meanwhile (Notes).

- [x] `CleanupTmpGoCaches`: `/tmp/muxcode-go*cache*` older than the age floor (`MUXCODE_TMP_GOCACHE_AGE`, default 10 min), bytes reported; wired as the stage after `CleanupStale` (Decision 2 — age floor only) — **not built** (Phase 3: 0 caches on either road)
- [x] `MuxcodeTmpFootprint` measures allocated blocks — **not built** (Phase 3: 0 caches on either road); the footprint still sums apparent bytes
- [x] Stage 4 gated on the post-cleanup footprint as well as `GoCacheRelievesTmp` — **not built** (Phase 3: 0 caches on either road); stage 4 can still `go clean -cache` the real cache past its floor
- [x] Negative controls: `go env GOCACHE` untouched with `/tmp` caches present; a cache younger than the floor survives; tests under the scratch-tmp override — **not built** (Phase 3: 0 caches on either road)

### Phase 5: Docs

- [x] `docs/configuration.md`: `MUXCODE_DISK_PRESSURE_BACKOFF`, the `disk-pressure-unclearable` event, and — if Phase 4 was built — `MUXCODE_TMP_GOCACHE_AGE` and the block-based footprint — a new "Disk pressure" subsection: the four variables (the three older ones had no entry anywhere) and both lifecycle rows. Phase 4 not built, so no cache-age variable or block footprint
- [x] `docs/architecture.md`: the ladder's byte-based accounting and back-off, and the new stage if built — a new "Disk-pressure cleanup" subsection: the four stages, the accounting, the back-off table. The ladder had no section before. No new stage

### Phase 6: Integration test

- [x] Extend `scripts/test-disk-pressure.sh` (hermetic: scratch tmp via the override, never the real `/tmp`): `stale=N, freed=0` reads ineffective; stale bytes appear in `freed` — section "MUX-160 byte-based accounting", 3 checks
- [x] Three zero-byte cycles emit `disk-pressure-unclearable` once and the fourth cycle does not run the ladder; a footprint change runs it again — section "MUX-160 back-off", through `checkDiskPressure` and its seams
- [x] Negative control: a cycle that frees bytes emits no `disk-pressure-unclearable` and leaves no back-off; a live bus dir is untouched — the back-off section's controls (a freeing cycle; `MUXCODE_DISK_PRESSURE_BACKOFF=0` never holds) and the accounting section's live-session check
- [x] If Phase 4 was built: a `muxcode-gocache.X` fixture over the limit is removed and reported in `freed`, one under the age floor survives, a fake `GOCACHE` on the same device is untouched — **not applicable: Phase 4 was not built**; the script's header says so
- [x] Coverage floor keeps a skipped section from reading green — passes plus the one legitimate skip (a machine genuinely low on headroom) must equal 16, else exit 1
- [x] Run the script and record pass/fail counts in this spec

Run through the run agent on 2026-11-27 19:03 (task `1795824225-spawn-a5769f4f-155254b0`, log
`/tmp/test-disk-pressure.log`, read by plan): **16 passed, 0 failed, 0 skipped, floor 16, exit 0** —
11 checks from before this spec and 5 new ones in two sections.

What the script is, so the count is not read as more: like the checks it joined, each new one runs
named Go tests (`TestDiskPressureFreed_…`, `TestCleanupStaleReportsBytesFreed…`,
`TestLargestFootprintEntries…`, `TestCheckDiskPressure_RepeatedRemovalErrors…`, the
`TestDiskPressureBackoff_…` set) and asserts how many ran. No daemon is started and no real pressure
cycle runs — deliberately, since a real cycle's `CleanupStale` deletes other sessions' artifacts on
the machine running it. `checkDiskPressure` is driven end to end only through its test seams.

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

Complete

Closed out 2026-11-27 by `55-defect-backlog-loop` run `1795811929` at 32/32. What shipped: a pressure
cycle is judged on bytes freed, the stale stage's included; three zero-byte cycles log
`disk-pressure-unclearable` once and hold the ladder until the footprint moves (by at least 1 % and
1 MiB — the worker's reading of Decision 3) or 30 minutes pass. What did not: no `/tmp` Go-cache
removal stage, no block-based footprint and no extra gate on stage 4 — the re-measure found no such
cache on either Codex road, so four steps and one criterion are ticked as **not built**. Left with
the user: 27 `bus` tests fail under the Codex `workspace-write` opt-out
([Findings for Phase 3](#findings-for-phase-3)), unfiled.

Activated 2026-11-27 by `55-defect-backlog-loop` run `1795811929` (`select` node), at 1/32: one
criterion ticked as already shipped. Reached 32/32 the same day: Phases 1 (pin), 2 (accounting and
back-off), 3 (re-measure), 5 (docs) and 6 (integration test) are complete; Phase 4 is ticked as
**not built**, since the re-measure found no `/tmp` Go cache on either road. Unparked 2026-11-24 on
the user's decisions.
