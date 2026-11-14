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

- [x] A failing `./test.sh` always names what failed: the summary lists each failing package and
  test, and the full output of the failing module is saved to a file the summary points at —
  Phase 1, shown against a stub `go` printing go-test-shaped output; past four failing packages the
  summary points at the log rather than listing them
- [ ] That information survives into the test agent's history row and its reply, so a failure seen
  only in history can still be named
- [ ] The flaky test or tests behind the 2026-11-13 failures are identified, or the failures are
  shown to come from overlapping or mid-edit runs — with the evidence recorded here
- [ ] An identified flaky test is fixed, or its cause is filed as its own defect and linked
- [x] Negative control: a passing run's summary and exit code are unchanged, and a run that fails in
  `go vet` still reports `failed (go vet)` — Phase 1's script
- [x] `CLAUDE.md`'s `./test.sh` row and `docs/agents.md` describe the saved log and the named
  failures — Phase 1

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

- [x] `./test.sh` saves each module's full output to a log file and prints the path in the summary
  for a failing module
- [x] On a `go test` failure the summary lists the failing packages and tests, kept short enough to
  survive the history tail
- [x] `agents/test-runner.md`: a failure reply names the failing tests from the summary
- [x] Extend `scripts/test-test-sh-coverage.sh`: a forced test failure names the test and the log
  path; a passing run's summary is unchanged (negative control) — 31 of 31, floor 31

### Findings and changes for Phase 1

Recorded 2026-11-13 from the run worker's handoff (run `1794608375`, node `implement`), checked by
plan against the tree. Uncommitted. The run's build, `./test.sh` (20:43, both modules) and review
then passed on it, and the phase was ticked.

| Change | Where | What it does |
|--------|-------|--------------|
| Per-module log | `test.sh` | Each module's vet and test output is mirrored (`tee -a`, with `pipefail` keeping go's status) to `$MUXCODE_TEST_LOG_DIR/<YYYYmmdd-HHMMSS-pid>/<module>.log`, default root `/tmp/muxcode-test-logs`. If the directory cannot be made, nothing is logged and the summary has no log line |
| Named failures | `test.sh` summary | A failed module's line is followed by `  log: <path>`, then for a test failure one `  FAIL <package>: <test>, <test>…` line per failing package (module path stripped, `[build failed]`/`[setup failed]` kept; four packages, then `  ... N more failing package(s), see the log`; 120 characters a line), or for a vet failure up to three `  vet <file:line: finding>` lines. A passing run prints none, and the summary contract and exit codes are unchanged |
| Failure reply | `agents/test-runner.md` | A failure reply quotes the `FAIL`/`vet` lines and the `log:` path, and does not rerun to check for flakiness |
| Script | `scripts/test-test-sh-coverage.sh` | The stub `go` prints go-test-shaped failure output and a vet finding. New checks: a passing run has no detail lines (negative control); a vet failure names the log and the finding; a test failure names `FAIL stubpkg: TestStubForced`, a log holding the full output, and that line sits within the last 15 lines a history row keeps. Floor 25 → 31 |

**A cause found on the way: the history clip kept the wrong end.** Hook capture (`clipOutput`,
`bus/hook.go`) already kept the last 15 lines, but then cut them to their **first** 1000 bytes.
When long go-test lines preceded the summary, the cut removed the summary — the only place a
failure is named. It now keeps the **last** 1000 bytes (`...` prefix, never splitting a rune). This
is a plausible reason the 2026-11-13 rows named nothing, though it is not shown that those rows
were cut this way. The change is wider than this spec: every history row, and the `output_contains`
condition's 4000-byte window, now keep the end of the tail rather than its start. Tests:
`TestGetOutput_ClipKeepsTheClosingSummary` (new — summary below long lines survives, output under
the limit returned whole, no split rune), with `TestGetOutput_Truncation`,
`TestGetScrubbedOutput_RedactsBeforeClip` and `TestGetScrubbedOutput_CleanMatchesGetOutput` updated
to the new direction.

Runs, through the run agent: `scripts/test-test-sh-coverage.sh` **31 passed, 0 failed, floor 31,
exit 0** (task `1794620416-spawn-100fae85-4b015b5b`); `scripts/test-root-go-vet.sh` 26 of 26 as a
compile check of the tree after the clip change (task `1794620443-spawn-100fae85-7f58e544`).

Not done here: Phase 3's check that a history row written through the real `hook bash` contains
the failing test's name. The logs live under `/tmp`, which macOS clears of files untouched for
three days — acceptable for a diagnostic log, and a limit on how late a failure can be looked up.

### Phase 2: Identify the failures

- [ ] Reproduce: run the suite repeatedly, alone and with a second run overlapping, until a failure
  is captured by name; record the test, the frequency and the conditions here
- [ ] Decide from that evidence whether it is a flaky test, an interference effect, or a mid-edit
  artefact; record which
- [ ] Fix an identified flaky test, or file its cause as its own defect and link it

### Findings for Phase 2

Recorded 2026-11-13 from the run worker's handoff (run `1794608375`, node `implement`). **The
failing test is not identified.** Nothing is ticked on this. Plan confirmed the run counts in the
run agent's replies; the reading of the test agent's Codex transcript is the worker's and was not
re-read by plan.

**Reproduction — 14 uncached runs, 0 failures.** Through the run agent, `GOFLAGS=-count=1` so no
result came from the test cache, about 51 s a run, every summary `modules=2 ran=2 passed=2 failed=0`.

| Experiment | Runs | Failures | Task |
|------------|------|----------|------|
| Solo, back to back | 5 | 0 | `1794620745-spawn-100fae85-bafb691b` |
| Overlapping pairs, the second run starting 2 s after the first | 6 (3 pairs) | 0 | same |
| Churn mid-run, recreating both failing windows: `muxcode upgrade-daemons --force --session muxcode` about 10 s in (what every build does — the 18:04:42 restart) and `muxcode agent config review` from the repository root about 25 s in (the `.codex` rewrite of the 18:57:34 review reload) | 3 | 0 | `1794621229-spawn-100fae85-8d334bb4` |

What that shows, and its limit (corrected after review, which found the first wording claimed more
than the runs support): no failure was reproduced in five solo runs, three overlapping pairs at one
2 s offset, or three runs under one churn schedule. That is an observation about those exact
configurations and nothing wider — it excludes no cause, at any frequency, outside them. The
original cause remains undetermined.

**What the failing runs' own transcripts show** (the test agent's Codex rollout for the day):

- Both failures were the graph's test node, the first suite run after a build node — build with
  install and daemon restart at 18:04:28–18:04:42, test 18:04:57–18:05:52; build at 18:57:32, test
  to 18:58:33. No `editing` lifecycle row falls inside either run and no second `./test.sh`
  overlapped either. Neither a mid-edit tree nor an overlapping run is evidenced for these two.
- Each run's output reached the agent as one Codex-truncated chunk — "truncated output (original
  token count: 123161 / 123348)", 8967 lines — of which only the head and the tail (the `tui`
  package, `FAIL`, the summary) survive. No `--- FAIL`, `_test.go:N:` or panic line survives in
  either. The failing test's name was in the truncated middle. This is the loss Phase 1 prevents:
  the summary now names the test and the log keeps the output.
- 18:05 — the agent then ran `go test -v ./...` in `tools/muxcode` to a file, which passed, and
  found no FAIL in it.
- 18:58 — the agent re-ran `TestExecUnverifiedHoldReleasedByOptedInAgent` alone and it passed. That
  name came from an edit request at 18:22 mentioning a new test, not from the failure output: a
  guess, not evidence.
- **The module mismatch in the 18:59 account is resolved**: the test agent's own reply said the
  harness module failed and `muxcode` passed, the reverse of its run's summary. The account
  repeated the agent's misreport; `muxcode` is the module that failed both times.

**Decision (step 2): undetermined.** No failure appeared in the 9 runs under the one overlap offset
and the one churn schedule tried; neither failing run is evidenced as mid-edit or overlapped. A
flaky test, or an interference under a timing not tried, both remain possible, and nothing can be
named from the 2026-11-13 evidence, which was lost to output truncation.

**Step 3 cannot be met as written.** No flaky test was identified, so there is nothing to fix and
no cause to file as a defect. What Phase 1 changes is the next occurrence: a failing `./test.sh`
now names `FAIL <package>: <tests>` and keeps the full log. The acceptance criterion "the flaky
test or tests behind the 2026-11-13 failures are identified, or the failures are shown to come from
overlapping or mid-edit runs" is likewise unmet: closing it needs a new occurrence, now nameable,
or the user narrowing the criterion. No follow-up defect has been filed — that is the user's call.

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
- [MUX-214](../completed/MUX-214-root-go-vet-false-alarm-from-the-build-agent.md) — the false alarms; this
  spec is about the real failures
- [MUX-152](./MUX-152-test-sh-hides-modules-after-first-failure.md) — `test.sh` hiding modules after
  the first failure; the summary this builds on came from MUX-174
- [MUX-153](./MUX-153-codex-test-agent-cannot-run-the-suite.md) — the Codex test agent and the suite
- [MUX-217](../completed/MUX-217-agent-defs-reload-races-a-graph-dispatch-and-clears-it.md) — a test node of this
  spec's run failed without running the suite, when a watchdog reload cleared its dispatch

## Status

Backlog — **parked 2026-11-13** on the user's decision relayed by edit: the flaky test was not
reproducible in 14 runs. Reopen on a named failure — a failing `./test.sh` now names its packages
and tests and keeps the log, so the next occurrence can be identified. 7 of 17 boxes are ticked.
Phase 1 is complete and reviewed, and its capture work stays in the tree (uncommitted at parking):
`test.sh`, `tools/muxcode/bus/hook.go` (`clipOutput`), `agents/test-runner.md`,
`scripts/test-test-sh-coverage.sh`, `CLAUDE.md` and `docs/agents.md`. Open: Phase 2 (3 steps — not
meetable without a new occurrence), Phase 3 (4 steps) and acceptance criteria 2, 3 and 4.

It was activated 2026-11-13 by `55-defect-backlog-loop` run `1794608375` (Defects rank 1); that run
is canceled.

Filed 2026-11-13 on the user's request relayed by edit, ranked directly after MUX-213 and MUX-214
and eligible for `55-defect-backlog-loop`. Two failures are on record by time and module — the
second with a module that disagrees with the account — and neither names a test. The cause is
**not determined**.
