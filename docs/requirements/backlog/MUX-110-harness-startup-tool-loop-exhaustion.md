# Harness Startup Message Exhausts the Tool Loop

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
| The message | `bus/launch.go:747` | `"Session started — review last saved context from memory to restore session state."` |
| The turn budget | `harness/config.go:29` | `MaxTurns: 10` per batch |
| The failure text | `harness/loop.go:481` | `finalResponse = "(no response generated — tool loop exhausted)"` |
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

## Requirements

### Acceptance criteria

- [ ] Harness roles receive a startup message with an explicit terminal condition — one action, one report, done
- [ ] A harness agent answers its startup message within its turn budget on `qwen3:4b`, and the reply reaches the sender
- [ ] Tool-loop exhaustion is **terminal for that batch**, not silently retried forever — an exhausted batch is reported once and not re-driven indefinitely
- [ ] The exhaustion path emits a lifecycle event so the condition is visible without reading a pane
- [ ] Hook-provider agents (Claude Code) keep their current startup message unchanged — this is a harness-shaped fix, not a global prompt rewrite
- [ ] **Negative control:** an open-ended message still exhausts if the model genuinely cannot finish — the fix must not be a blanket "always succeed after one turn" that masks real exhaustion
- [ ] A test pins that the harness startup path produces a bounded number of tool calls for a startup message
- [ ] `scripts/test-harness-startup.sh` passes

### Technical approach

- **State the stopping point.** Replace the open-ended text for harness roles with a closed-form
  instruction naming one command and one deliverable — e.g. read the memory summary and report it in
  a sentence. The existing `InlineFallbackPrompt()` / role-specific prompt machinery in
  `bus/launch.go` is the natural seam.
- **Consider skipping it entirely.** Harness panes already poll their inbox directly rather than
  being woken by send-keys. If the startup message exists only to make an agent check its inbox, a
  harness agent may not need one at all. Confirm before assuming — the message may also be the only
  thing restoring session context for these roles.
- **Make exhaustion terminal.** An exhausted batch should be recorded as attempted and not left in a
  state the backstop re-drives. This overlaps MUX-111's receipt fix; land whichever comes first and
  re-check the other.
- **Do not raise `MaxTurns` as the fix.** A bigger budget for a task with no stopping condition buys
  a longer loop, not a completion.

### Key files

| File | Change |
|------|--------|
| `bus/launch.go` | Startup message construction — closed-form variant for harness roles (`:747`) |
| `harness/loop.go` | Exhaustion handling: terminal outcome, lifecycle event (`:481`) |
| `harness/prompt.go` | Role prompt/examples if the stopping condition is expressed there |
| `harness/loop_test.go` | Bounded-tool-call test plus the negative control |
| `scripts/test-harness-startup.sh` | New — integration test |
| `docs/agents.md` | Document the harness startup contract |

## Implementation

### Phase 1: Reproduce and bound

- [ ] Reproduce the exhaustion with a harness role on `qwen3:4b` and capture the turn trace
- [ ] Confirm whether `isSingleShotRole()` fails to arm because no tool execution succeeds, or for another reason — record the actual cause rather than the assumed one
- [ ] Decide between closed-form startup message and no startup message for harness roles, with the reason recorded

### Phase 2: Fix the message shape

- [ ] Implement the chosen startup treatment for harness roles only
- [ ] Leave hook-provider startup messages untouched
- [ ] Unit test: the harness startup path yields a bounded tool-call count
- [ ] Negative control: a genuinely open-ended message still exhausts, so the guard cannot mask real failure

### Phase 3: Make exhaustion terminal and visible

- [ ] An exhausted batch is reported once and not re-driven indefinitely
- [ ] Emit a lifecycle event on exhaustion
- [ ] Re-check the interaction with [MUX-111](./MUX-111-harness-reply-miscorrelation.md) once either lands

### Phase 4: Integration test

- [ ] Create `scripts/test-harness-startup.sh`
- [ ] A harness role receiving a startup message answers within its turn budget, and the reply reaches the sender
- [ ] The exhaustion path fires its lifecycle event when a deliberately open-ended message is used
- [ ] No infinite re-drive: the same startup message is not reprocessed unboundedly
- [ ] Skip-with-reason when Ollama or the model is unavailable, with a **coverage floor** so a skipped run cannot report green
- [ ] Run the script and confirm all checks pass

## Status

Draft
