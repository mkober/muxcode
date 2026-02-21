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
  esac
}

# Allowed Bash tools per role (scoped permissions for autonomous operation)
allowed_tools() {
  local bus='Bash(muxcode-agent-bus *)' buspath='Bash(./bin/muxcode-agent-bus *)'
  # cd-prefixed variants (Claude Code agents often prefix commands with cd)
  local cdbus='Bash(cd * && muxcode-agent-bus *)'
  # Read-only tools all agents need for context and memory access
  local readonly_tools='Read Glob Grep'
  case "$1" in
    build)
      echo "$bus" "$buspath" "$cdbus" $readonly_tools \
        'Bash(./build.sh*)' 'Bash(make*)' \
        'Bash(pnpm run build*)' 'Bash(go build*)' 'Bash(cargo build*)' \
        'Bash(cd * && ./build.sh*)' 'Bash(cd * && make*)' \
        'Bash(cd * && pnpm run build*)' 'Bash(cd * && go build*)' 'Bash(cd * && cargo build*)'
      ;;
    test)
      echo "$bus" "$buspath" "$cdbus" $readonly_tools \
        'Bash(./test.sh*)' 'Bash(./scripts/muxcode-test-wrapper.sh*)' \
        'Bash(go test*)' 'Bash(go vet*)' \
        'Bash(jest*)' 'Bash(npx jest*)' 'Bash(pnpm test*)' 'Bash(pnpm run test*)' \
        'Bash(pytest*)' 'Bash(cargo test*)' \
        'Bash(cd * && go test*)' 'Bash(cd * && go vet*)' \
        'Bash(cd * && jest*)' 'Bash(cd * && npx jest*)' 'Bash(cd * && pnpm test*)' \
        'Bash(cd * && pnpm run test*)' 'Bash(cd * && pytest*)' 'Bash(cd * && cargo test*)'
      ;;
    review)
      echo "$bus" "$buspath" "$cdbus" $readonly_tools \
        'Bash(git diff*)' 'Bash(git log*)' 'Bash(git status*)' 'Bash(git show*)' \
        'Bash(cd * && git diff*)' 'Bash(cd * && git log*)' \
        'Bash(cd * && git status*)' 'Bash(cd * && git show*)'
      ;;
    git)
      echo "$bus" "$buspath" "$cdbus" $readonly_tools \
        'Bash(git *)' 'Bash(gh *)' \
        'Bash(cd * && git *)' 'Bash(cd * && gh *)'
      ;;
    analyst)
      echo "$bus" "$buspath" "$cdbus" $readonly_tools
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

# Sub-agents run autonomously — skip interactive permission prompts.
# The edit agent keeps normal permissions since it's user-facing.
if [ "$ROLE" != "edit" ]; then
  FLAGS="--dangerously-skip-permissions $FLAGS"
fi

# Auto-accept the bypass-permissions confirmation prompt.
# Claude Code shows an interactive select ("Yes, I accept") when launched
# with --dangerously-skip-permissions. This polls the tmux pane for the
# prompt text and sends the keystrokes to accept it automatically.
auto_accept_bypass() {
  local pane="${TMUX_PANE:-}"
  [ -z "$pane" ] && return
  local max_wait="${MUXCODE_ACCEPT_TIMEOUT:-30}"
  # Poll rapidly (0.2s) — the prompt can disappear within a second if a
  # stale keystroke reaches it before we do.
  local intervals=$(( max_wait * 5 ))
  for ((i=0; i<intervals; i++)); do
    local content
    content="$(tmux capture-pane -t "$pane" -p 2>/dev/null)"
    if echo "$content" | grep -q "Yes, I accept"; then
      # Already exited? Don't send keys to a bare shell.
      echo "$content" | grep -q "Resume this session" && return 1
      sleep 0.3
      # Send Down and Enter as SEPARATE calls so the TUI has time to
      # process the cursor move before receiving the confirmation.
      tmux send-keys -t "$pane" Down
      sleep 0.3
      tmux send-keys -t "$pane" Enter
      return 0
    fi
    sleep 0.2
  done
}

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
  # shellcheck disable=SC2086
  exec $AGENT_CLI --agent "$name" --agents "$agents_json" $@
}

# Clear terminal so Claude Code starts with a clean screen
clear

# Drain any stale keystrokes from stdin (e.g. the Enter that launched this
# script via tmux send-keys) so they don't accidentally confirm the bypass
# permissions prompt before the auto-accept watcher can select "Yes".
read -r -t 0.5 -n 10000 _ 2>/dev/null || true

# Start the auto-accept watcher in the background for non-edit agents.
# The background process polls the pane and accepts the bypass prompt
# once it appears. It must be started before exec replaces this shell.
if [ "$ROLE" != "edit" ]; then
  auto_accept_bypass &
fi

# Search for agent file in priority order
if [ -n "$AGENT" ]; then
  SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
  INSTALL_DIR="${SCRIPT_DIR%/scripts}"

  if [ -f ".claude/agents/${AGENT}.md" ]; then
    # shellcheck disable=SC2086
    exec $AGENT_CLI --agent "$AGENT" $FLAGS
  elif [ -f "$HOME/.config/muxcode/agents/${AGENT}.md" ]; then
    launch_agent_from_file "$AGENT" "$HOME/.config/muxcode/agents/${AGENT}.md" $FLAGS
  elif [ -f "$INSTALL_DIR/agents/${AGENT}.md" ]; then
    launch_agent_from_file "$AGENT" "$INSTALL_DIR/agents/${AGENT}.md" $FLAGS
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
