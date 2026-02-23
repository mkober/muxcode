#!/bin/bash
# muxcode-watch-log.sh - Poll watch history every N seconds
# Used in the watch window's left pane during muxcode sessions
#
# Displays recent log watch entries with timestamps, sources, and summaries.
# Reads from the watch-history.jsonl file maintained by the watch agent.
#
# Usage: muxcode-watch-log.sh [interval_seconds]

set -uo pipefail

INTERVAL="${1:-5}"
SESSION="${BUS_SESSION:-${SESSION:-default}}"
HISTORY_FILE="/tmp/muxcode-bus-${SESSION}/watch-history.jsonl"
MAX_ENTRIES=25

# Dracula colors
PURPLE='\033[38;5;141m'
CYAN='\033[38;5;117m'
GREEN='\033[38;5;80m'
PINK='\033[38;5;212m'
ORANGE='\033[38;5;215m'
RED='\033[38;5;203m'
DIM='\033[2m'
BOLD='\033[1m'
RESET='\033[0m'

while true; do
  BUF=""
  BUF+="${PURPLE}  watch log${RESET}  ${DIM}$(date '+%H:%M:%S')${RESET}  ${DIM}(every ${INTERVAL}s)${RESET}\n"
  BUF+="${DIM}$(printf '%.0s─' {1..50})${RESET}\n"
  BUF+="\n"

  if [ ! -f "$HISTORY_FILE" ]; then
    BUF+="  ${DIM}no watch history yet${RESET}\n"
    BUF+="  ${DIM}waiting for watch agent...${RESET}\n"
  else
    ENTRY_COUNT=$(wc -l < "$HISTORY_FILE" 2>/dev/null || echo 0)
    ENTRY_COUNT=$(echo "$ENTRY_COUNT" | tr -d ' ')

    if [ "$ENTRY_COUNT" -eq 0 ]; then
      BUF+="  ${DIM}no watch entries yet${RESET}\n"
    else
      BUF+="  ${DIM}${ENTRY_COUNT} entries${RESET}\n\n"

      # Show last N entries (process substitution keeps loop in main shell)
      while IFS= read -r line; do
        # Parse JSONL fields
        TIMESTAMP=$(echo "$line" | jq -r '.timestamp // empty' 2>/dev/null | cut -c12-19)
        SUMMARY=$(echo "$line" | jq -r '.summary // .message // empty' 2>/dev/null)

        if [ -n "$SUMMARY" ]; then
          # Color based on content
          if echo "$SUMMARY" | grep -qi 'error\|fail\|crash\|panic\|fatal'; then
            COLOR="$RED"
          elif echo "$SUMMARY" | grep -qi 'warn'; then
            COLOR="$ORANGE"
          elif echo "$SUMMARY" | grep -qi 'success\|ok\|healthy\|running'; then
            COLOR="$GREEN"
          else
            COLOR="$CYAN"
          fi

          BUF+="  ${DIM}${TIMESTAMP:-??:??:??}${RESET}  ${COLOR}${SUMMARY}${RESET}\n"
        fi
      done < <(tail -n "$MAX_ENTRIES" "$HISTORY_FILE" 2>/dev/null)
    fi
  fi

  printf '\033[2J\033[H'
  echo -ne "$BUF"

  sleep "$INTERVAL"
done
