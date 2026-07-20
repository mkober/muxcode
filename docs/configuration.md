# Configuration

## Config File

Muxcode uses a shell-sourceable config file. Resolution order:

1. `$MUXCODE_CONFIG` — explicit path (set this env var to use a custom location)
2. `./.muxcode/config` — project-local config
3. `~/.config/muxcode/config` — user global config
4. Built-in defaults

Variables set in a higher-priority config completely replace lower-priority values (bash source semantics). To extend rather than replace a value, use the `${VAR:-default}` pattern in your config file.

The config file is a plain bash script that sets environment variables:

```bash
# ~/.config/muxcode/config
MUXCODE_PROJECTS_DIR="$HOME/Projects,$HOME/Work"
MUXCODE_EDITOR="nvim"
MUXCODE_SHELL_INIT="source ~/.venv/bin/activate"
```

## Environment Variables

### Session Settings

| Variable | Default | Description |
|----------|---------|-------------|
| `MUXCODE_PROJECTS_DIR` | `$HOME` | Directories to scan for git projects (comma-separated) |
| `MUXCODE_SCAN_DEPTH` | `3` | Max depth for project discovery via `find` |
| `MUXCODE_EDITOR` | `nvim` | Editor command for the edit window |
| `MUXCODE_NVIM_APPNAME` | `muxcode/nvim` | Neovim `NVIM_APPNAME` — isolates muxcode's nvim config from your personal `~/.config/nvim/` |
| `MUXCODE_AGENT_CLI` | `claude` | Default AI CLI provider (`claude`, `opencode`, `codex`, or `local`) |
| `MUXCODE_{ROLE}_CLI` | (unset) | Per-role AI CLI override (e.g. `MUXCODE_BUILD_CLI=opencode`). See [Multi-CLI providers](#multi-cli-providers) |
| `MUXCODE_SHELL_INIT` | (empty) | Command to run in each new tmux pane (e.g. activate a virtualenv) |

### Window Layout

| Variable | Default | Description |
|----------|---------|-------------|
| `MUXCODE_WINDOWS` | `plan edit build test serve review deploy run watch commit` | Space-separated list of windows to create |
| `MUXCODE_ROLE_MAP` | (empty) | Space-separated `window=role` mappings for windows whose role differs from name. Not needed for built-in roles — `commit`, `serve`, `analyze`, and `run` are now canonical names matching their window names. Only needed for custom roles (e.g. `docs=documentor`). |
| `MUXCODE_SPLIT_LEFT` | `plan edit build test serve review deploy run commit watch` | Space-separated windows that have a left pane (tool) + right pane (agent) |

**Auto-refit on resize**: `config/tmux.conf` registers a `client-resized` hook (tmux 3.0+) that runs `muxcode resize` to re-fit every window in **every session** — including detached subsessions — to the attached client whenever the terminal changes size (monitor resolution change, tile, or reattach). Attached sessions are refit with `resize-window -A`; detached sessions (where `-A` is a no-op) get the fit size read from an attached window and pushed explicitly. This prevents any session from being clipped at its old geometry without needing a restart. See [Architecture → Window Resize Flow](architecture.md#window-resize-flow) for the two-pass `muxcode resize` design and the cross-session reasons it replaced the old single-session xargs one-liner.

### Hook Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `MUXCODE_BUILD_PATTERNS` | `./build.sh\|pnpm*build\|go*build\|make\|cargo*build\|cdk*synth\|tsc` | Pipe-separated patterns for build command detection |
| `MUXCODE_TEST_PATTERNS` | `./test.sh\|jest\|pnpm*test\|pytest\|go*test\|go*vet\|cargo*test\|vitest` | Pipe-separated patterns for test command detection |
| `MUXCODE_DEPLOY_PATTERNS` | `cdk*diff\|cdk*deploy\|cdk*destroy\|...` | Pipe-separated patterns for deploy command detection (all deploy commands, logged to history) |
| `MUXCODE_DEPLOY_APPLY_PATTERNS` | `cdk*deploy\|cdk*destroy\|terraform*apply\|...` | Pipe-separated patterns for deploy-apply commands (mutation-only, triggers verify chain) |
| `MUXCODE_ROUTE_RULES` | `test\|spec=test cdk\|stack\|construct\|terraform\|pulumi=deploy .ts\|.js\|.py\|.go\|.rs=build` | Space-separated `pattern=target` rules for file-change routing |
| `MUXCODE_PREVIEW_SKIP` | `/.claude/settings.json /.claude/CLAUDE.md /.muxcode/` | Space-separated substrings — skip diff preview for matching files |

### Agent Bus

| Variable | Default | Description |
|----------|---------|-------------|
| `BUS_SESSION` | (auto-detected) | Session name for the bus directory |
| `AGENT_ROLE` | (auto-detected) | Current agent's role name |
| `BUS_MEMORY_DIR` | `.muxcode/memory/` | Path to persistent memory directory |
| `MUXCODE_ROLES` | (empty) | Comma-separated extra roles to add to the known roles list |
| `MUXCODE_SPLIT_LEFT` | `plan edit build test serve review deploy run commit watch` | See Window Layout above — also read by the bus binary for pane targeting |
| `MUXCODE_DEDUP_WINDOW` | `30` | Dedup window in seconds for duplicate message suppression (set to 0 to disable) |
| `MUXCODE_INBOX_POLL_TIMEOUT` | `600` | Timeout in seconds for `send --wait` polling |
| `MUXCODE_LIFECYCLE_LOG_MAX` | `1000` | Max entries per lifecycle log before rotation |
| `MUXCODE_WAIT_DEGRADE_SECS` | `90` | Cap (seconds) before `send --wait` auto-degrades into a tracked task and returns, unblocking the sender. Set to `0` to disable (full blocking up to `MUXCODE_INBOX_POLL_TIMEOUT`) |
| `MUXCODE_RELAY_SUPPRESS_THRESHOLD` | `4` | Identical `(from,to,action)` request relays from a non-edit sender within the window past this count are suppressed (relay-loop guard). Set to `0` to disable |
| `MUXCODE_RELAY_SUPPRESS_WINDOW` | `300` | Window in seconds for relay-loop suppression counting |
| `MUXCODE_ACTIVE_WATCHDOG_SECS` | `600` | Daemon advisory threshold (seconds) — an agent continuously active past this is nudged to summarize + escalate (runaway-think guard). Set to `0` to disable |
| `MUXCODE_STUCK_RELOAD_DISABLE` | (unset) | Set to `1` to disable the daemon's stuck-provider auto-reload watchdog for non-hook agents |
| `MUXCODE_DELIVERY_ACK` | (unset → **ON**) | The **receipt-based delivery cutover** is now the **default**: agents self-poll their own inbox and the daemon's `checkPollHealth` receipt-gap backstop takes over, bypassing the pane-scrape delivery machinery (`checkIdleAgents`/`checkParkedInput`/`checkPaneSweep`). Set to `off`/`0`/`false`/`no` to opt out, or `on`/`1`/`true`/`yes` to pin it on. **Read at daemon startup** — for a live rollback with no restart use `muxcode delivery-ack off` (writes a `delivery-ack.off` marker the daemon re-reads every poll; `on` clears it). Default ON as a soak; physical removal of the bypassed machinery is still deferred (see [delivery-acknowledgement](requirements/drafts/delivery-acknowledgement.md) and the [mis-fire limitation](requirements/backlog/remove-gated-pane-scrape-delivery.md)) |
| `MUXCODE_DELIVERY_ACK_DISABLE` | (unset) | Hard kill switch — set to `1` to force the old pane-scrape delivery path even though the cutover is now on by default. Highest-precedence rollback valve (needs a daemon restart); for an instant restart-free rollback use `muxcode delivery-ack off` instead |
| `MUXCODE_COMMIT_AUTHORITY_ROLES` | `edit` | Comma-separated roles allowed to request a git mutation (`commit`, `stage`, `push`, `merge`, `rebase`, `tag`) from the commit agent — a send from any other role is rejected at the bus. **Not bypassable with `--force`.** Set to `edit,auto` to let the autonomous story-lifecycle agent commit by design; set to the empty string to deny every role, including edit. The commit agent's read-only `pr-read` action is never gated |

### Claude model selection

Built-in defaults by role:

| Roles | Default model |
|-------|---------------|
| `edit`, `review` | `claude-opus-4-8` |
| `api`, `deploy`, `run`, `watch`, `commit` | `claude-sonnet-4-5` |
| all others (`build`, `test`, `docs`, `research`, …) | claude CLI default |

Override with env vars (resolution order: per-role → global → built-in default):

| Variable | Description |
|----------|-------------|
| `MUXCODE_CLAUDE_MODEL` | Global override for all agents. Passed as `--model` to the `claude` CLI. |
| `MUXCODE_{ROLE}_CLAUDE_MODEL` | Per-role override. Role key examples: `EDIT`, `BUILD`, `TEST`, `SERVE`, `REVIEW`, `DEPLOY`, `COMMIT`, `ANALYZE`, `WATCH`, `DOCS`, `RESEARCH`, `RUN`, `API`. |

Example — downgrade review to Sonnet, use Haiku for build/test:

```bash
# ~/.config/muxcode/config
MUXCODE_REVIEW_CLAUDE_MODEL=claude-sonnet-4-5
MUXCODE_BUILD_CLAUDE_MODEL=claude-haiku-4-5
MUXCODE_TEST_CLAUDE_MODEL=claude-haiku-4-5
```

### Local LLM (Ollama)

| Variable | Default | Description |
|----------|---------|-------------|
| `MUXCODE_{ROLE}_CLI` | (unset) | Set to `local` to run a role via Ollama instead of Claude Code (e.g. `MUXCODE_COMMIT_CLI=local`) |
| `MUXCODE_{ROLE}_MODEL` | (unset) | Per-role Ollama model override (e.g. `MUXCODE_COMMIT_MODEL=llama3.1:8b`). Takes precedence over `MUXCODE_OLLAMA_MODEL`. |
| `MUXCODE_OLLAMA_MODEL` | `qwen2.5-coder:7b` | Default Ollama model for local LLM agents |
| `MUXCODE_OLLAMA_URL` | `http://localhost:11434` | Ollama server URL |

### Multi-CLI providers

Each agent window independently resolves its AI CLI provider. A single session can mix Claude Code, OpenCode, and local LLM agents.

| Variable | Default | Description |
|----------|---------|-------------|
| `MUXCODE_AGENT_CLI` | `claude` | Session-wide default provider (`claude`, `opencode`, `codex`, or `local`) |
| `MUXCODE_{ROLE}_CLI` | (unset) | Per-role override (e.g. `MUXCODE_BUILD_CLI=opencode`, `MUXCODE_COMMIT_CLI=local`, `MUXCODE_ANALYZE_CLI=codex`) |

Resolution order (first non-empty wins):
1. Runtime override (`/tmp/muxcode-bus-{session}/config/{role}.env`) — session-scoped, set by `muxcode reload --cli`
2. `MUXCODE_{ROLE}_CLI` — per-agent override
3. `MUXCODE_AGENT_CLI` — session-wide default
4. Config file (`~/.config/muxcode/config`) — persistent, set by `muxcode config set`
5. `roleDefaultCLI()` — built-in fallback

### Runtime configuration

Change CLI provider or model at runtime without restarting the session:

```bash
# Session-scoped override (lost on session exit)
muxcode reload build --cli opencode --model opencode-go/deepseek-v4-pro

# Persistent change (survives session restarts)
muxcode config set build.cli opencode
muxcode config set build.model opencode-go/deepseek-v4-pro

# Persistent change + immediate reload
muxcode config set build.cli opencode --reload

# View effective config with resolution source
muxcode config get build
# Output:
#   === build ===
#   CLI:     opencode                       (runtime override)
#   Model:   opencode-go/deepseek-v4-pro    (env: MUXCODE_BUILD_MODEL)
#   Default: claude / claude-sonnet-5

# List all roles
muxcode config list
```

See [Agents — Hot reload](agents.md#hot-reload) for full details.

Provider comparison:

| Feature | Claude Code | OpenCode | Codex CLI | Local LLM (Ollama) |
|---------|-------------|----------|-----------|-------------------|
| Hook-driven chains | Yes (send policy blocks manual sends) | No — role-specific prompt + body adaptation + send policy bypass | No — prompt instructions + send policy bypass | No (prompt-based fallback) |
| Idle detection | `❯` prompt match | Not supported (TUI) | Heuristic (`>` / "Summarize") | Not supported |
| Wake-up notifications | Send-keys text injection | Send-keys message payload injection | Send-keys message payload injection | N/A (daemon-driven) |
| Tool permissions | `--allowedTools` patterns | `permission` blocks in agent config | `.codex/AGENTS.md` instructions | `IsToolAllowed()` in Go |
| Context compaction | `/compact` via send-keys | Auto-compact at 95% (no-op from muxcode) | No-op | Reset between inbox checks |
| LLM providers | Anthropic only | Anthropic, OpenAI, Google, Groq, Bedrock | OpenAI (GPT-4.1, o3, o4-mini, etc.) | Ollama (any pulled model) |
| Startup handling | Trust + bypass prompts | TUI frame detection | Prompt ready detection | N/A |

**Codex CLI sandbox limitation**: Codex CLI sandboxes all filesystem writes to the workspace and blocks outbound network access (DNS resolution fails). This makes it unsuitable for:
- **commit/deploy roles** — cannot write to `.git/` or push to remotes
- **Any role requiring network** — cannot reach GitHub, npm registries, or APIs

Only assign Codex to read-only or workspace-scoped roles (review, analyze). Use Claude Code or OpenCode for git operations and network-dependent tasks.

Example — mixed session via config file:

```bash
# ~/.config/muxcode/config
MUXCODE_AGENT_CLI=claude              # default: Claude Code
MUXCODE_EDIT_CLI=opencode             # edit uses OpenCode + DeepSeek V4 Pro
MUXCODE_BUILD_CLI=opencode            # build uses OpenCode
MUXCODE_TEST_CLI=opencode             # test uses OpenCode
MUXCODE_ANALYZE_CLI=codex             # analyze uses Codex CLI (read-only — no git/network)
MUXCODE_COMMIT_CLI=claude             # commit needs git/network — use Claude Code
```

### OpenCode edit agent

The edit agent can run on OpenCode with DeepSeek V4 Pro. Set `MUXCODE_EDIT_CLI=opencode` to enable; unset or set to `claude` to restore the default Claude Code edit agent.

| Variable | Default | Description |
|----------|---------|-------------|
| `MUXCODE_EDIT_CLI` | `claude` | Set to `opencode` to run edit on OpenCode |
| `MUXCODE_EDIT_MODEL` | `opencode-go/deepseek-v4-pro` | OpenCode model override (only when `MUXCODE_EDIT_CLI=opencode`) |

When running on OpenCode: delegation enforcement uses `DenyTools` permission denies instead of the hook guard, the build→test→review chain is prompt-driven instead of hook-driven, and workflow state transitions are detected by the daemon via `git diff --stat` polling. See [Agents — OpenCode edit agent](agents.md#opencode-edit-agent) for details.

### Autonomous agent

| Variable | Default | Description |
|----------|---------|-------------|
| `MUXCODE_AGENT_JQL` | `assignee = currentUser() AND status = 'To Do' ORDER BY priority DESC` | JQL query for finding stories (override for `TASKS.md`) |
| `MUXCODE_AGENT_PR_POLL_INTERVAL` | `120` | Seconds between PR approval checks |
| `MUXCODE_AGENT_PR_MAX_WAIT` | `3600` | Max seconds to wait for PR approval |
| `MUXCODE_AGENT_MAX_STORIES` | `5` | Max stories to process per session |
| `MUXCODE_AGENT_MAX_ITERATIONS` | `10` | Max build/test/fix cycles per story |
| `MUXCODE_AGENT_PAUSE_ON_FAILURE` | `3` | Consecutive story failures before pausing |
| `MUXCODE_AGENT_HEARTBEAT` | `1800` | Seconds between heartbeat ticks (0 to disable) |
| `MUXCODE_AGENT_TASKS` | `.muxcode/agent-tasks.md` | Path to natural-language task definitions file |

**Task file** (`TASKS.md`): The autonomous agent reads `.muxcode/agent-tasks.md` (or the path in `MUXCODE_AGENT_TASKS`) for natural-language task configuration. Changes take effect on the next heartbeat cycle — no restart needed. Env vars override task file values when set.

**State files** (ephemeral, in `/tmp/muxcode-bus-{session}/`):

| File | Purpose |
|------|---------|
| `mode-cycle-edit.json` | Mode cycle state: current index, registered agents |
| `agent-current-story` | Current Jira key being worked |
| `agent-phase` | Current phase: requirements, implementation, waiting |
| `agent-stories-done` | Count of completed stories this session |
| `agent-last-heartbeat` | Timestamp of last heartbeat |

### Branch time tracking

Per-branch active working time, accumulated by the daemon and surfaced in the tmux status bar, `muxcode branch-time --status`, and the `prepare-commit-msg` commit trailer. See the [branch-time-tracking spec](requirements/completed/branch-time-tracking.md).

| Variable | Default | Description |
|----------|---------|-------------|
| `MUXCODE_BRANCH_TIME_DISABLE` | (unset) | Set to `1` to disable accumulation and suppress all `--status` / `--trailer` output |
| `MUXCODE_BRANCH_TIME_IDLE_SECS` | `300` | Pause accumulation after this many seconds without user input; `0` disables the input-idle check (attach-only) |
| `MUXCODE_BRANCH_TIME_IGNORE` | `main,master` | Comma-separated branch names excluded from tracking — shared integration branches where "active working time" is not meaningful |
| `MUXCODE_BRANCH_TIME_ACTIVITY_ROLES` | `plan,edit,build,test,review,deploy,run,commit,analyze` | Comma-separated agent windows sampled to decide whether an agent is actively working |
| `MUXCODE_BRANCH_TIME_FILE` | `~/.config/muxcode/branch-time.json` | Explicit path to the cross-session ledger (primarily for tests). Resolution: `MUXCODE_BRANCH_TIME_FILE` > `$MUXCODE_CONFIG_DIR/branch-time.json` > `~/.config/muxcode/branch-time.json` |

**When time accrues**: while the user is active (a tmux client is attached *and* there has been input within `MUXCODE_BRANCH_TIME_IDLE_SECS`) **or** an agent in `MUXCODE_BRANCH_TIME_ACTIVITY_ROLES` is producing output. An agent working on the branch is productive time even when the user is only watching, is away while agents run, or has detached a background session whose agents keep working. When neither is true, the clock pauses.

**Why `watch` and `serve` are excluded from the activity roles**: both emit output continuously even when no one is working (log tailing, dev server), so counting them would keep the clock running forever. Hosted/duplicate roles (`docs`, `research`, `pr-read`) and non-interactive roles (`webhook`, `api`, `auto`) are omitted because they share a host pane or have no meaningful agent activity.

**Set-to-empty semantics**: `MUXCODE_BRANCH_TIME_IGNORE` and `MUXCODE_BRANCH_TIME_ACTIVITY_ROLES` both distinguish *unset* (use the built-in default) from *set to an empty string* (replace the default with nothing). Setting either variable — even to an empty value — **fully replaces** the built-in list rather than adding to it:

```bash
# Track every branch, including main/master
MUXCODE_BRANCH_TIME_IGNORE=

# Ignore exactly these two branches (main is no longer ignored unless listed)
MUXCODE_BRANCH_TIME_IGNORE=main,develop

# Only count edit/build agent output as agent activity
MUXCODE_BRANCH_TIME_ACTIVITY_ROLES=edit,build

# Never count agent output — accrue only on live user input
MUXCODE_BRANCH_TIME_ACTIVITY_ROLES=
```

### Integrations

| Variable | Default | Description |
|----------|---------|-------------|
| `JIRA_BASE_URL` | (unset) | Atlassian instance URL (e.g. `https://your-org.atlassian.net`). Used by Jira and Confluence skills. |
| `JIRA_USER_EMAIL` | (unset) | Atlassian account email for API authentication |
| `JIRA_API_TOKEN` | (unset) | Atlassian API token ([create one here](https://id.atlassian.com/manage-profile/security/api-tokens)) |
| `CONFLUENCE_BASE_URL` | (unset) | Override Confluence base URL if different from `JIRA_BASE_URL`. Falls back to `JIRA_BASE_URL` if unset. |

## Claude Code Permissions

The `config/settings.json` template includes pre-approved permissions for common CLI commands. These are merged into `~/.claude/settings.json` during `install.sh` so agents can run standard commands without triggering interactive approval prompts.

### Allowed commands

| Category | Commands |
|----------|----------|
| **MuxCode** | `muxcode` (including `atlassian` subcommand for Jira/Confluence), tmux capture/display |
| **Git (read-only)** | `branch`, `diff`, `log`, `show`, `stash list`, `status` |
| **Build/Test** | `go build/test/vet`, `pnpm install/build/lint/test`, `npx jest/cdk`, `pytest`, `ruff` |
| **AWS** | CloudFormation (`describe-stacks`, `list-stacks`), DynamoDB (`describe-table`, `get-item`, `query`, `scan`), EventBridge (`describe-rule`, `list-rules`, `list-targets`), Glue (`get-job`, `get-job-run`, `list-jobs`), Lambda (`get-function`, `invoke`, `list-functions`), CloudWatch Logs (`describe-log-groups`, `filter-log-events`, `get-log-events`, `tail`), S3 (`cp`, `ls`), SNS, SQS, SSM (`get-parameter`), Step Functions (`describe-execution`, `get-execution-history`, `list-executions`, `list-state-machines`, `start-execution`), STS (`get-caller-identity`), CDK (`diff`, `ls`, `synth`) |
| **GCP** | `gcloud` — App Engine (`app describe`, `app services list`), Compute (`instances list/describe`), Functions (`list`, `describe`, `logs read`), Cloud Run (`services list/describe`), Pub/Sub (`topics list/publish`, `subscriptions list`), Logging (`read`), Storage (`ls`, `cp`), SQL (`instances list/describe`), Projects (`list`, `describe`), Config (`list`, `get-value`). Also `gsutil ls/cp`. |
| **Azure** | `az` — Account (`show`), Resources (`list`, `show`), Groups (`list`, `show`), Functions (`functionapp list/show`), Web Apps (`webapp list/show`), Storage (`blob *`, `container list`), Service Bus (`queue/topic *`), SQL (`db list/show`), Key Vault (`secret show`), Monitoring (`log-analytics query`) |
| **Infrastructure** | Terraform (`plan`, `show`, `output`, `state list/show`), kubectl (`get`, `describe`, `logs`), Docker (`ps`, `logs`, `inspect`) |
| **Shell** | `ls`, `which`, `node --version`, `python3 --version` |

### Denied commands (blocked even if explicitly requested)

| Category | Commands |
|----------|----------|
| **Git** | `push --force`, `reset --hard` |
| **AWS** | `s3 rb` (delete bucket), `s3 rm` (delete objects) |
| **GCP** | `gcloud projects delete`, `gcloud storage rm`, `gsutil rm` |
| **Azure** | `az group delete`, `az resource delete`, `az storage blob delete`, `az storage container delete` |
| **Infrastructure** | `cdk destroy`, `terraform destroy`, `kubectl delete` |
| **Shell** | `rm -rf /` |

### Customizing permissions

Add project-specific permissions in `.claude/settings.local.json` (not committed). Add user-wide permissions in `~/.claude/settings.json`. The `install.sh` script merges the template into your existing settings without overwriting custom entries.

## Directory Structure

### Ephemeral (per-session)

```
/tmp/muxcode-bus-{session}/
├── inbox/{role}.jsonl     # Per-agent message queues
├── lock/{role}.lock       # Busy indicators
├── lock/{role}.stopped    # Agent health: auto-restart suppression marker
├── log.jsonl              # Activity log
├── proc.jsonl             # Background process entries
├── proc/{id}.log          # Per-process output logs
├── spawn.jsonl            # Spawned agent entries
├── cron.jsonl             # Scheduled task entries
├── cron-history.jsonl     # Cron execution history
├── subscriptions.jsonl    # Event subscription definitions
├── webhook.pid            # Webhook server PID file (port:pid)
└── watcher.keepalive      # Unix timestamp updated each daemon poll loop
```

Created by `muxcode init`, cleaned up by the tmux session-closed hook.

### Persistent (per-project)

```
.muxcode/memory/
├── shared.md              # Cross-agent shared learnings (active, today)
├── {role}.md              # Per-agent learnings (active, today)
└── {role}/                # Daily archives (lazy rotation)
    └── YYYY-MM-DD.md      # Archived memory for that date (30-day retention)
```

Created on first `muxcode init` in the project directory.

### User Config

```
~/.config/muxcode/
├── config                 # User global config
├── settings.json          # Claude Code hooks template
├── tmux.conf              # Tmux snippet to source
├── nvim/                  # Neovim config (loaded via NVIM_APPNAME=muxcode)
│   ├── init.lua           # Full lazy.nvim config (Dracula, treesitter, render-markdown, telescope)
│   ├── plugin/            # Auto-loaded plugins
│   │   └── startscreen.lua  # MuxCode start screen
│   ├── lua/user/          # User extensions (not overwritten by install)
│   │   └── plugins.lua    # Additional lazy.nvim plugin specs (optional)
│   └── after/plugin/      # User overrides (not overwritten by install)
├── agents/                # User global agent definitions
│   ├── code-editor.md
│   ├── code-builder.md
│   └── ...
├── skills/                # User global skill definitions
│   └── ...
├── memory/                # Global (cross-session) memory
│   ├── shared.md          # Universal shared learnings
│   ├── {role}.md          # Universal per-role learnings
│   └── {role}/            # Daily archives (same rotation as project)
│       └── YYYY-MM-DD.md
└── context.d/             # User global context files
    ├── shared/            # Applied to all roles
    └── {role}/            # Role-specific context
```

## Per-Project Config

Create a `.muxcode/config` file in your project root for project-specific settings:

```bash
# .muxcode/config
MUXCODE_SHELL_INIT="source .venv/bin/activate"
MUXCODE_BUILD_PATTERNS="./build.sh|make"
MUXCODE_TEST_PATTERNS="./test.sh|go test"
```

## Example Configurations

### Python Project

```bash
MUXCODE_SHELL_INIT="source .venv/bin/activate"
MUXCODE_BUILD_PATTERNS="./build.sh|pip install|python setup.py"
MUXCODE_TEST_PATTERNS="pytest|python -m pytest"
MUXCODE_ROUTE_RULES="test=test .py=build"
```

### Rust Project

```bash
MUXCODE_BUILD_PATTERNS="cargo build|cargo check"
MUXCODE_TEST_PATTERNS="cargo test|cargo bench"
MUXCODE_ROUTE_RULES="test=test .rs=build Cargo.toml=build"
```

### Minimal Setup (No Deploy/Run)

```bash
MUXCODE_WINDOWS="edit build test review commit"
```

### Custom Window Names

```bash
MUXCODE_WINDOWS="code compile verify review ship exec git logs dash"
MUXCODE_ROLE_MAP="code=edit compile=build verify=test ship=deploy exec=run git=commit logs=watch dash=status"
```
