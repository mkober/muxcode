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
#
# Usage: bash scripts/test-root-go-vet.sh
set -uo pipefail

PASS=0
FAIL=0
EXPECTED_PASS=26

command -v jq >/dev/null 2>&1 || { echo "SKIP: jq is required"; exit 2; }
command -v git >/dev/null 2>&1 || { echo "SKIP: git is required"; exit 2; }
command -v go >/dev/null 2>&1 || { echo "SKIP: go is required"; exit 2; }

REPO=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
WORK=$(mktemp -d /tmp/root-go-vet-XXXXXX)
trap 'rm -rf "$WORK"' EXIT
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

# ── Coverage floor ─────────────────────────────────────────────────
echo
echo "=== results: $PASS passed, $FAIL failed (floor $EXPECTED_PASS) ==="
if [ "$PASS" -ne "$EXPECTED_PASS" ]; then
  echo "  FAIL  coverage floor: expected exactly $EXPECTED_PASS passes, got $PASS — a section was skipped or double-counted"
  exit 1
fi
[ "$FAIL" -eq 0 ] || exit 1
exit 0
