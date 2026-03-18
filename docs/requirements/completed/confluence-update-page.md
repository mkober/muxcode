# Confluence Page Read+Update

General-purpose Confluence GET+PUT skill for reading and updating pages via REST API.

## Requirements

- Read page content with version tracking for safe updates
- Update pages using Atlassian Document Format (ADF)
- Append mode for adding content without replacing existing body
- CQL search for finding pages by query
- Three-path page identification: by ID, by URL, or by space key + title
- 409 conflict retry with version re-fetch on concurrent edit detection
- Uses `CONFLUENCE_BASE_URL` env var, falls back to `JIRA_BASE_URL`

## Key files

| File | Purpose |
|------|---------|
| `skills/confluence-update-page.md` | Skill definition with API patterns, ADF examples, and conflict handling |
