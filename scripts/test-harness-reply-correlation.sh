#!/usr/bin/env bash
# Integration test for MUX-111 — the local LLM harness answers each request in
# a batch under that request's own id, never the batch's last message.
#
# Hermetic section (no Ollama): a real muxcode bus built from THIS tree —
# scratch HOME, state and lifecycle log, a private tmux server holding the
# session, and a scratch daemon that completes tracked tasks
# (checkTrackedTasks). The harness runs from a scratch copy of its module
# carrying one extra test, TestDriveBatch: the real ConsumeInbox reads
# commit's inbox and the real processBatch answers it, with a stub model
# through the harness's *http.Client seam (newPipeServer — no bound socket).
#
# Sections: (A) [tracked request, response] — the reply goes to edit under the
# request's id, the request reaches responded, edit's tracked task completes,
# nothing goes to the response's sender, commit's own inbox holds no reply to
# itself, and diagnose finds no receipt gap to re-drive; (B) two tracked
# requests from two senders — one reply each under its own id, both responded,
# both tasks completed; (C) two tracked requests from one sender answered with
# the same text — both delivered (not braked as a repeat, MUX-175) and both
# completed; (D) control — a copy with the request selection reverted to the
# batch's last message correlates to the response and leaves the request
# un-responded; (F) a tracked request beside commit's own request:startup
# bootstrap and a self-addressed response planted in its inbox (an older
# binary's row) — the self-response never reaches the batch or gets a reply,
# the request is answered to edit, and commit's inbox ends holding nothing
# from commit; (E) model-driven — sections A, B and C's batches through a
# real Ollama model, each request answered under its own id and responded,
# skipped with a reason when no endpoint or model answers (MUX111_OLLAMA_URL,
# default http://localhost:11434; MUX111_OLLAMA_MODEL, default the endpoint's
# first model). The coverage floor counts the hermetic checks alone, taken
# before E, so model checks can never make up for a skipped hermetic one.
# Requires go, tmux, jq, curl.
set -euo pipefail
export MUXCODE_CONSUMER_GUARD=0 # commit is played by the harness driver, which no window shows

PASS=0
FAIL=0
for t in tmux go jq curl; do
  command -v "$t" >/dev/null 2>&1 || { echo "SKIP: $t is required"; exit 2; }
done

REPO=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
WORK=$(mktemp -d /tmp/harness-reply-XXXXXX)
SESSION="harness-reply-$$"
. "$REPO/scripts/lib/private-tmux.sh"
private_tmux_init "$WORK/tmux"
BUSDIR=""

export BUS_SESSION="$SESSION" AGENT_ROLE=edit BUS_ROLE=edit
export HOME="$WORK/home" XDG_STATE_HOME="$WORK/state" MUXCODE_LIFECYCLE_LOG_DIR="$WORK/lifecycle"
export MUXCODE_AGENT_HEALTH_CHECK_SECS=3600 MUXCODE_CONTROL_PANE_DISABLE=1 MUXCODE_FORCE_RESPOND_DISABLE=1
export MUXCODE_AGENTDEFS_WATCH_DISABLE=1 MUXCODE_TMP_CLEANUP_THRESHOLD=0 MUXCODE_ACTIVE_WATCHDOG_SECS=0 MUXCODE_PROMPT_AGENT_DISABLE=1
mkdir -p "$HOME" "$XDG_STATE_HOME"

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

# send FROM TO ACTION PAYLOAD [--track|--type response] — a bus send as FROM.
# --force skips the CLI's 30 s same-sender/target/action dedup, sender-side
# policy that section C's two requests from one sender would otherwise trip;
# a failed send is a recorded FAIL with its output, never a silent abort.
send() {
  local from=$1 out; shift
  out=$(AGENT_ROLE=$from "$MUX" send "$@" --force 2>&1) || fail "send as $from $*: $out"
  return 0
}
id_of() { jq -r --arg p "$1" 'select(.payload == $p) | .id' "$BUSDIR/log.jsonl" | tail -1; }
reply_to() { jq -c --arg r "$1" 'select(.from == "commit" and .type == "response" and .reply_to == $r)' "$BUSDIR/log.jsonl"; }
status_of() { jq -r .status "$BUSDIR/delivery/$1.status" 2>/dev/null || echo none; }
task_of() { jq -r .status "$BUSDIR/tasks/$1.json" 2>/dev/null || echo none; }
task_done() { [ "$(task_of "$1")" = completed ]; }
# answered_as ID TO [ACTION] — ID's reply went to TO (with ACTION) and ID responded.
answered_as() {
  local r; r=$(reply_to "$1")
  [ "$(echo "$r" | jq -r .to)" = "$2" ] && { [ -z "${3:-}" ] || [ "$(echo "$r" | jq -r .action)" = "$3" ]; } \
    && [ "$(status_of "$1")" = responded ]
}
# drive DIR [MODEL_URL MODEL] — run the harness driver from module copy DIR.
drive() {
  (cd "$1" && DRIVE_MUX="$MUX" DRIVE_SESSION="$SESSION" DRIVE_MODEL_URL="${2:-}" DRIVE_MODEL="${3:-}" \
    go test ./harness -run '^TestDriveBatch$' -count=1 -v >"$WORK/drive.log" 2>&1)
}
wait_for() {
  local secs=$1 desc=$2 i=0; shift 2
  while [ "$i" -lt "$secs" ]; do "$@" >/dev/null 2>&1 && { ok "$desc"; return 0; }; sleep 1; i=$((i + 1)); done
  fail "$desc (timeout ${secs}s)"; return 0
}

echo "=== MUX-111 harness reply correlation integration test ==="
(cd "$REPO/tools/muxcode" && go build -buildvcs=false -o "$WORK/muxcode" .) || { echo "SKIP: go build muxcode failed"; exit 2; }
MUX="$WORK/muxcode"
BUSDIR=$("$MUX" bus-dir "$SESSION")

# The driver copy (tree code + TestDriveBatch) and the control copy (the same,
# with batchRequests reverted to the batch's last message).
DRIVER=$(cat <<'GO'
package harness

import (
	"context"
	"os"
	"testing"
)

// TestDriveBatch is scripts/test-harness-reply-correlation.sh's door to the
// real ConsumeInbox and processBatch; it exists only in that script's copies.
func TestDriveBatch(t *testing.T) {
	bus := &BusClient{Session: os.Getenv("DRIVE_SESSION"), Role: "commit", BinPath: os.Getenv("DRIVE_MUX")}
	msgs, err := bus.ConsumeInbox()
	if err != nil || len(msgs) == 0 {
		t.Fatalf("consumed %d messages: %v", len(msgs), err)
	}
	for _, m := range msgs {
		t.Logf("batch: %s %s/%s from %s", m.ID, m.Type, m.Action, m.From)
	}
	model := textModel(t)
	if u := os.Getenv("DRIVE_MODEL_URL"); u != "" {
		model = NewOllamaClient(u, os.Getenv("DRIVE_MODEL"))
	}
	cfg := Config{Role: "commit", Session: bus.Session, BusDir: t.TempDir(), MaxTurns: 3}
	processBatch(context.Background(), cfg, bus, model, NewExecutor(nil), BuildToolDefs(nil),
		"You are the commit agent. Answer the task in one short sentence.", NewFilter("commit"), msgs, &mockEventSink{}, nil)
}
GO
)
for d in driver control; do
  cp -R "$REPO/tools/muxcode-llm-harness" "$WORK/$d"
  printf '%s\n' "$DRIVER" >"$WORK/$d/harness/zz_drive_test.go"
done
perl -0pi -e 's/\treturn reqs\n\}/\tif len(msgs) == 0 {\n\t\treturn nil\n\t}\n\treturn msgs[len(msgs)-1:]\n}/' "$WORK/control/harness/loop.go"
if grep -q 'return msgs\[len(msgs)-1:\]' "$WORK/control/harness/loop.go"; then
  ok "built: muxcode from this tree; harness driver and last-message control copies"
else
  echo "SKIP: the control revert did not apply"; exit 2
fi

tmux new-session -d -s "$SESSION" -n commit -c "$WORK"
"$MUX" init "$SESSION" >/dev/null 2>&1 || true
"$MUX" watch "$SESSION" --poll 1 >"$WORK/daemon.log" 2>&1 &
DPID=$!
sleep 1
kill -0 "$DPID" 2>/dev/null && ok "scratch daemon running" || fail "scratch daemon started"

# ── A: [request, response] ───────────────────────────────────────
echo "-- A: a tracked request and a response in one batch"
send edit commit commit "MUX111-A commit the change" --track
send build commit build "MUX111-A build OK" --type response
RA=$(id_of "MUX111-A commit the change"); PA=$(id_of "MUX111-A build OK")
drive "$WORK/driver" && ok "A: the harness consumed and answered the batch" || { fail "A: driver failed"; tail -6 "$WORK/drive.log"; }
REPLY=$(reply_to "$RA")
[ -n "$REPLY" ] && [ "$(echo "$REPLY" | jq -r .to)" = edit ] && [ "$(echo "$REPLY" | jq -r .action)" = commit ] \
  && ok "A: the reply went to edit with the request's action, under the request's id" || fail "A: reply for $RA: '$REPLY'"
[ -z "$(reply_to "$PA")" ] && ok "A: nothing correlated to the response" || fail "A: a reply correlated to the response"
[ -z "$(jq -c 'select(.from == "commit" and .to == "build")' "$BUSDIR/log.jsonl")" ] && ok "A: nothing sent to build" || fail "A: a reply went to build"
[ "$(status_of "$RA")" = responded ] && ok "A: the request reached responded" || fail "A: request status $(status_of "$RA")"
wait_for 15 "A: edit's tracked task completed (the daemon's checkTrackedTasks)" task_done "$RA"
[ -z "$(jq -c 'select(.from == "commit" and .to == "commit")' "$BUSDIR/log.jsonl")" ] \
  && ok "A: the harness sent nothing addressed to itself (bus log)" || fail "A: a commit → commit message: $(jq -c 'select(.from == "commit" and .to == "commit")' "$BUSDIR/log.jsonl" | head -1)"
set +e; "$MUX" diagnose commit --json >"$WORK/diag.json" 2>/dev/null; set -e
jq -e '[.findings[]? | select(.failure_mode == "receipt-gap")] | length == 0' "$WORK/diag.json" >/dev/null \
  && ok "A: diagnose finds no receipt gap — the delivery backstop has nothing to re-drive" \
  || fail "A: receipt-gap: $(jq -c '.findings[]? | select(.failure_mode == "receipt-gap")' "$WORK/diag.json")"

# ── B: two requests, two senders ─────────────────────────────────
echo "-- B: tracked requests from edit and plan in one batch"
send edit commit commit "MUX111-B commit it" --track
send plan commit pr-read "MUX111-B read the PR" --track
RB1=$(id_of "MUX111-B commit it"); RB2=$(id_of "MUX111-B read the PR")
drive "$WORK/driver" && ok "B: the harness answered the batch" || { fail "B: driver failed"; tail -6 "$WORK/drive.log"; }
[ "$(reply_to "$RB1" | jq -r .to)" = edit ] && ok "B: edit's request answered to edit under its own id" || fail "B: reply for $RB1: '$(reply_to "$RB1")'"
[ "$(reply_to "$RB2" | jq -r .to)" = plan ] && [ "$(reply_to "$RB2" | jq -r .action)" = pr-read ] \
  && ok "B: plan's request answered to plan, action pr-read, under its own id" || fail "B: reply for $RB2: '$(reply_to "$RB2")'"
[ "$(status_of "$RB1")" = responded ] && [ "$(status_of "$RB2")" = responded ] && ok "B: both requests responded" \
  || fail "B: statuses $(status_of "$RB1") / $(status_of "$RB2")"
wait_for 15 "B: edit's task completed" task_done "$RB1"
wait_for 15 "B: plan's task completed" task_done "$RB2"

# ── C: two requests, one sender, the same answer ─────────────────
echo "-- C: two tracked requests from edit, answered with the same text"
send edit commit commit "MUX111-C first" --track
send edit commit commit "MUX111-C second" --track
RC1=$(id_of "MUX111-C first"); RC2=$(id_of "MUX111-C second")
drive "$WORK/driver" && ok "C: the harness answered the batch" || { fail "C: driver failed"; tail -6 "$WORK/drive.log"; }
[ -n "$(reply_to "$RC1")" ] && [ -n "$(reply_to "$RC2")" ] && ok "C: both replies on the bus, each under its own id" \
  || fail "C: replies: '$(reply_to "$RC1")' / '$(reply_to "$RC2")'"
[ "$(reply_to "$RC1" | jq -r .payload)" = "$(reply_to "$RC2" | jq -r .payload)" ] && ok "C: the two replies carry the same text" || fail "C: the reply texts differ"
grep -q "response-suppressed" <("$MUX" lifecycle show "$SESSION" --limit 1000 2>/dev/null) \
  && fail "C: a reply was braked as a repeat (response-suppressed)" || ok "C: neither reply was braked as a repeat"
[ "$(status_of "$RC1")" = responded ] && [ "$(status_of "$RC2")" = responded ] && ok "C: both requests responded" \
  || fail "C: statuses $(status_of "$RC1") / $(status_of "$RC2")"
wait_for 15 "C: both of edit's tasks completed" bash -c "[ \"\$(jq -r .status '$BUSDIR/tasks/$RC1.json')\" = completed ] && [ \"\$(jq -r .status '$BUSDIR/tasks/$RC2.json')\" = completed ]"

# ── D: control — the last message again ──────────────────────────
echo "-- D: control — request selection reverted to the batch's last message"
send edit commit commit "MUX111-D commit it" --track
send build commit build "MUX111-D build OK" --type response
RD=$(id_of "MUX111-D commit it"); PD=$(id_of "MUX111-D build OK")
drive "$WORK/control" && ok "D: the control harness answered the batch" || { fail "D: control driver failed"; tail -6 "$WORK/drive.log"; }
[ -n "$(reply_to "$PD")" ] && [ -z "$(reply_to "$RD")" ] && ok "D: the control correlated its reply to the response, not the request" \
  || fail "D: control replies: request '$(reply_to "$RD")' response '$(reply_to "$PD")'"
[ "$(status_of "$RD")" != responded ] && ok "D: the control left the request un-responded ($(status_of "$RD"))" || fail "D: the control's request reached responded"

# ── F: self-addressed messages in the batch ──────────────────────
echo "-- F: a tracked request beside commit's own bootstrap and a planted self-response"
send edit commit commit "MUX111-F commit it" --track
send commit commit startup "MUX111-F bootstrap"
RF=$(id_of "MUX111-F commit it"); BF=$(id_of "MUX111-F bootstrap")
printf '%s\n' '{"id":"MUX111-F-SELF","ts":1,"from":"commit","to":"commit","type":"response","action":"commit","payload":"MUX111-F my own earlier reply"}' >>"$BUSDIR/inbox/commit.jsonl"
[ -n "$BF" ] && grep -q MUX111-F-SELF "$BUSDIR/inbox/commit.jsonl" \
  && ok "F: commit's inbox holds the request, its startup bootstrap and a planted self-response" || fail "F: setup (bootstrap '$BF')"
drive "$WORK/driver" && ok "F: the harness answered the batch" || { fail "F: driver failed"; tail -6 "$WORK/drive.log"; }
grep -q 'MUX111-F-SELF' "$WORK/drive.log" && fail "F: the planted self-response reached the harness batch" || ok "F: the self-response never reached the batch"
[ "$(reply_to "$RF" | jq -r .to)" = edit ] && [ "$(status_of "$RF")" = responded ] \
  && ok "F: the request was answered to edit under its own id and responded" || fail "F: reply '$(reply_to "$RF")', status $(status_of "$RF")"
[ -z "$(reply_to MUX111-F-SELF)" ] && ok "F: nothing answered the self-response" || fail "F: a reply correlated to the self-response"
[ -z "$(jq -c 'select(.from == "commit")' "$BUSDIR/inbox/commit.jsonl" 2>/dev/null)" ] \
  && ok "F: commit's own inbox holds no message from commit afterwards" || fail "F: commit's inbox: $(jq -c 'select(.from == "commit") | {id, type, action}' "$BUSDIR/inbox/commit.jsonl" | tr '\n' ' ')"
wait_for 15 "F: edit's task completed" task_done "$RF"

# The coverage floor counts the hermetic sections alone: a model run's checks
# must never make up for a skipped hermetic one.
HERMETIC_PASS=$PASS

# ── E: model-driven ──────────────────────────────────────────────
echo "-- E: sections A, B and C's batches through a real Ollama model"
OLLAMA_URL=${MUX111_OLLAMA_URL:-http://localhost:11434}
MODEL=${MUX111_OLLAMA_MODEL:-}
if ! TAGS=$(curl -sf --max-time 5 "$OLLAMA_URL/api/tags" 2>/dev/null); then
  skip "E: no Ollama endpoint answers at $OLLAMA_URL"
else
  [ -n "$MODEL" ] || MODEL=$(echo "$TAGS" | jq -r '.models[0].name // empty')
  if [ -z "$MODEL" ]; then
    skip "E: the endpoint at $OLLAMA_URL lists no model"
  else
    send edit commit commit "MUX111-E1 say done" --track
    send build commit build "MUX111-E1 build OK" --type response
    RE1=$(id_of "MUX111-E1 say done")
    drive "$WORK/driver" "$OLLAMA_URL" "$MODEL" || true
    answered_as "$RE1" edit commit && ok "E: [request, response] with $MODEL — answered to edit under the request's id, responded" \
      || fail "E: [request, response] with $MODEL: reply '$(reply_to "$RE1")', status $(status_of "$RE1")"

    send edit commit commit "MUX111-E2 say done" --track
    send plan commit pr-read "MUX111-E2 say read" --track
    RE2=$(id_of "MUX111-E2 say done"); RE3=$(id_of "MUX111-E2 say read")
    drive "$WORK/driver" "$OLLAMA_URL" "$MODEL" || true
    answered_as "$RE2" edit commit && answered_as "$RE3" plan pr-read \
      && ok "E: two senders with $MODEL — each answered under its own id, both responded" \
      || fail "E: two senders with $MODEL: '$(reply_to "$RE2")' / '$(reply_to "$RE3")'"

    send edit commit commit "MUX111-E4 first" --track
    send edit commit commit "MUX111-E4 second" --track
    RE4=$(id_of "MUX111-E4 first"); RE5=$(id_of "MUX111-E4 second")
    drive "$WORK/driver" "$OLLAMA_URL" "$MODEL" || true
    answered_as "$RE4" edit && answered_as "$RE5" edit \
      && ok "E: one sender twice with $MODEL — each answered once under its own id, both responded" \
      || fail "E: one sender twice with $MODEL: '$(reply_to "$RE4")' / '$(reply_to "$RE5")'"
  fi
fi

echo "=== $PASS passed, $FAIL failed ($HERMETIC_PASS hermetic) ==="
[ "$HERMETIC_PASS" -ge 32 ] || { echo "FAIL: coverage floor not met ($HERMETIC_PASS < 32 hermetic checks)"; exit 1; }
[ "$FAIL" -eq 0 ] || exit 1
echo "PASS"
