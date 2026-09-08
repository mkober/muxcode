# A Codex Status Line Closes Tracked Tasks as Their Answer

Five times on 2026-09-08 a `type: response` row whose entire payload was the Codex TUI's progress
line — `• Working (2m 08s • esc to interrupt)` — completed a tracked task. Each carried a `reply_to`,
so it correlated to a real request; `MarkResponded` drained that request from the inbox; the daemon
recorded the task as succeeded. The real answer, when it came, had nothing left to correlate to.

Two of those "successes" fired the `verify-spec` requests plan handled this afternoon — **1m45s before
the review agent's genuine reply** said two findings were unresolved.

It converts *nothing happened* into *done*, and it disarms the recovery path with the same stroke.

Tracking: _(no GitHub issue yet)_

## Context

### Observed (2026-09-08; every row read from `log.jsonl` by plan, not relayed)

| Time | From → to | Payload | `reply_to` |
|------|-----------|---------|------------|
| 13:53:07 | build → edit | `• Working (13s • esc to interrupt)` | `1788889973-edit-…` |
| 13:55:02 | build → edit | `• Working (2m 08s • esc to interrupt)` | `1788890087-edit-…` |
| 14:00:02 | test → edit | `• Working (14s • esc to interrupt)` | `1788890388-edit-…` |
| 14:12:55 | review → test | `• Working (13s • esc to interrupt)` | `1788891160-test-…` |
| 14:13:25 | review → test | `• Working (42s • esc to interrupt)` | `1788891191-test-…` |

The lifecycle log then reads, to the second:

```
14:12:55  task-detected   review task review from test: succeeded
14:13:25  task-detected   review task review from test: succeeded
14:13:42  plan-verify     docs/requirements/drafts/MUX-144-…
14:14:37  plan-verify-suppressed  nothing moved since last verify-spec
```

The reviewer's genuine reply is the row at **14:14:40**: *"P1 caller-controlled gate authority and
P2 selection-pinned flat-view scrolling remain unresolved"*. The daemon had judged the review a
success — twice — and dispatched the verifier on it before the reviewer had said anything.

### Consequences, each observed

| | |
|---|---|
| `muxcode tasks` | "No in-flight tasks" — the real result has nothing to correlate to, so the daemon never wakes the sender with it |
| Edit's `--wait` | returned the status line as the answer, three times |
| `muxcode deliver build --force` | "nothing pending" — `MarkResponded`'s `ConsumeByID` had drained the request, so the recovery path is disarmed by the bug it would recover from |
| A build that never ran | read as done (the codex build agent produced no result all session) |
| A chain link and `verify-spec` | fired on a non-result |

### Mechanism — verified

`CodexProvider.DetectTaskCompletion` (`bus/provider_codex.go:404-470`) reads the pane and decides in
order:

1. **Active signals** → still running: braille spinners `⠋ ⠙ ⠹ ⠸`, `▸`, or the word `thinking`
2. A `Sent … to …` line in the last ten → the agent replied; done
3. A `›` or `>` prompt in the last three lines → done, **summary = the last content line**

Codex's current TUI renders progress as `• Working (13s • esc to interrupt)` — a bullet, not a
braille spinner, no "thinking" — and keeps its `›` composer visible while working. So step 1 passes
it, step 3 fires, and the summary is the progress line itself. The synthesized response is then
`Send()`-ed with the request's id as `reply_to`, which calls `MarkResponded` (drains the request)
and lets `checkTrackedTasks` (`daemon/daemon.go:2667`, completion at `:2712`) close the task — with
**no provenance check** on either road.

The signature is already known — on two *other* roads:

| Road | Where | What it does with `esc to interrupt` |
|------|-------|--------------------------------------|
| Console history | `bus/history_provenance.go:57`, `LooksLikeNonResult` (`:85`) | keeps a synthesized row from rendering as a pass — [`MUX-003`](../completed/MUX-003-echo-as-result.md) |
| Claude pane classifier | `bus/provider_claude.go:178` | treats it as a **working** signature |
| Codex completion | `bus/provider_codex.go:413` | **does not know it** |

A control verified on one road is not verified on all of them — the MUX-142 lesson, again.

### Relationship

| Spec | Relationship |
|------|--------------|
| [`MUX-003`](../completed/MUX-003-echo-as-result.md) | Closed the console-history road: a synthesized row never renders as a pass. Did not touch task completion. Its guard (`LooksLikeNonResult`) is the one to reuse, not re-derive |
| [`MUX-148`](./MUX-148-node-outcome-reads-command-ran-as-task-done.md) | Graph-executor half of the same family — a node outcome reads "a command ran" as done. That one is about authoritative-row provenance in `graph_exec`; this is the tracked-task store. Same defect shape, different consumer |
| [`MUX-009`](./MUX-009-response-echo-chain-retrigger.md) | A *response* injected back as a *prompt* on the receiving side. This is a status line synthesized as a *response* on the sending side. Distinct mechanisms, both "the bus believes a TUI" |
| [`MUX-127`](./MUX-127-review-completion-routing.md) | Routes the chain on review outcomes; this corrupts the outcome it routes on |
| [`MUX-153`](./MUX-153-codex-test-agent-cannot-run-the-suite.md) | Why the codex test agent has no real answer to give — this defect is what turns that silence into "done" |

## Requirements

### Acceptance criteria

- [ ] A response synthesized from a pane never completes a tracked task unless the pane shows a
      genuine completion — a `Sent … to …` line or a real result line — and never a progress line
- [ ] `DetectTaskCompletion` recognizes Codex's `• Working (… esc to interrupt)` line as an **active**
      signal, using the **same** signature definition `history_provenance.go` already holds — one
      definition, not a third copy
- [ ] A progress-line payload never drains the request from the inbox — `MarkResponded` /
      `ConsumeByID` are not reached — so `deliver --force` still has something to deliver
- [ ] A chain link, `verify-spec`, or graph-node completion never fires on a synthesized non-result
- [ ] Negative control: a genuine codex reply (`Sent response…` in the pane) still completes the task
      and fires the chain exactly as today
- [ ] Negative control: Claude-provider tasks are unaffected

### Technical approach

Two layers, because the pane heuristic will always be a heuristic. **Recognize** the progress line
as active in `DetectTaskCompletion` by consulting the shared signature in `history_provenance.go`
(and fold `provider_claude.go:178`'s copy into it while there). Then **refuse** at the consumer: the
synthesized-response send path and `checkTrackedTasks` decline to complete on a payload that
`LooksLikeNonResult`, log a `task-nonresult-ignored` lifecycle row, and leave the request in the
inbox. The second layer is what makes the first layer's inevitable misses harmless.

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/provider_codex.go` | `DetectTaskCompletion` (`:404-470`), `SendWakeUp` (`:275`) — where the status line becomes a response |
| `tools/muxcode/bus/history_provenance.go` | `"esc to interrupt"` (`:57`), `LooksLikeNonResult` (`:85`) — the guard to reuse |
| `tools/muxcode/bus/provider_claude.go` | `:178` — the Claude copy of the same signature |
| `tools/muxcode/daemon/daemon.go` | `checkTrackedTasks` (`:2667`), `CompleteTask` sites (`:2712`, `:2805`, `:2870`) |
| `tools/muxcode/bus/delivery.go` | `MarkResponded` → `ConsumeByID` — the drain |
| `scripts/test-echo-as-result.sh` | MUX-003's guard test — extend to the task path or sibling it |

## Implementation

### Phase 1: Pin

- [ ] Characterization test: a pane fixture ending `• Working (13s • esc to interrupt)` above a `›`
      prompt → `DetectTaskCompletion` returns `completed=true` with the progress line as summary
      today; failure message names Phase 2
- [ ] Pin that the synthesized response completes a tracked task and drains the request from the
      inbox (scratch bus)
- [ ] Reconstruct the 14:12:55 / 14:13:25 rows from the bus log as the fixture's payload — the pin
      should be the incident, not an invented shape

### Phase 2: One signature

- [ ] Move working-signature detection to a single predicate in `history_provenance.go`; the codex
      bullet-`Working` line joins it
- [ ] `DetectTaskCompletion` and `provider_claude.go`'s classifier both consult it
- [ ] Invert the Phase 1 characterization test
- [ ] Negative control: a genuine `Sent response…` pane still detects as complete

### Phase 3: Refuse at the consumer

- [ ] The synthesized-response send declines a payload that `LooksLikeNonResult`; the request stays
      in the inbox
- [ ] `checkTrackedTasks` never completes a task on such a payload; lifecycle `task-nonresult-ignored`
- [ ] Negative control: a real response completes the task, fires the chain, and drains as today
- [ ] Negative control: Claude-provider task completion unchanged

### Phase 4: Integration test

- [ ] Create `scripts/test-status-line-task-close.sh` (hermetic; scratch bus + daemon + a fake codex
      pane) or extend `scripts/test-echo-as-result.sh` to the task path
- [ ] Test: progress-line pane → task stays in flight, request remains in the inbox, no chain fire,
      `task-nonresult-ignored` row written
- [ ] Test: genuine `Sent …` pane → task completes, chain fires (negative control — the guard cannot
      go inert)
- [ ] Test: `deliver --force` after a progress line still has the request to deliver
- [ ] Coverage floor keeps a skipped section from reporting green
- [ ] Run the script and verify all checks pass

## Notes

Filed 2026-09-08 by plan from edit's handoff (`/tmp/mux-new-findings-20260908.md`). Edit reported
three echoes; plan found five in `log.jsonl`, matched two of them to the second to the daemon's
`task-detected … succeeded` rows, and established that the `verify-spec` plan was handling at the
time had been fired by them — so this spec's own evidence includes the request that led to its filing.
The mechanism was read from `DetectTaskCompletion` directly.

**Placement argument** (tier 0): it corrupts the completion signal every other control consumes —
the chain, tracked tasks, `verify-spec`, recovery — it converts silence into success, it fired five
times today, and it disarms `deliver --force`. MUX-148 is the same family on the graph road and sits
at #2; this is the task road, and it is firing.

## Status

**Backlog** — filed 2026-09-08. Not started.
