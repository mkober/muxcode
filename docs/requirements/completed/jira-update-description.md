# Jira Description Read+Update Skill

## Purpose

General-purpose skill for the git-manager agent to read a Jira issue's current description (with context fields) and update it with new ADF content. The Jira issue key is extracted from the request message or falls back to the branch name. Uses the Atlassian REST API v3 with Basic auth via environment variables.

## Requirements

- Skill file at `skills/jira-update-description.md` with frontmatter: `name: jira-update-description`, `roles: [git]`, `tags: [jira, integration, description, adf]`
- Git-manager agent executes the skill when asked to read or update a Jira issue description
- Gracefully skip (no error) when env vars are missing (`JIRA_BASE_URL`, `JIRA_USER_EMAIL`, `JIRA_API_TOKEN`)
- Two-path key identification: first scan request message for `[A-Z][A-Z0-9]*-[0-9]+`, then fall back to branch name extraction (`^` anchored); skip silently if neither yields a key
- GET `/rest/api/3/issue/{key}?fields=description,summary,status,assignee` — parse context fields and flatten ADF text nodes for human-readable preview
- PUT `/rest/api/3/issue/{key}` with `{"fields":{"description":{...}}}` — success is 204 No Content
- ADF content array composed via `jq -n --argjson blocks` to avoid shell escaping issues
- Report result back to edit agent
- No Go code or tool profile changes needed — `Bash(curl*)` is already in the `git` tool profile

## Changes

### 1. Create `skills/jira-update-description.md`

New skill file with instructions for the git-manager agent:

1. Check env vars (`JIRA_BASE_URL`, `JIRA_USER_EMAIL`, `JIRA_API_TOKEN`) — skip silently if missing
2. Extract Jira key from request message, fall back to branch name — skip if no match
3. GET issue with context fields (`description`, `summary`, `status`, `assignee`)
4. Flatten ADF description to human-readable text via `jq`
5. Build ADF content array and inject via `jq -n --argjson blocks`
6. PUT updated description — check for 204 success
7. Report result back to edit agent

### 2. Update backlog

Add row to the Implemented table in `docs/requirements/backlog.md`.

## Acceptance criteria

- `muxcode-agent-bus skill list --role git` shows `jira-update-description`
- `muxcode-agent-bus skill load jira-update-description` renders skill content correctly
- Build passes — no Go changes, but install copies new skill file
- With env vars set and a valid Jira key, GET returns issue context and flattened description
- PUT with ADF content array returns 204 and updates the issue description
- Explicit key in request message (`"update PROJ-456 description"`) takes priority over branch name
- Missing env vars or no Jira key cause silent skip, not an error
