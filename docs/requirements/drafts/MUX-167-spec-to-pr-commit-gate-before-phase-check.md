# spec-to-pr Asks for a Commit Approval Before It Checks the Phase Is Complete

`spec-to-pr` routes `update-spec` → `phase-gate` → `commit`, and the phase-progress guard that
decides whether the phase is actually complete runs at `commit` dispatch — *after* the human has
approved. When the phase is still open the guard declines, the run falls to `stuck-gate`, and the
human approves a second gate to retry: two gates per lap for one decision, the first of them wasted.
Worse, the wasted gate carries the wrong name — its label is `${completed_phase}`, the completion
frontier, which on an open phase is the *previous* phase's title. And the most common way a phase
stays open — a review that returns `EXIT=0` with should-fixes — never routes to `fix`, so the
stuck-gate has become the should-fix loop. On 2026-09-09 one run paid this four times.

## Context

### Observed (run `1788966148-spec-to-pr-2338488d`, session `muxcode`, MUX-163)

From the session log (`graph-gate-approved`, `graph-guard-declined` rows):

| Time | Event | The phase at that moment |
|------|-------|--------------------------|
| 12:24:50 | `phase-gate` approved → **guard declined** 0 s later: "0 commits shipped but only 0 phases complete" | Phase 1 at 1/2 (matrix unrecorded); label read "(no completed phase)" |
| 12:25:02 | `stuck-gate` approved | — |
| 13:03:44 | `phase-gate` approved → commit `67ad9dc` | Phase 1 complete |
| 13:24:46 | `phase-gate` approved → **guard declined** 1 s later: "1 … only 1" | Phase 2 at 5/6; label named Phase 1 |
| 13:25:04 | `stuck-gate` approved | — |
| 13:52:54 | `phase-gate` approved → commit `8d48888` | Phase 2 complete |
| 14:09:30 | `phase-gate` approved → commit `7191251` | Phase 3 complete |
| 14:50:02 | `phase-gate` approved → **guard declined** 3 s later: "3 … only 3" | Phase 4 at 0/5; label named Phase 3 |
| 14:50:20 | `stuck-gate` approved | — |
| 15:04:59 | `phase-gate` approved → **guard declined** 1 s later: "3 … only 3" | Phase 4 at 3/5; label named Phase 3 |
| 15:05:02 | `stuck-gate` pending | — |

Totals for one run: **7 phase-gate approvals, 4 declined within a second of approval, 3 (soon 4)
stuck-gate approvals** — eleven human gates for three commits. Every declined lap had the same
cause: the lap's review returned `EXIT=0` with should-fixes (13:21, 14:47, 14:58) or plan's verify
left the phase open (12:24), and the graph had no way to know before asking.

A second cost showed up on the same run: the worker never ran the phase's integration script
through the run agent, so plan had to dispatch the scripts itself (14:45, 14:46) before it could
credit Phase 4 — the `implement`/`fix` messages do not say to.

### Mechanism — verified in code

- `bus/graph_templates.go:41–45, 58–62` — `update-spec` → `phase-gate` (`wait_human`, "Approve
  committing ${completed_phase}") → `commit` (guard `phase-progress`); `commit` failure →
  `stuck-gate` → `implement`. No node between `update-spec` and `phase-gate` reads the spec.
- `bus/graph_exec.go:462–497` — `phaseProgressGuardAllows` runs when the `commit` node dispatches:
  `SpecCompletedPhaseCount(path) < prior+1` → `declineGuard`. Correct, and too late — the human
  approved one node earlier.
- `bus/graph_exec.go:611–626`, `bus/spec_items.go:144–157` — `${completed_phase}` is
  `SpecJustCompletedPhase`: "the last phase in file order with zero open items before the first open
  one". Chosen so a commit is labelled with the phase it ships (MUX-121); on an open phase it names
  the phase *before* the one whose work is in the tree — exactly the laps the guard will decline.
- `bus/graph_templates.go:57` and `agents/code-reviewer.md:16, 21` — `review` → `fix` only on
  `outcome: failure`, and the reviewer's sentinel is `EXIT=0` unless a must-fix exists. A should-fix
  therefore rides `success` into `update-spec`, where plan re-opens the step, and the phase reaches
  the gate open. The routing is deliberate for must-fix; it simply has no branch for "green but the
  phase is not done".
- `bus/conditions.go:109` — `spec_phases_remaining` is the only spec-aware condition; it answers
  "is anything open anywhere", not "is the phase just worked complete".

### Scope boundary

The phase-progress guard stays as the runtime backstop — the condition proposed here routes, the
guard still refuses. The reviewer's `EXIT` semantics stay (a should-fix is not a must-fix). Not in
scope: the `update-spec` node firing before plan's ticks land (14:47:51 read "(no open phase)"),
which is a separate ordering question between the worker's request to plan and the node.

## Requirements

### Acceptance criteria

- [ ] An incomplete phase never asks a human to approve its commit: after `update-spec`, a phase-complete condition routes an open phase straight to `stuck-gate` without visiting `phase-gate` — replayed, the 2026-09-09 run records 0 approvals declined within a second
- [x] `phase-gate` is reached only when the phase is complete, so its `${completed_phase}` label always names the phase whose work the commit ships — never the previous one — _by the template's edges plus the shared predicate: the gate is downstream of `phase-check` success only (review 15:15:40)_
- [x] The phase-progress guard remains and still declines when the condition and the guard disagree (negative control: a spec edited between the condition and the commit) — _the guard calls the same `phaseCommitReady` at dispatch; the reopened-spec case is pinned in the executor test (15:18:40)_
- [ ] A review that returns `EXIT=0` with should-fixes and leaves the phase open reaches `stuck-gate` → `implement` at the cost of one gate, not two; a lap that closes the phase proceeds to `phase-gate` as before
- [ ] `implement` and `fix` messages tell the worker to run the phase's integration script through the run agent before reporting, and to report the counts with the run task id, so plan's verify has store rows without dispatching the scripts itself
- [ ] `graph validate` passes for every builtin template; `scripts/test-multi-phase-graph.sh` covers the new routing on both branches
- [ ] Docs: `docs/architecture.md` graph section, `docs/agent-bus.md` template reference, `CLAUDE.md` graph-orchestration constraint name the phase-complete condition and the one-gate rule

### Technical approach

**Primary (edit's, as implemented 15:13).** A condition type `spec_phase_committable` in
`bus/conditions.go` whose value names the guarded commit node (`{"spec_phase_committable":
"commit"}`), evaluated through the **same predicate the guard uses** — `phaseCommitReady(session,
run, graph, nodeID)` in `bus/graph_exec.go`, which `phaseProgressGuardAllows` now calls — so the
pre-gate check and the dispatch-time guard cannot disagree by construction; the condition fails
closed without a graph-run context (`ChainContext.GraphRun`/`Graph`, set for condition nodes) or a
named node. `spec-to-pr` gains the `phase-check` condition node: `update-spec` → `phase-check`;
success → `phase-gate`; failure → `stuck-gate`. `validateNode` rejects a `spec_phase_committable`
check naming a node that does not carry the `phase-progress` guard, so a template cannot route on a
predicate the commit will not enforce. The guard on `commit` stays as the runtime backstop.
`${completed_phase}` needs no change: once the gate is reachable only on a committable phase, the
frontier *is* the phase being shipped. The `implement` and `fix` node messages gain one sentence:
run the phase's `scripts/test-*.sh` via the run agent before reporting, and report counts plus the
task id.

**Rejected — route should-fixes to `fix`.** Making the reviewer emit `EXIT=1` for should-fixes
would send every nit-level lap through the fix worker and overload the must-fix signal that
`commit-pr-review-loop` and the review chain also read. The phase-complete condition catches the
same laps at the right place — the spec, which is what plan updates from the review.

**Rejected — label the gate with `${current_phase}`.** MUX-121 moved it off that for a reason: at
gate time the pointer is already on the next phase. The fix is to not reach the gate on an open
phase, not to relabel a gate that should not be asked.

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/graph_templates.go` | `spec-to-pr` nodes 41–45 and edges 58–62: the `phase-check` node and its two edges; `implement`/`fix` message text |
| `tools/muxcode/bus/conditions.go` | `spec_phase_committable` (value: the guarded commit node id) beside `spec_phases_remaining`; `ChainContext.GraphRun`/`Graph` for condition nodes |
| `tools/muxcode/bus/graph_exec.go` | `phaseCommitReady` — the one predicate shared by the condition and `phaseProgressGuardAllows` (the backstop); `resolveCompletedPhaseText` |
| `tools/muxcode/bus/graph.go` | `(g *Graph).node(id)`; `validateNode` rejects a check naming a node without the `phase-progress` guard |
| `tools/muxcode/bus/spec_items.go` | `SpecCompletedPhaseCount`, `SpecJustCompletedPhase` — the phase predicates underneath |
| `tools/muxcode/bus/phase_check_test.go`, `graph_test.go` | template shape, condition matrix, validation, executor routing; `TestShipTemplatesUpdateSpecBeforeGate` updated |
| `scripts/test-multi-phase-graph.sh` | the routing on both branches, and the disagreement case |
| `docs/architecture.md`, `docs/agent-bus.md`, `CLAUDE.md` | graph section, template reference, constraint bullet |

## Implementation

### Phase 1: The condition and the rewired template

- [x] `spec_phase_committable` condition in `conditions.go`, evaluated through the guard's own `phaseCommitReady`; unit tests: open phase → false, committable phase → true, no active spec / no graph context / unknown node → false (fail closed) — _`conditions.go:114, 378–393`; `TestSpecPhaseCommittableCondition`; suite green 15:14:31; review 15:15:40 EXIT=0 ("same completed-versus-shipped predicate as the commit guard")_
- [x] `spec-to-pr`: `phase-check` node between `update-spec` and `phase-gate`; success → `phase-gate`, failure → `stuck-gate`; `graph validate` passes for every builtin template — _`graph_templates.go:42, 59–61`; `TestSpecToPRPhaseCheckPrecedesGate`, `TestShipTemplatesUpdateSpecBeforeGate` updated; review 15:15:40 "retains its failure edge"_
- [x] `validateNode`: a `spec_phase_committable` check must name a node carrying the `phase-progress` guard — _`TestValidateSpecPhaseCommittableNamesGuardedNode`; review 15:15:40 "reviewed condition validation, context wiring"_
- [x] Executor test: an open phase reaches `stuck-gate` with no `phase-gate` approval recorded; a committable phase reaches `phase-gate`; a spec edited open between the check and the commit is still declined by the guard (negative control) — _`TestExecPhaseCheckRoutesOpenPhaseToStuckGate` (15:18:13) now carries the backstop block: spec reopened after the check, gate approved via `ApproveGraphGate`, commit guard declines "still open", stuck gate arms; test agent 15:18:40 `go test ./bus -run 'PhaseCheck|SpecPhaseCommittable|ValidateSpecPhaseCommittable|ShipTemplatesUpdateSpecBeforeGate'` exit 0 (5/5); full suite last green 15:14:31, before this test edit — the next lap's suite row covers it_

### Phase 2: The worker runs the phase's integration script

- [x] `implement` and `fix` messages: run the phase's `scripts/test-*.sh` through the run agent before reporting, and report counts with the run task id — _`graph_templates.go:36, 39`: "run it through the run agent … never go test … quote its counts and its task id"; pinned by `TestSpecToPRPhaseCheckPrecedesGate:44` ("task id"); test agent 15:19:38 focused run exit 0_
- [x] `docs/agents.md` spawned-worker section (or the template reference): the run-agent step is part of "done" — _plan 15:30: a bullet under "Spawned agents" (run agent, counts and task id, never `go test`, the store row is what plan credits)_

### Phase 3: Docs

- [x] `docs/architecture.md` graph section and `docs/agent-bus.md` template reference: the phase-complete condition and the one-gate rule, with the 2026-09-09 run (7 approvals, 4 declined) as the incident; `CLAUDE.md` graph-orchestration bullet gains the clause — _plan 15:40: "Check before you ask (MUX-167)" paragraph after the phase-progress guard paragraph in `architecture.md`; "`spec-to-pr` lap shape" paragraph ahead of the run examples in `agent-bus.md`; `CLAUDE.md:133` clause by edit_

### Phase 4: Integration test

- [ ] `scripts/test-multi-phase-graph.sh`: a scratch spec whose phase is left open after `update-spec` → the run parks at `stuck-gate` with no `phase-gate.approved` in `approvals/`; the same spec with the phase closed → `phase-gate`
- [ ] Negative control: condition passes, then the spec is re-opened before `commit` → the phase-progress guard declines (backstop still live)
- [ ] Run the script and record pass/fail counts in this spec

## Notes

- Filed 2026-09-09 15:12 by plan on the user's request relayed by edit (1788980859), from the
  session log and the run's node records; edit is implementing on the current tree. Created in
  `drafts/` as In Progress; **the active-spec pointer stays on MUX-163** — its run is live and
  depends on it.
- 2026-09-09 15:20 aligned to the implementation (edit 1788981248): the condition is
  `spec_phase_committable` naming the guarded commit node, sharing `phaseCommitReady` with the guard;
  Phase 1 gained the validator step (now 4 steps, spec 0/17). Store so far: build ok 15:13, a `go vet`
  exit-1 row at 15:13:47 whose cause the row does not carry, `go test -p 1 -count=1 ./...` exit 0 at
  15:14:31; review in flight on the build-test-review run `1788981195`. Ticks wait for the review row.
- 2026-09-09 15:25 (edit 1788981361): review 15:15:40 **EXIT=0**, 0 must-fix; its two should-fixes
  are MUX-163's unchanged Phase 4 script findings, none on this change ("no additional correctness
  finding"). Phase 1 steps 1–3 and AC2 ticked → 4/17; step 4 held on the missing
  between-check-and-commit control; AC3 waits on it.
- The gate labels in the table above are derived from the code path (`${completed_phase}` on an
  open phase names the frontier), not from stored text — a `wait_human` node record keeps `approved`,
  not the message the person saw.
- Related: [MUX-121](../completed/MUX-121-multi-phase-sequential-graph.md) (the guard and the
  `${completed_phase}` label this spec keeps); [MUX-132](../completed/MUX-132-graph-retry-launders-gate-approval.md)
  (single-use approvals — a wasted approval is also a spent one);
  [MUX-163](./MUX-163-prompt-inject-escape-eats-first-char.md) (the run that paid the cost);
  [MUX-166](../backlog/MUX-166-run-builds-on-stale-base-no-freshness-gate.md) (the other missing
  pre-gate check in the same template, filed today).

## Status

**In Progress** — 9/17. Filed 2026-09-09 15:12; Phase 1 complete 15:18 (suite green 15:14:31, review
15:15:40 EXIT=0, backstop control 15:18:40), Phase 2 complete 15:19 (task-id clause pinned), Phase 3
docs complete 15:40; Phase 4 (`test-multi-phase-graph.sh`) open. ACs 1, 4–7 wait on Phase 4 rows.
