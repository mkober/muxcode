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

- [ ] Roles enrolled via config get `/clear` injected after a task completes, once all guards pass
- [ ] `edit` and `auto` are never cleared, even when listed in the config
- [ ] No clear fires while any guard holds: pending actionable inbox, in-flight tasks, reload marker present, agent busy/active, non-Claude provider, harness pane
- [ ] Quiet window (default 60s after the response) elapses before injection
- [ ] Exactly one clear per completed task — repeated daemon polls do not re-clear (marker/cooldown)
- [ ] Each clear emits a lifecycle event (`auto-clear`) with role and trigger context
- [ ] Delivery still works after a clear: the next inbox message is delivered and receipted (Stop-hook relaunches the `muxcode inbox --poll --loop` listener on the next turn; daemon backstop covers the gap)
- [ ] Feature is off by default; enabling requires explicit per-role config

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
| `tools/muxcode/watcher/watcher.go` | `checkAutoClear()` in the poll loop |
| `tools/muxcode/cmd/` | `muxcode clear <role>` subcommand |
| `docs/configuration.md` | Document the two env vars |
| `docs/architecture.md` | Auto-clear subsection alongside compaction |

## Implementation

### Phase 1: Guarded clear primitive

- [ ] `bus/clear.go`: `AutoClearEligible(role)` — enrolled, Claude provider, idle, no
  actionable inbox, no in-flight tasks, no reload marker, quiet window elapsed, hard-exclude
  edit/auto/harness
- [ ] `ClearAgent(role)` — two-step send-keys `/clear` injection, marker write, lifecycle
  `auto-clear` event
- [ ] `muxcode clear <role>` manual subcommand
- [ ] Unit tests: guard matrix (each guard independently flips the verdict), edit/auto
  exclusion pinned by test

### Phase 2: Daemon trigger and config

- [ ] `checkAutoClear()` in the watcher poll loop — fires once per completed task per role
- [ ] Config plumbing: `MUXCODE_AUTO_CLEAR_ROLES`, `MUXCODE_AUTO_CLEAR_QUIET_SECS` with
  config-file resolution
- [ ] Cooldown/idempotence: marker timestamp gates re-fire across poll cycles
- [ ] Docs: `configuration.md` env vars, `architecture.md` subsection, `CLAUDE.md`
  constraint bullet

### Phase 3: Integration test

- [ ] Create `scripts/test-auto-clear.sh` (isolated scratch `BUS_SESSION` where possible,
  following `test-echo-as-result.sh` pattern)
- [ ] Test: enrolled role + completed task + quiet window elapsed → clear fires once
  (marker written, lifecycle event logged)
- [ ] Test: pending actionable inbox message → no clear
- [ ] Test: `edit` listed in `MUXCODE_AUTO_CLEAR_ROLES` → never cleared
- [ ] Test: repeated poll cycles after one clear → no duplicate clears
- [ ] Test: unenrolled role with completed task → untouched
- [ ] Run integration test and verify all steps pass

## Status

Draft
