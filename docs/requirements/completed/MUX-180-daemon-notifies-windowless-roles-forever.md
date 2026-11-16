# MUX-180: The Daemon Notifies a Windowless Role Forever, and Its Inbox Grows Unbounded

**Tracking:** [mkober/muxcode#138](https://github.com/mkober/muxcode/issues/138)

**Provenance:** filed 2026-09-11 by plan on the user's request relayed by edit (`1789155218`). Every
figure below verified in the primary records before filing.

A role can be **configured without being launched**. `analyze` is set to `codex`/`gpt-5.6-sol` and has
no tmux window in this session. The daemon routes to it anyway: it rewrites `trigger-analyze.notify`
each cycle, attempts an injection, fails at pane capture, logs a warning, and repeats — **164 times
across 28 hours**. Meanwhile its inbox has reached **109 messages** that nothing can ever consume.

## Context

### Observed (session `muxcode`, 2026-09-10 → 2026-09-11)

| Fact | Value | Source |
|------|-------|--------|
| `analyze` window | **absent** — session holds `auto plan edit build test serve review deploy run watch commit` | `tmux list-windows` |
| `analyze` configured | `codex`, `gpt-5.6-sol` | `muxcode config list` |
| Queued inbox | **109 messages** | `AGENT_ROLE=analyze muxcode inbox --peek` |
| Refusals | **164** rows, `injection-refused  analyze: pane capture failed: exit status 1` | `~/.config/muxcode/logs/muxcode.log` |
| Window | first **2026-09-10 08:27:31**, last **2026-09-11 13:11:17** (~28 h) | same |
| Cadence | median **51 s**, min 2 s | same |
| Trigger file | `trigger-analyze.notify` rewritten each cycle (mtime 13:11) | filesystem |

### Mechanism

`WindowForRole(role)` resolves a target name whether or not that window exists
(`bus/notify.go:432`, `:565`, `:1001`); the notify path then captures the pane, and capture is where
it fails. Nothing upstream asks **"does this role have a window?"** — the failure is detected at the
last possible moment, per attempt, forever.

`analyze` is a legitimate role: `bus/launcher.go:113–116` lists it with `research` as included "for
opt-in/mode-cycled configurations". So **configured-but-windowless is a supported state**, and the
notify path does not account for it.

Two consequences, and the second is the one that persists:

| | |
|---|---|
| **Noise** | a `warn` row every ~51 s, indefinitely — it drowns the signal that `injection-refused` is meant to carry (a *dead agent's shell* about to receive a payload, [MUX-164](./MUX-164-codex-trust-prompt-reads-idle-wakeup-into-shell.md)) |
| **Unbounded inbox** | 109 messages accumulate with no consumer and no cap. Delivery receipts never arrive, so anything keyed on receipt for this role never resolves |

### A third effect worth noting

The workflow state machine transitions **into** `analyzing` for this role each cycle —
`workflow analyzing from=editing trigger=daemon:analyze-route` at 13:09:56 and 13:11:17, each
immediately followed by the refusal. The state machine advances to a state whose agent cannot receive
anything, then returns on the next `hook:analyze:edit`. Whether that is a separate defect or the same
missing precondition is for Phase 1.

### Decisions — the user's, recorded 2026-11-15

Answered by the user in conversation and relayed by edit (`1794787239-edit-1aacf9a4`). The criteria
and phases below were rewritten to them the same day.

**Context the user gave, which changes this spec's vocabulary.** The feature that hosts several
agents in one window (research inside plan's window, for example) is going to be removed, and some
future agents may never have a window of their own. **Every agent always has its own inbox, window
or not.** So "has no window" is not the fault. The fault is an inbox **nothing can consume**.

| # | Decision | Chosen |
|---|----------|--------|
| 1 | What an inbox does at its cap | **Cap 50 per role.** Everything up to 50 is kept and one alert is raised on reaching it; past it a new send is refused with `NOT SENT`, exit 1, so the sender is told. Nothing already queued is dropped, and the queue is delivered when a consumer appears |
| 2 | Whether the workflow routes to such a role | **No.** The workflow does not transition (`analyze-route` → `analyzing`, for example) to a role with no consumer; the transition is skipped and logged once per role |
| 3 | How this divides with [MUX-145](./MUX-145-messages-routed-to-windowless-role.md) | **Refuse at `Send`, which is MUX-145's.** The test is "can anything consume this role's inbox" — no window **and** no listener **and** no headless agent running — never "does it have a window"; a windowless agent that reads its own inbox receives normally. **MUX-145 owns the `Send`-time refusal; this spec owns notify suppression, the cap and workflow routing** |

**Kept as two specs, not merged** — proposed by plan where Decision 3 left it open, **confirmed by
the user 2026-11-16** (relayed by edit, `1794844857-edit-5f52a782`). MUX-145 is 9/38 with a
landed reload fix and a diagnose half this spec has no part in; merging would bury that record. The
one thing the two share is the consumer predicate, and whichever ships first builds it: MUX-145
ranks ahead, so this spec expects to reuse it and builds it only if activated first.

**How the cap and the refusal fit together** — plan's reading, **confirmed by the user 2026-11-16**.
With MUX-145 in place a role with no consumer is refused at `Send` and its inbox cannot grow at all.
The cap is therefore the bound for every case the predicate passes or cannot decide: a window whose
agent is dead or wedged, a consumer that went away after messages queued, an unreadable window list
(which must read as indeterminate, never as "no consumer"). It counts a role's unconsumed messages
whatever the reason, which is also why a healthy role never meets it.

### Findings at activation (2026-11-16)

Re-read by plan against `b2fd5a1`. [MUX-145](./MUX-145-messages-routed-to-windowless-role.md)
shipped the same day (`f87cb3d`) and took the headline symptom with it; what is left is narrower than
this spec's title. Phase 1's re-measure decides how much narrower.

| Spec says | Tree at `b2fd5a1` |
|-----------|-------------------|
| The shared consumer predicate is "reused from MUX-145 if that has shipped, built here if not" | It shipped: `RoleHasConsumer(session, role)`, `bus/consumer.go:31`. A window, a `--poll` **or `--wait`** listener (`IsPolling`, `IsWaiting`) or a running harness (`IsHarnessActive`); an unreadable or empty window list counts; a spawn worker role always counts; **a mode role gets no exception** (the user's decision of 2026-11-16). Nothing here builds a predicate |
| The daemon sends to `analyze`, notifies, fails at capture, 164 times | No longer the path for a role with no consumer. `bus.Send` refuses first (`refuseWithoutConsumer`, `ErrNoConsumer`), and the analyze route returns on that error **before** `bus.Notify` (`daemon/daemon.go:782–787`). No message lands, so no trigger file is written and no injection is attempted for it |
| The condition is reported per cycle | MUX-145 already reports it once per role per episode: one `send-no-consumer` row and one stderr line (`no-consumer-<role>.marker`). `muxcode status` shows `no-consumer` and `diagnose` has a `no-consumer` finding. Criterion 2 may already hold for the send road; whether a *second* once-per-role report is wanted for the notify and workflow roads, or the existing one serves, is Phase 1's to record |
| The inbox grows without bound (109 messages) | Cannot grow for a role with no consumer — every send is refused. It can still grow for a role the predicate passes: a window whose agent is dead or wedged, a consumer that went away after messages queued, an unreadable window list, or sends made with `MUXCODE_CONSUMER_GUARD=0`. **No cap exists anywhere**, so Phase 2 is all still to do |
| `analyze-route` transitions the workflow to `analyzing` each cycle | **Still true and untouched.** `TransitionWorkflow(…, StateAnalyzing, "daemon:analyze-route")` at `daemon/daemon.go:775` runs before the send, so the state machine still enters `analyzing` every cycle for a role that is then refused |
| `bus.Notify` asks nothing about a consumer | **Still true.** `Notify` (`bus/notify.go:563`) resolves `WindowForRole` and writes the trigger file unconditionally (`:572`). It is reached from 16 call sites in `daemon/daemon.go`, not only the analyze route — so a role holding messages stranded before the refusal existed, or left by a consumer that went away, is still notified on every sweep. That, not the analyze route, is where the repeat `injection-refused` rows can now come from |
| `WindowForRole` at `notify.go:432`, `:565`, `:1001` | `:433`, `:566`, `:1046` |
| The cap's refusal returns "through `notSent`" | `notSent` is a CLI helper. `bus.Send` needs a sentinel of its own for the cap, beside `ErrNoConsumer` and, since MUX-175, `ErrResponseSuppressed` |
| `scripts/test-windowless-notify.sh` | Does not exist. `scripts/test-windowless-routing.sh` (MUX-145) already covers the refusal, the single `send-no-consumer` row and an empty inbox after two cycles; Phase 5 should not repeat those and should say what it adds |

Two things the decisions do not spell out, which Phase 2 has to state rather than leave implicit:

- **Where the alert at 50 goes when the capped role is `edit`.** Alerts normally go to edit. An edit
  agent that is down keeps its window, passes the consumer check and can fill to the cap — at which
  point a message *about* the cap is itself refused. A lifecycle row is unaffected.
- **The cap applies to every message type.** Decision 1 says "a new send is refused", so a response
  or a daemon event past 50 is refused like a request, and a reply to a tracked request can be lost to
  the cap. That is the decision as written; it is recorded here so the consequence is not a surprise.

### Scope boundary

In scope: notifying a role nothing can consume for, routing the workflow to it, and an inbox that
grows without bound. Not in scope: refusing the send itself
([MUX-145](./MUX-145-messages-routed-to-windowless-role.md), Decision 3); whether `analyze` *should*
be launched in this session (a configuration choice); removing hosted-window modes (the user's
planned change, not specified here); and the `injection-refused` guard itself — it is working
correctly, refusing an injection it cannot verify.

## Requirements

### Acceptance criteria

Rewritten 2026-11-15 to Decisions 1–3. "No consumer" means no window, no live inbox listener and no
headless agent running for the role.

- [x] A role with no consumer is **not notified** — no trigger file, no injection attempt — and the attempt is not retried on a timer
- [x] The condition is reported **once** per role, not once per cycle — a configured-but-unlaunched role is a setup fact, not a recurring incident
- [x] **Negative control:** a role with no window whose own listener or headless agent is running is notified and receives normally — a window is not the test (Decision 3)
- [x] A role's inbox holds at most 50 unconsumed messages: one alert on reaching 50, and a send past it is refused with `NOT SENT`, exit 1 — nothing already queued is dropped (Decision 1)
- [x] **Negative control:** a role that *gains* a consumer later starts receiving normally, and its queued messages are delivered — the fix must not permanently blacklist a role
- [x] **Negative control:** a role with a window whose pane capture fails for a *real* reason still logs `injection-refused` — the signal is preserved, not suppressed
- [x] The workflow does not transition to a role with no consumer: `analyze-route` is skipped and logged once per role, and a role with a consumer still transitions (Decision 2)

### Technical approach

Ask the question before the attempt, not after: the consumer predicate upstream of
`trigger-<role>.notify` being written, so a role nothing can consume for is skipped cheaply and
silently after one report. The predicate is three existing answers joined — a window
(`RoleHasWindow`, `bus/agent_health.go`), a live listener (`IsPolling`, `bus/notify.go`) and a
running headless agent (`IsHarnessActive`, `bus/notify.go`) — and is shared with MUX-145's `Send`
refusal; an unreadable window list is indeterminate and reads as "has a consumer", so a tmux blip
silences nothing.

The cap is 50 and it refuses rather than drops (Decision 1): dropping silently trades one invisible
failure for another, while a refusal tells the sender at the moment it matters and the alert at 50
tells the operator once. The refusal returns through
[MUX-155](./MUX-155-send-dedup-keys-on-target-not-sender.md)'s `notSent` road.

**The load-bearing control is the second negative one.** A fix that suppresses `injection-refused` for
"roles that look absent" would also suppress the real case the guard exists for: a *dead agent's
window still present*, whose shell would execute an injected payload. Those two must stay
distinguishable — absent window vs. present window that fails capture.

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/notify.go` | `:432`, `:565`, `:1001` — `WindowForRole` target resolution; where the window check belongs |
| `tools/muxcode/bus/launcher.go` | `:113–116` — roles valid for opt-in/mode-cycled configs, i.e. legitimately windowless |
| `tools/muxcode/daemon/daemon.go` | the notify cycle and `analyze-route` workflow transition |
| `tools/muxcode/bus/inbox.go` | where an undeliverable backlog would be capped |

## Implementation

### Phase 1: Establish the precondition

- [x] Re-measure the notify cycle on the current tree (it was not re-measured at parking) and record whether it still behaves as observed
- [x] Add the consumer check upstream of the notify attempt — the shared predicate, reused from MUX-145 if that has shipped, built here if not; record where it belongs
- [x] `analyze-route`, and any other daemon-driven workflow transition, skips a role with no consumer and logs the skip once per role
- [x] Unit tests: no consumer → no notify, no trigger file, no transition, one report; windowed role → unchanged; windowless role with a live listener or headless agent → notified (negative control); unreadable window list → treated as having a consumer

### Results for Phase 1

Recorded 2026-11-16 from the worker's request; the diff to `bus/consumer.go`, `bus/notify.go` and
`daemon/daemon.go` and the re-measure log were read by plan. Uncommitted. Verified and ticked
2026-11-16 once all three had run after the last source edit (16:18:12): `./build.sh` exit 0 at
16:22:42, `./test.sh` exit 0 at 16:24:07, review at 16:26:25 with 0 must-fix, 0 should-fix, 0 nits
(`/tmp/muxcode-review-1794864251.txt`). Plan's own `docs/agent-bus.md` edit (16:22:26) preceded all
three.

**A caveat from review, kept here.** The worker's live check — 30 s of stranded messages, no trigger
file, no `injection-refused` — produced **zero** `notify-no-consumer` rows, so it did not show the
guard firing: nothing called `Notify` for the role in that window. The notify skip is proven by the
unit tests (`TestNotifySkipsRoleWithNoConsumer`), not live. The two criteria that depend on it — not
notified, and reported once — are left open for Phase 5 to show against a running daemon.

**Re-measure, on the tree before this phase's change** (`/tmp/mux180-p1-out.log`, run task
`1794863738-spawn-926e95e5-baee6a66`).

| Case | Measured |
|------|----------|
| Two edit-stabilization cycles, analyze with no consumer | 1 `analyzing` transition (the second cycle was already in that state), 1 `send-no-consumer` row, **0** `injection-refused`, no `trigger-analyze.notify`, analyze's inbox empty. The 2026-09-10 loop does not occur on this road any more |
| Three messages stranded in analyze's inbox (sent with the guard off; two were deduped as in-flight requests, so one landed), watched for 30 s | The trigger file was rewritten once and `injection-refused` gained one row, through `inbox-notify`. **The notify road still fires for a stranded inbox** — once in the 30 s watched, so the per-cycle cadence of 2026-09-10 was not reproduced, only that the attempt still happens |

**What was built.**

| Item | Result |
|------|--------|
| Where the check belongs | Inside `bus.Notify`, after `WindowForRole` and before the trigger file is written — one place that covers all 16 daemon call sites. A role nothing can read for returns `nil` at once: no trigger file, no injection attempt |
| Routes | `bus.RouteToRole(session, role, route)` (`bus/consumer.go`), called at the top of the daemon's analyze route, **before** `TransitionWorkflow` — so neither the transition nor the send happens. Per the worker, `analyze-route` is the only daemon transition that hands work to a role; the `reviewed` and `editing` transitions do not, and were left alone |
| Reporting | One lifecycle row per road per episode, through a shared `NoteNoConsumer` / `ClearNoConsumer`: `send-no-consumer` (MUX-145's), `notify-no-consumer` and `route-no-consumer`. Each has its own marker, `<event>-<role>.marker`; a consumer appearing clears it, so the next absence is reported again. This answers the activation question — three rows, one per road, not one shared report |
| A rename to know about | MUX-145's marker `no-consumer-<role>.marker` is now `send-no-consumer-<role>.marker`. A marker left under the old name by a running session is simply orphaned: the first refusal after the upgrade writes one more `send-no-consumer` row |
| Tests | `bus/notify_no_consumer_test.go`: `TestNotifySkipsRoleWithNoConsumer`, `TestNotifyStillNotifiesARoleWithAConsumer`, `TestRouteToRole` |
| A MUX-145 script changed | `scripts/test-windowless-routing.sh` section A now expects `route-no-consumer` and **no** `analyzing` transition where it used to expect the refused send; its `CLAUDE.md` entry was updated by the worker. Run after the change, run task `1794864132-spawn-926e95e5-24aa88fc`: 25 passed, 0 failed, floor 25 |
| Run | Run task `1794863924-spawn-926e95e5-00ed4526`: 10 passed, 0 failed |
| Docs touched now | `docs/agent-bus.md` `muxcode send`: the marker's new name and the two daemon roads (plan) — the paragraph named the old marker, so it could not wait for Phase 4 |

**One consequence for the analyze road.** With the route skipped up front, the daemon's analyze send
is never attempted for a role with no consumer, so that road now logs `route-no-consumer` and no
longer `send-no-consumer`. The latter still comes from any other sender.

### Phase 2: Bound the inbox

- [x] Refuse a send to a role whose inbox already holds 50 unconsumed messages: `NOT SENT`, exit 1, nothing queued is dropped
- [x] Alert once when a role's inbox reaches 50, not again while it stays there
- [x] The queue is delivered once a consumer drains it, and sends are accepted again below the cap
- [x] **Negative control:** a healthy role's inbox is unaffected by the cap in normal operation

### Results for Phase 2

Recorded 2026-11-16 from the worker's request; `bus/inbox_cap.go` and its call sites were read by
plan. Uncommitted. Verified and ticked 2026-11-16 once all three had run after the last edit
(17:06:38): `./build.sh` exit 0 at 17:10:39, `./test.sh` exit 0 at 17:12:17, review at 17:13:02 with
0 must-fix, 0 should-fix, 0 nits (`/tmp/muxcode-review-1794867141.txt`) — its third pass. The graph's
phase report still described the first cut (`refuseIfInboxFull`, the startup exemption); what was
verified is the tree after both review fixes, as the two "review fix" tables below describe. The
tables are in the order the work happened: the first is the first cut, and each later one overrides
it where they differ.

| Item | Result |
|------|--------|
| The cap | `InboxCap = 50` (`bus/inbox_cap.go`), a constant — there is no variable to change it |
| The refusal | `refuseIfInboxFull`, called in `sendMessage` (`bus/inbox.go:351`) on the resolved inbox role, counting unconsumed messages with `InboxCount`. At 50 or more it returns `ErrInboxFull`; `cmd/send.go` maps that to `NOT SENT`, exit 1. Nothing queued is touched. It applies to **every message type**, as the activation note said the decision implied — a reply to a tracked request can meet it too. The startup self-send is exempt, so a full inbox never blocks an agent's own launch |
| The alert | `noteInboxAtCap` runs after a delivery, so the alert comes on **reaching** 50, before any send is refused. One `inbox-full` lifecycle row per episode, and one `inbox-full` event to edit naming the role and `muxcode diagnose <role>` |
| The episode | Any send that finds the inbox below 50 ends it, so a second fill alerts again |
| Tests | `bus/inbox_cap_test.go`: `TestInboxCapRefusesPastFifty`, `TestInboxCapEditAlertsItselfByRowOnly`, `TestInboxCapControls` |
| Run | Run task `1794864769-spawn-926e95e5-167b1cf3`: 8 passed, 0 failed |

**Review fixes, same phase — these supersede the rows above where they differ.** Recorded 2026-11-16
from the worker's second request; `bus/inbox_cap.go` was re-read by plan.

| Change | Detail |
|--------|--------|
| One road into an inbox | `appendCapped` is now the only write, for the addressee's inbox and for the auto-CC copy alike (`bus/inbox.go:403`, `:410`). `refuseIfInboxFull` and `noteInboxAtCap` are gone |
| The race | Count, refuse-or-append and the episode update run under a per-inbox lock (`lock/inbox-cap-<role>.lock`), so concurrent writers in any process cannot take an inbox past 50. The alert is raised after the lock is released, because the alert to edit is itself a send that takes edit's lock. If the lock cannot be taken the count and append still run, unserialized |
| **The startup exemption is removed** | The first cut let an agent's own startup message through a full inbox. Now it is refused like anything else; the queue is intact and the agent drains it once it is up. This reverses the "startup self-send is exempt" statement in the table above |
| Auto-CC at the cap | Still not delivered when edit's inbox is full, and still not reported to the sender — `sendMessage` swallows `ErrInboxFull` from the CC append. What changed: the CC road now goes through `appendCapped`, so a CC that fills or meets edit's cap raises the `inbox-full` episode (the lifecycle row) like a direct send. The drop is logged once per episode, not silent |
| `shouldAutoCC` | Guarded by a mutex |
| Tests | Added `TestInboxCapAutoCCFillsEditAndAlerts`, `TestInboxCapHoldsUnderConcurrency`, `TestInboxCapHoldsTheLock`; the startup control now asserts full → refused and below the cap → lands |
| Run | Run task `1794865285-spawn-926e95e5-d3b97aa3`: 10 passed, 0 failed; three reverted copies each fail their own test |

**Second review fix, same phase.** Recorded 2026-11-16 from the worker's third request;
`appendUnderCap` was re-read by plan.

| Change | Detail |
|--------|--------|
| Partial consume is coordinated | `receiveMatching` and `ConsumeByID` — which take some messages out of an inbox and write the rest back — now hold the same per-inbox capacity lock across take-and-restore, so a send cannot land between the two and be overwritten or miscounted |
| **The lock now fails closed** | If the capacity lock cannot be taken, a send writes nothing and returns the lock error; a partial consume likewise does nothing. This reverses the first review fix's "the count and append still run, unserialized" recorded above. The consequence: a lock that cannot be opened — a full or read-only state directory, a permissions fault — now stops **every** send to that inbox, where before it only weakened the cap. The error is not `ErrInboxFull`, so the CLI reports it as a send error, not `NOT SENT` |
| Episodes | An append that finds the inbox below 50 ends the `inbox-full` episode before writing, so 50 → one consumed → 50 again is a new episode with its own row and alert. The row is written under the lock (`openInboxFullEpisode`); only the alert to edit is sent after it is released |
| Tests | Added `TestInboxCapPartialConsumeIsCoordinated`, `TestInboxCapFailsClosedWithoutTheLock`, `TestInboxCapEpisodeResetsAfterOneConsumed` — nine tests in `bus/inbox_cap_test.go` now |
| Run | Run task `1794866800-spawn-926e95e5-b69d7cf6`: 9 passed, 0 failed; three reverted copies each fail only their own test. An earlier run (`1794866466…`) was 7 passed, 2 failed, from a fault in the test setup |

**Two choices the worker made where the decision was silent — for the user to see.** Plan raised
both at activation as things Phase 2 had to state; these are the statements.

| Point | What was built | The consequence |
|-------|----------------|-----------------|
| The alert when the full inbox is `edit`'s own | The lifecycle row only; no message to edit, since it would be refused by the cap it reports | If edit is the role that is down with a full inbox, nothing tells a person except the lifecycle log and `muxcode status` / `diagnose`. That is the case most likely to matter: edit is resume-only and can sit dead behind its window while the other agents keep reporting to it |
| Auto-CC while edit is at its cap | The CC copy to edit is **skipped**; the original message is still delivered to its addressee (`bus/inbox.go:412`) | Not in Decision 1, which says a send past the cap is refused and the sender told. Here the sender is not told: its message went through and reads as sent, and edit's copy is not made. It keeps CC from breaching the bound. Since the review fix the drop is no longer invisible — it raises the `inbox-full` row once per episode — but it is still a drop where Decision 1 chose refusal, and the individual copies lost are not listed anywhere |

### Phase 3: Preserve the guard's signal

- [x] `injection-refused` still fires for a **present** window whose capture fails
- [x] Test distinguishing absent-window from present-but-failing-capture

### Results for Phase 3

Recorded 2026-11-16 from the worker's request. Plan confirmed the test exists
(`bus/notify_no_consumer_test.go:116`); the run's log was not among the proc logs, so the counts and
the live result are the worker's report. No production code changed in this phase — the guard already
behaved this way after Phase 1; the phase adds the proof. Uncommitted. Verified and ticked
2026-11-16 once all three had run after the last edit (17:14:25): `./build.sh` exit 0 at 17:18:25,
`./test.sh` exit 0 at 17:20:00, review at 17:20:42 with 0 must-fix, 0 should-fix, 0 nits.

| Item | Result |
|------|--------|
| The test | `TestInjectionRefusedSurvivesForAPresentWindow`. **Absent window:** skipped at `Notify` — one `notify-no-consumer` row, no `injection-refused`. **Present window whose capture fails:** passes the consumer check, the wake-up reaches the injection guard, and `injection-refused` is logged with "pane capture failed" |
| A limit of the unit test | The present-window case calls `notifySendKeys` directly, not `Notify`: `Notify`'s `tmux has-session` guard cannot be stubbed. So the unit test proves the guard still fires once reached, not that `Notify` reaches it |
| The live check, which covers that | A real daemon on a private tmux server. An `analyze` window that exists but has no agent pane, holding a stranded message → `injection-refused`. No `analyze` window → `notify-no-consumer` and no `injection-refused` |
| What the live check also settles | Review's Phase 1 caveat was that no live run had shown the notify guard firing (zero `notify-no-consumer` rows). This one does, for the absent-window case, per the worker |
| Run | Run task `1794867284-spawn-926e95e5-c29a97a1`: 6 passed, 0 failed; with `Notify` made to skip every role, the present-window case fails |

### Phase 4: Docs

- [x] [`docs/architecture.md`](../../architecture.md) notification section and [`docs/agents.md`](../../agents.md): a configured role nothing can consume for is a supported state and is skipped, not retried; the test is a consumer, not a window
- [x] [`docs/agent-bus.md`](../../agent-bus.md): the inbox cap as a row in `muxcode send`'s "A message that was not written says so" table

### Results for Phase 4

Written 2026-11-16. Verified and ticked the same day once all three had run after the last doc edit
(17:22:05): `./build.sh` exit 0 at 17:22:56, `./test.sh` exit 0 at 17:23:15, review at 17:23:44 with
0 must-fix, 0 should-fix, 1 nit (`/tmp/muxcode-review-1794867800.txt`). The nit: `docs/agent-bus.md`'s
no-consumer paragraph, written for MUX-145, said a startup self-send "is never refused", which the
cap made untrue. Corrected after the review — it now says exempt from the consumer check, not from
the cap.

| Doc | What was added |
|-----|----------------|
| `CLAUDE.md` | Agent-notifications bullet (the worker's edit) |
| `docs/architecture.md` | Notification Flow: a "no consumer" branch at the head of step 4, and a new subsection, "A role nothing can read for" — the supported state, the consumer test, the three roads that ask it with their rows, the kept `injection-refused` signal, and the inbox cap's mechanism (plan) |
| `docs/agents.md` | Built-in Roles: a paragraph under the table — a role can be configured without being launched, what the bus does about it, and how to make it receive (plan) |
| `docs/agent-bus.md` | `muxcode send`: the inbox-cap row in the "A message that was not written says so" table. The notify and route roads were added to the "No consumer, no send" paragraph in Phase 1 (plan) |

The docs describe the cap as built, including the two choices still with the user — the row-only
alert for edit's own full inbox, and the auto-CC copy not made while edit is at its cap. If either is
reversed, the last paragraph of the new architecture subsection changes with it.

### Phase 5: Integration test

- [x] `scripts/test-windowless-notify.sh`: configure a role with no consumer, run the daemon, assert **zero** repeat refusals, no trigger file, no `analyzing` transition and one report
- [x] **Negative control:** create the window; assert delivery resumes and the backlog drains
- [x] **Negative control:** a windowless role with its own listener running is notified and consumes
- [x] Cap: fill a role's inbox to 50 → one alert; the 51st send prints `NOT SENT` and exits 1; all 50 are still queued
- [x] Coverage floor pinned to the exact pass count
- [x] Run through the run agent (**foreground**, per [MUX-171](./MUX-171-stall-watchdog-redrive-kills-busy-claude-tool.md)) and record counts here

### Results for Phase 5

Recorded 2026-11-16 from the worker's request; the script's header was read by plan. The run was in
the foreground, as the step requires, so it left no proc log; the run agent's own history row
confirms it (exit 0, "10 passed, 0 failed (floor 10)", log `/tmp/windowless-notify-out.log`), and
review read that log. Uncommitted. Verified and ticked 2026-11-16 once all three had run after the
last edit (17:27:13): `./build.sh` exit 0 at 17:27:54, `./test.sh` exit 0 at 17:28:12, review at
17:28:53 with 0 must-fix, 0 should-fix, 0 nits (`/tmp/muxcode-review-1794868096.txt`).

| Item | Result |
|------|--------|
| The script | `scripts/test-windowless-notify.sh` — muxcode built from the tree, scratch HOME, state, lifecycle log, memory and config, a scratch daemon on a private tmux server, `analyze` on OpenCode (the road that injects). `MUXCODE_CONSUMER_GUARD` is unset except on the two lines that strand a backlog on purpose. About 1.5 minutes |
| A — no consumer | A stranded backlog (one request and two events, sent with the guard off) and two edit-stabilization cycles over about 30 s: **zero** `injection-refused` rows, no `trigger-analyze.notify`, no `analyzing` transition, and exactly one `notify-no-consumer` and one `route-no-consumer` row |
| B — negative control, recovery | The window appears with a pane that reads analyze's inbox: the backlog drains, receipted by analyze, and the next cycle routes again — an `analyzing` transition, the trigger written, the event consumed. Nothing is blacklisted |
| C — negative control | No window but analyze's own `inbox --poll --loop` listener: an edit cycle routes, the trigger is written and the listener consumes the event |
| D — the cap | 50 sends to build land with one `inbox-full` row and one alert to edit; the 51st prints `NOT SENT` and exits 1; all 50 are still queued |
| Run 1 | Run task `1794867916…`: 9 passed, 1 failed — the scenario had stranded only events, and the daemon does not notify for an inbox holding events alone, so no `notify-no-consumer` row appeared. Fixed by stranding a request too |
| Run 2 | Run task `1794867980-spawn-926e95e5-79bed719`: 10 passed, 0 failed, floor 10 (the exact pass count), exit 0 |
| What it adds over `test-windowless-routing.sh` | The notify road, the route skip under a running daemon, recovery when a consumer appears, and the cap. The send refusal and its single row stay with MUX-145's script |
| Docs | `CLAUDE.md` integration-test entry `test-windowless-notify` (the worker) |

**What run 1 taught, worth keeping.** The daemon's wake-up only fires for an inbox holding a
**request**; events alone are not notified for. So the 2026-09-10 loop — 109 messages, 164 refusals —
needed at least one actionable message in the pile, and an inbox of pure daemon events with no
consumer was never the noisy case. It also explains Phase 1's live sample showing no
`notify-no-consumer` row.

**Not covered by section D:** the cap's concurrency, the partial-consume coordination, the fail-closed
lock and the auto-CC bound are unit-tested only (`bus/inbox_cap_test.go`).

## Notes

**It ran for 28 hours without being noticed**, because each individual row is a plausible transient —
the cost is only visible in aggregate (164 refusals, 109 messages). A once-per-role report would have
made the same condition obvious on day one, which is why the second acceptance criterion is about
*reporting cadence* rather than volume.

**Parked 2026-11-14 by plan**, at `55-defect-backlog-loop` selection (run
`1794626267-55-defect-backlog-loop-15547e2e`), under the rule that a spec holding an unrecorded
design decision is not worked unattended. The tree was not re-measured at parking; whether the
notify cycle still behaves as observed is the first thing to check when this is taken up.

The three decisions it was parked on — the cap's overflow behaviour, workflow routing, and the
division with MUX-145 — were answered by the user on 2026-11-15 and are recorded under
[Decisions](#decisions--the-users-recorded-2026-11-15). **Unparked 2026-11-15.**

**Related:** [MUX-145](./MUX-145-messages-routed-to-windowless-role.md) (the `Send`-time refusal and
the diagnose and reload halves of the same condition); [MUX-156](./MUX-156-orphaned-inbox-listener-consumes-into-the-void.md) (the other way a
role's inbox stops being consumed — there a rogue consumer, here no consumer at all);
[MUX-164](./MUX-164-codex-trust-prompt-reads-idle-wakeup-into-shell.md) (the guard whose
signal this noise buries).

## Status

**Complete** — activated and closed 2026-11-16 by `55-defect-backlog-loop` run `1794626267-55-defect-backlog-loop-15547e2e` (Defects rank 14 then; ranks 1–13 were parked). 25/25: Phases 1–5 complete and all seven criteria met. Uncommitted at close-out; the run's ship step commits it. **Closed with two questions unanswered** — see the end of this paragraph; a reversal of either after shipping is a follow-up defect, not a reopening. Edit acknowledged this at 17:30 (`1794868229-edit-8e272b73`): the spec ships under the user's standing approval rule and the two choices stay as built unless the user reverses them. That is edit's statement of the standing rule, not the user's answer to the two questions. **Awaiting the user:** two of the cap's choices (Results for Phase 2) — a row-only alert when edit's own inbox is full, and the auto-CC copy skipped while edit is at its cap. MUX-145 shipped the same day and removed the headline symptom; see Findings at activation for what remains — the workflow transition, `Notify`'s missing consumer check, and the cap.

History: filed 2026-09-11. Parked 2026-11-14; unparked 2026-11-15 once the user's Decisions 1–3 were recorded and the criteria and phases rewritten to them (19 items became 25).
