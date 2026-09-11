# The Stall Watchdog Re-Drives Into a Busy Claude Agent and Its Escape Preamble Kills the Running Tool

`checkStalledTasks` decides an agent has stalled on a consumed task when its pane shows the `❯`
prompt (`PaneHasIdlePrompt`) and nothing is pending. Claude Code keeps the `❯` composer on screen
while a tool call runs, so a busy Claude agent reads as idle — a fact two other daemon sites already
state in their comments and gate around with `PaneShowsRecoverableIdle`. After `TaskStallSecs` and
two consecutive sightings the watchdog force-delivers; with nothing unnotified that becomes
`redriveInFlightTasks` → `SendWakeUpWithText(force)`, whose Escape-before-payload preamble is
Claude's tool-interrupt key. The running command dies (`Interrupted · What should Claude do
instead?`), the re-drive text is the next prompt, the agent restarts the command, and the second
re-drive kills the restart. Net effect: any run-agent command longer than about two minutes cannot
finish. On 2026-09-09 that was MUX-167's integration script; the user ran it by hand.

## Context

### Observed (session `muxcode`, run agent, task `1788982499 edit→run:run`, 2026-09-09)

| When | What | Source |
|------|------|--------|
| 15:35:00 | edit → run `request:run` "run exactly this one command …: `bash scripts/test-multi-phase-graph.sh`" — a multi-minute integration script | history, lifecycle `inbox-notify run` |
| ~15:35:30 | run (Claude) consumes it through its listener and starts the Bash call | run pane |
| 15:37:24 | `deliver force-redrive run: 1 in-flight task(s)`; `daemon task-stall-redrive edit→run:run redrive 1/2` | lifecycle |
| 15:38:25 | `redrive 2/2` | lifecycle |
| 15:38 | run → edit `response:run-blocked`: "execution was rejected/interrupted twice at the tool-use level before running (no script output produced). Halting retries" | `muxcode history run` |
| 15:47 | run pane: the Bash call shows `⎿ Interrupted · What should Claude do instead?` followed by the injected `❯ Re-drive (consumed but never completed): New message from edit [request:run] …` — twice | pane capture (edit) |
| later | the user types "go ahead and run it" into the run pane by hand | edit's account |

### Mechanism — verified in code

- `daemon/daemon.go:2926` `checkStalledTasks` — `:2943` `TaskStalled(t, now, TaskStallSecs())`
  (90 s), `:2956` the idle test is `PaneHasIdlePrompt(content)` with `!HasPendingInput`; `:132`
  `taskStallSeen` debounces to two consecutive 30 s sightings; then `ForceDeliver(session, role,
  true)`.
- `daemon/daemon.go:2337–2338` and `:2489` — two sibling checks say it outright: "Gate on
  `PaneShowsRecoverableIdle`, NOT `PaneHasIdlePrompt`: the ❯ composer renders even mid-turn, so
  `PaneHasIdlePrompt` is true for a busy agent". The stall watchdog uses the weaker test they reject.
- `bus/deliver.go:117` `redriveInFlightTasks` and `:145` `RedriveTask` — both end in
  `SendWakeUpWithText(session, role, provider, text, true)` (`:132`, `:158`); `:101` is the
  `ForceDeliver` road into them.
- `bus/notify.go:871` `SendWakeUpWithText` — the typed injection, preceded by `TmuxDismissOverlay`
  (Escape → `C-e` absorber; [MUX-163](../completed/MUX-163-prompt-inject-escape-eats-first-char.md)).
  On an idle pane the Escape clears an overlay; on a busy pane it is the interrupt. The Escape was
  there before MUX-163 — the preamble made it reliable, not new.
- `bus/graph_exec.go:1302–1305` `graphAgentIdleFn = IsAgentIdle` — the executor's own redrive path
  was given the provider-aware test on 2026-09-09 for exactly this class ("redriven into its running
  turn twice"); the stall watchdog was not.
- `bus/notify.go:547` `IsAgentIdle` and `bus/provider_claude.go:112` `ClaudeCodeProvider.IsIdle` —
  the provider-aware answer the watchdog should be asking.

### Scope boundary

The Escape preamble stays: an idle pane's overlay eats the Enter without it (MUX-163), and the
defect is *when* the wake fires, not *how*. The stall cadence stays. What changes is that no road —
watchdog, receipt-gap recovery, `deliver --force`, remote — re-drives a pane that is working.

## Requirements

### Acceptance criteria

- [x] The stall watchdog never re-drives a Claude agent whose pane shows a running tool call (spinner / "esc to interrupt"), `❯` on screen or not; the stall sighting resets and a `stall-skipped-busy` row is written once per task
- [x] An idle prompt with no running tool still re-drives after `TaskStallSecs` (negative control — the watchdog keeps catching real stalls)
- [x] `redriveInFlightTasks` and `RedriveTask` refuse a busy pane themselves (`redrive-skipped-busy`), so `deliver --force`, receipt-gap recovery and remote callers cannot interrupt a running tool either
- [x] A run-agent command longer than 2 × `TaskStallSecs` (default 90 s, so > 180 s) runs **in the foreground**, completes, and its reply lands, with **no `Interrupted` pair in the pane** — and the gate is shown to be what saved it: at least one `stall-skipped-busy` row naming that task. _Strengthened 2026-09-10 16:12 after a near-miss (see Notes): a backgrounded command satisfies every clause of the original wording while leaving the pane idle throughout, so the criterion would pass identically with the busy-gate deleted. The foreground requirement and the `stall-skipped-busy` row are the discriminating parts — without them this is a green check that tests nothing._ **Closed 2026-09-10 16:36 on the strengthened wording, all four clauses verified by plan:** `bash scripts/test-prompt-mode.sh` run **in the foreground** through the run agent 16:17:55–16:25:53 (~478 s, well past 180 s), reply landed at 16:25 (`run → edit [response:run]`), no `Interrupted` pair in the pane, and the discriminating row present — **`16:19:55 stall-skipped-busy edit→run:run working — re-drive withheld`**. That row is the non-vacuous part: the watchdog *tried* to re-drive mid-run and the busy gate stopped it, which is the exact kill this spec exists to prevent — under the pre-fix behaviour that run would have died. It also independently corroborates the foreground requirement, since the row can only fire on a **busy** pane, which a backgrounded call would not have produced.
- [x] Docs: watchdog tables in `docs/architecture.md` and `docs/hooks.md`, `CLAUDE.md` "Daemon watchdogs" bullet

### Technical approach

**Primary — ask the provider, and refuse at the helper too.** `checkStalledTasks` replaces
`PaneHasIdlePrompt` with `bus.IsAgentIdle` (provider-aware; `ClaudeCodeProvider.IsIdle` must read a
running tool call — spinner, "esc to interrupt" — as busy, extended if it only looks for `❯`) and
treats not-idle as busy: reset `taskStallSeen`, one `stall-skipped-busy` row per task. Defence in
depth: `redriveInFlightTasks` and `RedriveTask` check the same predicate and log
`redrive-skipped-busy`, so every caller inherits the rule.

**Rejected — drop the Escape from forced wakes.** Breaks the idle case MUX-163 fixed and leaves the
real fault — waking a working agent — in place.

**Rejected — lengthen `TaskStallSecs`.** A twenty-minute script is still killed, later.

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/daemon/daemon.go` | `checkStalledTasks` (2926–2993), `taskStallSeen` (132), the sibling gates that already know (2337, 2489) |
| `tools/muxcode/bus/deliver.go` | `ForceDeliver` road (101), `redriveInFlightTasks` (117), `RedriveTask` (145) |
| `tools/muxcode/bus/notify.go` | `PaneHasIdlePrompt` (488), `IsAgentIdle` (547), `SendWakeUpWithText` (871) |
| `tools/muxcode/bus/provider_claude.go` | `IsIdle` (112) — the running-tool signals |
| `tools/muxcode/bus/graph_exec.go` | `graphAgentIdleFn` (1302–1305) — the precedent |
| `docs/architecture.md`, `docs/hooks.md`, `CLAUDE.md` | watchdog tables and bullet |

## Implementation

### Phase 1: The watchdog asks the provider

- [x] `checkStalledTasks`: `bus.PaneShowsRecoverableIdle` in place of `PaneHasIdlePrompt` (the predicate the sibling gates at `daemon.go:2337`/`:2489` already use, on the same widened capture — chosen over `IsAgentIdle` at implementation time because it reuses the existing capture and matches those siblings); not-idle → reset `taskStallSeen`, `stall-skipped-busy` once per task
- [x] `ClaudeCodeProvider.IsIdle`: verified (or extended) to read a running tool call as busy — spinner line, "esc to interrupt" — _verified already true at implementation; no change needed_
- [x] Tests: a pane fixture with `❯` plus an active spinner → no redrive, sighting reset, one row; an idle fixture without a spinner → redrive after `TaskStallSecs` (negative control)

### Phase 2: The helpers refuse

- [x] `redriveInFlightTasks` and `RedriveTask` refuse a busy pane and log `redrive-skipped-busy`
- [x] Tests: the helpers refuse the busy fixture; `ForceDeliver` on the idle fixture still re-drives (negative control)

### Phase 3: Docs

- [x] Watchdog tables in `docs/architecture.md` and `docs/hooks.md`; `CLAUDE.md` "Daemon watchdogs" bullet — busy panes are never re-driven, from any road

### Phase 4: Integration test

- [x] Hermetic script (scratch `BUS_SESSION`, scratch pane rendering a Claude-shaped busy frame — `❯` plus spinner — with an in-flight task older than `TaskStallSecs`, the daemon on a short `MUXCODE_TASK_STALL_SECS`): assert the pane content is untouched after two check intervals and the `stall-skipped-busy` row exists
- [x] Negative control: the same with an idle frame → `task-stall-redrive` row and the re-drive text in the pane
- [x] Run the script and record the counts in this spec

## Notes

### Near-miss: the criterion nearly closed on a vacuous pass (2026-09-10)

Edit flagged this at 16:05 and it holds up in the records; plan verified before changing anything.

A `bash scripts/test-prompt-mode.sh` run through the run agent lasted **7 m 14 s** (15:46:55 →
15:54:09), comfortably past 2 × `TaskStallSecs` (180 s), completed, and produced no `Interrupted`
pair. By the original wording of the live criterion, that closed it.

It did not, because the run agent **backgrounded** the script (the artefact is on disk:
`tasks/bqxzncyob.output`, ending `[exited with code 1]`). A backgrounded call leaves the agent's pane
idle, so:

| | |
|---|---|
| Was the pane ever busy? | No — the work ran outside the turn |
| Did the busy-gate fire? | **No** — zero `stall-skipped-busy` rows between 15:46:55 and 15:54:09; the six in this session are all 15:59 or later, none for this task |
| Would it have passed with the gate deleted? | **Yes** — nothing threatened the run |

The script survived because nothing was working in the pane, not because the watchdog refused to
re-drive a busy one. That is the exact failure this project checks for elsewhere — a green result
that would still be green if the feature were removed.

**Same root behaviour as [MUX-176](../backlog/MUX-176-run-chain-fires-success-on-backgrounded-call.md).**
The backgrounding that hollowed out this criterion is the same backgrounding that made the run chain
fire "Run succeeded" 7 m before the script failed. One behaviour, two consequences: a premature
success edge, and a live check that could not fail. Closing MUX-176's Phase 2 (an incomplete call is
not chain evidence) would also make this criterion's foreground requirement enforceable rather than
merely stated.

The synthetic proof is unaffected — `scripts/test-stall-redrive-busy.sh` drives a real busy frame and
asserts the `stall-skipped-busy` row with a resting-pane negative control (Phase 4, ticked). What
stays open is only the live confirmation, and it now has to be a foreground run.


- Filed 2026-09-09 16:02 by plan from edit's handoff (`mux-171-handoff.md`, session `03a897a2`);
  the lifecycle rows, run's `run-blocked` reply and the code lines above re-verified here. The pane
  capture is edit's.
- MUX-167 Phase 4 is what this blocked: `test-multi-phase-graph.sh` was never run through the run
  agent under the graph's rule that workers verify via the run agent, not `go test`.
- Related: [MUX-112](./MUX-112-idle-task-rescue-closes-live-work.md) (the synthetic-reply sibling —
  the same "❯ means idle" error closing live work from the other side);
  [MUX-123](./MUX-123-stall-watchdog-selective-misses.md) (the watchdog's misses; this is its false
  positive); [MUX-163](../completed/MUX-163-prompt-inject-escape-eats-first-char.md) (the preamble);
  [MUX-170](./MUX-170-graph-dispatch-adopts-foreign-in-flight-task.md) (found in the same hour).

## Implementation state (2026-09-09 17:05)

Phases 1 and 2 are written in the tree (uncommitted) and **not yet verifiable** — recorded here
rather than as ticks, because the evidence refuses them:

| Phase | In tree | Evidence |
|-------|---------|----------|
| 1 | `checkStalledTasks` on `PaneShowsRecoverableIdle`, capture 30 → 200, `stallBusyLogged` map, `stall-skipped-busy` once per task; a false claim in its own doc comment corrected | **should-fix** (review 16:59:49, `daemon.go:2973`): the debounce reset, once-per-task logging and map cleanup have no daemon-level test — reverting the predicate still passes the suite |
| 2 | `paneBusy` in `deliver.go`, refusing inside `redriveInFlightTasks` and `RedriveTask` with `redrive-skipped-busy`; fails **open** on a capture error | **2 must-fix** (review 16:59:49) — see below |
| 3 | — | docs, blocked on the must-fixes (the provider scope changes what the table would say) |
| 4 | `scripts/test-stall-redrive-busy.sh`, floor 9, paired busy/resting controls, ~45 s live-session | **not run** |

The two must-fix findings, both `deliver.go`:

- **`:123` provider scope.** `paneBusy` applies `PaneShowsRecoverableIdle` — which requires the
  literal Claude prompt `U+276F` — to every provider. An idle Codex pane (`U+203A`) or an OpenCode
  pane is therefore classified *busy*, and `ForceDeliver` consumed-task recovery and `RedriveTask`
  now refuse panes they used to recover. The reviewer's caution is worth keeping: do **not**
  substitute `Provider.IsIdle` blindly, because `CodexProvider.IsIdle` currently always returns
  false. Non-Claude idle/busy controls are required.
- **`:149` guard placement.** The refusal is reached only when `ForceDeliver` finds no unnotified
  messages. With an actionable pending row — including rows a force resets from notified —
  `ForceDeliver` bypasses `redriveInFlightTasks` and calls `SendWakeUpWithText(…, true)` directly,
  so receipt-gap recovery, remote and manual `deliver --force` can still drive the interrupting
  Escape into a working Claude pane. The guard has to sit ahead of every forced pane mutation,
  including the parked-input and marker clearing.

**17:07 revision.** Both original must-fixes are **resolved** — the review confirms it — and
`paneBusy` is gone, replaced by an `AgentIsWorking` guard at `ForceDeliver` entry (a genuine
reduction: one gate instead of two). The review nonetheless returns `EXIT=1` again, on a **new**
must-fix of the same shape one layer down:

- **`deliver.go:57` (also `daemon.go:2980`, `RedriveTask`) — capture scope.** `AgentIsWorking` feeds
  the *whole* capture to `isClaudeThinking`, and `TmuxCapturePaneLines(…, 12)` is `capture-pane -S
  -12`: the visible pane **plus history**, not the bottom 12 lines. An idle pane with a quoted old
  spinner or "esc to interrupt" line anywhere above its live footer is refused *indefinitely*,
  defeating force recovery altogether. `diagnose.go:639–653` already documents and solves exactly
  this false positive with `paneLiveTail`; the fix is to reuse that primitive rather than add a
  second spinner parser, with a stale-spinner-above-idle-tail negative control beside a live-spinner
  control.

Two script findings matter for Phase 4, because they mean the script cannot currently pass: its
Phase A still asserts `redrive-skipped-busy` while the code now emits `deliver-skipped-busy` before
entering `redriveInFlightTasks` (`:111`), and its Codex control renders a *Claude* PS1 and seeds no
task, so it passes even against the earlier broken implementation (`:139`) — a control that cannot
fail. The watchdog state-sequence coverage (`daemon.go:2980`) is still absent.

**The suite is red**: `./test.sh` exit 1 at 17:07:33. The stored row's captured output is truncated
to its tail and does not name the failing package or test
([MUX-152](./MUX-152-test-sh-hides-modules-after-first-failure.md)); identifying it belongs to the
test agent. The earlier green at 17:02:42 predates this revision. _(Superseded 2026-09-10 08:40 —
see Resolution below.)_

## Resolution (2026-09-10 08:43)

Every finding above is closed; 13 of 14 boxes are ticked. What landed after the 17:07 revision:

| Finding | Resolution |
|---------|------------|
| `deliver.go:57` capture scope (must-fix) | `AgentIsWorking` (`bus/timetrack.go:229`) feeds `paneLiveTail(out)` to `paneShowsAgentWorking` — the `diagnose.go:667` primitive the reviewer named, not a second spinner parser — and takes the provider through `IsClaudeTUI(ResolveProvider(role))`. Controls: `TestAgentIsWorking_ScopedToLiveTail`, `TestForceDeliver_StaleSpinnerStillDelivers`, `TestAgentIsWorking_ProviderAware` |
| `daemon.go:2973` no state-sequence test (should-fix) | bookkeeping extracted to `noteStallSighting`/`forgetCompletedTasks`, with six tests in `daemon/stall_sighting_test.go`: debounce reset on working and on not-at-rest, once-per-task logging, redrive cap and give-up, map cleanup on completion |
| Script asserted `redrive-skipped-busy` where the code emits `deliver-skipped-busy` (`:111`) | event name corrected |
| Codex control rendered a Claude PS1 and seeded no task — a control that could not fail (`:139`) | the control now overrides `MUXCODE_REVIEW_CLI=codex` for its own command against a pending row, and the rest of the script pins Claude (`MUXCODE_AGENT_CLI=claude …`) — which is also the review's last should-fix |
| Suite red at 17:07:33 | green at 08:40 |

Verification:

| Evidence | Result |
|----------|--------|
| Review, 08:41 | **EXIT=0** — 0 must-fix, 1 should-fix (pin the Claude provider; applied) |
| Suite, 08:40 | `go vet`, `go test -p 1 -count=1 ./...` and `./test.sh` all exit 0, no failing tests |
| `scripts/test-stall-redrive-busy.sh` through the run agent, 08:43 | exit 0 — **14 passed, 0 failed (floor 14)** |
| Live session, 2026-09-10 | `stall-skipped-busy` ×3 (08:29:25, 08:32:28, 08:42:45); `deliver-skipped-busy` ×4 (08:28:24, 08:31:46, 08:41:18, 08:43:42); and **no `task-stall-redrive` row at all** — the interrupting road did not fire once |

Phase 3 docs written by plan on 2026-09-10: `docs/architecture.md` gains a Stall-re-drive row in the
watchdog table and a *No road re-drives a working pane* section contrasting `AgentIsWorking`
(live-turn evidence, provider-aware) with `PaneShowsRecoverableIdle` (finished-at-prompt), both
scoped to `paneLiveTail`; the send-keys paragraph further down — which asserted that "idle agents at
the `❯` prompt are safe to wake … because no tool execution is in progress" — is corrected, that
sentence being the defect's own premise. `docs/hooks.md` carries the rule on the `hook stop` listener
road. The `CLAUDE.md` bullet was updated by edit.

**Still open — AC4 alone.** The criterion names its proof: `scripts/test-multi-phase-graph.sh`-length
work through the run agent, producing no `Interrupted` pair. That script has still not been run
there — it is also MUX-167 Phase 4, which this defect is what blocked. The mechanism is proven
hermetically across two check intervals and by the live rows above; what is missing is the original
failing scenario replayed.

## Status

**Complete — 14/14.** Filed 2026-09-09 16:02; Phases 1–4 implemented and revised through
four reviews (16:59:49, 17:03:24, 17:07:12 all `EXIT=1`; **08:41 on 2026-09-10 `EXIT=0`**). Suite
green 08:40, integration script 14/14 through the run agent 08:43 — **superseded: re-run foreground through the run agent 2026-09-11 12:53:46 at `15 passed, 0 failed (floor 15)`, exit 0, verified in `run-history.jsonl`. The 08:43 count predated this script revision; PR #79 review S6 caught the mismatch against `EXPECTED_PASS=15` and the re-run settled it** — and the live session shows the
skip rows with no re-drive. Phase 3 docs written 2026-09-10.

**AC:63, the last open box, closed 2026-09-10 16:36** on its strengthened wording — a foreground
~478 s run through the run agent carrying the discriminating `stall-skipped-busy edit→run:run` row at
16:19:55. Detail in the criterion itself. The criterion was rewritten at 16:12 the same day precisely
because an earlier 7 m 14 s run met its *original* letter while the script had been **backgrounded**,
leaving the pane idle and the busy-gate unexercised; the close-out therefore rests on evidence the
weaker wording could not have produced.

_Evidence provenance:_ the 16:19:55 row had already **rotated out of `muxcode lifecycle show`** by the
time it was verified (the visible window had advanced to 16:20:05) and was recovered from the raw
JSONL at `~/.config/muxcode/logs/muxcode.log`. Worth noting as a hazard in its own right — a
discriminating lifecycle row can age out of the default view within minutes on a busy session, so
evidence should be captured into the spec when observed, not left to be looked up later.

_The file sits in `drafts/`; the `drafts/` → `completed/` move is a `git mv` and belongs to commit on
the user's word — plan does not move it._

Two follow-ups, neither plan's to perform:

- **Ready to move to `drafts/`** (or straight to `completed/` if the user re-scopes AC4) — the file
  was committed in `ffa0da6`, so the move is a `git mv` and the user's call.
- The tree is uncommitted and ready to commit: `bus/deliver.go`, `bus/timetrack.go`,
  `bus/deliver_test.go`, `daemon/daemon.go`, `daemon/stall_sighting_test.go`,
  `scripts/test-stall-redrive-busy.sh`, plus this spec and the Phase 3 docs.
