# muxcode

A multi-agent coding environment built on tmux, neovim, and Claude Code. Each agent runs in its own tmux window with dedicated responsibilities — editing, building, testing, reviewing, deploying, and more — coordinated through a file-based message bus.

```
┌─────────────────────────────────────────────────────────────┐
│  F1 edit  F2 build  F3 test  F4 review  F5 deploy  F6 run  │
│  F7 commit  F8 analyze  F9 status                          │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  ┌──────────────┐    ┌──────────────┐    ┌──────────────┐  │
│  │ edit         │    │ build        │    │ test         │  │
│  │ nvim | agent │──→ │ term | agent │──→ │ term | agent │  │
│  └──────────────┘    └──────────────┘    └──────────────┘  │
│         │                                       │           │
│         │            ┌──────────────┐           │           │
│         └───────────→│ review       │←──────────┘           │
│                      │ term | agent │                       │
│                      └──────────────┘                       │
│                                                             │
│  Message Bus: /tmp/muxcode-bus-{session}/                  │
│  Memory:      .muxcode/memory/                             │
└─────────────────────────────────────────────────────────────┘
```

## Quick Start

### Prerequisites

- tmux >= 3.0
- Go >= 1.22 (build from source)
- [Claude Code](https://claude.ai/code) CLI (`claude`)
- jq (for hooks)
- Neovim (for diff preview)
- fzf (for interactive project picker)

### Install

```bash
git clone https://github.com/mkober/muxcode.git
cd muxcode
./install.sh
```

The installer checks prerequisites, builds the Go binary, and installs everything to `~/.local/bin/` and `~/.config/muxcode/`. It will guide you through the remaining setup steps.

For subsequent builds after pulling updates:

```bash
./build.sh
```

### Configure

1. Add the tmux snippet to your `.tmux.conf`:

```tmux
source-file ~/.config/muxcode/tmux.conf
```

2. Copy the Claude Code hooks to your project:

```bash
cp ~/.config/muxcode/settings.json .claude/settings.json
```

3. (Optional) Edit your config:

```bash
$EDITOR ~/.config/muxcode/config
```

### Launch

```bash
# Interactive project picker
muxcode

# Direct path
muxcode ~/Projects/my-app

# Custom session name
muxcode ~/Projects/my-app my-session
```

## How It Works

### Windows

Each muxcode session creates 9 tmux windows:

| Window | Role | Description |
|--------|------|-------------|
| edit | edit | Primary orchestrator — nvim (left) + AI agent (right) |
| build | build | Compile and package |
| test | test | Run tests |
| review | review | Review diffs for quality |
| deploy | deploy | Infrastructure deployments |
| run | runner | Execute commands |
| commit | git | Git operations — status poller (left) + agent (right) |
| analyze | analyst | Analyze changes — bus watcher (left) + agent (right) |
| status | — | Dashboard TUI |

### Build-Test-Review Chain

The chain is **hook-driven**, not LLM-driven:

```
edit → build (request)
         ↓
     build agent runs build command, replies to edit
         ↓
     hook detects success → sends request to test
         ↓
     test agent runs tests, replies to requester
         ↓
     hook detects success → sends request to review
         ↓
     review agent reviews diff, replies to requester
```

### Message Bus

Agents communicate via a file-based JSONL message bus managed by `muxcode-agent-bus`:

```bash
muxcode-agent-bus init              # Initialize bus directories
muxcode-agent-bus send <to> <action> "<msg>"  # Send a message
muxcode-agent-bus inbox             # Read your messages
muxcode-agent-bus memory context    # Read shared + own memory
muxcode-agent-bus dashboard         # Launch status TUI
muxcode-agent-bus watch [session]   # Run the bus watcher daemon
muxcode-agent-bus notify <role>     # Send tmux notification to agent
muxcode-agent-bus lock [role]       # Mark agent as busy
muxcode-agent-bus unlock [role]     # Mark agent as available
muxcode-agent-bus cleanup [session] # Remove ephemeral bus directory
```

Bus directory: `/tmp/muxcode-bus-{session}/`

### Hooks

Four Claude Code hooks drive the integration:

| Hook | Phase | Trigger | Action |
|------|-------|---------|--------|
| `muxcode-preview-hook.sh` | PreToolUse | Write/Edit | Diff preview in nvim |
| `muxcode-diff-cleanup.sh` | PreToolUse | Read/Bash/etc | Clean stale diff |
| `muxcode-analyze-hook.sh` | PostToolUse | Write/Edit | Route file events |
| `muxcode-bash-hook.sh` | PostToolUse | Bash | Build/test chain |

## Configuration

Shell-sourceable config. Resolution order:

1. `$MUXCODE_CONFIG` (explicit path)
2. `./.muxcode/config` (project-local)
3. `~/.config/muxcode/config` (user global)
4. Built-in defaults

See [docs/configuration.md](docs/configuration.md) for the full reference.

### Key Settings

| Variable | Default | Purpose |
|----------|---------|---------|
| `MUXCODE_PROJECTS_DIR` | `$HOME` | Dirs to scan for projects |
| `MUXCODE_WINDOWS` | `edit build test review deploy run commit analyze status` | Windows to create |
| `MUXCODE_EDITOR` | `nvim` | Editor for edit window |
| `MUXCODE_AGENT_CLI` | `claude` | AI CLI command |
| `MUXCODE_BUILD_PATTERNS` | `./build.sh\|pnpm*build\|go*build\|make\|cargo*build` | Hook detection |
| `MUXCODE_TEST_PATTERNS` | `./test.sh\|jest\|pnpm*test\|pytest\|go*test\|cargo*test` | Hook detection |
| `MUXCODE_SCAN_DEPTH` | `3` | Max depth for project discovery |
| `MUXCODE_SHELL_INIT` | (empty) | Command to run in each new tmux pane |

## Customization

### Custom Agent Definitions

Place custom agent files in `.claude/agents/` in your project or `~/.config/muxcode/agents/` for global overrides. See [docs/agents.md](docs/agents.md).

### Adding New Roles

1. Add the role to `MUXCODE_WINDOWS` and `MUXCODE_ROLES`
2. Create an agent definition file
3. Map the window to a role in `MUXCODE_ROLE_MAP` if they differ

## Documentation

- [Architecture](docs/architecture.md) — System design and data flow
- [Agent Bus](docs/agent-bus.md) — CLI reference for `muxcode-agent-bus`
- [Agents](docs/agents.md) — Role descriptions and customization
- [Hooks](docs/hooks.md) — Hook system and customization
- [Configuration](docs/configuration.md) — Config file and env var reference

## Troubleshooting

| Symptom | Cause | Fix |
|---------|-------|-----|
| Build-test-review chain doesn't fire | `jq` and `python3` both missing | Install `jq` — hooks need it to parse JSON from stdin |
| No diff preview in nvim | `python3` not available | Preview hook uses `python3` to generate proposed content; install it |
| Messages not delivered | Bus directory missing or stale | Run `muxcode-agent-bus init` or restart the session |
| Watcher floods analyst with events | Debounce too short for large edits | Increase `--debounce` (default 8s) in the watcher command |
| Agent has wrong permissions | Role not mapped in `allowed_tools()` | Add a case to `allowed_tools()` in `scripts/muxcode-agent.sh` |

## License

MIT
