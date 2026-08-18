# Modal: Cron Manager

Interactive modal for managing scheduled cron jobs. Replaces the static `muxcode cron list` popup with a navigable interface that supports creating, editing, enabling/disabling, and viewing execution history.

**Depends on:** [Modal Window Manager](modal-window-manager.md)

## Use case

Cron jobs are currently managed via CLI commands (`muxcode cron add/remove/enable/disable/list/history`). The bottom menu shows a static list in a popup. Managing multiple jobs requires switching between CLI commands. A modal provides a single interactive interface for all cron operations.

## Layout

Vertical split: job list on top (70%), detail/history view on bottom (30%).

```
+--------------------------------------------------+
|  ' Cron Manager '                                |
|                                                   |
|  +----------------------------------------------+ |
|  | ID       Schedule      Target  Action  Status | |
|  | cron-01  */5 * * * *   build   build   ● on   | |
|  |>cron-02  0 */2 * * *   test    test    ○ off  | |
|  | cron-03  0 9 * * 1-5   deploy  diff    ● on   | |
|  +----------------------------------------------+ |
|  | cron-02: Run tests every 2 hours              | |
|  | Schedule: 0 */2 * * *  (every 2 hours)        | |
|  | Last run: 2026-03-25 14:00 — exit 0 (3.2s)   | |
|  | Next due: 2026-03-25 16:00                    | |
|  | Runs: 12 total, 11 pass, 1 fail               | |
|  +----------------------------------------------+ |
+--------------------------------------------------+
```

## Modal config

```go
RegisterModal(ModalConfig{
  Name:    "cron",
  Title:   " Cron Manager ",
  Width:   "62%",
  Height:  "62%",
  Command: "muxcode modal cron-manager",
  Split: &ModalSplit{
    Direction: "v",
    Size:      "30%",
    Command:   "", // detail pane managed by the cron-manager command
    Primary:   "top",
  },
  Sizes: map[string][2]string{
    "compact": {"50%", "40%"},
    "full":    {"95%", "95%"},
  },
})
```

## Features

### Job list

Displays all registered cron entries from `ReadCronEntries()`. Columns: ID, schedule (cron expression), target role, action, enabled status. Auto-refreshes on a 5-second interval to reflect changes from other sources.

### Detail view

Selecting a job shows in the bottom pane:
- Full description and message template
- Human-readable schedule interpretation (e.g. "every 5 minutes", "weekdays at 9 AM")
- Last execution result (timestamp, exit code, duration)
- Next due time
- Execution summary (total runs, pass/fail counts)

### Execution history

`h` on a selected job shows its execution history from `ReadCronHistory()`:
- Timestamp, exit code, duration for each run
- Scrollable with `j/k`
- `Escape` to return to job list

### Job operations

| Key | Action |
|-----|--------|
| `a` | Add new job — prompts for schedule, target, action, message |
| `d` | Delete selected job (with confirmation) |
| `e` | Edit selected job — opens editable fields |
| `Space` | Toggle enable/disable on selected job |
| `Enter` | Run selected job immediately via `ExecuteCron()` |
| `r` | Refresh job list |

### Schedule validation

When adding or editing, validates cron expressions via `ParseSchedule()` and shows human-readable interpretation before confirming.

### Display

- Enabled status: `●` in `ColorGreen` (on), `○` in `ColorDim` (off)
- Job ID in `ColorCyan`
- Schedule in `ColorPurple`
- Last run exit 0 in `ColorGreen`, non-zero in `ColorRed`
- Next due time in `ColorYellow` if within 5 minutes

## Menu entry

Replace existing "Cron Jobs" entry:

```
"Cron Manager"            c "run-shell 'muxcode modal open cron'"
```

## Keybinding

```
bind c run-shell 'muxcode modal open cron'
```

## Success criteria

- [ ] Modal displays all cron jobs with status, schedule, target
- [ ] Detail pane shows execution summary and next due time
- [ ] Execution history view with `h`
- [ ] Add (`a`), delete (`d`), edit (`e`), toggle (`Space`), run now (`Enter`)
- [ ] Schedule validation with human-readable interpretation
- [ ] Auto-refresh on 5-second interval
- [ ] Replaces existing static "Cron Jobs" menu entry
- [ ] Dracula-themed color coding
- [ ] Menu entry and keybinding registered

## Status

Backlog
