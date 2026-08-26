#!/usr/bin/env bash
# Integration test for the graph TUI (MUX-031).
#
# Hermetic: a fixture run store is built under a scratch BUS_SESSION via
# `muxcode graph run --file` plus hand-written node status JSON — no live
# muxcode session and no daemon needed. Frames come from the scriptable
# seam `muxcode graph ui --render-once`.
#
# Requires the installed binary to include MUX-031 (run ./build.sh first).
set -euo pipefail

PASS=0
FAIL=0
ESC=$(printf '\033')

# Skips exit 2, not 0: a run where zero checks executed must never read
# as PASS (exit 1 = checks failed, exit 2 = could not run).
command -v jq >/dev/null 2>&1 || { echo "SKIP: jq is required"; exit 2; }
command -v muxcode >/dev/null 2>&1 || { echo "SKIP: muxcode not installed"; exit 2; }

# Binary must carry the graph ui subcommand. Capture first: `muxcode
# graph` itself exits 1 (usage), and under pipefail that poisons the
# pipeline status even when grep matches — piping directly false-SKIPs
# every run (found live by the run agent on 2026-08-26).
graph_usage=$(muxcode graph 2>&1 || true)
if ! printf '%s' "$graph_usage" | grep -q ' ui '; then
  echo "SKIP: installed muxcode lacks MUX-031 graph ui — run ./build.sh"
  exit 2
fi

SESSION="graph-tui-test-$$"
export BUS_SESSION="$SESSION"
WORK=$(mktemp -d /tmp/graph-tui-fixtures-XXXXXX)
export MUXCODE_LIFECYCLE_LOG_DIR="$WORK/lifecycle"
BUSDIR="/tmp/muxcode-bus-$SESSION"

cleanup() { rm -rf "$BUSDIR" "$WORK"; }
trap cleanup EXIT

# BSD sed does not expand \x1b inside single quotes — build the escape
# byte with printf and interpolate it (lesson pinned by MUX-003).
plain() { sed "s/${ESC}\[[0-9;]*[A-Za-z]//g"; }

ok()   { PASS=$((PASS + 1)); echo "  ok: $1"; }
fail() { FAIL=$((FAIL + 1)); echo "  FAIL: $1"; }

assert_contains() { # name haystack needle
  if printf '%s' "$2" | grep -qF -- "$3"; then ok "$1"; else fail "$1 (missing: $3)"; fi
}
assert_not_contains() { # name haystack needle
  if printf '%s' "$2" | grep -qF -- "$3"; then fail "$1 (unexpected: $3)"; else ok "$1"; fi
}

# start_run <fixture.json> — prints the new run id
start_run() {
  local out
  out=$(muxcode graph run --file "$1" fixture)
  printf '%s' "$out" | sed -n 's/^Started run \([^ ]*\).*/\1/p'
}

# set_node <run> <node> <state> [outcome]
set_node() {
  local outcome_field=""
  [ -n "${4:-}" ] && outcome_field=",\"outcome\":\"$4\""
  cat > "$BUSDIR/graphs/$1/nodes/$2.json" <<EOF
{"node_id":"$2","state":"$3"${outcome_field},"updated_at":1000}
EOF
}

echo "=== graph TUI integration test (MUX-031) ==="

# ── Fixtures ─────────────────────────────────────────────────

cat > "$WORK/fanout.json" <<'EOF'
{
  "name": "fanout-fixture", "start": "start",
  "nodes": [
    {"id": "start", "type": "send", "role": "build", "action": "build", "message": "m"},
    {"id": "worker-a", "type": "send", "role": "test", "action": "test", "message": "m"},
    {"id": "worker-b", "type": "send", "role": "review", "action": "review", "message": "m"},
    {"id": "barrier", "type": "join", "join": "all"}
  ],
  "edges": [
    {"from": "start", "to": "worker-a"},
    {"from": "start", "to": "worker-b"},
    {"from": "worker-a", "to": "barrier"},
    {"from": "worker-b", "to": "barrier"}
  ]
}
EOF

cat > "$WORK/gated.json" <<'EOF'
{
  "name": "gated-fixture", "start": "review",
  "nodes": [
    {"id": "review", "type": "send", "role": "review", "action": "review", "message": "m"},
    {"id": "ship-gate", "type": "wait_human", "message": "ship it?"},
    {"id": "ship", "type": "send", "role": "commit", "action": "commit", "message": "m"}
  ],
  "edges": [
    {"from": "review", "to": "ship-gate"},
    {"from": "ship-gate", "to": "ship"}
  ]
}
EOF

cat > "$WORK/benign.json" <<'EOF'
{
  "name": "benign-fixture", "start": "start",
  "nodes": [
    {"id": "start", "type": "send", "role": "test", "action": "test", "message": "m"},
    {"id": "verify-gate", "type": "wait_human", "message": "verify?"},
    {"id": "after", "type": "send", "role": "review", "action": "review", "message": "m"}
  ],
  "edges": [
    {"from": "start", "to": "verify-gate"},
    {"from": "verify-gate", "to": "after"}
  ]
}
EOF

cat > "$WORK/wide.json" <<'EOF'
{
  "name": "wide-fixture", "start": "stage-alpha-preflight",
  "nodes": [
    {"id": "stage-alpha-preflight", "type": "send", "role": "build", "action": "build", "message": "m"},
    {"id": "stage-bravo-compile", "type": "send", "role": "build", "action": "build", "message": "m"},
    {"id": "stage-charlie-verify", "type": "send", "role": "test", "action": "test", "message": "m"},
    {"id": "stage-delta-inspect", "type": "send", "role": "review", "action": "review", "message": "m"},
    {"id": "stage-echo-publish", "type": "send", "role": "review", "action": "review", "message": "m"}
  ],
  "edges": [
    {"from": "stage-alpha-preflight", "to": "stage-bravo-compile"},
    {"from": "stage-bravo-compile", "to": "stage-charlie-verify"},
    {"from": "stage-charlie-verify", "to": "stage-delta-inspect"},
    {"from": "stage-delta-inspect", "to": "stage-echo-publish"}
  ]
}
EOF

cat > "$WORK/linear.json" <<'EOF'
{
  "name": "linear-fixture", "start": "build",
  "nodes": [
    {"id": "build", "type": "send", "role": "build", "action": "build", "message": "m"},
    {"id": "test", "type": "send", "role": "test", "action": "test", "message": "m"},
    {"id": "review", "type": "send", "role": "review", "action": "review", "message": "m"}
  ],
  "edges": [
    {"from": "build", "to": "test"},
    {"from": "test", "to": "review"}
  ]
}
EOF

# ── 1. Fan-out/join DAG frame ────────────────────────────────

echo "-- fan-out/join frame"
FANOUT_RUN=$(start_run "$WORK/fanout.json")
[ -n "$FANOUT_RUN" ] && ok "fan-out run created" || fail "fan-out run created"
set_node "$FANOUT_RUN" start done success
set_node "$FANOUT_RUN" worker-a running
set_node "$FANOUT_RUN" worker-b done success
set_node "$FANOUT_RUN" barrier waiting

frame=$(muxcode graph ui "$FANOUT_RUN" --render-once --width 200 | plain)
for node in start worker-a worker-b barrier; do
  assert_contains "frame lists node $node" "$frame" "$node"
done
assert_contains "done glyph on start" "$frame" "✓ start"
assert_contains "running glyph on worker-a" "$frame" "● worker-a"
assert_contains "waiting glyph on join barrier" "$frame" "◐ barrier"
assert_not_contains "no gate glyph in a gateless graph" "$frame" "⚑"
assert_not_contains "frame is ANSI-stripped by plain()" "$frame" "$ESC"

if muxcode graph ui no-such-run --render-once --width 200 >/dev/null 2>&1; then
  fail "unknown run id must error"
else
  ok "unknown run id must error"
fi

# ── 2. Waiting gate: DAG prominence + run-list badge ─────────

echo "-- waiting gate"
GATED_RUN=$(start_run "$WORK/gated.json")
set_node "$GATED_RUN" review done success
set_node "$GATED_RUN" ship-gate waiting

frame=$(muxcode graph ui "$GATED_RUN" --render-once --width 200 | plain)
assert_contains "gate renders with flag glyph" "$frame" "⚑ ship-gate"

listframe=$(muxcode graph ui --render-once --width 200 | plain)
assert_contains "run list carries the gate badge" "$listframe" "⚑ gate"
assert_contains "run list shows the gated run" "$listframe" "$GATED_RUN"

# ── 3. Gate queue across two runs ────────────────────────────

echo "-- gate queue"
BENIGN_RUN=$(start_run "$WORK/benign.json")
set_node "$BENIGN_RUN" start done success
set_node "$BENIGN_RUN" verify-gate waiting

queue=$(muxcode graph ui --gates --render-once --width 200 | plain)
assert_contains "queue lists the commit-downstream gate" "$queue" "ship-gate"
assert_contains "queue lists the benign gate" "$queue" "verify-gate"
assert_contains "queue shows downstream impact" "$queue" "approval releases:"
# `|| true` on every capture pipeline: a non-matching grep exits 1, and
# under pipefail that would abort the script instead of counting a FAIL —
# an assertion must be able to fail and still be reported.
gate_line=$(printf '%s' "$queue" | grep -F "ship-gate" | head -1 || true)
assert_contains "commit-downstream gate is flagged" "$gate_line" "⚠ mutates"
benign_line=$(printf '%s' "$queue" | grep -F "verify-gate" | head -1 || true)
assert_not_contains "benign gate is not flagged" "$benign_line" "⚠ mutates"

# ── 4. Wide graph falls back to the flat list ────────────────

echo "-- wide fallback"
WIDE_RUN=$(start_run "$WORK/wide.json")
set_node "$WIDE_RUN" stage-alpha-preflight done success
set_node "$WIDE_RUN" stage-bravo-compile failed failure
set_node "$WIDE_RUN" stage-charlie-verify waiting

wide=$(muxcode graph ui "$WIDE_RUN" --render-once --width 60 | plain)
assert_contains "narrow pane renders the flat fallback" "$wide" "flat view"
failed_ln=$(printf '%s\n' "$wide" | grep -nF "stage-bravo-compile" | head -1 | cut -d: -f1 || true)
done_ln=$(printf '%s\n' "$wide" | grep -nF "stage-alpha-preflight" | head -1 | cut -d: -f1 || true)
if [ -n "$failed_ln" ] && [ -n "$done_ln" ] && [ "$failed_ln" -lt "$done_ln" ]; then
  ok "failed node lists before done node"
else
  fail "failed node lists before done node (failed=$failed_ln done=$done_ln)"
fi

widegrid=$(muxcode graph ui "$WIDE_RUN" --render-once --width 250 | plain)
assert_not_contains "wide pane renders the grid (negative control)" "$widegrid" "flat view"

# ── 5. Completed run renders as post-mortem ──────────────────

echo "-- post-mortem"
DONE_RUN=$(start_run "$WORK/linear.json")
for n in build test review; do set_node "$DONE_RUN" "$n" done success; done
runfile="$BUSDIR/graphs/$DONE_RUN/run.json"
jq '.state = "complete" | .updated_at = (.created_at + 60)' "$runfile" > "$runfile.tmp"
mv "$runfile.tmp" "$runfile"

post=$(muxcode graph ui "$DONE_RUN" --render-once --width 200 | plain)
assert_contains "post-mortem shows complete state" "$post" "[complete]"
assert_contains "post-mortem shows full progress" "$post" "3/3 done"
assert_contains "post-mortem elapsed frozen at 1m0s" "$post" "1m0s"
assert_contains "post-mortem shows final node states" "$post" "✓ review"

completed_list=$(muxcode graph ui --render-once --width 200 | plain)
assert_contains "run list includes the completed run" "$completed_list" "$DONE_RUN"

# ── 6. Surface headers carry name + cycle hint (MUX-105) ─────

echo "-- surface headers"
# The pre-existing checks above double as the byte-stability proof: node
# names, glyphs, badges, and ordering are asserted against the same
# frames, so the only header change the cycling work made is the hint.
listframe=$(muxcode graph ui --render-once --width 200 | plain)
assert_contains "run list header names its surface" "$listframe" "Graph Runs"
assert_contains "run list header carries the cycle hint" "$listframe" "Tab: next surface"
queue=$(muxcode graph ui --gates --render-once --width 200 | plain)
assert_contains "gate queue header names its surface" "$queue" "Pending Gates"
assert_contains "gate queue header carries the cycle hint" "$queue" "Tab: next surface"

# ── 7. Removed popups gone, CLI capabilities intact ──────────

echo "-- menu reclamation"
# The six MUX-031 removals plus the three graph popups retired by the
# control pane (MUX-108) — all must be unknown.
for popup in agent-status agent-history memory-context spawn-agent proc-list cron-list graph-runs graph-launch graph-gates; do
  if out=$(muxcode popup "$popup" 2>&1); then
    fail "popup $popup must be unknown"
  else
    assert_contains "popup $popup is unknown" "$out" "unknown popup"
  fi
done

for cli in "status" "history build" "memory context" "proc list" "spawn list" "cron list"; do
  # shellcheck disable=SC2086
  if muxcode $cli >/dev/null 2>&1; then
    ok "muxcode $cli still succeeds"
  else
    fail "muxcode $cli still succeeds"
  fi
done

# ── Summary ──────────────────────────────────────────────────

echo ""
echo "=== $PASS passed, $FAIL failed ==="
[ "$PASS" -ge 34 ] || { echo "FAIL: coverage floor not met ($PASS < 34) — checks did not run"; exit 1; }
[ "$FAIL" -eq 0 ] || exit 1
echo "OK"
