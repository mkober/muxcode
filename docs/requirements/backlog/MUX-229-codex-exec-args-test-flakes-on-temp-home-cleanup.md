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
| History row | `./test.sh` at 17:48:21, exit 1: `muxcode: failed (go test)`, `FAIL bus: TestCodexBuildExecArgs_NoShimWithoutInstallDir` — the name is there because of [MUX-215](./MUX-215-non-reproducible-test-sh-failures-and-lost-failing-output.md)'s Phase 1 |
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

- [ ] What writes under the temporary `HOME`, and why it outlives the test, is identified and recorded here with the leftover tree as evidence — identified from the Go source (see Mechanism); the leftover tree and the rate are still to capture
- [ ] No process a test starts through `codexTestHome` writes under that `HOME` after the test returns
- [ ] The affected tests run repeatedly (`-count` high enough to have caught the race, the number justified from Phase 1's measured rate) with zero cleanup failures
- [ ] Negative control: `TestCodexBuildExecArgs_NoShimWithoutInstallDir` still asserts no `BASH_ENV` without an install directory, and the build and test roles' exec args still carry the real Go cache roots in production
- [ ] The fix is not a retry or a sleep around the cleanup

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

- [ ] Reproduce in a loop that keeps the leftover tree on a cleanup failure; record the rate and the tree's contents here
- [ ] Record which process wrote it and why it was still running after `go env` returned — the source reading is in Mechanism; confirm it against the leftover tree, and list which other tests run `go` under a temporary `HOME`

### Phase 2: Keep the child out of the test's HOME

- [ ] Apply the mechanism Phase 1's finding selects; production behaviour unchanged with no seam set
- [ ] Negative controls: the no-`BASH_ENV` assertion still holds; a test still proves the build and test roles receive the Go cache roots

### Phase 3: Integration test

- [ ] Create `scripts/test-codex-home-cleanup.sh`: run the `codexTestHome` tests uncached at the count Phase 1 justifies → zero `TempDir RemoveAll cleanup` failures
- [ ] Negative control: a throwaway copy of the tree with the fix reverted reproduces the cleanup failure within the same count, or the script reports that it could not — a control that cannot fail proves nothing
- [ ] Run the script, record pass/fail counts in this spec, and add it to the script index in `CLAUDE.md`

## Notes

- Filed 2026-11-14 by plan from `/tmp/defect-codex-home-cleanup-flake.md` (edit), on the user's standing instruction to file defects found along the way.
- Not parked: the mechanism is chosen from Phase 1's evidence among options that all leave production behaviour alone; no user decision is owed.
- Root cause recorded 2026-11-14, the same day, replacing the filing's hypothesis (edit's request; the MUX-156 worker's finding, re-read by plan in the Go 1.27.4 source).
- The Phase 3 control may prove unreachable if the race is rare. The step says what to do then rather than pretending it passed.

## Status

**Backlog** — 0/12. Filed 2026-11-14.
