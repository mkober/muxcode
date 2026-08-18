# Requirements Backlog

## Completed

51 delivered feature specs live in [`completed/`](./completed/). Each file contains requirements, key files, and implementation notes.

## In progress

| Feature | Spec | Status |
|---------|------|--------|
| Agent mode | [`docs/requirements/drafts/agent-mode.md`](../drafts/agent-mode.md) | In Progress — Phases 1-7 complete, all acceptance criteria checked |
| Branch active-time tracking | [`docs/requirements/drafts/branch-time-tracking.md`](../drafts/branch-time-tracking.md) | In Progress — `--json` read path + verify-spec doc sink implemented (`seed`/`record` reconciliation); remaining: docs bullets + Phase 3 integration test |
| Disk-pressure signal fix | [`docs/requirements/drafts/disk-pressure-wrong-filesystem.md`](../drafts/disk-pressure-wrong-filesystem.md) | In Progress — Phase 1 complete, integration test outstanding |
| Lifecycle log test leak | [`docs/requirements/backlog/lifecycle-log-test-leak.md`](./lifecycle-log-test-leak.md) | In Progress — fix shipped, regression test outstanding |
| Modal auto-size | [`docs/requirements/drafts/modal-auto-size.md`](../drafts/modal-auto-size.md) | Complete — all 6 phases verified; ready to move to `completed/` |

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
| High | `muxcode diagnose` false clean verdict on dead agents |
| High | Verify-spec refires on stale unconsumed review message |
| High | Unverified daemon auto-restart (fire-and-hope relaunch) |
| High | Response payloads re-trigger chains on non-hook agents |
| High | Run chain fires watch on every successful command |
| Medium | Disk-pressure check measures the wrong filesystem |
| Low | Message delivery receipts |
| Low | Bus audit trail |

- **Structured agent metrics** — Track per-agent metrics (messages sent/received, tool calls, errors, avg response time) in `metrics.jsonl` — dashboard TUI shows metrics panel
- **File integrity validation** — Timestamp-based change detection on file operations — detect external modifications between read and edit/write, warn agent of stale content before applying changes. Inspired by OpenCode's file integrity checks
- **Tool-call doom loop detection** — Detect 3+ identical consecutive tool calls within a single agent turn (same tool, same args) — prompt user or abort. Complements existing message-level loop detection in `bus/guard.go`. Inspired by OpenCode's `doom_loop` permission
- **`muxcode diagnose` false clean verdict on dead agents** — `diagnose` collects `AgentState.IsAlive` but no detector evaluates it: a dead agent's report prints `State: dead` yet concludes `✅ No issues detected` with exit 0, so automation gating on the exit code reads a dead agent as healthy. Fix: `checkAgentDead` registered first in `diagnosticChecks`, critical severity, remediation `muxcode agent-health --start <role>`. Spec: [`diagnose-false-clean-verdict`](./diagnose-false-clean-verdict.md)
- **Verify-spec refires on stale unconsumed review message** — one review completion generated 4+ identical `verify-spec` requests to plan in ~2 min: `checkInboxes()` fires the reviewed-transition on **any** edit-inbox growth while **any** unconsumed review→edit message exists, and plan's mandated "reply to edit" is itself growth — a self-sustaining loop while edit is busy. Fix: fire once per actual completion (track last-seen review message ID or inspect the growth delta). Spec: [`verify-spec-stale-review-refire`](./verify-spec-stale-review-refire.md)
- **Unverified daemon auto-restart** — `RestartLocalAgent()` (`bus/health.go`) sends `C-c`, sleeps a fixed 500ms, sends the relaunch line, and returns `nil` unconditionally — no exit wait, no launch verification. A slow-exiting CLI swallows the launch line, the pane settles at a bare shell, and the daemon still emits `agent-recovered`; pane-based `IsAgentAlive` reads the swallowed launch text as alive indefinitely (field report: orphan `opencode --agent build` detached from its pane). Fix: bounded exit poll + post-relaunch verification (reuse `ReloadAgent()`'s 15s poll), failed restarts count toward the attempt cap, orphan detection. Spec: [`unverified-daemon-auto-restart`](./unverified-daemon-auto-restart.md)
- **Response payloads re-trigger chains on non-hook agents** — non-hook `SendWakeUp` injects `type: response` payloads into the TUI composer as prompts, so an agent re-fires its chain on its own delegation's answer (observed: `build` re-sent `request:test` 5× in 3.5 min, 84.9K tokens burned on `test`). The existing echo guard suppresses only the reply *instruction*, not the *payload*. Secondary driver: answered-but-unconsumed requests keep counting as actionable, so the daemon re-wakes agents for finished work. Fix: never inject responses as prompts + requester-side responded-check in `HasActionableMessages`. Spec: [`response-echo-chain-retrigger`](./response-echo-chain-retrigger.md)
- **Run chain fires watch on every successful command** — the run agent's `OnSuccess` chain (`bus/profile.go:918-941`) sends watch a "tail logs to verify deployed services" request for **any** successful command except `muxcode *` — a read-only `cat` triggers a bogus tail-logs request, and the daemon logged a `run→watch` loop 4× in 4m44s (capped only by relay suppression's 4-per-300s backstop). Root cause: denylist gate on an allowlist-shaped trigger. Fix: first-match-wins `command_match` allowlist of verification-run shapes; docs sync (condition-type list is stale at 8, actual 10). Spec: [`run-chain-watch-overfire`](./run-chain-watch-overfire.md)
- **Disk-pressure check measures the wrong filesystem** — **In progress** — The daemon's `/tmp` pressure watchdog reported boot-volume percent-used (`Statfs` on macOS's symlinked `/private/tmp`), fired perpetually on healthy 85–90%-full dev machines, and its cleanup freed 0 B. Now measures free-bytes headroom and muxcode footprint (`TmpPressure()`); integration test outstanding. Spec: [`disk-pressure-wrong-filesystem`](../drafts/disk-pressure-wrong-filesystem.md)
- **Lifecycle log test leak** — **In progress** — `LifecycleLogDir()` resolved unconditionally to `~/.config/muxcode/logs`, so test runs deposited real per-session log files into the live install (41,789 stray `test-*.log` files, ~169 MB). Fixed via `MUXCODE_LIFECYCLE_LOG_DIR` override pinned by `TestMain`; automated regression test outstanding. Spec: [`lifecycle-log-test-leak`](./lifecycle-log-test-leak.md)
- **Message delivery receipts** — ~~Agents ACK message consumption~~ **Delivered** via [`delivery-acknowledgement`](../drafts/delivery-acknowledgement.md) (Phases 1–6 committed): per-message receipts (`acked` for Claude/harness, verified-inject `delivered` for OpenCode/Codex), agent self-poll, and a `checkPollHealth` receipt-gap backstop replace pane-scrape delivery inference. Remaining follow-up: [`remove-gated-pane-scrape-delivery`](./remove-gated-pane-scrape-delivery.md) — physically delete the old machinery (currently gated OFF behind `MUXCODE_DELIVERY_ACK`) once the cutover is proven stable live
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
| High | OpenCode plugin hook bridge |
| Medium | Pipeline definitions |
| Medium | Retry with backoff |
| Medium | Workspace checkpoints |
| Medium | Undo/redo for agent file changes |
| Low | Pre-commit hooks |

- **Conditional chains** — Extend event chains with conditions beyond exit codes — file pattern matching (only run deploy chain if infra files changed), time-of-day gates, branch name filters
- **OpenCode plugin hook bridge** — OpenCode has no shell hooks, but its plugin system exposes `tool.execute.after` / `session.idle` events: a muxcode-authored TS plugin auto-installed by `WriteAgentConfig()` shells out to `muxcode hook bash` on tool completion, restoring deterministic build→test→review chains for OpenCode agents (today chains are LLM-followed instructions — the root enabler of the [`response-echo-chain-retrigger`](./response-echo-chain-retrigger.md) storms). Narrow capability flag (`SupportsChainEvents()`), not a `SupportsHooks()` flip — guard stays on `DenyTools`. Targets the stable v1 event surface; v2's `Plugin.define` API is beta. Spec: [`opencode-plugin-hook-bridge`](./opencode-plugin-hook-bridge.md)
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
| High | Gemini CLI provider |
| High | GitHub Actions webhook bridge |
| Medium | Slack/Discord notifications |
| Medium | IDE status bar |
| Medium | GitHub App for comment-triggered agents |
| Low | Linear/Jira bidirectional sync |

- **Gemini CLI provider** — `MUXCODE_AGENT_CLI=gemini` silently falls through `ResolveProvider()`'s `default:` case to `ClaudeCodeProvider` and launches Claude; `install.sh` deliberately omits Gemini from its catalogue for this reason. Gemini CLI has a full hook system (`BeforeTool`/`AfterTool`, exit-code-2 blocking), making it the first alternative provider that can run `SupportsHooks() = true` — deterministic chains, diff preview, and edit guard without degradation. 7 phases: provider struct, `.gemini/settings.json` hook config, dual-format hook script adaptation, idle/wake-up, compaction, tool-name translation, integration test + installer catalogue entry. Spec: [`gemini-cli-provider`](./gemini-cli-provider.md)
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

