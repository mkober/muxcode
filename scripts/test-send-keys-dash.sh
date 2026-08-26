#!/usr/bin/env bash
# Integration test for MUX-104 — wake-injection payloads starting with a dash.
#
# Part A pins the underlying tmux behaviour the fix defends against, on an
# isolated tmux server (-L socket): a dash-leading payload is rejected bare
# AND with -l alone; only the -- separator delivers it.
#
# Part B drives the real muxcode injection path: a dash-leading bus message
# force-delivered into a scratch pane, verified by capture-pane, and marked
# notified so it is not retried forever.
#
# Hermetic: scratch BUS_SESSION + scratch tmux session; no live muxcode
# session and no daemon needed.
set -euo pipefail

PASS=0
FAIL=0

command -v tmux >/dev/null 2>&1 || { echo "SKIP: tmux is required"; exit 2; }
command -v muxcode >/dev/null 2>&1 || { echo "SKIP: muxcode not installed"; exit 2; }

SOCK="skdash-$$"
SESSION="skdash-test-$$"
export BUS_SESSION="$SESSION"
WORK=$(mktemp -d /tmp/skdash-XXXXXX)
export MUXCODE_LIFECYCLE_LOG_DIR="$WORK/lifecycle"
# Pin the run role to the Claude injection path (notify.go), the site the
# incident hit; provider resolution must not depend on ambient env.
export MUXCODE_AGENT_CLI=claude
export MUXCODE_RUN_CLI=claude
# Pin the sender identity — BOTH vars: BusRole() reads AGENT_ROLE before
# BUS_ROLE, and the run agent's pane exports AGENT_ROLE=run, so pinning
# BUS_ROLE alone still makes a send to run a dropped self-send and every
# delivery check downstream tests an empty inbox.
export AGENT_ROLE=edit
export BUS_ROLE=edit

cleanup() {
  tmux -L "$SOCK" kill-server 2>/dev/null || true
  tmux kill-session -t "$SESSION" 2>/dev/null || true
  rm -rf "/tmp/muxcode-bus-$SESSION" "$WORK"
}
trap cleanup EXIT

ok()   { PASS=$((PASS + 1)); echo "  ok: $1"; }
fail() { FAIL=$((FAIL + 1)); echo "  FAIL: $1"; }

echo "=== send-keys dash payload integration test (MUX-104) ==="

# ── Part A: pin the tmux behaviour on an isolated server ─────

echo "-- tmux behaviour pins"
tmux -L "$SOCK" new-session -d -s probe -x 80 -y 24
target=$(tmux -L "$SOCK" list-panes -t probe -F '#{pane_id}' | head -1)

if tmux -L "$SOCK" send-keys -t "$target" '- bullet' 2>/dev/null; then
  fail "bare dash payload must be rejected (fix defends against this)"
else
  ok "bare dash payload rejected by tmux"
fi

# -l alone does NOT protect against flag parsing — the finding that
# separates this fix from the natural-looking wrong one.
if tmux -L "$SOCK" send-keys -t "$target" -l '- bullet' 2>/dev/null; then
  fail "-l alone must NOT deliver a dash payload (it governs key names, not flags)"
else
  ok "-l alone still rejected — -- is the real fix"
fi

if tmux -L "$SOCK" send-keys -t "$target" -l -- '- mux104 literal probe' 2>/dev/null; then
  ok "-l -- form accepted"
else
  fail "-l -- form accepted"
fi
sleep 0.3
cap=$(tmux -L "$SOCK" capture-pane -t "$target" -p)
if printf '%s' "$cap" | grep -qF -- '- mux104 literal probe'; then
  ok "dash payload landed intact in the pane"
else
  fail "dash payload landed intact in the pane"
fi

if tmux -L "$SOCK" send-keys -t "$target" -l -- '--render-once probe' 2>/dev/null; then
  ok "double-dash payload accepted"
else
  fail "double-dash payload accepted"
fi
sleep 0.3
cap=$(tmux -L "$SOCK" capture-pane -t "$target" -p)
if printf '%s' "$cap" | grep -qF -- '--render-once probe'; then
  ok "double-dash payload landed intact"
else
  fail "double-dash payload landed intact"
fi

# ── Part B: the muxcode injection path end-to-end ────────────

echo "-- muxcode injection path"
# Scratch session on the default server — muxcode's pane targeting
# (PaneTarget = {session}:{window}.1) runs against the default socket.
tmux new-session -d -s "$SESSION" -n run -x 120 -y 30
tmux split-window -h -t "$SESSION:run"

muxcode init "$SESSION" >/dev/null 2>&1 || true

# A message whose formatted text leads with a dash — the exact traffic
# shape (bullet-formatted build reports) that fired the incident.
muxcode send run run "- dash bullet payload mux104 end-to-end" >/dev/null

# Precondition: the message must actually be in the inbox — a dropped
# send would let every later check false-pass on emptiness.
if muxcode inbox --role run --peek 2>/dev/null | grep -qF 'dash bullet payload mux104'; then
  ok "message landed in the run inbox"
else
  fail "message landed in the run inbox — send was dropped"
fi

# Exit 0 alone is not delivery ("no pending messages" also exits 0) —
# require the woke-with-pending report.
deliver_out=$(muxcode deliver run --force 2>&1 || true)
if printf '%s' "$deliver_out" | grep -q "woke run"; then
  ok "force-deliver reports a real delivery"
else
  fail "force-deliver reports a real delivery (got: $deliver_out)"
fi
sleep 0.5
# Capture both panes — AgentPane targets .1, but capturing both keeps the
# check independent of layout assumptions. -J joins wrapped lines: the
# split panes are ~60 columns, so the injected text wraps mid-word and a
# contiguous-string grep on an unjoined capture false-fails.
cap=$(tmux capture-pane -t "$SESSION:run.0" -pJ 2>/dev/null || true; tmux capture-pane -t "$SESSION:run.1" -pJ 2>/dev/null || true)

# Always-on diagnostics: when this check fails the log must answer where
# the injection went, not restate that it is missing.
echo "  [diag] deliver output: $deliver_out"
echo "  [diag] panes:"
tmux list-panes -t "$SESSION:run" -F '    #{pane_index} active=#{pane_active} cmd=#{pane_current_command} dead=#{pane_dead}' || true
echo "  [diag] pane captures (non-empty lines):"
printf '%s\n' "$cap" | grep -v '^[[:space:]]*$' | sed 's/^/    | /' || true

if printf '%s' "$cap" | grep -qF -- 'dash bullet payload mux104'; then
  ok "payload text landed in the agent pane"
else
  fail "payload text landed in the agent pane"
fi

# The notified markers must stop the NORMAL path from re-delivering — a
# second deliver WITHOUT --force reports nothing pending. (A forced
# re-deliver intentionally clears markers for still-actionable messages,
# so it is the wrong probe for this property.)
redeliver=$(muxcode deliver run 2>&1 || true)
if printf '%s' "$redeliver" | grep -q "no pending messages"; then
  ok "message marked notified — normal path will not re-deliver"
else
  fail "message marked notified — normal path will not re-deliver (got: $redeliver)"
fi

# ── Summary ──────────────────────────────────────────────────

echo ""
echo "=== $PASS passed, $FAIL failed ==="
[ "$PASS" -ge 9 ] || { echo "FAIL: coverage floor not met ($PASS < 9)"; exit 1; }
[ "$FAIL" -eq 0 ] || exit 1
echo "OK"
