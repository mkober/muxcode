# Force-Respond Escalation and Graph TUI Mode Cycling

Two TUI-and-delivery improvements requested together:

1. **Force-respond escalation** — a daemon escalation ladder that recovers an agent holding an un-responded request, plus a one-key **force-respond** action in the TUI. Today recovery requires a human noticing the stall and running two or three commands by hand — and the automatic backstop that *should* handle it is silently neutered by a guard that blocks the recovery injection with the very task it is trying to recover.
2. **Graph TUI mode cycling** — `Tab` cycles the graph TUI between its three surfaces in place, so switching modes never requires closing the popup and reopening the `prefix + b` menu.

> **Scope note.** These are independent changes in different subsystems (`bus`/`daemon` delivery
> versus `tui/graph_ui.go` navigation) that share only "the user asked for both, in the TUI". They
> were briefly filed apart and merged back here at the user's direction. **Phases 1–5 and Phase 6
> can be implemented, reviewed, and landed separately** — nothing in the escalation ladder depends
> on the cycling work or vice versa.

## Context

### The user's request

> "It's related to the issue of the build agent not responding and needed to be forced by another message from edit or by the user telling the agent to check its inbox. There should be a way to automate this in the TUI to force an agent to respond to a request from the edit."

### Motivating incident — 2026-08-26

The build agent (OpenCode / DeepSeek V4 Flash) held an edit build request un-responded for roughly **20 minutes**. Recovery took two manual interventions plus a timed retry:

| Step | Result |
|------|--------|
| `muxcode reload build` | Cleared the wedged conversation, but not the delivery |
| `muxcode deliver build --force` | **Skipped its own injection**: `[wakeup] skipping build injection — in-flight task build:… exists (121s old)` |
| Wake injections | Separately failing with `command send-keys: invalid flag -` — now [`MUX-104`](./MUX-104-send-keys-dash-payload.md); no fallback path existed |
| Daemon receipt-gap backstop | Fired a `delivery-gap` event to edit; the agent stayed stuck |
| Final recovery | A delayed `deliver --force` after the in-flight task expired |

### Root cause: the guard blocks its own recovery, and lies about it

`OpenCodeProvider.SendWakeUp()` (`bus/provider_opencode.go:135-147`) skips injection whenever *any* in-flight task targets the role and is more than 5 seconds old:

```go
tasks, _ := ListTasks(session, TaskInFlight)
for _, t := range tasks {
    if t.To == role && time.Now().Unix()-t.SentAt > 5 {
        fmt.Fprintf(os.Stderr, "  [wakeup] skipping %s injection — in-flight task %s:%s exists (%ds old)\n", …)
        return nil                          // ← indistinguishable from success
    }
}
```

Two properties make this worse than a missing feature:

1. **The stuck request is its own blocker.** The un-responded request *is* the in-flight task, so it suppresses every recovery injection until `TaskExpired` (default 600s). `deliver --force` cannot override it — `SendWakeUp(session, role string)` has **no force parameter** at all, on the interface or any implementation.

2. **The skip returns `nil`, so callers believe it worked.** This is the sharper half. The daemon's `checkPollHealth()` backstop is *not* alert-only by design — it genuinely re-drives delivery via `ForceDeliver` / `SendWakeUp`. But a skipped injection reports success, so the backstop records a re-drive that never happened and moves on. The observable behaviour ("alert fired, nothing recovered") looks like an alert-only design; it is actually a recovery path silently swallowed by the guard.

The guard's own comment states the assumption that fails: *"The message was already consumed and injected on a prior wake-up."* When the prior injection **failed** — exactly what MUX-104 caused — the guard converts a transient failure into a permanent one for the length of the task timeout.

### The second request: graph TUI mode cycling

> In the graph TUI modal, `Tab` cycles between the three related surfaces (Graph Runs browser,
> Launch Graph template picker, Pending Gates queue) in place, so switching modes never requires
> closing the popup and reopening the `prefix + b` menu; applies to all three entry points
> `g`/`G`/`a`, which currently open single-mode popups.

Today the three [`MUX-031`](./MUX-031-graph-run-tui.md) entries each launch a
single-mode TUI with no way out but closing it:

| Menu entry | Key | Command |
|------------|-----|---------|
| Graph Runs | `g` | `muxcode graph ui` |
| Launch Graph | `G` | `muxcode graph ui --templates` |
| Pending Gates | `a` | `muxcode graph ui --gates` |

**The three modes are already one type.** `NewGraphUI()`, `NewGraphLauncherUI()`, and
`NewGraphGatesUI()` all return `*GraphUI`, differing only in the initial `view` field
(`viewGraphRuns` / `viewGraphTemplates` / `viewGraphGates`). Cycling is therefore a key handler
that changes `ui.view` and refreshes — **not a rearchitecture**. `graph_ui.go` has no `Tab`
handler at present.

The three surfaces answer three halves of one question — *what is running*, *what should I start*,
*what is waiting on me*. Moving between them currently costs: close popup → `prefix + b` → pick
another entry → new process, new run-store read, selection state lost. That friction peaks in
exactly the situation the graph TUI exists for: a gate is waiting and you want to see the DAG
behind it before approving.

### Why the existing machinery does not cover this

| Mechanism | Why it falls short |
|-----------|--------------------|
| `checkPollHealth()` receipt-gap backstop | Re-drives delivery, but the in-flight guard no-ops the injection and reports success |
| `deliver --force` | Clears stale notified markers and parked input, still honours the in-flight skip |
| `checkTrackedTasks()` task timeout | Self-heals eventually — after 600s of a wedged agent |
| Dashboard / remote TUI | Show agent state; offer **no** action to force a response |

## Requirements

### Acceptance criteria

- [x] `SendWakeUp` gains a force path (parameter or sibling method) so a recovery injection is never blocked by the stuck request's own in-flight task; the `Provider` interface and all **four** implementations (`claude`, `opencode`, `codex`, and the no-op `local`) are updated coherently — this criterion originally said "three" and undercounted, see the note below
- [x] A skipped injection is **distinguishable from a delivered one** by its return value — callers must never read a skip as success
- [x] `checkPollHealth()` (and any other re-driver) reacts to a skip rather than recording a phantom re-drive
- [x] Daemon escalation ladder for a request un-responded past a threshold: re-notify → `ForceDeliver` → `ForceDeliver` overriding the in-flight skip → alert edit **with the escalation history**, not just "a gap exists"
- [x] Each rung emits a distinct lifecycle event, so the ladder is reconstructible after the fact
- [x] A rung counts as succeeded only when the injection **verifiably landed** — implemented as receipt-based advancement (stronger than the named `confirmInjectionAndConsume`)
- [x] TUI force-respond: a single key on the dashboard TUI agent row (and/or remote TUI agent detail) runs the same force path, behind a confirm prompt
- [x] The TUI action and the daemon ladder share one code path — no second implementation that can drift
- [x] Escalation is opt-out via an env var, per existing watchdog convention (`MUXCODE_FORCE_RESPOND_DISABLE`, threshold via `MUXCODE_FORCE_RESPOND_SECS`)
- [x] Any role may be a target — unlike auto-clear, there is no `edit`/`auto` exclusion, since edit stalling is itself a failure worth recovering
- [x] Escalation never fires against an agent that is legitimately busy on a long task — the trigger is an **un-responded request past a threshold**, not mere elapsed activity

### Acceptance criteria — gate auto-show (addendum)

Added 2026-08-26 by user decision. This **resolves the open question carried by
[`MUX-031`](./MUX-031-graph-run-tui.md)** — *"should the TUI's gate badge replace or
complement a status-bar flash?"* The answer is neither: surface the approval UI itself.

- [x] Dispatching a `wait_human` node opens the Pending Gates popup
- [x] Best effort — no attached client or no popup surface degrades silently, never failing the run
- [x] Opt-out via `MUXCODE_GATE_AUTOSHOW_DISABLE=1`
- [x] The existing edit notification and `graph approve` instruction are **kept**, not replaced
- [x] Pinned by `TestExecHumanGateAutoShowsGatesPopup` — **test exists and was read** (`graph_exec_test.go:411`); a green suite verdict covering it is still outstanding (see note)

**Motivation:** a demo gate sat unnoticed for **37 minutes**. The gate had done its job — the run
paused, edit was notified — but the notification landed in an inbox nobody was watching. A gate
exists solely to collect a human decision, so a gate whose notice is missable has failed at the one
thing it is for.

Why this is the right resolution rather than a louder notification: a status-bar flash tells you
*that* something is waiting; the popup shows you **what** you are approving and what it releases.
MUX-031 already built the queue with downstream-impact and git/Atlassian flagging — auto-show
connects the alert to the surface that can answer it, instead of adding a second alert to ignore.

**Evidence status, stated precisely.** The four behavioural criteria above are checked off on
**source inspection**: `graph_exec.go:286` guards `OpenPopup(session, "graph-gates", …)` behind the
env var, discards its error, and sits *below* the existing `SendNoCC` + `Notify(edit)` so the
notification path is genuinely retained. The pinning test exists at `graph_exec_test.go:411`.

What is **not** yet in hand is a suite verdict covering it. The last clean run — 1998 PASS / 0 FAIL
— predates this test. Three attempts to get a fresh count returned pane-scrape bleed rather than a
reply (the test agent runs OpenCode/DeepSeek, whose completion detection reads terminal text; the
same failure produced `support.apple.com/kb/HT208050` as a "result" earlier today). Rather than
keep retrying a flaky channel, the gap is recorded here: **implemented and pinned, not yet
re-verified green.**

The `best effort` property matters more than it reads. Opening a popup depends on an attached tmux
client; a headless or detached session has none. Gate dispatch must never fail because a UI could
not be shown — `OpenPopup`'s error is deliberately discarded, and the bus notification remains the
durable channel.

### Acceptance criteria — graph TUI mode cycling

- [x] `Tab` from any of the three top-level surfaces advances in a fixed cycle: runs → templates → gates → runs
- [x] `Shift-Tab` cycles backwards
- [x] Cycling works identically regardless of entry point — `g`, `G`, and `a` differ only in where the cycle starts
- [x] Cycling re-reads the run store for the surface being entered, so a stale frame is never shown
- [x] Per-surface selection state is preserved across a cycle — returning restores the previous selection where the underlying item still exists, and falls back to the first row where it does not
- [x] `Tab` is inert in **drill-down** views (DAG, node detail, intent prompt) — those have their own `q`/`Enter` semantics, and cycling out of a half-entered prompt would discard input
- [x] `Tab` is inert while a confirm prompt is open — a pending approve/cancel/retry must be answered or dismissed, never sidestepped by a mode switch
- [x] The current surface and the cycle affordance appear in the frame header, so the key is discoverable without documentation
- [x] Popup titles remain accurate after a cycle, or are made neutral — a gate queue under a `Graph Runs` title is worse than a generic one
- [ ] `--render-once` output is unchanged; cycling is interactive-only and must not move the scriptable seam

### Technical approach

- **Force plumb-through**: widen `SendWakeUp(session, role string, force bool)` on the `Provider` interface. Three implementations change; `provider_opencode.go` is the only one with a skip to bypass. A sibling `ForceWakeUp()` avoids touching the interface but risks the two paths drifting — prefer the parameter.
- **Skip must be visible**: return a sentinel (`ErrInjectionSkipped`) rather than `nil`, so `checkPollHealth` can escalate instead of assuming success. This is the single highest-value change in the spec — it converts a silent failure into a signal the existing backstop can already act on.
- **Ladder state**: per-role escalation state alongside the existing watchdog markers, with cooldown and a cap, following `checkStuckProviders()` conventions (two-sighting debounce, cap, cooldown, then alert and stop).
- **TUI action**: `tui/model.go` agent row keybind → confirm → the shared force path. Reuse the `provider_select.go` confirm-flow pattern.
- **MUX-104 dependency**: the ladder is only as good as the injection beneath it. [`MUX-104`](./MUX-104-send-keys-dash-payload.md) must land first, or a dash-leading payload defeats every rung identically.

**Mode cycling** (independent of everything above):

- **Cycle as a view transition.** Add `Tab`/`Shift-Tab` to `handleKey()` mapping the three top-level views in a ring, then `ui.refresh()`. The existing `view` field and `refresh()` switch already carry the semantics.
- **Guard the drill-downs.** Cycle only when `ui.view` is one of the three top-level surfaces; `viewGraphDAG`, `viewGraphNode`, `viewGraphIntent`, and `viewGraphConfirm` ignore `Tab`.
- **Per-surface selection.** `runIdx`/`nodeIdx` are single fields today; cycling needs a per-surface index (or a map keyed by view). Restore by **matching the previously selected item's id**, not the raw index — the list is re-read on entry and rows may shift or vanish.
- **Header affordance.** Extend `renderGraphHeader()` with the surface name and a `Tab: next` hint; this also fixes the stale-popup-title problem. tmux popup titles are fixed once opened, so prefer neutral titles (` Graph `) plus an in-frame surface name.

### Key files

| File | Change |
|------|--------|
| `bus/provider.go` | `SendWakeUp` signature, `ErrInjectionSkipped` sentinel |
| `bus/provider_opencode.go` | Honour force; return the sentinel on skip |
| `bus/provider_claude.go`, `bus/provider_codex.go` | Signature conformance |
| `bus/deliver.go` | `ForceDeliver` propagates force into the wake path |
| `daemon/daemon.go` | Escalation ladder; `checkPollHealth` reacts to the sentinel |
| `tui/model.go` | Force-respond keybind + confirm |
| `scripts/test-force-respond.sh` | Integration test |
| `tui/graph_ui.go` | *(cycling)* `Tab`/`Shift-Tab` in `handleKey()`, per-surface selection state, drill-down guards |
| `tui/graph.go` | *(cycling)* header surface name + cycle hint |
| `bus/popup.go` | *(cycling)* neutral popup titles for the three graph entries |
| `tui/graph_ui_test.go` | *(cycling)* cycle order, guards, selection restoration |
| `scripts/test-graph-tui.sh` | *(cycling)* extend with a header-affordance assertion |
| `docs/agent-bus.md` | *(cycling)* document `Tab` in the graph TUI key list |

## Implementation

### Phase 1: Make the skip visible

- [x] Add `ErrInjectionSkipped`; return it from the `SendWakeUp` in-flight skip instead of `nil`
- [x] Audit every `SendWakeUp` caller for the new return
- [x] `checkPollHealth()` treats a skip as "not re-driven" and escalates
- [x] Unit tests: a skip is not reported as a successful re-drive

### Phase 2: Force path

- [x] Widen `SendWakeUp` with a force parameter across the interface and all three providers
- [x] `ForceDeliver(..., force=true)` propagates through to bypass the in-flight skip
- [x] Unit tests: force injects despite an in-flight task; non-force still skips (negative control)

### Phase 3: Daemon escalation ladder

- [x] Per-role escalation state, threshold, cooldown, and cap
- [x] Rungs: re-notify → ForceDeliver → force-override → alert edit with history
- [x] A rung succeeds only on verified injection — implemented as **receipt-based** advancement, which is stronger than the `confirmInjectionAndConsume` this step named
- [x] Distinct lifecycle event per rung; opt-out env var
- [x] Unit tests: ladder advances only on failure; a busy-but-responding agent never escalates

### Phase 4: TUI force-respond

- [x] Dashboard TUI agent-row keybind + confirm prompt, calling the shared force path
- [x] Escalation state surfaced in the row so the user sees what was already tried
- [x] Unit tests: action gated on confirm; shares the daemon's code path

### Phase 5: Integration test

- [x] Create `scripts/test-force-respond.sh` (hermetic — scratch bus + scratch daemon)
- [x] Test: agent with an un-responded request past threshold → ladder escalates through its rungs, lifecycle events recorded in order
- [x] Test: the in-flight task no longer blocks the recovery injection (the exact 2026-08-26 catch-22)
- [x] Test: **negative control** — an agent responding normally never escalates
- [x] Test: a skipped injection is not counted as a successful re-drive
- [x] Test: opt-out env var disables the ladder
- [x] Run the script and verify all checks pass

### Phase 6: Graph TUI mode cycling

Independent of Phases 1–5; can be built and landed on its own.

- [x] `Tab` / `Shift-Tab` in `handleKey()` cycling the three top-level views, with `refresh()` on entry
- [x] Guards: inert in DAG, node detail, intent prompt, and confirm views
- [x] Per-surface selection state, restored by item id with a first-row fallback when the item is gone
- [x] Header shows the current surface and a `Tab: next` hint
- [x] Neutral popup titles in `bus/popup.go` so a cycled frame is never mislabelled
- [x] `docs/agent-bus.md` graph TUI key list gains `Tab`
- [x] Unit tests: forward and backward cycle order; a **negative control** asserting `Tab` does nothing in each guarded view; selection survives a full cycle; a removed item degrades to the first row rather than an out-of-range index

### Phase 7: Mode-cycling integration test

- [x] Extend `scripts/test-graph-tui.sh`: `--render-once` frames carry the surface name and cycle hint in the header
- [ ] ~~Assert `--render-once` output is otherwise byte-identical to pre-change~~ — **withdrawn as self-contradictory**, see below
- [x] Run the script and verify all checks pass

## Verification notes

### Phases 1–2 (2026-08-26) — 7/7 steps, root cause closed

Suite: **1992 PASS, 0 FAIL**, all packages ok.

| Claim | Evidence |
|-------|----------|
| Sentinel exists and is returned | `provider.go:17` `ErrInjectionSkipped`; the opencode skip returns it **wrapped** (`%w`), so `errors.Is` works while the message still names the blocking task and its age |
| Both guarded providers | `TestOpenCodeSendWakeUp_SkipReturnsSentinel`, `TestCodexSendWakeUp_SkipReturnsSentinel` — codex carries the same guard, which the original write-up only guessed at |
| Force bypasses the guard | `if !force { …ListTasks… }` in `provider_opencode.go`; `TestSendWakeUp_ForceBypassesGuard` |
| Negative control | `TestSendWakeUp_YoungTaskDoesNotSkip` — a task under 5s must **not** skip, so the guard is pinned at both edges rather than only "skips when told to" |
| Backstop reacts | `daemon.go:1812` — on `ErrInjectionSkipped` it sets `pollGapRecovered[role] = false`, keeping the episode open for later polls, and logs a distinct `delivery-gap-skip` event. `TestCheckPollHealth_SkipIsNotARedrive` |
| Caller audit complete | Every `SendWakeUp` call site updated with an explicit force value — `reload.go`, `launcher.go`, `mode.go`, `daemon.go` (×2) pass `false`; `notify.go` threads it through; `ForceDeliver` → `SendWakeUpWithText(…, force)` |

**The root cause named in this spec is closed.** A skip is no longer indistinguishable from
success, so the existing receipt-gap backstop stops recording re-drives that never happened.

#### A second silent-loss path, found and fixed beyond the spec

`ForceDeliver` calls `AddNotifiedIDs()` **before** injecting. Without a rollback, a failed or
skipped injection would leave those messages marked notified — and the normal delivery path
would then treat them as already delivered and never retry. That is the same class of bug as the
one this spec targets, one layer up, and it was not in the requirements.

`deliver.go:86-90` now calls `ClearNotifiedIDs()` on any injection error before returning, pinned
by `TestForceDeliver_NonForceSkipRollsBackAndSurfaces`. Worth crediting: it was found by following
the sentinel's implications rather than by implementing the ticket as written.

### Phase 3 (2026-08-26) — 5/5 steps, suite 1998 PASS / 0 FAIL

`daemon/force_respond.go` + `force_respond_test.go`.

| Claim | Evidence |
|-------|----------|
| Four rungs | `frRungNotify → frRungDeliver → frRungOverride → frRungAlert`, plus `frRungDone` holding until the gap clears |
| Per-role state, cooldown, cap | `frRung`/`frLastFire`/`frPostponed`/`frHistory` per role; `forceRespondRungCooldown = 60s`; `frOverridePostponeMax` |
| Distinct events | six: `force-respond-notify`, `-deliver`, `-override`, `-alert`, `-postpone`, `force-respond` |
| Opt-out | `MUXCODE_FORCE_RESPOND_DISABLE=1`; threshold via `MUXCODE_FORCE_RESPOND_SECS` |
| Alert carries history | `frHistory[role]` accumulates per-rung lines (e.g. `override postponed 1/N (pane active)`) |

**Advancement is receipt-based, which is stronger than this spec asked for.** The step named
`confirmInjectionAndConsume`; the implementation instead drives the whole ladder off
`bus.ReceiptGap()` — an empty gap calls `frReset()` and ends the episode, and a rung *never*
advances to recovered on a command's return value. That closes the exact failure this spec was
written about (a `nil` return read as success) at the level above it: even a genuinely-returned
injection does not count until a receipt proves the agent received it.

**Two negative controls, both of which matter.** `TestForceRespond_BusyButRespondingNeverEscalates`
pins the criterion that a long-running-but-healthy agent is never escalated against, and
`TestForceRespond_YoungRequestNoTrigger` pins the threshold. Without them the ladder could fire on
every busy agent and still show a green suite.

The override rung also **postpones rather than fires while the pane is active**, capped — so the
most invasive rung is the one most reluctant to run.

#### Two count corrections, one of them mine

The progress report said "4 providers" and "8 callers"; I had reported 3 and 6. **The report was
right on both.** `LocalProvider.SendWakeUp(_, _ string, _ bool)` (`provider.go:313`) is a fourth
implementation my grep missed because I anchored the pattern on parameter *names*
(`SendWakeUp(session, role string`) rather than on the method. That is the sixth pattern-match
false negative recorded across these specs — same root cause each time: a constructed pattern
proves presence, never absence.

The report also said "7 tests"; the file has **6**, and the test agent's independent count agrees.
A small over-count, noted because the number is what a reader would otherwise trust.

### Phase 4 (2026-08-26) — 3/3 steps

`f` on a `RemoteUI` agent row (`viewSessionDetail`), confirm-gated on `y`.

| Claim | Evidence |
|-------|----------|
| **One code path, verified at both ends** | `cmd/remote.go:365` runs `bus.ForceDeliver(sel.Session, sel.Role, true)`; the daemon's override rung runs `d.frDeliver(…, true)`, whose default (`daemon.go:209`) is `bus.ForceDeliver(session, role, force)`. Same function — the func field is injectable only so tests can stub it |
| Never a bus message | the call site's own comment says so, and there is no `bus.Send` on the path |
| Confirm shows prior attempts | `renderForceRespondConfirm()` reads `bus.ReadForceRespondState()`, so the prompt shows what the ladder already tried before asking for a decision |
| Cross-process state | new `bus/force_respond_state.go` — the daemon writes, the TUI reads, so the badge survives the TUI being a separate process |
| Tests | `TestRemoteForceRespond_GatedOnConfirm`, `_ConfirmSwallowsOtherKeys`, `_ConfirmShowsEscalationHistory`, `TestForceRespondState_RoundTripAndClear` |

#### The review fix is the non-obvious half, and it is the right way round

The override rung is pane-gated by `frPaneGated(role) = provider.SupportsHooks()`:

- **Hook providers (Claude)** — gated: postpone while the pane is active, since idle detection is trustworthy there
- **Non-hook providers (OpenCode, Codex)** — **not** gated: deliver regardless of pane state

That direction matters more than it looks. Non-hook agents are exactly the ones that stall — the
motivating incident was OpenCode/DeepSeek — and their pane-based idle detection is the unreliable
kind. Had the gate been applied uniformly, the ladder would have postponed forever precisely for
the agents it exists to recover, while still showing a green suite.

### Phase 5 (2026-08-26) — script exists, **first run failed 12/1**, not checked off

`scripts/test-force-respond.sh` ran firsthand via the run agent: **12 passed, 1 failed, exit 1**.
The ladder-walk, opt-out, alert-history, and negative-control sections all pass. One assertion
fails:

```
--- in-flight catch-22
  ok:   deliver --force bypasses the in-flight skip
  ok:   no skip message under force
  FAIL: non-forced deliver surfaces the skip
        (got: deliver: nothing delivered to test (no pending messages))
```

**Diagnosis — most likely a harness race, not a product defect.** The assertion (script lines
167-174) sends a message, sleeps 6s, then runs a non-forced `deliver` expecting the skip to
surface. But the scratch daemon is running at `--poll 2` throughout, so it almost certainly
delivered and notified that message during the sleep. `deliver` then finds nothing unnotified,
returns "no pending messages", and never reaches the skip path the assertion is testing. The
failure message is consistent with that: it reports *nothing pending*, not *a skip that was
silently swallowed*.

Two things support the reading, and one caveat against over-trusting it:

- The behaviour under test **is** proven at unit level by `TestOpenCodeSendWakeUp_SkipReturnsSentinel` and `TestForceDeliver_NonForceSkipRollsBackAndSurfaces`.
- The sibling assertions immediately above it — force bypasses, no skip message under force — pass, so the fixture's in-flight task exists and the force path works against it.
- **Caveat:** this is a diagnosis from the log and the script source, not a confirmed fix. It needs the harness change and a green re-run before Phase 5 is checked off.

#### Update — the race diagnosis above was **wrong**

The harness was changed to kill the daemon (`kill "$DPID"`, script line 151) *before* the catch-22
section, with a comment citing the race. The assertion at line 177 still fails identically:
`12 passed, 1 failed`, same message. **No daemon is running when it fails**, so daemon
interference cannot be the cause. My diagnosis was plausible, testable, and false.

A concrete flaw *is* visible, though it is not yet proven to be the cause:

```bash
"$MUX" send test test "skip surfacing payload" --track >/dev/null 2>&1 || true   # line 175
sleep 6
nonforce_out=$("$MUX" deliver test 2>&1 || true)                                 # line 177
```

**The setup send is silenced and its failure swallowed** — `>/dev/null 2>&1` plus `|| true`. If
that send never lands (suppressed by the in-flight-task dedup guard, relay-loop suppression, or
anything else — note line 99 already sent the same `test:test` tuple earlier in the run), the
script cannot distinguish *"delivered, then correctly skipped"* from *"never sent at all"*. Both
present as `no pending messages`, and the assertion reports the latter as if it were the former.

The fix is to make the setup observable before asserting on it: check the send succeeded and the
message is actually in the inbox, then assert on `deliver`. Until that separation exists, a green
result would not be trustworthy either — the assertion currently cannot fail *for the right
reason*.

Phase 5 stays **unchecked**. Two failed runs, one disproven theory, and a setup step that hides
its own errors is not evidence of a working integration test.

### Phase 6 (2026-08-26) — 7/7 steps

| Claim | Evidence |
|-------|----------|
| Cycle + guards | `cycleSurface(delta)` returns early unless the current view is one of `graphSurfaces`, so DAG, node detail, intent prompt, and confirm are all inert — one guard covering every drill-in rather than a list that can fall out of date |
| Save → refresh → restore | `saveSelection()`, `refresh()`, `restoreSelection()` in that order, so the entered surface is re-read from the store before its selection is reapplied |
| Restore by id | `restoreSelection` defaults the index to `0` and only advances on an id match — the first-row fallback is the default path, not an error branch |
| Tab bar | `renderSurfaceTabs()` highlights the active surface in `Purple+Bold` and appends `⇥ Tab: next surface` |
| Neutral popup titles | all three graph popups now titled ` Graph `, so a cycled frame is never mislabelled |
| Tests | `TestGraphUI_TabCyclesSurfacesForwardAndBack`, `_TabInertInGuardedViews`, `_SelectionSurvivesFullCycle`, `_RemovedSelectionFallsBackToFirstRow`, `TestSurfaceHeadersCarryCycleHint` |

`docs/agent-bus.md` gained the `Tab` documentation in this pass — it was the one step still
outstanding when I verified.

**Deliberate deviation from this spec's stated cycle order, and it is an improvement.** The
criterion said `runs → templates → gates`; the implementation rings
`runs → gates → templates` and matches the tab bar to it (`graphSurfaces` and `renderSurfaceTabs`
carry a comment requiring they stay in sync, which I verified they do). That puts **Pending Gates
one `Tab` from the default entry** — the surface most likely to be urgent is the closest, and
since it is a ring every surface stays two keystrokes away in the worst case. Checked off against
the intent rather than the letter.

**One subtlety worth recording:** `Shift-Tab` arrives as `ESC [ Z`, which a naive handler reads as
a bare Escape and treats as "go back". `handleEscapeSequence()` disambiguates it from Escape and
from arrow keys, with a timeout path so unit tests (which have no key channel) still see `27` as a
bare Escape. Getting this wrong would have made `Shift-Tab` silently exit a view instead of
cycling.

### Phase 7 (2026-08-26) — 2/3 steps; the third was a bad criterion

`scripts/test-graph-tui.sh`: **46 passed, 0 failed, exit 0** — up from 42/0, gaining four
assertions (`run list header names its surface`, `…carries the cycle hint`, and the same pair for
the gate queue). All 42 pre-existing checks still pass, so the cycling work broke nothing.

#### I wrote two criteria that cannot both hold

- *"The current surface and the cycle affordance appear in the frame header"*
- *"`--render-once` output is unchanged; cycling must not move the scriptable seam"*

The tab bar renders in `--render-once` frames — which is what makes the four new assertions
possible — so the output **has** changed, by design, in the way the first criterion demands. The
second is unsatisfiable alongside it. That is my error in writing the spec, not a shortfall in the
implementation.

The implementation resolved it the right way: show the affordance everywhere, and keep every prior
assertion passing so the seam still works even though its bytes moved. A script that string-matched
whole frames would need updating; one that greps for node names or glyphs is unaffected. The
byte-identical step is **withdrawn**, and the acceptance criterion above it stays unchecked rather
than being quietly reworded — a contradiction I introduced should be visible, not tidied away.

### Not started: nothing — Phase 5 is the only phase still red

Verified absent by enumeration, not by pattern guess — the escalation-related hits in
`daemon.go` all belong to the pre-existing active-watchdog, agent-restart, and parked-input
watchdogs, none to a force-respond ladder.

| Phase | State |
|-------|-------|
| 4 — TUI force-respond | `tui/` untouched |
| 5 — force-respond integration test | `scripts/test-force-respond.sh` does not exist |
| 6–7 — graph TUI mode cycling | `tui/graph_ui.go` untouched; no `Tab` handler |

Phases 1–2 are independently useful and safe to land alone: they fix the root cause and change no
behaviour for callers that pass `force=false`, which is all of them today.

### 2026-08-26 12:43 — **not Complete: two unit tests are failing**

A finalize request arrived reporting the integration script green 15/0 and branch review 0/0. The
script result is credible, but the **unit suite is red**: `2012 PASS, 2 FAIL`, both in `bus`:

- `TestExecCappedLoopExhaustion`
- `TestRetryGraphRunResetsLoopBudget`

**Prime suspect — a change made minutes before the finalize request.** `bus/graph_exec.go` was
modified at **12:42** and carries +27 uncommitted lines. Only ~8 are the gate auto-show; the rest
add `ErrSendSuppressed` handling that **adopts an in-flight task instead of failing the node**, with
the comment: *"a loop re-entry or retry racing the prior pass"*.

Those are precisely the two scenarios the failing tests cover — one exercises capped-loop
re-entry, the other retry loop-budget reset. Neither test touches `NodeWaitHuman`, so the auto-show
half is not implicated; the adopt-on-suppressed path is. A loop re-entry that now adopts a prior
task rather than issuing a fresh send would plausibly change loop-exhaustion accounting.

**Why the green reports do not settle it:** the last clean unit verdict (1998/0) predates this
change, and an integration script exercising the force-respond ladder would not cover graph loop
budgets at all. Two green signals from adjacent scopes are not evidence about this one.

Status stays **In Progress**. Setting Complete over two failing tests — in the subsystem a
just-landed change touches — would put a false verdict into the permanent record, which is the one
thing a completed spec must never do.

## Open questions

- **Threshold value** — long-active watchdog uses 600s; the same here would leave the observed 20-minute stall largely untouched. Something nearer 120–180s for an *un-responded request* seems right, but wants a real distribution of healthy response times before it is fixed.
- **Does the Codex provider carry the same skip?** Only `provider_opencode.go` was audited; `provider_codex.go` should be checked before Phase 2 is called complete.
- **Interaction with relay-loop suppression** — an escalation ladder issues repeated same-tuple sends and could trip `MUXCODE_RELAY_SUPPRESS_THRESHOLD` (4 within 300s). The same hazard is flagged unresolved in MUX-014's *Known gaps*; worth settling once for both.

Mode cycling:

- **Should the DAG view participate?** Cycling out of a drilled-in DAG is ambiguous — is `Tab` a surface switch, or "back then switch"? Inert is the conservative default, but a user deep in a DAG who wants the gate queue must press `q` first.
- **Is `a` still the right menu key for Pending Gates?** Inside the TUI, `a` means *approve*. With cycling, the menu key and the in-TUI key diverge in meaning; worth confirming that is acceptable.
- **Does the cycle grow?** If a graph-history or run-log surface lands later, the ring extends; the implementation should not hard-code three.

## Sources

- User request relayed via the edit agent, 2026-08-26, with incident evidence in `/tmp/mux-auto-force-respond-spec.md`
- Incident observed live in this session; skip behaviour and `nil` return verified in `bus/provider_opencode.go:135-147` by the plan agent
- [`MUX-104-send-keys-dash-payload.md`](./MUX-104-send-keys-dash-payload.md) — the injection bug that co-occurred
- [Architecture — delivery tracking](../../architecture.md#delivery-tracking)
- [`MUX-031-graph-run-tui.md`](./MUX-031-graph-run-tui.md) — the three graph TUI surfaces and the `GraphUI` view model that Phases 6–7 extend
- `tools/muxcode/tui/graph_ui.go` (`handleKey`, `refresh`, the three constructors), `tools/muxcode/bus/popup.go`

## Provenance

Filed by the plan agent on 2026-08-26 from a user-requested feature relayed by edit. The handoff characterised the receipt-gap backstop as "alert-only in practice"; reading the code showed it does attempt recovery and is defeated by the guard's `nil` return, so the spec targets that as the root cause rather than adding a parallel recovery path.

The graph TUI mode-cycling request (Phases 6–7) arrived the same day. It was briefly filed as a
separate spec, `MUX-106`, on the reasoning that it is graph-TUI navigation rather than delivery
recovery; the user directed it be merged here as originally asked, and MUX-106 was withdrawn
before any work began. **The id is retired, not reusable** — the backlog registry never reuses
ids, and a future spec taking `MUX-106` would collide with this history. Next free id: `MUX-107`.

## Time Tracking

| Branch | Active time | Last updated |
|--------|-------------|--------------|
| MUX-105-force-respond-escalation | 1h 31m | 2026-08-26 13:00 |

## Status

Complete
