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

# --- Check prerequisites ---
info "Checking prerequisites..."

missing=()
command -v tmux   >/dev/null 2>&1 || missing+=("tmux (>= 3.0)")
command -v go     >/dev/null 2>&1 || missing+=("go (>= 1.22)")
command -v claude >/dev/null 2>&1 || missing+=("claude (Claude Code CLI)")
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
  ok "All required tools found"
fi

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

# --- Configure Claude Code hooks ---
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

# --- Done ---
echo ""
echo -e "${GREEN}Installation complete!${NC}"
echo ""
echo "Next steps:"
echo ""
echo "  1. Edit your config (optional):"
echo ""
echo "     \$EDITOR ~/.config/muxcode/config"
echo ""
echo "  2. Launch a session:"
echo ""
echo "     muxcode"
echo ""
