# MUX-243: A Halt Request Does Not Stop an Agent's Work, and a Reload Brings It Back

**Tracking:** [mkober/muxcode#256](https://github.com/mkober/muxcode/issues/256)

**Split from:** [MUX-157](../completed/MUX-157-role-boundary-an-agent-can-ignore.md) — its "a halt that halts"
criterion and Phase 3, moved here on the user's decision of 2026-11-24 relayed by edit.

There is no halt. An agent told to stop is sent an ordinary bus request whose action happens to be
named `halt-authoring`; whether it stops is the model's choice. On 2026-09-08 edit sent one to a
test agent that was authoring source, the agent kept writing, edit reloaded it, and the reloaded
agent wrote a fourth attempt. Two containment steps, neither of which is a mechanism.

## Context

### Observed (2026-09-08, session `muxcode`)

From [MUX-157](../completed/MUX-157-role-boundary-an-agent-can-ignore.md)'s timeline; the lifecycle rows were
verified there, the message-level account is edit's.

| When | What |
|------|------|
| 18:52:53–56 | `halt-authoring` request from edit to test. The daemon marks the task *succeeded* 3 s after `force-respond-notify test: rung 0 fired` — a status-line echo, not a reply ([MUX-154](../completed/MUX-154-codex-status-line-closes-tracked-tasks.md)) |
| 18:55:23 | A write to `bus/graph_exec_test.go` follows the halt |
| 19:04:00 | `agent-reload test: codex→codex` — the same-provider reload |
| 19:05 | Attempt 4 is written after the halt and the reload |

### Mechanism

| Fact | How established |
|------|-----------------|
| No `halt` action, command or task state exists in the bus or CLI | Read by plan 2026-11-24: `grep '"halt'` over `tools/muxcode/bus` and `cmd` (non-test) finds nothing |
| A halt is therefore a request like any other — delivered, and obeyed or not | Follows from the above |
| What a reload preserves has changed since the incident: [MUX-196](../completed/MUX-196-agent-launch-expires-in-flight-graph-dispatch.md) made a launch **keep** an unread dispatch (`inflight-task-kept`), clear one the agent read and never answered (`inflight-task-cleared`) and **re-send** it when a graph run owns it (`graph-dispatch-resent`) | `CLAUDE.md`, `test-reload-keeps-dispatch`; not re-read in code for this spec |
| So today a reload, by design, hands unread work back to the relaunched agent — the opposite of what a halt wants | Inferred from the row above; Phase 1 confirms it |
| A Claude agent relaunched by the daemon is resumed into its conversation (`--resume`), which carries the work in the model's own context whatever the bus holds | `CLAUDE.md` (MUX-139); the 2026-09-08 agent was Codex, where this was not the road |

### Undecided — the design is not recorded

Nobody has chosen what a halt **is**. MUX-157 proposed "cancel the role's in-flight tasks and mark
the carrying request `halted`", as a starting point, not a decision. Open:

| # | Question | Options |
|---|----------|---------|
| 1 | The surface | a `muxcode halt <role>` command · a reserved bus action the bus itself acts on · a flag on `reload` |
| 2 | What it stops | the role's in-flight tasks only · those plus its pending inbox · those plus the running turn (an interrupt key, or killing the process) |
| 3 | A graph-owned dispatch | a halt fails the node · parks the run · is refused while a run owns the role (as `graph cancel` is the way to stop a run's work) |
| 4 | Who may halt | the user only · edit · any agent |
| 5 | A resumed Claude conversation | accepted as out of reach · a halted role is relaunched fresh, not resumed |

Until these are answered this spec is not ready to be worked unattended.

## Requirements

### Acceptance criteria

- [ ] What survives `muxcode reload <role>`, a daemon health restart and `muxcode resume` is
      recorded here from a measurement, per provider: in-flight tasks, inbox rows, graph re-sends,
      the provider's own session
- [ ] The five questions above are answered by the user and recorded in a Decisions section
- [ ] A halt stops the role's in-flight work as decided, and a subsequent reload does not resurrect
      it
- [ ] A halt is attributable: a lifecycle row naming the actor, the role and what was stopped
- [ ] Negative control: a reload with no halt keeps and re-sends work exactly as MUX-196 left it
- [ ] Docs: `docs/agent-bus.md` and `CLAUDE.md` describe the halt and how it differs from
      `graph cancel`, `spawn stop` and `agent-health --stop`

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/reload.go` | What a reload preserves |
| `tools/muxcode/bus/task.go` | In-flight tasks; `ClearInFlightTasksForRole` |
| `tools/muxcode/bus/graph_exec.go` | The re-send of a cleared graph dispatch |
| `tools/muxcode/bus/agent_health.go` | The existing stop marker (`agent-health --stop`) |
| `tools/muxcode/cmd/` | The command surface, per Decision 1 |

## Implementation

### Phase 1: Measure and decide

- [ ] Measure what survives each relaunch road for Claude, hook-road Codex and OpenCode, against a
      scratch daemon; record the table here
- [ ] Put the five questions to the user with that table; record the answers as
      `## Decisions (user, <date>)` and rewrite Phase 2 to them

### Phase 2: The halt

- [ ] Implement the halt as decided: the surface, what it stops, the graph case, the authority check
- [ ] The halted state survives a reload: the stopped work is neither kept nor re-sent
- [ ] Lifecycle row naming actor, role and the tasks stopped
- [ ] Negative control: a reload with no halt behaves as before (`test-reload-keeps-dispatch` and
      `test-reload-defers-dispatch` still pass unchanged)

### Phase 3: Docs

- [ ] `docs/agent-bus.md`: the command or action, and the table of what each stop mechanism stops
- [ ] `CLAUDE.md`: one clause under "Keep agents deliverable"

### Phase 4: Integration test

- [ ] Create `scripts/test-halt.sh` (hermetic: muxcode and the claude-stub built from the tree, a
      private tmux server, a scratch daemon): a stub agent holding an unread request and one it read
      and never answered is halted → neither is delivered or re-sent afterwards
- [ ] Halt → `muxcode reload <role>` → the relaunched agent receives none of the halted work
- [ ] Negative control: the same reload with no halt delivers the unread request (MUX-196's keep)
- [ ] Control: a muxcode built from a scratch copy with the halted check removed lets the reload
      resurrect the work, which the second test rejects
- [ ] Coverage floor so a skipped section cannot report green
- [ ] Run the script and record pass/fail counts in this spec

## Status

**Backlog — parked at filing 2026-11-24** by plan, under the rule that a spec holding an unrecorded
design decision is not worked unattended. Filed as a split from MUX-157. Not started. Carries five design questions
nobody has answered (see "Undecided"); Phase 1 ends with the user's decisions.
