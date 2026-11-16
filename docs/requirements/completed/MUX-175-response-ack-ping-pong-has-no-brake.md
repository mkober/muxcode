# MUX-175: Two Agents Can Acknowledge Each Other Forever — Response Traffic Has No Brake

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
| 3 | What happens to a suppressed response | **Not delivered**; a lifecycle row names the pair and the action; the sender is told `NOT SENT - <reason>` and exits 1 ([MUX-155](./MUX-155-send-dedup-keys-on-target-not-sender.md)'s `notSent` pattern). **A response answering an open tracked request is never suppressed. A request is never suppressed** |

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

### Findings at activation (2026-11-16)

Re-read by plan against `3d24d22`. No response brake exists and `DetectMessageLoop` still only
alerts, so the defect stands. Three statements in this spec do not match the tree, and each bears on
Phase 1.

| Spec says | Tree at `3d24d22` |
|-----------|-------------------|
| "Relay-loop suppression in `bus.Send` drops identical non-edit `request` relays" | The relay-loop guard is in the **CLI**, not `bus.Send`: `relaySuppressLimits` and its check live in `cmd/send.go` (`:509–521`). A response sent by any other road — the harness, a daemon-synthesized reply — never passes it. The decision puts the response brake at `bus.Send`, so it is a new check there, not an extension of the relay guard; "extend the suppression that already exists" under Technical approach should be read that way |
| The refusal reaches the sender "through `notSent`" | `notSent` is a CLI helper (`cmd/send.go:23`). `bus.Send` reports a refusal as a sentinel error the CLI maps to `NOT SENT`: `ErrSendSuppressed` (`bus/inbox.go:22`), `ErrReplySuppressed` (`:28`), `ErrSelfSendDropped` (`:33`), and since MUX-145 `ErrNoConsumer` (`bus/consumer.go:12`). The brake needs its own sentinel on that road, and callers other than the CLI must not read it as a failure worth retrying |
| Nothing dedups a response | One thing now does: a **second reply to a task already answered** is refused with `ErrReplySuppressed` (MUX-155, `bus/inbox.go:202`). That covers a duplicate reply to one request; it does not cover the ping-pong, where each ack carries a fresh `--reply-to` or none |
| `DetectMessageLoop` in `bus/guard.go` | `:177`, unchanged |
| — | `bus.Send` now refuses a message to a role with no consumer first (`refuseWithoutConsumer`, `bus/inbox.go:346`, MUX-145). Where the brake sits relative to it, and to the auto-CC copy, is Phase 1's to place; the criterion that a suppressed response reaches "neither inbox nor the auto-CC copy" fixes the outcome, not the order |

"An open tracked request" — the exemption in Decision 3 — has an existing definition to reuse rather
than invent: an in-flight task under `tasks/` whose id the response's `--reply-to` names. A response
with no `--reply-to`, or one naming a completed task, is not exempt. *Widened in Phase 1 and accepted
by the user 2026-11-16: any request not yet answered counts, tracked or not — see Results for
Phase 1.*

### Scope boundary

In scope: a brake on response-type ping-pong, and whatever content test distinguishes it from a
substantive exchange. Not in scope: request relay suppression (already exists and works),
`DetectMessageLoop`'s detection itself (it was correct both times), auto-CC's routing, or the agent
definitions' "never reply to a response" guidance — that guidance is right, but it is instruction, and
this spec exists because instruction alone did not hold.

## Requirements

### Acceptance criteria

Rewritten 2026-11-15 to Decisions 1–3.

- [x] A response whose text, normalized for case, whitespace and punctuation, equals the previous
      response delivered on the same `(from, to, action)` within 300 s is suppressed on that first
      repeat — a pair trading `Acknowledged.` stops without a human issuing a directive
- [x] A substantive response exchange — new content each round — is **not** suppressed, pinned by a
      test built from the 10:42:37 `plan <-> edit` shape; two responses differing only in a number or
      an id (`10/23`, `11/23`) are both delivered
- [x] A suppressed response is not delivered — in neither inbox nor the auto-CC copy — and the sender
      gets `NOT SENT - <reason>` with exit 1
- [x] The suppression writes a lifecycle row naming the pair and the action, so a stopped exchange is
      visible rather than silent
- [x] Suppression is bounded and self-clearing: the same text is delivered again once 300 s have
      passed since the last delivered copy
- [x] A response answering an **open tracked request** is never suppressed, however similar — and,
      as widened and accepted by the user 2026-11-16, neither is one answering any request not yet
      answered, tracked or not
- [x] The brake cannot suppress a `request`, only a `response`

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

- [x] Add response suppression at `bus.Send`: a `response` whose normalized text (case, whitespace
      and punctuation folded; digits and ids kept) equals the last response delivered on the same
      `(from, to, action)` within the window is not written to any inbox
- [x] Exempt a response that answers an open tracked request, and every `request`
- [x] Return the refusal through `notSent`: `NOT SENT - <reason>`, exit 1
- [x] Emit a lifecycle row when a response is suppressed, naming the pair and the action
- [x] Window 300 s by default, `MUXCODE_RESPONSE_SUPPRESS_WINDOW` overriding it; a suppressed attempt
      neither replaces the comparison text nor restarts the window

### Results for Phase 1

Recorded 2026-11-16 from the worker's request; `bus/response_brake.go` and its call sites were read
by plan. Uncommitted. Verified and ticked 2026-11-16 once all three had run after the last edit
(15:25:16): `./build.sh` exit 0 at 15:52:15, `./test.sh` exit 0 at 15:53:45, review at 15:54:24 with
0 must-fix, 0 should-fix, 1 nit (`/tmp/muxcode-review-1794862430.txt`) — its second pass; the first
returned the two blockers recorded below, and the nit is against the lock-failure wording, fixed in
that row.

| Item | Result |
|------|--------|
| The brake | `brakeRepeatedResponse` (`bus/response_brake.go`), reached only through `withResponseBrake`, which `sendMessage` wraps around delivery (`bus/inbox.go:388`), so a suppressed response reaches neither the recipient's inbox nor the auto-CC copy. It is its own check at `bus.Send`, as the activation findings said it would have to be |
| **Review fixes, same phase** | Two defects review found in the first cut, both fixed before the phase was verified. **Key collision:** the state file was named `<from>_<to>_<action>`, so `a/b` and `a_b` shared one comparison point; the key is now a SHA-256 of the NUL-joined `(from, to, action)`. **A race:** check, delivery and the comparison-point update were not serialized, so two concurrent sends on one tuple could both pass the check, or record in a different order from delivery; they now run under a session lock (`response-brake.lock` in the bus directory) taken in `withResponseBrake` — responses only. If the lock cannot be taken the send goes ahead **without serialization, but still through the brake check**: a repeat is still refused, and only the two-concurrent-sends protection is lost. The code comment on `withResponseBrake` first said "delivered unbraked", which is not what the code does; review's nit of 2026-11-16 caught it, this row first repeated it, and edit corrected the comment the same afternoon (after Phase 1's verification — a comment-only change) |
| Tests added by the fix | `TestResponseBrakeTupleKeysDoNotCollide`, `TestResponseBrakeHoldsTheLock` (deterministic), `TestResponseBrakeSerializesConcurrentSends`. Run task `1794862113-spawn-926e95e5-cd6b1a56`: 9 passed, 0 failed; with the old key the collision test fails; with the lock removed the lock test fails and the concurrent-sends outcome test failed 24 of 50. Two earlier runs failed only on the check script, whose no-lock patch still took the lock |
| Normalization | `normalizeResponse`: lower-cased, and every punctuation or symbol rune turned into a **space**, then whitespace collapsed. Punctuation becomes a space rather than vanishing so a number keeps its separators: `10/23` and `11/23` differ, and so do `10/23` and `102/3` |
| The comparison point | One small file per `(from, to, action)` under the bus directory (`response-last/<hash>.json`, the hash described below): the normalized text of the last response **delivered** and when, in milliseconds. `recordDeliveredResponse` writes it only on delivery (`inbox.go:390`), so a suppressed retry neither replaces the text nor restarts the window |
| The window | `MUXCODE_RESPONSE_SUPPRESS_WINDOW` in seconds, default 300 |
| The refusal | Sentinel `ErrResponseSuppressed`; `cmd/send.go` maps it to `NOT SENT`, exit 1. The error text names the pair, the action and how long ago the matching response was delivered |
| The row | `response-suppressed` (warn, source `bus`): `<from>→<to>:<action> repeats the response delivered <n>s ago — not delivered`. One row per suppressed attempt, plus one stderr line |
| Never braked | A `request`. A response while the window is `0` |
| Fixture change | `bus/chain_request_test.go`: each case now gets its own bus, since cases sharing one tripped the brake on each other |
| Tests — Phase 2's, written in this phase | `bus/response_brake_test.go`: `TestNormalizeResponse`, `TestResponseBrakeStopsRepeatedAck`, `TestResponseBrakeLetsSubstantiveExchangeThrough` (the `plan <-> edit` `verify-spec` shape), `TestResponseBrakeExemptions`, `TestResponseBrakeClearsAfterWindow` |
| Run | Run task `1794859486-spawn-926e95e5-bf03b316`: 11 passed, 0 failed — full `bus`/`cmd`/`daemon` passing; with the brake ignored the stop test fails and so does only the braked-repeat line of the exemptions test; live, the CLI prints `NOT SENT`, exits 1 and the row is written |

**Departure 1 accepted by the user 2026-11-16** (relayed by edit, `1794860351-edit-fed389eb`): any
request not yet answered, tracked or not, is exempt from the brake. Decision 3's "open tracked
request" is to be read that way from here on.

**Departure 2 accepted by the user 2026-11-16** (relayed by edit, `1794863242-edit-d3df125e`):
`MUXCODE_RESPONSE_SUPPRESS_WINDOW=0` stays as the emergency off switch for the response brake. The
docs written in Phase 3 stand as they are. Both departures are now decisions; nothing awaits the user.

**Two departures from the user's decisions — the worker's, for the user to accept or reverse.**

| Departure | What the decision said | What was built, and why |
|-----------|------------------------|-------------------------|
| The exemption is wider | Decision 3: "a response answering an **open tracked request** is never suppressed" — plan's activation reading was an in-flight task named by `--reply-to` | `answersOpenRequest` also exempts a response whose `--reply-to` names **any request with no response recorded yet**, tracked or not. Reason given: a graph spawn worker's seed has no task, and a terse worker answers each loop iteration's seed with identical text — `TestExecSpawnLoopReusesWorker` failed until this was added. Without it the brake would suppress a worker's legitimate second-iteration reply and stall a graph run. An ack ping-pong still gets no exemption: its replies name nothing, a response, or a request already answered |
| An off switch | The decisions name a window and its override, nothing more | `MUXCODE_RESPONSE_SUPPRESS_WINDOW=0` turns the brake off entirely. Not asked for; the same shape as `MUXCODE_RELAY_SUPPRESS_THRESHOLD=0` and, since MUX-145, `MUXCODE_CONSUMER_GUARD=0` |

Something the brake's key makes possible and no test covers: the comparison is per `(from, to,
action)`, not per request. Two **different** already-answered requests answered again with the same
short text inside the window — or an agent sending the same unsolicited status line twice in five
minutes — are braked. That is the decision working as written, and the reason the sender is told
`NOT SENT` rather than left to assume delivery.

### Phase 2: Tests

- [x] Positive: the build↔test `Acknowledged.` shape is stopped on the first repeat, and a repeat
      differing only in case, spacing or punctuation is stopped too
- [x] **Negative control:** the plan↔edit `verify-spec` shape — new content each round — is never
      suppressed, and `10/23` followed by `11/23` are both delivered
- [x] **Negative control:** an identical response that answers an open tracked request is delivered
- [x] A suppressed pair can converse again after the window clears, including while it kept retrying
      during the window

### Results for Phase 2

Recorded 2026-11-16 from the worker's request; the stop test and the substantive-exchange test were
read by plan. The tests were written during Phase 1; this phase filled one gap. Uncommitted. Verified
and ticked 2026-11-16 once all three had run after the last edit (15:55:12): `./build.sh` exit 0 at
15:58:15, `./test.sh` exit 0 at 15:59:44, review at 16:00:21 with 0 must-fix, 0 should-fix, 0 nits
(`/tmp/muxcode-review-1794862790.txt`). Plan read all four tests before ticking.

| Step | Test (`bus/response_brake_test.go`) | What it holds |
|------|-------------------------------------|---------------|
| Positive | `TestResponseBrakeStopsRepeatedAck` | After one delivered `Acknowledged.` from build to test, the repeats `Acknowledged.`, `acknowledged` and ` ACKNOWLEDGED !` are each refused with `ErrResponseSuppressed`. Test's inbox holds only the first, edit's auto-CC gains no copy, and three `response-suppressed` rows name `build→test:test` |
| Negative control — substantive | `TestResponseBrakeLetsSubstantiveExchangeThrough` | Five `plan ↔ edit` `verify-spec` responses, new content each round (evidence, a correction, its acceptance), are all delivered. **The gap filled in this phase:** two back-to-back reports from the same sender, `Phase 4: 10/23 ticked` then `Phase 4: 11/23 ticked`, differing only in a number, are both delivered |
| Negative control — open request | `TestResponseBrakeExemptions` | An identical `done` answering a tracked request (a task created for it) is delivered; a spawn worker's identical answer to each loop iteration's untracked seed is delivered; the same text naming an already-answered request is braked; repeated requests are never braked |
| Clears after the window | `TestResponseBrakeClearsAfterWindow` | With a 2 s window: a retry at 1.1 s is refused, and at 2.2 s after the delivered copy the same text is delivered — the retry did not restart the window. A window of `0` delivers at once. The test sleeps 2.2 s of real time |
| Run | — | Run task `1794862523-spawn-926e95e5-d5d649be`: 5 passed, 0 failed — the eight brake tests three times over, full `./bus` ok; with the brake ignored the stop test fails and the substantive control still passes |

### Phase 3: Docs

- [x] `CLAUDE.md` relay-loop bullet: state that responses are now braked too, and how the content test
      keeps substantive exchanges alive
- [x] [`docs/architecture.md`](../../architecture.md): the same, beside `DetectMessageLoop`
- [x] [`docs/agent-bus.md`](../../agent-bus.md): a row in `muxcode send`'s "A message that was not
      written says so" table, and the window variable in [`docs/configuration.md`](../../configuration.md)

### Results for Phase 3

Written 2026-11-16. Verified and ticked the same day once all three had run after the last doc edit
(16:02:07): `./build.sh` exit 0 at 16:02:55, `./test.sh` exit 0 at 16:03:16, review at 16:03:51 with
0 must-fix, 0 should-fix, 0 nits (`/tmp/muxcode-review-1794863002.txt`).

| Doc | What was added |
|-----|----------------|
| `CLAUDE.md` | Relay-loop bullet: the response brake, its match rule, the exemption, the lock, and that the relay guard is in `cmd/send.go` (the worker's edit) |
| `docs/architecture.md` | A new subsection, "Loop detection and the two brakes", after the daemon-identity list — there was no existing text beside `DetectMessageLoop` to extend. It sets the detector (alert-only), the relay-loop guard (the CLI) and the response brake (`bus.Send`) side by side, then the brake's mechanism (plan) |
| `docs/agent-bus.md` | `muxcode send`: a row in the "A message that was not written says so" table, and a "The response brake" table written for whoever meets a `NOT SENT` — what matches, what it is compared against, what is never braked, what it does not stop (plan) |
| `docs/configuration.md` | `MUXCODE_RESPONSE_SUPPRESS_WINDOW`, including that `0` turns the brake off (plan) |

The off switch was documented because it was in the code while its fate was still the user's call;
the user kept it later the same day (see Results for Phase 1), so these lines stay.

### Phase 4: Integration test

- [x] `scripts/test-response-pingpong.sh`: drive two scratch agents into an ack exchange, assert it
      stops without intervention, that the repeat's sender read `NOT SENT` with exit 1 and that a
      lifecycle row names the pair
- [x] Include the substantive-exchange case as a section that must **not** trip the brake, and a
      reply to an open tracked request that must be delivered though identical
- [x] Coverage floor set so a skipped section cannot read as green
- [x] Run through the run agent and record the row here

### Results for Phase 4

Recorded 2026-11-16 from the worker's request; the script's header and both run logs were read by
plan. Uncommitted. Verified and ticked 2026-11-16 once all three had run after the last edit
(16:07:07): `./build.sh` exit 0 at 16:07:47, `./test.sh` exit 0 at 16:08:06, review at 16:08:49 with
0 must-fix, 0 should-fix, 0 nits (`/tmp/muxcode-review-1794863293.txt`).

| Item | Result |
|------|--------|
| The script | `scripts/test-response-pingpong.sh` — muxcode built from the tree (`MUXCODE_RESPONSE_PINGPONG_BIN` overrides), scratch HOME, state, lifecycle log and config, one `BUS_SESSION` per section, no daemon and no tmux server. The two "agents" are shell loops that answer every message they read with `Acknowledged.` — the worst case. The CLI's own window dedup is turned off (`MUXCODE_DEDUP_WINDOW=0`) so that only the brake decides. About 45 s |
| A — the ack loop | Seeded once, it stops by itself: 2 acks delivered and none after 5 s; the repeat's sender read `NOT SENT` with exit 1; a `response-suppressed` row names `build↔test:test` |
| B — control | The same agents on a muxcode built from a copy of the tree with the brake's result ignored: **34 acks in 8 s** |
| C — negative control | A substantive `plan ↔ edit` `verify-spec` exchange, new text each round, and `10/23` then `11/23` back to back: every response delivered, exit 0 |
| D — negative control | A reply to an open tracked request is delivered though identical to the last response on its pair; a later untracked repeat is refused `NOT SENT`, exit 1 |
| Run 1 | Proc `1794863135-proc-999122b7` (run task `1794863127…`): 7 passed, 1 failed — the script's agent logged the bus's stderr notice ahead of the `NOT SENT` line it was checking for; a fault in the script, fixed |
| Run 2 | Proc `1794863188-proc-f73bda7a` (run task `1794863181-spawn-926e95e5-3db89449`): 8 passed, 0 failed, floor 8, exit 0 |
| MUX-145's guard | The script does **not** set `MUXCODE_CONSUMER_GUARD=0`: with no tmux server the consumer check reads as indeterminate and lets the sends through. One script fewer for [MUX-230](../backlog/MUX-230-integration-scripts-opt-out-of-the-consumer-guard.md) to convert, though the guard is passed here on its indeterminate branch, not exercised |
| Not covered | The window clearing end to end (the unit test covers it with a 2 s window), and the untracked-request half of the widened exemption (a spawn seed) — section D uses a tracked request |
| Docs | `CLAUDE.md` integration-test entry `test-response-pingpong` (the worker) |

## Notes

**Why instruction was not enough.** Both agents were told, in their definitions, not to reply to
response-type messages. The loop happened anyway, twice, and needed a human to name it before it
stopped. That is the argument for a mechanism at `bus.Send` rather than stronger wording.

**Parked 2026-11-14 by plan**, at `55-defect-backlog-loop` selection (run
`1794626267-55-defect-backlog-loop-15547e2e`), under the rule that a spec holding an unrecorded
design decision is not worked unattended. Checked against main `7b73f05`: no response brake exists;
`DetectMessageLoop` (`bus/guard.go:177`) still only alerts. Two things have narrowed the exposure
since filing — responses are no longer typed into a listenerless agent's pane
([MUX-009](./MUX-009-response-echo-chain-retrigger.md), `splitWakeBatch`),
and a Codex test agent woken with no request is told to send no reply (MUX-213) — but an agent that
reads its own inbox still receives every response.

The three decisions it was parked on — the brake's shape, what "near-identical" means, and what
happens to a suppressed response — were answered by the user on 2026-11-15 and are recorded under
[Decisions](#decisions--the-users-recorded-2026-11-15). **Unparked 2026-11-15.**

## Status

**Complete** — activated and closed 2026-11-16 by `55-defect-backlog-loop` run `1794626267-55-defect-backlog-loop-15547e2e` (Defects rank 14 then; ranks 1–13 were parked). 23/23: Phases 1–4 complete and all seven criteria met. Uncommitted at close-out; the run's ship step commits it. Two things are covered by unit tests only and not by the integration script: the window clearing, and the untracked half of the exemption (see Results for Phase 4). Both departures from the original decisions were accepted by the user 2026-11-16 — the widened exemption and `MUXCODE_RESPONSE_SUPPRESS_WINDOW=0` as an emergency off switch. Nothing awaits the user.

History: filed 2026-09-10. Parked 2026-11-14; unparked 2026-11-15 once the user's Decisions 1–3 were recorded and the criteria and phases rewritten to them (17 items became 23).
