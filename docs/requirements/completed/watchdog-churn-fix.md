# Watchdog Churn Fix

Stop the long-active watchdog from driving a self-sustaining, token-burning re-wake loop. A daemon hardening fix (three independent guards A/B/C), not a feature — it caps the long-active advisory ladder, bounds the wake-up notification size, and self-heals the force-wake path.

## Context

### Problem (observed in a live session)

A review agent burned tokens for ~100 minutes in a self-sustaining re-wake loop. The failure chained four independent behaviors into a feedback loop:

1. **Advisory ladder** — `checkActiveWatchdog` (`daemon/daemon.go`) re-fires every ~10 min while an agent reads as "active", appending a **new** `long-active` advisory each time → 12 messages piled up in the inbox (a 10m→100m ladder).
2. **Blob notification** — `BuildCombinedNotification` (`bus/notify.go`) concatenates every message subject into `"You have N new messages: […] | […] | …"` — a ~450-char blob.
3. **Parked blob → idle misdetection** — each daemon re-notify injects that blob; a dropped Enter leaves it **parked** in the composer. The long parked text wraps past `IsAgentIdle`'s 8-line capture window, so the agent reads as "active" while the pane actually shows `❯`.
4. **Uncapped force-wake** — that routes into the "active-but-`❯` force-wake" path (`checkIdleAgents`, `daemon/daemon.go`) which re-injects the blob every 5s poll. Each re-wake is a full agent turn = token churn.

### Root cause

Three independent guards were missing. No single one is the "bug"; the loop only sustains because all three are absent at once:

| Gap | Consequence |
|-----|-------------|
| Long-active watchdog re-nudges indefinitely (every `threshold`) | Unbounded advisory ladder feeds the inbox |
| Wake-up notification concatenates all subjects with no length cap | Blob wraps past the idle-detection window → idle misdetection |
| Force-wake path has no retry cap | Re-injects every poll while the misdetection persists → token churn |

### Goal

Break the loop by adding a cap at each of the three points, so no single misdetection can sustain a re-wake storm:

- **Fix A** — cap long-active advisories per active-episode
- **Fix B** — bound the wake-up notification size so it can never wrap past the idle window
- **Fix C** — self-healing cap on the force-wake path (stop, clear, suppress)

## Design

### Fix A — cap the long-active watchdog per active-episode

`checkActiveWatchdog()` (`daemon/daemon.go:813`) previously re-nudged every `threshold` (600s) indefinitely.

- Add `activeNudgeCount map[string]int` field (`daemon/daemon.go:101`), initialized in `New()` (`daemon/daemon.go:187`).
- Stop after `activeWatchdogMaxNudges()` (`daemon/daemon.go:794`) — env `MUXCODE_ACTIVE_WATCHDOG_MAX_NUDGES`, default 2. Cap checked at `daemon.go:873` (`activeNudgeCount[role] >= activeWatchdogMaxNudges()`), incremented at `daemon.go:877`.
- **`0` = never nudge** — a value of `0` disables long-active advisories entirely (the cap check is a plain `>=`, so `0` stops before the first nudge). Any value `< 0` or non-numeric falls back to the default 2.
- Reset the counter to 0 wherever `activeSince[role]` is reset — idle transition, reload, and wait/poll.
- **Effect:** at most 2 advisories per stuck-episode instead of one every ~10 min (or zero with `MUXCODE_ACTIVE_WATCHDOG_MAX_NUDGES=0`).

### Fix B — bound the wake-up notification size

`BuildCombinedNotification()` (`bus/notify.go:135`) now tiers the wake-up text by message count and hard-caps its length:

| Count | Output |
|-------|--------|
| 0 | `"You have new messages"` |
| 1 | `"New message from <from> [<type>:<action>]: <payload≤80>"` |
| 2–3 (`≤ notifyMaxSubjects`) | Compact enumerated subjects (`[from>action] preview≤50`), joined by ` \| `, hard-capped at `notifyMaxLen` (200 chars) with `"and N more"` overflow |
| `> notifyMaxSubjects` (3) | **Short fixed string** — `"You have N new messages. Run: muxcode inbox"` (no subject concatenation) |

- Constants: `notifyMaxSubjects = 3` (`bus/notify.go:124`), `notifyMaxLen = 200` (`bus/notify.go:128`).
- **Effect:** an injected wake-up can never wrap past the idle-detection window and become a disruptive parked blob.

### Fix C — self-healing cap on the force-wake path

The "active but `❯` found in wider capture → force deliver" branch of `checkIdleAgents()` (`daemon/daemon.go:1544`, cap block `1736`–`1791`) previously re-injected every poll while the misdetection persisted.

- Add `forceWakeCount map[string]int` (`daemon.go:60`) and `churnSuppressed map[string]bool` (`daemon.go:61`), both initialized in `New()` (`daemon.go:168`–`169`).
- After `churnForceWakeCap` (`daemon.go:2421`, default 3) consecutive force-wakes without the inbox draining:
  - Clear the composer once (`TmuxClearInput`, guarded by pending-input + not-focused checks).
  - Set `churnSuppressed[role] = true` so the daemon backs off (permission-block-watchdog style) until the agent genuinely idles / the inbox drains.
  - Log a `churn-suppress` lifecycle event and send a **`churn-suppressed` event to edit** (`bus.NewMessage("daemon", "edit", "event", "churn-suppressed", …)`, `daemon.go:1769`) — once per suppression, kept quiet to avoid new noise.
- Reset via the `resetChurnGuard(role)` helper (`daemon.go:2436` — clears `forceWakeCount[role]` and `delete`s `churnSuppressed[role]`), called on idle transition and inbox drain (`daemon.go:1649`, `1801`).
- **Effect:** the force-wake path stops after 3 attempts and self-heals instead of looping every poll.

### Key files

| File | Change | Fix |
|------|--------|-----|
| `daemon/daemon.go` | `activeNudgeCount` field + init, `activeWatchdogMaxNudges()`, cap in `checkActiveWatchdog()`, counter resets | A |
| `bus/notify.go` | `BuildCombinedNotification()` tiering + `notifyMaxSubjects`/`notifyMaxLen` caps | B |
| `daemon/daemon.go` | `forceWakeCount` + `churnSuppressed` fields + init, `churnForceWakeCap`, self-heal + suppress in `checkIdleAgents()`, resets | C |
| `daemon/daemon_test.go` | Unit tests for watchdog cap + force-wake churn cap, incl. `TestResetChurnGuard` (`resetChurnGuard` clears `forceWakeCount` + lifts `churnSuppressed`, no suppression for a fresh role) | A, C |
| `bus/notify_test.go` | Unit tests for notification shape/length caps | B |
| `scripts/test-watchdog-churn.sh` | Integration test (notification shape; watchdog cap logic) — exists and passes | A, B, C |

## Requirements

### Acceptance criteria

- [x] Long-active advisories are capped per active-episode (default 2), reset on idle transition
- [x] Advisory cap is configurable via `MUXCODE_ACTIVE_WATCHDOG_MAX_NUDGES`; existing disable envs preserved
- [x] Wake-up text for `>3` messages is a short fixed string (`"You have N new messages. Run: muxcode inbox"`), never concatenating subjects
- [x] Wake-up text is hard-capped (~200 chars) even in the enumerated (≤3) form
- [x] Force-wake path stops after `churnForceWakeCap` (default 3) attempts without the inbox draining
- [x] On force-wake cap, the daemon clears the composer once and suppresses further force-wakes until the agent idles / inbox drains
- [x] Force-wake counter and suppression flag reset on idle transition or inbox drain
- [x] Suppression sends a single `churn-suppressed` event to edit per episode (no re-notify noise)
- [x] `MUXCODE_ACTIVE_WATCHDOG_MAX_NUDGES=0` disables long-active advisories entirely (never nudge)
- [x] Unit + integration tests pass (incl. `TestResetChurnGuard`; `scripts/test-watchdog-churn.sh` green)

## Implementation

### Phase 1: Fix A — long-active watchdog cap

- [x] Add `activeNudgeCount map[string]int` to the `Daemon` struct and initialize in `New()`
- [x] Add `activeWatchdogMaxNudges()` reading `MUXCODE_ACTIVE_WATCHDOG_MAX_NUDGES` (default 2)
- [x] Cap `checkActiveWatchdog()` at `activeWatchdogMaxNudges()`; increment per nudge
- [x] Reset the counter wherever `activeSince[role]` resets (idle, reload, wait/poll)
- [x] Unit test: watchdog stops nudging after the cap; resets on idle

Success criteria:
- [x] At most `activeWatchdogMaxNudges()` advisories per active-episode
- [x] Counter resets to 0 on idle transition, reload, and wait/poll
- [x] Env override honored; existing disable env still works

### Phase 2: Fix B — bounded wake-up notification

- [x] Add `notifyMaxSubjects = 3` and `notifyMaxLen = 200` constants
- [x] `BuildCombinedNotification()` returns the short fixed string for `> notifyMaxSubjects` messages
- [x] Enumerated (≤3) form hard-capped at `notifyMaxLen` with `"and N more"` overflow
- [x] Unit test: shape for 0/1/2-3/>3 messages; length never exceeds cap

Success criteria:
- [x] `>3` messages → `"You have N new messages. Run: muxcode inbox"` (no subject blob)
- [x] Output length ≤ ~200 chars in all branches
- [x] Single/zero-message forms unchanged

### Phase 3: Fix C — self-healing force-wake cap

- [x] Add `forceWakeCount map[string]int` and `churnSuppressed map[string]bool` to the struct + init
- [x] Add `churnForceWakeCap` (default 3)
- [x] After the cap: clear composer once, set `churnSuppressed[role]`, log `churn-suppress`, send a `churn-suppressed` event to edit once
- [x] Skip force-wake while `churnSuppressed[role]` is set
- [x] Reset via `resetChurnGuard(role)` on idle transition / inbox drain (clears `forceWakeCount`, lifts `churnSuppressed`)
- [x] Unit test `TestResetChurnGuard`: `resetChurnGuard` clears the budget, lifts suppression, and never creates suppression for a fresh role

Success criteria:
- [x] Force-wake stops after `churnForceWakeCap` consecutive attempts without draining
- [x] Composer cleared once and suppression flag set on cap
- [x] Suppression lifts when the agent idles or the inbox drains
- [x] Edit alerted at most once per suppression episode

### Phase 4: Integration test and verification

- [x] Create `scripts/test-watchdog-churn.sh` (exists, 1.6 KB) and confirm it passes
- [x] Test: `BuildCombinedNotification` returns the short fixed string for `>3` messages
- [x] Test: notification length is bounded (≤ ~200 chars) across message counts
- [x] Test: watchdog cap logic (advisory count bounded) where unit-testable
- [x] Run the script and verify all checks pass

Success criteria:
- [x] `scripts/test-watchdog-churn.sh` exists and passes end-to-end
- [x] Script covers notification shape/length and watchdog cap behavior
- [x] Script performs prerequisite checks and cleans up after itself

## Configuration

| Aspect | Mechanism |
|--------|-----------|
| Long-active advisory cap | `MUXCODE_ACTIVE_WATCHDOG_MAX_NUDGES` (default 2; `0` = never nudge / advisories disabled; invalid → default) |
| Long-active watchdog threshold/disable | Existing `MUXCODE_ACTIVE_WATCHDOG_SECS` / disable env (unchanged) |
| Notification subject cap | `notifyMaxSubjects = 3` (compile-time) |
| Notification length cap | `notifyMaxLen = 200` (compile-time) |
| Force-wake churn cap | `churnForceWakeCap = 3` (compile-time) |

## Known limitations

| Limitation | Impact | Mitigation |
|------------|--------|------------|
| Idle proxy is capture-based | A parked blob can still momentarily read as active | Fix B removes the blob source; Fix C caps the resulting force-wakes |
| Compile-time notification caps | `notifyMaxSubjects`/`notifyMaxLen` not env-tunable | Values chosen to stay well under the idle-capture window; env-tuning is a follow-up if needed |
| Suppression waits for idle/drain | A genuinely stuck agent stays suppressed until it idles | Intentional — avoids re-churn; edit is alerted once so a human can intervene |

## Dependencies

| Dependency | Purpose | Status |
|------------|---------|--------|
| `daemon/daemon.go` `checkActiveWatchdog()` | Host for Fix A cap | Existing |
| `daemon/daemon.go` `checkIdleAgents()` | Host for Fix C force-wake cap | Existing |
| `bus/notify.go` `BuildCombinedNotification()` | Host for Fix B tiering | Existing |
| Permission-block watchdog pattern | Suppression-flag model reused by Fix C | Existing |
| Lifecycle logging | `churn-suppress` event emission | Existing |

## Status

Complete — fixes A/B/C implemented in `daemon/daemon.go` + `bus/notify.go`, unit + integration tests green
