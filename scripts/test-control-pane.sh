#!/usr/bin/env bash
# Integration test for the control pane (MUX-108).
#
# Hermetic: a scratch tmux session (default server) with launcher-shaped
# windows (panes 0/1), a scratch BUS_SESSION, and a scratch daemon whose
# supervision sweep creates the control panes. The per-window pane-order
# + delivery check is the one that protects the delivery layer: a
# creation-order slip types agent messages into an nvim buffer on every
# window at once.
#
# Requires the installed binary to include MUX-108 (run ./build.sh first).
set -euo pipefail

PASS=0
FAIL=0

command -v tmux >/dev/null 2>&1 || { echo "SKIP: tmux is required"; exit 2; }
command -v muxcode >/dev/null 2>&1 || { echo "SKIP: muxcode not installed"; exit 2; }
if ! grep -q "MUXCODE_CONTROL_PANE_EXCLUDE" "$(command -v muxcode)" 2>/dev/null; then
  echo "SKIP: installed muxcode lacks MUX-108 control pane — run ./build.sh"
  exit 2
fi

SESSION="control-pane-test-$$"
export BUS_SESSION="$SESSION"
export AGENT_ROLE=edit BUS_ROLE=edit
WORK=$(mktemp -d /tmp/control-pane-XXXXXX)
export MUXCODE_LIFECYCLE_LOG_DIR="$WORK/lifecycle"
export MUXCODE_AGENT_CLI=claude MUXCODE_RUN_CLI=claude
export MUXCODE_TMP_CLEANUP_THRESHOLD=0
# Compress supervisor time: the first sweep runs one interval after
# daemon start (the launcher owns launch-time creation), so the default
# 60s would stall every daemon-restart check below.
export MUXCODE_CONTROL_PANE_CHECK_SECS=2
unset MUXCODE_CONTROL_PANE_DISABLE MUXCODE_CONTROL_PANE_EXCLUDE 2>/dev/null || true
BUSDIR="/tmp/muxcode-bus-$SESSION"
MUX=$(command -v muxcode)

DPID=""
cleanup() {
  [ -n "$DPID" ] && kill "$DPID" 2>/dev/null || true
  pkill -f "watch $SESSION" 2>/dev/null || true
  tmux kill-session -t "$SESSION" 2>/dev/null || true
  rm -rf "$BUSDIR" "$WORK"
}
trap cleanup EXIT

ok()   { PASS=$((PASS + 1)); echo "  ok: $1"; }
fail() { FAIL=$((FAIL + 1)); echo "  FAIL: $1"; }

panes_of() { tmux list-panes -t "$SESSION:$1" -F '#{pane_index}:#{pane_current_command}' 2>/dev/null || true; }

echo "=== control pane integration test (MUX-108) ==="

# Launcher-shaped windows: pane 0 + pane 1 (agent stand-in with the idle
# glyph PS1 so delivery injection works — the MUX-103 pattern).
tmux new-session -d -s "$SESSION" -n edit -x 200 -y 50
for w in build run; do tmux new-window -t "$SESSION" -n "$w"; done
for w in edit build run; do
  tmux split-window -h -t "$SESSION:$w"
  tmux send-keys -t "$SESSION:$w.1" -l -- 'PS1="❯ "; clear'
  tmux send-keys -t "$SESSION:$w.1" Enter
done
sleep 1
"$MUX" init "$SESSION" >/dev/null 2>&1 || true

# ── 1. Supervisor creates the pane on every window ───────────

echo "-- pane creation"
pkill -f "watch $SESSION" 2>/dev/null || true
"$MUX" watch "$SESSION" --poll 2 >"$WORK/daemon.log" 2>&1 &
DPID=$!
sleep 4 # first sweep runs one (compressed) interval after start

for w in edit build run; do
  layout=$(panes_of "$w")
  if printf '%s' "$layout" | grep -q "^2:muxcode"; then
    ok "window $w has the control pane at index 2"
  else
    fail "window $w has the control pane at index 2 (got: $(printf '%s' "$layout" | tr '\n' ' '))"
  fi
done

# ── 2. Delivery still reaches pane 1 on every window ─────────

echo "-- delivery contract"
for w in build run; do
  "$MUX" send "$w" probe "delivery probe $w mux108" --no-notify --track >/dev/null 2>&1 || true
  # One retry: send-keys injection into a freshly-created pane can park
  # its Enter (the known TUI redraw race — reproduced with NO control
  # pane present, so it is injection flakiness, not a MUX-108 defect).
  landed=0
  for attempt in 1 2; do
    "$MUX" deliver "$w" --force >/dev/null 2>&1 || true
    sleep 2
    cap=$(tmux capture-pane -t "$SESSION:$w.1" -pJ 2>/dev/null || true)
    if printf '%s' "$cap" | grep -qF "delivery probe $w mux108"; then landed=1; break; fi
  done
  if [ "$landed" = "1" ]; then
    ok "delivery reaches $w pane 1 with the control pane present"
  else
    fail "delivery reaches $w pane 1 (capture: $(printf '%s' "$cap" | grep -v '^\s*$' | tail -2 | tr '\n' ' '))"
  fi
done

# ── 3. A killed pane respawns ────────────────────────────────

echo "-- respawn"
tmux kill-pane -t "$SESSION:build.2"
# Restart the daemon: its first sweep (one compressed interval in)
# respawns the pane — the same path that recycles panes onto a fresh
# binary after an install.
kill "$DPID" 2>/dev/null || true
wait "$DPID" 2>/dev/null || true
"$MUX" watch "$SESSION" --poll 2 >>"$WORK/daemon.log" 2>&1 &
DPID=$!
sleep 4
if panes_of build | grep -q "^2:muxcode"; then
  ok "killed pane respawned by the supervision sweep"
else
  fail "killed pane respawned (got: $(panes_of build | tr '\n' ' '))"
fi

# ── 3b. A duplicate control pane converges to one ────────────
# The 2026-08-26 incident: two creators racing at launch left a second,
# untitled graph pane on the window. The sweep must kill the extra and
# keep exactly one.

echo "-- duplicate dedupe"
tmux split-window -vf -d -l 5 -t "$SESSION:run" "muxcode graph ui"
sleep 5 # two sweep intervals — the running daemon dedupes in place
dup_count=$(panes_of run | grep -c ":muxcode$" || true)
if [ "$dup_count" = "1" ]; then
  ok "duplicate control pane killed, one survivor"
else
  fail "duplicate control pane killed (got $dup_count muxcode panes: $(panes_of run | tr '\n' ' '))"
fi

# ── 4. A new gate switches the pane to Pending Gates ─────────

echo "-- gate switch"
cat > "$WORK/gated.json" <<'EOF'
{
  "name": "gated-pane-test", "start": "pane-gate",
  "nodes": [
    {"id": "pane-gate", "type": "wait_human", "message": "pane switch test"},
    {"id": "after", "type": "send", "role": "review", "action": "review", "message": "m"}
  ],
  "edges": [{"from": "pane-gate", "to": "after"}]
}
EOF
"$MUX" graph run --file "$WORK/gated.json" "control pane gate test" >/dev/null
deadline=$((SECONDS + 20))
switched=0
while [ $SECONDS -lt $deadline ]; do
  cap=$(tmux capture-pane -t "$SESSION:edit.2" -pJ 2>/dev/null || true)
  if printf '%s' "$cap" | grep -qF "pane-gate"; then switched=1; break; fi
  sleep 2
done
if [ "$switched" = "1" ]; then
  ok "pane switched to Pending Gates on a new gate"
else
  fail "pane switched to Pending Gates (capture: $(tmux capture-pane -t "$SESSION:edit.2" -pJ 2>/dev/null | grep -v '^\s*$' | head -3 | tr '\n' ' '))"
fi

# ── 5. Titles and the display-time substitution ──────────────

echo "-- titles"
t2=$(tmux display-message -p -t "$SESSION:edit.2" '#{pane_title}')
if [ "$t2" = " GRAPH " ]; then ok "control pane titled ' GRAPH '"; else fail "control pane title (got: [$t2])"; fi
t1=$(tmux display-message -p -t "$SESSION:edit.1" '#{pane_title}')
if [ "$t1" != " GRAPH " ] && [ "$t1" != " NVIM " ]; then
  ok "pane 1 raw title untouched by the supervisor"
else
  fail "pane 1 raw title untouched (got: [$t1])"
fi
# The uppercase transform is display-time only: a changed raw title shows
# through the substitution, never pinned.
tmux select-pane -t "$SESSION:edit.1" -T "◐ code-editor busy"
shown=$(tmux display-message -p -t "$SESSION:edit.1" '#{s/code-editor/CODE-EDITOR/:pane_title}')
if [ "$shown" = "◐ CODE-EDITOR busy" ]; then
  ok "display substitution transforms the live title without pinning it"
else
  fail "display substitution (got: [$shown])"
fi
if grep -q 's/code-editor/CODE-EDITOR/' "$PWD/config/tmux.conf" 2>/dev/null || grep -q 's/code-editor/CODE-EDITOR/' config/tmux.conf 2>/dev/null; then
  ok "tmux.conf carries the border format globally"
else
  fail "tmux.conf carries the border format globally"
fi

# ── 6. Negative controls: exclusion and wholesale disable ────

echo "-- negatives"
kill "$DPID" 2>/dev/null || true; wait "$DPID" 2>/dev/null || true
tmux kill-pane -t "$SESSION:run.2" 2>/dev/null || true
MUXCODE_CONTROL_PANE_EXCLUDE=run "$MUX" watch "$SESSION" --poll 2 >>"$WORK/daemon.log" 2>&1 &
DPID=$!
sleep 4
if panes_of run | grep -q "^2:"; then
  fail "excluded window must keep two panes (got: $(panes_of run | tr '\n' ' '))"
else
  ok "excluded window keeps two panes"
fi
if panes_of build | grep -q "^2:muxcode"; then
  ok "sibling window keeps its control pane"
else
  fail "sibling window keeps its control pane"
fi

kill "$DPID" 2>/dev/null || true; wait "$DPID" 2>/dev/null || true
tmux kill-pane -t "$SESSION:build.2" 2>/dev/null || true
tmux kill-pane -t "$SESSION:edit.2" 2>/dev/null || true
MUXCODE_CONTROL_PANE_DISABLE=1 "$MUX" watch "$SESSION" --poll 2 >>"$WORK/daemon.log" 2>&1 &
DPID=$!
sleep 4
none=1
for w in edit build run; do
  if panes_of "$w" | grep -q "^2:"; then none=0; fi
done
if [ "$none" = "1" ]; then
  ok "wholesale disable creates no third pane anywhere"
else
  fail "wholesale disable creates no third pane anywhere"
fi

# ── Summary ──────────────────────────────────────────────────

echo ""
echo "=== $PASS passed, $FAIL failed ==="
[ "$PASS" -ge 13 ] || { echo "FAIL: coverage floor not met ($PASS < 13)"; exit 1; }
[ "$FAIL" -eq 0 ] || exit 1
echo "OK"
