# Muxcode

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
├── muxcode-analyze-hook.sh   # PostToolUse — route file events, trigger watcher
├── muxcode-bash-hook.sh      # PostToolUse — build-test-review chain
├── muxcode-git-status.sh     # Git status poller for commit window left pane
└── muxcode-test-wrapper.sh   # Test runner wrapper
agents/                        # Default agent definition files (.md)
config/
├── settings.json              # Claude Code hooks template
├── tmux.conf                  # Tmux keybinding snippet
└── nvim.lua                   # Reference nvim snippet (not auto-loaded)
docs/                          # Documentation
tools/muxcode-agent-bus/      # Go module — the bus binary
├── bus/                       # Core library (config, message, inbox, lock, memory, notify)
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
- Auto-CC: messages from build/test/review to non-edit agents are copied to the edit inbox
- Build-test-review chain is **hook-driven** (bash exit codes), not LLM-driven

### Hook chain

Four hooks configured in `.claude/settings.json`:

1. `muxcode-preview-hook.sh` — PreToolUse on Write/Edit/NotebookEdit (edit window only)
2. `muxcode-diff-cleanup.sh` — PreToolUse on Read/Bash/Grep/Glob (edit window only)
3. `muxcode-analyze-hook.sh` — PostToolUse on Write/Edit/NotebookEdit (all windows)
4. `muxcode-bash-hook.sh` — PostToolUse on Bash (all windows)

### Lock mechanism

Agents indicate busy state via lock files at `/tmp/muxcode-bus-{session}/lock/{role}.lock`. The dashboard TUI reads these. Commands: `lock`, `unlock`, `is-locked`.

### Watcher debounce

The bus watcher (`muxcode-agent-bus watch`) uses a two-phase debounce: detect trigger file change, then wait for stability (default 8 seconds). Burst edits are coalesced into a single aggregate analyze event sent to the analyst.

## Working on each area

### Go bus code

- Packages: `bus/` (core), `cmd/` (subcommands), `watcher/` (monitor), `tui/` (dashboard)
- Build: `cd tools/muxcode-agent-bus && go build .`
- Test: `cd tools/muxcode-agent-bus && go test ./...`
- Bus directory path is in `bus/config.go` — `BusDir()`, `InboxPath()`, `LockPath()`, `TriggerFile()`
- Pane targeting logic in `bus/config.go` — `PaneTarget()`, `AgentPane()`, `IsSplitLeft()`

### Bash scripts

- Hooks consume JSON from stdin via `cat` — parse with `jq` or `python3`
- Preview hook detects edit window via `tmux display-message -p '#W'` — exits immediately if not `edit`
- Analyze hook writes trigger file at `/tmp/muxcode-analyze-{session}.trigger` — format: `<timestamp> <filepath>` per line
- `auto_accept_bypass` in `muxcode-agent.sh` polls tmux pane for "Yes, I accept" prompt — timeout controlled by `MUXCODE_ACCEPT_TIMEOUT` (default 30s)

### Agent definitions

- Override by placing files in `.claude/agents/` (project) or `~/.config/muxcode/agents/` (global)
- Frontmatter extraction by `launch_agent_from_file` — uses `awk` to strip `---` delimiters, `jq` to build `--agents` JSON
- Project-local agent files use `--agent <name>` natively; external files are read, stripped, and passed via `--agents` JSON
- `agent_name()` maps roles to filenames; `allowed_tools()` maps roles to `--allowedTools` flags

### Configuration

- Shell-sourceable config files — resolution order: `$MUXCODE_CONFIG` > `.muxcode/config` > `~/.config/muxcode/config` > defaults
- Variables set in higher-priority configs completely replace lower-priority values (bash source semantics)
- `MUXCODE_SPLIT_LEFT` is read by both `muxcode.sh` (window layout) and the bus binary (pane targeting in `bus/config.go`)

### Documentation

- Cross-link between docs using relative paths (e.g. `[Architecture](docs/architecture.md)`)
- When updating docs, augment existing content — don't rewrite or reorganize
- Keep tables and code blocks as the primary format
