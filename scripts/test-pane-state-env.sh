#!/usr/bin/env bash
# Integration test for MUX-237: panes never receive an empty XDG_STATE_HOME.
#
# An empty value reads as unset to muxcode but as a relative path to Neovim,
# whose stdpath("state") then becomes `nvim` (or `muxcode` under
# NVIM_APPNAME=muxcode): every nvim opened in a pane wrote its shada, logs and
# undo files — one for ~/.aws/credentials among them — under the pane's
# working directory.
#
# Covers, with XDG_STATE_HOME unset in the launching shell:
#   A. A real `muxcode launch` (LaunchSession): the session environment, the
#      first window's pane (new-session -e), a later window's pane and a split
#      hold $HOME/.local/state, while MUXCODE_BUS_BASE keeps its blank; in a
#      pane nvim's stdpath("state") is absolute with and without
#      NVIM_APPNAME=muxcode, and an undofile edit made from a scratch working
#      directory creates nothing there and lands under the state directory.
#   B. Stale values: a different absolute XDG_STATE_HOME planted in the server
#      environment before the launch, then in the session environment before a
#      daemon restart — a later pane's `muxcode bus-dir` equals the launcher's.
#   C. A session seeded with the pre-fix empty value is corrected when its
#      daemon starts on this binary (ExportSessionBusEnv); a new window holds
#      the absolute value.
#   E. A `muxcode spawn start` worker: its agent pane (a stub research CLI
#      reporting its own environment) holds the absolute value.
#   D. Negative control: a daemon built from a copy of the tree with the blank
#      restored writes XDG_STATE_HOME=, a new window holds the empty value and
#      the same nvim edit creates ./nvim under the working directory.
#
# Pane values are read by the pane's own shell: `ps -E` shows no environment
# on macOS. "[]" is set-and-empty, "<unset>" unset.
#
# ISOLATION: muxcode and the claude-stub built from the tree; scratch HOME (so
# nvim's state and muxcode's bus live under it), lifecycle log and install
# dir; a private tmux server (MUX-233). Never touches the user's session.
#
# REQUIRES: go, tmux, nvim, jq, git. Without nvim it exits 2 (SKIP), never green.
# ~1 minute.
#
# Usage: bash scripts/test-pane-state-env.sh
#        MUXCODE_TEST_KEEP=1 bash scripts/test-pane-state-env.sh   # keep WORK
set -uo pipefail

for tool in go tmux nvim jq git; do
  command -v "$tool" >/dev/null 2>&1 || { echo "SKIP: $tool is required — MUX-237's pane checks were not run"; exit 2; }
done

REPO=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
WORK=$(mktemp -d /tmp/pane-state-env-XXXXXX)
L="pane-state-$$"
FLOOR=23

unset AGENT_ROLE BUS_ROLE TMUX TMUX_PANE MUXCODE_CONFIG MUXCODE_BUS_BASE MUXCODE_BUS_DIR XDG_STATE_HOME NVIM_APPNAME
export HOME="$WORK/home"
export MUXCODE_LIFECYCLE_LOG_DIR="$WORK/lifecycle" MUXCODE_INSTALL_DIR="$WORK/install"
export BUS_GLOBAL_MEMORY_DIR="$WORK/global-memory"
export MUXCODE_AGENT_CLI=claude MUXCODE_BUILD_CLI=claude MUXCODE_TEST_CLI=claude
export MUXCODE_AGENT_HEALTH_CHECK_SECS=86400 MUXCODE_AUTO_RESUME_DISABLE=1 MUXCODE_AGENT_HEARTBEAT=86400
export MUXCODE_EDIT_AUTO_RESTART_DISABLE=1 MUXCODE_CONTROL_PANE_DISABLE=1 MUXCODE_FORCE_RESPOND_DISABLE=1
export MUXCODE_TMP_CLEANUP_THRESHOLD=0 MUXCODE_ACTIVE_WATCHDOG_SECS=0 MUXCODE_PROMPT_AGENT_DISABLE=1
export MUXCODE_BRANCH_TIME_DISABLE=1 MUXCODE_STUCK_RELOAD_DISABLE=1 MUXCODE_AGENTDEFS_WATCH_DISABLE=1
export PATH="$WORK/bin:$PATH"
# The spawn worker's agent: reports its environment, then idles at a ❯.
export MUXCODE_RESEARCH_CLI="$WORK/stub-agent"
mkdir -p "$WORK/bin" "$WORK/negbin" "$HOME" "$WORK/lifecycle" "$WORK/install/agents" "$WORK/proj" "$WORK/target"
MUX="$WORK/bin/muxcode"
DEF="$HOME/.local/state"
DPID=""

. "$REPO/scripts/lib/private-tmux.sh"
private_tmux_init "$WORK/tmux"
cleanup() {
  [ -n "$DPID" ] && kill "$DPID" 2>/dev/null
  pkill -f "muxcode (watch|launch --resize|launch --auto-accept)( --monitor)? $L" 2>/dev/null
  private_tmux_kill
  cd / 2>/dev/null
  if [ "${MUXCODE_TEST_KEEP:-0}" = "1" ]; then echo "  (kept for diagnosis: $WORK)"; else rm -rf "$WORK"; fi
  true
}
trap cleanup EXIT

GREEN=$'\033[0;32m'; RED=$'\033[0;31m'; NC=$'\033[0m'
PASS=0; FAIL=0
ok()   { echo "  ${GREEN}PASS${NC}  $*"; PASS=$((PASS + 1)); }
bad()  { echo "  ${RED}FAIL${NC}  $*"; FAIL=$((FAIL + 1)); }
expect() { if [ "$2" = "$3" ]; then ok "$1"; else bad "$1 — got '$2', want '$3'"; fi; }

# wait_for SECS CHECK [ARGS...] — polls every 0.25 s; status 0 once CHECK holds.
wait_for() { local n=$(($1 * 4)) i; shift; for ((i = 0; i < n; i++)); do "$@" >/dev/null 2>&1 && return 0; sleep 0.25; done; return 1; }

# READ makes a pane's shell report both keys; the result reads XDG=[...] BASE=[...].
READ='printf "XDG=[%s] BASE=[%s]\n" "${XDG_STATE_HOME-<unset>}" "${MUXCODE_BUS_BASE-<unset>}"'
session_xdg() { tmux show-environment -t "$L" XDG_STATE_HOME 2>&1 | head -1; }
# shell_read TARGET — interrupt what the launcher typed into the pane, then have its shell report READ.
shell_read() {
  local out="$WORK/read-${1//[:.]/-}.txt"
  tmux send-keys -t "$1" C-c; sleep 0.3; tmux send-keys -t "$1" C-c; sleep 0.5
  tmux send-keys -t "$1" -l -- "$READ > $out"; sleep 0.1; tmux send-keys -t "$1" Enter
  wait_for 5 test -s "$out"; cat "$out" 2>/dev/null || echo "<no reading>"
}
# window_run NAME CWD CMD — a new window running CMD in CWD; waits for its done marker.
window_run() {
  local done="$WORK/done-$1"
  tmux new-window -d -t "$L" -n "$1" -c "$2" "$3; echo done > $done; sleep 600"
  wait_for 15 test -s "$done"
}
window_read() { window_run "$1" "$WORK" "$READ > $WORK/read-$1.txt" && cat "$WORK/read-$1.txt" || echo "<no reading>"; }
# nvim_edit CWD [APPNAME] — an undofile edit of a file outside CWD, made from CWD.
nvim_edit() { echo "cd $1 && NVIM_APPNAME=${2:-nvim} nvim --headless -u NONE -c 'set undofile' -c 'normal Gochanged' -c w -c qa $WORK/target/creds.txt"; }
stop_daemons() {
  pkill -f "muxcode watch --monitor $L\$" 2>/dev/null
  sleep 0.5
  pkill -f "muxcode watch $L( |\$)" 2>/dev/null
  [ -n "$DPID" ] && kill "$DPID" 2>/dev/null
  DPID=""
  wait_for 10 eval '! pgrep -f "muxcode watch( --monitor)? $L( |\$)"'
}
# start_daemon BIN — a daemon on BIN for the session, XDG_STATE_HOME unset in its environment.
start_daemon() { BUS_SESSION="$L" "$1" watch "$L" --poll 2 >>"$WORK/daemon.log" 2>&1 & DPID=$!; }

echo "=== MUX-237 pane XDG_STATE_HOME ==="
(cd "$REPO/tools/muxcode" && go build -buildvcs=false -o "$MUX" .) \
  && (cd "$REPO/scripts/fixtures/claude-stub" && go build -buildvcs=false -o "$WORK/bin/claude" .) \
  && ok "muxcode and claude-stub built from this tree" || { bad "build failed"; exit 1; }

# The negative-control copy: the pre-fix plan, blanking XDG_STATE_HOME like MUXCODE_BUS_BASE.
cp -R "$REPO/tools/muxcode" "$WORK/negsrc"
perl -0pi -e 's/set = append\(set, \[2\]string\{"XDG_STATE_HOME", base\}\)/_ = base\n\t\tblank = append(blank, "XDG_STATE_HOME")/' "$WORK/negsrc/bus/config.go"
if grep -q 'blank = append(blank, "XDG_STATE_HOME")' "$WORK/negsrc/bus/config.go" \
  && (cd "$WORK/negsrc" && go build -buildvcs=false -o "$WORK/negbin/muxcode" .); then
  ok "negative-control build with the blank restored"
else
  bad "negative-control patch or build failed"
fi

printf 'module proj\n\ngo 1.22\n' >"$WORK/proj/go.mod"
printf -- '---\ndescription: Builder\n---\nBuild.\n' >"$WORK/install/agents/code-builder.md"
printf -- '---\ndescription: Tester\n---\nTest.\n' >"$WORK/install/agents/test-runner.md"
printf -- '---\ndescription: Researcher\n---\nResearch.\n' >"$WORK/install/agents/code-researcher.md"
echo "secret" >"$WORK/target/creds.txt"
git -C "$WORK/proj" init -q && git -C "$WORK/proj" -c user.name=t -c user.email=t@example.invalid commit -q --allow-empty -m init
cat >"$WORK/stub-agent" <<STUB
#!/usr/bin/env bash
printf 'XDG=[%s] BASE=[%s]\n' "\${XDG_STATE_HOME-<unset>}" "\${MUXCODE_BUS_BASE-<unset>}" > "$WORK/read-spawn.txt"
while :; do printf '\n❯ '; sleep 1; done
STUB
chmod +x "$WORK/stub-agent"

echo "--- A: a real launch with XDG_STATE_HOME unset in the launcher"
tmux new-session -d -s "pre-$$" "sleep 600"
tmux set-environment -g XDG_STATE_HOME "$WORK/stale-global"
LAUNCHER_BUS=$("$MUX" bus-dir "$L")
(cd "$WORK/proj" && MUXCODE_WINDOWS="build test" BUS_SESSION="$L" EDITOR=true \
  "$MUX" launch "$WORK/proj" "$L" >"$WORK/launch.out" 2>&1 </dev/null)
# The launcher's final attach fails with no terminal, after the session is built.
wins=$(tmux list-windows -t "$L" -F '#W' 2>/dev/null | tr '\n' ' ')
expect "A: launcher created windows build, test" "$wins" "build test "
expect "A: session environment holds the resolved state base" "$(session_xdg)" "XDG_STATE_HOME=$DEF"
expect "A: first window's pane (new-session -e) holds it, MUXCODE_BUS_BASE blank" "$(shell_read "$L:build.0")" "XDG=[$DEF] BASE=[]"
expect "A: later window's pane holds it" "$(shell_read "$L:test.0")" "XDG=[$DEF] BASE=[]"
tmux split-window -d -t "$L:test" -c "$WORK" "$READ > $WORK/read-split.txt; sleep 600"
wait_for 5 test -s "$WORK/read-split.txt"
expect "A: a split holds it" "$(cat "$WORK/read-split.txt" 2>/dev/null)" "XDG=[$DEF] BASE=[]"

SCR="$WORK/cwd-a"; mkdir -p "$SCR"
SP='-c "lua io.open(os.getenv(\"OUT\"),\"w\"):write(vim.fn.stdpath(\"state\"))" -c qa'
window_run nv "$SCR" "OUT=$WORK/sp-plain.txt nvim --headless -u NONE $SP; OUT=$WORK/sp-mux.txt NVIM_APPNAME=muxcode nvim --headless -u NONE $SP; $(nvim_edit "$SCR" muxcode)"
expect "A: nvim stdpath(state) in a pane is absolute" "$(cat "$WORK/sp-plain.txt" 2>/dev/null)" "$DEF/nvim"
expect "A: with NVIM_APPNAME=muxcode too" "$(cat "$WORK/sp-mux.txt" 2>/dev/null)" "$DEF/muxcode"
expect "A: an undofile edit made from the pane's cwd creates nothing there" "$(ls -A "$SCR" | tr '\n' ' ')" ""
undo=$(find "$DEF/muxcode/undo" -type f -name '*creds.txt' 2>/dev/null | wc -l | tr -d ' ')
expect "A: the undo file is under the state directory" "$undo" "1"

echo "--- B: stale absolute values never override the launcher's resolution"
expect "B: the planted server-global value is still there" "$(tmux show-environment -g XDG_STATE_HOME)" "XDG_STATE_HOME=$WORK/stale-global"
window_run busdir-a "$WORK" "muxcode bus-dir $L > $WORK/busdir-a.txt"
expect "B: under a stale global, a later pane's bus-dir equals the launcher's" "$(cat "$WORK/busdir-a.txt" 2>/dev/null)" "$LAUNCHER_BUS"
stop_daemons
tmux set-environment -t "$L" XDG_STATE_HOME "$WORK/stale-session"
start_daemon "$MUX"
wait_for 15 eval '[ "$(session_xdg)" = "XDG_STATE_HOME=$DEF" ]'
expect "B: a daemon start replaces a stale session value" "$(session_xdg)" "XDG_STATE_HOME=$DEF"
window_run busdir-b "$WORK" "muxcode bus-dir $L > $WORK/busdir-b.txt"
expect "B: a pane created after it resolves the launcher's bus-dir" "$(cat "$WORK/busdir-b.txt" 2>/dev/null)" "$LAUNCHER_BUS"

echo "--- C: a session holding the pre-fix empty value is corrected at daemon start"
stop_daemons
tmux set-environment -t "$L" XDG_STATE_HOME ""
expect "C: seeded with the empty value" "$(session_xdg)" "XDG_STATE_HOME="
start_daemon "$MUX"
wait_for 15 eval '[ "$(session_xdg)" = "XDG_STATE_HOME=$DEF" ]'
expect "C: the daemon wrote the absolute value" "$(session_xdg)" "XDG_STATE_HOME=$DEF"
expect "C: a new window holds it" "$(window_read win-c)" "XDG=[$DEF] BASE=[]"

echo "--- E: a spawn worker's pane"
(cd "$WORK/proj" && BUS_SESSION="$L" AGENT_ROLE=edit "$MUX" spawn start research "pane-state spawn task" >"$WORK/spawn.out" 2>&1)
if grep -q '^Started spawn: ' "$WORK/spawn.out"; then ok "E: spawn start launched a worker"; else bad "E: spawn start: $(tail -2 "$WORK/spawn.out")"; fi
wait_for 30 test -s "$WORK/read-spawn.txt"
expect "E: the worker's agent pane holds the resolved state base" "$(cat "$WORK/read-spawn.txt" 2>/dev/null)" "XDG=[$DEF] BASE=[]"

echo "--- D: negative control — a daemon with the blank restored"
stop_daemons
start_daemon "$WORK/negbin/muxcode"
wait_for 15 eval '[ "$(session_xdg)" = "XDG_STATE_HOME=" ]'
expect "D: the pre-fix daemon blanks the session value" "$(session_xdg)" "XDG_STATE_HOME="
expect "D: a new window holds the empty value" "$(window_read win-d)" "XDG=[] BASE=[]"
SCRD="$WORK/cwd-d"; mkdir -p "$SCRD"
window_run nv-d "$SCRD" "$(nvim_edit "$SCRD")"
if [ -d "$SCRD/nvim" ]; then ok "D: the same edit creates ./nvim under the working directory"; else bad "D: no ./nvim under $SCRD: $(ls -A "$SCRD")"; fi
stop_daemons

TOTAL=$((PASS + FAIL))
echo
echo "=== test-pane-state-env: $PASS passed, $FAIL failed ($TOTAL checks, floor $FLOOR) ==="
[ "$FAIL" -eq 0 ] && [ "$TOTAL" -ge "$FLOOR" ]
