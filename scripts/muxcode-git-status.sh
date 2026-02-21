#!/bin/bash
# muxcode-git-status.sh - Poll git status every N seconds
# Used in the commit window's left pane during muxcode sessions
#
# Usage: muxcode-git-status.sh [interval_seconds]

set -uo pipefail

INTERVAL="${1:-5}"

# Dracula colors
PURPLE='\033[38;5;141m'
CYAN='\033[38;5;117m'
GREEN='\033[38;5;80m'
PINK='\033[38;5;212m'
DIM='\033[2m'
RESET='\033[0m'

while true; do
  BUF=""
  BUF+="${PURPLE}  git status${RESET}  ${DIM}$(date '+%H:%M:%S')${RESET}  ${DIM}(every ${INTERVAL}s)${RESET}\n"
  BUF+="${DIM}$(printf '%.0s─' {1..50})${RESET}\n"
  BUF+="\n"

  # Branch info
  BRANCH=$(git branch --show-current 2>/dev/null)
  if [ -n "$BRANCH" ]; then
    BUF+="  ${CYAN}branch${RESET}  $BRANCH\n"

    UPSTREAM=$(git rev-parse --abbrev-ref '@{upstream}' 2>/dev/null)
    if [ -n "$UPSTREAM" ]; then
      AHEAD=$(git rev-list --count '@{upstream}..HEAD' 2>/dev/null || echo 0)
      BEHIND=$(git rev-list --count 'HEAD..@{upstream}' 2>/dev/null || echo 0)
      if [ "$AHEAD" -gt 0 ] || [ "$BEHIND" -gt 0 ]; then
        BUF+="  ${CYAN}remote${RESET}  ↑${AHEAD} ↓${BEHIND} (${UPSTREAM})\n"
      fi
    fi
    BUF+="\n"
  fi

  # Staged files
  STAGED=$(git diff --cached --name-status 2>/dev/null)
  if [ -n "$STAGED" ]; then
    BUF+="  ${GREEN}staged${RESET}\n"
    while IFS=$'\t' read -r status file; do
      BUF+="    ${GREEN}${status}${RESET}  ${file}\n"
    done <<< "$STAGED"
    BUF+="\n"
  fi

  # Unstaged changes
  UNSTAGED=$(git diff --name-status 2>/dev/null)
  if [ -n "$UNSTAGED" ]; then
    BUF+="  ${PINK}modified${RESET}\n"
    while IFS=$'\t' read -r status file; do
      BUF+="    ${PINK}${status}${RESET}  ${file}\n"
    done <<< "$UNSTAGED"
    BUF+="\n"
  fi

  # Untracked files
  UNTRACKED=$(git ls-files --others --exclude-standard 2>/dev/null)
  if [ -n "$UNTRACKED" ]; then
    BUF+="  ${DIM}untracked${RESET}\n"
    while IFS= read -r file; do
      BUF+="    ${DIM}?${RESET}  ${file}\n"
    done <<< "$UNTRACKED"
    BUF+="\n"
  fi

  # Clean working tree
  if [ -z "$STAGED" ] && [ -z "$UNSTAGED" ] && [ -z "$UNTRACKED" ]; then
    BUF+="  ${GREEN}clean working tree${RESET}\n"
    BUF+="\n"
  fi

  # Last commit
  LAST=$(git log -1 --format='%h %s' 2>/dev/null)
  if [ -n "$LAST" ]; then
    BUF+="  ${DIM}last commit  ${LAST}${RESET}\n"
  fi

  printf '\033[2J\033[H'
  echo -ne "$BUF"

  sleep "$INTERVAL"
done
