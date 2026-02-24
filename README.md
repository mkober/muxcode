# MUXcode

```
███╗   ███╗██╗   ██╗██╗  ██╗   ██████╗ ██████╗ ██████╗ ███████╗
████╗ ████║██║   ██║╚██╗██╔╝  ██╔════╝██╔═══██╗██╔══██╗██╔════╝
██╔████╔██║██║   ██║ ╚███╔╝   ██║     ██║   ██║██║  ██║█████╗
██║╚██╔╝██║██║   ██║ ██╔██╗   ██║     ██║   ██║██║  ██║██╔══╝
██║ ╚═╝ ██║╚██████╔╝██╔╝ ██╗  ╚██████╗╚██████╔╝██████╔╝███████╗
╚═╝     ╚═╝ ╚═════╝ ╚═╝  ╚═╝   ╚═════╝ ╚═════╝ ╚═════╝ ╚══════╝
```

A multi-agent coding environment built on tmux — where you stay in the loop.

![MUXcode demo](assets/demo.gif)

## What is MUXcode?

MUXcode is a tmux session. Everything — your editor, the AI agents, the message bus, the dashboard — lives inside tmux windows. You launch it, and a session spins up with nine windows, each bound to a function key. F1 is your edit window. F2 is build. F3 is test. You get the idea.

You work in neovim with an AI editing agent alongside you in the edit window. When you need a build, tests, a code review, or a commit, you tell the edit agent and it delegates to a specialist in another window. Each step of the workflow has its own agent, and they work in parallel while you keep editing. Press a function key to watch any agent work, or stay in your editor and let results flow back. Nothing happens unless you ask for it.

The edit window is where you spend most of your time — neovim on the left, the edit agent on the right. Unlike the other agents, it doesn't run builds or tests or git commands directly. It helps you write code and dispatches work to the right specialist when you're ready. More copilot than autonomous assistant.

Everything runs locally inside that tmux session. The agents coordinate through plain text files in `/tmp/` — no servers, no databases, no containers. The only external call is to the LLM powering each agent.

```
┌─────────────────────────────────────────────────────────────┐
│  F1 edit  F2 build  F3 test  F4 review  F5 deploy  F6 run  │
│  F7 commit  F8 analyze  F9 status                           │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  ┌──────────────┐    ┌──────────────┐    ┌──────────────┐   │
│  │ edit         │    │ build        │    │ test         │   │
│  │ nvim | agent │──→ │ term | agent │──→ │ term | agent │   │
│  └──────────────┘    └──────────────┘    └──────────────┘   │
│         │                                       │           │
│         │            ┌──────────────┐           │           │
│         └───────────→│ review       │←──────────┘           │
│                      │ term | agent │                       │
│                      └──────────────┘                       │
│                                                             │
│  Message Bus: /tmp/muxcode-bus-{session}/                   │
│  Memory:      .muxcode/memory/                              │
└─────────────────────────────────────────────────────────────┘
```

## How it works

You launch `muxcode`, pick a project (or pass a path directly), and a tmux session spins up with nine windows — one per agent. The edit window opens neovim on the left and the edit agent on the right. Every other window has a terminal pane alongside its specialist agent.

A typical workflow looks like this:

1. **You edit code** in neovim, talking to the edit agent when you need help.
2. **You ask for a build.** The edit agent sends a request to the build agent, which runs your build command and reports back.
3. **Tests fire automatically.** A bash hook detects the successful build and triggers the test agent — no LLM decision-making, just an exit code check.
4. **Review follows tests.** Same pattern — if tests pass, the review agent picks up the diff and flags anything worth discussing.
5. **You iterate.** Results flow back to the edit agent. If the reviewer finds issues, you fix them and kick off another cycle.
6. **You commit when ready.** The commit agent handles staging, committing, and pushing. A pre-commit safeguard blocks commits if other agents still have pending work.

The entire build-test-review chain is **hook-driven** — bash scripts check exit codes and fire the next step. No tokens are spent on routing decisions, and the chain runs at the speed of your tools, not your LLM.

A live dashboard (F9) shows which agents are busy, idle, or waiting on messages, so you always know what's happening across the session.

## Agents

MUXcode ships with these default agents:

| Window | Agent | What it does |
|--------|-------|-------------|
| edit | Code editor | Your primary interface — orchestrates by delegating, never runs build/test/git directly |
| build | Code builder | Compiles and packages your project |
| test | Test runner | Runs your test suite and reports results |
| review | Code reviewer | Reviews diffs for bugs, style issues, and improvements |
| deploy | Infra deployer | Runs infrastructure deployments and diffs |
| run | Command runner | Executes ad-hoc commands |
| commit | Git manager | Handles all git operations — commits, branches, rebases, pushes |
| analyze | Editor analyst | Watches file changes and provides codebase analysis |
| status | Dashboard | Live TUI showing agent status (not an AI agent) |

Each agent has constrained tool permissions — the build agent can run builds but can't edit files, the commit agent can run git but can't deploy infrastructure. This separation prevents agents from stepping on each other.

You can customize or replace any agent by dropping a markdown file in `.claude/agents/` (per-project) or `~/.config/muxcode/agents/` (global). See [Agents](docs/agents.md) for details.

## Key features

- **Skills and plugins** — Reusable instruction sets that auto-inject into agent prompts based on role. Create project-specific or global skills in markdown.
- **Persistent memory with search** — Agents read and write to shared memory. Context survives across sessions and is searchable.
- **Event-driven automation chains** — Build-test-review and deploy-verify chains fire automatically via hook exit codes.
- **Cron scheduling** — Run recurring tasks on intervals (`@every 5m`, `@hourly`, `@daily`). Managed by the bus watcher.
- **Background process tracking** — Launch long-running processes, track their status, and get notified on completion.
- **Loop detection guardrails** — The bus detects when agents get stuck in repetitive patterns and escalates to the edit agent.
- **Session compaction** — Agents can snapshot their context to memory, enabling long-running sessions without losing history.
- **Session inspection** — Query any agent's status, message history, or busy state programmatically from the CLI.
- **Pre-commit safeguards** — Commit delegation is blocked when other agents have pending work, preventing incomplete commits.

See the [Architecture](docs/architecture.md) and [Agent Bus](docs/agent-bus.md) docs for the full details.

## How MUXcode compares to autonomous AI coding tools

Tools like [OpenClaw](https://github.com/openclaw/openclaw), Devin, OpenHands, and SWE-agent push toward fully autonomous software engineering — give the AI a task, let it plan and execute end-to-end with minimal human input. MUXcode shares some of the same building blocks but takes a different approach to how humans and AI agents collaborate.

OpenClaw is a good point of comparison because it's also open-source and runs locally. It's a long-running Node.js service that acts as a message router between chat platforms (WhatsApp, Telegram, Discord) and an AI agent that can browse the web, read and write files, and run shell commands autonomously. It can manage Claude Code sessions, run tests, capture errors via Sentry, and open PRs on GitHub — all without you in the loop. MUXcode solves many of the same problems but keeps you in your terminal, in your editor, making the decisions.

### What they have in common

Both MUXcode and autonomous AI tools solve the same coordination problems:

- **Persistent memory** — Context that survives across sessions, searchable and shared between agents
- **Skills and plugins** — Reusable instruction sets that shape agent behavior for specific tasks or domains
- **Event-driven automation** — Chains of actions that trigger automatically based on outcomes
- **Session management** — Context compaction, history tracking, and long-running session support
- **Background process tracking** — Launching, monitoring, and reacting to long-running tasks
- **Loop detection** — Guardrails that catch agents stuck in repetitive failure patterns

### Where MUXcode differs

**Human-in-the-loop, not fully autonomous.** MUXcode keeps you as the orchestrator. The edit agent delegates on your behalf — you see every step, you decide what happens next. Autonomous tools aim to minimize human involvement, handling planning, execution, and error recovery on their own.

**Local-first, no infrastructure.** The message bus is JSONL files in `/tmp/`. The memory system is markdown files in your project directory. There's no database, no HTTP server, no container runtime. Autonomous tools typically require a runtime environment — SQLite, vector databases, sandboxed execution containers.

**Tmux-native, editor-centric.** You work in your actual editor alongside the agents. Press F2 to watch the build agent work. Press F7 to see the commit agent run git commands. There's no web UI, no chat interface separate from your terminal. Autonomous tools typically abstract the execution environment behind an API or web interface.

**Hook-driven orchestration, not LLM-driven.** The build-test-review chain fires via bash exit codes — deterministic, fast, zero token cost for routing. Autonomous tools typically use the LLM itself to decide what to do next, which is more flexible but slower and more expensive.

**Composable specialists, not a monolithic agent.** Each agent is a focused role with constrained permissions. The build agent can't edit files. The commit agent can't deploy. This separation of concerns mirrors how teams actually work. Autonomous tools often use a single agent with broad capabilities that handles everything.

**Zero external dependencies.** The bus binary is stdlib-only Go — it compiles in seconds with no dependency management. The hooks are bash scripts that use `jq` and standard Unix tools. Autonomous tools typically have significant dependency trees (Python packages, Node modules, system libraries).

The tradeoff is clear: autonomous tools can handle more without you, but MUXcode gives you visibility and control at every step. If you want to understand what's happening in your codebase — not just get a result — MUXcode is designed for that workflow.

## Quick start

### Prerequisites

- tmux >= 3.0
- Go >= 1.22
- [Claude Code](https://claude.ai/code) CLI (`claude`) — currently the only supported AI provider
- jq
- Neovim
- fzf (optional, for interactive project picker)

> **Note:** MUXcode currently requires Claude Code as the AI backend. Support for alternative providers (OpenAI, local models via Ollama/LM Studio, etc.) is on the roadmap.

### Install

```bash
git clone https://github.com/mkober/muxcode.git
cd muxcode
./install.sh
```

The installer checks prerequisites, builds the Go binary, and installs everything to `~/.local/bin/` and `~/.config/muxcode/`. It walks you through the remaining setup (tmux config, Claude Code hooks).

For subsequent builds after pulling updates:

```bash
./build.sh
```

### Launch

```bash
# Interactive project picker (requires fzf)
muxcode

# Direct path
muxcode ~/Projects/my-app

# Custom session name
muxcode ~/Projects/my-app my-session
```

## Configuration

MUXcode uses a shell-sourceable config file. Resolution order:

1. `$MUXCODE_CONFIG` (explicit path)
2. `.muxcode/config` (project-local)
3. `~/.config/muxcode/config` (user global)
4. Built-in defaults

See [Configuration](docs/configuration.md) for the full variable reference.

## Documentation

- [Architecture](docs/architecture.md) — System design, data flow, and hook chains
- [Agent Bus](docs/agent-bus.md) — CLI reference for `muxcode-agent-bus`
- [Agents](docs/agents.md) — Role descriptions and customization
- [Hooks](docs/hooks.md) — Hook system details
- [Configuration](docs/configuration.md) — Config file and environment variable reference

## License

[MIT](LICENSE)
