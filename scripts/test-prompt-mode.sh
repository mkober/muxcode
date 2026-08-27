#!/usr/bin/env bash
# Integration test for the Prompt mode + prompt-agent (MUX-109).
#
# Mechanical sections (no model needed): --render-once frames (empty,
# clamped, unreachable), the live surface in a pane (tab cycle, inject
# toggle), the prompt authority gate, graph create (valid writes /
# invalid writes nothing / ungated commit rejected), CLI launch, and
# dash-leading injection end-to-end into a scratch agent pane.
#
# Live section (requires Ollama + the qwen3:4b model + the harness
# binary): the scratch daemon launching the headless prompt-agent, and
# the launch / status / named-vs-unnamed approve / create intents driven
# through the real model. Skipped WITH REASON when the model is absent —
# and the coverage floor below guarantees a skip can never read as green
# silence: the mechanical sections alone must clear it.
#
# Hermetic: scratch BUS_SESSION + scratch tmux session + scratch project
# dir; nothing touches a live muxcode session.
set -euo pipefail

PASS=0
FAIL=0
SKIP=0

command -v tmux >/dev/null 2>&1 || { echo "SKIP: tmux is required"; exit 2; }
command -v muxcode >/dev/null 2>&1 || { echo "SKIP: muxcode not installed"; exit 2; }

SESSION="promptmode-$$"
export BUS_SESSION="$SESSION"
WORK=$(mktemp -d /tmp/promptmode-XXXXXX)
export MUXCODE_LIFECYCLE_LOG_DIR="$WORK/lifecycle"
export AGENT_ROLE=edit
export BUS_ROLE=edit
# The scratch daemon must not race the mechanical sections with harness
# launches; the live section re-enables supervision explicitly.
export MUXCODE_PROMPT_AGENT_DISABLE=1

PROJECT="$WORK/project"
mkdir -p "$PROJECT"
cd "$PROJECT"

DPID=""
cleanup() {
  [ -n "$DPID" ] && kill "$DPID" 2>/dev/null || true
  # The daemon may have started a headless harness — stop it via its marker.
  if [ -f "/tmp/muxcode-bus-$SESSION/harness-prompt.pid" ]; then
    kill "$(cat "/tmp/muxcode-bus-$SESSION/harness-prompt.pid")" 2>/dev/null || true
  fi
  tmux kill-session -t "$SESSION" 2>/dev/null || true
  rm -rf "/tmp/muxcode-bus-$SESSION" "$WORK"
}
trap cleanup EXIT

ok()   { PASS=$((PASS + 1)); echo "  ok: $1"; }
fail() { FAIL=$((FAIL + 1)); echo "  FAIL: $1"; }
skip() { SKIP=$((SKIP + 1)); echo "  SKIP: $1"; }
strip_ansi() { sed 's/\x1b\[[0-9;]*[A-Za-z]//g'; }

# live_diag dumps the scratch agent's own evidence on a live-intent FAIL,
# so "latency, not capability" is observable rather than assumed: the
# harness log shows whether a tool call was emitted late or never.
live_diag() {
  echo "  [diag] prompt-agent log tail:"
  tail -12 "/tmp/muxcode-bus-$SESSION/prompt-agent.log" 2>/dev/null | sed 's/^/    | /' || echo "    | (no log)"
  echo "  [diag] daemon log tail:"
  tail -6 "$WORK/daemon.log" 2>/dev/null | sed 's/^/    | /' || true
}

echo "=== prompt mode integration test (MUX-109) ==="

tmux new-session -d -s "$SESSION" -n edit -x 160 -y 40
# Pane 1 is the delivery contract target (AgentPane = "1") — run cat so
# injected text echoes verbatim with no shell interpretation.
tmux split-window -h -t "$SESSION:edit" cat
muxcode init "$SESSION" >/dev/null 2>&1 || true

# ── 1. Prompt authority gate ─────────────────────────────────
# MUST run before any authorized seed send: a prior (prompt,prompt)
# in-flight task makes the dedup guard suppress the unauthorized send
# with exit 0 before the authority check can refuse it (first run's
# false FAIL, 2026-08-27).

echo "-- authority gate"
if muxcode send prompt prompt "approve commit-gate on run wf-1" >"$WORK/deny.out" 2>&1; then
  fail "unauthorized prompt request must be refused"
else
  ok "unauthorized prompt request refused"
fi
if grep -q "human-initiated" "$WORK/deny.out"; then
  ok "refusal explains the human-initiated rule"
else
  fail "refusal explains the human-initiated rule (got: $(cat "$WORK/deny.out"))"
fi

# ── 2. Render-once frames ────────────────────────────────────
# env -u: the script disables prompt-agent supervision globally, but the
# frame must show the natural not-running reason, not the disabled one.

echo "-- render-once frames"
frame=$(env -u MUXCODE_PROMPT_AGENT_DISABLE muxcode graph ui --prompt --render-once --width 100 | strip_ansi)
if printf '%s' "$frame" | grep -q "Prompt" && printf '%s' "$frame" | grep -q "Pending Gates"; then
  ok "frame carries the four-surface tab bar"
else
  fail "frame carries the four-surface tab bar"
fi
# Placeholder text was removed by request — the empty frame self-describes
# structurally: the separator column runs the full body height and the
# input line names its destination.
if printf '%s' "$frame" | grep -q "No prompts yet"; then
  fail "placeholder instructional text must not return"
else
  ok "no placeholder instructional text"
fi
sep_rows=$(printf '%s\n' "$frame" | grep -c "│" || true)
if [ "$sep_rows" -ge 5 ]; then
  ok "separator column runs the body height ($sep_rows rows)"
else
  fail "separator column runs the body height (got $sep_rows rows)"
fi
if printf '%s' "$frame" | grep -q "interpret: prompt-agent"; then
  ok "input line names its destination"
else
  fail "input line names its destination"
fi
# No harness runs in this scratch session, so the unreachable state must
# render — the spec's model-absent frame, deliberately checkable with no
# Ollama at all.
if printf '%s' "$frame" | grep -q "prompt-agent not running"; then
  ok "unreachable state renders with its reason"
else
  fail "unreachable state renders with its reason"
fi

# Seed a long exchange through the sanctioned opt-in (also proving the
# override env works), then check the narrow frame clamps it.
long=$(printf 'x%.0s' $(seq 1 200))
if MUXCODE_PROMPT_AUTHORITY_ROLES=edit muxcode send prompt prompt "clamp probe $long" >/dev/null 2>&1; then
  ok "authorized send delivers (MUXCODE_PROMPT_AUTHORITY_ROLES opt-in)"
else
  fail "authorized send delivers (MUXCODE_PROMPT_AUTHORITY_ROLES opt-in)"
fi
narrow=$(env -u MUXCODE_PROMPT_AGENT_DISABLE muxcode graph ui --prompt --render-once --width 40 | strip_ansi)
if printf '%s' "$narrow" | grep -qE 'x{100}'; then
  fail "40-col frame clamps the 200-char prompt"
else
  ok "40-col frame clamps the 200-char prompt"
fi
if printf '%s' "$narrow" | grep -q "clamp probe"; then
  ok "clamped exchange still present in the transcript"
else
  fail "clamped exchange still present in the transcript"
fi
# The seeded question has no answer and no agent — the working state.
wide=$(env -u MUXCODE_PROMPT_AGENT_DISABLE muxcode graph ui --prompt --render-once --width 120 | strip_ansi)
if printf '%s' "$wide" | grep -q "working"; then
  ok "working state renders for the open question"
else
  fail "working state renders for the open question"
fi

# ── 3. Graph create ──────────────────────────────────────────

echo "-- graph create"
valid='{"name":"pm-valid","description":"test graph","start":"a","nodes":[{"id":"a","type":"send","role":"build","action":"build","message":"go"}],"edges":[]}'
if muxcode graph create --json "$valid" >"$WORK/create.out" 2>&1; then
  ok "valid definition accepted"
else
  fail "valid definition accepted (got: $(cat "$WORK/create.out"))"
fi
if [ -f ".muxcode/graphs/pm-valid.json" ]; then
  ok "definition written project-local"
else
  fail "definition written project-local"
fi
if muxcode graph list | grep -q "pm-valid.*project"; then
  ok "created graph resolves at project tier in graph list"
else
  fail "created graph resolves at project tier in graph list"
fi

ungated='{"name":"pm-ungated","description":"test graph","start":"c","nodes":[{"id":"c","type":"send","role":"commit","action":"commit","message":"commit it"}],"edges":[]}'
if muxcode graph create --json "$ungated" >"$WORK/ungated.out" 2>&1; then
  fail "ungated commit node must be rejected"
else
  ok "ungated commit node rejected"
fi
if grep -q "wait_human" "$WORK/ungated.out"; then
  ok "rejection cites the gate rule verbatim"
else
  fail "rejection cites the gate rule verbatim (got: $(cat "$WORK/ungated.out"))"
fi
if [ -e ".muxcode/graphs/pm-ungated.json" ]; then
  fail "rejected definition wrote nothing"
else
  ok "rejected definition wrote nothing"
fi

if muxcode graph run pm-valid >/dev/null 2>&1 && muxcode graph status | grep -q "pm-valid"; then
  ok "created graph launches and appears in the run store"
else
  fail "created graph launches and appears in the run store"
fi

# ── 4. Live surface: cycle, toggle, injection ────────────────

echo "-- live surface + injection"
tmux split-window -vf -l 16 -e "BUS_SESSION=$SESSION" -t "$SESSION:edit" "muxcode graph ui --prompt"
sleep 2
SURFACE="$SESSION:edit.2"
cap=$(tmux capture-pane -t "$SURFACE" -pJ | strip_ansi)
if printf '%s' "$cap" | grep -q "interpret: prompt-agent"; then
  ok "surface pane opens on the Prompt surface"
else
  fail "surface pane opens on the Prompt surface"
fi

tmux send-keys -t "$SURFACE" C-t
sleep 1
cap=$(tmux capture-pane -t "$SURFACE" -pJ | strip_ansi)
if printf '%s' "$cap" | grep -q "inject: edit agent"; then
  ok "Ctrl-T flips to inject with the active agent named"
else
  fail "Ctrl-T flips to inject with the active agent named"
fi

tmux send-keys -t "$SURFACE" -l -- '- dash inject probe mux109'
sleep 0.5
tmux send-keys -t "$SURFACE" Enter
sleep 2
agent_cap=$(tmux capture-pane -t "$SESSION:edit.1" -pJ | strip_ansi)
echo "  [diag] agent pane: $(printf '%s' "$agent_cap" | grep -v '^[[:space:]]*$' | tail -3)"
if printf '%s' "$agent_cap" | grep -qF -- '- dash inject probe mux109'; then
  ok "dash-leading payload injected intact into the agent pane"
else
  fail "dash-leading payload injected intact into the agent pane"
fi
cap=$(tmux capture-pane -t "$SURFACE" -pJ | strip_ansi)
if printf '%s' "$cap" | grep -q "injected to edit"; then
  ok "surface shows the injection receipt"
else
  fail "surface shows the injection receipt"
fi

# Tab cycles away even with the toggle flipped.
tmux send-keys -t "$SURFACE" Tab
sleep 1
cap=$(tmux capture-pane -t "$SURFACE" -pJ | strip_ansi)
if printf '%s' "$cap" | grep -qE "No graph templates|pm-valid"; then
  ok "Tab cycles the surface to the launcher"
else
  fail "Tab cycles the surface to the launcher"
fi

# ── 5. Live model (skip-with-reason) ─────────────────────────

echo "-- live model intents"
live_reason=""
command -v muxcode-llm-harness >/dev/null 2>&1 || live_reason="muxcode-llm-harness not installed"
if [ -z "$live_reason" ]; then
  command -v ollama >/dev/null 2>&1 || live_reason="ollama not installed"
fi
if [ -z "$live_reason" ]; then
  ollama list 2>/dev/null | awk '{print $1}' | grep -qx "qwen3:4b" || live_reason="qwen3:4b not pulled (ollama list)"
fi

if [ -n "$live_reason" ]; then
  skip "live intents: $live_reason"
  skip "live intents: daemon-launched headless prompt-agent unverified"
  skip "live intents: launch/status/approve/create via the model unverified"
else
  # The scratch daemon owns the headless harness lifecycle (Phase 2) —
  # supervision itself is under test here.
  unset MUXCODE_PROMPT_AGENT_DISABLE
  muxcode watch "$SESSION" --poll 2 >"$WORK/daemon.log" 2>&1 &
  DPID=$!
  marker="/tmp/muxcode-bus-$SESSION/harness-prompt.pid"
  for _ in $(seq 1 45); do [ -f "$marker" ] && break; sleep 2; done
  if [ -f "$marker" ] && kill -0 "$(cat "$marker")" 2>/dev/null; then
    ok "daemon launched the headless prompt-agent (marker + live pid)"
  else
    fail "daemon launched the headless prompt-agent (see $WORK/daemon.log)"
  fi

  runs_before=$(muxcode graph status 2>/dev/null | grep -c "pm-valid" || true)
  MUXCODE_PROMPT_AUTHORITY_ROLES=edit muxcode send prompt prompt "launch the pm-valid graph" >/dev/null 2>&1 || true
  launched=""
  for _ in $(seq 1 90); do
    runs_now=$(muxcode graph status 2>/dev/null | grep -c "pm-valid" || true)
    [ "$runs_now" -gt "$runs_before" ] && launched=1 && break
    sleep 2
  done
  if [ -n "$launched" ]; then
    ok "launch intent started a run"
  else
    fail "launch intent started a run (3min timeout)"
    live_diag
  fi

  MUXCODE_PROMPT_AUTHORITY_ROLES=edit muxcode send prompt prompt "what graph runs exist right now" >/dev/null 2>&1 || true
  answered=""
  for _ in $(seq 1 90); do
    if muxcode history prompt --limit 10 2>/dev/null | grep -q "response"; then answered=1 && break; fi
    sleep 2
  done
  if [ -n "$answered" ]; then
    ok "status intent produced a response"
  else
    fail "status intent produced a response (3min timeout)"
  fi

  # Gated graph: launch, wait for the gate, then the guard's negative
  # control — the spec's verbatim phrasing must approve nothing.
  gated='{"name":"pm-gated","description":"test graph","start":"g","nodes":[{"id":"g","type":"wait_human"},{"id":"c","type":"send","role":"build","action":"build","message":"post-gate"}],"edges":[{"from":"g","to":"c"}]}'
  muxcode graph create --json "$gated" >/dev/null 2>&1 || true
  muxcode graph run pm-gated >/dev/null 2>&1 || true
  waiting=""
  for _ in $(seq 1 30); do
    if muxcode graph status 2>/dev/null | grep "pm-gated" | grep -q "running"; then waiting=1 && break; fi
    sleep 2
  done
  [ -n "$waiting" ] || skip "gate never reached waiting — approve checks not exercised"
  if [ -n "$waiting" ]; then
    run_id=$(muxcode graph status | grep "pm-gated" | head -1 | awk '{print $1}')
    MUXCODE_PROMPT_AUTHORITY_ROLES=edit muxcode send prompt prompt "approve whatever is waiting" >/dev/null 2>&1 || true
    sleep 90
    unnamed_held=""
    if muxcode graph status "$run_id" 2>/dev/null | grep "g" | grep -q "waiting"; then
      unnamed_held=1
    else
      fail "unnamed approve released nothing — the gate moved"
    fi
    MUXCODE_PROMPT_AUTHORITY_ROLES=edit muxcode send prompt prompt "approve the gate g on run $run_id" >/dev/null 2>&1 || true
    released=""
    for _ in $(seq 1 90); do
      if ! muxcode graph status "$run_id" 2>/dev/null | grep "g" | grep -q "waiting"; then released=1 && break; fi
      sleep 2
    done
    # The negative control's verdict is CONDITIONAL on the positive
    # control (plan's catch): "unnamed released nothing" while named
    # approve ALSO releases nothing proves only that nothing releases
    # gates — vacuous. Only a run where the named approve works can
    # claim the unnamed refusal discriminated.
    if [ -n "$released" ]; then
      ok "named approve released the gate"
      [ -n "$unnamed_held" ] && ok "unnamed approve released nothing (negative control, validated by the positive control)"
    else
      fail "named approve released the gate (3min timeout)"
      live_diag
      [ -n "$unnamed_held" ] && skip "unnamed-approve negative control undetermined — positive control failed"
    fi
  fi

  MUXCODE_PROMPT_AUTHORITY_ROLES=edit muxcode send prompt prompt "create a graph named pm-composed that sends a build request to the build role with message run the build" >/dev/null 2>&1 || true
  composed=""
  for _ in $(seq 1 120); do
    [ -f ".muxcode/graphs/pm-composed.json" ] && composed=1 && break
    sleep 2
  done
  if [ -n "$composed" ] && muxcode graph validate pm-composed >/dev/null 2>&1; then
    ok "create intent composed and wrote a valid definition"
  else
    fail "create intent composed and wrote a valid definition (4min timeout — escalation ladder territory if this persists)"
    live_diag
  fi
fi

# ── Summary ──────────────────────────────────────────────────

echo ""
echo "=== $PASS passed, $FAIL failed, $SKIP skipped ==="
# Coverage floor: the mechanical sections alone are 20 checks — a run
# that skipped its way below this is reporting silence, not health.
[ "$PASS" -ge 18 ] || { echo "FAIL: coverage floor not met ($PASS < 18)"; exit 1; }
[ "$FAIL" -eq 0 ] || exit 1
if [ "$SKIP" -gt 0 ]; then
  echo "OK (with $SKIP skipped — live-model checks did not run on this machine)"
else
  echo "OK"
fi
