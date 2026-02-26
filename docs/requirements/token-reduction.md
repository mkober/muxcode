# Token Usage Reduction — Refactoring Plan

## Context

The multi-agent MUXcode system burns excessive tokens due to three primary causes:
1. **Auto-CC duplication**: Every chain message (build→test, test→review) is copied to edit's inbox via auto-CC, causing edit to process ~5 intermediate messages per build cycle that provide no actionable info
2. **Analyst spam on success**: The analyst agent is notified on every build/test outcome (including routine success), consuming ~3-4K tokens per notification for trivial "all good" responses
3. **Watcher overhead**: Loop detection scans all history every 30s; inbox stat calls poll all 13 known roles every 5s; cron/proc/spawn files are parsed every cycle even when empty

This plan addresses the top 3 by impact-to-effort ratio. On-demand agent spawning (runner, watch, analyst) is recorded as a follow-up item.

---

## Step 1 — Add `SendNoCC` function to `bus/inbox.go`

A variant of `Send()` that skips auto-CC. Chain and subscription fan-out messages will use this.

**File**: `tools/muxcode-agent-bus/bus/inbox.go`

- Add `SendNoCC(session string, m Message) error` — identical to `Send()` but omits lines 37-41 (auto-CC block)
- Extract the shared write logic into a private `sendMessage(session string, m Message, autoCC bool) error` to avoid duplication
- `Send()` calls `sendMessage(session, m, true)` and `SendNoCC()` calls `sendMessage(session, m, false)`

**Tests**: `bus/inbox_test.go`
- `TestSendNoCC_SkipsAutoCC` — send from build to test, verify edit inbox is empty
- `TestSend_StillCCs` — send from build to test via `Send()`, verify edit inbox has the message

---

## Step 2 — Use `SendNoCC` in chain execution (`cmd/chain.go`)

Switch chain intermediate messages and analyst notifications to `SendNoCC`.

**File**: `tools/muxcode-agent-bus/cmd/chain.go`

- **Line 86**: Replace `bus.Send(session, msg)` → `bus.SendNoCC(session, msg)` for the primary chain action
- **Line 114**: Replace `bus.Send(session, aMsg)` → `bus.SendNoCC(session, aMsg)` for analyst notification
- **Lines 93-96**: Remove the explicit `Notify(session, "edit")` call — with no auto-CC, there's nothing for edit to see

Edit still receives:
- Direct chain messages (build failure → edit, test failure → edit) — these use `SendTo: "edit"` which bypasses auto-CC anyway
- Review results — the review agent explicitly reports back to edit

---

## Step 3 — Use `SendNoCC` in subscription fan-out (`bus/subscribe.go`)

**File**: `tools/muxcode-agent-bus/bus/subscribe.go`

- In `FireSubscriptions()` (~line 191), replace `Send(session, msg)` → `SendNoCC(session, msg)`
- Subscription fan-out messages are derivative of chain events and should not duplicate to edit

---

## Step 4 — Add `NotifyAnalystOn` to `EventChain` (`bus/profile.go`)

Make analyst notifications outcome-conditional instead of always-on.

**File**: `tools/muxcode-agent-bus/bus/profile.go`

- Add `NotifyAnalystOn []string` field to `EventChain` struct (JSON: `"notify_analyst_on"`)
  - Valid values: `"success"`, `"failure"`, `"unknown"`, `"*"` (wildcard)
- Replace `ChainNotifyAnalyst(eventType string) bool` with `ChainShouldNotifyAnalyst(eventType, outcome string) bool`:
  - If `NotifyAnalystOn` is set: check if outcome matches any entry (or `"*"`)
  - If `NotifyAnalystOn` is empty: fall back to `NotifyAnalyst` bool (backward compat with custom configs)
- Update `DefaultConfig()` chains:
  - `build`: `NotifyAnalystOn: []string{"failure", "unknown"}` (was `NotifyAnalyst: true`)
  - `test`: `NotifyAnalystOn: []string{"failure", "unknown"}` (was `NotifyAnalyst: true`)
  - `deploy`: `NotifyAnalystOn: []string{"*"}` (keep all — deploy outcomes always matter)

**File**: `tools/muxcode-agent-bus/cmd/chain.go`

- **Line 68** (dry-run): Replace `ChainNotifyAnalyst(eventType)` → `ChainShouldNotifyAnalyst(eventType, outcome)`
- **Line 102**: Same replacement

**Tests**: `bus/profile_test.go`
- `TestChainShouldNotifyAnalyst_FailureOnly` — build failure triggers, success does not
- `TestChainShouldNotifyAnalyst_Wildcard` — deploy with `"*"` triggers on all outcomes
- `TestChainShouldNotifyAnalyst_LegacyFallback` — `NotifyAnalyst: true` without `NotifyAnalystOn` still works
- Update any existing `TestChainNotifyAnalyst` tests

---

## Step 5 — Watcher efficiency improvements (`watcher/watcher.go`)

### 5a. Increase loop detection interval: 30s → 60s

- **Line 372**: Change `now-w.lastLoopCheck < 30` → `now-w.lastLoopCheck < 60`

### 5b. Lazy cron/proc/spawn loading — skip if file is empty or missing

- In `loadCron()` (line 208): before calling `ReadCronEntries()`, check `os.Stat(bus.CronPath(w.session))`; if file size == 0, set `w.cronEntries = nil` and return early
- In `checkProcs()` (line 277): before calling `RefreshProcStatus()`, check `os.Stat(bus.ProcPath(w.session))`; if size == 0, return early
- In `checkSpawns()` (line 319): before calling `RefreshSpawnStatus()`, check `os.Stat(bus.SpawnPath(w.session))`; if size == 0, return early

### 5c. Cache running-state flags to skip tmux checks

- Add `hasRunningProcs bool` and `hasRunningSpawns bool` fields to `Watcher`
- In `checkProcs()`: after `RefreshProcStatus()`, set `w.hasRunningProcs = len(running) > 0` (where `running` is filtered from proc entries). On subsequent cycles, if `!hasRunningProcs` AND empty-file check passes (5b), skip entirely.
- Same for `checkSpawns()` with `hasRunningSpawns`
- Reset flags when file size changes (indicates new proc/spawn was started)

**Tests**: `watcher/watcher_test.go` (new file)
- `TestCheckCron_SkipsEmptyFile` — verify no `ReadCronEntries` call when file is empty
- `TestCheckLoops_60sInterval` — verify loop check respects 60s interval

---

## Step 6 — Update CLAUDE.md docs

Add a note about the `NotifyAnalystOn` config field and `SendNoCC` function to the code index sections.

---

## Summary of files to modify

| File | Changes |
|------|---------|
| `bus/inbox.go` | Add `SendNoCC()`, extract `sendMessage()` helper |
| `bus/inbox_test.go` | New tests for `SendNoCC` |
| `bus/profile.go` | Add `NotifyAnalystOn` field, new `ChainShouldNotifyAnalyst()` |
| `bus/profile_test.go` | New/updated tests for analyst notification |
| `bus/subscribe.go` | Switch `FireSubscriptions()` to `SendNoCC()` |
| `cmd/chain.go` | Use `SendNoCC()`, conditional analyst via `ChainShouldNotifyAnalyst()`, remove edit CC notify |
| `watcher/watcher.go` | Loop interval 30→60, lazy cron/proc/spawn checks, running-state cache |
| `watcher/watcher_test.go` | New tests for watcher optimizations |
| `CLAUDE.md` | Doc updates |

## Follow-up (not in this PR)

**On-demand agent spawning**: Convert runner, watch, and analyst from always-on to deferred (launched on first message). Tmux windows still created for left-pane pollers, but agent process starts only when a bus message targets the role. Uses existing spawn infrastructure. Recorded in backlog.

## Verification

1. `cd tools/muxcode-agent-bus && go vet ./...` — no errors
2. `cd tools/muxcode-agent-bus && go test ./...` — all tests pass
3. Manual test: start a MUXcode session, trigger a build, verify:
   - Edit does NOT receive CC messages for build→test and test→review chain steps
   - Analyst is NOT notified on build/test success
   - Analyst IS notified on build/test failure
   - Review results still reach edit directly
   - Subscription fan-out does not CC edit
4. Verify watcher logs show 60s loop check interval (not 30s)
