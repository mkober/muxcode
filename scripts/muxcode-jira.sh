#!/usr/bin/env bash
# muxcode-jira.sh — Jira API helper for MuxCode agents
# Usage:
#   muxcode-jira.sh read <ISSUE-KEY>
#   muxcode-jira.sh update <ISSUE-KEY> <ADF-JSON-FILE>
#   muxcode-jira.sh comment <ISSUE-KEY> <ADF-JSON-FILE>
#
# Reads credentials from config files. Avoids inline curl+auth that
# triggers Claude Code "quoted characters in flag names" prompts.

# Source config for JIRA_BASE_URL, JIRA_USER_EMAIL, JIRA_API_TOKEN
if [ -f .muxcode/config ]; then
  set -a; source .muxcode/config 2>/dev/null; set +a
fi
if [ -f ~/.config/muxcode/config ]; then
  set -a; source ~/.config/muxcode/config 2>/dev/null; set +a
fi

if [ -z "$JIRA_BASE_URL" ] || [ -z "$JIRA_USER_EMAIL" ] || [ -z "$JIRA_API_TOKEN" ]; then
  echo "ERROR: Missing Jira config (JIRA_BASE_URL, JIRA_USER_EMAIL, JIRA_API_TOKEN)" >&2
  exit 1
fi

cmd="${1:-}"
jira_key="${2:-}"

if [ -z "$cmd" ] || [ -z "$jira_key" ]; then
  echo "Usage: muxcode-jira.sh <read|update|comment> <ISSUE-KEY> [ADF-JSON-FILE]" >&2
  exit 1
fi

case "$cmd" in
  read)
    response=$(curl -s -w "\n%{http_code}" \
      -u "${JIRA_USER_EMAIL}:${JIRA_API_TOKEN}" \
      -H "Content-Type: application/json" \
      "${JIRA_BASE_URL}/rest/api/3/issue/${jira_key}?fields=description,summary,status,assignee,issuetype,priority")

    body=$(echo "$response" | sed '$d')
    status=$(echo "$response" | tail -1)

    if [ "$status" != "200" ]; then
      echo "ERROR: Jira API returned HTTP ${status}" >&2
      echo "$body" >&2
      exit 1
    fi

    summary=$(echo "$body" | jq -r '.fields.summary // "No summary"')
    issue_status=$(echo "$body" | jq -r '.fields.status.name // "Unknown"')
    assignee=$(echo "$body" | jq -r '.fields.assignee.displayName // "Unassigned"')
    issue_type=$(echo "$body" | jq -r '.fields.issuetype.name // "Unknown"')
    priority=$(echo "$body" | jq -r '.fields.priority.name // "Unknown"')

    echo "=== ${jira_key} ==="
    echo "Summary: ${summary}"
    echo "Type: ${issue_type} | Priority: ${priority}"
    echo "Status: ${issue_status} | Assignee: ${assignee}"
    echo ""
    echo "--- Description ---"
    echo "$body" | jq -r '[.fields.description // empty | .. | .text? // empty] | join(" ")'
    ;;

  update)
    adf_file="${3:-}"
    if [ -z "$adf_file" ] || [ ! -f "$adf_file" ]; then
      echo "Usage: muxcode-jira.sh update <ISSUE-KEY> <ADF-JSON-FILE>" >&2
      echo "ADF-JSON-FILE should contain the full request body JSON." >&2
      exit 1
    fi

    response=$(curl -s -w "\n%{http_code}" \
      -u "${JIRA_USER_EMAIL}:${JIRA_API_TOKEN}" \
      -H "Content-Type: application/json" \
      -X PUT \
      -d @"${adf_file}" \
      "${JIRA_BASE_URL}/rest/api/3/issue/${jira_key}")

    status=$(echo "$response" | tail -1)

    if [ "$status" = "204" ]; then
      echo "Updated description for ${jira_key}"
    else
      body=$(echo "$response" | sed '$d')
      echo "ERROR: Jira API returned HTTP ${status}" >&2
      echo "$body" >&2
      exit 1
    fi
    ;;

  comment)
    adf_file="${3:-}"
    if [ -z "$adf_file" ] || [ ! -f "$adf_file" ]; then
      echo "Usage: muxcode-jira.sh comment <ISSUE-KEY> <ADF-JSON-FILE>" >&2
      echo "ADF-JSON-FILE should contain the comment body JSON." >&2
      exit 1
    fi

    response=$(curl -s -w "\n%{http_code}" \
      -u "${JIRA_USER_EMAIL}:${JIRA_API_TOKEN}" \
      -H "Content-Type: application/json" \
      -X POST \
      -d @"${adf_file}" \
      "${JIRA_BASE_URL}/rest/api/3/issue/${jira_key}/comment")

    status=$(echo "$response" | tail -1)

    if [ "$status" = "201" ]; then
      echo "Posted comment to ${jira_key}"
    else
      body=$(echo "$response" | sed '$d')
      echo "ERROR: Jira API returned HTTP ${status}" >&2
      echo "$body" >&2
      exit 1
    fi
    ;;

  *)
    echo "Unknown command: $cmd" >&2
    echo "Usage: muxcode-jira.sh <read|update|comment> <ISSUE-KEY> [ADF-JSON-FILE]" >&2
    exit 1
    ;;
esac
