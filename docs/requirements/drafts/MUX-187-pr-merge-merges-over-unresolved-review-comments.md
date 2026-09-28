# MUX-187: `110-pr-merge` Merges Over Unresolved Review Comments

The `110-pr-merge` builtin asks a human to approve a merge on one fact: CI is green. Its shape is
`find-pr → pr-exists → ci-watch → ci-green → merge-gate → merge → tracker`
(`bus/graph_templates.go:275-300`); nothing between finding the PR and opening the gate reads the
PR's reviews. On 2026-09-24 run `1790280483-110-pr-merge-d22797b9` merged PR #89 while Copilot's
review sat at *"Changes recommended"* with six inline comments and no replies. The gate text the
user approved — *"CI is green — approve merging the PR, deleting its branch, updating main, and
moving the tracker story (Jira) to Done"* — was true and incomplete: the commit agent reported the
open review only in its post-merge summary (*"HEADS-UP: Copilot's review said Changes recommended …
and was never addressed"*), after the branch was deleted. The findings themselves are filed as
[MUX-186](../backlog/MUX-186-pr-89-cancel-races-and-fail-open-cleanup-merged-unaddressed.md).

## Context

### Source and standard of evidence

First-hand: plan received the run's `tracker` dispatch, whose message carried the merge node's
report verbatim. Template shape verified at `bus/graph_templates.go:275-300` on 2026-09-24. Filed on
the user's instruction relayed by edit.

### Mechanism

| Step | Code | Behaviour |
|---|---|---|
| The only evidence the gate sees is CI | `ci-watch` (role `watch`, `CI-GREEN` token) → `ci-green` condition → `merge-gate` | Review state is never read |
| The gate text asserts what it knows | `merge-gate` message: "CI is green — approve merging …" | A human reading it has no reason to open the PR |
| The tool to read reviews already exists | `80-pr-review-fix`'s `read-comments` node (`graph_templates.go:127`): commit/`pr-read`, lists every unresolved actionable comment with id and `file:line`, emits `NO-ACTIONABLE-COMMENTS` when there are none; `no-comments` condition at `:128` | Nothing in `110-pr-merge` reuses it |
| The merge itself is unconditional past the gate | `merge` (commit/`commit`): `gh pr merge`, delete branch, checkout main, pull | The branch is gone before anyone reads the review |

This is the [MUX-183](../completed/MUX-183-phase-commit-ready-recredits-shipped-phases.md) shape on
a different gate: a human backstop that is *blind by construction* because the prompt omits the
evidence the decision needs. MUX-167's rule — **ask before the guard, and ask only what the
evidence supports** — applies.

### Blast radius

- Every `110-pr-merge` run on a PR with an open review: the review is silently discarded on merge,
  and the branch deletion removes the easiest place to answer it.
- Not limited to Copilot — a human reviewer's "changes requested" is equally invisible to the gate,
  unless branch protection refuses the merge (this repo's did not).

## Requirements

### Acceptance criteria

- [x] `110-pr-merge` reads the PR's unresolved review comments before `merge-gate` and the run **stops** (or routes to a `stuck-gate`-style hold) when any are actionable, naming each with id and `file:line`; it reaches `merge-gate` only on `NO-ACTIONABLE-COMMENTS` — `read-comments → no-comments` after `pr-exists`; the false edge ends at the `open-comments` hold (no outgoing edges, message carries `${output:read-comments}`); `TestPRMergeReviewReadRouting` (`graph_exec_test.go`) holds before `ci-watch` with the comment's `file:line` in the pending approval, and `TestPRMergeTemplate` pins `ci-watch` reachable only from `no-comments` success
- [x] The `merge-gate` text states what was checked — CI green **and** no unresolved review comments — so the approval means what it says — asserted in `TestPRMergeTemplate`
- [x] The read node reuses `80-pr-review-fix`'s `read-comments` message and token verbatim (one definition, or a shared constant), so the two templates cannot drift on what "actionable" means — `prReviewReadNodesJSON` + `NoActionableCommentsToken` (`graph_templates.go`) spliced into both; `TestPRReviewFixQuestionNodesDeclareExitConvention` fails on any drift between the two
- [x] A human "Changes requested" review with zero inline comments is also actionable (review state, not only comment count) — the shared read message says so; `test-pr-merge-review-gate.sh` section 2 asserts the **dispatched** wording counts it and that a bare `CHANGES_REQUESTED` reply holds at `open-comments` naming the review. The stub decides the token, so this proves routing and instruction, not a live agent's judgment
- [x] **Negative control:** a PR with resolved threads only, or with non-actionable bot chatter, still reaches `merge-gate` — script section 3: `NO-ACTIONABLE-COMMENTS` + `CI-GREEN` reaches `merge-gate` waiting, `open-comments` never opens, `merge` never dispatched unapproved
- [x] `docs/agent-bus.md` and `docs/architecture.md` describe the new shape; the 12-row builtin table stays accurate — Phase 2, 2026-09-28, checked against `graph_templates.go` at `c7ad449`+tree; table still 12 rows
- [x] `bash scripts/test-pr-merge-review-gate.sh` passes — 24 passed, 0 failed, exit 0 (run agent `1790630937-run-f3fea2df`, on the file as it stands)

### Technical approach

Insert `read-comments → no-comments` between `pr-exists` and `ci-watch` (cheapest first: a comment
read is seconds, a CI watch is minutes), with the `no-comments` false edge to a terminal
`open-comments` node that names the comments and ends the run. Alternative: keep the run alive and
put the comment list into the gate text so the human can still choose to merge — the run should then
end in a *different* gate message so an approval cannot be mistaken for the clean one. Decide in
Phase 1; the default is to stop, because `80-pr-review-fix` is the road for open comments.

### Key files

| File | Role |
|---|---|
| `tools/muxcode/bus/graph_templates.go:127-128,275-300` | `read-comments`/`no-comments` in `80-pr-review-fix`; `110-pr-merge` |
| `tools/muxcode/bus/graph_test.go:389` | token-contract test for `find-pr`/`read-comments` — extend to `110-pr-merge` |
| `docs/agent-bus.md:1760`, `docs/architecture.md:469` | where the token and the template shapes are documented |
| `scripts/test-pr-merge-review-gate.sh` | new |

## Implementation

### Phase 1: Template

- [x] Decide stop-vs-annotate ([Decision 1](#decision-1--stop-or-annotate)); record it here — **stop**, the spec's default, as implemented 2026-09-28; no user decision was relayed (see Decision 1)
- [x] Add `read-comments` and `no-comments` to `110-pr-merge` before `ci-watch`; false edge to a terminal node that lists the comments — terminal node is the `open-comments` `wait_human` hold: approving it only acknowledges and ends the run, and its text points at `80-pr-review-fix`
- [x] Gate text updated to state both checks — "CI is green and the PR has no unresolved review comments"
- [x] Share the message/token with `80-pr-review-fix` (constant or generator) and extend the `graph_test.go:389` contract test — the contract test now iterates both templates and asserts identical `read-comments` messages. The shared message also gained the `CHANGES_REQUESTED` and resolved/bot-chatter clauses, so `80-pr-review-fix`'s read changed with it

### Phase 2: Docs

- [x] `docs/agent-bus.md` `110-pr-merge` shape and the gate note; `docs/architecture.md` builtin table row — done by hand on the user's request (relayed by edit) after graph run `1790629144` failed on it: `agent-bus.md` gains the full shape, the `open-comments` hold and a **shared review read** paragraph (incl. the outdated-thread rule added after Phase 1's first review); `architecture.md` row 110 and a cross-reference in the `80-pr-review-fix` paragraph

### Phase 3: Integration test

- [x] Create `scripts/test-pr-merge-review-gate.sh` — hermetic: a stub commit agent whose `pr-read` reply is scripted, scratch daemon — stub commit **and** watch agents, scratch bus/repo/HOME/lifecycle log, real `110-pr-merge` builtin; every call runs from the scratch repo so a project `.muxcode/graphs` override cannot mask the builtin (review `1790630837` should-fix, fixed)
- [x] Test: reply lists one actionable comment → run ends before `ci-watch`, output names the comment id and `file:line`, `merge-gate` never opens — section 1: holds at `open-comments`, prompt and edit's gate request carry the id, `file:line` and `80-pr-review-fix`; no `ci-watch` dispatch within 5 s; no `merge-gate` marker
- [x] **Negative control:** reply contains `NO-ACTIONABLE-COMMENTS` → run reaches `ci-watch` — section 3, and on through `merge-gate`
- [x] Test: gate text (from `graph status`) contains both the CI and the review clause — `graph status --json` node message plus the pending prompt
- [x] Coverage floor; run and record counts here — exactly 23 checks (equality); the floor is the 24th pass. 2026-09-28: first run `1790630712-run-d2561bac` 24/0 predates the isolation fix (edited 17:27:51); re-run `1790630937-run-f3fea2df` at 17:28:57 on the fixed file **24 passed, 0 failed, exit 0**; review `1790631028` 0/0/0. Not covered by the script: the outdated-thread clause of the shared read

## Open decisions

### Decision 1 — stop or annotate

Stop the run and hand the comments to `80-pr-review-fix` (clean separation, one more run to start),
or carry the list into the gate text and let the human merge anyway (one run, but an approval on a
gate that lists open comments must be distinguishable in the audit row from a clean approval).

**Resolved by default (stop), 2026-09-28** — Phase 1 implemented the spec's default: open comments end
at an `open-comments` hold that names them and merges nothing. No user decision was relayed; the user
may still choose annotate.

## Out of scope

- Addressing PR #89's comments — MUX-186.
- Branch-protection settings on GitHub — a repo setting, not a template.

## Time Tracking

| Branch | Active time | Last updated |
|--------|-------------|--------------|
| MUX-187-pr-merge-merges-over-unresolved-review-comments | 30m | 2026-09-28 17:30 |

## Status

In Progress — all three phases verified, 17/17 (Phase 1 `c7ad449`; Phases 2–3 uncommitted, 2026-09-28); close-out pending

Filed 2026-09-24 on the user's instruction relayed by edit, from run `1790280483-110-pr-merge`
merging PR #89 over an unanswered Copilot review; template shape verified the same day. Started
2026-09-28 on branch `MUX-187-pr-merge-merges-over-unresolved-review-comments`, relayed by edit.
