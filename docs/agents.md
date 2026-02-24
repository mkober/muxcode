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
