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
| `MUXCODE_AGENT_CLI` | `claude` | AI CLI command to run agents |
| `MUXCODE_SHELL_INIT` | (empty) | Command to run in each new tmux pane (e.g. activate a virtualenv) |

### Window Layout

| Variable | Default | Description |
|----------|---------|-------------|
| `MUXCODE_WINDOWS` | `edit build test review deploy run watch commit analyze console` | Space-separated list of windows to create |
| `MUXCODE_ROLE_MAP` | `run=runner commit=git analyze=analyst` | Space-separated `window=role` mappings for windows whose role differs from name |
| `MUXCODE_SPLIT_LEFT` | `edit build test review deploy analyze commit watch` | Space-separated windows that have a left pane (tool) + right pane (agent) |

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
| `MUXCODE_SPLIT_LEFT` | `edit build test review deploy analyze commit watch` | See Window Layout above — also read by the bus binary for pane targeting |

## Directory Structure

### Ephemeral (per-session)

```
/tmp/muxcode-bus-{session}/
├── inbox/{role}.jsonl     # Per-agent message queues
├── lock/{role}.lock       # Busy indicators
└── log.jsonl              # Activity log
```

Created by `muxcode-agent-bus init`, cleaned up by the tmux session-closed hook.

### Persistent (per-project)

```
.muxcode/memory/
├── shared.md              # Cross-agent shared learnings
└── {role}.md              # Per-agent learnings
```

Created on first `muxcode-agent-bus init` in the project directory.

### User Config

```
~/.config/muxcode/
├── config                 # User global config
├── settings.json          # Claude Code hooks template
├── tmux.conf              # Tmux snippet to source
├── nvim.lua               # Reference nvim snippet (not auto-loaded — copy relevant sections to your nvim config manually)
└── agents/                # User global agent definitions
    ├── code-editor.md
    ├── code-builder.md
    └── ...
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
