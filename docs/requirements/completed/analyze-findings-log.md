# Analyze Findings Log

Left-pane poller for the analyze window displaying filtered analyst findings from bus history.

## Requirements

- Filter `log.jsonl` for analyst response messages
- Display total findings count and recent entries in the left pane
- Show full payload of the latest finding
- Watcher runs as a background process started at session init
- Refresh on new findings via trigger file monitoring

## Key files

| File | Purpose |
|------|---------|
| `scripts/muxcode-analyze-log.sh` | Left-pane poller script for analyze window |
