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
launch-on-restore ([MUX-141](./MUX-141-auto-agent-restart-relaunches-graph-runs.md)'s shape), and
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

- [ ] `graph cancel` terminates in-flight spawn nodes, **or** refuses to report the run cancelled while naming exactly which spawns survived and how to stop them
- [ ] A cancelled run cannot mutate files or call an external API after the cancel returns
- [ ] Every provenance surface (`run.json`, `graph status`, `graph runs`, `graph-run-created`) distinguishes "launched by the user by hand" from "launched autonomously by `<agent>`", in wording an agent cannot misread as the other
- [ ] **Negative control: a genuinely agent-launched run is still labelled autonomous** — a fix that labels everything "user" is not a fix
- [ ] A spawn-originated bus message carries its originating run id, `created_by`, and current run state, so a recipient can verify whether a prompt traces back to a human
- [ ] An agent cannot assert human provenance it did not receive — plan writing "on a user request" is unsupported unless the prompt carried it
- [ ] `graph cancel` / `spawn stop` issued **by an agent** against a run whose `created_by` is a human requires explicit user approval; agent-launched runs stay freely cancellable
- [ ] `graph-run-canceled` records **who** cancelled, as `graph-gate-approved` records who approved
- [ ] The watch completion notification carries the agent's real summary, or is neutral (`"Watch completed — see result"`); it never asserts a finding the chain did not establish
- [ ] A daemon pane scrape is never delivered in a `response` body and never completes a task as though answered — it is marked unmistakably as a non-answer
- [ ] A suppressed self-addressed startup reply is excluded from loop detection, or the reply affordance is not printed for it
- [ ] `bash scripts/test-cancel-provenance.sh` passes

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/graph_exec.go` | `CancelGraphRun:233` (the missing `Running` case and `StopSpawn`), `runProvenance:164` (the wording to propagate) |
| `tools/muxcode/bus/graph_run.go` | `CreatedBy:197`, `"Started by: %s":773-774` — the ambiguous render |
| `tools/muxcode/bus/config.go` | `BusActorVerified:167`, `agentRuntimeAncestor` — does it recognise `auto`? |
| `tools/muxcode/bus/spawn.go` | `StopSpawn`, spawn lifecycle and worktree mode |
| `tools/muxcode/bus/commit_authority.go` | `CheckCommitAuthority*` — the pattern to mirror for a cancel-authority check |
| `tools/muxcode/bus/profile.go` | `:980` — the fixed watch banner |
| `tools/muxcode/daemon/daemon.go` | `:3441-3450` — pane scrape sent as a response and completing the task |
| `tools/muxcode/bus/inbox.go` | `isLoopingSelfSend:122`, `DetectMessageLoop` — defect 5 |

## Implementation

### Phase 1: Establish the boundary

- [ ] Confirm whether `agentRuntimeAncestor` recognises the `auto` agent's runtime, or can return `user` for an auto-launched run
- [ ] Enumerate every surface that renders run provenance and every consumer that reads it
- [ ] Determine what a `spawn` node's worker actually is (process, window, worktree) and what terminating it requires
- [ ] Establish whether `spawn stop` alone is sufficient to end an orphan, or whether the worktree also needs reclaiming
- [ ] Record findings here before choosing an approach

### Phase 2: Stop the orphan (defect 2)

- [ ] Add `GraphNodeRunning` handling to `CancelGraphRun`, terminating spawn-backed nodes via `StopSpawn`
- [ ] On a spawn that cannot be stopped, **fail closed**: do not report the run cancelled; name the surviving spawns and the exact command to stop them
- [ ] Emit a lifecycle event for every spawn terminated or survived by a cancel
- [ ] Unit tests, including the negative control: a run with no running spawns still cancels cleanly and reports success

### Phase 3: Make provenance readable (defect 1)

- [ ] Route `run.json`, `graph status`, `graph runs` and `graph-run-created` through `runProvenance`'s vocabulary
- [ ] **Negative control test:** an agent-launched run still renders autonomous
- [ ] Propagate originating run id, `created_by` and run state into every spawn-originated bus message
- [ ] Make an unprovenanced prompt legible as such, so a recipient cannot assert human provenance it never received

### Phase 4: Gate cancellation of human-launched runs (defect 3)

- [ ] Add a cancel-authority check mirroring `CheckCommitAuthority`: an agent cancelling a human-created run requires explicit approval
- [ ] Keep agent-launched runs freely cancellable — **negative control:** the autonomous path must not regress
- [ ] Record the actor in `graph-run-canceled`
- [ ] Confirm the check cannot be bypassed by the daemon→edit role normalization, as MUX-144 Phase 4 had to

### Phase 5: Fix the misleading channels (defects 4 and 5)

- [ ] Replace the fixed watch banner with the agent's real summary, or make it neutral
- [ ] Stop delivering pane scrapes in a `response` body; mark them as non-answers and do not complete the task as answered
- [ ] Exclude suppressed self-addressed startup replies from loop detection, or stop printing the reply affordance for them
- [ ] Verify no other chain action asserts a finding it cannot establish (audit `bus/profile.go` messages)

### Phase 6: Integration test

- [ ] Create `scripts/test-cancel-provenance.sh` (hermetic; scratch bus, tmux session and daemon)
- [ ] Test: a run with a live spawn is cancelled → **the spawn is dead**, verified by process/window absence, not by run state
- [ ] Test: a spawn that cannot be stopped → cancel **refuses** to report success and names the survivor
- [ ] **Negative control:** a run with no spawns cancels cleanly and reports success
- [ ] Test: a user-launched run renders as user-launched in all four surfaces; **negative control:** an agent-launched run renders autonomous in all four
- [ ] Test: an agent cancelling a human-created run is refused without approval; an agent-created run is cancelled freely
- [ ] Test: a cancelled run performs no file mutation after cancel returns
- [ ] Test: the watch notification does not contain a health claim the chain did not establish
- [ ] Test: a pane scrape never arrives as `Type: response`
- [ ] Coverage floor keeps a skipped section from reporting green
- [ ] Run the script and verify all checks pass

## Open decisions

### Decision 1 — cancel semantics when a spawn will not die

Fail closed (refuse to report cancelled, leaving the run in a visibly wrong state until resolved) or
report cancelled with a loud survivor list? **Recommendation: fail closed** — the incident's harm came
precisely from a run that *said* cancelled while working.

### Decision 2 — scope of the cancel-authority gate

Only `graph cancel` and `spawn stop`, or every agent action that stops human-initiated work
(`TaskStop`, `proc kill`, reload of a busy agent)? The report covers the first; the class is wider.

### Decision 3 — is defect 4a one bug or a category?

`bus/profile.go` may hold other chain messages asserting outcomes they cannot establish. Fixing only
the watch banner leaves the pattern. Phase 5's audit step is scoped to find out.

### Decision 4 — relationship to MUX-148 and MUX-178

All three are **false or unverifiable completion signals**: MUX-148 a node claiming a success it never
earned, MUX-178 a spawn reporting success having ported nothing, this one a run reporting cancelled
while still working. Whether they share a fix or only a theme is not settled here.

## Out of scope

- **Whether the edit agent should have cancelled.** Its inference was wrong, but it was reading an
  ambiguous label; this spec is about the label and the machinery, not the judgement.
- **The AWS activity of the orphaned spawn.** What it did is evidence of blast radius, not a defect in
  muxcode. That it *could* act after cancel is the defect.
- **[MUX-141](./MUX-141-auto-agent-restart-relaunches-graph-runs.md)** — the auto agent relaunching
  runs is the behaviour edit wrongly believed it was seeing. Related, separately tracked.

## Status

Backlog
