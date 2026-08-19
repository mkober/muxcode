# Branch Time Tracking

Track how much **active working time** has been spent on the current git branch and surface that total in four places: the tmux status bar, git commit messages, the Jira worklog, and a `muxcode branch-time` CLI. The daemon accumulates real elapsed time per branch into a global, cross-session ledger; the four surfaces read from that ledger.

## Context

### Overview

When working across many branches (often one per Jira story), there is no automatic record of how long each branch was actively worked on. This feature accumulates active session time per branch and exposes it through four surfaces:

| # | Surface | What it shows |
|---|---------|---------------|
| 1 | tmux status bar | Live segment with accumulated time on the current branch (e.g. `⏱ 4h12m`) |
| 2 | Git commit messages | A `Time-spent: <formatted>` trailer injected via a `prepare-commit-msg` hook |
| 3 | Jira worklog | Logs tracked time to the Jira story parsed from the branch name |
| 4 | CLI | `muxcode branch-time` reads/reports the ledger |

### Measurement semantics (decided)

| Aspect | Decision |
|--------|----------|
| What counts | **Active session time** — the daemon accumulates real elapsed seconds into a per-branch ledger while a muxcode session is open on that branch |
| Pause: session closed | Accumulation pauses when the daemon is not running |
| Pause: idle | Accumulation pauses when the session is idle. Two pause signals ship: (1) **no tmux client attached** (detached / session closed), and (2) **input inactivity** — no user interaction for longer than `branchTimeIdleSecs()` (`MUXCODE_BRANCH_TIME_IDLE_SECS`, default 300s / 5 min), so time spent away from the keyboard while still attached (lunch, a meeting, overnight) is not counted. Setting the env to `0` disables the input-inactivity check (revert to attach-only) |
| Cross-session persistence | Canonical ledger is global at `~/.config/muxcode/branch-time.json`, keyed by **repo identity + branch** so the same branch resumes its total across sessions/restarts |
| Repo identity | Git remote URL if present, else the repo top-level path |
| Clock-jump guard | Cap each tick's increment at ~2× the poll interval so a laptop sleep or clock change cannot add spurious hours |

### Data model

- **File**: `~/.config/muxcode/branch-time.json` (global, cross-session).
- **Shape**:
  ```json
  {
    "<repoKey>": {
      "<branch>": {
        "seconds": 15120,
        "lastJiraLoggedSeconds": 0,
        "updated": 1783344538
      }
    }
  }
  ```
- `seconds` — total accumulated active seconds for the branch.
- `lastJiraLoggedSeconds` — watermark of seconds already posted to Jira, so `log-jira` posts only the delta (prevents double-counting).
- `updated` — unix timestamp of the last write.
- All read-modify-write happens under a lock (reuse the bus lock pattern) to avoid races between the daemon accumulator and CLI commands.

## Design

### Components and integration points

#### A. Ledger library — new `bus/timetrack.go`

| Symbol | Purpose |
|--------|---------|
| `BranchTimePath()` | `~/.config/muxcode/branch-time.json` — helper added near the other `Global*` path helpers in `bus/config.go` (~line 183) |
| `RepoKey()` | Git remote URL, else repo toplevel path |
| `LoadBranchTime()` / `SaveBranchTime()` | Locked JSON read/write |
| `AccumulateBranchTime(repoKey, branch, deltaSecs)` | Add a capped delta to the branch total |
| `BranchTimeSeconds(repoKey, branch)` / `AllBranchTimes(repoKey)` | Read a single branch total / all branches for a repo |
| `FormatDuration(secs)` | Compact formatting: `4h 12m`, `46m`, `2d 5h` |

#### B. Daemon accumulator — `daemon/daemon.go`

- Add `checkBranchTime()` to the `Run()` loop alongside the other `check*()` calls (the cluster at daemon.go ~249–268). Reuse the existing single poll loop — no new ticker.
- Track `d.lastBranchTick time.Time` and `d.lastBranch string` on the `Daemon` struct.
- Each tick: resolve the current branch via existing `branchName()` (`bus/conditions.go:411`); if a client is attached, the session is not input-idle, and the branch is unchanged since the last tick, add `min(now-lastTick, 2*pollInterval)` seconds to that branch. On detach, input-idle, or branch change the accumulator flushes and resets the baseline so the paused interval is never back-counted.
- Idle gate: `checkBranchTime()` (`daemon/daemon.go`) uses `bus.SessionIdleSeconds()` vs `branchTimeIdleSecs()` (`MUXCODE_BRANCH_TIME_IDLE_SECS`, default 300s; `0` disables). Per-tick deltas are accrued in memory and flushed to the ledger at most once per `branchTimeFlushSecs` (or on pause / branch change).
- Opt-out env var `MUXCODE_BRANCH_TIME_DISABLE=1`.
- Emit a lifecycle event on the first accumulation per branch.

#### C. CLI command — new `cmd/branchtime.go`

Command name is `branch-time` (`track` is taken by delivery-status).

| Invocation | Behavior |
|------------|----------|
| `muxcode branch-time` | Current branch, formatted total |
| `muxcode branch-time --all` | Table of all branches for this repo |
| `muxcode branch-time --status` | Short status-bar string (e.g. `⏱ 4h12m`); **empty output** when disabled or not in a git repo (keeps the status bar clean) |
| `muxcode branch-time --trailer` | Print the bare commit-hook trailer line (`Time-spent: <formatted>`) for the current branch; consumed by the `prepare-commit-msg` hook. Empty output when disabled or not in a git repo |
| `muxcode branch-time --add <secs>` | Manually add `<secs>` seconds to the current branch's total (manual time entry — e.g. reconciling offline work). Subject to the same lock + clamp as automatic accumulation |
| `muxcode branch-time log-jira [--dry-run]` | Parse the Jira key from the branch name, post the worklog delta (`seconds - lastJiraLoggedSeconds`), then advance the watermark. `--dry-run` prints what it would post without posting |
| `muxcode branch-time reset [branch]` | Zero a branch counter |

Register the command in `main.go`'s command switch.

#### D. tmux status bar — `bus/launcher.go`

- In `TransformStatusRight()` (`bus/launcher.go:827`), add a `#(muxcode branch-time --status)` segment (Dracula-themed).
- Ensure `status-interval` is set (e.g. 15s) so tmux re-runs the command — set it in `config/tmux.conf` and/or the launcher status setup.

#### E. Git commit-msg trailer — `bus/git_hooks.go`

- Add `InstallPrepareCommitMsgHook()` mirroring the existing idempotent, marker-based, `hooksPath`-respecting `InstallCommitMsgHook()` (`bus/git_hooks.go:43`).
- The `prepare-commit-msg` hook appends a `Time-spent: <formatted>` trailer (sourced from `muxcode branch-time --trailer`), skipping merge/squash/amend message sources to avoid duplicates.
- Install it from `LaunchSession()`, next to the existing commit-msg hook install.

#### F. Jira worklog — `bus/atlassian.go` + `cmd/atlassian.go`

- `JiraAddWorklog(cfg, key, timeSpentSeconds, comment)` → `POST /rest/api/3/issue/{key}/worklog` with body `{"timeSpentSeconds":N,"comment":<ADF>}`.
- CLI: `muxcode atlassian jira worklog <key> <seconds> [comment]` in the jira subcommand dispatch in `cmd/atlassian.go`.
- Reuse the CLI-only Atlassian policy (never MCP). Report raw HTTP status/body on error.

### Key files

| File | Purpose | Status |
|------|---------|--------|
| `bus/timetrack.go` | Ledger library (load/save/accumulate/format) | New |
| `bus/timetrack_test.go` | Unit tests for ledger + clock guard + formatting | New |
| `bus/config.go` | Add `BranchTimePath()` near `Global*` helpers | Update |
| `daemon/daemon.go` | Add `checkBranchTime()` + `lastBranchTick`/`lastBranch` fields | Update |
| `cmd/branchtime.go` | `branch-time` CLI (report/all/status/trailer/add/log-jira/reset) | New |
| `main.go` | Register `branch-time` command | Update |
| `bus/launcher.go` | Status-bar segment in `TransformStatusRight()` | Update |
| `config/tmux.conf` | `status-interval` so tmux re-runs the segment | Update |
| `bus/git_hooks.go` | `InstallPrepareCommitMsgHook()` (`Time-spent:` trailer) | Update |
| `bus/launch.go` | Wire hook install into `LaunchSession()` | Update |
| `bus/atlassian.go` | `JiraAddWorklog()` | Update |
| `cmd/atlassian.go` | `jira worklog` subcommand | Update |
| `scripts/test-branch-time.sh` | End-to-end integration test | New |

## Requirements

### Acceptance criteria

- [x] Time accrues per-branch only while a tmux client is attached and the session is not input-idle; accumulation pauses when the session is detached, closed, or idle past `MUXCODE_BRANCH_TIME_IDLE_SECS`
- [x] Totals persist across session restarts (global ledger keyed by repo identity + branch)
- [x] Repo identity resolves to the git remote URL when present, else the repo top-level path
- [x] Clock jumps cannot add more than ~2× the poll interval of time per tick
- [x] Status bar shows a compact live segment; output is empty/clean when disabled or not in a git repo
- [x] Commits carry a `Time-spent:` trailer via the `prepare-commit-msg` hook (skipped on merge/squash/amend)
- [x] `branch-time log-jira` posts only the un-logged delta (`seconds - lastJiraLoggedSeconds`) and advances the watermark
- [x] `branch-time log-jira --dry-run` reports the delta it would post without posting
- [x] `MUXCODE_BRANCH_TIME_DISABLE=1` disables both accumulation and status output
- [x] All ledger read-modify-write is lock-guarded (no races between daemon and CLI)
- [x] Integration test script passes end-to-end

## Implementation

### Phase 1: Ledger library and config path

- [x] Add `BranchTimePath()` to `bus/config.go` near the other `Global*` path helpers
- [x] Create `bus/timetrack.go` with `RepoKey()`, `LoadBranchTime()`, `SaveBranchTime()`, `AccumulateBranchTime()`, `BranchTimeSeconds()`, `AllBranchTimes()`, `FormatDuration()`
- [x] Implement locked read-modify-write reusing the bus lock pattern
- [x] Implement clock-jump guard (cap each accumulate delta at ~2× poll interval)
- [x] Create `bus/timetrack_test.go` with unit tests

Success criteria:
- [x] `RepoKey()` returns the git remote URL when present, else the repo toplevel path
- [x] `AccumulateBranchTime()` caps the delta and is race-safe under concurrent access
- [x] `FormatDuration(secs)` returns `4h 12m` / `46m` / `2d 5h` shapes
- [x] Ledger round-trips through `SaveBranchTime()` / `LoadBranchTime()` without loss
- [x] Unit tests pass

### Phase 2: Daemon accumulator

- [x] Add `lastBranchTick time.Time` and `lastBranch string` fields to the `Daemon` struct
- [x] Implement `checkBranchTime()` and call it from the `Run()` loop
- [x] Resolve the current branch via existing `branchName()`
- [x] Only accumulate when a tmux client is attached, the session is not input-idle (`SessionIdleSeconds` ≤ `branchTimeIdleSecs()`), and the branch is unchanged since the last tick
- [x] Apply `min(now-lastTick, 2*pollInterval)` per tick
- [x] Honor `MUXCODE_BRANCH_TIME_DISABLE=1` (skip accumulation) and `MUXCODE_BRANCH_TIME_IDLE_SECS` (idle threshold; `0` = attach-only)
- [x] Emit a lifecycle event on first accumulation per branch

Success criteria:
- [x] Accumulation advances only while a client is attached and the session is not input-idle
- [x] No accumulation when detached, closed, input-idle past the threshold, or disabled
- [x] Branch change resets the tick baseline (no cross-branch bleed)
- [x] Clock jump / laptop sleep adds at most ~2× poll interval

### Phase 3: `branch-time` CLI

- [x] Create `cmd/branchtime.go`
- [x] Implement default (current branch total), `--all` (table), `--status` (compact string), `reset [branch]`
- [x] Implement `--trailer` (bare `Time-spent:` line for the commit hook) and `--add <secs>` (manual time entry)
- [x] `--status` and `--trailer` print empty output when disabled or not in a git repo
- [x] `--add <secs>` writes under the same lock + clamp as automatic accumulation
- [x] Register `branch-time` in `main.go`'s command switch

Success criteria:
- [x] `muxcode branch-time` prints the current branch's formatted total
- [x] `muxcode branch-time --all` prints a table of all branches for the repo
- [x] `muxcode branch-time --status` prints a compact segment (e.g. `⏱ 4h12m`), empty when disabled/not-a-repo
- [x] `muxcode branch-time --trailer` prints the bare `Time-spent: <formatted>` line, empty when disabled/not-a-repo
- [x] `muxcode branch-time --add <secs>` adds to the current branch total under lock
- [x] `muxcode branch-time reset [branch]` zeroes the counter

### Phase 4: tmux status-bar segment

- [x] Add a `#(muxcode branch-time --status)` segment to `TransformStatusRight()` (Dracula-themed)
- [x] Set `status-interval` (e.g. 15s) in `config/tmux.conf` and/or launcher status setup

Success criteria:
- [x] Status bar shows the live branch-time segment and refreshes on `status-interval`
- [x] Segment is empty/clean when disabled or not in a git repo (no stray characters)

### Phase 5: `prepare-commit-msg` trailer hook

- [x] Add `InstallPrepareCommitMsgHook()` to `bus/git_hooks.go` (idempotent, marker-based, `hooksPath`-respecting)
- [x] Hook appends a `Time-spent: <formatted>` trailer
- [x] Skip on merge/squash/amend message sources
- [x] Wire the install into `LaunchSession()` next to the commit-msg hook install

Success criteria:
- [x] A normal commit carries a `Time-spent:` trailer reflecting the branch total
- [x] Merge/squash/amend commits do not get a duplicate trailer
- [x] Hook install is idempotent and chains with existing hooks / respects `core.hooksPath`

### Phase 6: Jira worklog

- [x] Add `JiraAddWorklog(cfg, key, timeSpentSeconds, comment)` to `bus/atlassian.go` (POST `/rest/api/3/issue/{key}/worklog`, ADF comment)
- [x] Add `muxcode atlassian jira worklog <key> <seconds> [comment]` to the jira dispatch in `cmd/atlassian.go`
- [x] Implement `muxcode branch-time log-jira [--dry-run]`: parse the Jira key from the branch name, post the delta, advance `lastJiraLoggedSeconds`
- [x] Report raw HTTP status/body on error; CLI-only Atlassian policy (never MCP)

Success criteria:
- [x] `jira worklog <key> <seconds>` posts a worklog and reports success/error with HTTP detail
- [x] `branch-time log-jira` posts only `seconds - lastJiraLoggedSeconds` and advances the watermark
- [x] `branch-time log-jira --dry-run` prints the computed delta without posting
- [x] Running `log-jira` twice with no new time posts nothing the second time (watermark prevents double-count)

### Phase 7: Integration test

- [x] Create `scripts/test-branch-time.sh` end-to-end automation script
- [x] Test: accumulate over simulated ticks and verify the branch total grows
- [x] Test: `muxcode branch-time --status` output shape (compact, empty when disabled)
- [x] Test: a commit carries the `Time-spent:` trailer
- [x] Test: `log-jira --dry-run` computes the correct un-logged delta
- [x] Test: `reset` zeroes the branch counter
- [x] Run the script and verify all checks pass

Success criteria:
- [x] `scripts/test-branch-time.sh` passes end-to-end
- [x] Script covers: accumulation, `--status` shape, commit trailer, `log-jira --dry-run` delta, reset
- [x] Script performs prerequisite checks and cleans up after itself

## Configuration

| Aspect | Mechanism |
|--------|-----------|
| Disable feature | `MUXCODE_BRANCH_TIME_DISABLE=1` — disables accumulation and `--status` output |
| Input-inactivity idle | `MUXCODE_BRANCH_TIME_IDLE_SECS` (default 300s / 5 min) — pause accumulation after this long without user input; `0` disables the idle check (attach-only) |
| Ledger location | `~/.config/muxcode/branch-time.json` (global, cross-session) |
| Poll cadence | Reuses the daemon's existing poll loop — no new ticker |
| Status refresh | tmux `status-interval` (e.g. 15s) |
| Clock-jump cap | Hardcoded ~2× poll interval per tick |

## Known limitations

| Limitation | Impact | Mitigation |
|------------|--------|------------|
| Input-inactivity idle granularity | Idle is detected from tmux client input activity (`SessionIdleSeconds`), not agent-level work — an attached user reading output without typing for `> MUXCODE_BRANCH_TIME_IDLE_SECS` is treated as idle | Shipped: input-inactivity detection (default 5 min) supersedes the original attach-only proxy; agent-activity-aware idle remains a possible follow-up. Tune via `MUXCODE_BRANCH_TIME_IDLE_SECS` or set `0` for attach-only |
| Repo identity via remote/path | Renamed remotes or moved repos start a fresh ledger key | Keyed on remote URL first, path fallback — stable for the common case |
| Sequential daemon poll | Accumulation granularity is bounded by the poll interval | Acceptable — sub-poll precision is unnecessary for working-time totals |
| Jira worklog needs a parseable key | Branches without a Jira key in the name can't `log-jira` | `log-jira` reports a clear error; other surfaces still work |

## Dependencies

| Dependency | Purpose | Status |
|------------|---------|--------|
| `bus/config.go` `Global*` helpers | Pattern for `BranchTimePath()` | Existing |
| `bus/conditions.go` `branchName()` | Resolve current branch in the daemon | Existing |
| `bus/git_hooks.go` `InstallCommitMsgHook()` | Template for `InstallPrepareCommitMsgHook()` | Existing |
| `bus/launcher.go` `TransformStatusRight()` | Status-bar segment injection point | Existing |
| `bus/launch.go` `LaunchSession()` | Hook install wiring | Existing |
| `bus/atlassian.go` `LoadAtlassianConfig()` / `JiraComment()` | Auth + ADF patterns for `JiraAddWorklog()` | Existing |
| `daemon/daemon.go` `Run()` loop | Host for `checkBranchTime()` | Existing |
| Bus lock pattern | Race-safe ledger read-modify-write | Existing |

## Status

Complete — all 7 phases implemented, build clean, unit + integration tests green
