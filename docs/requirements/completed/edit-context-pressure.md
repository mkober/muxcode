# Edit agent context pressure from notification storms

Reduce context window consumption on the edit agent caused by excessive auto-CC messages, repeated chain echoes, and loop-detection noise accumulating in the edit inbox during normal multi-agent operation.

## Context

### Current state

| Aspect | Current behavior |
|--------|-----------------|
| Auto-CC | Messages from build, test, review, deploy, and analyze are copied to edit inbox |
| Chain echoes | Each chain step (build→test→review) generates CC'd messages to edit at every hop |
| Loop detection | `loop-detected` events are sent to edit as `event` type messages |
| Daemon notifications | `agent-down`, `agent-restarting`, `agent-recovered`, `agent-reloaded` all land in edit inbox |
| Watch chain | `run→watch` chain fires on every successful run command, including `muxcode inbox` reads |
| Safety net retries | Capped at 3, but each retry injects "You have new messages" text that adds to context |
| Dedup | Message-level dedup exists but doesn't prevent CC copies or event-type messages |

### Problem

During a typical development session with active chains, the edit agent's context window fills with low-value messages:

1. **Chain echo amplification** — A single build triggers build→test→review. Each hop generates a CC to edit. A failed test that triggers a code fix and rebuild creates 6+ CC messages per cycle. Multiple iterations exhaust the edit context window.

2. **Run→watch loop** — The `run` event chain triggers `watch` on *any* successful command, including `muxcode inbox` reads. The watch agent finishes, its chain sends back to edit, the run agent reads its inbox (success), chains to watch again. This creates a sustained loop that was observed firing 5+ times in under 5 minutes.

3. **Informational noise** — `loop-detected`, `agent-down`, `agent-restarting`, `agent-recovered` events are useful once but redundant on repeat. During the observed session, the edit agent received 13 messages in a single inbox read — most were noise.

4. **Context window exhaustion** — Claude Code's context window is finite. Each injected message consumes tokens. The accumulated system-reminder tags from notification injections compound the problem. In the observed session, the edit agent's context filled and Claude had to be resumed — losing conversational state and requiring manual re-orientation.

### Observed impact

In a 2-hour session on 2026-06-01:
- 15+ `inbox-notify` events for edit in 30 minutes
- 4x `loop-detected` events (test↔review, run↔watch, edit↔run, watch↔edit)
- Run→watch chain fired 5+ times on `muxcode inbox` reads
- Edit agent context exhausted, requiring session resume
- User lost conversational context and had to re-establish state

### Goal

1. Reduce the volume of messages landing in the edit inbox by 50%+ during chain-heavy workflows
2. Prevent run→watch chain from firing on non-deployment commands
3. Deduplicate or suppress repeated informational events (loop-detected, agent health)
4. Add a context budget awareness mechanism so the daemon can throttle notifications when edit is under pressure

## Design

### 1. Run chain command filtering

The `run` event chain currently fires `watch` on any successful command. Add a condition to only trigger watch for actual deployment or service-start commands:

```json
{
  "on_success": {
    "send_to": "watch",
    "action": "watch",
    "message": "Run succeeded (${command}) — tail logs",
    "type": "request",
    "conditions": {
      "output_contains": ["deploy", "invoke", "start", "restart", "exec"]
    }
  }
}
```

This prevents `muxcode inbox`, `muxcode send`, and other bus commands from triggering the watch chain.

### 2. Auto-CC message coalescing

Instead of CC'ing every individual chain message to edit, coalesce chain sequences into a single summary:

- When a chain is in progress (build→test→review), suppress intermediate CC messages
- When the chain completes (final hop responds to edit), include a one-line chain summary
- Example: instead of 3 separate CC messages, one message: "Chain: build OK → test OK → review complete (2 findings)"

Implementation options:
- **Chain-aware CC suppression** — the daemon tracks active chains and holds CC messages until the chain terminates
- **CC rate limiting** — max 1 CC per role per 60-second window, latest message wins
- **Chain summary mode** — new `auto_cc_mode` config: `"all"` (current), `"summary"` (coalesced), `"none"`

### 3. Event dedup for edit inbox

Repeated informational events should be deduplicated or suppressed for the edit inbox:

| Event | Current | Proposed |
|-------|---------|----------|
| `loop-detected` | Every detection sent to edit | First occurrence + count summary after resolution |
| `agent-down` | Sent immediately | Sent immediately (keep — actionable) |
| `agent-restarting` | Sent per attempt | Suppress if `agent-recovered` follows within 60s |
| `agent-recovered` | Sent on recovery | Combine with restart: "agent-down → recovered (attempt 1)" |
| `agent-reloaded` | Sent per reload | Keep (user-initiated, low frequency) |

### 4. Context pressure detection

Add a mechanism for the daemon to detect when the edit agent is under context pressure and throttle notifications:

- **Compaction proximity** — `CheckRoleCompaction()` already tracks conversation size. When edit is above 80% of its compaction threshold, the daemon should:
  - Suppress event-type messages (informational only)
  - Hold CC messages (deliver on next idle transition after compaction)
  - Only deliver request-type messages addressed directly to edit
- **Notification budget** — configurable max notifications per 5-minute window (default: 10). After the budget is exhausted, only direct requests are delivered. Budget resets on idle transition.

### 5. Chain outcome events

Replace individual CC messages with a single chain outcome event:

```
Chain build→test→review completed: build OK (2.1s) → test OK (4.3s) → review: 2 findings
```

This requires the daemon to track chain state across hops — it already has the `EventChains` config and sees each message flow through `checkInboxes`.

## Requirements

### Acceptance criteria

- [x] Run→watch chain only fires for deployment/service commands, not inbox reads
- [x] Auto-CC messages are rate-limited (1 per role per 60s)
- [x] Repeated `loop-detected` events are deduplicated (first + summary)
- [x] `agent-restarting` + `agent-recovered` pairs are combined when recovery is fast
- [x] Notification budget (configurable) caps event delivery to edit (15 per 5-min window)
- [x] Edit agent context window survives a full build→test→review cycle without exhaustion
- [x] All existing chain behavior preserved for non-edit agents
- [x] Existing tests pass (no regressions)

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/conditions.go` | `command_match` and `command_not_match` condition types |
| `tools/muxcode/bus/conditions_test.go` | Tests for command conditions and run chain filtering |
| `tools/muxcode/bus/profile.go` | Run event chain `command_not_match` condition |
| `tools/muxcode/bus/inbox.go` | Auto-CC rate limiting (`shouldAutoCC()`) |
| `tools/muxcode/daemon/daemon.go` | Event dedup (`shouldSendEvent()`), notification budget (`shouldNotifyEdit()`) |

## Implementation

### Phase 1: Run chain command filtering
- [x] Add `command_not_match` condition type to `conditions.go` (parallels `branch_not_match`)
- [x] Add `command_match` condition type to `conditions.go`
- [x] Add `command_not_match: "muxcode *"` to `run.on_success` chain in `DefaultConfig()`
- [x] `muxcode inbox` reads no longer trigger run→watch chain
- [x] Unit tests: `TestCommandMatch`, `TestCommandNotMatch`, `TestRunChainSkipsMuxcodeCommands`

### Phase 2: Event dedup for edit
- [x] Add `shouldSendEvent()` helper with `lastEventSent` tracking (5-minute window per action+key)
- [x] `loop-detected`: deduped per role — first occurrence sent, repeats suppressed for 5 min
- [x] `agent-restarting` + `agent-recovered`: share `agent-health` dedup key — at most one per 5-min window per role
- [x] Lifecycle logging preserved (always logs regardless of dedup)

### Phase 3: Auto-CC rate limiting
- [x] Add `shouldAutoCC()` rate limiter in `inbox.go` — max 1 CC per role per 60s
- [x] CC messages from build/test/review/deploy/analyze throttled to edit
- [x] Direct messages to edit unaffected (not CC, delivered normally)
- [x] Rate limit state in `autoCCLastSent` map

### Phase 4: Notification budget
- [x] Add `shouldNotifyEdit()` budget gate in daemon — caps event messages per 5-min window
- [x] Default budget: 15 events per window (configurable via `MUXCODE_EDIT_NOTIFY_BUDGET`)
- [x] Request-type messages bypass budget (edit always receives direct asks)
- [x] Budget resets on edit idle transition and window expiry
- [x] Applied to loop-detected events (primary offender)
- [x] `resetEditBudget()` called on edit idle transition

### Phase 5: Integration test
- [x] Create `scripts/test-context-pressure.sh` — 12 assertions across 4 test groups
- [x] Fix `cmd/chain.go` to populate `ctx.Command` from `--command` flag (conditions were not evaluated)
- [x] Run chain exits 2 (no match) for `muxcode inbox` and `muxcode send` commands
- [x] Run chain fires watch for `aws lambda invoke` and `bash scripts/deploy.sh`
- [x] Verbose mode shows `command_not_match` condition evaluation
- [x] Auto-CC rate limited (≤1 CC per role per 60s)
- [x] Duplicate messages suppressed by dedup
- [x] Notification budget env var accepted (valid and invalid values)

## Status

Complete — all 5 phases implemented and verified
