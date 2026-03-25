# Workflow state machine

Track the workflow lifecycle as code moves from editing through validation to ready-for-commit. The state machine observes events (file edits, chain outcomes, agent messages) and maintains a single persisted state — it never blocks actions.

## States

Ordered integers for regression comparison. Any file edit regresses state to `editing` and clears accumulated outcomes.

| # | State | Color | Meaning |
|---|-------|-------|---------|
| 0 | `idle` | dim | No activity — session started or work committed |
| 1 | `editing` | cyan | Files being edited (trigger file written) |
| 2 | `analyzing` | cyan | Analyze agent processing file changes |
| 3 | `building` | yellow | Build command in progress |
| 4 | `build-failed` | red | Build failed — needs fix |
| 5 | `testing` | yellow | Test command in progress |
| 6 | `test-failed` | red | Tests failed — needs fix |
| 7 | `reviewing` | yellow | Review agent processing |
| 8 | `reviewed` | green | Review complete — ready for commit |
| 9 | `committing` | yellow | Git commit/push in progress |
| 10 | `deploying` | yellow | Deploy in progress |
| 11 | `deploy-failed` | red | Deploy failed |

Failure states (`build-failed`, `test-failed`, `deploy-failed`) are distinct from in-progress states because they represent a stuck workflow needing attention.

## State persistence

Single JSON file at `{BusDir(session)}/workflow-state.json`.

```json
{
  "state": "testing",
  "prev_state": "building",
  "since": 1711324800,
  "updated": 1711324860,
  "trigger": "chain:build:success",
  "files_changed": 3,
  "last_files": ["bus/hook.go", "bus/config.go", "cmd/hook.go"],
  "build_outcome": "success",
  "test_outcome": "",
  "review_outcome": "",
  "deploy_outcome": ""
}
```

- Single JSON (not JSONL) — only current state matters; history lives in lifecycle logs
- `trigger` records what caused the transition (colon-separated provenance)
- `since` = when current state was entered; `updated` = last write time
- `last_files` = filenames from the most recent trigger for context display
- Outcome fields accumulate through the workflow, reset on regression to `editing`

### Concurrency

Read-modify-write under exclusive `syscall.Flock` on `{BusDir}/lock/workflow.lock`, matching the pattern used by `WriteHookHistory` and watcher locks. Multiple hooks (bash, analyze, inbox-poll) can fire concurrently for the same session.

## State transitions

| Current state | Event | New state | Trigger source |
|---|---|---|---|
| any | file edit (trigger written) | `editing` | `ProcessAnalyzeHook` |
| `editing` | analyze event sent | `analyzing` | watcher `routeTrigger` |
| `analyzing` | build command detected | `building` | `ProcessBashHook` (CmdBuild) |
| `editing`/`analyzing` | build command detected | `building` | `ProcessBashHook` (CmdBuild) |
| `building` | build chain success | `testing` | `triggerChain` build:success |
| `building` | build chain failure | `build-failed` | `triggerChain` build:failure |
| `build-failed` | build command detected | `building` | `ProcessBashHook` (retry) |
| `testing`/any | test command detected | `testing` | `ProcessBashHook` (CmdTest) |
| `testing` | test chain success | `reviewing` | `triggerChain` test:success |
| `testing` | test chain failure | `test-failed` | `triggerChain` test:failure |
| `test-failed` | test command detected | `testing` | `ProcessBashHook` (retry) |
| `reviewing` | review completes (message to edit) | `reviewed` | watcher `checkInboxes` |
| `reviewed` | file edit | `editing` | regression |
| any | git commit detected | `committing` | `ProcessBashHook` (CmdGit+commit) |
| `committing` | git chain completes | `idle` | git commit success |
| any | deploy command detected | `deploying` | `ProcessBashHook` (CmdDeployApply) |
| `deploying` | deploy chain failure | `deploy-failed` | `triggerChain` deploy:failure |

### Regression rule

Any transition to `editing` from state >= `analyzing` resets all outcome fields (`build_outcome`, `test_outcome`, `review_outcome`, `deploy_outcome`). File changes potentially invalidate prior build/test/review results.

## New files

### `bus/workflow.go` — core state machine

```go
// WorkflowStatePath returns the state file path.
func WorkflowStatePath(session string) string

// WorkflowStateEntry is the persisted state.
type WorkflowStateEntry struct {
    State         WorkflowState `json:"state"`
    PrevState     WorkflowState `json:"prev_state"`
    Since         int64         `json:"since"`
    Updated       int64         `json:"updated"`
    Trigger       string        `json:"trigger"`
    FilesChanged  int           `json:"files_changed"`
    LastFiles     []string      `json:"last_files"`
    BuildOutcome  string        `json:"build_outcome"`
    TestOutcome   string        `json:"test_outcome"`
    ReviewOutcome string        `json:"review_outcome"`
    DeployOutcome string        `json:"deploy_outcome"`
}

// ReadWorkflowState reads current state from disk. Returns StateIdle if missing.
func ReadWorkflowState(session string) WorkflowStateEntry

// TransitionWorkflow atomically transitions state under flock.
// Returns true if the state actually changed.
func TransitionWorkflow(session string, newState WorkflowState, trigger string, opts ...TransitionOpt) bool

// TransitionOpt functional options for setting files, outcomes, etc.
type TransitionOpt func(*WorkflowStateEntry)
func WithFiles(files []string) TransitionOpt
func WithOutcome(phase string, outcome string) TransitionOpt

// FormatWorkflowState returns a human-readable state description.
func FormatWorkflowState(entry WorkflowStateEntry) string

// FormatWorkflowStateCompact returns a short colored indicator (for console/TUI).
func FormatWorkflowStateCompact(entry WorkflowStateEntry, width int) string

// WorkflowStateColor returns the ANSI color for a state.
func WorkflowStateColor(state WorkflowState) string
```

### `bus/workflow_test.go` — tests

- `TestWorkflowStatePath` — path construction
- `TestReadWorkflowStateEmpty` — returns `StateIdle` when file missing
- `TestTransitionWorkflow` — basic forward transition
- `TestTransitionWorkflowRegression` — edit during `reviewed` drops to `editing`, clears outcomes
- `TestTransitionWorkflowWithFiles` — files stored correctly
- `TestTransitionWorkflowWithOutcome` — outcome accumulation
- `TestFormatWorkflowState` — human-readable output
- `TestFormatWorkflowStateCompact` — compact colored output

### `cmd/workflow.go` — CLI subcommand

```
Usage: muxcode-agent-bus workflow [--json]
       muxcode-agent-bus workflow reset
```

- Default: human-readable state (name, duration, trigger, last files)
- `--json`: raw `WorkflowStateEntry` as JSON
- `reset`: transitions to `idle` (manual override)

## Integration points

Minimal changes to existing files (~25 lines total across all integration points).

### `bus/hook.go` — `ProcessAnalyzeHook` (+2 lines)

When trigger file is written, transition to `editing`:

```go
TransitionWorkflow(session, StateEditing, "hook:analyze:edit",
    WithFiles([]string{filePath}))
```

### `bus/hook.go` — `ProcessBashHook` (+3 lines)

After classifying command, transition for build/test/deploy/git:

```go
case CmdBuild:
    TransitionWorkflow(session, StateBuilding, "hook:bash:build")
case CmdTest:
    TransitionWorkflow(session, StateTesting, "hook:bash:test")
case CmdDeployApply:
    TransitionWorkflow(session, StateDeploying, "hook:bash:deploy")
```

### `cmd/hook.go` — `triggerChain` (+10 lines)

After chain message sent, transition based on event type and outcome:

```go
switch eventType {
case "build":
    if outcome == "success" {
        bus.TransitionWorkflow(session, bus.StateTesting, "chain:build:success",
            bus.WithOutcome("build", "success"))
    } else {
        bus.TransitionWorkflow(session, bus.StateBuildFail, "chain:build:failure",
            bus.WithOutcome("build", "failure"))
    }
case "test":
    if outcome == "success" {
        bus.TransitionWorkflow(session, bus.StateReviewing, "chain:test:success",
            bus.WithOutcome("test", "success"))
    } else {
        bus.TransitionWorkflow(session, bus.StateTestFail, "chain:test:failure",
            bus.WithOutcome("test", "failure"))
    }
case "deploy":
    if outcome != "success" {
        bus.TransitionWorkflow(session, bus.StateDeployFail, "chain:deploy:failure",
            bus.WithOutcome("deploy", "failure"))
    }
}
```

### `watcher/watcher.go` — `routeTrigger` (+1 line)

When analyze event sent, transition to `analyzing`:

```go
bus.TransitionWorkflow(w.session, bus.StateAnalyzing, "watcher:analyze-route",
    bus.WithFiles(files))
```

### `watcher/watcher.go` — `checkInboxes` (+5 lines)

Detect review→edit messages to transition to `reviewed`:

```go
if role == "edit" && size > prev && size > 0 {
    if bus.HasNewMessageFrom(session, "edit", "review") {
        bus.TransitionWorkflow(w.session, bus.StateReviewed, "watcher:review-complete")
    }
}
```

### `bus/setup.go` — `Init` (+2 lines)

Reset workflow state on session re-init:

```go
if err := resetFile(WorkflowStatePath(session), reInit); err != nil {
    return err
}
```

### `main.go` (+2 lines)

Register `workflow` subcommand in switch and usage string.

## Visibility

### TUI dashboard (`tui/model.go`)

Add `WORKFLOW` section between session info and AGENTS sections:

```
  WORKFLOW  ● testing  ⟵ build:success  3 files  2m ago
```

Color-coded by state. Reads `WorkflowStateEntry` from disk on each render tick.

### Console left-panes (`bus/console.go`)

Add workflow state line to `ConsoleHeader` (visible across all agent consoles):

```
  workflow: testing (2m)
```

Requires adding `session` parameter to `ConsoleHeader` (one signature change, one call site update in `cmd/console.go`).

## Design decisions

- **Observational, not blocking**: Never prevents actions. If tests run before building, state goes directly to `testing`.
- **Single writer via flock**: Concurrent hooks are safe. No goroutines or channels needed.
- **Trigger provenance**: Colon-separated format (`hook:bash:build`, `chain:test:success`, `watcher:analyze-route`) — human-readable and grep-able.
- **Regression simplicity**: Numeric state ordering makes "should we regress?" a trivial integer comparison.
- **Lifecycle integration**: Every transition calls `LogLifecycle()` for audit trail.
- **Backwards-compatible**: Existing chains work unchanged; state machine is purely additive.
