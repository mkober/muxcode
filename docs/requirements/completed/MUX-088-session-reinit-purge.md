# Session Re-init Purge

Stale data cleanup on session restart -- preserves memory, purges ephemeral files.

## Requirements

- `Init()` detects an existing bus directory and triggers re-init cleanup
- `resetFile()` truncates or removes individual ephemeral files
- `purgeStaleFiles()` removes lock files, trigger files, and other transient state
- Memory files in `.muxcode/memory/` are preserved across re-init
- Inbox files, log history, and process/spawn/cron state are purged
- Re-init is idempotent and safe to run multiple times

## Key files

| File | Purpose |
|------|---------|
| `bus/setup.go` | `Init()`, `resetFile()`, `purgeStaleFiles()` |
| `bus/config.go` | Path definitions for all bus directory files |

## Status

Complete
