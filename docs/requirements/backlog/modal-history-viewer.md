# Modal: History Viewer

Full-screen modal for browsing agent command history with filtering, search, and detail expansion. Replaces the `muxcode history <role>` CLI output with an interactive navigable view.

**Depends on:** [Modal Window Manager](modal-window-manager.md)

## Use case

Agent history is currently viewed via `muxcode history <role>`, which dumps JSONL entries to stdout. For long sessions this produces hundreds of lines that are hard to scan. The existing bottom menu opens this in a static popup with "press any key" — no search, no filtering, no detail drill-down. A modal provides interactive navigation.

## Layout

Single pane with inline detail expansion.

```
+--------------------------------------------------+
|  ' History: edit '                               |
|                                                   |
|  16:01:32  bash     0  muxcode inbox              |
|  16:01:34  edit     0  tools/muxcode/bus/tmux.go  |
|  16:01:35  bash     0  muxcode send build ...     |
|> 16:01:40  bash     1  go test ./...              |
|  │ exit_code: 1                                   |
|  │ stderr: FAIL bus/launcher_test.go:42           |
|  │ duration: 3.2s                                 |
|  16:01:45  edit     0  tools/muxcode/bus/launch.. |
|  ...                                              |
|                                                   |
|  [q]uit [/]search [f]ilter [r]ole  entries: 142   |
+--------------------------------------------------+
```

## Modal config

```go
RegisterModal(ModalConfig{
  Name:    "history",
  Title:   " History Viewer ",
  Width:   "62%",
  Height:  "62%",
  Command: "muxcode modal history-viewer",
  Sizes: map[string][2]string{
    "compact": {"50%", "40%"},
    "full":    {"95%", "95%"},
  },
})
```

## Features

### Role selection

- Default: current window's role (auto-detected from `#W`)
- Override: `muxcode modal open history --arg edit`
- Cycle: `r` key cycles through available roles

### Entry navigation

- `j/k` or arrow keys to move cursor
- `Enter` to expand/collapse entry details (exit code, stderr, duration, full command)
- `g/G` to jump to first/last entry
- `Home/End` equivalent bindings

### Filtering

- `/` to search entry content (command text, file paths)
- `f` to filter by tool type (bash, edit, read, glob, grep)
- `e` to filter errors only (non-zero exit codes)
- `Escape` to clear filters

### Time range

- Default: current session
- `t` to toggle time range: last 1h / last 4h / all session

### Display

- Timestamps in `ColorDim`
- Tool type in `ColorCyan`
- Exit code 0 in `ColorGreen`, non-zero in `ColorRed`
- Selected row highlighted with `ColorPurple` background
- Expanded details indented with `│` prefix in `ColorDim`

## Menu entry

Replace existing "Agent History" entry:

```
"Agent History"           h "run-shell 'muxcode modal open history'"
```

## Keybinding

```
bind h run-shell 'muxcode modal open history'
```

## Success criteria

- [ ] Modal displays history entries with timestamp, tool, exit code, command summary
- [ ] Role auto-detection from current window, cycling with `r`
- [ ] Entry expansion with `Enter` showing full details
- [ ] Search with `/` and filter by tool type with `f`
- [ ] Error-only filter with `e`
- [ ] Time range toggle with `t`
- [ ] Dracula-themed color coding
- [ ] Replaces existing static "Agent History" menu entry

## Status

Backlog
