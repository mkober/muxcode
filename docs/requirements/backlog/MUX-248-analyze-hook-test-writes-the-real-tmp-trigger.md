# MUX-248: An Analyze-Hook Test Writes the Real `/tmp` Trigger on Every Suite Run

**Tracking:** [mkober/muxcode#272](https://github.com/mkober/muxcode/issues/272)

`TestProcessAnalyzeHook_TriggerFile` appends a line to
`/tmp/muxcode-analyze-test-analyze-trigger.trigger` on the real machine every time the suite runs,
and never removes it. The test isolates its bus directory, but the trigger path is a fixed `/tmp`
name that no test override reaches. The file grows by one line per run and is counted as a stale
artifact by every cleanup pass.

## Context

### Source and standard of evidence

Found 2026-11-27 in [MUX-150](../completed/MUX-150-verify-spec-names-last-routed-batch.md)'s Phase 1,
by the worker writing a neighbouring test, and recorded there as an unfiled side finding. Filed on
the user's request relayed by edit. Every row below was checked by plan on 2026-11-27.

| Fact | How established |
|------|-----------------|
| `TriggerFile` returns `"/tmp/muxcode-analyze-" + session + ".trigger"` with no override | Read: `bus/config.go:999` |
| The test calls `useTempBusDir(t)`, then `ProcessAnalyzeHook("test-analyze-trigger", "build", ev)` and reads the trigger back | Read: `bus/hook_test.go:1822` |
| `ProcessAnalyzeHook` opens the trigger with `O_APPEND\|O_CREATE` | Read: `bus/hook.go` |
| The file exists on this machine and grows | `/tmp/muxcode-analyze-test-analyze-trigger.trigger`: 27 lines, last modified 2026-11-27 21:17, the minute after the 21:16 suite run |
| It is what cleanup keeps counting | [MUX-160](../completed/MUX-160-tmp-go-cache-leak-unclearable-pressure.md)'s Phase 1 dry runs list this file as the one stale trigger, "born today" |

### Why it matters

- **It breaks the suite's own rule.** No test writes outside its scratch directories:
  [MUX-137](../completed/MUX-137-test-bus-dir-leak.md) and
  [MUX-224](../completed/MUX-224-tests-use-the-real-global-memory-dir.md) check the bus
  directories and the memory directories for exactly this, and
  `scripts/test-bus-dir-leak.sh` fails a suite that leaks one. A fixed-name file under `/tmp` is
  outside what that script snapshots, so this leak passes it.
- **It feeds an instrument.** The disk-pressure cleanup lists the file as stale on every pass. One
  small file is harmless as bytes; a "stale" count that is never zero was the mechanism of MUX-160.
- **The fixed session name collides.** Any two suite runs on one machine — two checkouts, a spawn
  worker, CI beside a developer — append to the same file, and the test's assertion ("contains
  `/foo/bar.go`") is satisfied by a line an earlier run wrote. The test can pass without the hook
  having written anything.

That last point makes this more than hygiene: the assertion is not evidence of the behaviour it
names once the file exists.

### What the neighbouring test did

MUX-150's new `bus/analyze_docs_gate_test.go` uses a unique session name per test and removes its
trigger afterwards. That avoids the collision and the leftover for that test, and is a workaround in
each test rather than a seam: the next test that calls the hook must remember to do the same.

## Requirements

### Acceptance criteria

- [ ] A full `./test.sh` creates or modifies no `/tmp/muxcode-analyze-*.trigger` file on the machine
- [ ] `TestProcessAnalyzeHook_TriggerFile` asserts on a trigger it can prove this run wrote: it fails
      if the hook writes nothing, even when a file from an earlier run exists at the old path
- [ ] Production is unchanged: with no test override the trigger is still
      `/tmp/muxcode-analyze-<session>.trigger`, the path the daemon and the hooks agree on
- [ ] A leak check fails the suite if any test writes a trigger file under the real `/tmp`
- [ ] **Negative control:** the leak check reports a planted trigger write, so a green check is not a
      check that sees nothing

### Technical approach

The trigger stays in `/tmp` in production — it is rewritten on every edit and is deliberately not
durable state. The seam is for tests: `TriggerFile` returns a path under the bus-directory override
when one is set (`SetBusDirBase`, which the test helper `useTempBusDir` already calls), and the
fixed `/tmp` path otherwise. Every existing and future test that isolates its bus directory then isolates its trigger
with no further step. The alternative — each test choosing a unique session and removing its file —
is what MUX-150's test does today, and leaves the rule to memory.

The daemon reads `bus.TriggerFile(session)` too, so both sides move together under the override.

### Key files

| File | Relevance |
|------|-----------|
| `tools/muxcode/bus/config.go` | `TriggerFile` (`:999`) — the fixed path |
| `tools/muxcode/bus/hook_test.go` | `TestProcessAnalyzeHook_TriggerFile` (`:1822`) |
| `tools/muxcode/bus/analyze_docs_gate_test.go` | The per-test workaround, removable once the seam exists |
| `tools/muxcode/bus/hook.go`, `bus/cleanup.go`, `bus/setup.go`, `daemon/daemon.go` | The other callers of `TriggerFile` |
| `scripts/test-bus-dir-leak.sh` | The leak check to extend |

## Implementation

### Phase 1: Pin the leak

- [ ] Characterization: run `TestProcessAnalyzeHook_TriggerFile` with the old trigger file removed
      and record that it re-creates it under the real `/tmp`
- [ ] Show the false pass: with the file left in place from an earlier run and the hook's write
      disabled in a scratch copy, the test still passes; record it

### Phase 2: Fix

- [ ] `TriggerFile` returns a path under the bus-directory override when one is set, the fixed
      `/tmp` path otherwise; a unit test pins both
- [ ] `TestProcessAnalyzeHook_TriggerFile` reads the overridden path and fails when the hook writes
      nothing
- [ ] Remove the unique-session-and-cleanup workaround from `analyze_docs_gate_test.go` if the seam
      makes it unnecessary, or record why it stays
- [ ] Check every other caller of `TriggerFile` and every test that calls `ProcessAnalyzeHook` for
      the same leak; record the list

### Phase 3: Integration test

- [ ] Extend `scripts/test-bus-dir-leak.sh`: snapshot `/tmp/muxcode-analyze-*.trigger` with the other
      paths before the suite and fail if any is created or modified after it
- [ ] **Negative control:** a throwaway copy of the tree with the override removed from `TriggerFile`
      must report the trigger leak by name
- [ ] The script never deletes a trigger file it did not create; record here how the existing stale
      `/tmp/muxcode-analyze-test-analyze-trigger.trigger` is removed (by the user, or by a cleanup
      pass)
- [ ] Update the script's coverage floor; run it and record the counts here

## Out of scope

- Moving the production trigger out of `/tmp`.
- The preview temp file (`/tmp/muxcode-preview-<session>.tmp`), unless Phase 2's survey finds a test
  writing it — then it is recorded here and filed separately.

## Status

Backlog

Filed 2026-11-27 on the user's request relayed by edit, from a side finding in MUX-150's Phase 1.
The mechanism and the file on disk are verified. No decision is open. Not started.
