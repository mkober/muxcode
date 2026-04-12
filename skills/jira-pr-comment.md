---
name: jira-pr-comment
description: Post a comment on a Jira issue when a PR is created
roles: [git, commit]
tags: [jira, github, pr, integration]
---

## Jira PR comment

After creating a PR with `gh pr create`, post a comment on the corresponding Jira issue with PR details and a summary of addressed Copilot review feedback. The Jira issue key is extracted from the branch name.

**Companion skill**: `github-pr-comment` handles threaded replies to individual Copilot review comments on GitHub. Run that skill first (or in parallel) when Copilot feedback exists.

### Prerequisites

The `muxcode atlassian` subcommand handles Jira API calls. It reads credentials from `.muxcode/config` or `~/.config/muxcode/config`:

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

3. **Gather Copilot review feedback** — fetch PR review comments to build a summary of addressed Copilot issues. Use `gh api` to get all review comments on the PR:

   ```bash
   owner_repo=$(gh repo view --json nameWithOwner -q '.nameWithOwner')
   comments_json=$(gh api "repos/${owner_repo}/pulls/${pr_number}/comments" --paginate)
   ```

   Filter for Copilot-authored comments (login `copilot-pull-request-reviewer` or `github-actions[bot]` with Copilot context, or any comment from a bot with `copilot` in the name). For each comment, extract:
   - `path` — the file referenced
   - `line` or `original_line` — the line number
   - `body` — the review comment text

   Also check for PR review threads via:

   ```bash
   reviews_json=$(gh api "repos/${owner_repo}/pulls/${pr_number}/reviews" --paginate)
   ```

   Filter reviews where `user.login` contains `copilot` or where `user.type` is `Bot`.

4. **Build PR issue summary** — if Copilot comments were found, compose a summary section. For each addressed comment, write a concise one-line description of the issue and how it was resolved. Group by status:

   **Format:**
   ```
   Github PR Issue Summary

   All review comments have been addressed in commit <short-sha>:

   - <summary of issue> (<file>:<line>): <how it was fixed>
   - <summary of issue> (<file>:<line>): <how it was fixed>
   - ...

   If any comment was not addressed (out of scope, pre-existing, etc.):
   - <summary of issue> (<file>:<line>): <reason it was not addressed>
   ```

   To determine how each comment was addressed, examine the diff between the commit referenced in the comment and HEAD. Read the changed files to understand what fix was applied. Write a clear, specific explanation — not just "Fixed" but what was actually changed (e.g. "Fixed by increasing SQS visibility timeout to 20 minutes").

   **Always include this summary** when there are Copilot review comments, even if all comments are out of scope. If there are no Copilot comments on the PR, skip this section entirely.

5. **Build ADF comment payload** — construct the Atlassian Document Format JSON with `jq`, write to a temp file. The payload includes the PR link, stats, and the PR issue summary (if any):

   ```bash
   # Build the base content blocks
   content_blocks='[
     {
       "type": "paragraph",
       "content": [
         { "type": "text", "text": "Pull Request: " },
         {
           "type": "text",
           "text": "#'"${pr_number}"' '"${pr_title}"'",
           "marks": [{ "type": "link", "attrs": { "href": "'"${pr_url}"'" } }]
         }
       ]
     },
     {
       "type": "paragraph",
       "content": [
         {
           "type": "text",
           "text": "+'"${pr_additions}"' / -'"${pr_deletions}"' across '"${pr_changed_files}"' files"
         }
       ]
     }
   ]'

   # If Copilot feedback was found, append the summary section
   if [ -n "$copilot_summary" ]; then
     # Add a horizontal rule separator
     content_blocks=$(echo "$content_blocks" | jq '. + [{ "type": "rule" }]')

     # Add "Github PR Issue Summary" heading
     content_blocks=$(echo "$content_blocks" | jq '. + [{
       "type": "heading",
       "attrs": { "level": 3 },
       "content": [{ "type": "text", "text": "Github PR Issue Summary" }]
     }]')

     # Add intro paragraph with commit reference
     content_blocks=$(echo "$content_blocks" | jq --arg msg "$copilot_intro" '. + [{
       "type": "paragraph",
       "content": [{ "type": "text", "text": $msg }]
     }]')

     # Add each feedback item as a bullet list
     # $copilot_list_items is a jq-compatible JSON array of listItem objects
     content_blocks=$(echo "$content_blocks" | jq --argjson items "$copilot_list_items" '. + [{
       "type": "bulletList",
       "content": $items
     }]')
   fi

   payload=$(jq -n --argjson blocks "$content_blocks" '{
     body: {
       version: 1,
       type: "doc",
       content: $blocks
     }
   }')

   tmpfile=$(mktemp /tmp/jira-comment-XXXXXX.json)
   echo "$payload" > "$tmpfile"
   ```

   Each bullet list item follows this ADF structure:

   ```json
   {
     "type": "listItem",
     "content": [{
       "type": "paragraph",
       "content": [
         { "type": "text", "text": "Issue summary ", "marks": [{ "type": "strong" }] },
         { "type": "text", "text": "(file.ts:42)" },
         { "type": "text", "text": ": How it was fixed." }
       ]
     }]
   }
   ```

6. **POST comment to Jira** — use the wrapper script:

   ```bash
   muxcode atlassian jira comment "$jira_key" "$tmpfile"
   rm -f "$tmpfile"
   ```

   Success output: `"Posted comment to <KEY>"`

7. **Report result** — send a message to edit with the outcome:
   - Success: `"Posted PR comment to Jira issue ${jira_key}"` (include whether Copilot summary was included)
   - Failure: report the error output from the script

### Error handling

- No Jira key in branch name: skip silently
- `jq` not available: skip the Jira comment (do not break PR creation)
- Copilot comment fetch fails: post the Jira comment without the Copilot summary (degrade gracefully)
- Script errors (non-zero exit): report failure to edit but do not fail the overall PR workflow
