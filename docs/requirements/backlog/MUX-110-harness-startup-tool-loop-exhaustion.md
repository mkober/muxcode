# Harness Startup Message Exhausts the Tool Loop

**Tracking:** [mkober/muxcode#44](https://github.com/mkober/muxcode/issues/44)

The startup message every agent receives is open-ended prose. A Claude-class model treats it as a
cue to read memory and stop; a small local model treats it as a task with no terminal condition and
burns every available turn on it, emitting `(no response generated — tool loop exhausted)` and
never answering. Found during the [MUX-109](../completed/MUX-109-prompt-mode-graph-control-pane.md) Phase 2
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
receipted — which is [MUX-111](../completed/MUX-111-harness-reply-miscorrelation.md)'s territory. The delivery
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

## Requirements

### Acceptance criteria

- [ ] Harness roles receive a closed-form startup message with an explicit terminal condition — one action, one report, done
- [ ] A harness agent answers its startup message within its turn budget on `qwen3:4b`, and the reply reaches the sender — checked where Ollama is available; the script reports the section skipped elsewhere
- [ ] Tool-loop exhaustion is **terminal for that batch**, not silently retried forever — an exhausted batch is reported once and not re-driven indefinitely
- [ ] The exhaustion path emits a lifecycle event so the condition is visible without reading a pane
- [ ] Hook-provider agents (Claude Code) keep their current startup message unchanged — this is a harness-shaped fix, not a global prompt rewrite
- [ ] **Negative control:** an open-ended message still exhausts if the model genuinely cannot finish — the fix must not be a blanket "always succeed after one turn" that masks real exhaustion
- [ ] A test pins that the harness startup path produces a bounded number of tool calls for a startup message
- [ ] `scripts/test-harness-startup.sh` passes

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

- [ ] Unit test pinning today's behaviour: an exhausted batch sends `(no response generated — tool loop exhausted)` and writes no lifecycle row
- [ ] An exhausted batch is reported once and is not re-driven indefinitely — the request it could not answer ends in a state the delivery backstop does not chase
- [ ] Emit a lifecycle event on exhaustion naming the role, the request id and the turn budget
- [ ] Negative control: a batch that answers within its budget writes no exhaustion event and is receipted as today
- [ ] Re-check the interaction with [MUX-111](../completed/MUX-111-harness-reply-miscorrelation.md) once either lands, and record it here

### Phase 2: The closed-form startup message

- [ ] Harness roles get a closed-form startup message naming one command and one deliverable; hook-provider startup messages are untouched
- [ ] Unit test: a harness role's launch seeds the closed-form text, a Claude role's the unchanged `startupRestorePayload`
- [ ] Unit test: the harness startup path yields a bounded tool-call count against a scripted model
- [ ] Negative control: a genuinely open-ended message still exhausts, so the guard cannot mask real failure
- [ ] Record why `isSingleShotRole()` did not arm in the 2026-08-26 runs if a model run shows it (no successful tool execution, or another cause); without Ollama, say it was not observed

### Phase 3: Docs

- [ ] `docs/agents.md`: the harness startup contract and the exhaustion event

### Phase 4: Integration test

- [ ] Create `scripts/test-harness-startup.sh` — scratch bus; hermetic section driven by a stub model through the harness's `*http.Client` seam, never a bound socket
- [ ] Hermetic: the exhaustion path fires its lifecycle event once for a deliberately open-ended message, and the same message is not reprocessed unboundedly
- [ ] Hermetic: a harness role's startup request carries the closed-form text
- [ ] **Negative control:** a batch answered within budget writes no exhaustion event
- [ ] Model-driven section: on `qwen3:4b` a harness role answers its startup message within its turn budget and the reply reaches the sender — the wording's validation; skipped with a reason when Ollama or the model is unavailable
- [ ] **Coverage floor** on the hermetic section, so a run that skipped the model cannot report green on nothing
- [ ] Run the script and record the counts here, naming whether the model section ran

## Status

**Backlog** — unparked 2026-11-24 on the user's decisions above. Not started.
