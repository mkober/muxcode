# MUX-188: Every Spawn Worker Launch Sends Edit a Stray Startup Request

Each time a graph spawns a worker, the **edit** agent receives a `request:startup` — *"Session
started — review last saved context from memory to restore session state."* — from itself, one to
three seconds after the worker's window opens. Observed seven times on 2026-09-23/24 and matched in
the bus log; edit answers each one (its self-reply is correlated and never delivered, per MUX-182
Phase 5), so the cost so far is a wasted turn per launch and a misleading row in edit's history.

## Context

### Source and standard of evidence

Reported by edit from its own inbox; timestamps confirmed by plan in
`/tmp/muxcode-bus-muxcode/log.jsonl` (`"action":"startup"`, `from:edit`, `to:edit`): `ts`
`1790262552` = 11:09:12 and `1790263489` = 11:24:49 on 2026-09-24 match the report's list and the
`implement`/`fix` worker launches of run `1790262549`. The log shows them in **pairs** — a second row
13–17 s after the first (`1790262569`, `1790263502`) — which the report did not mention and whose
cause is not yet known. Filed on the user's instruction relayed by edit.

### Mechanism — verified in code

| Step | Code | Behaviour |
|---|---|---|
| The worker window runs the **base** role's launch | `bus/spawn.go:215,217`: `AGENT_ROLE=<spawnRole> muxcode agent launch <role>` where `role` is the parent role (`edit` for implement/fix workers) and `spawnRole` is `spawn-xxxxxxxx` | `RunAgentLaunch` receives `edit` |
| Launch writes the bootstrap for the role it was given | `bus/launch.go:34` (`RunAgentLaunch`) → `PreLaunchSetup(role, session, binary)` → `launch.go:849` startup `Message` from `role` to `role` | A `request:startup` from `edit` to `edit` lands in **edit's** inbox, not the worker's |
| Nothing consults `AGENT_ROLE` on this path | no `SPAWN`/`AGENT_ROLE` reference in `bus/launch.go` | The bootstrap cannot know it is a worker |
| The worker is seeded separately | `bus/spawn.go:400`: `spawn-task` request from the owner to `entry.SpawnRole` | The worker gets its work; it never gets a startup bootstrap of its own |
| The daemon delivers the stray | startup is request-type precisely so `checkIdleAgents` re-notifies (`launch.go:843-846`) | Edit is woken for a message that was meant for a window that is not its own |

So the hypothesis in the report — *addressed to the base role instead of the spawn role* — is
**confirmed in code**. The pairing is not: two rows per launch may be two launches (a replaced
worker, `replaceLostWorkers`), a relaunch by the daemon, or a second `PreLaunchSetup` road
(`mode.go:357` notes one), and Phase 1 must say which.

### Blast radius

- One wasted edit turn per worker launch, during which edit is not idle for real deliveries.
- Edit's history gains a `startup` row per launch that reads as a session restart; `muxcode
  diagnose` and anyone reading the log can mistake it for edit having relaunched.
- A worker starts with no startup bootstrap — harmless today because `spawn-task` seeds it, but any
  future logic keyed on "has this role received its startup" will read the worker as never started.
- Harmless in effect so far; a defect in attribution, and a supply of unrequested requests.

### Family

- [MUX-182](../completed/MUX-182-cancelled-run-keeps-working-provenance-unreadable.md) Phase 5 — made the self-reply silent; this is the request that provokes it.
- [MUX-135](./MUX-135-spawn-seed-record-gc-strands-completion.md), [MUX-120](./MUX-120-spawn-worker-never-woken-for-seeded-task.md) — the spawn seeding cluster.

## Requirements

### Acceptance criteria

- [ ] A spawn worker launch writes **no** `startup` request to the base role's inbox — test: launch a worker on a scratch session, assert edit's inbox and `log.jsonl` gain no `startup` row from/to `edit`
- [ ] The worker's own bootstrap is either addressed to its `spawn-xxxxxxxx` role or deliberately omitted (it is seeded by `spawn-task`); the choice is recorded here and in `docs/architecture.md`
- [ ] **Positive control:** a plain `muxcode agent launch edit` (a real edit relaunch) still writes edit's startup request
- [ ] The paired second row is explained (Phase 1) and, if it is a second launch of the same worker, fixed or filed
- [ ] `bash scripts/test-spawn-startup-bootstrap.sh` passes

### Technical approach

`RunAgentLaunch` should derive the **bus** role from `AGENT_ROLE` when set (it already is the
worker's window and inbox name), and pass that to `PreLaunchSetup` — or `StartSpawn` should pass
`spawnRole` as the launch argument and let the launcher resolve the definition from the base role.
The first is smaller. Whether a worker *should* receive a startup bootstrap at all is Decision 1;
the seed already carries the task, and a second request would race it.

### Key files

| File | Role |
|---|---|
| `tools/muxcode/bus/spawn.go:213-220,400` | the launch string; the `spawn-task` seed |
| `tools/muxcode/bus/launch.go:10-40,848-` | `RunAgentLaunch`, `PreLaunchSetup` |
| `tools/muxcode/bus/mode.go:357` | the other road that notes `PreLaunchSetup` wrote a startup message |
| `tools/muxcode/bus/graph_exec.go` (`replaceLostWorkers`) | candidate cause of the paired row |
| `scripts/test-spawn-startup-bootstrap.sh` | new |

## Implementation

### Phase 1: Establish the boundary

- [ ] Reproduce on a scratch session: one `spawn start` → count `startup` rows to `edit` in `log.jsonl` (expect 1–2 today); a failing test kept as the negative control
- [ ] Explain the pair: correlate the second row's `ts` with `spawn-launched`/`graph-spawn-replaced`/relaunch lifecycle rows; record the cause here
- [ ] Decide whether a worker gets its own bootstrap ([Decision 1](#decision-1--does-a-worker-get-a-bootstrap))

### Phase 2: Fix

- [ ] `RunAgentLaunch` resolves the bus role from `AGENT_ROLE` for the bootstrap (definition still from the base role); unit test for both roles
- [ ] If the pair was a second launch, fix its cause or file it
- [ ] `docs/architecture.md` spawn section states which role a worker's bootstrap is addressed to

### Phase 3: Integration test

- [ ] Create `scripts/test-spawn-startup-bootstrap.sh` — live scratch daemon, a stub worker
- [ ] Test: one worker launch → zero `startup` rows to `edit`; worker inbox holds exactly one `spawn-task`
- [ ] **Positive control:** `muxcode agent launch edit` on the scratch session → one `startup` row to `edit`
- [ ] Coverage floor; run and record counts here

## Open decisions

### Decision 1 — does a worker get a bootstrap?

Omit it (the seed is the bootstrap; nothing else should wake the worker before its task) or address
it to the spawn role (uniform launch semantics; the worker then has two requests at start and the
order they are read is not fixed). The evidence favours omitting.

## Out of scope

- Edit's response to the stray — already silent (MUX-182 Phase 5).
- Loop detection on the pair — `DetectMessageLoop` already drops self rows.

## Status

Backlog

Filed 2026-09-24 on the user's instruction relayed by edit, from seven observations on 2026-09-23/24
confirmed in `log.jsonl`. The addressing cause is verified in code (`spawn.go:215` → `launch.go:34`);
the paired second row is observed and **unexplained**. Harmless so far. Not started.
