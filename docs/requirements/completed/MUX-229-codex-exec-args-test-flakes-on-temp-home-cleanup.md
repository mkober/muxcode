# MUX-229: `TestCodexBuildExecArgs_NoShimWithoutInstallDir` Flakes on Its Temp-HOME Cleanup

**Tracking:** [mkober/muxcode#218](https://github.com/mkober/muxcode/issues/218)

A Codex exec-args test failed `./test.sh` with none of its assertions failing: the test's temporary
`HOME` could not be removed because a `Library` directory inside it was not empty. Something the test
started was still writing under that `HOME` when the test returned. The failure passed on rerun and
cost a graph test node a fix iteration.

## Context

### Observed (2026-11-14 17:48, run `1794626267-55-defect-backlog-loop-15547e2e`, MUX-156 Phase 1's test node)

Reported by edit on the user's standing instruction to file defects found along the way. Plan read
the test log, the test agent's history row and the code path.

| Evidence | Detail |
|----------|--------|
| History row | `./test.sh` at 17:48:21, exit 1: `muxcode: failed (go test)`, `FAIL bus: TestCodexBuildExecArgs_NoShimWithoutInstallDir` — the name is there because of [MUX-215](../backlog/MUX-215-non-reproducible-test-sh-failures-and-lost-failing-output.md)'s Phase 1 |
| Test log | `/tmp/muxcode-test-logs/20261114-174723-85976/muxcode.log`, lines 5649–5651: `testing.go:1619: TempDir RemoveAll cleanup: unlinkat …/TestCodexBuildExecArgs_NoShimWithoutInstallDir406626677/001/Library: directory not empty`, then `--- FAIL` after 0.01 s |
| The assertion | Did not fail. The only failure line is the cleanup's |
| The change under test | MUX-156 Phase 1 touched `cmd/inbox_orphan_test.go` only; nothing in `bus/` |
| Earlier runs | Edit's account: the test passed in every earlier `./test.sh` run that day. Plan did not re-count them |

### Mechanism — verified

- `bus/provider_codex_test.go:617` `codexTestHome` — sets `HOME` to `t.TempDir()`. Eleven call sites
  in that file use it.
- `bus/provider_codex.go:398–399` `goToolchainRoots` — `exec.Command("go", "env", "GOCACHE",
  "GOMODCACHE")`, reached through `codexWritableRoots` for the build and test roles. The child
  inherits the temporary `HOME`.
- **The writer is the Go command's telemetry.** Established 2026-11-14 by the MUX-156 worker against
  the Go 1.27.4 source (`/tmp/mux156-codex-flake-cause.md`, relayed by edit); plan re-read the same
  lines in the installed toolchain, `cmd/vendor/golang.org/x/telemetry`:

  | Step | Source |
  |------|--------|
  | With no mode file, the telemetry mode is `local` | `internal/telemetry/dir.go`: an unreadable mode file returns `"local"` ("default") |
  | Any mode but `off` opens the counter file and may start a detached child | `start.go` `parent`: returns early only when `mode == "off"`; otherwise `counter.Open()`, then the child |
  | Both write under the user config directory | `os.UserConfigDir()/go/telemetry`, which on darwin is `$HOME/Library/Application Support/go/telemetry` |
  | The child outlives `go env` | It is detached, so it can still be writing when `t.TempDir`'s `RemoveAll` runs: "directory not empty" |

  A fresh temporary `HOME` has no mode file, so every `go` run under it takes this path.

**Not measured:** how often the race is lost. The worker found this one occurrence in
`/tmp/muxcode-test-logs`; no reproduction loop was run, and no leftover tree was listed.

**A fix was tried by the worker and reverted** on edit's instruction, since it belongs here and not
in MUX-156: in `codexTestHome`, clear `XDG_CONFIG_HOME` and write `off` to
`os.UserConfigDir()/go/telemetry/mode` when that directory is under the temporary home. With the
mode `off`, `parent` returns before the counter and the child.

**Wider than this test.** Every test that pairs `codexTestHome` with a build- or test-role argument
build is exposed; this one lost the race. So is any test that runs `go` under a temporary `HOME` —
`bus/artifacts.go:165` also runs `go env GOCACHE`.

### Relationship to MUX-215

MUX-215 is parked waiting for a flaky `./test.sh` failure that can be named. This is one, caught by
the summary that spec added. It is a different test and a different cause from the candidate MUX-215
records (a leaked tmux wake goroutine, fixed in PR #196), and whether it is the test behind the
2026-11-13 failures cannot be known — that output is lost. It is filed here as its own defect, which
is what MUX-215's Phase 2 asks for; MUX-215's boxes are the user's to tick.

## Requirements

### Acceptance criteria

- [x] What writes under the temporary `HOME`, and why it outlives the test, is identified and recorded here with the leftover tree as evidence — identified from the Go source (see Mechanism); the leftover tree and the rate are still to capture
- [x] No process a test starts through `codexTestHome` writes under that `HOME` after the test returns
- [x] The affected tests run repeatedly (`-count` high enough to have caught the race, the number justified from Phase 1's measured rate) with zero cleanup failures — met on the race's **precondition**, not the failure: at `-count=20` the unfixed tree leaves a leftover tree every iteration and the fixed tree none, with zero cleanup failures. No count could be justified against the failure itself (2 in 87 suite runs, 0 in 25,800 isolated); see Results for Phase 3
- [x] Negative control: `TestCodexBuildExecArgs_NoShimWithoutInstallDir` still asserts no `BASH_ENV` without an install directory, and the build and test roles' exec args still carry the real Go cache roots in production
- [x] The fix is not a retry or a sleep around the cleanup

### Technical approach

The writer is known (Mechanism). Keep the `go` child from writing under the test's `HOME` at all,
by one of: telemetry switched off for the temporary home (a mode file holding `off`, the fix already
tried once; or `GOTELEMETRY` pinned through a `GOENV` file), or a test seam for the `go env` probe
(nil in production, as the `*http.Client` seams are) so the tests start no `go` child. A seam is the
strongest form, provided one test still exercises the real probe against a scratch environment that
outlives it. Whichever is chosen must cover the other `go env` caller's tests too
(`bus/artifacts.go`), or say why they are not exposed.

The production call is not changed in behaviour: with no seam set it runs `go env` exactly as now.

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/provider_codex.go` | `goToolchainRoots`, `codexWritableRoots`, `swiftPMCacheRoots` |
| `tools/muxcode/bus/provider_codex_test.go` | `codexTestHome` and its eleven call sites |
| `test.sh` | The summary that named the failure (MUX-215) |

## Implementation

### Phase 1: Find the writer

- [x] Reproduce in a loop that keeps the leftover tree on a cleanup failure; record the rate and the tree's contents here — the loop ran and did **not** reproduce it (0 in 25,800); the rate and the tree are recorded from the field instead (2 failures in 87 `./test.sh` runs; 171 leftover trees per leaking test)
- [x] Record which process wrote it and why it was still running after `go env` returned — the source reading is in Mechanism; confirm it against the leftover tree, and list which other tests run `go` under a temporary `HOME`

### Results for Phase 1

Recorded 2026-11-16 from the worker's findings file (`/tmp/mux229-p1-findings.md`); plan listed the
leftover trees in `$TMPDIR` itself, which changed one finding (below). No code changed — all
instrumentation ran on scratch copies. Go 1.27.4, darwin/arm64. Verified and ticked 2026-11-16: no
source file changed in this phase; `./build.sh` exit 0 at 14:26:48, `./test.sh` exit 0 at 14:28:22,
review at 14:29:08 with 0 must-fix, 0 should-fix, 1 nit (`/tmp/muxcode-review-1794857307.txt`) — the
nit against plan's wording of the Phase 3 control, fixed below.

**Reproduction and rate — the worker's.**

| Run | Result |
|-----|--------|
| Isolated loop (`/tmp/mux229-p1.sh` part 2, run task `1794855468-spawn-926e95e5-b530f0b9`, 21 min) | `TestCodex*` under a scratch `TMPDIR`, 6 rounds × `-count=50`: **0 cleanup failures in 25,800 test runs**, no leftover tree. Not reproducible in isolation |
| `go env GOCACHE` under 20 fresh homes (part 1) | Telemetry files under `HOME` at return: 20/20. A telemetry child still alive at the instant `go env` returned: 0/20. New files 2 s later: 0/20 — in isolation the child finishes almost at once |
| Field rate, `/tmp/muxcode-test-logs/*` | **2 cleanup failures in 87 logged `./test.sh` runs** (2026-11-14 17:48 and a second on 2026-11-16 12:55), both `TestCodexBuildExecArgs_NoShimWithoutInstallDir` — at 0.01 s the fastest of its group, so its `RemoveAll` runs soonest after `go env` returns. Parallel packages in the full suite are the plausible difference from the isolated loop |

**The leftover trees — corrected by plan.** The findings file names two, one per logged failure.
Listing `$TMPDIR` on 2026-11-16 14:3x found **171** `TestCodexBuildExecArgs_NoShimWithoutInstallDir*`
directories and **171** `TestCodexSandboxMode_OptOutAndPrecedence*` directories, every one holding the
same tree and none holding `upload.token`:

```
001/Library/Application Support/go/telemetry/local/weekends
001/Library/Application Support/go/telemetry/local/<counter file>
001/Library/Application Support/go/telemetry/upload/        (empty)
```

| Dated | `NoShimWithoutInstallDir` trees |
|-------|-------------------------------|
| 2026-11-12 | 1 |
| 2026-11-13 | 109 |
| 2026-11-14 | 43 |
| 2026-11-16 | 18 |

What that means, as far as the evidence goes:

- The two tests that leak are exactly the two the worker's instrumentation shows making **one** `go`
  exec under a temporary home; the four that make two leave nothing. Why one exec leaks and two do
  not is not established.
- A leftover tree with no `upload.token` is the child's writes alone, made **after** `RemoveAll` had
  already removed the parent's — `MkdirAll` recreates the test's own temp directory. So the child
  outliving the test is routine, not rare; it is a *test failure* only when the write lands while
  `RemoveAll` is still walking the directory (2 logged), and otherwise a silent leak into `$TMPDIR`
  (171 each).
- This is in tension with the isolated loop's "no leftover tree" under a scratch `TMPDIR`. Both are
  recorded as measured; the full-suite run is where the trees appear.
- It gives Phase 3 a **candidate** control: count leftover `TestCodex*` trees after a full `./bus`
  run — with the fix, none; with it reverted, some. It is not proven reachable: the 171 trees are
  history, and no fresh run has yet been shown to leave one (review's nit, 2026-11-16, against this
  bullet's first wording). The cleanup-failure count alone cannot discriminate (0 in 25,800), so this
  is the control worth trying first.

**The writer — the worker's, from the Go source.** The detached telemetry child `go env` starts
(`x/telemetry` `start.go`). With no mode file under a fresh home, `Mode()` is `local`; `parent()` runs
`counter.Open()` and — `cmd/go` passes `Upload: true`, and a fresh home has no `upload.token` younger
than 24 h — `acquireUploadToken()` succeeds and `startChild` daemonizes the child with
`cmd.Dir = LocalDir`, never waiting for it. `child()` calls `counter.Open()` itself (creating
`weekends` and its own counter file) and the uploader's `findwork` does `os.MkdirAll(uploaddir)` —
exactly the leftover tree.

**Tests that run `go` under a temporary home — the worker's.** From an instrumented scratch copy
(`goToolchainRoots` and `goCacheDir`, the only production code that execs `go`, logging `HOME`) over
the full `./bus ./cmd ./daemon`, run task `1794856997-spawn-926e95e5-7d93c840`: 25 `go` execs, **10
under a `t.TempDir` home, all in `codexTestHome` tests** in `bus/provider_codex_test.go`.

| Test | `go` execs |
|------|-----------|
| `TestCodexBuildExecArgs_EveryRoleGetsMuxcodeState` | 2 |
| `TestCodexBuildExecArgs_NoShimWithoutInstallDir` | 1 |
| `TestCodexBuildExecArgs_NoSwiftGrantsOffDarwin` | 2 |
| `TestCodexBuildExecArgs_PackageCachesInTmp` | 2 |
| `TestCodexBuildExecArgs_SwiftShimAndCacheOnDarwin` | 2 |
| `TestCodexSandboxMode_OptOutAndPrecedence` | 1 |

The other 15 run under the real home (the artifact tests, other `BuildExecArgs` callers): they write
the user's own telemetry directory and cannot race a temp-directory cleanup. So `bus/artifacts.go`'s
tests are not exposed, which answers the Technical approach's open point.

**For Phase 2.** Every racing call goes through `codexTestHome`. Turning telemetry off for that home
(a mode file holding `off`, which `parent()` checks before `counter.Open`) or a seam on
`goToolchainRoots` both remove the child. The earlier tried fix gave 615 passed, 0 failed for
`TestCodex*` ×5 (proc `1794696583-proc-ce44fef7`, 2026-11-14) — supportive only, since the failure
does not reproduce in isolation.

### Phase 2: Keep the child out of the test's HOME

- [x] Apply the mechanism Phase 1's finding selects; production behaviour unchanged with no seam set
- [x] Negative controls: the no-`BASH_ENV` assertion still holds; a test still proves the build and test roles receive the Go cache roots

### Results for Phase 2

Recorded 2026-11-16 from the worker's request; the diff and the check script's log were read by
plan. Test-only change, uncommitted. Verified and ticked 2026-11-16 once all three had run after the
last edit (14:30:31): `./build.sh` exit 0 at 14:42:03, `./test.sh` exit 0 at 14:43:29, review at
14:44:09 with 0 must-fix, 0 should-fix, 0 nits (`/tmp/muxcode-review-1794858213.txt`).

| Item | Result |
|------|--------|
| Mechanism chosen | **Telemetry off for the temporary home**, not a seam. Production code is untouched — the only file changed is `bus/provider_codex_test.go` — and the real `go env` probe still runs in every test that ran it before |
| The change | `codexTestHome` clears `XDG_CONFIG_HOME` and calls the new `goTelemetryOff`, which writes `go/telemetry/mode` holding `off` under `os.UserConfigDir()` — inside the temp home. With the mode `off`, `go` opens no counters and starts no child |
| New test | `TestCodexTestHomeStartsNoGoTelemetry`: after `codexTestHome` and a real `goToolchainRoots()` call, no `go/telemetry/local` directory exists under the home. Deterministic — it does not depend on losing a race. It fails if the probe returns nothing, so it cannot pass by not running `go`; it skips only when `go` is not on `PATH` |
| Negative controls, unchanged | `TestCodexBuildExecArgs_NoShimWithoutInstallDir` (no `BASH_ENV` without an install directory) and the test that the build and test roles get the Go caches from the real probe under the real home (`TestCodexBuildExecArgs_TestGetsGoCachesOnly`, `provider_codex_test.go:290`) |
| Not touched | `bus/artifacts.go`'s `go env` tests run under the real home (Phase 1) and are not exposed |
| Run | `/tmp/mux229-p2-check.sh`, proc `1794857791-proc-f3d40491` (run task `1794857784-spawn-926e95e5-6600c2be`): 7 passed, 0 failed (expected 7) — `TestCodex*` ×3 green with the new test 3/3 and not skipped; full `./bus` ok; with the `goTelemetryOff` call removed, only the new test fails |
| **Leftover trees** | After `TestCodex*` at `-count=20`: **0 with the fix, 20 without.** This answers review's Phase 1 nit — a fresh run without the fix does leave trees, one per iteration here, so the leftover-tree count is a reachable control for Phase 3 |
| An earlier run | Run task `1794857451…`: the same results, exit 1 only because the script's expected-count constant was wrong |

### Phase 3: Integration test

- [x] Create `scripts/test-codex-home-cleanup.sh`: run the `codexTestHome` tests uncached at the count Phase 1 justifies → zero `TempDir RemoveAll cleanup` failures
- [x] Negative control: a throwaway copy of the tree with the fix reverted reproduces the cleanup failure within the same count, or the script reports that it could not — a control that cannot fail proves nothing
- [x] Run the script, record pass/fail counts in this spec, and add it to the script index in `CLAUDE.md`

### Results for Phase 3

Recorded 2026-11-16 from the worker's request; the script's header and its run log were read by
plan. Uncommitted. Verified and ticked 2026-11-16 once all three had run after the last edit
(14:48:13): `./build.sh` exit 0 at 14:48:50, `./test.sh` exit 0 at 14:49:08, review at 14:49:46 with
0 must-fix, 0 should-fix, 1 nit (`/tmp/muxcode-review-1794858552.txt`) — the isolation wording, in
the first row below.

| Item | Result |
|------|--------|
| The script | `scripts/test-codex-home-cleanup.sh` — two copies of the tree in a scratch directory (no `.git`), each with its own scratch `TMPDIR`. It is **not** hermetic about the home directory: the script keeps the real `HOME`, so the Go build and module caches are written there, and so is Go's own telemetry for the `go test` runs themselves (review's nit, 2026-11-16, against the header's "nothing outside the work dir is written but the Go build cache"; the script header still says that). Runs the `TestCodex*` tests uncached at `-count=20` (`MUXCODE_CODEX_HOME_COUNT` overrides). About 3 minutes |
| A — the fixed tree | All `TestCodex*` pass; `TestCodexTestHomeStartsNoGoTelemetry` ran 20/20, not skipped; zero `TempDir RemoveAll cleanup` failures; no `TestCodex*` tree left in `TMPDIR` |
| B — control, `goTelemetryOff`'s call removed | `TestCodexTestHomeStartsNoGoTelemetry` fails, and 20 `TestCodex*` trees are left behind — the child outliving the test, which the cleanup race needs |
| The race itself | **Not reproduced** in the control's 20 runs. The script prints that as a note and does not count it as a pass — the outcome the Phase 3 control step allows for ("or the script reports that it could not") |
| Run | Proc `1794858321-proc-110ba99e` (run task `1794858316-spawn-926e95e5-4b949fc1`): 6 passed, 0 failed, floor 6, exit 0 |
| Docs | `CLAUDE.md` script-index entry `test-codex-home-cleanup` (the worker) |

**What the count of 20 does and does not justify.** The repeated-run criterion asks for a count "high
enough to have caught the race, the number justified from Phase 1's measured rate". Measured against
the **cleanup failure**, no affordable count qualifies: 2 in 87 full-suite runs, 0 in 25,800 isolated
ones. Measured against the **precondition** — the child writing under the home after the test returns
— the rate without the fix is one leftover tree per iteration (20 of 20, here and in Phase 2), so 20
is ample, and with the fix it is 0 of 20. The script proves the precondition is gone, which makes the
failure impossible by mechanism; it does not show the failure rate itself falling, because that rate
was never observable in a loop.

## Notes

- Filed 2026-11-14 by plan from `/tmp/defect-codex-home-cleanup-flake.md` (edit), on the user's standing instruction to file defects found along the way.
- Not parked: the mechanism is chosen from Phase 1's evidence among options that all leave production behaviour alone; no user decision is owed.
- Root cause recorded 2026-11-14, the same day, replacing the filing's hypothesis (edit's request; the MUX-156 worker's finding, re-read by plan in the Go 1.27.4 source).
- The Phase 3 control may prove unreachable if the race is rare. The step says what to do then rather than pretending it passed.

## Status

**Complete** — activated and closed 2026-11-16 by `55-defect-backlog-loop` run `1794626267-55-defect-backlog-loop-15547e2e` (Defects rank 14 then; ranks 1–13 were parked). 12/12: Phases 1–3 complete and all five criteria met. Uncommitted at close-out; the run's ship step commits it. The repeated-run criterion is met on the race's precondition (no leftover tree in 20 iterations, against one per iteration unfixed), not on the cleanup failure itself, which never reproduced in a loop — said on the box. Checked against `f87cb3d` at activation: every reference in Mechanism still holds at the same lines — `codexTestHome` at `provider_codex_test.go:617` with eleven call sites, `goToolchainRoots` at `provider_codex.go:398–399`, the second `go env` caller at `artifacts.go:165` — and nothing in the test file mentions telemetry, so the reverted fix is not in the tree.

History: filed 2026-11-14.
