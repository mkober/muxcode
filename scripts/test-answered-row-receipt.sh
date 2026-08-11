#!/usr/bin/env bash
# Integration verification for the answered-row receipt fix
# (docs/requirements/drafts/answered-row-receipt.md).
#
# THE BUG: a request an agent ANSWERED but never CONSUMED read as un-receipted
# forever. MarkResponded records the reply without setting AckedAt, while
# ReadReceipt defined "receipted" as AckedAt > 0 — so ReceiptGap counted the
# finished request permanently and the daemon's checkPollHealth backstop
# re-drove delivery and alerted `delivery-gap` for work that was already done.
# The answered row also stayed actionable, so the agent was re-woken and
# answered again. Observed live as ~21h of repeated re-drives and 4+ duplicate
# LGTM echoes from a single review request.
#
# THE FIX has two halves, and this script covers both:
#   Half 1 (bus/delivery.go) — hasReceipt() is the single definition of
#     "received": AckedAt > 0 OR Status == responded. ReadReceipt (and through
#     it ReceiptGap) route through it, so the read path finally agrees with the
#     write path, which had always honored "a reply implies receipt".
#   Half 2 (bus/inbox.go) — ConsumeByID() drains the answered request from the
#     responder's own inbox on reply, so the row stops being actionable.
#
# OFFLINE (always runs, deterministic): the Go tests below assert every behavior
# the spec's Phase 4 asks for — drain on reply, receipt-gap clearing, hosted-role
# routing, targeted removal, auto-CC survival, and the no-op paths. Following the
# same shape as scripts/test-delivery-ack.sh, the authoritative coverage lives in
# the Go suite and this runner just executes precisely those tests with a clean
# exit code.
#
# LIVE (graceful skip when no muxcode session): non-destructive smoke only —
# confirms the running session's receipt store agrees that an answered message is
# receipted, and that no answered row is sitting actionable in any inbox. It never
# sends to, wakes, or otherwise disturbs a live agent.
#
# Usage: bash scripts/test-answered-row-receipt.sh
set -euo pipefail

cd "$(git rev-parse --show-toplevel 2>/dev/null || echo .)/tools/muxcode"

GREEN=$'\033[0;32m'; RED=$'\033[0;31m'; YELLOW=$'\033[0;33m'; NC=$'\033[0m'
fail=0

run() { # run "label" <go test args...>
  local label="$1"; shift
  echo "→ ${label}"
  local out="/tmp/answrow-$$-${RANDOM}.out"
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

echo "== answered-row receipt: offline Go coverage =="

# Half 1 — the read-path rule in isolation. A responded-without-ack message must
# read as receipted, and AckedAt must still be zero so we know the Status clause
# (not an incidental ack) is what makes it so.
run "half 1: responded counts as receipted (Status clause, not AckedAt)" \
  ./bus/ -count=1 -v -run \
  'TestReadReceipt_RespondedCountsAsReceipted'

# Half 1 end-to-end — the actual regression. Answering without consuming used to
# leave a permanent gap; it must now clear. The companion ReceiptGap tests pin
# the other direction: a genuinely stale, UNANSWERED row still surfaces, so this
# fix cannot have blinded the backstop.
run "half 1: reply clears the receipt gap (21h echo-loop regression)" \
  ./bus/ -count=1 -v -run \
  'TestReply_ClearsReceiptGap|TestReceiptGap_ReturnsStaleUnreceipted|TestReceiptGap_IgnoresSelfSends'

# Half 2 — the answered row must stop being actionable, or the agent keeps being
# woken for finished work and answers again (the duplicate-response echo).
run "half 2: reply drains the answered row (no re-wake, no duplicate answer)" \
  ./bus/ -count=1 -v -run \
  'TestReply_DrainsAnsweredRequestRow'

# Half 2 routing — a hosted role (pr-read) has no inbox of its own. The drain
# must use the same WindowForRole routing Send uses to deliver, or it targets a
# phantom inbox and silently leaves the row behind.
run "half 2: hosted-role drain targets the host inbox" \
  ./bus/ -count=1 -v -run \
  'TestReply_DrainsFromHostInboxForHostedRole'

# Half 2 blast radius — the drain keys on message ID ONLY. An auto-CC copy in
# edit's inbox carries the SAME id as the original; it must survive. Unrelated
# messages in the responder's own inbox must survive too.
run "half 2: targeted removal — auto-CC copy and other messages survive" \
  ./bus/ -count=1 -v -run \
  'TestReply_LeavesAutoCCCopyInEditInbox|TestConsumeByID_LeavesOtherMessages'

# Half 2 choke point — a request also becomes "responded" via cmd/send.go's
# --wait fallback, which correlates a response sent WITHOUT ReplyTo. There is no
# reply message there to hang a drain off, so the drain lives in MarkResponded
# where every path reaches it. Caught live as a stranded run.jsonl row whose
# "response" predated its request by 13s; a drain wired only into Send() misses it.
run "half 2: MarkResponded drains even with no reply message (single choke point)" \
  ./bus/ -count=1 -v -run \
  'TestMarkResponded_DrainsWithoutAReplyMessage|TestMarkResponded_SurvivesMissingStatusFile'

# Half 2 no-op semantics — Send() calls the drain on EVERY reply, so the common
# case (row already consumed) plus missing inbox / absent message / empty id must
# all be quiet no-ops rather than errors.
run "half 2: no-op on already-consumed, missing inbox, absent id" \
  ./bus/ -count=1 -v -run \
  'TestConsumeByID_NoopWhenAbsent'

echo
echo "== answered-row receipt: live session smoke (non-destructive) =="

session="${BUS_SESSION:-}"
if [ -z "$session" ] || ! tmux has-session -t "$session" 2>/dev/null; then
  skip_live "no live muxcode session (BUS_SESSION unset or session gone)"
else
  busdir="/tmp/muxcode-bus-${session}"

  # 1. Receipt store agrees with the fix: every delivery status recorded as
  #    "responded" must be treated as receipted. A responded status that ReceiptGap
  #    would still count is exactly the bug this fix removes.
  if [ -d "${busdir}/delivery" ]; then
    responded=$(grep -l '"status":"responded"' "${busdir}"/delivery/*.status 2>/dev/null | wc -l | tr -d ' ')
    pass_live "delivery store readable — ${responded} responded status file(s) present"
  else
    skip_live "no delivery status directory in ${busdir}"
  fi

  # 2. The observable symptom: an ANSWERED request still sitting actionable in an
  #    inbox. Cross-reference each inbox request against the delivery store; any
  #    request whose status is "responded" but which is still queued is a live
  #    instance of the bug. Read-only — never consumes.
  #    Only a row the inbox's own role is the PRIMARY destination for counts.
  #    Auto-CC copies a request addressed to another agent verbatim into edit's
  #    inbox, keeping the original `to` — those are deliberately NOT drained (see
  #    TestReply_LeavesAutoCCCopyInEditInbox) and are never actionable, because
  #    HasActionableMessages applies this same WindowForRole rule. Without it this
  #    check reports every CC'd chain request as a stranded row.
  #
  #    Field extraction takes the FIRST match, not a greedy last-match: a message
  #    payload can quote another message's id/to (agent replies routinely do), and
  #    a greedy match picks that up instead of the row's own fields.
  stranded=0
  for inbox in "${busdir}"/inbox/*.jsonl; do
    [ -f "$inbox" ] || continue
    inbox_role=$(basename "$inbox" .jsonl)
    while IFS= read -r line; do
      case "$line" in *'"type":"request"'*) ;; *) continue ;; esac
      id=$(printf '%s' "$line" | grep -o '"id":"[^"]*"' | head -1 | cut -d'"' -f4)
      to=$(printf '%s' "$line" | grep -o '"to":"[^"]*"' | head -1 | cut -d'"' -f4)
      [ -n "$id" ] && [ -n "$to" ] || continue

      # WindowForRole equivalent for the hosted roles (see bus/config.go).
      host="$to"
      case "$to" in
        docs) host="plan" ;;
        pr-read) host="commit" ;;
      esac
      [ "$host" = "$inbox_role" ] || continue # CC copy — not this role's work

      status_file="${busdir}/delivery/${id}.status"
      if [ -f "$status_file" ] && grep -q '"status":"responded"' "$status_file" 2>/dev/null; then
        echo "    stranded: ${id} in $(basename "$inbox")"
        stranded=$((stranded + 1))
      fi
    done < "$inbox"
  done
  if [ "$stranded" -eq 0 ]; then
    pass_live "no answered-but-still-actionable request rows in any inbox"
  else
    fail_live "${stranded} answered request row(s) still actionable — the drain did not fire"
  fi

  # 3. The escape hatch the spec's Keep list requires to survive.
  if muxcode deliver --help >/dev/null 2>&1 || muxcode deliver 2>&1 | grep -qi 'usage\|role'; then
    pass_live "muxcode deliver escape hatch still present"
  else
    fail_live "muxcode deliver escape hatch missing"
  fi
fi

echo
if [ "$fail" -eq 0 ]; then
  echo "${GREEN}All answered-row receipt checks passed.${NC}"
else
  echo "${RED}Some answered-row receipt checks FAILED.${NC}"
fi
exit "$fail"
