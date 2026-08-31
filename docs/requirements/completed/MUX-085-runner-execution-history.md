# Runner Execution History

Left-pane poller for run window showing command history, exit codes, and output.

## Requirements

- Left-pane script displays recent command executions from the runner agent
- Shows command name, exit code, and truncated output for each entry
- Color-coded exit codes: green for success, red for failure
- Reads from runner's `log.jsonl` history file
- Auto-refreshes on a polling interval
- Formatted for narrow tmux left-pane display

## Key files

| File | Purpose |
|------|---------|
| `scripts/muxcode-runner-log.sh` | Left-pane poller script for runner execution history |

## Status

Complete
