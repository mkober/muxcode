# Jira Description Read+Update Skill

## Purpose

General-purpose skill for the git-manager and edit agents to read a Jira issue's current description (with context fields) and update it with new ADF content. The Jira issue key is extracted from the request message or falls back to the branch name. Uses the `muxcode-jira.sh` wrapper script which handles Atlassian REST API v3 auth internally.

## Requirements

- Skill file at `skills/MUX-060-jira-update-description.md` with frontmatter: `name: jira-update-description`, `roles: [git, edit]`, `tags: [jira, integration, description, adf]`
- Agents execute the skill when asked to read or update a Jira issue description
- Two-path key identification: first scan request message for `[A-Z][A-Z0-9]*-[0-9]+`, then fall back to branch name extraction (`^` anchored); skip silently if neither yields a key
- Read via `muxcode-jira.sh read <KEY>` — outputs summary, type, priority, status, assignee, and flattened description
- Update via `muxcode-jira.sh update <KEY> <ADF-FILE>` — success is 204 No Content
- ADF content array composed via `jq -n --argjson blocks` to avoid shell escaping issues, written to temp file
- Report result back to edit agent
- `Bash(muxcode-jira.sh *)` must be in the agent's permissions (global settings)

## Changes

### 1. Create `skills/MUX-060-jira-update-description.md`

New skill file with instructions:

1. Extract Jira key from request message, fall back to branch name — skip if no match
2. Read: `muxcode-jira.sh read "$jira_key"`
3. Update: build ADF content array via `jq`, write to temp file, `muxcode-jira.sh update "$jira_key" "$tmpfile"`
4. Report result back to edit agent

### 2. Wrapper script `scripts/muxcode-jira.sh`

Handles config sourcing and curl+auth internally. Avoids inline curl+auth that triggers Claude Code "quoted characters in flag names" permission prompts.

### 3. Update backlog

Add row to the Implemented table in `docs/requirements/backlog.md`.

## Acceptance criteria

- `muxcode skill list --role git` shows `jira-update-description`
- `muxcode skill load jira-update-description` renders skill content correctly
- Build passes — install copies skill file and wrapper script
- `muxcode-jira.sh read PROJ-123` returns issue context and flattened description without permission prompts
- `muxcode-jira.sh update PROJ-123 /tmp/payload.json` updates the description without permission prompts
- Explicit key in request message (`"update PROJ-456 description"`) takes priority over branch name
- Missing env vars cause script error (non-zero exit), skill reports failure gracefully
- No Jira key causes silent skip, not an error
