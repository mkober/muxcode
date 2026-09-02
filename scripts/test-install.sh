#!/usr/bin/env bash
# Integration test for install.sh.
#
# Runs the installer against a throwaway HOME so nothing on the real machine is
# touched, and asserts the properties that matter for a first-time install on a
# clean box: the script survives a non-TTY run, the version gate rejects
# too-old tools, and a full install produces a working binary idempotently.
set -uo pipefail

REPO_DIR="$(cd "$(dirname "$0")/.." && pwd)"
INSTALL="$REPO_DIR/install.sh"

pass=0; fail=0
ok()   { echo "  PASS  $*"; pass=$((pass + 1)); }
bad()  { echo "  FAIL  $*"; fail=$((fail + 1)); }
check() { if [ "$1" = "$2" ]; then ok "$3"; else bad "$3 (want '$2', got '$1')"; fi; }

echo "=== install.sh integration test ==="

# --- 1. Syntax -------------------------------------------------------------
bash -n "$INSTALL" 2>/dev/null && ok "syntax valid" || bad "syntax error"

# --- 2. CLI contract -------------------------------------------------------
bash "$INSTALL" --help >/dev/null 2>&1
check "$?" "0" "--help exits 0"

bash "$INSTALL" --bogus >/dev/null 2>&1
check "$?" "1" "unknown flag exits 1"

bash "$INSTALL" --help 2>/dev/null | grep -q -- "--yes" \
  && ok "--help documents --yes" || bad "--help missing --yes"

# Throwaway HOME for the output-probing sections. Kept separate from section
# 5's sandbox so that one still exercises a true first-time install.
PROBE_HOME="$(mktemp -d)"
PROBE_PATH="$(echo "$PATH" | tr ':' '\n' | grep -v "^$HOME/.local/bin$" | paste -sd: -)"

# probe_install [path-prefix] — run the installer purely to capture its output.
# The `head` truncation upstream still keeps this fast; the sandbox is what
# makes it safe.
probe_install() {
  env -i \
    HOME="$PROBE_HOME" \
    PATH="${1:+$1:}$PROBE_PATH" \
    TERM="${TERM:-xterm}" \
    SHELL=/bin/bash \
    LC_ALL=C \
    MUXCODE_SKIP_DAEMON_UPGRADE=1 \
    MUXCODE_LIFECYCLE_LOG_DIR="$PROBE_HOME/lifecycle-logs" \
    bash "$INSTALL" --no-deps </dev/null 2>&1
}

# --- 3. Non-TTY must not abort at the first prompt --------------------------
# Regression: `read` returns non-zero at EOF, which under `set -e` killed piped
# installs silently. The installer must detect the non-TTY and use defaults.
out="$(probe_install | head -40)"
echo "$out" | grep -q "non-interactive" \
  && ok "non-TTY detected, defaults used" \
  || bad "non-TTY not detected"

echo "$out" | grep -q "Prerequisites" \
  && ok "reached prerequisite step without aborting" \
  || bad "aborted before prerequisite step"

# --- 4. Version gate --------------------------------------------------------
# Stub a tmux below the 3.3 popup floor; the installer must flag it rather than
# passing it through to fail at runtime on display-popup.
stub="$(mktemp -d)"
printf '#!/bin/sh\necho "tmux 3.1b"\n' > "$stub/tmux"; chmod +x "$stub/tmux"
gate="$(probe_install "$stub" | head -40)"
echo "$gate" | grep -q "need >= 3.3" \
  && ok "version gate flags tmux 3.1 as too old" \
  || bad "version gate missed old tmux"
rm -rf "$stub" "$PROBE_HOME"

# --- 5. Full sandboxed install ---------------------------------------------
# A throwaway HOME isolates every write (~/.local/bin, ~/.config/muxcode,
# ~/.tmux.conf, ~/.claude). The real ~/.local/bin is stripped from PATH so
# build.sh cannot find an installed muxcode and skips upgrade-daemons, which
# would otherwise restart the daemons of live sessions.
SANDBOX="$(mktemp -d)"
CLEAN_PATH="$(echo "$PATH" | tr ':' '\n' | grep -v "^$HOME/.local/bin$" | paste -sd: -)"

run_install() {
  # MUXCODE_SKIP_DAEMON_UPGRADE is mandatory, not tidiness: build.sh otherwise
  # runs `muxcode upgrade-daemons`, which repoints the live session's daemons at
  # this sandbox's binary — and that binary is deleted when the test finishes.
  # MUXCODE_LIFECYCLE_LOG_DIR keeps timestamped logs out of the sandbox config
  # dir so the idempotency manifest compares installed artifacts only.
  env -i \
    HOME="$SANDBOX" \
    PATH="$CLEAN_PATH" \
    TERM="${TERM:-xterm}" \
    SHELL=/bin/bash \
    LC_ALL=C \
    MUXCODE_SKIP_DAEMON_UPGRADE=1 \
    MUXCODE_LIFECYCLE_LOG_DIR="$SANDBOX/lifecycle-logs" \
    bash "$INSTALL" --yes --no-deps >"$SANDBOX/install-$1.log" 2>&1
}

run_install 1
rc=$?
check "$rc" "0" "sandboxed install exits 0"

if [ -x "$SANDBOX/.local/bin/muxcode" ]; then
  ok "binary installed to sandbox"
else
  bad "binary missing from sandbox"
  echo "  --- tail of install log ---"
  tail -20 "$SANDBOX/install-1.log" | sed 's/^/    /'
fi

for f in .config/muxcode/agents .config/muxcode/skills .config/muxcode/tmux.conf \
         .config/muxcode/nvim/init.lua .config/muxcode/config .tmux.conf; do
  [ -e "$SANDBOX/$f" ] && ok "installed: $f" || bad "missing: $f"
done

grep -q "muxcode/tmux.conf" "$SANDBOX/.tmux.conf" 2>/dev/null \
  && ok ".tmux.conf sources muxcode config" || bad ".tmux.conf not wired up"

# The installed binary must actually run. A bare `muxcode` opens the project
# picker, so probe with the read-only `version` subcommand instead.
"$SANDBOX/.local/bin/muxcode" version >/dev/null 2>&1
check "$?" "0" "installed binary runs (version)"

grep -q "Installation complete" "$SANDBOX/install-1.log" \
  && ok "reported success" || bad "did not report success"

# --- 6. Idempotency ---------------------------------------------------------
# Per-file manifests, not one rolled-up hash: when this fails, the useful
# information is *which* file moved, not that something did.
manifest() {
  find "$SANDBOX/.config/muxcode" -type f \
       -not -path '*/logs/*' -not -path '*/memory/*' 2>/dev/null | sort | while read -r f; do
    printf '%s  %s\n' "$(shasum "$f" 2>/dev/null | cut -d' ' -f1)" "${f#"$SANDBOX/"}"
  done
}
manifest > "$SANDBOX/manifest-1"
run_install 2
rc2=$?
manifest > "$SANDBOX/manifest-2"
check "$rc2" "0" "second install exits 0"

if diff -q "$SANDBOX/manifest-1" "$SANDBOX/manifest-2" >/dev/null 2>&1; then
  ok "second install is idempotent"
else
  bad "second install is not idempotent — files that changed:"
  diff "$SANDBOX/manifest-1" "$SANDBOX/manifest-2" | grep -E '^[<>]' | sed 's/^/      /'
  # Show the actual content delta for the first changed file, re-deriving it by
  # rerunning nothing: the file on disk is the post-run-2 state.
  first="$(diff "$SANDBOX/manifest-1" "$SANDBOX/manifest-2" | grep -E '^>' | head -1 | awk '{print $3}')"
  if [ -n "$first" ] && [ -f "$SANDBOX/$first" ]; then
    echo "      --- tail of $first after run 2 ---"
    tail -12 "$SANDBOX/$first" | sed 's/^/        /'
  fi
fi

# A re-run must not duplicate the tmux source line.
n="$(grep -c "muxcode/tmux.conf" "$SANDBOX/.tmux.conf" 2>/dev/null || true)"
check "${n:-0}" "1" "tmux source line not duplicated"

# Regression guard: the sandbox must never end up owning a live daemon.
if ps -eo args 2>/dev/null | grep -v grep | grep -q "$SANDBOX"; then
  bad "a running process references the sandbox — daemon upgrade leaked out"
  ps -eo pid,args | grep -v grep | grep "$SANDBOX" | sed 's/^/      /'
else
  ok "no live process references the sandbox"
fi

rm -rf "$SANDBOX"

# --- 7. Every missing provider is offered -----------------------------------
# Sources the real step-2 provider block with stubs at its boundary (ask, row,
# install_provider), so this asserts the shipped lines rather than a copy of
# them. Regression: the gating used to offer OpenCode only when Claude was
# absent and Codex only when both were absent, so the common machine — Claude
# and OpenCode already present — printed "codex not found" and never asked.

helpers_from=$(grep -n '^provider_label()' "$INSTALL" | cut -d: -f1)
helpers_to=$(( $(grep -n '^# install_provider <key>' "$INSTALL" | cut -d: -f1) - 1 ))
# Starts at the use_* initialisation, not at the Claude comment, so the
# extracted region is self-contained under set -u.
body_from=$(grep -n '^use_claude=false' "$INSTALL" | cut -d: -f1)
body_to=$(( $(grep -n '^if ! \$use_claude && ! \$use_opencode && ! \$use_codex; then' "$INSTALL" | cut -d: -f1) - 1 ))

PROV_DIR="$(mktemp -d)"
mkdir -p "$PROV_DIR/home"
sed -n "${helpers_from},${helpers_to}p" "$INSTALL" > "$PROV_DIR/helpers.sh"
sed -n "${body_from},${body_to}p"       "$INSTALL" > "$PROV_DIR/body.sh"

# Absolute path: the PATH override below is a command prefix, so it governs the
# lookup of the interpreter too — a bare "bash" there is not found at all.
PROV_BASH="$(command -v bash)"

# offers_when <installed-provider>... — echoes the providers the block offers to
# install, sorted, given exactly those already present on PATH. Child stderr is
# left attached so a broken harness is loud instead of silently returning "".
offers_when() {
  local stub="$PROV_DIR/bin" p
  rm -rf "$stub"; mkdir -p "$stub"
  for p in "$@"; do
    printf '#!/bin/sh\necho 1.0.0\n' > "$stub/$p"; chmod +x "$stub/$p"
  done
  PATH="$stub" HOME="$PROV_DIR/home" PROV_DIR="$PROV_DIR" "$PROV_BASH" -c '
    set -u
    C_OK=; C_DIM=; C_WARN=; C_KEY=; NC=
    row() { :; }; note() { :; }; ok() { :; }; warn() { :; }
    version_of() { printf "%s" "${1:-}"; }
    version_ge() { return 0; }
    install_provider() { return 0; }
    ask() {
      case "$1" in
        Install*Claude*)   echo "OFFER claude" ;;
        Install*OpenCode*) echo "OFFER opencode" ;;
        Install*Codex*)    echo "OFFER codex" ;;
      esac
      return 1
    }
    . "$PROV_DIR/helpers.sh"
    . "$PROV_DIR/body.sh"
  ' | awk '/^OFFER /{print $2}' | sort | tr '\n' ' ' | sed 's/ *$//'
}

check "$(offers_when claude opencode)" "codex" \
  "codex offered when claude+opencode already present"
check "$(offers_when claude)" "codex opencode" \
  "opencode+codex offered when only claude present"
check "$(offers_when)" "claude codex opencode" \
  "all three offered on a bare machine"
check "$(offers_when claude opencode codex)" "" \
  "nothing offered when all three present"

rm -rf "$PROV_DIR"

# --- Summary ----------------------------------------------------------------
echo ""
echo "=== $pass passed, $fail failed ==="
[ "$fail" -eq 0 ]
