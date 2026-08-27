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
muxcode send <to> <action> "<payload>" [--type TYPE] [--reply-to ID] [--no-notify] [--force] [--wait] [--track]
```

- `<to>` — target agent role (edit, build, test, review, deploy, run, commit, analyze, api, watch)
- `<action>` — action name (build, test, review, deploy, run, commit, analyze, notify, etc.)
- `<payload>` — message content (quoted string)
- `--type TYPE` — message type: `request` (default), `response`, or `event`
- `--reply-to ID` — ID of the message being replied to
- `--no-notify` — skip tmux notification to the target agent
- `--force` — bypass pre-commit safeguard (only relevant when sending commit actions to the commit agent)
- `--wait` — after sending, poll the sender's inbox every 500ms until a response arrives or timeout. Timeout controlled by `MUXCODE_INBOX_POLL_TIMEOUT` (default 600s). The response is printed to stdout inline. **Auto-degrade:** if no response arrives within `MUXCODE_WAIT_DEGRADE_SECS` (default 90s; `0` disables), the send is converted into a tracked task and returns immediately — the sender unblocks and drains its inbox, and the daemon wakes it when the result lands (same self-heal as `--track`). Backed by `awaitOrTrack`/`degradeWaitSecs` in `cmd/send.go`.
- `--track` — after sending, create a tracked task and return immediately. The daemon auto-completes the task when the response arrives and wakes the sender via inbox notification. Use for long-running or fire-and-forget operations. Mutually exclusive with `--wait`.

**Pre-commit safeguard:** When sending a commit action (`commit`, `stage`, `push`, `merge`, `rebase`, `tag`) to the commit agent, the bus checks that all other agents (excluding edit, commit, watch) have empty inboxes, are not busy, and have no running background processes. If any agent has pending work, the send is blocked with an error. Use `--force` to bypass.

**Dedup guard:** Duplicate messages (same `from`, `to`, `action`, `type` tuple) within a 30-second window are automatically suppressed. The check and send are performed atomically under a file lock (`dedup.lock`) to prevent TOCTOU races. System actions are never deduped. Configure the window via `MUXCODE_DEDUP_WINDOW` env var (seconds); set to `0` to disable.

**Relay-loop suppression:** repeated identical agent-to-agent request relays are dropped once the same `(from,to,action)` tuple fires `>= MUXCODE_RELAY_SUPPRESS_THRESHOLD` times (default 4; `0` disables) within `MUXCODE_RELAY_SUPPRESS_WINDOW` seconds (default 300). Scoped to non-edit senders — prevents wedged relay storms (e.g. `run→watch` when the watch agent is stood down). Backed by `bus.CountRecentRequestTuple` (`guard.go`) and a guard in `cmd/send.go`.

**Stuck-task self-heal:** a tracked task left `in-flight` (delivered while the target was busy, never responded) used to permanently block all new `(to,action)` sends to that role via the dedup guard — even `--force` couldn't bypass it, so the target silently stopped receiving messages. Now `bus.TaskExpired` lets `HasInFlightTaskForRole`/`FindInFlightTask` (`dedup.go`) ignore expired in-flight tasks, and the daemon's `checkTrackedTasks` times them out (lifecycle `task-timeout`). Tasks self-heal after their timeout (default 600s). See also `muxcode deliver <role> [--force]` to force-drain a target's pending inbox.

Auto-detects sender from `AGENT_ROLE` env var or tmux window name.

**Examples:**
```
# Blocking — wait for response inline
$ muxcode send build build "Run ./build.sh and report results" --wait
Sent request:build to build

--- Message from build at 14:32:05 ---
Type: response  Action: build
Content: Build succeeded. 0 errors, 0 warnings.

# Non-blocking — track and continue working
$ muxcode send deploy deploy "Deploy to dev01" --track
Sent request:deploy to deploy
Tracking task 1779979541-edit-d2c7d769 — response will arrive in inbox
```

**Message hygiene:** keep delegations short, single-line, and intent-level.

- **Short, single-line, no newlines.** `validatePayload()` warns when a payload
  contains newlines ("may break allowedTools glob matching") or exceeds 500 chars.
  These warnings are advisory but real — a long or multi-line payload can miss the
  `Bash(muxcode *)` permission glob and trigger a permission prompt instead of
  delivering cleanly. Treat the warnings as errors to fix, not noise.
- **Delegate intent, not pre-baked artifacts.** Describe *what* you want done and
  let the receiving agent compose the details. Don't hand the commit agent a full
  multi-line commit message or an exhaustive file list — say what to commit and
  let it stage tracked files and write the message.
  - Avoid: `muxcode send commit commit "Stage fileA fileB fileC fileD, commit with message '<long body…>', exclude untracked doc, then push"` (~580 chars, multi-field).
  - Prefer: `muxcode send commit commit "Commit the canvas-api fixes for PR #188 (exclude the untracked doc) and push; report new HEAD." --force --track`
- **File handoff for genuinely long or structured content.** When the work *needs*
  a lot of detail (many items, multi-line bodies, per-item instructions), write it
  to a scratch file and send a **short** message pointing the agent at the file —
  never inline the content into the bus payload. The file holds the data and the
  per-item instructions; the bus message stays one short line. This keeps the bus
  uncluttered and avoids the length/newline/glob pitfalls entirely.
  - Pattern: `Write /tmp/<descriptive-name>.md` (self-describing — include IDs,
    bodies, and how to process each entry), then:
    `muxcode send commit pr-read "Read /tmp/pr188-comment-replies.md and post each entry's body as a reply to its PR #188 review comment id; report how many of the 12 posted." --track`
  - Good for: batches of PR-comment replies, multi-file commit plans, long
    generated bodies, anything you'd otherwise cram into one giant payload.
  - Prefer descriptive `/tmp/<name>.md` names; the receiving agent reads the file
    directly. (A future `muxcode send --payload-file` will formalize this — see
    `docs/requirements/backlog/MUX-010-delegation-message-hygiene.md`.)
- **Prefer `--track` for delegations.** Use `--track` for fire-and-forget /
  long-running work so the sender keeps working; reserve `--wait` for when the
  result is needed before the next step. A healthy `--wait` polls every 500ms and
  can look like a hang while the sender drains its inbox backlog — it is not stuck.

**Keep agents deliverable (no blocking foreground commands):** the daemon's
`checkIdleAgents()` delivers and notifies **only idle** agents. An agent stuck in
**active** state never receives its inbox — messages pile up unnotified
(`muxcode diagnose <role>` reports this as `active-with-stale-messages`).

- **Never run a blocking / never-exiting command in an agent's interactive pane.**
  `gh pr checks --watch`, `tail -f`, log follows, and interactive watchers keep
  the agent active indefinitely, starving its inbox. Route watch-to-completion
  work to the **watch** agent (log tailing) or a detached `muxcode proc`:
  - Avoid (in an agent pane): `gh pr checks 188 --watch`
  - Prefer: `muxcode proc start "gh pr checks 188 --watch" --name pr188-checks`
- **Recovering a wedged-active agent.** Recovery depends on whether the agent is
  *busy* or genuinely *frozen*:
  - **Busy** (will finish): it returns to idle on its own; the daemon then delivers.
  - **Frozen TUI** (`Escape`, `Ctrl-U`, **and** `Ctrl-C` all ignored, static
    display): keystroke- and marker-based recovery all **fail**. `muxcode
    agent-health --stop`/`--start` only toggles a marker (never touches the
    process); `muxcode reload` / `GracefulStop()` recover via `tmux send-keys`
    (Escape → `/exit` → C-c) which don't land on a frozen pane. A frozen-but-alive
    process also passes `IsAgentAlive`, so the daemon's auto-restart (it fires only
    on a **dead** process) never triggers.
  - **Reliable fix — kill the OS process.** Find the agent PID (`claude --agent
    <file>` under the role's pane) and `kill -TERM <pid>` (escalate to `-KILL` if
    it survives). The now-dead process triggers daemon auto-restart; the fresh
    agent re-reads its on-disk inbox on startup, so the pending message is not lost.
    Beware the "1 shell still running" indicator — it can be **stale** (no real
    child process), so don't waste time hunting a child to kill; terminate the
    agent CLI process itself.
  - **After respawn, confirm the inbox was drained.** A freshly respawned agent may
    sit idle without running its startup inbox check (the daemon notify doesn't
    always fire — the "post-restart wake gap"). If the pending message isn't picked
    up, wake it manually so it re-reads its inbox.

> There is currently no single `muxcode` command that force-terminates a
> hung-but-alive agent, and a respawned agent isn't guaranteed to drain its inbox —
> every stop/restart path is keystroke- or marker-based and the startup wake is
> unreliable. An automatic watchdog (detect freeze → kill → respawn → verify inbox
> drained, with no user intervention) is tracked in
> `docs/requirements/backlog/MUX-010-delegation-message-hygiene.md`.

### `muxcode inbox`

Read messages from an agent's inbox.

```bash
muxcode inbox [--peek] [--raw] [--role ROLE] [--poll] [--loop]
```

- Default mode: consume messages and format as actionable prompts with reply commands
- `--peek` — non-destructive preview (does not consume messages)
- `--raw` — dump raw JSONL
- `--role ROLE` — read a specific role's inbox (defaults to own role)
- `--poll` — block until a message arrives (watches `trigger-{role}.notify` mtime + inbox), then consume and return. Sets a `polling-{role}.marker` that suppresses redundant daemon send-keys wake-ups while the poll is active — the agent pulls its own inbox instead of being pushed to.
- `--loop` — with `--poll`, re-arm the poll after each return. This is the **self-poll listener** each agent runs as a background job under the [delivery-acknowledgement](architecture.md#delivery-tracking) model; for Claude Code agents the [Stop hook](hooks.md#hook-stop-self-poll-re-launch) re-launches it if it dies.

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

Run the bus daemon (unified background supervisor).

```bash
muxcode watch [session] [--poll N] [--debounce N] [--monitor]
```

- Polls agent inboxes and notifies agents via `tmux display-message` (passive status bar flash) when new messages arrive
- Monitors the analyze trigger file and routes file-edit events to relevant agents based on file patterns
- `--poll N` — inbox polling interval in seconds (default: 2)
- `--debounce N` — trigger file debounce interval in seconds (default: 8)
- `--monitor` — run as daemon health monitor instead of the daemon itself. Checks the daemon keepalive every 15 seconds; if stale (>30s), kills and relaunches the daemon process. Exits cleanly when the tmux session is gone.

Without `--monitor`, runs in a split-left window's left pane as the primary daemon. With `--monitor`, runs as a companion background process launched by `LaunchSession()`.

#### Trigger file format

The trigger file (`/tmp/muxcode-analyze-{SESSION}.trigger`) is written by `muxcode hook analyze` with one line per file edit:

```
<unix-timestamp> <filepath>
```

When the daemon detects a change in the trigger file, it starts debouncing. After the debounce interval elapses with no further changes, the daemon:

1. Reads the trigger file and collects unique file paths
2. Sends an aggregate `analyze` event to the analyst agent with all edited files
3. Truncates the trigger file

Per-file routing to specific agents (test/deploy/build) is handled earlier by `hook analyze` at edit time — the daemon only handles the aggregate analyst notification.

### `muxcode upgrade-daemons`

Restart all running session daemons so they pick up the freshly installed binary.

```bash
muxcode upgrade-daemons [--dry-run]
```

- Long-lived daemons keep executing the code loaded at their launch — a `make install` that fixes daemon behavior does not reach already-running sessions until they cycle. This command discovers every running `muxcode watch` daemon and monitor process (across all sessions on the machine via `ps`) and re-launches each from the binary currently on `PATH`.
- Per session, the **monitor is killed first** (so it cannot resurrect the old daemon mid-cycle), then the daemon, then both are relaunched. Kills use `SIGTERM` with a 2s grace period, escalating to `SIGKILL`.
- **Orphan cleanup**: daemons whose tmux session no longer exists are killed without relaunch (logged as `daemon-orphan-killed`). Successful upgrades log `daemon-upgraded`.
- `--dry-run` (`-n`) — list the daemons that would be restarted (and orphans that would be killed) without touching any process.
- Exits non-zero if any session's relaunch fails. Prints `no running daemons found` when none are discovered.

`build.sh` calls `muxcode upgrade-daemons` after `make install`, so every install automatically rolls the new binary out to all live sessions.

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

Runs in the `status` window (F9). Hotkeys: `r` opens the Remote session browser TUI, `q` closes the dashboard.

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

### `muxcode deliver`

Force-deliver an agent's pending inbox messages into its pane, bypassing the daemon's idle detection.

```bash
muxcode deliver <role>                      # requires the pane to show an idle prompt (wide 200-line capture)
muxcode deliver <role> --force              # skip the idle check and inject regardless of pane state
muxcode deliver <role> --session <name>     # target a different session (e.g. a subsession) from anywhere
```

Use this when an agent has pending inbox messages it never processed — the recovery for `active-with-stale-messages`, `stale-notified-ids`, `missed-send-keys`, and `pending-input-blocking` findings from `muxcode diagnose`.

Behavior:

- Resolves hosted roles to their host pane (`docs`/`research`/`pr-read` → host window).
- Delivers via the robust wake-up path (text → delay → Enter → verify for hook providers, `SendWakeUp()` for non-hook providers) — avoids the dropped-Enter failure of a single `tmux send-keys "text" Enter` write.
- With `--force`, if all messages are already marked notified but still unconsumed (a prior send-keys was dropped), the notified markers are cleared so they re-deliver.
- Clears stale parked input in an unfocused pane before injecting.
- On send failure, notified markers are rolled back so a later attempt can retry.

**Prefer `muxcode deliver` over manual `tmux send-keys` wake-ups** — manual text+Enter in one pty write is the known dropped-Enter pitfall.

### `muxcode delivery-ack`

Roll the **receipt-based delivery cutover** ([delivery-acknowledgement](architecture.md#delivery-tracking)) — now the **default** — on or off at runtime, no daemon restart.

```bash
muxcode delivery-ack status                 # report current runtime + env state (default)
muxcode delivery-ack on                      # restore the default (clears the rollback marker)
muxcode delivery-ack off                     # roll back to the pane-scrape delivery path (writes the marker)
muxcode delivery-ack <sub> --session <name>  # target a different session
```

The cutover is on by default. `off` writes a **`delivery-ack.off` rollback marker** in the bus dir; `on` clears it. The daemon's `ackDeliveryActive()` re-reads the marker every poll loop, so the rollback (and its removal) takes effect **immediately** — unlike the startup-only `MUXCODE_DELIVERY_ACK` env var, which the daemon only reads at its own process start. This is the fast, restart-free operational rollback valve for a live session.

Resolution order for whether the cutover is active:

1. `MUXCODE_DELIVERY_ACK_DISABLE` env set → **OFF** (hard kill switch, forces the old path — overrides everything below; needs a daemon restart).
2. `MUXCODE_DELIVERY_ACK=off|0|false|no` env → OFF; `=on|1|true|yes` pins it ON (evaluated at daemon startup).
3. Runtime OFF marker (`muxcode delivery-ack off`) → OFF (no restart needed).
4. Otherwise → **ON** (default).

`status` prints the effective state (noting "default: ON"), the rollback-marker path (present/absent), and any relevant env. Physical removal of the bypassed pane-scrape machinery stays deferred behind the known receipt-gap-backstop limitation — see [`remove-gated-pane-scrape-delivery`](requirements/backlog/MUX-012-remove-gated-pane-scrape-delivery.md).

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

**Daemon integration:** The bus daemon (`muxcode watch`) checks for due cron entries on each poll cycle. It reloads the cron file from disk at most every 10 seconds to avoid excessive filesystem reads. When a cron entry fires, the daemon sends a bus message to the target agent, updates `last_run_ts`, appends to execution history, and notifies the target via tmux.

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

**Daemon integration:** The bus daemon checks for loops every 60 seconds. When a loop is detected, it sends a `loop-detected` event to the edit agent and notifies via tmux. Alerts are deduplicated within a 10-minute cooldown (exceeds the 5-minute detection window to prevent self-sustaining alerts). System actions (`loop-detected`, `compact-recommended`, `proc-complete`, `spawn-complete`) are excluded from message loop detection.

#### Watcher event: `compact-recommended`

The daemon monitors agent context size (memory + history + log files) and staleness (time since last compaction) every 120 seconds. When **both** conditions are met — total tracked size > 512 KB **and** time since last compact > 2 hours — the daemon sends a `compact-recommended` event to the role itself with an actionable message:

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

**Daemon integration:** The bus daemon checks running processes on each poll cycle (2s). When a process completes, it sends a `proc-complete` event to the owner agent with the command, status, and exit code. The owner is notified via tmux.

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
4. Launches `muxcode agent launch research` with `AGENT_ROLE=spawn-a1b2c3d4` — the base role (`research`) determines agent definition, tools, and prompts; the `AGENT_ROLE` env var (`spawn-a1b2c3d4`) determines the bus communication channel
5. After 2s delay, notifies the spawn agent to read its inbox
6. When the agent finishes and exits (tmux window closes), the daemon detects it and sends a `spawn-complete` event to the owner

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

**Daemon integration:** The bus daemon checks spawned agent windows on each poll cycle (2s). When a spawn's tmux window no longer exists, it marks the spawn as `completed`, extracts the last result message from `log.jsonl`, and sends a `spawn-complete` event to the owner agent with the result summary.

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
| `prompt` | Output formatted context for prompt injection (used by `RunAgentLaunch()`) |
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
- `--model MODEL` — Ollama model name (default: `MUXCODE_OLLAMA_MODEL` or `qwen3:4b`)
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

Launch an AI CLI agent for a role. Resolves agent file, model, tools, prompt, and execs the agent CLI.

```bash
muxcode agent launch <role>
```

- `<role>` — agent role to launch (e.g. `edit`, `build`, `test`, `commit`)

**Resolution cascade:**

1. **Config loading** — reads shell-sourceable config from `MUXCODE_CONFIG` > `.muxcode/config` > `~/.config/muxcode/config`
2. **CLI selection** — checks `MUXCODE_{ROLE}_CLI` env var; if `"local"`, routes to Ollama harness
3. **Agent file** — 3-tier search: `.claude/agents/<name>.md` > `~/.config/muxcode/agents/<name>.md` > install dir defaults
4. **Model** — per-role env (`MUXCODE_{ROLE}_CLAUDE_MODEL`) > global env (`MUXCODE_CLAUDE_MODEL`) > role default (opus for edit/review, sonnet for others)
5. **Tools** — resolves from tool profiles in `muxcode.json`
6. **Prompt** — assembles shared coordination prompt + skills + context.d + session resume
7. **Venv** — activates Python venv if found (`MUXCODE_VENV_DIR` > `.venv` > `venv`)
8. **Exec** — replaces process with `claude` (or `muxcode-llm-harness` for local LLM)

**Pre-launch actions:**

- Sends startup inbox message for `edit` role (context restoration). The analyze role also receives one when enabled via `MUXCODE_WINDOWS`.
- Logs agent launch to persistent lifecycle log

**Examples:**
```bash
# Launch the build agent (standard usage from LaunchSession)
$ muxcode agent launch build

# Launch the edit agent (prompted mode, opus model)
$ muxcode agent launch edit
```

### `muxcode agent status`

Show the autonomous agent's current status — story, phase, heartbeat, and session statistics.

```bash
muxcode agent status
```

Reads state files (`agent-current-story`, `agent-phase`, `agent-stories-done`, `agent-last-heartbeat`) and prints a summary to stdout.

**Example:**
```
$ muxcode agent status
Story: PROJ-123 Implement user authentication
Phase: implementation (iteration 3/10)
Stories: 2 done, 1 in progress
Uptime: 1h 23m | Last heartbeat: 2m ago
```

Core code: `cmd/agent.go`, `bus/console.go` (`ReadAutonomousAgentStatus()`, `FormatAutonomousAgentStatus()`).

### `muxcode mode`

Manage agent mode cycling — swap between agents on a shared window while preserving all sessions. Supports both the edit window (F2) and plan window (F1).

```bash
muxcode mode cycle [--window NAME]
muxcode mode status [--window NAME]
muxcode mode switch <mode> [--window NAME]
muxcode mode list [--window NAME]
muxcode mode active [--window NAME]
```

| Subcommand | Description |
|------------|-------------|
| `cycle` | Cycle to the next registered agent (wraps around) |
| `status` | Show current agent, cycle index, registered agents |
| `switch <mode>` | Jump directly to a specific agent by mode name |
| `list` | List all registered agents with current indicator |
| `active` | Print the currently active role name for a window |

All subcommands accept `--window <name>` to target a different window (default: `edit`).

**Cycle mechanism**: uses `swap-window` to exchange the visible window with the target agent's holding window. All processes (nvim, Claude Code agents, console viewer) continue running — only visibility changes.

**State file**: `mode-cycle-{window}.json` in the bus directory. Per-window state — `mode-cycle-edit.json` for F2, `mode-cycle-plan.json` for F1.

**Keybindings**:

| Key | Action |
|-----|--------|
| `F2` | Cycle when on edit window, switch to edit window otherwise |
| `prefix + a` | Cycle edit-window agents regardless of current window |
| `F1` | Cycle when on plan window, switch to plan window otherwise |
| `prefix + r` | Cycle plan-window agents regardless of current window |

**Examples:**
```bash
# Cycle to next agent on edit window (edit → agent → edit)
$ muxcode mode cycle
Cycled to: agent (index 1)

# Cycle plan window agents (plan → research → plan)
$ muxcode mode cycle --window plan
Cycled to: research (index 1)

# Check current mode
$ muxcode mode status
Window: edit  Current: agent (index 1/2)
  [0] edit (default)
  [1] agent ← active

# Get active role for a window (useful for routing)
$ muxcode mode active --window edit
edit

$ muxcode mode active --window plan
research

# Switch directly to edit mode
$ muxcode mode switch edit
Switched to: edit (index 0)

# List all registered agents
$ muxcode mode list
  [0] edit (default)
  [1] agent
```

Core code: `bus/mode.go` (cycle state, pane swap, `ActiveModeRole()`), `cmd/mode.go` (CLI handler).

### `muxcode subscribe`

Manage event subscriptions for fan-out after chain execution.

```bash
muxcode subscribe add <event> <outcome> <notify-role> <action> [message-template] [--conditions JSON]
muxcode subscribe list [--all]
muxcode subscribe remove <id>
muxcode subscribe enable <id>
muxcode subscribe disable <id>
```

**Subcommands:**

| Subcommand | Description |
|------------|-------------|
| `add` | Create a new subscription (with optional conditions) |
| `list` | Show enabled subscriptions (use `--all` to include disabled) |
| `remove` | Delete a subscription by ID |
| `enable` | Enable a disabled subscription |
| `disable` | Disable a subscription without removing it |

- `<event>` — event to match: `build`, `test`, `deploy`, `run`, `watch`, or `*` (wildcard)
- `<outcome>` — outcome to match: `success`, `failure`, or `*` (wildcard)
- `<notify-role>` — role to notify when matched
- `<action>` — action name for the sent message
- `[message-template]` — optional template with `${event}`, `${outcome}`, `${exit_code}`, `${command}`, `${branch}`, `${changed_files}` (default: `"${event} ${outcome}: ${command}"`)
- `--conditions JSON` — optional JSON object with condition expressions (same types as chain conditions: `files_match`, `branch_match`, `env_set`, `env_equals`, `output_contains`, `exit_code`, etc.). All conditions must pass for the subscription to fire.

**Examples:**
```bash
# Notify watch agent on any build failure
$ muxcode subscribe add build failure watch alert "Build failed: ${command}"

# Notify analyst on all events
$ muxcode subscribe add "*" "*" analyze observe

# Only fire on release branches
$ muxcode subscribe add build success deploy deploy "Deploy ${branch}" --conditions '{"branch_match": "^release/"}'

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

The daemon probes agent panes every 30 seconds. Three consecutive failures trigger auto-restart (capped at 3 per role per session). Excluded roles: `edit`, `webhook`, `spawn-*`.

**Data files:**

| File | Location | Purpose |
|------|----------|---------|
| `{role}.stopped` | `/tmp/muxcode-bus-{session}/lock/` | Marker suppressing auto-restart |
| `watcher.keepalive` | `/tmp/muxcode-bus-{session}/` | Unix timestamp updated each daemon poll loop |

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
| `source` | Origin: `launcher`, `daemon`, `monitor`, `auto-accept`, `agent`, `init`, `cleanup` |
| `session` | Session name |
| `event` | Machine-readable event type (see catalog below) |
| `pid` | Process ID (optional) |
| `detail` | Human-readable context (optional) |

**Event catalog:**

| Source | Event | Level | When |
|--------|-------|-------|------|
| launcher | session-start | info | `LaunchSession()` begins |
| launcher | bus-init | info | `muxcode init` completes |
| launcher | stale-kill | info | Killed stale daemon or monitor |
| launcher | watcher-start | info | Daemon process launched (with PID) |
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
| daemon | started | info | Daemon `Run()` begins (with PID) |
| daemon | lock-failed | error | Another daemon already running |
| daemon | inbox-notify | info | Agent notified of new messages |
| daemon | startup-notify | info | First-idle notification delivery |
| daemon | trigger-route | info | Edit events routed to analyst |
| daemon | cron-fire | info | Cron entry executed |
| daemon | proc-complete | info | Background process finished |
| daemon | spawn-complete | info | Spawned agent finished |
| daemon | loop-detected | warn | Loop alert sent |
| daemon | compact-alert | warn | Compaction recommended |
| daemon | ollama-probe-fail | warn | Ollama health check failed |
| daemon | ollama-restart | warn | Ollama restart attempted |
| daemon | ollama-recovered | info | Ollama back online |
| daemon | agent-health-fail | warn | Agent health check failed |
| daemon | agent-restart | warn | Agent restart attempted |
| daemon | agent-recovered | info | Agent came back |
| monitor | session-gone | info | tmux session gone, monitor exiting |
| monitor | stale-detected | warn | Daemon keepalive stale |
| monitor | watcher-restart | info | Daemon restarted by monitor |
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
2026-03-12 08:30:16  info   daemon         startup-notify          edit

# Filter by source and level
$ muxcode lifecycle show --source daemon --level warn --since 1h

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
| `jira-manage-issues` | commit, edit | Full Jira issue lifecycle. Read (with links/subtasks), update descriptions (ADF), search (JQL), transition status, link dependencies, read/post comments, create subtasks. Uses `muxcode atlassian jira read/update/comment/comments/link/link-types/transitions/transition/search/create-subtask`. Requires `JIRA_BASE_URL`, `JIRA_USER_EMAIL`, and `JIRA_API_TOKEN` in config. |
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

### `muxcode chain`

Resolve and fire event chain actions. Used by `hook bash` internally and available as a standalone CLI for testing and manual chain triggers.

```bash
muxcode chain <event_type> <outcome> [flags]
```

**Arguments:**

- `<event_type>` — event to resolve: `build`, `test`, `deploy`, `run`, `watch`
- `<outcome>` — outcome to match: `success`, `failure`, `unknown`

**Flags:**

| Flag | Description |
|------|-------------|
| `--exit-code N` | Override exit code in chain context |
| `--command CMD` | Override command in chain context |
| `--files F` | Override changed files (comma-separated) for condition evaluation |
| `--branch B` | Override branch name for condition evaluation |
| `--output O` | Override command output for condition evaluation |
| `--verbose` | Show per-condition PASS/FAIL results |
| `--dry-run` | Resolve chain without sending messages |
| `--no-notify` | Skip analyst notification |

**Exit codes:** 0 = sent, 1 = error, 2 = no chain configured

**Examples:**
```bash
# Test what chain action would fire for build success on a release branch
muxcode chain build success --branch release/v2 --dry-run --verbose

# Manually trigger test chain with file context
muxcode chain build success --files "src/main.go,src/util.go"

# Check deploy chain resolution
muxcode chain deploy success --verbose
```

### `muxcode graph`

Declarative DAG orchestration over the bus (MUX-014). A graph is JSON: typed nodes, edges
keyed by outcome. The daemon executes edges — no LLM decides node succession. See
[Architecture](architecture.md#graph-orchestration-control-plane).

| Subcommand | Purpose |
|------------|---------|
| `run <template>\|--file <path> [intent]` | Start a run; returns immediately with the run id |
| `validate <file\|template>` | Structural validation — exits non-zero on errors |
| `list` | List resolvable templates across all three tiers |
| `status [--json] [run-id]` | Per-node state, timestamps, outcome; no id lists all runs |
| `cancel <run-id>` | Stop scheduling; in-flight node work completes or times out |
| `retry <run-id> --from <node>` | Re-execute from a node, keeping upstream results |
| `approve <run-id> <node>` | Release a `wait_human` gate |
| `ui [run-id] [--render-once] [--width N]` | Interactive run browser → layered DAG → node detail ([MUX-031](requirements/completed/MUX-031-graph-run-tui.md)) |
| `ui --templates` | Template launcher — pick, validate, and start a run |
| `ui --gates [--render-once]` | Pending `wait_human` approval queue across all in-flight runs |

```bash
# Start a run from a built-in template, with intent interpolated into node messages
muxcode graph run req-code-pr "implement PBP1-4915"

# Run a custom definition — --file must be the first argument after `run`,
# since a bare first arg is read as a template name
muxcode graph run --file my-dag.json "implement PBP1-4915"

# Watch it progress (Dracula-colored node grid), or script against it
muxcode graph status <run-id>
muxcode graph status --json <run-id>

# Validate before running — catches undefined refs, unreachable nodes, uncapped cycles
muxcode graph validate my-dag.json

# Release a human gate, or abandon the run
muxcode graph approve <run-id> await-review
muxcode graph cancel <run-id>

# Re-run from a failed node without redoing completed upstream work
muxcode graph retry <run-id> --from test

# Interactive surfaces (MUX-031)
muxcode graph ui                          # run browser; Enter opens a run's DAG, then a node
muxcode graph ui <run-id>                 # straight into one run's DAG view
muxcode graph ui --templates              # pick a template, validated before it starts a run
muxcode graph ui --gates                  # every wait_human gate awaiting approval

# Scriptable single frames — no terminal required, stable width for diffing
muxcode graph ui --render-once --width 100 <run-id>
muxcode graph ui --gates --render-once
```

Keys in the interactive views: `j`/`k` move, `Enter` descends, `q` goes back (quits from the
top level), `R` forces a refresh, and `a` / `c` / `r` approve a gate, cancel the run, or retry
from the selected node — each behind a confirm prompt.

`Tab` cycles the three top-level surfaces in place — **Graph Runs → Pending Gates → Launch Graph**
— and `Shift-Tab` cycles back, so switching modes never means closing the popup and reopening the
menu. All three menu entries open the same TUI and differ only in where the cycle starts. A tab bar
in the header shows the active surface; drill-ins (DAG, node detail, intent prompt) and open
confirm prompts leave `Tab` inert, so it can never yank you out of a half-answered prompt. Each
surface remembers its own selection across a cycle, restored by item id rather than row index, so
a list that changed underneath falls back to the first row instead of pointing at the wrong thing.

Three details worth knowing:

- **`--templates` has no `--render-once` form** and exits non-zero if you combine them; the
  launcher is interactive by definition. `--gates --render-once` *is* supported.
- **Approving from the TUI is the human approval the gate exists to collect.** It calls the same
  `bus.ApproveGraphGate` the `approve` subcommand does — there is no bus-message path into it, so
  no agent can reach it. A confirm that would release a git or Atlassian mutation says so
  explicitly before it accepts the keypress.
- **Views re-read the run store on a 2s tick** and hold no daemon connection, so a graph TUI left
  open costs nothing and survives a daemon restart.

**Templates** resolve `project > user > builtin`, the same precedence as agent files:
`.muxcode/graphs/<name>.json` > `~/.config/muxcode/graphs/<name>.json` > built-in.

**Run `muxcode graph list` for the built-in set** — it prints each name with its description, and
is the authority. This page deliberately does not enumerate them: the list changed three times in
one afternoon (2026-08-27) and went stale within minutes on each occasion, twice while being
actively maintained. A hand-copied set is a standing liability, and the command that answers the
question correctly already exists.

Broadly they follow a story's life — derive a spec from the branch, implement it, review, commit and
PR, deploy — plus a `build-test-review` subgraph the others compose. Every builtin is pinned by
`TestBuiltinGraphTemplatesValidate`, so one violating the gate rule fails the suite rather than
shipping.

**Validation is strict by design.** Undefined node refs, unreachable nodes, and uncapped
cycles are errors, not warnings — a loop is only legal via an explicit `max_iterations` on a
loop edge. A node that commits or writes to Jira/Confluence is rejected unless it sits
downstream of a `wait_human` gate.

**Run state** lives under `/tmp/muxcode-bus-{session}/graphs/<run-id>/` — `run.json`,
`graph.json`, and `nodes/<id>.json` per node, written atomically. Because every transition
is persisted, a daemon restart resumes in-flight runs with no separate recovery step.

### `muxcode hook`

Hook handlers for Claude Code's PreToolUse and PostToolUse events. Each subcommand reads the tool event as JSON on stdin.

```bash
muxcode hook guard       # PreToolUse: edit agent command guard
muxcode hook bash        # PostToolUse: build/test/deploy chain triggers
muxcode hook analyze     # PostToolUse: file-edit trigger writer
muxcode hook inbox-poll  # PreToolUse: inbox check on tool execution
```

- `guard` — blocks prohibited commands for the edit agent (build, test, git, deploy, curl). Returns JSON `{"decision":"block","reason":"..."}` or passes through.
- `bash` — detects build, test, deploy, run, watch, and git commands from exit codes and command text. Drives the build→test→review and deploy→run→watch chains via `ResolveChain()` with `ChainContext` (conditions, action arrays). Transitions the workflow state machine. Logs command history with error extraction.
- `analyze` — writes file-edit events to the analyze trigger file for daemon debounce. Transitions workflow to `editing`.
- `inbox-poll` — checks the agent's inbox on each tool execution and injects a "You have new messages" notification if messages are pending.

**Chain dedup:** Chain messages use `SendNoCCIfNotDuplicate()` with atomic file locking to prevent duplicate chain triggers within the 30-second dedup window.

**Workflow state guard:** Before firing chains, `triggerChain` checks the current workflow state — if state is already `reviewing`/`reviewed`, test success chain is skipped; if `testing`/`reviewing`/`reviewed`, build success chain is skipped. This prevents the test→review loop where review responses re-triggered the test chain.

Core code: `bus/hook.go` (library), `cmd/hook.go` (CLI dispatcher).

### `muxcode console`

Run a left-pane log console for an agent window.

```bash
muxcode console <role> [--interval N] [--once]
```

- `<role>` — the window name (build, test, review, deploy, run, commit, analyze, watch) or modal role (api)
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

**Write authority:** mutating subcommands (`jira update`, `comment`, `link`, `transition`, `create-subtask`, `confluence update`, …) are restricted to the roles in `MUXCODE_ATLASSIAN_AUTHORITY_ROLES` (default `edit`) and refused with `DENIED:` for anyone else — checked before credentials load, so an unauthorized call fails on the rule rather than on a missing token. Read subcommands (`read`, `comments`, `search`, `link-types`, `transitions`) are open to every role, and a call from the user's own shell (no agent role) is never gated. See [Atlassian write authority](architecture.md#atlassian-write-authority).

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

**Self-documenting banner:** when anything is redacted, an in-band notice (`PIIScrubNotice`) is prepended to the output so the agent doesn't mistake redacted placeholders for real data or compute lengths/sizes/counts over masked text. This makes the redaction explicit in-conversation rather than silent. Backed by `ScrubPIIWithNotice()` (`bus/scrub.go` + `harness/scrub.go`), wired into `cmd/scrub.go` (CLI) and `harness/executor.go` (harness agents).

Patterns: emails, SSN, credit cards (prefix-anchored), phone numbers (separator-required), AWS access keys, AWS secret keys, JWTs, generic secrets/tokens, dates of birth.

Core code: `bus/scrub.go` (patterns + `ScrubPII()`, `ScrubPIIWithNotice()`, `PIIScrubNotice`), `cmd/scrub.go` (CLI handler).

### `muxcode track`

Show the delivery status of a message by ID.

```bash
muxcode track <msg-id>
```

Reads the delivery status file for the given message ID and prints its lifecycle state. Delivery tracking is automatically managed by `Send()` (creates "sent" status), `Receive()` (marks "delivered"), and reply messages with `--reply-to` (marks original as "responded").

**Output example:**

```
1711324800-edit-a1b2c3d4  delivered  (delivered in 3s)
```

Status values: `sent`, `delivered`, `responded`, `expired`.

Core code: `cmd/track.go`, `bus/delivery.go`.

### `muxcode branch-time`

Per-branch active working time, accumulated by the daemon sampler and read/reconciled via the CLI. See [Configuration → Branch time tracking](configuration.md#branch-time-tracking) for the env vars that control accumulation.

```bash
muxcode branch-time show [--branch <b>] [--json]   # one branch (default: current)
muxcode branch-time --all [--json]                 # all tracked branches for this repo
muxcode branch-time --status                       # tmux status bar snippet
muxcode branch-time --trailer                      # commit trailer line
muxcode branch-time seed --secs <n> [--branch <b>] # floor-reseed the ledger (never lowers)
muxcode branch-time record --secs <n> [--branch <b>] # mark <n> seconds as recorded (staleness watermark)
muxcode branch-time log-jira [--dry-run]           # Jira worklog with watermark
```

**`--json` output** — the machine-readable read path the plan agent consumes during `verify-spec`:

| Field | Meaning |
|-------|---------|
| `repoKey` | Stable repo identity (origin URL or toplevel path) |
| `branch` | Branch name |
| `seconds` / `formatted` | Cumulative active time (absolute total) |
| `unrecordedSeconds` / `lastRecordedSeconds` / `lastRecordedAt` | Staleness watermark set by `record` — how much has accrued since the last doc write |
| `lastJiraLoggedSeconds` | Jira worklog watermark (`log-jira`) |
| `updated` | Last accumulation timestamp |
| `current` / `ignored` | Whether this is the checked-out branch / on the ignore list |

A fresh or unknown branch returns `seconds: 0` rather than an error, so the plan agent's read never fails on a new branch.

**Recording flow (verify-spec)**: when the build→test→review chain succeeds with an active spec set, the plan agent reads the ledger via `--json` and upserts a `## Time Tracking` row in that spec — keyed by branch, replaced in place, **absolute totals, never deltas** (which is what makes re-recording idempotent). **Never-regress reconciliation**: if the ledger reads lower than the doc row (lost or reset store), the doc's larger value is kept and the ledger is re-seeded from it via `seed` — a floor that only ever raises. Do not use `--add` for reconciliation: it is additive and double-counts whenever the ledger is not exactly zero. **Degrade-quietly**: branches with no active spec, and repos without `docs/requirements/`, accumulate time but write nothing.

Core code: `cmd/branchtime.go`, `bus/timetrack.go` (ledger at `~/.config/muxcode/branch-time.json`), `daemon/daemon.go` (`checkBranchTime()` sampler, `notifyPlanOnReview()` recording instruction). Integration tests: `scripts/test-branch-time.sh` (accumulator/CLI), `scripts/test-branch-time-recording.sh` (recording sink — JSON shape, idempotent upsert, never-regress).

### `muxcode compact`

Trigger conversation compression for an agent or all agents.

```bash
muxcode compact [--all] [role]
```

Polls the agent's tmux pane for idle state (detects `❯` prompt) every second for up to 30 seconds. Once idle, clears residual input and injects `/compact` via `tmux send-keys` to trigger Claude Code's built-in conversation compression. If the agent doesn't become idle within the timeout, exits silently.

- `--all` — compact all active agents (skips hosted roles, stopped agents, dead agents)
- `role` — target role (defaults to `AGENT_ROLE` env var)

With `--all`, iterates over all compactable roles (excludes hosted roles like `docs`/`research`/`pr-read`, modal roles like `api`/`webhook`, stopped agents, and dead agents). Each agent is compacted sequentially. Progress is printed to stderr.

This is a fire-and-forget command — run it in the background after saving context via `muxcode session compact "<summary>"`.

Core code: `cmd/compact.go`, `bus/compact.go` (`CompactableRoles`).

### `muxcode reload`

Stop one or more agents, reconfigure, and relaunch (hot reload).

```bash
# Single agent
muxcode reload <role> [--cli <cli>] [--model <model>] [--compact]

# Multiple agents
muxcode reload <role1> <role2> ... [--cli <cli>] [--model <model>] [--compact]

# All agents (with optional overrides and provider filter)
muxcode reload --all [--cli <cli>] [--model <model>] [--compact] [--provider <cli>]
```

| Flag | Description |
|------|-------------|
| `--cli <cli>` | CLI provider override (claude, opencode, codex, local) |
| `--model <model>` | Model override (e.g. opencode-go/deepseek-v4-pro) |
| `--compact` | Compact agent context before stopping |
| `--all` | Reload all active agents sequentially (3s gap between agents) |
| `--provider <cli>` | Filter `--all` to only agents currently on the specified CLI (requires `--all`) |

**Single agent**: writes runtime override to `/tmp/muxcode-bus-{session}/config/{role}.env`, gracefully stops the agent (C-c, poll for exit, force-kill after 6s), regenerates provider config, relaunches via `muxcode agent launch`, and verifies liveness (15s timeout). Reload marker suppresses daemon health checks during the cycle.

**Multi-role**: accepts multiple positional role arguments. Reloads agents sequentially via `ReloadBatch()` with a 3s gap between each. Per-agent results are printed as each completes, with a summary line at the end. Failure of one agent does not abort the batch.

**`--all` with overrides**: `--all` now supports `--cli` and `--model` flags — applies the same provider/model override to every active agent. Combined with `--provider`, only agents currently running on the specified CLI are reloaded (others are skipped).

Examples:

```bash
# Switch 3 agents to OpenCode
muxcode reload build test review --cli opencode --model opencode-go/minimax-m2.5

# Switch ALL agents to OpenCode
muxcode reload --all --cli opencode --model opencode-go/minimax-m2.5

# Switch only Claude agents to OpenCode (leaves OpenCode agents untouched)
muxcode reload --all --provider claude --cli opencode --model opencode-go/minimax-m2.5
```

Core code: `bus/reload.go`, `bus/reload_batch.go`, `cmd/reload.go`.

### `muxcode config`

View or change agent CLI/model configuration.

```bash
muxcode config set <role>.<field> <value> [--reload]
muxcode config get <role>
muxcode config list
```

| Subcommand | Description |
|------------|-------------|
| `set` | Write a config value to the persistent config file. Fields: `cli`, `model`. |
| `get` | Show effective CLI, model, and resolution source for a role |
| `list` | Show all roles with their effective CLI and model |

The `--reload` flag on `set` triggers an immediate agent reload after writing the config.

Core code: `bus/config_file.go` (`SetShellConfigValue`, `ResolveConfigPath`), `bus/launch.go` (`EffectiveConfig`), `cmd/config.go`.

### `muxcode provider-select`

Interactive provider/model selector TUI (used by the provider modal).

```bash
muxcode provider-select [--role <role>]
```

Launched via `muxcode modal open provider` (keybinding: `prefix + R`). Presents an interactive TUI with provider and model selection, plus an **Agents section** for multi-agent bulk reload. Select a target provider/model, then check which agents to switch. Shortcuts: `a` (select all, excludes edit/auto), `p` (select by current provider), `n` (deselect all). On confirm with >1 agent, transitions to a live progress view showing per-agent reload status. Single-agent selection preserves the existing workflow.

Core code: `tui/provider_select.go`, `bus/provider_options.go`, `bus/reload_batch.go`, `cmd/provider_select.go`.

### `muxcode remote`

Cross-session investigation — inspect other muxcode sessions running on the same machine without affecting them.

```bash
muxcode remote                                  # Launch interactive session browser TUI
muxcode remote list                             # List all muxcode sessions
muxcode remote status <session>                 # Agent status overview
muxcode remote capture <session> <role> [N]     # Capture last N lines from agent pane (default: 30)
muxcode remote inbox <session> [role]           # Peek at agent inbox(es)
muxcode remote log <session> <role> [N]         # Show last N messages for a role (default: 20)
muxcode remote diagnose <session> <role>        # Run diagnostics on a remote agent
muxcode remote diagnose <session> --all         # Diagnose all agents in a session
```

| Subcommand | Description |
|------------|-------------|
| _(no args)_ | Launch the interactive session browser TUI. Three views: session list → session detail (agent table) → content view (capture/inbox/diagnose). Also accessible via `r` hotkey from the dashboard. Auto-refreshes every 10s. See keyboard shortcuts below. |
| `list` (alias: `ls`) | Discover all sessions via `/tmp/muxcode-bus-*` dirs. Shows session name, tmux liveness, agent count, log size, and project directory. Current session marked with `*`. |
| `status` | Combined agent table (role, provider, state, health, inbox count, last activity) plus pending inbox summaries. Reuses `GetAllAgentStatus()` with the remote session name. |
| `capture` (alias: `cap`) | Captures the last N lines from a remote agent's tmux pane. Requires a live tmux session. Reports whether the agent is idle (shows `❯` prompt) or active. |
| `inbox` | Peeks at inbox messages without consuming them. If role is omitted, shows all roles with non-empty inboxes. Each message shows ID, sender, type, action, age, and payload preview (truncated to 80 chars). |
| `log` | Shows the last N messages from the bus log for a specific role. Reuses `ReadLogHistory()`. |
| `diagnose` | Runs the full diagnostic pipeline (`CollectEvidence()` + `RunDiagnostics()`) against a remote agent. Same 10 failure mode patterns as local `muxcode diagnose`. Supports `--json` output and `--all` for a summary table of all agents. Exits non-zero on critical findings. |

**Session name resolution**: supports prefix matching — `muxcode remote status mux` resolves to `muxcode` if it's the only session with that prefix. Ambiguous prefixes produce an error listing all matches. Exact matches are preferred over prefix matches.

**Read-only**: all remote operations are non-destructive — they never modify the target session's bus state, inbox files, or agent processes.

**TUI keyboard shortcuts** (interactive mode):

| Key | View | Action |
|-----|------|--------|
| `↑↓` / `j` `k` | All | Navigate list or scroll content |
| `Enter` | Session list | Open session detail |
| `Enter` / `c` | Session detail | Capture selected agent's pane |
| `i` | Session detail | Show selected agent's inbox |
| `d` | Session detail | Run diagnostics on selected agent |
| `I` | Session detail | Show all inboxes in session |
| `D` | Session detail | Diagnose all agents in session |
| `r` | List / Detail | Refresh data |
| `q` / `Esc` | Content / Detail | Go back one level |
| `q` | Session list | Quit TUI |

Examples:

```bash
# List all sessions to find the one you want
muxcode remote list

# Get a full status overview of another project's session
muxcode remote status is-admissions-gateway

# Capture what the build agent is currently showing
muxcode remote capture is-admissions-gateway build 50

# Check if any agents have stuck inbox messages
muxcode remote inbox is-admissions-gateway

# Diagnose why the commit agent isn't responding in another session
muxcode remote diagnose is-admissions commit

# Diagnose all agents at once (prefix match: "is-admissions" → "is-admissions-gateway")
muxcode remote diagnose is-admissions --all --json
```

Core code: `bus/remote.go`, `cmd/remote.go`, `tui/remote.go`.

## Environment Variables

| Variable | Description |
|----------|-------------|
| `BUS_SESSION` | Session name for the bus directory |
| `AGENT_ROLE` | Current agent's role name (auto-detected from tmux window if unset) |
| `BUS_MEMORY_DIR` | Path to persistent memory directory (defaults to `.muxcode/memory/`) |
| `MUXCODE_ROLES` | Comma-separated extra roles to add to the known roles list |
| `MUXCODE_SPLIT_LEFT` | Space-separated windows with agent in pane 1 (defaults: plan edit build test review deploy run commit watch) |
| `MUXCODE_LIFECYCLE_LOG_MAX` | Max entries per lifecycle log before rotation (default: 1000) |
| `MUXCODE_DEDUP_WINDOW` | Dedup window in seconds for duplicate message suppression (default: 30, set to 0 to disable) |
| `MUXCODE_INBOX_POLL_TIMEOUT` | Timeout in seconds for `--wait` polling (default: 600) |
| `MUXCODE_WAIT_DEGRADE_SECS` | Cap before `--wait` auto-degrades to a tracked task and returns (default: 90, set to 0 to disable) |
| `MUXCODE_RELAY_SUPPRESS_THRESHOLD` | Identical non-edit `(from,to,action)` relays past this count in the window are suppressed (default: 4, set to 0 to disable) |
| `MUXCODE_RELAY_SUPPRESS_WINDOW` | Window in seconds for relay-loop suppression counting (default: 300) |
| `MUXCODE_ACTIVE_WATCHDOG_SECS` | Daemon advisory threshold (seconds) for a continuously-active agent (default: 600, set to 0 to disable) |
| `MUXCODE_STUCK_RELOAD_DISABLE` | Set to 1 to disable the daemon's stuck-provider auto-reload watchdog |

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

### Event Chains

Driven by `muxcode hook bash`, not by agent LLMs. Chain actions support conditional expressions with first-match-wins on action arrays.

**Build-Test-Review chain:**

1. **Build succeeds** → hook evaluates build success actions → sends `request:test` to the test agent
2. **Test succeeds** → hook evaluates test success actions → sends `request:review` to the review agent
3. **Any failure** → hook sends `event:notify` directly to edit

**Deploy-Run-Watch chain:**

1. **Deploy succeeds** → hook sends `request:run` to the run agent
2. **Run succeeds** → hook sends `request:watch` to the watch agent
3. **Watch completes** → hook sends results to edit
4. **Any failure** → hook sends `event:notify` directly to edit

After the primary chain action, subscription fan-out fires for matching event+outcome+condition patterns.

## Pane Targeting

Pane targeting is consolidated in `bus/config.go`:

- **Split-left windows** (default: plan, edit, build, test, review, deploy, run, commit, watch): agent runs in pane 1
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
│   ├── watcher_health.go # Daemon keepalive monitoring
│   ├── cleanup.go     # Session cleanup
│   └── setup.go       # Bus directory initialization and re-init purge
├── cmd/               # Subcommand handlers
├── watcher/           # Bus daemon — inbox poller + trigger file monitor
├── tui/               # Dracula-themed dashboard TUI
└── main.go            # Entry point and subcommand dispatch
```
