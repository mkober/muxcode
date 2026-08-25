# Graph-Agent Orchestrator

An explicit DAG control plane on top of the existing bus, specialist roles, linear chains, spawns, tasks, and workflow state machine. Today orchestration is mostly linear chains plus LLM-as-router (edit/auto); a graph layer adds fan-out/join, branching/loops, durable multi-node runs, and inspectable templates — without replacing specialists or authority gates.

## Context

### What muxcode orchestrates today

| Primitive | Role | Shape |
|-----------|------|-------|
| edit / auto | LLM orchestrators | Conversational routing via `muxcode send` |
| Event chains (`bus/profile.go`) | Deterministic pipelines | build→test→review, deploy→run→watch; first-match conditions |
| Workflow state machine (`bus/workflow.go`) | Session telemetry | Single current state; observes, never blocks |
| Subscriptions (`bus/subscribe.go`) | Fan-out notify | No join/barrier |
| Tasks + delivery receipts (`bus/task.go`, `bus/delivery.go`) | Track delegated work | `--wait` / `--track` |
| Spawns + worktrees (`bus/spawn.go`) | Ephemeral workers | One-off agents; owner gets `spawn-complete` |

So: fixed lines + LLM scheduler + optional workers. Missing: an executable multi-node graph with joins and durable run history. There is no graph agent or spec in the repo today — the closest building blocks all exist and are reused below.

**The wake-per-step tax.** `--track` already makes any *single* delegation non-blocking, but multi-step work still routes through edit: every completion wakes it, and it must hold the remaining plan in context and decide the next send. Edit's involvement in an N-step sequence is O(N) wakes — each one consuming attention, context, and a prompt turn — and the plan itself is only as durable as edit's context window. The graph moves that routing into the daemon so edit fires one `graph run` and is interrupted only at human gates and terminal states.

### What graph-agent orchestration provides

1. **Explicit multi-agent DAGs** — nodes = roles/actions; edges = success/failure/custom outcomes. Not "edit decides the next send" or a single-successor chain.
2. **Fan-out / fan-in with join barriers** — parallel research (dual-provider draft), multi-package test, multi-env verify; wait for all/any/quorum before synthesize or review. Spawns already create workers; the graph schedules and joins them.
3. **Richer control flow** — multi-way branches, capped fix loops (fix→build→test × N), human wait nodes (requirements PR approve), skip subgraphs (no infra → skip deploy). Conditions already exist (`bus/conditions.go`); graphs use them as branch nodes, not only "which next action".
4. **Durable run state** — separate from the global `workflow-state.json`: per-run node status, I/O, resume after crash, `graph status|cancel|retry --from test`.
5. **Planner vs executor vs specialists** — a graph agent compiles intent → DAG and monitors; the daemon executes edges (send/spawn); build/test/review/commit/… stay exactly as they are. Keeps the "don't trust the LLM to remember test after build" property of hooks.
6. **Natural fit for patterns already in flight**:

   | Emerging need | Graph value |
   |---------------|-------------|
   | Research dual-provider ([`backlog/MUX-016-research-dual-provider.md`](../backlog/MUX-016-research-dual-provider.md)) | Fan-out → critique → join/synthesize |
   | Auto story-lifecycle (skill) | Skill compiled to a DAG + human gates |
   | build→test→review | Reusable subgraph template |
   | Spawn worktrees ([`completed/MUX-091-spawn-worktrees.md`](../completed/MUX-091-spawn-worktrees.md)) | Map nodes → parallel workers |
   | `NotifyPlanOn` spec verify | Side-effect / parallel edge |

7. **Observability** — DAG rendered in TUI/console; lifecycle events per edge. Clearer than reconstructing call order from inbox JSONL.

8. **Write-parallel fan-out via worktrees** — map/worker nodes reuse `StartSpawn()`, which gives every worker its own git worktree by default ([`completed/MUX-091-spawn-worktrees.md`](../completed/MUX-091-spawn-worktrees.md)). Isolation upgrades fan-out from parallel readers to parallel *writers*: per-package migrations, parallel fix attempts on a red build, and tournament patterns (N independent attempts at the same task → join → a review node picks the winner — impossible in a shared tree, where attempts overwrite each other). A run works on an isolated HEAD snapshot, so it never collides with edit's live uncommitted work, and `graph cancel` discards worktrees without dirtying the main tree.

### What it does not replace

- Tool profiles / authorities — edit≠git, plan owns docs + Jira/Confluence writes, **user-initiated commits stay user-initiated**
- Hook vs non-hook provider split — the graph still sends ordinary bus messages; providers keep their delivery paths
- Delivery-ack, stuck watchdogs, PII scrub — the executor rides on top of them, never around them
- Workflow SM as session status — graph runs are instances; the SM stays global telemetry

### Illustrative surface

```bash
muxcode graph run coding-pr "implement PBP1-4915"
muxcode graph run custom --file my-dag.json
muxcode graph status <run-id>
muxcode graph cancel <run-id>
muxcode graph retry <run-id> --from test
muxcode graph validate my-dag.json
```

Node types: `send`, `spawn`, `wait_human`, `wait_event`, `join` (all/any/quorum), `condition` (reuses `EvaluateConditions()`), `map` (dynamic fan-out).

Templates: `coding-pr`, `story-lifecycle`, `research-critique`, `deploy-verify`, plus a `build-test-review` reusable subgraph.

### Relationship to existing backlog items

- **Subsumes** "Pipeline definitions" (user-defined YAML/JSON pipelines) — a linear pipeline is the degenerate single-path graph; this spec delivers that surface plus joins, branches, and durable runs.
- **Complements** [`opencode-plugin-hook-bridge`](../backlog/MUX-011-opencode-plugin-hook-bridge.md) — outcome fidelity for non-hook providers determines how reliably graph edges can route on success/failure (see Open questions).
- **Builds on** [`completed/MUX-043-conditional-chains.md`](../completed/MUX-043-conditional-chains.md) (condition engine), [`completed/MUX-099-workflow-state-machine.md`](../completed/MUX-099-workflow-state-machine.md) (telemetry stays as-is), [`completed/MUX-033-agent-spawn.md`](../completed/MUX-033-agent-spawn.md) + [`completed/MUX-091-spawn-worktrees.md`](../completed/MUX-091-spawn-worktrees.md) (map-node workers), [`completed/MUX-094-transactional-messaging-bus.md`](../completed/MUX-094-transactional-messaging-bus.md) (delivery semantics).

## Requirements

### Acceptance criteria

- [x] Graph definitions are declarative JSON: nodes (typed), edges keyed by outcome (`success`/`failure`/custom), validated by `muxcode graph validate` — undefined node refs, unreachable nodes, and uncapped cycles are errors; loops only via explicit `max_iterations` on a loop edge
- [x] Async orchestration: `graph run` returns immediately (never blocks the caller's prompt); for a gate-free run, edit receives exactly one wake (run completion) — its involvement is O(`wait_human` gates), never O(nodes) — proven live: `graph run` returned a run id before any node executed (`a=ready`, build inbox empty, daemon not yet ticked), then edit received **exactly one** `graph-complete` wake and **zero** per-node wakes across a 3-node run
- [x] `muxcode graph run <template>|--file <path>` starts a run; the daemon executes edges deterministically (send/spawn) — no LLM decides node succession
- [x] Fan-out via `map` nodes (dynamic item list → parallel spawn/send) and fan-in via `join` nodes with `all`/`any`/`quorum` barrier semantics — re-opened once and now genuinely closed: checked off on unit tests, **disproved** by the first live run (`z never dispatched after join` — barrier held but never released), root-caused to unknown-outcome completions not counting toward the barrier, fixed in `joinBarrierMet` (now counts `run.EdgeFires`) with `TestExecJoinReleasesOnUnknownOutcomes`, and **confirmed by the green re-run**: barrier holds with one branch (✅), holds `z` back (✅), *and releases once both complete* (✅). That third assertion is the one that was failing; the first two passed even while fan-in was broken.
- [ ] Worktree workers have a defined output contract: a worker's diff is harvested into the run store *before* its worktree is cleaned up (MUX-091 deletes worktrees on spawn completion — uncommitted work dies with them), so join nodes consume harvested outputs, never live trees; resume treats a missing or purged worktree as re-run-the-node, never reattach
- [x] `condition` nodes reuse `EvaluateConditions()` and `ChainContext` verbatim — no second condition dialect
- [x] Per-run durable state under the bus dir (`graphs/<run-id>/`): node status, inputs/outputs, timestamps; a daemon restart resumes in-flight runs from persisted state
- [x] `graph status|cancel|retry <run-id> [--from <node>]` work against the run store; retry re-executes from the named node without re-running completed upstream nodes
- [x] `wait_human` nodes block until explicit user approval; **no graph node may fire a git mutation (commit/push/PR) or Atlassian write without passing a `wait_human` gate** — authority gates (`CheckCommitAuthority`, `CheckAtlassianAuthority`) remain the enforcement backstop
- [ ] Specialists are unchanged: graph edges deliver ordinary bus messages / spawns; tool profiles, delivery-ack receipts, PII scrub, dedup/relay guards, and watchdogs all apply to graph-originated traffic
- [x] Workflow state machine untouched: graph runs are instances alongside it, not a replacement for global session telemetry — verified by absence: `graph.go`, `graph_run.go`, `graph_exec.go`, `graph_templates.go`, and `cmd/graph.go` contain **zero** references to `TransitionWorkflow`/`WorkflowState`, and the daemon's hook is a bare `bus.StepGraphRuns(d.session)` with no SM call
- [x] Lifecycle events per node/edge transition (`graph-node-start`, `graph-node-done`, `graph-run-complete`, …) in the session lifecycle log
- [ ] TUI/console can render a run: node grid with per-node state colors and the active edge
- [x] Built-in templates ship and validate: `coding-pr`, `story-lifecycle`, `research-critique`, `deploy-verify`, `build-test-review` subgraph
- [ ] Existing chain, subscription, spawn, and daemon tests still pass — the graph layer is additive

### Technical approach

- **Model** (`bus/graph.go`): `Graph`, `Node` (type + role/action/message/conditions/join policy/map source), `Edge` (from, to, outcome, optional `max_iterations`). JSON (un)marshal, structural validation (DAG check with explicit capped-loop exemption), embedded template registry resolved like agent files (project > user > builtin).
- **Run store** (`bus/graph_run.go`): run dir per instance under `BusDir()`, JSONL or per-node status files, atomic transitions (`pending → ready → running → done|failed|skipped|waiting`), input/output capture from correlated response messages.
- **Executor** (daemon): a `checkGraphRuns()` tick computes ready nodes (all incoming edges satisfied per join policy), fires edges via the existing send path (`SendNoCC` + tracked task for correlation) or `StartSpawn()` for map/worker nodes, consumes completions via task/receipt correlation, routes the outcome edge, persists state before and after each transition. Cancellation marks the run and stops scheduling; in-flight node work completes or times out via existing task expiry.
- **Outcome routing**: node outcome derives from the correlated response (exit code / outcome field where the provider supplies one; explicit `unknown` otherwise) — same `describeOutcome()` vocabulary as chains.
- **Human gates**: `wait_human` writes a pending-approval marker surfaced to edit (which talks to the user); `muxcode graph approve <run-id> <node>` releases it. Commit/Atlassian node types are refused by `graph validate` unless downstream of a `wait_human` gate.
- **Planner split** (later phase): a graph-agent role (or edit) may *compose* a DAG file from intent, but composition is authoring, not execution — the daemon executes only validated definitions.

### Key files

| File | Change |
|------|--------|
| `bus/graph.go` | New — graph model, node/edge types, validation, template registry |
| `bus/graph_run.go` | New — durable per-run state store, transitions, resume scan |
| `watcher/watcher.go` | New `checkGraphRuns()` executor tick in the daemon poll loop |
| `cmd/graph.go` | New — `graph run|status|cancel|retry|approve|validate|list` |
| `bus/conditions.go` | Reused as-is by `condition` nodes (`EvaluateConditions()`, `ChainContext`) |
| `bus/spawn.go` | Reused for `map`/worker nodes; possibly a run-id tag on spawns |
| `bus/task.go`, `bus/delivery.go` | Reused for node-completion correlation |
| `bus/lifecycle.go` | New graph event kinds |
| `tui/` | Run view (node grid, states, active edge) |
| `docs/architecture.md`, `docs/agent-bus.md` | Document the control plane and CLI |

## Implementation

### Phase 1: Graph model and validation

- [x] Define `Graph`/`Node`/`Edge` structs with JSON encoding in `bus/graph.go`
- [x] Implement node types: `send`, `spawn`, `wait_human`, `wait_event`, `join`, `condition`, `map`
- [x] Structural validation: unknown refs, unreachable nodes, uncapped cycles, join-policy sanity, gated commit/Atlassian rule
- [x] Template registry with 3-tier resolution and the 5 built-in templates
- [x] `muxcode graph validate` + `graph list` (templates)
- [x] Unit tests: parse/validate matrix incl. capped-loop acceptance and gate-rule rejection

> Verified 2026-08-25 by the plan agent against the code, not by assertion count.
> Structs + JSON tags (`bus/graph.go:38-71`); all 7 node types as `NodeSend`…`NodeMap`
> constants (`:15-21`); five discrete validators — `validateNode`, `validateEdges`,
> `validateReachability`, `validateAcyclic`, `validateJoins`, `validateGates`; 3-tier
> resolution `project > user > builtin` in `ResolveGraphTemplate` with all five built-in
> templates present in `graph_templates.go`; `validate`/`list` in `cmd/graph.go` **and
> wired into the `main.go` dispatch** (`main.go:28,241`) — a subcommand file alone would
> not have made the CLI reachable. 23 unit tests including
> `TestValidateCappedLoopAccepted`, `TestValidateGateRule`,
> `TestValidateGateRuleMixedPaths`, `TestBuiltinGraphTemplatesValidate`.
>
> Test **coverage** was confirmed by reading; test **passing** is inferred from the
> build→test→review chain reaching review-success, not from a run by this agent.

### Phase 2: Durable run store

- [x] `bus/graph_run.go`: run creation, node status transitions, input/output persistence under `BusDir()/graphs/<run-id>/`
- [x] Resume scan: enumerate in-flight runs on daemon start, recompute ready set from persisted state
- [x] `graph status` rendering (per-node state, timestamps, outcome)
- [x] Unit tests: transition legality, crash-resume from each node state, idempotent re-persist

> Verified 2026-08-25 by the plan agent — **3/4, not 4/4**. Done: the store itself
> (`GraphRunsDir` = `BusDir()/graphs`, `run.json` + `graph.json` + `nodes/<id>.json`,
> `atomicWriteJSON`, `TransitionGraphNode` guarded by a `legalNodeTransitions` table,
> `GraphNodeStatus` carrying `Output`/`TaskID`/timestamps) and `graph status`
> (`FormatGraphRun` + `case "status"` in `cmd/graph.go`).
>
> **Resume scan left open deliberately.** `ScanInFlightGraphRuns` and `ComputeReadyNodes`
> are implemented but **never called** — `grep` finds zero callers and `daemon/daemon.go`
> contains no graph reference at all. The step reads "on daemon start", so the primitives
> existing is not the step being done; that wiring belongs to the Phase 3 executor.
>
> **Run-store tests landed at 10:04**, mid-verification — an earlier pass in this same
> session correctly recorded them as absent. 14 tests in `bus/graph_run_test.go`, covering
> all three cases this step names by name: `TestTransitionLegality` (asserts
> `pending→running`, `ready→done`, and un-re-armed `done→running` are all rejected),
> `TestCrashResumeReadySet` (readiness derivable purely from persisted state), and
> `TestIdempotentRePersist`. Plus join policies, capped-loop exhaustion, and
> `TestAtomicWriteLeavesNoTmp`.
>
> Hardened 10:10: `NewGraphRunID` now sanitizes the template name (anything outside
> `[A-Za-z0-9_-]` becomes `-`) before it is interpolated into the run id. That id becomes a
> **directory name** under `BusDir()/graphs/<run-id>/`, so an unsanitized name was a path
> traversal — `TestNewGraphRunIDSanitizesName` pins it with `"../evil/name x"`. Worth
> carrying into Phase 3: node ids reach `nodes/<id>.json` by the same route and are
> currently written unsanitized.
>
> **Resume scan closed 10:29** — `daemon.go:317` now calls `checkGraphRuns()` in the poll
> loop, which calls `bus.StepGraphRuns` → `ScanInFlightGraphRuns` (`graph_exec.go:86`), a
> genuine non-test caller. All state lives in the per-run store, so as the daemon comment
> puts it, *the first tick after a restart **is** the resume scan* — no separate recovery
> path. That satisfies the step's intent.
>
> One divergence worth recording rather than glossing: `ComputeReadyNodes` — the Phase 2
> ready-set API this step was written around — was **not** what the executor used. It
> derives readiness by arming edges from persisted node statuses (`ReadAllNodeStatuses` +
> `routeFinishedNodes`/`armTarget`/`joinBarrierMet`), leaving `ComputeReadyNodes` reachable
> only from tests. **Resolved: deleted** (verified — zero references remain in the module).
> The step's intent survives it — the tick is stateless, reading run, graph, and node
> statuses fresh from disk each time, with a per-node `Routed` flag preventing a completion
> from being double-routed across a restart.

### Phase 3: Daemon executor

- [x] `checkGraphRuns()` tick: ready-node computation per join policy (`all`/`any`/`quorum`)
- [x] Edge firing: `send` nodes via `SendNoCC` + tracked task; `spawn`/`map` nodes via `StartSpawn()`
- [x] Completion correlation via tasks/receipts; outcome extraction and edge routing; `condition` nodes via `EvaluateConditions()`
- [x] Cancellation and node timeout (reuse task expiry); failed-edge default routing
- [x] Lifecycle events per node/edge transition
- [x] Unit tests: linear run, fan-out/join, quorum, capped loop exhaustion, cancel mid-run

> Verified 2026-08-25 by the plan agent — **6/6**, wiring confirmed at both ends, not just
> function existence. `daemon.go:317` calls `checkGraphRuns()` inside the poll loop
> (deliberately right after `checkTrackedTasks`, so completions correlated on a tick route
> their edges on that same tick).
>
> | Step | Evidence in `bus/graph_exec.go` |
> |---|---|
> | Join policies | `JoinAll` / `JoinAny` / `JoinQuorum` in `joinBarrierMet` (`:524-528`) |
> | Edge firing | `SendNoCC` + `CreateTask` for send (`:181-185`); `StartSpawn` for spawn/map (`:33`) |
> | Condition nodes | `EvaluateConditions(n.Conditions, ctx)` (`:225`) — the existing engine, no second dialect |
> | Cancel + timeout | `CancelGraphRun`; `nodeTimeoutSecs(n)` passed into `CreateTask`, reusing task expiry |
> | Failed-edge default | `finishNode(..., OutcomeFailure, ...)` on every dispatch failure path |
> | Lifecycle events | 7 kinds: `graph-node-start`, `graph-node-done`, `graph-gate-pending`, `graph-run-failed`, `graph-run-canceled`, `graph-loop-exhausted`, `graph-unknown-fallback` |
> | Tests | `TestExecLinearRun`, `TestExecFanOutJoinAll`, `TestExecJoinQuorumBarrier`, `TestExecCappedLoopExhaustion`, `TestExecCancelMidRun` — all five named cases, plus condition/map/gate/failure-routing (11 total) |
>
> As with Phase 1, test **passing** is inferred from the chain reaching review-success; this
> agent read the tests, it did not run them.

### Phase 4: Control CLI and human gates

- [x] `muxcode graph run <template>|--file` with intent arg interpolation into node messages
- [x] `graph cancel`, `graph retry --from <node>` (upstream results preserved)
- [x] `wait_human`: pending-approval marker + edit notification + `graph approve` release; `wait_event` on bus events
- [ ] Verify graph-originated sends pass dedup/relay-suppression and delivery-ack unmodified
- [x] Unit tests: retry-from semantics, approval release, gate enforcement end to end

> Verified 2026-08-25 — **4/5**. `cmd/graph.go` now exposes all seven subcommands
> (`run`, `validate`, `list`, `status`, `cancel`, `retry`, `approve`); `--from` is parsed and
> the help documents "keeping upstream results"; `wait_event` parks at dispatch
> (`graph_exec.go:252`) and releases in `harvestWaitingNode` (`:413`). Tests cover
> retry-from (`TestRetryGraphRunFromNode`, `…RefusesRunningRun`, `…ResetsLoopBudget`),
> approval release (`TestExecHumanGate`), gate enforcement (`TestValidateGateRule`,
> `…MixedPaths`), and `TestExecWaitEventRelease`.
>
> **Step 4 left open deliberately.** It says *verify*, and no such verification exists:
> `graph_exec_test.go` contains no reference to dedup, relay-suppression, or delivery-ack.
> The executor does route through `SendNoCC`, so the guards structurally apply — but
> "structurally should" is not the evidence this step asks for, and graph traffic is exactly
> the shape those guards were tuned against (repeated same-`(from,to,action)` sends to one
> role would trip relay-suppression at 4 within 300s). This wants a real test before close.

### Phase 5: Observability

- [x] Console/TUI run view: node grid with Dracula state colors, active edges, run header (id, template, elapsed) — minimal status render only; the dedicated interactive DAG TUI (run browser, gate approval from the view) is [`MUX-031-graph-run-tui.md`](../backlog/MUX-031-graph-run-tui.md), buildable in parallel once Phase 2's run store lands
- [x] `graph status --json` for scripting
- [x] Docs: `docs/architecture.md` control-plane section, `docs/agent-bus.md` CLI reference, backlog cross-links

> Docs written 2026-08-25 by the plan agent: `architecture.md` gained
> "Graph orchestration control plane" (node-type table, executor tick sequence, the
> resume-is-not-a-separate-path property, unchanged authority gates, 7 lifecycle events)
> alongside the other daemon sections; `agent-bus.md` gained `### muxcode graph` next to
> `muxcode chain` with all seven subcommands, template precedence, and the run-store layout.
> Cross-links are reciprocal and all seven documented subcommands were checked against
> `case` arms in `cmd/graph.go` rather than taken from the spec's illustrative surface.
> Backlog cross-links to/from MUX-031 were repointed when the spec moved to `drafts/`.
>
> Scope note on the run view: `FormatGraphRunColored` (`graph_run.go:509`, with
> `GraphNodeStateColor`) renders the Dracula node grid, and it is reached from
> `cmd/graph.go:254` — i.e. the render is delivered by `graph status`, **not** wired into
> `bus/console.go` or the `tui/` package, neither of which references graphs. That matches
> this step's "minimal status render only" scoping, with the interactive DAG surface left to
> [`MUX-031-graph-run-tui.md`](../backlog/MUX-031-graph-run-tui.md) — but nobody should read
> this checkbox as "the console shows graph runs".

### Phase 6: Integration test

- [x] Create `scripts/test-graph-orchestrator.sh` (requires running muxcode session)
- [x] Test: `graph validate` rejects an uncapped cycle and an ungated commit node; accepts all built-in templates
- [x] Test: run a 3-node linear graph (send→condition→send) → verify node statuses reach `done` and lifecycle events recorded
- [x] Test: gate-free linear run → `graph run` returns before any node executes, and edit's inbox shows exactly one graph wake (run completion), zero per-node wakes
- [x] Test: run a fan-out/join graph (map → 2 spawns → join all) → verify barrier held until both workers completed
- [x] Test: kill and restart the daemon mid-run → verify the run resumes and completes from persisted state
- [x] Test: `graph retry --from <node>` re-executes only downstream nodes
- [x] Run the script and verify all checks pass — **29 passed, 0 failed** (11:17)

> Script verified 2026-08-25 — 317 lines, all seven scenarios present with **real
> assertions, not stubs**. Spot-checked the two that are easiest to fake:
>
> - *Single wake* counts `Action: graph-complete` in edit's inbox and asserts `== 1`, then
>   asserts `grep -c 'graph-node'` is `0` — a genuine test of the O(gates)-not-O(nodes)
>   claim, not just "the run finished".
> - *Daemon resume* kills the daemon mid-run, **answers the pending request while it is
>   down** so the response can only come from the persisted store, restarts, and requires
>   both that the next node dispatches and that the run reaches `complete`.
>
> Runs against a scratch `BUS_SESSION` with its own daemon and a pinned lifecycle log, so it
> needs no live session. Registered in `CLAUDE.md` (line 59).
>
> **Run 2026-08-25 — and it earned its keep immediately.**
>
> | Run | Result |
> |-----|--------|
> | 11:05 (first ever execution) | **15 passed, 22 failed** |
> | 11:10 (after `message.go` fix) | **28 passed, 1 failed** |
>
> The first run found a **real product bug**, not a harness defect. Every failure cascaded
> from one point: `answer_role` could not reply to a graph-dispatched request. Graph sends
> carry `From = graphSender = "daemon"` (`graph_exec.go:27`), and the inbox rendered its
> reply instruction verbatim — so an agent handed a graph node's request was told to run
> `muxcode send daemon …`, which fails with *unknown role*, and its response was never
> recorded. Every downstream node then waited forever on a completion that could not arrive.
> Fixed in `bus/message.go` by normalizing the reply target through `NormalizeBusRole`
> (`daemon → edit`); correlation runs on `--reply-to`, so routing the reply to the
> normalized role loses nothing.
>
> **This is the case for running the script rather than reading it.** All 15 executor unit
> tests passed against this bug, because they drive the executor in-process and never
> exercise the inbox render an agent actually reads. Nothing short of an end-to-end run
> would have caught it.
>
> **Beware the first run's 15 "passes".** Several were vacuous — `join barrier held with one
> of two branches complete` and `post-join node z held back` both passed because *nothing
> ever dispatched*, so the barrier trivially held. A pass count is not a coverage measure
> when upstream steps have already failed.
>
> **One failure remains: `z never dispatched after join`.** The barrier holds correctly but
> never releases once both branches complete — fan-in does not finish. See the re-opened
> fan-out/join acceptance criterion above. This step stays unchecked until the run is green.

## Open questions

- **Outcome fidelity on non-hook providers** — hook providers give deterministic exit codes; OpenCode/Codex outcomes are inferred. Does the executor demand hook-grade outcomes for `failure`-routed edges, or accept `unknown` with an explicit `on_unknown` edge? Interacts with [`opencode-plugin-hook-bridge`](../backlog/MUX-011-opencode-plugin-hook-bridge.md).
- **Who authors DAGs in v1** — hand-authored JSON + built-in templates only, or also an LLM planner (graph agent / edit) that compiles intent → DAG? Proposal: templates-only first; planner is a later, separate spec.
- **Story-lifecycle template scope** — the full `story-lifecycle` skill includes Jira transitions and PR creation, all behind authority gates; the template must model those as `wait_human`-gated nodes, which may make it more documentation than automation until authorities are opted in.
- **Concurrency limits** — per-run and global caps on simultaneously running nodes (map fan-out could spawn many worktrees); likely `MUXCODE_GRAPH_MAX_PARALLEL`. N full checkouts under `/tmp/muxcode-spawn-*` also count toward the [MUX-002](../completed/MUX-002-disk-pressure-wrong-filesystem.md) disk-pressure footprint signal (1 GiB default) — a correct guard (worktrees are exactly what cleanup can free), but another reason to cap fan-out. Per-tree build caches are cold (`node_modules` absent; Go's user-level cache is shared), so tiny tasks may not pay for isolation.
- **Dirty-tree baseline** — worktree workers see committed `HEAD` only (MUX-091 non-goal: no uncommitted state in worktrees). A run started mid-edit fans out workers blind to edit's uncommitted changes. At minimum a documented constraint; possibly `graph run`/`validate` warns when a write-shaped map starts on a dirty tree.
- **Workflow SM interplay** — should the SM observe graph runs (e.g. surface "graph coding-pr: 4/9 nodes") or stay fully independent?

## Sources

- `docs/architecture.md`, `docs/hooks.md`
- `docs/requirements/completed/{conditional-chains,workflow-state-machine,agent-spawn,spawn-worktrees,agent-mode,transactional-messaging-bus}.md`
- `docs/requirements/backlog/MUX-016-research-dual-provider.md`
- `tools/muxcode/bus/{profile,workflow,spawn,subscribe,task}.go`

## Provenance

Filed by the plan agent on 2026-08-18 from a user-provided analysis of what a graph-agent orchestrator would add to muxcode. Subsumes the earlier "Pipeline definitions" backlog idea.

## Time Tracking

| Branch | Active time | Last updated |
|--------|-------------|--------------|
| MUX-014-graph-agent-orchestrator | 2h 24m | 2026-08-25 13:01 |

## Status

**Complete** — closed 2026-08-25 on explicit user approval, at **31/32 steps and 11/15
acceptance criteria**. Phases 1, 2, 3, 5, 6 complete; Phase 4 at 4/5.

**Integration test green: 29 passed, 0 failed** — fan-out/join, daemon-restart resume,
retry-from, and the one-wake async property all confirmed against a live daemon.

### Known gaps at completion

Closed deliberately with these unmet, **not** silently checked off. No box above was ticked
without evidence; each item below is a real remainder, recorded so the completed spec does
not misrepresent itself.

| Unmet | Why it matters | Evidence status |
|-------|----------------|-----------------|
| Phase 4 step 4 — verify graph sends pass dedup / relay-suppression / delivery-ack | A wide `map` fan-out or capped retry loop issues repeated same-`(from,to,action)` sends to one role, which trips relay-suppression at 4 within 300s. Plausible silent false positive in production | **Zero** references to dedup, relay-suppression, or delivery-ack in `graph_exec_test.go` *or* `scripts/test-graph-orchestrator.sh` |
| Criterion: specialists unchanged (guards apply to graph traffic) | Same root as above | Same — no test exercises it |
| Criterion: worktree output contract (harvest diff before cleanup) | MUX-091 deletes worktrees on spawn completion; uncommitted worker output would die with them | No harvest-before-cleanup implementation found in `graph_exec.go` |
| Criterion: TUI/console can render a run | `graph status` renders a colored node grid, but from `cmd/graph.go` — `bus/console.go` and `tui/` contain **zero** graph references | Factually unmet as written; the criterion may simply be broader than intended (see [`MUX-031`](../backlog/MUX-031-graph-run-tui.md)) |
| Criterion: existing chain/subscription/spawn/daemon tests still pass | The graph layer touches `main.go`, `message.go`, `task.go`, `daemon.go` — not purely additive | No full `go test ./...` run observed by the verifying agent |

**If this spec is reopened, start with the first row.** It is the only one that could hide a
production defect rather than a missing convenience, and it is cheap to test.
Graph model and validation, durable run store with restart resume, daemon executor
(join barriers, edge firing, `EvaluateConditions` reuse, cancel/timeout, 7 lifecycle event
kinds), the full seven-subcommand control CLI, and observability including docs.

Acceptance criteria **9/15** satisfied.

**Remaining work, in priority order:**

1. **Phase 4 step 4** — the last open step: verify graph-originated sends pass
   dedup/relay-suppression and delivery-ack unmodified. Still zero evidence;
   `graph_exec_test.go` mentions none of the three, and the integration script does not
   exercise them either. Graph traffic is the exact shape those guards were tuned against —
   a wide `map` fan-out or a capped retry loop issues repeated same-`(from,to,action)` sends
   to one role, which trips relay-suppression at 4 within 300s. This also holds the
   "Specialists are unchanged…" criterion open.

### What running the tests actually bought

Two production defects were found by the integration run and **missed by every executor unit
test**, which is the durable lesson from this spec:

| Defect | Why unit tests missed it | Would have caused |
|--------|--------------------------|-------------------|
| Graph sends carried `From = "daemon"`, so the inbox told agents to `muxcode send daemon …` (*unknown role*) | Unit tests drive the executor in-process; they never render the inbox an agent reads | No graph node completion ever recorded — every run hangs after node 1 |
| Unknown-outcome completions did not count toward a join barrier | Unit tests supplied authoritative outcomes | Any join with a non-hook provider (OpenCode/Codex infer outcomes) hangs forever, run parked in `running` |

Both are live-path bugs that in-process tests structurally cannot see. The first live run
scored 15/22 on a spec that read as 30/32 complete.
2. **Phase 4 step 4** — "verify graph-originated sends pass dedup/relay-suppression and
   delivery-ack unmodified" has no evidence. `graph_exec_test.go` never mentions any of the
   three. This is not a formality: graph traffic is the exact shape those guards were tuned
   against — repeated same-`(from,to,action)` sends to one role trip relay-suppression at 4
   within 300s, so a wide `map` fan-out or a capped retry loop is a plausible false positive.
   Criterion "Specialists are unchanged…" stays open on the same grounds.

**Two open questions for decision before close:**

- ~~`ComputeReadyNodes` is reachable only from tests~~ — **resolved: deleted.** Verified zero
  references remain.
- Criterion "TUI/console can render a run" is left **unchecked** even though Phase 5's
  render step is done, because they are not the same claim: `FormatGraphRunColored` is
  reached from `cmd/graph.go`, and neither `bus/console.go` nor `tui/` references graphs at
  all. Either wire the render into those surfaces, or narrow the criterion to match what
  `graph status` actually delivers.
