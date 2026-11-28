#!/usr/bin/env bash
# Integration test for MUX-010 — delegation message hygiene on the real CLI.
#
# Hermetic: muxcode built from THIS tree, a scratch HOME, state and bus, no
# daemon and no tmux; the script plays the receiver (plan). Every send's
# stdout and stderr are captured to separate files, so the warning block and
# the --wait heartbeat are observed on stderr itself.
#
# Sections: (A) an inline payload past 500 characters, and one carrying a
# newline, each print the `!!` warning block on stderr and are delivered;
# (B) the same two under MUXCODE_SEND_STRICT=1 are NOT SENT, exit 1, absent
# from plan's inbox and the bus log; (C) a long multi-line payload sent with
# --payload-file, and the same through stdin (-), print no warning and arrive
# byte for byte, and plan's `muxcode inbox` shows every line of it with the
# continuation lines indented, while --payload-file given twice, or naming a
# missing file, exits 1 and writes nothing; (D) --wait on a request plan answers after
# ~35 s prints at least one `still waiting on plan (Ns elapsed)` heartbeat on
# stderr and the reply on stdout, and no heartbeat on stdout; (E) negative
# control — a compliant short request answered at once prints no warning and
# no heartbeat. The heartbeat interval is the real 30 s (it has no override),
# so the script takes about a minute. A coverage floor keeps a skipped
# section from reporting green. Requires go, jq.
set -euo pipefail
export MUXCODE_CONSUMER_GUARD=0 # plan is played by this script, which no window or listener shows

PASS=0
FAIL=0
for t in go jq; do
  command -v "$t" >/dev/null 2>&1 || { echo "SKIP: $t is required"; exit 2; }
done

REPO=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
WORK=$(mktemp -d /tmp/send-hygiene-XXXXXX)
SESSION="send-hygiene-$$"
BUSDIR=""
export BUS_SESSION="$SESSION" AGENT_ROLE=edit BUS_ROLE=edit
export HOME="$WORK/home" XDG_STATE_HOME="$WORK/state" MUXCODE_LIFECYCLE_LOG_DIR="$WORK/lifecycle"
export MUXCODE_SEND_STRICT=
unset TMUX
mkdir -p "$HOME" "$XDG_STATE_HOME"

ANSWERER=""
cleanup() {
  set +e
  [ -n "$ANSWERER" ] && kill "$ANSWERER" 2>/dev/null
  rm -rf ${BUSDIR:+"$BUSDIR"} "$WORK" 2>/dev/null
  true
}
trap cleanup EXIT

ok()   { PASS=$((PASS + 1)); echo "  ok: $1"; }
fail() { FAIL=$((FAIL + 1)); echo "  FAIL: $1"; }

# send NAME ARGS... — `muxcode send` as edit; stdout, stderr and exit code land
# in $WORK/NAME.out, .err and .rc. Each section's sends use their own action:
# the CLI refuses a repeat of one (from, to, action) inside its 30 s dedup
# window, which is not what this script tests.
send() {
  local name=$1; shift
  set +e
  "$MUX" send "$@" >"$WORK/$name.out" 2>"$WORK/$name.err"
  echo $? >"$WORK/$name.rc"
  set -e
}
rc()       { cat "$WORK/$1.rc"; }
warned()   { grep -q '^!! muxcode send: WARNING' "$WORK/$1.err"; }
beats()    { grep -c '^still waiting on plan (' "$WORK/$1.err" || true; }
in_inbox() { AGENT_ROLE=plan "$MUX" inbox --peek --raw 2>/dev/null | jq -e --arg p "$1" 'select(.payload == $p)' >/dev/null; }
in_log()   { jq -e --arg p "$1" 'select(.payload == $p)' "$BUSDIR/log.jsonl" >/dev/null 2>&1; }
request_id() { jq -r --arg p "$1" 'select(.payload == $p) | .id' "$BUSDIR/log.jsonl" 2>/dev/null | tail -1; }
# answer_after SECS PAYLOAD — plan answers the request carrying PAYLOAD once it
# is logged, SECS later, correlated to it.
answer_after() {
  local secs=$1 payload=$2 id="" i=0
  while [ -z "$id" ] && [ "$i" -lt 100 ]; do id=$(request_id "$payload"); [ -n "$id" ] || sleep 0.1; i=$((i + 1)); done
  sleep "$secs"
  AGENT_ROLE=plan "$MUX" send edit plan "answered: $payload" --type response --reply-to "$id" >/dev/null 2>&1
}

echo "=== MUX-010 send hygiene integration test ==="
(cd "$REPO/tools/muxcode" && go build -buildvcs=false -o "$WORK/muxcode" .) || { echo "SKIP: go build muxcode failed"; exit 2; }
MUX="$WORK/muxcode"
BUSDIR=$("$MUX" bus-dir "$SESSION")
"$MUX" init "$SESSION" >/dev/null 2>&1 || true
ok "muxcode built from this tree, scratch bus initialised"

LONG=$(printf 'x%.0s' $(seq 1 520))
NL=$'first line\nsecond line'

# ── A: inline payloads the warning names ─────────────────────────
echo "-- A: inline payload too long, and one with a newline — warned and delivered"
send a-long plan note-a-long "$LONG" --type event --no-notify
send a-nl plan note-a-nl "$NL" --type event --no-notify
warned a-long && grep -q 'cause: payload is 520 chars (>500)' "$WORK/a-long.err" && ok "A: >500 chars — the warning block on stderr names the cause" || fail "A: long stderr: $(cat "$WORK/a-long.err")"
warned a-nl && grep -q 'cause: payload contains newlines' "$WORK/a-nl.err" && ok "A: newline — the warning block on stderr names the cause" || fail "A: newline stderr: $(cat "$WORK/a-nl.err")"
grep -q 'Bash(muxcode \*) permission glob' "$WORK/a-long.err" && grep -q -- '--payload-file <path>' "$WORK/a-long.err" && grep -q '/tmp/<name>.md' "$WORK/a-long.err" \
  && ok "A: the block names the consequence and both remedies" || fail "A: block incomplete: $(cat "$WORK/a-long.err")"
! grep -q '^!!' "$WORK/a-long.out" "$WORK/a-nl.out" && ok "A: the warning never reaches stdout" || fail "A: warning on stdout"
[ "$(rc a-long)" = 0 ] && [ "$(rc a-nl)" = 0 ] && in_inbox "$LONG" && in_inbox "$NL" && ok "A: both delivered to plan, exit 0" || fail "A: rc $(rc a-long)/$(rc a-nl)"

# ── B: the same under strict ─────────────────────────────────────
echo "-- B: the same payloads under MUXCODE_SEND_STRICT=1 — NOT SENT"
LONG_B="$LONG-strict"; NL_B="$NL strict"
MUXCODE_SEND_STRICT=1 send b-long plan note-b-long "$LONG_B" --type event --no-notify
MUXCODE_SEND_STRICT=1 send b-nl plan note-b-nl "$NL_B" --type event --no-notify
[ "$(rc b-long)" = 1 ] && grep -q '^NOT SENT' "$WORK/b-long.out" && ok "B: >500 chars — NOT SENT, exit 1" || fail "B: long rc $(rc b-long): $(cat "$WORK/b-long.out")"
[ "$(rc b-nl)" = 1 ] && grep -q '^NOT SENT' "$WORK/b-nl.out" && ok "B: newline — NOT SENT, exit 1" || fail "B: newline rc $(rc b-nl): $(cat "$WORK/b-nl.out")"
warned b-long && ok "B: the warning still prints before the refusal" || fail "B: no warning before the refusal"
! in_inbox "$LONG_B" && ! in_inbox "$NL_B" && ! in_log "$LONG_B" && ! in_log "$NL_B" \
  && ok "B: neither refused payload is in plan's inbox or the bus log" || fail "B: a refused payload was written"

# ── C: by reference ──────────────────────────────────────────────
echo "-- C: a long multi-line payload by --payload-file and by stdin"
{ for i in $(seq 1 20); do echo "handoff line $i: the content a delegation may carry by reference"; done; } >"$WORK/handoff.md"
FILE_PAYLOAD=$(cat "$WORK/handoff.md"; echo .)
FILE_PAYLOAD=${FILE_PAYLOAD%.}
send c-file plan note-c-file --payload-file "$WORK/handoff.md" --type event --no-notify
send c-stdin plan note-c-stdin - --type event --no-notify <"$WORK/handoff.md"
[ "$(rc c-file)" = 0 ] && ! warned c-file && ok "C: --payload-file sent, exit 0, no warning" || fail "C: file rc $(rc c-file): $(cat "$WORK/c-file.err")"
[ "$(rc c-stdin)" = 0 ] && ! warned c-stdin && ok "C: stdin sent, exit 0, no warning" || fail "C: stdin rc $(rc c-stdin): $(cat "$WORK/c-stdin.err")"
[ "$(AGENT_ROLE=plan "$MUX" inbox --peek --raw | jq -c --arg p "$FILE_PAYLOAD" 'select(.payload == $p)' | wc -l | tr -d ' ')" = 2 ] \
  && ok "C: both arrived byte for byte — the file's content, trailing newline included" || fail "C: payloads not byte-identical in plan's inbox"
AGENT_ROLE=plan "$MUX" inbox >"$WORK/c-inbox.txt" 2>&1 || true
[ "$(grep -c 'handoff line' "$WORK/c-inbox.txt")" -ge 40 ] && grep -q '^  handoff line 20:' "$WORK/c-inbox.txt" \
  && ok "C: plan's muxcode inbox shows every line, continuation lines indented" || fail "C: inbox render: $(grep -c 'handoff line' "$WORK/c-inbox.txt") lines"
! grep -q '^handoff line' "$WORK/c-inbox.txt" && ok "C: no payload line starts at column 0 (none can pose as a header)" || fail "C: a payload line at column 0"
send c-twice plan note-c-twice --payload-file "$WORK/handoff.md" --payload-file "$WORK/handoff.md" --type event
[ "$(rc c-twice)" = 1 ] && grep -q 'given twice' "$WORK/c-twice.err" && ok "C: --payload-file given twice is refused, exit 1" || fail "C: twice rc $(rc c-twice): $(cat "$WORK/c-twice.err")"
MISSING="$WORK/no-such-handoff.md"
LOG_BEFORE=$(wc -l <"$BUSDIR/log.jsonl" 2>/dev/null | tr -d ' ' || echo 0)
send c-missing plan note-c-missing --payload-file "$MISSING" --type event --no-notify
[ "$(rc c-missing)" = 1 ] && grep -qF "$MISSING" "$WORK/c-missing.err" && grep -q 'nothing sent' "$WORK/c-missing.err" \
  && [ "$(wc -l <"$BUSDIR/log.jsonl" | tr -d ' ')" = "$LOG_BEFORE" ] && ! AGENT_ROLE=plan "$MUX" inbox --peek --raw 2>/dev/null | grep -q note-c-missing \
  && ok "C: a missing --payload-file exits 1 naming its path, nothing in inbox or log" || fail "C: missing rc $(rc c-missing): $(cat "$WORK/c-missing.err")"

# ── D: --wait heartbeat ──────────────────────────────────────────
echo "-- D: --wait on a request answered after ~35 s (the real 30 s heartbeat)"
REQ_D="MUX010-D build the thing"
answer_after 35 "$REQ_D" &
ANSWERER=$!
send d-wait plan plan "$REQ_D" --wait
wait "$ANSWERER" 2>/dev/null || true; ANSWERER=""
[ "$(rc d-wait)" = 0 ] && grep -q "answered: $REQ_D" "$WORK/d-wait.out" && ok "D: the reply arrived on stdout" || fail "D: rc $(rc d-wait), stdout: $(cat "$WORK/d-wait.out")"
[ "$(beats d-wait)" -ge 1 ] && ok "D: $(beats d-wait) heartbeat line(s) on stderr" || fail "D: no heartbeat on stderr: $(cat "$WORK/d-wait.err")"
grep -qE '^still waiting on plan \([0-9]+s elapsed\)$' "$WORK/d-wait.err" && ok "D: the heartbeat reads 'still waiting on plan (Ns elapsed)'" || fail "D: heartbeat format"
! grep -q 'still waiting' "$WORK/d-wait.out" && ok "D: no heartbeat on stdout" || fail "D: heartbeat on stdout"

# ── E: negative control ──────────────────────────────────────────
echo "-- E: control — a compliant short request answered at once"
REQ_E="MUX010-E quick one"
answer_after 0 "$REQ_E" &
ANSWERER=$!
send e-wait plan plan "$REQ_E" --wait
wait "$ANSWERER" 2>/dev/null || true; ANSWERER=""
[ "$(rc e-wait)" = 0 ] && grep -q "answered: $REQ_E" "$WORK/e-wait.out" && ok "E: answered, exit 0" || fail "E: rc $(rc e-wait): $(cat "$WORK/e-wait.out")"
! warned e-wait && [ "$(beats e-wait)" = 0 ] && ok "E: no warning and no heartbeat" || fail "E: stderr: $(cat "$WORK/e-wait.err")"

echo "=== $PASS passed, $FAIL failed ==="
[ "$PASS" -ge 23 ] || { echo "FAIL: coverage floor not met ($PASS < 23)"; exit 1; }
[ "$FAIL" -eq 0 ] || exit 1
echo "PASS"
