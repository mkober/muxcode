# Agent diagnostic command

Add a `muxcode diagnose <role>` command that performs automated root cause analysis when an agent isn't responding to messages. Instead of manually inspecting pane state, lifecycle logs, notified IDs, delivery status, and daemon behavior, a single command collects all evidence and produces a structured diagnosis with actionable remediation steps.

## Problem

### Observed behavior

When an agent stops responding to messages (e.g., the commit agent after a session restart), debugging requires the user or another agent to manually run 5-10 commands across different subsystems:

1. `muxcode status --json` — check agent health and inbox count
2. `tmux capture-pane` — inspect pane state (idle? busy? stuck?)
3. `muxcode lifecycle show --event idle-wake,idle-transition,inbox-notify` — trace notification flow
4. `ls /tmp/muxcode-bus-*/notified-*.ids` — check notified ID markers
5. `cat /tmp/muxcode-bus-*/delivery/*.status` — check delivery state
6. `muxcode inbox --peek` — examine pending messages
7. Cross-reference timestamps between lifecycle events to find gaps

This produced the diagnosis in the example conversation:
- Before session restart: `inbox-notify commit → idle-wake commit` (working, 1-2s gap)
- After session restart: `inbox-notify commit → idle-task-rescue commit` (broken, 30s gap — `idle-wake` never fires)

The manual process takes 2-5 minutes, requires deep knowledge of the notification pipeline, and is error-prone. An automated diagnostic could produce the same analysis in seconds.

### Root causes this should detect

| Failure mode | Evidence pattern |
|-------------|-----------------|
| **Stale notified IDs** | Agent idle + inbox has messages + all IDs in notified set + marker older than 15s |
| **Missed send-keys** | `idle-wake` logged but agent still at `❯` prompt with unconsumed inbox |
| **Idle detection failure** | Agent at `❯` prompt but `IsAgentIdle()` returns false (pane capture mismatch) |
| **Daemon not waking** | `inbox-notify` logged but no `idle-wake` within 10s, agent is idle |
| **Post-restart wake gap** | `idle-transition` logged after restart, but subsequent `idle-wake` never fires |
| **Provider mismatch** | Agent runs on non-hook provider but daemon skips non-hook wake-up (cooldown) |
| **Reload marker stuck** | `IsReloading()` returns true (stale marker from failed reload) — daemon skips role entirely |
| **Pending input blocking** | `HasPendingInput()` returns true — user mid-typing, injection deferred indefinitely |
| **HasActionableMessages false** | Inbox has messages but all are response/event type — no wake-up needed (not a bug) |
| **Daemon dead** | Keepalive stale (>30s) — daemon isn't running the poll loop |

## Requirements

### Acceptance criteria

- [ ] `muxcode diagnose <role>` produces a structured report covering: agent state, inbox state, notification state, lifecycle event timeline, and root cause diagnosis
- [ ] The command identifies the specific failure mode from the table above (or reports "no issue detected" if the agent is healthy)
- [ ] The command provides actionable remediation steps for each detected failure mode (e.g., "Clear stale notified IDs: `muxcode notify --clear <role>`", "Restart daemon: `muxcode watch --restart`")
- [ ] The diagnosis completes in under 2 seconds (no network calls, all local state)
- [ ] Output works in both human-readable (default) and JSON (`--json`) formats
- [ ] The command can be run by any agent (not just humans) to self-diagnose or diagnose peers
- [ ] `muxcode diagnose --all` runs diagnosis for all known roles and reports a summary
- [ ] All existing tests pass (`go test ./...`)
- [ ] New tests cover each failure mode detection path

### Out of scope

- Automatic remediation (this command diagnoses only — the user or agent decides whether to act)
- Modifying the notification pipeline itself (that's covered by the notification-dedup spec)
- Real-time monitoring or continuous diagnosis (single-shot command)

## Technical approach

### Core concept: evidence-based diagnosis pipeline

The command collects evidence from every subsystem involved in message delivery, then runs a series of diagnostic checks that pattern-match against known failure modes. Each check produces a finding with severity, evidence, and remediation.

```go
type DiagnosticReport struct {
    Role        string              `json:"role"`
    Timestamp   int64               `json:"timestamp"`
    AgentState  AgentStateEvidence  `json:"agent_state"`
    InboxState  InboxStateEvidence  `json:"inbox_state"`
    NotifyState NotifyStateEvidence `json:"notify_state"`
    DaemonState DaemonStateEvidence `json:"daemon_state"`
    Timeline    []TimelineEvent     `json:"timeline"`
    Findings    []DiagnosticFinding `json:"findings"`
}

type DiagnosticFinding struct {
    Severity    string   `json:"severity"`    // "critical", "warning", "info"
    FailureMode string   `json:"failure_mode"` // matches table above
    Summary     string   `json:"summary"`
    Evidence    []string `json:"evidence"`
    Remediation []string `json:"remediation"`
}
```

### Evidence collection (Phase 1)

Gather state from all subsystems in parallel:

```go
type AgentStateEvidence struct {
    IsIdle          bool   `json:"is_idle"`
    IsAlive         bool   `json:"is_alive"`
    IsStopped       bool   `json:"is_stopped"`
    IsReloading     bool   `json:"is_reloading"`
    Provider        string `json:"provider"`
    SupportsHooks   bool   `json:"supports_hooks"`
    HasPendingInput bool   `json:"has_pending_input"`
    PaneLastLine    string `json:"pane_last_line"`
}

type InboxStateEvidence struct {
    MessageCount     int    `json:"message_count"`
    ActionableCount  int    `json:"actionable_count"`
    OldestMessageAge int64  `json:"oldest_message_age_secs"`
    NewestMessageAge int64  `json:"newest_message_age_secs"`
    Messages         []MessageSummary `json:"messages"`
}

type NotifyStateEvidence struct {
    NotifiedIDCount    int    `json:"notified_id_count"`
    UnnotifiedCount    int    `json:"unnotified_count"`
    MarkerAge          int64  `json:"marker_age_secs"`   // -1 if no marker
    IsMarkerStale      bool   `json:"is_marker_stale"`   // >15s
    TriggerNotifyAge   int64  `json:"trigger_notify_age_secs"`
    IsPolling          bool   `json:"is_polling"`
    IsWaiting          bool   `json:"is_waiting"`
}

type DaemonStateEvidence struct {
    IsAlive            bool  `json:"is_alive"`
    KeepaliveAge       int64 `json:"keepalive_age_secs"`
    IsKeepaliveStale   bool  `json:"is_keepalive_stale"` // >30s
}
```

### Lifecycle timeline analysis (Phase 2)

Read recent lifecycle events for the target role and build a timeline. Look for gaps in the expected event chain:

```
Expected chain: inbox-notify → idle-wake → (agent processes) → idle-transition
Expected chain after restart: startup-wake → idle-transition → idle-wake (on next message)
```

```go
// buildTimeline reads the last N lifecycle events for a role and
// annotates gaps in the expected notification chain.
func buildTimeline(session, role string, limit int) []TimelineEvent {
    entries := bus.FilterLifecycleLog(session, bus.LifecycleFilterOpts{
        Limit: limit,
    })
    // Filter to events mentioning this role
    var events []TimelineEvent
    for _, e := range entries {
        if strings.Contains(e.Detail, role) || e.Event == "idle-wake" || ... {
            events = append(events, TimelineEvent{
                Timestamp: e.TS,
                Event:     e.Event,
                Detail:    e.Detail,
                Gap:       0, // computed below
            })
        }
    }
    // Annotate gaps between expected event pairs
    annotateGaps(events, role)
    return events
}
```

### Diagnostic checks (Phase 3)

Run each failure-mode check against the collected evidence:

```go
var diagnosticChecks = []DiagnosticCheck{
    checkDaemonDead,
    checkStaleNotifiedIDs,
    checkMissedSendKeys,
    checkIdleDetectionFailure,
    checkDaemonNotWaking,
    checkPostRestartWakeGap,
    checkProviderMismatch,
    checkReloadMarkerStuck,
    checkPendingInputBlocking,
    checkNoActionableMessages,
}

type DiagnosticCheck func(report *DiagnosticReport) *DiagnosticFinding
```

Each check returns nil if no issue detected, or a `DiagnosticFinding` with severity, evidence lines, and remediation steps.

### Human-readable output format

```
$ muxcode diagnose commit

  Agent: commit (claude, hooks: yes)
  State: idle (at ❯ prompt)
  Health: alive

  Inbox: 2 messages (1 actionable)
    [plan→commit] "git mv docs/requirements/drafts/..."  (32s ago)
    [daemon→compact-recommended] "Context usage high"    (45s ago)

  Notification state:
    Notified IDs: 1 (marker age: 47s — STALE)
    Unnotified: 1 message
    Trigger file: 32s ago

  Daemon: alive (keepalive: 3s ago)

  Timeline (last 60s):
    14:38:11  inbox-notify    commit
    14:38:11  --- EXPECTED idle-wake within 10s — NOT FOUND ---
    14:38:41  idle-task-rescue commit (30s gap)

  ⚠  FINDING: Daemon not waking agent (critical)
     The daemon logged inbox-notify for commit but never fired idle-wake
     within the expected 10s window. The agent is idle with unnotified
     messages. This happened 10 times in the last 5 minutes.

     Evidence:
     - inbox-notify at 14:38:11, no idle-wake followed
     - Agent is idle (at ❯ prompt)
     - 1 unnotified message in inbox
     - Notified IDs marker is stale (47s)

     Remediation:
     1. Clear stale notification state: muxcode notify --clear commit
     2. Restart daemon: kill $(cat /tmp/muxcode-bus-muxcode/watcher.pid) && muxcode watch &
     3. Manual wake: tmux send-keys -t muxcode:commit.1 "You have new messages" Enter
```

### JSON output format

Same data as `DiagnosticReport` struct, serialized with `json.MarshalIndent`. Useful for agents calling diagnose and parsing the result programmatically.

## Key files

| File | Change |
|------|--------|
| `tools/muxcode/bus/diagnose.go` | New file — `DiagnosticReport`, evidence structs, `CollectEvidence()`, `RunDiagnostics()`, `BuildTimeline()`, all diagnostic check functions, `FormatDiagnosticReport()`, `FormatDiagnosticJSON()` |
| `tools/muxcode/cmd/diagnose.go` | New file — `Diagnose()` command handler, `--json` flag, `--all` flag |
| `tools/muxcode/main.go` | Add `"diagnose"` to `knownSubcommands` and route to `cmd.Diagnose()` |
| `tools/muxcode/bus/diagnose_test.go` | New file — tests for each diagnostic check with synthetic evidence, timeline gap detection, report formatting |

## Implementation

### Phase 1: Evidence collection framework

Build the data structures and collection functions for gathering state from all subsystems.

- [ ] Create `bus/diagnose.go` with `DiagnosticReport`, `AgentStateEvidence`, `InboxStateEvidence`, `NotifyStateEvidence`, `DaemonStateEvidence`, `TimelineEvent`, `DiagnosticFinding` structs
- [ ] Add `CollectAgentState(session, role) AgentStateEvidence` — calls `IsAgentIdle`, `IsAgentAlive`, `IsAgentStopped`, `IsReloading`, `ResolveProvider`, `HasPendingInput`, captures last pane line
- [ ] Add `CollectInboxState(session, role) InboxStateEvidence` — calls `Peek`, `HasActionableMessages`, computes message ages and summaries
- [ ] Add `CollectNotifyState(session, role) NotifyStateEvidence` — reads notified IDs file, computes `UnnotifiedMessages`, checks marker age, `IsPolling`, `IsWaiting`
- [ ] Add `CollectDaemonState(session) DaemonStateEvidence` — checks keepalive file age via `IsKeepaliveStale`, `TouchKeepalive` path
- [ ] Add `CollectEvidence(session, role) DiagnosticReport` — orchestrates all collectors
- [ ] Add tests for each collector with synthetic bus directory state
- [ ] **Verify**: `cd tools/muxcode && go test ./...` — all tests pass
- [ ] **Verify**: `cd tools/muxcode && go vet ./...` — no issues

### Phase 2: Lifecycle timeline analysis

Build the timeline from lifecycle logs and annotate gaps in expected event chains.

- [ ] Add `TimelineEvent` struct with `Timestamp`, `Event`, `Detail`, `GapSecs`, `GapNote` fields
- [ ] Add `BuildTimeline(session, role, limit) []TimelineEvent` — filters lifecycle entries for role-relevant events
- [ ] Add `annotateGaps(events, role)` — detects missing `idle-wake` after `inbox-notify`, missing `idle-transition` after `startup-wake`, etc.
- [ ] Add `countRepeatedFailures(events, role) int` — counts consecutive `inbox-notify` without `idle-wake` to quantify the pattern
- [ ] Add tests for timeline building with synthetic lifecycle log entries
- [ ] **Verify**: `cd tools/muxcode && go test ./...` — all tests pass

### Phase 3: Diagnostic checks

Implement each failure-mode detector as a standalone function.

- [ ] Add `DiagnosticCheck` type: `func(*DiagnosticReport) *DiagnosticFinding`
- [ ] Implement `checkDaemonDead` — keepalive stale >30s
- [ ] Implement `checkStaleNotifiedIDs` — agent idle + unnotified messages + marker stale >15s
- [ ] Implement `checkMissedSendKeys` — `idle-wake` in timeline but agent still idle with unconsumed inbox
- [ ] Implement `checkIdleDetectionFailure` — pane shows `❯` but `IsAgentIdle` returned false
- [ ] Implement `checkDaemonNotWaking` — `inbox-notify` without `idle-wake` within 10s, agent is idle
- [ ] Implement `checkPostRestartWakeGap` — `idle-transition` after restart but no subsequent `idle-wake`
- [ ] Implement `checkProviderMismatch` — non-hook provider with 60s cooldown blocking wake-up
- [ ] Implement `checkReloadMarkerStuck` — `IsReloading` true for >60s (stale marker)
- [ ] Implement `checkPendingInputBlocking` — `HasPendingInput` true, preventing injection
- [ ] Implement `checkNoActionableMessages` — inbox has messages but none are requests (informational — not a bug)
- [ ] Add `RunDiagnostics(report *DiagnosticReport)` — runs all checks, populates `report.Findings`
- [ ] Add tests for each check with crafted evidence triggering/not-triggering the finding
- [ ] **Verify**: `cd tools/muxcode && go test ./...` — all tests pass

### Phase 4: Output formatting and CLI

Build the human-readable and JSON formatters, and wire up the CLI command.

- [ ] Add `FormatDiagnosticReport(report) string` — human-readable output with sections, colors (Dracula), and severity icons
- [ ] Add `FormatDiagnosticJSON(report) string` — JSON with `json.MarshalIndent`
- [ ] Create `cmd/diagnose.go` with `Diagnose(args)` — parses role arg, `--json` flag, `--all` flag
- [ ] `--all` mode: iterates `KnownRoles`, runs `CollectEvidence` + `RunDiagnostics` for each, prints summary table
- [ ] Add `"diagnose"` to `knownSubcommands` in `main.go` and route to `cmd.Diagnose()`
- [ ] Add tests for formatting output (human-readable structure, JSON valid)
- [ ] **Verify**: `cd tools/muxcode && go test ./...` — all tests pass
- [ ] **Verify**: `cd tools/muxcode && go vet ./...` — no issues
- [ ] **Verify**: `make install` — binary builds and installs with diagnose command available

## Status

Draft
