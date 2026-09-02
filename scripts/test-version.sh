#!/usr/bin/env bash
# Integration test for binary versioning (MUX-138): the stamped identity, the
# --version/-v route, the --at-least comparison, and the daemon rollout the
# stamp makes observable (upgrade-daemons skip/cycle, diagnose mismatch).
#
# Hermetic: every binary under test is built here from this checkout's
# tools/muxcode with explicit -X stamps, so no tag has to exist and the
# installed muxcode is never run. Everything else is scratch — a private tmux
# server (TMUX_TMPDIR), a scratch BUS_SESSION and daemon, lifecycle log and
# config in a temp dir. The rollout section passes --session to
# upgrade-daemons: an unscoped run cycles every stale daemon on the machine,
# live sessions included.
#
# Skips exit 2, not 0, and a coverage floor keeps a partially executed run
# from reporting green.
#
# REQUIRES: go, tmux, jq.
#
# Usage: bash scripts/test-version.sh
set -uo pipefail

PASS=0
FAIL=0
ok()   { PASS=$((PASS + 1)); echo "  ok: $1"; }
fail() { FAIL=$((FAIL + 1)); echo "  FAIL: $1"; }

command -v go   >/dev/null 2>&1 || { echo "SKIP: go is required"; exit 2; }
command -v tmux >/dev/null 2>&1 || { echo "SKIP: tmux is required"; exit 2; }
command -v jq   >/dev/null 2>&1 || { echo "SKIP: jq is required"; exit 2; }
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MOD="$ROOT/tools/muxcode"
[ -f "$MOD/go.mod" ] || { echo "SKIP: $MOD/go.mod not found"; exit 2; }

# --- Isolation -------------------------------------------------------------
WORK=$(mktemp -d /tmp/version-test-XXXXXX)
export TMUX_TMPDIR="$WORK/tmux"
mkdir -p "$TMUX_TMPDIR" "$WORK/repo"
unset TMUX
export BUS_SESSION="version-test-$$"
BD="/tmp/muxcode-bus-$BUS_SESSION"
export MUXCODE_LIFECYCLE_LOG_DIR="$WORK/lifecycle"
: > "$WORK/empty-config"
export MUXCODE_CONFIG="$WORK/empty-config"
export MUXCODE_SESSION_REPO_DIR="$WORK/repo"
export MUXCODE_TMP_CLEANUP_THRESHOLD=0
export MUXCODE_BRANCH_TIME_DISABLE=1
export MUXCODE_CONTROL_PANE_DISABLE=1
export AGENT_ROLE=edit BUS_ROLE=edit
LIFELOG="$MUXCODE_LIFECYCLE_LOG_DIR/$BUS_SESSION.log"
REAL_LOG_DIR="$HOME/.config/muxcode/logs"
REAL_LOGS_BEFORE="$(ls -1 "$REAL_LOG_DIR" 2>/dev/null | sort)"

cleanup() {
  pkill -f "muxcode watch $BUS_SESSION" 2>/dev/null
  tmux kill-server 2>/dev/null
  rm -rf "$BD" "$WORK"
}
trap cleanup EXIT

# --- Scratch builds --------------------------------------------------------
BUSPKG=github.com/mkober/muxcode/tools/muxcode/bus
# build <dir> <version> <commit> <date> — a muxcode binary stamped with that
# identity. The file is named muxcode because upgrade-daemons discovers
# daemons by that exact basename.
build() {
  mkdir -p "$1"
  (cd "$MOD" && go build -buildvcs=false \
      -ldflags "-X $BUSPKG.Version=$2 -X $BUSPKG.Commit=$3 -X $BUSPKG.BuildDate=$4" \
      -o "$1/muxcode" .) || { echo "SKIP: go build failed for $2"; exit 2; }
}
DATE=2026-09-02T00:00:00Z
build "$WORK/rel"  v1.2.3            abc1234 "$DATE"
build "$WORK/desc" v1.2.3-4-gabc1234 abc1234 "$DATE"
build "$WORK/pre"  v1.2.3-rc1        abc1234 "$DATE"
build "$WORK/dev"  abc1234-dirty     abc1234 "$DATE"
build "$WORK/old"  v0.0.1-test       aaaaaaa "$DATE"
build "$WORK/new"  v0.0.2-test       bbbbbbb "$DATE"
REL="$WORK/rel/muxcode"; DESC="$WORK/desc/muxcode"; PRE="$WORK/pre/muxcode"
DEV="$WORK/dev/muxcode"; OLD="$WORK/old/muxcode"; NEW="$WORK/new/muxcode"

# --- 1. Identity -----------------------------------------------------------
echo "-- identity"
want="muxcode v1.2.3 (abc1234, $DATE, $(go env GOVERSION) $(go env GOOS)/$(go env GOARCH))"
line=$("$REL" version 2>&1); rc=$?
[ "$rc" -eq 0 ] && [ "$line" = "$want" ] && ok "stamped build reports itself: $line" \
  || fail "version: rc=$rc got '$line' want '$want'"

json=$("$REL" version --json 2>&1)
rebuilt=$(printf '%s' "$json" | jq -r '"muxcode \(.version) (\(.commit), \(.date), \(.go) \(.os)/\(.arch))"' 2>/dev/null)
[ "$rebuilt" = "$want" ] && ok "--json carries the same fields as the line" || fail "--json: $json"
keys=$(printf '%s' "$json" | jq -r 'keys | join(",")' 2>/dev/null)
[ "$keys" = "arch,commit,date,go,os,version" ] && ok "--json field set is exactly the documented contract" \
  || fail "--json keys: $keys"

# The launcher, had either flag reached it, would name a session after the
# cwd basename: the cwd, that bus dir and the private tmux server are the
# three places a regression leaves a trace.
PROJ="$WORK/proj-$$"
mkdir -p "$PROJ"
for flag in --version -v; do
  out=$(cd "$PROJ" && "$REL" "$flag" 2>&1); rc=$?
  [ "$rc" -eq 0 ] && [ "$out" = "$want" ] && ok "$flag prints the version line and exits 0" \
    || fail "$flag: rc=$rc out='$out'"
done
[ -z "$(ls -A "$PROJ")" ] && ok "--version/-v wrote nothing into the cwd" || fail "cwd gained: $(ls -A "$PROJ")"
[ ! -e "/tmp/muxcode-bus-proj-$$" ] && ok "--version/-v created no bus dir for the cwd" \
  || fail "bus dir /tmp/muxcode-bus-proj-$$ appeared"
if tmux ls >/dev/null 2>&1; then
  fail "--version/-v started a tmux server"
else
  ok "--version/-v started no tmux server"
fi

# --- 2. --at-least truth table ---------------------------------------------
echo "-- --at-least"
# row <bin> <want> <expected exit> <label>
row() {
  local got
  "$1" version --at-least "$2" >/dev/null 2>&1; got=$?
  [ "$got" -eq "$3" ] && ok "$4 → exit $got" || fail "$4: exit $got, want $3"
}
row "$REL"  v1.2.2  0 "v1.2.3 at-least v1.2.2 (lower)"
row "$REL"  v1.2.3  0 "v1.2.3 at-least v1.2.3 (equal)"
row "$REL"  v1.2.4  1 "v1.2.3 at-least v1.2.4 (higher patch)"
row "$REL"  v2.0.0  1 "v1.2.3 at-least v2.0.0 (higher major)"
row "$DESC" v1.2.3  0 "v1.2.3-4-gabc1234 at-least v1.2.3 (describe suffix sorts after its tag)"
row "$DESC" v1.2.4  1 "v1.2.3-4-gabc1234 at-least v1.2.4 (describe suffix stays below the next patch)"
row "$PRE"  v1.2.3  1 "v1.2.3-rc1 at-least v1.2.3 (pre-release sorts before its release)"
row "$PRE"  v1.2.2  0 "v1.2.3-rc1 at-least v1.2.2 (pre-release still above the previous patch)"
row "$REL"  abc1234 2 "uncomparable want (bare commit) is exit 2, not 1"
row "$DEV"  v0.1.0  2 "untagged dev build (abc1234-dirty) cannot be ranked → exit 2"
msg=$("$REL" version --at-least v1.2.4 2>&1 >/dev/null)
[[ "$msg" == *"v1.2.3 is older than required v1.2.4"* ]] && ok "older verdict names both versions on stderr" \
  || fail "stderr: $msg"

# --- 3. Daemon rollout -----------------------------------------------------
echo "-- daemon rollout"
# daemon_pid → the scratch session's daemon pid, found the way upgrade-daemons
# finds it: a process whose binary basename is muxcode running "watch <session>".
daemon_pid() {
  ps -axo pid=,command= | awk -v s="$BUS_SESSION" '$2 ~ /(^|\/)muxcode$/ && $3 == "watch" && $4 == s { print $1; exit }'
}
# wait_version <want> → true once daemon.version reports want (10s budget).
wait_version() {
  local i
  for i in $(seq 1 50); do
    [ "$(jq -r .version "$BD/daemon.version" 2>/dev/null)" = "$1" ] && return 0
    sleep 0.2
  done
  return 1
}
# mismatch_findings <diagnose json> → count of binary-daemon-version-mismatch
# findings. A report with no findings at all carries `findings: null`, which
# a bare `.findings[]` cannot iterate — the guard is what makes zero
# distinguishable from a parse error.
mismatch_findings() {
  printf '%s' "$1" | jq '[(.findings // [])[] | select(.failure_mode == "binary-daemon-version-mismatch")] | length' 2>/dev/null
}

tmux new-session -d -s "$BUS_SESSION" -n scratch -x 120 -y 30 -c "$WORK/repo"
"$NEW" init >/dev/null 2>&1
"$OLD" watch "$BUS_SESSION" --poll 2 >"$WORK/daemon-old.log" 2>&1 &
disown
wait_version v0.0.1-test && ok "scratch daemon from v0.0.1-test recorded daemon.version" \
  || fail "daemon.version never showed v0.0.1-test: $(cat "$BD/daemon.version" 2>/dev/null) $(tail -3 "$WORK/daemon-old.log" 2>/dev/null)"
pid0=$(daemon_pid)
[ -n "$pid0" ] && ok "daemon discoverable by ps as 'muxcode watch $BUS_SESSION' (pid $pid0)" \
  || fail "daemon not discoverable via ps"

out=$("$NEW" upgrade-daemons --dry-run --session "$BUS_SESSION" 2>&1)
[[ "$out" == *"$BUS_SESSION: daemon v0.0.1-test → installed v0.0.2-test — would restart"* ]] \
  && ok "dry-run names daemon v0.0.1-test → installed v0.0.2-test" || fail "dry-run: $out"
[ "$(printf '%s\n' "$out" | grep -c '')" -eq 1 ] && ok "--session scoped the plan to one session" \
  || fail "dry-run listed more than one session: $out"
[ "$(daemon_pid)" = "$pid0" ] && ok "dry-run left the daemon running (pid unchanged)" \
  || fail "dry-run changed the daemon pid"
out=$("$NEW" upgrade-daemons --dry-run --session "no-such-session-$$" 2>&1)
[[ "$out" == *"no running daemon found for session no-such-session-$$"* ]] \
  && ok "--session with no match plans nothing" || fail "unmatched --session: $out"

dj=$("$NEW" diagnose edit --json 2>/dev/null)
[ "$(printf '%s' "$dj" | jq -r .daemon_state.is_alive 2>/dev/null)" = "true" ] \
  && ok "diagnose sees the daemon alive before the rollout (mismatch check reachable)" \
  || fail "daemon not alive in diagnose: $(printf '%s' "$dj" | jq -c .daemon_state 2>/dev/null)"
[ "$(mismatch_findings "$dj")" = "1" ] && ok "diagnose reports binary-daemon-version-mismatch before the rollout" \
  || fail "mismatch findings before: $(mismatch_findings "$dj")"
builds=$(printf '%s' "$dj" | jq -r '.daemon_state.daemon_build.version + " vs " + .daemon_state.installed_build.version' 2>/dev/null)
[ "$builds" = "v0.0.1-test vs v0.0.2-test" ] && ok "diagnose evidence carries both builds" || fail "diagnose builds: $builds"
sev=$(printf '%s' "$dj" | jq -r '(.findings // [])[] | select(.failure_mode == "binary-daemon-version-mismatch") | .severity' 2>/dev/null)
[ "$sev" = "warning" ] && ok "mismatch is a warning, not critical" || fail "mismatch severity: $sev"

# Relaunch resolves "muxcode" on PATH, so the "installed" build goes first.
out=$(PATH="$WORK/new:$PATH" "$NEW" upgrade-daemons --session "$BUS_SESSION" 2>&1)
[[ "$out" == *"$BUS_SESSION: daemon v0.0.1-test → installed v0.0.2-test — daemon restarted"* ]] \
  && ok "real run cycled the stale daemon" || fail "upgrade: $out"
wait_version v0.0.2-test && ok "relaunched daemon recorded v0.0.2-test" \
  || fail "daemon.version after cycle: $(cat "$BD/daemon.version" 2>/dev/null)"
pid1=$(daemon_pid)
[ -n "$pid1" ] && [ "$pid1" != "$pid0" ] && ok "a new daemon process replaced pid $pid0 (now $pid1)" \
  || fail "daemon pid after cycle: '$pid1' (was $pid0)"
if kill -0 "$pid0" 2>/dev/null; then fail "old daemon pid $pid0 still alive"; else ok "old daemon pid $pid0 is gone"; fi
grep -q '"event":"daemon-upgraded"' "$LIFELOG" 2>/dev/null && ok "lifecycle logged daemon-upgraded" \
  || fail "no daemon-upgraded lifecycle row in $LIFELOG"

out=$(PATH="$WORK/new:$PATH" "$NEW" upgrade-daemons --session "$BUS_SESSION" 2>&1)
[[ "$out" == *"$BUS_SESSION: daemon v0.0.2-test → installed v0.0.2-test (current) — skipped"* ]] \
  && ok "second run skipped the now-current daemon" || fail "second run: $out"
[ "$(daemon_pid)" = "$pid1" ] && ok "skip left pid $pid1 untouched" || fail "skip changed the daemon pid"
grep -q '"event":"daemon-current"' "$LIFELOG" 2>/dev/null && ok "lifecycle logged daemon-current" \
  || fail "no daemon-current lifecycle row"

dj=$("$NEW" diagnose edit --json 2>/dev/null)
[ "$(printf '%s' "$dj" | jq -r .daemon_state.is_alive 2>/dev/null)" = "true" ] \
  && ok "diagnose still sees the daemon alive after the rollout" \
  || fail "daemon not alive after rollout: $(printf '%s' "$dj" | jq -c .daemon_state 2>/dev/null)"
[ "$(mismatch_findings "$dj")" = "0" ] && ok "no binary-daemon-version-mismatch finding after the rollout" \
  || fail "mismatch findings after: $(mismatch_findings "$dj")"

# --- Real install untouched ------------------------------------------------
REAL_LOGS_AFTER="$(ls -1 "$REAL_LOG_DIR" 2>/dev/null | sort)"
[ "$REAL_LOGS_BEFORE" = "$REAL_LOGS_AFTER" ] && ok "real lifecycle log dir untouched" \
  || fail "real lifecycle log dir changed during the test"

echo
echo "  $PASS passed, $FAIL failed"
# Coverage floor: the achievable maximum — a skipped section cannot green.
[ "$PASS" -ge 40 ] || { echo "FAIL: coverage floor not met ($PASS < 40)"; exit 1; }
[ "$FAIL" -eq 0 ] || exit 1
echo "OK"
