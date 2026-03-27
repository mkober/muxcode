# Transactional messaging bus

Redesign the agent notification and message delivery system to eliminate the inter-tool-call race condition where `send-keys` interrupts agents during brief idle gaps between consecutive Claude Code tool calls. Replace the current destructive notification mechanism with a pull-based polling model that separates message delivery from agent wake-up.

## Problem statement

### The inter-tool-call race

Claude Code executes tool calls sequentially. Between consecutive tool calls (e.g., `muxcode inbox` followed by `muxcode send`), the TUI briefly shows the `❯` idle prompt for ~100-200ms before starting the next tool. During this window:

1. The watcher's `checkInboxes()` or a `cmd/send.go` call runs `IsAgentIdle()` (in `bus/notify.go`)
2. `IsAgentIdle()` captures the pane via `tmux capture-pane -S -8` and finds the `❯` character
3. `Notify()` fires `notifySendKeys()` which injects "You have new messages" + Enter into the pane
4. The agent starts its next tool call, but Claude Code interprets the injected text as user input
5. Result: "Interrupted - What should Claude do instead?" -- the agent loses its current task

### Current mitigations and their limits

| Mitigation | Location | Gap |
|-----------|----------|-----|
| `sendKeysCooldown` (10s) | `bus/notify.go` | Only suppresses *after* a send-keys was delivered. First notification during a tool-call chain still hits the race. Cooldown is a fixed duration that doesn't adapt to actual agent activity. |
| `SetWaiting()` marker | `bus/notify.go`, `cmd/send.go` | Only active during `--wait` polling loops. Agents performing multi-step work without `--wait` are unprotected. |
| Passive retry via watcher | `watcher/watcher.go` | Falls back to `display-message` which Claude Code cannot see. The watcher retries with send-keys when the agent appears idle, but the idle detection has the same TOCTOU race. |
| `notifyCooldown` (2s) | `bus/notify.go` | Prevents duplicate notifications within a 2s window, but doesn't prevent the first notification from arriving during an unsafe window. |

### Failure cascade

```
edit sends "run tests" -> test agent
  |
test agent reads inbox, starts executing tests (tool call 1)
  |
Between tool calls: prompt appears for ~150ms
  |
watcher checkInboxes() sees new CC message for edit, or
another agent sends a message to test -> Notify() fires
  |
IsAgentIdle() returns true -> send-keys fires
  |
"Interrupted" -- test agent abandons current task
  |
edit agent's --wait times out (600s) -- no response ever arrives
  |
edit agent has no way to know the test failed vs is still running
```

### Impact

- **Lost responses**: Interrupted agents never send their response. The requesting agent waits until timeout.
- **Stuck workflows**: The build->test->review chain breaks when any agent gets interrupted mid-task.
- **Silent failures**: No error is reported -- the timeout just expires with no response.
- **Wasted context**: The interrupted agent loses all accumulated context from the interrupted task. Retrying means re-reading everything from scratch.

## Goals

1. **Zero agent interruption**: No mechanism should inject text into an active agent's terminal pane
2. **Reliable message delivery**: Messages reach their destination exactly once, with confirmation
3. **Request-response correlation**: Every request can be matched to its response via ID
4. **Orchestrator awareness**: The edit agent (or any sender) can track the lifecycle of delegated tasks
5. **Crash resilience**: Agent crashes or restarts don't lose pending messages
6. **Backward compatible CLI**: `muxcode send` and `muxcode inbox` retain their current interface

## Non-goals

- Distributed messaging (multi-machine) -- the bus remains file-based in `/tmp`
- Message persistence across sessions -- session re-init purges as today
- Priority queues or message ordering guarantees beyond FIFO within a single inbox
- Replacing the hook-driven chain system (`bus/hook.go`) -- chains are deterministic and work correctly
- Real-time streaming between agents -- messages remain discrete JSONL entries
- External dependencies -- Go stdlib only

## Proposed architecture

### Core change: eliminate send-keys for wake-up

Replace `notifySendKeys()` entirely with a **trigger file** polling mechanism. Instead of injecting text into the agent's pane, write a trigger marker that the agent reads on its own schedule.

### Message delivery (unchanged)

Message delivery via `Send()` / `SendNoCC()` in `bus/inbox.go` remains the same: append JSONL to the recipient's inbox file. This is already atomic at the OS level (single `write()` call for each line).

### Notification replacement: trigger file polling

#### Agent-side polling

Each agent runs a persistent background `muxcode inbox --poll` command (Bash tool) that watches a trigger file:

```
/tmp/muxcode-bus-{session}/trigger-{role}.notify
```

When `Notify()` is called, instead of `send-keys`, it writes the current unix timestamp to this trigger file. The polling command detects the file change (via `stat()` polling at 1-2s intervals) and outputs the inbox contents to stdout, which Claude Code sees as a Bash tool result.

This eliminates the race entirely: the agent's own process reads the inbox when it's ready, not when an external process decides the pane looks idle.

#### Notify() rewrite

```
Notify(session, role):
  1. Skip if harness pane (unchanged)
  2. Skip if already notified for this inbox size (unchanged dedup)
  3. Write timestamp to trigger-{role}.notify
  4. Optionally: display-message for visual indicator (human-visible only)
```

No `IsAgentIdle()` check. No `send-keys`. No cooldown timers. The trigger file is always safe to write.

#### Polling command: `muxcode inbox --poll`

```
muxcode inbox --poll [--timeout 600]
  1. Record current trigger file mtime
  2. Loop:
     a. stat() trigger file
     b. If mtime changed or inbox has messages: read + consume inbox, print, exit
     c. Sleep 2s
     d. If timeout exceeded: exit with "no messages" status
```

The agent's shared prompt instructs it to keep a `muxcode inbox --poll` running whenever it finishes processing messages. This creates a continuous poll loop without send-keys.

### Message lifecycle states

Extend the `Message` struct with a `Status` field tracked in a separate delivery log:

| State | Meaning | Written by |
|-------|---------|-----------|
| `sent` | Message appended to recipient inbox | `Send()` |
| `delivered` | Message read from inbox by recipient | `Receive()` |
| `acknowledged` | Recipient confirms receipt (optional) | `muxcode ack <msg-id>` |
| `responded` | Response message sent with matching `reply_to` | `Send()` with `reply_to` |
| `expired` | TTL exceeded without delivery | Watcher cleanup |

#### Delivery tracking file

```
/tmp/muxcode-bus-{session}/delivery/{msg-id}.status
```

Each file contains a single JSON object:

```json
{
  "id": "1711324800-edit-a1b2c3d4",
  "status": "delivered",
  "sent_at": 1711324800,
  "delivered_at": 1711324805,
  "response_id": ""
}
```

`Send()` creates the status file as `sent`. `Receive()` updates it to `delivered`. When a response with matching `reply_to` is sent, the original's status updates to `responded` with the `response_id`.

### Request-response correlation

The existing `Message.ReplyTo` field already carries request IDs. Strengthen this:

1. `--wait` uses `ReplyTo` to match responses instead of matching by sender role
2. `Send()` returns the generated message ID for the caller to track
3. New `muxcode track <msg-id>` command shows the delivery status of a message

### Orchestrator pattern: task tracking

Add a task registry for the edit agent (or any orchestrator) to track delegated work:

```
/tmp/muxcode-bus-{session}/tasks/{msg-id}.json
```

```json
{
  "id": "1711324800-edit-a1b2c3d4",
  "to": "test",
  "action": "test",
  "payload": "Run tests and report results",
  "status": "in-flight",
  "sent_at": 1711324800,
  "timeout": 600,
  "response_id": "",
  "response_at": 0
}
```

Task states: `in-flight` | `completed` | `timed-out` | `failed`

New CLI commands:

| Command | Purpose |
|---------|---------|
| `muxcode tasks` | List all in-flight tasks for current role |
| `muxcode tasks --all` | List all tasks (including completed) |
| `muxcode track <msg-id>` | Show delivery + response status for a message |

The `--wait` flag automatically creates a task entry and updates it on response/timeout.

### Watcher changes

The watcher's `checkInboxes()` simplifies dramatically:

**Current**: Detect inbox growth -> check if idle -> send-keys or display-message -> handle cooldowns, passive retry, startup notifications

**New**: Detect inbox growth -> write trigger file -> done

Remove from watcher:
- `checkStartupNotifications()` -- replaced by agent-side polling
- Passive retry logic -- no longer needed
- `firstIdleSeen` / `allIdleSeen` tracking -- no longer needed

Keep in watcher:
- Inbox size tracking (for logging/monitoring)
- All other periodic checks (loops, compaction, cron, procs, agent health, ollama)

### Agent shared prompt changes

Update the shared agent prompt (built by `BuildSharedPrompt()` in `bus/launch.go`) to instruct agents:

1. After processing inbox messages, start `muxcode inbox --poll` as a background Bash tool
2. When the poll returns messages, process them immediately
3. If poll times out, restart it (the agent is idle and waiting for work)

This replaces the current instruction: "When you see 'You have new messages', run `muxcode inbox`"

## Notification strategy comparison

| Aspect | Current (send-keys) | Proposed (trigger file) |
|--------|---------------------|------------------------|
| Agent wake-up | External text injection into pane | Agent's own polling process |
| Race window | ~100-200ms between tool calls | None -- agent reads when ready |
| Active agent safety | Requires `IsAgentIdle()` check (TOCTOU) | Always safe -- file write only |
| Latency | ~50ms (immediate send-keys) | 1-2s (poll interval) |
| Reliability | Depends on TUI state, pane capture | Deterministic file I/O |
| Complexity | 6 marker files, 3 cooldown timers, startup retry logic | 1 trigger file per role |

The 1-2s latency increase is acceptable. Most agent tasks take 5-30 seconds. The reliability gain far outweighs the latency cost.

## Files to remove or simplify

| Current file/function | Disposition |
|----------------------|-------------|
| `IsAgentIdle()` | Keep for `agent-health` detection only, remove from notification path |
| `notifySendKeys()` | Remove entirely |
| `notifyIdleSendKeys()` | Remove entirely |
| `sendKeysCooldown` / `markSendKeys()` / `IsSendKeysCoolingDown()` | Remove |
| `SendKeysMarkerPath()` | Remove |
| `SetPassiveNotify()` / `ClearPassiveNotify()` / `HasPassiveNotify()` | Remove |
| `PassiveNotifyMarkerPath()` | Remove |
| `checkStartupNotifications()` in watcher | Remove |
| `firstIdleSeen` / `allIdleSeen` / `startupNotifyAt` / `startupRetries` | Remove from watcher struct |

## Migration path

### Phase 1: Add trigger file notification (non-breaking)

Add the trigger file write path alongside existing notification. Add `muxcode inbox --poll` command. No behavior change yet -- agents still use the old send-keys path.

| # | Change | File |
|---|--------|------|
| 1 | Add `TriggerNotifyPath()` to config | `bus/config.go` |
| 2 | Add trigger file write in `Notify()` (alongside existing logic) | `bus/notify.go` |
| 3 | Add `--poll` flag to inbox command | `cmd/inbox.go` |
| 4 | Add delivery status file creation in `Send()` | `bus/inbox.go` |
| 5 | Add delivery status update in `Receive()` | `bus/inbox.go` |
| 6 | Add `bus/delivery.go` for status tracking functions | `bus/delivery.go` |

### Phase 2: Agent prompt migration

Update agent prompts to use `muxcode inbox --poll` instead of waiting for send-keys. Both paths work during transition -- agents that haven't restarted still get send-keys, new agents use polling.

| # | Change | File |
|---|--------|------|
| 7 | Update shared prompt to use `--poll` pattern | `bus/launch.go` |
| 8 | Add task tracking on `--wait` | `cmd/send.go` |
| 9 | Add `muxcode tasks` command | `cmd/tasks.go` |
| 10 | Add `muxcode track` command | `cmd/track.go` |

### Phase 3: Remove send-keys notification

Once all agents use polling, remove the send-keys path entirely.

| # | Change | File |
|---|--------|------|
| 11 | Remove `notifySendKeys()`, `notifyIdleSendKeys()` | `bus/notify.go` |
| 12 | Remove send-keys marker files and cooldown logic | `bus/notify.go`, `bus/config.go` |
| 13 | Remove passive notify markers and retry logic | `bus/notify.go`, `bus/config.go` |
| 14 | Remove `checkStartupNotifications()` and related watcher state | `watcher/watcher.go` |
| 15 | Simplify `checkInboxes()` to just trigger file writes | `watcher/watcher.go` |
| 16 | Clean up `purgeStaleFiles()` for removed marker types | `bus/setup.go` |

### Phase 4: Hardening

| # | Change | File |
|---|--------|------|
| 17 | Add delivery status expiry/cleanup in watcher | `watcher/watcher.go` |
| 18 | Add `muxcode tasks` and `muxcode track` to CLI help | `main.go` |
| 19 | Update architecture.md, agent-bus.md, agents.md | `docs/` |
| 20 | Update CLAUDE.md notification documentation | `CLAUDE.md` |
| 21 | Add integration tests for poll-based notification | `bus/notify_test.go` |

## Success criteria

| Criterion | Measurement |
|-----------|-------------|
| Zero send-keys interruptions | No "Interrupted" messages in any agent conversation during a full build->test->review chain |
| Response reliability | 100% of `--wait` sends receive a response (no timeouts from notification failures) |
| Notification latency | Messages processed within 4s of delivery (2s poll interval + 2s margin) |
| Marker file reduction | From 6 marker file types (notified-size, passive-notify, sendkeys-ts, waiting, per role) to 1 (trigger-notify, per role) |
| Watcher simplification | `checkInboxes()` reduced to ~15 lines; `checkStartupNotifications()` removed entirely |
| No regressions | All existing tests pass; build->test->review chain completes end-to-end |

## Open questions

1. **Poll interval tuning**: 2s is conservative. Could use `kqueue` (macOS) / `inotify` (Linux) via Go's `os` package for near-instant detection, but adds complexity. Start with polling, optimize later if latency matters.

2. **Multiple concurrent polls**: If an agent starts `--poll` but then gets a direct `muxcode inbox` call (e.g., from a hook), the poll should detect the inbox was consumed and restart cleanly.

3. **Harness agents**: The LLM harness already polls inbox directly (`harness/bus.go:ConsumeInbox()`). No changes needed for harness-based agents -- the trigger file is an additional signal they can optionally watch.

4. **Edit agent user input**: When the human types at the edit agent's prompt, a `--poll` running as a Bash tool would still be "active" (the poll command is the current tool). The human's typed text goes to Claude Code's input, not to the Bash tool. This should work naturally but needs testing.
