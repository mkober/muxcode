# Modal: Log Viewer

On-demand modal for tailing and browsing logs. Opens as a modal window via the modal manager, replacing the need to switch to the watch agent window for quick log inspection.

**Depends on:** [Modal Window Manager](../completed/MUX-069-modal-window-manager.md)

## Use case

Users frequently need to check CloudWatch logs, container logs, or local log files during development. The watch agent handles long-running log tailing, but quick "check the last 50 lines" lookups don't need a persistent agent window. A modal provides fast access without leaving the current context.

## Layout

Single pane — no split needed. The modal runs a scrollable log viewer.

```
+--------------------------------------------------+
|  ' Log Viewer '                                  |
|                                                   |
|  2026-03-25 14:32:01 INFO  Lambda started         |
|  2026-03-25 14:32:01 INFO  Processing event...    |
|  2026-03-25 14:32:02 WARN  Retry attempt 2        |
|  2026-03-25 14:32:03 ERROR Connection timeout     |
|  2026-03-25 14:32:03 INFO  Fallback triggered     |
|  ...                                              |
+--------------------------------------------------+
```

## Modal config

```go
RegisterModal(ModalConfig{
  Name:    "logs",
  Title:   " Log Viewer ",
  Width:   "80%",
  Height:  "70%",
  Command: "muxcode modal log-viewer",
  Role:    "watch",
  Sizes: map[string][2]string{
    "compact": {"60%", "50%"},
    "full":    {"95%", "95%"},
  },
})
```

## Features

### Log source selection

On open, prompts for log source if not specified:

```
muxcode modal open logs                              # prompts for source
muxcode modal open logs --arg "/aws/lambda/my-func"  # direct CloudWatch log group
muxcode modal open logs --arg "./app.log"             # local file
```

### Tail vs snapshot

| Mode | Behavior |
|------|----------|
| `--tail` (default) | Live tail with auto-scroll, similar to `tail -f` |
| `--snapshot` | Fetch last N lines and display statically with scroll support |

### Filtering

Supports inline grep filtering:
- `--filter "ERROR"` — show only lines matching pattern
- `--filter "ERROR|WARN"` — multiple patterns via regex

### Color coding

Log levels are color-coded using Dracula theme colors:
- ERROR/FATAL → `ColorRed`
- WARN → `ColorYellow`
- INFO → `ColorGreen`
- DEBUG → `ColorDim`

### Integration with watch agent

If the watch agent is already tailing a source, the modal can attach to its output stream rather than starting a new tail. The modal reads from the watch agent's console history when available.

## Menu entry

```
"Log Viewer"              L "run-shell 'muxcode modal open logs'"
```

## Keybinding

```
bind L run-shell 'muxcode modal open logs'
```

## Success criteria

- [ ] Modal opens with log source selection
- [ ] Live tail mode with auto-scroll
- [ ] Snapshot mode with scrollback
- [ ] Log level color coding (Dracula theme)
- [ ] `--filter` for inline grep
- [ ] Menu entry and keybinding registered
- [ ] Reuses watch agent history when available

## Status

Backlog
