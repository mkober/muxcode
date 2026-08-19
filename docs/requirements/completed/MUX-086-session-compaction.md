# Session Compaction

Manual session summary save/restore for context preservation across restarts.

## Requirements

- `muxcode session compact` triggers a summary of the current agent session
- Summary is saved to `.muxcode/memory/` as a persistent memory file per role
- `muxcode memory context` retrieves stored context for session resume
- Memory files survive session teardown and bus directory cleanup
- Summaries are injected into agent system prompts on session restart
- Each role maintains its own memory file independently

## Key files

| File | Purpose |
|------|---------|
| `cmd/session.go` | CLI handler for `session compact` subcommand |
| `cmd/memory.go` | CLI handler for `memory context` subcommand |
| `bus/config.go` | Path helpers for `.muxcode/memory/` directory |
| `bus/agent.go` | `buildSystemPrompt()` injects session resume context |
| `harness/prompt.go` | `BuildSystemPrompt()` injects session resume for harness agents |
