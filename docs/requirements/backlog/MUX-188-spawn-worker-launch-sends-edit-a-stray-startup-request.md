# MUX-188: Every Spawn Worker Launch Sends Edit a Stray Startup Request

**Tracking:** [mkober/muxcode#142](https://github.com/mkober/muxcode/issues/142)

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
| Launch writes the bootstrap for the role it was given | `RunAgentLaunch` → `RunAgentLaunchResume` → `PreLaunchSetup(role, session, binary, reason)` (`bus/launch.go:1091` on 2026-11-24) → startup `Message` from `role` to `role` | A `request:startup` from `edit` to `edit` lands in **edit's** inbox, not the worker's |
| Nothing consults `AGENT_ROLE` before the bootstrap | `AGENT_ROLE` is read only after it, at `launch.go:1113`, to export it | The bootstrap cannot know it is a worker |
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
- [MUX-135](../completed/MUX-135-spawn-seed-record-gc-strands-completion.md), [MUX-120](../completed/MUX-120-spawn-worker-never-woken-for-seeded-task.md) — the spawn seeding cluster.

## Decisions (user, 2026-11-24)

Relayed by edit. **Decision 1 (a): a spawn worker gets no startup bootstrap.** The `spawn-task` seed
is its bootstrap; nothing else should wake a worker before its task. The launch skips the startup
message when `AGENT_ROLE` is a `spawn-*` role, on the fresh and the resume road alike. The option of
addressing a bootstrap to the spawn role is dropped: the worker would hold two requests at start, read
in no fixed order.

Checked in the tree on 2026-11-24: `RunAgentLaunch` is a wrapper over `RunAgentLaunchResume`, which
calls `PreLaunchSetup(role, session, binary, reason)` at `bus/launch.go:1091` with the **base** role;
`AGENT_ROLE` is read only later (`:1113`), to export it. `IsSpawnRole` (`bus/config.go:1049`) is the
existing test for a `spawn-*` role.

## Requirements

### Acceptance criteria

- [ ] A spawn worker launch writes **no** `startup` request to the base role's inbox — test: launch a worker on a scratch session, assert edit's inbox and `log.jsonl` gain no `startup` row from/to `edit`
- [ ] A spawn worker receives no `startup` request of its own either: its inbox holds its `spawn-task` seed and nothing else at start; `docs/architecture.md` says so
- [ ] The launch's `agent launch` lifecycle row is still written for a worker, naming the spawn role
- [ ] **Positive control:** a plain `muxcode agent launch edit` (a real edit relaunch) still writes edit's startup request
- [ ] The paired second row is explained (Phase 1) and, if it is a second launch of the same worker, fixed or filed
- [ ] `bash scripts/test-spawn-startup-bootstrap.sh` passes

### Technical approach

In `RunAgentLaunchResume`, when `AGENT_ROLE` names a spawn role (`IsSpawnRole`), skip the startup
message: `PreLaunchSetup` does three things — retires pending startups for the role, sends the
startup, logs the launch row — and a worker launch needs only the third, attributed to the spawn role.
Retiring must be skipped too: run with the base role it would remove **edit's** own unread startup.

The same function next calls `ClearInFlightTasksForRole(session, role, …)` with the base role. Whether
a worker launch clears the base role's in-flight tasks through it is not established; Phase 1 checks
it and, if it does, files it rather than widening this spec.

### Key files

| File | Role |
|---|---|
| `tools/muxcode/bus/spawn.go` | the launch string (`AGENT_ROLE=<spawnRole> muxcode agent launch <role>`); the `spawn-task` seed |
| `tools/muxcode/bus/launch.go:867,1091` | `PreLaunchSetup`, and its call in `RunAgentLaunchResume` |
| `tools/muxcode/bus/config.go:1049` | `IsSpawnRole` |
| `tools/muxcode/bus/mode.go` | the other road that notes `PreLaunchSetup` wrote a startup message |
| `tools/muxcode/bus/graph_exec.go` (`replaceLostWorkers`) | candidate cause of the paired row |
| `scripts/test-spawn-startup-bootstrap.sh` | new |

## Implementation

### Phase 1: Establish the boundary

- [ ] Reproduce on a scratch session: one `spawn start` → count `startup` rows to `edit` in `log.jsonl` (1–2 expected before the fix); record the count here. The assertion that it is zero is written with Phase 2's fix, not before it
- [ ] Explain the pair: correlate the second row's `ts` with `spawn-launched`/`graph-spawn-replaced`/relaunch lifecycle rows; record the cause here
- [ ] Check whether a worker launch clears the base role's in-flight tasks (`ClearInFlightTasksForRole` with the base role); record what was found and file it if it does

### Phase 2: Fix

- [ ] `RunAgentLaunchResume` sends no startup message and retires none when `AGENT_ROLE` is a spawn role; the launch lifecycle row is still written, naming the spawn role
- [ ] Unit tests: a spawn-role launch leaves the base role's inbox unchanged, an unread base-role startup included; **control** — a base-role launch writes its startup as before, on the fresh and the resume road
- [ ] If the pair was a second launch, fix its cause or file it
- [ ] `docs/architecture.md` spawn section: a worker gets no startup bootstrap; the `spawn-task` seed is what starts it

### Phase 3: Integration test

- [ ] Create `scripts/test-spawn-startup-bootstrap.sh` — live scratch daemon on a private tmux server, a stub worker
- [ ] Test: one worker launch → zero `startup` rows to `edit` and none to the spawn role; worker inbox holds exactly one `spawn-task`
- [ ] **Positive control:** `muxcode agent launch edit` on the scratch session → one `startup` row to `edit`
- [ ] **Negative control:** a build from a copy of the tree with the skip removed writes the stray row, so the zero-rows assertion can fail
- [ ] Coverage floor; run and record counts here

## Out of scope

- Edit's response to the stray — already silent (MUX-182 Phase 5).
- Loop detection on the pair — `DetectMessageLoop` already drops self rows.

## Status

Backlog

Filed 2026-09-24 on the user's instruction relayed by edit, from seven observations on 2026-09-23/24
confirmed in `log.jsonl`. The addressing cause is verified in code (the spawn launch string →
`PreLaunchSetup` at `launch.go:1091`); the paired second row is observed and **unexplained**. Harmless
so far. Not started.

Unparked 2026-11-24 on the user's Decision 1 (a) — no bootstrap for a worker — relayed by edit and
recorded above.
