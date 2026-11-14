# MUX-218: A Build Agent's Failing Vet Row Is Filed as Build Evidence but Rejected as Build Evidence

**Tracking:** [mkober/muxcode#197](https://github.com/mkober/muxcode/issues/197)

[MUX-214](./MUX-214-root-go-vet-false-alarm-from-the-build-agent.md) made a build
agent's failing `go vet` a build precheck: the row lands in build history and edit is told the
build failed. The graph executor was not told. It re-classifies the row's command without the role,
reads it as a test precheck, and refuses it as evidence for a `build` node — so a build node whose
vet failed can be recorded on the agent's own claim alone, including a claim that contradicts the
row.

## Context

### Source and standard of evidence

Filed 2026-11-13 on the user's request relayed by edit (`/tmp/defect-218.md`). The finding is
Copilot's, on merged PR #192, review thread 4570625190 at `bus/hook.go:1123`. Plan read
`StageForRole`, `rowAttributesTo`, `actionEvidenceTypes` and `deriveSendOutcome` in the working
tree and confirms the mismatch below. It has not been observed in a run, and nothing was run for
this spec.

### Mechanism — read in code

| Step | Where | What happens |
|------|-------|--------------|
| The hook stages by role | `StageForRole`, `bus/hook.go` | For the build agent, or a spawned worker whose base role is build, a `CmdTestPrecheck` becomes `CmdBuildPrecheck`. A failing `go -C <module> vet ./...` writes a build-history row |
| The executor attributes by command alone | `rowAttributesTo`, `bus/graph_exec.go` | For action `build` it accepts rows whose `ClassifyCommand(row.Command)` is `CmdBuild` or `CmdBuildPrecheck`. `ClassifyCommand` takes no role, so the vet row classifies as `CmdTestPrecheck` and is rejected |
| The outcome falls through to the claim | `deriveSendOutcome` | With no attributable row, an `EXIT=<n>` sentinel in the reply decides the node. The conflict check (`graph-outcome-conflict`) only runs against an attributable row, so it never sees the vet failure |

### Consequences

| Reply after a failing vet | Recorded today | Should be |
|---------------------------|----------------|-----------|
| `EXIT=1` | failure — right, but on the claim, not the row | failure, on the row |
| `EXIT=0` (an echoed request, a mistaken summary) | **success** — the contradicting row is ignored | held as `graph-outcome-conflict` |
| No sentinel | unverified hold, logged `graph-outcome-untied`: "a failure row … cannot testify for action build" | failure, on the row |
| `./build.sh` then run and passing in the same node | success, on the later build row | unchanged — the latest attributable row decides; out of scope here |

The same row is correctly rejected for a `test` node run by the test agent's window — it is in
build history, which that node never reads — so the gap is one-sided: the build node.

## Requirements

### Acceptance criteria

- [x] A failing vet row written by the build agent testifies for a graph `build` node: with no
  sentinel in the reply the node is recorded `failure` on the row — script section E
- [x] A build reply claiming `EXIT=0` beside that failing row is held with `graph-outcome-conflict`,
  not recorded success — script section E, with the pre-fix build as the control
- [x] The same holds for a spawned worker whose base role is build — unit level
- [x] Negative control: a test precheck row in the **test** agent's history still testifies for a
  `test` node and never for a `build` node
- [x] Negative control: a passing `./build.sh` row still decides a build node as today
- [x] The hook's staging and the executor's attribution use one rule, so they cannot drift apart
  again — both call `StageForRole`
- [x] `bash scripts/test-root-go-vet.sh` covers the graph build node and its floor is raised — 34

### Technical approach

Attribute with the role the row was written under. `deriveSendOutcome` already knows the node's
role; pass it to `rowAttributesTo` and classify through `StageForRole(session, role,
ClassifyCommand(row.Command))` — the same function the hook used when it chose the history file.
One rule, two callers.

Rejected: adding `CmdTestPrecheck` to the `build` entry of `actionEvidenceTypes`. It would also let
a vet row from some other window testify for a build node if the lookup ever widened, and it leaves
the two classifications free to diverge on the next role-dependent stage.

Not in scope: a later passing build row masking an earlier failing vet row in the same node. That
is the "latest row decides" rule, shared by every action.

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/graph_exec.go` | `rowAttributesTo`, `actionEvidenceTypes`, `deriveSendOutcome` |
| `tools/muxcode/bus/hook.go` | `StageForRole`, `ClassifyCommand` |
| `tools/muxcode/bus/graph_exec_test.go` | The attribution table tests (`rowAttributesTo` cases) |
| `scripts/test-root-go-vet.sh` | MUX-214's script, extended here |

## Implementation

### Phase 1: Pin

- [x] Unit test: `rowAttributesTo("build", …)` on a `go -C tools/muxcode vet ./...` row returns
  false today (pin red) — the `defect MUX-218` case in `TestRowAttributesTo`
- [x] Unit test: a build node with that failing row and a reply ending `EXIT=0` is recorded success
  today (pin red) — `TestBuildNodeVetRowAttribution`, which also pins the no-sentinel reply as
  unknown/untied
- [x] Unit test (negative control, stays green): a `./build.sh` row decides a build node; a vet row
  in test history decides a test node

### Results for Phase 1 (2026-11-14)

Verified by plan against the tree. One file changed, `tools/muxcode/bus/graph_exec_test.go` (last edit 1794685404), then the build (1794685449), `./test.sh` (2 of 2 modules, 1794685514) and the review (0 must-fix, 0 should-fix, 0 nits, `/tmp/muxcode-review-1794685514.txt`). No production change.

"Pin red" is read as the worker implemented it: the pins assert today's defective behaviour, so they **pass** now and are the exact assertions Phase 2 inverts. They are marked `defect` in the test names.

| Pin | Today | After Phase 2 |
|-----|-------|---------------|
| `rowAttributesTo("build", "go -C tools/muxcode vet ./...")` | false | true for a build-role node |
| Build node, failing vet row, reply ends `EXIT=0` | success | held with `graph-outcome-conflict` |
| Build node, failing vet row, reply with no sentinel | unknown, untied | failure |
| Control: passing `./build.sh` row | decides the build node | unchanged |
| Control: failing `go vet ./...` row in test history | fails the test node | unchanged |

### Phase 2: One rule

- [x] `rowAttributesTo` takes the node's role and classifies through `StageForRole` — and the
  session, which `StageForRole` needs to resolve a spawn's base role
- [x] Invert the Phase 1 pins: the failing vet row fails the build node, and `EXIT=0` beside it is
  held as a conflict — `TestBuildNodeVetRowAttribution`, which also asserts the
  `graph-outcome-conflict` row is logged
- [x] A spawned build worker's row is attributed the same way — a persisted spawned build role in
  `TestRowAttributesToStagesByRole`
- [x] The negative controls stay green

### Results for Phase 2 (2026-11-14)

Verified by plan against the tree. Last source edit 1794685657 (`graph_exec.go` at 1794685592, its test file after), then the build (1794685703), `./test.sh` (2 of 2 modules, 1794685768) and the review (0 must-fix, 0 should-fix, 0 nits, `/tmp/muxcode-review-1794685768.txt`).

| Item | State |
|------|-------|
| The change | `rowAttributesTo(session, role, action, row)` (`bus/graph_exec.go`): the type branch reads `StageForRole(session, role, ClassifyCommand(row.Command))`. Its one caller, `deriveSendOutcome`, passes the normalized history role |
| Left alone, deliberately | The command-pattern branch (`actionEvidenceCommands`) and the unmapped-action fallback |
| One rule | The executor now calls the function `ProcessBashHook` stages with; there is no second classifier to drift |
| Tests | Both Phase 1 defect pins inverted; conflict logging asserted; passing-`./build.sh` and test-history-vet controls kept; build/test cross-attribution and a spawned build role covered |

Criteria ticked here are the ones a unit test settles. The two that describe a graph build node's recorded outcome wait for Phase 3's script, which runs a real one-node graph.

### Phase 3: Integration test

- [x] Extend `scripts/test-root-go-vet.sh`: a one-node graph `build` run whose build agent's vet
  fails — the node ends `failure` on the row with no sentinel, and holds on a reply claiming
  `EXIT=0` — section E, a scratch daemon and a real `go -C tools/broken vet`
- [x] Negative control: with the role-aware attribution removed, the `EXIT=0` case is recorded
  success — proving the check can see it — a muxcode built from a copy of the tree
- [x] Raise the floor; run through the run agent and record the counts here — 26 → 34; 34 passed,
  0 failed (proc `1794686069-proc-e8eae729`)
- [x] Update `CLAUDE.md` (hook-driven chains entry) and `docs/hooks.md`

### Results for the integration phase (2026-11-14)

Verified by plan against the tree and the proc log. The Go sources are unchanged since Phase 2's verified round (last edit 1794685657). The script's last edit (1794686061) precedes its run (1794686069, exit 0); the review (0 must-fix, 0 should-fix, 1 nit, `/tmp/muxcode-review-1794686065.txt`) read that run. The round's build (1794686049) and `./test.sh` (2 of 2 modules, 1794686065) finished within seconds of the script's last edit; neither builds nor runs the script, so its own run is the evidence for it. The worker's phase report describes the first run (32 checks).

| Section E check | Result |
|-----------------|--------|
| No sentinel in the reply | The build node fails on the build agent's vet row |
| Reply claims `EXIT=0` | The node holds, outcome unknown |
| `graph-outcome-conflict` | Logged, naming the failing vet command |
| Isolation | The scratch sessions live on a private tmux server; the user's real server holds none (added in the second run — the defect [MUX-226](../backlog/MUX-226-graph-node-caps-script-not-isolated-from-real-tmux-and-binary.md) records for another script) |
| Control | Role-aware attribution removed from a copy of the executor; that muxcode builds; under it the `EXIT=0` claim records success |

Sections A–D (MUX-214) still pass. The review's nit — `CLAUDE.md` naming floor 32 — is fixed: the entry reads floor 34. `docs/hooks.md` gained one sentence on the "Test precheck patterns" bullet (plan), `CLAUDE.md` its hook-driven-chains clause and script entry (edit).

## Related

- [MUX-214](./MUX-214-root-go-vet-false-alarm-from-the-build-agent.md) — the staging
  change this follows
- [MUX-185](../backlog/MUX-185-history-row-provenance-declared-not-proven.md) — a history row's provenance
- [MUX-170](../backlog/MUX-170-graph-dispatch-adopts-foreign-in-flight-task.md) — another way a node is
  credited with evidence that is not its own

## Status

Complete — closed 2026-11-14 by `55-defect-backlog-loop` run `1794626267-55-defect-backlog-loop-15547e2e`, 18/18 (activated the same day from Defects rank 16; ranks 1–15 were parked). Checked against the tree at activation: `rowAttributesTo` (`bus/graph_exec.go:2598`) still takes the action alone and never calls `StageForRole`, so the spec stands as written.

Filed 2026-11-13 on the user's request relayed by edit, eligible for `55-defect-backlog-loop`, ranked at the
end of tier 1 (repair the instruments): Medium, no open design decision, behind the tier's older
rows. The
mismatch is confirmed in code and has not been observed in a run. The PR #192 review thread is to
be answered with a pointer to this spec's issue — the commit agent's work, on the user's request.
