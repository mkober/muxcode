package harness

import (
	"os"
	"strings"
)

// BuildSystemPrompt assembles the full system prompt for a local LLM agent.
// Order: agent definition → local LLM instructions → role examples → skills → context.
func BuildSystemPrompt(role string, agentDef, skills, contextPrompt string) string {
	var parts []string

	if agentDef != "" {
		parts = append(parts, agentDef)
	}

	parts = append(parts, LocalLLMInstructions(role))

	if examples := RoleExamples(role); examples != "" {
		parts = append(parts, examples)
	}

	if skills != "" {
		parts = append(parts, skills)
	}

	if contextPrompt != "" {
		parts = append(parts, contextPrompt)
	}

	return strings.Join(parts, "\n\n")
}

// LocalLLMInstructions generates the core behavioral instructions for small LLMs.
// This is the key differentiator — tells the LLM NOT to check inbox.
func LocalLLMInstructions(role string) string {
	return `## How You Work

You are an autonomous agent. Tasks are delivered in the user message below.
Your inbox has already been read — do NOT run ` + "`muxcode-agent-bus inbox`" + `.

### Rules
1. Read the task below and execute it immediately using your tools
2. Do NOT check the inbox — your task is already here
3. Do NOT ask for confirmation — execute autonomously
4. After completing, provide a short summary of what you did

### Sending Results
When you need to send a message to another agent:
` + "```" + `
muxcode-agent-bus send <target> <action> "<short single-line result>"
` + "```" + `

### Saving Learnings
` + "```" + `
muxcode-agent-bus memory write "<section>" "<text>"
` + "```" + `

### Important
- NEVER run ` + "`muxcode-agent-bus inbox`" + ` — messages are already delivered
- NEVER send messages to yourself (` + role + `)
- Keep tool calls focused — complete the task, then stop
- If a command fails, try a different approach (do not repeat the same command)`
}

// RoleExamples returns concrete tool call examples for a given role.
func RoleExamples(role string) string {
	switch role {
	case "commit", "git":
		return `### Git Examples
When asked to show status:
` + "```" + `bash
git status
` + "```" + `

When asked to commit:
` + "```" + `bash
git add -A
git status
git commit -m "descriptive message"
` + "```" + `

When asked to push:
` + "```" + `bash
git push origin HEAD
` + "```" + `

When asked to create a PR:
` + "```" + `bash
gh pr create --title "PR title" --body "Description"
` + "```" + `

When asked to show diff:
` + "```" + `bash
git diff
` + "```" + `

When asked to show log:
` + "```" + `bash
git log --oneline -10
` + "```"

	case "build":
		return `### Build Examples
When asked to build:
` + "```" + `bash
./build.sh
` + "```" + `

When asked to build with make:
` + "```" + `bash
make build
` + "```"

	case "test":
		return `### Test Examples
When asked to run tests:
` + "```" + `bash
./test.sh
` + "```"

	case "deploy":
		return `### Deploy Examples
When asked to diff:
` + "```" + `bash
cdk diff 2>&1
` + "```"

	default:
		return ""
	}
}

// ReadAgentDefinition reads the agent definition markdown for a role.
// Searches: .claude/agents/<name>.md → ~/.config/muxcode/agents/<name>.md
func ReadAgentDefinition(role string) string {
	name := AgentFileName(role)
	if name == "" {
		return ""
	}

	paths := []string{
		".claude/agents/" + name + ".md",
	}

	home, _ := os.UserHomeDir()
	if home != "" {
		paths = append(paths, home+"/.config/muxcode/agents/"+name+".md")
	}

	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		return StripFrontmatter(string(data))
	}

	return ""
}

// AgentFileName maps role names to agent definition filenames (without .md).
func AgentFileName(role string) string {
	switch role {
	case "edit":
		return "code-editor"
	case "build":
		return "code-builder"
	case "test":
		return "test-runner"
	case "review":
		return "code-reviewer"
	case "deploy":
		return "infra-deployer"
	case "runner":
		return "command-runner"
	case "git", "commit":
		return "git-manager"
	case "analyst", "analyze":
		return "editor-analyst"
	case "docs":
		return "doc-writer"
	case "research":
		return "code-researcher"
	case "watch":
		return "log-watcher"
	case "pr-read":
		return "pr-reader"
	default:
		return role
	}
}

// StripFrontmatter removes YAML frontmatter (--- delimited) from markdown.
func StripFrontmatter(content string) string {
	if !strings.HasPrefix(content, "---") {
		return content
	}
	rest := content[3:]
	idx := strings.Index(rest, "\n---")
	if idx < 0 {
		return content
	}
	after := rest[idx+4:]
	if len(after) > 0 && after[0] == '\n' {
		after = after[1:]
	}
	return after
}
