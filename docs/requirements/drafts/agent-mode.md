# Agent mode

A fully autonomous **agent** that shares the F2 window with the edit agent. Pressing F2 cycles through the agents registered to the F2 window — Edit (nvim + edit agent) → Agent (console + autonomous agent) → Edit → ... All agents persist across cycles — nvim preserves its session, each agent keeps its Claude conversation. The autonomous agent reads Jira stories from the user's todo queue, creates requirements docs, opens PRs for review, implements stories, and submits completed PRs — all without user intervention. It independently delegates to build, test, review, deploy, run, watch, and commit agents.

## Context

### Current state

| Aspect | Current behavior |
|--------|-----------------|
| F2 window | `edit` — permanent, created at launch |
| Layout | Split-left: nvim editor (left, pane 0) + Claude Code agent (right, pane 1) |
| Left pane | Neovim with `NVIM_APPNAME=muxcode` |
| F2 cycle | None — F2 is always the editor (single agent) |
| Orchestration | Edit agent delegates manually — user initiates each delegation |
| Jira integration | `bus/atlassian.go` supports read/update/comment/search/transitions/subtasks |
| PR workflow | Commit agent handles git/gh operations on request |

### Problem

The edit agent requires user initiation for every task — the user must read Jira stories, decide what to work on, create branches, write requirements, open PRs, and drive the implementation workflow. There's no way to hand off an entire story lifecycle (from Jira todo → requirements → implementation → PR) to an autonomous agent. The user is the bottleneck in an otherwise automated pipeline.

### Goal

A separate autonomous agent with its own persistent Claude session, accessible by cycling F2, that operates a complete story lifecycle without user intervention. Pressing F2 when already on the F2 window cycles through registered agents (edit → agent → edit → ...). The agent polls Jira for assigned todo stories, creates requirements docs and review PRs, implements approved requirements, and submits completed PRs — delegating freely to all specialist agents (build, test, review, deploy, run, watch, commit). All agent sessions survive cycles so context is never lost. The cycling mechanism is generic — future agents (e.g. design-mode) can register to the same window and F2 cycles through all of them.

## Competitive analysis

### Claude Code autonomous features (native)

Claude Code now ships several autonomous capabilities that overlap with this spec:

| Feature | What it does | Overlap with agent-mode |
|---------|-------------|------------------------|
| **Headless mode** (`-p` + `--allowedTools`) | Fully unattended execution — accepts prompt, runs tools, exits | Could run the autonomous agent as a headless session instead of interactive |
| **`auto` permission mode** | No prompts; background safety classifier blocks dangerous ops | Alternative to `--dangerously-skip-permissions` with guardrails |
| **Built-in subagents** (`Agent` tool) | Spawn subagents with custom prompts, tools, permissions; run in parallel | Agent could spawn subagents instead of delegating via bus |
| **Agent Teams** (experimental) | Lead session spawns independent teammates; shared task list with self-claiming; direct mailbox communication; `plan approval` gate; per-teammate model selection | Directly competes with muxcode's bus-based multi-agent coordination |
| **`/loop`** | Recurring prompt execution, self-pacing or fixed interval | Could replace custom PR polling with `/loop` |
| **CronCreate** | Cron-scheduled recurring tasks within session | Could schedule Jira polling instead of custom loop |
| **RemoteTrigger / webhooks** | Event-driven autonomous runs | Could trigger story processing from external events |
| **Hooks** (`PreToolUse`, `PostToolUse`, etc.) | 20+ lifecycle hooks including `SubagentStart`, `TaskCreated`, `TeammateIdle` | Already used by muxcode; agent-mode could leverage team hooks |
| **Checkpoints / `/rewind`** | Auto-save state before changes; restore code, conversation, or both | Safety net the spec doesn't account for |
| **Git worktrees** | Parallel independent sessions on isolated branches | Could isolate story work without custom branch management |
| **Agent SDK** (Python + TypeScript) | Programmatic subagent spawning, tool access, permission framework | Alternative to bus-based orchestration for CI/headless |

### OpenCode autonomous features

| Feature | What it does | Overlap with agent-mode |
|---------|-------------|------------------------|
| **Built-in agents** (Build, Plan) | Primary agents with full/read-only tool access; Tab to switch | Simpler version of F2 cycling between agents |
| **Subagents** (General, Explore) | Auto-spawned by primary agents; parent-child session navigation | Similar to muxcode spawn system |
| **Headless mode** (`opencode run`) | Non-interactive with `--agent`, `--model`, `--session`, `--continue` | `--continue` enables persistent autonomous sessions |
| **Headless server** (`opencode serve`) | HTTP API for programmatic agent control | Could drive autonomous agent via API instead of tmux |
| **Plugin system** (JS/TS) | `tool.execute.before/after`, `session.idle` events | No hook chains, but extensible |
| **MCP servers** | Local + remote MCP with OAuth; per-agent tool config | Could connect to Jira via MCP instead of custom `bus/atlassian.go` |
| **Auto-compact** | Summarizes context at limit; `compaction.autocontinue` hook | Already handled by muxcode daemon |
| **Max iteration steps** | Configurable per agent | Similar to `MUXCODE_AGENT_MAX_ITERATIONS` |
| **GitHub integration** | Responds to issues and PR comments | Could replace custom PR polling |

### OpenClaw autonomous features

[OpenClaw](https://openclaw.ai/) (formerly Clawdbot/Moltbot) is an open-source autonomous AI agent platform (100K+ GitHub stars, 2026) that runs on your own devices and communicates via messaging platforms. Originally by Peter Steinberger, it was renamed from Clawdbot after Anthropic trademark complaints. Unlike muxcode (terminal-native, coding-focused), OpenClaw is a general-purpose life assistant that can also code — but its autonomous architecture has significant overlap with agent-mode.

| Feature | What it does | Overlap with agent-mode |
|---------|-------------|------------------------|
| **Heartbeat daemon** | Wakes agent every 30 min to assess state and execute scheduled tasks; proactive, not just reactive | Directly analogous to agent-mode's Jira polling loop — both make agents proactive |
| **HEARTBEAT.md** | Natural-language scheduling ("Every Monday at 9 AM, summarize unread emails") instead of cron syntax | More ergonomic than `MUXCODE_AGENT_JQL` env var; agent-mode could adopt natural-language task definitions |
| **Cron tool** | Built-in cron scheduling as a first-class agent tool (`group:automation`) | Similar to muxcode daemon's cron support, but agent-accessible |
| **Sessions** (`sessions_spawn`, `sessions_send`) | Spawn sub-sessions, send messages between sessions, list/status sessions | Directly analogous to muxcode's bus send/inbox + spawn system |
| **Skills** (SKILL.md) | Modular markdown files with YAML frontmatter injected into prompts; shared via ClawHub registry | Same pattern as muxcode's `skills/*.md` with YAML frontmatter and role-based resolution |
| **Coding agent skill** | Orchestrates Claude Code, Codex, and OpenCode as sub-tools; persistent memory, task queuing | The exact use case agent-mode targets — OpenClaw already does multi-CLI orchestration |
| **Tool groups** | Typed tool access: `group:runtime` (exec, bash), `group:fs` (read, write, edit), `group:sessions`, `group:memory`, `group:web` | Similar to muxcode's tool profile groups (`bus`, `readonly`, `common`) |
| **Tool policies** | Global allow/deny via `openclaw.json`; per-agent and sandbox-level policies; `group:*` expansion | Same concept as muxcode's `bus/profile.go` tool profiles |
| **Multi-agent kit** | Community templates for deploying agent teams with shared context, bot-to-bot communication | Agent-mode's bus delegation model serves the same purpose |
| **Nodes** | Multi-device coordination — discover paired nodes, send notifications, capture screen | No muxcode equivalent; muxcode is single-machine |
| **Browser + Canvas** | Built-in browser control and visual workspace | No muxcode equivalent; muxcode is terminal-only |
| **Memory** (`memory_search`, `memory_get`) | Built-in memory tools for agent context persistence | Same as muxcode's `muxcode memory context/write/search` |
| **Gateway** (`group:automation`) | Webhook/API gateway for external event triggers | Similar to muxcode's webhook support (`bus/webhook.go`) |

**Key insight**: OpenClaw's coding-agent skill already orchestrates Claude Code + Codex + OpenCode as sub-tools with persistent memory and task queuing. This is remarkably close to what agent-mode proposes — the difference is that OpenClaw runs as a messaging-platform agent (Telegram, Slack, etc.) while muxcode agent-mode runs as a tmux-native terminal agent with visual pane management.

**Architecture comparison**:

| Aspect | OpenClaw | Muxcode agent-mode |
|--------|----------|-------------------|
| Interface | Messaging platforms (Telegram, Slack, Discord, etc.) | tmux terminal with F2 cycling |
| Scheduling | Heartbeat daemon (30 min) + HEARTBEAT.md (natural language) | Custom Jira polling loop + env var config |
| Multi-agent | Sessions (spawn/send) + community multi-agent kit | Bus protocol (send/inbox) + specialist agents |
| Coding orchestration | Coding-agent skill wrapping Claude Code/Codex/OpenCode | Direct Claude Code agent with bus delegation |
| Tool control | `openclaw.json` policies with group expansion | `bus/profile.go` tool profiles with includes |
| Skills | SKILL.md files + ClawHub registry | `skills/*.md` files with role-based resolution |
| Memory | Built-in memory tools | `muxcode memory` commands |
| Visibility | Chat messages in messaging platform | Dracula-themed console log in tmux pane |
| Self-hosted | Yes (any device, any OS) | Yes (macOS/Linux, tmux required) |

### Key gaps in the spec

Based on this analysis, the spec should address:

1. **Agent Teams overlap**: Claude Code's experimental Agent Teams feature (`CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=1`) provides native multi-agent coordination with shared task lists, direct mailbox communication, and plan approval gates. The spec should explain why muxcode's bus-based delegation is preferred over Agent Teams (answer: muxcode's bus predates Agent Teams, supports mixed providers including local LLMs, and provides deterministic hook-driven chains that Agent Teams doesn't).

2. **`/loop` for polling**: The spec proposes custom PR polling with `MUXCODE_AGENT_PR_POLL_INTERVAL`. Claude Code's `/loop` or `CronCreate` could handle this natively — the spec should evaluate whether to use native polling or custom state files.

3. **`auto` permission mode**: The spec assumes `--dangerously-skip-permissions` but Claude Code's `auto` mode provides guardrails (safety classifier, category-based blocking) that align with the spec's safety guardrails section. The spec should consider `auto` mode as the default for the autonomous agent.

4. **Git worktrees for isolation**: Claude Code supports `EnterWorktree` for branch isolation. The spec's branch management (create feature branch → implement → PR) could leverage worktrees to avoid polluting the main working tree while the edit agent is active.

5. **Checkpoints as safety net**: The spec's safety guardrails don't mention Claude Code's checkpoint/rewind capability, which could provide rollback on failed story implementations.

6. **Headless vs interactive**: The spec assumes an interactive Claude Code session in a tmux pane. A headless session (`claude -p`) with `/loop` could be simpler — no tmux pane management, no idle detection, no send-keys. The trade-off is losing the interactive console view.

7. **MCP for Jira**: OpenCode's MCP server support could connect to Jira via a Jira MCP server (e.g. Atlassian's official MCP) instead of the custom `bus/atlassian.go` REST wrapper. This is relevant if the agent ever runs on OpenCode instead of Claude Code.

8. **OpenClaw's heartbeat model**: The spec's Jira polling is a rigid env-var-driven loop. OpenClaw's HEARTBEAT.md approach (natural-language scheduling like "Every 30 minutes, check Jira for new assigned stories") is more flexible and user-friendly. The spec should consider whether the autonomous agent's polling behavior could be configured via a natural-language task file rather than `MUXCODE_AGENT_JQL` + `MUXCODE_AGENT_PR_POLL_INTERVAL` env vars.

9. **OpenClaw's coding-agent skill pattern**: OpenClaw's coding-agent skill already orchestrates Claude Code + Codex + OpenCode as sub-tools with persistent memory and task queuing. The spec should acknowledge this prior art and clarify what muxcode adds beyond it — specifically: tmux-native visual panes, F2 cycling, deterministic hook chains, and deep integration with the existing bus/specialist agent ecosystem.

### Differentiation — what agent-mode adds beyond native tools

| Capability | Claude Code native | OpenCode | OpenClaw | Agent-mode adds |
|------------|-------------------|----------|----------|-----------------|
| Jira → requirements → PR lifecycle | Not available | Not available | Not available (general-purpose) | Full story lifecycle automation |
| Multi-provider agent coordination | Agent Teams (Claude-only) | Single-provider per session | Multi-LLM via skills | Bus supports Claude, OpenCode, Codex, local LLM in same session |
| Deterministic build→test→review chains | Hooks (Claude-only) | No hooks | No hooks | Hook chains + graceful degradation for non-hook providers |
| Visual activity console | Not available | Not available | Chat messages in Telegram/Slack | Dracula-themed tmux console with delegation tracking |
| Agent cycling / switching | Not available | Tab (Build/Plan) | Not applicable (messaging) | F2 cycling with tmux pane persistence, extensible to N agents |
| Safety guardrails (story-level) | `auto` mode (tool-level) | Max iteration steps | Heartbeat interval | Story-level limits: max stories, max iterations, pause on failure |
| Jira status transitions | Not available | Not available | Not available | Automatic To Do → In Progress → Done |
| Requirements review gate | Agent Teams `plan approval` (experimental) | Not available | Not available | PR-based review gate with human approval |
| Proactive scheduling | CronCreate (cron syntax) | Not available | HEARTBEAT.md (natural language) | Env var config (rigid but simple) |
| Coding sub-tool orchestration | Subagents (same provider) | Not available | Coding-agent skill (Claude+Codex+OpenCode) | Bus delegation to persistent specialist agents |
| Terminal-native visibility | CLI output | TUI | Messaging platform | tmux panes with live console, F2 cycling, nvim integration |

### Recommendations

1. **Use `auto` permission mode** instead of `--dangerously-skip-permissions` for the autonomous agent — it provides tool-level safety classification while allowing autonomous operation.
2. **Evaluate `/loop` for PR polling** instead of custom polling state — reduces implementation scope in Phase 4-5.
3. **Document the Agent Teams trade-off** — explain that muxcode's bus is preferred because it supports mixed providers, deterministic chains, and doesn't require the experimental flag.
4. **Consider worktrees** for story isolation — prevents the autonomous agent's branch work from conflicting with the edit agent's working tree.
5. **Add checkpoint awareness** — the agent should leverage Claude Code checkpoints before risky operations (large refactors, multi-file changes).
6. **Consider natural-language scheduling** (inspired by OpenClaw's HEARTBEAT.md) — a `HEARTBEAT.md` or `AGENT.md` file with natural-language task definitions ("Check Jira for new assigned stories every 30 minutes") would be more user-friendly than env vars. This could be a Phase 7 enhancement.
7. **Acknowledge prior art** — the spec should note that OpenClaw's coding-agent skill already orchestrates multiple AI CLIs. Muxcode's differentiation is terminal-native pane management, deterministic hook chains, and deep bus integration — not the concept of autonomous coding orchestration itself.

### Adopted from OpenClaw

After evaluating OpenClaw's feature set, these features are worth adopting into agent-mode:

#### 1. Natural-language task file (from HEARTBEAT.md)

**Problem**: The spec configures autonomous behavior via 6+ env vars (`MUXCODE_AGENT_JQL`, `MUXCODE_AGENT_PR_POLL_INTERVAL`, `MUXCODE_AGENT_MAX_STORIES`, etc.). This is rigid and requires restarting the session to change behavior.

**Adopt**: A `TASKS.md` file (project-local `.muxcode/agent-tasks.md` or user-global `~/.config/muxcode/agent-tasks.md`) with natural-language task definitions that the agent reads at startup and on each heartbeat cycle:

```markdown
# Agent tasks

## Story pipeline
- Every 30 minutes, check Jira for stories assigned to me in "To Do" status
- Prioritize stories by Jira priority, then by sprint
- Maximum 5 stories per session
- Maximum 10 build/test/fix iterations per story

## PR review polling
- Check PR approval status every 2 minutes
- After 1 hour with no review, notify user and move to next story

## Guardrails
- Never work on stories not assigned to me
- Always create requirements PR before implementation
- Pause after 3 consecutive failures and alert user
```

The agent reads this file as part of its system prompt. Changes take effect on the next heartbeat cycle — no restart needed. Env vars remain as overrides for CI/scripted usage. This replaces the rigid env var configuration in the spec's Configuration section while keeping the same defaults.

**Implementation**: read `TASKS.md` in the agent's `--append-system-prompt` assembly (same path as skills and context files). Add to Phase 2.

#### 2. Proactive heartbeat model (from heartbeat daemon)

**Problem**: The spec's story lifecycle is a simple sequential loop (poll → process → poll). Between stories, the agent is idle. There's no mechanism for the agent to proactively check on things besides the next Jira story — e.g., checking if a PR got approved while working on something else, or noticing a new high-priority story was assigned mid-implementation.

**Adopt**: A heartbeat tick that fires between story phases, not just between stories. The daemon already polls every 5 seconds for inbox messages — extend this to fire a `heartbeat` action to the agent at a configurable interval (default 30 minutes, via `MUXCODE_AGENT_HEARTBEAT` or `TASKS.md`).

On each heartbeat, the agent:
1. Checks for higher-priority stories that were assigned since the last check
2. Checks PR status on any open PRs (not just the one it's actively waiting on)
3. Checks if any delegated tasks have been waiting too long without response
4. Reports status to the console log

This is lighter than OpenClaw's full heartbeat (which wakes the agent from scratch) because the muxcode agent is already running — the heartbeat is just an interrupt that triggers a status check.

**Implementation**: add heartbeat trigger to daemon's `checkIdleAgents()` loop. New state file: `agent-last-heartbeat`. Add to Phase 5.

#### 3. Session status visibility (from `session_status`)

**Problem**: The spec's console log shows activity as it happens, but there's no summary view of the agent's current state — what story it's working on, what phase it's in, how many stories are done, what's blocked.

**Adopt**: A status summary at the top of the console view and accessible via CLI:

```
┌─ Agent Status ──────────────────────────────────────┐
│ Story: PROJ-123 Implement user authentication       │
│ Phase: Implementation (iteration 3/10)              │
│ PR: #47 APPROVED ✓ | CI: PENDING                   │
│ Stories: 2 done, 1 in progress, 2 remaining         │
│ Uptime: 1h 23m | Last heartbeat: 2m ago            │
├─────────────────────────────────────────────────────┤
│ 14:28:37  → test: run tests                         │
│ 14:28:55  ← test: 42 passed, 0 failed               │
│ ...                                                  │
└─────────────────────────────────────────────────────┘
```

CLI: `muxcode agent-mode status` — prints the same summary to stdout for scripted queries.

**Implementation**: extend console viewer header in Phase 6. Status data comes from existing state files (`agent-current-story`, `agent-phase`, `agent-stories-done`).

#### 4. Skill-based task definitions (from SKILL.md pattern)

**Problem**: The spec hardcodes the story lifecycle (Jira → requirements → PR → implement → PR → done). Users can't customize the workflow without editing the agent definition.

**Adopt**: The story lifecycle as a **skill** rather than hardcoded behavior. The autonomous agent loads a `story-lifecycle` skill that defines the workflow steps. Users can override the skill to customize the pipeline — e.g., skip the requirements PR step, add a deploy phase, or change the PR review gate to Slack-based approval.

```markdown
# skills/story-lifecycle.md
---
name: story-lifecycle
description: Standard Jira story lifecycle for autonomous agent
roles: [agent]
tags: [workflow, jira]
---

## Story lifecycle

When processing a Jira story, follow these phases in order:
1. Create feature branch via commit agent
2. Write requirements doc in docs/requirements/drafts/
3. Open requirements PR for review
4. Wait for PR approval (handle feedback)
5. Implement based on approved requirements
6. Delegate to build → test → review chain
7. Open implementation PR
8. Wait for PR approval (handle feedback)
9. Transition Jira to Done, move requirements to completed
```

Users override by creating `.muxcode/skills/story-lifecycle.md` with their custom workflow. The existing 3-tier skill resolution handles precedence.

**Implementation**: extract lifecycle from agent definition into a skill file. Add to Phase 2 alongside the agent definition work.

#### Features evaluated but not adopted

| OpenClaw feature | Why not |
|-----------------|---------|
| **Nodes (multi-device)** | Muxcode is single-machine by design — tmux sessions don't span devices |
| **Browser + Canvas** | Muxcode is terminal-only; browser automation is out of scope |
| **Messaging platform interface** | Muxcode's value is terminal-native with tmux panes; messaging adds complexity without benefit for coding workflows |
| **ClawHub skill registry** | Premature — muxcode's skill system is project/user scoped. A registry makes sense at scale but not yet |
| **Gateway (webhook triggers)** | Already exists in muxcode (`bus/webhook.go`) |
| **Memory tools** | Already exists in muxcode (`muxcode memory` commands) |

## Design

### Architecture: agent cycling on F2

The F2 window hosts **multiple agents** that share the window but only one is visible at a time. Pressing F2 cycles through registered agents in order. Each agent has its own left pane + right pane pair, persisted in hidden tmux panes when not active. All agents run concurrently — cycling only changes visibility, not process state.

**Registered agents for F2** (ordered):

| Index | Mode | Left pane | Right pane | Role |
|-------|------|-----------|------------|------|
| 0 | Edit (default) | Neovim | Edit agent | `edit` |
| 1 | Agent | Console log | Autonomous agent | `agent` |

The cycle is extensible — future modes (e.g. design-mode) register at a new index and F2 cycles through all of them.

```
F2 Window — Cycle Index
┌─────────────────────────────────────────────┐
│  [0] Edit mode (default, visible)           │
│  ┌──────────┬──────────────┐                │
│  │  nvim    │  edit agent  │  ← active      │
│  │  pane 0  │  pane 1      │                │
│  └──────────┴──────────────┘                │
│                                             │
│  [1] Agent mode (hidden)                    │
│  ┌──────────┬──────────────┐                │
│  │  console │  agent agent │  ← holding     │
│  │  viewer  │              │    window       │
│  └──────────┴──────────────┘                │
│                                             │
│  F2 press → cycle to index (current+1) % N  │
└─────────────────────────────────────────────┘
```

**Cycle state file**:

```
/tmp/muxcode-bus-{session}/f2-cycle.json
```

```json
{
  "current": 0,
  "agents": [
    {"index": 0, "mode": "edit", "role": "edit", "hold_window": ""},
    {"index": 1, "mode": "agent", "role": "agent", "hold_window": "agent-hold"}
  ]
}
```

Index 0 (edit) has no hold window — it's the default owner of the F2 window panes. All other agents store their panes in a hold window when not visible.

The **agent** is a distinct agent role (`agent`) with its own:
- Persistent Claude Code session (survives cycles)
- Agent definition file (`agents/autonomous-agent.md`)
- Tool profile in `bus/profile.go`
- Bus inbox (receives messages as role `agent`)
- System prompt focused on autonomous story execution and multi-agent delegation
- Left pane: console log viewer showing the agent's activity stream

### Session persistence

All agents and their left panes persist across cycles using tmux pane management:

**Mechanism**: the cycle command uses `swap-pane` or `join-pane`/`break-pane` to move panes between visible and hidden holding windows. All processes (nvim, Claude Code agents, console viewer) continue running — only their visibility changes.

Each non-default agent stores its pane pair in a dedicated holding window:

```
/tmp/muxcode-bus-{session}/agent-hold   # holding window for agent mode panes
```

| Cycle transition | Visible panes | Hidden panes |
|-----------------|---------------|--------------|
| Edit → Agent | console viewer + agent agent | nvim + edit agent (agent-hold) |
| Agent → Edit | nvim + edit agent | console viewer + agent agent (agent-hold) |

**Key constraint**: `swap-pane` swaps individual panes, not pairs. The cycle must swap pane 0 and pane 1 independently, maintaining the left/right split layout in both states.

**Generalized cycle logic**:
1. Read `f2-cycle.json` for current index and agent list
2. Compute next index: `(current + 1) % len(agents)`
3. Swap current agent's panes to its hold window (unless index 0 — edit owns the window)
4. Swap next agent's panes from its hold window to the F2 window (unless index 0)
5. Update `current` in state file
6. Update tmux window name to the active mode name

### F2 cycling

F2 behavior changes from simple window selection to an agent cycle:

```
F2 pressed → is current window F2?
  YES → cycle to next registered agent
  NO  → switch to F2 window (show whichever agent is active)
```

Implementation: F2 keybinding calls `muxcode f2 cycle` when already on the F2 window, otherwise does the normal `select-window -t:2`.

```bash
# In tmux.conf
bind -n F2 if-shell '[ "$(tmux display-message -p "#I")" = "2" ]' \
  'run-shell "muxcode f2 cycle"' \
  'select-window -t:2'
```

**CLI commands**:

| Command | Description |
|---------|-------------|
| `muxcode f2 cycle` | Cycle to next agent on F2 window |
| `muxcode f2 status` | Show current agent, registered agents, cycle index |
| `muxcode f2 switch <mode>` | Jump directly to a specific agent by mode name (e.g. `edit`, `agent`) |
| `muxcode f2 list` | List all registered agents with index and mode name |

### Autonomous agent

The autonomous agent is a full Claude Code agent, not a mode of the edit agent. It has its own personality and toolset optimized for end-to-end story execution.

**Agent definition** (`agents/autonomous-agent.md`):

```yaml
---
description: Autonomous agent — reads Jira stories, creates requirements, implements features, and submits PRs
---
```

**Key behaviors:**
- Polls Jira for stories assigned to the current user in the "To Do" status
- Creates a feature branch for each story (`feature/{JIRA_KEY}-{slug}`)
- Creates a requirements doc in `docs/requirements/drafts/` from the Jira story description
- Opens a PR with the requirements doc for review
- Waits for PR approval before proceeding to implementation
- Implements the story based on the approved requirements
- Delegates freely to build, test, review, deploy, run, watch, and commit agents
- Opens a final PR with the implementation for review
- Updates Jira story status at each transition (To Do → In Progress → In Review → Done)
- Comments on the Jira story with progress updates and PR links
- Handles multiple stories sequentially — completes one before starting the next

**Tool profile** (in `bus/profile.go`):

```go
"agent": {
  Include: []string{"bus", "readonly", "common"},
  Tools: []string{
    "Write(*)", "Edit(*)",
    "Bash(muxcode send *)", "Bash(muxcode inbox *)",
    "Bash(muxcode memory *)", "Bash(muxcode session *)",
    "Bash(muxcode jira *)", "Bash(muxcode confluence *)",
    "Bash(muxcode agent-mode *)",
    "Bash(gh pr view *)", "Bash(gh pr checks *)",
    "Bash(gh pr list *)", "Bash(gh pr status *)",
    "Bash(git branch *)", "Bash(git checkout *)",
    "Bash(git status)", "Bash(git diff *)",
    "Bash(git log *)", "Bash(git rev-parse *)",
  },
},
```

**Note**: The agent has read-only git access for status checks but delegates all write operations (commit, push, PR creation) to the commit agent. It has read-only `gh pr` access to check PR status (approved, changes requested, CI checks) but delegates PR creation to commit.

### Story lifecycle

The agent operates a complete story lifecycle, from Jira todo to merged PR:

```
┌─────────────┐
│  Poll Jira  │  muxcode jira search "assignee = currentUser() AND status = 'To Do'"
│  for todos  │  Pick highest priority story
└──────┬──────┘
       ▼
┌─────────────┐
│  Read story │  muxcode jira read {KEY}
│  details    │  Parse acceptance criteria, description, linked stories
└──────┬──────┘
       ▼
┌─────────────┐
│  Create     │  Delegate to commit: create branch feature/{KEY}-{slug}
│  branch     │  Transition Jira: To Do → In Progress
└──────┬──────┘
       ▼
┌─────────────┐
│  Write      │  Create docs/requirements/drafts/{KEY}-{slug}.md
│  requirements│  Based on Jira story description + acceptance criteria
└──────┬──────┘
       ▼
┌─────────────┐
│  PR for     │  Delegate to commit: stage, commit, push, create PR
│  requirements│  PR title: "Requirements: {KEY} {summary}"
│  review     │  Comment on Jira with PR link
└──────┬──────┘
       ▼
┌─────────────┐
│  Wait for   │  Poll gh pr checks + gh pr view for approval status
│  PR approval│  Handle review feedback: update requirements if needed
└──────┬──────┘
       ▼
┌─────────────┐
│  Implement  │  Read approved requirements
│  story      │  Write code, delegate to build/test/review
│             │  Iterate until build+test pass and review is clean
└──────┬──────┘
       ▼
┌─────────────┐
│  PR for     │  Delegate to commit: stage, commit, push, create PR
│  implementation│  PR title: "{KEY} {summary}"
│  review     │  Comment on Jira with PR link
└──────┬──────┘
       ▼
┌─────────────┐
│  Wait for   │  Poll for approval + CI checks passing
│  PR approval│  Handle review feedback: fix code, re-push
└──────┬──────┘
       ▼
┌─────────────┐
│  Complete   │  Transition Jira: In Progress → Done
│  story      │  Move requirements doc: drafts → completed
│             │  Comment on Jira with completion summary
└──────┬──────┘
       ▼
       Loop back to Poll Jira
```

### Delegation model

The agent delegates to all specialist agents independently. Unlike the edit agent (which delegates on user request), the agent delegates autonomously as part of its workflow:

| Delegation | Target | When |
|------------|--------|------|
| Create branch, commit, push, PR | `commit` | Branch creation, requirements commit, implementation commits |
| Build project | `build` | After code changes, before test |
| Run tests | `test` | After successful build |
| Code review | `review` | After tests pass, before PR |
| Deploy (optional) | `deploy` | When story requires infrastructure changes |
| Run commands | `run` | Ad-hoc commands needed during implementation |
| Watch logs | `watch` | When debugging runtime behavior |
| Documentation | `plan` | When story requires architecture doc updates |

**Delegation pattern** — all delegations use `--wait` for synchronous responses:

```bash
# Create feature branch
muxcode send commit commit "Create and checkout branch feature/PROJ-123-user-auth" --wait

# Build after code changes
muxcode send build build "Run ./build.sh and report results" --wait

# Run tests
muxcode send test test "Run tests and report results" --wait

# Code review
muxcode send review review "Review changes on current branch" --wait

# Create PR
muxcode send commit commit "Stage all changes, commit with message 'PROJ-123: implement user auth', push, and create PR titled 'PROJ-123 Implement user authentication'" --wait
```

### PR review polling

The agent polls for PR approval status at a configurable interval:

```bash
# Check PR status
gh pr view --json state,reviewDecision,statusCheckRollup

# Possible states:
# reviewDecision: APPROVED, CHANGES_REQUESTED, REVIEW_REQUIRED
# statusCheckRollup: SUCCESS, FAILURE, PENDING
```

| PR state | Agent action |
|----------|-------------|
| `REVIEW_REQUIRED` | Wait, poll again after interval |
| `CHANGES_REQUESTED` | Read review comments, address feedback, push updates |
| `APPROVED` + checks `SUCCESS` | Proceed to next phase |
| `APPROVED` + checks `FAILURE` | Fix CI failures, push updates |
| `APPROVED` + checks `PENDING` | Wait for checks to complete |

**Poll interval**: `MUXCODE_AGENT_PR_POLL_INTERVAL`, default `120` seconds (2 minutes).

**Max wait**: `MUXCODE_AGENT_PR_MAX_WAIT`, default `3600` seconds (1 hour). After max wait, the agent alerts the user and moves to the next story.

### Requirements doc format

The agent generates requirements docs following the project's existing spec format:

```markdown
# {JIRA_KEY}: {story summary}

## Context

### Jira story
- **Key**: {KEY}
- **Summary**: {summary}
- **Type**: {type}
- **Priority**: {priority}
- **Assignee**: {assignee}
- **Sprint**: {sprint}

### Description
{Jira story description, reformatted as markdown}

## Requirements

### Acceptance criteria
{Extracted from Jira story, formatted as checkboxes}

- [ ] Criterion 1
- [ ] Criterion 2
- [ ] Criterion 3

### Technical approach
{Agent's analysis of how to implement the story}

### Key files
| File | Purpose |
|------|---------|
| `path/to/file` | Description |

### Dependencies
{Any blockers, linked stories, or prerequisites}

## Implementation

### Phase 1: {description}
- [ ] Step 1
- [ ] Step 2

## Status

Draft
```

### Jira integration

The agent uses existing `bus/atlassian.go` APIs for all Jira operations:

| Operation | API |
|-----------|-----|
| Find assigned stories | `muxcode jira search "assignee = currentUser() AND status = 'To Do' ORDER BY priority DESC"` |
| Read story details | `muxcode jira read {KEY}` |
| Transition status | `muxcode jira transition {KEY} "In Progress"` |
| Add comment | `muxcode jira comment {KEY} "Started work — branch: feature/{KEY}-{slug}"` |
| Link PR | `muxcode jira comment {KEY} "Requirements PR: {url}"` |

**JQL query**: the agent uses a configurable JQL query to find stories. Default: stories assigned to the current user in "To Do" status. Override via `MUXCODE_AGENT_JQL`.

### Console log viewer (left pane)

The left pane in agent mode shows a live activity stream — a console-style log of the agent's actions, delegations, and results. This replaces nvim (which serves no purpose for an autonomous agent).

The console viewer monitors the agent's bus history and renders a Dracula-themed activity log:

```
┌─ Agent Activity ─────────────────────────────────┐
│ 14:23:01  Reading Jira PROJ-123...               │
│ 14:23:03  Story: Implement user authentication   │
│ 14:23:05  → commit: create branch feature/...    │
│ 14:23:08  ← commit: branch created               │
│ 14:23:10  Writing requirements doc...             │
│ 14:23:15  → commit: stage, commit, push, PR      │
│ 14:23:22  ← commit: PR #47 created               │
│ 14:23:22  → jira: comment with PR link            │
│ 14:23:25  Waiting for PR approval... (poll 2m)    │
│ 14:25:30  PR #47: CHANGES_REQUESTED              │
│ 14:25:32  Reading review comments...              │
│ 14:25:40  Updating requirements...                │
│ 14:25:45  → commit: push updates                  │
│ 14:27:50  PR #47: APPROVED ✓                     │
│ 14:27:52  Starting implementation...              │
│ 14:28:10  → build: ./build.sh                     │
│ 14:28:35  ← build: SUCCESS                        │
│ 14:28:37  → test: run tests                       │
│ 14:28:55  ← test: 42 passed, 0 failed             │
└──────────────────────────────────────────────────┘
```

Implementation: `muxcode agent-mode console` — tails the agent's `log.jsonl`, filters for agent-role messages, renders with Dracula colors. Uses the same pattern as the existing console infrastructure (`bus/console.go`).

### Safety guardrails

The autonomous agent operates without user intervention but has safety limits:

| Guardrail | Mechanism |
|-----------|-----------|
| Max stories per session | `MUXCODE_AGENT_MAX_STORIES`, default `5` — prevents runaway |
| Max implementation iterations | `MUXCODE_AGENT_MAX_ITERATIONS`, default `10` — build/test/fix cycles per story |
| PR wait timeout | `MUXCODE_AGENT_PR_MAX_WAIT`, default `3600s` — moves on if PR stalls |
| Branch protection | Never pushes to main — always feature branches |
| Commit delegation | All commits go through commit agent (never direct git write) |
| Story scope | Only processes stories assigned to the configured user |
| Pause on failure | After 3 consecutive story failures, pauses and alerts user |
| No force operations | Never force-pushes, never deletes branches, never resets |

### State management

```
/tmp/muxcode-bus-{session}/f2-cycle.json          # cycle state: current index, registered agents
/tmp/muxcode-bus-{session}/agent-hold              # hidden tmux window for agent panes
/tmp/muxcode-bus-{session}/agent-current-story     # current Jira key being worked
/tmp/muxcode-bus-{session}/agent-phase             # current phase: requirements|implementation|waiting
/tmp/muxcode-bus-{session}/agent-stories-done      # count of completed stories this session
```

`f2-cycle.json` is the shared cycle state for all F2 agents (edit, agent, and any future modes like design). Per-agent state files (current story, phase, stories done) are agent-mode specific. State does not persist across session restarts.

### Mode transitions

**First cycle to Agent (agent not yet launched):**

1. Read `f2-cycle.json` — current index 0 (edit)
2. Create holding window `agent-hold` for edit panes
3. Create agent panes: console viewer (pane 0) + agent agent (pane 1) in holding window
4. Swap edit panes → `agent-hold`, agent panes → F2 window
5. Update `current` to 1 in state file
6. Update tmux window name to `agent`
7. Agent begins autonomous story lifecycle loop

**Subsequent cycle (Edit → Agent, agent already running):**

1. Swap edit panes → `agent-hold`
2. Swap agent panes from `agent-hold` → F2 window
3. Update `current` to 1 in state file
4. Update tmux window name to `agent`

**Cycle (Agent → Edit):**

1. Swap agent panes → `agent-hold`
2. Swap edit panes from `agent-hold` → F2 window
3. Update `current` to 0 in state file
4. Update tmux window name to `edit`
5. Agent continues running in background (hidden pane)

All pane processes continue running throughout cycles. The agent does not pause when hidden — it continues its autonomous workflow.

**Generalized for N agents**: with 3+ agents registered (e.g. edit → agent → design → edit), the cycle command always moves the current agent's panes to its hold window and the next agent's panes to the F2 window. Each agent has its own dedicated hold window. Only one agent is visible at a time.

### Keybindings

| Key | Action |
|-----|--------|
| `F2` | Cycle to next agent when on F2 window, switch to F2 otherwise |
| `prefix + a` | Cycle F2 agents regardless of current window |

### Configuration

| Env var | Default | Description |
|---------|---------|-------------|
| `MUXCODE_AGENT_JQL` | `assignee = currentUser() AND status = 'To Do' ORDER BY priority DESC` | JQL query for finding stories (override for `TASKS.md`) |
| `MUXCODE_AGENT_PR_POLL_INTERVAL` | `120` | Seconds between PR approval checks (override for `TASKS.md`) |
| `MUXCODE_AGENT_PR_MAX_WAIT` | `3600` | Max seconds to wait for PR approval (override for `TASKS.md`) |
| `MUXCODE_AGENT_MAX_STORIES` | `5` | Max stories to process per session (override for `TASKS.md`) |
| `MUXCODE_AGENT_MAX_ITERATIONS` | `10` | Max build/test/fix cycles per story (override for `TASKS.md`) |
| `MUXCODE_AGENT_PAUSE_ON_FAILURE` | `3` | Consecutive story failures before pausing (override for `TASKS.md`) |
| `MUXCODE_AGENT_HEARTBEAT` | `1800` | Seconds between heartbeat ticks (0 to disable) |
| `MUXCODE_AGENT_TASKS` | `.muxcode/agent-tasks.md` | Path to natural-language task definitions file |

### Relationship to edit agent

The agent and edit agents are fully independent:

| Aspect | Edit agent | Autonomous agent |
|--------|-----------|-----------------|
| Role | `edit` | `agent` |
| Claude session | Persistent (survives cycles) | Persistent (survives cycles) |
| System prompt | Code editing, user-directed orchestration | Autonomous story execution, multi-agent delegation |
| Left pane | Neovim | Console activity log |
| Tool profile | Read-only (delegates writes) | Full read/write + delegation |
| Bus inbox | `edit` | `agent` |
| Orchestration | User-initiated delegation | Fully autonomous — Jira-driven |
| Jira access | Via plan agent delegation | Direct (read/transition/comment) |
| Git access | Via commit agent delegation | Read-only + commit agent delegation |
| PR creation | Via commit agent | Via commit agent |
| Active when hidden | Pauses (user-facing) | Continues running (autonomous) |

The agent does **not** replace the edit agent — it complements it. The edit agent remains the user's interactive workspace for hands-on coding. The agent handles stories the user wants to fully delegate. The user can cycle between them freely with F2, checking on the agent's progress or taking over a story in edit mode.

### Console view

The agent's left pane runs a Dracula-themed console viewer (same infrastructure as `bus/console.go`) filtered to show the agent role's activity stream. This provides at-a-glance visibility into what the agent is doing, which story it's working on, and what delegations are in flight.

## Implementation

### Phase 1: F2 cycle infrastructure and persistence

New files:

| File | Purpose |
|------|---------|
| `bus/f2_cycle.go` | Cycle state (JSON read/write), agent registration, pane swap logic, holding window management — generic for all F2 agents |
| `bus/f2_cycle_test.go` | Unit tests for cycle state, index wrapping, agent registration |
| `cmd/f2.go` | CLI subcommand: `muxcode f2 {cycle,status,switch,list}` |
| `agents/autonomous-agent.md` | Agent definition with autonomous story execution prompt |

Updated files:

| File | Change |
|------|--------|
| `main.go` | Add `f2` subcommand dispatch |
| `config/tmux.conf` | F2 cycle keybinding, `prefix + a` keybinding |
| `bus/config.go` | Add `agent` to role lists, `WindowForRole` mapping |
| `bus/profile.go` | Add `agent` tool profile |
| `muxcode.sh` | Initialize `f2-cycle.json` with edit as index 0 + agent as index 1 |

Success criteria:
- [ ] `muxcode f2 cycle` advances to the next registered agent
- [ ] Cycle wraps around: last agent → back to edit (index 0)
- [ ] `muxcode f2 switch agent` jumps directly to agent mode
- [ ] `muxcode f2 list` shows all registered agents with current indicator
- [ ] Agent launches with its own Claude Code session
- [ ] Nvim session preserved across cycles (process stays alive in hidden pane)
- [ ] Agent session preserved across cycles (Claude conversation intact)
- [ ] Edit agent session preserved across cycles (Claude conversation intact)
- [ ] F2 cycle works: same-window cycles agent, other-window switches to F2
- [ ] `muxcode f2 status` shows current agent, cycle index, registered agents
- [ ] `f2-cycle.json` state file is extensible for future agents (design-mode)

### Phase 2: Jira integration, task file, and story lifecycle skill

New files:

| File | Purpose |
|------|---------|
| `skills/story-lifecycle.md` | Story lifecycle workflow as a customizable skill (Jira → requirements → PR → implement → PR → done) |

Updated files:

| File | Change |
|------|--------|
| `agents/autonomous-agent.md` | Add Jira polling workflow, story selection logic, TASKS.md reading |
| `scripts/muxcode-agent.sh` | Read `TASKS.md` into `--append-system-prompt` for agent role |

Success criteria:
- [ ] Agent reads `.muxcode/agent-tasks.md` (or `MUXCODE_AGENT_TASKS` path) for natural-language task config
- [ ] TASKS.md values are used as defaults; env vars override when set
- [ ] `story-lifecycle` skill loaded for agent role — defines the workflow phases
- [ ] Users can override `story-lifecycle` skill via `.muxcode/skills/story-lifecycle.md`
- [ ] Agent polls Jira for assigned todo stories using configurable JQL
- [ ] Agent reads story details (description, acceptance criteria, priority, linked stories)
- [ ] Agent selects highest priority story from results
- [ ] Agent transitions Jira status: To Do → In Progress
- [ ] Agent comments on Jira story with work-started message and branch name
- [ ] Configurable JQL query via `MUXCODE_AGENT_JQL` or TASKS.md

### Phase 3: requirements workflow

Success criteria:
- [ ] Agent creates feature branch via commit agent delegation
- [ ] Agent generates requirements doc in `docs/requirements/drafts/{KEY}-{slug}.md`
- [ ] Requirements doc includes Jira context, acceptance criteria, technical approach
- [ ] Agent commits and pushes requirements via commit agent
- [ ] Agent creates PR for requirements review via commit agent
- [ ] Agent comments on Jira story with requirements PR link
- [ ] Agent polls for PR approval status
- [ ] Agent handles PR review feedback (updates requirements, re-pushes)
- [ ] Agent proceeds to implementation only after PR approval

### Phase 4: implementation workflow

Success criteria:
- [ ] Agent reads approved requirements doc as implementation guide
- [ ] Agent implements code changes based on requirements
- [ ] Agent delegates to build agent after code changes
- [ ] Agent delegates to test agent after successful build
- [ ] Agent delegates to review agent after tests pass
- [ ] Agent iterates on build/test/review failures (fix → rebuild → retest)
- [ ] Max iteration limit (`MUXCODE_AGENT_MAX_ITERATIONS`) prevents runaway loops
- [ ] Agent creates implementation PR via commit agent
- [ ] Agent comments on Jira with implementation PR link
- [ ] Agent polls for PR approval and CI status
- [ ] Agent handles review feedback on implementation PR

### Phase 5: story completion, heartbeat, and lifecycle

Updated files:

| File | Change |
|------|--------|
| `watcher/watcher.go` | Add heartbeat trigger for agent role at configurable interval |
| `bus/config.go` | Add `agent-last-heartbeat` state file path |

Success criteria:
- [ ] Agent transitions Jira status to Done after PR approval
- [ ] Agent moves requirements doc from `drafts/` to `completed/`
- [ ] Agent comments on Jira with completion summary
- [ ] Agent loops back to poll for next todo story
- [ ] Max stories per session limit (`MUXCODE_AGENT_MAX_STORIES`) enforced
- [ ] Pause on consecutive failures (`MUXCODE_AGENT_PAUSE_ON_FAILURE`) works
- [ ] Agent continues working when hidden (cycled to edit mode)
- [ ] Daemon fires `heartbeat` action to agent inbox at `MUXCODE_AGENT_HEARTBEAT` interval (default 30 min)
- [ ] On heartbeat, agent checks for higher-priority stories assigned since last check
- [ ] On heartbeat, agent checks PR status on all open PRs (not just active one)
- [ ] On heartbeat, agent checks for stale delegations (waiting too long without response)
- [ ] Heartbeat can be disabled (`MUXCODE_AGENT_HEARTBEAT=0`)

### Phase 6: console log viewer with status header

New files:

| File | Purpose |
|------|---------|
| `scripts/agent-console.sh` | Console viewer script for left pane |
| `cmd/agent_mode.go` | CLI: `muxcode agent-mode {status,console}` |

Updated files:

| File | Change |
|------|--------|
| `bus/console.go` | Add `agent` role renderer with status header for console view |

Success criteria:
- [ ] Left pane shows live activity stream with Dracula theme
- [ ] **Status header** at top of console: current story, phase, iteration count, PR status, stories done/remaining, uptime, last heartbeat
- [ ] `muxcode agent-mode status` prints the same status summary to stdout
- [ ] Delegations shown with `→` send and `←` receive indicators
- [ ] Current story and phase displayed in header
- [ ] PR poll status visible (waiting, approved, changes requested)
- [ ] Console auto-scrolls on new activity

### Phase 7: polish and docs

Success criteria:
- [ ] Agent role documented in `docs/agents.md`
- [ ] Architecture docs updated with agent mode
- [ ] Configuration docs updated (env vars, TASKS.md format, heartbeat)
- [ ] `docs/agent-bus.md` updated with `agent-mode` subcommand
- [ ] README updated with agent mode feature
- [ ] Backlog updated to reference this feature

## Dependencies

| Dependency | Purpose | Status |
|------------|---------|--------|
| `bus/atlassian.go` | Jira read/search/transition/comment | Existing |
| `bus/config.go` | Role configuration | Existing (needs update) |
| `bus/profile.go` | Tool profiles | Existing (needs update) |
| `bus/console.go` | Console log viewer infrastructure | Existing (needs renderer) |
| Toggle infrastructure | Pane swap mechanism | Shared with design-mode (backlog) |
| Commit agent | Git operations, PR creation | Existing |
| Build/test/review agents | Implementation feedback loop | Existing |

**Shared infrastructure with design-mode**: the F2 cycle mechanism (`bus/f2_cycle.go`, `cmd/f2.go`, `f2-cycle.json`) is generic — design-mode registers as another agent at index 2 and F2 cycles through all three: edit → agent → design → edit. If design-mode is implemented later, it adds an entry to `f2-cycle.json` and a holding window — no changes to the cycle infrastructure itself.

## Status

Draft
