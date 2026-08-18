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

# --- 8. Popups actually open at the size muxcode asks for -------------------
# Sections 1-7 check the flags muxcode emits. This opens a real tmux popup with
# those exact flags and has it report its own interior geometry, which is the
# only check that fails if tmux disagrees with the computed size. capture-pane
# cannot be used here: a popup is not a pane, so it never appears in a capture.
if [ -n "${TMUX:-}" ] && tmux display-message -p '#S' >/dev/null 2>&1; then
  probe_out=$(mktemp /tmp/muxcode-popup-probe-XXXXXX)
  probe_sh=$(mktemp /tmp/muxcode-popup-probe-XXXXXX.sh)
  # stty reads the controlling tty, so it stays correct with stdout redirected;
  # tput would report the 80x24 no-tty fallback and pass against any size.
  printf '#!/usr/bin/env bash\nstty size < /dev/tty > %s 2>&1\n' "$probe_out" > "$probe_sh"
  chmod +x "$probe_sh"

  ARGS=()
  while IFS= read -r line; do ARGS+=("$line"); done < <("$MUX" popup switch-session --dry-run)
  # Read the requested size out of ARGS rather than re-invoking the resolver:
  # a second invocation could resolve differently (the client can be resized
  # between the two), and the test would then compare the popup it opened
  # against a size it never asked for.
  want_w=""; want_h=""
  for i in "${!ARGS[@]}"; do
    [ "${ARGS[$i]}" = "-w" ] && want_w="${ARGS[$((i + 1))]}"
    [ "${ARGS[$i]}" = "-h" ] && want_h="${ARGS[$((i + 1))]}"
  done
  unset "ARGS[$(( ${#ARGS[@]} - 1 ))]"
  tmux "${ARGS[@]}" "$probe_sh" >/dev/null 2>&1
  # Wait for the probe to report rather than sleeping a fixed interval. The
  # popup holds the client until its command exits, and a still-open popup
  # makes the next run of this script fail its own startup checks — a fixed
  # sleep raced that shut-down and failed roughly every second consecutive run.
  for _ in 1 2 3 4 5 6 7 8 9 10; do
    [ -s "$probe_out" ] && break
    sleep 0.5
  done

  got=$(cat "$probe_out" 2>/dev/null)
  got_rows=$(echo "$got" | awk '{print $1}')
  got_cols=$(echo "$got" | awk '{print $2}')
  rm -f "$probe_out" "$probe_sh"

  if [ -z "$got_cols" ]; then
    bad "popup did not report its geometry (got '$got')"
  else
    # tmux draws a one-column border on each side, so the interior is 2 smaller.
    if [ "$got_cols" -eq "$((want_w - 2))" ] && [ "$got_rows" -eq "$((want_h - 2))" ]; then
      ok "popup opened at the requested size (interior ${got_cols}x${got_rows} inside ${want_w}x${want_h})"
    else
      bad "popup interior ${got_cols}x${got_rows}, expected $((want_w - 2))x$((want_h - 2))"
    fi
  fi

  # The fit must leave room for the widest row plus fzf's pointer and scrollbar.
  cur=$(tmux display-message -p '#S')
  widest=$(tmux list-sessions -F '#{session_name}|#{session_windows}|#{session_created}|#{?session_attached,attached,}' 2>/dev/null |
    while IFS='|' read -r name windows created attached; do
      # Same fallback chain as the script: date -r is BSD/macOS, date -d is GNU.
      # Without it every row renders "unknown" on Linux, which is 5 columns
      # shorter than a real timestamp and would false-fail the width guard.
      created_fmt=$(date -r "$created" '+%b %d %H:%M' 2>/dev/null \
        || date -d "@$created" '+%b %d %H:%M' 2>/dev/null \
        || echo unknown)
      marker=""; [ "$name" = "$cur" ] && marker=" X"
      [ -n "$attached" ] && [ "$name" != "$cur" ] && marker=" X"
      printf "  %-20s  %2d windows  %s%s\n" "$name" "$windows" "$created_fmt" "$marker"
    done | awk '{ if (length($0) > m) m = length($0) } END { print m + 0 }')
  if [ "${widest:-0}" -gt 0 ] && [ -n "$got_cols" ]; then
    if [ "$got_cols" -ge "$((widest + 3))" ]; then
      ok "interior ${got_cols} fits widest row ${widest} plus pointer and scrollbar"
    else
      bad "interior ${got_cols} too narrow for widest row ${widest} plus 3 columns of fzf gutter"
    fi

    # Regression guard for the shipped bug: switch-session had no measurer and
    # fell through to the cap, opening at 160 columns for ~50 columns of rows.
    # The checks above all passed in that state - they compare the popup against
    # what muxcode asked for, and asking for the cap is self-consistent. This is
    # the one that fails, because it compares the request against the content.
    if [ "$want_w" -le "$((widest + 20))" ]; then
      ok "switch-session is sized from its content (${want_w}) not the cap"
    else
      bad "switch-session width ${want_w} far exceeds its ${widest}-column content — measurer bypassed?"
    fi
  fi
else
  echo "  SKIP  popup geometry check needs a live tmux client"
fi

# --- 9. Every popup AND modal resolves to a real size -----------------------
# The general form of the bug this suite exists for. A popup or modal with
# neither a measurer nor the cap tier falls through to its configured
# percentage, which tmux expands against the whole terminal - 50% of a 317
# column client is 158 columns of frame around 60 columns of content. The
# provider modal shipped exactly that way and every check above still passed,
# because they all asserted things about popups only.
#
# With a client size known, nothing should resolve to a percentage.
inventory=""
for n in $("$MUX" popup 2>/dev/null | awk '/^  [a-z][a-z-]+ /{print $1}'); do
  inventory="$inventory popup:$n"
done
for n in $("$MUX" modal list 2>/dev/null | awk 'NR>2 && /^[a-z]/{print $1}'); do
  inventory="$inventory modal:$n"
done

pct=0
counted=0
for entry in $inventory; do
  kind="${entry%%:*}"; name="${entry#*:}"
  if [ "$kind" = "popup" ]; then
    dims=$(env $WIDE "$MUX" popup "$name" --dry-run 2>/dev/null)
  else
    dims=$(env $WIDE "$MUX" modal open "$name" --dry-run 2>/dev/null)
  fi
  w=$(echo "$dims" | grep -A1 -x -- '-w' | tail -1)
  h=$(echo "$dims" | grep -A1 -x -- '-h' | tail -1)
  counted=$((counted + 1))
  case "$w$h" in
    *%*)
      bad "$kind '$name' resolves to a percentage (${w}x${h}) — needs a Measurer or AutoCap"
      pct=1
      ;;
  esac
  # Nothing may exceed the client it is drawn on.
  if [ "${w%\%}" = "$w" ] && [ "$w" -gt 317 ]; then
    bad "$kind '$name' width $w exceeds the 317-column client"
    pct=1
  fi
done

if [ "$counted" -lt 5 ]; then
  bad "inventory only found $counted popups/modals — enumeration is broken, not clean"
elif [ "$pct" -eq 0 ]; then
  ok "all $counted popups and modals resolve to absolute sizes within the client"
fi

echo ""
echo "=== $pass passed, $fail failed ==="
[ "$fail" -eq 0 ]
