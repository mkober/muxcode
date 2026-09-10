#!/usr/bin/env bash
# Integration test for MUX-171: no re-drive road types into a working pane.
#
# A re-drive injection is preceded by an Escape (TmuxDismissOverlay, MUX-163).
# On a pane at rest that dismisses an overlay; on a working Claude agent it is
# the tool-interrupt key. On 2026-09-09 the stall watchdog re-drove the run
# agent twice mid-command, 61 s apart, and killed MUX-167's integration script
# both times — the second re-drive killing the restart the first provoked.
#
# Hermetic: a scratch bus session and a scratch tmux session whose role panes
# are shells with the Claude idle glyph as PS1 (the MUX-103 pattern that
# satisfies provider idle detection). The `run` pane additionally renders a
# spinner line carrying "esc to interrupt", so it reads as a turn in progress
# while its ❯ composer is on screen — the exact shape that fooled the
# watchdog, since PaneHasIdlePrompt is true for both panes and only
# PaneShowsRecoverableIdle tells them apart.
#
# Phase A (no daemon): `muxcode deliver --force` — the manual escape hatch, and
# the road every automatic recovery shares — refuses the busy pane and still
# re-drives the resting one.
# Phase B (real scratch daemon): the stall watchdog withholds the re-drive from
# the busy pane and still catches the resting one. checkStalledTasks debounces
# on two sightings 30 s apart, so Phase B takes ~40 s by design.
#
# Every busy-pane assertion is paired with a resting-pane control: a build that
# simply never re-drives anything would pass the first half alone.
#
# Requires installed muxcode >= v0.1.0 (run ./build.sh first).
set -uo pipefail

PASS=0
FAIL=0
EXPECTED_PASS=15

command -v tmux >/dev/null 2>&1 || { echo "SKIP: tmux is required"; exit 2; }
command -v muxcode >/dev/null 2>&1 || { echo "SKIP: muxcode not installed"; exit 2; }
MUX=$(command -v muxcode)
REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REPO_BEFORE=$(ls -A "$REPO")
. "$(dirname "${BASH_SOURCE[0]}")/lib/muxcode-version.sh"
require_muxcode_version "$MUX" v0.1.0 MUX-171 || { echo "  FAIL  binary precondition not met"; exit 1; }

SESSION="stall-busy-test-$$"
export BUS_SESSION="$SESSION"
export AGENT_ROLE=edit BUS_ROLE=edit
WORK=$(mktemp -d /tmp/stall-busy-XXXXXX)
export MUXCODE_LIFECYCLE_LOG_DIR="$WORK/lifecycle"
# Every fixture pane renders a CLAUDE-shaped frame, so each role's provider is
# pinned per-role: a per-role variable inherited from the caller's environment
# (MUXCODE_REVIEW_CLI=codex in a live session) outranks MUXCODE_AGENT_CLI and
# would judge these panes with Codex detection, which has no spinner check —
# the stale-spinner assertion would then pass against a reverted paneLiveTail.
# The codex control below overrides this for its own command only.
export MUXCODE_AGENT_CLI=claude MUXCODE_RUN_CLI=claude MUXCODE_TEST_CLI=claude MUXCODE_REVIEW_CLI=claude
export MUXCODE_TASK_STALL_SECS=5
export MUXCODE_TMP_CLEANUP_THRESHOLD=0
BUSDIR="/tmp/muxcode-bus-$SESSION"

DPID=""
cleanup() {
  [ -n "$DPID" ] && kill "$DPID" 2>/dev/null || true
  pkill -f "watch $SESSION" 2>/dev/null || true
  tmux kill-session -t "$SESSION" 2>/dev/null || true
  rm -rf "$BUSDIR" "$WORK"
}
trap cleanup EXIT

ok()   { PASS=$((PASS + 1)); echo "  ok: $1"; }
fail() { FAIL=$((FAIL + 1)); echo "  FAIL: $1"; }

lifecycle_has() { cat "$WORK/lifecycle"/* 2>/dev/null | grep -q -- "$1"; }
# A row for one role only — the event name and the role must be on the same line.
lifecycle_row() { cat "$WORK/lifecycle"/* 2>/dev/null | grep -- "$1" | grep -c -- "$2"; }
pane_text() { tmux capture-pane -t "$SESSION:$1.1" -p 2>/dev/null; }

# A working agent: spinner with the interrupt hint above a live ❯ composer.
render_busy() {
  tmux send-keys -t "$SESSION:run.1" -l -- 'PS1="❯ "; clear; printf "⏺ Running the verification script\n✻ Cogitating… (2m 14s · esc to interrupt)\n"'
  tmux send-keys -t "$SESSION:run.1" Enter
}

# The same pane at rest: a completed recap carries no interrupt hint.
render_rest() {
  tmux send-keys -t "$SESSION:$1.1" -l -- 'PS1="❯ "; clear; printf "✻ Cooked for 2m 14s\n"'
  tmux send-keys -t "$SESSION:$1.1" Enter
}

# A pane that is idle NOW but whose scrollback still carries a spinner line.
# The busy check reads only the live tail, so the interrupt hint must sit far
# enough above the rest frame to fall outside it — hence the filler.
render_stale_spinner() {
  tmux send-keys -t "$SESSION:$1.1" -l -- 'PS1="❯ "; clear; printf "✻ Cogitating… (2m 14s · esc to interrupt)\n⏺ Read(a)\n  ⎿ 1\n⏺ Read(b)\n  ⎿ 2\n⏺ Read(c)\n  ⎿ 3\n⏺ Read(d)\n  ⎿ 4\n⏺ Read(e)\n  ⎿ 5\n✻ Cooked for 2m 14s\n"'
  tmux send-keys -t "$SESSION:$1.1" Enter
}

# An in-flight task whose inbox row is already consumed — the
# consumed-but-never-started shape the re-drive exists to rescue.
seed_task() {
  "$MUX" send "$1" "$1" "long-running verification for $1" --track --no-notify >/dev/null 2>&1
  BUS_ROLE="$1" AGENT_ROLE="$1" "$MUX" inbox --role "$1" >/dev/null 2>&1
}

echo "=== stall re-drive busy-pane integration test (MUX-171) ==="

# Every pane is rooted in $WORK, never the invoking directory. These fixtures
# are real shells, and a delivered payload lands on their command line: with
# the checkout as cwd, an injected label containing shell metacharacters is
# EXECUTED there — which is how the empty files `codex-row]` and `stale-row]`
# appeared in the repo, stamped at an earlier run of this script.
tmux new-session -d -s "$SESSION" -n run -x 140 -y 35 -c "$WORK"
tmux split-window -h -t "$SESSION:run" -c "$WORK"
tmux new-window -t "$SESSION" -n test -c "$WORK"
tmux split-window -h -t "$SESSION:test" -c "$WORK"
tmux new-window -t "$SESSION" -n review -c "$WORK"
tmux split-window -h -t "$SESSION:review" -c "$WORK"
render_busy
render_rest test
sleep 1
"$MUX" init "$SESSION" >/dev/null 2>&1 || true
seed_task run
seed_task test
sleep 1

# ── Phase A: the manual escape hatch refuses a working pane ──────
echo "-- deliver --force"
"$MUX" deliver run --force >/dev/null 2>&1
if pane_text run | grep -q "Re-drive"; then
  fail "deliver --force typed into a running tool call"
else
  ok "deliver --force refused the working pane"
fi
# The guard sits at ForceDeliver ENTRY, above the unnotified-inbox branch, so a
# busy pane is withheld before redriveInFlightTasks is ever reached: the row is
# deliver-skipped-busy, and redrive-skipped-busy would mean the guard slipped
# back below the branch where a pending row bypasses it.
[ "$(lifecycle_row "deliver-skipped-busy" "run")" -ge 1 ] \
  && ok "deliver-skipped-busy row written for the working pane" \
  || fail "no deliver-skipped-busy row for the working pane"

"$MUX" deliver test --force >/dev/null 2>&1
sleep 1
if pane_text test | grep -q "Re-drive"; then
  ok "control: deliver --force still re-drove the resting pane"
else
  fail "the resting pane was not re-driven — the refusal is too broad"
fi

# A busy pane with an UNNOTIFIED row takes the ordinary delivery branch, not
# the re-drive one. Guarding only the re-drive left this road typing mid-turn.
echo "-- busy pane with a pending row"
"$MUX" send run pending-row "a row that arrived mid-turn" --no-notify >/dev/null 2>&1
"$MUX" deliver run --force >/dev/null 2>&1
if pane_text run | grep -q "a row that arrived mid-turn"; then
  fail "a pending inbox row was injected into a running tool call"
else
  ok "pending row withheld from the working pane"
fi
if "$MUX" inbox --role run --peek 2>/dev/null | grep -q "a row that arrived mid-turn"; then
  ok "the withheld row is still pending for a later delivery"
else
  fail "the withheld row was consumed by the refusal"
fi

# The busy test must be provider-aware: an idle codex pane has no ❯ at all,
# so a Claude-shaped predicate would call every non-Claude agent busy forever.
#
# This pane therefore renders NO ❯ — not even as PS1. With the Claude glyph
# present the control cannot fail: a ❯-based predicate reads the pane as idle
# and the assertion passes against the very implementation it exists to reject.
# The absence of a log is only half a control, so delivery is asserted too. The
# row is left PENDING rather than seeded as consumed work: the codex scrape road
# builds its injection from Peek, so a consumed task legitimately injects
# nothing, and asserting delivery on one would fail against correct code.
echo "-- provider awareness"
tmux send-keys -t "$SESSION:review.1" -l -- 'PS1="› "; clear; printf "› Ask Codex to do anything\n  gpt-5.6-luna medium · ~/Repos\n"'
tmux send-keys -t "$SESSION:review.1" Enter
sleep 1
"$MUX" send review codex-row "a row for the idle codex pane" --no-notify >/dev/null 2>&1
codex_out=$(MUXCODE_REVIEW_CLI=codex "$MUX" deliver review --force 2>&1)
if [ "$(lifecycle_row "deliver-skipped-busy" "review")" -ge 1 ]; then
  fail "an idle codex pane was classified as working"
else
  ok "an idle non-Claude pane is not classified as working"
fi
if printf '%s' "$codex_out" | grep -q "woke review"; then
  ok "control: the idle codex pane was actually delivered to"
else
  fail "no delivery to the idle codex pane — the control cannot fail: $codex_out"
fi

# The busy check reads the pane's live TAIL, not the whole capture. A pane whose
# scrollback merely mentions a spinner is at rest NOW, and refusing it would not
# make recovery late — it would make force recovery impossible for as long as the
# line stayed in the capture window.
echo "-- stale spinner in scrollback"
render_stale_spinner review
sleep 1
"$MUX" send review stale-row "a row after a stale spinner" --no-notify >/dev/null 2>&1
"$MUX" deliver review --force >/dev/null 2>&1
sleep 1
if pane_text review | grep -q "a row after a stale spinner"; then
  ok "an idle pane with stale spinner history is still delivered to"
else
  fail "stale spinner history pinned the pane busy — force recovery refused forever"
fi

# ── Phase B: the stall watchdog withholds from a working pane ────
echo "-- stall watchdog"
render_busy
render_rest test
sleep 1
# A concurrent ./build.sh runs `muxcode upgrade-daemons`, which re-execs
# daemons across ALL sessions — including this scratch one.
pkill -f "watch $SESSION" 2>/dev/null || true
sleep 1
"$MUX" watch "$SESSION" --poll 2 >"$WORK/daemon.log" 2>&1 &
DPID=$!
sleep 1
kill -0 "$DPID" 2>/dev/null && ok "scratch daemon running" || fail "scratch daemon started"

# Two sightings 30s apart, plus margin for the poll and the injection.
sleep 40

[ "$(lifecycle_row task-stall-redrive 'run:run')" = "0" ] &&
  ok "no task-stall-redrive for the working pane" ||
  fail "the watchdog re-drove a working agent"
lifecycle_has "stall-skipped-busy" && ok "stall-skipped-busy row written" || fail "no stall-skipped-busy row"
if pane_text run | grep -q "Re-drive"; then
  fail "the watchdog typed into a running tool call"
else
  ok "working pane untouched across two check intervals"
fi

[ "$(lifecycle_row task-stall-redrive 'test:test')" != "0" ] &&
  ok "control: the watchdog still re-drove the resting pane" ||
  fail "no task-stall-redrive for the resting pane — the watchdog stopped catching stalls"
if pane_text test | grep -q "Re-drive"; then
  ok "control: re-drive text landed in the resting pane"
else
  fail "no re-drive text in the resting pane"
fi

# The fixture panes are real shells, so a delivered payload reaching a command
# line leaves evidence: a new file in the checkout. This is the assertion the
# earlier `codex-row]` / `stale-row]` droppings would have failed.
if [ "$(ls -A "$REPO")" = "$REPO_BEFORE" ]; then
  ok "the checkout is unchanged — no fixture pane executed a delivered payload"
else
  fail "the checkout gained entries: $(comm -13 <(printf '%s\n' "$REPO_BEFORE" | sort) <(ls -A "$REPO" | sort) | tr '\n' ' ')"
fi

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
