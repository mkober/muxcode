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

**Backlog** — filed 2026-09-08. Not started.
