---
name: jira-update-description
description: Read and update a Jira issue description with ADF content
roles: [git, edit]
tags: [jira, integration, description, adf]
---

## Jira issue description read+update

Read the current description of a Jira issue and/or update it with new ADF content. The Jira issue key is extracted from the request message or falls back to the branch name.

### Prerequisites

The `muxcode-agent-bus atlassian` subcommand handles Jira API calls. It reads credentials from `.muxcode/config` or `~/.config/muxcode/config`:

- `JIRA_BASE_URL` — e.g. `https://your-org.atlassian.net`
- `JIRA_USER_EMAIL` — Atlassian account email
- `JIRA_API_TOKEN` — Atlassian API token (create at https://id.atlassian.com/manage-profile/security/api-tokens)

If any are missing, the command reports an error.

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
muxcode-agent-bus atlassian jira read "$jira_key"
```

This outputs summary, type, priority, status, assignee, and the flattened description text.

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
muxcode-agent-bus atlassian jira update "$jira_key" "$tmpfile"
rm -f "$tmpfile"
```

Success output: `"Updated description for <KEY>"`

### Reporting

Send a message to edit with the outcome:

- **Read success**: `"Jira ${jira_key}: ${summary} [${issue_status}, ${assignee}] — description fetched"`
- **Update success**: `"Updated description for Jira issue ${jira_key}"`
- **Failure**: report the error output from the script

### Error handling

- No Jira key from request or branch name: skip silently
- `jq` not available: skip silently (do not break the calling workflow)
- Script errors (non-zero exit): report failure to edit but do not fail the overall workflow
