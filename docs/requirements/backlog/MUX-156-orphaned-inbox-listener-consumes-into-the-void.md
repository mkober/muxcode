# An Orphaned Inbox Listener Consumes Messages Into the Void

A `muxcode inbox --poll --loop` whose parent has died keeps running, keeps winning the race for its
role's inbox, and keeps writing receipts — while printing every message it consumes to a stdout that
nobody reads. The bus records the message as **acked by the role**; the agent never sees it. The
daemon's task-stall backstop eventually re-drives *requests*, minutes late; a response or event with
no task behind it is simply gone.

Observed 2026-09-08: an edit listener orphaned at 14:44:52 (PPID 1) ran for 68 minutes. Inside that
window a `run → edit` request was acked nine seconds after it was sent and answered three and a half
minutes later, only after two daemon re-drives.

Tracking: _(no GitHub issue yet)_

## Context

### Observed (2026-09-08, session `muxcode`)

| | |
|---|---|
| Orphan | `muxcode inbox --poll --loop`, pid 8068, `PPID=1` (launchd), running as **edit**, started 14:44:52, killed by edit at ~15:50 (edit's census; reported) |
| Message | `1788896523-run-c983741d` — `run → edit`, request `test153-followup`, sent 15:42:03 |
| Receipt | `acked_at` 15:42:12, `acked_by: edit`, `receipt_kind: ack` — nine seconds after send (**verified**, `delivery/…status`) |
| What edit saw | Nothing, until the daemon intervened: `task-stall-redrive run→edit:test153-followup redrive 1/2` at 15:44:30, `force-redrive edit: 1 in-flight task(s)` + `redrive 2/2` at 15:45:32 (**verified**, lifecycle log) |
| Response | `1788896739-edit-d2393bb9` at 15:45:39 — 3m36s after send, 7s after the second re-drive |
| Attribution | The receipt names the *role*, not the consuming process, so the live and orphaned edit listeners are indistinguishable in the store. The stall-then-redrive shape is **consistent with** the orphan winning the race; it cannot be proven from the store, which is itself a finding |

Plan's own inbox was unaffected: all thirteen messages addressed to plan during the orphan's lifetime
were processed by plan's live listener (checked against `log.jsonl`). The orphan could only eat what
was addressed to the role it ran as.

### Observed again (2026-09-10, session `is-advising-gateway`) — now with an inside witness

A second occurrence, on a **live AWS deploy**, that closes the attribution gap the 2026-09-08 entry
had to leave open. This time the consuming agent reported the race from the inside.

| | |
|---|---|
| Orphan census | **31** live `muxcode inbox --poll --loop` processes at 09:46 across two sessions (`ps aux`, measured). The deploy agent's own count: "9+ … going back to 8:29AM, apparently one per turn from the Stop-hook auto-relaunch never reaping the prior instance" — 08:29:10 is this session's creation time |
| Inside witness | deploy tried to start its listener and was refused: **"Another inbox listener claimed deploy concurrently — exiting rather than double-consuming"**. The live agent lost the claim to an orphan and *said so* — the 09-08 entry could only infer this from timing |
| Message | `1789047789-edit-f7f0cce8` — `edit → deploy`, request `deploy`, "TASK q6-deploy-retry: **USER AUTHORIZED DEPLOY**", sent 09:43:09 |
| What deploy saw | Nothing. At 09:45 deploy was **idle at `❯`**, `Inbox: 0 message(s) (0 actionable)`, `Unnotified: 0`, `Polling: active` — consumed and acked, never acted on |
| Recovery blocked | edit's `muxcode deliver --force` returned **"no pending messages"** — the row was already consumed, so force-delivery had nothing to re-drive. The documented recovery path is a no-op against this failure |
| Daemon backstop | `force-redrive deploy` at 09:34:05, 09:40:20, 09:45:58; `task-stall-redrive edit→deploy:deploy redrive 1/2` at 09:40:20 and 09:45:58 (lifecycle, verified) |
| Consequence | **The deploy silently did not happen.** edit verified against AWS: all three producer Lambdas still `LastModified 2026-09-09T20:26` with unchanged `CodeSha256`. A user-authorised production-path deploy was acked by the bus and dropped on the floor |
| Rate | edit's count: "the second dropped delivery on this agent in twenty minutes" |

The 09-08 entry noted that the receipt "names the *role*, not the consuming process, so the live and
orphaned … listeners are indistinguishable in the store." That still holds — but the concurrent-claim
refusal proves an orphan was holding the claim, so the inference is no longer only from timing. It
also names the **source** the 09-08 entry did not: the Stop-hook auto-relaunch starts a listener per
turn and reaps none, so the orphan population grows monotonically with turn count. That makes this a
function of session age, not luck.

**Escalation.** This is no longer a latency defect. On 2026-09-08 the cost was a request answered
3m36s late. On 2026-09-10 the cost was a user-authorised `cdk deploy` that the bus recorded as
delivered and that never ran — caught only because the requesting agent distrusted the silence and
checked Lambda metadata directly. A dropped *response* or *event*, which has no task behind it and no
stall backstop, would leave no trace at all.

### Mechanism

| Fact | How established |
|------|-----------------|
| The listener is a background Bash command; the harness terminates it at turn end (`exit code 143`) and the `Stop` hook relaunches one when `IsPolling`/`IsWaiting` say none is alive | `cmd/hook.go:426-471` (**verified**); the 143 exits are visible in every agent pane |
| A listener whose parent dies without the SIGTERM reaching the child reparents to launchd and keeps polling — the process has no check that its parent is still alive | pid 8068 `PPID=1` for 68 minutes (edit's census, reported); no `getppid` check in the poll loop (**to pin in Phase 1**) |
| Consuming writes an `acked` receipt attributed to the role | `delivery/1788896523-run-c983741d.status` (**verified**) |
| `IsPolling` reads **one pid** from `PollingMarkerPath` and validates only that it is alive | `bus/notify.go:786-800` (**verified**) — the marker cannot represent two pollers; whichever wrote last is "the" listener, and an alive orphan can satisfy the hook's liveness question |
| The only recovery is `task-stall-redrive`, which fires for a **tracked request** whose receipt exists but whose task never completes | lifecycle rows above (**verified**); responses and events create no task, so nothing re-drives them |

### Why it matters

The receipt model (MUX-050) replaced "did the pane look idle?" with "did the agent's runtime read it?"
— and the orphan *is* the agent's runtime, as far as the store can tell. It produces the one thing
the model cannot detect: a positive receipt for a message no one processed. Requests survive by the
stall backstop, late; untracked responses and events do not survive at all. And because the orphan
holds the role's identity, it can satisfy the `Stop` hook's "is a listener alive?" question, so the
agent's own relaunch may be suppressed exactly when it is needed.

### Relationship

| Spec | Relationship |
|------|--------------|
| [`MUX-147`](./MUX-147-process-leak-and-memory-footprint.md) | Orphaned **harness** processes after daemon restarts — the same reparent-to-launchd shape, a different process, no message loss |
| [`MUX-155`](./MUX-155-send-dedup-keys-on-target-not-sender.md) | Filed the same afternoon for plan's dropped notifies; edit's late `test153-followup` is **this** defect, not that one — the two were disentangled by checking each inbox |
| [`MUX-050`](../completed/MUX-050-delivery-acknowledgement.md) | The receipt model this defect defeats from inside |

### Third occurrence, 2026-09-11 — still firing in `is-advising-gateway`, a day later

Found incidentally by plan (session `muxcode`) while diagnosing two of its own listener kills, which
turned out to be ordinary turn-boundary reaping and unrelated.

| Field | Value |
|-------|-------|
| PID | 21146 |
| PPID | **1** (launchd) |
| Command | `muxcode inbox --poll --loop` |
| `AGENT_ROLE` | `serve` |
| `BUS_SESSION` | **`is-advising-gateway`** |
| Started | 2026-09-11 09:20:09, **~1 h 48 m** alive at observation |

The same session as the 2026-09-10 entry above, a day later, on a different role — so the condition is
**recurring in that session rather than a one-off**, and it survives across days. No attempt was made
to read what it had consumed: the receipt names the role, not the process, which is the attribution
gap this spec already records.

**Not killed, deliberately.** It belongs to another session, plan holds no process-management role,
and an agent killing a `PPID=1` process on ownership inferred from `ps` is precisely the mistake the
watch agent was corrected for on 2026-09-10 — it killed a listener that turned out to be the run
agent's own, already exited, on an unverified assumption. Ownership here *was* verified (the env
carries `AGENT_ROLE`/`BUS_SESSION`), but verification establishes whose it is, not the authority to
kill it. Reported instead.

**Side finding worth its own attention: `ps eww` leaks credentials into agent output.** Identifying
the orphan's role required reading its environment, and a wholesale `ps eww` dump printed
`MUXCODE_OPENCODE_API_KEY` in clear text into the agent's conversation and thence its history file.
`ScrubPIIWithNotice` covers `api`, `run` and `watch` output; a diagnostic run from any other role is
unscrubbed. The narrow form — `ps eww -p <pid> | tr ' ' '\n' | grep -E '^(AGENT_ROLE|BUS_SESSION)='` —
gets the same answer without the exposure and should be what any diagnosis of this defect uses.

## Requirements

### Acceptance criteria

- [ ] A listener exits on its own when its parent process is gone — it never reparents and keeps
      polling
- [ ] The daemon detects more than one live poller for a role and terminates any whose parent is dead,
      logging a lifecycle event naming the pid
- [ ] A receipt records **which process** consumed the message (pid, and whether its parent was alive),
      so an orphan-consumed message is distinguishable after the fact
- [ ] `IsPolling` cannot be satisfied by an orphan alone — a poller with a dead parent does not count
      as "alive" for the `Stop` hook
- [ ] Negative control: a healthy listener with a live parent is untouched by the reaper and still
      counts for `IsPolling`
- [ ] Negative control: the turn-boundary SIGTERM/relaunch cycle behaves exactly as today

### Technical approach

Two layers. In the listener: check `getppid()` each poll iteration and exit when it becomes 1 (or when
the recorded parent pid is no longer alive) — the process that would eat the message is the one best
placed to notice it should not exist. In the daemon: a per-role poller census on the health sweep,
killing pollers whose parent is dead and logging `listener-orphan-reaped`; the same sweep can refuse
to count them for `IsPolling`. Receipts gain `acked_pid` and `parent_alive` so the store can say
what happened next time.

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/cmd/inbox.go` | The `--poll --loop` loop — where a parent-liveness check belongs |
| `tools/muxcode/bus/notify.go` | `IsPolling` (`:786`), `PollingMarkerPath` — single-pid marker |
| `tools/muxcode/cmd/hook.go` | `hookStop` (`:426-471`) — relaunch decision that an orphan can satisfy |
| `tools/muxcode/bus/delivery.go` | Receipt fields — add the consuming pid |
| `tools/muxcode/daemon/daemon.go` | Health sweep — home for the poller census and reaper |

## Implementation

### Phase 1: Pin

- [ ] Characterization: a listener whose parent is killed keeps running and keeps consuming (scratch
      bus; spawn the listener under a short-lived parent, kill the parent, send a message, observe the
      receipt written and the message absent from any live reader); failure message names Phase 2
- [ ] Pin that `IsPolling` returns true for that orphan
- [ ] Pin that the receipt carries no consumer identity

### Phase 2: The listener notices

- [ ] `inbox --poll --loop` exits when its parent is gone (`getppid() == 1`, or recorded parent dead)
- [ ] Invert the Phase 1 characterization
- [ ] Negative control: a listener with a live parent runs as before

### Phase 3: The daemon reaps and the store attributes

- [ ] Health sweep counts pollers per role; kills any with a dead parent; `listener-orphan-reaped`
      lifecycle event with the pid
- [ ] `IsPolling` excludes pollers with a dead parent
- [ ] Receipts record `acked_pid` and whether the consumer's parent was alive
- [ ] Negative control: healthy listeners untouched, `IsPolling` unchanged for them

### Phase 4: Integration test

- [ ] Create `scripts/test-orphan-listener.sh` (hermetic; scratch bus + daemon)
- [ ] Test: orphan a listener → it exits on its own within one poll interval; the next message is
      consumed by a live listener and its receipt names that pid
- [ ] Test: with the self-exit disabled (env), the daemon reaps the orphan and logs the event
- [ ] Test: `IsPolling` is false while only an orphan exists
- [ ] Negative control: a live listener is never reaped and keeps its receipts
- [ ] Coverage floor keeps a skipped section from reporting green
- [ ] Run the script and verify all checks pass

## Notes

Filed 2026-09-08 by plan after edit found and killed the orphan and pointed to the late
`test153-followup`. Plan verified the receipt, the lifecycle rows and the `IsPolling` code, and
checked its own inbox for the same window (nothing lost there). Plan's first reaction — "the orphan
ate nothing" — was true only of plan's inbox and was corrected by edit; the spec records the
correction. The cause of the orphaning itself (why the turn-end SIGTERM missed this child) is not
established and is Phase 1's first question.

## Status

**Backlog** — filed 2026-09-08. Not started. **Second occurrence 2026-09-10** in session
`is-advising-gateway`, with an inside witness and a materially worse consequence: a user-authorised
`cdk deploy` acked by the bus and never run (see [Observed again](#observed-again-2026-09-10-session-is-advising-gateway--now-with-an-inside-witness)).
31 orphaned listeners were alive at the time. Priority should be re-read against that: the 09-08
filing measured latency, the 09-10 recurrence measured a silently skipped deploy.
