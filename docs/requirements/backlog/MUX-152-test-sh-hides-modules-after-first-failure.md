# `test.sh` Hides Every Module After the First Failure

`test.sh` runs each Go module under `set -euo pipefail` in glob order, and the glob puts
`tools/muxcode-llm-harness/` before `tools/muxcode/` because `-` (0x2D) sorts before `/` (0x2F). A
failure anywhere in the harness module — `go vet` or `go test` — aborts the script before the bus
module starts, and the run still prints every harness test that passed. Observed 2026-09-08: a run
reported **65 pass / 1 fail** while none of `bus`, `cmd`, `daemon` or `tui` had executed. The true
figure once they ran was **2901**.

The exit code is honest — `set -e` makes it non-zero — so CI gates correctly. What lies is the
**output**: it reads as a result with a pass count, and nothing says "two of three modules did not
run". An agent summarizing that output, or a person skimming it, sees a mostly-green run with one
failure to fix, not a suite that was 2 % executed.

Tracking: _(no GitHub issue yet)_

## Context

### Observed (2026-09-08, reported by edit from the validation run)

| | |
|---|---|
| Trigger | a harness test failure (`TestProcessBatch_SimpleResponse`, see MUX-153) |
| Reported | "65 pass / 1 fail" |
| Actually executed | the harness module only — `bus`, `cmd`, `daemon`, `tui` never started |
| After the harness was fixed | 2901 pass / 0 fail across six packages (read from the run agent's pane by plan) |

### Mechanism — verified from `test.sh`

```bash
set -euo pipefail
for moddir in "$REPO_DIR"/tools/*/; do
  [ -f "$moddir/go.mod" ] || continue
  (cd "$moddir" && go vet ./...)
  (cd "$moddir" && go test -v ./...)
done
```

| Fact | How established |
|------|-----------------|
| Glob order is harness first | `ls -d tools/*/` → `tools/muxcode-llm-harness/`, then `tools/muxcode/` |
| `set -e` aborts the script at the first non-zero subshell | `test.sh:2` |
| No summary, no "N modules not run" line | `test.sh` has nothing after the loop |
| `CLAUDE.md` describes `test.sh` as running vet + test "in the bus module" | stale — the loop covers every module with a `go.mod` |

### Why it matters

This is the class of failure the coverage floors in `scripts/test-*.sh` exist to prevent: a partial
run that looks like a result. The asymmetry makes it worse — the harness is the small module (~65
tests) and runs first, so a harness break hides ~2900 tests while a bus break hides nothing. The
build→test→review chain, `verify-spec`, and the release gate all consume this script's output.

Discovery is also serialized: while the harness is broken, nothing about the bus module can be
learned — its own failures wait behind a module that has nothing to do with them.

### Relationship

| Spec | Relationship |
|------|--------------|
| [`MUX-153`](./MUX-153-codex-test-agent-cannot-run-the-suite.md) | Why the harness module is the one most likely to fail first on the agent meant to run the suite — its socket-binding test cannot run under the codex sandbox. That defect supplies the failure; this one hides its scope |
| [`MUX-154`](../drafts/MUX-154-codex-status-line-closes-tracked-tasks.md) | The other way a test run reports something that did not happen |

## Requirements

### Acceptance criteria

- [ ] A failure in one module never prevents the other modules from running
- [ ] The output ends with one line per module naming its verdict — `pass`, `FAIL`, or `not run` —
      so a partial run cannot read as a complete one
- [ ] The exit code is non-zero if any module failed or did not run (negative control: it already
      is on failure — must not regress)
- [ ] Negative control: an all-green run's output and exit code are unchanged in meaning
- [ ] `CLAUDE.md`'s `test.sh` row describes what the script runs

### Technical approach

Collect per-module status rather than aborting: run each module's vet and test with `|| status=$?`
(or drop `set -e` around the loop body), record the verdict, keep going, print a summary table after
the loop, and exit with the worst status. Keep `go test -v` so per-test output is unchanged.

### Key files

| File | Purpose |
|------|---------|
| `test.sh` | The loop and the missing summary |
| `CLAUDE.md` | Build/test table row — stale description |
| `.github/workflows/release.yml` | Runs `./test.sh` as the release gate — gates correctly on exit code today, but its log inherits the same blind spot |

## Implementation

### Phase 1: Pin

- [ ] Pin current behaviour against a scratch tree with two modules: the first fails → the second
      never runs, the output ends in a pass count, and no line names the module that did not run

### Phase 2: Per-module verdicts

- [ ] Run every module regardless of earlier failures and collect each verdict
- [ ] Print a summary — one line per module: `pass` / `FAIL` / `not run` — and exit non-zero on any
      failure or unrun module
- [ ] Negative control: an all-green run prints all `pass` and exits 0
- [ ] Update the `CLAUDE.md` row

### Phase 3: Integration test

- [ ] Create `scripts/test-test-sh-summary.sh` against a scratch tree with two Go modules
- [ ] Test: first module fails → second still runs, summary names both verdicts, exit non-zero
- [ ] Test: both pass → summary all `pass`, exit 0
- [ ] Test: a directory without `go.mod` is named as skipped, not silently absent
- [ ] Coverage floor keeps a skipped section from reporting green
- [ ] Run the script and verify all checks pass

## Notes

Filed 2026-09-08 by plan from edit's handoff (`/tmp/mux-new-findings-20260908.md`). The mechanism
was verified from `test.sh` and the glob order from the filesystem; the "65 pass / 1 fail" figure is
edit's observation from the run and is recorded as reported.

## Status

**Backlog** — filed 2026-09-08. Not started.
