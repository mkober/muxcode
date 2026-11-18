# MUX-234: Three Integration Scripts Fail — Their Fixtures Send to Roles the Consumer Guard Refuses

**Tracking:** [mkober/muxcode#242](https://github.com/mkober/muxcode/issues/242)

Three `scripts/test-*.sh` fail on every run: `test-status-line-task-close` (14 passed, 5 failed),
`test-plan-verify-verdict` (32 passed, 4 failed) and `test-codex-idle-delivery` (29 passed, 7
failed). For the first two the cause is in their own logs:
[MUX-145](../completed/MUX-145-messages-routed-to-windowless-role.md)'s consumer guard refuses a
send to a role the scratch session has no window, listener or headless agent for. The third fails
at a delivery step and its cause is not determined. `bash scripts/test-all.sh` exits 1 on every run
because of them.

## Context

### Source and standard of evidence

Found 2026-11-17 by the worker on
[MUX-233](../completed/MUX-233-hermetic-test-scripts-run-on-the-users-real-tmux-server.md), which ran
twenty-two scripts under a measuring wrapper and re-ran the failures standalone with the same
counts (run task `1794960246-spawn-56d56314-976f2eb6`). Filed by plan on the user's standing
instruction to file defects found along the way. Plan read the three re-run logs
(`/tmp/mux233-rerun-test-*.log`) and did not run the scripts.

The binary was the installed `v0.1.76-1-gb0d3b15-dirty`, built from the loop's working tree, so a
regression from uncommitted work is not excluded — Phase 1 re-runs against `main`.

### Observed — verified in the logs

| Script | Result | Failing checks | In the log |
|--------|--------|----------------|------------|
| `test-status-line-task-close` | 14 / 5, floor 19 | The three "control: … task completed" checks; "genuine review reply fired verify-spec at plan"; "plan-verify row written" | `[task-detect] failed to send response for … build→edit:response: edit has no window, inbox listener or headless agent: no consumer for the role's inbox` — and the same for test and review |
| `test-plan-verify-verdict` | 32 / 4 | Section E: "passing review never fired"; no verify-spec in plan's inbox; it does not name the active spec; no plan-verify row | `[daemon] failed to send plan verification: daemon→plan:verify-spec: plan has no window, inbox listener or headless agent: no consumer for the role's inbox` |
| `test-codex-idle-delivery` | 29 / 7, floor 36 | Section 1, "stale answered task, unforced wake": the pending request is not typed into the idle pane and does not leave the inbox; no `[wakeup] skipping` line; no `wake-skipped` row; the task carries no answer and stays in flight; no `task-answered-elsewhere` row | No refusal text — the script discards the output that would show one |

Earlier results, from the worker's report: `test-status-line-task-close` 19 / 0 and
`test-plan-verify-verdict` 36 / 0 on 2026-11-13; no earlier log for `test-codex-idle-delivery`.
MUX-145 merged on 2026-11-16 (`f87cb3d`).

### Cause

**For the first two — the consumer guard, verified by the refusal in the log.** MUX-145 made
`bus.Send` refuse a message to a role nothing can read for. It added
`export MUXCODE_CONSUMER_GUARD=0` to twenty-eight scripts whose fixtures play an agent by reading
its inbox from the script
([MUX-230](./MUX-230-integration-scripts-opt-out-of-the-consumer-guard.md) lists them). These two
are not among the twenty-eight, and neither sets the variable — plan checked with `grep`. Their
daemons send to `edit` and `plan`, which their scratch sessions have no window for.

Why MUX-145's own run did not catch them is not known: its close-out records which scripts were
run, and plan has not re-read it.

**For `test-codex-idle-delivery` — not determined.** It also does not set the variable, so the same
guard is the first candidate. But its failing step is a wake typed into a pane, not a send into an
inbox, and nothing in its log names a refusal. Other candidates: the wake gate
([MUX-192](../completed/MUX-192-stale-in-flight-task-starves-codex-delivery.md), which this script
was written for), and the re-drive changes of
[MUX-232](../completed/MUX-232-redrive-arrives-truncated-with-no-id-and-no-reply-route.md) and
[MUX-220](../completed/MUX-220-wake-batch-drops-a-roles-own-delegation-reply.md), both merged on
2026-11-17. If Phase 1 finds a product fault rather than a stale fixture, that is a different
defect and is to be raised, not fixed under this spec's name.

**One of its checks is also unstable.** Across four runs on 2026-11-17 the script gave 29 / 7
three times and 28 / 8 once (MUX-233's full re-measure, 19:17–19:33; not reproduced). Which check
flipped was not recorded. Phase 1 should find it: a script that fails the same way every time and
one with a flaky check are different problems.

### Two more failing scripts, cause not looked at (added 2026-11-18)

MUX-233's full runner (`bash scripts/test-all.sh`, task `1794965635-spawn-56d56314-bcb67e77`,
2026-11-17) showed two further failures, neither among the scripts MUX-233 changed:

| Script | Result | Standing |
|--------|--------|----------|
| `test-fix-loop-cap` | 26 / 5 | Passed on 2026-11-13. The same counts on a clean export of `HEAD` (`eaf0e69`), so it is on `main` and not from MUX-233's change |
| `test-restart-definition` | 28 / 2 | The same |

Both lines are from the MUX-233 worker's findings as plan recorded them in that spec on the 17th.
The findings file and the run logs were under `/tmp` and did not survive the machine's restart on
2026-11-18, so the failing checks are not named here and plan has read no log for either. Neither
sets `MUXCODE_CONSUMER_GUARD` (plan checked with `grep`, 2026-11-18), and both last passed before
the consumer guard merged — which makes the guard a candidate, nothing more. They are recorded here so the runner's failures have one home;
if Phase 1 finds a different cause, they are split out.

MUX-233 said these were added here on the 17th. They were not; its review caught that.

### Consequence

- The full integration runner cannot go green, so a new failure is easy to miss among three known
  ones.
- Two of the three are the scripts that pin the daemon's `verify-spec` gate (MUX-172) and the
  status-line task-close rule (MUX-154). While they fail at a fixture step, the checks after it do
  not run as written.

### Not this spec

`test-prompt-mode` also fails (17 / 7, 3 skipped) and did so before this loop; it is the subject of
parked [MUX-122](./MUX-122-prompt-agent-turn-attribution-and-fix.md).

## Requirements

### Acceptance criteria

- [ ] The cause of each of the three failures is recorded here with the evidence — for `test-codex-idle-delivery`, determined, not assumed from the other two
- [ ] `bash scripts/test-status-line-task-close.sh` exits 0, floor met
- [ ] `bash scripts/test-plan-verify-verdict.sh` exits 0, floor met
- [ ] `bash scripts/test-codex-idle-delivery.sh` exits 0, floor met — or its failure is shown to be a product fault and filed separately, and this criterion is ticked as handed off with a link
- [ ] `test-fix-loop-cap` and `test-restart-definition`: the failing checks and the cause of each are recorded here, and each either exits 0 with its floor met or is filed separately with a link (added 2026-11-18)
- [ ] No assertion is weakened or removed to get there; a check whose text changes is listed here with the reason
- [ ] Negative control: with the fix reverted in a scratch copy, each script fails as it does today

### Technical approach

Give the fixture a real consumer where that is cheap — a window for the role in the scratch
session, which is what `RoleHasConsumer` looks for, and the direction MUX-230 sets. Where the
script means to read an inbox without a consumer, set `MUXCODE_CONSUMER_GUARD=0` with the same
comment the other twenty-eight carry, and add the script to MUX-230's list so that spec removes it
with the rest. The user's recorded decision in MUX-230 is that the scripts keep the switch for now,
so either road is within it. Stop discarding the `deliver` output in `test-codex-idle-delivery`, as
MUX-212 did for its sibling, so the next failure names its cause.

These three are among the scripts MUX-233 is moving onto a private tmux server. Whichever lands
second rebases onto the first; the two changes do not depend on each other.

### Key files

| File | Purpose |
|------|---------|
| `scripts/test-status-line-task-close.sh` | Sends to edit and plan from a scratch daemon |
| `scripts/test-plan-verify-verdict.sh` | Section E, the passing review that should fire `verify-spec` |
| `scripts/test-codex-idle-delivery.sh` | Section 1, the unforced wake |
| `tools/muxcode/bus/consumer.go` | `RoleHasConsumer`, `refuseWithoutConsumer` — read only |
| `docs/requirements/backlog/MUX-230-integration-scripts-opt-out-of-the-consumer-guard.md` | The list a script joins if it takes the opt-out |

## Implementation

### Phase 1: Establish the causes

- [ ] Run the three scripts against a binary built from `main`; record the counts, so a regression from uncommitted work is ruled in or out
- [ ] For `test-codex-idle-delivery`, capture the `deliver` and wake output the script discards and the lifecycle rows of the failing section; record the cause
- [ ] Run `test-fix-loop-cap` and `test-restart-definition` the same way; record the counts, the failing checks and what each log says (added 2026-11-18)
- [ ] Record why MUX-145's own verification did not show the first two failing, if its close-out says

### Phase 2: Fix the fixtures

- [ ] `test-status-line-task-close` and `test-plan-verify-verdict`: a consumer for the roles their daemons send to, or the opt-out with the script added to MUX-230's list — record which and why
- [ ] `test-codex-idle-delivery`: the fix Phase 1's cause calls for, and its discarded output kept
- [ ] If any of the three turns out to be a product fault: stop, record it, and report it for filing

### Phase 3: Integration test

- [ ] Run the three scripts through the run agent: every check passes, floors met, exit 0; record the counts here
- [ ] Negative control: each script, with its fix reverted in a scratch copy, fails as recorded above
- [ ] `bash scripts/test-all.sh` — the full runner — and record its summary; the three no longer appear as failed
- [ ] Update each script's entry in `CLAUDE.md` if its floor or setup changed

## Related

- [MUX-145](../completed/MUX-145-messages-routed-to-windowless-role.md) — the consumer guard
- [MUX-230](./MUX-230-integration-scripts-opt-out-of-the-consumer-guard.md) — the twenty-eight scripts that opt out, and the plan to give them real consumers
- [MUX-233](../completed/MUX-233-hermetic-test-scripts-run-on-the-users-real-tmux-server.md) — where these failures were found
- [MUX-209](../completed/MUX-209-force-respond-script-non-forced-deliver-no-longer-surfaces-in-flight-skip.md), [MUX-212](../completed/MUX-212-stall-redrive-busy-script-stale-spinner-in-scrollback-pins-pane-busy.md) — two earlier scripts that failed on a stale fixture, not the product

## Status

**Backlog** — 0/18. Filed 2026-11-17. The consumer-guard cause is verified for two scripts and a
candidate for the third. Two more failing scripts were added on 2026-11-18 with no cause looked at;
the title and GitHub issue #242 still say three.
