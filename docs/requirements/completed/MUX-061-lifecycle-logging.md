# Lifecycle Logging

Persistent JSONL logs recording session-level events that survive session cleanup.

## Requirements

- Logs written to `~/.config/muxcode/logs/{session}.log` in JSONL format
- Records launcher sequence, watcher events, agent launches, auto-accept, and cleanup
- Filterable via `lifecycle show` with `--source`, `--level`, `--event`, `--since` flags
- Automatic rotation at 1000 entries (configurable via `MUXCODE_LIFECYCLE_LOG_MAX`)
- `lifecycle purge --days N` removes logs older than N days
- Logs persist outside `/tmp` bus directory so they survive session teardown

## Key files

| File | Purpose |
|------|---------|
| `bus/lifecycle.go` | `LogLifecycle()`, `ReadLifecycleLog()`, `FilterLifecycleLog()`, `PurgeLifecycleLogs()` |
| `cmd/lifecycle.go` | CLI subcommand handler for `lifecycle show` and `lifecycle purge` |
