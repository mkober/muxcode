# MUX-224: The Test Suite Creates and Reads the User's Real Global Memory Directory

**Tracking:** [mkober/muxcode#208](https://github.com/mkober/muxcode/issues/208)

`GlobalMemoryDir()` is `$HOME/.config/muxcode/memory` with no override. `Init` creates it and tests
read it, and no package `TestMain` redirects it. Every `go test` run therefore touches the
developer's real global memory — the one directory the suite's isolation does not cover.

## Context

### Source and standard of evidence

Found by the worker's audit in [MUX-137](./MUX-137-test-bus-dir-leak.md) Phase 2 and
recorded there as a remaining gap; filed on the user's standing instruction, relayed by edit. Plan
confirmed the code; no run was made to observe a write.

| Evidence | Detail |
|----------|--------|
| `bus/config.go:411–414` `GlobalMemoryDir()` | `os.UserHomeDir()` joined with `.config/muxcode/memory`; reads no environment variable |
| `Init` | `MkdirAll`s the global memory directory (worker's audit) |
| Package `TestMain`s | Pin `MUXCODE_LIFECYCLE_LOG_DIR`, `CODEX_HOME`, `XDG_STATE_HOME` and, since MUX-137, `BUS_MEMORY_DIR` — not `HOME`, and nothing for global memory |
| Readers | `bus/memory.go` (`readMemoryWithHistoryAt(GlobalMemoryDir(), …)`), `bus/rotation.go`, `bus/provider_codex.go` (a sandbox writable root) |

What a test run can do to the real directory beyond creating it — whether any test writes or
rotates a file there — is not established here; Phase 1 finds out.

### Scope boundary

Same family as [MUX-004](./MUX-004-lifecycle-log-test-leak.md) (lifecycle log) and
MUX-137 (bus and session memory). Where project memory resolves in production is
[MUX-223](../backlog/MUX-223-project-memory-resolves-against-the-callers-cwd.md).

## Requirements

### Acceptance criteria

- [x] `GlobalMemoryDir()` honors an override, and with none set resolves exactly as today — `BUS_GLOBAL_MEMORY_DIR`, absolute only; `TestGlobalMemoryDir_Resolution`
- [x] Every package `TestMain` that can reach `Init` or global memory pins the override to its run's temp root — bus, cmd, daemon and tui
- [x] A control test fails if the pin is removed — one per package; the pins-removed copy fails all four (proof run 9/0)
- [x] A full `./test.sh` run leaves `~/.config/muxcode/memory` byte-for-byte unchanged, and does not create it when absent — `scripts/test-bus-dir-leak.sh` sections 1 and 3, 22/22
- [x] The Codex sandbox writable root still names the real directory in production — `TestCodexBuildExecArgs_EveryRoleGetsMuxcodeState` unchanged, the override cleared in `codexTestHome`
- [x] `docs/configuration.md` lists the override — and `docs/agent-bus.md`

### Technical approach

Add an environment override read by `GlobalMemoryDir()` (name to match the existing
`BUS_MEMORY_DIR`, e.g. `BUS_GLOBAL_MEMORY_DIR`), pin it beside the other pins in each `TestMain`,
and add a control test in the style of `TestDefaultMemoryDir_IsolatedFromSourceTree`.

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/config.go` | `GlobalMemoryDir`, `GlobalMemoryPath` |
| `tools/muxcode/bus/main_test.go`, `cmd/main_test.go`, `daemon/main_test.go`, `tui/main_test.go` | The pins |
| `tools/muxcode/bus/provider_codex.go` | The sandbox root that must stay the real directory |
| `scripts/test-bus-dir-leak.sh` | MUX-137's regression script, to extend if it exists by then |

## Implementation

### Phase 1: An override, pinned

- [x] Record what the suite does to the real global memory directory today (creates, reads, writes) — creates and reads, never writes; a static audit, Phase 2 measures it
- [x] `GlobalMemoryDir()` reads the override; unset behaviour unchanged, with a test
- [x] Pin it in each package `TestMain`; record any package left unpinned, with the reason — none left unpinned
- [x] Control test: with the pin removed the test fails

### Results for Phase 1

Recorded 2026-11-14 from edit's handoff (`/tmp/mux224-phase1-docs.md`), each row read against the
working tree by plan. Uncommitted. Verified and ticked 2026-11-14 once all three had run after the
last source edit (16:38:37): `./build.sh` exit 0 at 16:40:51, `./test.sh` exit 0 at 16:42:00 (both
modules passed), review at 16:42:32 with 0 must-fix, 0 should-fix, 0 nits
(`/tmp/muxcode-review-1794692526.txt`).

| Item | Result |
|------|--------|
| Suite's effect before the fix (static audit; Phase 2 measures it) | **Creates**: `Init` (`bus/setup.go:124`) `MkdirAll`s `GlobalMemoryDir()`, and dozens of tests call `Init` (bus; cmd log/send/spec tests; daemon helpers). **Reads**: `ReadContext`/`ReadContextFull` (`memory_test` `TestReadContext*`) and `AllMemoryEntriesWithArchives` (`rotation_test`, `search_test`) read global shared and role files and archives — a developer's real global memory flowed into those assertions. **Writes**: none — no test calls `AppendGlobalMemory` or `memory write --global`, so nothing was written or rotated there |
| Override | `BUS_GLOBAL_MEMORY_DIR` (const `bus.GlobalMemoryDirEnv`), absolute only; unset or relative resolves to `~/.config/muxcode/memory` as before. Test: `TestGlobalMemoryDir_Resolution` (`bus/config_test.go`) |
| Pins | All four package `TestMain`s — bus, cmd, daemon, tui — set it to `<tempdir>/global-memory`. tui reaches no memory code today and is pinned by construction, like its lifecycle pin. No package left unpinned; the llm-harness module holds no global-memory reference |
| Codex sandbox root | `muxcodeStateRoots` follows `GlobalMemoryDir()`, so it is the real directory whenever the override is unset. `codexTestHome` clears the override, so `TestCodexBuildExecArgs_EveryRoleGetsMuxcodeState` still asserts the production home path, unchanged |
| Control tests | `TestGlobalMemoryDir_IsolatedFromHome` (bus), `TestMainIsolatesBusAndMemory` gained a global-memory case (cmd, daemon), new `TestMainIsolatesGlobalMemory` (tui) |
| Red/green proof | `/tmp/mux224-pin-proof.sh` via the run agent, proc `1794692351-proc-91db5cdb`: 9 passed, 0 failed (expected 9). The tree as written passes in all four packages; a copy with the four pins removed fails the control test in each of bus, cmd, daemon and tui |
| Docs | `BUS_GLOBAL_MEMORY_DIR` row added to `docs/configuration.md` and `docs/agent-bus.md` |

### Phase 2: Integration test

- [x] Extend MUX-137's `scripts/test-bus-dir-leak.sh`, or add `scripts/test-global-memory-leak.sh`: snapshot the real global memory directory, run the full suite, assert it is unchanged — and, under a scratch `HOME` holding none, that it was not created
- [x] Negative control: a scratch copy of the tree with the pin removed makes the script fail — built into the script as section 4: the unpinned copy creates the directory and fails the control test, and the script requires both
- [x] Run the script and record pass/fail counts in this spec — 22 passed, 0 failed (floor 22)

### Results for the integration phase

Recorded 2026-11-14 from the worker's request; the counts and section lines were read by plan in the
proc log. Uncommitted. Verified and ticked 2026-11-14 once all three had run after the last edit
(`CLAUDE.md`, 16:46:27): `./build.sh` exit 0 at 16:48:00, `./test.sh` exit 0 at 16:49:10 (both
modules passed), review at 16:49:41 with 0 must-fix, 0 should-fix, 0 nits
(`/tmp/muxcode-review-1794692954.txt`).

| Item | Result |
|------|--------|
| Script | MUX-137's `scripts/test-bus-dir-leak.sh` extended rather than a new script; floor raised 15 → 22. `CLAUDE.md`'s entry updated by edit |
| Run | Run agent, proc `1794692695-proc-d2a92691`: **22 passed, 0 failed (floor 22)**, exit 0. The script was last edited 13 s before the run started and not since |
| Section 1 (existing run, one new check) | After the full uncached `./test.sh`, the real `~/.config/muxcode/memory` is unchanged byte for byte (1 entry) |
| Section 3: scratch `HOME` | The scratch `HOME` holds no global memory before the run; the full suite passes under it (6 packages ok, 0 cached); the suite created no global memory there |
| Section 4: negative control | A throwaway copy with all four `TestMain` pins removed: `Init` creates the scratch `HOME`'s global memory and the detector sees it, and `TestGlobalMemoryDir_IsolatedFromHome` fails naming the missing pin |
| MUX-137's sections | Unchanged and still passing: no `/tmp/muxcode-bus-*` or real-state bus directory, no memory file under `tools/`, the sentinel bus intact, and the reverted-fix control reporting exactly its two leaks |

## Notes

- Filed 2026-11-14 by plan from `/tmp/defect-memory-dirs.md` (edit) and MUX-137's Phase 2 results.

## Status

**Complete** — closed 2026-11-14 by `55-defect-backlog-loop` run `1794626267-55-defect-backlog-loop-15547e2e`, 13/13. Activated 2026-11-14 by `55-defect-backlog-loop` run `1794626267-55-defect-backlog-loop-15547e2e` (Defects rank 16; ranks 1–15 are parked). 13/13: both phases and all six criteria verified 2026-11-14; ready for close-out. Checked against main `2958f25` at activation: `GlobalMemoryDir()` (`bus/config.go:411`) still reads no override and `scripts/test-bus-dir-leak.sh` exists to extend. Filed 2026-11-14.
