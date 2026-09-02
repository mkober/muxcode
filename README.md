# MuxCode

```
███╗   ███╗██╗   ██╗██╗  ██╗   ██████╗ ██████╗ ██████╗ ███████╗
████╗ ████║██║   ██║╚██╗██╔╝  ██╔════╝██╔═══██╗██╔══██╗██╔════╝
██╔████╔██║██║   ██║ ╚███╔╝   ██║     ██║   ██║██║  ██║█████╗
██║╚██╔╝██║██║   ██║ ██╔██╗   ██║     ██║   ██║██║  ██║██╔══╝
██║ ╚═╝ ██║╚██████╔╝██╔╝ ██╗  ╚██████╗╚██████╔╝██████╔╝███████╗
╚═╝     ╚═╝ ╚═════╝ ╚═╝  ╚═╝   ╚═════╝ ╚═════╝ ╚═════╝ ╚══════╝
```

A multi-agent coding environment built on tmux — where you stay in the loop.

![MuxCode demo](assets/demo.gif)

## What is MuxCode?

MuxCode is a tmux-native multi-agent development environment. Ten specialist AI agents — planner, editor, builder, tester, dev server, reviewer, deployer, runner, log watcher, and git manager — each run in their own tmux window, coordinated through a file-based message bus. You work in neovim alongside an editing agent, and every other part of the development lifecycle has its own dedicated agent a function key away.

You stay in control. The edit agent is your primary interface — it helps you write code and delegates to specialists when you're ready. Ask for a build, and the build agent runs it. Tests fire automatically on success. Review follows tests. Results flow back while you keep editing. The chain routing is hook-driven — Go hooks checking exit codes, not LLM routing decisions — so dispatch is deterministic and fast.

MuxCode supports multiple AI CLI providers. Each agent window can independently use [Claude Code](https://claude.ai/code), [OpenCode](https://opencode.ai/), [Codex CLI](https://github.com/openai/codex), or a local LLM via [Ollama](https://ollama.com/) — a single session can mix providers. Claude Code provides full hook support for deterministic chains. OpenCode brings multi-provider LLM access (Anthropic, OpenAI, Google, Groq, Bedrock) via its TUI. Codex CLI runs in full-auto mode with its own agent definitions. Ollama reduces API costs for structured-command roles. Provider assignment is per-agent at launch time via environment variables.

The coordination layer is entirely local. Agents communicate through JSONL files in `/tmp/`. Memory persists across sessions as markdown files. The bus binary is stdlib-only Go — no external dependencies, no containers, no databases. If Ollama goes down, affected agents fall back to Claude Code automatically, and the bus daemon handles restart and recovery.

Each agent has scoped tool permissions — the build agent can't edit files, the commit agent can't deploy infrastructure, the edit agent can't run builds or git commands. This separation prevents agents from stepping on each other and keeps the human in the loop for every code change.

```
┌──────────────────────────────────────────────────────────────────┐
│  F1 Plan  F2 Edit  F3 Build  F4 Test  F5 Serve  F6 Review        │
│  F7 Deploy  F8 Run  F9 Watch  F10 Commit                         │
├──────────────────────────────────────────────────────────────────┤
│                                                                  │
│  ┌──────────────┐    ┌──────────────┐    ┌──────────────┐        │
│  │ edit         │    │ build        │    │ test         │        │
│  │ nvim | agent │──→ │ term | agent │──→ │ term | agent │        │
│  └──────────────┘    └──────────────┘    └──────────────┘        │
│         │                                       │                │
│         │            ┌──────────────┐           │                │
│         └───────────→│ review       │←──────────┘                │
│                      │ term | agent │                            │
│                      └──────────────┘                            │
│                                                                  │
│  Message Bus: /tmp/muxcode-bus-{session}/                        │
│  Memory:      .muxcode/memory/                                   │
└──────────────────────────────────────────────────────────────────┘
```

## How it works

You launch `muxcode`, pick a project (or pass a path directly), and a tmux session spins up with ten windows — one per agent. The plan window opens neovim on the left (showing the last-edited doc) and the plan agent on the right. The edit window opens neovim and the edit agent. Every other window has a terminal pane alongside its specialist agent.

A typical workflow looks like this:

1. **You edit code** in neovim, talking to the edit agent when you need help.
2. **You ask for a build.** The edit agent sends a request to the build agent, which runs your build command and reports back.
3. **Tests fire automatically.** A bash hook detects the successful build and triggers the test agent — no LLM decision-making, just an exit code check.
4. **Review follows tests.** Same pattern — if tests pass, the review agent picks up the diff and flags anything worth discussing.
5. **You iterate.** Results flow back to the edit agent. If the reviewer finds issues, you fix them and kick off another cycle.
6. **You commit when ready.** The commit agent handles staging, committing, and pushing. A pre-commit safeguard blocks commits if other agents still have pending work.

The entire build-test-review chain is **hook-driven** — Go hooks check exit codes and fire the next step. No tokens are spent on routing decisions, and the chain runs at the speed of your tools, not your LLM. Chain actions support conditional expressions — route builds to deploy on release branches, to test on feature branches — all config-driven with first-match-wins semantics.

For multi-step work beyond a linear chain, **graph orchestration** moves the routing into the daemon: `muxcode graph run spec-to-pr "implement X"` executes a declarative DAG — implement, build/test with a capped fix loop, review, then a human approval gate before any commit — waking the edit agent only at gates and completion instead of once per step. Runs persist to disk and resume across daemon restarts.

The `muxcode tui` command launches a live dashboard showing which agents are busy, idle, or waiting on messages, so you always know what's happening across the session. You can add a dashboard window by including `status` in your `MUXCODE_WINDOWS` list.

## Agents

MuxCode ships with ten specialist agents, each in its own tmux window:

| Window (F-key) | Agent          | What it does                                                                            |
| -------------- | -------------- | --------------------------------------------------------------------------------------- |
| plan (F1)      | Planner        | Maintains requirements specs, architecture docs, and planning artifacts                 |
| edit (F2)      | Code editor    | Your primary interface — orchestrates by delegating, never runs build/test/git directly |
| build (F3)     | Code builder   | Compiles and packages your project                                                      |
| test (F4)      | Test runner    | Runs your test suite and reports results                                                |
| serve (F5)     | Dev server     | Starts, monitors, and auto-restarts local development servers                           |
| review (F6)    | Code reviewer  | Reviews diffs for bugs, style issues, and improvements                                  |
| deploy (F7)    | Infra deployer | Runs infrastructure deployments and diffs                                               |
| run (F8)       | Command runner | Executes ad-hoc commands                                                                |
| watch (F9)     | Log watcher    | Monitors logs — local files, CloudWatch, Kubernetes, Docker                             |
| commit (F10)   | Git manager    | Handles all git operations — commits, branches, rebases, pushes                         |

**Agent mode** — Press F2 when already on the edit window to cycle to the autonomous agent, which reads Jira stories and drives the full story lifecycle (requirements → PR → implementation → PR) without user intervention. Press F2 again to cycle back to the editor. Both sessions persist across cycles. See [Agents](docs/agents.md#autonomous-agent-agent) for details.

Additional roles that share a host agent's window or are available as opt-in:

| Role     | Host / access | What it does                                                                                |
| -------- | ------------- | ------------------------------------------------------------------------------------------- |
| analyze  | Opt-in window | Watches file changes and provides codebase analysis — add `analyze` to `MUXCODE_WINDOWS`   |
| api      | Modal popup   | API tester — opens via `prefix + i` or `muxcode modal open api`. Manages collections, executes requests, tracks history |
| docs     | plan          | Documentation writer — handled by the plan agent                                            |
| research | edit          | Web search and codebase exploration — handled by the edit agent                             |
| pr-read  | commit        | PR review analysis — handled by the commit agent                                            |
| status   | —             | Live TUI dashboard (`muxcode tui`) — add `status` to `MUXCODE_WINDOWS` to include |

### Default model assignments

Out of the box, MuxCode uses Claude Code for orchestration roles and OpenCode for command-execution roles:

| Role                                             | Default CLI | Default model                   |
| ------------------------------------------------ | ----------- | ------------------------------- |
| edit                                             | Claude Code | `claude-fable-5-1`             |
| plan                                             | Claude Code | `claude-opus-5`               |
| review, analyze                                  | OpenCode    | `opencode-go/qwen3.7-plus`     |
| build, test, serve, deploy, run, watch, commit   | OpenCode    | `opencode-go/minimax-m3`       |
| api                                              | Claude Code | `claude-sonnet-5`              |

OpenCode Go models are available through [OpenCode Go](https://opencode.ai/go). Override the model per role with `MUXCODE_{ROLE}_MODEL` for OpenCode roles (e.g. `MUXCODE_BUILD_MODEL=opencode-go/minimax-m3`) or `MUXCODE_{ROLE}_CLAUDE_MODEL` for Claude Code roles. Override the CLI provider per role with `MUXCODE_{ROLE}_CLI`.

### Recommended multi-provider configuration

The defaults above already provide a cost-effective split — Claude Code for orchestration (edit, plan) and OpenCode with role-specific models for everything else. To further customize, add Codex CLI for reasoning-heavy roles:

```bash
# ~/.config/muxcode/config or .muxcode/config

# Reasoning-heavy roles — Codex CLI with gpt-5.5
MUXCODE_REVIEW_CLI=codex
MUXCODE_ANALYZE_CLI=codex

# Cost optimization — use Haiku for API role
MUXCODE_API_CLAUDE_MODEL=claude-haiku-4-5
```

This gives you:

| Role     | Provider    | Model              | Why                                                      |
| -------- | ----------- | ------------------ | -------------------------------------------------------- |
| edit     | Claude Code | Opus 5             | Full hook support, orchestration, code editing            |
| commit   | OpenCode    | MiniMax M3         | Git operations — prompt-instructed chains                |
| build    | OpenCode    | MiniMax M3         | Runs `./build.sh` — structured commands, no hooks needed |
| test     | OpenCode    | MiniMax M3         | Runs `./test.sh` — structured commands, no hooks needed  |
| serve    | OpenCode    | MiniMax M3         | Dev server lifecycle — start, monitor, restart            |
| deploy   | OpenCode    | MiniMax M3         | Runs CDK/terraform — command execution role              |
| run      | OpenCode    | MiniMax M3         | Ad-hoc commands — capable free model                     |
| watch    | OpenCode    | MiniMax M3         | Log tailing — lightweight, read-only                     |
| review   | Codex CLI   | gpt-5.5      | Deep code reasoning, thorough diff analysis              |
| analyze  | Codex CLI   | gpt-5.5      | Codebase-wide analysis, pattern detection                |

**Provider tradeoffs:**

| Provider   | Hooks | Chains           | Idle detection | Sandbox | Best for                           |
| ---------- | ----- | ---------------- | -------------- | ------- | ---------------------------------- |
| Claude Code | Yes   | Deterministic    | Yes            | No      | Edit, commit — need hooks + control |
| OpenCode   | No    | Prompt-instructed | Limited        | No      | Build, test, deploy — command execution |
| Codex CLI  | No    | Prompt-instructed | Heuristic      | Yes     | Review, analyze — read-only reasoning  |
| Local LLM  | No    | Prompt-instructed | No             | No      | Cost-sensitive structured commands  |

**Codex CLI sandbox restriction:** Codex CLI sandboxes all filesystem writes and blocks outbound network access. This makes it unsuitable for roles that write to `.git` (commit) or push to remotes. Use Claude Code or OpenCode for git operations. Codex is best for read-only roles like review and analyze.

Any role can use any provider. Set `MUXCODE_{ROLE}_CLI` to `claude`, `opencode`, `codex`, or `local`:

- `MUXCODE_{ROLE}_CLI=claude` — Claude Code (full hook support, deterministic chains)
- `MUXCODE_{ROLE}_CLI=opencode` — OpenCode TUI (multi-provider LLM access, autonomous context management)
- `MUXCODE_{ROLE}_CLI=codex` — Codex CLI (OpenAI models, sandboxed — read-only roles only)
- `MUXCODE_{ROLE}_CLI=local` — Local LLM via Ollama (free, structured commands)

Per-role model selection is also supported via `MUXCODE_{ROLE}_CLAUDE_MODEL` (Claude Code), `MUXCODE_{ROLE}_CODEX_MODEL` (Codex CLI, default `gpt-5.5`), or `MUXCODE_{ROLE}_MODEL` (OpenCode/local, falls back to `MUXCODE_OLLAMA_MODEL`, default `qwen3:4b`). If Ollama is unreachable at launch, affected agents fall back to Claude Code automatically.

Each agent has constrained tool permissions — the build agent can run builds but can't edit files, the commit agent can run git but can't deploy infrastructure. This separation prevents agents from stepping on each other. Tool profiles are automatically translated to each provider's permission format (Claude Code's `--allowedTools`, OpenCode's `permission` blocks, or the harness's `IsToolAllowed()`).

You can customize or replace any agent by dropping a markdown file in `.claude/agents/` (per-project) or `~/.config/muxcode/agents/` (global). See [Agents](docs/agents.md) for details.

### Choosing a model per role

When assigning models, balance two axes — **capability** (reasoning/code quality) and **throughput/cost** (request quota) — against what each role actually does. The decision principle: **use Claude where hooks and reasoning compound (edit, deploy, high-stakes review); offload high-volume, low-reasoning roles to OpenCode's cheap, high-quota models.** Match the model's quota to how often the role fires — chain-driven roles (build, test, watch) burn requests fast, so they belong on the highest-quota models, not the flagships.

Claude vs OpenCode, role by role:

| Role          | Recommended            | Why                                                                                   |
| ------------- | ---------------------- | ------------------------------------------------------------------------------------- |
| edit          | Claude `sonnet`/`opus` | Tool-use + delegation reasoning, and the edit-agent guard is hook-enforced            |
| review        | Claude `opus` or OpenCode `deepseek-v4-pro` | Correctness matters most; DeepSeek V4 Pro closes most of the gap at lower cost |
| plan / docs   | Claude `sonnet` or OpenCode `deepseek-v4-pro` | Spec writing benefits from structure; low frequency tolerates lower quota |
| research      | OpenCode `deepseek-v4-pro` or `kimi-k2.6` | Web/doc digestion — no hook need; long-context helps                          |
| build / test  | OpenCode `minimax-m3` / `mimo-v2.5` | Single-shot, exit-code driven — spend the cheapest high-quota tokens            |
| serve         | OpenCode `minimax-m3`  | Dev-server lifecycle — capable enough to parse logs, not in any chain                  |
| deploy / run  | OpenCode `minimax-m3` or Claude | Deploy benefits from Claude's git/push reliability + hooks; run is command exec   |
| watch         | OpenCode `mimo-v2.5`   | Log tailing — cheapest/highest-quota; output is PII-scrubbed before the model         |
| commit        | OpenCode `minimax-m3` or Claude | Git operations — either works; Claude if you want hook-driven chain hygiene       |

**Rough quality ranking:** `opus` ≳ `sonnet` ≈ `deepseek-v4-pro` > `minimax-m3` ≈ `qwen3.x-plus` > `haiku` ≈ `mimo-v2.5`. Claude's top end edges out OpenCode's, but DeepSeek V4 Pro is competitive at the workhorse tier.

**The real tradeoff is hooks vs. cost, not raw capability.** Claude Code gives native deterministic chains (build→test→review by exit code) and hook-enforced guards; OpenCode gives cheaper high-volume throughput via prompt-instructed chains, at the price of graceful-degradation fragility (mitigated by the daemon's stuck-provider auto-reload watchdog). For never-exiting processes (serve's `pnpm dev`, watch's `tail -f`), the dominant reliability factor is the agent staying deliverable (run them as background `muxcode proc`), which is independent of model choice — so pick those roles purely on cost.

## Key features

### Multi-CLI provider support
- **Provider interface** — Abstraction layer (`bus/provider.go`) encapsulating all CLI-specific behavior: launch, idle detection, notifications, lifecycle management, and agent configuration
- **Claude Code provider** — Full integration: hook-driven chains, idle prompt detection, startup acceptance, `/compact` injection, `--allowedTools` permissions
- **OpenCode provider** — TUI mode integration: bare `opencode` binary launch, box-drawing frame detection, display-message notifications, auto-compact, permission block generation. Multi-provider LLM access (Anthropic, OpenAI, Google, Groq, Bedrock)
- **Codex CLI provider** — Automatic approval mode: `codex -a never --no-alt-screen` launch, `.codex/AGENTS.md` generation, send-keys wake-up with message payload injection, heuristic task completion detection
- **Per-agent provider assignment** — Each tmux window independently resolves its CLI via `MUXCODE_{ROLE}_CLI`. A single session can mix Claude Code, OpenCode, Codex CLI, and local LLM agents
- **Graceful degradation** — Non-hook providers (OpenCode, Codex CLI, local LLM) degrade gracefully: chains disabled (system prompt instructs bus messaging instead), idle detection skipped, tool profiles translated to provider-native permission format
- **Hot reload** — Switch any agent's CLI provider or model at runtime without restarting the session. `muxcode reload <role> --cli opencode --model opencode-go/mimo-v2.5-pro` gracefully stops the agent, writes session-scoped runtime overrides, and relaunches. Provider selector modal (`Prefix + R`) provides a visual TUI for browsing installed providers and models. Persistent config via `muxcode config set/get/list` with full resolution chain: runtime override → per-role env → global env → config file → default
- **Interactive installer** — `install.sh` detects and offers to install Claude Code, OpenCode, and Codex CLI, with default provider selection when multiple are available

### Agent orchestration
- **10 specialist agents** — Plan, edit, build, test, serve, review, deploy, run, watch, and commit — each with scoped tool permissions and its own tmux window
- **Autonomous agent mode** — An autonomous agent shares the F2 window with the editor. Press F2 to cycle between them. The agent reads Jira stories, creates requirements docs, opens review PRs, implements approved requirements, and submits completed PRs — all without user intervention. Configurable via natural-language task files (`.muxcode/agent-tasks.md`) and a customizable story-lifecycle skill
- **Hook-driven automation chains** — Build→test→review and deploy→run→watch chains fire via bash exit codes. Deterministic, fast, zero token cost for routing
- **Conditional chain actions** — Chain actions support 11 condition types (`files_match`, `branch_match`, `command_match`, `env_set`, `output_contains`, `spec_phases_remaining`, etc.) with first-match-wins on action arrays. Route builds to deploy on release branches, to test on feature branches — all config-driven
- **Event subscriptions** — Fan-out after chain execution. Subscribe any agent to build/test/deploy/run/watch events with outcome and condition filtering
- **Graph orchestration** — Declarative multi-agent DAGs executed by the daemon: fan-out/fan-in with `all`/`any`/`quorum` join barriers, outcome-keyed branching, capped fix loops, human approval gates, and durable per-run state that survives a daemon restart. `muxcode graph run spec-to-pr "implement X"` returns immediately; the orchestrator is interrupted only at human gates and completion instead of once per step. Git mutations and Atlassian writes are rejected at `graph validate` unless downstream of a `wait_human` gate. Ships 7 built-in templates (`spec-to-pr` — requires an active requirements spec, `story-to-spec`, `commit-pr-review-loop`, `pr-local-review`, `update-spec-docs`, `deploy-verify`, `build-test-review`) with project/user overrides, plus `graph status|cancel|retry --from|approve` for run control
- **Spawned agents** — Create temporary agents for one-off tasks in their own tmux window. Results collected automatically on completion
- **Modal windows** — On-demand overlay windows for specialized tasks. API testing opens via `prefix + i` or `muxcode modal open api`. Declarative config with size presets and optional pane splits
- **Pre-commit safeguards** — Commit delegation blocked when other agents have pending work, preventing incomplete commits
- **Workflow state machine** — Persistent state tracking through the development lifecycle (idle → editing → building → testing → reviewing → committing → deploying → running → watching). 16 states with automatic regression on file edits, color-coded display. Query with `muxcode workflow`

### Local LLM & cost control
- **Ollama integration** — Any agent role can run via a local LLM instead of Claude Code. Per-role model selection via `MUXCODE_{ROLE}_MODEL`
- **Ollama health monitoring** — Watcher detects stuck inference (not just process liveness), auto-restarts Ollama and affected agents with 3-strike escalation
- **LLM harness** — Standalone binary with guardrails for smaller models: tool call filtering, loop prevention, structured task formatting, corrective feedback
- **Agent health monitoring** — Watcher probes agent tmux panes every 30s, auto-restarts dead agents after 3-strike escalation (log → alert → restart)

### Transactional messaging
- **Delivery tracking** — Every message gets a delivery status (sent → delivered → responded → expired). Track with `muxcode track <msg-id>`
- **Task tracking** — Delegated work is tracked with timeouts. The `--wait` flag on send commands creates a task entry and polls until a response arrives. List in-flight tasks with `muxcode tasks`
- **Message dedup** — Duplicate messages (same sender/target/action/type) within a 30-second window are atomically suppressed to prevent chain loops

### Memory & context
- **Persistent memory** — Two-layer system: project-level (`.muxcode/memory/`) and global (`~/.config/muxcode/memory/`). Daily rotation with 30-day archive retention
- **BM25 search** — Search memory across projects and sessions with IDF weighting, stemming, stop words, and quoted phrase matching
- **Drop-in context files** — Per-role context injection via `context.d/` directories. Auto-detects 17 project types (Go, Node.js, Python, Rust, CDK, etc.)
- **Skills and plugins** — Reusable instruction sets in markdown that auto-inject into agent prompts based on role
- **Session compaction** — Agents snapshot context to memory for long-running sessions. Watcher triggers compaction alerts when context approaches limits

### Developer experience
- **Managed Neovim config** — MuxCode ships its own neovim configuration via `NVIM_APPNAME=muxcode`, fully isolated from your personal `~/.config/nvim/`. Includes Dracula theme, treesitter, render-markdown.nvim, telescope, and vim-tmux-navigator out of the box. Extend with `~/.config/muxcode/nvim/lua/user/plugins.lua`
- **Inline diff preview** — Every Write/Edit tool call opens a scrollbound diff split in neovim before you accept or reject
- **Inline response delivery** — The `--wait` flag on send commands polls for responses inline — no manual inbox checking needed
- **Left-pane pollers** — Each window shows live history: build/test results with pass/fail stats, review findings, deploy status, git log, API history, analyze insights
- **Live TUI dashboard** — Dracula-themed dashboard showing agent status, message flow, and session health
- **Demo mode** — Scripted demo scenarios that replay bus message sequences with tmux window switching, suitable for screen recording and presentations. Run with `muxcode demo`
- **Session inspection** — Query any agent's status, message history, or busy state from the CLI

### Automation & integration
- **Webhook HTTP endpoint** — HTTP-to-bus bridge for CI/CD, GitHub webhooks, monitoring. Optional bearer token auth
- **Cron scheduling** — Recurring tasks on intervals (`@every 5m`, `@hourly`, `@daily`), managed by the bus daemon
- **Background process tracking** — Launch long-running processes, track status, get notified on completion
- **Atlassian integration** — Jira issue descriptions, PR comments, and Confluence page updates via Atlassian REST API. Jira key extracted from branch name, Confluence pages identified by ID, URL, or space+title
- **Lifecycle logging** — Persistent JSONL logs recording the full startup-to-cleanup lifecycle. Survives session restarts, filterable by source/level/event/time

### Safety & guardrails
- **Scoped tool permissions** — Per-role tool profiles enforce what each agent can and can't do. Build can't edit files, commit can't deploy, edit can't run builds
- **Loop detection** — Bus detects agents stuck in repetitive patterns and escalates to the edit agent
- **Edit guard** — Sync hook blocks prohibited commands (build, test, git, deploy) in the edit window with delegation instructions
- **PII scrubbing** — Automatic redaction of emails, SSNs, credit cards, AWS keys, JWTs, and other secrets from tool output in PII-sensitive roles (api, runner, watch)
- **Daemon self-monitoring** — Keepalive heartbeat with companion monitor process (`watch --monitor`) — auto-restarts the daemon if it hangs

See the [Architecture](docs/architecture.md) and [Agent Bus](docs/agent-bus.md) docs for the full details.

## How MuxCode compares to autonomous AI coding tools

Tools like [OpenClaw](https://github.com/openclaw/openclaw), Devin, OpenHands, and SWE-agent push toward fully autonomous software engineering — give the AI a task, let it plan and execute end-to-end with minimal human input. MuxCode shares some of the same building blocks but takes a different approach to how humans and AI agents collaborate.

OpenClaw is a good point of comparison because it's also open-source and runs locally. It's a long-running Node.js service that acts as a message router between chat platforms (WhatsApp, Telegram, Discord) and an AI agent that can browse the web, read and write files, and run shell commands autonomously. It can manage Claude Code sessions, run tests, capture errors via Sentry, and open PRs on GitHub — all without you in the loop. MuxCode solves many of the same problems but keeps you in your terminal, in your editor, making the decisions.

### What they have in common

Both MuxCode and autonomous AI tools solve the same coordination problems:

- **Persistent memory** — Context that survives across sessions, searchable and shared between agents
- **Skills and plugins** — Reusable instruction sets that shape agent behavior for specific tasks or domains
- **Event-driven automation** — Chains of actions that trigger automatically based on outcomes
- **Session management** — Context compaction, history tracking, and long-running session support
- **Background process tracking** — Launching, monitoring, and reacting to long-running tasks
- **Loop detection** — Guardrails that catch agents stuck in repetitive failure patterns

### Where MuxCode differs

**Human-in-the-loop, not fully autonomous.** MuxCode keeps you as the orchestrator. The edit agent delegates on your behalf — you see every step, you decide what happens next. Autonomous tools aim to minimize human involvement, handling planning, execution, and error recovery on their own.

**Local-first, minimal infrastructure.** The message bus is JSONL files in `/tmp/`. The memory system is markdown files in your project directory. There's no database, no container runtime. An optional webhook HTTP endpoint bridges external tools (CI/CD, GitHub) to the bus, but it's a single lightweight server — not a required runtime. Autonomous tools typically require a runtime environment — SQLite, vector databases, sandboxed execution containers.

**Tmux-native, editor-centric.** You work in your actual editor alongside the agents. Press F3 to watch the build agent work. Press F9 to see the commit agent run git commands. There's no web UI, no chat interface separate from your terminal. Autonomous tools typically abstract the execution environment behind an API or web interface.

**Hook-driven orchestration, not LLM-driven.** The build-test-review and deploy-run-watch chains fire via exit codes in Go hooks — deterministic, fast, zero token cost for routing. Conditional chain actions add branch-aware, file-aware routing without leaving the config-driven model. Autonomous tools typically use the LLM itself to decide what to do next, which is more flexible but slower and more expensive.

**Composable specialists, not a monolithic agent.** Each agent is a focused role with constrained permissions. The build agent can't edit files. The commit agent can't deploy. The serve agent manages dev servers but can't modify source code. This separation of concerns mirrors how teams actually work. Autonomous tools often use a single agent with broad capabilities that handles everything.

**Zero external dependencies.** The bus binary is stdlib-only Go — it compiles in seconds with no dependency management. The launcher, agent bootstrap, and hooks are all Go subcommands of the single `muxcode` binary (with utility shell scripts for tmux/vim timing-sensitive operations). Autonomous tools typically have significant dependency trees (Python packages, Node modules, system libraries).

The tradeoff is clear: autonomous tools can handle more without you, but MuxCode gives you visibility and control at every step. If you want to understand what's happening in your codebase — not just get a result — MuxCode is designed for that workflow.

## Quick start

### Prerequisites

`install.sh` verifies each of these — including minimum versions — and offers to
install anything missing via your system package manager (Homebrew, apt, dnf,
pacman, or zypper). You do not need to install them by hand first.

| Tool | Minimum | Why |
| ---- | ------- | --- |
| tmux | **3.3** | Popups use `display-popup -b/-S/-T`, which older tmux rejects |
| Go | 1.22 | Builds the `muxcode` binary |
| git | 2.0 | Branch and changed-file detection, commit-msg hook install |
| make | — | Drives the build |
| jq | — | Hook and settings JSON merging |
| Neovim | 0.9 | Editor pane |
| Ollama | — | *Optional* — local LLM agents and the Prompt mode's `MUXCODE_PROMPT_BACKEND=ollama` opt-in (the default Prompt backend is the OpenCode gateway and needs `MUXCODE_OPENCODE_API_KEY` instead) |
| fzf | — | *Optional* — interactive project picker (`muxcode <path>` works without it) |

Plus at least one AI CLI provider, which the installer can also install for you:

- [Claude Code](https://claude.ai/code) (`claude`) — recommended, full hook support
- [OpenCode](https://opencode.ai/) (`opencode`) — alternative, multi-provider LLM access
- [Codex CLI](https://github.com/openai/codex) (`codex`) — alternative, OpenAI models

Optional extras, all detected and offered during install:
Mermaid CLI and draw.io (plan-agent diagram rendering).

### Install

```bash
git clone https://github.com/mkober/muxcode.git
cd muxcode
./install.sh
```

For a fully unattended install — every recommended default accepted and missing
prerequisites installed without prompting:

```bash
./install.sh --yes
```

| Flag | Effect |
| ---- | ------ |
| `-y`, `--yes` | Non-interactive; accept defaults and install prerequisites. The multi-GB Ollama model pull is still declined |
| `--no-deps` | Never install system prerequisites; report them and continue |
| `--no-color` | Disable colored output (also honors `NO_COLOR`) |
| `-h`, `--help` | Show usage |

The installer also switches to non-interactive mode automatically when stdin is
not a TTY, so piped and CI installs run to completion instead of stopping at the
first prompt.

It verifies prerequisites, builds the Go binary, and installs everything to
`~/.local/bin/` and `~/.config/muxcode/` — adding `~/.local/bin` to your shell
profile if it is missing from `PATH`. It detects and offers to install AI CLI
providers, sets up the managed neovim config (via `NVIM_APPNAME=muxcode`), tmux
integration, and Claude Code hooks (when Claude Code is selected), then runs a
smoke test to confirm the binary works. Your personal `~/.config/nvim/` is never
modified.

For subsequent builds after pulling updates:

```bash
./build.sh
```

`build.sh` also rolls the new binary out to any sessions already running. Long-lived session daemons
keep executing the code they launched with, so an install alone would not reach them — see
[Versions and releases](#versions-and-releases).

### Versions and releases

Check what you are running:

```bash
muxcode version          # muxcode v0.1.0 (a1b2c3d, 2026-09-02, go1.22.0 darwin/arm64)
muxcode version --json   # same facts as JSON
```

Releases are SemVer-tagged, and MuxCode is **`0.x` deliberately** — the compatibility contract is not
written yet, so MINOR bumps may carry breaking changes. Pushing a `v*` tag builds darwin and linux
binaries for amd64 and arm64, attaches `sha256sums.txt`, and publishes a GitHub release with notes
generated from PR labels.

A build from a working tree describes itself honestly rather than claiming a release —
`v0.1.0-3-gabc1234-dirty` — and a source build with no version stamp falls back to Go's embedded VCS
info, then to `devel`. It never prints an empty version.

Scripts can assert a minimum:

```bash
muxcode version --at-least v0.1.0
```

Exit `0` means at or past it, `1` means older, and `2` means **uncomparable** — an untagged dev build
has no version to rank. That third state is why the check is not a plain pass/fail: failing on it
would block the tree-built binary you run between tags, and passing on it would let a genuinely stale
binary through. The repo's integration tests branch on all three via
`scripts/lib/muxcode-version.sh`.

After upgrading, running sessions pick up the new binary with:

```bash
muxcode upgrade-daemons                      # every session on the machine
muxcode upgrade-daemons --session <name>     # just one
muxcode upgrade-daemons --dry-run            # show what would cycle
```

Daemons already on the installed build are skipped, so this is safe to re-run.

### Launch

```bash
# Interactive project picker (requires fzf)
muxcode

# Direct path
muxcode ~/Projects/my-app

# Custom session name
muxcode ~/Projects/my-app my-session
```

### Workspace trust

When Claude Code opens a new workspace for the first time, it shows a "Yes, I trust this folder" safety prompt in every agent window. MuxCode automatically accepts this prompt on your behalf — a background process polls each agent pane after launch and presses Enter on any window showing the trust dialog. This runs over ~18 seconds to catch slow-starting agents, and skips panes that are already past the prompt.

## Configuration

MuxCode uses a shell-sourceable config file. Resolution order:

1. `$MUXCODE_CONFIG` (explicit path)
2. `.muxcode/config` (project-local)
3. `~/.config/muxcode/config` (user global)
4. Built-in defaults

See [Configuration](docs/configuration.md) for the full variable reference.

### Runtime configuration

Switch providers or models at runtime without restarting your session:

```bash
# Reload a single agent with a different provider/model
muxcode reload review --cli codex --model gpt-5.5

# Reload with compaction (saves context before restart)
muxcode reload build --cli opencode --compact

# Reload all agents
muxcode reload --all

# Persistent config (survives session restarts)
muxcode config set MUXCODE_BUILD_CLI opencode
muxcode config get MUXCODE_BUILD_CLI
muxcode config list
```

Or press `Prefix + R` to open the visual provider selector — browse installed providers and models, pick one, and the agent reloads automatically.

## OpenCode agents

Any agent role can run via [OpenCode](https://opencode.ai/) instead of Claude Code. OpenCode is an open-source AI coding agent with multi-provider LLM support (Anthropic, OpenAI, Google, Groq, Bedrock) and a full TUI interface. This is useful when you want to use non-Anthropic models for specific roles or take advantage of OpenCode's autonomous context management.

### Setup

1. Install OpenCode:

   ```bash
   curl -fsSL https://opencode.ai/install | bash
   ```

2. Set per-role overrides in `.muxcode/config`:

   ```bash
   MUXCODE_BUILD_CLI=opencode     # build agent uses OpenCode
   MUXCODE_TEST_CLI=opencode      # test agent uses OpenCode
   ```

   Or set the session-wide default:

   ```bash
   MUXCODE_AGENT_CLI=opencode     # all agents use OpenCode
   MUXCODE_EDIT_CLI=claude        # except edit — keep Claude Code for hooks
   ```

3. Launch MuxCode normally — configured roles launch the OpenCode TUI, others use Claude Code.

### How it works

OpenCode agents run as interactive TUI sessions in their tmux panes. MuxCode generates per-role agent definitions in `.opencode/agents/<role>.md` with tool permissions translated from muxcode's tool profiles. The TUI manages its own context window, compaction, and tool execution autonomously.

Since OpenCode has no hook system, chain-driven features (build→test→review, deploy→run→watch) are replaced with config-driven prompt instructions generated by `buildChainInstruction()` — each agent only sees chain commands relevant to its role, including condition descriptions for conditional actions. Agent definition body text is adapted to replace hook chain references with manual commands, and the send policy is bypassed so non-hook agents can actually send chain messages. Wake-up notifications inject message content directly into the TUI input via send-keys. This same graceful degradation applies to all non-hook providers (OpenCode, Codex CLI, local LLM).

### Limitations

| Feature | Behavior with OpenCode |
|---------|----------------------|
| Build/test/review chains | Role-specific prompt instructions + adapted body text + send policy bypass (best-effort) instead of hook-driven (deterministic) |
| Edit guard | Disabled — relies on OpenCode's native `permission` blocks |
| Workflow state transitions | Skipped for non-hook agents |
| Idle detection | Not supported — agent always treated as "active" |
| Wake-up | Message content injected via send-keys into TUI input |
| Compact | No-op from muxcode — OpenCode auto-compacts at 95% context |

## Codex CLI agents

Any agent role can run via [Codex CLI](https://github.com/openai/codex) instead of Claude Code. Codex CLI is OpenAI's open-source coding agent that runs in your terminal with access to OpenAI models (GPT-4.1, o3, o4-mini, etc.).

### Setup

1. Install Codex CLI:

   ```bash
   npm install -g @openai/codex
   ```

2. Set per-role overrides in `.muxcode/config`:

   ```bash
   MUXCODE_ANALYZE_CLI=codex     # analyze agent uses Codex CLI
   ```

   Or set the session-wide default:

   ```bash
   MUXCODE_AGENT_CLI=codex       # all agents use Codex CLI
   MUXCODE_EDIT_CLI=claude       # except edit — keep Claude Code for hooks
   ```

3. Launch MuxCode normally — configured roles launch `codex -a never --no-alt-screen`, others use their default provider.

### How it works

Codex CLI agents run with automatic approval (`-a never`) without the alternate screen buffer (`--no-alt-screen`) so tmux pane capture works for idle detection and task completion. MuxCode generates a shared agent definition at `.codex/AGENTS.md` with tool permissions and bus instructions. Wake-up notifications inject message content directly via send-keys.

Since Codex CLI has no hook system, the same three-layer graceful degradation used by OpenCode applies: role-specific prompt instructions, agent body adaptation, and send policy bypass.

### Limitations

| Feature | Behavior with Codex CLI |
|---------|------------------------|
| Sandbox | All filesystem writes sandboxed, outbound network blocked — **do not use for commit, deploy, or any role that writes to `.git` or pushes to remotes** |
| Build/test/review chains | Role-specific prompt instructions + send policy bypass (best-effort) instead of hook-driven (deterministic) |
| Edit guard | Disabled — relies on Codex's native sandbox |
| Workflow state transitions | Skipped for non-hook agents |
| Idle detection | Heuristic — detects `>` prompt or "Summarize" text |
| Wake-up | Message content injected via send-keys with payload |
| Compact | No-op from muxcode |
| Agent config | Shared `.codex/AGENTS.md` file (not per-role) |

## Local LLM agents

Any agent role can run via a local LLM (Ollama) instead of Claude Code. This is useful for roles that primarily execute structured commands — git operations, builds, log monitoring — where a small model is sufficient and API costs add up.

### Setup

1. Install and start Ollama:

   ```bash
   brew install ollama
   ollama serve
   ollama pull qwen3:4b
   ```

2. Set per-role overrides in `.muxcode/config`:

   ```bash
   MUXCODE_COMMIT_CLI=local    # commit agent uses local LLM
   MUXCODE_BUILD_CLI=local     # build agent uses local LLM
   ```

3. Launch MuxCode normally — configured roles run via Ollama, others use Claude Code. If Ollama is unreachable at launch, the agent falls back to Claude Code automatically.

### Configuration

| Variable               | Default                  | Description                                                 |
| ---------------------- | ------------------------ | ----------------------------------------------------------- |
| `MUXCODE_{ROLE}_CLI`   | (unset)                  | Set to `local` to use Ollama (e.g. `MUXCODE_COMMIT_CLI=local`) |
| `MUXCODE_OLLAMA_MODEL` | `qwen3:4b`               | Ollama model name                                           |
| `MUXCODE_OLLAMA_URL`   | `http://localhost:11434` | Ollama server URL                                           |

### Recommended models

| Model                   | Size   | Best for                              | Notes                                                          |
| ----------------------- | ------ | ------------------------------------- | -------------------------------------------------------------- |
| `qwen3:4b`              | 2.5 GB | All local roles (single resident model) | Default — smallest credible tool-caller; one set of weights in memory regardless of how many local agents run |
| `qwen3:8b`              | 5.2 GB | Escalation when 4B quality disappoints | Same family, so a swap changes capability without changing conventions |
| `deepseek-coder-v2:16b` | 8.9 GB | Review, analysis, complex code tasks  | Strong at code review and multi-file reasoning                 |
| `llama3.1:8b`           | 4.7 GB | General-purpose agent tasks           | Good all-rounder when code specialization isn't critical       |

The default is deliberately a single small model shared by every local role — Ollama keeps each distinct model resident while in use, so per-role model pins multiply memory cost. Override globally via `MUXCODE_OLLAMA_MODEL`; per-role pins (`MUXCODE_{ROLE}_MODEL`) exist but reintroduce a second resident model.

### Health monitoring

The bus daemon monitors Ollama inference health (not just process liveness) and auto-restarts both Ollama and affected agents when inference hangs. Up to 3 automatic restarts per session, then periodic alerts for manual intervention.

### LLM harness

For smaller models that struggle with structured tool calling, the standalone `muxcode-llm-harness` binary provides guardrails: tool call filtering, loop prevention, structured task formatting, corrective feedback, and single-shot auto-complete for build/test roles. The harness includes a Dracula-themed TUI with a live activity log showing Ollama calls, tool executions with output previews, and a status bar. The launcher prefers the harness when available.

Harness-specific agent definitions in `agents/harness/` provide simplified instructions tailored for local LLMs — shorter, more directive prompts that avoid confusing smaller models with bus messaging or multi-step discovery sequences.

See [Agents](docs/agents.md#local-llm-agent-ollama) for the full reference.

## Skills

Skills are reusable instruction sets defined as markdown files with YAML frontmatter. They auto-inject into agent prompts based on role, giving agents domain-specific knowledge without editing agent definitions.

### Built-in skills

| Skill                     | Roles        | Description                                                  |
| ------------------------- | ------------ | ------------------------------------------------------------ |
| `git-commit-conventions`  | commit, edit | Commit message format and git workflow conventions           |
| `go-testing`              | test, build  | Go testing patterns and conventions                          |
| `code-review-checklist`   | review       | Code review quality checklist                                |
| `github-pr-comment`       | commit       | Post threaded replies to Copilot review comments on PRs and summary comments addressing all feedback |
| `jira-pr-comment`         | commit       | Post PR details as a comment on the corresponding Jira issue |
| `jira-manage-issues` | commit, edit | Full Jira issue lifecycle — read, update, search (JQL), transition status, link dependencies, read/post comments, create subtasks |
| `confluence-update-page`  | commit, edit | Read and update Confluence pages with ADF content            |
| `docs-management`         | plan, edit   | Documentation lifecycle — move specs, update status, check off phases, cross-reference verification |
| `agent-debug`             | edit         | Diagnostic procedures for inspecting agent state, checking idle/active status, and troubleshooting stuck agents |

### Resolution order

1. `.muxcode/skills/` — project-local (highest priority, shadows by name)
2. `~/.config/muxcode/skills/` — user global
3. `skills/` — installed defaults

### Creating skills

```bash
muxcode skill create my-skill "Description" --roles build,test --tags ci,workflow "Skill body here"
```

Or drop a markdown file in `.muxcode/skills/`:

```markdown
---
name: my-skill
description: What the skill does
roles: [build, test]
tags: [ci, workflow]
---

Instructions for the agent...
```

### Atlassian integration

Three skills integrate with Atlassian Cloud via the REST API:

- **`jira-pr-comment`** — The git-manager agent posts a comment on the Jira issue when a PR is created, including the PR link with diff stats. Extracts the Jira key from the branch name (e.g. `DATA-456-add-validation` → `DATA-456`).
- **`jira-manage-issues`** — Full Jira issue lifecycle: read (with links/subtasks), update descriptions, search via JQL, transition status, link dependencies, read/post comments, create subtasks. Available to the commit and edit roles.
- **`confluence-update-page`** — Reads and updates Confluence pages using ADF. Pages identified by page ID, Confluence URL, or space key + title. Supports full replacement, append mode, and CQL search. Available to the git and edit roles.

Add to `.muxcode/config` or `~/.config/muxcode/config`:

```bash
JIRA_BASE_URL="https://your-org.atlassian.net"
JIRA_USER_EMAIL="you@example.com"
JIRA_API_TOKEN="your-atlassian-api-token"

# Optional: override base URL for Confluence (defaults to JIRA_BASE_URL)
# CONFLUENCE_BASE_URL="https://your-org.atlassian.net"
```

If the env vars are missing, all Atlassian skills skip silently. Jira skills also skip if the branch name doesn't start with a Jira key.

## Tmux tips

Useful keybindings for navigating your MuxCode session:

| Keybinding | Action |
| --- | --- |
| `F1`–`F10` | Switch between agent windows (plan, edit, build, test, serve, review, deploy, run, watch, commit) |
| `F2` (on edit window) | Cycle agent mode (edit → agent → edit) |
| `Prefix + a` | Cycle edit-window agents from any window |
| `Prefix + R` | Open provider selector (hot reload) |
| `Prefix + i` | Open API testing modal |
| `Prefix + b` | Open MuxCode quick menu |

Every agent window also carries the **control pane** — a fixed full-width strip at the bottom hosting the graph TUI (prompt, launcher, runs, pending gates; `Tab` cycles, and the selected surface stays consistent across all windows). It is on by default; `MUXCODE_CONTROL_PANE_EXCLUDE` names windows to opt out, `MUXCODE_CONTROL_PANE_DISABLE=1` turns it off wholesale, `MUXCODE_CONTROL_PANE_HEIGHT` sets its rows (default 18), and `MUXCODE_CONTROL_PANE_SURFACE` picks the starting surface (`runs`/`gates`/`prompt`/`launcher`). A waiting `wait_human` gate switches every pane to Pending Gates by itself; the daemon respawns a killed pane and recycles panes onto a freshly installed binary. The graph popups and menu entries were retired with the pane's arrival — `muxcode graph ui` remains for ad-hoc use.

| `Prefix + C` | New MuxCode session (project picker) |
| `Prefix + z` | Zoom current pane to full screen |
| `Prefix + [` | Enter scroll/copy mode |
| `Prefix + d` | Detach from session |
| `Ctrl + h/j/k/l` | Switch panes (vim-style, works across nvim and tmux) |
| `Prefix + c` | New tmux window |
| `Prefix + n` / `Prefix + p` | Next / previous window |
| `Prefix + w` | Window list |
| `Prefix + s` | Session list |
| `Prefix + x` | Close current pane |
| `Prefix + %` | Split pane horizontally |
| `Prefix + "` | Split pane vertically |
| `Prefix + :` | Tmux command prompt |
| `Prefix + ?` | List all keybindings |

## Documentation

- [Architecture](docs/architecture.md) — System design, data flow, and hook chains
- [Agent Bus](docs/agent-bus.md) — CLI reference for `muxcode`
- [Agents](docs/agents.md) — Role descriptions and customization
- [Hooks](docs/hooks.md) — Hook system details
- [Configuration](docs/configuration.md) — Config file and environment variable reference

## License

[MIT](LICENSE)
