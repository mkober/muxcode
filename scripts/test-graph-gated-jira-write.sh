#!/usr/bin/env bash
# Integration test for MUX-165: a tracker write behind an approved wait_human
# gate is executed by plan without a relay or a retyped request, while a
# tracker write from any other agent is still declined.
#
# Hermetic: muxcode built from the tree (MUXCODE_GRAPH_GATED_JIRA_BIN overrides
# the binary under test), scratch HOME, state, lifecycle log and bus, scratch
# daemons on a private tmux server. `muxcode atlassian` and `gh` are shimmed on
# PATH and only record their calls — no network. plan is played by the script:
# it reads plan's inbox and decides each tracker write by the consent paths its
# definition (agents/planner.md) states, so the definition's words, not the
# script, decide whether a gate-approved dispatch counts.
#
# Gate approvals are made as the user through `tmux run-shell`, whose ancestry
# holds no agent runtime (BusActorVerified walks it; see test-cancel-provenance).
#
#   0  the definition states consent path 3 (a gate-approved graph dispatch,
#      named by its Origin line) and the refusal of any other agent's write
#   1  wait_human → jira-write approved as the user: plan's inbox holds the
#      dispatch with its `Origin: graph run … · node …` line, the shim records
#      one write, the node is done/success and the run completes
#   2  an unapproved gate (retry --from the write node): the dispatch never
#      reaches plan, tracker-write-refused names run and node, no write
#   3  a jira-write sent by build is delivered, declined by the rule naming
#      build, and records no write
#   4  the builtin 10-story-to-spec validates and, with both gates approved,
#      its jira-update and issue-update nodes reach plan, are executed and the
#      run completes — the 2026-09-09 run's shape
#   5  control: the same flow as 1 against a copy of the definition with path
#      3 removed — plan declines, the node fails, the run fails, no write: the
#      2026-09-09 outcome
#
# Usage: bash scripts/test-graph-gated-jira-write.sh
set -uo pipefail
export MUXCODE_CONSUMER_GUARD=0 # plan is played by reading its inbox, which no window or listener shows (MUX-145)

PASS=0
FAIL=0
EXPECTED_PASS=33

command -v jq >/dev/null 2>&1 || { echo "SKIP: jq is required"; exit 2; }
command -v go >/dev/null 2>&1 || { echo "SKIP: go is required"; exit 2; }
command -v tmux >/dev/null 2>&1 || { echo "SKIP: tmux is required"; exit 2; }

REPO=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
DEF="$REPO/agents/planner.md"
WORK=$(mktemp -d /tmp/graph-gated-jira-XXXXXX)
. "$REPO/scripts/lib/private-tmux.sh"
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
mkdir -p "$WORK/bin" "$WORK/shim"
if [ -n "${MUXCODE_GRAPH_GATED_JIRA_BIN:-}" ]; then
  MUX="$(cd "$(dirname "$MUXCODE_GRAPH_GATED_JIRA_BIN")" && pwd)/$(basename "$MUXCODE_GRAPH_GATED_JIRA_BIN")"
else
  (cd "$REPO/tools/muxcode" && go build -buildvcs=false -o "$WORK/bin/muxcode" .) \
    || { echo "SKIP: go build muxcode failed"; exit 2; }
  MUX="$WORK/bin/muxcode"
fi

# Shims: `muxcode atlassian …` and `gh …` append one line to $CALLS; every other
# muxcode subcommand runs the binary under test.
CALLS="$WORK/tracker.calls"
: >"$CALLS"
cat >"$WORK/shim/muxcode" <<EOF
#!/usr/bin/env bash
if [ "\${1:-}" = atlassian ]; then echo "atlassian \$*" >>"$CALLS"; exit 0; fi
exec "$MUX" "\$@"
EOF
cat >"$WORK/shim/gh" <<EOF
#!/usr/bin/env bash
echo "gh \$*" >>"$CALLS"
EOF
chmod +x "$WORK/shim/muxcode" "$WORK/shim/gh"
writes() { grep -c . "$CALLS" 2>/dev/null || true; }

# --- Isolation -------------------------------------------------------------
# An inherited absolute MUXCODE_BUS_BASE outranks XDG_STATE_HOME (busDirBase),
# so it is pinned under WORK; OUTER_BUS_BASE is checked for leaks at the end.
OUTER_BUS_BASE="$(env -u MUXCODE_BUS_DIR "$MUX" bus-dir probe-$$ 2>/dev/null | xargs dirname 2>/dev/null)"
cd "$WORK" || exit 1
export HOME="$WORK/home"
mkdir -p "$HOME/.config/muxcode"
export XDG_STATE_HOME="$HOME/.local/state"
export MUXCODE_BUS_BASE="$WORK/bus"
unset MUXCODE_BUS_DIR
export MUXCODE_LIFECYCLE_LOG_DIR="$WORK/lifecycle"
: >"$WORK/empty-config"
export MUXCODE_CONFIG="$WORK/empty-config"
export MUXCODE_TMP_CLEANUP_THRESHOLD=0 MUXCODE_BRANCH_TIME_DISABLE=1 MUXCODE_DEDUP_WINDOW=0
export MUXCODE_SESSION_REPO_DIR="$WORK"
unset MUXCODE_ATLASSIAN_AUTHORITY_ROLES MUXCODE_COMMIT_AUTHORITY_ROLES MUXCODE_GATE_AUTHORITY_ROLES 2>/dev/null || true

cat >"$WORK/gated.json" <<'EOF'
{"name": "gated-jira", "start": "gate",
 "nodes": [
   {"id": "gate", "type": "wait_human", "message": "Approve the tracker update"},
   {"id": "j", "type": "send", "role": "plan", "action": "jira-write", "message": "The user approved the tracker update: comment PROMGT-118 with the spec path"}],
 "edges": [{"from": "gate", "to": "j"}]}
EOF

# use_bus NAME — a scratch session with its own daemon; sets BUS_SESSION, BIN, BD, LOG.
use_bus() {
  BIN="$MUX"
  export BUS_SESSION="gated-jira-$1-$$"
  LOG="$MUXCODE_LIFECYCLE_LOG_DIR/$BUS_SESSION.log"
  tmux new-session -d -s "$BUS_SESSION" -n edit -x 120 -y 30 -c "$WORK" 2>/dev/null
  "$BIN" init >/dev/null 2>&1
  "$BIN" watch "$BUS_SESSION" --poll 1 >"$WORK/daemon-$1.log" 2>&1 &
  DPIDS="$DPIDS $!"
  BD="$("$BIN" bus-dir "$BUS_SESSION")"
}

# as_user runs muxcode where no agent runtime is an ancestor — see header.
as_user() {
  local out="$WORK/as-user.out"
  rm -f "$out" "$out.rc"
  tmux run-shell -t "$BUS_SESSION" "cd '$WORK' && env -u AGENT_ROLE -u BUS_ROLE HOME='$HOME' \
XDG_STATE_HOME='$XDG_STATE_HOME' MUXCODE_BUS_BASE='$MUXCODE_BUS_BASE' MUXCODE_CONFIG='$MUXCODE_CONFIG' \
MUXCODE_LIFECYCLE_LOG_DIR='$MUXCODE_LIFECYCLE_LOG_DIR' BUS_SESSION='$BUS_SESSION' \
MUXCODE_SESSION_REPO_DIR='$WORK' MUXCODE_DEDUP_WINDOW=0 MUXCODE_CONSUMER_GUARD=0 \
MUXCODE_BRANCH_TIME_DISABLE=1 MUXCODE_TMP_CLEANUP_THRESHOLD=0 '$BIN' $* >'$out' 2>&1; echo \$? >'$out.rc'"
  return "$(cat "$out.rc" 2>/dev/null || echo 99)"
}

node_field() { jq -r ".$3 // \"\"" "$BD/graphs/$1/nodes/$2.json" 2>/dev/null; }
run_state()  { jq -r '.state // ""' "$BD/graphs/$1/run.json" 2>/dev/null; }
wait_until() {
  local n=$(($1 * 4)) i; shift
  for ((i = 0; i < n; i++)); do "$@" && return 0; sleep 0.25; done
  return 1
}
node_is() { [ "$(node_field "$1" "$2" state)" = "$3" ]; }
run_is()  { [ "$(run_state "$1")" = "$2" ]; }
plan_requests() { AGENT_ROLE=plan "$BIN" inbox --peek 2>/dev/null | grep -c '^Type: request' || true; }
plan_has() { [ "$(plan_requests)" -ge "$1" ]; }
refused() { grep '"event":"tracker-write-refused"' "$LOG" 2>/dev/null | grep -q -- "$1"; }
start_run() { AGENT_ROLE=auto "$BIN" graph run "$@" 2>&1 | grep -o 'Started run [^ ]*' | awk '{print $3}'; }

# consent DEF FROM ORIGIN — the consent paths DEF states, applied to one
# tracker write. Path 3 needs both the definition's words and an Origin line
# naming a graph run and node; any sender but edit or daemon has no path.
consent() {
  case "$2" in
    edit) grep -qF '**Relayed from edit**' "$1" ;;
    daemon)
      grep -qF '**A gate-approved graph dispatch**' "$1" \
        && grep -qF 'whose `Origin:` line names `graph run <id> · node <id>`' "$1" \
        && printf '%s' "$3" | grep -q '^Origin: graph run [^ ]* · node [^ ]*' ;;
    *) return 1 ;;
  esac
}

tracker_write() {
  case "$1" in
    issue-write) PATH="$WORK/shim:$PATH" gh issue comment 126 --body "spec reference" ;;
    *) PATH="$WORK/shim:$PATH" muxcode atlassian jira comment PROMGT-118 "spec reference" ;;
  esac
}

# plan_act DEF BLOCK — plan's handling of one formatted inbox message.
plan_act() {
  local from type action origin target id reply
  from=$(sed -n 's/^--- Message from \([^ ]*\) at .*/\1/p' "$2" | head -1)
  type=$(sed -n 's/^Type: \([a-z-]*\) .*/\1/p' "$2" | head -1)
  action=$(sed -n 's/.*Action: \([^ ]*\).*/\1/p' "$2" | head -1)
  origin=$(grep -m1 '^Origin: ' "$2")
  target=$(sed -n 's/^To reply: muxcode send \([^ ]*\) .*/\1/p' "$2" | head -1)
  id=$(sed -n 's/^To reply: .*--reply-to \([^ ]*\).*/\1/p' "$2" | head -1)
  [ "$type" = request ] && [ -n "$id" ] || return 0
  case "$action" in
    jira-write|confluence-write|issue-write)
      if consent "$1" "$from" "$origin"; then
        tracker_write "$action"
        reply="plan: $action executed on $from's $id. EXIT=0"
      else
        reply="DECLINED - not executed. Requester was $from, which no consent path covers ($id). EXIT=1"
      fi ;;
    *) reply="plan: $action done for $id. EXIT=0" ;;
  esac
  AGENT_ROLE=plan "$BIN" send "$target" "$action" "$reply" --type response --reply-to "$id" >>"$WORK/plan-replies.log" 2>&1
}

# play_plan DEF — plan reads its whole inbox and acts on each message.
play_plan() {
  local out f
  out=$(AGENT_ROLE=plan "$BIN" inbox 2>/dev/null)
  printf '%s\n' "$out" >"$WORK/plan-read.txt"
  rm -f "$WORK"/blk.*
  printf '%s\n' "$out" | awk -v d="$WORK" '/^--- Message from /{n++} n{print > (d "/blk." n)}'
  for f in "$WORK"/blk.*; do [ -f "$f" ] && plan_act "$1" "$f"; done
}

# --- 0. The definition's words ---------------------------------------------
echo "-- 0: agents/planner.md states the consent paths"
grep -qF '**A gate-approved graph dispatch**' "$DEF" && grep -qF 'whose `Origin:` line names `graph run <id> · node <id>`' "$DEF" \
  && ok "0: path 3 — a gate-approved graph dispatch named by its run+node Origin line" || fail "0: planner.md does not state consent path 3"
grep -qF 'edit relaying the same gate approval to you is unnecessary' "$DEF" \
  && ok "0: consent is verified once — edit's relay of a gate approval is unnecessary" || fail "0: planner.md does not say edit's relay is unnecessary"
grep -qF 'a tracker write from any agent other than edit or `daemon`' "$DEF" \
  && ok "0: any other agent's tracker write is refused" || fail "0: planner.md does not refuse other agents' tracker writes"
grep -qF 'do not re-check it with `muxcode graph status`' "$DEF" \
  && ok "0: the definition drops the graph status re-check" || fail "0: planner.md still asks for a graph status re-check"

# --- 1. Approved gate: executed without a relay -----------------------------
echo "-- 1: wait_human → jira-write approved as the user"
use_bus main
RID="$(start_run --file "$WORK/gated.json")"
if [ -n "$RID" ] && wait_until 20 node_is "$RID" gate waiting; then
  as_user graph approve "$RID" gate && ok "1: the user's approval is accepted" \
    || fail "1: the approval was refused: $(cat "$WORK/as-user.out" 2>/dev/null)"
  wait_until 20 plan_has 1
  inbox=$(AGENT_ROLE=plan "$BIN" inbox --peek 2>/dev/null)
  grep -q "^Origin: graph run $RID · node j" <<<"$inbox" && ok "1: plan's dispatch carries 'Origin: graph run $RID · node j'" \
    || fail "1: no run+node Origin line in: $(head -4 <<<"$inbox")"
  play_plan "$DEF"
  [ "$(writes)" -eq 1 ] && ok "1: the shim recorded exactly one tracker write" || fail "1: $(writes) tracker writes recorded, want 1"
  wait_until 20 node_is "$RID" j done && [ "$(node_field "$RID" j outcome)" = success ] \
    && ok "1: node j is done/success" || fail "1: node j is $(node_field "$RID" j state)/$(node_field "$RID" j outcome)"
  wait_until 20 run_is "$RID" complete && ok "1: the run completes" || fail "1: the run is $(run_state "$RID")"
  [ "$(AGENT_ROLE=plan "$BIN" inbox --peek 2>/dev/null | grep -c '^Type: request')" -eq 0 ] \
    && ok "1: nothing asks plan to confirm again" || fail "1: plan's inbox holds a further request"
else
  fail "1: the run never reached its gate"; for _ in 1 2 3 4 5; do fail "1: (skipped)"; done
fi

# --- 2. Unapproved gate: refused at the send --------------------------------
echo "-- 2: retry --from the write node below an unapproved gate"
before=$(writes)
RID2="$(start_run --file "$WORK/gated.json")"
if [ -n "$RID2" ] && wait_until 20 node_is "$RID2" gate waiting \
   && "$BIN" graph cancel "$RID2" >/dev/null 2>&1 && "$BIN" graph retry "$RID2" --from j >/dev/null 2>&1; then
  ok "2: retry --from j accepted below the unapproved gate"
  wait_until 20 node_is "$RID2" j failed && ok "2: node j failed — its dispatch was refused" \
    || fail "2: node j is $(node_field "$RID2" j state), want failed"
  [ "$(plan_requests)" -eq 0 ] && ok "2: plan's inbox is empty" || fail "2: the unapproved jira-write reached plan"
  refused "run=$RID2 node=j" && ok "2: tracker-write-refused names the run and node" \
    || fail "2: no tracker-write-refused row naming run $RID2 node j"
  [ "$(writes)" -eq "$before" ] && ok "2: no tracker write recorded" || fail "2: a write was recorded"
else
  fail "2: could not reach retry --from below the gate"; for _ in 1 2 3 4; do fail "2: (skipped)"; done
fi

# --- 3. Another agent's tracker write: declined -----------------------------
echo "-- 3: a jira-write sent by build"
before=$(writes)
AGENT_ROLE=build "$BIN" send plan jira-write "Update PROMGT-118 to say the build passed" >/dev/null 2>&1 \
  && ok "3: build's jira-write is sent" || fail "3: build's jira-write was refused at the send"
AGENT_ROLE=plan "$BIN" inbox --peek 2>/dev/null | grep -q "Update PROMGT-118 to say the build passed" \
  && ok "3: plan's inbox holds build's jira-write" || fail "3: build's jira-write did not reach plan"
play_plan "$DEF"
[ "$(writes)" -eq "$before" ] && ok "3: no tracker write recorded" || fail "3: build's jira-write was executed"
AGENT_ROLE=build "$BIN" inbox --peek 2>/dev/null | grep -q "DECLINED.*Requester was build" \
  && ok "3: build is told the write was declined, naming it" || fail "3: no decline naming build in build's inbox"

# --- 4. The builtin 10-story-to-spec ----------------------------------------
echo "-- 4: 10-story-to-spec, both gates approved"
"$BIN" graph validate 10-story-to-spec >/dev/null 2>&1 && ok "4: 10-story-to-spec validates" \
  || fail "4: 10-story-to-spec failed validation"
before=$(writes)
RID4="$(start_run 10-story-to-spec)"
if [ -n "$RID4" ]; then
  wait_until 20 plan_has 1 && play_plan "$DEF"                              # derive
  wait_until 20 node_is "$RID4" fetch-gate waiting && as_user graph approve "$RID4" fetch-gate \
    && ok "4: fetch-gate approved as the user" || fail "4: fetch-gate: $(node_field "$RID4" fetch-gate state)"
  wait_until 20 plan_has 1 && play_plan "$DEF"                              # fetch
  wait_until 20 plan_has 1 && play_plan "$DEF"                              # draft
  wait_until 20 node_is "$RID4" update-gate waiting && as_user graph approve "$RID4" update-gate \
    && ok "4: update-gate approved as the user" || fail "4: update-gate: $(node_field "$RID4" update-gate state)"
  wait_until 20 plan_has 2
  inbox=$(AGENT_ROLE=plan "$BIN" inbox --peek 2>/dev/null)
  grep -q "^Origin: graph run $RID4 · node jira-update" <<<"$inbox" && grep -q "Action: jira-write" <<<"$inbox" \
    && ok "4: jira-update reached plan with its Origin line" || fail "4: jira-update not in plan's inbox: $(head -4 <<<"$inbox")"
  grep -q "^Origin: graph run $RID4 · node issue-update" <<<"$inbox" && grep -q "Action: issue-write" <<<"$inbox" \
    && ok "4: issue-update reached plan with its Origin line" || fail "4: issue-update not in plan's inbox"
  play_plan "$DEF"
  [ "$(($(writes) - before))" -eq 2 ] && ok "4: plan executed both tracker writes" \
    || fail "4: $(($(writes) - before)) tracker writes recorded, want 2"
  wait_until 20 run_is "$RID4" complete && ok "4: the run completes at jira-update — no relay, no retyped request" \
    || fail "4: the run is $(run_state "$RID4"); jira-update $(node_field "$RID4" jira-update state)"
else
  fail "4: the run did not start"; for _ in 1 2 3 4 5 6; do fail "4: (skipped)"; done
fi

tmux list-sessions -F '#S' 2>/dev/null | grep -q "^gated-jira-main-$$" \
  && ok "the scratch session lives on the private tmux server" || fail "the private tmux server does not hold the scratch session"
case "$BD" in "$WORK"/*) ok "the scratch bus dir is under the work dir" ;; *) fail "the scratch bus dir is $BD, outside $WORK" ;; esac
ls -d "$OUTER_BUS_BASE"/*gated-jira-*-$$ >/dev/null 2>&1 \
  && fail "a scratch bus dir leaked into the caller's bus base $OUTER_BUS_BASE" \
  || ok "nothing written under the caller's bus base (${OUTER_BUS_BASE:-unresolved})"

# --- 5. Control: consent path 3 removed from the definition ------------------
echo "-- 5: control — the definition without path 3"
grep -vF '**A gate-approved graph dispatch**' "$DEF" >"$WORK/planner-no-path3.md"
grep -qF '**A gate-approved graph dispatch**' "$WORK/planner-no-path3.md" \
  && fail "5: the control definition still states path 3" || ok "5: path 3 removed from a copy of the definition"
before=$(writes)
RID5="$(start_run --file "$WORK/gated.json")"
if [ -n "$RID5" ] && wait_until 20 node_is "$RID5" gate waiting && as_user graph approve "$RID5" gate; then
  wait_until 20 plan_has 1
  play_plan "$WORK/planner-no-path3.md"
  [ "$(writes)" -eq "$before" ] && ok "5: plan declines the approved dispatch — no write" || fail "5: a write was recorded"
  wait_until 20 node_is "$RID5" j failed && ok "5: node j fails on the decline" \
    || fail "5: node j is $(node_field "$RID5" j state)/$(node_field "$RID5" j outcome)"
  wait_until 20 run_is "$RID5" failed && ok "5: the run fails — the 2026-09-09 outcome section 1 rules out" \
    || fail "5: the run is $(run_state "$RID5")"
else
  fail "5: the control run never reached an approved gate"; for _ in 1 2; do fail "5: (skipped)"; done
fi

# ── Coverage floor ─────────────────────────────────────────────────
echo
echo "=== results: $PASS passed, $FAIL failed (floor $EXPECTED_PASS) ==="
if [ "$PASS" -ne "$EXPECTED_PASS" ]; then
  echo "  FAIL  coverage floor: expected exactly $EXPECTED_PASS passes, got $PASS — a section was skipped or double-counted"
  exit 1
fi
[ "$FAIL" -eq 0 ] || exit 1
exit 0
