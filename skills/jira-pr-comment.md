---
name: jira-pr-comment
description: Post a comment on a Jira issue when a PR is created
roles: [git]
tags: [jira, github, pr, integration]
---

## Jira PR comment

After creating a PR with `gh pr create`, post a comment on the corresponding Jira issue with PR details. The Jira issue key is extracted from the branch name.

### Prerequisites

Three environment variables must be set (in `.muxcode/config` or `~/.config/muxcode/config`):

- `JIRA_BASE_URL` — e.g. `https://your-org.atlassian.net`
- `JIRA_USER_EMAIL` — Atlassian account email
- `JIRA_API_TOKEN` — Atlassian API token (create at https://id.atlassian.com/manage-profile/security/api-tokens)

If any are missing, skip the Jira comment silently — do not treat it as an error.

### Steps

1. **Check env vars** — if `JIRA_BASE_URL`, `JIRA_USER_EMAIL`, or `JIRA_API_TOKEN` is empty or unset, skip silently.

2. **Extract Jira key from branch name** — get the current branch name and match the leading Jira key pattern (`PROJ-123`). The key is one or more uppercase letters, a hyphen, then one or more digits at the start of the branch name. Example: `DATA-456-add-validation` yields `DATA-456`. If no match, skip silently.

   ```bash
   branch=$(git rev-parse --abbrev-ref HEAD)
   jira_key=$(echo "$branch" | grep -oE '^[A-Z]+-[0-9]+')
   ```

3. **Gather PR metadata** — use `gh pr view` on the current branch:

   ```bash
   gh pr view --json number,title,url,additions,deletions,changedFiles
   ```

4. **Build ADF comment payload** — construct the Atlassian Document Format JSON with `jq`. Include the PR link, title, and diff stats:

   ```bash
   jq -n \
     --arg url "$pr_url" \
     --arg title "$pr_title" \
     --argjson num "$pr_number" \
     --argjson adds "$pr_additions" \
     --argjson dels "$pr_deletions" \
     --argjson files "$pr_changed_files" \
     '{
       body: {
         version: 1,
         type: "doc",
         content: [
           {
             type: "paragraph",
             content: [
               { type: "text", text: "Pull Request: " },
               {
                 type: "text",
                 text: ("#" + ($num | tostring) + " " + $title),
                 marks: [{ type: "link", attrs: { href: $url } }]
               }
             ]
           },
           {
             type: "paragraph",
             content: [
               {
                 type: "text",
                 text: ("+" + ($adds | tostring) + " / -" + ($dels | tostring) + " across " + ($files | tostring) + " files")
               }
             ]
           }
         ]
       }
     }'
   ```

5. **POST comment to Jira** — use Basic auth with the Atlassian REST API v3:

   ```bash
   curl -s -w "\n%{http_code}" \
     -u "${JIRA_USER_EMAIL}:${JIRA_API_TOKEN}" \
     -H "Content-Type: application/json" \
     -X POST \
     -d "$payload" \
     "${JIRA_BASE_URL}/rest/api/3/issue/${jira_key}/comment"
   ```

   Check the HTTP status code — `201` means success. Report the result back to the edit agent.

6. **Report result** — send a message to edit with the outcome:
   - Success: `"Posted PR comment to Jira issue ${jira_key}"`
   - Failure: `"Failed to post Jira comment for ${jira_key} (HTTP ${status})"`

### Error handling

- Missing env vars: skip silently, do not report an error
- No Jira key in branch name: skip silently
- `jq` not available: skip the Jira comment (do not break PR creation)
- Jira API error (non-201 response): report failure to edit but do not fail the overall PR workflow
