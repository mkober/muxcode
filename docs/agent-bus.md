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
muxcode-agent-bus send <to> <action> "<payload>" [--type TYPE] [--reply-to ID] [--no-notify] [--force]
```

- `<to>` — target agent role (edit, build, test, review, deploy, run, commit, analyze)
- `<action>` — action name (build, test, review, deploy, run, commit, analyze, notify, etc.)
- `<payload>` — message content (quoted string)
- `--type TYPE` — message type: `request` (default), `response`, or `event`
- `--reply-to ID` — ID of the message being replied to
- `--no-notify` — skip tmux notification to the target agent
- `--force` — bypass pre-commit safeguard (only relevant when sending commit actions to the commit agent)

**Pre-commit safeguard:** When sending a commit action (`commit`, `stage`, `push`, `merge`, `rebase`, `tag`) to the commit agent, the bus checks that all other agents (excluding edit, commit, watch) have empty inboxes, are not busy, and have no running background processes. If any agent has pending work, the send is blocked with an error. Use `--force` to bypass.

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

### `muxcode-agent-bus guard`

Check for agent loop patterns — command retries and message ping-pong.

```bash
muxcode-agent-bus guard [role] [--json] [--threshold N] [--window N]
```

- No role: check all known roles
- `role`: check only that role
- `--json` — output as JSON array
- `--threshold N` — override repeat threshold (default 3 for commands, 4 for messages)
- `--window N` — override time window in seconds (default 300)
- Exit code 0: no loops detected
- Exit code 1: loops detected (useful for scripting)

**Detection targets:**

| Type | Source | Default threshold | Description |
|------|--------|-------------------|-------------|
| Command loop | `{role}-history.jsonl` | 3 | Same command fails N+ times consecutively within the time window |
| Message loop | `log.jsonl` | 4 | Same `(from, to, action)` tuple or ping-pong pattern repeats N+ times |

Command normalization strips `cd ... &&` prefixes, env var assignments, `bash -c`, trailing `2>&1`, and collapses whitespace to prevent false negatives.

**Examples:**
```bash
# Check all agents
$ muxcode-agent-bus guard
⚠ LOOP DETECTED: build
  Type: command
  Command: go build ./... (failed 3x in 2m)
  Action: Check build window — agent may be stuck

# Check a specific agent as JSON
$ muxcode-agent-bus guard build --json
[
  {
    "role": "build",
    "type": "command",
    "count": 3,
    "command": "go build ./...",
    "window_s": 120,
    "message": "go build ./... failed 3x in 2m"
  }
]

# Custom thresholds
$ muxcode-agent-bus guard --threshold 5 --window 600
```

**Watcher integration:** The bus watcher checks for loops every 60 seconds. When a loop is detected, it sends a `loop-detected` event to the edit agent and notifies via tmux. Alerts are deduplicated within a 10-minute cooldown (exceeds the 5-minute detection window to prevent self-sustaining alerts). System actions (`loop-detected`, `compact-recommended`, `proc-complete`, `spawn-complete`) are excluded from message loop detection.

#### Watcher event: `compact-recommended`

The watcher monitors agent context size (memory + history + log files) and staleness (time since last compaction) every 120 seconds. When **both** conditions are met — total tracked size > 512 KB **and** time since last compact > 2 hours — the watcher sends a `compact-recommended` event to the role itself with an actionable message:

```
Context approaching limits for edit (total: 620 KB, memory: 180 KB, history: 340 KB, log: 100 KB).
Last compact: 2h 30m ago. Run: muxcode-agent-bus session compact "<summary>"
```

Alerts are deduplicated within a 10-minute cooldown per role. The agent receiving the alert should run `muxcode-agent-bus session compact "<summary>"` to save its context and reset the staleness timer.

### `muxcode-agent-bus proc`

Manage background processes — launch, track, and auto-notify on completion.

```bash
muxcode-agent-bus proc start "<command>" [--dir DIR]
muxcode-agent-bus proc list [--all]
muxcode-agent-bus proc status <id>
muxcode-agent-bus proc log <id> [--tail N]
muxcode-agent-bus proc stop <id>
muxcode-agent-bus proc clean
```

**Subcommands:**

| Subcommand | Description |
|------------|-------------|
| `start` | Launch a background process and track it |
| `list` | Show running processes (use `--all` to include finished) |
| `status` | Detailed status for a single process |
| `log` | Read process output log (use `--tail N` for last N lines) |
| `stop` | Send SIGTERM to a running process |
| `clean` | Remove finished entries and their log files |

**Examples:**
```bash
# Start a long-running build in the background
$ muxcode-agent-bus proc start "./build.sh"
Started process: 1740000000-proc-a1b2c3d4
  PID: 12345  Owner: build
  Command: ./build.sh
  Log: /tmp/muxcode-bus-mysession/proc/1740000000-proc-a1b2c3d4.log

# Check running processes
$ muxcode-agent-bus proc list
ID                                   PID      STATUS     OWNER      STARTED    COMMAND
----------------------------------------------------------------------------------------------------
1740000000-proc-a1b2c3d4             12345    running    build      14:00:00   ./build.sh

# View process log
$ muxcode-agent-bus proc log 1740000000-proc-a1b2c3d4 --tail 20

# Stop a process
$ muxcode-agent-bus proc stop 1740000000-proc-a1b2c3d4

# Clean up finished processes
$ muxcode-agent-bus proc clean
Cleaned 2 finished process(es).
```

**Watcher integration:** The bus watcher checks running processes on each poll cycle (2s). When a process completes, it sends a `proc-complete` event to the owner agent with the command, status, and exit code. The owner is notified via tmux.

**Data files:**

| File | Location | Purpose |
|------|----------|---------|
| `proc.jsonl` | `/tmp/muxcode-bus-{SESSION}/proc.jsonl` | Process entry definitions |
| `{id}.log` | `/tmp/muxcode-bus-{SESSION}/proc/{id}.log` | Per-process stdout/stderr output |

### `muxcode-agent-bus spawn`

Manage spawned agent sessions — create temporary agents for one-off tasks, collect results, and tear down.

```bash
muxcode-agent-bus spawn start <role> "<task>"
muxcode-agent-bus spawn list [--all]
muxcode-agent-bus spawn status <id>
muxcode-agent-bus spawn result <id>
muxcode-agent-bus spawn stop <id>
muxcode-agent-bus spawn clean
```

**Subcommands:**

| Subcommand | Description |
|------------|-------------|
| `start` | Create tmux window, seed inbox with task, launch agent, track |
| `list` | Show running spawns (use `--all` to include completed/stopped) |
| `status` | Detailed status for a single spawn |
| `result` | Get the last message sent by the spawned agent |
| `stop` | Kill the tmux window and mark spawn as stopped |
| `clean` | Remove finished entries and their inbox files |

**How it works:**

1. `spawn start research "What does bus/guard.go do?"` generates a unique spawn ID (e.g. `spawn-a1b2c3d4`)
2. Creates a tmux window named `spawn-a1b2c3d4`, splits horizontally (agent in pane 1)
3. Pre-seeds the spawn's inbox with the task message
4. Launches `AGENT_ROLE=spawn-a1b2c3d4 muxcode-agent.sh research` — the base role (`research`) determines agent definition, tools, and prompts; the `AGENT_ROLE` env var (`spawn-a1b2c3d4`) determines the bus communication channel
5. After 2s delay, notifies the spawn agent to read its inbox
6. When the agent finishes and exits (tmux window closes), the watcher detects it and sends a `spawn-complete` event to the owner

**Examples:**
```bash
# Spawn a research agent
$ muxcode-agent-bus spawn start research "What does bus/guard.go do?"
Started spawn: 1771900000-spawn-a1b2c3d4
  Role: research  Spawn Role: spawn-a1b2c3d4  Owner: edit
  Window: spawn-a1b2c3d4
  Task: What does bus/guard.go do?

# Check running spawns
$ muxcode-agent-bus spawn list
ID                                   ROLE         SPAWN-ROLE   STATUS     OWNER      TASK
--------------------------------------------------------------------------------------------------------------
1771900000-spawn-a1b2c3d4            research     spawn-a1b2c  running    edit       What does bus/guard.go do?

# Get the result after completion
$ muxcode-agent-bus spawn result 1771900000-spawn-a1b2c3d4

# Stop a running spawn
$ muxcode-agent-bus spawn stop 1771900000-spawn-a1b2c3d4

# Clean up finished spawns
$ muxcode-agent-bus spawn clean
Cleaned 1 finished spawn(s).
```

**Watcher integration:** The bus watcher checks spawned agent windows on each poll cycle (2s). When a spawn's tmux window no longer exists, it marks the spawn as `completed`, extracts the last result message from `log.jsonl`, and sends a `spawn-complete` event to the owner agent with the result summary.

**Pre-commit safeguard:** Running spawns block commits, same as running background processes. Use `--force` on the send command to bypass.

**Data files:**

| File | Location | Purpose |
|------|----------|---------|
| `spawn.jsonl` | `/tmp/muxcode-bus-{SESSION}/spawn.jsonl` | Spawn entry definitions |

### `muxcode-agent-bus demo`

Run scripted demo scenarios — sends real bus messages, switches tmux windows, and toggles lock states with configurable timing.

```bash
muxcode-agent-bus demo run [SCENARIO] [--speed FACTOR] [--dry-run] [--no-switch]
muxcode-agent-bus demo list
```

**Subcommands:**

| Subcommand | Description |
|------------|-------------|
| `run` | Execute a demo scenario |
| `list` | Show available scenarios with step counts and timing |

**Flags for `run`:**

| Flag | Description |
|------|-------------|
| `SCENARIO` | Scenario name (default: `build-test-review`) |
| `--speed FACTOR` | Delay multiplier: `2.0` = fast (GIF), `0.5` = slow (live talk). Default: `1.0` |
| `--dry-run` | Print steps without executing (no tmux needed) |
| `--no-switch` | Skip tmux window switching (headless mode) |

**Built-in scenario: `build-test-review`**

20-step cycle demonstrating the full delegation workflow: edit → build → test → review → commit → edit. Duration: ~20s at 1.0x, ~10s at 2.0x. All messages use `From: "demo"` so agents can identify demo traffic.

| Step | Window | Action | Description |
|------|--------|--------|-------------|
| 1 | edit | select-window | Show edit window |
| 2 | — | send → build | Delegate build |
| 3 | build | select-window | Switch to build window |
| 4-5 | — | lock/unlock build | Build agent busy → complete |
| 6 | — | send → test | Hook chain fires |
| 7 | test | select-window | Switch to test window |
| 8-9 | — | lock/unlock test | Test agent busy → pass |
| 10 | — | send → review | Hook chain fires |
| 11 | review | select-window | Switch to review window |
| 12-13 | — | lock/unlock review | Review agent busy → complete |
| 14 | edit | select-window | Results arrive at edit |
| 15-16 | — | send → edit, commit | Results + delegate commit |
| 17 | commit | select-window | Switch to commit window |
| 18-19 | — | lock/unlock commit | Git manager busy → complete |
| 20 | edit | select-window | Return to edit |

**Examples:**
```bash
# List available scenarios
$ muxcode-agent-bus demo list
Available demo scenarios:

  build-test-review          Full build-test-review-commit cycle across agent windows
                             20 steps, ~20s at 1.0x speed

# Dry-run (no tmux needed)
$ muxcode-agent-bus demo run --dry-run

# Live demo at 2x speed (for GIF recording)
$ muxcode-agent-bus demo run --speed 2.0

# Slow demo for live presentation
$ muxcode-agent-bus demo run --speed 0.5
```

**GIF capture:** Use `scripts/muxcode-demo.sh` to record the screen during a demo run and convert to GIF:

```bash
scripts/muxcode-demo.sh --speed 2.0 --output assets/demo.gif
```

Requires `ffmpeg` and `gifski` (`brew install ffmpeg gifski`). Auto-detects the screen capture device via avfoundation.

### `muxcode-agent-bus webhook`

Manage the webhook HTTP endpoint — an HTTP-to-bus bridge for external tools (CI/CD, GitHub webhooks, monitoring, custom scripts).

```bash
muxcode-agent-bus webhook start [--port PORT] [--host HOST] [--token TOKEN]
muxcode-agent-bus webhook stop
muxcode-agent-bus webhook status
```

**Subcommands:**

| Subcommand | Description |
|------------|-------------|
| `start` | Launch HTTP server as a detached background process |
| `stop` | Send SIGTERM to the running server and remove PID file |
| `status` | Check if the server is running, show port and PID |

**Flags for `start`:**

| Flag | Default | Description |
|------|---------|-------------|
| `--port PORT` | `9090` | TCP port to listen on |
| `--host HOST` | `127.0.0.1` | Bind address (localhost only by default) |
| `--token TOKEN` | *(none)* | Bearer token for auth (no auth when omitted) |

**HTTP endpoints:**

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/send` | Convert JSON request body to a bus message |
| `GET` | `/health` | Health check with session name and uptime |

**POST /send request body:**

```json
{
  "to": "build",
  "action": "build",
  "payload": "Run ./build.sh and report results",
  "type": "request",
  "reply_to": ""
}
```

| Field | Required | Default | Description |
|-------|----------|---------|-------------|
| `to` | yes | — | Target agent role (validated via `IsKnownRole()`) |
| `action` | yes | — | Message action name |
| `payload` | yes | — | Message content |
| `type` | no | `"request"` | Message type: `request`, `response`, or `event` |
| `reply_to` | no | `""` | ID of the message being replied to |

**Response format:**

Success (200):
```json
{"ok": true, "id": "1740000000-webhook-a1b2c3d4"}
```

Error (4xx/5xx):
```json
{"ok": false, "error": "unknown role 'foo'"}
```

**GET /health response:**

```json
{"ok": true, "session": "muxcode", "uptime_seconds": 3600}
```

**Security:**

- Binds to `127.0.0.1` only by default — not accessible from external networks
- Optional bearer token auth via `--token` flag
- When a token is set, all requests require `Authorization: Bearer <token>` header
- Request body limited to 64 KB via `http.MaxBytesReader`
- Target role validation reuses existing `bus.IsKnownRole()`
- Send policy enforcement reuses existing `bus.CheckSendPolicy()`

**Message identity:** All webhook-originated messages use `From: "webhook"`. The `webhook` role is excluded from pre-commit checks (passive bridge, not a working agent).

**PID tracking:** PID file at `/tmp/muxcode-bus-{SESSION}/webhook.pid` with format `port:pid`. Read by `stop` and `status`. Removed on graceful shutdown, `stop`, and session re-init.

**Startup verification:** The `start` command polls `/health` up to 3 seconds after launching the background process to confirm the server is listening before reporting success.

**Examples:**

```bash
# Start webhook with default settings
$ muxcode-agent-bus webhook start
Webhook server started on 127.0.0.1:9090 (PID 54854)

# Start with auth token
$ muxcode-agent-bus webhook start --port 8080 --token mysecret

# Health check
$ curl http://127.0.0.1:9090/health
{"ok":true,"session":"muxcode","uptime_seconds":13}

# Send a message
$ curl -X POST http://127.0.0.1:9090/send \
  -H "Content-Type: application/json" \
  -d '{"to":"edit","action":"webhook-test","payload":"Hello from webhook"}'
{"ok":true,"id":"1740000000-webhook-a1b2c3d4"}

# Send with auth token
$ curl -X POST http://127.0.0.1:8080/send \
  -H "Authorization: Bearer mysecret" \
  -H "Content-Type: application/json" \
  -d '{"to":"build","action":"build","payload":"CI triggered build"}'

# Check status
$ muxcode-agent-bus webhook status
Webhook: running on 127.0.0.1:9090 (PID 54854)

# Stop
$ muxcode-agent-bus webhook stop
Webhook server stopped
```

**Data files:**

| File | Location | Purpose |
|------|----------|---------|
| `webhook.pid` | `/tmp/muxcode-bus-{SESSION}/webhook.pid` | PID file (`port:pid` format) |

### `muxcode-agent-bus context`

Manage per-agent drop-in context files — a lightweight, file-based way to inject project-specific knowledge into agent prompts without the frontmatter/roles/tags overhead of skills.

```bash
muxcode-agent-bus context list [--role ROLE] [--no-auto]
muxcode-agent-bus context prompt <role> [--no-auto]
muxcode-agent-bus context detect [DIR]
```

**Subcommands:**

| Subcommand | Description |
|------------|-------------|
| `list` | Show all context files with source (project/user/auto), filterable by `--role` |
| `prompt` | Output formatted context for prompt injection (used by `muxcode-agent.sh`) |
| `detect` | Auto-detect project type from indicator files and show convention snippets |

- `--no-auto` — exclude auto-detected project context (only show manual `context.d/` files)

**Auto-detection:** Scans the working directory for 17 project types (go, nodejs, typescript, python, rust, cdk, java-maven, java-gradle, ruby, docker, terraform, make, cpp, csharp, gdscript, php, swift) via indicator files and glob patterns. Detected types inject convention snippets (~200 bytes each) covering build, test, and lint commands. Manual `context.d/` files shadow auto-detected entries by `(role, name)` key.

**Directory layout:**

```
.muxcode/context.d/              # Project-local (highest priority)
  shared/                        # Applied to all roles
    conventions.md
    architecture.md
  edit/                          # Role-specific
    refactoring-guide.md
  build/
    troubleshooting.md

~/.config/muxcode/context.d/     # User-level (lower priority)
  shared/
    my-patterns.md
```

- `shared/` files injected into every role's prompt
- `<role>/` files injected only for that role
- Project files shadow user files by filename (same key = role + name)
- Only `.md` files read; subdirectories within role dirs and other extensions ignored
- No `create`/`load`/`search` — users create files directly with their editor

**Prompt injection order:**

```
Agent definition → Shared prompt → Skills → Project Context → Session Resume
```

**Output format (prompt):**

```markdown
## Project Context

### conventions
Use 2-space indentation

### architecture
Event-driven microservices
```

**Examples:**

```bash
# Create context files
$ mkdir -p .muxcode/context.d/shared .muxcode/context.d/edit
$ echo "Use 2-space indentation" > .muxcode/context.d/shared/conventions.md
$ echo "Prefer minimal diffs" > .muxcode/context.d/edit/patterns.md

# List all context files
$ muxcode-agent-bus context list
conventions              shared           project
patterns                 edit             project

# List files for a specific role
$ muxcode-agent-bus context list --role edit
conventions              shared           project
patterns                 edit             project

# Generate prompt for a role
$ muxcode-agent-bus context prompt edit
## Project Context

### conventions
Use 2-space indentation

### patterns
Prefer minimal diffs
```

### `muxcode-agent-bus agent`

Run a local LLM agentic loop for a role via Ollama, replacing Claude Code for that role.

```bash
muxcode-agent-bus agent run <role> [--model MODEL] [--url URL]
```

- `<role>` — agent role to run (e.g. `git`, `build`, `runner`)
- `--model MODEL` — Ollama model name (default: `MUXCODE_OLLAMA_MODEL` or `qwen2.5-coder:7b`)
- `--url URL` — Ollama base URL (default: `MUXCODE_OLLAMA_URL` or `http://localhost:11434`)

**Agentic loop:**

1. Builds system prompt from agent definition + shared prompt + skills + context.d + session resume
2. Builds tool definitions from the role's tool profile (allowedTools enforcement)
3. Polls inbox every 3 seconds for new messages
4. Sends conversation to Ollama's OpenAI-compatible API (`POST /v1/chat/completions`) with tool definitions
5. Executes tool calls (bash, read_file, glob, grep, write_file, edit_file) — max 20 turns per inbox batch
6. Sends final response back via bus, logs bash commands to `{role}-history.jsonl`

**Tool execution details:**

| Tool | Ollama function | Notes |
|------|----------------|-------|
| `bash` | `bash` | 60s timeout, 10K char output truncation, allowedTools enforced |
| `read_file` | `read_file` | Returns file content |
| `glob` | `glob` | `filepath.Glob` matching |
| `grep` | `grep` | Shells out to `grep -rn --exclude-dir` |
| `write_file` | `write_file` | Full file write |
| `edit_file` | `edit_file` | String replacement in file |

**Auto-pull:** If the model is not found locally, runs `ollama pull` automatically before starting.

**Examples:**
```bash
# Run the git manager via local LLM
$ muxcode-agent-bus agent run git

# Use a specific model
$ muxcode-agent-bus agent run git --model codellama:13b

# Custom Ollama URL
$ muxcode-agent-bus agent run build --url http://192.168.1.100:11434
```

### `muxcode-agent-bus subscribe`

Manage event subscriptions for fan-out after chain execution.

```bash
muxcode-agent-bus subscribe add <event> <outcome> <notify-role> <action> [message-template]
muxcode-agent-bus subscribe list [--all]
muxcode-agent-bus subscribe remove <id>
muxcode-agent-bus subscribe enable <id>
muxcode-agent-bus subscribe disable <id>
```

**Subcommands:**

| Subcommand | Description |
|------------|-------------|
| `add` | Create a new subscription |
| `list` | Show enabled subscriptions (use `--all` to include disabled) |
| `remove` | Delete a subscription by ID |
| `enable` | Enable a disabled subscription |
| `disable` | Disable a subscription without removing it |

- `<event>` — event to match: `build`, `test`, `deploy`, or `*` (wildcard)
- `<outcome>` — outcome to match: `success`, `failure`, or `*` (wildcard)
- `<notify-role>` — role to notify when matched
- `<action>` — action name for the sent message
- `[message-template]` — optional template with `${event}`, `${outcome}`, `${exit_code}`, `${command}` (default: `"${event} ${outcome}: ${command}"`)

**Examples:**
```bash
# Notify watch agent on any build failure
$ muxcode-agent-bus subscribe add build failure watch alert "Build failed: ${command}"

# Notify analyst on all events
$ muxcode-agent-bus subscribe add "*" "*" analyze observe

# List subscriptions
$ muxcode-agent-bus subscribe list
```

**Data files:**

| File | Location | Purpose |
|------|----------|---------|
| `subscriptions.jsonl` | `/tmp/muxcode-bus-{SESSION}/subscriptions.jsonl` | Subscription definitions |

### `muxcode-agent-bus session`

Manage session context — save summaries for context preservation across restarts.

```bash
muxcode-agent-bus session status
muxcode-agent-bus session compact "<summary>"
```

- `status` — show session uptime and compact count
- `compact "<summary>"` — save session summary to memory for restoration on restart

### `muxcode-agent-bus skill`

Manage skill definitions — file-based plugins for reusable instruction sets.

```bash
muxcode-agent-bus skill list [--role ROLE]
muxcode-agent-bus skill load <name>
muxcode-agent-bus skill search <query>
muxcode-agent-bus skill create <name> <desc> [--roles r1,r2] [--tags t1,t2] <body>
muxcode-agent-bus skill prompt <role>
```

| Subcommand | Description |
|------------|-------------|
| `list` | Show available skills, filterable by `--role` |
| `load` | Load a skill by name (output its content) |
| `search` | Search skills by keyword |
| `create` | Create a new skill definition file |
| `prompt` | Output all skills for a role (used by agent launcher for prompt injection) |

**Resolution order:** `.muxcode/skills/` (project) > `~/.config/muxcode/skills/` (user) > `skills/` (defaults). Project skills shadow user skills by name.

### `muxcode-agent-bus tools`

Resolve and display the tool profile for a role.

```bash
muxcode-agent-bus tools <role>
```

Outputs one `--allowedTools` pattern per line. Resolves shared includes (`bus`, `readonly`, `common`), applies `CdPrefix` variants, and appends role-specific patterns from `bus/profile.go`.

**Examples:**
```bash
# Show git agent's tool permissions
$ muxcode-agent-bus tools git
Bash(muxcode-agent-bus *)
Bash(git *)
Bash(gh *)
Read
Glob
Grep
...
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

Messages from `build`, `test`, or `review` to any non-edit agent are automatically copied to the edit inbox via `Send()`, giving the orchestrator visibility into all workflow events. Chain-triggered messages and subscription fan-out use `SendNoCC()` to avoid redundant CC copies (the edit agent already receives chain results directly).

### Build-Test-Review Chain

Driven by `muxcode-bash-hook.sh`, not by agent LLMs:

1. **Build succeeds** -> hook sends `request:test` to the test agent
2. **Test succeeds** -> hook sends `request:review` to the review agent
3. **Any failure** -> hook sends `event:notify` directly to edit
4. After primary chain action, subscription fan-out fires for matching event+outcome patterns

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
│   ├── guard.go       # Loop detection (command retries, message ping-pong)
│   ├── compact.go     # Context compaction monitoring (size + staleness checks)
│   ├── proc.go        # Background process management (start, track, notify)
│   ├── spawn.go       # Spawned agent sessions (create, track, collect results)
│   ├── webhook.go     # Webhook HTTP endpoint (server, handlers, PID management)
│   ├── demo.go        # Demo scenarios (step engine, built-in scenarios)
│   ├── context.go     # Context directory (drop-in context files per role)
│   ├── detect.go      # Project-aware context detection (17 project types)
│   ├── search.go      # BM25 memory search (tokenize, stem, rank)
│   ├── rotation.go    # Daily memory rotation (archive, retention, context window)
│   ├── profile.go     # Tool profiles (per-role permissions, shared groups)
│   ├── subscribe.go   # Event subscriptions (fan-out after chain execution)
│   ├── ollama.go      # Ollama HTTP client (ChatComplete, CheckHealth)
│   ├── tools.go       # Tool definitions for local LLM (BuildToolDefs, IsToolAllowed)
│   ├── executor.go    # Tool executor for local LLM (bash, read, glob, grep, write, edit)
│   ├── agent.go       # Local LLM agentic loop (inbox poll, tool-call loop, history)
│   ├── cleanup.go     # Session cleanup
│   └── setup.go       # Bus directory initialization and re-init purge
├── cmd/               # Subcommand handlers
├── watcher/           # Inbox poller + trigger file monitor
├── tui/               # Dracula-themed dashboard TUI
└── main.go            # Entry point and subcommand dispatch
```
