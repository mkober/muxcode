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

- [x] A message consumed by an agent's own inbox read writes a **durable receipt**
  (`AckedAt` / `AckedBy`) keyed by message ID.
- [ ] The daemon makes **delivery decisions from receipts**, not `notified-{role}.ids`
  or pane-scrape idle detection.
- [ ] **Every agent** (Claude, OpenCode, Codex, harness) pulls its own inbox; the daemon
  no longer injects wake-ups for routine delivery.
- [ ] A message is **retried until a receipt** (or verified-inject) appears — no message
  strands due to a dropped Enter, idle misdetection, or a crashed+restarted agent.
- [x] A **dead poll loop / sidecar is detected via receipt-gap** and recovered (re-launch
  or alert) — the new backstop. (`checkPollHealth`, committed `e55a84a`.)
- [ ] The notified-IDs marker, churn-suppression, safety-net retries, and the
  active-with-stale-messages watchdog are **removed**. (Currently **bypassed** under the
  cutover flag, not deleted — see Phase 5 decision note; removal deferred until Phase 6.)
- [x] Provider matrix documented: true receipts for Claude/harness; verified-inject
  `delivered` for OpenCode/Codex with the limitation stated. (In-spec matrix + user-facing
  `docs/agents.md` "Message delivery and receipts" subsection.)
- [x] `muxcode deliver --force` **survives** as a manual last-resort escape hatch.
  (Kept, and now load-bearing inside `checkPollHealth`'s self-poller recovery.)

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

- [x] Add `AckedAt int64` + `AckedBy string` (role) fields and a `StatusAcked`.
- [x] Add a `ReceiptKind` field to distinguish a **true consume-ack** (agent-side
  `Receive`, `ReceiptKindAck`) from a **verified-inject `delivered`**
  (`ReceiptKindDelivered`, OpenCode/Codex sidecar).
- [x] Add `WriteReceipt`, `ReadReceipt`, and a `ReceiptGap(session, role)` helper that
  returns inbox messages with no receipt older than a threshold.

#### The consume choke point — `bus/inbox.go`

- [x] `Receive` (`inbox.go:207`) and `ReceiveFromFunc` (`inbox.go:262`) already call
  `MarkDelivered` per consumed message and are the single choke point for inbox
  consumption. Write the **receipt here**, tagging it agent-side vs daemon-side — the
  caller distinguishes: `cmd/inbox.go` = agent (true ack), `provider_*.go SendWakeUp` =
  daemon (delivered). **Done**: both `Receive` (`inbox.go:244`) and `ReceiveFromFunc`
  (`inbox.go:305`) write `WriteReceipt(..., ReceiptKindAck)`. The daemon-side
  `delivered` write lands in Phase 4 (the verified-inject loop), using the same
  `kind` parameter.

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

- [x] Extend `DeliveryStatus` in `delivery.go`: `AckedAt int64`, `AckedBy string`,
  `ReceiptKind` field, and a `StatusAcked` constant (+ `ReceiptKindAck` /
  `ReceiptKindDelivered`).
- [x] Add `WriteReceipt`, `ReadReceipt`, and `ReceiptGap(session, role)` helpers.
- [x] Write receipts in `Receive` / `ReceiveFromFunc`, tagged **agent-side** (true ack)
  by caller. (Daemon-side `delivered` write is Phase 4; the `kind` param is in place.)
- [x] Unit tests: receipt written on consume; `ReceiptGap` returns un-receipted messages
  past threshold; agent- vs daemon-side tagging distinguished.

### Phase 2: Claude self-poll + Stop hook

- [x] Claude agent runs `muxcode inbox --poll --loop` as a background Bash tool; processes
  messages on return, then re-launches the poll. (`bus/prompt.go` `SharedPrompt` now emits
  self-poll instructions gated on `provider.SupportsHooks()`; non-hook providers keep the
  daemon-wake text.)
- [x] Add a new `Stop` hook to `config/settings.json` (none exists today). (Also merged
  idempotently into the user's global `~/.claude/settings.json` by `install.sh`.)
- [x] Add a `stop` case to `cmd/hook.go` that re-launches the poll loop after each turn.
  (`hookStop()` — provider-gated to hook providers, `stop_hook_active` loop guard, blocks the
  stop with `StopHookPollReason` when no `--poll`/`--wait` listener is alive; backed by pure
  `DecideStopHook` in `bus/hook.go`.)
- [x] Update the relevant agent definitions with self-poll loop instructions. (Delivered via
  the shared prompt injected into every Claude agent.)
- [ ] Verify the Stop-hook re-launch fires reliably after a turn ends (the new single
  point of reliability for Claude). — Decision logic unit-tested (`TestDecideStopHook`,
  `TestParseToolEvent_StopHookActive`); live end-to-end firing deferred to the Phase 6
  integration test.

### Phase 3: Harness receipts

- [x] Ensure `AgentLoop`'s in-process consume (`bus/agent.go:118` → `Receive`) writes a
  true `acked` receipt. (Satisfied by the Phase 1 choke-point write; proven end-to-end by
  `TestReceive_ClearsReceiptGap` — an in-process consume writes the receipt that clears the
  `ReceiptGap` the Phase 5 backstop reads.)
- [x] Confirm the external `tools/muxcode-llm-harness` binary's consume path writes
  receipts (external module — see open items). (`harness/bus.go` `ConsumeInbox` consumes via
  the `muxcode inbox --raw` CLI, which routes through `bus.Receive` and writes a true `acked`
  receipt attributed via the `AGENT_ROLE` env `run()` sets — documented + guarded against
  regressing to a direct file read.)

### Phase 4: OpenCode/Codex verified-inject delivery

- [x] Remove the inbox-draining `Receive` from `SendWakeUp` in `provider_opencode.go` and
  `provider_codex.go`. (Post-send-keys drain replaced with `confirmInjectionAndConsume`; the
  self-addressed-only branch now uses `ReceiveDelivered`.)
- [x] Add a per-agent verified-injection + retry delivery loop: `send-keys` + pane-scrape
  confirmation the text landed; retry until verified; write a `delivered` receipt. (New
  `bus/inject_verify.go`: `injectionNeedle` → tail-slice needle, `verifyInjectionLanded`
  re-captures the composer and re-sends Enter up to `injectVerifyRetries`, returning
  submitted/parked/unknown; `confirmInjectionAndConsume` writes a `ReceiptKindDelivered`
  receipt on submit, leaves the inbox intact when parked, falls back to consume when
  unverifiable. `inbox.go` split into `Receive` (ack) / `ReceiveDelivered` (delivered) over a
  shared `receiveWithReceipt` core — completing the Phase 1 daemon-side `delivered` deferral.
  Full unit coverage in `inject_verify_test.go`.)
- [x] Document the true-receipt limitation and the in-pane-poll open item in the skill/docs.
  — Added to `docs/agents.md` → **Message delivery and receipts** (new subsection under
  "Differences across providers"): the receipt model, per-provider receipt-kind matrix
  (`acked` for Claude/harness vs verified-inject `delivered` for OpenCode/Codex), the
  limitation stated plainly, and the in-pane-poll open item, plus a `Message delivery` row in
  the provider comparison table.

### Phase 5: Daemon cutover

- [x] Add `checkPollHealth` (receipt-gap backstop): detect a growing receipt gap per agent;
  re-launch the Claude poll / restart the OpenCode/Codex sidecar; else alert edit.
  (`daemon.go` — `ReceiptGap` past `pollHealthGapSecs`=45s → `ForceDeliver` for self-pollers
  / `SendWakeUp` for non-hook TUIs → `delivery-gap` event to edit past
  `pollHealthAlertSecs`=120s; unit-tested in `poll_health_test.go`.)
- [ ] Remove pane-scrape delivery machinery: idle-based delivery in `checkIdleAgents`,
  parked-input / pane-sweep **as delivery**, notified-IDs subsystem, churn-suppression,
  safety-net retries, active-with-stale-messages watchdog.
  **Implemented as a gated bypass, not a deletion** — see decision note below; outright
  removal deferred until Phase 6 verifies the self-poll path live.
- [x] Keep task round-trip tracking (`checkTrackedTasks`, `StatusResponded`), non-hook
  task-completion detection, injection mechanics, and the `trigger-{role}.notify` write.
  (All preserved and still called from `Run()`.)
- [x] Add a kill-switch env var (e.g. `MUXCODE_DELIVERY_ACK_DISABLE`) as an operational
  rollback valve during rollout, even though the end-state replaces the old path.
  (`ackDeliveryActive()`: `MUXCODE_DELIVERY_ACK=1` activates the cutover;
  `MUXCODE_DELIVERY_ACK_DISABLE=1` hard-forces the old path; tested in `TestAckDeliveryActive`.)

> **Decision (Phase 5) — cutover gate instead of outright removal.** The spec's
> "Replace outright" decision called for **deleting** the notified-IDs subsystem,
> churn-suppression, safety-net retries, and the active-with-stale-messages watchdog.
> The implementation instead gates them behind `ackDeliveryActive()`. The cutover has
> since been **flipped to default ON** (env `MUXCODE_DELIVERY_ACK`, unset → ON): the
> receipt model (`checkPollHealth` + agent self-poll) is now in charge by default, while
> `checkIdleAgents`, `checkParkedInput`, and `checkPaneSweep` early-return (bypassed) and
> the old machinery stays intact only as a rollback fallback. Rationale (per commit
> `e55a84a`): keep a working delivery path until the Phase 2 Stop-hook self-poll is
> verified **live**. **Consequence**: the "removed" acceptance criterion and this step
> stay open **by design** — physical deletion of the dead machinery is the last step, now
> gated on the default-ON soak proving no regressions **and** the receipt-gap backstop
> mis-fire being resolved. The deletion is tracked in the backlog:
> [remove-gated-pane-scrape-delivery](../backlog/remove-gated-pane-scrape-delivery.md).

### Phase 6: Integration test (required)

Create `scripts/test-delivery-ack.sh` (`set -euo pipefail`) exercising the feature
end-to-end. Document what requires a **live session / real providers** vs what is asserted
**offline**, with graceful skips.

- [x] Create `scripts/test-delivery-ack.sh`. (Committed `53b5b73`.)
- [x] For each provider type, send a message → assert a **receipt is written** (true `acked`
  for Claude/harness; `delivered` for OpenCode/Codex). (Asserted offline via
  `TestWriteReceipt_*` / `TestReceiveMarksAcked` / `TestReceive_WritesConsumeReceipt`.)
- [x] Kill an agent's poll loop → assert the daemon detects the **receipt-gap and recovers**
  (re-launch or alert). (Asserted offline via `TestCheckPollHealth_RecordsGapAndAlertsWhenActive`
  / `TestCheckPollHealth_ClearsGapOnReceipt`; the **destructive live** repro against real
  providers is intentionally out of the CI runner's scope — documented in the script header.)
- [x] Simulate a **dropped Enter** → assert retry-until-received (no strand). (Offline via
  `TestVerifyInjectionLanded_*` / `TestConfirmInjectionAndConsume_*`.)
- [x] Restart a **mid-task agent** → assert sends are not blocked and the message is received
  after restart. (Offline via `TestClearInFlightTasksForRole_*` / `TestTaskExpired`.)
- [ ] Assert **no `notified-{role}.ids` writes** occur (old marker path is gone). **Open by
  design** — the marker path is *gated* (bypassed under the now-default-ON cutover), not
  removed (Phase 5 decision), so it still exists in the tree and writes only when the cutover
  is rolled back; the script instead asserts the pane-scrape delivery checks *bypass* under the
  cutover (`TestDeliveryChecksGatedWhenCutoverActive`). This closes once the machinery is
  physically deleted.
- [x] Assert `muxcode deliver --force` still works as a manual escape hatch. (Live smoke —
  usage/flag presence, session-independent.)
- [x] Run the script and verify all checks pass. (Edit reported green; **independently
  confirmed by the run agent** — 27 Go tests + 3 live smoke checks, exit 0.)

## Open items

- [ ] **Can OpenCode Go / Codex run `muxcode inbox --poll` in-process** (upgrading them
  from verified-inject to true receipts)? Investigate **before Phase 4** — it changes the
  provider matrix. — Phase 4 shipped the verified-inject `delivered` path (the matrix's
  fallback), so this remains **open**: no confirmed in-process poll path for these TUIs was
  found. If one exists, it would upgrade them to true `acked` receipts. Still to confirm.
- [x] **Confirm the `tools/muxcode-llm-harness` binary's consume path writes receipts**
  (external module — Phase 3 depends on it). Confirmed: `ConsumeInbox` consumes via the
  `muxcode inbox --raw` CLI (→ `bus.Receive`, role-attributed by `AGENT_ROLE`), so it is a
  true receipt producer; behavior documented and guarded in `harness/bus.go`.
- [ ] **Reliability of the Stop-hook poll re-launch for Claude** — is the daemon
  receipt-gap backstop (Phase 5) sufficient if the hook itself fails to fire?

## Status

In Progress — **Phases 1–4 committed; Phase 5 committed as a gated cutover** (`e55a84a`);
**Phase 6 integration test committed and green** (`scripts/test-delivery-ack.sh`, `53b5b73`).
All Phases 1–6 are now committed and pushed to `origin/main`, and **Phase 4 step 3**
(user-facing provider-matrix docs) is done in `docs/agents.md`. The cutover default has now
been **flipped to ON** (soak). The **only** remaining work is deferred by design: **physical
removal** of the bypassed pane-scrape machinery, awaiting the default-ON soak proving no
regressions **and** the receipt-gap backstop mis-fire being resolved. That removal is tracked
in its own backlog doc — [remove-gated-pane-scrape-delivery](../backlog/remove-gated-pane-scrape-delivery.md)
— along with the open "removed" acceptance criterion + Phase 5 step 2.

**Phase 1 (receipt store)**: `delivery.go` extended with
`AckedAt`/`AckedBy`/`ReceiptKind` + `StatusAcked`/`ReceiptKindAck`/`ReceiptKindDelivered`,
`WriteReceipt`/`ReadReceipt`/`ReceiptGap` added, agent-side `ReceiptKindAck` receipts
written at the `inbox.go` `Receive`/`ReceiveFromFunc` choke points, with unit tests
(`delivery_receipt_test.go` + additions to `delivery_test.go`/`poll_test.go`); build +
test + review green.

**Phase 2 (Claude self-poll + Stop hook)**: new `Stop` hook in `config/settings.json`
(and idempotent merge into global `~/.claude/settings.json` via `install.sh`); `stop` case
+ `hookStop()` in `cmd/hook.go` re-launching the self-poll listener, backed by the pure
`DecideStopHook`/`StopHookAction`/`StopHookPollReason`/`FormatStopBlock` helpers and the
`ToolEvent.StopHookActive` loop guard in `bus/hook.go`; `SharedPrompt` (`bus/prompt.go`)
now emits background `muxcode inbox --poll --loop` instructions for hook providers.
`MUXCODE_DELIVERY_ACK_DISABLE` kill switch wired early (spec'd for Phase 5). Unit tests
in `hook_test.go`. Steps 1–4 checked off; step 5 (live re-launch firing) deferred to the
Phase 6 integration test.

**Phase 3 (harness receipts)**: `AgentLoop`'s in-process `Receive` already writes a true
`acked` receipt (Phase 1 choke point), proven by `TestReceive_ClearsReceiptGap`; the
external `tools/muxcode-llm-harness` `ConsumeInbox` consumes via `muxcode inbox --raw`
(→ `bus.Receive`, role-attributed by `AGENT_ROLE`), making it a first-class receipt
producer — documented + guarded in `harness/bus.go`. Both steps checked off; the matching
open item is closed.

**Phase 4 (OpenCode/Codex verified-inject) — code complete**: new `bus/inject_verify.go`
replaces the fire-and-hope post-send-keys drain with `confirmInjectionAndConsume` — verify
the injected prompt left the composer (re-sending Enter up to `injectVerifyRetries`), then
consume with a `ReceiptKindDelivered` receipt; leave the inbox intact when the text stays
parked (no drop on a dropped Enter), fall back to consume when unverifiable. `provider_opencode.go`
/`provider_codex.go` `SendWakeUp` rewired to it; `inbox.go` split into `Receive` (ack) /
`ReceiveDelivered` (delivered) over a shared `receiveWithReceipt` core, finishing the Phase 1
daemon-side `delivered` deferral. Steps 1–2 checked off with full unit coverage
(`inject_verify_test.go`); **step 3 complete** — the provider matrix + true-receipt
limitation + in-pane-poll open item are now documented user-facing in `docs/agents.md`
("Message delivery and receipts"). The "investigate before Phase 4" open item stays open
(no in-process poll path for these TUIs confirmed).

**Phase 5 (daemon cutover) — committed `e55a84a`**: new `checkPollHealth` receipt-gap
backstop (`daemon.go` + `poll_health_test.go`) detects inbox messages un-receipted past
`pollHealthGapSecs` (45s), re-drives delivery (`ForceDeliver` for hook self-pollers /
`SendWakeUp` for non-hook TUIs), and emits a `delivery-gap` event to edit once the gap
persists past `pollHealthAlertSecs` (120s); `delivery-gap` added to the `isSystemAction`
allowlist (`guard.go`) so the alert doesn't trip loop detection. The pane-scrape delivery
machinery (`checkIdleAgents`, `checkParkedInput`, `checkPaneSweep`) is **gated off** — not
deleted — under the `MUXCODE_DELIVERY_ACK` cutover flag, now **default ON**, with
`MUXCODE_DELIVERY_ACK_DISABLE` (env hard kill switch) and the runtime `delivery-ack.off`
marker (`muxcode delivery-ack off`) as rollback valves; task round-trip tracking, non-hook
completion detection, injection mechanics, and the `trigger-{role}.notify` write are all
kept. Steps 1, 3, 4 checked off; **step 2 (physical removal) stays open by design** until
the default-ON soak proves the self-poll path live and the receipt-gap mis-fire is resolved.

**Phase 6 (integration test) — committed `53b5b73` and green** (`scripts/test-delivery-ack.sh`):
mirrors `test-watchdog-churn.sh` — the authoritative coverage runs the Go
suite (6 groups spanning the receipt store + per-provider kinds, receipt-gap
detect/recover, verified-inject dropped-Enter retry, mid-task-restart send unblocking,
cutover gating, and Stop-hook self-poll decision logic) with a strict "no silent pass"
guard, then a non-destructive live smoke section (`muxcode deliver --force` present;
receipt store active; `muxcode status` healthy — all graceful-skip without a session). Edit
reports all checks green. Six of the eight Phase 6 boxes checked; the "no `notified-IDs`
writes" box stays open by design (that path is gated, not deleted — the script asserts the
pane-scrape checks *bypass* under the cutover instead). Destructive live scenarios (kill a
poll loop, restart a mid-task agent, drop an Enter) are asserted deterministically offline
rather than against a live user session, by design. Only remaining spec item: Phase 4
step 3 (user-facing skill/doc).
