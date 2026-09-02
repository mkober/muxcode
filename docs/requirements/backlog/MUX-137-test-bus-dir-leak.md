# PreLaunch Tests Leak Real Bus Directories Into `/tmp`

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

Same defect class as [`MUX-004`](../completed/MUX-004-lifecycle-log-test-leak.md), which fixed the
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

- [ ] A full `./test.sh` run creates **zero** `/tmp/muxcode-bus-*` directories
- [ ] The three PreLaunch tests isolate through `SetBusDirBase()` / `ResetBusDirBase()`, the
      documented mechanism, and no longer reference `BUS_DIR_BASE`
- [ ] Real (non-test) sessions continue to resolve to `/tmp/muxcode-bus-<session>` unchanged
- [ ] A test that forgets to pin the override is **caught rather than silently leaking** — the net
      does not depend on every future author remembering
- [ ] The 11 existing leaked directories are swept, and the sweep does not touch live-session
      directories
- [ ] An automated regression script proves a full suite run leaves `/tmp` untouched

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

### Phase 1: Pin the leak, then fix the call sites

- [ ] Add a test that **fails on the current code**: run the PreLaunch path under a pinned override
      and assert no `/tmp/muxcode-bus-test-*` directory is created
- [ ] Confirm it fails before the fix — a test that cannot go red here proves nothing, since the
      existing assertions pass in both states
- [ ] Replace `os.Setenv("BUS_DIR_BASE", …)` with `SetBusDirBase(dir)` + `t.Cleanup(ResetBusDirBase)`
      in all three tests
- [ ] Confirm the new test goes green and the three tests still assert their original behaviour

### Phase 2: Package-level net

- [ ] Add `TestMain` to `bus` pinning `busDirOverride` to a per-run temp dir, removed afterward
- [ ] Audit other packages (`cmd`, `daemon`, `tui`) for tests reaching `Init()` / `BusDir()`; pin
      where needed and record any package deliberately left unpinned, with the reason
- [ ] Negative control: a deliberately unpinned test lands in the temp root, **not** `/tmp`
- [ ] Optional hardening — check that every `MUXCODE_*` / `BUS_*` env name set in test code is read
      by non-test code; report offenders

### Phase 3: Sweep existing leaks

- [ ] Remove the 11 `/tmp/muxcode-bus-test-prelaunch-*` directories
- [ ] Verify the sweep pattern cannot match a live session directory
- [ ] Record the pre-sweep inventory in the spec so the count is auditable

### Phase 4: Integration test

- [ ] Create `scripts/test-bus-dir-leak.sh` with end-to-end verification
- [ ] Snapshot `/tmp/muxcode-bus-*` before the run, run the full suite, snapshot after → assert the
      sets are identical
- [ ] Negative control: with the fix reverted, the script **fails** — pinned with the verbatim
      failure text, so a green run proves the check executed
- [ ] Assert a live-session directory present before the run survives it untouched
- [ ] Coverage floor equal to the achievable maximum, so a skipped section cannot report green
- [ ] Run the script and verify all checks pass

## Status

Draft
