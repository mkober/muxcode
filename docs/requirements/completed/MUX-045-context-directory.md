# Context Directory

Per-agent drop-in context files injected into agent prompts via `context.d/` directories.

## Requirements

- `context.d/shared/*.md` files injected into all agent prompts
- `context.d/<role>/*.md` files injected into role-specific prompts only
- Project-level files (`.muxcode/context.d/`) shadow user-level (`~/.config/muxcode/context.d/`) by filename
- Context injected into prompt between skills section and session resume
- Files sorted alphabetically within each directory

## Key files

| File | Purpose |
|------|---------|
| `bus/context.go` | `ContextFilesForRole()`, `AllContextFilesForRole()`, `FormatContextPrompt()`, `FormatContextList()` |
