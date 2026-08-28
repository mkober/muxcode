# Keep the Edit Agent Free While a Graph Runs

Route graph-driven implementation work to the `auto` agent instead of the `edit` agent (`code` after
[MUX-118](./MUX-118-rename-edit-role-to-code.md)), so the user can keep working with edit
interactively while a graph executes.

The goal is **interactive availability of the edit agent during a graph run**. That framing matters,
because measuring the current behaviour showed the obvious implementation — redirect nodes whose
`role` is `edit` — would not achieve it.

Tracking: _(no GitHub issue yet)_

## Context

### What actually occupies edit during a graph run

Measured 2026-08-28 against `bus/graph_templates.go` and `bus/graph_exec.go`. Five builtin nodes
target `edit`, but **they do not behave alike**:

| Node | Template | Type | Occupies the interactive edit agent? |
|------|----------|------|--------------------------------------|
| `implement` | `req-code-pr` | `spawn` | ❌ No — isolated |
| `fix` | `req-code-pr` | `spawn` | ❌ No — isolated |
| `implement` | `story-lifecycle` | `spawn` | ❌ No — isolated |
| `fix` | `story-lifecycle` | `spawn` | ❌ No — isolated |
| `c` | `commit-pr-review-loop` | **`send`** | ✅ **Yes — this one blocks** |

`spawn` nodes call `StartSpawn()` (`bus/spawn.go:112`), which creates a **new tmux window**
`spawn-<8hex>` with its own git worktree. `Node.Role` there selects the agent *definition* to launch,
not a running pane. Four of the five "edit steps" therefore already run isolated and never touch the
user's edit pane.

Only the `send` node lands a message in `inbox/edit.jsonl`, where the daemon wakes the interactive
agent and the user loses it for the duration.

### Two channels that wake edit and are not "edit steps" at all

This is the finding that reframes the request. Even if **every** `role: "edit"` node were redirected,
edit would still be interrupted:

| Channel | Site | Behaviour |
|---------|------|-----------|
| Gate approval | `bus/graph_exec.go:347` | `NewMessage(graphSender, "edit", …, "graph-approval")` + `Notify(session, "edit")` — **hardcoded** `"edit"` |
| Run completion | `bus/graph_exec.go:690` | `NewMessage(graphSender, "edit", …, "graph-complete")` + `Notify(session, "edit")` — **hardcoded** `"edit"` |
| Auto-CC | `bus/inbox.go:22` | Messages from build/test/review to non-edit roles are copied into edit's inbox (rate-limited, `autoCCWindowSecs = 60`) |

The graph control plane treats edit as **the human's proxy** — the place where approvals and results
surface. That is deliberate and mostly correct: a `wait_human` gate must reach a human. But it means
"stop the graph from using edit" and "keep the graph's approvals reaching the user" are in tension,
and the spec has to resolve it rather than assume it away.

### Prerequisite: the `auto` agent is not running

`LaunchSession()` (`bus/launcher.go:215`) writes the mode-cycle state registering `auto` at index 1
with `HoldWindow: "auto"` — but **never creates the window or launches the agent**. `modeSwitchTo()`
(`bus/mode.go:204-213`) creates it lazily: *"On first switch to a non-default agent, create the
holding window and launch the agent."*

So in a fresh session, a graph node dispatching to `auto` finds no agent. Messages queue in
`inbox/auto.jsonl` with no consumer, and the node hangs until its timeout. **Any routing change must
ensure auto is running first.**

### The good news: concurrency genuinely works

`modeSwitchTo()` uses tmux **`swap-window`**, not `swap-pane` — the code comment states it plainly:
*"Uses swap-window instead of swap-pane so each window keeps its own panes."* Cycling F2 exchanges
window **indices**; it does not stop or replace a process.

`WindowForRole("auto")` returns `"auto"` (`modeRoles` maps auto → its own window), so `PaneTarget`
resolves auto independently of edit. Both agents run continuously, each in its own window, whichever
is currently displayed at index 2. **The user's goal is architecturally achievable** — this is not a
case where two agents contend for one pane.

### Interactions to respect

- **Commit authority**: `commitAuthorityDefault = []string{"edit"}` (`bus/commit_authority.go:29`).
  `auto` may **not** request git mutations by default. The opt-in is documented in the same file:
  `MUXCODE_COMMIT_AUTHORITY_ROLES=edit,auto`. Moving implementation work to auto without deciding
  this leaves auto unable to complete work that ends in a commit.
- **Auto-clear**: `autoClearExcluded = {"edit": true, "auto": true}` (`bus/clear.go:23`) — auto is
  already excluded, so its context will not be cleared mid-graph. No change needed.
- **Agent definition fit**: `AgentFileName("auto")` → `agents/autonomous-agent.md`, which is the
  *story-lifecycle* autonomous agent with its own driving loop. Whether that prompt is the right
  worker for a graph-dispatched step is an open question below.

## Open decisions

- [ ] **Reuse `auto`, or add a dedicated graph-worker role?** `auto` carries story-lifecycle
      framing (it drives its own loop, selects stories, and is written to commit by design). A graph
      node wants a worker that does one scoped task and reports. Reusing auto risks the two framings
      fighting; a new role costs a window and a definition.
- [ ] **Where do `graph-approval` and `graph-complete` go?** Options: keep them on edit (approvals
      are for the human — arguably correct, but it is exactly what interrupts), make the recipient
      configurable per run, or route to the control pane's Pending Gates surface
      ([MUX-109](../completed/MUX-109-prompt-mode-graph-control-pane.md)) and notify edit only on
      terminal outcomes.
- [ ] **Is this per-run or global?** A `--worker <role>` flag on `graph run`, a field in the graph
      definition, or a session-wide default — each has different blast radius.
- [ ] **Does auto get commit authority?** Required if graph work must reach a commit; declining it
      means graphs stop at the gate and edit finishes the job.
- [ ] **Should the 4 `spawn` nodes change at all?** They already do not block edit. Changing them
      is optional and mostly about which agent *definition* does implementation work.

## Requirements

### Acceptance criteria

- [ ] With a graph running implementation work, the user can send the edit agent an unrelated
      request and get a normal response — **verified by measurement, not by inspection**
- [ ] The `commit-pr-review-loop` `send` node no longer occupies the interactive edit agent
- [ ] `auto` (or the chosen worker role) is **running and consuming** before any node dispatches to
      it; a fresh session with no prior F2 cycle does not hang the run
- [ ] Graph approvals still reach a human — a `wait_human` gate is never silently unreachable
- [ ] Gate approval and run completion notifications behave per the Phase 1 decision, and the
      hardcoded `"edit"` at `graph_exec.go:347,690` is replaced by that decision, not left in place
- [ ] Auto-CC does not reintroduce the interruption through the back door
- [ ] Commit authority is explicit: either the worker role is opted in, or graphs are documented as
      stopping at the gate for edit to finish
- [ ] Existing graph behaviour is unchanged when the new routing is not requested (negative control)
- [ ] `go vet ./...` and `go test ./...` green in both modules

### Technical approach

The routing change itself is small — `Node.Role` already parameterizes the target, and
`graphSpawnFn`/the send dispatch in `bus/graph_exec.go` both honour it. The work is in the
surrounding guarantees: worker liveness, notification routing, and authority.

Suggested shape, subject to the Phase 1 decisions:

- A worker-role resolution point in the executor, so `role: "edit"` in a definition resolves to the
  configured worker at dispatch time. This keeps builtin templates readable and avoids editing five
  node definitions plus every user graph.
- An explicit **liveness precondition** before dispatch — the same architectural lesson as
  [MUX-114](../completed/MUX-114-close-spec-node-has-no-completion-check.md): a daemon-side check
  beats an instruction to the receiving agent, because there is no agent to receive it.
- Notification recipient becomes a run-level property rather than a literal.

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/graph_exec.go` | Dispatch, spawn/send routing, hardcoded `"edit"` notifications (347, 690) |
| `tools/muxcode/bus/graph_templates.go` | The 5 `role: "edit"` builtin nodes |
| `tools/muxcode/bus/graph.go` | `Node.Role`, validation, guards |
| `tools/muxcode/bus/mode.go` | Hold windows, lazy agent creation (`modeCreateAgent`) |
| `tools/muxcode/bus/launcher.go` | Mode-cycle init; where eager auto launch would go |
| `tools/muxcode/bus/commit_authority.go` | Worker-role commit authority |
| `tools/muxcode/bus/inbox.go` | Auto-CC into edit |
| `tools/muxcode/cmd/graph.go` | `graph run` flags if routing is per-run |

## Implementation

### Phase 1: Decide routing and notification policy

- [ ] Resolve the five open decisions above with the user
- [ ] Re-measure which nodes reach edit (this spec's table ages with the templates)
- [ ] Record the chosen notification policy in `docs/architecture.md`

### Phase 2: Worker liveness

- [ ] Ensure the worker role is running before dispatch — eager launch, or a dispatch-time
      precondition that starts it
- [ ] Verify a fresh session (no prior F2 cycle) can run a graph end to end
- [ ] Confirm `swap-window` cycling still leaves both agents running

### Phase 3: Routing

- [ ] Resolve `role: "edit"` to the configured worker at dispatch time
- [ ] Wire the per-run/global switch per the Phase 1 decision
- [ ] Leave `spawn` nodes' behaviour deliberate and documented either way

### Phase 4: Notifications and CC

- [ ] Replace the hardcoded `"edit"` recipients per the Phase 1 decision
- [ ] Ensure `wait_human` gates remain reachable by a human
- [ ] Check auto-CC does not reintroduce the interruption

### Phase 5: Authority and docs

- [ ] Settle worker commit authority; pin the resulting default by test
- [ ] `CLAUDE.md`, `docs/architecture.md` (graph section), `docs/agent-bus.md` (`muxcode graph`),
      `docs/agents.md` (auto role)

### Phase 6: Integration test

- [ ] Create `scripts/test-graph-worker-routing.sh` (hermetic: scratch bus + tmux + daemon)
- [ ] **The headline test**: start a graph that runs implementation work, then send edit an
      unrelated request and assert a normal response while the run is still in flight — this is the
      user-visible goal and must be asserted directly, not inferred from routing
- [ ] Test: a fresh session with no prior F2 cycle runs a graph to completion (worker liveness)
- [ ] Test: `wait_human` gate still surfaces to a human and `graph approve` releases it
- [ ] **Negative control**: with the new routing not requested, dispatch targets are unchanged — a
      routing switch that always redirects would pass a one-sided test
- [ ] **Negative control**: edit is genuinely reachable *because* of the change — assert the
      pre-change behaviour blocks, so the test cannot pass vacuously on a graph that never dispatched
- [ ] Test: worker commit request is admitted or denied exactly per the Phase 5 decision
- [ ] Coverage floor so a skipped run cannot report green
- [ ] Run the script and verify all checks pass

## Risks

| Risk | Why it matters | Mitigation |
|------|----------------|------------|
| Redirecting only `role: "edit"` nodes | Does **not** meet the goal — approvals, completions, and auto-CC still wake edit | Phase 4 is not optional |
| Worker not running at dispatch | Node hangs to timeout with messages queued for nobody | Phase 2 liveness precondition, daemon-side |
| `wait_human` becomes unreachable | A gate nobody sees is a stalled run, and the gate rule is a safety mechanism | Explicit criterion + integration test |
| Worker lacks commit authority | Graph work that ends in a commit cannot finish | Decide in Phase 1; pin by test |
| `auto`'s story-lifecycle framing fights graph dispatch | Auto is written to drive its own loop and commit by design | Open decision: dedicated worker role |
| Vacuous integration pass | A graph that never dispatched trivially leaves edit free | Negative control asserting pre-change behaviour blocks |

## Status

Backlog
