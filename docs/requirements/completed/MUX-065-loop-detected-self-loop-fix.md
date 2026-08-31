# Loop-Detected Self-Loop Fix

System action exclusion and increased dedup cooldown prevent false-positive message loop detection.

## Requirements

- `isSystemAction()` filters infrastructure actions (`loop-detected`, `compact-recommended`, `proc-complete`, `spawn-complete`, `ollama-down`, `ollama-recovered`, `ollama-restarting`, `agent-down`, `agent-restarting`, `agent-recovered`) from loop detection
- Dedup cooldown increased from 300s to 600s to exceed the detection window
- Prevents `loop-detected` messages from themselves triggering further loop alerts

## Key files

| File | Purpose |
|------|---------|
| `bus/guard.go` | `isSystemAction()`, `DetectMessageLoop()` with cooldown and exclusion logic |

## Status

Complete
