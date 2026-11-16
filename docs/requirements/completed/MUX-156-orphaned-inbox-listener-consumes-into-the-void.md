# MUX-156: An Orphaned Inbox Listener Consumes Messages Into the Void

**Tracking:** [mkober/muxcode#122](https://github.com/mkober/muxcode/issues/122)

A `muxcode inbox --poll --loop` whose parent has died keeps running, keeps winning the race for its
role's inbox, and keeps writing receipts — while printing every message it consumes to a stdout that
nobody reads. The bus records the message as **acked by the role**; the agent never sees it. The
daemon's task-stall backstop eventually re-drives *requests*, minutes late; a response or event with
no task behind it is simply gone.

Observed 2026-09-08: an edit listener orphaned at 14:44:52 (PPID 1) ran for 68 minutes. Inside that
window a `run → edit` request was acked nine seconds after it was sent and answered three and a half
minutes later, only after two daemon re-drives.

## Context

### Findings at activation (2026-11-14)

Checked by plan against main `1f13107` when the defect loop activated this spec. The defect stands
in a narrower form than filed.

| Spec says | Tree at `1f13107` |
|-----------|-------------------|
| Orphans accumulate — one per turn, 31 alive on 2026-09-10 — because a newcomer finding a live holder exits | Changed since filing: `SetPolling` (`bus/notify.go:770`) is **newest-wins**. A new listener terminates the incumbent and takes the marker, so each relaunch removes the previous listener. The population no longer grows with turn count |
| An orphan can be the role's only consumer | Still true, and now the whole defect: `hookStop` relaunches a listener only when `IsPolling` says none is alive, and `IsPolling` (`bus/notify.go:786–801`) accepts any live pid in the marker. An orphan holding the marker therefore suppresses the relaunch that would displace it |
| The listener has no parent-liveness check | Still true — no `getppid` anywhere in `bus/`, `cmd/` or `daemon/` |
| The receipt carries no consumer identity | Still true — no consuming pid on the receipt |
| `hookStop` at `cmd/hook.go:426-471` | Now `cmd/hook.go:481` |
| A deliberately detached listener | Refused at PreToolUse since 2026-09-22 (`CheckListenerGuard`), as the 2026-09-22 entry below records |

### What this spec no longer carries

The daemon-side poller census and reaper are handed to
[MUX-184](../backlog/MUX-184-orphaned-session-processes-never-reaped.md), which already claims them
("its daemon-side census is this reaper") and holds the open decision they depend on: what proves a
process is the daemon's to kill. That spec was parked on it. Three items below are ticked as deferred
for that reason — plan's call at activation, **confirmed by the user on 2026-11-15** (MUX-184
Decision 5, relayed by edit: this spec's reaper phases point to MUX-184 and it keeps only its
listener-specific half). MUX-184 was unparked the same day with its ownership proof decided — for a
listener, the `polling-<role>.marker` pid still naming the same process. The self-exit, the `IsPolling` change and the receipt attribution stay here and need no such
decision: a listener that exits by itself kills nothing.

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
| [`MUX-147`](../backlog/MUX-147-process-leak-and-memory-footprint.md) | Orphaned **harness** processes after daemon restarts — the same reparent-to-launchd shape, a different process, no message loss |
| [`MUX-155`](./MUX-155-send-dedup-keys-on-target-not-sender.md) | Filed the same afternoon for plan's dropped notifies; edit's late `test153-followup` is **this** defect, not that one — the two were disentangled by checking each inbox |
| [`MUX-050`](./MUX-050-delivery-acknowledgement.md) | The receipt model this defect defeats from inside |

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

### Observed again, 2026-09-22 — a cause seen directly, and refused at the source

The first orphaning whose **cause** was observed rather than inferred: the agent detached its own
listener on purpose.

| Field | Value |
|-------|-------|
| When | 2026-09-22 10:09:22, session `muxcode`, role **watch** |
| Call | `nohup muxcode inbox --poll --loop > /tmp/watch-listener.log 2>&1 & disown; sleep 1; ps aux \| grep …` |
| PID / PPID | 19757 / **1** (launchd) within a second of the call |
| Ownership | `AGENT_ROLE=watch BUS_SESSION=muxcode`, read with the narrow `ps eww -p` form above (no full dump) |
| Outcome | edit killed it; watch's inbox was empty at the time, so nothing was consumed. A void consumer by construction — stdout to a file nobody reads, receipts written as `watch` |
| Side effect | The same call fired the watch chain's unconditioned `notify` to edit with the raw command as its body — [MUX-177](./MUX-177-watch-chain-fires-every-bash-call-with-raw-command-payload.md)'s defect, recorded there |

**Refused at PreToolUse, for every role.** `CheckListenerGuard` (`bus/listener_guard.go`, the last
check in `GuardDecisionFor`) now denies a `muxcode inbox --poll`/`--loop` that is not the bare, lone
statement of its call. Detection follows command position: env assignments, `(`/`{` and a known
wrapper set (`nohup`, `setsid`, `env`, `timeout`, `nice`, `command`, `exec`, `time`, `caffeinate`,
`stdbuf`, `sudo`, with their option/numeric args) are peeled to the executed word, so a wrapped
listener is found but not bare; `eval`'s arguments and a shell's `-c` string are searched as
commands; a trailing `&`, any stdout redirect (per operator token; `2>`/`2>&1` alone pass, `>&2`
does not), a pipe or any other statement in the call is refused. Every other argument is data, so
`echo muxcode inbox --poll`, a `grep -c` pattern and a bus message quoting the form all pass.
`hookGuard` lost its role gate so the rule binds every role.
Documented in [Hooks](../../hooks.md#hook-guard-edit-guard).

**What this does and does not close.** It refuses the one orphaning cause an agent makes on purpose.
The 09-08 and 09-10 entries were orphans of the Stop-hook relaunch cycle — a turn-end SIGTERM that
missed the child — which this guard does not touch, and the listener still has no parent-liveness
check, the daemon no reaper, the receipt no consumer pid. Every acceptance criterion stays open.

## Requirements

### Acceptance criteria

- [x] A listener exits on its own when its parent process is gone — it never reparents and keeps
      polling
- [x] The daemon detects more than one live poller for a role and terminates any whose parent is dead,
      logging a lifecycle event naming the pid — deferred to
      [MUX-184](../backlog/MUX-184-orphaned-session-processes-never-reaped.md)
- [x] A receipt records **which process** consumed the message (pid, and whether its parent was alive),
      so an orphan-consumed message is distinguishable after the fact
- [x] `IsPolling` cannot be satisfied by an orphan alone — a poller with a dead parent does not count
      as "alive" for the `Stop` hook
- [x] Negative control: a healthy listener with a live parent never exits by itself and still counts
      for `IsPolling` (the reaper half of this control moves with it to MUX-184)
- [x] Negative control: the turn-boundary SIGTERM/relaunch cycle behaves exactly as today

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

- [x] Characterization: a listener whose parent is killed keeps running and keeps consuming (scratch
      bus; spawn the listener under a short-lived parent, kill the parent, send a message, observe the
      receipt written and the message absent from any live reader); failure message names Phase 2
- [x] Pin that `IsPolling` returns true for that orphan
- [x] Pin that the receipt carries no consumer identity

### Results for Phase 1

Recorded 2026-11-14 from the worker's request; the test, the poll loop and both run logs were read
by plan. Test-only change, uncommitted. Verified and ticked 2026-11-14 once all three had run after
the last test edit (17:44:58): `./build.sh` exit 0 at 17:52:12, `./test.sh` exit 0 at 17:53:23
(both modules passed), review at 17:54:05 with 0 must-fix, 0 should-fix, 0 nits
(`/tmp/muxcode-review-1794696808.txt`). The first `./test.sh` on this change, at 17:48, failed on an
unrelated flaky test in `bus/` — filed as
[MUX-229](./MUX-229-codex-exec-args-test-flakes-on-temp-home-cleanup.md) — and the rerun
passed.

| Item | Result |
|------|--------|
| The pin | `TestOrphanedListenerKeepsConsuming_MUX156Pin` (`cmd/inbox_orphan_test.go`) runs the real `inbox --poll --loop` — the test binary in helper mode through `TestMain` — as a child of a `/bin/sh` that exits, on a scratch bus (`MUXCODE_BUS_BASE`), with no tmux |
| What it holds | The orphan stays alive after its parent exits (inverted by Phase 2); `IsPolling` is true for it (Phase 3); it consumes the next message into `/dev/null`, leaving an `ack` receipt attributed to the role (Phase 2); the receipt names no process (Phase 3). Each failure message names the phase that inverts it |
| Run 1 | Run agent, proc `1794696320-proc-36923deb`: 6 passed, 0 failed, but exit 1 — its leftover-process check counted 1, a `pgrep -f` pattern matching an unrelated command line |
| Run 2 | The same with an exact-name process check, proc `1794696355-proc-3e943f8f`: 3 passed (want 3), 0 `cmd.test` processes before and after, exit 0 |
| **Finding: one batch, not many** | `inboxPoll` (`cmd/inbox.go:148–189`) returns after printing its first non-empty batch; `--loop` only restarts the cycle on a timeout with nothing read. An orphan therefore swallows **one** batch and exits — the spec's "keeps consuming" overstates it. The 2026-09-10 dropped deploy is one such batch. The loop also already exits when its tmux session is gone (checked every few ticks), which this spec did not record |
| What that changes | The harm per orphan is bounded at one batch, but the batch is still lost and still acked. Nothing in Phases 2–4 depends on repeated consumption, except that Phase 4's "the next message is consumed by a live listener" needs a fresh listener started after the orphan exits |

### Phase 2: The listener notices

- [x] `inbox --poll --loop` exits when its parent is gone (`getppid() == 1`, or recorded parent dead)
- [x] Invert the Phase 1 characterization
- [x] Negative control: a listener with a live parent runs as before

### Results for Phase 2

Recorded 2026-11-14 from the worker's request; the code, the tests and the run log were read by
plan. Uncommitted. Verified and ticked 2026-11-14 once all three had run after the last edit
(17:56:09): `./build.sh` exit 0 at 17:59:00, `./test.sh` exit 0 at 18:00:09 (both modules passed),
review at 18:00:51 with 0 must-fix, 0 should-fix, 0 nits (`/tmp/muxcode-review-1794697215.txt`).

| Item | Result |
|------|--------|
| The check | `inboxPoll` records `os.Getppid()` at start; every tick, before the inbox is read, `listenerOrphaned(parent, os.Getppid())` — true when the parent pid has changed or is 1 — ends the listener with a `listener-orphan-exit` warn row naming the role, its pid and the lost parent (`cmd/inbox.go:105–110`, `:145`, `:164–168`) |
| Phase 1 pin inverted | `TestOrphanedListenerExitsWithoutConsuming` replaces `TestOrphanedListenerKeepsConsuming_MUX156Pin`: the orphan exits and the message stays in the inbox for a live listener |
| Negative control | `TestLiveListenerKeepsRunningAndConsumes` — a listener whose parent lives keeps running and consumes. It also carries a Phase 3 pin on the receipt's missing pid |
| Phase 3 pin kept | `TestIsPollingCountsAnOrphanedMarkerHolder_MUX156Pin` — `IsPolling` still counts an orphaned marker holder until Phase 3 |
| Run | `/tmp/mux156-p2-check.sh` via the run agent, proc `1794696993-proc-081da989`: 6 passed, 0 failed (expected 6). The three tests pass three times over (9/9) and the full `./cmd` package passes; a copy with `listenerOrphaned` forced false fails the orphan test ("the message must stay in build's inbox for a live listener, got []") while the live control still passes; no listener process left behind |
| To watch | A listener legitimately started with parent pid 1 would read as orphaned at its first tick and exit. No such launch road is known — the harness backgrounds the listener under its own shell, and `CheckListenerGuard` refuses a detached one — but the unit control covers a live parent only; Phase 4 or a live session is where a real agent's listener is seen to survive |
| Docs | `docs/agent-bus.md` `muxcode inbox`: `--loop` corrected (it keeps polling past the timeout and still returns after its first batch — it had said "re-arm after each return") and a table of the two exit-without-consuming conditions with the `listener-orphan-exit` row; `docs/hooks.md` inbox-listener rule: one sentence pointing at it |

### Phase 3: The daemon reaps and the store attributes

- [x] Health sweep counts pollers per role; kills any with a dead parent; `listener-orphan-reaped`
      lifecycle event with the pid — deferred to
      [MUX-184](../backlog/MUX-184-orphaned-session-processes-never-reaped.md)
- [x] `IsPolling` excludes pollers with a dead parent
- [x] Receipts record `acked_pid` and whether the consumer's parent was alive
- [x] Negative control: healthy listeners unaffected, `IsPolling` unchanged for them

### Results for Phase 3

Recorded 2026-11-16 from the worker's report; the code, the tests and the review notes were read by
plan. Uncommitted. Verified and ticked 2026-11-16 once all three had run after the last edit
(11:27:34): `./build.sh` exit 0 at 11:31:58, `./test.sh` exit 0 at 11:33:05 (both modules passed),
review at 11:33:39 with 0 must-fix, 0 should-fix, 0 nits (`/tmp/muxcode-review-1794846790.txt`). An
earlier review of this phase had asked for a real orphan control on the receipt; that finding is
resolved.

| Item | Result |
|------|--------|
| The sidecar | `SetPolling` writes `polling-<role>.parent` (`<listener pid> <parent pid>`) beside the marker, which stays a bare pid; `ClearPolling` removes it when this process owns it (`bus/notify.go`, `PollingParentPath` in `bus/config.go`) |
| `IsPolling` | False for a live holder whose sidecar names it and records a parent that is pid 1 or gone (`pollerOrphaned`). The marker is left in place — the holder exits on its next tick and a newcomer's `SetPolling` terminates it. No sidecar, or one naming another pid, is not judged: the holder counts as before |
| Receipts | `acked_pid` and `acked_parent_alive` (`bus/delivery.go`), the latter read as "not reparented to pid 1"; absent on older receipts |
| Tests | `bus/poll_orphan_test.go`: `TestIsPollingExcludesAnOrphanedHolder` (5 cases, three of them controls), `TestSetPollingRecordsParentSidecar`, `TestWriteReceiptNamesTheConsumer`, `TestWriteReceiptByAnOrphanRecordsItsParentGone` (the real writer after its shell parent exits). `cmd/inbox_orphan_test.go` asserts a live listener's receipt carries its pid and a live parent |
| Runs | Run task `1794846035-spawn-926e95e5-12e1cd8f`: 11 passed, 0 failed; with `pollerOrphaned` forced false the parent-dead and pid-1 cases fail and the three controls pass. Proc `1794846588-proc-1369b2bc` (per the review notes): 6/6, the orphan receipt case 3/3 with no skips; with `parentAlive` hardcoded true the orphan case fails and the healthy control passes |
| Limitation | An orphan adopted by a Linux subreaper rather than pid 1 reads as having a live parent, in both `acked_parent_alive` and the listener's own check; the orphan receipt test skips, naming it, where that happens |
| Docs | `docs/agent-bus.md` `muxcode inbox` (the sidecar and `IsPolling`), `docs/architecture.md` delivery tracking (the two receipt fields) |

### Phase 4: Integration test

- [x] Create `scripts/test-orphan-listener.sh` (hermetic; scratch bus + daemon)
- [x] Test: orphan a listener → it exits on its own within one poll interval; the next message is
      consumed by a live listener and its receipt names that pid
- [x] Test: with the self-exit disabled (env), the daemon reaps the orphan and logs the event —
      deferred to [MUX-184](../backlog/MUX-184-orphaned-session-processes-never-reaped.md)
- [x] Test: `IsPolling` is false while only an orphan exists
- [x] Negative control: a live listener never exits by itself and keeps its receipts
- [x] Coverage floor keeps a skipped section from reporting green
- [x] Run the script and verify all checks pass

### Results for Phase 4

Recorded 2026-11-16 from the worker's report; the script's header, the run log and the review notes
were read by plan. Uncommitted. Verified and ticked 2026-11-16 once all three had run after the last
edit (the script, 11:43:04): `./build.sh` exit 0 at 11:44:14, `./test.sh` exit 0 at 11:44:33 (both
modules passed), review at 11:45:10 with 0 must-fix, 0 should-fix, 0 nits
(`/tmp/muxcode-review-1794847480.txt`). The worker's first version had 25 checks; a review finding
that the turn-boundary criterion had no coverage added section E, and the reviewed script has 34.

| Item | Result |
|------|--------|
| The script | `scripts/test-orphan-listener.sh` — builds muxcode from the tree (`MUXCODE_ORPHAN_LISTENER_BIN` overrides), scratch HOME, state, config and one `BUS_SESSION` per section; **no daemon and no tmux server**, because the reaper it would have exercised is deferred to MUX-184. The step's "scratch bus + daemon" is met as a scratch bus only |
| How the orphan is made | A listener under a shell is SIGSTOPped once it holds the marker and the shell exits — a real reparented listener that cannot tick until SIGCONT, so "only an orphan exists" is observable |
| A — the orphan | With a request pending, `muxcode hook stop` demands a relaunch (`IsPolling` false); on SIGCONT the orphan exits within one poll interval with a `listener-orphan-exit` row, releases the marker, and leaves the request in the inbox with no receipt |
| B — the next listener | Consumes that request; its receipt names its own pid and a live parent |
| C — negative control | A listener with a live parent runs across several ticks, keeps the Stop hook silent while stopped with a request pending, and consumes with a receipt naming it |
| E — negative control | The turn-boundary cycle as today: a healthy listener SIGTERMed (exit 143) leaves marker and sidecar stale, the hook demands a relaunch, the replacement claims both, consumes with its own receipt, no orphan row, and its clean exit clears both files |
| D — control | A muxcode built from a copy of the tree with the self-exit and the `IsPolling` exclusion reverted fails A: the hook stays silent and the orphan consumes, its receipt recording `acked_parent_alive=false` |
| Provider pin | `build` is pinned to Claude for the run: only a self-polling provider's Stop hook reads `IsPolling`, and build's default would return before it, leaving the hook checks vacuously green |
| Run | Proc `1794847394-proc-46e5ce3a`: 34 passed, 0 failed, floor 34, exit 0 |
| Docs | `CLAUDE.md` integration-test entry `test-orphan-listener` |

## Notes

Filed 2026-09-08 by plan after edit found and killed the orphan and pointed to the late
`test153-followup`. Plan verified the receipt, the lifecycle rows and the `IsPolling` code, and
checked its own inbox for the same window (nothing lost there). Plan's first reaction — "the orphan
ate nothing" — was true only of plan's inbox and was corrected by edit; the spec records the
correction. The cause of the orphaning itself (why the turn-end SIGTERM missed this child) is not
established and is Phase 1's first question.

**2026-09-22 addendum.** One cause is now established by direct observation — an agent detaching
its own listener with `nohup … & disown` — and refused at PreToolUse by `CheckListenerGuard` (see
[Observed again, 2026-09-22](#observed-again-2026-09-22--a-cause-seen-directly-and-refused-at-the-source)).
That is a different cause from the relaunch-cycle orphans of 09-08 and 09-10, whose mechanism is still
Phase 1's question; the guard narrows the population, it does not answer it.

## Status

**Complete** — closed 2026-11-16 by `55-defect-backlog-loop` run `1794626267-55-defect-backlog-loop-15547e2e`, which activated it 2026-11-14 (Defects rank 19 then; ranks 1–18 were parked). 23/23: Phases 1–4 complete and every criterion met, three of the 23 as reaper items deferred to MUX-184. Uncommitted at close-out; the run's ship step commits it.

History: filed 2026-09-08. **Second occurrence 2026-09-10** in session
`is-advising-gateway`, with an inside witness and a materially worse consequence: a user-authorised
`cdk deploy` acked by the bus and never run (see [Observed again](#observed-again-2026-09-10-session-is-advising-gateway--now-with-an-inside-witness)).
31 orphaned listeners were alive at the time. Priority should be re-read against that: the 09-08
filing measured latency, the 09-10 recurrence measured a silently skipped deploy. **Fourth
occurrence 2026-09-22** (session `muxcode`, watch, deliberate `nohup … & disown`): the first with an
observed cause, now refused at PreToolUse by `CheckListenerGuard`; no message lost. The spec's
own work — self-exit, reaper, consumer attribution — is untouched and all criteria remain open.
