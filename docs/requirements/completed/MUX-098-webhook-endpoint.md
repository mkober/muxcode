# Webhook Endpoint

HTTP listener converting POST requests into bus messages for external integrations.

## Requirements

- `POST /send` accepts JSON body with `to`, `action`, `message` fields
- Role validation rejects messages to unknown agents
- Bearer token authentication via configurable secret
- `GET /health` returns server status
- PID management for lifecycle control (write, read, check running, stop)
- Runs as a detached background process started via CLI subcommand

## Key files

| File | Purpose |
|------|---------|
| `bus/webhook.go` | `ServeWebhook()`, `WriteWebhookPid()`, `ReadWebhookPid()`, `IsWebhookRunning()`, `StopWebhookProcess()` |

## Status

Complete
