# Two Agents Can Acknowledge Each Other Forever — Response Traffic Has No Brake

**Tracking:** filed 2026-09-10 on the user's explicit request. Related: MUX-169 (self-reply echo,
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

### Scope boundary

In scope: a brake on response-type ping-pong, and whatever content test distinguishes it from a
substantive exchange. Not in scope: request relay suppression (already exists and works),
`DetectMessageLoop`'s detection itself (it was correct both times), auto-CC's routing, or the agent
definitions' "never reply to a response" guidance — that guidance is right, but it is instruction, and
this spec exists because instruction alone did not hold.

## Requirements

### Acceptance criteria

- [ ] A pair of agents exchanging near-identical response-type messages is stopped automatically,
      without a human issuing a directive
- [ ] A substantive response exchange — new content each round — is **not** suppressed, pinned by a
      test built from the 10:42:37 `plan <-> edit` shape
- [ ] The suppression writes a lifecycle row naming the pair and the action, so a stopped exchange is
      visible rather than silent
- [ ] Suppression is bounded and self-clearing: a pair stopped once can converse again later
- [ ] The brake cannot suppress a `request`, only a `response`

### Technical approach

Extend the suppression that already exists for relays rather than inventing a second mechanism, but
key it on response identity. `bus.Send` is the choke point — the same place MUX-169 chose to drop
self-replies, for the same reason: an alert-only detector downstream cannot stop what has already been
written.

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

- [ ] Add response-ping-pong suppression at `bus.Send`, keyed on `(from, to, action)` plus payload
      similarity
- [ ] Emit a lifecycle row when a response is suppressed
- [ ] Make the threshold and window configurable, defaulting conservatively

### Phase 2: Tests

- [ ] Positive: the build↔test `Acknowledged.` shape is stopped within the threshold
- [ ] **Negative control:** the plan↔edit `verify-spec` shape — new content each round — is never
      suppressed
- [ ] A suppressed pair can converse again after the window clears

### Phase 3: Docs

- [ ] `CLAUDE.md` relay-loop bullet: state that responses are now braked too, and how the content test
      keeps substantive exchanges alive
- [ ] [`docs/architecture.md`](../../architecture.md): the same, beside `DetectMessageLoop`

### Phase 4: Integration test

- [ ] `scripts/test-response-pingpong.sh`: drive two scratch agents into an ack exchange, assert it
      stops without intervention and that a lifecycle row names the pair
- [ ] Include the substantive-exchange case as a section that must **not** trip the brake
- [ ] Coverage floor set so a skipped section cannot read as green
- [ ] Run through the run agent and record the row here

## Notes

**Why instruction was not enough.** Both agents were told, in their definitions, not to reply to
response-type messages. The loop happened anyway, twice, and needed a human to name it before it
stopped. That is the argument for a mechanism at `bus.Send` rather than stronger wording.

## Status

Backlog
