#!/bin/bash
# muxcode-review-log.sh - Poll review history every N seconds
# Used in the review window's left pane during muxcode sessions
#
# Usage: muxcode-review-log.sh [interval_seconds]

set -uo pipefail

INTERVAL="${1:-5}"
SESSION="${BUS_SESSION:-$(tmux display-message -p '#S' 2>/dev/null || echo default)}"
HISTORY_FILE="/tmp/muxcode-bus-${SESSION}/review-history.jsonl"

# Dracula colors
PURPLE='\033[38;5;141m'
CYAN='\033[38;5;117m'
GREEN='\033[38;5;80m'
PINK='\033[38;5;212m'
RED='\033[38;5;203m'
YELLOW='\033[38;5;228m'
DIM='\033[2m'
RESET='\033[0m'

# Padding
PAD="   "          # 3-space left padding
CONT_PAD="     "   # 5-space continuation indent (for wrapped lines)
ENTRY_PAD="         "  # 9-space entry payload indent
RIGHT_MARGIN=2

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

# Extract issue counts from summary string like "2 must-fix, 3 should-fix, 1 nits"
extract_counts() {
  local summary="$1"
  local must_fix=0 should_fix=0 nits=0
  # Try to extract numbers before each keyword
  if [[ "$summary" =~ ([0-9]+)[[:space:]]*must-fix ]]; then
    must_fix="${BASH_REMATCH[1]}"
  fi
  if [[ "$summary" =~ ([0-9]+)[[:space:]]*should-fix ]]; then
    should_fix="${BASH_REMATCH[1]}"
  fi
  if [[ "$summary" =~ ([0-9]+)[[:space:]]*nit ]]; then
    nits="${BASH_REMATCH[1]}"
  fi
  echo "$must_fix $should_fix $nits"
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
  BUF+="${PAD}${PURPLE}Review${RESET}  ${DIM}$(date '+%H:%M:%S')${RESET}  ${DIM}(every ${INTERVAL}s)${RESET}\n"
  BUF+="${PAD}${DIM}$(printf '%.0s─' $(seq 1 "$SEP_WIDTH"))${RESET}\n"
  BUF+="\n"

  if [ ! -f "$HISTORY_FILE" ] || [ ! -s "$HISTORY_FILE" ]; then
    BUF+="${PAD}${DIM}no reviews yet${RESET}\n"
    printf '\033[2J\033[H'
    echo -ne "$BUF"
    sleep "$INTERVAL"
    continue
  fi

  # Parse review history with jq (python3 fallback)
  if command -v jq &>/dev/null; then
    TOTAL=$(jq -s 'length' "$HISTORY_FILE" 2>/dev/null || echo 0)
    CLEAN=$(jq -s '[.[] | select(.exit_code == "0" or .exit_code == 0)] | length' "$HISTORY_FILE" 2>/dev/null || echo 0)
    ISSUES=$(( TOTAL - CLEAN ))

    # Summary line
    BUF+="${PAD}${DIM}total${RESET} ${CYAN}${TOTAL}${RESET}  ${DIM}clean${RESET} ${GREEN}${CLEAN}${RESET}  ${DIM}issues${RESET} ${RED}${ISSUES}${RESET}\n"
    BUF+="\n"

    # Recent reviews (last 15)
    BUF+="${PAD}${CYAN}recent reviews${RESET}\n"
    ENTRY_OFFSET=$(( TOTAL > 15 ? TOTAL - 15 : 0 ))
    ENTRIES=$(jq -s '.[-15:][] | @json' "$HISTORY_FILE" 2>/dev/null)
    REVIEW_NUM=$ENTRY_OFFSET
    if [ -n "$ENTRIES" ]; then
      while IFS= read -r entry; do
        entry="${entry%\"}"
        entry="${entry#\"}"
        REVIEW_NUM=$(( REVIEW_NUM + 1 ))
        # Unescape the JSON string
        raw=$(printf '%s' "$entry" | jq -r '.' 2>/dev/null) || continue
        ts=$(printf '%s' "$raw" | jq -r '.ts // empty' 2>/dev/null)
        summary=$(printf '%s' "$raw" | jq -r '.summary // empty' 2>/dev/null)
        ec=$(printf '%s' "$raw" | jq -r '.exit_code // empty' 2>/dev/null)

        [ -z "$ts" ] && continue
        TIME=$(format_ts "$ts")

        DISPLAY_SUMMARY="$summary"

        # Review number prefix
        NUM_LABEL=$(printf '#%-3s' "$REVIEW_NUM")

        if [ "$ec" = "0" ]; then
          WRAPPED_SUMMARY=$(word_wrap "${DISPLAY_SUMMARY}" "$ENTRY_CONTENT_WIDTH")
          FIRST_WRAP=1
          while IFS= read -r wline; do
            if [ "$FIRST_WRAP" -eq 1 ]; then
              BUF+="${CONT_PAD}${DIM}${NUM_LABEL}${RESET} ${DIM}${TIME}${RESET}  ${GREEN}CLEAN${RESET}   ${wline}\n"
              FIRST_WRAP=0
            else
              BUF+="${ENTRY_PAD}${wline}\n"
            fi
          done <<< "$WRAPPED_SUMMARY"
        else
          WRAPPED_SUMMARY=$(word_wrap "${DISPLAY_SUMMARY}" "$ENTRY_CONTENT_WIDTH")
          FIRST_WRAP=1
          while IFS= read -r wline; do
            if [ "$FIRST_WRAP" -eq 1 ]; then
              BUF+="${CONT_PAD}${DIM}${NUM_LABEL}${RESET} ${DIM}${TIME}${RESET}  ${RED}ISSUES${RESET}  ${wline}\n"
              FIRST_WRAP=0
            else
              BUF+="${ENTRY_PAD}${wline}\n"
            fi
          done <<< "$WRAPPED_SUMMARY"
        fi

        # Extract and show issue counts on second line
        read -r mf sf ni <<< "$(extract_counts "$summary")"
        if [ "$mf" -gt 0 ] || [ "$sf" -gt 0 ] || [ "$ni" -gt 0 ]; then
          BUF+="${ENTRY_PAD}${DIM}↳ must-fix: ${RESET}${RED}${mf}${RESET}${DIM}  should-fix: ${RESET}${YELLOW}${sf}${RESET}${DIM}  nits: ${RESET}${CYAN}${ni}${RESET}\n"
        fi
      done <<< "$ENTRIES"
    fi
    BUF+="\n"

    # Last review detail (output from most recent review)
    LAST_EC=$(jq -s '.[-1].exit_code // "0"' "$HISTORY_FILE" 2>/dev/null)
    LAST_EC=$(printf '%s' "$LAST_EC" | tr -d '"')
    LAST_OUTPUT=$(jq -s -r '.[-1].output // ""' "$HISTORY_FILE" 2>/dev/null)
    LAST_SUMMARY=$(jq -s -r '.[-1].summary // ""' "$HISTORY_FILE" 2>/dev/null)

    if [ -n "$LAST_OUTPUT" ]; then
      if [ "$LAST_EC" = "0" ]; then
        BUF+="${PAD}${GREEN}⏺ Review clean:${RESET}\n\n"
      else
        BUF+="${PAD}${RED}⏺ Review found issues:${RESET}\n\n"
      fi
      FIRST_LINE=1
      while IFS= read -r oline; do
        oline=$(printf '%s' "$oline" | sed 's/\x1b\[[0-9;]*[A-Za-z]//g; s/^[[:space:]]*//')
        [ -z "$oline" ] && continue
        if [ "$FIRST_LINE" -eq 1 ]; then
          WRAPPED_LINE=$(word_wrap "${oline}" "$CONTENT_WIDTH")
          FIRST_WRAP=1
          while IFS= read -r wline; do
            if [ "$FIRST_WRAP" -eq 1 ]; then
              BUF+="${PAD}${CYAN}${wline}${RESET}\n"
              FIRST_WRAP=0
            else
              BUF+="${CONT_PAD}${CYAN}${wline}${RESET}\n"
            fi
          done <<< "$WRAPPED_LINE"
          FIRST_LINE=0
        else
          WRAPPED_LINE=$(word_wrap "${oline}" "$CONTENT_WIDTH")
          FIRST_WRAP=1
          while IFS= read -r wline; do
            if [ "$FIRST_WRAP" -eq 1 ]; then
              BUF+="${CONT_PAD}${DIM}- ${wline}${RESET}\n"
              FIRST_WRAP=0
            else
              BUF+="${CONT_PAD}${DIM}  ${wline}${RESET}\n"
            fi
          done <<< "$WRAPPED_LINE"
        fi
      done <<< "$LAST_OUTPUT"
      BUF+="\n"
    elif [ -n "$LAST_SUMMARY" ]; then
      # Show summary as detail when no output captured
      if [ "$LAST_EC" = "0" ]; then
        WRAPPED_LINE=$(word_wrap "Last review: ${LAST_SUMMARY}" "$CONTENT_WIDTH")
        FIRST_WRAP=1
        while IFS= read -r wline; do
          if [ "$FIRST_WRAP" -eq 1 ]; then
            BUF+="${PAD}${GREEN}⏺ ${wline}${RESET}\n"
            FIRST_WRAP=0
          else
            BUF+="${CONT_PAD}${GREEN}${wline}${RESET}\n"
          fi
        done <<< "$WRAPPED_LINE"
      else
        WRAPPED_LINE=$(word_wrap "Last review: ${LAST_SUMMARY}" "$CONTENT_WIDTH")
        FIRST_WRAP=1
        while IFS= read -r wline; do
          if [ "$FIRST_WRAP" -eq 1 ]; then
            BUF+="${PAD}${RED}⏺ ${wline}${RESET}\n"
            FIRST_WRAP=0
          else
            BUF+="${CONT_PAD}${RED}${wline}${RESET}\n"
          fi
        done <<< "$WRAPPED_LINE"
      fi
    fi

  else
    # python3 fallback
    PARSED=$(python3 -c '
import json, sys, re
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
clean = sum(1 for e in entries if str(e.get("exit_code", "1")) == "0")
issues = total - clean
print(f"TOTAL={total}")
print(f"CLEAN={clean}")
print(f"ISSUES={issues}")
offset = max(0, total - 15)
recent = entries[-15:]
for i, e in enumerate(recent):
    ts = e.get("ts", 0)
    summary = e.get("summary", "")
    ec = str(e.get("exit_code", "?"))
    num = offset + i + 1
    display = summary
    status = "CLEAN" if ec == "0" else "ISSUES"
    # Extract counts
    mf = re.search(r"(\d+)\s*must-fix", summary)
    sf = re.search(r"(\d+)\s*should-fix", summary)
    ni = re.search(r"(\d+)\s*nit", summary)
    mf = mf.group(1) if mf else "0"
    sf = sf.group(1) if sf else "0"
    ni = ni.group(1) if ni else "0"
    print(f"ENTRY={ts}\t{status}\t{display}\t{ec}\t{num}\t{mf}\t{sf}\t{ni}")
last = entries[-1] if entries else {}
last_ec = str(last.get("exit_code", "0"))
last_output = last.get("output", "")
last_summary = last.get("summary", "")
if last_output:
    print(f"LAST_EC={last_ec}")
    for ol in last_output.strip().split("\n"):
        ol = re.sub(r"\x1b\[[0-9;]*[A-Za-z]", "", ol).strip()
        if ol:
            print(f"LAST_OUTPUT_LINE={ol}")
elif last_summary:
    print(f"LAST_EC={last_ec}")
    print(f"LAST_SUMMARY={last_summary}")
' "$HISTORY_FILE" 2>/dev/null)

    TOTAL=0; CLEAN=0; ISSUES=0
    while IFS= read -r line; do
      case "$line" in
        TOTAL=*)  TOTAL="${line#TOTAL=}" ;;
        CLEAN=*)  CLEAN="${line#CLEAN=}" ;;
        ISSUES=*) ISSUES="${line#ISSUES=}" ;;
      esac
    done <<< "$PARSED"

    BUF+="${PAD}${DIM}total${RESET} ${CYAN}${TOTAL}${RESET}  ${DIM}clean${RESET} ${GREEN}${CLEAN}${RESET}  ${DIM}issues${RESET} ${RED}${ISSUES}${RESET}\n"
    BUF+="\n"

    BUF+="${PAD}${CYAN}recent reviews${RESET}\n"
    while IFS= read -r line; do
      case "$line" in
        ENTRY=*)
          line="${line#ENTRY=}"
          IFS=$'\t' read -r ts status display ec num mf sf ni <<< "$line"
          TIME=$(format_ts "$ts")
          NUM_LABEL=$(printf '#%-3s' "$num")
          if [ "$status" = "CLEAN" ]; then
            WRAPPED_DISPLAY=$(word_wrap "${display}" "$ENTRY_CONTENT_WIDTH")
            FIRST_WRAP=1
            while IFS= read -r wline; do
              if [ "$FIRST_WRAP" -eq 1 ]; then
                BUF+="${CONT_PAD}${DIM}${NUM_LABEL}${RESET} ${DIM}${TIME}${RESET}  ${GREEN}CLEAN${RESET}   ${wline}\n"
                FIRST_WRAP=0
              else
                BUF+="${ENTRY_PAD}${wline}\n"
              fi
            done <<< "$WRAPPED_DISPLAY"
          else
            WRAPPED_DISPLAY=$(word_wrap "${display}" "$ENTRY_CONTENT_WIDTH")
            FIRST_WRAP=1
            while IFS= read -r wline; do
              if [ "$FIRST_WRAP" -eq 1 ]; then
                BUF+="${CONT_PAD}${DIM}${NUM_LABEL}${RESET} ${DIM}${TIME}${RESET}  ${RED}ISSUES${RESET}  ${wline}\n"
                FIRST_WRAP=0
              else
                BUF+="${ENTRY_PAD}${wline}\n"
              fi
            done <<< "$WRAPPED_DISPLAY"
          fi
          if [ "$mf" != "0" ] || [ "$sf" != "0" ] || [ "$ni" != "0" ]; then
            BUF+="${ENTRY_PAD}${DIM}↳ must-fix: ${RESET}${RED}${mf}${RESET}${DIM}  should-fix: ${RESET}${YELLOW}${sf}${RESET}${DIM}  nits: ${RESET}${CYAN}${ni}${RESET}\n"
          fi
          ;;
      esac
    done <<< "$PARSED"
    BUF+="\n"

    # Last review detail
    PY_LAST_EC=""
    HAS_OUTPUT=0
    PY_FIRST_LINE=1
    PY_LAST_SUMMARY=""
    while IFS= read -r line; do
      case "$line" in
        LAST_EC=*) PY_LAST_EC="${line#LAST_EC=}" ;;
        LAST_SUMMARY=*) PY_LAST_SUMMARY="${line#LAST_SUMMARY=}" ;;
        LAST_OUTPUT_LINE=*)
          if [ "$HAS_OUTPUT" -eq 0 ]; then
            HAS_OUTPUT=1
            if [ "$PY_LAST_EC" = "0" ]; then
              BUF+="${PAD}${GREEN}⏺ Review clean:${RESET}\n\n"
            else
              BUF+="${PAD}${RED}⏺ Review found issues:${RESET}\n\n"
            fi
          fi
          OL="${line#LAST_OUTPUT_LINE=}"
          if [ "$PY_FIRST_LINE" -eq 1 ]; then
            WRAPPED_LINE=$(word_wrap "${OL}" "$CONTENT_WIDTH")
            FIRST_WRAP=1
            while IFS= read -r wline; do
              if [ "$FIRST_WRAP" -eq 1 ]; then
                BUF+="${PAD}${CYAN}${wline}${RESET}\n"
                FIRST_WRAP=0
              else
                BUF+="${CONT_PAD}${CYAN}${wline}${RESET}\n"
              fi
            done <<< "$WRAPPED_LINE"
            PY_FIRST_LINE=0
          else
            WRAPPED_LINE=$(word_wrap "${OL}" "$CONTENT_WIDTH")
            FIRST_WRAP=1
            while IFS= read -r wline; do
              if [ "$FIRST_WRAP" -eq 1 ]; then
                BUF+="${CONT_PAD}${DIM}- ${wline}${RESET}\n"
                FIRST_WRAP=0
              else
                BUF+="${CONT_PAD}${DIM}  ${wline}${RESET}\n"
              fi
            done <<< "$WRAPPED_LINE"
          fi
          ;;
      esac
    done <<< "$PARSED"
    if [ "$HAS_OUTPUT" -eq 1 ]; then
      BUF+="\n"
    elif [ -n "$PY_LAST_SUMMARY" ]; then
      if [ "$PY_LAST_EC" = "0" ]; then
        WRAPPED_LINE=$(word_wrap "Last review: ${PY_LAST_SUMMARY}" "$CONTENT_WIDTH")
        FIRST_WRAP=1
        while IFS= read -r wline; do
          if [ "$FIRST_WRAP" -eq 1 ]; then
            BUF+="${PAD}${GREEN}⏺ ${wline}${RESET}\n"
            FIRST_WRAP=0
          else
            BUF+="${CONT_PAD}${GREEN}${wline}${RESET}\n"
          fi
        done <<< "$WRAPPED_LINE"
      else
        WRAPPED_LINE=$(word_wrap "Last review: ${PY_LAST_SUMMARY}" "$CONTENT_WIDTH")
        FIRST_WRAP=1
        while IFS= read -r wline; do
          if [ "$FIRST_WRAP" -eq 1 ]; then
            BUF+="${PAD}${RED}⏺ ${wline}${RESET}\n"
            FIRST_WRAP=0
          else
            BUF+="${CONT_PAD}${RED}${wline}${RESET}\n"
          fi
        done <<< "$WRAPPED_LINE"
      fi
    fi
  fi

  printf '\033[2J\033[H'
  echo -ne "$BUF"

  sleep "$INTERVAL"
done
