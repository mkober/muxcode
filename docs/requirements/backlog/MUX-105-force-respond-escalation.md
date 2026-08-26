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
| Wake injections | Separately failing with `command send-keys: invalid flag -` — now [`MUX-104`](../completed/MUX-104-send-keys-dash-payload.md); no fallback path existed |
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

Today the three [`MUX-031`](../completed/MUX-031-graph-run-tui.md) entries each launch a
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

- [ ] `SendWakeUp` gains a force path (parameter or sibling method) so a recovery injection is never blocked by the stuck request's own in-flight task; the `Provider` interface and all three implementations (`claude`, `opencode`, `codex`) are updated coherently
- [ ] A skipped injection is **distinguishable from a delivered one** by its return value — callers must never read a skip as success
- [ ] `checkPollHealth()` (and any other re-driver) reacts to a skip rather than recording a phantom re-drive
- [ ] Daemon escalation ladder for a request un-responded past a threshold: re-notify → `ForceDeliver` → `ForceDeliver` overriding the in-flight skip → alert edit **with the escalation history**, not just "a gap exists"
- [ ] Each rung emits a distinct lifecycle event, so the ladder is reconstructible after the fact
- [ ] A rung counts as succeeded only when the injection **verifiably landed** (`bus/inject_verify.go` `confirmInjectionAndConsume`), never merely because the command returned
- [ ] TUI force-respond: a single key on the dashboard TUI agent row (and/or remote TUI agent detail) runs the same force path, behind a confirm prompt
- [ ] The TUI action and the daemon ladder share one code path — no second implementation that can drift
- [ ] Escalation is opt-out via an env var, per existing watchdog convention (`MUXCODE_*_DISABLE`)
- [ ] Any role may be a target — unlike auto-clear, there is no `edit`/`auto` exclusion, since edit stalling is itself a failure worth recovering
- [ ] Escalation never fires against an agent that is legitimately busy on a long task — the trigger is an **un-responded request past a threshold**, not mere elapsed activity

### Acceptance criteria — graph TUI mode cycling

- [ ] `Tab` from any of the three top-level surfaces advances in a fixed cycle: runs → templates → gates → runs
- [ ] `Shift-Tab` cycles backwards
- [ ] Cycling works identically regardless of entry point — `g`, `G`, and `a` differ only in where the cycle starts
- [ ] Cycling re-reads the run store for the surface being entered, so a stale frame is never shown
- [ ] Per-surface selection state is preserved across a cycle — returning restores the previous selection where the underlying item still exists, and falls back to the first row where it does not
- [ ] `Tab` is inert in **drill-down** views (DAG, node detail, intent prompt) — those have their own `q`/`Enter` semantics, and cycling out of a half-entered prompt would discard input
- [ ] `Tab` is inert while a confirm prompt is open — a pending approve/cancel/retry must be answered or dismissed, never sidestepped by a mode switch
- [ ] The current surface and the cycle affordance appear in the frame header, so the key is discoverable without documentation
- [ ] Popup titles remain accurate after a cycle, or are made neutral — a gate queue under a `Graph Runs` title is worse than a generic one
- [ ] `--render-once` output is unchanged; cycling is interactive-only and must not move the scriptable seam

### Technical approach

- **Force plumb-through**: widen `SendWakeUp(session, role string, force bool)` on the `Provider` interface. Three implementations change; `provider_opencode.go` is the only one with a skip to bypass. A sibling `ForceWakeUp()` avoids touching the interface but risks the two paths drifting — prefer the parameter.
- **Skip must be visible**: return a sentinel (`ErrInjectionSkipped`) rather than `nil`, so `checkPollHealth` can escalate instead of assuming success. This is the single highest-value change in the spec — it converts a silent failure into a signal the existing backstop can already act on.
- **Ladder state**: per-role escalation state alongside the existing watchdog markers, with cooldown and a cap, following `checkStuckProviders()` conventions (two-sighting debounce, cap, cooldown, then alert and stop).
- **TUI action**: `tui/model.go` agent row keybind → confirm → the shared force path. Reuse the `provider_select.go` confirm-flow pattern.
- **MUX-104 dependency**: the ladder is only as good as the injection beneath it. [`MUX-104`](../completed/MUX-104-send-keys-dash-payload.md) must land first, or a dash-leading payload defeats every rung identically.

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

- [ ] Add `ErrInjectionSkipped`; return it from the `SendWakeUp` in-flight skip instead of `nil`
- [ ] Audit every `SendWakeUp` caller for the new return
- [ ] `checkPollHealth()` treats a skip as "not re-driven" and escalates
- [ ] Unit tests: a skip is not reported as a successful re-drive

### Phase 2: Force path

- [ ] Widen `SendWakeUp` with a force parameter across the interface and all three providers
- [ ] `ForceDeliver(..., force=true)` propagates through to bypass the in-flight skip
- [ ] Unit tests: force injects despite an in-flight task; non-force still skips (negative control)

### Phase 3: Daemon escalation ladder

- [ ] Per-role escalation state, threshold, cooldown, and cap
- [ ] Rungs: re-notify → ForceDeliver → force-override → alert edit with history
- [ ] A rung succeeds only on verified injection (`confirmInjectionAndConsume`)
- [ ] Distinct lifecycle event per rung; opt-out env var
- [ ] Unit tests: ladder advances only on failure; a busy-but-responding agent never escalates

### Phase 4: TUI force-respond

- [ ] Dashboard TUI agent-row keybind + confirm prompt, calling the shared force path
- [ ] Escalation state surfaced in the row so the user sees what was already tried
- [ ] Unit tests: action gated on confirm; shares the daemon's code path

### Phase 5: Integration test

- [ ] Create `scripts/test-force-respond.sh` (hermetic — scratch bus + scratch daemon)
- [ ] Test: agent with an un-responded request past threshold → ladder escalates through its rungs, lifecycle events recorded in order
- [ ] Test: the in-flight task no longer blocks the recovery injection (the exact 2026-08-26 catch-22)
- [ ] Test: **negative control** — an agent responding normally never escalates
- [ ] Test: a skipped injection is not counted as a successful re-drive
- [ ] Test: opt-out env var disables the ladder
- [ ] Run the script and verify all checks pass

### Phase 6: Graph TUI mode cycling

Independent of Phases 1–5; can be built and landed on its own.

- [ ] `Tab` / `Shift-Tab` in `handleKey()` cycling the three top-level views, with `refresh()` on entry
- [ ] Guards: inert in DAG, node detail, intent prompt, and confirm views
- [ ] Per-surface selection state, restored by item id with a first-row fallback when the item is gone
- [ ] Header shows the current surface and a `Tab: next` hint
- [ ] Neutral popup titles in `bus/popup.go` so a cycled frame is never mislabelled
- [ ] `docs/agent-bus.md` graph TUI key list gains `Tab`
- [ ] Unit tests: forward and backward cycle order; a **negative control** asserting `Tab` does nothing in each guarded view; selection survives a full cycle; a removed item degrades to the first row rather than an out-of-range index

### Phase 7: Mode-cycling integration test

- [ ] Extend `scripts/test-graph-tui.sh`: `--render-once` frames carry the surface name and cycle hint in the header
- [ ] Assert `--render-once` output is otherwise byte-identical to pre-change for a fixed fixture, proving the scriptable seam did not move
- [ ] Run the script and verify all checks pass

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
- [`MUX-104-send-keys-dash-payload.md`](../completed/MUX-104-send-keys-dash-payload.md) — the injection bug that co-occurred
- [Architecture — delivery tracking](../../architecture.md#delivery-tracking)
- [`MUX-031-graph-run-tui.md`](../completed/MUX-031-graph-run-tui.md) — the three graph TUI surfaces and the `GraphUI` view model that Phases 6–7 extend
- `tools/muxcode/tui/graph_ui.go` (`handleKey`, `refresh`, the three constructors), `tools/muxcode/bus/popup.go`

## Provenance

Filed by the plan agent on 2026-08-26 from a user-requested feature relayed by edit. The handoff characterised the receipt-gap backstop as "alert-only in practice"; reading the code showed it does attempt recovery and is defeated by the guard's `nil` return, so the spec targets that as the root cause rather than adding a parallel recovery path.

The graph TUI mode-cycling request (Phases 6–7) arrived the same day. It was briefly filed as a
separate spec, `MUX-106`, on the reasoning that it is graph-TUI navigation rather than delivery
recovery; the user directed it be merged here as originally asked, and MUX-106 was withdrawn
before any work began. **The id is retired, not reusable** — the backlog registry never reuses
ids, and a future spec taking `MUX-106` would collide with this history. Next free id: `MUX-107`.

## Status

Backlog
