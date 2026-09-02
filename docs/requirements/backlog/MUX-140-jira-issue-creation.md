# MUX-140: Jira Issue Creation From MuxCode

**Tracking:** [mkober/muxcode#69](https://github.com/mkober/muxcode/issues/69)

## Context

The `muxcode atlassian jira` CLI can do almost everything to an issue that already exists and
**nothing whatsoever to bring one into existence**. A spec-to-tracker flow therefore dead-ends: plan
can write the description, link the dependencies, transition the status and comment the PR — but the
issue has to be hand-created in the Jira UI first, by the user, before any of that is reachable.

### Observed 2026-09-02

A user request to file five stories from a spec brief could not be executed. The stories were
well-formed and the brief was sound; the tooling simply has no verb for it. `create-subtask` was the
only creation action available, and it was wrong on two counts: it produces a **Sub-task**, not a
Story, and it requires a parent the stories did not share (one belonged to a different epic, another
to core observability). "Assign to Mark Koberlein" and "put it in the current sprint" were
unreachable for the same reason — no action exists for either.

That is a **missing capability, not a failure**: there was no auth error to retry and no HTTP body to
quote. `mcp__claude_ai_Atlassian__createJiraIssue` was available in the environment and would have
done it in one call; it was declined under the standing CLI-only rule. This spec closes the gap so
the rule stops costing the user real work — **the fix is to give the sanctioned path the capability,
never to relax the rule.**

### Evidence (verified 2026-09-02 against the tree at `6a05bc8`)

| Claim | Where |
|-------|-------|
| The complete `jira` surface is 12 actions — `read`, `update`, `comment`, `comments`, `link-types`, `link`, `transitions`, `transition`, `search`, `create-subtask`, `worklog`, `attach`. **No `create`.** | `cmd/atlassian.go:78-241` |
| `create-subtask` hardcodes `issuetype: {name: "Sub-task"}` and requires a `parent` | `bus/atlassian.go:823-895` |
| It POSTs to `/rest/api/3/issue` — **the same endpoint a top-level create uses** | `bus/atlassian.go:872` |
| Assignee is **read-only**: parsed for display, never written | `bus/atlassian.go:181`, `:236`, `:262` |
| No Agile API anywhere in the tree — sprint/board work needs `/rest/agile/1.0`, a different base path from `/rest/api/3` | `bus/atlassian.go` (absent) |
| The authority gate is a **fail-closed read-only allowlist** — anything not listed counts as mutating | `bus/atlassian_authority.go:158-186` |

**The capability is one field away.** `JiraCreateSubtask` already performs an authenticated POST to
the exact endpoint, marshals ADF, handles the 201, and parses the created key. A top-level create is
that function without the `parent` field and with a caller-supplied issue type. This is a
generalization of working code, not new infrastructure — which is worth stating plainly, because the
size of the gap in *capability* is wildly out of proportion to the size of the gap in *code*.

### Authority: no change needed, and that is load-bearing

`IsAtlassianMutatingAction` treats an unrecognised action as mutating
(`atlassian_authority.go:180-186`), and `create` is not in `atlassianReadOnlyActions`. So the new verb
is **plan-only and user-initiated the moment it exists**, with no edit to the authority file.

That is the correct default and it arrives for free — but it is exactly the kind of property that
breaks silently later, when someone tidying the allowlist adds `create` to it, reasoning that
"creating isn't editing". Phase 1 pins it with a test so the regression is loud.

The scope rule is unchanged and applies in full: **plan writes only on an explicit user-initiated
request relayed from edit, never as a side effect of a spec or docs change.** Creating five issues
because a spec mentions five work items is precisely the failure this rule exists to prevent.

## Requirements

### Acceptance criteria

- [ ] `muxcode atlassian jira create` creates a **top-level** issue: `--project`, `--type`, `--summary`, optional `--description <adf.json>`, `--parent` (epic link), `--labels`, `--priority`
- [ ] Issue type is validated against the project's `createmeta` **before** posting; an invalid type fails with the list of types the project actually offers, not a raw HTTP 400
- [ ] `--assignee` accepts an email or display name and resolves it to an `accountId`; **an ambiguous match refuses and prints the candidates** rather than guessing a person
- [ ] `--sprint current|<id>` places the issue in a sprint via the Agile API; `current` resolves through the board's active sprint
- [ ] `--board <id>` (or config default) supplies the board for `current`; a missing board fails with a clear message rather than silently skipping placement
- [ ] Batch: `jira create --from <file>` creates N issues from one file and reports each created key
- [ ] **A partially-failed batch is resumable, never silently half-done** — see [Decision 1](#decision-1-a-batch-is-resumable-because-jira-cannot-roll-one-back)
- [ ] `--dry-run` prints the exact payload per issue and creates nothing
- [ ] Output names the created key **and its browse URL**, so the user can open it without constructing the link
- [ ] `create`, and any assignee/sprint mutation, are gated by `CheckAtlassianAuthority` — pinned by a test asserting `IsAtlassianMutatingAction("jira", "create")` is true, so adding it to the read-only allowlist fails the suite
- [ ] The `jira-manage-issues` skill documents the new surface — without it plan cannot know the verb exists (see [Decision 2](#decision-2-the-skill-file-is-part-of-the-feature))
- [ ] Docs: [`docs/agent-bus.md`](../../agent-bus.md), [`docs/configuration.md`](../../configuration.md) for any new config keys, CLAUDE.md's Atlassian bullet

#### Decision 1: a batch is resumable, because Jira cannot roll one back

Jira has **no transactional multi-create**. Five issues are five POSTs, and a failure on the third
leaves two real issues in the tracker that a naive retry would duplicate. Silent partial creation in
a shared system is the same irreversible-partial-state class as
[MUX-135](./MUX-135-spawn-seed-record-gc-strands-completion.md), and duplicates are worse than a
failure because a human has to find and close them.

- [ ] Each batch writes a **receipt** mapping input index → created key as it goes, before the next POST
- [ ] Re-running the same batch file **skips entries that already have a receipt** and creates only the remainder
- [ ] A failed batch reports created keys, the failed index with its error, and the exact resume command
- [ ] Test: a batch failing at index 3 of 5, re-run, yields **5 issues total, not 8**

#### Decision 2: the skill file is part of the feature

`skills/jira-manage-issues.md` enumerates the CLI surface, and it is how the plan agent learns what
Jira commands exist. A `create` verb shipped without a skill update is a verb no agent will ever
invoke — the code would be reachable by a human at a terminal and invisible to the role that holds
the authority to use it. The skill update is an acceptance criterion, not documentation follow-up.

### Technical approach

| Area | Change |
|------|--------|
| `bus/atlassian.go` | `JiraCreateIssue(cfg, opts JiraCreateOptions) (key string, err error)` — generalizes `JiraCreateSubtask`'s POST: caller-supplied issue type, optional parent, optional assignee/labels/priority. Refactor `JiraCreateSubtask` to call it with `type="Sub-task"` so there is **one** create path, not two that drift |
| `bus/atlassian.go` | `JiraCreateMeta(cfg, projectKey)` → available issue types, for pre-validation and the error message |
| `bus/atlassian.go` | `JiraResolveAccountID(cfg, query)` → `/rest/api/3/user/search`; exact-match-wins, ambiguity refuses with candidates |
| `bus/atlassian_agile.go` (new) | `JiraActiveSprint(cfg, boardID)`, `JiraAddIssueToSprint(cfg, sprintID, key)` — `/rest/agile/1.0`, kept in its own file because the base path and response shapes differ from `/rest/api/3` |
| `bus/atlassian_batch.go` (new) | Batch parse, receipt read/write, resume |
| `cmd/atlassian.go` | `create` case, flag parsing, `--dry-run`, `--from` |
| `bus/atlassian_authority.go` | **No change** — verified fail-closed; a test pins that |
| `skills/jira-manage-issues.md` | Document `create`, batch, assignee and sprint flags |

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/atlassian.go` | `JiraCreateIssue`, createmeta, account resolution |
| `tools/muxcode/bus/atlassian_agile.go` (new) | Agile API — board and sprint |
| `tools/muxcode/bus/atlassian_batch.go` (new) | batch create + resumable receipts |
| `tools/muxcode/cmd/atlassian.go` | `create` subcommand and flags |
| `tools/muxcode/bus/atlassian_authority.go` | unchanged; regression test target |
| `skills/jira-manage-issues.md` | agent-facing surface documentation |
| `scripts/test-jira-create.sh` (new) | integration test against a stub Jira |

## Implementation

### Phase 1: `jira create` core

- [ ] `JiraCreateOptions` + `JiraCreateIssue`; `JiraCreateSubtask` refactored to call it (one create path)
- [ ] `JiraCreateMeta` pre-validation; invalid type error names the project's available types
- [ ] `create` subcommand with `--project`, `--type`, `--summary`, `--description`, `--parent`, `--labels`, `--priority`
- [ ] `--dry-run` prints the payload and posts nothing
- [ ] Output includes the created key and browse URL
- [ ] **Authority test**: `IsAtlassianMutatingAction("jira", "create")` is true, and a non-plan role is refused — fails loudly if `create` is ever added to the read-only allowlist
- [ ] Tests: minimal create, with-description, with-parent, invalid type, missing config

### Phase 2: Assignee resolution

- [ ] `JiraResolveAccountID` against `/rest/api/3/user/search`
- [ ] `--assignee` on create; exact match wins
- [ ] **Ambiguity refuses** with the candidate list; no-match refuses naming the query
- [ ] Tests: unique match, ambiguous refusal, no match, email vs display name

### Phase 3: Sprint and board placement

- [ ] `bus/atlassian_agile.go` with `JiraActiveSprint` and `JiraAddIssueToSprint`
- [ ] `--sprint current|<id>`, `--board <id>` with a config default
- [ ] Missing board, or no active sprint, fails clearly instead of skipping placement silently
- [ ] Placement failure after a successful create **reports the created key** so the issue is not orphaned by a confusing error
- [ ] Tests: explicit sprint id, `current` resolution, no active sprint, missing board

### Phase 4: Batch creation with resumable receipts

- [ ] Batch file format (one object per issue: type, summary, description path, assignee, labels, parent)
- [ ] Receipt written per issue **before** the next POST
- [ ] Re-run skips entries with receipts; `--from` reports created, failed and remaining
- [ ] Failure output names the resume command verbatim
- [ ] Test: **fail at index 3 of 5, re-run, total is 5 issues not 8**
- [ ] Test: a completed batch re-run creates nothing

### Phase 5: Docs and skill surface

- [ ] `skills/jira-manage-issues.md`: `create`, batch, assignee, sprint, with the user-initiated rule restated
- [ ] [`docs/agent-bus.md`](../../agent-bus.md) CLI reference
- [ ] [`docs/configuration.md`](../../configuration.md) for board/project defaults
- [ ] CLAUDE.md Atlassian authority bullet notes creation is now available and still plan-only

### Phase 6: Integration test

- [ ] Create `scripts/test-jira-create.sh` — hermetic: a **stub Jira** over loopback (no live tracker), asserting on the requests received
- [ ] Create a Story: assert one POST to `/rest/api/3/issue`, no `parent` field, correct issue type, key reported
- [ ] `--dry-run`: assert **zero** requests reach the stub
- [ ] Invalid issue type: createmeta consulted, no create POST issued, error names available types
- [ ] Assignee: unique resolves and lands in the payload; **ambiguous refuses and issues no create**
- [ ] Sprint: create then Agile POST in the right order; a sprint failure still reports the created key
- [ ] **Batch resume: stub fails index 3 of 5, re-run, assert exactly 5 create POSTs across both runs**
- [ ] **Negative control — authority:** a non-plan role is refused and **no request reaches the stub**
- [ ] Coverage floor set to the maximum achievable count so a skipped section cannot report green
- [ ] Run the script and confirm all checks pass

## Related

| Spec | Relationship |
|------|--------------|
| [MUX-135](./MUX-135-spawn-seed-record-gc-strands-completion.md) | Same irreversible-partial-state class the batch receipts guard against |
| [MUX-137](./MUX-137-test-bus-dir-leak.md) | Hermetic-test discipline — the stub Jira exists so this suite never touches a real tracker |

## Status

Backlog
