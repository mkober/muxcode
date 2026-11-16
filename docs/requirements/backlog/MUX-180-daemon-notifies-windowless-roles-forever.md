# The Daemon Notifies a Windowless Role Forever, and Its Inbox Grows Unbounded

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
| **Noise** | a `warn` row every ~51 s, indefinitely — it drowns the signal that `injection-refused` is meant to carry (a *dead agent's shell* about to receive a payload, [MUX-164](../completed/MUX-164-codex-trust-prompt-reads-idle-wakeup-into-shell.md)) |
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

- [ ] A role with no consumer is **not notified** — no trigger file, no injection attempt — and the attempt is not retried on a timer
- [ ] The condition is reported **once** per role, not once per cycle — a configured-but-unlaunched role is a setup fact, not a recurring incident
- [ ] **Negative control:** a role with no window whose own listener or headless agent is running is notified and receives normally — a window is not the test (Decision 3)
- [ ] A role's inbox holds at most 50 unconsumed messages: one alert on reaching 50, and a send past it is refused with `NOT SENT`, exit 1 — nothing already queued is dropped (Decision 1)
- [ ] **Negative control:** a role that *gains* a consumer later starts receiving normally, and its queued messages are delivered — the fix must not permanently blacklist a role
- [ ] **Negative control:** a role with a window whose pane capture fails for a *real* reason still logs `injection-refused` — the signal is preserved, not suppressed
- [ ] The workflow does not transition to a role with no consumer: `analyze-route` is skipped and logged once per role, and a role with a consumer still transitions (Decision 2)

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
[MUX-155](../completed/MUX-155-send-dedup-keys-on-target-not-sender.md)'s `notSent` road.

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

- [ ] Re-measure the notify cycle on the current tree (it was not re-measured at parking) and record whether it still behaves as observed
- [ ] Add the consumer check upstream of the notify attempt — the shared predicate, reused from MUX-145 if that has shipped, built here if not; record where it belongs
- [ ] `analyze-route`, and any other daemon-driven workflow transition, skips a role with no consumer and logs the skip once per role
- [ ] Unit tests: no consumer → no notify, no trigger file, no transition, one report; windowed role → unchanged; windowless role with a live listener or headless agent → notified (negative control); unreadable window list → treated as having a consumer

### Phase 2: Bound the inbox

- [ ] Refuse a send to a role whose inbox already holds 50 unconsumed messages: `NOT SENT`, exit 1, nothing queued is dropped
- [ ] Alert once when a role's inbox reaches 50, not again while it stays there
- [ ] The queue is delivered once a consumer drains it, and sends are accepted again below the cap
- [ ] **Negative control:** a healthy role's inbox is unaffected by the cap in normal operation

### Phase 3: Preserve the guard's signal

- [ ] `injection-refused` still fires for a **present** window whose capture fails
- [ ] Test distinguishing absent-window from present-but-failing-capture

### Phase 4: Docs

- [ ] [`docs/architecture.md`](../../architecture.md) notification section and [`docs/agents.md`](../../agents.md): a configured role nothing can consume for is a supported state and is skipped, not retried; the test is a consumer, not a window
- [ ] [`docs/agent-bus.md`](../../agent-bus.md): the inbox cap as a row in `muxcode send`'s "A message that was not written says so" table

### Phase 5: Integration test

- [ ] `scripts/test-windowless-notify.sh`: configure a role with no consumer, run the daemon, assert **zero** repeat refusals, no trigger file, no `analyzing` transition and one report
- [ ] **Negative control:** create the window; assert delivery resumes and the backlog drains
- [ ] **Negative control:** a windowless role with its own listener running is notified and consumes
- [ ] Cap: fill a role's inbox to 50 → one alert; the 51st send prints `NOT SENT` and exits 1; all 50 are still queued
- [ ] Coverage floor pinned to the exact pass count
- [ ] Run through the run agent (**foreground**, per [MUX-171](../completed/MUX-171-stall-watchdog-redrive-kills-busy-claude-tool.md)) and record counts here

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
the diagnose and reload halves of the same condition); [MUX-156](../completed/MUX-156-orphaned-inbox-listener-consumes-into-the-void.md) (the other way a
role's inbox stops being consumed — there a rogue consumer, here no consumer at all);
[MUX-164](../completed/MUX-164-codex-trust-prompt-reads-idle-wakeup-into-shell.md) (the guard whose
signal this noise buries).

## Status

**Backlog** — 0/25. Filed 2026-09-11. Parked 2026-11-14; unparked 2026-11-15 once the user's Decisions 1–3 were recorded and the criteria and phases rewritten to them (19 items became 25).
