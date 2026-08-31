# Session Inspection

Agent status overview and message history querying.

## Requirements

- `GetAgentStatus()` reports individual agent state (idle, active, inbox count)
- `GetAllAgentStatus()` provides a full session overview of all agents
- `ReadLogHistory()` queries `log.jsonl` for message history with filtering
- `PreCommitCheck()` validates no agents have pending work before commits
- CLI exposes inspection data for dashboard and ad-hoc queries
- Status detection uses tmux pane capture to determine agent activity

## Key files

| File | Purpose |
|------|---------|
| `bus/inspect.go` | `GetAgentStatus()`, `GetAllAgentStatus()`, `ReadLogHistory()`, `PreCommitCheck()` |
| `cmd/inspect.go` | CLI handler for inspect subcommands |
| `bus/config.go` | `PaneTarget()`, `AgentPane()` for tmux pane resolution |

## Status

Complete
