# Planner agent

## Purpose

A dedicated agent for viewing, editing, and maintaining project documentation — requirements specs, architecture docs, and planning artifacts. The planner runs in its own tmux window (F1 Plan) with a Neovim editor on the left and Claude Code on the right, mirroring the edit window layout but scoped exclusively to docs directories. The edit agent can message the planner to update docs as implementation progresses — checking off completed phases, updating status fields, and recording decisions.

## Context

### Current state

Documentation updates are handled by the edit agent alongside code changes. This creates friction:

- The edit agent's context window fills with code, leaving little room for doc-aware reasoning
- Docs fall out of sync with implementation — phases stay "Draft" long after completion
- No agent is responsible for doc quality, cross-referencing, or keeping specs current
- The `docs` role exists as a hosted role in the edit window but has no dedicated agent definition or tooling

### What this changes

- Adds a `plan` window (F1) with Neovim + Claude Code, identical layout to `edit`
- Introduces a `plan` agent role scoped to docs directories only
- The edit agent can delegate doc updates via `muxcode send plan update-docs "..."`
- Neovim in the plan window opens with docs directory as the working path
- The `docs` hosted role is replaced by the standalone `plan` role

## Requirements

### Tmux window

- Window name: `plan`
- Position: first in `MUXCODE_WINDOWS` (F1), shifting `edit` to F2 and all others down by one
- Default `MUXCODE_WINDOWS`: `plan edit build test review deploy run watch commit analyze`
- Layout: horizontal split — pane 0 (left) Neovim, pane 1 (right) Claude Code agent
- Neovim opens the last-edited doc file on startup (see Neovim configuration below)
- Same `NVIM_APPNAME=muxcode/nvim` config as the edit window

### Agent role

- Role name: `plan`
- Registered in `KnownRoles` in `bus/config.go`
- Added to `splitLeftWindows` (has a left-pane Neovim editor)
- Added to `WindowForRole()` mapping (self-mapped, not hosted)
- The existing `docs` hosted role (`hostedRoles["docs"] = "edit"`) is remapped to `plan`: `hostedRoles["docs"] = "plan"`
- `NormalizeBusRole("planner")` maps to `plan` (legacy alias)

### Agent definition

- File: `agents/planner.md` (resolved via `AgentFileName("plan")`)
- Frontmatter: `description: Documentation specialist — maintains requirements, architecture docs, and planning artifacts`
- Scoped to docs directories: `docs/`, `docs/requirements/`, `docs/requirements/drafts/`, `docs/requirements/completed/`, `docs/requirements/backlog/`
- Also permitted to read (but not write) source files for context when updating docs
- Uses the standard reply protocol (`muxcode send <requester> <action> "..." --type response --reply-to <id>`)

### Tool profile

- Profile key: `plan` in `DefaultConfig()` in `bus/profile.go`
- Includes: `bus`, `readonly`, `common`
- Permitted tools:
  - `Read`, `Write`, `Edit`, `Glob`, `Grep` — scoped to docs directories
  - `Bash(git diff*)`, `Bash(git log*)`, `Bash(git status*)` — read-only git for understanding what changed
  - `Bash(git rev-parse*)` — branch name detection
  - `Bash(python3*)`, `Bash(jq*)` — utility commands
  - All `muxcode *` bus commands
- NOT permitted: build, test, deploy, `gh` commands, code editing outside docs

### Actions the planner handles

| Action | Description | Example message |
|--------|-------------|-----------------|
| `update-docs` | Update docs based on implementation progress | `"Phase 1 of conditional-chains is complete — update the status and check off acceptance criteria"` |
| `update-status` | Change a requirement spec's status field | `"Mark MUX-043-conditional-chains.md status as In Progress"` |
| `check-phase` | Check off a completed phase in a spec | `"Check off Phase 2 in docs/requirements/drafts/MUX-043-conditional-chains.md"` |
| `add-decision` | Record an implementation decision in a doc | `"Record decision: used AND-only conditions, deferred OR support"` |
| `review-docs` | Review docs for accuracy against current code | `"Review architecture.md for accuracy after the daemon rename"` |
| `create-spec` | Create a new requirements spec from a description | `"Create a draft spec for webhook retry logic"` |
| `move-spec` | Move a spec between drafts/completed/backlog | `"Move MUX-043-conditional-chains.md from drafts to completed"` |

### Edit agent integration

The edit agent delegates doc updates to the planner via the bus:

```bash
muxcode send plan update-docs "Phase 1 of conditional-chains is complete — check off acceptance criteria and update status to In Progress" --wait
```

Trigger patterns for the edit agent to delegate to planner:
- After completing an implementation phase referenced in a requirements doc
- When the user says "update the docs", "mark phase complete", "update the spec"
- After moving a spec between directories
- When recording architectural decisions

The edit agent's `code-editor.md` gets a new delegation row:

| Prohibited prefix | Delegate to | Bus command |
|---|---|---|
| Doc updates in `docs/` | plan agent | `muxcode send plan update-docs "..."` |

### Event chains

- No automatic chain triggers for the planner — it responds to explicit requests only
- The planner does NOT participate in the build→test→review chain
- Optional: add to `AutoCC` list so the planner receives copies of chain messages for awareness (low priority)

### Skills

Create a `docs-management` skill available to the `plan` role:

- Moving specs between `drafts/`, `completed/`, `backlog/`
- Updating status fields in YAML frontmatter or markdown body
- Checking off acceptance criteria checkboxes
- Updating phase status tables
- Cross-referencing docs with code (verifying file paths in key files tables)

### Neovim configuration

- Same `NVIM_APPNAME=muxcode/nvim` config as edit window
- Neovim opens the **last-edited doc** on startup — determined by `git log -1 --diff-filter=M --name-only --pretty=format: -- docs/` (most recently modified file in `docs/`). If no git history is available, falls back to opening the `docs/` directory
- Telescope file finder scoped to `docs/` by default (can be overridden)
- Render-markdown plugin active for previewing docs in the terminal

## Acceptance criteria

### Phase 1: Core window and agent

- [x] `plan` window created at F1 with Neovim (left) + Claude Code (right)
- [x] `edit` shifts to F2, all other windows shift accordingly
- [x] `plan` role registered in `KnownRoles`, `splitLeftWindows`, `WindowForRole`
- [x] `agents/planner.md` agent definition created with docs-scoped instructions
- [x] Tool profile in `bus/profile.go` restricts plan agent to docs directories
- [x] `muxcode send plan update-docs "..."` delivers messages and planner responds
- [x] Neovim in plan window opens the last-edited doc file (via `git log` of `docs/`)

### Phase 2: Edit agent integration

- [x] `code-editor.md` updated with planner delegation rules
- [x] Edit agent delegates doc updates to planner when user requests
- [x] Planner replies via standard response protocol
- [x] `docs` hosted role remapped from `edit` to `plan`

### Phase 3: Skills and polish

- [x] `docs-management` skill created with spec lifecycle operations
- [x] Planner can move specs between `drafts/`, `completed/`, `backlog/`
- [x] Planner can check off acceptance criteria and update status fields
- [x] Harness agent definition created at `agents/harness/planner.md` for local LLMs
- [x] Status bar shows `F1 Plan` label

### Phase 4: Documentation

- [x] `CLAUDE.md` updated with plan role, tool profile, and key constraints
- [x] `docs/agents.md` updated with planner role description
- [x] `docs/architecture.md` updated with plan window in layout diagram
- [x] `README.md` updated with plan window in feature list
- [x] This spec moved to `completed/`

## Key files

| File | Changes |
|------|---------|
| `muxcode.sh` | Add `plan` to default `MUXCODE_WINDOWS`, window creation logic |
| `config/tmux.conf` | F-key mappings shift (F1=plan, F2=edit, ...) |
| `tools/muxcode/bus/config.go` | Register `plan` in `KnownRoles`, `splitLeftWindows`, `WindowForRole`, remap `docs` hosted role |
| `tools/muxcode/bus/profile.go` | Add `plan` tool profile, add to `AutoCC` if needed |
| `tools/muxcode/bus/launch.go` | Add `AgentFileName("plan")` mapping to `planner` |
| `agents/planner.md` | New agent definition |
| `agents/harness/planner.md` | Simplified agent definition for local LLMs |
| `agents/code-editor.md` | Add planner delegation rules |
| `skills/docs-management.md` | New skill for spec lifecycle management |
| `CLAUDE.md` | Document plan role and constraints |
| `docs/agents.md` | Add planner role description |
| `docs/architecture.md` | Update layout diagram |

## Non-goals

- **Code editing** — the planner never edits source code; that's the edit agent's job
- **Build/test/deploy** — the planner has no access to build or test tools
- **GitHub interaction** — no `gh` commands; PR-related doc updates are relayed via the edit agent
- **Automatic doc generation** — the planner updates existing docs on request, not auto-generates from code
- **Wiki/Confluence sync** — external doc platforms remain the edit agent's responsibility via skills

## Risks

| Risk | Mitigation |
|------|------------|
| F-key shift breaks muscle memory | Document the change prominently; allow `MUXCODE_WINDOWS` override to restore old order |
| Planner edits code files by mistake | Tool profile restricts Write/Edit to `docs/` paths; agent definition reinforces scope |
| Message overhead for small doc updates | Edit agent can still make trivial doc edits directly (e.g. fixing a typo); delegation is for structured updates |

## Status

Complete
