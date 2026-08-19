# Daily Memory Rotation

Lazy daily rotation archives previous day's memory file on first write of the new day.

## Requirements

- On first memory write of the day, archive previous day's file to `{role}/YYYY-MM-DD.md`
- Configurable retention period (default 30 days), old archives purged automatically
- 7-day context window loads recent archives alongside current memory
- Search operations cover both current memory and archived files
- Rotation is lazy (triggered by write), not scheduled

## Key files

| File | Purpose |
|------|---------|
| `bus/rotation.go` | `NeedsRotation()`, `RotateMemory()`, `PurgeOldArchives()`, `ReadMemoryWithHistory()`, `AllMemoryEntriesWithArchives()` |
