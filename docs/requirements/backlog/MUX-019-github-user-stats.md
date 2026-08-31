# GitHub user stats

Add a GitHub contribution analytics modal triggered from the MuxCode main menu (`prefix + b → S`) that displays commit history, code volume, PR activity, and active days — scoped to the current repo or expanded across all repos in the user's account. Also available as a CLI command (`muxcode stats`) for programmatic use by agents.

## Problem

### Observed behavior

Developers lack a quick way to see their contribution footprint. Getting a picture of commit volume, code churn, PR review activity, and active days requires manually querying `git log`, GitHub's contribution graph, or third-party tools. There's no unified view that works from the terminal where developers already live.

### Use cases

| Scenario | What the user wants |
|----------|-------------------|
| **Current repo summary** | "How much have I contributed to this project?" — commits, LOC, PRs, active days |
| **Cross-repo overview** | "What does my GitHub activity look like across all my repos?" — aggregated stats |
| **Time-bounded review** | "What did I do last quarter?" — stats filtered by date range |
| **Team visibility** | "How active is a collaborator?" — stats for a specific GitHub user in the current repo |

## Requirements

### Acceptance criteria

#### Modal integration
- [ ] `prefix + b → S` opens the stats modal from the MuxCode main menu
- [ ] Stats modal registered in `DefaultModalConfigs()` with name `"stats"`, Dracula-themed border
- [ ] `muxcode modal open stats` opens the modal programmatically (toggle behavior like other modals)
- [ ] Modal displays an interactive TUI: current repo stats on launch, with key bindings to switch views
- [ ] TUI key bindings: `a` = all repos, `r` = current repo (default), `q` = quit, `j/k` = scroll
- [ ] Modal size defaults (e.g. `70%x70%`) with `compact` and `full` size presets

#### CLI command (for agents and scripting)
- [ ] `muxcode stats` shows contribution stats for the authenticated user in the current repo
- [ ] `muxcode stats --all` aggregates stats across all repos in the user's GitHub account
- [ ] `muxcode stats --user <username>` shows stats for a specific GitHub user (current repo only)
- [ ] `--since` and `--until` flags filter stats by date range (ISO 8601 or relative like `30d`, `6m`, `1y`)
- [ ] `--json` flag outputs structured JSON for programmatic consumption

#### Stats content
- [ ] Stats include: total commits, lines added, lines removed, net lines of code
- [ ] Stats include: PRs opened, PRs merged, PRs reviewed, PRs approved
- [ ] Stats include: active commit days (unique days with at least one commit), longest streak
- [ ] Human-readable output uses Dracula-themed colors consistent with other muxcode commands
- [ ] Current-repo stats use local git history (fast, no API calls) supplemented by GitHub API for PR data
- [ ] Cross-repo stats (`--all`) use the GitHub REST API via `gh` CLI (requires authentication)
- [ ] The command completes within 5 seconds for single-repo local stats
- [ ] All existing tests pass (`go test ./...`)
- [ ] New tests cover stat computation, date filtering, modal config, and output formatting

### Out of scope

- Graphical visualizations (ASCII charts, sparklines) — may be added later
- Comparison between users (side-by-side)
- Issue/discussion activity (only commits and PRs)
- Commit categorization by type (feature, fix, refactor)

## Technical approach

### Modal architecture

The stats feature runs as a modal popup (like the API testing modal) using the existing `ModalConfig` system in `bus/modal.go`. The modal launches a TUI built with Go's standard library (alternate screen buffer, raw terminal mode) — consistent with the existing harness TUI and provider selector TUI.

```go
// Registered in DefaultModalConfigs()
ModalConfig{
    Name:    "stats",
    Title:   " GitHub Stats ",
    Width:   "70%",
    Height:  "70%",
    Command: "muxcode stats --tui",
    Sizes: map[string][2]string{
        "compact": {"55%", "50%"},
        "full":    {"95%", "95%"},
    },
}
```

The `--tui` flag launches the interactive TUI mode (modal context). Without `--tui`, the command runs in CLI mode (one-shot output for agents and scripts).

```
┌─────────────── GitHub Stats ───────────────┐
│                                            │
│  GitHub Stats: mkoberlein @ muxcode        │
│  Period: all time (2024-11-15 → 2026-05-11)│
│  ──────────────────────────────────────     │
│                                            │
│  Commits                                   │
│    Total:          347 (12 merges)          │
│                                            │
│  Code volume                               │
│    Lines added:    +42,810                  │
│    Lines removed:  -18,234                  │
│    Net:            +24,576                  │
│                                            │
│  Pull requests                             │
│    Opened: 28   Merged: 24                 │
│    Reviewed: 15   Approved: 12             │
│                                            │
│  Activity                                  │
│    Active days:    142 / 543 (26%)          │
│    Longest streak: 14 days                  │
│    Current streak: 3 days                   │
│                                            │
│  [r] Repo  [a] All repos  [q] Quit         │
└────────────────────────────────────────────┘
```

**TUI key bindings:**

| Key | Action |
|-----|--------|
| `r` | Show current repo stats (default view) |
| `a` | Switch to all-repos aggregate view |
| `j` / `↓` | Scroll down |
| `k` / `↑` | Scroll up |
| `q` / `Esc` | Quit modal |

### Data sources

| Metric | Source | Method |
|--------|--------|--------|
| Commits, LOC added/removed | Local git | `git log --author --numstat --format` |
| Active days, streaks | Local git | Parse commit dates from `git log` |
| PRs opened, merged | GitHub API | `gh api /repos/{owner}/{repo}/pulls?state=all` |
| PRs reviewed, approved | GitHub API | `gh api /repos/{owner}/{repo}/pulls/{n}/reviews` |
| Cross-repo commit counts | GitHub API | `gh api /users/{user}/repos` + per-repo queries |

### Core data structures

```go
type UserStats struct {
    User        string       `json:"user"`
    Repo        string       `json:"repo"`        // "" for --all aggregate
    Period      StatsPeriod  `json:"period"`
    Commits     CommitStats  `json:"commits"`
    CodeVolume  CodeStats    `json:"code_volume"`
    PullRequests PRStats     `json:"pull_requests"`
    Activity    ActivityStats `json:"activity"`
}

type CommitStats struct {
    Total       int `json:"total"`
    Merges      int `json:"merges"`
    NonMerge    int `json:"non_merge"`
}

type CodeStats struct {
    LinesAdded   int `json:"lines_added"`
    LinesRemoved int `json:"lines_removed"`
    NetLines     int `json:"net_lines"`
    FilesChanged int `json:"files_changed"`
}

type PRStats struct {
    Opened   int `json:"opened"`
    Merged   int `json:"merged"`
    Closed   int `json:"closed"`    // closed without merge
    Reviewed int `json:"reviewed"`  // PRs where user left a review
    Approved int `json:"approved"`  // PRs where user approved
}

type ActivityStats struct {
    ActiveDays    int    `json:"active_days"`
    TotalDays     int    `json:"total_days"`      // span from first to last commit
    LongestStreak int    `json:"longest_streak"`   // consecutive days with commits
    CurrentStreak int    `json:"current_streak"`
    FirstCommit   string `json:"first_commit"`     // ISO 8601 date
    LastCommit    string `json:"last_commit"`
}

type StatsPeriod struct {
    Since string `json:"since,omitempty"` // ISO 8601
    Until string `json:"until,omitempty"`
}
```

### Git log parsing

Extract commit stats from local git without GitHub API calls:

```go
// collectGitStats runs git log with numstat and parses output.
// Uses --author to filter by user, --since/--until for date range.
func collectGitStats(repoPath, author, since, until string) (*CommitStats, *CodeStats, *ActivityStats, error) {
    // git log --author="<author>" --since="<since>" --until="<until>" \
    //   --numstat --format="%H|%aI|%P" --no-merges
    // Parse: commit hash, author date (ISO), parent hashes (merge detection)
    // Accumulate: lines added/removed per file, unique dates, streak computation
}
```

### GitHub API integration

Use `gh` CLI for PR data (avoids managing auth tokens directly):

```go
// collectPRStats fetches PR activity for a user in a repo via gh CLI.
func collectPRStats(owner, repo, user string) (*PRStats, error) {
    // gh api --paginate /repos/{owner}/{repo}/pulls?state=all
    // Filter by user for opened/merged/closed
    // gh api /repos/{owner}/{repo}/pulls/{n}/reviews
    // Filter by user for reviewed/approved
}
```

### Cross-repo aggregation

For `--all` mode, query the user's repos and aggregate:

```go
// collectAllRepoStats fetches stats across all user repos.
func collectAllRepoStats(user, since, until string) (*UserStats, []RepoBreakdown, error) {
    // gh api --paginate /users/{user}/repos?type=all&sort=pushed
    // For each repo: clone-less commit counting via GitHub API
    // gh api /repos/{owner}/{repo}/contributors
    // Aggregate PRStats per repo
}

type RepoBreakdown struct {
    Repo       string    `json:"repo"`
    Commits    int       `json:"commits"`
    LinesAdded int       `json:"lines_added"`
    PRsOpened  int       `json:"prs_opened"`
    LastActive string    `json:"last_active"`
}
```

### Human-readable output format

```
$ muxcode stats

  GitHub Stats: mkoberlein @ muxcode
  Period: all time (2024-11-15 → 2026-05-11)
  ─────────────────────────────────────────────

  Commits
    Total:          347 (12 merges, 335 non-merge)

  Code volume
    Lines added:    +42,810
    Lines removed:  -18,234
    Net:            +24,576
    Files changed:  1,204

  Pull requests
    Opened:         28
    Merged:         24
    Reviewed:       15
    Approved:       12

  Activity
    Active days:    142 / 543 (26%)
    Longest streak: 14 days (2025-03-01 → 2025-03-14)
    Current streak: 3 days
```

```
$ muxcode stats --all

  GitHub Stats: mkoberlein (all repos)
  Period: all time
  ─────────────────────────────────────────────

  Summary
    Repos:          12
    Total commits:  1,247
    Total LOC:      +186,420 / -73,210
    Total PRs:      89 opened, 78 merged
    Active days:    298

  Top repos by commits
    ✦  muxcode              347 commits   +42,810 LOC   last: 2d ago
    ✦  data-pipeline         231 commits   +38,100 LOC   last: 5d ago
    ✦  infra-cdk            198 commits   +28,400 LOC   last: 12d ago
    ✦  contact-service      142 commits   +22,300 LOC   last: 30d ago
    ...
```

## Key files

| File | Change |
|------|--------|
| `tools/muxcode/bus/stats.go` | New file — `UserStats`, `CommitStats`, `CodeStats`, `PRStats`, `ActivityStats` structs, `collectGitStats()`, `collectPRStats()`, `collectAllRepoStats()`, `parseGitLog()`, `computeStreaks()`, `FormatStats()`, `FormatStatsJSON()` |
| `tools/muxcode/bus/stats_tui.go` | New file — `RunStatsTUI()`, alternate screen buffer, raw terminal mode, key handler, view rendering, scroll state |
| `tools/muxcode/bus/modal.go` | Add `stats` modal to `DefaultModalConfigs()` |
| `tools/muxcode/cmd/stats.go` | New file — `Stats()` command handler, flag parsing (`--tui`, `--all`, `--user`, `--since`, `--until`, `--json`) |
| `tools/muxcode/main.go` | Add `"stats"` to `knownSubcommands` and route to `cmd.Stats()` |
| `tools/muxcode/bus/stats_test.go` | New file — tests for git log parsing, streak computation, date filtering, stat aggregation, output formatting, modal config |
| `config/tmux.conf` | Add `"GitHub Stats" S` entry to the MuxCode quick menu (`prefix + b`) |

## Implementation

### Phase 1: Local git stats collection

Parse `git log` output for commits, code volume, and activity metrics in the current repo.

- [ ] Create `bus/stats.go` with `UserStats`, `CommitStats`, `CodeStats`, `ActivityStats`, `StatsPeriod` structs
- [ ] Add `parseGitLog(output string) ([]GitCommitEntry, error)` — parse `git log --numstat --format` output into structured entries
- [ ] Add `collectGitStats(repoPath, author, since, until string) (*CommitStats, *CodeStats, *ActivityStats, error)` — runs git log and aggregates stats
- [ ] Add `computeStreaks(dates []time.Time) (longest, current int)` — calculate consecutive-day streaks from commit dates
- [ ] Add `resolveGitUser(repoPath string) (name, email string, error)` — get current user from `git config user.name` / `user.email`
- [ ] Add `resolveRelativeDate(input string) (string, error)` — parse relative dates (`30d`, `6m`, `1y`) to ISO 8601
- [ ] Add tests for git log parsing with synthetic output, streak computation edge cases, relative date parsing
- [ ] **Verify**: `cd tools/muxcode && go test ./...` — all tests pass
- [ ] **Verify**: `cd tools/muxcode && go vet ./...` — no issues

### Phase 2: GitHub API integration for PR stats

Fetch PR data via `gh` CLI for opened, merged, reviewed, and approved counts.

- [ ] Add `PRStats` struct with `Opened`, `Merged`, `Closed`, `Reviewed`, `Approved` fields
- [ ] Add `resolveRepoOwner(repoPath string) (owner, repo string, error)` — parse GitHub remote URL to get owner/repo
- [ ] Add `collectPRStats(owner, repo, user string) (*PRStats, error)` — query `gh api` for PRs authored by user, then reviews by user
- [ ] Add `isGHAvailable() bool` — check if `gh` CLI is installed and authenticated
- [ ] Handle pagination for repos with many PRs (`--paginate` flag)
- [ ] Add tests for PR stat aggregation with mock `gh` output
- [ ] **Verify**: `cd tools/muxcode && go test ./...` — all tests pass

### Phase 3: Cross-repo aggregation

Support `--all` flag to aggregate stats across all repos in the user's GitHub account.

- [ ] Add `RepoBreakdown` struct for per-repo summary in aggregate mode
- [ ] Add `collectAllRepoStats(user, since, until string) (*UserStats, []RepoBreakdown, error)` — list user repos via `gh api`, collect commit counts per repo from GitHub API (no local clone needed)
- [ ] Add `aggregateStats(breakdowns []RepoBreakdown) *UserStats` — sum across repos
- [ ] Sort repos by commit count for display
- [ ] Add tests for aggregation logic
- [ ] **Verify**: `cd tools/muxcode && go test ./...` — all tests pass

### Phase 4: Output formatting and CLI

Build human-readable and JSON formatters, wire up the CLI command.

- [ ] Add `FormatStats(stats *UserStats) string` — Dracula-themed human-readable output with sections
- [ ] Add `FormatStatsAllRepos(stats *UserStats, breakdowns []RepoBreakdown) string` — aggregate view with top repos table
- [ ] Add `FormatStatsJSON(stats *UserStats) string` — JSON with `json.MarshalIndent`
- [ ] Create `cmd/stats.go` with `Stats(args)` — parse flags: `--tui`, `--all`, `--user <name>`, `--since <date>`, `--until <date>`, `--json`
- [ ] Add `"stats"` to `knownSubcommands` in `main.go` and route to `cmd.Stats()`
- [ ] Add tests for formatting output (section structure, JSON validity)
- [ ] **Verify**: `cd tools/muxcode && go test ./...` — all tests pass
- [ ] **Verify**: `cd tools/muxcode && go vet ./...` — no issues

### Phase 5: Modal TUI and menu integration

Build the interactive TUI for modal mode and wire into the tmux main menu.

- [ ] Create `bus/stats_tui.go` with `RunStatsTUI()` — alternate screen buffer, raw terminal input, Dracula colors
- [ ] Implement view rendering: repo stats view (default), all-repos view, with scroll support
- [ ] Implement key handler: `r` (repo view), `a` (all repos), `j/k` (scroll), `q/Esc` (quit)
- [ ] Add `stats` modal to `DefaultModalConfigs()` in `bus/modal.go` — name `"stats"`, title `" GitHub Stats "`, width `70%`, height `70%`, command `muxcode stats --tui`
- [ ] Wire `--tui` flag in `cmd/stats.go` to call `RunStatsTUI()` instead of one-shot output
- [ ] Add `"GitHub Stats" S "run-shell 'muxcode modal open stats'"` to the MuxCode quick menu in `config/tmux.conf` (between "Provider" and the separator)
- [ ] Add tests for modal config registration, TUI key handling
- [ ] **Verify**: `cd tools/muxcode && go test ./...` — all tests pass
- [ ] **Verify**: `cd tools/muxcode && go vet ./...` — no issues
- [ ] **Verify**: `make install` — binary builds and installs, `prefix + b → S` opens stats modal

## Status

Backlog
