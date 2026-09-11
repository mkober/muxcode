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
#
# Exit codes: 0 = everything ran and passed; 1 = a check failed, or the
# coverage floor was not met; 2 = nothing failed but section 4's required
# chord-receiver checks could not run (python3 absent), so the run is
# INCOMPLETE rather than green.
set -euo pipefail

PASS=0
FAIL=0
SKIP=0
# Section 4's two chord-receiver checks are the point of this script, and the
# global PASS floor cannot speak for them: unrelated checks clear it on their
# own, so a run where python3 was absent met the floor and reported green with
# zero parser coverage. This flag is set only once both checks have actually
# been evaluated, and the summary exits 2 when it has not been.
PARSER_RAN=0
# Coverage floor: the mechanical sections alone clear this. Named once so
# summary_verdict and the message it prints can never drift apart.
PASS_FLOOR=18

# Resolve the script's own dir before any cd — section 4 loads the chord
# receiver from it, and the test cd's into a scratch project dir below.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

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

# summary_verdict echoes the exit code a run's counters imply: 0 all good,
# 1 a real failure or an unmet floor, 2 nothing failed but the required
# parser section could not run. It is a pure function of (fail, pass,
# parser_ran) so the PRECEDENCE — a real failure outranks a could-not-run —
# is drivable from stub counters. Deciding this inline made exit 2 reachable
# only on a machine without python3, which is to say never on the machines
# that run this, and an inverted order would have gone unnoticed.
summary_verdict() {
  local fail=$1 pass=$2 parser=$3
  [ "$fail" -eq 0 ] || { echo 1; return; }
  [ "$pass" -ge "$PASS_FLOOR" ] || { echo 1; return; }
  [ "$parser" -eq 1 ] || { echo 2; return; }
  echo 0
}

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

# The agent pane runs the chord receiver (MUX-163), not cat: cat echoes every
# byte, so a first character fused into a Meta chord would still read back
# whole. The receiver logs what a pending-ESC parser actually saw, so the inject
# preamble's absorber can be verified to keep the payload's first char intact.
#
# The receiver is a REPO file, so its absence is a regression, not an
# environment gap: a missing receiver FAILS rather than falling back to a weaker
# check that would let a deleted receiver read green. python3 is an external
# prerequisite, so its absence is an honest skip. Section 4's two parser checks
# are the point of this section — nothing substitutes for them.
INJ_PAYLOAD='- dash inject probe mux163'
RECEIVER="$SCRIPT_DIR/lib/escape-chord-receiver.py"
if [ ! -f "$RECEIVER" ]; then
  fail "chord receiver missing: $RECEIVER — a deleted receiver must not read green"
  tmux send-keys -t "$SURFACE" -l -- "$INJ_PAYLOAD"; sleep 0.5
  tmux send-keys -t "$SURFACE" Enter; sleep 2
  receipt_cap=$(tmux capture-pane -t "$SURFACE" -pJ | strip_ansi)
elif ! command -v python3 >/dev/null 2>&1; then
  skip "chord-receiver inject check — python3 not installed (parser coverage did not run)"
  skip "chord-receiver negative control — python3 not installed"
  tmux send-keys -t "$SURFACE" -l -- "$INJ_PAYLOAD"; sleep 0.5
  tmux send-keys -t "$SURFACE" Enter; sleep 2
  receipt_cap=$(tmux capture-pane -t "$SURFACE" -pJ | strip_ansi)
else
  inj_log="$WORK/inject-chord.log"
  tmux respawn-pane -k -t "$SESSION:edit.1" "python3 '$RECEIVER' '$inj_log'"
  sleep 0.8
  tmux send-keys -t "$SURFACE" -l -- "$INJ_PAYLOAD"
  sleep 0.5
  tmux send-keys -t "$SURFACE" Enter
  sleep 2
  # Capture the surface receipt now, before the negative control respawns the
  # pane — the "injected to edit" notice is transient.
  receipt_cap=$(tmux capture-pane -t "$SURFACE" -pJ | strip_ansi)
  echo "  [diag] chord log: $(tr '\n' '|' <"$inj_log" 2>/dev/null)"
  # Reconstruct the typed text from the receiver's per-key log: the preamble
  # (chord M-C-e) and the submit (key Enter) drop out, leaving the payload.
  got=$(python3 -c 'import sys
out=[]
for ln in open(sys.argv[1]):
    ln = ln.rstrip("\n")
    if ln.startswith("key "):
        k = ln[4:]
        if k == "Space": out.append(" ")
        elif len(k) == 1: out.append(k)
print("".join(out))' "$inj_log" 2>/dev/null)
  if [ "$got" = "$INJ_PAYLOAD" ] && ! grep -qxF "chord M--" "$inj_log"; then
    ok "injected payload arrives whole at a pending-ESC receiver, first char plain"
  else
    fail "injected payload mangled: got '$got' want '$INJ_PAYLOAD'; log $(tr '\n' '|' <"$inj_log")"
  fi

  # Negative control: hand-drive the pre-fix shape (Escape straight into the
  # literal, no absorber) so the receiver is proven able to SEE the defect.
  neg_log="$WORK/inject-defect.log"
  tmux respawn-pane -k -t "$SESSION:edit.1" "python3 '$RECEIVER' '$neg_log'"
  sleep 0.8
  tmux send-keys -t "$SESSION:edit.1" Escape
  tmux send-keys -t "$SESSION:edit.1" -l -- '- dash inject probe'
  sleep 0.5
  if grep -qxF "chord M--" "$neg_log"; then
    ok "negative control: pre-fix Escape→payload fuses the first char (defect seen)"
  else
    fail "receiver must see the defect on the pre-fix shape; log $(tr '\n' '|' <"$neg_log")"
  fi
  PARSER_RAN=1
fi
# The surface confirms an accepted inject by CLEARING its input (the "⇒ injected
# to edit" notice is transient and not reliably in a headless capture). A failed
# inject keeps the input, so an empty input line is the observable receipt: the
# payload no longer appears anywhere on the surface.
if printf '%s' "$receipt_cap" | grep -qF -- "$INJ_PAYLOAD"; then
  fail "surface input not cleared after inject — submit not accepted"
else
  ok "surface input cleared after inject (submit accepted)"
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
# Gate on the precondition of the backend that will actually RUN — the
# default is the opencode gateway, and gating on ollama there let a
# gateway-only machine skip the very path it defaults to and still read
# OK-with-skips (plan's catch, 2026-08-27).
live_reason=""
command -v muxcode-llm-harness >/dev/null 2>&1 || live_reason="muxcode-llm-harness not installed"
backend="${MUXCODE_PROMPT_BACKEND:-opencode}"
if [ -z "$live_reason" ] && [ "$backend" = "ollama" ]; then
  command -v ollama >/dev/null 2>&1 || live_reason="ollama not installed"
  if [ -z "$live_reason" ]; then
    ollama list 2>/dev/null | awk '{print $1}' | grep -qx "qwen3:4b" || live_reason="qwen3:4b not pulled (ollama list)"
  fi
fi
if [ -z "$live_reason" ] && [ "$backend" != "ollama" ]; then
  key="${MUXCODE_OPENCODE_API_KEY:-}"
  if [ -z "$key" ] && [ -f "$HOME/.config/muxcode/config" ]; then
    key="$(sed -n 's/^MUXCODE_OPENCODE_API_KEY=//p' "$HOME/.config/muxcode/config" | head -1)"
  fi
  [ -n "$key" ] || live_reason="gateway backend with no MUXCODE_OPENCODE_API_KEY (env or ~/.config/muxcode/config)"
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

  # Since MUX-144 a gate is released only by an authorized approver, and the
  # compiled default is the user alone. The prompt agent inherits the session's
  # environment rather than this script's, so this reads the variable instead of
  # setting it: with prompt unauthorized the approve intent is refused by
  # design, and asserting a release would be asserting the hole is still open.
  case ",${MUXCODE_GATE_AUTHORITY_ROLES:-},"  in
    *,prompt,*) ;;
    *) skip "approve intent: prompt holds no gate authority (MUX-144) — gate release not exercised"; waiting="" ;;
  esac

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
# ── Verdict self-check, against controlled counters ──────────
# A real run reaches exactly one of summary_verdict's branches, so the others
# are only correct by inspection unless driven. The last case is the one that
# matters: fail>0 AND parser_ran=0 must report the FAILURE (1), never the
# weaker could-not-run (2) — the criterion's "after honouring real failures".
#
# These count in their OWN tallies, never ok/fail. They test this script's
# bookkeeping, not the product, and folding them into PASS would inflate it by
# five — leaving PASS_FLOOR cleared by five fewer real integration checks than
# it was written to demand, which is the floor quietly weakening itself.
echo "-- verdict precedence"
VPASS=0
VFAIL=0
while read -r f p r want label; do
  got=$(summary_verdict "$f" "$p" "$r")
  if [ "$got" = "$want" ]; then
    VPASS=$((VPASS + 1)); echo "  ok: verdict($f,$p,$r) = $want — $label"
  else
    VFAIL=$((VFAIL + 1)); echo "  FAIL: verdict($f,$p,$r) = $got, want $want — $label"
  fi
done <<EOF
0 26 1 0 clean run
1 26 1 1 a real failure
0 10 1 1 floor not met
0 26 0 2 parser section could not run
1 26 0 1 failure outranks could-not-run
EOF
# Exact count, not just zero failures: a truncated heredoc would otherwise
# report a clean self-check having driven nothing.
if [ "$VFAIL" -ne 0 ] || [ "$VPASS" -ne 5 ]; then
  echo "FAIL: verdict self-check ($VPASS/5 passed, $VFAIL failed) — the exit-code precedence rule is broken"
  exit 1
fi

echo ""
echo "=== $PASS passed, $FAIL failed, $SKIP skipped ==="
case "$(summary_verdict "$FAIL" "$PASS" "$PARSER_RAN")" in
  1)
    [ "$FAIL" -eq 0 ] && echo "FAIL: coverage floor not met ($PASS < $PASS_FLOOR)"
    exit 1
    ;;
  2)
    echo "INCOMPLETE: section 4's chord-receiver checks did not run (python3 absent)."
    echo "  Nothing failed, but the MUX-163 parser coverage this script exists for is missing."
    exit 2
    ;;
esac
if [ "$SKIP" -gt 0 ]; then
  echo "OK (with $SKIP skipped — live-model checks did not run on this machine)"
else
  echo "OK"
fi
