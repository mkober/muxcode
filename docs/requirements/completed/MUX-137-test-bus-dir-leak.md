# PreLaunch Tests Leak Real Bus Directories Into `/tmp`

**Tracking:** [mkober/muxcode#64](https://github.com/mkober/muxcode/issues/64)

Three `PreLaunchSetup` tests isolate themselves with `os.Setenv("BUS_DIR_BASE", dir)` — an
environment variable **no production code reads**. `BusDir()` honors the package variable
`busDirOverride`, set only through `SetBusDirBase()`, so the override never takes effect and
`Init()` creates **11 real `/tmp/muxcode-bus-test-prelaunch-*` directories** on every suite run.

The tests are not missing isolation. They call an isolation mechanism that does not exist, and pass
either way — which is why this survived review.

Tracking: _(no GitHub issue yet)_

## Context

### Root cause, verified in code

| Step | Code | Effect |
|---|---|---|
| 1 | `BusDir()` (`bus/config.go:124`) | returns `busDirOverride + "/muxcode-bus-" + session` **only** when `busDirOverride != ""`; otherwise `/tmp/muxcode-bus-<session>` |
| 2 | `busDirOverride` (`bus/config.go:107`) | package variable, written **only** by `SetBusDirBase()` / `ResetBusDirBase()` (`:113`, `:118`) |
| 3 | `os.Setenv("BUS_DIR_BASE", dir)` in the tests | **nothing reads this name** — a grep for non-test readers of `BUS_DIR_BASE` returns empty |
| 4 | `Init(session, dir)` | creates the directory `BusDir()` returned — the real `/tmp` path |

The variable's own doc comment states the intended contract, which makes the divergence explicit:

> `busDirOverride` … Used by tests to isolate bus directories in `t.TempDir()` instead of polluting
> the real `/tmp/muxcode-bus-*` namespace. Set via `SetBusDirBase` / `ResetBusDirBase`.

**The rest of the package already does this correctly** — `dedup_test.go`, `remote_test.go` and
`disk_pressure_test.go` all assign `busDirOverride` directly and restore it on cleanup. Only the
PreLaunch tests reach for the env var.

### The 11 directories

| Test | Line | Sessions |
|---|---|---|
| `TestPreLaunchSetup_AutoStartupMessage` | `launch_test.go:642` | `test-prelaunch-auto` |
| `TestPreLaunchSetup_EditStartupMessage` | `launch_test.go:681` | `test-prelaunch-edit` |
| `TestPreLaunchSetup_AllRolesGetStartupMessage` | `launch_test.go:716-720` | `test-prelaunch-{build,test,commit,review,deploy,plan,run,watch,serve}` (9) |

The `t.TempDir()` each test creates is real and is cleaned up — it is simply never used as the bus
root, so it is cleaned up empty while the real work lands in `/tmp`.

### Why a passing test proves nothing here

Each test asserts on `Peek(session, role)`, which resolves through the same `BusDir()`. Test and
assertion agree on the wrong directory, so the suite is green whether isolation works or not. No
existing assertion can distinguish the two states — that is the gap Phase 1 closes.

### Provenance of this report

The leak count and mechanism are **verified by code inspection**, and the count of 11 derived here
independently matches the 11 reported from an observed `./test.sh` run. This spec's author did not
execute the suite (that is the test/run agent's role), and `/tmp` currently holds no
`test-prelaunch-*` directories — consistent with a sweep since the last run, not with absence of the
defect.

### Cross-links

Same defect class as [`MUX-004`](./MUX-004-lifecycle-log-test-leak.md), which fixed the
**lifecycle-log** variant: tests writing real files into the user's live install because a path
helper had no test-visible override. That spec's shape is the template here — a per-package
`TestMain` pin, plus `scripts/test-lifecycle-log-leak.sh` as the regression proof.

Two differences worth carrying:

- MUX-004 had to **add** an override (`MUXCODE_LIFECYCLE_LOG_DIR`). Here the override already exists
  and is documented; only the call sites are wrong. The fix is smaller, the detection harder.
- MUX-004 recorded that `tui` leaked nothing "only because no tui test triggers lifecycle logging —
  luck, not design." The same reasoning applies to any future test calling `Init()` without pinning
  the override, which is why Phase 2 adds a net rather than only fixing three call sites.

## Requirements

### Acceptance criteria

- [x] A full `./test.sh` run creates **zero** `/tmp/muxcode-bus-*` directories — and none in the real
      state base, and no memory file under `tools/` (script section 1, uncached)
- [x] The three PreLaunch tests isolate through `SetBusDirBase()` / `ResetBusDirBase()`, the
      documented mechanism, and no longer reference `BUS_DIR_BASE` — and the seven other tests that
      set or read it; no test code sets or reads the name now
- [x] Real (non-test) sessions continue to resolve to `/tmp/muxcode-bus-<session>` unchanged — read
      as "resolution is unchanged": the real base is `$XDG_STATE_HOME/muxcode/bus` since MUX-208, and
      this spec changed no production file, only tests and a script
- [x] A test that forgets to pin the override is **caught rather than silently leaking** — the net
      does not depend on every future author remembering — it lands in the run's temp root for both
      bus and session memory (`TestMain` pins, four control tests); the global memory dir is not
      covered, see Results for Phase 2
- [x] The 11 existing leaked directories are swept, and the sweep does not touch live-session
      directories — the 11 were already gone (inventory of zero recorded); the one leak that did exist
      was removed and both live session directories and both real summaries are untouched
- [x] An automated regression script proves a full suite run leaves `/tmp` untouched —
      `scripts/test-bus-dir-leak.sh`, 15 of 15

### Technical approach

1. **Fix the call sites** — replace `os.Setenv("BUS_DIR_BASE", dir)` / `defer os.Unsetenv(...)` with
   `SetBusDirBase(dir)` / `t.Cleanup(ResetBusDirBase)` in the three tests.
2. **Add a package-level net** — a `TestMain` in `bus` (and any package whose tests call `Init()`)
   that points `busDirOverride` at a per-run temp dir, mirroring MUX-004's design so a forgotten pin
   degrades to the temp root instead of `/tmp`.
3. **Sweep** existing `test-prelaunch-*` directories, matching only the synthetic prefix so live
   sessions are never touched.

**A phantom-identifier check is the generalizable half.** The root cause is an env var set by tests
and read by nothing. The same shape produced a separate defect the same week (docs teaching a model
id absent from the catalog). A cheap guard: assert every `MUXCODE_*` / `BUS_*` name set in test code
is read somewhere in non-test code.

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/config.go` | `BusDir()` (:124), `busDirOverride` (:107), `SetBusDirBase`/`ResetBusDirBase` (:113,:118) — the mechanism, unchanged by this fix |
| `tools/muxcode/bus/launch_test.go` | The three leaking tests (:642, :681, :716) |
| `tools/muxcode/bus/dedup_test.go`, `remote_test.go`, `disk_pressure_test.go` | The correct pattern, already in-repo |
| `tools/muxcode/bus/main_test.go` | `TestMain` net (new, Phase 2) |
| `scripts/test-bus-dir-leak.sh` | Regression proof (new, Phase 3) |
| `docs/requirements/completed/MUX-004-lifecycle-log-test-leak.md` | Precedent and template |

## Implementation

### Findings at activation (2026-11-14)

Checked against the tree by plan before the first phase. [MUX-208](./MUX-208-durable-bus-state-in-tmp-deleted-by-macos-tmp-cleaner.md) moved the bus out of `/tmp` and built most of this spec's net on the way; read the phases through this table.

| The spec says | The tree says | Consequence for the work |
|---------------|---------------|--------------------------|
| The bus lives at `/tmp/muxcode-bus-<session>` and an unpinned test leaks there | The bus base is `$XDG_STATE_HOME/muxcode/bus` (`BusDirBase`, `bus/config.go`); nothing resolves to `/tmp` | "Zero `/tmp/muxcode-bus-*` directories" already holds. The criterion to prove is the wider one: a suite run leaves both `/tmp/muxcode-bus-*` **and** the real `~/.local/state/muxcode/bus` unchanged |
| `bus` has no `TestMain`; Phase 2 adds one pinning `busDirOverride` | `bus`, `cmd`, `daemon` and `tui` each have a `TestMain` pinning `XDG_STATE_HOME` to the run's temp root (MUX-208); `TestDefaultBusBase_IsolatedFromRealState` fails if the pin is removed | Phase 2's first three steps are checks that this net holds and has its negative control, not new code — unless the audit finds a gap |
| Three tests set the dead `BUS_DIR_BASE` (`launch_test.go:642`, `:681`, `:716`) | Still dead (no non-test reader), and there are more sites now: `launch_test.go:828`, `:868`, `:907`, `:1061`; `launch_reason_test.go:16`, `:106`; `launcher_test.go:572` | Phase 1's call-site fix is the live work, over every site, not three. These tests are isolated today only by the `TestMain` pin, never by what they wrote |
| A test "fails on the current code" by leaking into `/tmp` | With the net in place no call site can leak into `/tmp`, fixed or not | Phase 1's red test has to assert something the dead variable actually changes — that each test's bus directory is under the `t.TempDir()` it created, not merely somewhere under the package temp root |
| 11 leaked `/tmp/muxcode-bus-test-prelaunch-*` directories to sweep | None exist in `/tmp` or under the real state directory today | Phase 3 is an inventory of zero, recorded; the pattern check still applies |
| Phase 4's negative control is "the fix reverted" | Reverting the call sites cannot make a `/tmp` snapshot differ | The control that can go red is the `TestMain` pin removed (or the override unset), in a scratch copy of the tree |
| `Tracking: _(no GitHub issue yet)_` in the body | Issue #64 is linked on the `**Tracking:**` line under the title | The body line is stale |

### Phase 1: Pin the leak, then fix the call sites

- [x] Add a test that **fails on the current code**: run the PreLaunch path under a pinned override
      and assert no `/tmp/muxcode-bus-test-*` directory is created — as `assertIsolatedBus`
      (`testutil_test.go`), called inside each repaired test rather than as a standalone test: the bus
      and the memory `Init` seeds must sit under the test's own directory
- [x] Confirm it fails before the fix — a test that cannot go red here proves nothing, since the
      existing assertions pass in both states — two mutations, each failing at the repaired call site
      (proc `1794682910-proc-78cc7419`)
- [x] Replace `os.Setenv("BUS_DIR_BASE", …)` with `SetBusDirBase(dir)` + `t.Cleanup(ResetBusDirBase)`
      in all three tests — in all ten, through `useTempBusDir`
- [x] Confirm the new test goes green and the three tests still assert their original behaviour —
      `./test.sh` 2 of 2 modules

### Results for Phase 1 (2026-11-14)

Verified by plan against the tree, tests only. Last source edit 1794682879, then the mutation run (1794682910), the build (1794682970), `./test.sh` (2 of 2 modules, 1794683035) and the review (0 must-fix, 0 should-fix, 0 nits, `/tmp/muxcode-review-1794683035.txt`). The worker's phase report describes the first round; the tree is the second.

| Item | State |
|------|-------|
| Dead `BUS_DIR_BASE` sites | Ten tests, not three: 3 PreLaunch and 4 `RunAgentLaunch` in `launch_test.go`, 2 in `launch_reason_test.go`, `TestNeedsWakeUp` in `launcher_test.go`. All now call `useTempBusDir`, which returns its directory, and pass it to `Init`. The name survives only in two comments in `testutil_test.go` |
| A second leak, found here | Five of those tests passed `Init` an empty memory dir, so `shared.md` was written to the working directory's `.muxcode/memory` — `tools/muxcode/bus/.muxcode/memory` in the source tree (gitignored). Left for the sweep phase. **Sweep boundary, verified by edit:** only `tools/muxcode/bus/.muxcode/memory/shared.md` (0 bytes) is a test leak. The `edit.md` beside it, and `tools/muxcode/.muxcode/memory/edit.md`, are real session summaries written by `session compact` run from those directories — they are kept, and a sweep step that deletes them is not ticked |
| First review round | A standalone `TestPreLaunchSetupLandsInItsOwnBusDir` duplicated the setup instead of guarding the repaired tests; replaced by `assertIsolatedBus` inside each of them |
| Mutation A | `TestPreLaunchSetup_AutoStartupMessage` reverted to `t.TempDir` + `BUS_DIR_BASE` → fails "bus … is outside the test's directory" |
| Mutation B | `TestRunAgentLaunch_SetsAgentRole`'s `Init` reverted to an empty memory dir → fails "Init seeded no memory under the test's directory" |
| Carried to Phase 2 | The worker notes `Init` also `MkdirAll`s `GlobalMemoryDir` — the audit should say where that lands under test |

### Phase 2: Package-level net

- [x] Add `TestMain` to `bus` pinning `busDirOverride` to a per-run temp dir, removed afterward —
      already present from MUX-208, pinning `XDG_STATE_HOME` rather than `busDirOverride`; this phase
      added a `BUS_MEMORY_DIR` pin beside it
- [x] Audit other packages (`cmd`, `daemon`, `tui`) for tests reaching `Init()` / `BusDir()`; pin
      where needed and record any package deliberately left unpinned, with the reason — recorded below
- [x] Negative control: a deliberately unpinned test lands in the temp root, **not** `/tmp` —
      `TestDefaultBusBase_IsolatedFromRealState`, `TestDefaultMemoryDir_IsolatedFromSourceTree`, and
      `TestMainIsolatesBusAndMemory` in `cmd` and `daemon`
- [x] Optional hardening — check that every `MUXCODE_*` / `BUS_*` env name set in test code is read
      by non-test code; report offenders — a one-time audit by the worker, not an automated guard;
      one offender found and fixed

### Results for Phase 2 (2026-11-14)

Verified by plan against the tree, tests only. Last source edit 1794683273, then the build (1794683323), `./test.sh` (2 of 2 modules, 1794683388) and the review (0 must-fix, 0 should-fix, 0 nits, `/tmp/muxcode-review-1794683388.txt`).

| Item | State |
|------|-------|
| Bus-directory net | Already held: `bus`, `cmd`, `daemon` and `tui` `TestMain`s pin `XDG_STATE_HOME`; `TestDefaultBusBase_IsolatedFromRealState` is the control |
| Gap found: memory | `MemoryDir`'s default is relative to the working directory, so an unpinned test wrote into the package source tree. `BUS_MEMORY_DIR` is now pinned in the `bus`, `cmd` and `daemon` `TestMain`s (`main_test.go`) |
| Left unpinned, deliberately | `tui` — its tests call no `Init` and write no memory or bus state. The root package has no bus tests |
| Phantom-env audit | One real offender: `BUS_DIR_PREFIX` in `agent_health_test.go`, read by nothing. `TestAgentStoppedMarkerRoundTrip` never called production code; it is rewritten through `MarkAgentStopped`/`IsAgentStopped`/`ClearAgentStopped` with an unaffected-role control. The other hits are dynamic names or `env_set` fixtures |
| Remaining gap, not changed | `GlobalMemoryDir` is still the real `~/.config/muxcode/memory` under test: `Init` creates it and tests read it. Outside this spec's criteria; filed as [MUX-224](../backlog/MUX-224-tests-use-the-real-global-memory-dir.md). The cwd-relative default that stranded the real `edit.md` summaries is [MUX-223](../backlog/MUX-223-project-memory-resolves-against-the-callers-cwd.md) |

### Phase 3: Sweep existing leaks

- [x] Remove the 11 `/tmp/muxcode-bus-test-prelaunch-*` directories — none existed to remove; the one
      test leak found, the empty `tools/muxcode/bus/.muxcode/memory/shared.md`, was removed instead
- [x] Verify the sweep pattern cannot match a live session directory
- [x] Record the pre-sweep inventory in the spec so the count is auditable — below

### Results for the sweep (2026-11-14)

Verified by plan against the tree after the sweep. No source file changed in this phase (last source edit 1794683273); the build (1794683519), `./test.sh` (2 of 2 modules, 1794683588) and the review (0 must-fix, 0 should-fix, 0 nits, `/tmp/muxcode-review-1794683588.txt`) followed.

| Pre-sweep inventory (2026-11-14 14:11, the worker's) | Count |
|------------------------------------------------------|-------|
| `/tmp/muxcode-bus-*` | 0 |
| `test-prelaunch` directories under `~/.local/state/muxcode/bus` | 0 — it holds only the live `muxcode-bus-muxcode` and `muxcode-bus-tibo` |
| The spec's 11 directories | Gone before this run; MUX-208 moved the bus, and nothing recreated them |
| Test leaks in the source tree | 1 — `tools/muxcode/bus/.muxcode/memory/shared.md`, 0 bytes, dated 2026-10-12 |

| Check | Result |
|-------|--------|
| Pattern safety | `muxcode-bus-test-prelaunch-*` matches neither live session (`muxcode`, `tibo`); it can match only a session named with the synthetic `test-prelaunch-` prefix |
| Removed | The empty `shared.md` only, guarded on still being empty |
| Kept, confirmed present by plan | `tools/muxcode/bus/.muxcode/memory/edit.md` (1161 bytes), `tools/muxcode/.muxcode/memory/edit.md` (5524 bytes) and its `edit/` archive — real session summaries, see MUX-223 |
| Not recreated | The suite ran after the sweep (14:13) and `bus/.muxcode/memory` still holds only `edit.md` — the Phase 2 pin at work |

### Phase 4: Integration test

- [x] Create `scripts/test-bus-dir-leak.sh` with end-to-end verification
- [x] Snapshot `/tmp/muxcode-bus-*` before the run, run the full suite, snapshot after → assert the
      sets are identical — and the real state base and every memory file under `tools/`, with the
      suite forced uncached
- [x] Negative control: with the fix reverted, the script **fails** — pinned with the verbatim
      failure text, so a green run proves the check executed — in a throwaway copy with the call
      sites reverted **and** the `TestMain` pins removed; both `LEAK` lines are matched verbatim
- [x] Assert a live-session directory present before the run survives it untouched — a seeded
      seven-file sentinel bus in the real state base, compared by checksum manifest, with mutation and
      deletion controls
- [x] Coverage floor equal to the achievable maximum, so a skipped section cannot report green — 15
- [x] Run the script and verify all checks pass — 15 passed, 0 failed, floor 15 (proc
      `1794684840-proc-7fb0281d`)

### Results for the integration phase (2026-11-14)

Verified by plan against the tree and the proc log. The script's last edit (1794684827) precedes its run (1794684840, exit 0), then the build (1794684947), `./test.sh` (2 of 2 modules, 1794685014) and the review (0 must-fix, 0 should-fix, 0 nits, `/tmp/muxcode-review-1794685014.txt`). The worker's phase report describes the first round (11 checks); the tree is the third.

| Section | Checks | What it shows |
|---------|--------|---------------|
| 1. Full `./test.sh`, uncached | 10 | Suite completes (2 of 2 modules); 6 packages executed, 0 cached; no `/tmp/muxcode-bus-*` directory, no bus directory in the real state base and no memory file under `tools/` is created; the sentinel live bus survives byte for byte; an untouched copy passes, a mutated inbox file and a deleted file are each rejected |
| 2. Control, fix reverted in a throwaway copy | 5 | With one PreLaunch site back on `BUS_DIR_BASE`, one `Init` given an empty memory dir and the `TestMain` pins removed, the script reports `LEAK <work>/fake-state/muxcode/bus/muxcode-bus-test-prelaunch-auto` and `LEAK <work>/tree/muxcode/bus/.muxcode/memory/shared.md` |

| Review round | Finding | Resolution |
|--------------|---------|------------|
| First (`1794684714`) | A cached `go test` result would let the leak check pass without running anything | `-count=1` appended to `GOFLAGS`; package logs captured and a cached result rejected |
| First | "Live session dirs survive" checked existence only | The sentinel bus and its checksum manifest, with mutation and deletion controls |
| Between rounds | The graph's test node expired at 600 s on a passing suite — the test agent's reply carried a mangled `--reply-to` | No code defect here; filed as [MUX-225](../backlog/MUX-225-reply-to-unknown-id-accepted-silently.md) |

No production file changed in this spec: every change under `tools/` is a `*_test.go` file, plus the new script.

## Status

Complete — closed 2026-11-14 by `55-defect-backlog-loop` run `1794626267-55-defect-backlog-loop-15547e2e`, 23/23 (activated the same day from Defects rank 14; ranks 1–13 were parked).
