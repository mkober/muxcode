#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "$0")" && pwd)"

# ─────────────────────────────────────────────────────────────────────────────
# CLI
# ─────────────────────────────────────────────────────────────────────────────

usage() {
  cat <<'USAGE'
Usage: ./install.sh [options]

Options:
  -y, --yes      Non-interactive. Accept every recommended default and install
                 missing prerequisites without asking. Large optional downloads
                 (the Ollama model) are still declined.
      --no-deps  Never install system prerequisites; report them and continue.
      --no-color Disable colored output.
  -h, --help     Show this help.

Environment:
  MUXCODE_INSTALL_YES=1  Same as --yes.
  NO_COLOR=1             Same as --no-color.
  MUXCODE_OLLAMA_MODEL   Model pulled for the local LLM agents (default qwen3:4b).
USAGE
}

ASSUME_YES=false
INSTALL_DEPS=true
FORCE_NO_COLOR=false
for arg in "$@"; do
  case "$arg" in
    -y|--yes)   ASSUME_YES=true ;;
    --no-deps)  INSTALL_DEPS=false ;;
    --no-color) FORCE_NO_COLOR=true ;;
    -h|--help)  usage; exit 0 ;;
    *) echo "Unknown option: $arg (try --help)" >&2; exit 1 ;;
  esac
done
[ "${MUXCODE_INSTALL_YES:-}" = "1" ] && ASSUME_YES=true

# Prompts fall back to their documented default whenever the run is unattended.
# `read` returns non-zero at EOF, so under `set -e` a piped or CI install used
# to die at the first question with no diagnostic at all.
INTERACTIVE=true
if $ASSUME_YES || [ ! -t 0 ]; then
  INTERACTIVE=false
fi

# ─────────────────────────────────────────────────────────────────────────────
# Theme — Dracula, matching the nvim start screen and tmux status line
# ─────────────────────────────────────────────────────────────────────────────

if $FORCE_NO_COLOR || [ -n "${NO_COLOR:-}" ] || [ ! -t 1 ] || [ "${TERM:-dumb}" = "dumb" ]; then
  C_HEAD=""; C_SECT=""; C_OK=""; C_WARN=""; C_ERR=""; C_KEY=""; C_DIM=""; BOLD=""; NC=""
else
  C_HEAD=$'\033[38;5;117m'   # cyan     — logo, headings
  C_SECT=$'\033[38;5;212m'   # pink     — section rules
  C_OK=$'\033[38;5;84m'      # green    — success
  C_WARN=$'\033[38;5;215m'   # orange   — warnings
  C_ERR=$'\033[38;5;203m'    # red      — failures
  C_KEY=$'\033[38;5;141m'    # purple   — values, spinner
  C_DIM=$'\033[38;5;61m'     # comment  — secondary text
  BOLD=$'\033[1m'
  NC=$'\033[0m'
fi

TERM_COLS=$( { command -v tput >/dev/null 2>&1 && tput cols; } 2>/dev/null || echo 80 )
[ "$TERM_COLS" -gt 0 ] 2>/dev/null || TERM_COLS=80
[ "$TERM_COLS" -gt 100 ] && TERM_COLS=100

info() { printf '  %s→%s %s\n'  "$C_HEAD" "$NC" "$*"; }
ok()   { printf '  %s✓%s %s\n'  "$C_OK"   "$NC" "$*"; }
warn() { printf '  %s!%s %s\n'  "$C_WARN" "$NC" "$*"; }
fail() { printf '  %s✗%s %s\n'  "$C_ERR"  "$NC" "$*"; exit 1; }
note() { printf '    %s%s%s\n'  "$C_DIM"  "$*"  "$NC"; }

# row <symbol-color> <symbol> <label> <value> — aligned status line, so the
# prerequisite report reads as a table instead of a wall of sentences.
row() {
  printf '  %s%s%s %-14s %s%s%s\n' "$1" "$2" "$NC" "$3" "$C_DIM" "$4" "$NC"
}

TOTAL_STEPS=9
STEP=0
step() {
  STEP=$((STEP + 1))
  local label="  [$STEP/$TOTAL_STEPS] $1 "
  local rule_len=$(( TERM_COLS - ${#label} - 2 ))
  [ "$rule_len" -lt 3 ] && rule_len=3
  local rule=""
  rule=$(printf '─%.0s' $(seq 1 "$rule_len"))
  printf '\n%s%s%s%s%s%s\n\n' "$C_SECT" "$label" "$NC" "$C_DIM" "$rule" "$NC"
}

banner() {
  local art_w=63
  local pad=""
  [ "$TERM_COLS" -gt "$art_w" ] && pad=$(printf ' %.0s' $(seq 1 $(( (TERM_COLS - art_w) / 2 )) ))
  echo ""
  if [ "$TERM_COLS" -ge 66 ]; then
    printf '%s%s' "$C_HEAD" "$BOLD"
    while IFS= read -r l; do printf '%s%s\n' "$pad" "$l"; done <<'LOGO'
███╗   ███╗██╗   ██╗██╗  ██╗   ██████╗ ██████╗ ██████╗ ███████╗
████╗ ████║██║   ██║╚██╗██╔╝  ██╔════╝██╔═══██╗██╔══██╗██╔════╝
██╔████╔██║██║   ██║ ╚███╔╝   ██║     ██║   ██║██║  ██║█████╗
██║╚██╔╝██║██║   ██║ ██╔██╗   ██║     ██║   ██║██║  ██║██╔══╝
██║ ╚═╝ ██║╚██████╔╝██╔╝ ██╗  ╚██████╗╚██████╔╝██████╔╝███████╗
╚═╝     ╚═╝ ╚═════╝ ╚═╝  ╚═╝   ╚═════╝ ╚═════╝ ╚═════╝ ╚══════╝
LOGO
    printf '%s' "$NC"
  else
    printf '%s%s  muxcode%s\n' "$C_HEAD" "$BOLD" "$NC"
  fi
  printf '%s%s%s\n' "$C_DIM" "$(printf '%*s' $(( (TERM_COLS + 30) / 2 )) 'multi-agent coding environment')" "$NC"
  echo ""
}

# ─────────────────────────────────────────────────────────────────────────────
# Prompting
# ─────────────────────────────────────────────────────────────────────────────

# ask <prompt> <Y|N default> — exit 0 for yes, 1 for no.
ask() {
  local prompt="$1" default="$2" reply="" hint
  [ "$default" = "Y" ] && hint="[Y/n]" || hint="[y/N]"
  if ! $INTERACTIVE; then
    [ "$default" = "Y" ]
    return
  fi
  read -rp "$(printf '  %s?%s %s %s%s%s ' "$C_KEY" "$NC" "$prompt" "$C_DIM" "$hint" "$NC")" reply || reply=""
  [ -n "$reply" ] || reply="$default"
  [[ "$reply" =~ ^[Yy]$ ]]
}

# ask_value <prompt> <default> — echoes the reply, or the default when unattended.
ask_value() {
  local prompt="$1" default="$2" reply=""
  if ! $INTERACTIVE; then
    printf '%s' "$default"
    return
  fi
  read -rp "$(printf '  %s?%s %s %s[%s]%s ' "$C_KEY" "$NC" "$prompt" "$C_DIM" "$default" "$NC")" reply || reply=""
  printf '%s' "${reply:-$default}"
}

# ─────────────────────────────────────────────────────────────────────────────
# Command execution
# ─────────────────────────────────────────────────────────────────────────────

# Install attempts log here so a failure can name its cause. Previously every
# attempt ran under `2>/dev/null`, so the most common real failure — an npm
# EACCES — surfaced only as "Auto-install failed" with nothing to act on.
INSTALL_LOG="${TMPDIR:-/tmp}/muxcode-install-$$.log"
: > "$INSTALL_LOG"

run_logged() {
  local desc="$1"; shift
  if "$@" >>"$INSTALL_LOG" 2>&1; then
    return 0
  fi
  warn "$desc failed — last lines:"
  tail -5 "$INSTALL_LOG" | sed 's/^/      /'
  return 1
}

SPIN=(⠋ ⠙ ⠹ ⠸ ⠼ ⠴ ⠦ ⠧ ⠇ ⠏)

# run_spinner <message> <cmd...> — for non-interactive commands only. Anything
# that may prompt (sudo, npm auth) must run in the foreground instead, or its
# prompt would be painted over by the spinner and appear to hang.
run_spinner() {
  local msg="$1"; shift
  if [ ! -t 1 ]; then
    info "$msg"
    "$@" >>"$INSTALL_LOG" 2>&1
    return
  fi
  "$@" >>"$INSTALL_LOG" 2>&1 &
  local pid=$! i=0 rc=0
  while kill -0 "$pid" 2>/dev/null; do
    i=$(( (i + 1) % 10 ))
    printf '\r  %s%s%s %s' "$C_KEY" "${SPIN[$i]}" "$NC" "$msg"
    sleep 0.1
  done
  wait "$pid" || rc=$?
  printf '\r\033[K'
  return $rc
}

# version_ge <version-string> <minimum> — tolerates "3.6a", "go1.26.2" and
# "NVIM v0.12.2". Sorts by numeric field so 0.12 correctly outranks 0.9.
# An unparseable version never blocks the install.
version_ge() {
  local have want
  have=$(printf '%s' "$1" | grep -oE '[0-9]+(\.[0-9]+)*' | head -1 || true)
  want="$2"
  [ -n "$have" ] || return 0
  [ "$(printf '%s\n%s\n' "$want" "$have" | sort -t. -k1,1n -k2,2n -k3,3n | head -1)" = "$want" ]
}

# Trailing `|| true`: under `pipefail` a version string with no digits makes
# grep fail, and the caller's `hv=$(version_of ...)` assignment would abort the
# whole install under `set -e`. An unknown version is reported, never fatal.
version_of() { printf '%s' "$1" | grep -oE '[0-9]+(\.[0-9]+)*[a-z]?' | head -1 || true; }

# ─────────────────────────────────────────────────────────────────────────────
# Package manager
# ─────────────────────────────────────────────────────────────────────────────

PKG_MGR=""
PKG_INSTALL=""
if command -v brew >/dev/null 2>&1; then
  PKG_MGR="brew";   PKG_INSTALL="brew install"
elif command -v apt-get >/dev/null 2>&1; then
  PKG_MGR="apt";    PKG_INSTALL="sudo apt-get install -y"
elif command -v dnf >/dev/null 2>&1; then
  PKG_MGR="dnf";    PKG_INSTALL="sudo dnf install -y"
elif command -v pacman >/dev/null 2>&1; then
  PKG_MGR="pacman"; PKG_INSTALL="sudo pacman -S --noconfirm"
elif command -v zypper >/dev/null 2>&1; then
  PKG_MGR="zypper"; PKG_INSTALL="sudo zypper install -y"
fi

# Package names diverge per manager. An unmapped tool echoes nothing and is
# reported as a manual install rather than guessed at.
pkg_name_for() {
  case "$PKG_MGR:$1" in
    apt:go)      echo golang-go ;;
    dnf:go)      echo golang ;;
    *:go)        echo go ;;
    apt:make)    echo build-essential ;;
    pacman:make) echo base-devel ;;
    *:make)      echo make ;;
    *:nvim)      echo neovim ;;
    *:tmux)      echo tmux ;;
    *:jq)        echo jq ;;
    *:fzf)       echo fzf ;;
    *:git)       echo git ;;
    brew:ollama) echo ollama ;;
  esac
}

banner
$INTERACTIVE || note "non-interactive mode — accepting defaults"

# ─────────────────────────────────────────────────────────────────────────────
step "Prerequisites"
# ─────────────────────────────────────────────────────────────────────────────

# tool|minimum|required|version-command
#
# The tmux floor is 3.3, not the 3.0 previously advertised: config/tmux.conf
# builds every popup with `display-popup -b/-S/-T`, flags older tmux rejects,
# so a 3.0-3.2 install fails at runtime rather than here.
#
# fzf is optional — it backs the interactive project picker, but `muxcode <path>`
# works without it. It is still offered for install alongside the required set.
#
# ollama went required → optional and back (MUX-109): the Prompt mode's
# default backend is now OpenCode's hosted gateway, so the default path
# never touches Ollama — a required-tier prereq the default never uses
# is wrong. Local Ollama remains the MUXCODE_PROMPT_BACKEND=ollama
# opt-in, checked in the optional section below.
PREREQS="tmux|3.3|1|tmux -V
go|1.22|1|go version
git|2.0|1|git --version
make||1|make --version
jq||1|jq --version
nvim|0.9|1|nvim --version
fzf||0|fzf --version"

missing=()
missing_opt=()
outdated=()

while IFS='|' read -r tool min req vcmd; do
  [ -n "$tool" ] || continue
  if ! command -v "$tool" >/dev/null 2>&1; then
    if [ "$req" = "1" ]; then
      missing+=("$tool")
      row "$C_ERR" "✗" "$tool" "not found"
    else
      missing_opt+=("$tool")
      row "$C_DIM" "·" "$tool" "not found (optional)"
    fi
    continue
  fi
  # Trailing `|| true`: a tool can exist yet exit non-zero from its version
  # probe, and under `pipefail` that aborts the whole install at this
  # assignment with no diagnostic — the exact silent death this scan exists to
  # prevent. An unreadable version is reported as unknown, never fatal.
  have="$($vcmd 2>&1 | head -1 || true)"
  hv="$(version_of "$have")"
  if [ -n "$min" ] && ! version_ge "$have" "$min"; then
    outdated+=("$tool|$hv|$min")
    row "$C_WARN" "!" "$tool" "${hv:-unknown} — need >= $min"
  else
    row "$C_OK" "✓" "$tool" "${hv:-installed}"
  fi
done <<< "$PREREQS"

# Optional tools join the same install offer, but never block the install —
# the still-missing check below decides that per tier, not per batch.
[ ${#missing_opt[@]} -gt 0 ] && missing+=("${missing_opt[@]}") || true

echo ""

if [ ${#missing[@]} -gt 0 ]; then
  if ! $INSTALL_DEPS; then
    warn "--no-deps set; skipping automatic install of: ${missing[*]}"
  elif [ -z "$PKG_MGR" ]; then
    warn "No supported package manager found (brew/apt/dnf/pacman/zypper)"
    note "Install manually: ${missing[*]}"
  else
    pkg_list=""; unmapped=""
    for t in "${missing[@]}"; do
      p="$(pkg_name_for "$t")"
      if [ -n "$p" ]; then
        case " $pkg_list " in *" $p "*) ;; *) pkg_list="$pkg_list $p" ;; esac
      else
        unmapped="$unmapped $t"
      fi
    done
    pkg_list="${pkg_list# }"
    [ -n "$unmapped" ] && warn "No package mapping for:$unmapped — install manually"
    if [ -n "$pkg_list" ]; then
      info "Missing ${#missing[@]} — install via ${C_KEY}${PKG_MGR}${NC}: ${C_KEY}${pkg_list}${NC}"
      if ask "Install missing prerequisites now?" Y; then
        # Foreground, not run_spinner: apt/dnf/pacman go through sudo and a
        # password prompt hidden behind a spinner looks like a hang.
        echo ""
        if $PKG_INSTALL $pkg_list; then
          echo ""; ok "Prerequisites installed"
        else
          echo ""; warn "Some packages failed to install"
        fi
      fi
    fi
  fi

  # Split by tier rather than trusting the batch-level flag: only a required
  # tool may block, whatever else was missing alongside it.
  still_req=""; still_opt=""
  for t in "${missing[@]}"; do
    command -v "$t" >/dev/null 2>&1 && continue
    case " ${missing_opt[*]:-} " in
      *" $t "*) still_opt="$still_opt $t" ;;
      *)        still_req="$still_req $t" ;;
    esac
  done
  [ -n "$still_opt" ] && warn "Still missing (optional):$still_opt — continuing"
  if [ -n "$still_req" ]; then
    warn "Still missing:$still_req"
    ask "Continue anyway?" N || fail "Install the tools above and re-run"
  fi
fi

if [ ${#outdated[@]} -gt 0 ]; then
  warn "Below minimum version — muxcode may misbehave:"
  for o in "${outdated[@]}"; do
    note "${o%%|*} $(echo "$o" | cut -d'|' -f2) → need >= $(echo "$o" | cut -d'|' -f3)"
  done
  [ "${PKG_MGR:-}" = "brew" ] && note "Upgrade: brew upgrade <package>"
  ask "Continue anyway?" N || fail "Upgrade the tools above and re-run"
fi

[ ${#missing[@]} -eq 0 ] && [ ${#outdated[@]} -eq 0 ] && ok "All prerequisites satisfied"

# ─────────────────────────────────────────────────────────────────────────────
# AI CLI providers
#
# Every missing provider is offered, not just the ones needed to reach a
# working install. The old gating gave OpenCode a prompt only when Claude was
# absent and Codex one only when both were absent, so the common machine — one
# with Claude and OpenCode already present — printed "codex not found" and
# never asked. All but Claude default to no, so declining is one keypress and
# an unattended run installs nothing extra.
#
# Gemini is deliberately absent from this catalogue: ResolveProvider in
# bus/provider.go has no Gemini backend and falls through to Claude, so an
# agent configured for it would silently launch Claude instead.
# ─────────────────────────────────────────────────────────────────────────────

provider_label() {
  case "$1" in
    claude)   printf 'Claude Code' ;;
    opencode) printf 'OpenCode' ;;
    codex)    printf 'Codex CLI' ;;
  esac
}

provider_desc() {
  case "$1" in
    claude)   printf 'recommended — full hook support' ;;
    opencode) printf 'TUI mode — multi-provider LLM access' ;;
    codex)    printf 'exec mode — OpenAI models' ;;
  esac
}

provider_installed() {
  case "$1" in
    claude)   $use_claude ;;
    opencode) $use_opencode ;;
    codex)    $use_codex ;;
    *)        false ;;
  esac
}

# install_provider <key> — install one AI CLI, preferring its native installer
# and falling back to Homebrew. Sets the matching use_* flag and returns 0 on
# success; on failure names the manual command and returns 1 so the caller can
# offer a different choice rather than configuring a CLI that cannot launch.
install_provider() {
  case "$1" in
    claude)
      if run_spinner "Installing Claude Code via npm..." npm install -g @anthropic-ai/claude-code; then
        ok "Claude Code installed"; use_claude=true; return 0
      elif run_logged "brew install claude-code" brew install claude-code; then
        ok "Claude Code installed via Homebrew"; use_claude=true; return 0
      fi
      warn "Auto-install failed — install manually:"
      note "npm install -g @anthropic-ai/claude-code"
      ;;
    opencode)
      if run_spinner "Installing OpenCode..." bash -c 'curl -fsSL https://opencode.ai/install | bash'; then
        ok "OpenCode installed"; use_opencode=true
      elif run_logged "brew install opencode" brew install opencode; then
        ok "OpenCode installed via Homebrew"; use_opencode=true
      else
        warn "Auto-install failed — install manually:"
        note "curl -fsSL https://opencode.ai/install | bash"
        note "see $INSTALL_LOG for the failure reason"
        return 1
      fi
      case ":$PATH:" in
        *":$HOME/.opencode/bin:"*) ;;
        *) [ -d "$HOME/.opencode/bin" ] && export PATH="$HOME/.opencode/bin:$PATH" || true ;;
      esac
      return 0
      ;;
    codex)
      if run_spinner "Installing Codex CLI..." npm install -g @openai/codex; then
        ok "Codex CLI installed"; use_codex=true; return 0
      elif run_logged "brew install --cask codex" brew install --cask codex; then
        ok "Codex CLI installed via Homebrew"; use_codex=true; return 0
      fi
      warn "Auto-install failed — install manually:"
      note "npm install -g @openai/codex"
      ;;
  esac
  note "see $INSTALL_LOG for the failure reason"
  return 1
}

# ─────────────────────────────────────────────────────────────────────────────
step "AI CLI providers"
# ─────────────────────────────────────────────────────────────────────────────

note "Each agent window can use a different provider. At least one is required."
echo ""

use_claude=false
use_opencode=false
use_codex=false

# --- Claude Code ---
if command -v claude >/dev/null 2>&1; then
  row "$C_OK" "✓" "claude" "$(version_of "$(claude --version 2>/dev/null || echo '')")"
  use_claude=true
else
  row "$C_DIM" "·" "claude" "not found"
  if ask "Install Claude Code? ($(provider_desc claude))" Y; then
    install_provider claude || true
  fi
fi

# --- OpenCode — add known install location to PATH if not already there ---
if [ -d "$HOME/.opencode/bin" ] && [[ ":$PATH:" != *":$HOME/.opencode/bin:"* ]]; then
  export PATH="$HOME/.opencode/bin:$PATH"
fi
if command -v opencode >/dev/null 2>&1; then
  oc_version=$(opencode --version 2>/dev/null || echo "unknown")
  if version_ge "$oc_version" 1.4; then
    row "$C_OK" "✓" "opencode" "$(version_of "$oc_version")"
  else
    row "$C_WARN" "!" "opencode" "$(version_of "$oc_version") — need >= 1.4.0 (server API)"
    note "Update: curl -fsSL https://opencode.ai/install | bash"
  fi
  use_opencode=true
else
  row "$C_DIM" "·" "opencode" "not found"
  if ask "Install OpenCode? ($(provider_desc opencode))" N; then
    install_provider opencode || true
  fi
fi

# --- Codex CLI ---
if command -v codex >/dev/null 2>&1; then
  row "$C_OK" "✓" "codex" "$(version_of "$(codex --version 2>/dev/null || echo '')")"
  use_codex=true
else
  row "$C_DIM" "·" "codex" "not found"
  if ask "Install Codex CLI? ($(provider_desc codex))" N; then
    install_provider codex || true
  fi
fi

echo ""
if ! $use_claude && ! $use_opencode && ! $use_codex; then
  fail "At least one AI CLI provider is required. Re-run and select a provider."
fi

# ─────────────────────────────────────────────────────────────────────────────
step "Default provider"
# ─────────────────────────────────────────────────────────────────────────────

# Every supported provider is listed whether or not it is installed, so the menu
# doubles as the discovery path for one the user does not have yet. Numbering is
# fixed — option 2 is always OpenCode — so it never shifts with what happens to
# be present. Choosing a missing provider offers to install it on the spot.
PROVIDER_KEYS=(claude opencode codex)

# The default is the first provider actually present. The previous menu always
# defaulted to option 1 (claude) even when claude was the one provider missing,
# so pressing Enter — or any unattended run — configured a CLI that is not
# installed.
default_cli=""
default_idx=1
idx=0
for key in "${PROVIDER_KEYS[@]}"; do
  idx=$((idx + 1))
  if [ -z "$default_cli" ] && provider_installed "$key"; then
    default_cli="$key"; default_idx=$idx
  fi
done

idx=0
for key in "${PROVIDER_KEYS[@]}"; do
  idx=$((idx + 1))
  if provider_installed "$key"; then
    printf '    %s%s%s  %-12s %s(%s)%s\n' \
      "$C_KEY" "$idx" "$NC" "$(provider_label "$key")" "$C_DIM" "$(provider_desc "$key")" "$NC"
  else
    printf '    %s%s%s  %-12s %s(%s)%s  %s· not installed%s\n' \
      "$C_KEY" "$idx" "$NC" "$(provider_label "$key")" "$C_DIM" "$(provider_desc "$key")" "$NC" "$C_WARN" "$NC"
  fi
done
echo ""

while true; do
  choice="$(ask_value "Default provider" "$default_idx")"
  chosen=""
  case "$choice" in
    ''|*[!0-9]*) ;;
    *)
      if [ "$choice" -ge 1 ] && [ "$choice" -le "${#PROVIDER_KEYS[@]}" ]; then
        chosen="${PROVIDER_KEYS[$((choice - 1))]}"
      fi
      ;;
  esac
  if [ -z "$chosen" ]; then
    $INTERACTIVE || break
    warn "Enter 1, 2, or 3."
    continue
  fi
  if provider_installed "$chosen"; then
    default_cli="$chosen"; break
  fi
  if ask "$(provider_label "$chosen") is not installed — install it now?" Y && install_provider "$chosen"; then
    default_cli="$chosen"; break
  fi
  # Declined or failed: re-open the menu rather than configure a provider that
  # cannot launch. Unattended runs keep the detected default instead of looping.
  $INTERACTIVE || break
  warn "Keeping $(provider_label "$default_cli") — pick another provider"
done
ok "Default provider: ${C_KEY}${default_cli}${NC}"

# ─────────────────────────────────────────────────────────────────────────────
step "Optional components"
# ─────────────────────────────────────────────────────────────────────────────

# --- Ollama model (optional local backend, consent-gated download) ---
# The Prompt mode's default backend is the OpenCode gateway (MUX-109);
# local Ollama is the MUXCODE_PROMPT_BACKEND=ollama opt-in and the local
# LLM agents' engine.
if command -v ollama >/dev/null 2>&1; then
  OLLAMA_MODEL="${MUXCODE_OLLAMA_MODEL:-qwen3:4b}"
  if ! ollama list >/dev/null 2>&1; then
    row "$C_WARN" "!" "ollama" "installed but not running (ollama serve)"
    note "model auto-pulls on first local agent run"
  # Exact-name match (":latest" tolerated for an untagged config value): the
  # old family-prefix grep reported "ready" when any same-family sibling of a
  # different size was pulled.
  elif ollama list | awk '{print $1}' | grep -qxE "${OLLAMA_MODEL}(:latest)?"; then
    row "$C_OK" "✓" "ollama" "$OLLAMA_MODEL ready"
  else
    row "$C_OK" "✓" "ollama" "installed — $OLLAMA_MODEL not pulled"
    # Multi-GB download — the one thing --yes will not accept on the user's behalf.
    if ask "Pull $OLLAMA_MODEL now? (several GB)" N; then
      # Foreground: ollama prints its own progress; behind a spinner it reads as a hang.
      echo ""
      if ollama pull "$OLLAMA_MODEL"; then
        echo ""; ok "Model $OLLAMA_MODEL pulled"
      else
        echo ""; warn "Pull failed — the local agent will auto-pull on first run"
      fi
    else
      note "skipped — auto-pulls on first local agent run"
    fi
  fi
else
  row "$C_DIM" "·" "ollama" "not found (optional — local LLM agents, MUXCODE_PROMPT_BACKEND=ollama)"
fi

# --- OpenCode gateway key (Prompt mode's default backend — MUX-109) ---
# The check the required-Ollama tier turned into: the default Prompt
# backend needs a Zen gateway key, not a local model. Prompt for it when
# missing — interactive only: a scripted install must never pause for a
# secret, so --yes and non-TTY fall through to the warning. The read is
# silent (a key echoed into scrollback outlives the install) and the key
# never rides argv (ps exposes argv to other local users). Blank = skip.
CONFIG_FILE="$HOME/.config/muxcode/config"
# Presence check tolerates every assignment shape the shell accepts —
# optional `export ` prefix, quoted or unquoted, single or double quotes
# (Copilot review catch, PR #40: the quoted-only match caused a false
# re-prompt for `export MUXCODE_OPENCODE_API_KEY=sk-...`).
existing_okey="$(sed -n -E 's/^(export )?MUXCODE_OPENCODE_API_KEY=//p' "$CONFIG_FILE" 2>/dev/null | head -1 | tr -d "\"'")"
if [ -n "${MUXCODE_OPENCODE_API_KEY:-}" ] || [ -n "$existing_okey" ]; then
  row "$C_OK" "✓" "opencode-key" "gateway key configured (Prompt mode ready)"
else
  okey=""
  if $INTERACTIVE; then
    printf "  %s?%s OpenCode Zen API key (blank to skip — or set MUXCODE_PROMPT_BACKEND=ollama to run locally): " "$C_KEY" "$NC"
    read -rs okey || okey=""
    echo ""
  fi
  if [ -n "$okey" ]; then
    mkdir -p "$(dirname "$CONFIG_FILE")"
    touch "$CONFIG_FILE"
    if grep -Eq '^(export )?MUXCODE_OPENCODE_API_KEY=' "$CONFIG_FILE" 2>/dev/null; then
      # Replace the existing (empty/commented-out-value) assignment —
      # appending a second one means the last silently wins. The match
      # tolerates an `export ` prefix like the presence check above.
      awk -v k="$okey" '/^(export )?MUXCODE_OPENCODE_API_KEY=/{print "MUXCODE_OPENCODE_API_KEY=\"" k "\""; next} {print}' \
        "$CONFIG_FILE" > "$CONFIG_FILE.tmp" && mv "$CONFIG_FILE.tmp" "$CONFIG_FILE"
    else
      {
        echo ""
        echo "# OpenCode Zen gateway key — the Prompt mode's default backend (MUX-109)"
        echo "MUXCODE_OPENCODE_API_KEY=\"$okey\""
      } >> "$CONFIG_FILE"
    fi
    chmod 600 "$CONFIG_FILE"
    row "$C_OK" "✓" "opencode-key" "gateway key saved to ~/.config/muxcode/config (chmod 600)"
  else
    row "$C_WARN" "!" "opencode-key" "no MUXCODE_OPENCODE_API_KEY — Prompt mode needs one (or MUXCODE_PROMPT_BACKEND=ollama)"
    note "add MUXCODE_OPENCODE_API_KEY=sk-... to ~/.config/muxcode/config"
  fi
fi

# One-time permissions tighten — independent of the prompt above: the
# config can hold JIRA_API_TOKEN and the gateway key, and it was found
# live at 644 (world-readable) with both (2026-08-27). Secrets present +
# group/other read bits => tighten and say so.
if [ -f "$CONFIG_FILE" ] && grep -qE '^[A-Z_]*(_TOKEN|_KEY)="..*"' "$CONFIG_FILE" 2>/dev/null; then
  cfg_perms=$(stat -f %Lp "$CONFIG_FILE" 2>/dev/null || stat -c %a "$CONFIG_FILE" 2>/dev/null || echo "600")
  if [ "${cfg_perms#?}" != "00" ]; then
    chmod 600 "$CONFIG_FILE"
    row "$C_OK" "✓" "config-perms" "tightened ~/.config/muxcode/config to 600 (held secrets group/other-readable)"
  fi
fi

# --- Diagram renderers (plan-agent diagram authoring) ---
# scripts/render-diagram.sh renders draw.io / Mermaid diagrams to SVG/PNG for
# embedding into docs, Jira, and Confluence. Both degrade gracefully.
if command -v mmdc >/dev/null 2>&1; then
  row "$C_OK" "✓" "mmdc" "Mermaid diagrams"
else
  row "$C_DIM" "·" "mmdc" "not found (optional — Mermaid diagrams)"
  if ask "Install Mermaid CLI (mmdc)?" N; then
    run_spinner "Installing @mermaid-js/mermaid-cli..." npm install -g @mermaid-js/mermaid-cli \
      && ok "mmdc installed" \
      || warn "Install manually: npm install -g @mermaid-js/mermaid-cli"
  fi
fi

# The macOS cask installs the binary inside the app bundle (not on PATH);
# scripts/render-diagram.sh auto-discovers it there, so no PATH surgery needed.
if command -v drawio >/dev/null 2>&1 \
   || [ -x "/Applications/draw.io.app/Contents/MacOS/draw.io" ] \
   || [ -x "$HOME/Applications/draw.io.app/Contents/MacOS/draw.io" ]; then
  row "$C_OK" "✓" "drawio" "AWS / draw.io diagrams"
else
  row "$C_DIM" "·" "drawio" "not found (optional — architecture diagrams)"
  if ask "Install draw.io desktop?" N; then
    # Foreground: a cask install may prompt for a sudo password.
    if brew install --cask drawio; then
      ok "draw.io installed"
    else
      warn "Install manually: brew install --cask drawio"
    fi
  fi
fi

# ─────────────────────────────────────────────────────────────────────────────
step "PATH"
# ─────────────────────────────────────────────────────────────────────────────

mkdir -p "$HOME/.local/bin"
if [[ ":$PATH:" != *":$HOME/.local/bin:"* ]]; then
  warn "~/.local/bin is not in your PATH"
  profile=""
  case "$(basename "${SHELL:-bash}")" in
    zsh)  profile="$HOME/.zshrc" ;;
    bash) [ -f "$HOME/.bash_profile" ] && profile="$HOME/.bash_profile" || profile="$HOME/.bashrc" ;;
    fish) profile="" ;;
  esac
  path_line='export PATH="$HOME/.local/bin:$PATH"'
  if [ -n "$profile" ] && ask "Add it to $(basename "$profile")?" Y; then
    if grep -qF '.local/bin' "$profile" 2>/dev/null; then
      ok "$(basename "$profile") already references ~/.local/bin"
    else
      printf '\n# Added by the muxcode installer\n%s\n' "$path_line" >> "$profile"
      ok "Added to $profile"
      note "run: source $profile"
    fi
  else
    note "Add to your shell profile:  $path_line"
  fi
fi
# Export for the remainder of this run so build.sh and the smoke test below
# resolve the freshly installed binary even before the user reloads a shell.
export PATH="$HOME/.local/bin:$PATH"
ok "~/.local/bin ready"

# ─────────────────────────────────────────────────────────────────────────────
step "Build and install"
# ─────────────────────────────────────────────────────────────────────────────

if run_spinner "Compiling Go binaries and installing..." "$REPO_DIR/build.sh"; then
  ok "Binary, agents, skills, and configs installed"
else
  warn "Build failed — last lines:"
  tail -15 "$INSTALL_LOG" | sed 's/^/      /'
  fail "Fix the build error above and re-run. Full log: $INSTALL_LOG"
fi

# ─────────────────────────────────────────────────────────────────────────────
step "Editor and multiplexer"
# ─────────────────────────────────────────────────────────────────────────────

# --- tmux ---
TMUX_CONF="$HOME/.tmux.conf"
TMUX_SOURCE_LINE="source-file ~/.config/muxcode/tmux.conf"

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
  printf '# Muxcode: multi-agent coding environment\n%s\n' "$TMUX_SOURCE_LINE" > "$TMUX_CONF"
  ok "Created ~/.tmux.conf sourcing the muxcode config"
fi

# --- Neovim (NVIM_APPNAME=muxcode; personal ~/.config/nvim is never touched) ---
NVIM_CONFIGDIR="$HOME/.config/muxcode/nvim"
install_nvim_config() {
  mkdir -p "$NVIM_CONFIGDIR/plugin"
  cp "$REPO_DIR/config/nvim/init.lua" "$NVIM_CONFIGDIR/init.lua"
  cp "$REPO_DIR/config/nvim/plugin/startscreen.lua" "$NVIM_CONFIGDIR/plugin/startscreen.lua"
  ok "Neovim config installed to ~/.config/muxcode/nvim/"
}

if [ -f "$NVIM_CONFIGDIR/init.lua" ]; then
  note "existing muxcode nvim config found (user extensions in lua/user/ and after/ are preserved)"
  if ask "Overwrite muxcode nvim config?" Y; then
    install_nvim_config
  else
    warn "Skipped nvim config install"
  fi
else
  install_nvim_config
fi

# Clean up old site plugin from pre-NVIM_APPNAME installs
OLD_SITE_PLUGIN="$HOME/.local/share/nvim/site/plugin/muxcode-startscreen.lua"
if [ -f "$OLD_SITE_PLUGIN" ]; then
  rm -f "$OLD_SITE_PLUGIN"
  ok "Removed old start screen from site/plugin"
fi

# ─────────────────────────────────────────────────────────────────────────────
step "Provider configuration"
# ─────────────────────────────────────────────────────────────────────────────

# --- Claude Code hooks ---
if $use_claude; then
  CLAUDE_SETTINGS="$HOME/.claude/settings.json"
  MUXCODE_SETTINGS="$HOME/.config/muxcode/settings.json"

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

      # Stop (and other matcher-less events) fire on every turn end — they have
      # no matcher, so merge by appending a { "hooks": [ ... ] } group for each
      # command not already present anywhere under .hooks.Stop (idempotent).
      reduce ($mc[0].hooks.Stop // [] | .[] | .hooks[]) as $h (
        .;
        if ((.hooks.Stop // []) | [.[].hooks[]?.command] | index($h.command)) then .
        else .hooks.Stop = ((.hooks.Stop // []) + [{"hooks": [$h]}]) end
      ) |

      # Drop allow rules superseded by a newer form before merging. Claude Code
      # only matches Edit(path) rules for file edits — a Write(path) allow rule
      # is rejected at startup with a warning. The merge below is an additive
      # union, so a stale entry from an earlier install would survive forever
      # unless it is pruned here.
      .permissions.allow = (.permissions.allow - ["Write(/tmp/muxcode-*)", "Write(/private/tmp/muxcode-*)"]) |

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

# --- OpenCode auth ---
if $use_opencode; then
  if [ -n "${ANTHROPIC_API_KEY:-}" ] || [ -n "${OPENAI_API_KEY:-}" ]; then
    ok "OpenCode — API key found"
  else
    warn "OpenCode — no API key detected; agents will fail until configured"
    note 'export ANTHROPIC_API_KEY=sk-ant-...   or   export OPENAI_API_KEY=sk-...'
  fi
fi

# --- Codex auth: subscription login OR API key ---
if $use_codex; then
  codex_config_dir="${XDG_CONFIG_HOME:-$HOME/.config}/codex"
  if [ -n "${OPENAI_API_KEY:-}" ]; then
    ok "Codex CLI — OPENAI_API_KEY found"
  elif [ -d "$codex_config_dir" ] || [ -f "$HOME/.codex/auth.json" ] || [ -f "$HOME/.codexrc" ]; then
    ok "Codex CLI — subscription login detected"
  else
    warn "Codex CLI — no auth detected; agents will fail until configured"
    note "run: codex login    or    export OPENAI_API_KEY=sk-..."
  fi
fi

# --- Default provider in the muxcode config ---
CONFIG_FILE="$HOME/.config/muxcode/config"
if [ -f "$CONFIG_FILE" ]; then
  # Matches the commented form too. The generated config ships the setting
  # commented out ("Uncomment to force all roles to a single provider"), so an
  # anchored '^MUXCODE_AGENT_CLI=' test missed it and a second install appended
  # an active line — silently overriding the per-role provider defaults.
  if grep -qE '^[[:space:]]*#?[[:space:]]*MUXCODE_AGENT_CLI=' "$CONFIG_FILE"; then
    ok "MUXCODE_AGENT_CLI already present in config"
  else
    printf '\n# Default AI CLI provider (claude, opencode, codex, local).\n# Uncomment to force all roles to a single provider:\n# MUXCODE_AGENT_CLI=%s\n' "$default_cli" >> "$CONFIG_FILE"
    ok "Added commented MUXCODE_AGENT_CLI=$default_cli to config"
  fi
else
  mkdir -p "$(dirname "$CONFIG_FILE")"
  cat > "$CONFIG_FILE" << EOF
# MuxCode configuration
# See: docs/configuration.md

# Default AI CLI provider — overrides built-in role defaults.
# Built-in defaults: edit/review/analyze/api → claude,
#   build/test/deploy/run/watch/commit → opencode (MiniMax M3).
# Uncomment to force all roles to a single provider:
# MUXCODE_AGENT_CLI=$default_cli

# Per-role overrides (uncomment to customize):
# MUXCODE_REVIEW_CLI=codex
# MUXCODE_BUILD_MODEL=anthropic/claude-sonnet-5
EOF
  ok "Created config at ~/.config/muxcode/config"
fi

# ─────────────────────────────────────────────────────────────────────────────
step "Verify"
# ─────────────────────────────────────────────────────────────────────────────

verify_failed=false
if ! command -v muxcode >/dev/null 2>&1; then
  warn "muxcode not found on PATH — open a new shell, or source your profile"
  verify_failed=true
elif installed=$(muxcode version 2>/dev/null); then
  ok "muxcode runs correctly: $installed"
else
  warn "muxcode is on PATH but 'muxcode version' failed"
  verify_failed=true
fi

for f in "$HOME/.config/muxcode/agents" "$HOME/.config/muxcode/skills" "$HOME/.config/muxcode/tmux.conf"; do
  [ -e "$f" ] || { warn "missing: $f"; verify_failed=true; }
done
$verify_failed || ok "Installed files present"

# ─────────────────────────────────────────────────────────────────────────────
# Summary
# ─────────────────────────────────────────────────────────────────────────────

echo ""
if $verify_failed; then
  printf '  %s%sInstalled with warnings%s\n' "$C_WARN" "$BOLD" "$NC"
else
  printf '  %s%sInstallation complete%s\n' "$C_OK" "$BOLD" "$NC"
fi
echo ""

$use_claude   && row "$C_OK" "✓" "Claude Code" "$([ "$default_cli" = claude ]   && echo 'default' || echo 'available')"
$use_opencode && row "$C_OK" "✓" "OpenCode"    "$([ "$default_cli" = opencode ] && echo 'default' || echo 'available')"
$use_codex    && row "$C_OK" "✓" "Codex CLI"   "$([ "$default_cli" = codex ]    && echo 'default' || echo 'available')"

echo ""
printf '  %sNext steps%s\n' "$BOLD" "$NC"
printf '    %s1%s  Launch a session      %smuxcode%s\n'                       "$C_KEY" "$NC" "$C_DIM" "$NC"
printf '    %s2%s  Edit config           %s$EDITOR ~/.config/muxcode/config%s\n' "$C_KEY" "$NC" "$C_DIM" "$NC"
if $use_opencode || $use_codex; then
  printf '    %s3%s  Per-role provider     %sMUXCODE_BUILD_CLI=opencode muxcode%s\n' "$C_KEY" "$NC" "$C_DIM" "$NC"
fi
echo ""

if [ -s "$INSTALL_LOG" ]; then
  note "install log: $INSTALL_LOG"
else
  rm -f "$INSTALL_LOG"
fi
