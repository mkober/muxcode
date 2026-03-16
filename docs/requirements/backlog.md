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
| Demo mode | Scripted demo scenarios with bus messages, window switching, and GIF capture | |
| Auto session compaction | Watcher-triggered compaction alerts when agent context approaches limits | |
| Agent spawn | Create temporary agent sessions for one-off tasks, collect result, tear down | |
| Session re-init purge | Stale data cleanup on session restart — preserves memory, purges ephemeral files | |
| Runner execution history | Left-pane poller for run window showing command history, exit codes, and output | |
| Preview fold fix | Persistent `foldlevel=99` in nvim diff previews replacing one-shot `zR` | |
| User-initiated git ops | Chain stops at review — commits, pushes, and PRs require explicit user action | |
| Log tailing delegation | Route `aws logs`, `tail -f`, `kubectl logs`, etc. to the watch agent via edit guard | |
| Review agent permissions | Process substitution (`diff <(...)`), `python3`, `jq` added to review tool profile | |
| Git manager HEREDOC | Commit agent uses HEREDOC for commit messages instead of temp files | |
| Analyze findings log | Left-pane poller for analyze window — filters `log.jsonl` for analyst responses, shows findings count, recent entries, and full latest payload. Watcher moved to background process at session init. | |
| BM25 memory search | Okapi BM25 ranking with IDF weighting, length normalization, stemming, stop words, and quoted phrase matching — replaces keyword counting as default search mode | |
| Daily memory rotation | Lazy daily rotation on first write — archives previous day's file to `{role}/YYYY-MM-DD.md`, configurable 30-day retention, 7-day context window, search covers archives | |
| Loop-detected self-loop fix | System action exclusion (`isSystemAction()`) filters infrastructure actions from message loop detection; dedup cooldown increased from 300s to 600s to exceed detection window | |
| Webhook endpoint | HTTP listener converting POST requests to bus messages — `POST /send` with role validation, bearer token auth, PID management; `GET /health`; detached background process via CLI | |
| Context directory | Per-agent `context.d/` drop-in context files — `shared/` for all roles, `<role>/` for role-specific; project shadows user by filename; injected into prompt between skills and session resume | |
| Project-aware context | Auto-detect project type (17 types via indicator files/globs) and inject convention snippets into all agent prompts — metadata extraction from go.mod, package.json, cdk.json, composer.json; manual context.d files shadow auto-detected; `--no-auto` opt-out on list/prompt | |
| Event subscription | JSONL-persisted subscription table for event fan-out — agents subscribe to event+outcome patterns (`build/success`, `*/failure`, `*/*`), chain fires subscriptions after primary action, message template expansion with `${event}`, `${outcome}`, `${exit_code}`, `${command}` | |
| Token usage reduction | `SendNoCC()` skips auto-CC on chain/subscription messages, `ChainShouldNotifyAnalyst()` with `NotifyAnalystOn` field for outcome-conditional analyst notifications (build/test: failure+unknown only), watcher efficiency: loop interval 30→60s, lazy cron/proc/spawn loading, running-state cache | [token-reduction.md](./token-reduction.md) |
| Vim diff preview fix | `sil!` prefix on every command in vim pipe chains (only suppresses next command, not full chain) — fixes E35 errors and "Press ENTER" prompts. Separate `tmux send-keys` with 150ms delay for jump-to-line after diff setup so scrollbind is active | |
| Local LLM agent | Per-role Ollama integration — Go agentic loop (`muxcode-agent-bus agent run`) with OpenAI-compatible API, tool execution (bash/read/glob/grep/write/edit), allowedTools enforcement, per-role config via `MUXCODE_{ROLE}_CLI=local`, fallback to Claude Code if Ollama unreachable | [local-llm-agent.md](./local-llm-agent.md) |
| Ollama health monitoring | Watcher-integrated inference probes (30s interval) detect stuck Ollama instances — `ollama-down` alert at 60s, auto-restart at 90s (cap 3), agent relaunch, recovery detection; agent-side failure sentinels track consecutive `ChatComplete` errors | |
| Jira description read+update | General-purpose GET+PUT skill for git-manager — read current description with context fields, update with ADF content, explicit-key + branch-name extraction | [jira-update-description.md](./jira-update-description.md) |
| Agent health monitoring | Watcher detects dead agents (3-strike restart) + watcher self-monitoring via keepalive file + monitor script — `agent-health` CLI for stop/start/check | [agent-health-monitoring.md](./agent-health-monitoring.md) |
| Cross-session memory | Global memory at `~/.config/muxcode/memory/` persisting across projects — `write-global`, `--no-global`, `--scope` flags, global context in prompts, BM25 search across both layers | [cross-session-memory.md](./cross-session-memory.md) |
| Lifecycle logging | Persistent JSONL logs at `~/.config/muxcode/logs/{session}.log` recording launcher sequence, watcher events, agent launches, auto-accept, and cleanup — survives session cleanup, filterable by source/level/event/time via `lifecycle show`, rotation at 1000 entries, `lifecycle purge` for cleanup | |
| Build/test error extraction | Bash hook extracts error-relevant lines from failed build/test output into `errors` field in history JSONL — left-pane log views prefer errors over raw output for failures, filter "Exit code:" noise, color errors red/yellow | |
| Harness circuit breaker | 3-layer stuck protection: within-turn filter, within-batch all-blocked early exit (2 turns), cross-batch cooldown (3 failures → 30s pause), 5-minute batch timeout — prevents runaway Ollama calls | |
| PII scrubbing | Automatic PII/secret redaction for api/runner/watch roles — harness: `scrub.go` in executor; Claude Code: `muxcode-pii-scrub.sh` pipe-through script + agent definition instructions. Patterns: emails, SSN, CC (prefix-anchored), phone (separator-required), AWS keys, JWTs, generic secrets | |
| Agent debug skill | Edit-agent skill for diagnosing other agents via tmux capture-pane — pane content capture, idle detection, inbox/health checks, multi-agent sweep, troubleshooting table | |

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

