# Agents

## Overview

Each muxcode window runs an AI agent with a specific role. Agent behavior is defined by markdown files that serve as system prompts.

## Agent File Resolution

When `muxcode agent launch` runs, it searches for the agent definition in this order:

1. `.claude/agents/<name>.md` — project-local (highest priority)
2. `~/.config/muxcode/agents/<name>.md` — user global
3. `<install-dir>/agents/<name>.md` — muxcode defaults

If no agent file is found, a built-in inline prompt is used as fallback.

### How agent files are loaded

The `RunAgentLaunch()` function in `bus/launch.go` handles agent file loading:

- **Project-local files** (`.claude/agents/<name>.md`): launched natively via `claude --agent <name>` — Claude Code resolves the file automatically.
- **External files** (`~/.config/muxcode/agents/` or install dir): the file is read, YAML frontmatter is extracted by `ExtractFrontmatter()`, and the prompt body and metadata are passed to Claude Code via `--agents <JSON>` (via `BuildAgentsJSON()`).

The three-tier search (project-local → user config → install default) runs in `ResolveAgentFile()` after resolving the agent filename via `AgentFileName()`.

### Shared prompt assembly

Every agent (regardless of source) receives a dynamically assembled `--append-system-prompt` containing:

1. **Coordinator prompt** — role-specific coordination instructions (`muxcode prompt <role>`)
2. **Skills** — matching skill definitions for the role (`muxcode skill prompt <role>`)
3. **Context files** — from `context.d/shared/` + `context.d/<role>/` (`muxcode context prompt <role>`)
4. **Session resume** — previous session summaries from memory (`muxcode session resume <role>`)

### Permission mode

All Claude Code agents run with `--dangerously-skip-permissions` for autonomous operation. The edit agent relies on hook-based guardrails (`muxcode hook guard`) and tool profiles to enforce safety, rather than interactive permission prompts.

OpenCode agents use per-agent `permission` blocks in their generated agent definition (`.opencode/agents/<role>.md`). Muxcode's tool profiles are automatically translated to OpenCode's format — `Bash(pattern)` → `"pattern": allow`, `Write`/`Edit` → `edit: allow`.

### Multi-CLI provider support

Each agent independently resolves its AI CLI provider. The provider is fixed for the session lifetime.

| Provider | CLI value | Best for |
|----------|-----------|----------|
| Claude Code | `claude` (default) | Edit (default), review, deploy — full hook support, deterministic chains |
| OpenCode | `opencode` | Edit (optional), build, test, research — multi-provider LLM access, autonomous TUI |
| Codex CLI | `codex` | Analyze, review — OpenAI models, automatic approval mode (-a never) |
| Local LLM | `local` | Commit, build, watch — structured commands, zero API cost |

Set per-role: `MUXCODE_{ROLE}_CLI=opencode` in `.muxcode/config`. Set session-wide: `MUXCODE_AGENT_CLI=opencode`.

**Codex CLI sandbox limitation**: Codex CLI sandboxes all filesystem writes to the workspace and blocks outbound network access. This makes it unsuitable for roles that write to `.git/` (commit, deploy) or push to remotes. Only use Codex for read-only or workspace-scoped roles (review, analyze). Use Claude Code or OpenCode for git operations and network-dependent tasks.

Non-hook providers degrade gracefully across three layers:

1. **Role-specific prompt instructions** — the shared prompt includes a "Manual Bus Messaging" section with instructions specific to the agent's role (build agents see build chain commands, test agents see test chain commands, review agents see generic reply instructions). The shared prompt also includes explicit role identity ("You are the build agent") to prevent LLM confusion.
2. **Agent body adaptation** — hook chain references in agent definitions (e.g. "the bash hook auto-chains build→test") are rewritten to manual chain instructions via `adaptBodyForNonHookProvider()`.
3. **Send policy bypass** — `CheckSendPolicy()` skips deny rules for non-hook providers, and the "Send Restrictions" prompt section is hidden. Without this, non-hook agents told to manually chain would have their sends blocked by the very policy meant for hook-capable agents.

## Built-in Roles

| Role | Agent File | Window | Description |
|------|-----------|--------|-------------|
| plan | planner.md | plan (F1) | Documentation specialist — maintains requirements, architecture docs, and planning artifacts |
| edit | code-editor.md | edit (F2) | Primary orchestrator — delegates to other agents |
| build | code-builder.md | build (F3) | Compile and package |
| test | test-runner.md | test (F4) | Run tests |
| serve | dev-server.md | serve (F5) | Start, monitor, and auto-restart local development servers |
| review | code-reviewer.md | review (F6) | Review diffs for quality |
| deploy | infra-deployer.md | deploy (F7) | Infrastructure deployments |
| run | command-runner.md | run (F8) | Execute commands |
| watch | log-watcher.md | watch (F9) | Monitor logs (local, CloudWatch, k8s, Docker) |
| commit | git-manager.md | commit (F10) | Git operations |
| analyze | editor-analyst.md | *(opt-in window)* | Analyze changes and explain patterns — add `analyze` to `MUXCODE_WINDOWS` |
| docs | doc-writer.md | plan *(via planner)* | Generate and maintain documentation |
| research | code-researcher.md | plan *(via mode cycle)* | Search API docs, platform refs, GitHub projects (OpenCode/DeepSeek) |
| pr-read | pr-reader.md | commit *(via git-manager)* | Analyze PR review feedback and report suggested fixes |
| api | api-tester.md | api | Manage API collections, execute requests, track history |
| agent | autonomous-agent.md | edit *(via mode cycle)* | Autonomous story executor — reads Jira, creates requirements, implements features, submits PRs |

## Agent Categories

### Orchestrator (edit)

The edit agent is the primary user-facing agent. It **never** runs build, test, deploy, or commit commands directly. Instead, it delegates via the message bus:

```bash
muxcode send build build "Run ./build.sh and report results"
muxcode send test test "Run tests and report results"
muxcode send review review "Review the latest changes"
```

#### OpenCode edit agent

The edit agent can optionally run on OpenCode with DeepSeek V4 Pro instead of Claude Code. Set `MUXCODE_EDIT_CLI=opencode` to switch; set back to `claude` (or unset) to restore the default.

```bash
# In ~/.config/muxcode/config
MUXCODE_EDIT_CLI=opencode
MUXCODE_EDIT_MODEL=opencode-go/deepseek-v4-pro  # optional — this is the default
```

Key differences from the Claude Code edit agent:

| Aspect | Claude Code (default) | OpenCode |
|--------|----------------------|----------|
| Delegation enforcement | PreToolUse hook (`muxcode hook guard`) | `DenyTools` in tool profile → OpenCode `deny` permission rules |
| Build→test→review chain | PostToolUse bash hook (automatic) | Prompt-driven manual orchestration via `muxcode send` |
| Write/Edit tools | Implicit (Claude built-in) | Explicit in tool profile |
| Workflow state transitions | PostToolUse hooks | Daemon-side `checkNonHookEdits()` via `git diff --stat` polling |
| Compact | `/compact` slash command | OpenCode auto-compaction + `muxcode session compact` |
| Startup context | Self-addressed inbox message | Memory context appended to SharedPrompt |
| Model | `claude-opus-4-8` | `opencode-go/deepseek-v4-pro` (configurable via `MUXCODE_EDIT_MODEL`) |

All delegation rules, bus messaging, and agent coordination remain identical — only the enforcement mechanism changes.

### Documentation (plan)

The plan agent maintains project documentation — requirements specs, architecture docs, and planning artifacts. It runs in the F1 window with Neovim on the left (opening the last-edited doc) and Claude Code on the right. The edit agent delegates doc updates to the planner:

```bash
muxcode send plan update-docs "Phase 1 complete — check off acceptance criteria"
muxcode send plan create-spec "Create a draft spec for webhook retry logic"
muxcode send plan move-spec "Move conditional-chains.md from drafts to completed"
```

The plan agent is scoped to docs directories only — it can read source code for context but never writes outside `docs/`, `CLAUDE.md`, or `README.md`.

### Dev server (serve)

The serve agent manages local development server lifecycles — starting, monitoring, and auto-restarting dev servers (Vite, Next.js, Webpack, Flask, Go, Docker Compose, etc.). It runs in the F5 window.

```bash
muxcode send serve serve "Start the dev server"
muxcode send serve status "Report server status"
muxcode send serve stop "Stop the server on port 5173"
muxcode send serve restart "Restart the dev server"
```

The serve agent:
- Auto-detects project type from `package.json`, framework configs, or Makefile targets
- Prefers repo scripts (`run.sh`, `run-dev.sh`, `dev.sh`) over framework-specific commands
- Checks for port conflicts before starting
- Monitors server health via PID liveness and HTTP probes
- Auto-restarts crashed servers (up to 5 consecutive failures)
- Tracks state in `/tmp/muxcode-bus-{session}/serve-state.json`

Tool profile: `bus`, `readonly`, `common`, plus process management (`kill`, `nohup`), HTTP tools (`curl`, `wget`), network inspection (`lsof`, `ss`), framework CLIs (`node`, `pnpm`, `npx`, `python`, `flask`, `go run`, `docker`), and temp file I/O.

### Autonomous Specialists (build, test, review, analyst)

These agents operate autonomously — they receive requests, execute unconditionally, and reply. They never ask for permission before acting.

**Sequence:**
1. Read inbox: `muxcode inbox`
2. Execute their command
3. Reply to requester: `muxcode send <from> <action> "<result>" --type response --reply-to <id>`

### PR Reading (via commit agent)

PR review analysis runs in the **commit window** via the git-manager agent. The edit agent delegates with action `pr-read`:

**Invoke from the edit agent:**
```bash
muxcode send commit pr-read "Read PR reviews and CI failures on the current branch and report suggested fixes"
```

The git-manager reads reviews, CI checks, and inline comments, categorizes them (must-fix / should-fix / informational), and reports a structured summary back to edit.

**Standalone use** (outside a session):
```bash
export BUS_SESSION="your-session"
muxcode agent launch pr-read
```

### Autonomous Agent (agent)

The autonomous agent operates a complete Jira story lifecycle without user intervention — from reading todo stories to submitting completed PRs. It shares the F2 window with the edit agent via mode cycling (press F2 when on the edit window to toggle, or `prefix + a` from any window).

**Mode cycling**: `muxcode mode cycle` swaps panes between the edit agent (nvim + Claude Code) and the autonomous agent (console viewer + Claude Code). All processes persist across cycles — nvim, edit agent, and autonomous agent keep their sessions alive in hidden tmux holding windows.

| Command | Description |
|---------|-------------|
| `muxcode mode cycle` | Cycle to next agent on the edit window |
| `muxcode mode status` | Show current agent, cycle index, registered agents |
| `muxcode mode switch <mode>` | Jump directly to a specific agent by mode name |
| `muxcode mode list` | List all registered agents with current indicator |

**Story lifecycle**: The agent polls Jira for assigned todo stories, creates feature branches, writes requirements docs, opens review PRs, implements approved requirements via build/test/review delegation, and submits implementation PRs. Jira status transitions happen automatically (To Do → In Progress → Done).

**Delegation model**: The agent delegates autonomously to all specialist agents — `commit` (branch, commit, push, PR), `build`, `test`, `review`, `deploy`, `run`, `watch`, and `plan`. All delegations use `--wait` for synchronous responses.

**Task file**: The agent reads `.muxcode/agent-tasks.md` (or `MUXCODE_AGENT_TASKS` path) for natural-language task configuration — polling intervals, story limits, guardrails. Changes take effect on the next heartbeat cycle without restart. Env vars override task file values.

**Story lifecycle skill**: The workflow is defined in `skills/story-lifecycle.md` — users can override it via `.muxcode/skills/story-lifecycle.md` to customize the pipeline (skip requirements PR, add deploy phase, etc.).

**Heartbeat**: The daemon fires a `heartbeat` action to the agent inbox at `MUXCODE_AGENT_HEARTBEAT` interval (default 30 minutes). On heartbeat, the agent checks for higher-priority stories, PR status on open PRs, and stale delegations. Set to `0` to disable.

**Console viewer**: The left pane shows a Dracula-themed activity log (`muxcode console agent`) with a status header displaying: current story, phase, stories done, uptime, and last heartbeat. Query status programmatically via `muxcode agent status`.

**State files** (ephemeral, in `/tmp/muxcode-bus-{session}/`):

| File | Purpose |
|------|---------|
| `mode-cycle-edit.json` | Cycle state: current index, registered agents |
| `agent-current-story` | Current Jira key being worked |
| `agent-phase` | Current phase: requirements, implementation, waiting |
| `agent-stories-done` | Count of completed stories this session |
| `agent-last-heartbeat` | Timestamp of last heartbeat |

**Safety guardrails**:

| Guardrail | Default |
|-----------|---------|
| Max stories per session | `MUXCODE_AGENT_MAX_STORIES` = 5 |
| Max build/test/fix iterations per story | `MUXCODE_AGENT_MAX_ITERATIONS` = 10 |
| PR wait timeout | `MUXCODE_AGENT_PR_MAX_WAIT` = 3600s |
| Consecutive failures before pause | `MUXCODE_AGENT_PAUSE_ON_FAILURE` = 3 |
| Branch protection | Always feature branches, never main |
| Commit delegation | All commits via commit agent |

Core code: `bus/mode.go` (cycle state, pane swap), `bus/console.go` (agent status header, console renderer), `cmd/mode.go` (CLI), `cmd/agent.go` (status sub-subcommand).

### Observers (watch)

The watch agent monitors logs from various sources — local files, CloudWatch, Kubernetes, Docker — and reports findings back to the edit agent. It is **read-only** by default: no Write/Edit tools, no git commands. It uses `muxcode log watch "summary"` to record observations to the watch history.

### Tool Specialists (deploy, run, commit)

These agents receive requests and execute, but may require more context or confirmation depending on the operation.

### Spawned Agents (temporary)

Any agent can create a temporary spawned agent for one-off tasks. The spawn inherits the base role's agent definition, tool permissions, and prompts but runs with a unique bus identity (`spawn-{id}`).

```bash
# Spawn a review agent for a one-off task
muxcode spawn start review "Review the changes in bus/guard.go"

# Check status
muxcode spawn list

# Get the result after completion
muxcode spawn result <id>

# Clean up
muxcode spawn clean
```

Spawned agents:
- Run in their own tmux window (named `spawn-{id}`)
- Receive their task via the bus inbox (pre-seeded before launch)
- Send results back to the owner via normal bus messages
- Are tracked in `spawn.jsonl` and monitored by the daemon
- Block commits while running (same as background processes)

## Local LLM Agent (Ollama)

Any agent role can optionally run via a local LLM (Ollama) instead of Claude Code, reducing API costs for roles that primarily execute structured commands (e.g. git operations).

### Configuration

Set per-role CLI override in `.muxcode/config`:

```bash
MUXCODE_COMMIT_CLI=local           # commit agent uses local LLM
MUXCODE_OLLAMA_MODEL=qwen2.5-coder:7b  # global default model
MUXCODE_COMMIT_MODEL=llama3.1:8b   # per-role model override
MUXCODE_OLLAMA_URL=http://localhost:11434  # Ollama URL (default)
```

The variable format is `MUXCODE_{ROLE}_CLI=local` where `{ROLE}` is the uppercase canonical role name (e.g. `COMMIT` for the commit agent, `BUILD` for the build agent, `ANALYZE` for the analyze agent).

**Per-role model selection:** Each role can use a different model via `MUXCODE_{ROLE}_MODEL`. Resolution order: per-role env var → `MUXCODE_OLLAMA_MODEL` → default (`qwen2.5:7b`).

### How it works

1. The provider system resolves `MUXCODE_{ROLE}_CLI` for the role
2. If `"local"`, `LocalProvider.ConfigureLaunch()` sets `IsLocal=true` and builds harness args
3. `RunAgentLaunch()` verifies Ollama is reachable (`GET /api/tags`)
4. If reachable: runs `muxcode-llm-harness run <role>` (or `muxcode agent run <role>` as fallback)
5. If unreachable: falls back to Claude Code with a warning

### Differences across providers

| Aspect | Claude Code | OpenCode (TUI) | Codex CLI | Local LLM (Ollama) |
|--------|------------|----------------|-----------|-------------------|
| System prompt | Claude Code built-in + agent file | Agent markdown body + shared prompt | Shared `.codex/AGENTS.md` + prompt instructions | Same assembly: agent def + shared + skills + context.d + resume |
| Tool enforcement | `--allowedTools` flag | `permission` blocks in agent config | `.codex/AGENTS.md` instructions | `IsToolAllowed()` in Go, same patterns |
| Hook chains | PostToolUse hooks fire automatically | No hooks — role-specific prompt instructions + adapted body text + send policy bypass | No hooks — prompt instructions + send policy bypass | Bash commands logged directly to `{role}-history.jsonl` |
| Conversation state | Managed by Claude Code | Managed by OpenCode TUI (auto-compact) | Managed by Codex CLI | Reset between inbox checks (prevents unbounded context) |
| Idle detection | `❯` prompt match | Not supported (TUI) | Heuristic (`>` prompt / "Summarize") | Not supported |
| Cost | Anthropic API usage | Provider-dependent (multi-provider) | OpenAI API usage | Free (local compute) |

### CLI

```bash
muxcode agent run <role> [--model MODEL] [--url URL]
```

See [Agent Bus CLI](agent-bus.md#muxcode-agent) for full reference.

## Message Bus Protocol

All agents share the same bus protocol:

```bash
# Check inbox
muxcode inbox

# Send a message
muxcode send <to> <action> "<message>"

# Reply to a request
muxcode send <from> <action> "<result>" --type response --reply-to <id>

# Read memory
muxcode memory context

# Save learnings
muxcode memory write "<section>" "<text>"

# Search memory
muxcode memory search "<query>" [--role ROLE] [--limit N]

# List all memory sections
muxcode memory list [--role ROLE]
```

## Customization

### Override an Agent

Create a custom agent file in your project:

```bash
mkdir -p .claude/agents
cp ~/.config/muxcode/agents/code-builder.md .claude/agents/code-builder.md
# Edit to add project-specific instructions
```

### Add a New Role

1. Add the window to your config:
   ```bash
   MUXCODE_WINDOWS="edit build test review deploy run commit watch api analyze"
   ```

2. Add a role mapping if window name differs from role:
   ```bash
   MUXCODE_ROLE_MAP="docs=documentor"
   ```

3. Add the role to known roles:
   ```bash
   MUXCODE_ROLES="documentor"
   ```

4. Create an agent definition:
   ```bash
   # ~/.config/muxcode/agents/repo-documentor.md
   ```

5. Add a case to `AgentFileName()` in `bus/launch.go` to map the role to its agent filename. Optionally add a tool profile entry in `bus/profile.go` to scope the agent's permissions.

### Agent Permissions

Agents have scoped permissions via tool profiles (`bus/profile.go`). The `--allowedTools` flags are resolved dynamically by `muxcode tools <role>` and passed to Claude Code at launch. Default permissions per role:

- **plan**: `Read`, `Glob`, `Grep`, `Write`, `Edit`, read-only git (`git diff`, `git log`, `git status`, `git rev-parse`), `tree`, `python3`, `jq` (scoped to docs directories)
- **edit**: `Read`, `Glob`, `Grep`, `tree`, `python3`, `jq` (read-only — deliberately **no** `Write` or `Edit` tools, enforcing delegation via the bus)
- **build**: `./build.sh`, `make`, `go build`, `pnpm build`, `cargo build`
- **test**: `./test.sh`, `go test`, `jest`, `pytest`, `cargo test`
- **serve**: `curl`, `wget`, `lsof`, `kill`, `nohup`, `node`, `pnpm`, `npx`, `python`, `flask`, `go run`, `docker`, `make`, repo scripts (`run.sh`, `run-dev.sh`, `dev.sh`), temp file I/O
- **review**: `git diff`, `git log`, `git status`, `git show` (read-only git), `Write`
- **commit**: `git *`, `gh *` (all git and GitHub CLI subcommands), `Write`, `Edit`
- **deploy**: `cdk`, `terraform`, `pulumi`, `aws`, `sam`, `curl`, `wget`, `./build.sh`, `make`, read-only git, `Write`, `Edit`
- **run**: unrestricted (no `--allowedTools` filter)
- **analyze**: bus commands + Read, Glob, Grep (no shell commands)
- **watch**: `tail`, `journalctl`, `aws logs`, `kubectl logs`, `docker logs`, `stern`, `ssh`, `lnav` (read-only log tools)
- **pr-read**: `gh pr view/checks/diff/review/list/status`, `gh api`, `git diff/log/status/show/blame/rev-parse/branch`, `jq` (read-only: scoped gh + git, no Write/Edit)
- **api**: `curl`, `wget`, `http`, `jq`, `python`, `node`, `openssl`, `base64`, `dig`, `nslookup`, `Write`, `Edit`

All agents have access to `muxcode` commands. The edit agent's lack of `Write`/`Edit` tools is enforced at the tool profile level — Claude Code will not auto-approve file modifications, ensuring all code changes go through the user's accept/reject flow.

## Memory

Memory has two layers — project-level and global (cross-session):

```
.muxcode/memory/                     # Project-level (per-project)
├── shared.md                        # Cross-agent learnings
├── edit.md                          # Edit agent learnings
├── build.md                         # Build agent learnings
└── ...

~/.config/muxcode/memory/            # Global (cross-session, all projects)
├── shared.md                        # Universal shared learnings
├── {role}.md                        # Universal per-role learnings
└── {role}/                          # Daily archives
    └── YYYY-MM-DD.md
```

Agents read memory with `muxcode memory context` (includes both global and project memory) and write with `muxcode memory write "<section>" "<text>"` (project) or `muxcode memory write-global "<section>" "<text>"` (global). Use `--no-global` on context to skip global memory. To find specific learnings, use `muxcode memory search "<query>"` (BM25 search with `--scope project|global|all`) or `muxcode memory list` to see all sections.

Global memory stores universal patterns (conventions, tool quirks, workflow preferences) that apply across all projects. Project memory stores project-specific learnings (build commands, architecture decisions, test patterns).

## Tool profiles

Per-role tool permissions defined in `bus/profile.go`. Each profile specifies which `--allowedTools` patterns the agent receives.

| Component | Description |
|-----------|-------------|
| `Include` | Shared tool groups to inherit (`bus`, `readonly`, `common`) |
| `CdPrefix` | Auto-generate `cd <dir> &&` variants of commands |
| `Tools` | Role-specific `--allowedTools` patterns |

Shared groups:

- `bus` — `Bash(muxcode *)` and bus CLI commands
- `readonly` — `Read`, `Glob`, `Grep`
- `common` — `ls`, `cat`, `diff`, `sed`, `awk`, etc.

CLI: `muxcode tools <role>` — resolves includes, applies CdPrefix, outputs one pattern per line. Patterns use Claude Code `--allowedTools` glob syntax (e.g. `Bash(git diff*)`).

**Process substitution**: `Bash(diff *)` does NOT match `diff <(...)` — Claude Code treats `<()` as a special construct requiring explicit `Bash(diff <(*)`.

## Hot reload

Change the CLI provider and model for any agent at runtime without restarting the session. Preserves inbox, memory, workflow state, and bus identity.

### Usage

```bash
# Reload with current config (re-reads env vars and config file)
muxcode reload build

# Switch CLI provider
muxcode reload build --cli opencode

# Switch model
muxcode reload build --model opencode-go/deepseek-v4-pro

# Switch both
muxcode reload edit --cli opencode --model opencode-go/deepseek-v4-pro

# Compact context before reloading
muxcode reload edit --model claude-opus-4-8 --compact

# Reload all active agents
muxcode reload --all

# Reload multiple specific agents with provider/model override
muxcode reload build test review --cli opencode --model opencode-go/minimax-m2.5

# Reload all agents currently on Claude, switching them to OpenCode
muxcode reload --all --provider claude --cli opencode --model opencode-go/minimax-m2.5
```

### Bulk reload

Switch multiple agents to a different provider/model in a single operation — useful for responding to provider outages or migrating workloads.

#### CLI multi-role

Pass multiple role names as positional arguments. All agents are reloaded sequentially (3s gap) with the same `--cli`/`--model` overrides:

```bash
muxcode reload build test review --cli opencode --model opencode-go/minimax-m2.5
```

Per-agent results are printed as each completes. Failed agents don't abort the batch — all agents are attempted. Exit code is non-zero if any agent failed.

#### `--provider` filter

Combined with `--all`, the `--provider` flag limits the reload to agents currently running on the specified CLI:

```bash
# Only switch Claude agents to OpenCode (OpenCode agents are untouched)
muxcode reload --all --provider claude --cli opencode --model opencode-go/minimax-m2.5
```

`--provider` requires `--all` and is rejected without it.

#### `--all` with overrides

`--all` now accepts `--cli` and `--model` flags — applies the same override to every active agent:

```bash
muxcode reload --all --cli opencode --model opencode-go/minimax-m2.5
```

Core code: `bus/reload_batch.go` (`ReloadBatch()`, `ReloadResult`, `ActiveAgentStatuses()`).

### Provider selector modal

An interactive TUI modal for visually picking a provider, model, and target agents. Supports single-agent reload (existing workflow) and multi-agent bulk reload.

- **Keybinding**: `prefix + R` or `prefix + b → Provider`
- **Sections**: Provider (radio), Model (radio + custom input), Agents (checkboxes), Options (compact/persist checkboxes)
- **Navigation**: `j`/`k`/arrows move, `Tab` switches section, `Space` selects, `Enter` confirms, `q`/`Esc` cancels

Mode-cycled windows resolve to the active role (e.g., research on F1, auto on F2).

#### Agents section

The Agents section lists all active agents with their current CLI, abbreviated model, and F-key:

```
[x] Build     claude / sonnet-4-6 F3
[ ] Test      opencode / minimax  F4
[ ] Review    claude / opus-4-8   F6
```

**Safety indicators**: `edit` and `auto` are shown with a `⚠` warning suffix (orchestrator disruption risk). They are selectable individually but excluded from the `a` (select all) shortcut.

| Key | Action |
|-----|--------|
| `a` | Select all agents (excludes edit/auto) |
| `n` | Deselect all agents |
| `p` | Toggle all agents matching the selected provider's CLI |
| `Space` | Toggle individual agent |

The `p` shortcut is the key workflow accelerator for provider outages: select the target provider, select the model, tab to Agents, press `p` to select all agents on the failing provider, then confirm.

#### Progress view

When >1 agent is selected, confirming transitions the modal to a live progress view:

- `✓` — reload succeeded (green)
- `✗` — reload failed (red)
- `⟳` — currently reloading (yellow)
- `○` — pending (dim)

Progress bar shows `N/M` completion count. Pressing `q` closes the modal but does not cancel in-progress reloads.

### Persistent config changes

```bash
# Set CLI for a role (persists to ~/.config/muxcode/config)
muxcode config set build.cli opencode

# Set and reload in one step
muxcode config set build.cli opencode --reload

# View effective config (shows resolution source)
muxcode config get build

# List all roles
muxcode config list
```

### Runtime override resolution

When reloading, configuration is resolved in this order (first non-empty wins):

| Priority | Source | Scope |
|----------|--------|-------|
| 1 | Runtime override (`/tmp/muxcode-bus-{session}/config/{role}.env`) | Per-role, session-scoped |
| 2 | `MUXCODE_{ROLE}_CLI` / `MUXCODE_{ROLE}_MODEL` env var | Per-role, shell-scoped |
| 3 | `MUXCODE_AGENT_CLI` env var | Session-wide |
| 4 | Config file (`~/.config/muxcode/config`) | Persistent |
| 5 | `roleDefaultCLI()` / `RoleClaudeModelDefault()` | Built-in default |

Runtime overrides are ephemeral (session-scoped in `/tmp/`). Use `muxcode config set` for persistent changes.

### Mode-cycled agents

Reload operates on individual roles within mode cycles. The active role reloads in the host window pane; the inactive role reloads in its holding window pane. Mode state is preserved.

### What is preserved across reload

| Preserved | Not preserved |
|-----------|---------------|
| Inbox messages | Active conversation context |
| Memory (cross-session) | In-progress tool executions |
| Workflow state | Agent process state |
| Bus identity | |
| Console viewer (pane 0) | |

Use `--compact` to save conversation context to memory before reloading.

Core code: `bus/reload.go`, `bus/reload_batch.go`, `bus/override.go`, `cmd/reload.go`, `cmd/config.go`, `tui/provider_select.go`, `bus/provider_options.go`.

## Agent health monitoring

Daemon-integrated liveness detection for agent processes. The daemon probes agent tmux panes every 30 seconds and applies a 3-strike escalation:

| Strike | Elapsed | Action |
|--------|---------|--------|
| 1 | 30s | Log failure, increment counter |
| 2 | 60s | Send `agent-down` event to edit |
| 3 | 90s | Restart agent, send `agent-restarting` event |

- **Detection heuristic**: captures last 5 lines of agent pane — checks for harness PID, `❯` idle prompt, bare shell prompt (`$`/`%` at end of last line), or startup text
- **Excluded roles**: `edit` (user session), `webhook` (PID-managed), `spawn-*` (own lifecycle)
- **Intentional stop**: `muxcode agent-health --stop <role>` writes a `{role}.stopped` marker, suppressing auto-restart. `--start` removes it.
- **Restart cap**: max 3 per role per session — after cap, alerts only (no more restarts)
- **Recovery detection**: when a previously-down agent passes a probe, sends `agent-recovered` event
- **System action exclusion**: `agent-down`, `agent-restarting`, `agent-recovered` registered in `isSystemAction()`

### Daemon self-monitoring

The daemon writes a Unix timestamp to `watcher.keepalive` at the top of each poll loop. A companion monitor (`muxcode watch --monitor`) checks the keepalive every 15 seconds — if stale (>30s), it kills and relaunches the daemon.

Core code: `bus/agent_health.go`, `bus/watcher_health.go`. Daemon code: `watcher/watcher.go` (`checkAgentHealth()`, `touchKeepalive()`). Monitor: `cmd/watch.go` (`runWatcherMonitor()`).

## Ollama health monitoring

Daemon-integrated health monitoring detects stuck Ollama instances (process alive but inference hanging) and auto-restarts both Ollama and affected agents.

- **Inference probe**: `CheckOllamaInference()` sends minimal chat completion (`max_tokens:1`) with 10s timeout — distinguishes "process alive but stuck" from "healthy" (unlike `/api/tags` which only checks process liveness)
- **Role discovery**: `LocalLLMRoles()` scans `MUXCODE_*_CLI=local` env vars to find which roles use Ollama
- **Agent failure tracking**: `agentState.consecutiveFailures` counter — after 3 consecutive `ChatComplete` failures, writes sentinel file at `lock/{role}.ollama-fail`; cleared on success
- **Detection timeline**: 30s first probe failure → 60s `ollama-down` alert to edit → 90s restart attempted → ~105s agents relaunched → ~135s recovery confirmed
- **Restart mechanism**: `RestartOllama()` kills via `pkill -f "ollama serve"`, starts detached, polls `/api/tags` for readiness (500ms intervals, 15s timeout)
- **Agent restart**: `RestartLocalAgent()` sends `C-c` via tmux, waits 500ms, relaunches `muxcode agent launch {role}`
- **Restart cap**: max 3 automatic restarts per session — after cap, periodic alerts only (manual intervention required)
- **Alert dedup**: `ollama-down`, `ollama-recovered`, `ollama-restarting` events deduped via `lastAlertKey` with 600s cooldown
- **System action exclusion**: registered in `isSystemAction()` to prevent false loop detection
- **Re-init cleanup**: `ollama-health.json` and `lock/*.ollama-fail` sentinels purged on session restart

Core code: `bus/health.go`, `bus/health_test.go`. Daemon code: `watcher/watcher.go` (`checkOllama()`).

## Local LLM harness

Standalone binary (`muxcode-llm-harness`) that replaces `muxcode agent run` for local LLM roles. Solves the inbox-loop problem where small LLMs repeatedly call `muxcode inbox` instead of executing tasks.

| Feature | Description |
|---------|-------------|
| Tool call filtering | Blocks inbox commands, self-sends, and repetitive commands before they reach the executor |
| Structured task format | Inbox messages pre-consumed and formatted as structured markdown tasks |
| Corrective feedback | Blocked tool calls receive explanatory messages |
| Loop prevention | Command hash tracking, blocks same command after 3 repetitions |
| Role examples | `RoleExamples()` provides concrete tool call examples per role |
| Circuit breaker | Cross-batch failure tracking with cooldown — prevents runaway Ollama calls |
| Single-shot auto-complete | Build and test roles auto-complete after one successful tool execution — prevents small models from looping |
| Harness agent definitions | Simplified prompts in `agents/harness/` tailored for local LLMs |
| TUI activity log | Dracula-themed display showing Ollama calls, tool executions with output previews, and status bar |
| Chat history truncation | Tool outputs truncated to 2KB in persistent chat history to prevent context exhaustion |
| PII scrubbing | Automatic redaction of PII/secrets in tool output for sensitive roles |

CLI: `muxcode-llm-harness run <role> [--model MODEL] [--url URL] [--max-turns N] [--tui]`

Separate Go module at `tools/muxcode-llm-harness/` — stdlib only, no external deps. The launcher (`RunAgentLaunch()`) prefers the harness binary when available, falls back to `muxcode agent run`.

### Circuit breaker

The harness has three layers of stuck-loop protection:

| Layer | Scope | Mechanism |
|-------|-------|-----------|
| Within-turn | Single tool call | Filter blocks inbox checks, self-sends, repeated commands (3x limit) |
| Within-batch | Single message batch | `MaxAllBlockedTurns` (2) — breaks out early if all tool calls are blocked for 2 consecutive turns |
| Cross-batch | Across message batches | `MaxConsecutiveFailures` (3) — after 3 failed batches, enters 30s cooldown before resuming |

Each batch runs under a 5-minute `context.WithTimeout`. A batch is considered failed if it produces `(no response generated)`, `(all tool calls blocked)`, times out, or encounters an Ollama error.

### PII scrubbing

Tool output from `api`, `runner`/`run`, and `watch` roles is automatically scrubbed before entering the Ollama conversation. Patterns redacted:

- Email addresses, SSN, credit card numbers (prefix-anchored: Visa, MC, Amex, Discover), phone numbers (separator-required)
- AWS access/secret keys, JWT tokens, generic API keys/tokens/passwords, dates of birth

Redacted values are replaced with bracketed placeholders (e.g. `[EMAIL_REDACTED]`, `[SECRET_REDACTED]`). Scrubbing is logged to stderr with redaction count per tool call. When anything is redacted, a self-documenting banner (`PIIScrubNotice`) is also prepended to the output via `ScrubPIIWithNotice()` so the agent doesn't treat masked placeholders as real data or compute lengths/counts over them.

For Claude Code agents in the same roles, `muxcode pii-scrub` provides equivalent pipe-through filtering. Agent definitions for api, runner, and watch instruct the agent to pipe sensitive output through the scrubber.

### Single-shot auto-complete

Build and test roles are designated as "single-shot" via `isSingleShotRole()`. After one successful tool execution, the harness breaks out of the tool-calling loop and forces a text-only Ollama call to generate the response summary. This prevents small models (e.g. gemma4, qwen2.5-coder) from endlessly re-running the same command.

Flow: tool executes → single-shot detected → loop breaks → summary call (no tools) → response sent.

### Harness agent definitions

The `agents/harness/` directory contains simplified agent definitions for local LLMs. These are shorter and more directive than the standard definitions — they avoid bus messaging instructions, multi-step discovery sequences, and other patterns that confuse smaller models.

Resolution order for `ReadAgentDefinition()`:

1. `agents/harness/<name>.md` — project-local harness-specific (highest priority)
2. `~/.config/muxcode/agents/harness/<name>.md` — user harness-specific
3. `.claude/agents/<name>.md` — project-local standard
4. `~/.config/muxcode/agents/<name>.md` — user standard

Shipped harness definitions: `code-builder.md`, `test-runner.md`, `code-reviewer.md`, `planner.md`.

### Harness TUI

The `--tui` flag enables a Dracula-themed terminal UI (used by default when launched from muxcode). Features:

- **Activity log** — live event stream showing Ollama calls, tool executions with timing, and output previews (last meaningful line of command output, tabs replaced with spaces)
- **Status bar** — bottom line with status/uptime (left) and role/model/provider (right)
- **Action labels** — turns labeled by action name (e.g. "Build 1/10", "Test 1/10") instead of generic "Turn"
- **Alternate screen buffer** — clean rendering without scrollback pollution
- **Tool output preview** — `╰` connector showing the last line of tool output (skips exit codes and truncation markers)

### Chat history management

Tool outputs in the current conversation turn use full output (up to `MaxOutputLen` = 30KB). When stored in persistent chat history for subsequent turns, outputs are truncated to `maxChatToolOutput` (2KB) to prevent context window exhaustion with small models.

Core code: `harness/` package — `config.go`, `ollama.go`, `bus.go`, `tools.go`, `executor.go`, `filter.go`, `prompt.go`, `loop.go`, `events.go`, `tui.go`, `message.go`, `scrub.go`.
