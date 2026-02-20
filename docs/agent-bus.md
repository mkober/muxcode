# muxcoder-agent-bus — CLI Reference

Single Go binary for inter-agent communication in muxcoder sessions. Manages message routing, persistent memory, inbox notifications, and the dashboard TUI.

## Module Location

```
tools/muxcoder-agent-bus/
```

## Build Instructions

From the repo root:
```bash
make build
```

The binary is built to `bin/muxcoder-agent-bus` and installed to `~/.local/bin/muxcoder-agent-bus`.

## CLI Reference

### `muxcoder-agent-bus init`

Initialize the message bus directory structure for a session.

```bash
muxcoder-agent-bus init [--memory-dir PATH]
```

Creates the ephemeral bus directory at `/tmp/muxcoder-bus-{SESSION}/` with `inbox/`, `lock/`, and `log.jsonl`. Optionally initializes the persistent memory directory.

### `muxcoder-agent-bus send`

Send a message to another agent's inbox.

```bash
muxcoder-agent-bus send <to> <action> "<payload>" [--type TYPE] [--reply-to ID] [--no-notify]
```

- `<to>` — target agent role (edit, build, test, review, deploy, run, commit, analyze)
- `<action>` — action name (build, test, review, deploy, run, commit, analyze, notify, etc.)
- `<payload>` — message content (quoted string)
- `--type TYPE` — message type: `request` (default), `response`, or `event`
- `--reply-to ID` — ID of the message being replied to
- `--no-notify` — skip tmux notification to the target agent

Auto-detects sender from `AGENT_ROLE` env var or tmux window name.

### `muxcoder-agent-bus inbox`

Read messages from an agent's inbox.

```bash
muxcoder-agent-bus inbox [--peek] [--raw] [--role ROLE]
```

- Default mode: consume messages and format as actionable prompts with reply commands
- `--peek` — non-destructive preview (does not consume messages)
- `--raw` — dump raw JSONL
- `--role ROLE` — read a specific role's inbox (defaults to own role)

### `muxcoder-agent-bus memory`

Read and write persistent per-project memory.

```bash
muxcoder-agent-bus memory read [role|shared]
muxcoder-agent-bus memory write "<section>" "<text>"
muxcoder-agent-bus memory write-shared "<section>" "<text>"
muxcoder-agent-bus memory context
```

- `read` — read a specific role's memory or shared memory
- `write` — append to own role's memory file
- `write-shared` — append to the shared memory file
- `context` — output both shared memory and own role's memory

Memory is stored in `.muxcoder/memory/` relative to the project directory.

### `muxcoder-agent-bus watch`

Run the unified bus watcher daemon.

```bash
muxcoder-agent-bus watch [session] [--poll N] [--debounce N]
```

- Polls agent inboxes (except edit) and notifies agents via `tmux send-keys` when new messages arrive
- Monitors the analyze trigger file and routes file-edit events to relevant agents based on file patterns
- `--poll N` — inbox polling interval in seconds (default: 2)
- `--debounce N` — trigger file debounce interval in seconds (default: 8)

Runs in the `analyze` window left pane.

### `muxcoder-agent-bus dashboard`

Launch the Dracula-themed terminal dashboard TUI.

```bash
muxcoder-agent-bus dashboard [--refresh N]
```

- Displays agent window statuses (active/ready/idle/error)
- Shows per-agent cost and token usage
- Shows inbox counts and lock status
- Shows recent log entries and inter-agent messages
- Monitors Claude Code teams and tasks
- `--refresh N` — refresh interval in seconds (default: 5)
- Dynamically reads windows from the tmux session

Runs in the `status` window (F9). Press `q` to quit, `r` to refresh.

### `muxcoder-agent-bus cleanup`

Remove the ephemeral bus directory and trigger files.

```bash
muxcoder-agent-bus cleanup [session]
```

Removes `/tmp/muxcoder-bus-{SESSION}/` and `/tmp/muxcoder-analyze-{SESSION}.trigger`. Called automatically by the tmux session-closed hook.

### `muxcoder-agent-bus notify`

Send a tmux notification to an agent's pane.

```bash
muxcoder-agent-bus notify <role>
```

Sends `tmux send-keys` to the target agent's pane. The notification includes a preview: `[from -> action] payload -> Run: muxcoder-agent-bus inbox`. Pane targeting uses the consolidated logic from `bus.PaneTarget()` — split-left windows target pane 1, others target pane 0.

**Note:** `muxcoder-agent-bus send` calls `notify` automatically. Use `--no-notify` to suppress.

### `muxcoder-agent-bus lock` / `unlock` / `is-locked`

Manage agent busy indicators.

```bash
muxcoder-agent-bus lock [role]
muxcoder-agent-bus unlock [role]
muxcoder-agent-bus is-locked [role]
```

- `lock` — create the lock file for the specified role (defaults to own role)
- `unlock` — remove the lock file
- `is-locked` — check lock status (exits 0 if locked, 1 if not)

## Environment Variables

| Variable | Description |
|----------|-------------|
| `BUS_SESSION` | Session name for the bus directory |
| `AGENT_ROLE` | Current agent's role name (auto-detected from tmux window if unset) |
| `BUS_MEMORY_DIR` | Path to persistent memory directory (defaults to `.muxcoder/memory/`) |
| `MUXCODER_ROLES` | Comma-separated extra roles to add to the known roles list |
| `MUXCODER_SPLIT_LEFT` | Space-separated windows with agent in pane 1 (defaults: edit analyze commit) |

## Message Format

Messages are stored as JSONL in per-agent inbox files.

```json
{
  "id": "1708300000-edit-a1b2c3d4",
  "ts": 1708300000,
  "from": "edit",
  "to": "build",
  "type": "request",
  "action": "build",
  "payload": "Run ./build.sh and report results",
  "reply_to": ""
}
```

| Field | Description |
|-------|-------------|
| `id` | Unique message ID (timestamp-sender-random) |
| `ts` | Unix timestamp |
| `from` | Sender role |
| `to` | Recipient role |
| `type` | `request`, `response`, or `event` |
| `action` | Action name |
| `payload` | Message content |
| `reply_to` | ID of the message being replied to |

### Auto-CC to Edit

Messages from `build`, `test`, or `review` to any non-edit agent are automatically copied to the edit inbox, giving the orchestrator visibility into all workflow events.

### Build-Test-Review Chain

Driven by `muxcoder-bash-hook.sh`, not by agent LLMs:

1. **Build succeeds** -> hook sends `request:test` to the test agent
2. **Test succeeds** -> hook sends `request:review` to the review agent
3. **Any failure** -> hook sends `event:notify` directly to edit

## Pane Targeting

Pane targeting is consolidated in `bus/config.go`:

- **Split-left windows** (default: edit, analyze, commit): agent runs in pane 1
- **All other windows**: agent runs in pane 0
- Override via `MUXCODER_SPLIT_LEFT` env var

## Architecture

```
tools/muxcoder-agent-bus/
├── bus/               # Core library
│   ├── config.go      # Session/role/path/pane configuration
│   ├── message.go     # Message struct and JSONL encoding
│   ├── inbox.go       # Read/write/consume inbox files
│   ├── lock.go        # Lock file management
│   ├── memory.go      # Persistent memory read/write
│   ├── notify.go      # Tmux send-keys notification
│   ├── cleanup.go     # Session cleanup
│   └── setup.go       # Bus directory initialization
├── cmd/               # Subcommand handlers
├── watcher/           # Inbox poller + trigger file monitor
├── tui/               # Dracula-themed dashboard TUI
└── main.go            # Entry point and subcommand dispatch
```
