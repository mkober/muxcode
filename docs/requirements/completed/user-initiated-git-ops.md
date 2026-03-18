# User-initiated Git Ops

Chain stops at review -- commits, pushes, and PRs require explicit user action.

## Requirements

- Automated build-test-review chain terminates after review completes
- Git commits are never auto-triggered by chain completion or review LGTM
- Git pushes and PR creation require explicit user request
- `PreCommitCheck()` validates no agents have pending inbox or active work before allowing commits
- Edit agent system prompt explicitly prohibits auto-committing
- Commit delegation blocked when agents are busy unless `--force` flag is used

## Key files

| File | Purpose |
|------|---------|
| `agents/code-editor.md` | Edit agent instructions prohibiting auto-commit |
| `bus/inspect.go` | `PreCommitCheck()` pre-commit safeguard |
| `cmd/send.go` | `--force` flag to bypass pre-commit agent-idle check |
