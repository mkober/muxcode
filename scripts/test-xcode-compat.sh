#!/usr/bin/env bash
# Integration test for MUX-207: Xcode and Swift projects. Detection must find a
# Swift package and an XcodeGen spec nested below the repo root, `hook bash`
# must classify Swift and Xcode build and test commands into history rows and
# chains, and session-start init must create the three project files without
# ever overwriting one.
#
# Hermetic section: muxcode built from this tree (MUXCODE_XCODE_COMPAT_BIN names
# a binary instead), a scratch bus session, scratch HOME, chain config and
# lifecycle log, and scratch git repos. Asserts:
#   detection       — `context detect` lists packages/Kit/Package.swift and
#                     apps/ios/project.yml, not the copies under .build/ and
#                     DerivedData/; negative control: a non-XcodeGen
#                     project.yml detects nothing
#   classification  — Claude payloads through the real `hook bash`: swift build
#                     (cd-prefixed) writes a build row and requests test; swift
#                     test and xcodebuild test write test rows and request
#                     review; a bare scripts/test.sh writes a test row; a passing
#                     xcodegen generate (build precheck) writes no row and fires
#                     nothing, a failing one records exit 1 and notifies edit;
#                     negative controls: swift --version and xcodebuild -list
#                     write no row and fire nothing
#   live matcher    — the live section's codex-log parser counts only the bare
#                     command: an echo, `|| true` and a chain do not match, and
#                     the bare call keeps its own exit code
#   session init    — `context init`, the launcher's own road
#                     (InitSessionProject): creates .muxcode/config,
#                     .claude/settings.json and CLAUDE.md under a home config
#                     (layered, not withheld), content checked and the config
#                     sourced back; the files stay untracked; a project-init
#                     lifecycle row and one edit notice; a re-run changes nothing
#                     and sends no notice; a deleted file alone is re-created;
#                     negative controls: a fully configured project is
#                     byte-identical, MUXCODE_CONFIG withholds only the config,
#                     and nothing detected writes nothing
# Coverage floor: the exact hermetic pass count.
#
# Live section: `--live` or MUXCODE_XCODE_COMPAT_LIVE=1 with `codex` and `swift`
# on PATH — per role, one real `codex exec` under the flags muxcode launches
# that role with (`agent sandbox-args`, resolved from the user's own HOME and
# config and checked against `agent sandbox-fingerprint`): build runs
# `swift build`, test runs `swift test`, each of which must appear as its own
# command in codex's --json log with exit 0, and then classifies through
# `hook bash`. Six live passes, outside the floor; skipped with a reason
# otherwise.
set -uo pipefail

PASS=0
FAIL=0
LIVE_PASS=0
EXPECTED_PASS=24

command -v jq >/dev/null 2>&1 || { echo "SKIP: jq is required"; exit 2; }
command -v git >/dev/null 2>&1 || { echo "SKIP: git is required"; exit 2; }

LIVE=0
if [ "${1:-}" = --live ] || [ "${MUXCODE_XCODE_COMPAT_LIVE:-}" = 1 ]; then
  LIVE=1
fi

REPO=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
SESSION="xcode-compat-$$"
BUSDIR="/tmp/muxcode-bus-$SESSION"
WORK=$(mktemp -d /tmp/xcode-compat-XXXXXX)
REAL_HOME=$HOME
# The user's own config and install tree, captured before the scratch setup
# unsets them, for real_launch. A tree-built binary resolves no install tree
# (resolveInstallDir), so without this its live sandbox args would lack the
# installed BASH_ENV shim; this mirrors resolveInstallDir for the muxcode on PATH.
REAL_MUXCODE_CONFIG=${MUXCODE_CONFIG:-}
REAL_INSTALL_DIR=${MUXCODE_INSTALL_DIR:-}
if [ -z "$REAL_INSTALL_DIR" ]; then
  case $(dirname "$(command -v muxcode 2>/dev/null)") in
    */.local/bin) REAL_INSTALL_DIR="$REAL_HOME/.config/muxcode" ;;
  esac
fi

cleanup() { rm -rf "$BUSDIR" "$WORK"; }
trap cleanup EXIT

if [ -n "${MUXCODE_XCODE_COMPAT_BIN:-}" ]; then
  MUX=$MUXCODE_XCODE_COMPAT_BIN
else
  command -v go >/dev/null 2>&1 || { echo "SKIP: go is required to build muxcode from the tree"; exit 2; }
  mkdir -p "$WORK/bin"
  (cd "$REPO/tools/muxcode" && go build -buildvcs=false -o "$WORK/bin/muxcode" .) \
    || { echo "SKIP: go build muxcode failed"; exit 2; }
  MUX="$WORK/bin/muxcode"
fi

ok()   { PASS=$((PASS + 1)); echo "  ok: $1"; }
fail() { FAIL=$((FAIL + 1)); echo "  FAIL: $1"; }
lok()  { LIVE_PASS=$((LIVE_PASS + 1)); echo "  ok (live): $1"; }

mkdir -p "$WORK/home" "$WORK/config" "$WORK/lifecycle"
export HOME="$WORK/home"
export BUS_SESSION="$SESSION"
export MUXCODE_CONFIG_DIR="$WORK/config"
export MUXCODE_LIFECYCLE_LOG_DIR="$WORK/lifecycle"
export MUXCODE_DEDUP_WINDOW=0
export MUXCODE_BUILD_CLI=claude MUXCODE_TEST_CLI=claude
unset MUXCODE_CONFIG MUXCODE_AGENT_CLI MUXCODE_BUILD_PATTERNS MUXCODE_BUILD_PRECHECK_PATTERNS \
  MUXCODE_TEST_PATTERNS MUXCODE_TEST_PRECHECK_PATTERNS 2>/dev/null || true
cd "$WORK" || exit 1

# post ROLE COMMAND — a Claude PostToolUse Bash success into the real hook.
post() {
  jq -nc --arg c "$2" \
    '{hook_event_name:"PostToolUse",tool_name:"Bash",tool_use_id:"toolu_mux207",tool_input:{command:$c},
      tool_response:{stdout:"ok",stderr:"",interrupted:false,isImage:false}}' \
    | AGENT_ROLE="$1" "$MUX" hook bash >/dev/null 2>&1
}

# post_fail ROLE COMMAND ERROR — a Claude PostToolUseFailure Bash event.
post_fail() {
  jq -nc --arg c "$2" --arg e "$3" \
    '{hook_event_name:"PostToolUseFailure",tool_name:"Bash",tool_use_id:"toolu_mux207f",tool_input:{command:$c},error:$e,is_interrupt:false}' \
    | AGENT_ROLE="$1" "$MUX" hook bash >/dev/null 2>&1
}

# row_for HISTORY COMMAND — the last row whose command is exactly COMMAND.
row_for() { jq -c --arg n "$2" 'select(.command == $n)' "$BUSDIR/$1" 2>/dev/null | tail -1; }

# inbox_count ROLE TYPE ACTION NEEDLE... — messages in ROLE's inbox of TYPE and
# ACTION whose payload contains every NEEDLE.
inbox_count() {
  local role=$1 type=$2 action=$3
  shift 3
  [ -f "$BUSDIR/inbox/$role.jsonl" ] || { echo 0; return; }
  jq -c --arg t "$type" --arg a "$action" --args \
    'select(.type == $t and .action == $a and (.payload as $p | $ARGS.positional | all(. as $n | $p | contains($n))))' \
    "$@" <"$BUSDIR/inbox/$role.jsonl" 2>/dev/null | wc -l | tr -d ' '
}

# lifecycle_count EVENT NEEDLE... — `lifecycle show --event EVENT` lines holding every NEEDLE.
lifecycle_count() {
  local event=$1
  shift
  "$MUX" lifecycle show "$SESSION" --event "$event" 2>/dev/null \
    | jq -R --args 'select(. as $l | $ARGS.positional | all(. as $n | $l | contains($n)))' "$@" \
    | wc -l | tr -d ' '
}

# real_launch CMD... — CMD under the user's own HOME, config, install tree and
# environment, as a launched agent resolves them, not the scratch bus's.
real_launch() {
  local -a real=()
  [ -n "$REAL_MUXCODE_CONFIG" ] && real+=("MUXCODE_CONFIG=$REAL_MUXCODE_CONFIG")
  [ -n "$REAL_INSTALL_DIR" ] && real+=("MUXCODE_INSTALL_DIR=$REAL_INSTALL_DIR")
  env -u BUS_SESSION -u AGENT_ROLE -u TMUX -u TMUX_PANE -u MUXCODE_CONFIG_DIR -u MUXCODE_LIFECYCLE_LOG_DIR \
    -u MUXCODE_BUILD_CLI -u MUXCODE_TEST_CLI HOME="$REAL_HOME" ${real[@]+"${real[@]}"} "$@"
}

# shell_execs JSONL — "<exit>\t<command>" per finished command in a codex
# --json log, unwrapped from its `bash -lc` shell but otherwise verbatim, so an
# echo or a chained command never reads as the bare command.
read -r -d '' SHELL_EXECS_JQ <<'JQ'
.. | objects | select(has("command") and has("exit_code") and .exit_code != null)
| (.command
   | if type == "array" then
       (if length == 3 and (.[0] | test("(^|/)(ba|z)?sh$")) and (.[1] | test("^-l?c$")) then .[2] else join(" ") end)
     else tostring end
   | sub("^(/usr)?(/bin/)?(ba|z)?sh -l?c "; "")
   | sub("^'(?<x>.*)'$"; "\(.x)")
   | sub("^\"(?<x>.*)\"$"; "\(.x)")) as $c
| "\(.exit_code)\t\($c)"
JQ
shell_execs() { jq -r "$SHELL_EXECS_JQ" "$1" 2>/dev/null; }

# live_role ROLE COMMAND — one real `codex exec` under ROLE's launch flags
# (`agent sandbox-args`, checked against `agent sandbox-fingerprint` so they are
# the flags a launch stamps) asked to run exactly COMMAND in the scratch
# package. `-a` is dropped because exec never asks for approval, which is what
# build's and test's `-a never` says. Three live passes: the flags match the
# fingerprint, COMMAND ran on its own with exit 0, and it classifies.
live_role() {
  local role=$1 want=$2 upper a skip=0 joined fp log status execs rc hist row
  local -a launch=() exec_args=()
  upper=$(tr '[:lower:]' '[:upper:]' <<<"$role")
  while IFS= read -r a; do launch+=("$a"); done < <(real_launch "$MUX" agent sandbox-args "$role")
  joined=$(for i in "${!launch[@]}"; do
             [ "$i" -gt 0 ] && printf '\0'
             printf '%s' "${launch[$i]}"
           done | shasum -a 256 | cut -d' ' -f1)
  fp=$(real_launch env "MUXCODE_${upper}_CLI=codex" "$MUX" agent sandbox-fingerprint "$role" | cut -f2)
  if [ "${#launch[@]}" -gt 0 ] && [ "$joined" = "$fp" ]; then
    lok "$role: launch flags '${launch[*]}' hash to its launch fingerprint"
  else
    fail "live $role: sandbox-args '${launch[*]:-}' hash $joined, fingerprint '${fp:-<none>}'"
    return
  fi
  for a in "${launch[@]}"; do
    if [ "$skip" = 1 ]; then skip=0; continue; fi
    case "$a" in
      -a) skip=1 ;;
      --search) ;;
      *) exec_args+=("$a") ;;
    esac
  done
  log="$WORK/codex-$role"
  (cd "$PKG" && real_launch codex exec --skip-git-repo-check ${exec_args[@]+"${exec_args[@]}"} ${model[@]+"${model[@]}"} \
    --json -o "$log.last" \
    "Run exactly the shell command \`$want\` in the current directory, once, as a command of its own with nothing before or after it. Do not edit any file. Then reply with one line: EXIT=<its exit code>." \
    >"$log.jsonl" 2>"$log.err" </dev/null)
  status=$?
  execs=$(shell_execs "$log.jsonl")
  rc=$(awk -F'\t' -v w="$want" '$2 == w { rc = $1 } END { print rc }' <<<"$execs")
  if [ "$status" = 0 ] && [ "$rc" = 0 ]; then
    lok "$role: codex ran exactly '$want' on its own, exit 0"
  else
    fail "live $role: codex exit $status, exact '$want' exit '${rc:-<not run>}'; commands: $(tr '\n' ';' <<<"$execs" | head -c 300); stderr: $(tail -c 300 "$log.err")"
    return
  fi
  "$MUX" workflow reset >/dev/null 2>&1
  post "$role" "$want"
  hist=build-history.jsonl
  [ "$role" = test ] && hist=test-history.jsonl
  row=$(row_for "$hist" "$want")
  if jq -e '.exit_code == "0"' <<<"$row" >/dev/null 2>&1; then
    lok "$role: '$want' classifies, $hist row at exit 0"
  else
    fail "live $role: '$want' wrote no $hist row"
  fi
}

# snapshot DIR — every file and symlink under DIR (.git excluded) with its checksum.
snapshot() {
  (cd "$1" && find . -path ./.git -prune -o \( -type f -o -type l \) -print | LC_ALL=C sort \
    | while read -r f; do
        if [ -L "$f" ]; then echo "link $f -> $(readlink "$f")"; else echo "file $f $(shasum "$f" | cut -d' ' -f1)"; fi
      done)
}

XCODEGEN_SPEC=$'name: App\ntargets:\n  App:\n    type: application\n    platform: iOS\n'

# swift_package DIR — a buildable SwiftPM package with one XCTest.
swift_package() {
  mkdir -p "$1/Sources/Kit" "$1/Tests/KitTests"
  cat >"$1/Package.swift" <<'EOF'
// swift-tools-version:5.9
import PackageDescription

let package = Package(
    name: "Kit",
    targets: [
        .target(name: "Kit"),
        .testTarget(name: "KitTests", dependencies: ["Kit"]),
    ]
)
EOF
  printf 'public func answer() -> Int { 42 }\n' >"$1/Sources/Kit/Kit.swift"
  printf 'import XCTest\n@testable import Kit\n\nfinal class KitTests: XCTestCase {\n    func testAnswer() { XCTAssertEqual(answer(), 42) }\n}\n' \
    >"$1/Tests/KitTests/KitTests.swift"
}

APP="$WORK/app"
mkdir -p "$APP/apps/ios" "$APP/.build/checkouts/Dep" "$APP/vendor/DerivedData/x"
swift_package "$APP/packages/Kit"
printf '%s' "$XCODEGEN_SPEC" >"$APP/apps/ios/project.yml"
printf '// swift-tools-version:5.9\n' >"$APP/.build/checkouts/Dep/Package.swift"
printf '%s' "$XCODEGEN_SPEC" >"$APP/vendor/DerivedData/x/project.yml"
git -C "$APP" init -q

echo "=== Xcode and Swift compatibility integration test (MUX-207) ==="
"$MUX" init >/dev/null 2>&1 || true

# ── Detection ─────────────────────────────────────────────────────
echo "-- detection: nested Package.swift and XcodeGen spec"
detect=$("$MUX" context detect "$APP" 2>&1)
line=$(grep -E '^swift ' <<<"$detect")
if grep -q 'packages/Kit/Package.swift' <<<"$line" && grep -q 'apps/ios/project.yml' <<<"$line"; then
  ok "swift detected with packages/Kit/Package.swift and apps/ios/project.yml"
else
  fail "context detect did not list both nested markers: $detect"
fi
if ! grep -qE '\.build/|DerivedData/' <<<"$detect"; then
  ok "the copies under .build/ and DerivedData/ are skipped"
else
  fail "excluded directories leaked into detection: $detect"
fi
CI="$WORK/ci"
mkdir -p "$CI"
printf 'version: 2\njobs:\n  build: {}\n' >"$CI/project.yml"
detect=$("$MUX" context detect "$CI" 2>&1)
if grep -q 'No project types detected' <<<"$detect"; then
  ok "negative control: a non-XcodeGen project.yml detects nothing"
else
  fail "a CI project.yml was detected: $detect"
fi

# ── Classification ────────────────────────────────────────────────
SB="cd packages/Kit && swift build"
echo "-- classification: swift build on the build agent"
post build "$SB"
row=$(row_for build-history.jsonl "$SB")
tests=$(inbox_count test request test "Build succeeded (" "swift build")
if jq -e '.exit_code == "0" and .outcome == "success"' <<<"$row" >/dev/null 2>&1 && [ "$tests" = 1 ]; then
  ok "swift build: build row exit 0 and one test request"
else
  fail "swift build: row ${row:-<none>}, $tests test request(s); want exit 0 and 1"
fi

ST="cd packages/Kit && swift test"
echo "-- classification: swift test and xcodebuild test on the test agent"
post test "$ST"
row=$(row_for test-history.jsonl "$ST")
reviews=$(inbox_count review request review "Tests passed (" "swift test")
if jq -e '.exit_code == "0" and .outcome == "success"' <<<"$row" >/dev/null 2>&1 && [ "$reviews" = 1 ]; then
  ok "swift test: test row exit 0 and one review request"
else
  fail "swift test: row ${row:-<none>}, $reviews review request(s); want exit 0 and 1"
fi
"$MUX" workflow reset >/dev/null 2>&1
XT="xcodebuild test -scheme App -destination 'platform=iOS Simulator,name=iPhone 16'"
post test "$XT"
row=$(row_for test-history.jsonl "$XT")
reviews=$(inbox_count review request review "Tests passed (" "xcodebuild test")
if jq -e '.exit_code == "0"' <<<"$row" >/dev/null 2>&1 && [ "$reviews" = 1 ]; then
  ok "xcodebuild test: test row exit 0 and one review request"
else
  fail "xcodebuild test: row ${row:-<none>}, $reviews review request(s); want exit 0 and 1"
fi
"$MUX" workflow reset >/dev/null 2>&1
post test "scripts/test.sh"
row=$(row_for test-history.jsonl "scripts/test.sh")
if jq -e '.exit_code == "0"' <<<"$row" >/dev/null 2>&1; then
  ok "a bare scripts/test.sh is classified as a test, like its bash form"
else
  fail "bare scripts/test.sh wrote no test row"
fi

echo "-- classification: xcodegen generate is a build precheck"
"$MUX" workflow reset >/dev/null 2>&1
XG="cd apps/ios && xcodegen generate"
before=$(inbox_count test request test)
post build "$XG"
row=$(row_for build-history.jsonl "$XG")
after=$(inbox_count test request test)
if [ -z "$row" ] && [ "$after" = "$before" ]; then
  ok "a passing xcodegen generate writes no row and requests no test"
else
  fail "passing xcodegen generate: row ${row:-<none>}, test requests $before -> $after; want no row and no request"
fi
post_fail build "$XG" $'Exit code 1\nerror: spec is invalid'
row=$(row_for build-history.jsonl "$XG")
notices=$(inbox_count edit event notify "Build FAILED (exit 1)" "xcodegen generate")
if jq -e '.exit_code == "1" and .outcome == "failure"' <<<"$row" >/dev/null 2>&1 && [ "$notices" = 1 ]; then
  ok "a failing xcodegen generate records exit 1 and notifies edit"
else
  fail "failing xcodegen generate: row ${row:-<none>}, $notices edit notice(s); want exit 1 and 1"
fi

echo "-- classification, negative controls: non-build Swift and Xcode calls"
"$MUX" workflow reset >/dev/null 2>&1
before=$(inbox_count test request test)
post build "swift --version"
row=$(row_for build-history.jsonl "swift --version")
after=$(inbox_count test request test)
if [ -z "$row" ] && [ "$after" = "$before" ]; then
  ok "swift --version writes no build row and requests no test"
else
  fail "swift --version was classified: row ${row:-<none>}, test requests $before -> $after"
fi
before=$(inbox_count review request review)
post test "xcodebuild -list"
row=$(row_for test-history.jsonl "xcodebuild -list")
after=$(inbox_count review request review)
if [ -z "$row" ] && [ "$after" = "$before" ]; then
  ok "xcodebuild -list writes no test row and requests no review"
else
  fail "xcodebuild -list was classified: row ${row:-<none>}, review requests $before -> $after"
fi

echo "-- live matcher: only the bare command counts as running it"
cat >"$WORK/execs.jsonl" <<'EOF'
{"type":"item.completed","item":{"type":"command_execution","command":"/bin/bash -lc 'echo swift build'","exit_code":0}}
{"type":"item.completed","item":{"type":"command_execution","command":"/bin/bash -lc 'swift build || true'","exit_code":0}}
{"type":"item.completed","item":{"type":"command_execution","command":["/bin/bash","-lc","swift test && swift build"],"exit_code":0}}
{"type":"item.started","item":{"type":"command_execution","command":"/bin/bash -lc 'swift build'","exit_code":null}}
{"type":"item.completed","item":{"type":"command_execution","command":["/bin/bash","-lc","swift build"],"exit_code":7}}
EOF
matched=$(shell_execs "$WORK/execs.jsonl" | awk -F'\t' '$2 == "swift build" { print $1 }' | tr '\n' ' ')
if [ "$matched" = "7 " ]; then
  ok "an echo, a masked or a chained command is not swift build; the bare one keeps its own exit code"
else
  fail "shell_execs matched swift build with exit codes '$matched', want only '7 '"
fi

# ── Session-start init ────────────────────────────────────────────
echo "-- session init: a detected project under a home config"
mkdir -p "$HOME/.config/muxcode"
printf 'MUXCODE_XCODE_COMPAT_HOME=1\n' >"$HOME/.config/muxcode/config"
notices_before=$(inbox_count edit request project-init)
out=$("$MUX" context init "$APP" 2>&1)
if grep -qx 'created .muxcode/config' <<<"$out" && grep -qx 'created .claude/settings.json' <<<"$out" \
  && grep -qx 'created CLAUDE.md' <<<"$out" && ! grep -q '^skipped' <<<"$out"; then
  ok "context init created all three files, the config layered over the home config"
else
  fail "context init output: $out"
fi
cfg="$APP/.muxcode/config"
sourced=$(env -i bash -c ". '$cfg' && printf '%s' \"\$MUXCODE_TEST_PATTERNS\"" 2>/dev/null)
if grep -q 'swift test' <<<"$sourced" && grep -q '^# Detected: swift$' "$cfg" && ! grep -q 'packages/Kit' "$cfg"; then
  ok ".muxcode/config sources back with swift test, names the type and no repository path"
else
  fail ".muxcode/config content wrong (sourced MUXCODE_TEST_PATTERNS='$sourced'): $(head -c 400 "$cfg" 2>/dev/null)"
fi
settings="$APP/.claude/settings.json"
if jq -e '.permissions.allow | (index("Bash(swift --version*)") != null) and (index("Bash(xcrun simctl list*)") != null)
          and (map(select(test("^Bash\\((swift build|swift test|xcodebuild)"))) | length == 0)' "$settings" >/dev/null 2>&1; then
  ok ".claude/settings.json allows read-only toolchain queries and no build or test"
else
  fail ".claude/settings.json content wrong: $(cat "$settings" 2>/dev/null)"
fi
if grep -q 'swift build' "$APP/CLAUDE.md" 2>/dev/null && grep -q 'swift test' "$APP/CLAUDE.md" && grep -q 'xcodegen generate' "$APP/CLAUDE.md"; then
  ok "the CLAUDE.md stub names swift build, swift test and xcodegen generate"
else
  fail "CLAUDE.md stub: $(head -c 400 "$APP/CLAUDE.md" 2>/dev/null)"
fi
tracked=$(git -C "$APP" ls-files -- .muxcode .claude CLAUDE.md)
untracked=$(git -C "$APP" status --porcelain -uall -- .muxcode/config .claude/settings.json CLAUDE.md | grep -c '^?? ')
if [ -z "$tracked" ] && [ "$untracked" = 3 ]; then
  ok "the three files are untracked"
else
  fail "git state: tracked '$tracked', $untracked untracked; want none tracked and 3 untracked"
fi
rows=$(lifecycle_count project-init "created" ".muxcode/config" ".claude/settings.json" "CLAUDE.md" "(untracked)")
if [ "$rows" = 1 ]; then ok "one project-init lifecycle row names the three files"; else fail "project-init lifecycle rows: $rows, want 1"; fi
notices=$(inbox_count edit request project-init ".muxcode/config" ".claude/settings.json" "CLAUDE.md" "never commit them yourself")
if [ "$notices" = 1 ] && [ "$notices_before" = 0 ]; then
  ok "edit got one project-init notice listing the files"
else
  fail "edit project-init notices: $notices (before $notices_before), want 1"
fi

echo "-- session init: never overwrites"
printf '\nUser note kept.\n' >>"$APP/CLAUDE.md"
snap=$(snapshot "$APP")
out=$("$MUX" context init "$APP" 2>&1)
notices=$(inbox_count edit request project-init)
if [ "$out" = "nothing to create" ] && [ "$(snapshot "$APP")" = "$snap" ] && [ "$notices" = 1 ]; then
  ok "a re-run creates nothing, changes no byte and sends no notice"
else
  fail "re-run: output '$out', $notices notice(s), snapshot changed: $([ "$(snapshot "$APP")" = "$snap" ] && echo no || echo yes)"
fi
rm "$settings"
keep=$(snapshot "$APP")
out=$("$MUX" context init "$APP" 2>&1)
if [ "$out" = "created .claude/settings.json" ] && [ -f "$settings" ] \
  && [ "$(snapshot "$APP" | grep -v 'settings.json')" = "$keep" ]; then
  ok "a deleted settings.json alone is re-created; every other file is untouched"
else
  fail "partial fill: output '$out'"
fi

echo "-- session init, negative control: a fully configured project"
FULL="$WORK/full"
swift_package "$FULL"
mkdir -p "$FULL/.muxcode" "$FULL/.claude"
printf "MUXCODE_BUILD_PATTERNS='make app'\n" >"$FULL/.muxcode/config"
printf '{"permissions":{"allow":["Bash(make *)"]}}\n' >"$FULL/.claude/settings.json"
printf '# Full\n\nHand-written.\n' >"$FULL/CLAUDE.md"
snap=$(snapshot "$FULL")
notices_before=$(inbox_count edit request project-init)
out=$("$MUX" context init "$FULL" 2>&1)
if [ "$out" = "nothing to create" ] && [ "$(snapshot "$FULL")" = "$snap" ] \
  && [ "$(inbox_count edit request project-init)" = "$notices_before" ]; then
  ok "a project with all three files is byte-identical after init, with no notice"
else
  fail "fully configured project: output '$out'"
fi

echo "-- session init: MUXCODE_CONFIG withholds only the config"
EXPL="$WORK/explicit"
swift_package "$EXPL"
printf 'MUXCODE_XCODE_COMPAT_EXPLICIT=1\n' >"$WORK/explicit-config"
out=$(MUXCODE_CONFIG="$WORK/explicit-config" "$MUX" context init "$EXPL" 2>&1)
skips=$(lifecycle_count project-init-skipped ".muxcode/config" "MUXCODE_CONFIG names")
if grep -q '^skipped .muxcode/config: MUXCODE_CONFIG names' <<<"$out" && grep -qx 'created .claude/settings.json' <<<"$out" \
  && grep -qx 'created CLAUDE.md' <<<"$out" && [ ! -e "$EXPL/.muxcode/config" ] && [ "$skips" = 1 ]; then
  ok "under MUXCODE_CONFIG the config is skipped and logged, the other two created"
else
  fail "MUXCODE_CONFIG: output '$out', $skips project-init-skipped row(s)"
fi

echo "-- session init, negative control: nothing detected"
EMPTY="$WORK/empty"
mkdir -p "$EMPTY"
printf 'notes\n' >"$EMPTY/README"
out=$("$MUX" context init "$EMPTY" 2>&1)
if [ "$out" = "nothing to create" ] && [ "$(find "$EMPTY" -mindepth 1 | wc -l | tr -d ' ')" = 1 ]; then
  ok "an undetected project gets no files"
else
  fail "undetected project: output '$out', contents $(find "$EMPTY" -mindepth 1 | tr '\n' ' ')"
fi

# ── Live: a real Codex agent builds and tests the scratch package ─
if [ "$LIVE" != 1 ]; then
  echo "-- live section skipped (pass --live or set MUXCODE_XCODE_COMPAT_LIVE=1; spends a real model call)"
elif ! command -v codex >/dev/null 2>&1 || ! command -v swift >/dev/null 2>&1; then
  echo "-- live: skipped (codex and swift must both be on PATH)"
else
  echo "-- live: codex exec under the build and test roles' own launch flags"
  [ -n "$REAL_INSTALL_DIR" ] || echo "   note: no installed muxcode found, so the launch flags carry no BASH_ENV shim"
  PKG="$APP/packages/Kit"
  model=()
  [ -n "${MUXCODE_XCODE_COMPAT_LIVE_MODEL:-}" ] && model=(-m "$MUXCODE_XCODE_COMPAT_LIVE_MODEL")
  live_role build "swift build"
  live_role test "swift test"
fi

echo
echo "=== results: $PASS passed, $FAIL failed (floor $EXPECTED_PASS), $LIVE_PASS live ==="
if [ "$FAIL" -gt 0 ]; then
  exit 1
fi
if [ "$PASS" -ne "$EXPECTED_PASS" ]; then
  echo "  FAIL  coverage floor: expected exactly $EXPECTED_PASS passes, got $PASS — a section was skipped or double-counted"
  exit 1
fi
exit 0
