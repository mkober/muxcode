# A Gate-Approved Jira Write Is Declined Because the Rule Checks the Messenger, Not the Consent

**Tracking:** [mkober/muxcode#126](https://github.com/mkober/muxcode/issues/126)

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

Upstream of this spec: who may *open* a gate ([MUX-144](../completed/MUX-144-wait-human-gate-openable-by-any-agent.md),
parked at 17/33). This spec is about what an opened gate is worth downstream, and it must not widen
MUX-144's authority: the provenance a dispatch carries is only as good as the approval record it
points at. Not in scope: the Atlassian CLI gate itself (`CheckAtlassianAuthority` stays role-based),
the ADF payloads, or making commit's GitHub writes stricter.

## Decisions (user, 2026-11-24)

The user's decision, relayed by edit. This spec was parked because it ships together with
[MUX-181](../completed/MUX-181-graph-atlassian-write-judged-on-configuration-not-gate.md), whose seam was
undecided.

| Question | Decision |
|----------|----------|
| Where the consent is verified | **Seam (a) — at the send**, beside `CheckCommitAuthorityForMessage`, keyed on the raw sender `daemon` plus the gate approval the message's `GraphRun`/`GraphNode` lead to |
| One mechanism or two | **One verifier serves both specs.** Plan's rule loosens to accept gate-approved writes |

**Division of the work.** MUX-181 owns the **verifier** (the send-time check, its refusal row, the
inverted pins). This spec owns the **rule change**: plan's definition (`agents/planner.md`), the
`CLAUDE.md` Atlassian-authority clause, and the statement of which consent paths plan honours. The
two land in one change set, the verifier first.

**What that removes from this spec as filed.** A receiver-side verifier, a `Message.Provenance`
field, the `muxcode graph provenance <task-id>` command and a validator rule about "dispatchable
without provenance" were this spec's original approach. Under seam (a) none is built: the message
already carries its run and node (`Message.GraphRun`/`GraphNode`, MUX-144 Phase 4), every executor
dispatch is stamped with them, and a dispatch whose gate approval does not hold is refused before it
reaches plan's inbox.

**Found in code at the decision (2026-11-24):**

- `agents/planner.md:179` already tells plan to honour a gate-dispatched `jira-write`/`issue-write`
  once `muxcode graph status <run>` shows the gate approved (commit `b8bfcab`). So the 2026-09-09
  decline would not recur under today's definition — but the test plan applies is a status read it
  performs by hand, not a check the bus made. This spec's remaining work is to restate the rule on
  MUX-181's verifier once that exists. Whether a `10-story-to-spec` run now completes at
  `jira-update` has not been re-run.
- `issue-update` in `10-story-to-spec` now dispatches to **plan** as `issue-write`
  (`bus/graph_templates.go:192`), not to commit as the Mechanism section records; GitHub issue
  writes sit behind the same gate list (`gatedTrackerWriteActions`, `bus/graph.go:209`). The rule
  change covers `issue-write` alongside the two Atlassian actions.

## Requirements

### Acceptance criteria

- [ ] A `jira-write`, `confluence-write` or `issue-write` dispatched by the executor behind an approved `wait_human` gate is executed by plan without a relay or a retyped request: a `10-story-to-spec` run of the 2026-09-09 shape completes at `jira-update`
- [ ] Plan's acceptance rests on MUX-181's send-time verifier, not on the payload's prose and not on a status read plan performs by hand: the definition says a `daemon` tracker write that was delivered has had its gate approval verified by the bus, and names the origin line (`graph run … · node …`) plan reads to tell such a dispatch from any other
- [ ] A tracker-write request from any sender other than `edit` or a graph dispatch is declined exactly as today — a non-edit agent's bus message is never consent
- [ ] Consent is verified once per decision: the same gate approval accepted from the daemon is not re-requested from edit or the user; the definition says edit's relay of a gate approval is unnecessary
- [ ] Plan's rule text enumerates the consent paths it honours — typed into plan's pane, relayed from edit carrying the user's words, or a gate-approved graph dispatch — and names what it still refuses: any other agent's bus message, and node text claiming approval on a message that is not a graph dispatch
- [ ] The rule change lands only together with MUX-181's verifier — never ahead of it
- [ ] Docs: `docs/architecture.md` authority-gates paragraph names the three consent paths; `CLAUDE.md` Atlassian-authority and Plan-agent-scope bullets gain the clause

### Technical approach

**Primary — verified at the send, honoured at the receiver.** MUX-181 adds a send-time check that
refuses a `daemon`-sent tracker write unless the message is the frozen node's own work and a
`wait_human` gate over that node carries an attributable, authorized, corroborated approval — the
same questions `checkGraphCommitDispatch` asks of a commit. This spec changes what plan is told:
from `edit` → the relay path as today; from `daemon` as a graph dispatch → execute, the gate
approval is the request and the bus verified it; from any other sender → decline. The
`jira-write`/`confluence-write`/`issue-write` action rows stop saying only edit may originate them.
The single-use approval rule stays with the gate (MUX-132).

`agents/planner.md` is edit's file: plan drafts the wording in this spec, edit applies it.

**Not chosen — verify at the receiver** (this spec's approach as filed: a `Provenance` field,
`VerifyGateProvenance`, `muxcode graph provenance <task-id>`). It keeps a step plan performs by
hand between the inbox and the write, which is the behavioural step the pair of specs exists to
remove.

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

### Phase 1: What plan is shown

- [ ] Confirm, with a test, that a graph dispatch delivered to plan prints its run and node on the origin line (`FormatMessage`), and that a hand-written `daemon` message without them prints none — the line plan's rule will name
- [ ] Draft the rule wording here: the three consent paths, what is still refused, and the sentence that a delivered graph tracker write has been verified at the send

### Phase 2: Plan's rule (after MUX-181 Phase 2)

- [ ] `agents/planner.md` (applied by edit): the graph-dispatch row drops "confirm with `muxcode graph status`" in favour of the bus's verification, keeps `EXIT=1` for anything that is not a graph dispatch; the `jira-write`/`confluence-write`/`issue-write` action rows no longer say only edit may originate them; the consent paths are listed once
- [ ] `CLAUDE.md` Atlassian-authority and Plan-agent-scope bullets: "relayed from edit" becomes "relayed from edit, or a gate-approved graph dispatch verified at the send"
- [ ] The skills that restate the rule (`jira-manage-issues`, `jira-pr-comment`, `story-lifecycle`) are read; any that says a bus message from the daemon is never consent is brought into line
- [ ] Negative control, in the definition's own words: a `jira-write` from `build`, `test`, `review` or `commit` is declined naming who asked

### Phase 3: Docs

- [ ] `docs/architecture.md` authority-gates paragraph: the 2026-09-09 double consent as the incident, send-time verification plus plan's rule as the fix
- [ ] `docs/agent-bus.md`: one sentence at the gate rule — an approved gate is sufficient consent for the tracker write behind it

### Phase 4: Integration test

- [ ] `scripts/test-graph-gated-jira-write.sh` (hermetic: scratch `BUS_SESSION`, scratch graph dir, a private tmux server, `muxcode atlassian` shimmed on `PATH` to record calls without a network, the script playing plan by the definition's rule): a `wait_human → jira-write` graph approved as the user → plan's inbox holds the dispatch with its origin line, the shim records one write, the node succeeds and the run completes
- [ ] Negative controls in the same script: an unapproved gate → the node never reaches plan's inbox (MUX-181's refusal row present) and the shim records nothing; a `jira-write` sent by `build` → delivered, declined by the rule, the shim records nothing
- [ ] The builtin `10-story-to-spec` template validates and, with its gate approved, its `jira-update` and `issue-update` nodes both reach plan
- [ ] Coverage floor so a skipped section cannot report green
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
- Related: [MUX-144](../completed/MUX-144-wait-human-gate-openable-by-any-agent.md) (who may open a gate —
  upstream, parked); [MUX-132](../completed/MUX-132-graph-retry-launders-gate-approval.md)
  (single-use approvals, which provenance must not re-spend); [MUX-148](../completed/MUX-148-node-outcome-reads-command-ran-as-task-done.md)
  (the evidence half of the same authority story); [MUX-109](../completed/MUX-109-prompt-mode-graph-control-pane.md)
  (`CheckPromptAuthority` — the same "whose words count as the user's" question at the Prompt surface).

## Status

**Backlog** — unparked 2026-11-24 on the user's decision (seam (a); one verifier, owned by MUX-181;
this spec owns plan's rule change). Ships together with MUX-181.

**2026-11-24 — part of this spec is already in the tree, and nothing here is ticked for it.**
MUX-181 closed out the same day, and its Phase 3 carried this spec's rule change: `agents/planner.md`
and `CLAUDE.md` now name the three consent paths (uncommitted when this note was written; it ships
with MUX-181). So most of Phase 2 here, and part of Phase 3, describe work that exists. Whoever
activates this spec verifies those steps against the tree rather than building them again. Not done
anywhere: Phase 1 (what plan is shown — the origin-line test) and Phase 4 (this spec's integration
script). Open with the user from MUX-181's verification: whether the rule should keep plan's
`muxcode graph status` re-check, and the "typed into your pane" consent path. Filed 2026-09-09
11:12. The Mechanism and Key files sections are as filed; the Decisions section carries what was
re-read on 2026-11-24.
