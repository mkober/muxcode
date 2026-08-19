# Modal: Webhook Monitor

Live modal for inspecting incoming webhook requests. Shows request stream in real time with detail drill-down, replay, and filtering.

**Depends on:** [Modal Window Manager](../completed/MUX-069-modal-window-manager.md)

## Use case

The webhook endpoint (`muxcode webhook start`) accepts HTTP requests and routes them to agents via the bus. Currently there's no visibility into what requests are arriving, their payloads, or whether they were processed successfully. Debugging webhook integrations requires checking agent inboxes manually. A modal provides a live request inspector.

## Layout

Vertical split: request list on top (70%), detail view on bottom (30%).

```
+--------------------------------------------------+
|  ' Webhook Monitor '                             |
|                                                   |
|  +----------------------------------------------+ |
|  | 20:15:01  POST  /send  → edit    200  12ms   | |
|  | 20:15:03  POST  /send  → build   200   8ms   | |
|  |>20:15:05  POST  /send  → test    400  15ms   | |
|  | 20:15:10  GET   /health          200   2ms   | |
|  +----------------------------------------------+ |
|  | POST /send → test                             | |
|  | Status: 400 Bad Request                       | |
|  | Body: {"to":"test","action":"run"}            | |
|  | Error: missing 'message' field               | |
|  +----------------------------------------------+ |
+--------------------------------------------------+
```

## Modal config

```go
RegisterModal(ModalConfig{
  Name:    "webhook",
  Title:   " Webhook Monitor ",
  Width:   "62%",
  Height:  "62%",
  Command: "muxcode modal webhook-monitor",
  Split: &ModalSplit{
    Direction: "v",
    Size:      "30%",
    Command:   "", // detail pane managed by the monitor command
    Primary:   "top",
  },
  Sizes: map[string][2]string{
    "compact": {"50%", "40%"},
    "full":    {"95%", "95%"},
  },
})
```

## Features

### Live request stream

Displays incoming webhook requests in real time. New requests appear at the bottom with auto-scroll. Each entry shows: timestamp, HTTP method, path, target role, status code, response time.

### Detail view

Selecting a request (with `Enter` or cursor movement) shows full details in the bottom pane:
- Request headers
- Request body (JSON pretty-printed)
- Response status and body
- Target role and action
- Processing result (delivered / error)

### Filtering

- `/` to search by path, target role, or body content
- `e` to show errors only (4xx/5xx responses)
- `r` to filter by target role

### Replay

- `R` on a selected request replays it through the webhook endpoint
- Useful for retrying failed deliveries during development

### Status indicator

Shows webhook server status at bottom: `listening on :8080` or `webhook not running`. If not running, offers to start it.

### Request history

Reads from webhook access log at `BusDir/webhook-access.log`. The monitor writes entries to this log as requests arrive, so history persists across modal opens.

### Display

- Method in `ColorCyan` (GET) / `ColorGreen` (POST) / `ColorRed` (DELETE)
- Status 2xx in `ColorGreen`, 4xx in `ColorYellow`, 5xx in `ColorRed`
- Timestamps in `ColorDim`
- Selected row highlighted with `ColorPurple`
- JSON body syntax-highlighted

## Menu entry

```
"Webhook Monitor"         W "run-shell 'muxcode modal open webhook'"
```

## Keybinding

```
bind W run-shell 'muxcode modal open webhook'
```

## Success criteria

- [ ] Live request stream with auto-scroll
- [ ] Detail pane shows full request/response for selected entry
- [ ] Error filtering with `e`, role filtering with `r`
- [ ] Request replay with `R`
- [ ] Webhook server status indicator
- [ ] Persistent access log for history across modal opens
- [ ] Dracula-themed color coding
- [ ] Menu entry and keybinding registered

## Status

Backlog
