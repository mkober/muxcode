# Remove gated pane-scrape delivery machinery

Physically delete the daemon's pane-scrape delivery machinery — the notified-IDs subsystem,
churn-suppression, safety-net retries, and the active-with-stale-messages watchdog — now that
receipt-based delivery ([delivery-acknowledgement](../drafts/delivery-acknowledgement.md))
supersedes it. Today that machinery is **gated OFF at runtime** behind `ackDeliveryActive()`
(`MUXCODE_DELIVERY_ACK`, default OFF) but still **present in the tree** as a fallback. This
doc tracks the final step: removing the dead code once the cutover is proven stable live.

This is the **deferred item** of the delivery-acknowledgement spec — its "Replace outright"
decision called for deletion, but Phase 5 shipped a gated bypass instead so a working
delivery path survives until the self-poll model is verified in production. See that spec's
**Phase 5 decision note** and the still-open "removed" acceptance criterion / Phase 5 step 2.

## Context

### Why this is deferred, not done

The delivery-ack redesign replaces pane-scrape delivery *inference* with per-message
**receipts** + agent self-poll. Phase 5 gated the old machinery behind a cutover flag
(default OFF) rather than deleting it, deliberately, so:

- the fallback stays intact until the Phase 2 Stop-hook self-poll is proven to fire reliably
  **live** (not just in unit tests), and
- `MUXCODE_DELIVERY_ACK_DISABLE` can hard-revert to the old path during rollout.

Deleting the machinery is safe only **after** the cutover has run as the default in a real
session with no delivery regressions.

### Current state

| Aspect | State |
|--------|-------|
| Receipt model (Phases 1–6) | Committed + pushed to `origin/main` |
| Cutover flag `MUXCODE_DELIVERY_ACK` | Default **OFF** — old machinery still in charge |
| Old pane-scrape machinery | Bypassed under the flag, **not deleted** |
| This removal | **Backlog** — blocked on the prerequisite below |

### `checkPollHealth` hardening (committed `77b8093`)

The receipt-gap backstop was scoped and debounced to stop `delivery-gap` churn:

- **Live-agent + actionable-request gate** — mirrors the `checkInboxes` delivery gate. A role
  is skipped (and its gap state reset) unless **both** hold:
  - `agentAlive(session, role)` (injectable, defaults to `bus.IsAgentAlive`) — a crashed-to-shell
    agent has nothing to recover; `checkAgentHealth` handles restarts.
  - `bus.HasActionableMessages(session, role)` — response-only / informational inbox growth is not
    a delivery failure; only an **un-consumed request** past the threshold signals a dead poll loop.
- **Recover-once per gap episode** (`pollGapRecovered[role]`) — re-drive delivery (`ForceDeliver` /
  `SendWakeUp`) **once** per gap, not every poll, plus a single `delivery-gap` alert to edit. The gap
  clears and re-arms once a receipt lands. Prevents a failed-attempt + warning storm against an agent
  that legitimately hasn't consumed yet.

### Known limitation — receipt-gap backstop mis-fires (why the cutover stays opt-in)

`provider.IsAlive` **fail-safes to "alive"** for a role whose pane cannot be captured, so the
live-agent gate alone cannot suppress an agent that is **alive but not (yet) self-polling**. The
backstop therefore still mis-fires as a `delivery-gap` for agents whose inbox is legitimately
un-consumed for benign reasons:

- **Busy non-hook TUIs** (OpenCode/Codex mid-task) — not idle to accept an inject, no receipt yet.
- **Freshly-idle Claude** whose `muxcode inbox --poll --loop` self-poll loop hasn't (re)launched yet.

The recover-once guard reduces this to a single wasted attempt + one alert per episode rather than
per-poll churn, but it does **not** eliminate the false positive. A durable fix needs a **positive
"self-poll loop is running" signal** (e.g. a liveness heartbeat from the poll listener / sidecar)
rather than inferring death from a receipt gap. **Until then the cutover stays opt-in**
(`MUXCODE_DELIVERY_ACK` / `muxcode delivery-ack on`), never the default — this is an added
prerequisite for the removal below.

## Requirements

### Prerequisite (blocker)

- [ ] The receipt-based cutover (`MUXCODE_DELIVERY_ACK=1`) has run as the effective default
  in a live session across all provider types (Claude, OpenCode, Codex, harness) with **no
  delivery regressions** — no stranded messages, no missed wake-ups — over a sustained period.
- [ ] The daemon `checkPollHealth` receipt-gap backstop has been observed recovering a real
  dead poll loop / sidecar (or is otherwise trusted as the sole wedge detector).
- [ ] The **receipt-gap mis-fire** (see Known limitation above) is resolved — a positive
  self-poll liveness signal replaces gap-inferred death so the backstop no longer false-alarms
  on busy non-hook TUIs or freshly-idle Claude agents.
- [ ] Flip the `ackDeliveryActive()` default to ON (cutover is the default), keeping
  `MUXCODE_DELIVERY_ACK_DISABLE` as the rollback valve for at least one release before removal.

### Removal list (delete outright)

Sourced verbatim from the delivery-acknowledgement spec's "Daemon — remove the pane-scrape
delivery machinery" section and Phase 5 step 2. These exist **solely** to guess whether a
wake-up was processed:

- [ ] `checkIdleAgents` (`daemon/daemon.go`) idle-detection-based delivery: the
  idle-transition combined-notification path, the unnotified-messages gate, the
  active-with-stale-messages watchdog + `ForceDeliver` gating, churn-suppression
  (`churnForceWakeCap` / `forceWakeCount`), and the stale-marker safety-net retry.
- [ ] `checkParkedInput` and `checkPaneSweep` (`daemon/daemon.go`) **as delivery mechanisms**
  (their injection-hygiene role, if any survives, is covered by the Keep list).
- [ ] The notified-IDs subsystem in `bus/notify.go`: `AddNotifiedIDs`, `clearNotifiedIDs`,
  `UnnotifiedMessages` (as a delivery gate), `IsNotifiedRecently`, and the
  `notified-{role}.ids` marker file.
- [ ] The now-unused `ackDeliveryActive()` gate branches themselves — once the old paths are
  gone, the early-return guards in `checkIdleAgents` / `checkParkedInput` / `checkPaneSweep`
  and the cutover flag plumbing collapse to a single path.
- [ ] Any dead helpers, struct fields (`forceWakeCount`, notified-ID maps), and tests left
  orphaned by the deletions above.

### Keep (must NOT be removed)

Receipt delivery does not replace these — they are separate signals:

- [ ] Task round-trip tracking: `checkTrackedTasks` reading `StatusResponded` (for
  `--wait` / `--track`). A receipt = "inbox read", not "work done".
- [ ] Non-hook task-**completion** detection (`checkNonHookTasks` / `DetectTaskCompletion`).
- [ ] Injection mechanics for the verified-inject path (`bus/inject_verify.go`) —
  Enter-drop recovery, `HasPendingInput` / window-focus guards so injection never corrupts
  user typing.
- [ ] The `trigger-{role}.notify` file and `Notify`'s trigger-write half (the poll watches
  its mtime) — drop only the send-keys / notified-IDs half.
- [ ] `checkPollHealth` receipt-gap backstop and the `muxcode deliver --force` escape hatch.

### Key files

| File | Change |
|------|--------|
| `tools/muxcode/daemon/daemon.go` | Delete gated idle/parked/pane-sweep delivery paths, churn-suppression, active-stale watchdog, `forceWakeCount`, and the `ackDeliveryActive()` gate plumbing |
| `tools/muxcode/bus/notify.go` | Remove the notified-IDs subsystem; keep the `trigger-{role}.notify` write |
| `tools/muxcode/daemon/poll_health_test.go` | Update cutover-gating tests that assert the old paths bypass (they become the only path) |
| `docs/requirements/drafts/delivery-acknowledgement.md` | Check off the "removed" AC + Phase 5 step 2 once this lands |

## Implementation

### Phase 1: Flip the default and soak

- [ ] Flip `ackDeliveryActive()` to default ON; keep `MUXCODE_DELIVERY_ACK_DISABLE` as the
  rollback valve.
- [ ] Soak in a live session; confirm no stranded messages / missed wake-ups across providers.

### Phase 2: Delete the machinery

- [ ] Remove the notified-IDs subsystem from `bus/notify.go` (keep the trigger write).
- [ ] Remove the gated delivery paths, churn-suppression, active-stale watchdog, and
  `forceWakeCount` from `daemon/daemon.go`.
- [ ] Collapse the `ackDeliveryActive()` gate branches to the single receipt-based path.
- [ ] Delete orphaned helpers / struct fields / tests.

### Phase 3: Verify the Keep list survives

- [ ] Confirm `checkTrackedTasks`, non-hook completion detection, injection mechanics,
  `trigger-{role}.notify`, `checkPollHealth`, and `muxcode deliver --force` are intact.
- [ ] Check off the "removed" acceptance criterion + Phase 5 step 2 in the delivery-ack spec;
  move that spec to `completed/` if it is otherwise done.

### Phase 4: Integration test

- [ ] Update `scripts/test-delivery-ack.sh`: replace the "pane-scrape delivery bypassed under
  cutover" assertions (`TestDeliveryChecksGatedWhenCutoverActive`) with assertions that the
  old paths **no longer exist** and delivery is receipt-only.
- [ ] Add an assertion that **no `notified-{role}.ids` file is ever written** (the box the
  Phase 6 test left open by design — closeable only once this removal lands).
- [ ] Run the script and verify all checks pass.

## Status

Backlog — blocked on the prerequisite: the receipt cutover must be proven stable live as the
default before the gated machinery is deleted. Tracks the deferred "removed" acceptance
criterion / Phase 5 step 2 of
[delivery-acknowledgement](../drafts/delivery-acknowledgement.md).
