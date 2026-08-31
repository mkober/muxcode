# Ollama Health Monitoring

Watcher-integrated inference probes detect and recover stuck Ollama instances.

## Requirements

- Inference health probes run at 30-second intervals via the watcher
- `ollama-down` alert sent to affected agents after 60 seconds of failure
- Auto-restart triggered at 90 seconds with a cap of 3 restart attempts
- Agent relaunch after successful Ollama restart
- Recovery detection clears alert state and sends `ollama-recovered` notification
- Agent-side failure sentinels track consecutive `ChatComplete` errors

## Key files

| File | Purpose |
|------|---------|
| `bus/health.go` | `CheckOllamaInference()`, `RestartOllama()`, `RestartLocalAgent()`, `LocalLLMRoles()` |

## Status

Complete
