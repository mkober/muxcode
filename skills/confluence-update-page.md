---
name: confluence-update-page
description: Read and update a Confluence page with ADF content
roles: [git, edit]
tags: [confluence, integration, documentation, adf]
---

## Confluence page read+update

Read the current content of a Confluence page and/or update it with new ADF content. Pages are identified by page ID (from request message or URL) or by space key + title.

### Prerequisites

Environment variables must be set (in `.muxcode/config` or `~/.config/muxcode/config`):

- `CONFLUENCE_BASE_URL` — e.g. `https://your-org.atlassian.net` (falls back to `JIRA_BASE_URL`)
- `JIRA_USER_EMAIL` — Atlassian account email (shared with Jira)
- `JIRA_API_TOKEN` — Atlassian API token (shared with Jira, create at https://id.atlassian.com/manage-profile/security/api-tokens)

If auth vars are missing, skip silently — do not treat it as an error.

### Base URL resolution

```bash
base_url="${CONFLUENCE_BASE_URL:-${JIRA_BASE_URL}}"
if [ -z "$base_url" ] || [ -z "$JIRA_USER_EMAIL" ] || [ -z "$JIRA_API_TOKEN" ]; then
  # skip silently
  exit 0
fi
```

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

3. **Space key + title from request** — search by space and title. Look for patterns like `space:KEY title:Some Page` or `KEY/Some Page`:

   ```bash
   if [ -z "$page_id" ]; then
     space_key=$(echo "$request_message" | grep -oE 'space[: ]+([A-Z][A-Z0-9]+)' | awk '{print $NF}' | head -1)
     page_title=$(echo "$request_message" | grep -oE 'title[: ]+".+"' | sed 's/title[: ]*"//;s/"$//' | head -1)

     if [ -n "$space_key" ] && [ -n "$page_title" ]; then
       encoded_title=$(printf '%s' "$page_title" | jq -sRr @uri)
       response=$(curl -s -w "\n%{http_code}" \
         -u "${JIRA_USER_EMAIL}:${JIRA_API_TOKEN}" \
         -H "Content-Type: application/json" \
         "${base_url}/wiki/rest/api/content?spaceKey=${space_key}&title=${encoded_title}&expand=version")

       status=$(echo "$response" | tail -1)
       body=$(echo "$response" | sed '$d')

       if [ "$status" = "200" ]; then
         page_id=$(echo "$body" | jq -r '.results[0].id // empty')
       fi
     fi
   fi
   ```

If no page ID is found, report to the caller and stop.

### Read (GET)

Fetch the current page content with version info. Use the v1 API with `atlas_doc_format` body expansion:

```bash
response=$(curl -s -w "\n%{http_code}" \
  -u "${JIRA_USER_EMAIL}:${JIRA_API_TOKEN}" \
  -H "Content-Type: application/json" \
  "${base_url}/wiki/rest/api/content/${page_id}?expand=body.atlas_doc_format,version,space,ancestors")

body=$(echo "$response" | sed '$d')
status=$(echo "$response" | tail -1)
```

Check the HTTP status code — `200` means success.

Parse context fields from the response:

```bash
title=$(echo "$body" | jq -r '.title // "Untitled"')
space_key=$(echo "$body" | jq -r '.space.key // "Unknown"')
space_name=$(echo "$body" | jq -r '.space.name // "Unknown"')
version_number=$(echo "$body" | jq -r '.version.number // 1')
version_by=$(echo "$body" | jq -r '.version.by.displayName // "Unknown"')
version_when=$(echo "$body" | jq -r '.version.when // "Unknown"')
page_url="${base_url}/wiki/spaces/${space_key}/pages/${page_id}"
```

Extract the ADF body content. The `value` field is a **stringified JSON string** — use `fromjson` to parse it into a usable ADF object:

```bash
adf_content=$(echo "$body" | jq -r '.body.atlas_doc_format.value // empty')
```

Flatten ADF into a human-readable preview (parse the string into JSON first):

```bash
content_text=$(echo "$adf_content" | jq -r '
  fromjson | [.. | .text? // empty] | join(" ")
')
```

Report context:
- `"Confluence page ${page_id}: ${title} [${space_key}] — v${version_number} by ${version_by}"`
- Include the flattened content text
- Include the page URL for reference

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

Compose the ADF `content` array as a JSON value, then build the full payload:

```bash
new_version=$((version_number + 1))

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
```

Where `$content_array_string` is the full ADF document as a JSON string:

```bash
content_array_string=$(jq -n --argjson blocks "$content_array" '{
  version: 1,
  type: "doc",
  content: $blocks
}' | jq -c '.')
```

Send the update — success is **200 OK**:

```bash
response=$(curl -s -w "\n%{http_code}" \
  -u "${JIRA_USER_EMAIL}:${JIRA_API_TOKEN}" \
  -H "Content-Type: application/json" \
  -X PUT \
  -d "$payload" \
  "${base_url}/wiki/rest/api/content/${page_id}")

status=$(echo "$response" | tail -1)
```

Check the HTTP status code — `200` means success.

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
# Search by title in a space — use -G + --data-urlencode for safe encoding
cql="space = \"${space_key}\" AND title = \"${search_title}\""

response=$(curl -s -G -w "\n%{http_code}" \
  -u "${JIRA_USER_EMAIL}:${JIRA_API_TOKEN}" \
  -H "Content-Type: application/json" \
  "${base_url}/wiki/rest/api/content/search" \
  --data-urlencode "cql=${cql}" \
  --data-urlencode "expand=version" \
  --data-urlencode "limit=25")

body=$(echo "$response" | sed '$d')
status=$(echo "$response" | tail -1)
results=$(echo "$body" | jq -r '.results[] | "\(.id) \(.title)"')
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
- **Failure**: `"Failed to update Confluence page ${page_id} (HTTP ${status})"`

### Error handling

- Missing env vars: skip silently, do not report an error
- No page ID from request, URL, or search: report to caller that a page ID, URL, or space+title is needed
- `jq` not available: skip silently (do not break the calling workflow)
- Version conflict (HTTP 409): re-read the page to get the latest version, then retry the update once
- Confluence API error (non-200 on GET/PUT): report failure to edit but do not fail the overall workflow
