#!/usr/bin/env bash
# MUX-235: measures whether integration scripts leave a tmux server running.
#
# Usage, from the repository root (TMUX set or unset):
#   bash scripts/measure-tmux-leak.sh [--tree DIR] [--signal INT|TERM --after SECS] NAME...
#   bash scripts/measure-tmux-leak.sh --self-test
# NAME is a script under scripts/ (`test-version` or `test-version.sh`). Each is
# run as `bash scripts/NAME.sh` from DIR (default: the current directory),
# serially. --signal sends SIG to the script SECS seconds in. A background job
# of a non-interactive shell starts with SIGINT ignored, which bash then refuses
# to trap, so every run is exec'd through a python3 shim restoring its default.
#
# Ownership is proved, never inferred from timing: each run exports a unique
# MEASURE_TMUX_RUN token, and a tmux server forks from its client without exec,
# so every server the run starts — directly or from a pane — carries the token
# in its environment (`ps -E`). Only such a server is the run's. Any other new
# server is reported `foreign` and never signalled: the user, or another worker,
# may have started it. The census is every process whose argv[0] is tmux —
# default socket, -L, -S, and TMUX_TMPDIR servers, which name no socket in argv.
# Owned servers are tied to their socket through lsof while the run is going
# and probed then for config/tmux.conf's kill-server alias, since a script that
# removes its work dir deletes the socket. An owned server alive 3 s after the
# run is a leak; it is ended by pid only after its token and start time are
# re-checked, so a recycled pid is never signalled.
#
# Output: one `RESULT` line per script; ends with
# `=== measure: scripts=N leaky=K clean=M ===`. Exit 1 when any script leaked.
# --self-test is the negative control: a fixture that leaks a `-L` and a `-S`
# server must have both flagged and ended, while a server started outside the
# run during it must be reported foreign and survive. It ends with `=== self-test: ... ===`.
set -uo pipefail

UID_N="$(id -u)"
STATE="$(mktemp -d)"
trap 'rm -rf "$STATE"' EXIT
SIGDFL='import os,signal,sys; signal.signal(signal.SIGINT, signal.SIG_DFL); os.execvp(sys.argv[1], sys.argv[1:])'

server_pids() { ps -axo pid=,ppid=,command= | awk '$2 == 1 && $3 ~ /(^|\/)tmux$/ {print $1}' | sort -n; }
socket_of() { lsof -a -p "$1" -U -F n 2>/dev/null | sed -n 's/^n\(\/.*\)/\1/p' | head -1; }
started_at() { local s; s=$(ps -o lstart= -p "$1" 2>/dev/null | tr -s ' ' '_'); echo "${s:--}"; }
owned_by() { ps -E -ww -o command= -p "$1" 2>/dev/null | grep -qE "MEASURE_TMUX_RUN=$2( |$)"; }
alias_state() {
  [ -n "$1" ] && [ -S "$1" ] || { echo alias-unknown; return; }
  env -u TMUX tmux -S "$1" show-options -s command-alias 2>/dev/null | grep -q 'kill-server=' \
    && echo alias-loaded || echo alias-absent
}

# note_new BEFORE SEEN TOKEN — records "pid owner start socket alias command"
# for each server not in BEFORE; owner is `owned` or `foreign`. A server whose
# socket lsof cannot resolve is still recorded, with socket `-`.
note_new() {
  local p sock owner
  for p in $(server_pids); do
    grep -qx "$p" "$1" && continue
    grep -q "^$p " "$2" && continue
    if owned_by "$p" "$3"; then owner=owned; else owner=foreign; fi
    sock=$(socket_of "$p")
    echo "$p $owner $(started_at "$p") ${sock:--} $(alias_state "$sock") $(ps -o command= -p "$p" 2>/dev/null | cut -c1-90)" >>"$2"
  done
}

# end_owned PID START TOKEN — TERM, then KILL, only while PID is still the
# server first seen (same start time) and still carries TOKEN.
end_owned() {
  local i
  same() { [ "$(started_at "$1")" = "$2" ] && owned_by "$1" "$3"; }
  same "$1" "$2" "$3" || return 0
  kill "$1" 2>/dev/null
  for i in 1 2 3 4; do kill -0 "$1" 2>/dev/null || return 0; sleep 0.5; done
  same "$1" "$2" "$3" && kill -KILL "$1" 2>/dev/null
  sleep 0.5
  ! kill -0 "$1" 2>/dev/null
}

# judge SEEN TOKEN — prints one line per recorded server and sets LEAKED and
# FOREIGN_ALIVE; owned survivors are ended, foreign ones only reported.
judge() {
  local pid owner start sock alias cmd work workstate ended
  LEAKED=0; FOREIGN_ALIVE=""
  while read -r pid owner start sock alias cmd; do
    [ -n "$pid" ] || continue
    work="${sock%/tmux-$UID_N/*}"; work="${work%/tmux}"
    if [ "$sock" = - ] || [ "$work" = "$sock" ] || [ "$work" = /private/tmp ] || [ "$work" = /tmp ]; then
      workstate=socket-outside-work-dir
    elif [ -d "$work" ]; then workstate=work-dir-present
    else workstate=work-dir-gone; fi
    if ! kill -0 "$pid" 2>/dev/null || [ "$(started_at "$pid")" != "$start" ]; then
      echo "    exited $owner pid=$pid socket=$sock $alias $workstate :: $cmd"
    elif [ "$owner" = foreign ]; then
      FOREIGN_ALIVE="$FOREIGN_ALIVE $pid"
      echo "    FOREIGN pid=$pid socket=$sock — not this run's, left running :: $cmd"
    else
      LEAKED=$((LEAKED + 1))
      ended=$(end_owned "$pid" "$start" "$2" && echo ended-by-pid || echo STILL-ALIVE)
      echo "    LEAK  pid=$pid socket=$sock $alias $workstate $ended :: $cmd"
    fi
  done <"$1"
}

# measure_one TREE NAME SIG AFTER [HOOK] — runs one script, judges it, sets
# RC, LEAKED, FOREIGN_ALIVE. HOOK, if given, runs once two seconds in.
measure_one() {
  local tree=$1 name=$2 sig=$3 after=$4 hook=${5:-} before seen out start tpid sent hooked secs summary token
  before="$STATE/$name.before"; seen="$STATE/$name.seen"; out="$STATE/$name.out"
  token="mux235-$$-$RANDOM-$(date +%s)"
  server_pids >"$before"; : >"$seen"
  start=$(date +%s)
  (cd "$tree" && MEASURE_TMUX_RUN="$token" exec python3 -c "$SIGDFL" bash "scripts/$name.sh") >"$out" 2>&1 &
  tpid=$!
  sent=""; hooked=""
  while kill -0 "$tpid" 2>/dev/null; do
    note_new "$before" "$seen" "$token"
    if [ -n "$hook" ] && [ -z "$hooked" ] && [ $(($(date +%s) - start)) -ge 2 ]; then "$hook"; hooked=1; fi
    if [ -n "$sig" ] && [ -z "$sent" ] && [ $(($(date +%s) - start)) -ge "$after" ]; then
      kill "-$sig" "$tpid" 2>/dev/null
      sent="$sig@$(($(date +%s) - start))s"
    fi
    sleep 0.5
  done
  wait "$tpid"; RC=$?
  secs=$(($(date +%s) - start))
  sleep 3
  note_new "$before" "$seen" "$token"
  summary=$(grep -E '^=== .*(passed|failed|PASS|FAIL)' "$out" | tail -1)
  [ -n "$summary" ] || summary=$(grep -v '^\s*$' "$out" | tail -1)
  echo "--- $name: exit $RC in ${secs}s${sent:+ (sent $sent)}"
  echo "    summary: $summary"
  judge "$seen" "$token"
  echo "RESULT $name exit=$RC servers_seen=$(wc -l <"$seen" | tr -d ' ') leaked=$LEAKED foreign=$(echo $FOREIGN_ALIVE | wc -w | tr -d ' ')"
}

start_foreign() {
  env -u TMUX -u MEASURE_TMUX_RUN tmux -L "$FOREIGN_SOCK" -f /dev/null new-session -d sleep 120
}

self_test() {
  local ok=0 bad=0 tree fpid
  pass() { echo "PASS: $*"; ok=$((ok + 1)); }
  fail() { echo "FAIL: $*"; bad=$((bad + 1)); }
  tree="$(cd "$STATE" && pwd -P)/tree"; mkdir -p "$tree/scripts"
  printf '%s\n' '#!/usr/bin/env bash' \
    'env -u TMUX tmux -L "leakfix-$$" -f /dev/null new-session -d sleep 120' \
    "env -u TMUX tmux -S '$tree/leakfix-S.sock' -f /dev/null new-session -d sleep 120" \
    'sleep 5' 'echo "=== fixture: 1 passed, 0 failed ==="' >"$tree/scripts/test-leakfixture.sh"
  FOREIGN_SOCK="mux235-foreign-$$"
  measure_one "$tree" test-leakfixture "" 0 start_foreign >"$STATE/selftest.out"
  cat "$STATE/selftest.out"
  fpid=$(env -u TMUX tmux -L "$FOREIGN_SOCK" list-sessions -F '#{pid}' 2>/dev/null | head -1)
  [ "$LEAKED" -eq 2 ] && pass "owned -L and -S leaked servers both flagged" || fail "owned leaks flagged: $LEAKED, want 2"
  grep -q "LEAK .*socket=$tree/leakfix-S.sock " "$STATE/selftest.out" \
    && pass "the -S leak is reported with its socket" || fail "no LEAK line for the -S socket"
  server_pids | while read -r p; do ps -o command= -p "$p"; done | grep -q "leakfix-" \
    && fail "an owned -L leaked server still running" || pass "owned -L leaked server ended by pid"
  server_pids | while read -r p; do ps -o command= -p "$p"; done | grep -q "leakfix-S.sock" \
    && fail "the owned -S leaked server still running" || pass "owned -S leaked server ended by pid"
  [ -n "$fpid" ] && kill -0 "$fpid" 2>/dev/null && pass "foreign server started during the run survived" \
    || fail "foreign server was killed"
  case " $FOREIGN_ALIVE " in *" $fpid "*) pass "foreign server reported foreign" ;; *) fail "foreign server not reported" ;; esac
  [ -n "$fpid" ] && kill "$fpid" 2>/dev/null
  echo "=== self-test: passed=$ok failed=$bad (TMUX $([ -n "${TMUX-}" ] && echo set || echo unset)) ==="
  [ "$bad" -eq 0 ]
}

TREE="$PWD"; SIG=""; AFTER=0
while [ $# -gt 0 ]; do
  case "$1" in
    --self-test) self_test; exit $? ;;
    --tree) TREE="$2"; shift 2 ;;
    --signal) SIG="$2"; shift 2 ;;
    --after) AFTER="$2"; shift 2 ;;
    -*) echo "unknown option $1" >&2; exit 2 ;;
    *) break ;;
  esac
done
[ $# -gt 0 ] || { echo "usage: $0 [--tree DIR] [--signal INT|TERM --after SECS] NAME... | --self-test" >&2; exit 2; }
[ -d "$TREE/scripts" ] || { echo "no scripts/ under $TREE" >&2; exit 2; }

LEAKY=0; CLEAN=0; N=0
for name in "$@"; do
  name="${name%.sh}"; N=$((N + 1))
  measure_one "$TREE" "$name" "$SIG" "$AFTER"
  [ "$LEAKED" -gt 0 ] && LEAKY=$((LEAKY + 1)) || CLEAN=$((CLEAN + 1))
done

echo "=== measure: scripts=$N leaky=$LEAKY clean=$CLEAN ==="
[ "$LEAKY" -eq 0 ]
