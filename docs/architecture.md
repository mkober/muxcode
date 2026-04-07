# Architecture

## Overview

Muxcode creates a tmux session with multiple windows, each running an independent AI agent process. Agents communicate through a file-based message bus and are coordinated by hook scripts that respond to tool execution events.

## System Design

```
┌─────────────────────────────────────────────────────────────────┐
│                          tmux session                           │
│                                                                 │
│  ┌─────────┐ ┌─────────┐ ┌─────────┐ ┌─────────┐ ┌─────────┐    │
│  │  edit   │ │  build  │ │  test   │ │ review  │ │  ...    │    │
│  │nvim|cli │ │term|cli │ │term|cli │ │term|cli │ │         │    │
│  └────┬────┘ └────┬────┘ └────┬────┘ └────┬────┘ └─────────┘    │
│       │           │           │           │                     │
│  ─────┼───────────┼───────────┼───────────┼──────────────────── │
│       │     Message Bus (/tmp/muxcode-bus-{session}/)           │
│       │     ├── inbox/{role}.jsonl                              │
│       │     ├── lock/{role}.lock                                │
│       │     ├── workflow-state.json                             │
│       │     ├── log.jsonl                                       │
│       │     ├── proc.jsonl                                      │
│       │     ├── spawn.jsonl                                     │
│       │     ├── cron.jsonl                                      │
│       │     ├── subscriptions.jsonl                             │
│       │     ├── delivery/{msg-id}.status                        │
│       │     ├── dedup.lock                                      │
│       │     ├── watcher.keepalive                               │
│       │     └── webhook.pid                                     │
│  ─────┼───────────┼───────────┼───────────┼──────────────────── │
│       │           │           │           │                     │
│  ┌────┴────┐ ┌────┴────┐ ┌────┴────┐ ┌────┴────┐                │
│  │ Hooks   │ │ Hooks   │ │ Hooks   │ │ Hooks   │                │
│  │Pre/Post │ │Pre/Post │ │Pre/Post │ │Pre/Post │                │
│  └─────────┘ └─────────┘ └─────────┘ └─────────┘                │
└─────────────────────────────────────────────────────────────────┘

Persistent:  .muxcode/memory/{role}.md      (project)
             ~/.config/muxcode/memory/     (global / cross-session)
             ~/.config/muxcode/logs/       (lifecycle logs per session)
```

## Data Flow

### Edit-Initiated Build

```
1. User types in edit window
2. Edit agent sends: muxcode send build build "Run ./build.sh" --wait
3. Bus writes to /tmp/muxcode-bus-{s}/inbox/build.jsonl
4. Bus sends tmux notification to build agent pane
5. --wait polls edit's inbox every 2s for a response
6. Build agent reads inbox, runs ./build.sh
7. Build agent replies: muxcode send edit result "Build succeeded"
8. --wait detects response, prints it to stdout as part of the Bash tool result
9. PostToolUse hook (hook bash) detects build success
10. Hook automatically sends: muxcode send test test "Run tests"
11. Test agent reads inbox, runs tests
12. Hook detects test success, sends request to review
13. Review agent reviews diff, replies to edit
```

### Deploy-Initiated Verification

```
1. Deploy agent runs `cdk deploy` (or terraform apply, pulumi up, etc.)
2. PostToolUse hook (hook bash) detects deploy-apply command success
3. Hook sends: muxcode chain deploy success
4. Chain self-loops: sends verify request back to deploy agent
5. Deploy agent runs verification checks (AWS health, HTTP smoke, CloudWatch)
6. Deploy agent reports results to edit
```

Note: preview commands (`cdk diff`, `terraform plan`, `pulumi preview`) are logged to deploy history but do **not** trigger the verify chain.

### File Edit Event Flow

```
1. Agent writes/edits a file (Write/Edit tool)
2. PostToolUse hook (hook analyze) fires
3. Hook appends file path to trigger file
4. Hook routes event to relevant agent (test/deploy/build) based on file type
5. In edit window: hook cleans up nvim diff preview, reloads file
6. Bus watcher (in analyze window) detects trigger file changes
7. After debounce, watcher sends aggregate analyze event to analyst
```

### Agent Spawn Flow

```
1. Agent runs: muxcode spawn start research "What does guard.go do?"
2. Bus generates spawn role (spawn-a1b2c3d4), creates tmux window
3. Task message pre-seeded in spawn's inbox
4. Launches: AGENT_ROLE=spawn-a1b2c3d4 muxcode-agent.sh research
5. After 2s delay, bus notifies spawn agent to read inbox
6. Spawn agent works on task, sends messages back to owner via bus
7. Spawn agent completes, exits (tmux window closes)
8. Watcher detects window death via checkSpawns()
9. Watcher sends spawn-complete event to owner with last result
10. Owner retrieves result: muxcode spawn result <id>
```

### Watcher debounce

The watcher uses a two-phase approach to coalesce burst edits:

1. **Detect change**: trigger file size changes → record pending timestamp
2. **Wait for stability**: if no further changes for the debounce interval (default 8 seconds), fire the aggregate event

This means rapid consecutive edits (e.g. Claude writing multiple files) are coalesced into a single analyst event containing all affected file paths, rather than firing once per edit.

### Diff Preview Flow

```
1. Agent proposes an edit (Write/Edit tool)
2. PreToolUse hook (muxcode-preview-hook.sh) fires
3. Hook dismisses any pending prompt, cleans stale diff from prior rejected edit
4. Hook opens the file in nvim at the target line (folds open, search cleared)
5. For Edit tool: python3 generates temp file with proposed change
6. Hook opens diff split with scrollbind (original below, proposed above)
7. After 150ms: separate send-keys jumps to changed line (scrollbind must be active first)
8. User reviews in nvim, accepts or rejects in Claude Code
9a. Accept → PostToolUse hook waits ~1s for preview to finish, cleans diff, reloads file
9b. Reject → Next tool's PreToolUse hook (muxcode-diff-cleanup.sh) cleans diff
```

**Key constraints:**
- Every nvim command in a `|` pipe chain needs its own `sil!` prefix
- Jump-to-line must be a separate `tmux send-keys` after 150ms so scrollbind is active
- Concurrent hook invocations (global + project settings) guarded by temp file age (< 3s → skip)

## Bus Protocol

### Message Types

- **request**: Ask an agent to do something. The recipient should reply with a response.
- **response**: Reply to a request. Include `--reply-to <id>` to link to the original.
- **event**: Informational notification. No reply expected.

### Auto-CC

Messages from build, test, review, and deploy agents to any non-edit agent are automatically copied to the edit inbox. This gives the orchestrator visibility without explicit routing.

### Notification Flow

1. `muxcode send` delivers message to inbox file
2. `Send()` creates a delivery status file (`delivery/{msg-id}.status`) tracking the message lifecycle (sent → delivered → responded)
3. If the message has `ReplyTo`, `Send()` marks the original message as "responded"
4. `send` calls `Notify()` to alert the recipient (triple-path):
   - **Trigger file** (always): writes timestamp to `trigger-{role}.notify` — agents running `muxcode inbox --poll` detect this via `stat()` polling (no pane interaction, no TOCTOU race)
   - **Polling agents** (`--poll` or `--wait` active): skipped for send-keys — the poll loop watches the trigger file
   - **Harness panes**: skipped — they poll inbox directly
   - **Idle agents** (at `❯` prompt, including edit): `send-keys` "You have new messages" + Enter to wake them up
   - **Active agents** (including edit): `display-message` (passive status bar flash)
5. If auto-CC fires, `send` also notifies edit
6. The watcher provides fallback notifications for all roles
7. When an agent reads its inbox via `Receive()`, consumed messages are marked "delivered" in their status files

Never use `send-keys` on **active** agents — it disrupts Claude Code's input buffer, interrupts in-progress tool execution, and causes agents to stall at "Interrupted" prompts. Idle agents at the `❯` prompt are safe to wake via `send-keys` because no tool execution is in progress. `IsAgentIdle()` detects idle state via `tmux capture-pane -S -8` (scans all captured lines for exact match on the `❯` character — scans all lines because Claude Code renders a decorative footer below the prompt).

### Delivery Tracking

Every message sent through the bus gets a delivery status file at `delivery/{msg-id}.status` tracking its lifecycle:

| State | Written by | Meaning |
|-------|-----------|---------|
| `sent` | `Send()` | Message appended to recipient inbox |
| `delivered` | `Receive()` | Message consumed from inbox by recipient |
| `responded` | `Send()` with `ReplyTo` | Response sent with matching reply-to ID |

Query status via `muxcode track <msg-id>`. Expired status files are cleaned by `CleanExpiredDeliveries()` based on `SentAt` age.

Core code: `bus/delivery.go`, `cmd/track.go`.

### Edit inbox polling (`--wait`)

The `--wait` flag on `send` provides inline response delivery for agents that need synchronous request/response patterns:

1. Edit agent runs: `muxcode send build build "Run ./build.sh" --wait`
2. Bus delivers the message and notifies the recipient
3. `--wait` enters a poll loop — checks the sender's (edit's) inbox every 2 seconds
4. When a response arrives, `--wait` consumes it and prints it to stdout
5. The response appears as part of the Bash tool result — no separate inbox check needed

Timeout is controlled by `MUXCODE_INBOX_POLL_TIMEOUT` (default: 600 seconds). If no response arrives before timeout, `--wait` exits with a timeout message.

**Why not PostToolUse hooks?** Hook stdout is never seen by Claude Code — hooks are fire-and-forget side effects. A previous approach using a PostToolUse inbox-polling hook consumed the inbox but the output went nowhere. The `--wait` flag solves this by keeping the poll inside the original Bash tool invocation, so the response is part of the same tool result stream.

### Lock mechanism

Agents indicate busy state via lock files at `/tmp/muxcode-bus-{session}/lock/{role}.lock`. The dashboard TUI reads lock status for display. Commands:

- `muxcode lock [role]` — create the lock file
- `muxcode unlock [role]` — remove the lock file
- `muxcode is-locked [role]` — check status (exit 0 if locked, 1 if not)

## Memory System

Memory has two layers:

```
~/.config/muxcode/memory/            # Global (cross-session, all projects)
├── shared.md                        # Universal shared learnings
├── {role}.md                        # Universal per-role learnings
└── {role}/                          # Daily archives
    └── YYYY-MM-DD.md

.muxcode/memory/                     # Project-level (per-project)
├── shared.md                        # Cross-agent shared learnings
├── edit.md                          # Edit agent learnings
├── build.md                         # Build agent learnings
└── ...                              # Per-role files
```

When agents read context (`muxcode memory context`), global memory is prepended before project memory. Project-specific learnings can override or refine global patterns. Use `--no-global` to skip global memory.

Agents can search memory with `muxcode memory search "<query>"` (BM25 ranking by default with IDF weighting, length normalization, and 2x header boost; keyword mode also available via `--mode keyword`). List all sections with `muxcode memory list`. Both support `--role` filtering and `--scope project|global|all`.

Memory files rotate daily — on first write each day, the previous day's file is archived to `{role}/YYYY-MM-DD.md`. Archives are retained for 30 days. Context includes the active file plus the last 7 days of archives by default (`--days N` to override). Both global and project memory rotate independently.

## Hook Architecture

Hooks are Claude Code shell hooks configured in `.claude/settings.json`. They run asynchronously and receive tool event JSON on stdin.

| Hook | Phase | Trigger | Mode | Purpose |
|------|-------|---------|------|---------|
| `muxcode hook guard` | PreToolUse | Bash | sync | Block prohibited commands in edit window |
| `muxcode-preview-hook.sh` | PreToolUse | Write/Edit | async | Show diff preview in nvim |
| `muxcode-diff-cleanup.sh` | PreToolUse | Read/Bash/etc | async | Clean stale diff preview |
| `muxcode hook analyze` | PostToolUse | Write/Edit | async | Route file events, trigger watcher |
| `muxcode hook bash` | PostToolUse | Bash | async | Drive build-test-review and deploy-verify chains + subscription fan-out |

### Hook Chain Guarantee

The build-test-review and deploy-verify chains are **deterministic** — driven by bash hooks detecting command exit codes, not by LLM decisions. This ensures the chains fire reliably regardless of how the agent phrases its output.

## Window Layout

### Standard Agent Window
```
┌────────────────────┬────────────────────┐
│                    │                    │
│   Terminal         │   AI Agent         │
│   (pane 0)         │   (pane 1)         │
│                    │                    │
└────────────────────┴────────────────────┘
```

### Split-Left Windows (edit, build, test, review, deploy, analyze, commit, watch)
```
┌────────────────────┬────────────────────┐
│                    │                    │
│   Tool             │   AI Agent         │
│   (nvim/watcher/   │   (pane 1)         │
│    git-status/     │                    │
│    watch-log)      │                    │
│   (pane 0)         │                    │
└────────────────────┴────────────────────┘
```

### Status Window
```
┌─────────────────────────────────────────┐
│                                         │
│   Dashboard TUI                         │
│   (single pane 0)                       │
│                                         │
└─────────────────────────────────────────┘
```

### Local LLM Agent Flow

```
1. muxcode-agent.sh checks MUXCODE_{ROLE}_CLI for role
2. If "local", checks Ollama health (GET /api/tags)
3a. Ollama reachable: exec muxcode agent run <role>
3b. Ollama unreachable: fall through to Claude Code
4. Agent loop: poll inbox → build conversation → call Ollama API → execute tools → send response
5. Tool execution enforces allowedTools from tool profile
6. Bash commands logged directly to {role}-history.jsonl (replaces PostToolUse hooks)
7. Conversation state reset between inbox checks (prevents unbounded context)
```

### Event Subscription Fan-out

```
1. Build/test/deploy command completes
2. hook bash detects exit code
3. Hook sends: muxcode chain <event> <outcome>
4. Chain fires primary action (e.g. build success → test)
5. Chain fires subscriptions: read subscriptions.jsonl, match event+outcome
6. Matching subscribers receive messages via SendNoCC() (no auto-CC to edit)
```

## Left-pane consoles

Each split-left window runs `muxcode console <role>` in the left pane, displaying role-specific status and history. The console command is a single Go binary that replaced the original per-role shell poller scripts.

| Window | Command | Data source |
|--------|---------|-------------|
| build | `console build` | `build-history.jsonl` |
| test | `console test` | `test-history.jsonl` |
| review | `console review` | `review-history.jsonl` |
| deploy | `console deploy` | `deploy-history.jsonl` |
| run | `console run` | `run-history.jsonl` |
| watch | `console watch` | `watch-history.jsonl` |
| commit | `console commit` | `commit-history.jsonl` + live git status |
| analyze | `console analyze` | `log.jsonl` (filtered: `from=analyze`, `type=response`) |
| api | `console api` | `.muxcode/api/history.jsonl` |

Consoles share a common rendering pipeline in `bus/console.go`: Dracula color scheme, 5-second poll interval (configurable via `--interval`), clear-and-redraw via ANSI escape codes. Per-role rendering is driven by a config map (`DefaultConsoleConfigs()`) with function pointers — not separate codepaths.

Build and test consoles display an `errors` field (extracted by the bash hook) for failed entries, preferring error-relevant lines over raw output. Failed builds/tests show the command, exit code, and error lines in red; previous failures show in yellow.

The commit console combines live git status (branch, staged, modified, untracked files) with commit history entries.

The analyze console reads the shared bus log (`log.jsonl`) rather than a dedicated history file, filtering for analyst response messages. It displays findings count, recent entries with timestamp/action/target/truncated payload, and the full payload of the latest finding.

## Startup prompt handling

When launching a session, Claude Code may show two sequential prompts per agent window:
1. **Workspace trust** — "Yes, I trust this folder" (new workspaces)
2. **Bypass permissions** — "Bypass Permissions mode" warning (all agents use `--dangerously-skip-permissions`)

The launcher handles both automatically via a single background loop that runs after all windows are created:

1. Polls each agent pane every 2 seconds (30 attempts, ~60 seconds max)
2. Captures pane content with `tmux capture-pane -p`
3. If "trust this folder" detected: sends Enter to accept (marks as not-done — bypass prompt may follow)
4. If "Bypass Permissions" detected: sends Down + Enter to select "Yes, I accept"
5. If `❯` idle prompt detected: agent is past all prompts — marks as accepted
6. **Edit agent startup**: when the edit agent reaches `❯`, waits 1s for the TUI to fully initialize, re-verifies `❯` is still showing, then sends a startup event (`Session started — review last saved context from memory`) via `muxcode send` with `AGENT_ROLE=edit` (the bus `Notify()` handles wake-up via `notifyIdleSendKeys()` with dedup)
7. Watch/analyze agents do **not** get startup messages — the watcher delivers inbox items naturally, and unsolicited responses would CC noise to edit
8. Exits early once all panes are handled

Core code: `muxcode.sh` (auto-accept block near end of file)

## Session re-init

When a MuxCode session restarts with the same name, `Init()` in `bus/setup.go` detects the existing bus directory and purges stale data to prevent false watcher alerts (loop-detected, compact-recommended) from the previous session.

- **Detection**: `os.Stat(busDir)` — if the directory exists, `reInit` flag is set
- **Truncated files** (path preserved for writers): inboxes, `log.jsonl`, `cron.jsonl`, `proc.jsonl`, `spawn.jsonl`, `subscriptions.jsonl`, `{role}-history.jsonl`, `cron-history.jsonl`
- **Removed files** (recreated on demand): session meta (`session/*.json`), lock files (`lock/*.lock`, `lock/*.stopped`), proc logs (`proc/*.log`), orphaned spawn inboxes (`inbox/spawn-*.jsonl`), trigger file, delivery status files (`delivery/*.status`)
- **Preserved**: memory files (`.muxcode/memory/`, `~/.config/muxcode/memory/`) — persistent learnings survive re-init
- **Watcher grace period**: `lastLoopCheck` and `lastCompactCheck` initialized to `time.Now()` in `New()`, so loop detection (60s) and compaction checks (120s) skip the first interval

Core code: `bus/setup.go` (`Init()`, `resetFile()`, `purgeStaleFiles()`), `watcher/watcher.go` (`New()`)

## Workflow state machine

The workflow state machine tracks the editing lifecycle as code moves from editing through validation to ready-for-commit. It observes events (file edits, chain outcomes, agent messages) and maintains a single persisted state — it never blocks actions.

### States

12 ordered integer states for regression comparison:

| # | State | Color | Meaning |
|---|-------|-------|---------|
| 0 | `idle` | dim | No activity |
| 1 | `editing` | cyan | Files being edited |
| 2 | `analyzing` | cyan | Analyze agent processing |
| 3 | `building` | yellow | Build in progress |
| 4 | `build-failed` | red | Build failed |
| 5 | `testing` | yellow | Tests in progress |
| 6 | `test-failed` | red | Tests failed |
| 7 | `reviewing` | yellow | Review in progress |
| 8 | `reviewed` | green | Review complete — ready for commit |
| 9 | `committing` | yellow | Git commit/push in progress |
| 10 | `deploying` | yellow | Deploy in progress |
| 11 | `deploy-failed` | red | Deploy failed |

### Persistence

Single JSON file at `{BusDir(session)}/workflow-state.json`. Read-modify-write under exclusive `syscall.Flock` on `{BusDir}/lock/workflow.lock`.

### Transition sources

| Source | Transitions |
|--------|-------------|
| `hook analyze` | → `editing` (file edit detected) |
| `watcher routeTrigger` | → `analyzing` (analyze event sent) |
| `hook bash` | → `building`, `testing`, `deploying` (command detected) |
| `triggerChain` | → `testing`/`build-failed`, `reviewing`/`test-failed`, `deploy-failed` (chain outcomes) |
| `watcher checkInboxes` | → `reviewed` (review message to edit detected) |

### Regression rule

Any transition to `editing` from state >= `analyzing` clears all outcome fields (`build_outcome`, `test_outcome`, `review_outcome`, `deploy_outcome`). File changes potentially invalidate prior results.

### Visibility

- **TUI dashboard** (`tui/model.go`): WORKFLOW section between session info and AGENTS, color-coded by state
- **Left-pane consoles** (`cmd/console.go`): workflow state line in header across all agent consoles
- **CLI**: `muxcode workflow [--json]` to query, `workflow reset` to manually reset

Every transition logs to the lifecycle system via `LogLifecycle()`.

Core code: `bus/workflow.go`, `bus/workflow_test.go`, `cmd/workflow.go`

## Lifecycle logging

Persistent JSONL logs at `~/.config/muxcode/logs/{session}.log` record the full startup-to-cleanup lifecycle of each session. Unlike the ephemeral bus log at `/tmp/`, lifecycle logs survive session cleanup and accumulate across restarts of the same session name.

**Instrumented components:**

| Component | Source | Key events |
|-----------|--------|------------|
| `muxcode.sh` | `launcher` | session-start, bus-init, stale-kill, watcher-start, monitor-start, session-create, session-ready |
| `muxcode.sh` (auto-accept loop) | `auto-accept` | trust-prompt, bypass-prompt, agent-ready, complete |
| `muxcode-agent.sh` | `agent` | launch (role + CLI type) |
| `bus/setup.go` | `init` | init, re-init |
| `watcher/watcher.go` | `watcher` | started, lock-failed, inbox-notify, startup-notify, trigger-route, cron-fire, proc/spawn-complete, loop/compact alerts, ollama/agent health |
| `cmd/watch.go` (`--monitor`) | `monitor` | session-gone, stale-detected, watcher-restart |
| `bus/cleanup.go` | `cleanup` | session-cleanup |

**Dual-writer pattern:** Go code calls `bus.LogLifecycle()` directly. Bash scripts call `muxcode lifecycle log` (CLI wrapper) which handles JSON formatting and flock-protected writes. Both converge on the same JSONL file.

**Debugging workflow:**

```bash
# After a subsession launch failure, check what happened
muxcode lifecycle show is-admissions-gateway --source launcher
muxcode lifecycle show is-admissions-gateway --source watcher --level error

# Compare startup timing across sessions
muxcode lifecycle show muxcode --event session-start --all
```

Core code: `bus/lifecycle.go`, `cmd/lifecycle.go`

## See also

- [Agent Bus](agent-bus.md) — CLI reference for `muxcode`
- [Agents](agents.md) — Role descriptions and customization
- [Hooks](hooks.md) — Hook system and customization
- [Configuration](configuration.md) — Config file and env var reference
