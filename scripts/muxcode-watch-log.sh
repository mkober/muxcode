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
# Continuation indent: "             " = 13 chars (matches "  HH:MM:SS  " prefix)
CONT_INDENT=13

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

# Format epoch timestamp to "HH:MM:SS"
format_ts() {
  local ts="$1"
  [ -z "$ts" ] && return
  # macOS: date -r <epoch>
  if date -r "$ts" '+%H:%M:%S' 2>/dev/null; then
    return
  fi
  # Linux: date -d @<epoch>
  if date -d "@$ts" '+%H:%M:%S' 2>/dev/null; then
    return
  fi
}

# Word-wrap text to MAX_WIDTH, returning lines
word_wrap() {
  local text="$1"
  local width="$2"
  local line="" word=""

  for word in $text; do
    if [ -z "$line" ]; then
      line="$word"
    elif [ $(( ${#line} + 1 + ${#word} )) -le "$width" ]; then
      line="$line $word"
    else
      echo "$line"
      line="$word"
    fi
  done
  [ -n "$line" ] && echo "$line"
}

while true; do
  # Detect pane width dynamically
  PANE_WIDTH=$(tput cols 2>/dev/null || echo 80)
  MAX_WIDTH=$(( PANE_WIDTH - CONT_INDENT - 1 ))
  [ "$MAX_WIDTH" -lt 20 ] && MAX_WIDTH=20

  BUF=""
  BUF+="${PURPLE}  Watch${RESET}  ${DIM}$(date '+%H:%M:%S')${RESET}  ${DIM}(every ${INTERVAL}s)${RESET}\n"
  SEP_WIDTH=$(( PANE_WIDTH - 4 ))
  [ "$SEP_WIDTH" -lt 20 ] && SEP_WIDTH=20
  BUF+="  ${DIM}$(printf '%.0s─' $(seq 1 "$SEP_WIDTH"))${RESET}\n"
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
        # Parse JSONL fields — ts is epoch integer
        TS=$(echo "$line" | jq -r '.ts // empty' 2>/dev/null)
        SUMMARY=$(echo "$line" | jq -r '.summary // .message // empty' 2>/dev/null)

        if [ -n "$SUMMARY" ]; then
          # Format timestamp from epoch
          TIME=""
          if [ -n "$TS" ]; then
            TIME=$(format_ts "$TS")
          fi

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

          # Word-wrap long summaries
          FIRST=1
          while IFS= read -r wline; do
            if [ "$FIRST" -eq 1 ]; then
              BUF+="  ${DIM}${TIME:-??:??:??}${RESET}  ${COLOR}${wline}${RESET}\n"
              FIRST=0
            else
              BUF+="             ${COLOR}${wline}${RESET}\n"
            fi
          done <<< "$(word_wrap "$SUMMARY" "$MAX_WIDTH")"
          BUF+="\n"
        fi
      done < <(tail -n "$MAX_ENTRIES" "$HISTORY_FILE" 2>/dev/null)
    fi
  fi

  printf '\033[2J\033[H'
  echo -ne "$BUF"

  sleep "$INTERVAL"
done
