# MUX-198: Builtin Graph Requests Carry Literal Verdicts an Echoed Request Satisfies

**Tracking:** [mkober/muxcode#101](https://github.com/mkober/muxcode/issues/101)

Several builtin graph node messages spell out a literal exit code (`EXIT=0`, `EXIT=1`) and the literal
tokens their downstream `output_contains` conditions look for (`NO-ACTIONABLE-COMMENTS`, `CI-GREEN`,
`PR-CONFIRMED`). A reply that echoes its request (the [MUX-154](../completed/MUX-154-codex-status-line-closes-tracked-tasks.md)
failure class, where captured prompt text stands in for an answer) then carries a verdict and a
routing token nobody reported. The worst case is `prReviewReadMessage`: echoed, it reads as success
**and** contains `NO-ACTIONABLE-COMMENTS`, so `110-pr-merge` can route `recheck-comments → still-clear →
merge` past unresolved comments. That is the [MUX-187](../completed/MUX-187-pr-merge-merges-over-unresolved-review-comments.md)
failure by another road.

## Context

### Source and standard of evidence

Filed 2026-09-28 on the user's instruction relayed by edit (brief `/tmp/exit-literal-defect.md`).
Found during the PR #99 review, which flagged the literal `EXIT=0` in `20-defect-to-spec`'s `issue`
node. That one node was fixed in `d4911e1` with a scoped `parseExitSentinel` assertion in
`TestDefectToSpecTemplate`; the rest of the class is filed here. Mechanism read by plan from code at
`a40c10e` (main after PR #99). **Not reproduced end to end**: no run has been observed routing on an
echoed request. The chain below is read from code.

### Offending builtin messages (`tools/muxcode/bus/graph_templates.go` at `a40c10e`)

| Line | Template / node | Literals | Last `EXIT=` → echo parses as | Token an echo satisfies |
|------|-----------------|----------|-------------------------------|-------------------------|
| 125 | `80-pr-review-fix` `find-pr` | `EXIT=0 EITHER WAY; reserve EXIT=1`, `PR-CONFIRMED`, `NO-PR-FOUND` | failure | `PR-CONFIRMED` (`pr-exists`) |
| 210 | `40-sync-main` `rebase` | `EXIT=1` | failure | — |
| 247 | `90-ci-fix` `find-pr` | as line 125 | failure | `PR-CONFIRMED` |
| 249 | `90-ci-fix` `read-ci` | `EXIT=0 either way`, `CI-PENDING with EXIT=1`, `CI-GREEN` | failure | `CI-GREEN` (`ci-green`) |
| 279 | `110-pr-merge` `find-pr` | as line 125 | failure | `PR-CONFIRMED` |
| 283 | `110-pr-merge` `ci-watch` | `CI-GREEN` (no exit literal) | — | `CI-GREEN` (`ci-green`) |
| 320 | `prReviewReadMessage` (built into `80-pr-review-fix` `read-comments`, `110-pr-merge` `read-comments` and `recheck-comments` via `prReviewReadNodes`) | ends `A completed read is EXIT=0 either way`, `NO-ACTIONABLE-COMMENTS` | **success** | `NO-ACTIONABLE-COMMENTS` (`no-comments`, `still-clear`) |

Line 283 is a finding beyond the brief: it carries no exit literal, but its token still routes.

### Mechanism — verified

| Fact | Where |
|------|-------|
| The sentinel parser takes the **last** `EXIT=<n>` in a reply; its own doc says a request should say `EXIT=<n>`, never a literal code, because an echo would be misread as a result | `parseExitSentinel`, `bus/graph_exec.go:2046–2069` |
| The executor's own verdict instruction follows that rule | `verdictTokenInstruction`, `bus/graph_exec.go:801–804` |
| `output_contains` is a plain `strings.Contains` over the predecessor's harvested output | `evalOutputContains`, `bus/conditions.go:287`; `predecessorOutput`, `bus/graph_exec.go` |
| The existing echo guards match **chrome** (status lines, banners, rule lines, truncated tool-call renders), not request prose. An echoed request body passes them | `dropsAsProviderChrome` (`bus/inbox.go`), `LooksLikeNonResult` / `looksLikeChrome` (`bus/history_provenance.go:214–260`), `sendResponseIsNonResult` (`bus/graph_exec.go:1897`) |
| In `110-pr-merge`, `still-clear → merge` has no human gate between them: `merge-gate` sits **before** `recheck-comments` | edges, `bus/graph_templates.go:294–302` |

### Blast radius

| Echoed node | Route taken | Consequence |
|-------------|-------------|-------------|
| `110-pr-merge` `recheck-comments` | success, `still-clear` passes → `merge` | **Merges over comments posted after the merge gate** (MUX-187's failure) |
| `110-pr-merge` `read-comments` | `no-comments` passes → `ci-watch` | Skips `open-comments`; `merge-gate` still asks a human, who is not told comments exist |
| `80-pr-review-fix` `read-comments` | `no-comments` passes | Run ends as "nothing to fix" |
| `ci-watch` / `read-ci` | `ci-green` passes | Reaches `merge-gate` (a human) or skips the CI fix |
| `find-pr` ×3 | parses failure, but `PR-CONFIRMED` is present | Fails the node, so it is safe by accident. The token is still echo-satisfiable |
| `40-sync-main` `rebase` | failure | Fails safe |

## Requirements

### Acceptance criteria

- [x] No builtin template node message, and no shared message constant, contains a literal `EXIT=<digit>`
- [x] No builtin message spells a downstream `output_contains` token in a form an echo of the request
      satisfies, **or** the conditions are made robust to an echo. Which one is recorded as a decision
      (option C, [Decision](#decision-2026-09-29-option-c--echo-aware-harvest))
- [x] A test iterates every builtin template's node messages (after template expansion) and asserts
      `parseExitSentinel` finds nothing, generalising the scoped `TestDefectToSpecTemplate` assertion
      (`TestBuiltinRequestsCarryNoVerdict`)
- [x] A test asserts that each condition's token cannot be satisfied by its predecessor's own request
      text: met at the harvest, not per condition. Under option C an echoed reply never reaches a
      condition (`TestEchoesRequest` on `prReviewReadMessage`, `TestExecSendNodeEchoWaitsForGenuineReply`
      on a CI read)
- [x] Negative control: a genuine reply carrying `EXIT=0` and the token still routes success
      (`TestExecSendNodeEchoWaitsForGenuineReply`)

### Technical approach

Options for the token half (the exit literals are a plain rewrite to `EXIT=<n>` or prose):

| Option | Idea | Cost |
|--------|------|------|
| A. Describe, don't spell | Name the token indirectly (e.g. assemble it from parts, or refer to "the no-comments token defined below") so the request text never contains it | Agents must still emit the exact token; indirection can confuse weaker models |
| B. Echo-aware condition | Before `output_contains`, subtract the dispatched request text from the harvested output, or refuse a harvest that contains the request verbatim | Touches the harvester; needs the request text on the node status |
| C. Echo-aware harvest | Treat a response that contains its own request's text as a non-result (extend `sendResponseIsNonResult`) | Closes every node at once; a genuine reply that quotes its request would hold |

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/graph_templates.go` | Offending messages, `prReviewReadMessage`, `prReviewReadNodes`, `NoActionableCommentsToken` |
| `tools/muxcode/bus/graph_exec.go` | `parseExitSentinel`, `verdictTokenInstruction`, `predecessorOutput`, `sendResponseIsNonResult` |
| `tools/muxcode/bus/conditions.go` | `evalOutputContains` |
| `tools/muxcode/bus/history_provenance.go` | `LooksLikeNonResult`, `looksLikeChrome` |
| `tools/muxcode/bus/graph_workflow_templates_test.go` | `TestDefectToSpecTemplate` (the scoped assertion to generalise) |

## Implementation

### Phase 1: Pin

- [x] Add the template-wide `parseExitSentinel` test; confirm it fails on the seven listed lines
      (`TestBuiltinRequestsCarryNoVerdict`, landed with the rewrite in `d6f064c`; six of the seven lines
      carried an exit literal, since line 283 has only a token)
- [x] Add a test feeding each node's own message to its downstream condition; confirm
      `still-clear`, `no-comments`, `ci-green` and `pr-exists` pass on it today (superseded by option C:
      the echo is pinned at the harvest by `TestEchoesRequest` and
      `TestExecSendNodeEchoWaitsForGenuineReply`, before any condition reads it)

### Phase 2: Rewrite the exit literals

- [x] Replace every literal `EXIT=<digit>` in the listed messages with `EXIT=<n>` or prose
- [x] Phase 1's sentinel test passes

### Phase 3: Close the token road

- [x] Choose option A, B or C with the user and record the decision here: option C, below
- [x] Implement it; Phase 1's condition test now holds for every listed node
- [x] Negative control: a genuine reply still routes

#### Decision (2026-09-29): option C — echo-aware harvest

Chosen by the user. `sendResponseIsNonResult` (`bus/graph_exec.go`) treats a reply as a non-result
when `echoesRequest` matches, so the node keeps waiting and the genuine reply routes. A reply counts
as an echo on either of two shapes:

| Shape | Rule | Constant |
|-------|------|----------|
| Head/tail window | The reply contains the request's first or last 20 words (requests under 20 words skip this shape) | `echoWindowWords` |
| Wrapped fragment | The reply, less a trailing `EXIT` line, is wholly a contiguous run of at least 6 request words from anywhere in the request (added in `2e40262` for Copilot comment 4134200569: short, interior, pane-wrapped fragments) | `echoFragmentMinWords` |

This closes the token road at the harvest for every node at once, so the tokens stay spelled out in
the requests. The accepted cost: a genuine reply that quotes its request at that length holds instead
of routing. Shipped in PR #102 (merge `06e133b`); CI green on `2e40262`.

### Phase 4: Integration test

- [x] Create `scripts/test-graph-request-echo.sh`: on a scratch daemon, run `110-pr-merge` with a stub
      commit agent that answers `recheck-comments` by echoing its request verbatim
- [x] Assert the run does **not** reach `merge`: it holds or routes to `new-comments`
- [x] Same stub answering with a genuine `NO-ACTIONABLE-COMMENTS` reply: assert the run reaches `merge`
- [x] Coverage floor so a skipped section cannot report green (exactly 15 checks)
- [x] Run the script and verify all checks pass (16/16 on 2026-09-29)

**Integration run (2026-09-29).** The first run was 12 pass / 4 fail: the `recheck-comments` section
never reached the echo, because the script wrote `MUXCODE_GATE_AUTHORITY_ROLES` to a `$MUXCODE_CONFIG`
file and gate authority reads only `$HOME/.config/muxcode/config` (`bus/gate_authority.go:144–151`).
With the authority line written to the scratch `$HOME` config, as `scripts/test-gate-authority.sh`
does, the re-run passed 16/16 (15 assertions plus the coverage floor). An echoed `recheck-comments`
never dispatches `merge` and keeps waiting; the genuine clear reply dispatches it; an echoed `ci-watch`
never opens `merge-gate`; the genuine `CI-GREEN` does.

## Related

- [MUX-154](../completed/MUX-154-codex-status-line-closes-tracked-tasks.md): captured pane text closes
  tracked tasks; the echo this defect is exposed to
- [MUX-187](../completed/MUX-187-pr-merge-merges-over-unresolved-review-comments.md): pr-merge
  merges over unresolved review comments; the failure line 320 reopens
- [MUX-148](../completed/MUX-148-node-outcome-reads-command-ran-as-task-done.md): node outcome
  provenance

## Status

Complete — PR #102 (`06e133b`), issue #101 closed, integration run 16/16 on 2026-09-29.
