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

### What graph-agent orchestration provides

1. **Explicit multi-agent DAGs** — nodes = roles/actions; edges = success/failure/custom outcomes. Not "edit decides the next send" or a single-successor chain.
2. **Fan-out / fan-in with join barriers** — parallel research (dual-provider draft), multi-package test, multi-env verify; wait for all/any/quorum before synthesize or review. Spawns already create workers; the graph schedules and joins them.
3. **Richer control flow** — multi-way branches, capped fix loops (fix→build→test × N), human wait nodes (requirements PR approve), skip subgraphs (no infra → skip deploy). Conditions already exist (`bus/conditions.go`); graphs use them as branch nodes, not only "which next action".
4. **Durable run state** — separate from the global `workflow-state.json`: per-run node status, I/O, resume after crash, `graph status|cancel|retry --from test`.
5. **Planner vs executor vs specialists** — a graph agent compiles intent → DAG and monitors; the daemon executes edges (send/spawn); build/test/review/commit/… stay exactly as they are. Keeps the "don't trust the LLM to remember test after build" property of hooks.
6. **Natural fit for patterns already in flight**:

   | Emerging need | Graph value |
   |---------------|-------------|
   | Research dual-provider ([`backlog/MUX-016-research-dual-provider.md`](MUX-016-research-dual-provider.md)) | Fan-out → critique → join/synthesize |
   | Auto story-lifecycle (skill) | Skill compiled to a DAG + human gates |
   | build→test→review | Reusable subgraph template |
   | Spawn worktrees ([`completed/MUX-091-spawn-worktrees.md`](../completed/MUX-091-spawn-worktrees.md)) | Map nodes → parallel workers |
   | `NotifyPlanOn` spec verify | Side-effect / parallel edge |

7. **Observability** — DAG rendered in TUI/console; lifecycle events per edge. Clearer than reconstructing call order from inbox JSONL.

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
- **Complements** [`opencode-plugin-hook-bridge`](./MUX-011-opencode-plugin-hook-bridge.md) — outcome fidelity for non-hook providers determines how reliably graph edges can route on success/failure (see Open questions).
- **Builds on** [`completed/MUX-043-conditional-chains.md`](../completed/MUX-043-conditional-chains.md) (condition engine), [`completed/MUX-099-workflow-state-machine.md`](../completed/MUX-099-workflow-state-machine.md) (telemetry stays as-is), [`completed/MUX-033-agent-spawn.md`](../completed/MUX-033-agent-spawn.md) + [`completed/MUX-091-spawn-worktrees.md`](../completed/MUX-091-spawn-worktrees.md) (map-node workers), [`completed/MUX-094-transactional-messaging-bus.md`](../completed/MUX-094-transactional-messaging-bus.md) (delivery semantics).

## Requirements

### Acceptance criteria

- [ ] Graph definitions are declarative JSON: nodes (typed), edges keyed by outcome (`success`/`failure`/custom), validated by `muxcode graph validate` — undefined node refs, unreachable nodes, and uncapped cycles are errors; loops only via explicit `max_iterations` on a loop edge
- [ ] `muxcode graph run <template>|--file <path>` starts a run; the daemon executes edges deterministically (send/spawn) — no LLM decides node succession
- [ ] Fan-out via `map` nodes (dynamic item list → parallel spawn/send) and fan-in via `join` nodes with `all`/`any`/`quorum` barrier semantics
- [ ] `condition` nodes reuse `EvaluateConditions()` and `ChainContext` verbatim — no second condition dialect
- [ ] Per-run durable state under the bus dir (`graphs/<run-id>/`): node status, inputs/outputs, timestamps; a daemon restart resumes in-flight runs from persisted state
- [ ] `graph status|cancel|retry <run-id> [--from <node>]` work against the run store; retry re-executes from the named node without re-running completed upstream nodes
- [ ] `wait_human` nodes block until explicit user approval; **no graph node may fire a git mutation (commit/push/PR) or Atlassian write without passing a `wait_human` gate** — authority gates (`CheckCommitAuthority`, `CheckAtlassianAuthority`) remain the enforcement backstop
- [ ] Specialists are unchanged: graph edges deliver ordinary bus messages / spawns; tool profiles, delivery-ack receipts, PII scrub, dedup/relay guards, and watchdogs all apply to graph-originated traffic
- [ ] Workflow state machine untouched: graph runs are instances alongside it, not a replacement for global session telemetry
- [ ] Lifecycle events per node/edge transition (`graph-node-start`, `graph-node-done`, `graph-run-complete`, …) in the session lifecycle log
- [ ] TUI/console can render a run: node grid with per-node state colors and the active edge
- [ ] Built-in templates ship and validate: `coding-pr`, `story-lifecycle`, `research-critique`, `deploy-verify`, `build-test-review` subgraph
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

- [ ] Define `Graph`/`Node`/`Edge` structs with JSON encoding in `bus/graph.go`
- [ ] Implement node types: `send`, `spawn`, `wait_human`, `wait_event`, `join`, `condition`, `map`
- [ ] Structural validation: unknown refs, unreachable nodes, uncapped cycles, join-policy sanity, gated commit/Atlassian rule
- [ ] Template registry with 3-tier resolution and the 5 built-in templates
- [ ] `muxcode graph validate` + `graph list` (templates)
- [ ] Unit tests: parse/validate matrix incl. capped-loop acceptance and gate-rule rejection

### Phase 2: Durable run store

- [ ] `bus/graph_run.go`: run creation, node status transitions, input/output persistence under `BusDir()/graphs/<run-id>/`
- [ ] Resume scan: enumerate in-flight runs on daemon start, recompute ready set from persisted state
- [ ] `graph status` rendering (per-node state, timestamps, outcome)
- [ ] Unit tests: transition legality, crash-resume from each node state, idempotent re-persist

### Phase 3: Daemon executor

- [ ] `checkGraphRuns()` tick: ready-node computation per join policy (`all`/`any`/`quorum`)
- [ ] Edge firing: `send` nodes via `SendNoCC` + tracked task; `spawn`/`map` nodes via `StartSpawn()`
- [ ] Completion correlation via tasks/receipts; outcome extraction and edge routing; `condition` nodes via `EvaluateConditions()`
- [ ] Cancellation and node timeout (reuse task expiry); failed-edge default routing
- [ ] Lifecycle events per node/edge transition
- [ ] Unit tests: linear run, fan-out/join, quorum, capped loop exhaustion, cancel mid-run

### Phase 4: Control CLI and human gates

- [ ] `muxcode graph run <template>|--file` with intent arg interpolation into node messages
- [ ] `graph cancel`, `graph retry --from <node>` (upstream results preserved)
- [ ] `wait_human`: pending-approval marker + edit notification + `graph approve` release; `wait_event` on bus events
- [ ] Verify graph-originated sends pass dedup/relay-suppression and delivery-ack unmodified
- [ ] Unit tests: retry-from semantics, approval release, gate enforcement end to end

### Phase 5: Observability

- [ ] Console/TUI run view: node grid with Dracula state colors, active edges, run header (id, template, elapsed)
- [ ] `graph status --json` for scripting
- [ ] Docs: `docs/architecture.md` control-plane section, `docs/agent-bus.md` CLI reference, backlog cross-links

### Phase 6: Integration test

- [ ] Create `scripts/test-graph-orchestrator.sh` (requires running muxcode session)
- [ ] Test: `graph validate` rejects an uncapped cycle and an ungated commit node; accepts all built-in templates
- [ ] Test: run a 3-node linear graph (send→condition→send) → verify node statuses reach `done` and lifecycle events recorded
- [ ] Test: run a fan-out/join graph (map → 2 spawns → join all) → verify barrier held until both workers completed
- [ ] Test: kill and restart the daemon mid-run → verify the run resumes and completes from persisted state
- [ ] Test: `graph retry --from <node>` re-executes only downstream nodes
- [ ] Run the script and verify all checks pass

## Open questions

- **Outcome fidelity on non-hook providers** — hook providers give deterministic exit codes; OpenCode/Codex outcomes are inferred. Does the executor demand hook-grade outcomes for `failure`-routed edges, or accept `unknown` with an explicit `on_unknown` edge? Interacts with [`opencode-plugin-hook-bridge`](./MUX-011-opencode-plugin-hook-bridge.md).
- **Who authors DAGs in v1** — hand-authored JSON + built-in templates only, or also an LLM planner (graph agent / edit) that compiles intent → DAG? Proposal: templates-only first; planner is a later, separate spec.
- **Story-lifecycle template scope** — the full `story-lifecycle` skill includes Jira transitions and PR creation, all behind authority gates; the template must model those as `wait_human`-gated nodes, which may make it more documentation than automation until authorities are opted in.
- **Concurrency limits** — per-run and global caps on simultaneously running nodes (map fan-out could spawn many worktrees); likely `MUXCODE_GRAPH_MAX_PARALLEL`.
- **Workflow SM interplay** — should the SM observe graph runs (e.g. surface "graph coding-pr: 4/9 nodes") or stay fully independent?

## Sources

- `docs/architecture.md`, `docs/hooks.md`
- `docs/requirements/completed/{conditional-chains,workflow-state-machine,agent-spawn,spawn-worktrees,agent-mode,transactional-messaging-bus}.md`
- `docs/requirements/backlog/MUX-016-research-dual-provider.md`
- `tools/muxcode/bus/{profile,workflow,spawn,subscribe,task}.go`

## Provenance

Filed by the plan agent on 2026-08-18 from a user-provided analysis of what a graph-agent orchestrator would add to muxcode. Subsumes the earlier "Pipeline definitions" backlog idea.

## Status

Backlog
