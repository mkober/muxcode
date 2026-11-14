# MUX-221: The Dead-Agent Diagnose Test Is Uncontrolled, and a Hosted Role's Remediation Names a Command That Refuses

**Tracking:** [mkober/muxcode#204](https://github.com/mkober/muxcode/issues/204)

A follow-up to [MUX-006](./MUX-006-diagnose-false-clean-verdict.md) (PR #199), from two
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

- [x] The script sets `MUXCODE_AGENT_HEALTH_CHECK_SECS=8` and `MUXCODE_RESUME_FIRST_SIGHTING=0` as two variables, and asserts the scratch daemon saw first-sighting restarts off (a check that would have failed on the fused line) — and does fail on it, in the script's own control
- [x] For a hosted role, `diagnose`'s remediation names the host: `muxcode resume <host>` (`WindowForRole`), and running it as written does not refuse — named by unit test; "does not refuse" read from `ResumeAgent`, not run
- [x] A non-hosted role's remediation is unchanged (negative control)
- [x] The non-Claude branch (`muxcode reload <role>`) is checked for the same hosted-role defect and either fixed or recorded as correct — fixed in the remediation; the reload command's own defect is MUX-227
- [x] `scripts/test-diagnose-dead-agent.sh` still passes, with its floor raised for the new checks — 38 of 38

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

- [x] `relaunchCommand` names the host window's role for a hosted role — and branches on the host's provider, not the hosted role's
- [x] Unit test: a dead hosted role's remediation is `muxcode resume <host>`; a non-hosted role's is unchanged — `TestCheckAgentDown_HostedRoleNamesTheHost`
- [x] Check the `muxcode reload <role>` branch for hosted roles and record the result here — below

### Results for Phase 1 (2026-11-14)

Verified by plan against the tree. Last source edit 1794686506 (`diagnose.go` at 1794686483, its test file after), then the build (1794686553), `./test.sh` (2 of 2 modules, 1794686619) and the review (0 must-fix, 0 should-fix, 0 nits, `/tmp/muxcode-review-1794686619.txt`).

| Item | State |
|------|-------|
| The change | `relaunchCommand` (`bus/diagnose.go`): for an `IsHostedRole` role it takes `WindowForRole(role)` as the role to name and `ResolveProvider(host)` as the provider to branch on. A non-hosted role takes the old path unchanged |
| Why the provider too | `AgentState.Provider` follows the hosted role's own `MUXCODE_<ROLE>_CLI`, not the host's, so the old code could pick the wrong branch as well as the wrong name. Corrected inside `relaunchCommand` only; `CollectAgentState` is left alone |
| Test cases | `docs` with an OpenCode decoy on a Claude plan → `muxcode resume plan`; `pr-read` with a Claude decoy on a Codex commit → `muxcode reload commit`; a live `docs` → `muxcode resume plan --force`; controls: Claude and Codex `build` unchanged |
| **The reload branch** | Checked. The remediation text had the same defect — `muxcode reload docs` for a non-Claude hosted role — and is fixed by the same host resolution. But the command it used to name does not refuse: `muxcode reload docs` resolves to plan's window (`RoleWindowMissing`), stops that pane and relaunches it as the docs agent, replacing the planner. That is a defect in reload itself, outside this spec — [MUX-227](../backlog/MUX-227-reload-of-a-hosted-role-replaces-the-host-agent.md) |

"Running it as written does not refuse" is ticked on a reading of the code, not a run: `ResumeAgent` refuses only an `IsHostedRole` role, and the command now names the host, which is not one. No hosted-role remediation was executed.

### Phase 2: Integration test

- [x] Split the fused `export` line in `scripts/test-diagnose-dead-agent.sh`
- [x] Add an assertion that fails when first-sighting restarts are on, and raise the coverage floor — it reads the scratch daemon's own environment; floor 36 → 38
- [x] Run the script and record pass/fail counts in this spec — 38 passed, 0 failed, floor 38 (proc `1794686743-proc-1c13a999`)

### Results for the integration phase (2026-11-14)

Verified by plan against the tree and the proc log. The script's last edit (1794686735) precedes its run (1794686743, exit 0); `CLAUDE.md`'s script entry followed (1794686775), then the build (1794686809), `./test.sh` (2 of 2 modules, 1794686824) and the review (0 must-fix, 0 should-fix, 0 nits, `/tmp/muxcode-review-1794686824.txt`).

| Check | Result |
|-------|--------|
| The `export` line | Now `MUXCODE_AGENT_HEALTH_CHECK_SECS=8 MUXCODE_RESUME_FIRST_SIGHTING=0 MUXCODE_AGENT_HEARTBEAT=86400` — three variables |
| New: the daemon's real environment | Read from the running scratch daemon (`ps eww`, piped through `muxcode pii-scrub`); both variables must be present as separate entries |
| New: negative control | A decoy process started with the old fused line fails the same check |
| Sections A–E (MUX-006) | All pass; "C: diagnosed while still dead" held — the race the fused line left open did not need winning |

The script does not exercise the hosted-role remediation; that stays unit-verified, as Results for Phase 1 records.

## Notes

- Filed 2026-11-14 by plan from edit's reading of PR #199's post-merge review threads, on the user's standing instruction to file defects found along the way.
- MUX-006's criterion 4 was amended after close-out to name `muxcode resume <role>`; the hosted-role case was not considered then.
- 2026-11-14, found by this spec's Phase 1 worker while checking the `muxcode reload <role>` branch: `ReloadAgent` has no hosted-role guard, so `muxcode reload docs` would stop plan's pane and relaunch it as the docs agent. That is a defect in reload, not in `diagnose`'s remediation text; filed as [MUX-227](../backlog/MUX-227-reload-of-a-hosted-role-replaces-the-host-agent.md) on the user's standing instruction, relayed by edit.

## Status

Complete — closed 2026-11-14 by `55-defect-backlog-loop` run `1794626267-55-defect-backlog-loop-15547e2e`, 11/11 (filed and activated the same day, from Defects rank 16; ranks 1–15 were parked).
