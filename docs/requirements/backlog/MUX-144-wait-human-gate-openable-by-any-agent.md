# A `wait_human` Gate Is Openable by Any Agent, Unaudited

A `wait_human` gate released **four seconds** after it opened, on a run nobody requested, and
dispatched a `commit` node whose message was *"Stage all unstaged files, commit, push, and create a
PR"* — against a branch eight commits ahead of `origin/main` with no remote branch, so the push would
have created the branch and opened a PR.

No human approved it. Nothing recorded that no human approved it.

`wait_human` is not a human gate. It is a gate **any agent can open**, and neither opening it nor
creating the run behind it leaves an audit trail.

Tracking: _(no GitHub issue yet)_

## Context

### The incident (live, 2026-09-03 00:48)

Observed **in this session**, four minutes before this spec was filed. Run
`1788410915-commit-pr-review-loop-46d7a0f0` surfaced in the edit agent's inbox as a gate-approval
notification. From the lifecycle log — **read directly by plan**, not relayed:

```
00:48:37  graph-node-start    gate1 (wait_human)
00:48:37  graph-gate-pending  gate1
00:48:41  graph-node-done     gate1 -> success        <-- 4 seconds
00:48:41  graph-node-start    a (send)
00:49:06  graph-run-canceled                          <-- edit cancelled it
```

Node `a`, read from the run's frozen `graph.json`:

```json
{"id": "a", "type": "send", "role": "commit", "action": "commit",
 "message": "Stage all unstaged files, commit, push, and create a PR"}
```

The approval marker exists on disk and contains **only a timestamp**:

```json
{"approved_at":1788410921}
```

**Outcome: no damage.** `HEAD` is unchanged at `8faa8c5`, nothing is staged, no push, no PR, and the
branch still has no upstream. The commit agent received the dispatch and held off pending
verification. **Record that plainly: the safety margin was one agent's judgement, not a control.** The
same dispatch to an agent that simply complied would have pushed.

### Attribution is not possible, and that is the finding

Investigation **could not determine who created the run or who approved it.** The `auto` agent is
alive and matches the shape [`MUX-141`](./MUX-141-auto-agent-restart-relaunches-graph-runs.md)
describes, but `auto` has no tmux window to scrape and nothing is logged, so **naming it would be a
guess and this spec does not make one.**

That inability *is* the defect, not a gap in the investigation: a control plane that can mutate git
keeps no record of who operated it.

### The three defects

Each was verified by plan against this repo's source. The provenance column says how.

#### Defect A — `graph approve` has no authority check

| Fact | How established |
|------|-----------------|
| `approve` calls `bus.ApproveGraphGate` directly, with nothing between the CLI and the write | **Verified** — `cmd/graph.go:92-101` |
| `cmd/graph.go` contains **no** authority check of any kind | **Verified** — `grep -cE "Authority\|AGENT_ROLE\|BusRole\|authoriz" cmd/graph.go` returns `0` |
| Every agent can therefore run it — the `Bash(muxcode *)` permission glob is granted to all roles | **Verified** — tool profiles |

The gate the validator insists on is openable by the very agents it exists to gate, including an
autonomous one approving its own run.

#### Defect B — approval and run creation are entirely unaudited

| Fact | How established |
|------|-----------------|
| `ApproveGraphGate` is `ReadNodeStatus` + `MkdirAll` + `atomicWriteJSON` — **no `LogLifecycle` call** | **Verified** — `bus/graph_exec.go:80-89` |
| The marker records no approver identity, only `approved_at` | **Verified** — read from disk (above) |
| No `graph-gate-approved` event exists; the log jumps `graph-gate-pending` → `graph-node-done` | **Verified** — lifecycle log above |
| Run **creation** logs nothing either — the first event of the incident is `graph-node-start` | **Verified** — `lifecycle show \| grep -cE "graph-run-created\|graph-run-start\|graph-gate-approved"` returns `0` |

#### Defect C — the runtime backstop is a no-op for graph sends

`CLAUDE.md` currently claims:

> **Authority gates are not bypassable**: … `CheckCommitAuthority`/`CheckAtlassianAuthority` remain the
> runtime backstop — a graph cannot launder an action around the rules that govern it.

| Fact | How established |
|------|-----------------|
| Graph sends carry `From = "daemon"` | **Verified** — `const graphSender = "daemon"`, `bus/graph_exec.go:28`, used at `:539` |
| `CheckCommitAuthority` normalizes the sender before checking it | **Verified** — `from = NormalizeBusRole(from)`, `bus/commit_authority.go:90` |
| `NormalizeBusRole("daemon")` returns `"edit"` | **Verified** — `bus/config.go:618-620` |
| `edit` is the default commit authority | **Verified** — `commitAuthorityDefault = []string{"edit"}`, `bus/commit_authority.go:29` |

A graph commit dispatch passes the authority check **as if it were the edit agent**. The backstop
cannot distinguish *"the user's agent asked"* from *"a graph node asked"*.

### Why the two controls do not add up to defence in depth

The validator half of the claim **does** hold: `validateGates` refuses any graph where a git or
Atlassian mutation is reachable without crossing a `wait_human` node
(`bus/graph.go:567-577` — *"node %q fires a git/Atlassian mutation without an upstream wait_human
gate"*). That check is sound and did its job here: the run **had** a gate.

The two mechanisms were meant to be independent — structural (a gate must exist) and runtime (the
sender must hold authority). They are not. The runtime check is a **no-op for every graph send**
because of the daemon→edit normalization, so the entire weight of the guarantee rests on the gate
alone — and the gate is unguarded. Two controls that look independent on paper collapse to **one
control with no lock on it.**

So the CLAUDE.md paragraph is **false as written**, and correcting it is part of this work — not as
tidying, but because a documented safety property nobody can rely on is worse than an absent one.

### The full laundering path

| Step | Who can do it | What stops them | What is recorded |
|------|---------------|-----------------|------------------|
| 1. `muxcode graph run commit-pr-review-loop` | any agent | nothing | nothing |
| 2. `muxcode graph approve <run> gate1` | any agent | nothing | a bare timestamp |
| 3. node `a` sends `From="daemon"` → normalized to `edit` | the daemon | `CheckCommitAuthority` **passes** | the dispatch |
| 4. stage → commit → push → PR | commit agent | *the agent's own judgement* | the commit |

No step requires a human. No step records that one was absent.

### Relationship to existing specs

| Spec | Relationship |
|------|--------------|
| [`MUX-141`](./MUX-141-auto-agent-restart-relaunches-graph-runs.md) | **Compounding, neither subsumes the other.** MUX-141 supplies the *source* of unrequested runs (a restart relaunches autonomous work); this spec supplies the reason one can reach a push. MUX-141 alone yields spurious runs that **stop at a gate**; this alone makes gates openable. Together they are an unattended path from an external process exit to a PR. Cross-linked both ways. |
| [`MUX-132`](../completed/MUX-132-graph-retry-launders-gate-approval.md) | **Adjacent hole, and 132's fix is sound.** MUX-132 closed a *stale-marker reuse* path so a retried run demands a **fresh** approval. This spec is about a fresh approval **nobody human made**. 132 guards the step "is this approval current?"; nothing guards "is this approval human?" — the two are complementary, and 132 needs no revision. |
| [`MUX-142`](./MUX-142-spawn-worker-delegates-into-wrong-tree.md) | Shares the lesson that a control verified on one road is not verified on all of them. |

### Why it matters

Every other defect in the backlog produces a **wrong answer or corrupt local state** — recoverable
inside the repo, by the person who finds it. This one produces an **irreversible, externally visible
action**: a pushed branch and an opened PR, on a repository other people can see. A force-push is not
an undo, it is a second hazard.

It is also **live**: the incident is four minutes old at filing, and MUX-141 — its supply of
unrequested runs — is itself still open and firing.

### Second occurrence, and what it shows about the Atlassian path (2026-09-03 01:19)

A second unrequested run appeared 31 minutes after the first: `1788412762-story-to-spec-e8fd9e31`,
canceled after **9 seconds** with its `derive` node still `running` and every downstream node `skipped`.
Plan received that node's `story-read` dispatch directly. Two runs, two templates, both unrequested,
both canceled by hand — recorded here and in MUX-141 as live corroboration.

It also answers this spec's own open question *"scope to git, or all gated actions?"* — **with a
correction to the obvious guess.**

The naive expectation is that Atlassian writes share the commit bypass. **They do not**, and the reason
is an accident rather than a design:

| Fact | How established |
|------|-----------------|
| `atlassianAuthorityDefault = []string{"plan"}` | **Verified** — `bus/atlassian_authority.go:56` |
| `CheckAtlassianAuthority` normalizes the role, then checks it against that list | **Verified** — `:207-220` |
| A graph send normalizes `daemon` → `edit`, and `edit` is **not** the Atlassian authority | **Verified** — so a graph-dispatched Atlassian *write* would be **refused** |

So `CheckAtlassianAuthority` survives the normalization **only because `edit` happens not to hold that
authority.** Had the Atlassian authority still sat with `edit` — where it sat until recently — this
would be the commit bypass exactly.

**But the graph does not need to bypass it.** `story-to-spec`'s `jira-update` node is:

```json
{"id": "jira-update", "type": "send", "role": "plan", "action": "jira-write",
 "message": "The user approved the tracker update: ..."}
```

It dispatches to **`plan`, the role that legitimately holds the write authority**, behind an
`update-gate` (`wait_human`). Authority is never bypassed; the instruction is simply handed to the
agent that has it, carrying a **template-authored sentence asserting the user approved** — a claim no
user made, generated by the run itself.

Two consequences worth stating plainly:

1. **The gate is the only control**, and Defect A says any agent can open it. Once open, plan is told
   the user approved, by a message that cannot itself be distinguished from one that is true.
2. **The "relayed from edit" test is defeated by the same normalization.** Plan's scope rule is *write
   only on an explicit user-initiated request relayed from edit*. A graph dispatch carries
   `From = "daemon"`, and `NormalizeBusRole("daemon")` returns `"edit"` — so a mechanical check of
   "did this come from edit?" would **pass**. What refuses it today is plan's definition treating a
   bus message from another agent as never being user consent — a behavioural control, not a
   mechanical one.

This is the same shape as the git finding: the margin is an agent's judgement rather than a control.
Here plan declined — this dispatch arrived while this spec was being written, and no Jira write was
made.

## Requirements

### Acceptance criteria

- [ ] Approving a gate **requires authority**; the permitted roles are configurable and default to
      something narrower than "every agent"
- [ ] An autonomous agent **cannot approve a gate on a run it created**
- [ ] Gate approval emits a lifecycle event **naming the approver**, and the marker records approver
      identity, not just a timestamp
- [ ] Graph **run creation** emits a lifecycle event naming its creator
- [ ] A graph-dispatched git mutation is **distinguishable** from an edit-originated one at
      `CheckCommitAuthority`, and is judged on the gate's approval rather than on the normalized sender
- [ ] `CLAUDE.md`'s "Authority gates are not bypassable" paragraph is corrected to describe what is
      actually enforced
- [ ] **Negative control:** a genuine user approval at the CLI still releases the gate with no extra
      friction, and the run proceeds exactly as today
- [ ] **Negative control:** the daemon's own legitimate non-git sends are unaffected by any change to
      sender normalization

### Key files

| File | Relevance |
|------|-----------|
| `cmd/graph.go` | `approve` and `run` subcommands — where authority and audit are absent |
| `bus/graph_exec.go` | `ApproveGraphGate` (:80), `graphSender` (:28), send dispatch (:539) |
| `bus/commit_authority.go` | `CheckCommitAuthority` (:86) and the normalization that voids it for graphs |
| `bus/config.go` | `NormalizeBusRole` daemon→edit (:618) |
| `bus/graph.go` | `validateGates` (:567) — the half that works; must not regress |
| `bus/atlassian_authority.go` | Same bypass shape applies to Atlassian writes; verify and cover |
| `CLAUDE.md` | The false safety claim |

## Implementation

### Phase 1: Pin the bypass

- [ ] Write a failing test: a graph `send` node with `role: commit` passes `CheckCommitAuthority`
      today because the sender normalizes to `edit`
- [ ] Write a failing test: `ApproveGraphGate` succeeds with no caller identity and emits no lifecycle
      event
- [ ] Confirm the same bypass applies to `CheckAtlassianAuthority`, or record precisely why it does not
- [ ] Confirm `validateGates` still rejects an ungated commit node — the working half must not regress

### Phase 2: Authority and identity on approval

- [ ] Add an approver-authority check to `graph approve`, with a configurable role list
- [ ] Record approver identity in the marker; keep it backward-compatible with existing markers or
      migrate them deliberately
- [ ] Refuse self-approval: an agent may not approve a gate on a run it created
- [ ] Negative control: an authorized human approval path is unchanged

### Phase 3: Audit the control plane

- [ ] Emit `graph-run-created` naming the creator
- [ ] Emit `graph-gate-approved` naming the approver
- [ ] Verify an incident of this exact shape is now attributable from the lifecycle log alone

### Phase 4: Make the runtime backstop real

- [ ] Distinguish a graph-dispatched mutation from an edit-originated one at `CheckCommitAuthority`
- [ ] Judge it on the gate's recorded approval rather than on the normalized sender
- [ ] Negative control: the daemon's legitimate non-git sends still route correctly (the
      normalization exists so replies reach `edit` — do not break that)
- [ ] Correct the `CLAUDE.md` paragraph to describe what is enforced

### Phase 5: Integration test

- [ ] Create `scripts/test-gate-authority.sh` against a scratch bus, daemon and repo dir
- [ ] Test: an unauthorized role's `graph approve` is refused, the gate stays pending, the commit node
      never dispatches
- [ ] Test: an authorized approval releases the gate and the run proceeds
- [ ] Test: self-approval by the run's creator is refused
- [ ] Test: a graph commit dispatch with no valid human approval is refused at `CheckCommitAuthority`
- [ ] Test: `graph-run-created` and `graph-gate-approved` both appear with identities
- [ ] Negative control: `validateGates` still rejects an ungated commit graph
- [ ] Coverage floor so a skipped section cannot report green
- [ ] Run the script and verify all checks pass

## Status

**Backlog** — filed 2026-09-03 from an incident **four minutes old at filing**, observed live in this
session. Unlike most entries here, the evidence was not relayed: plan read the lifecycle log, the
frozen `graph.json`, the approval marker on disk, and the git state directly. Every source claim in
the tables above was verified against this repo.

No implementation has started.

**Placement argument** (the table entry is ranked **#1**): this is the only defect in the backlog whose
failure mode is an **irreversible, externally visible** action taken with no human in the loop — a
pushed branch and an opened PR. Everything else corrupts local state or returns a wrong answer, both
recoverable by the person who finds them. It also compounds with MUX-141, which is open and firing, to
form a complete unattended path from a process exit to a PR. It is placed above the in-flight work
because throughput is the wrong thing to optimize while the gate is open.

Open questions for the user:

- **Who may approve?** The narrowest defensible default is "no agent — a human at the CLI only", but
  that may make autonomous graph arcs unusable by design. That trade is the user's call, not a default
  to be picked here.
- **Retrofit existing markers?** Markers written before this change carry no identity. Treating them as
  invalid is safest and breaks any in-flight run; treating them as valid preserves the hole for the
  life of those runs.
- **Scope to git, or all gated actions?** Atlassian writes travel the same normalization path. The
  evidence here is git-only; extending the claim to Atlassian needs the check in Phase 1 first.
