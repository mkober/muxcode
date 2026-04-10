#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "$0")" && pwd)"

# --- Colors ---
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
CYAN='\033[0;36m'
NC='\033[0m'

info()  { echo -e "${CYAN}→${NC} $*"; }
ok()    { echo -e "${GREEN}✓${NC} $*"; }
warn()  { echo -e "${YELLOW}!${NC} $*"; }
fail()  { echo -e "${RED}✗${NC} $*"; exit 1; }

echo ""
echo -e "${GREEN}muxcode${NC} — multi-agent coding environment"
echo ""

# --- Check core prerequisites ---
info "Checking core prerequisites..."

missing=()
command -v tmux   >/dev/null 2>&1 || missing+=("tmux (>= 3.0)")
command -v go     >/dev/null 2>&1 || missing+=("go (>= 1.22)")
command -v jq     >/dev/null 2>&1 || missing+=("jq")
command -v nvim   >/dev/null 2>&1 || missing+=("nvim")
command -v fzf    >/dev/null 2>&1 || missing+=("fzf")

if [ ${#missing[@]} -gt 0 ]; then
  warn "Missing required tools:"
  for m in "${missing[@]}"; do
    echo "    - $m"
  done
  echo ""
  read -rp "Continue anyway? [y/N] " ans
  [[ "$ans" =~ ^[Yy]$ ]] || exit 1
else
  ok "All core tools found"
fi

# --- AI CLI provider selection ---
echo ""
info "Which AI CLI providers do you want to use?"
echo ""
echo "  MuxCode supports multiple AI CLI providers. Each agent window can use"
echo "  a different provider. Select all providers you want available."
echo ""

# Detect what's already installed
use_claude=false
use_opencode=false

# Claude Code
if command -v claude >/dev/null 2>&1; then
  claude_version=$(claude --version 2>/dev/null || echo "version unknown")
  ok "Claude Code found ($claude_version)"
  use_claude=true
else
  read -rp "  Install Claude Code? (recommended) [Y/n] " ans
  if [[ ! "$ans" =~ ^[Nn]$ ]]; then
    info "Installing Claude Code..."
    if npm install -g @anthropic-ai/claude-code 2>/dev/null; then
      ok "Claude Code installed"
      use_claude=true
    elif brew install claude-code 2>/dev/null; then
      ok "Claude Code installed via Homebrew"
      use_claude=true
    else
      warn "Auto-install failed. Install manually:"
      echo "    npm install -g @anthropic-ai/claude-code"
      echo "    — or —"
      echo "    brew install claude-code"
      echo ""
      read -rp "  Continue without Claude Code? [y/N] " skip_ans
      if [[ "$skip_ans" =~ ^[Yy]$ ]]; then
        use_claude=false
      else
        fail "Install Claude Code and re-run"
      fi
    fi
  fi
fi

# OpenCode — add known install location to PATH if not already there
if [ -d "$HOME/.opencode/bin" ] && [[ ":$PATH:" != *":$HOME/.opencode/bin:"* ]]; then
  export PATH="$HOME/.opencode/bin:$PATH"
fi
if command -v opencode >/dev/null 2>&1; then
  oc_version=$(opencode --version 2>/dev/null || echo "version unknown")
  ok "OpenCode found ($oc_version)"
  # Version check — require >= 1.4.0
  oc_semver=$(echo "$oc_version" | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' | head -1)
  if [ -n "$oc_semver" ]; then
    oc_minor=$(echo "$oc_semver" | cut -d. -f2)
    if [ "${oc_minor:-0}" -lt 4 ]; then
      warn "OpenCode $oc_semver found but >= 1.4.0 required (server API support)"
      warn "Update with: curl -fsSL https://opencode.ai/install | bash"
    fi
  fi
  use_opencode=true
else
  # Only prompt to install OpenCode if Claude Code is not available —
  # avoids re-asking on every install.sh run when user already has a provider.
  if $use_claude; then
    info "OpenCode not found (optional — Claude Code is available)"
    ans="n"
  else
    read -rp "  Install OpenCode? (no AI CLI provider found yet) [y/N] " ans
  fi
  if [[ "$ans" =~ ^[Yy]$ ]]; then
    info "Installing OpenCode..."
    if curl -fsSL https://opencode.ai/install | bash 2>/dev/null; then
      ok "OpenCode installed"
      use_opencode=true
    elif brew install opencode 2>/dev/null; then
      ok "OpenCode installed via Homebrew"
      use_opencode=true
    else
      warn "Auto-install failed. Install manually:"
      echo "    curl -fsSL https://opencode.ai/install | bash"
      echo "    — or —"
      echo "    brew install opencode"
      echo ""
      read -rp "  Continue without OpenCode? [y/N] " skip_ans
      [[ "$skip_ans" =~ ^[Yy]$ ]] && use_opencode=false || fail "Install OpenCode and re-run"
    fi
  fi
fi

# Validate: at least one provider selected
if ! $use_claude && ! $use_opencode; then
  fail "At least one AI CLI provider is required. Re-run and select Claude Code or OpenCode."
fi

# Default provider selection (when both available)
default_cli="claude"
if $use_claude && $use_opencode; then
  echo ""
  info "Both Claude Code and OpenCode are available."
  echo "  Which should be the default for new agent windows?"
  echo ""
  echo "    1) Claude Code (recommended — full hook support)"
  echo "    2) OpenCode (server mode — multi-provider LLM support)"
  echo ""
  read -rp "  Default provider [1]: " default_choice
  if [[ "$default_choice" == "2" ]]; then
    default_cli="opencode"
    info "Default: OpenCode (override per-role with MUXCODE_{ROLE}_CLI=claude)"
  else
    default_cli="claude"
    info "Default: Claude Code (override per-role with MUXCODE_{ROLE}_CLI=opencode)"
  fi
elif $use_opencode; then
  default_cli="opencode"
fi

echo ""
ok "Provider selection complete"

# --- Check optional: Ollama (for local LLM agent) ---
if command -v ollama >/dev/null 2>&1; then
  ok "ollama found (local LLM agent available)"
  if ollama list >/dev/null 2>&1; then
    OLLAMA_MODEL="${MUXCODE_OLLAMA_MODEL:-qwen2.5:7b}"
    if ollama list | grep -q "${OLLAMA_MODEL%%:*}"; then
      ok "Model $OLLAMA_MODEL available"
    else
      info "Pulling default model $OLLAMA_MODEL for local LLM agent..."
      if ollama pull "$OLLAMA_MODEL"; then
        ok "Model $OLLAMA_MODEL pulled"
      else
        warn "Failed to pull $OLLAMA_MODEL — local agent will auto-pull on first run"
      fi
    fi
  else
    warn "Ollama not running — start with: ollama serve"
    warn "Model will be auto-pulled on first local agent run"
  fi
else
  info "ollama not found (optional — install for local LLM agent: brew install ollama)"
fi

# --- Ensure ~/.local/bin exists and is in PATH ---
info "Checking ~/.local/bin..."
mkdir -p "$HOME/.local/bin"
if [[ ":$PATH:" != *":$HOME/.local/bin:"* ]]; then
  warn "~/.local/bin is not in your PATH"
  echo "    Add to your shell profile:"
  echo "    export PATH=\"\$HOME/.local/bin:\$PATH\""
fi
ok "~/.local/bin ready"

# --- Build and install ---
info "Building and installing..."
"$REPO_DIR/build.sh"
ok "Binary, scripts, agents, and configs installed"

# --- Configure tmux ---
TMUX_CONF="$HOME/.tmux.conf"
TMUX_SOURCE_LINE="source-file ~/.config/muxcode/tmux.conf"

info "Configuring tmux..."
if [ -f "$TMUX_CONF" ]; then
  if grep -qF "muxcode/tmux.conf" "$TMUX_CONF"; then
    ok "tmux already configured"
  else
    tmpfile=$(mktemp)
    if grep -q "tpm/tpm" "$TMUX_CONF"; then
      # Insert before TPM plugin init
      awk -v line="$TMUX_SOURCE_LINE" '/tpm\/tpm/ { if (done == 0) { print "# Muxcode: multi-agent coding environment"; print line; print ""; done=1 } } { print }' "$TMUX_CONF" > "$tmpfile"
    else
      cp "$TMUX_CONF" "$tmpfile"
      printf '\n# Muxcode: multi-agent coding environment\n%s\n' "$TMUX_SOURCE_LINE" >> "$tmpfile"
    fi
    mv "$tmpfile" "$TMUX_CONF"
    ok "Added muxcode source to ~/.tmux.conf"
  fi
else
  warn "No ~/.tmux.conf found — add manually: $TMUX_SOURCE_LINE"
fi

# --- Install Neovim configuration (NVIM_APPNAME=muxcode) ---
NVIM_CONFIGDIR="$HOME/.config/muxcode/nvim"

info "MuxCode manages its own Neovim config via NVIM_APPNAME=muxcode"
info "Config location: ~/.config/muxcode/nvim/"
info "Your personal ~/.config/nvim/ will NOT be modified"
echo ""
if [ -f "$NVIM_CONFIGDIR/init.lua" ]; then
  warn "Existing muxcode nvim config will be overwritten:"
  warn "  $NVIM_CONFIGDIR/init.lua"
  warn "  $NVIM_CONFIGDIR/plugin/startscreen.lua"
  warn "User extensions in lua/user/ and after/ will be preserved."
  echo ""
  read -rp "Overwrite muxcode nvim config? [Y/n] " nvim_ans
  if [[ "$nvim_ans" =~ ^[Nn]$ ]]; then
    warn "Skipping nvim config install"
  else
    mkdir -p "$NVIM_CONFIGDIR/plugin"
    cp "$REPO_DIR/config/nvim/init.lua" "$NVIM_CONFIGDIR/init.lua"
    cp "$REPO_DIR/config/nvim/plugin/startscreen.lua" "$NVIM_CONFIGDIR/plugin/startscreen.lua"
    ok "Neovim config installed to ~/.config/muxcode/nvim/"
  fi
else
  mkdir -p "$NVIM_CONFIGDIR/plugin"
  cp "$REPO_DIR/config/nvim/init.lua" "$NVIM_CONFIGDIR/init.lua"
  cp "$REPO_DIR/config/nvim/plugin/startscreen.lua" "$NVIM_CONFIGDIR/plugin/startscreen.lua"
  ok "Neovim config installed to ~/.config/muxcode/nvim/"
fi

# Clean up old site plugin from pre-NVIM_APPNAME installs
OLD_SITE_PLUGIN="$HOME/.local/share/nvim/site/plugin/muxcode-startscreen.lua"
if [ -f "$OLD_SITE_PLUGIN" ]; then
  rm -f "$OLD_SITE_PLUGIN"
  ok "Removed old start screen from site/plugin (migrated to NVIM_APPNAME config)"
fi

# --- Configure Claude Code hooks (if selected) ---
if $use_claude; then
  CLAUDE_SETTINGS="$HOME/.claude/settings.json"
  MUXCODE_SETTINGS="$HOME/.config/muxcode/settings.json"

  info "Configuring Claude Code hooks..."
  if [ ! -f "$MUXCODE_SETTINGS" ]; then
    warn "Muxcode settings not found at $MUXCODE_SETTINGS"
  elif [ ! -f "$CLAUDE_SETTINGS" ]; then
    mkdir -p "$HOME/.claude"
    cp "$MUXCODE_SETTINGS" "$CLAUDE_SETTINGS"
    ok "Created ~/.claude/settings.json with muxcode hooks"
  else
    # Always merge — add_hook is idempotent (skips existing commands)
    jq --slurpfile mc "$MUXCODE_SETTINGS" '
      def add_hook($phase; $matcher; $hook):
        if (.hooks[$phase] // [] | map(select(.matcher == $matcher)) | length) > 0 then
          .hooks[$phase] |= map(
            if .matcher == $matcher and (.hooks | map(.command) | index($hook.command) | not) then
              .hooks += [$hook]
            else . end
          )
        else
          .hooks[$phase] = ((.hooks[$phase] // []) + [{"matcher": $matcher, "hooks": [$hook]}])
        end;

      .hooks = (.hooks // {}) |
      .permissions = (.permissions // {}) |
      .permissions.allow = (.permissions.allow // []) |

      reduce ($mc[0].hooks.PreToolUse // [] | .[] | . as $entry | $entry.hooks[] | {m: $entry.matcher, h: .}) as $x (
        .; add_hook("PreToolUse"; $x.m; $x.h)
      ) |
      reduce ($mc[0].hooks.PostToolUse // [] | .[] | . as $entry | $entry.hooks[] | {m: $entry.matcher, h: .}) as $x (
        .; add_hook("PostToolUse"; $x.m; $x.h)
      ) |
      .permissions.allow = (.permissions.allow + ($mc[0].permissions.allow // []) | unique) |
      .permissions.deny = ((.permissions.deny // []) + ($mc[0].permissions.deny // []) | unique)
    ' "$CLAUDE_SETTINGS" > "${CLAUDE_SETTINGS}.tmp"

    if ! diff -q "${CLAUDE_SETTINGS}.tmp" "$CLAUDE_SETTINGS" >/dev/null 2>&1; then
      cp "$CLAUDE_SETTINGS" "${CLAUDE_SETTINGS}.pre-muxcode"
      mv "${CLAUDE_SETTINGS}.tmp" "$CLAUDE_SETTINGS"
      ok "Updated ~/.claude/settings.json (backup: settings.json.pre-muxcode)"
    else
      rm -f "${CLAUDE_SETTINGS}.tmp"
      ok "Claude Code hooks already up-to-date"
    fi
  fi
fi

# --- Configure OpenCode (if selected) ---
if $use_opencode; then
  info "Configuring OpenCode..."

  # API key check
  if [ -z "${ANTHROPIC_API_KEY:-}" ] && [ -z "${OPENAI_API_KEY:-}" ]; then
    echo ""
    info "OpenCode needs at least one API key configured."
    echo "  Set one of these in your shell profile or .env:"
    echo "    export ANTHROPIC_API_KEY=sk-ant-..."
    echo "    export OPENAI_API_KEY=sk-..."
    echo ""
    warn "No API key detected — OpenCode agents will fail until configured"
  else
    ok "API key found for OpenCode"
  fi

  ok "OpenCode configured"
fi

# --- Write default provider to config ---
CONFIG_FILE="$HOME/.config/muxcode/config"
if [ -f "$CONFIG_FILE" ]; then
  if ! grep -q '^MUXCODE_AGENT_CLI=' "$CONFIG_FILE"; then
    echo "" >> "$CONFIG_FILE"
    echo "# Default AI CLI provider (claude, opencode, local)" >> "$CONFIG_FILE"
    echo "MUXCODE_AGENT_CLI=$default_cli" >> "$CONFIG_FILE"
    ok "Added MUXCODE_AGENT_CLI=$default_cli to config"
  else
    ok "MUXCODE_AGENT_CLI already set in config"
  fi
else
  mkdir -p "$(dirname "$CONFIG_FILE")"
  cat > "$CONFIG_FILE" << EOF
# MuxCode configuration
# See: docs/configuration.md

# Default AI CLI provider (claude, opencode, local)
MUXCODE_AGENT_CLI=$default_cli

# Per-role overrides (uncomment to customize):
# MUXCODE_BUILD_CLI=opencode
# MUXCODE_TEST_CLI=opencode
# MUXCODE_BETA_CLI=opencode
EOF
  ok "Created config at $CONFIG_FILE"
fi

# --- Done ---
echo ""
echo -e "${GREEN}Installation complete!${NC}"
echo ""
echo "Installed providers:"
$use_claude   && echo -e "  ${GREEN}✓${NC} Claude Code (default: $( [[ $default_cli == claude ]] && echo 'yes' || echo 'no' ))"
$use_opencode && echo -e "  ${GREEN}✓${NC} OpenCode (default: $( [[ $default_cli == opencode ]] && echo 'yes' || echo 'no' ))"
echo ""
echo "Next steps:"
echo ""
echo "  1. Edit your config (optional):"
echo "     \$EDITOR ~/.config/muxcode/config"
echo ""
echo "  2. Launch a session:"
echo "     muxcode"
echo ""
if $use_opencode; then
  echo "  3. Assign agents to OpenCode (per-role):"
  echo "     MUXCODE_BUILD_CLI=opencode muxcode"
  echo ""
fi
