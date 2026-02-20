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
echo -e "${GREEN}muxcoder${NC} — multi-agent coding environment"
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

# --- Post-install guidance ---
echo ""
echo -e "${GREEN}Installation complete!${NC}"
echo ""
echo "Next steps:"
echo ""
echo "  1. Add the tmux snippet to your .tmux.conf:"
echo ""
echo "     source-file ~/.config/muxcoder/tmux.conf"
echo ""
echo "  2. Copy Claude Code hooks to your project:"
echo ""
echo "     cp ~/.config/muxcoder/settings.json <project>/.claude/settings.json"
echo ""
echo "  3. Edit your config (optional):"
echo ""
echo "     \$EDITOR ~/.config/muxcoder/config"
echo ""
echo "  4. Launch a session:"
echo ""
echo "     muxcoder"
echo ""
