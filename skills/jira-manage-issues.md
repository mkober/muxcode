---
name: jira-manage-issues
description: Read, update, search, transition, and link Jira issues — full issue lifecycle management
roles: [commit, edit]
tags: [jira, integration, description, adf, links, dependencies, transitions, search, subtasks, comments]
---

## Jira issue management

Full Jira issue lifecycle: read, update descriptions, link dependencies, transition status, search via JQL, read/post comments, and create subtasks. The Jira issue key is extracted from the request message or falls back to the branch name.

### Prerequisites

The `muxcode atlassian` subcommand handles Jira API calls. It reads credentials from `.muxcode/config` or `~/.config/muxcode/config`:

- `JIRA_BASE_URL` — e.g. `https://your-org.atlassian.net`
- `JIRA_USER_EMAIL` — Atlassian account email
- `JIRA_API_TOKEN` — Atlassian API token (create at https://id.atlassian.com/manage-profile/security/api-tokens)

If any are missing, the command reports an error.

### Tooling policy — CLI only, never MCP

**The `muxcode atlassian` CLI is the ONLY sanctioned path for Jira.** NEVER fall back to the Atlassian MCP server (`mcp__*atlassian*` tools such as `getJiraIssue`, `editJiraIssue`, `addCommentToJiraIssue`, `transitionJiraIssue`, etc.) under any circumstances — not even if the CLI returns an error.

- **On CLI failure, report the actual command output verbatim** (HTTP status code + response body) and stop. Do NOT guess "token expired", do NOT invent a cause, and do NOT silently switch to another tool.
- **A token-rotation or transient auth failure is fixed by updating `~/.config/muxcode/config`, not by changing tools.** The CLI re-reads that config file fresh on every invocation — once the file is updated, the very next CLI call uses the new credentials with no restart needed.
- If you believe the credential is genuinely invalid, surface that to the caller with the exact error text so the config can be fixed — then retry the CLI. Switching to MCP is never the answer.

### Key identification

Use a two-path approach to find the Jira issue key:

1. **Explicit key from request** — scan the incoming request message for a Jira key pattern:

   ```bash
   jira_key=$(echo "$request_message" | grep -oE '[A-Z][A-Z0-9]*-[0-9]+' | head -1)
   ```

2. **Branch name fallback** — if no key found in the message, extract from the current branch:

   ```bash
   if [ -z "$jira_key" ]; then
     branch=$(git rev-parse --abbrev-ref HEAD)
     jira_key=$(echo "$branch" | grep -oE '^[A-Z][A-Z0-9]*-[0-9]+')
   fi
   ```

If neither yields a key, skip silently.

### Read (GET)

Fetch the issue using the bus binary:

```bash
muxcode atlassian jira read "$jira_key"
```

This outputs summary, type, priority, status, assignee, existing issue links (with direction, status, and summary of linked issues), parent issue (if any), subtasks, and the flattened description text.

### ADF reference

Building-block examples for composing the `content` array. Each is a standalone JSON fragment.

**Paragraph:**
```json
{
  "type": "paragraph",
  "content": [
    { "type": "text", "text": "Plain text here." }
  ]
}
```

**Heading (level 2):**
```json
{
  "type": "heading",
  "attrs": { "level": 2 },
  "content": [
    { "type": "text", "text": "Section title" }
  ]
}
```

**Bullet list:**
```json
{
  "type": "bulletList",
  "content": [
    {
      "type": "listItem",
      "content": [
        {
          "type": "paragraph",
          "content": [{ "type": "text", "text": "First item" }]
        }
      ]
    },
    {
      "type": "listItem",
      "content": [
        {
          "type": "paragraph",
          "content": [{ "type": "text", "text": "Second item" }]
        }
      ]
    }
  ]
}
```

**Code block:**
```json
{
  "type": "codeBlock",
  "attrs": { "language": "bash" },
  "content": [
    { "type": "text", "text": "echo hello" }
  ]
}
```

**Inline link (via marks):**
```json
{
  "type": "text",
  "text": "Click here",
  "marks": [{ "type": "link", "attrs": { "href": "https://example.com" } }]
}
```

**Horizontal rule:**
```json
{ "type": "rule" }
```

### Update (PUT)

Compose the ADF `content` array as a JSON value, write to a temp file, then use the wrapper:

```bash
payload=$(jq -n --argjson blocks "$content_array" '{
  fields: {
    description: {
      version: 1,
      type: "doc",
      content: $blocks
    }
  }
}')

tmpfile=$(mktemp /tmp/jira-update-XXXXXX.json)
echo "$payload" > "$tmpfile"
muxcode atlassian jira update "$jira_key" "$tmpfile"
rm -f "$tmpfile"
```

Success output: `"Updated description for <KEY>"`

### Link related issues

Create dependency links between Jira issues — useful when a requirements doc references pre-requisite stories or related work items.

#### Discover available link types

Each Jira instance has its own set of link types. List them first:

```bash
muxcode atlassian jira link-types
```

Example output:
```
=== Available Issue Link Types ===
Blocks                outward: blocks                    inward: is blocked by
Dependency            outward: depends on                inward: is depended on by
Relates               outward: relates to                inward: relates to
```

Common link types and when to use them:

| Link type | Use when |
|-----------|----------|
| `Blocks` | Issue A must complete before B can start (hard blocker) |
| `Dependency` | Issue A depends on B (pre-requisite in requirements) |
| `Relates` | Issues are related but not blocking |

#### Create a link

The `link` command takes three arguments: the link type name, the source issue key, and the target issue key.

**Argument order**: `<TYPE> <SOURCE-KEY> <TARGET-KEY>` — reads naturally as "SOURCE [type] TARGET".

```bash
# "PROJ-200 blocks PROJ-100" (PROJ-200 is a pre-req for PROJ-100)
muxcode atlassian jira link "Blocks" "PROJ-200" "PROJ-100"
```

This means: PROJ-200 **blocks** PROJ-100, or equivalently PROJ-100 **is blocked by** PROJ-200.

**Dependency example** — when a requirements doc says "Story B depends on Story A":

```bash
# "PROJ-B depends on PROJ-A"
muxcode atlassian jira link "Dependency" "PROJ-B" "PROJ-A"
```

#### Extracting dependencies from a requirements doc

When a requirements document references pre-requisite stories, extract the Jira keys and create links:

1. Read the current issue to get context:
   ```bash
   muxcode atlassian jira read "$jira_key"
   ```

2. Identify referenced Jira keys in the description or requirements text. Look for patterns like:
   - "depends on PROJ-123"
   - "requires PROJ-456 to be completed first"
   - "pre-requisite: PROJ-789"
   - "blocked by PROJ-321"

3. Discover available link types to find the right one:
   ```bash
   muxcode atlassian jira link-types
   ```

4. Create the appropriate link for each dependency:
   ```bash
   # For each pre-requisite referenced in the requirements
   # prereq_key blocks jira_key (prereq must complete first)
   muxcode atlassian jira link "Blocks" "$prereq_key" "$jira_key"
   ```

Success output: `"Linked PROJ-200 -[Blocks]-> PROJ-100"` (PROJ-200 blocks PROJ-100)

### Transition issue status

Move an issue through workflow states (e.g. To Do -> In Progress -> Done). Transitions are issue-specific — available transitions depend on the current status and workflow.

#### List available transitions

```bash
muxcode atlassian jira transitions "$jira_key"
```

Example output:
```
=== Available Transitions for PROJ-123 ===
  ID: 11      In Progress                -> In Progress
  ID: 21      Done                       -> Done
  ID: 31      Review                     -> In Review
```

#### Execute a transition

Use the transition ID (not the name) from the list above:

```bash
# Move to "In Progress" (transition ID 11)
muxcode atlassian jira transition "$jira_key" "11"
```

Success output: `"Transitioned PROJ-123 via transition 11"`

**Common workflow**: when starting work on a story, transition it to "In Progress":

```bash
# 1. List transitions to find the right ID
muxcode atlassian jira transitions "$jira_key"
# 2. Execute the transition
muxcode atlassian jira transition "$jira_key" "$transition_id"
```

### Search issues via JQL

Query for issues using Jira Query Language. Useful for finding related work items, checking sprint backlogs, or discovering issues to link as dependencies.

```bash
muxcode atlassian jira search "project = PROJ AND status = 'To Do' ORDER BY priority DESC"
```

Example output:
```
=== Jira Search Results (3 of 3) ===
JQL: project = PROJ AND status = 'To Do' ORDER BY priority DESC

PROJ-456      [To Do       ]  Story       Add user authentication
PROJ-789      [To Do       ]  Bug         Fix login redirect loop
PROJ-321      [To Do       ]  Task        Update API documentation
```

Common JQL patterns:

| Query | Use case |
|-------|----------|
| `project = PROJ AND sprint in openSprints()` | Current sprint issues |
| `project = PROJ AND labels = "backend"` | Issues with a specific label |
| `project = PROJ AND issuekey in linkedIssues("PROJ-100")` | Issues linked to a specific issue |
| `project = PROJ AND status = "In Progress" AND assignee = currentUser()` | Your in-progress work |
| `project = PROJ AND text ~ "authentication"` | Full-text search |

Returns up to 50 results. The total count is shown in the header.

### Read comments

Fetch existing comments on an issue (newest first, up to 50):

```bash
muxcode atlassian jira comments "$jira_key"
```

Example output:
```
=== Comments on PROJ-123 (2) ===

--- Jane Smith at 2026-04-13T10:30:00.000+0000 ---
Updated the acceptance criteria based on the design review.

--- John Doe at 2026-04-12T15:45:00.000+0000 ---
Initial requirements look good, but we need to clarify the edge cases.
```

This is useful for understanding discussion context before posting a new comment or updating the description.

### Create subtasks

Break a story into subtasks. The project key is auto-derived from the parent key if not provided.

```bash
# Auto-derive project key from parent (PROJ-123 -> PROJ)
muxcode atlassian jira create-subtask "PROJ-123" "Implement login form"

# Explicit project key
muxcode atlassian jira create-subtask "PROJ-123" "Implement login form" "PROJ"
```

Success output: `"Created subtask PROJ-456 under PROJ-123: Implement login form"`

**Breaking down a requirements doc into subtasks**:

1. Read the parent story to understand scope:
   ```bash
   muxcode atlassian jira read "$jira_key"
   ```

2. Create subtasks for each logical piece of work:
   ```bash
   muxcode atlassian jira create-subtask "$jira_key" "Design database schema"
   muxcode atlassian jira create-subtask "$jira_key" "Implement API endpoints"
   muxcode atlassian jira create-subtask "$jira_key" "Add unit tests"
   muxcode atlassian jira create-subtask "$jira_key" "Update documentation"
   ```

3. Verify the subtasks were created:
   ```bash
   muxcode atlassian jira read "$jira_key"
   ```

### Reporting

Send a message to edit with the outcome:

- **Read success**: `"Jira ${jira_key}: ${summary} [${issue_status}, ${assignee}] — description fetched"`
- **Update success**: `"Updated description for Jira issue ${jira_key}"`
- **Link success**: `"Linked ${source_key} -[${link_type}]-> ${target_key}"`
- **Link types listed**: `"Found ${count} link types on Jira instance"`
- **Transition success**: `"Transitioned ${jira_key} via transition ${transition_id}"`
- **Search success**: `"Found ${count} issues matching JQL query"`
- **Comments read**: `"Read ${count} comments on ${jira_key}"`
- **Subtask created**: `"Created subtask ${new_key} under ${parent_key}"`
- **Failure**: report the error output from the script

### Error handling

- No Jira key from request or branch name: skip silently
- `jq` not available: skip silently (do not break the calling workflow)
- Script errors (non-zero exit): report the **exact** error output (HTTP status + body) to edit, but do not fail the overall workflow. Do NOT fall back to the Atlassian MCP — see "Tooling policy" above.
- Auth errors (HTTP 401/403): report the verbatim error and note that `~/.config/muxcode/config` may need a fresh `JIRA_API_TOKEN`. Do NOT switch tools; once the config is updated, retry the same CLI command.
