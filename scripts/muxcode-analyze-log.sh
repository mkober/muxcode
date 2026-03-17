#!/bin/bash
# muxcode-analyze-log.sh - Poll analyst findings every N seconds
# Used in the analyze window's left pane during muxcode sessions
#
# Reads response messages from the analyze agent in log.jsonl and displays
# them as a scrolling findings log with timestamps, actions, and payloads.
#
# Usage: muxcode-analyze-log.sh [interval_seconds]

set -uo pipefail

INTERVAL="${1:-5}"
SESSION="${BUS_SESSION:-$(tmux display-message -p '#S' 2>/dev/null || echo default)}"
LOG_FILE="/tmp/muxcode-bus-${SESSION}/log.jsonl"
MAX_RECENT=15

# Padding
PAD="   "          # 3-space left padding
CONT_PAD="     "   # 5-space continuation indent (for wrapped lines)
ENTRY_PAD="         "  # 9-space entry payload indent
RIGHT_MARGIN=2

# Dracula colors
PURPLE='\033[38;5;141m'
CYAN='\033[38;5;117m'
GREEN='\033[38;5;80m'
DIM='\033[2m'
RESET='\033[0m'

# Word-wrap text to a given width, returning lines
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

# Format epoch timestamp to "Mon DD HH:MM:SS"
format_ts() {
  local ts="$1"
  if date -r "$ts" '+%b %d %H:%M:%S' 2>/dev/null; then
    return
  fi
  if date -d "@$ts" '+%b %d %H:%M:%S' 2>/dev/null; then
    return
  fi
  echo "??? ?? ??:??:??"
}

while true; do
  # Detect pane width dynamically
  PANE_WIDTH=$(tput cols 2>/dev/null || echo 80)
  CONTENT_WIDTH=$(( PANE_WIDTH - ${#PAD} - RIGHT_MARGIN ))
  [ "$CONTENT_WIDTH" -lt 20 ] && CONTENT_WIDTH=20
  ENTRY_CONTENT_WIDTH=$(( PANE_WIDTH - ${#ENTRY_PAD} - RIGHT_MARGIN ))
  [ "$ENTRY_CONTENT_WIDTH" -lt 20 ] && ENTRY_CONTENT_WIDTH=20
  SEP_WIDTH=$(( PANE_WIDTH - ${#PAD} - RIGHT_MARGIN ))
  [ "$SEP_WIDTH" -lt 10 ] && SEP_WIDTH=10

  BUF=""
  BUF+="${PAD}${PURPLE}Analyze${RESET}  ${DIM}$(date '+%H:%M:%S')${RESET}  ${DIM}(every ${INTERVAL}s)${RESET}\n"
  BUF+="${PAD}${DIM}$(printf '%.0s─' $(seq 1 "$SEP_WIDTH"))${RESET}\n"
  BUF+="\n"

  if [ ! -f "$LOG_FILE" ] || [ ! -s "$LOG_FILE" ]; then
    BUF+="${PAD}${DIM}no findings yet${RESET}\n"
    BUF+="${PAD}${DIM}waiting for analyst agent...${RESET}\n"
    printf '\033[2J\033[H'
    echo -ne "$BUF"
    sleep "$INTERVAL"
    continue
  fi

  if command -v jq &>/dev/null; then
    # jq path: filter analyze responses from log.jsonl
    FINDINGS=$(jq -s '[.[] | select(.from == "analyze" and .type == "response")]' "$LOG_FILE" 2>/dev/null || echo "[]")
    TOTAL=$(printf '%s' "$FINDINGS" | jq 'length' 2>/dev/null || echo 0)

    # Summary line
    BUF+="${PAD}${DIM}findings${RESET} ${CYAN}${TOTAL}${RESET}\n"
    BUF+="\n"

    if [ "$TOTAL" -eq 0 ]; then
      BUF+="${PAD}${DIM}no analyst findings yet${RESET}\n"
    else
      # Last finding: full payload (shown first)
      LAST_PAYLOAD=$(printf '%s' "$FINDINGS" | jq -r '.[-1].payload // ""' 2>/dev/null)
      LAST_ACTION=$(printf '%s' "$FINDINGS" | jq -r '.[-1].action // ""' 2>/dev/null)
      LAST_TO=$(printf '%s' "$FINDINGS" | jq -r '.[-1].to // ""' 2>/dev/null)

      if [ -n "$LAST_PAYLOAD" ]; then
        BUF+="${PAD}${GREEN}⏺ Latest finding${RESET}  ${DIM}(${LAST_ACTION} → ${LAST_TO})${RESET}\n\n"
        FIRST_LINE=1
        while IFS= read -r oline; do
          oline=$(printf '%s' "$oline" | sed 's/\x1b\[[0-9;]*[A-Za-z]//g; s/^[[:space:]]*//')
          [ -z "$oline" ] && continue
          if [ "$FIRST_LINE" -eq 1 ]; then
            while IFS= read -r wline; do
              BUF+="${PAD}${CYAN}${wline}${RESET}\n"
            done <<< "$(word_wrap "$oline" "$CONTENT_WIDTH")"
            FIRST_LINE=0
          else
            while IFS= read -r wline; do
              BUF+="${CONT_PAD}${DIM}- ${wline}${RESET}\n"
            done <<< "$(word_wrap "$oline" "$(( CONTENT_WIDTH - ${#CONT_PAD} + ${#PAD} ))")"
          fi
        done <<< "$LAST_PAYLOAD"
        BUF+="\n"
      fi

      # Recent findings (last N)
      BUF+="${PAD}${CYAN}recent findings${RESET}\n"
      ENTRY_OFFSET=$(( TOTAL > MAX_RECENT ? TOTAL - MAX_RECENT : 0 ))
      ENTRIES=$(printf '%s' "$FINDINGS" | jq -c ".[-${MAX_RECENT}:][]" 2>/dev/null)
      FINDING_NUM=$ENTRY_OFFSET
      if [ -n "$ENTRIES" ]; then
        while IFS= read -r entry; do
          FINDING_NUM=$(( FINDING_NUM + 1 ))
          ts=$(printf '%s' "$entry" | jq -r '.ts // empty' 2>/dev/null)
          action=$(printf '%s' "$entry" | jq -r '.action // empty' 2>/dev/null)
          payload=$(printf '%s' "$entry" | jq -r '.payload // empty' 2>/dev/null)
          to_agent=$(printf '%s' "$entry" | jq -r '.to // empty' 2>/dev/null)

          [ -z "$ts" ] && continue
          TIME=$(format_ts "$ts")

          NUM_LABEL=$(printf '#%-3s' "$FINDING_NUM")

          BUF+="${CONT_PAD}${DIM}${NUM_LABEL}${RESET} ${DIM}${TIME}${RESET}  ${GREEN}${action}${RESET}  ${DIM}→${to_agent}${RESET}\n"
          if [ -n "$payload" ]; then
            while IFS= read -r wline; do
              BUF+="${ENTRY_PAD}${DIM}${wline}${RESET}\n"
            done <<< "$(word_wrap "$payload" "$ENTRY_CONTENT_WIDTH")"
          fi
        done <<< "$ENTRIES"
      fi
      BUF+="\n"
    fi

  else
    # python3 fallback
    PARSED=$(python3 -c '
import json, sys
entries = []
with open(sys.argv[1]) as f:
    for line in f:
        line = line.strip()
        if line:
            try:
                e = json.loads(line)
                if e.get("from") == "analyze" and e.get("type") == "response":
                    entries.append(e)
            except:
                pass
total = len(entries)
print(f"TOTAL={total}")
max_recent = int(sys.argv[2])
offset = max(0, total - max_recent)
recent = entries[-max_recent:]
for i, e in enumerate(recent):
    ts = e.get("ts", 0)
    action = e.get("action", "")
    payload = e.get("payload", "")
    to_agent = e.get("to", "")
    num = offset + i + 1
    print(f"ENTRY={ts}\t{action}\t{to_agent}\t{num}\t{payload}")
if entries:
    last = entries[-1]
    print(f"LAST_ACTION={last.get('action', '')}")
    print(f"LAST_TO={last.get('to', '')}")
    for ol in last.get("payload", "").strip().split("\n"):
        ol = ol.strip()
        if ol:
            print(f"LAST_PAYLOAD_LINE={ol}")
' "$LOG_FILE" "$MAX_RECENT" 2>/dev/null)

    TOTAL=0
    while IFS= read -r line; do
      case "$line" in
        TOTAL=*) TOTAL="${line#TOTAL=}" ;;
      esac
    done <<< "$PARSED"

    BUF+="${PAD}${DIM}findings${RESET} ${CYAN}${TOTAL}${RESET}\n"
    BUF+="\n"

    if [ "$TOTAL" -eq 0 ]; then
      BUF+="${PAD}${DIM}no analyst findings yet${RESET}\n"
    else
      # Last finding: full payload (shown first)
      PY_LAST_ACTION=""
      PY_LAST_TO=""
      HAS_PAYLOAD=0
      PY_FIRST_LINE=1
      while IFS= read -r line; do
        case "$line" in
          LAST_ACTION=*) PY_LAST_ACTION="${line#LAST_ACTION=}" ;;
          LAST_TO=*) PY_LAST_TO="${line#LAST_TO=}" ;;
          LAST_PAYLOAD_LINE=*)
            if [ "$HAS_PAYLOAD" -eq 0 ]; then
              HAS_PAYLOAD=1
              BUF+="${PAD}${GREEN}⏺ Latest finding${RESET}  ${DIM}(${PY_LAST_ACTION} → ${PY_LAST_TO})${RESET}\n\n"
            fi
            OL="${line#LAST_PAYLOAD_LINE=}"
            if [ "$PY_FIRST_LINE" -eq 1 ]; then
              while IFS= read -r wline; do
                BUF+="${PAD}${CYAN}${wline}${RESET}\n"
              done <<< "$(word_wrap "$OL" "$CONTENT_WIDTH")"
              PY_FIRST_LINE=0
            else
              while IFS= read -r wline; do
                BUF+="${CONT_PAD}${DIM}- ${wline}${RESET}\n"
              done <<< "$(word_wrap "$OL" "$(( CONTENT_WIDTH - ${#CONT_PAD} + ${#PAD} ))")"
            fi
            ;;
        esac
      done <<< "$PARSED"
      if [ "$HAS_PAYLOAD" -eq 1 ]; then
        BUF+="\n"
      fi

      # Recent findings (last N)
      BUF+="${PAD}${CYAN}recent findings${RESET}\n"
      while IFS= read -r line; do
        case "$line" in
          ENTRY=*)
            line="${line#ENTRY=}"
            IFS=$'\t' read -r ts action to_agent num payload <<< "$line"
            TIME=$(format_ts "$ts")
            NUM_LABEL=$(printf '#%-3s' "$num")
            BUF+="${CONT_PAD}${DIM}${NUM_LABEL}${RESET} ${DIM}${TIME}${RESET}  ${GREEN}${action}${RESET}  ${DIM}→${to_agent}${RESET}\n"
            if [ -n "$payload" ]; then
              while IFS= read -r wline; do
                BUF+="${ENTRY_PAD}${DIM}${wline}${RESET}\n"
              done <<< "$(word_wrap "$payload" "$ENTRY_CONTENT_WIDTH")"
            fi
            ;;
        esac
      done <<< "$PARSED"
      BUF+="\n"
    fi
  fi

  printf '\033[2J\033[H'
  echo -ne "$BUF"

  sleep "$INTERVAL"
done
