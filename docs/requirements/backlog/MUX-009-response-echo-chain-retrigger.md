# Response Echo Chain Retrigger

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

- [ ] Non-hook `SendWakeUp` does not inject `type: response` payloads as prompts; responses are summarized or dropped, never handed over as instructions
- [ ] A response-only wake-up with no request in the batch performs no injection at all
- [ ] A request that has been responded to is no longer counted as actionable for wake-up purposes (requester-side equivalent of `MarkResponded`'s inbox drain)
- [ ] Regression test: queue a response-only inbox for a non-hook role, assert no pane injection occurs
- [ ] Regression test: an answered-but-unconsumed request does not keep an agent actionable

### Key files

| File | Change |
|------|--------|
| `bus/provider_opencode.go` | `SendWakeUp()`: exclude response payloads from injection, no-op on response-only batches |
| `bus/provider_codex.go` | Same policy as OpenCode |
| `bus/inbox.go` | `HasActionableMessages()`: exclude requests with a recorded response (delivery-store lookup) |
| `bus/delivery.go` | Expose responded-status lookup for the actionable check |

### Design consideration

Fourth instance in one day of the same root pattern — the non-hook wake-up path injecting content the hook path already learned not to: (1) unbounded wake-up argv (fixed — `BoundWakeUpBatch`), (2) unbounded `compact-recommended` accumulation (fixed — `HasPendingAction`), (3) unverified auto-restart (filed — [`unverified-daemon-auto-restart`](./MUX-008-unverified-daemon-auto-restart.md)), (4) this. Consider a single explicit policy for "what may become a prompt" in the non-hook wake-up path rather than continuing to patch cases individually.

## Implementation

### Phase 1: Injection policy

- [ ] Filter `type: response` payloads out of non-hook `SendWakeUp` injection; summarize or drop, never inject as prompt
- [ ] Response-only batch → no injection at all
- [ ] Unit tests: response-only batch injects nothing; mixed batch injects only requests

### Phase 2: Answered requests stop being actionable

- [ ] `HasActionableMessages` excludes requests whose delivery status is `responded`
- [ ] Unit test: answered-but-unconsumed request does not wake the agent

### Phase 3: Integration test

- [ ] Create `scripts/test-response-echo.sh` (requires a running muxcode session with a non-hook agent)
- [ ] Test: send a request to a non-hook role, let it respond, assert the requester's chain does not re-fire on the response wake-up
- [ ] Test: queue only responses for a non-hook role, assert no pane injection occurs
- [ ] Run the script and verify all checks pass

## Provenance

Filed by the edit agent from live observation (2026-08-17, session `muxcode`), verified from inbox JSONL + daemon events. The same storm was independently observed by the plan agent as the pump behind the sustained verify-spec echo burst.

## Status

Backlog
