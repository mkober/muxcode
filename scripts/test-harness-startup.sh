#!/usr/bin/env bash
# Integration test for MUX-110 — a local-model role's startup is closed-form,
# and a batch the harness cannot answer is terminal and visible.
#
# Hermetic section (no Ollama): a real muxcode bus built from THIS tree —
# scratch HOME, state, install dir and lifecycle log, a private tmux server
# holding the session and a scratch daemon. Startup text comes from the real
# launch road: `muxcode agent launch build` with a stub `muxcode-llm-harness`
# (local) or stub `claude` first on PATH, so PreLaunchSetup seeds the startup
# and the stub is exec'd and exits. The harness runs from a scratch copy of
# its module carrying a TestDriveStartup that calls the real ConsumeInbox and
# processBatch, its model a stub through the *http.Client seam (no socket):
# `loop` never stops calling tools, `text` answers.
#
# Sections: (A) a local build's launch seeds the closed-form startup — and a
# Claude build's the unchanged text (control); (B) the open-ended startup
# under a model that never stops — one harness-tool-loop-exhausted row naming
# build, the startup's id and the turns used, the startup answered with the
# exhaustion text under its id and responded, and with the daemon running
# 10 s more nothing re-delivers it: build's inbox stays empty and no second
# row appears; (C) negative control — the closed-form startup answered within
# budget writes no exhaustion row and is responded, judged by answered_ok, the
# one success predicate C and D share; (C2) its control — a model answering
# HTTP 500 leaves an "Error calling …" reply, which answered_ok rejects, so D
# cannot pass on an unreachable model; (D) model-driven — the
# launch-seeded closed-form startup through a real Ollama model
# (MUX110_OLLAMA_URL, default http://localhost:11434; MUX110_OLLAMA_MODEL,
# default qwen3:4b when listed, else the endpoint's first model): answered
# within budget, responded, no exhaustion row — the wording's validation,
# skipped with a reason when no endpoint or model answers. The coverage floor
# counts the hermetic checks alone. Requires go, tmux, jq, curl.
set -euo pipefail
export MUXCODE_CONSUMER_GUARD=0 # build is played by the harness driver, which no window shows

PASS=0
FAIL=0
for t in tmux go jq curl; do
  command -v "$t" >/dev/null 2>&1 || { echo "SKIP: $t is required"; exit 2; }
done

REPO=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
WORK=$(mktemp -d /tmp/harness-startup-XXXXXX)
SESSION="harness-startup-$$"
. "$REPO/scripts/lib/private-tmux.sh"
private_tmux_init "$WORK/tmux"
BUSDIR=""

export BUS_SESSION="$SESSION" AGENT_ROLE=edit BUS_ROLE=edit
export HOME="$WORK/home" XDG_STATE_HOME="$WORK/state" MUXCODE_LIFECYCLE_LOG_DIR="$WORK/lifecycle"
export MUXCODE_INSTALL_DIR="$WORK/install"
export MUXCODE_AGENT_HEALTH_CHECK_SECS=3600 MUXCODE_CONTROL_PANE_DISABLE=1 MUXCODE_FORCE_RESPOND_DISABLE=1
export MUXCODE_AGENTDEFS_WATCH_DISABLE=1 MUXCODE_TMP_CLEANUP_THRESHOLD=0 MUXCODE_ACTIVE_WATCHDOG_SECS=0 MUXCODE_PROMPT_AGENT_DISABLE=1
mkdir -p "$HOME" "$XDG_STATE_HOME" "$WORK/install/agents" "$WORK/stub-local" "$WORK/stub-claude"

DPID=""
cleanup() {
  set +e
  if [ -n "$DPID" ]; then kill "$DPID" 2>/dev/null; wait "$DPID" 2>/dev/null; fi
  private_tmux_kill
  rm -rf ${BUSDIR:+"$BUSDIR"} "$WORK" 2>/dev/null
  true
}
trap cleanup EXIT

ok()   { PASS=$((PASS + 1)); echo "  ok: $1"; }
fail() { FAIL=$((FAIL + 1)); echo "  FAIL: $1"; }
skip() { echo "  SKIP: $1"; }

CLOSED="Session started — run \`muxcode memory context\` once, then reply with one sentence summarizing what it shows. That reply finishes this task: run nothing else."
OPEN="Session started — review last saved context from memory to restore session state."

events()   { "$MUX" lifecycle show "$SESSION" --limit 1000 2>/dev/null | grep -E "$1" || true; }
n_events() { events "$1" | grep -c . || true; }
startup_payload() { AGENT_ROLE=build "$MUX" inbox --peek --raw 2>/dev/null | jq -r 'select(.action == "startup") | .payload' | tail -1; }
startup_id()      { AGENT_ROLE=build "$MUX" inbox --peek --raw 2>/dev/null | jq -r 'select(.action == "startup") | .id' | tail -1; }
status_of() { jq -r .status "$BUSDIR/delivery/$1.status" 2>/dev/null || echo none; }
reply_to()  { jq -c --arg r "$1" 'select(.type == "response" and .reply_to == $r)' "$BUSDIR/log.jsonl"; }
inbox_empty() { [ -z "$(AGENT_ROLE=build "$MUX" inbox --peek --raw 2>/dev/null)" ]; }
# launch CLI — `muxcode agent launch build` on CLI, its exec'd binary a stub.
launch() {
  local stubdir="$WORK/stub-$1"
  (export PATH="$stubdir:$WORK/bin:$PATH" MUXCODE_BUILD_CLI="$1" AGENT_ROLE=build
   "$MUX" agent launch build --reason restart >"$WORK/launch-$1.log" 2>&1) || true
}
# drive MODEL [URL NAME] — the harness driver as build (loop, text, error or
# ollama), with this tree's muxcode first on PATH so the model's own
# `muxcode memory` calls run it, not an installed binary.
drive() {
  (cd "$WORK/h" && export AGENT_ROLE=build BUS_ROLE=build PATH="$WORK/bin:$PATH" \
    DRIVE_MUX="$MUX" DRIVE_SESSION="$SESSION" DRIVE_MODEL="$1" DRIVE_URL="${2:-}" DRIVE_NAME="${3:-}" &&
    go test ./harness -run '^TestDriveStartup$' -count=1 -v >"$WORK/drive-$1.log" 2>&1)
}
# answered_ok ID MODEL ROWS — the one success predicate for an answered
# startup: the driver passed, the batch counted a success, the startup ID was
# responded with a reply that is no harness error or exhaustion text, and the
# exhaustion rows still number ROWS.
answered_ok() {
  local payload; payload=$(reply_to "$1" | jq -r .payload)
  grep -q -- '^--- PASS: TestDriveStartup' "$WORK/drive-$2.log" && grep -q "batch success=true" "$WORK/drive-$2.log" \
    && [ "$(status_of "$1")" = responded ] && [ -n "$payload" ] \
    && case "$payload" in Error*|"(no response generated"*|"(all tool calls blocked"*) false ;; *) true ;; esac \
    && [ "$(n_events "harness-tool-loop-exhausted")" = "$3" ]
}

echo "=== MUX-110 harness startup integration test ==="
mkdir -p "$WORK/bin"
(cd "$REPO/tools/muxcode" && go build -buildvcs=false -o "$WORK/bin/muxcode" .) || { echo "SKIP: go build muxcode failed"; exit 2; }
MUX="$WORK/bin/muxcode"
BUSDIR=$("$MUX" bus-dir "$SESSION")
printf '#!/bin/sh\nexit 0\n' >"$WORK/stub-local/muxcode-llm-harness"
printf '#!/bin/sh\nexit 0\n' >"$WORK/stub-claude/claude"
chmod +x "$WORK/stub-local/muxcode-llm-harness" "$WORK/stub-claude/claude"
printf -- '---\ndescription: Builder\n---\nBuild things.\n' >"$WORK/install/agents/code-builder.md"

cp -R "$REPO/tools/muxcode-llm-harness" "$WORK/h"
cat >"$WORK/h/harness/zz_drive_startup_test.go" <<'GO'
package harness

import (
	"context"
	"net/http"
	"os"
	"testing"
)

// TestDriveStartup is scripts/test-harness-startup.sh's door to the real
// ConsumeInbox and processBatch; it exists only in that script's copy.
func TestDriveStartup(t *testing.T) {
	bus := &BusClient{Session: os.Getenv("DRIVE_SESSION"), Role: "build", BinPath: os.Getenv("DRIVE_MUX")}
	msgs, err := bus.ConsumeInbox()
	if err != nil || len(msgs) == 0 {
		t.Fatalf("consumed %d messages: %v", len(msgs), err)
	}
	var model *OllamaClient
	switch os.Getenv("DRIVE_MODEL") {
	case "loop":
		model, _ = toolLoopModel(t)
	case "text":
		model = textModel(t)
	case "error":
		server := newPipeServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "model unavailable", http.StatusInternalServerError)
		}))
		t.Cleanup(server.Close)
		model = NewOllamaClient(server.URL, "test-model")
		model.HTTP = server.Client()
	default:
		model = NewOllamaClient(os.Getenv("DRIVE_URL"), os.Getenv("DRIVE_NAME"))
	}
	allowed := []string{"Bash(echo *)", "Bash(muxcode memory *)"}
	cfg := Config{Role: "build", Session: bus.Session, BusDir: t.TempDir(), MaxTurns: 10}
	ok := processBatch(context.Background(), cfg, bus, model, NewExecutor(allowed), BuildToolDefs(allowed),
		"You are the build agent.", NewFilter("build"), msgs, &mockEventSink{}, nil)
	t.Logf("batch success=%v", ok)
}
GO
ok "built: muxcode from this tree, stub launch binaries, the harness driver copy"

tmux new-session -d -s "$SESSION" -n build -c "$WORK"
"$MUX" init "$SESSION" >/dev/null 2>&1 || true
"$MUX" watch "$SESSION" --poll 1 >"$WORK/daemon.log" 2>&1 &
DPID=$!
sleep 1
kill -0 "$DPID" 2>/dev/null && ok "scratch daemon running" || fail "scratch daemon started"

# ── A: the launch seeds the startup by provider ──────────────────
echo "-- A: startup text from the real launch road"
launch local
[ "$(startup_payload)" = "$CLOSED" ] && ok "A: a local build's launch seeds the closed-form startup" \
  || fail "A: local startup '$(startup_payload)' ($(tail -2 "$WORK/launch-local.log" | tr '\n' ' '))"
launch claude
[ "$(startup_payload)" = "$OPEN" ] && ok "A: control — a Claude build's launch seeds the unchanged startup" \
  || fail "A: claude startup '$(startup_payload)' ($(tail -2 "$WORK/launch-claude.log" | tr '\n' ' '))"

# ── B: the open-ended startup, a model that never stops ──────────
echo "-- B: open-ended startup, a model that never stops calling tools"
SB=$(startup_id)
drive loop && ok "B: the harness consumed and answered the startup" || { fail "B: driver failed"; tail -6 "$WORK/drive-loop.log"; }
[ "$(n_events "harness-tool-loop-exhausted")" = 1 ] && ok "B: exactly one harness-tool-loop-exhausted row" || fail "B: rows: $(events harness-tool-loop-exhausted)"
events "harness-tool-loop-exhausted" | grep -qE "build: request $SB answered with the exhaustion text after [0-9]+ of its 10 turns" \
  && ok "B: the row names build, the startup's id and the turns used" || fail "B: row: $(events harness-tool-loop-exhausted)"
[ "$(reply_to "$SB" | jq -r .payload)" = "(no response generated — tool loop exhausted)" ] \
  && ok "B: the startup was answered with the exhaustion text under its own id" || fail "B: reply '$(reply_to "$SB")'"
[ "$(status_of "$SB")" = responded ] && ok "B: the startup reached responded" || fail "B: status $(status_of "$SB")"
grep -q "batch success=false" "$WORK/drive-loop.log" && ok "B: the batch counted as failed for the breaker" || fail "B: $(grep -m1 'batch success' "$WORK/drive-loop.log")"
sleep 10
inbox_empty && ok "B: 10 s later build's inbox is empty — nothing re-delivered the startup" || fail "B: build's inbox: $(AGENT_ROLE=build "$MUX" inbox --peek --raw | jq -c '{id, action}' | tr '\n' ' ')"
[ "$(n_events "harness-tool-loop-exhausted")" = 1 ] && ok "B: still one exhaustion row — the startup was not reprocessed" || fail "B: rows now $(n_events harness-tool-loop-exhausted)"

# ── C: negative control — answered within budget ────────────────
echo "-- C: closed-form startup answered within budget"
launch local
SC=$(startup_id)
[ "$(startup_payload)" = "$CLOSED" ] && ok "C: a fresh local launch seeded the closed-form startup" || fail "C: startup '$(startup_payload)'"
drive text && ok "C: the harness answered it" || { fail "C: driver failed"; tail -6 "$WORK/drive-text.log"; }
answered_ok "$SC" text 1 && [ "$(reply_to "$SC" | jq -r .payload)" = "Done. EXIT=0" ] \
  && ok "C: answered within budget — the model's text, responded, a successful batch, no new exhaustion row" \
  || fail "C: reply '$(reply_to "$SC")', status $(status_of "$SC"), rows $(n_events harness-tool-loop-exhausted), $(grep -m1 'batch success' "$WORK/drive-text.log")"
echo "-- C2: control — a model that errors is not an answered startup"
launch local
SE=$(startup_id)
drive error || true
answered_ok "$SE" error 1 && fail "C2: the success predicate accepted a model error: '$(reply_to "$SE" | jq -r .payload)'" \
  || ok "C2: a model error ('$(reply_to "$SE" | jq -r .payload | cut -c1-50)') is rejected by the success predicate"

HERMETIC_PASS=$PASS

# ── D: model-driven ──────────────────────────────────────────────
echo "-- D: the closed-form startup through a real Ollama model"
OLLAMA_URL=${MUX110_OLLAMA_URL:-http://localhost:11434}
if ! TAGS=$(curl -sf --max-time 5 "$OLLAMA_URL/api/tags" 2>/dev/null); then
  skip "D: no Ollama endpoint answers at $OLLAMA_URL"
else
  MODEL=${MUX110_OLLAMA_MODEL:-}
  [ -n "$MODEL" ] || MODEL=$(echo "$TAGS" | jq -r '[.models[].name] | (map(select(. == "qwen3:4b")) + .)[0] // empty')
  if [ -z "$MODEL" ]; then
    skip "D: the endpoint at $OLLAMA_URL lists no model"
  else
    launch local
    SD=$(startup_id)
    BEFORE=$(n_events "harness-tool-loop-exhausted")
    drive ollama "$OLLAMA_URL" "$MODEL" || true
    answered_ok "$SD" ollama "$BEFORE" \
      && ok "D: with $MODEL the closed-form startup was answered within budget and responded" \
      || fail "D: with $MODEL: reply '$(reply_to "$SD" | jq -r .payload | cut -c1-80)', status $(status_of "$SD"), exhaustion rows $(n_events harness-tool-loop-exhausted)"
  fi
fi

echo "=== $PASS passed, $FAIL failed ($HERMETIC_PASS hermetic) ==="
[ "$HERMETIC_PASS" -ge 16 ] || { echo "FAIL: coverage floor not met ($HERMETIC_PASS < 16 hermetic checks)"; exit 1; }
[ "$FAIL" -eq 0 ] || exit 1
echo "PASS"
