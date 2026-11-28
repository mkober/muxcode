# Harness Startup Message Exhausts the Tool Loop

**Tracking:** [mkober/muxcode#44](https://github.com/mkober/muxcode/issues/44)

The startup message every agent receives is open-ended prose. A Claude-class model treats it as a
cue to read memory and stop; a small local model treats it as a task with no terminal condition and
burns every available turn on it, emitting `(no response generated — tool loop exhausted)` and
never answering. Found during the [MUX-109](./MUX-109-prompt-mode-graph-control-pane.md) Phase 2
regression check, where build and test repeated this roughly every five minutes.

## Context

### What was observed

During a live regression run on 2026-08-26 (build and test reloaded to `--cli local` on `qwen3:4b`),
both harness agents repeatedly exhausted `MaxTurns` on the startup message and produced no response.
The cycle repeated indefinitely at roughly five-minute intervals.

### Root cause

| Element | Location | Detail |
|---------|----------|--------|
| The message | `bus/launch_reason.go:37` (`startupRestorePayload`; was `bus/launch.go:747` at filing) | `"Session started — review last saved context from memory to restore session state."` |
| The turn budget | `harness/config.go:41` | `MaxTurns: 10` per batch |
| The failure text | `harness/loop.go:557` | `finalResponse = "(no response generated — tool loop exhausted)"` |
| The escape hatch that does not fire | `harness/loop.go` `isSingleShotRole()` | Auto-completes after **one successful tool execution** — build, test, and prompt qualify |

The message asks the agent to "review context and restore state". That has **no completion
predicate**: nothing tells the model when it is finished, so it keeps calling tools until the budget
runs out. Single-shot does not save it, because single-shot triggers on *one successful tool
execution*; a model that spends its turns on calls the harness filter blocks (inbox commands,
self-sends, repeats) never records a success, so the auto-complete never arms and the batch runs to
exhaustion.

This is a **prompt-shape defect, not a model-quality defect.** The same message is fine for a large
model because a large model infers the stopping point. Small models need the stopping point stated.

### Why it repeats rather than failing once

An exhausted batch produces no usable response, so the request is never answered and never
receipted — which is [MUX-111](./MUX-111-harness-reply-miscorrelation.md)'s territory. The delivery
backstop then re-drives the same startup message forever.

**The two defects multiply.** MUX-111 makes the retry infinite; MUX-110 makes each retry cost a full
turn budget. Either fix alone reduces the damage; both are needed to remove it. They are filed
separately because they are independent — the correlation bug would strand any un-receipted request,
and the prompt-shape bug would waste a budget even on a single delivery.

## Decisions (user, 2026-11-24)

Relayed by edit; this spec was parked on Phase 1's choice and is unparked by them.

| Question | Decision |
|----------|----------|
| Startup treatment for harness roles | **(A) a closed-form startup message** — one action, one report, done. "No startup message at all" is dropped |
| What ships first | **Criteria 3–4 now**: exhaustion is a terminal state for its batch, and it writes a lifecycle event. Neither needs a model |
| The message's wording | Validated against a real model **when Ollama is available**; the integration script skips that section without it and keeps a coverage floor on the rest |

So the order below is: terminal exhaustion and its event first (Phase 1), the closed-form message
second (Phase 2), the wording's live validation whenever a machine with Ollama runs the script.

### Restated 2026-11-28 (edit's call)

**Not a user decision.** Made by edit under the user's standing autonomy for this run, after review
removed the bounded-call unit test (it passed with the feature reverted) and no Ollama endpoint
answered on this machine. Flagged for the user to overrule.

| Item | As filed | As restated |
|------|----------|-------------|
| Criterion 7 | "A test pins that the harness startup path produces a bounded number of tool calls for a startup message" | What is proven: a test pins that local-model roles receive the closed-form startup. The bounded tool-call count is deferred to [MUX-249](../backlog/MUX-249-verify-closed-form-startup-bounded-against-live-local-model.md) |
| Phase 2's bounded-call step | "Unit test: the harness startup path yields a bounded tool-call count against a scripted model" | Deferred to MUX-249 — a stub cannot show it; a live model can |

Extended by edit at 01:23 the same day, also its call and flagged for the user: at Phase 4's
verification, any box still open that only the real-model section can prove is ticked "deferred to
MUX-249" with its check carried there; anything provable hermetically must be proven, not
deferred. That reaches criterion 2 (the `qwen3:4b` answer), which MUX-249's first criterion
already carries.

MUX-249 is a task, not a defect, with no GitHub issue. It carries the model-driven checks: the
closed-form text bounded on `qwen3:4b`, the open-ended text as control, and the two questions this
spec could not answer (why single-shot did not arm, why the 2026-08-26 failure repeated).

## Requirements

### Acceptance criteria

- [x] Harness roles receive a closed-form startup message with an explicit terminal condition — one action, one report, done
- [x] A harness agent answers its startup message within its turn budget on `qwen3:4b`, and the reply reaches the sender — checked where Ollama is available; the script reports the section skipped elsewhere — deferred to [MUX-249](../backlog/MUX-249-verify-closed-form-startup-bounded-against-live-local-model.md) on edit's call: no model run happened, the script reported its model section skipped. The tick means handed off, not checked
- [x] Tool-loop exhaustion is **terminal for that batch**, not silently retried forever — an exhausted batch is reported once and not re-driven indefinitely
- [x] The exhaustion path emits a lifecycle event so the condition is visible without reading a pane
- [x] Hook-provider agents (Claude Code) keep their current startup message unchanged — this is a harness-shaped fix, not a global prompt rewrite
- [x] **Negative control:** an open-ended message still exhausts if the model genuinely cannot finish — the fix must not be a blanket "always succeed after one turn" that masks real exhaustion
- [x] A test pins that local-model roles receive the closed-form startup message, with a Claude role as the control — restated 2026-11-28 on edit's call. The bounded tool-call count the criterion first asked for is deferred to [MUX-249](../backlog/MUX-249-verify-closed-form-startup-bounded-against-live-local-model.md)
- [x] `scripts/test-harness-startup.sh` passes — 16/0 on its hermetic sections; its model section skipped

### Technical approach

- **State the stopping point** (decided 2026-11-24). Replace the open-ended text for harness roles with a closed-form
  instruction naming one command and one deliverable — e.g. read the memory summary and report it in
  a sentence. The existing `InlineFallbackPrompt()` / role-specific prompt machinery in
  `bus/launch.go` is the natural seam.
- **Make exhaustion terminal.** An exhausted batch should be recorded as attempted and not left in a
  state the backstop re-drives. This overlaps MUX-111's receipt fix; land whichever comes first and
  re-check the other.
- **Do not raise `MaxTurns` as the fix.** A bigger budget for a task with no stopping condition buys
  a longer loop, not a completion.

### Key files

| File | Change |
|------|--------|
| `bus/launch_reason.go`, `bus/launch.go` | Startup message construction — closed-form variant for harness roles (`startupRestorePayload`) |
| `harness/loop.go` | Exhaustion handling: terminal outcome, lifecycle event (`:514`, `:557`) |
| `harness/prompt.go` | Role prompt/examples if the stopping condition is expressed there |
| `harness/loop_test.go` | Bounded-tool-call test plus the negative control |
| `scripts/test-harness-startup.sh` | New — integration test |
| `docs/agents.md` | Document the harness startup contract |

## Implementation

### Phase 1: Make exhaustion terminal and visible

Needs no model and no decision beyond the above — ships first.

- [x] Unit test pinning today's behaviour: an exhausted batch sends `(no response generated — tool loop exhausted)` and writes no lifecycle row — pinned as a mutant: the tree without the new log call fails the exhausted case
- [x] An exhausted batch is reported once and is not re-driven indefinitely — the request it could not answer ends in a state the delivery backstop does not chase — observed as exactly one reply under the request's id on a stub bus; the request reaching `responded` on a real bus is Phase 4's hermetic step and criterion 3
- [x] Emit a lifecycle event on exhaustion naming the role, the request id and the turn budget — the row also names the turns used
- [x] Negative control: a batch that answers within its budget writes no exhaustion event and is receipted as today — the unit test's bus is a stub, so the receipt is not observed here; Phase 4's negative control carries it
- [x] Re-check the interaction with [MUX-111](./MUX-111-harness-reply-miscorrelation.md) once either lands, and record it here — recorded in [Findings for Phase 1](#findings-for-phase-1)

### Phase 2: The closed-form startup message

- [x] Harness roles get a closed-form startup message naming one command and one deliverable; hook-provider startup messages are untouched — as built, every `LocalProvider` role (the harness and `muxcode agent`)
- [x] Unit test: a harness role's launch seeds the closed-form text, a Claude role's the unchanged `startupRestorePayload`
- [x] Negative control: a genuinely open-ended message still exhausts, so the guard cannot mask real failure — Phase 1's exhausted case stands as the control; Phase 2 added wording and no guard in the harness
- [x] Record why `isSingleShotRole()` did not arm in the 2026-08-26 runs if a model run shows it (no successful tool execution, or another cause); without Ollama, say it was not observed — not observed: no Ollama endpoint on this machine, the cause stays unrecorded

### Phase 3: Docs

- [x] `docs/agents.md`: the harness startup contract and the exhaustion event — written by plan 2026-11-28: [Startup contract](../../agents.md#startup-contract) and [Tool-loop exhaustion](../../agents.md#tool-loop-exhaustion), under "Local LLM harness". The contract says the wording is unproven against a model and points to MUX-249

### Phase 4: Integration test

- [x] Create `scripts/test-harness-startup.sh` — scratch bus; hermetic section driven by a stub model through the harness's `*http.Client` seam, never a bound socket
- [x] Hermetic: the exhaustion path fires its lifecycle event once for a deliberately open-ended message, and the same message is not reprocessed unboundedly — section B; "not reprocessed" is observed over 10 s, see the limits in [Findings for Phase 4](#findings-for-phase-4)
- [x] Hermetic: a harness role's startup request carries the closed-form text — section A, read from a real `muxcode agent launch`
- [x] **Negative control:** a batch answered within budget writes no exhaustion event — section C
- [x] Model-driven section: on `qwen3:4b` a harness role answers its startup message within its turn budget and the reply reaches the sender — the wording's validation; skipped with a reason when Ollama or the model is unavailable — section D is written and **has never run**: it skipped, no endpoint at `localhost:11434`. The validation itself is [MUX-249](../backlog/MUX-249-verify-closed-form-startup-bounded-against-live-local-model.md)'s
- [x] **Coverage floor** on the hermetic section, so a run that skipped the model cannot report green on nothing — floor 16, counted before section D
- [x] Run the script and record the counts here, naming whether the model section ran — 16 passed, 0 failed, floor 16; **the model section did not run** (no Ollama). See [Findings for Phase 4](#findings-for-phase-4)

### Deferred

Moved here from Phase 2 on 2026-11-28 so it would not hold the phase loop, then handed off on
edit's call the same day. The tick means handed off, not done.

- [x] Unit test: the harness startup path yields a bounded tool-call count against a scripted model — deferred to [MUX-249](../backlog/MUX-249-verify-closed-form-startup-bounded-against-live-local-model.md). The test written for it passed with the feature reverted and was removed on review; a stub cannot show it — see [The bounded-call test was removed](#the-bounded-call-test-was-removed)

## Findings for Phase 1

Recorded 2026-11-28 from the worker's report. Verified the same day: `./test.sh` run
`20261128-010115-52678` (01:01, after the last code edit at 00:59) passed both modules with 0
failures — harness 178 PASS, bus 3364 PASS — and review returned 0 must-fix, 0 should-fix, 0 nits.
Criteria 3 and 4 stay open: the unit test's bus is a stub, so the request reaching `responded` and
the row landing through the real `muxcode lifecycle log` are Phase 4's to observe.

### What changed

| File | Change |
|------|--------|
| `harness/bus.go` | `BusClient.LogLifecycle(level, event, detail)` — writes through `muxcode lifecycle log <session> <level> harness <event> --detail …` |
| `harness/loop.go` | The exhaustion text is the constant `toolLoopExhausted`, text unchanged. A batch that reaches the send with no response calls `logExhaustion`, which writes one `harness-tool-loop-exhausted` row (warn, source `harness`): `<role>: request <ids> answered with the exhaustion text after <used> of its <budget> turns` (`no request` for a request-free batch; `<used>` is clamped to the budget). The exhaustion text is still sent to each request as its reply |
| `harness/loop_test.go` | `TestProcessBatch_ExhaustionIsTerminalAndLogged` (three cases), stub `toolLoopModel`; `recordingBus` now returns calls by verb |

### Evidence

| Step | Evidence |
|------|----------|
| 1 — today's behaviour | A mutant removing the `logExhaustion` call fails the exhausted case at `loop_test.go:155` ("lifecycle calls = [], want one … row") while the control passes. The exhaustion reply was already sent before this phase |
| 2 — terminal | Exhausted case (a model that calls a distinct `echo` every turn, `MaxTurns` 3): exactly one reply, to edit under the request's id, carrying the exhaustion text; the model is called at most `MaxTurns + 1` times (the last is the summary call) |
| 3 — event | The row names role, request id, turns used and turn budget — asserted on the recorded `lifecycle` call: the looping model writes "after 3 of its 3 turns", a model answering empty text "after 2 of its 3 turns" (its turn 1 is the forced tool-use retry) |
| 4 — negative control | Same test, model answering with text: no lifecycle call, reply is the model's text |

Pin `/tmp/mux110-p1-pin.sh`, run agent task `1795845594-spawn-a5769f4f-a8153edb`, 01:00:
7 passed, 0 failed — `go vet` clean, harness package 178 PASS, all three cases ran,
`TestProcessBatch_ReplyCorrelatesToTheRequest` still green, the no-row mutant caught. The run
before it (`1795845574`) was 6 passed, 1 failed: the pin's mutant pattern still matched the old
call, so the mutant did not apply; the tree itself passed. The first pin (`1795845435`, 00:57,
6/0) predates the wording change below.

### The MUX-111 re-check

MUX-111 has landed (PR #277). Two things follow:

- The consume already writes an ack receipt (MUX-111 Phase 1), so "never answered and never
  receipted … the delivery backstop then re-drives the same startup message forever" did not hold
  as written even before MUX-111.
- Since MUX-111 the exhaustion reply correlates to the request and marks it responded.

So no re-drive mechanism remains for an exhausted startup request, and **the 2026-08-26
five-minute repeat is not explained by today's code**. Candidates, neither observed: the bus of
that date, before receipts on consume; or a launch seeding a fresh startup request on each
relaunch. "Why it repeats" in the Context, "The two defects multiply", and the technical
approach's "not left in a state the backstop re-drives" are left as filed and should be read as
historical.

### Limits

| Limit | Detail |
|-------|--------|
| "Terminal" is observed as one reply under the request's id | The test's bus is a stub: the request reaching `responded` and nothing re-driving it are not observed. Phase 4's hermetic step ("not reprocessed unboundedly") carries that |
| The row follows the exhaustion text, not the turn count | It is written when the batch reaches the send with an empty response. A batch that spends every turn and then answers the tool-less summary call writes no row; that is an answered batch |
| The row fires on any empty response, not only a spent budget | A model returning empty text with no tool call after the first turn, or a single-shot break whose summary call fails, also reaches that branch — each such batch sends the exhaustion text. The row first read "after its `<n>`-turn budget", which overstated those cases; it now names the turns used ("after 2 of its 3 turns"), with the empty-text case tested. The event name still says `exhausted` for a batch that stopped early |

### Review fix after Phase 3: exhaustion is never a success for the breaker

Review's should-fix on `docs/agents.md` ("the batch counts as failed … as before") found the line
untrue: a model answering empty text with no tool call set `batchSuccess = true` before the batch
fell back to the exhaustion text, so that case counted as a success and reset the circuit breaker.
Plan wrote the line from the turn-budget case without checking the empty-answer one.

Fixed in code rather than in the doc: `processBatch` now sets `batchSuccess = false` wherever it
falls back to the exhaustion text. `TestProcessBatch_ExhaustionIsTerminalAndLogged` asserts
`processBatch`'s return — false for the looping and empty-answer cases, true for the answered
control. Pin `/tmp/mux110-fix-pin.sh`, run agent task `1795846570-spawn-a5769f4f-9cbde958`, 01:16:
3 passed, 0 failed, harness 178 PASS; a mutant without the reset fails the empty-answer case at
`loop_test.go:138` ("success = true, want false") while the control passes. The doc line now names
the change instead of "as before".

This is a behaviour change beyond the spec's criteria: an empty-answer batch now counts toward the
breaker's three-failure cooldown.

Verified 2026-11-28: `./test.sh` run `20261128-011733-6177` (01:17, after the fix at 01:16) passed
both modules with 0 failures — harness 178 PASS, bus 3365 PASS — and review returned 0 must-fix,
0 should-fix, 0 nits.

## Findings for Phase 2

Recorded 2026-11-28 from the worker's report. Verified the same day: `./test.sh` run
`20261128-011130-48093` (01:11, after the last code edit at 01:09) passed both modules with 0
failures — harness 178 PASS, bus 3365 PASS, the bus package's result served from the test cache,
its files unchanged since 01:04 — and review's second pass returned 0 must-fix, 0 should-fix, 0
nits. Criteria 1, 5 and the restated 7 are ticked on the launch routing test.

### What changed

| File | Change |
|------|--------|
| `bus/launch_reason.go` | New constant `localStartupRestorePayload`: "Session started — run `muxcode memory context` once, then reply with one sentence summarizing what it shows. That reply finishes this task: run nothing else." |
| `bus/launch.go` | `PreLaunchSetup` seeds that text when the role resolves to `LocalProvider` — the LLM harness or `muxcode agent`. Every other provider keeps `startupRestorePayload`; the auto agent's user-reason Jira task still replaces either. Both texts open with "Session started —", the words the agent definitions key on |
| `bus/launch_test.go` | `TestPreLaunchSetup_LocalRoleGetsClosedFormStartup` — `local` and `claude` cases |
| `harness/loop_test.go` | `TestProcessBatch_ClosedFormStartupIsBounded` — added, then **removed on review** (below) |

### Evidence

| Step | Evidence |
|------|----------|
| 2 — seeded text | Build launched as `local` holds one `request:startup` with the closed-form text; as `claude`, the unchanged text. A mutant without the local branch fails the local case and passes the claude one (`/tmp/mux110-p2-mut.log`) |
| 3 — bounded | **None — step open.** The test (a scripted model making one tool call then answering) was removed: review's must-fix (01:09) found it passed with the startup feature reverted |
| 4 — negative control | No new test: Phase 1's exhausted case, a model that never stops, still exhausts and logs |
| 5 — single-shot | Not observed. No Ollama endpoint here; why `isSingleShotRole()` did not arm on 2026-08-26 stays unrecorded |

Pin `/tmp/mux110-p2-pin.sh`, run agent task `1795846181-spawn-a5769f4f-71831364`, 01:10:
7 passed, 0 failed — `go vet` clean on both modules, the bus `PreLaunchSetup` tests 7 PASS, harness
package 178 PASS, the no-local-branch mutant caught. The earlier pin (`1795845871`, 01:04, 8/0,
harness 179) included the removed test.

### The bounded-call test was removed

Review's must-fix: `TestProcessBatch_ClosedFormStartupIsBounded` passed with the startup feature
reverted. Its stub model ignored the payload and always emitted one `echo` then text, the payload
was a copy typed into the test, and its tool count counted what the stub emitted, not what ran. A
stub that stops proves nothing about whether a message makes a model stop. The harness module
cannot import the bus module to take the launch-produced text, so the test was removed rather than
strengthened.

Step 3 was ticked on the first pin with this weakness noted as a limit; it is unticked. It and
criterion 7 ("a test pins … a bounded number of tool calls for a startup message") now rest on
Phase 4's model-driven section.

**That section needs Ollama, and none answers on this machine** (`localhost:11434` refused,
2026-11-28). Edit resolved it the same day: criterion 7 restated to what is proven and the step
deferred to MUX-249 — see [Restated 2026-11-28](#restated-2026-11-28-edits-call).

### Limits

| Limit | Detail |
|-------|--------|
| The wording is untested | Whether a small model stops on the closed-form text is Phase 4's model-driven step, which needs Ollama |
| The command the text names is allowed | Read, not run: every role's profile includes `Bash(muxcode *)` through the shared `bus` group, and the harness filter blocks only `muxcode inbox` and self-sends — so `muxcode memory context` is not one of the blocked calls the root cause describes |
| Scope is `LocalProvider`, wider than "harness roles" | `muxcode agent` roles get the closed form too. Codex and OpenCode keep the open-ended text |

## Findings for Phase 4

Recorded 2026-11-28 from the worker's report, the script read and its log checked. Verified the
same day: the 16/0 run (01:26) postdates the script's last edit (01:25); `./test.sh` run
`20261128-012724-92356` (01:27) passed both modules with 0 failures — harness 178 PASS, bus 3365
PASS; review returned 0 must-fix, 0 should-fix, 0 nits on the fixed script. Criterion 8 and the
final-run step are ticked on that; criterion 2 is ticked as deferred to MUX-249 on edit's call.

### The script

`scripts/test-harness-startup.sh`: muxcode built from the tree, scratch HOME, state, install dir
and lifecycle log, a private tmux server and a scratch daemon. The startup text comes from the
real launch road — `muxcode agent launch build` with a stub `muxcode-llm-harness` or stub `claude`
first on PATH. The harness runs from a scratch copy of its module carrying a `TestDriveStartup`
that calls the real `ConsumeInbox` and `processBatch`, its model a stub through the `*http.Client`
seam (`toolLoopModel`, `textModel` — no socket).

### Run

Run agent task `1795847138-spawn-a5769f4f-7f04f119`, 01:26, log `/tmp/test-harness-startup.log`:
**16 passed, 0 failed, 16 hermetic, floor 16, 1 skipped.**

| Section | Checks | What it shows |
|---------|--------|---------------|
| Setup | 2 | Tree-built muxcode, stubs and driver copy; scratch daemon running |
| A | 2 | A local build's launch seeds the closed-form startup; a Claude build's the unchanged text |
| B | 8 | The open-ended startup under a model that never stops: one `harness-tool-loop-exhausted` row naming build, the startup's id and the turns used; answered with the exhaustion text under its id; `responded`; the batch counted failed; 10 s later build's inbox is empty and there is still one row |
| C | 3 | Negative control: a fresh closed-form startup answered within budget — judged by `answered_ok`: the model's text as the reply, `responded`, batch a success, no new row |
| C2 | 1 | Control on the predicate: a stub model answering HTTP 500 leaves "Error calling Ollama: all retries exhausted", which `answered_ok` rejects |
| D | 0 — **skipped** | Model-driven. No Ollama endpoint at `http://localhost:11434`. **The model section did not run** |

Earlier runs: `1795846894` at 01:21 passed 17 of 17 (floor 17) before the review fixes below; the
run before that passed 17 and failed only on a floor miscounted as 18. The count fell from 17 to
16 because C's three result checks became one `answered_ok` check and C2 added one.

### Review should-fixes on the script

| Fix | Why |
|-----|-----|
| The harness driver runs as build (`AGENT_ROLE`/`BUS_ROLE=build`) with the tree's muxcode first on PATH | A model's `muxcode memory` call would otherwise have run the installed binary, not the tree under test |
| Sections C and D share one success predicate, `answered_ok` — driver passed, batch success, request `responded`, reply not an error, exhaustion or all-blocked text, exhaustion rows unchanged — with C2 as its control | Section D judged success more loosely than C, so it could have passed on an unreachable model that left an error reply |

This closes what Phases 1 and 2 carried forward: the request reaching `responded` and the row
landing through the real `muxcode lifecycle log` (criteria 3 and 4), and the launch-seeded text
(criterion 1).

### Limits

| Limit | Detail |
|-------|--------|
| The model section has never run | Section D's code path is unexecuted, so a fault in it would not have shown. The wording, criterion 2's `qwen3:4b` answer and the deferred bounded-call check are unverified; MUX-249 carries them |
| "Not re-driven" is a 10 s observation | The 2026-08-26 repeat was about every five minutes. What makes the batch terminal is its state — consumed, answered under its id, `responded` — and the 10 s wait with a 1 s daemon poll only corroborates it. No watchdog with a longer threshold was waited out |
| Exhaustion in B is the stub's doing | The stub model never stops whatever it is sent, so B shows the exhaustion path on the open-ended startup, not that the open-ended text causes exhaustion |
| No mutant in the script | The row and the terminal reply are pinned by mutants at unit level (Phase 1, the review fix); the script has its negative control C and no reverted-tree control |

## Status

**Complete** — closed out 2026-11-28 by `55-defect-backlog-loop` run 1795811929 at 26/26.

### What shipped

- **Exhaustion is terminal and visible.** A harness batch that reaches its send with no response
  answers each request with the exhaustion text under that request's own id, writes one
  `harness-tool-loop-exhausted` lifecycle row naming the role, the request ids and the turns used
  of the budget, and counts as a failed batch for the circuit breaker.
- **A closed-form startup for local-model roles.** `PreLaunchSetup` seeds a `LocalProvider` role
  (the harness or `muxcode agent`) one command, one deliverable and a stated end; every other
  provider keeps the open-ended text.
- **`scripts/test-harness-startup.sh`**, 16 passed, 0 failed, floor 16, on its hermetic sections;
  `docs/agents.md` documents the startup contract and the exhaustion event.

### Left as recorded

| Item | Detail |
|------|--------|
| No model has run the closed-form text | The defect's actual question — does a small model stop on this wording — is unanswered. The script's model section has never executed (no Ollama on this machine). Carried by [MUX-249](../backlog/MUX-249-verify-closed-form-startup-bounded-against-live-local-model.md) |
| Two ticks are hand-offs, not proofs | Criterion 2 and Phase 2's bounded-call step are ticked "deferred to MUX-249" |
| Edit's calls, not user decisions | Criterion 7 restated to what is proven; the model-only checks deferred. See [Restated 2026-11-28](#restated-2026-11-28-edits-call). Flagged for the user to overrule |
| The 2026-08-26 repeat is unexplained | No re-drive mechanism exists in today's code. "Why it repeats", "The two defects multiply" and the technical approach's re-drive wording are left as filed — see [the MUX-111 re-check](#the-mux-111-re-check) |
| Why single-shot did not arm | Not observed |
| A behaviour change beyond the criteria | A batch whose model answers empty text now counts toward the breaker's cooldown; before, it reset the breaker |
| The row fires on any empty response | Its event name says `exhausted` for a batch that stopped early; the detail names the turns used |
| "Not re-driven" was watched for 10 s | The original repeat was about every five minutes |
| Scope is wider than "harness roles" | `muxcode agent` roles get the closed form too |

### How it got here

Unparked 2026-11-24 on the user's decisions; activated 2026-11-28. Phase 1 added the row and found
the repeat premise historical. Phase 2 added the closed-form startup; review removed its
bounded-call unit test, which passed with the feature reverted. Phase 3's docs line on the breaker
was wrong for the empty-answer case and was made true in code. Phase 4's script took two review
fixes — the driver's role and PATH, and one shared success predicate with a control.
