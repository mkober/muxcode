# Messages Route to a Role With No Window, and Diagnose Prescribes an Impossible Fix

The daemon routed 11 analyze events over 4.6 hours to a role that **has no window in the session**, so
nothing could ever consume them. `muxcode diagnose` then reported the pile as a critical `receipt-gap`
and prescribed `muxcode deliver analyze --force` — a remediation that targets a pane which does not
exist.

Two defects, filed together because the second is what makes the first hard to see: the pile looks
like a delivery failure, so the operator is sent to fix delivery, and the actual cause (no consumer
exists) is never surfaced.

A third, added 2026-09-08 from a second live incident: **reloading** the same windowless role fails
with *"did not exit after 12 seconds"* — a hung process that never existed — because the reload path
reads the same fail-safe liveness signal and has the same blind spot. Three paths, one root:
`KnownRoles` is a superset of the launched windows, and `IsAgentAlive` cannot say "no" for a role that
was never launched.

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

### Defect C — the reload path has the same blind spot (2026-09-08)

Observed live 2026-09-08 on session `muxcode`: the Provider Selector was used to reload `analyze` onto
codex/gpt-5.6-sol. It failed with:

```
stop agent: agent analyze did not exit after 12 seconds
```

Nothing failed to exit. `analyze` has no window, no pane and no process in the session — the same
absence Defects A and B are about, met on a third path. Reported by edit from the selector; the
supporting facts were re-verified from this role before filing.

| Fact | How established |
|------|-----------------|
| Session windows: `plan edit build test serve review deploy run watch commit` — no `analyze` | **Verified** — `tmux list-windows -t muxcode` |
| `analyze` is in `KnownRoles` but absent from `DefaultLauncherConfig().Windows`, so this is reachable in **every default session** | **Verified** — `bus/config.go:15-19`, `bus/launcher.go:35` |
| A failed reload writes **no lifecycle row** — the only `LogLifecycle` in `reload.go` is `agent-reload` (`:378`), after a successful relaunch | **Verified** — the 12h log carries nothing for the attempt; the error text is edit's report |
| The same error string already misled once, with a different cause — the OpenCode C-c pairing bug that `pairedInterrupt` fixed | **Verified** — `bus/reload.go:84-91` |

#### Mechanism — five swallowed signals

Line numbers are at `HEAD` (`18bf967`); the tree carries a proposed fix, below.

| Step | Site | Behaviour |
|------|------|-----------|
| 1 | `ReloadableRoles()` — `bus/reload_batch.go:87-89` | Walks `KnownRoles` with no window check, so the selector offers `analyze` |
| 2 | `ActiveAgentStatuses()` — `:50` | `Alive` comes from `IsAgentAlive`, which fail-safes to **true** when it cannot capture a pane |
| 3 | `pairedInterrupt()` — `bus/reload.go:95-98` | `exec.Command(...).Run()` discards tmux's `can't find window: analyze` |
| 4 | `IsAgentAlive` | Cannot capture the pane, reports "alive" — the fail-safe that `RoleHasWindow`'s own doc comment warns about (`bus/agent_health.go:54-59`) |
| 5 | Stop poll — `:147-173` | 20 × 500ms polls plus the fallback waits, on a signal that can never go false → "did not exit after 12 seconds" |

The error names a hung process that never existed and sends the operator to hunt a crash instead of an
unlaunched role — the same false specificity as Defect B, on a third path. That the string has now
covered two unrelated faults is its own small finding: "did not exit" is a catch-all for *any* way the
stop poll can time out.

#### Diagnose is falsely clean here, not falsely specific

With `analyze`'s inbox **empty** (edit had drained it), `muxcode diagnose analyze` reported:

```
State: active   Health: alive
Inbox: 0 message(s)
No issues detected
```

— for a role with no window and no process. Defect B covers the windowless role that *holds* messages
(`receipt-gap`). With nothing in the inbox nothing trips at all, and `checkUnexplainedEvidence` cannot
catch it because it keys on a stuck inbox. This is the MUX-006 shape (falsely clean) rather than the
Defect B shape (falsely specific), reached from the same missing check. Reported by edit from the
drained state; by the time plan re-ran it (13:56) the inbox held **three fresh `daemon→analyze`
triggers accumulated since 13:50** and the verdict was `receipt-gap` again — Defect A reproducing in
the relaunched session, which is the state the empty-inbox verdict sits between.

#### Proposed fix on the `MUX-144` branch — **UNVALIDATED**

Present in the working tree at filing, **not built, tested or reviewed** (edit reports the build agent
accepting requests and returning nothing). Recorded so the Phase 5 boxes describe what was written;
they stay open until a verify-spec pass confirms it.

| File | Change |
|------|--------|
| `bus/reload.go` | New `RoleWindowMissing()`; `ReloadAgent` refuses a windowless role before writing the reload marker, with an error naming the absence. An unreadable window list is **indeterminate**, not empty — a tmux failure must not refuse every reload |
| `bus/reload_batch.go` | New `AgentReloadStatus.Windowless`; the window list is read **once** per sweep; `alive := !windowless && IsAgentAlive(...)` so a windowless role never reports `Alive` |
| `tui/provider_select.go` | New `notAliveLabel()` renders `(no window)` instead of `(dead)`; `isSelectable` already gates on `!Alive`, so the role is listed but cannot be checked |
| `bus/reload_windowless_test.go` | New — `TestRoleWindowMissing` (4 cases: windowless refused, windowed passes, unreadable list indeterminate, mode-cycled `research` resolves via its host window), `TestActiveAgentStatusesMarksWindowlessRoles`, `TestActiveAgentStatusesIndeterminateWhenTmuxUnavailable` |

Scope note: this touches the reload path **only**. Defect A's emit guard and Defect B's diagnose check
are untouched by it, and `notified-analyze.ids` in the bus dir (164 IDs at 13:56) shows the daemon
still notifying the phantom role every cycle.

## Requirements

### Acceptance criteria

- [ ] A message is never routed to a role that has no window in the session, on the analyze path
- [ ] The suppression is visible — a windowless route emits a lifecycle event rather than failing silently
- [ ] `diagnose` distinguishes "no consumer exists" from "consumer exists but did not receive", and
      never prescribes `deliver --force` for a role with no pane
- [ ] The windowless finding names the actual remediation (open the window, or stop routing to the role)
- [ ] `muxcode status` makes a windowless role distinguishable from a windowed idle one
- [ ] Draining a windowless inbox by hand is no longer required — the pile does not re-accumulate
- [ ] A reload of a role with no window fails **immediately** with an error naming the absence — never
      "did not exit"
- [ ] The provider selector does not offer a windowless role as a reload target, and labels it
      distinctly from a dead one
- [ ] `diagnose` on a windowless role with an **empty** inbox does not report "No issues detected"

### Technical approach

Defect A is a guard at the emit site using the helper that already exists. The open design question
is what "no window" should mean: **suppress the send**, or **send but skip the notify**. Suppressing
is correct here — an inbox nobody reads is not storage, it is a leak — but the analyze payload is the
only consumer of the edit-stabilization signal, so suppressing it silently discards the signal. Hence
the lifecycle-event criterion: the suppression must be observable.

Defect B is an ordering fix in `diagnosticChecks`. A windowless check must run **before**
`receipt-gap`, because the receipt evidence is genuinely present and will match otherwise. The check
must not depend on the inbox: an empty inbox on a windowless role is the falsely-clean case, not a
healthy one.

Defect C is the same guard at the reload entry, with the same helper. The one subtlety is the failure
mode of the window read itself: `TmuxListWindowNames` failing must be **indeterminate** (proceed as
today), never "no windows exist" (refuse every reload on the session). All three defects should
consult `RoleHasWindow` rather than `IsAgentAlive` — the former can say "no", the latter cannot.

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/daemon/daemon.go` | Analyze route emit (`:661-673`) — needs the window guard |
| `tools/muxcode/bus/diagnose.go` | Uses neither helper; needs a windowless check ordered before `receipt-gap` |
| `tools/muxcode/bus/agent_health.go` | `RoleHasWindow()` `:67`, `IsAgentHealthExcluded()` `:44` — both already exist |
| `tools/muxcode/bus/inspect.go` | `GetAllAgentStatus()` — status listing that hides the distinction |
| `tools/muxcode/bus/reload.go` | `ReloadAgent` / stop sequence — `pairedInterrupt` (`:95`) swallows the missing-window error, stop poll (`:147-173`) cannot terminate; a failed reload logs nothing |
| `tools/muxcode/bus/reload_batch.go` | `ReloadableRoles()` (`:87`) walks `KnownRoles`; `ActiveAgentStatuses()` (`:50`) reads the fail-safe `Alive` |
| `tools/muxcode/tui/provider_select.go` | Selector rendering — one `(dead)` label for everything not alive |
| `tools/muxcode/bus/launcher.go` | `DefaultLauncherConfig().Windows` (`:35`) — the launched set that `KnownRoles` exceeds |

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
- [ ] A windowless role with an **empty** inbox is reported as windowless, never "No issues detected"
      (Defect C's clean verdict — the check must not key on the inbox)

### Phase 4: Surface it in status

- [ ] `muxcode status` distinguishes a windowless role from a windowed idle one
- [ ] Verify `docs`→`plan` style hosted-role mappings are not misreported as windowless

### Phase 5: Refuse to reload a windowless role

A proposed implementation is in the tree (see Defect C) — **unvalidated**, so every box stays open
until a verify-spec pass confirms it built, tested and reviewed.

- [ ] `ReloadAgent` refuses a windowless role **before** writing the reload marker, with an error
      naming the absence and what would fix it — never "did not exit"
- [ ] An unreadable window list is indeterminate: the reload proceeds as today rather than refusing
      every role on the session
- [ ] `ActiveAgentStatuses` marks a windowless role and never reports it `Alive`; the window list is
      read once per sweep, not once per role
- [ ] The provider selector labels a windowless role `(no window)`, distinct from `(dead)`, and it is
      not selectable
- [ ] Negative control: a windowed role passes the check and reloads exactly as today
- [ ] Negative control: a mode-cycled role (`research`) resolves via its host window and is not
      marked windowless
- [ ] Unit tests cover each case above with its negative control (`bus/reload_windowless_test.go`)

### Phase 6: Integration test

- [ ] Create `scripts/test-windowless-routing.sh` (hermetic; scratch bus + tmux session + daemon)
- [ ] Test: session without an analyze window → edits stabilize → **no** message accumulates, and a
      suppression lifecycle event is written
- [ ] Test: session **with** an analyze window → message routes and is consumed (negative control —
      the guard cannot go inert)
- [ ] Test: `diagnose` on a windowless role reports the windowless finding, not `receipt-gap`
- [ ] Test: `diagnose` on a windowed role holding un-receipted messages still reports `receipt-gap`
- [ ] Test: reload a windowless role → fails fast with the error naming the absence, no reload marker
      written, no 12-second wait
- [ ] Test: reload a windowed role → still reloads (negative control — the refusal cannot go inert)
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

Defect C (2026-09-08) was root-caused by edit and handed over as `/tmp/mux-145-defect-c.md`; plan
re-verified the window list, the `KnownRoles`/`Windows` split and every cited line at `HEAD` before
folding it in, and added two facts the handoff did not carry — a failed reload logs nothing, and the
error string has a prior unrelated cause. The three defects share one root and should share one fix:
`RoleHasWindow` is the signal that can say "no", and the emit path, diagnose and reload each need to
consult it rather than `IsAgentAlive`.

## Status

**Backlog — 0/37.** Filed 2026-09-03 from a live incident the same morning; **Defect C added
2026-09-08** from a second live incident on the reload path, with three more acceptance criteria and
a new Phase 5.

Defects A and B are verified and unfixed — Defect A reproduced in the relaunched session on
2026-09-08 (three `daemon→analyze` triggers accumulated 13:50–13:56), so the emit path still refills
the inbox until Phase 2 lands. A fix for Defect C **only** is in the working tree on the `MUX-144`
branch, **unvalidated** (not built, tested or reviewed at filing); Phase 5's boxes stay open until a
verify-spec pass confirms it. No box is ticked.
