# MUX-182: A Cancelled Run Keeps Working, and Nothing Downstream Can Tell Who Launched It

A graph run the user launched by hand was **cancelled by an agent that misread its provenance**. The
cancel reported success, every downstream node went `skipped` — and the `implement` **spawn kept
running for 8m45s**, sharing the working tree. In that window the orphan dispatched two AWS discovery
runs, **silently edited its own execution script between them** (a profile swap plus an undisclosed
extra AWS account), and prompted the plan agent to edit a requirements doc. Only an explicit
`muxcode spawn stop` ended it.

Plan then wrote **"on a user request"** into that doc. The prompt carried no provenance, so the claim
was unverifiable by anyone downstream — including plan, which asserted it anyway.

**A cancelled run mutated files and called AWS minutes after being cancelled, and a false provenance
claim entered a spec. Neither is recoverable by reading the run state.**

## Context

### Source and standard of evidence

Reported from a live session on **2026-09-14**, repo `is-operations-gateway`, graph run
`1789392738-spec-to-pr-f337bb22`. **That session's run state and logs are not in this repo**, so the
incident narrative above is second-hand and is recorded as reported.

**Every mechanism claim below was verified independently against this repo** at `11e5559` before
filing. Where a report's framing did not survive that check, the correction is stated rather than the
framing repeated — see defect 1.

### Defect 1 — provenance is unambiguous in exactly one surface, and ambiguous in the rest

The edit agent read the run's `Started by: user` label, concluded it meant the **auto** agent's
launch-on-restore ([MUX-141](../backlog/MUX-141-auto-agent-restart-relaunches-graph-runs.md)'s shape), and
cancelled. A second agent (plan) independently made the same error, letting `state=canceled` carry the
conclusion despite having read `created_by: user`.

**Verified — and the report's root-cause framing is half right:**

| Surface | Wording | Ambiguous? |
|---|---|---|
| Gate request messages | `runProvenance` (`graph_exec.go:164`) — **"launched by: the user, by hand"** vs `"<actor> (autonomous)"` | **No** |
| `graph status` / `graph runs` | `"Started by: %s"` (`graph_run.go:773-774`) | **Yes** — renders `user` bare |
| `run.json` | `created_by: user` | **Yes** |
| `graph-run-created` lifecycle event | `"started by user"` | **Yes** |

**The unambiguous wording already exists** — `runProvenance` was written for this exact failure, and
its doc comment records the same class on 2026-09-03: *"an agent refused a gate as an 'auto-launched
run' that the user had started a minute earlier."* It was applied to gate messages only. **The defect
is that three other surfaces still render the ambiguous form**, so the fix is to extend a mechanism
already in the tree, not to invent one.

**Correction to the report's second claim.** It proposes that *"if the auto agent can produce
`created_by: user`, that is itself the defect."* **It cannot, by construction:** `CreateGraphRun`
(`graph_run.go:197`) uses `BusActorVerified()` (`config.go:167`), which resolves the nearest agent
runtime by **process ancestry** and returns `ActorUnknown` rather than `user` when the process table
is unreadable — "could not tell" is never read as "no agent above me". The forgery concern is already
closed. **What is not established** is whether `agentRuntimeAncestor` recognises the `auto` agent's
runtime specifically; that is a Phase 1 check, not an assumption.

### Defect 2 — `graph cancel` does not stop running spawns

**Verified in code.** `CancelGraphRun` (`graph_exec.go:233`):

```go
switch st.State {
case GraphNodePending, GraphNodeReady, GraphNodeWaiting:
    _ = TransitionGraphNode(session, runID, id, GraphNodeSkipped, nil)
}
```

- **`GraphNodeRunning` is not in the switch** — a running node is left untouched.
- **`StopSpawn` is never called on the cancel path.** The only `StopSpawn` in the executor
  (`:1466`) is inside `replaceLostWorkers`'s `failClosed`, an unrelated worker-replacement path.
- The correlated task *is* expired (`TimeoutTask`), which stops the stall watchdog re-driving it —
  but expiring a task does not terminate a spawn's process.

So `graph cancel` marks the **run** cancelled while its **worker keeps its worktree and keeps
working**. With `worktree=shared` the orphan writes into the live checkout. The run reports
`[canceled]`; the machine disagrees.

### Defect 3 — an agent can cancel work it did not start

No authority check exists on `graph cancel` or `spawn stop`. `CancelGraphRun` takes a run id and
proceeds. This is the mirror image of [MUX-144](../completed/MUX-144-wait-human-gate-openable-by-any-agent.md):
that spec gated **releasing** a gate on an authorized, audited approval; **stopping** a human's run
is ungated and unaudited — `graph-run-canceled` logs the run id and **not the actor**.

### Defect 4 — two reporting channels that state conclusions they did not reach

**(a) The watch chain's completion banner is a fixed string.** Verified: `bus/profile.go:980` sends
`"Watch completed — logs look healthy after deploy (${command})"` as the chain action's message,
**independent of anything watch found**. It fired verbatim while the underlying run had failed on an
expired SSO session and had verified nothing — and produced a false accusation that the agent had
fabricated results, when the agent's own substantive messages were accurate. **The banner, not the
agent, made the claim.**

**(b) A pane scrape is delivered as a response, and closes the task.** Verified:
`daemon/daemon.go:3441-3450` builds `NewMessage(..., "response", "response", "[daemon: <role> went
idle without responding (retried once) — pane content follows]\n" + paneContent, task.ID)` and then
calls `CompleteTask`. **Corroborated first-hand: the plan agent received exactly this message during
the MUX-148 work on 2026-09-14** and had to recognise it as a non-answer from its text. A scrape is
raw terminal state, not a conclusion; occupying a `response` body and completing the task makes
"the agent never answered" indistinguishable from "the agent answered".

### Defect 5 (residual) — a self-addressed startup reply still trips the loop detector

Reported: 4 × `edit ↔ edit action:startup` in 2m59s, two of them replies the CLI itself reported as
`(not delivered)`.

**Scoped against the tree: this is a narrower residual of
[MUX-169](../completed/MUX-169-startup-self-reply-echo-loop.md), not a regression of it.** That spec's
fix drops self-addressed sends at the source (`isLoopingSelfSend`, `bus/inbox.go:122`) with the
launch-time bootstrap exempt, which is why the replies report `(not delivered)` — **delivery is
correctly suppressed**. What MUX-169 did not do is stop the **detector counting** the suppressed
attempts, or stop the CLI **advertising a reply affordance** on a message whose reply can never be
delivered. An agent following the printed instruction generates guaranteed-dead sends that then read
as a loop.

## Requirements

### Acceptance criteria

- [x] `graph cancel` terminates in-flight spawn nodes, **or** refuses to report the run cancelled while naming exactly which spawns survived and how to stop them — Phase 2 `bus/graph_cancel.go`, closed 2026-09-24 after three review iterations (unit level: survivors and every failed cleanup step named in `CancelIncompleteError`); confirmed live by Phase 6's script — a worker dead by window and process, and a refused cancel whose named survivor really was alive
- [x] A cancelled run cannot mutate files or call an external API after the cancel returns — measured live in Phase 6: the worker's heartbeat file grows before the cancel and stops after it, and the worker is dead by process and window; the API half follows from the dead process (Phase 2's limit stands: a request another agent had *already consumed* cannot be recalled — that agent now sees the run state on its message)
- [x] Every provenance surface (`run.json`, `graph status`, `graph runs`, `graph-run-created`) distinguishes "launched by the user by hand" from "launched autonomously by `<agent>`", in wording an agent cannot misread as the other — Phase 3 routes every surface through `DescribeRunCreator`, closed 2026-09-24 11:13 once the TUI run-list clamp preserved the category (the last surface that could clip `(autonomous)`)
- [x] **Negative control: a genuinely agent-launched run is still labelled autonomous** — a fix that labels everything "user" is not a fix — Phase 3, every provenance test paired manual/`auto`
- [x] A spawn-originated bus message carries its originating run id, `created_by`, and current run state, so a recipient can verify whether a prompt traces back to a human — Phase 3, `stampMessageOrigin`; `TestSpawnMessageCarriesRunOrigin`, `TestSpawnMessageFromCancelledRunSaysSo`
- [x] An agent cannot assert human provenance it did not receive — plan writing "on a user request" is unsupported unless the prompt carried it — Phase 3: the prompt now states its origin or states that it carries no user request, and a forged human origin is discarded at send (`TestUnprovenancedMessagesSayNoUserRequest`, `TestStampDiscardsForgedHumanOrigin`); whether agents honour it is what Phase 6 exercises
- [x] `graph cancel` / `spawn stop` issued **by an agent** against a run whose `created_by` is a human requires explicit user approval; agent-launched runs stay freely cancellable — Phase 4, `CheckCancelAuthority`; the approval is the user issuing the stop; negative control `TestCancelGraphRunLeavesAgentRunsFree`
- [x] `graph-run-canceled` records **who** cancelled, as `graph-gate-approved` records who approved — Phase 4, sourced by the actor with `canceled by <actor>`
- [x] The watch completion notification carries the agent's real summary, or is neutral (`"Watch completed — see result"`); it never asserts a finding the chain did not establish — Phase 5, neutral wording plus an audit test over every default notice
- [x] A daemon pane scrape is never delivered in a `response` body and never completes a task as though answered — it is marked unmistakably as a non-answer — Phase 5, `event:no-answer` from `daemon`, task timed out; `TestIdleRescueIsNeverAResponse`
- [x] A suppressed self-addressed startup reply is excluded from loop detection, or the reply affordance is not printed for it — Phase 5, excluded from detection (the affordance is unchanged)
- [x] `bash scripts/test-cancel-provenance.sh` passes — 57/0 on 2026-09-24 12:0x through the run agent (task `1790265785-spawn-4d7bf287-3089eb85`), floor 56 met

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/graph_cancel.go` | `CancelGraphRun` (moved here in Phase 2): `runSpawnRoles`, `stopSpawnRole`, `retractSpawnDelegations`, `CancelIncompleteError` |
| `tools/muxcode/bus/graph_exec.go` | `StepGraphRun:247` takes the run lock for the tick; `replaceLostWorkers.failClosed` (stops by role since Phase 2), `runProvenance:164` (the wording to propagate) |
| `tools/muxcode/bus/task.go` | `expireTask`, `scanTasks` — checked expiry and strict enumeration for the cancel path (Phase 2 must-fix 3); `TimeoutTask`/`ListTasks` stay best-effort for other callers |
| `tools/muxcode/bus/run_provenance.go` | `DescribeRunCreator` (the one vocabulary), `stampMessageOrigin`, `formatMessageOrigin` — Phase 3 |
| `tools/muxcode/bus/graph_run.go` | `CreatedBy`, `MarshalJSON` (derived `provenance`), `Launched by:` render — the ambiguous `Started by:` fixed in Phase 3 |
| `tools/muxcode/bus/config.go` | `BusActorVerified:167`, `agentRuntimeAncestor` — does it recognise `auto`? |
| `tools/muxcode/bus/spawn.go` | `StopSpawn`, spawn lifecycle and worktree mode |
| `tools/muxcode/bus/commit_authority.go` | `CheckCommitAuthority*` — the pattern to mirror for a cancel-authority check |
| `tools/muxcode/bus/cancel_authority.go` | `CheckCancelAuthority`, `StopSpawnAuthorized` — Phase 4; `lockExistingGraphRun` in `graph_cancel.go` serializes the decision with retry |
| `tools/muxcode/bus/profile.go` | `:980` — the fixed watch banner |
| `tools/muxcode/daemon/daemon.go` | `:3441-3450` — pane scrape sent as a response and completing the task |
| `tools/muxcode/bus/inbox.go` | `isLoopingSelfSend:122`, `DetectMessageLoop` — defect 5 |

## Implementation

### Phase 1: Establish the boundary

- [x] Confirm whether `agentRuntimeAncestor` recognises the `auto` agent's runtime, or can return `user` for an auto-launched run
- [x] Enumerate every surface that renders run provenance and every consumer that reads it
- [x] Determine what a `spawn` node's worker actually is (process, window, worktree) and what terminating it requires
- [x] Establish whether `spawn stop` alone is sufficient to end an orphan, or whether the worktree also needs reclaiming
- [x] Record findings here before choosing an approach

#### Phase 1 findings — 2026-09-23

Investigated by the `implement` worker of graph run `1790189504-spec-to-pr-a113bedb` (spawn
`spawn-2d3e8636`, tree `a762869`); every claim re-verified by plan against the same tree before it was
recorded here, and one did not survive (Q1). No code changed. **The run that produced these findings
reproduced defect 2 live** — see "Live evidence" below.

**Q1 — can `auto` yield `created_by: user`? No, on any road that keeps `AGENT_ROLE`.** Every agent
launches through `RunAgentLaunch` (`bus/launch.go:1021`), which exports
`AGENT_ROLE=NormalizeBusRole(role)`; `BusActorVerified` (`config.go:167`) takes a set `AGENT_ROLE` as
given, so an auto-launched run records `created_by: auto` and gates render `launched by: auto
(autonomous)`. Runs are created on two roads only, both in the caller's process (`cmd/graph.go:181`,
`tui/graph_ui.go:1426`); the daemon never calls `CreateGraphRun`, so MUX-141's shape can only arrive
through the auto agent's own CLI call. The worker reported a hole for a stripped `AGENT_ROLE`: an
nvm-installed Codex runs as `node …/bin/codex`, basename `node`, which `agentRuntimeAncestor` does not
recognise → `ActorUser`. **Not reproduced.** The `node` wrapper spawns the native
`…/codex-darwin-arm64/vendor/aarch64-apple-darwin/bin/codex` as its child (live `ps` 2026-09-23: pid
94982 ← 94772), that binary's basename `codex` is in `agentRuntimeNames` (`config.go:139`), and
`codex-code-mode-host` and every tool shell descend from it — so the upward walk (`config.go:207`)
reaches `codex` before it reaches `node`. Residual, cosmetic: when ancestry fires it returns the runtime
name, so provenance reads `claude (autonomous)` rather than a role.

**Q2 — every provenance surface and consumer.**

| Surface | Where | Current rendering |
|---------|-------|-------------------|
| Gate request messages | `graph_exec.go:1003`, `:1135` via `runProvenance:164` | unambiguous |
| `graph status <id>` | `graph_run.go:773-774` `formatGraphRun` | `Started by: user` — ambiguous |
| `graph status` (no id — the run list) | `cmd/graph.go:524-526` | **no provenance at all** |
| `graph status --json` (list and single) | `cmd/graph.go:516`, `:556` | raw `created_by` |
| `run.json` | `GraphRun.CreatedBy` `graph_run.go:50` | raw `created_by` |
| `graph-run-created` lifecycle + edit event | `graph_run.go:221-222` `announceGraphAction` | `started by user` — ambiguous |
| TUI graph screen | `tui/graph_ui.go` | **no provenance rendered** |
| `graph-run-canceled` | `graph_exec.go:258` | source hard-coded `daemon`, **no actor** |

Consumers that decide on it: `CheckGateApprovalAuthority` (`gate_authority.go:222`, the self-approval
check through `NormalizeBusRole(run.CreatedBy)`) and agents reading the text surfaces. Nothing else
reads `CreatedBy`. **Corrections to this spec's own wording:** there is no `graph runs` subcommand —
the list is `graph status` with no id, and it is one of two surfaces (with the TUI) that render nothing
rather than something ambiguous; Phase 3 and the acceptance criteria should read "four surfaces" as
those in the table.

**Q3 — what a `spawn` node's worker is.** A tmux window `spawn-<8hex>` (console left, agent right)
running `AGENT_ROLE=spawn-<hex> muxcode agent launch <role>` → exec into the provider CLI
(`spawn.go:184-221`). State is one `SpawnEntry` row in `spawn.jsonl`: `ID` = `spawn-<ts>-<hex>`,
`SpawnRole`/`Window` = `spawn-<hex>`, `RunID`, `NodeID`. **Graph workers never get a worktree** —
`graphSpawnFn` passes `useWorktree=false` (`graph_exec.go:53`, rationale `:67-75`), so every worker
edits the live session checkout. For spawn/map nodes `GraphNodeStatus.TaskID` holds comma-separated
**spawn role names**, not task ids (`lostSpawnWorkers` keys by `SpawnRole`).

**Q4 — is `spawn stop` alone sufficient? No, and there is no worktree to reclaim.** `StopSpawn`
(`spawn.go:302`) is `tmux kill-window` (errors only if the window survives) → `removeSpawnWorktree`
(a no-op for graph workers) → entry `stopped`. Nothing to reclaim — and nothing to roll back: the
orphan's edits are already in the shared checkout. Insufficient on its own for three reasons:

1. **Delegated work outlives the worker.** In the incident the AWS calls and the doc edit were made by
   the run and plan agents on requests the spawn had sent; killing the spawn retracts nothing already in
   another inbox. A spawn's own sends carry `From: spawn-<hex>` and no `GraphRun`/`GraphNode` (only
   executor dispatches set those), but `SpawnEntry` maps `SpawnRole` → `RunID`, so a cancel *can* find
   them: expire in-flight tasks and drain pending rows whose `From` is one of the run's spawn roles.
2. **Descendants may survive `kill-window`.** It delivers SIGHUP; a detached child (`nohup`, `setsid`,
   the [MUX-156](../backlog/MUX-156-orphaned-inbox-listener-consumes-into-the-void.md) orphan listener
   shape) is not guaranteed dead. **Unverified** — which is why Phase 6 proves death by process absence.
3. **Ordering is safe today.** `CancelGraphRun` flips the run to `canceled` before touching nodes, and
   `replaceLostWorkers` runs only from the executor over in-flight runs, so a stopped worker is not
   replaced. Phase 2 must keep that order.

**Latent bugs found, both verified in the tree — added to Phase 2:**

- `CancelGraphRun` never reaches a spawn node even for task expiry: `ReadTask(session, st.TaskID)` is
  handed `spawn-<hex>` role names, no such task file exists, so its `TimeoutTask` branch never runs for
  spawn/map nodes.
- `replaceLostWorkers.failClosed` cannot stop anything: `launched` holds `SpawnRole`s (`graphSpawnFn`
  returns `entry.SpawnRole`, `graph_exec.go:57`) but `StopSpawn` → `GetSpawnEntry` matches `e.ID`
  (`spawn.go:86`), so every call errors `spawn not found` and the replacement is reported "still live"
  while it keeps running.

**Live evidence — the investigating run reproduced defect 2.** Lifecycle ledger, session `muxcode`:

| Time | Event |
|------|-------|
| 14:51:44 | `graph-run-created` `1790189504-spec-to-pr-a113bedb (spec-to-pr) started by user` |
| 14:51:45 | `graph-node-start … implement (spawn)`, `spawn-wake spawn-2d3e8636` |
| 14:53:05 | `graph-run-canceled 1790189504-spec-to-pr-a113bedb` |
| 14:54:05 | `spawn-complete 1790189505-spawn-2d3e8636 role=edit window=spawn-2d3e8636` |

`spawn.jsonl`: `started_at 1790189505`, `finished_at 1790189645`, `status: completed`. The worker kept
working for **60 s after the cancel returned**, finished the investigation and answered its seed; edit
saved that reply as the findings file at 14:59:32. Exactly the incident's shape, on this repo, with a
run cancelled by the user rather than by an agent — the cancel-authority question (defect 3) is
orthogonal to the orphan (defect 2).

**Recommended approach (Phase 2 onward):** resolve node → spawn entries by `SpawnRole`, stop by `ID`,
treat "window still live after kill" as a survivor and fail closed (Decision 1). On cancel also expire
in-flight tasks and drain unanswered requests from the run's spawn roles so delegated work does not
proceed. Phase 4 can rely on `created_by` as recorded; the ancestry extension the worker proposed
(`node <script>` launches, `codex-code-mode-host`) is not needed on the evidence above, but a
negative-control test that a stripped-`AGENT_ROLE` shell under Codex still resolves to `codex` would
pin the ancestry assumption Phase 4 rests on.

### Phase 2: Stop the orphan (defect 2)

- [x] Add `GraphNodeRunning` handling to `CancelGraphRun`, terminating spawn-backed nodes via `StopSpawn` — closed by the second review 2026-09-23: `lockGraphRun` (`<run dir>/run.lock`, flock) serializes the cancel with the executor tick
- [x] Resolve a spawn node's workers by `SpawnRole` (what `TaskID` holds) and stop each by `ID` — a stop-by-role helper; `replaceLostWorkers.failClosed` uses it too, since today it passes roles and every `StopSpawn` errors `spawn not found` (Phase 1 findings)
- [x] On cancel, expire in-flight tasks and drain unanswered requests originated by the run's spawn roles (`SpawnEntry.SpawnRole` → `RunID`), so delegated run/plan work does not proceed after the worker is dead (Phase 1 findings, Q4.1) — closed 2026-09-24: registry, task-scan and inbox errors propagate (must-fix 2), and expiry is checked (`expireTask`, `scanTasks`; must-fix 3), so a task the write cannot reach fails the cancel instead of being counted expired
- [x] On a spawn that cannot be stopped, **fail closed**: do not report the run cancelled; name the surviving spawns and the exact command to stop them — closed 2026-09-24: a surviving worker, an unreadable registry or task file, a failed retraction or a failed expiry each leave the run `canceling` and are named in `CancelIncompleteError` (must-fix 1, 2, 3)
- [x] Emit a lifecycle event for every spawn terminated or survived by a cancel
- [x] Unit tests, including the negative control: a run with no running spawns still cancels cleanly and reports success
- [x] **Review must-fix 1 (2026-09-23, `graph_cancel.go:62`) — closed by the second review the same day** (`lockGraphRun`: `StepGraphRun` holds the run lock for the whole tick, single try, skips the tick if busy; `CancelGraphRun` waits up to 30 s and errors without touching state if it cannot take it; `TestCancelWaitsOutSpawnCreation`, `TestCancelWaitsOutReplacement`, `TestStepSkipsWhileCancelHoldsRun`). Original finding: serialize cancel with executor dispatch and worker replacement — a daemon tick past `graph_exec.go:288` can spawn after the CLI sets `canceling` and snapshots the registry, so the run reports `canceled` and the worker then starts; `replaceLostWorkers` reads an already-loaded run the same way. A shared cross-process run lock, or a reservation/acknowledgement protocol; a state re-read alone leaves the check-to-spawn race. Test: cancel paused inside spawn creation and inside replacement
- [x] **Review must-fix 2 (2026-09-23, `graph_cancel.go:117/203/214/232`) — closed by the second review the same day** (`runSpawnRoles` and `retractSpawnDelegations` return their errors, `inboxRoles` treats only a missing directory as empty, `CancelIncompleteError.Cleanup` names each failed step and keeps the run `canceling`; `TestCancelFailsClosedOnUnreadableRegistry`, `TestCancelFailsClosedOnRetractionFailure`). Original finding: propagate `ReadSpawnEntries`, `ListTasks`, inbox-enumeration and `receiveMatching` errors — an unreadable registry was an empty worker set and a `canceled` run with live workers
- [x] **Review must-fix 3 (second review 2026-09-23, `graph_cancel.go:239` and `:121`) — closed 2026-09-24, third review 0/0/0** (`expireTask` returns its write error, `scanTasks` reports unreadable or invalid files, both collected into `Cleanup`; `TestCancelFailsClosedOnUnexpirableNodeTask`, `TestCancelFailsClosedOnUnreadableTaskFile`). Original finding: task expiry is still best-effort — `TimeoutTask` returns no error and discards `writeTask` failures (`task.go:73-83`), and `ListTasks` silently skips unreadable or invalid task files, so a delegated task whose file cannot be written is counted expired, its inbox request withdrawn, and the run reported `canceled` while the persisted task stays in-flight. Use an error-reporting expiry and strict enumeration on the cancel path, collect failures into `Cleanup`, apply the same checked expiry to node-correlated tasks. Tests: task read/write failure regressions plus a successful retry
- [x] **Review should-fix (second review 2026-09-23, `tui/graph.go:951`, `graph_run.go:588`) — closed 2026-09-24**: TUI summary is now `cancel incomplete — re-run graph cancel` and the retry refusal says "a worker survived or a cleanup step failed", neither asserting a survivor. Original finding: `canceling` now also means a cleanup step failed after every worker stopped, but both surfaces asserted "a worker survived"
- [x] **Follow-up review must-fix (2026-09-24)**: checked node-task expiry applied to every node's `TaskID` — but spawn and map nodes store worker *roles* there, not task ids, so a large map failed its cancel on "task not found" every retry. `runSendNodes` reads the frozen graph and restricts node-task expiry to `send` nodes; a graph-read failure is an explicit cleanup failure. `TestCancelLargeMapIsNotATaskID` (20-worker map, all stopped, re-cancel succeeds)

#### Phase 2 findings — 2026-09-23

Landed in run `1790194224-spec-to-pr-df3901e4` over two iterations. First: build and test green,
**review returned two must-fix** (`/tmp/muxcode-review-1790194802.txt`). Second, after the fix
re-seed: both closed (`/tmp/muxcode-review-1790195792.txt`, "Resolved"), **one new must-fix and one
in-scope should-fix raised**. Third, 2026-09-24: those closed plus a follow-up must-fix found and
fixed on the way (send-node-only task expiry), review `/tmp/muxcode-review-1790258608.txt` 0/0/0 —
every box above ticked. **In the first two iterations the run's `review` node recorded
`outcome=success` with a must-fix outstanding**, which is defect 4's shape (a channel stating a
conclusion it did not reach) and is noted here as evidence for Phase 5. Between the iterations the
run's `phase-gate` was approved and its `commit` node landed `56edc17` — the Phase 1 move and this
spec's docs only; the Phase 2 code stays uncommitted. `CancelGraphRun` moved out of
`graph_exec.go` into a new `bus/graph_cancel.go`. The boundaries it draws, recorded so later phases
build on what is true:

- **Cancel and the tick are serialized** by a per-run flock (`lockGraphRun`, `<run dir>/run.lock`):
  `StepGraphRun` holds it for the whole tick (single try — a busy lock skips the tick), so every
  dispatch, reseed and replacement happens inside one; `CancelGraphRun` holds it for the whole cancel
  and waits up to 30 s, erroring without touching state if it cannot take it. A tick therefore either
  registers its worker before the cancel reads the registry, or sees `canceling` and spawns nothing.
- **Expiry is checked, and typed by node.** `expireTask` returns its write error and `scanTasks`
  reports unreadable files; both feed `Cleanup`. Only `send` nodes hold a task id in `TaskID` —
  spawn and map nodes hold worker roles — so node-task expiry runs through `runSendNodes`; the
  workers' own delegated tasks are found by sender role instead.

- **Order of operations**: run → `canceling` first (a new `GraphRunCanceling` state, which halts
  dispatch and `replaceLostWorkers`) → stop every worker the run owns → retract their delegations →
  skip nodes → `canceled` only with zero survivors. `RetryGraphRun` refuses a canceling run; the TUI
  renders it red with `cancel incomplete — a worker survived; re-run graph cancel`.
- **Worker ownership** is resolved two ways: the `SpawnEntry.RunID` stamp, plus any spawn role a
  node's `TaskID` names — so a parked worker whose node is already `done` is found by the stamp.
- **Retraction reaches unconsumed requests and in-flight tasks only.** A request an agent has already
  consumed cannot be recalled; that recipient must notice the run state itself. This is AC 2's
  remaining gap — it stays open until Phase 6 measures it live — and Phase 3's run-state stamp on
  spawn-originated messages is the recipient's half of closing it.
- A running `send` node has no worker and is left to finish; only spawn/map nodes whose workers all
  stopped are skipped (`running → skipped` is a new legal node transition).
- Withdrawn rows are marked `expired` with no receipt (`receiveMatching` with an empty kind writes
  none), so a retraction can never read as a delivery.
- **Lifecycle**: `graph-cancel-spawn-stopped`, `graph-cancel-spawn-survived`,
  `graph-cancel-retracted`, `graph-cancel-incomplete`. `graph-run-canceled` still names no actor —
  that is Phase 4.
- **Tests**: `bus/graph_cancel_test.go`, five cases — running worker stopped with delegations
  retracted and a bystander control, parked worker, fail-closed survivor then successful re-cancel,
  no-spawn negative control (another run's live worker untouched), `failClosed` stop-by-role.
  `liveSpawnFake.distinctIDs` gives entries the production shape (ID ≠ SpawnRole), which is what
  exposes the stop-by-role bug the default shape hid.

### Phase 3: Make provenance readable (defect 1)

- [x] Route `run.json`, `graph status`, `graph runs` and `graph-run-created` through `runProvenance`'s vocabulary — 2026-09-24, `DescribeRunCreator` (`bus/run_provenance.go`): `run.json` and every `--json` carry a derived `provenance` beside raw `created_by`; `graph status <id>` renders `Launched by:` always; the list gains `launched by:`; the event says `launched by: …`; the TUI header line and run-list column follow
  - Phase 1 correction: `graph runs` does not exist — the list is `graph status` with no id (`cmd/graph.go:524`) and shows no provenance at all; the TUI graph screen shows none either. Both count among the surfaces to route, with `--json` carrying the raw field alongside
- [x] **Negative control test:** an agent-launched run still renders autonomous — every provenance test is table-paired manual/`auto`: `auto` must render `auto (autonomous)` and never contain "the user"; manual must never contain "autonomous" (`bus/run_provenance_test.go`, `tui/graph_test.go`)
- [x] Propagate originating run id, `created_by` and run state into every spawn-originated bus message — `Message.OriginRun/OriginCreatedBy/OriginRunState`, derived by `stampMessageOrigin` at `sendMessage` after the authority checks (a sender-supplied origin is wiped first); `FormatMessage` renders `Origin: graph run … · launched by: … · run state at send: …`, adding "the run was cancelled; do not act on this" for `canceled`/`canceling`
- [x] Make an unprovenanced prompt legible as such, so a recipient cannot assert human provenance it never received — a spawn tied to no run renders "carries no record of a user request", as does any request from an agent other than edit (daemon included); `SendHumanPrompt`'s road is stamped `ActorUser` ("typed by the user at the Prompt surface"); edit's own messages carry no line, being the consent boundary
- [x] **Review should-fix (2026-09-24, `tui/graph.go:1062`) — closed 11:13, review `/tmp/muxcode-review-1790262753.txt` 0/0/0** (`clampLaunchedBy` shortens only the actor and keeps ` (autonomous)` whole — `spawn-abcd… (autonomous)`; `launchedByWidth` 24 so "could not be established" fits; the selected run's full provenance renders on its own `▸ launched by:` line under the list, inside the height budget; `TestRunListKeepsProvenanceCategory` at widths 80/100/120/200 with a user negative control, `TestRunListProvenanceLineOnlyForSelection`). Original: the run-list column `clampCol(r.LaunchedBy, 20)` clips the category the vocabulary exists to preserve — `spawn-abcd1234 (autonomous)` becomes `spawn-abcd1234 (auton…`, and even `research (autonomous)` exceeds 20; the row is then clipped again at terminal width, and the test covers only `user`/`auto` at width 200. Preserve the category while shortening the actor, or render provenance on a wrapping/detail line; test a spawned or long actor at realistic widths. Holds AC 3 open

#### Phase 3 findings — 2026-09-24

Landed in run `1790258935-spec-to-pr-49587ed8`: build and test green, review
`/tmp/muxcode-review-1790259602.txt` 0 must-fix, 1 should-fix (the open box above). Worker report:
`/tmp/mux182-phase3-report.md`. Boundaries drawn:

- The vocabulary lives in one place, `DescribeRunCreator` (`bus/run_provenance.go`); `runProvenance`
  delegates to it, so gate messages and every other surface cannot drift apart again.
- `run.json`'s `provenance` field is **derived on every write and never read back** — `created_by`
  stays the record, the words are a rendering of it.
- The `Origin*` message fields are deliberately separate from `GraphRun`/`GraphNode`: those feed the
  commit-authority backstop, and a worker's own send must never be judged as an executor dispatch.
- Run state is captured **at send**, so a message from a run cancelled later still reads as sent
  while running; the cancel's retraction (Phase 2) is what withdraws it.
- No origin line on edit's messages (edit is the consent boundary) or on agent responses; every other
  request either names its run or says it carries no record of a user request.
- **Second review, 10:38** (`/tmp/muxcode-review-1790260651.txt`): the run-list clamp should-fix
  repeated unchanged; its three must-fix are on work that shares the tree but is not this phase —
  the HEAD-anchored `phaseCommitReady` of [MUX-183](./MUX-183-phase-commit-ready-recredits-shipped-phases.md)
  (two, recorded there) and the review node's new findings-count parser (one, recorded under Phase 5
  below, since it is the mechanism behind the `outcome=success`-with-must-fix evidence). Run
  `1790258935` was cancelled after its `phase-check`; the cancel stopped cleanly.
- **Closed 11:13 in run `1790262549-spec-to-pr-00360289`** — whose `update-spec` dispatch arrived
  carrying its own origin line, `graph run 1790262549 · launched by: the user, by hand · run state at
  send: running`: the phase's mechanism seen working on the message that verified it. The TUI clamp
  now keeps the category whole and shortens only the actor, and the selected run's full provenance
  has its own line; review 0/0/0. AC 3 released.

### Phase 4: Gate cancellation of human-launched runs (defect 3)

- [x] Add a cancel-authority check mirroring `CheckCommitAuthority`: an agent cancelling a human-created run requires explicit approval — 2026-09-24, `CheckCancelAuthority` (`bus/cancel_authority.go`): the approval *is* the user issuing the cancel; no flag, token or env can stand in for it. `CancelGraphRun` resolves `BusActorVerified()` itself (CLI and TUI roads alike) and refuses before touching state with a `graph-cancel-refused` row; `spawn stop` goes through `StopSpawnAuthorized` (`spawn-stop-refused`); `TestCheckCancelAuthority` (15 rows), `TestCancelGraphRunGatesHumanRuns`, `TestStopSpawnAuthorizedGatesRunWorkers`
- [x] Keep agent-launched runs freely cancellable — **negative control:** the autonomous path must not regress — `TestCancelGraphRunLeavesAgentRunsFree` (an `auto`-created run cancelled by edit, event names edit) plus the autonomous rows of the table test
- [x] Record the actor in `graph-run-canceled` — sourced by the actor, detail `<run> canceled by <actor>` (was source `daemon`, run id only)
- [x] Confirm the check cannot be bypassed by the daemon→edit role normalization, as MUX-144 Phase 4 had to — the check grants no role anything and compares only against `ActorUser`, so normalization gains nothing; `AGENT_ROLE=user` under an agent runtime falls to the ancestry walk and is refused; `TestCancelGraphRunNotBypassedByIdentity` (daemon identity, forged `AGENT_ROLE`, stripped `AGENT_ROLE` under codex)

#### Phase 4 findings — 2026-09-24

Landed in run `1790262549-spec-to-pr-00360289` (the run that also closed Phase 3): build and test
green, one fix iteration, final review `/tmp/muxcode-review-1790263753.txt` 0/0/0. Worker report:
`/tmp/mux182-phase4-report.md`. Boundaries drawn:

- **Unestablished is human.** A run whose creator is `unknown` or unrecorded may be stopped only by
  the user — the misreading this phase exists for is an agent deciding a run was *not* the user's.
- **A run already `canceling` or `canceled` is exempt**: re-running the cancel or stopping a survivor
  completes the user's decision, which is what `CancelIncompleteError` tells its reader to do.
- **Authority is decided under the run lock and held through the mutation** (`lockExistingGraphRun`,
  shared by cancel, retry and authorized spawn stop): it depends on state, and a canceled run read
  before the lock could be running again through `RetryGraphRun` before the cancel acts —
  `TestStopAuthoritySerializedWithRetry` lands a retry in that window. A lock or run read that fails
  is treated as unestablished provenance: user only.
- **`spawn stop` is gated only for a worker tied to a run.** Untied spawns, `TaskStop`, `proc kill`
  and reloads stay ungated — Decision 2's wider scope is still open, deliberately.
- The worker ran `go vet ./bus/` from edit once by mistake; it failed on a duplicate helper (since
  fixed) and may have left a failed precheck row in edit's test history.

### Phase 5: Fix the misleading channels (defects 4 and 5)

- [x] Replace the fixed watch banner with the agent's real summary, or make it neutral — neutral, 2026-09-24: `Watch command exited 0 (${command}) — see watch's own report for findings` / `Watch command FAILED (exit ${exit_code})` (`bus/profile.go`); the rule — a chain message states only what its trigger establishes — is stated once on `DefaultConfig`
- [x] Stop delivering pane scrapes in a `response` body; mark them as non-answers and do not complete the task as answered — `checkIdleTaskCompletionAt` phase 2 now sends `event:no-answer` from `daemon`, no reply-to, payload opening `[daemon — NOT A RESPONSE: …]`, and calls `TimeoutTask`, not `CompleteTask`; `responseAnswers` requires `Type response`, so no `--wait` takes it; a late real reply still completes the task (`TestIdleRescueIsNeverAResponse`)
- [x] Exclude suppressed self-addressed startup replies from loop detection, or stop printing the reply affordance for them — exclusion, 2026-09-24: `DetectMessageLoop` (`bus/guard.go`) drops `From == To` rows before both detectors; the reported `edit ↔ edit startup ×4` shape no longer alerts, four `edit → test` requests still do (`TestDetectMessageLoop_DelegationIsNotPingPong`)
- [x] Verify no other chain action asserts a finding it cannot establish (audit `bus/profile.go` messages) — only the two watch notices did; `TestChainNoticesAssertNoFindings` (`bus/profile_claims_test.go`) now guards every event-type default notice against healthy / detected / look good / no errors / verified, with the negative control that a *request* asking for a finding ("verify … healthy") is an instruction, not a claim
- [x] **Review node verdict parsing (`graph_exec.go` `reviewFindingsOutcome`) — closed 2026-09-24 10:52** (review `/tmp/muxcode-review-1790261523.txt`: a complete bounded-count summary anchored to the first non-blank reply line; quoted earlier summaries, fenced examples, incomplete counts and narrative-first replies are held; `TestReviewFindingsOutcome`, `TestExecReviewFindingsRouteToFix`). Original: the mechanism behind the Phase 2 evidence: the node read `outcome=success` off replies carrying must-fix. Review must-fix (`/tmp/muxcode-review-1790260651.txt`): the parser accepts any must-fix count anywhere and defaults a missing should-fix count to zero, so "0 must-fix found so far; review incomplete" passes as success. Parse one complete explicit summary line, require both gating counts, reject malformed or overflowing numbers, hold incomplete or quoted-only counts; negative controls for a missing should-fix count and an unfinished reply

#### Phase 5 findings — 2026-09-24

Landed in run `1790262549-spec-to-pr-00360289`: build and test green, review
`/tmp/muxcode-review-1790264685.txt` 0/0/0. Worker report: `/tmp/mux182-phase5-report.md`.
Boundaries drawn:

- **The scrape road is untouched where the pane *is* the evidence.** Only the hook-road idle rescue
  changed; `checkNonHookTasks` still reads the pane for providers whose pane is the completion
  signal (`PaneIsEvidence`).
- **A no-answer notice cannot be mistaken for an answer by construction, not by wording alone**: it
  is `Type event`, correlated to nothing, so `responseAnswers` rejects it and nothing marks the
  request responded — the wording is for the human reading it.
- **The watch chain's real defect stays with MUX-177**: it still fires on every successful watch
  Bash call for want of a `command_match` condition; Phase 5 made the notice honest, not selective.
- The watch *failure* notice was equally unfounded ("detected errors" on an expired SSO session's
  non-zero exit) and was neutralised in the same change.
- **Defect 4a is a category, not one bug** (Decision 3): the audit found exactly two offending
  notices, both watch, and the test now holds the whole set to the rule.

### Phase 6: Integration test

- [x] Create `scripts/test-cancel-provenance.sh` (hermetic; scratch bus, tmux session and daemon) — 2026-09-24: scratch `BUS_SESSION`, bus dir, `HOME`, config, lifecycle log and repo; a real `muxcode watch` daemon, real tmux windows, a stub agent that prints `❯` and writes a heartbeat line per second; no AI CLI launched
- [x] Test: a run with a live spawn is cancelled → **the spawn is dead**, verified by process/window absence, not by run state — section 1: window gone, stub pid and pane pids dead, entry `stopped`, `graph-cancel-spawn-stopped` row
- [x] Test: a spawn that cannot be stopped → cancel **refuses** to report success and names the survivor — section 2: a decoy window sharing the worker's name makes kill-by-name ambiguous; the cancel exits non-zero, names the role and `muxcode spawn stop <id>`, leaves the run `canceling`, and the survivor is verified **alive** (the refusal is truthful); decoy removed, the re-run cancel completes and the survivor is dead
- [x] **Negative control:** a run with no spawns cancels cleanly and reports success — section 4, an agent-created no-spawn run cancelled by edit
- [x] Test: a user-launched run renders as user-launched in all four surfaces; **negative control:** an agent-launched run renders autonomous in all four — section 3: the user's run is created through `tmux run-shell` so `created_by: user` comes from **real ancestry**, not a claimed `AGENT_ROLE`; `run.json` `provenance`, `graph status <id>`, the status list and `graph-run-created` all read "the user, by hand" and never "autonomous"; the `auto` run reads "auto (autonomous)" and never "the user" in all four
- [x] Test: an agent cancelling a human-created run is refused without approval; an agent-created run is cancelled freely — section 4: edit's cancel refused ("let the user cancel it"), run still running, `graph-cancel-refused` sourced edit; the user's cancel succeeds, `graph-run-canceled` "canceled by user"
- [x] Test: a cancelled run performs no file mutation after cancel returns — section 1: the worker's heartbeat file grew before the cancel and froze after it, with a live-observable control (a vacuous "0 beats unchanged" pass was found and guarded in iteration 1)
- [x] Test: the watch notification does not contain a health claim the chain did not establish — section 5: a `PostToolUse` payload through `hook bash` as watch; exit 0 → "exited 0" with no "healthy", exit 1 → "FAILED (exit 1)" with no "detected errors"
- [x] Test: a pane scrape never arrives as `Type: response` — section 6: a stub-backed idle build window, a tracked request from edit → one `event:no-answer` from `daemon` marked NOT A RESPONSE, no `Type: response` from build, task timed-out
- [x] Coverage floor keeps a skipped section from reporting green — floor 56, the arithmetic of all sections; the reviewer counted the assertions preceding it and they match
- [x] Run the script and verify all checks pass — run agent task `1790265785-spawn-4d7bf287-3089eb85` (store row `completed`, response `1790265883-run-64502422`), log `/tmp/test-cancel-provenance-3.log`: **57 passed, 0 failed, exit 0**, floor met; two script-only iterations before it (a fixture agent definition the scratch `HOME` lacked — isolation confirmed; two vacuous checks guarded)

#### Phase 6 findings — 2026-09-24

Landed in run `1790262549-spec-to-pr-00360289` (implement node 888 s — three script iterations
through the run agent): build and test green, review `/tmp/muxcode-review-1790265995.txt` 0/0/0.
Worker report: `/tmp/mux182-phase6-report.md`. What the script established beyond the boxes:

- **The user identity in the test is real, not claimed.** `BusActorVerified` overrules a missing
  or `user` `AGENT_ROLE` by walking process ancestry for an agent runtime, so a script an agent
  runs cannot become the user by unsetting a variable; `as_user` therefore runs through
  `tmux run-shell`, whose server daemonized away from whoever started it. If `ps` cannot be read
  there the creator resolves `unknown` and section 3 fails loudly rather than passing on a guess.
- **Isolation held**: the scratch `HOME` had no agent definitions, so `agent launch` refused with
  "resolved at no tier" instead of falling back to the real `HOME` — found as a failure in
  iteration 2 and answered with a minimal fixture definition, not a fallback.
- **Two checks passed vacuously in iteration 1** (an empty pid reading as "dead", zero beats as
  "unchanged") and were guarded — the pass count is not the coverage; the discriminating assertion
  has to be reachable.
- The un-stoppable worker is manufactured honestly: a decoy window sharing the worker's name makes
  tmux's kill-by-name ambiguous, so the survivor the refusal names is genuinely alive.
- Section 6 waits up to ~150 s for the idle rescue's two grace periods, with the stall, stuck-reload,
  permission-block and force-respond watchdogs disabled for that section only; the whole script runs
  about 2.5 min against the **installed** binary (run `./build.sh` first).

## Open decisions

### Decision 1 — cancel semantics when a spawn will not die

Fail closed (refuse to report cancelled, leaving the run in a visibly wrong state until resolved) or
report cancelled with a loud survivor list? **Recommendation: fail closed** — the incident's harm came
precisely from a run that *said* cancelled while working.
Phase 1 findings concur, and add the second half: a dead worker is necessary, not sufficient — its
already-delegated requests must be expired too (Q4.1), or the run keeps acting through other agents.

**Resolved: fail closed — implemented in Phase 2, closed 2026-09-24 after three reviews.** A
survivor leaves the run `canceling` and its node `running`, and the cancel returns
`CancelIncompleteError` naming each survivor's `muxcode spawn stop <id>`, each failed cleanup step,
and the `graph cancel` to re-run; retry is refused until a cancel reports `canceled`. The reviews
found and closed three roads that still failed *open* — a spawn racing the cancel, a failed
registry or inbox read treated as "nothing to stop", and a best-effort task expiry — recorded as
the Phase 2 must-fix boxes.

### Decision 2 — scope of the cancel-authority gate

Only `graph cancel` and `spawn stop`, or every agent action that stops human-initiated work
(`TaskStop`, `proc kill`, reload of a busy agent)? The report covers the first; the class is wider.

**Held open after Phase 4 (2026-09-24).** Phase 4 gated `graph cancel` and `spawn stop` of a
run-tied worker only; a spawn tied to no run, `TaskStop`, `proc kill` and reloads are ungated. The
wider class is real but has no incident behind it yet; widening it is a separate decision, not a
Phase 4 gap.

### Decision 3 — is defect 4a one bug or a category?

`bus/profile.go` may hold other chain messages asserting outcomes they cannot establish. Fixing only
the watch banner leaves the pattern. Phase 5's audit step is scoped to find out.

**Resolved 2026-09-24 by the Phase 5 audit: a category, guarded as one.** Only the two watch
notices asserted findings, but the rule now lives in `DefaultConfig`'s doc comment and
`TestChainNoticesAssertNoFindings` holds every event-type default notice to it, so a future chain
cannot reintroduce the class.

### Decision 4 — relationship to MUX-148 and MUX-178

All three are **false or unverifiable completion signals**: MUX-148 a node claiming a success it never
earned, MUX-178 a spawn reporting success having ported nothing, this one a run reporting cancelled
while still working. Whether they share a fix or only a theme is not settled here.

## Out of scope

- **Whether the edit agent should have cancelled.** Its inference was wrong, but it was reading an
  ambiguous label; this spec is about the label and the machinery, not the judgement.
- **The AWS activity of the orphaned spawn.** What it did is evidence of blast radius, not a defect in
  muxcode. That it *could* act after cancel is the defect.
- **[MUX-141](../backlog/MUX-141-auto-agent-restart-relaunches-graph-runs.md)** — the auto agent relaunching
  runs is the behaviour edit wrongly believed it was seeing. Related, separately tracked.

## Time Tracking

| Branch | Active time | Last updated |
|--------|-------------|--------------|
| MUX-182-cancelled-run-keeps-working | 1h 53m | 2026-09-24 12:07 |

## Status

Complete — closed 2026-09-24 12:1x at 53/53 (acceptance criteria 12/12, six phases), moved
`drafts/` → `completed/` by the `spec-to-pr` run `1790262549`'s `close-spec` node. Decision 2's wider
cancel-authority scope is recorded open by choice, not as an unmet item. History below.

Started 2026-09-23 on the user's instruction; moved `backlog/` → `drafts/` at 0/44.
**Phase 1 complete 2026-09-23** (5/5, investigation only, no code changed; findings recorded under
Phase 1, two latent bugs added to Phase 2). The investigating run itself reproduced defect 2 live.
**Phase 2 complete 2026-09-24** (run `1790194224`, three review iterations, build and test green
each time, third review 0/0/0) — `graph cancel` stops the run's workers under a per-run lock,
retracts their delegations with checked expiry, propagates registry, inbox and task failures, and
fails closed on a surviving worker or any failed cleanup step; 6/6 steps, four review boxes and AC 1
ticked. AC 2 stays open for Phase 6's live measurement (a consumed request cannot be recalled).
**Phase 3 complete 2026-09-24 11:13** (landed in run `1790258935`, closed in `1790262549`; build and
test green each time, final review 0/0/0) — 4/4 steps, the TUI clamp should-fix and ACs 3, 4, 5, 6
ticked; a clipped `(auton…` was the ambiguity defect 1 is about, so AC 3 waited for the clamp. The
review node's verdict parser (a Phase 5 box) closed 10:52. Phase 3 committed as `4cf52a3`.
**Phase 4 complete 2026-09-24 11:31** (run `1790262549`, build and test green, review 0/0/0) — 4/4
steps and ACs 7, 8 ticked: only the user may stop a run the user launched (or one whose creator is
unestablished), agent runs stay freely cancellable, the actor is named on `graph-run-canceled`, and
the decision is taken under the run lock. Decision 2's wider scope stays open by choice. Phase 4
committed as `f8e52ba`.
**Phase 5 complete 2026-09-24 11:45** (run `1790262549`, build and test green, review 0/0/0) — 4/4
steps and ACs 9, 10, 11 ticked: watch notices state only an exit code (audited across every default
notice), the idle rescue is an `event:no-answer` that times the task out rather than a response that
completes it, and self-addressed rows are excluded from loop detection. Phase 5 committed as
`2b91f48`.
**Phase 6 complete 2026-09-24 12:07** (run `1790262549`, three script iterations, review 0/0/0) —
`scripts/test-cancel-provenance.sh` 57/0 through the run agent, all eleven steps and ACs 2 and 12
ticked. **All six phases complete, 53/53, acceptance criteria 12/12.** Close-out (Status `Complete`,
move to `completed/`, backlog row) follows through the run's `close-spec` node; Decision 2's wider
cancel-authority scope stays recorded as open by choice, not as a gap.
