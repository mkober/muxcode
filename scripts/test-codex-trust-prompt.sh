#!/usr/bin/env bash
# Integration test for MUX-164 — Codex's directory-trust prompt and the
# injection guard.
#
# Part A pins the unit contracts: a live trust prompt classifies as
# PaneTrustPrompt (and NOT once it has scrolled past the composer), Enter
# answers it, and every wake road refuses a shell pane.
#
# Part B drives the real `muxcode deliver --force` path against scratch panes
# on the default tmux server (muxcode pane targeting runs against the default
# socket), on the codex scrape road so the PAYLOAD is what would be typed:
#   B1 a bare shell in the agent pane — the payload must NOT be typed
#   B2 a fake codex showing the trust prompt — Enter is pressed, payload held
#   B3 the fake codex at its composer — the payload lands
#
# Hermetic: scratch BUS_SESSION + scratch tmux session, no daemon. Skips exit
# 2, and a coverage floor keeps a partial run from reporting green.
#
# REQUIRES: go, tmux, muxcode.
#
# Usage: bash scripts/test-codex-trust-prompt.sh
set -uo pipefail

PASS=0
FAIL=0
ok()   { PASS=$((PASS + 1)); echo "  ok: $1"; }
fail() { FAIL=$((FAIL + 1)); echo "  FAIL: $1"; }
EXPECTED_PASS=30

command -v go      >/dev/null 2>&1 || { echo "SKIP: go is required"; exit 2; }
command -v tmux    >/dev/null 2>&1 || { echo "SKIP: tmux is required"; exit 2; }
command -v muxcode >/dev/null 2>&1 || { echo "SKIP: muxcode not installed"; exit 2; }
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MOD="$ROOT/tools/muxcode"
[ -f "$MOD/go.mod" ] || { echo "SKIP: $MOD/go.mod not found"; exit 2; }

SESSION="trust-test-$$"
export BUS_SESSION="$SESSION"
WORK=$(mktemp -d /tmp/trust-test-XXXXXX)
export MUXCODE_LIFECYCLE_LOG_DIR="$WORK/lifecycle"
LIFELOG="$MUXCODE_LIFECYCLE_LOG_DIR/$SESSION.log"
# Codex scrape road: the message payload itself is typed, which is the
# dangerous case. The hook road types only the fixed wake sentence.
export MUXCODE_AGENT_CLI=codex
export MUXCODE_BUILD_CLI=codex
export MUXCODE_CODEX_HOOKS=0
# Sender identity, both vars (see test-send-keys-dash.sh).
export AGENT_ROLE=edit
export BUS_ROLE=edit

cleanup() {
  tmux kill-session -t "$SESSION" 2>/dev/null || true
  rm -rf "/tmp/muxcode-bus-$SESSION" "$WORK"
}
trap cleanup EXIT

echo "=== Codex trust prompt + injection guard integration test (MUX-164) ==="

# ── Part A: unit contracts ───────────────────────────────────

echo "-- unit contracts"
UNIT_RE='TestCaptureInjectionTarget|TestCodexAcceptStartup_TrustPrompt|TestCodexSendWakeUp_(HookRoad|ScrapeRoad|TrustPromptThen)|TestOpenCodeSendWakeUp_(RefusesShell|InjectsAt)|TestSendWakeUpWithText_ClaudeRefusesShell|TestCodexClassifyPane'
unit_out=$(cd "$MOD" && go test ./bus -run "$UNIT_RE" -count=1 -v 2>&1)
if [ $? -eq 0 ]; then
  ok "guard unit tests pass"
else
  fail "guard unit tests pass"
  printf '%s\n' "$unit_out" | grep -E '^(--- FAIL|FAIL|.*_test.go:[0-9]+:)' | sed 's/^/    | /'
fi
for name in \
  TestCaptureInjectionTarget_RefusesShellPrompt \
  TestCaptureInjectionTarget_ComposerProceeds \
  TestCaptureInjectionTarget_CaptureFailureRefuses \
  TestCaptureInjectionTarget_ShellUnderStaleComposerRefused \
  TestCodexAcceptStartup_TrustPromptPressesEnter \
  TestCodexSendWakeUp_HookRoadRefusesShell \
  TestCodexSendWakeUp_HookRoadAnswersTrustPromptAndDefers \
  TestCodexSendWakeUp_HookRoadInjectsAtComposer \
  TestCodexSendWakeUp_ScrapeRoadRefusesShellAndKeepsInbox \
  TestCodexSendWakeUp_TrustPromptThenComposerInjects \
  TestOpenCodeSendWakeUp_RefusesShellAndKeepsInbox \
  TestOpenCodeSendWakeUp_InjectsAtComposer \
  TestSendWakeUpWithText_ClaudeRefusesShell \
  TestCodexClassifyPane/trust_prompt_live \
  TestCodexClassifyPane/trust_prompt_scrolled_past_composer \
  TestCodexClassifyPane/trust_text_quoted_in_output; do
  if printf '%s\n' "$unit_out" | grep -qE -- "^ *--- PASS: ${name} "; then
    ok "$name"
  else
    fail "$name ran and passed"
  fi
done

# ── Part B: the deliver path against scratch panes ───────────

echo "-- B1: bare shell pane"
tmux new-session -d -s "$SESSION" -n build -x 160 -y 40
tmux split-window -h -t "$SESSION:build"
# A fixed prompt: the guard keys on the prompt suffix, and a host PS1 that
# ends in ❯ (starship and friends) would read as an agent.
tmux send-keys -t "$SESSION:build.1" 'exec env PS1="\$ " bash --norc --noprofile' Enter
sleep 0.4
muxcode init "$SESSION" >/dev/null 2>&1 || true

PAY="mux164 injection probe payload"
muxcode send build build "$PAY" >/dev/null 2>&1 || true
if muxcode inbox --role build --peek 2>/dev/null | grep -qF "$PAY"; then
  ok "message landed in the build inbox"
else
  fail "message landed in the build inbox — send was dropped"
fi

deliver_out=$(muxcode deliver build --force 2>&1 || true)
echo "  [diag] deliver: $deliver_out"
if printf '%s' "$deliver_out" | grep -qiE 'shell prompt|skipped'; then
  ok "deliver reports the shell refusal"
else
  fail "deliver reports the shell refusal"
fi
sleep 0.3
cap=$(tmux capture-pane -t "$SESSION:build.1" -pJ 2>/dev/null || true)
if printf '%s' "$cap" | grep -qF "$PAY"; then
  fail "payload must NOT be typed into a shell pane"
  printf '%s\n' "$cap" | grep -v '^[[:space:]]*$' | sed 's/^/    | /'
else
  ok "payload was not typed into the shell pane"
fi
refused_before=$(grep -c '"event":"injection-refused"' "$LIFELOG" 2>/dev/null || true)
if [ "${refused_before:-0}" -ge 1 ]; then
  ok "injection-refused lifecycle row written"
else
  fail "injection-refused lifecycle row written"
fi
if muxcode inbox --role build --peek 2>/dev/null | grep -qF "$PAY"; then
  ok "refused message stays in the inbox"
else
  fail "refused message stays in the inbox"
fi

echo "-- B2: fake codex at its trust prompt"
cat > "$WORK/fake-codex.sh" <<'EOF'
#!/usr/bin/env bash
printf '╭────────────────────────────╮\n│ >_ OpenAI Codex (v0.153.4) │\n╰────────────────────────────╯\n'
printf '› Ask Codex to do anything\n  ? for shortcuts\n'
printf '> You are in %s\n' "$PWD"
printf '  Do you trust the contents of this directory? Working with untrusted contents comes with higher risk of prompt injection. Trusting the directory allows\n'
printf '  project-local config, hooks, and exec policies to load.\n'
printf '› 1. Yes, continue\n  2. No, quit\n  Press enter to continue\n'
IFS= read -r _
printf '› Ask Codex to do anything\n  ? for shortcuts\n  gpt-5.6-luna medium · ~/repo\n'
sleep 120
EOF
tmux send-keys -t "$SESSION:build.1" "bash $WORK/fake-codex.sh" Enter
shown=0
for _ in $(seq 1 25); do
  if tmux capture-pane -t "$SESSION:build.1" -p 2>/dev/null | grep -qF 'Press enter to continue'; then
    shown=1; break
  fi
  sleep 0.2
done
if [ "$shown" -eq 1 ]; then
  ok "fake codex shows the trust prompt"
else
  fail "fake codex shows the trust prompt"
fi

deliver_out=$(muxcode deliver build --force 2>&1 || true)
echo "  [diag] deliver: $deliver_out"
if printf '%s' "$deliver_out" | grep -qi 'trust prompt'; then
  ok "deliver reports the trust prompt was answered and the injection deferred"
else
  fail "deliver reports the trust prompt was answered and the injection deferred"
fi
composer=0
for _ in $(seq 1 25); do
  if tmux capture-pane -t "$SESSION:build.1" -p 2>/dev/null | grep -qF 'gpt-5.6-luna'; then
    composer=1; break
  fi
  sleep 0.2
done
if [ "$composer" -eq 1 ]; then
  ok "Enter answered the trust prompt — composer rendered"
else
  fail "Enter answered the trust prompt — composer rendered"
fi
cap=$(tmux capture-pane -t "$SESSION:build.1" -pJ 2>/dev/null || true)
if printf '%s' "$cap" | grep -qF "$PAY"; then
  fail "payload must NOT be typed into the trust prompt"
else
  ok "payload was not typed into the trust prompt"
fi
if grep -q '"source":"auto-accept".*"event":"trust-prompt"' "$LIFELOG" 2>/dev/null; then
  ok "auto-accept trust-prompt lifecycle row written"
else
  fail "auto-accept trust-prompt lifecycle row written"
fi
if muxcode inbox --role build --peek 2>/dev/null | grep -qF "$PAY"; then
  ok "deferred message stays in the inbox"
else
  fail "deferred message stays in the inbox"
fi

echo "-- B3: fake codex at its composer"
deliver_out=$(muxcode deliver build --force 2>&1 || true)
echo "  [diag] deliver: $deliver_out"
sleep 0.6
cap=$(tmux capture-pane -t "$SESSION:build.1" -pJ 2>/dev/null || true)
if printf '%s' "$cap" | grep -qF "$PAY"; then
  ok "payload landed at the composer"
else
  fail "payload landed at the composer"
  printf '%s\n' "$cap" | grep -v '^[[:space:]]*$' | sed 's/^/    | /'
fi
refused_after=$(grep -c '"event":"injection-refused"' "$LIFELOG" 2>/dev/null || true)
if [ "${refused_after:-0}" -eq "${refused_before:-0}" ]; then
  ok "no refusal at the composer"
else
  fail "no refusal at the composer (before=$refused_before after=$refused_after)"
fi

# ── Summary ──────────────────────────────────────────────────

echo ""
echo "=== $PASS passed, $FAIL failed ==="
[ "$PASS" -ge "$EXPECTED_PASS" ] || { echo "FAIL: coverage floor not met ($PASS < $EXPECTED_PASS)"; exit 1; }
[ "$FAIL" -eq 0 ] || exit 1
echo "OK"
