#!/usr/bin/env bash
# Integration test for MUX-150 — verify-spec names the change set, not the last
# routed batch.
#
# On 2026-09-08 three verify-spec requests in four minutes each named ONE file
# while 5, 5 and 8 Go files were dirty; the third named plan's own spec write
# and no code file at all. "Changed files" was the workflow entry's LastFiles —
# the last <= 5 paths of whichever write batch ran last — and every docs write
# plan made while verifying regressed the workflow and became that list.
#
# Drives the real CLI end to end: a scratch git repo pinned with
# MUXCODE_SESSION_REPO_DIR, a real `muxcode watch` daemon (2 s debounce),
# writes reported through the real `muxcode hook analyze` as each window, and
# review completions sent over the bus — the test-verify-spec-refire.sh
# harness. Sections:
#   A  three code files dirtied across two debounce batches, plan writing the
#      spec between them: the verify-spec names all three and never the spec,
#      and plan's docs write neither routed nor moved the workflow
#   B  one more code file changes: the second verify-spec names that file and
#      not the three the first already covered
#   C  plan's post-review spec write leaves the workflow at reviewed; negative
#      control — an edit-window code write still routes to analyze and
#      regresses reviewed -> editing
#   D  negative control — exactly one verify-spec per review completion, and a
#      repeat completion with nothing moved fires none (MUX-007's gates)
# A coverage floor keeps a skipped section from reporting green.
#
# ISOLATION: scratch BUS_SESSION, work dir under /tmp, a private tmux server,
# lifecycle log in the work dir, empty config. The edit window's pane runs
# `cat` so the hook's nvim diff cleanup types into nothing that executes.
#
# REQUIRES: tmux, git, jq; a muxcode with MUX-150 — the installed one, or
# MUXCODE_BIN.
#
# Usage: bash scripts/test-verify-spec-files.sh
set -uo pipefail
export MUXCODE_CONSUMER_GUARD=0 # agents here are played by reading their inboxes (MUX-145)

MUX="${MUXCODE_BIN:-muxcode}"
MUX="$(command -v "$MUX" 2>/dev/null || echo "$MUX")"
[ -x "$MUX" ] || { echo "  FAIL  cannot resolve muxcode binary ('$MUX') — set MUXCODE_BIN"; exit 1; }
for t in tmux git jq; do
  command -v "$t" >/dev/null 2>&1 || { echo "SKIP: $t is required"; exit 2; }
done
. "$(dirname "${BASH_SOURCE[0]}")/lib/muxcode-bus-dir.sh"

GREEN=$'\033[0;32m'; RED=$'\033[0;31m'; NC=$'\033[0m'
pass=0; fail=0
EXPECTED=21
ok()  { echo "  ${GREEN}PASS${NC}  $*"; pass=$((pass + 1)); }
bad() { echo "  ${RED}FAIL${NC}  $*"; fail=$((fail + 1)); }
check() { local desc=$1; shift; if "$@" >/dev/null 2>&1; then ok "$desc"; else bad "$desc"; fi; }

# --- Isolation -------------------------------------------------------------
export BUS_SESSION="verify-files-test-$$"
BD="$(muxcode_bus_dir "$MUX" "$BUS_SESSION")" || exit 1
WORK="/tmp/verify-files-work-$$"
REPO="$WORK/repo"
mkdir -p "$REPO/docs/requirements/drafts"
export MUXCODE_LIFECYCLE_LOG_DIR="$WORK/lifecycle"
: > "$WORK/empty-config"
export MUXCODE_CONFIG="$WORK/empty-config"
export MUXCODE_TMP_CLEANUP_THRESHOLD=0 MUXCODE_BRANCH_TIME_DISABLE=1 MUXCODE_DEDUP_WINDOW=0
export MUXCODE_FORCE_RESPOND_DISABLE=1 MUXCODE_EDIT_CLI=claude MUXCODE_PLAN_CLI=claude MUXCODE_BUILD_CLI=claude
export MUXCODE_SESSION_REPO_DIR="$REPO"

DPID=""
. "$(dirname "${BASH_SOURCE[0]}")/lib/private-tmux.sh"
private_tmux_init "$WORK/tmux"
cleanup() {
  [ -n "$DPID" ] && kill "$DPID" 2>/dev/null
  private_tmux_kill
  rm -f "/tmp/muxcode-analyze-$BUS_SESSION.trigger" "/tmp/muxcode-preview-$BUS_SESSION.tmp"
  rm -rf "$BD" "$WORK"
}
trap cleanup EXIT

LIFELOG="$MUXCODE_LIFECYCLE_LOG_DIR/${BUS_SESSION}.log"
tmux new-session -d -s "$BUS_SESSION" -n edit -c "$WORK" "cat > /dev/null"
tmux split-window -h -t "$BUS_SESSION:edit" -c "$WORK" "cat > /dev/null"
"$MUX" init >/dev/null 2>&1

# --- Helpers ---------------------------------------------------------------
vs_count() { local c; c="$(grep -c '"action":"verify-spec"' "$BD/log.jsonl" 2>/dev/null)"; echo "${c:-0}"; }
wait_vs_count() {
  local i
  for i in $(seq 1 30); do [ "$(vs_count)" -ge "$1" ] && return 0; sleep 0.5; done
  return 1
}
# last_changed_files — the "Changed files (repo-relative): …" list of the newest verify-spec.
last_changed_files() {
  grep '"action":"verify-spec"' "$BD/log.jsonl" 2>/dev/null | tail -1 | jq -r '.payload' \
    | sed -n 's/.*Changed files (repo-relative): \(.*\)\. Read the spec.*/\1/p'
}
wf() { jq -r ".$1 // \"\"" "$BD/workflow-state.json" 2>/dev/null; }
wf_snapshot() { jq -c '{state, updated, last_files}' "$BD/workflow-state.json" 2>/dev/null; }
regressed_from_reviewed() { [ "$(wf state)" = editing ] && [ "$(wf prev_state)" = reviewed ]; }
route_rows() { local c; c="$(grep -c '"event":"trigger-route"' "$LIFELOG" 2>/dev/null)"; echo "${c:-0}"; }
routed() { grep '"event":"trigger-route"' "$LIFELOG" 2>/dev/null | grep -qF "$1"; }
wait_routed() {
  local i
  for i in $(seq 1 30); do routed "$1" && return 0; sleep 0.5; done
  return 1
}
# write ROLE REL TEXT — write REL in the repo and report it through the real
# PostToolUse analyze hook as ROLE's window.
write() {
  printf '%s\n' "$3" > "$REPO/$2"
  jq -nc --arg p "$REPO/$2" '{hook_event_name:"PostToolUse",tool_name:"Write",tool_input:{file_path:$p,content:"x"}}' \
    | AGENT_ROLE="$1" BUS_ROLE="$1" "$MUX" hook analyze >/dev/null 2>&1
}
# review N — a review completion; waits for the Nth verify-spec, plan drains as a live agent does.
review() {
  AGENT_ROLE=review "$MUX" send edit review-result "Review complete: pass $1. EXIT=0" --type response >/dev/null 2>&1
  wait_vs_count "$1"
  local rc=$?
  sleep 1
  AGENT_ROLE=plan "$MUX" inbox >/dev/null 2>&1
  return $rc
}

# --- Fixtures --------------------------------------------------------------
SPEC="docs/requirements/drafts/verify-files-fixture.md"
printf '# Verify Files Fixture\n\n- [ ] an open item\n\n## Status\n\nIn Progress\n' > "$REPO/$SPEC"
for f in a b c d; do printf 'package x\n' > "$REPO/$f.go"; done
git -C "$REPO" init -q && git -C "$REPO" add -A \
  && git -C "$REPO" -c user.email=t@t -c user.name=t commit -q -m base \
  && ok "fixture repo committed with four code files and the spec" \
  || bad "fixture repo failed to commit"
(cd "$REPO" && "$MUX" spec set "$SPEC" >/dev/null 2>&1) && ok "active spec set" || bad "spec set failed"

"$MUX" watch "$BUS_SESSION" --poll 1 --debounce 2 >"$WORK/daemon.log" 2>&1 &
DPID=$!
sleep 1
kill -0 "$DPID" 2>/dev/null && ok "scratch daemon running (pid $DPID)" || bad "daemon exited: $(tail -3 "$WORK/daemon.log")"

# --- A. three code files over two batches, a spec write between them --------
echo "-- A: two debounce batches with plan's spec write between them"
write build a.go 'package x // a'
write build b.go 'package x // b'
check "A: batch 1 routed a.go and b.go" wait_routed "$REPO/b.go"
ROUTES=$(route_rows); SNAP=$(wf_snapshot)
write plan "$SPEC" '# Verify Files Fixture — plan ticked something'
sleep 4
check "A: plan's spec write routed nothing" [ "$(route_rows)" -eq "$ROUTES" ]
check "A: plan's spec write left the workflow untouched ($SNAP)" [ "$(wf_snapshot)" = "$SNAP" ]
write build c.go 'package x // c'
check "A: batch 2 routed c.go" wait_routed "$REPO/c.go"
check "A: the batches were separate routes" [ "$(route_rows)" -ge 2 ]
check "A: review 1 fired a verify-spec" review 1
FILES=$(last_changed_files)
check "A: it names all three code files (got: $FILES)" [ "$FILES" = "a.go, b.go, c.go" ]
case $FILES in *docs/* | *"$SPEC"*) bad "A: it names a docs path" ;; *) ok "A: it never names the spec or any docs path" ;; esac

# --- B. a second review names only what moved --------------------------------
echo "-- B: one more code file, a second review"
write build d.go 'package x // d'
check "B: d.go routed" wait_routed "$REPO/d.go"
check "B: review 2 fired a verify-spec" review 2
FILES=$(last_changed_files)
check "B: it names only d.go (got: $FILES)" [ "$FILES" = "d.go" ]
AGENT_ROLE=review "$MUX" send edit review-result "Review complete: pass 3, nothing new. EXIT=0" --type response >/dev/null 2>&1
sleep 4
check "D: a completion with nothing moved since review 2 fired none" [ "$(vs_count)" -eq 2 ]

# --- C. docs write after review vs an edit-window code write -----------------
echo "-- C: the workflow after a review"
check "C: the review left the workflow at reviewed" [ "$(wf state)" = reviewed ]
write plan "$SPEC" '# Verify Files Fixture — plan recorded time'
sleep 3
check "C: plan's verification write kept it at reviewed" [ "$(wf state)" = reviewed ]
check "C: e.go has not been routed before the edit-window write" [ "$(grep '"event":"trigger-route"' "$LIFELOG" 2>/dev/null | grep -cF "$REPO/e.go")" -eq 0 ]
write edit e.go 'package x // e, a path no earlier section wrote'
check "C: an edit-window code write regressed reviewed -> editing" regressed_from_reviewed
check "C: and routed to analyze" wait_routed "$REPO/e.go"

# --- D. one verify-spec per completion ---------------------------------------
echo "-- D: MUX-007 gate"
sleep 2
check "D: exactly two verify-spec for the two completions that moved something" [ "$(vs_count)" -eq 2 ]

# --- Coverage floor ----------------------------------------------------------
echo ""
echo "=== $pass passed, $fail failed (floor $EXPECTED) ==="
if [ "$pass" -ne "$EXPECTED" ]; then
  echo "  ${RED}FAIL${NC}  coverage floor: expected exactly $EXPECTED passes, got $pass — a section was skipped or double-counted"
  exit 1
fi
[ "$fail" -eq 0 ]
