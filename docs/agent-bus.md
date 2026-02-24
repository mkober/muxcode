# muxcode-agent-bus — CLI Reference

Single Go binary for inter-agent communication in muxcode sessions. Manages message routing, persistent memory, inbox notifications, and the dashboard TUI.

## Module Location

```
tools/muxcode-agent-bus/
```

## Build Instructions

From the repo root:
```bash
make build
```

The binary is built to `bin/muxcode-agent-bus` and installed to `~/.local/bin/muxcode-agent-bus`.

## CLI Reference

### `muxcode-agent-bus init`

Initialize the message bus directory structure for a session.

```bash
muxcode-agent-bus init [--memory-dir PATH]
```

Creates the ephemeral bus directory at `/tmp/muxcode-bus-{SESSION}/` with `inbox/`, `lock/`, and `log.jsonl`. Optionally initializes the persistent memory directory.

### `muxcode-agent-bus send`

Send a message to another agent's inbox.

```bash
muxcode-agent-bus send <to> <action> "<payload>" [--type TYPE] [--reply-to ID] [--no-notify]
```

- `<to>` — target agent role (edit, build, test, review, deploy, run, commit, analyze)
- `<action>` — action name (build, test, review, deploy, run, commit, analyze, notify, etc.)
- `<payload>` — message content (quoted string)
- `--type TYPE` — message type: `request` (default), `response`, or `event`
- `--reply-to ID` — ID of the message being replied to
- `--no-notify` — skip tmux notification to the target agent

Auto-detects sender from `AGENT_ROLE` env var or tmux window name.

**Example:**
```
$ muxcode-agent-bus send build build "Run ./build.sh and report results"
Sent: edit → build [request:build] Run ./build.sh and report results
```

### `muxcode-agent-bus inbox`

Read messages from an agent's inbox.

```bash
muxcode-agent-bus inbox [--peek] [--raw] [--role ROLE]
```

- Default mode: consume messages and format as actionable prompts with reply commands
- `--peek` — non-destructive preview (does not consume messages)
- `--raw` — dump raw JSONL
- `--role ROLE` — read a specific role's inbox (defaults to own role)

**Example:**
```
$ muxcode-agent-bus inbox
You have new messages! Check below and reply to any that need action.

---
📨 Message from edit (request)
Action: build
Message: Run ./build.sh and report results
ID: 1708300000-edit-a1b2c3d4

→ Reply: muxcode-agent-bus send edit build "<your reply>" --type response --reply-to 1708300000-edit-a1b2c3d4
---
```

### `muxcode-agent-bus memory`

Read, write, search, and list persistent per-project memory.

```bash
muxcode-agent-bus memory read [role|shared]
muxcode-agent-bus memory write "<section>" "<text>"
muxcode-agent-bus memory write-shared "<section>" "<text>"
muxcode-agent-bus memory context
muxcode-agent-bus memory search <query> [--role ROLE] [--limit N]
muxcode-agent-bus memory list [--role ROLE]
```

- `read` — read a specific role's memory or shared memory
- `write` — append to own role's memory file
- `write-shared` — append to the shared memory file
- `context` — output both shared memory and own role's memory
- `search` — keyword search across all memory entries with relevance scoring (header matches weighted 2x). Supports `--role` to filter by role and `--limit` to cap results. Query terms are matched case-insensitively via substring matching. Silent output on no results.
- `list` — show a columnar inventory of all memory sections across all roles. Supports `--role` to filter by role.

Memory is stored in `.muxcode/memory/` relative to the project directory.

**Search examples:**
```bash
$ muxcode-agent-bus memory search "pnpm build"
--- [build] Build Config (2026-02-21 14:27) score:4.0 ---
use pnpm for all builds

$ muxcode-agent-bus memory search "permission" --role shared
--- [shared] Agent Permissions (2026-02-21 14:30) score:2.0 ---
edit agent must never run build commands directly

$ muxcode-agent-bus memory list
shared     Agent Permissions                    2026-02-21 14:27
edit       delegation rules                     2026-02-20 17:30
build      Build Config                         2026-02-21 14:27
```

### `muxcode-agent-bus watch`

Run the unified bus watcher daemon.

```bash
muxcode-agent-bus watch [session] [--poll N] [--debounce N]
```

- Polls agent inboxes (except edit) and notifies agents via `tmux send-keys` when new messages arrive
- Monitors the analyze trigger file and routes file-edit events to relevant agents based on file patterns
- `--poll N` — inbox polling interval in seconds (default: 2)
- `--debounce N` — trigger file debounce interval in seconds (default: 8)

Runs in the `analyze` window left pane.

#### Trigger file format

The trigger file (`/tmp/muxcode-analyze-{SESSION}.trigger`) is written by `muxcode-analyze-hook.sh` with one line per file edit:

```
<unix-timestamp> <filepath>
```

When the watcher detects a change in the trigger file, it starts debouncing. After the debounce interval elapses with no further changes, the watcher:

1. Reads the trigger file and collects unique file paths
2. Sends an aggregate `analyze` event to the analyst agent with all edited files
3. Truncates the trigger file

Per-file routing to specific agents (test/deploy/build) is handled earlier by `muxcode-analyze-hook.sh` at edit time — the watcher only handles the aggregate analyst notification.

### `muxcode-agent-bus dashboard`

Launch the Dracula-themed terminal dashboard TUI.

```bash
muxcode-agent-bus dashboard [--refresh N]
```

- Displays agent window statuses (active/ready/idle/error)
- Shows per-agent cost and token usage
- Shows inbox counts and lock status
- Shows recent log entries and inter-agent messages
- Monitors Claude Code teams and tasks (these are Claude Code's built-in Task tool sub-agents, not muxcode's own bus coordination)
- `--refresh N` — refresh interval in seconds (default: 5)
- Dynamically reads windows from the tmux session

Runs in the `status` window (F9). Press `q` to quit, `r` to refresh.

### `muxcode-agent-bus cleanup`

Remove the ephemeral bus directory and trigger files.

```bash
muxcode-agent-bus cleanup [session]
```

Removes `/tmp/muxcode-bus-{SESSION}/` and `/tmp/muxcode-analyze-{SESSION}.trigger`. Called automatically by the tmux session-closed hook.

### `muxcode-agent-bus notify`

Send a tmux notification to an agent's pane.

```bash
muxcode-agent-bus notify <role>
```

Sends `tmux send-keys` to the target agent's pane. The notification includes a preview: `[from -> action] payload -> Run: muxcode-agent-bus inbox`. Pane targeting uses the consolidated logic from `bus.PaneTarget()` — split-left windows target pane 1, others target pane 0.

**Note:** `muxcode-agent-bus send` calls `notify` automatically. Use `--no-notify` to suppress.

### `muxcode-agent-bus cron`

Manage scheduled tasks that fire bus messages on a cadence.

```bash
muxcode-agent-bus cron add <schedule> <target> <action> <message>
muxcode-agent-bus cron list [--all]
muxcode-agent-bus cron remove <id>
muxcode-agent-bus cron enable <id>
muxcode-agent-bus cron disable <id>
muxcode-agent-bus cron history [--id CRON_ID] [--limit N]
```

**Subcommands:**

| Subcommand | Description |
|------------|-------------|
| `add` | Create a new scheduled task |
| `list` | Show enabled entries (use `--all` to include disabled) |
| `remove` | Delete an entry by ID |
| `enable` | Enable a disabled entry |
| `disable` | Disable an entry without removing it |
| `history` | Show execution history (optionally filtered by `--id` and `--limit`) |

**Schedule formats:**

| Format | Interval |
|--------|----------|
| `@every 30s` | 30 seconds |
| `@every 5m` | 5 minutes |
| `@every 1h` | 1 hour |
| `@every 2h30m` | 2 hours 30 minutes |
| `@half-hourly` | 30 minutes |
| `@hourly` | 1 hour |
| `@daily` | 24 hours |

Minimum interval is 30 seconds. Schedules are case-insensitive.

**Examples:**
```bash
# Schedule a git status check every 5 minutes
$ muxcode-agent-bus cron add "@every 5m" commit status "Run git status and report"
Added cron entry: 1771897000-cron-a1b2c3d4
  Schedule: @every 5m  Target: commit  Action: status
  Message: Run git status and report

# Schedule hourly test runs
$ muxcode-agent-bus cron add "@hourly" test test "Run tests and report results"

# List all enabled entries
$ muxcode-agent-bus cron list

# Disable an entry
$ muxcode-agent-bus cron disable 1771897000-cron-a1b2c3d4

# View execution history
$ muxcode-agent-bus cron history --limit 10
```

**Watcher integration:** The bus watcher (`muxcode-agent-bus watch`) checks for due cron entries on each poll cycle. It reloads the cron file from disk at most every 10 seconds to avoid excessive filesystem reads. When a cron entry fires, the watcher sends a bus message to the target agent, updates `last_run_ts`, appends to execution history, and notifies the target via tmux.

**Data files:**

| File | Location | Purpose |
|------|----------|---------|
| `cron.jsonl` | `/tmp/muxcode-bus-{SESSION}/cron.jsonl` | Cron entry definitions |
| `cron-history.jsonl` | `/tmp/muxcode-bus-{SESSION}/cron-history.jsonl` | Execution history log |

### `muxcode-agent-bus status`

Show all agents' current state overview.

```bash
muxcode-agent-bus status [--json]
```

- Default: human-readable table with role, state, inbox count, and last activity
- `--json` — output as JSON array for programmatic use
- STATE: `busy` (lock file exists) or `idle`
- LAST ACTIVITY: timestamp + direction arrow (← received, → sent) + peer:action from log.jsonl
- Roles with no activity show `—`

**Example:**
```
$ muxcode-agent-bus status
ROLE         STATE  INBOX  LAST ACTIVITY
edit         idle   0      14:32 ← build:response
build        busy   1      14:31 ← edit:compile
test         idle   0      14:30 ← build:test
review       idle   0      —
```

### `muxcode-agent-bus history`

Show recent messages to/from an agent.

```bash
muxcode-agent-bus history <role> [--limit N] [--context]
```

- `<role>` — show messages involving this role (from `log.jsonl`)
- `--limit N` — show last N messages (default: 20)
- `--context` — output as a markdown block for prompt injection

**Default output:**
```
$ muxcode-agent-bus history build
--- Message history for build (last 20) ---
14:30  edit → build  [request:build] Run ./build.sh and report results
14:31  build → test  [request:test] Build succeeded — run tests
14:31  build → edit  [response:build] Build succeeded: Go binary built
```

**Context output (`--context`):**
```
$ muxcode-agent-bus history build --context
## Recent activity for build

- 14:30 [request from edit] Run ./build.sh and report results
- 14:31 [response to edit] Build succeeded: Go binary built
- 14:31 [request to test] Build succeeded — run tests
```

### `muxcode-agent-bus lock` / `unlock` / `is-locked`

Manage agent busy indicators.

```bash
muxcode-agent-bus lock [role]
muxcode-agent-bus unlock [role]
muxcode-agent-bus is-locked [role]
```

- `lock` — create the lock file for the specified role (defaults to own role)
- `unlock` — remove the lock file
- `is-locked` — check lock status (exits 0 if locked, 1 if not)

## Environment Variables

| Variable | Description |
|----------|-------------|
| `BUS_SESSION` | Session name for the bus directory |
| `AGENT_ROLE` | Current agent's role name (auto-detected from tmux window if unset) |
| `BUS_MEMORY_DIR` | Path to persistent memory directory (defaults to `.muxcode/memory/`) |
| `MUXCODE_ROLES` | Comma-separated extra roles to add to the known roles list |
| `MUXCODE_SPLIT_LEFT` | Space-separated windows with agent in pane 1 (defaults: edit analyze commit) |

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

Driven by `muxcode-bash-hook.sh`, not by agent LLMs:

1. **Build succeeds** -> hook sends `request:test` to the test agent
2. **Test succeeds** -> hook sends `request:review` to the review agent
3. **Any failure** -> hook sends `event:notify` directly to edit

## Pane Targeting

Pane targeting is consolidated in `bus/config.go`:

- **Split-left windows** (default: edit, analyze, commit): agent runs in pane 1
- **All other windows**: agent runs in pane 0
- Override via `MUXCODE_SPLIT_LEFT` env var

## Architecture

```
tools/muxcode-agent-bus/
├── bus/               # Core library
│   ├── config.go      # Session/role/path/pane configuration
│   ├── message.go     # Message struct and JSONL encoding
│   ├── inbox.go       # Read/write/consume inbox files
│   ├── lock.go        # Lock file management
│   ├── memory.go      # Persistent memory read/write/search/list
│   ├── notify.go      # Tmux send-keys notification
│   ├── cron.go        # Cron scheduling (structs, parsing, CRUD, execution)
│   ├── inspect.go     # Session inspection (agent status, history, context)
│   ├── cleanup.go     # Session cleanup
│   └── setup.go       # Bus directory initialization
├── cmd/               # Subcommand handlers
├── watcher/           # Inbox poller + trigger file monitor
├── tui/               # Dracula-themed dashboard TUI
└── main.go            # Entry point and subcommand dispatch
```
