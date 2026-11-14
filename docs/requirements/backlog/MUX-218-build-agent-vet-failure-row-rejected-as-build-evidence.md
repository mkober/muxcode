# MUX-218: A Build Agent's Failing Vet Row Is Filed as Build Evidence but Rejected as Build Evidence

**Tracking:** [mkober/muxcode#197](https://github.com/mkober/muxcode/issues/197)

[MUX-214](../completed/MUX-214-root-go-vet-false-alarm-from-the-build-agent.md) made a build
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

- [ ] A failing vet row written by the build agent testifies for a graph `build` node: with no
  sentinel in the reply the node is recorded `failure` on the row
- [ ] A build reply claiming `EXIT=0` beside that failing row is held with `graph-outcome-conflict`,
  not recorded success
- [ ] The same holds for a spawned worker whose base role is build
- [ ] Negative control: a test precheck row in the **test** agent's history still testifies for a
  `test` node and never for a `build` node
- [ ] Negative control: a passing `./build.sh` row still decides a build node as today
- [ ] The hook's staging and the executor's attribution use one rule, so they cannot drift apart
  again
- [ ] `bash scripts/test-root-go-vet.sh` covers the graph build node and its floor is raised

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

- [ ] Unit test: `rowAttributesTo("build", …)` on a `go -C tools/muxcode vet ./...` row returns
  false today (pin red)
- [ ] Unit test: a build node with that failing row and a reply ending `EXIT=0` is recorded success
  today (pin red)
- [ ] Unit test (negative control, stays green): a `./build.sh` row decides a build node; a vet row
  in test history decides a test node

### Phase 2: One rule

- [ ] `rowAttributesTo` takes the node's role and classifies through `StageForRole`
- [ ] Invert the Phase 1 pins: the failing vet row fails the build node, and `EXIT=0` beside it is
  held as a conflict
- [ ] A spawned build worker's row is attributed the same way
- [ ] The negative controls stay green

### Phase 3: Integration test

- [ ] Extend `scripts/test-root-go-vet.sh`: a one-node graph `build` run whose build agent's vet
  fails — the node ends `failure` on the row with no sentinel, and holds on a reply claiming
  `EXIT=0`
- [ ] Negative control: with the role-aware attribution removed, the `EXIT=0` case is recorded
  success — proving the check can see it
- [ ] Raise the floor; run through the run agent and record the counts here
- [ ] Update `CLAUDE.md` (hook-driven chains entry) and `docs/hooks.md`

## Related

- [MUX-214](../completed/MUX-214-root-go-vet-false-alarm-from-the-build-agent.md) — the staging
  change this follows
- [MUX-185](./MUX-185-history-row-provenance-declared-not-proven.md) — a history row's provenance
- [MUX-170](./MUX-170-graph-dispatch-adopts-foreign-in-flight-task.md) — another way a node is
  credited with evidence that is not its own

## Status

Backlog

Filed 2026-11-13 on the user's request relayed by edit, eligible for `55-defect-backlog-loop`, ranked at the
end of tier 1 (repair the instruments): Medium, no open design decision, behind the tier's older
rows. The
mismatch is confirmed in code and has not been observed in a run. The PR #192 review thread is to
be answered with a pointer to this spec's issue — the commit agent's work, on the user's request.
