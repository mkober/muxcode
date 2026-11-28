# MUX-247: The Bus Suite Fails Under Codex's Opt-In Workspace-Write Sandbox

**Tracking:** [mkober/muxcode#271](https://github.com/mkober/muxcode/issues/271)

With `MUXCODE_CODEX_SANDBOX=workspace-write` — the opt-in that restores Codex's sandbox — a full
`./test.sh` run by a Codex test agent exits 1: **27 tests fail, all in `tools/muxcode/bus`**. The
same tree passes on the default, unsandboxed road. A user who opts back into the sandbox therefore
gets a red suite from their test agent for a change that is green everywhere else, and nothing in the
output says the sandbox is the reason.

## Context

### Source and standard of evidence

Found 2026-11-27 during [MUX-160](../completed/MUX-160-tmp-go-cache-leak-unclearable-pressure.md)'s
Phase 3 re-measure, which ran the first recorded full `./test.sh` under the Codex sandbox
([MUX-153](../completed/MUX-153-codex-test-agent-cannot-run-the-suite.md) notes it had never run
there). Filed on the user's request relayed by edit.

| Claim | How established |
|-------|-----------------|
| 27 top-level failures, all in the `bus` package | Counted by plan from the `--- FAIL:` lines of `/tmp/muxcode-test-logs/20261127-185146-70535/muxcode.log` |
| The root, `cmd`, `daemon` and `tui` packages passed | The same log's package lines |
| The same tree passes unsandboxed | The 18:50 suite log (`20261127-185022-61181`) holds no `--- FAIL:` line; it reads `ok … bus (cached)`, so the fresh run behind it is an earlier one on identical inputs |
| The run was a real sandboxed Codex test agent | One `codex exec` (codex-cli 0.162.1) under the test role's opt-out launch flags from `muxcode agent sandbox-args test`, with `-a` dropped; script `/tmp/mux160-remeasure-full.sh`, run by the run agent |
| **Why each test fails** | **Not established.** Only one failure message was read |

That log lives under `/tmp` and will not survive; Phase 1 reproduces it rather than relying on it.

### What failed

| Group | Count | Tests |
|-------|-------|-------|
| Graph cancel and spawn-worker stop | 22 | `TestCancel*` — among them `TestCancelStopsRunningSpawnWorker`, `TestCancelStopsParkedWorker`, `TestCancelFailsClosedOnSurvivor`, `TestCancelWaitsOutReplacement` |
| Cleanup and orphan reaping | 2 | `TestCleanupHoldsAnUnresolvedSessionWhole`, `TestReapOrphansTerminatesTheWholeProcGroup` |
| Graph executor | 3 | `TestExecCancelMidRun`, `TestExecRetryBelowNeverApprovedGateUnaffected`, `TestExecHoldingOwnerLockSurvivesExec` |

The one message read: `TestCleanupHoldsAnUnresolvedSessionWhole/a_proc_group_that_outlived_its_leader`
— "the proc tree never started its TERM-ignoring child", a fixture-setup failure before any cleanup
ran.

### The pattern, and what it does not prove

These are the tests that start, signal and supervise processes and process groups. That fits Codex's
Seatbelt policy restricting process control, which is already known to refuse `ps` and the tmux
socket under `workspace-write`. It is a pattern across test names, not a diagnosis: 26 of the 27
messages are unread, and `TestExecRetryBelowNeverApprovedGateUnaffected` does not obviously belong
to the group.

### Blast radius

- Only a user who sets `MUXCODE_CODEX_SANDBOX=workspace-write` (or the per-role variable) for the
  test role. Codex has launched unsandboxed by default since 2026-11-02.
- For that user: every `./test.sh` from the Codex test agent fails, so the build → test → review
  chain stops at test and a graph run's test node fails, whatever the change under test.
- The failure names 27 tests and no cause, so it reads as a broken change.

### Family

- [MUX-153](../completed/MUX-153-codex-test-agent-cannot-run-the-suite.md) — the Codex test agent
  could not run the suite; closed as overtaken once Codex ran unsandboxed.
- [MUX-161](../completed/MUX-161-upgrade-daemons-ps-blocked-in-codex-sandbox.md) — `ps` refused in
  the Codex sandbox, fixed by discovering processes another way.
- [MUX-238](./MUX-238-nothing-fails-when-a-test-binds-a-socket.md) — the same class from the other
  side: a suite property a sandbox depends on, with nothing enforcing it.

## Decisions

### Open — the user's, after Phase 1

| # | Question | What bears on it |
|---|----------|------------------|
| 1 | **Skip with a reason, or fix the fixtures?** | Skipping under a detected sandbox makes the suite green and says why, but stops testing cancel, reap and exec-lock behaviour for that user. Fixing means each fixture works without what the sandbox refuses — possible only if the refused operation is incidental to the fixture, which Phase 1 establishes per test. It may be both: fix what can be fixed, skip the rest by name |
| 2 | **How is the sandbox detected, if tests skip?** | An environment marker Codex sets, a probe of the refused operation, or the muxcode variable itself. A probe is the only one that cannot go stale |

Phase 2 is not started until these are recorded here.

## Requirements

### Acceptance criteria

- [ ] Each of the 27 failures has its cause recorded: the operation the sandbox refused, or another
      cause, read from that test's own failure message
- [ ] Under `MUXCODE_CODEX_SANDBOX=workspace-write`, a full `./test.sh` run by a Codex agent with the
      test role's launch flags exits 0
- [ ] No test is silently dropped: a test that does not run under the sandbox is reported as skipped
      with a reason naming the sandbox, and the suite summary shows the count
- [ ] **Negative control:** on the default, unsandboxed road every one of the 27 still runs and
      passes — none is skipped there
- [ ] **Negative control:** a genuine failure in one of those tests still fails the suite under the
      sandbox — the remedy must not turn a real failure into a skip
- [ ] `docs/configuration.md` states, beside `MUXCODE_CODEX_SANDBOX`, what the suite does under the
      opt-in

### Key files

| File | Relevance |
|------|-----------|
| `tools/muxcode/bus/*_test.go` | The 27 tests — `TestCancel*`, the cleanup and reap tests, the three `TestExec*` |
| `tools/muxcode/bus/provider_codex.go` | `codexSandboxMode`, `codexSandboxArgs` — what the opt-in launches with |
| `test.sh` | The summary that must show skips |
| `docs/configuration.md` | The `MUXCODE_CODEX_SANDBOX` entry |
| `scripts/test-codex-sandbox-suite.sh` | New |

## Implementation

### Phase 1: Establish the cause per test

- [ ] Reproduce: one real `codex exec` under the test role's `workspace-write` launch flags running
      the full `./test.sh`; keep the log outside `/tmp` for the length of this spec and record the
      failing tests here
- [ ] For each failing test, read its failure message and record the refused operation or other cause
      in a table in this spec; group tests that share one
- [ ] Record whether each fixture needs the refused operation, or uses it incidentally
- [ ] Confirm all of them pass unsandboxed on the same tree, from a fresh (uncached) run

### Phase 2: Remedy, per the decisions

- [ ] Record the user's answers to open Decisions 1 and 2 in this spec and rewrite this phase's
      remaining steps to them
- [ ] Apply the remedy to each group from Phase 1's table
- [ ] A test skipped under the sandbox names the sandbox in its skip reason; `./test.sh`'s summary
      counts skips
- [ ] Negative controls: unsandboxed, none of the 27 is skipped; under the sandbox a forced real
      failure in one of them still fails

### Phase 3: Docs

- [ ] `docs/configuration.md`: beside `MUXCODE_CODEX_SANDBOX`, what the suite does under the opt-in
      and which tests it does not run there

### Phase 4: Integration test

- [ ] Create `scripts/test-codex-sandbox-suite.sh` — a live section, opt-in by flag or environment
      variable as the other real-Codex scripts are, running one `codex exec` under the test role's
      `workspace-write` flags on the full `./test.sh`
- [ ] Test: the sandboxed run exits 0 and its summary names the skipped tests and the reason
- [ ] **Negative control:** the same run with a forced failure in one of the 27 exits 1
- [ ] **Negative control:** an unsandboxed run of the same tree skips none of them
- [ ] A hermetic section that needs no Codex: the detection and skip logic against a stubbed sandbox
      signal, so the script proves something on a machine without Codex
- [ ] Coverage floor; run the script and record the counts here

## Out of scope

- Changing what Codex's sandbox permits.
- Making the sandbox the default again.

## Status

Backlog

Filed 2026-11-27 on the user's request relayed by edit, from MUX-160's Phase 3 re-measure. The
failure count and its package are verified from the suite log; the cause per test is not
established. Two decisions are open and Phase 2 waits on them. Not started.
