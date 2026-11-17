#!/usr/bin/env bash
# Integration test for MUX-219 — a graph run's review is verified once.
#
# When a graph run owned the review, plan was asked to verify it twice in the
# same second: by the daemon's review chain (notifyPlanOnReview) and by the
# run's own update-spec node. On run 1794626267-55-defect-backlog-loop-15547e2e
# plan answered every phase twice. The daemon now resolves the review reply's
# --reply-to to its request; a request stamped with a graph run sends plan no
# daemon verify-spec and writes plan-verify-graph-owned instead.
#
#   A. A run `review` → `update-spec`: the script plays review and answers the
#      dispatch clean; plan receives exactly one verify-spec — the run's — one
#      plan-verify-graph-owned row names the run and node, the reviewed marker
#      records the reply and the movement marker stays unwritten
#   B. The same run, review answered with a should-fix: it routes to `fix`, and
#      plan receives no verify-spec at all
#   C. Negative control: a review edit requests by hand, answered with
#      --reply-to, fires exactly one daemon verify-spec, with no graph-owned row
#   D. Negative control: the same while a run waits on a wait_human gate —
#      a live run does not own a review it did not dispatch
#   E. Control: A on a build with the ownership check bypassed sends plan two
#      verify-specs for the one review — the duplicate
#
# ISOLATION: muxcode built from the tree (MUXCODE_GRAPH_VERIFY_ONCE_BIN
# overrides the fixed binary); scratch HOME, XDG_STATE_HOME, lifecycle log,
# config, memory and committed repo; one BUS_SESSION and daemon per section; a
# private tmux server (TMUX_TMPDIR in the work dir, TMUX unset, killed on exit).
#
# REQUIRES: go, tmux, jq, git. ~1 min, mostly the two builds.
#
# Usage: bash scripts/test-graph-verify-once.sh
set -uo pipefail
export MUXCODE_CONSUMER_GUARD=0 # review and plan are played by the script, which no window or listener shows (MUX-145)

FLOOR=21
REPO_ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)

GREEN=$'\033[0;32m'; RED=$'\033[0;31m'; NC=$'\033[0m'
pass=0; fail=0
ok()  { echo "  ${GREEN}PASS${NC}  $*"; pass=$((pass + 1)); }
bad() { echo "  ${RED}FAIL${NC}  $*"; fail=$((fail + 1)); }

for tool in go tmux jq git; do
  command -v "$tool" >/dev/null 2>&1 || { echo "  FAIL  $tool is required"; exit 1; }
done

WORK=$(mktemp -d "${TMPDIR:-/tmp}/graph-verify-once-XXXXXX")
mkdir -p "$WORK/tmux"
export TMUX_TMPDIR="$WORK/tmux"
unset TMUX TMUX_PANE
DPIDS=""
cleanup() {
  local p
  for p in $DPIDS; do kill "$p" 2>/dev/null; done
  tmux kill-server 2>/dev/null
  rm -rf "$WORK"
}
trap cleanup EXIT
GOCACHE="$(go env GOCACHE)"; GOPATH="$(go env GOPATH)"
export GOCACHE GOPATH

# --- Isolation -------------------------------------------------------------
export HOME="$WORK/home"
export XDG_STATE_HOME="$HOME/.local/state"
REPO="$WORK/repo"
mkdir -p "$HOME/.config/muxcode" "$WORK/lifecycle" "$WORK/config-dir" "$REPO/docs/requirements/drafts"
: > "$WORK/empty-config"
export MUXCODE_CONFIG="$WORK/empty-config"
export MUXCODE_CONFIG_DIR="$WORK/config-dir"
export MUXCODE_LIFECYCLE_LOG_DIR="$WORK/lifecycle"
export BUS_MEMORY_DIR="$WORK/memory" BUS_GLOBAL_MEMORY_DIR="$WORK/global-memory"
export MUXCODE_SESSION_REPO_DIR="$REPO"
export MUXCODE_DEDUP_WINDOW=0 MUXCODE_RELAY_SUPPRESS_THRESHOLD=0 MUXCODE_TMP_CLEANUP_THRESHOLD=0
export MUXCODE_BRANCH_TIME_DISABLE=1 MUXCODE_FORCE_RESPOND_DISABLE=1 MUXCODE_EDIT_AUTO_RESTART_DISABLE=1
export MUXCODE_CONTROL_PANE_DISABLE=1 MUXCODE_AGENTDEFS_WATCH_DISABLE=1 MUXCODE_STUCK_RELOAD_DISABLE=1
export MUXCODE_PROMPT_AGENT_DISABLE=1
unset MUXCODE_BUS_BASE MUXCODE_BUS_DIR AGENT_ROLE BUS_ROLE BUS_SESSION MUXCODE_AGENT_CLI MUXCODE

SPEC="docs/requirements/drafts/verify-once-fixture.md"
printf '# Verify Once Fixture\n\n## Requirements\n\n- [ ] an open item\n\n## Status\n\nIn Progress\n' >"$REPO/$SPEC"
git -C "$REPO" init -q 2>/dev/null
git -C "$REPO" add -A 2>/dev/null
git -C "$REPO" -c user.email=t@t -c user.name=t -c commit.gpgsign=false -c core.hooksPath= commit -q -m base 2>/dev/null

# --- Binaries --------------------------------------------------------------
echo "--- binaries"
if [ -n "${MUXCODE_GRAPH_VERIFY_ONCE_BIN:-}" ]; then
  MUX="$MUXCODE_GRAPH_VERIFY_ONCE_BIN"; ok "fixed binary from MUXCODE_GRAPH_VERIFY_ONCE_BIN: $MUX"
else
  MUX="$WORK/muxcode"
  (cd "$REPO_ROOT/tools/muxcode" && go build -buildvcs=false -o "$MUX" .) >"$WORK/build.log" 2>&1 \
    && ok "built muxcode from the tree" || { bad "go build failed: $(tail -3 "$WORK/build.log")"; exit 1; }
fi
CTL_TREE="$WORK/ctl-tree"
cp -R "$REPO_ROOT/tools/muxcode" "$CTL_TREE"
perl -pi -e 's/if run, node, owned := graphOwnedReview\(d\.session, reply\); owned \{/if run, node, owned := graphOwnedReview(d.session, reply); false && owned {/' "$CTL_TREE/daemon/daemon.go"
CTL="$WORK/muxcode-prefix"
if grep -q 'graphOwnedReview(d.session, reply); false && owned {' "$CTL_TREE/daemon/daemon.go" \
  && (cd "$CTL_TREE" && go build -buildvcs=false -o "$CTL" .) >"$WORK/ctl-build.log" 2>&1; then
  ok "built the control: the graph-ownership check bypassed"
else
  bad "control build or patch failed: $(tail -3 "$WORK/ctl-build.log" 2>/dev/null)"
fi

cat >"$WORK/graph-review.json" <<'EOF'
{"name": "mux219-verify-once", "start": "review",
 "nodes": [{"id": "review", "type": "send", "role": "review", "action": "review", "message": "Review the latest changes"},
           {"id": "update-spec", "type": "send", "role": "plan", "action": "verify-spec", "message": "Verify the active spec against the reviewed work"},
           {"id": "fix", "type": "send", "role": "edit", "action": "fix", "message": "Fix the review findings"}],
 "edges": [{"from": "review", "to": "update-spec"},
           {"from": "review", "to": "fix", "outcome": "failure"}]}
EOF
cat >"$WORK/graph-gate.json" <<'EOF'
{"name": "mux219-parked", "start": "gate",
 "nodes": [{"id": "gate", "type": "wait_human", "message": "Hold here"},
           {"id": "after", "type": "send", "role": "edit", "action": "after-gate", "message": "after the gate"}],
 "edges": [{"from": "gate", "to": "after"}]}
EOF

# --- Helpers ---------------------------------------------------------------
# start_session <bin> <name> — a fresh bus with the fixture spec active, its
# tmux session and a daemon on bin.
start_session() {
  MUX_NOW=$1
  export BUS_SESSION="verify-once-$2-$$"
  BD="$("$1" bus-dir "$BUS_SESSION")" || exit 1
  LIFELOG="$MUXCODE_LIFECYCLE_LOG_DIR/$BUS_SESSION.log"
  "$1" init >/dev/null 2>&1
  (cd "$REPO" && "$1" spec set "$SPEC" >/dev/null 2>&1)
  tmux new-session -d -s "$BUS_SESSION" -n edit 2>/dev/null
  (cd "$WORK" && exec "$1" watch "$BUS_SESSION" --poll 1) >"$WORK/daemon-$2.log" 2>&1 &
  DPIDS="$DPIDS $!"
  sleep 1
}
start_run() { (cd "$REPO" && "$MUX_NOW" graph run --file "$1" 2>&1) | grep -o 'Started run [^ ]*' | awk '{print $3}'; }
review_dispatch() { jq -r --arg r "$1" 'select(.type == "request" and .to == "review" and .graph_run == $r) | .id' "$BD/log.jsonl" 2>/dev/null | head -1; }
verify_specs()  { jq -c 'select(.type == "request" and .to == "plan" and .action == "verify-spec")' "$BD/log.jsonl" 2>/dev/null | grep -c . || true; }
daemon_specs()  { jq -c 'select(.type == "request" and .to == "plan" and .action == "verify-spec" and ((.graph_run // "") == ""))' "$BD/log.jsonl" 2>/dev/null | grep -c . || true; }
graph_specs()   { jq -c --arg r "$1" 'select(.type == "request" and .to == "plan" and .action == "verify-spec" and .graph_run == $r)' "$BD/log.jsonl" 2>/dev/null | grep -c . || true; }
fix_dispatch()  { jq -c --arg r "$1" 'select(.type == "request" and .to == "edit" and .action == "fix" and .graph_run == $r)' "$BD/log.jsonl" 2>/dev/null | grep -c . || true; }
owned_rows()    { jq -r 'select(.event == "plan-verify-graph-owned") | .detail' "$LIFELOG" 2>/dev/null; }
plan_verify_rows() { jq -r 'select(.event == "plan-verify") | .detail' "$LIFELOG" 2>/dev/null | grep -c . || true; }
node_state()    { jq -r '.state // ""' "$BD/graphs/$1/nodes/$2.json" 2>/dev/null; }
wait_until() {
  local n=$(($1 * 4)) i; shift
  for ((i = 0; i < n; i++)); do "$@" && return 0; sleep 0.25; done
  return 1
}
has_review_dispatch() { [ -n "$(review_dispatch "$1")" ]; }
has_owned_row()  { [ -n "$(owned_rows)" ]; }
has_graph_spec() { [ "$(graph_specs "$1")" -ge 1 ]; }
has_fix()        { [ "$(fix_dispatch "$1")" -ge 1 ]; }
has_plan_verify(){ [ "$(plan_verify_rows)" -ge 1 ]; }
# answer_review <request-id> <text> — the review agent answers to edit.
answer_review() {
  AGENT_ROLE=review "$MUX_NOW" send edit review "$2" --type response --reply-to "$1" >/dev/null 2>&1
}
CLEAN=$'0 must-fix, 0 should-fix, 0 nits\nReviewed. EXIT=0'
SHOULD=$'0 must-fix, 1 should-fix, 0 nits\nOne should-fix. EXIT=0'

# graph_review <bin> <name> <verdict> — section A/B/E's setup: a run whose
# review dispatch the script answers with verdict. Sets RID and REQ.
graph_review() {
  start_session "$1" "$2"
  RID=$(start_run "$WORK/graph-review.json")
  wait_until 20 has_review_dispatch "$RID"
  REQ=$(review_dispatch "$RID")
  answer_review "$REQ" "$3"
}

# --- A. A clean graph-owned review: one verification, the run's -------------
echo "--- A. a graph run's clean review is verified once, by the run"
graph_review "$MUX" a "$CLEAN"
[ -n "$REQ" ] && ok "A: the run dispatched its review ($REQ)" || bad "A: no review dispatch for run '$RID' ($(tail -2 "$WORK/daemon-a.log"))"
wait_until 20 has_graph_spec "$RID" && ok "A: the run's update-spec sent plan its verify-spec" || bad "A: no update-spec dispatch"
wait_until 20 has_owned_row && ok "A: the daemon logged plan-verify-graph-owned" || bad "A: no plan-verify-graph-owned row"
sleep 2
[ "$(verify_specs)" = 1 ] && [ "$(daemon_specs)" = 0 ] \
  && ok "A: plan received exactly one verify-spec — the run's, none from the daemon" \
  || bad "A: verify-specs $(verify_specs), from the daemon $(daemon_specs)"
row=$(owned_rows)
[ "$(echo "$row" | grep -c .)" = 1 ] && echo "$row" | grep -qF "$RID" && echo "$row" | grep -qF "node review" \
  && ok "A: one row names the run and the node" || bad "A: rows: $row"
[ "$(plan_verify_rows)" = 0 ] && ok "A: no plan-verify row from the daemon" || bad "A: $(plan_verify_rows) plan-verify rows"
[ "$(cat "$BD/reviewed-transition.last" 2>/dev/null | tr -d '[:space:]')" != "" ] \
  && grep -q '"state":"reviewed"' "$BD/workflow-state.json" 2>/dev/null \
  && ok "A: the reviewed marker and transition still happen" || bad "A: reviewed marker or state missing"
[ ! -f "$BD/verify-movement.last" ] && ok "A: the movement marker stays unwritten" \
  || bad "A: movement marker written: $(cat "$BD/verify-movement.last")"

# --- B. A graph-owned review with a should-fix routes to fix -----------------
echo "--- B. a graph-owned review routed to fix sends plan nothing"
graph_review "$MUX" b "$SHOULD"
wait_until 20 has_fix "$RID" && ok "B: the review routed to the run's fix node" || bad "B: no fix dispatch"
wait_until 20 has_owned_row && ok "B: the daemon logged plan-verify-graph-owned" || bad "B: no plan-verify-graph-owned row"
sleep 2
[ "$(verify_specs)" = 0 ] && ok "B: plan received no verify-spec at all" || bad "B: $(verify_specs) verify-spec(s) — daemon $(daemon_specs)"

# --- C. Negative control: a hand-delegated review ----------------------------
echo "--- C. control — a review edit requested by hand"
start_session "$MUX" c
AGENT_ROLE=edit "$MUX_NOW" send review review "Review the latest changes" --no-notify >/dev/null 2>&1
HAND=$(jq -r 'select(.type == "request" and .to == "review" and .from == "edit") | .id' "$BD/log.jsonl" 2>/dev/null | tail -1)
answer_review "$HAND" "$CLEAN"
wait_until 20 has_plan_verify && ok "C: the daemon fired plan-verify" || bad "C: no plan-verify row ($(tail -2 "$WORK/daemon-c.log"))"
sleep 2
[ "$(daemon_specs)" = 1 ] && [ "$(verify_specs)" = 1 ] && ok "C: exactly one verify-spec, from the daemon" \
  || bad "C: verify-specs $(verify_specs), daemon $(daemon_specs)"
[ -z "$(owned_rows)" ] && ok "C: no plan-verify-graph-owned row" || bad "C: graph-owned row for a hand review: $(owned_rows)"

# --- D. Negative control: a hand review while a run waits on a gate ----------
echo "--- D. control — a hand review while a run waits on a gate"
start_session "$MUX" d
GRID=$(start_run "$WORK/graph-gate.json")
gate_waiting() { [ "$(node_state "$GRID" gate)" = waiting ]; }
wait_until 20 gate_waiting && ok "D: a run waits on its wait_human gate ($GRID)" || bad "D: gate state '$(node_state "$GRID" gate)'"
AGENT_ROLE=edit "$MUX_NOW" send review review "Review the post-close fix" --no-notify >/dev/null 2>&1
HAND=$(jq -r 'select(.type == "request" and .to == "review" and .from == "edit") | .id' "$BD/log.jsonl" 2>/dev/null | tail -1)
answer_review "$HAND" "$CLEAN"
wait_until 20 has_plan_verify && ok "D: the daemon fired plan-verify" || bad "D: no plan-verify row"
sleep 2
[ "$(daemon_specs)" = 1 ] && [ -z "$(owned_rows)" ] \
  && ok "D: exactly one verify-spec — the parked run does not own the hand review" \
  || bad "D: daemon verify-specs $(daemon_specs), owned rows: $(owned_rows)"

# --- E. Control: the ownership check bypassed ------------------------------
echo "--- E. control — the ownership check bypassed"
graph_review "$CTL" e "$CLEAN"
wait_until 20 has_graph_spec "$RID" && ok "E: the run's update-spec sent its verify-spec" || bad "E: no update-spec dispatch"
two() { [ "$(verify_specs)" -ge 2 ]; }
wait_until 20 two && [ "$(daemon_specs)" = 1 ] \
  && ok "E: plan received two verify-specs for one review — the daemon's and the run's, the duplicate" \
  || bad "E: verify-specs $(verify_specs), daemon $(daemon_specs)"

# --- Coverage floor ---------------------------------------------------------
echo
echo "=== Summary: $pass passed, $fail failed (floor $FLOOR) ==="
if [ "$pass" -lt "$FLOOR" ]; then
  echo "  ${RED}FAIL${NC}  coverage floor: $pass passes is below $FLOOR — a section was skipped"
  exit 1
fi
[ "$fail" -eq 0 ] || exit 1
exit 0
