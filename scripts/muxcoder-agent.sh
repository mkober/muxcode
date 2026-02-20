#!/bin/bash
# muxcoder-agent.sh - Launch AI agent with a role-specific agent definition
# Usage: muxcoder-agent.sh <role>
#
# Agent file search order:
#   1. .claude/agents/<name>.md  (project-local)
#   2. ~/.config/muxcoder/agents/<name>.md  (user config)
#   3. <install-dir>/agents/<name>.md  (muxcoder defaults)
# Falls back to inline system prompts if no agent file found.

ROLE="${1:-general}"
AGENT_CLI="${MUXCODER_AGENT_CLI:-claude}"

# Map role names to agent filenames (without .md)
agent_name() {
  case "$1" in
    edit)    echo "code-editor" ;;
    build)   echo "code-builder" ;;
    test)    echo "test-runner" ;;
    review)  echo "code-reviewer" ;;
    deploy)  echo "infra-deployer" ;;
    runner)  echo "command-runner" ;;
    git)     echo "git-manager" ;;
    analyst) echo "editor-analyst" ;;
  esac
}

# Allowed Bash tools per role (scoped permissions for autonomous operation)
allowed_tools() {
  local bus='Bash(muxcoder-agent-bus *)' buspath='Bash(./bin/muxcoder-agent-bus *)'
  case "$1" in
    build)
      echo "$bus" "$buspath" \
        'Bash(./build.sh*)' 'Bash(make*)' \
        'Bash(pnpm run build*)' 'Bash(go build*)' 'Bash(cargo build*)'
      ;;
    test)
      echo "$bus" "$buspath" \
        'Bash(./test.sh*)' 'Bash(./scripts/muxcoder-test-wrapper.sh*)' \
        'Bash(go test*)' 'Bash(go vet*)' \
        'Bash(jest*)' 'Bash(npx jest*)' 'Bash(pnpm test*)' 'Bash(pnpm run test*)' \
        'Bash(pytest*)' 'Bash(cargo test*)'
      ;;
    review)
      echo "$bus" "$buspath" \
        'Bash(git diff*)' 'Bash(git log*)' 'Bash(git status*)' 'Bash(git show*)'
      ;;
    analyst)
      echo "$bus" "$buspath"
      ;;
  esac
}

# Build --allowedTools flags from the role's tool list
build_flags() {
  local tools
  tools="$(allowed_tools "$1")"
  [ -z "$tools" ] && return
  for tool in $tools; do
    printf -- '--allowedTools %s ' "$tool"
  done
}

AGENT="$(agent_name "$ROLE")"
FLAGS="$(build_flags "$ROLE")"

# Search for agent file in priority order
if [ -n "$AGENT" ]; then
  SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
  INSTALL_DIR="${SCRIPT_DIR%/scripts}"

  if [ -f ".claude/agents/${AGENT}.md" ]; then
    # shellcheck disable=SC2086
    exec $AGENT_CLI --agent "$AGENT" $FLAGS
  elif [ -f "$HOME/.config/muxcoder/agents/${AGENT}.md" ]; then
    # shellcheck disable=SC2086
    exec $AGENT_CLI --agent-file "$HOME/.config/muxcoder/agents/${AGENT}.md" $FLAGS
  elif [ -f "$INSTALL_DIR/agents/${AGENT}.md" ]; then
    # shellcheck disable=SC2086
    exec $AGENT_CLI --agent-file "$INSTALL_DIR/agents/${AGENT}.md" $FLAGS
  fi
fi

# Fallback: inline system prompts for projects without agent files
case "$ROLE" in
  edit)
    PROMPT="You are the edit agent. Focus on writing and modifying code. Make precise, minimal changes that follow existing patterns. One concern at a time."
    ;;
  build)
    PROMPT="You are the build agent. Focus on building, compiling, and packaging. Run the project's build command. Diagnose and fix build failures."
    ;;
  test)
    PROMPT="You are the test agent. Focus on writing, running, and debugging tests. Run the project's test command. Analyze failures and suggest fixes."
    ;;
  review)
    PROMPT="You are the review agent. Focus on reviewing code for correctness, security, and quality. Run git diff and provide feedback organized by severity."
    ;;
  deploy)
    PROMPT="You are the deploy agent. Focus on infrastructure as code and deployments. Write, review, and debug infrastructure definitions. Run deployment diffs. Check security and compliance."
    ;;
  runner)
    PROMPT="You are the runner agent. Focus on executing commands and processes. Confirm target environment before running. Show command and parse responses. Report errors clearly."
    ;;
  git)
    PROMPT="You are the git agent. Focus on git operations: branches, commits, rebasing, PRs. Run git status, git diff, gh pr commands. Keep the repo clean."
    ;;
  analyst)
    PROMPT="You are the analyst agent. Evaluate code changes, builds, tests, reviews, deployments, and runs. Explain what happened, why it matters, and what to watch for. Highlight patterns and concepts. Be concise but informative."
    ;;
  *)
    PROMPT="You are a general-purpose coding assistant."
    ;;
esac

# shellcheck disable=SC2086
exec $AGENT_CLI --append-system-prompt "$PROMPT" $FLAGS
