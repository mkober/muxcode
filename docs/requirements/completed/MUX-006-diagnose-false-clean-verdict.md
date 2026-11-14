# Diagnose False Clean Verdict

**Tracking:** [mkober/muxcode#6](https://github.com/mkober/muxcode/issues/6)

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

### Field corroboration (2026-08-17)

A second incident, different failure mode, same contradictory verdict: `muxcode diagnose build` printed `✅ No issues detected` while its own evidence showed `inbox-notify` with **no** subsequent idle-wake and `Notified IDs: 239 STALE`. The agent was alive but wedged, so `checkStaleNotifiedIDs`' opening `if !report.AgentState.IsIdle { return nil }` disqualified the one detector that had the evidence. Pane capture was the only reliable signal. This confirms the verdict-contradicts-evidence problem is a family (detectors self-disqualifying on the very states they should flag), not solely the missing dead-agent check — strengthening the "audit whether other detectors should be skipped or restructured once the agent's true state is known" consideration below.

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

- [x] A dead agent produces a critical finding naming the agent as dead — lead finding `agent-down`, critical, summary "Agent is dead: …" (`TestRunDiagnostics_DeadAgentVerdict`)
- [x] `muxcode diagnose <dead-agent>` exits non-zero — exit 1 through the real CLI (`scripts/test-diagnose-dead-agent.sh` section C; exit 0 on the pre-fix build, section E)
- [x] `muxcode diagnose --all` shows the dead agent as critical in the summary table — its row reads dead and critical and `--all` exits 1 (section C)
- [x] Remediation names a command that relaunches the agent (`muxcode resume <role>` for Claude, `muxcode reload <role>` otherwise) and states the inbox survives — `TestRunDiagnostics_DeadAgentVerdict` asserts both commands and rejects `agent-health --start`; the script's section D runs the advertised command. Amended 2026-11-14 after close-out, see "Amendment after close-out"; originally "Remediation names `muxcode agent-health --start <role>` and states the inbox survives", which was ticked on the 2026-08-13 report
- [x] A live, healthy agent still reports `✅ No issues detected` (no false positives) — the test's healthy control: 0 findings, clean verdict, `healthy` summary
- [x] Existing diagnose tests still pass — `./test.sh` exit 0, modules=2 passed=2 (test-history row 1794665014)

### Key files

| File | Change |
|------|--------|
| `bus/diagnose.go` | Add `checkAgentDead` detector, registered first in `diagnosticChecks` |
| `bus/diagnose_test.go` | Unit tests: dead → critical finding, live → none |
| `cmd/diagnose.go` | No change expected — existing critical-severity exit path covers it |

## Implementation

### Phase 1: Detector

- [x] Add `checkAgentDead` to `bus/diagnose.go`, registered first in `diagnosticChecks` — met by the existing `checkAgentDown`, registered second; name and slot are recorded deviations (below), and its summary now reads "Agent is dead: …"
- [x] Unit tests: dead agent yields a critical finding; live agent yields none — `TestRunDiagnostics_DeadAgentVerdict`, beside the existing `TestCheckAgentDown_Detected` and `_ExpectedAbsences`

### Findings at activation (2026-11-14)

Read from the tree at activation; nothing here is ticked until this run's build, test and review pass.

| Finding | Detail |
|---------|--------|
| The detector already exists | `checkAgentDown` (`bus/diagnose.go`, commit f30ecb8 "Fix diagnose false-clean verdicts on stuck inboxes") — critical, failure mode `agent-down`, names `muxcode agent-health --start <role>`, states the inbox survives, counts waiting actionable messages |
| It differs from the proposal in two ways | Named `checkAgentDown`, not `checkAgentDead`, and registered **second** in `diagnosticChecks`, after `checkDaemonDead`; it also stays silent for an intentionally stopped or reloading agent, which the proposal did not ask for |
| Unit coverage exists | `bus/diagnose_test.go` references it; whether it pins both the dead and the live case is for this phase to confirm |
| The wedged-but-alive family has a backstop | `checkUnexplainedEvidence` runs last, so a stuck actionable inbox is not reported clean (the 2026-08-17 corroboration) |
| No integration script covers it | No `scripts/test-*.sh` asserts `diagnose` on a dead agent — Phase 2 is open in full |

So Phase 1 is expected to be verification and gap-filling rather than new code: confirm each acceptance criterion against the existing detector, and record the name and registration order as deviations rather than rework them.

### Results for Phase 1 (2026-11-14)

Verified after this run's build (`./build.sh` exit 0), `./test.sh` (exit 0, 2 of 2 modules) and review (0 must-fix, 0 should-fix, `/tmp/muxcode-review-1794665019.txt`).

| Item | Result |
|------|--------|
| Code change | One line in `bus/diagnose.go`: the `agent-down` summary now names the agent dead. No test or doc pinned the old text |
| New test | `TestRunDiagnostics_DeadAgentVerdict` replays the 2026-08-13 plan report (dead, 2 of 3 messages actionable, daemon healthy) through `RunDiagnostics`, `FormatDiagnosticReport` and `FormatDiagnosticSummary`. It fails if `checkAgentDown` leaves `diagnosticChecks`, which the detector-only tests miss |
| Deviation — name | `checkAgentDown`, not `checkAgentDead`; kept, since `agent-down` is the failure mode other code and lifecycle rows already name |
| Deviation — slot | Registered after `checkDaemonDead`, not first. The test runs with the daemon alive, so the both-dead ordering is not pinned |
| Deviation — scope | Silent for a stopped or reloading agent (`TestCheckAgentDown_ExpectedAbsences`), which the proposal did not ask for |
| Still open | The exit code and the `--all` table are proven only at the formatter: `cmd/diagnose.go`'s exit path and the real table are unchanged and untested here. Both criteria wait on the Phase 2 script |

### Phase 2: Integration test

- [x] Add coverage to `scripts/test-*.sh` (or a new `scripts/test-diagnose-dead-agent.sh`) — the new script, listed in `CLAUDE.md`
- [x] Test: stop an agent, run `muxcode diagnose <role>`, assert exit code is non-zero — section C, on a SIGTERMed agent with no stop marker; an agent stopped on purpose is deliberately not flagged (section B)
- [x] Test: assert the output names the agent as dead and gives the restart remediation — section C
- [x] Test: restart the agent, re-run, assert exit 0 and a clean verdict — section D, the restart being the daemon's health sweep (see the deviation below)
- [x] Run the script and verify all checks pass — 34 passed, 0 failed, floor 34; build, `./test.sh` (2 of 2 modules) and review (0 must-fix, 0 should-fix, `/tmp/muxcode-review-1794665586.txt`) passed after it

### Results for the integration phase (2026-11-14)

Reported by the phase worker and verified the same day once this run's build, test and review had passed. Three earlier runs of the script failed on bugs in the script itself (null findings in `jq`, an id-capture race, a silently refused second send), not in diagnose.

| Item | Result |
|------|--------|
| Script | New `scripts/test-diagnose-dead-agent.sh` — hermetic: scratch bus, private tmux server, scratch daemon, `muxcode` and `scripts/fixtures/claude-stub` built from the tree. The build agent is a launched stub killed with SIGTERM |
| Run | 34 passed, 0 failed, floor 34 — run agent, task `1794665459-spawn-0616aad2-a5f9744c`, confirmed in `run-history.jsonl` |
| A | Live idle agent, empty inbox: exit 0, no findings, "No issues detected", `--all` row healthy |
| B (added) | Negative control — an agent stopped with `agent-health --stop` is down on purpose: no `agent-down` finding |
| C | The same agent with its stop marker cleared is a crash: exit 1, a leading critical `agent-down` finding naming it dead, the restart remediation, inbox survival and the two queued requests; its `--all` row is dead and critical and `--all` exits 1 |
| D | The daemon restarts it, the two queued requests reach the new agent, and diagnose returns to exit 0 and clean |
| E (added) | Pre-fix control — the tree with `checkAgentDown` patched out of `diagnosticChecks` in a scratch copy reproduces 2026-08-13: "No issues detected", exit 0, while this build exits 1 on the same pane |

Deviations from the phase's wording:

- **"Restart the agent" is not what `agent-health --start` does.** It only clears the stop marker; the daemon's health sweep performs the relaunch. So for a crashed agent that carries no stop marker, the remediation the finding names is a no-op, and the daemon's own restart is what recovers it. The script reaches the restart by stopping, clearing the marker, and letting the sweep relaunch, with first-sighting restarts off so the agent stays dead long enough to diagnose.
- **The remediation text is therefore accurate only for a stopped agent.** The criterion asks that it name `muxcode agent-health --start <role>`, and it does; whether the text should also say the daemon restarts a crashed agent on its own (or point at `muxcode resume <role>` for Claude) is not in this spec's scope and is left for the user.

### Amendment after close-out (2026-11-14)

Verified the same day: `./build.sh` (exit 0), `./test.sh` (exit 0, 2 of 2 modules, test-history row 1794665885) and review (0 must-fix, 0 should-fix, `/tmp/muxcode-review-1794665885.txt`) all ran after the fix's last edit, and `run-history.jsonl` confirms the script at 36 passed, 0 failed, floor 36. Criterion 4 is ticked on that evidence; the table below is as first recorded.

After the spec was closed at 13/13, edit sent the worker a must-fix on the no-op remediation above, and the worker asked plan to amend acceptance criterion 4. This is edit's change, not a recorded user decision.

| Item | Change |
|------|--------|
| Criterion 4 | Was "names `muxcode agent-health --start <role>`"; now "names a command that relaunches the agent (`resume` for Claude, `reload` otherwise)". Unticked: the new wording has had no build, `./test.sh` or review in this run |
| `checkAgentDown` | Remediation leads with `relaunchCommand`: `muxcode resume <role>` for a Claude agent, `muxcode reload <role>` for any other provider. Every role but edit also gets a line saying the daemon's health sweep relaunches a dead agent itself; edit does not, being resume-only |
| Other detectors | `checkActiveWithStaleMessages` and `checkReloadMarkerStuck` name the same command, with `resume --force` when the agent is alive — a change outside this spec's stated scope |
| Script | Reported 36 passed, 0 failed, floor 36 (task `1794665729-spawn-0616aad2-230f24b8`); section D now runs the named remediation as written rather than waiting on the sweep. Not confirmed in `run-history.jsonl` at the time of writing |
| Superseded | The first deviation above (the script reaching the restart through the stop marker and the sweep) and the "34/34" counts describe the tree before this change |

## Provenance

Found by the edit agent while diagnosing a real `plan` agent death during the branch-time-tracking work. The agent was recovered with `muxcode agent-health --start plan`; its 3 queued messages survived the restart.

## Status

Complete — closed 2026-11-14 by `55-defect-backlog-loop` run `1794626267-55-defect-backlog-loop-15547e2e` (activated the same day from Defects rank 9). 13/13: the detector was already in the tree as `checkAgentDown` (f30ecb8); this run made its summary name the agent dead, pinned the verdict with `TestRunDiagnostics_DeadAgentVerdict`, and added `scripts/test-diagnose-dead-agent.sh` (34/34). After close-out the remediation was changed to name `muxcode resume`/`muxcode reload` on edit's must-fix (not a recorded user decision) and criterion 4 amended to match; it was reopened, then re-verified and ticked once build, `./test.sh` and review had passed over the fix (script 36/36). 13/13.
