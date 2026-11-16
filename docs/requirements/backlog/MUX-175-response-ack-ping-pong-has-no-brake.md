# Two Agents Can Acknowledge Each Other Forever — Response Traffic Has No Brake

**Tracking:** [mkober/muxcode#133](https://github.com/mkober/muxcode/issues/133)

**Provenance:** filed 2026-09-10 on the user's explicit request. Related: MUX-169 (self-reply echo,
fixed by dropping at the source), and the relay-loop suppression that already exists for requests.

Build and test spent roughly one round trip every 4–5 seconds answering each other's
acknowledgements, degrading to bare `Acknowledged.` and `Confirmed: …` in both directions. The daemon
saw it and said so — and then nothing happened, because the detector only alerts.

## Context

### Observed (2026-09-10, session `muxcode`)

| When | Row |
|------|-----|
| 10:15:18 | `loop-detected  build  type=message` — the daemon's pane form named it `ping-pong build <-> test action:test 6x in 56s` |
| 10:17:20 | `loop-detected  edit  type=message` |
| 10:24:27 | `loop-detected  edit  type=message` |
| 10:25:27 | `loop-detected  build  type=message` |

It recurred after being cleared once. Auto-CC copies build/test traffic to edit, so the loop also
flooded the orchestrator's inbox — edit's listener woke repeatedly on ack pairs.

### Why nothing stops it — verified

- The messages are **`response`-type**. Relay-loop suppression in `bus.Send` drops identical
  **non-edit `request` relays** above a threshold (`MUXCODE_RELAY_SUPPRESS_THRESHOLD`, default 4,
  within a 300 s window). Responses are outside that path entirely — they are never deduped.
- `DetectMessageLoop` (`bus/guard.go`) recognises this exact pattern and **only alerts**. This is the
  documented design ("Response ping-pong is only reported"), so the gap is deliberate rather than a
  regression — but the incident shows alert-only is not sufficient when both ends are agents that
  reply by default.

### The manual remedy, for the runbook

Draining alone did **not** work — new acks were generated faster than the drain:

```
muxcode inbox --role test --from build     # drain one direction
muxcode inbox --role build --from test     # and the other
```

repeated two or three times, **plus** an explicit directive to both agents ("never reply to a
response-type message"). Build then confirmed: *"The response exchange with test is stopped."* The
directive, not the drain, is what ended it.

### The caution that shapes the fix

At 10:42:37 the same detector fired for `plan <-> edit action:verify-spec` — and that exchange was
**entirely substantive**: each round carried new evidence, then a correction, then the correction
being accepted. A brake that counted round trips alone would have severed a working conversation at
its most useful moment.

So the discriminator cannot be frequency. It has to be **whether the payload carries new
information**. Two candidate shapes, both worth costing before choosing:

| Shape | Mechanism | Risk |
|-------|-----------|------|
| Content-similarity suppression | drop a response substantially identical to the previous one on the same `(from,to,action)` | needs a similarity measure that is cheap and stable; near-duplicates with one new fact must survive |
| Consecutive response-only cap | cap consecutive response↔response exchanges between one pair regardless of content | simple and content-blind — would have caught build↔test, but also caps a long substantive exchange |

A hybrid is likely right: cap consecutive **near-identical** responses, so a pair trading new content
is never limited while a pair trading `Acknowledged.` is stopped quickly.

*Superseded 2026-11-15: the user chose content-similarity suppression alone, with no count — see
[Decisions](#decisions--the-users-recorded-2026-11-15).*

### Decisions — the user's, recorded 2026-11-15

Answered by the user in conversation and relayed by edit (`1794787239-edit-1aacf9a4`). The criteria
and phases below were rewritten to them the same day.

| # | Decision | Chosen |
|---|----------|--------|
| 1 | The brake's shape | **Content-similarity suppression only — no consecutive-count cap.** A response too similar to the previous response on the same `(from, to, action)` is suppressed immediately, on the first repeat |
| 2 | What "too similar" means | **An exact match after normalizing case, whitespace and punctuation.** Numbers and ids stay significant: `10/23` and `11/23` differ. **Plus a 5-minute window:** only a match against a previous response sent within 300 s counts, which is what makes the brake self-clearing |
| 3 | What happens to a suppressed response | **Not delivered**; a lifecycle row names the pair and the action; the sender is told `NOT SENT - <reason>` and exits 1 ([MUX-155](../completed/MUX-155-send-dedup-keys-on-target-not-sender.md)'s `notSent` pattern). **A response answering an open tracked request is never suppressed. A request is never suppressed** |

Two points the decisions left open. Plan proposed both readings; **the user confirmed both on
2026-11-16** (relayed by edit, `1794844857-edit-5f52a782`):

| Point | Reading — confirmed | Why |
|-------|----------------|-----|
| What "the previous response" is | The last response on that `(from, to, action)` that was **delivered**. A suppressed attempt does not become the comparison point and does not restart the 300 s | Otherwise a pair that never stops trying stays suppressed for as long as it tries, and "a pair stopped once can converse again later" would hold only for a pair that went quiet |
| The window's override | `MUXCODE_RESPONSE_SUPPRESS_WINDOW`, default 300, beside the relay suppression's `MUXCODE_RELAY_SUPPRESS_WINDOW` | The integration test has to see the window clear without waiting five minutes. No threshold variable exists, because Decision 1 has no count |

**What the chosen measure does not stop, by decision — accepted by the user 2026-11-16.** A loop whose text changes each round — a
counter, a timestamp, a fresh id — is never an exact match, so it is not braked; numbers are
significant so that `10/23` → `11/23` progress reports survive. The 2026-09-10 exchange was braked
only from the point it degraded to bare `Acknowledged.` on one side. `DetectMessageLoop` still alerts
on the frequency pattern either way.

### Scope boundary

In scope: a brake on response-type ping-pong, and whatever content test distinguishes it from a
substantive exchange. Not in scope: request relay suppression (already exists and works),
`DetectMessageLoop`'s detection itself (it was correct both times), auto-CC's routing, or the agent
definitions' "never reply to a response" guidance — that guidance is right, but it is instruction, and
this spec exists because instruction alone did not hold.

## Requirements

### Acceptance criteria

Rewritten 2026-11-15 to Decisions 1–3.

- [ ] A response whose text, normalized for case, whitespace and punctuation, equals the previous
      response delivered on the same `(from, to, action)` within 300 s is suppressed on that first
      repeat — a pair trading `Acknowledged.` stops without a human issuing a directive
- [ ] A substantive response exchange — new content each round — is **not** suppressed, pinned by a
      test built from the 10:42:37 `plan <-> edit` shape; two responses differing only in a number or
      an id (`10/23`, `11/23`) are both delivered
- [ ] A suppressed response is not delivered — in neither inbox nor the auto-CC copy — and the sender
      gets `NOT SENT - <reason>` with exit 1
- [ ] The suppression writes a lifecycle row naming the pair and the action, so a stopped exchange is
      visible rather than silent
- [ ] Suppression is bounded and self-clearing: the same text is delivered again once 300 s have
      passed since the last delivered copy
- [ ] A response answering an **open tracked request** is never suppressed, however similar
- [ ] The brake cannot suppress a `request`, only a `response`

### Technical approach

Extend the suppression that already exists for relays rather than inventing a second mechanism, but
key it on response identity: `(from, to, action)` plus the normalized text of the last delivered
response and when it was sent. There is no counter (Decision 1). `bus.Send` is the choke point — the
same place MUX-169 chose to drop self-replies, for the same reason: an alert-only detector downstream
cannot stop what has already been written. The refusal reaches the CLI through the `notSent` road
MUX-155 built, so the sender reads the reason rather than a `Sent` line for a message written
nowhere.

The test that matters is the **negative** one. A brake that suppresses everything passes a naive
"loop stopped" assertion, so the suite needs both: the build↔test shape must be stopped, and the
plan↔edit shape must survive. Without the second, a brake that severs all response traffic reads as a
fix.

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/inbox.go` | `Send` — where suppression belongs, beside the relay and self-send drops |
| `tools/muxcode/bus/guard.go` | `DetectMessageLoop` — detection; keep, and reuse its pair/action keying |
| `tools/muxcode/bus/dedup.go` | existing dedup shapes to extend rather than duplicate |

## Implementation

### Phase 1: The brake

- [ ] Add response suppression at `bus.Send`: a `response` whose normalized text (case, whitespace
      and punctuation folded; digits and ids kept) equals the last response delivered on the same
      `(from, to, action)` within the window is not written to any inbox
- [ ] Exempt a response that answers an open tracked request, and every `request`
- [ ] Return the refusal through `notSent`: `NOT SENT - <reason>`, exit 1
- [ ] Emit a lifecycle row when a response is suppressed, naming the pair and the action
- [ ] Window 300 s by default, `MUXCODE_RESPONSE_SUPPRESS_WINDOW` overriding it; a suppressed attempt
      neither replaces the comparison text nor restarts the window

### Phase 2: Tests

- [ ] Positive: the build↔test `Acknowledged.` shape is stopped on the first repeat, and a repeat
      differing only in case, spacing or punctuation is stopped too
- [ ] **Negative control:** the plan↔edit `verify-spec` shape — new content each round — is never
      suppressed, and `10/23` followed by `11/23` are both delivered
- [ ] **Negative control:** an identical response that answers an open tracked request is delivered
- [ ] A suppressed pair can converse again after the window clears, including while it kept retrying
      during the window

### Phase 3: Docs

- [ ] `CLAUDE.md` relay-loop bullet: state that responses are now braked too, and how the content test
      keeps substantive exchanges alive
- [ ] [`docs/architecture.md`](../../architecture.md): the same, beside `DetectMessageLoop`
- [ ] [`docs/agent-bus.md`](../../agent-bus.md): a row in `muxcode send`'s "A message that was not
      written says so" table, and the window variable in [`docs/configuration.md`](../../configuration.md)

### Phase 4: Integration test

- [ ] `scripts/test-response-pingpong.sh`: drive two scratch agents into an ack exchange, assert it
      stops without intervention, that the repeat's sender read `NOT SENT` with exit 1 and that a
      lifecycle row names the pair
- [ ] Include the substantive-exchange case as a section that must **not** trip the brake, and a
      reply to an open tracked request that must be delivered though identical
- [ ] Coverage floor set so a skipped section cannot read as green
- [ ] Run through the run agent and record the row here

## Notes

**Why instruction was not enough.** Both agents were told, in their definitions, not to reply to
response-type messages. The loop happened anyway, twice, and needed a human to name it before it
stopped. That is the argument for a mechanism at `bus.Send` rather than stronger wording.

**Parked 2026-11-14 by plan**, at `55-defect-backlog-loop` selection (run
`1794626267-55-defect-backlog-loop-15547e2e`), under the rule that a spec holding an unrecorded
design decision is not worked unattended. Checked against main `7b73f05`: no response brake exists;
`DetectMessageLoop` (`bus/guard.go:177`) still only alerts. Two things have narrowed the exposure
since filing — responses are no longer typed into a listenerless agent's pane
([MUX-009](../completed/MUX-009-response-echo-chain-retrigger.md), `splitWakeBatch`),
and a Codex test agent woken with no request is told to send no reply (MUX-213) — but an agent that
reads its own inbox still receives every response.

The three decisions it was parked on — the brake's shape, what "near-identical" means, and what
happens to a suppressed response — were answered by the user on 2026-11-15 and are recorded under
[Decisions](#decisions--the-users-recorded-2026-11-15). **Unparked 2026-11-15.**

## Status

**Backlog** — 0/23. Filed 2026-09-10. Parked 2026-11-14; unparked 2026-11-15 once the user's Decisions 1–3 were recorded and the criteria and phases rewritten to them (17 items became 23).
