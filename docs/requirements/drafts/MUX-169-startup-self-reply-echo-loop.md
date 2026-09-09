# The Startup Bootstrap's Self-Reply Rides Its Own Exemption Back Into the Sender's Inbox

`PreLaunchSetup` seeds every agent's inbox with one self-addressed `request:startup` — the bootstrap
that makes the daemon keep waking the agent until it restores context. `isLoopingSelfSend` refuses
every other self-addressed message at `Send`, and until 2026-09-09 it exempted the bootstrap **by
action alone**. The agent's own reply to the bootstrap is self-addressed too, and it carries the same
action — so a `response:startup` sent with `--reply-to` rode the exemption straight back into the
sender's inbox. On the codex hook road, where `hook stop` blocks the turn's end with the inbox as the
next prompt, that reply became a prompt, the prompt a reply, and the test agent acknowledged its own
acknowledgement every five seconds: 16 rows in a minute. `DetectMessageLoop` caught it —
`loop-detected test type=message` at 15:35:14 — and could do nothing more: the detector is
alert-only, an event to edit under a 600 s cooldown, and nothing on the bus drops what it flags.

## Context

### Observed (session `muxcode`, codex test agent on the hook road, 2026-09-09)

| When | What | Source |
|------|------|--------|
| 15:34:16 | `launch role=test cli=codex` after the session relaunch; bootstrap `request:startup` seeded | lifecycle |
| 15:35:14 | `loop-detected test type=message` — reported, not suppressed | lifecycle |
| 15:35–15:36 | 16 × `test → test [response:startup] Acknowledged.` | `muxcode history test` |
| 15:36 | edit's `request:test` "STOP the startup ack loop…" lands between two more echoes | `muxcode history test` |
| 15:36:31 | `inbox-notify test` — the daemon still waking it for its own reply | lifecycle |
| ~15:40 | fix in tree; edit's request `1788982822` to plan | bus |

The build agent (codex too) escaped only because its reply used action `response`, which the
action-only exemption did not cover. Edit's report counted ~25 messages each CC'd to edit; the store
holds 16 in test's history and no CC rows in edit's — the figures above are the store's.

### Mechanism — verified in code

- `bus/inbox.go:122` `isLoopingSelfSend` — before: `m.From == m.To && m.Action != "startup"`. The
  bootstrap request and the agent's reply to it share `From == To` and `Action == "startup"`; only
  `Type` separates them, and `Type` was not read.
- `bus/inbox.go:171–183` `sendMessage` — a self-send that is a reply takes `recordUndeliveredReply`
  (delivery status + `MarkResponded`, no inbox write); any other self-send is dropped. The exempted
  reply bypassed both and was appended to the sender's inbox like any request, then CC'd by the
  ordinary auto-CC rule.
- `bus/inbox.go:631–632` — the consume-side unnotified/hook path uses the same predicate, so a reply
  already on disk was surfaced there too.
- `docs/hooks.md` "Delivery without a listener" — on the codex hook road `hook stop` consumes the
  pending inbox and blocks the stop with the messages as the next prompt. A delivered self-reply is
  therefore a new turn, and the daemon's 5 s `checkIdleAgents` sets the cadence.
- `bus/guard.go:162` `DetectMessageLoop` — it saw the echo (`loop-detected test type=message` at
  15:35:14, then an `event` to edit under the 600 s cooldown) and that is all it can do: the
  detector is alert-only and never drops or suppresses (CLAUDE.md: "Response ping-pong is only
  reported"); the `inbox.go:118` comment on the fix says the same.
- Why Claude agents never hit it: the planner and editor definitions say no reply is needed for the
  startup message. The bootstrap's own "To reply:" line says otherwise, and a codex agent obeys the
  line in front of it — the trap was any agent that followed the printed reply instruction.

### The fix (in tree, uncommitted)

`isStartupBootstrap(m)` — `Type == "request" && Action == "startup"` — is the exemption;
`isLoopingSelfSend` delegates to it. A self-addressed `response:startup` is now a looping self-send
that happens to be a reply, so it takes `recordUndeliveredReply`: correlated (the bootstrap's
delivery status reads `responded`, `HasActionableMessages` goes false, the daemon stops waking),
never delivered, never CC'd. Three tests: `TestIsLoopingSelfSend` (table), `TestStartupSelfReplyNotDelivered`
(bootstrap delivered and actionable as the negative control; reply absent from the sender's inbox
and from edit's; bootstrap `responded`; no longer actionable) and `TestStaleStartupSelfReplyFiltered`
(a reply already on disk is invisible to `UnnotifiedMessages` and `ConsumeInboxForHook`).

## Requirements

### Acceptance criteria

- [x] A self-addressed `response:startup` is never delivered to its sender's inbox and never CC'd to edit — on the send path and on the hook consume path alike — _`TestStartupSelfReplyNotDelivered` (send path) and `TestStaleStartupSelfReplyFiltered` (hook consume path); suite green 15:49:59, review 15:51:44 `EXIT=0`_
- [x] The reply still correlates its bootstrap: delivery status `responded`, `HasActionableMessages` false afterward, so the daemon stops re-waking the agent — _pinned in `TestStartupSelfReplyNotDelivered`_
- [x] The bootstrap request itself stays delivered and actionable (negative control — the exemption is narrowed, not removed) — _the negative-control assertion in both tests_
- [ ] A relaunched codex agent on the hook road writes no `loop-detected <role> type=message` row and at most one `response:startup` to its history
- [x] Docs name the rule — the bootstrap **request** is the one self-send the bus delivers, keyed on type and action — in `CLAUDE.md`, `docs/architecture.md`, `docs/hooks.md` and `docs/agent-bus.md` — _CLAUDE.md wake-up bullet (edit); the other three by plan, 15:45_

### Technical approach

Key the exemption on the message's type as well as its action, and let the reply fall through to the
branch that already exists for self-addressed replies. Nothing new is invented: `recordUndeliveredReply`
was written for edit answering a graph node whose reply path normalizes back to edit, and it does
exactly what a bootstrap reply needs — correlation without delivery.

**Rejected — teach the definitions not to reply.** The planner and editor files already say so and a
codex agent replied anyway, because the bootstrap prints a reply instruction. Prose does not hold
this line; the bus must.

**Rejected — leave it to `DetectMessageLoop`.** It did alert, inside a minute; the detector is
alert-only by design ([MUX-032](../backlog/MUX-032-loop-detector-granularity.md) is its
granularity work, not a drop path), so an alert is all it can ever give. The echo has to be
refused where it enters.

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/inbox.go` | `isLoopingSelfSend` (122), `isStartupBootstrap` (131), `recordUndeliveredReply` (140), the send branch (171–183), the consume-side predicate (631–632) |
| `tools/muxcode/bus/inbox_test.go` | `TestIsLoopingSelfSend`, `TestStartupSelfReplyNotDelivered`, `TestStaleStartupSelfReplyFiltered` |
| `tools/muxcode/bus/guard.go` | `DetectMessageLoop` (162) — alerted on the echo; alert-only, cannot stop one |
| `CLAUDE.md` | wake-up bullet (edit) |
| `docs/architecture.md`, `docs/hooks.md`, `docs/agent-bus.md` | notification list, hook-road delivery paragraph, launch section (plan) |
| `scripts/test-codex-hooks.sh` | hermetic section pattern for Phase 3 |

## Implementation

### Phase 1: Refuse the echo at `Send` (in tree)

- [x] `isStartupBootstrap` (type `request` and action `startup`); `isLoopingSelfSend` delegates to it on both the send and consume paths — _review nit 15:51 (collapse to the one-line predicate) applied by edit; targeted re-test 15:54:44 4/4 exit 0_
- [x] `TestIsLoopingSelfSend`: bootstrap request exempt; `response:startup` self-send looping; ordinary self-send looping
- [x] `TestStartupSelfReplyNotDelivered`: bootstrap actionable (negative control) → reply absent from the sender's inbox and edit's, bootstrap `responded`, no longer actionable
- [x] `TestStaleStartupSelfReplyFiltered`: a reply already on disk is invisible to `UnnotifiedMessages` and `ConsumeInboxForHook`
- [x] Suite green (`./test.sh` row in the test store — the store was truncated by the 15:34 re-init, so the row must be fresh) and review `EXIT=0` — _`./test.sh` row 15:49:59 exit 0 (2551 PASS / 0 FAIL / 2 SKIP across 5 packages, `go vet` clean) on run `d67cd45e`'s retried test node; review 15:51:44 `EXIT=0`, 0 must-fix, "startup … sound"_

### Phase 2: Docs

- [x] `CLAUDE.md` wake-up bullet: self-sends dropped at `Send`, the bootstrap request the sole exemption, the 2026-09-09 echo (edit)
- [x] `docs/architecture.md` notification list, `docs/hooks.md` delivery-without-a-listener paragraph, `docs/agent-bus.md` launch section (plan)

### Phase 3: Integration test

- [x] Hermetic section (in `scripts/test-codex-hooks.sh` or a sibling, scratch `BUS_SESSION`): seed a bootstrap and a self-addressed `response:startup` into a role's inbox; `muxcode inbox --peek` and `muxcode hook stop` surface the bootstrap alone; the reply's delivery status reads `responded`; negative control — an ordinary `request` self-send is dropped with the `[send]` log line and never reaches the inbox — _`scripts/test-startup-self-reply.sh` (own script, not a section): four sections, hermetic on a scratch session with a stub `codex` on PATH, floor pinned to the exact pass count_
- [ ] Live check: relaunch a codex agent on the hook road and confirm at most one `response:startup` in its history and no `loop-detected` row within two minutes
- [x] Run the section and record the counts in this spec — _run agent 16:07:53: **15 passed / 0 failed** (floor 15), exit 0. The 16:07:10 first run was 14/15 — its own "reply CC'd to edit" assertion counted the CLI bootstrap's auto-CC (`PreLaunchSetup` uses `SendNoCC`, a bare `muxcode send` does not); the assertion was narrowed to the reply, not the script's subject. Suite green 16:07:24, review 16:08:18 on graph `3fcb5ed9`_

## Notes

- Filed 2026-09-09 15:48 by plan on edit's request `1788982822`, after edit fixed it in the tree.
  Phase 1's code steps are ticked on the diff; the acceptance criteria wait on a fresh suite row
  and a review, since the test store's last green (15:14:31) predates the change and was truncated
  by the re-init anyway.
- Edit's `build-test-review` run `1788982872-d67cd45e` (15:41): build green (row 15:41:24), the
  test node **timed out at 297 s with no row in the test store** — the codex test agent never ran
  the suite ([MUX-153](../backlog/MUX-153-codex-test-agent-cannot-run-the-suite.md)'s shape) — and
  review never dispatched. The suite row Phase 1 waits on still does not exist.
- Related: [MUX-009](../backlog/MUX-009-response-echo-chain-retrigger.md) (the other response
  echo — a `type: response` typed as a prompt on the scrape road — closed at its root by the hook
  road; this one is the send-side sibling); [MUX-032](../backlog/MUX-032-loop-detector-granularity.md)
  (the detector's blind spot for one-role echoes); [MUX-153](../backlog/MUX-153-codex-test-agent-cannot-run-the-suite.md)
  (the same codex test agent); [MUX-168](../backlog/MUX-168-reinit-purges-active-spec-graph-run-survives.md)
  (found in the same relaunch — the re-init that also emptied the test store).

## Status

**In Progress** — 13/15. Filed 2026-09-09 15:48; Phase 1 complete 15:51 (suite green 15:49:59 on
run `d67cd45e`'s retried test node, review 15:51:44 `EXIT=0`), Phase 2 docs complete 15:45, Phase 3
script complete 16:07 (`scripts/test-startup-self-reply.sh` 15/0, suite green 16:07:24, review
16:08:18 `EXIT=0` on graph `3fcb5ed9` — 0 must-fix, its two should-fixes both MUX-163 P4 script
findings). Open: the **live relaunch check** and the acceptance criterion it evidences — both need a
codex agent restarted on the hook road, which is a user-approved reload. Nothing committed.
