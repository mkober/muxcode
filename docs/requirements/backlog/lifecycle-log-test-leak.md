# Lifecycle Log Test Leak

Test runs deposited real lifecycle log files into the user's live install. `LifecycleLogDir()` resolved unconditionally to `~/.config/muxcode/logs`, lifecycle logging is a side effect of many code paths, and it writes one persistent file per session name — so every test run using synthetic session names left permanent files behind. The fix (an env override honored by `LifecycleLogDir()`, pinned by `TestMain` in each package) has **already shipped** alongside the disk-pressure change; the automated regression test (`scripts/test-lifecycle-log-leak.sh`) landed 2026-08-19.

## Context

### Observed failure (2026-08-12)

- **41,789 stray `test-*.log` files (~169 MB)** had accumulated under `~/.config/muxcode/logs`, alongside only 21 real session logs.
- Tests use synthetic session names (`test-<random>`, `test-cron-exec`, `test-webhook-...`), and every test run deposited more.

### Root cause

- `LifecycleLogDir()` (`bus/lifecycle.go`) resolved unconditionally to `~/.config/muxcode/logs` — no way to redirect it.
- Lifecycle logging is a side effect of many code paths, so tests exercising those paths logged for real, one persistent file per session name.

### Cross-links

Found while working on [disk-pressure-wrong-filesystem](disk-pressure-wrong-filesystem.md) — the same investigation into lifecycle-log health (disk-pressure spam had collapsed retained history) surfaced this second way the log directory degrades.

## Requirements

### Acceptance criteria

- [x] A test run writes zero files into `~/.config/muxcode/logs` — lifecycle logging in tests is redirected to a temp dir and cleaned up afterward
- [x] The redirect is honored by `LifecycleLogDir()` itself, so every lifecycle-logging code path is covered, not just the ones tests call directly
- [x] Real (non-test) sessions keep logging to `~/.config/muxcode/logs` unchanged
- [x] An automated regression test asserts that a test run leaves `~/.config/muxcode/logs` untouched

### Technical approach (as implemented)

- `LifecycleLogDir()` now honors `MUXCODE_LIFECYCLE_LOG_DIR`.
- `TestMain` in the `bus`, `cmd`, and `daemon` packages points it at a temp dir for the whole package run and removes it afterward.
- Eight tests in `bus/lifecycle_test.go` that previously isolated via `t.Setenv("HOME", ...)` now also pin `MUXCODE_LIFECYCLE_LOG_DIR`, because the new override takes precedence over `HOME` and they would otherwise share one directory and see each other's logs.

**Verification**: the 41,789 stray files were deleted, then the `bus` package ran a fresh uncached test pass and recreated **zero** of them.

### Key files

| File | Change |
|------|--------|
| `bus/lifecycle.go` | `LifecycleLogDir()` honors `MUXCODE_LIFECYCLE_LOG_DIR` |
| `bus/lifecycle_test.go` | Eight `HOME`-isolated tests now also pin `MUXCODE_LIFECYCLE_LOG_DIR` |
| `bus`, `cmd`, `daemon` packages | `TestMain` pins the override to a temp dir for the package run, removed afterward |
| `tui/main_test.go` | Added (Phase 2) — the `tui` package had 4 test files and no `TestMain` pin. It leaked nothing in practice only because no tui test triggers lifecycle logging — luck, not design; the leak would have returned silently the moment a tui test touched a logging path |
| `scripts/test-lifecycle-log-leak.sh` | Phase 2 regression test — 7 checks, redirected-HOME design (see Phase 2 notes) |

## Implementation

### Phase 1: Redirect lifecycle logging in tests

- [x] `LifecycleLogDir()` honors `MUXCODE_LIFECYCLE_LOG_DIR`
- [x] `TestMain` in `bus`, `cmd`, `daemon` packages pins the override to a per-run temp dir and removes it afterward
- [x] `bus/lifecycle_test.go`: the eight `HOME`-isolated tests also pin `MUXCODE_LIFECYCLE_LOG_DIR` so they don't share a directory
- [x] Verify: delete the stray files, run a fresh uncached `bus` test pass, confirm zero recreated

### Phase 2: Integration test

- [x] Add an automated regression test asserting that a test run leaves `~/.config/muxcode/logs` untouched (`scripts/test-lifecycle-log-leak.sh` — executable, 7 checks)
- [x] Run it and verify it passes — and that it fails if the `MUXCODE_LIFECYCLE_LOG_DIR` pin is removed

**Design decision — redirect HOME, don't snapshot the real dir.** The naive approach (snapshot `~/.config/muxcode/logs`, run the suite, diff) only detects a leak by allowing it to happen — it writes the very files it exists to prevent. Instead the suite runs under a throwaway `HOME`, so the HOME-derived log path is a temp dir: a lost pin dumps files where the test catches them, and the user's real install is never written to either way. The real dir is still snapshotted as a backstop confirming the redirect itself held. Mechanical detail worth keeping: `GOCACHE` / `GOMODCACHE` / `GOPATH` are resolved and exported **before** `HOME` is redirected — otherwise every run rebuilds from a cold cache in a throwaway dir, and a cache-write failure would masquerade as a test failure.

**How "fails if the pin is removed" is satisfied** — two ways, both in the script:

1. An explicit negative control runs first: an unpinned `muxcode init` under the fake `HOME` must produce a log file. If it does not, the script fails loudly — without that, a green run would prove nothing (code that stopped logging entirely would also show "no files appeared").
2. The suite check itself is the removal test: with the pins in place the run leaks 0 files; remove any package's `TestMain` pin and its synthetic session names land in the fake `HOME` and the check fails.

**Deliberate non-assertion — do NOT "fix" this later.** The script does **not** require the suite to be green under the redirected `HOME`. `TestOpenCodeModelsExist` reads the real `HOME`'s OpenCode model config and so fails under redirection for reasons unrelated to logging. Demanding greenness would make this script fail permanently on an unrelated test, and would tempt someone to weaken the leak check to restore green. What it asserts instead is **coverage**, since "0 leaks" is only meaningful if the suite actually ran — a build failure leaks nothing and looks identical to a clean pass. So it hard-fails on build failure or fewer than 3 packages executed, and reports unrelated failures as a non-fatal NOTE. Suite health belongs to `./test.sh`; this script owns the leak.

**Verification evidence (2026-08-19).** First run: 5 passed / 1 failed — the failure was the greenness assertion described above (`TestOpenCodeModelsExist`), not a leak; the leak check passed at 0 files. The assertion was replaced with the coverage guard and the `tui` pin was added. Second run: 7 passed / 0 failed.

## Status

Complete
