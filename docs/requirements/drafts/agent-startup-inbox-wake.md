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

- [ ] Agents process pre-existing inbox messages within 10s of reaching their idle prompt after startup
- [ ] `checkIdleAgents()` clears stale `notified-{role}.size` markers when an agent transitions from non-idle to idle (agent restart detection)
- [ ] `AutoAccept()` wakes all agents with pre-existing inbox messages, not just edit
- [ ] Non-hook provider cooldown reset when agent restarts (stale cooldown from pre-restart state shouldn't apply)
- [ ] All existing tests pass (`go test ./...`)
- [ ] New tests for startup inbox wake-up scenarios

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

## Key files

| File | Change |
|------|--------|
| `tools/muxcode/daemon/daemon.go` | Add `lastIdleState` map, clear notified-size on idle transition, reset non-hook cooldown |
| `tools/muxcode/bus/launcher.go` | Change `NeedsWakeUp()` to check inbox for all agents |
| `tools/muxcode/bus/notify.go` | No changes needed (existing `ClearNotifiedSize` is sufficient) |
| `tools/muxcode/daemon/daemon_test.go` | Add tests for idle transition detection |

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
- [ ] Verify with manual test: launch session with pre-existing inbox messages, confirm all agents wake up

## Status

In Progress
