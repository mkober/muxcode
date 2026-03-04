---
name: jira-update-description
description: Read and update a Jira issue description with ADF content
roles: [git, edit]
tags: [jira, integration, description, adf]
---

## Jira issue description read+update

Read the current description of a Jira issue and/or update it with new ADF content. The Jira issue key is extracted from the request message or falls back to the branch name.

### Prerequisites

Three environment variables must be set (in `.muxcode/config` or `~/.config/muxcode/config`):

- `JIRA_BASE_URL` — e.g. `https://your-org.atlassian.net`
- `JIRA_USER_EMAIL` — Atlassian account email
- `JIRA_API_TOKEN` — Atlassian API token (create at https://id.atlassian.com/manage-profile/security/api-tokens)

If any are missing, skip silently — do not treat it as an error.

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

Fetch the current issue description along with context fields:

```bash
response=$(curl -s -w "\n%{http_code}" \
  -u "${JIRA_USER_EMAIL}:${JIRA_API_TOKEN}" \
  -H "Content-Type: application/json" \
  "${JIRA_BASE_URL}/rest/api/3/issue/${jira_key}?fields=description,summary,status,assignee")

body=$(echo "$response" | sed '$d')
status=$(echo "$response" | tail -1)
```

Check the HTTP status code — `200` means success.

Parse context fields from the response:

```bash
summary=$(echo "$body" | jq -r '.fields.summary // "No summary"')
issue_status=$(echo "$body" | jq -r '.fields.status.name // "Unknown"')
assignee=$(echo "$body" | jq -r '.fields.assignee.displayName // "Unassigned"')
```

Flatten ADF description text nodes into a human-readable preview:

```bash
description_text=$(echo "$body" | jq -r '
  [.fields.description // empty | .. | .text? // empty] | join(" ")
')
```

Report context to edit:
- `"Jira ${jira_key}: ${summary} [${issue_status}, ${assignee}]"`
- Include the flattened description text

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

Compose the ADF `content` array as a JSON value, then inject it into the wrapper via `jq -n --argjson`:

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
```

Send the update — success is **204 No Content** (not 200):

```bash
response=$(curl -s -w "\n%{http_code}" \
  -u "${JIRA_USER_EMAIL}:${JIRA_API_TOKEN}" \
  -H "Content-Type: application/json" \
  -X PUT \
  -d "$payload" \
  "${JIRA_BASE_URL}/rest/api/3/issue/${jira_key}")

status=$(echo "$response" | tail -1)
```

Check the HTTP status code — `204` means success.

### Reporting

Send a message to edit with the outcome:

- **Read success**: `"Jira ${jira_key}: ${summary} [${issue_status}, ${assignee}] — description fetched"`
- **Update success**: `"Updated description for Jira issue ${jira_key}"`
- **Failure**: `"Failed to update Jira description for ${jira_key} (HTTP ${status})"`

### Error handling

- Missing env vars: skip silently, do not report an error
- No Jira key from request or branch name: skip silently
- `jq` not available: skip silently (do not break the calling workflow)
- Jira API error (non-200 on GET, non-204 on PUT): report failure to edit but do not fail the overall workflow
