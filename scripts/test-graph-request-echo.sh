#!/usr/bin/env bash
# Integration test for echoed graph requests (MUX-198).
#
# A reply that echoes its own request (the MUX-154 capture failure) used to be
# harvested as the agent's answer. 110-pr-merge's review read names the
# NO-ACTIONABLE-COMMENTS token it asks for and ended "EXIT=0 either way", so an
# echoed recheck-comments passed still-clear and dispatched the merge with no
# gate after it. The executor now treats a reply reproducing the opening or
# closing 20 words of its request as a non-result: the node keeps waiting and
# the genuine reply that follows routes as before.
#
# Drives the real builtin through a scratch daemon with stub commit and watch
# agents whose replies are scripted:
#   1. recheck-comments answered with its own request plus EXIT=0 — the node
#      stays running, still-clear never passes, merge is never dispatched; the
#      genuine NO-ACTIONABLE-COMMENTS reply to the same request then reaches
#      merge (negative control)
#   2. ci-watch answered with its own request (which names CI-GREEN) — merge-gate
#      never opens; the genuine CI-GREEN reply then opens it (negative control)
#
# ISOLATION: scratch BUS_SESSION, scratch repo via MUXCODE_SESSION_REPO_DIR,
# lifecycle log in a temp dir, a scratch HOME config granting gate authority to
# the stub role test-approver. Gate authority reads two fixed paths in order —
# ./.muxcode/config, then ~/.config/muxcode/config — never $MUXCODE_CONFIG, and
# seals the value at daemon startup; the scratch repo has no gate line in its
# .muxcode/config, so the HOME file decides, and it is written first. No
# GitHub call is made; the stubs decide every reply.
#
# REQUIRES: installed muxcode carrying MUX-198 (run ./build.sh first).
#
# Usage: bash scripts/test-graph-request-echo.sh
set -uo pipefail

MUX="${MUXCODE_BIN:-muxcode}"
MUX="$(command -v "$MUX" 2>/dev/null || echo "$MUX")"
case "$MUX" in /*) ;; *) MUX="$PWD/$MUX" ;; esac
if [ ! -x "$MUX" ]; then
  echo "  FAIL  cannot resolve muxcode binary ('$MUX') — set MUXCODE_BIN"
  exit 1
fi
. "$(dirname "${BASH_SOURCE[0]}")/lib/muxcode-version.sh"
require_muxcode_version "$MUX" v0.1.0 MUX-198 || { echo "  FAIL  binary precondition not met"; exit 1; }

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
  for rid in ${RID_A:-} ${RID_B:-}; do
    "$MUX" graph status "$rid" 2>/dev/null | sed $'s/\x1b\\[[0-9;]*[A-Za-z]//g'
  done
  grep -h '"event":"graph-' "$LOG" 2>/dev/null | tail -8
  echo "  --- end diagnostic ---"
}

# --- Isolation -------------------------------------------------------------
export BUS_SESSION="echo-test-$$"
BD="/tmp/muxcode-bus-${BUS_SESSION}"
WORK="/tmp/echo-work-$$"
REPO="$WORK/repo"
mkdir -p "$REPO"
export HOME="$WORK/home"
mkdir -p "$HOME/.config/muxcode"
git -C "$REPO" init -q
export MUXCODE_LIFECYCLE_LOG_DIR="$WORK/lifecycle"
LOG="$MUXCODE_LIFECYCLE_LOG_DIR/${BUS_SESSION}.log"
echo "MUXCODE_GATE_AUTHORITY_ROLES=test-approver" > "$HOME/.config/muxcode/config"
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
# consuming it, capturing its first content line and correlation id.
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
        CAPTURED="$(printf '%s\n' "$block" | grep '^Content:' | head -1 | sed 's/^Content: //')"
        return 0
      fi
    fi
    sleep 0.5
  done
  return 1
}

# answer <role> <text> — consume the role's inbox and reply to the captured
# request with the given body followed by a success verdict.
answer() {
  AGENT_ROLE="$1" "$MUX" inbox >/dev/null 2>&1
  AGENT_ROLE="$1" "$MUX" send edit response "$2 EXIT=0" --type response --reply-to "$REQ_ID" >/dev/null 2>&1
}

# echo_back <role> — answer the captured request with its own text, as a TUI
# capture would, plus a success verdict.
echo_back() { answer "$1" "› $CAPTURED"; }

node_state() { "$MUX" graph status "$1" 2>/dev/null | sed $'s/\x1b\\[[0-9;]*[A-Za-z]//g' | awk -v n="$2" '$1==n {print $2}'; }

wait_node_state() {
  local i
  for i in $(seq 1 80); do
    [ "$(node_state "$1" "$2")" = "$3" ] && return 0
    sleep 0.5
  done
  return 1
}

gate_marker() { cat "$BD/graphs/$1/approvals/$2.pending" 2>/dev/null; }

no_request_within() { sleep "$3"; ! AGENT_ROLE="$1" "$MUX" inbox --peek 2>/dev/null | grep -q "Action: $2\$"; }

start_run() { "$MUX" graph run 110-pr-merge "$1" 2>&1 | grep -o 'Started run [^ ]*' | awk '{print $3}'; }

# to_ci_watch — answer find-pr and read-comments genuinely, leaving the run
# with ci-watch dispatched and captured.
to_ci_watch() {
  wait_request commit pr-read || return 1
  answer commit "PR-CONFIRMED #4242 https://github.com/example/repo/pull/4242"
  wait_request commit pr-read || return 1
  answer commit "All threads resolved. NO-ACTIONABLE-COMMENTS"
  wait_request watch watch
}

# --- 0. Precondition -------------------------------------------------------
"$MUX" graph validate 110-pr-merge >/dev/null 2>&1 \
  && ok "110-pr-merge builtin validates" \
  || bad "110-pr-merge builtin failed validation"

"$MUX" watch "$BUS_SESSION" --poll 2 >"$WORK/daemon.log" 2>&1 &
DPID=$!
sleep 1
kill -0 "$DPID" 2>/dev/null && ok "scratch daemon running (pid $DPID)" \
  || bad "scratch daemon exited: $(tail -3 "$WORK/daemon.log" 2>/dev/null)"

# --- 1. Echoed recheck-comments never merges -------------------------------
RID_A="$(start_run "echoed recheck")"
[ -n "$RID_A" ] && ok "recheck run started: $RID_A" || bad "recheck run failed to start"

to_ci_watch || bad "recheck run: never reached ci-watch"
answer watch "all checks passed. CI-GREEN"
wait_node_state "$RID_A" merge-gate waiting \
  && ok "recheck run reaches merge-gate on genuine replies" \
  || bad "recheck run: merge-gate never reached: $(node_state "$RID_A" merge-gate)"

AGENT_ROLE=test-approver "$MUX" graph approve "$RID_A" merge-gate >/dev/null 2>&1 \
  && ok "merge-gate approved by the authorized stub" \
  || bad "merge-gate approval refused"

if wait_request commit pr-read; then
  case "$CAPTURED" in
    *"Read every review comment"*NO-ACTIONABLE-COMMENTS*) ok "recheck-comments dispatched; its request names NO-ACTIONABLE-COMMENTS" ;;
    *) bad "recheck request is not the review read naming the token: $CAPTURED" ;;
  esac
else
  bad "recheck-comments never dispatched after approval"
fi
echo_back commit

no_request_within commit commit 8 \
  && ok "echoed recheck never dispatches the merge" \
  || bad "merge dispatched on an echoed recheck-comments reply"
[ "$(node_state "$RID_A" recheck-comments)" = "running" ] \
  && ok "recheck-comments keeps waiting after the echo" \
  || bad "recheck-comments = $(node_state "$RID_A" recheck-comments) after the echo, want running"
[ "$(node_state "$RID_A" new-comments)" != "waiting" ] \
  && ok "the echo raises no gate" \
  || bad "the echo opened new-comments"

AGENT_ROLE=commit "$MUX" send edit response "Re-read: 0 unresolved threads. NO-ACTIONABLE-COMMENTS EXIT=0" \
  --type response --reply-to "$REQ_ID" >/dev/null 2>&1
wait_request commit commit \
  && ok "negative control: the genuine clear reply dispatches the merge" \
  || bad "negative control: merge never dispatched after the genuine reply: $(node_state "$RID_A" recheck-comments)"

# --- 2. Echoed ci-watch never opens merge-gate -----------------------------
RID_B="$(start_run "echoed ci-watch")"
[ -n "$RID_B" ] && ok "ci-watch run started: $RID_B" || bad "ci-watch run failed to start"

if to_ci_watch; then
  case "$CAPTURED" in
    *CI-GREEN*) ok "ci-watch request names CI-GREEN" ;;
    *) bad "ci-watch request does not name CI-GREEN: $CAPTURED" ;;
  esac
else
  bad "ci-watch run: ci-watch never dispatched"
fi
echo_back watch

sleep 8
[ "$(node_state "$RID_B" merge-gate)" != "waiting" ] && [ -z "$(gate_marker "$RID_B" merge-gate)" ] \
  && ok "echoed ci-watch never opens merge-gate" \
  || bad "merge-gate opened on an echoed ci-watch reply"
[ "$(node_state "$RID_B" ci-watch)" = "running" ] \
  && ok "ci-watch keeps waiting after the echo" \
  || bad "ci-watch = $(node_state "$RID_B" ci-watch) after the echo, want running"

AGENT_ROLE=watch "$MUX" send edit response "test — pass. CI-GREEN EXIT=0" \
  --type response --reply-to "$REQ_ID" >/dev/null 2>&1
wait_node_state "$RID_B" merge-gate waiting \
  && ok "negative control: the genuine CI-GREEN reply opens merge-gate" \
  || bad "negative control: merge-gate never opened: $(node_state "$RID_B" merge-gate)"

# --- Coverage floor --------------------------------------------------------
# A clean pass executes EXACTLY 15 checks: validate + daemon + 8 recheck
# (start, merge-gate, approve, dispatch, no merge, running, no gate, control)
# + 5 ci-watch (start, request, no merge-gate, running, control). Equality,
# not >=, so a short-circuited run cannot report green.
total=$((pass + fail))
if [ "$total" -eq 15 ]; then
  ok "coverage floor met and equals max ($total checks executed)"
else
  bad "coverage floor mismatch — $total checks executed, want exactly 15"
fi

echo ""
echo "  ${pass} passed, ${fail} failed"
[ "$fail" -eq 0 ] || exit 1
exit 0
