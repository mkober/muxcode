# Requirements Backlog

## Implemented

| Feature | Description | Requirements doc |
|---------|-------------|-----------------|
| Memory search | Keyword search and section inventory across agent memory files | [memory-search.md](./memory-search.md) |
| Skills plugin system | File-based plugin mechanism for reusable instruction sets with role-based injection | [skills-plugin.md](./skills-plugin.md) |
| Session compaction | Manual session summary save/restore for context preservation across restarts | |
| Cron scheduling | Interval-based scheduled tasks with watcher integration and execution history | |
| Tool profiles | Config-driven per-role tool permissions with shared groups and composition | [tool-profiles-and-chains.md](./tool-profiles-and-chains.md) |
| Event-driven chains | Configurable build-test-review and deploy-verify automation chains | [tool-profiles-and-chains.md](./tool-profiles-and-chains.md) |
| Session inspection | Agent status overview and message history querying | |
| Loop detection | Automatic detection of repetitive agent patterns with escalation to edit agent | |
| Dynamic prompts | Go-based system prompt composition with role-specific sections | |
| Process management | Background process lifecycle tracking with auto-notification on completion | |
| Deploy verification | Post-deploy health checks triggered automatically after successful deployments | [deploy-verify.md](./deploy-verify.md) |

## Planned

| Feature | Description | Priority |
|---------|-------------|----------|
| Semantic memory search | Vector embeddings for memory search (currently keyword-only) | Low |
| Daily memory rotation | Rolling window of daily memory files with automatic archival | Low |
| Auto session compaction | Watcher-triggered compaction when agent context approaches limits | Medium |
| Webhook endpoint | HTTP listener converting POST requests to bus messages | Low |
| Agent spawn | Create temporary agent sessions for one-off tasks, collect result, tear down | Medium |
| Context directory | Per-agent `context.d/` directory for drop-in context files | Low |
| Project-aware context | Auto-detect project type and inject relevant conventions | Low |
| Event subscription | Subscribe agents to event patterns beyond build/test/deploy | Low |

## Sources

- [OpenClaw](https://openclaw.ai/) — architecture inspiration for many features
- [OpenClaw Architecture Overview](https://ppaolo.substack.com/p/openclaw-system-architecture-overview)
