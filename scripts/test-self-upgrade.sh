#!/usr/bin/env bash
# Integration test for the self-upgrade pipeline (MUX-202): `muxcode upgrade`
# from Check to Reload tmux config, against a release that exists only on
# disk, restarting a real scratch daemon onto the binary it installs.
#
# Hermetic: two binaries are built here from this checkout with explicit -X
# stamps — v0.0.1-test, the "installed" build that runs the CLI and the
# scratch daemon, and v0.0.2-test, the "release" — so no tag has to exist and
# the installed muxcode is never run. The release lookup and the source
# tarball are local files reached through MUXCODE_UPGRADE_API_URL and
# MUXCODE_UPGRADE_TARBALL_URL; a fake `make` on PATH records each call and, for
# install, copies $FAKE_INSTALL_BIN into BINDIR with a marker tmux.conf. HOME,
# XDG_CACHE_HOME, BINDIR, CONFIGDIR, BUS_SESSION, the lifecycle log and the
# tmux server (TMUX_TMPDIR) are scratch.
#
# Every run that can reach Restart daemons is bound with --expect-tag and
# --expect-session, so upgrade-daemons is scoped to the scratch session: an
# unbound run restarts every stale daemon on the machine, live sessions
# included. The last section checks that no other daemon was touched.
#
# Skips exit 2, not 0, and a coverage floor keeps a partially executed run
# from reporting green.
#
# REQUIRES: go, tmux, jq, tar.
#
# Usage: bash scripts/test-self-upgrade.sh
set -uo pipefail

PASS=0
FAIL=0
ok()   { PASS=$((PASS + 1)); echo "  ok: $1"; }
fail() { FAIL=$((FAIL + 1)); echo "  FAIL: $1"; }

for tool in go tmux jq tar; do
  command -v "$tool" >/dev/null 2>&1 || { echo "SKIP: $tool is required"; exit 2; }
done
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MOD="$ROOT/tools/muxcode"
[ -f "$MOD/go.mod" ] || { echo "SKIP: $MOD/go.mod not found"; exit 2; }

sha256() {
  if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1"; else shasum -a 256 "$1"; fi | cut -d' ' -f1
}
mtime() { stat -f %m "$1" 2>/dev/null || stat -c %Y "$1"; }
# daemon_pid → the scratch session's daemon pid, found the way upgrade-daemons
# finds it: a process whose binary basename is muxcode running "watch <session>".
daemon_pid() {
  ps -axo pid=,command= | awk -v s="$BUS_SESSION" '$2 ~ /(^|\/)muxcode$/ && $3 == "watch" && $4 == s { print $1; exit }'
}
daemon_cmd() { ps -o command= -p "$1" 2>/dev/null | awk '{ print $1 }'; }
# other_daemons → every muxcode daemon on the machine that is not the scratch one.
other_daemons() {
  ps -axo pid=,command= | awk -v s="$BUS_SESSION" '$2 ~ /(^|\/)muxcode$/ && $3 == "watch" && index($0, " " s) == 0 { print $1 }' | sort
}

# Named before the snapshot below: other_daemons excludes $BUS_SESSION, and the
# caller's own session name would hide exactly the daemons it must count.
export BUS_SESSION="self-upgrade-test-$$"
REAL_HOME="$HOME"
REAL_LOG_DIR="$REAL_HOME/.config/muxcode/logs"
REAL_LOGS_BEFORE="$(ls -1 "$REAL_LOG_DIR" 2>/dev/null | sort)"
REAL_BIN="$REAL_HOME/.local/bin/muxcode"
REAL_BIN_MTIME_BEFORE="$(mtime "$REAL_BIN" 2>/dev/null)"
OTHER_DAEMONS_BEFORE="$(other_daemons)"

# --- Scratch builds (before HOME moves, so the Go cache is the real one) -----
WORK=$(mktemp -d /tmp/self-upgrade-test-XXXXXX)
BUSPKG=github.com/mkober/muxcode/tools/muxcode/bus
build() {
  mkdir -p "$1"
  (cd "$MOD" && go build -buildvcs=false \
      -ldflags "-X $BUSPKG.Version=$2 -X $BUSPKG.Commit=$3 -X $BUSPKG.BuildDate=2026-10-07T00:00:00Z" \
      -o "$1/muxcode" .) || { echo "SKIP: go build failed for $2"; rm -rf "$WORK"; exit 2; }
}
build "$WORK/old" v0.0.1-test aaaaaaa
build "$WORK/new" v0.0.2-test bbbbbbb
OLD="$WORK/old/muxcode"; NEW="$WORK/new/muxcode"
TAG=v0.0.2-test

# --- Isolation -------------------------------------------------------------
export TMUX_TMPDIR="$WORK/tmux"
unset TMUX
export HOME="$WORK/home"
export XDG_CACHE_HOME="$WORK/cache"
export BINDIR="$WORK/bin"
export CONFIGDIR="$WORK/config"
BD="/tmp/muxcode-bus-$BUS_SESSION"
export MUXCODE_LIFECYCLE_LOG_DIR="$WORK/lifecycle"
mkdir -p "$TMUX_TMPDIR" "$HOME" "$BINDIR" "$WORK/repo" "$WORK/fakebin"
: > "$WORK/empty-config"
export MUXCODE_CONFIG="$WORK/empty-config"
export MUXCODE_SESSION_REPO_DIR="$WORK/repo"
export MUXCODE_TMP_CLEANUP_THRESHOLD=0
export MUXCODE_BRANCH_TIME_DISABLE=1
export MUXCODE_CONTROL_PANE_DISABLE=1
export AGENT_ROLE=edit BUS_ROLE=edit
unset GITHUB_TOKEN GH_TOKEN
LIFELOG="$MUXCODE_LIFECYCLE_LOG_DIR/$BUS_SESSION.log"
CACHE="$XDG_CACHE_HOME/muxcode/upgrade/$TAG"

cleanup() {
  pkill -f "muxcode watch $BUS_SESSION" 2>/dev/null
  tmux kill-server 2>/dev/null
  rm -rf "$BD" "$WORK"
}
trap cleanup EXIT

# --- Release on disk, fake make --------------------------------------------
mkdir -p "$WORK/stage/muxcode-0.0.2-test"
printf 'install:\n' > "$WORK/stage/muxcode-0.0.2-test/Makefile"
tar -czf "$WORK/src.tgz" -C "$WORK/stage" muxcode-0.0.2-test
printf '{"tag_name":"%s","published_at":"2026-10-07T00:00:00Z"}\n' "$TAG" > "$WORK/latest.json"
export MUXCODE_UPGRADE_API_URL="file://$WORK/latest.json"
export MUXCODE_UPGRADE_TARBALL_URL="file://$WORK/src.tgz"

cat > "$WORK/fakebin/make" <<'EOF'
#!/bin/sh
echo "$(pwd -P) $*" >> "$FAKE_MAKE_LOG"
target=$1
for a in "$@"; do
  case "$a" in
    BINDIR=*) bindir=${a#BINDIR=} ;;
    CONFIGDIR=*) configdir=${a#CONFIGDIR=} ;;
  esac
done
if [ "$FAKE_MAKE_FAIL" = "$target" ]; then
  echo "fake make: $target failed" >&2
  exit 1
fi
if [ "$target" = install ]; then
  mkdir -p "$bindir" "$configdir"
  cp "$FAKE_INSTALL_BIN" "$bindir/muxcode.tmp" && mv "$bindir/muxcode.tmp" "$bindir/muxcode"
  chmod 755 "$bindir/muxcode"
  echo 'set -g @muxcode_upgrade_test 1' > "$configdir/tmux.conf"
fi
echo "fake make $target done"
EOF
chmod 755 "$WORK/fakebin/make"
export FAKE_MAKE_LOG="$WORK/make.log" FAKE_MAKE_FAIL="" FAKE_INSTALL_BIN="$NEW"
# BINDIR first: Verify requires the muxcode on PATH to be the installed one.
export PATH="$BINDIR:$WORK/fakebin:$PATH"
cp "$OLD" "$BINDIR/muxcode"
cp "$OLD" "$WORK/installed-before"

# upgrade <binary> [flags...] → a full run bound to the release and the scratch session.
upgrade() {
  local bin=$1; shift
  "$bin" upgrade --expect-tag "$TAG" --expect-session "$BUS_SESSION" "$@" 2>&1
}
wait_version() {
  local i
  for i in $(seq 1 50); do
    [ "$(jq -r .version "$BD/daemon.version" 2>/dev/null)" = "$1" ] && return 0
    sleep 0.2
  done
  return 1
}
last_failed() { jq -r 'select(.event == "upgrade-failed") | .detail' "$LIFELOG" 2>/dev/null | tail -1; }
unchanged() { cmp -s "$WORK/installed-before" "$BINDIR/muxcode"; }

# --- Scratch daemon --------------------------------------------------------
echo "-- scratch daemon"
tmux new-session -d -s "$BUS_SESSION" -n scratch -x 120 -y 30 -c "$WORK/repo"
"$OLD" init >/dev/null 2>&1
"$OLD" watch "$BUS_SESSION" --poll 2 >"$WORK/daemon.log" 2>&1 &
disown
wait_version v0.0.1-test && ok "scratch daemon on v0.0.1-test recorded daemon.version" \
  || fail "daemon.version never showed v0.0.1-test: $(tail -3 "$WORK/daemon.log" 2>/dev/null)"
PID0=$(daemon_pid)
[ -n "$PID0" ] && ok "daemon discoverable as 'muxcode watch $BUS_SESSION' (pid $PID0)" || fail "daemon not discoverable via ps"

# --- 1. --check ------------------------------------------------------------
echo "-- --check"
out=$("$OLD" upgrade --check 2>&1); rc=$?
[ "$rc" -eq 10 ] && ok "--check exits 10 against a newer release" || fail "--check rc=$rc: $out"
[[ "$out" == *"installed v0.0.1-test → latest $TAG available"* ]] && ok "--check names both versions" || fail "--check output: $out"
out=$("$NEW" upgrade --check 2>&1); rc=$?
[ "$rc" -eq 0 ] && ok "--check exits 0 when installed is the release" || fail "current --check rc=$rc: $out"
[[ "$out" == *"installed $TAG is current"* ]] && ok "--check reports current explicitly" || fail "current output: $out"
out=$(MUXCODE_UPGRADE_API_URL="file://$WORK/missing.json" "$OLD" upgrade --check 2>&1); rc=$?
[ "$rc" -eq 1 ] && ok "--check exits 1 when the release file is missing" || fail "missing-API rc=$rc: $out"
[[ "$out" == *"HTTP 404"* ]] && ok "the failed lookup names HTTP 404" || fail "missing-API output: $out"
json=$("$OLD" upgrade --check --json 2>/dev/null)
[ "$(printf '%s' "$json" | jq -r '.verdict + " " + .latest.tag' 2>/dev/null)" = "newer $TAG" ] \
  && ok "--check --json carries verdict and latest tag" || fail "--check --json: $json"
[ ! -e "$XDG_CACHE_HOME/muxcode" ] && ok "--check wrote nothing to the cache" || fail "--check created the cache"

# --- 2. Negative control: up to date touches nothing -----------------------
echo "-- up to date"
m0=$(mtime "$BINDIR/muxcode")
out=$(upgrade "$NEW"); rc=$?
[ "$rc" -eq 0 ] && ok "an up-to-date run exits 0" || fail "up-to-date rc=$rc: $out"
[[ "$out" == *"Check: installed $TAG is current"* && "$out" != *"Download:"* ]] \
  && ok "it stops after Check" || fail "up-to-date output: $out"
[ ! -e "$XDG_CACHE_HOME/muxcode/upgrade" ] && ok "no download: the cache was never created" || fail "up-to-date created the cache"
[ "$(mtime "$BINDIR/muxcode")" = "$m0" ] && unchanged && ok "BINDIR untouched (bytes and mtime)" || fail "up-to-date changed BINDIR"
[ ! -e "$FAKE_MAKE_LOG" ] && ok "make never ran" || fail "make ran: $(cat "$FAKE_MAKE_LOG")"
[ "$(daemon_pid)" = "$PID0" ] && ok "daemon untouched (pid $PID0)" || fail "up-to-date changed the daemon pid"

# --- 3. Negative control: a failed build -----------------------------------
echo "-- failed build"
out=$(FAKE_MAKE_FAIL=build upgrade "$OLD"); rc=$?
[ "$rc" -eq 1 ] && [[ "$out" == *"Build: FAILED"* ]] && ok "a failing make build fails the Build step, exit 1" \
  || fail "failed build rc=$rc: $out"
sum=$(sha256 "$WORK/src.tgz")
dl=$(printf '%s\n' "$out" | grep '^Download:')
[[ "$dl" == *"sha256 $sum"* && "$dl" != *"cache hit"* ]] && ok "first run fetched the tarball and recorded its sha256" \
  || fail "Download row: $dl"
rec=$(jq -r '"\(.sha256) \(.size)"' "$CACHE/download.json" 2>/dev/null)
[ "$rec" = "$sum $(wc -c < "$WORK/src.tgz" | tr -d ' ')" ] && ok "download.json records the tarball's sha256 and size" \
  || fail "download.json: $rec"
unchanged && ok "installed binary byte-identical after the failed build" || fail "failed build changed the installed binary"
[ "$(daemon_pid)" = "$PID0" ] && [ "$(jq -r .version "$BD/daemon.version")" = v0.0.1-test ] \
  && ok "daemon not restarted" || fail "failed build restarted the daemon"
f=$(last_failed)
[[ "$f" == *"Build: "* && "$f" == *"$CACHE/build.log"* ]] && ok "upgrade-failed names the Build step and the log path" \
  || fail "upgrade-failed: $f"
! grep -q " install " "$FAKE_MAKE_LOG" && ok "make install never ran after the failed build" || fail "make install ran"

# --- 4. Negative control: a failed install ---------------------------------
echo "-- failed install"
out=$(FAKE_MAKE_FAIL=install upgrade "$OLD"); rc=$?
[ "$rc" -eq 1 ] && [[ "$out" == *"Install: FAILED"* ]] && ok "a make install exiting 1 fails the Install step" \
  || fail "failed install rc=$rc: $out"
unchanged && ok "installed binary byte-identical after the failed install" || fail "failed install changed the installed binary"
[ "$(daemon_pid)" = "$PID0" ] && ok "daemon not restarted" || fail "failed install restarted the daemon"
f=$(last_failed)
[[ "$f" == *"Install: "* && "$f" == *"$CACHE/build.log"* ]] && ok "upgrade-failed names the Install step and the log path" \
  || fail "upgrade-failed: $f"

# --- 5. Negative control: the wrong binary fails Verify ---------------------
echo "-- wrong version"
out=$(FAKE_INSTALL_BIN="$OLD" upgrade "$OLD"); rc=$?
[ "$rc" -eq 1 ] && [[ "$out" == *"Verify: FAILED"*"reports v0.0.1-test, want $TAG"* ]] \
  && ok "a binary reporting the wrong version fails Verify, naming both" || fail "verify rc=$rc: $out"
[ "$(daemon_pid)" = "$PID0" ] && [ "$(jq -r .version "$BD/daemon.version")" = v0.0.1-test ] \
  && ok "daemon not restarted after the failed Verify" || fail "failed Verify restarted the daemon"
[ -z "$(tmux show-options -gqv @muxcode_upgrade_test 2>/dev/null)" ] && ok "tmux config not reloaded after the failed Verify" \
  || fail "Reload tmux config ran after the failed Verify"

# --- 6. Full upgrade --------------------------------------------------------
echo "-- full upgrade"
out=$(upgrade "$OLD"); rc=$?
[ "$rc" -eq 0 ] && ok "full upgrade exits 0" || fail "full upgrade rc=$rc: $out"
[[ "$(printf '%s\n' "$out" | grep '^Download:')" == *"cache hit"* ]] && ok "a later run against the same tag skips the download" \
  || fail "Download row: $(printf '%s\n' "$out" | grep '^Download:')"
src=$(cd "$CACHE/src" 2>/dev/null && pwd -P)
calls=$(tail -2 "$FAKE_MAKE_LOG" | awk '{ print $1, $2, $3 }' | paste -sd'|' -)
[ -n "$src" ] && [ "$calls" = "$src build VERSION=$TAG|$src install VERSION=$TAG" ] \
  && ok "make build then make install VERSION=$TAG ran in the extracted tree" || fail "make calls: $calls (tree $src)"
[[ "$out" == *"Verify: $BINDIR/muxcode reports $TAG"* ]] && ok "Verify passed against the installed binary" || fail "Verify row: $out"
[ "$("$BINDIR/muxcode" version --json | jq -r .version)" = "$TAG" ] && ok "BINDIR now holds $TAG" || fail "BINDIR version"
wait_version "$TAG" && ok "the scratch daemon was restarted onto $TAG" || fail "daemon.version after: $(cat "$BD/daemon.version" 2>/dev/null)"
PID1=$(daemon_pid)
[ -n "$PID1" ] && [ "$PID1" != "$PID0" ] && ok "a new daemon process replaced pid $PID0 (now $PID1)" || fail "daemon pid after: '$PID1'"
[ "$(daemon_cmd "$PID1")" = "$BINDIR/muxcode" ] && ok "the new daemon runs from BINDIR" || fail "new daemon runs $(daemon_cmd "$PID1")"
kill -0 "$PID0" 2>/dev/null && fail "old daemon pid $PID0 still alive" || ok "old daemon pid $PID0 is gone"
[[ "$out" == *"  $BUS_SESSION: "*"daemon restarted"* ]] && ok "Restart daemons lists the scratch session" || fail "daemon rows: $out"
[ "$(tmux show-options -gqv @muxcode_upgrade_test 2>/dev/null)" = 1 ] && ok "Reload tmux config sourced the installed tmux.conf" \
  || fail "tmux option not set"
seq=$(jq -r 'select(.source == "upgrade") | .event' "$LIFELOG" 2>/dev/null | grep '^upgrade-' | tail -8 | paste -sd, -)
[ "$seq" = "upgrade-check,upgrade-download,upgrade-build,upgrade-install,upgrade-verify,upgrade-daemons,upgrade-tmux,upgrade-done" ] \
  && ok "lifecycle rows upgrade-check … upgrade-done, in order" || fail "lifecycle sequence: $seq"
[[ "$out" == *"upgraded v0.0.1-test → $TAG; agents keep running until restarted"* ]] && ok "done line names the delta and the agent follow-up" \
  || fail "done line: $out"

# --- Real install untouched ------------------------------------------------
echo "-- real install"
[ "$(other_daemons)" = "$OTHER_DAEMONS_BEFORE" ] && ok "no other daemon on the machine was restarted" \
  || fail "other daemons changed: before [$OTHER_DAEMONS_BEFORE] after [$(other_daemons)]"
[ "$REAL_LOGS_BEFORE" = "$(ls -1 "$REAL_LOG_DIR" 2>/dev/null | sort)" ] && ok "real lifecycle log dir untouched" \
  || fail "real lifecycle log dir changed"
[ "$(mtime "$REAL_BIN" 2>/dev/null)" = "$REAL_BIN_MTIME_BEFORE" ] && ok "real ~/.local/bin/muxcode untouched" \
  || fail "real ~/.local/bin/muxcode changed"

echo
echo "  $PASS passed, $FAIL failed"
# Coverage floor: the achievable maximum — a skipped section cannot green.
[ "$PASS" -ge 46 ] || { echo "FAIL: coverage floor not met ($PASS < 46)"; exit 1; }
[ "$FAIL" -eq 0 ] || exit 1
echo "OK"
