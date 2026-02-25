#!/bin/bash
# muxcode-edit-guard.sh — PreToolUse hook for Bash (edit window only)
#
# Blocks prohibited commands in the edit window and tells the agent
# to delegate via the message bus instead. This provides deterministic
# enforcement — the agent cannot run gh, git-write, build, test, or
# deploy commands even if the prompt instructions are ignored.
#
# Runs synchronously (not async) so it can return a block decision
# before the command is executed.

SESSION=$(tmux display-message -p '#S' 2>/dev/null) || exit 0
WINDOW_NAME=$(tmux display-message -t "${TMUX_PANE:-}" -p '#W' 2>/dev/null) || exit 0
[ "$WINDOW_NAME" = "edit" ] || exit 0

EVENT_JSON=$(cat)
COMMAND=$(echo "$EVENT_JSON" | jq -r '.tool_input.command // empty' 2>/dev/null)
[ -z "$COMMAND" ] && exit 0

# Strip leading whitespace and cd prefixes for matching
CMD=$(echo "$COMMAND" | sed 's/^[[:space:]]*//' | sed 's/^cd [^&]*&& *//')

block() {
  jq -n --arg r "$1" '{"decision":"block","reason":$r}'
  exit 0
}

case "$CMD" in
  # PR reading → commit agent (git-manager handles pr-read action)
  gh\ pr\ view*|gh\ pr\ checks*|gh\ pr\ diff*|gh\ pr\ status*|gh\ pr\ list*|gh\ api\ repos/*/pulls*)
    block "BLOCKED: PR read commands are prohibited in the edit window. Delegate to the commit agent. Run this command: muxcode-agent-bus send commit pr-read \"Read the PR on the current branch and report review feedback, CI failures, and suggested fixes\""
    ;;
  # PR/release mutations → commit agent
  gh\ pr\ create*|gh\ pr\ merge*|gh\ pr\ close*|gh\ pr\ reopen*|gh\ pr\ edit*|gh\ release*)
    block "BLOCKED: PR/release mutations are prohibited in the edit window. Delegate to the commit agent. Run: muxcode-agent-bus send commit commit \"<describe the PR/release operation>\""
    ;;
  # Other gh commands → commit agent
  gh\ *)
    block "BLOCKED: gh commands are prohibited in the edit window. Delegate to the commit agent. Run: muxcode-agent-bus send commit commit \"<describe the operation>\""
    ;;
  # All git commands → commit agent (edit window runs no git at all)
  git\ *)
    block "BLOCKED: All git commands are prohibited in the edit window. Delegate to the commit agent. Run: muxcode-agent-bus send commit commit \"<describe the git operation>\""
    ;;
  # Build commands → build agent
  ./build.sh*|pnpm\ build*|pnpm\ run\ build*|npm\ run\ build*|go\ build*|cargo\ build*|tsc\ *)
    block "BLOCKED: Build commands are prohibited in the edit window. Delegate to the build agent. Run: muxcode-agent-bus send build build \"Run ./build.sh and report results\""
    ;;
  make\ *|make)
    block "BLOCKED: Build commands are prohibited in the edit window. Delegate to the build agent. Run: muxcode-agent-bus send build build \"Run ./build.sh and report results\""
    ;;
  # Test commands → test agent
  ./test.sh*|pnpm\ test*|pnpm\ run\ test*|npm\ test*|npm\ run\ test*|jest*|npx\ jest*|npx\ vitest*|pytest*|python\ -m\ pytest*|go\ test*|cargo\ test*)
    block "BLOCKED: Test commands are prohibited in the edit window. Delegate to the test agent. Run: muxcode-agent-bus send test test \"Run tests and report results\""
    ;;
  # CDK/deploy commands → deploy agent
  cdk\ *|npx\ cdk\ *|envName=*cdk\ *|envName=*npx\ cdk\ *|terraform\ *|pulumi\ *|sam\ *)
    block "BLOCKED: Deploy commands are prohibited in the edit window. Delegate to the deploy agent. Run: muxcode-agent-bus send deploy deploy \"<describe the deploy operation>\""
    ;;
  # Log tailing commands → watch agent
  aws\ logs*|tail\ -f*|tail\ -F*|kubectl\ logs*|docker\ logs*|docker-compose\ logs*|stern\ *)
    block "BLOCKED: Log tailing commands are prohibited in the edit window. Delegate to the watch agent. Run: muxcode-agent-bus send watch watch \"<describe what logs to tail>\""
    ;;
esac

# All checks passed — allow the command
exit 0
