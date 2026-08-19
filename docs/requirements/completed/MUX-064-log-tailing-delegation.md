# Log Tailing Delegation

Route `aws logs`, `tail -f`, `kubectl logs`, etc. to the watch agent via edit guard.

## Requirements

- Edit guard script intercepts log tailing commands before execution
- Commands matching `aws logs`, `tail -f`, `kubectl logs`, `docker logs`, `stern` are blocked
- Blocked commands produce a message instructing delegation to the watch agent
- Watch agent definition includes permissions for all log tailing tools
- Edit agent system prompt documents the delegation pattern with bus command examples
- Guard runs as a pre-tool-use hook in Claude Code

## Key files

| File | Purpose |
|------|---------|
| `scripts/muxcode-edit-guard.sh` | Pre-tool-use hook blocking log commands in edit |
| `agents/code-editor.md` | Edit agent instructions with watch delegation pattern |
| `agents/log-watcher.md` | Watch agent definition with log tool permissions |
