# Skills/Plugin System — Implementation Plan

## Context

Feature #2 from `docs/openclaw-suggestions.md`: **Skills/plugin system for reusable instruction sets**.

Agent definitions are static `.md` files with no runtime extensibility. There's no way to add reusable instruction snippets that span across roles. The skills system adds a file-based plugin mechanism that lets users (and agents themselves) define reusable instruction sets, searchable and automatically injected into relevant agent prompts at launch time.

## Architecture

### File format

Skills are markdown files with YAML frontmatter, matching the agent definition pattern:

```markdown
---
name: cdk-diff
description: Run CDK diff to preview infrastructure changes
roles: [deploy, edit]
tags: [aws, cdk]
---

Instructions go here...
```

### Three-tier resolution

| Priority | Location | Source label |
|----------|----------|-------------|
| 1 (highest) | `.muxcode/skills/` | project |
| 2 | `~/.config/muxcode/skills/` | user |
| 3 (lowest) | Installed defaults from `skills/` | user |

Project-local skills shadow user-level skills by name. Empty `roles: []` means the skill applies to all agents.

### Search scoring

Term-based scoring with weighted fields:

| Field | Weight |
|-------|--------|
| Name | 3x |
| Description | 2x |
| Tags | 2x |
| Body | 1x |

Case-insensitive substring matching via `strings.Count()`.

### Prompt injection

Skills are auto-injected into agent system prompts at launch. The bash launcher (`muxcode-agent.sh`) calls `muxcode-agent-bus skill prompt <role>` and concatenates the output with the shared coordination prompt via `--append-system-prompt`.

## Files created

| File | Purpose |
|------|---------|
| `tools/muxcode-agent-bus/bus/skill.go` | Core library — SkillDef struct, YAML frontmatter parsing, list/search/load/create/format |
| `tools/muxcode-agent-bus/bus/skill_test.go` | 14 tests — parsing, listing, shadowing, role filtering, search, create, formatting |
| `tools/muxcode-agent-bus/cmd/skill.go` | Subcommand handler — list, load, search, create, prompt sub-subcommands |
| `skills/git-commit-conventions.md` | Default skill: commit message format (roles: commit, edit) |
| `skills/go-testing.md` | Default skill: Go testing patterns (roles: test, build) |
| `skills/code-review-checklist.md` | Default skill: review quality checklist (roles: review) |

## Files modified

| File | Change |
|------|--------|
| `bus/config.go` | Added `SkillsDir()` and `UserSkillsDir()` path helpers with env overrides |
| `main.go` | Added `"skill"` to usage string and switch dispatch |
| `bus/prompt.go` | Added `### Skills` section with CLI usage examples |
| `bus/prompt_test.go` | Added `"### Skills"` and `"muxcode-agent-bus skill"` to required sections |
| `scripts/muxcode-agent.sh` | Updated `build_shared_prompt()` to merge skill prompt output |
| `Makefile` | Added `skills/*.md` install to `$(CONFIGDIR)/skills/` |
| `CLAUDE.md` | Added `skills/` to directory structure + "Skill definitions" subsection |

## Core types

```go
type SkillDef struct {
    Name        string
    Description string
    Roles       []string // empty = all roles
    Tags        []string
    Body        string
    Source      string   // "project" or "user"
}

type SkillSearchResult struct {
    Skill SkillDef
    Score float64
}
```

## Core functions in `bus/skill.go`

| Function | What it does |
|----------|-------------|
| `parseSkillFile(path, source)` | Read file, split frontmatter/body, parse fields (stdlib only) |
| `parseFrontmatter(text, *SkillDef)` | Parse `key: value` and `key: [a, b]` lines |
| `parseYAMLList(val)` | Parse `[a, b, c]` into `[]string` |
| `ListSkills()` | Scan all dirs, de-duplicate by name (higher priority shadows) |
| `SkillsForRole(role)` | Filter: empty roles = all, otherwise must match |
| `LoadSkill(name)` | Load single skill by name, first-match across dirs |
| `SearchSkills(query, roleFilter)` | Weighted term scoring across all fields |
| `CreateSkill(name, desc, body, roles, tags)` | Write to project-local `.muxcode/skills/` |
| `FormatSkillList(skills)` | Columnar: name, roles, description |
| `FormatSkillSearchResults(results)` | Block-style with scores |
| `FormatSkillPrompt(skill)` | `### Skill: name\ndescription\n\nbody` |
| `FormatSkillsPrompt(skills)` | `## Available Skills\n` + each skill formatted |

## CLI subcommands

| Subcommand | Usage | What it does |
|-----------|-------|-------------|
| `list` | `skill list [--role ROLE]` | List all or role-filtered skills |
| `load` | `skill load <name>` | Print a single skill's formatted body |
| `search` | `skill search <query> [--role ROLE]` | Search skills by keyword |
| `create` | `skill create <name> <desc> [--roles r1,r2] [--tags t1,t2] <body>` | Create a new skill file |
| `prompt` | `skill prompt <role>` | Output assembled skills prompt for a role |

## Bash integration

`build_shared_prompt()` in `scripts/muxcode-agent.sh` concatenates coordination prompt + skills prompt:

```bash
build_shared_prompt() {
  local prompt skills combined
  prompt="$(muxcode-agent-bus prompt "$1" 2>/dev/null)" || prompt=""
  skills="$(muxcode-agent-bus skill prompt "$1" 2>/dev/null)" || skills=""
  combined="${prompt}${skills:+$'\n'$skills}"
  [ -z "$combined" ] && return
  SHARED_PROMPT_FLAGS=(--append-system-prompt "$combined")
}
```

## Tests

| Test | What it validates |
|------|------------------|
| `TestParseSkillFile` | Frontmatter parsing, all fields |
| `TestParseSkillFile_NoFrontmatter` | Plain markdown without frontmatter |
| `TestListSkills_Empty` | No skills dirs → empty list |
| `TestListSkills_FindsSkills` | Finds .md files, sorts alphabetically |
| `TestListSkills_ProjectShadowsUser` | Project-local skills shadow user-level |
| `TestSkillsForRole` | Role filtering (build, edit, test, deploy) |
| `TestLoadSkill` | Load single skill by name |
| `TestLoadSkill_NotFound` | Error for nonexistent skill |
| `TestSearchSkills` | Term-based search finds correct skill |
| `TestSearchSkills_CaseInsensitive` | Case-insensitive matching |
| `TestCreateSkill` | Writes file with correct frontmatter |
| `TestFormatSkillsPrompt` | Formatted output includes headers and bodies |
| `TestFormatSkillsPrompt_Empty` | Nil/empty skills → empty string |
| `TestParseYAMLList` | Table-driven for various inputs |

## Default skills shipped

| File | Roles | Purpose |
|------|-------|---------|
| `git-commit-conventions.md` | commit, edit | Commit message format and workflow |
| `go-testing.md` | test, build | Go testing patterns and conventions |
| `code-review-checklist.md` | review | Review quality checklist |

## Env overrides

| Variable | Controls |
|----------|----------|
| `BUS_SKILLS_DIR` | Project-local skills directory (default: `.muxcode/skills`) |
| `MUXCODE_CONFIG_DIR` | User config root for skills (default: `~/.config/muxcode`) |

## Usage examples

```bash
# List all skills
muxcode-agent-bus skill list

# List skills for a specific role
muxcode-agent-bus skill list --role build

# Load a single skill
muxcode-agent-bus skill load go-testing

# Search for skills
muxcode-agent-bus skill search "testing"
muxcode-agent-bus skill search "aws" --role deploy

# Create a new project-local skill
muxcode-agent-bus skill create pnpm-conventions "pnpm usage rules" --roles build,edit --tags pnpm "Always use pnpm, never npm or yarn."

# Output prompt for a role (used by launcher)
muxcode-agent-bus skill prompt edit
```

## Verification

1. `go build .` — compiles cleanly
2. `go test ./...` — all 118 tests pass (14 new skill tests)
3. `skill list` — lists 3 default skills
4. `skill prompt edit` — outputs formatted skills for edit role
5. `skill search "testing"` — finds go-testing skill
6. `skill create test-new "A test" "Body"` — creates `.muxcode/skills/test-new.md`
7. `make install` — skills copied to `~/.config/muxcode/skills/`
8. `prompt edit` output includes `### Skills` section

## Commit

Shipped in `2efb67b` — 13 files, 1,034 insertions.
