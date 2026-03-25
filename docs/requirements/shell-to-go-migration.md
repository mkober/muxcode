# Shell-to-Go Migration

Consolidate 30 shell scripts (5,159 lines) into the existing `muxcode-agent-bus` Go binary. Phased approach — one category per phase, tested independently before proceeding.

## Context

The shell scripts have grown organically alongside the Go bus binary. Key problems:
- **10 log pollers** (3,301 lines) are 90%+ duplicated code — same JSONL parsing, word-wrap, Dracula colors, poll loop
- **Hooks** shell out to `jq` with `python3` fallback paths, adding ~40% of their line count for JSON parsing that Go handles natively
- **PII scrubbing** was implemented twice (Go in `harness/scrub.go`, bash in `muxcode-pii-scrub.sh`) — now both in Go (`bus/scrub.go` + `harness/scrub.go`, kept in sync)
- **Atlassian wrappers** exist solely to work around Claude Code's curl permission prompt issues — Go's `net/http` eliminates this

The Go binary already has 27 subcommands, a TUI with Dracula colors, JSONL history handling, and idle detection. Most shell logic maps directly to existing bus library functions.

## Architecture decision

**Absorb into `muxcode-agent-bus`** (not new binaries). Reasons:
- Single binary already in PATH, ~10ms startup
- Shares bus state (paths, config, history files)
- Makefile auto-builds from `tools/*/go.mod` — no new modules needed
- New subcommands follow the established `cmd/*.go` → `bus/*.go` pattern

## What stays as shell

| Script | Lines | Reason |
|--------|-------|--------|
| `muxcode.sh` | 543 | Deferred — heavily tmux-dependent with tuned timing, evaluate after Phase 5 |
| `muxcode-preview-hook.sh` | 95 | Pure tmux/vim send-keys with timing-sensitive scrollbind — Go adds no value |
| `muxcode-diff-cleanup.sh` | 22 | 5 lines of tmux send-keys |
| `muxcode-switch-session.sh` | 72 | Interactive fzf picker — shell is the natural fit |
| `muxcode-demo.sh` | 249 | Recording orchestrator (asciinema/ffmpeg/gifski) |
| `build.sh` | 7 | Makefile wrapper |
| `test.sh` | 13 | Go test runner |
| `muxcode-test-wrapper.sh` | 9 | Delegates to test.sh |
| `install.sh` | 205 | First-time setup (interactive, runs once) |
| `test-diff-split.sh` | 348 | Integration test for nvim diff preview |

**Retained:** ~1,563 lines (11 scripts, including `muxcode.sh` deferred)
**Eliminated:** ~3,596 lines (19 scripts across 5 phases)

---

## Phase 1: Log pollers → `console` subcommand

**Scripts:** 10 scripts, ~3,301 lines → single `muxcode-agent-bus log-view <role>` command
**ROI:** Highest — eliminates the most duplicated code in the project

### New files

| File | Purpose |
|------|---------|
| `bus/console.go` | JSONL parsing, word-wrap, ANSI/Dracula formatting, timestamp formatting, terminal width detection, per-role renderers |
| `bus/console_test.go` | Unit tests for parsing, word-wrap, per-role rendering (22 tests) |
| `cmd/console.go` | Handler: `console <role> [--interval N] [--once]` |

### Design

- **Per-role rendering** as a config map (not separate codepaths). Each role entry specifies: title, empty message, recent label, max recent count, renderer function pointer
- **`commit` role** combines git status (branch, staged, modified, untracked) with commit history — absorbs `muxcode-git-status.sh` and `muxcode-commit-log.sh`
- **Screen management:** Clear terminal with ANSI escape, render full buffer, sleep interval. Raw ANSI output — no TUI framework (matches current behavior)
- **`--once` flag** for testing — renders once without looping

### Role config map

| Role | History file | Stats | Special sections |
|------|-------------|-------|-----------------|
| build | `build-history.jsonl` | total/pass/fail | Error output on failure |
| test | `test-history.jsonl` | total/pass/fail | Test suite names, error output |
| review | `review-history.jsonl` | total/pass/fail | Review summary |
| deploy | `deploy-history.jsonl` | total/pass/fail | Deploy output |
| run | `run-history.jsonl` | total count | Command output |
| commit | `commit-history.jsonl` | total/pass/fail | Git status (exec `git` directly) |
| watch | `watch-history.jsonl` | event count | — |
| analyze | `log.jsonl` (filtered) | findings count | Analyst response messages |
| api | `.muxcode/api/history.jsonl` | request count | Status code distribution |

### Changes to existing files

| File | Change |
|------|--------|
| `main.go` | Add `case "console": cmd.Console(args)` |
| `muxcode.sh` | Change left-pane commands from per-role script calls to `muxcode-agent-bus console $ROLE` |

### Scripts eliminated

`muxcode-build-log.sh`, `muxcode-test-log.sh`, `muxcode-review-log.sh`, `muxcode-deploy-log.sh`, `muxcode-runner-log.sh`, `muxcode-commit-log.sh`, `muxcode-watch-log.sh`, `muxcode-analyze-log.sh`, `muxcode-api-log.sh`, `muxcode-git-status.sh`

### Verification

- Unit tests: JSONL parsing, word-wrap at various widths, timestamp formatting, per-role config resolution (22 tests, all passing)
- Integration: write sample JSONL, run `console build --once`, assert output contains expected headers/colors/stats
- Visual: run in live session, compare output side-by-side with old shell scripts

---

## Phase 2: Hooks → `hook` subcommand ✅

**Status:** Complete
**Scripts:** 4 of 6 hooks, ~777 lines → `muxcode-agent-bus hook <name>` subcommands
**Retained as shell:** `muxcode-preview-hook.sh`, `muxcode-diff-cleanup.sh`

### New files

| File | Purpose |
|------|---------|
| `bus/hook.go` | Shared hook helpers: read JSON from stdin, parse tool event, output JSON block decision |
| `bus/hook_test.go` | Unit tests for event parsing, command classification, history writing |
| `cmd/hook.go` | Dispatcher: `hook bash`, `hook guard`, `hook analyze`, `hook inbox-poll` |

### 2a: `hook bash` (replaces muxcode-bash-hook.sh, 559 lines)

The most complex hook. Reads JSON stdin, detects command type (build/test/deploy/git/runner), extracts output, writes JSONL history, triggers chains.

- JSON parsing via `json.Decoder` — eliminates jq/python3 dual-path (~40% of shell script)
- Command classification reuses same patterns (`BUILD_PATTERNS`, etc.) via Go regex or string matching
- History writing reuses existing `cmd/log.go` rotation logic
- Chain triggering calls `bus.Chain()` directly (no subprocess)
- Output extraction: ANSI stripping, truncation, HOME replacement — Go `strings` package

### 2b: `hook guard` (replaces muxcode-edit-guard.sh, 67 lines)

Reads JSON stdin, pattern-matches command against prohibited list, outputs JSON block decision. Pure logic, no tmux/vim interaction.

### 2c: `hook inbox-poll` (replaces muxcode-inbox-poll.sh, 71 lines)

Reads JSON stdin, checks if command is a bus send, polls inbox. Calls `bus.ConsumeInbox()` directly instead of shelling out.

### 2d: `hook analyze` (replaces muxcode-analyze-hook.sh, 80 lines)

Reads JSON stdin, writes trigger file, routes file-change events. The nvim diff cleanup (tmux send-keys) executes via `exec.Command("tmux", "send-keys", ...)` — simple enough for Go.

### Changes to existing files

| File | Change |
|------|--------|
| `main.go` | Add `case "hook": cmd.Hook(args)` |
| `.claude/settings.local.json` | Update hook commands from `muxcode-bash-hook.sh` to `muxcode-agent-bus hook bash` |
| `config/settings.json` | Same hook command updates in template |
| `install.sh` | Update hook paths in settings.json merge logic |
| `Makefile` | Stop installing eliminated hook scripts |

### Scripts eliminated

`muxcode-bash-hook.sh`, `muxcode-edit-guard.sh`, `muxcode-inbox-poll.sh`, `muxcode-analyze-hook.sh`

### Verification

- Unit tests: JSON event parsing, command classification (build vs test vs deploy vs git), history JSONL writing/rotation, guard pattern matching
- Integration: pipe sample tool events through `hook bash`, verify history files written and chains triggered
- Hook latency: measure startup time (`time muxcode-agent-bus hook guard < event.json`) — must stay under 50ms

---

## Phase 3: Utilities → individual subcommands ✅

**Status:** Complete
**Scripts:** 3 scripts, ~149 lines

### 3a: `pii-scrub` (replaces muxcode-pii-scrub.sh, 56 lines)

Copy `ScrubPII()` from `harness/scrub.go` into `bus/scrub.go` (avoids cross-module dependency). CLI reads stdin, scrubs, writes stdout. Drop-in pipe replacement.

| File | Purpose |
|------|---------|
| `bus/scrub.go` | `ScrubPII()` copied from harness, regex patterns |
| `bus/scrub_test.go` | Unit tests for all PII patterns |
| `cmd/scrub.go` | Handler: `pii-scrub` (stdin → stdout) |

### 3b: `watch --monitor` (replaces muxcode-watcher-monitor.sh, 54 lines)

Add `--monitor` flag to existing `watch` subcommand. Uses `bus.IsKeepaliveStale()` and `bus.TouchKeepalive()` which already exist. ~30 lines of Go for the monitor loop.

| File | Change |
|------|--------|
| `cmd/watch.go` | Add `--monitor` flag, monitor loop logic |

### 3c: `compact` (replaces muxcode-compact.sh, 39 lines)

Polls tmux for idle state, sends `/compact` via tmux send-keys. Idle detection (`IsAgentIdle()`) already exists in `bus/notify.go`.

| File | Purpose |
|------|---------|
| `cmd/compact.go` | Handler: `compact [role]` — poll idle, inject /compact |

### Changes to existing files

| File | Change |
|------|--------|
| `main.go` | Add `case "pii-scrub"` and `case "compact"` |
| `muxcode.sh` | Change watcher-monitor launch from `muxcode-watcher-monitor.sh` to `muxcode-agent-bus watch --monitor` |
| `Makefile` | Stop installing eliminated scripts |

### Scripts eliminated

`muxcode-pii-scrub.sh`, `muxcode-watcher-monitor.sh`, `muxcode-compact.sh`

### Verification

- PII scrub: pipe test data with emails/SSN/AWS keys, verify redaction matches harness/scrub.go output
- Watcher monitor: kill watcher process, verify monitor restarts it within 30s
- Compact: run against idle agent pane, verify `/compact` injected

---

## Phase 4: Atlassian wrappers → `atlassian` subcommand ✅

**Status:** Complete
**Scripts:** 2 scripts, 260 lines → `muxcode-agent-bus atlassian <jira|confluence> <action>`

### New files

| File | Purpose |
|------|---------|
| `bus/atlassian.go` | Config loading, HTTP client, Jira read/update/comment, Confluence read/update/search |
| `bus/atlassian_test.go` | Unit tests for config resolution, request building, response parsing |
| `cmd/atlassian.go` | Dispatcher: `atlassian jira read <KEY>`, `atlassian confluence read <ID>`, etc. |

### Design

- Go `net/http` replaces curl — eliminates the permission prompt issue that motivated these wrappers
- Config resolution reuses bus config loading (`.muxcode/config` > `~/.config/muxcode/config`)
- Same CLI interface: `read`, `update`, `comment` (jira), `read`, `update`, `search` (confluence)

### Changes to existing files

| File | Change |
|------|--------|
| `main.go` | Add `case "atlassian": cmd.Atlassian(args)` |
| `skills/jira-update-description.md` | Update command from `muxcode-jira.sh` to `muxcode-agent-bus atlassian jira` |
| `skills/confluence-update-page.md` | Update command from `muxcode-confluence.sh` to `muxcode-agent-bus atlassian confluence` |
| `Makefile` | Stop installing eliminated scripts |

### Scripts eliminated

`muxcode-jira.sh`, `muxcode-confluence.sh`

### Verification

- Unit tests: config resolution, HTTP request construction, response parsing
- Integration: read a known Jira issue, verify output matches old script
- Skill test: run jira-update-description skill end-to-end

---

## Phase 5: Agent launcher → `agent launch` subcommand

**Script:** `muxcode-agent.sh`, 325 lines → `muxcode-agent-bus agent launch <role>`
**Out of scope:** `muxcode.sh` (543 lines) — deferred to future evaluation after Phase 5 experience

### New files

| File | Purpose |
|------|---------|
| `bus/launch.go` | Agent file resolution (3-tier), YAML frontmatter parsing, model selection, CLI arg construction |
| `bus/launch_test.go` | Unit tests for file resolution, frontmatter, model cascade |
| `cmd/launch.go` | Handler: `agent launch <role> [--session S] [--project P]` |

### Design

- 3-tier agent file resolution: `.claude/agents/` > `~/.config/muxcode/agents/` > defaults
- YAML frontmatter extraction (currently awk/sed) → Go `bufio.Scanner`
- Model cascade: per-role env var → global env var → default — currently a bash `case` statement mapping roles to env var names
- Tool profile resolution: calls existing `bus.ResolveTools()` directly
- Skill/context/session-resume prompts: calls existing `bus.FormatContextPrompt()`, `bus.FormatSkillPrompt()` directly
- Constructs Claude Code CLI args and execs `claude` (or `muxcode-llm-harness` for local LLM roles)

### Changes to existing files

| File | Change |
|------|--------|
| `main.go` | Extend existing `agent` case to handle `agent launch` subcommand |
| `muxcode.sh` | Change agent launch calls from `muxcode-agent.sh` to `muxcode-agent-bus agent launch` |
| `Makefile` | Stop installing `muxcode-agent.sh` |

### Scripts eliminated

`muxcode-agent.sh`

### Verification

- Unit tests: 3-tier file resolution, frontmatter parsing, model cascade (per-role env → global → default)
- Integration: launch an agent via `agent launch build`, verify correct Claude Code args
- Side-by-side: compare generated Claude Code command args between old shell and new Go

---

## Phase execution summary

| Phase | Scripts | Lines eliminated | New Go files | Key benefit |
|-------|---------|-----------------|-------------|-------------|
| 1 | 10 log pollers | ~3,301 | 3 (bus/logview.go, test, cmd) | Eliminate 90% code duplication |
| 2 | 4 hooks | ~777 | 3 (bus/hook.go, test, cmd) | Remove jq/python3 dependency, direct bus calls |
| 3 | 3 utilities | ~149 | 4 (bus/scrub.go, test, cmd/scrub, cmd/compact) | Deduplicate PII scrub, eliminate script deps |
| 4 | 2 Atlassian | ~260 | 3 (bus/atlassian.go, test, cmd) | Eliminate curl permission issues |
| 5 | 1 agent launcher | ~325 | 3 (bus/launch.go, test, cmd) | Structured config, eliminate shell case statements |
| **Total** | **20** | **~4,812** | | |

**Deferred:** `muxcode.sh` (543 lines) — evaluate Go port after Phase 5 experience with `muxcode-agent.sh`
