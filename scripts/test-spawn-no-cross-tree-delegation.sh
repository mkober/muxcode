#!/usr/bin/env bash
# Integration test for MUX-142: a request to a tree-scoped agent (build, test,
# review) issued from a different git tree than the receiving agent's pane is
# refused at `muxcode send` (CheckCrossTree), instead of being answered silently
# about the main checkout — the 2026-09-02 worker told "tests green" about a
# tree holding none of its code, and MUX-136's review re-reporting a finding
# already fixed in a worktree.
#
# Hermetic: a scratch bus session, a scratch git repo plus a real `git worktree`,
# scratch tmux windows whose agent pane sits in the main checkout (tagged
# @muxcode_pane=agent, the identity PaneTarget resolves), lifecycle log in a temp
# dir. No daemon, no agents; every send uses --no-notify.
#
# Sections: (A) a spawn worker in the worktree is refused for build, test and
# review — non-zero exit, no inbox row, no task; (B) negative control: edit in
# the main checkout, and in a subdirectory of it, still delegates build/test;
# (C) negative control: the worker's message to a non-tree-scoped peer (plan)
# delivers; (D) one cross-tree-refused lifecycle row per refused request, and a
# refusal leaves nothing behind to re-drive; (E) a worker that cannot verify is
# told so at once — a refused --wait returns non-zero without waiting, naming
# both trees, so no delegated green can ever reach it; (F) MUXCODE_CROSS_TREE_GUARD=0
# lets the same worktree request through, proving the guard is what refused.
#
# DEVIATION from the spec checklist: graph spawn workers no longer take a
# worktree (graph_exec.go), so the worktree worker here is the CLI-spawn shape;
# the graph-owned-role refusal (CheckGraphNodeAuthority) is covered by
# bus/graph_authority_test.go.
#
# Coverage floor: the exact pass count.
#
# Requires installed muxcode carrying CheckCrossTree (run ./build.sh first) and tmux.
set -uo pipefail

PASS=0
FAIL=0
EXPECTED_PASS=19

MUX="${MUXCODE_BIN:-muxcode}"
MUX="$(command -v "$MUX" 2>/dev/null || echo "$MUX")"
[ -x "$MUX" ] || { echo "  FAIL  cannot resolve muxcode binary ('$MUX') — set MUXCODE_BIN"; exit 1; }
command -v tmux >/dev/null 2>&1 || { echo "  FAIL  tmux not available"; exit 1; }
command -v git >/dev/null 2>&1 || { echo "  FAIL  git not available"; exit 1; }
. "$(dirname "${BASH_SOURCE[0]}")/lib/muxcode-version.sh"
require_muxcode_version "$MUX" v0.1.0 MUX-142 || { echo "  FAIL  binary precondition not met"; exit 1; }

ok()   { PASS=$((PASS + 1)); echo "  ok: $1"; }
fail() { FAIL=$((FAIL + 1)); echo "  FAIL: $1"; }

export BUS_SESSION="cross-tree-test-$$"
BD="/tmp/muxcode-bus-${BUS_SESSION}"
WORK=$(mktemp -d /tmp/cross-tree-work-XXXXXX)
MAIN="$WORK/main"
WT="$WORK/wt"
export MUXCODE_LIFECYCLE_LOG_DIR="$WORK/lifecycle"
: > "$WORK/empty-config"
export MUXCODE_CONFIG="$WORK/empty-config"
export MUXCODE_DEDUP_WINDOW=0
export MUXCODE_BRANCH_TIME_DISABLE=1
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_SYSTEM=/dev/null
unset MUXCODE_CROSS_TREE_GUARD 2>/dev/null || true
LIFELOG="$MUXCODE_LIFECYCLE_LOG_DIR/${BUS_SESSION}.log"
WORKER="spawn-c0ffee42"

cleanup() {
  tmux kill-session -t "$BUS_SESSION" 2>/dev/null
  rm -rf "$BD" "$WORK"
}
trap cleanup EXIT

g() { git -C "$MAIN" -c user.email=t@localhost -c user.name=t -c commit.gpgsign=false "$@" >/dev/null 2>&1; }
mkdir -p "$MAIN/sub" "$MUXCODE_LIFECYCLE_LOG_DIR" "$BD/inbox"
git init -q "$MAIN" || { echo "  FAIL  git init"; exit 1; }
echo base > "$MAIN/sub/base.txt"
g add -A && g commit -q -m base && g worktree add --detach "$WT" HEAD || { echo "  FAIL  git worktree setup"; exit 1; }

# One agent pane per tree-scoped role, sitting in the main checkout.
tmux new-session -d -s "$BUS_SESSION" -n edit -c "$MAIN" 'sleep 600'
for w in build test review plan; do
  tmux new-window -d -t "$BUS_SESSION" -n "$w" -c "$MAIN" 'sleep 600'
done
for w in edit build test review plan; do
  tmux set-option -p -t "$BUS_SESSION:$w.0" @muxcode_pane agent >/dev/null 2>&1
  tmux set-option -w -t "$BUS_SESSION:$w" @muxcode_tagged 1 >/dev/null 2>&1
done

# rows TO MARKER — inbox rows addressed to TO whose payload carries MARKER, across every inbox file.
rows() { cat "$BD"/inbox/*.jsonl 2>/dev/null | grep -F "\"to\":\"$1\"" | grep -cF "$2"; }
tasks() { ls "$BD/tasks" 2>/dev/null | wc -l | tr -d ' '; }
refusals() { local n; n=$(grep -c '"cross-tree-refused"' "$LIFELOG" 2>/dev/null); echo "${n:-0}"; }
send_from() { local dir=$1 role=$2; shift 2; (cd "$dir" && AGENT_ROLE="$role" "$MUX" send "$@" --no-notify); }

echo "=== cross-tree delegation integration test (MUX-142) ==="

# ── A: worktree worker → tree-scoped roles refused ───────────────
echo "-- worktree worker refused"
T0=$(tasks)
for r in build test review; do
  if send_from "$WT" "$WORKER" "$r" "$r" "A-$r from worktree" >/dev/null 2>&1; then
    fail "$r request from the worktree was accepted"
  else
    ok "$r request from the worktree exits non-zero"
  fi
  [ "$(rows "$r" "A-$r from worktree")" = "0" ] && ok "no $r inbox row written" || fail "$r inbox row written despite refusal"
done
[ "$(tasks)" = "$T0" ] && ok "no task recorded for a refused request" || fail "task count moved $T0 → $(tasks)"

# ── B: negative control — edit in the main checkout still delegates
echo "-- edit in the main checkout"
for r in build test; do
  send_from "$MAIN" edit "$r" "$r" "B-$r from main" >/dev/null 2>&1 && ok "edit $r request from main accepted" || fail "edit $r request from main refused"
  [ "$(rows "$r" "B-$r from main")" = "1" ] && ok "edit $r request delivered to its inbox" || fail "edit $r request not delivered"
done
send_from "$MAIN/sub" edit review review "B-review from subdir" >/dev/null 2>&1 \
  && [ "$(rows review "B-review from subdir")" = "1" ] \
  && ok "a subdirectory of the same checkout is the same tree" || fail "subdirectory request refused or undelivered"

# ── C: negative control — non-tree-scoped peer delivers ──────────
echo "-- worker to a peer"
send_from "$WT" "$WORKER" plan update-docs "C-peer from worktree" >/dev/null 2>&1 && ok "worker request to plan accepted" || fail "worker request to plan refused"
[ "$(rows plan "C-peer from worktree")" = "1" ] && ok "worker request to plan delivered" || fail "worker request to plan not delivered"

# ── D: one lifecycle row per refusal, nothing left to re-drive ───
echo "-- lifecycle accounting"
[ "$(refusals)" = "3" ] && ok "three refusals logged exactly three cross-tree-refused rows" || fail "expected 3 cross-tree-refused rows, got $(refusals)"
send_from "$WT" "$WORKER" build build "D-one more" >/dev/null 2>&1
[ "$(refusals)" = "4" ] && ok "one more refusal adds exactly one row" || fail "expected 4 rows after one more refusal, got $(refusals)"

# ── E: a worker that cannot verify is told so, at once ───────────
echo "-- refused --wait reports instead of waiting"
start=$(date +%s)
err=$(cd "$WT" && AGENT_ROLE="$WORKER" "$MUX" send test test "E-wait from worktree" --wait --no-notify 2>&1 >/dev/null); rc=$?
elapsed=$(( $(date +%s) - start ))
if [ "$rc" -ne 0 ] && [ "$elapsed" -lt 10 ]; then ok "refused --wait returns non-zero in ${elapsed}s, never awaiting a reply"; else fail "--wait rc=$rc after ${elapsed}s"; fi
wt_root=$(git -C "$WT" rev-parse --show-toplevel); main_root=$(git -C "$MAIN" rev-parse --show-toplevel)
if grep -qF "$wt_root" <<<"$err" && grep -qF "$main_root" <<<"$err"; then ok "refusal names the worker's tree and the agent's tree"; else fail "refusal does not name both trees: ${err:-<empty>}"; fi

# ── F: the guard, and only the guard, refused ────────────────────
echo "-- opt-out"
# --force: B left an in-flight build:build task, which a tracked send would reattach to instead of sending.
(cd "$WT" && MUXCODE_CROSS_TREE_GUARD=0 AGENT_ROLE="$WORKER" "$MUX" send build build "F-opt-out" --no-notify --force >/dev/null 2>&1)
[ "$(rows build "F-opt-out")" = "1" ] && ok "MUXCODE_CROSS_TREE_GUARD=0 lets the worktree request through" || fail "opt-out request not delivered"

echo
echo "=== results: $PASS passed, $FAIL failed (floor $EXPECTED_PASS) ==="
[ "$FAIL" -eq 0 ] || exit 1
if [ "$PASS" -ne "$EXPECTED_PASS" ]; then
  echo "  FAIL  coverage floor: expected exactly $EXPECTED_PASS passes, got $PASS — a section was skipped or double-counted"
  exit 1
fi
echo "PASS"
