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
  BUF+="${PURPLE}  Test${RESET}  ${DIM}$(date '+%H:%M:%S')${RESET}  ${DIM}(every ${INTERVAL}s)${RESET}\n"
  BUF+="  ${DIM}$(printf '%.0s─' {1..46})${RESET}\n"
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

        # Test number prefix
        NUM_LABEL=$(printf '#%-3s' "$TEST_NUM")

        if [ "$ec" = "0" ]; then
          BUF+="    ${DIM}${NUM_LABEL}${RESET} ${DIM}${TIME}${RESET}  ${GREEN}OK${RESET}    ${cmd}\n"
        else
          BUF+="    ${DIM}${NUM_LABEL}${RESET} ${DIM}${TIME}${RESET}  ${RED}FAIL${RESET}  ${cmd}  ${DIM}exit ${ec}${RESET}\n"
        fi

        # Show description on second line if present
        if [ -n "$desc" ]; then
          BUF+="         ${DIM}↳ ${desc}${RESET}\n"
        fi
      done <<< "$ENTRIES"
    fi
    BUF+="\n"

    # Last test output
    LAST_EC=$(jq -s '.[-1].exit_code // "0"' "$HISTORY_FILE" 2>/dev/null)
    LAST_EC=$(printf '%s' "$LAST_EC" | tr -d '"')
    LAST_TS=$(jq -s '.[-1].ts // 0' "$HISTORY_FILE" 2>/dev/null)
    LAST_TS=$(printf '%s' "$LAST_TS" | tr -d '"')
    LAST_TIME=""
    if [ -n "$LAST_TS" ] && [ "$LAST_TS" != "0" ]; then
      LAST_TIME=$(format_ts "$LAST_TS")
    fi
    LAST_OUTPUT=$(jq -s -r '.[-1].output // ""' "$HISTORY_FILE" 2>/dev/null)
    LAST_ERRORS=$(jq -s -r '.[-1].errors // ""' "$HISTORY_FILE" 2>/dev/null)

    # For failed tests, prefer errors field; for passing tests, show output
    if [ "$LAST_EC" != "0" ]; then
      DISPLAY_OUTPUT="${LAST_ERRORS:-$LAST_OUTPUT}"
    else
      DISPLAY_OUTPUT="$LAST_OUTPUT"
    fi

    if [ -n "$DISPLAY_OUTPUT" ] || [ "$LAST_EC" != "0" ]; then
      if [ "$LAST_EC" = "0" ]; then
        BUF+="  ${GREEN}⏺ Tests passed${RESET}  ${DIM}${LAST_TIME}${RESET}\n\n"
      else
        LAST_CMD=$(jq -s -r '.[-1].command // ""' "$HISTORY_FILE" 2>/dev/null)
        BUF+="  ${RED}⏺ Tests failed${RESET}  ${DIM}${LAST_TIME}${RESET}\n"
        BUF+="    ${DIM}cmd${RESET}  ${LAST_CMD}  ${DIM}exit ${LAST_EC}${RESET}\n\n"
      fi
      if [ -n "$DISPLAY_OUTPUT" ]; then
        LINE_COUNT=0
        while IFS= read -r oline; do
          oline=$(printf '%s' "$oline" | sed 's/\x1b\[[0-9;]*[A-Za-z]//g; s/^[[:space:]]*//')
          [ -z "$oline" ] && continue
          case "$oline" in
            "Exit code:"*) continue ;;
          esac
          if [ "$LAST_EC" != "0" ]; then
            BUF+="    ${RED}- ${oline}${RESET}\n"
          else
            if [ "$LINE_COUNT" -eq 0 ]; then
              BUF+="  ${CYAN}${oline}${RESET}\n"
            else
              BUF+="    ${DIM}- ${oline}${RESET}\n"
            fi
          fi
          LINE_COUNT=$(( LINE_COUNT + 1 ))
          [ "$LINE_COUNT" -ge 20 ] && break
        done <<< "$DISPLAY_OUTPUT"
        if [ "$LINE_COUNT" -eq 0 ] && [ "$LAST_EC" != "0" ]; then
          BUF+="    ${DIM}- No error details captured${RESET}\n"
        fi
      fi
      if [ "$LAST_EC" = "0" ]; then
        BUF+="    ${DIM}- All tests passed${RESET}\n"
      fi
      BUF+="\n"
    fi

    # Last error summary — show most recent failure when latest run passed
    if [ "$LAST_EC" = "0" ] && [ "$FAIL" -gt 0 ]; then
      PREV_FAIL=$(jq -s '[.[] | select(.exit_code != "0" and .exit_code != 0)] | last' "$HISTORY_FILE" 2>/dev/null)
      if [ -n "$PREV_FAIL" ] && [ "$PREV_FAIL" != "null" ]; then
        PF_TS=$(printf '%s' "$PREV_FAIL" | jq -r '.ts // empty' 2>/dev/null)
        PF_CMD=$(printf '%s' "$PREV_FAIL" | jq -r '.command // empty' 2>/dev/null)
        PF_EC=$(printf '%s' "$PREV_FAIL" | jq -r '.exit_code // empty' 2>/dev/null)
        PF_ERRORS=$(printf '%s' "$PREV_FAIL" | jq -r '.errors // empty' 2>/dev/null)
        PF_OUTPUT=$(printf '%s' "$PREV_FAIL" | jq -r '.output // empty' 2>/dev/null)
        PF_DISPLAY="${PF_ERRORS:-$PF_OUTPUT}"
        if [ -n "$PF_TS" ]; then
          PF_TIME=$(format_ts "$PF_TS")
          BUF+="  ${YELLOW}⏺ Last errors${RESET}  ${DIM}${PF_TIME}${RESET}\n"
          BUF+="    ${DIM}cmd${RESET}  ${PF_CMD}  ${DIM}exit ${PF_EC}${RESET}\n"
          if [ -n "$PF_DISPLAY" ]; then
            PF_LINE_COUNT=0
            while IFS= read -r oline; do
              oline=$(printf '%s' "$oline" | sed 's/\x1b\[[0-9;]*[A-Za-z]//g; s/^[[:space:]]*//')
              [ -z "$oline" ] && continue
              case "$oline" in
                "Exit code:"*) continue ;;
              esac
              BUF+="    ${YELLOW}- ${oline}${RESET}\n"
              PF_LINE_COUNT=$(( PF_LINE_COUNT + 1 ))
              [ "$PF_LINE_COUNT" -ge 20 ] && break
            done <<< "$PF_DISPLAY"
          fi
          BUF+="\n"
        fi
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
    status = "OK" if ec == "0" else "FAIL"
    print(f"ENTRY={ts}\t{status}\t{cmd}\t{ec}\t{num}\t{desc}")
last = entries[-1] if entries else {}
last_ec = str(last.get("exit_code", "0"))
last_ts = last.get("ts", 0)
last_cmd = last.get("command", "")
last_output = last.get("output", "")
last_errors = last.get("errors", "")
import re
print(f"LAST_TS={last_ts}")
print(f"LAST_EC={last_ec}")
print(f"LAST_CMD={last_cmd}")
# For failures, prefer errors; for success, show output
display = last_errors if (last_ec != "0" and last_errors) else last_output
if display:
    for ol in display.strip().split("\n"):
        ol = re.sub(r"\x1b\[[0-9;]*[A-Za-z]", "", ol).strip()
        if ol and not ol.startswith("Exit code:"):
            print(f"LAST_DISPLAY_LINE={ol}")
if last_ec == "0" and failed > 0:
    failures = [e for e in entries if str(e.get("exit_code", "1")) != "0"]
    if failures:
        pf = failures[-1]
        pf_ts = pf.get("ts", 0)
        pf_cmd = pf.get("command", "")
        pf_ec = str(pf.get("exit_code", "?"))
        pf_errors = pf.get("errors", "")
        pf_output = pf.get("output", "")
        pf_display = pf_errors if pf_errors else pf_output
        print(f"PREV_FAIL_TS={pf_ts}")
        print(f"PREV_FAIL_CMD={pf_cmd}")
        print(f"PREV_FAIL_EC={pf_ec}")
        if pf_display:
            count = 0
            for ol in pf_display.strip().split("\n"):
                ol = re.sub(r"\x1b\[[0-9;]*[A-Za-z]", "", ol).strip()
                if ol and not ol.startswith("Exit code:"):
                    print(f"PREV_FAIL_LINE={ol}")
                    count += 1
                    if count >= 20:
                        break
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

    # Last test summary
    PY_LAST_EC=""
    PY_LAST_TS=""
    PY_LAST_CMD=""
    HAS_DISPLAY=0
    PY_LINE_COUNT=0
    while IFS= read -r line; do
      case "$line" in
        LAST_TS=*) PY_LAST_TS="${line#LAST_TS=}" ;;
        LAST_EC=*) PY_LAST_EC="${line#LAST_EC=}" ;;
        LAST_CMD=*) PY_LAST_CMD="${line#LAST_CMD=}" ;;
        LAST_DISPLAY_LINE=*)
          if [ "$HAS_DISPLAY" -eq 0 ]; then
            HAS_DISPLAY=1
            PY_LAST_TIME=""
            if [ -n "$PY_LAST_TS" ] && [ "$PY_LAST_TS" != "0" ]; then
              PY_LAST_TIME=$(format_ts "$PY_LAST_TS")
            fi
            if [ "$PY_LAST_EC" = "0" ]; then
              BUF+="  ${GREEN}⏺ Tests passed${RESET}  ${DIM}${PY_LAST_TIME}${RESET}\n\n"
            else
              BUF+="  ${RED}⏺ Tests failed${RESET}  ${DIM}${PY_LAST_TIME}${RESET}\n"
              BUF+="    ${DIM}cmd${RESET}  ${PY_LAST_CMD}  ${DIM}exit ${PY_LAST_EC}${RESET}\n\n"
            fi
          fi
          OL="${line#LAST_DISPLAY_LINE=}"
          if [ "$PY_LAST_EC" != "0" ]; then
            BUF+="    ${RED}- ${OL}${RESET}\n"
          else
            if [ "$PY_LINE_COUNT" -eq 0 ]; then
              BUF+="  ${CYAN}${OL}${RESET}\n"
            else
              BUF+="    ${DIM}- ${OL}${RESET}\n"
            fi
          fi
          PY_LINE_COUNT=$(( PY_LINE_COUNT + 1 ))
          ;;
      esac
    done <<< "$PARSED"
    # Handle failed test with no display lines
    if [ "$HAS_DISPLAY" -eq 0 ] && [ -n "$PY_LAST_EC" ] && [ "$PY_LAST_EC" != "0" ]; then
      PY_LAST_TIME=""
      if [ -n "$PY_LAST_TS" ] && [ "$PY_LAST_TS" != "0" ]; then
        PY_LAST_TIME=$(format_ts "$PY_LAST_TS")
      fi
      BUF+="  ${RED}⏺ Tests failed${RESET}  ${DIM}${PY_LAST_TIME}${RESET}\n"
      BUF+="    ${DIM}cmd${RESET}  ${PY_LAST_CMD}  ${DIM}exit ${PY_LAST_EC}${RESET}\n"
      BUF+="    ${DIM}- No error details captured${RESET}\n"
      HAS_DISPLAY=1
    fi
    if [ "$HAS_DISPLAY" -eq 1 ] && [ "$PY_LAST_EC" = "0" ]; then
      BUF+="    ${DIM}- All tests passed${RESET}\n"
    fi
    if [ "$HAS_DISPLAY" -eq 1 ]; then
      BUF+="\n"
    fi

    # Last error summary — show most recent failure when latest run passed
    PF_TS="" PF_CMD="" PF_EC="" PF_HAS_LINES=0
    while IFS= read -r line; do
      case "$line" in
        PREV_FAIL_TS=*)  PF_TS="${line#PREV_FAIL_TS=}" ;;
        PREV_FAIL_CMD=*) PF_CMD="${line#PREV_FAIL_CMD=}" ;;
        PREV_FAIL_EC=*)  PF_EC="${line#PREV_FAIL_EC=}" ;;
        PREV_FAIL_LINE=*)
          if [ "$PF_HAS_LINES" -eq 0 ]; then
            PF_HAS_LINES=1
            PF_TIME=$(format_ts "$PF_TS")
            BUF+="  ${YELLOW}⏺ Last errors${RESET}  ${DIM}${PF_TIME}${RESET}\n"
            BUF+="    ${DIM}cmd${RESET}  ${PF_CMD}  ${DIM}exit ${PF_EC}${RESET}\n"
          fi
          OL="${line#PREV_FAIL_LINE=}"
          BUF+="    ${YELLOW}- ${OL}${RESET}\n"
          ;;
      esac
    done <<< "$PARSED"
    if [ -n "$PF_TS" ] && [ "$PF_HAS_LINES" -eq 0 ]; then
      PF_TIME=$(format_ts "$PF_TS")
      BUF+="  ${YELLOW}⏺ Last errors${RESET}  ${DIM}${PF_TIME}${RESET}\n"
      BUF+="    ${DIM}cmd${RESET}  ${PF_CMD}  ${DIM}exit ${PF_EC}${RESET}\n"
    fi
    if [ -n "$PF_TS" ]; then
      BUF+="\n"
    fi
  fi

  printf '\033[2J\033[H'
  echo -ne "$BUF"

  sleep "$INTERVAL"
done
