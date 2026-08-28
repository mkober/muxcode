# The `close-spec` Node Marks Specs Complete Without Checking Whether They Are

The builtin `commit-pr-review-loop` template ends by telling the plan agent to set the active spec's
status to `Complete`, move it to `completed/`, and clear the pointer — with **no predicate on the
spec's actual state**. The node then feeds a commit node that pushes the move to the PR branch.

Fired live on 2026-08-27 against [MUX-109](../drafts/MUX-109-prompt-mode-graph-control-pane.md),
which was at **75 done / 22 open** with two acceptance criteria measured *failing* and its
integration test not passing. Plan refused on the evidence; nothing in the template would have
stopped it.

## Context

### The instruction, verbatim

`bus/graph_templates.go:95`, node `close-spec`:

> Close out the active requirements doc: set its status to Complete, check off finished items, move
> it to `docs/requirements/completed/`, clear the active spec, and report the new path (**no active
> spec = reply nothing to do**)

The parenthetical is the node's only guard, and it asks the wrong question: *does a spec pointer
exist*, not *is the work it points at finished*. Those come apart precisely when a spec is active
because it is still being worked on — the normal case.

### What was actually true when it fired

| Signal | Value |
|--------|-------|
| Checkboxes | 75 done, **22 open** |
| Acceptance criterion — `launch` intent | Measured **failing**, Phase 7, stable across 3 runs |
| Acceptance criterion — named-gate approval | Measured **failing**, same runs |
| `scripts/test-prompt-mode.sh` | **Does not pass** — 26 passed / 2 failed / 1 skipped |
| 4B regression criterion | Measured **FALSE**, not merely unverified |
| Spec status | `In Progress` |

### Why the node is positioned to do real damage

```
gate2 ──▶ c (edit: address review) ──▶ d (commit: reply to comments)
                                            │
                                            ▼
                                     close-spec (plan: mark Complete, move)
                                            │
                                            ▼
                                     commit-spec (commit: stage, commit, push to PR branch)
```

A false `Complete` does not stay local. `commit-spec` commits the status change and the file move
and **pushes them to the PR branch**, so the wrong claim lands in git history and in a PR a reviewer
is reading. Recovering means a revert plus a second move, not an edit.

### Second defect: the gate's consequence does not cover what it releases

`gate2` reads *"Approve addressing the review feedback and replying to comments."* Three nodes later
`commit-spec` commits a spec close-out. A human who approved gate2 approved review work; they were
never shown the words "mark the spec complete" or "push a spec move".

[MUX-014](../completed/MUX-014-graph-agent-orchestrator.md)'s rule — a commit node must be downstream
of a `wait_human` gate — is satisfied **structurally** here while its purpose is not. The rule exists
so a human sees what a mutation will do; a gate whose stated consequence stops three nodes short of
the mutation passes validation and defeats the intent. This is the [tui-style](../../tui-style.md)
confirm rule in another setting: *the confirm must state the consequence, including downstream
effects worth flagging*.

### Why plan refusing is not the fix

It worked this time, and it is not a control:

- It depends on the plan agent choosing to verify rather than comply. The node's wording (*"set its
  status to Complete"*) is an instruction, not a question, and an agent that follows instructions is
  behaving correctly by every other measure.
- It is exactly the failure mode this repo keeps re-learning — a guard that lives in an agent's
  judgement rather than in the mechanism fails open the first time the judgement is elsewhere.
- The template is a **builtin**. It ships to every user of every project, most of whom will never
  read it.

### Interim mitigation landed 2026-08-27 — one defect closed, one only narrowed

Both nodes were reworded the same afternoon this was filed. The two fixes are **not** of equal
strength, and the difference is the whole point of this spec.

**`gate2` — closed at the template level.** Its message now reads *"Approve addressing the review
feedback, replying to comments, and the spec close-out + spec commit that follow."* The human now
sees every mutation the approval releases. What remains is the general rule (Phase 3): nothing stops
the *next* template from reintroducing the gap.

**`close-spec` — narrowed, not closed.** Its message now reads:

> Close out the active requirements doc **ONLY if every acceptance criterion and phase step is
> checked complete** … Any item still open = refuse and report the open count

That is the correct instruction, and it codifies the refusal this spec was filed over. But it is
still an *instruction to a model*, which is the exact mechanism this spec identifies as the defect —
see [Why plan refusing is not the fix](#why-plan-refusing-is-not-the-fix). The guard moved from "an
agent that happens to verify" to "an agent that is told to verify"; it has not moved into the
mechanism. A model that misreads the checkbox state, or an agent whose definition later drifts, still
closes the spec and still pushes it.

So the live risk is materially reduced and **Phase 1 stands unchanged**. Treat the reworded node as
the thing to be replaced by a predicate, not as the predicate.

### Mechanism decision — landed 2026-08-28

**Chosen: the daemon dispatch guard**, the first and most expensive of the three placements. Phase 1
is implemented and its tests pass.

| | |
|---|---|
| Declared on the node | `"guard": "spec-complete"` — a new `Node.Guard` field (`bus/graph.go:49`) |
| Validated | Unknown guard names are a validation error (`graph.go:334`), so a typo fails loudly instead of silently not guarding |
| Evaluated | `nodeGuardAllows()` → `specCompleteGuardAllows()`, called from `dispatchNode()` **before** any send (`graph_exec.go:248`) |
| Counted | `SpecOpenItems()` (`bus/spec_items.go`) — counts `- [ ]` lines, returns count plus names in file order |
| Declined | Node → `failed` with `%d open items in <spec>: <names>`, plus a `graph-guard-declined` lifecycle event |

**Why this one and not the cheaper two.** The spec argued the daemon placement is the only one that
holds when the caller is *not this template and not this agent*, and that is what the implementation
buys: the guard is a property of the node, checked by the executor, so a hand-written graph that
declares it gets the same enforcement, and no wording in any agent definition is load-bearing. The
`condition`-node placement would have covered this template only; the plan-agent placement would
have covered routes through plan only. **This defect exists because the design trusted wording** —
so the fix had to stop trusting it.

Three design details worth keeping, each of which prevents the guard from becoming a new defect:

- **No active spec passes through.** Blocking there would make the node inert, which the spec called
  out as a failure mode of its own — a fix that never closes anything is not a fix.
- **An unreadable spec declines loudly** rather than passing. Closing out against a spec that cannot
  be read is as wrong as closing an open one.
- **An unresolvable repo dir postpones** instead of declining — the node stays `ready` and is retried
  next tick, so a transient condition does not become a permanent failure. This required a new
  `ready → failed` legality (`graph_run.go:73`): the node fails *without ever running*, and routing
  it through `running` would stamp a start that never happened.

**Fragility note — closed by Phase 4.** The "`commit-spec` does not commit" criterion rests on
`edgeOutcome()` defaulting to `OutcomeSuccess`, so a failed node matches no unlabelled edge. When
Phase 1 landed this was correct but **unpinned**: adding a
`{"from": "close-spec", "to": "commit-spec", "outcome": "failure"}` edge, or changing the default,
would have silently re-opened the exact hole this spec was filed over.

`scripts/test-close-spec-guard.sh` now pins it end to end — *"commit-spec held back (never
dispatched after decline)"* and *"commit inbox empty — nothing to commit was requested"*. Either
change above now fails the suite.

## Requirements

### Acceptance criteria

- [x] `close-spec` does not mark a spec `Complete` while it has unchecked items — the guard blocks **dispatch**, so the node never reaches plan (`dispatchNode`, `graph_exec.go:248`); pinned by `TestExecSpecGuardDeclinesOpenSpec`
- [x] The predicate reads the **spec**, not the pointer — an active spec with open work is a "not yet", not a "nothing to do" — `SpecOpenItems()` (`bus/spec_items.go:17`) reads the file and counts `- [ ]` lines; the pointer is only used to locate it
- [x] When it declines, it says so with the count and names the open items, rather than failing silently — decline detail carries `%d open items in %s: %s` with `summarizeOpenItems(names, 5)`, plus a `graph-guard-declined` lifecycle event at `warn`
- [x] A spec that genuinely has zero open items still closes out normally — **the node must not become inert** — pinned by `TestExecSpecGuardAllowsCompleteSpec`; `TestExecSpecGuardNoActiveSpecDispatches` covers the no-pointer case, which passes through deliberately
- [x] `commit-spec` does not commit a move that did not happen — **satisfied by the routing mechanism, traced end to end:** a decline calls `finishNode(…, OutcomeFailure)` → `GraphNodeFailed` with `Outcome = failure` (`graph_exec.go:396`); edge routing matches on `edgeOutcome(e) == st.Outcome` (`:567`); `edgeOutcome` defaults to `OutcomeSuccess` when unset (`graph.go:376`); the `close-spec → commit-spec` edge declares no outcome, so it never matches a failed source. **No dedicated test pins this** — see the fragility note below; the Phase 4 step covers it
- [x] `gate2`'s message names every mutation it releases downstream, including the spec close-out and the push — landed 2026-08-27: *"Approve addressing the review feedback, replying to comments, **and the spec close-out + spec commit that follow**"*
- [x] **Negative control:** a test proves the close-out path still fires for a fully-checked spec — a fix that simply never closes anything cannot pass — `TestExecSpecGuardAllowsCompleteSpec` **passed**, and it is the discriminating case: an always-decline guard fails it
- [x] A template whose gate text omits a downstream mutation is caught, not merely discouraged — **for builtins, which is where the defect was found and where the risk lives**: `validateGateText()` flags it and `TestBuiltinGateTextClean` fails the build. For hand-written user graphs it is advisory only, a deliberate trade so vocabulary cannot block a user's own graph — see [Phase 3 outcome](#phase-3-outcome--landed-2026-08-28)
- [x] `scripts/test-close-spec-guard.sh` passes — **21/0**, 2026-08-28 11:45

### Technical approach

- **Put the predicate where it cannot be reasoned around.** The check is a count of `- [ ]` lines in
  the active spec. Rewording the node's message to say "only if complete" leaves the decision with
  the model; the mechanical forms make it structural. Prefer a mechanical form — this defect exists
  *because* the current design trusts wording. Three placements, cheapest last:

  | Placement | Covers | Cost |
  |-----------|--------|------|
  | **Daemon refuses to dispatch** a spec-Complete write while the spec has open items (proposed by edit, 2026-08-27) | Every path to the write, template or ad-hoc — the node never fires | Needs the daemon to read spec state it does not read today |
  | `condition` node upstream of `close-spec` | This template only; a hand-written graph can still omit it | Reuses existing vocabulary, no new machinery |
  | Refusal inside the plan agent's close-out path | Every route through plan, but only through plan | Still agent-side, though enforced in code rather than prompt |

  The daemon placement is the strongest because it is the only one that holds when the caller is not
  this template and not this agent. Phase 1 should weigh it first.
- **Reuse the existing condition vocabulary.** `EvaluateConditions()` already backs graph `condition`
  nodes, and a `command_match` on a checkbox count needs no new dialect
  ([MUX-014](../completed/MUX-014-graph-agent-orchestrator.md) was explicit about not inventing a
  second one).
- **Widen the gate text rather than adding a gate.** A second `wait_human` before `close-spec` would
  be more correct in isolation but adds a stop to a flow whose whole value is running unattended
  after one approval. Naming the downstream mutations in `gate2` restores the guarantee at no
  interaction cost. If a separate gate is chosen instead, that is a deliberate trade to record, not
  a default.
- **Consider whether the general rule is checkable.** "A gate's message must describe every mutation
  it dominates" could be a `Validate()` rule, at least to the extent of flagging a gate that
  dominates a commit or Atlassian node without naming it. Worth scoping — it would catch this class
  in user templates too, not just the builtin.

### Key files

| File | Change |
|------|--------|
| `bus/graph_templates.go:95` | `close-spec` predicate; `gate2` message text |
| `bus/graph.go` | Optional: `Validate()` rule for gate text vs dominated mutations |
| `bus/conditions.go` | Reuse `EvaluateConditions()` for the open-item count |
| `agents/planner.md` | The close-out contract: refuse with evidence, never mark Complete on an open spec |
| `bus/graph_test.go` | Template validation, plus the negative control |
| `scripts/test-close-spec-guard.sh` | New — end-to-end refusal and normal close-out |
| `docs/agent-bus.md` | Document the close-out predicate under the template |

## Implementation

### Phase 1: Predicate the close-out

- [x] Decide the mechanism from the three placements above (daemon dispatch check preferred) and record the reasoning — **daemon dispatch guard chosen**, see [Mechanism decision](#mechanism-decision--landed-2026-08-28)
- [x] Implement the open-item count against the active spec — `SpecOpenItems()` in `bus/spec_items.go`
- [x] Decline path reports the count and the open item names — `specCompleteGuardAllows()` in `bus/graph_exec.go`
- [x] Test: spec with open items is not closed; spec with none **is** (negative control) — `TestExecSpecGuardDeclinesOpenSpec` / `TestExecSpecGuardAllowsCompleteSpec`, both passing

### Phase 2: Gate text covers what it releases

- [x] Rewrite `gate2` to name the spec close-out and the push
- [x] Audit the other builtin templates for the same gap — a gate that dominates a mutation it does not describe — see [Phase 2 audit](#phase-2-audit--landed-2026-08-28)
- [x] Record any template where the gap is deliberate — four recorded, with reasons

### Phase 2 audit — landed 2026-08-28

All eight builtin templates audited against one rule: **every `wait_human` gate's message must name
each mutation it dominates, up to the next downstream gate** (nearest-gate principle). Suite green
2563/0/1. Verified against `graph_templates.go` directly — nine gates, all now carrying non-empty
text that names their mutations.

| Template | Gate | Fix |
|----------|------|-----|
| `req-code-pr` | `ship-gate` | Push was unnamed, though PR creation pushes → *"Approve commit, push, and PR creation"* |
| `story-lifecycle` | `ship-gate` | Same unnamed push → *"Approve commit, push, and PR"* |
| `commit-pr-review-loop` | `gate2` | The spec commit's **push** was unnamed — and the criterion checked off above demands the push be named → *"…spec close-out with its commit and push that follow"* |
| `update-spec-docs` | `gate` | **Defect, not an omission:** the node used a `"prompt"` key rather than `"message"`, so the JSON silently dropped it and the gate rendered with **no approval text at all**. Now a proper message naming the commit |

**The `update-spec-docs` row is the one that matters.** It is a different failure from the one this
spec was filed over: not a gate whose wording was too narrow, but a gate with *no wording*, produced
by a key typo that parsed cleanly and failed silently. A human approving it saw nothing describing
what they were releasing. It is pinned by a new test, `TestBuiltinGateMessagesNonEmpty`, which asserts
every builtin gate carries non-empty message text — catching that whole defect class rather than the
single instance.

Note the limit of that pin, because it is easy to over-read: it proves a gate says *something*, not
that it says the *right* thing. A gate reading *"Approve the thing"* passes it. Catching a gate whose
text omits a mutation it dominates is [Phase 3](#phase-3-make-it-checkable-scope-first)'s job and
remains open.

**Four deliberate gaps, recorded and unchanged:**

| Template | Node | Why the gap is deliberate |
|----------|------|---------------------------|
| `pr-local-review` | `restore` (`git checkout -`) | The return leg of the gate-approved "switching branches" round trip — already covered by that gate's wording |
| `update-spec-docs` | `spec` / `docs` | Ungated plan doc writes are working-tree edits; the commit gate downstream is the consequence point |
| `story-lifecycle` | `req-gate` | Names no mutations because nothing mutating runs before `ship-gate` re-gates — nearest-gate principle |
| `deploy-verify` | *(no gate at all)* | Deploy is outside MUX-014's gate rule, which covers commit and Atlassian only; launching the template is the approval. **Flagged for Phase 3 scoping** — see below |

### Phase 3: Make it checkable (scope first)

- [x] Assess whether `Validate()` can flag a gate dominating an unnamed commit/Atlassian node — **viable**, landed as `validateGateText()` (`graph.go:578`), wired into `Validate()` at `:260`
- [x] If viable, implement and pin all builtins against it — `TestBuiltinGateTextClean`
- [x] ~~If not, record why and leave Phase 2's audit as the control~~ — moot; the viable branch was taken
- [x] **Raised by the Phase 2 audit:** decide whether `Validate()` should warn on an **ungated `deploy` node** — **decided: warn, do not gate.** See [Phase 3 outcome](#phase-3-outcome--landed-2026-08-28)

### Phase 3 outcome — landed 2026-08-28

`validateGateText()` compares each `wait_human` gate's message against the mutation classes of the
nodes it dominates, matching keyword classes **on word boundaries with prefix matching** — so
`commit`/`committing` and `push`/`pushing` both count, while plain substring matching is avoided
because it would let *"**Appr**ove"* satisfy *"pr"*.

**Warnings, not errors — and the reasoning matters.** Gate text is natural language, so the check is
a heuristic. Making it an error would block user graphs on vocabulary. The enforcement therefore
splits:

| Scope | Strength |
|-------|----------|
| **Builtin templates** | **Hard** — `TestBuiltinGateTextClean` fails the build on any *"does not name the mutation"* warning |
| User / project graphs | Advisory — surfaced by `graph validate`, never blocking |

That split answers the criterion *"caught, not merely discouraged"* where the original risk was: the
defect was found in a **builtin**, which "ships to every user of every project, most of whom will
never read it". Builtins are now caught by a failing test. For hand-written user graphs it is
genuinely discouragement, and that is a deliberate trade rather than an oversight.

**The ungated-`deploy` decision: warn, and pin the warning.** `deploy` is outside MUX-014's gate rule
(commit and Atlassian only), so `deploy-verify` having no gate is compliant by the letter — but an
infrastructure mutation released by nothing more than launching a template is worth surfacing.
`isDeployNode()` (`graph.go:523`) warns rather than gates.

The pin is the part worth noting: `TestBuiltinGateTextClean` asserts **positively** that
`deploy-verify` *does* carry the ungated-deploy warning, and that no other builtin does. So the
deliberate trade cannot go silent — if someone later suppresses the warning or adds an ungated
deploy elsewhere, a test fails. A recorded trade that nothing enforces decays into an unrecorded
one; this one cannot.

### Phase 4: Integration test

- [x] Create `scripts/test-close-spec-guard.sh` — hermetic: scratch bus, scratch spec fixtures — 255 lines, isolated `BUS_SESSION`, scratch repo and daemon
- [x] An active spec with open items reaches `close-spec` and is **not** marked Complete or moved — *"run failed at the guard"*, *"close-spec node failed at dispatch"*, *"spec file untouched (still in drafts/, status unchanged)"*
- [x] The decline is reported with the count, not silent — *"decline reports the open-item count"*, *"decline names the open items"*, plus *"lifecycle records graph-guard-declined"*
- [x] A fully-checked spec closes out and moves normally (**negative control**) — *"fully-checked spec dispatched close-spec to plan (guard not inert)"*, *"negative-control run reached complete"*
- [x] `commit-spec` commits nothing when no move happened — *"commit-spec held back (never dispatched after decline)"*, *"commit inbox empty — nothing to commit was requested"*
- [x] Coverage floor so a skipped run cannot report green — floor of 18; run executed 20
- [x] Run the script and confirm all checks pass — **21 passed / 0 failed** (2026-08-28 11:45)

### Phase 4 outcome — the first run found two real gaps

Worth recording, because the script earned its keep immediately rather than rubber-stamping work
already believed done:

| Run | Result |
|-----|--------|
| 11:42 | **19 passed / 2 failed** — the decline produced no open-item **count** and no item **names** in `graph status` output |
| 11:45, after the fix | **21 passed / 0 failed**, coverage floor met (20 checks) |

The two failures were against the criterion *"when it declines, it says so with the count and names
the open items, rather than failing silently"* — a criterion that had already been checked off from
reading the decline-path source. The unit tests passed; the code built the detail string correctly;
it simply did not reach the surface a human reads. **That is the MUX-014 pattern again** — an
integration run finding what in-process tests structurally cannot see, on a spec that read complete.

The negative control is the load-bearing half: *"fully-checked spec dispatched close-spec to plan
(guard not inert)"* and *"negative-control run reached complete"* both pass, so a guard that simply
declined everything would fail this suite.

## Time Tracking

| Branch | Active time | Last updated |
|--------|-------------|--------------|
| MUX-114-close-spec-completion-check | 1h 7m | 2026-08-28 11:48 |

## Status

Complete

Closed 2026-08-28 at **27 / 27 checked, zero open items** — and closed *through the guard this spec
built*. The `spec-complete` predicate evaluated the active spec, found no open checkboxes, and
permitted the `close-spec` dispatch that had been declining all afternoon. The mechanism's first
production act was authorising the close-out of its own specification.

That is the end-to-end proof the spec asked for, and it is worth more than a self-attested
completion: the same predicate that **refused** MUX-109 on 22 open items **allowed** this one on
zero. Both directions exercised, on real specs, in production.

**Remaining:** the file move to `docs/requirements/completed/` is a git operation outside the plan
agent's scope — reported to the user rather than performed.
