# Git Manager HEREDOC

Commit agent uses HEREDOC syntax for multi-line commit messages instead of temporary files.

## Requirements

- Commit messages passed via HEREDOC (`cat <<'EOF'`) to `git commit -m`
- Eliminates temp file creation and cleanup for commit message formatting
- Preserves multi-line commit messages with proper newline handling
- Co-author trailers included naturally within the HEREDOC block

## Key files

| File | Purpose |
|------|---------|
| `agents/git-manager.md` | Agent definition with HEREDOC commit message instructions |
