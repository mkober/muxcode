# Configuration

## Config File

Muxcoder uses a shell-sourceable config file. Resolution order:

1. `$MUXCODER_CONFIG` — explicit path (set this env var to use a custom location)
2. `./.muxcoder/config` — project-local config
3. `~/.config/muxcoder/config` — user global config
4. Built-in defaults

The config file is a plain bash script that sets environment variables:

```bash
# ~/.config/muxcoder/config
MUXCODER_PROJECTS_DIR="$HOME/Projects,$HOME/Work"
MUXCODER_EDITOR="nvim"
MUXCODER_SHELL_INIT="source ~/.venv/bin/activate"
```

## Environment Variables

### Session Settings

| Variable | Default | Description |
|----------|---------|-------------|
| `MUXCODER_PROJECTS_DIR` | `$HOME` | Directories to scan for git projects (comma-separated) |
| `MUXCODER_SCAN_DEPTH` | `3` | Max depth for project discovery via `find` |
| `MUXCODER_EDITOR` | `nvim` | Editor command for the edit window |
| `MUXCODER_AGENT_CLI` | `claude` | AI CLI command to run agents |
| `MUXCODER_SHELL_INIT` | (empty) | Command to run in each new tmux pane (e.g. activate a virtualenv) |

### Window Layout

| Variable | Default | Description |
|----------|---------|-------------|
| `MUXCODER_WINDOWS` | `edit build test review deploy run commit analyze status` | Space-separated list of windows to create |
| `MUXCODER_ROLE_MAP` | `run=runner commit=git analyze=analyst` | Space-separated `window=role` mappings for windows whose role differs from name |
| `MUXCODER_SPLIT_LEFT` | `edit analyze commit` | Space-separated windows that have a left pane (tool) + right pane (agent) |

### Hook Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `MUXCODER_BUILD_PATTERNS` | `./build.sh\|pnpm*build\|go*build\|make\|cargo*build\|cdk*synth\|tsc` | Pipe-separated patterns for build command detection |
| `MUXCODER_TEST_PATTERNS` | `./test.sh\|jest\|pnpm*test\|pytest\|go*test\|go*vet\|cargo*test\|vitest` | Pipe-separated patterns for test command detection |
| `MUXCODER_ROUTE_RULES` | `test\|spec=test cdk\|stack\|construct\|terraform\|pulumi=deploy .ts\|.js\|.py\|.go\|.rs=build` | Space-separated `pattern=target` rules for file-change routing |
| `MUXCODER_PREVIEW_SKIP` | `/.claude/settings.json /.claude/CLAUDE.md /.muxcoder/` | Space-separated substrings — skip diff preview for matching files |

### Agent Bus

| Variable | Default | Description |
|----------|---------|-------------|
| `BUS_SESSION` | (auto-detected) | Session name for the bus directory |
| `AGENT_ROLE` | (auto-detected) | Current agent's role name |
| `BUS_MEMORY_DIR` | `.muxcoder/memory/` | Path to persistent memory directory |
| `MUXCODER_ROLES` | (empty) | Comma-separated extra roles to add to the known roles list |
| `MUXCODER_SPLIT_LEFT` | `edit analyze commit` | Space-separated windows where agent runs in pane 1 |

## Directory Structure

### Ephemeral (per-session)

```
/tmp/muxcoder-bus-{session}/
├── inbox/{role}.jsonl     # Per-agent message queues
├── lock/{role}.lock       # Busy indicators
└── log.jsonl              # Activity log
```

Created by `muxcoder-agent-bus init`, cleaned up by the tmux session-closed hook.

### Persistent (per-project)

```
.muxcoder/memory/
├── shared.md              # Cross-agent shared learnings
└── {role}.md              # Per-agent learnings
```

Created on first `muxcoder-agent-bus init` in the project directory.

### User Config

```
~/.config/muxcoder/
├── config                 # User global config
├── settings.json          # Claude Code hooks template
├── tmux.conf              # Tmux snippet to source
├── nvim.lua               # Nvim snippet reference
└── agents/                # User global agent definitions
    ├── code-editor.md
    ├── code-builder.md
    └── ...
```

## Per-Project Config

Create a `.muxcoder/config` file in your project root for project-specific settings:

```bash
# .muxcoder/config
MUXCODER_SHELL_INIT="source .venv/bin/activate"
MUXCODER_BUILD_PATTERNS="./build.sh|make"
MUXCODER_TEST_PATTERNS="./test.sh|go test"
```

## Example Configurations

### Python Project

```bash
MUXCODER_SHELL_INIT="source .venv/bin/activate"
MUXCODER_BUILD_PATTERNS="./build.sh|pip install|python setup.py"
MUXCODER_TEST_PATTERNS="pytest|python -m pytest"
MUXCODER_ROUTE_RULES="test=test .py=build"
```

### Rust Project

```bash
MUXCODER_BUILD_PATTERNS="cargo build|cargo check"
MUXCODER_TEST_PATTERNS="cargo test|cargo bench"
MUXCODER_ROUTE_RULES="test=test .rs=build Cargo.toml=build"
```

### Minimal Setup (No Deploy/Run)

```bash
MUXCODER_WINDOWS="edit build test review commit analyze status"
```

### Custom Window Names

```bash
MUXCODER_WINDOWS="code compile verify review ship exec git watch dash"
MUXCODER_ROLE_MAP="code=edit compile=build verify=test ship=deploy exec=runner git=git watch=analyst dash=status"
```
