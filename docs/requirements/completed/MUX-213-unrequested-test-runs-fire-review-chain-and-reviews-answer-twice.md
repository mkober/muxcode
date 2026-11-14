# MUX-213: Test Runs Nobody Requested Fire the Review Chain, and Every Review Is Answered Twice

**Tracking:** [mkober/muxcode#187](https://github.com/mkober/muxcode/issues/187)

The test agent runs `./test.sh` more often than anything asks it to, and every one of those runs
that passes fires the test→review chain. Review then reviews a tree that has not changed, or one a
worker is halfway through editing, and its reply goes out twice — once to the test agent that asked,
once to edit. On 2026-11-13 the daemon's own loop detector flagged the pattern twice. The cost is
wasted turns, findings and FAILED notices about a half-edited tree that read as authoritative, and
extra `verify-spec` passes for plan set off by a chain no graph node started.

## Context

### Source and standard of evidence

Filed 2026-11-13 on the user's request relayed by edit, from edit's evidence note
(`/tmp/defect-unrequested-test-review-cycles.md`). Plan checked the note against the session's
lifecycle log and the bus directory of session `muxcode`; the counts below cover 15:06 (the last
session re-init, which truncated the stores) to about 18:00. Rows marked *edit's account* were not
re-verified by plan. Code was **not** read for this spec: the root cause is undetermined and
establishing it is Phase 1.

### Observed — verified by plan

| Fact | Where |
|------|-------|
| 57 `./test.sh` rows in the test agent's history in about three hours | `test-history.jsonl` |
| 30 requests were addressed to the test agent in the same window: 8 from edit, 10 from build (the build→test chain), 12 from `daemon` (graph nodes and daemon sends) | `log.jsonl`, `"to":"test"` |
| So roughly 27 history rows have no request behind them. Some are seconds apart — `1794610411`, `1794610423`, `1794610442`, `1794610447` are four rows inside 36 s, though one suite run takes about a minute — so a row is not always a whole run. What each row is has not been established | `test-history.jsonl` |
| 24 `test → review` requests were sent in the window | `log.jsonl` |
| The daemon flagged it twice: `loop-detected test type=command` at 16:33:08, and `loop-detected test type=message` at 17:54:42 | `muxcode lifecycle show --event loop-detected` |
| **Each "duplicate" review reply is two messages to two recipients.** For request `1794610622-test-df8c7470` (`test → review`), review sent `review-complete` to `test` at `…645` and to `edit` at `…646`, both carrying that `reply_to`. The same pair exists for `1794607113`, `1794607188`, `1794610293`, `1794610337`, `1794610411` and `1794610442` | `log.jsonl` |
| A `verify-spec` reached plan at 17:24 while the graph's review node stood `failed` on a must-fix, and several more arrived naming only plan's own doc edits | plan's inbox; [MUX-172](../completed/MUX-172-plan-verify-gate-hardcodes-success.md) |

### Observed — edit's account, not re-verified

- The test agent starts a run with no request at all: after daemon timeout notices for synthetic
  stuck-agent tasks, after build chains while files were mid-edit, and spontaneously.
- Runs made mid-edit produce false FAILED notices — 16:21 (`writeCancelRequest` undefined) and 17:57
  (during the MUX-172 worker's edits) — and stale review findings that edit must triage.
- The detector's detail for the two rows: "`./test.sh` failed 3x in 1m44s" and
  "test <-> review action:review repeated 4x in 4m3s".
- Edit's memory note of 10:57 the same day: "review replies twice per request; unrequested test
  cycles observed".

### Cause — not determined

_Determined in Phase 1 — see [Findings for Phase 1](#findings-for-phase-1): the reply to the test
agent is the trigger, with three links required. This section is kept as filed._

Candidates, none verified:

- **The reply to the test agent is itself the trigger.** Review answers the test agent's request, so
  a `review-complete` response lands in the test agent's inbox. If the test agent treats that as
  work — re-running the suite to "confirm" — the run passes, the chain asks review again, and the
  loop is test ↔ review, which is what the 17:54 row names. Plan's own hypothesis, from the reply
  pairs above.
- **The second reply is auto-CC.** Messages from review to a non-edit agent are copied to edit by
  design; the pair may be one send and its copy rather than two sends. The two ids differ and are a
  second apart, which does not settle it.
- **The test agent's definition** tells it to run the suite on something other than a request.
- **A daemon re-drive** (idle wake, stall watchdog, synthetic task timeout) re-delivers an already
  answered test request.
- **Two history rows per run**, from a precheck or a second hook event, inflating the count without
  an extra run.

### Why it matters

- The test→review chain is deterministic and trusted: a passing run **is** the review request. An
  unrequested run therefore launders itself into a review nobody asked for, and a review into a
  `verify-spec`.
- A FAILED notice or a must-fix finding about a tree mid-edit is indistinguishable, to edit, from one
  about finished work.
- Each cycle occupies the test and review agents, so a graph node's real dispatch to either waits or
  is suppressed as a duplicate ([MUX-155](../backlog/MUX-155-send-dedup-keys-on-target-not-sender.md),
  [MUX-170](../backlog/MUX-170-graph-dispatch-adopts-foreign-in-flight-task.md)).

## Requirements

### Acceptance criteria

- [x] The cause of the unrequested runs is recorded here, with the evidence that distinguishes it
  from the other candidates — including what a `test-history.jsonl` row actually is —
  [Findings for Phase 1](#findings-for-phase-1)
- [x] The test agent starts a suite run only for a request — a graph node, the build chain, or edit.
  A response arriving in its inbox, a timeout notice or an idle wake starts none — ticked 2026-11-13
  on the live check recorded at the end of this item: a real Codex test agent, one completed turn
  per case. It is evidence of behaviour, not a guarantee — one trial each, Codex only, and a model
  can still disobey; the criterion two below ("an unrequested or re-driven run does not fire the
  test→review chain") is the mechanical backstop. History of this item: what is
  delivered — the review reply no longer reaches the test agent, its definition says request only,
  its Codex wake frames a responses-only inbox as no work, and a run it starts anyway fires no chain
  (next criterion). What is shown — section A, with a *stub* that honours "request only". What is
  not shown — that a **live** test agent refrains, and the timeout-notice and idle-wake cases, which
  no test exercises. Closes with a live check of each, or with the user narrowing the criterion to
  what muxcode can enforce.
  **Evidence added by the run's `criteria` node, 2026-11-13 19:40 (not yet ticked):** (1) the Codex
  test wake with **no request** in it — a reply, a timeout notice, or both — is now framed "none of
  them is a request. You run tests only for a request: run no tests and send no reply for these,
  then stop" (`codexTestNoRequestPreamble`, test role only; the condition is `Requests == 0` and
  base role `test`), with a unit test covering the notice, a mixed inbox, and edit and build as
  controls. (2) A live section, `scripts/test-unrequested-test-cycles.sh --live`, drives a real
  `codex exec` as the test agent: it ran the suite for a request (the control — the check can see a
  run) and started **no** run for a review reply, a no-answer notice, or an idle wake.
  **The first live result was unproven and is withdrawn** (run-agent task
  `1794615805-spawn-100fae85-c7737248`, "4 live passed"): the live cases ignored Codex's exit
  status, so a Codex that crashed, timed out or never finished its turn would have counted as "no
  suite run". The review caught it. A live case now counts only after a **completed turn** — exit
  0, not killed by the watchdog, a `turn.completed` event in `codex --json` and no `turn.failed` —
  and a new hermetic section F, with a stub Codex, proves the evaluation: a completed request turn
  and a completed no-op turn pass; a non-zero exit, a watchdog kill, exit 0 without `turn.completed`,
  and a `turn.failed` event each **fail** the case. Result of record (run-agent task
  `1794617144-spawn-100fae85-5e5e88eb`, proc `1794617148-proc-878d0b14`): **40 passed, 0 failed,
  floor 40; 4 live passed, 0 live failed; exit 0** — request: ran the suite; response, notice and
  idle: each completed its turn and ran nothing. An intermediate run of the corrected script (proc
  `1794617030-proc-a8f1f309`) failed 3 regular and 3 live checks while the completion check was
  still misreading real turns. Plan read all three proc logs and the preamble's condition. Limits:
  one trial per live case, on Codex only — a Claude test agent was not driven
- [x] One review request produces one review, and edit is told about it once. Whether the requester
  also receives a reply is decided and recorded, and that reply cannot start a new test run — the
  requester gets none (`reply_role`, decided by the loop's worker); section B of the Phase 3 script:
  one review, answering edit, held once, nothing reaching test
- [x] An unrequested or re-driven run does not fire the test→review chain — `TakeTestRunOrigin`,
  judged at PreToolUse; `TestOpenRequestFor`, `TestTakeTestRunOrigin_SurvivesReplyBeforeHook` (a
  rerun started after the reply is refused), `TestOpenRequestFor_StaleReceiptDoesNotVouch`
- [x] Negative control: a requested run still passes its result on — build→test→review fires exactly
  once per request, and a graph `test` node still completes on its reply — sections B (one request,
  one review request, the reply landing before the hook) and E (a graph test node completes on its
  reply and no chain escapes) of the Phase 3 script
- [x] `docs/hooks.md` and `docs/architecture.md` state what may start a test run and who receives a
  review reply — Phase 2

### Technical approach

Phase 1 is attribution, not a fix. For every `./test.sh` row in a bounded window, find the message
or event that preceded it: a request id, a response delivered to the test agent, a wake, or
nothing. That table decides between the candidates. The fix then goes where the cause is — the
agent definition, the chain condition, the reply's recipient list, or the re-drive — and nowhere
else.

### Key files

| File | Purpose |
|------|---------|
| `agents/test-runner.md`, `agents/code-reviewer.md` | What each is told to do on a response, and whom to reply to |
| `tools/muxcode/bus/hook.go`, `bus/profile.go` | The test→review chain and its conditions |
| `tools/muxcode/bus/inbox.go` | Auto-CC of review's replies to edit |
| `tools/muxcode/daemon/daemon.go` | Idle wake, stall and timeout re-drives |
| `tools/muxcode/bus/guard.go` | `DetectMessageLoop` — alert-only today |

## Implementation

### Phase 1: Attribute every run

- [x] For one window of at least an hour, list each `./test.sh` history row beside what preceded it
  in `log.jsonl` and the lifecycle log; record the table here — 69 minutes, 30 rows, matched to the
  test agent's Codex transcript as well
- [x] Establish what one history row is: a whole run, a precheck, or a second hook event — a whole
  run; the store also holds reply rows and the build agent's `go vet` rows
- [x] Establish whether the paired review replies are one send plus auto-CC, or two sends — two
  sends, with auto-CC as a third delivery
- [x] Reproduce on a scratch session: deliver a `review-complete` response to a stub test agent and
  observe whether a run starts — `/tmp/mux213-repro.sh`, 12 passed, 0 failed (run-agent task
  `1794613326-spawn-100fae85-bab68dbf`). The stub shows the response waking the agent, being
  presented as work, and a passing run then firing the chain from `reviewed`; it cannot show the
  model choosing to rerun, which the live transcript shows eight times
- [x] Record the cause and the candidates it rules out

### Findings for Phase 1

Recorded 2026-11-13 from the implement node of run `1794608375-55-defect-backlog-loop`, from the
worker's handoff `/tmp/mux213-phase1-findings.md`. The attribution, the transcript reading and the
reproduction are the worker's; plan spot-checked three of the code claims the cause rests on — the
test agent's rule (`agents/test-runner.md:11`), the chain guard and its comment (`cmd/hook.go:136-150`)
and the two reply instructions (`agents/code-reviewer.md:156`, `config/muxcode.json:180`) — and each
reads as stated. The test and review agents were on **Codex, hook road**.

#### Attribution — 17:22 to 18:31 (69 min), 30 `./test.sh` rows

Each row was matched to the test agent's turn in its Codex transcript
(`~/.codex/sessions/2026/11/13/rollout-2026-11-13T15-29-36-01a1d699-…jsonl`) and to the prompt that
started the turn.

| # | Row | Turn started by | Chain |
|---|-----|-----------------|-------|
| 1 | 17:23:39 | Graph request, daemon 17:22:37 (the turn also carried two stale `review-complete` responses, 16:58 and 17:00) | Graph-owned, none |
| 2–3 | 17:23:44, 17:23:50 | Same turn — the agent reran (cached) | None |
| 4–9 | 17:26:33, 17:30:38, 17:33:48, 17:35:45, 17:39:58, 17:44:44 | Graph requests 17:25:33 … 17:43:47 | Graph-owned, none |
| 10 | 17:51:33 | build→test request 17:50:39 | test→review `1794610293` |
| 11 | 17:51:45 | edit→test request 17:50:49 (same turn) | Deduped (30 s) |
| 12 | 17:52:17 | **`review-complete` response `1794610331` only** (turn 17:52:12; the agent: "I'm rerunning the full suite for this final review update") | **test→review `1794610337`** |
| 13 | 17:53:31 | edit→test request 17:52:37 | test→review `1794610411` |
| 14 | 17:53:43 | Same turn — rerun | Deduped |
| 15 | 17:54:02 | **Responses `1794610360` + `1794610436` only** (turn 17:53:56) | **test→review `1794610442`** (state was `reviewed`) |
| 16 | 17:54:07 | Same turn — rerun | Deduped |
| 17 | 17:57:02 | **Response `1794610565` only** (turn 17:56:05) | **test→review `1794610622`** (state was `reviewed`) |
| 18 | 17:57:32 | **Response `1794610645` only** (turn 17:57:26) | **Exit 1 mid-edit → false "Tests FAILED" to edit** |
| 19 | 18:00:03 | Graph request 17:59:06 | Graph-owned |
| 20 | 18:05:52 | Graph request 18:04:51 (a real failure) | Graph-owned |
| 21 | 18:07:15 | edit→test request 18:07:08 | test→review `1794611235` |
| 22 | 18:07:58 | **Response `1794611269` only** (turn 18:07:49) | **test→review `1794611278`** |
| 23 | 18:08:26 | **Response `1794611300` only** (turn 18:08:20) | Deduped (28 s) |
| 24 | 18:20:54 | Graph request 18:20:45 | Graph-owned |
| 25 | 18:22:45 | build→test request 18:21:50 | test→review `1794612165` |
| 26 | 18:22:53 | edit→test request 18:22:02 (same turn) | Deduped |
| 27 | 18:23:36 | **Response `1794612210` only** (turn 18:23:30) | **test→review `1794612216`** |
| 28 | 18:24:57 | **Response `1794612243` only** (turn 18:24:03) | Suppressed: the graph owns test |
| 29 | 18:25:12 | Graph request 18:24:09, delivered mid-turn 18:25:05 | Graph-owned |
| 30 | 18:30:43 | Graph request 18:30:35 | Graph-owned |

Eighteen rows answer a request (12 graph, 6 build chain or edit) and four are same-turn reruns.
**Eight were started by a turn whose only prompt was a `review-complete` response** — rows 12, 15,
17, 18, 22, 23, 27 and 28. Of the nine chain-fired test→review requests in the window, **five came
from response-started runs**, and a sixth response-started run (row 18) sent edit a false FAILED
notice. Every such turn began with the wake sentence "You have new messages" 0–1 s after review's
send, its prompt-submit context holding only `review-complete` responses under "Handle every request
below now".

The loops: 17:51:33 → 17:52:17 → 17:54:02 → 17:57:02 → 17:57:32 (a failure ends it);
18:07:15 → 18:07:58 → 18:08:26 (dedup ends it); 18:22:45 → 18:23:36 → 18:24:57 (graph ownership
ends it). Nothing in muxcode ends it by design.

#### What a history row is

- A `./test.sh` row is **one whole run**: every row's output ends in `=== Summary ===`. Rows seconds
  apart are Go test-cache reruns (`(cached)` per package, about 5 s), not fragments. **This spec's
  Context is wrong** where it says a row is not always a whole run.
- `test-history.jsonl` is not "the test agent's runs". The file is chosen by **command class**, not
  by the agent that ran the command (`bus/hook.go:1159-1214`). Of 100 rows: 54 `./test.sh`, 22
  `bus-response` reply rows (the test agent's own replies), 3 `go test`, and **21 `go vet ./...`
  rows run by the build agent** at the repo root inside its build node.
- **A second, independent source of false FAILED notices.** That vet fails every time (`pattern
  ./...: directory prefix . does not contain main module` — the repository root is not a Go module)
  and, as a failing test precheck, fires the test chain's failure edge from build: **20 "Tests
  FAILED (exit 1): go vet ./... — check test window" notices to edit**. Out of this spec's scope
  unless Phase 2 takes it; a candidate for its own backlog entry.

#### The paired review replies: two sends, plus auto-CC

- For each chain review, review sends **two distinct messages** — `review→test` (the requester) and
  `review→edit` — with different ids and different payload text (for example `1794610331` and
  `1794610332`). Auto-CC copies a payload verbatim and keeps `To=test`, so the pair is not a send and
  its copy.
- Why two: `agents/code-reviewer.md` says to reply to the requester and (`:156`) "Do NOT send a
  separate notify to edit — the bus auto-CC's your response"; the chain's request text
  (`config/muxcode.json:180`) says "review the changes and **report results to edit**". The Codex
  reviewer obeys both.
- Auto-CC fires as well: the receipt for `review→test 1794610331` is `acked_by: edit`. So per chain
  review the test agent gets one message and **edit gets two** — the CC of the reply to test, and
  review's own send.
- Graph-dispatched reviews (`daemon→review`) reply to edit only: no pair, no loop.

#### Scratch reproduction

`/tmp/mux213-repro.sh` (throwaway), run by the run agent: **12 passed, 0 failed, exit 0**, task
`1794613326-spawn-100fae85-bab68dbf`. A first attempt (`1794613300`) failed 3 checks because the
scratch role was not on the Codex hook road, which made two sections vacuous; fixed by activating
it with `muxcode agent config test` and asserting the marker.

1. A `review→test review-complete` response lands in the test inbox, `muxcode send` writes
   `trigger-test.notify` for it (`cmd/send.go:241` notifies the recipient whatever the message
   type), and auto-CC puts a `to=test` copy in edit's inbox.
2. Negative control: `hook stop`, the request-gated road, does not block for a response-only inbox
   and leaves it unconsumed; it does deliver once a request is pending, the response riding along.
3. The wake sentence through `hook prompt-submit` returns context opening "Handle every request
   below now" that carries the response, and consumes the inbox —
   `ConsumeInboxForHook(requireRequest=false)`, `bus/hook_codex.go:298-306`.
4. With the workflow seeded at `reviewed`, a passing `./test.sh` PostToolUse from test logs
   `testing from=reviewed trigger=hook:bash:test` and fires a test→review request.

The stub cannot show the model deciding to rerun; the live transcript shows it eight times.

#### Cause — three links, all required

1. **Delivery.** `muxcode send` wakes the recipient for a response (`cmd/send.go:241`). For a Codex
   test agent the wake is the sentence, and prompt-submit renders the response-only inbox under
   "Handle every request below now".
2. **Definition.** `agents/test-runner.md:11` — "When you receive ANY message, do this exact
   sequence: 1. Run tests". A `review-complete` response is a message, so the agent reruns the
   suite: cached, about 5 s, passing.
3. **The chain guard is defeated by ordering.** `triggerChain`'s workflow guard (`cmd/hook.go:143`)
   exists for exactly this loop — its comment says it "breaks the test→review→test loop where review
   completion made the test agent re-run tests". But `ProcessBashHook` moves the workflow to
   `testing` (`bus/hook.go:1131`) **before** `triggerChain` reads the state, so a test success never
   sees `reviewing` or `reviewed`. The lifecycle log shows `testing from=reviewed` and then
   `reviewing from=testing trigger=chain:test:success` in the same second at 17:54:02 and 17:57:02.
   The only effective brakes were the 30 s send-dedup window (rows 11, 14, 16 and 23 deduped at
   5–28 s; rows 12, 15, 17, 22 and 27 fired at 31–51 s) and graph ownership.

| Candidate | Verdict |
|-----------|---------|
| The reply to the test agent is itself the trigger | **Confirmed** — eight response-only turns, in the transcript and the reproduction |
| The second reply is auto-CC | **Ruled out** as the explanation of the pair — two sends with different ids and text; auto-CC is a *third* delivery on top |
| The test agent's definition | **Confirmed as a contributing link** — "ANY message" → run tests |
| A daemon re-drive | **Ruled out** — no wake, notify or redrive lifecycle row for test in these turns; each wake is `muxcode send`'s own notify 0–1 s after review's send |
| Two history rows per run | **Ruled out** — each row is a whole run; but the store mixes in 22 reply rows and 21 build-agent vet rows |

**Relation to the backlog.** This is [MUX-009](./MUX-009-response-echo-chain-retrigger.md)'s
mechanism — a response echo re-triggering the chain — seen from the Codex hook road, and
[MUX-127](../backlog/MUX-127-review-completion-routing.md)'s "loops on success" junction.
[MUX-175](../backlog/MUX-175-response-ack-ping-pong-has-no-brake.md) is not involved: no
acknowledgements were exchanged.

**Candidate fix points for Phase 2**, one per link — the choice among them, and the requester-reply
decision, are the worker's per the user and are recorded in Phase 2, not here: (a)
`agents/test-runner.md` — run tests on a *request* only, never on a response; (b) the Codex wake and
delivery road — do not wake for, or do not present as work, a response-only inbox (Claude's
listener road may need the same check); (c) `triggerChain` — evaluate the guard against the state
*before* `hook bash`'s transition; (d) one notice to edit — drop "report results to edit" from the
chain text, since auto-CC already delivers it.

### Phase 2: Fix at the cause

- [x] Stop the unrequested run where Phase 1 placed it, with a test that fails without the change —
  all three links; `TestCodexPromptSubmitContext_TestResponsesOnlyIsNotWork`,
  `TestNewChainRequest_TestChainRepliesToEdit`
- [x] One review, one notice to edit; record the decision on the requester's reply — `reply_role`;
  the decision is recorded below
- [x] The chain does not fire for a run with no request behind it — `TakeTestRunOrigin`, with the
  provenance captured at PreToolUse after the review's must-fix
- [x] Update `docs/hooks.md` and `docs/architecture.md`

### Findings and decisions for Phase 2

Recorded 2026-11-13 from the implement node of run `1794608375-55-defect-backlog-loop`, from the
worker's handoff `/tmp/mux213-phase2-docs.md`. Plan read `bus/chain_request.go`, `ReplyTarget` in
`bus/message.go`, the new check in `cmd/hook.go` and the two Codex preambles in `bus/hook_codex.go`;
they match what is recorded here. The tests are listed as the worker reported them.

#### Decision — the requester's reply (third acceptance criterion)

**The requester of a test-chain review — the test agent — receives no reply. Edit receives it,
once.** The user left this point to the loop's worker; this is the worker's decision, not the
user's.

| | |
|---|---|
| Mechanism | A new message field, `reply_role` (`Message.ReplyRole`, `bus/message.go`). `ReplyTarget(m)` returns it when set, otherwise the sender, normalized; `FormatMessage`'s "To reply" line uses it. `bus.NewChainRequest` (`bus/chain_request.go`) sets `ReplyRole = "edit"` on a **test-chain request only** — build→test, deploy and run requests still answer their sender. Both chain roads build through it (`triggerChain` in `cmd/hook.go`, `muxcode chain` in `cmd/chain.go`), and the LLM harness honours the field |
| Why | The hook sends the review request on the test agent's behalf, and the test agent has already answered its own requester, so nothing in it waits for the review. Delivering the review to it is what started the reruns (Phase 1). A reply to edit carrying `--reply-to <chain request id>` keeps correlation, tells edit exactly once (it is `to=edit`, so no auto-CC copy), and keeps the daemon's reviewed transition and `verify-spec` gate working — that gate reads only review replies addressed to edit |
| Rejected | Keep replying to test and drop "report results to edit" from the chain text: review replies would stop reaching edit as `to=edit`, which would silently stop `verify-spec` for chain reviews |
| Rejected | Stop auto-CC for review: edit would still receive review's explicit second send |

#### What changed, per step

| Step | Change |
|------|--------|
| Stop the unrequested run — all three links | **Delivery:** for the **test role only**, `CodexPromptSubmitContext` frames a wake whose consumed messages are **all responses** with `codexTestResponsesPreamble` ("responses only, no request among them. You run tests only for a request: run no tests and send no reply for these, then stop") in place of "Handle every request below now". Every other role, and any inbox holding an event or a request, keeps the unchanged request preamble (corrected after review — see below). **Definition:** `agents/test-runner.md` — run tests once, on a `Type: request`; a response is information, so no run, no reply, and no rerun after replying. `agents/code-reviewer.md` — the requester is the role the "To reply" line names, and there is exactly one reply. **And** with `reply_role` the test agent no longer receives review replies at all |
| One review, one notice to edit | `reply_role`, above |
| No chain for a run with no request behind it | For a test-chain **success**, `triggerChain` now requires that a request stood behind the run **when it started** (`bus.TakeTestRunOrigin`, corrected after review — see below). The underlying question is `bus.OpenRequestFor(session, role)`: a request addressed to the role's window that it has received (a receipt — acked or delivered), not marked responded, received within two hours (`openRequestMaxAge`). Self-sends, the startup bootstrap and received responses do not count. None → no chain, and a `chain-withheld` lifecycle row (source `hook`, info): `test chain not fired for <role> — no unanswered request stands behind this run: <command>`, scrubbed and clipped to 160 runes. With delivery acknowledgement off the check fails open. A **failing** run still notifies edit. The check sits after the workflow guard and the graph-ownership suppression |
| Tests | `bus/chain_request_test.go`: `TestNewChainRequest_TestChainRepliesToEdit` (build chain and a test-chain event unchanged as negative controls), `TestReplyTarget_NormalizesBothRoads`, `TestOpenRequestFor`, `TestOpenRequestFor_StaleReceiptDoesNotVouch`, `TestOpenRequestFor_FailsOpenWithoutReceipts`, `TestCodexPromptSubmitContext_ResponsesOnlyIsNotWork`; harness `TestReplyTarget_HonoursReplyRole`. `scripts/test-xcode-compat.sh`: its two "test row and one review request" cases now hand the test agent a received request first, since a bare payload fed to `hook bash` has no request behind it; floor unchanged |

#### Corrected after review

The first Phase 2 implementation drew two must-fix findings (`/tmp/muxcode-review-1794614352.txt`).
Both were flaws in what this section first recorded; the rows above describe the corrected code,
which plan read (`TakeTestRunOrigin`, `codexTestResponsesPreamble` and its condition, the
`RecordRunProvenance` call in `cmd/hook.go`).

| Must-fix | What was wrong | What it is now |
|----------|----------------|----------------|
| The open-request check raced the agent's own reply | It ran at **completion**. Claude's `hook bash` is asynchronous (`config/settings.json`, PostToolUse Bash `async: true`): a requested suite finishes, the agent sends its reply — which marks the request responded — and only then the delayed hook asked `OpenRequestFor`, found nothing open and withheld a legitimate review. Nothing retried it | **Provenance is captured at invocation.** The synchronous PreToolUse `hook guard` calls `bus.RecordRunProvenance(session, role, ev)` for a test command (`ClassifyCommand == CmdTest`) that carries a `tool_use_id`, writing `<bus>/run-provenance/<sanitized tool_use_id>.json` (`request`, `open`, `ts`) from `OpenRequestFor` at that moment; records older than two hours are swept on write. `triggerChain`, now passed the `tool_use_id`, calls `bus.TakeTestRunOrigin`, which consumes the record and returns its answer. With no record — no guard hook, or no id — it falls back to asking `OpenRequestFor` at completion, which still refuses an unrequested run but can lose that race. Both Claude and the Codex hook road send a `tool_use_id` on PreToolUse and run the guard synchronously |
| The "no work" wake framing reached too far | `Requests == 0` was read as "responses only", so an event-only or response-plus-event inbox (the daemon's `proc-complete`, analyze events, agent-down alerts) was consumed under a "run no command, stop" instruction — and it applied to **every** Codex role, including edit, which must carry on delegated work when its result arrives. A sentence added to the shared request preamble ("a response is never a reason to run a command") had the same reach | `HookDelivery` counts responses, and the no-work framing applies only when **every** consumed message is a response **and** the role's base role is test (`SpawnBaseRole`). Everything else keeps the request preamble, and the extra sentence is removed |

Tests added with the corrections: `TestTakeTestRunOrigin_SurvivesReplyBeforeHook` (a requested run
whose reply lands before the asynchronous hook is still credited; a rerun started after the reply
is not; the record is consumed), `TestRecordRunProvenance_OnlyTestCommandsWithAnID`,
`TestTakeTestRunOrigin_FallsBackWithoutRecord`, and
`TestCodexPromptSubmitContext_TestResponsesOnlyIsNotWork` (test with responses only → the no-work
framing; test with an event only, with a response and an event, with a response and a request, and
edit with a delegated result → all keep the request preamble). The earlier
`TestCodexPromptSubmitContext_ResponsesOnlyIsNotWork` named above is superseded by the last of
these.

**A second correction, from the Phase 3 review.** The start-time record now also names the graph
run and node that owned the test agent when the command started: `RecordRunProvenance` asks
`GraphOwnsRunningSendNode` and stores `graph_run` / `graph_node` beside the request answer, and
`TakeTestRunOrigin` — which replaced the first version's `RequestBehindRun`, and whose name this
section now uses throughout — returns a `TestRunOrigin{Requested, GraphRun, GraphNode}`. The reason
is the same ordering problem one layer up: a graph run can **complete** before the asynchronous hook
fires, and the live ownership check then no longer sees it, so a graph-owned test run would have
fired an independent test→review chain beside the graph's own review node. With the record,
`triggerChain` suppresses it and the `chain-suppressed` row names the owning run and node. Plan read
`RecordRunProvenance` and `TakeTestRunOrigin`; section E of the script exercises it.

**A third correction, from the `criteria` node.** The test role's no-work framing was widened again,
in the one direction the first review left open: from "every consumed message is a response" to
"no consumed message is a request" (`codexTestNoRequestPreamble`, `Requests == 0` and base role
`test`), so a timeout notice — an event — is covered too. It is still the test role's alone; an
event reaching edit, build or any other role keeps the request preamble. Where this section says
`codexTestResponsesPreamble`, that is the name it had between the two changes.

#### What was deliberately not changed

- **The guard's ordering.** Phase 1's third link — `ProcessBashHook` moving the workflow to
  `testing` before `triggerChain` reads the state — is left as it is, and the guard's doc comment was
  corrected to say it cannot stop a test success. Evaluating the guard against the pre-run state
  would also suppress the build→test chain for any build edit requests after a review: a behaviour
  change outside this defect. The open-request check does the job instead.

#### Consequences worth knowing

- **A test run started without a bus request no longer requests a review.** That is the defect's
  fix, and it also covers a run a person starts by typing into the test agent's pane: it passes,
  logs `chain-withheld`, and review is not asked. Send the request over the bus
  (`muxcode send test test …`) to get the chain.
- **A request answered without `--reply-to` vouches for later runs for up to two hours.** It is
  never marked responded, so until the bound expires a run it did not start can still fire the
  chain. The two-hour bound is the worker's choice.
- **The build agent's `go vet` false alarms are untouched** (Findings for Phase 1): they fire the
  test chain's *failure* edge, which this change deliberately leaves alone.

### Phase 3: Integration test

- [x] Create `scripts/test-unrequested-test-cycles.sh` — hermetic: scratch `BUS_SESSION`, scratch
  daemon, stub test and review agents
- [x] A `review-complete` response delivered to the test agent starts no run and sends review nothing
- [x] One `test` request → one run, one `review` request, one review, one notice to edit
- [x] Negative control: with the fix reverted in the stub's configuration the loop reproduces, so
  the script proves it can see the defect
- [x] Coverage floor; run the script through the run agent and record the counts here

Run through the run agent on 2026-11-13: `scripts/test-unrequested-test-cycles.sh`, log
`/tmp/test-unrequested-test-cycles.log`. First run 19:11 (task
`1794615071-spawn-100fae85-4174a436`), sections A–D: 24 passed, 0 failed, floor 24. The review then
asked for a graph test node; with section E added (task `1794615383-spawn-100fae85-2b7f8f24`):
34 passed, 0 failed, floor 34. With section F and the `--live` section added by the `criteria` node
(task `1794617144-spawn-100fae85-5e5e88eb`): **40 passed, 0 failed, floor 40; 4 live passed, 0 live
failed; exit 0.** Sections F and live are described beside the first open acceptance criterion.

| Section | Setup | What it shows |
|---------|-------|---------------|
| A | A `review-complete` reply delivered to the test agent | A request-only test agent starts no run for it. A legacy stub that *does* run for it — as the Codex agent did — sends review nothing, logs `chain-withheld` naming the missing request, and review's inbox stays empty |
| B | One test request, fixed stubs, a scratch daemon | One run, one review request (the reply landing before the asynchronous hook did not withhold it), naming edit as its reply target; one review, answering edit; nothing from review reaches test; edit holds the review exactly once; no `chain-withheld` row; the daemon credits one review completion |
| C | Negative control — legacy stubs and delivery acknowledgement off, so the gate fails open | The loop reproduces: 4 runs and 4 review requests for one test request, each review answering test and starting the next run, and edit holding 8 review messages — the auto-CC duplicate |
| D | Legacy stubs with delivery acknowledgement on | The gate alone holds: one review request although the test agent runs on every message; the stub still re-ran (2 runs) and each unrequested rerun logged `chain-withheld` |
| E | A graph run with one `test` node, a scratch daemon, and the asynchronous hook held until the run had completed | The graph dispatches its test node; the node completes on the stub's `EXIT=0` reply and the run completes; one run for the node; **no test→review chain escapes** although the hook landed after the node completed, when the live ownership check could no longer see the run; `chain-suppressed` names the run and node that owned the test agent as the command started — graph ownership, not the request gate, held it |

Section E was added after the first run, when the review's should-fix (and plan's note here) said
no section drove a graph test node. It closes the second half of the fifth acceptance criterion.

## Related

- [MUX-127](../backlog/MUX-127-review-completion-routing.md) — review completion routes nowhere on failure
  and loops on success; the same junction
- [MUX-009](./MUX-009-response-echo-chain-retrigger.md) — a response echo re-triggers the chain;
  possibly the same mechanism from the other side
- [MUX-175](../backlog/MUX-175-response-ack-ping-pong-has-no-brake.md) — two agents acknowledging each other
  with no brake
- [MUX-172](../completed/MUX-172-plan-verify-gate-hardcodes-success.md) — why each of these chains also
  reaches plan
- [MUX-032](../backlog/MUX-032-loop-detector-granularity.md) — the detector that saw this and could not act

## Status

Complete

Closed out 2026-11-13 by `55-defect-backlog-loop` run `1794608375`, which activated it the same day
(Defects rank 1). All three phases and six acceptance criteria are checked.

- **Cause** — the review's reply to the test agent restarted the suite, through three links
  recorded under [Findings for Phase 1](#findings-for-phase-1). It is MUX-009's mechanism at
  MUX-127's junction; whether either of those specs is now satisfied was not assessed here.
- **Fix** — a test-chain review answers edit, not the test agent (`reply_role`); a passing test run
  fires the chain only when a request stood behind it as it started; a Codex test agent's wake with
  no request in it is framed as no work. See
  [Findings and decisions for Phase 2](#findings-and-decisions-for-phase-2).
- **Decided by the run's worker, not the user** — the requester receives no review reply (the user
  left that point to the worker), and the two-hour bound on how long a received request can vouch
  for a run.
- **Corrected in review three times**, each an ordering problem with the asynchronous hook or a
  framing that reached too far; and the first live result for the first acceptance criterion was
  withdrawn as unproven before the one of record.
- **Limits that remain** — the live check is one trial per case, on Codex only; with no start-time
  record the chain check falls back to completion time and can lose the race with a fast reply; a
  test run started without a bus request, including one a person types into the test pane, no
  longer requests a review.
- **Not fixed here** — the build agent's root `go vet` false alarms
  ([MUX-214](../completed/MUX-214-root-go-vet-false-alarm-from-the-build-agent.md)) and the
  unnameable `./test.sh` failures
  ([MUX-215](../backlog/MUX-215-non-reproducible-test-sh-failures-and-lost-failing-output.md)).

As filed, 2026-11-13, on the user's request relayed by edit: the counts and the paired replies were
verified by plan against the session's stores, the unrequested starts were edit's account, and the
cause was not determined.

**Unparked 2026-11-13 18:15, on the user's decision relayed by edit.** Plan had filed it parked
minutes earlier under the user's same-day rule, because one design point is unrecorded: whether the
requester also receives the review reply. The user's decision: unpark it, rank it first among the
eligible defects (Defects rank 1, tier 0) so the running `55-defect-backlog-loop` takes it right
after MUX-172, and **leave that design point to the loop's worker**. The worker records its choice
and the reason in this spec; the third acceptance criterion is where it lands.
