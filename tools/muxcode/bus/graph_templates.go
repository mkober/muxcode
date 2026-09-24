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
// 70-pr-local-review deliberately keeps ${intent}: there the argument is a
// PR number, not a spec, and ${spec} would misname it.
var builtinGraphJSON = map[string]string{
	"30-build-test-review": `{
  "name": "30-build-test-review",
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

	"50-spec-to-pr": `{
  "name": "50-spec-to-pr",
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

	"10-story-to-spec": `{
  "name": "10-story-to-spec",
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

	"70-pr-local-review": `{
  "name": "70-pr-local-review",
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

	"80-pr-review-fix": `{
  "name": "80-pr-review-fix",
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

	"100-docs-sync": `{
  "name": "100-docs-sync",
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

	"120-deploy-verify": `{
  "name": "120-deploy-verify",
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

	"20-defect-to-spec": `{
  "name": "20-defect-to-spec",
  "description": "Turn a defect into a backlog spec: read-only evidence capture, plan drafts the spec and backlog row from the evidence, then gated commit and GitHub issue",
  "start": "evidence",
  "nodes": [
    {"id": "evidence", "type": "send", "role": "run", "action": "run", "message": "Collect evidence for this defect WITHOUT changing anything: ${intent}. Run read-only diagnostics, each as its own command — the recent lifecycle log (muxcode lifecycle show --since 2h), muxcode diagnose --all, and any log, pane or file the description names — and report the exact excerpts with their timestamps. Never write files"},
    {"id": "draft", "type": "send", "role": "plan", "action": "update-docs", "message": "Draft a backlog requirements spec for this defect: ${intent}. Take the next free MUX id from docs/requirements/backlog/backlog.md, write docs/requirements/backlog/<id>-<slug>.md (context grounded in the evidence below, acceptance criteria and phases as checkboxes, ending in an integration test phase) and add its backlog.md row. Mark anything the evidence does not establish as unverified. Report the id, title and path. EVIDENCE: ${output:evidence}"},
    {"id": "gate", "type": "wait_human", "message": "Approve committing the new backlog spec and creating its GitHub issue"},
    {"id": "commit-spec", "type": "send", "role": "commit", "action": "commit", "message": "Stage and commit only the new backlog spec and its backlog.md row (no push). The plan agent reported: ${output:draft}"},
    {"id": "issue", "type": "send", "role": "commit", "action": "issue-update", "message": "Create a GitHub issue for the new backlog spec (gh issue create): title '<id> <spec title>', body a short summary and the spec path. The plan agent reported: ${output:draft}. Report the issue number and URL"}
  ],
  "edges": [
    {"from": "evidence", "to": "draft"},
    {"from": "draft", "to": "gate"},
    {"from": "gate", "to": "commit-spec"},
    {"from": "commit-spec", "to": "issue"}
  ]
}`,

	"40-sync-main": `{
  "name": "40-sync-main",
  "description": "Gated rebase of the current branch onto origin/main, build and test on the new base, then push with --force-with-lease; a conflict or a red build withholds the push",
  "start": "gate",
  "nodes": [
    {"id": "gate", "type": "wait_human", "message": "Approve fetching origin, rebasing this branch onto origin/main, and pushing the rebased branch with --force-with-lease"},
    {"id": "rebase", "type": "send", "role": "commit", "action": "rebase", "message": "Fetch origin and rebase the current branch onto origin/main. On a conflict run git rebase --abort, report the conflicting files and EXIT=1 — never resolve a conflict yourself. Report the old and new base sha"},
    {"id": "build", "type": "send", "role": "build", "action": "build", "message": "Run ./build.sh and report results"},
    {"id": "test", "type": "send", "role": "test", "action": "test", "message": "Run tests and report results"},
    {"id": "push", "type": "send", "role": "commit", "action": "push", "message": "Push the rebased branch with git push --force-with-lease and report the pushed sha"}
  ],
  "edges": [
    {"from": "gate", "to": "rebase"},
    {"from": "rebase", "to": "build"},
    {"from": "build", "to": "test"},
    {"from": "test", "to": "push"}
  ]
}`,

	"60-integration-suite": `{
  "name": "60-integration-suite",
  "description": "Run the integration scripts (scripts/test-*.sh) one at a time through the run agent; reported failures go to a fix worker, rebuild and re-run, capped; a suite that times out stops the run instead of starting a fix beside it",
  "start": "suite",
  "nodes": [
    {"id": "suite", "type": "send", "role": "run", "action": "run", "timeout_secs": 5400, "message": "Run exactly this one command and report its summary: bash scripts/test-all.sh — it runs every scripts/test-*.sh one at a time and exits non-zero if any fail. If the repo has no scripts/test-all.sh, run each scripts/test-*.sh one at a time as separate commands instead. Report each script's pass and fail counts. If any script failed, your reply MUST contain the literal token SUITE-FAILED and name every failing check; if every script passed it must not"},
    {"id": "suite-failed", "type": "condition", "conditions": {"output_contains": "SUITE-FAILED"}},
    {"id": "fix", "type": "spawn", "role": "edit", "message": "Fix what the integration suite reported failing. THE SUITE'S REPORT: ${output:suite}. If the rebuild after your last change failed, fix that too — ${failure_report}. Fix the code, not the test, unless the test itself is wrong — say which. Do not run the suite yourself: the graph rebuilds and re-runs it after you report"},
    {"id": "build", "type": "send", "role": "build", "action": "build", "message": "Run ./build.sh and report results"}
  ],
  "edges": [
    {"from": "suite", "to": "suite-failed", "outcome": "failure"},
    {"from": "suite-failed", "to": "fix"},
    {"from": "fix", "to": "build", "max_iterations": 3},
    {"from": "build", "to": "suite"},
    {"from": "build", "to": "fix", "outcome": "failure"}
  ]
}`,

	"90-ci-fix": `{
  "name": "90-ci-fix",
  "description": "Find the current branch's PR, read its failing CI checks, gated fix (build/test/review loop), then push the fixes to the PR",
  "start": "find-pr",
  "nodes": [
    {"id": "find-pr", "type": "send", "role": "commit", "action": "pr-read", "message": "Report whether an open PR exists for the current branch WITHOUT creating or changing anything. Your reply MUST contain the literal token PR-CONFIRMED followed by its number and URL if one exists, or the literal token NO-PR-FOUND if none does. This node asks a question, so a completed lookup is EXIT=0 EITHER WAY; reserve EXIT=1 for a lookup you could not complete at all"},
    {"id": "pr-exists", "type": "condition", "conditions": {"output_contains": "PR-CONFIRMED"}},
    {"id": "read-ci", "type": "send", "role": "commit", "action": "pr-read", "message": "Read the CI checks on this branch's PR WITHOUT changing anything. For each failing check give its name, the failing job and step, and the log excerpt that shows why (gh run view --log-failed). If every check passed your reply MUST contain the literal token CI-GREEN; if any failed it must not. A completed read is EXIT=0 either way. If checks are still running, reply CI-PENDING with EXIT=1 — the run stops, to be re-run once CI finishes"},
    {"id": "ci-green", "type": "condition", "conditions": {"output_contains": "CI-GREEN"}},
    {"id": "fix-gate", "type": "wait_human", "message": "The PR's CI is failing — approve fixing it, then committing and pushing the fixes to the PR branch"},
    {"id": "fix", "type": "spawn", "role": "edit", "message": "Fix the failing CI checks on this branch's PR, listed by the CI read: ${output:read-ci}. If a build, test or review failed after your last change, fix that too — THE FAILURE TO FIX: ${failure_report}. Do not commit or push: the graph does that after you report. Report what you changed for each failing check"},
    {"id": "build", "type": "send", "role": "build", "action": "build", "message": "Run ./build.sh and report results"},
    {"id": "test", "type": "send", "role": "test", "action": "test", "message": "Run tests and report results"},
    {"id": "review", "type": "send", "role": "review", "action": "review", "message": "Review the latest changes on this branch — the fixes made for the PR's failing CI checks"},
    {"id": "push-fixes", "type": "send", "role": "commit", "action": "commit", "message": "Stage and commit the CI fixes, push them to the PR branch, and report the commit sha (nothing changed = reply nothing to do)"}
  ],
  "edges": [
    {"from": "find-pr", "to": "pr-exists"},
    {"from": "pr-exists", "to": "read-ci"},
    {"from": "read-ci", "to": "ci-green"},
    {"from": "ci-green", "to": "fix-gate", "outcome": "failure"},
    {"from": "fix-gate", "to": "fix"},
    {"from": "fix", "to": "build", "max_iterations": 3},
    {"from": "build", "to": "test"},
    {"from": "build", "to": "fix", "outcome": "failure"},
    {"from": "test", "to": "review"},
    {"from": "test", "to": "fix", "outcome": "failure"},
    {"from": "review", "to": "push-fixes"},
    {"from": "review", "to": "fix", "outcome": "failure"}
  ]
}`,

	"110-pr-merge": `{
  "name": "110-pr-merge",
  "description": "Find the current branch's PR, wait for its CI to finish green, then gated merge, branch delete, main update and tracker story move",
  "start": "find-pr",
  "nodes": [
    {"id": "find-pr", "type": "send", "role": "commit", "action": "pr-read", "message": "Report whether an open PR exists for the current branch WITHOUT creating or changing anything. Your reply MUST contain the literal token PR-CONFIRMED followed by its number and URL if one exists, or the literal token NO-PR-FOUND if none does. This node asks a question, so a completed lookup is EXIT=0 EITHER WAY; reserve EXIT=1 for a lookup you could not complete at all"},
    {"id": "pr-exists", "type": "condition", "conditions": {"output_contains": "PR-CONFIRMED"}},
    {"id": "ci-watch", "type": "send", "role": "watch", "action": "watch", "timeout_secs": 3600, "message": "Watch the CI checks on this branch's PR until they finish (gh pr checks --watch) and report each check's result. If every check passed your reply MUST contain the literal token CI-GREEN; otherwise it must not"},
    {"id": "ci-green", "type": "condition", "conditions": {"output_contains": "CI-GREEN"}},
    {"id": "merge-gate", "type": "wait_human", "message": "CI is green — approve merging the PR, deleting its branch, updating main, and moving the tracker story (Jira) to Done"},
    {"id": "merge", "type": "send", "role": "commit", "action": "commit", "message": "Merge this branch's PR with the repo's usual merge method (gh pr merge), delete the PR branch, then check out main and pull it. Report the merge commit sha"},
    {"id": "tracker", "type": "send", "role": "plan", "action": "jira-write", "message": "The user approved the tracker update: if the merged branch tracks a Jira story, transition it to Done and comment the merged PR URL; if it tracks none, reply nothing to do. The merge node reported: ${output:merge}"}
  ],
  "edges": [
    {"from": "find-pr", "to": "pr-exists"},
    {"from": "pr-exists", "to": "ci-watch"},
    {"from": "ci-watch", "to": "ci-green"},
    {"from": "ci-green", "to": "merge-gate"},
    {"from": "merge-gate", "to": "merge"},
    {"from": "merge", "to": "tracker"}
  ]
}`,
}
