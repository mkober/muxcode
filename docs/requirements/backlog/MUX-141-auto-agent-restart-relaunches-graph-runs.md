# MUX-141: Auto Agent Restarts Relaunch Autonomous Graph Runs

**Tracking:** [mkober/muxcode#67](https://github.com/mkober/muxcode/issues/67)

> **Compounds with [`MUX-144`](./MUX-144-wait-human-gate-openable-by-any-agent.md) — read them
> together.** This spec supplies the *source* of unrequested runs; MUX-144 supplies the reason one can
> reach a push. Alone, this spec's runs stop harmlessly at a `wait_human` gate — that gate is the
> mitigation this spec has been leaning on. MUX-144 establishes (live, 2026-09-03) that the gate is
> **openable by any agent and unaudited**, so the mitigation does not hold: the two together form an
> unattended path from an external process exit to a pushed branch and an open PR. Neither subsumes the
> other, and fixing this one alone does not close that path.

## Context

Every launch of the `auto` agent — **including a restore or a daemon auto-restart, not just a fresh
user-initiated start** — seeds its inbox with an actionable `startup` request. The autonomous-agent
definition treats that request as a mandate to begin work immediately, and its graph preference turns
"begin work" into launching a `spec-to-pr` run that drives the branch's spec to its next phase and
stops at a commit gate.

The result is that **restarting a session performs work**. Observed 2026-09-02 in a live session:
run `1788368612` drove Phase 1, then run `1788373205` auto-advanced to Phase 2 — one run per restore,
each continuing the spec, none requested. The user was mid-session doing careful manual
curate-each-commit work and had to cancel spurious runs as they appeared.

### Mechanism (verified 2026-09-02 against the tree at `6a05bc8`)

| Step | Where |
|------|-------|
| `LaunchAgent` calls `PreLaunchSetup` on **every** launch — no fresh-vs-restart distinction exists | `bus/launch.go:850` |
| For `auto` it seeds a **request-type** `startup` message: *"Agent started — search Jira for available stories and present them to the user for selection."* | `bus/launch.go:747-758` |
| Request-type is deliberate so the daemon **re-wakes the agent until it consumes the message** — the message cannot be quietly ignored | `bus/launch.go:739-742`, `HasActionableMessages` |
| The agent definition reads `startup` as: *"do NOT wait for further instructions — immediately search Jira and present the stories. This is your primary entry point."* | `agents/autonomous-agent.md:19` |
| …and prefers graphs: *"`story-to-spec` then `spec-to-pr` cover most of this agent's arc"* | `agents/autonomous-agent.md:126` |

Nothing here is misbehaving in isolation. `PreLaunchSetup` is right that an agent launching into an
empty inbox would sit idle forever; the request-type choice is right for exactly the dropped-keystroke
reason its comment documents; and the definition is right that an autonomous agent should not idle
waiting to be told to start. The defect is in the composition: **no layer distinguishes "the user
started this agent" from "this agent came back".**

### The seed message and the definition disagree

The payload says *"present them to the user for selection"* — a human-in-the-loop menu. The definition
says *"do NOT wait for further instructions"* and prefers whole-arc graph templates. So the launcher
asks for a list and the agent reads a mandate to drive a spec to its next commit gate. Whichever is
intended, both cannot be, and today the definition wins because it is the thing actually reasoning.

This matters beyond wording: if the payload's reading were the operative one, a spurious restart would
cost a Jira search and a printed list. Under the definition's reading it costs a graph run.

### Why this is a defect, not a configuration preference

- [ ] **A recovery event causes work.** Auto-restart exists to restore availability; it is not a request for the agent to advance a spec. Every other role's `startup` restores *context* — only `auto` interprets it as a task
- [ ] **It is unbounded.** The message is re-seeded on every launch, and the daemon re-wakes until consumed, so the behaviour repeats indefinitely across restores rather than firing once
- [ ] **It reaches a commit gate.** The runs drive toward `wait_human`; the user's stated risk is a run slipping through to an auto-approved commit on a tree they are hand-curating
- [ ] **It is invisible as a cause.** The user sees graph runs appearing and cancels them; nothing in the run or the lifecycle log says "this was triggered by a restart"

### Compounds with MUX-139

[MUX-139](./MUX-139-claude-agent-auto-resume.md) exists to make agents **come back more reliably**
after mass exits, and adds `edit`-and-worker coverage plus an operator "Restart Agents" control. Every
one of those paths is a launch. Shipping MUX-139 over this defect converts a machine-wide Claude exit
— a single external event — into **N spurious autonomous graph runs**, and the operator's own recovery
button becomes a work-triggering button.

**The two specs must not land in that order.** Either this fix precedes MUX-139, or MUX-139's restart
paths must suppress the auto startup task from the outset.

## Requirements

### Acceptance criteria

- [ ] A **restart or restore** of the `auto` agent does not begin autonomous work: no Jira sweep that leads into a run, and no graph launch
- [ ] A genuine **user-initiated** start still works exactly as today — the agent is useful without being told to begin
- [ ] The two cases are distinguished by an explicit signal carried into `PreLaunchSetup`, not inferred from timing, inbox contents or pane state (see [Decision 1](#decision-1-the-launcher-knows-why-it-is-launching-so-it-should-say-so))
- [ ] On a restart, `auto` restores context like every other role and then **idles**, reporting what it would have resumed
- [ ] The `startup` payload and the agent definition are reconciled so both describe the same behaviour — whichever is chosen, the disagreement in [The seed message and the definition disagree](#the-seed-message-and-the-definition-disagree) does not survive this spec
- [ ] A graph run launched by the auto agent records **what triggered it** (user request vs startup) in the run record and a lifecycle event, so a spurious run is diagnosable rather than merely cancellable
- [ ] `MUXCODE_AUTO_STARTUP_TASK=0` (or equivalent) disables the startup task entirely, for users driving commits by hand — the documented alternative to `agent-health --stop auto`, which currently costs the agent's availability to buy quiet
- [ ] Stopping the auto agent remains available and documented as the blunt instrument it is
- [ ] Docs: CLAUDE.md autonomous-agent bullet, [`docs/agents.md`](../../agents.md), [`docs/configuration.md`](../../configuration.md)

#### Decision 1: the launcher knows why it is launching, so it should say so

The fix belongs at the seam where the information exists. `LaunchAgent` is called from distinct
callers — first launch, `reload`, daemon auto-restart, mode cycling, and (with MUX-139) resume — and
each one knows which it is. The agent, reading an identical inbox message in all five cases, cannot
know and should not be asked to guess.

- [ ] `PreLaunchSetup` takes an explicit launch **reason**; the auto task message is seeded only for a user-initiated start
- [ ] Every existing caller passes its reason explicitly; **no default that silently means "user-initiated"** — an unset reason must be treated as a restart (the safe direction: the failure mode is an agent that waits, not one that acts)
- [ ] Pinned by test per caller, so a future call site cannot inherit work-triggering behaviour by omission

The alternative — having the agent detect its own restarts — was rejected: it is the same
"agent infers its own lifecycle from ambient state" shape as the pane-scrape delivery the
delivery-ack cutover replaced, and it would put the safety decision in the least reliable place.

### Technical approach

| Area | Change |
|------|--------|
| `bus/launch.go` | `PreLaunchSetup(role, session, cli, reason)`; auto task message gated on a user-initiated reason. Context-restoration startup still seeded for every role and every reason |
| `bus/launch.go`, `bus/reload.go`, `bus/mode.go`, `daemon/daemon.go` | Each call site passes its reason; restart/reload/resume are not user-initiated |
| `agents/autonomous-agent.md` | Reconcile with the payload; state that a restart restores context and idles |
| `bus/graph_run.go` | Record the trigger on the run record; lifecycle event names it |
| `docs/`, `CLAUDE.md` | The env opt-out and the restart semantics |

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/launch.go` | launch reason; auto task gating |
| `tools/muxcode/bus/reload.go`, `bus/mode.go` | reload and mode-cycle call sites |
| `tools/muxcode/daemon/daemon.go` | auto-restart call site |
| `agents/autonomous-agent.md` | startup semantics reconciled |
| `tools/muxcode/bus/graph_run.go` | run trigger provenance |
| `scripts/test-auto-startup-gating.sh` (new) | integration test |

## Implementation

### Phase 1: Launch reason plumbed through

- [ ] Add the launch reason to `PreLaunchSetup` and `LaunchAgent`
- [ ] Update every call site explicitly; unset is treated as a restart
- [ ] Tests: user-initiated seeds the auto task; restart, reload, mode-cycle and resume do not
- [ ] Test: **an omitted reason does not seed the task** (safe default pinned)

### Phase 2: Auto agent restart semantics

- [ ] `auto` receives the ordinary context-restoration startup on a restart
- [ ] Definition updated: on a restart, restore context, report what would have been resumed, then idle
- [ ] Payload and definition reconciled so both describe one behaviour
- [ ] `MUXCODE_AUTO_STARTUP_TASK=0` opt-out + test

### Phase 3: Run trigger provenance

- [ ] Graph runs record their trigger (user request vs startup vs graph edge)
- [ ] Lifecycle event names the trigger at launch
- [ ] `graph status` surfaces it, so a spurious run is explainable after the fact
- [ ] Tests: each trigger recorded and rendered

### Phase 4: Integration test

- [ ] Create `scripts/test-auto-startup-gating.sh` — hermetic: scratch bus, tmux session and daemon
- [ ] User-initiated launch: assert the auto task message **is** seeded
- [ ] Daemon auto-restart of `auto`: assert the task message is **not** seeded, a context-restoration startup **is**, and **no graph run is created**
- [ ] Reload and mode-cycle: same as restart
- [ ] **Negative control:** with the opt-out set, even a user-initiated launch seeds no task
- [ ] **Regression control:** a genuine user-initiated launch still reaches the Jira-search behaviour, so the fix cannot be "disable the agent"
- [ ] Assert an omitted reason behaves as a restart
- [ ] Coverage floor set to the maximum achievable count so a skipped section cannot report green
- [ ] Run the script and confirm all checks pass

## Related

| Spec | Relationship |
|------|--------------|
| [MUX-139](./MUX-139-claude-agent-auto-resume.md) | **Ordering constraint** — MUX-139 multiplies restarts, turning one external exit into N spurious runs; this must land first or MUX-139 must suppress the task itself |
| [MUX-126](./MUX-126-edit-resume-aware-auto-restart.md) | Same family: a restart that does not faithfully reproduce the pre-restart state |
| [MUX-112](./MUX-112-idle-task-rescue-closes-live-work.md) | Same class of harm — automation acting on state it has misread, against work already in flight |

## Status

Backlog
