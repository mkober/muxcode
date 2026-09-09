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
| [`MUX-148`](./MUX-148-node-outcome-reads-command-ran-as-task-done.md) | Graph-executor half of the same family — a node outcome reads "a command ran" as done. That one is about authoritative-row provenance in `graph_exec`; this is the tracked-task store. Same defect shape, different consumer |
| [`MUX-009`](./MUX-009-response-echo-chain-retrigger.md) | A *response* injected back as a *prompt* on the receiving side. This is a status line synthesized as a *response* on the sending side. Distinct mechanisms, both "the bus believes a TUI" |
| [`MUX-127`](./MUX-127-review-completion-routing.md) | Routes the chain on review outcomes; this corrupts the outcome it routes on |
| [`MUX-153`](./MUX-153-codex-test-agent-cannot-run-the-suite.md) | Why the codex test agent has no real answer to give — this defect is what turns that silence into "done" |
| [`MUX-159`](./MUX-159-codex-hooks-provider.md) | The structural fix: Codex ships `PostToolUse`/`Stop`/`UserPromptSubmit` hooks (verified 2026-09-08 on 0.153.4), so a hook-enabled codex agent is never scraped — this spec patches the scrape, that one removes its reason to exist for codex |

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
- [ ] Negative control: a genuine codex reply (`Sent response…` in the pane) still completes the task
      and fires the chain exactly as today — **detection half pinned**
      (`TestDetectTaskCompletionGenuineSendCompletes`); the daemon completion and chain fire have no
      test in `bae22dc`
- [x] Negative control: Claude-provider tasks are unaffected
- [x] The rule line (a line of `─`) is chrome under the shared signature, and the `›`-composer branch
      of `DetectTaskCompletion` never returns a rule or blank line as the summary — when nothing but
      chrome sits above the composer, the task is *not* complete
- [ ] A graph `send` node is never completed by a synthesized non-result: it stays `running` until a
      genuine reply or the task timeout, and a genuine codex reply ending `EXIT=0` routes success with
      no hold — negative control: a genuine reply with no sentinel still holds — **code landed**
      (`sendResponseIsNonResult`, `graph_exec.go`), but `bae22dc` carries no graph-level pin for
      either half; Phase 4 must supply them

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
- [ ] Pin that the synthesized response completes a tracked task and drains the request from the
      inbox (scratch bus) — **open**: `bae22dc` adds no daemon-level test; the refusal in
      `checkNonHookTasks` is unpinned
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
- [ ] Negative control: a real response completes the task, fires the chain, and drains as today —
      **open**: no daemon-level test in `bae22dc` (the detection half is pinned; the completion, chain
      and drain are not)
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

- [ ] Create `scripts/test-status-line-task-close.sh` (hermetic; scratch bus + daemon + a fake codex
      pane) or extend `scripts/test-echo-as-result.sh` to the task path
- [ ] Test: progress-line pane → task stays in flight, request remains in the inbox, no chain fire,
      `task-nonresult-ignored` row written
- [ ] Test: genuine `Sent …` pane → task completes, chain fires (negative control — the guard cannot
      go inert)
- [ ] Test: `deliver --force` after a progress line still has the request to deliver
- [ ] Coverage floor keeps a skipped section from reporting green
- [ ] Run the script and verify all checks pass

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

## Status

**Backlog — parked 2026-09-08 22:50 at 17/27: Phases 1–3 landed in `bae22dc` (22:02), Phase 4 open.**
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
