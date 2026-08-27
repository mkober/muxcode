# Idle-Task Rescue Closes Tasks That Are Still Running

The daemon treats "agent sitting at the `❯` prompt" as "agent finished without answering". An agent
that launched background work — a long script, a `run_in_background` command — is at the prompt
precisely *because* the work is still going. The rescue path then scrapes its pane, sends the
scrapings as the task's response, and closes the task. The real reply arrives later and is dropped
as a duplicate.

Reproduced 3/3 on 2026-08-27 (09:18, 09:34, 09:47) with `run`→`edit` tasks backed by multi-minute
scripts.

## Context

### The mechanism, read from the code

`checkIdleTaskCompletion()` — `daemon/daemon.go:2912`:

| Step | Behaviour |
|------|-----------|
| Scope | **Hook providers only** (`if !provider.SupportsHooks() { continue }`) — non-hook roles are handled separately by `checkNonHookTasks()` |
| Trigger | An in-flight task older than 10 s whose target reports `bus.IsAgentIdle()` — i.e. sitting at the `❯` prompt |
| Grace | Tracked in `idleTaskFirstSeen`; **Phase 1** re-queues the request, clears notified ids, and re-notifies — a genuine second chance |
| Phase 2 | Captures **50 pane lines**, sends them to the original requester as a `response` message, then calls `CompleteTask` (`:3034`) and logs an `idle-task-rescue` lifecycle warning |

**The faulty premise is one line:** `IsAgentIdle()` answers "is this pane at a prompt", and the code
reads that as "this agent is done". Those differ exactly when the agent has delegated work to a
background process — which is the **`run` agent's normal mode of operation**, and the reason it is
the role that reproduces this.

### What actually goes wrong

1. **The task closes on scraped text.** `CompleteTask` fires with the pane capture as the recorded
   result, so a task whose work is still running is marked finished.
2. **The real reply is then suppressed.** When the script completes and the agent answers for real,
   the task is already closed and the response is dropped as a duplicate — the *correct* answer is
   the one discarded.
3. **The requester acts on pane chrome.** 50 lines of terminal scrollback stand in for a result.

### What the current design already gets right

Worth preserving in any fix — these are not the defect:

- The synthetic response is **honestly labelled**: `[daemon: <role> went idle without responding
  (retried once) — pane content follows]`. It does not claim success, which is the
  [MUX-003](../completed/MUX-003-echo-as-result.md) lesson holding.
- Console-history logging routes through `NewBusResponseEntry`, which records scraped output as
  **unverified activity** and drops plain TUI chrome.
- Phase 1's re-queue-and-retry is a real second chance, not a formality.
- A dead pane is excluded elsewhere, so this is not the "login banner recorded as a successful build"
  failure returning.

The bug is not that the daemon reports pane content. It is that it **closes the task** on it.

### Live reproduction, 2026-08-27 — a graph run reported `[complete]` on a node that never ran

Caught first-hand hours after this spec was filed, and it is worse than the reported case because
the false completion propagated into a **graph run's durable state**.

Run `1787845136-review-spec-docs-ae1b86f1` (`review → spec → docs`). The `docs` node dispatched
`update-docs` to plan. Plan was mid-turn — at the `❯` prompt, composing a reply — and the rescue
fired after **31 seconds**:

```
warn  daemon  idle-task-rescue  plan idle with unresponded task update-docs
                                from daemon (idle 31s, retry exhausted)
```

The node's persisted output (`graphs/<run>/nodes/docs.json`) is:

```
[daemon: plan went idle without responding (retried once) — pane content follows]
muxcode agent launch plan

The default interactive shell is now zsh.
To update your account to use zsh, please run `chsh -s /bin/zsh`.
For more details, please visit https://support.apple.com/kb/HT208050.
…
```

A **macOS zsh login banner** followed by the agent's own scrollback, stored as the result of a
documentation node. `graph status` then reports the run `[complete]`, all three nodes `done`.

Three things this adds beyond the original report:

1. **The banner is the exact failure `checkNonHookTasks` already warns about.** Its comment cites "a
   macOS `run chsh -s /bin/zsh` login banner … once recorded as a successful build" and guards the
   dead-pane case. `checkIdleTaskCompletion` has no equivalent guard, so the same banner reappeared
   through the other door.
2. **31 seconds is not a wedge.** The grace period cannot distinguish "wedged" from "composing a
   reply", which is why a longer timer is not the fix — an agent thinking for 45 s would fail
   identically.
3. **The damage outlives the message.** A bad bus response is transient; a bad *node output* is
   written to the run store and reported as `[complete]` forever. Any consumer of graph history —
   `graph status`, the run browser's results column, a future post-mortem — now reads a login banner
   as the outcome of a docs task.

The last point raises the priority of the advisory-vs-authoritative choice in
[Phase 3](#phase-3-stop-losing-the-real-answer): a synthetic response that does not close the task
would have left this node honestly `running` instead of falsely `done`.

## Requirements

### Acceptance criteria

- [ ] An agent at the prompt with **live background work** is not treated as having finished — the rescue path does not fire while a process it owns is running
- [ ] An agent that is simply **composing a reply** is not rescued after 31 s — the live reproduction shows the grace period cannot separate "wedged" from "still working"
- [ ] A graph node is **never marked `done` from a synthetic response** — a run must not report `[complete]` for a node whose work never happened
- [ ] The dead-pane/login-banner guard that `checkNonHookTasks` already carries is applied to `checkIdleTaskCompletion` too, so the same banner cannot enter through the other door
- [ ] A `run`→`edit` task backed by a multi-minute script survives to its real reply, reproducing the 3/3 case
- [ ] The real reply is **never** suppressed as a duplicate of a synthetic one — if both exist, the agent's own answer wins
- [ ] A genuinely wedged agent (idle, no background work, no reply) is still rescued — the fix must not disable the safety net
- [ ] **Negative control:** a test proves the rescue still fires for a wedged agent with no live procs, so a fix that simply never rescues cannot pass
- [ ] The synthetic response remains honestly labelled and is never recorded as a verified result
- [ ] Lifecycle events distinguish "rescued a wedged agent" from "declined to rescue, work still running"
- [ ] `scripts/test-idle-task-rescue.sh` passes

### Technical approach

- **Consult the work signal that already exists.** `RefreshProcStatus()` (`bus/proc.go:298`) and
  `CheckProcAlive()` (`:288`) already expose running background procs by owner, and
  `PreCommitCheck()` (`bus/inspect.go:185`) already consumes exactly that to block commits while
  procs run. The rescue path should ask the same question before concluding an agent is finished.
- **Prefer advisory over authoritative.** The cheapest correct fix may be to keep sending the pane
  content but **stop calling `CompleteTask`** — leave the task in-flight so the real reply still
  lands and correlates. That converts a wrong answer into a progress note.
- **Do not simply lengthen the grace period.** A longer timer moves the failure rather than removing
  it: any script slower than the new threshold fails identically. The grace period is a heuristic
  standing in for a signal that is actually available.
- **Watch the interaction with dedup.** Whatever lands, the suppression half needs its own check —
  a task closed by mistake must not make the correct answer unreachable.

### Key files

| File | Change |
|------|--------|
| `daemon/daemon.go:2912` | `checkIdleTaskCompletion()` — consult live background work before Phase 2; reconsider `CompleteTask` at `:3034` |
| `bus/proc.go` | Reuse `RefreshProcStatus()` / `CheckProcAlive()` as the liveness signal |
| `bus/spawn.go` | Same question for spawns, if they can back a task |
| `bus/dedup.go` | Ensure a real reply is not dropped against a synthetic one |
| `daemon/daemon_test.go` | Rescue-fires and rescue-declines cases, including the negative control |
| `scripts/test-idle-task-rescue.sh` | New — integration test |
| `docs/architecture.md` | Document the corrected rescue contract |

## Implementation

### Phase 1: Reproduce and pin

- [ ] Reproduce with a `run`→`edit` task backed by a multi-minute background script
- [ ] Confirm the sequence from lifecycle events: `idle-task-rescue` fires, `CompleteTask` closes the task, the later real reply is dropped
- [ ] Unit test capturing today's behaviour before changing it

### Phase 2: Consult live background work

- [ ] Query running procs/spawns owned by the target role before Phase 2 fires
- [ ] Decline the rescue while work is live, emitting a distinct lifecycle event
- [ ] Test: idle agent **with** a live proc is not rescued
- [ ] **Negative control:** idle agent with **no** live proc is still rescued

### Phase 3: Stop losing the real answer

- [ ] Decide advisory-vs-authoritative for the synthetic response, and record the reasoning
- [ ] Ensure the agent's own reply always wins over a synthetic one
- [ ] Test: a real reply arriving after a synthetic one is delivered, not suppressed

### Phase 4: Integration test

- [ ] Create `scripts/test-idle-task-rescue.sh` — hermetic: scratch `BUS_SESSION`, scratch tmux session, scratch daemon
- [ ] A task backed by a live background process is not force-answered while it runs
- [ ] Its real reply lands and correlates to the original task
- [ ] A wedged agent with no live work is still rescued (**negative control**)
- [ ] The synthetic response is labelled and not recorded as a verified result
- [ ] Coverage floor so a skipped or short-circuited run cannot report green
- [ ] Run the script and confirm all checks pass

## Status

Draft
