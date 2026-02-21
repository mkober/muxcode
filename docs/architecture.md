# Architecture

## Overview

Muxcode creates a tmux session with multiple windows, each running an independent AI agent process. Agents communicate through a file-based message bus and are coordinated by hook scripts that respond to tool execution events.

## System Design

```
┌─────────────────────────────────────────────────────────────────┐
│                        tmux session                             │
│                                                                 │
│  ┌─────────┐ ┌─────────┐ ┌─────────┐ ┌─────────┐ ┌─────────┐ │
│  │  edit    │ │  build  │ │  test   │ │ review  │ │  ...    │ │
│  │ nvim|cli │ │term|cli │ │term|cli │ │term|cli │ │         │ │
│  └────┬────┘ └────┬────┘ └────┬────┘ └────┬────┘ └─────────┘ │
│       │           │           │           │                     │
│  ─────┼───────────┼───────────┼───────────┼─────────────────── │
│       │     Message Bus (/tmp/muxcode-bus-{session}/)          │
│       │     ├── inbox/{role}.jsonl                               │
│       │     ├── lock/{role}.lock                                │
│       │     └── log.jsonl                                       │
│  ─────┼───────────┼───────────┼───────────┼─────────────────── │
│       │           │           │           │                     │
│  ┌────┴────┐ ┌────┴────┐ ┌────┴────┐ ┌────┴────┐              │
│  │ Hooks   │ │ Hooks   │ │ Hooks   │ │ Hooks   │              │
│  │Pre/Post │ │Pre/Post │ │Pre/Post │ │Pre/Post │              │
│  └─────────┘ └─────────┘ └─────────┘ └─────────┘              │
└─────────────────────────────────────────────────────────────────┘

Persistent:  .muxcode/memory/{role}.md
```

## Data Flow

### Edit-Initiated Build

```
1. User types in edit window
2. Edit agent sends: muxcode-agent-bus send build build "Run ./build.sh"
3. Bus writes to /tmp/muxcode-bus-{s}/inbox/build.jsonl
4. Bus sends tmux notification to build agent pane
5. Build agent reads inbox, runs ./build.sh
6. Build agent replies: muxcode-agent-bus send edit result "Build succeeded"
7. PostToolUse hook (muxcode-bash-hook.sh) detects build success
8. Hook automatically sends: muxcode-agent-bus send test test "Run tests"
9. Test agent reads inbox, runs tests
10. Hook detects test success, sends request to review
11. Review agent reviews diff, replies to edit
```

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

Messages from build, test, and review agents to any non-edit agent are automatically copied to the edit inbox. This gives the orchestrator visibility without explicit routing.

### Notification Flow

1. `muxcode-agent-bus send` delivers message to inbox file
2. `send` calls `notify` to alert the recipient via `tmux send-keys`
3. If auto-CC fires, `send` also notifies edit
4. The watcher provides fallback notifications for all roles except edit

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

## Hook Architecture

Hooks are Claude Code shell hooks configured in `.claude/settings.json`. They run asynchronously and receive tool event JSON on stdin.

| Hook | Phase | Trigger | Purpose |
|------|-------|---------|---------|
| `muxcode-preview-hook.sh` | PreToolUse | Write/Edit | Show diff preview in nvim |
| `muxcode-diff-cleanup.sh` | PreToolUse | Read/Bash/etc | Clean stale diff preview |
| `muxcode-analyze-hook.sh` | PostToolUse | Write/Edit | Route file events, trigger watcher |
| `muxcode-bash-hook.sh` | PostToolUse | Bash | Drive build-test-review chain |

### Hook Chain Guarantee

The build-test-review chain is **deterministic** — driven by bash hooks detecting command exit codes, not by LLM decisions. This ensures the chain fires reliably regardless of how the agent phrases its output.

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

### Split-Left Windows (edit, analyze, commit)
```
┌────────────────────┬────────────────────┐
│                    │                    │
│   Tool             │   AI Agent         │
│   (nvim/watcher/   │   (pane 1)         │
│    git-status)     │                    │
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

## See also

- [Agent Bus](agent-bus.md) — CLI reference for `muxcode-agent-bus`
- [Agents](agents.md) — Role descriptions and customization
- [Hooks](hooks.md) — Hook system and customization
- [Configuration](configuration.md) — Config file and env var reference
