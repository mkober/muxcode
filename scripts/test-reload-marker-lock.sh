#!/usr/bin/env bash
# Integration test for MUX-194 — the reload marker is a renewed lease: a live
# holder keeps it past the 60 s stale threshold, `diagnose` reads it as a
# relaunch in progress, and only a holder that stopped renewing is reclaimed.
#
# Hermetic: scratch HOME, state, bus and lifecycle log, a private tmux server
# (scripts/lib/private-tmux.sh) holding a session with a plan window. Binaries:
#   - muxcode built from THIS tree — every CLI call (`diagnose`, `resume`);
#   - a scratch copy of the tree, changed in two ways only: checkCleanup's
#     300 s cadence set to 3 s (its daemon is the cleanup under test), and a
#     test-only export of acquireReloadMarker, which a `holder` program in
#     the copy calls — the real acquire, renewal and token-checked release —
#     and releases on SIGTERM.
#
# Sections: (A) a holder kept alive 65 s — the cleanup leaves its marker, a
# concurrent `muxcode resume plan` is refused as held, `diagnose` reports
# relaunch-in-progress (info, not critical); (B) negative control — the
# holder SIGKILLed, so it never renews or releases: with the daemon paused,
# once past 60 s `diagnose` reports reload-marker-stuck (critical, exit 1);
# the restarted daemon's cleanup removes the marker and a
# reload-marker-reclaimed row names plan; (C) a marker replaced
# under its holder survives the holder's release; control — a holder's own
# release removes its marker. ~3 min (two real 60 s waits). A coverage floor
# keeps a skipped section from reporting green. Requires go, tmux, jq.
set -euo pipefail
export MUXCODE_CONSUMER_GUARD=0

PASS=0
FAIL=0
for t in tmux go jq; do
  command -v "$t" >/dev/null 2>&1 || { echo "SKIP: $t is required"; exit 2; }
done

REPO=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
WORK=$(mktemp -d /tmp/reload-marker-lock-XXXXXX)
SESSION="reload-lock-$$"
. "$REPO/scripts/lib/muxcode-bus-dir.sh"
. "$REPO/scripts/lib/private-tmux.sh"
private_tmux_init "$WORK/tmux"
BUSDIR=""

export BUS_SESSION="$SESSION" AGENT_ROLE=edit BUS_ROLE=edit
export HOME="$WORK/home" XDG_STATE_HOME="$WORK/state"
export MUXCODE_LIFECYCLE_LOG_DIR="$WORK/lifecycle"
export MUXCODE_AGENT_CLI=claude MUXCODE_PLAN_CLI=claude
export MUXCODE_AGENT_HEALTH_CHECK_SECS=3600 # no health restart races the marker
export MUXCODE_CONTROL_PANE_DISABLE=1 MUXCODE_FORCE_RESPOND_DISABLE=1 MUXCODE_AGENTDEFS_WATCH_DISABLE=1
export MUXCODE_TMP_CLEANUP_THRESHOLD=0 MUXCODE_ACTIVE_WATCHDOG_SECS=0 MUXCODE_PROMPT_AGENT_DISABLE=1
mkdir -p "$WORK/bin" "$WORK/bin-copy" "$WORK/home" "$WORK/state" "$WORK/project"

DPID="" HPIDS=""
cleanup() {
  set +e
  for p in $HPIDS; do kill -KILL "$p" 2>/dev/null; done
  if [ -n "$DPID" ]; then kill "$DPID" 2>/dev/null; wait "$DPID" 2>/dev/null; fi
  private_tmux_kill
  sleep 1
  rm -rf ${BUSDIR:+"$BUSDIR"} "$WORK" 2>/dev/null || { sleep 2; rm -rf ${BUSDIR:+"$BUSDIR"} "$WORK" 2>/dev/null; }
  true
}
trap cleanup EXIT

ok()   { PASS=$((PASS + 1)); echo "  ok: $1"; }
fail() { FAIL=$((FAIL + 1)); echo "  FAIL: $1"; }

MARKER() { echo "$BUSDIR/lock/plan.reloading"; }
events()   { "$MUX" lifecycle show "$SESSION" --limit 1000 2>/dev/null | grep -E "$1" || true; }
has_event() { [ -n "$(events "$1")" ]; }
# diag — `muxcode diagnose plan --json` into $WORK/diag.json, its exit code in $DIAG_RC.
diag() { set +e; "$MUX" diagnose plan --json >"$WORK/diag.json" 2>/dev/null; DIAG_RC=$?; set -e; }
finding() { jq -r --arg m "$1" '.findings[]? | select(.failure_mode == $m) | .severity' "$WORK/diag.json" 2>/dev/null; }
stuck_now() { diag; [ "$(finding reload-marker-stuck)" = critical ]; }
marker_gone() { [ ! -e "$(MARKER)" ]; }

# hold — start a holder for plan; its pid in $HPID once it prints "held".
hold() {
  "$WORK/bin-copy/holder" "$SESSION" plan >"$WORK/holder-$1.out" 2>&1 &
  HPID=$!
  HPIDS="$HPIDS $HPID"
  local i=0
  while [ "$i" -lt 50 ] && ! grep -q '^held' "$WORK/holder-$1.out" 2>/dev/null; do sleep 0.1; i=$((i + 1)); done
  grep -q '^held' "$WORK/holder-$1.out"
}
# release_holder — SIGTERM the holder and wait for its token-checked release.
release_holder() { kill -TERM "$HPID"; wait "$HPID" 2>/dev/null || true; }

echo "=== MUX-194 reload-marker lease integration test ==="

(cd "$REPO/tools/muxcode" && go build -buildvcs=false -o "$WORK/bin/muxcode" .) \
  || { echo "SKIP: go build muxcode failed"; exit 2; }
MUX="$WORK/bin/muxcode"
BUSDIR="$(muxcode_bus_dir "$MUX" "$SESSION")" || exit 1

COPY="$WORK/copy"
cp -R "$REPO/tools/muxcode" "$COPY"
perl -pi -e 's/if now-d\.lastCleanupCheck < 300 \{/if now-d.lastCleanupCheck < 3 {/' "$COPY/daemon/daemon.go"
cat >"$COPY/bus/zz_reload_holder.go" <<'EOF'
package bus

// HoldReloadMarkerForIntegrationTest is scripts/test-reload-marker-lock.sh's
// door to the real acquireReloadMarker. It exists only in that script's copy.
func HoldReloadMarkerForIntegrationTest(session, role string) (func(), error) {
	return acquireReloadMarker(session, role)
}
EOF
mkdir -p "$COPY/zzholder"
cat >"$COPY/zzholder/main.go" <<'EOF'
package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

func main() {
	release, err := bus.HoldReloadMarkerForIntegrationTest(os.Args[1], os.Args[2])
	if err != nil {
		fmt.Println("refused:", err)
		os.Exit(3)
	}
	fmt.Println("held")
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	<-sig
	release()
	fmt.Println("released")
}
EOF
if grep -q 'lastCleanupCheck < 3 {' "$COPY/daemon/daemon.go" \
  && (cd "$COPY" && go build -buildvcs=false -o "$WORK/bin-copy/muxcode" . && go build -buildvcs=false -o "$WORK/bin-copy/holder" ./zzholder); then
  ok "built: muxcode from this tree; the cleanup daemon (3 s cadence) and holder from a copy"
else
  echo "SKIP: the scratch copy did not patch or build"; exit 2
fi

tmux new-session -d -s "$SESSION" -n plan -x 160 -y 40 -c "$WORK/project"
tmux split-window -h -t "$SESSION:plan" -c "$WORK/project"
"$MUX" init "$SESSION" >/dev/null 2>&1 || true
"$WORK/bin-copy/muxcode" watch "$SESSION" --poll 1 >"$WORK/daemon.log" 2>&1 &
DPID=$!
sleep 2
kill -0 "$DPID" 2>/dev/null && ok "scratch cleanup daemon running" || fail "scratch daemon started"

# ── A: a live holder past the threshold ──────────────────────────
echo "-- A: a holder kept alive 65 s"
hold a && ok "A: holder acquired plan's marker" || fail "A: holder did not acquire: $(cat "$WORK/holder-a.out")"
TOKEN=$(cat "$(MARKER)" 2>/dev/null || true)
case "$TOKEN" in "reloading $HPID-"*) ok "A: the marker carries the holder's token ($TOKEN)" ;; *) fail "A: marker content '$TOKEN'" ;; esac
sleep 65
[ "$(cat "$(MARKER)" 2>/dev/null || true)" = "$TOKEN" ] \
  && ok "A: after 65 s and ~20 cleanup passes the holder's marker is still in place" \
  || fail "A: the live hold's marker was removed or replaced"
has_event "reload-marker-reclaimed" && fail "A: a reload-marker-reclaimed row for a live hold" || ok "A: no reclaimed row for the live hold"
set +e; "$MUX" resume plan >"$WORK/resume.out" 2>&1; RESUME_RC=$?; set -e
[ "$RESUME_RC" -ne 0 ] && grep -q "in progress" "$WORK/resume.out" \
  && ok "A: a concurrent muxcode resume plan is refused as held ($(head -1 "$WORK/resume.out" | cut -c1-90))" \
  || fail "A: resume exit $RESUME_RC: $(cat "$WORK/resume.out")"
[ "$(cat "$(MARKER)" 2>/dev/null || true)" = "$TOKEN" ] && ok "A: the refused resume left the holder's marker" || fail "A: the refused resume touched the marker"
diag
[ "$(finding relaunch-in-progress)" = info ] && ok "A: diagnose reports relaunch-in-progress, info" || fail "A: relaunch-in-progress = '$(finding relaunch-in-progress)'"
[ -z "$(finding reload-marker-stuck)" ] && ok "A: diagnose does not report reload-marker-stuck" || fail "A: reload-marker-stuck reported for a live hold"
jq -e '[.findings[]? | select(.failure_mode == "relaunch-in-progress") | .remediation[] | select(test("rm "))] | length == 0' "$WORK/diag.json" >/dev/null \
  && ok "A: its remediation offers no rm" || fail "A: the live hold's remediation offers an rm"
jq -e '[.findings[]? | select(.failure_mode == "relaunch-in-progress") | .evidence[] | select(test("held for (6[5-9]|[7-9][0-9])s"))] | length == 1' "$WORK/diag.json" >/dev/null \
  && ok "A: the finding names the hold time (past 65 s)" || fail "A: hold time missing: $(jq -c '.findings[]? | select(.failure_mode == "relaunch-in-progress") | .evidence' "$WORK/diag.json")"
CRIT=$(jq '[.findings[]? | select(.severity == "critical")] | length' "$WORK/diag.json")
{ [ "$CRIT" = 0 ] && [ "$DIAG_RC" = 0 ]; } || { [ "$CRIT" -gt 0 ] && [ "$DIAG_RC" = 1 ]; } \
  && ok "A: diagnose exit $DIAG_RC follows its other findings alone ($CRIT critical, none about the marker)" \
  || fail "A: diagnose exit $DIAG_RC with $CRIT critical"

# ── B: negative control — a holder that died ─────────────────────
echo "-- B: the holder SIGKILLed — no renewal, no release"
# The daemon is paused while the marker ages, so diagnose reads the stale
# marker deterministically rather than racing a 3 s cleanup for it; its
# restart runs a cleanup pass at once (lastCleanupCheck starts at 0).
kill "$DPID"; wait "$DPID" 2>/dev/null || true; DPID=""
kill -KILL "$HPID"; wait "$HPID" 2>/dev/null || true
[ -e "$(MARKER)" ] && ok "B: the dead holder's marker remains (nothing released it)" || fail "B: the marker vanished at the kill"
i=0
while [ "$i" -lt 80 ] && ! stuck_now; do sleep 1; i=$((i + 1)); done
[ "$(finding reload-marker-stuck)" = critical ] && ok "B: past 60 s (${i}s after the kill) diagnose reports reload-marker-stuck, critical" \
  || fail "B: diagnose never reported reload-marker-stuck"
[ "$DIAG_RC" = 1 ] && ok "B: that diagnose exited 1" || fail "B: diagnose exit $DIAG_RC on a stale marker"
"$WORK/bin-copy/muxcode" watch "$SESSION" --poll 1 >>"$WORK/daemon.log" 2>&1 &
DPID=$!
i=0
while [ "$i" -lt 15 ] && ! marker_gone; do sleep 1; i=$((i + 1)); done
marker_gone && ok "B: the restarted daemon's cleanup removed the stale marker" || fail "B: the stale marker is still there ${i}s after the restart"
has_event "reload-marker-reclaimed.*plan: stale [0-9]+s since its last renewal, content \"reloading $HPID-" \
  && ok "B: a reload-marker-reclaimed row names plan, its age and the dead holder's token" \
  || fail "B: reclaimed row: $(events reload-marker | tail -2)"

# ── C: release is token-checked ──────────────────────────────────
echo "-- C: a replaced marker survives its holder's release"
hold c && ok "C: a holder acquired plan's marker" || fail "C: holder did not acquire"
printf 'reloading 99999-1' >"$(MARKER)"
release_holder
grep -q "^released" "$WORK/holder-c.out" && ok "C: the holder released" || fail "C: the holder did not report its release"
[ "$(cat "$(MARKER)" 2>/dev/null || true)" = "reloading 99999-1" ] \
  && ok "C: the foreign marker survived the original holder's release" || fail "C: the holder's release removed a marker it no longer owned"
rm -f "$(MARKER)"
hold c2 && ok "C: control — a fresh holder acquired" || fail "C: control holder did not acquire"
release_holder
marker_gone && ok "C: control — the holder's own release removed its marker" || fail "C: control — the owner's release left its marker"

echo "=== $PASS passed, $FAIL failed ==="
[ "$PASS" -ge 23 ] || { echo "FAIL: coverage floor not met ($PASS < 23)"; exit 1; }
[ "$FAIL" -eq 0 ] || exit 1
echo "PASS"
