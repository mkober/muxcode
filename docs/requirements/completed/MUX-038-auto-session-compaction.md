# Auto Session Compaction

Watcher-triggered compaction alerts when agent context approaches limits.

## Requirements

- `CheckCompaction()` evaluates whether any agent is approaching context limits
- `CheckRoleCompaction()` checks a specific role's context usage
- `FormatCompactAlert()` produces a human-readable alert message
- `FilterNewCompactAlerts()` deduplicates alerts to avoid repeated notifications
- Watcher runs compaction checks periodically alongside other health checks
- Alert sent as `compact-recommended` system action to the affected agent
- Does not auto-compact — only advises the agent to initiate compaction

## Key files

| File | Purpose |
|------|---------|
| `bus/compact.go` | `CheckCompaction()`, `CheckRoleCompaction()`, `FormatCompactAlert()`, `FilterNewCompactAlerts()` |
| `watcher/watcher.go` | Periodic compaction check integration |

## Status

Complete
