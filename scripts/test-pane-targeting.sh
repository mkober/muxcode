#!/usr/bin/env bash
# Integration test for pane resolution by identity (MUX-117).
#
# Proves the delivery layer resolves panes by the @muxcode_pane tag, never
# by position: delivery on a normal three-pane window, the insert-a-pane-
# before-the-agent negative control (an index-based fix fails it), legacy
# fallback for an untagged old-binary-style session with a once-per-window
# lifecycle event, loud failure on a marked-but-untagged window (nothing
# ever lands at an index), clear/compact reaching the tagged agent past an
# interloper, and control-pane respawn/dedupe re-identification.
#
# ISOLATION: a private tmux server (TMUX_TMPDIR under a temp dir, TMUX
# unset) so no live session is touched, scratch BUS_SESSIONs under /tmp,
# MUXCODE_LIFECYCLE_LOG_DIR pinned to a temp dir, MUXCODE_CONFIG pointed
# at an empty file, and dedup/branch-time/cleanup machinery disabled.
#
# REQUIRES: tmux, and an installed muxcode binary that includes MUX-117
# (run ./build.sh first).
#
# Usage: bash scripts/test-pane-targeting.sh
set -uo pipefail

PASS=0
FAIL=0
ok()   { PASS=$((PASS + 1)); echo "  ok: $1"; }
fail() { FAIL=$((FAIL + 1)); echo "  FAIL: $1"; }

command -v tmux >/dev/null 2>&1 || { echo "SKIP: tmux is required"; exit 2; }
command -v muxcode >/dev/null 2>&1 || { echo "SKIP: muxcode not installed"; exit 2; }
MUX=$(command -v muxcode)
if ! grep -q "@muxcode_pane" "$MUX" 2>/dev/null || ! "$MUX" pane -h >/dev/null 2>&1; then
  echo "SKIP: installed muxcode lacks MUX-117 pane identity — run ./build.sh"
  exit 2
fi

SESSION="pane-id-test-$$"
LEGACY="pane-legacy-test-$$"
WORK=$(mktemp -d /tmp/pane-target-XXXXXX)
export BUS_SESSION="$SESSION"
export AGENT_ROLE=edit BUS_ROLE=edit
# Private tmux server: every tmux call below (script, muxcode CLI, and the
# scratch daemon alike — all inherit this env) hits an isolated socket.
export TMUX_TMPDIR="$WORK/tmux"
mkdir -p "$TMUX_TMPDIR"
unset TMUX
export MUXCODE_LIFECYCLE_LOG_DIR="$WORK/lifecycle"
: > "$WORK/empty-config"
export MUXCODE_CONFIG="$WORK/empty-config"
export MUXCODE_AGENT_CLI=claude MUXCODE_RUN_CLI=claude
export MUXCODE_TMP_CLEANUP_THRESHOLD=0
export MUXCODE_BRANCH_TIME_DISABLE=1
export MUXCODE_DEDUP_WINDOW=0
export MUXCODE_CONTROL_PANE_CHECK_SECS=2
unset MUXCODE_CONTROL_PANE_DISABLE MUXCODE_CONTROL_PANE_EXCLUDE 2>/dev/null || true
unset MUXCODE_AUTO_CLEAR_ROLES 2>/dev/null || true
BUSDIR="/tmp/muxcode-bus-$SESSION"
BUSDIR2="/tmp/muxcode-bus-$LEGACY"
LIFELOG="$MUXCODE_LIFECYCLE_LOG_DIR/$SESSION.log"
LIFELOG2="$MUXCODE_LIFECYCLE_LOG_DIR/$LEGACY.log"

DPID=""
cleanup() {
  [ -n "$DPID" ] && kill "$DPID" 2>/dev/null || true
  pkill -f "watch $SESSION" 2>/dev/null || true
  tmux kill-server 2>/dev/null || true
  rm -rf "$BUSDIR" "$BUSDIR2" "$WORK"
}
trap cleanup EXIT

panes_of() { tmux list-panes -t "$SESSION:$1" -F '#{pane_index}:#{pane_current_command}' 2>/dev/null || true; }
tag_of()   { tmux display-message -p -t "$1" '#{@muxcode_pane}' 2>/dev/null || true; }

# deliver_lands <role> <action> <probe> <capture-target> [session] — send
# + force-deliver with one retry (freshly-created panes can park the Enter
# — the known TUI redraw race, not a resolver defect). Callers vary the
# action per send: a live in-flight task blocks a repeat (to,action) send.
# Sets LANDED and LAST_CAP.
deliver_lands() {
  local role=$1 action=$2 probe=$3 target=$4 session=${5:-$SESSION}
  BUS_SESSION="$session" "$MUX" send "$role" "$action" "$probe" --no-notify --track >/dev/null 2>&1 || true
  LANDED=0
  LAST_CAP=""
  for _ in 1 2; do
    "$MUX" deliver "$role" --force --session "$session" >/dev/null 2>&1 || true
    sleep 2
    LAST_CAP=$(tmux capture-pane -t "$target" -pJ 2>/dev/null || true)
    if printf '%s' "$LAST_CAP" | grep -qF "$probe"; then LANDED=1; break; fi
  done
}

echo "=== pane targeting integration test (MUX-117) ==="

# Launcher-shaped windows (pane 0 left, pane 1 agent stand-in with the idle
# glyph PS1 so idle detection and injection behave — the MUX-103 pattern),
# tagged the way TagWindowPanes stamps them at creation. review gets the
# window marker but NO pane tags: the marked-but-untagged broken contract.
tmux new-session -d -s "$SESSION" -n edit -x 220 -y 50
for w in build run review; do tmux new-window -t "$SESSION" -n "$w"; done
for w in edit build run review; do
  tmux split-window -h -t "$SESSION:$w"
  tmux send-keys -t "$SESSION:$w.1" -l -- 'PS1="❯ "; clear'
  tmux send-keys -t "$SESSION:$w.1" Enter
done
for w in edit build run; do
  tmux set-option -p -t "$SESSION:$w.0" @muxcode_pane left
  tmux set-option -p -t "$SESSION:$w.1" @muxcode_pane agent
  tmux set-option -w -t "$SESSION:$w" @muxcode_tagged 1
done
tmux set-option -w -t "$SESSION:review" @muxcode_tagged 1
sleep 1
"$MUX" init "$SESSION" >/dev/null 2>&1 || true

BUILD_AGENT=$(tmux list-panes -t "$SESSION:build" -F '#{pane_id} #{@muxcode_pane}' | awk '$2=="agent"{print $1}')
RUN_AGENT=$(tmux list-panes -t "$SESSION:run" -F '#{pane_id} #{@muxcode_pane}' | awk '$2=="agent"{print $1}')

# ── 1. Identity resolution on a tagged window ────────────────

echo "-- identity resolution"
resolved=$("$MUX" pane build agent 2>/dev/null)
if [ -n "$BUILD_AGENT" ] && [ "$resolved" = "$BUILD_AGENT" ]; then
  ok "resolver returns the agent pane id ($resolved), not an index"
else
  fail "resolver returns the agent pane id (want $BUILD_AGENT, got: $resolved)"
fi

# ── 2. Delivery on a normal three-pane window ────────────────
# left / agent / control — the launch shape. The control pane runs the
# real graph UI so the later sweep recognizes it by start command.

echo "-- three-pane delivery"
tmux split-window -vf -d -l 5 -t "$SESSION:build" "muxcode graph ui"
sleep 1
BUILD_CTL=$(panes_of build | awk -F: '$2=="muxcode"{print $1}' | head -1)
[ -n "$BUILD_CTL" ] && tmux set-option -p -t "$SESSION:build.$BUILD_CTL" @muxcode_pane control
deliver_lands build probe "pane probe normal $$" "$BUILD_AGENT"
if [ "$LANDED" = "1" ]; then
  ok "delivery reaches the agent on a three-pane window"
else
  fail "delivery reaches the agent on a three-pane window (capture: $(printf '%s' "$LAST_CAP" | tail -2 | tr '\n' ' '))"
fi

# ── 3. Insert a pane BEFORE the agent (negative control) ─────
# split-window -hb renumbers the agent (1 → 2): an index-based resolver
# now types into the interloper. Identity must keep resolving and keep
# delivering to the same pane id.

echo "-- insert-before negative control"
BUILD_INTRUDER=$(tmux split-window -hb -d -P -F '#{pane_id}' -t "$SESSION:build.1")
sleep 1
resolved=$("$MUX" pane build agent 2>/dev/null)
if [ "$resolved" = "$BUILD_AGENT" ]; then
  ok "resolver survives pane insertion (still $BUILD_AGENT)"
else
  fail "resolver survives pane insertion (want $BUILD_AGENT, got: $resolved)"
fi
deliver_lands build probe2 "pane probe displaced $$" "$BUILD_AGENT"
if [ "$LANDED" = "1" ]; then
  ok "delivery reaches the displaced agent by identity"
else
  fail "delivery reaches the displaced agent (capture: $(printf '%s' "$LAST_CAP" | tail -2 | tr '\n' ' '))"
fi
intruder_cap=$(tmux capture-pane -t "$BUILD_INTRUDER" -pJ 2>/dev/null || true)
if printf '%s' "$intruder_cap" | grep -qF "pane probe displaced $$"; then
  fail "interloper at the agent's old index must not receive the message"
else
  ok "interloper at the agent's old index received nothing"
fi

# ── 4. Untagged legacy session still resolves, logs fallback ─
# An old-binary session: no tags, no window marker. Resolution falls back
# to the creation-order index and logs ONE pane-fallback event per window.

echo "-- legacy fallback"
tmux new-session -d -s "$LEGACY" -n build -x 220 -y 50
tmux split-window -h -t "$LEGACY:build"
tmux send-keys -t "$LEGACY:build.1" -l -- 'PS1="❯ "; clear'
tmux send-keys -t "$LEGACY:build.1" Enter
sleep 1
# init reads BUS_SESSION, not argv — env override targets the legacy bus
BUS_SESSION="$LEGACY" "$MUX" init >/dev/null 2>&1 || true
resolved=$("$MUX" pane build agent --session "$LEGACY" 2>/dev/null)
if [ "$resolved" = "$LEGACY:build.1" ]; then
  ok "untagged window resolves to the legacy index target"
else
  fail "untagged window resolves to the legacy index target (got: $resolved)"
fi
fallbacks=$(grep -c '"event":"pane-fallback"' "$LIFELOG2" 2>/dev/null)
fallbacks=${fallbacks:-0}
if [ "$fallbacks" = "1" ]; then
  ok "legacy fallback logged one pane-fallback lifecycle event"
else
  fail "legacy fallback logged one pane-fallback lifecycle event (got $fallbacks)"
fi
"$MUX" pane build agent --session "$LEGACY" >/dev/null 2>&1
fallbacks=$(grep -c '"event":"pane-fallback"' "$LIFELOG2" 2>/dev/null)
fallbacks=${fallbacks:-0}
if [ "$fallbacks" = "1" ]; then
  ok "pane-fallback throttled to once per window"
else
  fail "pane-fallback throttled to once per window (got $fallbacks)"
fi
deliver_lands build probe "pane probe legacy $$" "$LEGACY:build.1" "$LEGACY"
if [ "$LANDED" = "1" ]; then
  ok "delivery still works in a legacy session"
else
  fail "delivery still works in a legacy session (capture: $(printf '%s' "$LAST_CAP" | tail -2 | tr '\n' ' '))"
fi

# ── 5. Unresolvable pane fails loudly, never an index ────────
# review is marked @muxcode_tagged with no pane tags: a broken contract,
# not a legacy window. Resolution must error and delivery must land
# nowhere — the index it would have hit may host an editor or a git TUI.

echo "-- loud failure"
if "$MUX" pane review agent >/dev/null 2>&1; then
  fail "resolver must exit non-zero on a marked-but-untagged window"
else
  ok "resolver exits non-zero on a marked-but-untagged window"
fi
if grep -q '"event":"pane-resolve-failed"' "$LIFELOG" 2>/dev/null; then
  ok "resolution failure logged a pane-resolve-failed event"
else
  fail "resolution failure logged a pane-resolve-failed event"
fi
BUS_SESSION="$SESSION" "$MUX" send review probe "pane probe broken $$" --no-notify --track >/dev/null 2>&1 || true
if "$MUX" deliver review --force --session "$SESSION" >/dev/null 2>&1; then
  fail "deliver must fail loudly on an unresolvable pane"
else
  ok "deliver fails loudly on an unresolvable pane"
fi
sleep 1
review_cap=$(tmux capture-pane -t "$SESSION:review.1" -pJ 2>/dev/null || true)
if printf '%s' "$review_cap" | grep -q '❯' && ! printf '%s' "$review_cap" | grep -qF "pane probe broken $$"; then
  ok "nothing was delivered to the index the fallback would have hit"
else
  fail "nothing was delivered to the index (capture: $(printf '%s' "$review_cap" | tail -2 | tr '\n' ' '))"
fi

# ── 6. clear and compact reach the right agent ───────────────
# Both mutate a live agent's conversation, so they resolve through the
# same identity path. With an interloper displacing the run agent, the
# injections must land in the tagged pane, not at the old index.

echo "-- clear and compact"
RUN_INTRUDER=$(tmux split-window -hb -d -P -F '#{pane_id}' -t "$SESSION:run.1")
sleep 1
clr_out=$("$MUX" clear run 2>&1)
sleep 1
run_cap=$(tmux capture-pane -t "$RUN_AGENT" -pJ 2>/dev/null || true)
if printf '%s' "$run_cap" | grep -qF "/clear"; then
  ok "clear reaches the displaced agent by identity"
else
  fail "clear reaches the displaced agent (cmd: $clr_out | capture: $(printf '%s' "$run_cap" | tail -2 | tr '\n' ' '))"
fi
intruder_cap=$(tmux capture-pane -t "$RUN_INTRUDER" -pJ 2>/dev/null || true)
if printf '%s' "$intruder_cap" | grep -qF "/clear"; then
  fail "interloper must not receive /clear"
else
  ok "interloper received no /clear"
fi
cmp_out=$("$MUX" compact run 2>&1)
sleep 1
run_cap=$(tmux capture-pane -t "$RUN_AGENT" -pJ 2>/dev/null || true)
if printf '%s' "$run_cap" | grep -qF "/compact"; then
  ok "compact reaches the displaced agent by identity"
else
  fail "compact reaches the displaced agent (cmd: $cmp_out | capture: $(printf '%s' "$run_cap" | tail -2 | tr '\n' ' '))"
fi
intruder_cap=$(tmux capture-pane -t "$RUN_INTRUDER" -pJ 2>/dev/null || true)
if printf '%s' "$intruder_cap" | grep -qF "/compact"; then
  fail "interloper must not receive /compact"
else
  ok "interloper received no /compact"
fi

# ── 7. Control pane: create, respawn, re-identify, dedupe ────
# The daemon's supervision sweep owns control panes. It must create one
# on a clean window, keep exactly one on build (whose pre-made pane was
# displaced to index 3 by the interloper — recognition by identity, not
# position), respawn a killed pane tagged for resolution, and converge a
# duplicate to one survivor.

echo "-- control pane sweep"
pkill -f "watch $SESSION" 2>/dev/null || true
"$MUX" watch "$SESSION" --poll 2 >"$WORK/daemon.log" 2>&1 &
DPID=$!
disown
sleep 5
if [ "$(panes_of edit | grep -c ':muxcode$')" = "1" ]; then
  ok "sweep created the control pane on a clean window"
else
  fail "sweep created the control pane on a clean window (got: $(panes_of edit | tr '\n' ' '))"
fi
if [ "$(panes_of build | grep -c ':muxcode$')" = "1" ]; then
  ok "displaced pre-made control pane recognized, not duplicated"
else
  fail "displaced pre-made control pane recognized (got: $(panes_of build | tr '\n' ' '))"
fi

EDIT_CTL=$(tmux list-panes -t "$SESSION:edit" -F '#{pane_id}:#{pane_current_command}' | awk -F: '$2=="muxcode"{print $1}' | head -1)
tmux kill-pane -t "$EDIT_CTL" 2>/dev/null || true
kill "$DPID" 2>/dev/null || true
wait "$DPID" 2>/dev/null || true
"$MUX" watch "$SESSION" --poll 2 >>"$WORK/daemon.log" 2>&1 &
DPID=$!
disown
sleep 5
if [ "$(panes_of edit | grep -c ':muxcode$')" = "1" ]; then
  ok "killed control pane respawned by the sweep"
else
  fail "killed control pane respawned (got: $(panes_of edit | tr '\n' ' '))"
fi
NEW_CTL=$(tmux list-panes -t "$SESSION:edit" -F '#{pane_id}:#{pane_current_command}' | awk -F: '$2=="muxcode"{print $1}' | head -1)
resolved=$("$MUX" pane edit control 2>/dev/null)
if [ -n "$NEW_CTL" ] && [ "$(tag_of "$NEW_CTL")" = "control" ] && [ "$resolved" = "$NEW_CTL" ]; then
  ok "respawned pane re-identified: tagged control and resolvable ($NEW_CTL)"
else
  fail "respawned pane re-identified (id=$NEW_CTL tag=$(tag_of "$NEW_CTL") resolved=$resolved)"
fi
tmux split-window -vf -d -l 5 -t "$SESSION:edit" "muxcode graph ui"
sleep 6
if [ "$(panes_of edit | grep -c ':muxcode$')" = "1" ]; then
  ok "duplicate control pane converged to one survivor"
else
  fail "duplicate control pane converged (got: $(panes_of edit | tr '\n' ' '))"
fi

# ── Summary ──────────────────────────────────────────────────
# Coverage floor: every check above is unconditional, so a green run must
# have executed all 22 — a partial run cannot report green.

echo ""
echo "=== $PASS passed, $FAIL failed ==="
[ "$PASS" -ge 22 ] || { echo "FAIL: coverage floor not met ($PASS < 22)"; exit 1; }
[ "$FAIL" -eq 0 ] || exit 1
echo "OK"
