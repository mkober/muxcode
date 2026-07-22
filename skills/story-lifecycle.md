---
name: story-lifecycle
description: Standard Jira story lifecycle for autonomous agent
roles:
  - agent
tags:
  - workflow
  - jira
  - autonomous
---

## Story lifecycle

When processing a Jira story, follow these phases in order. Complete each phase before moving to the next. If any phase fails, log the failure and decide whether to retry or skip to the next story.

### Git steps require authorization — they are NOT automatic

Several phases below delegate commits, pushes, and PRs to the commit agent. **Those
steps only run if this role is authorized to request git mutations.** By default it
is not: the bus rejects a `commit` request from any role except `edit`
(`CheckCommitAuthority`), and `--force` does not bypass it. This is deliberate —
git mutations are user-initiated, and a lifecycle loop that commits at every phase
boundary is exactly how a fleet produces commits nobody asked for.

To run this lifecycle fully autonomously, the user opts in explicitly:

```bash
MUXCODE_COMMIT_AUTHORITY_ROLES=edit,auto   # canonical role name for the autonomous agent
```

**Without that opt-in, do not attempt the git steps and do not treat their absence
as a failure.** Complete the work, then report what is ready and stop:

> "PBP1-4915 implementation complete, tests green, tree ready to commit."

The user decides. Never commit to "checkpoint" progress, never commit because a
phase ended, and never route a commit request through another agent to get around
this.

### Jira writes require the same authorization

The phases below also transition issues, post comments, and create links. **Those
are gated identically** — `CheckAtlassianAuthority` (`bus/atlassian_authority.go`)
allows Jira and Confluence writes from `edit` only by default, so
`muxcode atlassian jira transition|comment|link|create-subtask` returns `DENIED`
for this role. Jira is a shared system the user's team sees, and a lifecycle loop
that comments at every phase boundary is how a fleet fills a team's tracker with
noise nobody asked for.

The opt-in mirrors the git one:

```bash
MUXCODE_ATLASSIAN_AUTHORITY_ROLES=edit,auto
```

**Without it, skip the Jira write steps and do not treat them as failures.** Reads
(`read`, `comments`, `search`, `transitions`) stay available, so story selection and
context-gathering work unchanged. Report what you would have written and continue:

> "PBP1-4915 done — would transition to Done and comment the PR link, if authorized."

A `DENIED` response is the rule working, not a broken token. Do not retry it and do
not ask another agent to run the write for you.

### Phase 1: Story selection (requires user confirmation)

1. Search Jira for assigned stories: `muxcode atlassian jira search "<JQL query>"`
2. Present the results to the user as a numbered list showing: Jira key, priority, summary, and any blockers
3. Ask the user which story to work on — wait for confirmation before proceeding
4. Accept: a number from the list, a Jira key, "all" for auto-processing, or a new JQL query
5. Read the full story details: `muxcode atlassian jira read {KEY}`
6. **Check for an existing requirements doc** (see "Requirements doc priority" below)
7. Extract acceptance criteria, description, priority, and linked stories
8. Check for blockers — warn on stories with unresolved "is blocked by" links

### Requirements doc priority

**The requirements doc in the repo is the authoritative source of truth for implementation.** The Jira description is only used to create the initial requirements doc. After story selection, always check for an existing doc before proceeding:

```bash
# Check all requirements directories for a doc matching the Jira key
ls docs/requirements/drafts/{KEY}-*.md docs/requirements/completed/{KEY}-*.md docs/requirements/backlog/{KEY}-*.md 2>/dev/null
```

Based on where a doc is found:

| Location | Action |
|----------|--------|
| `drafts/{KEY}-*.md` | **Read the doc and skip to Phase 5** (implementation). The requirements are already written — use the doc as your implementation guide. Do NOT re-read the Jira description for implementation details. |
| `completed/{KEY}-*.md` | **Skip the story entirely** — it's already done. Report to the user and move to the next story. |
| `backlog/{KEY}-*.md` | **Read the doc and use it as the starting point for Phase 3** (requirements). Move it to `drafts/`, enrich with Jira context if needed, then continue with the requirements review PR. |
| Not found | **Proceed normally** from Phase 2 (branch and setup) through Phase 3 (write requirements from Jira). |

When a requirements doc exists, **read it first and follow its implementation phases, acceptance criteria, and technical approach.** The Jira description may be outdated or incomplete compared to the reviewed requirements doc.

### Progress tracking in the requirements doc

As you complete implementation phases and acceptance criteria, **update the requirements doc to reflect progress**. This keeps the doc as the single source of truth for story status.

**Check off completed items** by changing `- [ ]` to `- [x]`:

```bash
# After completing a phase step or acceptance criterion, edit the requirements doc:
# Change: - [ ] Implement validation logic
# To:     - [x] Implement validation logic
```

**Update the Status section** at the bottom of the doc as you progress:
- `Draft` → `In Progress` when starting implementation
- `In Progress` → `Complete` when all phases and criteria are done

**When to update**:
- After each implementation phase is completed (all steps checked off)
- After each acceptance criterion is verified (build passes, tests pass)
- After build/test/review cycles confirm a phase works
- Commit the updated doc along with the code changes for that phase

This ensures that if the agent is interrupted or restarted, it can read the doc, see which phases are `[x]` done vs `[ ]` pending, and resume from the right place.

### Phase 2: Branch and setup

1. Create a feature branch via commit agent: `muxcode send commit commit "Create and checkout branch feature/{KEY}-{slug}" --force --wait`
2. Transition Jira status to In Progress: first list transitions with `muxcode atlassian jira transitions {KEY}`, then execute with `muxcode atlassian jira transition {KEY} {id}`
3. Comment on Jira with work-started message: `muxcode atlassian jira comment {KEY} "Started work — branch: feature/{KEY}-{slug}"`

### Phase 3: Requirements

1. Write a requirements doc at `docs/requirements/drafts/{KEY}-{slug}.md`
2. Include: Jira context, acceptance criteria, technical approach, key files, implementation phases
3. **The final implementation phase MUST be an integration test phase** — include either specific automatable test steps as checkboxes (e.g. `- [ ] Reload agents → verify config changed`) or a step to create a `scripts/test-{feature}.sh` automation script. The test phase validates end-to-end behavior.
4. Stage, commit, and push via commit agent: `muxcode send commit commit "Stage and commit the requirements doc, push to remote" --force --wait`
5. Create a PR for requirements review: `muxcode send commit commit "Create PR titled 'Requirements: {KEY} {summary}'" --force --wait`
6. Comment on Jira with the PR link: `muxcode atlassian jira comment {KEY} "Requirements PR: {url}"`

### Phase 4: Requirements review

1. Poll PR status: `muxcode send commit pr-read "Read PR on current branch and report: review decision, CI status, review comments" --wait`
2. If `CHANGES_REQUESTED`: read feedback, update requirements doc, push changes, repeat
3. If `REVIEW_REQUIRED`: wait and poll again after the configured interval
4. If `APPROVED` and checks pass: proceed to implementation
5. If waiting longer than the max wait time: alert user via memory write and move to next story

### Phase 5: Implementation

1. **Read the requirements doc** (`docs/requirements/drafts/{KEY}-*.md`) as the implementation guide — this is the authoritative source, not the Jira description. Follow its implementation phases, acceptance criteria, key files, and technical approach.
2. **Update the doc Status to `In Progress`** if not already set.
3. For each implementation phase in the doc:
   a. Implement the code changes for that phase
   b. Delegate to build: `muxcode send build build "Run ./build.sh and report results" --wait`
   c. On build failure: fix issues and rebuild (up to max iterations)
   d. Delegate to test: `muxcode send test test "Run tests and report results" --wait`
   e. On test failure: fix issues, rebuild, and retest (up to max iterations)
   f. **Check off completed steps** (`- [ ]` → `- [x]`) in the requirements doc for that phase
   g. **Check off acceptance criteria** that are now satisfied
   h. Commit the updated requirements doc along with the code changes
4. Delegate to review: `muxcode send review review "Review changes on current branch" --wait`
5. Address review feedback if needed

### Phase 6: Implementation PR

1. Stage, commit, and push via commit agent: `muxcode send commit commit "Stage all changes, commit with message '{KEY} {summary}', and push" --force --wait`
2. Create implementation PR: `muxcode send commit commit "Create PR titled '{KEY} {summary}'" --force --wait`
3. Comment on Jira with implementation PR link

### Phase 7: Implementation review

1. Poll PR status (same as Phase 4)
2. Handle review feedback: fix code, re-push, repeat
3. Handle CI failures: fix and re-push
4. Wait for approval + passing checks

### Phase 8: Story completion

1. **Update the requirements doc**: check off all remaining items, set Status to `Complete`
2. Transition Jira to Done: list transitions, then execute the Done transition
3. Move requirements doc: `docs/requirements/drafts/{KEY}-{slug}.md` to `docs/requirements/completed/`
4. Commit and push the move via commit agent
5. Comment on Jira with completion summary
6. Save progress to memory: `muxcode memory write "agent" "Completed {KEY}: {summary}"`
7. Loop back to Phase 1 for the next story

### Delegation reference

Always use `--force --wait` on commit/push/PR delegations. Use `--wait` on all other delegations.

| Task | Target | Command |
|------|--------|---------|
| Branch/commit/push/PR | commit | `muxcode send commit commit "..." --force --wait` |
| Build | build | `muxcode send build build "..." --wait` |
| Test | test | `muxcode send test test "..." --wait` |
| Review | review | `muxcode send review review "..." --wait` |
| PR status | commit | `muxcode send commit pr-read "..." --wait` |
| Deploy (if needed) | deploy | `muxcode send deploy deploy "..." --wait` |
| Docs | plan | `muxcode send plan update-docs "..." --wait` |

### Requirements doc format

**All actionable items MUST use checkboxes** (`- [ ]`). Never use plain bullets for tasks, criteria, or steps that need tracking. This applies to acceptance criteria, implementation steps, and phase tasks.

```markdown
# {KEY}: {summary}

## Context

### Jira story
- **Key**: {KEY}
- **Summary**: {summary}
- **Type**: {type}
- **Priority**: {priority}

### Description
{Jira description reformatted as markdown}

## Requirements

### Acceptance criteria
- [ ] Criterion 1
- [ ] Criterion 2

### Technical approach
{Analysis of how to implement}

### Key files
| File | Purpose |
|------|---------|
| `path/to/file` | Description |

## Implementation

### Phase 1: {description}
- [ ] Step 1
- [ ] Step 2

### Phase N: Integration test
- [ ] Create `scripts/test-{feature}.sh` automation script
- [ ] Test step 1: {describe specific verifiable behavior}
- [ ] Test step 2: {describe specific verifiable behavior}
- [ ] Run integration test and verify all steps pass

## Status

Draft
```
