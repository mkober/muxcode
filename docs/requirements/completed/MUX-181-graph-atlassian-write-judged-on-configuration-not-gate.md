# A Graph-Dispatched Atlassian Write Is Judged on Configuration, Not on the Gate

**Tracking:** [mkober/muxcode#139](https://github.com/mkober/muxcode/issues/139)

**Provenance:** filed 2026-09-13 by plan on the user's request relayed by edit (`1789323849`) — MUX-144's
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
[MUX-165](../backlog/MUX-165-gated-jira-write-declined-by-requester-rule.md): that spec wants plan to *honour*
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
| [MUX-165](../backlog/MUX-165-gated-jira-write-declined-by-requester-rule.md) | **Co-scheduled (`⇄`), not a dependency in either direction.** MUX-165's fix already specifies provenance on the dispatch and `VerifyGateProvenance` at the bus so plan can *accept* a gate-approved write; this spec pins the *refusal* side of that same check — fail closed, keyed on the raw graph sender, logged — and the inversion of the two MUX-144 pins. Build the mechanism once; land both rules together |
| [MUX-132](../completed/MUX-132-graph-retry-launders-gate-approval.md) | Adjacent: guards whether an approval is *current*, not whether it is *human* — unchanged |
| [MUX-157](../backlog/MUX-157-role-boundary-an-agent-can-ignore.md) | Same lesson: a role's definition is a behavioural boundary, and this spec is what a mechanical one would look like for one write path |

## Decisions (user, 2026-11-24)

The user's decision, relayed by edit. The spec was parked because Phase 2's seam was the user's
choice among three and unrecorded.

| Question | Decision |
|----------|----------|
| The seam | **Seam (a) — at the send.** A verifier in `sendMessage`, beside `CheckCommitAuthorityForMessage`, keyed on the raw sender being `daemon` plus the gate approval that the message's `GraphRun`/`GraphNode` lead to |
| One mechanism or two | **One verifier serves both this spec and [MUX-165](../backlog/MUX-165-gated-jira-write-declined-by-requester-rule.md)**; plan's rule loosens to accept gate-approved writes. The two ship together |

**Division of the work.** This spec owns the **verifier** — the send-time check, its refusal row,
the inverted MUX-144 pins and the integration script. MUX-165 owns the **rule change** — plan's
definition (`agents/planner.md`), the `CLAUDE.md` Atlassian-authority clause and the consent-path
wording — and lands in the same change set, after this spec's Phase 2.

**Found in code at the decision (2026-11-24), and folded in below:**

- The validator's list is now `gatedTrackerWriteActions` (`bus/graph.go:209`) and holds three
  actions: `jira-write`, `confluence-write` **and `issue-write`**. GitHub issue writes moved to plan
  after this spec was filed, and four builtin nodes send one (`bus/graph_templates.go:192`, `:297`
  and the `jira-write` nodes at `:191`, `:393`). The verifier keys on that list, so it covers
  `issue-write` too. That widening follows from "keyed on the list the validator keeps" and was not
  spelled out in the user's decision — flagged to edit.
- `agents/planner.md:179` already tells plan to honour a gate-dispatched `jira-write`/`issue-write`
  after confirming the gate with `muxcode graph status <run>` (commit `b8bfcab`). So the acceptance
  half exists as a behavioural rule today; what is still missing is the mechanical refusal this spec
  builds.
- `sendMessage` still carries only the commit check (`bus/inbox.go:316`), and both pins
  (`TestGraphAtlassianWriteRefusedOnlyByConfiguration`, `TestGraphAtlassianWriteNeedsNoBypass`,
  `bus/graph_bypass_test.go:332`, `:355`) still assert the undefended shape.

## Requirements

### Acceptance criteria

- [x] A graph-dispatched tracker write (`jira-write`, `confluence-write`, `issue-write` — the
      validator's `gatedTrackerWriteActions`) is **bound to the
      `wait_human` gate that released it** — attributable, authorized and audited approval, message
      matched to its frozen node — before plan acts on it, by a mechanical check and not by plan's
      reading of the payload
- [x] A dispatch with no provenance, an unreadable run, a node the frozen graph lacks, or a forged
      marker is refused **and logged**, naming the reason
- [x] The template sentence *"The user approved the tracker update"* no longer carries any weight:
      the decision rests on the gate's record, whatever the payload says
- [x] The check sits in `sendMessage` beside `CheckCommitAuthorityForMessage` and is keyed on the
      **raw** sender `daemon`, never the normalized role — so a refused dispatch never reaches
      plan's inbox
- [x] **Negative control:** plan's own legitimate, user-relayed tracker writes (a `jira-write` or
      `issue-write` request **from `edit`**) are completely unaffected
- [x] **Negative control:** a graph-dispatched `jira-read` / `confluence-read` is unaffected — reads
      stay open to every role
- [x] The two MUX-144 Phase 1 pins that assert the current shape
      (`TestGraphAtlassianWriteRefusedOnlyByConfiguration`, `TestGraphAtlassianWriteNeedsNoBypass`) are
      inverted or re-pointed at the landed control, never relaxed

### Technical approach

**The seam is decided: at the send** (Decisions above). `CheckTrackerWriteForMessage(session, m)`
— the name is the implementer's — returns "" unless `m` is a request whose action is in
`gatedTrackerWriteActions` **and** whose raw sender is the graph (`isGraphDispatch(m.From)`); for
those it asks the two questions `checkGraphCommitDispatch` asks of a commit: is the message the work
the frozen graph defines for the node it names (`dispatchMatchesNode`), and does a `wait_human` gate
whose territory holds that node carry an approval that is attributable, authorized and corroborated
by its `graph-gate-approved` row (`graphGateApprovalAuthorizes`, `gateApprovalHolds`). Anything else
is refused and logged `tracker-write-refused`, fail closed. Any other sender is not this check's
business: `edit` relays the user, and a non-edit agent's request is plan's rule to decline.

The three candidates as weighed, kept for the record:

| Seam | Shape | Cost |
|------|-------|------|
| **At the send** (`sendMessage`, beside `CheckCommitAuthorityForMessage`) | extend the message-road check to `gatedAtlassianActions`: a `daemon`-sent `jira-write` must carry provenance and pass the same node-binding + `approvalDenial` test; refused dispatches never reach plan's inbox | symmetric with the git fix and reuses every piece; but a `jira-write` from `edit` is a user relay and must pass untouched, so the predicate keys on the **raw sender being the graph**, exactly as the commit road does |
| **At the CLI with provenance carried through** | plan's `muxcode atlassian` write call names the run and node it is acting for; `CheckAtlassianAuthority` re-decides on the gate | keeps the check where the mutation happens, but plan must carry the provenance from inbox to CLI by hand, which is the behavioural step this spec exists to remove |
| **Make the template sentence unforgeable** | the executor, not the template, writes the consent claim, signed by the audited approval | least general — it trusts the executor's own message, and a forged inbox row is still a bus write any agent can make |

The first is the recommendation on the evidence: it is one predicate away from the landed commit
road, and its negative control (an `edit`-originated request passes) is the same test the commit road
already has. It was the recommendation and is the user's choice. MUX-165 as filed named a
receiver-side `VerifyGateProvenance` with a `muxcode graph provenance <task-id>` CLI; under seam (a)
the verification happens before delivery, so that CLI is not built and MUX-165 is rewritten to lean
on this check.

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

- [x] Characterization test: a `daemon`-sent `jira-write` with **no** provenance reaches plan's inbox
      today; written to invert when the control lands, failure message naming this spec
- [x] Characterization test: the same dispatch with provenance but an **unapproved** gate reaches plan's
      inbox today — the discriminating pair for Phase 3
- [x] Re-read `TestGraphAtlassianWriteNeedsNoBypass` and record in this spec which of its assertions
      survive the fix and which invert

#### Phase 1 findings (2026-11-24)

Reported by the run's implement worker (`spawn-5f179934`, run `1795547986`) and recorded by plan as
reported.

**Verified by plan 2026-11-24 15:27** (`verify-spec`): both pins are in
`tools/muxcode/bus/graph_bypass_test.go` (last written 15:21:16), each with a failure message
naming MUX-181 and saying to invert; `./test.sh` passed at 15:24 with them; review returned 0
must-fix, 0 should-fix, 1 nit at 15:25. The nit, for Phase 2: the `jira-write` no-provenance
assertion is now made twice — by `TestGraphAtlassianWriteNeedsNoBypass` and by the new pin — and
should be consolidated when both invert. Plan read the test names and messages and the history
rows; it ran nothing. Phase 1 is 3/3; no acceptance criterion is met by it.

**Pins added** (`tools/muxcode/bus/graph_bypass_test.go`), both asserting today's behaviour:

| Test | What it pins |
|------|--------------|
| `TestGraphTrackerWriteWithoutProvenanceReachesPlan` | A `daemon`-sent request carrying no `GraphRun`/`GraphNode` and the template sentence "The user approved the tracker update: …" is delivered to plan for **each** of `jira-write`, `confluence-write` and `issue-write` — the validator's `gatedTrackerWriteActions`. Its failure message names MUX-181 and says to invert. It covers `issue-write`, which `TestGraphAtlassianWriteNeedsNoBypass` never reached |
| `TestGraphTrackerWriteWithUnapprovedGateReachesPlan` | A real run (`a` → `gate` wait_human → `j` plan:`jira-write`) created by an agent (`auto`). The dispatch the executor would send for `j`, run and node stamped, is delivered to plan with the gate **unapproved**; then, after a person approves (`ApproveGraphGate`), the same dispatch is delivered again. Both halves deliver today — the discriminating pair Phase 2 turns into refused-then-delivered with the sender unchanged. The inbox is drained between halves, since the duplicate-request guard would otherwise suppress the identical second send |

**`TestGraphAtlassianWriteNeedsNoBypass` — what survives the fix:**

| # | Assertion | After Phase 2 |
|---|-----------|---------------|
| 1 | `SendNoCC` of a `daemon` `jira-write` to plan with no provenance succeeds and plan's inbox holds it | **Inverts** — refused (`tracker-write-refused`), plan's inbox empty. The new no-provenance pin generalizes this half to all three actions |
| 2 | `CheckAtlassianAuthority("plan", "jira", "update")` is allowed | **Survives unchanged** — the fix is at the send, not the CLI; plan keeps its own write authority, and a delivered, verified dispatch is still executed by plan as plan |

The test's doc comment ("the graph never needs to defeat `CheckAtlassianAuthority` … the gate is
again the only control") becomes historical; the worker's recommendation is to re-point it at the
landed send-time check rather than delete it.

**`TestGraphAtlassianWriteRefusedOnlyByConfiguration`:** both assertions are facts about
`CheckAtlassianAuthority` at the CLI — `daemon`→`edit` refused with the authority at plan, admitted
with it at edit — and **survive**, since the send-time check does not touch that function. Its
framing ("not a control") is re-pointed: the control now sits at the send.

### Phase 2: Bind the dispatch to the gate (seam a)

- [x] The send-time check keys on `gatedTrackerWriteActions` — one list, shared with the validator,
      so the two cannot disagree; a test fails if an action is added to one and not the other
- [x] Implement it in `sendMessage` beside the commit check, requests only: a `daemon`-sent tracker
      write is judged on node binding plus the gate's audited approval, refused and logged
      `tracker-write-refused` (naming run, node and reason) otherwise
- [x] Invert the Phase 1 pins in the same change, so the suite is never left red; the discriminating
      pair reads refused-then-allowed with the sender unchanged
- [x] Negative control: an `edit`-originated `jira-write` and `issue-write`, and a `daemon`-originated
      `jira-read`, pass untouched
- [x] Positive control through the real executor: a gated `jira-write` released by a person still
      reaches plan's inbox carrying its run and node
- [x] Every builtin template that sends a tracker write (`10-story-to-spec` `jira-update` and
      `issue-update`, `20-defect-to-spec` `issue`, `110-pr-merge` `tracker`) still dispatches after
      its gate is approved — read from `graph_templates.go`, not from this list

**Verified by plan 2026-11-24 15:35** (`verify-spec`, run `1795547986`): the check is
`CheckTrackerWriteForMessage` (new `bus/tracker_authority.go`), keyed on `gatedTrackerWriteActions`
and the raw sender, called in `sendMessage` (`bus/inbox.go`) where a refusal logs
`tracker-write-refused`; it shares one judgement with the commit road
(`checkGraphGatedDispatch`, `bus/commit_authority.go`). `tracker_authority_test.go` holds
`TestGraphTrackerWriteRefusedWithoutProvenance` (all three actions, inbox empty, one row each),
`TestGraphTrackerWriteJudgedOnGateApproval` (refused unapproved, delivered once a person approves,
sender unchanged), `TestGraphTrackerWriteRefusals` (unknown run, node not in the graph, a node of
another kind, a rewritten payload, the bare template sentence — each refused under an approved
gate), `TestGraphTrackerWriteRefusesForgedApproval`, `TestTrackerWriteCheckSharesValidatorList`,
`TestTrackerWriteNegativeControls`, `TestGraphTrackerWriteReachesPlanThroughExecutor` and
`TestBuiltinTrackerWritesDispatchAfterGate`. Files last written 15:30:02; `./build.sh` and
`./test.sh` passed after that (15:30, 15:32); review returned 0 must-fix, 0 should-fix, 1 nit at
15:33 — the Phase 1 nit again, the duplicated `jira-write` no-provenance assertion, still not
consolidated. Plan read the code and tests and the history rows; it ran nothing. Phase 2 is 6/6,
and all seven acceptance criteria are met by it on unit tests; Phase 5's script is the end-to-end
proof and is not a criterion.

One thing the worker changed beyond the step: `dispatchMatchesNode` now compares against
`graphSendMessage`'s payload, because `jira-write` and `issue-write` dispatches carry the
verdict-token seed. Commit payloads are reported unchanged.

### Phase 3: Hand-off to MUX-165

- [x] MUX-165's rule change (`agents/planner.md`, `CLAUDE.md`) lands in the same change set, after
      Phase 2 — plan is told a delivered `daemon` tracker write has been verified only once that is
      true

**Verified by plan 2026-11-24 15:42** (`verify-spec`, run `1795547986`), from the diff itself.
`agents/planner.md` (last written 15:36:59) now names three consent paths for a tracker write —
typed into plan's pane, relayed from edit, or a `daemon` dispatch whose `Origin:` line names a graph
run — and tells plan to execute the third without asking edit or the user again and without a
`muxcode graph status` check; a `daemon` tracker write naming no graph run, and one from any other
agent, are still declined. `CLAUDE.md`'s Atlassian-authority and plan-scope bullets say the same and
name `CheckTrackerWriteForMessage`. `./test.sh` passed at 15:39; review returned 0 must-fix, 1
should-fix at 15:36 (the refusal wording also rejected edit's relays) and 0/0/0 at 15:39, with the
duplicated no-provenance test consolidated. Phase 3 is 1/1.

**Two things in the new rule that are not in the user's decision, raised with edit at verification:**

- **"Typed into your pane" is a new consent path.** The rule it replaces named edit's relay only.
  It is the user acting directly, so it is hard to object to, but it was not asked for.
- **The `muxcode graph status` re-check is removed, on the grounds that delivery is the
  verification.** That holds for a message that went through `Send`. Two cases where it does not:
  a daemon still running a build from before this change sends the dispatch unchecked, while a plan
  agent relaunched on the new definition executes it without looking (definition and binary install
  together, but a daemon restart can fail — MUX-161 — and `session status` reports a stale daemon
  only when asked); and plan's inbox is a file the same uid can append to, so a line written there
  directly, naming a real run and node, never passes the check at all. The re-check was a cheap
  second look at the gate's own record in both cases. Plan has not checked whether any guard refuses
  a direct write to an inbox file. Keeping the re-check costs one read-only command per tracker
  write; whether to keep it is the user's call, and it is MUX-165's text.

### Phase 4: Docs

- [x] `docs/architecture.md` "Authority gates — what actually holds": the tracker-write road
      paragraph, with the refusal row
- [x] `docs/agent-bus.md`: the `tracker-write-refused` row and the gate rule's second half

**Verified by plan 2026-11-24 15:46** (`verify-spec`, run `1795547986`). Plan wrote both from the
worker's brief after reading `sendMessage` and `checkGraphGatedDispatch`: `architecture.md`'s
"Atlassian road is not yet judged the same way" sentence is replaced by the tracker-write road
paragraph, and `agent-bus.md` has the gate rule's run-time half and a refusal-row table. Review
returned 0 must-fix, 0 should-fix, 1 nit at 15:44 — the paragraph said one approval authorizes one
node's payload, where an approval covers the nodes in its gate's territory; plan corrected the
sentence at verification, after that review. Found while writing: `commit-authority-refused`
records only the sender, with the reason on stderr and not in the row; it is documented as it is
and not changed. Phase 4 is 2/2.

### Phase 5: Integration test

- [x] Extend `scripts/test-gate-authority.sh` (or create `scripts/test-atlassian-gate.sh`) against the
      same scratch daemon: a gated `jira-write` graph
- [x] Test: with the gate unapproved, `retry --from` the write node → refused, plan's inbox empty,
      refusal row logged
- [x] Test: an authorized non-creator approval releases the gate → plan's inbox holds the dispatch with
      its provenance
- [x] Test: a hand-written `daemon` `jira-write` naming a real run and node but a different payload,
      and one carrying no run or node, are each refused with a `tracker-write-refused` row
- [x] Negative control: an `edit`-sent `jira-write` in the same session is delivered untouched
- [x] Negative control: `validateGates` still rejects an ungated `jira-write` node
- [x] Control: a muxcode built from a scratch copy with the send-time check removed delivers the
      unapproved dispatch, which the first test rejects
- [x] Coverage floor so a skipped section cannot report green
- [x] Run the script and record pass/fail counts in this spec

#### Phase 5 results (2026-11-24)

`bash scripts/test-atlassian-gate.sh`, run by the run agent at 15:47 (request
`1795553200-spawn-5f179934-2fc6f0ec`): **21 passed, 0 failed, floor 21, exit 0.** Read by plan from
the run agent's history row, watch's check of the log, and the log itself (`/tmp/mux-atlgate.log`,
written 15:47:03 — after the script's last edit at 15:46:37; a `/tmp` file, so this table is the
record). Plan did not run it.

| Section | Checks | What passed |
|---------|-------:|-------------|
| 1 — `validateGates` | 2 | An ungated `jira-write` graph is rejected, naming `wait_human`; the gated one validates |
| 2 — `retry --from` below an unapproved gate | 4 | The retry is accepted, the write node fails because its dispatch was refused, plan's inbox is empty, and `tracker-write-refused` names the run and node |
| 3 — an authorized approval | 4 | A non-creator's approval is accepted, plan's inbox holds the node's `jira-write`, its `Origin:` line names the run, and no refusal row is written |
| 4 — hand-written `daemon` writes | 5 | A forged payload naming the real approved run and node is refused, its row naming the payload mismatch; one with no run or node is refused, its row naming the missing provenance; neither reached plan |
| 5 — an `edit`-sent `jira-write` | 2 | Sent, and delivered to plan untouched |
| Private tmux server | 1 | The scratch session lives on it |
| 6 — control | 3 | A muxcode built from a copy with the send-time check removed builds, and delivers the unapproved dispatch — so section 2 can see the defect |

**Verified by plan 2026-11-24 15:50** (`verify-spec`, run `1795547986`): nothing in the tree but
`CLAUDE.md`'s scripts row changed after the 15:47 run — the script was last edited 15:46:37 and the
check itself (`bus/tracker_authority.go`) 15:27:43. `./build.sh` and `./test.sh` passed at 15:48
(2 modules, 0 failed), and review returned 0 must-fix, 0 should-fix, 0 nits at 15:48, also accepting
the gate-territory sentence plan corrected in `docs/architecture.md` after the Phase 4 review.
`CLAUDE.md` names the script. Phase 5 is 9/9.

It is a new script, not an extension of `scripts/test-gate-authority.sh`: the control needs a build
from a copy of the tree, which that script does not do.

## Notes

- Filed from MUX-144's own analysis (its *second occurrence*, 2026-09-03 01:19) and edit's Phase 4
  note that the path is **not symmetric** with commit — there is no sender to re-judge, so the fix is a
  new check, not an extension of an existing one. That asymmetry is why this is its own spec.
- MUX-165's live incident (2026-09-09: a user-approved `update-gate` whose dispatch plan declined) is
  the *cost* of the current shape; this spec is the *control* that lets MUX-165 lower it safely. Their
  overlap is deliberate and recorded: MUX-165 already carries the provenance mechanism in its fix, and
  this spec exists so the refusal half is a tracked requirement with its own pins, not a side effect.

## Status

**Complete** — 28/28, closed out 2026-11-24 by plan (`close-spec`, `55-defect-backlog-loop` run
`1795547986`). All five phases verified (3/3, 6/6, 1/1, 2/2, 9/9) and all 7 criteria met.
Uncommitted at close-out; the run's ship step commits it.

**What shipped.** A graph tracker write — `jira-write`, `confluence-write`, `issue-write` — sent by
`daemon` is judged at the send (`CheckTrackerWriteForMessage`, `bus/tracker_authority.go`): it must
name its run and node, be exactly that node's dispatch, and sit behind a `wait_human` gate with an
audited, authorized approval, or it is refused before plan's inbox and logged
`tracker-write-refused`. The commit road and this one share one judgement
(`checkGraphGatedDispatch`). Edit's relays and reads are untouched. Proved end to end by
`scripts/test-atlassian-gate.sh`, 21 passed, 0 failed, floor 21, with a control built without the
check.

**Shipped with it, from MUX-165:** plan's rule change in `agents/planner.md` and `CLAUDE.md` — the
three consent paths. MUX-165 stays in `backlog/` with what is left of it: its own origin-line test
and its integration script.

**Left for the user.** Raised with edit at Phase 3 and not answered: the new rule adds "typed into
your pane" as a consent path, and drops plan's `muxcode graph status` re-check on the grounds that
delivery is the verification — which does not hold for a dispatch from a daemon on an older build,
or for a line written straight into plan's inbox file. `commit-authority-refused` still records only
the sender, where the new row names run, node and reason. This is corroboration and a cost against
a same-uid agent, not a boundary. Plan verified by reading code, logs and history rows, and ran
nothing itself.

**History of the count:** 28/28 was reached 2026-11-24 15:50, when Phase 5 was verified. Raised with edit at Phase 3: the new plan rule adds a "typed into your pane" consent path and drops the `graph status` re-check. Activated 2026-11-24 by `55-defect-backlog-loop` run
`1795547986-55-defect-backlog-loop-8444bd99` (Defects rank 2; rank 1, MUX-215, is parked). Five
phases. Filed 2026-09-13. Ships together with MUX-165 (`⇄`): this spec owns the verifier, MUX-165
the rule change — and Phase 3 here puts MUX-165's rule change (`agents/planner.md`, `CLAUDE.md`) in
this spec's change set, so the run's one ship for this defect carries part of MUX-165, which stays
in `backlog/` until it is selected itself.

Parked 2026-11-13 on an unrecorded seam; **unparked 2026-11-24** on the user's decision — seam (a),
at the send, one verifier for both specs. The Context tables' file and line references are as filed
at `23d2804`; the Decisions section carries what was re-read on 2026-11-24.
