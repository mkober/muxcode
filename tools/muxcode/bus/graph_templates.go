package bus

// builtinGraphJSON holds the built-in graph templates, keyed by name.
// These are tier 3 of template resolution (project > user > builtin) and
// every entry must pass Graph.Validate() — pinned by TestBuiltinGraphTemplatesValidate.
//
// ${spec} in node messages is replaced at execution time with what the
// run is driving — for spec-driven templates that is the active spec and
// its current phase. ${intent} is the former name and still expands, so
// templates saved against it keep working.
//
// 4-pr-local-review deliberately keeps ${intent}: there the argument is a
// PR number, not a spec, and ${spec} would misname it.
var builtinGraphJSON = map[string]string{
	"build-test-review": `{
  "name": "build-test-review",
  "description": "Standard build, test, review pipeline as a reusable subgraph",
  "start": "build",
  "nodes": [
    {"id": "build", "type": "send", "role": "build", "action": "build", "message": "Run ./build.sh and report results"},
    {"id": "test", "type": "send", "role": "test", "action": "test", "message": "Run tests and report results"},
    {"id": "review", "type": "send", "role": "review", "action": "review", "message": "Review the latest changes on this branch"}
  ],
  "edges": [
    {"from": "build", "to": "test"},
    {"from": "test", "to": "review"}
  ]
}`,

	"2-spec-to-pr": `{
  "name": "2-spec-to-pr",
  "description": "Walk the active spec phase by phase in one run: implement, build/test, review (findings route to fix), update the spec, gated per-phase commit, loop; stuck phases gate-and-ask; then close out the spec (Complete, completed/, backlog.md) before a final gate covers push and PR",
  "requires_spec": true,
  "start": "implement",
  "nodes": [
    {"id": "implement", "type": "spawn", "role": "edit", "message": "Implement the active requirements spec's ${current_phase} (run: ${spec}). The phase is derived from the spec — if it is already complete, verify and report rather than re-implementing. Before reporting, if the phase has an integration script, run it through the run agent (muxcode send run run \"bash scripts/test-<feature>.sh\" --wait — never go test, the graph's test node owns the suite) and quote its counts and its task id"},
    {"id": "build", "type": "send", "role": "build", "action": "build", "message": "Run ./build.sh and report results"},
    {"id": "test", "type": "send", "role": "test", "action": "test", "message": "Run tests and report results"},
    {"id": "fix", "type": "spawn", "role": "edit", "message": "Fix the reported build, test, or review failure in ${current_phase} (run: ${spec}). A review failure means every must-fix and should-fix it lists — read its findings file when it names one. Verify the fix the way the reviewer will: run the phase's integration script through the run agent (muxcode send run run \"bash scripts/test-<feature>.sh\" --wait — never go test, the graph's test node owns the suite) and quote its counts and its task id in your report. THE FAILURE TO FIX: ${failure_report}"},
    {"id": "review", "type": "send", "role": "review", "action": "review", "message": "Review the latest changes on this branch"},
    {"id": "update-spec", "type": "send", "role": "plan", "action": "verify-spec", "message": "Verify the implemented changes against the active requirements spec and check off completed criteria and steps of ${current_phase} — the commit gate follows, so the spec must reflect reality before it. The worker's report for this phase follows; if it names a handoff or decision file, read that file and record what it carries — a decision the user already made is not yours to re-open, and a phase whose work is a decision rather than code is verified from that record. WORKER REPORT: ${output:implement}"},
    {"id": "phase-check", "type": "condition", "conditions": {"spec_phase_committable": "commit"}},
    {"id": "phase-gate", "type": "wait_human", "message": "Approve committing ${completed_phase}: the phase's work plus its spec update (commit only — push and PR wait for the final gate)"},
    {"id": "commit", "type": "send", "role": "commit", "action": "commit", "guard": "phase-progress", "message": "Stage and commit the work and spec update for ${completed_phase} (no push)"},
    {"id": "loop-check", "type": "condition", "conditions": {"spec_phases_remaining": true}},
    {"id": "stuck-gate", "type": "wait_human", "message": "The current phase did not complete this iteration — its commit was not attempted; approve retrying the phase, or cancel the run to stop"},
    {"id": "close-spec", "type": "send", "role": "plan", "action": "update-docs", "guard": "spec-complete", "message": "Every phase is complete — close out the active requirements doc ONLY if every acceptance criterion and phase step is checked: set its status Complete, move it to docs/requirements/completed/ (a plain file move, not git mv — push-pr stages it), update its row in docs/requirements/backlog/backlog.md and every cross-reference to the old path, clear the active spec, and report the new path. Any item still open = refuse and report the open items"},
    {"id": "close-stuck-gate", "type": "wait_human", "message": "The spec close-out was refused — items are still open (see the close-spec report). Resolve them, then approve retrying the close-out, or cancel the run"},
    {"id": "final-gate", "type": "wait_human", "message": "All phases complete and the spec closed out (status Complete, moved to completed/, backlog.md updated) — approve committing the close-out, pushing the branch and creating the PR"},
    {"id": "push-pr", "type": "send", "role": "commit", "action": "commit", "message": "Stage and commit the spec close-out (the move to docs/requirements/completed/ and the backlog.md update), then push the branch and create a PR for: ${spec}"}
  ],
  "edges": [
    {"from": "implement", "to": "build"},
    {"from": "build", "to": "test"},
    {"from": "build", "to": "fix", "outcome": "failure"},
    {"from": "test", "to": "review"},
    {"from": "test", "to": "fix", "outcome": "failure"},
    {"from": "fix", "to": "build", "max_iterations": 3},
    {"from": "review", "to": "update-spec"},
    {"from": "review", "to": "fix", "outcome": "failure"},
    {"from": "update-spec", "to": "phase-check"},
    {"from": "phase-check", "to": "phase-gate"},
    {"from": "phase-check", "to": "stuck-gate", "outcome": "failure"},
    {"from": "phase-gate", "to": "commit"},
    {"from": "commit", "to": "loop-check"},
    {"from": "commit", "to": "stuck-gate", "outcome": "failure"},
    {"from": "stuck-gate", "to": "implement", "max_iterations_from_spec": true},
    {"from": "loop-check", "to": "implement", "max_iterations_from_spec": true},
    {"from": "loop-check", "to": "close-spec", "outcome": "failure"},
    {"from": "close-spec", "to": "final-gate"},
    {"from": "close-spec", "to": "close-stuck-gate", "outcome": "failure"},
    {"from": "close-stuck-gate", "to": "close-spec", "max_iterations": 3},
    {"from": "final-gate", "to": "push-pr"}
  ]
}`,

	"1-story-to-spec": `{
  "name": "1-story-to-spec",
  "description": "Derive the Jira/GitHub id from the branch, read its requirements, draft a requirements doc and set it active, then human-gated tracker update",
  "start": "derive",
  "nodes": [
    {"id": "derive", "type": "send", "role": "plan", "action": "story-read", "message": "Derive the story key from the current branch name (git branch --show-current; key pattern like MUX-109). If it is a Jira story, read it with muxcode jira read <id> and report the id, title, and requirement text; if this repo tracks GitHub issues, report the issue number — the gated fetch node reads it"},
    {"id": "fetch-gate", "type": "wait_human", "message": "Approve the tracker read (gh) and requirements drafting"},
    {"id": "fetch", "type": "send", "role": "commit", "action": "story-read", "message": "If the derived id is a GitHub issue, read it (gh issue view <n> --json title,body) and report the requirement text; if it is a Jira story, reply nothing to do — plan already read it"},
    {"id": "draft", "type": "send", "role": "plan", "action": "update-docs", "message": "From the story/issue requirements reported upstream, create a requirements doc at docs/requirements/drafts/<ID>-<slug>.md (status field, acceptance criteria as checkboxes, phased plan ending in an integration test phase), then set it as the active spec with: muxcode spec set <path> — report the path"},
    {"id": "update-gate", "type": "wait_human", "message": "Approve updating the tracker (Jira story / GitHub issue) to reference the new requirements doc"},
    {"id": "jira-update", "type": "send", "role": "plan", "action": "jira-write", "message": "The user approved the tracker update: if this branch tracks a Jira story, update it to reference the new requirements doc; if it tracks a GitHub issue instead, reply nothing to do — commit handles it"},
    {"id": "issue-update", "type": "send", "role": "commit", "action": "issue-update", "message": "The user approved the tracker update: if this branch tracks a GitHub issue, comment on it (gh issue comment) referencing the new requirements doc; if it tracks a Jira story instead, reply nothing to do"}
  ],
  "edges": [
    {"from": "derive", "to": "fetch-gate"},
    {"from": "fetch-gate", "to": "fetch"},
    {"from": "fetch", "to": "draft"},
    {"from": "draft", "to": "update-gate"},
    {"from": "update-gate", "to": "jira-update"},
    {"from": "update-gate", "to": "issue-update"}
  ]
}`,

	"4-pr-local-review": `{
  "name": "4-pr-local-review",
  "description": "Prompt for a PR id, gated checkout of main+rebase and the PR branch, local diff, review with an issue list, then branch restore",
  "start": "gate",
  "nodes": [
    {"id": "gate", "type": "wait_human", "message": "Approve switching branches to review PR #${intent} (checkout main, rebase origin/main, gh pr checkout)"},
    {"id": "prepare", "type": "send", "role": "commit", "action": "pr-checkout", "message": "Check out main, pull origin main with rebase, then check out the branch for PR #${intent} (gh pr checkout ${intent}); report the branch name"},
    {"id": "diff", "type": "send", "role": "commit", "action": "pr-diff", "message": "Diff the PR branch against main locally (git diff main...HEAD --stat, then the notable hunks) and report a file-by-file summary of the changes in PR #${intent}"},
    {"id": "review", "type": "send", "role": "review", "action": "review", "message": "Review the local diff of PR #${intent}: report the list of changes and any issues that need to be addressed, ranked by severity"},
    {"id": "restore", "type": "send", "role": "commit", "action": "checkout", "message": "Return the repo to the branch it was on before the PR review (git checkout -) and report the branch"}
  ],
  "edges": [
    {"from": "gate", "to": "prepare"},
    {"from": "prepare", "to": "diff"},
    {"from": "diff", "to": "review"},
    {"from": "review", "to": "restore"},
    {"from": "review", "to": "restore", "outcome": "failure"}
  ]
}`,

	"3-pr-review-fix": `{
  "name": "3-pr-review-fix",
  "description": "Find the current branch's PR, read its review comments, gated fix of each (build/test/review loop), push the fixes to the PR and reply to every comment",
  "start": "find-pr",
  "nodes": [
    {"id": "find-pr", "type": "send", "role": "commit", "action": "pr-read", "message": "Report whether an open PR exists for the current branch WITHOUT creating or changing anything. Your reply MUST contain the literal token PR-CONFIRMED followed by its number and URL if one exists, or the literal token NO-PR-FOUND if none does. This node asks a question, so a completed lookup is EXIT=0 EITHER WAY; reserve EXIT=1 for a lookup you could not complete at all"},
    {"id": "pr-exists", "type": "condition", "conditions": {"output_contains": "PR-CONFIRMED"}},
    {"id": "read-comments", "type": "send", "role": "commit", "action": "pr-read", "message": "Read every review comment on this branch's PR — review summaries, inline comments with file:line, Copilot and human — and list each unresolved, actionable one with its comment id, file:line and what it asks for. If there are none, your reply MUST contain the literal token NO-ACTIONABLE-COMMENTS; otherwise it must not. Change nothing. A completed read is EXIT=0 either way"},
    {"id": "no-comments", "type": "condition", "conditions": {"output_contains": "NO-ACTIONABLE-COMMENTS"}},
    {"id": "fix-gate", "type": "wait_human", "message": "The PR has review comments to address — approve fixing them, committing and pushing the fixes to the PR branch, and replying to each comment"},
    {"id": "fix", "type": "spawn", "role": "edit", "message": "Address the review comments on this branch's PR, listed by the PR read: ${output:read-comments}. For each comment either fix it in the code or decline it with a reason (wrong, or out of scope). If a build, test or review failed after your last change, fix that too — THE FAILURE TO FIX: ${failure_report}. Do not commit, push or reply to comments: the graph does those after you report. Your report MUST list every comment id with what you changed (file:line) or why you declined it — carry earlier iterations forward, since the reply step reads only your latest report"},
    {"id": "build", "type": "send", "role": "build", "action": "build", "message": "Run ./build.sh and report results"},
    {"id": "test", "type": "send", "role": "test", "action": "test", "message": "Run tests and report results"},
    {"id": "review", "type": "send", "role": "review", "action": "review", "message": "Review the latest changes on this branch — the fixes made for the PR's review comments"},
    {"id": "push-fixes", "type": "send", "role": "commit", "action": "commit", "message": "Stage and commit the PR review-feedback changes, push them to the PR branch, and report the commit sha (nothing changed = reply nothing to do)"},
    {"id": "reply", "type": "send", "role": "commit", "action": "comment", "message": "Reply to each PR review comment: cite the commit sha for every fix that was pushed, or give the reason for every comment declined. The push node reported: ${output:push-fixes}. The fix worker reported, per comment: ${output:fix}"}
  ],
  "edges": [
    {"from": "find-pr", "to": "pr-exists"},
    {"from": "pr-exists", "to": "read-comments"},
    {"from": "read-comments", "to": "no-comments"},
    {"from": "no-comments", "to": "fix-gate", "outcome": "failure"},
    {"from": "fix-gate", "to": "fix"},
    {"from": "fix", "to": "build", "max_iterations": 3},
    {"from": "build", "to": "test"},
    {"from": "build", "to": "fix", "outcome": "failure"},
    {"from": "test", "to": "review"},
    {"from": "test", "to": "fix", "outcome": "failure"},
    {"from": "review", "to": "push-fixes"},
    {"from": "review", "to": "fix", "outcome": "failure"},
    {"from": "push-fixes", "to": "reply"}
  ]
}`,

	"5-docs-sync": `{
  "name": "5-docs-sync",
  "description": "Verify requirements-spec alignment, update spec/architecture docs and README, then human-gated commit",
  "start": "spec",
  "nodes": [
    {"id": "spec", "type": "send", "role": "plan", "action": "verify-spec", "message": "Verify the current branch changes align with the active requirements spec; update the spec doc if needed (check off completed items, adjust status)"},
    {"id": "docs", "type": "send", "role": "plan", "action": "update-docs", "message": "Update architecture documentation and README if the reviewed changes require it; report what changed or why nothing did"},
    {"id": "gate", "type": "wait_human", "message": "Approve committing the spec and documentation updates on the current branch"},
    {"id": "commit", "type": "send", "role": "commit", "action": "commit", "message": "Stage and commit the spec and documentation updates on the current branch"}
  ],
  "edges": [
    {"from": "spec", "to": "docs"},
    {"from": "docs", "to": "gate"},
    {"from": "gate", "to": "commit"}
  ]
}`,

	"6-deploy-verify": `{
  "name": "6-deploy-verify",
  "description": "Deploy, run a verification invocation, watch logs",
  "start": "deploy",
  "nodes": [
    {"id": "deploy", "type": "send", "role": "deploy", "action": "deploy", "message": "Run the deployment and report changes"},
    {"id": "verify", "type": "send", "role": "run", "action": "run", "message": "Run a verification invocation and report output"},
    {"id": "watch", "type": "send", "role": "watch", "action": "watch", "message": "Tail the deployment logs and report errors"}
  ],
  "edges": [
    {"from": "deploy", "to": "verify"},
    {"from": "verify", "to": "watch"}
  ]
}`,
}
