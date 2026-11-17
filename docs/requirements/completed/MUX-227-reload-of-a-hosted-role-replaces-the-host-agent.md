# MUX-227: `muxcode reload <hosted-role>` Replaces the Host Agent Instead of Refusing

**Tracking:** [mkober/muxcode#213](https://github.com/mkober/muxcode/issues/213)

A hosted role has no window of its own — `docs` runs inside plan's pane, `pr-read` inside commit's.
`muxcode reload docs` resolves that to plan's window, stops the planner and relaunches the pane as
the docs agent. The planner and its conversation are gone. `muxcode resume` refuses a hosted role
and names the host; the batch reload skips one; the single-role reload does neither.

## Context

### Source and standard of evidence

Found by the worker in [MUX-221](../completed/MUX-221-diagnose-dead-agent-test-uncontrolled-and-hosted-role-remediation.md)
Phase 1, while checking `diagnose`'s `muxcode reload <role>` remediation for hosted roles. Verified
by edit against main `e4a0936`; plan re-read the guards in `bus/reload.go`. Not observed in a
session — nobody is known to have run the command — so what the relaunched pane looks like is read
from code, not seen.

| Road | Hosted role | Code |
|------|-------------|------|
| `muxcode resume <role>` | Refused, naming the host: "use `muxcode resume <host>`" | `bus/resume.go:43–46`, `IsHostedRole` |
| Batch reload (`reload --all`, `--provider`) | Skipped | `bus/reload_batch.go:144`, `IsHostedRole` |
| `muxcode reload <role>` | **Proceeds** | `bus/reload.go:427–439` `ReloadAgent` checks `IsKnownRole` and `RoleWindowMissing` only |
| The daemon's reserved reload | **Proceeds** | `bus/reload.go:455–465` `ReloadAgentReserved`, the same two checks |

`RoleWindowMissing` resolves a hosted role to its host's window, so the check passes and
`reloadHolding` stops that pane and relaunches it with the hosted role's definition and
`AGENT_ROLE`.

### Related, not in scope

`AgentState.Provider` follows the hosted role's own `MUXCODE_<ROLE>_CLI`, not the host's
(`CollectAgentState`). MUX-221 corrects that only inside `relaunchCommand`. Whether the daemon's
agent-defs watchdog can reach `ReloadAgentReserved` with a hosted role is not established here;
Phase 1 says.

## Requirements

### Acceptance criteria

- [x] `muxcode reload <hosted-role>` is refused before anything is stopped, with a message naming the host's command, as `resume` does
- [x] `ReloadAgentReserved` refuses a hosted role the same way and releases its marker
- [x] The host agent's pane and process are untouched after a refused reload
- [x] Negative control: a non-hosted role still reloads; `muxcode reload <host>` still reloads the host
- [x] The refusal is one shared check, used by both reload roads, so they cannot drift from `resume` again
- [x] `docs/agent-bus.md` `muxcode reload` says a hosted role is refused and names what to run

### Technical approach

Add the `IsHostedRole` check to `ReloadAgent` and `ReloadAgentReserved` ahead of
`RoleWindowMissing`, through one helper that builds the message from `WindowForRole`. Check
`cmd/reload.go` and the selector modal (`prefix + R`) for a path that offers a hosted role.

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/reload.go` | `ReloadAgent`, `ReloadAgentReserved`, `reloadHolding` |
| `tools/muxcode/bus/resume.go` | The refusal to mirror |
| `tools/muxcode/bus/reload_batch.go` | The batch skip, already correct |
| `tools/muxcode/bus/config.go` | `IsHostedRole`, `WindowForRole` |
| `tools/muxcode/cmd/reload.go`, `tui/provider_select.go` | Callers that might offer a hosted role |

## Implementation

### Phase 1: Refuse

- [x] Record which callers can reach either reload road with a hosted role (CLI, selector modal, agent-defs watchdog)
- [x] One shared hosted-role check in `ReloadAgent` and `ReloadAgentReserved`, before any stop; the reserved road releases its marker
- [x] Unit tests: a hosted role is refused naming its host on both roads and nothing is stopped; a non-hosted role and the host itself still reload (negative controls)

### Results for Phase 1

Recorded 2026-11-17 from the worker's notes (`/tmp/mux227-phase1.md`); plan read the diff of
`bus/reload.go` and `bus/resume.go`. Boxes are ticked at `verify-spec`, not here.

**Which callers could reach a reload road with a hosted role** — the worker's audit, before the fix:

| Caller | Road | Reaches a hosted role? |
|--------|------|------------------------|
| `muxcode reload <role>` | `cmd/reload.go` → `ReloadAgent` | **Yes** — the defect as filed |
| `muxcode reload <r1> <r2> …` | `cmd/reload.go` → `ReloadBatch(roles)` → `ReloadAgent` per role | **Yes** — a batch of named roles reloads what it is given. Not named in this spec at filing |
| `muxcode config set <role> cli\|model … --reload` | `cmd/config.go` → `ReloadAgent` | **Yes**. Not named in this spec at filing |
| The daemon's stuck-provider watchdog | `daemon.go` → `ReloadAgent` | No — it walks `KnownRoles` and skips a role whose window is another's |
| The daemon's agent-defs and sandbox watchdog | `checkAgentDefs` → `ReserveReload` + `ReloadAgentReserved` | No — `autoReloadRoles` skips such a role, and edit and auto |
| The daemon's definition watchdog | `definition_watchdog.go` → `ReloadAgentReserved` | No — the same skip |
| `reload --all`, `--provider`, and the selector modal (`prefix + R`) | `ReloadableRoles` | No — it excludes hosted roles |

So `ReloadAgentReserved` had no road to a hosted role, which settles what "Related, not in scope"
left open; it refuses one now all the same, as the second criterion asks.

**The fix.** One helper, `hostedRoleRefusal(role, command)` (`bus/config.go`), returning
"`<role>` runs inside `<host>`'s pane — use `muxcode <command> <host>`".

| Road | Where the check sits |
|------|----------------------|
| `ReloadAgent` | Right after `IsKnownRole`, before `RoleWindowMissing` and any stop |
| `ReloadAgentReserved` | The same place; on refusal it releases its marker first |
| `ResumeAgent` | Now calls the same helper in place of its own check; its message is unchanged |

All three CLI callers above go through `ReloadAgent`, so all three are covered; for a batch of named
roles the hosted one is refused as one error and the rest proceed.

**Tests** (`bus/reload_hosted_test.go`): `TestReloadAgent_RefusesHostedRole` — `docs` → plan and
`pr-read` → commit, on both roads, with zero tmux calls and no marker left on the role or its host;
`TestResumeAgent_HostedRefusalUnchanged`; negative control
`TestReloadAgent_HostAndOrdinaryRolesPassTheHostedCheck` — plan, commit and build pass the hosted
check and stop only at the stub's missing window.

Also run by the worker: `scripts/test-bus-dir-leak.sh` 22 passed, 0 failed (run task
`1794947828-spawn-26088880-822bdee8`), full suite green.

Verified and ticked 2026-11-17 once all three had run after the last edit (15:36:26): `./build.sh`
exit 0 at 15:41:08, `./test.sh` exit 0 at 15:42:58, review at 15:43:43 with 0 must-fix, 0 should-fix
and **1 nit, left open**: `TestResumeAgent_HostedRefusalUnchanged` duplicates an existing
resume-refusal test at `resume_test.go:161` and could fold into it. The run counted the review as
passed. No criterion is ticked yet — the first four are about the command and the pane, which
Phase 2's script drives.

### Phase 2: Integration test

- [x] Create `scripts/test-reload-hosted-role.sh` on a private tmux server with a stub agent: `muxcode reload docs` exits non-zero naming plan, and plan's pane still runs the same process
- [x] Control: `muxcode reload plan` relaunches plan; a pre-fix build (the guard removed from a copy of the tree) replaces the host on `reload docs`
- [x] Run the script and record pass/fail counts in this spec
- [x] Update `docs/agent-bus.md` and the script index in `CLAUDE.md`

### Results for Phase 2

Recorded 2026-11-17 from the worker's request; plan read the script's header and the run's log.
Boxes are ticked at `verify-spec`, not here.

`scripts/test-reload-hosted-role.sh` — hermetic: a private tmux server (`TMUX_TMPDIR` in the work
dir, `TMUX` unset, killed on exit), muxcode and `scripts/fixtures/claude-stub` built from the tree,
agent definitions in a scratch install dir. The stub stands in for plan's Claude agent, with a real
inbox listener. Its header says never to run it against a live session, since its control replaces
the host it reloads. About a minute.

**Run: 13 passed, 0 failed, floor 13, exit 0** (run task `1794948320-spawn-26088880-e91d5ea4`,
requested 15:45:20 after the script's last edit at 15:45:13; the run agent's history row confirms
it, and plan read the log — 13 `ok`, no `FAIL`).

| Section | Shows |
|---------|-------|
| A | `muxcode reload docs` exits 1 naming `muxcode reload plan`. Five seconds later the planner is **the same process**; no docs launch was logged; no reload marker is left on docs or plan |
| B | Control: `muxcode reload plan` exits 0 and relaunches the planner — a new process, and a reload launch row for plan |
| C | Control: a build with the guard removed from `ReloadAgent` replaces the planner on `reload docs` — the host's process is gone and a docs agent is launched into plan's pane. The defect, seen for the first time: at filing it had only been read from code |

**What it does not drive.** `pr-read` on commit; the multi-role and `config set … --reload` callers;
and `ReloadAgentReserved`, which no caller reaches with a hosted role. Each is covered by the unit
test `TestReloadAgent_RefusesHostedRole` or by the single check they all pass through.

Docs: `docs/agent-bus.md` — a hosted-role paragraph and table under `muxcode reload` (plan);
`CLAUDE.md` — the script index (edit).

**Review should-fix, on plan's docs.** That paragraph first said `research` runs inside plan's pane
and is refused. It was wrong and is corrected: only `docs` and `pr-read` are hosted. The same wrong
grouping stood in an older line under `muxcode deliver` ("`docs`/`research`/`pr-read` → host
window"), also corrected. A third, under `muxcode compact --all` ("hosted roles like
`docs`/`research`/`pr-read`"), was left: plan did not check why `research` is excluded there.

**Phase 1's nit is closed.** `TestResumeAgent_HostedRefusalUnchanged` was folded into the existing
resume-refusal test in `bus/resume_test.go`; the tests in `bus/reload_hosted_test.go` are now
`TestReloadAgent_RefusesHostedRole` and its negative control.

**Verified and ticked 2026-11-17**, every piece of evidence after the last edits (code at 15:48:09,
plan's docs correction at 15:48:44):

| Evidence | When | Result |
|----------|------|--------|
| `bash scripts/test-reload-hosted-role.sh` | requested 15:45:20 | 13 passed, 0 failed, floor 13, exit 0 |
| `./build.sh` | 15:53:19 | exit 0 |
| `./test.sh` (hook row) | 15:55:03 | exit 0, `modules=2 ran=2 passed=2 failed=0 never-executed=0` |
| Review | 15:55:28 | 0 must-fix, 0 should-fix, 0 nits |

The script run predates the test fold at 15:48; that change was to unit tests only, and `./test.sh`
ran after it.

What each criterion rests on:

| # | Criterion | Evidence |
|---|-----------|----------|
| 1 | `reload <hosted-role>` refused before anything is stopped, naming the host's command | Section A; `TestReloadAgent_RefusesHostedRole` (zero tmux calls) |
| 2 | `ReloadAgentReserved` refuses the same way and releases its marker | `TestReloadAgent_RefusesHostedRole`, both roads, no marker left. Unit test only — no caller reaches that road with a hosted role |
| 3 | The host's pane and process are untouched | Section A — the same process five seconds later, no docs launch, no marker |
| 4 | A non-hosted role, and the host itself, still reload | Section B (`reload plan`); the unit negative control (plan, commit, build pass the check) |
| 5 | One shared check for both reload roads and `resume` | `hostedRoleRefusal`, read in the diff |
| 6 | `docs/agent-bus.md` says a hosted role is refused and names what to run | The paragraph and table under `muxcode reload`, as corrected |


## Notes

- Filed 2026-11-14 by plan from `/tmp/defect-reload-hosted.md` (edit), on the user's standing instruction to file defects found along the way.

### Findings at activation (2026-11-17)

Checked against the tree at `cb6179e`. The defect stands, and the line numbers still hold.

| Spec says | Tree today |
|-----------|------------|
| `muxcode resume` refuses a hosted role — `bus/resume.go:43–46` | `IsHostedRole` at `:43` |
| The batch reload skips one — `bus/reload_batch.go:144` | `IsHostedRole` at `:144` |
| `ReloadAgent` (`bus/reload.go:427`) and `ReloadAgentReserved` (`:455`) check `IsKnownRole` and `RoleWindowMissing` only | Both at those lines; `IsHostedRole` appears nowhere in `bus/reload.go` or `cmd/reload.go` |
| `scripts/test-reload-hosted-role.sh` | Does not exist |

Nothing in this spec waits on a user decision. Its Phase 2 step "update `docs/agent-bus.md`" is
plan's; the script index in `CLAUDE.md` is edit's or plan's.

One caution for the worker and the run agent, since this session is where the work happens: plan
hosts `docs` here, and commit hosts `pr-read`. The defect is that `muxcode reload docs`
replaces the planner. It is to be exercised on the script's private tmux server only — never against
this session.

(Corrected 2026-11-17 after the Phase 2 review: plan first wrote here, and in
`docs/agent-bus.md`, that `research` is hosted in plan too. It is not — `hostedRoles` in
`bus/config.go` holds `docs` and `pr-read` only; `research` is a mode role with its own window, and
this change does not refuse `muxcode reload research`. The guard was deliberately not widened.)

## Status

**Complete** — activated and closed 2026-11-17 by `55-defect-backlog-loop` run `1794626267-55-defect-backlog-loop-15547e2e` (Defects rank 21 then; ranks 1–20 were parked — MUX-008 at rank 19 was parked at that selection). Uncommitted at close-out; the run's ship step commits it. 13/13 — Phases 1–2 complete and all six criteria met. Filed 2026-11-14.

`muxcode reload` of a hosted role — `docs` in plan's pane, `pr-read` in commit's — is refused with
exit 1 before anything is stopped, naming the host's command, as `resume` already did. One check,
`hostedRoleRefusal`, serves both reload roads and `resume`. It covers the three CLI callers that
could reach it: the single-role reload, a reload of several named roles, and
`config set … --reload`. The integration script shows the planner is the same process after a
refused reload, and a control build without the guard shows the defect itself: the planner replaced
by a docs agent in its own pane.

For a later reader: `research` is not hosted and is not refused — plan wrote otherwise at first, and
a review corrected it. The daemon's reserved road refuses too, on a unit test only, since no caller
reaches it with a hosted role.
