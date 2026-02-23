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
  # Common shell commands all agents may need
  local common='Bash(ls*) Bash(cat*) Bash(which*) Bash(command -v*) Bash(pwd*) Bash(wc*) Bash(head*) Bash(tail*)'
  case "$1" in
    build)
      echo "$bus" "$buspath" "$cdbus" $readonly_tools $common \
        'Bash(./build.sh*)' 'Bash(make*)' \
        'Bash(pnpm run build*)' 'Bash(pnpm build*)' 'Bash(npm run build*)' \
        'Bash(npx *)' 'Bash(go build*)' 'Bash(cargo build*)' \
        'Bash(cd * && ./build.sh*)' 'Bash(cd * && make*)' \
        'Bash(cd * && pnpm run build*)' 'Bash(cd * && pnpm build*)' \
        'Bash(cd * && npm run build*)' 'Bash(cd * && npx *)' \
        'Bash(cd * && go build*)' 'Bash(cd * && cargo build*)'
      ;;
    test)
      echo "$bus" "$buspath" "$cdbus" $readonly_tools $common \
        'Bash(./test.sh*)' 'Bash(./scripts/muxcode-test-wrapper.sh*)' \
        'Bash(./scripts/test-and-notify.sh*)' \
        'Bash(go test*)' 'Bash(go vet*)' \
        'Bash(jest*)' 'Bash(npx jest*)' 'Bash(npx vitest*)' \
        'Bash(pnpm test*)' 'Bash(pnpm run test*)' \
        'Bash(npm test*)' 'Bash(npm run test*)' \
        'Bash(pytest*)' 'Bash(python -m pytest*)' 'Bash(cargo test*)' \
        'Bash(cd * && ./test.sh*)' 'Bash(cd * && ./scripts/test-and-notify.sh*)' \
        'Bash(cd * && go test*)' 'Bash(cd * && go vet*)' \
        'Bash(cd * && jest*)' 'Bash(cd * && npx jest*)' 'Bash(cd * && npx vitest*)' \
        'Bash(cd * && pnpm test*)' 'Bash(cd * && pnpm run test*)' \
        'Bash(cd * && npm test*)' 'Bash(cd * && npm run test*)' \
        'Bash(cd * && pytest*)' 'Bash(cd * && python -m pytest*)' \
        'Bash(cd * && cargo test*)'
      ;;
    review)
      echo "$bus" "$buspath" "$cdbus" $readonly_tools $common \
        'Bash(git diff*)' 'Bash(git log*)' 'Bash(git status*)' 'Bash(git show*)' \
        'Bash(git blame*)' 'Bash(git branch*)' \
        'Bash(cd * && git diff*)' 'Bash(cd * && git log*)' \
        'Bash(cd * && git status*)' 'Bash(cd * && git show*)' \
        'Bash(cd * && git blame*)' 'Bash(cd * && git branch*)'
      ;;
    git)
      echo "$bus" "$buspath" "$cdbus" $readonly_tools $common \
        'Bash(git *)' 'Bash(gh *)' \
        'Bash(cd * && git *)' 'Bash(cd * && gh *)'
      ;;
    deploy)
      echo "$bus" "$buspath" "$cdbus" $readonly_tools $common \
        'Bash(cdk *)' 'Bash(npx cdk *)' \
        'Bash(terraform *)' 'Bash(pulumi *)' \
        'Bash(aws *)' 'Bash(sam *)' \
        'Bash(./build.sh*)' 'Bash(make*)' \
        'Bash(cd * && cdk *)' 'Bash(cd * && npx cdk *)' \
        'Bash(cd * && terraform *)' 'Bash(cd * && pulumi *)' \
        'Bash(cd * && aws *)' 'Bash(cd * && sam *)' \
        'Bash(cd * && ./build.sh*)' 'Bash(cd * && make*)'
      ;;
    runner)
      echo "$bus" "$buspath" "$cdbus" $readonly_tools $common \
        'Bash(curl*)' 'Bash(wget*)' \
        'Bash(aws *)' 'Bash(gcloud *)' 'Bash(az *)' \
        'Bash(docker *)' 'Bash(docker-compose *)' \
        'Bash(jq*)' 'Bash(yq*)' \
        'Bash(python*)' 'Bash(node*)' 'Bash(bash*)' \
        'Bash(cd * && curl*)' 'Bash(cd * && wget*)' \
        'Bash(cd * && aws *)' 'Bash(cd * && gcloud *)' 'Bash(cd * && az *)' \
        'Bash(cd * && docker *)' 'Bash(cd * && docker-compose *)' \
        'Bash(cd * && jq*)' 'Bash(cd * && yq*)' \
        'Bash(cd * && python*)' 'Bash(cd * && node*)' 'Bash(cd * && bash*)'
      ;;
    analyst)
      echo "$bus" "$buspath" "$cdbus" $readonly_tools $common \
        'Bash(git diff*)' 'Bash(git log*)' 'Bash(git show*)' \
        'Bash(git blame*)' 'Bash(git status*)' \
        'Bash(cd * && git diff*)' 'Bash(cd * && git log*)' \
        'Bash(cd * && git show*)' 'Bash(cd * && git blame*)' \
        'Bash(cd * && git status*)'
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
