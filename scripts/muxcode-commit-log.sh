#!/bin/bash
# muxcode-commit-log.sh - Combined git status + commit history poller
# Used in the commit window's left pane during muxcode sessions
#
# Shows git status at top (branch, staged, modified, untracked) and
# commit session history below (from commit-history.jsonl).
#
# Usage: muxcode-commit-log.sh [interval_seconds]

set -uo pipefail

INTERVAL="${1:-5}"
SESSION="${BUS_SESSION:-$(tmux display-message -p '#S' 2>/dev/null || echo default)}"
HISTORY_FILE="/tmp/muxcode-bus-${SESSION}/commit-history.jsonl"

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
  BUF+="${PAD}${PURPLE}Commit${RESET}  ${DIM}$(date '+%H:%M:%S')${RESET}  ${DIM}(every ${INTERVAL}s)${RESET}\n"
  BUF+="${PAD}${DIM}$(printf '%.0s─' $(seq 1 "$SEP_WIDTH"))${RESET}\n"
  BUF+="\n"

  # ── Git status section ──────────────────────────────────────

  # Branch info
  BRANCH=$(git branch --show-current 2>/dev/null)
  if [ -n "$BRANCH" ]; then
    BUF+="${PAD}${CYAN}branch${RESET}  $BRANCH\n"

    UPSTREAM=$(git rev-parse --abbrev-ref '@{upstream}' 2>/dev/null)
    if [ -n "$UPSTREAM" ]; then
      AHEAD=$(git rev-list --count '@{upstream}..HEAD' 2>/dev/null || echo 0)
      BEHIND=$(git rev-list --count 'HEAD..@{upstream}' 2>/dev/null || echo 0)
      if [ "$AHEAD" -gt 0 ] || [ "$BEHIND" -gt 0 ]; then
        BUF+="${PAD}${CYAN}remote${RESET}  ↑${AHEAD} ↓${BEHIND} (${UPSTREAM})\n"
      fi
    fi
    BUF+="\n"
  fi

  # Staged files
  STAGED=$(git diff --cached --name-status 2>/dev/null)
  if [ -n "$STAGED" ]; then
    BUF+="${PAD}${GREEN}staged${RESET}\n"
    while IFS=$'\t' read -r status file; do
      BUF+="${CONT_PAD}${GREEN}${status}${RESET}  ${file}\n"
    done <<< "$STAGED"
    BUF+="\n"
  fi

  # Unstaged changes
  UNSTAGED=$(git diff --name-status 2>/dev/null)
  if [ -n "$UNSTAGED" ]; then
    BUF+="${PAD}${PINK}modified${RESET}\n"
    while IFS=$'\t' read -r status file; do
      BUF+="${CONT_PAD}${PINK}${status}${RESET}  ${file}\n"
    done <<< "$UNSTAGED"
    BUF+="\n"
  fi

  # Untracked files
  UNTRACKED=$(git ls-files --others --exclude-standard 2>/dev/null)
  if [ -n "$UNTRACKED" ]; then
    BUF+="${PAD}${DIM}untracked${RESET}\n"
    while IFS= read -r file; do
      BUF+="${CONT_PAD}${DIM}?${RESET}  ${file}\n"
    done <<< "$UNTRACKED"
    BUF+="\n"
  fi

  # Clean working tree
  if [ -z "$STAGED" ] && [ -z "$UNSTAGED" ] && [ -z "$UNTRACKED" ]; then
    BUF+="${PAD}${GREEN}clean working tree${RESET}\n"
    BUF+="\n"
  fi

  # Last commit
  LAST=$(git log -1 --format='%h %s' 2>/dev/null)
  if [ -n "$LAST" ]; then
    BUF+="${PAD}${CYAN}last commit${RESET}  ${LAST}\n"
    LAST_FILES=$(git diff-tree --no-commit-id --name-status -r HEAD 2>/dev/null)
    if [ -n "$LAST_FILES" ]; then
      while IFS=$'\t' read -r status file; do
        case "$status" in
          A*) BUF+="${CONT_PAD}${GREEN}${status}${RESET}  ${file}\n" ;;
          D*) BUF+="${CONT_PAD}${RED}${status}${RESET}  ${file}\n" ;;
          *)  BUF+="${CONT_PAD}${YELLOW}${status}${RESET}  ${file}\n" ;;
        esac
      done <<< "$LAST_FILES"
    fi
    BUF+="\n"
  fi

  # ── Commit history section ──────────────────────────────────

  BUF+="\n"
  BUF+="${PAD}${DIM}$(printf '%.0s─' $(seq 1 "$SEP_WIDTH"))${RESET}\n"
  BUF+="\n"

  if [ ! -f "$HISTORY_FILE" ] || [ ! -s "$HISTORY_FILE" ]; then
    BUF+="${PAD}${DIM}no git operations yet${RESET}\n"
    printf '\033[2J\033[H'
    echo -ne "$BUF"
    sleep "$INTERVAL"
    continue
  fi

  # Parse commit history with jq (python3 fallback)
  if command -v jq &>/dev/null; then
    TOTAL=$(jq -s 'length' "$HISTORY_FILE" 2>/dev/null || echo 0)
    PASS=$(jq -s '[.[] | select(.exit_code == "0" or .exit_code == 0)] | length' "$HISTORY_FILE" 2>/dev/null || echo 0)
    FAIL=$(( TOTAL - PASS ))

    # Summary line
    BUF+="${PAD}${DIM}total${RESET} ${CYAN}${TOTAL}${RESET}  ${DIM}pass${RESET} ${GREEN}${PASS}${RESET}  ${DIM}fail${RESET} ${RED}${FAIL}${RESET}\n"
    BUF+="\n"

    # Recent operations (last 15)
    BUF+="${PAD}${CYAN}recent operations${RESET}\n"
    ENTRY_OFFSET=$(( TOTAL > 15 ? TOTAL - 15 : 0 ))
    ENTRIES=$(jq -s '.[-15:][] | @json' "$HISTORY_FILE" 2>/dev/null)
    OP_NUM=$ENTRY_OFFSET
    if [ -n "$ENTRIES" ]; then
      while IFS= read -r entry; do
        entry="${entry%\"}"
        entry="${entry#\"}"
        OP_NUM=$(( OP_NUM + 1 ))
        # Unescape the JSON string
        raw=$(printf '%s' "$entry" | jq -r '.' 2>/dev/null) || continue
        ts=$(printf '%s' "$raw" | jq -r '.ts // empty' 2>/dev/null)
        cmd=$(printf '%s' "$raw" | jq -r '.command // empty' 2>/dev/null)
        desc=$(printf '%s' "$raw" | jq -r '.description // empty' 2>/dev/null)
        summary=$(printf '%s' "$raw" | jq -r '.summary // empty' 2>/dev/null)
        ec=$(printf '%s' "$raw" | jq -r '.exit_code // empty' 2>/dev/null)

        [ -z "$ts" ] && continue
        TIME=$(format_ts "$ts")

        # Operation number prefix
        NUM_LABEL=$(printf '#%-3s' "$OP_NUM")

        if [ "$ec" = "0" ]; then
          BUF+="${CONT_PAD}${DIM}${NUM_LABEL}${RESET} ${DIM}${TIME}${RESET}  ${GREEN}OK${RESET}    ${cmd}\n"
        else
          BUF+="${CONT_PAD}${DIM}${NUM_LABEL}${RESET} ${DIM}${TIME}${RESET}  ${RED}FAIL${RESET}  ${cmd}  ${DIM}exit ${ec}${RESET}\n"
        fi

        # Show summary (preferred), description, or nothing on second line (word-wrapped)
        DETAIL="${summary:-$desc}"
        if [ -n "$DETAIL" ]; then
          WRAP_FIRST=1
          while IFS= read -r wline; do
            if [ "$WRAP_FIRST" -eq 1 ]; then
              BUF+="${ENTRY_PAD}${DIM}↳ ${wline}${RESET}\n"
              WRAP_FIRST=0
            else
              BUF+="${ENTRY_PAD}${DIM}  ${wline}${RESET}\n"
            fi
          done <<< "$(word_wrap "$DETAIL" "$ENTRY_CONTENT_WIDTH")"
        fi
      done <<< "$ENTRIES"
    fi
    BUF+="\n"

    # Last operation output
    LAST_EC=$(jq -s '.[-1].exit_code // "0"' "$HISTORY_FILE" 2>/dev/null)
    LAST_EC=$(printf '%s' "$LAST_EC" | tr -d '"')
    LAST_OUTPUT=$(jq -s -r '.[-1].output // ""' "$HISTORY_FILE" 2>/dev/null)

    if [ -n "$LAST_OUTPUT" ]; then
      if [ "$LAST_EC" = "0" ]; then
        BUF+="${PAD}${GREEN}⏺ Operation completed:${RESET}\n\n"
      else
        BUF+="${PAD}${RED}⏺ Operation failed:${RESET}\n\n"
      fi
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
          done <<< "$(word_wrap "$oline" "$(( CONTENT_WIDTH - 2 ))")"
        fi
      done <<< "$LAST_OUTPUT"
      BUF+="\n"
    fi

    # Last failure detail (if most recent failed and no output captured)
    if [ "$LAST_EC" != "0" ] && [ -z "$LAST_OUTPUT" ]; then
      LAST_CMD=$(jq -s -r '.[-1].command // ""' "$HISTORY_FILE" 2>/dev/null)
      BUF+="${PAD}${RED}last failure${RESET}\n"
      BUF+="${CONT_PAD}${DIM}cmd${RESET}   ${LAST_CMD}\n"
      BUF+="${CONT_PAD}${DIM}exit${RESET}  ${LAST_EC}\n"
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
    summary = e.get("summary", "")
    desc = e.get("description", "")
    detail = summary if summary else desc
    ec = str(e.get("exit_code", "?"))
    num = offset + i + 1
    status = "OK" if ec == "0" else "FAIL"
    print(f"ENTRY={ts}\t{status}\t{cmd}\t{ec}\t{num}\t{detail}")
last = entries[-1] if entries else {}
last_ec = str(last.get("exit_code", "0"))
last_output = last.get("output", "")
if last_output:
    import re
    print(f"LAST_EC={last_ec}")
    for ol in last_output.strip().split("\n"):
        ol = re.sub(r"\x1b\[[0-9;]*[A-Za-z]", "", ol).strip()
        if ol:
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

    BUF+="${PAD}${DIM}total${RESET} ${CYAN}${TOTAL}${RESET}  ${DIM}pass${RESET} ${GREEN}${PASS}${RESET}  ${DIM}fail${RESET} ${RED}${FAIL}${RESET}\n"
    BUF+="\n"

    BUF+="${PAD}${CYAN}recent operations${RESET}\n"
    while IFS= read -r line; do
      case "$line" in
        ENTRY=*)
          line="${line#ENTRY=}"
          IFS=$'\t' read -r ts status cmd ec num detail <<< "$line"
          TIME=$(format_ts "$ts")
          NUM_LABEL=$(printf '#%-3s' "$num")
          if [ "$status" = "OK" ]; then
            BUF+="${CONT_PAD}${DIM}${NUM_LABEL}${RESET} ${DIM}${TIME}${RESET}  ${GREEN}OK${RESET}    ${cmd}\n"
          else
            BUF+="${CONT_PAD}${DIM}${NUM_LABEL}${RESET} ${DIM}${TIME}${RESET}  ${RED}FAIL${RESET}  ${cmd}  ${DIM}exit ${ec}${RESET}\n"
          fi
          if [ -n "$detail" ]; then
            WRAP_FIRST=1
            while IFS= read -r wline; do
              if [ "$WRAP_FIRST" -eq 1 ]; then
                BUF+="${ENTRY_PAD}${DIM}↳ ${wline}${RESET}\n"
                WRAP_FIRST=0
              else
                BUF+="${ENTRY_PAD}${DIM}  ${wline}${RESET}\n"
              fi
            done <<< "$(word_wrap "$detail" "$ENTRY_CONTENT_WIDTH")"
          fi
          ;;
      esac
    done <<< "$PARSED"
    BUF+="\n"

    # Last operation output
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
              BUF+="${PAD}${GREEN}⏺ Operation completed:${RESET}\n\n"
            else
              BUF+="${PAD}${RED}⏺ Operation failed:${RESET}\n\n"
            fi
          fi
          OL="${line#LAST_OUTPUT_LINE=}"
          if [ "$PY_FIRST_LINE" -eq 1 ]; then
            while IFS= read -r wline; do
              BUF+="${PAD}${CYAN}${wline}${RESET}\n"
            done <<< "$(word_wrap "$OL" "$CONTENT_WIDTH")"
            PY_FIRST_LINE=0
          else
            while IFS= read -r wline; do
              BUF+="${CONT_PAD}${DIM}- ${wline}${RESET}\n"
            done <<< "$(word_wrap "$OL" "$(( CONTENT_WIDTH - 2 ))")"
          fi
          ;;
      esac
    done <<< "$PARSED"
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
      BUF+="${PAD}${RED}last failure${RESET}\n"
      BUF+="${CONT_PAD}${DIM}cmd${RESET}   ${LASTFAIL_CMD}\n"
      BUF+="${CONT_PAD}${DIM}exit${RESET}  ${LASTFAIL_EC}\n"
    fi
  fi

  printf '\033[2J\033[H'
  echo -ne "$BUF"

  sleep "$INTERVAL"
done
