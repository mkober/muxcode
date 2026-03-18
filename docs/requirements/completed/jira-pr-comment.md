# Jira PR Comment Skill

## Purpose

When a PR is created via the git-manager agent, automatically post a comment on the corresponding Jira issue with PR details. The Jira issue key is extracted from the branch name. Uses the `muxcode-jira.sh` wrapper script which handles Atlassian REST API v3 auth internally.

## Requirements

- Skill file at `skills/jira-pr-comment.md` with frontmatter: `name: jira-pr-comment`, `roles: [git]`, `tags: [jira, github, pr, integration]`
- Git-manager agent executes the skill after `gh pr create`
- Extract Jira key from branch name pattern `PROJ-123-desc` → `PROJ-123`; skip silently if no match
- Gather PR metadata via `gh pr view --json number,title,body,url,additions,deletions,changedFiles`
- Build ADF (Atlassian Document Format) JSON payload with `jq` — includes PR link, title, summary, diff stats
- POST comment via `muxcode-jira.sh comment <KEY> <ADF-FILE>`
- Report result back to edit agent
- `Bash(muxcode-jira.sh *)` must be in the agent's permissions (global settings)

## Changes

### 1. Create `skills/jira-pr-comment.md`

New skill file with instructions for the git-manager agent:

1. Extract Jira key from branch name (`PROJ-123-desc` → `PROJ-123`) — skip if no match
2. Gather PR metadata via `gh pr view --json number,title,body,url,additions,deletions,changedFiles`
3. Build ADF JSON with `jq`, write to temp file
4. POST comment via `muxcode-jira.sh comment "$jira_key" "$tmpfile"`
5. Report result back to edit agent

### 2. Add `Bash(curl*)` to `git` tool profile

**File**: `tools/muxcode-agent-bus/bus/profile.go`

Add `"Bash(curl*)"` to the `Tools` slice for the `"git"` profile entry. One-line addition — `curl` is already permitted in `deploy` and `runner` profiles.

### 3. Wrapper script `scripts/muxcode-jira.sh`

Handles config sourcing and curl+auth internally. Three commands: `read`, `update`, `comment`. Avoids inline curl+auth in agent commands that triggers Claude Code "quoted characters in flag names" permission prompts.

### 4. User setup (documented in skill, not committed)

Add to `.muxcode/config` or `~/.config/muxcode/config`:

```bash
JIRA_BASE_URL="https://your-org.atlassian.net"
JIRA_USER_EMAIL="you@example.com"
JIRA_API_TOKEN="your-atlassian-api-token"
```

## Acceptance criteria

- Build passes after `profile.go` change
- `muxcode-agent-bus skill list --role git` shows `jira-pr-comment`
- `muxcode-agent-bus skill load jira-pr-comment` renders skill content correctly
- On a branch named `PROJ-123-something`, with env vars set, creating a PR posts a comment to Jira
- On a branch without a Jira key prefix, the skill skips silently without breaking PR creation
- `muxcode-jira.sh comment PROJ-123 /tmp/payload.json` posts comment without triggering permission prompts
- Missing env vars cause script error (non-zero exit), skill reports failure gracefully
