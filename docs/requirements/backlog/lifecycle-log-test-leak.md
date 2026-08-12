# Lifecycle Log Test Leak

Test runs deposited real lifecycle log files into the user's live install. `LifecycleLogDir()` resolved unconditionally to `~/.config/muxcode/logs`, lifecycle logging is a side effect of many code paths, and it writes one persistent file per session name — so every test run using synthetic session names left permanent files behind. The fix (an env override honored by `LifecycleLogDir()`, pinned by `TestMain` in each package) has **already shipped** alongside the disk-pressure change; the automated regression test is outstanding.

## Context

### Observed failure (2026-08-12)

- **41,789 stray `test-*.log` files (~169 MB)** had accumulated under `~/.config/muxcode/logs`, alongside only 21 real session logs.
- Tests use synthetic session names (`test-<random>`, `test-cron-exec`, `test-webhook-...`), and every test run deposited more.

### Root cause

- `LifecycleLogDir()` (`bus/lifecycle.go`) resolved unconditionally to `~/.config/muxcode/logs` — no way to redirect it.
- Lifecycle logging is a side effect of many code paths, so tests exercising those paths logged for real, one persistent file per session name.

### Cross-links

Found while working on [disk-pressure-wrong-filesystem](../drafts/disk-pressure-wrong-filesystem.md) — the same investigation into lifecycle-log health (disk-pressure spam had collapsed retained history) surfaced this second way the log directory degrades.

## Requirements

### Acceptance criteria

- [x] A test run writes zero files into `~/.config/muxcode/logs` — lifecycle logging in tests is redirected to a temp dir and cleaned up afterward
- [x] The redirect is honored by `LifecycleLogDir()` itself, so every lifecycle-logging code path is covered, not just the ones tests call directly
- [x] Real (non-test) sessions keep logging to `~/.config/muxcode/logs` unchanged
- [ ] An automated regression test asserts that a test run leaves `~/.config/muxcode/logs` untouched

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

## Implementation

### Phase 1: Redirect lifecycle logging in tests

- [x] `LifecycleLogDir()` honors `MUXCODE_LIFECYCLE_LOG_DIR`
- [x] `TestMain` in `bus`, `cmd`, `daemon` packages pins the override to a per-run temp dir and removes it afterward
- [x] `bus/lifecycle_test.go`: the eight `HOME`-isolated tests also pin `MUXCODE_LIFECYCLE_LOG_DIR` so they don't share a directory
- [x] Verify: delete the stray files, run a fresh uncached `bus` test pass, confirm zero recreated

### Phase 2: Integration test

- [ ] Add an automated regression test asserting that a test run leaves `~/.config/muxcode/logs` untouched (e.g. snapshot the dir listing before/after a package test pass, or a `scripts/test-lifecycle-log-leak.sh` check)
- [ ] Run it and verify it passes — and that it fails if the `MUXCODE_LIFECYCLE_LOG_DIR` pin is removed

## Status

In Progress
