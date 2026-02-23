#!/bin/bash
# muxcode-build-log.sh - Poll build history every N seconds
# Used in the build window's left pane during muxcode sessions
#
# Usage: muxcode-build-log.sh [interval_seconds]

set -uo pipefail

INTERVAL="${1:-5}"
SESSION="${BUS_SESSION:-$(tmux display-message -p '#S' 2>/dev/null || echo default)}"
HISTORY_FILE="/tmp/muxcode-bus-${SESSION}/build-history.jsonl"

# Dracula colors
PURPLE='\033[38;5;141m'
CYAN='\033[38;5;117m'
GREEN='\033[38;5;80m'
PINK='\033[38;5;212m'
RED='\033[38;5;203m'
YELLOW='\033[38;5;228m'
DIM='\033[2m'
RESET='\033[0m'

# Format epoch timestamp to "Mon DD HH:MM:SS"
format_ts() {
  local ts="$1"
  # macOS: date -r <epoch>
  if date -r "$ts" '+%b %d %H:%M:%S' 2>/dev/null; then
    return
  fi
  # Linux: date -d @<epoch>
  if date -d "@$ts" '+%b %d %H:%M:%S' 2>/dev/null; then
    return
  fi
  echo "??? ?? ??:??:??"
}

while true; do
  BUF=""
  BUF+="${PURPLE}  build log${RESET}  ${DIM}$(date '+%H:%M:%S')${RESET}  ${DIM}(every ${INTERVAL}s)${RESET}\n"
  BUF+="${DIM}$(printf '%.0s─' {1..50})${RESET}\n"
  BUF+="\n"

  if [ ! -f "$HISTORY_FILE" ] || [ ! -s "$HISTORY_FILE" ]; then
    BUF+="  ${DIM}no builds yet${RESET}\n"
    printf '\033[2J\033[H'
    echo -ne "$BUF"
    sleep "$INTERVAL"
    continue
  fi

  # Parse build history with jq (python3 fallback)
  if command -v jq &>/dev/null; then
    TOTAL=$(jq -s 'length' "$HISTORY_FILE" 2>/dev/null || echo 0)
    PASS=$(jq -s '[.[] | select(.exit_code == "0" or .exit_code == 0)] | length' "$HISTORY_FILE" 2>/dev/null || echo 0)
    FAIL=$(( TOTAL - PASS ))

    # Summary line
    BUF+="  ${DIM}total${RESET} ${CYAN}${TOTAL}${RESET}  ${DIM}pass${RESET} ${GREEN}${PASS}${RESET}  ${DIM}fail${RESET} ${RED}${FAIL}${RESET}\n"
    BUF+="\n"

    # Recent builds (last 15)
    BUF+="  ${CYAN}recent builds${RESET}\n"
    ENTRY_OFFSET=$(( TOTAL > 15 ? TOTAL - 15 : 0 ))
    ENTRIES=$(jq -s '.[-15:][] | @json' "$HISTORY_FILE" 2>/dev/null)
    BUILD_NUM=$ENTRY_OFFSET
    if [ -n "$ENTRIES" ]; then
      while IFS= read -r entry; do
        entry="${entry%\"}"
        entry="${entry#\"}"
        BUILD_NUM=$(( BUILD_NUM + 1 ))
        # Unescape the JSON string
        raw=$(printf '%s' "$entry" | jq -r '.' 2>/dev/null) || continue
        ts=$(printf '%s' "$raw" | jq -r '.ts // empty' 2>/dev/null)
        cmd=$(printf '%s' "$raw" | jq -r '.command // empty' 2>/dev/null)
        desc=$(printf '%s' "$raw" | jq -r '.description // empty' 2>/dev/null)
        ec=$(printf '%s' "$raw" | jq -r '.exit_code // empty' 2>/dev/null)
        outcome=$(printf '%s' "$raw" | jq -r '.outcome // empty' 2>/dev/null)
        changes=$(printf '%s' "$raw" | jq -r '.changes // empty' 2>/dev/null)

        [ -z "$ts" ] && continue
        TIME=$(format_ts "$ts")

        # Truncate long commands
        if [ ${#cmd} -gt 35 ]; then
          cmd="${cmd:0:32}..."
        fi

        # Build number prefix
        NUM_LABEL=$(printf '#%-3s' "$BUILD_NUM")

        if [ "$ec" = "0" ]; then
          BUF+="    ${DIM}${NUM_LABEL}${RESET} ${DIM}${TIME}${RESET}  ${GREEN}OK${RESET}    ${cmd}\n"
        else
          BUF+="    ${DIM}${NUM_LABEL}${RESET} ${DIM}${TIME}${RESET}  ${RED}FAIL${RESET}  ${cmd}  ${DIM}exit ${ec}${RESET}\n"
        fi

        # Show changes summary (preferred) or description on second line
        DETAIL="${changes:-$desc}"
        if [ -n "$DETAIL" ]; then
          if [ ${#DETAIL} -gt 44 ]; then
            DETAIL="${DETAIL:0:41}..."
          fi
          BUF+="         ${DIM}↳ ${DETAIL}${RESET}\n"
        fi
      done <<< "$ENTRIES"
    fi
    BUF+="\n"

    # Last failure detail (if most recent build failed)
    LAST_EC=$(jq -s '.[-1].exit_code // "0"' "$HISTORY_FILE" 2>/dev/null)
    # Normalize: could be string or number
    LAST_EC=$(printf '%s' "$LAST_EC" | tr -d '"')
    if [ "$LAST_EC" != "0" ]; then
      LAST_CMD=$(jq -s -r '.[-1].command // ""' "$HISTORY_FILE" 2>/dev/null)
      BUF+="  ${RED}last failure${RESET}\n"
      BUF+="    ${DIM}cmd${RESET}   ${LAST_CMD}\n"
      BUF+="    ${DIM}exit${RESET}  ${LAST_EC}\n"
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
                entries.append(json.loads(line))
            except:
                pass
total = len(entries)
passed = sum(1 for e in entries if str(e.get("exit_code", "1")) == "0")
failed = total - passed
print(f"TOTAL={total}")
print(f"PASS={passed}")
print(f"FAIL={failed}")
offset = max(0, total - 15)
recent = entries[-15:]
for i, e in enumerate(recent):
    ts = e.get("ts", 0)
    cmd = e.get("command", "")
    changes = e.get("changes", "")
    desc = e.get("description", "")
    detail = changes if changes else desc
    ec = str(e.get("exit_code", "?"))
    num = offset + i + 1
    if len(cmd) > 35:
        cmd = cmd[:32] + "..."
    if len(detail) > 44:
        detail = detail[:41] + "..."
    status = "OK" if ec == "0" else "FAIL"
    print(f"ENTRY={ts}\t{status}\t{cmd}\t{ec}\t{num}\t{detail}")
if entries and str(entries[-1].get("exit_code", "0")) != "0":
    print(f"LASTFAIL_CMD={entries[-1].get('command', '')}")
    print(f"LASTFAIL_EC={entries[-1].get('exit_code', '?')}")
' "$HISTORY_FILE" 2>/dev/null)

    TOTAL=0; PASS=0; FAIL=0
    while IFS= read -r line; do
      case "$line" in
        TOTAL=*) TOTAL="${line#TOTAL=}" ;;
        PASS=*)  PASS="${line#PASS=}" ;;
        FAIL=*)  FAIL="${line#FAIL=}" ;;
      esac
    done <<< "$PARSED"

    BUF+="  ${DIM}total${RESET} ${CYAN}${TOTAL}${RESET}  ${DIM}pass${RESET} ${GREEN}${PASS}${RESET}  ${DIM}fail${RESET} ${RED}${FAIL}${RESET}\n"
    BUF+="\n"

    BUF+="  ${CYAN}recent builds${RESET}\n"
    while IFS= read -r line; do
      case "$line" in
        ENTRY=*)
          line="${line#ENTRY=}"
          IFS=$'\t' read -r ts status cmd ec num desc <<< "$line"
          TIME=$(format_ts "$ts")
          NUM_LABEL=$(printf '#%-3s' "$num")
          if [ "$status" = "OK" ]; then
            BUF+="    ${DIM}${NUM_LABEL}${RESET} ${DIM}${TIME}${RESET}  ${GREEN}OK${RESET}    ${cmd}\n"
          else
            BUF+="    ${DIM}${NUM_LABEL}${RESET} ${DIM}${TIME}${RESET}  ${RED}FAIL${RESET}  ${cmd}  ${DIM}exit ${ec}${RESET}\n"
          fi
          if [ -n "$desc" ]; then
            BUF+="         ${DIM}↳ ${desc}${RESET}\n"
          fi
          ;;
      esac
    done <<< "$PARSED"
    BUF+="\n"

    LASTFAIL_CMD=""
    LASTFAIL_EC=""
    while IFS= read -r line; do
      case "$line" in
        LASTFAIL_CMD=*) LASTFAIL_CMD="${line#LASTFAIL_CMD=}" ;;
        LASTFAIL_EC=*)  LASTFAIL_EC="${line#LASTFAIL_EC=}" ;;
      esac
    done <<< "$PARSED"
    if [ -n "$LASTFAIL_CMD" ]; then
      BUF+="  ${RED}last failure${RESET}\n"
      BUF+="    ${DIM}cmd${RESET}   ${LASTFAIL_CMD}\n"
      BUF+="    ${DIM}exit${RESET}  ${LASTFAIL_EC}\n"
    fi
  fi

  printf '\033[2J\033[H'
  echo -ne "$BUF"

  sleep "$INTERVAL"
done
