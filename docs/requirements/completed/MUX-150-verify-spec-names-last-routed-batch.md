# MUX-150: `verify-spec` Names the Last Routed Batch, Not the Change Set

**Tracking:** [mkober/muxcode#117](https://github.com/mkober/muxcode/issues/117)

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
| Any write, any window | `ProcessAnalyzeHook`, `bus/hook.go:2086` (re-read 2026-11-24; `:1359` at filing — still no window or path gate) | Appends the path to the trigger file and calls `TransitionWorkflow(StateEditing, "hook:analyze:edit", WithFiles([]string{filePath}))`. The only window check in the function is the nvim-diff cleanup for `edit` (`:1389`) — the transition and the trigger fire for the **plan** window too |
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
   ([`MUX-145`](../completed/MUX-145-messages-routed-to-windowless-role.md)). Plan writes docs on **every**
   verification by design (ticks, status, time tracking), so every verification pass rewrites the
   state the next verification reads. A docs write is not an edit of the thing under review.

### Relationship to existing specs

| Spec | Relation |
|------|----------|
| [`MUX-007`](../completed/MUX-007-verify-spec-stale-review-refire.md) | Orthogonal. That gate fixed *how many times* `verify-spec` fires per review completion — it now fires the right number of times with the wrong file list |
| [`MUX-145`](../completed/MUX-145-messages-routed-to-windowless-role.md) | Consequence 2 lands in the inbox that spec describes |
| [`MUX-006`](../completed/MUX-006-diagnose-false-clean-verdict.md), [`MUX-124`](../completed/MUX-124-lifecycle-since-truncated-by-limit.md) | Same family — an instrument misreports its own subject |

## Decisions (user, 2026-11-24)

Relayed by edit; this spec was parked on its two open decisions and is unparked by them.

| Question | Decision |
|----------|----------|
| Decision 1 — gate the hook by window or by path | **By path: `docs/`.** A write under `docs/` neither transitions the workflow nor writes the analyze trigger, whichever window made it. Root `CLAUDE.md` and `README.md` are not under `docs/` and stay in the change set |
| Decision 2 — "changed since when" | **Since the last verification's fingerprint**, using the `git status` walk in `bus/provenance.go`. The branch-base alternative is dropped |

**Read in the tree 2026-11-24.** `VerifyMovementFingerprint` (`bus/provenance.go:119`) hashes the
walk into one fingerprint; a single hash cannot say *which* paths moved. Naming only what changed
since the last verification therefore needs the per-path state kept beside the fingerprint (path and
content hash at the last verification). That is the implementation the decision implies, and Phase 2
says so; the stored form is the implementer's to choose. One more thing follows from MUX-219: a
review a graph run dispatched sends no daemon `verify-spec` and leaves the movement marker
unwritten, so "the last verification" is the last one the daemon itself fired — Phase 2 records
what the list then covers.

## Decision (user, 2026-11-27)

Relayed by edit, after this spec closed. Plan had flagged a side effect of Decision 1 as built: the
analyze role receives no event for a documentation edit
([Findings for Phase 3](#findings-for-phase-3)).

| Question | Decision |
|----------|----------|
| Should the analyze role still hear about `docs/` edits? | **No — analyze does not need them.** Decision 1 stands as built: a write under `docs/` writes no trigger and moves no workflow state. Nothing is to be changed |

## Requirements

### Acceptance criteria

- [x] The `verify-spec` "Changed files" list is the set of **code** files whose state differs from
  the previous verification's, not the last workflow batch; with no previous verification on record
  it is every dirty non-docs path — Phase 2; `TestNotifyPlan_ChangedFilesIsTheGitChangeSet`,
  `TestNotifyPlan_ChangedFilesSinceLastVerification`
- [x] A `verify-spec` never names the active spec, or any `docs/` path, as what changed — Phase 2:
  the walk drops every `docs/` path; the first test above asserts the spec is absent
- [x] A write under `docs/` neither regresses the workflow state nor routes to analyze, from any window
  — Phase 3, unit tests (plan and build windows)
- [x] **Negative control:** an edit-window write to a code file still regresses to `editing`, still
  routes to analyze, and the nvim diff cleanup still runs; a root `CLAUDE.md` write is still routed —
  Phase 3, unit tests: the trigger is written and the state regresses; the daemon's own route is
  exercised by Phase 4's script
- [x] **Negative control:** MUX-007's once-per-completion gate is unchanged — exactly one
  `verify-spec` per review completion — the fingerprint's git call and value are untouched, and
  `scripts/test-verify-spec-refire.sh` passes 43/0 against a muxcode built from this tree
- [x] The workflow display's `last: …` keeps its current meaning; nothing else reads `LastFiles` as
  a change set — `WithFiles` and the display are unchanged; the one change-set reader is gone
- [x] A unit test pins the list's source so it cannot drift back to `LastFiles` —
  `TestNotifyPlan_ChangedFilesIsTheGitChangeSet` sets `LastFiles` to the spec and requires the eight
  dirty files instead

### Technical approach — options

Decided 2026-11-24: **A + C**, C gated by path. B is not built.


| Option | What | Trade |
|--------|------|-------|
| **A. Derive from git** | At `notifyPlanOnReview`, list dirty non-docs paths with the same `git status` walk `VerifyMovementFingerprint` already does (`bus/provenance.go:124-141`, which already skips `docs/`); optionally diff against the last verification's fingerprint to name only what moved | Reuses a walk that exists; names every file, not ≤ 5 |
| B. Accumulate `LastFiles` | Append across the `editing → reviewed` cycle, reset on `reviewed` | Changes the display field's semantics and keeps the 5-cap; plan's writes still enter the list |
| **C. Gate the hook** | `ProcessAnalyzeHook` skips the transition and the trigger for `docs/` paths (`isDocsMarkdown`, `bus/hook.go:1795`, is the existing docs-path test — Markdown only; whether a non-Markdown file under `docs/`, such as `docs/media/*.svg`, is gated too is settled in Phase 1 against the `docs/` prefix `VerifyMovementFingerprint` uses) | Independent of A/B; the only option that fixes consequence 2 |

**Recommended: A + C.** B alone still lists at most five files and still lets a docs write into
the set.

### Key files

| File | Relevance |
|------|-----------|
| `tools/muxcode/daemon/daemon.go` | `notifyPlanOnReview` — the list source; `routeTrigger` — the batch transition; `:419` — the `reviewed` transition that carries no files |
| `tools/muxcode/bus/hook.go` | `ProcessAnalyzeHook` (`:2086`) — no window or path gate on the transition and trigger |
| `tools/muxcode/bus/workflow.go` | `WithFiles` (`:136`) replace semantics and 5-cap; display (`:255`) |
| `tools/muxcode/bus/provenance.go` | `VerifyMovementFingerprint` (`:119`) — already walks status and skips `docs/`; `RepoScopedFiles` (`:34`) |
| `scripts/test-verify-spec-refire.sh` | The harness shape the integration test reuses |

## Implementation

### Phase 1: Pin the misreport

- [x] Unit test: a workflow entry whose `LastFiles` is the spec path, with eight dirty code files in
  the repo dir → today's message names only the spec (fails after Phase 2) —
  `TestNotifyPlan_ChangedFilesIsTheLastBatch_Characterization`; `./test.sh` passed 2026-11-27 19:17
- [x] Unit test: a write under `docs/` transitions the workflow to `editing` and writes
  the trigger (fails after Phase 3) —
  `TestProcessAnalyzeHook_DocsWriteDrivesTheWorkflow_Characterization`; same run, review 0/0/0
- [x] Settle whether the path gate is all of `docs/` or `docs/**/*.md`, to match the prefix
  `VerifyMovementFingerprint` already skips; record it — **all of `docs/`**, by prefix
- [x] Confirm nothing else consumes `LastFiles` as a change set (`grep LastFiles`), and record it —
  confirmed: `notifyPlanOnReview` is the only one

### Findings for Phase 1

Recorded 2026-11-27 from the Phase 1 worker's notes (`/tmp/mux150-phase1-findings.md`, run
`1795811929`). Plan re-ran the `LastFiles` grep, read the prefix test in `bus/provenance.go` and
listed the non-Markdown files under `docs/`; all three match the notes. The two characterization
tests first ran under `./test.sh` at 19:17, and their steps were ticked then, at the run's
verification.

| Finding | Detail |
|---------|--------|
| The gate is all of `docs/`, by prefix | It matches `VerifyMovementFingerprint` (`bus/provenance.go:133`, `strings.HasPrefix(path, "docs/")`), which Phase 2's change set reuses — so a path is out of the change set exactly when it is out of the workflow. A Markdown-only gate would let a `docs/media` write regress the workflow while the git-derived list never named it |
| What non-Markdown the gate takes in | Eight files today: six samples under `docs/media/` (`.svg`, `.drawio`, `.mmd`) and two `.gitkeep`. None is code under review |
| `isDocsMarkdown` is left alone | `bus/hook.go:1791` stays the test for `CheckDocFileGuard` (edit may not author docs Markdown). It also matches `/docs/` anywhere in a path; the hook gate is a separate prefix test on the repo-relative path |
| `LastFiles` has one change-set reader | Non-test references: the field (`bus/workflow.go:100`), `WithFiles` writing it (`:141`, `:143`), the `last: …` display (`:255`–`:258`), and `notifyPlanOnReview` (`daemon/daemon.go:664`) — the only one that reads it as a change set |
| The characterization tests | `TestNotifyPlan_ChangedFilesIsTheLastBatch_Characterization` (`daemon/verify_spec_files_test.go`): a scratch repo with the eight dirty code files of 2026-09-08 and `LastFiles` set to the spec → "Changed files" is exactly the spec path; flips in Phase 2. `TestProcessAnalyzeHook_DocsWriteDrivesTheWorkflow_Characterization` (`bus/analyze_docs_gate_test.go`): a plan-window write under `docs/requirements/drafts/` writes the trigger, regresses `reviewed → editing` and becomes `LastFiles`; flips in Phase 3 |
| Side finding, out of scope, unfiled | `TestProcessAnalyzeHook_TriggerFile` (`bus/hook_test.go:1822`, session `test-analyze-trigger`) writes the real `/tmp/muxcode-analyze-test-analyze-trigger.trigger` on every suite run and never removes it: `TriggerFile` (`bus/config.go:999`) is a fixed `/tmp` path that no test override reaches. This is the re-created trigger [MUX-160](../completed/MUX-160-tmp-go-cache-leak-unclearable-pressure.md)'s Phase 1 dry run listed. The `/tmp` location is deliberate (the trigger is rewritten on every edit); the candidate defect is a test writing outside its scratch root |

### Phase 2: Derive the change set from git

- [x] Extract the status walk from `VerifyMovementFingerprint` into a shared helper that returns
  dirty non-docs repo-relative paths with each one's content hash; the fingerprint is unchanged for
  the same tree, so MUX-007's gate does not move — `dirtyCodePaths` (the walk) and `CodeChangeState`
  (path → content hash, plus HEAD), `bus/provenance.go`
- [x] Store the per-path state with the movement marker at each daemon-fired verification —
  `verify-movement.files.json` in the bus directory (`WriteVerifyChangeState`)
- [x] `notifyPlanOnReview` builds "Changed files" from the paths whose state differs from the stored
  one (every dirty non-docs path when none is stored); keep the
  `(none verified in the repo working tree)` fallback for an empty set — `ChangedSinceVerification`
- [x] Record what the list covers after a graph-owned review, which writes no marker (MUX-219) — it
  writes no state either, so the next daemon-fired list also covers what that run verified; see the
  findings
- [x] List every file — no 5-cap; if a cap is kept, name the elided count (`+N more`) — sorted, no cap
- [x] Unit test pins the source — `TestNotifyPlan_ChangedFilesIsTheGitChangeSet`, the Phase 1
  characterization flipped: all eight files, never the spec

### Findings for Phase 2

Recorded 2026-11-27 from the Phase 2 `implement` and `fix` nodes of run `1795811929`; code read by
plan. The first review returned 2 must-fix — a new untracked directory was stored collapsed as
`pkg/`, and a file clean at the last verification then edited **and committed** was omitted — and the
re-review at 19:31 passed 0/0/0. `./test.sh` passed 19:30. The phase worker's report describes the
first cut (directories hashed by their files); what follows is the tree after the fixes.

| Finding | Detail |
|---------|--------|
| What "changed since the last verification" means | The stored state is the HEAD that verification saw plus each dirty non-docs file's content hash. The list is: each file dirty now that is new or whose content moved; plus each file clean now that was dirty then, or was committed between the two HEADs, whose content no longer matches what that verification saw. So a commit of content already verified is **no** change, a revert **is**, and a file clean then and committed since always appears |
| No previous state, or one that cannot be compared | Every dirty non-docs file. That covers the first verification, an unreadable state file, and a stored HEAD that no longer resolves (rebased away) |
| Untracked files are named one by one | The change set asks git for `--untracked-files=all`. The movement fingerprint keeps git's default, where a new directory reads once as `dir/` — so its value, and MUX-007's gate, do not move |
| After a graph-owned review (MUX-219) | That review fires no daemon `verify-spec` and writes neither marker nor state. The next daemon-fired list is therefore measured from the last *daemon* verification and also names what the run's own `update-spec` already covered — more than strictly new, never less |
| A failed state write does not withhold the fire | Like the marker: the next list is then everything dirty — too much, never too little |
| An out-of-repo path cannot enter the list any more | The list is `git status` of the session repo, so the credentials-shaped config and spawn-worktree copies the old signal carried are not candidates. `RepoScopedFiles`, the filter written for them, now has **no production caller** — dead code, left for a follow-up |
| `LastFiles` is display-only again | `notifyPlanOnReview` no longer reads the workflow entry; the `last: …` rendering is its one reader |
| Cost | Every dirty non-docs file is read and hashed at each daemon-fired verification |
| The regression script needed its fixture changed | `scripts/test-verify-spec-refire.sh` now edits the repo's `bus/pane_test.go` after its base commit, since a file is listed only when git sees it dirty. Run against a muxcode built from the tree (`/tmp/mux150-refire.sh`, log `/tmp/mux150-refire.log`): **43 passed, 0 failed**. A run at 19:27 before that fixture change read 42 passed, 1 failed, on that one check. The script defaults to the muxcode on `PATH`, so it passes there only once this tree is installed |

### Phase 3: Keep docs writes out of the workflow

- [x] `ProcessAnalyzeHook`: no transition and no trigger for a path under `docs/`, whichever window
  wrote it — `inCheckoutDocs` (`bus/hook.go`), result field `DocsSkipped`;
  `TestProcessAnalyzeHook_DocsWriteLeavesTheWorkflowAlone`, the Phase 1 characterization flipped, from
  the plan and the build window
- [x] Negative control: an edit-window code write is unchanged (transition, trigger, diff cleanup),
  and a root `CLAUDE.md` write still transitions and triggers —
  `TestProcessAnalyzeHook_NonDocsWritesStillRoute`,
  `TestProcessAnalyzeHook_EditWindowCodeWriteStillCleansTheDiff`

### Findings for Phase 3

Recorded 2026-11-27 from the Phase 3 `implement` and `fix` nodes of run `1795811929`; code read by
plan. `./test.sh` passed 19:43; the review's one finding (a relative path was not cleaned, so
`./docs/x` routed and `docs/../bus/x.go` did not) was fixed and the re-review passed 0/0/0. The
refire script, on a muxcode built from the tree, 19:40: 43 passed, 0 failed.

| Finding | Detail |
|---------|--------|
| What counts as `docs/` | The `docs/` directory at the root of the **written file's own checkout**: an absolute path is resolved against `git rev-parse --show-toplevel` of its directory and its repo-relative form tested for the `docs/` prefix. A relative path is cleaned and read as repo-relative. So `tools/gen/docs/render.go` — a code directory merely named `docs` — still routes, and a path no checkout contains is not docs |
| It is not tied to the session repo | A docs write in *any* checkout is gated, not only the session's. The change set (Phase 2) reads the session repo; the two agree for the session repo, which is the case the decision is about |
| Cost | One `git rev-parse` per write whose absolute path contains `/docs/`; every other write skips the git call |
| The edit window's diff cleanup still runs for a docs write | So a preview is never left open. Only the trigger and the workflow transition are skipped |
| **Analyze no longer hears about docs writes** | The trigger file is what routes a change to the analyze role, so a `docs/` edit now produces no `analyze` event and no `analyzing` state. This is Decision 1 as worded ("neither transitions the workflow nor writes the analyze trigger"); it is recorded because it also ends analysis of documentation changes |
| Root `CLAUDE.md` and `README.md` | Not under `docs/`: they still route and still regress the workflow, and they remain in the change set |

### Phase 4: Integration test

- [x] Create `scripts/test-verify-spec-files.sh` — scratch bus, daemon and repo dir via
  `MUXCODE_SESSION_REPO_DIR`, the `test-verify-spec-refire.sh` harness — a real `muxcode watch`
  daemon (2 s debounce), writes reported through the real `muxcode hook analyze` as each window, a
  private tmux server
- [x] Test: dirty three code files across two debounce batches, complete a review → the
  `verify-spec` names all three — section A: `a.go, b.go, c.go`, two separate routes
- [x] Test: plan writes the spec between the batches → the `verify-spec` still names the three code
  files and never the spec, and the workflow state was not regressed by the docs write — section A:
  the spec write routed nothing and left the workflow entry byte-identical
- [x] Test: a second review after one more code file changes → the second `verify-spec` names that
  file and not the three the first already covered — section B: `d.go` alone
- [x] Negative control: an edit-window code write still routes to analyze and regresses to `editing`
  — section C, after showing plan's post-review spec write kept the state at `reviewed`; the write is
  to a fresh `e.go`, asserted unrouted beforehand
- [x] Negative control: exactly one `verify-spec` per review completion (MUX-007 gate) — section D:
  two for the two completions that moved something, none for a third with nothing moved
- [x] Coverage floor so a skipped section cannot report green — exactly 21 passes or exit 1
- [x] Run the script and verify all checks pass

Run through the run agent on 2026-11-27 19:52 (task `1795827096-spawn-a5769f4f-776e5afb`, log
`/tmp/mux150-files.log`, read by plan): **21 passed, 0 failed, coverage floor 21, exit 0**, against
a muxcode built from this tree (`/tmp/mux150-files.sh` sets `MUXCODE_BIN`). This replaces the 19:47
run (20 passed, floor 20): the review found that section C's "routed to analyze" check wrote `a.go`
again and so could be satisfied by section A's earlier route of the same path. Section C now writes
`e.go`, a path no earlier section touched, and first asserts it has not been routed — the added
check. Plan had read the 19:47 script and did not see this. The script defaults to
the muxcode on `PATH`, so it passes there only once this change is installed. It carries no
pre-fix control build: that its assertions can fail rests on Phase 1's characterization tests,
which asserted the old behaviour and were flipped in Phases 2 and 3.

### Docs — outside the phases

This spec has no docs phase. Written 2026-11-27 on the worker's request, after plan flagged the gap:
`docs/architecture.md` (what "Changed files" names, in the verify-spec section; the `docs/` gate, in
File Edit Event Flow) and `docs/hooks.md` (`hook analyze`), by plan from the code; `CLAUDE.md`'s
spec-verification bullet and the `test-verify-spec-files` entry, by the worker. The two docs' links
to this spec were retargeted to `requirements/completed/` at close-out. One stale line was left as
found: `docs/hooks.md` still lists per-file-type routing to test, deploy and build under
`hook analyze`, while the code routes file changes to the analyze role only.

## Out of scope

- Routing to a windowless analyze role — [`MUX-145`](../completed/MUX-145-messages-routed-to-windowless-role.md).
- What the review agent itself is told to review; this spec covers only the message plan receives.

## Status

Complete

Closed out 2026-11-27 by `55-defect-backlog-loop` run `1795811929` at 27/27. What shipped: the
`verify-spec` "Changed files" list is derived from git — the code files whose content differs from
the last daemon-fired verification, every one named, never a `docs/` path — and a write under a
checkout's `docs/` no longer writes the analyze trigger or moves the workflow. Known and recorded:
the analyze role now hears nothing about documentation edits; `RepoScopedFiles` has no production
caller; a pre-existing test writes a real `/tmp` trigger on every suite run (unfiled).

Activated 2026-11-27 by `55-defect-backlog-loop` run `1795811929` (`select` node), at 0/27. Reached
27/27 the same day: Phases 1 (pin the misreport), 2 (the change set from git), 3 (docs writes
out of the workflow) and 4 (integration test) are complete and all seven criteria are ticked. Unparked 2026-11-24 on the user's decisions above.

Filed 2026-09-08 from three `verify-spec` requests received in one session. Every code claim was
verified against this repo at filing, every timing against the lifecycle log; the line numbers
marked above were re-read 2026-11-24.
