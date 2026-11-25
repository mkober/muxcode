#!/usr/bin/env bash
# Integration test for MUX-157: build, test and review validate and never
# author — the PreToolUse guard refuses their source writes on the hook road,
# and their OpenCode config denies them.
#
# Hermetic: muxcode built from the tree (MUXCODE_ROLE_WRITE_GUARD_BIN
# overrides), a scratch HOME, bus, lifecycle log and git checkout, a stub
# `codex` on PATH so the codex hook road is eligible without the real CLI; no
# live session, no daemon, no tmux. Payloads go through the real
# `muxcode hook guard` with the scratch checkout as the working directory.
# Every probe records the guard's exit status, stdout and stderr (`probe`):
# an allow is exit 0 with no answer, a refusal exit 0 with the provider's
# deny shape, and anything else — a crash, a stray answer — fails the check.
#   0  harness control: a guard that exits non-zero with no output is read as
#      neither an allow nor a refusal
#   1  a Claude `Write` to a .go path as test, build and review → refused
#      naming the role, and a role-write-refused row for each
#   2  a Codex `apply_patch` from build naming a source file → the codex deny
#      answer, also when the source file is the patch's second path; a patch
#      to /tmp alone passes
#   3  recognised shell writers as test (`sed -i`, `gofmt -w`) into the tree →
#      refused; the same to /tmp, and `gofmt -l`, pass
#   4  the OpenCode config `muxcode agent config` writes for test carries the
#      path-aware edit rules and the shell-writer denies; build's re-allows bin/
#   5  negative controls: edit's Write, plan's docs/ Write, watch's and
#      analyze's Writes (Decision 1), build's bin/ Write (Phase 1's list) and
#      review's /tmp findings file pass, and no further role-write-refused
#      row appears
#   6  control: a muxcode built from a scratch copy whose CheckSourceWriteGuard
#      returns nil admits the test-role Write section 1 refuses
# Coverage floor: the exact pass count.
#
# Live section: `--live` or MUXCODE_ROLE_WRITE_GUARD_LIVE=1 with `opencode` on
# PATH — one real model call, outside the floor, skipped with a reason
# otherwise. A real OpenCode agent carrying the test role's generated
# permissions (its body replaced by a neutral probe definition at the project
# tier, so the model attempts both writes rather than declining by its role
# text) is asked to write bus/probe.go in the checkout and a file under /tmp
# with its file tool. Pass: opencode exits 0; the event stream holds a
# write tool part for bus/probe.go in error with "Permission denied: edit" and
# the file is absent; and a completed tool part for the /tmp file, which
# exists — the control that the tool works. The stream is judged by
# oc_tool_verdict, which section 0b holds to two controls: prose naming the
# path and the denial is no verdict, a real denial part is one. `opencode debug agents` is printed first: OpenCode's own reading
# of the rules. Runs with the caller's real HOME, where opencode keeps its
# credentials.
set -uo pipefail
export GOTOOLCHAIN=local
REAL_HOME="$HOME"
LIVE=0
[ "${1:-}" = "--live" ] || [ "${MUXCODE_ROLE_WRITE_GUARD_LIVE:-}" = 1 ] && LIVE=1

PASS=0
FAIL=0
LIVE_PASS=0
LIVE_FAIL=0
EXPECTED_PASS=29

for tool in jq go git python3; do
  command -v "$tool" >/dev/null 2>&1 || { echo "SKIP: $tool is required"; exit 2; }
done

REPO_ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
FIX="$REPO_ROOT/tools/muxcode/bus/testdata/codex-hooks"
WORK=$(mktemp -d /tmp/role-write-guard-XXXXXX)
trap 'cd /; rm -rf "$WORK"' EXIT
export GOCACHE="$(go env GOCACHE)" GOMODCACHE="$(go env GOMODCACHE)"

ok()   { PASS=$((PASS + 1)); echo "  ok: $1"; }
fail() { FAIL=$((FAIL + 1)); echo "  FAIL: $1"; }

# --- Build -----------------------------------------------------------------
mkdir -p "$WORK/bin"
if [ -n "${MUXCODE_ROLE_WRITE_GUARD_BIN:-}" ]; then
  MUX="$MUXCODE_ROLE_WRITE_GUARD_BIN"
else
  (cd "$REPO_ROOT/tools/muxcode" && go build -buildvcs=false -o "$WORK/bin/muxcode" .) \
    || { echo "SKIP: go build muxcode failed"; exit 2; }
  MUX="$WORK/bin/muxcode"
fi

# --- Isolation -------------------------------------------------------------
export HOME="$WORK/home" XDG_STATE_HOME="$WORK/home/.local/state" MUXCODE_BUS_BASE="$WORK/bus"
export MUXCODE_LIFECYCLE_LOG_DIR="$WORK/lifecycle" CODEX_HOME="$WORK/codex-home"
mkdir -p "$HOME/.config/muxcode" "$CODEX_HOME" "$MUXCODE_LIFECYCLE_LOG_DIR"
: >"$WORK/empty-config"
export MUXCODE_CONFIG="$WORK/empty-config" MUXCODE_CODEX_HOOKS=1 MUXCODE_DEDUP_WINDOW=0
export BUS_SESSION="rolewrite-$$"
unset MUXCODE_BUS_DIR MUXCODE_AGENT_CLI 2>/dev/null || true
printf '#!/usr/bin/env bash\necho "codex-cli 0.153.4"\n' >"$WORK/bin/codex"
chmod +x "$WORK/bin/codex"
export PATH="$WORK/bin:$PATH"
LOG="$MUXCODE_LIFECYCLE_LOG_DIR/$BUS_SESSION.log"

R="$WORK/repo"
mkdir -p "$R/bus"
git -C "$R" init -q
echo "package bus" >"$R/bus/x.go"

# probe BIN ROLE CLI PAYLOAD — run one PreToolUse event through BIN's guard in
# the scratch checkout; sets GRC (exit status), GOUT (stdout), GERR (stderr).
probe() {
  local up
  up=$(printf %s "$2" | tr a-z A-Z)
  GOUT=$(cd "$R" && printf '%s' "$4" | env AGENT_ROLE="$2" "MUXCODE_${up}_CLI=$3" "$1" hook guard 2>"$WORK/probe.err")
  GRC=$?
  GERR=$(head -c 300 "$WORK/probe.err")
}
# The three verdicts read the last probe; each requires a clean exit.
allowed() { [ "$GRC" = 0 ] && [ -z "$GOUT" ]; }
claude_refused() {
  [ "$GRC" = 0 ] && [ "$(jq -r '.decision // empty' <<<"$GOUT" 2>/dev/null)" = block ] && grep -q 'validates and never authors' <<<"$GOUT"
}
codex_denied() {
  [ "$GRC" = 0 ] && [ "$(jq -r '.hookSpecificOutput.permissionDecision // empty' <<<"$GOUT" 2>/dev/null)" = deny ] && grep -q 'never authors' <<<"$GOUT"
}
why() { echo "exit=$GRC stdout=${GOUT:-<empty>} stderr=${GERR:-<empty>}"; }

write_ev() { jq -nc --arg p "$1" '{hook_event_name:"PreToolUse",tool_name:"Write",tool_input:{file_path:$p,content:"x"}}'; }
bash_ev() { jq -nc --arg c "$1" '{hook_event_name:"PreToolUse",tool_name:"Bash",tool_input:{command:$c}}'; }
patch_ev() {
  local body="*** Begin Patch" f
  for f in "$@"; do body="$body"$'\n'"*** Update File: $f"$'\n'"+x"; done
  jq -c --arg c "$body"$'\n'"*** End Patch" --arg d "$R" '.tool_input.command=$c | .cwd=$d' "$FIX/pre-tool-use-apply-patch.json"
}
refused_rows() { local n; n=$(grep -c '"event":"role-write-refused"' "$LOG" 2>/dev/null); echo "${n:-0}"; }

# oc_tool_verdict JSONL SUFFIX — the verdict an `opencode run --format json`
# event stream records for a file-tool call whose input path ends in SUFFIX:
# "denied" (a write/edit tool part in error with "Permission denied: edit"),
# "completed", or "none". Only tool parts count — a model's prose naming the
# path, or declining to try, is never a verdict.
oc_tool_verdict() {
  python3 - "$1" "$2" <<'PY'
import json, sys
path, suffix = sys.argv[1], sys.argv[2]
verdict = "none"
def walk(o):
    global verdict
    if isinstance(o, dict):
        st = o.get("state")
        if o.get("tool") in ("write", "edit", "patch") and isinstance(st, dict):
            inp = st.get("input") or {}
            p = str(inp.get("filePath") or inp.get("path") or inp.get("file_path") or "")
            if p.endswith(suffix):
                if st.get("status") == "error" and "Permission denied: edit" in str(st.get("error", "")):
                    verdict = "denied"
                elif st.get("status") == "completed" and verdict == "none":
                    verdict = "completed"
        for v in o.values():
            walk(v)
    elif isinstance(o, list):
        for v in o:
            walk(v)
for line in open(path, errors="ignore"):
    try:
        walk(json.loads(line))
    except ValueError:
        pass
print(verdict)
PY
}

echo "=== role write guard integration test (MUX-157) ==="

# --- 0b. Evaluator controls (the live section's judge) --------------------------
echo "-- 0b: the OpenCode event-stream evaluator"
printf '%s\n' '{"type":"text","part":{"type":"text","text":"I will not write bus/probe.go: Permission denied: edit — as a test agent I never author."}}' >"$WORK/prose.jsonl"
[ "$(oc_tool_verdict "$WORK/prose.jsonl" bus/probe.go)" = none ] \
  && ok "0b: prose naming the path and the denial is no verdict" || fail "0b: prose read as $(oc_tool_verdict "$WORK/prose.jsonl" bus/probe.go)"
printf '%s\n' '{"type":"tool_use","part":{"type":"tool","tool":"write","state":{"status":"error","input":{"path":"/x/live/bus/probe.go"},"error":"Permission denied: edit"}}}' >"$WORK/denied.jsonl"
[ "$(oc_tool_verdict "$WORK/denied.jsonl" bus/probe.go)" = denied ] \
  && ok "0b: a write tool part in error with Permission denied: edit is a denial" || fail "0b: denial fixture read as $(oc_tool_verdict "$WORK/denied.jsonl" bus/probe.go)"

# --- 0. Harness control ---------------------------------------------------------
echo "-- 0: a crashing guard is not read as a verdict"
printf '#!/usr/bin/env bash\necho "panic: boom" >&2\nexit 2\n' >"$WORK/crash-guard"
chmod +x "$WORK/crash-guard"
probe "$WORK/crash-guard" test claude "$(write_ev bus/x.go)"
! allowed && ! claude_refused && ok "0: exit 2 with no output is neither an allow nor a refusal ($(why))" \
  || fail "0: the verdicts accepted a crash: $(why)"

# --- 1. File tools on the hook road (Claude) --------------------------------
echo "-- 1: a Write to source as each validation role"
for role in test build review; do
  probe "$MUX" "$role" claude "$(write_ev bus/x.go)"
  claude_refused && grep -q "the $role agent" <<<"$GOUT" && ok "1: $role Write bus/x.go refused naming the role" \
    || fail "1: $role Write: $(why)"
  grep '"event":"role-write-refused"' "$LOG" 2>/dev/null | grep -q "$role: Write bus/x.go" \
    && ok "1: a role-write-refused row names $role, Write and bus/x.go" || fail "1: no role-write-refused row for $role"
done

# --- 2. Codex apply_patch ----------------------------------------------------
echo "-- 2: a Codex apply_patch from build"
# The codex hook road is active per role once agent config has written
# hooks.json and its activation marker, as a launch does.
(cd "$R" && env AGENT_ROLE=build MUXCODE_BUILD_CLI=codex "$MUX" agent config build >/dev/null 2>&1)
probe "$MUX" build codex "$(patch_ev "$R/bus/x.go")"
codex_denied && ok "2: a patch to a source file gets the codex deny answer" || fail "2: patch: $(why)"
probe "$MUX" build codex "$(patch_ev /tmp/role-write-notes.txt "$R/bus/x.go")"
codex_denied && ok "2: a source file patched second is denied too" || fail "2: second path: $(why)"
probe "$MUX" build codex "$(patch_ev /tmp/role-write-notes.txt)"
allowed && ok "2: a patch to /tmp alone passes" || fail "2: /tmp patch: $(why)"

# --- 3. Shell writers --------------------------------------------------------
echo "-- 3: recognised shell writers as test"
probe "$MUX" test claude "$(bash_ev "sed -i 's/a/b/' bus/x.go")"
claude_refused && ok "3: sed -i into the tree refused" || fail "3: sed -i: $(why)"
probe "$MUX" test claude "$(bash_ev 'gofmt -w bus/x.go')"
claude_refused && ok "3: gofmt -w into the tree refused" || fail "3: gofmt -w: $(why)"
probe "$MUX" test claude "$(bash_ev "sed -i 's/a/b/' /tmp/role-write-notes.txt")"
allowed && ok "3: sed -i on a /tmp file passes" || fail "3: /tmp sed -i: $(why)"
probe "$MUX" test claude "$(bash_ev 'gofmt -l .')"
allowed && ok "3: gofmt -l (check mode) passes" || fail "3: gofmt -l: $(why)"

# --- 4. OpenCode config -------------------------------------------------------
echo "-- 4: the OpenCode config for test and build"
for role in test build; do
  up=$(printf %s "$role" | tr a-z A-Z)
  (cd "$R" && env AGENT_ROLE="$role" "MUXCODE_${up}_CLI=opencode" "$MUX" agent config "$role" >/dev/null 2>&1)
done
TCFG=$(grep -l 'gofmt -w' "$R"/.opencode/agents/*test*.md 2>/dev/null | head -1)
BCFG=$(ls "$R"/.opencode/agents/*build*.md 2>/dev/null | head -1)
if [ -n "$TCFG" ] && python3 - "$TCFG" <<'PY'
import sys
s = open(sys.argv[1]).read()
e = s.index("  edit:\n")
d, a = s.index('"*": deny', e), s.index('"/*": allow', e)
sys.exit(0 if e < d < a else 1)
PY
then ok "4: test's edit rules deny the checkout, then re-allow outside it"; else fail "4: test config edit rules: ${TCFG:-no config written}"; fi
missing=""
for p in 'sed -i*' 'perl -pi*' 'gofmt -w*' '* --fix*' '* --write*'; do
  grep -qF "\"$p\": deny" "${TCFG:-/dev/null}" || missing="$missing $p"
done
[ -z "$missing" ] && ok "4: test's config denies the shell writers" || fail "4: test's config lacks:$missing"
[ -n "$TCFG" ] && ! grep -qF '"bin/*": allow' "$TCFG" && ok "4: test's config re-allows no bin/" || fail "4: test's config re-allows bin/"
grep -qF '"bin/*": allow' "${BCFG:-/dev/null}" && ok "4: build's config re-allows its bin/" || fail "4: build config: ${BCFG:-none}"

# --- 5. Negative controls -----------------------------------------------------
echo "-- 5: writes the guard leaves alone"
rows_before=$(refused_rows)
for c in "edit:tools/x.go" "plan:docs/requirements/backlog/x.md" "watch:bus/x.go" "analyze:bus/x.go" "build:bin/muxcode" "review:/tmp/muxcode-review-1.txt"; do
  role="${c%%:*}" path="${c#*:}"
  probe "$MUX" "$role" claude "$(write_ev "$path")"
  allowed && ok "5: $role Write $path passes" || fail "5: $role Write $path: $(why)"
done
[ "$(refused_rows)" = "$rows_before" ] && ok "5: the controls wrote no role-write-refused row" \
  || fail "5: role-write-refused rows grew from $rows_before to $(refused_rows)"

# --- 6. Control: the guard removed --------------------------------------------
echo "-- 6: a build with CheckSourceWriteGuard returning nil"
mkdir -p "$WORK/ctl"
cp -R "$REPO_ROOT/tools/muxcode" "$WORK/ctl/"
python3 - "$WORK/ctl/muxcode/bus/source_write_guard.go" <<'PY'
import sys
p = sys.argv[1]
s = open(p).read()
sig = "func CheckSourceWriteGuard(role, p string) *GuardDecision {"
n = s.replace(sig, sig + "\n\treturn nil\n}\n\nfunc checkSourceWriteGuardRemoved(role, p string) *GuardDecision {", 1)
open(p, "w").write(n)
sys.exit(0 if n != s else 1)
PY
patched=$?
if [ "$patched" = 0 ] && (cd "$WORK/ctl/muxcode" && go build -buildvcs=false -o "$WORK/ctl-muxcode" .); then
  ok "6: control binary built with the guard removed"
  probe "$WORK/ctl-muxcode" test claude "$(write_ev bus/x.go)"
  allowed && ok "6: the control admits, exit 0, the test-role Write section 1 refused" \
    || fail "6: the control did not admit it — the first test may not be exercising CheckSourceWriteGuard: $(why)"
else
  fail "6: could not patch and build the control (patched=$patched)"
fi

# --- Live: a real OpenCode agent ------------------------------------------------
lok()   { LIVE_PASS=$((LIVE_PASS + 1)); echo "  ok (live): $1"; }
lfail() { LIVE_FAIL=$((LIVE_FAIL + 1)); echo "  FAIL (live): $1"; }
if [ "$LIVE" != 1 ]; then
  echo "-- live: skipped (pass --live or MUXCODE_ROLE_WRITE_GUARD_LIVE=1)"
elif ! command -v opencode >/dev/null 2>&1; then
  echo "-- live: skipped (opencode is not on PATH)"
else
  echo "-- live: a real OpenCode agent with the test role's permissions"
  L="$WORK/live"
  OUTSIDE="$WORK/outside"
  mkdir -p "$L/bus" "$L/.claude/agents" "$OUTSIDE"
  git -C "$L" init -q
  printf -- '---\ndescription: MUX-157 permissions probe\n---\nYou are a permissions probe. Do exactly what the user asks, using the tools the user names, and report each tool result verbatim, including any error or refusal.\n' \
    >"$L/.claude/agents/test-runner.md"
  (cd "$L" && env AGENT_ROLE=test MUXCODE_TEST_CLI=opencode "$MUX" agent config test >/dev/null 2>&1)
  CFG=$(grep -l '"/\*": allow' "$L"/.opencode/agents/*.md 2>/dev/null | head -1)
  if [ -z "$CFG" ]; then
    lfail "agent config wrote no OpenCode agent with the edit rules"
  else
    AGENT=$(basename "$CFG" .md)
    echo "  agent $AGENT from $CFG"
    (cd "$L" && env -u XDG_STATE_HOME -u MUXCODE_BUS_BASE HOME="$REAL_HOME" opencode debug agents --standalone 2>&1) \
      | grep -n -A40 "$AGENT" | grep -n -i -B1 -A2 'edit\|probe' | head -40 | sed 's/^/    debug agents: /'
    PROMPT="Permissions probe. Use your file write tool, not bash, for both steps. Step 1: create the file bus/probe.go containing the single line: package bus. Step 2: create the file $OUTSIDE/probe.txt containing the single line: ok. Attempt both even if one fails, then report each tool result verbatim."
    (cd "$L" && env -u XDG_STATE_HOME -u MUXCODE_BUS_BASE HOME="$REAL_HOME" \
      perl -e 'alarm shift; exec @ARGV' 300 opencode run --standalone --archived --agent "$AGENT" --format json "$PROMPT") \
      >"$WORK/live.jsonl" 2>"$WORK/live.err"
    LRC=$?
    echo "  opencode run exit $LRC — $(wc -l <"$WORK/live.jsonl" | tr -d ' ') event lines"
    grep -o '.\{0,160\}bus/probe.go.\{0,200\}' "$WORK/live.jsonl" | head -4 | sed 's/^/    event: /'
    [ "$LRC" = 0 ] && lok "opencode run exited 0" || lfail "opencode run exited $LRC — $(tail -c 400 "$WORK/live.err")"
    inside=$(oc_tool_verdict "$WORK/live.jsonl" bus/probe.go)
    outside=$(oc_tool_verdict "$WORK/live.jsonl" "$(basename "$OUTSIDE")/probe.txt")
    [ "$outside" = completed ] && [ -f "$OUTSIDE/probe.txt" ] \
      && lok "the outside write's tool part completed and the file landed — the tool works for this agent" \
      || lfail "control: the outside write is '$outside', file present=$([ -f "$OUTSIDE/probe.txt" ] && echo yes || echo no)"
    [ "$inside" = denied ] && [ ! -e "$L/bus/probe.go" ] \
      && lok "the bus/probe.go write tool part failed with Permission denied: edit and wrote nothing" \
      || lfail "the in-checkout write is '$inside', file present=$([ -e "$L/bus/probe.go" ] && echo yes || echo no) — none means the model never called the tool, so nothing is proven"
  fi
  echo "  live: $LIVE_PASS passed, $LIVE_FAIL failed"
fi

# --- Coverage floor ------------------------------------------------------------
echo
echo "=== results: $PASS passed, $FAIL failed (floor $EXPECTED_PASS) ==="
if [ "$PASS" -ne "$EXPECTED_PASS" ]; then
  echo "  FAIL  coverage floor: expected exactly $EXPECTED_PASS passes, got $PASS — a section was skipped or double-counted"
  exit 1
fi
[ "$FAIL" -eq 0 ] && [ "$LIVE_FAIL" -eq 0 ] || exit 1
exit 0
