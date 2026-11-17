#!/usr/bin/env bash
# Integration test for MUX-184: a dead session's processes are reaped, on
# proof, before its files are removed — and nothing else is.
#
# A tree-built muxcode on a private tmux server. Session fixtures run a real
# `muxcode watch` daemon and monitor, a real `muxcode inbox --poll --loop`
# listener under a live parent, and a stand-in harness the daemon starts that
# takes its owner lock exactly as tools/muxcode-llm-harness does (the real one
# exits at once with no model backend).
#
#   A. Dead-session sweep (`muxcode cleanup`): daemon, monitor, listener and
#      harness of a killed session gone, an orphan-reaped row naming each, its
#      bus dir removed and still absent after twice the daemon poll.
#   B. Negative control — a live session through the same sweep: all four
#      processes and its bus dir survive.
#   C. Negative control — a dead session whose proc record names a sleeper
#      this script started: the sleeper survives, orphan-unproven, dir kept.
#   D. Launch sweep (`muxcode launch`, agents on the claude-stub): a dead
#      session's legacy `inbox-listener.sh` reaped on argv; the `while true`
#      loop form survives, orphan-unproven; the same script form under the
#      live session survives; an escabs server whose socket pid is dead is
#      reaped (escabs-reaped), one named for a live pid survives.
#   E. Opt-out: a launch under MUXCODE_ORPHAN_REAP=0 beside a dead session
#      reaps nothing and removes nothing.
#   F. Close hook: `tmux kill-session` on the launched session — its own bus
#      dir is gone with no manual command, its daemon with it.
#   G. ps unavailable: a ps that fails — the owner-lock road still reaps the
#      recorded families, orphan-census-unavailable is logged, and the
#      argv-only legacy listener is left.
#   H. Daemon children: TERM a daemon — its harness is gone and
#      daemon-stopped logged; an in-place upgrade-daemons re-exec keeps both
#      the daemon pid and its harness pid.
#   I. Order: a daemon killed while its monitor lives is relaunched (the
#      failure monitor-first prevents); monitor first, it is not.
#   J. Negative control first — `tmux rename-session` is not a closure: the
#      daemon's session-gone check judges by tmux session id, so the renamed
#      session's daemon, harness, proc, webhook holder and spawn pane outlive
#      two checks, and closing it under its new name still reaps them all.
#      Then every closing road, each within a 30 s grace, for daemon, monitor,
#      listener, harness, a real `muxcode proc` group, a webhook owner-lock
#      stand-in and a spawn pane: `tmux kill-session` with no close hook (the
#      daemon's own session-gone check), a same-name relaunch (the launch
#      sweep), and `kill -9` of the tmux server (the session-gone check again).
#
# Fixtures run their daemons under MUXCODE_ORPHAN_REAP=0, which turns off the
# daemon's own session-gone reap with the launch sweep, so in A-I the sweep
# under test is the only actor; J turns it back on.
#
# ISOLATION: scratch HOME, XDG_STATE_HOME, lifecycle log, config and memory;
# private tmux server (TMUX_TMPDIR in the work dir, TMUX unset); every process
# started here is killed on exit. ~4 min. Run in the foreground (MUX-171).
#
# REQUIRES: go, tmux, jq, python3, lsof, pgrep.
#
# Usage: bash scripts/test-orphan-reaper.sh
set -uo pipefail

FLOOR=56
PASS=0
FAIL=0
ok()   { PASS=$((PASS + 1)); echo "  ok: $1"; }
fail() { FAIL=$((FAIL + 1)); echo "  FAIL: $1"; }
check() { local label=$1; shift; if "$@"; then ok "$label"; else fail "$label"; fi; }
for t in go tmux jq python3 lsof pgrep; do command -v "$t" >/dev/null 2>&1 || { echo "SKIP: $t is required"; exit 2; }; done

REPO=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
WORK=$(mktemp -d /tmp/orphan-reaper-XXXXXX)
mkdir -p "$WORK/tmux" "$WORK/bin" "$WORK/noPS" "$WORK/home" "$WORK/lifecycle" "$WORK/install/agents" "$WORK/proj"
export TMUX_TMPDIR="$WORK/tmux"
unset TMUX TMUX_PANE AGENT_ROLE BUS_ROLE BUS_SESSION MUXCODE_BUS_BASE MUXCODE_BUS_DIR MUXCODE_CONSUMER_GUARD
export MUXCODE_ORPHAN_REAP=0
GOCACHE="$(go env GOCACHE)"; GOMODCACHE="$(go env GOMODCACHE)"; export GOCACHE GOMODCACHE
export HOME="$WORK/home" XDG_STATE_HOME="$WORK/home/.local/state" MUXCODE_LIFECYCLE_LOG_DIR="$WORK/lifecycle"
export MUXCODE_CONFIG="$WORK/empty-config" BUS_MEMORY_DIR="$WORK/memory" BUS_GLOBAL_MEMORY_DIR="$WORK/gmem"
export MUXCODE_INSTALL_DIR="$WORK/install" MUXCODE_AGENT_CLI=claude MUXCODE_EDIT_CLI=claude
export MUXCODE_TMP_CLEANUP_THRESHOLD=0 MUXCODE_BRANCH_TIME_DISABLE=1 MUXCODE_EDIT_AUTO_RESTART_DISABLE=1 MUXCODE_CONTROL_PANE_DISABLE=1
export MUXCODE_AGENT_HEALTH_CHECK_SECS=86400 MUXCODE_AUTO_RESUME_DISABLE=1 MUXCODE_AUTO_UPGRADE_DAEMONS_DISABLE=1
export PATH="$WORK/bin:$PATH"
: >"$WORK/empty-config"
MUX="$WORK/bin/muxcode"
STARTED=""
cleanup() {
  local p
  for p in $STARTED $(pgrep -f "$WORK/" 2>/dev/null); do kill -9 "$p" 2>/dev/null; done
  tmux kill-server 2>/dev/null
  rm -f /tmp/muxcode-analyze-orph-*-$$.trigger
  rm -rf "$WORK"
}
trap cleanup EXIT

echo "=== MUX-184 orphan reaper ==="
(cd "$REPO/tools/muxcode" && go build -buildvcs=false -o "$MUX" .) >"$WORK/build.log" 2>&1 \
  && (cd "$REPO/scripts/fixtures/claude-stub" && go build -buildvcs=false -o "$WORK/bin/claude" .) >>"$WORK/build.log" 2>&1 \
  && ok "built muxcode and the claude-stub from the tree" || { fail "build: $(tail -3 "$WORK/build.log")"; exit 1; }
cat >"$WORK/bin/muxcode-llm-harness" <<'PY'
#!/usr/bin/env python3
# Stand-in harness: records harness-prompt.pid and holds
# lock/owners/harness.prompt.<pid>.owner as a record lock, as the real loop does.
import fcntl, os, subprocess, time
session = os.environ.get("MUXCODE_SESSION") or os.environ["BUS_SESSION"]
bus = subprocess.check_output(["muxcode", "bus-dir", session], text=True).strip()
with open(os.path.join(bus, "harness-prompt.pid"), "w") as f:
    f.write(str(os.getpid()))
owners = os.path.join(bus, "lock", "owners")
os.makedirs(owners, exist_ok=True)
lock = open(os.path.join(owners, "harness.prompt.%d.owner" % os.getpid()), "w")
fcntl.lockf(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
while True:
    time.sleep(1)
PY
cat >"$WORK/bin/owner-holder" <<'PY'
#!/usr/bin/env python3
# owner-holder SESSION FAMILY ROLE: holds lock/owners/FAMILY.ROLE.<pid>.owner
# as a record lock — the webhook's record, without binding a port.
import fcntl, os, subprocess, sys, time
session, family, role = sys.argv[1:4]
bus = subprocess.check_output(["muxcode", "bus-dir", session], text=True).strip()
owners = os.path.join(bus, "lock", "owners")
os.makedirs(owners, exist_ok=True)
lock = open(os.path.join(owners, "%s.%s.%d.owner" % (family, role, os.getpid())), "w")
fcntl.lockf(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
while True:
    time.sleep(1)
PY
printf '#!/bin/sh\necho "ps: operation not permitted" >&2\nexit 1\n' >"$WORK/noPS/ps"
chmod +x "$WORK/bin/muxcode-llm-harness" "$WORK/bin/owner-holder" "$WORK/noPS/ps"
for f in code-editor; do printf -- '---\ndescription: %s\n---\nWork. Keep `muxcode inbox --poll --loop` running.\n' "$f" >"$WORK/install/agents/$f.md"; done
printf 'module proj\n\ngo 1.22\n' >"$WORK/proj/go.mod"
tmux new-session -d -s "orph-host-$$" -n host
tmux set-option -g default-command "/bin/bash --noprofile --norc"
SOCKET=$(tmux display-message -p '#{socket_path}')

bd()       { "$MUX" bus-dir "$1"; }
log_of()   { echo "$MUXCODE_LIFECYCLE_LOG_DIR/$1.log"; }
rows()     { jq -r --arg e "$2" 'select(.event == $e) | .detail' "$(log_of "$1")" 2>/dev/null; }
nrows()    { local n; n=$(rows "$1" "$2" | grep -c . ); echo "${n:-0}"; }
holder()   { lsof -t "$1" 2>/dev/null | head -1; }
alive()    { kill -0 "$1" 2>/dev/null; }
gone()     { ! kill -0 "$1" 2>/dev/null; }
owner_pid() { local f; f=$(ls "$(bd "$1")/lock/owners/" 2>/dev/null | grep "^$2\." | head -1); f=${f%.owner}; echo "${f##*.}"; }
wait_for() { local n=$(($1 * 4)) i; shift; for ((i = 0; i < n; i++)); do "$@" && return 0; sleep 0.25; done; return 1; }
detach()   { ( "$@" >/dev/null 2>&1 & ); }

# fixture S — a tmux session with a real daemon and monitor, the stand-in
# harness the daemon starts, and a real listener under a live bash parent.
# Sets D_PID M_PID L_PID H_PID.
fixture() {
  local s=$1 b i
  tmux new-session -d -s "$s" -n main
  b=$(bd "$s"); BUS_SESSION="$s" "$MUX" init --memory-dir "$WORK/mem-$s" >/dev/null 2>&1
  ( cd "$WORK" && exec "$MUX" watch "$s" --poll 1 --debounce 1 >"$WORK/daemon-$s.log" 2>&1 & )
  ( cd "$WORK" && exec "$MUX" watch --monitor "$s" >"$WORK/monitor-$s.log" 2>&1 & )
  ( AGENT_ROLE=edit BUS_SESSION="$s" exec bash -c "'$MUX' inbox --poll --loop >/dev/null 2>&1; sleep 3600" & )
  for i in $(seq 1 80); do
    D_PID=$(holder "$b/lock/daemon.owner"); M_PID=$(holder "$b/lock/monitor.owner")
    L_PID=$(owner_pid "$s" listener); H_PID=$(owner_pid "$s" harness)
    [ -n "$D_PID" ] && [ -n "$M_PID" ] && [ -n "$L_PID" ] && [ -n "$H_PID" ] && [ -s "$b/tmux.socket" ] && break
    sleep 0.25
  done
  STARTED="$STARTED $D_PID $M_PID $L_PID $H_PID"
  [ -n "$D_PID" ] && [ -n "$M_PID" ] && [ -n "$L_PID" ] && [ -n "$H_PID" ] && [ -s "$b/tmux.socket" ]
}
# dead_dir S — a bus dir for a session that is not running, recorded on this
# server, as a session that ran here and was killed leaves it.
dead_dir() { local b; b=$(bd "$1"); mkdir -p "$b"; echo "$SOCKET" >"$b/tmux.socket"; echo "$b"; }
launch() { (cd "$WORK/proj" && MUXCODE_WINDOWS=edit EDITOR=true MUXCODE_SHELL_INIT="export PATH=$WORK/bin:\$PATH" "$MUX" launch "$WORK/proj" "$1" </dev/null >"$WORK/launch-$1.log" 2>&1); }

# ── A, B, C ─────────────────────────────────────────────────────────
echo "-- A/B/C: muxcode cleanup beside a dead, a live and an unprovable session"
DS="orph-dead-$$"; LS="orph-live-$$"; US="orph-unproven-$$"
check "A: dead-session fixture up (daemon, monitor, listener, harness, socket record)" fixture "$DS"
read -r AD AM AL AH <<<"$D_PID $M_PID $L_PID $H_PID"
check "B: live-session fixture up" fixture "$LS"
read -r BD_ BM BL BH <<<"$D_PID $M_PID $L_PID $H_PID"
UB=$(dead_dir "$US"); sleep 30 & SLEEPER=$!; STARTED="$STARTED $SLEEPER"
echo "{\"id\":\"legacy\",\"pid\":$SLEEPER,\"command\":\"sleep 30\",\"status\":\"running\",\"started_at\":1}" >"$UB/proc.jsonl"
tmux kill-session -t "$DS"; sleep 1
BUS_SESSION="$LS" "$MUX" cleanup >"$WORK/cleanup-abc.out" 2>&1
check "A: daemon, monitor, listener and harness of the dead session all gone" eval 'gone $AD && gone $AM && gone $AL && gone $AH'
for fam in monitor daemon listener harness; do
  check "A: orphan-reaped row for the $fam" eval "rows $LS orphan-reaped | grep -q 'session=$DS family=$fam '"
done
sleep 2.5
check "A: its bus dir removed and still absent after twice the daemon poll" eval '[ ! -d "$(bd $DS)" ]'
check "B: the live session's four processes survive" eval 'alive $BD_ && alive $BM && alive $BL && alive $BH'
check "B: the live session's bus dir survives" eval '[ -d "$(bd $LS)" ]'
check "C: the sleeper a proc record names survives" alive "$SLEEPER"
check "C: orphan-unproven logged for it" eval "rows $LS orphan-unproven | grep -q 'session=$US family=proc pid=$SLEEPER'"
check "C: its bus dir is left in place and reported held" eval '[ -d "$UB" ] && grep -q "Left 1 bus directory" "$WORK/cleanup-abc.out"'

# ── D, E, F ─────────────────────────────────────────────────────────
echo "-- D/E: launches beside dead sessions (opt-out first, then the sweep)"
QS="orph-optout-$$"; LG="orph-legacy-$$"
check "E: dead-session fixture up" fixture "$QS"
read -r QD QM QL QH <<<"$D_PID $M_PID $L_PID $H_PID"
tmux kill-session -t "$QS"; sleep 1
N0="orph-n0-$$"
MUXCODE_ORPHAN_REAP=0 launch "$N0"
check "E: the opt-out launch reaps nothing — the dead session's four processes alive" eval 'alive $QD && alive $QM && alive $QL && alive $QH'
check "E: and removes nothing — its bus dir present, no orphan-reaped row" eval '[ -d "$(bd $QS)" ] && [ "$(nrows $N0 orphan-reaped)" = 0 ]'
tmux kill-session -t "$N0" 2>/dev/null
GB=$(dead_dir "$LG"); printf 'while :; do sleep 1; done\n' >"$GB/inbox-listener.sh"; cp "$GB/inbox-listener.sh" "$(bd "$LS")/inbox-listener.sh"
detach bash "$GB/inbox-listener.sh"; detach bash "$(bd "$LS")/inbox-listener.sh"
detach bash -c 'while true; do sleep 1; done; muxcode inbox --poll'
DEADPID=$(bash -c 'echo $$')
detach tmux -L "escabs-$DEADPID" new-session -d sleep 600; detach tmux -L "escabs-$$" new-session -d sleep 600
sleep 1
pid_of() { pgrep -f "$1" | head -1; }
GL=$(pid_of "bash $GB/inbox-listener.sh"); LL=$(pid_of "bash $(bd "$LS")/inbox-listener.sh")
LOOP=$(pid_of "while true; do sleep 1; done; muxcode inbox --poll")
ESD=$(pgrep -f "tmux -L escabs-$DEADPID " | head -1); ESL=$(pgrep -f "tmux -L escabs-$$ " | head -1)
STARTED="$STARTED $GL $LL $LOOP $ESD $ESL"
check "D: fixtures up (legacy script listeners, loop form, two escabs servers)" eval '[ -n "$GL" ] && [ -n "$LL" ] && [ -n "$LOOP" ] && [ -n "$ESD" ] && [ -n "$ESL" ]'
NS="orph-n-$$"
MUXCODE_ORPHAN_REAP= launch "$NS"
check "D: the dead session's legacy script listener reaped" gone "$GL"
check "D: with an orphan-reaped row naming argv proof" eval "rows $NS orphan-reaped | grep -q 'session=$LG family=legacy-listener pid=$GL proof=argv'"
check "D: the loop form survives" alive "$LOOP"
check "D: logged orphan-unproven" eval "rows $NS orphan-unproven | grep -q 'family=legacy-listener pid=$LOOP reason=loop form'"
check "D: the live session's script listener survives" alive "$LL"
check "D: the dead-pid escabs server reaped with its own row" eval "gone $ESD && rows $NS escabs-reaped | grep -q 'pid=$ESD'"
check "D: the live-pid escabs server survives" alive "$ESL"
check "E→D: the launch sweep then reaps the opt-out's dead session" eval 'gone $QD && gone $QM && gone $QH'

echo "-- F: the session-closed hook cleans its own session"
NB=$(bd "$NS")
wait_for 15 test -s "$NB/tmux.socket"
ND=$(holder "$NB/lock/daemon.owner"); STARTED="$STARTED $ND"
check "F: the launched session's daemon recorded its socket" test -s "$NB/tmux.socket"
tmux kill-session -t "$NS"
wait_for 20 eval '[ ! -d "$NB" ]'
check "F: kill-session → its own bus dir gone with no manual command" eval '[ ! -d "$NB" ]'
check "F: and its daemon reaped first" eval '[ -n "$ND" ] && gone $ND'

# ── G ───────────────────────────────────────────────────────────────
echo "-- G: ps unavailable"
PS_="orph-nops-$$"
check "G: dead-session fixture up" fixture "$PS_"
read -r GD GM GLI GH <<<"$D_PID $M_PID $L_PID $H_PID"
cp "$GB/inbox-listener.sh" "$(bd "$PS_")/inbox-listener.sh"; detach bash "$(bd "$PS_")/inbox-listener.sh"; sleep 0.5
GLEG=$(pid_of "bash $(bd "$PS_")/inbox-listener.sh"); STARTED="$STARTED $GLEG"
tmux set-hook -gu 'session-closed[184]' # the close hook's own cleanup has ps; this section's sweep must not
tmux kill-session -t "$PS_"; sleep 1
PATH="$WORK/noPS:$PATH" BUS_SESSION="$LS" "$MUX" cleanup >"$WORK/cleanup-g.out" 2>&1
check "G: the recorded families reaped by owner lock alone" eval 'gone $GD && gone $GM && gone $GLI && gone $GH'
check "G: orphan-census-unavailable logged" eval '[ "$(nrows $LS orphan-census-unavailable)" -ge 1 ]'
check "G: the argv-only legacy listener is left" eval '[ -n "$GLEG" ] && alive $GLEG'

# ── H ───────────────────────────────────────────────────────────────
echo "-- H: the daemon stops its children on TERM, keeps them across a re-exec"
HS="orph-children-$$"
check "H: live-session fixture up" fixture "$HS"
read -r HD HM HL HH <<<"$D_PID $M_PID $L_PID $H_PID"
kill -TERM "$HM"; wait_for 5 gone "$HM"
kill -TERM "$HD"; wait_for 5 gone "$HD"
check "H: TERM — the harness is gone with the daemon" eval 'gone $HD && gone $HH'
check "H: daemon-stopped logged naming the harness" eval "rows $HS daemon-stopped | grep -q 'stopped 1 harness'"
RS="orph-reexec-$$"
check "H: re-exec fixture up" fixture "$RS"
read -r RD RM RL RH <<<"$D_PID $M_PID $L_PID $H_PID"
PATH="$WORK/noPS:$PATH" "$MUX" upgrade-daemons --session "$RS" --force >"$WORK/upgrade-h.out" 2>&1
check "H: re-exec — the same daemon pid, a daemon-reexec row" eval 'alive $RD && [ "$(holder $(bd $RS)/lock/daemon.owner)" = "$RD" ] && [ "$(nrows $RS daemon-reexec)" -ge 1 ]'
check "H: and its harness still running under the same pid" eval 'alive $RH && [ "$(owner_pid $RS harness)" = "$RH" ]'

# ── I ───────────────────────────────────────────────────────────────
echo "-- I: monitor first (~2 min)"
OS="orph-order-$$"
check "I: live-session fixture up" fixture "$OS"
read -r OD OM OL OH <<<"$D_PID $M_PID $L_PID $H_PID"
kill -TERM "$OD"; wait_for 5 gone "$OD"
relaunched() { local p; p=$(holder "$(bd "$OS")/lock/daemon.owner"); [ -n "$p" ] && [ "$p" != "$OD" ] && STARTED="$STARTED $p"; }
check "I: mis-ordered — a daemon killed under a live monitor is relaunched" wait_for 75 relaunched
NEWD=$(holder "$(bd "$OS")/lock/daemon.owner")
kill -TERM "$OM"; wait_for 5 gone "$OM"; kill -TERM "$NEWD"; wait_for 5 gone "$NEWD"
sleep 45
check "I: monitor first — the daemon stays down" eval '[ -z "$(holder $(bd $OS)/lock/daemon.owner)" ]'

# ── J ───────────────────────────────────────────────────────────────
echo "-- J: every closing road leaves nothing past a 30 s grace"
# extras S — a real proc, a webhook owner-lock holder and a spawn-pane process
# for S; prints their pids (the proc's is its group id).
extras() {
  local s=$1 b proc hook pane
  b=$(bd "$s")
  (cd "$WORK" && BUS_SESSION="$s" "$MUX" proc start "sleep 600" >/dev/null 2>&1)
  detach "$WORK/bin/owner-holder" "$s" webhook -
  tmux new-window -d -t "$s" -n spawn-j "sleep 600"
  wait_for 10 eval '[ -n "$(owner_pid $s proc)" ] && [ -n "$(owner_pid $s webhook)" ]'
  proc=$(owner_pid "$s" proc); hook=$(owner_pid "$s" webhook)
  pane=$(tmux display-message -p -t "$s:spawn-j" '#{pane_pid}' 2>/dev/null)
  STARTED="$STARTED $proc $hook $pane"
  echo "$proc $hook $pane"
}
group_gone() { [ -z "$(pgrep -g "$1" 2>/dev/null)" ]; }
none_alive() { local p; for p in "$@"; do [ -n "$p" ] && kill -0 "$p" 2>/dev/null && return 1; done; return 0; }
none_dead()  { local p; for p in "$@"; do kill -0 "$p" 2>/dev/null || return 1; done; return 0; }
all_gone() { local proc=$1; shift; none_alive "$@" && group_gone "$proc"; }

J0="orph-rename-$$"
MUXCODE_ORPHAN_REAP= fixture "$J0"; read -r J0D J0M J0L J0H <<<"$D_PID $M_PID $L_PID $H_PID"
read -r J0P J0W J0S <<<"$(extras "$J0")"
check "J0: fixture up with a proc, a webhook holder and a spawn pane" eval '[ -n "$J0D" ] && [ -n "$J0P" ] && [ -n "$J0W" ] && [ -n "$J0S" ]'
tmux rename-session -t "$J0" "$J0-renamed"
sleep 25
# The monitor and listener check the session by name themselves, so they are
# not in the survival set; the daemon's reap is judged by session id.
check "J0: rename — daemon, harness, proc group, webhook holder and spawn pane alive past two session checks" eval 'none_dead $J0D $J0H $J0P $J0W $J0S && ! group_gone $J0P'
check "J0: no daemon-session-gone for a renamed session" eval '[ "$(nrows $J0 daemon-session-gone)" = 0 ]'
tmux kill-session -t "$J0-renamed"
check "J0: closing the renamed session — all seven gone within 30 s" wait_for 30 all_gone "$J0P" $J0D $J0M $J0L $J0H $J0W $J0S

J1="orph-close-$$"
MUXCODE_ORPHAN_REAP= fixture "$J1"; read -r J1D J1M J1L J1H <<<"$D_PID $M_PID $L_PID $H_PID"
read -r J1P J1W J1S <<<"$(extras "$J1")"
check "J1: fixture up with a proc, a webhook holder and a spawn pane" eval '[ -n "$J1D" ] && [ -n "$J1P" ] && [ -n "$J1W" ] && [ -n "$J1S" ]'
tmux kill-session -t "$J1"
check "J1: kill-session with no close hook — all seven gone within 30 s" wait_for 30 all_gone "$J1P" $J1D $J1M $J1L $J1H $J1W $J1S
check "J1: by the daemon's own daemon-session-gone" eval '[ "$(nrows $J1 daemon-session-gone)" = 1 ]'

J2="orph-relaunch-$$"
launch "$J2" # first instance under the opt-out, so its own daemon cannot beat the relaunch's sweep
J2B=$(bd "$J2"); wait_for 15 test -s "$J2B/tmux.socket"; wait_for 10 eval '[ -n "$(owner_pid $J2 harness)" ]'
J2D=$(holder "$J2B/lock/daemon.owner"); J2M=$(holder "$J2B/lock/monitor.owner"); J2H=$(owner_pid "$J2" harness)
read -r J2P J2W J2S <<<"$(extras "$J2")"
J2X=$(pgrep -f "muxcode launch --(resize|auto-accept) $J2" | tr '\n' ' ')
STARTED="$STARTED $J2D $J2M $J2H"
check "J2: launched session up with daemon, monitor, harness, proc, webhook holder, spawn pane" eval '[ -n "$J2D" ] && [ -n "$J2M" ] && [ -n "$J2H" ] && [ -n "$J2P" ] && [ -n "$J2W" ] && [ -n "$J2S" ]'
tmux set-hook -gu 'session-closed[184]'
tmux kill-session -t "$J2"; MUXCODE_ORPHAN_REAP= launch "$J2"
NEW2=$(holder "$J2B/lock/daemon.owner"); STARTED="$STARTED $NEW2"
check "J2: the same-name relaunch went ahead on a new daemon" eval '[ -n "$NEW2" ] && [ "$NEW2" != "$J2D" ]'
check "J2: the first instance's processes all gone within 30 s (launch helpers included)" wait_for 30 all_gone "$J2P" $J2D $J2M $J2H $J2W $J2S $J2X
check "J2: reaped by the relaunch's sweep" eval "rows $J2 orphan-reaped | grep -q 'family=daemon pid=$J2D '"

J3="orph-server-$$"
MUXCODE_ORPHAN_REAP= fixture "$J3"; read -r J3D J3M J3L J3H <<<"$D_PID $M_PID $L_PID $H_PID"
read -r J3P J3W J3S <<<"$(extras "$J3")"
check "J3: fixture up with a proc, a webhook holder and a spawn pane" eval '[ -n "$J3D" ] && [ -n "$J3P" ] && [ -n "$J3W" ] && [ -n "$J3S" ]'
kill -9 "$(tmux display-message -p '#{pid}')"
check "J3: kill -9 of the tmux server — all seven gone within 30 s" wait_for 30 all_gone "$J3P" $J3D $J3M $J3L $J3H $J3W $J3S
check "J3: by the daemon's own daemon-session-gone" eval '[ "$(nrows $J3 daemon-session-gone)" = 1 ]'

total=$((PASS + FAIL))
echo
echo "=== test-orphan-reaper: $PASS passed, $FAIL failed ($total checks, floor $FLOOR) ==="
[ "$total" -lt "$FLOOR" ] && { echo "  FAIL: only $total checks ran (floor $FLOOR) — a section was skipped"; exit 1; }
[ "$FAIL" = 0 ]
