# Agent Debug Skill

Edit-agent skill for diagnosing other agents via tmux pane inspection.

## Requirements

- Capture agent pane content via `tmux capture-pane` for inspection
- Detect agent idle state (at prompt) vs active (running command)
- Check agent inbox for pending messages and health status
- Multi-agent sweep mode to check all agents in a single invocation
- Troubleshooting table mapping common symptoms to diagnostic steps

## Key files

| File | Purpose |
|------|---------|
| `skills/agent-debug.md` | Skill definition with diagnostic procedures and troubleshooting table |
