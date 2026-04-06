---
name: github-pr-comment
description: Post threaded replies to Copilot review comments and a summary comment on a GitHub PR
roles: [git]
tags: [github, pr, copilot, review]
---

## GitHub PR comment

After pushing commits that address Copilot (or other reviewer) feedback, post threaded replies to each inline review comment and a summary comment on the PR. This ensures every comment thread has a visible response explaining how it was addressed.

If there are no Copilot review comments on the PR, skip this skill entirely.

### Steps

1. **Gather PR metadata** — use `gh pr view` on the current branch:

   ```bash
   gh pr view --json number,title,url,additions,deletions,changedFiles
   owner_repo=$(gh repo view --json nameWithOwner -q '.nameWithOwner')
   ```

2. **Gather Copilot review comments** — fetch all inline review comments on the PR:

   ```bash
   comments_json=$(gh api --paginate "repos/${owner_repo}/pulls/${pr_number}/comments" \
     --jq '.[] | {id, path, line, start_line, body, user: .user.login}')
   ```

   Filter for Copilot-authored comments (login `copilot-pull-request-reviewer` or `github-actions[bot]` with Copilot context, or any comment from a bot with `copilot` in the name). For each comment, extract:
   - `id` — the comment ID (needed for threaded replies)
   - `path` — the file referenced
   - `line` or `start_line` — the line number
   - `body` — the review comment text

   Also check for PR review summaries:

   ```bash
   reviews_json=$(gh api --paginate "repos/${owner_repo}/pulls/${pr_number}/reviews" \
     --jq '.[] | {state, body, user: .user.login}')
   ```

   Filter reviews where `user.login` contains `copilot` or where `user.type` is `Bot`.

3. **Analyze how each comment was addressed** — for each Copilot comment, examine the diff between the commit referenced in the comment and HEAD. Read the changed files to understand what fix was applied. Write a clear, specific explanation — not just "Fixed" but what was actually changed (e.g. "Fixed by adding default value `[\"STUDENT_USERS\"]` at line 133").

   For comments that were not addressed (out of scope, pre-existing, etc.), note the reason.

4. **Post threaded replies to each review comment** — this is the **primary** response mechanism. Every Copilot comment **must** get a direct threaded reply:

   ```bash
   # For each Copilot review comment, post a reply in its thread
   gh api "repos/${owner_repo}/pulls/${pr_number}/comments/${comment_id}/replies" \
     -f body="Fixed in ${commit_sha} — <specific explanation of what was changed>"
   ```

   **Reply guidelines**:
   - Be specific: not just "Fixed" but "Fixed by adding default value at line 133"
   - Reference the commit hash where the fix was applied
   - For out-of-scope items: "Not addressed — <reason> (pre-existing condition, different PR scope, etc.)"
   - If a reply fails (404 or error), log the failure and continue with the next comment

5. **Post a summary comment on the PR** — after all threaded replies are posted, add a general summary comment:

   ```bash
   gh pr comment --body "$(cat <<'EOF'
   ## Copilot Review Feedback Addressed

   All review comments have been addressed in commit <short-sha>:

   - **<issue summary>** (<file>:<line>): <specific fix description>
   - **<issue summary>** (<file>:<line>): <specific fix description>
   - **<out-of-scope issue>** (<file>:<line>): <reason not addressed>
   EOF
   )"
   ```

6. **Report result** — send a message to edit with the outcome:
   - Success: `"Posted ${reply_count} threaded replies and summary comment on PR #${pr_number}"`
   - Failure: report which replies failed and why

### Error handling

- No Copilot review comments found: skip entirely (do not post empty summary)
- Threaded reply fails (404 or error): log the failure, continue with remaining comments. The summary comment serves as a fallback
- `gh` CLI errors: report failure to edit but do not fail the overall workflow
