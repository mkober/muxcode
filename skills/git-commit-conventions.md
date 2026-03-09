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

## Commit workflow

- Build and test before committing
- Keep commits focused — one logical change per commit
- Stage specific files, avoid `git add -A` in shared repos
- Never commit secrets, credentials, or .env files
