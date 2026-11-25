# A Run Builds on a Stale Base — Branch Freshness Is a Note, Not a Gate

**Tracking:** [mkober/muxcode#127](https://github.com/mkober/muxcode/issues/127)

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
build node was — in the subsession's words — hollow (a separate defect, [MUX-148](../completed/MUX-148-node-outcome-reads-command-ran-as-task-done.md)
/ [MUX-154](../completed/MUX-154-codex-status-line-closes-tracked-tasks.md) family). Neither compares anything
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

## Decisions (user, 2026-11-24)

The user's explicit choice, relayed by edit. The spec was parked on one decision it marked "owed":
the behind-count at which a branch with no spec-file overlap is gated.

| Question | Decision |
|----------|----------|
| No-overlap threshold | **Gate at 5 commits behind `origin/main` by default**, configurable through `MUXCODE_FRESHNESS_MAX_BEHIND` |
| Overlap | **Always gates**, whatever the behind count — one upstream commit touching a spec-named file is enough |

So a branch 1–4 behind with no overlap proceeds with a `branch-freshness` row; 5 or more behind
gates; any overlap gates. The row records the numbers either way, so the threshold can be tuned
from evidence.

## Requirements

### Acceptance criteria

- [x] A run cannot begin implementing on a branch behind `origin/main` whose upstream commits touch files the spec names without a person seeing the numbers: `spec-to-pr` parks at a `sync-gate` whose text carries the behind count, the upstream merges touching spec-named files (with the files), and the age of the last fetch — before the first `implement`
- [x] The check re-runs on every loop lap (`loop-check` → freshness → `implement`), so a run that goes stale mid-way parks too
- [x] A fresh branch — 0 behind, or fewer than `MUXCODE_FRESHNESS_MAX_BEHIND` (default 5) behind with no spec-file overlap — proceeds to `implement` with a `branch-freshness` lifecycle row recording the numbers: no gate, and no silence
- [x] A branch at or past the threshold gates with no overlap at all; a branch 1 behind gates when that one commit touches a spec-named file; `MUXCODE_FRESHNESS_MAX_BEHIND` moves the first and never the second
- [x] Freshness is measured against a **fetched** `origin/main` from `merge-base HEAD origin/main`, never `@{upstream}`; a fetch that fails or a ref older than the threshold trips the gate rather than reading as fresh (fail closed)
- [x] The sync is a gated commit-agent mutation — fetch, rebase onto `origin/main`, report — and a conflict aborts the rebase and parks the run with the conflict list, never a half-rebase in the tree
- [x] `graph validate` rejects a builtin template whose first `spawn` or `implement`-role node is reachable from `start` — or from a loop re-entry — without crossing a freshness check — met 2026-11-25 by Phases 5 and 6: the rule holds every builtin, and the script's section 7 shows 55, 60, 80 and 90 each rejected with its check spliced out
- [x] The 2026-09-09 shape replayed in a scratch repo (branch cut at a base; `main` advanced by merges that delete a file the branch's phase writes) parks at the gate naming the deleted file, instead of building
- [x] Docs: `docs/architecture.md` graph section, `docs/agent-bus.md` condition and template references, `CLAUDE.md` conditional-chains constraint (the condition count), `agents/git-manager.md` sync action

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
MUXCODE_FRESHNESS_MAX_BEHIND` without overlap (default 5 — the user's decision of 2026-11-24); gate on fetch age above
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

- [x] `BranchFreshness` in `bus/`: fetch with timeout, `merge-base` counts, upstream file list, spec-file intersection, fetch age; typed result with a one-line summary for gate text and lifecycle rows
- [x] Unit tests on scratch repos: fresh; 4 behind without overlap (fresh); 5 behind without overlap (stale); 1 behind with overlap (stale); upstream deleted a file the phase names; fetch failure → stale (negative control: a result computed from `@{upstream}` must not pass the overlap case)
- [x] `MUXCODE_FRESHNESS_MAX_BEHIND` read through the config layer (default 5); an override of 2 gates at 2 behind, and an override of 100 still gates on overlap
- [x] `console.go` ahead/behind reads the same measure

### Phase 2: The gate in the graph

- [x] Condition types `branch_behind` and `upstream_touches_spec_files` in `conditions.go`, with `ChainContext` carrying the freshness result lazily
- [x] `spec-to-pr`: `refresh` → (`implement` | `sync-gate` → `sync` → `refresh`); `loop-check` and `stuck-gate` re-enter via `refresh`; gate text interpolates the numbers and files
- [x] `branch-freshness` lifecycle row on every evaluation (fresh or stale, with the numbers)
- [x] `graph validate`: first `spawn`/implement node reachable without a freshness check → error naming the node; every builtin template passes
- [x] `agents/git-manager.md`: the `sync` action — fetch, rebase onto `origin/main`, `--abort` on conflict, report the list and the ahead/behind after

### Phase 3: Docs

- [x] `docs/architecture.md` graph section: the freshness gate, with the 2026-09-09 run as the incident; `docs/agent-bus.md`: the two condition types and the template's new nodes
- [x] `CLAUDE.md` conditional-chains constraint: the condition count and the freshness rule in one clause
- [x] `docs/configuration.md`: `MUXCODE_FRESHNESS_MAX_BEHIND` (default 5; overlap gates regardless) and `MUXCODE_FRESHNESS_MAX_FETCH_AGE`

### Phase 4: Integration test

- [x] `scripts/test-branch-freshness-gate.sh` (hermetic: scratch bare `origin`, scratch clone, scratch `BUS_SESSION`, commit agent stubbed): replay the incident shape — branch cut, `main` advanced by merges that delete a file the phase writes — and assert the run parks at `sync-gate` with the file named and a `branch-freshness` row
- [x] Fresh branch → `implement` dispatched with no gate and a row recording 0 behind (negative control for the gate)
- [x] Threshold: 4 behind with no overlap → no gate, row records 4; 5 behind with no overlap → `sync-gate`; with `MUXCODE_FRESHNESS_MAX_BEHIND=100`, 1 behind with overlap still parks at `sync-gate`
- [x] Coverage floor so a skipped section cannot report green
- [x] Approve the gate → stub sync rebases → `refresh` passes → `implement` dispatched; a conflicting sync → run parks with the conflict list and a clean tree (`git status` shows no rebase in progress)
- [x] Run the script and record pass/fail counts in this spec — 2026-11-24 19:03, run agent: exit 0, **27 passed, 0 failed (floor 27)**, sections 0–6 (request `1795565004-spawn-ebfdff5a-cfc3262e`, reply `1795565036-run-0c2a8b53`; watch read the same from `/tmp/test-branch-freshness-gate.log`). The commit agent is played by the real `muxcode sync-main`, not a stub rebase. Re-run 19:11 after the acceptance worker's criterion-1 fix: exit 0, **28 passed, 0 failed (floor 28)** (reply `1795565507-run-d1dbc951`)

### Phase 5: Widen the gate to every builtin

Added 2026-11-25 on the user's decision on acceptance criterion 7 (see Notes): the criterion stands
as written, so the rule and the gates reach every builtin that implements work.

- [x] `validateFreshness` applies to every builtin template, not `requires_spec` ones alone: an `implementsWork` node reachable from `start` or a loop re-entry without crossing a freshness check is an error naming the template and the node
- [x] `55-defect-backlog-loop`: a freshness check between `activated` and `implement`, crossed again on the `phases-left` re-entry and on each new defect; the measure is `main` itself against `origin/main`; a stale result parks at a `wait_human` sync gate carrying the numbers
- [x] `55-defect-backlog-loop` sync: bring `main` up to `origin/main` with the run's uncommitted work in the tree (the activated spec move, earlier phases) — `muxcode sync-main` refuses an unsettled tree today, so this road is designed here; a conflict parks with the conflict list and nothing half-applied
- [x] `80-pr-review-fix` and `90-ci-fix`: a freshness check between `fix-gate` and `fix`, measuring the PR branch against `origin/main`; stale parks at a sync gate whose approval rebases through the commit agent and re-enters through the check
- [x] `60-integration-suite`: the same check before its `fix` node, measuring the current branch against `origin/main`
- [x] `upstream_touches_spec_files` with no active spec (60, 80, 90) is defined and pinned by a unit test — it neither gates every run as unmeasured nor passes an overlap it never measured
- [x] Every builtin passes `graph validate`; a unit test removes the check from each of 55, 60, 80 and 90 in turn and requires validation to fail naming the node (negative control); the template and loop-cap tests follow the new nodes
- [x] Docs: `docs/architecture.md` and `docs/agent-bus.md` (the gate per template, and the Node time limits table for any new node), and `CLAUDE.md`'s freshness clause, which names `50-spec-to-pr` and `requires_spec` alone

### Phase 6: Integration test — the widened gate

- [x] `scripts/test-branch-freshness-gate.sh`: a real `55-defect-backlog-loop` run with scratch `main` 5 behind its `origin/main` parks at the sync gate with a stale `branch-freshness` row and no implement dispatched; negative control — a fresh `main` dispatches implement with no gate
- [x] A real `80-pr-review-fix` or `90-ci-fix` run whose PR branch is stale parks before `fix`; negative control — a fresh branch reaches `fix` with a `0 behind` row — built on `90-ci-fix` with no active spec (section 9); 80 is covered by validation only
- [x] `graph validate` on a copy of a builtin with its freshness check removed exits non-zero naming the node; the shipped builtins all validate — section 7 does both for 55 (`refresh`), 60, 80 and 90, and section 0 validates 50; the remaining builtins and 55's `pr-refresh` bypass are covered by the unit tests (`TestEveryBuiltinPassesFreshnessRule`, `TestWidenedBuiltinsFailWithoutTheirCheck`), not the script
- [x] Coverage floor raised to cover the new sections — 28 → 62 (`EXPECTED_PASS=62`)
- [x] Run the script through the run agent and record pass/fail counts in this spec — 2026-11-25 11:41, run agent: exit 0, **62 passed, 0 failed (floor 62)**, sections 0–9 (request `1795624814-spawn-a018cf09-8ab68a68`, reply `1795624880-run-8f4c8d75`; the run agent's hook row and `/tmp/test-branch-freshness-gate.log` read the same). Section 8's approved sync is the real `muxcode sync-main` fast-forwarding `main` with the uncommitted work carried

## Notes

- **User decision on acceptance criterion 7 (2026-11-25, relayed by edit): widen; the criterion
  stays as written.** `graph validate` must reject any builtin whose implement or fix path
  bypasses a freshness check, so `55-defect-backlog-loop` and the PR-fix templates 60, 80 and 90
  get freshness gates in this spec, not a later one. For 55 the measure is `main` itself behind
  `origin/main`; for 80 and 90 it is the PR branch against `origin/main`. Phases 5 and 6 carry the
  work; criterion 7 is ticked only once they are built and verified. This supersedes the scope
  recorded under "Phase 2 as built" below, where those four templates were deliberately outside
  the rule. Plan's additions, not in the decision and flagged for the worker: 60 was named in the
  decision without a measure, and is written as the current branch against `origin/main`; the 55
  sync has no road today (`sync-main` refuses an unsettled tree, and 55 implements uncommitted);
  and 60, 80 and 90 hold no active spec for the overlap condition to read.
- Phase 5 as built (worker's report `/tmp/mux166-phase5-implement.md`, 2026-11-25, read against
  the tree by plan; build, `./test.sh` (2 modules passed) and a review with no findings preceded
  this verify). It supersedes "Phase 2 as built" below wherever the two differ:
  - `validateFreshness` holds every template carrying a builtin's name — a user or project
    template shadowing one included — and any `requires_spec` template. It now follows every
    edge, failure edges too (60's `suite` → `suite-failed` → `fix`); a loop re-entry root is the
    target of a capped success edge whose source is not itself a worker. The error names the node
    and the root it was reached from; the template is the one being validated.
  - The check is one shared builder (`freshnessCheckNodes`/`freshnessCheckEdges`): `refresh`,
    `sync-gate`, `sync`, `sync-conflict`, both re-measure edges capped at 3. 55 carries it twice —
    before `implement` (re-crossed by `phases-left`, which resets both sync loops per phase) and,
    not asked for by this phase but required by the widened rule, as `pr-refresh` before `pr-fix`.
  - The 55 sync road is `fastForwardMain` (`bus/sync_main.go`): `muxcode sync-main` on `main`
    never rebases, it runs `git merge --ff-only origin/main` carrying the uncommitted work, and
    refuses with nothing changed and no stash when `main` holds local commits or an uncommitted
    or untracked file is one the upstream changes touch (those files listed, `EXIT=1`).
  - With no active spec the overlap reads `branchChangedFiles` (`Freshness.OverlapSource`
    `branch`), pinned by `TestRefreshWithoutSpecMeasuresBranchFiles`. A 55 run holds an active
    spec, so its overlap is still the spec-named files.
  - Negative control: `TestWidenedBuiltinsFailWithoutTheirCheck` bypasses each of 55 `refresh`,
    55 `pr-refresh`, 60, 80 and 90 in turn. The review fix also changed `graph validate` to allow
    acyclic reset chains and refuse only a cycle (`resetsReach`), which the 55 per-phase reset needs.
  - No Phase 5 integration script exists yet — Phase 6 adds the sections. The existing
    `scripts/test-branch-freshness-gate.sh` was re-run as a regression: exit 0, 28 passed, 0
    failed, floor 28 (reply `1795623496-run-431dcc20`).
  - Docs: plan wrote `docs/architecture.md`, `docs/agent-bus.md` and `docs/hooks.md`; edit wrote
    `CLAUDE.md`'s clause and `agents/git-manager.md`. No Node time limits row was needed — no
    added node sets `timeout_secs`.
- Filed 2026-09-09 13:58 by plan on the user's direct request, from the subsession's account
  (screenshot) with the timeline re-verified read-only against `~/Repos/pkh/is-advising-gateway`
  (branch history, `origin/main` since July 10, the in-progress rebase) and that session's log (run
  `28374348` started 11:09:03 at `implement`; commit gates 11:42:08 and 13:03:36).
- The no-overlap threshold was decided by the user on 2026-11-24 (see Decisions): 5 by default,
  configurable, overlap always gating. The reasoning as filed: gating every run that is 1 behind on
  an active repo would make the gate noise; gating only on overlap misses a stale toolchain or
  config the spec does not name.
- The Mechanism section's file and line references are as filed on 2026-09-09 and were not re-read
  on 2026-11-24; the template is now named `50-spec-to-pr`, and the condition set has grown from
  the eleven types counted there to sixteen. Phase 1 re-reads them before building.
- Phase 2 as built (worker's review-fix report, 2026-11-24, read against the tree by plan):
  - The validate rule (`validateFreshness`, `bus/freshness_conditions.go`) applies to
    `requires_spec` templates only — today `50-spec-to-pr`. `55-defect-backlog-loop` (implements
    uncommitted on main, where a rebase sync does not apply) and the PR-fix templates 60, 80 and 90
    (the branch's freshness is the PR's own concern) are deliberately outside it. This is narrower
    than the acceptance criterion's "a builtin template" as written.
  - An implementation node is a `spawn`, or a `send`/`map` addressed to edit (`implementsWork`).
    Roots are `start` and the target of every capped success edge; a send's or spawn's failure
    edges are not followed, a condition's or gate's branches are.
  - The `sync` node runs a new `muxcode sync-main` command (`bus.SyncOntoMain`,
    `bus/sync_main.go`): it refuses an unsettled tree, aborts a conflicting rebase, and reports a
    conflicting autostash while keeping its stash. `refresh` reads unmerged paths or a rebase in
    progress as stale.
  - From the worker's handoff (`/tmp/mux166-phase2.md`): the condition set is now 18 types;
    both conditions share one measurement and one `branch-freshness` row per evaluation and fail
    closed while unmeasured (`Freshness.Unmeasured`). `50-spec-to-pr` gained a fourth node the
    spec did not name, `sync-conflict` (`wait_human`, carrying `${output:sync}`), where a failed
    sync parks; `sync → refresh` and `sync-conflict → refresh` are capped at 3 and reset on each
    loop re-entry. A freshness condition records its summary as node output so the gate text can
    carry it; other conditions still record none. The commit agent's action is named `rebase`,
    not `sync`.
- Two acceptance criteria left open at the Phase 4 verify (plan, 2026-11-24), each a gap between
  the wording and the build rather than missing work:
  - Criterion 1 asks the gate text to carry "the upstream merges touching spec-named files (with
    the files)". `Freshness.Summary()` carries the behind count, the files (a deleted one marked)
    and the fetch age, but names no commit or merge. **Closed 2026-11-24 19:13** by the
    acceptance worker: `Freshness.OverlapBy` names each upstream commit on main touching an
    overlapping file (short sha and subject; the merge, not its side-branch commit), `Summary()`
    carries it, a unit test pins it, and the script's section 1 asserts the real gate text holds
    `3 behind`, the deleting commit and the fetch age (28/28).
  - Criterion 7 says `graph validate` rejects "a builtin template"; the rule covers
    `requires_spec` templates only (see above). **Still open, and the user's decision**: the
    acceptance worker reports it not met by design — widening the rule means adding freshness
    gates to `55-defect-backlog-loop` and the PR-fix templates 60, 80 and 90, a behaviour change
    to other workflows. Either the criterion is reworded to the built scope, or the wider rule is
    filed as its own spec and this box ticked as deferred to it. **Decided 2026-11-25: widen,
    in this spec** — see the first note and Phases 5 and 6.
  The other seven are ticked on the integration script's 27/27 and, for the loop re-entry and the
  `@{upstream}` and fetch-failure cases, on the unit tests named in Phases 1 and 2.
- Related: [MUX-144](../completed/MUX-144-wait-human-gate-openable-by-any-agent.md) (the gate the sync sits
  behind); [MUX-148](../completed/MUX-148-node-outcome-reads-command-ran-as-task-done.md) and
  [MUX-154](../completed/MUX-154-codex-status-line-closes-tracked-tasks.md) (why the build node's green meant
  nothing here); [MUX-132](../completed/MUX-132-graph-retry-launders-gate-approval.md) (single-use
  approvals — a re-entered `sync-gate` needs a fresh one); [MUX-165](../completed/MUX-165-gated-jira-write-declined-by-requester-rule.md)
  (the other consent instrument the graph carries, filed today).

## Status

**Complete** — closed out 2026-11-25 at 40/40 by the `55-defect-backlog-loop` run that activated it on 2026-11-24: Phases 1–6 and every acceptance criterion verified (Phases 5 and 6: the gate widened to `55-defect-backlog-loop` and templates 60, 80 and 90 on the user's decision of 2026-11-25), the integration script 62/62 (Phases 1–3 verified 2026-11-24: `bus/branch_freshness.go`, the console on `BranchDivergence`, the two conditions, the `50-spec-to-pr` nodes, the validator rule, `muxcode sync-main`, and the docs — the condition table rows went into `docs/hooks.md`, where that reference lives). Unparked
2026-11-24 on the user's decision (threshold 5, configurable; overlap always gates). Filed
2026-09-09 13:58.
