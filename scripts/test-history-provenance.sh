#!/usr/bin/env bash
# Integration test for MUX-185: a history row's provenance is declared, not
# proven. A `hook` row counts as observed evidence only when the provider
# transcript it names confirms it; `hook guard` refuses an agent's write to a
# history file.
#
# Hermetic: muxcode built from the tree (MUXCODE_HISTORY_PROVENANCE_BIN
# overrides), scratch HOME, state, lifecycle log and CLAUDE_CONFIG_DIR, a
# stand-in Claude transcript in the shape measured live on 2026-11-24, and
# scratch daemons on a private tmux server. Every verdict is the real one: a
# daemon runs a one-node graph `build`, the script plays the build agent —
# writing rows through `muxcode hook bash`, `muxcode log` or a shell append —
# and answers with no EXIT= sentinel, so the rows alone decide the node.
#
#   F  forged row: a `hook` success appended by shell beside an honest
#      self-reported failure — the failure decides, history-row-rejected logged
#   G  genuine row: `hook bash` with a matching transcript record is accepted
#      as hook, outranking a newer self-reported failure; the row carries
#      tool_use_id, transcript_path and actor
#   M  mismatch: the transcript says exit 3, the row 0 — no evidence, the
#      reason names the disagreement
#   W  fake witness: a transcript outside the provider root matching the row
#      exactly — no evidence, the reason names the path
#   H  guard: `hook guard` denies a redirect into build-history.jsonl and logs
#      guard-denied; a jq read is allowed
#   L  legacy before the node started: never read, no rejection row
#   N  legacy inside the window: a witness-less `hook bash` row ranks below an
#      honest self-report
#   C  warm cache: a genuine row accepted, then on the same daemon a forged row
#      reusing its id with another exit code is rejected while the genuine row
#      is accepted again
#   U  unfinished call: a forged success citing a background call is no
#      evidence alone; beside a completed success the success decides
#   O  witness-less road: on an OpenCode-configured build role a self-report
#      is still last-resort evidence, and a witness-less hook row ranks no
#      higher than it
#   X  control: a muxcode built from a copy with the corroboration bypassed
#      takes F's forged row — the check above can see the defect
#
# Usage: bash scripts/test-history-provenance.sh
set -uo pipefail
export MUXCODE_CONSUMER_GUARD=0 # the build agent is played by reading its inbox, which no window or listener shows (MUX-145)

PASS=0
FAIL=0
EXPECTED_PASS=28

command -v jq >/dev/null 2>&1 || { echo "SKIP: jq is required"; exit 2; }
command -v go >/dev/null 2>&1 || { echo "SKIP: go is required"; exit 2; }
command -v tmux >/dev/null 2>&1 || { echo "SKIP: tmux is required"; exit 2; }

REPO=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
WORK=$(mktemp -d /tmp/history-provenance-XXXXXX)
. "$REPO/scripts/lib/private-tmux.sh"
private_tmux_init "$WORK/tmux"
DPIDS=""
cleanup() {
  local p
  for p in $DPIDS; do kill "$p" 2>/dev/null; done
  private_tmux_kill
  rm -rf "$WORK"
}
trap cleanup EXIT
export GOCACHE="$(go env GOCACHE)" GOMODCACHE="$(go env GOMODCACHE)" GOTOOLCHAIN=local

mkdir -p "$WORK/bin"
if [ -n "${MUXCODE_HISTORY_PROVENANCE_BIN:-}" ]; then
  MUX="$(cd "$(dirname "$MUXCODE_HISTORY_PROVENANCE_BIN")" && pwd)/$(basename "$MUXCODE_HISTORY_PROVENANCE_BIN")"
else
  (cd "$REPO/tools/muxcode" && go build -buildvcs=false -o "$WORK/bin/muxcode" .) \
    || { echo "SKIP: go build muxcode failed"; exit 2; }
  MUX="$WORK/bin/muxcode"
fi

ok()   { PASS=$((PASS + 1)); echo "  ok: $1"; }
fail() { FAIL=$((FAIL + 1)); echo "  FAIL: $1"; }

mkdir -p "$WORK/home" "$WORK/config" "$WORK/lifecycle" "$WORK/claude/projects/-work" "$WORK/fake"
export HOME="$WORK/home"
export XDG_STATE_HOME="$WORK/home/.local/state"
export MUXCODE_CONFIG_DIR="$WORK/config"
export MUXCODE_LIFECYCLE_LOG_DIR="$WORK/lifecycle"
export CLAUDE_CONFIG_DIR="$WORK/claude"
export CODEX_HOME="$WORK/codex"
export MUXCODE_DEDUP_WINDOW=0
export MUXCODE_BUILD_CLI=claude MUXCODE_TEST_CLI=claude
unset MUXCODE_CONFIG MUXCODE_AGENT_CLI MUXCODE_BUILD_PATTERNS MUXCODE_BUILD_PRECHECK_PATTERNS \
  MUXCODE_TEST_PATTERNS MUXCODE_TEST_PRECHECK_PATTERNS 2>/dev/null || true
cd "$WORK" || exit 1

TRANSCRIPT="$WORK/claude/projects/-work/session.jsonl"
FAKE="$WORK/fake/session.jsonl"

cat > "$WORK/build-node.json" <<'JSON'
{"name": "provenance-build-node", "description": "MUX-185: one build node", "start": "build",
 "nodes": [{"id": "build", "type": "send", "role": "build", "action": "build", "message": "Run ./build.sh and report"}],
 "edges": []}
JSON

# transcript_call FILE ID EXIT [background] — append a ./build.sh Bash call in
# the Claude transcript shape: the tool_use record, then its tool_result.
transcript_call() {
  local file="$1" id="$2" exit="$3" bg="${4:-}"
  jq -nc --arg id "$id" '{type:"assistant",message:{content:[{type:"tool_use",id:$id,name:"Bash",input:{command:"./build.sh"}}]}}' >>"$file"
  if [ "$exit" -ne 0 ]; then
    jq -nc --arg id "$id" --arg t "Exit code $exit"$'\nboom' \
      '{type:"user",message:{content:[{type:"tool_result",tool_use_id:$id,is_error:true,content:$t}]},toolUseResult:("Error: "+$t)}' >>"$file"
  elif [ -n "$bg" ]; then
    jq -nc --arg id "$id" '{type:"user",message:{content:[{type:"tool_result",tool_use_id:$id,is_error:false,content:"started"}]},
      toolUseResult:{stdout:"",stderr:"",interrupted:false,isImage:false,backgroundTaskId:"bg1"}}' >>"$file"
  else
    jq -nc --arg id "$id" '{type:"user",message:{content:[{type:"tool_result",tool_use_id:$id,is_error:false,content:"built"}]},
      toolUseResult:{stdout:"built",stderr:"",interrupted:false,isImage:false}}' >>"$file"
  fi
}
transcript_call "$TRANSCRIPT" toolu_g 0
transcript_call "$TRANSCRIPT" toolu_m 3
transcript_call "$TRANSCRIPT" toolu_c 0
transcript_call "$TRANSCRIPT" toolu_bg 0 background
transcript_call "$TRANSCRIPT" toolu_done 0
transcript_call "$FAKE" toolu_w 0

# new_bus NAME [BIN] — a scratch bus and its own daemon; sets BUS_SESSION, BD, LOG, BIN.
new_bus() {
  BIN="${2:-$MUX}"
  export BUS_SESSION="hprov-$1-$$"
  BD="$("$BIN" bus-dir "$BUS_SESSION")" || exit 1
  LOG="$MUXCODE_LIFECYCLE_LOG_DIR/$BUS_SESSION.log"
  "$BIN" init >/dev/null 2>&1
  tmux new-session -d -s "$BUS_SESSION" -n edit 2>/dev/null
  "$BIN" watch "$BUS_SESSION" --poll 1 >"$WORK/daemon-$1.log" 2>&1 &
  DPIDS="$DPIDS $!"
}

# dispatch — start the one-node graph and wait for its build request; sets RID
# and NODE. Rows written after this return are inside the node's window.
dispatch() {
  local run i
  RID=""
  run=$("$BIN" graph run --file "$WORK/build-node.json" "mux185" 2>&1 | grep -o 'Started run [^ ]*' | awk '{print $3}')
  NODE="$BD/graphs/$run/nodes/build.json"
  for i in $(seq 1 40); do
    RID=$(AGENT_ROLE=build "$BIN" inbox --peek 2>/dev/null | grep -o -- '--reply-to [A-Za-z0-9-]*' | head -1 | awk '{print $2}')
    [ -n "$RID" ] && break
    sleep 0.5
  done
  AGENT_ROLE=build "$BIN" inbox >/dev/null 2>&1
  [ -n "$RID" ]
}

# answer TEXT — reply with no sentinel and print the node's outcome once recorded.
answer() {
  local i
  AGENT_ROLE=build "$BIN" send edit response "$1" --type response --reply-to "$RID" >/dev/null 2>&1
  for i in $(seq 1 40); do
    if [ -n "$(jq -r '.outcome // empty' "$NODE" 2>/dev/null)" ]; then
      jq -r '.outcome' "$NODE"
      return
    fi
    sleep 0.5
  done
  echo none
}

# hook_row ID TRANSCRIPT EXIT — the PostToolUse(Failure) event Claude would send
# for ./build.sh, fed to `hook bash`; an empty ID or TRANSCRIPT omits the field.
hook_row() {
  local ev
  if [ "$3" -eq 0 ]; then
    ev=$(jq -nc '{hook_event_name:"PostToolUse",tool_name:"Bash",tool_input:{command:"./build.sh"},
      tool_response:{stdout:"built",stderr:"",interrupted:false,isImage:false}}')
  else
    ev=$(jq -nc --arg e "Exit code $3"$'\nboom' '{hook_event_name:"PostToolUseFailure",tool_name:"Bash",
      tool_input:{command:"./build.sh"},error:$e,is_interrupt:false}')
  fi
  jq -c --arg id "$1" --arg tp "$2" '(if $id != "" then .tool_use_id = $id else . end)
    | (if $tp != "" then .transcript_path = $tp else . end)' <<<"$ev" \
    | AGENT_ROLE=build "$BIN" hook bash >/dev/null 2>&1
}

# forge EXIT [ID TRANSCRIPT] [TS] — what an agent's shell appends by hand.
forge() {
  local outcome=success ts="${4:-$(date +%s)}"
  [ "$1" -eq 0 ] || outcome=failure
  jq -nc --argjson ts "$ts" --arg x "$1" --arg o "$outcome" --arg id "${2:-}" --arg tp "${3:-}" \
    '{ts:$ts,command:"./build.sh",exit_code:$x,outcome:$o,source:"hook"}
     + (if $id != "" then {tool_use_id:$id,transcript_path:$tp} else {} end)' >>"$BD/build-history.jsonl"
}

# self_report EXIT — the honest road: `muxcode log`, filed self-reported.
self_report() {
  AGENT_ROLE=build "$BIN" log build "build result" --exit-code "$1" --command ./build.sh >/dev/null 2>&1
}

rejected() { grep '"history-row-rejected"' "$LOG" 2>/dev/null | grep -q -- "$1"; }

# Each tick of the same second shares a ts; sleeping 1 s orders rows strictly.
tick() { sleep 1; }

# forged_case NAME [BIN] — F's fixture: an honest failure, then a forged
# success; sets OUT. Not run in a subshell: new_bus's globals must survive.
forged_case() {
  OUT=none
  new_bus "$1" "${2:-}"
  dispatch || return
  self_report 1; tick
  forge 0
  OUT=$(answer "build finished ($1)")
}

echo "-- F: a forged hook row is no evidence"
forged_case f
out=$OUT
[ "$out" = failure ] && ok "F: the honest self-reported failure decides over a forged hook success" \
  || fail "F: outcome $out, want failure — the forged row was taken"
rejected "no witness" && ok "F: history-row-rejected names the missing witness" \
  || fail "F: no history-row-rejected row for the forged row"

echo "-- G: a genuine row is accepted as hook"
new_bus g
if dispatch; then
  hook_row toolu_g "$TRANSCRIPT" 0; tick
  self_report 1
  out=$(answer "build finished (g)")
  [ "$out" = success ] && ok "G: the witnessed hook success outranks a newer self-reported failure" \
    || fail "G: outcome $out, want success — the genuine row was not accepted as hook"
  grep -q '"history-row-rejected"' "$LOG" 2>/dev/null && fail "G: a genuine row was logged rejected" \
    || ok "G: no rejection row for the genuine row"
  row=$(jq -c 'select(.source == "hook")' "$BD/build-history.jsonl" | tail -1)
  [ "$(jq -r '[.tool_use_id, .actor] | join(" ")' <<<"$row")" = "toolu_g build" ] && [ -n "$(jq -r '.transcript_path // empty' <<<"$row")" ] \
    && ok "G: the hook row carries tool_use_id, transcript_path and actor" \
    || fail "G: hook row fields: $row"
else
  fail "G: the build node never dispatched"; fail "G: (skipped)"; fail "G: (skipped)"
fi

echo "-- M: a row its transcript contradicts"
new_bus m
if dispatch; then
  hook_row toolu_m "$TRANSCRIPT" 0
  out=$(answer "build finished (m)")
  [ "$out" = unknown ] && ok "M: the contradicted row decides nothing — the node holds" \
    || fail "M: outcome $out, want unknown"
  rejected "exit code 0, transcript says 3" && ok "M: the rejection names the disagreement" \
    || fail "M: no rejection naming the exit-code disagreement"
else
  fail "M: the build node never dispatched"; fail "M: (skipped)"
fi

echo "-- W: a witness outside the provider root"
new_bus w
if dispatch; then
  forge 0 toolu_w "$FAKE"
  out=$(answer "build finished (w)")
  [ "$out" = unknown ] && ok "W: a matching transcript outside the root corroborates nothing" \
    || fail "W: outcome $out, want unknown"
  rejected "transcript_path $FAKE outside the provider transcript root" && ok "W: the rejection names the path" \
    || fail "W: no rejection naming $FAKE"
else
  fail "W: the build node never dispatched"; fail "W: (skipped)"
fi

echo "-- H: hook guard on history files"
new_bus h
deny=$(jq -nc --arg c "echo '{\"source\":\"hook\"}' >> $BD/build-history.jsonl" \
  '{hook_event_name:"PreToolUse",tool_name:"Bash",tool_input:{command:$c}}' | AGENT_ROLE=build "$BIN" hook guard 2>/dev/null)
grep -q 'MUX-185' <<<"$deny" && ok "H: a redirect into build-history.jsonl is denied" \
  || fail "H: redirect not denied: $deny"
grep '"guard-denied"' "$LOG" 2>/dev/null | grep -q 'history.jsonl' && ok "H: guard-denied is logged" \
  || fail "H: no guard-denied row naming the history file"
allow=$(jq -nc --arg c "jq . $BD/build-history.jsonl" '{hook_event_name:"PreToolUse",tool_name:"Bash",tool_input:{command:$c}}' \
  | AGENT_ROLE=build "$BIN" hook guard 2>/dev/null)
[ -z "$allow" ] && ok "H: a jq read of the history file is allowed" || fail "H: jq read refused: $allow"

echo "-- L: a legacy row before the node started"
new_bus l
forge 0 "" "" "$(( $(date +%s) - 100 ))"
if dispatch; then
  out=$(answer "build finished (l)")
  [ "$out" = unknown ] && ok "L: a row older than the node is never evidence" || fail "L: outcome $out, want unknown"
  grep -q '"history-row-rejected"' "$LOG" 2>/dev/null && fail "L: a row never read was logged rejected" \
    || ok "L: no rejection row — the row was never read"
else
  fail "L: the build node never dispatched"; fail "L: (skipped)"
fi

echo "-- N: a witness-less hook row inside the window"
new_bus n
if dispatch; then
  self_report 1; tick
  hook_row "" "" 0
  out=$(answer "build finished (n)")
  [ "$out" = failure ] && ok "N: a witness-less hook row ranks below the honest self-report" \
    || fail "N: outcome $out, want failure"
  rejected "no witness" && ok "N: history-row-rejected names the missing witness" || fail "N: no rejection row"
else
  fail "N: the build node never dispatched"; fail "N: (skipped)"
fi

echo "-- C: a warm cache compares every row"
new_bus c
if dispatch; then
  hook_row toolu_c "$TRANSCRIPT" 0
  out=$(answer "build finished (c1)")
  [ "$out" = success ] && ok "C: the genuine row is accepted (cache warmed)" || fail "C: first outcome $out, want success"
else
  fail "C: the first build node never dispatched"
fi
if dispatch; then
  hook_row toolu_c "$TRANSCRIPT" 0; tick
  forge 1 toolu_c "$TRANSCRIPT"
  out=$(answer "build finished (c2)")
  [ "$out" = success ] && ok "C: on the same daemon the genuine row is accepted again over a newer forgery" \
    || fail "C: second outcome $out, want success"
  rejected "exit code 1, transcript says 0" && ok "C: the forgery reusing the id is rejected" \
    || fail "C: no rejection for the forged row reusing toolu_c"
else
  fail "C: the second build node never dispatched"; fail "C: (skipped)"
fi

echo "-- U: a forged success citing an unfinished call"
new_bus u1
if dispatch; then
  forge 0 toolu_bg "$TRANSCRIPT"
  out=$(answer "build finished (u1)")
  [ "$out" = unknown ] && ok "U: a background call's id corroborates nothing alone" || fail "U: lone outcome $out, want unknown"
  rejected "not final in transcript: background" && ok "U: the rejection names the unfinished call" \
    || fail "U: no rejection naming the background call"
else
  fail "U: the lone build node never dispatched"; fail "U: (skipped)"
fi
new_bus u2
if dispatch; then
  self_report 1; tick
  hook_row toolu_done "$TRANSCRIPT" 0; tick
  forge 0 toolu_bg "$TRANSCRIPT"
  out=$(answer "build finished (u2)")
  [ "$out" = success ] && ok "U: the completed success decides, outranking the self-report, beside the background-citing row" \
    || fail "U: outcome $out, want success"
else
  fail "U: the paired build node never dispatched"
fi

echo "-- O: a witness-less road keeps its self-reports"
MUXCODE_BUILD_CLI=opencode
new_bus o1
if dispatch; then
  self_report 0
  out=$(answer "build finished (o1)")
  [ "$out" = success ] && ok "O: an OpenCode role's self-report is still last-resort evidence" || fail "O: outcome $out, want success"
else
  fail "O: the self-report build node never dispatched"
fi
new_bus o2
if dispatch; then
  self_report 1; tick
  forge 0
  out=$(answer "build finished (o2)")
  [ "$out" = failure ] && ok "O: a witness-less hook row there ranks no higher than the self-report" \
    || fail "O: outcome $out, want failure"
else
  fail "O: the paired build node never dispatched"
fi
MUXCODE_BUILD_CLI=claude

tmux list-sessions -F '#S' 2>/dev/null | grep -q "^hprov-f-$$" \
  && ok "the scratch sessions live on the private tmux server" \
  || fail "the private tmux server does not hold the scratch sessions"

echo "-- X: control — corroboration bypassed"
CTRL_SRC="$WORK/ctrl-src"
cp -R "$REPO/tools/muxcode" "$CTRL_SRC"
perl -0pi -e 's/(func corroborateHookRow\(e ConsoleEntry\) string \{\n)/$1\treturn ""\n/' "$CTRL_SRC/bus/history_witness.go"
grep -A1 'func corroborateHookRow' "$CTRL_SRC/bus/history_witness.go" | grep -q 'return ""' \
  && ok "X: corroboration bypassed in a copy of the tree" \
  || fail "X: the bypass patch did not apply — corroborateHookRow changed shape"
if (cd "$CTRL_SRC" && go build -buildvcs=false -o "$WORK/bin/muxcode-ctrl" .) 2>"$WORK/ctrl-build.log"; then
  ok "X: the bypassed muxcode builds"
  forged_case xf "$WORK/bin/muxcode-ctrl"
  out=$OUT
  [ "$out" = success ] && ok "X: without corroboration the forged row decides success — the defect F catches" \
    || fail "X: bypassed outcome $out, want success — F may be passing for another reason"
else
  fail "X: building the bypassed muxcode failed: $(tail -3 "$WORK/ctrl-build.log")"
  fail "X: (skipped)"
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
