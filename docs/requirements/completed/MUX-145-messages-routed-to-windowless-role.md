# MUX-145: Messages Route to a Role With No Window, and Diagnose Prescribes an Impossible Fix

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

### Findings at activation (2026-11-16)

Re-read by plan against `b404e52`. Defects A and B are still as filed; what moved is line numbers and
two things the spec predates. The line references in the sections above are the 2026-09-08 ones;
Phase 1 pins behaviour, this table pins where it lives now.

| Spec says | Tree at `b404e52` |
|-----------|-------------------|
| Defect A: the analyze emit at `daemon/daemon.go:661-673` is unguarded | Still unguarded, now `:781-790`. The `StateAnalyzing` workflow transition fires just above it (`:775`), before the send — that transition is MUX-180's |
| `RoleHasWindow` `agent_health.go:67`, `IsAgentHealthExcluded` `:44` | `:96` and `:61` |
| Defect B: `bus/diagnose.go` calls neither helper | Still true — no `RoleHasWindow`, `IsAgentHealthExcluded`, `RoleWindowPresent` or `RoleWindowMissing` in the file |
| `diagnosticChecks` has no check for a role that is not there | [MUX-006](./MUX-006-diagnose-false-clean-verdict.md) has since added `checkAgentDown` (`diagnose.go:1127`), second in the order (`:554`). It keys on `AgentState.IsAlive`, the fail-safe signal this spec says cannot say "no" for a windowless role — and if it ever did read false there, it would prescribe a relaunch of a role with no window. Phase 1 pins which; Phase 3's check must be ordered against it as well as against `receipt-gap` |
| The refusal returns "through MUX-155's `notSent` road" | `notSent` is a CLI helper (`cmd/send.go:23`), not a `bus` function. `bus.Send` reports a refusal as a sentinel error (`ErrSendSuppressed`, `bus/inbox.go:22`) that the CLI turns into `NOT SENT`. The Phase 2 refusal needs its own sentinel on that road, so the daemon's emit site and the CLI each see it |
| The consumer predicate's three inputs | `RoleHasWindow` (`agent_health.go:96`), `IsPolling` (`notify.go:831` — false for an orphaned holder since MUX-156), `IsHarnessActive` (`notify.go:19`) |
| Reload-path sites (Defect C, landed) | `pairedInterrupt` `reload.go:181`; `RoleWindowMissing` `:364`; `RoleWindowPresent` `:389`; `ReloadWindowForRole` `:398`; `ConfigureWindowlessRole` `reload_batch.go:54`; `ActiveAgentStatuses` `:87`; `ReloadableRoles` `:140`; `ConfigOnlyRole` `:165`; `isSelectable` `provider_select.go:251`; `notAliveLabel` `:242` |
| Phase 5's open step: `configureIfWindowless` (`cmd/reload.go:197`) has no test | Now `:264`, and still named in no `cmd/*_test.go` |
| `scripts/test-windowless-routing.sh` | Does not exist |
| `analyze` is in `KnownRoles` but not in the default windows | Still true (`bus/launcher.go:35`) |

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

- [x] A message to a role with no consumer is refused at `Send` — written to no inbox — and the
      sender reads `NOT SENT` with exit 1; the daemon's analyze route is refused the same way
- [x] The refusal is visible — a lifecycle event names the role, once per role while the condition
      holds, rather than failing silently or once per message
- [x] **Negative control:** a role with no window whose own listener or headless agent is running is
      sent to and receives normally — a window is not the test
- [x] **Negative control:** session launch still seeds every role's startup message; no send made
      before a role's consumer is up is refused
- [x] A mode role (`research`, `auto`) gets no special case: one nothing reads is refused like any
      other role, and one cycled to receives normally (the user's decision of 2026-11-16)
- [x] `diagnose` distinguishes "no consumer exists" from "consumer exists but did not receive", and
      never prescribes `deliver --force` for a role with no pane
- [x] The no-consumer finding names the actual remediation (launch the role, or stop routing to it)
- [x] `muxcode status` makes a role with no consumer distinguishable from an idle one that has one
- [x] Draining such an inbox by hand is no longer required — the pile does not re-accumulate
- [x] A reload of a role with no window fails **immediately** with an error naming the absence — never
      "did not exit" — `RoleWindowMissing` refuses before the reload marker is written
      (`TestRoleWindowMissing`, 2026-09-08, uncommitted)
- [x] The provider selector does not offer a windowless role as a reload target, and labels it
      distinctly from a dead one — it is offered as a **config** target instead: `Windowless` never
      reports `Alive`, `isSelectable` admits it for a config-only apply and refuses a dead one,
      `notAliveLabel` renders `(no window)` (`TestActiveAgentStatusesMarksWindowlessRoles`,
      `TestReloadBatchConfiguresWindowlessRole`)
- [x] `diagnose` on a role with no consumer and an **empty** inbox does not report "No issues detected"

### Technical approach

Defect A is a refusal at `bus.Send` (decided 2026-11-15), not a guard at the one emit site: a role
nothing can consume for is refused whoever the sender is, and the analyze route is one caller among
them. The predicate joins three answers that already exist — a window (`RoleHasWindow`,
`bus/agent_health.go`), a live listener (`IsPolling`, `bus/notify.go`) and a running headless agent
(`IsHarnessActive`, `bus/notify.go`) — and is the one MUX-180's notify and workflow checks reuse.
The refusal returns through
[MUX-155](./MUX-155-send-dedup-keys-on-target-not-sender.md)'s `notSent` road. An inbox
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

- [x] Pin current behavior: a send to a windowless role lands in the inbox and is never consumed
- [x] Pin that `diagnose` on that role returns `receipt-gap` with the `deliver --force` remediation
- [x] Confirm `bus.Notify` to a nonexistent pane fails non-fatally (stderr only) — the silent half
      (non-fatal confirmed; "stderr only" corrected in the results below)
- [x] Enumerate the sends made before a role's consumer is up (session launch, the startup bootstrap,
      a reload's relaunch window) and record which the refusal must let through

### Results for Phase 1

Recorded 2026-11-16 from the worker's findings file (`/tmp/mux145-p1-findings.md`); the pin tests'
names, the two run logs and the cited send sites were checked by plan. Test-only change, uncommitted.
Verified and ticked 2026-11-16 once all three had run after the last test edit (12:33:09):
`./build.sh` exit 0 at 12:36:35, `./test.sh` exit 0 at 12:37:46, review at 12:38:26 with 0 must-fix,
0 should-fix, 0 nits.

**The pins** — `tools/muxcode/bus/windowless_routing_test.go`. Fixture: tmux pointed at an empty
`TMUX_TMPDIR`, so pane capture fails exactly as for a missing window; daemon keepalive fresh and
`daemon.version` equal to this build, so the daemon checks stay quiet.

| Test | Pins | Inverted by |
|------|------|-------------|
| `TestWindowlessRoleSendLands_MUX145Pin` (claude, opencode) | `Send(daemon→analyze event)` to a role with no window, no listener (`IsPolling`) and no headless agent (`IsHarnessActive`) lands in `inbox/analyze.jsonl`, with no receipt | Phase 2 |
| `TestDiagnoseWindowlessRole_MUX145Pin` (claude, opencode) | Real `CollectEvidence` + `RunDiagnostics`: `IsAlive` fail-safes to true, so `agent-down` (MUX-006) stays silent; three un-receipted events → critical `receipt-gap` prescribing `muxcode deliver analyze --force`; inbox drained → "No issues detected" | Phase 3 |

That settles the activation question about `checkAgentDown`: for a windowless role it is silent, not
falsely specific. Phase 3's check still has to be ordered so it speaks before `receipt-gap`.

**Live reproduction** — private tmux server, a session with only an `edit` window, daemon at
`--poll 1 --debounce 1`, tree-built binary.

| Item | Result |
|------|--------|
| The send lands and is never consumed | Two edit-stabilization cycles → 2 events in `inbox/analyze.jsonl`, no receipt; the pile grows each cycle |
| `diagnose` (the CLI) | Events backdated 400 s: `receipt-gap` naming `muxcode deliver analyze --force`, non-zero exit, no `agent-down`. After draining: "No issues detected" — falsely clean |
| `Notify` to a nonexistent pane, **OpenCode** analyze | Non-fatal; the daemon stays up. The error reaches daemon stderr (`[route] failed to notify analyze: …`). **Correction to this spec's "stderr only":** since the injection guard, each cycle also writes an `injection-refused` lifecycle row — `analyze: pane capture failed: exit status 1` — which names a capture failure, not the missing window or consumer. One `analyzing` workflow row too (`trigger=daemon:analyze-route`, MUX-180's) |
| `Notify`, **Claude** analyze — the default, since `roleDefaultCLI` has no analyze case | Fully silent: `Notify` takes the Claude road, `IsAgentIdle` cannot capture and reads false, the call returns nil. No stderr, no lifecycle row. The events still land |
| `muxcode send` | Discards `Notify`'s error (`_ = bus.Notify(session, to)`, `cmd/send.go:265`) |
| Run | `/tmp/mux145-p1-check.sh` via the run agent. Proc `1794850436-proc-653b82bc`: 12 passed, 1 failed — a loose lifecycle grep in the script matched unrelated rows. Fixed; proc `1794850475-proc-1102e595` (run task `1794850470-spawn-926e95e5-ebf6c941`): 13 passed, 0 failed (expected 13), exit 0 |

**Sends made before a role's consumer is up** — what Phase 2's refusal must let through.

| Send | Site | Consumer at that instant | The refusal must |
|------|------|--------------------------|------------------|
| Project-init request to edit (`announceProjectInit`) | `InitSessionProject`, `bus/launcher.go:221` — **before** `TmuxNewSession` (`:274`) | None: no tmux session exists, so the window list is unreadable | **Let through** — would break otherwise. An unreadable window list, including "no such session", reads as indeterminate, which counts as having a consumer |
| Spawn worker seed | `bus/spawn.go:261` — `Send` runs **before** the worker's window is created | No window, no listener yet | **Let through** — would break every `spawn start` and graph spawn node otherwise. A spawn role whose registry entry is starting or live counts as a consumer (or spawn roles are exempt) |
| `request:startup` bootstrap (`PreLaunchSetup`, `bus/launch.go:867`) | Inside `muxcode agent launch`, in the agent's own pane | Window present | Passes the window test; also already a self-send (`isStartupBootstrap`) |
| Plan's opened-doc `context` request | `bus/launcher.go:391`, after the plan window exists | Window present | Passes |
| Sends during a reload's stop→relaunch | Any sender | Window persists across a reload | Passes (graph dispatches are already deferred, MUX-217) |
| Hosted roles — `docs`→plan, `pr-read`→commit | `Send` delivers to the host's inbox | The host's window | Passes, provided the predicate tests the inbox role (`WindowForRole`), not the name addressed |
| `prompt` role (`SendHumanPrompt`, `bus/prompt_authority.go:67`) | Human-typed | A headless prompt agent, no window | Passes, provided the headless agent counts — which is the decision's third clause |
| `webhook` | `bus/webhook.go:164` sends **from** webhook; it is a sender, not a recipient | — | A webhook-originated send to a role with no consumer is refused like any other |
| Mode roles `research` / `auto` | Their own inboxes; each maps to its **own** hold window, created only when first cycled to | No window until first cycled | **Undecided — see below** |

**Decided by the user 2026-11-16 (relayed by edit, `1794854125-edit-6209ef94`): (a) refuse.**
`research` and `auto` get no special case — a message to a mode role nothing reads is `NOT SENT`
like any other role. Phase 2 had kept today's queueing as a placeholder (`RoleHasConsumer` returns
true for any `modeRoles` entry), so the decision is not yet in the code: Phase 6 carries the step that
removes the exemption and the test that covers it. A mode role that **has** been cycled to has its
hold window and receives normally. The question as it was put:

**Decision owed — mode roles that have never been cycled to.** Today a message to `research` or
`auto` queues in its inbox and is read when the user first cycles F1 or F2 to that mode. Under the
refusal as decided (no window, no listener, no headless agent) such a role has no consumer, so the
send would be refused with `NOT SENT` — a change to behaviour that works today. The reload path
already treats a never-cycled mode role as windowless (`ReloadWindowForRole`). The choices: refuse
like any other role, or count a known mode role as having a consumer and keep queueing. This is the
user's to make; it is not decided here, and Phase 2 should not settle it by implementation. It bears
on the user's 2026-11-15 note that the hosted-window feature is to be removed.

### Phase 2: Refuse at Send

- [x] One consumer predicate — a window, a live listener or a running headless agent; an unreadable
      window list reads as having a consumer — placed where MUX-180 can reuse it
- [x] `bus.Send` refuses a message to a role the predicate fails for, through `notSent`: `NOT SENT`,
      exit 1, nothing written to the inbox
- [x] Emit a lifecycle event for the refusal, once per role while the condition holds
- [x] Negative control: a session **with** an analyze window still routes normally
- [x] Negative control: a windowless role with its own listener, and one with a running headless
      agent, are each sent to normally
- [x] Negative control: the sends Phase 1 enumerated still land

### Results for Phase 2

Recorded 2026-11-16 from the worker's request; `bus/consumer.go`, the call sites and the test names
were read by plan. Uncommitted. Verified and ticked 2026-11-16 once all three had run after the last
edit (13:04:16): `./build.sh` exit 0 at 13:05:28, `./test.sh` exit 0 at 13:06:46, review at 13:07:24
with 0 must-fix, 0 should-fix, 0 nits (`/tmp/muxcode-review-1794852410.txt`) — its second pass; the
first returned the prompt-role must-fix and the stderr nit recorded below. Final recorded checks
16/16.

| Item | Result |
|------|--------|
| The predicate | `RoleHasConsumer(session, role)` (`bus/consumer.go`), `role` being the inbox a send lands in. True for a window, a live listener (`IsPolling` **or `IsWaiting`** — a `--wait`er reads its own inbox too), or a running harness (`IsHarnessActive`, the pid marker). The `prompt` role is covered by that last clause only: the first cut also counted it whenever `PromptAgentEnabled`, which review rejected as a must-fix — enabled is not running — and the clause was removed. An unreadable **or empty** window list is indeterminate and counts as a consumer — that is what lets the project-init send through before the tmux session exists |
| Always a consumer, by rule not evidence | A spawn worker role (`IsSpawnRole`) — its seed precedes its window. A mode role (`research`, `auto`) — **today's queue-until-cycled behaviour kept, pending the owed decision**; the code comment says so. *The decision came later the same day — refuse — so this exemption is to be removed; see the first step of Phase 6* |
| The refusal | `refuseWithoutConsumer`, called in `sendMessage` (`bus/inbox.go:346`) on the resolved inbox role, so every road passes it — `Send`, `SendNoCC`, the force road and `SendHumanPrompt`. Returns `ErrNoConsumer`; a self-send is never refused. Nothing is written to the inbox |
| The CLI | `cmd/send.go`: `ErrNoConsumer` → `NOT SENT — … has no window, inbox listener or headless agent …; launch the role or stop routing to it`, exit 1. `--force` does not deliver it |
| The row | `send-no-consumer` (warn, source `bus`), once per role per episode: `no-consumer-<role>.marker` in the bus dir is created by the first refusal and removed by the first send that finds a consumer |
| The daemon's analyze route | Prints no error line of its own for `ErrNoConsumer` and returns before `Notify` (`daemon/daemon.go:783`). As first written `refuseWithoutConsumer` printed `[send] REFUSED …` to stderr on every refusal, one line per edit-stabilization cycle; after review's nit the line is written with the `send-no-consumer` row, once per episode (run task `1794851687-spawn-926e95e5-3e2fb561`: one `REFUSED` line across two cycles). The `analyzing` workflow transition just above still fires each cycle — MUX-180's |
| Tests | `bus/windowless_routing_test.go`: `TestSendRefusesRoleWithNoConsumer` (replaces the Phase 1 send pin), `TestSendDeliversWhenAConsumerExists` (10 controls, per the worker); `TestDiagnoseWindowlessRole_MUX145Pin` kept for Phase 3. `deliver_test.go` and `inject_verify_test.go` fixtures now answer `list-windows` |
| Run | Run task `1794851422-spawn-926e95e5-3e6b7858`: 15 passed, 0 failed — full `bus`/`cmd`/`daemon` passing; with the guard off the refusal test fails; live: a no-consumer session refuses the route with one row, the CLI prints `NOT SENT` and exits 1, `MUXCODE_CONSUMER_GUARD=0` delivers, and a session with an analyze window receives |
| Docs | `docs/agent-bus.md` `muxcode send` (the NOT SENT row and a "No consumer, no send" table), `docs/configuration.md` (`MUXCODE_CONSUMER_GUARD`) |

**Decided by the user 2026-11-16 (relayed by edit, `1794854617-edit-48e5e641`): (a) keep it** as an
emergency off switch, and the 28 integration scripts keep setting it for now. The follow-up the user
asked for — give those scripts' fake agents a real consumer so the opt-out can be removed and they
exercise the guard — is filed as
[MUX-230](../backlog/MUX-230-integration-scripts-opt-out-of-the-consumer-guard.md). The question as
it was put:

**Not in this spec as written — added by the worker, for the user to see.** `MUXCODE_CONSUMER_GUARD=0`
turns the refusal off. It is read from the sending process's environment on every send. **28
integration scripts now set it** (one added line each), because their fixtures play an agent by
reading its inbox from the script — a reader that is no window, listener or headless agent. The
consequence is that those 28 scripts run with the refusal disabled throughout, so none of them would
notice it misfiring on a real role; the refusal's own coverage is the unit tests above and Phase 6's
script. No decision recorded for or against the variable; the spec's decision said refuse, and did not
ask for a switch.

### Phase 3: Teach diagnose the no-consumer case

- [x] Add a no-consumer check ordered **before** `receipt-gap` in `diagnosticChecks`
- [x] Remediation names launching the role or stopping the routing — never `deliver --force`
- [x] Negative control: a windowed role with a genuine receipt gap still reports `receipt-gap`
      (the new check must not swallow the real one)
- [x] A role with no consumer and an **empty** inbox is reported as such, never "No issues detected"
      (Defect C's clean verdict — the check must not key on the inbox)
- [x] Negative control: a windowless role that reads its own inbox is not reported

### Results for Phase 3

Recorded 2026-11-16 from the worker's report; the diff to `bus/diagnose.go` and the test names were
read by plan. Uncommitted. Verified and ticked 2026-11-16 once all three had run after the last edit
(13:09:47): `./build.sh` exit 0 at 13:13:36, `./test.sh` exit 0 at 13:14:54, review at 13:15:35 with
0 must-fix, 0 should-fix, 0 nits (`/tmp/muxcode-review-1794852901.txt`).

| Item | Result |
|------|--------|
| The evidence | `AgentStateEvidence.NoConsumer` (`no_consumer` in `--json`) — `!RoleHasConsumer(session, WindowForRole(role))`, Phase 2's predicate, so a hosted role is judged on its host |
| The check | `checkNoConsumer`, in `diagnosticChecks` ahead of `receipt-gap`. Failure mode `no-consumer`. It does not read the inbox to decide: an empty inbox is a **warning**, stranded messages make it **critical** |
| The remediation | Launch the role (add it to `MUXCODE_WINDOWS` and relaunch, or run its agent headless), or stop routing to it (the chain, subscription or cron entry); with strays, `muxcode inbox --role <role>` to discard them. Never `deliver --force` |
| Impossible remediations withheld | While `NoConsumer`, `RunDiagnostics` drops any finding whose remediation prescribes `muxcode deliver` (`prescribesDeliver`) — `receipt-gap` included. This goes further than the step asked: it is a rule over every check, not an ordering between two |
| Timeline | `send-no-consumer` joins the lifecycle events the report shows |
| Tests | `TestDiagnoseNoConsumer` (claude, opencode; replaces the Phase 1 diagnose pin), `TestDiagnoseConsumerControls` — a windowed role with a real receipt gap, a windowless role with its own listener, an unreadable window list |
| Run | Run task `1794852618-spawn-926e95e5-d0b8a9a2`: 12 passed, 0 failed — full `bus`/`cmd`/`daemon` passing; with the check off `TestDiagnoseNoConsumer` fails and the controls pass; live `diagnose` checked |
| Exit code | An empty-inbox `no-consumer` is a warning, so `muxcode diagnose` exits 0 on it — the role is reported, not "No issues detected", but a script reading only the exit code sees nothing. With stranded messages it is critical and exits non-zero |
| Docs | `docs/architecture.md` diagnose section (seventeen modes; the two rules), `CLAUDE.md` Agent diagnostics bullet (plan) |

### Phase 4: Surface it in status

- [x] `muxcode status` distinguishes a role with no consumer from an idle one that has one
- [x] Verify `docs`→`plan` style hosted-role mappings are not misreported as having no consumer

### Results for Phase 4

Recorded 2026-11-16 from the worker's report; the `bus/inspect.go` diff was read by plan.
Uncommitted. Verified and ticked 2026-11-16 once all three had run after the last edit (13:17:52):
`./build.sh` exit 0 at 13:21:23, `./test.sh` exit 0 at 13:22:43, review at 13:23:29 with 0 must-fix,
0 should-fix, 0 nits (`/tmp/muxcode-review-1794853373.txt`).

| Item | Result |
|------|--------|
| The value | `AgentStatus.Health` gains `no-consumer`, set from `!RoleHasConsumer(session, WindowForRole(role))` after `excluded` and ahead of `stopped`/`alive`/`dead` (`bus/inspect.go`). It is in `--json` as `health` |
| The column | `HEALTH` widened from 8 to 11 characters in the status table, the remote view (`bus/remote.go`, `tui/remote.go`) and the TUI, which colours it yellow |
| Hosted roles | Judged on the host's window: `docs` is not reported while plan has a window; `pr-read` is reported when commit has none |
| Test | `TestStatusShowsNoConsumer` — `analyze` and `pr-read` reported; `build`, `docs`, `research` (a mode role, by Phase 2's rule) and `webhook` (`excluded`) not |
| Run | Run task `1794853093-spawn-926e95e5-926c3a6e`: 10 passed, 0 failed — full `bus`/`cmd`/`daemon`/`tui` passing; with the branch off the test fails; the live table and `--json` mark `analyze` `no-consumer` and not `docs`, `edit`, `plan` or `research` |
| Docs | `docs/agent-bus.md` `muxcode status` — the HEALTH values (plan, written after the review). Its example output there predates the PROVIDER and HEALTH columns and was left as it is |

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
- [x] The CLI road is tested: `configureIfWindowless` (`cmd/reload.go:197`) has no test — the modal
      road is covered by `TestReloadBatchConfiguresWindowlessRole`, the CLI road by nothing (edit,
      2026-09-08 14:3x). Both roads must stay in step, or the MUX-142 shape returns — closed
      2026-11-16 by `TestConfigureIfWindowless`, see the results below

### Results for Phase 5's last step

Recorded 2026-11-16 from the worker's report; the test's cases were read by plan. Uncommitted.
Verified and ticked 2026-11-16 once all three had run after the last edit (13:25:04): `./build.sh`
exit 0 at 13:28:36, `./test.sh` exit 0 at 13:29:59, review at 13:30:36 with 0 must-fix, 0 should-fix,
0 nits (`/tmp/muxcode-review-1794853807.txt`). The other seven steps of this phase are the
2026-09-08 ones and were not re-run.

| Item | Result |
|------|--------|
| Seams | `cmd/reload.go` gains `listWindows` and `reloadExit` — the tmux and process-exit boundaries `configureIfWindowless` touches — so the function is testable without a tmux server or an `os.Exit` |
| The test | `cmd/reload_windowless_test.go` `TestConfigureIfWindowless`, in step with the modal road's `TestReloadBatchConfiguresWindowlessRole`: a windowless `analyze` with `--cli codex --model …` is configured, not reloaded (resolves over a stale environment value, persisted to the config file, no reload marker written); no `--cli` or `--model` exits 1 and writes nothing; a windowed role and an unreadable window list both fall through to the reload |
| Run | Run task `1794853524-spawn-926e95e5-38dfa907`: 7 passed, 0 failed — full `cmd` and `bus` passing; with the function made to always fall through, both windowless subtests fail and both controls pass |

### Phase 6: Integration test

- [x] Mode roles get no special case (the user's decision of 2026-11-16): remove the `modeRoles`
      exemption from `RoleHasConsumer` (`bus/consumer.go`) and invert the unit control that pins it
      (`TestSendDeliversWhenAConsumerExists/never-cycled mode role`); `diagnose` and `muxcode status`
      then report a never-cycled mode role as `no-consumer`, and `TestStatusShowsNoConsumer`'s
      `research` case moves with it. Check what sends to `research` or `auto` at session launch or
      from the daemon before the role is first cycled to, and record what is now refused
- [x] Test: a mode role never cycled to → `muxcode send research …` prints `NOT SENT` and exits 1,
      nothing in its inbox; the same role once its hold window exists → the send lands (negative
      control)
- [x] Create `scripts/test-windowless-routing.sh` (hermetic; scratch bus + tmux session + daemon)
- [x] Test: session with no consumer for analyze → edits stabilize → **no** message accumulates, one
      refusal lifecycle event is written, and a `muxcode send analyze …` prints `NOT SENT` and exits 1
- [x] Test: session **with** an analyze window → message routes and is consumed (negative control —
      the guard cannot go inert)
- [x] Test: a windowless role with its own listener running → the send lands and is consumed
      (negative control — a window is not the test)
- [x] Test: `diagnose` on a role with no consumer reports that finding, not `receipt-gap`
- [x] Test: `diagnose` on a windowed role holding un-receipted messages still reports `receipt-gap`
- [x] Test: reload a windowless role → fails fast with the error naming the absence, no reload marker
      written, no 12-second wait
- [x] Test: reload a windowed role → still reloads (negative control — the refusal cannot go inert)
- [x] Coverage floor keeps a skipped section from reporting green
- [x] Run the script and verify all checks pass

### Results for Phase 6

Recorded 2026-11-16 from the worker's request; the script's header and both run logs were read by
plan. Uncommitted. Recorded in two passes: the worker's first request (13:35:47) crossed the
mode-role decision, so sections A–G came first and the mode-role rows — the exemption's removal,
section H and run 3 — were added at 13:4x once the worker had done the two new steps. Verified and
ticked 2026-11-16 once all three had run after the last edit (13:41:38): `./build.sh` exit 0 at
13:42:16, `./test.sh` exit 0 at 13:43:33, review at 13:45:46 with 0 must-fix, 0 should-fix, 0 nits
(`/tmp/muxcode-review-1794854618.txt`) — on the mode-role version, 21/21; an earlier pass had
returned the missing mode-role work as a blocker. The graph's phase report still described the
19-check script; the tree and the review are what was verified.

| Item | Result |
|------|--------|
| The script | `scripts/test-windowless-routing.sh` — tree-built muxcode and claude-stub, scratch HOME, lifecycle log, install dir and project, a scratch daemon on a private tmux server. The session has windows `edit`, `test` (a `❯` stand-in that reads nothing) and `build` (a claude-stub with its own listener), agents in pane 1 as `LaunchSession` lays them out. `MUXCODE_CONSUMER_GUARD` is **unset** — the refusal is what the script tests; one line sets it to `0` deliberately, to plant the stranded events section D diagnoses |
| A — no consumer | Two edit-stabilization cycles leave analyze's inbox empty, with one `send-no-consumer` row and one `REFUSED` line in the daemon log; `muxcode send analyze …` prints `NOT SENT` and exits 1 |
| B — negative control | An analyze window whose pane reads the inbox: the next cycle's event lands and is consumed, receipted by analyze |
| C — negative control | No analyze window but its own `inbox --poll --loop` listener: the send lands and the listener consumes it, the receipt naming its pid |
| D — diagnose, no consumer | Stranded events backdated: `no-consumer` naming the launch and stop-routing remediations; no `receipt-gap`, no `deliver` |
| E — negative control | `diagnose test`, a windowed role holding un-receipted events: still `receipt-gap` prescribing `deliver`, no `no-consumer` |
| F — reload, windowless | `reload analyze` fails at once naming the absence; no reload marker |
| G — negative control | `reload build`: the claude-stub is stopped and relaunched (a launch row with `reason=reload`), its listener back |
| Run 1 | Proc `1794854006-proc-9f29aef8`: 14 passed, 5 failed — two faults in the script (a count helper that emitted two lines; single-pane windows, where the agents live in pane 1); not product failures |
| Run 2 | Proc `1794854112-proc-273d6e10` (run task `1794854106-spawn-926e95e5-1f9d2f2d`): 19 passed, 0 failed, floor 19, exit 0 |
| Mode roles — the code | `RoleHasConsumer` no longer exempts `modeRoles` (`bus/consumer.go`). A never-cycled `research` or `auto` is refused, diagnosed and shown in status as `no-consumer`; once cycled to, its hold window — kept by name across the window swap — makes it a consumer |
| Mode roles — what is now refused | Per the worker's check: nothing in production sends to `research` or `auto` before the first cycle — the `startup` request is sent from the agent launch inside the hold window cycling creates — so only an explicit send before a cycle is refused. Plan did not trace this independently |
| Mode roles — unit | `TestSendRefusesNeverCycledModeRole` (new); the delivery controls gain "mode role once cycled to" in place of the never-cycled one; `TestStatusShowsNoConsumer`'s `research` case moved to reported. Go check task `1794854311-spawn-926e95e5-b8220204`: 9 passed, 0 failed; with the exemption restored both fail and the delivery controls pass |
| H — mode role | `research` never cycled to refuses a send (`NOT SENT`, exit 1, nothing written); once its hold window exists the same send lands |
| Run 3 | Proc `1794854470-proc-582b4878` (run task `1794854464-spawn-926e95e5-e501eac1`): 21 passed, 0 failed, floor 21, exit 0 |
| I — negative control, a real launch | Added after `verify-spec` left the launch-seeding criterion open. `muxcode launch` (`LaunchSession`) of a Go project with windows `edit`, `build` and `test`, claude-stub agents and the guard on: all three startups are seeded and edit's project-init — sent before the tmux session exists — lands, with no `send-no-consumer` row and no refusal. The launcher's final attach fails for want of a terminal, after the launch is done |
| Run 4 | Proc `1794854888-proc-28120cc2` (run task `1794854880-spawn-926e95e5-297356eb`): 25 passed, 0 failed, floor 25, exit 0 |
| Section I verified | 2026-11-16: the script's last edit was 13:47:59; `./build.sh` exit 0 at 13:49:55, `./test.sh` exit 0 at 13:50:13, review at 13:50:57 with 0 must-fix, 0 should-fix, 0 nits. The worker's `CLAUDE.md` entry edit (13:50:10) fell between the build and the test — a file the build does not read. The launch-seeding criterion was ticked on this |
| Limit of section I | Three windowed Claude-stub roles. A spawn worker's seed before its window, and a launch under a provider other than Claude, are not launched for real; the spawn seed stays on its unit control |
| Docs | `CLAUDE.md` integration-test entry `test-windowless-routing` (the worker) |

## Notes

Root-caused by the edit agent during the live incident; both halves independently verified from this
role before filing (window list read from `tmux list-windows`, helper absence from grep, emit path
read at `daemon.go:645-680`). Edit drained the 11 messages by hand, which cleared the symptom for
this session only — the guard is what stops it returning.

Related: the diagnose half belongs to the same family as the `checkUnexplainedEvidence` invariant
described in [`CLAUDE.md`](../../../CLAUDE.md) — verdict honesty rather than pattern coverage. The
routing half is adjacent to, but distinct from,
[`MUX-127`](../backlog/MUX-127-review-completion-routing.md): that one routes to the wrong *recipient*, this
one routes to a recipient that does not exist.

Defect C (2026-09-08) was root-caused by edit and handed over as `/tmp/mux-145-defect-c.md`; plan
re-verified the window list, the `KnownRoles`/`Windows` split and every cited line at `HEAD` before
folding it in, and added two facts the handoff did not carry — a failed reload logs nothing, and the
error string has a prior unrelated cause. The three defects share one root and should share one fix:
`RoleHasWindow` is the signal that can say "no", and the emit path, diagnose and reload each need to
consult it rather than `IsAgentAlive`.

## Status

**Complete** — activated and closed 2026-11-16 by `55-defect-backlog-loop` run `1794626267-55-defect-backlog-loop-15547e2e` (Defects rank 13 then; ranks 1–12 were parked). 49/49: Phases 1–6 complete and all twelve criteria met. Uncommitted at close-out; the run's ship step commits it. The launch-seeding control was the last: left open at Phase 6's verification for want of a real launch, then shown by the integration script's section I (a real `LaunchSession` with the guard on). The mode-role question was **decided by the user 2026-11-16: refuse, no special case** — one criterion and two Phase 6 steps were added for it (46 items became 49), because Phase 2 shipped the opposite as a placeholder. The `MUXCODE_CONSUMER_GUARD=0` switch was **decided the same day: kept** as an emergency off switch, its 28 script opt-outs staying for now, with [MUX-230](../backlog/MUX-230-integration-scripts-opt-out-of-the-consumer-guard.md) filed to remove them. Nothing awaits the user.

**Previously: Backlog — 9/46, Phase 5 at 7/8. Unparked 2026-11-15** on the user's instruction relayed by edit,
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
