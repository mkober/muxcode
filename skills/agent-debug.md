---
name: agent-debug
description: Debug agent issues by capturing tmux pane content and checking agent status
roles: [edit]
tags: [debug, tmux, agents]
---

## Agent debugging via tmux capture-pane

Use these techniques to inspect what other agents are doing, verify message delivery, and diagnose stuck agents.

### Prerequisites

- `BUS_SESSION` env var (always exported in muxcode sessions)
- Agent pane targeting: agent runs in pane 1 (right pane) of its window

### Pane target format

```
{session}:{window}.1
```

Where `window` matches the role name for most agents. Hosted roles map to their host window:
- `docs`, `research` → `edit`
- `pr-read` → `commit`

### Capture agent pane content

Capture the last N lines from an agent's tmux pane to see what it's currently doing:

```bash
# Capture last 30 lines from an agent pane (strip ANSI codes)
tmux capture-pane -t "${BUS_SESSION}:{role}.1" -p -S -30 | sed 's/\x1b\[[0-9;]*[A-Za-z]//g'
```

Adjust `-S -30` for more/less context (e.g. `-S -50` for 50 lines, `-S -100` for 100 lines).

### Check if agent is idle or active

An idle agent shows the `❯` prompt character. Check:

```bash
tmux capture-pane -t "${BUS_SESSION}:{role}.1" -p -S -8 | grep -q '❯' && echo "idle" || echo "active"
```

### Check agent left pane (log view)

Windows with split panes have a log view in pane 0 (left):

```bash
# Capture the build log view
tmux capture-pane -t "${BUS_SESSION}:build.0" -p -S -30 | sed 's/\x1b\[[0-9;]*[A-Za-z]//g'
```

Split-left windows: edit, build, test, review, deploy, analyze, commit, watch.

### Check inbox and message status

```bash
# Check if agent has pending messages
muxcode inbox --role {role} --peek

# Check all agent statuses (health, inbox count, last message)
muxcode inspect

# Check specific agent status
muxcode inspect --role {role}
```

### Debugging workflow

When an agent appears stuck or unresponsive:

1. **Capture pane** — see what the agent is currently showing:
   ```bash
   tmux capture-pane -t "${BUS_SESSION}:{role}.1" -p -S -50 | sed 's/\x1b\[[0-9;]*[A-Za-z]//g'
   ```

2. **Check idle state** — is the agent at the prompt or mid-execution?
   ```bash
   tmux capture-pane -t "${BUS_SESSION}:{role}.1" -p -S -8 | grep -q '❯' && echo "idle" || echo "active"
   ```

3. **Check inbox** — does it have unprocessed messages?
   ```bash
   muxcode inbox --role {role} --peek
   ```

4. **Check health** — is the agent process alive?
   ```bash
   muxcode inspect --role {role}
   ```

5. **Review recent messages** — did the message get delivered?
   ```bash
   muxcode log --role {role} --last 5
   ```

### Common issues

| Symptom | Diagnosis | Fix |
|---------|-----------|-----|
| Agent idle with pending inbox | Notification missed | Re-send wake-up: `muxcode send {role} notify "You have new messages"` |
| Agent active for too long | Stuck in tool execution | Check pane for errors, may need restart: `muxcode agent-health --start {role}` |
| Agent shows "permission" prompt | Waiting for user approval | Approve/reject in the agent's tmux window |
| Agent shows bash `$` prompt | Claude Code crashed | Restart: `muxcode agent-health --start {role}` |
| Message sent but no response | Agent may not have received | Check log: `muxcode log --role {role} --last 5` then re-send |

### Capture multiple agents at once

To get a quick overview of all agents:

```bash
for role in build test review commit deploy; do
  echo "=== ${role} ==="
  idle=$(tmux capture-pane -t "${BUS_SESSION}:${role}.1" -p -S -8 2>/dev/null | grep -q '❯' && echo "idle" || echo "active")
  inbox=$(muxcode inbox --role "${role}" --peek 2>/dev/null | grep -c "Message from" || echo 0)
  echo "  status: ${idle}  inbox: ${inbox}"
  tmux capture-pane -t "${BUS_SESSION}:${role}.1" -p -S -5 2>/dev/null | sed 's/\x1b\[[0-9;]*[A-Za-z]//g' | tail -3 | sed 's/^/  /'
  echo ""
done
```
