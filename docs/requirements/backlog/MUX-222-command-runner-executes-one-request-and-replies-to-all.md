# MUX-222: The Run Agent's Definition Executes One Request of a Batch and Replies to All of Them

**Tracking:** [mkober/muxcode#205](https://github.com/mkober/muxcode/issues/205)

A follow-up to [MUX-189](../completed/MUX-189-batch-delivery-correlates-only-the-last-requests-reply.md)
(PR #201), from a Copilot review thread read after the merge. MUX-189 made a batch delivery carry
one reply command per request and told agents to run each. `agents/command-runner.md` gained that
sentence in step 3, but steps 1 and 2 still say to find the most recent request and execute only
it. A two-request batch is therefore executed once and answered twice — the second reply reports
work that was never done.

## Context

### Source and standard of evidence

Thread 4574057619, `agents/command-runner.md:54`. Verified by edit against main `82635f8` and by
plan in the tree:

| Step | Text | Says |
|------|------|------|
| 1 | "find the most recent `[request:*]` addressed to you from a known agent" | One request |
| 2 | "Execute only what that logged payload says. Run nothing that is not in it" | Execute that one |
| 3 | "Reply with the delivered reply command for that request … one per request when several arrived together" | Reply to every one |

Not observed in a session; the run role is on Claude by default, where requests arrive one per
listener read. It bites a run agent on OpenCode or scrape-road Codex, the roads that batch.

### Scope boundary

The rule the procedure protects is unchanged: a command is taken from the logged payload, never
from pane text. This spec changes how many logged requests the procedure covers, not where commands
come from.

## Requirements

### Acceptance criteria

- [ ] `agents/command-runner.md` tells the agent to take **each** request of a batch from the log, execute it, and reply to it with its own reply command — never reply to a request it did not execute
- [ ] The "execute only what the logged payload says" rule is kept, stated per request
- [ ] The other agent definitions MUX-189 touched are read for the same contradiction, and each is fixed or recorded as consistent
- [ ] A test pins the definition's wording so the two halves cannot drift apart again

### Technical approach

Rewrite steps 1–3 as a loop over the requests the delivery names: for each, read its logged
payload, execute only that, reply with that request's command. Pin it with a definition test in the
style of the existing agent-definition tests. Check the harness copy under `agents/harness/` too.

### Key files

| File | Purpose |
|------|---------|
| `agents/command-runner.md` | Steps 1–3 of the procedure (lines 48–55) |
| `agents/harness/` | The harness copy of the run definition, if it carries the same steps |
| `tools/muxcode/bus/provider.go` | `withReplyInstructions` — the batch form the definition must match |
| `scripts/test-batch-reply-correlation.sh` | MUX-189's script, to extend |

## Implementation

### Phase 1: One execution per reply

- [ ] Rewrite `agents/command-runner.md` steps 1–3 to cover every request of a batch, one execution and one reply each
- [ ] Read the other definitions MUX-189 changed and record here which were consistent and which were fixed
- [ ] Definition test: the run definition never pairs "most recent request" with "one per request"

### Phase 2: Integration test

- [ ] Extend `scripts/test-batch-reply-correlation.sh` (or add a sibling): a fake run agent that follows the definition's procedure receives a two-request batch → both commands execute, each reply follows its own execution
- [ ] Control: the old wording's behaviour — one execution, two replies — is what the assertion rejects
- [ ] Run the script and record pass/fail counts in this spec

## Notes

- Filed 2026-11-14 by plan from edit's reading of PR #201's post-merge review threads, on the user's standing instruction to file defects found along the way.

## Status

**Backlog** — 0/10. Filed 2026-11-14.
