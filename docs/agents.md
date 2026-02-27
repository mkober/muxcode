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

## Agent Categories

### Orchestrator (edit)

The edit agent is the primary user-facing agent. It **never** runs build, test, deploy, or commit commands directly. Instead, it delegates via the message bus:

```bash
muxcode-agent-bus send build build "Run ./build.sh and report results"
muxcode-agent-bus send test test "Run tests and report results"
muxcode-agent-bus send review review "Review the latest changes"
```

### Autonomous Specialists (build, test, review, analyst)

These agents operate autonomously — they receive requests, execute unconditionally, and reply. They never ask for permission before acting.

**Sequence:**
1. Read inbox: `muxcode-agent-bus inbox`
2. Execute their command
3. Reply to requester: `muxcode-agent-bus send <from> <action> "<result>" --type response --reply-to <id>`

### PR Reading (via commit agent)

PR review analysis runs in the **commit window** via the git-manager agent. The edit agent delegates with action `pr-read`:

**Invoke from the edit agent:**
```bash
muxcode-agent-bus send commit pr-read "Read PR reviews and CI failures on the current branch and report suggested fixes"
```

The git-manager reads reviews, CI checks, and inline comments, categorizes them (must-fix / should-fix / informational), and reports a structured summary back to edit.

**Standalone use** (outside a session):
```bash
export BUS_SESSION="your-session"
muxcode-agent.sh pr-read
```

### Observers (watch)

The watch agent monitors logs from various sources — local files, CloudWatch, Kubernetes, Docker — and reports findings back to the edit agent. It is **read-only** by default: no Write/Edit tools, no git commands. It uses `muxcode-agent-bus log watch "summary"` to record observations to the watch history.

### Tool Specialists (deploy, runner, git)

These agents receive requests and execute, but may require more context or confirmation depending on the operation.

### Spawned Agents (temporary)

Any agent can create a temporary spawned agent for one-off tasks. The spawn inherits the base role's agent definition, tool permissions, and prompts but runs with a unique bus identity (`spawn-{id}`).

```bash
# Spawn a research agent for a one-off task
muxcode-agent-bus spawn start research "What does bus/guard.go do?"

# Check status
muxcode-agent-bus spawn list

# Get the result after completion
muxcode-agent-bus spawn result <id>

# Clean up
muxcode-agent-bus spawn clean
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
MUXCODE_OLLAMA_MODEL=qwen2.5-coder:7b  # model (default)
MUXCODE_OLLAMA_URL=http://localhost:11434  # Ollama URL (default)
```

The variable format is `MUXCODE_{ROLE}_CLI=local` where `{ROLE}` is the uppercase role name (e.g. `GIT` for the git/commit agent, `BUILD` for the build agent).

### How it works

1. `muxcode-agent.sh` checks `MUXCODE_{ROLE}_CLI` for the role
2. If `"local"`, verifies Ollama is reachable (`GET /api/tags`)
3. If reachable: runs `muxcode-agent-bus agent run <role>` instead of Claude Code
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
muxcode-agent-bus agent run <role> [--model MODEL] [--url URL]
```

See [Agent Bus CLI](agent-bus.md#muxcode-agent-bus-agent) for full reference.

## Message Bus Protocol

All agents share the same bus protocol:

```bash
# Check inbox
muxcode-agent-bus inbox

# Send a message
muxcode-agent-bus send <to> <action> "<message>"

# Reply to a request
muxcode-agent-bus send <from> <action> "<result>" --type response --reply-to <id>

# Read memory
muxcode-agent-bus memory context

# Save learnings
muxcode-agent-bus memory write "<section>" "<text>"

# Search memory
muxcode-agent-bus memory search "<query>" [--role ROLE] [--limit N]

# List all memory sections
muxcode-agent-bus memory list [--role ROLE]
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
   MUXCODE_WINDOWS="edit build test review deploy run commit analyze docs status"
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

5. Add a case to `agent_name()` in `scripts/muxcode-agent.sh` to map the role to its agent filename. Optionally add a case to `allowed_tools()` to scope the agent's Bash permissions.

### Agent Permissions

Agents have scoped Bash permissions for autonomous operation. The default permissions per role are defined in `muxcode-agent.sh`:

- **build**: `./build.sh`, `make`, `go build`, `pnpm build`, `cargo build`
- **test**: `./test.sh`, `go test`, `jest`, `pytest`, `cargo test`
- **review**: `git diff`, `git log`, `git status`, `git show` (read-only git)
- **git**: `git *`, `gh *` (all git and GitHub CLI subcommands)
- **deploy**: `cdk`, `terraform`, `pulumi`, `aws`, `sam`, `curl`, `wget`, `./build.sh`, `make`, read-only git, `Write`, `Edit`
- **runner**: unrestricted (no `--allowedTools` filter)
- **analyst**: bus commands + Read, Glob, Grep (no shell commands)
- **watch**: `tail`, `journalctl`, `aws logs`, `kubectl logs`, `docker logs`, `stern`, `ssh`, `lnav` (read-only log tools)
- **pr-read**: `gh pr view/checks/diff/review/list/status`, `gh api`, `git diff/log/status/show/blame/rev-parse/branch`, `jq` (read-only: scoped gh + git, no Write/Edit)

All agents have access to `muxcode-agent-bus` commands.

## Memory

Per-project memory is stored in `.muxcode/memory/`:

```
.muxcode/memory/
├── shared.md     # Cross-agent learnings
├── edit.md       # Edit agent learnings
├── build.md      # Build agent learnings
└── ...
```

Agents read memory with `muxcode-agent-bus memory context` and write with `muxcode-agent-bus memory write "<section>" "<text>"`. To find specific learnings, use `muxcode-agent-bus memory search "<query>"` (keyword search with relevance scoring) or `muxcode-agent-bus memory list` to see all sections.

## Tool profiles

Per-role tool permissions defined in `bus/profile.go`. Each profile specifies which `--allowedTools` patterns the agent receives.

| Component | Description |
|-----------|-------------|
| `Include` | Shared tool groups to inherit (`bus`, `readonly`, `common`) |
| `CdPrefix` | Auto-generate `cd <dir> &&` variants of commands |
| `Tools` | Role-specific `--allowedTools` patterns |

Shared groups:

- `bus` — `Bash(muxcode-agent-bus *)` and bus CLI commands
- `readonly` — `Read`, `Glob`, `Grep`
- `common` — `ls`, `cat`, `diff`, `sed`, `awk`, etc.

CLI: `muxcode-agent-bus tools <role>` — resolves includes, applies CdPrefix, outputs one pattern per line. Patterns use Claude Code `--allowedTools` glob syntax (e.g. `Bash(git diff*)`).

**Process substitution**: `Bash(diff *)` does NOT match `diff <(...)` — Claude Code treats `<()` as a special construct requiring explicit `Bash(diff <(*)`.

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

Standalone binary (`muxcode-llm-harness`) that replaces `muxcode-agent-bus agent run` for local LLM roles. Solves the inbox-loop problem where small LLMs repeatedly call `muxcode-agent-bus inbox` instead of executing tasks.

| Feature | Description |
|---------|-------------|
| Tool call filtering | Blocks inbox commands, self-sends, and repetitive commands before they reach the executor |
| Structured task format | Inbox messages pre-consumed and formatted as structured markdown tasks |
| Corrective feedback | Blocked tool calls receive explanatory messages |
| Loop prevention | Command hash tracking, blocks same command after 3 repetitions |
| Role examples | `RoleExamples()` provides concrete tool call examples per role |

CLI: `muxcode-llm-harness run <role> [--model MODEL] [--url URL] [--max-turns N]`

Separate Go module at `tools/muxcode-llm-harness/` — stdlib only, no external deps. The launcher (`muxcode-agent.sh`) prefers the harness binary when available, falls back to `muxcode-agent-bus agent run`.

Core code: `harness/` package — `config.go`, `ollama.go`, `bus.go`, `tools.go`, `executor.go`, `filter.go`, `prompt.go`, `loop.go`, `message.go`.
