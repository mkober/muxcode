# A Graph-Dispatched Atlassian Write Is Judged on Configuration, Not on the Gate

**Tracking:** filed 2026-09-13 by plan on the user's request relayed by edit (`1789323849`) — MUX-144's
Phase 4 step 5, extracted into its own spec on the user's decision. Every claim below was verified by
plan against this repo at `23d2804`.

MUX-144 Phase 4 made the runtime backstop real for **git**: a graph dispatch now carries its run and
node, `CheckCommitAuthorityForMessage` reads the raw sender, binds the message to its frozen node and
judges it on the gate's audited approval. **The same fix has no purchase on the Atlassian path**, and
not because the check is defeated — because it never sits on the bus path at all. A graph `jira-write`
is handed straight to `plan`, the role that legitimately holds the authority, carrying a sentence the
template wrote: *"The user approved the tracker update"*. Nothing mechanical checks that a user did.

## Context

### The finding

| Fact | How established |
|------|-----------------|
| `CheckAtlassianAuthority(role, service, action)` normalizes the role and checks it against the authority list | **Verified** — `bus/atlassian_authority.go:207-227` |
| Its only enforcement sites are the CLI (`cmd/atlassian.go:53`, judging the **writer's** own role) and the MCP guard (`bus/hook.go:1404`) — **none in `sendMessage`** | **Verified** — `grep -rn 'CheckAtlassianAuthority(' tools/muxcode` |
| `validateGates` requires an upstream `wait_human` for a node sending `jira-write` / `confluence-write` (`gatedAtlassianActions`) | **Verified** — `bus/graph.go:152-156`, `:590` |
| `story-to-spec`'s `jira-update` node dispatches to `plan` with the message *"The user approved the tracker update: …"* | **Verified** — `bus/graph_templates.go:114` |
| A graph send is `From = "daemon"`; the commit road now reads that raw sender (MUX-144 Phase 4), the Atlassian road has no sender check to read it | **Verified** — `bus/commit_authority.go` `CheckCommitAuthorityForMessage`; no Atlassian counterpart |
| The refusal that exists today is configuration: with the authority at `plan` a `daemon`→`edit` sender is refused **by the CLI check**, and moving the authority to `edit` admits it — but the graph never asks that question, it dispatches to `plan` | **Verified** — `TestGraphAtlassianWriteRefusedOnlyByConfiguration`, `TestGraphAtlassianWriteNeedsNoBypass` (`bus/graph_bypass_test.go`) |

So there is **no laundered sender to re-judge**. The instruction is simply handed to the agent that
may act on it, and what stands between the dispatch and a tracker write is plan's *definition*: a bus
message from another agent is never user consent. Plan declined one such dispatch live on 2026-09-03
(MUX-144's second occurrence) and another on 2026-09-09 (MUX-165). **A behavioural control, twice
exercised, is still not a mechanical one** — the same shape MUX-144 found on the git road, where the
margin was the commit agent's judgement.

### Why it matters

A wrong write here is a **tracker entry other people read** — externally visible, though not
irreversible the way a pushed branch is. It is also one half of a pair with
[MUX-165](./MUX-165-gated-jira-write-declined-by-requester-rule.md): that spec wants plan to *honour*
a gate-approved write, and its technical approach already names the mechanism — the executor attaches
gate provenance to the dispatch and a bus-side `VerifyGateProvenance` checks it against `approvals/`
— in service of **acceptance**. This spec is the **refusal** half of the same mechanism: a backstop
that fails closed for a graph-originated Atlassian write whose provenance does not hold, so that plan's
acceptance rule has something mechanical to lean on. One mechanism, two rules; they ship as one piece
of work (`⇄` in the backlog index), and neither is safe alone — acceptance without refusal trusts a
sentence any run can author, refusal without acceptance leaves MUX-165's three-consents incident in
place.

### What the two halves look like today

| | Git road (MUX-144 Phase 4) | Atlassian road (this spec) |
|---|---|---|
| Gate required by the validator | yes (`nodeRequiresGate`) | yes (`gatedAtlassianActions`) |
| Gate approval guarded and audited | yes (Phase 2) | yes — same gate |
| Dispatch carries provenance | `Message.GraphRun` / `GraphNode` | the same fields are stamped — nothing reads them |
| Judged on the gate at the bus | `CheckCommitAuthorityForMessage` in `sendMessage` | **nothing** |
| Judged where the mutation runs | the commit agent's judgement | `CheckAtlassianAuthority` judges plan's role — passes |
| What stops a forged "the user approved" | the backstop | plan's rule |

### Relationship to existing specs

| Spec | Relationship |
|------|--------------|
| [MUX-144](../completed/MUX-144-wait-human-gate-openable-by-any-agent.md) | **Parent.** Its Phase 1 step 3 recorded this finding; its Phase 4 closed the git half and step 5 now points here. Its reusable pieces are the starting point: `Message.GraphRun`/`GraphNode`, `dispatchMatchesNode`, `approvalDenial` |
| [MUX-165](./MUX-165-gated-jira-write-declined-by-requester-rule.md) | **Co-scheduled (`⇄`), not a dependency in either direction.** MUX-165's fix already specifies provenance on the dispatch and `VerifyGateProvenance` at the bus so plan can *accept* a gate-approved write; this spec pins the *refusal* side of that same check — fail closed, keyed on the raw graph sender, logged — and the inversion of the two MUX-144 pins. Build the mechanism once; land both rules together |
| [MUX-132](../completed/MUX-132-graph-retry-launders-gate-approval.md) | Adjacent: guards whether an approval is *current*, not whether it is *human* — unchanged |
| [MUX-157](./MUX-157-role-boundary-an-agent-can-ignore.md) | Same lesson: a role's definition is a behavioural boundary, and this spec is what a mechanical one would look like for one write path |

## Requirements

### Acceptance criteria

- [ ] A graph-dispatched Atlassian write (`jira-write`, `confluence-write`) is **bound to the
      `wait_human` gate that released it** — attributable, authorized and audited approval, message
      matched to its frozen node — before plan acts on it, by a mechanical check and not by plan's
      reading of the payload
- [ ] A dispatch with no provenance, an unreadable run, a node the frozen graph lacks, or a forged
      marker is refused **and logged**, naming the reason
- [ ] The template sentence *"The user approved the tracker update"* no longer carries any weight:
      the decision rests on the gate's record, whatever the payload says
- [ ] **Negative control:** plan's own legitimate, user-relayed Jira and Confluence writes (a
      `jira-write` request **from `edit`**) are completely unaffected
- [ ] **Negative control:** a graph-dispatched `jira-read` / `confluence-read` is unaffected — reads
      stay open to every role
- [ ] The two MUX-144 Phase 1 pins that assert the current shape
      (`TestGraphAtlassianWriteRefusedOnlyByConfiguration`, `TestGraphAtlassianWriteNeedsNoBypass`) are
      inverted or re-pointed at the landed control, never relaxed

### Technical approach

**The seam is the open design question, and this spec does not settle it.** Three candidates, each
with a cost the user should weigh:

| Seam | Shape | Cost |
|------|-------|------|
| **At the send** (`sendMessage`, beside `CheckCommitAuthorityForMessage`) | extend the message-road check to `gatedAtlassianActions`: a `daemon`-sent `jira-write` must carry provenance and pass the same node-binding + `approvalDenial` test; refused dispatches never reach plan's inbox | symmetric with the git fix and reuses every piece; but a `jira-write` from `edit` is a user relay and must pass untouched, so the predicate keys on the **raw sender being the graph**, exactly as the commit road does |
| **At the CLI with provenance carried through** | plan's `muxcode atlassian` write call names the run and node it is acting for; `CheckAtlassianAuthority` re-decides on the gate | keeps the check where the mutation happens, but plan must carry the provenance from inbox to CLI by hand, which is the behavioural step this spec exists to remove |
| **Make the template sentence unforgeable** | the executor, not the template, writes the consent claim, signed by the audited approval | least general — it trusts the executor's own message, and a forged inbox row is still a bus write any agent can make |

The first is the recommendation on the evidence: it is one predicate away from the landed commit
road, and its negative control (an `edit`-originated request passes) is the same test the commit road
already has — and it is the shape MUX-165's approach already names as `VerifyGateProvenance` (with
`muxcode graph provenance <task-id>` as its CLI), so the two specs build it once. The decision is
Phase 2's, recorded in this spec before any code moves.

### Key files

| File | Relevance |
|------|-----------|
| `bus/atlassian_authority.go` | `CheckAtlassianAuthority` (:207) — judges a role at the CLI; has no bus-path caller |
| `bus/commit_authority.go` | `CheckCommitAuthorityForMessage`, `checkGraphCommitDispatch`, `dispatchMatchesNode`, `graphGateApprovalAuthorizes` — the landed commit road to mirror |
| `bus/graph_exec.go` | `approvalDenial` — the single definition of a valid gate approval; `dispatchNode` stamps `GraphRun`/`GraphNode` on every send already |
| `bus/graph.go` | `gatedAtlassianActions` (:152) — the action list the validator already keeps; the send-time check would key on it |
| `bus/inbox.go` | `sendMessage` (:221) — the seam the commit check sits at |
| `bus/graph_templates.go` | `story-to-spec` `jira-update` node (:114) — the template-authored consent sentence |
| `bus/graph_bypass_test.go` | the two Atlassian pins to invert |
| `agents/planner.md` | plan's rule: *write only on an explicit user-initiated request relayed from edit* — gains a mechanical companion, does not lose the behavioural one |

## Implementation

### Phase 1: Pin the gap

- [ ] Characterization test: a `daemon`-sent `jira-write` with **no** provenance reaches plan's inbox
      today; written to invert when the control lands, failure message naming this spec
- [ ] Characterization test: the same dispatch with provenance but an **unapproved** gate reaches plan's
      inbox today — the discriminating pair for Phase 3
- [ ] Re-read `TestGraphAtlassianWriteNeedsNoBypass` and record in this spec which of its assertions
      survive the fix and which invert

### Phase 2: Decide the seam

- [ ] Record the user's choice among the three seams above, with the reason, in this spec's Notes
- [ ] If the send seam: confirm `IsAtlassianMutatingAction` / `gatedAtlassianActions` agree on the
      action list, or name which one the check keys on

### Phase 3: Bind the dispatch to the gate

- [ ] Implement the chosen seam; a graph-dispatched Atlassian write is judged on `approvalDenial` +
      node binding, refused and logged otherwise
- [ ] Invert the Phase 1 pins; the discriminating pair reads refused-then-allowed with the sender unchanged
- [ ] Negative control: an `edit`-originated `jira-write` and a `daemon`-originated `jira-read` pass untouched
- [ ] Positive control through the real executor: a gated `jira-write` released by a person still
      reaches plan's inbox carrying its provenance

### Phase 4: Docs

- [ ] `docs/architecture.md` "Authority gates — what actually holds": the Atlassian road paragraph
- [ ] `CLAUDE.md` Atlassian-authority constraint: the mechanical check beside the relayed-from-edit rule
- [ ] `agents/planner.md`: plan may treat a **verified** gate dispatch as consent — the rule MUX-165
      needs, stated only once this control exists

### Phase 5: Integration test

- [ ] Extend `scripts/test-gate-authority.sh` (or create `scripts/test-atlassian-gate.sh`) against the
      same scratch daemon: a gated `jira-write` graph
- [ ] Test: with the gate unapproved, `retry --from` the write node → refused, plan's inbox empty,
      refusal row logged
- [ ] Test: an authorized non-creator approval releases the gate → plan's inbox holds the dispatch with
      its provenance
- [ ] Negative control: an `edit`-sent `jira-write` in the same session is delivered untouched
- [ ] Negative control: `validateGates` still rejects an ungated `jira-write` node
- [ ] Coverage floor so a skipped section cannot report green
- [ ] Run the script and verify all checks pass

## Notes

- Filed from MUX-144's own analysis (its *second occurrence*, 2026-09-03 01:19) and edit's Phase 4
  note that the path is **not symmetric** with commit — there is no sender to re-judge, so the fix is a
  new check, not an extension of an existing one. That asymmetry is why this is its own spec.
- MUX-165's live incident (2026-09-09: a user-approved `update-gate` whose dispatch plan declined) is
  the *cost* of the current shape; this spec is the *control* that lets MUX-165 lower it safely. Their
  overlap is deliberate and recorded: MUX-165 already carries the provenance mechanism in its fix, and
  this spec exists so the refusal half is a tracked requirement with its own pins, not a side effect.

## Status

Backlog — filed 2026-09-13, 0/25. Co-scheduled with MUX-165 (`⇄`).
