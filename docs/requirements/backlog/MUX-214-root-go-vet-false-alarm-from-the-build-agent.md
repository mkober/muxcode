# MUX-214: The Build Agent's Root `go vet` Fails Every Time and Tells Edit the Tests Failed

**Tracking:** [mkober/muxcode#189](https://github.com/mkober/muxcode/issues/189)

Before every build, the build agent's lint step runs `go vet ./...` at the repository root. The
root is not a Go module — the two modules live under `tools/` — so the command fails every time
with `directory prefix . does not contain main module`. `go vet` is a **test precheck** pattern,
and a failing precheck fires the test chain's failure edge, so edit receives "Tests FAILED (exit 1):
go vet ./... — check test window" from the *build* agent, about a run that tested nothing, while the
build itself then succeeds. On 2026-11-13 that notice reached edit at least ten times.

## Context

### Source and standard of evidence

Filed 2026-11-13 on the user's request relayed by edit (`/tmp/defect-flaky-and-govet.md`), from a
finding in [MUX-213](../completed/MUX-213-unrequested-test-runs-fire-review-chain-and-reviews-answer-twice.md)'s
Phase 1. The counts are plan's, read from session `muxcode`'s bus stores since the 15:06 re-init;
the agent definition and the pattern were read at `67004ed`. Which code path turns a build-pane
`go vet` failure into a test-chain notice was **not** traced by plan — Phase 1 does that.

### Observed — verified by plan

| Fact | Where |
|------|-------|
| 19 `go vet ./...` rows in `test-history.jsonl`, every one `exit_code 1`, output `pattern ./...: directory prefix . does not contain main module or its selected dependencies` | `test-history.jsonl` |
| The repository has no root `go.mod`; the modules are `tools/muxcode` and `tools/muxcode-llm-harness` | `ls` |
| 10 notices `build → edit`, type `event`, action `notify`, payload `Tests FAILED (exit 1): go vet ./... — check test window`, between 16:13:14 and 18:21:44 | `log.jsonl` |
| The build agent's own reply names it: "Build succeeded: gofmt clean; root go vet could not run because the repository root is not a Go module; ./build.sh built and installed 2 Go modules" | `log.jsonl`, `build → edit` |
| The build agent's lint table: detect `go.mod` → `go vet` → `go vet ./...`, run before the build | `agents/code-builder.md:36-43` |
| `go vet` is the one default test precheck pattern | `DefaultTestPrecheckPatterns = []string{"go*vet"}`, `bus/hook.go:398` |
| `./test.sh` runs `go vet ./...` **inside each module** and reports `failed (go vet)` per module | `test.sh` |

MUX-213's Phase 1 worker counted 20 such notices and 21 vet rows for the day; plan's counts above
are from the stores as they stood at about 19:00 and are lower. The difference is not reconciled.

### Mechanism — partly verified

- **Verified:** the vet rows land in `test-history.jsonl` although the build agent ran them — a
  history file is chosen by command class, not by the role that ran the command (MUX-213 Findings
  for Phase 1, `bus/hook.go:1159-1214`).
- **Verified:** a test precheck feeds the test chain only when it fails (`CLAUDE.md`, hook-driven
  chains) — which is exactly this case, every time.
- **Not traced:** why the lint step runs `go vet ./...` at the root when its own table says to
  detect `go.mod` there. Either the agent detects a `go.mod` anywhere in the tree, or it runs the
  table unconditionally.

### Why it matters

- Edit is told tests failed when no test ran and the build passed. Each notice costs a turn and
  reads exactly like a real failure — the same notice MUX-213 found arriving from mid-edit runs.
- The vet the notice is about never ran, so a real vet finding in either module is not reported by
  this step at all; only `./test.sh` runs vet where it can work.
- Any repository whose Go modules are not at its root gets the same false alarm on every build.

## Requirements

### Acceptance criteria

- [ ] A build in a repository with no root Go module sends edit no "Tests FAILED" notice and writes
  no failing `go vet` row, when every module vets clean
- [ ] `go vet` still runs before a build where it can: inside each Go module the repository has, as
  `./test.sh` does
- [ ] A real vet finding in a module is still reported, and names the module
- [ ] Negative control: a repository with a root `go.mod` and a real vet failure still fails the
  lint step and notifies edit
- [ ] The cause recorded in Phase 1 is fixed where it is — the agent definition, the precheck
  classification, or both — and the choice is recorded here
- [ ] `docs/agents.md` (the build agent's lint step) and `docs/hooks.md` (test prechecks) say where
  vet runs and which role's failure reaches edit as a test failure

### Technical approach

**Primary — vet per module, in the build agent's lint step.** The lint table's Go rows run where a
`go.mod` is: for each directory holding one, `go vet ./...` from that directory, matching
`./test.sh`. With no module at the root, nothing runs at the root. This removes the failing command
rather than hiding its notice.

**Secondary, only if Phase 1 shows it is needed — the notice's wording.** A precheck that fails in
the *build* pane reaches edit as "Tests FAILED … check test window". If a legitimate build-side vet
failure can still take that road after the primary fix, the notice should name the role and stage
that actually failed.

**Rejected — drop `go vet` from the test precheck patterns.** The pattern exists so a failing vet
in the test pane stops the chain; removing it trades a false alarm for a missed one.

### Key files

| File | Purpose |
|------|---------|
| `agents/code-builder.md:36-43` | The lint step and its Go rows |
| `tools/muxcode/bus/hook.go:376-398` | Build and test precheck patterns |
| `tools/muxcode/bus/hook.go:1159-1214` | History file chosen by command class |
| `tools/muxcode/cmd/hook.go` | `triggerChain` — the failure edge the precheck fires |
| `test.sh` | The per-module vet to mirror |

## Implementation

### Phase 1: Trace the false alarm

- [ ] Reproduce on a scratch repository with no root `go.mod` and one module under a subdirectory:
  a build-role `go vet ./...` at the root → record the history row and the notice it produces
- [ ] Record why the lint step runs at the root — how `agents/code-builder.md`'s "detect `go.mod`"
  is being applied
- [ ] Record the code path from a build-pane precheck failure to the "Tests FAILED" notice

### Phase 2: Fix

- [ ] The build agent's lint step vets each Go module from its own directory and runs nothing at a
  root that is not a module
- [ ] If Phase 1 shows a build-side precheck failure is mislabelled, the notice names the failing
  role and stage; a test that fails without the change
- [ ] Update `docs/agents.md` and `docs/hooks.md`

### Phase 3: Integration test

- [ ] Create `scripts/test-root-go-vet.sh` — hermetic: scratch repository with modules under a
  subdirectory, scratch `BUS_SESSION`, payloads fed to the real `hook bash` as the build role
- [ ] A clean build cycle → no failing vet row, no notice to edit
- [ ] A real vet failure inside a module → reported, naming the module
- [ ] Negative control: a root-module repository with a vet failure → edit is notified
- [ ] Coverage floor; run through the run agent and record the counts here

## Related

- [MUX-213](../completed/MUX-213-unrequested-test-runs-fire-review-chain-and-reviews-answer-twice.md) —
  where this was found; the other source of false FAILED notices
- [MUX-152](./MUX-152-test-sh-hides-modules-after-first-failure.md) — `test.sh` and its per-module
  reporting
- [MUX-215](./MUX-215-non-reproducible-test-sh-failures-and-lost-failing-output.md) — the failures
  that are real but cannot be named

## Status

Backlog

Filed 2026-11-13 on the user's request relayed by edit, ranked directly after MUX-213 and eligible
for `55-defect-backlog-loop`. The failing command, its rows and the notices are verified by plan;
the path from the build pane to the test-chain notice is not traced. Not started.
