# Requirements Backlog

## Completed

44 delivered feature specs live in [`completed/`](./completed/). Each file contains requirements, key files, and implementation notes.

## Planned

### Reliability & Observability

| Feature | Description | Priority |
|---------|-------------|----------|
| Structured agent metrics | Track per-agent metrics (messages sent/received, tool calls, errors, avg response time) in `metrics.jsonl` — dashboard TUI shows metrics panel | Medium |
| Message delivery receipts | Agents ACK message consumption — sender knows message was read vs. sitting unprocessed, enables "read but no response" alerts | Low |
| Bus audit trail | Append-only audit log separate from `log.jsonl` capturing all bus operations (send, consume, lock, unlock, cron fire, proc start/stop) with caller identity — post-session debugging. Partially addressed by lifecycle logging (`~/.config/muxcode/logs/`) which covers process lifecycle and watcher events | Low |

### Performance & Cost

| Feature | Description | Priority |
|---------|-------------|----------|
| On-demand agent spawning | Convert runner, watch, and analyst from always-on to deferred launch on first message — tmux windows still created for left-pane pollers, agent process starts only when a bus message targets the role | Medium |
| Smart context pruning | Before hitting compaction threshold, auto-prune low-relevance memory entries (BM25-scored against recent activity) — more surgical than full session compact | Medium |
| Tiered model routing | Route simple/structured tasks (git status, build) to cheaper/faster models (Haiku) and complex tasks (review, analysis) to Opus — config-driven per-role model selection | Medium |
| Batch message coalescing | When multiple messages arrive in an agent's inbox between polls, coalesce into a single prompt rather than processing sequentially — reduces context overhead and API calls | Low |

### Workflow & Automation

| Feature | Description | Priority |
|---------|-------------|----------|
| Conditional chains | Extend event chains with conditions beyond exit codes — file pattern matching (only run deploy chain if infra files changed), time-of-day gates, branch name filters | High |
| Pipeline definitions | User-defined multi-step pipelines as YAML/JSON files (e.g. `lint → build → test → security-scan → review`) — more flexible than hardcoded build→test→review chain | Medium |
| Retry with backoff | Configurable retry policy for failed chain steps — exponential backoff, max attempts, different behavior per step | Medium |
| Workspace checkpoints | Snapshot working directory state before risky operations (deploy, large refactor) — allows rollback via `muxcode-agent-bus checkpoint restore`, leverages `git stash` or worktrees internally | Medium |
| Pre-commit hooks | Beyond the current safeguard (pending inbox check), run configurable checks before commit — lint, type-check, test subset — blocks commit until all pass | Low |

### Intelligence & Context

| Feature | Description | Priority |
|---------|-------------|----------|
| Memory tagging & expiry | Tag memory entries with categories (bug-fix, convention, workaround) and optional TTL — auto-expire stale workarounds, improves signal-to-noise in memory search | Medium |
| Agent handoff protocol | Structured handoff when one agent needs another to continue its work — includes context bundle (relevant files, conversation excerpt, constraints), not just "send a message" | Medium |
| Semantic memory search | Augment BM25 with embeddings (local via Ollama embedding models) for semantic similarity — falls back to BM25 when Ollama unavailable | Low |

### UX & Dashboard

| Feature | Description | Priority |
|---------|-------------|----------|
| Dashboard activity timeline | Visual timeline in TUI showing message flow between agents over time — like a sequence diagram but live — currently dashboard shows status tables but no temporal view | High |
| Agent log viewer in TUI | Navigate and search `log.jsonl` from the dashboard — filter by role, action, time range — currently requires `muxcode-agent-bus history` CLI | Medium |
| Notification sound/bell | Optional terminal bell or macOS notification on important events (build failure, review complete, agent-down) — configurable per-event | Low |
| Session recording & replay | Record all bus messages during a session for later replay/analysis — useful for demos, debugging, understanding multi-agent interactions — inverse of demo mode (record real sessions) | Low |

### Integrations

| Feature | Description | Priority |
|---------|-------------|----------|
| GitHub Actions webhook bridge | Pre-built GitHub Actions workflow that POSTs to the webhook endpoint on PR events (opened, review submitted, CI status) — turns external events into agent actions | High |
| Slack/Discord notifications | Forward important agent events (build failure, deploy complete, review findings) to a Slack/Discord channel via webhook URL — one-way, config-driven | Medium |
| IDE status bar | Lightweight status indicator for VS Code / Neovim showing agent states and inbox counts — read-only, polls bus directory — for Neovim: a Lua plugin reading lock files | Medium |
| Linear/Jira bidirectional sync | Beyond current Jira description updates — auto-update issue status based on agent activity (e.g. move to "In Review" when review agent starts) | Low |

### Security & Isolation

| Feature | Description | Priority |
|---------|-------------|----------|
| Secret scanning in commits | Pre-commit agent check scans staged diffs for patterns matching API keys, tokens, passwords — blocks commit and alerts edit. PII scrubbing (`scrub.go`, `muxcode-pii-scrub.sh`) partially addresses this for tool output but not for commits | High |
| Agent sandbox levels | Graduated trust levels — `read-only`, `project-scoped`, `unrestricted` — new agents start at read-only and escalate based on config, more granular than current tool profiles | Medium |
| Webhook rate limiting | Per-IP and global rate limits on the webhook endpoint — currently only has auth token + localhost binding, important if exposing via tunnel | Low |

### Developer Experience

| Feature | Description | Priority |
|---------|-------------|----------|
| `muxcode init` wizard | Interactive project setup — detects project type, generates `.muxcode/config`, copies relevant agent overrides, suggests window layout | High |
| Agent definition linting | Validate agent markdown files — check frontmatter schema, verify referenced tools exist in profiles, warn about common mistakes — `muxcode-agent-bus agent lint` | Medium |
| Skill marketplace | Community-shared skills via a git-based registry — `muxcode-agent-bus skill install <url>` — each skill is a markdown file with frontmatter, already the right format | Low |
| Multi-repo sessions | Support sessions spanning multiple related repos (monorepo-like) — each repo gets its own bus directory but agents can cross-reference | Low |

## Sources

- [OpenClaw](https://openclaw.ai/) — architecture inspiration for many features
- [OpenClaw Architecture Overview](https://ppaolo.substack.com/p/openclaw-system-architecture-overview)

