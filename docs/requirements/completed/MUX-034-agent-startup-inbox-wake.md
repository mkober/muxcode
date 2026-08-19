# Agent startup inbox wake-up

Fix the reliability issue where agents fail to process pre-existing inbox messages after startup or restart. Messages sent to agents go undelivered — the daemon's wake-up mechanisms are insufficient for agents that launch into an idle state with a non-empty inbox.

## Problem

When an agent restarts (hot reload, crash recovery, or fresh launch into a session with existing messages), it reaches the idle prompt but never processes inbox messages. The `--wait` flag times out after 600s with no response.

### Root causes

1. **`checkInboxes()` only fires on inbox growth** — it compares `size > prev` where `prev` is the size at daemon startup or last check. If the inbox already had messages when the daemon started tracking, `checkInboxes()` never fires for those messages. After agent restart, the inbox size hasn't changed (the message was already written), so `checkInboxes()` doesn't trigger.

2. **`checkIdleAgents()` has notification dedup** — `alreadyNotified()` compares the current inbox size to the `notified-{role}.size` marker file. If a notification was sent before the agent restarted (matching the current inbox size), `alreadyNotified()` returns true and suppresses the wake-up for up to `notifyRetryInterval` (30s). Combined with the 5s poll interval, this means up to 35s delay.

3. **`NeedsWakeUp()` only covers `edit`** — the `AutoAccept()` startup flow only sends wake-up messages to the edit agent. All other agents (commit, build, test, plan, etc.) rely entirely on the daemon's `checkIdleAgents()` for their first wake-up after launch.

4. **Non-hook provider 60s cooldown** — for OpenCode/Codex agents, `checkIdleAgents()` applies a 60s cooldown between `SendWakeUp()` calls per role. If the first wake-up fires before the agent is ready (during the trust prompt or startup phase), the next attempt is 60s away.

### Observed behavior

- Send message to commit agent → commit agent restarts → message sits in inbox → `--wait` times out at 600s
- Agent is idle at `>` prompt with unread messages but daemon doesn't inject "You have new messages"
- Problem is intermittent but increasingly frequent

## Requirements

### Acceptance criteria

- [x] Agents process pre-existing inbox messages within 10s of reaching their idle prompt after startup
- [x] `checkIdleAgents()` clears stale `notified-{role}.size` markers when an agent transitions from non-idle to idle (agent restart detection)
- [x] `AutoAccept()` wakes all agents with pre-existing inbox messages, not just edit
- [x] Non-hook provider cooldown reset when agent restarts (stale cooldown from pre-restart state shouldn't apply)
- [x] Send-keys delivery is verified — if agent is still idle after injection, marker is cleared for immediate retry (worst case 10s vs 35s)
- [x] All existing tests pass (`go test ./...`)
- [x] New tests for startup inbox wake-up scenarios

### Out of scope

- Changing the notification dedup mechanism fundamentally
- Modifying the `--wait` polling mechanism
- Changing the daemon poll interval

## Technical approach

### Fix 1: Clear notified-size on agent idle transition

In `checkIdleAgents()`, track per-role "was idle last check" state. When an agent transitions from not-idle to idle (first time detected at `>` prompt), clear the `notified-{role}.size` marker. This ensures the next notification attempt won't be suppressed by stale dedup state from before the restart.

```go
// In checkIdleAgents():
isIdle := bus.IsAgentIdle(d.session, role)
wasIdle := d.lastIdleState[role]
if isIdle && !wasIdle {
    // Agent just became idle — clear stale notification marker
    bus.ClearNotifiedSize(d.session, role)
    d.lastNonHookWake[role] = 0 // reset cooldown too
}
d.lastIdleState[role] = isIdle
```

### Fix 2: Expand `AutoAccept()` wake-up to all agents with inbox

Change `NeedsWakeUp()` to return true for any agent that has actionable inbox messages, not just edit. When `AutoAccept()` detects an agent at the idle prompt, check if it has unread messages and wake it.

```go
func NeedsWakeUp(session, window string) bool {
    return bus.HasActionableMessages(session, window)
}
```

### Fix 3: Reset non-hook cooldown on idle transition

When `checkIdleAgents()` detects a non-hook agent's idle transition (first time idle), reset `d.lastNonHookWake[role]` to 0 so the wake-up fires immediately instead of waiting up to 60s.

### Fix 4: Verify send-keys delivery and clear marker on failure

`notifySendKeys()` calls `markNotified()` before `SendWakeUp()`, so even when Claude Code's TUI silently drops the send-keys injection, the system thinks it succeeded and won't retry for 30s (`notifyRetryInterval`). Fix: after `SendWakeUp()`, briefly wait and check if the agent is still idle. If it is, the injection was dropped — clear the notified-size marker so the daemon's next `checkIdleAgents()` cycle (5s) retries immediately.

```go
// In notifySendKeys(), after SendWakeUp:
markNotified(session, role)

provider := ResolveProvider(role)
err := provider.SendWakeUp(session, role)

// Verify delivery — send-keys can be silently dropped by TUI redraws.
// If the agent is still idle after a brief delay, the text didn't land.
// Clear the marker so the next daemon cycle retries immediately (5s)
// instead of waiting notifyRetryInterval (30s).
if err == nil && provider.SupportsHooks() {
    time.Sleep(500 * time.Millisecond)
    if provider.IsIdle(session, role) {
        ClearNotifiedSize(session, role)
    }
}
```

Worst-case latency drops from **35s to 10s** (5s daemon cycle + 500ms verify delay, with immediate retry on next cycle).

## Key files

| File | Change |
|------|--------|
| `tools/muxcode/daemon/daemon.go` | Add `lastIdleState` map, clear notified-size on idle transition, reset non-hook cooldown |
| `tools/muxcode/bus/launcher.go` | Change `NeedsWakeUp()` to check inbox for all agents |
| `tools/muxcode/bus/notify.go` | Add delivery verification in `notifySendKeys()`, clear marker on failed injection, retry logic |
| `tools/muxcode/bus/provider_claude.go` | Simplify `SendWakeUp()` — fewer tmux send-keys calls, longer text-to-Enter delay |
| `tools/muxcode/daemon/daemon_test.go` | Add tests for idle transition detection |
| `tools/muxcode/bus/notify_test.go` | Add tests for delivery verification (mock IsIdle) |

## Implementation

### Phase 1: Idle transition detection and stale marker cleanup

- [x] Add `lastIdleState map[string]bool` field to `Daemon` struct in `daemon.go`
- [x] In `checkIdleAgents()`, detect idle transitions (not-idle -> idle) and call `ClearNotifiedSize()` + reset `lastNonHookWake`
- [x] Add test for idle transition clearing notified-size marker (4 tests: transition clears, already-idle noop, becoming-non-idle noop, init check)
- [x] Export `NotifiedSizePath()` in `bus/notify.go` for daemon test access
- [x] Verify with manual test: sent message to commit agent, restarted daemon with new binary, agent woke and processed messages immediately

### Phase 2: Expand AutoAccept wake-up

- [x] Change `NeedsWakeUp()` to accept `session` and `window` params and check `HasActionableMessages()`
- [x] Update `AutoAccept()` call site to pass session
- [x] Update tests for `NeedsWakeUp()` — rewrote to test with actual inbox messages (request vs response)
- [x] Verify with manual test: launch session with pre-existing inbox messages, confirm all agents wake up (2026-05-08: startup-wake-full fired for plan, edit, commit — all processed inbox within seconds)

### Manual test observations (2026-05-07)

Fresh session launch with no pre-existing inbox messages:
- **Plan agent**: woke immediately — launcher sends a `context` request (type `request`) with the Neovim open file. `AutoAccept()` detected the pending message and woke it on startup. ✅
- **Edit agent**: only had a self-notification event (type `event`, action `notify`). `HasActionableMessages()` correctly returns false for events — no wake-up needed. Working as designed.
- **Commit agent**: empty inbox at startup — no messages to process. When a test message was sent post-startup (~3 min after launch), the `Notify()` from `cmd/send.go` fired but the send-keys injection didn't visibly reach the agent (no "You have new messages" in pane history). The daemon's `checkInboxes()` logged `inbox-notify` but the `notified-size` marker was already set from the first attempt, blocking retry for 30s (`notifyRetryInterval`). Agent eventually woke after the retry interval expired.

**Conclusion**: Phase 1 (idle transition detection) and Phase 2 (expanded `NeedsWakeUp`) are working correctly for the startup case. The remaining reliability gap is the send-keys injection being silently dropped by Claude Code's TUI — the 30s retry mechanism catches it but adds latency. Phase 3 addresses this.

### Manual test observations — Phase 3 regression (2026-05-07, session 16:13)

Phase 3 `verifySendKeysDelivery()` is not resolving the dropped send-keys problem in practice. Timeline from lifecycle logs:

| Time | Event | Detail |
|------|-------|--------|
| 16:13:23 | `launch` | commit agent launched |
| 16:13:24 | `agent-ready` | commit agent reached `❯` prompt |
| 16:13:26 | `idle-transition` | daemon cleared stale markers for commit (Phase 1 ✅) |
| 16:13:51 | `inbox-notify` | daemon notified commit — **27s after agent-ready** |
| 16:15+ | still idle | commit agent sitting at `❯` with unread test message |

**Observations**:
- `notified-commit.size` marker (203 bytes) matched inbox size (203 bytes) — daemon skipped re-notification
- `tmux capture-pane` confirmed commit agent idle at `❯` with no injected text visible
- Manual `tmux send-keys` to the same pane worked immediately — pane IS responsive
- After manual text injection + Enter, commit agent woke and processed the message within 3s

**Root cause**: `SendWakeUp()` sends Escape → C-u → text → (100ms) → Enter as 4 separate `tmux send-keys` commands. During TUI startup or redraws, one or more of these commands are silently dropped. The `verifySendKeysDelivery()` 500ms check then sees one of two outcomes:
1. Text was partially injected (visible at `❯ You hav...`) — `IsIdle()` returns true (matches `❯ ` prefix), marker is cleared, but the incomplete text + Enter never executed. Daemon retries on next cycle.
2. Text was fully injected but Enter was dropped — `IsIdle()` sees `❯ You have new messages` and returns true (matches `❯ ` prefix), marker is cleared. But Claude Code never processes the text because Enter didn't land. Daemon retries on next cycle, but the stale text is now in the input buffer — next `SendWakeUp()` sends Escape + C-u to clear it, which may itself get dropped.

In both cases, `verifySendKeysDelivery()` correctly clears the marker (agent IS still idle), but the retry suffers the same send-keys drop. The verification-and-retry loop runs every ~5.5s (5s daemon cycle + 500ms verify) but each attempt has the same probability of being dropped.

**Why manual injection works**: a single `tmux send-keys` with the text (no Escape/C-u/Enter ceremony) lands reliably. The multi-command sequence with sub-100ms gaps is what gets dropped.

### Phase 3: Verify send-keys delivery and clear marker on failure

- [x] In `notifySendKeys()`, after `SendWakeUp()` returns, wait 500ms and check `provider.IsIdle()` — if still idle, call `ClearNotifiedSize()` to allow immediate retry on next daemon cycle
- [x] Only verify for hook providers (`provider.SupportsHooks()`) — non-hook providers have their own delivery mechanism
- [x] Add tests: mock `IsIdle` to return true after `SendWakeUp` → verify marker is cleared; mock `IsIdle` to return false → verify marker persists; non-hook provider skips verification
- [x] Verify with manual test: sent message to idle commit agent at 16:10:42, received response at 16:10:48 (6s) — within 10s target ✅

### Phase 4: Reliable send-keys delivery

The multi-command `SendWakeUp()` sequence (Escape → C-u → text → Enter) is unreliable during TUI redraws. Fix by consolidating into fewer tmux send-keys calls and adding retry-with-backoff.

- [x] Refactor `ClaudeCodeProvider.SendWakeUp()` to send text and Enter in a single `tmux send-keys` call using literal-flag (`-l`) for the text portion, followed by a separate Enter. Remove the Escape/C-u preamble — the `verifySendKeysDelivery()` retry handles stale buffer text by detecting the agent is still idle.
- [x] Increase the delay between text and Enter from 100ms to 200ms to give the TUI more time to register the text before the Enter keypress
- [x] In `verifySendKeysDelivery()`, if the agent is still idle after 500ms, retry the `SendWakeUp()` once immediately (within the same call) before clearing the marker. This gives a second attempt within the same Notify() call rather than waiting for the next 5s daemon cycle.
- [x] Add test for retry behavior: mock `IsIdle` to return true on first check, false on second → verify single retry fires
- [x] Manual verification: confirmed reliable delivery in current session — idle-transition detection + startup-wake-full + delivery retry all firing correctly (2026-05-08 session lifecycle logs)

## Status

Complete
