# OpenClaw-Inspired Suggestions for Muxcode

Feature suggestions for making muxcode agents and the agent-bus more dynamic and useful, based on analysis of the [OpenClaw](https://openclaw.ai/) architecture.

## 1. Persistent memory with semantic search

**OpenClaw**: Vector-embedded memory in SQLite with hybrid BM25 + semantic search. Daily notes (`memory/YYYY-MM-DD.md`), curated long-term facts (`MEMORY.md`), and automatic session transcript indexing.

**Muxcode today**: Flat file memory via `muxcode-agent-bus memory write`. No search, no embeddings, no daily rotation.

**Suggestions**:
- Add `memory search "<query>"` subcommand with keyword matching (BM25-style) across memory sections
- Daily memory rotation — auto-create `memory/YYYY-MM-DD.jsonl` files, keep a rolling window
- Per-agent + shared memory scopes already exist — add a `memory list` command to show all sections
- Session summary on exit — when a muxcode session ends, auto-summarize key decisions into memory

## 2. Skills / plugin system

**OpenClaw**: `SKILL.md` files define capabilities, hot-reloadable, agents can write their own skills, ClawHub marketplace for community skills.

**Muxcode today**: Agent definitions are static `.md` files. No runtime extensibility.

**Suggestions**:
- Add a `skills/` directory alongside `agents/` — each skill is a markdown file with a tool definition + instructions
- Skills get injected into agent system prompts only when relevant (selective injection, not all-at-once)
- `muxcode-agent-bus skill list` / `skill load <name>` subcommands
- Hot-reload: watch `skills/` directory, re-inject into agent context when skills change
- Let agents create skills: a bus subcommand `skill create "<name>" "<content>"` that writes a new skill file

## 3. Session compaction / context management

**OpenClaw**: Automatic context compaction — older conversation portions are summarized when approaching model limits. Memory flush promotes durable info before condensation.

**Muxcode today**: No context management. Agents run until context fills, then lose everything.

**Suggestions**:
- Add a `session compact` bus subcommand that triggers a summary of the current conversation
- Watcher-based: when agent context approaches limits, auto-write key context to memory before it's lost
- Session resume hints — on agent restart, load relevant memory sections into the system prompt

## 4. Cron / scheduled tasks

**OpenClaw**: Gateway-level cron jobs trigger agent actions at specified times. Webhooks provide external triggers.

**Muxcode today**: No scheduling. Everything is reactive (human request -> bus message -> agent acts).

**Suggestions**:
- Add `muxcode-agent-bus cron add "<schedule>" "<target>" "<action>" "<message>"` — stores cron entries in the bus directory
- A cron daemon thread in the watcher process that fires bus messages on schedule
- Use cases: periodic `git status` reports, scheduled test runs, daily code review of uncommitted changes
- Webhook endpoint: a simple HTTP listener in the bus that accepts POST requests and converts them to bus messages (e.g., GitHub webhook -> build agent)

## 5. Tool profiles (layered permissions)

**OpenClaw**: Predefined profiles (`minimal`, `coding`, `messaging`, `full`) with layered allow/deny and wildcard groups like `group:fs`, `group:runtime`.

**Muxcode today**: Per-role `allowed_tools()` function with explicit tool lists hardcoded in bash.

**Suggestions**:
- Define tool profiles in a config file (`config/tool-profiles.json`) instead of hardcoding in bash
- Support profile composition: `base + role-specific` overrides
- Add tool groups: `group:git-readonly`, `group:build`, `group:test`, `group:cloud` — referenced by name in agent configs
- Move `allowed_tools()` out of bash and into the Go binary: `muxcode-agent-bus tools <role>` outputs the flags — easier to maintain and test

## 6. Inter-agent session inspection ✅ (partial)

**OpenClaw**: `sessions_list`, `sessions_send`, `sessions_history`, `sessions_spawn` tools let agents discover and communicate across sessions.

**Muxcode today**: Bus messages are fire-and-forget. No way to inspect another agent's conversation or spawn sub-tasks.

**Suggestions**:
- ✅ `muxcode-agent-bus history <role>` — show recent messages to/from a specific agent
- ✅ `muxcode-agent-bus status` — show all agents' current state (busy/idle/last-message)
- `muxcode-agent-bus spawn <role> "<task>"` — create a temporary agent session for a one-off task, collect result, tear down *(deferred to follow-up)*
- ✅ Agent-to-agent context sharing: an agent can request another agent's last N messages for context (`history --context`)

## 7. Loop detection / guardrails ✅

**OpenClaw**: Built-in loop detection that catches repetitive no-progress tool-call patterns.

**Muxcode today**: ~~No guardrails. An agent can spin in circles forever.~~ Implemented via `muxcode-agent-bus guard`.

**Implemented**:
- ✅ `muxcode-agent-bus guard [role] [--json] [--threshold N] [--window N]` — on-demand loop check
- ✅ Command loop detection: same command failing N+ times in a time window (from `{role}-history.jsonl`)
- ✅ Message loop detection: repeated or ping-pong messages between agents (from `log.jsonl`)
- ✅ Auto-escalate to edit agent via watcher integration (checks every 30s, deduplicates within 5m cooldown)
- ✅ Configurable thresholds and time windows via CLI flags

## 8. Dynamic system prompt composition

**OpenClaw**: Prompts compose from `AGENTS.md` + `SOUL.md` + `TOOLS.md` + dynamic context + skill definitions + memory search results.

**Muxcode today**: Static agent `.md` files. No dynamic context injection.

**Suggestions**:
- Support a `context.d/` directory per agent — drop-in context files that get appended to the system prompt
- Auto-inject recent memory into agent prompts on launch
- Project-aware context: auto-detect project type and inject relevant conventions (e.g., Go conventions for Go projects, CDK conventions for CDK projects)
- `muxcode-agent-bus context <role>` — outputs the assembled prompt for debugging

## 9. Process management (background tasks)

**OpenClaw**: `process` tool manages backgrounded tasks — poll, log, write, kill, clear — scoped per agent.

**Muxcode today**: No background task tracking. Build/test are one-shot.

**Suggestions**:
- `muxcode-agent-bus proc start <role> "<command>"` — launch and track a background process
- `muxcode-agent-bus proc list` / `proc log <id>` / `proc kill <id>`
- Use case: long-running builds, watch-mode test runners, `cdk deploy` that takes minutes
- Auto-notify the requesting agent when a background process completes

## 10. Event-driven automation chains

**OpenClaw**: Webhook + cron + channel routing creates event-driven workflows without custom code.

**Muxcode today**: Bash hook chain (build->test->review) is hardcoded in hook scripts.

**Suggestions**:
- Make the chain configurable: `config/chains.json` defines `{ "build:success": ["test"], "test:success": ["review"], "test:failure": ["edit"] }`
- Support custom chains per project (override in `.muxcode/config`)
- Add event types beyond build/test: `file:changed`, `git:commit`, `git:push`, `deploy:diff`
- Allow agents to register interest in events: `muxcode-agent-bus subscribe <event-pattern>`

## Priority ranking

| # | Feature | Impact | Effort | Priority |
|---|---------|--------|--------|----------|
| 1 | Memory search | High — agents lose context constantly | Medium | **P0** |
| 5 | Tool profiles in config | High — maintainability, testability | Medium | **P0** |
| 10 | Configurable chains | High — removes hardcoded logic | Low | **P1** |
| 6 | Session inspection | High — debugging, agent coordination | Medium | **P1** |
| 7 | Loop detection | Medium — prevents wasted tokens | Low | **P1** |
| 4 | Cron / scheduling | Medium — enables proactive agents | Medium | **P2** |
| 8 | Dynamic prompt composition | Medium — better context | Medium | **P2** |
| 2 | Skills system | Medium — extensibility | High | **P2** |
| 9 | Process management | Medium — long tasks | Medium | **P3** |
| 3 | Session compaction | Low — Claude Code manages its own context | High | **P3** |

## Sources

- [OpenClaw — Personal AI Assistant](https://openclaw.ai/)
- [OpenClaw Architecture, Explained](https://ppaolo.substack.com/p/openclaw-system-architecture-overview)
- [OpenClaw Tools Documentation](https://docs.openclaw.ai/tools)
- [OpenClaw GitHub](https://github.com/openclaw/openclaw)
- [OpenClaw in 2026 Guide](https://vallettasoftware.com/blog/post/openclaw-2026-guide)
