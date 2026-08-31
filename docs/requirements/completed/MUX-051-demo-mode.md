# Demo Mode

Scripted demo scenarios with bus messages, window switching, and GIF capture.

## Requirements

- `RunDemo()` executes a named scenario with scripted bus interactions
- `BuiltinScenarios()` provides pre-defined demo sequences
- `ScaleDelay()` adjusts timing for faster or slower playback
- Scenarios send bus messages between agents to simulate real workflows
- Tmux window switching included in demo scripts for visual effect
- Suitable for screen recording and GIF capture of multi-agent interactions

## Key files

| File | Purpose |
|------|---------|
| `bus/demo.go` | `RunDemo()`, `BuiltinScenarios()`, `ScaleDelay()` |
| `cmd/demo.go` | CLI handler for demo subcommand |

## Status

Complete
