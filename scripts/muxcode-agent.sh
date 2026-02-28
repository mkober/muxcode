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

# --- Load configuration (same resolution order as muxcode.sh) ---
load_config() {
  local config_file=""
  if [ -n "${MUXCODE_CONFIG:-}" ] && [ -f "$MUXCODE_CONFIG" ]; then
    config_file="$MUXCODE_CONFIG"
  elif [ -f "./.muxcode/config" ]; then
    config_file="./.muxcode/config"
  elif [ -f "$HOME/.config/muxcode/config" ]; then
    config_file="$HOME/.config/muxcode/config"
  fi
  [ -n "$config_file" ] && source "$config_file"
}

load_config

AGENT_CLI="${MUXCODE_AGENT_CLI:-claude}"

# Check for per-role local LLM override (e.g. MUXCODE_GIT_CLI=local for commit agent)
# Maps role -> env var name: commit->GIT, build->BUILD, test->TEST, etc.
role_cli_var() {
  case "$1" in
    commit|git) echo "MUXCODE_GIT_CLI" ;;
    build)      echo "MUXCODE_BUILD_CLI" ;;
    test)       echo "MUXCODE_TEST_CLI" ;;
    review)     echo "MUXCODE_REVIEW_CLI" ;;
    deploy)     echo "MUXCODE_DEPLOY_CLI" ;;
    edit)       echo "MUXCODE_EDIT_CLI" ;;
    analyze|analyst) echo "MUXCODE_ANALYZE_CLI" ;;
    *)          echo "MUXCODE_${1^^}_CLI" ;;
  esac
}

ROLE_CLI_VAR="$(role_cli_var "$ROLE")"
ROLE_CLI="${!ROLE_CLI_VAR:-}"

# If per-role CLI is "local", route to the local LLM agent
if [ "$ROLE_CLI" = "local" ]; then
  OLLAMA_URL="${MUXCODE_OLLAMA_URL:-http://localhost:11434}"
  if curl -s --max-time 2 "${OLLAMA_URL}/api/tags" >/dev/null 2>&1; then
    HARNESS_ARGS=(run "$ROLE")
    # Per-role model: MUXCODE_{ROLE}_MODEL (e.g. MUXCODE_GIT_MODEL=llama3.1:8b)
    # Resolution: per-role env → MUXCODE_OLLAMA_MODEL → default (qwen2.5:7b)
    role_model_var() {
      case "$1" in
        commit|git) echo "MUXCODE_GIT_MODEL" ;;
        build)      echo "MUXCODE_BUILD_MODEL" ;;
        test)       echo "MUXCODE_TEST_MODEL" ;;
        review)     echo "MUXCODE_REVIEW_MODEL" ;;
        deploy)     echo "MUXCODE_DEPLOY_MODEL" ;;
        edit)       echo "MUXCODE_EDIT_MODEL" ;;
        analyze|analyst) echo "MUXCODE_ANALYZE_MODEL" ;;
        *)          echo "MUXCODE_${1^^}_MODEL" ;;
      esac
    }
    ROLE_MODEL_VAR="$(role_model_var "$ROLE")"
    ROLE_MODEL="${!ROLE_MODEL_VAR:-}"
    if [ -n "$ROLE_MODEL" ]; then
      HARNESS_ARGS+=(--model "$ROLE_MODEL")
    elif [ -n "${MUXCODE_OLLAMA_MODEL:-}" ]; then
      HARNESS_ARGS+=(--model "$MUXCODE_OLLAMA_MODEL")
    fi
    [ "$OLLAMA_URL" != "http://localhost:11434" ] && HARNESS_ARGS+=(--url "$OLLAMA_URL")
    clear
    # Prefer harness binary; fall back to bus agent subcommand
    if command -v muxcode-llm-harness >/dev/null 2>&1; then
      exec muxcode-llm-harness "${HARNESS_ARGS[@]}"
    else
      exec muxcode-agent-bus agent "${HARNESS_ARGS[@]}"
    fi
  else
    echo "Ollama not running at $OLLAMA_URL, falling back to Claude Code" >&2
  fi
fi

# Map role names to agent filenames (without .md)
agent_name() {
  case "$1" in
    edit)    echo "code-editor" ;;
    build)   echo "code-builder" ;;
    test)    echo "test-runner" ;;
    review)  echo "code-reviewer" ;;
    deploy)  echo "infra-deployer" ;;
    runner|run) echo "command-runner" ;;
    git|commit) echo "git-manager" ;;
    analyst|analyze) echo "editor-analyst" ;;
    docs)    echo "doc-writer" ;;
    research) echo "code-researcher" ;;
    watch)    echo "log-watcher" ;;
    pr-read)  echo "pr-reader" ;;
    api)      echo "api-tester" ;;
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

# Build --append-system-prompt flag from shared prompt template + skills.
# Uses muxcode-agent-bus prompt <role> for coordination and skill prompt <role> for skills.
SHARED_PROMPT_FLAGS=()
build_shared_prompt() {
  local prompt skills context resume combined
  prompt="$(muxcode-agent-bus prompt "$1" 2>/dev/null)" || prompt=""
  skills="$(muxcode-agent-bus skill prompt "$1" 2>/dev/null)" || skills=""
  context="$(muxcode-agent-bus context prompt "$1" 2>/dev/null)" || context=""
  resume="$(muxcode-agent-bus session resume "$1" 2>/dev/null)" || resume=""
  combined="${prompt}${skills:+$'\n'$skills}${context:+$'\n'$context}${resume:+$'\n'$resume}"
  [ -z "$combined" ] && return
  SHARED_PROMPT_FLAGS=(--append-system-prompt "$combined")
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
  runner|run)
    PROMPT="You are the runner agent. Focus on executing commands and processes. Confirm target environment before running. Show command and parse responses. Report errors clearly."
    ;;
  git|commit)
    PROMPT="You are the git agent. Focus on git operations: branches, commits, rebasing, PRs. Run git status, git diff, gh pr commands. Keep the repo clean."
    ;;
  analyst|analyze)
    PROMPT="You are the analyst agent. Evaluate code changes, builds, tests, reviews, deployments, and runs. Explain what happened, why it matters, and what to watch for. Highlight patterns and concepts. Be concise but informative."
    ;;
  docs)
    PROMPT="You are the docs agent. Generate, update, and maintain project documentation. Read code changes, update READMEs, write doc comments, maintain changelogs. Keep docs accurate and in sync with the code."
    ;;
  research)
    PROMPT="You are the research agent. Search the web, read documentation, explore codebases, and answer technical questions. Provide concise findings with sources. Summarize APIs, libraries, and patterns."
    ;;
  watch)
    PROMPT="You are the watch agent. Monitor logs from local files, CloudWatch, Kubernetes, and Docker. Tail logs, detect errors, summarize patterns. Report findings to the edit agent via the bus."
    ;;
  pr-read)
    PROMPT="You are the pr-read agent. Read GitHub PR reviews and CI check failures, then report suggested fixes to the edit agent. Use gh pr view, gh pr checks, gh api to read feedback. Never modify files directly — report suggestions only. The edit agent will prompt the user before making changes."
    ;;
  api)
    PROMPT="You are the API testing agent. Manage API collections and environments using muxcode-agent-bus api subcommands. Execute requests via curl with jq formatting. Support variable substitution from environments. Log requests to history. Report results (status, timing, response) to the edit agent."
    ;;
  *)
    PROMPT="You are a general-purpose coding assistant."
    ;;
esac

exec $AGENT_CLI --append-system-prompt "$PROMPT" "${TOOL_FLAGS[@]}" "${SHARED_PROMPT_FLAGS[@]}"
