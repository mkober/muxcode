# MUX-246: The Daemon Auto-Approves Gates on Runs Whose Creator May Self-Approve

A `wait_human` gate on a run launched by a role the user has opted into self-approval still parks,
sends edit a `request:graph-approval`, and waits for edit to run `muxcode graph approve`. The approval
is one the configuration has already granted, so the prompt is a round trip with no decision in it —
and a place for the run to stall. This spec has the daemon release such a gate itself, keeping the
gate, its single-use approval and its audit row, so `55-defect-backlog-loop`'s `ship-gate` never
prompts anyone on an agent-launched run.

## Context

### Request

User, 2026-11-27, relayed by edit: *"remove the ship gate from the defect graph loop."* Offered three
ways — delete the gate, have edit keep approving by hand, or have the daemon approve — the user chose
the third (notes: `/tmp/gate-auto-approve.md`).

### Why the gate is not simply deleted

| Constraint | Code | Effect of deleting the gate |
|------------|------|-----------------------------|
| A commit or tracker-write node must sit downstream of a `wait_human` gate | `validateGates` (`bus/graph.go`) | The template fails `graph validate` |
| A graph commit dispatch is judged on its upstream gate's recorded approval — attributable, authorized, audited | `CheckCommitAuthorityForMessage` → `gateApprovalHolds` (`bus/graph_exec.go`), MUX-144 Phase 4 | The daemon's own commit dispatch is refused, `commit-authority-refused` |

Auto-approval keeps both intact: the gate stays in the template, and a real approval record still
stands behind every commit.

### Today

| Step | Behaviour |
|------|-----------|
| Config | `MUXCODE_GATE_AUTHORITY_ROLES=user,edit` and `MUXCODE_GATE_SELF_APPROVAL_ROLES=edit`, both read from the config file only and sealed at daemon start (`SealGateAuthority`, `bus/gate_authority.go`) |
| The gate parks | The executor writes the `pending` marker and sends edit `request:graph-approval` (`bus/graph_exec.go`) |
| Edit approves | `muxcode graph approve` → `ApproveGraphGate`: `CheckGateApprovalAuthority(actor, run)` passes because `selfApprovalGranted(edit)`; one clock reading stamps the `graph-gate-approved` audit row and the `approved` marker |
| The daemon honours it | `gateApprovalHolds` re-checks the marker's `approved_by` against the sealed lists and the audit row, then spends it (single-use) |

On 2026-11-27 edit relayed run `1795811929`'s `ship-gate` to the user instead of approving it — the
configuration had already answered the question, and the run waited on a person anyway.

### Family

- [MUX-144](../completed/MUX-144-wait-human-gate-openable-by-any-agent.md) — the gate authority, the
  self-approval refusal and the daemon-side backstop this must not weaken.
- [MUX-181](../completed/MUX-181-graph-atlassian-write-judged-on-configuration-not-gate.md),
  [MUX-165](../completed/MUX-165-gated-jira-write-declined-by-requester-rule.md) — a tracker write is
  delivered to plan on its gate's approval, which `agents/planner.md` reads as the user's request.
- [MUX-166](../completed/MUX-166-run-builds-on-stale-base-no-freshness-gate.md) — `sync-gate` and
  `sync-conflict`, gates that ask a question rather than seek consent.

## Decisions

### Made (user, 2026-11-27)

| Question | Decision |
|----------|----------|
| How the ship gate stops prompting | **The daemon auto-approves a `wait_human` gate when the run's creator is in the sealed `MUXCODE_GATE_SELF_APPROVAL_ROLES`.** The gate is not deleted and edit does not approve by hand |
| Which gates (answered the same day, relayed by edit) | **All `wait_human` gates, but only on loop graphs** — `55-defect-backlog-loop` is the example — **not on other templates.** No per-node list and no named-gate list |
| 1 — whose name goes on the approval | **`daemon`.** `approvalDenial` accepts `daemon` as the approver when the sealed self-approval list names the run's creator. The creator role is not written: it made no approval. Marker and audit row agree on one clock reading, as `ApproveGraphGate` does, and neither ever reads `user` |
| 2 — what makes a template a loop graph | **An explicit template flag, `auto_approve_gates: true`,** set on `55-defect-backlog-loop`. Nothing is derived from loop edges. A copied template qualifies **only if it keeps the flag**. The run's frozen graph is what is read, as for `RequiresSpec` |
| 3 — a user-launched run | **Prompts as today.** Only a run whose recorded creator is a role in the sealed self-approval list is auto-approved |
| 4 — unverified holds | **Excluded.** A hold exists because no authoritative row proved a node succeeded; it keeps its own release rule (`unverifiedHoldReleased`) |
| 5 — gates above a tracker write (`jira-write`, `confluence-write`, `issue-write`) | **Excluded.** Such a gate parks and prompts even on a flagged template, so plan's consent path 3 (`agents/planner.md`) still rests on an approval somebody made. `55-defect-backlog-loop` holds no tracker-write node today |

All five questions this spec was filed with are answered above (relayed by edit, 2026-11-27); none is
open.

What the scope decision takes in, stated so it is not a surprise: `55-defect-backlog-loop`'s
`sync-gate`, `sync-conflict`, `ship-stuck-gate` and its other stuck gates are `wait_human` gates too.
They report a condition — a stale base, a conflicting rebase, a phase that will not complete — and
under this decision the daemon proceeds past each one on an eligible run without anyone reading it.
The lifecycle row and the event to edit (third criterion) are then the only notice.

Two points the decisions leave to the implementation, recorded so they are settled in Phase 2 rather
than assumed:

- **A gate above both a commit and a tracker write.** Decision 5 is read here as: a gate with any
  tracker-write node reachable downstream parks. No builtin has that shape on a flagged template.
- **Accepting `daemon` must not become a forgery road.** A hand-written marker reading
  `approved_by: daemon` must still fail: the acceptance holds only for a run whose frozen graph carries
  the flag, whose recorded creator the **sealed** list names, on a gate in scope, with its audit row.

## Requirements

### Acceptance criteria

- [ ] A `wait_human` gate on a run whose frozen graph carries `auto_approve_gates: true` and whose recorded creator is in the **sealed** self-approval list and holds the sealed gate authority is released by the daemon with no `muxcode graph approve` call, within one graph tick of parking
- [ ] The release writes an `approved` marker and a `graph-gate-approved` audit row on one clock reading, both naming `daemon`; `gateApprovalHolds` accepts it, and the downstream commit dispatch passes `CheckCommitAuthorityForMessage`
- [ ] **Negative control:** a hand-written `approved_by: daemon` marker is refused — on a run without the flag, on a run whose creator the sealed list does not name, and on any run with no matching audit row
- [ ] Each auto-approval writes one `graph-gate-auto-approved` lifecycle row naming run, node and creator, and edit receives an `event`, not a `request:graph-approval`
- [ ] Approval stays single-use: a loop re-entry or `graph retry` reaching the gate again is auto-approved again, with one audit row and one lifecycle row per release
- [ ] **Negative control:** a run whose creator is not in the self-approval list parks and prompts as today
- [ ] **Negative control:** a user-launched run of a flagged template parks and prompts as today
- [ ] **Negative control:** a run with no recorded creator, or `ActorUnknown`, is never auto-approved
- [ ] **Negative control:** with the config line removed and the daemons restarted, the same run parks; a mid-session edit that adds a role does not widen the sealed list
- [ ] Every `wait_human` gate on a flagged run is released this way — `ship-gate`, `sync-gate`, `sync-conflict` and the stuck gates alike; a gate on a template without the flag, launched by the same role, parks and prompts as today (**negative control**)
- [ ] `55-defect-backlog-loop` carries `auto_approve_gates: true` and no other builtin does; a copy of it keeps the behaviour only while it keeps the flag
- [ ] A gate with a tracker-write node downstream parks and prompts even on a flagged run
- [ ] An unverified hold is not auto-released
- [ ] `graph validate` still rejects a commit or tracker-write node with no upstream gate; no template loses a gate
- [ ] `bash scripts/test-gate-auto-approve.sh` passes

### Technical approach

The executor's gate step already knows the run, the node and the sealed lists. Where it would write
the `pending` marker and send edit the request, it first asks one predicate — the run's creator is
recorded, is not `user` or `ActorUnknown`, holds the sealed gate authority and is named by the sealed
self-approval list, the run's frozen graph carries `auto_approve_gates`, and the gate has no
tracker-write node downstream — and, if it holds, writes the approval as `daemon` through the same
code `ApproveGraphGate` uses (audit row first, then the marker, one clock reading), then lets the
ordinary honour-and-spend path run on the same or the next tick. Nothing in `gateApprovalHolds` is
bypassed: `approvalDenial` gains one arm — approver `daemon` is accepted exactly when that same
predicate holds for the run and gate — so the MUX-144 backstop still judges every release, and a
marker naming `daemon` anywhere the predicate fails is refused like any forged one.

The predicate reads the **sealed** lists only, in the daemon. The CLI never auto-approves: it reads
the lists unsealed from caller-controlled paths (the reason delegated cancel is filed for the daemon
rather than done in-process).

Revocation is unchanged: delete the config line and restart the daemons.

### Key files

| File | Role |
|------|------|
| `tools/muxcode/bus/gate_authority.go` | `GateSelfApprovalRoles`, `selfApprovalGranted`, `CheckGateApprovalAuthority`, `SealGateAuthority` — the lists and the predicate's home |
| `tools/muxcode/bus/graph_exec.go` | the gate step that parks and prompts; `ApproveGraphGate`; `gateApprovalHolds` / `approvalDenial`; `unverifiedHoldReleased` |
| `tools/muxcode/bus/graph.go` | the `auto_approve_gates` graph field; `validateGates` — unchanged, pinned by a test |
| `tools/muxcode/bus/graph_templates.go` | the flag on `55-defect-backlog-loop` |
| `tools/muxcode/bus/commit_authority.go`, `bus/tracker_authority.go` | the backstops the auto-approval must satisfy; tracker-write gates stay person-approved |
| `docs/agent-bus.md`, `docs/architecture.md`, `docs/configuration.md`, `CLAUDE.md` | gate authority and self-approval sections |
| `scripts/test-gate-auto-approve.sh` | new |

## Implementation

### Phase 1: Decisions and boundary

- [x] Record the user's answers to open Decisions 1–5 in this spec and rewrite the affected criteria to them — answered 2026-11-27, relayed by edit; see [Decisions](#decisions)
- [ ] Pin today's behaviour in a test: a gated run created by a self-approval role parks, sends edit `request:graph-approval`, and opens only on `graph approve`

### Phase 2: Auto-approval

- [ ] The `auto_approve_gates` graph field, parsed, exported and frozen with the run; set on `55-defect-backlog-loop` only
- [ ] One predicate in `bus/gate_authority.go` over the sealed lists, the run's recorded creator, the frozen graph's flag and the gate's downstream nodes — refusing `user`, `ActorUnknown`, an unrecorded creator, an unflagged graph and a gate above a tracker write
- [ ] The executor's gate step calls it and, when it holds, records the approval as `daemon` through the code `ApproveGraphGate` uses — audit row then marker, one clock reading
- [ ] `approvalDenial` accepts approver `daemon` exactly when the predicate holds for that run and gate, and refuses it everywhere else
- [ ] `graph-gate-auto-approved` lifecycle row per release; an `event` to edit in place of the request
- [ ] Unverified holds untouched: `unverifiedHoldReleased` never takes the auto-approval road
- [ ] Unit tests: flagged run with creator in the list → released with marker, audit row and lifecycle row; creator not in the list, `user`, unknown and unrecorded → parked; unflagged template → parked; a gate above a tracker write → parked; a re-entered gate → a second release with its own rows; an unsealed process never auto-approves; a hand-forged `daemon` marker is refused; an unverified hold is not released

### Phase 3: Docs

- [ ] `docs/agent-bus.md` and `docs/architecture.md` gate sections, `docs/configuration.md` `MUXCODE_GATE_SELF_APPROVAL_ROLES` entry, `CLAUDE.md` graph-orchestration bullet: what the opt-in now does on a flagged template, the `auto_approve_gates` field, the `daemon` approver, the lifecycle row, what stays person-approved (user-launched runs, unverified holds, tracker-write gates), and how to revoke it

### Phase 4: Integration test

- [ ] Create `scripts/test-gate-auto-approve.sh` — scratch daemon on a private tmux server, config file holding the two lists, the commit agent played by the script
- [ ] Test: a gated run created by edit with self-approval configured is released with no approve call; `graph-gate-approved` and `graph-gate-auto-approved` rows are present and the commit dispatch is delivered, not refused
- [ ] Test: a loop re-entry releases the gate again with a second pair of rows
- [ ] **Negative control:** creator not in the list → the gate parks and edit receives `request:graph-approval`
- [ ] **Negative control:** the same creator launching a gated run of a template without `auto_approve_gates` → parks and prompts; a copy of the flagged template with the flag removed → parks
- [ ] **Negative control:** a user-created run of the flagged template → parks
- [ ] **Negative control:** on a flagged run, a gate above a tracker-write node parks, and an unverified hold is not released
- [ ] **Negative control:** a hand-written `approved_by: daemon` marker on an ineligible run is refused and the commit dispatch with it (`commit-authority-refused`)
- [ ] **Negative control:** the config line removed and the daemon restarted → parks; adding a role to the file under a running daemon does not auto-approve
- [ ] **Negative control:** a build from a copy of the tree with the predicate forced false parks the first run, so the release assertion can fail
- [ ] Coverage floor; run and record counts here

## Out of scope

- Deleting or reordering any gate in a builtin template.
- Changing who holds the gate authority or how the lists are sealed.
- Auto-approval from the CLI or any process other than the daemon.

## Status

Backlog

Filed 2026-11-27 on the user's request relayed by edit. Every question it was filed with was answered
by the user the same day (Decisions, above): the daemon approves as `daemon`, on templates flagged
`auto_approve_gates`, for runs a sealed self-approval role launched; user-launched runs, unverified
holds and gates above tracker writes keep a person. Nothing is built — the one ticked box is the
recording of those answers. A feature, not a Defects row.
