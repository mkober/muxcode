#!/usr/bin/env bash
# Integration verification for the delivery-acknowledgement feature
# (docs/requirements/drafts/delivery-acknowledgement.md).
#
# The delivery-ack redesign replaces the daemon's pane-scrape delivery inference
# with per-message RECEIPTS — a positive signal the agent actually read its inbox
# (a true "acked" receipt for in-process consumers: Claude self-poll / the LLM
# harness / a --wait sender; a weaker "delivered" receipt for the OpenCode/Codex
# verified-inject path) — plus agent self-poll and a receipt-gap backstop. Almost
# all of it is daemon/bus internal logic, so — exactly like
# scripts/test-watchdog-churn.sh — the authoritative end-to-end coverage lives in
# the Go suite; this script runs precisely those tests with a clean exit code,
# then adds a non-destructive live smoke section.
#
# OFFLINE (always runs, deterministic): the Go tests below assert every Phase 1-5
# behavior — the receipt store, per-provider receipt kinds, receipt-gap detection
# + recovery, verified-inject dropped-Enter retry-until-received, mid-task-restart
# send unblocking, cutover gating, and the Stop-hook self-poll decision logic.
#
# LIVE (graceful skip when no muxcode session): non-destructive smoke only — the
# `muxcode deliver --force` manual escape hatch still exists, and the receipt
# store is active in the running session. The DESTRUCTIVE Phase 6 scenarios (kill
# a poll loop, restart a mid-task agent, drop an Enter) are asserted
# deterministically OFFLINE via the Go tests above; reproducing them against real
# providers requires a dedicated throwaway session with MUXCODE_DELIVERY_ACK=1 and
# is intentionally out of scope for this CI-style runner (it must never disrupt a
# live user session).
#
# Usage: bash scripts/test-delivery-ack.sh
set -euo pipefail

cd "$(git rev-parse --show-toplevel 2>/dev/null || echo .)/tools/muxcode"

GREEN=$'\033[0;32m'; RED=$'\033[0;31m'; YELLOW=$'\033[0;33m'; NC=$'\033[0m'
fail=0

run() { # run "label" <go test args...>
  local label="$1"; shift
  echo "→ ${label}"
  local out="/tmp/delivack-$$-${RANDOM}.out"
  if go test "$@" >"$out" 2>&1; then
    grep -E '^(--- PASS|ok|PASS)' "$out" | sed 's/^/    /'
    # A -run pattern matching zero tests still exits 0. Require at least one real
    # PASS so a future test rename can't silently pass this check.
    if ! grep -qE '^--- PASS' "$out"; then
      echo "  ${RED}✗ ${label} — no tests matched the -run pattern (silent pass)${NC}"
      fail=1
    else
      echo "  ${GREEN}✓ ${label}${NC}"
    fi
  else
    grep -E '^(--- FAIL|FAIL|.*\.go:[0-9]+:)' "$out" | sed 's/^/    /'
    echo "  ${RED}✗ ${label}${NC}"
    fail=1
  fi
  rm -f "$out"
}

pass_live() { echo "  ${GREEN}✓ $1${NC}"; }
fail_live() { echo "  ${RED}✗ $1${NC}"; fail=1; }
skip_live() { echo "  ${YELLOW}– $1 (skipped)${NC}"; }

echo "== delivery-ack: offline Go coverage =="

# Phase 1/3 — receipt store + per-provider receipt kinds. An in-process consume
# writes a true "ack"; a verified inject writes the weaker "delivered"; a receipt
# never regresses a message already marked responded, and survives a GC'd status.
run "receipt store: ack vs delivered, no-regress, consume writes ack" \
  ./bus/ -count=1 -v -run \
  'TestWriteReceipt_Ack|TestWriteReceipt_DeliveredIsWeakerThanAck|TestWriteReceipt_DoesNotRegressResponded|TestWriteReceipt_NoPriorStatus|TestReadReceipt_NoReceipt|TestReceive_WritesConsumeReceipt|TestReceiveMarksAcked'

# Phase 5 — receipt-gap detector (the poll-loop-is-dead signal that replaces
# pane-scrape wedge detection): stale un-receipted messages surface, self-sends
# are ignored (never a permanent gap), and an in-process consume clears the gap.
run "receipt gap: stale surfaces, self-sends ignored, consume clears" \
  ./bus/ -count=1 -v -run \
  'TestReceiptGap_ReturnsStaleUnreceipted|TestReceiptGap_IgnoresSelfSends|TestReceive_ClearsReceiptGap'

# Phase 4 — OpenCode/Codex verified-inject: dropped-Enter retry-until-received,
# parked text leaves the inbox intact (no strand), a confirmed submit writes a
# "delivered" receipt.
run "verified-inject: needle, landed/parked/submitted retry, no strand, delivered receipt" \
  ./bus/ -count=1 -v -run \
  'TestInjectionNeedle|TestComposerHoldsText|TestVerifyInjectionLanded_|TestConfirmInjectionAndConsume_'

# Phase 2 — Claude self-poll + Stop-hook re-launch: the decision logic that keeps
# the self-poll listener alive (the new single point of delivery reliability) and
# the poll markers that suppress redundant daemon wake-ups.
run "stop-hook self-poll: decision, stop_hook_active guard, block format, poll markers" \
  ./bus/ -count=1 -v -run \
  'TestDecideStopHook|TestParseToolEvent_StopHookActive|TestFormatStopBlock|TestShouldPollInbox|TestPollDetectsTriggerChange|TestPollingMarkerPreventsDisplayMessage|TestWaitingMarkerPreventsDisplayMessage'

# Phase 5 (kept path) — a restarted mid-task agent must not have new sends blocked:
# in-flight tasks are cleared on relaunch / expire, so the dedup guard stops
# silently dropping re-sent requests to it.
run "mid-task restart: in-flight tasks cleared/expired unblock sends" \
  ./bus/ -count=1 -v -run \
  'TestClearInFlightTasksForRole_UnblocksSendsAfterRestart|TestClearInFlightTasksForRole_ClearsHostedRole|TestClearInFlightTasksForRole_LeavesOtherRolesAlone|TestHasInFlightTaskForRole_IgnoresExpired|TestTaskExpired'

# Phase 5 — daemon cutover: the MUXCODE_DELIVERY_ACK gate + kill switch, the
# checkPollHealth backstop (records a gap, alerts edit once it persists, clears on
# receipt, inert when the cutover is off), and the pane-scrape delivery checks
# (checkIdleAgents / checkParkedInput / checkPaneSweep — the notified-IDs delivery
# path) bypassing when the cutover is active.
run "daemon cutover: gate + poll-health backstop + pane-scrape delivery bypassed" \
  ./daemon/ -count=1 -v -run \
  'TestAckDeliveryActive|TestCheckPollHealth_InertWhenCutoverOff|TestCheckPollHealth_RecordsGapAndAlertsWhenActive|TestCheckPollHealth_ClearsGapOnReceipt|TestDeliveryChecksGatedWhenCutoverActive'

echo ""
echo "== delivery-ack: live smoke (non-destructive) =="

# The manual recovery valve must still exist: `muxcode deliver --force` injects a
# stuck agent's pending inbox regardless of pane state. Works with or without a
# session (usage text is enough to prove the flag survived the cutover).
deliver_usage="$(muxcode deliver 2>&1 || true)"
if echo "$deliver_usage" | grep -q -- '--force'; then
  pass_live "muxcode deliver --force escape hatch present"
else
  fail_live "muxcode deliver --force escape hatch missing"
fi

SESSION="${BUS_SESSION:-}"
if [ -z "$SESSION" ]; then
  skip_live "live session checks — no BUS_SESSION (run inside a muxcode session)"
else
  # Documented bus path (bus/config.go BusDir): /tmp/muxcode-bus-<session>.
  # Treat a base-path miss as a skip, not a failure, so the runner stays robust
  # if the bus root ever moves.
  busdir="/tmp/muxcode-bus-${SESSION}"
  if [ -d "${busdir}/delivery" ]; then
    n="$(find "${busdir}/delivery" -name '*.status' 2>/dev/null | wc -l | tr -d ' ')"
    pass_live "receipt store active in session ${SESSION} (${n} status file(s))"
  else
    skip_live "receipt store dir not found at ${busdir}/delivery"
  fi
  if muxcode status >/dev/null 2>&1; then
    pass_live "muxcode status healthy in live session ${SESSION}"
  else
    skip_live "muxcode status unavailable in this environment"
  fi
fi

echo ""
if [ "$fail" -eq 0 ]; then
  echo "${GREEN}delivery-ack: all checks passed${NC}"
else
  echo "${RED}delivery-ack: FAILURES above${NC}"
fi
exit "$fail"
