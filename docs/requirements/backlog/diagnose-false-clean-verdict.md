# Diagnose False Clean Verdict

`muxcode diagnose` reports a dead agent as healthy: the report prints `State: dead` in its evidence section, then concludes `✅ No issues detected` and exits 0. The command exists to be run when an agent is not responding — dead is the single most likely reason, and the one case it cannot name.

## Context

### Observed failure (2026-08-13 ~11:37)

The `plan` agent died. `muxcode diagnose plan` printed, in one report:

```
State:  dead
Health: dead
Inbox:  3 message(s) (2 actionable)
...
✅ No issues detected
```

The evidence section was correct. The verdict line contradicted it.

### Root cause — a pure omission, not a logic bug

`bus/diagnose.go`:

- `CollectAgentState()` (~:101) already collects the fact: `IsAlive: IsAgentAlive(session, role)` (~:104), stored as `report.AgentState.IsAlive` and rendered in the evidence section.
- `diagnosticChecks` (~:397) registers 11 detectors: `checkDaemonDead`, `checkStaleNotifiedIDs`, `checkMissedSendKeys`, `checkIdleDetectionFailure`, `checkDaemonNotWaking`, `checkPostRestartWakeGap`, `checkProviderMismatch`, `checkReloadMarkerStuck`, `checkPendingInputBlocking`, `checkActiveWithStaleMessages`, `checkNoActionableMessages`.
- There is a check for the **daemon** being dead. There is **no check for the agent being dead**. No detector reads `AgentState.IsAlive` at all.

So the most severe state an agent can be in is collected, displayed, and never evaluated.

### Why the other checks do not compensate

A dead agent silently disqualifies the delivery detectors:

- `checkStaleNotifiedIDs` (~:445) and `checkMissedSendKeys` (~:473) both open with `if !report.AgentState.IsIdle { return nil }`. A dead agent is not idle.
- A dead agent has no notification markers (observed: `Notified IDs: 0`, no marker), so the marker-based detectors have nothing to match.

The result is zero findings, which the formatter (~:955) renders as `✅ No issues detected`.

### Impact

1. **Exit code lies.** `cmd/diagnose.go` (~:61-64) exits non-zero only when a finding has `Severity == "critical"`. Zero findings means **exit 0** — so `muxcode diagnose <dead-agent>` reports success. Any automation or agent gating on the exit status concludes the agent is healthy.
2. **`--all` summary is wrong** in the same way: a dead agent lands in the table with no findings.
3. It defeats the command's stated purpose. `diagnose` exists to be run when an agent is not responding — dead is the single most likely reason, and the one case it cannot name.

## Requirements

### Proposed fix

Add `checkAgentDead` as the **first** entry in `diagnosticChecks` (before `checkDaemonDead`, since agent death is the more specific diagnosis):

- Fires when `!report.AgentState.IsAlive`
- `Severity: "critical"` so the exit code and `--all` summary both reflect it
- Remediation text: `muxcode agent-health --start <role>`; note that the inbox persists across restart and queued messages are re-read
- Should mention pending actionable message count, since a dead agent holding actionable work is the urgent case

Consider also auditing whether other detectors should be skipped outright once the agent is known dead, so the report leads with the real cause instead of listing downstream symptoms.

### Acceptance criteria

- [ ] A dead agent produces a critical finding naming the agent as dead
- [ ] `muxcode diagnose <dead-agent>` exits non-zero
- [ ] `muxcode diagnose --all` shows the dead agent as critical in the summary table
- [ ] Remediation names `muxcode agent-health --start <role>` and states the inbox survives
- [ ] A live, healthy agent still reports `✅ No issues detected` (no false positives)
- [ ] Existing diagnose tests still pass

### Key files

| File | Change |
|------|--------|
| `bus/diagnose.go` | Add `checkAgentDead` detector, registered first in `diagnosticChecks` |
| `bus/diagnose_test.go` | Unit tests: dead → critical finding, live → none |
| `cmd/diagnose.go` | No change expected — existing critical-severity exit path covers it |

## Implementation

### Phase 1: Detector

- [ ] Add `checkAgentDead` to `bus/diagnose.go`, registered first in `diagnosticChecks`
- [ ] Unit tests: dead agent yields a critical finding; live agent yields none

### Phase 2: Integration test

- [ ] Add coverage to `scripts/test-*.sh` (or a new `scripts/test-diagnose-dead-agent.sh`)
- [ ] Test: stop an agent, run `muxcode diagnose <role>`, assert exit code is non-zero
- [ ] Test: assert the output names the agent as dead and gives the restart remediation
- [ ] Test: restart the agent, re-run, assert exit 0 and a clean verdict
- [ ] Run the script and verify all checks pass

## Provenance

Found by the edit agent while diagnosing a real `plan` agent death during the branch-time-tracking work. The agent was recovered with `muxcode agent-health --start plan`; its 3 queued messages survived the restart.

## Status

Backlog
