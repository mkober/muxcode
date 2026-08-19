# Cron Scheduling

Interval-based scheduled tasks with watcher integration and execution history.

## Requirements

- Support interval expressions: `@every 5m`, `@hourly`, `@daily`
- Cron jobs stored as JSONL in the bus directory
- Watcher polls cron schedule and fires due jobs automatically
- Each execution recorded in JSONL history with timestamp and outcome
- CLI commands for create, list, delete, and history viewing
- Jobs send bus messages to target agents on trigger

## Key files

| File | Purpose |
|------|---------|
| `bus/cron.go` | Cron structs, parsing, CRUD, execution, formatting |
| `watcher/watcher.go` | Polls cron schedule and fires due jobs |
| `cmd/cron.go` | CLI handler for cron subcommands |
| `bus/config.go` | Path helpers for cron data files |
