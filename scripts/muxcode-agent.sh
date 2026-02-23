#!/bin/bash
# muxcode-agent.sh - Launch AI agent with a role-specific agent definition
# Usage: muxcode-agent.sh <role>
#
# Agent file search order:
#   1. .claude/agents/<name>.md  (project-local)
#   2. ~/.config/muxcode/agents/<name>.md  (user config)
#   3. <install-dir>/agents/<name>.md  (muxcode defaults)
# Falls back to inline system prompts if no agent file found.

ROLE="${1:-general}"
AGENT_CLI="${MUXCODE_AGENT_CLI:-claude}"

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
    docs)    echo "doc-writer" ;;
    research) echo "code-researcher" ;;
  esac
}

# Build --allowedTools flags from config-driven tool profiles.
# Uses muxcode-agent-bus tools <role> to resolve the tool list.
# Populates the TOOL_FLAGS array (patterns may contain spaces).
TOOL_FLAGS=()
build_flags() {
  local tools
  tools="$(muxcode-agent-bus tools "$1" 2>/dev/null)" || return
  [ -z "$tools" ] && return
  while IFS= read -r tool; do
    [ -z "$tool" ] && continue
    TOOL_FLAGS+=(--allowedTools "$tool")
  done <<< "$tools"
}

AGENT="$(agent_name "$ROLE")"
build_flags "$ROLE"

# Build --append-system-prompt flag from shared prompt template.
# Uses muxcode-agent-bus prompt <role> to generate the common coordination section.
SHARED_PROMPT_FLAGS=()
build_shared_prompt() {
  local prompt
  prompt="$(muxcode-agent-bus prompt "$1" 2>/dev/null)" || return
  [ -z "$prompt" ] && return
  SHARED_PROMPT_FLAGS=(--append-system-prompt "$prompt")
}
build_shared_prompt "$ROLE"

# Launch agent from a .md file outside the project by reading its content
# and passing it via --agents JSON + --agent <name>.
launch_agent_from_file() {
  local name="$1" file="$2"
  shift 2
  local prompt desc
  # Strip YAML frontmatter, extract prompt body
  prompt="$(awk '/^---$/{c++; next} c>=2' "$file")"
  # Extract description from frontmatter (if present)
  desc="$(awk '/^---$/{c++; next} c==1 && /^description:/{sub(/^description: */, ""); print}' "$file")"
  : "${desc:=$name}"
  local agents_json
  agents_json="$(jq -n --arg n "$name" --arg d "$desc" --arg p "$prompt" \
    '{($n): {description: $d, prompt: $p}}')"
  exec $AGENT_CLI --agent "$name" --agents "$agents_json" "$@"
}

# Clear terminal so Claude Code starts with a clean screen
clear

# Search for agent file in priority order
if [ -n "$AGENT" ]; then
  SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
  INSTALL_DIR="${SCRIPT_DIR%/scripts}"

  if [ -f ".claude/agents/${AGENT}.md" ]; then
    exec $AGENT_CLI --agent "$AGENT" "${TOOL_FLAGS[@]}" "${SHARED_PROMPT_FLAGS[@]}"
  elif [ -f "$HOME/.config/muxcode/agents/${AGENT}.md" ]; then
    launch_agent_from_file "$AGENT" "$HOME/.config/muxcode/agents/${AGENT}.md" "${TOOL_FLAGS[@]}" "${SHARED_PROMPT_FLAGS[@]}"
  elif [ -f "$INSTALL_DIR/agents/${AGENT}.md" ]; then
    launch_agent_from_file "$AGENT" "$INSTALL_DIR/agents/${AGENT}.md" "${TOOL_FLAGS[@]}" "${SHARED_PROMPT_FLAGS[@]}"
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
  docs)
    PROMPT="You are the docs agent. Generate, update, and maintain project documentation. Read code changes, update READMEs, write doc comments, maintain changelogs. Keep docs accurate and in sync with the code."
    ;;
  research)
    PROMPT="You are the research agent. Search the web, read documentation, explore codebases, and answer technical questions. Provide concise findings with sources. Summarize APIs, libraries, and patterns."
    ;;
  *)
    PROMPT="You are a general-purpose coding assistant."
    ;;
esac

exec $AGENT_CLI --append-system-prompt "$PROMPT" "${TOOL_FLAGS[@]}" "${SHARED_PROMPT_FLAGS[@]}"
