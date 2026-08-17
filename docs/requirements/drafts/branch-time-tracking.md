# Branch Active-Time Tracking

Auto-record the time muxcode agents spend working on a branch into the corresponding requirements doc, for every repo muxcode works on. Time is **session-derived active time** (not git commit span), and recording rides the existing `verify-spec` chain — when build→test→review succeeds, the plan agent's verification pass also writes the branch's accumulated active time into the spec it is verifying.

> **Scope reset (2026-08-13):** the original draft specified a from-scratch accumulator, CLI, and daemon sampler. All of that already shipped in commits `0c93011` + `2594f36` (July 2026) — discovered when Phase 1 was delegated. The user chose **extend existing**: this spec is now a small delta on the shipped `branch-time` system. The remaining work is a JSON read path and the requirements-doc sink with never-regress reconciliation.

## Context

### User request (verbatim)

> "need to have the plan agent auto record the time spent on branches to the corresponding requirement docs for all repos muxcode works on"

### Settled decisions (by the user — not open for redesign)

| Decision | Choice | Accepted trade-off |
|----------|--------|--------------------|
| Time source | Session-derived "active time": windows where muxcode agents/user were actually working | Misses work done outside muxcode; cannot backfill old branches |
| Trigger | Ride the existing `verify-spec` chain — recording is one more thing plan does in that pass | No recording on branches/sessions where the chain never fires |
| Architecture | **Extend the shipped `branch-time` system** (2026-08-13) — no parallel store, no replacement | Shipped behaviors win where the original draft differed (see deltas below) |

### Shipped baseline (already built — commits `0c93011`, `2594f36`)

| Component | Location | What it does |
|-----------|----------|--------------|
| Ledger | `bus/timetrack.go`, store at `~/.config/muxcode/branch-time.json` (`BranchTimePath()`) | Global cross-session ledger: repoKey → branch → `{seconds, lastJiraLoggedSeconds, updated}`; flock + atomic writes; clock-jump clamp |
| Repo identity | `RepoKey()` — origin URL (userinfo-stripped) or toplevel path | Stable across clones — covers "all repos muxcode works on" |
| Daemon sampler | `checkBranchTime()` in `daemon/daemon.go` poll loop | Accrues while the user is active (attached + typing within idle threshold) OR any worker agent shows positive working markers (Claude thinking spinner, OpenCode `▸`); in-memory pending, 60s flush / branch change / pause |
| Union accounting | `AnyAgentWorking()` — one boolean per sample | Concurrent agents count once by construction (wall-clock union, not per-agent sum) |
| CLI | `cmd/branchtime.go` — `muxcode branch-time` | `show`, `--all`, `--status` (tmux bar), `--trailer` (commit trailer), `--add <secs>`, `reset`, `log-jira [--dry-run]` (Jira worklog with watermark) |
| Ignored branches | `main`/`master` by default (`MUXCODE_BRANCH_TIME_IGNORE`) | Integration branches accrue no time |
| Kill switch | `MUXCODE_BRANCH_TIME_DISABLE=1` | Silences status bar / trailer output |
| Integration test | `scripts/test-branch-time.sh` | Daemon-free accumulation path via `--add` |

### Deltas from the original draft (shipped behavior wins)

| Original draft said | Shipped reality | Position |
|---------------------|-----------------|----------|
| Store `~/.config/muxcode/timetrack/{repo-slug}.json`, per-repo files | Single global ledger `branch-time.json` keyed by repoKey | Keep shipped |
| Active rule: delivery-path idle signal ∧ not-reloading ∧ not-permBlocked | Positive working markers + user keyboard activity | Keep shipped — a permission-blocked or reloading agent shows no working marker, so it is effectively excluded |
| `muxcode timetrack` CLI + `seed` command | `muxcode branch-time` CLI; `seed` subcommand added 2026-08-13 | Revised — reconciliation uses `seed` (a floor, never lowers); `--add` rejected because additive re-seeding double-counts on a non-zero ledger |
| `main` accumulates in store, never written to docs | `main`/`master` accrue nothing (ignored by default) | Keep shipped — requirement docs map to feature branches |
| Store tracks `agentSecs` + `sessions` counters | Not tracked | Dropped — not needed for the doc sink |
| Store tracks `lastRecorded` doc watermark | Not tracked | Dropped — the doc row's "Last updated" column carries this |

### Cross-repo verification (2026-08-11)

Verified directly: `is-advising-gateway`, `is-lms-gateway`, and `is-service-providers-gateway` (under `~/Repos/pkh/`) **all have a `docs/requirements/` structure**. The convention this feature writes into exists in the repos the user named. Repos without one degrade to accumulate-only (the ledger works everywhere; only the doc write needs the convention).

## Requirements

### Acceptance criteria

Satisfied by the shipped baseline (verified 2026-08-13):

- [x] Active time accumulates per branch, per repo, in a durable store that survives session cleanup, daemon restart, and lifecycle-log rotation
- [x] Concurrent agent activity counts once: wall-clock union of active intervals, not per-agent sum
- [x] Works identically in every repo muxcode runs in — nothing muxcode-repo-specific
- [x] CLI reports accumulated time without requiring a doc write (`muxcode branch-time show` / `--all`)

Remaining (this spec's delta):

- [x] `muxcode branch-time --json` (current branch) and `--all --json` emit structured output plan can consume (branch, seconds, formatted duration, updated timestamp)
- [ ] A successful build→test→review chain (with an active spec set) results in the branch's cumulative active time recorded in the active spec's `## Time Tracking` section
- [ ] Re-running `verify-spec` for the same chain writes the same absolute total — no double-count, no duplicate rows (idempotent by construction: absolute totals, in-place row replace)
- [ ] Recorded totals never regress: a lost/reset store can never cause a doc to show less time than it already showed (doc value kept, store re-seeded via `seed`)
- [ ] Branches with no active spec, and repos without `docs/requirements/`, degrade to accumulate-only with no errors — stated behaviour, not emergent
- [x] Branch/spec mismatch (branch key prefix does not match the active spec's filename) still records into the active spec but is flagged in plan's reply to edit

### Technical approach

**The accumulator is done. The delta is a read path and a sink.**

**1. JSON read path (code — `cmd/branchtime.go`).**

- `muxcode branch-time --json` — current branch entry as JSON: `{"branch": "...", "seconds": N, "formatted": "4h 12m", "updated": ts}`
- `muxcode branch-time --all --json` — array of the same shape for every tracked branch in this repo
- Zero-time / unknown branches return `seconds: 0` rather than an error, so plan's read never fails on a fresh branch

**2. Requirements-doc sink — plan records during `verify-spec`.**

- `notifyPlanOnReview()` (`daemon/daemon.go`) gains one sentence instructing plan to record the branch's active time into the spec's Time Tracking section
- Plan reads the number via `muxcode branch-time --json` (branch from read-only `git rev-parse --abbrev-ref HEAD`), then upserts a `## Time Tracking` section in the active spec — created on first record, one row per branch, **row replaced in place** keyed by branch:

```markdown
## Time Tracking

| Branch | Active time | Last updated |
|--------|-------------|--------------|
| PROMGT-115-fix-syntax | 12h 34m | 2026-08-13 15:33 |
```

- Totals are **absolute cumulative values read from the ledger**, never deltas — re-recording rewrites the identical row, which is what makes the write idempotent
- Plan includes the recorded total (and any branch/spec mismatch flag) in its summary reply to edit

**3. Never-regress reconciliation — ledger is truth, doc never regresses.**

On each record, plan compares the ledger total with the doc's existing row (if any):

| Case | Behaviour |
|------|-----------|
| Ledger ≥ doc | Normal — write ledger total |
| Ledger < doc (store lost/reset) | Keep the doc's larger value; re-seed the ledger via `muxcode branch-time seed --secs <doc_secs>` (a floor — only ever raises) so the two re-converge |
| Doc table malformed (hand-edited) | Recreate the section from the ledger; malformed rows replaced, never duplicated |

**Decision (2026-08-13):** reconciliation shipped as a new `seed` subcommand with floor semantics instead of the draft's `--add` — `--add` is additive and double-counts whenever the ledger is not exactly zero, while a repeated or stale `seed` cannot deflate a ledger that has since accrued past it. A `record --secs <n>` watermark (`lastRecordedSeconds`, surfaced as `unrecordedSeconds` in `--json`) was also added as an inspectable staleness signal; it does not gate writes — idempotency comes from absolute totals.

**4. Branch → doc mapping (unchanged from original draft).**

| Situation | Behaviour |
|-----------|-----------|
| `verify-spec` fires (chain success + active spec set) | Record into the **active spec** — the explicit pointer wins over any filename inference |
| Branch key prefix does not match the active spec's filename | Record anyway (explicit beats inferred), flag the mismatch in plan's reply |
| No active spec set | Chain never notifies plan (existing `notifyPlanOnReview` gate) — accumulate-only |
| Repo has no `docs/requirements/` | Accumulate-only; CLI still works |

### Key files

| File | Change |
|------|--------|
| `cmd/branchtime.go` | Add `--json` to `show` and `--all` |
| `daemon/daemon.go` | One-sentence addition to `notifyPlanOnReview()` message |
| `agents/planner.md` | `verify-spec` process gains the recording step: read `--json`, upsert the row, never-regress reconciliation, mismatch flag |
| `scripts/test-branch-time.sh` | Extend with `--json` and doc-sink checks (or companion script) |
| `docs/architecture.md`, `docs/agent-bus.md`, `CLAUDE.md` | Document the JSON flag, the recording step, and reconciliation |

### Risks

| Risk | Mitigation / stated position |
|------|------------------------------|
| Working-marker detection imprecision skews measurement | Inherited from the shipped sampler — documented as approximate; this delta does not change measurement |
| Store lost or corrupted | Never-regress reconciliation: doc keeps its total, ledger re-seeded via `seed` (floor) |
| Daemon down → undercount | Accepted and stated (shipped behavior); at most ~60s lost on crash (flush cadence) |
| Daemon relaunched from a foreign cwd (`upgrade-daemons` inherits the caller's directory) misattributes time to the wrong repo, or silently stops tracking when that repo sits on `main` | Fixed 2026-08-13: `checkBranchTime()`/`flushBranchTime()`/`notifyPlanOnReview()` resolve branch and repo key via `SessionRepoDir(session)` (`CurrentBranchIn`/`RepoKeyIn`), never the process cwd; an unresolvable session dir skips the tick / omits the instruction rather than falling back. Cause also closed at the source: `UpgradeDaemons` relaunches daemons/monitors in the session's own directory (`startDetachedProcessIn`) |
| Work on branch B while active spec points at spec A | Recorded into the active spec (explicit pointer wins), mismatch flagged — the user sees it |
| Doc table parse fails (hand-edited section) | Plan recreates the section from the ledger; rows replaced, never duplicated |

## Implementation

### Phase 1: JSON read path

- [x] `muxcode branch-time --json` — current branch entry as structured JSON (zero-time branches return `seconds: 0`, not an error)
- [x] `muxcode branch-time --all --json` — all tracked branches for the repo
- [x] Unit tests: JSON shape, zero/unknown branch, `--all` ordering

### Phase 2: Requirements-doc sink via verify-spec

- [x] `notifyPlanOnReview()` message gains the recording instruction (branch named only when not on the ignore list)
- [x] `agents/planner.md`: verify-spec step — read `branch-time show --branch <b> --json`, upsert the `## Time Tracking` row (absolute totals, in-place replace keyed by branch), apply never-regress reconciliation (re-seed via `seed`), mark via `record`, flag branch/spec mismatch in the reply
- [ ] Docs: `docs/architecture.md`, `docs/agent-bus.md`, `CLAUDE.md` bullets

### Phase 3: Integration test

- [ ] Extend `scripts/test-branch-time.sh` (or add `scripts/test-branch-time-recording.sh`): `--json` output shape matches spec
- [ ] Test: simulated `verify-spec` recording into a scratch spec → `## Time Tracking` row created with the ledger's absolute total
- [ ] Test: re-run the same recording → identical row, no duplicate (idempotency)
- [ ] Test: ledger total lower than doc row → doc value kept, ledger re-seeded via `--add` (never-regress)
- [ ] Test: no active spec set → no doc write occurs, ledger still accumulates
- [ ] Run the script and verify all checks pass

## Status

In Progress — Phase 1 complete (JSON read path + unit tests); Phase 2 sink steps 1–2 implemented (daemon instruction, planner recording process; reconciliation revised to `seed` floor + `record` watermark); remaining: Phase 2 docs bullets (`docs/architecture.md`, `docs/agent-bus.md`, `CLAUDE.md`) and Phase 3 integration test
