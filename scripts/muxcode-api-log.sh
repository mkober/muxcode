#!/bin/bash
# muxcode-api-log.sh - Poll API request history every N seconds
# Used in the api window's left pane during muxcode sessions
#
# Reads from .muxcode/api/history.jsonl (project-local, not /tmp).
# Fields: ts, collection, request, method, url, status, duration_ms
#
# Usage: muxcode-api-log.sh [interval_seconds]

set -uo pipefail

INTERVAL="${1:-5}"
HISTORY_FILE=".muxcode/api/history.jsonl"

# Dracula colors
PURPLE='\033[38;5;141m'
CYAN='\033[38;5;117m'
GREEN='\033[38;5;80m'
PINK='\033[38;5;212m'
RED='\033[38;5;203m'
YELLOW='\033[38;5;228m'
ORANGE='\033[38;5;215m'
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

# Color an HTTP status code
status_color() {
  local code="$1"
  if [ "$code" -ge 200 ] 2>/dev/null && [ "$code" -lt 300 ]; then
    echo "$GREEN"
  elif [ "$code" -ge 300 ] 2>/dev/null && [ "$code" -lt 400 ]; then
    echo "$CYAN"
  elif [ "$code" -ge 400 ] 2>/dev/null && [ "$code" -lt 500 ]; then
    echo "$YELLOW"
  elif [ "$code" -ge 500 ] 2>/dev/null; then
    echo "$RED"
  else
    echo "$DIM"
  fi
}

# Color an HTTP method
method_color() {
  local method="$1"
  case "$method" in
    GET)    echo "$GREEN" ;;
    POST)   echo "$CYAN" ;;
    PUT)    echo "$YELLOW" ;;
    PATCH)  echo "$ORANGE" ;;
    DELETE) echo "$RED" ;;
    *)      echo "$DIM" ;;
  esac
}

while true; do
  BUF=""
  BUF+="${PURPLE}  api log${RESET}  ${DIM}$(date '+%H:%M:%S')${RESET}  ${DIM}(every ${INTERVAL}s)${RESET}\n"
  BUF+="${DIM}$(printf '%.0s─' {1..50})${RESET}\n"
  BUF+="\n"

  if [ ! -f "$HISTORY_FILE" ] || [ ! -s "$HISTORY_FILE" ]; then
    BUF+="  ${DIM}no requests yet${RESET}\n"

    # Show environment and collection counts
    ENV_COUNT=0
    COL_COUNT=0
    if [ -d ".muxcode/api/environments" ]; then
      ENV_COUNT=$(find .muxcode/api/environments -name '*.json' 2>/dev/null | wc -l | tr -d ' ')
    fi
    if [ -d ".muxcode/api/collections" ]; then
      COL_COUNT=$(find .muxcode/api/collections -name '*.json' 2>/dev/null | wc -l | tr -d ' ')
    fi
    if [ "$ENV_COUNT" -gt 0 ] || [ "$COL_COUNT" -gt 0 ]; then
      BUF+="\n  ${DIM}envs${RESET} ${CYAN}${ENV_COUNT}${RESET}  ${DIM}collections${RESET} ${CYAN}${COL_COUNT}${RESET}\n"
    fi

    printf '\033[2J\033[H'
    echo -ne "$BUF"
    sleep "$INTERVAL"
    continue
  fi

  if command -v jq &>/dev/null; then
    TOTAL=$(jq -s 'length' "$HISTORY_FILE" 2>/dev/null || echo 0)
    OK=$(jq -s '[.[] | select(.status >= 200 and .status < 400)] | length' "$HISTORY_FILE" 2>/dev/null || echo 0)
    ERR=$(( TOTAL - OK ))

    # Summary line
    BUF+="  ${DIM}total${RESET} ${CYAN}${TOTAL}${RESET}  ${DIM}ok${RESET} ${GREEN}${OK}${RESET}  ${DIM}err${RESET} ${RED}${ERR}${RESET}\n"

    # Environment and collection counts
    ENV_COUNT=0
    COL_COUNT=0
    if [ -d ".muxcode/api/environments" ]; then
      ENV_COUNT=$(find .muxcode/api/environments -name '*.json' 2>/dev/null | wc -l | tr -d ' ')
    fi
    if [ -d ".muxcode/api/collections" ]; then
      COL_COUNT=$(find .muxcode/api/collections -name '*.json' 2>/dev/null | wc -l | tr -d ' ')
    fi
    BUF+="  ${DIM}envs${RESET} ${CYAN}${ENV_COUNT}${RESET}  ${DIM}collections${RESET} ${CYAN}${COL_COUNT}${RESET}\n"
    BUF+="\n"

    # Recent requests (last 15)
    BUF+="  ${CYAN}recent requests${RESET}\n"
    ENTRY_OFFSET=$(( TOTAL > 15 ? TOTAL - 15 : 0 ))
    ENTRIES=$(jq -s '.[-15:][] | @json' "$HISTORY_FILE" 2>/dev/null)
    REQ_NUM=$ENTRY_OFFSET
    if [ -n "$ENTRIES" ]; then
      while IFS= read -r entry; do
        entry="${entry%\"}"
        entry="${entry#\"}"
        REQ_NUM=$(( REQ_NUM + 1 ))
        raw=$(printf '%s' "$entry" | jq -r '.' 2>/dev/null) || continue
        ts=$(printf '%s' "$raw" | jq -r '.ts // empty' 2>/dev/null)
        method=$(printf '%s' "$raw" | jq -r '.method // empty' 2>/dev/null)
        url=$(printf '%s' "$raw" | jq -r '.url // empty' 2>/dev/null)
        status=$(printf '%s' "$raw" | jq -r '.status // empty' 2>/dev/null)
        duration=$(printf '%s' "$raw" | jq -r '.duration_ms // empty' 2>/dev/null)
        collection=$(printf '%s' "$raw" | jq -r '.collection // empty' 2>/dev/null)
        request=$(printf '%s' "$raw" | jq -r '.request // empty' 2>/dev/null)

        [ -z "$ts" ] && continue
        TIME=$(format_ts "$ts")

        # Truncate long URLs
        if [ ${#url} -gt 35 ]; then
          url="${url:0:32}..."
        fi

        NUM_LABEL=$(printf '#%-3s' "$REQ_NUM")
        MCOL=$(method_color "$method")
        SCOL=$(status_color "$status")
        DUR="${duration:-?}ms"

        BUF+="    ${DIM}${NUM_LABEL}${RESET} ${DIM}${TIME}${RESET}  ${MCOL}$(printf '%-6s' "$method")${RESET} ${SCOL}${status}${RESET} ${DIM}${DUR}${RESET}\n"
        BUF+="         ${DIM}${url}${RESET}\n"

        # Show collection/request name on third line if present
        if [ -n "$collection" ] && [ -n "$request" ]; then
          BUF+="         ${DIM}↳ ${collection}/${request}${RESET}\n"
        fi
      done <<< "$ENTRIES"
    fi
    BUF+="\n"

    # Average response time
    AVG=$(jq -s 'if length > 0 then ([.[].duration_ms] | add / length | floor) else 0 end' "$HISTORY_FILE" 2>/dev/null || echo 0)
    BUF+="  ${DIM}avg response time${RESET} ${CYAN}${AVG}ms${RESET}\n"

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
ok = sum(1 for e in entries if 200 <= e.get("status", 0) < 400)
err = total - ok
print(f"TOTAL={total}")
print(f"OK={ok}")
print(f"ERR={err}")
offset = max(0, total - 15)
recent = entries[-15:]
for i, e in enumerate(recent):
    ts = e.get("ts", 0)
    method = e.get("method", "")
    url = e.get("url", "")
    status = e.get("status", 0)
    dur = e.get("duration_ms", 0)
    col = e.get("collection", "")
    req = e.get("request", "")
    num = offset + i + 1
    if len(url) > 35:
        url = url[:32] + "..."
    detail = f"{col}/{req}" if col and req else ""
    print(f"ENTRY={ts}\t{method}\t{url}\t{status}\t{dur}\t{num}\t{detail}")
if entries:
    avg = sum(e.get("duration_ms", 0) for e in entries) // len(entries)
    print(f"AVG={avg}")
' "$HISTORY_FILE" 2>/dev/null)

    TOTAL=0; OK=0; ERR=0
    while IFS= read -r line; do
      case "$line" in
        TOTAL=*) TOTAL="${line#TOTAL=}" ;;
        OK=*)    OK="${line#OK=}" ;;
        ERR=*)   ERR="${line#ERR=}" ;;
      esac
    done <<< "$PARSED"

    BUF+="  ${DIM}total${RESET} ${CYAN}${TOTAL}${RESET}  ${DIM}ok${RESET} ${GREEN}${OK}${RESET}  ${DIM}err${RESET} ${RED}${ERR}${RESET}\n"
    BUF+="\n"

    BUF+="  ${CYAN}recent requests${RESET}\n"
    while IFS= read -r line; do
      case "$line" in
        ENTRY=*)
          line="${line#ENTRY=}"
          IFS=$'\t' read -r ts method url status dur num detail <<< "$line"
          TIME=$(format_ts "$ts")
          NUM_LABEL=$(printf '#%-3s' "$num")
          MCOL=$(method_color "$method")
          SCOL=$(status_color "$status")
          BUF+="    ${DIM}${NUM_LABEL}${RESET} ${DIM}${TIME}${RESET}  ${MCOL}$(printf '%-6s' "$method")${RESET} ${SCOL}${status}${RESET} ${DIM}${dur}ms${RESET}\n"
          BUF+="         ${DIM}${url}${RESET}\n"
          if [ -n "$detail" ]; then
            BUF+="         ${DIM}↳ ${detail}${RESET}\n"
          fi
          ;;
      esac
    done <<< "$PARSED"
    BUF+="\n"

    # Average response time
    PY_AVG=""
    while IFS= read -r line; do
      case "$line" in
        AVG=*) PY_AVG="${line#AVG=}" ;;
      esac
    done <<< "$PARSED"
    if [ -n "$PY_AVG" ]; then
      BUF+="  ${DIM}avg response time${RESET} ${CYAN}${PY_AVG}ms${RESET}\n"
    fi
  fi

  printf '\033[2J\033[H'
  echo -ne "$BUF"

  sleep "$INTERVAL"
done
