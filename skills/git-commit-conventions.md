---
name: git-commit-conventions
description: Commit message format and git workflow conventions
roles: [commit, edit]
tags: [git, commit]
---

## Commit message format

- Keep the subject line under 72 characters
- Use imperative mood ("Add feature" not "Added feature")
- Separate subject from body with a blank line
- Wrap body at 72 characters
- Use body to explain what and why, not how
- **Jira key prefix**: if the branch name starts with a Jira key (e.g. `PBP1-456-add-validation`), prepend it to the subject line: `PBP1-456 Add validation logic`. Extract with: `git rev-parse --abbrev-ref HEAD | grep -oE '^[A-Z][A-Z0-9]*-[0-9]+'`. If no key is found, commit without a prefix.

## PR title format

- Apply the same Jira key prefix rule to PR titles: `PBP1-456 Add validation logic` (no parentheses, no suffix)
- Keep the title under 70 characters

## Handling commit-msg hook failures

When a commit fails because the Jira key prefix doesn't match the repo's commit-msg hook regex:

1. **Parse the error** — look for the hook's expected regex pattern in the error output (e.g. `Run regex="..."` followed by `Commit message does not start with a Jira Issue ID`)
2. **Check if the branch Jira key matches** — extract the allowed prefixes from the regex and compare against the key extracted from the branch name
3. **Retry without the prefix** — if the branch key (e.g. `PROMGT-115`) doesn't match any allowed prefix in the regex (e.g. only `PT`, `PS`, `PBP1`), strip the Jira key prefix from the commit message and retry the commit. The hook may also accept `build(deps)` or other non-Jira prefixes — check the full regex
4. **Never force past the hook** — do not use `--no-verify`. Fix the message to satisfy the hook

Example: branch `PROMGT-115-fix-syntax` → key `PROMGT-115` → hook only allows `PT|PS|PBP1` → commit without prefix:
```
Fix EventBridge schedule syntax
```

## Commit workflow

- Build and test before committing
- Keep commits focused — one logical change per commit
- Stage specific files, avoid `git add -A` in shared repos
- Never commit secrets, credentials, or .env files
