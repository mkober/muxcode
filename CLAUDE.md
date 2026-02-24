# MUXcode

Multi-agent coding environment built on tmux, Neovim, and Claude Code. Each agent runs in its own tmux window, coordinated through a file-based message bus.

## Tech stack

| Layer | Technology |
|-------|------------|
| Launcher & hooks | Bash |
| Bus binary | Go 1.22 (stdlib only, no external deps) |
| Agent definitions | Markdown with YAML frontmatter |
| Terminal multiplexer | tmux >= 3.0 |
| Editor | Neovim |
| AI CLI | Claude Code (`claude`) |

## Directory structure

```
muxcode.sh                    # Main launcher — creates tmux session & windows
scripts/
├── muxcode-agent.sh          # Agent launcher — file resolution, permissions, auto-accept
├── muxcode-preview-hook.sh   # PreToolUse — diff preview in nvim (edit window only)
├── muxcode-diff-cleanup.sh   # PreToolUse — close stale diff previews
├── muxcode-edit-guard.sh     # PreToolUse — blocks prohibited commands in edit window (sync)
├── muxcode-analyze-hook.sh   # PostToolUse — route file events, trigger watcher
├── muxcode-bash-hook.sh      # PostToolUse — build-test-review chain + deploy-verify chain
├── muxcode-git-status.sh     # Git status poller for commit window left pane
├── muxcode-watch-log.sh      # Watch history poller for watch window left pane
└── muxcode-test-wrapper.sh   # Test runner wrapper
agents/                        # Default agent definition files (.md)
skills/                        # Default skill definition files (.md)
config/
├── settings.json              # Claude Code hooks template
├── tmux.conf                  # Tmux keybinding snippet
└── nvim.lua                   # Reference nvim snippet (not auto-loaded)
docs/                          # Documentation
tools/muxcode-agent-bus/      # Go module — the bus binary
├── bus/                       # Core library (config, message, inbox, lock, memory, search, notify, cron)
├── cmd/                       # Subcommand handlers (send, inbox, watch, dashboard, etc.)
├── watcher/                   # Inbox poller + trigger file monitor
├── tui/                       # Dracula-themed dashboard TUI
└── main.go                    # Entry point and subcommand dispatch
```

## Build, test, install

| Command | What it does |
|---------|-------------|
| `./build.sh` | Runs `make install` — builds Go binary, installs scripts/agents/configs |
| `./test.sh` | Runs `go vet ./...` and `go test -v ./...` in the bus module |
| `make build` | Builds Go binary to `bin/muxcode-agent-bus` |
| `make install` | Build + install binary to `~/.local/bin/`, scripts, agents, configs to `~/.config/muxcode/` |
| `make clean` | Remove `bin/` directory |
| `./install.sh` | First-time setup — checks prereqs, builds, configures tmux and Claude Code hooks |

The Go module at `tools/muxcode-agent-bus/` has **no external dependencies** (stdlib only). `go.mod` declares `go 1.22` with no `require` block.

## Code conventions

### Go (bus binary)

- PascalCase for exported identifiers, camelCase for unexported
- Stdlib only — no third-party imports
- Tests in `*_test.go` files, same package (not `_test` suffix)
- Bus directory path hardcoded to `/tmp/muxcode-bus-{session}/` in `bus/config.go`

### Bash (launcher & hooks)

- `set -euo pipefail` for launcher scripts (`muxcode.sh`, `build.sh`, `test.sh`, `install.sh`)
- Hooks do NOT use `set -e` — they exit gracefully on errors
- 2-space indentation
- `snake_case` for functions, `UPPER_CASE` for environment variables
- JSON parsing: `jq` primary, `python3` fallback (bash-hook uses both; preview-hook uses python3 for content generation)

### Agent definitions

- YAML frontmatter with `description:` field (extracted by `launch_agent_from_file`)
- kebab-case filenames (e.g. `code-editor.md`, `git-manager.md`)
- Role-to-filename mapping in `agent_name()` function of `scripts/muxcode-agent.sh`

### Documentation

- 2-space indentation in markdown
- Title Case for H1, Sentence case for H2+
- Prefer tables and code blocks over prose
- Cross-link docs with relative paths (e.g. `docs/architecture.md`)

## Architecture summary

### Delegation model

The **edit** agent is the user-facing orchestrator. It **never** runs build, test, deploy, or git commands directly — including read-only git commands like `git status`, `git log`, and `git diff`. **All** git operations must be delegated to the commit agent via the message bus. It delegates via the message bus. All other agents execute autonomously and reply.

### Bus protocol

- Messages are JSONL stored at `/tmp/muxcode-bus-{session}/inbox/{role}.jsonl`
- Three message types: `request`, `response`, `event`
- Auto-CC: messages from build/test/review/deploy to non-edit agents are copied to the edit inbox
- Build-test-review and deploy-verify chains are **hook-driven** (bash exit codes), not LLM-driven

### Hook chain

Five hooks configured in `.claude/settings.json`:

1. `muxcode-edit-guard.sh` — PreToolUse on Bash, **sync** (edit window only) — blocks prohibited commands and returns delegation instructions
2. `muxcode-preview-hook.sh` — PreToolUse on Write/Edit/NotebookEdit, async (edit window only)
3. `muxcode-diff-cleanup.sh` — PreToolUse on Read/Bash/Grep/Glob, async (edit window only)
4. `muxcode-analyze-hook.sh` — PostToolUse on Write/Edit/NotebookEdit, async (all windows)
5. `muxcode-bash-hook.sh` — PostToolUse on Bash, async (all windows)

### Lock mechanism

Agents indicate busy state via lock files at `/tmp/muxcode-bus-{session}/lock/{role}.lock`. The dashboard TUI reads these. Commands: `lock`, `unlock`, `is-locked`.

### Watcher debounce

The bus watcher (`muxcode-agent-bus watch`) uses a two-phase debounce: detect trigger file change, then wait for stability (default 8 seconds). Burst edits are coalesced into a single aggregate analyze event sent to the analyst.

### Cron scheduling

The bus supports interval-based scheduled tasks via `muxcode-agent-bus cron`. Cron entries are JSONL-persisted at `/tmp/muxcode-bus-{session}/cron.jsonl` with execution history at `cron-history.jsonl`.

- Schedule formats: `@every 30s`, `@every 5m`, `@hourly`, `@daily`, `@half-hourly`
- Minimum interval: 30s (enforced by `ParseSchedule`)
- Watcher integration: `checkCron()` runs each poll cycle, `loadCron()` reloads from disk at most every 10s
- Fire-and-forget: no overlap prevention, target agent queues messages in inbox
- CLI: `muxcode-agent-bus cron add|list|remove|enable|disable|history`
- Core code: `bus/cron.go` (structs, parsing, CRUD, execution, formatting), `cmd/cron.go` (CLI)
- Known TODO: read-modify-write race on `cron.jsonl` between watcher and CLI (no file locking, matches existing bus patterns)

### Process management (background tasks)

Agents can launch, track, and auto-notify on background processes via `muxcode-agent-bus proc`. Useful for long-running builds, deploys, or watch-mode test runners.

- `muxcode-agent-bus proc start "<command>" [--dir DIR]` — launch a background process, returns immediately
- `muxcode-agent-bus proc list [--all]` — show running processes (use `--all` to include finished)
- `muxcode-agent-bus proc status <id>` — detailed status for a single process
- `muxcode-agent-bus proc log <id> [--tail N]` — read process output log
- `muxcode-agent-bus proc stop <id>` — send SIGTERM to a running process
- `muxcode-agent-bus proc clean` — remove finished entries and their log files
- Watcher integration: `checkProcs()` runs each poll cycle, sends `proc-complete` events to the owner agent on completion
- Exit code capture: commands are wrapped in a subshell with an `EXIT_CODE:$?` sentinel appended to the log file
- Process detachment: `SysProcAttr{Setpgid: true}` detaches processes from the bus binary
- JSONL storage: `proc.jsonl` (entries), `proc/{id}.log` (per-process output)
- Core code: `bus/proc.go` (ProcEntry, CRUD, StartProc, CheckProcAlive, RefreshProcStatus, StopProc, CleanFinished, formatting), `cmd/proc.go` (CLI)

### Session inspection

Agents can programmatically query each other's state and message history via CLI:

- `muxcode-agent-bus status` — all-agents overview (role, busy/idle, inbox count, last activity)
- `muxcode-agent-bus status --json` — JSON output for programmatic use
- `muxcode-agent-bus history <role>` — recent messages to/from a role (from `log.jsonl`)
- `muxcode-agent-bus history <role> --limit N` — limit to last N messages (default: 20)
- `muxcode-agent-bus history <role> --context` — markdown block for prompt injection
- Core code: `bus/inspect.go` (AgentStatus struct, GetAgentStatus, GetAllAgentStatus, ReadLogHistory, ExtractContext, formatting), `cmd/status.go`, `cmd/history.go`
- Data sources: `log.jsonl` (message log), lock files (busy state), inbox files (pending messages)

### Loop detection / guardrails

The bus detects repetitive agent patterns and auto-escalates to the edit agent via `muxcode-agent-bus guard`.

- **Command loops**: same command failing N+ times consecutively (from `{role}-history.jsonl`)
- **Message loops**: repeated `(from, to, action)` tuples or ping-pong patterns (from `log.jsonl`)
- CLI: `muxcode-agent-bus guard [role] [--json] [--threshold N] [--window N]`
- Watcher integration: `checkLoops()` runs every 30s, sends `loop-detected` events to edit
- Dedup: alerts suppressed within 5-minute cooldown per `(role, type, command/peer)` key
- Command normalization: strips `cd ... &&`, env vars, `bash -c`, `2>&1`, collapses whitespace
- Core code: `bus/guard.go` (HistoryEntry, LoopAlert, ReadHistory, DetectCommandLoop, DetectMessageLoop, CheckLoops, CheckAllLoops, formatting), `cmd/guard.go` (CLI)
- Default thresholds: 3 for command loops, 4 for message loops; default window: 300s (5 minutes)

### Pre-commit safeguard

The `send` command blocks commit delegation when agents have pending work, preventing incomplete commits.

- **Trigger**: `muxcode-agent-bus send commit commit "..."` (and other commit actions: stage, push, merge, rebase, tag)
- **Excluded roles**: edit (sender), commit (target), watch (passive watcher)
- **Checked conditions** for all other agents: pending inbox messages, busy (locked), running background procs, running spawns
- **Bypass**: `--force` flag on the send command skips the check
- **Read-only git ops not blocked**: status, log, diff, pr-read actions pass through without the check
- Core code: `bus/inspect.go` (`PreCommitCheck`), `cmd/send.go` (`isCommitAction`, `--force` flag)

### Auto session compaction

The watcher monitors agent context size and staleness, sending `compact-recommended` events when compaction is advisable.

- **Metrics tracked per role**: memory file size, history file size, log file size
- **Threshold logic** — both conditions must be met: total tracked size > 512 KB AND time since last compact > 2 hours
- **Watcher integration**: `checkCompaction()` runs every 120 seconds, alerts deduped with 10-minute cooldown via `lastAlertKey` map
- **Alert target**: sent to the role itself (the agent should compact its own context)
- Core code: `bus/compact.go` (`CompactAlert`, `CompactThresholds`, `CheckCompaction()`, `CheckRoleCompaction()`, `FormatCompactAlert()`, `FilterNewCompactAlerts()`)
- Watcher code: `watcher/watcher.go` (`checkCompaction()`, `lastCompactCheck` field)

### Session re-init (stale data purge)

When a MUXcode session restarts with the same name, `Init()` detects the existing bus directory and purges stale data to prevent false watcher alerts (loop-detected, compact-recommended) from the previous session.

- **Detection**: `os.Stat(busDir)` — if the directory exists, `reInit` flag is set
- **Truncated files** (path preserved for writers): inboxes, `log.jsonl`, `cron.jsonl`, `proc.jsonl`, `spawn.jsonl`, `{role}-history.jsonl`, `cron-history.jsonl`
- **Removed files** (recreated on demand): session meta (`session/*.json`), lock files (`lock/*.lock`), proc logs (`proc/*.log`), orphaned spawn inboxes (`inbox/spawn-*.jsonl`), trigger file
- **Preserved**: memory files (`.muxcode/memory/`) — persistent learnings survive re-init
- **Watcher grace period**: `lastLoopCheck` and `lastCompactCheck` initialized to `time.Now()` in `New()`, so loop detection (30s) and compaction checks (120s) skip the first interval
- Core code: `bus/setup.go` (`Init()`, `resetFile()`, `purgeStaleFiles()`), `watcher/watcher.go` (`New()`)

### Agent spawn

Agents can programmatically create temporary agent sessions for one-off tasks via `muxcode-agent-bus spawn`. The spawned agent runs in its own tmux window, receives its task via the bus inbox, and results are collected from the session log.

- `muxcode-agent-bus spawn start <role> "<task>"` — create window, seed inbox, launch agent, track
- `muxcode-agent-bus spawn list [--all]` — show running spawns (use `--all` for completed too)
- `muxcode-agent-bus spawn status <id>` — detailed status for a spawn entry
- `muxcode-agent-bus spawn result <id>` — get last message sent by the spawn agent
- `muxcode-agent-bus spawn stop <id>` — kill tmux window, mark stopped, notify owner
- `muxcode-agent-bus spawn clean` — remove finished entries and their inbox files
- **Launch mechanism**: `AGENT_ROLE=spawn-{id} muxcode-agent.sh <base-role>` — base role determines agent definition/tools, AGENT_ROLE determines bus identity
- **Task seeding**: task message pre-seeded in spawn's inbox before launch, agent notified after 2s delay
- **Result collection**: `GetSpawnResult()` reads `log.jsonl` for the last message FROM the spawn role
- **Completion detection**: watcher's `checkSpawns()` checks if tmux window still exists; if gone, marks completed and sends `spawn-complete` event to owner
- **Pre-commit safeguard**: running spawns block commits (same as running procs)
- **Role validation**: `IsSpawnRole()` + `IsKnownRole()` accepts `spawn-` prefixed roles dynamically
- JSONL storage: `spawn.jsonl` (entries)
- Core code: `bus/spawn.go` (SpawnEntry, CRUD, StartSpawn, StopSpawn, RefreshSpawnStatus, GetSpawnResult, CleanFinishedSpawns, formatting, findAgentLauncher), `cmd/spawn.go` (CLI)
- Watcher code: `watcher/watcher.go` (`checkSpawns()`)

## Working on each area

### Go bus code

- Packages: `bus/` (core), `cmd/` (subcommands), `watcher/` (monitor), `tui/` (dashboard)
- Build: `cd tools/muxcode-agent-bus && go build .`
- Test: `cd tools/muxcode-agent-bus && go test ./...`
- Bus directory path is in `bus/config.go` — `BusDir()`, `InboxPath()`, `LockPath()`, `TriggerFile()`, `CronPath()`, `CronHistoryPath()`, `ProcDir()`, `ProcPath()`, `ProcLogPath()`, `SpawnPath()`
- Pane targeting logic in `bus/config.go` — `PaneTarget()`, `AgentPane()`, `IsSplitLeft()`
- Session inspection in `bus/inspect.go` — `GetAgentStatus()`, `GetAllAgentStatus()`, `ReadLogHistory()`, `ExtractContext()`, `PreCommitCheck()`
- Loop detection in `bus/guard.go` — `ReadHistory()`, `DetectCommandLoop()`, `DetectMessageLoop()`, `CheckLoops()`, `CheckAllLoops()`
- Process management in `bus/proc.go` — `StartProc()`, `CheckProcAlive()`, `RefreshProcStatus()`, `StopProc()`, `CleanFinished()`
- Agent spawn in `bus/spawn.go` — `StartSpawn()`, `StopSpawn()`, `RefreshSpawnStatus()`, `GetSpawnResult()`, `CleanFinishedSpawns()`
- Compaction monitoring in `bus/compact.go` — `CheckCompaction()`, `CheckRoleCompaction()`, `FormatCompactAlert()`, `FilterNewCompactAlerts()`
- Session re-init in `bus/setup.go` — `Init()`, `resetFile()`, `purgeStaleFiles()`

### Bash scripts

- Hooks consume JSON from stdin via `cat` — parse with `jq` or `python3`
- Preview hook detects edit window via `tmux display-message -p '#W'` — exits immediately if not `edit`
- Analyze hook writes trigger file at `/tmp/muxcode-analyze-{session}.trigger` — format: `<timestamp> <filepath>` per line

### Agent definitions

- Override by placing files in `.claude/agents/` (project) or `~/.config/muxcode/agents/` (global)
- Frontmatter extraction by `launch_agent_from_file` — uses `awk` to strip `---` delimiters, `jq` to build `--agents` JSON
- Project-local agent files use `--agent <name>` natively; external files are read, stripped, and passed via `--agents` JSON
- `agent_name()` maps roles to filenames; `allowed_tools()` maps roles to `--allowedTools` flags

### Configuration

- Shell-sourceable config files — resolution order: `$MUXCODE_CONFIG` > `.muxcode/config` > `~/.config/muxcode/config` > defaults
- Variables set in higher-priority configs completely replace lower-priority values (bash source semantics)
- `MUXCODE_SPLIT_LEFT` is read by both `muxcode.sh` (window layout) and the bus binary (pane targeting in `bus/config.go`)

### Skill definitions

- Markdown files with YAML frontmatter (`name`, `description`, `roles`, `tags`)
- Three-tier resolution: `.muxcode/skills/` (project) > `~/.config/muxcode/skills/` (user) > `skills/` (defaults)
- Project-local skills shadow user-level skills by name
- Empty `roles: []` means the skill applies to all roles
- kebab-case filenames (e.g. `go-testing.md`, `code-review-checklist.md`)
- CLI: `muxcode-agent-bus skill list|load|search|create|prompt`
- Skills are auto-injected into agent prompts at launch via `skill prompt <role>`
- See [Skills requirements](docs/requirements/skills-plugin.md) for feature requirements

### Demo mode

- Go subcommand: `muxcode-agent-bus demo run [SCENARIO] [--speed FACTOR] [--dry-run] [--no-switch]`
- GIF capture: `scripts/muxcode-demo.sh [--speed FACTOR] [--output FILE]`
- Core code: `bus/demo.go` (DemoStep, DemoScenario, RunDemo, BuiltinScenarios, ScaleDelay), `cmd/demo.go` (CLI)
- Built-in scenario `build-test-review`: 20 steps, ~20s at 1.0x — edit→build→test→review→commit cycle
- Messages use `From: "demo"` so agents see them but they're identifiable as demo traffic
- `--dry-run` prints steps without executing (no tmux needed) — used in tests
- `--no-switch` skips tmux window switching (headless mode)
- `--speed` multiplier: 2.0 = fast (GIF recording), 0.5 = slow (live talks)
- GIF target: <5 MB, 12 fps, 960px wide; requires `ffmpeg` + `gifski` (Homebrew)
- GIF capture auto-detects screen device via `ffmpeg -f avfoundation` (device index varies per machine)
- Output goes to `assets/demo.gif`, embedded in README

### Documentation

- Cross-link between docs using relative paths (e.g. `[Architecture](docs/architecture.md)`)
- When updating docs, augment existing content — don't rewrite or reorganize
- Keep tables and code blocks as the primary format
- Feature requirements live in `docs/requirements/` — see [backlog](docs/requirements/backlog.md) for the full list
