# Requirements Backlog

Index of all pending requirement specs. Every planned feature with a written spec has a row
in the [Spec index](#spec-index) below; ideas without a spec doc yet are collected in
[Ideas without specs](#ideas-without-specs). 72 delivered specs live in
[`completed/`](../completed/) — each with requirements, key files, and implementation notes.

Spec lifecycle: `backlog/` (planned or parked) → `drafts/` (actively being designed or
implemented) → `completed/` (implemented and verified).

## GitHub tracking (MUX ids)

Work on this repo is tracked through GitHub issues, branches, and pull requests. Each spec
in the index carries a stable **`MUX-NNN`** id that ties the req doc to its GitHub artifacts:

| Artifact | Convention | Example |
|----------|-----------|---------|
| Req doc (once work starts) | `MUX-NNN-<slug>.md` | `docs/requirements/drafts/MUX-011-gemini-cli-provider.md` |
| GitHub issue title | `MUX-NNN: <summary>` | `MUX-011: Gemini CLI provider` |
| Branch | `MUX-NNN-<slug>` | `MUX-011-gemini-cli-provider` |
| PR title | `MUX-NNN: <summary>` | `MUX-011: Gemini CLI provider` |

- Ids are assigned in the Spec index below and never reused or renumbered — the index is
  the registry.
- The spec file gains its `MUX-NNN-` filename prefix when its GitHub issue is created
  (rename + cross-link fixes handled by the plan agent); until then the id lives only in
  the index.
- `MUX-NNN` matches the `[A-Z][A-Z0-9]*-[0-9]+` key shape existing muxcode tooling expects
  (story-lifecycle `{KEY}-*.md` spec lookup, branch-time key-prefix matching, branch-name
  key extraction), so branches and specs named this way work with no code changes.

## Spec index

### In progress

| ID | Spec | Priority | Status |
|----|------|----------|--------|
| MUX-001 | [`branch-time-tracking.md`](./branch-time-tracking.md) | High | In Progress — read path + verify-spec doc sink done; remaining: docs bullets (`architecture.md`, `agent-bus.md`, `CLAUDE.md`) + Phase 3 test extensions |
| MUX-002 | [`disk-pressure-wrong-filesystem.md`](./disk-pressure-wrong-filesystem.md) | Medium | In Progress — `TmpPressure()` headroom+footprint signals shipped; integration test outstanding |
| MUX-003 | [`echo-as-result.md`](./echo-as-result.md) | High | Complete — fix shipped via `bus.NewBusResponseEntry` provenance; Phase 4 test script (`scripts/test-echo-as-result.sh`, 20/20 checks, no live session needed) done; ready to move to `completed/` |
| MUX-004 | [`lifecycle-log-test-leak.md`](./lifecycle-log-test-leak.md) | Medium | Complete — `MUXCODE_LIFECYCLE_LOG_DIR` pin shipped; regression test (`scripts/test-lifecycle-log-leak.sh`, 7/7 checks, redirected-HOME design) + `tui/main_test.go` pin done; ready to move to `completed/` |
| MUX-005 | [`plan-diagrams.md`](./plan-diagrams.md) | Medium | In Progress — Phases 1–3 complete (render script, media store, req-doc embeds); Jira/Confluence embed phases remaining |

### Reliability & observability

| ID | Spec | Priority | Summary |
|----|------|----------|---------|
| MUX-006 | [`diagnose-false-clean-verdict.md`](./diagnose-false-clean-verdict.md) | High | `diagnose` collects `IsAlive` but no detector reads it — a dead agent gets "No issues detected" exit 0; add `checkAgentDead` first in `diagnosticChecks` |
| MUX-007 | [`verify-spec-stale-review-refire.md`](./verify-spec-stale-review-refire.md) | High | `checkInboxes()` refires the reviewed-transition on any edit-inbox growth while an unconsumed review message exists — one review completion spawns unbounded `verify-spec` echoes |
| MUX-008 | [`unverified-daemon-auto-restart.md`](./unverified-daemon-auto-restart.md) | High | `RestartLocalAgent()` fire-and-hope relaunch: no exit wait, no launch verification — add bounded exit poll + post-relaunch verification + orphan detection |
| MUX-009 | [`response-echo-chain-retrigger.md`](./response-echo-chain-retrigger.md) | High | Non-hook `SendWakeUp` injects response payloads as prompts, re-firing chains on a delegation's own answer; never inject responses + responded-check in `HasActionableMessages` |
| MUX-010 | [`delegation-message-hygiene.md`](./delegation-message-hygiene.md) | Medium | Agent-freeze auto-recovery + delegation hygiene: force-terminate for hung-but-alive agents, payload/format rules enforced at the bus |
| MUX-012 | [`remove-gated-pane-scrape-delivery.md`](./remove-gated-pane-scrape-delivery.md) | Low | Physically delete the pane-scrape delivery machinery bypassed by the receipt cutover ([delivery-acknowledgement](../completed/delivery-acknowledgement.md)); gated on default-ON soak + backstop mis-fire fix |
| MUX-013 | [`channels-message-transport.md`](./channels-message-transport.md) | Medium | Replace file-polling inbox transport with channel-based delivery |

### Workflow & automation

| ID | Spec | Priority | Summary |
|----|------|----------|---------|
| MUX-011 | [`opencode-plugin-hook-bridge.md`](./opencode-plugin-hook-bridge.md) | High | Muxcode TS plugin on OpenCode's `tool.execute.after`/`session.idle` events shells to `muxcode hook bash` — deterministic chains for OpenCode via narrow `SupportsChainEvents()`, root enabler fix for MUX-009 storms |
| MUX-014 | [`graph-agent-orchestrator.md`](./graph-agent-orchestrator.md) | Medium | DAG control plane over bus/chains/spawns/tasks: 7 node types, outcome-keyed edges, joins, durable resumable runs (`muxcode graph run\|status\|cancel\|retry`); commit/Atlassian nodes require `wait_human`. Subsumes the "Pipeline definitions" idea |

### Agents & roles

| ID | Spec | Priority | Summary |
|----|------|----------|---------|
| MUX-015 | [`refactor-agent.md`](./refactor-agent.md) | Medium | F6 review ↔ refactor mode toggle: a write-capable refactoring specialist paired with the read-only reviewer |
| MUX-016 | [`research-dual-provider.md`](./research-dual-provider.md) | Medium | Research window split into multiple provider panes (`research-N` bus identities) with broadcast/relay/synthesize across providers |

### Integrations & providers

| ID | Spec | Priority | Summary |
|----|------|----------|---------|
| MUX-017 | [`gemini-cli-provider.md`](./gemini-cli-provider.md) | High | `GeminiProvider` with full hook support (`BeforeTool`/`AfterTool`) — first alternative provider that can run `SupportsHooks() = true`; fixes the silent Claude fall-through in `ResolveProvider()` |
| MUX-018 | [`opencode-diff-preview-plugin.md`](./opencode-diff-preview-plugin.md) | Medium | OpenCode plugin restoring the nvim diff split preview that hook-less providers lose |
| MUX-019 | [`github-user-stats.md`](./github-user-stats.md) | Medium | Per-user GitHub contribution stats surfaced through the bus/CLI |

### UX & tooling

| ID | Spec | Priority | Summary |
|----|------|----------|---------|
| MUX-020 | [`cli-help-command.md`](./cli-help-command.md) | Low | `muxcode help` command: discoverable, grouped CLI reference |
| MUX-021 | [`demo-mode-agent-coverage.md`](./demo-mode-agent-coverage.md) | Low | Refresh `bus/demo.go` scenarios to cover the current agent roster |
| MUX-022 | [`design-mode.md`](./design-mode.md) | Low | Design mode for UI-centric sessions |
| MUX-023 | [`modal-cron-manager.md`](./modal-cron-manager.md) | Low | Interactive cron schedule manager modal |
| MUX-024 | [`modal-history-viewer.md`](./modal-history-viewer.md) | Low | Bus history browser modal with filtering |
| MUX-025 | [`modal-log-viewer.md`](./modal-log-viewer.md) | Low | Lifecycle/log viewer modal |
| MUX-026 | [`modal-memory-browser.md`](./modal-memory-browser.md) | Low | Memory browser modal with BM25 search |
| MUX-027 | [`modal-webhook-monitor.md`](./modal-webhook-monitor.md) | Low | Live webhook request inspector modal with replay |

## Ideas without specs

Curated ideas that have no requirements doc yet. Writing the spec (and giving it the next
free MUX id) is the first step to promoting one.

### Reliability & observability

- **Structured agent metrics** (Medium) — Track per-agent metrics (messages sent/received, tool calls, errors, avg response time) in `metrics.jsonl` — dashboard TUI shows metrics panel
- **File integrity validation** (Medium) — Timestamp-based change detection on file operations — detect external modifications between read and edit/write, warn agent of stale content before applying changes. Inspired by OpenCode's file integrity checks
- **Tool-call doom loop detection** (Medium) — Detect 3+ identical consecutive tool calls within a single agent turn (same tool, same args) — prompt user or abort. Complements existing message-level loop detection in `bus/guard.go`. Inspired by OpenCode's `doom_loop` permission
- **Bus audit trail** (Low) — Append-only audit log separate from `log.jsonl` capturing all bus operations (send, consume, lock, unlock, cron fire, proc start/stop) with caller identity — post-session debugging. Partially addressed by lifecycle logging (`~/.config/muxcode/logs/`)

### Performance & cost

- **Agent max steps / iteration limits** (High) — Per-role configurable maximum tool-call iterations per message — `MUXCODE_{ROLE}_MAX_STEPS` or profile field. Prevents runaway API costs from stuck agents. Harness circuit breaker handles local LLM; this extends to Claude Code agents via conversation turn counting. Inspired by OpenCode's `maxSteps` per agent
- **On-demand agent spawning** (Medium) — Convert runner, watch, and analyst from always-on to deferred launch on first message — tmux windows still created for left-pane pollers, agent process starts only when a bus message targets the role
- **Smart context pruning** (Medium) — Before hitting compaction threshold, auto-prune low-relevance memory entries (BM25-scored against recent activity) — more surgical than full session compact
- **Tiered model routing** (Medium) — Route simple/structured tasks (git status, build) to cheaper/faster models (Haiku) and complex tasks (review, analysis) to Opus — config-driven per-role model selection
- **Batch message coalescing** (Low) — When multiple messages arrive in an agent's inbox between polls, coalesce into a single prompt rather than processing sequentially — reduces context overhead and API calls

### Workflow & automation

- **Retry with backoff** (Medium) — Configurable retry policy for failed chain steps — exponential backoff, max attempts, different behavior per step
- **Workspace checkpoints** (Medium) — Snapshot working directory state before risky operations (deploy, large refactor) — allows rollback via `muxcode checkpoint restore`, leverages `git stash` or worktrees internally
- **Undo/redo for agent file changes** (Medium) — Track file snapshots before each agent Write/Edit operation — `muxcode undo [steps]` restores previous state via git stash or shadow copies. Inspired by OpenCode's `/undo` and `/redo` commands
- **Pre-commit hooks** (Low) — Beyond the current safeguard (pending inbox check), run configurable checks before commit — lint, type-check, test subset — blocks commit until all pass

### Intelligence & context

- **LSP integration for agent tools** (High) — Auto-manage LSP servers for project languages — inject diagnostics into edit/write tool results so agents see type errors and lint warnings immediately after file changes. Start with Go (`gopls`), TypeScript (`typescript-language-server`), Python (`pyright`). Auto-download LSP binaries on first use, disable via `MUXCODE_DISABLE_LSP`. Inspired by OpenCode's 30+ language LSP integration
- **Memory tagging & expiry** (Medium) — Tag memory entries with categories (bug-fix, convention, workaround) and optional TTL — auto-expire stale workarounds, improves signal-to-noise in memory search
- **Agent handoff protocol** (Medium) — Structured handoff when one agent needs another to continue its work — includes context bundle (relevant files, conversation excerpt, constraints), not just "send a message"
- **MCP protocol support** (Medium) — Model Context Protocol server integration for external resource access — databases, APIs, custom data sources. Configure MCP servers in `.muxcode/config` or `opencode.json`-compatible format. Inspired by OpenCode's MCP integration
- **Semantic memory search** (Low) — Augment BM25 with embeddings (local via Ollama embedding models) for semantic similarity — falls back to BM25 when Ollama unavailable

### UX & dashboard

- **Dashboard activity timeline** (High) — Visual timeline in TUI showing message flow between agents over time — like a sequence diagram but live — currently dashboard shows status tables but no temporal view
- **TUI theme system** (Medium) — Configurable color themes for the dashboard TUI and left-pane log scripts — built-in themes (Dracula default, Tokyo Night, Catppuccin, Nord, Gruvbox), custom themes via JSON in `~/.config/muxcode/themes/` or `.muxcode/themes/`. Inspired by OpenCode's theme system
- **Agent log viewer in TUI** (Medium) — Navigate and search `log.jsonl` from the dashboard — filter by role, action, time range — currently requires `muxcode history` CLI
- **Notification sound/bell** (Low) — Optional terminal bell or macOS notification on important events (build failure, review complete, agent-down) — configurable per-event
- **Session recording & replay** (Low) — Record all bus messages during a session for later replay/analysis — useful for demos, debugging, understanding multi-agent interactions — inverse of demo mode

### Integrations

- **GitHub Actions webhook bridge** (High) — Pre-built GitHub Actions workflow that POSTs to the webhook endpoint on PR events (opened, review submitted, CI status) — turns external events into agent actions
- **Slack/Discord notifications** (Medium) — Forward important agent events (build failure, deploy complete, review findings) to a Slack/Discord channel via webhook URL — one-way, config-driven
- **IDE status bar** (Medium) — Lightweight status indicator for VS Code / Neovim showing agent states and inbox counts — read-only, polls bus directory — for Neovim: a Lua plugin reading lock files
- **GitHub App for comment-triggered agents** (Medium) — GitHub App + Actions workflow that triggers MuxCode agents from PR/issue comments — `/muxcode fix this`, `/muxcode review`, `/muxcode explain`. Agent runs in CI runner, posts results as PR comment. Inspired by OpenCode's `/opencode` GitHub integration
- **Linear/Jira bidirectional sync** (Low) — Beyond current Jira description updates — auto-update issue status based on agent activity (e.g. move to "In Review" when review agent starts)

### Security & isolation

- **Secret scanning in commits** (High) — Pre-commit agent check scans staged diffs for patterns matching API keys, tokens, passwords — blocks commit and alerts edit. PII scrubbing (`bus/scrub.go`, `harness/scrub.go`) partially addresses this for tool output but not for commits
- **Agent sandbox levels** (Medium) — Graduated trust levels — `read-only`, `project-scoped`, `unrestricted` — new agents start at read-only and escalate based on config, more granular than current tool profiles
- **Webhook rate limiting** (Low) — Per-IP and global rate limits on the webhook endpoint — currently only has auth token + localhost binding, important if exposing via tunnel

### Developer experience

- **`muxcode init` wizard** (High) — Interactive project setup — detects project type, generates `.muxcode/config`, copies relevant agent overrides, suggests window layout
- **Agent definition linting** (Medium) — Validate agent markdown files — check frontmatter schema, verify referenced tools exist in profiles, warn about common mistakes — `muxcode agent lint`
- **Custom slash commands** (Medium) — User-defined slash commands with argument interpolation — markdown files in `.muxcode/commands/` with `$ARGUMENTS`, positional args, bash output injection, `@file` inclusion. Inspired by OpenCode's custom commands system
- **Skill marketplace** (Low) — Community-shared skills via a git-based registry — `muxcode skill install <url>` — each skill is a markdown file with frontmatter, already the right format
- **Multi-repo sessions** (Low) — Support sessions spanning multiple related repos (monorepo-like) — each repo gets its own bus directory but agents can cross-reference

## Sources

- [OpenClaw](https://openclaw.ai/) — architecture inspiration for many features
- [OpenClaw Architecture Overview](https://ppaolo.substack.com/p/openclaw-system-architecture-overview)
- [OpenCode](https://opencode.ai/) — open source AI coding agent with LSP integration, MCP protocol, multi-provider support, theme system, GitHub App, custom commands
- [OpenCode DeepWiki](https://deepwiki.com/anomalyco/opencode) — architecture analysis
