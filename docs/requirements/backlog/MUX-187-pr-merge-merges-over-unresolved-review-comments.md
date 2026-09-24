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
[MUX-186](./MUX-186-pr-89-cancel-races-and-fail-open-cleanup-merged-unaddressed.md).

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

- [ ] `110-pr-merge` reads the PR's unresolved review comments before `merge-gate` and the run **stops** (or routes to a `stuck-gate`-style hold) when any are actionable, naming each with id and `file:line`; it reaches `merge-gate` only on `NO-ACTIONABLE-COMMENTS`
- [ ] The `merge-gate` text states what was checked — CI green **and** no unresolved review comments — so the approval means what it says
- [ ] The read node reuses `80-pr-review-fix`'s `read-comments` message and token verbatim (one definition, or a shared constant), so the two templates cannot drift on what "actionable" means
- [ ] A human "Changes requested" review with zero inline comments is also actionable (review state, not only comment count)
- [ ] **Negative control:** a PR with resolved threads only, or with non-actionable bot chatter, still reaches `merge-gate`
- [ ] `docs/agent-bus.md` and `docs/architecture.md` describe the new shape; the 12-row builtin table stays accurate
- [ ] `bash scripts/test-pr-merge-review-gate.sh` passes

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

- [ ] Decide stop-vs-annotate ([Decision 1](#decision-1--stop-or-annotate)); record it here
- [ ] Add `read-comments` and `no-comments` to `110-pr-merge` before `ci-watch`; false edge to a terminal node that lists the comments
- [ ] Gate text updated to state both checks
- [ ] Share the message/token with `80-pr-review-fix` (constant or generator) and extend the `graph_test.go:389` contract test

### Phase 2: Docs

- [ ] `docs/agent-bus.md` `110-pr-merge` shape and the gate note; `docs/architecture.md` builtin table row

### Phase 3: Integration test

- [ ] Create `scripts/test-pr-merge-review-gate.sh` — hermetic: a stub commit agent whose `pr-read` reply is scripted, scratch daemon
- [ ] Test: reply lists one actionable comment → run ends before `ci-watch`, output names the comment id and `file:line`, `merge-gate` never opens
- [ ] **Negative control:** reply contains `NO-ACTIONABLE-COMMENTS` → run reaches `ci-watch`
- [ ] Test: gate text (from `graph status`) contains both the CI and the review clause
- [ ] Coverage floor; run and record counts here

## Open decisions

### Decision 1 — stop or annotate

Stop the run and hand the comments to `80-pr-review-fix` (clean separation, one more run to start),
or carry the list into the gate text and let the human merge anyway (one run, but an approval on a
gate that lists open comments must be distinguishable in the audit row from a clean approval).

## Out of scope

- Addressing PR #89's comments — MUX-186.
- Branch-protection settings on GitHub — a repo setting, not a template.

## Status

Backlog

Filed 2026-09-24 on the user's instruction relayed by edit, from run `1790280483-110-pr-merge`
merging PR #89 over an unanswered Copilot review; template shape verified the same day. Not started.
