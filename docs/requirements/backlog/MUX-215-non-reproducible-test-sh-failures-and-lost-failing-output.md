# MUX-215: `./test.sh` Fails, Passes on Rerun, and Nothing Recorded Which Test Failed

**Tracking:** [mkober/muxcode#190](https://github.com/mkober/muxcode/issues/190)

Twice on 2026-11-13 `./test.sh` reported a module `failed (go test)`, and a rerun moments later
passed. Neither failure can be named: the test agent's history keeps only the tail of the output —
the summary block — and the agent's reply reported the module, not the test. So there is a flaky
test, or a test that fails when the tree is mid-edit or two runs overlap, and no record of which.

## Context

### Source and standard of evidence

Filed 2026-11-13 on the user's request relayed by edit (`/tmp/defect-flaky-and-govet.md`). The two
occurrences are edit's account; plan read the test agent's history in session `muxcode`'s bus
stores for both. No test was re-run for this spec, and the cause is not determined.

### Observed

| When | Edit's account | What the history row holds |
|------|----------------|----------------------------|
| 18:06 | `./test.sh` failed in module `muxcode` (go test); an immediate `go test -v ./...` passed; the failing test's name was not captured | `./test.sh` at 18:05:52, exit 1: `muxcode-llm-harness: passed`, `muxcode: failed (go test)`. The stored row is 781 bytes — the `=== Summary ===` block and nothing above it; no `--- FAIL` line |
| 18:59 | `./test.sh` failed in module `muxcode-llm-harness` (go test) while `muxcode` passed; the rerun passed; truncated output hid the failing test | `./test.sh` at 18:58:33, exit 1: `muxcode-llm-harness: passed`, **`muxcode: failed (go test)`**. Also 781 bytes, summary only |

**The second row disagrees with the account**: the store shows the `muxcode` module failing at
18:58, not the harness. Either the account transposed the modules or a different run is meant; the
store holds no failing harness row since 15:06.

Seven more `./test.sh` rows failed that afternoon — 16:31:04, 16:31:58, 16:32:48, 16:38:24, 16:53:50
and 16:55:42 (`muxcode: failed (go test)`) and 17:57:32 (`muxcode: failed (go vet)`). Those fall in
windows where code was being edited (17:57:32 is the mid-edit run MUX-213 attributes to a
response-started turn), so they are not counted as non-reproducible here; they have the same
problem of naming nothing.

### Why no failure can be named — verified

- Hook capture stores the tail of a command's output (a 15-line tail and a length clip, per
  `CLAUDE.md`'s PII-scrubbing note on the history writers). `./test.sh` ends with its summary block,
  so the tail is the summary and the failing test's `--- FAIL` lines, printed far above, are gone.
- `./test.sh` runs `go test -v ./...`, whose output for this repository is on the order of a hundred
  thousand tokens; an agent reading it back sees a truncated middle.
- The summary names the module and the stage — `failed (go test)` — never the package or the test.

### Cause — not determined

Candidates, none verified:

- **Overlapping runs.** MUX-213 found the test agent re-running the suite seconds apart; two runs
  sharing scratch state, ports, tmux servers or the Go build cache could fail one another.
- **A tree mid-edit.** A run started while a worker was writing files compiles a half-written
  change; the rerun sees the finished one.
- **A timing-dependent test.** Several suites poll with deadlines; a loaded machine could miss one.
- **Shared state between tests** inside one module — a session name, a temp directory, an
  environment variable — that only collides under some orderings.

## Requirements

### Acceptance criteria

- [ ] A failing `./test.sh` always names what failed: the summary lists each failing package and
  test, and the full output of the failing module is saved to a file the summary points at
- [ ] That information survives into the test agent's history row and its reply, so a failure seen
  only in history can still be named
- [ ] The flaky test or tests behind the 2026-11-13 failures are identified, or the failures are
  shown to come from overlapping or mid-edit runs — with the evidence recorded here
- [ ] An identified flaky test is fixed, or its cause is filed as its own defect and linked
- [ ] Negative control: a passing run's summary and exit code are unchanged, and a run that fails in
  `go vet` still reports `failed (go vet)`
- [ ] `CLAUDE.md`'s `./test.sh` row and `docs/agents.md` describe the saved log and the named
  failures

### Technical approach

**Capture first, then hunt.** The capture is worth having whatever the cause turns out to be, and
without it the next occurrence is as unnameable as these two.

- `./test.sh` writes each module's vet and test output to a log under a run directory, and on
  failure adds to its summary one line per failing test (`--- FAIL` lines, and `FAIL <package>`
  lines for build failures) plus the path of the module's log. The summary stays the last thing
  printed, so it is what the history tail keeps.
- With names in hand, run the identified tests repeatedly (`go test -run <name> -count=N`), alone
  and with a second suite run overlapping, to separate a flaky test from an interference effect.

**Rejected — rerun on failure and report the second result.** It would hide exactly the failures
this spec is about.

### Key files

| File | Purpose |
|------|---------|
| `test.sh` | The summary block and where each module's output goes |
| `tools/muxcode/bus/hook.go` | Hook capture's output tail and clip |
| `agents/test-runner.md` | What the test agent reports for a failure |
| `scripts/test-test-sh-coverage.sh` | The existing script pinning `test.sh`'s summary (MUX-174) |

## Implementation

### Phase 1: Make a failure nameable

- [ ] `./test.sh` saves each module's full output to a log file and prints the path in the summary
  for a failing module
- [ ] On a `go test` failure the summary lists the failing packages and tests, kept short enough to
  survive the history tail
- [ ] `agents/test-runner.md`: a failure reply names the failing tests from the summary
- [ ] Extend `scripts/test-test-sh-coverage.sh`: a forced test failure names the test and the log
  path; a passing run's summary is unchanged (negative control)

### Phase 2: Identify the failures

- [ ] Reproduce: run the suite repeatedly, alone and with a second run overlapping, until a failure
  is captured by name; record the test, the frequency and the conditions here
- [ ] Decide from that evidence whether it is a flaky test, an interference effect, or a mid-edit
  artefact; record which
- [ ] Fix an identified flaky test, or file its cause as its own defect and link it

### Phase 3: Integration test

- [ ] `bash scripts/test-test-sh-coverage.sh` through the run agent: every check passes, floor met;
  record the counts here
- [ ] A history row for a failing `./test.sh`, written through the real `hook bash`, contains the
  failing test's name
- [ ] Negative control: with the summary's failure lines removed, that check fails — proving it can
  see a nameless failure
- [ ] Update `CLAUDE.md` and `docs/agents.md`

## Related

- [MUX-213](../completed/MUX-213-unrequested-test-runs-fire-review-chain-and-reviews-answer-twice.md) —
  overlapping and mid-edit runs, one candidate cause
- [MUX-214](./MUX-214-root-go-vet-false-alarm-from-the-build-agent.md) — the false alarms; this
  spec is about the real failures
- [MUX-152](./MUX-152-test-sh-hides-modules-after-first-failure.md) — `test.sh` hiding modules after
  the first failure; the summary this builds on came from MUX-174
- [MUX-153](./MUX-153-codex-test-agent-cannot-run-the-suite.md) — the Codex test agent and the suite

## Status

Backlog

Filed 2026-11-13 on the user's request relayed by edit, ranked directly after MUX-213 and MUX-214
and eligible for `55-defect-backlog-loop`. Two failures are on record by time and module — the
second with a module that disagrees with the account — and neither names a test. The cause is
**not determined**. Not started.
