#!/usr/bin/env bash
# Integration test for MUX-156: an orphaned `muxcode inbox --poll --loop`
# listener no longer consumes messages into output nobody reads.
#
# On 2026-09-08 and 2026-09-10 a listener whose parent had exited — reparented
# to launchd — kept the polling marker, so the Stop hook saw a live listener and
# relaunched none, then acked the role's next message into a dead pipe. Every
# section below runs the real CLI against a scratch bus. An orphan is made by
# starting a listener under a shell, SIGSTOPping it once it holds the marker,
# and letting the shell exit: a real reparented listener that cannot tick until
# SIGCONT, so the moment "only an orphan exists" can be observed.
#
#   A. Orphan: while only the stopped orphan holds the marker and a request is
#      pending, `muxcode hook stop` (the Stop hook's IsPolling reading) demands
#      a listener relaunch; on SIGCONT the orphan exits within one poll
#      interval with a `listener-orphan-exit` row naming its pid, releases the
#      marker, and leaves the request in the inbox with no receipt.
#   B. The next listener, with a live parent, consumes that request; its
#      receipt names that listener's pid and a live parent.
#   C. Negative control: a listener whose parent lives keeps running across
#      several ticks; stopped with a request pending it still counts, so the
#      Stop hook stays silent; resumed it consumes with a receipt naming it.
#   E. Negative control: the turn-boundary cycle as today — a healthy listener
#      SIGTERMed (exit 143, no handler) leaves its marker and sidecar stale; with
#      a request pending the Stop hook demands a relaunch; the replacement
#      claims the marker and rewrites the sidecar, keeps the hook silent, and
#      consumes with a receipt naming it, never the terminated listener; no
#      orphan row, and its clean exit clears both files.
#   D. Control: a muxcode built from a copy of this tree with the self-exit
#      (listenerOrphaned) and the IsPolling exclusion reverted fails A under
#      the same checks — the hook stays silent and the orphan consumes the
#      request, its receipt recording acked_parent_alive=false — so a green A
#      proves the checks can see the defect.
#
# The daemon reaper (Phase 3's first item, Phase 4's reap test) is deferred to
# MUX-184 and not exercised here; no daemon runs.
#
# ISOLATION: builds muxcode from the tree (MUXCODE_ORPHAN_LISTENER_BIN
# overrides the fixed binary); scratch HOME, XDG_STATE_HOME, lifecycle log,
# memory and config, one BUS_SESSION per section; no daemon and no tmux server
# (TMUX unset, TMUX_TMPDIR in the work dir). Every listener started is killed
# on exit. build is pinned to Claude (MUXCODE_BUILD_CLI): only a self-polling
# provider's Stop hook reads IsPolling, and build's default, OpenCode, returns
# before it — which would leave every hook check silent and C's and D's
# vacuously green.
#
# REQUIRES: go. ~1 min, mostly the two builds and the listeners' 2 s ticks.
#
# Usage: bash scripts/test-orphan-listener.sh
#        MUXCODE_TEST_KEEP=1 bash scripts/test-orphan-listener.sh   # keep WORK
set -uo pipefail

FLOOR=34
REPO_ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)

GREEN=$'\033[0;32m'; RED=$'\033[0;31m'; NC=$'\033[0m'
pass=0; fail=0
ok()  { echo "  ${GREEN}PASS${NC}  $*"; pass=$((pass + 1)); }
bad() { echo "  ${RED}FAIL${NC}  $*"; fail=$((fail + 1)); }

WORK=$(mktemp -d "${TMPDIR:-/tmp}/orphan-listener-XXXXXX")
PIDS=()
cleanup() {
  local p
  for p in "${PIDS[@]:-}"; do [ -n "$p" ] && kill -CONT "$p" 2>/dev/null; [ -n "$p" ] && kill -KILL "$p" 2>/dev/null; done
  if [ "${MUXCODE_TEST_KEEP:-0}" = "1" ]; then echo "  (kept for diagnosis: $WORK)"; else rm -rf "$WORK"; fi
}
trap cleanup EXIT

command -v go >/dev/null 2>&1 || { echo "  FAIL  go is required"; exit 1; }
GOCACHE="$(go env GOCACHE)"; GOPATH="$(go env GOPATH)"
export GOCACHE GOPATH

# --- Isolation -------------------------------------------------------------
export HOME="$WORK/home"
export XDG_STATE_HOME="$HOME/.local/state"
export TMUX_TMPDIR="$WORK/tmux"
mkdir -p "$HOME/.config/muxcode" "$WORK/lifecycle" "$TMUX_TMPDIR"
: > "$WORK/empty-config"
export MUXCODE_CONFIG="$WORK/empty-config"
export MUXCODE_CONFIG_DIR="$WORK/config-dir"
export MUXCODE_LIFECYCLE_LOG_DIR="$WORK/lifecycle"
export BUS_MEMORY_DIR="$WORK/memory"
export BUS_GLOBAL_MEMORY_DIR="$WORK/global-memory"
export MUXCODE_DEDUP_WINDOW=0
export MUXCODE_RELAY_SUPPRESS_THRESHOLD=0
export MUXCODE_BUILD_CLI=claude
unset TMUX TMUX_PANE MUXCODE_BUS_BASE MUXCODE_BUS_DIR AGENT_ROLE BUS_ROLE BUS_SESSION \
  MUXCODE_DELIVERY_ACK_DISABLE MUXCODE_DELIVERY_ACK MUXCODE_AGENT_CLI
mkdir -p "$MUXCODE_CONFIG_DIR"

# --- Binaries --------------------------------------------------------------
echo "--- binaries"
if [ -n "${MUXCODE_ORPHAN_LISTENER_BIN:-}" ]; then
  MUX="$MUXCODE_ORPHAN_LISTENER_BIN"; ok "fixed binary from MUXCODE_ORPHAN_LISTENER_BIN: $MUX"
else
  MUX="$WORK/muxcode"
  (cd "$REPO_ROOT/tools/muxcode" && go build -buildvcs=false -o "$MUX" .) >"$WORK/build.log" 2>&1 \
    && ok "built muxcode from the tree" || { bad "go build failed: $(tail -3 "$WORK/build.log")"; exit 1; }
fi

CTL_TREE="$WORK/ctl-tree"
cp -R "$REPO_ROOT/tools/muxcode" "$CTL_TREE"
perl -pi -e 's/return now != parent \|\| now == 1/return false/' "$CTL_TREE/cmd/inbox.go"
perl -pi -e 's/return !pollerOrphaned\(session, role, pid\)/return true/' "$CTL_TREE/bus/notify.go"
CTL="$WORK/muxcode-prefix"
if ! grep -q 'return now != parent' "$CTL_TREE/cmd/inbox.go" && ! grep -q 'return !pollerOrphaned' "$CTL_TREE/bus/notify.go" \
  && (cd "$CTL_TREE" && go build -buildvcs=false -o "$CTL" .) >"$WORK/ctl-build.log" 2>&1; then
  ok "built the control: self-exit and IsPolling exclusion reverted"
else
  bad "control build or revert failed: $(tail -3 "$WORK/ctl-build.log" 2>/dev/null)"
fi

# --- Helpers ---------------------------------------------------------------
bus_dir() { "$MUX" bus-dir "$1"; }
init_session() { BUS_SESSION="$1" "$MUX" init --memory-dir "$WORK/memory-$1" >/dev/null 2>&1; }
alive() { kill -0 "$1" 2>/dev/null; }
# wait_until <tenths> <cmd...> — polls cmd every 0.1 s; status 0 once it holds.
wait_until() {
  local n=$1 i; shift
  for ((i = 0; i < n; i++)); do "$@" && return 0; sleep 0.1; done
  return 1
}
# request <bin> <session> <payload> — edit→build request; prints its id.
request() {
  BUS_SESSION="$2" AGENT_ROLE=edit "$1" send build build "$3" --no-notify >/dev/null 2>&1
  grep -F "\"payload\":\"$3\"" "$(bus_dir "$2")/inbox/build.jsonl" 2>/dev/null | head -1 \
    | sed -E 's/.*"id":"([^"]+)".*/\1/'
}
# stop_hook <bin> <session> — the Stop hook's verdict for build; prints its output.
stop_hook() { echo '{}' | BUS_SESSION="$2" AGENT_ROLE=build "$1" hook stop 2>&1; }
receipt() { cat "$(bus_dir "$1")/delivery/$2.status" 2>/dev/null; }
in_inbox() { grep -qF "\"id\":\"$2\"" "$(bus_dir "$1")/inbox/build.jsonl" 2>/dev/null; }
orphan_rows() { grep -rhF "listener-orphan-exit" "$WORK/lifecycle" 2>/dev/null | grep -cF "pid $1:" ; }
marker_is() { [ "$(cat "$(bus_dir "$1")/polling-build.marker" 2>/dev/null)" = "$2" ]; }
no_marker() { [ ! -e "$(bus_dir "$1")/polling-build.marker" ]; }

# start_orphan <bin> <session> — a listener under a shell; once it holds the
# marker it is SIGSTOPped and the shell exits. Sets OPID, OSH.
start_orphan() {
  local bin=$1 s=$2 d="$WORK/orphan-$2"
  mkdir -p "$d"
  BUS_SESSION="$s" AGENT_ROLE=build sh -c \
    'echo $$ >"$2"; "$0" inbox --poll --loop >/dev/null 2>&1 & echo $! >"$1"; while [ ! -e "$3" ]; do sleep 0.1; done' \
    "$bin" "$d/pid" "$d/sh" "$d/go" &
  local shjob=$!
  wait_until 50 test -s "$d/pid"; OPID=$(cat "$d/pid" 2>/dev/null); OSH=$(cat "$d/sh" 2>/dev/null)
  PIDS+=("$OPID")
  wait_until 50 marker_is "$s" "$OPID"
  wait_until 20 test -s "$(bus_dir "$s")/polling-build.parent"
  kill -STOP "$OPID" 2>/dev/null
  touch "$d/go"; wait "$shjob" 2>/dev/null
}

# orphan_scenario <bin> <session> — section A; sets A_* for the caller to judge.
orphan_scenario() {
  local bin=$1 s=$2 t0 t1
  init_session "$s"
  start_orphan "$bin" "$s"
  A_SIDECAR=$(cat "$(bus_dir "$s")/polling-build.parent" 2>/dev/null)
  A_SH_GONE=no; alive "$OSH" || A_SH_GONE=yes
  A_ALIVE=no; alive "$OPID" && A_ALIVE=yes
  A_PPID=$(ps -o ppid= -p "$OPID" 2>/dev/null | tr -d ' ')
  A_ID=$(request "$bin" "$s" "orphan window build")
  A_HOOK=$(stop_hook "$bin" "$s")
  A_MARKER=no; marker_is "$s" "$OPID" && A_MARKER=yes
  t0=$(date +%s)
  kill -CONT "$OPID" 2>/dev/null
  A_EXITED=no; wait_until 60 eval '! alive "$OPID"' && A_EXITED=yes
  t1=$(date +%s); A_SECS=$((t1 - t0))
  A_ROWS=$(orphan_rows "$OPID")
  A_RELEASED=no; no_marker "$s" && A_RELEASED=yes
  A_KEPT=no; in_inbox "$s" "$A_ID" && A_KEPT=yes
  A_RECEIPT=$(receipt "$s" "$A_ID")
}

# --- A. Orphan --------------------------------------------------------------
echo "--- A. an orphaned listener neither counts nor consumes"
SA="orphan-a-$$"
orphan_scenario "$MUX" "$SA"
[ "$A_SIDECAR" = "$OPID $OSH" ] && ok "listener $OPID claimed the marker, sidecar records its shell $OSH" \
  || bad "sidecar '$A_SIDECAR', want '$OPID $OSH'"
[ "$A_SH_GONE" = yes ] && [ "$A_ALIVE" = yes ] && [ -n "$A_PPID" ] && [ "$A_PPID" != "$OSH" ] \
  && ok "orphaned: shell gone, listener alive, reparented to ${A_PPID}$([ "$A_PPID" = 1 ] || echo ' (a subreaper)')" \
  || bad "orphan setup: shell gone=$A_SH_GONE, listener alive=$A_ALIVE, ppid=${A_PPID:-?}"
[ -n "$A_ID" ] && ok "a request to build is pending ($A_ID)" || bad "the request never reached build's inbox"
grep -q "inbox listener is not running" <<<"$A_HOOK" \
  && ok "Stop hook demands a relaunch while only the orphan holds the marker (IsPolling false)" \
  || bad "Stop hook with only an orphan: '${A_HOOK:-<silent>}'"
[ "$A_MARKER" = yes ] && ok "the orphan's marker was left in place for it to clear" || bad "the marker no longer names $OPID before SIGCONT"
[ "$A_EXITED" = yes ] && [ "$A_SECS" -le 4 ] && ok "resumed, the orphan exited within one poll interval (${A_SECS}s)" \
  || bad "orphan exited=$A_EXITED after ${A_SECS}s"
[ "$A_ROWS" = 1 ] && ok "one listener-orphan-exit row names pid $OPID" || bad "$A_ROWS listener-orphan-exit rows name pid $OPID"
[ "$A_RELEASED" = yes ] && ok "the orphan released the polling marker" || bad "the polling marker survived the orphan's exit"
[ "$A_KEPT" = yes ] && ok "the request stayed in build's inbox" || bad "the orphan consumed the request"
grep -q '"acked_at":[1-9]' <<<"$A_RECEIPT" && bad "the orphan wrote a receipt: $A_RECEIPT" || ok "no receipt was written for it"

# --- B. The next listener consumes -----------------------------------------
echo "--- B. a listener with a live parent consumes the orphan's leftover"
BUS_SESSION="$SA" AGENT_ROLE=build "$MUX" inbox --poll --loop >"$WORK/b.out" 2>&1 &
BPID=$!; PIDS+=("$BPID")
wait_until 80 eval '! alive "$BPID"' && ok "listener $BPID returned with a batch" || bad "listener $BPID never returned"
grep -qF "orphan window build" "$WORK/b.out" && ok "it printed the request to its reader" || bad "output: $(head -3 "$WORK/b.out")"
R=$(receipt "$SA" "$A_ID")
grep -q '"acked_by":"build"' <<<"$R" && grep -q "\"acked_pid\":$BPID[,}]" <<<"$R" && grep -q '"acked_parent_alive":true' <<<"$R" \
  && ok "the receipt names listener $BPID with a live parent" || bad "receipt: ${R:-<none>}"
in_inbox "$SA" "$A_ID" && bad "the request is still in the inbox" || ok "the request left the inbox"

# --- C. Negative control: a live listener ----------------------------------
echo "--- C. a listener whose parent lives is unaffected"
SC="orphan-c-$$"; init_session "$SC"
BUS_SESSION="$SC" AGENT_ROLE=build "$MUX" inbox --poll --loop >"$WORK/c.out" 2>&1 &
CPID=$!; PIDS+=("$CPID")
wait_until 50 marker_is "$SC" "$CPID" && ok "listener $CPID claimed the marker" || bad "listener $CPID never claimed the marker"
sleep 7
alive "$CPID" && [ "$(orphan_rows "$CPID")" = 0 ] && ok "still running after three ticks, no orphan row" \
  || bad "the live listener exited by itself (orphan rows: $(orphan_rows "$CPID"))"
kill -STOP "$CPID" 2>/dev/null
C_ID=$(request "$MUX" "$SC" "live window build")
C_HOOK=$(stop_hook "$MUX" "$SC")
[ -n "$C_ID" ] && [ -z "$C_HOOK" ] && ok "with a request pending the Stop hook stays silent (IsPolling true)" \
  || bad "Stop hook with a live listener: '${C_HOOK}' (request ${C_ID:-missing})"
kill -CONT "$CPID" 2>/dev/null
wait_until 60 eval '! alive "$CPID"' && grep -qF "live window build" "$WORK/c.out" \
  && ok "resumed, it consumed the request" || bad "the live listener did not consume: $(head -3 "$WORK/c.out")"
R=$(receipt "$SC" "$C_ID")
grep -q "\"acked_pid\":$CPID[,}]" <<<"$R" && grep -q '"acked_parent_alive":true' <<<"$R" \
  && ok "its receipt names $CPID with a live parent" || bad "receipt: ${R:-<none>}"

# --- E. Negative control: the turn-boundary SIGTERM/relaunch cycle ---------
echo "--- E. a turn-end SIGTERM and the Stop hook's relaunch behave as before"
SE="orphan-e-$$"; init_session "$SE"; BD_E=$(bus_dir "$SE")
BUS_SESSION="$SE" AGENT_ROLE=build "$MUX" inbox --poll --loop >"$WORK/e1.out" 2>&1 &
E1=$!; PIDS+=("$E1")
wait_until 50 marker_is "$SE" "$E1"; wait_until 20 test -s "$BD_E/polling-build.parent"
kill -TERM "$E1" 2>/dev/null; wait "$E1" 2>/dev/null; E1_RC=$?
[ "$E1_RC" = 143 ] && ok "listener $E1 terminated by SIGTERM and reaped (exit 143)" || bad "listener $E1 exit $E1_RC, want 143"
marker_is "$SE" "$E1" && [ "$(cat "$BD_E/polling-build.parent" 2>/dev/null)" = "$E1 $$" ] \
  && ok "its marker and sidecar are left stale, naming $E1" \
  || bad "after SIGTERM: marker '$(cat "$BD_E/polling-build.marker" 2>/dev/null)', sidecar '$(cat "$BD_E/polling-build.parent" 2>/dev/null)'"
E_ID=$(request "$MUX" "$SE" "relaunch window build")
grep -q "inbox listener is not running" <<<"$(stop_hook "$MUX" "$SE")" \
  && ok "with a request pending the Stop hook demands the relaunch" || bad "the Stop hook did not demand a relaunch after SIGTERM (request ${E_ID:-missing})"
BUS_SESSION="$SE" AGENT_ROLE=build "$MUX" inbox --poll --loop >"$WORK/e2.out" 2>&1 &
E2=$!; PIDS+=("$E2")
wait_until 15 marker_is "$SE" "$E2" && wait_until 10 eval '[ "$(cat "$BD_E/polling-build.parent" 2>/dev/null)" = "$E2 $$" ]' \
  && ok "replacement $E2 claimed the marker and rewrote the stale sidecar" \
  || bad "replacement: marker '$(cat "$BD_E/polling-build.marker" 2>/dev/null)', sidecar '$(cat "$BD_E/polling-build.parent" 2>/dev/null)'"
kill -STOP "$E2" 2>/dev/null
E_HOOK=$(stop_hook "$MUX" "$SE")
kill -CONT "$E2" 2>/dev/null
[ -z "$E_HOOK" ] && ok "the replacement counts: the Stop hook stays silent" || bad "Stop hook with the replacement alive: '$E_HOOK'"
wait "$E2" 2>/dev/null; E2_RC=$?
[ "$E2_RC" = 0 ] && grep -qF "relaunch window build" "$WORK/e2.out" && ! grep -qF "relaunch window build" "$WORK/e1.out" \
  && ok "the replacement consumed the request, exit 0; the terminated listener printed nothing" \
  || bad "replacement exit $E2_RC: $(head -3 "$WORK/e2.out")"
R=$(receipt "$SE" "$E_ID")
grep -q "\"acked_pid\":$E2[,}]" <<<"$R" && grep -q '"acked_parent_alive":true' <<<"$R" \
  && ok "the receipt names the replacement $E2 with a live parent, never $E1" || bad "receipt: ${R:-<none>}"
[ "$(orphan_rows "$E1")" = 0 ] && [ "$(orphan_rows "$E2")" = 0 ] && ok "neither listener logged listener-orphan-exit" \
  || bad "orphan rows: $E1=$(orphan_rows "$E1"), $E2=$(orphan_rows "$E2")"
no_marker "$SE" && [ ! -e "$BD_E/polling-build.parent" ] && ok "the replacement's clean exit cleared marker and sidecar" \
  || bad "after the replacement's exit: marker '$(cat "$BD_E/polling-build.marker" 2>/dev/null)', sidecar '$(cat "$BD_E/polling-build.parent" 2>/dev/null)'"

# --- D. Control: the defect reverted ----------------------------------------
echo "--- D. control: a build without the self-exit and the IsPolling exclusion"
if [ -x "$CTL" ]; then
  orphan_scenario "$CTL" "orphan-d-$$"
  [ "$A_ALIVE" = yes ] && [ "$A_SH_GONE" = yes ] && ok "control orphan $OPID set up the same way" \
    || bad "control orphan setup: shell gone=$A_SH_GONE, alive=$A_ALIVE"
  [ -z "$A_HOOK" ] && ok "control: the Stop hook trusts the orphan and stays silent" \
    || bad "control: Stop hook '$A_HOOK' — A's hook check could not see the defect"
  [ "$A_KEPT" = no ] && grep -q "\"acked_pid\":$OPID[,}]" <<<"$A_RECEIPT" \
    && ok "control: the orphan consumed the request into /dev/null" || bad "control: request kept=$A_KEPT, receipt ${A_RECEIPT:-<none>}"
  grep -q '"acked_parent_alive":false' <<<"$A_RECEIPT" \
    && ok "control: its receipt records the consumer's parent gone" || bad "control: receipt ${A_RECEIPT:-<none>}"
else
  bad "control binary missing — section D skipped"
fi

# --- Summary ----------------------------------------------------------------
total=$((pass + fail))
echo
echo "=== test-orphan-listener: $pass passed, $fail failed ($total checks, floor $FLOOR) ==="
if [ "$total" -lt "$FLOOR" ]; then
  echo "  ${RED}FAIL${NC}  only $total checks ran (floor $FLOOR) — a section was skipped"
  exit 1
fi
[ "$fail" = 0 ]
