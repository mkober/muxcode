#!/usr/bin/env bash
# Integration test for MUX-166: 50-spec-to-pr measures the branch against a
# fetched origin/main before every implement and parks a stale branch at
# sync-gate with the numbers, instead of building on a stale base.
#
# Hermetic: muxcode built from the tree (MUXCODE_BRANCH_FRESHNESS_BIN
# overrides), scratch HOME, git config, lifecycle log and buses, a private tmux
# server under the work dir, one scratch daemon per scenario. Each scenario has
# its own bare origin, an upstream clone that advances main, and a work clone
# on the spec's branch. The implement worker's CLI is a stub idle prompt; the
# commit agent is played by this script running the real `muxcode sync-main`.
#
#   1  the incident shape: main deletes a file the phase writes → the run parks
#      at sync-gate naming it deleted, a branch-freshness row, no implement
#   2  negative control: a fresh branch → implement dispatched, no gate, a row
#      recording 0 behind
#   3  threshold: 4 behind without overlap → implement; 5 behind → sync-gate
#   4  approving sync-gate → the commit agent's sync rebases → refresh passes →
#      implement dispatched
#   5  MUXCODE_FRESHNESS_MAX_BEHIND=100 and 1 behind with overlap still parks
#   6  a conflicting sync → sync-conflict with the conflict named and a clean
#      tree: no rebase in progress, no unmerged paths
#   7  Phase 5's widened gate: 55, 60, 80 and 90 validate, and a copy of each
#      with its freshness check spliced out fails naming the work it guarded
#   8  55-defect-backlog-loop on a main 5 behind parks at sync-gate with no
#      implement; the approved sync fast-forwards main carrying the run's
#      uncommitted work and implement follows; negative control — a fresh main
#      dispatches implement with no gate
#   9  90-ci-fix with no active spec: a PR branch 5 behind parks before fix,
#      its row measuring the files the branch changes; negative control — a
#      fresh branch reaches fix with a 0 behind row
#
# Usage: bash scripts/test-branch-freshness-gate.sh
set -uo pipefail
export MUXCODE_CONSUMER_GUARD=0 # plan and commit are played by reading their inboxes (MUX-145)

PASS=0
FAIL=0
EXPECTED_PASS=62

for tool in jq go tmux git; do
  command -v "$tool" >/dev/null 2>&1 || { echo "SKIP: $tool is required"; exit 2; }
done

REPO_ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
WORK=$(mktemp -d /tmp/branch-freshness-XXXXXX)
. "$REPO_ROOT/scripts/lib/private-tmux.sh"
private_tmux_init "$WORK/tmux"
DPIDS=""
cleanup() {
  local p
  for p in $DPIDS; do kill "$p" 2>/dev/null; done
  private_tmux_kill
  cd / 2>/dev/null
  rm -rf "$WORK"
}
trap cleanup EXIT
export GOCACHE="$(go env GOCACHE)" GOMODCACHE="$(go env GOMODCACHE)" GOTOOLCHAIN=local

ok()   { PASS=$((PASS + 1)); echo "  ok: $1"; }
fail() { FAIL=$((FAIL + 1)); echo "  FAIL: $1"; }

# --- Build -----------------------------------------------------------------
mkdir -p "$WORK/bin"
if [ -n "${MUXCODE_BRANCH_FRESHNESS_BIN:-}" ]; then
  ln -sf "$(cd "$(dirname "$MUXCODE_BRANCH_FRESHNESS_BIN")" && pwd)/$(basename "$MUXCODE_BRANCH_FRESHNESS_BIN")" "$WORK/bin/muxcode"
else
  (cd "$REPO_ROOT/tools/muxcode" && go build -buildvcs=false -o "$WORK/bin/muxcode" .) \
    || { echo "SKIP: go build muxcode failed"; exit 2; }
fi
MUX="$WORK/bin/muxcode"
export PATH="$WORK/bin:$PATH"

# --- Isolation -------------------------------------------------------------
export HOME="$WORK/home"
mkdir -p "$HOME/.config/muxcode"
# Gate releases need an authorized actor that did not create the run (MUX-144).
echo "MUXCODE_GATE_AUTHORITY_ROLES=test-approver" >"$HOME/.config/muxcode/config"
export XDG_STATE_HOME="$HOME/.local/state" MUXCODE_BUS_BASE="$WORK/bus"
unset MUXCODE_BUS_DIR MUXCODE_FRESHNESS_MAX_BEHIND MUXCODE_FRESHNESS_MAX_FETCH_AGE 2>/dev/null || true
export MUXCODE_LIFECYCLE_LOG_DIR="$WORK/lifecycle"
: >"$WORK/empty-config"
export MUXCODE_CONFIG="$WORK/empty-config"
export MUXCODE_TMP_CLEANUP_THRESHOLD=0 MUXCODE_BRANCH_TIME_DISABLE=1 MUXCODE_DEDUP_WINDOW=0
cat >"$WORK/gitconfig" <<'EOF'
[user]
	name = t
	email = t@t
[commit]
	gpgsign = false
[init]
	defaultBranch = main
EOF
export GIT_CONFIG_GLOBAL="$WORK/gitconfig" GIT_CONFIG_NOSYSTEM=1
cat >"$WORK/stub-agent" <<'STUB'
#!/usr/bin/env bash
while :; do
  printf '\n❯ '
  read -r _ || sleep 5
done
STUB
chmod +x "$WORK/stub-agent"

SPEC_REL="docs/requirements/drafts/MUX-900-freshness-demo.md"
BRANCH="MUX-900-freshness-demo"

# make_repos NAME [main] — a bare origin, an upstream clone U and a work clone
# W on the spec's branch, one commit ahead of main; with "main", W stays on
# main at origin/main, as 55-defect-backlog-loop works. The spec names
# src/query_builder.py in its Key files table and its Phase 1 item; the
# backlog index gives 55 its loop cap.
make_repos() {
  local d="$WORK/$1"
  ORIGIN="$d/origin.git" U="$d/up" W="$d/work"
  git init -q --bare "$ORIGIN"
  git clone -q "$ORIGIN" "$U" 2>/dev/null
  mkdir -p "$U/src" "$U/$(dirname "$SPEC_REL")"
  cat >"$U/$SPEC_REL" <<'EOF'
# MUX-900 Freshness Demo

### Key files

| File | Purpose |
|------|---------|
| `src/query_builder.py` | the builder the phase writes |

## Implementation

### Phase 1: Build

- [ ] Write the exclusion builder into `src/query_builder.py`

## Status

In Progress
EOF
  echo "base" >"$U/src/query_builder.py"
  echo "base" >"$U/README.md"
  mkdir -p "$U/docs/requirements/backlog"
  printf '### Defects — prioritized\n\n| # | ID |\n|---|----|\n| 1 | MUX-900 |\n' >"$U/docs/requirements/backlog/backlog.md"
  git -C "$U" add -A && git -C "$U" commit -q -m base && git -C "$U" push -q origin main
  git clone -q "$ORIGIN" "$W"
  [ "${2:-}" = main ] && return 0
  git -C "$W" checkout -q -b "$BRANCH"
  echo "branch work" >"$W/feature.txt"
  git -C "$W" add -A && git -C "$W" commit -q -m "phase 0"
}

# advance N [FILE] — N commits on origin/main, each to FILE or a new unrelated file.
ADV=0
advance() {
  local i f
  for i in $(seq 1 "$1"); do
    ADV=$((ADV + 1))
    f="${2:-other/unrelated-$ADV.txt}"
    mkdir -p "$U/$(dirname "$f")"
    echo "upstream $ADV" >"$U/$f"
    git -C "$U" add -A && git -C "$U" commit -q -m "upstream $ADV"
  done
  git -C "$U" push -q origin main
}

# start_scenario NAME [ENV...] — a session and daemon whose repo is W; sets
# BUS_SESSION, BD, LOG and RID (the started run of TPL, default
# 50-spec-to-pr). NOSPEC=1 leaves no active spec.
start_scenario() {
  local name="$1"
  shift
  export BUS_SESSION="fresh-$name-$$" MUXCODE_SESSION_REPO_DIR="$W"
  BD="$("$MUX" bus-dir "$BUS_SESSION")"
  LOG="$MUXCODE_LIFECYCLE_LOG_DIR/$BUS_SESSION.log"
  tmux new-session -d -s "$BUS_SESSION" -n edit -x 120 -y 30 -c "$W"
  local kv
  for kv in "PATH=$PATH" "BUS_SESSION=$BUS_SESSION" "MUXCODE_CONFIG=$MUXCODE_CONFIG" \
    "MUXCODE_EDIT_CLI=$WORK/stub-agent" "MUXCODE_SESSION_REPO_DIR=$W" "$@"; do
    tmux set-environment -t "$BUS_SESSION" "${kv%%=*}" "${kv#*=}"
  done
  (cd "$W" && "$MUX" init >/dev/null 2>&1)
  [ "${NOSPEC:-}" = 1 ] || (cd "$W" && AGENT_ROLE=plan "$MUX" spec set "$SPEC_REL" >/dev/null 2>&1)
  (cd "$W" && exec env "$@" "$MUX" watch "$BUS_SESSION" --poll 1 >"$WORK/daemon-$name.log" 2>&1) &
  DPIDS="$DPIDS $!"
  RID="$(cd "$W" && AGENT_ROLE=auto "$MUX" graph run "${TPL:-50-spec-to-pr}" "MUX-900 Freshness Demo" 2>&1 |
    grep -o 'Started run [^ ]*' | awk '{print $3}')"
}

stop_scenario() {
  [ -n "${RID:-}" ] && "$MUX" graph cancel "$RID" >/dev/null 2>&1
  local p
  for p in $DPIDS; do kill "$p" 2>/dev/null; done
  DPIDS=""
  tmux kill-session -t "$BUS_SESSION" 2>/dev/null
}

node_state() { jq -r '.state // ""' "$BD/graphs/$1/nodes/$2.json" 2>/dev/null; }
node_is() { [ "$(node_state "$1" "$2")" = "$3" ]; }
wait_until() {
  local n=$(($1 * 4)) i
  shift
  for ((i = 0; i < n; i++)); do "$@" && return 0; sleep 0.25; done
  return 1
}
node_started() { case "$(node_state "$RID" "$1")" in running | done | failed) return 0 ;; esac; return 1; }
implement_started() { node_started implement; }
gate_text() { cat "$BD/graphs/$RID/approvals/$1.pending" 2>/dev/null; }
freshness_rows() { grep '"event":"branch-freshness"' "$LOG" 2>/dev/null | grep -F "$RID"; }
approve() { AGENT_ROLE=test-approver "$MUX" graph approve "$RID" "$1" >/dev/null 2>&1; }

# play_commit_sync — the commit agent: read the rebase request, run the real
# sync in the work clone, reply with its output. Sets SYNC_OUT.
play_commit_sync() {
  local out rid
  for _ in $(seq 1 60); do
    out="$(AGENT_ROLE=commit "$MUX" inbox --peek 2>/dev/null)"
    rid="$(printf '%s\n' "$out" | grep -A20 'Action: rebase' | grep -o -- '--reply-to [A-Za-z0-9-]*' | head -1 | awk '{print $2}')"
    [ -n "$rid" ] && break
    sleep 0.5
  done
  [ -n "$rid" ] || return 1
  AGENT_ROLE=commit "$MUX" inbox >/dev/null 2>&1
  SYNC_OUT="$(cd "$W" && AGENT_ROLE=commit "$MUX" sync-main 2>&1)"
  AGENT_ROLE=commit "$MUX" send edit response "$(printf '%s' "$SYNC_OUT" | tr '\n' ' ')" --type response --reply-to "$rid" >/dev/null 2>&1
}

# answer_request ROLE ACTION REPLY — play ROLE: wait for its ACTION request,
# consume it and answer with REPLY and a passing verdict.
answer_request() {
  local role="$1" action="$2" reply="$3" out rid=""
  for _ in $(seq 1 60); do
    out="$(AGENT_ROLE=$role "$MUX" inbox --peek 2>/dev/null)"
    rid="$(printf '%s\n' "$out" | grep -A20 "Action: $action" | grep -o -- '--reply-to [A-Za-z0-9-]*' | head -1 | awk '{print $2}')"
    [ -n "$rid" ] && break
    sleep 0.5
  done
  [ -n "$rid" ] || return 1
  AGENT_ROLE=$role "$MUX" inbox >/dev/null 2>&1
  AGENT_ROLE=$role "$MUX" send edit "$action" "$reply EXIT=0" --type response --reply-to "$rid" >/dev/null 2>&1
}

# --- 0. Validation ---------------------------------------------------------
echo "-- 0: the builtin validates with its freshness check"
"$MUX" graph validate 50-spec-to-pr >"$WORK/validate.out" 2>&1 && ok "0: 50-spec-to-pr validates" \
  || fail "0: 50-spec-to-pr failed validation: $(head -3 "$WORK/validate.out")"

# --- 1. The incident shape -------------------------------------------------
echo "-- 1: main deleted a file the phase writes"
make_repos incident
git -C "$U" rm -q src/query_builder.py && git -C "$U" commit -q -m "move the builder" && git -C "$U" push -q origin main
advance 2
start_scenario incident
[ -n "$RID" ] && ok "1: the run started" || fail "1: the run did not start"
wait_until 30 node_is "$RID" sync-gate waiting && ok "1: the run parks at sync-gate" \
  || fail "1: sync-gate is $(node_state "$RID" sync-gate)"
gate_text sync-gate | grep -q 'src/query_builder.py (deleted)' && ok "1: the gate names the deleted file" \
  || fail "1: gate text: $(gate_text sync-gate)"
gate_text sync-gate | grep -q '3 behind' && gate_text sync-gate | grep -q '[0-9a-f]\{7\} move the builder' \
  && gate_text sync-gate | grep -q 'fetched [0-9.ms]* ago' \
  && ok "1: the gate carries the behind count, the merge that deleted it and the fetch age" \
  || fail "1: gate text lacks count, merge or fetch age: $(gate_text sync-gate)"
freshness_rows | grep -q '3 behind.*STALE' && ok "1: a branch-freshness row records 3 behind, stale" \
  || fail "1: no stale branch-freshness row for $RID"
node_is "$RID" implement pending && ok "1: implement was never dispatched" \
  || fail "1: implement is $(node_state "$RID" implement)"
stop_scenario

# --- 2. Negative control: fresh --------------------------------------------
echo "-- 2: a fresh branch"
make_repos fresh
start_scenario fresh
wait_until 30 implement_started && ok "2: implement dispatched" || fail "2: implement is $(node_state "$RID" implement)"
freshness_rows | grep -q '0 behind.*fresh' && ok "2: the row records 0 behind, fresh" || fail "2: no fresh row for $RID"
node_is "$RID" sync-gate pending && ok "2: no gate" || fail "2: sync-gate is $(node_state "$RID" sync-gate)"
stop_scenario

# --- 3. Threshold ----------------------------------------------------------
echo "-- 3: 4 behind proceeds, 5 behind gates"
make_repos four
advance 4
start_scenario four
wait_until 30 implement_started && ok "3: 4 behind without overlap → implement" || fail "3: 4 behind: implement $(node_state "$RID" implement)"
freshness_rows | grep -q '4 behind.*fresh' && ok "3: the row records 4 behind" || fail "3: no 4-behind row"
node_is "$RID" sync-gate pending && ok "3: 4 behind: no gate" || fail "3: 4 behind: sync-gate $(node_state "$RID" sync-gate)"
stop_scenario

make_repos five
advance 5
start_scenario five
wait_until 30 node_is "$RID" sync-gate waiting && ok "3: 5 behind without overlap → sync-gate" \
  || fail "3: 5 behind: sync-gate $(node_state "$RID" sync-gate)"
gate_text sync-gate | grep -q '5 behind ≥ 5' && ok "3: the gate states 5 behind ≥ 5" || fail "3: gate text: $(gate_text sync-gate)"

# --- 4. Approve → sync → refresh → implement -------------------------------
echo "-- 4: the approved sync rebases and the run proceeds"
approve sync-gate && ok "4: sync-gate approved" || fail "4: the approval was refused"
play_commit_sync && ok "4: the commit agent received the rebase request" || fail "4: no rebase request reached commit"
printf '%s' "${SYNC_OUT:-}" | grep -q 'EXIT=0' && ok "4: sync-main synced (EXIT=0)" || fail "4: sync output: ${SYNC_OUT:-}"
wait_until 30 implement_started && ok "4: refresh passed and implement was dispatched" \
  || fail "4: implement is $(node_state "$RID" implement); refresh $(node_state "$RID" refresh)"
[ "$(git -C "$W" rev-list --count HEAD..origin/main)" = 0 ] && ok "4: the work branch is 0 behind origin/main" \
  || fail "4: still $(git -C "$W" rev-list --count HEAD..origin/main) behind"
stop_scenario

# --- 5. Overlap gates whatever the threshold --------------------------------
echo "-- 5: MUXCODE_FRESHNESS_MAX_BEHIND=100, 1 behind with overlap"
make_repos override
advance 1 src/query_builder.py
start_scenario override MUXCODE_FRESHNESS_MAX_BEHIND=100
wait_until 30 node_is "$RID" sync-gate waiting && ok "5: 1 behind with overlap still parks" \
  || fail "5: sync-gate $(node_state "$RID" sync-gate)"
gate_text sync-gate | grep -q 'gate at 100' && ok "5: the override reached the measurement (gate at 100)" \
  || fail "5: gate text: $(gate_text sync-gate)"
stop_scenario

# --- 6. A conflicting sync parks with a clean tree --------------------------
echo "-- 6: a conflicting sync"
make_repos conflict
echo "branch builder" >"$W/src/query_builder.py"
git -C "$W" commit -q -am "phase 1 writes the builder"
advance 1 src/query_builder.py
start_scenario conflict
wait_until 30 node_is "$RID" sync-gate waiting && ok "6: the overlap parks at sync-gate" \
  || fail "6: sync-gate $(node_state "$RID" sync-gate)"
approve sync-gate && ok "6: sync-gate approved" || fail "6: the approval was refused"
play_commit_sync && printf '%s' "${SYNC_OUT:-}" | grep -q 'EXIT=1' \
  && ok "6: sync-main reported the conflict (EXIT=1)" || fail "6: sync output: ${SYNC_OUT:-}"
wait_until 30 node_is "$RID" sync-conflict waiting && ok "6: the run parks at sync-conflict" \
  || fail "6: sync-conflict $(node_state "$RID" sync-conflict); sync $(node_state "$RID" sync)"
gate_text sync-conflict | grep -q 'src/query_builder.py' && ok "6: the conflict gate names the file" \
  || fail "6: gate text: $(gate_text sync-conflict)"
rb="$(git -C "$W" rev-parse --git-path rebase-merge)"
case "$rb" in /*) ;; *) rb="$W/$rb" ;; esac
[ ! -e "$rb" ] && [ -z "$(git -C "$W" diff --name-only --diff-filter=U)" ] \
  && ok "6: no rebase in progress, no unmerged paths" || fail "6: the tree was left mid-rebase or unmerged"
stop_scenario

# --- 7. The widened rule ---------------------------------------------------
echo "-- 7: every widened builtin validates; without its check it does not"
for spec in 55-defect-backlog-loop:refresh 60-integration-suite:refresh 80-pr-review-fix:refresh 90-ci-fix:refresh; do
  tpl="${spec%%:*}" check="${spec#*:}"
  "$MUX" graph validate "$tpl" >"$WORK/validate-$tpl.out" 2>&1 && ok "7: $tpl validates" \
    || fail "7: $tpl failed validation: $(head -3 "$WORK/validate-$tpl.out")"
  "$MUX" graph export "$tpl" 2>/dev/null >"$WORK/$tpl.json"
  work="$(jq -r --arg c "$check" '[.edges[] | select(.from == $c and ((.outcome // "success") == "success")) | .to][0] // empty' "$WORK/$tpl.json")"
  jq --arg c "$check" --arg w "$work" \
    '.edges |= [.[] | select(.from != $c) | (if .to == $c then .to = $w else . end) | del(.resets_iterations)]' \
    "$WORK/$tpl.json" >"$WORK/$tpl-bypassed.json"
  if [ -n "$work" ] && ! "$MUX" graph validate "$WORK/$tpl-bypassed.json" >"$WORK/bypassed-$tpl.out" 2>&1 &&
    grep -q "\"$work\".*freshness" "$WORK/bypassed-$tpl.out"; then
    ok "7: $tpl without $check fails naming \"$work\""
  else
    fail "7: $tpl without $check: $(head -5 "$WORK/bypassed-$tpl.out" 2>/dev/null)"
  fi
done

# --- 8. 55-defect-backlog-loop measures main ---------------------------------
echo "-- 8: 55 on a stale main"
make_repos loop55 main
echo "the run's uncommitted phase work" >"$W/src/query_builder.py"
advance 5
TPL=55-defect-backlog-loop start_scenario loop55
[ -n "$RID" ] && ok "8: the 55 run started" || fail "8: the 55 run did not start"
answer_request commit pr-read "Branch main, tree dirty. ON-MAIN" && ok "8: preflight answered ON-MAIN" || fail "8: no preflight request"
answer_request plan defect-select "DEFECT-ACTIVATED MUX-900 Freshness Demo $SPEC_REL" && ok "8: select activated the defect" \
  || fail "8: no defect-select request"
wait_until 30 node_is "$RID" sync-gate waiting && ok "8: a main 5 behind parks at sync-gate" \
  || fail "8: sync-gate $(node_state "$RID" sync-gate); refresh $(node_state "$RID" refresh)"
freshness_rows | grep -q '5 behind.*STALE' && ok "8: a stale branch-freshness row records 5 behind" \
  || fail "8: no stale row for $RID"
node_is "$RID" implement pending && ok "8: implement was never dispatched" || fail "8: implement is $(node_state "$RID" implement)"
approve sync-gate && ok "8: sync-gate approved" || fail "8: the approval was refused"
play_commit_sync && printf '%s' "${SYNC_OUT:-}" | grep -q 'EXIT=0' && ok "8: sync-main on main fast-forwarded (EXIT=0)" \
  || fail "8: sync output: ${SYNC_OUT:-}"
wait_until 30 implement_started && ok "8: refresh passed and implement was dispatched" \
  || fail "8: implement is $(node_state "$RID" implement); refresh $(node_state "$RID" refresh)"
[ "$(git -C "$W" rev-parse HEAD)" = "$(git -C "$W" rev-parse origin/main)" ] &&
  [ "$(cat "$W/src/query_builder.py")" = "the run's uncommitted phase work" ] &&
  ok "8: main is origin/main and the uncommitted work was carried" || fail "8: main or the uncommitted work moved wrongly"
stop_scenario

make_repos loop55fresh main
TPL=55-defect-backlog-loop start_scenario loop55fresh
answer_request commit pr-read "Branch main, tree clean. ON-MAIN" &&
  answer_request plan defect-select "DEFECT-ACTIVATED MUX-900 Freshness Demo $SPEC_REL" &&
  ok "8: fresh main: preflight and select answered" || fail "8: fresh main: a request never arrived"
wait_until 30 implement_started && ok "8: a fresh main dispatches implement" || fail "8: fresh main: implement $(node_state "$RID" implement)"
freshness_rows | grep -q '0 behind.*fresh' && ok "8: fresh main: the row records 0 behind" || fail "8: fresh main: no fresh row"
node_is "$RID" sync-gate pending && ok "8: fresh main: no gate" || fail "8: fresh main: sync-gate $(node_state "$RID" sync-gate)"
stop_scenario

# --- 9. 90-ci-fix measures the PR branch, no spec ------------------------------
echo "-- 9: 90 on a stale PR branch with no active spec"
make_repos ci90
advance 5
TPL=90-ci-fix NOSPEC=1 start_scenario ci90
[ -n "$RID" ] && ok "9: the 90 run started" || fail "9: the 90 run did not start"
answer_request commit pr-read "PR-CONFIRMED #1 https://example.test/pull/1" &&
  answer_request commit pr-read "check unit failed at step go test: TestX" &&
  ok "9: find-pr and read-ci answered" || fail "9: a PR read never arrived"
wait_until 30 node_is "$RID" fix-gate waiting && ok "9: failing CI parks at fix-gate" || fail "9: fix-gate $(node_state "$RID" fix-gate)"
approve fix-gate && ok "9: fix-gate approved" || fail "9: the approval was refused"
wait_until 30 node_is "$RID" sync-gate waiting && ok "9: a branch 5 behind parks at sync-gate before fix" \
  || fail "9: sync-gate $(node_state "$RID" sync-gate); refresh $(node_state "$RID" refresh)"
freshness_rows | grep -q '5 behind.*files this branch changes.*STALE' \
  && ok "9: the stale row measures the files the branch changes" || fail "9: row: $(freshness_rows | tail -1)"
node_is "$RID" fix pending && ok "9: fix was never dispatched" || fail "9: fix is $(node_state "$RID" fix)"
stop_scenario

make_repos ci90fresh
TPL=90-ci-fix NOSPEC=1 start_scenario ci90fresh
answer_request commit pr-read "PR-CONFIRMED #1 https://example.test/pull/1" &&
  answer_request commit pr-read "check unit failed at step go test: TestX" &&
  ok "9: fresh branch: find-pr and read-ci answered" || fail "9: fresh branch: a PR read never arrived"
wait_until 30 node_is "$RID" fix-gate waiting && approve fix-gate && ok "9: fresh branch: fix-gate approved" \
  || fail "9: fresh branch: fix-gate $(node_state "$RID" fix-gate)"
wait_until 30 node_started fix && ok "9: a fresh branch reaches fix" || fail "9: fresh branch: fix $(node_state "$RID" fix)"
freshness_rows | grep -q '0 behind.*fresh' && ok "9: fresh branch: the row records 0 behind" || fail "9: fresh branch: no fresh row"
node_is "$RID" sync-gate pending && ok "9: fresh branch: no gate" || fail "9: fresh branch: sync-gate $(node_state "$RID" sync-gate)"
stop_scenario

# ── Coverage floor ─────────────────────────────────────────────────
echo
echo "=== results: $PASS passed, $FAIL failed (floor $EXPECTED_PASS) ==="
if [ "$PASS" -ne "$EXPECTED_PASS" ]; then
  echo "  FAIL  coverage floor: expected exactly $EXPECTED_PASS passes, got $PASS — a section was skipped or double-counted"
  exit 1
fi
[ "$FAIL" -eq 0 ] || exit 1
exit 0
