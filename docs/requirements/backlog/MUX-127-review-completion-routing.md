# Review Completion Routes Nowhere on Failure and Loops on Success

When a review finishes, two independent mechanisms decide what happens next: the **graph executor's
edges** and the **chain's notify gates**. On 2026-08-31 both were observed mis-routing, in opposite
directions, within the same hour:

| | Outcome | What should happen | What happens |
|---|---------|--------------------|--------------|
| **A** | `review → failure` | route to the `fix` node | **no live edge** — the run dies, `fix` stranded `pending` |
| **B** | `review → success` | notify plan when a spec needs verifying | **fires unconditionally**, including on plan's own doc writes → self-sustaining loop |

The one outcome carrying reasoned, actionable findings has nowhere to go. The outcome carrying
nothing new is delivered over and over.

> **These are two root causes, not one.** They live in different subsystems (`bus/graph.go`
> templates vs `bus/profile.go` + the daemon chain) and have separate fix sites. They are filed
> together because they were found in a single incident, they share a diagnostic frame — *what does
> the system do when a review completes?* — and a reader who fixes one will want to know about the
> other. **Fixing A does not fix B.**

Tracking: _(no GitHub issue yet)_

## Context

## Defect A — a review failure has no route to `fix`

### What happened

Run `1788195259-req-code-pr-3fbcc7da` (MUX-117 Phase 2) reached review with the implementation
complete and **tests passing**. Review returned an authoritative failure carrying real findings:

> Tests passed; review found **2 must-fix**, 2 should-fix, 1 nit — propagate pane-tagging failures
> and fail closed on unknown worktree status before cleanup.

The run then died:

```
13:48:50  graph-node-done   review -> failure
13:48:50  graph-run-failed  node review failed with no live edge
```

### The structural cause

The frozen `spec-to-pr` definition wires a repair path from build and test, but not from review:

```
build  -> test        build -> fix
test   -> review      test  -> fix
review -> update-spec          ← no review -> fix
```

So a **build** or **test** failure is repairable in-loop, while a **review** failure terminates the
run and leaves `fix` unreachable-`pending`. This inverts the value of the signal: build and test
failures are usually mechanical, whereas a review failure is the one that carries reasoned findings a
`fix` node could act on.

### Compounding: the other two gates asserted nothing

In the same run, both upstream gates returned non-authoritative outcomes and were routed onward as
success:

```
13:48:22  build -> unknown   graph-unknown-fallback: build routed unknown outcome via success edge
13:48:34  test  -> unknown   graph-unknown-fallback: test  routed unknown outcome via success edge
```

Review was the **only** node in the run that returned a real outcome — and it is the one with nowhere
to send it. Any fix should consider whether `unknown` on build/test deserves the same scrutiny, since
today those gates can pass silently while the one gate that speaks is fatal.

### Scope

This is a property of the **template**, not of MUX-117's work. It will recur on every spec that runs
`spec-to-pr`. Whether sibling templates (`commit-pr-review-loop`, `build-test-review`) share the
gap is unverified and part of Phase 1.

## Defect B — verify-spec fires on the plan agent's own doc writes

> **Prior art: this defect was already filed as
> [MUX-007](../completed/MUX-007-verify-spec-stale-review-refire.md) on 2026-08-13**, and MUX-007 names the
> mechanism more precisely than the 2026-08-31 observation did: the daemon fires the
> reviewed-transition on **any growth of edit's inbox** while **any unconsumed review→edit message
> exists** — and plan's mandated reply to edit *is* that growth, so plan's own compliance sustains
> the loop. That explains both puzzles below: why replying fed the cycle, and why fires 13–14
> continued after plan stopped writing docs.
>
> What follows is therefore **new evidence for MUX-007**, not a new defect — kept here because it
> was found alongside Defect A and because it adds two things MUX-007 does not have: the
> provenance-blind worktree path (B1) and the fire-11 case that rules out the obvious fix.
> **These two specs should probably be merged** — see *Open question* at the end of this section.

### The cycle

```
plan writes a spec
  → analyze hook routes the changed .md
  → workflow: editing → analyzing → reviewed  (trigger=daemon:review-complete)
  → daemon emits plan-verify (active spec is set)
  → plan is asked to read the spec and update it
  → plan writes a spec …
```

The gate is `ChainShouldNotifyPlan()` / `NotifyPlanOn` (default `["success"]`), which fires whenever a
review completes and an active spec is set. Nothing in that condition asks **who** changed the files
or **whether the change was code**.

### Fire census, 2026-08-31

**14 `verify-spec` fires in ~50 minutes; 2 carried real state changes.** The daemon's own loop
detector flagged it twice — `loop-detected plan type=message` at 13:20:51 and 13:51:37.

| Fire | Named changed file | Real? |
|------|--------------------|-------|
| 1 | `bus/pane_test.go` — in a spawn worktree under `$TMPDIR`, **absent from the repo** | Partly — real work existed, but not at the named path |
| 2–4 | `bus/spawn.go`, `bus/spawn_test.go` — MUX-120 work, unrelated to the active spec | No |
| 5–10 | plan's own edits to MUX-117 / MUX-123 / MUX-126 | No |
| 11 | MUX-117 doc edit — but the graph run had transitioned to `failed` underneath | **Yes** — caught Defect A |
| 12–14 | plan's own MUX-117 edit, run already terminal, code tree byte-identical | No |

Fires 13 and 14 arrived *after* plan stopped writing, so the cycle does not need continuous input to
keep running.

### Two sub-defects

**B1. The changed-files list is provenance-blind.** Fire 1 named an absolute path inside a spawn's
detached-HEAD worktree (`$TMPDIR/muxcode-spawn-muxcode/spawn-<hex>/…`). Read literally it asserted a
file the branch did not contain. A verifier that trusted it would have checked off Phase 2 against
code absent from the repo.

**B2. The plan agent's own writes are indistinguishable from implementation progress.** Nothing marks
a changed file as *"the verifier wrote this in response to the last fire."* So the signal that work
progressed and the signal that plan responded are the same signal.

### The cheap fix is wrong

"It names a doc, therefore ignore it" would have swallowed **fire 11** — which named a doc edit yet
carried a real change, because the graph run had transitioned to `failed` underneath it. That fire is
how Defect A was discovered at all. Suppression must key on **state movement**, not filename shape.

The discriminator that actually fits the evidence: every echo fire had a **byte-identical code tree**;
the one real fire did not. Note that fire 11's movement was a *graph run state* transition rather
than a file edit, so a working-tree fingerprint alone is insufficient.

### Secondary cost: suppressed time recording

Each fire also asks plan to record branch active time — itself a doc write, and therefore more fuel.
Plan began declining that write on echo fires, so the ledger drifted from the recorded value (17m vs
12m in the doc). Correct behaviour and loop-avoiding behaviour are in direct conflict, which indicates
the gate is misplaced rather than that the agent is choosing badly.

### Open question: merge with MUX-007?

[MUX-007](../completed/MUX-007-verify-spec-stale-review-refire.md) covers the same loop with a sharper
mechanism; this section adds B1 (provenance) and the fire-11 negative control. Carrying both risks
two half-specs of one defect. The likely resolution is to **fold B into MUX-007** and leave this spec
scoped to Defect A alone — but that is a filing decision for the user, not one to make silently.
Related: [MUX-009](./MUX-009-response-echo-chain-retrigger.md) is the same *shape* (a response
re-triggering a chain) on non-hook providers, and [MUX-032](./MUX-032-loop-detector-granularity.md)
governs whether the detector that flagged this can act on it.

## Why both are worth fixing

- **Together they strand work silently.** Defect A killed a run holding 2 must-fix findings; Defect B
  buried the notification of that death in twelve near-identical echoes.
- **Defect B trains the wrong habit.** The rational response to a stream of false fires is to stop
  delta-checking them — and fire 11 shows what that costs: a real failure arriving in an envelope
  identical to twelve echoes.
- **Context burn.** Repeated no-op verification passes force compaction, losing the accumulated
  reasoning that makes verification worth anything.

## Requirements

### Acceptance criteria — Defect A

- [ ] A `review` node returning `failure` routes to a repair path rather than terminating the run
- [ ] The `fix` node is reachable from review, and a run that enters it can return to build/test
- [ ] **Negative control:** a review failure with no repair path configured still fails loudly — the
      fix must not silently swallow failures by routing everything to `fix`
- [ ] Sibling templates are audited for the same gap, and the result recorded (gap or no gap)
- [ ] `unknown` outcomes on build/test are reviewed: either justified as safe or tightened

### Acceptance criteria — Defect B

- [ ] A `plan-verify` is **not** emitted when every changed file in the triggering context was
      written by the plan agent itself in response to a prior `plan-verify`
- [ ] The changed-files list distinguishes repository paths from worktree/temporary paths; a request
      naming only non-repo paths is suppressed or explicitly labelled as such
- [ ] **Negative control:** a genuine code change still fires `plan-verify` exactly as today — a fix
      that merely suppresses everything cannot pass
- [ ] **Negative control:** a fire whose named files are stale but whose run/tree state *has* moved is
      still delivered (the fire-11 case) — suppression keys on change, not filename shape
- [ ] State movement is detected for **graph run transitions**, not only working-tree edits
- [ ] Branch time recording no longer conflicts with loop avoidance
- [ ] Repeated identical `plan-verify` requests are bounded

### Acceptance criteria — shared

- [ ] `scripts/test-review-completion-routing.sh` passes

### Technical approach

**Defect A** is a template edit plus a validation question: should `Validate()` reject a graph whose
`review` node has no failure route, the way it already rejects uncapped cycles and ungated commits? A
template fix repairs one template; a validation rule prevents the class.

**Defect B** — options in rough order of preference:

| Option | Note |
|--------|------|
| Attribute changed files to their writing role, suppress self-authored | Most precise; needs an author record the analyze hook does not keep |
| Debounce per (spec, state fingerprint) — suppress when neither tree nor run state moved | Directly matches the evidence; fire 11 passes because run state moved |
| Exclude `docs/**/*.md` from the analyze-route driving `review-complete` | Cheap, but a spec edit *is* sometimes reviewable work |
| Move recording out of the fire path | Removes the secondary conflict regardless of which lands |

### Key files

| File | Purpose |
|------|---------|
| `bus/graph_templates.go` | `spec-to-pr` definition — the missing `review → fix` edge |
| `bus/graph.go` | `Validate()` — candidate home for a "review needs a failure route" rule |
| `bus/graph_exec.go` | Edge routing, `graph-unknown-fallback`, `graph-run-failed` |
| `daemon/daemon.go` | `plan-verify` emission; analyze-route and review-complete handling |
| `bus/profile.go` | `ChainShouldNotifyPlan()`, `NotifyPlanOn`, `NotifyAnalystOn` |
| `bus/branch_time.go` | Recording path, if lifted out of the fire |

## Implementation

### Phase 1: Reproduce and bound

- [ ] Reproduce Defect A: run a graph whose review returns failure; confirm the run dies with `fix` pending
- [ ] Audit sibling templates for a missing review failure route; record gap or no gap per template
- [ ] Reproduce Defect B in a scratch session: set an active spec, have plan edit it, observe the fire
- [ ] Record how many fires one doc write produces and whether it terminates on its own
- [ ] Confirm whether `loop-detected` currently suppresses anything here or only logs

### Phase 2: Route review failures

- [ ] Add the repair route from `review` in `spec-to-pr` (and any sibling found in Phase 1)
- [ ] Decide and implement whether `Validate()` rejects a review node with no failure route
- [ ] Ensure a genuinely unrepairable review failure still fails loudly

### Phase 3: Suppress self-authored verify fires

- [ ] Implement the chosen gate so self-authored doc writes do not emit `plan-verify`
- [ ] Preserve delivery when code tree **or graph run state** has changed (the fire-11 case)
- [ ] Log a lifecycle row when a fire is suppressed, so suppression is observable rather than silent

### Phase 4: Provenance and recording

- [ ] Mark or filter non-repository paths (spawn worktrees, `$TMPDIR`) in the changed-files list
- [ ] Recording no longer requires a doc write inside the fire path, or no longer triggers one
- [ ] A skipped recording cannot leave the ledger permanently drifted from the doc

### Phase 5: Integration test

- [ ] Create `scripts/test-review-completion-routing.sh` — hermetic: scratch bus, scratch session,
      scratch daemon, pinned active spec
- [ ] A review failure routes to `fix` and the run continues (Defect A)
- [ ] A review failure with no repair path still fails the run (negative control — the fix cannot
      swallow failures)
- [ ] A plan-authored doc write produces **no** subsequent `plan-verify` (Defect B)
- [ ] A code change **does** produce one (negative control — suppression cannot go inert)
- [ ] A stale-filename fire with a moved graph run state is still delivered (fire-11 negative control)
- [ ] A fire naming only a `$TMPDIR` worktree path is labelled or suppressed, never presented as repo state
- [ ] Coverage floor so a skipped run cannot report green
- [ ] Run the script and confirm all checks pass

## Risks

| Risk | Why it matters | Mitigation |
|------|----------------|------------|
| Routing every review failure to `fix` hides real failures | A run that should stop instead loops through repair | Negative control asserting an unrepairable failure still fails |
| Suppression goes inert and swallows real fires | The verifier stops being told about genuine progress | Two negative controls, one of them the fire-11 case |
| Tree-fingerprint gate misses non-file state | Fire 11's movement was a graph run transition, not a file edit | Fingerprint must cover run state explicitly |
| Fix applied only to `plan-verify` | The same review-complete path feeds other notifications | Audit `NotifyAnalystOn` alongside `NotifyPlanOn` |
| The two defects are treated as one | They have separate fix sites; a single patch will miss one | Acceptance criteria and phases are partitioned by defect |
| Filing this spec triggers the very loop it describes | Writing this file is itself a plan-authored doc write | Expected; noted rather than worked around |

## Status

Backlog
