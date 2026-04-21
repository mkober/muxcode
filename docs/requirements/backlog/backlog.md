# Requirements Backlog

## Completed

51 delivered feature specs live in [`completed/`](./completed/). Each file contains requirements, key files, and implementation notes.

## In progress

| Feature | Spec | Status |
|---------|------|--------|
| Agent mode | [`docs/requirements/drafts/agent-mode.md`](../drafts/agent-mode.md) | In Progress — Phases 1-7 complete, all acceptance criteria checked |

## Top priorities

| Priority | Category | Feature |
|----------|----------|---------|
| Medium | Reliability | Structured agent metrics |
| Medium | Reliability | File integrity validation |
| High | Cost | Agent max steps / iteration limits |
| Medium | Cost | On-demand agent spawning |
| High | Workflow | Conditional chains |
| Medium | Workflow | Pipeline definitions |
| High | Intelligence | LSP integration for agent tools |
| Medium | Intelligence | Memory tagging & expiry |
| High | UX | Dashboard activity timeline |
| Medium | UX | TUI theme system |
| High | Integrations | GitHub Actions webhook bridge |
| Medium | Integrations | Slack/Discord notifications |
| High | Security | Secret scanning in commits |
| Medium | Security | Agent sandbox levels |
| High | Dev Experience | `muxcode init` wizard |
| Medium | Dev Experience | Custom slash commands |

## Planned

### Reliability & Observability

| Priority | Feature |
|----------|---------|
| Medium | Structured agent metrics |
| Medium | File integrity validation |
| Medium | Tool-call doom loop detection |
| Low | Message delivery receipts |
| Low | Bus audit trail |

- **Structured agent metrics** — Track per-agent metrics (messages sent/received, tool calls, errors, avg response time) in `metrics.jsonl` — dashboard TUI shows metrics panel
- **File integrity validation** — Timestamp-based change detection on file operations — detect external modifications between read and edit/write, warn agent of stale content before applying changes. Inspired by OpenCode's file integrity checks
- **Tool-call doom loop detection** — Detect 3+ identical consecutive tool calls within a single agent turn (same tool, same args) — prompt user or abort. Complements existing message-level loop detection in `bus/guard.go`. Inspired by OpenCode's `doom_loop` permission
- **Message delivery receipts** — ~~Agents ACK message consumption~~ Partially implemented: `bus/delivery.go` tracks sent→delivered→responded lifecycle per message via status files. Remaining: agent-facing `muxcode tasks` command for in-flight task listing, watcher integration for `CleanExpiredDeliveries()`, "read but no response" alerts
- **Bus audit trail** — Append-only audit log separate from `log.jsonl` capturing all bus operations (send, consume, lock, unlock, cron fire, proc start/stop) with caller identity — post-session debugging. Partially addressed by lifecycle logging (`~/.config/muxcode/logs/`) which covers process lifecycle and watcher events

### Performance & Cost

| Priority | Feature |
|----------|---------|
| High | Agent max steps / iteration limits |
| Medium | On-demand agent spawning |
| Medium | Smart context pruning |
| Medium | Tiered model routing |
| Low | Batch message coalescing |

- **Agent max steps / iteration limits** — Per-role configurable maximum tool-call iterations per message — `MUXCODE_{ROLE}_MAX_STEPS` or profile field. Prevents runaway API costs from stuck agents. Harness circuit breaker handles local LLM; this extends to Claude Code agents via conversation turn counting. Inspired by OpenCode's `maxSteps` per agent
- **On-demand agent spawning** — Convert runner, watch, and analyst from always-on to deferred launch on first message — tmux windows still created for left-pane pollers, agent process starts only when a bus message targets the role
- **Smart context pruning** — Before hitting compaction threshold, auto-prune low-relevance memory entries (BM25-scored against recent activity) — more surgical than full session compact
- **Tiered model routing** — Route simple/structured tasks (git status, build) to cheaper/faster models (Haiku) and complex tasks (review, analysis) to Opus — config-driven per-role model selection
- **Batch message coalescing** — When multiple messages arrive in an agent's inbox between polls, coalesce into a single prompt rather than processing sequentially — reduces context overhead and API calls

### Workflow & Automation

| Priority | Feature |
|----------|---------|
| High | Conditional chains |
| Medium | Pipeline definitions |
| Medium | Retry with backoff |
| Medium | Workspace checkpoints |
| Medium | Undo/redo for agent file changes |
| Low | Pre-commit hooks |

- **Conditional chains** — Extend event chains with conditions beyond exit codes — file pattern matching (only run deploy chain if infra files changed), time-of-day gates, branch name filters
- **Pipeline definitions** — User-defined multi-step pipelines as YAML/JSON files (e.g. `lint → build → test → security-scan → review`) — more flexible than hardcoded build→test→review chain
- **Retry with backoff** — Configurable retry policy for failed chain steps — exponential backoff, max attempts, different behavior per step
- **Workspace checkpoints** — Snapshot working directory state before risky operations (deploy, large refactor) — allows rollback via `muxcode checkpoint restore`, leverages `git stash` or worktrees internally
- **Undo/redo for agent file changes** — Track file snapshots before each agent Write/Edit operation — `muxcode undo [steps]` restores previous state via git stash or shadow copies. Enables safe experimentation without manual git gymnastics. Inspired by OpenCode's `/undo` and `/redo` commands
- **Pre-commit hooks** — Beyond the current safeguard (pending inbox check), run configurable checks before commit — lint, type-check, test subset — blocks commit until all pass

### Intelligence & Context

| Priority | Feature |
|----------|---------|
| High | LSP integration for agent tools |
| Medium | Memory tagging & expiry |
| Medium | Agent handoff protocol |
| Medium | MCP protocol support |
| Low | Semantic memory search |

- **LSP integration for agent tools** — Auto-manage LSP servers for project languages — inject diagnostics into edit/write tool results so agents see type errors and lint warnings immediately after file changes. Start with Go (`gopls`), TypeScript (`typescript-language-server`), Python (`pyright`). Auto-download LSP binaries on first use, disable via `MUXCODE_DISABLE_LSP`. Inspired by OpenCode's 30+ language LSP integration
- **Memory tagging & expiry** — Tag memory entries with categories (bug-fix, convention, workaround) and optional TTL — auto-expire stale workarounds, improves signal-to-noise in memory search
- **Agent handoff protocol** — Structured handoff when one agent needs another to continue its work — includes context bundle (relevant files, conversation excerpt, constraints), not just "send a message"
- **MCP protocol support** — Model Context Protocol server integration for external resource access — databases, APIs, custom data sources. Configure MCP servers in `.muxcode/config` or `opencode.json`-compatible format. Agents access external resources via `mcp-read` tool. Inspired by OpenCode's MCP integration
- **Semantic memory search** — Augment BM25 with embeddings (local via Ollama embedding models) for semantic similarity — falls back to BM25 when Ollama unavailable

### UX & Dashboard

| Priority | Feature |
|----------|---------|
| High | Dashboard activity timeline |
| Medium | TUI theme system |
| Medium | Agent log viewer in TUI |
| Low | Notification sound/bell |
| Low | Session recording & replay |

- **Dashboard activity timeline** — Visual timeline in TUI showing message flow between agents over time — like a sequence diagram but live — currently dashboard shows status tables but no temporal view
- **TUI theme system** — Configurable color themes for the dashboard TUI and left-pane log scripts — ship built-in themes (Dracula default, Tokyo Night, Catppuccin, Nord, Gruvbox), support custom themes via JSON files in `~/.config/muxcode/themes/` or `.muxcode/themes/`. Theme applies to dashboard, log scripts, and tmux status bar. Inspired by OpenCode's theme system
- **Agent log viewer in TUI** — Navigate and search `log.jsonl` from the dashboard — filter by role, action, time range — currently requires `muxcode history` CLI
- **Notification sound/bell** — Optional terminal bell or macOS notification on important events (build failure, review complete, agent-down) — configurable per-event
- **Session recording & replay** — Record all bus messages during a session for later replay/analysis — useful for demos, debugging, understanding multi-agent interactions — inverse of demo mode (record real sessions)

### Integrations

| Priority | Feature |
|----------|---------|
| High | GitHub Actions webhook bridge |
| Medium | Slack/Discord notifications |
| Medium | IDE status bar |
| Medium | GitHub App for comment-triggered agents |
| Low | Linear/Jira bidirectional sync |

- **GitHub Actions webhook bridge** — Pre-built GitHub Actions workflow that POSTs to the webhook endpoint on PR events (opened, review submitted, CI status) — turns external events into agent actions
- **Slack/Discord notifications** — Forward important agent events (build failure, deploy complete, review findings) to a Slack/Discord channel via webhook URL — one-way, config-driven
- **IDE status bar** — Lightweight status indicator for VS Code / Neovim showing agent states and inbox counts — read-only, polls bus directory — for Neovim: a Lua plugin reading lock files
- **GitHub App for comment-triggered agents** — GitHub App + Actions workflow that triggers MuxCode agents from PR/issue comments — `/muxcode fix this`, `/muxcode review`, `/muxcode explain`. Agent runs in CI runner, posts results as PR comment. Beyond current webhook bridge (inbound only). Inspired by OpenCode's `/opencode` GitHub integration
- **Linear/Jira bidirectional sync** — Beyond current Jira description updates — auto-update issue status based on agent activity (e.g. move to "In Review" when review agent starts)

### Security & Isolation

| Priority | Feature |
|----------|---------|
| High | Secret scanning in commits |
| Medium | Agent sandbox levels |
| Low | Webhook rate limiting |

- **Secret scanning in commits** — Pre-commit agent check scans staged diffs for patterns matching API keys, tokens, passwords — blocks commit and alerts edit. PII scrubbing (`bus/scrub.go`, `harness/scrub.go`) partially addresses this for tool output but not for commits
- **Agent sandbox levels** — Graduated trust levels — `read-only`, `project-scoped`, `unrestricted` — new agents start at read-only and escalate based on config, more granular than current tool profiles
- **Webhook rate limiting** — Per-IP and global rate limits on the webhook endpoint — currently only has auth token + localhost binding, important if exposing via tunnel

### Developer Experience

| Priority | Feature |
|----------|---------|
| High | `muxcode init` wizard |
| Medium | Agent definition linting |
| Low | Skill marketplace |
| Medium | Custom slash commands |
| Low | Multi-repo sessions |

- **`muxcode init` wizard** — Interactive project setup — detects project type, generates `.muxcode/config`, copies relevant agent overrides, suggests window layout
- **Agent definition linting** — Validate agent markdown files — check frontmatter schema, verify referenced tools exist in profiles, warn about common mistakes — `muxcode agent lint`
- **Skill marketplace** — Community-shared skills via a git-based registry — `muxcode skill install <url>` — each skill is a markdown file with frontmatter, already the right format
- **Custom slash commands** — User-defined slash commands with argument interpolation — markdown files in `.muxcode/commands/` with `$ARGUMENTS`, `$1`/`$2` positional args, `` !`command` `` for bash output injection, `@file` for content inclusion. Override built-in commands by name. Extends current skills (passive injection) with active command triggers. Inspired by OpenCode's custom commands system
- **Multi-repo sessions** — Support sessions spanning multiple related repos (monorepo-like) — each repo gets its own bus directory but agents can cross-reference

## Sources

- [OpenClaw](https://openclaw.ai/) — architecture inspiration for many features
- [OpenClaw Architecture Overview](https://ppaolo.substack.com/p/openclaw-system-architecture-overview)
- [OpenCode](https://opencode.ai/) — open source AI coding agent with LSP integration, MCP protocol, multi-provider support, theme system, GitHub App, custom commands
- [OpenCode DeepWiki](https://deepwiki.com/anomalyco/opencode) — architecture analysis

