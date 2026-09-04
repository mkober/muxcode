# MUX-149: The Unverified Hold — a Node With No Authoritative Result Parks for a Person

Retroactive documentation of **shipped, tested, live** behaviour. This spec was written after the
fact, on 2026-09-03/04, because the mechanism had no spec of any kind — its only description was
the doc comment on `unverifiedHoldReleased`.

**Not a proposal.** Everything in "What it does" was verified against the code at the time of
writing; the file/line references are the record.

## Context

### Why it needed a spec

The unverified hold is a **human-approval gate**. It can stop any graph run and demand a person.
It shipped in `16f2027` with amendments in `573ed2e` — both commits carrying a `MUX-136` prefix on
a branch where every commit did, while **MUX-136's spec never mentions it**. A `git log` search by
prefix misattributes it, and no spec in `docs/requirements/` described it.

Two concrete costs of that gap, both from 2026-09-03:

1. **A near-miss.** The edit agent proposed adding an explicit `outcome: unknown` edge to
   `commit-pr-review-loop` to stop a hold firing on a read-only node. That would have **silently
   re-opened the exact hole the mechanism closed** — an explicit unknown edge is precisely what
   routes an unverified node onward without a person. It was caught only because someone read the
   doc comment before writing the code.
2. **A misattribution in another spec.** [`MUX-148`](../backlog/MUX-148-node-outcome-reads-command-ran-as-task-done.md)
   was initially handed over describing this as "the MUX-136 hold".

A spec makes that reasoning available without requiring the reader to find the comment first.

### Why the mechanism exists

Recorded in the doc comment: a `spec-to-pr` run shipped a commit behind build, test and review
nodes that had **each recorded nothing**. The nodes never claimed success — *the router claimed it
for them*, and the human gate downstream was approved on that false signal.

The tradeoff is stated explicitly and is **accepted, not a defect**: holding costs one approval per
unverified node, which is frequent on non-hook providers that infer outcomes. That cost is paid
because the alternative is unverified work advancing into irreversible actions.

## What it does — verified against the code

| Behaviour | Location | Detail |
|-----------|----------|--------|
| The hold | `bus/graph_exec.go:1276` (`routeFinishedNodes`) | A node finishing `outcome=unknown` with **no explicit `unknown` edge** is parked rather than routed down its success edges |
| Release requires a person | `:676` (`unverifiedHoldReleased`) | An `approved` marker whose `approved_by` is not `ActorUser` is **refused** and logged `graph-unverified-self-approval` |
| Identity cannot be laundered | `bus/config.go:167` (`BusActorVerified`) | Falls back to `agentRuntimeAncestor()` (process ancestry), so an agent that unsets `AGENT_ROLE` is still recorded by its ancestry; an unreadable ancestry yields `ActorUnknown`, which is also refused |
| Single-use | `:684` | The grant is **consumed** on release (`purgeStaleApproval`) — "spent here so a re-entry asks again" — so a loop edge or `graph retry --from` demands a fresh approval |
| Marker files | `:704` | `<run>/approvals/<node>.pending` carrying `{"message", "reason": "unverified"}` |
| Discriminator | `:81`, `:835` | `unverifiedHoldReason = "unverified"`. A `wait_human` pending marker carries `{"message"}` and **no `reason`** — that field is what separates the two kinds |
| Release logging | `:690` | `graph-unverified-released` on success; `graph-unverified-purge-failed` if the grant cannot be spent |

### Amendments shipped 2026-09-03 (`573ed2e`)

**1. Gate-aware exemption** (`successorsAllHumanGates`, `:1244`). The hold is skipped when every
success edge out of the node lands on a `wait_human`: a person is already the next step and nothing
mutates in between, so holding charged **two approvals for one decision**.

The implementation returns a `found` flag, so a node with **no success edge at all is not exempt** —
verified, and pinned by `TestExecUnverifiedHoldAppliesWhenNoSuccessEdge`.

**2. Reachable from the UI** (`bus.ListUnverifiedHolds:110`, `tui/graph_ui.go LoadPendingGates:148`,
`approvableNow:945`). Before this the hold was **invisible and unapprovable from the control pane**:
the approval queue filtered on `wait_human` *type* and `Waiting` *state*, and a hold is a `send`
node sitting in `Done`, so it matched neither. Runs stalled behind an empty approval surface and the
Approve key did nothing. Now rendered with a `?` glyph and an `unverified` tag, so the two kinds are
distinguishable **without colour**.

## Requirements

### Acceptance criteria — cited against existing pins

Ticked criteria are covered by tests verified to exist at the paths named:

- [x] A node finishing `unknown` with no explicit unknown edge does not route down its success edges — `TestExecUnverifiedHoldAppliesWhenNextIsSend`
- [x] An agent cannot release its own hold — `TestExecUnverifiedHoldRefusesAgentApproval`
- [x] An agent that strips its identity still cannot release one — `TestExecUnverifiedHoldRefusesStrippedIdentityApproval`
- [x] An unreadable ancestry is refused rather than defaulted to a person — `TestExecUnverifiedHoldRefusesWhenAncestryUnreadable`
- [x] The hold is skipped when every success edge lands on a `wait_human` — `TestExecUnverifiedHoldExemptWhenNextIsHumanGate`
- [x] **Negative control:** the exemption does not apply when a gate is not the only successor — `TestExecUnverifiedHoldAppliesWhenGateIsNotTheOnlySuccessor`
- [x] **Negative control:** a node with no success edge is not exempt — `TestExecUnverifiedHoldAppliesWhenNoSuccessEdge`
- [x] Holds are listable and distinguishable from `wait_human` gates — `TestListUnverifiedHolds`, `TestListUnverifiedHoldsExcludesWaitHumanGate`
- [x] A hold appears in the control pane's pending queue — `TestLoadPendingGates_IncludesUnverifiedHold`
- [x] A hold is approvable from the queue — `TestApprovableNow`, `TestGraphUI_ApproveUnverifiedHoldFromQueue`
- [x] The grant is single-use — consumed at `graph_exec.go:684`, comment and code verified

## Known gaps carried at close-out

Documenting shipped behaviour does **not** assert it is complete. Three gaps, verified:

1. **No end-to-end integration coverage.** All eleven pins are unit/TUI tests. No
   `scripts/test-*.sh` drives a real scratch daemon through a hold and a human release. The two
   scripts that match `grep unverified` (`test-echo-as-result.sh`, `test-prompt-mode.sh`) use the
   word for **console row verdicts**, an unrelated sense — a search for coverage finds them and
   would wrongly read as covered.
2. **The CLI cannot discover a hold.** `bus.ListUnverifiedHolds` has no `cmd/graph.go` surface, so
   `muxcode graph status` does not show holds. `muxcode graph approve <run> <node>` *does* release
   one — `ApproveGraphGate` does not check node type, and the marker it writes is what
   `unverifiedHoldReleased` reads — but a CLI-only user has no way to learn the hold exists.
   **Discoverable only from the TUI control pane.**
3. **`approved_by` is evidence, not enforcement.** Stated in the doc comment at `:176-180`: the
   marker "remains forgeable by writing this marker directly", so it is "good enough to refuse a
   self-release, not good enough to be the only thing standing between an agent and an irreversible
   action." That enforcement is [`MUX-144`](../backlog/MUX-144-wait-human-gate-openable-by-any-agent.md).

### Deliberately not a defect

**Approval friction is by design.** One approval per unverified node, frequent on non-hook
providers, is the accepted cost of the mechanism. Anyone reading the friction as a bug and
"fixing" it re-opens the hole. In particular, **adding an explicit `outcome: unknown` edge to a
template silently disables the hold for that node** — that is the 2026-09-03 near-miss above, and
it is the single most likely way this protection gets removed by accident.

### Scope boundary — what it does not catch

It catches **`unknown` only**. A node whose agent declines its task while running a successful
shell command produces a confident `success`, so no hold fires and no human is asked. That is
[`MUX-148`](../backlog/MUX-148-node-outcome-reads-command-ran-as-task-done.md), tracked separately
and ranked tier 0 — not restated here.

## Follow-on hardening

Tracked, not abandoned. These sit under a non-`Phase` heading deliberately: `SpecPhases()`
(`bus/spec_items.go:97-107`) counts only items beneath a `### Phase N` heading, so these do not
hold the spec open or re-spawn a worker.

- [ ] End-to-end script driving a scratch daemon through a hold: node parks, an agent release is
      refused and logged, a person's release succeeds, the run advances — with a negative control
      that a gate-exempt node never parks
- [ ] A CLI surface for holds so `muxcode graph status` shows them alongside `wait_human` gates
- [ ] A line in `CLAUDE.md` and `docs/architecture.md` — the mechanism is absent from both

## Status

**Complete — retroactive documentation, 2026-09-04.** Behaviour shipped in `16f2027` with
amendments in `573ed2e`, pinned by eleven tests in `bus/graph_exec_test.go` and
`tui/graph_ui_test.go`. Closed with the three [Known gaps](#known-gaps-carried-at-close-out) above
recorded rather than resolved; the spec documents what exists, and does not claim the gaps are
closed.
