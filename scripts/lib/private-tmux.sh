# private-tmux.sh — run an integration script's tmux on a private server (MUX-233).
#
# A scratch session on the caller's own server shows in their session list,
# hooks and status line, and its panes inherit that server's environment — the
# real HOME included — so a pane-side process can write outside the script's
# scratch directories (MUX-226 measured a lifecycle log left in the real
# ~/.config/muxcode/logs). On a private server the panes inherit the script's
# own environment instead.
#
# Two rules, each learned by nearly killing the caller's server on 2026-11-17:
#   - call private_tmux_init BEFORE any trap is armed, so no cleanup can run
#     while TMUX_TMPDIR still points at the caller's server;
#   - kill by socket path, only if that socket exists: a bare `tmux
#     kill-server` falls back to the caller's socket once TMUX_TMPDIR names a
#     directory that is gone.
# And `kill-server` itself cannot be used: config/tmux.conf aliases it to a
# confirm-before prompt, which fails "no current client" on a server nothing is
# attached to — so a script whose HOME loads that config left its private
# server running after every run. The server is signalled by its pid instead.
#
# Usage, before the script's own `trap cleanup EXIT`:
#   . "$(dirname "${BASH_SOURCE[0]}")/lib/private-tmux.sh"
#   private_tmux_init "$WORK/tmux"
# and in cleanup, before the work directory is removed:
#   private_tmux_kill

# private_tmux_init DIR — unset TMUX, export TMUX_TMPDIR=DIR (created), record
# PRIVATE_TMUX_SOCK, and make INT and TERM exit so an EXIT trap still runs.
private_tmux_init() {
  unset TMUX TMUX_PANE
  export TMUX_TMPDIR="$1"
  mkdir -p "$TMUX_TMPDIR"
  PRIVATE_TMUX_SOCK="$TMUX_TMPDIR/tmux-$(id -u)/default"
  trap 'exit 130' INT
  trap 'exit 143' TERM
}

# private_tmux_kill [SOCKET] — stop the server on SOCKET (default
# PRIVATE_TMUX_SOCK; a `tmux -L NAME` server started after private_tmux_init
# is "$TMUX_TMPDIR/tmux-$(id -u)/NAME"), if that socket exists: TERM to the pid
# it reports, KILL if it is still up 2 s later. Never fails, so a caller's
# `set -e` cannot abort the EXIT trap before it removes the bus dir.
private_tmux_kill() {
  local sock="${1:-${PRIVATE_TMUX_SOCK:-}}" pid i
  [ -n "$sock" ] && [ -S "$sock" ] || return 0
  pid="$(tmux -S "$sock" list-sessions -F '#{pid}' 2>/dev/null | head -1)" || true
  [ -n "$pid" ] || return 0
  kill "$pid" 2>/dev/null || true
  for i in 1 2 3 4; do
    kill -0 "$pid" 2>/dev/null || return 0
    sleep 0.5
  done
  kill -KILL "$pid" 2>/dev/null || true
  return 0
}
