# Loop Detection

Automatic detection of repetitive agent patterns with escalation to edit agent.

## Requirements

- `DetectCommandLoop()` identifies repeated tool/command patterns in agent history
- `DetectMessageLoop()` identifies repeated bus message exchanges between agents
- `CheckLoops()` and `CheckAllLoops()` run both detectors across agents
- Watcher checks for loops every 60 seconds
- Dedup cooldown prevents redundant alerts for the same loop
- Loop detection escalates to the edit agent via `loop-detected` system action
- System actions (`loop-detected`, `compact-recommended`, etc.) are excluded from loop detection themselves

## Key files

| File | Purpose |
|------|---------|
| `bus/guard.go` | `ReadHistory()`, `DetectCommandLoop()`, `DetectMessageLoop()`, `CheckLoops()`, `CheckAllLoops()` |
| `watcher/watcher.go` | Periodic loop check integration |
| `bus/inbox.go` | `isSystemAction()` exclusion filter |

## Status

Complete
