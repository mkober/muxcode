#!/bin/bash
# muxcode-test-log.sh - Poll test history every N seconds
# Used in the test window's left pane during muxcode sessions
#
# Usage: muxcode-test-log.sh [interval_seconds]

set -uo pipefail

INTERVAL="${1:-5}"
SESSION="${BUS_SESSION:-$(tmux display-message -p '#S' 2>/dev/null || echo default)}"
HISTORY_FILE="/tmp/muxcode-bus-${SESSION}/test-history.jsonl"

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
  BUF+="${PURPLE}  test log${RESET}  ${DIM}$(date '+%H:%M:%S')${RESET}  ${DIM}(every ${INTERVAL}s)${RESET}\n"
  BUF+="${DIM}$(printf '%.0s─' {1..50})${RESET}\n"
  BUF+="\n"

  if [ ! -f "$HISTORY_FILE" ] || [ ! -s "$HISTORY_FILE" ]; then
    BUF+="  ${DIM}no tests yet${RESET}\n"
    printf '\033[2J\033[H'
    echo -ne "$BUF"
    sleep "$INTERVAL"
    continue
  fi

  # Parse test history with jq (python3 fallback)
  if command -v jq &>/dev/null; then
    TOTAL=$(jq -s 'length' "$HISTORY_FILE" 2>/dev/null || echo 0)
    PASS=$(jq -s '[.[] | select(.exit_code == "0" or .exit_code == 0)] | length' "$HISTORY_FILE" 2>/dev/null || echo 0)
    FAIL=$(( TOTAL - PASS ))

    # Summary line
    BUF+="  ${DIM}total${RESET} ${CYAN}${TOTAL}${RESET}  ${DIM}pass${RESET} ${GREEN}${PASS}${RESET}  ${DIM}fail${RESET} ${RED}${FAIL}${RESET}\n"
    BUF+="\n"

    # Recent tests (last 15)
    BUF+="  ${CYAN}recent tests${RESET}\n"
    ENTRY_OFFSET=$(( TOTAL > 15 ? TOTAL - 15 : 0 ))
    ENTRIES=$(jq -s '.[-15:][] | @json' "$HISTORY_FILE" 2>/dev/null)
    TEST_NUM=$ENTRY_OFFSET
    if [ -n "$ENTRIES" ]; then
      while IFS= read -r entry; do
        entry="${entry%\"}"
        entry="${entry#\"}"
        TEST_NUM=$(( TEST_NUM + 1 ))
        # Unescape the JSON string
        raw=$(printf '%s' "$entry" | jq -r '.' 2>/dev/null) || continue
        ts=$(printf '%s' "$raw" | jq -r '.ts // empty' 2>/dev/null)
        cmd=$(printf '%s' "$raw" | jq -r '.command // empty' 2>/dev/null)
        desc=$(printf '%s' "$raw" | jq -r '.description // empty' 2>/dev/null)
        ec=$(printf '%s' "$raw" | jq -r '.exit_code // empty' 2>/dev/null)

        [ -z "$ts" ] && continue
        TIME=$(format_ts "$ts")

        # Truncate long commands
        if [ ${#cmd} -gt 35 ]; then
          cmd="${cmd:0:32}..."
        fi

        # Test number prefix
        NUM_LABEL=$(printf '#%-3s' "$TEST_NUM")

        if [ "$ec" = "0" ]; then
          BUF+="    ${DIM}${NUM_LABEL}${RESET} ${DIM}${TIME}${RESET}  ${GREEN}OK${RESET}    ${cmd}\n"
        else
          BUF+="    ${DIM}${NUM_LABEL}${RESET} ${DIM}${TIME}${RESET}  ${RED}FAIL${RESET}  ${cmd}  ${DIM}exit ${ec}${RESET}\n"
        fi

        # Show description on second line if present
        if [ -n "$desc" ]; then
          if [ ${#desc} -gt 44 ]; then
            desc="${desc:0:41}..."
          fi
          BUF+="         ${DIM}↳ ${desc}${RESET}\n"
        fi
      done <<< "$ENTRIES"
    fi
    BUF+="\n"

    # Last test output
    LAST_EC=$(jq -s '.[-1].exit_code // "0"' "$HISTORY_FILE" 2>/dev/null)
    LAST_EC=$(printf '%s' "$LAST_EC" | tr -d '"')
    LAST_OUTPUT=$(jq -s -r '.[-1].output // ""' "$HISTORY_FILE" 2>/dev/null)

    if [ -n "$LAST_OUTPUT" ]; then
      if [ "$LAST_EC" = "0" ]; then
        BUF+="  ${GREEN}⏺ Tests passed:${RESET}\n\n"
      else
        BUF+="  ${RED}⏺ Tests failed:${RESET}\n\n"
      fi
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
      done <<< "$LAST_OUTPUT"
      if [ "$LAST_EC" = "0" ]; then
        BUF+="    ${DIM}- All tests passed${RESET}\n"
      fi
      BUF+="\n"
    fi

    # Last failure detail (if most recent test failed and no output captured)
    if [ "$LAST_EC" != "0" ] && [ -z "$LAST_OUTPUT" ]; then
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
    desc = e.get("description", "")
    ec = str(e.get("exit_code", "?"))
    num = offset + i + 1
    if len(cmd) > 35:
        cmd = cmd[:32] + "..."
    if len(desc) > 44:
        desc = desc[:41] + "..."
    status = "OK" if ec == "0" else "FAIL"
    print(f"ENTRY={ts}\t{status}\t{cmd}\t{ec}\t{num}\t{desc}")
last = entries[-1] if entries else {}
last_ec = str(last.get("exit_code", "0"))
last_output = last.get("output", "")
if last_output:
    import re
    print(f"LAST_EC={last_ec}")
    for ol in last_output.strip().split("\n"):
        ol = re.sub(r"\x1b\[[0-9;]*[A-Za-z]", "", ol).strip()
        if ol:
            if len(ol) > 60:
                ol = ol[:57] + "..."
            print(f"LAST_OUTPUT_LINE={ol}")
if entries and last_ec != "0" and not last_output:
    print(f"LASTFAIL_CMD={last.get('command', '')}")
    print(f"LASTFAIL_EC={last_ec}")
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

    BUF+="  ${CYAN}recent tests${RESET}\n"
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

    # Last test output
    PY_LAST_EC=""
    HAS_OUTPUT=0
    PY_FIRST_LINE=1
    while IFS= read -r line; do
      case "$line" in
        LAST_EC=*) PY_LAST_EC="${line#LAST_EC=}" ;;
        LAST_OUTPUT_LINE=*)
          if [ "$HAS_OUTPUT" -eq 0 ]; then
            HAS_OUTPUT=1
            if [ "$PY_LAST_EC" = "0" ]; then
              BUF+="  ${GREEN}⏺ Tests passed:${RESET}\n\n"
            else
              BUF+="  ${RED}⏺ Tests failed:${RESET}\n\n"
            fi
          fi
          OL="${line#LAST_OUTPUT_LINE=}"
          if [ "$PY_FIRST_LINE" -eq 1 ]; then
            BUF+="  ${CYAN}${OL}${RESET}\n"
            PY_FIRST_LINE=0
          else
            BUF+="    ${DIM}- ${OL}${RESET}\n"
          fi
          ;;
      esac
    done <<< "$PARSED"
    if [ "$HAS_OUTPUT" -eq 1 ] && [ "$PY_LAST_EC" = "0" ]; then
      BUF+="    ${DIM}- All tests passed${RESET}\n"
    fi
    if [ "$HAS_OUTPUT" -eq 1 ]; then
      BUF+="\n"
    fi

    # Last failure detail (fallback when no output captured)
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
