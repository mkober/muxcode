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
│       │     ├── log.jsonl                                       │
│       │     ├── proc.jsonl                                      │
│       │     ├── spawn.jsonl                                     │
│       │     ├── cron.jsonl                                      │
│       │     ├── subscriptions.jsonl                              │
│       │     └── webhook.pid                                     │
│  ─────┼───────────┼───────────┼───────────┼──────────────────── │
│       │           │           │           │                     │
│  ┌────┴────┐ ┌────┴────┐ ┌────┴────┐ ┌────┴────┐                │
│  │ Hooks   │ │ Hooks   │ │ Hooks   │ │ Hooks   │                │
│  │Pre/Post │ │Pre/Post │ │Pre/Post │ │Pre/Post │                │
│  └─────────┘ └─────────┘ └─────────┘ └─────────┘                │
└─────────────────────────────────────────────────────────────────┘

Persistent:  .muxcode/memory/{role}.md
```

## Data Flow

### Edit-Initiated Build

```
1. User types in edit window
2. Edit agent sends: muxcode-agent-bus send build build "Run ./build.sh" --wait
3. Bus writes to /tmp/muxcode-bus-{s}/inbox/build.jsonl
4. Bus sends tmux notification to build agent pane
5. --wait polls edit's inbox every 2s for a response
6. Build agent reads inbox, runs ./build.sh
7. Build agent replies: muxcode-agent-bus send edit result "Build succeeded"
8. --wait detects response, prints it to stdout as part of the Bash tool result
9. PostToolUse hook (muxcode-bash-hook.sh) detects build success
10. Hook automatically sends: muxcode-agent-bus send test test "Run tests"
11. Test agent reads inbox, runs tests
12. Hook detects test success, sends request to review
13. Review agent reviews diff, replies to edit
```

### Deploy-Initiated Verification

```
1. Deploy agent runs `cdk deploy` (or terraform apply, pulumi up, etc.)
2. PostToolUse hook (muxcode-bash-hook.sh) detects deploy-apply command success
3. Hook sends: muxcode-agent-bus chain deploy success
4. Chain self-loops: sends verify request back to deploy agent
5. Deploy agent runs verification checks (AWS health, HTTP smoke, CloudWatch)
6. Deploy agent reports results to edit
```

Note: preview commands (`cdk diff`, `terraform plan`, `pulumi preview`) are logged to deploy history but do **not** trigger the verify chain.

### File Edit Event Flow

```
1. Agent writes/edits a file (Write/Edit tool)
2. PostToolUse hook (muxcode-analyze-hook.sh) fires
3. Hook appends file path to trigger file
4. Hook routes event to relevant agent (test/deploy/build) based on file type
5. In edit window: hook cleans up nvim diff preview, reloads file
6. Bus watcher (in analyze window) detects trigger file changes
7. After debounce, watcher sends aggregate analyze event to analyst
```

### Agent Spawn Flow

```
1. Agent runs: muxcode-agent-bus spawn start research "What does guard.go do?"
2. Bus generates spawn role (spawn-a1b2c3d4), creates tmux window
3. Task message pre-seeded in spawn's inbox
4. Launches: AGENT_ROLE=spawn-a1b2c3d4 muxcode-agent.sh research
5. After 2s delay, bus notifies spawn agent to read inbox
6. Spawn agent works on task, sends messages back to owner via bus
7. Spawn agent completes, exits (tmux window closes)
8. Watcher detects window death via checkSpawns()
9. Watcher sends spawn-complete event to owner with last result
10. Owner retrieves result: muxcode-agent-bus spawn result <id>
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
3. Hook opens the file in nvim at the target line
4. Hook creates temp file with proposed change
5. Hook opens diff split in nvim (original below, proposed above)
6. User reviews in nvim, accepts or rejects in Claude Code
7a. Accept → PostToolUse hook cleans diff, reloads file at changed line
7b. Reject → Next tool's PreToolUse hook (muxcode-diff-cleanup.sh) cleans diff
```

## Bus Protocol

### Message Types

- **request**: Ask an agent to do something. The recipient should reply with a response.
- **response**: Reply to a request. Include `--reply-to <id>` to link to the original.
- **event**: Informational notification. No reply expected.

### Auto-CC

Messages from build, test, review, and deploy agents to any non-edit agent are automatically copied to the edit inbox. This gives the orchestrator visibility without explicit routing.

### Notification Flow

1. `muxcode-agent-bus send` delivers message to inbox file
2. `send` calls `notify` to alert the recipient via `tmux send-keys`
3. If auto-CC fires, `send` also notifies edit
4. The watcher provides fallback notifications for all roles except edit

### Edit inbox polling (`--wait`)

The edit agent cannot receive tmux `send-keys` notifications (they would inject text into the user's input and cause conversation loops). Instead, the `--wait` flag on `send` provides inline response delivery:

1. Edit agent runs: `muxcode-agent-bus send build build "Run ./build.sh" --wait`
2. Bus delivers the message and notifies the recipient
3. `--wait` enters a poll loop — checks the sender's (edit's) inbox every 2 seconds
4. When a response arrives, `--wait` consumes it and prints it to stdout
5. The response appears as part of the Bash tool result — no separate inbox check needed

Timeout is controlled by `MUXCODE_INBOX_POLL_TIMEOUT` (default: 120 seconds). If no response arrives before timeout, `--wait` exits with a timeout message.

**Why not PostToolUse hooks?** Hook stdout is never seen by Claude Code — hooks are fire-and-forget side effects. A previous approach using a PostToolUse inbox-polling hook consumed the inbox but the output went nowhere. The `--wait` flag solves this by keeping the poll inside the original Bash tool invocation, so the response is part of the same tool result stream.

### Lock mechanism

Agents indicate busy state via lock files at `/tmp/muxcode-bus-{session}/lock/{role}.lock`. The dashboard TUI reads lock status for display. Commands:

- `muxcode-agent-bus lock [role]` — create the lock file
- `muxcode-agent-bus unlock [role]` — remove the lock file
- `muxcode-agent-bus is-locked [role]` — check status (exit 0 if locked, 1 if not)

## Memory System

Per-project persistent memory stored in `.muxcode/memory/`:

```
.muxcode/memory/
├── shared.md      # Cross-agent shared learnings
├── edit.md        # Edit agent learnings
├── build.md       # Build agent learnings
└── ...            # Per-role files
```

Memory is project-scoped — each project has its own memory directory, created when `muxcode-agent-bus init` runs.

Agents can search memory with `muxcode-agent-bus memory search "<query>"` (BM25 ranking by default with IDF weighting, length normalization, and 2x header boost; keyword mode also available via `--mode keyword`). List all sections with `muxcode-agent-bus memory list`. Both support `--role` filtering.

Memory files rotate daily — on first write each day, the previous day's file is archived to `{role}/YYYY-MM-DD.md`. Archives are retained for 30 days. Context includes the active file plus the last 7 days of archives by default (`--days N` to override).

## Hook Architecture

Hooks are Claude Code shell hooks configured in `.claude/settings.json`. They run asynchronously and receive tool event JSON on stdin.

| Hook | Phase | Trigger | Mode | Purpose |
|------|-------|---------|------|---------|
| `muxcode-edit-guard.sh` | PreToolUse | Bash | sync | Block prohibited commands in edit window |
| `muxcode-preview-hook.sh` | PreToolUse | Write/Edit | async | Show diff preview in nvim |
| `muxcode-diff-cleanup.sh` | PreToolUse | Read/Bash/etc | async | Clean stale diff preview |
| `muxcode-analyze-hook.sh` | PostToolUse | Write/Edit | async | Route file events, trigger watcher |
| `muxcode-bash-hook.sh` | PostToolUse | Bash | async | Drive build-test-review and deploy-verify chains + subscription fan-out |

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

### Split-Left Windows (edit, build, test, review, deploy, analyze, commit, watch, api)
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
3a. Ollama reachable: exec muxcode-agent-bus agent run <role>
3b. Ollama unreachable: fall through to Claude Code
4. Agent loop: poll inbox → build conversation → call Ollama API → execute tools → send response
5. Tool execution enforces allowedTools from tool profile
6. Bash commands logged directly to {role}-history.jsonl (replaces PostToolUse hooks)
7. Conversation state reset between inbox checks (prevents unbounded context)
```

### Event Subscription Fan-out

```
1. Build/test/deploy command completes
2. muxcode-bash-hook.sh detects exit code
3. Hook sends: muxcode-agent-bus chain <event> <outcome>
4. Chain fires primary action (e.g. build success → test)
5. Chain fires subscriptions: read subscriptions.jsonl, match event+outcome
6. Matching subscribers receive messages via SendNoCC() (no auto-CC to edit)
```

## Left-pane pollers

Each split-left window runs a poller script in the left pane that displays role-specific history.

| Window | Script | Data source |
|--------|--------|-------------|
| build | `muxcode-build-log.sh` | `build-history.jsonl` |
| test | `muxcode-test-log.sh` | `test-history.jsonl` |
| review | `muxcode-review-log.sh` | `review-history.jsonl` |
| deploy | `muxcode-deploy-log.sh` | `deploy-history.jsonl` |
| run | `muxcode-runner-log.sh` | `run-history.jsonl` |
| watch | `muxcode-watch-log.sh` | `watch-history.jsonl` |
| commit | `muxcode-commit-log.sh` / `muxcode-git-status.sh` | `commit-history.jsonl` / git |
| analyze | `muxcode-analyze-log.sh` | `log.jsonl` (filtered: `from=analyze`, `type=response`) |
| api | `muxcode-api-log.sh` | `.muxcode/api/history.jsonl` |

Pollers share a common pattern: `set -uo pipefail`, Dracula color scheme, `jq` primary with `python3` fallback, 5-second poll interval, clear-and-redraw via `\033[2J\033[H`.

The analyze poller is unique — it reads the shared bus log (`log.jsonl`) rather than a dedicated history file, filtering for analyst response messages. It displays: findings count, last 15 entries with timestamp/action/target/truncated payload, and the full payload of the latest finding.

## Session re-init

When a MUXcode session restarts with the same name, `Init()` in `bus/setup.go` detects the existing bus directory and purges stale data to prevent false watcher alerts (loop-detected, compact-recommended) from the previous session.

- **Detection**: `os.Stat(busDir)` — if the directory exists, `reInit` flag is set
- **Truncated files** (path preserved for writers): inboxes, `log.jsonl`, `cron.jsonl`, `proc.jsonl`, `spawn.jsonl`, `subscriptions.jsonl`, `{role}-history.jsonl`, `cron-history.jsonl`
- **Removed files** (recreated on demand): session meta (`session/*.json`), lock files (`lock/*.lock`), proc logs (`proc/*.log`), orphaned spawn inboxes (`inbox/spawn-*.jsonl`), trigger file
- **Preserved**: memory files (`.muxcode/memory/`) — persistent learnings survive re-init
- **Watcher grace period**: `lastLoopCheck` and `lastCompactCheck` initialized to `time.Now()` in `New()`, so loop detection (60s) and compaction checks (120s) skip the first interval

Core code: `bus/setup.go` (`Init()`, `resetFile()`, `purgeStaleFiles()`), `watcher/watcher.go` (`New()`)

## See also

- [Agent Bus](agent-bus.md) — CLI reference for `muxcode-agent-bus`
- [Agents](agents.md) — Role descriptions and customization
- [Hooks](hooks.md) — Hook system and customization
- [Configuration](configuration.md) — Config file and env var reference
