#!/usr/bin/env bash
# Integration test for modal auto-sizing.
#
# Drives the real `muxcode popup --dry-run` resolver and asserts the arguments
# that reach tmux. MUXCODE_MODAL_CLIENT_SIZE stands in for an attached client,
# so the assertions hold on any terminal and in CI.
set -uo pipefail

# Resolved to an absolute path up front: section 6 wipes PATH to force the
# no-tmux fallback, and a bare command name would become unfindable along with
# tmux — the test would then measure its own broken invocation.
MUX="${MUXCODE_BIN:-muxcode}"
MUX="$(command -v "$MUX" 2>/dev/null || echo "$MUX")"
if [ ! -x "$MUX" ]; then
  echo "  FAIL  cannot resolve muxcode binary ('$MUX') — set MUXCODE_BIN"
  exit 1
fi
pass=0; fail=0
ok()  { echo "  PASS  $*"; pass=$((pass + 1)); }
bad() { echo "  FAIL  $*"; fail=$((fail + 1)); }

echo "=== modal auto-size integration test ==="

# Fail fast on a muxcode without the popup subcommand — usually an installed
# binary predating this feature. Without this the suite reports a dozen
# "unregistered popup" failures, which points at tmux.conf rather than at the
# stale binary actually responsible.
if ! "$MUX" popup 2>/dev/null | grep -q 'Registered popups:'; then
  echo "  FAIL  '$MUX' has no 'popup' subcommand — rebuild/install muxcode, or set MUXCODE_BIN"
  echo ""
  echo "=== 0 passed, 1 failed ==="
  exit 1
fi

# width_of <popup> [env assignments...] — echoes the -w value passed to tmux.
width_of() {
  local name="$1"; shift
  env "$@" "$MUX" popup "$name" --dry-run 2>/dev/null | grep -A1 -x -- '-w' | tail -1
}
height_of() {
  local name="$1"; shift
  env "$@" "$MUX" popup "$name" --dry-run 2>/dev/null | grep -A1 -x -- '-h' | tail -1
}

WIDE="MUXCODE_MODAL_CLIENT_SIZE=317x80"

# --- 1. The reported bug: fit sizing, not a percentage of a wide client -----
w=$(width_of session-picker "$WIDE")
if [[ "$w" =~ ^[0-9]+$ ]]; then
  ok "session picker resolves to absolute columns ($w), not a percentage"
else
  bad "session picker width is not absolute: '$w'"
fi
if [[ "$w" =~ ^[0-9]+$ ]] && [ "$w" -lt 190 ]; then
  ok "session picker ($w) is narrower than the old 60% of 317 (190)"
else
  bad "session picker did not improve on 190 columns (got '$w')"
fi

# --- 2. The cap holds on a wide client --------------------------------------
capped=0
for name in $("$MUX" popup 2>/dev/null | awk '/^  [a-z][a-z-]+ /{print $1}'); do
  [ -n "$name" ] || continue
  w=$(width_of "$name" "$WIDE")
  [[ "$w" =~ ^[0-9]+$ ]] || continue
  if [ "$w" -gt 160 ]; then
    bad "$name width $w exceeds the 160-column cap"
    capped=1
  fi
done
[ "$capped" -eq 0 ] && ok "no popup exceeds the 160-column cap at 317 columns"

# --- 3. Never wider than the client on a narrow one -------------------------
NARROW="MUXCODE_MODAL_CLIENT_SIZE=40x12"
over=0
for name in $("$MUX" popup 2>/dev/null | awk '/^  [a-z][a-z-]+ /{print $1}'); do
  [ -n "$name" ] || continue
  w=$(width_of "$name" "$NARROW"); h=$(height_of "$name" "$NARROW")
  [[ "$w" =~ ^[0-9]+$ ]] || continue
  if [ "$w" -gt 40 ] || { [[ "$h" =~ ^[0-9]+$ ]] && [ "$h" -gt 12 ]; }; then
    bad "$name ${w}x${h} exceeds the 40x12 client"
    over=1
  fi
done
[ "$over" -eq 0 ] && ok "no popup exceeds a 40x12 client"

# --- 4. An explicit env size still outranks auto-fit ------------------------
w=$(width_of session-picker "$WIDE" MUXCODE_MODAL_SIZE_SESSION_PICKER=90%x80%)
if [ "$w" = "90%" ]; then
  ok "MUXCODE_MODAL_SIZE_<NAME> overrides auto-fit"
else
  bad "env override ignored (got '$w', want 90%)"
fi

# --- 5. Width is never smaller than the popup title -------------------------
# " Provider Selector " is 19 visible columns, so its floor is 21.
w=$(width_of remote-sessions "MUXCODE_MODAL_CLIENT_SIZE=317x80" MUXCODE_MODAL_MIN_COLS=1)
title=$(env MUXCODE_MODAL_CLIENT_SIZE=317x80 "$MUX" popup remote-sessions --dry-run 2>/dev/null | grep -A1 -x -- '-T' | tail -1)
if [[ "$w" =~ ^[0-9]+$ ]] && [ "$w" -ge $(( ${#title} + 2 )) ]; then
  ok "width $w accommodates the title (${#title} visible + 2 corners)"
else
  bad "width $w is narrower than the title floor ($(( ${#title} + 2 )))"
fi

# --- 6. Unknown client falls back to the configured percentage --------------
notmux=$(mktemp -d)
w=$(env -u MUXCODE_MODAL_CLIENT_SIZE PATH="$notmux" "$MUX" popup edit-config --dry-run 2>/dev/null |
    grep -A1 -x -- '-w' | tail -1)
rmdir "$notmux" 2>/dev/null
if [ "$w" = "80%" ]; then
  ok "unresolvable client falls back to the 80% default"
else
  bad "expected 80% fallback with no client, got '$w'"
fi

# --- 7. Every tmux.conf popup name is registered ----------------------------
missing=0
for name in $(grep -oE "muxcode popup [a-z-]+" config/tmux.conf | awk '{print $3}' | sort -u); do
  if ! "$MUX" popup 2>/dev/null | awk '/^  [a-z][a-z-]+ /{print $1}' | grep -qx "$name"; then
    bad "tmux.conf references unregistered popup '$name'"
    missing=1
  fi
done
[ "$missing" -eq 0 ] && ok "every popup named in tmux.conf is registered"

echo ""
echo "=== $pass passed, $fail failed ==="
[ "$fail" -eq 0 ]
