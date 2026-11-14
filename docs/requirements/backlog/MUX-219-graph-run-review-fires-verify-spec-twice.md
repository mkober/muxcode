# MUX-219: A Graph Run's Review Fires `verify-spec` Twice

**Tracking:** [mkober/muxcode#200](https://github.com/mkober/muxcode/issues/200)

When a graph run owns the review, plan is asked to verify the same review twice in the same second:
once by the daemon's review chain (`notifyPlanOnReview`) and once by the run's own `update-spec`
node. Plan answers both. Every phase of every `55-defect-backlog-loop` and `50-spec-to-pr` run pays
a duplicate plan turn, and the two passes edit the same spec file's checkboxes.

## Context

### Source and standard of evidence

Filed 2026-11-14 on the user's instruction relayed by edit, from edit's handoff
(`/tmp/defect-verify-dup.md`). Plan was the recipient of every request below and confirms the pairs
first-hand; the lifecycle rows were read from `muxcode lifecycle show --event plan-verify`. The
mechanism is read from the tree; the fix is not designed beyond the approach below.

### Observed (2026-11-14, run `1794626267-55-defect-backlog-loop-15547e2e`)

| Time | Spec | Daemon review-chain request | Graph `update-spec` request |
|------|------|-----------------------------|-----------------------------|
| 09:26:37 | MUX-189 Phase 1 | `1794666397-daemon-ed9e00b5` | `1794666397-daemon-3c0a782b` |
| 09:34:21 | MUX-189 Phase 2 | `1794666861-daemon-864ca161` | `1794666861-daemon-d975796d` |

Both requests of a pair arrived in one inbox read. Plan did the verification once and answered the
second "Same verification as the graph's request". The same pairing ran through MUX-216, MUX-217
and MUX-009 on 2026-11-13 and 2026-11-14.

Two neighbouring fires show where the line falls:

| Time | What fired | Why it matters |
|------|------------|----------------|
| 09:31:40 | Daemon request only (`1794666700-daemon-c4898961`), for a graph-owned review that returned `EXIT=0` with 1 should-fix | The graph routed that review to its `fix` node and asked for no verification; the daemon asked anyway. Suppression covers this too |
| 09:18:40 | Daemon request only (`1794665920-daemon-a59f9ae8`), for a review edit requested by hand while the run sat at `ship-gate` | Not graph-owned. It was the only verification of a fix made after close-out, and must keep firing |

### Mechanism — read from the tree

| Step | Code | Behaviour |
|------|------|-----------|
| A review reply reaches edit | `daemon/daemon.go:477` | `d.notifyPlanOnReview(reply.ID, outcome, evidence)` for any review reply whose verdict passes the `NotifyPlanOn` gate |
| The daemon sends plan `verify-spec` | `daemon/daemon.go:546-619` (`notifyPlanOnReview`) | Checks verdict, active spec and movement since the last fire; logs `plan-verify <spec>`. It does not ask whether a graph run dispatched the review |
| The run sends plan `verify-spec` | the template's `update-spec` send node | Fires on the review node's success edge, carrying the worker's report and requiring an `EXIT=<n>` sentinel |
| The precedent | `cmd/hook.go:180` | A graph-owned test run's success logs `chain-suppressed` naming the run and node and fires no test→review (MUX-213) |

`plan-verify-suppressed` already exists as an event (`daemon.go:578`, "nothing moved since last
verify-spec"); a graph suppression needs a detail that reads differently, or its own event name.

### Cost

- One wasted plan turn per phase, on every graph run with a review node.
- Two requests for one piece of work invite two edits of the same checkboxes. Plan has so far
  noticed the pair and answered the second without re-editing; nothing enforces that.
- The daemon's request carries no worker report and names only the last routed batch's files
  ([MUX-150](./MUX-150-verify-spec-names-last-routed-batch.md)), so when it arrives first plan
  verifies with less than the graph's request would have given it.

### Family

- [MUX-213](../completed/MUX-213-unrequested-test-runs-fire-review-chain-and-reviews-answer-twice.md) — graph ownership suppressing a hook chain, and every review answered twice.
- [MUX-172](../completed/MUX-172-plan-verify-gate-hardcodes-success.md) — the gate this fire already passes through.
- [MUX-150](./MUX-150-verify-spec-names-last-routed-batch.md) — what the daemon's request names.

## Requirements

### Acceptance criteria

- [ ] A review dispatched by a graph run's executor sends plan no daemon `verify-spec`; the run's `update-spec` node is the one verification
- [ ] The withheld fire writes one lifecycle row naming the run and node, distinguishable from the existing "nothing moved" `plan-verify-suppressed` row
- [ ] A withheld fire leaves the movement marker unwritten, so a later review outside the run still fires
- [ ] A graph-owned review that routes to `fix` (no `update-spec`) also sends plan nothing from the daemon
- [ ] Negative control: a review requested outside any graph run still fires exactly one `verify-spec`, including while a run is parked on a gate
- [ ] The reviewed marker and the `reviewed` workflow transition still happen for a graph-owned review
- [ ] `bash scripts/test-graph-verify-once.sh` passes

### Technical approach

Decide ownership from the review **request**, not from whether a run happens to be live: a dispatch
carries its run and node (`Message.GraphRun` / `GraphNode`). In the daemon's review-reply handling,
resolve the request the reply answers; if it names a graph run, log the suppression with run and
node and return before `notifyPlanOnReview` writes its movement marker. A run merely existing — the
09:18:40 case, parked at `ship-gate` — must not suppress.

### Key files

| File | Role |
|------|------|
| `tools/muxcode/daemon/daemon.go` (`notifyPlanOnReview`, the review-reply branch near `:477`) | Where the daemon fires and where the ownership check goes |
| `tools/muxcode/bus/message.go` (`GraphRun`, `GraphNode`) | The provenance a dispatch carries |
| `tools/muxcode/bus/graph_exec.go` | How the executor stamps its review dispatch |
| `tools/muxcode/cmd/hook.go` (`chain-suppressed`) | The precedent's wording |
| `scripts/test-plan-verify-verdict.sh` | The existing script for this fire; its cases must still pass |
| `CLAUDE.md`, `docs/architecture.md` | The spec-verification passage |

## Implementation

### Phase 1: Pin the duplicate

- [ ] Unit test (kept as the negative control's mirror): a review reply answering a graph-stamped request reaches `notifyPlanOnReview` today and a `verify-spec` is sent
- [ ] Confirm from the tree whether a review reply can be traced to its request's `GraphRun` in the daemon's review branch; record what is available here

### Phase 2: Suppress for graph-owned reviews

- [ ] A graph-owned review reply sends plan nothing from the daemon, writes the suppression row naming run and node, and leaves the movement marker unwritten
- [ ] A review outside a run fires exactly as before, including with a run parked on a gate
- [ ] The reviewed marker and transition are unchanged for both
- [ ] `CLAUDE.md` and `docs/architecture.md` spec-verification passages updated

### Phase 3: Integration test

- [ ] Create `scripts/test-graph-verify-once.sh` — hermetic: scratch daemon, a graph with `review` → `update-spec`, stub review and plan agents
- [ ] Test: plan receives exactly one `verify-spec` for the run's review, and the suppression row names the run and node
- [ ] Test: a graph-owned review routed to `fix` sends plan none
- [ ] **Negative control:** a hand-delegated review with no graph fires exactly one `verify-spec`; so does one requested while a run waits on a gate
- [ ] Coverage floor; run the script and record the counts here

## Out of scope

- What the daemon's request names as changed files — [MUX-150](./MUX-150-verify-spec-names-last-routed-batch.md).
- Whether plan should refuse a second verification of the same review on its own.

## Status

Backlog

Filed 2026-11-14 on the user's instruction relayed by edit. Observed on run `1794626267`; mechanism
read from `daemon/daemon.go`. Not started.
