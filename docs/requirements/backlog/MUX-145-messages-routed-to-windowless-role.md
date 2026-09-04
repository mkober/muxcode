# Messages Route to a Role With No Window, and Diagnose Prescribes an Impossible Fix

The daemon routed 11 analyze events over 4.6 hours to a role that **has no window in the session**, so
nothing could ever consume them. `muxcode diagnose` then reported the pile as a critical `receipt-gap`
and prescribed `muxcode deliver analyze --force` — a remediation that targets a pane which does not
exist.

Two defects, filed together because the second is what makes the first hard to see: the pile looks
like a delivery failure, so the operator is sent to fix delivery, and the actual cause (no consumer
exists) is never surfaced.

Tracking: _(no GitHub issue yet)_

## Context

Observed live 2026-09-03 ~07:00 on session `muxcode` while checking agent state during startup.

| | |
|---|---|
| Role | `analyze` (opencode, non-hook provider) |
| Stranded | 11 messages, oldest 16,632s (**4h 37m**) |
| Payloads | all `daemon→analyze` file-change triggers — **0 actionable** |
| Session windows | 10: `plan edit build test serve review deploy run watch commit` — **no `analyze`** |
| Reported state | `active`, health `alive`, daemon alive and current |

`muxcode status` lists `analyze` as a role with an inbox count, which is what makes the absence easy
to miss — the role is present in every listing except the one that matters.

### Defect A — the emit path has no consumer check

`daemon/daemon.go:661-673` routes stabilized edits unconditionally:

```go
msg := bus.NewMessage("daemon", "analyze", "event", "analyze", analyzePayload, "")
if err := bus.Send(d.session, msg); err != nil { ... }
if err := bus.Notify(d.session, "analyze"); err != nil { ... }
```

`bus.Send` lands the message in `inbox/analyze.jsonl` permanently. `bus.Notify` then attempts a
send-keys to a pane that does not exist; its error is written to daemon stderr and otherwise
discarded. **The send is not gated on the role having a window**, so every edit-stabilization cycle
appends one more message to an inbox with no reader. Draining it by hand does not help — the next
edit refills it.

`bus.RoleHasWindow()` (`bus/agent_health.go:67`) already answers exactly this question and is not
called here.

### Defect B — diagnose reports a confidently wrong verdict

`bus/diagnose.go` calls **neither** `RoleHasWindow()` nor `IsAgentHealthExcluded()`
(`bus/agent_health.go:44`, `:67`) — verified by grep, both helpers exist and neither appears in the
file. So the windowless case is not in its model at all, and the evidence falls through to the
`receipt-gap` pattern, which produces:

> ❌ FINDING: 11 message(s) carry no delivery receipt — self-poll or delivery sidecar may be down (critical)
> Remediation: 1. Force-deliver pending inbox: `muxcode deliver analyze --force`

Every clause is locally true and the conclusion is wrong. There is no receipt because there is no
reader; the sidecar is not down because there is no sidecar. Following the remediation force-delivers
to a nonexistent pane.

This is a **stronger failure than the one `checkUnexplainedEvidence` was built to prevent.** That
backstop exists so diagnose never returns a clean bill of health over a wedged agent — an honest
"unexplained" beating a false clean. Here diagnose is not falsely clean but falsely *specific*: it
names a mechanism, rates it critical, and hands over a command that cannot work. A wrong diagnosis
with a confident remediation costs more operator trust than no diagnosis.

## Requirements

### Acceptance criteria

- [ ] A message is never routed to a role that has no window in the session, on the analyze path
- [ ] The suppression is visible — a windowless route emits a lifecycle event rather than failing silently
- [ ] `diagnose` distinguishes "no consumer exists" from "consumer exists but did not receive", and
      never prescribes `deliver --force` for a role with no pane
- [ ] The windowless finding names the actual remediation (open the window, or stop routing to the role)
- [ ] `muxcode status` makes a windowless role distinguishable from a windowed idle one
- [ ] Draining a windowless inbox by hand is no longer required — the pile does not re-accumulate

### Technical approach

Defect A is a guard at the emit site using the helper that already exists. The open design question
is what "no window" should mean: **suppress the send**, or **send but skip the notify**. Suppressing
is correct here — an inbox nobody reads is not storage, it is a leak — but the analyze payload is the
only consumer of the edit-stabilization signal, so suppressing it silently discards the signal. Hence
the lifecycle-event criterion: the suppression must be observable.

Defect B is an ordering fix in `diagnosticChecks`. A windowless check must run **before**
`receipt-gap`, because the receipt evidence is genuinely present and will match otherwise.

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/daemon/daemon.go` | Analyze route emit (`:661-673`) — needs the window guard |
| `tools/muxcode/bus/diagnose.go` | Uses neither helper; needs a windowless check ordered before `receipt-gap` |
| `tools/muxcode/bus/agent_health.go` | `RoleHasWindow()` `:67`, `IsAgentHealthExcluded()` `:44` — both already exist |
| `tools/muxcode/bus/inspect.go` | `GetAllAgentStatus()` — status listing that hides the distinction |

## Implementation

### Phase 1: Reproduce and pin

- [ ] Pin current behavior: a send to a windowless role lands in the inbox and is never consumed
- [ ] Pin that `diagnose` on that role returns `receipt-gap` with the `deliver --force` remediation
- [ ] Confirm `bus.Notify` to a nonexistent pane fails non-fatally (stderr only) — the silent half

### Phase 2: Guard the emit path

- [ ] Gate the analyze route on `bus.RoleHasWindow()`
- [ ] Emit a lifecycle event when a route is suppressed for want of a consumer
- [ ] Negative control: a session **with** an analyze window still routes normally

### Phase 3: Teach diagnose the windowless case

- [ ] Add a windowless-role check ordered **before** `receipt-gap` in `diagnosticChecks`
- [ ] Remediation names opening the window or stopping the routing — never `deliver --force`
- [ ] Negative control: a windowed role with a genuine receipt gap still reports `receipt-gap`
      (the new check must not swallow the real one)

### Phase 4: Surface it in status

- [ ] `muxcode status` distinguishes a windowless role from a windowed idle one
- [ ] Verify `docs`→`plan` style hosted-role mappings are not misreported as windowless

### Phase 5: Integration test

- [ ] Create `scripts/test-windowless-routing.sh` (hermetic; scratch bus + tmux session + daemon)
- [ ] Test: session without an analyze window → edits stabilize → **no** message accumulates, and a
      suppression lifecycle event is written
- [ ] Test: session **with** an analyze window → message routes and is consumed (negative control —
      the guard cannot go inert)
- [ ] Test: `diagnose` on a windowless role reports the windowless finding, not `receipt-gap`
- [ ] Test: `diagnose` on a windowed role holding un-receipted messages still reports `receipt-gap`
- [ ] Coverage floor keeps a skipped section from reporting green
- [ ] Run the script and verify all checks pass

## Notes

Root-caused by the edit agent during the live incident; both halves independently verified from this
role before filing (window list read from `tmux list-windows`, helper absence from grep, emit path
read at `daemon.go:645-680`). Edit drained the 11 messages by hand, which cleared the symptom for
this session only — the guard is what stops it returning.

Related: the diagnose half belongs to the same family as the `checkUnexplainedEvidence` invariant
described in [`CLAUDE.md`](../../../CLAUDE.md) — verdict honesty rather than pattern coverage. The
routing half is adjacent to, but distinct from,
[`MUX-127`](./MUX-127-review-completion-routing.md): that one routes to the wrong *recipient*, this
one routes to a recipient that does not exist.

## Status

**Backlog** — filed 2026-09-03 from a live incident the same morning. Not started. No phase work
begun; both defects are verified but unfixed, and the emit path will keep refilling the inbox until
Phase 2 lands.
