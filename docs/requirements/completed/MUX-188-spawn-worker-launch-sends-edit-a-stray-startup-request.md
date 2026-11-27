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

## Decisions (user, 2026-11-27)

Relayed by edit: **"widen MUX-188 and finish it in this PR."** Two things follow.

| Question | Decision |
|----------|----------|
| Scope | **Widened to every role-keyed launch step.** One cause, one guard: a worker runs `RunAgentLaunchResume` with the **base** role, so each step keyed on that role acted on the live base agent. When `AGENT_ROLE` is a spawn role the launch skips all of them and writes only the launch lifecycle row. The 2026-11-24 instruction to file a task-clearing finding "rather than widening this spec" is superseded |
| Where it ships | In the [MUX-190](./MUX-190-codex-agent-drops-a-consumed-request-after-a-guard-denial.md) PR (`55-defect-backlog-loop` run `1795811929`), not on a branch of its own. Edit implements; the loop's active spec stays MUX-190 |

The steps a worker launch now skips:

| Step | What it did to the live base agent | In the spec before? |
|------|------------------------------------|---------------------|
| The `startup` request | Sent edit a stray `request:startup` | Yes |
| `retirePendingStartups` | Removed edit's own unread startup | Yes |
| `ClearInFlightTasksForRole` | Cleared in-flight tasks addressed to edit that had left its inbox — a request the live edit agent had read and was working on | Phase 1 was to check it; confirmed, and widened in |
| `StampAgentDefHash` / `StampLaunchSandbox` | Overwrote edit's watchdog stamps with the installed state, which can hide a reload the real edit agent still needs | New |

## Requirements

### Acceptance criteria

- [x] A spawn worker launch writes **no** `startup` request to the base role's inbox — test: launch a worker on a scratch session, assert edit's inbox and `log.jsonl` gain no `startup` row from/to `edit` — script section A
- [x] A spawn worker receives no `startup` request of its own either: its inbox holds its `spawn-task` seed and nothing else at start; `docs/architecture.md` says so — script section A (the worker's inbox holds exactly one message, its seed)
- [x] The launch's `agent launch` lifecycle row is still written for a worker, naming the spawn role — script section A: `role=edit … reason=spawn as=<spawn role>`
- [x] **Positive control:** a plain `muxcode agent launch edit` (a real edit relaunch) still writes edit's startup request — script section B
- [x] The paired second row is explained (Phase 1) and, if it is a second launch of the same worker, fixed or filed — it is edit's own `response:startup`, not a second launch; nothing to fix. See [Findings for Phase 1](#findings-for-phase-1)
- [x] A spawn worker launch leaves the base role's in-flight tasks in flight: a request the base agent has read and not answered keeps its task. **Control:** the same launch as the base role itself clears it (added 2026-11-27) — script sections A and B, and the unit test
- [x] A spawn worker launch leaves the base role's agent-defs and sandbox stamps untouched. **Control:** the same launch as the base role itself restamps (added 2026-11-27) — the agent-defs stamp in script sections A and B and the unit test. **The sandbox stamp is not asserted anywhere**: a Claude or stub launch has no sandbox fingerprint; it is skipped by the same branch as the agent-defs stamp, by code reading only
- [x] `bash scripts/test-spawn-startup-bootstrap.sh` passes — 22 passed, 0 failed, floor 22, 2026-11-27 17:57

### Technical approach

In `RunAgentLaunchResume`, when `AGENT_ROLE` names a spawn role (`IsSpawnRole`), skip the startup
message: `PreLaunchSetup` does three things — retires pending startups for the role, sends the
startup, logs the launch row — and a worker launch needs only the third, attributed to the spawn role.
Retiring must be skipped too: run with the base role it would remove **edit's** own unread startup.

The same function next calls `ClearInFlightTasksForRole(session, role, …)` with the base role, then
`StampAgentDefHash` and `StampLaunchSandbox`. Phase 1 confirmed the first acts on the live base agent,
and on the user's decision of 2026-11-27 the guard covers all of them: one `IsSpawnRole(AGENT_ROLE)`
branch in `RunAgentLaunchResume` that writes the launch row (`logAgentLaunch`, factored out of
`PreLaunchSetup`, the row gaining ` as=<spawn role>`) and nothing else; the base-role road is unchanged.

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

- [x] Reproduce on a scratch session: one `spawn start` → count `startup` rows to `edit` in `log.jsonl` (1–2 expected before the fix); record the count here. The assertion that it is zero is written with Phase 2's fix, not before it — reproduced on the **live** session, not a scratch one: one worker launch, 2 rows (one request, one response)
- [x] Explain the pair: correlate the second row's `ts` with `spawn-launched`/`graph-spawn-replaced`/relaunch lifecycle rows; record the cause here — edit's own correlated reply
- [x] Check whether a worker launch clears the base role's in-flight tasks (`ClearInFlightTasksForRole` with the base role); record what was found and file it if it does — it does; widened into this spec on the user's decision rather than filed

### Findings for Phase 1

Recorded 2026-11-27 from edit's notes (`/tmp/mux188-widen.md`); the two bus-log rows and the launch
row were re-read by plan in the live session's `log.jsonl` and lifecycle log.

| Finding | Detail |
|---------|--------|
| One launch, one stray request | The only `reason=spawn` launch of the session — lifecycle `launch role=edit cli=claude reason=spawn` at 17:17:22, the `implement` worker of run `1795811929` — is followed by one `request:startup` from `edit` to `edit` (`ts` `1795817842`, `launch_reason` `spawn`) |
| The pair is edit's own answer | The second row is edit's `response:startup` (`ts` `1795817854`, 12 s later; the 2026-09-24 pairs were 13–17 s apart). It is written to `log.jsonl` though never delivered (MUX-182 Phase 5). Not a second launch, not `replaceLostWorkers`, not a second `PreLaunchSetup` road — nothing further to fix |
| A worker launch clears the base agent's work | `ClearInFlightTasksForRole` with the base role clears in-flight tasks addressed to edit that have left its inbox (`clearUnlessQueued`: consumed, or not queued) — a request the live edit agent has read and is working on. Pinned by the base-role control of `TestRunAgentLaunch_SpawnWorkerLeavesBaseRoleAlone`, where the same task ends `timed-out` |
| The stray misleads `graph status` | Run `1795811929`'s "Triggered by" read the stray as edit's last request: "launching agent's last bus request was its startup message (launch reason: spawn)" |

### Phase 2: Fix

- [x] `RunAgentLaunchResume` sends no startup message and retires none when `AGENT_ROLE` is a spawn role; the launch lifecycle row is still written, naming the spawn role — and, widened, clears no base-role task and writes no base-role stamp: `if worker := os.Getenv("AGENT_ROLE"); IsSpawnRole(worker)` → `logAgentLaunch(…, worker)` only (`role=<base> … as=<spawn role>`). Read in the tree by plan; the test's first cut passed `./test.sh` at 17:36, 17:46 and 17:50
- [x] Unit tests: a spawn-role launch leaves the base role's inbox unchanged, an unread base-role startup included; **control** — a base-role launch writes its startup as before, on the fresh and the resume road — `TestRunAgentLaunch_SpawnWorkerLeavesBaseRoleAlone` holds four cases (worker and base role, fresh and resume) over the unread startup, the read task and the agent-defs stamp; all four subtests passed on the test agent at 17:54 (`go test ./bus -run TestRunAgentLaunch`), after the 17:51 extension. It asserts neither the sandbox stamp nor the row's ` as=` field — edit, 2026-11-27: a Claude launch has no sandbox fingerprint to assert, and ` as=` is asserted in section A of the Phase 3 script
- [x] If the pair was a second launch, fix its cause or file it — it was not; nothing to fix or file
- [x] `docs/architecture.md` spawn section: a worker gets no startup bootstrap; the `spawn-task` seed is what starts it — "A worker launch touches nothing of its base role", after the Agent Spawn Flow steps

### Phase 3: Integration test

- [x] Create `scripts/test-spawn-startup-bootstrap.sh` — live scratch daemon on a private tmux server, a stub worker — private tmux server and stub agents, but **no daemon**: `spawn start` launches and seeds the worker itself, and every effect checked happens at launch
- [x] Test: one worker launch → zero `startup` rows to `edit` and none to the spawn role; worker inbox holds exactly one `spawn-task` — section A
- [x] **Positive control:** `muxcode agent launch edit` on the scratch session → one `startup` row to `edit` — section B: the unread startup retired for a new one, the task cleared, the stamp rewritten
- [x] Test: a worker launch leaves a base-role task the base agent has read in flight and the base role's stamps unchanged; the base-role launch clears and restamps (added 2026-11-27) — sections A and B, the agent-defs stamp only
- [x] **Negative control:** a build from a copy of the tree with the skip removed writes the stray row, so the zero-rows assertion can fail — section C: the guard patched to `false &&` in a copy; that build sends the stray startup, retires edit's unread one, clears the task and restamps
- [x] Coverage floor; run and record counts here

Run through the run agent on 2026-11-27 17:57 (reply `1795820268-run-47b37164`, log
`/tmp/test-spawn-startup-bootstrap.log`): **22 passed, 0 failed, coverage floor 22 met, exit 0** —
sections A (10), B (5), C (5) and the two builds.

## Out of scope

- Edit's response to the stray — already silent (MUX-182 Phase 5).
- Loop detection on the pair — `DetectMessageLoop` already drops self rows.

## Status

Complete

Closed out 2026-11-27 at 21/21 on the user's request relayed by edit — widened the same day (see
[Decisions (user, 2026-11-27)](#decisions-user-2026-11-27)), implemented by edit and shipping in the
[MUX-190](./MUX-190-codex-agent-drops-a-consumed-request-after-a-guard-denial.md) PR, which
closes #142. It was never the loop's active spec. The 17:54 review, run on the chain rather than by
the loop, found the launch tests, script and docs sound. One thing the ticks do not cover: the
sandbox stamp is skipped by code reading only, asserted nowhere.

Filed 2026-09-24 on the user's instruction relayed by edit, from seven observations on 2026-09-23/24
confirmed in `log.jsonl`. The addressing cause is verified in code (the spawn launch string →
`PreLaunchSetup` at `launch.go:1091`); the paired second row was then observed and unexplained — it is
explained in Phase 1's findings.

Unparked 2026-11-24 on the user's Decision 1 (a) — no bootstrap for a worker — relayed by edit and
recorded above.
