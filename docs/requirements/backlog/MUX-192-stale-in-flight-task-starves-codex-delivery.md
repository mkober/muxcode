# MUX-192: A Stale In-Flight Task Starves Every Wake to a Codex Agent

Twice on the morning of 2026-09-25 the codex **review** agent sat at an empty composer with a request
pending for minutes and was never woken. Both times `muxcode deliver review --force` woke it at once.
The daemon saw the gap (`delivery-gap`, then `delivery-gap-skip` every 15 s, "will retry") and could
not close it, because its recovery is the same call that was refusing: `SendWakeUp(force=false)`
declines to inject while **any** in-flight task to the role is older than 5 s, and the task holding
the role had already been answered — under a different request id. `muxcode diagnose review` then
reported `active-with-stale-messages` on `IsAgentIdle: false`, a value `CodexProvider.IsIdle` returns
unconditionally, with an evidence line that describes the Claude road.

## Context

### Source and standard of evidence

Filed 2026-09-25 on the user's instruction relayed by edit, from edit's brief
`/tmp/review-idle-defect.md` (non-durable) — two first-hand incidents in session `muxcode`, with
`diagnose` output, the pane text and the `[wakeup] skipping` line quoted. **Every mechanism claim below
was verified by plan against the working tree at `e3f7e44` (branch `MUX-154-codex-status-line-closes-tracked-tasks`)
and the daemon lifecycle log.** The brief's three hypotheses are assessed individually; the first is
rejected on code.

### Observed

| Time | Event |
|---|---|
| 10:57:32 | daemon `delivery-gap`: review has 1 un-receipted message for 120 s |
| ~10:58 | `muxcode diagnose review` → `active-with-stale-messages` (critical): "IsAgentIdle: false (8-line and wide capture)"; 1 actionable, 1 unnotified, oldest 181 s; "Neither Notify() nor daemon checkIdleAgents delivers to non-idle agents". The pane at that moment: `• The inbox is empty; nothing is pending. / Worked for 1m 7s · 11:01 AM / › Ask Codex to do anything / GPT-6-Astra medium · ~/Repos/mkober/muxcode · Review new messages ⚠ 1 warning · f2 to view` — idle |
| ~10:58 | `muxcode deliver review --force` wakes review immediately |
| 11:06:05 | edit sends review a request; `muxcode send review … --force --track` prints `[wakeup] skipping review injection — in-flight task review:17903477 exists (278s old)` |
| 11:06:56 – 11:07:41 | daemon `delivery-gap-skip` ×4, one per 15 s poll: "review: recovery injection skipped, will retry: review: in-flight task 17903484 (319s… 364s old): wake-up injection skipped" |
| ~11:08 | `muxcode deliver review --force` wakes review immediately |

The blocking tasks were edit's own review requests — `1790347799-edit-dfae26e9` for the first
incident, `1790348497-edit-7dfa92fa` for the second — both **answered**: review had replied
`--reply-to` the build→test→review **chain's** request id (`1790347737-test-…`) for the same review,
so edit's tracked task never received a correlated response and stayed `in-flight`. `muxcode tasks`
showed it in flight minutes after the answer had arrived; it could only clear at the 600 s task
timeout.

### Mechanism — verified

Under delivery-ack (default on), `checkIdleAgents` returns before doing anything
(`daemon.go:2194-2196`); a listenerless provider is served by `checkInboxes → Notify` and the
receipt-gap backstop. Both roads end in the same function, and that function refuses:

| Step | Code | What it does |
|---|---|---|
| `Notify` | `bus/notify.go:603-606` | for a provider that does not self-poll, calls `notifySendKeys` **without** consulting `IsAgentIdle` ("don't gate on IsIdle (which returns false for OpenCode)") |
| `SendWakeUp(force=false)` | `bus/provider_codex.go:554-563` (mirror `provider_opencode.go:142`) | lists `TaskInFlight` tasks; if any has `To == role` and is older than **5 s**, prints `[wakeup] skipping … injection — in-flight task … exists` and returns `ErrInjectionSkipped`. No upper bound on age; no check whether the task's request has been answered |
| `checkPollHealth` recovery | `daemon/daemon.go:2128-2135` | for a non-self-polling provider calls `provider.SendWakeUp(session, role, false)` — the **same refusable call**; on `ErrInjectionSkipped` it clears `pollGapRecovered` so the next poll retries, logging `delivery-gap-skip` "will retry" each time. The retry is identical to the attempt, so it is refused identically until the task leaves `in-flight` |
| `deliver --force` | `bus/deliver.go:44` → `SendWakeUp(…, true)` | `force=true` bypasses the skip — which is why the manual recovery works and the automatic one cannot |
| `CodexProvider.IsIdle` | `bus/provider_codex.go:212-216` | `return false` — "the TUI has no stable prompt character that can be matched via pane capture" |
| `diagnose` | `bus/diagnose.go:~957-990` | emits `active-with-stale-messages` with evidence `"IsAgentIdle: false (8-line and wide capture)"` and `"Neither Notify() nor daemon checkIdleAgents delivers to non-idle agents"` |

So the chain of custody is: an answer correlated to the wrong request → a task that is complete in
fact and `in-flight` on disk → every wake to that role refused for up to 600 s → the backstop that
exists for exactly this state refused by the same rule → a diagnostic that names a probe the provider
does not implement and a delivery path the agent is not on.

### The brief's hypotheses

| # | Hypothesis | Verdict |
|---|---|---|
| 1 | Codex idle classification misreads this frame (the `⚠ 1 warning · f2 to view` footer, the `Review new messages` title, the placeholder) | **Rejected.** No frame classification runs on this road: `IsIdle` is a constant and `Notify` does not consult it for a listenerless provider. The footer and title are red herrings that `diagnose`'s wording pointed at |
| 2 | Daemon on v0.1.8 (`7b2e0b4`) vs installed `v0.1.8-3-ge3f7e44-dirty` | **Not the cause.** The refusing code is unchanged between them (`SendWakeUp`'s skip predates both). Noted; `muxcode upgrade-daemons` is the fix for the mismatch itself |
| 3 | A stale in-flight task whose answer correlated to a different request id blocks wakes until the 600 s timeout | **Confirmed** — by the `[wakeup] skipping` line, the four `delivery-gap-skip` rows naming task `17903484…` at 319–364 s, and the task records |

### Two defects, one incident

1. **Delivery** — `SendWakeUp`'s in-flight skip is MUX-171's rule ("never re-drive a working pane")
   applied to a proxy for *working* that is not one: an in-flight task says a request was sent, not
   that the agent is busy. It has no age ceiling, ignores whether the request was answered, and the
   backstop that should override it calls it unforced. The result is a role starved for up to ten
   minutes per stale task, with the daemon logging that it is retrying.
2. **Diagnosis** — `diagnose` reports a constant as a finding and describes the wrong road. The
   remediation it prints ("Agent may be genuinely busy — check the pane before forcing") sends the
   operator to look for a busy agent; the real blocker — the task id in the `[wakeup]` line — is not
   named anywhere in the report.

### Why the task was stale

Two requests asked review for the same review: the chain's (`test → review`, from the test hook) and
edit's tracked one. Review answered once, `--reply-to` the chain's id. The correlation half of this —
one answer for two requests — belongs with
[MUX-170](./MUX-170-graph-dispatch-adopts-foreign-in-flight-task.md) (a foreign in-flight task
adopted) and [MUX-189](./MUX-189-batch-delivery-correlates-only-the-last-requests-reply.md) (one reply
line per request), and this spec does **not** try to make the agent answer both. It makes a stale
task unable to starve the role, which is the property that has to hold whatever the agent does.

### Blast radius

Every codex and OpenCode agent (the only roads through `SendWakeUp`'s skip), whenever any request to
it stays `in-flight` past 5 s — a busy agent legitimately, or a stale task indefinitely. The review
role is where it bites: the review→plan chain (`verify-spec`) does not fire while review is starved,
and a `50-spec-to-pr` review node waits on the task timeout.

### Family

[MUX-171](../completed/MUX-171-stall-watchdog-redrive-kills-busy-claude-tool.md) — the rule this skip
over-applies (a working pane must not be re-driven; an idle one with a stale task is not working).
[MUX-123](./MUX-123-stall-watchdog-selective-misses.md) — the watchdog that fires routinely and misses
live stalls. [MUX-170](./MUX-170-graph-dispatch-adopts-foreign-in-flight-task.md),
[MUX-189](./MUX-189-batch-delivery-correlates-only-the-last-requests-reply.md) — correlation.
[MUX-154](../completed/MUX-154-codex-status-line-closes-tracked-tasks.md) — the opposite failure on the same road
(a task closed that should not have been; here one held that should have closed).

## Requirements

### Acceptance criteria

- [ ] A wake to a listenerless agent is **not** refused by an in-flight task whose request already has a correlated response in the log, or whose age exceeds the send grace — test: a codex review fixture with a stale in-flight task and a pending request → `Notify` injects; **negative control:** a task under 5 s old with the agent mid-turn still skips
- [ ] The receipt-gap backstop recovers a starved listenerless agent instead of repeating the refused call — either it wakes with `force=true` under the MUX-171 busy gate (`AgentIsWorking` false), or it expires/answers the stale task first — test: the 10:57 shape (idle pane, stale answered task, pending request) → delivered within one backstop interval, `delivery-gap-skip` not logged more than once
- [ ] A working codex pane is still never injected into — **negative control:** a mid-turn codex frame (`• Working (…) esc to interrupt`) with a pending request → no injection, whichever road
- [ ] A tracked task whose request has been answered under another request id for the same `(from, to, action)` does not stay `in-flight` to the timeout — it is completed or marked answered — test: chain request and edit request to review, one reply to the chain id → edit's task leaves `in-flight`; **negative control:** a reply to an unrelated action leaves it in flight
- [ ] `diagnose` for a provider whose `IsIdle` is a constant does not report `active-with-stale-messages` on it; it names the road (`listenerless`, `SendWakeUp`) and the blocker it can see — the in-flight task id and age — and its remediation names `deliver --force` **and** the task — test: fixture report from the 10:58 state → finding names task `1790347799-edit-dfae26e9`, no "IsAgentIdle: false" evidence line
- [ ] Every skip is a lifecycle row naming the task: `[wakeup] skipping` to stderr is not evidence anyone reads — test: a skipped wake writes `wake-skipped` (or the existing `delivery-gap-skip`) with role, task id and age
- [ ] `bash scripts/test-codex-idle-delivery.sh` passes

### Technical approach

Three small changes and one diagnostic rewrite, in this order:

1. **Bound the skip** (`SendWakeUp`, both providers): an in-flight task blocks a wake only while it is
   younger than the send grace **and** unanswered (`FindResponseSince(session, task.To, task.From,
   task.SentAt)` with the same action, or `MarkResponded` state). Extract the predicate to one
   function both providers call, so they cannot drift.
2. **Make the backstop a backstop** (`checkPollHealth`): the recovery road for a listenerless provider
   is `ForceDeliver(session, role, true)` gated by `AgentIsWorking` — the same busy gate MUX-171 gave
   `ForceDeliver` — not an unforced `SendWakeUp`. A busy pane is a skip; a stale task is not.
3. **Close the stale task** (`checkTrackedTasks`): a task whose `(from → to, action)` has a later
   response from `to` naming a *different* request id of the same action is marked answered-elsewhere
   (lifecycle `task-answered-elsewhere`) rather than left to the 600 s timeout. Deliberately narrow —
   same role pair and action — so it cannot adopt a foreign answer (MUX-170's hole).
4. **`diagnose`**: for a provider whose `IsIdle` is constant, replace the idle-probe finding with a
   `wake-blocked-by-task` finding built from `ListTasks(TaskInFlight)` for the role; keep
   `active-with-stale-messages` for providers that implement idle detection.

### Key files

| File | Role |
|---|---|
| `tools/muxcode/bus/provider_codex.go:550-564` | `SendWakeUp` — the in-flight skip (5 s, unbounded, unanswered-blind) |
| `tools/muxcode/bus/provider_opencode.go:~135-150` | the OpenCode mirror of the same skip |
| `tools/muxcode/bus/provider_codex.go:212-216` | `IsIdle` — constant `false` |
| `tools/muxcode/bus/notify.go:599-606` | `Notify` — the listenerless road that does not gate on `IsIdle` |
| `tools/muxcode/daemon/daemon.go:2046-2140` | `checkPollHealth` — the backstop calling `SendWakeUp(false)` and logging `delivery-gap-skip` |
| `tools/muxcode/daemon/daemon.go:2735` | `checkTrackedTasks` — where a stale answered task could be closed |
| `tools/muxcode/bus/deliver.go:44` | `ForceDeliver` — the road that works, and its MUX-171 busy gate |
| `tools/muxcode/bus/timetrack.go:237` | `AgentIsWorking` — the real "busy" predicate |
| `tools/muxcode/bus/diagnose.go:~957-995` | `active-with-stale-messages` |
| `tools/muxcode/bus/dedup.go:291` | `FindResponseSince` — the answered-elsewhere lookup |

## Implementation

### Phase 1: Pin

- [ ] Characterization test: codex review fixture, one in-flight task 300 s old, one pending request → `SendWakeUp(false)` returns `ErrInjectionSkipped` (today's behaviour, named as the defect); the same fixture with the task's request answered in the log → still skipped today (the pin to invert)
- [ ] Reconstruct the 11:06:56 `delivery-gap-skip` sequence in a daemon test: `checkPollHealth` over the fixture retries and is refused on every poll; assert the row count grows per poll (today) — the pin to invert
- [ ] `diagnose` fixture from the 10:58 report: assert the finding today carries `IsAgentIdle: false` for a codex role — the pin to invert

### Phase 2: Bound the skip and fix the backstop

- [ ] One shared predicate `wakeBlockedByTask(session, role)` — younger than the grace **and** unanswered — used by both providers; invert the Phase 1 pins
- [ ] `checkPollHealth` listenerless recovery → `ForceDeliver(…, true)` under `AgentIsWorking`; a busy pane logs a skip once, not per poll
- [ ] Negative controls: a fresh unanswered task still skips; a mid-turn codex pane is never injected into on either road
- [ ] Lifecycle row for every skip, naming role, task id, age

### Phase 3: Close the stale task, fix the diagnosis

- [ ] `checkTrackedTasks`: answered-elsewhere detection, same `(from, to, action)` only; `task-answered-elsewhere` row; negative control with a different action
- [ ] `diagnose`: `wake-blocked-by-task` finding for constant-`IsIdle` providers, naming the task; `active-with-stale-messages` no longer emitted for them; remediation names the task and `deliver --force`
- [ ] `docs/architecture.md` delivery-tracking section and `docs/agent-bus.md` `diagnose` failure modes updated

### Phase 4: Integration test

- [ ] Create `scripts/test-codex-idle-delivery.sh` (hermetic: scratch bus + tmux + real scratch daemon, a static non-echoing codex fixture pane showing the 10:58 idle frame — same harness shape as `test-status-line-task-close.sh`)
- [ ] Test: stale answered in-flight task + pending request → delivered within one backstop interval; at most one `delivery-gap-skip` row
- [ ] Test: fresh unanswered task (< grace) → wake skipped, one row (negative control)
- [ ] Test: mid-turn codex fixture pane → no injection on either road (negative control)
- [ ] Test: two requests, one reply to the other id → the tracked task leaves `in-flight` before timeout
- [ ] Test: `muxcode diagnose <role> --json` on the starved fixture names the task, carries no `IsAgentIdle` evidence
- [ ] Coverage floor keeps a skipped section from reporting green
- [ ] Run the script and record the pass/fail counts here

## Open decisions

### Decision 1 — where does "answered elsewhere" live?

Closing edit's task when the chain's request was answered is a correlation decision that MUX-170 and
MUX-189 also touch. Narrow scope here (same `(from, to, action)`) keeps it safe; the alternative is to
leave the task alone and rely solely on the bounded skip + forced backstop. The bounded skip is
sufficient to stop the starvation; the task close is what makes `muxcode tasks` truthful.

### Decision 2 — should `IsIdle` for codex stop being a constant?

`DetectTaskCompletion` and `PaneShowsRecoverableIdle` already read a codex composer (`›`) for other
purposes. An honest `IsIdle` would let `diagnose` keep one finding shape across providers. Out of scope
here unless Phase 3 finds the diagnostic cannot be made truthful without it.

## Out of scope

- Making review answer both the chain's request and edit's — the correlation family (MUX-170, MUX-189).
- The daemon/binary version mismatch (hypothesis 2) — `upgrade-daemons` exists for it.
- Any change to the MUX-171 busy gate itself; this spec applies it, it does not weaken it.

## Status

Backlog

Filed 2026-09-25 on the user's instruction relayed by edit, from two first-hand incidents that morning
(10:57, ~11:06) on the codex review agent. Mechanism verified by plan against `e3f7e44` and the
lifecycle log: the brief's first hypothesis (a misread idle frame) is rejected — codex has no frame
classification on this road; its third (a stale in-flight task blocking wakes to the timeout) is
confirmed and is the defect, with `diagnose`'s constant-false idle evidence as a second. Not started.
