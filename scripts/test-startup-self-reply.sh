#!/usr/bin/env bash
# Integration test for MUX-169: the launch bootstrap (`request:startup`) is the
# one self-addressed message the bus delivers; an agent's `response:startup`
# reply to it is correlated and never delivered — not to its own inbox, not as
# a CC to edit, not through `muxcode inbox`, not through the codex `hook stop`
# road. On 2026-09-09 an action-only exemption let a codex test agent
# acknowledge its own acknowledgement every 5 s.
#
# Hermetic: a scratch bus session, a scratch project dir and a stub `codex` on
# PATH (so the hook road is eligible without the real CLI); no tmux, no daemon.
# Sections: (A) the bootstrap is delivered and visible; (B) the send-road reply
# is recorded for correlation — bootstrap `responded` and drained, reply absent
# from the sender's inbox and from edit's; (C) rows an older binary already
# delivered — bootstrap plus stale reply on disk — surface the bootstrap alone
# through `muxcode inbox --peek` and `muxcode hook stop`; (D) negative control:
# an ordinary self-addressed request is dropped with the `[send]` line.
# Coverage floor: the exact pass count.
#
# Requires installed muxcode >= v0.1.0 (run ./build.sh first).
set -uo pipefail

PASS=0
FAIL=0
EXPECTED_PASS=15

command -v muxcode >/dev/null 2>&1 || { echo "SKIP: muxcode not installed"; exit 2; }
command -v jq >/dev/null 2>&1 || { echo "SKIP: jq is required"; exit 2; }
MUX=$(command -v muxcode)
. "$(dirname "${BASH_SOURCE[0]}")/lib/muxcode-version.sh"
require_muxcode_version "$MUX" v0.1.0 MUX-169 || { echo "  FAIL  binary precondition not met"; exit 1; }

REPO=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
FIX="$REPO/tools/muxcode/bus/testdata/codex-hooks"
SESSION="startup-self-reply-$$"
WORK=$(mktemp -d /tmp/startup-self-reply-XXXXXX)
PROJ="$WORK/project"
BUSDIR="/tmp/muxcode-bus-$SESSION"
mkdir -p "$PROJ" "$WORK/bin" "$WORK/codex-home" "$WORK/lifecycle"

cleanup() { rm -rf "$BUSDIR" "$WORK"; }
trap cleanup EXIT

ok()   { PASS=$((PASS + 1)); echo "  ok: $1"; }
fail() { FAIL=$((FAIL + 1)); echo "  FAIL: $1"; }

# Stub codex: eligibility asks only `codex --version`.
printf '#!/usr/bin/env bash\necho "codex-cli 0.153.4"\n' > "$WORK/bin/codex"
chmod +x "$WORK/bin/codex"
export PATH="$WORK/bin:$PATH"
export BUS_SESSION="$SESSION"
export CODEX_HOME="$WORK/codex-home"
export MUXCODE_LIFECYCLE_LOG_DIR="$WORK/lifecycle"
export MUXCODE_TEST_CLI=codex
export MUXCODE_CODEX_HOOKS=1
export MUXCODE_DEDUP_WINDOW=0
unset MUXCODE_AGENT_CLI MUXCODE_TEST_CODEX_HOOKS 2>/dev/null || true

cp "$FIX/transcript.jsonl" "$WORK/transcript.jsonl"
ev() { jq -c --arg t "$WORK/transcript.jsonl" '.transcript_path=$t' "$FIX/$1"; }
inbox_lines() { [ -f "$BUSDIR/inbox/$1.jsonl" ] && wc -l < "$BUSDIR/inbox/$1.jsonl" | tr -d ' ' || echo 0; }
inbox_has() { grep -q -- "$2" "$BUSDIR/inbox/$1.jsonl" 2>/dev/null; }
# row ID FROM TO TYPE ACTION PAYLOAD [EXTRA-JSON-FIELDS] — a raw inbox line, the way an older binary would leave it.
row() { printf '{"id":"%s","from":"%s","to":"%s","type":"%s","action":"%s","payload":"%s","ts":%s%s}\n' "$1" "$2" "$3" "$4" "$5" "$6" "$(date +%s)" "${7:-}"; }

echo "=== startup self-reply integration test (MUX-169) ==="
cd "$PROJ"
"$MUX" init "$SESSION" >/dev/null 2>&1 || true
mkdir -p "$BUSDIR/inbox" "$BUSDIR/delivery"
AGENT_ROLE=test "$MUX" agent config test >/dev/null 2>&1 || true

# ── A: the bootstrap is the one self-send the bus delivers ────────
echo "-- bootstrap"
if AGENT_ROLE=test "$MUX" send test startup "Session started — restore context" --no-notify >/dev/null 2>&1; then ok "bootstrap request:startup self-send accepted"; else fail "bootstrap send refused"; fi
if [ "$(inbox_lines test)" = "1" ] && inbox_has test '"type":"request"' && inbox_has test '"action":"startup"'; then ok "bootstrap delivered to the agent's own inbox"; else fail "bootstrap not in inbox ($(inbox_lines test) row(s))"; fi
BOOT=$(jq -r 'select(.action=="startup") | .id' "$BUSDIR/inbox/test.jsonl" 2>/dev/null | head -1)
out=$("$MUX" inbox --role test --peek 2>/dev/null)
grep -q "Session started" <<<"$out" && ok "bootstrap visible to inbox --peek" || fail "peek lacks the bootstrap: ${out:-<empty>}"

# ── B: the reply is correlated, never delivered ───────────────────
echo "-- send-road reply"
err=$(AGENT_ROLE=test "$MUX" send test startup "Acknowledged." --type response --reply-to "$BOOT" --no-notify 2>&1 >/dev/null)
grep -q "self-addressed reply recorded for correlation" <<<"$err" && ok "reply recorded for correlation, not delivered" || fail "send stderr: ${err:-<empty>}"
inbox_has test '"type":"response"' && fail "reply landed in the sender's own inbox" || ok "reply absent from the sender's own inbox"
# The CLI bootstrap send above is auto-CC'd to edit (PreLaunchSetup uses SendNoCC); only the reply must be absent there.
if inbox_has edit '"type":"response"' || inbox_has edit '"payload":"Acknowledged."'; then fail "reply CC'd to edit"; else ok "reply not CC'd to edit"; fi
if [ -n "$BOOT" ] && jq -e '.status=="responded"' "$BUSDIR/delivery/$BOOT.status" >/dev/null 2>&1; then ok "bootstrap delivery status reads responded"; else fail "bootstrap status: $(cat "$BUSDIR/delivery/$BOOT.status" 2>/dev/null || echo missing)"; fi
[ "$(inbox_lines test)" = "0" ] && ok "answered bootstrap drained — nothing left to re-wake" || fail "inbox still has $(inbox_lines test) row(s)"

# ── C: rows an older binary already delivered ─────────────────────
echo "-- stale rows on disk"
row boot-stale test test request startup "Session started (stale)" >> "$BUSDIR/inbox/test.jsonl"
row reply-stale test test response startup "Acknowledged (stale)" ',"reply_to":"boot-stale"' >> "$BUSDIR/inbox/test.jsonl"
out=$("$MUX" inbox --role test --peek 2>/dev/null)
grep -q "Session started (stale)" <<<"$out" && ok "inbox --peek surfaces the bootstrap" || fail "peek lacks the stale bootstrap: ${out:-<empty>}"
grep -q "Acknowledged (stale)" <<<"$out" && fail "inbox --peek surfaced the stale self-reply" || ok "inbox --peek hides the stale self-reply"
out=$(ev stop.json | AGENT_ROLE=test "$MUX" hook stop 2>/dev/null)
if jq -e '.decision=="block"' <<<"$out" >/dev/null 2>&1 && grep -q "Session started (stale)" <<<"$out"; then ok "hook stop delivers the bootstrap"; else fail "stop answer: ${out:-<empty>}"; fi
grep -q "Acknowledged (stale)" <<<"$out" && fail "hook stop delivered the stale self-reply" || ok "hook stop drops the stale self-reply"
[ "$(inbox_lines test)" = "0" ] && ok "stale reply consumed with the bootstrap, not left to re-fire" || fail "inbox still has $(inbox_lines test) row(s) after hook stop"

# ── D: negative control — an ordinary self-send is dropped ────────
echo "-- ordinary self-send"
err=$(AGENT_ROLE=deploy "$MUX" send deploy verify "self ping" --no-notify 2>&1 >/dev/null)
grep -q "dropping self-addressed message" <<<"$err" && ok "ordinary self-send dropped with the [send] line" || fail "send stderr: ${err:-<empty>}"
[ "$(inbox_lines deploy)" = "0" ] && ok "ordinary self-send never reaches the inbox" || fail "deploy inbox has $(inbox_lines deploy) row(s)"

echo
echo "=== results: $PASS passed, $FAIL failed (floor $EXPECTED_PASS) ==="
if [ "$FAIL" -ne 0 ]; then
  exit 1
fi
if [ "$PASS" -ne "$EXPECTED_PASS" ]; then
  echo "  FAIL  coverage floor: expected exactly $EXPECTED_PASS passes, got $PASS — a section was skipped or double-counted"
  exit 1
fi
echo "PASS"
