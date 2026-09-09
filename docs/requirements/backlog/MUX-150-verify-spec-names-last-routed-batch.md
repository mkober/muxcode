# MUX-150: `verify-spec` Names the Last Routed Batch, Not the Change Set

The `verify-spec` request the daemon sends plan after every review completion lists "Changed
files" copied from the workflow entry's `LastFiles` — the files of whichever hook write or
analyze-route batch happened to transition the workflow **last** — not the set of changes the
review covered. Three requests in four minutes on 2026-09-08 each named **one** file while 5, 5
and 8 Go files were dirty. The third named a `docs/` file plan itself had just written, and no
code file at all: the verification was told its own output was the thing to verify.

Observed live by plan, 2026-09-08, session `muxcode`, branch `MUX-144-wait-human-gate-openable-by-any-agent`.

## Context

### Observed

| `verify-spec` sent | "Changed files" named | Dirty in the working tree at the time | Unnamed |
|--------------------|-----------------------|----------------------------------------|---------|
| 09:50:14 | `tools/muxcode/tui/graph.go` | 5 Go files + the spec | `bus/intent.go`, `bus/intent_test.go`, `tui/graph_ui.go`, `tui/graph_ui_test.go` |
| 09:52:18 | `tools/muxcode/tui/graph_ui.go` | same 5, ~70 lines larger | `tui/graph.go` (28 → 65 lines changed), `tui/graph_ui_test.go` (62 → 97), and the rest |
| 09:54:41 | `docs/requirements/drafts/MUX-144-…md` — **plan's own time-tracking write** | **8** Go files, incl. `cmd/launcher.go` (+59), `tui/model.go`, `bus/launcher.go` | **every code file** |
| 10:06:05 | `docs/requirements/backlog/backlog.md` — **plan's index write filing this spec** | the same 8 (`cmd/launcher.go` now +70) plus a **new, untracked** `cmd/launcher_test.go` | **every code file**, including the new test |

The fourth row also shows the batch has no notion of who wrote what: the 10:03:05 `trigger-route`
merged edit's `cmd/launcher.go` and `cmd/launcher_test.go` with plan's write of this very spec into
one three-file batch, and the next route (10:04:04, `backlog.md` alone) overwrote it.

Plan swept the full `git diff` on each pass rather than the named file, so nothing was mis-ticked.
But the message's premise was false all three times, and the third inverted it. The only thing that
kept the passes honest was the message's own escape clause — *"the repo working tree is the source
of truth for what changed"* — which works precisely to the extent the verifier **ignores the list it
was just given**.

> **A false reading, recorded so it is not re-derived.** Plan first read the third request as a
> self-sustaining loop — doc write → review → `verify-spec` → doc write — and told edit so, then
> withheld the time record to "break" it. The lifecycle log refutes that: the review behind it was
> edit's own chain (`build from edit` 09:52:41 → `test from build` 09:53:08 → `review from test`
> 09:54:20), and `VerifyMovementFingerprint` already skips `docs/`
> (`bus/provenance.go:133`). The doc write caused nothing downstream; it only **renamed** what the
> next request pointed at. Corrected to edit within the same pass. The symptom (a `verify-spec`
> naming the spec) invites exactly this misreading, which is part of why it is filed.

### Mechanism — verified in code

| Step | Code | Behaviour |
|------|------|-----------|
| Any write, any window | `ProcessAnalyzeHook`, `bus/hook.go:1359` | Appends the path to the trigger file and calls `TransitionWorkflow(StateEditing, "hook:analyze:edit", WithFiles([]string{filePath}))`. The only window check in the function is the nvim-diff cleanup for `edit` (`:1389`) — the transition and the trigger fire for the **plan** window too |
| Route | `routeTrigger`, `daemon/daemon.go` (`trigger-route`) | Debounced batch → `TransitionWorkflow(StateAnalyzing, "daemon:analyze-route", WithFiles(files))` and an `analyze` event |
| Replace, never accumulate | `WithFiles`, `bus/workflow.go:136-144` | `e.LastFiles = files` (or `files[:5]`) — each transition **overwrites** the list |
| `reviewed` keeps the stale list | `daemon/daemon.go:419` | `TransitionWorkflow(StateReviewed, "daemon:review-complete", WithOutcome(...))` — no `WithFiles`, so the entry still carries whatever the last write or route left |
| Verify copies it | `notifyPlanOnReview`, `daemon/daemon.go` (`plan-verify`) | `files := RepoScopedFiles(repoDir, wf.LastFiles)` → `"Changed files (repo-relative): %s"` |

So "Changed files" is **the last ≤ 5 paths of the last debounce batch, from whichever agent wrote
last**. `LastFiles` was built for the workflow display — `bus/workflow.go:255-261` renders it as
`last: <basenames>` — and is being read as an instruction.

### Two consequences

1. **The verifier is pointed at the wrong files.** A verifier that trusts the list reads one file,
   ticks or declines to tick against it, and never sees the seven others. The escape clause is a
   mitigation that depends on the recipient disbelieving the sender.
2. **Plan's own writes drive the workflow state machine.** Each spec write this session regressed
   `reviewed → editing` (09:51:11 `editing from=reviewed trigger=hook:analyze:edit`) and pinged the
   analyze role — windowless in this session
   ([`MUX-145`](./MUX-145-messages-routed-to-windowless-role.md)). Plan writes docs on **every**
   verification by design (ticks, status, time tracking), so every verification pass rewrites the
   state the next verification reads. A docs write is not an edit of the thing under review.

### Relationship to existing specs

| Spec | Relation |
|------|----------|
| [`MUX-007`](../completed/MUX-007-verify-spec-stale-review-refire.md) | Orthogonal. That gate fixed *how many times* `verify-spec` fires per review completion — it now fires the right number of times with the wrong file list |
| [`MUX-145`](./MUX-145-messages-routed-to-windowless-role.md) | Consequence 2 lands in the inbox that spec describes |
| [`MUX-006`](./MUX-006-diagnose-false-clean-verdict.md), [`MUX-124`](./MUX-124-lifecycle-since-truncated-by-limit.md) | Same family — an instrument misreports its own subject |

## Requirements

### Acceptance criteria

- [ ] The `verify-spec` "Changed files" list is the set of **code** files changed in the repo since
  the previous verification (or the branch base — see Open decisions), not the last workflow batch
- [ ] A `verify-spec` never names the active spec, or any `docs/` path, as what changed
- [ ] A write from the plan window neither regresses the workflow state nor routes to analyze
- [ ] **Negative control:** an edit-window write to a code file still regresses to `editing`, still
  routes to analyze, and the nvim diff cleanup still runs
- [ ] **Negative control:** MUX-007's once-per-completion gate is unchanged — exactly one
  `verify-spec` per review completion
- [ ] The workflow display's `last: …` keeps its current meaning; nothing else reads `LastFiles` as
  a change set
- [ ] A unit test pins the list's source so it cannot drift back to `LastFiles`

### Technical approach — options

| Option | What | Trade |
|--------|------|-------|
| **A. Derive from git** | At `notifyPlanOnReview`, list dirty non-docs paths with the same `git status` walk `VerifyMovementFingerprint` already does (`bus/provenance.go:124-141`, which already skips `docs/`); optionally diff against the last verification's fingerprint to name only what moved | Reuses a walk that exists; names every file, not ≤ 5 |
| B. Accumulate `LastFiles` | Append across the `editing → reviewed` cycle, reset on `reviewed` | Changes the display field's semantics and keeps the 5-cap; plan's writes still enter the list |
| **C. Gate the hook** | `ProcessAnalyzeHook` skips the transition and the trigger for plan-window writes (or for `docs/` paths — Open decision 1) | Independent of A/B; the only option that fixes consequence 2 |

**Recommended: A + C.** B alone still lists at most five files and still lets a docs write into
the set.

### Key files

| File | Relevance |
|------|-----------|
| `tools/muxcode/daemon/daemon.go` | `notifyPlanOnReview` — the list source; `routeTrigger` — the batch transition; `:419` — the `reviewed` transition that carries no files |
| `tools/muxcode/bus/hook.go` | `ProcessAnalyzeHook` (`:1359`) — no window or path gate on the transition and trigger |
| `tools/muxcode/bus/workflow.go` | `WithFiles` (`:136`) replace semantics and 5-cap; display (`:255`) |
| `tools/muxcode/bus/provenance.go` | `VerifyMovementFingerprint` (`:119`) — already walks status and skips `docs/`; `RepoScopedFiles` (`:34`) |
| `scripts/test-verify-spec-refire.sh` | The harness shape the integration test reuses |

## Implementation

### Phase 1: Pin the misreport

- [ ] Unit test: a workflow entry whose `LastFiles` is the spec path, with eight dirty code files in
  the repo dir → today's message names only the spec (fails after Phase 2)
- [ ] Unit test: a plan-window write under `docs/` transitions the workflow to `editing` and writes
  the trigger (fails after Phase 3)
- [ ] Confirm nothing else consumes `LastFiles` as a change set (`grep LastFiles`), and record it

### Phase 2: Derive the change set from git

- [ ] Extract the status walk from `VerifyMovementFingerprint` into a shared helper that returns
  dirty non-docs repo-relative paths
- [ ] `notifyPlanOnReview` builds "Changed files" from it; keep the
  `(none verified in the repo working tree)` fallback for an empty set
- [ ] List every file — no 5-cap; if a cap is kept, name the elided count (`+N more`)
- [ ] Unit test pins the source

### Phase 3: Keep docs writes out of the workflow

- [ ] `ProcessAnalyzeHook`: no transition and no trigger for the gated case (window or path — Open
  decision 1)
- [ ] Negative control: an edit-window code write is unchanged (transition, trigger, diff cleanup)

### Phase 4: Integration test

- [ ] Create `scripts/test-verify-spec-files.sh` — scratch bus, daemon and repo dir via
  `MUXCODE_SESSION_REPO_DIR`, the `test-verify-spec-refire.sh` harness
- [ ] Test: dirty three code files across two debounce batches, complete a review → the
  `verify-spec` names all three
- [ ] Test: plan writes the spec between the batches → the `verify-spec` still names the three code
  files and never the spec, and the workflow state was not regressed by the docs write
- [ ] Negative control: an edit-window code write still routes to analyze and regresses to `editing`
- [ ] Negative control: exactly one `verify-spec` per review completion (MUX-007 gate)
- [ ] Coverage floor so a skipped section cannot report green
- [ ] Run the script and verify all checks pass

## Open decisions

### Decision 1 — gate the hook by window or by path?

By **window** (`plan`) uses what the hook already receives and matches the role rule (plan writes
only docs). By **path** (`docs/`) is robust to which window writes docs, but `CLAUDE.md` and
`README.md` at the repo root are docs the edit agent may write and a `docs/` rule leaves them in the
change set — which is arguably correct, since they are not specs. Not chosen here.

### Decision 2 — "changed since when"?

Since the **last verification** (diff against the stored movement fingerprint) names only what the
review just covered; since the **branch base** names everything on the branch and is simpler. The
verifier's job is the former. Not chosen here.

## Out of scope

- Routing to a windowless analyze role — [`MUX-145`](./MUX-145-messages-routed-to-windowless-role.md).
- What the review agent itself is told to review; this spec covers only the message plan receives.

## Status

Draft — filed 2026-09-08 from three `verify-spec` requests received in one session. Every code
claim above was verified against this repo; every timing against the lifecycle log. No
implementation has started.
