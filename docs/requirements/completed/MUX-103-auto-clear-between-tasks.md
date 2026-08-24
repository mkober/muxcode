# MUX-103: Auto-Clear Between Tasks

## Context

Episodic agents (review, plan, commit, run, api) accumulate conversation context across
unrelated tasks. Every turn re-sends the full growing conversation, so input tokens compound
all session — the dominant driver of subscription limit burn, and roughly 2x worse per token
on premium models (Claude Fable 5 at $10/$50 per MTok vs Opus 5 at $5/$25).

Each bus request is self-contained by design, and cross-task state already lives in
`muxcode memory` — retained conversation context between tasks is dead weight for these
roles. Clearing it aligns with the architecture rather than fighting it.

The injection machinery already exists: `muxcode compact` waits for agent idle and injects
`/compact` via the two-step send-keys path (text → delay → Enter, per the dropped-Enter
pitfall). Auto-clear reuses that path with `/clear` and adds a daemon-side trigger.

### Scope

| Role class | Behavior |
|------------|----------|
| Episodic Claude roles (review, plan, commit, run, api) | Eligible for auto-clear, opt-in per role |
| edit, auto | **Hard-excluded** even if listed — they hold user conversation and loop state; they keep `/compact` |
| OpenCode / Codex roles (build, test, deploy, serve, ...) | Out of scope Phase 1 — `/clear` is a Claude Code built-in; a provider-specific mechanism is future work |
| Harness panes | Excluded — no TUI conversation to clear |

## Requirements

### Acceptance criteria

- [x] Roles enrolled via config get `/clear` injected after a task completes, once all guards pass
- [x] `edit` and `auto` are never cleared, even when listed in the config
- [x] No clear fires while any guard holds: pending actionable inbox, in-flight tasks, reload marker present, agent busy/active, non-Claude provider, harness pane
- [x] Quiet window (default 60s after the response) elapses before injection
- [x] Exactly one clear per completed task — repeated daemon polls do not re-clear (marker/cooldown)
- [x] Each clear emits a lifecycle event (`auto-clear`) with role and trigger context
- [x] Delivery still works after a clear: the next inbox message is delivered and receipted (Stop-hook relaunches the `muxcode inbox --poll --loop` listener on the next turn; daemon backstop covers the gap) — verified **live**, not by the suite; see note below
- [x] Feature is off by default; enabling requires explicit per-role config

### Technical approach

- **Guarded clear primitive** (`bus/clear.go`, new): `AutoClearEligible(role)` evaluates the
  guard matrix; `ClearAgent(role)` injects `/clear` via the same robust two-step send-keys
  path used by `muxcode compact`, writes a per-role marker
  (`/tmp/muxcode-bus-{session}/auto-clear-{role}.last`), and logs a lifecycle event.
- **Daemon trigger**: new `checkAutoClear()` in the watcher poll loop. For each enrolled
  role, fire when a task for that role completed (responded) after the last clear marker and
  `AutoClearEligible` passes. Task completion is observed through the existing task /
  delivery-receipt stores (`bus/task.go`, `bus/delivery.go`) — no new state tracking.
- **Config**: `MUXCODE_AUTO_CLEAR_ROLES` (comma-separated role list, default empty = off)
  and `MUXCODE_AUTO_CLEAR_QUIET_SECS` (default 60). Standard resolution chain
  (env → config file). Hard-exclusion of edit/auto applied after parsing, mirroring
  `reload --all` exclusions.
- **Manual path**: `muxcode clear <role>` subcommand runs the same guarded path on demand —
  keeps the injection logic exercisable without waiting for the daemon, and gives users a
  one-off lever.
- **Provider gating**: only fire for roles whose resolved provider is Claude Code
  (`/clear` is a Claude Code built-in slash command).

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/clear.go` | New — eligibility guards, `/clear` injection, marker, lifecycle event |
| `tools/muxcode/bus/config.go` | Marker path helper |
| `tools/muxcode/daemon/daemon.go` | `checkAutoClear()` in the poll loop (spec originally named `watcher/watcher.go`; that package no longer exists) |
| `tools/muxcode/cmd/` | `muxcode clear <role>` subcommand |
| `docs/configuration.md` | Document the two env vars |
| `docs/architecture.md` | Auto-clear subsection alongside compaction |

## Implementation

### Phase 1: Guarded clear primitive

- [x] `bus/clear.go`: `AutoClearEligible(role)` — enrolled, Claude provider, idle, no
  actionable inbox, no in-flight tasks, no reload marker, quiet window elapsed, hard-exclude
  edit/auto/harness
- [x] `ClearAgent(role)` — two-step send-keys `/clear` injection, marker write, lifecycle
  `auto-clear` event
- [x] `muxcode clear <role>` manual subcommand
- [x] Unit tests: guard matrix (each guard independently flips the verdict), edit/auto
  exclusion pinned by test

### Phase 2: Daemon trigger and config

- [x] `checkAutoClear()` in the daemon poll loop — fires once per completed task per role
- [x] Config plumbing: `MUXCODE_AUTO_CLEAR_ROLES`, `MUXCODE_AUTO_CLEAR_QUIET_SECS` with
  config-file resolution
- [x] Cooldown/idempotence: marker timestamp gates re-fire across poll cycles
- [x] Docs: `configuration.md` env vars, `architecture.md` subsection, `CLAUDE.md`
  constraint bullet

> Review verdict 2026-08-24: **0 must-fix, 2 should-fix, 2 nits — LGTM**, tests green
> (1889 PASS / 0 FAIL). The two should-fixes are the missing integration test (Phase 3) and
> the missing docs bullets above — both already tracked as open boxes. Third observation
> (nit-level): `checkAutoClear` runs a full per-role scan on every 15s tick even when nothing
> is due; a marker-read short-circuit was considered and rejected by review as having no cheap
> "any completion since marker" signal to test first. Partly addressed since — `LastTaskCompletion`
> now skips the recipient-resolving log scan for any delivery status whose timestamp cannot
> advance the running maximum.
>
> Follow-up fix 2026-08-24 (post-review): an in-flight task that has already been **answered**
> no longer blocks a clear. `hasLiveInFlightTask` skips tasks whose delivery status is
> `responded`, matching the delivery-ack rule that a reply is strictly stronger evidence than a
> consume-ack (`hasReceipt` in `bus/delivery.go`). Without it, a role whose task was answered but
> left in-flight would be blocked from clearing until the task expired. Pinned by
> `TestAutoClearEligible_AnsweredInFlightTaskDoesNotBlock`; suite now 1890 PASS / 0 FAIL.

### Phase 3: Integration test

- [x] Create `scripts/test-auto-clear.sh` (isolated scratch `BUS_SESSION` where possible,
  following `test-echo-as-result.sh` pattern)
- [x] Test: enrolled role + completed task + quiet window elapsed → clear fires once
  (marker written, lifecycle event logged) — Scenario 1
- [x] Test: pending actionable inbox message → no clear
- [x] Test: `edit` listed in `MUXCODE_AUTO_CLEAR_ROLES` → never cleared — Scenario 3
- [x] Test: repeated poll cycles after one clear → no duplicate clears — Scenario 4
- [x] Test: unenrolled role with completed task → untouched — Scenario 5
- [x] Run integration test and verify all steps pass — **22 passed, 0 failed**
  (fresh run 2026-08-24 16:49 against the 16:41 binary; log `/tmp/test-auto-clear3.log`)

### Note: post-clear delivery was verified live, not by the suite

None of the suite's 22 assertions exercise post-clear delivery, and none can: the harness
clears a *fake* idle pane (a shell whose prompt mimics the Claude glyph), which has no
Claude runtime and therefore no `Stop` hook to relaunch the `muxcode inbox --poll --loop`
listener. The one thing the criterion asserts is the one thing a scratch pane cannot show.

Closed by a live check against the real `run` agent (2026-08-24):

| Time | Event | Evidence |
|------|-------|----------|
| 16:52:22 | `muxcode clear run` fired | lifecycle `auto-clear role=run trigger=manual`; `auto-clear-run.last` written |
| 16:52:31 | edit → run request | "reply with a one-line ok to confirm delivery works after /clear" |
| 16:52:44 | run → edit response | `ok` — delivered and answered 13s after the clear |

A response is stronger evidence than a consume-ack under the delivery-ack rule
(`hasReceipt`, `bus/delivery.go`): the agent did not merely read the message, it completed
the work and answered. **If this harness is ever extended to drive a real Claude pane, fold
this check into the suite** — it is the one criterion currently resting on a manual run.

## Time Tracking

| Branch | Active time | Last updated |
|--------|-------------|--------------|
| MUX-103-auto-clear-between-tasks | 34m | 2026-08-24 16:53 |

## Status

Complete
