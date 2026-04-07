# Agents

## Overview

Each muxcode window runs an AI agent with a specific role. Agent behavior is defined by markdown files that serve as system prompts.

## Agent File Resolution

When `muxcode-agent.sh` launches an agent, it searches for the agent definition in this order:

1. `.claude/agents/<name>.md` — project-local (highest priority)
2. `~/.config/muxcode/agents/<name>.md` — user global
3. `<install-dir>/agents/<name>.md` — muxcode defaults

If no agent file is found, a built-in inline prompt is used as fallback.

### How agent files are loaded

The `launch_agent_from_file` function in `muxcode-agent.sh` handles agent file loading:

- **Project-local files** (`.claude/agents/<name>.md`): launched natively via `claude --agent <name>` — Claude Code resolves the file automatically.
- **External files** (`~/.config/muxcode/agents/` or install dir): the file is read, YAML frontmatter is stripped with `awk`, and the `description` field is extracted. The prompt body and metadata are passed to Claude Code via `--agents <JSON>` (requires `jq`).

The three-tier search (project-local → user config → install default) runs in `muxcode-agent.sh` after resolving the agent filename via `agent_name()`.

### Shared prompt assembly

Every agent (regardless of source) receives a dynamically assembled `--append-system-prompt` containing:

1. **Coordinator prompt** — role-specific coordination instructions (`muxcode prompt <role>`)
2. **Skills** — matching skill definitions for the role (`muxcode skill prompt <role>`)
3. **Context files** — from `context.d/shared/` + `context.d/<role>/` (`muxcode context prompt <role>`)
4. **Session resume** — previous session summaries from memory (`muxcode session resume <role>`)

### Permission mode

All agents run with `--dangerously-skip-permissions` for autonomous operation. The edit agent relies on hook-based guardrails (`muxcode hook guard`) and tool profiles to enforce safety, rather than interactive permission prompts.

## Built-in Roles

| Role | Agent File | Window | Description |
|------|-----------|--------|-------------|
| edit | code-editor.md | edit | Primary orchestrator — delegates to other agents |
| build | code-builder.md | build | Compile and package |
| test | test-runner.md | test | Run tests |
| review | code-reviewer.md | review | Review diffs for quality |
| deploy | infra-deployer.md | deploy | Infrastructure deployments |
| runner | command-runner.md | run | Execute commands |
| git | git-manager.md | commit | Git operations |
| analyst | editor-analyst.md | analyze | Analyze changes and explain patterns |
| watch | log-watcher.md | watch | Monitor logs (local, CloudWatch, k8s, Docker) |
| docs | doc-writer.md | docs | Generate and maintain documentation |
| research | code-researcher.md | research | Search web, explore codebases, answer questions |
| pr-read | pr-reader.md | commit *(via git-manager)* | Analyze PR review feedback and report suggested fixes |
| api | api-tester.md | api | Manage API collections, execute requests, track history |

## Agent Categories

### Orchestrator (edit)

The edit agent is the primary user-facing agent. It **never** runs build, test, deploy, or commit commands directly. Instead, it delegates via the message bus:

```bash
muxcode send build build "Run ./build.sh and report results"
muxcode send test test "Run tests and report results"
muxcode send review review "Review the latest changes"
```

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
muxcode-agent.sh pr-read
```

### Observers (watch)

The watch agent monitors logs from various sources — local files, CloudWatch, Kubernetes, Docker — and reports findings back to the edit agent. It is **read-only** by default: no Write/Edit tools, no git commands. It uses `muxcode log watch "summary"` to record observations to the watch history.

### Tool Specialists (deploy, runner, git)

These agents receive requests and execute, but may require more context or confirmation depending on the operation.

### Spawned Agents (temporary)

Any agent can create a temporary spawned agent for one-off tasks. The spawn inherits the base role's agent definition, tool permissions, and prompts but runs with a unique bus identity (`spawn-{id}`).

```bash
# Spawn a research agent for a one-off task
muxcode spawn start research "What does bus/guard.go do?"

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
- Are tracked in `spawn.jsonl` and monitored by the watcher
- Block commits while running (same as background processes)

## Local LLM Agent (Ollama)

Any agent role can optionally run via a local LLM (Ollama) instead of Claude Code, reducing API costs for roles that primarily execute structured commands (e.g. git operations).

### Configuration

Set per-role CLI override in `.muxcode/config`:

```bash
MUXCODE_GIT_CLI=local              # commit agent uses local LLM
MUXCODE_OLLAMA_MODEL=qwen2.5-coder:7b  # global default model
MUXCODE_GIT_MODEL=llama3.1:8b      # per-role model override
MUXCODE_OLLAMA_URL=http://localhost:11434  # Ollama URL (default)
```

The variable format is `MUXCODE_{ROLE}_CLI=local` where `{ROLE}` is the uppercase role name (e.g. `GIT` for the git/commit agent, `BUILD` for the build agent).

**Per-role model selection:** Each role can use a different model via `MUXCODE_{ROLE}_MODEL`. Resolution order: per-role env var → `MUXCODE_OLLAMA_MODEL` → default (`qwen2.5:7b`).

### How it works

1. `muxcode-agent.sh` checks `MUXCODE_{ROLE}_CLI` for the role
2. If `"local"`, verifies Ollama is reachable (`GET /api/tags`)
3. If reachable: runs `muxcode agent run <role>` instead of Claude Code
4. If unreachable: falls back to Claude Code with a warning

### Differences from Claude Code agents

| Aspect | Claude Code | Local LLM (Ollama) |
|--------|------------|-------------------|
| System prompt | Claude Code built-in + agent file | Same assembly: agent def + shared + skills + context.d + resume |
| Tool enforcement | `--allowedTools` flag | `IsToolAllowed()` in Go, same patterns |
| Hook chains | PostToolUse hooks fire automatically | Bash commands logged directly to `{role}-history.jsonl` |
| Conversation state | Managed by Claude Code | Reset between inbox checks (prevents unbounded context) |
| Cost | Anthropic API usage | Free (local compute) |

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
   MUXCODE_WINDOWS="edit build test review deploy run commit analyze api docs status"
   ```

2. Add a role mapping if window name differs from role:
   ```bash
   MUXCODE_ROLE_MAP="run=runner commit=git analyze=analyst docs=documentor"
   ```

3. Add the role to known roles:
   ```bash
   MUXCODE_ROLES="documentor"
   ```

4. Create an agent definition:
   ```bash
   # ~/.config/muxcode/agents/repo-documentor.md
   ```

5. Add a case to `agent_name()` in `scripts/muxcode-agent.sh` to map the role to its agent filename. Optionally add a tool profile entry in `bus/profile.go` to scope the agent's permissions.

### Agent Permissions

Agents have scoped permissions via tool profiles (`bus/profile.go`). The `--allowedTools` flags are resolved dynamically by `muxcode tools <role>` and passed to Claude Code at launch. Default permissions per role:

- **edit**: `Read`, `Glob`, `Grep`, `tree`, `python3`, `jq` (read-only — deliberately **no** `Write` or `Edit` tools, enforcing delegation via the bus)
- **build**: `./build.sh`, `make`, `go build`, `pnpm build`, `cargo build`
- **test**: `./test.sh`, `go test`, `jest`, `pytest`, `cargo test`
- **review**: `git diff`, `git log`, `git status`, `git show` (read-only git), `Write`
- **git**: `git *`, `gh *` (all git and GitHub CLI subcommands), `Write`, `Edit`
- **deploy**: `cdk`, `terraform`, `pulumi`, `aws`, `sam`, `curl`, `wget`, `./build.sh`, `make`, read-only git, `Write`, `Edit`
- **runner**: unrestricted (no `--allowedTools` filter)
- **analyst**: bus commands + Read, Glob, Grep (no shell commands)
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

## Agent health monitoring

Watcher-integrated liveness detection for agent processes. The watcher probes agent tmux panes every 30 seconds and applies a 3-strike escalation:

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

### Watcher self-monitoring

The watcher writes a Unix timestamp to `watcher.keepalive` at the top of each poll loop. A companion monitor (`muxcode watch --monitor`) checks the keepalive every 15 seconds — if stale (>30s), it kills and relaunches the watcher.

Core code: `bus/agent_health.go`, `bus/watcher_health.go`. Watcher code: `watcher/watcher.go` (`checkAgentHealth()`, `touchKeepalive()`). Monitor: `cmd/watch.go` (`runWatcherMonitor()`).

## Ollama health monitoring

Watcher-integrated health monitoring detects stuck Ollama instances (process alive but inference hanging) and auto-restarts both Ollama and affected agents.

- **Inference probe**: `CheckOllamaInference()` sends minimal chat completion (`max_tokens:1`) with 10s timeout — distinguishes "process alive but stuck" from "healthy" (unlike `/api/tags` which only checks process liveness)
- **Role discovery**: `LocalLLMRoles()` scans `MUXCODE_*_CLI=local` env vars to find which roles use Ollama
- **Agent failure tracking**: `agentState.consecutiveFailures` counter — after 3 consecutive `ChatComplete` failures, writes sentinel file at `lock/{role}.ollama-fail`; cleared on success
- **Detection timeline**: 30s first probe failure → 60s `ollama-down` alert to edit → 90s restart attempted → ~105s agents relaunched → ~135s recovery confirmed
- **Restart mechanism**: `RestartOllama()` kills via `pkill -f "ollama serve"`, starts detached, polls `/api/tags` for readiness (500ms intervals, 15s timeout)
- **Agent restart**: `RestartLocalAgent()` sends `C-c` via tmux, waits 500ms, relaunches `muxcode-agent.sh {role}`
- **Restart cap**: max 3 automatic restarts per session — after cap, periodic alerts only (manual intervention required)
- **Alert dedup**: `ollama-down`, `ollama-recovered`, `ollama-restarting` events deduped via `lastAlertKey` with 600s cooldown
- **System action exclusion**: registered in `isSystemAction()` to prevent false loop detection
- **Re-init cleanup**: `ollama-health.json` and `lock/*.ollama-fail` sentinels purged on session restart

Core code: `bus/health.go`, `bus/health_test.go`. Watcher code: `watcher/watcher.go` (`checkOllama()`).

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
| PII scrubbing | Automatic redaction of PII/secrets in tool output for sensitive roles |

CLI: `muxcode-llm-harness run <role> [--model MODEL] [--url URL] [--max-turns N]`

Separate Go module at `tools/muxcode-llm-harness/` — stdlib only, no external deps. The launcher (`muxcode-agent.sh`) prefers the harness binary when available, falls back to `muxcode agent run`.

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

Redacted values are replaced with bracketed placeholders (e.g. `[EMAIL_REDACTED]`, `[SECRET_REDACTED]`). Scrubbing is logged to stderr with redaction count per tool call.

For Claude Code agents in the same roles, `muxcode pii-scrub` provides equivalent pipe-through filtering. Agent definitions for api, runner, and watch instruct the agent to pipe sensitive output through the scrubber.

Core code: `harness/` package — `config.go`, `ollama.go`, `bus.go`, `tools.go`, `executor.go`, `filter.go`, `prompt.go`, `loop.go`, `message.go`, `scrub.go`.
