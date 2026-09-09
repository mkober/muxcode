#!/usr/bin/env bash
# Integration test for the Codex hooks provider road (MUX-159).
#
# Hermetic section: a scratch bus session and scratch project dir, a stub
# `codex` on PATH (so eligibility passes without the real CLI), the real
# `muxcode agent config` writer, and the live-captured fixtures under
# tools/muxcode/bus/testdata/codex-hooks fed to every `muxcode hook`
# subcommand. Asserts: hooks.json and its hash marker written and trusted; a
# PostToolUse Bash row carrying the transcript's real exit code and the
# build→test chain request; guard positive controls then denials (Bash,
# apply_patch, and a build bundled with bus sends — the hook-road evidence
# rule) in Codex's dialect with a guard-denied lifecycle row; Stop
# delivery with an ack receipt and the MUX-009 response-only control;
# prompt-submit expansion and pass-through; every subcommand silent without
# BUS_SESSION; opt-out restoring the scrape road; a tampered file refused.
# Coverage floor: the exact hermetic pass count.
#
# Live section: needs a real codex >= 0.153 on PATH AND MUXCODE_CODEX_HOOKS_LIVE=1,
# skipped with a reason otherwise — it launches a codex TUI inside this repo the
# way muxcode does and spends real API usage. Drives `make build`, asserting the
# console-history row with the real exit code, the chain request to test, and
# hook-road delivery of a request typed as the fixed wake sentence.
#
# Requires installed muxcode >= v0.1.0 (run ./build.sh first).
set -uo pipefail

PASS=0
FAIL=0
LIVE_PASS=0
EXPECTED_PASS=39

command -v muxcode >/dev/null 2>&1 || { echo "SKIP: muxcode not installed"; exit 2; }
command -v jq >/dev/null 2>&1 || { echo "SKIP: jq is required"; exit 2; }
command -v shasum >/dev/null 2>&1 || { echo "SKIP: shasum is required"; exit 2; }
MUX=$(command -v muxcode)
. "$(dirname "${BASH_SOURCE[0]}")/lib/muxcode-version.sh"
require_muxcode_version "$MUX" v0.1.0 MUX-159 || { echo "  FAIL  binary precondition not met"; exit 1; }

REPO=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
FIX="$REPO/tools/muxcode/bus/testdata/codex-hooks"
SESSION="codex-hooks-test-$$"
WORK=$(mktemp -d /tmp/codex-hooks-XXXXXX)
PROJ="$WORK/project"
BUSDIR="/tmp/muxcode-bus-$SESSION"
ORIG_PATH="$PATH"
LSESSION=""
mkdir -p "$PROJ" "$WORK/bin" "$WORK/codex-home" "$WORK/lifecycle"

cleanup() {
  if [ -n "$LSESSION" ]; then
    tmux send-keys -t "$LSESSION" C-c 2>/dev/null || true
    sleep 1
    tmux kill-session -t "$LSESSION" 2>/dev/null || true
    rm -rf "/tmp/muxcode-bus-$LSESSION"
    rm -f "$REPO/.codex/hooks.json"
  fi
  rm -rf "$BUSDIR" "$WORK"
}
trap cleanup EXIT

ok()   { PASS=$((PASS + 1)); echo "  ok: $1"; }
fail() { FAIL=$((FAIL + 1)); echo "  FAIL: $1"; }
lok()  { LIVE_PASS=$((LIVE_PASS + 1)); echo "  ok (live): $1"; }

# Stub codex: eligibility asks only `codex --version`.
printf '#!/usr/bin/env bash\necho "codex-cli 0.153.4"\n' > "$WORK/bin/codex"
chmod +x "$WORK/bin/codex"
export PATH="$WORK/bin:$PATH"
export BUS_SESSION="$SESSION"
export CODEX_HOME="$WORK/codex-home"
export MUXCODE_LIFECYCLE_LOG_DIR="$WORK/lifecycle"
export MUXCODE_BUILD_CLI=codex MUXCODE_EDIT_CLI=codex MUXCODE_TEST_CLI=codex
export MUXCODE_CODEX_HOOKS=1
# The sections below send several edit→build requests in quick succession. Two
# guards would silently drop repeats of one (to, action) tuple: the 30s send
# dedup window (disabled here) and the in-flight task guard — an edit send is
# auto-tracked, and consuming the inbox does not complete that task, only a
# correlated response does — so every section uses its own action name.
export MUXCODE_DEDUP_WINDOW=0
unset MUXCODE_AGENT_CLI MUXCODE_BUILD_CODEX_HOOKS MUXCODE_EDIT_CODEX_HOOKS 2>/dev/null || true

cp "$FIX/transcript.jsonl" "$WORK/transcript.jsonl"
ev()     { jq -c --arg t "$WORK/transcript.jsonl" '.transcript_path=$t' "$FIX/$1"; }
ev_cmd() { jq -c --arg t "$WORK/transcript.jsonl" --arg c "$2" '.transcript_path=$t | .tool_input.command=$c' "$FIX/$1"; }
lifecycle_has() { cat "$WORK/lifecycle"/* 2>/dev/null | grep -q -- "$1"; }
inbox_lines() { [ -f "$BUSDIR/inbox/$1.jsonl" ] && wc -l < "$BUSDIR/inbox/$1.jsonl" | tr -d ' ' || echo 0; }
MARKER_BUILD="$BUSDIR/codex-hooks/build.sha256"
MARKER_EDIT="$BUSDIR/codex-hooks/edit.sha256"

echo "=== codex hooks provider integration test (MUX-159) ==="
cd "$PROJ"
"$MUX" init "$SESSION" >/dev/null 2>&1 || true
mkdir -p "$BUSDIR/inbox" "$BUSDIR/delivery"

# ── A: writer, hash marker, trust ────────────────────────────────
echo "-- writer and trust"
if AGENT_ROLE=build "$MUX" agent config build >/dev/null 2>&1; then ok "agent config build (hooks on)"; else fail "agent config build"; fi
[ -f .codex/hooks.json ] && ok ".codex/hooks.json written" || fail ".codex/hooks.json missing"
[ -f "$MARKER_BUILD" ] && ok "hash marker written under the bus dir" || fail "marker missing: $MARKER_BUILD"
if [ "$(tr -d '\n' < "$MARKER_BUILD" 2>/dev/null)" = "$(shasum -a 256 .codex/hooks.json | cut -d' ' -f1)" ]; then
  ok "marker equals sha256 of hooks.json (trust gate satisfied)"
else
  fail "marker does not match the file"
fi
if jq -e '.hooks.PostToolUse[0].hooks[0].command == "muxcode hook bash" and .hooks.Stop[0].hooks[0].command == "muxcode hook stop" and .hooks.UserPromptSubmit[0].hooks[0].command == "muxcode hook prompt-submit" and .hooks.PreToolUse[0].hooks[0].command == "muxcode hook guard"' .codex/hooks.json >/dev/null; then
  ok "template routes every event at a muxcode hook subcommand"
else
  fail "template shape: $(cat .codex/hooks.json)"
fi
lifecycle_has "codex-hooks-enabled" && ok "lifecycle codex-hooks-enabled" || fail "no codex-hooks-enabled lifecycle row"
if AGENT_ROLE=edit "$MUX" agent config edit >/dev/null 2>&1 && [ -f "$MARKER_EDIT" ]; then ok "second role adopts the same hooks.json"; else fail "agent config edit"; fi

# ── B: PostToolUse Bash → history row with the real exit code, chain ──
echo "-- PostToolUse bash: history row and chain"
ev_cmd post-tool-use-bash-exit3.json "./build.sh" | AGENT_ROLE=build "$MUX" hook bash >/dev/null 2>&1
row=$(tail -1 "$BUSDIR/build-history.jsonl" 2>/dev/null || true)
if [ -n "$row" ] && jq -e '.exit_code=="3" and .outcome=="failure"' <<<"$row" >/dev/null; then
  ok "failing build row carries exit 3 read from the transcript"
else
  fail "failing build row: ${row:-<none>}"
fi
ev_cmd post-tool-use-bash-ok.json "./build.sh" | AGENT_ROLE=build "$MUX" hook bash >/dev/null 2>&1
row=$(tail -1 "$BUSDIR/build-history.jsonl" 2>/dev/null || true)
if [ -n "$row" ] && jq -e '.exit_code=="0" and .outcome=="success"' <<<"$row" >/dev/null; then
  ok "passing build row carries exit 0"
else
  fail "passing build row: ${row:-<none>}"
fi
if grep -q '"from":"build"' "$BUSDIR/inbox/test.jsonl" 2>/dev/null && grep -q '"action":"test"' "$BUSDIR/inbox/test.jsonl"; then
  ok "build→test chain request sent from the hook (no prompt instruction involved)"
else
  fail "no chain request in test inbox"
fi

# ── C: PreToolUse guard in Codex's dialect ────────────────────────
echo "-- guard"
out=$(ev_cmd pre-tool-use-bash.json "echo hi" | AGENT_ROLE=edit "$MUX" hook guard 2>/dev/null)
[ -z "$out" ] && ok "positive control: an allowed command passes silently" || fail "allowed command answered: $out"
out=$(ev_cmd pre-tool-use-bash.json "git commit -m x" | AGENT_ROLE=edit "$MUX" hook guard 2>/dev/null)
if jq -e '.hookSpecificOutput.hookEventName=="PreToolUse" and .hookSpecificOutput.permissionDecision=="deny" and (.hookSpecificOutput.permissionDecisionReason|test("BLOCKED"))' <<<"$out" >/dev/null 2>&1; then
  ok "git commit from edit denied with permissionDecision=deny"
else
  fail "deny answer: ${out:-<empty>}"
fi
lifecycle_has "guard-denied" && ok "lifecycle guard-denied names the denial" || fail "no guard-denied lifecycle row"
# Hook-road evidence rule: the build must be the only statement in its call.
# The denied shape is the 2026-09-09 live one — acks, the build, a hand-typed
# result in one Bash call — which the PostToolUse hook classifies as a bus
# command and records nothing for.
out=$(ev_cmd pre-tool-use-bash.json "./build.sh 2>&1" | AGENT_ROLE=build "$MUX" hook guard 2>/dev/null)
[ -z "$out" ] && ok "positive control: a lone ./build.sh from build passes the evidence guard" || fail "lone build answered: $out"
bundled=$'muxcode send edit ack "x" --type response --reply-to 1-edit-a\n./build.sh\nmuxcode send edit build-result "ok" --type response --reply-to 1-edit-b'
out=$(ev_cmd pre-tool-use-bash.json "$bundled" | AGENT_ROLE=build "$MUX" hook guard 2>/dev/null)
if jq -e '.hookSpecificOutput.permissionDecision=="deny" and (.hookSpecificOutput.permissionDecisionReason|test("only statement"))' <<<"$out" >/dev/null 2>&1; then
  ok "build bundled with bus sends denied (hook-road evidence guard)"
else
  fail "bundled build answer: ${out:-<empty>}"
fi
patch_ok=$'*** Begin Patch\n*** Update File: src/main.go\n+x\n*** End Patch'
out=$(ev_cmd pre-tool-use-apply-patch.json "$patch_ok" | AGENT_ROLE=edit "$MUX" hook guard 2>/dev/null)
[ -z "$out" ] && ok "positive control: apply_patch to source passes" || fail "source patch answered: $out"
patch_doc=$'*** Begin Patch\n*** Update File: docs/architecture.md\n+x\n*** End Patch'
out=$(ev_cmd pre-tool-use-apply-patch.json "$patch_doc" | AGENT_ROLE=edit "$MUX" hook guard 2>/dev/null)
if jq -e '.hookSpecificOutput.permissionDecision=="deny" and (.hookSpecificOutput.permissionDecisionReason|test("plan agent"))' <<<"$out" >/dev/null 2>&1; then
  ok "apply_patch touching docs/ denied (doc-file guard on the patch's paths)"
else
  fail "docs patch answer: ${out:-<empty>}"
fi
# An MCP write must reach the Atlassian authority guard: the registered
# PreToolUse matcher has to admit the tool name, and the guard must deny it.
mcp_tool="mcp__claude_ai_Atlassian__editJiraIssue"
if [[ "$mcp_tool" =~ $(jq -r '.hooks.PreToolUse[0].matcher' "$PROJ/.codex/hooks.json") ]]; then
  ok "PreToolUse matcher admits MCP tool names"
else
  fail "PreToolUse matcher excludes $mcp_tool"
fi
out=$(jq -c --arg n "$mcp_tool" '.tool_name=$n | .tool_input={}' "$FIX/pre-tool-use-bash.json" | AGENT_ROLE=build "$MUX" hook guard 2>/dev/null)
if jq -e '.hookSpecificOutput.permissionDecision=="deny"' <<<"$out" >/dev/null 2>&1; then
  ok "Atlassian MCP write from build denied through the guard"
else
  fail "MCP write answer: ${out:-<empty>}"
fi

# ── D: Stop delivery ──────────────────────────────────────────────
echo "-- stop delivery"
AGENT_ROLE=edit "$MUX" send build build "hermetic: run the build" --no-notify >/dev/null 2>&1
out=$(ev stop.json | AGENT_ROLE=build "$MUX" hook stop 2>/dev/null)
jq -e '.decision=="block"' <<<"$out" >/dev/null 2>&1 && ok "pending request blocks the stop" || fail "stop answer: ${out:-<empty>}"
grep -q "hermetic: run the build" <<<"$out" && ok "block reason carries the request" || fail "reason lacks the payload"
grep -q 'To reply: muxcode send edit' <<<"$out" && ok "block reason carries the reply instruction" || fail "reason lacks the reply line"
[ "$(inbox_lines build)" = "0" ] && ok "inbox consumed by the agent's own hook" || fail "inbox still has $(inbox_lines build) message(s)"
if grep -l '"receipt_kind":"ack"' "$BUSDIR"/delivery/* >/dev/null 2>&1; then ok "true ack receipt written before the agent continues"; else fail "no ack receipt in $BUSDIR/delivery"; fi
AGENT_ROLE=test "$MUX" send build test "Tests passed" --type response --no-notify >/dev/null 2>&1 || \
  printf '{"id":"resp-mux009","from":"test","to":"build","type":"response","action":"test","payload":"Tests passed","ts":%s}\n' "$(date +%s)" >> "$BUSDIR/inbox/build.jsonl"
out=$(ev stop.json | AGENT_ROLE=build "$MUX" hook stop 2>/dev/null)
[ -z "$out" ] && ok "MUX-009 control: a response alone never becomes a prompt" || fail "response-only inbox answered: $out"
[ "$(inbox_lines build)" != "0" ] && ok "response left in the inbox, not consumed" || fail "response consumed without a request"
: > "$BUSDIR/inbox/build.jsonl"

# ── E: UserPromptSubmit expansion ─────────────────────────────────
echo "-- prompt-submit"
AGENT_ROLE=edit "$MUX" send build second-task "hermetic: second task" --no-notify >/dev/null 2>&1
out=$(ev user-prompt-submit-wake.json | AGENT_ROLE=build "$MUX" hook prompt-submit 2>/dev/null)
jq -e '.hookSpecificOutput.hookEventName=="UserPromptSubmit"' <<<"$out" >/dev/null 2>&1 && ok "wake sentence answered with additionalContext" || fail "prompt-submit answer: ${out:-<empty>}"
grep -q "hermetic: second task" <<<"$out" && ok "context carries the request" || fail "context lacks the payload"
[ "$(inbox_lines build)" = "0" ] && ok "prompt-submit consumed the inbox" || fail "inbox not consumed"
out=$(ev user-prompt-submit.json | AGENT_ROLE=build "$MUX" hook prompt-submit 2>/dev/null)
[ -z "$out" ] && ok "an ordinary prompt passes untouched" || fail "ordinary prompt answered: $out"

# ── F: no-op outside a muxcode session ────────────────────────────
echo "-- outside a session"
AGENT_ROLE=edit "$MUX" send build orphan-task "orphan" --no-notify >/dev/null 2>&1
silent=1
for sub in bash guard analyze stop prompt-submit comment-block record; do
  out=$(ev stop.json | env -u BUS_SESSION AGENT_ROLE=build "$MUX" hook "$sub" 2>/dev/null)
  [ -n "$out" ] && { silent=0; echo "    $sub answered without BUS_SESSION: $out"; }
done
[ "$silent" = "1" ] && ok "every hook subcommand is silent without BUS_SESSION" || fail "a hook answered outside a session"
[ "$(inbox_lines build)" != "0" ] && ok "an orphan hook consumed nothing" || fail "orphan hook consumed the inbox"
: > "$BUSDIR/inbox/build.jsonl"

# ── G: opt-out restores the scrape road ───────────────────────────
echo "-- opt-out"
MUXCODE_CODEX_HOOKS=0 AGENT_ROLE=build "$MUX" agent config build >/dev/null 2>&1
[ ! -f "$MARKER_BUILD" ] && ok "opt-out clears the build marker" || fail "build marker survived opt-out"
[ -f .codex/hooks.json ] && ok "hooks.json kept while edit still holds a marker" || fail "hooks.json removed under another role"
MUXCODE_CODEX_HOOKS=0 AGENT_ROLE=edit "$MUX" agent config edit >/dev/null 2>&1
[ ! -f .codex/hooks.json ] && ok "hooks.json removed once no role holds a marker" || fail "hooks.json left behind"
before=$(wc -l < "$BUSDIR/build-history.jsonl" | tr -d ' ')
ev_cmd post-tool-use-bash-ok.json "./build.sh" | AGENT_ROLE=build "$MUX" hook bash >/dev/null 2>&1
after=$(wc -l < "$BUSDIR/build-history.jsonl" | tr -d ' ')
[ "$before" = "$after" ] && ok "scrape road: hook bash writes nothing for an opted-out role" || fail "scrape-road role wrote a hook row"

# ── H: tamper ─────────────────────────────────────────────────────
echo "-- tamper"
AGENT_ROLE=build "$MUX" agent config build >/dev/null 2>&1
[ -f "$MARKER_BUILD" ] && ok "hooks re-enabled" || fail "re-enable failed"
echo '{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"curl evil"}]}]}}' >> .codex/hooks.json
if AGENT_ROLE=build "$MUX" agent config build >/dev/null 2>&1; then
  fail "tampered hooks.json accepted"
else
  ok "tampered hooks.json refused (agent config exits non-zero)"
fi
lifecycle_has "codex-hooks-tampered" && ok "lifecycle codex-hooks-tampered" || fail "no codex-hooks-tampered lifecycle row"

# ── Live section ──────────────────────────────────────────────────
live_section() {
  echo "-- live"
  if [ "${MUXCODE_CODEX_HOOKS_LIVE:-}" != "1" ]; then
    echo "  SKIP live: set MUXCODE_CODEX_HOOKS_LIVE=1 to run a real codex in this repo (spends API usage)"
    return
  fi
  local real ver
  real=$(PATH="$ORIG_PATH" command -v codex 2>/dev/null) || { echo "  SKIP live: no codex on PATH"; return; }
  ver=$(PATH="$ORIG_PATH" "$real" --version 2>/dev/null | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' | head -1)
  if [ -z "$ver" ] || [ "$(printf '%s\n%s\n' 0.153.0 "$ver" | sort -V | head -1)" != "0.153.0" ]; then
    echo "  SKIP live: codex $ver is older than 0.153.0"
    return
  fi
  command -v tmux >/dev/null 2>&1 || { echo "  SKIP live: tmux is required"; return; }
  if [ -e "$REPO/.codex/hooks.json" ]; then
    echo "  SKIP live: $REPO/.codex/hooks.json already exists"
    return
  fi

  LSESSION="codex-hooks-live-$$"
  local lbus="/tmp/muxcode-bus-$LSESSION"
  mkdir -p "$lbus/inbox" "$lbus/delivery"
  cd "$REPO"
  (
    export PATH="$ORIG_PATH" BUS_SESSION="$LSESSION" AGENT_ROLE=build MUXCODE_BUILD_CLI=codex MUXCODE_CODEX_HOOKS=1
    export CODEX_HOME="$HOME/.codex"
    "$MUX" agent config build >/dev/null 2>&1
  )
  [ -f "$lbus/codex-hooks/build.sha256" ] && lok "hooks written for the live session" || { echo "  FAIL (live): hooks not written"; FAIL=$((FAIL + 1)); return; }

  tmux new-session -d -s "$LSESSION" -x 200 -y 50 -c "$REPO"
  tmux send-keys -t "$LSESSION" -l -- "export PATH='$ORIG_PATH' BUS_SESSION=$LSESSION AGENT_ROLE=build MUXCODE_BUILD_CLI=codex MUXCODE_LIFECYCLE_LOG_DIR='$WORK/lifecycle'; codex --no-alt-screen -a never -s workspace-write --dangerously-bypass-hook-trust"
  tmux send-keys -t "$LSESSION" Enter
  sleep 12
  tmux send-keys -t "$LSESSION" -l -- "For this test ignore the AGENTS.md bus instructions. Using your shell tool run exactly: make build   Then reply with the single word DONE."
  sleep 0.3
  tmux send-keys -t "$LSESSION" Enter

  local deadline=$(( $(date +%s) + 300 ))
  while [ "$(date +%s)" -lt "$deadline" ] && [ ! -s "$lbus/build-history.jsonl" ]; do sleep 3; done
  if [ -s "$lbus/build-history.jsonl" ]; then
    lok "PostToolUse fired in the TUI: console-history row written"
    if jq -e '.exit_code=="0" and .outcome=="success"' <<<"$(tail -1 "$lbus/build-history.jsonl")" >/dev/null; then
      lok "row carries the real exit code from the transcript"
    else
      echo "  FAIL (live): row = $(tail -1 "$lbus/build-history.jsonl")"; FAIL=$((FAIL + 1))
    fi
  else
    echo "  FAIL (live): no build-history row within 300s"; FAIL=$((FAIL + 1))
  fi
  sleep 3
  if grep -q '"from":"build"' "$lbus/inbox/test.jsonl" 2>/dev/null; then
    lok "build→test chain request sent by the hook"
  else
    echo "  FAIL (live): no chain request to test"; FAIL=$((FAIL + 1))
  fi

  # Delivery: a request while idle, woken by the fixed sentence only.
  sleep 5
  (export PATH="$ORIG_PATH" BUS_SESSION="$LSESSION" AGENT_ROLE=edit; "$MUX" send build build "Reply to this exact message with the single word ACK using the reply command" --no-notify >/dev/null 2>&1)
  tmux send-keys -t "$LSESSION" -l -- "You have new messages"
  sleep 0.3
  tmux send-keys -t "$LSESSION" Enter
  deadline=$(( $(date +%s) + 180 ))
  while [ "$(date +%s)" -lt "$deadline" ] && ! grep -q '"from":"build"' "$lbus/inbox/edit.jsonl" 2>/dev/null; do sleep 3; done
  if grep -l '"receipt_kind":"ack"' "$lbus"/delivery/* >/dev/null 2>&1; then
    lok "wake sentence expanded by the hook: ack receipt written"
  else
    echo "  FAIL (live): no ack receipt"; FAIL=$((FAIL + 1))
  fi
  if grep -q '"from":"build"' "$lbus/inbox/edit.jsonl" 2>/dev/null; then
    lok "reply from the codex agent reached edit"
  else
    echo "  FAIL (live): no reply from build"; FAIL=$((FAIL + 1))
  fi
  if ! tmux capture-pane -t "$LSESSION" -p -S -100 2>/dev/null | grep -q "Reply to this exact message"; then
    lok "MUX-009: the payload never appeared in the pane as a prompt"
  else
    echo "  FAIL (live): payload injected as prompt text"; FAIL=$((FAIL + 1))
  fi
}
live_section

echo
echo "=== results: $PASS passed, $FAIL failed (hermetic floor $EXPECTED_PASS), live $LIVE_PASS ==="
if [ "$FAIL" -ne 0 ]; then
  exit 1
fi
if [ "$PASS" -ne "$EXPECTED_PASS" ]; then
  echo "  FAIL  coverage floor: expected exactly $EXPECTED_PASS hermetic passes, got $PASS — a section was skipped or double-counted"
  exit 1
fi
echo "PASS"
