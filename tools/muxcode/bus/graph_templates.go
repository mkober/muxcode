package bus

// builtinGraphJSON holds the built-in graph templates, keyed by name.
// These are tier 3 of template resolution (project > user > builtin) and
// every entry must pass Graph.Validate() — pinned by TestBuiltinGraphTemplatesValidate.
//
// ${intent} in node messages is replaced with the graph run's intent
// argument at execution time (Phase 4).
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

	"req-code-pr": `{
  "name": "req-code-pr",
  "description": "Walk the active spec phase by phase in one run: implement, build/test, review, update the spec, gated per-phase commit, loop; stuck phases gate-and-ask; final gate covers push and PR",
  "requires_spec": true,
  "start": "implement",
  "nodes": [
    {"id": "implement", "type": "spawn", "role": "edit", "message": "Implement the active requirements spec's ${current_phase} (run: ${intent}). The phase is derived from the spec — if it is already complete, verify and report rather than re-implementing"},
    {"id": "build", "type": "send", "role": "build", "action": "build", "message": "Run ./build.sh and report results"},
    {"id": "test", "type": "send", "role": "test", "action": "test", "message": "Run tests and report results"},
    {"id": "fix", "type": "spawn", "role": "edit", "message": "Fix the reported build or test failure in ${current_phase} (run: ${intent})"},
    {"id": "review", "type": "send", "role": "review", "action": "review", "message": "Review the latest changes on this branch"},
    {"id": "update-spec", "type": "send", "role": "plan", "action": "verify-spec", "message": "Verify the implemented changes against the active requirements spec and check off completed criteria and steps of ${current_phase} — the commit gate follows, so the spec must reflect reality before it"},
    {"id": "phase-gate", "type": "wait_human", "message": "Approve committing ${completed_phase}: the phase's work plus its spec update (commit only — push and PR wait for the final gate)"},
    {"id": "commit", "type": "send", "role": "commit", "action": "commit", "guard": "phase-progress", "message": "Stage and commit the work and spec update for ${completed_phase} (no push)"},
    {"id": "loop-check", "type": "condition", "conditions": {"spec_phases_remaining": true}},
    {"id": "stuck-gate", "type": "wait_human", "message": "The current phase did not complete this iteration — approve retrying it (its commit was withheld); cancel the run to stop instead"},
    {"id": "final-gate", "type": "wait_human", "message": "All phases complete — approve pushing the branch and creating the PR"},
    {"id": "push-pr", "type": "send", "role": "commit", "action": "commit", "message": "Push the branch and create a PR for: ${intent}"}
  ],
  "edges": [
    {"from": "implement", "to": "build"},
    {"from": "build", "to": "test"},
    {"from": "build", "to": "fix", "outcome": "failure"},
    {"from": "test", "to": "review"},
    {"from": "test", "to": "fix", "outcome": "failure"},
    {"from": "fix", "to": "build", "max_iterations": 3},
    {"from": "review", "to": "update-spec"},
    {"from": "update-spec", "to": "phase-gate"},
    {"from": "phase-gate", "to": "commit"},
    {"from": "commit", "to": "loop-check"},
    {"from": "commit", "to": "stuck-gate", "outcome": "failure"},
    {"from": "stuck-gate", "to": "implement", "max_iterations_from_spec": true},
    {"from": "loop-check", "to": "implement", "max_iterations_from_spec": true},
    {"from": "loop-check", "to": "final-gate", "outcome": "failure"},
    {"from": "final-gate", "to": "push-pr"}
  ]
}`,

	"story-lifecycle": `{
  "name": "story-lifecycle",
  "description": "Requirements draft, human approval, implement with fix loop, review, human-gated commit and PR",
  "start": "requirements",
  "nodes": [
    {"id": "requirements", "type": "send", "role": "plan", "action": "update-docs", "message": "Draft a requirements doc for: ${intent}"},
    {"id": "req-gate", "type": "wait_human", "message": "Approve the requirements before implementation starts"},
    {"id": "implement", "type": "spawn", "role": "edit", "message": "Implement per the requirements doc: ${intent}"},
    {"id": "build", "type": "send", "role": "build", "action": "build", "message": "Run ./build.sh and report results"},
    {"id": "test", "type": "send", "role": "test", "action": "test", "message": "Run tests and report results"},
    {"id": "fix", "type": "spawn", "role": "edit", "message": "Fix the reported build or test failure"},
    {"id": "review", "type": "send", "role": "review", "action": "review", "message": "Review the changes on this branch"},
    {"id": "update-spec", "type": "send", "role": "plan", "action": "verify-spec", "message": "Verify the implemented changes against the active requirements spec and check off completed criteria and phase steps for: ${intent} — the human sign-off gate follows, so the spec must reflect reality before it"},
    {"id": "ship-gate", "type": "wait_human", "message": "Approve commit, push, and PR for: ${intent}"},
    {"id": "commit", "type": "send", "role": "commit", "action": "commit", "guard": "phase-complete", "message": "Stage and commit: ${intent}"},
    {"id": "pr", "type": "send", "role": "commit", "action": "commit", "message": "Create a PR for the current branch"}
  ],
  "edges": [
    {"from": "requirements", "to": "req-gate"},
    {"from": "req-gate", "to": "implement"},
    {"from": "implement", "to": "build"},
    {"from": "build", "to": "test"},
    {"from": "build", "to": "fix", "outcome": "failure"},
    {"from": "test", "to": "review"},
    {"from": "test", "to": "fix", "outcome": "failure"},
    {"from": "fix", "to": "build", "max_iterations": 3},
    {"from": "review", "to": "update-spec"},
    {"from": "update-spec", "to": "ship-gate"},
    {"from": "ship-gate", "to": "commit"},
    {"from": "commit", "to": "pr"}
  ]
}`,

	"commit-pr-review-loop": `{
  "name": "commit-pr-review-loop",
  "description": "Gated commit+PR, watch review feedback, gated fix loop with comment replies, then spec close-out",
  "start": "gate1",
  "nodes": [
    {"id": "gate1", "type": "wait_human", "message": "Approve staging, commit, push, and PR creation"},
    {"id": "a", "type": "send", "role": "commit", "action": "commit", "message": "Stage all unstaged files, commit, push, and create a PR"},
    {"id": "b", "type": "send", "role": "commit", "action": "pr-read", "message": "Watch for PR comments and report the review decision and any comments"},
    {"id": "gate2", "type": "wait_human", "message": "Approve addressing the review feedback, replying to comments, and the spec close-out with its commit and push that follow"},
    {"id": "c", "type": "send", "role": "edit", "action": "edit", "message": "Address the PR review comments"},
    {"id": "d", "type": "send", "role": "commit", "action": "comment", "message": "Reply to the PR comments"},
    {"id": "close-spec", "type": "send", "role": "plan", "action": "update-docs", "guard": "spec-complete", "message": "Close out the active requirements doc ONLY if every acceptance criterion and phase step is checked complete: set status Complete, move it to docs/requirements/completed/, clear the active spec, report the new path. Any item still open = refuse and report the open count (no active spec = reply nothing to do)"},
    {"id": "commit-spec", "type": "send", "role": "commit", "action": "commit", "message": "Stage and commit the completed requirements doc move and push it to the PR branch (nothing moved = reply nothing to do)"}
  ],
  "edges": [
    {"from": "gate1", "to": "a"},
    {"from": "a", "to": "b"},
    {"from": "b", "to": "gate2"},
    {"from": "gate2", "to": "c"},
    {"from": "c", "to": "d"},
    {"from": "d", "to": "close-spec"},
    {"from": "close-spec", "to": "commit-spec"}
  ]
}`,

	"story-to-spec": `{
  "name": "story-to-spec",
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

	"pr-local-review": `{
  "name": "pr-local-review",
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
    {"from": "review", "to": "restore"}
  ]
}`,

	"update-spec-docs": `{
  "name": "update-spec-docs",
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

	"deploy-verify": `{
  "name": "deploy-verify",
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
