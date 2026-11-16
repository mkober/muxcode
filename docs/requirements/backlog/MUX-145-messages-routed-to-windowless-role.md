# Messages Route to a Role With No Window, and Diagnose Prescribes an Impossible Fix

**Tracking:** [mkober/muxcode#115](https://github.com/mkober/muxcode/issues/115)

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

#### Fix on the `MUX-144` branch — landed in `c6fd196` (14:20) and `cbb6e5d` (14:33), 2026-09-08

Filed at 13:56 as unvalidated; by 14:1x the suite was **2901 pass / 0 fail, exit 0, all six packages**
on the run agent (Claude, unsandboxed — read from its pane by plan; the codex test agent could not run
it, MUX-153). Between the two, a consolidation caught a live regression: the reload guard and the
selector each carried its own copy of the presence rule and only one gained the mode-role clause, so
the selector greyed `research` out as `(no window)` — `modeRoles` maps `research`/`auto` to
**themselves**, not to their host windows. `RoleWindowPresent()` (`reload.go`) is now the single
predicate used by both `RoleWindowMissing` and `ActiveAgentStatuses` (`reload_batch.go:98`). Its
mode-role clause moved twice in one afternoon: `cbb6e5d` resolved a mode role via its **host** window
(`research → plan`), which briefly shipped and was wrong — plan exists, so `research` read as present,
while `ReloadTarget` addressed the `research` **hold** window, which is not created until that mode is
first cycled to; the reload fired keystrokes at a missing window and failed with the very "did not
exit after 12 seconds" this predicate exists to prevent. `f889786` (14:47) decides presence on the
window a reload actually addresses — `RoleHasWindow(names, ReloadWindowForRole(role))`, the hold
window for a mode role — so a never-cycled mode role reads as **absent**: configurable, not
reloadable, exactly like a role missing from `MUXCODE_WINDOWS`. The fix sits on a branch named for another spec, so a
`git log` by prefix will not attribute it here (the `16f2027` shape again).

**Design change between filing and landing (edit, `c6fd196` → `cbb6e5d`): a windowless role is
configurable, not reloadable.** The first cut refused the reload outright. The landed version refuses
only the *relaunch* — `RoleWindowMissing` still guards `ReloadAgent` (`reload.go:334`) — and routes the
role to a **config-only apply**: `ConfigureWindowlessRole` (`reload_batch.go:51`) writes the
provider/model to **both** stores, because each alone was insufficient and "picking one was the
original bug" — the shell config survives the session but is never consulted by `ResolveProviderCLI`
(it reaches a role only by being sourced at launch, and is outranked by the value the session already
exported), while the runtime override takes effect now but dies with the bus dir. `ConfigOnlyRole`
(`:166`) makes the routing decision (an indeterminate window list takes the normal path, so a tmux
blip cannot turn a whole batch into config writes; the headless `prompt` role is excluded),
`ReloadBatch` skips the inter-agent gap for such roles, and the CLI gained the same branch —
`configureIfWindowless` (`cmd/reload.go:197`) — so `muxcode reload analyze --cli codex` and the modal
answer one request one way (the MUX-142 shape, pre-empted). The selector therefore keeps a windowless
role **selectable** (`isSelectable`, `provider_select.go:171`), labelled `(no window)`, and excludes it
only from select-all (`:579`). Suite after: **2908 pass / 0 fail** (`TestReloadBatchConfiguresWindowlessRole`
and `TestConfigOnlyRole` added). **Gap:** `configureIfWindowless` — the CLI road — has no test (edit,
14:3x); recorded as an open Phase 5 step. `8b5c360` (14:47) wraps the selector's failure rows to the
frame width so a long refusal is readable (`renderFailureRow`, `wrapWords`,
`tui/provider_select_wrap_test.go`).

| File | Change |
|------|--------|
| `bus/reload.go` | New `RoleWindowMissing()` (`:276`) — `ReloadAgent` refuses a windowless role before writing the reload marker, with an error naming the absence; an unreadable window list is **indeterminate**, not empty. New `RoleWindowPresent()` and `ReloadWindowForRole()` (the hold window for a mode role; replaced `ModeRoleHasHostWindow` in `f889786`) — the single presence predicate both call sites use |
| `bus/reload_batch.go` | New `AgentReloadStatus.Windowless`; the window list is read **once** per sweep; `windowless := windowsKnown && !RoleWindowPresent(...)` (`:98`) and `alive := !windowless && IsAgentAlive(...)` so a windowless role never reports `Alive`. New `ConfigureWindowlessRole()` (`:51`, both stores) and `ConfigOnlyRole()` (`:166`); `ReloadBatch` routes config-only roles to the former |
| `cmd/reload.go` | New `configureIfWindowless()` (`:197`) — the CLI road to the same config-only apply; `--cli`/`--model` required, otherwise an error naming the absence |
| `tui/provider_select.go` | New `notAliveLabel()` renders `(no window)` instead of `(dead)`; `isSelectable` (`:171`) admits a windowless role for a config-only apply while refusing a dead one; select-all skips it (`:579`) |
| `bus/reload_windowless_test.go` | New — `TestRoleWindowMissing` (4 cases: windowless refused, windowed passes, unreadable list indeterminate, mode-cycled `research` resolves via its host window), `TestActiveAgentStatusesMarksWindowlessRoles`, `TestActiveAgentStatusesIndeterminateWhenTmuxUnavailable` |

Scope note: this touches the reload path **only**. Defect A's emit guard and Defect B's diagnose check
are untouched by it, and `notified-analyze.ids` in the bus dir (164 IDs at 13:56) shows the daemon
still notifying the phantom role every cycle.

### Decisions — the user's, recorded 2026-11-15

Answered by the user in conversation and relayed by edit (`1794787239-edit-1aacf9a4`), as one
decision across this spec and
[MUX-180](./MUX-180-daemon-notifies-windowless-roles-forever.md). The open criteria and phases below
were rewritten to it the same day; the ticked reload items are untouched.

**Context the user gave, which changes this spec's vocabulary.** The feature that hosts several
agents in one window (research inside plan's window, for example) is going to be removed, and some
future agents may never have a window of their own. **Every agent always has its own inbox, window
or not.** So this spec's title condition — "no window" — is no longer the fault by itself.

| Decision | Chosen |
|----------|--------|
| Suppress the send, or send and skip the notify (the "open design question" under Technical approach) | **Refuse at `Send`.** The sender is told — `NOT SENT`, exit 1 — rather than the message landing in an inbox nobody reads |
| What the refusal tests | **"Can anything consume this role's inbox"** — no window **and** no listener **and** no headless agent running. Never "does it have a window": a windowless agent that reads its own inbox receives normally |
| The division with MUX-180 | **This spec owns the `Send`-time refusal** (plus its own diagnose, status and reload halves). **MUX-180 owns notify suppression, the inbox cap and workflow routing.** Cross-linked, not merged — confirmed by the user 2026-11-16, recorded in MUX-180 |

What the decision leaves for Phase 1 to establish, because a refusal at `Send` is wider than the one
analyze route this spec was filed on. **Confirmed by the user 2026-11-16** (relayed by edit,
`1794844857-edit-5f52a782`): Phase 1 enumerates the launch-time seeded sends so they are never
refused, and the refusal's lifecycle row is once per role. The user also noted that Phase 1 re-pins
this spec's 2026-09-08 line numbers and mechanism against the tree.

| Question | Why it matters |
|----------|----------------|
| Which sends happen before a role's consumer is up | `LaunchSession` pre-populates inboxes and wakes agents at startup (the `request:startup` bootstrap). If a role has no window, listener or headless agent at that instant, the refusal would break session launch |
| What an unreadable window list reads as | Indeterminate — it must pass as "has a consumer", the rule `RoleWindowMissing` already follows, or a tmux blip refuses every send in the session |
| How often the refusal is logged | The daemon's own analyze route sends every edit-stabilization cycle. A row per refused message would recreate MUX-180's noise on a new event name, so the row is once per role while the condition holds |
| Whether the reload path's presence rule moves | `RoleWindowPresent` (Defect C, landed) is a *window* rule and is right for reload, which addresses a window. It is not the consumer predicate and is not replaced by it |

## Requirements

### Acceptance criteria

Open items rewritten 2026-11-15 to the user's decision. "No consumer" means no window, no live inbox
listener and no headless agent running for the role.

- [ ] A message to a role with no consumer is refused at `Send` — written to no inbox — and the
      sender reads `NOT SENT` with exit 1; the daemon's analyze route is refused the same way
- [ ] The refusal is visible — a lifecycle event names the role, once per role while the condition
      holds, rather than failing silently or once per message
- [ ] **Negative control:** a role with no window whose own listener or headless agent is running is
      sent to and receives normally — a window is not the test
- [ ] **Negative control:** session launch still seeds every role's startup message; no send made
      before a role's consumer is up is refused
- [ ] `diagnose` distinguishes "no consumer exists" from "consumer exists but did not receive", and
      never prescribes `deliver --force` for a role with no pane
- [ ] The no-consumer finding names the actual remediation (launch the role, or stop routing to it)
- [ ] `muxcode status` makes a role with no consumer distinguishable from an idle one that has one
- [ ] Draining such an inbox by hand is no longer required — the pile does not re-accumulate
- [x] A reload of a role with no window fails **immediately** with an error naming the absence — never
      "did not exit" — `RoleWindowMissing` refuses before the reload marker is written
      (`TestRoleWindowMissing`, 2026-09-08, uncommitted)
- [x] The provider selector does not offer a windowless role as a reload target, and labels it
      distinctly from a dead one — it is offered as a **config** target instead: `Windowless` never
      reports `Alive`, `isSelectable` admits it for a config-only apply and refuses a dead one,
      `notAliveLabel` renders `(no window)` (`TestActiveAgentStatusesMarksWindowlessRoles`,
      `TestReloadBatchConfiguresWindowlessRole`)
- [ ] `diagnose` on a role with no consumer and an **empty** inbox does not report "No issues detected"

### Technical approach

Defect A is a refusal at `bus.Send` (decided 2026-11-15), not a guard at the one emit site: a role
nothing can consume for is refused whoever the sender is, and the analyze route is one caller among
them. The predicate joins three answers that already exist — a window (`RoleHasWindow`,
`bus/agent_health.go`), a live listener (`IsPolling`, `bus/notify.go`) and a running headless agent
(`IsHarnessActive`, `bus/notify.go`) — and is the one MUX-180's notify and workflow checks reuse.
The refusal returns through
[MUX-155](../completed/MUX-155-send-dedup-keys-on-target-not-sender.md)'s `notSent` road. An inbox
nobody reads is not storage, it is a leak — but the analyze payload is the only carrier of the
edit-stabilization signal, so refusing it discards the signal. Hence the lifecycle-event criterion:
the refusal must be observable.

Defect B is an ordering fix in `diagnosticChecks`. A no-consumer check must run **before**
`receipt-gap`, because the receipt evidence is genuinely present and will match otherwise. The check
must not depend on the inbox: an empty inbox on a role with no consumer is the falsely-clean case,
not a healthy one. A windowless role that reads its own inbox is healthy and must not trip it.

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
- [ ] Enumerate the sends made before a role's consumer is up (session launch, the startup bootstrap,
      a reload's relaunch window) and record which the refusal must let through

### Phase 2: Refuse at Send

- [ ] One consumer predicate — a window, a live listener or a running headless agent; an unreadable
      window list reads as having a consumer — placed where MUX-180 can reuse it
- [ ] `bus.Send` refuses a message to a role the predicate fails for, through `notSent`: `NOT SENT`,
      exit 1, nothing written to the inbox
- [ ] Emit a lifecycle event for the refusal, once per role while the condition holds
- [ ] Negative control: a session **with** an analyze window still routes normally
- [ ] Negative control: a windowless role with its own listener, and one with a running headless
      agent, are each sent to normally
- [ ] Negative control: the sends Phase 1 enumerated still land

### Phase 3: Teach diagnose the no-consumer case

- [ ] Add a no-consumer check ordered **before** `receipt-gap` in `diagnosticChecks`
- [ ] Remediation names launching the role or stopping the routing — never `deliver --force`
- [ ] Negative control: a windowed role with a genuine receipt gap still reports `receipt-gap`
      (the new check must not swallow the real one)
- [ ] A role with no consumer and an **empty** inbox is reported as such, never "No issues detected"
      (Defect C's clean verdict — the check must not key on the inbox)
- [ ] Negative control: a windowless role that reads its own inbox is not reported

### Phase 4: Surface it in status

- [ ] `muxcode status` distinguishes a role with no consumer from an idle one that has one
- [ ] Verify `docs`→`plan` style hosted-role mappings are not misreported as having no consumer

### Phase 5: Refuse to reload a windowless role

Implemented and validated 2026-09-08 (see Defect C) — landed in `c6fd196` + `cbb6e5d` + `f889786`;
suite 2915 green on the run agent. The design moved between the two commits: relaunch refused, configuration
applied.

- [x] `ReloadAgent` refuses a windowless role **before** writing the reload marker, with an error
      naming the absence and what would fix it — never "did not exit" (`reload.go:276`, called at the
      top of `ReloadAgent` after `IsKnownRole`)
- [x] An unreadable window list is indeterminate: the reload proceeds as today rather than refusing
      every role on the session (`TestRoleWindowMissing/unreadable window list is indeterminate`,
      `TestActiveAgentStatusesIndeterminateWhenTmuxUnavailable`)
- [x] `ActiveAgentStatuses` marks a windowless role and never reports it `Alive`; the window list is
      read once per sweep, not once per role (`reload_batch.go:87`)
- [x] The provider selector labels a windowless role `(no window)`, distinct from `(dead)` — and, by
      the landed design, **keeps it selectable for a config-only apply** (`isSelectable`,
      `provider_select.go:171`; select-all skips it, `:579`). *This step originally read "not
      selectable": the first cut (`c6fd196`) did that, the landed cut (`cbb6e5d`) deliberately does
      not — see the design-change note under Defect C*
- [x] Negative control: a windowed role passes the check and reloads exactly as today
      (`TestRoleWindowMissing/windowed role passes`)
- [x] Negative control: a mode-cycled role (`research`) is judged on the window a reload actually
      addresses — its **hold** window (`ReloadWindowForRole`, `f889786`): present once cycled to,
      otherwise windowless and configurable (`bus/reload_windowless_test.go`). *Originally worded
      "resolves via its host window and is not marked windowless"; that reading shipped in `cbb6e5d`
      and reproduced the 12-second failure on `research`, so the step and the code both moved — see
      Defect C*
- [x] Unit tests cover each case above with its negative control (`bus/reload_windowless_test.go` —
      six cases, plus `TestReloadBatchConfiguresWindowlessRole` and `TestConfigOnlyRole` for the
      config-only path)
- [ ] The CLI road is tested: `configureIfWindowless` (`cmd/reload.go:197`) has no test — the modal
      road is covered by `TestReloadBatchConfiguresWindowlessRole`, the CLI road by nothing (edit,
      2026-09-08 14:3x). Both roads must stay in step, or the MUX-142 shape returns

### Phase 6: Integration test

- [ ] Create `scripts/test-windowless-routing.sh` (hermetic; scratch bus + tmux session + daemon)
- [ ] Test: session with no consumer for analyze → edits stabilize → **no** message accumulates, one
      refusal lifecycle event is written, and a `muxcode send analyze …` prints `NOT SENT` and exits 1
- [ ] Test: session **with** an analyze window → message routes and is consumed (negative control —
      the guard cannot go inert)
- [ ] Test: a windowless role with its own listener running → the send lands and is consumed
      (negative control — a window is not the test)
- [ ] Test: `diagnose` on a role with no consumer reports that finding, not `receipt-gap`
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

**Backlog — 9/46, Phase 5 at 7/8. Unparked 2026-11-15** on the user's instruction relayed by edit,
with the decision that had been open under Technical approach recorded (refuse at `Send`, tested on
"can anything consume this inbox") and the open criteria and Phases 1–4 and 6 rewritten to it — 38
items became 46; none of the nine ticked items changed. Its line numbers and the Defect A and B
mechanism date from 2026-09-08 and were not re-read against the tree at unparking; Phase 1 pins them.

**Previously: parked 2026-09-08 22:10 at 9/38.** Moved back from `drafts/` on the user's
instruction: nothing moved since the afternoon, and Defects A (emit guard) and B (diagnose) plus
Phase 5's CLI-road test are open. The record below is as it stood when parked.

**Previously: In Progress.** Filed 2026-09-03 from a live incident the same morning;
**Defect C added 2026-09-08** from a second live incident on the reload path, with three more
acceptance criteria and a new Phase 5 — then **fixed and validated the same afternoon** (Phase 5 7/8,
the CLI road's test being the open step; two of its three acceptance criteria; suite 2908 pass / 0
fail on the run agent), landed in `c6fd196` + `cbb6e5d` + `f889786` on the `MUX-144` branch. The design changed
between filing and landing — windowless roles are **configurable, not reloadable** — and is recorded
under Defect C.

Defects A and B are verified and unfixed — Defect A reproduced in the relaunched session on
2026-09-08 (three `daemon→analyze` triggers accumulated 13:50–13:56), so the emit path still refills
the inbox until Phase 2 lands, and the empty-inbox diagnose verdict (Defect C's third criterion) is
Phase 3 work. **Moved from `backlog/` to `drafts/` 2026-09-08 15:08 on the user's approval** (relayed
by edit) — a filesystem move with no git command, so git sees a delete plus an untracked file until
the user next asks the commit agent to commit, at which point it stages as a rename. Cross-references
in `backlog.md` (rank and registry rows, plus an In-progress row), MUX-146 and MUX-150 were re-pointed
to `../drafts/`, and this file's own sibling link now reaches `../backlog/`.
