# MUX-186: Cancel Races and Fail-Open Cleanup Merged Unaddressed in PR #89

PR #89 (the [MUX-182](../completed/MUX-182-cancelled-run-keeps-working-provenance-unreadable.md)
branch) was merged to `main` in `3e9ac86` on 2026-09-24 with Copilot's review — *"Changes
recommended: unresolved critical cancellation races and fail-open cleanup paths block safe
approval"*, six inline comments, no replies — never addressed. The merge was approved by the user at
`110-pr-merge`'s `merge-gate`, which shows CI only; the commit agent surfaced the open review after
merging (why the gate is blind is [MUX-187](./MUX-187-pr-merge-merges-over-unresolved-review-comments.md)).
**Every finding below was verified by plan against the merged tree at `3e9ac86`**; three of them
contradict acceptance criteria MUX-182 ticked as met.

## Context

### Source and standard of evidence

Copilot review on PR #89, submitted 16:26:53Z, merged over at 20:10:41Z; the six comments are
preserved verbatim in `/tmp/pr89-copilot-review.md` at filing. Filed on the user's instruction
relayed by edit ("file today's open follow-ups"). No live failure has been observed for any of the
six; each is a code-path reading, confirmed line by line below.

### The six findings — verified at `3e9ac86`

| # | Where | What the code does | Why it matters |
|---|---|---|---|
| 1 | `bus/cancel_authority.go:77-82` | `StopSpawnAuthorized` tries `lockGraphRun`; on failure it **continues with the placeholder** `&GraphRun{ID: e.RunID}` and asks `CheckCancelAuthority` about that | The serialization the lock exists for is not enforced: `RetryGraphRun` can resume or create work between the authority decision and `StopSpawn`, and a placeholder run has no `CreatedBy`, so the authority answer is given on a run the code did not read |
| 2 | `bus/graph_cancel.go:101-110` | `UpdateGraphRunState(canceling)` → **`PurgeSessionArtifacts` at `:104`** → `ReadAllNodeStatuses` → `runSpawnRoles` → worker stops | `cdk.out` is purged **before** any worker is stopped, so a live spawn still building or writing it races the purge; MUX-182's own ordering says workers stop first |
| 3 | `bus/graph_cancel.go:142-146` | A running `NodeSend`'s task is expired (`expireTask`); the agent executing it is **not** interrupted or stopped, and the function proceeds to `GraphRunCanceled` at `:154` | The agent can still edit files or call APIs after `graph cancel` returns — the guarantee MUX-182 AC 2 states ("cannot mutate files or call an external API after the cancel returns") holds for spawn workers only |
| 4 | `bus/graph_cancel.go:167-185` with `bus/spawn.go:56-58` | `runSpawnRoles` builds `known` from `ReadSpawnEntries`, which **skips malformed JSONL lines** (`continue // skip malformed lines`); a role named in `st.TaskID` but absent from `known` is ignored | A live run-owned worker whose registry line is damaged is neither stopped nor named as a survivor; cancel reaches `GraphRunCanceled` with it alive — **fail-open**, the exact shape MUX-182 AC 1 ("refuses … while naming exactly which spawns survived") forbids |
| 5 | `bus/provider_claude.go:315-317` | After the live-menu check (`claudeTrustPromptLive`) and the bypass check, `ClassifyPane` falls back to `strings.Contains(content, claudeTrustOption)` → `PaneTrustPrompt` | Reintroduces the stale-scrollback false positive the live check was added to remove: an idle pane whose scrollback still shows an answered "trust this folder" is classified as a trust prompt, and startup retries `AcceptClaudeTrust` instead of seeing an idle agent |
| 6 | `tui/graph.go:406-408` | `headerLines++` and the provenance line are conditional on `snap.Run.CreatedBy != ""` | An unrecorded run's DAG header shows **no** provenance, while `DescribeRunCreator` renders `unrecorded` and `graph runs`/`graph status` show it — the "provenance on every surface" AC (MUX-182 AC 3) has a surface that goes silent, and the height accounting differs from the other surfaces |

### Which MUX-182 criteria this reopens

| MUX-182 AC (ticked) | Finding | Status after this spec |
|---|---|---|
| 1 — cancel terminates spawns **or** refuses while naming survivors | 4 | a malformed registry line makes it do neither |
| 2 — a cancelled run cannot mutate files or call an API after cancel returns | 2, 3 | true for spawn workers (measured in Phase 6); false for a running send node's agent, and the purge precedes the stop |
| 3 — every provenance surface distinguishes user/autonomous | 6 | the DAG header omits the unrecorded case |
| 7 — an agent's `spawn stop` on a user run requires approval | 1 | judged on a placeholder when the lock fails |

MUX-182 stays closed; its Phase 6 script (`test-cancel-provenance.sh`, 57/0) measured the spawn road
and did not exercise a running send node, a damaged registry, or a failed run lock. This spec adds
those controls rather than reopening the closed one.

### Blast radius

- Findings 2–4 are the false-success shape the tier-0 family exists for: `graph cancel` returns
  success while work continues. Not observed live; the paths are real and unguarded.
- Finding 5 affects every Claude agent launch whose pane scrollback holds an answered trust prompt —
  a relaunch in a long-lived window — and reads as a stuck startup.
- Finding 1 needs a lock contention at the instant of an agent's `spawn stop`; rare, but it is the
  authority decision that is wrong when it happens.
- Finding 6 is display only.

### Family

- [MUX-182](../completed/MUX-182-cancelled-run-keeps-working-provenance-unreadable.md) — the parent; ACs 1, 2, 3 and 7 are the ones reopened.
- [MUX-187](./MUX-187-pr-merge-merges-over-unresolved-review-comments.md) — why the review reached `main` unread.
- [MUX-148](../completed/MUX-148-node-outcome-reads-command-ran-as-task-done.md), [MUX-178](./MUX-178-spawn-node-cuts-no-worktree-port-harvest-broken.md) — the self-concealing false success family.
- The tail-anchored prompt detection lesson (MUX-163, codex trust/approval prompts) — finding 5 is the same "match the live prompt, not the scrollback" rule.

## Requirements

### Acceptance criteria

- [x] `StopSpawnAuthorized` returns the lock error (or retries within `graphRunLockWait` and then refuses) when `lockGraphRun` fails; it never decides authority on the placeholder run — test: a held lock → `spawn stop` refused with a message naming the lock, `spawn-stop-refused` logged — `eb9d40a` returns the lock error; `f54f83a` refuses on any run-read error but "does not exist"; `TestStopSpawnAuthorizedRefusesWithoutTheRunLock`, `TestStopSpawnAuthorizedRefusesUnlockedOnCorruptRun`
- [x] `PurgeSessionArtifacts` runs **after** every run-owned worker is verified stopped, and not at all when the cancel is incomplete — test: a worker still writing under `cdk.out` at cancel time is stopped first and the purge follows; on `CancelIncompleteError` the artifact survives — `eb9d40a`: purge only after every worker is stopped and no cleanup step failed; `TestCancelPurgesArtifactsOnlyAfterWorkersStop`
- [x] A running `send` node's agent is interrupted or the run stays `canceling` until it is proven idle; `graph cancel` never reports `canceled` while a send node's agent is mid-turn — test: a busy pane on a running send node → run left `canceling`, the node and pane named in the reply — `eb9d40a` + `6b50c09`: the agent's pane decides — busy after receipt fails the cancel, idle or never-received expires the task, and a lost receipt is *unknown*, not proof; `TestCancelFailsClosedOnARunningSendAgent`, `TestCancelProceedsPastAnIdleSendAgent` (control), `TestCancelTreatsLostReceiptAsUnknown`
- [x] A malformed line in `spawn.jsonl` is a **cleanup failure**: `runSpawnRoles` (or `ReadSpawnEntries` via a strict variant) reports it, the cancel returns `CancelIncompleteError` naming the file and line, and the run stays `canceling` — test: corrupt one registry line for a live worker → cancel refuses and names it; **positive control:** an intact registry cancels as today — `eb9d40a` + `f54f83a`: a malformed or identity-less line, a missing registry with a live worker, and a failed window listing all fail the cancel; `TestCancelFailsClosedOnMalformedRegistryLine`, `…IdentitylessRegistryRecord`, `…MissingRegistry`, `…MissingRegistryParkedWorker`, `…WindowLookupError`
- [x] `ClassifyPane` reports `PaneTrustPrompt` only from `claudeTrustPromptLive` (bypass check first); the `claudeTrustOption` substring fallback is removed — test: an idle pane with an answered trust prompt in scrollback → `PaneIdle`; a live trust menu → `PaneTrustPrompt` (positive control) — `eb9d40a` + `f54f83a`: trust text is a prompt only when live; near the composer without its footer it is `PaneNotReady` (still drawing), in scrollback it is ignored; `TestClassifyPane_TrustStillDrawingIsNotIdle` plus four `provider_test.go` cases
- [x] `renderGraphHeader` renders the provenance line unconditionally, with `unrecorded` for an empty `CreatedBy`, and `headerLines` counts it unconditionally — test: golden frame for an unrecorded run shows the line; height accounting equal for recorded and unrecorded runs — `eb9d40a`: `tui/graph_test.go` golden case `"launched by: unrecorded"`, frame heights rebased one row taller for every run
- [x] Each fix carries the negative control that would have caught it: the test fails at `3e9ac86` and passes after — every fail-closed test above is paired with a case that still proceeds (`eb9d40a`, `f54f83a`: "each with a negative control"); the *fails at `3e9ac86`* half was not re-run by plan — **accepted, not re-verified**
- [x] MUX-182's Known gaps (or a note under its ACs 1, 2, 3, 7) records that this spec holds the follow-through; the closed spec's ticks are not reverted — written 2026-09-25 under MUX-182's acceptance criteria
- [ ] `bash scripts/test-cancel-provenance.sh` still passes (57, floor 56) and `bash scripts/test-cancel-followups.sh` passes — **open at closure**: `test-cancel-followups.sh` is not written (Phase 5), and `test-cancel-provenance.sh` was not re-run after the follow-ups

### Technical approach

Six small fixes, one per finding, in build order: **4, 2, 3** (the fail-open cleanup trio, all in
`graph_cancel.go`), then **1** (authority), **5** (startup classification), **6** (TUI). For 3, the
cheapest sound behaviour is to **keep the run in `canceling`** and name the send node and its pane
when `AgentIsWorking` is true, rather than inventing an interrupt road — MUX-171 already established
that no road re-drives or interrupts a working pane. For 4, prefer a strict reader
(`ReadSpawnEntriesStrict` returning the first parse error with its line number) used by cancel only,
so display callers keep tolerating a damaged file.

### Key files

| File | Role |
|---|---|
| `tools/muxcode/bus/cancel_authority.go:69-91` | `StopSpawnAuthorized` — finding 1 |
| `tools/muxcode/bus/graph_cancel.go:95-193` | `CancelGraphRun` body, `runSpawnRoles` — findings 2, 3, 4 |
| `tools/muxcode/bus/spawn.go:40-63` | `ReadSpawnEntries` — the lenient parser finding 4 rests on |
| `tools/muxcode/bus/provider_claude.go:305-322` | `ClassifyPane` — finding 5 |
| `tools/muxcode/tui/graph.go:402-408` | header height accounting; `renderGraphHeader` — finding 6 |
| `tools/muxcode/bus/graph_cancel_test.go`, `cancel_authority_test.go`, `provider_claude_test.go`, `tui/graph_test.go` | the negative controls |
| `scripts/test-cancel-provenance.sh` | MUX-182's script — must stay green |
| `docs/requirements/completed/MUX-182-cancelled-run-keeps-working-provenance-unreadable.md` | Known-gaps note pointing here |

## Implementation

### Phase 1: Fail-open cleanup (findings 4, 2, 3)

- [x] Strict registry read for cancel: a malformed line → `CancelIncompleteError` naming file and line, run stays `canceling`; positive control with an intact file — `eb9d40a`, hardened in `f54f83a` (`scanSpawnEntries` counts a record without ID or SpawnRole as malformed; a missing registry with a live window fails the cancel)
- [x] Move `PurgeSessionArtifacts` below the worker-stop loop, guarded on an empty `survivors`/`cleanup`; test that an incomplete cancel leaves the artifact — `eb9d40a`, `TestCancelPurgesArtifactsOnlyAfterWorkersStop`
- [x] Running send node: check `AgentIsWorking` on the node's role; if working, add it to `survivors` with the pane named and leave the run `canceling`; test with a stub pane that renders mid-turn — `eb9d40a`; `6b50c09` then stopped a lost receipt being read as "never received"
- [x] Failing-at-`3e9ac86` controls for each — controls present in each test pair; the pre-fix red was not re-run (AC 7, accepted)

### Phase 2: Authority and startup (findings 1, 5)

- [x] `StopSpawnAuthorized`: propagate the lock error; unit test with a held lock; positive control with a free lock — `eb9d40a` (`TestStopSpawnAuthorizedRefusesWithoutTheRunLock`), `f54f83a` (`…RefusesUnlockedOnCorruptRun`)
- [x] `ClassifyPane`: delete the substring fallback; tests for answered-prompt-in-scrollback → `PaneIdle`, live menu → `PaneTrustPrompt`, bypass-over-trust precedence — `eb9d40a` + `f54f83a`; `TestClassifyPane_TrustStillDrawingIsNotIdle` replaced `TestClassifyPane_TrustTakesPrecedence` (a trust option line with no footer is not-ready, not a prompt)
- [x] `AcceptStartup` no longer loops on a stale trust text (regression test on the relaunch shape) — covered at the classifier: stale trust text near an idle composer reads idle (`f54f83a`, four `ClassifyPane` cases); no `AcceptStartup`-level test was added

### Phase 3: DAG header (finding 6)

- [x] Unconditional provenance line and height count; golden test for recorded and unrecorded runs; `tui-style` checklist (height honoured, readable without colour) — `eb9d40a`: the header always carries `launched by:`; `tui/graph_test.go` adds the `unrecorded` case and rebases the windowed/short frame heights by one row

### Phase 4: Docs and MUX-182 cross-reference

- [x] MUX-182 Known gaps entry naming this spec and the four ACs; `docs/architecture.md` cancel-ordering sentence updated (workers stop, then purge) — 2026-09-25: follow-through note under MUX-182's acceptance criteria; `architecture.md`'s graph-workers paragraph now states the order (workers stop, then purge; a survivor, a mid-turn send agent or an unreadable registry keeps the run `canceling`)

### Phase 5: Integration test

- [ ] Create `scripts/test-cancel-followups.sh` — live scratch daemon (same harness shape as `test-cancel-provenance.sh`), a stub agent that heartbeats under `cdk.out`
- [ ] Test: corrupt one registry line → `graph cancel` refuses, names the line, run stays `canceling`; repair → cancel completes
- [ ] Test: heartbeat file under `cdk.out` survives an incomplete cancel and is purged only after a complete one
- [ ] Test: a send node whose pane is mid-turn → cancel leaves `canceling` and names the pane
- [ ] Test: `spawn stop` under a held run lock → refused with the lock named
- [ ] Test: `--render-once` of the DAG for an unrecorded run shows `unrecorded`
- [ ] Coverage floor keeps a skipped section from reporting green; `test-cancel-provenance.sh` re-run and still ≥ 56
- [ ] Run the script and record the pass/fail counts here

## Out of scope

- Reverting MUX-182's ticks or reopening it — the Phase 6 measurement it made stands for the road it measured.
- A general "interrupt a working agent" road — MUX-171's rule holds; a working send node keeps the run `canceling`.
- Why the review was merged over — [MUX-187](./MUX-187-pr-merge-merges-over-unresolved-review-comments.md).

## Status

Complete — closed 2026-09-25 **on the user's instruction ("remove 186 from backlog defect list"), by
acceptance, at 17/26.** All six findings are fixed on `main` in PR #91 (`7b2e0b4`): `eb9d40a` (the six
fixes, plus two the local review found), `f54f83a` (PR #91's own Copilot findings — fail-closed cancel
scan, trust menu still drawing) and `6b50c09` (a lost receipt is not proof a send request was unread).
Twelve new tests, each paired with a control. **Open at closure, recorded not done:** all of Phase 5 —
`scripts/test-cancel-followups.sh` and the `test-cancel-provenance.sh` re-run (AC 9) — and AC 7's
"fails at `3e9ac86`" half, which plan did not re-run. The spec never entered `drafts/`: the fix was
written the day it was filed. **Ready to move to `completed/`** — the move is the user's (edit →
commit); the cross-references here, in MUX-182, MUX-187 and `backlog.md` follow it.

Filed 2026-09-24 on the user's instruction relayed by edit, from Copilot's review of PR #89 (six
inline comments, none answered before the merge in `3e9ac86`). Every finding verified by plan
against the merged tree; findings 2–4 reopen MUX-182 ACs 1 and 2, finding 6 AC 3, finding 1 AC 7.
None observed live. Not started.
