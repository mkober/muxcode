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

- [ ] `muxcode reload <hosted-role>` is refused before anything is stopped, with a message naming the host's command, as `resume` does
- [ ] `ReloadAgentReserved` refuses a hosted role the same way and releases its marker
- [ ] The host agent's pane and process are untouched after a refused reload
- [ ] Negative control: a non-hosted role still reloads; `muxcode reload <host>` still reloads the host
- [ ] The refusal is one shared check, used by both reload roads, so they cannot drift from `resume` again
- [ ] `docs/agent-bus.md` `muxcode reload` says a hosted role is refused and names what to run

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

- [ ] Record which callers can reach either reload road with a hosted role (CLI, selector modal, agent-defs watchdog)
- [ ] One shared hosted-role check in `ReloadAgent` and `ReloadAgentReserved`, before any stop; the reserved road releases its marker
- [ ] Unit tests: a hosted role is refused naming its host on both roads and nothing is stopped; a non-hosted role and the host itself still reload (negative controls)

### Phase 2: Integration test

- [ ] Create `scripts/test-reload-hosted-role.sh` on a private tmux server with a stub agent: `muxcode reload docs` exits non-zero naming plan, and plan's pane still runs the same process
- [ ] Control: `muxcode reload plan` relaunches plan; a pre-fix build (the guard removed from a copy of the tree) replaces the host on `reload docs`
- [ ] Run the script and record pass/fail counts in this spec
- [ ] Update `docs/agent-bus.md` and the script index in `CLAUDE.md`

## Notes

- Filed 2026-11-14 by plan from `/tmp/defect-reload-hosted.md` (edit), on the user's standing instruction to file defects found along the way.

## Status

**Backlog** — 0/13. Filed 2026-11-14.
