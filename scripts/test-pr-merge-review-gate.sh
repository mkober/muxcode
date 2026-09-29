#!/usr/bin/env bash
# Integration test for the review read before merge (MUX-187).
#
# 110-pr-merge used to ask a human to approve a merge on CI alone: run
# 1790280483 merged PR #89 over six unanswered Copilot comments. The builtin
# now reads the PR's reviews (read-comments -> no-comments, shared with
# 80-pr-review-fix) before ci-watch, and only NO-ACTIONABLE-COMMENTS reaches
# the merge gate.
#
# Drives the real builtin through a scratch daemon with stub commit and watch
# agents whose replies are scripted:
#   1. actionable inline comment — the run holds at open-comments naming the
#      comment id and file:line; ci-watch and merge-gate never open
#   2. a CHANGES_REQUESTED review with no inline comments — same hold, naming
#      the review; the dispatched read tells the agent this state counts
#   3. negative control — resolved threads and bot chatter answered with
#      NO-ACTIONABLE-COMMENTS reach ci-watch, then merge-gate, whose text
#      states both the CI and the review check; merge is never dispatched
#
# The stubs decide the token, so sections 1–2 prove the routing and the
# dispatched wording, not a live agent's judgment of a real PR.
#
# ISOLATION: scratch BUS_SESSION, scratch repo via MUXCODE_SESSION_REPO_DIR,
# lifecycle log in a temp dir, empty config. No GitHub call is made. Every
# muxcode call runs with cwd in the scratch repo: graph templates resolve
# .muxcode/graphs against cwd, so a project override in the caller's repo
# would otherwise mask the builtin under test.
#
# REQUIRES: installed muxcode carrying MUX-187 (run ./build.sh first) — the
# precondition is probed in section 0, since the change is not yet tagged.
#
# Usage: bash scripts/test-pr-merge-review-gate.sh
set -uo pipefail

MUX="${MUXCODE_BIN:-muxcode}"
MUX="$(command -v "$MUX" 2>/dev/null || echo "$MUX")"
case "$MUX" in /*) ;; *) MUX="$PWD/$MUX" ;; esac
if [ ! -x "$MUX" ]; then
  echo "  FAIL  cannot resolve muxcode binary ('$MUX') — set MUXCODE_BIN"
  exit 1
fi
. "$(dirname "${BASH_SOURCE[0]}")/lib/muxcode-version.sh"
require_muxcode_version "$MUX" v0.1.0 MUX-187 || { echo "  FAIL  binary precondition not met"; exit 1; }

GREEN=$'\033[0;32m'; RED=$'\033[0;31m'; NC=$'\033[0m'
pass=0; fail=0
ok()  { echo "  ${GREEN}PASS${NC}  $*"; pass=$((pass + 1)); }
bad() { echo "  ${RED}FAIL${NC}  $*"; fail=$((fail + 1)); dump_diag; }

DUMPED=0
dump_diag() {
  [ "$DUMPED" -eq 1 ] && return
  DUMPED=1
  echo "  --- diagnostic dump (first failure) ---"
  local rid
  for rid in ${RID_A:-} ${RID_B:-} ${RID_C:-}; do
    "$MUX" graph status "$rid" 2>/dev/null | sed $'s/\x1b\\[[0-9;]*[A-Za-z]//g'
  done
  grep -h '"event":"graph-' "$LOG" 2>/dev/null | tail -8
  echo "  --- end diagnostic ---"
}

# --- Isolation -------------------------------------------------------------
export BUS_SESSION="prmerge-test-$$"
BD="/tmp/muxcode-bus-${BUS_SESSION}"
WORK="/tmp/prmerge-work-$$"
REPO="$WORK/repo"
mkdir -p "$REPO"
export HOME="$WORK/home"
mkdir -p "$HOME/.config/muxcode"
git -C "$REPO" init -q
export MUXCODE_LIFECYCLE_LOG_DIR="$WORK/lifecycle"
LOG="$MUXCODE_LIFECYCLE_LOG_DIR/${BUS_SESSION}.log"
: > "$WORK/empty-config"
export MUXCODE_CONFIG="$WORK/empty-config"
export MUXCODE_TMP_CLEANUP_THRESHOLD=0
export MUXCODE_BRANCH_TIME_DISABLE=1
export MUXCODE_DEDUP_WINDOW=0
export MUXCODE_RELAY_SUPPRESS_THRESHOLD=1000
export MUXCODE_SESSION_REPO_DIR="$REPO"

DPID=""
cleanup() {
  [ -n "$DPID" ] && kill "$DPID" 2>/dev/null
  [ "${MUXCODE_TEST_KEEP:-}" = "1" ] && { echo "  (kept for diagnosis: $WORK)"; return; }
  rm -rf "$BD" "$WORK"
}
trap cleanup EXIT

cd "$REPO" || { echo "  FAIL  cannot enter scratch repo $REPO"; exit 1; }
"$MUX" init >/dev/null 2>&1

CAPTURED=""
REQ_ID=""

# wait_request <role> <action> — wait for a matching REQUEST without
# consuming it, capturing its payload and correlation id.
wait_request() {
  local role="$1" action="$2" i out block rid
  for i in $(seq 1 80); do
    out="$(AGENT_ROLE="$role" "$MUX" inbox --peek 2>/dev/null || true)"
    block="$(printf '%s\n' "$out" | awk -v RS='--- Message' -v a="Action: $action" \
      'index($0, "Type: request") && index($0, a) {blk=$0} END{print blk}')"
    if [ -n "$block" ]; then
      rid="$(printf '%s\n' "$block" | grep -o -- '--reply-to [A-Za-z0-9-]*' | head -1 | awk '{print $2}')"
      if [ -n "$rid" ]; then
        REQ_ID="$rid"
        CAPTURED="$(printf '%s\n' "$block" | grep '^Content:' | head -1)"
        return 0
      fi
    fi
    sleep 0.5
  done
  return 1
}

# answer <role> <text> — consume the role's inbox and reply to the captured
# request with a scripted body and EXIT=0 (a completed read is EXIT=0 either way).
answer() {
  AGENT_ROLE="$1" "$MUX" inbox >/dev/null 2>&1
  AGENT_ROLE="$1" "$MUX" send edit response "$2 EXIT=0" --type response --reply-to "$REQ_ID" >/dev/null 2>&1
}

node_state() { "$MUX" graph status "$1" 2>/dev/null | sed $'s/\x1b\\[[0-9;]*[A-Za-z]//g' | awk -v n="$2" '$1==n {print $2}'; }

wait_node_state() {
  local i
  for i in $(seq 1 80); do
    [ "$(node_state "$1" "$2")" = "$3" ] && return 0
    sleep 0.5
  done
  return 1
}

# gate_marker <run-id> <node> — the interpolated prompt the executor wrote for a waiting gate.
gate_marker() { cat "$BD/graphs/$1/approvals/$2.pending" 2>/dev/null; }

# edit_gate_msg <run-id> <node> — the graph-approval request edit received for that gate.
edit_gate_msg() {
  AGENT_ROLE=edit "$MUX" inbox --peek 2>/dev/null | awk -v RS='--- Message' -v r="$1" -v g="\"$2\"" \
    'index($0, "Action: graph-approval") && index($0, r) && index($0, g) {blk=$0} END{print blk}'
}

# node_message <run-id> <node> — the node's declared message from graph status --json.
node_message() {
  local js
  js="$("$MUX" graph status --json "$1" 2>/dev/null)"
  if command -v jq >/dev/null 2>&1; then
    printf '%s' "$js" | jq -r --arg n "$2" '.graph.nodes[] | select(.id == $n) | .message'
  else
    printf '%s' "$js" | python3 -c 'import json,sys; g=json.load(sys.stdin)["graph"]; print(next(n.get("message","") for n in g["nodes"] if n["id"]==sys.argv[1]))' "$2"
  fi
}

no_request_within() { sleep "$3"; ! AGENT_ROLE="$1" "$MUX" inbox --peek 2>/dev/null | grep -q "Action: $2\$"; }

start_run() { "$MUX" graph run 110-pr-merge "$1" 2>&1 | grep -o 'Started run [^ ]*' | awk '{print $3}'; }

# confirm_pr — answer find-pr so the run proceeds to read-comments.
confirm_pr() {
  wait_request commit pr-read || return 1
  answer commit "PR-CONFIRMED #4242 https://github.com/example/repo/pull/4242"
}

# --- 0. Precondition -------------------------------------------------------
"$MUX" graph validate 110-pr-merge >/dev/null 2>&1 \
  && ok "110-pr-merge builtin validates" \
  || bad "110-pr-merge builtin failed validation"

# --- Daemon ----------------------------------------------------------------
"$MUX" watch "$BUS_SESSION" --poll 2 >"$WORK/daemon.log" 2>&1 &
DPID=$!
sleep 1
kill -0 "$DPID" 2>/dev/null && ok "scratch daemon running (pid $DPID)" \
  || bad "scratch daemon exited: $(tail -3 "$WORK/daemon.log" 2>/dev/null)"

# --- 1. One actionable inline comment --------------------------------------
RID_A="$(start_run "actionable comment")"
[ -n "$RID_A" ] && ok "actionable run started: $RID_A" || bad "actionable run failed to start"

if [ -z "$(node_message "$RID_A" read-comments)" ]; then
  bad "run has no read-comments node — installed muxcode predates MUX-187 (run ./build.sh)"
  echo ""; echo "  ${pass} passed, ${fail} failed"; exit 1
fi

confirm_pr || bad "find-pr never dispatched to commit"

if wait_request commit pr-read; then
  case "$CAPTURED" in
    *"Read every review comment"*) ok "read-comments dispatched to commit after find-pr" ;;
    *) bad "second commit pr-read is not the review read: $CAPTURED" ;;
  esac
else
  bad "read-comments never dispatched"
fi
case "$CAPTURED" in
  *"CHANGES_REQUESTED is actionable even with no inline comments"*) ok "dispatched read counts a CHANGES_REQUESTED review with no inline comments" ;;
  *) bad "dispatched read does not count a bare CHANGES_REQUESTED review: $CAPTURED" ;;
esac
case "$CAPTURED" in
  *"Resolved threads, and bot summaries or chatter that ask for no change, are not actionable"*) ok "dispatched read excludes resolved threads and bot chatter" ;;
  *) bad "dispatched read does not exclude resolved threads and bot chatter: $CAPTURED" ;;
esac
answer commit "1 actionable: comment 2987654321 at tools/muxcode/bus/graph_exec.go:1234 — asks to handle a nil run"

wait_node_state "$RID_A" open-comments waiting \
  && ok "run holds at open-comments" \
  || bad "open-comments never reached waiting: $(node_state "$RID_A" open-comments)"

marker="$(gate_marker "$RID_A" open-comments)"
case "$marker" in
  *"NOT MERGING"*"2987654321"*"graph_exec.go:1234"*) ok "open-comments prompt names the comment id and file:line" ;;
  *) bad "open-comments prompt missing id or file:line: $marker" ;;
esac

msg="$(edit_gate_msg "$RID_A" open-comments)"
case "$msg" in
  *"2987654321"*"graph_exec.go:1234"*"80-pr-review-fix"*) ok "edit's gate request names the comment and points at 80-pr-review-fix" ;;
  *) bad "edit's open-comments request missing comment or remedy: $msg" ;;
esac

no_request_within watch watch 5 \
  && ok "ci-watch never dispatched while comments are open" \
  || bad "ci-watch dispatched despite an actionable comment"

[ "$(node_state "$RID_A" merge-gate)" != "waiting" ] && [ -z "$(gate_marker "$RID_A" merge-gate)" ] \
  && ok "merge-gate never opened" \
  || bad "merge-gate opened over an actionable comment: $(node_state "$RID_A" merge-gate)"

# --- 2. CHANGES_REQUESTED with no inline comments --------------------------
RID_B="$(start_run "changes requested")"
[ -n "$RID_B" ] && ok "changes-requested run started: $RID_B" || bad "changes-requested run failed to start"
confirm_pr || bad "changes-requested run: find-pr never dispatched"
wait_request commit pr-read || bad "changes-requested run: read-comments never dispatched"
answer commit "1 actionable: review 5566778899 by reviewer-x, state CHANGES_REQUESTED, no inline comments"

wait_node_state "$RID_B" open-comments waiting \
  && ok "a bare CHANGES_REQUESTED review holds at open-comments" \
  || bad "changes-requested run never held: $(node_state "$RID_B" open-comments)"

case "$(gate_marker "$RID_B" open-comments)" in
  *"5566778899"*"CHANGES_REQUESTED"*) ok "open-comments prompt names the review id and state" ;;
  *) bad "open-comments prompt missing the review: $(gate_marker "$RID_B" open-comments)" ;;
esac

no_request_within watch watch 5 \
  && ok "ci-watch never dispatched over a CHANGES_REQUESTED review" \
  || bad "ci-watch dispatched despite CHANGES_REQUESTED"

# --- 3. Negative control: nothing actionable reaches merge-gate ------------
RID_C="$(start_run "clean review")"
[ -n "$RID_C" ] && ok "clean run started: $RID_C" || bad "clean run failed to start"
confirm_pr || bad "clean run: find-pr never dispatched"
wait_request commit pr-read || bad "clean run: read-comments never dispatched"
answer commit "2 threads, both resolved; Copilot summary asks for no change. NO-ACTIONABLE-COMMENTS"

wait_request watch watch \
  && ok "negative control: resolved threads and bot chatter reach ci-watch" \
  || bad "negative control: ci-watch never dispatched"

[ "$(node_state "$RID_C" open-comments)" != "waiting" ] && [ -z "$(gate_marker "$RID_C" open-comments)" ] \
  && ok "negative control: open-comments never opened" \
  || bad "negative control: open-comments opened on a clean review: $(node_state "$RID_C" open-comments)"

answer watch "all 3 checks passed. CI-GREEN"

wait_node_state "$RID_C" merge-gate waiting \
  && ok "negative control: clean review with green CI reaches merge-gate" \
  || bad "negative control: merge-gate never reached: $(node_state "$RID_C" merge-gate)"

marker="$(gate_marker "$RID_C" merge-gate)"
case "$marker" in
  *"CI is green"*) ok "merge-gate prompt states the CI check" ;;
  *) bad "merge-gate prompt missing the CI clause: $marker" ;;
esac
case "$marker" in
  *"no unresolved review comments"*) ok "merge-gate prompt states the review check" ;;
  *) bad "merge-gate prompt missing the review clause: $marker" ;;
esac

case "$(node_message "$RID_C" merge-gate)" in
  *"CI is green"*"no unresolved review comments"*) ok "graph status --json shows merge-gate text with both clauses" ;;
  *) bad "graph status merge-gate text: $(node_message "$RID_C" merge-gate)" ;;
esac

no_request_within commit commit 5 \
  && ok "merge never dispatched without an approval" \
  || bad "merge dispatched with merge-gate unapproved"

# --- Coverage floor --------------------------------------------------------
# A clean pass executes EXACTLY 23 checks: validate + daemon + 9 actionable
# (start, read dispatched, 2 wording, hold, marker, edit request, no ci-watch,
# no merge-gate) + 4 changes-requested (start, hold, marker, no ci-watch) + 8
# negative control (start, ci-watch, no open-comments, merge-gate, 2 marker
# clauses, status text, no merge). Equality, not >=, so a short-circuited run
# cannot report green.
total=$((pass + fail))
if [ "$total" -eq 23 ]; then
  ok "coverage floor met and equals max ($total checks executed)"
else
  bad "coverage floor mismatch — $total checks executed, want exactly 23"
fi

echo ""
echo "  ${pass} passed, ${fail} failed"
[ "$fail" -eq 0 ] || exit 1
exit 0
