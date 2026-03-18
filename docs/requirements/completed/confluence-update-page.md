# Confluence Page Read+Update

General-purpose Confluence GET+PUT skill for reading and updating pages via the `muxcode-confluence.sh` wrapper script, which handles REST API auth internally.

## Requirements

- Read page content with version tracking for safe updates
- Update pages using Atlassian Document Format (ADF)
- Append mode for adding content without replacing existing body
- CQL search for finding pages by query
- Three-path page identification: by ID, by URL, or by space key + title
- 409 conflict retry with version re-fetch on concurrent edit detection
- Uses `CONFLUENCE_BASE_URL` env var, falls back to `JIRA_BASE_URL`
- Wrapper script avoids inline curl+auth that triggers Claude Code "quoted characters in flag names" permission prompts

## Key files

| File | Purpose |
|------|---------|
| `skills/confluence-update-page.md` | Skill definition with ADF examples, conflict handling, and wrapper script usage |
| `scripts/muxcode-confluence.sh` | Wrapper script handling config sourcing, curl+auth, and API calls (read/update/search) |
