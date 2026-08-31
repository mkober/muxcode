# Dynamic Prompts

Go-based system prompt composition with role-specific sections.

## Requirements

- System prompt assembled from multiple sources: agent definition, shared prompt, skills, context.d, session resume
- Agent definitions loaded from markdown files with YAML frontmatter
- Skills injected based on role matching from skill file frontmatter
- Context files from `context.d/shared/` and `context.d/<role>/` included per role
- Session resume context from memory files appended when available
- Both bus agent loop and LLM harness use the same prompt assembly pattern
- Prompt composition is deterministic and consistent across restarts

## Key files

| File | Purpose |
|------|---------|
| `bus/agent.go` | `buildSystemPrompt()` for bus-based agent loop |
| `harness/prompt.go` | `BuildSystemPrompt()`, `ReadAgentDefinition()` for harness agents |
| `bus/context.go` | `ContextFilesForRole()`, `FormatContextPrompt()` |
| `bus/detect.go` | `DetectProject()`, `AutoContextFiles()` for project-aware context |

## Status

Complete
