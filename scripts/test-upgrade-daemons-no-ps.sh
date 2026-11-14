#!/usr/bin/env bash
# Integration test for MUX-161 — `muxcode upgrade-daemons` must find and cycle
# a session's daemon when ps cannot run, and say so when it cannot.
#
# A Codex build sandbox refuses to exec /bin/ps, and daemon discovery was ps
# alone: every build there printed "ps: … operation not permitted", skipped
# the rollout and read green, while the live daemon kept the code it launched
# with (2026-09-09). Discovery now asks the kernel which process holds each bus
# directory's lock/daemon.owner and lock/monitor.owner record lock when ps
# fails; a live daemon it still misses is a daemon-upgrade-failed row and exit 1.
#
# Hermetic: scratch HOME, XDG_STATE_HOME (so no walk can reach a real
# session's bus directory), lifecycle log and private tmux server; three
# binaries built from THIS tree and stamped apart — OLD and NEW, and LEGACY:
# the tree with AcquireInstanceLock's owner record lock patched out of a scratch
# copy, so it holds only the daemon.lock flock, as every daemon before this fix
# did. "No ps" is a PATH holding muxcode, tmux and a few shell tools, and no
# ps. Every rollout that acts is scoped with --session.
#
# Sections:
#   A  an OLD daemon and monitor run for the scratch session
#   B  with no ps on PATH, `upgrade-daemons --dry-run` still plans the session
#      (daemon v0.0.1-test → installed v0.0.2-test) and logs
#      daemon-discovery-ps-failed; with ps restored the plan line is identical
#   C  negative control — a ghost session's daemon.owner freshly naming a live,
#      unrelated process, with no lock held, is not planned
#   D  NEW installed over the executable the daemon and monitor were started
#      from, the real rollout with no ps — and the tmux socket unreachable,
#      which must not read as a gone session — kills nothing: daemon and monitor
#      re-exec their own executable, now NEW, on a restart request that names
#      no binary (same pids, daemon-reexec and
#      monitor-reexec rows), daemon.version carries NEW's version and date and
#      monitor.version NEW's version, and the unrelated process named by the
#      ghost lock file is still alive
#   E  a LEGACY daemon (flock only, no owner lock) beside a NEW monitor: a NEW
#      daemon is refused while it runs (mixed-version exclusion); with no ps
#      the rollout exits 1 naming the ps failure, writes daemon-upgrade-failed,
#      leaves the LEGACY daemon running, and `session status` reads STALE
#   F  control — the same state with ps on PATH cycles the LEGACY daemon onto
#      NEW and writes no further daemon-upgrade-failed row
# Live section: `--live` or MUXCODE_UPGRADE_NO_PS_LIVE=1 with codex on PATH —
# an OLD daemon again, then one real `codex exec` under the build role's own
# launch flags with MUXCODE_CODEX_SANDBOX=workspace-write (`agent sandbox-args
# build`, from the user's HOME and config) runs NEW's
# `upgrade-daemons --session <scratch>`: the daemon must be cycled onto NEW.
# Live passes are outside the floor; skipped with a reason otherwise. A
# coverage floor keeps a skipped section from reporting green.
# Requires go, tmux, jq.
set -uo pipefail

PASS=0
FAIL=0
EXPECTED_PASS=26
for t in tmux go jq; do
  command -v "$t" >/dev/null 2>&1 || { echo "SKIP: $t is required"; exit 2; }
done

LIVE=0
if [ "${1:-}" = --live ] || [ "${MUXCODE_UPGRADE_NO_PS_LIVE:-}" = 1 ]; then
  LIVE=1
fi
LIVE_PASS=0

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
MOD="$ROOT/tools/muxcode"
WORK=$(mktemp -d /tmp/upgrade-no-ps-XXXXXX)
. "$ROOT/scripts/lib/muxcode-bus-dir.sh"
# The user's own HOME, config and install tree, for real_launch: codex needs
# its real auth, and the build role's sandbox flags resolve from them.
REAL_HOME=$HOME
REAL_MUXCODE_CONFIG=${MUXCODE_CONFIG:-}
REAL_INSTALL_DIR=${MUXCODE_INSTALL_DIR:-}
if [ -z "$REAL_INSTALL_DIR" ]; then
  case $(dirname "$(command -v muxcode 2>/dev/null)") in
    */.local/bin) REAL_INSTALL_DIR="$REAL_HOME/.config/muxcode" ;;
  esac
fi
REAL_PATH=$PATH

unset TMUX AGENT_ROLE BUS_ROLE MUXCODE_BUS_BASE MUXCODE_BUS_DIR
export HOME="$WORK/home"
export XDG_STATE_HOME="$WORK/state"
export TMUX_TMPDIR="$WORK/tmux"
export BUS_SESSION="no-ps-$$"
export BUS_ROLE=edit
export MUXCODE_LIFECYCLE_LOG_DIR="$WORK/lifecycle"
export MUXCODE_SESSION_REPO_DIR="$WORK/repo"
: >"$WORK/empty-config"
export MUXCODE_CONFIG="$WORK/empty-config"
export MUXCODE_TMP_CLEANUP_THRESHOLD=0 MUXCODE_BRANCH_TIME_DISABLE=1 MUXCODE_CONTROL_PANE_DISABLE=1
export MUXCODE_AGENT_HEALTH_CHECK_SECS=86400 MUXCODE_AGENTDEFS_WATCH_DISABLE=1 MUXCODE_FORCE_RESPOND_DISABLE=1
mkdir -p "$HOME" "$XDG_STATE_HOME" "$TMUX_TMPDIR" "$WORK/repo" "$WORK/nops"
S="$BUS_SESSION"
LIFELOG="$MUXCODE_LIFECYCLE_LOG_DIR/$S.log"
UNRELATED=""

cleanup() {
  pkill -f "muxcode watch.* $S" 2>/dev/null
  pkill -f "muxcode watch $S" 2>/dev/null
  [ -n "$UNRELATED" ] && kill "$UNRELATED" 2>/dev/null
  tmux kill-server 2>/dev/null
  sleep 1
  rm -rf "$WORK"
  true
}
trap cleanup EXIT

ok()   { PASS=$((PASS + 1)); echo "  ok: $1"; }
fail() { FAIL=$((FAIL + 1)); echo "  FAIL: $1"; }

BUSPKG=github.com/mkober/muxcode/tools/muxcode/bus
# build SRC DIR VERSION DATE — a muxcode binary named muxcode (discovery
# matches that basename) stamped with VERSION and DATE.
build() {
  mkdir -p "$2"
  (cd "$1" && go build -buildvcs=false -ldflags "-X $BUSPKG.Version=$3 -X $BUSPKG.Commit=c0ffee0 -X $BUSPKG.BuildDate=$4" \
    -o "$2/muxcode" .) || { echo "SKIP: go build failed for $3"; exit 2; }
}
OLD_DATE=2026-11-01T00:00:00Z NEW_DATE=2026-11-14T00:00:00Z
build "$MOD" "$WORK/old" v0.0.1-test "$OLD_DATE"
build "$MOD" "$WORK/new" v0.0.2-test "$NEW_DATE"
OLD="$WORK/old/muxcode" NEW="$WORK/new/muxcode"
# INST is the trusted installation: daemons are started from it, and a restart
# request makes them re-exec it — never a path a request names. install_bin
# replaces it the way `make install` does (a new file renamed over the old,
# so running processes keep their inode).
INST="$WORK/inst/muxcode"
install_bin() { mkdir -p "$WORK/inst" && cp "$1" "$INST.tmp" && mv -f "$INST.tmp" "$INST"; }
cp -R "$MOD" "$WORK/legacy-src"
sed -i.orig 's/	unlockOwner, err := AcquirePidLock(ownerPath)/	unlockOwner, err := func() {}, error(nil)/' "$WORK/legacy-src/bus/pidlock.go"
if [ "$(grep -c 'unlockOwner, err := func() {}, error(nil)' "$WORK/legacy-src/bus/pidlock.go")" = 1 ]; then
  build "$WORK/legacy-src" "$WORK/legacy" v0.0.0-legacy "$OLD_DATE"
  ok "OLD, NEW and LEGACY (owner record lock patched out) built from this tree"
else
  fail "LEGACY build: the owner-lock patch did not apply exactly once"
fi
LEGACY="$WORK/legacy/muxcode"
BD="$(muxcode_bus_dir "$NEW" "$S")" || exit 1
BASE=$(dirname "$BD")

for tool in tmux git sh bash cat sleep date; do
  p=$(command -v "$tool" 2>/dev/null) && ln -s "$p" "$WORK/nops/$tool"
done
ln -s "$NEW" "$WORK/nops/muxcode"
NOPS="$WORK/nops"
[ -z "$(PATH="$NOPS" command -v ps 2>/dev/null)" ] && ok "the no-ps PATH has no ps" || fail "ps is reachable on the no-ps PATH"

daemon_pid() { ps -axo pid=,command= | awk -v s="$S" '$2 ~ /(^|\/)muxcode$/ && $3 == "watch" && $4 == s { print $1; exit }'; }
monitor_pid() { ps -axo pid=,command= | awk -v s="$S" '$2 ~ /(^|\/)muxcode$/ && $3 == "watch" && $4 == "--monitor" && $5 == s { print $1; exit }'; }
version_is() { [ "$(jq -r .version "$BD/daemon.version" 2>/dev/null)" = "$1" ]; }
rows() { jq -r --arg e "$1" 'select(.event == $e) | "\(.level) \(.detail)"' "$LIFELOG" 2>/dev/null; }
row_count() { rows "$1" | grep -c . || true; }
wait_for() {
  local secs=$1 desc=$2 i=0
  shift 2
  while [ "$i" -lt "$secs" ]; do
    if "$@" >/dev/null 2>&1; then ok "$desc"; return 0; fi
    sleep 1; i=$((i + 1))
  done
  fail "$desc (timeout ${secs}s)"
  return 1
}
plan_line() { printf '%s\n' "$1" | grep -F "  $S:" | head -1; }

echo "=== MUX-161 upgrade-daemons without ps ==="

# ── A: an OLD daemon and monitor ───────────────────────────────────
echo "-- A: OLD daemon and monitor running"
tmux new-session -d -s "$S" -n scratch -x 120 -y 30 -c "$WORK/repo"
"$NEW" init >/dev/null 2>&1
install_bin "$OLD"
(cd "$WORK/repo" && exec "$INST" watch "$S" --poll 1 >>"$WORK/daemon.log" 2>&1) &
(cd "$WORK/repo" && exec "$INST" watch --monitor "$S" >>"$WORK/monitor.log" 2>&1) &
wait_for 15 "A: OLD daemon recorded daemon.version v0.0.1-test" version_is v0.0.1-test
OLD_DAEMON=$(daemon_pid) OLD_MONITOR=$(monitor_pid)
[ -n "$OLD_DAEMON" ] && [ -n "$OLD_MONITOR" ] && ok "A: daemon ($OLD_DAEMON) and monitor ($OLD_MONITOR) running" \
  || fail "A: daemon '$OLD_DAEMON' monitor '$OLD_MONITOR'"

# ── B: dry run without ps, then with ──────────────────────────────
echo "-- B: discovery without ps"
nops_out=$(PATH="$NOPS" "$NEW" upgrade-daemons --dry-run --session "$S" 2>&1)
plan_line "$nops_out" | grep -q "daemon v0.0.1-test → installed v0.0.2-test — would restart" \
  && ok "B: with no ps the dry run still plans $S's restart" || fail "B: no-ps dry run: $nops_out"
[ "$(row_count daemon-discovery-ps-failed)" -ge 1 ] && rows daemon-discovery-ps-failed | head -1 | grep -q '^info ' \
  && ok "B: the ps failure is an info row (daemon-discovery-ps-failed)" || fail "B: rows: $(rows daemon-discovery-ps-failed)"
ps_out=$("$NEW" upgrade-daemons --dry-run --session "$S" 2>&1)
[ -n "$(plan_line "$ps_out")" ] && [ "$(plan_line "$ps_out")" = "$(plan_line "$nops_out")" ] \
  && ok "B: with ps restored the plan line is identical — the roads agree" \
  || fail "B: ps '$(plan_line "$ps_out")' vs no-ps '$(plan_line "$nops_out")'"

# ── C: a ghost lock file naming an unrelated live process ─────────
echo "-- C: an unheld lock file is no target (negative control)"
sleep 300 &
UNRELATED=$!
mkdir -p "$BASE/muxcode-bus-ghost-$$/lock"
echo "$UNRELATED" >"$BASE/muxcode-bus-ghost-$$/lock/daemon.owner"
echo "$UNRELATED" >"$BASE/muxcode-bus-ghost-$$/lock/monitor.owner"
ghost_out=$(PATH="$NOPS" "$NEW" upgrade-daemons --dry-run 2>&1)
printf '%s' "$ghost_out" | grep -q "ghost-$$" && fail "C: the ghost session was planned: $ghost_out" \
  || ok "C: a lock file freshly naming live pid $UNRELATED, unheld, is not planned"
[ -n "$(plan_line "$ghost_out")" ] && ok "C: the unscoped no-ps walk still plans $S" || fail "C: unscoped: $ghost_out"

# ── D: the real rollout with no ps, tmux unreachable ───────────────
echo "-- D: rollout with no ps — by restart request, with the tmux socket unreachable"
# NEW is installed over the executable the daemon and monitor were started
# from; TMUX_TMPDIR names an empty directory, so has-session fails as a refused
# socket would, and the session must not be read as gone and orphan-killed.
install_bin "$NEW"
out=$(TMUX_TMPDIR="$WORK/no-tmux" PATH="$NOPS" "$NEW" upgrade-daemons --session "$S" 2>&1)
rc=$?
[ "$rc" = 0 ] && plan_line "$out" | grep -q "daemon + monitor restarted" \
  && ok "D: daemon and monitor restarted with no ps and no reachable tmux" || fail "D: rc=$rc: $out"
version_is v0.0.2-test && [ "$(jq -r .date "$BD/daemon.version" 2>/dev/null)" = "$NEW_DATE" ] \
  && ok "D: daemon.version carries the installed build ($NEW_DATE) when the rollout returns" \
  || fail "D: daemon.version: $(cat "$BD/daemon.version" 2>/dev/null)"
[ "$(jq -r .version "$BD/monitor.version" 2>/dev/null)" = v0.0.2-test ] && ok "D: monitor.version reports v0.0.2-test" \
  || fail "D: monitor.version: $(cat "$BD/monitor.version" 2>/dev/null)"
[ "$(daemon_pid)" = "$OLD_DAEMON" ] && [ "$(monitor_pid)" = "$OLD_MONITOR" ] \
  && ok "D: nothing was killed — daemon $OLD_DAEMON and monitor $OLD_MONITOR re-exec'd in place" \
  || fail "D: daemon '$(daemon_pid)' (was $OLD_DAEMON), monitor '$(monitor_pid)' (was $OLD_MONITOR)"
[ "$(row_count daemon-reexec)" -ge 1 ] && [ "$(row_count monitor-reexec)" -ge 1 ] \
  && ok "D: daemon-reexec and monitor-reexec rows record the restart requests honoured" \
  || fail "D: reexec rows: $(rows daemon-reexec) / $(rows monitor-reexec)"
kill -0 "$UNRELATED" 2>/dev/null && ok "D: the unrelated process named by the ghost lock file is untouched" \
  || fail "D: unrelated pid $UNRELATED was killed"
[ "$(row_count daemon-upgrade-failed)" = 0 ] && ok "D: no daemon-upgrade-failed row" || fail "D: $(rows daemon-upgrade-failed)"

# ── E: a LEGACY daemon beside a NEW monitor, no ps ─────────────────
echo "-- E: a daemon holding no owner lock, no ps"
pkill -f "muxcode watch.* $S" 2>/dev/null
pkill -f "muxcode watch $S" 2>/dev/null
gone() { [ -z "$(daemon_pid)" ] && [ -z "$(monitor_pid)" ]; }
wait_for 10 "E: daemon and monitor stopped" gone
(cd "$WORK/repo" && exec "$LEGACY" watch "$S" --poll 1 >>"$WORK/daemon.log" 2>&1) &
wait_for 15 "E: LEGACY daemon recorded daemon.version v0.0.0-legacy" version_is v0.0.0-legacy
(cd "$WORK/repo" && exec "$NEW" watch --monitor "$S" >>"$WORK/monitor.log" 2>&1) &
LEGACY_DAEMON=$(daemon_pid)
"$NEW" watch "$S" --poll 1 >"$WORK/second-daemon.log" 2>&1 &
second=$!
sleep 2
if kill -0 "$second" 2>/dev/null; then
  kill "$second" 2>/dev/null
  fail "E: a NEW daemon started beside the LEGACY one"
else
  grep -q "another daemon is already running" "$WORK/second-daemon.log" \
    && ok "E: a NEW daemon is refused while the LEGACY daemon holds daemon.lock" \
    || fail "E: second daemon exited without the exclusion message: $(cat "$WORK/second-daemon.log")"
fi
sleep 2 # let the NEW monitor take its locks
out=$(PATH="$NOPS" "$NEW" upgrade-daemons --session "$S" 2>&1)
rc=$?
[ "$rc" = 1 ] && printf '%s' "$out" | grep -q "FAILED" && printf '%s' "$out" | grep -q "ps could not run" \
  && ok "E: the rollout exits 1 naming the ps failure" || fail "E: rc=$rc: $out"
rows daemon-upgrade-failed | grep -q "^warn daemon for $S is live but was not found" \
  && ok "E: a warn daemon-upgrade-failed row names the session" || fail "E: rows: $(rows daemon-upgrade-failed)"
[ "$(daemon_pid)" = "$LEGACY_DAEMON" ] && version_is v0.0.0-legacy \
  && ok "E: the LEGACY daemon ($LEGACY_DAEMON) is left running, not killed blind" || fail "E: daemon now '$(daemon_pid)'"
"$NEW" session status 2>&1 | grep -q "^Daemon: STALE — daemon v0.0.0-legacy → installed v0.0.2-test" \
  && ok "E: session status reads the daemon STALE against the installed build" \
  || fail "E: session status: $("$NEW" session status 2>&1)"

# ── F: control — the same state with ps ────────────────────────────
echo "-- F: control — ps on PATH cycles the LEGACY daemon"
failed_before=$(row_count daemon-upgrade-failed)
out=$(PATH="$WORK/new:$PATH" "$NEW" upgrade-daemons --session "$S" 2>&1)
rc=$?
[ "$rc" = 0 ] && plan_line "$out" | grep -q "restarted" && ok "F: with ps the LEGACY daemon is found and restarted" \
  || fail "F: rc=$rc: $out"
wait_for 15 "F: daemon.version reports v0.0.2-test" version_is v0.0.2-test
[ "$(row_count daemon-upgrade-failed)" = "$failed_before" ] && ok "F: no further daemon-upgrade-failed row" \
  || fail "F: $(rows daemon-upgrade-failed)"

# ── Live: the rollout run by a real codex under the build role's sandbox ─
lok() { LIVE_PASS=$((LIVE_PASS + 1)); echo "  ok (live): $1"; }
# live_wait SECS DESCRIPTION CHECK [ARGS...] — wait_for, counted as a live pass.
live_wait() {
  local secs=$1 desc=$2 i=0
  shift 2
  while [ "$i" -lt "$secs" ]; do
    if "$@" >/dev/null 2>&1; then lok "$desc"; return 0; fi
    sleep 1; i=$((i + 1))
  done
  fail "live: $desc (timeout ${secs}s)"
  return 1
}
# real_launch CMD... — CMD under the user's own HOME, config, install tree and
# PATH, as a launched agent resolves them, not the scratch session's.
real_launch() {
  local -a real=()
  [ -n "$REAL_MUXCODE_CONFIG" ] && real+=("MUXCODE_CONFIG=$REAL_MUXCODE_CONFIG")
  [ -n "$REAL_INSTALL_DIR" ] && real+=("MUXCODE_INSTALL_DIR=$REAL_INSTALL_DIR")
  env -u BUS_SESSION -u BUS_ROLE -u TMUX_TMPDIR -u XDG_STATE_HOME -u MUXCODE_CONFIG -u MUXCODE_LIFECYCLE_LOG_DIR \
    HOME="$REAL_HOME" PATH="$REAL_PATH" ${real[@]+"${real[@]}"} "$@"
}
if [ "$LIVE" != 1 ]; then
  echo "-- live section skipped (pass --live or set MUXCODE_UPGRADE_NO_PS_LIVE=1; spends a real model call)"
elif ! PATH="$REAL_PATH" command -v codex >/dev/null 2>&1; then
  echo "-- live: skipped (codex is not on PATH)"
else
  echo "-- live: codex exec under the build role's MUXCODE_CODEX_SANDBOX=workspace-write flags runs the rollout"
  pkill -f "muxcode watch.* $S" 2>/dev/null
  pkill -f "muxcode watch $S" 2>/dev/null
  live_wait 10 "the F daemon and monitor stopped" gone
  install_bin "$OLD"
  (cd "$WORK/repo" && exec "$INST" watch "$S" --poll 1 >>"$WORK/daemon.log" 2>&1) &
  (cd "$WORK/repo" && exec "$INST" watch --monitor "$S" >>"$WORK/monitor.log" 2>&1) &
  live_wait 15 "an OLD daemon is back on v0.0.1-test" version_is v0.0.1-test
  install_bin "$NEW"
  LIVE_DAEMON=$(daemon_pid)
  ps_failed_before=$(row_count daemon-discovery-ps-failed)

  launch=() exec_args=() skip=0
  while IFS= read -r a; do launch+=("$a"); done < <(real_launch env MUXCODE_CODEX_SANDBOX=workspace-write "$NEW" agent sandbox-args build)
  for a in ${launch[@]+"${launch[@]}"}; do
    if [ "$skip" = 1 ]; then skip=0; continue; fi
    case "$a" in
      -a) skip=1 ;;
      --search) ;;
      *) exec_args+=("$a") ;;
    esac
  done
  printf '%s\n' "${exec_args[@]:-}" | grep -qx 'workspace-write' \
    && lok "build role's flags under the opt-out: ${exec_args[*]}" \
    || fail "live: build sandbox args carry no workspace-write: '${launch[*]:-}'"

  cat >"$WORK/live-upgrade.sh" <<EOF
#!/bin/bash
exec >"$WORK/live-upgrade.out" 2>&1
export HOME="$HOME" XDG_STATE_HOME="$XDG_STATE_HOME" TMUX_TMPDIR="$TMUX_TMPDIR" BUS_SESSION="$S" BUS_ROLE=edit
export MUXCODE_LIFECYCLE_LOG_DIR="$MUXCODE_LIFECYCLE_LOG_DIR" MUXCODE_SESSION_REPO_DIR="$MUXCODE_SESSION_REPO_DIR"
export MUXCODE_CONFIG="$MUXCODE_CONFIG" MUXCODE_AGENT_HEALTH_CHECK_SECS=86400 MUXCODE_BRANCH_TIME_DISABLE=1
export PATH="$WORK/new:\$PATH"
"$NEW" upgrade-daemons --session "$S"
echo "upgrade-daemons exit \$?"
EOF
  want="bash $WORK/live-upgrade.sh"
  model=()
  [ -n "${MUXCODE_UPGRADE_NO_PS_LIVE_MODEL:-}" ] && model=(-m "$MUXCODE_UPGRADE_NO_PS_LIVE_MODEL")
  (cd "$WORK/repo" && real_launch codex exec --skip-git-repo-check ${exec_args[@]+"${exec_args[@]}"} ${model[@]+"${model[@]}"} \
    --json -o "$WORK/codex.last" \
    "Run exactly the shell command \`$want\` in the current directory, once, as a command of its own with nothing before or after it. Do not edit any file. Then reply with one line: EXIT=<its exit code>." \
    >"$WORK/codex.jsonl" 2>"$WORK/codex.err" </dev/null)
  codex_rc=$?
  echo "  codex exit $codex_rc; rollout output:"
  sed 's/^/    | /' "$WORK/live-upgrade.out" 2>/dev/null || echo "    (no output file — the sandbox may have refused the write)"
  [ "$(row_count daemon-discovery-ps-failed)" -gt "$ps_failed_before" ] \
    && echo "  note: ps was refused inside the sandbox (a new daemon-discovery-ps-failed row)" \
    || echo "  note: no new daemon-discovery-ps-failed row — ps ran inside the sandbox, or the log write was refused"
  if [ "$codex_rc" = 0 ] && grep -q "upgrade-daemons exit 0" "$WORK/live-upgrade.out" 2>/dev/null; then
    lok "codex ran the rollout under the build sandbox, exit 0"
  else
    fail "live: codex exit $codex_rc, rollout: $(tail -3 "$WORK/live-upgrade.out" 2>/dev/null | tr '\n' ' '); stderr: $(tail -c 300 "$WORK/codex.err")"
  fi
  if live_wait 20 "the session daemon was cycled onto NEW (daemon.version v0.0.2-test)" version_is v0.0.2-test; then
    sleep 5 # codex has exited; a process tied to its sandbox would be gone by now
    [ -n "$(daemon_pid)" ] && [ -n "$(monitor_pid)" ] && version_is v0.0.2-test \
      && lok "daemon ($(daemon_pid), was $LIVE_DAEMON) and monitor are alive on NEW after codex exited" \
      || fail "live: after codex exited, daemon '$(daemon_pid)' (was $LIVE_DAEMON), monitor '$(monitor_pid)'"
  else
    echo "  daemon now '$(daemon_pid)' (was $LIVE_DAEMON), daemon.version $(jq -c . "$BD/daemon.version" 2>/dev/null)"
  fi
fi

echo
echo "=== results: $PASS passed, $FAIL failed (floor $EXPECTED_PASS), $LIVE_PASS live ==="
if [ "$PASS" -lt "$EXPECTED_PASS" ]; then
  echo "FAIL: coverage floor — $PASS passed, expected at least $EXPECTED_PASS"
  exit 1
fi
[ "$FAIL" = 0 ]
