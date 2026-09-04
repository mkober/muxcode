# MUX-148: A Node Outcome Reads "a Command Ran" as "the Task Was Done"

A graph `send` node whose agent **declines** the task is recorded as
**`outcome=success`** and routed onward, because outcome derivation reads the newest successful
shell command the agent ran — and an agent inspecting state in order to refuse produces
byte-for-byte the same evidence as an agent doing the work.

Observed live 2026-09-03 by the edit agent and independently flagged by the commit agent in the
same run.

## Context

### Observed

Run `1788457453-commit-pr-review-loop-9fa65bca`, template `commit-pr-review-loop`, node `d`
(`send` → `commit:comment`, *"Reply to the PR comments"*).

The commit agent **declined**:

> Cannot post PR replies yet: node c's fix (…617 insertions) is uncommitted and unpushed — this
> template has no commit node between c and d, so there's nothing to cite as a fix sha yet. Not
> committing on my own initiative under a comment action.

The executor recorded `d` as **`outcome=success`** and routed on to `close-gate`. **The PR replies
were never posted.** The commit agent noticed ~5 minutes later and posted them by hand:

> graph node d earlier marked success from my decline reply
> (unknown-outcome-falls-back-to-success gap), so it silently skipped the real reply-posting step —
> done now for real.

Corroborated independently: a `muxcode graph status` run during the incident showed
`c … outcome=unknown` and `d … outcome=success`, with `close-gate` already `done`.

### Mechanism — verified in code

| Step | Code | Behaviour |
|------|------|-----------|
| Outcome derivation | `deriveSendOutcome` (`bus/graph_exec.go:1162`) | Returns `failure` only if the response message's action is literally `error`; otherwise defers to the newest authoritative console row |
| Row selection | `latestAuthoritativeRow` (`:1182`) | Walks console history backwards, skipping rows older than dispatch, `SourceBusResponse` rows, and unknown/empty outcomes — returns the **newest row with a real exit code** |
| Consequence | — | While composing its refusal the agent ran read-only `gh`/`git` commands. Those rows carry **exit 0 → `OutcomeSuccess`**, so the provenance doctrine proved *"a command ran successfully"* and the router read it as *"the node did its job"* |

**The evidence an agent produces when it declines a task while inspecting state is
indistinguishable from the evidence it produces when it performs the task.** Recency plus exit
code cannot separate them.

### Why the existing unverified hold does not catch it

The hold (`graph_exec.go:1276`) fires only on **`OutcomeUnknown`**: a node with no matching edge
and an unknown outcome is parked for approval rather than assumed successful, unless every
successor is a human gate or the hold was already released. This path produces a **confident
`success`**, so no hold is raised, no human is asked, and no lifecycle warning is emitted.

**Strictly worse than the unknown case the hold was built for** — unknown at least surfaces as
uncertain and parks for a person; this produces a **false green**.

> **Provenance correction.** This hold is commonly called "the MUX-136 hold" because it landed in
> commit `16f2027` (*"MUX-136 Verify actor identity and attribute gate approval/graph
> provenance"*). It is **not MUX-136 work**: MUX-136's spec never mentions it, and **no spec in
> `docs/requirements/` describes it at all**. Every commit on that branch carries the `MUX-136`
> prefix regardless of subject, so `git log` by prefix misattributes it. The hold is real,
> shipped, and unspecified.

### Blast radius

Any `send` node whose agent declines, partially completes, or errors **in prose** after having run
any successful shell command — which is most agents, since inspecting state is how they decide to
decline. Most dangerous **immediately upstream of a mutation**.

In this run it advanced to `close-gate` on the false signal. **Only two accidents prevented a spec
close-out on it**: the `spec-complete` guard, and the session happening to have no active spec.
Neither is a guarantee — the guard checks the spec's own completeness, not whether the upstream
node did its job.

### Defect 2 — the template puts node `d` in an impossible position

`commit-pr-review-loop` has **no commit node between `c` and `d`** (verified,
`bus/graph_templates.go:80-81`):

```
… → gate2 → c (edit: "Address the PR review comments")
          → d (commit:comment: "Reply to the PR comments")
          → close-gate → …
```

`c` makes changes; `d` is asked to reply to review comments citing a fix — but nothing has
committed `c`'s work, so **there is no sha to cite**. A run in which `c` changes anything leaves
`d` structurally unable to succeed.

**The commit agent's decline was correct behaviour.** The template, not the agent, was wrong.
Defect 1 then converted that correct refusal into a false green — the two compounded, but each is
independently reachable and either could be fixed alone.

## Requirements

### Acceptance criteria

- [ ] A node whose agent **declines** the task is not recorded as `success`
- [ ] A node that **genuinely succeeds** is still recorded as `success` — **negative control: a fix that holds everything is not a fix**
- [ ] The distinction does **not** rely on parsing prose
- [ ] A node that cannot be tied to its dispatched work surfaces as a hold or failure, never as a silent success
- [ ] Whatever signal is chosen degrades safely for non-hook providers, which infer outcomes and cannot be assumed to emit it
- [ ] `commit-pr-review-loop` can complete a run in which `c` made changes
- [ ] A lifecycle event records any node whose outcome could not be positively established
- [ ] `bash scripts/test-node-outcome-attribution.sh` passes

### Technical approach — options, deliberately not yet chosen

| # | Option | Cost | Risk |
|---|--------|------|------|
| 1 | **Correlate the authoritative row with the dispatched work** — match on command shape or action, not merely recency and exit code | Medium | Correlation heuristics can drift from what agents actually run |
| 2 | **Structured decline** — a response field the executor maps to failure or hold | Low | Only as good as agent discipline; **prose parsing is not acceptable** |
| 3 | **Require positive evidence** for nodes whose success matters — the literal-token pattern | **Lowest** | Per-node, not general; every new node must remember to opt in |
| 4 | **Widen the hold** — a node whose authoritative row cannot be tied to the dispatched work holds rather than routes | Medium | Risks holding on legitimate successes; needs the negative control above |

**Option 3 has precedent in this very template**: `verify-pr` already demands *"Your reply MUST
contain the literal token `PR-CONFIRMED`"* and gates on it with a `pr-check` condition node
(`graph_templates.go:76-77`). **The template author already hit this problem once and solved it
locally for one node** — which is evidence both that the pattern works and that a per-node fix
does not generalise on its own.

Options 1 and 4 are the general fixes; 3 is the cheap immediate mitigation. They are not exclusive
— 3 can ship first for the mutation-adjacent nodes while 1 or 4 is built.

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/graph_exec.go` | `deriveSendOutcome:1162`, `latestAuthoritativeRow:1182`, unknown-hold routing `:1276` |
| `tools/muxcode/bus/graph_templates.go` | `commit-pr-review-loop:69-97` — the `c`→`d` gap and the `verify-pr` token precedent |
| `tools/muxcode/bus/console.go` | `ConsoleEntry`, `SourceBusResponse`, how outcome rows are written |
| `tools/muxcode/bus/graph_run.go` | `TransitionGraphNode`, node status persistence |
| `tools/muxcode/bus/lifecycle.go` | `LogLifecycle` for the unestablished-outcome event |
| `scripts/test-graph-orchestrator.sh` | Existing graph integration harness to extend or model on |

## Implementation

### Phase 1: Establish the boundary

- [ ] Enumerate every path that can set a `send` node's outcome, and what evidence each trusts
- [ ] Determine how often a declining agent emits a successful command row (sample real console history)
- [ ] Confirm whether non-hook providers (OpenCode/Codex) can emit any positive signal at all
- [ ] Decide whether `deriveSendOutcome`'s `action == "error"` check can be widened without prose parsing
- [ ] Record findings here before choosing an option

### Phase 2: Choose and record the fix

- [ ] Weigh options 1–4 against the Phase 1 findings
- [ ] Choose a general mechanism and, if different, a cheap immediate mitigation
- [ ] Confirm the choice satisfies the "genuine success still succeeds" criterion by construction
- [ ] Record the decision and rationale in this spec

### Phase 3: Implement outcome attribution

- [ ] Implement the chosen mechanism with unit tests
- [ ] **Negative control test:** a node that genuinely succeeded still routes as success
- [ ] Emit a lifecycle event when a node's outcome cannot be positively established
- [ ] Ensure the unknown-hold and the new path do not double-hold the same node

### Phase 4: Fix the template gap

- [ ] Add a commit step between `c` and `d` in `commit-pr-review-loop`, or restructure so `d` has a citable sha
- [ ] Confirm the new node is gated per the commit-authority rules (a commit node must be downstream of a `wait_human`)
- [ ] Verify `graph validate` still passes for the amended template
- [ ] Check the other six builtin templates for the same shape — a node asked to cite work that nothing has committed

### Phase 5: Integration test

- [ ] Create `scripts/test-node-outcome-attribution.sh` (hermetic; scratch bus, tmux session and daemon)
- [ ] Test: a scratch node whose agent **declines after running a successful read-only command** does **not** route as success
- [ ] **Negative control:** a node whose agent genuinely completes the task **does** route as success
- [ ] Test: the declining node emits the lifecycle event and holds rather than advancing
- [ ] Test: a `commit-pr-review-loop`-shaped run in which `c` changes files reaches `d` with a citable commit
- [ ] Test: no prose parsing is involved — a decline worded differently is still caught
- [ ] Coverage floor keeps a skipped section from reporting green
- [ ] Run the script and verify all checks pass

## Open decisions

### Decision 1 — general fix, cheap fix, or both?

Option 3 could ship today for mutation-adjacent nodes; options 1 and 4 take longer but cover nodes
nobody remembered to annotate. **Recommendation: both** — token-gate the nodes immediately
upstream of mutations now, and build the general correlation behind it. Shipping only option 3
leaves the class open for every future node.

### Decision 2 — what happens to a node that cannot be attributed?

Hold for a human, or fail the run? Holding matches the existing unknown behaviour and is
recoverable; failing is louder but turns any attribution gap into a broken run. **Recommendation:
hold**, consistent with `graph_exec.go:1276`.

### Decision 3 — should the unverified hold be specified retroactively?

It is shipped, load-bearing, and **described by no spec**. Whether to write it up here, in its own
spec, or in the architecture doc is a judgement call — but leaving working safety machinery
undocumented is how it gets removed by a later tidy-up.

## Out of scope

- **Whether agents should decline at all.** The decline here was correct; this spec is about the
  executor believing the wrong thing afterwards.
- **The `spec-complete` guard.** It behaved correctly and is not implicated — it simply is not a
  substitute for upstream node attribution.

## Status

Backlog
