# Process Management

Background process lifecycle tracking with auto-notification on completion.

## Requirements

- `StartProc()` launches a background process and registers it in the bus directory
- `StopProc()` terminates a tracked process by name
- `CheckProcAlive()` verifies whether a tracked process is still running
- `RefreshProcStatus()` updates status for all tracked processes
- Watcher polls process status periodically and detects completion
- On process completion, a `proc-complete` system event is sent to the owning agent
- `CleanFinished()` removes completed process entries

## Key files

| File | Purpose |
|------|---------|
| `bus/proc.go` | `StartProc()`, `StopProc()`, `CheckProcAlive()`, `RefreshProcStatus()`, `CleanFinished()` |
| `watcher/watcher.go` | Periodic process status polling |
| `bus/config.go` | Path helpers for proc data files |
