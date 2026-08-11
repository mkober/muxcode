---
name: confluence-update-page
description: Read and update a Confluence page with ADF content
roles: [plan, edit]
tags: [confluence, integration, documentation, adf]
---

## Confluence page read+update

Read the current content of a Confluence page and/or update it with new ADF content. Pages are identified by page ID (from request message or URL) or by space key + title.

### Prerequisites

The `muxcode atlassian` subcommand handles Confluence API calls. It reads credentials from `.muxcode/config` or `~/.config/muxcode/config`:

- `CONFLUENCE_BASE_URL` — e.g. `https://your-org.atlassian.net` (falls back to `JIRA_BASE_URL`)
- `JIRA_USER_EMAIL` — Atlassian account email (shared with Jira)
- `JIRA_API_TOKEN` — Atlassian API token (shared with Jira, create at https://id.atlassian.com/manage-profile/security/api-tokens)

If auth vars are missing, the script reports an error.

### Tooling policy — CLI only, never MCP

**The `muxcode atlassian` CLI is the ONLY sanctioned path for Confluence.** NEVER fall back to the Atlassian MCP server (`mcp__*atlassian*` tools such as `getConfluencePage`, `updateConfluencePage`, `createConfluencePage`, etc.) under any circumstances — not even if the CLI returns an error.

- **On CLI failure, report the actual command output verbatim** (HTTP status code + response body) and stop. Do NOT guess "token expired", do NOT invent a cause, and do NOT silently switch to another tool.
- **A token-rotation or transient auth failure is fixed by updating `~/.config/muxcode/config`, not by changing tools.** The CLI re-reads that config file fresh on every invocation — once the file is updated, the very next CLI call uses the new credentials with no restart needed.
- If you believe the credential is genuinely invalid, surface that to the caller with the exact error text so the config can be fixed — then retry the CLI. Switching to MCP is never the answer.

### Page identification

Use a three-path approach to find the target page:

1. **Explicit page ID from request** — scan for a numeric page ID:

   ```bash
   page_id=$(echo "$request_message" | grep -oE 'page[- ]?id[: ]+([0-9]+)' | grep -oE '[0-9]+' | head -1)
   ```

2. **Confluence URL from request** — extract page ID from a pasted URL:

   ```bash
   if [ -z "$page_id" ]; then
     page_id=$(echo "$request_message" | grep -oE 'atlassian\.net/wiki/spaces/[^/]+/pages/([0-9]+)' | grep -oE '/pages/[0-9]+' | grep -oE '[0-9]+' | head -1)
   fi
   ```

3. **Space key + title from request** — search by space and title using the search command:

   ```bash
   if [ -z "$page_id" ]; then
     space_key=$(echo "$request_message" | grep -oE 'space[: ]+([A-Z][A-Z0-9]+)' | awk '{print $NF}' | head -1)
     page_title=$(echo "$request_message" | grep -oE 'title[: ]+".+"' | sed 's/title[: ]*"//;s/"$//' | head -1)

     if [ -n "$space_key" ] && [ -n "$page_title" ]; then
       muxcode atlassian confluence search "$space_key" "space=${space_key} AND title=\"${page_title}\""
     fi
   fi
   ```

If no page ID is found, report to the caller and stop.

### Read (GET)

Use the wrapper script to fetch the page:

```bash
muxcode atlassian confluence read "$page_id"
```

This outputs title, space, version info, URL, flattened content text, and raw ADF.

### ADF reference

Confluence uses the same Atlassian Document Format (ADF) as Jira. Building-block examples for composing the `content` array:

**Paragraph:**
```json
{
  "type": "paragraph",
  "content": [
    { "type": "text", "text": "Plain text here." }
  ]
}
```

**Heading (level 1-6):**
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
    }
  ]
}
```

**Ordered list:**
```json
{
  "type": "orderedList",
  "content": [
    {
      "type": "listItem",
      "content": [
        {
          "type": "paragraph",
          "content": [{ "type": "text", "text": "Step one" }]
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

**Table:**
```json
{
  "type": "table",
  "attrs": { "layout": "default" },
  "content": [
    {
      "type": "tableRow",
      "content": [
        {
          "type": "tableHeader",
          "content": [{ "type": "paragraph", "content": [{ "type": "text", "text": "Header" }] }]
        }
      ]
    },
    {
      "type": "tableRow",
      "content": [
        {
          "type": "tableCell",
          "content": [{ "type": "paragraph", "content": [{ "type": "text", "text": "Cell" }] }]
        }
      ]
    }
  ]
}
```

**Info panel:**
```json
{
  "type": "panel",
  "attrs": { "panelType": "info" },
  "content": [
    {
      "type": "paragraph",
      "content": [{ "type": "text", "text": "Info panel text." }]
    }
  ]
}
```

Panel types: `info`, `note`, `warning`, `success`, `error`.

**Inline link (via marks):**
```json
{
  "type": "text",
  "text": "Click here",
  "marks": [{ "type": "link", "attrs": { "href": "https://example.com" } }]
}
```

**Bold/italic (via marks):**
```json
{
  "type": "text",
  "text": "Bold text",
  "marks": [{ "type": "strong" }]
}
```

**Horizontal rule:**
```json
{ "type": "rule" }
```

**Expand (collapsible section):**
```json
{
  "type": "expand",
  "attrs": { "title": "Click to expand" },
  "content": [
    {
      "type": "paragraph",
      "content": [{ "type": "text", "text": "Hidden content." }]
    }
  ]
}
```

### Update (PUT)

Updates require the **current version number + 1**. The version number was captured during the read step.

Compose the full payload, write to a temp file, then use the wrapper:

```bash
new_version=$((version_number + 1))

content_array_string=$(jq -n --argjson blocks "$content_array" '{
  version: 1,
  type: "doc",
  content: $blocks
}' | jq -c '.')

payload=$(jq -n \
  --arg title "$title" \
  --argjson version "$new_version" \
  --arg adf_value "$content_array_string" \
  '{
    version: { number: $version, message: "Updated via MuxCode" },
    title: $title,
    type: "page",
    body: {
      atlas_doc_format: {
        value: $adf_value,
        representation: "atlas_doc_format"
      }
    }
  }')

tmpfile=$(mktemp /tmp/confluence-update-XXXXXX.json)
echo "$payload" > "$tmpfile"
muxcode atlassian confluence update "$page_id" "$tmpfile"
rm -f "$tmpfile"
```

Success output: `"Updated Confluence page <ID>"`

### Append mode

To add content to an existing page without replacing it, read the current ADF body first, parse its content array, append new blocks, and update:

```bash
# Parse existing content blocks (value is a stringified JSON string)
existing_blocks=$(echo "$adf_content" | jq -c 'fromjson | .content')

# Append new blocks
merged_blocks=$(jq -n --argjson existing "$existing_blocks" --argjson new_blocks "$content_array" '$existing + $new_blocks')

# Use merged_blocks as the content array for the update
```

### Search via CQL

Find pages by label, ancestor, or full-text search:

```bash
muxcode atlassian confluence search "$space_key" "space=${space_key} AND title=\"${search_title}\""
```

Common CQL patterns:
- `space=KEY AND title="Page Title"` — exact title in space
- `space=KEY AND ancestor=123456` — child pages under a parent
- `space=KEY AND label=my-label` — pages with a specific label
- `space=KEY AND text~"search term"` — full-text search

### Reporting

Send a message to edit with the outcome:

- **Read success**: `"Confluence page ${page_id}: ${title} [${space_key}] v${version_number} — content fetched"`
- **Update success**: `"Updated Confluence page ${page_id}: ${title} — now v${new_version}"`
- **Search results**: `"Found ${count} pages matching query in ${space_key}"`
- **Failure**: report the error output from the script

### Error handling

- No page ID from request, URL, or search: report to caller that a page ID, URL, or space+title is needed
- `jq` not available: skip silently (do not break the calling workflow)
- Version conflict (HTTP 409): re-read the page to get the latest version, then retry the update once
- Script errors (non-zero exit): report the **exact** error output (HTTP status + body) to edit, but do not fail the overall workflow. Do NOT fall back to the Atlassian MCP — see "Tooling policy" above.
- Auth errors (HTTP 401/403): report the verbatim error and note that `~/.config/muxcode/config` may need a fresh `JIRA_API_TOKEN`. Do NOT switch tools; once the config is updated, retry the same CLI command.
