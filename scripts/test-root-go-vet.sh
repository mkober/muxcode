#!/usr/bin/env bash
# Integration test for MUX-214: the build agent's root `go vet` false alarm.
#
# The build agent ran `go vet ./...` at a repository root that is not a Go
# module; it failed every build, and as a test precheck its failure told edit
# "Tests FAILED … check test window" from the build pane, wrote a test-history
# row and moved the workflow to test-failed. The fix: the lint step vets each
# module with `go -C <module> vet ./...`, finding modules with the discovery
# command in agents/code-builder.md, and a test precheck the build agent runs is
# filed as a build precheck (StageForRole).
#
# Hermetic: muxcode built from the tree (MUXCODE_ROOT_GO_VET_BIN overrides),
# scratch git repositories, scratch HOME and BUS_SESSIONs. Every command runs
# for real and its real exit and output are fed to `hook bash` as a Claude
# event. The discovery command is read from agents/code-builder.md itself, so
# the test follows the instruction the agent follows.
#
#   A  a clean build cycle in a repo whose modules sit under tools/: discovery
#      lists no root module; each module vets clean; no failing row, no
#      notice to edit; the build still chains to test
#   B  a real vet finding in a module: a build-history row and "Build FAILED"
#      to edit, both naming the module; no test-history row, no "Tests FAILED"
#   C  an untracked module is discovered and vetted; an ignored one and a
#      testdata one are not
#   D  negative controls: a root-module repo with a finding still fails and
#      notifies edit; the test agent's own failing vet is still "Tests FAILED"
#   E  MUX-218: a one-node graph `build` run, executed by a scratch daemon,
#      whose build agent's vet fails after dispatch — a reply with no
#      sentinel ends the node failure on the row, and a reply claiming EXIT=0
#      holds it (outcome unknown) with a graph-outcome-conflict row. Negative
#      control: a muxcode built from a copy of the tree with the executor's
#      role-aware attribution (StageForRole in rowAttributesTo) removed records
#      the EXIT=0 case success, proving the check can see the defect
#
# Usage: bash scripts/test-root-go-vet.sh
set -uo pipefail

PASS=0
FAIL=0
EXPECTED_PASS=34

command -v jq >/dev/null 2>&1 || { echo "SKIP: jq is required"; exit 2; }
command -v git >/dev/null 2>&1 || { echo "SKIP: git is required"; exit 2; }
command -v go >/dev/null 2>&1 || { echo "SKIP: go is required"; exit 2; }
command -v tmux >/dev/null 2>&1 || { echo "SKIP: tmux is required"; exit 2; }

REPO=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
WORK=$(mktemp -d /tmp/root-go-vet-XXXXXX)
# A private tmux server for section E's sessions and the daemons this script
# starts: TMUX_TMPDIR moves the socket, and TMUX — set when run from an agent's
# pane — would otherwise name the user's live server first (MUX-226).
REAL_TMUX_SOCK="${TMUX_TMPDIR:-/tmp}/tmux-$(id -u)/default"
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
export GOCACHE="$(go env GOCACHE)" GOMODCACHE="$(go env GOMODCACHE)" GOTOOLCHAIN=local

if [ -n "${MUXCODE_ROOT_GO_VET_BIN:-}" ]; then
  MUX="$(cd "$(dirname "$MUXCODE_ROOT_GO_VET_BIN")" && pwd)/$(basename "$MUXCODE_ROOT_GO_VET_BIN")"
else
  mkdir -p "$WORK/bin"
  (cd "$REPO/tools/muxcode" && go build -buildvcs=false -o "$WORK/bin/muxcode" .) \
    || { echo "SKIP: go build muxcode failed"; exit 2; }
  MUX="$WORK/bin/muxcode"
fi

ok()   { PASS=$((PASS + 1)); echo "  ok: $1"; }
fail() { FAIL=$((FAIL + 1)); echo "  FAIL: $1"; }

mkdir -p "$WORK/home" "$WORK/config" "$WORK/lifecycle"
export HOME="$WORK/home"
export XDG_STATE_HOME="$WORK/home/.local/state"
export MUXCODE_CONFIG_DIR="$WORK/config"
export MUXCODE_LIFECYCLE_LOG_DIR="$WORK/lifecycle"
export MUXCODE_DEDUP_WINDOW=0
export MUXCODE_BUILD_CLI=claude MUXCODE_TEST_CLI=claude
unset MUXCODE_CONFIG MUXCODE_AGENT_CLI MUXCODE_BUILD_PATTERNS MUXCODE_BUILD_PRECHECK_PATTERNS \
  MUXCODE_TEST_PATTERNS MUXCODE_TEST_PRECHECK_PATTERNS 2>/dev/null || true
# Every muxcode call runs from the scratch dir: the chain config is read from
# the working directory's .muxcode/muxcode.json, and the caller's checkout must
# not decide what this test's chains send.
cd "$WORK" || exit 1

DISCOVER=$(grep -o "git ls-files --cached --others --exclude-standard -- 'go.mod' '\*/go.mod'" "$REPO/agents/code-builder.md" | head -1)

# new_session NAME — a scratch bus; sets BUS_SESSION and BD.
new_session() {
  export BUS_SESSION="root-go-vet-$1-$$"
  BD="$("$MUX" bus-dir "$BUS_SESSION")" || exit 1
  "$MUX" init >/dev/null 2>&1
}

# post ROLE COMMAND DIR — run COMMAND in DIR for real, then feed its Claude
# PostToolUse or PostToolUseFailure event to `hook bash` as ROLE. Prints the exit.
post() {
  local out rc
  out=$(cd "$3" && bash -c "$2" 2>&1)
  rc=$?
  if [ "$rc" -eq 0 ]; then
    jq -nc --arg c "$2" --arg o "$out" '{hook_event_name:"PostToolUse",tool_name:"Bash",tool_use_id:"toolu_mux214",
      tool_input:{command:$c},tool_response:{stdout:$o,stderr:"",interrupted:false}}'
  else
    jq -nc --arg c "$2" --arg e "Exit code $rc"$'\n'"$out" '{hook_event_name:"PostToolUseFailure",tool_name:"Bash",
      tool_use_id:"toolu_mux214f",tool_input:{command:$c},error:$e,is_interrupt:false}'
  fi | AGENT_ROLE="$1" "$MUX" hook bash >/dev/null 2>&1
  echo "$rc"
}

# discover DIR — the lint step's module list: the definition's command, minus
# testdata/ and vendor/, as module directories.
discover() {
  (cd "$1" && eval "$DISCOVER") | grep -v -e '/testdata/' -e '^testdata/' -e '/vendor/' -e '^vendor/' \
    | sed -e 's#/\{0,1\}go\.mod$##' -e 's#^$#.#'
}

# lint_cycle DIR — the build agent's lint for every discovered module, as it is
# told to run it; prints each module and its exit.
lint_cycle() {
  local m
  while read -r m; do
    [ -n "$m" ] || continue
    echo "$m $(post build "go -C $m vet ./..." "$1")"
  done < <(discover "$1")
}

edit_payloads()   { jq -r 'select(.to == "edit") | .payload' "$BD/log.jsonl" 2>/dev/null; }
failing_rows()    { cat "$BD"/build-history.jsonl "$BD"/test-history.jsonl 2>/dev/null | jq -c 'select(.outcome == "failure")' | wc -l | tr -d ' '; }

# module DIR [bad] — a Go module at DIR; with "bad", one vet finding (Printf misuse).
module() {
  mkdir -p "$1"
  printf 'module example.com/%s\n\ngo 1.22\n' "$(basename "$1")" > "$1/go.mod"
  if [ "${2:-}" = bad ]; then
    printf 'package m\n\nimport "fmt"\n\nfunc Bad() { fmt.Printf("%%d\\n", "x") }\n' > "$1/m.go"
  else
    printf 'package m\n\nfunc Add(a, b int) int { return a + b }\n' > "$1/m.go"
  fi
}

commit_all() { git -C "$1" add -A && git -C "$1" -c user.email=t@t -c user.name=t commit -q -m base; }

echo "=== MUX-214: root go vet ==="
[ -n "$DISCOVER" ] && ok "the discovery command is read from agents/code-builder.md: $DISCOVER" \
  || fail "agents/code-builder.md names no discovery command — the lint step this test follows is gone"

# ── A: a clean build cycle ─────────────────────────────────────────
echo "-- A: a clean build cycle, modules under tools/"
R="$WORK/repo-a"
git init -q "$R"
module "$R/tools/alpha"
module "$R/tools/beta"
printf '#!/usr/bin/env bash\necho "Built 2 modules"\nexit 0\n' > "$R/build.sh"
chmod +x "$R/build.sh"
commit_all "$R"
new_session a
mods=$(discover "$R" | tr '\n' ' ')
[ "$mods" = "tools/alpha tools/beta " ] && ok "A: discovery lists the two modules and no root module ($mods)" \
  || fail "A: discovery = '$mods'"
lint=$(lint_cycle "$R")
[ "$(grep -c ' 0$' <<<"$lint")" = 2 ] && ok "A: each module vets clean ($(tr '\n' ';' <<<"$lint"))" \
  || fail "A: lint results: $lint"
[ "$(post build "./build.sh" "$R")" = 0 ] && ok "A: the build passes" || fail "A: build.sh failed"
[ "$(failing_rows)" = 0 ] && ok "A: no failing vet row in build or test history" || fail "A: $(failing_rows) failing row(s)"
[ ! -s "$BD/test-history.jsonl" ] && ok "A: the build cycle wrote no test-history row" || fail "A: test-history written"
grep -q "FAILED" <<<"$(edit_payloads)" && fail "A: edit was told something FAILED: $(edit_payloads)" \
  || ok "A: no FAILED notice reached edit"
jq -e 'select(.from == "build" and .to == "test" and .type == "request")' "$BD/log.jsonl" >/dev/null 2>&1 \
  && ok "A: the passing build still chained to the test agent" || fail "A: no build→test request"

# ── B: a real vet finding in a module ──────────────────────────────
echo "-- B: a real vet finding inside a module"
R="$WORK/repo-b"
git init -q "$R"
module "$R/tools/good"
module "$R/tools/broken" bad
commit_all "$R"
new_session b
lint=$(lint_cycle "$R")
grep -q '^tools/broken [1-9]' <<<"$lint" && ok "B: the broken module's vet fails ($(grep '^tools/broken' <<<"$lint"))" \
  || fail "B: lint results: $lint"
grep -q '^tools/good 0$' <<<"$lint" && ok "B: the clean module still vets clean" || fail "B: lint results: $lint"
jq -e 'select(.command == "go -C tools/broken vet ./..." and .outcome == "failure")' "$BD/build-history.jsonl" >/dev/null 2>&1 \
  && ok "B: build-history holds the failing row naming tools/broken" || fail "B: no failing build-history row for tools/broken"
note=$(edit_payloads | grep FAILED)
case "$note" in
  "Build FAILED (exit 1): go -C tools/broken vet ./..."*"check build window") ok "B: edit told, naming the module: $note" ;;
  *) fail "B: edit notice: ${note:-none}" ;;
esac
[ ! -s "$BD/test-history.jsonl" ] && ok "B: no test-history row from the build agent" || fail "B: test-history written"
grep -q "Tests FAILED" <<<"$(edit_payloads)" && fail "B: edit was told Tests FAILED" || ok "B: no 'Tests FAILED' for a build-pane vet"
grep -h 'chain:test:failure' "$MUXCODE_LIFECYCLE_LOG_DIR/$BUS_SESSION.log" 2>/dev/null | grep -q . \
  && fail "B: the workflow went test-failed mid-build" || ok "B: the workflow never went test-failed"

# ── C: an untracked module is discovered; ignored and testdata ones are not ──
echo "-- C: discovery — untracked, ignored and testdata modules"
R="$WORK/repo-c"
git init -q "$R"
module "$R/tools/tracked"
printf 'scratch/\n' > "$R/.gitignore"
commit_all "$R"
module "$R/tools/fresh"
module "$R/scratch/ignored" bad
module "$R/tools/tracked/testdata/fixture" bad
new_session c
[ -z "$(git -C "$R" ls-files tools/fresh)" ] && ok "C: tools/fresh is on disk but absent from the git index" \
  || fail "C: tools/fresh is tracked — the fixture is wrong"
mods=$(discover "$R")
grep -qx 'tools/fresh' <<<"$mods" && ok "C: the untracked module is discovered" || fail "C: discovery missed tools/fresh: $mods"
grep -q 'scratch/ignored' <<<"$mods" && fail "C: the ignored module was discovered" || ok "C: the ignored module is not discovered"
grep -q 'testdata' <<<"$mods" && fail "C: the testdata module was discovered" || ok "C: the testdata module is skipped"
lint=$(lint_cycle "$R")
grep -q '^tools/fresh 0$' <<<"$lint" && ok "C: the untracked module was vetted ($(tr '\n' ';' <<<"$lint"))" \
  || fail "C: lint results: $lint"
[ "$(failing_rows)" = 0 ] && ok "C: nothing failed — the bad ignored and testdata modules were never vetted" \
  || fail "C: $(failing_rows) failing row(s)"

# ── D: negative controls ───────────────────────────────────────────
echo "-- D: negative controls"
R="$WORK/repo-d"
git init -q "$R"
module "$R" bad
commit_all "$R"
new_session d
mods=$(discover "$R")
[ "$mods" = "." ] && ok "D: a root module is discovered as '.'" || fail "D: discovery = '$mods'"
lint=$(lint_cycle "$R")
grep -q '^\. [1-9]' <<<"$lint" && ok "D: the root module's vet fails on its finding" || fail "D: lint results: $lint"
note=$(edit_payloads | grep FAILED)
case "$note" in
  "Build FAILED (exit 1): go -C . vet ./..."*) ok "D: edit is still notified of a real finding: $note" ;;
  *) fail "D: edit notice: ${note:-none}" ;;
esac
new_session d2
[ "$(post test "go -C . vet ./..." "$R")" != 0 ] && ok "D: the test agent's vet fails on the same finding" || fail "D: test vet passed"
case "$(edit_payloads | grep FAILED)" in
  "Tests FAILED"*) ok "D: the test agent's vet failure is still 'Tests FAILED'" ;;
  *) fail "D: test agent's notice: $(edit_payloads)" ;;
esac

# ── E: a graph build node reads the build agent's vet row (MUX-218) ──
echo "-- E: a graph build node decided by the build agent's failing vet"
cat > "$WORK/build-node.json" <<'JSON'
{"name": "vet-build-node", "description": "MUX-218: one build node", "start": "build",
 "nodes": [{"id": "build", "type": "send", "role": "build", "action": "build", "message": "Run the lint step and report"}],
 "edges": []}
JSON

# graph_build_case BIN NAME REPLY — a fresh bus with its own BIN daemon runs the
# one-node graph; once the build request arrives the build agent's vet fails on
# repo-b's broken module, then REPLY answers the request. Sets NODE, the node's
# status file, and returns non-zero if the request or an outcome never came.
graph_build_case() {
  local bin="$1" rid="" i run
  MUX="$bin" new_session "$2"
  tmux new-session -d -s "$BUS_SESSION" -n edit 2>/dev/null
  "$bin" watch "$BUS_SESSION" --poll 1 >"$WORK/daemon-$2.log" 2>&1 &
  DPIDS="$DPIDS $!"
  run=$("$bin" graph run --file "$WORK/build-node.json" "mux218 $2" 2>&1 | grep -o 'Started run [^ ]*' | awk '{print $3}')
  NODE="$BD/graphs/$run/nodes/build.json"
  for i in $(seq 1 40); do
    rid=$(AGENT_ROLE=build "$bin" inbox --peek 2>/dev/null | grep -o -- '--reply-to [A-Za-z0-9-]*' | head -1 | awk '{print $2}')
    [ -n "$rid" ] && break
    sleep 0.5
  done
  [ -n "$rid" ] || return 1
  MUX="$bin" post build "go -C tools/broken vet ./..." "$WORK/repo-b" >/dev/null
  AGENT_ROLE=build "$bin" inbox >/dev/null 2>&1
  AGENT_ROLE=build "$bin" send edit response "$3" --type response --reply-to "$rid" >/dev/null 2>&1
  for i in $(seq 1 40); do
    [ -n "$(jq -r '.outcome // empty' "$NODE" 2>/dev/null)" ] && return 0
    sleep 0.5
  done
  return 1
}
outcome() { jq -r '.outcome // "none"' "$NODE" 2>/dev/null || echo none; }

if graph_build_case "$MUX" e1 "vet reported a finding in tools/broken"; then
  [ "$(outcome)" = failure ] && ok "E: with no sentinel the build node fails on the build agent's vet row" \
    || fail "E: no-sentinel build node outcome $(outcome), want failure"
else
  fail "E: the no-sentinel build node never dispatched or finished ($(tail -2 "$WORK/daemon-e1.log" 2>/dev/null))"
fi

if graph_build_case "$MUX" e2 "Build done. EXIT=0"; then
  [ "$(outcome)" = unknown ] && ok "E: EXIT=0 beside the failing vet row holds the node (outcome unknown)" \
    || fail "E: EXIT=0 build node outcome $(outcome), want unknown — the claim outranked the row"
else
  fail "E: the EXIT=0 build node never dispatched or finished ($(tail -2 "$WORK/daemon-e2.log" 2>/dev/null))"
fi
grep '"graph-outcome-conflict"' "$MUXCODE_LIFECYCLE_LOG_DIR/$BUS_SESSION.log" 2>/dev/null | grep -q 'go -C tools/broken vet' \
  && ok "E: graph-outcome-conflict names the failing vet row" \
  || fail "E: no graph-outcome-conflict row naming the vet command"
[ "$(tmux list-sessions -F '#S' 2>/dev/null | grep -c '^root-go-vet-e')" -ge 2 ] \
  && ok "E: the scratch sessions live on the private tmux server" \
  || fail "E: the private tmux server does not hold the scratch sessions — the isolation check below proves nothing"
tmux -S "$REAL_TMUX_SOCK" list-sessions -F '#S' 2>/dev/null | grep -q '^root-go-vet-' \
  && fail "E: a scratch session was created on the user's real tmux server" \
  || ok "E: the user's real tmux server holds no scratch session"

# Negative control: the same EXIT=0 case under a build of this tree whose
# executor classifies rows without the role — the pre-MUX-218 attribution.
CTRL_SRC="$WORK/ctrl-src"
cp -R "$REPO/tools/muxcode" "$CTRL_SRC"
perl -pi -e 's/got := StageForRole\(session, role, ClassifyCommand\(row\.Command\)\)/got := ClassifyCommand(row.Command)/' "$CTRL_SRC/bus/graph_exec.go"
if grep -q 'got := ClassifyCommand(row.Command)' "$CTRL_SRC/bus/graph_exec.go" \
  && ! grep -q 'got := StageForRole(session, role' "$CTRL_SRC/bus/graph_exec.go"; then
  ok "E control: role-aware attribution removed from a copy of the executor"
else
  fail "E control: the attribution patch did not apply — rowAttributesTo changed shape"
fi
if (cd "$CTRL_SRC" && go build -buildvcs=false -o "$WORK/bin/muxcode-ctrl" .) 2>"$WORK/ctrl-build.log"; then
  ok "E control: the pre-fix muxcode builds"
else
  fail "E control: building the pre-fix muxcode failed: $(tail -3 "$WORK/ctrl-build.log")"
fi
if [ -x "$WORK/bin/muxcode-ctrl" ] && graph_build_case "$WORK/bin/muxcode-ctrl" ectl "Build done. EXIT=0"; then
  [ "$(outcome)" = success ] && ok "E control: without role-aware attribution the EXIT=0 claim records success — the defect the check catches" \
    || fail "E control: pre-fix outcome $(outcome), want success — the check above may be passing for another reason"
else
  fail "E control: the pre-fix build node never dispatched or finished"
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
