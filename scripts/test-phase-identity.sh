#!/usr/bin/env bash
# Integration test for MUX-143 — a run carries two phase identities.
#
# On 2026-09-03 a run labelled Phase 5 handed its worker "Phase 2: Config and
# CDK": the intent's phase (frozen at launch) and the spec's lowest open phase
# (live) disagreed, nothing compared them, and a phase-complete guard checked
# the phase the worker was not given. And the reason Phase 2 stayed open was a
# box parked on a user decision, which held the loop on Phase 2 forever.
#
# A real `muxcode graph run` against a scratch daemon walks each case; the
# script reads every dispatch per run from the append-only bus log, so a stale
# dispatch from an earlier section can never be captured. Sections:
#   B  intent Phase 5, derived Phase 2: the launch prints the mismatch warning
#      naming both, writes graph-phase-identity-mismatch naming the run, the
#      run is created, and implement is dispatched for Phase 2
#   C  negative control — intent and derivation agree: no warning, no row, and
#      implement dispatches straight away (no gate added)
#   D  negative control — an intent naming no phase: the existing
#      unscoped-guard warning alone
#   E  the only open item of Phase 1 parked under `### Deferred`: derivation
#      moves on, implement is dispatched for Phase 2
#   F  negative control — the item moved back inside Phase 1 holds the loop
#      there: implement is dispatched for Phase 1
#   G  close-out on the deferred spec is still refused, naming the item
# A coverage floor keeps a skipped section from reporting green.
#
# ISOLATION: scratch BUS_SESSION, HOME, repo dir via MUXCODE_SESSION_REPO_DIR,
# lifecycle log, private tmux server, empty config.
#
# REQUIRES: tmux, git, jq; a muxcode with MUX-143 — the installed one, or
# MUXCODE_BIN.
#
# Usage: bash scripts/test-phase-identity.sh
set -uo pipefail
export MUXCODE_CONSUMER_GUARD=0 # agents here are played by reading the bus log (MUX-145)

MUX="${MUXCODE_BIN:-muxcode}"
MUX="$(command -v "$MUX" 2>/dev/null || echo "$MUX")"
[ -x "$MUX" ] || { echo "  FAIL  cannot resolve muxcode binary ('$MUX') — set MUXCODE_BIN"; exit 1; }
for t in tmux git jq; do
  command -v "$t" >/dev/null 2>&1 || { echo "SKIP: $t is required"; exit 2; }
done
. "$(dirname "${BASH_SOURCE[0]}")/lib/muxcode-bus-dir.sh"

GREEN=$'\033[0;32m'; RED=$'\033[0;31m'; NC=$'\033[0m'
pass=0; fail=0
EXPECTED=23
ok()  { echo "  ${GREEN}PASS${NC}  $*"; pass=$((pass + 1)); }
bad() { echo "  ${RED}FAIL${NC}  $*"; fail=$((fail + 1)); }
check() { local desc=$1; shift; if "$@" >/dev/null 2>&1; then ok "$desc"; else bad "$desc"; fi; }
contains() { case $1 in *"$2"*) return 0 ;; esac; return 1; }
lacks() { case $1 in *"$2"*) return 1 ;; esac; return 0; }

# --- Isolation -------------------------------------------------------------
export BUS_SESSION="phaseid-test-$$"
WORK="/tmp/phaseid-work-$$"
REPO="$WORK/repo"
mkdir -p "$REPO/docs/requirements/drafts"
export HOME="$WORK/home"
mkdir -p "$HOME/.config/muxcode"
git -C "$REPO" init -q
git -C "$REPO" config user.email "muxcode-test@example.invalid"
git -C "$REPO" config user.name "muxcode-test"
echo fixture > "$REPO/README.md"
git -C "$REPO" add README.md && git -C "$REPO" commit -q -m "fixture base"
export MUXCODE_LIFECYCLE_LOG_DIR="$WORK/lifecycle"
: > "$WORK/empty-config"
export MUXCODE_CONFIG="$WORK/empty-config"
export MUXCODE_TMP_CLEANUP_THRESHOLD=0 MUXCODE_BRANCH_TIME_DISABLE=1 MUXCODE_DEDUP_WINDOW=0
export MUXCODE_SESSION_REPO_DIR="$REPO"
BD="$(muxcode_bus_dir "$MUX" "$BUS_SESSION")" || exit 1
LIFELOG="$MUXCODE_LIFECYCLE_LOG_DIR/$BUS_SESSION.log"

DPID=""
. "$(dirname "${BASH_SOURCE[0]}")/lib/private-tmux.sh"
private_tmux_init "$WORK/tmux"
cleanup() {
  [ -n "$DPID" ] && kill "$DPID" 2>/dev/null
  private_tmux_kill
  rm -rf "$BD" "$WORK"
}
trap cleanup EXIT
tmux new-session -d -s "$BUS_SESSION" -n edit -c "$WORK" "cat > /dev/null"
"$MUX" init >/dev/null 2>&1

SPEC_REL="docs/requirements/drafts/fixture-spec.md"
SPEC="$REPO/$SPEC_REL"
rows() { local c; c="$(grep -c "\"event\":\"$1\"" "$LIFELOG" 2>/dev/null)"; echo "${c:-0}"; }
row_naming() { jq -r --arg e "$1" 'select(.event == $e) | .detail' "$LIFELOG" 2>/dev/null | grep -qF "$2"; }
# dispatch RID ACTION — the newest dispatch of ACTION for run RID, from the bus log.
dispatch() {
  jq -r --arg r "$1" --arg a "$2" 'select(.type == "request" and .action == $a and .graph_run == $r) | .payload' \
    "$BD/log.jsonl" 2>/dev/null | tail -1
}
wait_dispatch() {
  local i
  for i in $(seq 1 60); do [ -n "$(dispatch "$1" "$2")" ] && return 0; sleep 0.5; done
  return 1
}
node_state() { "$MUX" graph status "$1" 2>/dev/null | sed $'s/\x1b\\[[0-9;]*[A-Za-z]//g' | awk -v n="$2" '$1==n {print $2}'; }
wait_node_state() {
  local i
  for i in $(seq 1 60); do [ "$(node_state "$1" "$2")" = "$3" ] && return 0; sleep 0.5; done
  return 1
}
# launch GRAPH INTENT — run it; sets RID and OUT (the launch's full output).
launch() {
  OUT="$("$MUX" graph run --file "$1" "$2" 2>&1)"
  RID="$(printf '%s\n' "$OUT" | grep -o 'Started run [^ ]*' | awk '{print $3}')"
}
cancel() { [ -n "${RID:-}" ] && "$MUX" graph cancel "$RID" >/dev/null 2>&1; true; }

write_spec() { printf '%s' "$1" > "$SPEC"; }
DIVERGENT=$'# Fixture\n\n## Implementation\n\n### Phase 2: Config and CDK\n\n- [ ] open two\n\n### Phase 5: Ship\n\n- [x] done five\n'
PARKED=$'# Fixture\n\n## Implementation\n\n### Phase 1: Config\n\n- [x] wire the loader\n\n### Deferred\n\n- [ ] populate the prod role list on a user decision\n\n### Phase 2: Next\n\n- [ ] next work\n'
UNPARKED=$'# Fixture\n\n## Implementation\n\n### Phase 1: Config\n\n- [x] wire the loader\n- [ ] populate the prod role list on a user decision\n\n### Phase 2: Next\n\n- [ ] next work\n'

cat > "$WORK/identity.json" <<'EOF'
{"name": "identity", "start": "implement",
 "nodes": [
   {"id": "implement", "type": "send", "role": "edit", "action": "g-edit", "message": "Implement ${current_phase}"},
   {"id": "ship", "type": "send", "role": "plan", "action": "g-ship", "guard": "phase-complete", "message": "ship"}],
 "edges": [{"from": "implement", "to": "ship"}]}
EOF
cat > "$WORK/close.json" <<'EOF'
{"name": "close", "start": "close",
 "nodes": [{"id": "close", "type": "send", "role": "plan", "action": "g-close", "guard": "spec-complete", "message": "close out the spec"}],
 "edges": []}
EOF

echo "=== MUX-143 phase identity, end to end ==="

# --- A. setup ----------------------------------------------------------------
check "A: the fixture graphs validate" bash -c "'$MUX' graph validate '$WORK/identity.json' && '$MUX' graph validate '$WORK/close.json'"
write_spec "$DIVERGENT"
git -C "$REPO" add "$SPEC_REL" && git -C "$REPO" commit -q -m "fixture spec"
(cd "$REPO" && "$MUX" spec set "$SPEC_REL" >/dev/null 2>&1) && ok "A: active spec set" || bad "A: spec set failed"
(cd "$REPO" && exec "$MUX" watch "$BUS_SESSION" --poll 1 >"$WORK/daemon.log" 2>&1) &
DPID=$!
sleep 1
kill -0 "$DPID" 2>/dev/null && ok "A: scratch daemon running" || bad "A: daemon exited: $(tail -3 "$WORK/daemon.log")"

# --- B. divergent intent -----------------------------------------------------
echo "-- B: intent Phase 5, derived Phase 2"
launch "$WORK/identity.json" "MUX-143 — Phase 5: Ship"
check "B: the run was created ($RID)" [ -n "$RID" ]
check "B: the launch warns naming both phases" contains "$OUT" 'Warning: the intent names Phase 5 but the active spec'"'"'s lowest open phase is "Phase 2: Config and CDK"'
check "B: a graph-phase-identity-mismatch row names the run" row_naming graph-phase-identity-mismatch "$RID"
check "B: implement is dispatched" wait_dispatch "$RID" g-edit
check "B: for the derived Phase 2" contains "$(dispatch "$RID" g-edit)" "Implement Phase 2: Config and CDK"
cancel

# --- C. negative control: agreement ------------------------------------------
echo "-- C: negative control — intent and derivation agree"
MISMATCH_ROWS=$(rows graph-phase-identity-mismatch)
launch "$WORK/identity.json" "MUX-143 — Phase 2: Config and CDK"
check "C: the run was created" [ -n "$RID" ]
check "C: no warning at launch" lacks "$OUT" "Warning:"
check "C: no mismatch row" [ "$(rows graph-phase-identity-mismatch)" -eq "$MISMATCH_ROWS" ]
check "C: implement dispatched straight away — no gate added" wait_dispatch "$RID" g-edit
cancel

# --- D. negative control: no phase named --------------------------------------
echo "-- D: negative control — an intent naming no phase"
launch "$WORK/identity.json" "MUX-143 ship it"
check "D: the run was created" [ -n "$RID" ]
check "D: the existing unscoped-guard warning is printed" contains "$OUT" "the intent names no phase"
check "D: and no mismatch warning" lacks "$OUT" "the intent names Phase"
check "D: exactly one warning line" [ "$(printf '%s\n' "$OUT" | grep -c '^Warning:')" -eq 1 ]
cancel

# --- E. the parked item lets the loop move on ---------------------------------
echo "-- E: Phase 1's only open item parked under ### Deferred"
write_spec "$PARKED"
launch "$WORK/identity.json" "MUX-143 walk the spec"
check "E: implement is dispatched" wait_dispatch "$RID" g-edit
check "E: for Phase 2 — the parked item does not hold Phase 1" contains "$(dispatch "$RID" g-edit)" "Implement Phase 2: Next"
cancel

# --- F. negative control: the item back inside its phase ----------------------
echo "-- F: negative control — the item moved back inside Phase 1"
write_spec "$UNPARKED"
launch "$WORK/identity.json" "MUX-143 walk the spec"
check "F: implement is dispatched" wait_dispatch "$RID" g-edit
check "F: for Phase 1 — the open item holds the loop there" contains "$(dispatch "$RID" g-edit)" "Implement Phase 1: Config"
cancel

# --- G. close-out still refused on the parked item -----------------------------
echo "-- G: close-out with the deferred item open"
write_spec "$PARKED"
DECLINES=$(rows graph-guard-declined)
launch "$WORK/close.json" "MUX-143 close out"
check "G: the close node is refused" wait_node_state "$RID" close failed
check "G: the spec-complete decline names the deferred item" row_naming graph-guard-declined "populate the prod role list on a user decision"
check "G: one new decline row" [ "$(rows graph-guard-declined)" -eq $((DECLINES + 1)) ]

# --- Coverage floor ----------------------------------------------------------
echo ""
echo "=== $pass passed, $fail failed (floor $EXPECTED) ==="
if [ "$pass" -ne "$EXPECTED" ]; then
  echo "  ${RED}FAIL${NC}  coverage floor: expected exactly $EXPECTED passes, got $pass — a section was skipped or double-counted"
  exit 1
fi
[ "$fail" -eq 0 ]
