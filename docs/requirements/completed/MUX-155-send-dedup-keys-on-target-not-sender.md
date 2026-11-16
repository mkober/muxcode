# MUX-155: `muxcode send` Drops a Message Because Another Agent's Task Is In Flight

**Tracking:** [mkober/muxcode#121](https://github.com/mkober/muxcode/issues/121)

At 15:39 on 2026-09-08 plan sent `muxcode send edit notify "MUX-153 updated: …"`. The CLI answered
*"In-flight task for edit:notify already exists (sent 100s ago) — already tracking"* and returned.
Nothing was written — not to edit's inbox, not to the log, not to the task store. The in-flight task
it had matched was **watch's**: `1788895052-watch-a18e3100  watch→edit  notify`, sent 100 seconds
earlier. Plan's message was dropped because a different agent had an unanswered message to the same
recipient under the same action name.

The dedup guard exists so that one sender retrying its own request after a killed `--wait` does not
inject a duplicate. It keys on `(to, action)` and never looks at the sender, so every agent's
unanswered `notify` to edit silences every other agent's `notify` to edit — and `notify` is the
action every role uses for reports. The sender is told its message is tracked. It is not.

## Context

### Findings at activation (2026-11-14)

Checked by plan against main `7b73f05` when the defect loop activated this spec. The defect stands;
line numbers have moved and one piece of Phase 3 already exists.

| Spec says | Tree at `7b73f05` |
|-----------|-------------------|
| Pre-send dedup at `cmd/send.go:184-189` | `cmd/send.go:190-196` (`--track`: prints "already tracking" and returns, exit 0) and `:197-203` (`--wait`: reattaches) — both still call `bus.FindInFlightTask(session, to, action)` |
| `FindInFlightTask` / `HasInFlightTaskForRole` key on `(to, action)` | Unchanged, `bus/dedup.go:235` and `:217`; neither reads `From` |
| Duplicate-reply suppression returns `nil` at `bus/inbox.go:228` | Now `bus/inbox.go:186-187`; still returns `nil`, so the CLI still prints `Sent response:…` |
| `Send` returns `nil` for every suppression (Phase 3, first step) | Partly done since filing: the two **request** suppressions (`bus/inbox.go:354`, `:359`) return `ErrSendSuppressed`, and `cmd/send.go:220` reads it. The duplicate-reply road and the CLI's own pre-send return are what remain |
| Callers of the key | Besides `cmd/send.go`: `bus/inbox.go:356` (`Send` itself) and `bus/graph_exec.go:1264` (`dispatchNode`'s adoption — [MUX-170](./MUX-170-graph-dispatch-adopts-foreign-in-flight-task.md)'s subject; adding `from` changes what it can adopt, so its behaviour is pinned here even though its redesign is not this spec's) |

### Observed (2026-09-08, session `muxcode`; every fact below read by plan directly)

| | |
|---|---|
| Dropped | `plan → edit notify` at 15:39:12 |
| CLI output | `In-flight task for edit:notify already exists (sent 100s ago) — already tracking` |
| Matched task | `1788895052-watch-a18e3100  watch→edit  notify  [in-flight]` (`muxcode tasks`), sent 15:37:32 — 100s earlier, exactly the reported age |
| Written anywhere? | No — `grep` of `inbox/edit.jsonl` and `log.jsonl` for the payload: 0 rows; no task or delivery file for it |
| Recovered by | Re-sending under a made-up action name (`spec-update`), which the guard does not match — the workaround agents now rely on, so the fix must not break it |
| Same afternoon, edit's side | Edit's MUX-145 follow-up — a *different* message under its own in-flight `update-docs` to plan — was refused as a duplicate until `--force`. That is the same-sender case the guard intends, blocking a message that was not a retry (reported by edit, 15:22) |

An earlier drop the same afternoon (14:29) was the *same-sender* case — two plan notifies seconds
apart — which is the behaviour the guard intends. This one is not.

### Mechanism — verified

| Fact | How established |
|------|-----------------|
| The pre-send dedup runs for `request` sends with tracking on and no `--force`, and returns before any write | `cmd/send.go:184-189` — `if existing, found := bus.FindInFlightTask(session, to, action); found { … return }` |
| `FindInFlightTask` matches `t.To == to && t.Action == action` and nothing else | `bus/dedup.go:235-249` |
| `HasInFlightTaskForRole` has the same key | `bus/dedup.go:217` |
| The guard's stated purpose is a **same-sender** retry: "where `--wait` was killed by Bash tool timeout and the agent retries the same request" | `cmd/send.go` comment above the check |
| Tracking is on by default for requests (`Tracking task …` is printed on every plain send), so the guard applies to ordinary sends, not only `--track` | observed on every request send this session |

The sibling failure on the reply road: `bus.Send` returns `nil` after *"suppressing duplicate reply to
already-completed task"* (`bus/inbox.go:228`, deliberately before any write), and `cmd/send.go` then
prints `Sent response:<action> to <role>`. Twice today that line sent plan looking for a delivery that
never happened. Both roads share the defect: **the CLI reports success or tracking for a message it
did not send.**

### Consequences

| | |
|---|---|
| Silent message loss | Keyed on *another agent's* traffic — the sender can do nothing to predict or prevent it |
| The sender is misled | "already tracking" means someone else's task is tracked; no reply will ever wake this sender |
| The recipient is misled by omission | Edit believed plan had not moved on MUX-145 for eight minutes and sent a follow-up; plan had sent two reports |
| Collision rate is highest where it matters | `notify` is the universal report action, and edit is the universal recipient — watch, run, review and plan all notify edit |
| Workaround corrodes the bus | Senders learn to invent action names to get through, which defeats dedup entirely and makes action names meaningless |

### Relationship

| Spec | Relationship |
|------|--------------|
| [`MUX-010`](../backlog/MUX-010-delegation-message-hygiene.md) | The same guard's *expiry* half — a stuck in-flight task once blocked every `(to,action)` send to a role forever, fixed by `TaskExpired`. This is the *key* half: the guard also blocks across senders |
| [`MUX-154`](./MUX-154-codex-status-line-closes-tracked-tasks.md) | Same family of "the bus says one thing and did another" — that one records a success that did not happen; this one reports tracking for a message that does not exist |
| [`MUX-111`](../backlog/MUX-111-harness-reply-miscorrelation.md) | Adjacent: harness replies correlate to the wrong request. Different mechanism |

## Requirements

### Acceptance criteria

- [x] The in-flight dedup never suppresses a send because of a task **another sender** created — the
      key includes `from`
- [x] A suppressed send never prints a success or "tracking" line: the CLI names the suppression, says
      the message was not sent, and exits non-zero (or returns a distinct status) — on both roads,
      in-flight dedup and duplicate-reply — and on the five other not-sent roads: `NOT SENT`, exit 1
- [x] Negative control: a same-sender retry of an identical in-flight request is still deduplicated
      (the `--wait`-killed-by-timeout case the guard exists for), and `--wait` still reattaches
- [x] Negative control: `--force` still bypasses the dedup as documented
- [x] Negative control: a genuine duplicate reply to a completed task is still suppressed — but says so
      honestly
- [x] Negative control: distinct action names remain independently deliverable — varying the action
      is the workaround agents use today, and the fix must not turn it into a new drop

### Technical approach

Add `from` to the key in `FindInFlightTask` / `HasInFlightTaskForRole` (and their callers), so the
guard matches the retry it was written for and nothing else. Tighten further with a payload
hash, since a same-sender send of a *different* message under the same action is also not a retry —
edit hit exactly that today: its MUX-145 follow-up, a different message under its own in-flight
`update-docs`, was refused until `--force`. Then make `Send`'s suppression paths return a sentinel the CLI can distinguish from success,
and have `cmd/send.go` print the suppression and exit non-zero instead of `Sent …`.

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/dedup.go` | `HasInFlightTaskForRole` (`:217`), `FindInFlightTask` (`:235`) — the `(to, action)` key |
| `tools/muxcode/cmd/send.go` | The pre-send dedup (`:184-189`), `--wait` reattach (`:191-`), the `Sent …` success line |
| `tools/muxcode/bus/inbox.go` | Duplicate-reply suppression returning `nil` (`:228`, and the self-addressed variant at `:92`) |
| `tools/muxcode/bus/task.go` | `Task.From` — already recorded, just not consulted |

## Implementation

### Phase 1: Pin

- [x] Characterization test: two senders, same `(to, action)`, the second send is dropped today;
      failure message names Phase 2
- [x] Pin the same-sender retry case as the behaviour to keep
- [x] Pin that a suppressed send (both roads) currently exits 0 and prints a success/tracking line

### Results for Phase 1

Recorded 2026-11-14 from the worker's request; the test names were read by plan in the tree and the
counts in the proc log. Test-only change, uncommitted. The boxes above are ticked at verification,
after build, `./test.sh` and review.

| Pin | Test | What it holds |
|-----|------|---------------|
| Cross-sender drop at the CLI (inverted by Phase 2) | `TestSendCLI_CrossSenderInFlightDropped_MUX155Pin` (`cmd/send_dedup_test.go`) | With watch's `notify` to edit in flight, plan's `notify` prints "already tracking", writes nothing and exits 0. Its failure message names Phase 2 |
| Cross-sender drop in `Send` (inverted by Phase 2) | `TestSendMessage_CrossSenderInFlightSuppressed_MUX155Pin` (`bus/dedup_test.go`) | `Send`'s guard suppresses it, and `FindInFlightTask` hands any asker watch's task — the key `dispatchNode`'s adoption uses (MUX-170) |
| Same-sender retry (kept) | `TestSendCLI_SameSenderRetryDeduplicated` | An identical in-flight request from the same sender is deduplicated |
| `--wait` reattach (kept) | `TestSendCLI_SameSenderWaitReattaches` | `--wait` reattaches to the sender's own task |
| Duplicate reply reported as sent (inverted by Phase 3) | `TestSendCLI_DuplicateReplyReportedSent_MUX155Pin` | A suppressed reply to a completed task prints `Sent response` and exits 0 |

Run agent, proc `1794693930-proc-a1ed2c6d`: 8 passed, 0 failed (expected 8).

Verified and ticked 2026-11-14 once all three had run after the last test edit (17:05:11):
`./build.sh` exit 0 at 17:06:36, `./test.sh` exit 0 at 17:07:46 (both modules passed), review at
17:09:22 with 0 must-fix, 0 should-fix and 1 nit (`/tmp/muxcode-review-1794694070.txt`). The nit,
`runSendAs` (`cmd/send_dedup_test.go:57`) never closing the read end of its `os.Pipe`, was fixed in
Phase 2 and confirmed by that phase's review.

### Phase 2: Key on the sender

- [x] `FindInFlightTask` / `HasInFlightTaskForRole` take and match `from`
- [x] Invert the Phase 1 cross-sender pin
- [x] Negative control: same-sender dedup and `--wait` reattachment unchanged; `--force` unchanged

### Results for Phase 2

Recorded 2026-11-14 from the worker's request; signatures, callers and test names were read by plan
in the tree and the counts in the proc log. Uncommitted. Verified and ticked 2026-11-14 once all
three had run after the last edit (17:14:06, the Phase 1 pipe-reader nit's fix): `./build.sh` exit 0
at 17:14:15, `./test.sh` exit 0 at 17:15:21 (both modules passed), review at 17:15:59 with 0
must-fix, 0 should-fix, 0 nits (`/tmp/muxcode-review-1794694525.txt`). The check run below predates
that last test-file edit by 73 s; the full suite after it covers the same tests.

| Item | Result |
|------|--------|
| The key | `FindInFlightTask(session, to, from, action)` and `HasInFlightTaskForRole(session, to, from, action)` (`bus/dedup.go`) match `t.From == from` as well. The sender is compared **raw**, not through `NormalizeBusRole`, so a `daemon` dispatch matches only the daemon's own task, never edit's |
| Callers | `cmd/send.go` twice (`--track` report, `--wait` reattach), `bus/inbox.go` (`Send`'s guard) and `bus/graph_exec.go` `dispatchNode`, which now adopts only a task the daemon itself sent |
| Phase 1 pins inverted | `TestSendMessage_CrossSenderInFlightDelivered` (bus) and `TestSendCLI_CrossSenderInFlightDelivered` (cmd) replace the two cross-sender `_MUX155Pin` tests: the second sender's message is now delivered |
| New tests | `TestExecDispatchIgnoresForeignInFlightTask` and `TestExecSuppressedDispatchAdoptsOwnInFlightTask` (graph dispatch), `TestSendCLI_ForceBypassesSameSenderDedup`, `TestSendCLI_DistinctActionsIndependent` |
| Run | `/tmp/mux155-phase2-check.sh` via the run agent, proc `1794694373-proc-f64ef5aa`: 13 passed, 0 failed (expected 13). The tree as written passes its 20 tests; a copy with the sender compare removed fails the 5 cross-sender tests while the 6 controls (same-sender retry, `--wait` reattach, `--force`, distinct actions, `Send`'s own duplicate guard, own-task adoption) still pass |
| Not done | The payload-hash tightening from the technical approach — it is no Phase 2 step and no criterion requires it. A same-sender send of a *different* message under an in-flight action is still deduplicated without `--force` |
| Reach beyond this spec | `dispatchNode` no longer adopts a foreign task, which is part of [MUX-170](./MUX-170-graph-dispatch-adopts-foreign-in-flight-task.md)'s defect; what the executor does when a dispatch *is* suppressed stays that spec's |
| Docs | `docs/agent-bus.md` (new "In-flight dedup" paragraph; self-heal line) and `docs/architecture.md` (task-timeout row) name the `(from, to, action)` key |

### Phase 3: An honest CLI

- [x] `Send` returns a distinguishable result for each suppression path instead of `nil`
- [x] `cmd/send.go` prints what was suppressed and why, never `Sent …` or "already tracking" for a
      message it did not write; non-zero exit
- [x] Negative control: a delivered send still prints `Sent …` and exits 0

### Results for Phase 3

Recorded 2026-11-14 from the worker's request; the sentinels, the CLI's switch and the run logs were
read by plan. Uncommitted. Verified and ticked 2026-11-14 after review round 2, once all three had
run after the last edit (17:28:40): `./build.sh` exit 0 at 17:30:27, `./test.sh` exit 0 at 17:31:39
(both modules passed), review at 17:32:15 with 0 must-fix, 0 should-fix, 0 nits and round 1's
finding resolved (`/tmp/muxcode-review-1794695502.txt`).

| Item | Result |
|------|--------|
| Sentinels | `bus.Send` returns `ErrReplySuppressed` for a reply to an already-answered task (was `nil`) and `ErrSelfSendDropped` for a self-addressed message (was `nil`), beside the existing `ErrSendSuppressed` (`bus/inbox.go`) |
| The CLI | `notSent` (`cmd/send.go`) prints `NOT SENT — <reason>` and exits 1 for each of the three, and for the pre-send `--track` match on the sender's own in-flight task, which it names (was "already tracking", exit 0). No tracked task is recorded for an unsent message. `--wait` still reattaches |
| Tests | `TestSendCLI_DuplicateReplyReportedNotSent` replaces the Phase 1 `…ReportedSent_MUX155Pin`; `TestSendCLI_SameSenderRetryDeduplicated` now expects `NOT SENT` and exit 1 |
| Check run 1 | `/tmp/mux155-phase3-check.sh`, proc `1794694822-proc-8b44d622`: **9 passed, 1 failed** (expected 10), exit 1. `./cmd` and `./daemon` pass; a copy with `acceptReply` returning `nil` again fails the four honesty tests (`TestClaimReplyOutsideTheGraph`, `TestClaimReplyRefusesStrangers`, `TestExecSendNodeNonResultFirstGenuineReplyWins`, `TestSendCLI_DuplicateReplyReportedNotSent`) while two controls pass. The one failure was the check's own layout: its tools-only copy made four `./bus` tests that resolve repository paths (`TestClaudeAgentFlagSoleEmitter` among them) find no repo root |
| Check run 2 | The `./bus` leg rerun on a full-layout copy, proc `1794694942-proc-d78b6021`: `./bus` ok, 1 passed, 0 failed |
| Behaviour change to review | An agent whose reply is a duplicate, or addressed to itself, now sees exit 1 where it saw exit 0. Two self-sends are untouched and still succeed: the launch-time `request:startup` bootstrap (`isStartupBootstrap`), and a self-addressed response carrying a `--reply-to`, which is recorded as a completion without delivery (`recordUndeliveredReply` — edit answering a daemon-dispatched node). Only an uncorrelated self-send reaches `ErrSelfSendDropped` |
| Docs | `docs/agent-bus.md`'s `muxcode send` section: the "In-flight dedup" paragraph names `NOT SENT` and exit 1, and a new table lists the three sentinels |

**Review round 1 (17:26, `/tmp/muxcode-review-1794695174.txt`): 0 must-fix, 1 should-fix.** Plan's
new paragraph in `docs/agent-bus.md` promised `NOT SENT` and exit 1 "for every suppression", and
that was not true: the relay-loop guard and the dedup-window suppression still printed "Suppressed …"
and exited 0. Plan had written the sentence from the three cases the worker reported without reading
the CLI's other return paths. The review offered narrowing the doc or making the promise true; edit
made it true.

| Fix | Result |
|-----|--------|
| Three more roads | The relay-loop guard, the dedup-window suppression and the existing-answer road (which prints the answer already held) now go through `notSent`: `NOT SENT`, exit 1. All were exit 0 |
| Tests | `TestSendCLI_RelayLoopReportsNotSent`, `TestSendCLI_DedupWindowReportsNotSent`, `TestSendCLI_ExistingAnswerReportsNotSent` |
| Fix check | `/tmp/mux155-phase3-fix.sh`, proc `1794695340-proc-0473bf82`: 7 passed, 0 failed (expected 7). A copy with the three roads back on exit 0 fails the three new tests; two controls still pass |
| Scripts that send a deliberate duplicate | `scripts/test-browser-monitor-e2e.sh` and `scripts/test-context-pressure.sh` now tolerate exit 1 on that send. Both are live-session scripts, not run here |
| Docs | The paragraph's table now lists every `NOT SENT` road — seven, with what each did before — and says a script sending a deliberate duplicate must expect exit 1 |

### Phase 4: Integration test

- [x] Create `scripts/test-send-dedup-sender.sh` (hermetic; scratch bus)
- [x] Test: `watch → edit notify` in flight, then `plan → edit notify` → delivered (row in edit's
      inbox, its own task created)
- [x] Test: `plan → edit notify` twice → second suppressed, CLI says so, exit non-zero (negative
      control — the guard cannot go inert)
- [x] Test: reply to a completed task → suppressed and reported honestly, exit non-zero
- [x] Coverage floor keeps a skipped section from reporting green — floor 20
- [x] Run the script and verify all checks pass — 20 passed, 0 failed

### Results for the integration phase

Recorded 2026-11-14 from the worker's request; the section lines and counts were read by plan in the
proc log. Uncommitted. Verified and ticked 2026-11-14 once all three had run after the last edit
(`CLAUDE.md`, 17:35:27): `./build.sh` exit 0 at 17:36:16, `./test.sh` exit 0 at 17:36:33 (both
modules passed), review at 17:37:10 with 0 must-fix, 0 should-fix, 0 nits
(`/tmp/muxcode-review-1794695797.txt`).

| Item | Result |
|------|--------|
| Script | `scripts/test-send-dedup-sender.sh` — hermetic: a muxcode built from the tree, a scratch bus, no daemon and no tmux. Floor 20. `CLAUDE.md`'s script index entry added by edit |
| Run | Run agent, proc `1794695707-proc-7c1e0eda`: **20 passed, 0 failed (floor 20)**, exit 0. The script was last edited 10 s before the run started and not since |
| A: cross-sender | With watch's `notify` to edit in flight, plan's `notify` is sent (exit 0), lands in edit's inbox and is tracked as plan's own in-flight task; watch's task is still in flight |
| B: same-sender retry (negative control) | The first send is delivered and tracked; the retry prints `NOT SENT` naming that task and exits 1, writes no second inbox row and creates no second task; `--force` delivers past the guard |
| C: duplicate reply | Edit's `--wait` request reaches plan, plan's first reply is sent and completes the task; the second reply prints `NOT SENT`, already answered, exit 1, and is in neither edit's inbox nor the log |
| D: pre-fix control | A second binary built with the sender comparison removed and the duplicate reply back on `nil` drops plan's notify behind watch's task, and prints `Sent response:…` with exit 0 for a second reply that landed nowhere — the two defects, under the same checks |
| Deviation | The spec's first test said "exit non-zero" only for the suppressed cases; the control build's cross-sender drop also exits 1, because Phase 3's `NOT SENT` reporting stays in the control — it reverts the key and the reply sentinel, not the CLI's honesty |

## Notes

Filed 2026-09-08 by plan after being bitten twice in one afternoon — once by the intended same-sender
case, once by this cross-sender one — and after first reaching a wrong theory (that the dropped
message had been written to the inbox but not the log). The code settled it: the guard returns before
any write, and the matched task belonged to watch. The "Sent after suppression" idea filed earlier
today in the backlog's ideas list is folded in here as Phase 3.

## Status

**Complete** — closed 2026-11-14 by `55-defect-backlog-loop` run `1794626267-55-defect-backlog-loop-15547e2e`, 21/21. Activated 2026-11-14 by `55-defect-backlog-loop` run `1794626267-55-defect-backlog-loop-15547e2e` (Defects rank 17; ranks 1–16 are parked). 21/21: all four phases and all six criteria verified 2026-11-14; ready for close-out. Filed 2026-09-08.
