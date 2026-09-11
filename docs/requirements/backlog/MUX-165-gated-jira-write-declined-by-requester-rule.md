# A Gate-Approved Jira Write Is Declined Because the Rule Checks the Messenger, Not the Consent

A `wait_human` gate is the graph's consent instrument: a person releases it, the executor verifies
the person (`CheckGateApprovalAuthority`), and the nodes behind it run. Plan's Jira rule has a
different consent instrument: a write happens only on "an explicit user-initiated request relayed
from the edit agent". The two meet at every template that puts a `jira-write` node behind a gate,
and there the graph loses. On 2026-09-09 the user approved `update-gate` on a `story-to-spec` run;
the executor dispatched `jira-update` to plan as the daemon, carrying the words "The user approved the
tracker update"; plan declined it — correctly under its rule, because a node *saying* the user
approved is an agent's claim — and the run failed. The GitHub sibling behind the same gate succeeded.
The user then consented twice more for the same update: edit relayed the *identical* gate-approval
fact and plan accepted it; the user typed the fuller request into plan's pane and plan did that too.
One decision, three consent acts, one failed run.

## Context

### Observed (2026-09-09, session `is-advising-gateway`, run `1788964667-story-to-spec-5f4567ad`)

| Time | Row / record | Meaning |
|------|--------------|---------|
| 10:37:47 | `graph-run-created … started by edit` | `story-to-spec` for PROMGT-623 |
| 10:50:12 | `graph-gate-approved … "update-gate" approved by user` | the person consented; `approvals/update-gate.approved` = `{"approved_at":1788965412,"approved_by":"user"}` |
| 10:50:14 | `graph-node-start jira-update (send)`, `issue-update (send)` | two siblings behind the gate |
| 10:50:14 | task `1788965414-daemon-bb6e14a4`: `daemon → plan`, action `jira-write`, payload "The user approved the tracker update: …" | the dispatch carries **no run id, gate id, approver or timestamp** — only the template's prose |
| 10:50:26 | `issue-update → success` | commit posted the GitHub comment on the gate's say-so |
| 10:53:25 | `jira-update → failure`; node output `DECLINED - not executed. Requester was daemon (graph node), not edit; plan writes to Jira only on an explicit user request relayed from edit, and a node reporting 'the user approved' is not that relay … EXIT=1` | plan applied its rule; it offered options (a) comment / (b) description sync "on relay" |
| 10:53:25 | `graph-run-failed: node jira-update failed with no live edge` | the run is dead; the story never got its doc reference from the run |
| 10:53:50 | task `1788965630-edit-0089d49d`: `edit → plan`, `jira-write`, "USER-AUTHORIZED RELAY: the user approved wait_human gate update-gate on graph run … Execute option (a) only" | edit re-stated the **same approval** as a relay; plan executed (a) |
| ~10:55 | user typed `update jira story from spec` in plan's pane | plan did (b), the description sync, on the direct request |

The third act is the UX cost edit asked to have recorded: the approved gate did not count, a retyped
request did — and the relay in between changed nothing about the consent, only who carried it.

### Mechanism — verified in code

- `bus/graph_exec.go:853–855` — a send node becomes `NewMessage(graphSender, n.Role, "request",
  n.Action, msg, "")`: sender `daemon`, payload the interpolated node text, no metadata. The
  approval that released the gate is not on the message in any form the receiver could check.
- `bus/gate_authority.go` — `CheckGateApprovalAuthority` verifies the *approver* at approval time
  (default: a person, no agent; sealed at daemon start) and `approvals/<gate>.approved` records it.
  Consent is verified once, at the gate, then discarded at dispatch.
- `bus/graph.go:151–175, 568–577` — `gatedAtlassianActions` (`jira-write`, `confluence-write`) must
  sit downstream of a `wait_human` gate or `graph validate` fails ("fires a git/Atlassian mutation
  without an upstream wait_human gate"). `story-to-spec` satisfies this; validation cannot see that
  the node will dead-end at the receiver.
- `agents/planner.md:48–49, 164` and `CLAUDE.md:109` — "Write ONLY on an explicit user-initiated
  request relayed from the edit agent"; the actions table: "Only edit may originate this". The rule
  names a **sender** as its test. A daemon-originated dispatch fails it by identity, whatever it
  carries; an edit-originated relay passes it by identity, whatever it carries.
- `bus/atlassian_authority.go` — `CheckAtlassianAuthority` gates the CLI by role (plan) and says so:
  "we cannot verify 'the user asked for this' from inside a CLI call". It is neutral here.
- `bus/graph_templates.go:110–112` — `update-gate` → `jira-update` (plan, `jira-write`) and
  `issue-update` (commit, `issue-update`). Commit has no relay rule, so the same gate is sufficient
  consent for a GitHub mutation and insufficient for a Jira one.
- The rule's letter also omits a third path the same day exposed: a request typed directly into
  plan's pane is the user's own words with no relay at all, and plan had to reason past "relayed from
  edit" to honour it.

### Scope boundary

Upstream of this spec: who may *open* a gate ([MUX-144](./MUX-144-wait-human-gate-openable-by-any-agent.md),
parked at 17/33). This spec is about what an opened gate is worth downstream, and it must not widen
MUX-144's authority: the provenance a dispatch carries is only as good as the approval record it
points at. Not in scope: the Atlassian CLI gate itself (`CheckAtlassianAuthority` stays role-based),
the ADF payloads, or making commit's GitHub writes stricter.

## Requirements

### Acceptance criteria

- [ ] A `jira-write` or `confluence-write` dispatched by the executor behind an approved `wait_human` gate carries verifiable gate provenance — run id, gate node id, `approved_by`, `approved_at` — and plan executes it without a relay or a retyped request: the 2026-09-09 run completes at `jira-update`
- [ ] Plan verifies provenance against the run's approval record through a bus-side check (not by reading the payload's prose): a dispatch whose text says "the user approved" but carries no provenance, or whose provenance does not match `approvals/`, is declined exactly as today
- [ ] The verifier applies the same actor rule as `CheckGateApprovalAuthority`: an approval recorded by an actor outside the sealed gate authority is not consent, so an agent that could write an approval file cannot mint provenance
- [ ] Consent is verified once per decision: the same gate approval accepted from the daemon is not re-requested from edit or the user; edit's relay of a gate approval becomes unnecessary and the definition says so
- [ ] Plan's rule text enumerates the consent paths it honours — typed into plan's pane, relayed from edit carrying the user's words, or a gate-approved dispatch with verified provenance — and names what it still refuses: any agent's bus message or unverified node text
- [ ] `graph validate` rejects a builtin or user template whose Atlassian write node would be dispatched without provenance, so a template cannot pass validation and dead-end at plan
- [ ] Docs: `docs/architecture.md` authority-gates paragraph and `docs/agent-bus.md` gate reference name provenance as the second half of the gate rule; `CLAUDE.md` Atlassian-authority bullet gains the clause

### Technical approach

**Primary — carry the consent, verify it at the receiver.** The executor already knows everything the
receiver needs: at dispatch of a node downstream of a gate, read `approvals/<gate>.approved` and
attach `{run_id, gate, approved_by, approved_at}` to the message — a `Provenance` field on
`Message` (preferred: the payload prose stays untouched and `validatePayload` limits are unaffected)
or, failing that, a fixed header line the bus strips before display. A verifier
`VerifyGateProvenance(session, msg) (Approval, error)` re-reads `run.json` (the node is downstream
of that gate in this run), the approval file (present, same `approved_by`/`approved_at`), and
applies `CheckGateApprovalAuthority`'s actor rule to `approved_by`. Expose it as
`muxcode graph provenance <task-id>` (exit 0 with the approval, non-zero with the reason) so plan's
definition can name one command as the test, and so a person can audit a write after the fact. Plan's
`jira-write`/`confluence-write` handling: from `edit` → the relay path as today; from `daemon` →
run the verifier, execute on success, decline with the verifier's reason on failure; from any other
sender → decline. The single-use approval rule stays with the gate (MUX-132): provenance points at
an approval, it does not consume one.

**Rejected — route the write through edit.** Make the template send `jira-update` to edit, which
relays to plan. That is what happened by hand on 2026-09-09, and it laundered the gate approval into
a relay without adding any verification: it passes the sender test, not a consent test, and it
serialises every tracker write through the pane the user is talking in.

**Rejected — drop Jira nodes from templates.** Leaves the user consenting twice for every run that
touches the tracker, which is the cost this spec exists to remove.

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/graph_exec.go` | send-node dispatch (853–877): where provenance is attached |
| `tools/muxcode/bus/gate_authority.go` | `CheckGateApprovalAuthority`, the actor rule the verifier reuses |
| `tools/muxcode/bus/graph_run.go`, `graph.go` | `approvals/` records, `gatedAtlassianActions` (151), the gate-upstream validator (568–577) — extended with the provenance requirement |
| `tools/muxcode/bus/message.go` | `Message` — the `Provenance` field |
| `tools/muxcode/cmd/graph.go` | `graph provenance <task-id>` |
| `tools/muxcode/bus/graph_templates.go` | `story-to-spec` `jira-update` (111) and every other Atlassian write node |
| `tools/muxcode/bus/atlassian_authority.go` | unchanged role gate; its comment's "cannot verify the user asked" is where the provenance story is told |
| `agents/planner.md` (edit's file), `CLAUDE.md:109` | the rule text: three consent paths |
| `docs/architecture.md:300`, `docs/agent-bus.md:1717` | authority gates — provenance as the downstream half |
| `scripts/test-graph-orchestrator.sh` | existing graph integration script to extend, or a sibling |

## Implementation

### Phase 1: Provenance on the dispatch

- [ ] `Message.Provenance` (`run_id`, `gate`, `approved_by`, `approved_at`), serialised with the message and shown by `muxcode inbox` as one line
- [ ] Executor: at dispatch of a node downstream of an approved gate, read the approval record and attach it; nodes with no gate upstream carry none
- [ ] Unit tests: a gated send carries provenance matching `approvals/`; an ungated send carries none (negative control); a gate approved by an actor outside the sealed authority yields provenance the verifier rejects

### Phase 2: The verifier and plan's rule

- [ ] `VerifyGateProvenance` in `bus/`: run exists, node downstream of the gate in `run.json`, approval file matches, `approved_by` passes the gate actor rule; typed reasons for each failure
- [ ] `muxcode graph provenance <task-id>`: exit 0 and the approval on success, non-zero and the reason otherwise
- [ ] Plan's definition (`agents/planner.md`, via edit) and `CLAUDE.md:109`: the three consent paths and the one command that tests the daemon path; the `jira-write`/`confluence-write` action rows no longer say "Only edit may originate this"
- [ ] `graph validate`: an Atlassian write node must be downstream of a gate **and** the executor must be able to attach provenance for it; a template failing the second half names the node

### Phase 3: Docs

- [ ] `docs/architecture.md` authority-gates paragraph: the 2026-09-09 double consent as the incident, provenance as the fix
- [ ] `docs/agent-bus.md`: `graph provenance` in the command table and one sentence at the gate rule (1717)

### Phase 4: Integration test

- [ ] `scripts/test-graph-gated-jira-write.sh` (hermetic: scratch `BUS_SESSION`, scratch graph dir, `muxcode atlassian` shimmed on `PATH` to record calls without a network): a `wait_human → jira-write` graph, approved as the user → the dispatch carries provenance and `graph provenance` exits 0
- [ ] Negative controls in the same script: the same dispatch with provenance stripped → non-zero and declined; an unapproved gate → the node never dispatches; an approval file written by hand as an agent actor → verifier rejects
- [ ] The builtin `story-to-spec` template validates under the new rule and its `jira-update` node is dispatched with provenance
- [ ] Run the script and record pass/fail counts in this spec

## Notes

- Filed 2026-09-09 11:12 by plan on the user's request relayed by edit (1788966419, addendum
  1788966450), from the run's own store (`graphs/1788964667-story-to-spec-5f4567ad/{nodes,approvals}`),
  both `jira-write` task records and the session lifecycle log — not from a retelling. Plan's decline
  text is quoted from `nodes/jira-update.json` verbatim in its opening sentence.
- The decline was the rule working as written, and the rule's origin is real: the one prior
  unauthorized Jira write came from a definition that told plan to sync Jira on every spec change.
  This spec keeps that guard — unverified node text is still an agent's claim — and gives the graph a
  way to carry the consent it already verified.
- Related: [MUX-144](./MUX-144-wait-human-gate-openable-by-any-agent.md) (who may open a gate —
  upstream, parked); [MUX-132](../completed/MUX-132-graph-retry-launders-gate-approval.md)
  (single-use approvals, which provenance must not re-spend); [MUX-148](./MUX-148-node-outcome-reads-command-ran-as-task-done.md)
  (the evidence half of the same authority story); [MUX-109](../completed/MUX-109-prompt-mode-graph-control-pane.md)
  (`CheckPromptAuthority` — the same "whose words count as the user's" question at the Prompt surface).

## Status

**Backlog** — 0/20. Filed 2026-09-09 11:12.
