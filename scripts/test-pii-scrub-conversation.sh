#!/usr/bin/env bash
# Integration test for MUX-203: a PII-sensitive role's own conversation is
# scrubbed before its model reads it, by the road each provider allows, with
# the guard refusing raw environment dumps beneath them.
#
# Hermetic section: a scratch bus session and project dir driven through the
# installed binary, a stub `codex` on PATH so the hook road is eligible without
# the real CLI, and the real `muxcode agent config` writers. Asserts:
#   guard floor  — a run agent's bare `ps eww`, a piped `printenv NAME` and a
#                  pipe into `pii-scrub --role build` are
#                  denied naming the remedy; the scrubbed pipe, an ordinary
#                  `ps -ef`, and a build agent's bare `ps eww` pass (controls)
#   Claude       — `muxcode hook scrub` answers with the result redacted and
#                  its other fields kept; silent for a build role, a clean
#                  result, and PostToolUseFailure (the recorded residual gap)
#   Codex        — the guard rewrites a run agent's Bash call; the command it
#                  returns, executed as Codex would, prints redacted output and
#                  keeps the command's exit code and inner pipeline status; a
#                  missing scrubber withholds the output and exits 125; history
#                  records the original command; a build agent is not wrapped
#   OpenCode     — `agent config` writes the plugin; run under node it redacts
#                  a watch agent's result, leaves a build agent's alone, and
#                  withholds the result when muxcode is missing
#   pii-scrub    — `--role` redacts for a sensitive role, echoes otherwise, and
#                  fails with no role named rather than echo
# The `muxcode agent` model copy is not driven here: its loop needs an
# Ollama endpoint, and these scripts bind no sockets. It is pinned by
# TestProcessMessages_ModelCopyScrubbed and TestExecute_* instead.
# Coverage floor: the exact hermetic pass count.
#
# Live section: MUXCODE_PII_CONVERSATION_LIVE=1 with `claude` and/or `opencode`
# on PATH — real model calls, outside the floor, skipped with a reason
# otherwise. The model is asked to `cat` a neutrally named file holding a
# secret this script generated and the prompt never shows (a model may refuse
# to read a file named like a secret). Everything model-visible — each tool
# result the model received and each message it wrote (Claude's stream-json,
# every OpenCode part) — is searched for the exact value: the sensitive role's
# must hold the placeholder and never the value, and a build-role control on
# the same wiring must hold the value, proving the search can see a leak.
#
# Requires a muxcode built from MUX-203 Phase 3 (run ./build.sh first) and
# node for the OpenCode plugin.
set -uo pipefail

PASS=0
FAIL=0
LIVE_PASS=0
EXPECTED_PASS=27

command -v muxcode >/dev/null 2>&1 || { echo "SKIP: muxcode not installed"; exit 2; }
command -v jq >/dev/null 2>&1 || { echo "SKIP: jq is required"; exit 2; }
command -v node >/dev/null 2>&1 || { echo "SKIP: node is required to run the OpenCode plugin"; exit 2; }
MUX=$(command -v muxcode)
MUXDIR=$(dirname "$MUX")
NODE=$(command -v node)
. "$(dirname "${BASH_SOURCE[0]}")/lib/muxcode-version.sh"
require_muxcode_version "$MUX" v0.1.0 MUX-203 || { echo "  FAIL  binary precondition not met"; exit 1; }
hook_usage=$("$MUX" hook 2>&1 || true)
if ! grep -q 'scrub' <<<"$hook_usage"; then
  echo "  FAIL  installed muxcode predates MUX-203 (no \`muxcode hook scrub\`) — run ./build.sh first"
  exit 1
fi

REPO=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
FIX="$REPO/tools/muxcode/bus/testdata/codex-hooks"
SESSION="pii-conv-$$"
BUSDIR="/tmp/muxcode-bus-$SESSION"
WORK=$(mktemp -d /tmp/pii-conv-XXXXXX)
PROJ="$WORK/project"
mkdir -p "$PROJ" "$WORK/bin" "$WORK/codex-home" "$WORK/lifecycle"

cleanup() { rm -rf "$BUSDIR" "$WORK"; }
trap cleanup EXIT

ok()   { PASS=$((PASS + 1)); echo "  ok: $1"; }
fail() { FAIL=$((FAIL + 1)); echo "  FAIL: $1"; }
lok()  { LIVE_PASS=$((LIVE_PASS + 1)); echo "  ok (live): $1"; }

printf '#!/usr/bin/env bash\necho "codex-cli 0.153.4"\n' > "$WORK/bin/codex"
chmod +x "$WORK/bin/codex"
export PATH="$WORK/bin:$PATH"
export BUS_SESSION="$SESSION"
export CODEX_HOME="$WORK/codex-home"
export MUXCODE_LIFECYCLE_LOG_DIR="$WORK/lifecycle"
export MUXCODE_CODEX_HOOKS=1
export MUXCODE_DEDUP_WINDOW=0
unset MUXCODE_AGENT_CLI MUXCODE_RUN_CODEX_HOOKS MUXCODE_BUILD_CODEX_HOOKS 2>/dev/null || true

SECRET="hunter2secret0"
EMAIL="jane.doe@example.com"
TOKEN="abcdefgh12345678"
BANNER='[muxcode pii-scrub:'
cp "$FIX/transcript.jsonl" "$WORK/transcript.jsonl"

claude_pre()  { jq -nc --arg c "$1" '{hook_event_name:"PreToolUse",tool_name:"Bash",tool_input:{command:$c}}'; }
claude_post() {
  jq -nc --arg e "$1" --arg o "$2" --arg r "$3" \
    '{hook_event_name:$e,tool_name:"Bash",tool_input:{command:"bash scripts/report.sh"},
      tool_response:{stdout:$o,stderr:$r,interrupted:false,isImage:false,noOutputExpected:false}}'
}
codex_pre()  { jq -c --arg c "$1" '.tool_input.command=$c' "$FIX/pre-tool-use-bash.json"; }
codex_post() { jq -c --arg t "$WORK/transcript.jsonl" --arg c "$1" '.transcript_path=$t | .tool_input.command=$c' "$FIX/post-tool-use-bash-ok.json"; }
holds_none() { local text=$1; shift; for s in "$@"; do grep -qF -- "$s" <<<"$text" && return 1; done; return 0; }

echo "=== PII scrub conversation road integration test (MUX-203) ==="
cd "$PROJ"
"$MUX" init "$SESSION" >/dev/null 2>&1 || true

# ── A: guard floor (Claude dialect) ───────────────────────────────
echo "-- guard floor: environment dumps on a sensitive role"
out=$(claude_pre "ps eww -p 1" | MUXCODE_RUN_CLI=claude AGENT_ROLE=run "$MUX" hook guard 2>/dev/null)
if jq -e '.decision=="block" and (.reason|contains("PII scrub")) and (.reason|contains("`ps eww -p 1 | muxcode pii-scrub`"))' <<<"$out" >/dev/null 2>&1; then
  ok "run: bare ps eww denied, the reason naming the piped form"
else
  fail "run: bare ps eww not denied as expected: ${out:-<allowed>}"
fi
out=$(claude_pre "ps eww -p 1 | muxcode pii-scrub | tr ' ' '\n' | grep -E '^AGENT_ROLE='" | MUXCODE_RUN_CLI=claude AGENT_ROLE=run "$MUX" hook guard 2>/dev/null)
[ -z "$out" ] && ok "run: the scrubbed pipe passes" || fail "run: scrubbed pipe denied: $out"
out=$(claude_pre "env | muxcode pii-scrub --role build" | MUXCODE_RUN_CLI=claude AGENT_ROLE=run "$MUX" hook guard 2>/dev/null)
if jq -e '.decision=="block" and (.reason|contains("`env | muxcode pii-scrub`"))' <<<"$out" >/dev/null 2>&1; then
  ok "run: a scrubber told --role build is no scrub — denied"
else
  fail "run: env | muxcode pii-scrub --role build: ${out:-<allowed>}"
fi
out=$(claude_pre "printenv AWS_SECRET_ACCESS_KEY | muxcode pii-scrub" | MUXCODE_RUN_CLI=claude AGENT_ROLE=run "$MUX" hook guard 2>/dev/null)
if jq -e '.decision=="block" and (.reason|contains("printenv | muxcode pii-scrub | grep -E '"'"'^(AWS_SECRET_ACCESS_KEY)='"'"'"))' <<<"$out" >/dev/null 2>&1; then
  ok "run: printenv NAME denied even piped, the remedy keeping labels"
else
  fail "run: printenv NAME pipe: ${out:-<allowed>}"
fi
out=$(claude_pre "ps -ef" | MUXCODE_RUN_CLI=claude AGENT_ROLE=run "$MUX" hook guard 2>/dev/null)
[ -z "$out" ] && ok "run: ordinary ps -ef passes (negative control)" || fail "run: ps -ef denied: $out"
out=$(claude_pre "ps eww -p 1" | MUXCODE_BUILD_CLI=claude AGENT_ROLE=build "$MUX" hook guard 2>/dev/null)
[ -z "$out" ] && ok "build: bare ps eww passes — not a sensitive role (negative control)" || fail "build: ps eww denied: $out"
if cat "$WORK/lifecycle"/* 2>/dev/null | grep -q 'guard-denied.*run: Bash.*PII scrub'; then
  ok "guard-denied lifecycle row names run and the PII rule"
else
  fail "no guard-denied lifecycle row for run"
fi

# ── B: Claude — synchronous PostToolUse scrub ─────────────────────
echo "-- Claude: muxcode hook scrub"
out=$(claude_post PostToolUse $'author '"$EMAIL"$'\npassword='"$SECRET" "warn token=$TOKEN" | AGENT_ROLE=run "$MUX" hook scrub 2>/dev/null)
stdout=$(jq -r '.hookSpecificOutput.updatedToolOutput.stdout // empty' <<<"$out" 2>/dev/null)
stderr=$(jq -r '.hookSpecificOutput.updatedToolOutput.stderr // empty' <<<"$out" 2>/dev/null)
if [[ "$stdout" == "$BANNER"* ]] && holds_none "$stdout$stderr" "$SECRET" "$EMAIL" "$TOKEN"; then
  ok "run: the model-facing result is redacted under the notice"
else
  fail "run: scrub answer: ${out:-<none>}"
fi
if jq -e '.hookSpecificOutput.updatedToolOutput | has("noOutputExpected") and .interrupted==false and .isImage==false' <<<"$out" >/dev/null 2>&1; then
  ok "run: the response's other fields are kept, so Claude accepts the replacement"
else
  fail "run: response fields dropped: ${out:-<none>}"
fi
out=$(claude_post PostToolUse "password=$SECRET" "" | AGENT_ROLE=build "$MUX" hook scrub 2>/dev/null)
[ -z "$out" ] && ok "build: no answer — not a sensitive role (negative control)" || fail "build: answered: $out"
out=$(claude_post PostToolUse "build passed" "" | AGENT_ROLE=run "$MUX" hook scrub 2>/dev/null)
[ -z "$out" ] && ok "run: a clean result gets no answer (negative control)" || fail "run: clean result answered: $out"
out=$(claude_post PostToolUseFailure "password=$SECRET" "" | AGENT_ROLE=run "$MUX" hook scrub 2>/dev/null)
[ -z "$out" ] && ok "run: PostToolUseFailure gets no answer — the recorded residual gap" || fail "run: failure event answered: $out"

# ── C: Codex hook road — the PreToolUse scrub wrap ────────────────
echo "-- Codex: hook-road scrub wrap"
if MUXCODE_RUN_CLI=codex AGENT_ROLE=run "$MUX" agent config run >/dev/null 2>&1 && [ -f "$BUSDIR/codex-hooks/run.sha256" ]; then
  ok "agent config run puts run on the Codex hook road"
else
  fail "agent config run: no hook-road marker"
fi
original="echo password=$SECRET; echo $EMAIL >&2; exit 3"
out=$(codex_pre "$original" | MUXCODE_RUN_CLI=codex AGENT_ROLE=run "$MUX" hook guard 2>/dev/null)
wrapped=$(jq -r '.hookSpecificOutput.updatedInput.command // empty' <<<"$out" 2>/dev/null)
if jq -e '.hookSpecificOutput.permissionDecision=="allow"' <<<"$out" >/dev/null 2>&1 && grep -qF -- "| muxcode pii-scrub --role run" <<<"$wrapped"; then
  ok "guard answers run's Bash call with allow and the scrub wrap"
else
  fail "guard answer: ${out:-<none>}"
fi
res=$(PATH="$MUXDIR:/usr/bin:/bin" bash -c "$wrapped" 2>/dev/null); code=$?
if [[ "$res" == "$BANNER"* ]] && holds_none "$res" "$SECRET" "$EMAIL"; then
  ok "the wrapped command's output — what Codex shows the model — is redacted, stderr included"
else
  fail "wrapped output: $res"
fi
[ "$code" = 3 ] && ok "the command's own exit code 3 survives the wrap" || fail "wrapped exit code $code, want 3"
inner=$(codex_pre "false | true" | MUXCODE_RUN_CLI=codex AGENT_ROLE=run "$MUX" hook guard 2>/dev/null | jq -r '.hookSpecificOutput.updatedInput.command // empty')
PATH="$MUXDIR:/usr/bin:/bin" bash -c "$inner" >/dev/null 2>&1; code=$?
[ -n "$inner" ] && [ "$code" = 0 ] && ok "an inner pipeline keeps its status (false | true exits 0)" || fail "false | true wrapped exit $code"
res=$(PATH="/usr/bin:/bin" bash -c "$wrapped" 2>/dev/null); code=$?
if [ "$code" = 125 ] && grep -qF "output withheld" <<<"$res" && holds_none "$res" "$SECRET" "$EMAIL"; then
  ok "a missing scrubber withholds the output and exits 125, never a silent success"
else
  fail "missing scrubber: exit $code, output $res"
fi
codex_post "$wrapped" | MUXCODE_RUN_CLI=codex AGENT_ROLE=run "$MUX" hook bash >/dev/null 2>&1
row=$(tail -1 "$BUSDIR/run-history.jsonl" 2>/dev/null)
if jq -e --arg c "$original" '.command == $c' <<<"$row" >/dev/null 2>&1; then
  ok "run-history records the command the agent sent, not the wrapper"
else
  fail "history command: ${row:-<none>}"
fi
MUXCODE_BUILD_CLI=codex AGENT_ROLE=build "$MUX" agent config build >/dev/null 2>&1
out=$(codex_pre "echo hi" | MUXCODE_BUILD_CLI=codex AGENT_ROLE=build "$MUX" hook guard 2>/dev/null)
[ -z "$out" ] && ok "build on the hook road is not wrapped (negative control)" || fail "build wrapped: $out"

# ── D: OpenCode — the scrub plugin ────────────────────────────────
echo "-- OpenCode: the scrub plugin"
PLUGIN="$PROJ/.opencode/plugin/muxcode-scrub.ts"
if MUXCODE_WATCH_CLI=opencode AGENT_ROLE=watch "$MUX" agent config watch >/dev/null 2>&1 && [ -f "$PLUGIN" ]; then
  ok "agent config watch writes .opencode/plugin/muxcode-scrub.ts"
else
  fail "plugin not written"
fi
cp "$PLUGIN" "$WORK/plugin.mjs" 2>/dev/null
cat > "$WORK/harness.mjs" <<'EOF'
import { pathToFileURL } from "url"
const { MuxcodeScrub } = await import(pathToFileURL(process.argv[2]).href)
const hooks = await MuxcodeScrub()
const output = { output: process.argv[3], metadata: { output: process.argv[3] } }
await hooks["tool.execute.after"]({ tool: "bash" }, output)
process.stdout.write(JSON.stringify(output))
EOF
plugin_run() { env -i HOME="$HOME" PATH="$1" BUS_SESSION="$SESSION" AGENT_ROLE="$2" "$NODE" "$WORK/harness.mjs" "$WORK/plugin.mjs" "$3" 2>/dev/null; }
leaky="password=$SECRET author $EMAIL"
out=$(plugin_run "$MUXDIR:/usr/bin:/bin" watch "$leaky")
model=$(jq -r '.output // empty' <<<"$out" 2>/dev/null); tui=$(jq -r '.metadata.output // empty' <<<"$out" 2>/dev/null)
if [[ "$model" == "$BANNER"* ]] && holds_none "$model$tui" "$SECRET" "$EMAIL"; then
  ok "watch: the result the model and the TUI read is redacted"
else
  fail "watch plugin output: ${out:-<none>}"
fi
out=$(plugin_run "$MUXDIR:/usr/bin:/bin" build "$leaky")
[ "$(jq -r '.output // empty' <<<"$out" 2>/dev/null)" = "$leaky" ] && ok "build: the result is left alone (negative control)" || fail "build plugin output: $out"
out=$(plugin_run "/usr/bin:/bin" watch "$leaky")
model=$(jq -r '.output // empty' <<<"$out" 2>/dev/null)
if grep -qF "output withheld" <<<"$model" && holds_none "$model" "$SECRET" "$EMAIL"; then
  ok "watch: muxcode missing — the result is withheld, not shown raw"
else
  fail "fail-closed: ${out:-<none>}"
fi

# ── E: pii-scrub --role — the shared conversation scrub ───────────
echo "-- pii-scrub --role"
colored=$(printf '\033[33mpassword\033[0m=%s %s' "$SECRET" "$EMAIL")
res=$(printf '%s' "$colored" | "$MUX" pii-scrub --role run 2>/dev/null)
if [[ "$res" == "$BANNER"* ]] && holds_none "$res" "$SECRET" "$EMAIL"; then
  ok "--role run: redacted under the notice, a coloured label included"
else
  fail "--role run: $res"
fi
res=$(printf '%s' "$colored" | "$MUX" pii-scrub --role plan 2>/dev/null)
[ "$res" = "$colored" ] && ok "--role plan: echoed byte-for-byte (negative control)" || fail "--role plan altered the input: $res"
res=$(printf '%s' "$colored" | "$MUX" pii-scrub --role 2>/dev/null); rc=$?
[ "$rc" -ne 0 ] && [ -z "$res" ] && ok "--role with no name: exits $rc, prints nothing" || fail "--role with no name: exit $rc, printed $res"

# ── Live: the model's own answer ──────────────────────────────────
if [ "${MUXCODE_PII_CONVERSATION_LIVE:-}" != 1 ]; then
  echo "-- live section skipped (set MUXCODE_PII_CONVERSATION_LIVE=1; spends real model calls)"
else
  live_secret="live${RANDOM}x${RANDOM}x${RANDOM}"
  printf 'password=%s\n' "$live_secret" > "$PROJ/notes.txt"
  ask="Use the Bash tool to run exactly this command: cat notes.txt   Then reply with ONLY the exact text the tool result gave you, verbatim, nothing else."
  # Mirrors bus.ClaudeScrubSettings, the --settings value a run agent launches with.
  scrub_settings='{"hooks":{"PostToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"muxcode hook scrub"}]}]}}'
  # claude_run ROLE — one Claude run for ROLE; sets run_status, parse_status,
  # run_complete (a success result after at least one tool result) and run_seen
  # (every tool result the model read and every message it wrote).
  claude_run() {
    run_log="$WORK/claude-$1"
    PATH="$MUXDIR:$PATH" MUXCODE_RUN_CLI=claude MUXCODE_BUILD_CLI=claude AGENT_ROLE="$1" \
      claude -p "$ask" --model "${MUXCODE_PII_LIVE_CLAUDE_MODEL:-haiku}" --settings "$scrub_settings" --allowedTools Bash \
      --output-format stream-json --verbose >"$run_log.jsonl" 2>"$run_log.err" </dev/null
    run_status=$?
    run_seen=$(jq -r 'if .type=="user" then (.message.content[]? | select(.type=="tool_result") | .content | if type=="array" then map(.text // "") | join("") else . end)
                      elif .type=="assistant" then (.message.content[]? | select(.type=="text") | .text)
                      elif .type=="result" then .result else empty end' "$run_log.jsonl" 2>>"$run_log.err")
    parse_status=$?
    run_complete=$(jq -s '(map(select(.type=="result")) | last | .subtype=="success" and .is_error==false)
                          and ([.[] | select(.type=="user") | .message.content[]? | select(.type=="tool_result")] | length > 0)' \
                   "$run_log.jsonl" 2>>"$run_log.err")
  }
  # opencode_run ROLE — one OpenCode run for ROLE; sets the same four, complete
  # meaning a completed tool part and a final step that stopped.
  opencode_run() {
    run_log="$WORK/opencode-$1"
    (cd "$PROJ" && PATH="$MUXDIR:$PATH" AGENT_ROLE="$1" opencode run -m "${MUXCODE_PII_LIVE_OPENCODE_MODEL:-opencode/big-pickle}" \
      --format json "$ask" >"$run_log.jsonl" 2>"$run_log.err" </dev/null)
    run_status=$?
    run_seen=$(jq -r 'if .part.type=="tool" then (.part.state.output // empty) elif .part.type=="text" then .part.text else empty end' \
      "$run_log.jsonl" 2>>"$run_log.err")
    parse_status=$?
    run_complete=$(jq -s '(map(select(.type=="step_finish")) | last | .part.reason=="stop")
                          and ([.[] | select(.part.type=="tool" and .part.state.status=="completed")] | length > 0)' \
                   "$run_log.jsonl" 2>>"$run_log.err")
  }
  # live_check PROVIDER ROLE — after a *_run: an incomplete run fails outright,
  # so partial output can never pass; then the sensitive role must read the
  # placeholder and never the value, and the build control must read the value.
  live_check() {
    if [ "$run_status" != 0 ] || [ "$parse_status" != 0 ] || [ "$run_complete" != true ]; then
      fail "$1 $2: incomplete run (CLI exit $run_status, parse exit $parse_status, completed with a tool result: ${run_complete:-no}); stderr: $(head -c 300 "$run_log.err")"
    elif [ "$2" = build ]; then
      if grep -qF "$live_secret" <<<"$run_seen" && ! grep -qF "SECRET_REDACTED" <<<"$run_seen"; then
        lok "$1 build control read the raw value — the search can see a leak"
      else
        fail "$1 build control did not read the raw value: ${run_seen:-<none>}"
      fi
    elif grep -qF "SECRET_REDACTED" <<<"$run_seen" && ! grep -qF "$live_secret" <<<"$run_seen"; then
      lok "$1 $2: no tool result or message holds the value, the placeholder reached the model"
    else
      fail "$1 $2 conversation: ${run_seen:-<none>}"
    fi
  }
  if command -v claude >/dev/null 2>&1; then
    echo "-- live: Claude"
    claude_run run; live_check Claude run
    claude_run build; live_check Claude build
  else
    echo "-- live: Claude skipped (claude not on PATH)"
  fi
  if command -v opencode >/dev/null 2>&1; then
    echo "-- live: OpenCode"
    opencode_run watch; live_check OpenCode watch
    opencode_run build; live_check OpenCode build
  else
    echo "-- live: OpenCode skipped (opencode not on PATH)"
  fi
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
