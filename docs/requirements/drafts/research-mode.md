# Research mode

A persistent **research agent** running **OpenCode with DeepSeek** that shares the F1 window with the plan agent via mode cycling. Pressing F1 when already on the plan window toggles between Plan (nvim + plan agent) and Research (findings console + OpenCode/DeepSeek agent). The research agent is dedicated to **web searching API documentation, platform reference sites, and related open source projects on GitHub**. The left pane console persists research findings as a browsable knowledge base. It has full read access to the current repo for context, operates outside the build→test→review and deploy→run→watch chains, and can delegate implementation work to the active F2 agent when research findings require code changes.

## Context

### Current state

| Aspect | Current behavior |
|--------|-----------------|
| F1 window | `plan` — permanent, created at launch |
| Layout | Split-left: nvim editor (left, pane 0) + plan agent (right, pane 1) |
| Left pane | Neovim with `NVIM_APPNAME=muxcode` |
| F1 binding | `bind -n F1 select-window -t:1` (simple window switch, no cycling) |
| Research role | Hosted role on `edit` — `hostedRoles["research"] = "edit"` |
| Research inbox | Shares edit's inbox — messages to `research` deliver to edit |
| Research window | None — no default window, used only via spawn |
| Mode cycling | Only implemented on F2 (edit window) for edit/auto agents |

### Problem

The edit and auto agents frequently need answers about external APIs, platform services, SDK usage, and open source libraries — questions that require web searching official documentation, reading GitHub repos, and checking changelogs. Currently the edit agent handles this itself or spawns a temporary research agent that loses all context when it exits. There's no persistent agent dedicated to web research that accumulates knowledge across requests — every API doc lookup starts from scratch.

The F1 key is underutilized — it's a simple window switch while F2 already demonstrates that mode cycling on a single key is natural and powerful. The plan window's left pane (nvim) isn't useful for research — the research agent needs web search, API doc fetching, and GitHub exploration, not an editor.

### Goal

A persistent research agent running **OpenCode with DeepSeek**, accessible by toggling F1 when already on the plan window. The research agent specializes in web searching API docs (AWS, CDK, CloudFormation, etc.), platform reference sites (Go stdlib, Node.js, Python), and related open source projects on GitHub — building up a session of accumulated knowledge about the APIs and libraries in use. The left pane console persists research findings as a browsable knowledge base. It delivers faster, more authoritative answers over time because it retains context from prior lookups. The mode cycling infrastructure already exists and generalizes to any window — this spec applies it to the plan window with minimal new code.

## Design

### Architecture: F1 mode cycling

The F1 window hosts **two agents** that share the window, with only one visible at a time. Pressing F1 when already on the plan window toggles between them. The existing mode cycling infrastructure (`bus/mode.go`) handles all pane management — it's already generic per-window via the `--window` flag.

**Registered agents for F1** (ordered):

| Index | Mode | Left pane | Right pane | Role |
|-------|------|-----------|------------|------|
| 0 | Plan (default) | Neovim | Plan agent (Claude Code) | `plan` |
| 1 | Research | Findings console | Research agent (OpenCode/DeepSeek) | `research` |

```
F1 Window — Toggle
┌─────────────────────────────────────────────┐
│  [0] Plan mode (default, visible)           │
│  ┌──────────┬──────────────┐                │
│  │  nvim    │  plan agent  │  ← active      │
│  │  pane 0  │  (Claude)    │                │
│  │          │  pane 1      │                │
│  └──────────┴──────────────┘                │
│                                             │
│  [1] Research mode (hidden)                 │
│  ┌──────────┬──────────────┐                │
│  │  findings│  research    │  ← holding     │
│  │  console │  (OpenCode/  │    window       │
│  │  pane 0  │   DeepSeek)  │                │
│  │          │  pane 1      │                │
│  └──────────┴──────────────┘                │
│                                             │
│  F1 press (on F1) → toggle to other mode    │
└─────────────────────────────────────────────┘
```

**Cycle state file**:

```
/tmp/muxcode-bus-{session}/mode-cycle-plan.json
```

```json
{
  "window": "plan",
  "current": 0,
  "agents": [
    {"index": 0, "mode": "plan", "role": "plan", "hold_window": ""},
    {"index": 1, "mode": "research", "role": "research", "hold_window": "research"}
  ]
}
```

Index 0 (plan) has no hold window — it's the default owner of the plan window panes. The research agent stores its panes in a hold window named `research` when not visible.

### Role migration: hosted → mode

The research role moves from `hostedRoles` to `modeRoles`:

**Before** (current):
```go
// hostedRoles — messages to research deliver to edit's inbox
var hostedRoles = map[string]string{
    "docs":     "plan",
    "research": "edit",
    "pr-read":  "commit",
}
```

**After**:
```go
// hostedRoles — research removed
var hostedRoles = map[string]string{
    "docs":    "plan",
    "pr-read": "commit",
}

// modeRoles — research added with independent inbox
var modeRoles = map[string]string{
    "auto":     "auto",
    "research": "research",
}
```

This gives the research agent its own independent inbox (`inbox/research.jsonl`) instead of sharing edit's inbox. `WindowForRole("research")` returns `"research"` (the hold window name) for pane targeting.

### F1 cycling

F1 behavior changes from simple window selection to a mode toggle (same pattern as F2):

```
F1 pressed → is current window F1 (index 1)?
  YES → muxcode mode cycle --window plan
  NO  → select-window -t:1 (show whichever mode is active)
```

```bash
# In tmux.conf
bind -n F1 if-shell -F '#{==:#{window_index},1}' \
  'run-shell "muxcode mode cycle --window plan >/dev/null 2>&1"' \
  'select-window -t:1'
```

**Additional keybinding** — `prefix + r` toggles plan/research modes regardless of the current window:

```bash
# prefix + r — toggle plan/research modes regardless of current window
bind r run-shell 'muxcode mode cycle --window plan >/dev/null 2>&1'
```

### DefaultPlanModeCycleState

A new default state function for the plan window (alongside the existing `DefaultModeCycleState` for edit):

```go
func DefaultPlanModeCycleState() *ModeCycleState {
    return &ModeCycleState{
        Window:  "plan",
        Current: 0,
        Agents: []ModeAgent{
            {Index: 0, Mode: "plan", Role: "plan", HoldWindow: ""},
            {Index: 1, Mode: "research", Role: "research", HoldWindow: "research"},
        },
    }
}
```

`ReadModeCycleState` fallback: when the file doesn't exist and `window == "plan"`, return `DefaultPlanModeCycleState()` (same pattern as the existing `window == "edit"` fallback).

### Console: research findings log (left pane)

The research agent's left pane runs `muxcode console research` — a Dracula-themed console that **persists research findings** across the session. Unlike ephemeral activity logs, this console accumulates a browsable knowledge base of what was researched, what was found, and where it was sourced.

The console displays:
- Research requests received (from which agent, what topic)
- **Findings**: summarized answers with source URLs (API docs, GitHub repos, platform references)
- **Saved findings**: key facts extracted and saved to memory for cross-session persistence
- Delegations sent (implementation requests to edit/auto)

**Persistence model**: the console reads from `research-history.jsonl` (a dedicated history file, same pattern as `build-history.jsonl`). Each research completion writes an entry with the question, answer summary, source URLs, and timestamp. This history file survives within the session and is visible in the console as a scrollable findings log.

For cross-session persistence, the research agent saves important findings to memory via `muxcode memory write "research" "<finding>"`. The console displays a marker when a finding has been saved to memory.

This requires:
- Adding a `research` entry to `DefaultConsoleConfigs()` in `bus/console.go`
- Adding `research` to `HasConsoleView()` in `bus/launcher.go`
- The research agent writing `research-history.jsonl` entries via `muxcode history`

### Research agent: OpenCode + DeepSeek

The research agent runs on **OpenCode with DeepSeek** — not Claude Code. DeepSeek excels at web search, documentation comprehension, and technical analysis, making it well-suited for the research role's primary job of looking up API docs, platform references, and GitHub projects. This also diversifies the session's model usage — Claude handles orchestration (edit) and code-heavy tasks (build, test, review), while DeepSeek handles external knowledge retrieval.

**Provider configuration**:

```bash
# Set automatically via roleDefaultCLI / RoleOpenCodeModelDefault
MUXCODE_RESEARCH_CLI=opencode
# Model default set in RoleOpenCodeModelDefault("research")
```

The research agent is a non-hook provider — it uses prompt-driven bus messaging (same graceful degradation as other OpenCode agents). `adaptBodyForNonHookProvider()` rewrites hook references in the agent definition. OpenCode's `WriteAgentConfig()` generates `.opencode/agents/research.md` with the translated tool profile.

| Aspect | Before (hosted on edit) | After (F1 mode on plan) |
|--------|------------------------|---------------------|
| CLI | N/A (shared with edit) | OpenCode |
| Model | N/A | DeepSeek (via `RoleOpenCodeModelDefault`) |
| Inbox | Shared with edit | Independent (`inbox/research.jsonl`) |
| Session | Spawned on demand, ephemeral | Persistent, survives F1 toggles |
| Context | Lost between spawns | Accumulated across requests |
| Repo awareness | None (fresh spawn each time) | Full — reads CLAUDE.md, auto-detected project context, codebase |
| Delegation | Edit agent handles research itself | Direct delivery to research agent |
| Edit delegation | N/A | Can delegate implementation to active F2 agent |
| Wake-up | Via edit agent | Via daemon `checkIdleAgents()` / `SendWakeUp()` |
| Visibility | None (spawn window closes) | Research findings console in left pane, toggle via F1 |
| Hooks | N/A | None — non-hook provider, prompt-driven bus messaging |

### Startup and wake-up

The research agent launches lazily — only when F1 is first toggled to research mode (same as the auto agent on F2). The `modeCreateAgent()` function in `mode.go` handles this:

1. Creates the `research` hold window at index 0
2. Runs `muxcode console research` in pane 0
3. Splits horizontally for pane 1
4. Runs `muxcode agent launch research` in pane 1
5. The daemon's `checkIdleAgents()` handles wake-up for inbox messages

`NeedsWakeUp()` does **not** include the research window — the daemon handles wake-up naturally (same as other non-edit agents). Startup messages are not sent to the research agent.

### Repo context

The research agent launches in the project directory (`CdPrefix: true` in tool profile) and has full read access to the repo. It reads `CLAUDE.md` for project conventions, receives auto-detected project context (Go module info, package.json scripts, CDK config, etc. via `AllContextFilesForRole()`), and can explore the codebase with Grep, Glob, Read, and git read commands (`git log`, `git diff`, `git show`, `git blame`, `git status`).

This gives the research agent project awareness — it knows the tech stack, directory structure, and coding conventions. When asked about an API, it can cross-reference the answer against how the project already uses it.

The existing tool profile already supports this:

```go
"research": {
    Include:  []string{"bus", "readonly", "common"},
    CdPrefix: true,
    Tools: []string{
        "WebSearch", "WebFetch",
        "Bash(git diff*)", "Bash(git log*)", "Bash(git show*)",
        "Bash(git status*)", "Bash(git blame*)",
        "Bash(python3*)", "Bash(node*)", "Bash(jq*)",
        "Bash(curl *)", "Bash(gh *)", "Bash(tree *)",
        "Bash(go doc *)", "Bash(go list *)",
        "Bash(pip show*)", "Bash(npm info*)", "Bash(pnpm info*)",
    },
},
```

The `readonly` include group provides `Read`, `Glob`, `Grep` — full codebase exploration. The `common` include group provides `Bash(ls *)`, `Bash(find *)`, `Bash(wc *)`, etc.

On OpenCode, `translateToolProfile()` converts these Claude Code tool names into OpenCode permission blocks. `WebSearch` and `WebFetch` translate to OpenCode's web tools. The `curl` and `gh` bash patterns provide fallback access to APIs and GitHub when native web tools are insufficient.

### Chain exclusion

The research agent is **not** part of any event chain:

- **Not in build→test→review chain**: research is not triggered by build success, test success, or any chain outcome
- **Not in deploy→run→watch chain**: research is not triggered by deploy outcomes
- **Not in AutoCC list**: messages from build/test/review/deploy to research are not auto-copied to edit (current `AutoCC: []string{"build", "test", "review", "deploy", "analyze"}` — research is not included)
- **No send policy restrictions**: research has no deny rules — it can send to any agent
- **No chain triggers research**: no `EventChain` action targets the research role

The research agent responds only to direct inbox messages — it is purely request/response. Other agents ask it questions, it researches and replies. It never receives automated chain-triggered messages.

### Active F2 agent routing

The F2 window hosts two agents (edit and auto) that share the window via mode cycling. The research agent needs to know which one is active so that replies and delegations reach the right agent — the one the user is interacting with or the one driving the current workflow.

**Routing rule**: when research needs to send findings or delegate implementation, it targets the **active F2 agent** — not a hardcoded role.

**How to determine the active agent:**

```bash
# Returns just the active role name — "edit" or "auto"
muxcode mode active --window edit
```

This reads `mode-cycle-edit.json` and returns the active role. Requires adding an `active` subcommand to `cmd/mode.go` and an `ActiveModeRole()` helper to `bus/mode.go`.

**Routing scenarios:**

| Scenario | Active F2 mode | Research sends to |
|----------|---------------|-------------------|
| Bus request from edit | (any) | `edit` (reply to sender) |
| Bus request from auto | (any) | `auto` (reply to sender) |
| User asks directly in research pane | Edit active | `edit` |
| User asks directly in research pane | Auto active | `auto` |
| Research discovers needed fix | Edit active | `edit` |
| Research discovers needed fix | Auto active | `auto` |

**Rule of thumb:**
- **Request-reply** (incoming bus message): always reply to the sender via `--type response --reply-to <id>` — the `from` field tells you who asked
- **Proactive findings / delegation** (no incoming message, or user typed directly): check the active F2 agent and send there

### Delegation to active F2 agent

When research findings reveal that code changes are needed, the research agent delegates to the **active F2 agent** — it does **not** make code changes itself. This follows the same pattern as the plan agent, but targets whichever F2 agent is driving the current work.

**When to delegate:**

- Research reveals a bug, deprecation, or needed fix in the codebase
- A research question leads to a concrete implementation recommendation
- The requesting agent asks research to "fix this" or "update the code"
- Research identifies a pattern that should be refactored

**How to delegate:**

```bash
# Determine which F2 agent is active
ACTIVE=$(muxcode mode active --window edit)

# Delegate implementation based on research findings
muxcode send "$ACTIVE" implement "Research found that Go 1.22 changed http.ServeMux routing — update tools/muxcode/main.go to use the new pattern."

# Delegate a fix discovered during research
muxcode send "$ACTIVE" implement "The CDK KMS key rotation API changed in v2.130 — update lib/constructs/encryption.ts to use the new autoRotate property."
```

When the edit agent is active, this delegates the same way the plan agent does. When the auto agent is active, the delegation feeds into the autonomous workflow — the auto agent can incorporate the fix into its current story.

**What research does NOT do:**

- Never writes code files (no `Write` or `Edit` tools for source code)
- Never runs build, test, deploy, or git write commands
- Never creates branches, commits, or PRs

**Delegation flow:**

```
1. Agent X sends research request to research agent
2. Research agent investigates (web search, API docs, GitHub)
3. Research agent replies to Agent X with findings (--reply-to)
4. If findings require code changes:
   a. Research checks active F2 agent: muxcode mode active --window edit
   b. Research delegates to active agent: muxcode send <active> implement "..."
   c. Research replies to Agent X noting the delegation
```

```
1. User types question directly in research pane
2. Research agent investigates
3. Research checks active F2 agent: muxcode mode active --window edit
4. Research sends findings to active agent: muxcode send <active> research-findings "..."
5. If findings require code changes:
   a. Research delegates to same active agent: muxcode send <active> implement "..."
```

The research agent's tool profile already excludes `Write` and `Edit` — it has read-only codebase access plus web search tools. Implementation delegation is the only path to code changes.

### Delegation from other agents

Other agents send research requests directly to the research role. Typical requests involve looking up API documentation, checking GitHub repos, or finding platform reference material:

```bash
# API documentation lookups
muxcode send research research "Find the AWS CDK v2 docs for KMS key autoRotation — did the property name change in recent versions?" --wait
muxcode send research research "What's the CloudFormation resource type for EventBridge Pipes and what are the required properties?" --wait

# Platform reference / SDK usage
muxcode send research research "How does Go 1.22 http.ServeMux pattern routing work? What changed from 1.21?" --wait
muxcode send research research "Find the Node.js 18 docs for the built-in fetch API — does it support AbortController?" --wait

# Open source project research on GitHub
muxcode send research research "Check the christoomey/vim-tmux-navigator repo — does it support Neovim 0.10 and what's the latest install method?" --wait
muxcode send research research "Find open source Go libraries for JSONL streaming — compare performance and API design on GitHub" --wait

# Changelog / migration research
muxcode send research research "What breaking changes are in pnpm v9? Check the GitHub releases and migration guide" --wait
muxcode send research research "Search GitHub issues on aws-cdk for known bugs with S3 event notifications to Lambda" --wait
```

The `--wait` flag polls the sender's inbox for the response. The research agent reads its own inbox, performs the research, and replies.

### Relationship to plan agent

The research and plan agents share the F1 window but are fully independent — separate roles, inboxes, sessions, and even providers.

| Aspect | Plan agent | Research agent |
|--------|-----------|---------------|
| Role | `plan` | `research` |
| CLI / Model | Claude Code / Opus | OpenCode / DeepSeek |
| Hooks | Yes (Claude Code) | No (non-hook provider) |
| Claude session | Persistent (survives toggles) | Persistent (survives toggles) |
| System prompt | Documentation maintenance, requirements specs | API docs, platform references, GitHub project research |
| Left pane | Neovim | Research findings console |
| Tool profile | `bus`, `readonly`, `common`, `Write`, `Edit` | `bus`, `readonly`, `common`, `WebSearch`, `WebFetch`, `curl`, `gh`, `tree` |
| Bus inbox | `plan` | `research` |
| Scope | Docs directories only | Web (API docs, platform refs, GitHub) + codebase for context |
| Repo context | Reads code for context, writes docs only | Full read access — CLAUDE.md, auto-detected project context, git history |
| Chain participation | None — responds to direct requests only | None — responds to direct requests only |
| Delegates to edit/auto | Yes — implementation to edit only | Yes — delegates to whichever F2 agent is active |
| Delegates to commit | Yes — git operations | No — not involved in git workflow |
| Active when hidden | Woken by daemon for inbox | Woken by daemon for inbox |
| Findings persistence | N/A | `research-history.jsonl` + memory writes |

Both agents continue to respond to bus messages when hidden — toggling only changes visibility, not process state.

## Implementation

### Phase 1: Role migration, mode cycle state, and provider configuration

Move research from hosted role to mode role, add plan window cycle state, and configure OpenCode + DeepSeek as the default provider.

Updated files:

| File | Change |
|------|--------|
| `bus/config.go` | Move `research` from `hostedRoles` to `modeRoles` |
| `bus/mode.go` | Add `DefaultPlanModeCycleState()`, update `ReadModeCycleState()` fallback for `window == "plan"` |
| `bus/mode_test.go` | Add tests for plan cycle state (default state, read fallback, cycle wrapping) |
| `bus/launch.go` | Add `case "research"` to `RoleOpenCodeModelDefault()` returning `"opencode-go/deepseek-v4-pro"`, set `roleDefaultCLI("research")` to `"opencode"` |
| `bus/launcher.go` | Add `research` to `HasConsoleView()` |

Success criteria:
- [ ] `research` removed from `hostedRoles` map
- [ ] `research` added to `modeRoles` map with value `"research"`
- [ ] `DefaultPlanModeCycleState()` returns plan/research cycle with plan at index 0
- [ ] `ReadModeCycleState(session, "plan")` falls back to `DefaultPlanModeCycleState()` when file missing
- [ ] Research agent gets independent inbox (`inbox/research.jsonl`)
- [ ] `WindowForRole("research")` returns `"research"` (hold window)
- [ ] `research` in `HasConsoleView()` list
- [ ] `RoleOpenCodeModelDefault("research")` returns `"opencode-go/deepseek-v4-pro"`
- [ ] `MUXCODE_RESEARCH_CLI` defaults to `opencode`
- [ ] `MUXCODE_RESEARCH_CLI` can be overridden to `claude` for users who prefer Claude Code
- [ ] Existing mode cycling tests still pass

### Phase 2: F1 keybinding and prefix shortcut

Update tmux.conf to add F1 mode cycling and a prefix shortcut.

Updated files:

| File | Change |
|------|--------|
| `config/tmux.conf` | F1 `if-shell` toggle keybinding (same pattern as F2), `prefix + r` toggle shortcut |

Success criteria:
- [ ] F1 when on plan window (index 1) runs `muxcode mode cycle --window plan`
- [ ] F1 when on other window runs `select-window -t:1`
- [ ] `prefix + r` toggles plan/research modes regardless of current window
- [ ] F2 cycling continues to work unchanged

### Phase 3: Findings console and active mode helper

Add research findings console config and the `muxcode mode active` CLI helper for F2-aware routing.

Updated files:

| File | Change |
|------|--------|
| `bus/console.go` | Add `research` to `DefaultConsoleConfigs()` — reads `research-history.jsonl` for findings, shows question/answer/sources/timestamp per entry |
| `bus/mode.go` | Add `ActiveModeRole()` helper — returns active role for a window |
| `bus/mode_test.go` | Add tests for `ActiveModeRole()` |
| `cmd/mode.go` | Add `active` subcommand — prints active role for a window |
| `config/nvim/plugin/startscreen.lua` | Add `research` to roles list for status display |

Success criteria:
- [ ] `muxcode console research` renders Dracula-themed findings log
- [ ] Console displays research findings with question, answer summary, source URLs, timestamp
- [ ] Console reads from `research-history.jsonl` (dedicated history file, not `log.jsonl`)
- [ ] `muxcode mode active --window edit` returns `edit` or `auto` depending on current state
- [ ] `muxcode mode active --window plan` returns `plan` or `research` depending on current state
- [ ] Startscreen shows research agent status

### Phase 4: Agent definition update

Refocus the agent definition on the primary use case, configure for OpenCode/DeepSeek, and add F2-aware delegation.

Updated files:

| File | Change |
|------|--------|
| `agents/code-researcher.md` | Refocus on API docs / platform refs / GitHub research, add F2-aware delegation via `muxcode mode active`, add repo context awareness, add chain exclusion note, add findings persistence instructions (write to `research-history.jsonl` and memory) |

Success criteria:
- [ ] Agent definition emphasizes web search for API docs, platform references, and GitHub projects
- [ ] Agent definition routes delegations to active F2 agent via `muxcode mode active --window edit`
- [ ] Agent definition notes repo context awareness (CLAUDE.md, project structure, git history)
- [ ] Agent definition explicitly states it is not part of any event chain
- [ ] Agent definition instructs research agent to save findings to `research-history.jsonl` after each completed research
- [ ] Agent definition instructs saving important findings to memory for cross-session persistence
- [ ] Reply-to pattern documented: reply to sender for bus requests, active F2 agent for direct interaction
- [ ] `adaptBodyForNonHookProvider()` correctly adapts agent body for OpenCode

### Phase 5: Documentation

Update docs to reflect the new F1 cycling behavior.

Updated files:

| File | Change |
|------|--------|
| `docs/architecture.md` | Add F1 cycling section (parallel to F2 agent mode section), update window layout |
| `docs/agents.md` | Update research role entry — change from spawn-only to F1 mode on plan |
| `docs/agent-bus.md` | Update `mode` command docs to mention `--window plan`, add `mode active` subcommand |
| `CLAUDE.md` | Update key constraints noting research has independent inbox and F1 toggle |

Success criteria:
- [ ] Architecture docs describe F1 plan/research toggle
- [ ] Agents docs show research as a mode on the plan window (not spawn-only)
- [ ] CLI reference includes `--window plan` examples and `mode active` subcommand
- [ ] CLAUDE.md reflects research role migration

## Key files

| File | Purpose |
|------|---------|
| `bus/config.go` | Role maps: `hostedRoles`, `modeRoles`, `WindowForRole()` |
| `bus/mode.go` | Mode cycling: `ModeCycleState`, `DefaultPlanModeCycleState()`, `ModeCycle()`, `modeCreateAgent()`, `ActiveModeRole()` |
| `bus/mode_test.go` | Mode cycle unit tests |
| `cmd/mode.go` | CLI: `muxcode mode {cycle,status,switch,list,active} --window plan` |
| `bus/launch.go` | `RoleOpenCodeModelDefault("research")`, `roleDefaultCLI("research")` |
| `bus/launcher.go` | `HasConsoleView()` |
| `bus/console.go` | Console viewer configs: `DefaultConsoleConfigs()` — findings log from `research-history.jsonl` |
| `bus/profile.go` | Tool profiles: `research` profile (existing, no changes) |
| `bus/provider_opencode.go` | OpenCode agent config generation, body adaptation |
| `config/tmux.conf` | F1 toggle keybinding, `prefix + r` shortcut |
| `agents/code-researcher.md` | Research agent definition (update: API docs focus, OpenCode/DeepSeek, F2-aware delegation, findings persistence) |

## Dependencies

| Dependency | Purpose | Status |
|------------|---------|--------|
| `bus/mode.go` | Mode cycling infrastructure | Existing (from agent-mode) |
| `cmd/mode.go` | CLI subcommand | Existing |
| `agents/code-researcher.md` | Research agent definition | Existing (needs update) |
| `bus/profile.go` | Research tool profile | Existing |
| `bus/console.go` | Console viewer infrastructure | Existing |
| `bus/provider_opencode.go` | OpenCode provider: agent config, body adaptation, wake-up | Existing |
| F2 agent-mode | Precedent, shared cycling infrastructure, `mode-cycle-edit.json` | Complete |
| OpenCode CLI | Must be installed | External prerequisite |

**Shared infrastructure with F2 agent-mode**: the mode cycle mechanism (`bus/mode.go`, `cmd/mode.go`) is fully generic — it already accepts `--window <name>` and uses per-window state files (`mode-cycle-{window}.json`). This spec adds no new cycling logic — only a new default state and one role migration.

## Configuration

| Env var | Default | Description |
|---------|---------|-------------|
| `MUXCODE_RESEARCH_CLI` | `opencode` | CLI provider — set to `claude` to use Claude Code instead |
| `OPENCODE_MODEL` | `opencode-go/deepseek-v4-pro` | OpenCode model — set globally or per-role via `RoleOpenCodeModelDefault("research")` |
| `MUXCODE_RESEARCH_CLAUDE_MODEL` | `claude-sonnet-4-5` | Claude model override (only when `MUXCODE_RESEARCH_CLI=claude`) |

## Risks

| Risk | Mitigation |
|------|-----------|
| Breaking existing `muxcode send research` callers | No API change — `send research` still works, now delivers to independent inbox instead of edit's |
| Edit agent losing research context | Research now has its own session — edit agents that previously handled research requests should delegate to the research agent instead |
| OpenCode/DeepSeek not installed | Graceful fallback — `muxcode-agent.sh` falls through to Claude Code if OpenCode is unavailable |
| No hook support on OpenCode | Research is purely request/response with no chain participation — hooks are irrelevant. Bus messaging is prompt-driven via `adaptBodyForNonHookProvider()` |
| Lazy launch delay on first F1 toggle | Same as F2 agent-mode — acceptable UX, agent launches in ~2-3 seconds |
| Plan agent wake-up after toggle | Daemon's `checkIdleAgents()` handles this — no special case needed |
| Mixed providers on same window | Plan (Claude Code) and Research (OpenCode/DeepSeek) use different providers — `modeCreateAgent()` resolves the provider per-role, not per-window |

## Status

Draft
