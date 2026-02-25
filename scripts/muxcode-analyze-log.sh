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

# Dracula colors
PURPLE='\033[38;5;141m'
CYAN='\033[38;5;117m'
GREEN='\033[38;5;80m'
DIM='\033[2m'
RESET='\033[0m'

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
  BUF=""
  BUF+="${PURPLE}  analyze log${RESET}  ${DIM}$(date '+%H:%M:%S')${RESET}  ${DIM}(every ${INTERVAL}s)${RESET}\n"
  BUF+="${DIM}$(printf '%.0s─' {1..50})${RESET}\n"
  BUF+="\n"

  if [ ! -f "$LOG_FILE" ] || [ ! -s "$LOG_FILE" ]; then
    BUF+="  ${DIM}no findings yet${RESET}\n"
    BUF+="  ${DIM}waiting for analyst agent...${RESET}\n"
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
    BUF+="  ${DIM}findings${RESET} ${CYAN}${TOTAL}${RESET}\n"
    BUF+="\n"

    if [ "$TOTAL" -eq 0 ]; then
      BUF+="  ${DIM}no analyst findings yet${RESET}\n"
    else
      # Recent findings (last N)
      BUF+="  ${CYAN}recent findings${RESET}\n"
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

          # Truncate payload for list view
          short_payload="$payload"
          if [ ${#short_payload} -gt 40 ]; then
            short_payload="${short_payload:0:37}..."
          fi

          NUM_LABEL=$(printf '#%-3s' "$FINDING_NUM")

          BUF+="    ${DIM}${NUM_LABEL}${RESET} ${DIM}${TIME}${RESET}  ${GREEN}${action}${RESET}  ${DIM}→${to_agent}${RESET}\n"
          if [ -n "$short_payload" ]; then
            BUF+="         ${DIM}${short_payload}${RESET}\n"
          fi
        done <<< "$ENTRIES"
      fi
      BUF+="\n"

      # Last finding: full payload
      LAST_PAYLOAD=$(printf '%s' "$FINDINGS" | jq -r '.[-1].payload // ""' 2>/dev/null)
      LAST_ACTION=$(printf '%s' "$FINDINGS" | jq -r '.[-1].action // ""' 2>/dev/null)
      LAST_TO=$(printf '%s' "$FINDINGS" | jq -r '.[-1].to // ""' 2>/dev/null)

      if [ -n "$LAST_PAYLOAD" ]; then
        BUF+="  ${GREEN}⏺ Latest finding${RESET}  ${DIM}(${LAST_ACTION} → ${LAST_TO})${RESET}\n\n"
        FIRST_LINE=1
        while IFS= read -r oline; do
          oline=$(printf '%s' "$oline" | sed 's/\x1b\[[0-9;]*[A-Za-z]//g; s/^[[:space:]]*//')
          [ -z "$oline" ] && continue
          if [ ${#oline} -gt 60 ]; then
            oline="${oline:0:57}..."
          fi
          if [ "$FIRST_LINE" -eq 1 ]; then
            BUF+="  ${CYAN}${oline}${RESET}\n"
            FIRST_LINE=0
          else
            BUF+="    ${DIM}- ${oline}${RESET}\n"
          fi
        done <<< "$LAST_PAYLOAD"
        BUF+="\n"
      fi
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
    if len(payload) > 40:
        payload = payload[:37] + "..."
    print(f"ENTRY={ts}\t{action}\t{to_agent}\t{num}\t{payload}")
if entries:
    last = entries[-1]
    print(f"LAST_ACTION={last.get('action', '')}")
    print(f"LAST_TO={last.get('to', '')}")
    for ol in last.get("payload", "").strip().split("\n"):
        ol = ol.strip()
        if ol:
            if len(ol) > 60:
                ol = ol[:57] + "..."
            print(f"LAST_PAYLOAD_LINE={ol}")
' "$LOG_FILE" "$MAX_RECENT" 2>/dev/null)

    TOTAL=0
    while IFS= read -r line; do
      case "$line" in
        TOTAL=*) TOTAL="${line#TOTAL=}" ;;
      esac
    done <<< "$PARSED"

    BUF+="  ${DIM}findings${RESET} ${CYAN}${TOTAL}${RESET}\n"
    BUF+="\n"

    if [ "$TOTAL" -eq 0 ]; then
      BUF+="  ${DIM}no analyst findings yet${RESET}\n"
    else
      BUF+="  ${CYAN}recent findings${RESET}\n"
      while IFS= read -r line; do
        case "$line" in
          ENTRY=*)
            line="${line#ENTRY=}"
            IFS=$'\t' read -r ts action to_agent num payload <<< "$line"
            TIME=$(format_ts "$ts")
            NUM_LABEL=$(printf '#%-3s' "$num")
            BUF+="    ${DIM}${NUM_LABEL}${RESET} ${DIM}${TIME}${RESET}  ${GREEN}${action}${RESET}  ${DIM}→${to_agent}${RESET}\n"
            if [ -n "$payload" ]; then
              BUF+="         ${DIM}${payload}${RESET}\n"
            fi
            ;;
        esac
      done <<< "$PARSED"
      BUF+="\n"

      # Last finding: full payload
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
              BUF+="  ${GREEN}⏺ Latest finding${RESET}  ${DIM}(${PY_LAST_ACTION} → ${PY_LAST_TO})${RESET}\n\n"
            fi
            OL="${line#LAST_PAYLOAD_LINE=}"
            if [ "$PY_FIRST_LINE" -eq 1 ]; then
              BUF+="  ${CYAN}${OL}${RESET}\n"
              PY_FIRST_LINE=0
            else
              BUF+="    ${DIM}- ${OL}${RESET}\n"
            fi
            ;;
        esac
      done <<< "$PARSED"
      if [ "$HAS_PAYLOAD" -eq 1 ]; then
        BUF+="\n"
      fi
    fi
  fi

  printf '\033[2J\033[H'
  echo -ne "$BUF"

  sleep "$INTERVAL"
done
