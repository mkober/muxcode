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
| `MUXCODE_AGENT_CLI` | `claude` | Default AI CLI provider (`claude`, `opencode`, or `local`) |
| `MUXCODE_{ROLE}_CLI` | (unset) | Per-role AI CLI override (e.g. `MUXCODE_BUILD_CLI=opencode`). See [Multi-CLI providers](#multi-cli-providers) |
| `MUXCODE_SHELL_INIT` | (empty) | Command to run in each new tmux pane (e.g. activate a virtualenv) |

### Window Layout

| Variable | Default | Description |
|----------|---------|-------------|
| `MUXCODE_WINDOWS` | `edit build test review deploy run watch commit analyze` | Space-separated list of windows to create |
| `MUXCODE_ROLE_MAP` | `run=runner commit=git analyze=analyst` | Space-separated `window=role` mappings for windows whose role differs from name |
| `MUXCODE_SPLIT_LEFT` | `edit build test review deploy run analyze commit watch` | Space-separated windows that have a left pane (tool) + right pane (agent) |

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
| `MUXCODE_SPLIT_LEFT` | `edit build test review deploy run analyze commit watch` | See Window Layout above — also read by the bus binary for pane targeting |
| `MUXCODE_DEDUP_WINDOW` | `30` | Dedup window in seconds for duplicate message suppression (set to 0 to disable) |
| `MUXCODE_INBOX_POLL_TIMEOUT` | `600` | Timeout in seconds for `send --wait` polling |
| `MUXCODE_LIFECYCLE_LOG_MAX` | `1000` | Max entries per lifecycle log before rotation |

### Claude model selection

Built-in defaults by role:

| Roles | Default model |
|-------|---------------|
| `edit`, `review`, `analyze` | `claude-opus-4-6` |
| `api`, `deploy`, `run`, `watch`, `commit` | `claude-sonnet-4-5` |
| all others (`build`, `test`, `docs`, `research`, …) | claude CLI default |

Override with env vars (resolution order: per-role → global → built-in default):

| Variable | Description |
|----------|-------------|
| `MUXCODE_CLAUDE_MODEL` | Global override for all agents. Passed as `--model` to the `claude` CLI. |
| `MUXCODE_{ROLE}_CLAUDE_MODEL` | Per-role override. Role key examples: `EDIT`, `BUILD`, `TEST`, `REVIEW`, `DEPLOY`, `GIT`, `ANALYZE`, `WATCH`, `DOCS`, `RESEARCH`, `RUN`, `API`. |

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
| `MUXCODE_{ROLE}_CLI` | (unset) | Set to `local` to run a role via Ollama instead of Claude Code (e.g. `MUXCODE_GIT_CLI=local`) |
| `MUXCODE_{ROLE}_MODEL` | (unset) | Per-role Ollama model override (e.g. `MUXCODE_GIT_MODEL=llama3.1:8b`). Takes precedence over `MUXCODE_OLLAMA_MODEL`. |
| `MUXCODE_OLLAMA_MODEL` | `qwen2.5-coder:7b` | Default Ollama model for local LLM agents |
| `MUXCODE_OLLAMA_URL` | `http://localhost:11434` | Ollama server URL |

### Multi-CLI providers

Each agent window independently resolves its AI CLI provider. A single session can mix Claude Code, OpenCode, and local LLM agents.

| Variable | Default | Description |
|----------|---------|-------------|
| `MUXCODE_AGENT_CLI` | `claude` | Session-wide default provider (`claude`, `opencode`, or `local`) |
| `MUXCODE_{ROLE}_CLI` | (unset) | Per-role override (e.g. `MUXCODE_BUILD_CLI=opencode`, `MUXCODE_GIT_CLI=local`) |

Resolution order (first non-empty wins):
1. `MUXCODE_{ROLE}_CLI` — per-agent override
2. `MUXCODE_AGENT_CLI` — session-wide default
3. `claude` — built-in fallback

Provider comparison:

| Feature | Claude Code | OpenCode | Local LLM (Ollama) |
|---------|-------------|----------|-------------------|
| Hook-driven chains | Yes (send policy blocks manual sends) | No — role-specific prompt + body adaptation + send policy bypass | No (prompt-based fallback) |
| Idle detection | `❯` prompt match | Not supported (TUI) | Not supported |
| Wake-up notifications | Send-keys text injection | Send-keys message payload injection | N/A (watcher-driven) |
| Tool permissions | `--allowedTools` patterns | `permission` blocks in agent config | `IsToolAllowed()` in Go |
| Context compaction | `/compact` via send-keys | Auto-compact at 95% (no-op from muxcode) | Reset between inbox checks |
| LLM providers | Anthropic only | Anthropic, OpenAI, Google, Groq, Bedrock | Ollama (any pulled model) |
| Startup handling | Trust + bypass prompts | TUI frame detection | N/A |

Example — mixed session via config file:

```bash
# ~/.config/muxcode/config
MUXCODE_AGENT_CLI=claude              # default: Claude Code
MUXCODE_BUILD_CLI=opencode            # build uses OpenCode
MUXCODE_TEST_CLI=opencode             # test uses OpenCode
MUXCODE_GIT_CLI=local                 # commit uses local LLM
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
└── watcher.keepalive      # Unix timestamp updated each watcher poll loop
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
MUXCODE_WINDOWS="edit build test review commit analyze status"
```

### Custom Window Names

```bash
MUXCODE_WINDOWS="code compile verify review ship exec git watch dash"
MUXCODE_ROLE_MAP="code=edit compile=build verify=test ship=deploy exec=runner git=git watch=analyst dash=status"
```
