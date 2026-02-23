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

## Commit workflow

- Build and test before committing
- Keep commits focused — one logical change per commit
- Stage specific files, avoid `git add -A` in shared repos
- Never commit secrets, credentials, or .env files
