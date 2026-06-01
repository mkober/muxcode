# MuxCode

Multi-agent coding environment built on tmux, Neovim, and Claude Code. Each agent runs in its own tmux window, coordinated through a file-based message bus.

## Tech stack

| Layer | Technology |
|-------|------------|
| Launcher | Go (single `muxcode` binary) |
| Hooks | Bash |
| Bus binary | Go 1.22 (stdlib only, no external deps) |
| Agent definitions | Markdown with YAML frontmatter |
| Terminal multiplexer | tmux >= 3.0 |
| Editor | Neovim |
| AI CLI | Claude Code (`claude`) |

## Directory structure

```
scripts/                      # Hook scripts, utility scripts, pollers
agents/                       # Default agent definition files (.md)
agents/harness/               # Simplified agent definitions for local LLM harness
skills/                       # Default skill definition files (.md)
config/                       # settings.json, tmux.conf, nvim/ (managed nvim config)
config/nvim/                  # Neovim config loaded via NVIM_APPNAME=muxcode
├── init.lua                  # Full lazy.nvim config (Dracula, treesitter, render-markdown, telescope)
└── plugin/startscreen.lua    # MuxCode start screen (logo, agents, shortcuts)
docs/                         # Documentation
tools/muxcode/      # Go module — the bus binary
├── bus/                      # Core library
├── cmd/                      # Subcommand handlers
├── watcher/                  # Bus daemon — inbox poller + trigger file monitor
├── tui/                      # Dracula-themed dashboard TUI
└── main.go                   # Entry point
tools/muxcode-llm-harness/    # Go module — standalone local LLM harness
├── harness/                  # Core library
└── main.go                   # Entry point
```

## Build, test, install

| Command | What it does |
|---------|-------------|
| `./build.sh` | Runs `make install` — builds Go binary, installs agents/configs |
| `./test.sh` | Runs `go vet ./...` and `go test -v ./...` in the bus module |
| `make build` | Builds Go binary to `bin/muxcode` |
| `make install` | Build + install binary to `~/.local/bin/`, agents, skills, configs to `~/.config/muxcode/` |
| `make clean` | Remove `bin/` directory |
| `./install.sh` | First-time setup — checks prereqs, builds, configures tmux and Claude Code hooks |
| `bash scripts/test-diff-split.sh` | Integration test for nvim diff split preview (requires running muxcode session) |
| `bash scripts/test-hot-reload.sh` | Integration test for agent hot reload (requires running muxcode session) |

Both Go modules have **no external dependencies** (stdlib only).

## Code conventions

### Go (bus binary & harness)

- PascalCase for exported identifiers, camelCase for unexported
- Stdlib only — no third-party imports
- Tests in `*_test.go` files, same package (not `_test` suffix)
- Bus directory path: `/tmp/muxcode-bus-{session}/` in `bus/config.go`

### Bash (hooks & utility scripts)

- `set -euo pipefail` for build/test/install scripts (`build.sh`, `test.sh`, `install.sh`)
- Hooks do NOT use `set -e` — they exit gracefully on errors
- 2-space indentation
- `snake_case` for functions, `UPPER_CASE` for environment variables
- JSON parsing: `jq` primary, `python3` fallback

**Editing pitfalls:**

- **Vim `sil!` in pipe chains**: `sil!` only suppresses the immediately following command, NOT the full `|` pipe chain — every command needs its own `sil!` prefix (e.g. `sil! cmd1 | sil! cmd2 | sil! cmd3`). Without this, errors like E35 cause "Press ENTER" prompts that break subsequent commands.
- **Diff preview jump-to-line**: must be sent as a separate `tmux send-keys` after 150ms sleep — the diff needs scrollbind fully active before jumping. Uses `norm! {LINE}Gzz` (not `:N`) because `norm!` properly triggers scrollbind sync between both diff panes.
- **Process substitution in tool profiles**: `Bash(diff *)` does NOT match `diff <(...)` — Claude Code treats `<()` as a special construct requiring explicit `Bash(diff <(*)`.
- **tmux send-keys text + Enter**: must be sent as two separate `tmux send-keys` calls with a brief delay (100ms). Claude Code's TUI can drop the Enter key when it arrives in the same pty write as the preceding text characters — the agent ends up with text in the input buffer but never submits it.

### Agent definitions

- YAML frontmatter with `description:` field (extracted by `ExtractFrontmatter()` in `bus/launch.go`)
- kebab-case filenames (e.g. `code-editor.md`, `git-manager.md`, `dev-server.md`)
- Role-to-filename mapping in `AgentFileName()` in `bus/launch.go`

### Documentation

- 2-space indentation in markdown
- Title Case for H1, Sentence case for H2+
- Prefer tables and code blocks over prose
- Cross-link docs with relative paths (e.g. `docs/architecture.md`)
- When updating docs, augment existing content — don't rewrite or reorganize
- Feature requirements live in `docs/requirements/` — completed specs in `completed/`, in-progress drafts in `drafts/`, backlog at top level
- **Integration test phase required**: every requirements doc MUST include a dedicated integration test phase (typically the final implementation phase). It must contain either: (1) specific test steps written as checkboxes that can be automated (e.g. `- [ ] Reload build+test agents → verify config changed`), or (2) a step to create a test automation script (e.g. `- [ ] Create scripts/test-{feature}.sh with automated verification`). The test phase should validate end-to-end behavior, not just unit tests.

### Permissions (`.claude/settings.local.json`)

- **No hardcoded user paths**: this is an open-source project — never add absolute paths containing usernames or home directories (e.g. `/Users/mkoberlein/...`). Use relative paths (`tools/muxcode/...`) or generic patterns (`~/.config/muxcode/...`).
- User-specific `Read()` permissions for external dirs (nvim plugins, dotfiles, etc.) belong in the user's global `~/.claude/settings.json`, not in the project-local file.

## Key constraints

- **Edit agent delegation**: never runs build, test, deploy, dev servers, API requests, log tailing, AWS commands, git commands (including read-only like `git status`), or GitHub CLI commands (`gh`). All delegated via message bus. On Claude Code, enforcement is via PreToolUse hook (`muxcode hook guard`). On OpenCode (`MUXCODE_EDIT_CLI=opencode`), enforcement is via `DenyTools` in the tool profile (emitted as OpenCode `deny` permission rules) plus daemon-side pane audit (`checkNonHookEdits()`). The edit agent can run on OpenCode with DeepSeek V4 Pro — set `MUXCODE_EDIT_CLI=opencode` to switch, `MUXCODE_EDIT_CLI=claude` (or unset) to switch back. Dev server lifecycle (start, stop, restart, status) goes to the **serve** agent (F5 window). AWS process execution (`aws lambda invoke`, `aws stepfunctions`, `aws s3 ls`, `aws s3 cp`, `aws s3api`) and ad-hoc script execution (integration tests like `scripts/test-*.sh`, one-off shell scripts) go to the **run** agent. Log tailing (`aws logs`, `kubectl logs`, `docker logs`, `tail -f`) goes to the **watch** agent — watch is strictly read-only log tailing, no AWS mutations or data inspection. API testing requests go to the `api` agent (modal-only role, opened via `prefix + i` or `muxcode modal open api`). PR review reads (Copilot comments, CI failures) go to the **commit** agent with action `pr-read` — never to the review agent. Documentation updates (specs, architecture, requirements) go to the **plan** agent with action `update-docs`. See [Architecture](docs/architecture.md).
- **Plan agent scope**: the plan agent (F1) is scoped to docs directories only (`docs/`, `CLAUDE.md`, `README.md`). It can read source code for context but never writes outside docs. Tool profile: `bus`, `readonly`, `common`, plus `Write`, `Edit`, read-only git, `tree`, `python3`, `jq`. The `docs` hosted role is mapped to `plan` (messages to `docs` deliver to the plan agent's inbox). The F1 window also hosts a **research agent** via mode cycling — pressing F1 when on the plan window toggles between plan and research modes.
- **Spec verification**: the plan agent automatically verifies implementation progress against the active requirements spec after review completes. Set the active spec with `muxcode spec set <path>`. When the build→test→review chain finishes successfully, the daemon sends a `verify-spec` request to the plan agent with the spec path and changed files. The plan agent reads the spec and changed code, checks off completed acceptance criteria and phase steps (`- [ ]` → `- [x]`), updates the status field, and reports to edit. Controlled by `NotifyPlanOn` in the review `EventChain` config (default: `["success"]`). Only fires when an active spec is set.
- **Research agent**: runs on OpenCode with DeepSeek V4 Pro on the F1 window (mode index 1). Has its own independent inbox (`inbox/research.jsonl`) — not hosted on edit or plan. Specializes in web searching API docs, platform references, and GitHub projects. Not part of any event chain. Delegates implementation to the active F2 agent via `muxcode mode active --window edit`. Findings persist in `research-history.jsonl` (console) and memory (cross-session).
- **Hook-driven chains**: build→test→review, deploy→run→watch chains are deterministic (bash exit codes), not LLM-driven. Only fires for hook-supporting providers (`provider.SupportsHooks()`). Non-hook providers (OpenCode, Codex CLI, local LLM) use three-layer graceful degradation: (1) config-driven `buildChainInstruction()` generates natural-language chain instructions per role, (2) agent body adaptation via `adaptBodyForNonHookProvider()` (OpenCode) or shared agent config via `WriteAgentConfig()` (Codex) that rewrites hook chain references to manual commands, (3) `CheckSendPolicy()` bypass so non-hook agents can send chain messages that would be blocked for hook agents. See [Hooks](docs/hooks.md).
- **Conditional chains**: `ChainAction` supports a `Conditions` map with 8 condition types (`files_match`, `files_not_match`, `branch_match`, `branch_not_match`, `env_set`, `env_equals`, `output_contains`, `exit_code`). Actions in a `ChainActions` slice are evaluated first-match-wins — first action whose conditions pass (or has no conditions) fires. `ChainContext` carries git state (branch, changed files), command output, and exit code. Git info is lazy-loaded via `PopulateGitInfo()` only when conditions or templates reference it. Subscriptions also support conditions via the same mechanism.
- **Hot reload**: `muxcode reload <role> [--cli <cli>] [--model <model>] [--compact]` stops an agent, reconfigures with optional CLI/model overrides (written to session-scoped runtime override files in `/tmp/muxcode-bus-{session}/config/{role}.env`), and relaunches. Supports multi-role batch reload: `muxcode reload build test review --cli opencode --model opencode-go/minimax-m2.5` reloads 3 agents sequentially (3s gap). `muxcode reload --all --cli opencode` reloads all active agents (excludes edit/auto). `muxcode reload --all --provider claude --cli opencode` filters to only agents currently on Claude. Reload markers suppress daemon health checks during the cycle. Provider selector modal (`prefix + R` or `prefix + b → Provider`) provides visual provider/model selection with multi-agent checkbox section — `a` selects all (excludes edit/auto), `p` selects by current provider, `n` deselects all. Multi-agent confirm shows a live progress view with per-agent status. `muxcode config set/get/list` manages persistent config. Resolution chain: runtime override → per-role env → global env → config file → default. See [Configuration](docs/configuration.md).
- **Codex CLI sandbox**: Codex CLI sandboxes all filesystem writes and blocks outbound network access. This makes it unsuitable for roles that write to `.git` (commit) or push to remotes (commit, deploy). Codex agents that attempt git operations will silently create an alternate `.git` directory in `/tmp/` and fail to push. Only use Codex for read-only roles (review, analyze). Use Claude Code or OpenCode for git and deploy operations.
- **User-initiated commits**: git commits, pushes, and PR creation are never auto-triggered. The automated chain stops at review. Batch changes into logical units — do not commit every small change. Only commit when the user explicitly asks or when a complete logical unit of work is finished. No micro-commits.
- **Attribution stripping**: `LaunchSession()` installs a git `commit-msg` hook (`InstallCommitMsgHook()` in `bus/git_hooks.go`) that strips `Co-authored-by` trailers from every commit. The hook is idempotent (marker-based), chains with existing hooks, and respects `core.hooksPath`. This ensures AI attribution lines never appear in commit history regardless of what the LLM generates.
- **Pre-commit safeguard**: commit delegation blocked when any agent has pending inbox, is busy, or has running procs/spawns. Bypass with `--force`.
- **Auto-CC**: messages from build/test/review/deploy to non-edit agents are copied to edit inbox. Chain/subscription messages use `SendNoCC()` to avoid redundant CC.
- **Agent notifications**: `Notify()` (`bus/notify.go`) writes a timestamp to `trigger-{role}.notify` and wakes agents via send-keys. Non-hook providers (OpenCode TUI, Codex CLI) are routed to `provider.SendWakeUp()` which injects the actual message payload (filtering out self-addressed messages to prevent echo loops). Claude Code agents get "You have new messages" text injection. Display-message (tmux status bar flash) is sent as a human-visible indicator for agents that are active but not idle. Harness panes are skipped (they poll inbox directly).
- **Edit inbox**: two delegation modes — `--wait` blocks until the response arrives (polls every 500ms, timeout: `MUXCODE_INBOX_POLL_TIMEOUT`, default 600s), `--track` creates a tracked task and returns immediately (daemon auto-completes the task and wakes the sender when the response arrives in their inbox). Use `--wait` when the result is needed before proceeding; use `--track` for long-running or fire-and-forget operations where the editor should continue working. Both modes create task entries in `/tmp/muxcode-bus-{session}/tasks/` (view with `muxcode tasks`). `--wait` and `--track` are mutually exclusive.
- **Agent wake-up**: the daemon's `checkIdleAgents()` runs every 5 seconds — for each idle agent (including edit) with actionable inbox messages (request-type only, not response-only), it calls `Notify()` which injects "You have new messages" via send-keys. Agents just process messages, reply, and go idle. No agent polls for messages — all are woken by the daemon. On startup, `LaunchSession()` wakes agents with pre-populated inbox messages — for Claude Code agents via send-keys after reaching the `❯` prompt, for non-hook providers (OpenCode, Codex CLI) via `provider.SendWakeUp()` which injects the startup message payload directly.
- **Daemon identity**: the bus daemon (background supervisor process) uses `daemon` as its bus identity — not `watcher`, which would collide with the `watch` agent (F9 window). `NormalizeBusRole("daemon")` maps to `edit` so reply instructions route to a valid agent. Lifecycle logs show source `daemon`. The `daemon` identity is filtered out of message loop detection.
- **System actions**: `loop-detected`, `compact-recommended`, `proc-complete`, `spawn-complete`, `ollama-down`, `ollama-recovered`, `ollama-restarting`, `agent-down`, `agent-restarting`, `agent-recovered` are excluded from message loop detection (`isSystemAction()`).
- **Agent diagnostics**: `muxcode diagnose <role>` performs automated root cause analysis when an agent isn't responding. Collects evidence from agent state, inbox, notification pipeline, daemon health, and lifecycle timeline, then pattern-matches against 10 known failure modes (stale notified IDs, missed send-keys, idle detection failure, daemon not waking, post-restart wake gap, provider mismatch, reload marker stuck, pending input blocking, no actionable messages, daemon dead). Outputs human-readable report with Dracula colors (default) or JSON (`--json`). `muxcode diagnose --all` produces a summary table for all agents. Exits non-zero on critical findings. Can be run by any agent to diagnose peers — e.g., the edit agent can run `muxcode diagnose commit` when a delegated command times out. See `bus/diagnose.go`.
- **Lifecycle logging**: persistent JSONL logs at `~/.config/muxcode/logs/{session}.log` record launcher sequence, daemon events, agent launches, auto-accept, and cleanup. Survives session cleanup. View with `muxcode lifecycle show [session]`, filter with `--source`, `--level`, `--event`, `--since`. Rotation at 1000 entries (configurable via `MUXCODE_LIFECYCLE_LOG_MAX`). Purge old logs with `lifecycle purge --days 30`.
- **PII scrubbing**: tool output from `api`, `runner`/`run`, and `watch` roles is redacted before entering the LLM conversation. Harness agents use automatic scrubbing in the executor (`harness/scrub.go`). Claude Code agents pipe output through `muxcode pii-scrub`. Patterns: emails, SSN, credit cards (prefix-anchored), phone numbers (separator-required), AWS keys, JWTs, generic secrets/tokens.
- **Daemon self-monitoring**: the daemon writes a Unix timestamp to `watcher.keepalive` at the top of each poll loop. A companion monitor (`muxcode watch --monitor`) checks the keepalive every 15 seconds — if stale (>30s), it kills and relaunches the daemon.
- **Harness circuit breaker**: 3-layer stuck protection — within-turn (filter), within-batch (`MaxAllBlockedTurns=2`), cross-batch (`MaxConsecutiveFailures=3` triggers 30s cooldown). Each batch has 5-minute timeout. See [Agents](docs/agents.md#circuit-breaker).
- **Harness single-shot roles**: build and test roles auto-complete after one successful tool execution (`isSingleShotRole()`). Prevents small models from looping endlessly re-running the same command. After auto-complete, a text-only Ollama call generates the response summary.
- **Harness agent definitions**: `agents/harness/` directory provides simplified agent definitions for local LLMs. Resolution: `agents/harness/` > `.claude/agents/` > `~/.config/muxcode/agents/harness/` > `~/.config/muxcode/agents/`. Shorter, more directive prompts that avoid confusing smaller models.
- **Harness TUI**: Dracula-themed terminal UI with activity log (Ollama calls, tool executions, output previews), status bar (status/uptime left, role/model/provider right), and alternate screen buffer for clean rendering. Tool output previews show the last meaningful line of command output.

## Code reference

### Go bus binary (`tools/muxcode/`)

Build: `cd tools/muxcode && go build .`
Test: `cd tools/muxcode && go test ./...`

| File | Key exports |
|------|-------------|
| `bus/config.go` | `BusDir()`, `InboxPath()`, `LockPath()`, `TriggerFile()`, `PaneTarget()`, `AgentPane()`, `IsSplitLeft()`, `BusRole()`, `NormalizeBusRole()`, `IsKnownRole()`, `WindowForRole()`, `HarnessMarkerPath()`, `GlobalMemoryDir()`, `GlobalMemoryPath()`, `GlobalMemoryArchiveDir()`, `GlobalMemoryArchivePath()`, `ActiveSpecPath()`, `ReadActiveSpec()`, `WriteActiveSpec()`, `ClearActiveSpec()`, path helpers for cron/proc/spawn/webhook/memory |
| `bus/lifecycle.go` | `LifecycleLogDir()`, `LifecycleLogPath()`, `LogLifecycle()`, `LogLifecycleWithPID()`, `ReadLifecycleLog()`, `FilterLifecycleLog()`, `ListLifecycleSessions()`, `PurgeLifecycleLogs()`, `FormatLifecycleEntry()` |
| `bus/message.go` | Message struct, JSONL encoding |
| `bus/inbox.go` | Read/write/consume inbox, `Send()`, `SendNoCC()`, `HasActionableMessages()` |
| `bus/setup.go` | `Init()`, session re-init purge (`resetFile()`, `purgeStaleFiles()`) |
| `bus/inspect.go` | `GetAgentStatus()`, `GetAllAgentStatus()`, `ReadLogHistory()`, `ExtractContext()`, `PreCommitCheck()` |
| `bus/git_hooks.go` | `InstallCommitMsgHook()`, `resolveGitHooksDir()` — auto-installs git `commit-msg` hook to strip `Co-authored-by` trailers in any repo |
| `bus/guard.go` | `ReadHistory()`, `DetectCommandLoop()`, `DetectMessageLoop()`, `CheckLoops()`, `CheckAllLoops()` |
| `bus/compact.go` | `CheckCompaction()`, `CheckRoleCompaction()`, `FormatCompactAlert()`, `FilterNewCompactAlerts()` |
| `bus/dedup.go` | `IsDuplicateMessage()`, `SendIfNotDuplicate()`, `SendNoCCIfNotDuplicate()`, `DedupWindowSecs()` |
| `bus/delivery.go` | `DeliveryStatus`, `CreateDeliveryStatus()`, `MarkDelivered()`, `MarkResponded()`, `ReadDeliveryStatus()`, `ListDeliveryStatuses()`, `CleanExpiredDeliveries()`, `FormatDeliveryStatus()` |
| `bus/task.go` | `Task`, `CreateTask()`, `CompleteTask()`, `TimeoutTask()`, `ReadTask()`, `ListTasks()`, `CleanExpiredTasks()`, `FormatTask()` |
| `bus/scrub.go` | `ScrubPII()`, `IsPIISensitiveRole()`, PII/secret regex patterns (mirrored in harness) |
| `bus/provider.go` | `Provider` interface, `ResolveProvider()`, `ResolveProviderCLI()`, `LocalProvider`, `buildChainInstruction()` (config-driven natural-language chain instructions for non-hook providers), `describeOutcome()`, `describeAction()`, `describeConditions()`, `filterMeaningfulActions()` |
| `bus/provider_claude.go` | `ClaudeCodeProvider` — full Claude Code integration (hooks, idle detection, startup acceptance, compact) |
| `bus/provider_opencode.go` | `OpenCodeProvider` — TUI mode (pane detection, send-keys wake-up, agent config generation, tool profile translation, task completion detection, `adaptBodyForNonHookProvider()`, self-message filtering in `SendWakeUp()`) |
| `bus/provider_codex.go` | `CodexProvider` — TUI mode (`codex -a never --no-alt-screen`, send-keys wake-up with self-message filtering, `.codex/AGENTS.md` generation, heuristic task completion detection) |
| `bus/codex_events.go` | Codex JSONL event types, `ParseCodexEvents()`, `AnalyzeCodexEvents()`, `FormatCodexResult()`, `RunCodexExec()` |
| `bus/conditions.go` | `ChainContext`, `ConditionResult`, `EvaluateConditions()` (AND logic, 8 condition types: `files_match`, `files_not_match`, `branch_match`, `branch_not_match`, `env_set`, `env_equals`, `output_contains`, `exit_code`), `BuildChainContext()`, `BuildChainContextFromFlags()`, `PopulateGitInfo()` (lazy git), `FormatConditionResults()`, `ValidateConditions()`, `fileGlobMatch()` |
| `bus/hook.go` | `ProcessBashHook()`, `ProcessAnalyzeHook()`, `ProcessGuardHook()`, `ResolveChain()`, `ExpandMessage()` |
| `bus/console.go` | `DefaultConsoleConfigs()`, `ConsoleConfig`, `RunConsole()`, per-role renderers (Dracula theme) |
| `bus/profile.go` | `DefaultConfig()`, `MuxcodeConfig`, `ToolProfile`, `EventChain`, `ChainAction` (with `Conditions` map), `ChainActions` (slice with single-object/array JSON marshal/unmarshal), `ResolveTools()`, `ResolveChain()` (accepts `*ChainContext`, first-match-wins on action arrays), `ResolveChainVerbose()`, `ExpandMessageWithContext()` (supports `${branch}`/`${changed_files}` with lazy git), `CheckSendPolicy()` (provider-aware — bypasses deny for non-hook providers), `ChainShouldNotifyAnalyst()` (`NotifyAnalystOn` field), `ChainShouldNotifyPlan()` (`NotifyPlanOn` field — notifies plan agent to verify spec progress), `ValidateConfig()`, `resolveRoleAlias()` (normalizes legacy profile key aliases to canonical role names) |
| `bus/search.go` | BM25: `tokenize()`, `stem()`, `buildCorpus()`, `bm25Score()`, `SearchMemoryBM25()`, `SearchMemoryWithOptions()` |
| `bus/rotation.go` | `NeedsRotation()`, `RotateMemory()`, `PurgeOldArchives()`, `ReadMemoryWithHistory()`, `AllMemoryEntriesWithArchives()`, `ListMemoryRoles()` |
| `bus/launch.go` | `LaunchConfig`, `AgentFileName()`, `RoleCLIEnvVar()`, `RoleClaudeModelEnvVar()`, `RoleClaudeModelDefault()`, `InlineFallbackPrompt()`, `ExtractFrontmatter()`, `ResolveAgentFile()`, `BuildAgentsJSON()`, `ResolveVenv()`, `BuildSharedPrompt()`, `ResolveLaunchConfig()`, `BuildExecArgs()`, `PreLaunchSetup()`. Default windows: `plan edit build test serve review deploy run watch commit` |
| `bus/atlassian.go` | `LoadAtlassianConfig()`, `AtlassianConfig`, `JiraRead()`, `JiraUpdate()`, `JiraComment()`, `JiraReadComments()`, `FormatJiraComments()`, `JiraListLinkTypes()`, `JiraLinkIssues()`, `FormatLinkTypes()`, `JiraLinkType`, `JiraListTransitions()`, `JiraTransitionIssue()`, `FormatTransitions()`, `JiraTransition`, `JiraSearch()`, `FormatJiraSearch()`, `JiraSearchResult`, `JiraCreateSubtask()`, `JiraCommentEntry`, `ConfluenceRead()`, `ConfluenceUpdate()`, `ConfluenceSearch()`, `flattenADF()` |
| `bus/api.go` | API testing: `Environment`, `Collection`, `Request`, `ApiHistoryEntry` structs, CRUD, `ImportApiDir()`, formatters |
| `bus/cron.go` | Cron scheduling: structs, parsing, CRUD, execution, formatting |
| `bus/proc.go` | `StartProc()`, `CheckProcAlive()`, `RefreshProcStatus()`, `StopProc()`, `CleanFinished()` |
| `bus/spawn.go` | `StartSpawn()`, `StopSpawn()`, `RefreshSpawnStatus()`, `GetSpawnResult()`, `CleanFinishedSpawns()` |
| `bus/webhook.go` | `ServeWebhook()`, `WriteWebhookPid()`, `ReadWebhookPid()`, `IsWebhookRunning()`, `StopWebhookProcess()` |
| `bus/workflow.go` | `WorkflowState` (16 states including `StateRunning`, `StateRunFail`, `StateWatching`, `StateWatchFail`), `WorkflowStateEntry`, `ReadWorkflowState()`, `TransitionWorkflow()`, `WithFiles()`, `WithOutcome()`, `FormatWorkflowState()`, `FormatWorkflowStateCompact()`, `WorkflowStateColor()`, `HasNewMessageFrom()` |
| `bus/subscribe.go` | `Subscription` (with `Conditions` map), `AddSubscription()`, `MatchSubscriptions()` (accepts `*ChainContext` for condition evaluation), `FireSubscriptions()` (accepts `*ChainContext`), `ExpandSubscriptionMessage()` (supports `${branch}`/`${changed_files}`) |
| `bus/context.go` | `ContextFilesForRole()`, `AllContextFilesForRole()`, `FormatContextPrompt()`, `FormatContextList()` |
| `bus/detect.go` | `DetectProject()`, `AutoContextFiles()`, `conventionText()`, `FormatDetectOutput()` |
| `bus/demo.go` | `RunDemo()`, `BuiltinScenarios()`, `ScaleDelay()` |
| `bus/ollama.go` | `OllamaClient`, `ChatComplete()`, `CheckHealth()` |
| `bus/tools.go` | `BuildToolDefs()`, `IsToolAllowed()`, `globMatch()` |
| `bus/executor.go` | `ToolExecutor`, `Execute()` — bash/read/glob/grep/write/edit |
| `bus/agent.go` | `AgentLoop()`, `AgentConfig`, `buildSystemPrompt()`, `processMessages()` |
| `bus/health.go` | `CheckOllamaInference()`, `LocalLLMRoles()`, `RestartOllama()`, `RestartLocalAgent()` |
| `bus/agent_health.go` | `IsAgentAlive()`, `IsAgentStopped()`, `StopAgent()`, `StartAgent()`, `CheckAgentHealth()`, `FormatAgentHealthAlert()` |
| `bus/override.go` | `WriteRuntimeOverride()`, `ReadRuntimeOverrides()`, `LoadRuntimeOverrides()`, `ClearRuntimeOverrides()`, `RuntimeOverridePath()` — session-scoped runtime config overrides |
| `bus/reload.go` | `ReloadAgent()`, `ReloadAll()`, `GracefulStop()`, `ReloadTarget()`, `ReloadMarkerPath()`, `IsReloading()`, `IsReloadMarkerStale()`, `CleanStaleReloadMarkers()` |
| `bus/reload_batch.go` | `AgentReloadStatus`, `ActiveAgentStatuses()`, `ReloadableRoles()`, `ReloadResult`, `ReloadBatch()`, `AbbreviateModel()`, `FormatReloadResults()` |
| `bus/remote.go` | `RemoteSession`, `DiscoverSessions()`, `FormatSessionList()`, `RemoteAgentCapture()`, `RemoteAgentIsIdle()`, `RemoteInboxSummary`, `GetRemoteInbox()`, `FormatRemoteInbox()`, `RemoteOverview()`, `TmuxListWindowNames()` — cross-session investigation |
| `bus/provider_options.go` | `ProviderOption`, `AvailableProviders()`, `ProviderByIndex()`, `ProviderByCLI()`, `ResolveActiveAgentWindow()`, `WindowFKey()` — provider selector data layer |
| `bus/config_file.go` | `GetShellConfig()`, `ResolveConfigPath()`, `SetShellConfigValue()` — shell-sourceable config file read/write |
| `bus/watcher_health.go` | `KeepalivePath()`, `IsKeepaliveStale()`, `TouchKeepalive()` — daemon keepalive monitoring |
| `cmd/` | Subcommand handlers (one per CLI command) |
| `watcher/watcher.go` | Bus daemon: inbox polling, trigger debounce, cron/proc/spawn/loop/compaction/ollama checks |
| `tui/` | Dashboard TUI (Dracula theme), Provider selector TUI (`provider_select.go`), Remote session browser TUI (`remote.go`) |
| `tui/remote.go` | `RemoteUI`, `NewRemoteUI()`, `Run()` — interactive cross-session browser with session list, agent detail, and content views (capture/inbox/diagnose) |

### Go LLM harness (`tools/muxcode-llm-harness/`)

Build: `cd tools/muxcode-llm-harness && go build .`
Test: `cd tools/muxcode-llm-harness && go test ./...`

| File | Key exports |
|------|-------------|
| `harness/config.go` | `Config`, `DefaultConfig()`, `InboxPath()`, `HistoryPath()` |
| `harness/ollama.go` | `OllamaClient`, `ChatComplete()`, `CheckHealth()` |
| `harness/bus.go` | `BusClient`, `ConsumeInbox()`, `Send()`, `Lock()/Unlock()`, `ResolveTools()`, `LogHistory()` |
| `harness/tools.go` | `BuildToolDefs()`, `IsToolAllowed()`, `GlobMatch()` |
| `harness/executor.go` | `Executor`, `Execute()` — bash/read/glob/grep/write/edit, `ScrubPII` flag |
| `harness/filter.go` | `Filter`, `Check()`, `isInboxCommand()`, `isSelfSend()`, `commandHash()` |
| `harness/prompt.go` | `BuildSystemPrompt()`, `LocalLLMInstructions()`, `RoleExamples()`, `ReadAgentDefinition()` |
| `harness/loop.go` | `Run()`, `processBatch()`, `logToolToHistory()`, `isSingleShotRole()`, `toolOutputPreview()`, circuit breaker (cooldown, batch timeout), single-shot auto-complete |
| `harness/events.go` | `EventKind` constants (`EventStartup`, `EventToolOutput`, etc.), `Event` struct |
| `harness/tui.go` | `TUISink`, `NewTUISink()`, Dracula-themed TUI with activity log, status bar, alternate screen buffer |
| `harness/scrub.go` | `ScrubPII()`, `IsPIISensitiveRole()`, PII/secret regex patterns |
| `harness/message.go` | `Message`, `ParseMessages()`, `FormatTask()` |

### Bash scripts

- Hooks consume JSON from stdin via `cat` — parse with `jq` or `python3`
- Preview hook detects edit window via `tmux display-message -p '#W'` — exits immediately if not `edit`
- Analyze hook writes trigger file at `/tmp/muxcode-analyze-{session}.trigger` — format: `<timestamp> <filepath>` per line

### Agent definitions, skills, context

- **Agent files**: 3-tier resolution: `.claude/agents/` > `~/.config/muxcode/agents/` > defaults. Frontmatter extraction by `ExtractFrontmatter()` in `bus/launch.go`. See [Agents](docs/agents.md).
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
