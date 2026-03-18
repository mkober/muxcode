---
name: jira-pr-comment
description: Post a comment on a Jira issue when a PR is created
roles: [git]
tags: [jira, github, pr, integration]
---

## Jira PR comment

After creating a PR with `gh pr create`, post a comment on the corresponding Jira issue with PR details. The Jira issue key is extracted from the branch name.

### Prerequisites

The `muxcode-jira.sh` helper script must be installed (included in `make install`). It reads credentials from `.muxcode/config` or `~/.config/muxcode/config`:

- `JIRA_BASE_URL` — e.g. `https://your-org.atlassian.net`
- `JIRA_USER_EMAIL` — Atlassian account email
- `JIRA_API_TOKEN` — Atlassian API token (create at https://id.atlassian.com/manage-profile/security/api-tokens)

If any are missing, the script reports an error. Skip the Jira comment silently if the script fails.

### Steps

1. **Extract Jira key from branch name** — get the current branch name and match the leading Jira key pattern (`PROJ-123`). The key starts with an uppercase letter followed by uppercase letters or digits, a hyphen, then one or more digits. Examples: `DATA-456-add-validation` yields `DATA-456`, `PBP1-4365-fix-bug` yields `PBP1-4365`. If no match, skip silently.

   ```bash
   branch=$(git rev-parse --abbrev-ref HEAD)
   jira_key=$(echo "$branch" | grep -oE '^[A-Z][A-Z0-9]*-[0-9]+')
   ```

2. **Gather PR metadata** — use `gh pr view` on the current branch:

   ```bash
   gh pr view --json number,title,url,additions,deletions,changedFiles
   ```

3. **Build ADF comment payload** — construct the Atlassian Document Format JSON with `jq`, write to a temp file:

   ```bash
   payload=$(jq -n \
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
     }')

   tmpfile=$(mktemp /tmp/jira-comment-XXXXXX.json)
   echo "$payload" > "$tmpfile"
   ```

4. **POST comment to Jira** — use the wrapper script:

   ```bash
   muxcode-jira.sh comment "$jira_key" "$tmpfile"
   rm -f "$tmpfile"
   ```

   Success output: `"Posted comment to <KEY>"`

5. **Report result** — send a message to edit with the outcome:
   - Success: `"Posted PR comment to Jira issue ${jira_key}"`
   - Failure: report the error output from the script

### Error handling

- No Jira key in branch name: skip silently
- `jq` not available: skip the Jira comment (do not break PR creation)
- Script errors (non-zero exit): report failure to edit but do not fail the overall PR workflow
