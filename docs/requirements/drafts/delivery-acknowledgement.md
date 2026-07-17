# Delivery Acknowledgement (receipts + agent self-poll)

## Context

Message delivery today is **inference-based**. The daemon pane-scrapes to guess an
agent is idle, injects a wake-up ("You have new messages" for Claude; the payload
itself for OpenCode/Codex), and marks the message `notified` — **optimistically**,
recording that it *sent* a wake-up, not that the agent *read* the message.

Every "agent not getting the message" failure this session traces to that gap:
idle-misdetection, dropped Enter / parked input, stale `notified` markers, a crashed
agent's stuck task, and the OpenCode inject-and-drain race. Each was patched with
more pane-scrape heuristics — watchdogs, churn-suppression, safety-net retries — which
compound rather than resolve the root problem.

**The durable fix**: replace inference with a **positive signal of consumption** (a
receipt) and let agents **pull** their own inbox instead of the daemon pushing.

### Decisions already made (do not re-litigate)

- **Universal** — receipts for all providers (Claude, OpenCode, Codex, local harness).
- **Agent self-poll (no daemon injection)** — every agent pulls its own inbox; the
  daemon stops pane-scraping and injecting for routine delivery.
- **Replace outright** — receipts become the single source of truth. The notified-IDs
  marker, churn-suppression, safety-net retries, and pane-scrape delivery watchdogs are
  **removed**, not kept as fallback.

### Related

| Reference | Role |
|-----------|------|
| `tools/muxcode/bus/delivery.go` | Existing receipt store to **extend** (`DeliveryStatus`, `MarkDelivered`) — do not build a parallel system |
| `tools/muxcode/bus/inbox.go` | `Receive` (L207) / `ReceiveFromFunc` (L262) — the single inbox-consume choke point |
| `tools/muxcode/cmd/inbox.go` | `inboxPoll` (L113) — the `muxcode inbox --poll [--loop]` self-poll primitive already exists |
| `tools/muxcode/bus/notify.go` | Notified-IDs subsystem to remove; `trigger-{role}.notify` write to keep |
| `tools/muxcode/daemon/daemon.go` | Pane-scrape delivery machinery to remove; new receipt-gap backstop to add |
| `config/settings.json`, `tools/muxcode/cmd/hook.go` | Hook config + dispatch — **no `Stop` hook exists today** (Phase 2 adds it) |

## Design constraint / provider matrix

A receipt that truly means "the agent processed this message" requires the agent's
**own runtime** to consume the inbox. That holds for two providers and hits a hard wall
for the other two — the spec resolves this explicitly rather than pretending universality.

| Provider | Consumes inbox in-process? | Receipt available | Kind |
|----------|---------------------------|-------------------|------|
| **Claude** (hook provider) | Yes — agent runs `muxcode inbox` via a Bash tool | True consume-ack | `acked` |
| **Local harness** (`LocalProvider`) | Yes — `AgentLoop` (`bus/agent.go:118`) calls `Receive` in-process | True consume-ack | `acked` |
| **OpenCode** (non-hook TUI) | No — TUI receives text only via pane injection; never runs `muxcode inbox` | Verified-inject only | `delivered` |
| **Codex** (non-hook TUI) | No — same limitation | Verified-inject only | `delivered` |

### Why OpenCode/Codex cannot produce a true receipt today

Their TUI can only receive text via pane injection and cannot consume the inbox
in-process. Today the **daemon's** `provider.SendWakeUp` both injects the payload into
the pane **and** drains the inbox via `Receive` in one call
(`provider_opencode.go:176,212`, `provider_codex.go:217,256`) — gated only on `send-keys`
returning no error, with no verification the text landed. So "agent read it" is
**unobservable** for these two.

### Resolution (the design)

- **Claude + harness** → **true consume-receipt**: the agent-side `Receive` writes it.
- **OpenCode/Codex** → **verified-inject `delivered` receipt**: a per-agent delivery loop
  does `send-keys` **plus a pane-scrape confirmation the text actually landed** (the
  analogue of Claude's `verifyEnterDelivery`, which these providers lack today) and
  **retries until verified**. This is strictly better than today's fire-and-hope drain,
  but it is **NOT** a true consume-receipt — it confirms the text reached the pane, not
  that the agent processed it.

**Limitation stated plainly**: a true OpenCode/Codex receipt needs upstream TUI support
or an in-pane poll command those runtimes do not currently expose. Whether OpenCode Go /
Codex can be configured to run `muxcode inbox --poll` themselves — upgrading them to true
receipts — is an **open item** (see below) to investigate before Phase 4.

## Requirements

### Acceptance criteria

- [ ] A message consumed by an agent's own inbox read writes a **durable receipt**
  (`AckedAt` / `AckedBy`) keyed by message ID.
- [ ] The daemon makes **delivery decisions from receipts**, not `notified-{role}.ids`
  or pane-scrape idle detection.
- [ ] **Every agent** (Claude, OpenCode, Codex, harness) pulls its own inbox; the daemon
  no longer injects wake-ups for routine delivery.
- [ ] A message is **retried until a receipt** (or verified-inject) appears — no message
  strands due to a dropped Enter, idle misdetection, or a crashed+restarted agent.
- [ ] A **dead poll loop / sidecar is detected via receipt-gap** and recovered (re-launch
  or alert) — the new backstop.
- [ ] The notified-IDs marker, churn-suppression, safety-net retries, and the
  active-with-stale-messages watchdog are **removed**.
- [ ] Provider matrix documented: true receipts for Claude/harness; verified-inject
  `delivered` for OpenCode/Codex with the limitation stated.
- [ ] `muxcode deliver --force` **survives** as a manual last-resort escape hatch.

### Technical approach

All touchpoints below were confirmed against the working tree. Line numbers are
approximate anchors.

#### Receipt store — extend the existing `bus/delivery.go`

`delivery.go` already has `DeliveryStatus{ID, Status, SentAt, DeliveredAt, ResponseID}`
in per-message files at `/tmp/muxcode-bus-{session}/delivery/{msgID}.status`, with
`Create` / `Read` / `List` / `Clean` helpers. But `delivered` is **cosmetic** today —
written by `MarkDelivered` (from `Receive` / `ReceiveFromFunc`) yet **read by nothing**
for delivery decisions (only `StatusResponded` is read, for `--wait` / `--track`
round-trips). Extend it — do **not** build a parallel store:

- [ ] Add `AckedAt int64` + `AckedBy string` (role) fields and a `StatusAcked`.
- [ ] Add a `ReceiptKind` field to distinguish a **true consume-ack** (agent-side
  `Receive`) from a **verified-inject `delivered`** (OpenCode/Codex sidecar).
- [ ] Add `WriteReceipt`, `ReadReceipt`, and a `ReceiptGap(session, role)` helper that
  returns inbox messages with no receipt older than a threshold.

#### The consume choke point — `bus/inbox.go`

- [ ] `Receive` (`inbox.go:207`) and `ReceiveFromFunc` (`inbox.go:262`) already call
  `MarkDelivered` per consumed message and are the single choke point for inbox
  consumption. Write the **receipt here**, tagging it agent-side vs daemon-side — the
  caller distinguishes: `cmd/inbox.go` = agent (true ack), `provider_*.go SendWakeUp` =
  daemon (delivered).

#### Self-poll loop per agent

The primitive already exists: `cmd/inbox.go` `inboxPoll` (`muxcode inbox --poll [--loop]`,
L113) watches `trigger-{role}.notify` mtime + `HasMessages`, `Receive`s on a hit, and sets
a `polling-{role}.marker` that suppresses daemon pushes. `--loop` restarts silently.

- [ ] **Claude**: agent runs `muxcode inbox --poll --loop` as a background Bash tool; on
  return (messages arrived) it processes them, then re-launches the poll. To keep the
  loop from dying silently, add a **new `Stop` hook** — **confirmed absent today**
  (`config/settings.json` has only PreToolUse/PostToolUse; `cmd/hook.go` cases are
  `bash` / `guard` / `analyze`, no `stop`) — that re-launches the poll after each turn.
  **This Stop-hook re-launch is the new single point of reliability** for Claude delivery.
- [ ] **Harness**: `AgentLoop` already self-polls; ensure its `Receive` writes a receipt.
  Note: `harness-{role}.pid` is written by the separate `tools/muxcode-llm-harness` binary
  (out of this repo) — confirm before relying on it (open item).
- [ ] **OpenCode/Codex**: remove the inbox-draining `Receive` from their `SendWakeUp`
  (`provider_opencode.go`, `provider_codex.go`); replace the daemon push with a per-agent
  **verified-injection + retry** delivery loop (see the provider matrix).

#### Daemon — remove the pane-scrape delivery machinery (replace outright)

In `daemon/daemon.go`, these exist **solely** to guess whether a wake-up was processed and
must be removed as delivery mechanisms:

- [ ] `checkIdleAgents` (`daemon.go:1589`) idle-detection-based delivery: the
  idle-transition combined-notification path, the unnotified-messages gate, the
  active-with-stale-messages watchdog + `ForceDeliver` gating, churn-suppression
  (`churnForceWakeCap`), and the stale-marker safety-net retry.
- [ ] `checkParkedInput` (`daemon.go:1916`) and `checkPaneSweep` (`daemon.go:2028`) **as
  delivery mechanisms**.
- [ ] The notified-IDs subsystem in `bus/notify.go` (`AddNotifiedIDs`, `clearNotifiedIDs`,
  `UnnotifiedMessages` as a delivery gate, `IsNotifiedRecently`) and the
  `notified-{role}.ids` marker.
- [ ] The `Receive`-drain inside `SendWakeUp` for OpenCode/Codex.

#### Daemon — add the poll-health backstop

- [ ] New `checkPollHealth` (or similar): for each agent, detect a growing **receipt gap**
  (inbox messages with no receipt past a threshold) meaning the poll loop / sidecar died.
  Response: re-launch the Claude poll (or restart the OpenCode/Codex sidecar), else alert
  edit. This replaces pane-scrape wedge detection with a positive-signal detector.

#### Keep (not delivery inference — still needed)

- [ ] Injection mechanics for the sidecar / verified-inject path — Enter-drop recovery,
  `HasPendingInput` / `IsWindowFocused` guards so injection never corrupts user typing.
- [ ] Task round-trip tracking: `checkTrackedTasks` (`daemon.go:2120`) reading
  `StatusResponded` for `--wait` / `--track`. **A receipt = "inbox read", NOT "work
  done"** — the response / task-completion signal is separate.
- [ ] Non-hook task-**completion** detection (`checkNonHookTasks` / `DetectTaskCompletion`)
  — completion is a different signal than receipt; keep unless OpenCode/Codex gain hooks.
  (Note the overlap for the spec to reason about.)
- [ ] The `trigger-{role}.notify` file (the poll watches its mtime) and `Notify`'s
  trigger-write half — drop only the send-keys / notified-IDs half.

### Key files

| File | Change |
|------|--------|
| `tools/muxcode/bus/delivery.go` | Add `AckedAt`/`AckedBy`/`ReceiptKind` + `StatusAcked`; `WriteReceipt`/`ReadReceipt`/`ReceiptGap` |
| `tools/muxcode/bus/inbox.go` | Write receipt at the `Receive`/`ReceiveFromFunc` choke point, tagged agent- vs daemon-side |
| `tools/muxcode/cmd/inbox.go` | `inboxPoll` self-poll primitive (exists) — reuse for the Claude/agent loop |
| `config/settings.json` | Add a new `Stop` hook entry (absent today) |
| `tools/muxcode/cmd/hook.go` | Add a `stop` case that re-launches the Claude poll loop |
| `tools/muxcode/bus/provider_opencode.go` | Remove `Receive` drain from `SendWakeUp`; verified-inject + retry loop |
| `tools/muxcode/bus/provider_codex.go` | Same as OpenCode |
| `tools/muxcode/bus/notify.go` | Remove notified-IDs subsystem; keep `trigger-{role}.notify` write |
| `tools/muxcode/daemon/daemon.go` | Remove pane-scrape delivery machinery; add `checkPollHealth` receipt-gap backstop |
| `scripts/test-delivery-ack.sh` | New — integration test (Phase 6) |

## Implementation

### Phase 1: Receipt store

- [ ] Extend `DeliveryStatus` in `delivery.go`: `AckedAt int64`, `AckedBy string`,
  `ReceiptKind` field, and a `StatusAcked` constant.
- [ ] Add `WriteReceipt`, `ReadReceipt`, and `ReceiptGap(session, role)` helpers.
- [ ] Write receipts in `Receive` / `ReceiveFromFunc`, tagged **agent-side** (true ack)
  vs **daemon-side** (delivered) by caller.
- [ ] Unit tests: receipt written on consume; `ReceiptGap` returns un-receipted messages
  past threshold; agent- vs daemon-side tagging distinguished.

### Phase 2: Claude self-poll + Stop hook

- [ ] Claude agent runs `muxcode inbox --poll --loop` as a background Bash tool; processes
  messages on return, then re-launches the poll.
- [ ] Add a new `Stop` hook to `config/settings.json` (none exists today).
- [ ] Add a `stop` case to `cmd/hook.go` that re-launches the poll loop after each turn.
- [ ] Update the relevant agent definitions with self-poll loop instructions.
- [ ] Verify the Stop-hook re-launch fires reliably after a turn ends (the new single
  point of reliability for Claude).

### Phase 3: Harness receipts

- [ ] Ensure `AgentLoop`'s in-process consume (`bus/agent.go:118` → `Receive`) writes a
  true `acked` receipt.
- [ ] Confirm the external `tools/muxcode-llm-harness` binary's consume path writes
  receipts (external module — see open items).

### Phase 4: OpenCode/Codex verified-inject delivery

- [ ] Remove the inbox-draining `Receive` from `SendWakeUp` in `provider_opencode.go` and
  `provider_codex.go`.
- [ ] Add a per-agent verified-injection + retry delivery loop: `send-keys` + pane-scrape
  confirmation the text landed; retry until verified; write a `delivered` receipt.
- [ ] Document the true-receipt limitation and the in-pane-poll open item in the skill/docs.

### Phase 5: Daemon cutover

- [ ] Add `checkPollHealth` (receipt-gap backstop): detect a growing receipt gap per agent;
  re-launch the Claude poll / restart the OpenCode/Codex sidecar; else alert edit.
- [ ] Remove pane-scrape delivery machinery: idle-based delivery in `checkIdleAgents`,
  parked-input / pane-sweep **as delivery**, notified-IDs subsystem, churn-suppression,
  safety-net retries, active-with-stale-messages watchdog.
- [ ] Keep task round-trip tracking (`checkTrackedTasks`, `StatusResponded`), non-hook
  task-completion detection, injection mechanics, and the `trigger-{role}.notify` write.
- [ ] Add a kill-switch env var (e.g. `MUXCODE_DELIVERY_ACK_DISABLE`) as an operational
  rollback valve during rollout, even though the end-state replaces the old path.

### Phase 6: Integration test (required)

Create `scripts/test-delivery-ack.sh` (`set -euo pipefail`) exercising the feature
end-to-end. Document what requires a **live session / real providers** vs what is asserted
**offline**, with graceful skips.

- [ ] Create `scripts/test-delivery-ack.sh`.
- [ ] For each provider type, send a message → assert a **receipt is written** (true `acked`
  for Claude/harness; `delivered` for OpenCode/Codex).
- [ ] Kill an agent's poll loop → assert the daemon detects the **receipt-gap and recovers**
  (re-launch or alert).
- [ ] Simulate a **dropped Enter** → assert retry-until-received (no strand).
- [ ] Restart a **mid-task agent** → assert sends are not blocked and the message is received
  after restart.
- [ ] Assert **no `notified-{role}.ids` writes** occur (old marker path is gone).
- [ ] Assert `muxcode deliver --force` still works as a manual escape hatch.
- [ ] Run the script and verify all checks pass.

## Open items

- [ ] **Can OpenCode Go / Codex run `muxcode inbox --poll` in-process** (upgrading them
  from verified-inject to true receipts)? Investigate **before Phase 4** — it changes the
  provider matrix.
- [ ] **Confirm the `tools/muxcode-llm-harness` binary's consume path writes receipts**
  (external module — Phase 3 depends on it).
- [ ] **Reliability of the Stop-hook poll re-launch for Claude** — is the daemon
  receipt-gap backstop (Phase 5) sufficient if the hook itself fails to fire?

## Status

Draft
