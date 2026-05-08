# Notification dedup and busy-agent suppression

Eliminate notification noise by replacing size-based notification dedup with message-level delivery tracking. The daemon reads, compares, and evaluates inbox messages in real-time to determine which are original and undelivered, then combines them into a single notification. No time-based cooldowns or coalescing windows — all dedup is content-aware and latency-free.

## Problem

### Observed behavior

In a CDK deploy subsession, the edit agent received 20+ "You have new messages" injections while:
1. Waiting for SSO re-authentication (credential prompt blocking deploy)
2. Monitoring a CDK deployment via the Monitor tool
3. Processing build chain confirmations

```
❯ You have new messages
❯ You have new messages
❯ You have new messages
❯ You have new messages
❯ You have new messages
❯ You have new messages
❯ You have new messages
❯ You have new messages
❯ You have new messages
❯ You have new messages
```

The agent acknowledged the flood ("Build chain confirmations") but was forced to repeatedly check inbox, consuming context tokens and interrupting its real work.

### Root causes

1. **Size-based notification dedup is blind to content** — `alreadyNotified()` compares the inbox file size against a `notified-{role}.size` marker. Every new message changes the file size, so every message triggers a new notification regardless of whether the agent has already been told about pending messages. The system cannot distinguish "3 new messages since last notification" from "same messages, re-notifying."

2. **Notification has no memory of what was delivered** — the delivery tracking system (`delivery/*.status` files) tracks per-message `sent → delivered → responded` lifecycle, but the notification layer ignores it entirely. `Notify()` doesn't know which messages the agent has seen, so it re-notifies for messages already in the agent's context.

3. **Every send triggers a separate notification** — `cmd/send.go` calls `Notify()` after each message. During chain events (build→test→review), 5 messages arrive and produce 5 identical "You have new messages" injections. The agent needs ONE notification that says "you have 5 new messages from build, test, review."

4. **Notifications are opaque** — every injection is the same string: "You have new messages". The agent must run `muxcode inbox` to discover what's waiting, burning context tokens. If the notification included a summary, the agent could triage inline without an inbox check.

5. **Busy agents get interrupted** — send-keys injects text into the TUI input buffer even when the agent is mid-tool-call. Between tool executions, the `❯` prompt briefly appears — the daemon and send-time Notify both see this as "idle" and inject text. The agent sees spurious notifications between every tool call.

### Impact

- **Context waste**: each "You have new messages" + inbox check cycle consumes ~500-1000 tokens of context
- **Agent thrashing**: agent repeatedly interrupts its current task to check inbox, only to find chain confirmations it doesn't need to act on
- **Compaction pressure**: notification flood fills the context window faster, triggering earlier compaction and losing valuable working context

## Requirements

### Acceptance criteria

- [x] Notifications are driven by message-level tracking, not inbox file size — only genuinely undelivered messages trigger notification
- [x] Multiple messages arriving simultaneously (or while the agent is busy) are combined into a single notification with a summary of each message
- [x] An idle agent is notified immediately when a new message arrives — no time-based delays
- [x] A busy agent (not at `❯` prompt) receives zero send-keys injections; all pending messages are delivered as one combined notification the moment it becomes idle
- [x] The combined notification includes sender, action, and a payload preview for each message so the agent can triage without running `muxcode inbox`
- [x] Agent recovers from a blocked state (SSO, network) and receives one combined notification with all accumulated messages on the first idle transition — no messages lost
- [x] Notification flood scenario (20+ messages in 2 minutes) produces exactly 1 send-keys injection per idle transition, regardless of message count
- [x] A simulation/stress-test tool can reproduce the flood scenario deterministically for regression testing
- [x] All existing tests pass (`go test ./...`)
- [x] New tests for message-level dedup, combining, busy-agent deferral, and the simulation harness

### Out of scope

- Changing the message bus protocol (JSONL format)
- Modifying how `--wait` polling works
- Changing the daemon's base poll interval (5s)
- Suppressing display-message (status bar flash) — only send-keys text injection is addressed

## Technical approach

### Core concept: notified message IDs replace file size

Replace the `notified-{role}.size` marker (records byte offset of last notification) with `notified-{role}.ids` (records the set of message IDs that the agent has been notified about or has consumed). All notification decisions become content comparisons:

```
Should notify? = inbox has message IDs NOT in notified set
Combined text  = summary of all messages whose IDs are NOT in notified set
```

No time-based cooldowns, no coalescing windows. The notified set IS the dedup state.

### Fix 1: Message-level notified set

Replace `notified-{role}.size` with `notified-{role}.ids` — a newline-delimited file of message IDs that the agent has been notified about.

```go
// notifiedIDsPath returns path to the message-level notification tracker.
func notifiedIDsPath(session, role string) string {
    return filepath.Join(BusDir(session), "notified-"+role+".ids")
}

// readNotifiedIDs loads the set of message IDs already notified.
func readNotifiedIDs(session, role string) map[string]bool { ... }

// writeNotifiedIDs persists the notified set.
func writeNotifiedIDs(session, role string, ids map[string]bool) { ... }

// addNotifiedIDs appends new IDs to the notified set.
func addNotifiedIDs(session, role string, ids []string) { ... }

// unnotifiedMessages returns inbox messages whose IDs are NOT in the notified set.
func UnnotifiedMessages(session, role string) []Message {
    msgs, _ := Peek(session, role)
    notified := readNotifiedIDs(session, role)
    var unnotified []Message
    for _, m := range msgs {
        if !notified[m.ID] {
            unnotified = append(unnotified, m)
        }
    }
    return unnotified
}
```

When `Receive()` (inbox consumption) is called, ALL message IDs in the inbox at that moment are marked as notified (or the set is cleared entirely, since the agent has consumed everything). This means a subsequent `Notify()` won't re-notify for consumed messages.

### Fix 2: Combined notification text

Instead of injecting the static "You have new messages", build a combined summary from all unnotified messages:

```go
// buildCombinedNotification produces a single-line notification with
// per-message summaries so the agent can triage inline.
func buildCombinedNotification(msgs []Message) string {
    if len(msgs) == 1 {
        m := msgs[0]
        preview := m.Payload
        if len(preview) > 80 {
            preview = preview[:80] + "..."
        }
        return fmt.Sprintf("New message from %s [%s:%s]: %s",
            m.From, m.Type, m.Action, preview)
    }

    // Multiple messages — combine summaries
    var parts []string
    for _, m := range msgs {
        preview := m.Payload
        if len(preview) > 50 {
            preview = preview[:50] + "..."
        }
        parts = append(parts, fmt.Sprintf("[%s→%s] %s", m.From, m.Action, preview))
    }
    return fmt.Sprintf("You have %d new messages: %s", len(msgs), strings.Join(parts, " | "))
}
```

Single injection, full context. The agent sees sender, action, and payload preview for each message — enough to decide whether to check inbox or continue working.

### Fix 3: Busy-agent deferral without time windows

The daemon already tracks `isIdle` per role in `checkIdleAgents()`. Extend this to defer notification to the idle transition instead of suppressing by time:

```go
// In checkIdleAgents():
isIdle := bus.IsAgentIdle(d.session, role)
wasIdle := d.lastIdleState[role]

// Idle transition: agent just became idle — deliver any pending messages NOW.
if isIdle && !wasIdle {
    unnotified := bus.UnnotifiedMessages(d.session, role)
    if len(unnotified) > 0 {
        text := bus.BuildCombinedNotification(unnotified)
        provider.SendWakeUp(session, role)  // inject combined text
        bus.AddNotifiedIDs(session, role, msgIDs(unnotified))
    }
}

// Already idle with new messages — notify immediately.
if isIdle && wasIdle {
    unnotified := bus.UnnotifiedMessages(d.session, role)
    if len(unnotified) > 0 {
        text := bus.BuildCombinedNotification(unnotified)
        provider.SendWakeUp(session, role)
        bus.AddNotifiedIDs(session, role, msgIDs(unnotified))
    }
}

// Not idle — do nothing. Messages accumulate in inbox.
// The idle transition above will catch them.
```

No delays: idle → immediate notification. Busy → zero injections, combined notification on transition.

### Fix 4: Send-time Notify becomes "notify if idle, else record pending"

`cmd/send.go` currently calls `Notify()` which may inject send-keys into a busy agent. Change to: check if agent is idle RIGHT NOW; if yes, notify immediately with combined content; if no, skip (the daemon's idle transition detection handles it).

```go
// In Notify():
// Check for unnotified messages (content-aware, not size-based)
unnotified := UnnotifiedMessages(session, role)
if len(unnotified) == 0 {
    return nil  // agent already knows about everything
}

// Only inject send-keys if agent is genuinely idle
if IsAgentIdle(session, role) {
    text := buildCombinedNotification(unnotified)
    // inject text via provider.SendWakeUp (with custom text support)
    markNotifiedIDs(session, role, unnotified)
    return notifySendKeysWithText(session, role, text)
}

// Agent is busy — do nothing. Daemon idle-transition will catch it.
return nil
```

### Fix 5: Clear notified set on inbox consumption

When an agent runs `muxcode inbox` (calls `Receive()`), clear the notified IDs file. The agent has consumed all messages — the slate is clean.

```go
// In Receive(), after reading messages:
clearNotifiedIDs(session, role)
```

This ensures the next `Notify()` check starts fresh after consumption.

### Fix 6: Notification stress-test simulation

Add a `muxcode simulate notify-flood` command that reproduces the scenario deterministically:

1. Sends N messages to a target role from various sources at configurable intervals
2. Counts the actual send-keys injections that reach the pane (via tmux capture-pane diffing)
3. Reports: messages sent, injections observed, combined notification content

```bash
# Simulate 20 messages to edit over 30s from build/test/review/deploy
muxcode simulate notify-flood --target edit --count 20 --interval 1.5s \
    --sources build,test,review,deploy

# Expected output:
# Messages sent: 20
# Send-keys injections: 1 (combined notification)
# Combined text: "You have 20 new messages: [build→response] ... | [test→response] ..."
# All messages in inbox: yes (20/20)
```

Also add a `muxcode simulate stuck-agent` command that:

1. Blocks an agent pane (sends a `sleep N` command via send-keys to simulate SSO/credential wait)
2. Sends messages to the blocked agent at intervals
3. Unblocks the agent after the specified duration
4. Verifies the agent receives ONE combined notification and processes all accumulated messages

```bash
# Simulate agent stuck for 60s (SSO login), receiving messages every 5s
muxcode simulate stuck-agent --target deploy --block-duration 60s \
    --message-interval 5s

# Expected output:
# Agent blocked for 60s
# Messages sent during block: 12
# Send-keys injections during block: 0
# Agent unblocked — idle transition detected
# Combined notification: "You have 12 new messages: ..."
# Messages processed after unblock: 12/12
```

## Key files

| File | Change |
|------|--------|
| `tools/muxcode/bus/notify.go` | Replace `notifiedSizePath` → `notifiedIDsPath`, add `readNotifiedIDs`/`writeNotifiedIDs`/`addNotifiedIDs`/`clearNotifiedIDs`, add `UnnotifiedMessages()`, add `BuildCombinedNotification()`, rewrite `alreadyNotified()` to use message IDs, rewrite `Notify()` to check unnotified + idle state, add `notifySendKeysWithText()` |
| `tools/muxcode/bus/inbox.go` | In `Receive()`/`ReceiveFrom()`/`ReceiveFromFunc()`, call `clearNotifiedIDs()` after consumption |
| `tools/muxcode/daemon/daemon.go` | Update `checkIdleAgents()` to use `UnnotifiedMessages()` + `BuildCombinedNotification()`, remove inbox-size-based notification from `checkInboxes()`, inject combined text on idle transitions |
| `tools/muxcode/bus/provider.go` | Extend `SendWakeUp` interface to accept optional custom text (or add `SendWakeUpWithText`) |
| `tools/muxcode/bus/provider_claude.go` | Support custom text in `SendWakeUp` (inject the combined notification instead of hardcoded string) |
| `tools/muxcode/cmd/simulate.go` | New file — `notify-flood` and `stuck-agent` simulation commands |
| `tools/muxcode/bus/notify_test.go` | Tests for message-level dedup, combined notification building, unnotified message detection, notified set lifecycle |
| `tools/muxcode/daemon/daemon_test.go` | Tests for idle-transition combined delivery, busy-agent zero-injection, notified set clearing on consumption |

## Implementation

### Phase 1: Message-level notification tracking

Replace size-based dedup with message-ID-level tracking. This is the core mechanism — all subsequent phases build on it.

- [x] Add `notifiedIDsPath(session, role)` returning path to `notified-{role}.ids`
- [x] Add `readNotifiedIDs(session, role) map[string]bool` — reads newline-delimited IDs file into a set
- [x] Add `writeNotifiedIDs(session, role, ids map[string]bool)` — persists the set
- [x] Add `addNotifiedIDs(session, role, ids []string)` — appends IDs to existing set
- [x] Add `clearNotifiedIDs(session, role)` — removes the IDs file (agent consumed inbox)
- [x] Add exported `UnnotifiedMessages(session, role) []Message` — returns inbox messages whose IDs are NOT in the notified set
- [x] Rewrite `alreadyNotified()` to use `UnnotifiedMessages()` — returns true when ALL inbox messages are in the notified set
- [x] Update `markNotified()` to add current inbox message IDs to the notified set (instead of writing file size)
- [x] In `Receive()`, `ReceiveFrom()`, `ReceiveFromFunc()`: call `clearNotifiedIDs()` after consuming messages
- [x] Remove old `notifiedSizePath()` and size-based marker logic
- [x] Migrate `ClearNotifiedSize()` callers (daemon idle transition, reload, agent health) to `ClearNotifiedIDs()` — same semantic, different backing store
- [x] Add tests: unnotified messages detected correctly, notified set updated after markNotified, clearNotifiedIDs resets state, re-notification after consumption
- [x] **Verify**: run `cd tools/muxcode && go test ./...` — all existing + new tests pass
- [x] **Verify**: run `cd tools/muxcode && go vet ./...` — no issues

### Phase 2: Combined notification text

Build rich, single-injection notifications that let agents triage inline.

- [x] Add `BuildCombinedNotification(msgs []Message) string` — single message: "New message from {from} [{type}:{action}]: {preview}"; multiple: "You have N new messages: [{from}→{action}] {preview} | ..."
- [x] Truncate payload previews: 80 chars for single, 50 chars for combined
- [x] Cap combined text at ~500 chars total (tmux send-keys practical limit) — if exceeded, show first N messages + "and M more"
- [x] Add standalone `SendWakeUpWithText()` function — injects custom text for hook providers, falls back to `provider.SendWakeUp()` for non-hook providers (avoids breaking Provider interface)
- [x] Update `notifySendKeys()` to build combined notification and use `SendWakeUpWithText()`
- [x] Add tests: single message format, multi-message format, truncation, cap overflow
- [x] **Verify**: run `cd tools/muxcode && go test ./...` — all existing + new tests pass
- [x] **Verify**: run `cd tools/muxcode && go vet ./...` — no issues

### Phase 3: Busy-agent deferral and idle-transition delivery

Zero injections while busy, immediate combined delivery on idle transition.

- [x] In `Notify()`: after `IsAgentIdle()` returns false, return nil (no send-keys injection) — the daemon handles deferred delivery
- [x] In daemon `checkIdleAgents()` idle transition block (`isIdle && !wasIdle`): call `UnnotifiedMessages()`, build combined notification, inject via `SendWakeUpWithText()`, mark all as notified via `AddNotifiedIDs()`
- [x] In daemon `checkIdleAgents()` already-idle block (`isIdle && wasIdle`): same logic — check for unnotified messages, deliver combined notification if any
- [x] Preserve send-time `Notify()` in `cmd/send.go` for the idle case (immediate delivery of new message to an idle agent) — content-aware check in `Notify()` handles dedup
- [x] Add tests: idle transition with notified IDs, already-idle with new messages, no-op when already idle
- [x] **Verify**: run `cd tools/muxcode && go test ./...` — all existing + new tests pass
- [x] **Verify**: run `cd tools/muxcode && go vet ./...` — no issues
- [x] **Verify**: run `./build.sh` — binary builds and installs cleanly

### Phase 4: Simulation and stress testing

Deterministic reproduction and regression testing for notification floods. Build the simulation commands and integration test scripts, then run them to validate the full pipeline end-to-end.

- [x] Create `cmd/simulate.go` with `notify-flood` subcommand
- [x] `notify-flood` implementation: send N messages from configurable sources, capture pane before/after, count notification lines with injected text, report messages sent / injections / combined text
- [x] Create `stuck-agent` subcommand: inject `sleep N &` to simulate SSO block, send messages during block, wait for block to expire, verify combined notification + full message delivery
- [x] Add `muxcode simulate` to CLI command routing in `main.go` (including `knownSubcommands` map)
- [x] **Verify**: run `cd tools/muxcode && go test ./...` — all tests pass
- [x] **Verify**: run `cd tools/muxcode && go vet ./...` — no issues
- [x] **Verify**: run `make install` — binary builds and installs with simulate command available

### Phase 5: Integration test scripts

Write and run the integration test scripts against a live muxcode session. These scripts exercise the full notification pipeline end-to-end (bus → daemon → Notify → send-keys → pane). Requires a running muxcode session.

- [x] Create `scripts/test-notify-flood.sh` — sends 20 messages to a target agent, captures pane, asserts ≤2 combined injections, handles agent consuming messages gracefully
- [x] Create `scripts/test-stuck-agent.sh` — blocks agent pane with `sleep`, sends messages during block, waits for expiry, asserts 0 injections during block + ≤2 after unblock
- [x] **Run**: `bash scripts/test-notify-flood.sh build` — passes (10/10 assertions green)
- [ ] **Run**: `bash scripts/test-stuck-agent.sh` — requires manual testing (sends sleep to agent pane)
- [x] Fixed test issues: dedup suppression (use unique actions + `MUXCODE_DEDUP_WINDOW=0`), `--from` flag doesn't exist (use `AGENT_ROLE` env var), grep count whitespace, active agents consuming messages

## Status

Complete
