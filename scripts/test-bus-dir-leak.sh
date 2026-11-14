#!/usr/bin/env bash
# Integration test for test bus-directory and memory leaks (MUX-137) and the
# global memory directory (MUX-224).
#
# The PreLaunch tests once "isolated" through BUS_DIR_BASE, a variable nothing
# reads, and five tests passed Init an empty memory dir, which seeds shared.md
# into the package source tree. Both passed whatever happened, because their
# assertions resolve the same path as the write. This script judges the suite
# by what it leaves behind instead:
#
#   1. Main pass: seed a synthetic live session bus (inbox, history, task,
#      graph, lock files) in the real state base, snapshot /tmp/muxcode-bus-*,
#      the state base's bus directories and every .muxcode/memory file under
#      tools/, run the full ./test.sh with test caching off (GOFLAGS
#      -count=1; its logs must show no cached result, or the side effects
#      measured never ran), snapshot again — nothing new may appear, and the
#      sentinel's files must match their checksums. Controls: a copy of the
#      sentinel with one file mutated, and one with a file deleted under a
#      surviving directory, must both be rejected.
#   2. Negative control: in a throwaway copy of the module, revert the fix —
#      the Auto PreLaunch call site back to t.TempDir + BUS_DIR_BASE, one Init
#      back to an empty memory dir, and TestMain's XDG_STATE_HOME and
#      BUS_MEMORY_DIR pins removed — and run those tests with XDG_STATE_HOME
#      pointed at a scratch "real" state root. The same detector must report
#      the two leaks, verbatim:
#        LEAK <work>/fake-state/muxcode/bus/muxcode-bus-test-prelaunch-auto
#        LEAK <work>/tree/muxcode/bus/.muxcode/memory/shared.md
#      so a green main pass proves the check can see a leak.
#   3. Global memory (MUX-224): the main pass also requires the real
#      ~/.config/muxcode/memory byte for byte unchanged (or still absent). That
#      alone cannot fail where the directory already exists — Init's MkdirAll
#      and the readers change no byte — so the full ./test.sh runs again under
#      a scratch HOME holding no global memory, which must not be created.
#   4. Negative control: a throwaway copy with every TestMain's
#      BUS_GLOBAL_MEMORY_DIR pin removed, run under another scratch HOME, must
#      create that HOME's global memory and fail TestGlobalMemoryDir_IsolatedFromHome
#      naming the missing pin.
#
# The real tree and the real state directory are only read; the control's
# leaks land in its own scratch copy and scratch state root. A live agent's
# `muxcode memory write --global` during the run would fail section 1's global
# memory check — rerun on a quiet session.
#
# REQUIRES: go, and a tree whose ./test.sh passes (a partial suite run proves
# nothing about the tests it never reached). ~6-12 min, two suite runs.
#
# Usage: bash scripts/test-bus-dir-leak.sh
set -uo pipefail

FLOOR=22
REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
command -v go >/dev/null 2>&1 || { echo "  FAIL  go not available"; exit 1; }

GREEN=$'\033[0;32m'; RED=$'\033[0;31m'; NC=$'\033[0m'
pass=0; fail=0
ok()  { echo "  ${GREEN}PASS${NC}  $*"; pass=$((pass + 1)); }
bad() { echo "  ${RED}FAIL${NC}  $*"; fail=$((fail + 1)); }

WORK="$(mktemp -d /tmp/bus-dir-leak.XXXXXX)"
cleanup() {
  [ -n "${SENT:-}" ] && rm -rf "$SENT"
  [ "${MUXCODE_TEST_KEEP:-}" = "1" ] && { echo "  (kept for diagnosis: $WORK)"; return; }
  rm -rf "$WORK"
}
trap cleanup EXIT

# snap <state-base> <tree> — every path a leak would create, sorted.
snap() {
  {
    ls -d /tmp/muxcode-bus-* 2>/dev/null
    ls -d "$1"/muxcode-bus-* 2>/dev/null
    find "$2" -path '*/.muxcode/memory/*' -type f 2>/dev/null
  } | sort
}

# leaks <before-file> <after-file> — one "LEAK <path>" line per new path.
leaks() { comm -13 "$1" "$2" | sed 's/^/LEAK /'; }

# manifest <dir> — every file's path and checksum, relative to dir.
manifest() { (cd "$1" && find . -type f | sort | xargs shasum 2>/dev/null); }

# unchanged <dir> <manifest-file> — true when dir's files match the manifest.
unchanged() { [ -d "$1" ] && diff -q <(manifest "$1") "$2" >/dev/null; }

# gm_state <dir> — every entry and file checksum under a global memory dir,
# or ABSENT; directories are listed so a created subdirectory shows too.
gm_state() {
  [ -d "$1" ] || { echo ABSENT; return; }
  (cd "$1" && find . | sort | while IFS= read -r p; do
    if [ -f "$p" ]; then shasum "$p"; else echo "dir $p"; fi
  done)
}

# global_mem <home> — the global memory directory a home resolves to.
global_mem() { echo "$1/.config/muxcode/memory"; }

REAL_GOCACHE="$(go env GOCACHE)"; REAL_GOPATH="$(go env GOPATH)"; REAL_GOENV="$(go env GOENV)"
GITCFG=""
for f in "$HOME/.gitconfig" "${XDG_CONFIG_HOME:-$HOME/.config}/git/config"; do
  [ -f "$f" ] && { GITCFG="$f"; break; }
done

# in_home <home> <cmd...> — run with HOME replaced and no inherited memory
# override, keeping the real Go caches and git identity readable.
in_home() {
  local home="$1"; shift
  env -u BUS_GLOBAL_MEMORY_DIR -u BUS_MEMORY_DIR HOME="$home" GOCACHE="$REAL_GOCACHE" \
    GOPATH="$REAL_GOPATH" GOENV="$REAL_GOENV" ${GITCFG:+"GIT_CONFIG_GLOBAL=$GITCFG"} "$@"
}

# --- 1. Main pass: the full suite leaves nothing behind --------------------
echo "--- 1. full ./test.sh run, uncached"
STATE_BASE=""
command -v muxcode >/dev/null 2>&1 && STATE_BASE="$(muxcode bus-dir leak-probe 2>/dev/null | xargs dirname 2>/dev/null)"
[ -n "$STATE_BASE" ] || STATE_BASE="${MUXCODE_BUS_BASE:-${XDG_STATE_HOME:-$HOME/.local/state}/muxcode/bus}"

# A synthetic live session in the real state base: real sessions change while
# the suite runs, so content survival is judged on a bus only this script owns.
SENT="$STATE_BASE/muxcode-bus-leak-sentinel-$$"
mkdir -p "$SENT/inbox" "$SENT/lock" "$SENT/tasks" "$SENT/graphs/1790000000-sentinel/nodes"
echo '{"id":"1790000000-edit-sentinel","from":"edit","to":"test","type":"request","action":"test","payload":"sentinel"}' > "$SENT/inbox/test.jsonl"
: > "$SENT/inbox/edit.jsonl"
echo '{"ts":1790000000,"command":"./test.sh","exit_code":"0","outcome":"success"}' > "$SENT/test-history.jsonl"
echo '{"id":"1790000000-edit-sentinel","status":"in-flight","timeout":600}' > "$SENT/tasks/1790000000-edit-sentinel.json"
echo '{"name":"sentinel","start":"a"}' > "$SENT/graphs/1790000000-sentinel/graph.json"
echo '{"node_id":"a","state":"running"}' > "$SENT/graphs/1790000000-sentinel/nodes/a.json"
echo stopped > "$SENT/lock/build.stopped"
manifest "$SENT" > "$WORK/sentinel.manifest"
nfiles="$(grep -c . "$WORK/sentinel.manifest")"
[ "$nfiles" -eq 7 ] && ok "sentinel live bus seeded with $nfiles files in $STATE_BASE" \
  || bad "sentinel seeded $nfiles files, want 7"

snap "$STATE_BASE" "$REPO/tools" > "$WORK/main-before"
REAL_GMEM="$(global_mem "$HOME")"
gm_state "$REAL_GMEM" > "$WORK/gmem-before"
(cd "$REPO" && env -u BUS_GLOBAL_MEMORY_DIR MUXCODE_TEST_LOG_DIR="$WORK/test-logs" GOFLAGS="${GOFLAGS:+$GOFLAGS }-count=1" bash ./test.sh) > "$WORK/suite.log" 2>&1
rc=$?
if [ "$rc" -eq 0 ] && grep -q '^=== Summary ===' "$WORK/suite.log"; then
  ok "full suite ran to completion ($(grep -E '^modules=' "$WORK/suite.log" | tail -1))"
else
  bad "suite exit $rc — a partial run proves nothing; last lines:"
  tail -15 "$WORK/suite.log" | sed 's/^/        /'
fi
nok="$(cat "$WORK"/test-logs/*/*.log 2>/dev/null | grep -c '^ok ')"
ncached="$(cat "$WORK"/test-logs/*/*.log 2>/dev/null | grep -c '(cached)')"
[ "$nok" -ge 1 ] && [ "$ncached" -eq 0 ] \
  && ok "every package executed: $nok ok, 0 cached results" \
  || bad "suite results not all executed: $nok ok, $ncached cached — the side effects measured may never have run"
snap "$STATE_BASE" "$REPO/tools" > "$WORK/main-after"
leaks "$WORK/main-before" "$WORK/main-after" > "$WORK/main-leaks"

grep -q '^LEAK /tmp/muxcode-bus-' "$WORK/main-leaks" \
  && bad "suite created /tmp bus dirs: $(grep '^LEAK /tmp/' "$WORK/main-leaks" | tr '\n' ' ')" \
  || ok "no /tmp/muxcode-bus-* directory created"
grep -qF "LEAK $STATE_BASE/" "$WORK/main-leaks" \
  && bad "suite created bus dirs in the real state base: $(grep -F "LEAK $STATE_BASE/" "$WORK/main-leaks" | tr '\n' ' ')" \
  || ok "no bus directory created in the real state base $STATE_BASE"
grep -q '/\.muxcode/memory/' "$WORK/main-leaks" \
  && bad "suite wrote memory into the source tree: $(grep '/\.muxcode/memory/' "$WORK/main-leaks" | tr '\n' ' ')" \
  || ok "no memory file created under tools/"
unchanged "$SENT" "$WORK/sentinel.manifest" \
  && ok "the sentinel live bus survived the suite byte for byte" \
  || { bad "the sentinel live bus changed during the suite:"; diff <(manifest "$SENT") "$WORK/sentinel.manifest" | sed 's/^/        /'; }
gm_state "$REAL_GMEM" > "$WORK/gmem-after"
if diff -q "$WORK/gmem-before" "$WORK/gmem-after" >/dev/null; then
  if grep -qx ABSENT "$WORK/gmem-before"; then ok "real global memory $REAL_GMEM still absent after the suite"
  else ok "real global memory $REAL_GMEM unchanged byte for byte ($(grep -c . "$WORK/gmem-before") entries)"; fi
else
  bad "the suite changed the real global memory $REAL_GMEM:"
  diff "$WORK/gmem-before" "$WORK/gmem-after" | head -10 | sed 's/^/        /'
fi

# The preservation check must reject a bus whose directory survives but whose
# files do not — a mutated file and a deleted one.
cp -R "$SENT" "$WORK/sentinel-copy"
unchanged "$WORK/sentinel-copy" "$WORK/sentinel.manifest" \
  && ok "control: an untouched copy of the sentinel passes" || bad "control: an untouched copy fails — the check is broken"
echo '{"id":"injected"}' >> "$WORK/sentinel-copy/inbox/test.jsonl"
unchanged "$WORK/sentinel-copy" "$WORK/sentinel.manifest" \
  && bad "control: a mutated inbox still passed — content changes go unseen" \
  || ok "control: a mutated inbox file is rejected"
cp "$SENT/inbox/test.jsonl" "$WORK/sentinel-copy/inbox/test.jsonl"
rm "$WORK/sentinel-copy/graphs/1790000000-sentinel/graph.json"
unchanged "$WORK/sentinel-copy" "$WORK/sentinel.manifest" \
  && bad "control: a deleted graph.json still passed — a surviving directory hid it" \
  || ok "control: a deleted file under a surviving directory is rejected"
rm -rf "$SENT"

# --- 2. Negative control: the fix reverted leaks, and the detector sees it -
echo "--- 2. control: fix reverted in a throwaway copy"
TREE="$WORK/tree"
mkdir -p "$TREE" && cp -R "$REPO/tools/muxcode" "$TREE/muxcode" && rm -rf "$TREE/muxcode/bus/.muxcode" "$TREE/muxcode/.muxcode"
B="$TREE/muxcode/bus"

perl -0pi -e 's/dir := useTempBusDir\(t\)\n\tsession := "test-prelaunch-auto"/dir := t.TempDir()\n\tt.Setenv("BUS_DIR_BASE", dir)\n\tsession := "test-prelaunch-auto"/' "$B/launch_test.go"
grep -q 't.Setenv("BUS_DIR_BASE", dir)' "$B/launch_test.go" \
  && ok "reverted: Auto PreLaunch isolates through BUS_DIR_BASE" || bad "BUS_DIR_BASE revert did not apply"
perl -pi -e 's/Init\("test-role-env", dir\)/Init("test-role-env", "")/' "$B/launch_test.go"
grep -q 'Init("test-role-env", "")' "$B/launch_test.go" \
  && ok "reverted: SetsAgentRole passes Init an empty memory dir" || bad "empty memory dir revert did not apply"
perl -ni -e 'print unless /os\.Setenv\("(XDG_STATE_HOME|BUS_MEMORY_DIR)"/' "$B/main_test.go"
grep -qE 'Setenv\("(XDG_STATE_HOME|BUS_MEMORY_DIR)"' "$B/main_test.go" \
  && bad "TestMain pins still present" || ok "removed: TestMain's XDG_STATE_HOME and BUS_MEMORY_DIR pins"

FAKE="$WORK/fake-state"
mkdir -p "$FAKE/muxcode/bus"
snap "$FAKE/muxcode/bus" "$TREE" > "$WORK/ctl-before"
(cd "$TREE/muxcode" && env -u BUS_MEMORY_DIR -u MUXCODE_BUS_BASE XDG_STATE_HOME="$FAKE" \
  go test ./bus -run '^(TestPreLaunchSetup_AutoStartupMessage|TestRunAgentLaunch_SetsAgentRole)$' -count=1 \
  > "$WORK/ctl-test.log" 2>&1)
snap "$FAKE/muxcode/bus" "$TREE" > "$WORK/ctl-after"
leaks "$WORK/ctl-before" "$WORK/ctl-after" | sed "s|$WORK|<work>|g" > "$WORK/ctl-leaks"
echo "  control detector output:"; sed 's/^/        /' "$WORK/ctl-leaks"

grep -qFx 'LEAK <work>/fake-state/muxcode/bus/muxcode-bus-test-prelaunch-auto' "$WORK/ctl-leaks" \
  && ok "control: the reverted call site's bus dir is reported as a leak" \
  || { bad "control: bus-dir leak not reported — the main pass's silence proves nothing"; tail -5 "$WORK/ctl-test.log" | sed 's/^/        /'; }
grep -qFx 'LEAK <work>/tree/muxcode/bus/.muxcode/memory/shared.md' "$WORK/ctl-leaks" \
  && ok "control: the empty memory dir's shared.md is reported as a leak" \
  || bad "control: memory leak not reported"

# --- 3. Global memory: the full suite under a scratch HOME creates none ------
echo "--- 3. full ./test.sh run under a scratch HOME (MUX-224)"
SHOME="$WORK/home"; mkdir -p "$SHOME"
[ ! -e "$(global_mem "$SHOME")" ] && ok "scratch HOME holds no global memory before the run" \
  || bad "scratch HOME already holds $(global_mem "$SHOME")"
(cd "$REPO" && in_home "$SHOME" env MUXCODE_TEST_LOG_DIR="$WORK/home-test-logs" \
  GOFLAGS="${GOFLAGS:+$GOFLAGS }-count=1" bash ./test.sh) > "$WORK/home-suite.log" 2>&1
rc=$?
hok="$(cat "$WORK"/home-test-logs/*/*.log 2>/dev/null | grep -c '^ok ')"
hcached="$(cat "$WORK"/home-test-logs/*/*.log 2>/dev/null | grep -c '(cached)')"
if [ "$rc" -eq 0 ] && grep -q '^=== Summary ===' "$WORK/home-suite.log" && [ "$hok" -ge 1 ] && [ "$hcached" -eq 0 ]; then
  ok "full suite passed under the scratch HOME ($hok ok, 0 cached)"
else
  bad "scratch-HOME suite exit $rc, $hok ok, $hcached cached — a partial run proves nothing; last lines:"
  tail -15 "$WORK/home-suite.log" | sed 's/^/        /'
fi
[ ! -e "$(global_mem "$SHOME")" ] \
  && ok "the suite created no global memory under the scratch HOME" \
  || bad "the suite created $(global_mem "$SHOME"): $(cd "$(global_mem "$SHOME")" && find . | tr '\n' ' ')"

# --- 4. Negative control: global-memory pins removed -----------------------
echo "--- 4. control: BUS_GLOBAL_MEMORY_DIR pins removed in a throwaway copy"
GTREE="$WORK/gtree"
mkdir -p "$GTREE" && cp -R "$REPO/tools/muxcode" "$GTREE/muxcode"
for f in bus/main_test.go cmd/main_test.go daemon/main_test.go tui/main_test.go; do
  perl -ni -e 'print unless /Setenv\((bus\.)?GlobalMemoryDirEnv,/' "$GTREE/muxcode/$f"
done
grep -l 'GlobalMemoryDirEnv,' "$GTREE"/muxcode/{bus,cmd,daemon,tui}/main_test.go >/dev/null 2>&1 \
  && bad "global memory pins still present" || ok "removed: all four TestMains' BUS_GLOBAL_MEMORY_DIR pins"

CHOME="$WORK/ctl-home"; mkdir -p "$CHOME" "$WORK/ctl-tmp"
(cd "$GTREE/muxcode" && in_home "$CHOME" env TMPDIR="$WORK/ctl-tmp" \
  go test ./bus -run '^(TestDefaultMemoryDir_IsolatedFromSourceTree|TestGlobalMemoryDir_IsolatedFromHome)$' -count=1 -v \
  > "$WORK/gctl-test.log" 2>&1)
[ -d "$(global_mem "$CHOME")" ] \
  && ok "control: unpinned, Init created the scratch HOME's global memory — the detector sees it" \
  || { bad "control: no global memory created with the pins removed — section 3's silence proves nothing"; tail -5 "$WORK/gctl-test.log" | sed 's/^/        /'; }
grep -q -- '--- FAIL: TestGlobalMemoryDir_IsolatedFromHome' "$WORK/gctl-test.log" \
  && grep -q 'TestMain must pin BUS_GLOBAL_MEMORY_DIR' "$WORK/gctl-test.log" \
  && ok "control: TestGlobalMemoryDir_IsolatedFromHome fails naming the missing pin" \
  || { bad "control: the pin's control test did not fail"; tail -5 "$WORK/gctl-test.log" | sed 's/^/        /'; }

# --- Summary ---------------------------------------------------------------
echo
echo "=== Summary: $pass passed, $fail failed (floor $FLOOR) ==="
if [ "$fail" -ne 0 ]; then
  exit 1
fi
if [ "$pass" -lt "$FLOOR" ]; then
  echo "  FAIL  only $pass checks passed — below the coverage floor of $FLOOR"
  exit 1
fi
exit 0
