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

- [x] A full `./test.sh` creates or modifies no `/tmp/muxcode-analyze-*.trigger` file on the machine
      — `test-bus-dir-leak.sh` section 1, 22:17 run
- [x] `TestProcessAnalyzeHook_TriggerFile` asserts on a trigger it can prove this run wrote: it fails
      if the hook writes nothing, even when a file from an earlier run exists at the old path —
      executed on the fixed tree 22:18, see Findings for Phase 2
- [x] Production is unchanged: with no test override the trigger is still
      `/tmp/muxcode-analyze-<session>.trigger`, the path the daemon and the hooks agree on —
      pinned by `TestTriggerFile_FollowsTheBusDirOverride`, passing in the 22:10 suite run
- [x] A leak check fails the suite if any test writes a trigger file under the real `/tmp` — the
      check lives in `scripts/test-bus-dir-leak.sh`, which runs the suite; `./test.sh` alone does not
      carry it
- [x] **Negative control:** the leak check reports a planted trigger write, so a green check is not a
      check that sees nothing — section 5

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

- [x] Characterization: run `TestProcessAnalyzeHook_TriggerFile` with the old trigger file removed
      and record that it re-creates it under the real `/tmp`
- [x] Show the false pass: with the file left in place from an earlier run and the hook's write
      disabled in a scratch copy, the test still passes; record it

#### Findings for Phase 1

Executed 2026-11-27 22:02 by `/tmp/mux248-pin.sh` through the run agent, against a scratch copy of
`tools/muxcode` with `GOFLAGS=-count=1`. The real stale trigger (32 lines, 736 bytes) was moved
aside and restored byte for byte afterwards.

| Step | Setup | Result |
|------|-------|--------|
| 1. Characterization | Unmodified tree, stale file removed | The test passes and re-creates `/tmp/muxcode-analyze-test-analyze-trigger.trigger` with one line, `<timestamp> /foo/bar.go` |
| 2. False pass | The hook's trigger write redirected to `os.DevNull` in the scratch copy, stale file left in place | The test **passes**; the file is unchanged, 32 → 32 lines |
| 3. Control | The same disabled build, no stale file | The test **fails** at `hook_test.go:1844`, "trigger file missing file path" |

Step 3 is what makes step 2 mean something: the assertion can fail, so it was the stale file alone
that satisfied it. The script checks that its patch applied and that the patched tree compiles
before running step 2.

### Phase 2: Fix

- [x] `TriggerFile` returns a path under the bus-directory override when one is set, the fixed
      `/tmp` path otherwise; a unit test pins both
- [x] `TestProcessAnalyzeHook_TriggerFile` reads the overridden path and fails when the hook writes
      nothing
- [x] Remove the unique-session-and-cleanup workaround from `analyze_docs_gate_test.go` if the seam
      makes it unnecessary, or record why it stays
- [x] Check every other caller of `TriggerFile` and every test that calls `ProcessAnalyzeHook` for
      the same leak; record the list

#### Findings for Phase 2

Recorded 2026-11-27 from the worker's report, checked against the working-tree diff and a search
for every `TriggerFile(` call.

**Verified 2026-11-27 22:11.** `./test.sh` ran 22:09–22:10 on this tree (the last source edit was
22:07): both modules passed, no failing test, and `TestTriggerFile_FollowsTheBusDirOverride`,
`TestProcessAnalyzeHook_TriggerFile` and `TestCheckNonHookEdits_HookCodexSkipped` each pass. Review
returned 0 must-fix, 0 should-fix, 0 nits. The real
`/tmp/muxcode-analyze-test-analyze-trigger.trigger` was last modified 22:04, before the fix, and
that suite run left it alone (33 lines) — one observation, not the leak check Phase 3 builds.

**The mutation, executed on the fixed tree 22:18.** Phase 1's script, unchanged, re-run through
the run agent against this tree:

| Step | Before the fix (22:02) | After the fix (22:18) |
|------|------------------------|-----------------------|
| 1. Stale file removed, unmodified tree | Test passes and re-creates the real `/tmp` file | Test passes and does **not** re-create it |
| 2. Hook's write disabled, stale file in place | Test **passes** — the false pass | Test **fails** at `hook_test.go:1852`, `trigger file = ""` |
| 3. Hook's write disabled, no stale file | Test fails | Test fails the same way |

The stale file was restored unchanged afterwards (33 lines, 759 bytes, last written 22:04).

**The seam.** `TriggerFile` (`bus/config.go`) returns `<override>/muxcode-analyze-<session>.trigger`
when a test has set the bus-directory override (`SetBusDirBase`), and the unchanged `/tmp` path
otherwise. Production never sets the override, so the path the bash hooks and the daemon agree on
does not move.

**Production callers** — all go through `TriggerFile`, so they follow the seam together:

| File | Use |
|------|-----|
| `bus/hook.go` | `ProcessAnalyzeHook` appends the trigger line |
| `daemon/daemon.go` | `New` stores the path; the trigger-routing code reads it |
| `bus/cleanup.go` | `Cleanup` removes it |
| `bus/setup.go` | Session setup removes it |

**Tests:**

| Test | Before | Now |
|------|--------|-----|
| `TestProcessAnalyzeHook_TriggerFile` (`bus/hook_test.go`) | Wrote the real `/tmp` file; any earlier line satisfied it | Requires the path inside its own bus directory and exactly the one line this call wrote |
| `TestTriggerFile_FollowsTheBusDirOverride` (new) | — | Pins both the fixed path and the overridden one |
| `analyze_docs_gate_test.go` (`analyzeWrite`) | Unique session name plus a cleanup, to work around the fixed path | Workaround removed; the fresh bus directory holds the trigger |
| `TestCheckNonHookEdits_HookCodexSkipped` (`daemon/codex_hook_road_test.go`) | **The one other leak:** no override, so its scrape-road control wrote a real `/tmp` trigger each run and removed it at cleanup | Runs under `SetBusDirBase` |
| The hook tests that skip or pass an empty path | Return before writing | No leak, unchanged |

**Preview file.** No test writes `/tmp/muxcode-preview-*`: `cleanup_test.go` builds its preview
file under its own temp directory, and its other mention is a literal in a result struct. Nothing
to file separately.

### Phase 3: Integration test

- [x] Extend `scripts/test-bus-dir-leak.sh`: snapshot `/tmp/muxcode-analyze-*.trigger` with the other
      paths before the suite and fail if any is created or modified after it
- [x] **Negative control:** a throwaway copy of the tree with the override removed from `TriggerFile`
      must report the trigger leak by name
- [x] The script never deletes a trigger file it did not create; record here how the existing stale
      `/tmp/muxcode-analyze-test-analyze-trigger.trigger` is removed (by the user, or by a cleanup
      pass)
- [x] Update the script's coverage floor; run it and record the counts here

#### Findings for Phase 3

Run 2026-11-27 22:34 through the run agent (log `/tmp/test-bus-dir-leak.log`), on the script as
last edited 22:30: **27 passed, 0 failed, floor 27** (was 22). Five assertions are new:

| Section | Assertion | Result |
|---------|-----------|--------|
| 1 | The full uncached suite creates or modifies no `/tmp/muxcode-analyze-*.trigger` | Pass — 1 pre-existing trigger, unchanged |
| 5 | The control trigger is claimed exclusively | Pass |
| 5 | A claim on an existing trigger is refused and the file left byte for byte | Pass |
| 5 | The control's revert and session rename applied | Pass |
| 5 | The reverted `TriggerFile`'s `/tmp` trigger is reported by name | Pass — `LEAK /tmp/muxcode-analyze-test-analyze-trigger-ctl-<pid>-<random>.trigger` |

A first run at 22:17 passed 25 of 25. Review then asked that the control never remove a file by a
path derived from its pid: the control trigger is now claimed with `noclobber` under a fresh random
session name per try and removed by the exit trap, and the second assertion above is the
preservation control for that claim; that version ran 27 of 27 at 22:26 and left no control or
decoy trigger in `/tmp`. A second review round found that when every claim attempt failed the
script still went on to patch and run the hook test with an empty session name, against a `/tmp`
path it did not own; it now exits before deriving the control path. The 22:34 run is of that final
script. `./test.sh` passed at 22:37 and the last review returned 0 must-fix, 0 should-fix, 0 nits.

The allocation-failure exit itself was reviewed statically, not executed: no run forced every claim
to fail.

- **How it compares.** Each trigger is checksummed before and after, so a modified file is caught as
  well as a new one.
- **What it leaves out.** A live tmux session's own trigger is excluded, since that session's agents
  write it while the suite runs. A test leaking into a live session's name would not be seen.
- **What the control covers.** Section 5 removes the override from a throwaway copy and renames the
  hook test's session, so the leak lands in a file the script itself creates and then deletes. It
  runs the hook test only, not the daemon test Phase 2 also fixed.
- **The stale file.** `/tmp/muxcode-analyze-test-analyze-trigger.trigger` is still on disk (33
  lines, last written 22:04, before the fix). The script never deletes it. It names a session that
  does not exist, so `muxcode cleanup` or the disk-pressure stale pass removes it, or the user does.

## Out of scope

- Moving the production trigger out of `/tmp`.
- The preview temp file (`/tmp/muxcode-preview-<session>.tmp`), unless Phase 2's survey finds a test
  writing it — then it is recorded here and filed separately.

## Status

Complete

Closed out 2026-11-27 by `55-defect-backlog-loop` run `1795811929` at 15/15: three phases and five
acceptance criteria, nothing open.

**What shipped**

| Change | Where |
|--------|-------|
| `TriggerFile` follows a test's bus-directory override; the production `/tmp` path is unchanged | `bus/config.go` |
| The hook test reads its own bus directory and requires exactly the line this call wrote | `bus/hook_test.go` |
| A unit test pins both the fixed and the overridden path | `bus/hook_test.go` |
| The second leak, the daemon's scrape-road control, runs under the override | `daemon/codex_hook_road_test.go` |
| The unique-session workaround is removed | `bus/analyze_docs_gate_test.go` |
| The leak check and its negative control, floor 22 → 27 | `scripts/test-bus-dir-leak.sh` |

**Left as recorded**

- The stale `/tmp/muxcode-analyze-test-analyze-trigger.trigger` is still on disk (33 lines). Nothing
  here deletes it; `muxcode cleanup`, the disk-pressure stale pass or the user removes it.
- The leak check excludes a live tmux session's own trigger, so a test leaking into a live session's
  name would not be seen.
- The negative control runs the hook test only, not the daemon test.
- The leak check lives in `scripts/test-bus-dir-leak.sh`; `./test.sh` alone does not carry it.
- The script's exit when it cannot claim a control trigger was reviewed, not executed.

**How it got here.** Filed 2026-11-27 on the user's request relayed by edit, from a side finding in
MUX-150's Phase 1. Activated and worked the same day; no decision was open.
