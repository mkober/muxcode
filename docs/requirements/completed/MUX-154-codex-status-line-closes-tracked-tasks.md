# A Codex Status Line Closes Tracked Tasks as Their Answer

**Tracking:** [mkober/muxcode#75](https://github.com/mkober/muxcode/issues/75) — PR [#73](https://github.com/mkober/muxcode/pull/73) carries Phases 1–3 but does **not** close it (Phase 4 open)

Five times on 2026-09-08 a `type: response` row whose entire payload was the Codex TUI's progress
line — `• Working (2m 08s • esc to interrupt)` — completed a tracked task. Each carried a `reply_to`,
so it correlated to a real request; `MarkResponded` drained that request from the inbox; the daemon
recorded the task as succeeded. The real answer, when it came, had nothing left to correlate to.

Two of those "successes" fired the `verify-spec` requests plan handled this afternoon — **1m45s before
the review agent's genuine reply** said two findings were unresolved.

It converts *nothing happened* into *done*, and it disarms the recovery path with the same stroke.

## Context

### Observed (2026-09-08; every row read from `log.jsonl` by plan, not relayed)

| Time | From → to | Payload | `reply_to` |
|------|-----------|---------|------------|
| 13:53:07 | build → edit | `• Working (13s • esc to interrupt)` | `1788889973-edit-…` |
| 13:55:02 | build → edit | `• Working (2m 08s • esc to interrupt)` | `1788890087-edit-…` |
| 14:00:02 | test → edit | `• Working (14s • esc to interrupt)` | `1788890388-edit-…` |
| 14:12:55 | review → test | `• Working (13s • esc to interrupt)` | `1788891160-test-…` |
| 14:13:25 | review → test | `• Working (42s • esc to interrupt)` | `1788891191-test-…` |

The lifecycle log then reads, to the second:

```
14:12:55  task-detected   review task review from test: succeeded
14:13:25  task-detected   review task review from test: succeeded
14:13:42  plan-verify     docs/requirements/drafts/MUX-144-…
14:14:37  plan-verify-suppressed  nothing moved since last verify-spec
```

The reviewer's genuine reply is the row at **14:14:40**: *"P1 caller-controlled gate authority and
P2 selection-pinned flat-view scrolling remain unresolved"*. The daemon had judged the review a
success — twice — and dispatched the verifier on it before the reviewer had said anything.

### Consequences, each observed

| | |
|---|---|
| `muxcode tasks` | "No in-flight tasks" — the real result has nothing to correlate to, so the daemon never wakes the sender with it |
| Edit's `--wait` | returned the status line as the answer, three times |
| `muxcode deliver build --force` | "nothing pending" — `MarkResponded`'s `ConsumeByID` had drained the request, so the recovery path is disarmed by the bug it would recover from |
| A build that never ran | read as done (the codex build agent produced no result all session) |
| A chain link and `verify-spec` | fired on a non-result |

### Mechanism — verified

`CodexProvider.DetectTaskCompletion` (`bus/provider_codex.go:404-470`) reads the pane and decides in
order:

1. **Active signals** → still running: braille spinners `⠋ ⠙ ⠹ ⠸`, `▸`, or the word `thinking`
2. A `Sent … to …` line in the last ten → the agent replied; done
3. A `›` or `>` prompt in the last three lines → done, **summary = the last content line**

Codex's current TUI renders progress as `• Working (13s • esc to interrupt)` — a bullet, not a
braille spinner, no "thinking" — and keeps its `›` composer visible while working. So step 1 passes
it, step 3 fires, and the summary is the progress line itself. The synthesized response is then
`Send()`-ed with the request's id as `reply_to`, which calls `MarkResponded` (drains the request)
and lets `checkTrackedTasks` (`daemon/daemon.go:2667`, completion at `:2712`) close the task — with
**no provenance check** on either road.

The signature is already known — on two *other* roads:

| Road | Where | What it does with `esc to interrupt` |
|------|-------|--------------------------------------|
| Console history | `bus/history_provenance.go:57`, `LooksLikeNonResult` (`:85`) | keeps a synthesized row from rendering as a pass — [`MUX-003`](../completed/MUX-003-echo-as-result.md) |
| Claude pane classifier | `bus/provider_claude.go:178` | treats it as a **working** signature |
| Codex completion | `bus/provider_codex.go:413` | **does not know it** |

A control verified on one road is not verified on all of them — the MUX-142 lesson, again.

### Second incident — the separator line, and the graph road (2026-09-08 20:31–20:32)

Read by plan from `log.jsonl`, the run store (`graphs/1788913399-spec-to-pr-83bd9ed6/`) and the
lifecycle log while the run was still held at `test`.

| Time | From → to | `reply_to` | Payload |
|------|-----------|-----------|---------|
| 20:31:51 | build → daemon | `1788913878-daemon-158b9375` — graph node `build` | 158 × `─`: the horizontal rule codex draws between turns, and nothing else |
| 20:32:49 | test → daemon | `1788913947-daemon-e4ffe0c3` — graph node `test` | the same rule line |

Both are step 3 above: the `›` composer was visible, and the nearest non-empty line above it in
codex's layout is the rule drawn under the previous turn. The daemon logged
`task-detected build task build from daemon: succeeded` at 20:31:32, :38, :45 and :51 — four
detections in twenty seconds while the build agent was still running `gofmt` and `./build.sh` — and
only the last produced a message (why the first three wrote nothing is **not established**; no
`chrome-send-dropped` row exists before 20:35:42). Test: 20:32:38, :43, :49.

What the graph did with it: `deriveSendOutcome` (`graph_exec.go:1277`) found no `error` action, no
authoritative history row (codex writes none) and no `EXIT=` sentinel in a payload of dashes —
`OutcomeUnknown`. Lifecycle: `20:31:51 graph-node-done build -> unknown`, `graph-unverified-hold
build`; the user approved by hand at 20:32:25 (`graph-gate-approved … "build" approved by user`,
released 20:32:27); `test` was dispatched at 20:32:27 and held at 20:32:49 the same way.
**`c4997ed`'s sentinel fallback cannot help here: each node was closed by a synthesized response 33 s
and 22 s after dispatch, before the agent had a result to report.** The genuine replies came later
and correlated to nothing — build's went to edit at 20:31:47 with no `reply_to` and no `EXIT=` at all
(against `agents/code-builder.md:72`); test's `EXIT=0` went to review at 20:33:08. Third spec-to-pr
run held this way today: `1788879161` (11:03 test, 11:05 review), `1788899620` (16:48 build, 16:49
test), `1788913399` (20:31 build, 20:32 test).

Why `6b53863` (20:13, live — the daemon reports `v0.1.0-53-gfb9d2fb`) did not catch it:
`LooksLikeProviderChrome` (`history_provenance.go:99`) requires every line to open with one of
`renderPrefixes` (`:70` — `• └ ⎿ ✻ ✽ ⏵`) and then carry a status signature or end in `…`. A rule
line opens with `─` and carries neither, so `dropsAsProviderChrome` (`inbox.go:172`) passed it as
composed text. The guard is structural on purpose — a real reply that quotes a status line must
survive — so the rule line is a **third signature** it needs, beside the bullet-`Working` line.

### Open signatures — recorded, deliberately not fixed (2026-09-08 22:10)

Three distinct shapes reached edit's inbox as "results" within thirty minutes of the second incident,
beyond the bullet-`Working` line. Reported by edit with `bae22dc`; recorded here so they are not
re-derived.

| Payload | Caught by `bae22dc`? | Why not |
|---------|----------------------|---------|
| `──────` (158 ×) | **yes** | — |
| `└ go test ./...` | no | `isProviderChromeLine` needs a status signature or a trailing `…`; `./...` is three ASCII dots, not U+2026 |
| `…` (bare) | no | no render prefix, so `hasRenderPrefix` never engages |

Widening the tool-echo prefixes would change `LooksLikeNonResult`, and with it MUX-003's
console-history road — so these stay open signatures rather than a silent omission. They are Phase 4
fixtures first; whether to accept them is a decision for that phase.

### Relationship

| Spec | Relationship |
|------|--------------|
| [`MUX-003`](../completed/MUX-003-echo-as-result.md) | Closed the console-history road: a synthesized row never renders as a pass. Did not touch task completion. Its guard (`LooksLikeNonResult`) is the one to reuse, not re-derive |
| [`MUX-148`](../completed/MUX-148-node-outcome-reads-command-ran-as-task-done.md) | Graph-executor half of the same family — a node outcome reads "a command ran" as done. That one is about authoritative-row provenance in `graph_exec`; this is the tracked-task store. Same defect shape, different consumer |
| [`MUX-009`](./MUX-009-response-echo-chain-retrigger.md) | A *response* injected back as a *prompt* on the receiving side. This is a status line synthesized as a *response* on the sending side. Distinct mechanisms, both "the bus believes a TUI" |
| [`MUX-127`](./MUX-127-review-completion-routing.md) | Routes the chain on review outcomes; this corrupts the outcome it routes on |
| [`MUX-153`](./MUX-153-codex-test-agent-cannot-run-the-suite.md) | Why the codex test agent has no real answer to give — this defect is what turns that silence into "done" |
| [`MUX-159`](../completed/MUX-159-codex-hooks-provider.md) | The structural fix: Codex ships `PostToolUse`/`Stop`/`UserPromptSubmit` hooks (verified 2026-09-08 on 0.153.4), so a hook-enabled codex agent is never scraped — this spec patches the scrape, that one removes its reason to exist for codex |

## Requirements

### Acceptance criteria

- [x] A response synthesized from a pane never completes a tracked task unless the pane shows a
      genuine completion — a `Sent … to …` line or a real result line — and never a progress line
- [x] `DetectTaskCompletion` recognizes Codex's `• Working (… esc to interrupt)` line as an **active**
      signal, using the **same** signature definition `history_provenance.go` already holds — one
      definition, not a third copy
- [x] A progress-line payload never drains the request from the inbox — `MarkResponded` /
      `ConsumeByID` are not reached — so `deliver --force` still has something to deliver
- [x] A chain link, `verify-spec`, or graph-node completion never fires on a synthesized non-result
- [x] Negative control: a genuine codex reply (`Sent response…` in the pane) still completes the task
      and fires the chain exactly as today — detection half `TestDetectTaskCompletionGenuineSendCompletes`;
      daemon completion and drain `assertCompletedAndDrained` (2026-09-25, Phase 1 lap); **chain fire
      pinned 2026-09-25, Phase 3 lap**: `TestCheckNonHookTasks_ReviewChainFiresOnlyOnGenuineReply` — a
      codex review agent's progress-line pane fires **0** `verify-spec` and leaves the workflow unreviewed;
      the genuine `Sent response…` pane fires **exactly 1** and transitions to `StateReviewed`, with the
      task completed and the request drained. Green under run `1790345173`'s test node (64 s)
- [x] Negative control: Claude-provider tasks are unaffected
- [x] The rule line (a line of `─`) is chrome under the shared signature, and the `›`-composer branch
      of `DetectTaskCompletion` never returns a rule or blank line as the summary — when nothing but
      chrome sits above the composer, the task is *not* complete
- [x] A graph `send` node is never completed by a synthesized non-result: it stays `running` until a
      genuine reply or the task timeout, and a genuine codex reply ending `EXIT=0` routes success with
      no hold — negative control: a genuine reply with no sentinel still holds — **code landed**
      (`sendResponseIsNonResult`, `graph_exec.go`), but `bae22dc` carries no graph-level pin for
      either half; Phase 4 must supply them — Phase 4's script (19/0) exercises the daemon task road
      and the review chain, not a graph node. **Pins landed 2026-09-25 10:4x, in the working tree
      outside the graph run** (`bus/graph_nonresult_test.go`): `TestExecSendNodeNonResultWaitsForGenuineReply`
      — a chrome-completed node (rule line, working line) neither routes nor gates, an unrelated reply
      cannot answer it, a genuine `EXIT=0` reply to its own task routes success with no hold;
      `…GenuineReplyWithoutSentinelHolds` (the no-sentinel control); `…Expires` (a chrome-completed node
      still fails on task expiry); `TestSendDeliversReplyToChromeCompletedTask`. They needed production
      changes — `taskAnswered` (`inbox.go`: a task completed by chrome is completed but *unanswered*, so
      the duplicate-reply guard lets the genuine reply through) and `genuineReplyAfterNonResult` in the
      harvester (`graph_exec.go`). **Box stays open.** Review `1790347737` (1 must-fix): `Task.ResponseID`
      still pointed at the chrome after acceptance, so `EXIT=1` then `EXIT=0` before a tick adopted the
      second — a real failure converted into success. Answered by `claimReply` (`inbox.go`: the first
      genuine reply is claimed at acceptance on both reply paths; `TestExecSendNodeNonResultFirstGenuineReplyWins`,
      `TestClaimReplyOutsideTheGraph`). **Review `1790348077` then returned 2 must-fix + 1 should-fix
      on `claimReply`:** (1) `CompleteTask` runs *before* either path appends the reply to the log — a
      failed append leaves the task naming a response that does not exist, `sendResponseIsNonResult`
      then reads false and every retry is suppressed, and a graph tick in the write gap derives an
      outcome from a missing reply; the read/check/write is also unlocked, so concurrent sends can both
      claim — serialize per task, store before publishing the ID, propagate storage errors, keep
      retryability; (2) the `Type == response` and `From == task.To` checks `genuineReplyAfterNonResult`
      carried were dropped, so any event or foreign response bearing the `ReplyTo` becomes the task's
      answer and an `EXIT=0` payload can route the node — restore response-type and normalized
      sender/recipient correlation; (should-fix) `TestClaimReplyOutsideTheGraph`'s self-addressed case
      builds `edit → daemon`, which `Send` delivers normally, so `recordUndeliveredReply` is not exercised.
      **Review `1790348433` (11:0x): both resolved** — the reply is persisted before `ResponseID`,
      write errors propagate, wrong sender or type is refused, and the self-addressed case is a real
      `edit → edit` (`TestClaimReplyRefusesStrangers`, `TestClaimReplyFailedWriteLeavesTaskUnclaimed`,
      `TestClaimReplyOutsideTheGraph` rebuilt) — **and one must-fix remains:** `acceptReply`'s initial
      `ReadTask` fast path returns before the lock is taken, and `writeTask` publishes with
      `os.WriteFile` (truncate, then write), so a second sender reading in that gap sees EOF and delivers
      its contradicting reply unlocked, and the harvester can read the truncated task and fail the node
      as lost. Asked: publish by atomic rename, treat an unreadable task as *unknown* rather than absent,
      decide under the lock whenever a task exists, and add a concurrent-conflicting-send test plus a
      corrupt-task control. **Resolved — review `1790348765` (11:0x): 0 must-fix, 0 should-fix, 1 nit
      (a `defer` tidy in `publishTaskFile`, no behaviour change).** Claims on a completed task serialize
      and re-read under the lock; the task file is published by atomic rename (`publishTaskFile`,
      `task.go`; `TestWriteTaskNeverExposesPartialRecord`); an unreadable task fails closed
      (`TestClaimReplyFailsClosedOnUnreadableTask`); the `readOnlyTask` fixture in `graph_cancel_test.go`
      locks the tasks directory instead of one file, since a rename replaces a read-only file freely.
      Eight tests in `graph_nonresult_test.go`. Suite green on the test agent 11:05:02 (the 11:03:42 red
      was the fixture, repaired at 11:05:00), review fired from that pass. **Ticked 2026-09-25 11:1x**

### Technical approach

Two layers, because the pane heuristic will always be a heuristic. **Recognize** the progress line
as active in `DetectTaskCompletion` by consulting the shared signature in `history_provenance.go`
(and fold `provider_claude.go:178`'s copy into it while there). Then **refuse** at the consumer: the
synthesized-response send path and `checkTrackedTasks` decline to complete on a payload that
`LooksLikeNonResult`, log a `task-nonresult-ignored` lifecycle row, and leave the request in the
inbox. The second layer is what makes the first layer's inevitable misses harmless.

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/provider_codex.go` | `DetectTaskCompletion` (`:404-470`), `SendWakeUp` (`:275`) — where the status line becomes a response |
| `tools/muxcode/bus/history_provenance.go` | `"esc to interrupt"` (`:57`), `LooksLikeNonResult` (`:85`) — the guard to reuse |
| `tools/muxcode/bus/provider_claude.go` | `:178` — the Claude copy of the same signature |
| `tools/muxcode/daemon/daemon.go` | `checkTrackedTasks` (`:2667`), `CompleteTask` sites (`:2712`, `:2805`, `:2870`) |
| `tools/muxcode/bus/delivery.go` | `MarkResponded` → `ConsumeByID` — the drain |
| `tools/muxcode/daemon/status_line_task_close_test.go` | Phase 1's daemon-level pin (2026-09-25): `checkNonHookTasks` on the incident payloads leaves the task in flight and the request in the inbox; a genuine pane still completes and drains. Phase 3's chain control: a review progress line fires no `verify-spec`, a genuine review reply fires exactly one |
| `scripts/test-echo-as-result.sh` | MUX-003's guard test — extend to the task path or sibling it |
| `tools/muxcode/bus/history_provenance.go` | `renderPrefixes` (`:70`), `LooksLikeProviderChrome` (`:99`), `isProviderChromeLine` — `6b53863`'s send-road guard; knows bullets and branches, not the `─` rule |
| `tools/muxcode/bus/inbox.go` | `dropsAsProviderChrome` (`:172`) → `ErrSendChrome`, lifecycle `chrome-send-dropped` |
| `tools/muxcode/bus/graph_exec.go` | `deriveSendOutcome` (`:1277`), `parseExitSentinel` (`:1311`), the unverified hold (`:1425`) — the graph consumer of a synthesized response |

## Implementation

### Phase 1: Pin

- [x] Characterization test: a pane fixture ending `• Working (13s • esc to interrupt)` above a `›`
      prompt → `DetectTaskCompletion` returns `completed=true` with the progress line as summary
      today; failure message names Phase 2 — **superseded**: no pre-fix characterization was written;
      the pin landed directly in its inverted form (`TestDetectTaskCompletionWorkingLineIsActive`,
      `bae22dc`), which is the deliverable this step and Phase 2's inversion step share
- [x] Pin that the synthesized response completes a tracked task and drains the request from the
      inbox (scratch bus) — **pinned in inverted form 2026-09-25**, `daemon/status_line_task_close_test.go`
      (test-only, no production change): `TestCheckNonHookTasks_CodexChromeLeavesRequestPending` runs
      `checkNonHookTasks` on the codex scrape road against the 14:12:55 working line and the 20:31:51
      rule-above-composer payloads — task stays in flight, no response synthesized, request still in
      the inbox (`Peek`), not marked responded, no `task-detected` row — then the same session on a
      genuine `Sent response…` pane completes, answers and drains (negative control);
      `TestCheckNonHookTasks_NonResultSummaryRefused` reaches the consumer layer via an OpenCode stop
      marker (the fixture asserts detection says complete *and* the summary `LooksLikeNonResult`) —
      request pending plus exactly one `task-nonresult-ignored` row, then a genuine `EXIT=0` pane drains.
      Green under run `1790345173`'s test node (70 s)
- [x] Reconstruct the 14:12:55 / 14:13:25 rows from the bus log as the fixture's payload — the pin
      should be the incident, not an invented shape — `provider_codex_chrome_test.go`: `ruleLine158()`
      is the 20:31:51 payload byte for byte, and `• Working (13s • esc to interrupt)` is the 14:12:55
      row's text

### Phase 2: One signature

- [x] Move working-signature detection to a single predicate in `history_provenance.go`; the codex
      bullet-`Working` line joins it — `LooksLikeWorkingLine` over `providerWorkingHint`
- [x] The `─` rule line joins the same signature; the `›`-composer branch skips rule and blank lines
      when it picks the summary and reports *not complete* when only chrome sits above the composer
      (pinned against the 20:31:51 / 20:32:49 payloads) — `isRuleLine`, `lastComposedLine`, the
      `"Task completed"` fallback deleted; `TestDetectTaskCompletionRuleAboveComposerHolds`
- [x] `DetectTaskCompletion` and `provider_claude.go`'s classifier both consult it —
      `provider_claude.go:178` folded; `TestIsClaudeThinkingUnchanged`
- [x] Invert the Phase 1 characterization test — landed in inverted form directly (see Phase 1)
- [x] Negative control: a genuine `Sent response…` pane still detects as complete —
      `TestDetectTaskCompletionGenuineSendCompletes`, `…RealLineAboveComposerCompletes`

### Phase 3: Refuse at the consumer

- [x] The synthesized-response send declines a payload that `LooksLikeNonResult`; the request stays
      in the inbox — the daemon `continue`s before any `Send`, so nothing is written and nothing drains
- [x] `checkTrackedTasks` never completes a task on such a payload; lifecycle `task-nonresult-ignored`
      — the hunk sits in `checkNonHookTasks` (`daemon/daemon.go`), the scrape branch itself
- [x] The graph consumer: `deriveSendOutcome` never receives a synthesized non-result as a node's
      response — the node stays `running`, no hold is raised on it — `sendResponseIsNonResult`
      (`graph_exec.go`) returns before the outcome is derived
- [x] Negative control: a real response completes the task, fires the chain, and drains as today —
      completion and drain `assertCompletedAndDrained` (Phase 1 lap); **chain fire pinned 2026-09-25**
      (Phase 3 lap): `TestCheckNonHookTasks_ReviewChainFiresOnlyOnGenuineReply` in
      `daemon/status_line_task_close_test.go` runs `checkNonHookTasks` then `checkInboxes` on a codex
      review role — progress line: 0 `verify-spec`, no `StateReviewed`, request pending; genuine reply:
      exactly 1 `verify-spec`, `StateReviewed`, completed and drained (`MUXCODE_DEDUP_WINDOW=0` so the
      count is the daemon's, not the dedup guard's). Test-only; green under run `1790345173` (64 s)
- [x] Negative control: Claude-provider task completion unchanged — `TestIsClaudeThinkingUnchanged`,
      and hook providers never enter `checkNonHookTasks`

**Phases 1–3 evidence — `bae22dc` (22:02), 6 files, +290/−20.** Verified by the run agent,
independent of the authoring agents: `gofmt` clean, `build=0 vet=0 bus=0 daemon=0`, coverage floor
`matched_pass_count=9 (expected 9)`, `EXIT=0`.

| Test (`provider_codex_chrome_test.go`) | Pins |
|----------------------------------------|------|
| `TestIsRuleLine` | `─`/`━`/`—` runs are rules; `EXIT=0`, prose and `--` are not |
| `TestLooksLikeProviderChromeAcceptsRuleLine` | the send-road guard (`6b53863`) now drops `ruleLine158()` |
| `TestLooksLikeNonResultRejectsRuleLine` | the console-history road (MUX-003) rejects it; `go test ./bus passed EXIT=0` still passes |
| `TestLooksLikeWorkingLine` | `• Working (24s • esc to interrupt)` and Claude's `✻ Cooking…` are working lines |
| `TestDetectTaskCompletionWorkingLineIsActive` | the 14:12:55 shape reads as *active*, not done |
| `TestDetectTaskCompletionRuleAboveComposerHolds` | the 20:31:51 shape — rule above `›` — reports *not complete* |
| `TestDetectTaskCompletionGenuineSendCompletes` | negative control: a real `Sent …` pane still completes |
| `TestDetectTaskCompletionRealLineAboveComposerCompletes` | negative control: composed prose above `›` still completes |
| `TestIsClaudeThinkingUnchanged` | negative control: the Claude classifier is unchanged |

Also in the commit, beyond the spec's ask: a **turn-separator guard** in `lastComposedLine` — crossing
a rule only accepts a line bearing `EXIT=`, so stale prose from the previous turn cannot become this
turn's answer.

### Phase 4: Integration test

- [x] Create `scripts/test-status-line-task-close.sh` (hermetic; scratch bus + daemon + a fake codex
      pane) or extend `scripts/test-echo-as-result.sh` to the task path — 2026-09-25: scratch bus,
      scratch tmux session and a real scratch daemon started from the scratch repo with every role's CLI
      pinned; three static non-echoing fixture panes (codex review = 14:12:55 working line, codex build
      = 20:31:51 rule line, opencode test = working line over a `▣` stop marker, so only the consumer
      refusal stands)
- [x] Test: progress-line pane → task stays in flight, request remains in the inbox, no chain fire,
      `task-nonresult-ignored` row written — Phase A: all three tasks in flight, requests in the inbox,
      `StateReviewed` not entered, no `verify-spec`, the opencode task's `task-nonresult-ignored` row
- [x] Test: genuine `Sent …` pane → task completes, chain fires (negative control — the guard cannot
      go inert) — Phase C: genuine panes complete all three tasks and the review reply fires `verify-spec`
- [x] Test: `deliver --force` after a progress line still has the request to deliver — Phase B: the
      review request is still pending and `deliver --force` wakes review with 1 pending (a force-deliver,
      not a force-redrive)
- [x] Coverage floor keeps a skipped section from reporting green — `EXPECTED_PASS=19`, exact match
      required
- [x] Run the script and verify all checks pass — **run agent** task `1790346616-spawn-1304d234-8aa4d9eb`,
      reply `1790346675-run-e93ec7f2`: `exit 0. PASS: 19 passed, 0 failed (floor 19)`; stdout at
      `/tmp/test-status-line-task-close.log`

**Phase 4 evidence — 2026-09-25 10:3x, run `1790345173` lap 3.** The first cut drew one review
should-fix (`/tmp/muxcode-review-1790346768.txt`): `:93` started the scratch daemon from the caller's
checkout and inherited `MUXCODE_EDIT_CLI`, so with `edit=codex|opencode` `checkNonHookEdits` would
have `git diff`ed the real repo every 10 s into the scratch workflow. Fixed by the `fix` worker —
`start_daemon` now `cd`s to `$WORK/repo` and `exec`s `muxcode watch` there (`:96`), and every role's
CLI is pinned to `claude` (`:51`); the second review passed. Executed once by the **run agent**, not
by the authoring worker: 19 passed / 0 failed, floor 19 exact, exit 0. CLAUDE.md's script table
carries the row. **What the script does not cover:** a graph `send` node — AC 8's two graph-level
pins (a non-result never completes the node; a genuine reply without a sentinel still holds) remain
open, so the spec stands at 26/27 with that single box.

## Notes

Filed 2026-09-08 by plan from edit's handoff (`/tmp/mux-new-findings-20260908.md`). Edit reported
three echoes; plan found five in `log.jsonl`, matched two of them to the second to the daemon's
`task-detected … succeeded` rows, and established that the `verify-spec` plan was handling at the
time had been fired by them — so this spec's own evidence includes the request that led to its filing.
The mechanism was read from `DetectTaskCompletion` directly.

**Updated 2026-09-08 20:55 — second signature and the graph consequence**, recorded from run
`1788913399-spec-to-pr-83bd9ed6` while it was still held at `test`. The user's read — "thought this
was resolved" — was reasonable: `c4997ed` (19:11) added the `EXIT=` fallback, `6b53863` (20:13) added
the send-road chrome guard, and the build and test definitions mandate the sentinel. None of the three
reaches a node that is closed before its agent replies; this spec is the fix, and it is why the hold
keeps asking.

**Placement argument** (tier 0): it corrupts the completion signal every other control consumes —
the chain, tracked tasks, `verify-spec`, recovery — it converts silence into success, it fired five
times today, and it disarms `deliver --force`. MUX-148 is the same family on the graph road and sits
at #2; this is the task road, and it is firing.

## Time Tracking

| Branch | Active time | Last updated |
|--------|-------------|--------------|
| MUX-154-codex-status-line-closes-tracked-tasks | 47m | 2026-09-25 11:08 |

## Status

**Complete — 27/27 on 2026-09-25 11:1x; every phase and criterion verified.** AC 8 closed last, on
review `1790348765` (0 must-fix) and the test agent's green suite at 11:05:02: `claimReply` accepts the
first genuine reply to a chrome-completed task under a per-task lock, stores it before publishing
`ResponseID`, refuses strangers and non-responses, fails closed on an unreadable task, and `writeTask`
publishes by atomic rename. Moved to `completed/` the same hour on the user's instruction; the
`backlog.md` rows and every cross-reference followed. The record below is as it stood on the way.

Set as the active spec that morning on the user's instruction; run `1790345173` then walked Phases 1,
3 and 4 in three laps (Phase 2 was already done in `bae22dc`): the daemon-level pin (`2a242ff`), the
chain-fire control (`9064c8c`, where the work moved to branch `MUX-154-codex-status-line-closes-tracked-tasks`),
and `scripts/test-status-line-task-close.sh` — 19/0 through the run agent, committed `e3f7e44`. The
run then reached `close-spec`, whose `spec-complete` guard declined on the one open box, and the user
canceled it at 10:44. **Open: AC 8 only.** Its graph-level pins landed in the tree at 10:4x
(`graph_nonresult_test.go`, with `taskAnswered` and then `claimReply` behind them) but each review of
that mechanism has returned must-fix — first the newest-reply-wins conversion of a failure into
success (answered by `claimReply`), then a claim published before the reply is stored plus a dropped
sender/type correlation (both resolved by 11:0x), then the unlocked `ReadTask` fast path over a
truncate-then-write `writeTask` — resolved by the atomic-rename publish and a fail-closed unreadable
case, review `1790348765` clean. Four review rounds on one criterion; each moved a false-green shape
until the last removed it.

**Earlier the same morning** on graph run `1790345173-50-spec-to-pr-68c9b1ce`. Lap 1 (Phase 1:
Pin) landed the daemon-level pin in `daemon/status_line_task_close_test.go` and was committed at the
phase gate as `2a242ff`; lap 2 (Phase 3: Refuse at the consumer) added the chain-fire negative control
to the same file — build/test/review green both laps. **Phases 1–3 are complete (15/15); acceptance
criteria 7/8** — only AC 8's graph-level pins remain, and **Phase 4 (`scripts/test-status-line-task-close.sh`)
is the open work.** The file is still in `backlog/` — the
move to `drafts/` is the user's (edit → commit), and `muxcode spec set` warned that `verify-spec` may
not trigger on a spec outside `drafts/` until then. Issue #75 tracks it; PR #73 carries Phases 1–3.

**Previously: Backlog — parked 2026-09-08 22:50 at 17/27: Phases 1–3 landed in `bae22dc` (22:02), Phase 4 open.**
Moved back from `drafts/` on the user's instruction that every unfinished spec leaves In progress; the
active-spec pointer (set 21:12) was cleared with it. Issue #75 tracks it; PR #73 carries Phases 1–3
and does not close it. The record below is as it stood when parked.

**Previously: In Progress.** Filed 2026-09-08; moved from `backlog/` to `drafts/` at 21:05 the same
day on
the user's "fix this now", after the rule-line variant held the graph's build and test nodes for manual
approval on three runs. Phases 1–3 delegated to edit (`1788917469-plan-7c2cf5e4`); Phase 4 follows. Set as the **active
spec** at 21:12 on the user's request, so `verify-spec` after the review chain checks this file.

**22:15 — `bae22dc` landed and verified.** Phases 1–3 ticked against the commit's tests and hunks
(evidence table under Phase 3): 17/27. Open: the two daemon-level pins (Phase 1 step 2, Phase 3 step
4), the two acceptance criteria whose chain-fire and graph halves have no test, and all of Phase 4
(`scripts/test-status-line-task-close.sh`). Two further chrome shapes are recorded as open signatures
above. The 21:34 interim note this replaces recorded the same code unbuilt; the build, vet and full
bus+daemon suites then ran green on the run agent (`EXIT=0`).
