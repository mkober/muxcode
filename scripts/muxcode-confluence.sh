#!/usr/bin/env bash
# muxcode-confluence.sh — Confluence API helper for MuxCode agents
# Usage:
#   muxcode-confluence.sh read <PAGE-ID>
#   muxcode-confluence.sh update <PAGE-ID> <ADF-JSON-FILE>
#   muxcode-confluence.sh search <SPACE-KEY> <CQL-QUERY>
#
# Reads credentials from config files. Avoids inline curl+auth that
# triggers Claude Code "quoted characters in flag names" prompts.

# Source config for CONFLUENCE_BASE_URL/JIRA_BASE_URL, JIRA_USER_EMAIL, JIRA_API_TOKEN
if [ -f .muxcode/config ]; then
  set -a; source .muxcode/config 2>/dev/null; set +a
fi
if [ -f ~/.config/muxcode/config ]; then
  set -a; source ~/.config/muxcode/config 2>/dev/null; set +a
fi

base_url="${CONFLUENCE_BASE_URL:-${JIRA_BASE_URL}}"
if [ -z "$base_url" ] || [ -z "$JIRA_USER_EMAIL" ] || [ -z "$JIRA_API_TOKEN" ]; then
  echo "ERROR: Missing Confluence config (CONFLUENCE_BASE_URL or JIRA_BASE_URL, JIRA_USER_EMAIL, JIRA_API_TOKEN)" >&2
  exit 1
fi

cmd="${1:-}"

if [ -z "$cmd" ]; then
  echo "Usage: muxcode-confluence.sh <read|update|search> ..." >&2
  exit 1
fi

case "$cmd" in
  read)
    page_id="${2:-}"
    if [ -z "$page_id" ]; then
      echo "Usage: muxcode-confluence.sh read <PAGE-ID>" >&2
      exit 1
    fi

    response=$(curl -s -w "\n%{http_code}" \
      -u "${JIRA_USER_EMAIL}:${JIRA_API_TOKEN}" \
      -H "Content-Type: application/json" \
      "${base_url}/wiki/rest/api/content/${page_id}?expand=body.atlas_doc_format,version,space,ancestors")

    body=$(echo "$response" | sed '$d')
    status=$(echo "$response" | tail -1)

    if [ "$status" != "200" ]; then
      echo "ERROR: Confluence API returned HTTP ${status}" >&2
      echo "$body" >&2
      exit 1
    fi

    title=$(echo "$body" | jq -r '.title // "Untitled"')
    space_key=$(echo "$body" | jq -r '.space.key // "Unknown"')
    space_name=$(echo "$body" | jq -r '.space.name // "Unknown"')
    version_number=$(echo "$body" | jq -r '.version.number // 1')
    version_by=$(echo "$body" | jq -r '.version.by.displayName // "Unknown"')
    version_when=$(echo "$body" | jq -r '.version.when // "Unknown"')
    page_url="${base_url}/wiki/spaces/${space_key}/pages/${page_id}"

    echo "=== Confluence Page ${page_id} ==="
    echo "Title: ${title}"
    echo "Space: ${space_name} [${space_key}]"
    echo "Version: ${version_number} by ${version_by} (${version_when})"
    echo "URL: ${page_url}"
    echo ""
    echo "--- Content ---"
    adf_content=$(echo "$body" | jq -r '.body.atlas_doc_format.value // empty')
    echo "$adf_content" | jq -r 'fromjson | [.. | .text? // empty] | join(" ")' 2>/dev/null || echo "(unable to parse ADF content)"
    echo ""
    echo "--- Raw ADF ---"
    echo "$adf_content"
    ;;

  update)
    page_id="${2:-}"
    adf_file="${3:-}"
    if [ -z "$page_id" ] || [ -z "$adf_file" ] || [ ! -f "$adf_file" ]; then
      echo "Usage: muxcode-confluence.sh update <PAGE-ID> <ADF-JSON-FILE>" >&2
      echo "ADF-JSON-FILE should contain the full request body JSON." >&2
      exit 1
    fi

    response=$(curl -s -w "\n%{http_code}" \
      -u "${JIRA_USER_EMAIL}:${JIRA_API_TOKEN}" \
      -H "Content-Type: application/json" \
      -X PUT \
      -d @"${adf_file}" \
      "${base_url}/wiki/rest/api/content/${page_id}")

    status=$(echo "$response" | tail -1)

    if [ "$status" = "200" ]; then
      echo "Updated Confluence page ${page_id}"
    else
      body=$(echo "$response" | sed '$d')
      echo "ERROR: Confluence API returned HTTP ${status}" >&2
      echo "$body" >&2
      exit 1
    fi
    ;;

  search)
    space_key="${2:-}"
    cql="${3:-}"
    if [ -z "$cql" ]; then
      echo "Usage: muxcode-confluence.sh search <SPACE-KEY> <CQL-QUERY>" >&2
      exit 1
    fi

    response=$(curl -s -G -w "\n%{http_code}" \
      -u "${JIRA_USER_EMAIL}:${JIRA_API_TOKEN}" \
      -H "Content-Type: application/json" \
      "${base_url}/wiki/rest/api/content/search" \
      --data-urlencode "cql=${cql}" \
      --data-urlencode "expand=version" \
      --data-urlencode "limit=25")

    body=$(echo "$response" | sed '$d')
    status=$(echo "$response" | tail -1)

    if [ "$status" != "200" ]; then
      echo "ERROR: Confluence API returned HTTP ${status}" >&2
      echo "$body" >&2
      exit 1
    fi

    echo "=== Search Results (${space_key}) ==="
    echo "$body" | jq -r '.results[] | "\(.id)\t\(.title)\tv\(.version.number)"'
    ;;

  *)
    echo "Unknown command: $cmd" >&2
    echo "Usage: muxcode-confluence.sh <read|update|search> ..." >&2
    exit 1
    ;;
esac
