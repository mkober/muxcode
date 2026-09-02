#!/usr/bin/env bash
# require_muxcode_version — the binary precondition shared by the integration
# scripts: assert the installed muxcode is at least the release that shipped
# the feature under test.
#
#   . "$(dirname "${BASH_SOURCE[0]}")/lib/muxcode-version.sh"
#   require_muxcode_version "$MUX" v0.1.0 MUX-103 || exit 1
#
# Arguments: <binary> <minimum version> <feature id, named in the message>.
# Returns 0 when the binary is at or past the version and 1 when it is older
# or cannot be run, printing the binary's identity to stderr either way so
# the reader sees what was actually found.
#
# The check follows `muxcode version --at-least`, which exits 2 when the
# comparison cannot be decided: an untagged dev build reports a bare commit
# with no semver rank. That case returns 0 with a note rather than 1 — a
# tree-built binary is what every developer runs between tags, and refusing
# to test it would block the very loop that produced it. Once the build is
# tagged the comparison is real and a stale binary fails properly.
#
# A binary older than MUX-138 has no `version` verb at all and routes the
# word to the launcher as a project path, which exits 1 when that path does
# not exist — read here as "older", which it is.
require_muxcode_version() {
  local bin=$1 want=$2 feature=$3 rc=0 ident
  "$bin" version --at-least "$want" >/dev/null 2>&1 || rc=$?
  if [ "$rc" -eq 0 ]; then
    return 0
  fi
  ident=$("$bin" version 2>/dev/null || echo "$bin")
  case $rc in
    2)
      echo "note: $ident is untagged — cannot verify $want ($feature), continuing" >&2
      return 0 ;;
    1)
      echo "installed muxcode is older than $want ($feature): $ident — run ./build.sh" >&2
      return 1 ;;
    *)
      echo "cannot run '$bin version' (exit $rc) — set MUXCODE_BIN or run ./build.sh" >&2
      return 1 ;;
  esac
}
