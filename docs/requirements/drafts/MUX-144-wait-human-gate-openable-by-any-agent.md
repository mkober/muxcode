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
alive and matches the shape [`MUX-141`](../backlog/MUX-141-auto-agent-restart-relaunches-graph-runs.md)
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

#### Defect B — approval and run creation are entirely unaudited — **FIXED 2026-09-04 (`16f2027`)**

The table below is the **finding as of 2026-09-03** and is retained as the incident record; every row
has since been remediated. `ApproveGraphGate` (now `bus/graph_exec.go:184`) records
`approved_by` from `BusActorVerified` and calls `announceGraphAction` → `LogLifecycle`
(`bus/graph_run.go:238-239`), and run creation emits `graph-run-created` (`bus/graph_run.go:221`).
Defects A and C remain open — see Status.

| Fact (as of 2026-09-03, now remediated) | How established |
|------|-----------------|
| `ApproveGraphGate` is `ReadNodeStatus` + `MkdirAll` + `atomicWriteJSON` — **no `LogLifecycle` call** | **Verified** — `bus/graph_exec.go:80-89` (pre-fix location) |
| The marker records no approver identity, only `approved_at` | **Verified** — read from disk (above) |
| No `graph-gate-approved` event exists; the log jumps `graph-gate-pending` → `graph-node-done` | **Verified** — lifecycle log above |
| Run **creation** logs nothing either — the first event of the incident is `graph-node-start` | **Verified** — `lifecycle show \| grep -cE "graph-run-created\|graph-run-start\|graph-gate-approved"` returns `0` |

#### Defect C — the runtime backstop is a no-op for graph sends

`CLAUDE.md` currently claims:

> **Authority gates are not bypassable**: … `CheckCommitAuthority`/`CheckAtlassianAuthority` remain the
> runtime backstop — a graph cannot launder an action around the rules that govern it.

| Fact | How established |
|------|-----------------|
| Graph sends carry `From = "daemon"` | **Verified** — `const graphSender = "daemon"`, `bus/graph_exec.go:29`, used at `:739` |
| `CheckCommitAuthority` normalizes the sender before checking it | **Verified** — `from = NormalizeBusRole(from)`, `bus/commit_authority.go:90` |
| `NormalizeBusRole("daemon")` returns `"edit"` | **Verified** — `bus/config.go:729-731` |
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
| [`MUX-141`](../backlog/MUX-141-auto-agent-restart-relaunches-graph-runs.md) | **Compounding, neither subsumes the other.** MUX-141 supplies the *source* of unrequested runs (a restart relaunches autonomous work); this spec supplies the reason one can reach a push. MUX-141 alone yields spurious runs that **stop at a gate**; this alone makes gates openable. Together they are an unattended path from an external process exit to a PR. Cross-linked both ways. |
| [`MUX-132`](../completed/MUX-132-graph-retry-launders-gate-approval.md) | **Adjacent hole, and 132's fix is sound.** MUX-132 closed a *stale-marker reuse* path so a retried run demands a **fresh** approval. This spec is about a fresh approval **nobody human made**. 132 guards the step "is this approval current?"; nothing guards "is this approval human?" — the two are complementary, and 132 needs no revision. |
| [`MUX-142`](../backlog/MUX-142-spawn-worker-delegates-into-wrong-tree.md) | Shares the lesson that a control verified on one road is not verified on all of them. |

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

- [x] Approving a gate **requires authority**; the permitted roles are configurable and default to
      something narrower than "every agent" — `CheckGateApprovalAuthority` (`bus/gate_authority.go`),
      called from `ApproveGraphGate` so both roads (CLI and graph TUI) meet it; default is the
      **user alone**, `MUXCODE_GATE_AUTHORITY_ROLES` opts roles in, the empty string denies every
      actor, and `ActorUnknown` is refused whatever the list says. A refusal logs
      `graph-gate-approval-refused` naming the actor. *Landed in `4349a7a`, 2026-09-08 14:21.*
      **Review P1 (the list is caller-controlled) — narrowed in `d4ae976` and again in `31a2ca4`,
      still open:** the setting is read from the config file, never the approver's environment, and
      since `31a2ca4` from a fixed path list rather than `$MUXCODE_CONFIG` — but the file is
      agent-writable — see the open Phase 2 step, whose 19:30 note records `c4997ed` (the daemon-side seal and
      re-decision, **unverified**)
- [x] An autonomous agent **cannot approve a gate on a run it created** — the self-approval rule is
      checked after the list, so widening the list does not lift it
      (`TestApproveGraphGateRefusesSelfApproval`, `gate_authority_test.go`)
- [x] Gate approval emits a lifecycle event **naming the approver**, and the marker records approver
      identity, not just a timestamp — `ApproveGraphGate` writes `approved_by` from `BusActorVerified`
      (`graph_exec.go:193`) and `announceGraphAction` calls `LogLifecycle` with the actor
      (`graph_run.go:238-239`). *Landed in `16f2027`, merged 2026-09-04.*
- [x] Graph **run creation** emits a lifecycle event naming its creator — `CreatedBy` recorded at
      `graph_run.go:197`, `graph-run-created` emitted at `:221`
- [ ] A graph-dispatched git mutation is **distinguishable** from an edit-originated one at
      `CheckCommitAuthority`, and is judged on the gate's approval rather than on the normalized sender
- [x] `CLAUDE.md`'s "Authority gates are not bypassable" paragraph is corrected to describe what is
      actually enforced — rewritten to *"Authority gates are only as strong as the gate, and today
      that is weak (MUX-144)"*, naming both verified gaps and instructing readers to treat
      `wait_human` as a scheduling pause, not a security boundary, until this spec lands
- [x] **Negative control:** a genuine user approval at the CLI still releases the gate with no extra
      friction, and the run proceeds exactly as today — `TestApproveGraphGateAllowsUser`; the CLI's
      `approve` calls `ApproveGraphGate` with nothing in between, and the three integration scripts
      that release gates (`test-graph-orchestrator.sh`, `test-multi-phase-graph.sh`,
      `test-close-spec-guard.sh`) now approve through the CLI as an opted-in scratch role
- [ ] **Negative control:** the daemon's own legitimate non-git sends are unaffected by any change to
      sender normalization

### Key files

| File | Relevance |
|------|-----------|
| `cmd/graph.go` | `approve` and `run` subcommands — where authority and audit are absent |
| `bus/graph_exec.go` | `ApproveGraphGate` (:184), `graphSender` (:29), send dispatch (:739); `c4997ed`: `gateApprovalHolds`, `approvalHasAudit` — daemon-side re-decision, **unverified** (Phase 2 step 5) |
| `bus/gate_authority.go` | Phase 2: `GateApprovalAuthority()`, `GateAuthorityConfigured()` (config file, never the environment; since `31a2ca4` a fixed path list via `gateAuthorityConfigPaths`, never `$MUXCODE_CONFIG` — but the file is agent-writable; Phase 2 step 5), `CheckGateApprovalAuthority()` — the approver check and the self-approval rule; `c4997ed`: `SealGateAuthority()` (sealed at daemon startup), `configuredGateAuthority()`, `intersectRoles()` — **unverified** (step 5) |
| `bus/commit_authority.go` | `CheckCommitAuthority` (:86) and the normalization that voids it for graphs |
| `bus/config.go` | `NormalizeBusRole` daemon→edit (:729) |
| `bus/graph.go` | `validateGates` (:567) — the half that works; must not regress |
| `bus/atlassian_authority.go` | Same bypass shape applies to Atlassian writes; verify and cover |
| `CLAUDE.md` | The false safety claim |

## Implementation

### Phase 1: Pin the bypass

- [x] Write a failing test: a graph `send` node with `role: commit` passes `CheckCommitAuthority`
      today because the sender normalizes to `edit` — pinned as a **characterization test** (passes
      today, written to be inverted at the Phase 4 fix; its failure message names the phase to check
      off): `TestGraphCommitDispatchPassesCommitAuthority`, plus the end-to-end laundering path in
      `TestGraphCommitDispatchReachesCommitInbox`, `bus/graph_bypass_test.go` (`c2c1174`, 2026-09-04)
- [x] Write a failing test: `ApproveGraphGate` succeeds with no caller identity and emits no lifecycle
      event — **ticked with a qualification**: the step cannot be satisfied as literally worded,
      because the "emits no lifecycle event" half (Defect B) was fixed in `16f2027` and is now
      regression-guarded by `TestApproveGraphGateRecordsAndAnnouncesApprover` (`graph_run_test.go`).
      The half still open — any role may approve, and self-approval is accepted — is pinned by
      `TestApproveGraphGateAcceptsAnyRole` and `TestApproveGraphGateAcceptsSelfApproval`
      (`bus/graph_bypass_test.go`), both written to invert at Phase 2
- [x] Confirm the same bypass applies to `CheckAtlassianAuthority`, or record precisely why it does not
      — **recorded: it does not apply today, for two reasons that both leave the shape intact.**
      (1) A graph-dispatched Atlassian write is refused **only by configuration**: the sender
      normalizes to `edit` and the authority sits with `plan`; move the authority to `edit` and the
      same bypass opens (`TestGraphAtlassianWriteRefusedOnlyByConfiguration`). (2) The graph never
      *needs* a bypass — it sends the write to `plan`, which holds the authority itself
      (`TestGraphAtlassianWriteNeedsNoBypass`). Same defect as the commit path with a different role
      in the way; Phase 4's fix must judge both on the gate, not the sender
- [x] Confirm `validateGates` still rejects an ungated commit node — the working half must not regress
      — confirmed by the existing pins in `bus/graph_test.go`: ungated commit (`:266`), ungated
      jira-write (`:274`), commit-role spawn (`:288`) and map (`:291`) are all rejected

### Phase 2: Authority and identity on approval

- [x] Add an approver-authority check to `graph approve`, with a configurable role list —
      `CheckGateApprovalAuthority` in `ApproveGraphGate` (not the CLI, because the graph TUI is a
      second road to the same release); `MUXCODE_GATE_AUTHORITY_ROLES`, default `user`.
      `TestGateApprovalAuthorityDefault`, `TestApproveGraphGateRefusesAgentRoles`,
      `TestApproveGraphGateRefusesUnidentifiedApprovers`, `TestApproveGraphGateHonorsConfiguredAuthority`
- [x] Record approver identity in the marker; keep it backward-compatible with existing markers or
      migrate them deliberately — `approved_by` is **additive**: `gateApprovalTime` reads only
      `approved_at`, so markers written before the field existed still parse (`graph_exec.go:182-183`)
- [x] Refuse self-approval: an agent may not approve a gate on a run it created —
      `run.CreatedBy` compared to the normalized actor; a person approving their own run is never
      refused (`TestApproveGraphGateRefusesSelfApproval`)
- [x] Negative control: an authorized human approval path is unchanged —
      `TestApproveGraphGateAllowsUser`; `TestGraphCommitDispatchReachesCommitInbox` now releases as a
      person and still reaches commit's inbox (the Phase 4 pin, sharpened rather than weakened)
- [ ] The configured list is **not caller-controlled**: an agent cannot widen
      `MUXCODE_GATE_AUTHORITY_ROLES` from its own environment for the one command it was just refused
      — **review P1, 2026-09-08 14:14; narrowed in `d4ae976` at 14:47 and again in `31a2ca4` at
      16:14, not closed.** (Plan ticked
      this on the commit and its test at 14:55; edit reopened it at 14:50 — rightly, see the end of
      this step.) As landed in `4349a7a` the
      list was read from the approver's own environment, the only mitigation was withholding the
      variable name from the deny message, and `CLAUDE.md:150` named it to every agent anyway.
      `GateApprovalAuthority` now reads the setting from the muxcode **config file** via
      `GateAuthorityConfigured`, which parses the file directly — `GetShellConfig` ends its loop with
      "env takes precedence", the exact inversion this control must avoid, and routing through it
      failed five tests. `TestGateAuthorityIgnoresCallerEnvironment` pins the attack (a refused agent
      re-running with `MUXCODE_GATE_AUTHORITY_ROLES=user,build` prefixed is still refused) with the
      negative control that the same value **in the config file** does admit it. Suite 2915 / 0.
      **Why it stays open:** the config file is agent-writable. Its *path* was env-derived too until
      `31a2ca4` (16:14) — `ResolveConfigPath` honours `$MUXCODE_CONFIG` before `.muxcode/config` and
      `~/.config/muxcode/config`, so an agent could self-authorize by pointing the process at a file
      it wrote (`MUXCODE_CONFIG=/tmp/x muxcode graph approve …`); `d4ae976` removed a one-line bypass
      and left that two-line one, and `31a2ca4` closed it: `GateAuthorityConfigured` now walks a fixed
      list (`.muxcode/config`, then `~/.config/muxcode/config`) via `gateAuthorityConfigPaths` and
      never calls `ResolveConfigPath`, so `$MUXCODE_CONFIG` is ignored end to end
      (`TestGateAuthorityIgnoresConfigPathOverride`, with a negative control that the same file
      *does* grant from a consulted path). What is left is the file itself: an agent with shell access
      edits `.muxcode/config` directly. The real fix is **daemon-side authority**:
      the daemon reads the list once, from a store the agents cannot reach, and the approve path asks
      the daemon rather than its own process. The commit and Atlassian authorities share this shape
      and should move with it.
      **Updated 2026-09-08 19:30 — `c4997ed` (19:11) landed the daemon-side half, unverified; the step
      stays open.** `SealGateAuthority` freezes the list at daemon startup (lifecycle
      `gate-authority-sealed`; a config edited mid-session may narrow the live authority but never
      widen it — `TestSealedGateAuthorityIgnoresLaterWidening`, `…HonorsLaterNarrowing`,
      `…IgnoresHomeSelectedConfig`), and `gateApprovalHolds` re-decides every release inside the
      daemon on the marker's `approved_by` (`TestHarvestWaitHumanRefusesUnauthorizedApproval`). The
      same commit adds `approvalHasAudit`: a marker must match a `graph-gate-approved` lifecycle row
      (same actor, detail and second) or it is refused and purged. **Why it is not ticked:** (1) the
      commit has not been built, tested or reviewed — `approvalHasAudit` was authored by the **test
      agent**, in breach of its role, after a halt and a reload, and the suite never ran on it
      ([MUX-157](../backlog/MUX-157-role-boundary-an-agent-can-ignore.md)); (2) `approvalHasAudit`
      moves the forgery target from the marker to the lifecycle JSONL
      (`~/.config/muxcode/logs/<session>.log`), which every agent can append to, and which
      **every append rotates** to the caller's `MUXCODE_LIFECYCLE_LOG_MAX` (default 5000,
      `lifecycle.go:61`) — an approval whose audit row has rotated out is refused as forged, and one
      low-valued append from any process truncates the evidence behind every pending approval;
      (3) the seal reads the same agent-writable files, and `./build.sh` → `upgrade-daemons`
      restarts the daemon as a matter of routine, so a widening waits for a restart an agent with
      build authority can cause — logged by `gate-authority-sealed`, not prevented; (4) the
      commit inverted `TestExecUnverifiedHoldReleasedByUser` (`want running` → `want pending`), so
      the forged-hold quartet no longer carries a positive control of its own (MUX-157 Phase 1).
      Tick after a suite and a review pass on `c4997ed` and (2) has a written answer
      **Updated 2026-09-08 22:15 — `77f32b8` (22:03) closed two of the four caveats and stays open on
      the other two.** (2) **closed**: `rotateLifecycleLog` now retains every audit row past the count
      cap (`lifecycleAuditEvents`, `bus/lifecycle.go`), so a low `MUXCODE_LIFECYCLE_LOG_MAX` from any
      process can no longer erase the evidence behind a pending approval —
      `TestRotateLifecycleLogPreservesAuditRows`, `…PreservesAllPendingAuditRows`; the commit also
      records that `approvalHasAudit` is corroboration and tamper-evidence, not authentication. (4)
      **closed**: the quartet has its positive control again —
      `TestExecUnverifiedHoldReleasedByAuditedUserApproval` beside
      `…RefusesUnauditedUserApproval`. A defect found on the way is fixed too: `ApproveGraphGate` read
      the clock twice for the audit row and the marker, so a genuine approval straddling a second
      boundary was refused as forged and purged — one `approvedAt` now stamps both
      (`announceGraphActionAt` / `LogLifecycleAt`, `TestApproveGraphGateStampsMarkerAndAuditAlike`,
      `TestLogLifecycleAtHonorsSuppliedTimestamp`). (1) **partly**: build, vet and the full bus and
      daemon suites ran green (`EXIT=0`) on the run agent as edit's independent re-verification — but
      no review pass: the spec-to-pr run's review node never executed (the run stalled at the held
      `test` node, see [MUX-154](./MUX-154-codex-status-line-closes-tracked-tasks.md)). (3)
      **untouched and still true**: the seal reads agent-writable files and a routine `./build.sh`
      restarts the daemon. **Not ticked** — the step's words ("not caller-controlled") do not hold
      while (3) stands; tick after a review pass and a written answer to (3).

### Phase 3: Audit the control plane

- [x] Emit `graph-run-created` naming the creator — `graph_run.go:221`
- [x] Emit `graph-gate-approved` naming the approver — `announceGraphAction` → `LogLifecycle`
- [x] Verify an incident of this exact shape is now attributable from the lifecycle log alone —
      confirmed live 2026-09-04 against run `1788457453-commit-pr-review-loop`: the log carries
      `graph-run-created … started by user` plus three `graph-gate-approved … approved by user`
      rows. An agent-created or agent-approved run would name the agent instead.
      **Note:** the 2026-09-03 00:48 incident that motivated this spec remains **permanently
      unattributable** — it predates these events, and the fix is not retroactive

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

## Time Tracking

| Branch | Active time | Last updated |
|--------|-------------|--------------|
| MUX-144-wait-human-gate-openable-by-any-agent | 4h 14m | 2026-09-08 14:24 |

## Status

**In Progress — 17/33, Phases 1 and 3 complete; Phase 2 at 4/5 with the review's P1 narrowed but
open.** Moved to `drafts/` and set as the **active spec** 2026-09-04; the pointer was lost in the
2026-09-08 13:44 relaunch and re-set by the user.

**Updated 2026-09-08 19:30.** `c4997ed` (19:11) landed the daemon-side half of P1 — `SealGateAuthority`
at daemon startup, `gateApprovalHolds` re-deciding each release, `approvalHasAudit` requiring a matching
lifecycle row — **unverified**: not built, tested or reviewed, and authored by the test agent in breach
of its role ([MUX-157](../backlog/MUX-157-role-boundary-an-agent-can-ignore.md), filed from it). The
step stays open on four caveats recorded inline (unverified; forgery moved to the agent-writable,
append-rotated lifecycle log; the seal's inputs are still the agent-writable files and a routine
`build.sh` restarts the daemon; the forged-hold quartet lost its positive control). Phase 2 still 4/5,
count unchanged at 17/33. `CLAUDE.md:131` already describes the seal as *the* control — that text is
edit's from `45e37ef` and predates the caveats.

**Updated 2026-09-08 22:15.** `77f32b8` (22:03) closed caveats (2) and (4) — audit rows survive
rotation, the quartet's positive control is back — and fixed a second-boundary race that refused
genuine approvals; the suites ran green on the run agent but no review pass happened, and caveat (3)
is untouched. Step 5 **stays open**; detail inline under the step. Phase 2 still 4/5, count unchanged
at 17/33.

**Updated 2026-09-08 16:35.** `31a2ca4` (16:14) narrowed P1 a **second** time: `GateAuthorityConfigured`
reads a fixed path list (`gateAuthorityConfigPaths`) and never `ResolveConfigPath`, so
`$MUXCODE_CONFIG` no longer selects the file (`TestGateAuthorityIgnoresConfigPathOverride`). The one
remaining bypass is the agent-writable config file itself; the step stays open for daemon-side
authority, which is Phase 4's. Phase 2 still 4/5, count unchanged at 17/33. Code-only commit — no
docs moved with it, hence this note.

**Updated 2026-09-08 14:58.** `d4ae976` (14:47) **narrowed** the review's P1 — the authority list is
now read from the config file, never the caller's environment — and plan ticked the step on that
commit and its test. Edit reopened it minutes later, correctly: the config file is agent-writable and
`ResolveConfigPath` honours `$MUXCODE_CONFIG`, so the bypass is two lines instead of one. The step
stays open until authority is decided daemon-side; Phase 2 stands at 4/5. Phases 4 and 5 are
untouched.

**Verified 2026-09-08 14:24.** Phase 2 is **committed as `4349a7a` (14:21)** — this paragraph first
said *uncommitted*, from a tree read taken at 14:14 that was three minutes stale by the time it was
written; corrected 14:45. The commit carries: `bus/gate_authority.go`
(`CheckGateApprovalAuthority` — default `user` alone, `MUXCODE_GATE_AUTHORITY_ROLES` opt-in, empty
string denies all, `ActorUnknown` refused, self-approval refused whatever the list says),
`ApproveGraphGate` calls it and logs `graph-gate-approval-refused`, the two Phase 1 pins for Defect A
were inverted into six tests in `gate_authority_test.go`, the three integration scripts that release
gates now do so through the CLI as an opted-in scratch role, and the `CLAUDE.md` paragraph was
rewritten to describe Phase 2 as landed and Phase 4 as the open gap. Suite: **2901 pass / 0 fail, exit
0, all six packages** — run on the **run** agent (Claude, unsandboxed) and read from its pane; the
codex test agent produced no result all session (MUX-153). Review (codex): **P1 unresolved — the
authority list is caller-controlled**, recorded as an open Phase 2 step above rather than ticked
past (and narrowed, not closed, 33 minutes later in `d4ae976`); P2 (selection-pinned flat-view scrolling in the graph TUI) is outside this spec and was fixed in
`287a350` (14:21).
**Provenance note:** both `verify-spec` requests for this pass were fired by echoed codex status lines
(`• Working (13s • esc to interrupt)`) that the daemon read as review successes at 14:12:55 and
14:13:25 — 1m45s *before* the reviewer's genuine reply. Filed as MUX-154; the verification above uses
the genuine reply.

**Verified 2026-09-08.** **Phase 1 (Pin the bypass) is complete, and has been since `c2c1174`
(2026-09-04 11:26)** — `bus/graph_bypass_test.go`, six characterization tests that assert the
*current* behaviour and carry failure messages naming the phase to check off when each is inverted.
The paragraph below was written the same day and never credited it, and plan's later verification
passes diffed only from the session's `HEAD`, so the four boxes sat stale for four days. Surfaced by
the `spec-to-pr` run's `implement` spawn, which reported "nothing to port" — correctly, its brief
said to verify and report if the phase was already done — and reached the same reading, including
the step 2 qualification, independently. Step 3's Atlassian answer is recorded inline above.

**Verified 2026-09-04.** The audit half landed ahead of the authority half, via `16f2027` — a commit
carrying a `MUX-136` prefix on that branch and merged here in PR #72, so `git log` by prefix does not
show it as MUX-144 work. **Phase 3 (Audit the control plane) is complete**: run creation and gate
approval both emit lifecycle events naming their actor, confirmed live in the log, and the marker
records `approved_by` backward-compatibly (Phase 2 step 2).

**Phase 4 is untouched.** `CheckCommitAuthority` still normalizes `daemon` → `edit`, so the runtime
backstop is still a no-op for graph sends; Phases 4 and 5 are at 0. The approve road is now guarded —
an unauthorized or self-interested release is refused and logged — so the 2026-09-03 incident is
**prevented on that road** as well as attributable, with two qualifications: a marker forged on disk
still passes, which is why `unverifiedHoldReleased` re-reads `approved_by` daemon-side; and the
authority list is still caller-controlled one step removed — `d4ae976` closed the environment read
and `31a2ca4` the env-derived config path, not the agent-writable file (review P1, open). *(Superseded text, kept for the record:
until 2026-09-08 `graph approve` had no authority check and self-approval was unrefused.)*

Filed 2026-09-03 from an incident **four minutes old at filing**, observed live in this
session. Unlike most entries here, the evidence was not relayed: plan read the lifecycle log, the
frozen `graph.json`, the approval marker on disk, and the git state directly. Every source claim in
the tables above was verified against this repo.

No implementation had started at filing; the audit half (Phase 3) and the Phase 1 pins have since
landed (see above).

**Placement argument** (the table entry is ranked **#1**): this is the only defect in the backlog whose
failure mode is an **irreversible, externally visible** action taken with no human in the loop — a
pushed branch and an opened PR. Everything else corrupts local state or returns a wrong answer, both
recoverable by the person who finds them. It also compounds with MUX-141, which is open and firing, to
form a complete unattended path from a process exit to a PR. It is placed above the in-flight work
because throughput is the wrong thing to optimize while the gate is open.

Open questions for the user:

- **Who may approve?** The narrowest defensible default is "no agent — a human at the CLI only", but
  that may make autonomous graph arcs unusable by design. That trade is the user's call, not a default
  to be picked here. **Phase 2 shipped the narrow default** (`user` alone) with
  `MUXCODE_GATE_AUTHORITY_ROLES` as the opt-in for an unattended arc; the user has not yet ruled on
  whether that default stands.
- **Retrofit existing markers?** Markers written before this change carry no identity. Treating them as
  invalid is safest and breaks any in-flight run; treating them as valid preserves the hole for the
  life of those runs.
- **Scope to git, or all gated actions?** Atlassian writes travel the same normalization path. The
  evidence here is git-only; extending the claim to Atlassian needs the check in Phase 1 first.
