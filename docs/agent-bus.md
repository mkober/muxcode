# muxcode — CLI Reference

Single Go binary for inter-agent communication in muxcode sessions. Manages message routing, persistent memory, inbox notifications, and the dashboard TUI.

## Module Location

```
tools/muxcode/
```

## Build Instructions

From the repo root:
```bash
make build
```

The binary is built to `bin/muxcode` and installed to `~/.local/bin/muxcode`.

## CLI Reference

### `muxcode init`

Initialize the message bus directory structure for a session.

```bash
muxcode init [--memory-dir PATH]
```

Creates the ephemeral bus directory at `/tmp/muxcode-bus-{SESSION}/` with `inbox/`, `lock/`, and `log.jsonl`. Optionally initializes the persistent memory directory.

### `muxcode send`

Send a message to another agent's inbox.

```bash
muxcode send <to> <action> "<payload>" [--type TYPE] [--reply-to ID] [--no-notify] [--force] [--wait]
```

- `<to>` — target agent role (edit, build, test, review, deploy, run, commit, analyze, api)
- `<action>` — action name (build, test, review, deploy, run, commit, analyze, notify, etc.)
- `<payload>` — message content (quoted string)
- `--type TYPE` — message type: `request` (default), `response`, or `event`
- `--reply-to ID` — ID of the message being replied to
- `--no-notify` — skip tmux notification to the target agent
- `--force` — bypass pre-commit safeguard (only relevant when sending commit actions to the commit agent)
- `--wait` — after sending, poll the sender's inbox every 2s until a response arrives or timeout. Timeout controlled by `MUXCODE_INBOX_POLL_TIMEOUT` (default 600s). The response is printed to stdout inline.

**Pre-commit safeguard:** When sending a commit action (`commit`, `stage`, `push`, `merge`, `rebase`, `tag`) to the commit agent, the bus checks that all other agents (excluding edit, commit, watch) have empty inboxes, are not busy, and have no running background processes. If any agent has pending work, the send is blocked with an error. Use `--force` to bypass.

**Dedup guard:** Duplicate messages (same `from`, `to`, `action`, `type` tuple) within a 30-second window are automatically suppressed. The check and send are performed atomically under a file lock (`dedup.lock`) to prevent TOCTOU races. System actions are never deduped. Configure the window via `MUXCODE_DEDUP_WINDOW` env var (seconds); set to `0` to disable.

Auto-detects sender from `AGENT_ROLE` env var or tmux window name.

**Example:**
```
$ muxcode send build build "Run ./build.sh and report results" --wait
Sent request:build to build

--- Message from build at 14:32:05 ---
Type: response  Action: build
Content: Build succeeded. 0 errors, 0 warnings.
```

### `muxcode inbox`

Read messages from an agent's inbox.

```bash
muxcode inbox [--peek] [--raw] [--role ROLE]
```

- Default mode: consume messages and format as actionable prompts with reply commands
- `--peek` — non-destructive preview (does not consume messages)
- `--raw` — dump raw JSONL
- `--role ROLE` — read a specific role's inbox (defaults to own role)

**Example:**
```
$ muxcode inbox
You have new messages! Check below and reply to any that need action.

---
📨 Message from edit (request)
Action: build
Message: Run ./build.sh and report results
ID: 1708300000-edit-a1b2c3d4

→ Reply: muxcode send edit build "<your reply>" --type response --reply-to 1708300000-edit-a1b2c3d4
---
```

### `muxcode memory`

Read, write, search, and list persistent per-project memory.

```bash
muxcode memory read [role|shared]
muxcode memory write "<section>" "<text>"
muxcode memory write-shared "<section>" "<text>"
muxcode memory write-global "<section>" "<text>"
muxcode memory read-global [role]
muxcode memory context [--no-global]
muxcode memory context-global [--days N]
muxcode memory search <query> [--role ROLE] [--limit N] [--scope project|global|all]
muxcode memory list [--role ROLE] [--scope project|global|all]
```

- `read` — read a specific role's memory or shared memory
- `write` — append to own role's memory file
- `write-shared` — append to the shared memory file
- `write-global` — append to the global (cross-session) memory file at `~/.config/muxcode/memory/`
- `read-global` — read global memory (shared or role-specific)
- `context` — output global + shared + role memory. Use `--no-global` to skip global memory section
- `context-global` — output only global memory (shared + role)
- `search` — keyword search across all memory entries with relevance scoring (header matches weighted 2x). Supports `--role` to filter by role, `--limit` to cap results, and `--scope` to filter by source (`project`, `global`, or `all`; default: `all`). Query terms are matched case-insensitively via substring matching. Silent output on no results.
- `list` — show a columnar inventory of all memory sections across all roles. Supports `--role` to filter by role and `--scope` to filter by source.

Project memory is stored in `.muxcode/memory/` relative to the project directory. Global (cross-session) memory is stored in `~/.config/muxcode/memory/` and persists across all projects.

**Search examples:**
```bash
$ muxcode memory search "pnpm build"
--- [build] Build Config (2026-02-21 14:27) score:4.0 ---
use pnpm for all builds

$ muxcode memory search "permission" --role shared
--- [shared] Agent Permissions (2026-02-21 14:30) score:2.0 ---
edit agent must never run build commands directly

$ muxcode memory list
shared     Agent Permissions                    2026-02-21 14:27
edit       delegation rules                     2026-02-20 17:30
build      Build Config                         2026-02-21 14:27
```

### `muxcode watch`

Run the unified bus watcher daemon.

```bash
muxcode watch [session] [--poll N] [--debounce N] [--monitor]
```

- Polls agent inboxes and notifies agents via `tmux display-message` (passive status bar flash) when new messages arrive
- Monitors the analyze trigger file and routes file-edit events to relevant agents based on file patterns
- `--poll N` — inbox polling interval in seconds (default: 2)
- `--debounce N` — trigger file debounce interval in seconds (default: 8)
- `--monitor` — run as watcher health monitor instead of the watcher itself. Checks the watcher keepalive every 15 seconds; if stale (>30s), kills and relaunches the watcher process. Exits cleanly when the tmux session is gone.

Without `--monitor`, runs in the `analyze` window left pane as the primary watcher. With `--monitor`, runs as a companion background process launched by `muxcode.sh`.

#### Trigger file format

The trigger file (`/tmp/muxcode-analyze-{SESSION}.trigger`) is written by `muxcode hook analyze` with one line per file edit:

```
<unix-timestamp> <filepath>
```

When the watcher detects a change in the trigger file, it starts debouncing. After the debounce interval elapses with no further changes, the watcher:

1. Reads the trigger file and collects unique file paths
2. Sends an aggregate `analyze` event to the analyst agent with all edited files
3. Truncates the trigger file

Per-file routing to specific agents (test/deploy/build) is handled earlier by `hook analyze` at edit time — the watcher only handles the aggregate analyst notification.

### `muxcode dashboard`

Launch the Dracula-themed terminal dashboard TUI.

```bash
muxcode dashboard [--refresh N]
```

- Displays agent window statuses (active/ready/idle/error)
- Shows per-agent cost and token usage
- Shows inbox counts and lock status
- Shows recent log entries and inter-agent messages
- Monitors Claude Code teams and tasks (these are Claude Code's built-in Task tool sub-agents, not muxcode's own bus coordination)
- `--refresh N` — refresh interval in seconds (default: 5)
- Dynamically reads windows from the tmux session

Runs in the `status` window (F9). Press `q` to quit, `r` to refresh.

### `muxcode cleanup`

Remove the ephemeral bus directory and trigger files.

```bash
muxcode cleanup [session]
```

Removes `/tmp/muxcode-bus-{SESSION}/` and `/tmp/muxcode-analyze-{SESSION}.trigger`. Called automatically by the tmux session-closed hook.

### `muxcode notify`

Send a tmux notification to an agent's pane.

```bash
muxcode notify <role>
```

Sends a passive `tmux display-message` to the target agent's window (status bar flash). The notification includes a preview: `[role] [from -> action] payload -> Run: muxcode inbox`. Harness panes are skipped (they poll inbox directly).

**Note:** `muxcode send` calls `notify` automatically. Use `--no-notify` to suppress.

### `muxcode cron`

Manage scheduled tasks that fire bus messages on a cadence.

```bash
muxcode cron add <schedule> <target> <action> <message>
muxcode cron list [--all]
muxcode cron remove <id>
muxcode cron enable <id>
muxcode cron disable <id>
muxcode cron history [--id CRON_ID] [--limit N]
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
$ muxcode cron add "@every 5m" commit status "Run git status and report"
Added cron entry: 1771897000-cron-a1b2c3d4
  Schedule: @every 5m  Target: commit  Action: status
  Message: Run git status and report

# Schedule hourly test runs
$ muxcode cron add "@hourly" test test "Run tests and report results"

# List all enabled entries
$ muxcode cron list

# Disable an entry
$ muxcode cron disable 1771897000-cron-a1b2c3d4

# View execution history
$ muxcode cron history --limit 10
```

**Watcher integration:** The bus watcher (`muxcode watch`) checks for due cron entries on each poll cycle. It reloads the cron file from disk at most every 10 seconds to avoid excessive filesystem reads. When a cron entry fires, the watcher sends a bus message to the target agent, updates `last_run_ts`, appends to execution history, and notifies the target via tmux.

**Data files:**

| File | Location | Purpose |
|------|----------|---------|
| `cron.jsonl` | `/tmp/muxcode-bus-{SESSION}/cron.jsonl` | Cron entry definitions |
| `cron-history.jsonl` | `/tmp/muxcode-bus-{SESSION}/cron-history.jsonl` | Execution history log |

### `muxcode status`

Show all agents' current state overview.

```bash
muxcode status [--json]
```

- Default: human-readable table with role, state, inbox count, and last activity
- `--json` — output as JSON array for programmatic use
- STATE: `busy` (lock file exists) or `idle`
- LAST ACTIVITY: timestamp + direction arrow (← received, → sent) + peer:action from log.jsonl
- Roles with no activity show `—`

**Example:**
```
$ muxcode status
ROLE         STATE  INBOX  LAST ACTIVITY
edit         idle   0      14:32 ← build:response
build        busy   1      14:31 ← edit:compile
test         idle   0      14:30 ← build:test
review       idle   0      —
```

### `muxcode workflow`

Query or reset the workflow state machine.

```bash
muxcode workflow [--json]
muxcode workflow reset
```

- Default: human-readable state with name, duration, trigger, files, and accumulated outcomes
- `--json` — raw `WorkflowStateEntry` as JSON
- `reset` — manually transition to `idle` (clears all outcomes)

The workflow state machine tracks the editing lifecycle (edit→build→test→review) as a single persisted state. It is purely observational — it never blocks actions.

**Example:**
```
$ muxcode workflow
state: testing  since: 2m ago  trigger: chain:build:success  files: 3  outcomes: build:success

$ muxcode workflow --json
{"state":"testing","prev_state":"building","since":1711324800,"updated":1711324860,"trigger":"chain:build:success","files_changed":3,"last_files":["hook.go","config.go","cmd/hook.go"],"build_outcome":"success","test_outcome":"","review_outcome":"","deploy_outcome":""}

$ muxcode workflow reset
Workflow state reset to idle
```

**Data files:**

| File | Location | Purpose |
|------|----------|---------|
| `workflow-state.json` | `/tmp/muxcode-bus-{SESSION}/workflow-state.json` | Current workflow state |
| `workflow.lock` | `/tmp/muxcode-bus-{SESSION}/lock/workflow.lock` | Flock for atomic transitions |

See [Architecture](architecture.md#workflow-state-machine) for state definitions, transition sources, and the regression rule.

### `muxcode history`

Show recent messages to/from an agent.

```bash
muxcode history <role> [--limit N] [--context]
```

- `<role>` — show messages involving this role (from `log.jsonl`)
- `--limit N` — show last N messages (default: 20)
- `--context` — output as a markdown block for prompt injection

**Default output:**
```
$ muxcode history build
--- Message history for build (last 20) ---
14:30  edit → build  [request:build] Run ./build.sh and report results
14:31  build → test  [request:test] Build succeeded — run tests
14:31  build → edit  [response:build] Build succeeded: Go binary built
```

**Context output (`--context`):**
```
$ muxcode history build --context
## Recent activity for build

- 14:30 [request from edit] Run ./build.sh and report results
- 14:31 [response to edit] Build succeeded: Go binary built
- 14:31 [request to test] Build succeeded — run tests
```

### `muxcode guard`

Check for agent loop patterns — command retries and message ping-pong.

```bash
muxcode guard [role] [--json] [--threshold N] [--window N]
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
$ muxcode guard
⚠ LOOP DETECTED: build
  Type: command
  Command: go build ./... (failed 3x in 2m)
  Action: Check build window — agent may be stuck

# Check a specific agent as JSON
$ muxcode guard build --json
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
$ muxcode guard --threshold 5 --window 600
```

**Watcher integration:** The bus watcher checks for loops every 60 seconds. When a loop is detected, it sends a `loop-detected` event to the edit agent and notifies via tmux. Alerts are deduplicated within a 10-minute cooldown (exceeds the 5-minute detection window to prevent self-sustaining alerts). System actions (`loop-detected`, `compact-recommended`, `proc-complete`, `spawn-complete`) are excluded from message loop detection.

#### Watcher event: `compact-recommended`

The watcher monitors agent context size (memory + history + log files) and staleness (time since last compaction) every 120 seconds. When **both** conditions are met — total tracked size > 512 KB **and** time since last compact > 2 hours — the watcher sends a `compact-recommended` event to the role itself with an actionable message:

```
Context approaching limits for edit (total: 620 KB, memory: 180 KB, history: 340 KB, log: 100 KB).
Last compact: 2h 30m ago. Run: muxcode session compact "<summary>"
```

Alerts are deduplicated within a 10-minute cooldown per role. The agent receiving the alert should run `muxcode session compact "<summary>"` to save its context and reset the staleness timer.

### `muxcode proc`

Manage background processes — launch, track, and auto-notify on completion.

```bash
muxcode proc start "<command>" [--dir DIR]
muxcode proc list [--all]
muxcode proc status <id>
muxcode proc log <id> [--tail N]
muxcode proc stop <id>
muxcode proc clean
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
$ muxcode proc start "./build.sh"
Started process: 1740000000-proc-a1b2c3d4
  PID: 12345  Owner: build
  Command: ./build.sh
  Log: /tmp/muxcode-bus-mysession/proc/1740000000-proc-a1b2c3d4.log

# Check running processes
$ muxcode proc list
ID                                   PID      STATUS     OWNER      STARTED    COMMAND
----------------------------------------------------------------------------------------------------
1740000000-proc-a1b2c3d4             12345    running    build      14:00:00   ./build.sh

# View process log
$ muxcode proc log 1740000000-proc-a1b2c3d4 --tail 20

# Stop a process
$ muxcode proc stop 1740000000-proc-a1b2c3d4

# Clean up finished processes
$ muxcode proc clean
Cleaned 2 finished process(es).
```

**Watcher integration:** The bus watcher checks running processes on each poll cycle (2s). When a process completes, it sends a `proc-complete` event to the owner agent with the command, status, and exit code. The owner is notified via tmux.

**Data files:**

| File | Location | Purpose |
|------|----------|---------|
| `proc.jsonl` | `/tmp/muxcode-bus-{SESSION}/proc.jsonl` | Process entry definitions |
| `{id}.log` | `/tmp/muxcode-bus-{SESSION}/proc/{id}.log` | Per-process stdout/stderr output |

### `muxcode spawn`

Manage spawned agent sessions — create temporary agents for one-off tasks, collect results, and tear down.

```bash
muxcode spawn start <role> "<task>"
muxcode spawn list [--all]
muxcode spawn status <id>
muxcode spawn result <id>
muxcode spawn stop <id>
muxcode spawn clean
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
$ muxcode spawn start research "What does bus/guard.go do?"
Started spawn: 1771900000-spawn-a1b2c3d4
  Role: research  Spawn Role: spawn-a1b2c3d4  Owner: edit
  Window: spawn-a1b2c3d4
  Task: What does bus/guard.go do?

# Check running spawns
$ muxcode spawn list
ID                                   ROLE         SPAWN-ROLE   STATUS     OWNER      TASK
--------------------------------------------------------------------------------------------------------------
1771900000-spawn-a1b2c3d4            research     spawn-a1b2c  running    edit       What does bus/guard.go do?

# Get the result after completion
$ muxcode spawn result 1771900000-spawn-a1b2c3d4

# Stop a running spawn
$ muxcode spawn stop 1771900000-spawn-a1b2c3d4

# Clean up finished spawns
$ muxcode spawn clean
Cleaned 1 finished spawn(s).
```

**Watcher integration:** The bus watcher checks spawned agent windows on each poll cycle (2s). When a spawn's tmux window no longer exists, it marks the spawn as `completed`, extracts the last result message from `log.jsonl`, and sends a `spawn-complete` event to the owner agent with the result summary.

**Pre-commit safeguard:** Running spawns block commits, same as running background processes. Use `--force` on the send command to bypass.

**Data files:**

| File | Location | Purpose |
|------|----------|---------|
| `spawn.jsonl` | `/tmp/muxcode-bus-{SESSION}/spawn.jsonl` | Spawn entry definitions |

### `muxcode demo`

Run scripted demo scenarios — sends real bus messages, switches tmux windows, and toggles lock states with configurable timing.

```bash
muxcode demo run [SCENARIO] [--speed FACTOR] [--dry-run] [--no-switch]
muxcode demo list
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
$ muxcode demo list
Available demo scenarios:

  build-test-review          Full build-test-review-commit cycle across agent windows
                             20 steps, ~20s at 1.0x speed

# Dry-run (no tmux needed)
$ muxcode demo run --dry-run

# Live demo at 2x speed (for GIF recording)
$ muxcode demo run --speed 2.0

# Slow demo for live presentation
$ muxcode demo run --speed 0.5
```

**GIF capture:** Use `scripts/muxcode-demo.sh` to record the screen during a demo run and convert to GIF:

```bash
scripts/muxcode-demo.sh --speed 2.0 --output assets/demo.gif
```

Requires `ffmpeg` and `gifski` (`brew install ffmpeg gifski`). Auto-detects the screen capture device via avfoundation.

### `muxcode webhook`

Manage the webhook HTTP endpoint — an HTTP-to-bus bridge for external tools (CI/CD, GitHub webhooks, monitoring, custom scripts).

```bash
muxcode webhook start [--port PORT] [--host HOST] [--token TOKEN]
muxcode webhook stop
muxcode webhook status
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
$ muxcode webhook start
Webhook server started on 127.0.0.1:9090 (PID 54854)

# Start with auth token
$ muxcode webhook start --port 8080 --token mysecret

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
$ muxcode webhook status
Webhook: running on 127.0.0.1:9090 (PID 54854)

# Stop
$ muxcode webhook stop
Webhook server stopped
```

**Data files:**

| File | Location | Purpose |
|------|----------|---------|
| `webhook.pid` | `/tmp/muxcode-bus-{SESSION}/webhook.pid` | PID file (`port:pid` format) |

### `muxcode context`

Manage per-agent drop-in context files — a lightweight, file-based way to inject project-specific knowledge into agent prompts without the frontmatter/roles/tags overhead of skills.

```bash
muxcode context list [--role ROLE] [--no-auto]
muxcode context prompt <role> [--no-auto]
muxcode context detect [DIR]
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
$ muxcode context list
conventions              shared           project
patterns                 edit             project

# List files for a specific role
$ muxcode context list --role edit
conventions              shared           project
patterns                 edit             project

# Generate prompt for a role
$ muxcode context prompt edit
## Project Context

### conventions
Use 2-space indentation

### patterns
Prefer minimal diffs
```

### `muxcode agent`

Run a local LLM agentic loop for a role via Ollama, replacing Claude Code for that role.

```bash
muxcode agent run <role> [--model MODEL] [--url URL]
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
$ muxcode agent run git

# Use a specific model
$ muxcode agent run git --model codellama:13b

# Custom Ollama URL
$ muxcode agent run build --url http://192.168.1.100:11434
```

### `muxcode agent launch`

Launch a Claude Code (or local LLM) agent for a role. Replaces `muxcode-agent.sh` — resolves agent file, model, tools, prompt, and execs the agent CLI.

```bash
muxcode agent launch <role>
```

- `<role>` — agent role to launch (e.g. `edit`, `build`, `test`, `commit`)

**Resolution cascade:**

1. **Config loading** — reads shell-sourceable config from `MUXCODE_CONFIG` > `.muxcode/config` > `~/.config/muxcode/config`
2. **CLI selection** — checks `MUXCODE_{ROLE}_CLI` env var; if `"local"`, routes to Ollama harness
3. **Agent file** — 3-tier search: `.claude/agents/<name>.md` > `~/.config/muxcode/agents/<name>.md` > install dir defaults
4. **Model** — per-role env (`MUXCODE_{ROLE}_CLAUDE_MODEL`) > global env (`MUXCODE_CLAUDE_MODEL`) > role default (opus for edit/review/analyze, sonnet for others)
5. **Tools** — resolves from tool profiles in `muxcode.json`
6. **Prompt** — assembles shared coordination prompt + skills + context.d + session resume
7. **Venv** — activates Python venv if found (`MUXCODE_VENV_DIR` > `.venv` > `venv`)
8. **Exec** — replaces process with `claude` (or `muxcode-llm-harness` for local LLM)

**Pre-launch actions:**

- Sends startup inbox message for `edit` and `analyze` roles (context restoration)
- Logs agent launch to persistent lifecycle log

**Examples:**
```bash
# Launch the build agent (standard usage from muxcode.sh)
$ muxcode agent launch build

# Launch the edit agent (prompted mode, opus model)
$ muxcode agent launch edit
```

### `muxcode subscribe`

Manage event subscriptions for fan-out after chain execution.

```bash
muxcode subscribe add <event> <outcome> <notify-role> <action> [message-template]
muxcode subscribe list [--all]
muxcode subscribe remove <id>
muxcode subscribe enable <id>
muxcode subscribe disable <id>
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
$ muxcode subscribe add build failure watch alert "Build failed: ${command}"

# Notify analyst on all events
$ muxcode subscribe add "*" "*" analyze observe

# List subscriptions
$ muxcode subscribe list
```

**Data files:**

| File | Location | Purpose |
|------|----------|---------|
| `subscriptions.jsonl` | `/tmp/muxcode-bus-{SESSION}/subscriptions.jsonl` | Subscription definitions |

### `muxcode agent-health`

Manage agent liveness monitoring — stop/start auto-restart, check agent status.

```bash
muxcode agent-health --check <role>
muxcode agent-health --stop <role>
muxcode agent-health --start <role>
```

- `--check <role>` — report agent status: `alive`, `dead`, `stopped`, or `excluded`
- `--stop <role>` — write a stopped marker, suppressing auto-restart for that role
- `--start <role>` — remove the stopped marker, re-enabling auto-restart

The watcher probes agent panes every 30 seconds. Three consecutive failures trigger auto-restart (capped at 3 per role per session). Excluded roles: `edit`, `webhook`, `spawn-*`.

**Data files:**

| File | Location | Purpose |
|------|----------|---------|
| `{role}.stopped` | `/tmp/muxcode-bus-{session}/lock/` | Marker suppressing auto-restart |
| `watcher.keepalive` | `/tmp/muxcode-bus-{session}/` | Unix timestamp updated each watcher poll loop |

### `muxcode lifecycle`

Persistent lifecycle logging for debugging process and session issues across restarts. Logs are stored at `~/.config/muxcode/logs/{session}.log` as JSONL — survives session cleanup since the bus directory at `/tmp/` is ephemeral.

```bash
muxcode lifecycle log <session> <level> <source> <event> [--detail TEXT] [--pid N]
muxcode lifecycle show [session] [--limit N] [--source S] [--level L] [--event E] [--since DURATION] [--all]
muxcode lifecycle list
muxcode lifecycle purge [--days N]
```

| Subcommand | Description |
|------------|-------------|
| `log` | Write a lifecycle entry (used by bash scripts via CLI) |
| `show` | Display filtered entries with human-readable timestamps (default: last 50) |
| `list` | List sessions with lifecycle logs, entry counts, and last modified date |
| `purge` | Remove log files older than N days (default: 30) |

**Entry format:**

```json
{"ts":1710244800,"level":"info","source":"launcher","session":"muxcode","event":"watcher-start","pid":12345,"detail":"PID: 12345"}
```

| Field | Description |
|-------|-------------|
| `ts` | Unix timestamp |
| `level` | `info`, `warn`, or `error` |
| `source` | Origin: `launcher`, `watcher`, `monitor`, `auto-accept`, `agent`, `init`, `cleanup` |
| `session` | Session name |
| `event` | Machine-readable event type (see catalog below) |
| `pid` | Process ID (optional) |
| `detail` | Human-readable context (optional) |

**Event catalog:**

| Source | Event | Level | When |
|--------|-------|-------|------|
| launcher | session-start | info | `muxcode.sh` begins |
| launcher | bus-init | info | `muxcode init` completes |
| launcher | stale-kill | info | Killed stale watcher or monitor |
| launcher | watcher-start | info | Watcher process launched (with PID) |
| launcher | monitor-start | info | Monitor process launched (with PID) |
| launcher | session-create | info | tmux session created (with window list) |
| launcher | session-ready | info | Session fully initialized |
| auto-accept | trust-prompt | info | Workspace trust prompt dismissed |
| auto-accept | bypass-prompt | info | Bypass permissions prompt dismissed |
| auto-accept | agent-ready | info | Agent reached idle prompt |
| auto-accept | complete | info | All agents past prompts |
| init | init | info | Fresh bus directory created |
| init | re-init | info | Stale data purged from previous session |
| agent | launch | info | Agent process exec'd (role + CLI) |
| watcher | started | info | Watcher `Run()` begins (with PID) |
| watcher | lock-failed | error | Another watcher already running |
| watcher | inbox-notify | info | Agent notified of new messages |
| watcher | startup-notify | info | First-idle notification delivery |
| watcher | trigger-route | info | Edit events routed to analyst |
| watcher | cron-fire | info | Cron entry executed |
| watcher | proc-complete | info | Background process finished |
| watcher | spawn-complete | info | Spawned agent finished |
| watcher | loop-detected | warn | Loop alert sent |
| watcher | compact-alert | warn | Compaction recommended |
| watcher | ollama-probe-fail | warn | Ollama health check failed |
| watcher | ollama-restart | warn | Ollama restart attempted |
| watcher | ollama-recovered | info | Ollama back online |
| watcher | agent-health-fail | warn | Agent health check failed |
| watcher | agent-restart | warn | Agent restart attempted |
| watcher | agent-recovered | info | Agent came back |
| monitor | session-gone | info | tmux session gone, monitor exiting |
| monitor | stale-detected | warn | Watcher keepalive stale |
| monitor | watcher-restart | info | Watcher restarted by monitor |
| cleanup | session-cleanup | info | Bus directory removed |

**Rotation:** 1000 entries per file (configurable via `MUXCODE_LIFECYCLE_LOG_MAX` env var).

**Examples:**

```bash
# Show recent events for current session
$ muxcode lifecycle show
2026-03-12 08:30:01  info   launcher       session-start           Project: ~/Repos/mkober/muxcode
2026-03-12 08:30:01  info   init           init                    Creating bus directory: /tmp/muxcode-bus-muxcode
2026-03-12 08:30:01  info   launcher       watcher-start           PID: 12345
2026-03-12 08:30:15  info   auto-accept    agent-ready             edit
2026-03-12 08:30:16  info   watcher        startup-notify          edit

# Filter by source and level
$ muxcode lifecycle show --source watcher --level warn --since 1h

# Show events for a subsession
$ muxcode lifecycle show is-admissions-gateway --limit 20

# List all sessions with logs
$ muxcode lifecycle list
  muxcode                         42 entries  2026-03-12 08:30
  is-admissions-gateway           18 entries  2026-03-12 08:28

# Clean up old logs
$ muxcode lifecycle purge --days 30
Purged 3 log file(s) older than 30 days
```

**Data files:**

| File | Location | Purpose |
|------|----------|---------|
| `{session}.log` | `~/.config/muxcode/logs/` | JSONL lifecycle events per session name |

### `muxcode session`

Manage session context — save summaries for context preservation across restarts.

```bash
muxcode session status
muxcode session compact "<summary>"
```

- `status` — show session uptime and compact count
- `compact "<summary>"` — save session summary to memory for restoration on restart

### `muxcode skill`

Manage skill definitions — file-based plugins for reusable instruction sets.

```bash
muxcode skill list [--role ROLE]
muxcode skill load <name>
muxcode skill search <query>
muxcode skill create <name> <desc> [--roles r1,r2] [--tags t1,t2] <body>
muxcode skill prompt <role>
```

| Subcommand | Description |
|------------|-------------|
| `list` | Show available skills, filterable by `--role` |
| `load` | Load a skill by name (output its content) |
| `search` | Search skills by keyword |
| `create` | Create a new skill definition file |
| `prompt` | Output all skills for a role (used by agent launcher for prompt injection) |

**Resolution order:** `.muxcode/skills/` (project) > `~/.config/muxcode/skills/` (user) > `skills/` (defaults). Project skills shadow user skills by name.

#### Built-in skills

| Skill | Roles | Description |
|-------|-------|-------------|
| `git-commit-conventions` | commit, edit | Commit message format and git workflow conventions |
| `go-testing` | test, build | Go testing patterns and conventions |
| `code-review-checklist` | review | Code review quality checklist |
| `jira-pr-comment` | git | Post a comment on a Jira issue when a PR is created. Extracts the Jira key from the branch name (e.g. `DATA-456-*`, `PBP1-4365-*`) and posts PR link + diff stats via `muxcode atlassian jira comment`. Requires `JIRA_BASE_URL`, `JIRA_USER_EMAIL`, and `JIRA_API_TOKEN` in config. |
| `jira-update-description` | git, edit | Read and update a Jira issue description with ADF content. Extracts the Jira key from the request message or branch name. Uses `muxcode atlassian jira read/update`. Requires `JIRA_BASE_URL`, `JIRA_USER_EMAIL`, and `JIRA_API_TOKEN` in config. |
| `confluence-update-page` | git, edit | Read and update Confluence pages with ADF content. Pages identified by page ID, Confluence URL, or space key + title. Supports full replacement, append mode, and CQL search. Uses `muxcode atlassian confluence read/update/search`. Requires `CONFLUENCE_BASE_URL` (falls back to `JIRA_BASE_URL`), `JIRA_USER_EMAIL`, and `JIRA_API_TOKEN` in config. |

### `muxcode tools`

Resolve and display the tool profile for a role.

```bash
muxcode tools <role>
```

Outputs one `--allowedTools` pattern per line. Resolves shared includes (`bus`, `readonly`, `common`), applies `CdPrefix` variants, and appends role-specific patterns from `bus/profile.go`.

**Examples:**
```bash
# Show git agent's tool permissions
$ muxcode tools git
Bash(muxcode *)
Bash(git *)
Bash(gh *)
Read
Glob
Grep
...
```

### `muxcode lock` / `unlock` / `is-locked`

Manage agent busy indicators.

```bash
muxcode lock [role]
muxcode unlock [role]
muxcode is-locked [role]
```

- `lock` — create the lock file for the specified role (defaults to own role)
- `unlock` — remove the lock file
- `is-locked` — check lock status (exits 0 if locked, 1 if not)

### `muxcode api`

Manage API collections, environments, and request history. Data is stored in `.muxcode/api/` as JSON files.

#### Data files

| Path | Format | Description |
|------|--------|-------------|
| `.muxcode/api/environments/<name>.json` | JSON | Environment config (base URL, headers, variables) |
| `.muxcode/api/collections/<name>.json` | JSON | Request collection (name, description, requests) |
| `.muxcode/api/history.jsonl` | JSONL | Append-only log of executed requests |

#### Environment management

```bash
muxcode api env list
muxcode api env get <name>
muxcode api env create <name> --base-url <url>
muxcode api env set <name> <key> <value>
muxcode api env delete <name>
```

#### Collection management

```bash
muxcode api collection list
muxcode api collection get <name>
muxcode api collection create <name> [--description desc] [--base-url url]
muxcode api collection delete <name>
muxcode api collection add-request <collection> <name> --method GET --path /endpoint [--header key:value] [--body json] [--query key=value]
muxcode api collection remove-request <collection> <name>
```

#### History

```bash
muxcode api history [--collection name] [--limit N]
```

#### Import

```bash
muxcode api import <source-dir>
```

Copies environments and collections from a source directory into `.muxcode/api/`. Existing files are not overwritten. Example:

```bash
# Import the bundled httpbin example
muxcode api import examples/api
```

### `muxcode hook`

Hook handlers for Claude Code's PreToolUse and PostToolUse events. Each subcommand reads the tool event as JSON from stdin.

```bash
muxcode hook guard       # PreToolUse: edit agent command guard
muxcode hook bash        # PostToolUse: build/test/deploy chain triggers
muxcode hook analyze     # PostToolUse: file-edit trigger writer
muxcode hook inbox-poll  # PreToolUse: inbox check on tool execution
```

- `guard` — blocks prohibited commands for the edit agent (build, test, git, deploy, curl). Returns JSON `{"decision":"block","reason":"..."}` or passes through.
- `bash` — detects build, test, deploy, and git commands from exit codes and command text. Drives the build→test→review chain via `ResolveChain()`. Transitions the workflow state machine. Logs command history with error extraction.
- `analyze` — writes file-edit events to the analyze trigger file for watcher debounce. Transitions workflow to `editing`.
- `inbox-poll` — checks the agent's inbox on each tool execution and injects a "You have new messages" notification if messages are pending.

**Chain dedup:** Chain messages use `SendNoCCIfNotDuplicate()` with atomic file locking to prevent duplicate chain triggers within the 30-second dedup window.

**Workflow state guard:** Before firing chains, `triggerChain` checks the current workflow state — if state is already `reviewing`/`reviewed`, test success chain is skipped; if `testing`/`reviewing`/`reviewed`, build success chain is skipped. This prevents the test→review loop where review responses re-triggered the test chain.

Core code: `bus/hook.go` (library), `cmd/hook.go` (CLI dispatcher).

### `muxcode console`

Run a left-pane log console for an agent window.

```bash
muxcode console <role> [--interval N] [--once]
```

- `<role>` — the window name (build, test, review, deploy, run, commit, analyze, watch, api)
- `--interval N` — refresh interval in seconds (default: 2)
- `--once` — render once and exit (useful for testing)

Each role has a custom renderer showing relevant data: build/test/deploy show command history with exit codes, review shows review outcomes, commit shows git status + recent log, analyze shows recent file edits + workflow state, watch shows agent health + Ollama status, api shows request history. All use Dracula theme colors.

Core code: `bus/console.go` (library with `DefaultConsoleConfigs()` map), `cmd/console.go` (CLI handler).

### `muxcode atlassian`

Jira and Confluence API operations. Replaces the `muxcode-jira.sh` and `muxcode-confluence.sh` wrapper scripts with native Go `net/http` calls, eliminating Claude Code's curl permission prompt issues.

```bash
muxcode atlassian jira read <ISSUE-KEY>
muxcode atlassian jira update <ISSUE-KEY> <ADF-JSON-FILE>
muxcode atlassian jira comment <ISSUE-KEY> <ADF-JSON-FILE>
muxcode atlassian confluence read <PAGE-ID>
muxcode atlassian confluence update <PAGE-ID> <ADF-JSON-FILE>
muxcode atlassian confluence search <SPACE-KEY> <CQL-QUERY>
```

**Config resolution:** reads credentials from `.muxcode/config` > `~/.config/muxcode/config` > env vars (highest priority). Required vars: `JIRA_BASE_URL`, `JIRA_USER_EMAIL`, `JIRA_API_TOKEN`. For Confluence: `CONFLUENCE_BASE_URL` (falls back to `JIRA_BASE_URL`).

**Input validation:** Jira issue keys must match `[A-Z][A-Z0-9]*-[0-9]+`, Confluence page IDs must be numeric. Invalid inputs are rejected before any API call.

**Output format:** matches the original shell scripts — human-readable with `=== HEADER ===` sections, tab-separated search results, and raw ADF blocks.

Core code: `bus/atlassian.go` (config loading, HTTP client, API handlers, ADF text extraction), `cmd/atlassian.go` (CLI dispatcher).

### `muxcode pii-scrub`

Pipe-through PII and secret scrubber for stdin.

```bash
echo "user@example.com" | muxcode pii-scrub
# Output: [EMAIL_REDACTED]
```

Reads stdin, scrubs PII/secrets using the same regex patterns as the harness executor, writes scrubbed output to stdout. Logs redaction count to stderr when > 0. Used by Claude Code agents in PII-sensitive roles (`api`, `runner`/`run`, `watch`) to pipe tool output through before including in conversation.

Patterns: emails, SSN, credit cards (prefix-anchored), phone numbers (separator-required), AWS access keys, AWS secret keys, JWTs, generic secrets/tokens, dates of birth.

Core code: `bus/scrub.go` (patterns + `ScrubPII()`), `cmd/scrub.go` (CLI handler).

### `muxcode compact`

Trigger conversation compression for an agent.

```bash
muxcode compact [role]
```

Polls the agent's tmux pane for idle state (detects `❯` prompt) every second for up to 30 seconds. Once idle, clears residual input and injects `/compact` via `tmux send-keys` to trigger Claude Code's built-in conversation compression. If the agent doesn't become idle within the timeout, exits silently.

- `role` — target role (defaults to `AGENT_ROLE` env var)

This is a fire-and-forget command — run it in the background after saving context via `muxcode session compact "<summary>"`.

Core code: `cmd/compact.go`.

## Environment Variables

| Variable | Description |
|----------|-------------|
| `BUS_SESSION` | Session name for the bus directory |
| `AGENT_ROLE` | Current agent's role name (auto-detected from tmux window if unset) |
| `BUS_MEMORY_DIR` | Path to persistent memory directory (defaults to `.muxcode/memory/`) |
| `MUXCODE_ROLES` | Comma-separated extra roles to add to the known roles list |
| `MUXCODE_SPLIT_LEFT` | Space-separated windows with agent in pane 1 (defaults: edit api build test review deploy run analyze commit watch) |
| `MUXCODE_LIFECYCLE_LOG_MAX` | Max entries per lifecycle log before rotation (default: 1000) |
| `MUXCODE_DEDUP_WINDOW` | Dedup window in seconds for duplicate message suppression (default: 30, set to 0 to disable) |
| `MUXCODE_INBOX_POLL_TIMEOUT` | Timeout in seconds for `--wait` polling (default: 600) |

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

Driven by `muxcode hook bash`, not by agent LLMs:

1. **Build succeeds** -> hook sends `request:test` to the test agent
2. **Test succeeds** -> hook sends `request:review` to the review agent
3. **Any failure** -> hook sends `event:notify` directly to edit
4. After primary chain action, subscription fan-out fires for matching event+outcome patterns

## Pane Targeting

Pane targeting is consolidated in `bus/config.go`:

- **Split-left windows** (default: edit, api, build, test, review, deploy, run, analyze, commit, watch): agent runs in pane 1
- **All other windows**: agent runs in pane 0
- Override via `MUXCODE_SPLIT_LEFT` env var

## Architecture

```
tools/muxcode/
├── bus/               # Core library
│   ├── config.go      # Session/role/path/pane configuration
│   ├── message.go     # Message struct and JSONL encoding
│   ├── inbox.go       # Read/write/consume inbox files
│   ├── lock.go        # Lock file management
│   ├── memory.go      # Persistent memory read/write/search/list
│   ├── notify.go      # Tmux display-message notification
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
│   ├── api.go         # API testing (environments, collections, history, import)
│   ├── lifecycle.go   # Persistent lifecycle logging (~/.config/muxcode/logs/)
│   ├── hook.go        # Hook handlers (guard, bash, analyze, inbox-poll)
│   ├── console.go     # Left-pane log consoles (per-role renderers, Dracula theme)
│   ├── dedup.go       # Message dedup guard (30s window, flock-protected atomic check+send)
│   ├── scrub.go       # PII scrubbing (regex patterns, ScrubPII — mirrored in harness)
│   ├── workflow.go    # Workflow state machine (edit→build→test→review lifecycle)
│   ├── health.go      # Ollama health monitoring
│   ├── agent_health.go # Agent process health monitoring
│   ├── watcher_health.go # Watcher keepalive monitoring
│   ├── cleanup.go     # Session cleanup
│   └── setup.go       # Bus directory initialization and re-init purge
├── cmd/               # Subcommand handlers
├── watcher/           # Inbox poller + trigger file monitor
├── tui/               # Dracula-themed dashboard TUI
└── main.go            # Entry point and subcommand dispatch
```
