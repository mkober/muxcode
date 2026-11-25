# MUX-245: Nothing Says Whose Verdict Counts When a Worker Certifies Its Own Change

**Tracking:** [mkober/muxcode#258](https://github.com/mkober/muxcode/issues/258)

**Split from:** [MUX-157](../completed/MUX-157-role-boundary-an-agent-can-ignore.md) — its certification
criterion and the certification half of its Phase 4, moved here on the user's decision of
2026-11-24 relayed by edit.

On 2026-09-08 a test agent wrote four attempts at a fix and reported `EXIT=0` on each, while review
returned `EXIT=1` on the same change each time. No rule said which reading wins. Edit had to argue
the worker's verdict down by hand, four times.

## Context

### Observed (2026-09-08, session `muxcode`)

From [MUX-157](../completed/MUX-157-role-boundary-an-agent-can-ignore.md)'s account, which marks this row
**reported** (edit's statement; the bus log of that session was cleared at relaunch):

> `EXIT=0` from the role that authored the change, against review's `EXIT=1`. Nothing consumed the
> sentinel formally (it landed in the same commit), but it was the reading edit had to argue down
> each time.

### What has changed since

Read by plan on 2026-11-24 from `CLAUDE.md`; none of it re-read in code for this spec.

| Mechanism | What it settles | What it leaves |
|-----------|-----------------|----------------|
| Review's `EXIT=` sentinel is mandatory, and the daemon's `verify-spec` fires only on this review's own verdict ([MUX-172](../completed/MUX-172-plan-verify-gate-hardcodes-success.md)) | A failing review no longer sends plan to tick boxes | Says nothing about a second verdict from another role |
| A graph node's outcome is its history row attributed by role; a contradicting `EXIT=0` holds the node as `graph-outcome-conflict` ([MUX-218](../completed/MUX-218-build-agent-vet-failure-row-rejected-as-build-evidence.md)) | A build or test agent's claim cannot overrule its own failing command | The claim and the command are the same role's; the author-versus-reviewer case is different |
| In the builtin graphs, `review` is its own node and a `fix` follows a failing one | Within a run, the author's verdict is never asked for in review's place | Outside a run — the chain, or edit reading two replies — nothing orders them |
| [MUX-157](../completed/MUX-157-role-boundary-an-agent-can-ignore.md)'s source-write guard, once built | build, test and review cannot author through the guarded roads, so "a verifier certifying its own change" gets rarer | A spawn worker and edit author by design and also report `EXIT=0` |

So the gap today is narrower than on 2026-09-08 and is mostly a missing **rule**: no document says
that the author of a change does not certify it.

### Undecided — whether there is a mechanical form at all

MUX-157 said so in as many words: the mechanical form "is a design question for the spec, not
decided here". Open:

| # | Question | Options |
|---|----------|---------|
| 1 | Is the rule prose only, or enforced | prose in `CLAUDE.md` and the reviewer definition · enforced somewhere |
| 2 | If enforced, where | a graph `join` of test and review set to `all` rather than either alone · the executor ignores an `EXIT=0` from the role that authored the diff · the chain withholds a success whose sender authored the change |
| 3 | How "authored the diff" is known | provenance is declared, not proven, until [MUX-185](../completed/MUX-185-history-row-provenance-declared-not-proven.md) lands — a mechanical form may have to wait for it |

Phase 1 (the prose rule) needs none of these. Phase 2 needs all three.

## Requirements

### Acceptance criteria

- [ ] The rule is written down: a worker's own `EXIT=0` on a change it authored does not outrank
      review's `EXIT=1` on the same change, and where two verdicts disagree the reviewer's stands
      until a new review says otherwise — in `CLAUDE.md` and `agents/code-reviewer.md`
- [ ] Edit's definition says what to do with a disagreeing pair: act on the review, do not weigh
      them
- [ ] The three questions above are answered by the user and recorded here
- [ ] If a mechanical form is chosen: it is implemented, with a negative control showing an ordinary
      passing review still completes its chain or node
- [ ] If none is chosen: this item is ticked with the decision, and Phase 2's build steps are ticked
      as not built

### Key files

| File | Purpose |
|------|---------|
| `CLAUDE.md` | Key constraints — where the rule is stated |
| `agents/code-reviewer.md`, `agents/code-editor.md` | The reviewer's verdict and edit's handling of a disagreement (edit's files) |
| `tools/muxcode/bus/review_verdict.go` | `ReviewReplyOutcome` — the verdict the daemon already reads |
| `tools/muxcode/bus/graph_exec.go`, `bus/graph_templates.go` | Node outcomes and joins, if a mechanical form is chosen |

## Implementation

### Phase 1: The rule in prose

- [ ] `CLAUDE.md`: one clause under Spec verification or Hook-driven chains stating the rule
- [ ] `agents/code-reviewer.md` and `agents/code-editor.md` (applied by edit): the reviewer's
      verdict stands over the author's; edit acts on the review

### Phase 2: Decide, and build if decided

- [ ] Put the three questions to the user, with the table of what already holds; record the answer
      as `## Decisions (user, <date>)` and rewrite the steps below to it
- [ ] Implement the chosen mechanical form, or tick this as not built with the decision
- [ ] Negative control: a change with one passing review and no contradicting verdict proceeds
      exactly as today

### Phase 3: Integration test

- [ ] If a mechanical form was built: create `scripts/test-self-certification.sh` (hermetic: scratch
      daemon on a private tmux server, the script playing the worker and review) — a worker's
      `EXIT=0` beside review's `EXIT=1` on one change does not complete the node or fire the next
      chain step; a passing review alone does (negative control); a build from a scratch copy with
      the check removed lets the worker's verdict through (control); coverage floor; pass/fail
      counts recorded here
- [ ] If prose only: a test that fails when the rule's sentence is missing from the installed
      reviewer definition (`agents/code-reviewer.md`), run through `./test.sh` — with a doctored
      copy lacking the sentence as its negative control — and its result recorded here

## Status

**Backlog — parked at filing 2026-11-24** by plan, under the rule that a spec holding an unrecorded
design decision is not worked unattended. Filed as a split from MUX-157. Not started. Whether the rule gets a mechanical
form, where, and how authorship is known are undecided (see "Undecided"); Phase 1 can be worked
without them.
