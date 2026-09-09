# A Run Builds on a Stale Base — Branch Freshness Is a Note, Not a Gate

`spec-to-pr` starts at `implement` and nothing on the way asks how far the branch has fallen behind
`main`. On 2026-09-09 a branch cut on July 10 and idle for six weeks was resumed against a subsystem
that eight squash-merges had rebuilt in the meantime; plan had written "8 behind, sync needed" into
the spec that morning, but as a before-PR condition, and a spec note is prose — no instrument acts on
it. The run implemented two phases into files `main` had restructured or deleted, the build and test
nodes passed each lap on the branch's own base, and the first thing that objected was `cdk diff`
against an environment deployed from `main`: 28 destroys, then a rebase preview with 9 content
conflicts and 2 modify/delete cases whose resolution is a re-home, not a merge. The lesson the other
session wrote down — *sync before the first new phase, not before the PR* — is a rule for the graph
to enforce, not for a spec to remember.

## Context

### Observed (session `is-advising-gateway`, branch `PROMGT-623-student-data-contact`, run `1788966543-spec-to-pr-28374348`)

Timeline re-verified read-only in that repo's history and its session log; the account is the
subsession's own, relayed by screenshot.

| When | What | Source |
|------|------|--------|
| 2026-07-10 | `d13fb90` PR #89 merges; the branch is cut here | `git log origin/main` |
| 2026-07-28 | `22e1337` Phase 1 lands on the branch; nothing for six weeks | branch history |
| 08-03 → 09-01 | `main` takes eight squash-merges #90–#97 (`ae612f3`, `4608973`, `938628f`, `bd71522`, `2313a50`, `91c9fbf`, `0234192`, `7a6acee`); six touch this story's files | `git log --since=2026-07-10 origin/main` |
| 2026-08-12 | `bd71522` PR #93 — the decisive one: **deletes** `student_data_query/database/query_builder.py`, moves `DedupAuditStore` to `resources/lambda/shared/dynamo.py`, deletes `jest.config.js` | PR #93 |
| 09-09 10:40 | plan records in the spec: "`main` has since advanced 8 squash-merges past that base (#90–#97; verified …)" — context for the PR, not a precondition for building | `PROMGT-623-student-data-contact-update.md:19` |
| 11:09:03 | `graph-run-created … spec-to-pr-28374348 started by user` — first node `implement` | session log |
| 11:42:08 | commit gate approved → `dee54a1` 11:43: Phase 2 writes the exclusion builder **into `query_builder.py`**, deleted upstream four weeks earlier | session log, branch history |
| 13:03:36 | phase gate approved → `73e1b60`: Phase 3 edits the CDK stack and jest test against the July shape #93/#94/#96/#97 had reworked | session log, branch history |
| 13:12 | `cdk diff` against dev01 (deployed from `main`): **28 destroys** — the first signal | subsession account; `cdk.out` purge rows 13:08–13:10 |
| 13:14 | rebase preview: 9 content conflicts, 2 modify/delete (`query_builder.py`, `jest.config.js`) | subsession account |
| 13:48 → now | rebase in progress: `UU sql-templates.json`, `DU query_builder.py`; Phase 1 replayed as `6405646` | `git status` in that repo |

Every lap's build and test node passed: the tests were file-scoped to the branch's own base, and the
build node was — in the subsession's words — hollow (a separate defect, [MUX-148](./MUX-148-node-outcome-reads-command-ran-as-task-done.md)
/ [MUX-154](./MUX-154-codex-status-line-closes-tracked-tasks.md) family). Neither compares anything
with `main`, so neither could have caught this even working perfectly.

### Mechanism — verified in code

- `bus/graph_templates.go:34–36, 63` — `spec-to-pr` starts at `implement`, and `loop-check` re-enters
  `implement` directly. No node in any builtin template reads the branch's relation to its upstream.
- `bus/conditions.go:16–19, 89–109, 475` — `ChainContext` carries `ChangedFiles` (`git diff
  --name-only HEAD`) and `Branch`; the eleven condition types have no upstream predicate;
  `PopulateGitInfo` never looks at `origin/*`. A template author cannot express "behind main" today.
- `bus/console.go:1268–1274` — the console computes ahead/behind for display, and **against
  `@{upstream}`** — the branch's own remote, not `main`. It would have read 0 behind here.
- `agents/git-manager.md:23, 179` — the sync recipe exists (`git fetch origin main && git rebase
  origin/main`) and the commit agent reports ahead/behind after operations. A recipe is run when
  asked; nothing asks.
- `bus/graph.go:605` — `gateTextGitPrefixes` already lists `rebase`: a sync node fits the existing
  authority rule (a git mutation downstream of a `wait_human` gate) without a new gate class.
- The spec note is the same failure class `bus/atlassian_authority.go`'s comment names for Jira
  writes — "prose alone does not hold this line": plan's verify pass captured the staleness exactly,
  and the run never read it.

### Scope boundary

The safeguard is a **gate with numbers in it**, not an auto-rebase: a rebase is a git mutation and
stays a gated commit-agent action; a conflict parks the run, it never leaves a half-rebase. Not in
scope: making build/test nodes run against `main` (that is what the sync buys), the hollow build node
(MUX-148/154/161), or what the other session does with its current rebase.

## Requirements

### Acceptance criteria

- [ ] A run cannot begin implementing on a branch behind `origin/main` whose upstream commits touch files the spec names without a person seeing the numbers: `spec-to-pr` parks at a `sync-gate` whose text carries the behind count, the upstream merges touching spec-named files (with the files), and the age of the last fetch — before the first `implement`
- [ ] The check re-runs on every loop lap (`loop-check` → freshness → `implement`), so a run that goes stale mid-way parks too
- [ ] A fresh branch — 0 behind, or behind with no spec-file overlap under the threshold — proceeds to `implement` with a `branch-freshness` lifecycle row recording the numbers: no gate, and no silence
- [ ] Freshness is measured against a **fetched** `origin/main` from `merge-base HEAD origin/main`, never `@{upstream}`; a fetch that fails or a ref older than the threshold trips the gate rather than reading as fresh (fail closed)
- [ ] The sync is a gated commit-agent mutation — fetch, rebase onto `origin/main`, report — and a conflict aborts the rebase and parks the run with the conflict list, never a half-rebase in the tree
- [ ] `graph validate` rejects a builtin template whose first `spawn` or `implement`-role node is reachable from `start` — or from a loop re-entry — without crossing a freshness check
- [ ] The 2026-09-09 shape replayed in a scratch repo (branch cut at a base; `main` advanced by merges that delete a file the branch's phase writes) parks at the gate naming the deleted file, instead of building
- [ ] Docs: `docs/architecture.md` graph section, `docs/agent-bus.md` condition and template references, `CLAUDE.md` conditional-chains constraint (the condition count), `agents/git-manager.md` sync action

### Technical approach

**Primary — freshness as a graph primitive, sync as a gated node.** A `BranchFreshness(repo, spec)`
function in `bus/` runs `git fetch origin main` (bounded timeout; failure → stale), then reports
`behind`/`ahead` from `merge-base HEAD origin/main`, the files changed in `merge-base..origin/main`,
their intersection with the files the spec names (the Key files table and the current phase's items,
via the `spec_items.go` parsers), and the fetch age. Two new condition types expose it:
`branch_behind` (`{"branch_behind": {"max": N}}`) and `upstream_touches_spec_files`. `spec-to-pr`
gains `refresh` (condition: fresh → `implement`, stale → `sync-gate`), `sync-gate` (`wait_human`,
text interpolated with the numbers and files), and `sync` (send to commit: "fetch origin and rebase
onto origin/main; on conflict abort and report the list") looping back to `refresh`; `loop-check` and
`stuck-gate` re-enter through `refresh`, not `implement`. Every evaluation writes a
`branch-freshness` lifecycle row. Thresholds: gate on any overlap; gate on `behind ≥
MUXCODE_FRESHNESS_MAX_BEHIND` without overlap (default owed — see Notes); gate on fetch age above
`MUXCODE_FRESHNESS_MAX_FETCH_AGE`.

**Where the fetch runs.** The daemon already runs git for `PopulateGitInfo`, so the condition can
fetch itself — no network from a sandboxed agent, no extra gate for a read. If a repo's fetch needs
credentials the daemon lacks, the failure is a stale verdict and the gate text says why; the commit
agent's `sync` node then fetches for real.

**Rejected — teach plan to enforce the note.** Plan could turn "N behind" into a spec-level marker a
condition reads. That is prose with a different font: the instrument that acts must read git, not a
document about git.

**Rejected — auto-rebase when stale.** Fast for the common case and exactly the mutation the gate
rule exists to put in front of a person; the 2026-09-09 rebase is a re-home with conflicts, which no
run should attempt unattended.

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/graph_templates.go` | `spec-to-pr` (34–66): `refresh` / `sync-gate` / `sync` nodes and the loop re-entry through `refresh` |
| `tools/muxcode/bus/conditions.go` | `ChainContext` (16), the condition switch (89–109), `PopulateGitInfo` (475) — the two new types |
| `tools/muxcode/bus/graph.go` | validator (568–577) — the freshness-before-implement rule; `gateTextGitPrefixes` (605) already covers `rebase` |
| `tools/muxcode/bus/spec_items.go` | spec parsers reused for the Key files table and phase items |
| `tools/muxcode/bus/console.go` | ahead/behind display (1268) — switch to the same `origin/main` measure |
| `agents/git-manager.md` | the `sync` action: fetch, rebase, abort-on-conflict, report |
| `docs/architecture.md`, `docs/agent-bus.md`, `CLAUDE.md` | graph section, condition/template references, the constraint bullet |
| `scripts/test-graph-orchestrator.sh` | pattern for the scratch-repo integration test |

## Implementation

### Phase 1: The freshness facts

- [ ] `BranchFreshness` in `bus/`: fetch with timeout, `merge-base` counts, upstream file list, spec-file intersection, fetch age; typed result with a one-line summary for gate text and lifecycle rows
- [ ] Unit tests on scratch repos: fresh; behind without overlap; behind with overlap; upstream deleted a file the phase names; fetch failure → stale (negative control: a result computed from `@{upstream}` must not pass the overlap case)
- [ ] `console.go` ahead/behind reads the same measure

### Phase 2: The gate in the graph

- [ ] Condition types `branch_behind` and `upstream_touches_spec_files` in `conditions.go`, with `ChainContext` carrying the freshness result lazily
- [ ] `spec-to-pr`: `refresh` → (`implement` | `sync-gate` → `sync` → `refresh`); `loop-check` and `stuck-gate` re-enter via `refresh`; gate text interpolates the numbers and files
- [ ] `branch-freshness` lifecycle row on every evaluation (fresh or stale, with the numbers)
- [ ] `graph validate`: first `spawn`/implement node reachable without a freshness check → error naming the node; every builtin template passes
- [ ] `agents/git-manager.md`: the `sync` action — fetch, rebase onto `origin/main`, `--abort` on conflict, report the list and the ahead/behind after

### Phase 3: Docs

- [ ] `docs/architecture.md` graph section: the freshness gate, with the 2026-09-09 run as the incident; `docs/agent-bus.md`: the two condition types and the template's new nodes
- [ ] `CLAUDE.md` conditional-chains constraint: the condition count and the freshness rule in one clause

### Phase 4: Integration test

- [ ] `scripts/test-branch-freshness-gate.sh` (hermetic: scratch bare `origin`, scratch clone, scratch `BUS_SESSION`, commit agent stubbed): replay the incident shape — branch cut, `main` advanced by merges that delete a file the phase writes — and assert the run parks at `sync-gate` with the file named and a `branch-freshness` row
- [ ] Fresh branch → `implement` dispatched with no gate and a row recording 0 behind (negative control for the gate)
- [ ] Approve the gate → stub sync rebases → `refresh` passes → `implement` dispatched; a conflicting sync → run parks with the conflict list and a clean tree (`git status` shows no rebase in progress)
- [ ] Run the script and record pass/fail counts in this spec

## Notes

- Filed 2026-09-09 13:58 by plan on the user's direct request, from the subsession's account
  (screenshot) with the timeline re-verified read-only against `~/Repos/pkh/is-advising-gateway`
  (branch history, `origin/main` since July 10, the in-progress rebase) and that session's log (run
  `28374348` started 11:09:03 at `implement`; commit gates 11:42:08 and 13:03:36).
- Decision owed: the no-overlap threshold. Gating every run that is 1 behind on an active repo
  would make the gate noise; gating only on overlap misses a stale toolchain or config the spec does
  not name. A default of 5 with overlap always gating is the proposal; the row records the numbers
  either way so the threshold can be tuned from evidence.
- Related: [MUX-144](./MUX-144-wait-human-gate-openable-by-any-agent.md) (the gate the sync sits
  behind); [MUX-148](./MUX-148-node-outcome-reads-command-ran-as-task-done.md) and
  [MUX-154](./MUX-154-codex-status-line-closes-tracked-tasks.md) (why the build node's green meant
  nothing here); [MUX-132](../completed/MUX-132-graph-retry-launders-gate-approval.md) (single-use
  approvals — a re-entered `sync-gate` needs a fresh one); [MUX-165](./MUX-165-gated-jira-write-declined-by-requester-rule.md)
  (the other consent instrument the graph carries, filed today).

## Status

**Backlog** — 0/22. Filed 2026-09-09 13:58.
