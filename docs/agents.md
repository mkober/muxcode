# Agents

## Overview

Each muxcoder window runs an AI agent with a specific role. Agent behavior is defined by markdown files that serve as system prompts.

## Agent File Resolution

When `muxcoder-agent.sh` launches an agent, it searches for the agent definition in this order:

1. `.claude/agents/<name>.md` — project-local (highest priority)
2. `~/.config/muxcoder/agents/<name>.md` — user global
3. `<install-dir>/agents/<name>.md` — muxcoder defaults

If no agent file is found, a built-in inline prompt is used as fallback.

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

## Agent Categories

### Orchestrator (edit)

The edit agent is the primary user-facing agent. It **never** runs build, test, deploy, or commit commands directly. Instead, it delegates via the message bus:

```bash
muxcoder-agent-bus send build build "Run ./build.sh and report results"
muxcoder-agent-bus send test test "Run tests and report results"
muxcoder-agent-bus send review review "Review the latest changes"
```

### Autonomous Specialists (build, test, review, analyst)

These agents operate autonomously — they receive requests, execute unconditionally, and reply. They never ask for permission before acting.

**Sequence:**
1. Read inbox: `muxcoder-agent-bus inbox`
2. Execute their command
3. Reply to requester: `muxcoder-agent-bus send <from> <action> "<result>" --type response --reply-to <id>`

### Tool Specialists (deploy, runner, git)

These agents receive requests and execute, but may require more context or confirmation depending on the operation.

## Message Bus Protocol

All agents share the same bus protocol:

```bash
# Check inbox
muxcoder-agent-bus inbox

# Send a message
muxcoder-agent-bus send <to> <action> "<message>"

# Reply to a request
muxcoder-agent-bus send <from> <action> "<result>" --type response --reply-to <id>

# Read memory
muxcoder-agent-bus memory context

# Save learnings
muxcoder-agent-bus memory write "<section>" "<text>"
```

## Customization

### Override an Agent

Create a custom agent file in your project:

```bash
mkdir -p .claude/agents
cp ~/.config/muxcoder/agents/code-builder.md .claude/agents/code-builder.md
# Edit to add project-specific instructions
```

### Add a New Role

1. Add the window to your config:
   ```bash
   MUXCODER_WINDOWS="edit build test review deploy run commit analyze docs status"
   ```

2. Add a role mapping if window name differs from role:
   ```bash
   MUXCODER_ROLE_MAP="run=runner commit=git analyze=analyst docs=documentor"
   ```

3. Add the role to known roles:
   ```bash
   MUXCODER_ROLES="documentor"
   ```

4. Create an agent definition:
   ```bash
   # ~/.config/muxcoder/agents/repo-documentor.md
   ```

5. Add to agent name mapping in `muxcoder-agent.sh` if using the three-tier search.

### Agent Permissions

Agents have scoped Bash permissions for autonomous operation. The default permissions per role are defined in `muxcoder-agent.sh`:

- **build**: `./build.sh`, `make`, `go build`, `pnpm build`, `cargo build`
- **test**: `./test.sh`, `go test`, `jest`, `pytest`, `cargo test`
- **review**: `git diff`, `git log`, `git status`, `git show`
- **analyst**: bus commands only

All agents have access to `muxcoder-agent-bus` commands.

## Memory

Per-project memory is stored in `.muxcoder/memory/`:

```
.muxcoder/memory/
├── shared.md     # Cross-agent learnings
├── edit.md       # Edit agent learnings
├── build.md      # Build agent learnings
└── ...
```

Agents read memory with `muxcoder-agent-bus memory context` and write with `muxcoder-agent-bus memory write "<section>" "<text>"`.
