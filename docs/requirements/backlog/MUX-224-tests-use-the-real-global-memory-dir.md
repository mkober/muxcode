# MUX-224: The Test Suite Creates and Reads the User's Real Global Memory Directory

**Tracking:** [mkober/muxcode#208](https://github.com/mkober/muxcode/issues/208)

`GlobalMemoryDir()` is `$HOME/.config/muxcode/memory` with no override. `Init` creates it and tests
read it, and no package `TestMain` redirects it. Every `go test` run therefore touches the
developer's real global memory — the one directory the suite's isolation does not cover.

## Context

### Source and standard of evidence

Found by the worker's audit in [MUX-137](../completed/MUX-137-test-bus-dir-leak.md) Phase 2 and
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

Same family as [MUX-004](../completed/MUX-004-lifecycle-log-test-leak.md) (lifecycle log) and
MUX-137 (bus and session memory). Where project memory resolves in production is
[MUX-223](./MUX-223-project-memory-resolves-against-the-callers-cwd.md).

## Requirements

### Acceptance criteria

- [ ] `GlobalMemoryDir()` honors an override, and with none set resolves exactly as today
- [ ] Every package `TestMain` that can reach `Init` or global memory pins the override to its run's temp root
- [ ] A control test fails if the pin is removed
- [ ] A full `./test.sh` run leaves `~/.config/muxcode/memory` byte-for-byte unchanged, and does not create it when absent
- [ ] The Codex sandbox writable root still names the real directory in production
- [ ] `docs/configuration.md` lists the override

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

- [ ] Record what the suite does to the real global memory directory today (creates, reads, writes)
- [ ] `GlobalMemoryDir()` reads the override; unset behaviour unchanged, with a test
- [ ] Pin it in each package `TestMain`; record any package left unpinned, with the reason
- [ ] Control test: with the pin removed the test fails

### Phase 2: Integration test

- [ ] Extend MUX-137's `scripts/test-bus-dir-leak.sh`, or add `scripts/test-global-memory-leak.sh`: snapshot the real global memory directory, run the full suite, assert it is unchanged — and, under a scratch `HOME` holding none, that it was not created
- [ ] Negative control: a scratch copy of the tree with the pin removed makes the script fail
- [ ] Run the script and record pass/fail counts in this spec

## Notes

- Filed 2026-11-14 by plan from `/tmp/defect-memory-dirs.md` (edit) and MUX-137's Phase 2 results.

## Status

**Backlog** — 0/13. Filed 2026-11-14.
