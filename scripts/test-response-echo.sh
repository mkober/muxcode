#!/usr/bin/env bash
# Integration test for MUX-009 — a response delivered to a listenerless agent
# (OpenCode, scrape-road Codex) must never be typed into its pane as a prompt.
#
# On 2026-08-17 build re-sent request:test five times in 3.5 minutes: each
# test answer was injected into build's OpenCode composer and read as "run
# tests". The fix (bus/provider.go splitWakeBatch, settleWakeResponses) drops a
# non-orchestrator's responses from the typed batch, consumed with no receipt
# and logged wake-response-dropped, so a response-only wake types nothing.
#
# Hermetic: scratch bus, private tmux server, muxcode built from THIS tree, no
# daemon — every wake here is the one `muxcode send` itself makes (Notify →
# OpenCodeProvider.SendWakeUp) or a `muxcode deliver --force`. build and test
# are OpenCode roles whose panes run a fake agent: it draws an OpenCode-shaped
# composer, logs every line typed into it, and acts like the agents of
# 2026-08-17 — build sends request:test for ANY typed line (at most three),
# test answers each request it is typed with a response to build.
#
# The pre-fix build is this tree with the response filter patched out of a
# scratch copy (splitWakeBatch keeps responses), so the controls differ from
# the fix by that one line.
#
# Sections:
#   A  edit asks build to build: build asks test once, test answers, and the
#      answer is not typed into build's pane — one request:test, one typed line
#      in build's pane, one wake-response-dropped row, no response left queued
#   B  control — the same exchange on the pre-fix build re-fires: the answer is
#      typed into build's pane and build sends request:test again
#   C  responses queued alone for build, then `muxcode deliver build --force`:
#      nothing typed, the inbox drained with no receipt, two dropped rows
#   D  control — the same on the pre-fix build types the responses in
# A coverage floor keeps a skipped section from reporting green.
# Requires go, tmux, jq.
set -uo pipefail

PASS=0
FAIL=0
EXPECTED_PASS=17
for t in tmux go jq; do
  command -v "$t" >/dev/null 2>&1 || { echo "SKIP: $t is required"; exit 2; }
done

REPO=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
WORK=$(mktemp -d /tmp/response-echo-XXXXXX)
. "$REPO/scripts/lib/muxcode-bus-dir.sh"
SESSIONS=()

unset AGENT_ROLE BUS_ROLE BUS_SESSION TMUX MUXCODE_CONFIG MUXCODE_AGENT_CLI
export HOME="$WORK/home"
export XDG_STATE_HOME="$WORK/home/.local/state"
export TMUX_TMPDIR="$WORK/tmux"
export MUXCODE_LIFECYCLE_LOG_DIR="$WORK/lifecycle"
export MUXCODE_BUILD_CLI=opencode MUXCODE_TEST_CLI=opencode
export MUXCODE_DEDUP_WINDOW=0 MUXCODE_RELAY_SUPPRESS_THRESHOLD=1000 MUXCODE_BRANCH_TIME_DISABLE=1
mkdir -p "$WORK/bin" "$WORK/bin-pre" "$WORK/home" "$WORK/tmux" "$WORK/lifecycle" "$WORK/project"
cd "$WORK/project" || exit 1

cleanup() {
  local s
  tmux kill-server 2>/dev/null
  for s in "${SESSIONS[@]:-}"; do
    [ -n "$s" ] && rm -rf "$(muxcode_bus_dir "$MUX" "$s" 2>/dev/null)"
  done
  rm -rf "$WORK"
  true
}
trap cleanup EXIT

ok()   { PASS=$((PASS + 1)); echo "  ok: $1"; }
fail() { FAIL=$((FAIL + 1)); echo "  FAIL: $1"; }

(cd "$REPO/tools/muxcode" && go build -buildvcs=false -o "$WORK/bin/muxcode" .) \
  || { echo "SKIP: go build muxcode failed"; exit 2; }
MUX="$WORK/bin/muxcode"
ok "scratch muxcode built from this tree"

mkdir -p "$WORK/pre"
cp -R "$REPO/tools/muxcode" "$WORK/pre/"
sed -i.orig 's/if m.Type == "response" {/if false \&\& m.Type == "response" {/' "$WORK/pre/muxcode/bus/provider.go"
if [ "$(grep -c 'if false && m.Type == "response" {' "$WORK/pre/muxcode/bus/provider.go")" = 1 ] \
  && (cd "$WORK/pre/muxcode" && go build -buildvcs=false -o "$WORK/bin-pre/muxcode" .); then
  ok "pre-fix muxcode built: splitWakeBatch keeps responses"
else
  fail "pre-fix build: the response-filter patch did not apply exactly once, or the copy failed to build"
fi
MUX_PRE="$WORK/bin-pre/muxcode"

# fake-agent ROLE SESSION BIN LOG — an OpenCode-shaped composer that logs each
# typed line, redraws at once (so the injection confirm sees the composer
# empty), then acts as ROLE's agent.
cat >"$WORK/fake-agent.sh" <<'EOF'
#!/usr/bin/env bash
role=$1 session=$2 bin=$3 log=$4
export BUS_SESSION=$session
frame() {
  printf '\n%.0s' $(seq 1 45)
  printf '╭────────────────────────────────╮\n│ >                              │\n╰────────────────────────────────╯\n'
}
: >"$log"
# Non-canonical: a canonical tty splits an injected prompt longer than
# MAX_CANON (1024 bytes on macOS) into several reads.
stty -icanon min 1 time 0 2>/dev/null
frame
while IFS= read -r line; do
  frame
  # A bare Enter (the injection confirm's re-send) submits nothing.
  [ -n "$line" ] || continue
  printf '%s\n' "$line" >>"$log"
  id=$(printf '%s' "$line" | grep -oE -- '--reply-to [0-9A-Za-z_-]+' | head -1 | awk '{print $2}')
  case $role in
    build)
      # A request is answered first, as a real agent does, so its task stops
      # blocking the next wake; then, like the 2026-08-17 agent, ANY typed
      # prompt sends request:test (at most three). --force: no daemon runs
      # here to complete the previous request's tracked task, which would
      # otherwise refuse the re-send the 2026-08-17 daemon let through.
      [ -n "$id" ] && AGENT_ROLE=build "$bin" send edit build "Build succeeded. EXIT=0" --type response --reply-to "$id" >/dev/null 2>&1
      [ "$(wc -l <"$log")" -le 3 ] && AGENT_ROLE=build "$bin" send test test "Build succeeded — run tests and report results" --force >/dev/null 2>&1
      ;;
    test)
      AGENT_ROLE=test "$bin" send build response "All 4 packages passed. EXIT=0" --type response ${id:+--reply-to "$id"} >/dev/null 2>&1
      ;;
  esac
done
EOF
chmod +x "$WORK/fake-agent.sh"

# new_session NAME BIN — a private tmux session whose build and test windows
# run the fake agent in their agent pane (.1); sets SESSION, BUSDIR, LIFELOG.
new_session() {
  SESSION="echo-$1-$$"
  SESSIONS+=("$SESSION")
  export BUS_SESSION="$SESSION"
  BUSDIR="$(muxcode_bus_dir "$MUX" "$SESSION")" || exit 1
  LIFELOG="$MUXCODE_LIFECYCLE_LOG_DIR/$SESSION.log"
  "$2" init "$SESSION" >/dev/null 2>&1 || true
  tmux new-session -d -s "$SESSION" -n edit -x 200 -y 40 -c "$WORK/project" "sleep 100000"
  for r in build test; do
    tmux new-window -t "$SESSION" -n "$r" -c "$WORK/project" "sleep 100000"
    tmux split-window -h -t "$SESSION:$r" -c "$WORK/project" \
      "bash $WORK/fake-agent.sh $r $SESSION $2 $WORK/typed-$1-$r.log"
  done
  sleep 1
}

count_log()  { jq -c "select($1)" "$BUSDIR/log.jsonl" 2>/dev/null | wc -l | tr -d ' '; }
test_reqs()  { count_log '.from == "build" and .to == "test" and .type == "request"'; }
answers()    { count_log '.from == "test" and .to == "build" and .type == "response"'; }
typed()      { wc -l <"$WORK/typed-$1-build.log" 2>/dev/null | tr -d ' '; }
dropped()    { grep -c '"event":"wake-response-dropped"' "$LIFELOG" 2>/dev/null || true; }
queued_resp() { jq -c 'select(.type == "response")' "$BUSDIR/inbox/build.jsonl" 2>/dev/null | wc -l | tr -d ' '; }

# wait_for SECS DESCRIPTION CHECK [ARGS...] — polls twice a second.
wait_for() {
  local secs=$1 desc=$2 i=0
  shift 2
  while [ "$i" -lt $((secs * 2)) ]; do
    if "$@" >/dev/null 2>&1; then ok "$desc"; return 0; fi
    sleep 0.5; i=$((i + 1))
  done
  fail "$desc (timeout ${secs}s)"
  return 1
}
asked_test() { [ "$(test_reqs)" -ge 1 ]; }
answered()   { [ "$(answers)" -ge 1 ]; }

echo "=== MUX-009 response echo ==="

# ── A: this build — the answer is never typed into build's pane ────
echo "-- A: a request, its chain and its answer on this build"
new_session a "$MUX"
AGENT_ROLE=edit "$MUX" send build build "Run the build and report" >/dev/null 2>&1
wait_for 20 "A: build asked test to run" asked_test
wait_for 20 "A: test answered build" answered
sleep 6 # room for an echo to re-fire
[ "$(test_reqs)" = 1 ] && ok "A: build sent request:test exactly once — the answer did not re-fire its chain" \
  || fail "A: build sent request:test $(test_reqs) times"
[ "$(typed a)" = 1 ] && ok "A: only edit's request was typed into build's pane" \
  || fail "A: build's pane was typed $(typed a) lines: $(cat "$WORK/typed-a-build.log")"
[ "$(dropped)" -ge 1 ] && ok "A: wake-response-dropped logged for the answer" || fail "A: no wake-response-dropped row"
[ "$(queued_resp)" = 0 ] && ok "A: no response left queued to be typed in later" \
  || fail "A: $(queued_resp) response(s) still queued for build"

# ── B: control — the pre-fix build re-fires ────────────────────────
echo "-- B: control — the same exchange on the pre-fix build"
new_session b "$MUX_PRE"
AGENT_ROLE=edit "$MUX_PRE" send build build "Run the build and report" >/dev/null 2>&1
wait_for 20 "B: build asked test to run" asked_test
wait_for 20 "B: test answered build" answered
refired() { [ "$(test_reqs)" -ge 2 ]; }
wait_for 20 "B: the answer typed into build's pane re-fired request:test — the 2026-08-17 echo" refired
grep -q 'All 4 packages passed' "$WORK/typed-b-build.log" 2>/dev/null \
  && ok "B: the answer's text was typed into build's pane" || fail "B: build's pane never received the answer"

# ── C: responses alone, force-delivered, on this build ─────────────
echo "-- C: responses queued alone for build, force-delivered"
new_session c "$MUX"
for n in 1 2; do
  AGENT_ROLE=test "$MUX" send build response "Queued answer $n. EXIT=0" --type response --no-notify >/dev/null 2>&1
done
"$MUX" deliver build --force >"$WORK/deliver-c.out" 2>&1
sleep 1
[ "$(typed c)" = 0 ] && ok "C: nothing was typed into build's pane" \
  || fail "C: build's pane was typed: $(cat "$WORK/typed-c-build.log")"
[ "$(queued_resp)" = 0 ] && ok "C: the responses were drained" || fail "C: $(queued_resp) response(s) still queued"
receipted=$(for f in "$BUSDIR"/delivery/*.status; do jq -r 'select(.acked_at != null and .acked_at > 0) | .id' "$f" 2>/dev/null; done | grep -c . || true)
[ "$receipted" = 0 ] && ok "C: no receipt written for a response build never saw" \
  || fail "C: $receipted receipt(s) written"
grep '"event":"wake-response-dropped"' "$LIFELOG" 2>/dev/null | grep -q 'build: 2 response(s)' \
  && ok "C: one wake-response-dropped row names both responses" || fail "C: dropped rows: $(grep wake-response-dropped "$LIFELOG")"

# ── D: control — the pre-fix build types them in ───────────────────
echo "-- D: control — the same on the pre-fix build"
new_session d "$MUX_PRE"
for n in 1 2; do
  AGENT_ROLE=test "$MUX_PRE" send build response "Queued answer $n. EXIT=0" --type response --no-notify >/dev/null 2>&1
done
"$MUX_PRE" deliver build --force >"$WORK/deliver-d.out" 2>&1
typed_d() { grep -q 'Queued answer' "$WORK/typed-d-build.log" 2>/dev/null; }
wait_for 10 "D: the pre-fix build typed the responses into build's pane" typed_d

# ── Coverage floor ─────────────────────────────────────────────────
echo
echo "=== results: $PASS passed, $FAIL failed (floor $EXPECTED_PASS) ==="
if [ "$PASS" -ne "$EXPECTED_PASS" ]; then
  echo "  FAIL  coverage floor: expected exactly $EXPECTED_PASS passes, got $PASS — a section was skipped or double-counted"
  exit 1
fi
[ "$FAIL" -eq 0 ] || exit 1
exit 0
