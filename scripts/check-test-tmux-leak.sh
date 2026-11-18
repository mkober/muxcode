#!/usr/bin/env bash
# MUX-233: proves an integration script leaves no tmux server behind — default,
# -S or -L — and nothing on the real server or in the real lifecycle logs.
#
# Usage, from the repository root: bash scripts/check-test-tmux-leak.sh
#
# Runs scripts/test-send-keys-dash.sh as written, then a scratch copy whose
# probe-server cleanup is the pre-fix `tmux -L "$SOCK" kill-server`. The copy
# is the negative control: under a config that aliases kill-server to a confirm
# prompt (config/tmux.conf) it must leave its skdash-* server running, and this
# census must see it. A census blind to `tmux -L` servers once reported "no
# survivors" for exactly that leak. A leaked control server is ended by pid.
set -uo pipefail

TARGET="scripts/test-send-keys-dash.sh"
[ -f "$TARGET" ] || { echo "run from the repository root"; exit 2; }

tmux_servers() { ps -axo pid=,command= | awk '$2 ~ /(^|\/)tmux$/ {print}' | sort; }
real_sessions() { env -u TMUX tmux list-sessions -F '#{session_name}' 2>/dev/null | sort; }
LOGDIR="$HOME/.config/muxcode/logs"
real_logs() { ls -1 "$LOGDIR" 2>/dev/null | sort; }

OK=0; BAD=0
pass() { echo "PASS: $*"; OK=$((OK + 1)); }
fail() { echo "FAIL: $*"; BAD=$((BAD + 1)); }

# run_case <label> <dir> — runs `bash scripts/test-send-keys-dash.sh` from <dir>;
# sets RC, SEEN (real-server skdash sessions observed), NEWS (new tmux servers).
run_case() {
  local label=$1 dir=$2 out s0 r0 l0 tpid s
  s0=$(tmux_servers); r0=$(real_sessions); l0=$(real_logs)
  out=$(mktemp)
  (cd "$dir" && bash scripts/test-send-keys-dash.sh) >"$out" 2>&1 &
  tpid=$!
  SEEN=""
  while kill -0 "$tpid" 2>/dev/null; do
    s=$(real_sessions | grep -E 'skdash' || true)
    [ -n "$s" ] && SEEN="$SEEN $s"
    sleep 0.5
  done
  wait "$tpid"; RC=$?
  sleep 2
  echo "--- $label: script summary (exit $RC) ---"
  grep -E '^=== .*passed' "$out" || tail -5 "$out"
  NEWS=$(comm -13 <(echo "$s0") <(tmux_servers))
  NEWR=$(comm -13 <(echo "$r0") <(real_sessions))
  NEWL=$(comm -13 <(echo "$l0") <(real_logs))
  rm -f "$out"
}

CTL="skdash-census-ctl-$$"
env -u TMUX tmux -L "$CTL" -f /dev/null new-session -d -s probe 2>/dev/null
if tmux_servers | grep -q -- "-L $CTL"; then pass "census sees a tmux -L server"; else fail "census blind to a tmux -L server"; fi
ctlpid=$(env -u TMUX tmux -L "$CTL" list-sessions -F '#{pid}' 2>/dev/null | head -1)
[ -n "$ctlpid" ] && kill "$ctlpid" 2>/dev/null
sleep 1

run_case "fixed script" "$PWD"
[ "$RC" -eq 0 ] && pass "fixed: script exit 0" || fail "fixed: script exit $RC"
[ -z "$SEEN" ] && pass "fixed: no skdash session on the real server during the run" || fail "fixed: real server held:$SEEN"
[ -z "$NEWS" ] && pass "fixed: no tmux server process left (default or -L)" || { fail "fixed: tmux servers left:"; echo "$NEWS"; }
[ -z "$NEWR" ] && pass "fixed: real server sessions unchanged" || fail "fixed: new real sessions: $NEWR"
[ -z "$NEWL" ] && pass "fixed: real logs dir unchanged" || fail "fixed: new real logs: $NEWL"

W=$(mktemp -d)
mkdir -p "$W/scripts/lib"
cp scripts/lib/*.sh "$W/scripts/lib/"
sed 's|^  private_tmux_kill "\$TMUX_TMPDIR/tmux-\$(id -u)/\$SOCK"$|  tmux -L "$SOCK" kill-server 2>/dev/null \|\| true|' \
  "$TARGET" >"$W/$TARGET"
if grep -q 'tmux -L "$SOCK" kill-server' "$W/$TARGET"; then
  run_case "control (old kill-server probe cleanup)" "$W"
  leaked=$(echo "$NEWS" | grep -E -- '-L skdash-[0-9]+ ' || true)
  if [ -n "$leaked" ]; then
    pass "control: census detects the leaked probe server"
    echo "$leaked"
    echo "$leaked" | awk '{print $1}' | xargs kill 2>/dev/null
    sleep 1
    tmux_servers | grep -qE -- '-L skdash-[0-9]+ ' && fail "control: leaked server survived its kill" || pass "control: leaked server ended by pid"
  else
    fail "control: no leak detected (kill-server alias not loaded under this config?)"
  fi
else
  fail "control: could not rewrite the probe cleanup line"
fi
rm -rf "$W"

echo "=== checker: passed=$OK failed=$BAD ==="
[ "$BAD" -eq 0 ]
