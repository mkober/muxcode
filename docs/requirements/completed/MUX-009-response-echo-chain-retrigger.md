# Response Echo Chain Retrigger

**Tracking:** [mkober/muxcode#9](https://github.com/mkober/muxcode/issues/9)

On non-hook providers (OpenCode, Codex), `SendWakeUp` injects `type: response` payloads into the agent's TUI composer as if they were prompts — so an agent receiving its own delegation's *answer* treats it as an instruction and re-fires the chain. Observed live: `build` re-sent `request:test` five times in ~3.5 minutes with nobody asking, because every `test` response injected back into `build` read as "run tests".

## Context

### Observed failure (session `muxcode`, 2026-08-17 10:59–11:03; verified from bus state, not agent replies)

- `build` re-sent `request:test` ("Build succeeded — run tests and report results") 5× in ~3.5 min
- Daemon caught it: `[11:02:09] loop-detected build <-> test action:test repeated 4x in 3m13s`
- 26 messages reached the edit inbox in 3m30s; `test`'s context climbed to 84.9K tokens with real spend accruing per repeat
- At drain time, `build`'s inbox held 5800 bytes of almost entirely `test [response:response]` payloads
- Consuming the echoes stopped the loop immediately (5800 → 0 bytes; fleet-wide rate 0/60s thereafter)
- Downstream amplification: the same storm's auto-CC traffic into a non-consuming edit inbox drove the sustained `verify-spec` echo burst documented in [`verify-spec-stale-review-refire`](../completed/MUX-007-verify-spec-stale-review-refire.md) (impact item 5) — one echo bug feeding another

### Mechanism

```
build --request:test--> test
test  --response------> build's inbox        ("All 4 packages passed …")
                          |
                          v
        non-hook SendWakeUp INJECTS that response payload as a TUI prompt
                          |
                          v
                 build reacts to it and re-fires request:test
```

`bus/provider_opencode.go` / `bus/provider_codex.go` already treat this as a known hazard — response-only wake-ups skip appending the reply instruction (*"Response-only wake-ups skip this to avoid infinite echo loops"*) — but they still inject the response **payload**. Suppressing the instruction without suppressing the prompt removes only half the trigger: the agent is still handed text and still acts on it.

Non-hook-specific by construction: a Claude agent receives a fixed wake-up string and reads its inbox itself, so a response stays data. For OpenCode/Codex the wake-up *is* the delivery, so a response becomes an instruction.

### Secondary driver: answered requests stay actionable

The stronger driver was a request `build` had already fulfilled: a `make build` request (10:58:56) completed at 10:59:04 and was answered twice, but the request message itself was never consumed. `HasActionableMessages` counts request-type messages, so the daemon kept waking `build` for work it had already finished — and the chain re-fired on each wake. `MarkResponded` already drains the **responder's** inbox; the **requester** side has no equivalent.

## Requirements

### Acceptance criteria

- [x] Non-hook `SendWakeUp` does not inject `type: response` payloads as prompts; responses are summarized or dropped, never handed over as instructions — script sections A and C on OpenCode-shaped panes; scrape-road Codex by unit test
- [x] A response-only wake-up with no request in the batch performs no injection at all — except
  for an orchestrator (edit or auto, by base role) on OpenCode or scrape-road Codex, which gets the
  count notice typed alone: how many responses wait, never their payload, the responses kept for
  `muxcode inbox` (amended 2026-11-14, see Phase 1's findings) — script section C; the orchestrator
  notice by unit test
- [x] A request that has been responded to is no longer counted as actionable for wake-up purposes (requester-side equivalent of `MarkResponded`'s inbox drain) — unit test
- [x] Regression test: queue a response-only inbox for a non-hook role, assert no pane injection occurs — `TestSendWakeUp_ResponsesNeverTypedAsPrompts`
- [x] Regression test: an answered-but-unconsumed request does not keep an agent actionable — `TestHasActionableMessages_AnsweredRequestDoesNotWake`

### Key files

| File | Change |
|------|--------|
| `bus/provider_opencode.go` | `SendWakeUp()`: exclude response payloads from injection, no-op on response-only batches |
| `bus/provider_codex.go` | Same policy as OpenCode |
| `bus/inbox.go` | `HasActionableMessages()`: exclude requests with a recorded response (delivery-store lookup) |
| `bus/delivery.go` | Expose responded-status lookup for the actionable check |

### Design consideration

Fourth instance in one day of the same root pattern — the non-hook wake-up path injecting content the hook path already learned not to: (1) unbounded wake-up argv (fixed — `BoundWakeUpBatch`), (2) unbounded `compact-recommended` accumulation (fixed — `HasPendingAction`), (3) unverified auto-restart (filed — [`unverified-daemon-auto-restart`](../backlog/MUX-008-unverified-daemon-auto-restart.md)), (4) this. Consider a single explicit policy for "what may become a prompt" in the non-hook wake-up path rather than continuing to patch cases individually.

## Implementation

### Phase 1: Injection policy

- [x] Filter `type: response` payloads out of non-hook `SendWakeUp` injection; summarize or drop, never inject as prompt
- [x] Response-only batch → no injection at all
- [x] Unit tests: response-only batch injects nothing; mixed batch injects only requests

### Findings and changes for Phase 1

Recorded 2026-11-14 from the run worker's request (run `1794626267`, node `implement`); plan
confirmed the functions and the test file exist. Uncommitted. The run's build, `./test.sh` (exit 0,
both modules) and review of the revised policy (no must-fix or should-fix) passed, and the phase
was ticked. One nit is outstanding: the header comment of `bus/wake_policy_test.go` still describes
the retired orchestrator exception; its assertions are right.

- **Change** — `bus/provider.go`, `splitWakeBatch` and `dropWakeResponses`: on the typed-injection
  roads (OpenCode and scrape-road Codex) every `type: response` message is taken out of the batch
  before it is typed. The dropped responses are consumed with no receipt and one
  `wake-response-dropped` lifecycle row is written; a response-only batch types nothing.
- **Revised after the run's first review — the policy is uniform.** The first version typed
  responses in for the orchestrators (edit and auto, by base role), which narrowed the first
  acceptance criterion. That exception is gone: no role has a response typed in, and a
  response-only batch injects nothing for every role. What differs by role is only what happens to
  the response afterwards:

  | Role | A response in the wake batch |
  |------|------------------------------|
  | Any role but an orchestrator | Dropped: consumed with no receipt, one `wake-response-dropped` row. Such an agent never sees the answer to a request it sent itself, by any road |
  | edit, auto (by base role, so their spawn workers too) | Stays queued in the inbox as data, to be read with `muxcode inbox`. A prompt that does inject — because the batch also holds a request — carries one notice line giving the count of held responses, never their text |

- **Amended after the phase was ticked — a response-only batch for an orchestrator types the
  notice alone.** Found by edit after the review, fixed by the worker in `bus/provider_opencode.go`
  and `bus/provider_codex.go` (`settleWakeResponses`: "with no request in the batch the notice is
  the whole prompt"). Under "inject nothing for every role", an edit or auto agent on these roads
  would hold delegated results in its inbox and never be woken to read them — on a provider with no
  inbox listener the typed wake-up is the only delivery. So acceptance criterion 2 was amended, on
  edit's request, to name the exception: the count notice, no payload. The criterion's original
  wording was "performs no injection at all", with no exception. This is edit's change to an
  acceptance criterion, not a recorded user decision. The row above ("response-only batch injects
  nothing for every role") is superseded for orchestrators. The fix was then covered by the run's
  Phase 2 pass: `./test.sh` exit 0 and a review with no findings, which checked the count, the
  absence of result text and the retained inbox data; the stale test header was corrected too.
- `splitWakeBatch` now runs on the whole inbox before `BoundWakeUpBatch`, so held responses cannot
  fill the bounded batch and starve the requests behind them.
- **Chosen between "summarize or drop"**: drop for non-orchestrators, hold for orchestrators.
- **Tests** — `bus/wake_policy_test.go`, `TestSendWakeUp_ResponsesNeverTypedAsPrompts`: a
  response-only batch, a mixed batch, and the orchestrator case (held, counted, not typed).

### Phase 2: Answered requests stop being actionable

- [x] `HasActionableMessages` excludes requests whose delivery status is `responded`
- [x] Unit test: answered-but-unconsumed request does not wake the agent

### Findings and changes for Phase 2

Recorded 2026-11-14 from the run worker's request (run `1794626267`, node `implement`); plan
confirmed the function, its call site and the test exist. Uncommitted. The run's build,
`./test.sh` (exit 0, both modules) and review (no findings) passed, and the phase was ticked.

- `IsResponded(session, msgID)` (`bus/delivery.go`) reports whether a message's delivery status is
  `responded`.
- `HasActionableMessages` (`bus/inbox.go`) skips a request for which `IsResponded` holds, alongside
  its existing exclusions (not a request, a looping self-send, addressed to another window).
  `MarkResponded` already drains an answered row from the responder's inbox; this covers the row
  that is still there — an answer sent without the drain reaching it.
- Test: `TestHasActionableMessages_AnsweredRequestDoesNotWake` in `bus/wake_policy_test.go`, with
  unanswered-request controls.

Limit: a request answered without `--reply-to` is never marked responded, so it stays actionable as
before.

### Phase 3: Integration test

- [x] Create `scripts/test-response-echo.sh` (requires a running muxcode session with a non-hook agent) — built hermetic instead, with stand-in OpenCode panes; see the results below
- [x] Test: send a request to a non-hook role, let it respond, assert the requester's chain does not re-fire on the response wake-up
- [x] Test: queue only responses for a non-hook role, assert no pane injection occurs
- [x] Run the script and verify all checks pass — 17 of 17, floor 17

### Results for Phase 3

Recorded 2026-11-14 from the run worker's request (run `1794626267`); plan confirmed the result
line in the run agent's history and the floor in the script. Uncommitted. The run's build,
`./test.sh` (exit 0) and review (no findings) passed, and the phase and the remaining acceptance
criteria were ticked. The review noted that the fake build agent reacts to any typed line, so the
absence of a second request rests on the production filter, not on the fake.

New script: `scripts/test-response-echo.sh`. **A deviation from the first step, which says it
requires a running muxcode session with a non-hook agent**: the script is hermetic instead — a
private tmux server, no daemon, and fake OpenCode agents — so it can run unattended and in
`test-all.sh`. It exercises the real injection code against stand-in panes, not a real OpenCode or
Codex process.

| Section | What it shows |
|---------|---------------|
| A | One `request:test` sent to a non-hook role and answered: the answer is not typed into the requester's pane, it is dropped with a `wake-response-dropped` row, and none is left queued — the requester's chain does not re-fire |
| B | Negative control on a pre-fix build (the response filter patched out of a scratch copy): the same answer re-fires `request:test` |
| C | Responses alone, force-delivered: nothing is typed, the inbox is drained, no receipt is written, one dropped row |
| D | Negative control: the pre-fix build types them |

Through the run agent (task `1794634924-spawn-0616aad2-498290ba`): **17 passed, 0 failed, floor
17**, exit 0. `CLAUDE.md` was updated by edit.

Not covered by the script: scrape-road Codex (the fake agents are OpenCode; Codex shares the
split and settle helpers and is covered by the unit tests), the orchestrator count notice (unit
tests), and the answered-request rule of Phase 2 (unit test).

## Provenance

Filed by the edit agent from live observation (2026-08-17, session `muxcode`), verified from inbox JSONL + daemon events. The same storm was independently observed by the plan agent as the pump behind the sustained verify-spec echo burst.

**2026-09-08 20:32–20:39, session `muxcode`, observed by plan.** Fourteen `test → review` requests and nineteen `review → test` responses in seven minutes; `loop-detected test` at 20:37:01. The test pane showed the injected prompt accreting every prior reply — `… | Final review complete: LGTM … EXIT=0 | Final review complete …` — wrapped in the request-only reply/chain reminder, because edit's `halt` requests (`task-detected test task halt from edit`, 20:37:59 and 20:38:06) were batched into the same injection as the queued responses; each injection re-ran `go test` and re-fired the success chain to review. Same mechanism as the 08-17 storm, this time under a running graph (`1788913399-spec-to-pr-83bd9ed6`, whose own `test` node had already been closed by a chrome line — [MUX-154](../completed/MUX-154-codex-status-line-closes-tracked-tasks.md)).

## Status

Complete — closed 2026-11-14 by `55-defect-backlog-loop` run `1794626267`. On the typed-injection
roads (OpenCode and scrape-road Codex) a response is no longer typed into an agent's pane as a
prompt, and an answered request no longer keeps an agent actionable.

Activated 2026-11-14 by the same run (Defects rank 9, the best-ranked row not parked). Phase 1 (injection policy) is complete and reviewed: on the typed
roads no response is typed in for any role, an orchestrator getting a count-only notice. Phase 2
(answered requests stop being actionable) is complete and reviewed. Phase 3 (integration test) is
complete and reviewed: `scripts/test-response-echo.sh` passes 17 of 17, reproducing the echo on a
pre-fix build. All 14 boxes are ticked. Limits: the script uses stand-in OpenCode panes with no
daemon and no real model; scrape-road Codex, the orchestrator notice and the answered-request rule
are unit-tested only; and acceptance criterion 2 was amended on edit's request, not the user's.

Before starting, check what has already shipped against this spec's mechanism: the Codex hook road
([MUX-159](../completed/MUX-159-codex-hooks-provider.md)) no longer types payloads into the pane,
the test-chain rules of [MUX-213](../completed/MUX-213-unrequested-test-runs-fire-review-chain-and-reviews-answer-twice.md)
and [MUX-216](../completed/MUX-216-test-chain-reply-target-shared-receipt-and-request-reuse.md) stop
a response from standing behind a review, and `MarkResponded` now drains the answered row
(`ConsumeByID`). What remains is the scrape road — OpenCode and scrape-road Codex — and whether
`HasActionableMessages` still counts an answered request.
