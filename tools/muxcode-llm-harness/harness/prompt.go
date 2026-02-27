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
	base := `## How You Work

You are an autonomous agent. Tasks are delivered in the user message below.
Your inbox has already been read — do NOT run ` + "`muxcode-agent-bus inbox`" + `.

### Rules
1. Read the task below and execute it immediately using your tools
2. Do NOT check the inbox — your task is already here
3. Do NOT ask for confirmation — execute autonomously
4. After completing, provide a short summary of what you did

### Sending Results
Your final text response is automatically sent to the requesting agent as the reply.
Do NOT use ` + "`muxcode-agent-bus send`" + ` to reply — just provide a concise summary as your last text output.
Only use ` + "`muxcode-agent-bus send`" + ` when you need to notify a DIFFERENT agent than the requester.

### Saving Learnings
` + "```" + `
muxcode-agent-bus memory write "<section>" "<text>"
` + "```" + `

### Important
- NEVER run ` + "`muxcode-agent-bus inbox`" + ` — messages are already delivered
- NEVER send messages to yourself (` + role + `)
- Keep tool calls focused — complete the task, then stop
- If a command fails, try a different approach (do not repeat the same command)
- After executing commands, provide a short factual summary of what happened — do NOT narrate what you plan to do next`

	// Role-specific overrides
	switch role {
	case "build":
		base += `

### Build Agent Override
The agent definition mentions "Run muxcode-agent-bus inbox" as step 1 — skip that step entirely.
Your task has already been delivered above. Start directly with the build sequence.
Follow the build sequence in the examples below: detect project → lint → build → summarize.
Your final text response IS the reply — do NOT call muxcode-agent-bus send to reply.`
	}

	return base
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
		return `### Build Sequence

Follow these 4 steps in order for every build request.

**Step 1 — Detect project type**
` + "```" + `bash
ls go.mod package.json Cargo.toml Makefile 2>/dev/null
` + "```" + `

**Step 2 — Lint (failures here do NOT block the build)**
Choose the linter for the detected project type:
` + "```" + `bash
# Go
gofmt -l . 2>&1
go vet ./... 2>&1

# Node (package.json)
npm run lint 2>&1

# Rust (Cargo.toml)
cargo clippy 2>&1
` + "```" + `
If no linter is available, skip this step. Report lint warnings in your reply but continue to step 3.

**Step 3 — Build**
` + "```" + `bash
./build.sh 2>&1
` + "```" + `
If ` + "`./build.sh`" + ` does not exist, try these fallbacks in order:
` + "```" + `bash
make build 2>&1
go build ./... 2>&1
npm run build 2>&1
cargo build 2>&1
` + "```" + `

**Step 4 — Provide your result summary**
Your text response is sent automatically — do NOT call ` + "`muxcode-agent-bus send`" + ` to reply.
Just write a concise summary as your final text output:
- On success: ` + "`Build succeeded: <what was built>`" + `
- On failure: ` + "`Build FAILED: <error summary>`" + `

**Important**: Do NOT send to test — hooks handle chaining automatically.`

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
