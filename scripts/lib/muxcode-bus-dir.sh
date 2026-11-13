#!/usr/bin/env bash
# muxcode_bus_dir — resolve a session's bus directory through the binary
# instead of hard-coding where it lives (MUX-208 moved it out of /tmp).
#
#   . "$(dirname "${BASH_SOURCE[0]}")/lib/muxcode-bus-dir.sh"
#   BD="$(muxcode_bus_dir "$MUX" "$SESSION")" || exit 1
#
# Arguments: <binary> <session>. Prints the absolute directory and returns 0,
# or prints an error to stderr and returns 1 when the binary fails or prints
# anything but one absolute path — a binary that predates `muxcode bus-dir`
# routes the word to the launcher, and an empty result must never reach an
# `rm -rf "$BD/..."` cleanup.
muxcode_bus_dir() {
  local bin=$1 session=$2 out
  out=$("$bin" bus-dir "$session" 2>/dev/null) || out=""
  case $out in
    /*)
      case $out in
        *$'\n'*) out="" ;;
      esac ;;
    *) out="" ;;
  esac
  if [ -z "$out" ]; then
    echo "MUX-208: '$bin' did not print an absolute path for 'bus-dir $session' — the binary predates 'muxcode bus-dir', run ./build.sh" >&2
    return 1
  fi
  printf '%s\n' "$out"
}
