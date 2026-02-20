# muxcoder

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
│  Message Bus: /tmp/muxcoder-bus-{session}/                  │
│  Memory:      .muxcoder/memory/                             │
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
git clone https://github.com/mkober/muxcoder.git
cd muxcoder
./install.sh
```

The installer checks prerequisites, builds the Go binary, and installs everything to `~/.local/bin/` and `~/.config/muxcoder/`. It will guide you through the remaining setup steps.

For subsequent builds after pulling updates:

```bash
./build.sh
```

### Configure

1. Add the tmux snippet to your `.tmux.conf`:

```tmux
source-file ~/.config/muxcoder/tmux.conf
```

2. Copy the Claude Code hooks to your project:

```bash
cp ~/.config/muxcoder/settings.json .claude/settings.json
```

3. (Optional) Edit your config:

```bash
$EDITOR ~/.config/muxcoder/config
```

### Launch

```bash
# Interactive project picker
muxcoder

# Direct path
muxcoder ~/Projects/my-app

# Custom session name
muxcoder ~/Projects/my-app my-session
```

## How It Works

### Windows

Each muxcoder session creates 9 tmux windows:

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

Agents communicate via a file-based JSONL message bus managed by `muxcoder-agent-bus`:

```bash
muxcoder-agent-bus init              # Initialize bus directories
muxcoder-agent-bus send <to> <action> "<msg>"  # Send a message
muxcoder-agent-bus inbox             # Read your messages
muxcoder-agent-bus memory context    # Read shared + own memory
muxcoder-agent-bus dashboard         # Launch status TUI
```

Bus directory: `/tmp/muxcoder-bus-{session}/`

### Hooks

Four Claude Code hooks drive the integration:

| Hook | Phase | Trigger | Action |
|------|-------|---------|--------|
| `muxcoder-preview-hook.sh` | PreToolUse | Write/Edit | Diff preview in nvim |
| `muxcoder-diff-cleanup.sh` | PreToolUse | Read/Bash/etc | Clean stale diff |
| `muxcoder-analyze-hook.sh` | PostToolUse | Write/Edit | Route file events |
| `muxcoder-bash-hook.sh` | PostToolUse | Bash | Build/test chain |

## Configuration

Shell-sourceable config. Resolution order:

1. `$MUXCODER_CONFIG` (explicit path)
2. `./.muxcoder/config` (project-local)
3. `~/.config/muxcoder/config` (user global)
4. Built-in defaults

See [docs/configuration.md](docs/configuration.md) for the full reference.

### Key Settings

| Variable | Default | Purpose |
|----------|---------|---------|
| `MUXCODER_PROJECTS_DIR` | `$HOME` | Dirs to scan for projects |
| `MUXCODER_WINDOWS` | `edit build test review deploy run commit analyze status` | Windows to create |
| `MUXCODER_EDITOR` | `nvim` | Editor for edit window |
| `MUXCODER_AGENT_CLI` | `claude` | AI CLI command |
| `MUXCODER_BUILD_PATTERNS` | `./build.sh\|pnpm*build\|go*build\|make\|cargo*build` | Hook detection |
| `MUXCODER_TEST_PATTERNS` | `./test.sh\|jest\|pnpm*test\|pytest\|go*test\|cargo*test` | Hook detection |

## Customization

### Custom Agent Definitions

Place custom agent files in `.claude/agents/` in your project or `~/.config/muxcoder/agents/` for global overrides. See [docs/agents.md](docs/agents.md).

### Adding New Roles

1. Add the role to `MUXCODER_WINDOWS` and `MUXCODER_ROLES`
2. Create an agent definition file
3. Map the window to a role in `MUXCODER_ROLE_MAP` if they differ

## Documentation

- [Architecture](docs/architecture.md) — System design and data flow
- [Agent Bus](docs/agent-bus.md) — CLI reference for `muxcoder-agent-bus`
- [Agents](docs/agents.md) — Role descriptions and customization
- [Hooks](docs/hooks.md) — Hook system and customization
- [Configuration](docs/configuration.md) — Config file and env var reference

## License

MIT
