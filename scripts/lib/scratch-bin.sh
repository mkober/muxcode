# scratch-bin.sh — make every muxcode a script's workers resolve the binary under test (MUX-226, MUX-233).
#
# A graph spawn worker's launcher comes from findMuxcodeBinary: $HOME/.local/bin,
# then PATH. Under a scratch HOME that is the first muxcode on PATH — usually
# the installed one — so a run with MUXCODE_BIN=<tree build> would drive its
# workers with a different build from the one under test.
#
# Usage, once MUX is resolved and before the daemon or any tmux server starts:
#   . "$(dirname "${BASH_SOURCE[0]}")/lib/scratch-bin.sh"
#   scratch_bin_first "$WORK/bin"
# MUX is made absolute in place, linked as DIR/muxcode, and DIR leads PATH.

# scratch_bin_first DIR — link the (absolute) $MUX as DIR/muxcode and export
# PATH with DIR first.
scratch_bin_first() {
  case "$MUX" in
    /*) ;;
    */*) MUX="$(cd "$(dirname "$MUX")" && pwd)/$(basename "$MUX")" ;;
    *) MUX="$(command -v "$MUX")" ;;
  esac
  mkdir -p "$1"
  ln -sf "$MUX" "$1/muxcode"
  export PATH="$1:$PATH"
}
