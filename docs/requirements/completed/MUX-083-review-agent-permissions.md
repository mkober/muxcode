# Review Agent Permissions

Expanded review agent tool profile to support process substitution diffs and data processing utilities.

## Requirements

- Add `diff <(...)` process substitution pattern to review role tool permissions
- Add `python3` to review tool profile for data transformation and fallback parsing
- Add `jq` to review tool profile for JSON inspection of bus artifacts
- Existing review permissions (read, glob, grep) remain unchanged

## Key files

| File | Purpose |
|------|---------|
| `bus/profile.go` | Review role `Tools` list with `diff <(*)`, `python3`, `jq` entries |

## Status

Complete
