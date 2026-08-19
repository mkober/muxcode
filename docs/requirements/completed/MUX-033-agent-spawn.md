# Agent Spawn

Create temporary agent sessions for one-off tasks, collect result, tear down.

## Requirements

- `StartSpawn()` creates a new tmux window with a temporary agent for a specific task
- `StopSpawn()` terminates the spawned agent and cleans up its tmux window
- `GetSpawnResult()` retrieves the output from a completed spawn
- `RefreshSpawnStatus()` updates status for all active spawns
- `CleanFinishedSpawns()` removes completed spawn entries
- Watcher monitors spawn lifecycle and sends `spawn-complete` event on finish
- Spawned agents are fully isolated with their own inbox and history

## Key files

| File | Purpose |
|------|---------|
| `bus/spawn.go` | `StartSpawn()`, `StopSpawn()`, `GetSpawnResult()`, `RefreshSpawnStatus()`, `CleanFinishedSpawns()` |
| `watcher/watcher.go` | Spawn status polling and completion detection |
| `bus/config.go` | Path helpers for spawn data files |
