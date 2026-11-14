# MUX-221: The Dead-Agent Diagnose Test Is Uncontrolled, and a Hosted Role's Remediation Names a Command That Refuses

**Tracking:** [mkober/muxcode#204](https://github.com/mkober/muxcode/issues/204)

A follow-up to [MUX-006](../completed/MUX-006-diagnose-false-clean-verdict.md) (PR #199), from two
Copilot review threads read after the merge. The integration script that proves the dead-agent
verdict does not disable the daemon restart it says it disables, and the remediation `diagnose`
prints for a hosted role is a command `muxcode resume` refuses.

## Context

### Source and standard of evidence

Both verified by edit against main `82635f8` and by plan in the tree.

| Thread | Location | Defect |
|--------|----------|--------|
| 4573921349 | `scripts/test-diagnose-dead-agent.sh:65` | A missing space: `export MUXCODE_AGENT_HEALTH_CHECK_SECS=8MUXCODE_RESUME_FIRST_SIGHTING=0 MUXCODE_AGENT_HEARTBEAT=86400`. `MUXCODE_AGENT_HEALTH_CHECK_SECS` is set to the string `8MUXCODE_RESUME_FIRST_SIGHTING=0` and `MUXCODE_RESUME_FIRST_SIGHTING` is never set, so the daemon's first-sighting restart stays on |
| 4573921377 | `bus/diagnose.go:1175–1183` `relaunchCommand` | Builds `muxcode resume <role>` from `report.Role`. For a hosted role (`docs`, `pr-read`) `ResumeAgent` refuses (`IsHostedRole`, `bus/resume.go`) and names the host instead — so `diagnose` prescribes a command that fails |

### What the script defect costs

The script's header says it runs "with first-sighting restarts off". With them on, the daemon may
relaunch the agent the script just killed before `diagnose` reads the pane. The 36/36 recorded in
MUX-006 was reached anyway, so the race was won on that run; the script does not control it. How the
daemon reads an unparseable `MUXCODE_AGENT_HEALTH_CHECK_SECS` is not established here.

## Requirements

### Acceptance criteria

- [ ] The script sets `MUXCODE_AGENT_HEALTH_CHECK_SECS=8` and `MUXCODE_RESUME_FIRST_SIGHTING=0` as two variables, and asserts the scratch daemon saw first-sighting restarts off (a check that would have failed on the fused line)
- [ ] For a hosted role, `diagnose`'s remediation names the host: `muxcode resume <host>` (`WindowForRole`), and running it as written does not refuse
- [ ] A non-hosted role's remediation is unchanged (negative control)
- [ ] The non-Claude branch (`muxcode reload <role>`) is checked for the same hosted-role defect and either fixed or recorded as correct
- [ ] `scripts/test-diagnose-dead-agent.sh` still passes, with its floor raised for the new checks

### Technical approach

Fix the `export` line. In `relaunchCommand`, resolve the role through `WindowForRole` before
formatting. Add a unit test over a hosted role beside `TestRunDiagnostics_DeadAgentVerdict`.

### Key files

| File | Purpose |
|------|---------|
| `scripts/test-diagnose-dead-agent.sh` | The fused `export` line (65); the new control assertion |
| `tools/muxcode/bus/diagnose.go` | `relaunchCommand` |
| `tools/muxcode/bus/resume.go`, `bus/config.go` | `ResumeAgent`'s hosted-role refusal; `WindowForRole`, `IsHostedRole` |

## Implementation

### Phase 1: The remediation names a command that runs

- [ ] `relaunchCommand` names the host window's role for a hosted role
- [ ] Unit test: a dead hosted role's remediation is `muxcode resume <host>`; a non-hosted role's is unchanged
- [ ] Check the `muxcode reload <role>` branch for hosted roles and record the result here

### Phase 2: Integration test

- [ ] Split the fused `export` line in `scripts/test-diagnose-dead-agent.sh`
- [ ] Add an assertion that fails when first-sighting restarts are on, and raise the coverage floor
- [ ] Run the script and record pass/fail counts in this spec

## Notes

- Filed 2026-11-14 by plan from edit's reading of PR #199's post-merge review threads, on the user's standing instruction to file defects found along the way.
- MUX-006's criterion 4 was amended after close-out to name `muxcode resume <role>`; the hosted-role case was not considered then.

## Status

**Backlog** — 0/11. Filed 2026-11-14.
