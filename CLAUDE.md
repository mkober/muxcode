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
scripts/                      # Hook scripts, agent launcher, pollers
agents/                       # Default agent definition files (.md)
skills/                       # Default skill definition files (.md)
config/                       # settings.json, tmux.conf, nvim.lua
docs/                         # Documentation
tools/muxcode-agent-bus/      # Go module — the bus binary
├── bus/                      # Core library
├── cmd/                      # Subcommand handlers
├── watcher/                  # Inbox poller + trigger file monitor
├── tui/                      # Dracula-themed dashboard TUI
└── main.go                   # Entry point
tools/muxcode-llm-harness/    # Go module — standalone local LLM harness
├── harness/                  # Core library
└── main.go                   # Entry point
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

Both Go modules have **no external dependencies** (stdlib only).

## Code conventions

### Go (bus binary & harness)

- PascalCase for exported identifiers, camelCase for unexported
- Stdlib only — no third-party imports
- Tests in `*_test.go` files, same package (not `_test` suffix)
- Bus directory path: `/tmp/muxcode-bus-{session}/` in `bus/config.go`

### Bash (launcher & hooks)

- `set -euo pipefail` for launcher scripts (`muxcode.sh`, `build.sh`, `test.sh`, `install.sh`)
- Hooks do NOT use `set -e` — they exit gracefully on errors
- 2-space indentation
- `snake_case` for functions, `UPPER_CASE` for environment variables
- JSON parsing: `jq` primary, `python3` fallback

**Editing pitfalls:**

- **Vim `sil!` in pipe chains**: `sil!` only suppresses the immediately following command, NOT the full `|` pipe chain — every command needs its own `sil!` prefix (e.g. `sil! cmd1 | sil! cmd2 | sil! cmd3`). Without this, errors like E35 cause "Press ENTER" prompts that break subsequent commands.
- **Diff preview jump-to-line**: must be sent as a separate `tmux send-keys` after 150ms sleep — the diff needs scrollbind fully active before jumping. Uses `norm! {LINE}Gzz` (not `:N`) because `norm!` properly triggers scrollbind sync between both diff panes.
- **Process substitution in tool profiles**: `Bash(diff *)` does NOT match `diff <(...)` — Claude Code treats `<()` as a special construct requiring explicit `Bash(diff <(*)`.

### Agent definitions

- YAML frontmatter with `description:` field (extracted by `launch_agent_from_file`)
- kebab-case filenames (e.g. `code-editor.md`, `git-manager.md`)
- Role-to-filename mapping in `agent_name()` in `scripts/muxcode-agent.sh`

### Documentation

- 2-space indentation in markdown
- Title Case for H1, Sentence case for H2+
- Prefer tables and code blocks over prose
- Cross-link docs with relative paths (e.g. `docs/architecture.md`)
- When updating docs, augment existing content — don't rewrite or reorganize
- Feature requirements live in `docs/requirements/`

## Key constraints

- **Edit agent delegation**: never runs build, test, deploy, log tailing, or git commands (including read-only like `git status`). All delegated via message bus. See [Architecture](docs/architecture.md).
- **Hook-driven chains**: build→test→review and deploy→verify chains are deterministic (bash exit codes), not LLM-driven. See [Hooks](docs/hooks.md).
- **User-initiated commits**: git commits, pushes, and PR creation are never auto-triggered. The automated chain stops at review.
- **Pre-commit safeguard**: commit delegation blocked when any agent has pending inbox, is busy, or has running procs/spawns. Bypass with `--force`.
- **Auto-CC**: messages from build/test/review/deploy to non-edit agents are copied to edit inbox. Chain/subscription messages use `SendNoCC()` to avoid redundant CC.
- **Edit notifications**: edit uses passive `display-message` (tmux status bar flash) — never `send-keys`. Injecting text into the edit pane conflicts with user input and causes conversation loops. See `notifyEdit()` in `bus/notify.go`.
- **Edit inbox polling**: use `--wait` flag on send commands (`muxcode-agent-bus send <to> <action> "<msg>" --wait`) to poll the sender's inbox every 2 seconds until a response arrives (timeout: `MUXCODE_INBOX_POLL_TIMEOUT`, default 120s). The response is printed to stdout as part of the Bash tool result — no manual "check inbox" needed.
- **System actions**: `loop-detected`, `compact-recommended`, `proc-complete`, `spawn-complete`, `ollama-down`, `ollama-recovered`, `ollama-restarting` are excluded from message loop detection (`isSystemAction()`).

## Code reference

### Go bus binary (`tools/muxcode-agent-bus/`)

Build: `cd tools/muxcode-agent-bus && go build .`
Test: `cd tools/muxcode-agent-bus && go test ./...`

| File | Key exports |
|------|-------------|
| `bus/config.go` | `BusDir()`, `InboxPath()`, `LockPath()`, `TriggerFile()`, `PaneTarget()`, `AgentPane()`, `IsSplitLeft()`, `HarnessMarkerPath()`, path helpers for cron/proc/spawn/webhook/memory |
| `bus/message.go` | Message struct, JSONL encoding |
| `bus/inbox.go` | Read/write/consume inbox, `Send()`, `SendNoCC()` |
| `bus/setup.go` | `Init()`, session re-init purge (`resetFile()`, `purgeStaleFiles()`) |
| `bus/inspect.go` | `GetAgentStatus()`, `GetAllAgentStatus()`, `ReadLogHistory()`, `ExtractContext()`, `PreCommitCheck()` |
| `bus/guard.go` | `ReadHistory()`, `DetectCommandLoop()`, `DetectMessageLoop()`, `CheckLoops()`, `CheckAllLoops()` |
| `bus/compact.go` | `CheckCompaction()`, `CheckRoleCompaction()`, `FormatCompactAlert()`, `FilterNewCompactAlerts()` |
| `bus/profile.go` | `DefaultConfig()`, `MuxcodeConfig`, `ToolProfile`, `ResolveTools()`, `ChainShouldNotifyAnalyst()` (`NotifyAnalystOn` field) |
| `bus/search.go` | BM25: `tokenize()`, `stem()`, `buildCorpus()`, `bm25Score()`, `SearchMemoryBM25()`, `SearchMemoryWithOptions()` |
| `bus/rotation.go` | `NeedsRotation()`, `RotateMemory()`, `PurgeOldArchives()`, `ReadMemoryWithHistory()`, `AllMemoryEntriesWithArchives()`, `ListMemoryRoles()` |
| `bus/cron.go` | Cron scheduling: structs, parsing, CRUD, execution, formatting |
| `bus/proc.go` | `StartProc()`, `CheckProcAlive()`, `RefreshProcStatus()`, `StopProc()`, `CleanFinished()` |
| `bus/spawn.go` | `StartSpawn()`, `StopSpawn()`, `RefreshSpawnStatus()`, `GetSpawnResult()`, `CleanFinishedSpawns()` |
| `bus/webhook.go` | `ServeWebhook()`, `WriteWebhookPid()`, `ReadWebhookPid()`, `IsWebhookRunning()`, `StopWebhookProcess()` |
| `bus/subscribe.go` | `AddSubscription()`, `MatchSubscriptions()`, `FireSubscriptions()`, `ExpandSubscriptionMessage()` |
| `bus/context.go` | `ContextFilesForRole()`, `AllContextFilesForRole()`, `FormatContextPrompt()`, `FormatContextList()` |
| `bus/detect.go` | `DetectProject()`, `AutoContextFiles()`, `conventionText()`, `FormatDetectOutput()` |
| `bus/demo.go` | `RunDemo()`, `BuiltinScenarios()`, `ScaleDelay()` |
| `bus/ollama.go` | `OllamaClient`, `ChatComplete()`, `CheckHealth()` |
| `bus/tools.go` | `BuildToolDefs()`, `IsToolAllowed()`, `globMatch()` |
| `bus/executor.go` | `ToolExecutor`, `Execute()` — bash/read/glob/grep/write/edit |
| `bus/agent.go` | `AgentLoop()`, `AgentConfig`, `buildSystemPrompt()`, `processMessages()` |
| `bus/health.go` | `CheckOllamaInference()`, `LocalLLMRoles()`, `RestartOllama()`, `RestartLocalAgent()` |
| `cmd/` | Subcommand handlers (one per CLI command) |
| `watcher/watcher.go` | Unified watcher: inbox polling, trigger debounce, cron/proc/spawn/loop/compaction/ollama checks |
| `tui/` | Dashboard TUI (Dracula theme) |

### Go LLM harness (`tools/muxcode-llm-harness/`)

Build: `cd tools/muxcode-llm-harness && go build .`
Test: `cd tools/muxcode-llm-harness && go test ./...`

| File | Key exports |
|------|-------------|
| `harness/config.go` | `Config`, `DefaultConfig()`, `InboxPath()`, `HistoryPath()` |
| `harness/ollama.go` | `OllamaClient`, `ChatComplete()`, `CheckHealth()` |
| `harness/bus.go` | `BusClient`, `ConsumeInbox()`, `Send()`, `Lock()/Unlock()`, `ResolveTools()`, `LogHistory()` |
| `harness/tools.go` | `BuildToolDefs()`, `IsToolAllowed()`, `GlobMatch()` |
| `harness/executor.go` | `Executor`, `Execute()` — bash/read/glob/grep/write/edit |
| `harness/filter.go` | `Filter`, `Check()`, `isInboxCommand()`, `isSelfSend()`, `commandHash()` |
| `harness/prompt.go` | `BuildSystemPrompt()`, `LocalLLMInstructions()`, `RoleExamples()`, `ReadAgentDefinition()` |
| `harness/loop.go` | `Run()`, `processBatch()`, `logToolToHistory()` |
| `harness/message.go` | `Message`, `ParseMessages()`, `FormatTask()` |

### Bash scripts

- Hooks consume JSON from stdin via `cat` — parse with `jq` or `python3`
- Preview hook detects edit window via `tmux display-message -p '#W'` — exits immediately if not `edit`
- Analyze hook writes trigger file at `/tmp/muxcode-analyze-{session}.trigger` — format: `<timestamp> <filepath>` per line

### Agent definitions, skills, context

- **Agent files**: 3-tier resolution: `.claude/agents/` > `~/.config/muxcode/agents/` > defaults. Frontmatter extraction by `launch_agent_from_file`. See [Agents](docs/agents.md).
- **Skill files**: 3-tier resolution: `.muxcode/skills/` > `~/.config/muxcode/skills/` > `skills/`. YAML frontmatter with `name`, `description`, `roles`, `tags`.
- **Context files**: `context.d/shared/*.md` (all roles) + `context.d/<role>/*.md`. Priority: project > user > auto-detected.
- **Tool profiles**: `bus/profile.go` — per-role permissions with `Include` (shared groups), `CdPrefix`, `Tools`. See [Agents](docs/agents.md#tool-profiles).
- **Config files**: shell-sourceable, resolution: `$MUXCODE_CONFIG` > `.muxcode/config` > `~/.config/muxcode/config`. See [Configuration](docs/configuration.md).

## See also

- [Architecture](docs/architecture.md) — system design, data flows, bus protocol, left-pane pollers, session re-init
- [Agent Bus CLI](docs/agent-bus.md) — full CLI reference for all subcommands
- [Agents](docs/agents.md) — roles, permissions, local LLM, tool profiles, Ollama health, LLM harness
- [Hooks](docs/hooks.md) — hook system, chain behavior, customization
- [Configuration](docs/configuration.md) — env vars, directory structure, examples
- [Backlog](docs/requirements/backlog.md) — planned features
