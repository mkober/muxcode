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
Your inbox has already been read — do NOT run ` + "`muxcode inbox`" + `.

### Rules
1. Read the task below and execute it immediately using your tools
2. Do NOT check the inbox — your task is already here
3. Do NOT ask for confirmation — execute autonomously
4. After completing, provide a short summary of what you did

### Sending Results
Your final text response is automatically sent to the requesting agent as the reply.
Do NOT use ` + "`muxcode send`" + ` to reply — just provide a concise summary as your last text output.
Only use ` + "`muxcode send`" + ` when you need to notify a DIFFERENT agent than the requester.

### Saving Learnings
` + "```" + `
muxcode memory write "<section>" "<text>"
` + "```" + `

### Important
- NEVER run ` + "`muxcode inbox`" + ` — messages are already delivered
- NEVER send messages to yourself (` + role + `)
- Keep tool calls focused — complete the task, then stop
- If a command fails, try a different approach (do not repeat the same command)
- After executing commands, provide a short factual summary of what happened — do NOT narrate what you plan to do next`

	// Role-specific overrides
	switch role {
	case "build":
		base += `

### Build Agent Override
Start directly with the build sequence: detect project → lint → build → summarize.
Your final text response IS the reply — do NOT call muxcode send to reply.`

	case "test":
		base += `

### Test Agent Override
Start directly with the test sequence: detect test runner → run tests → summarize.
Your final text response IS the reply — do NOT call muxcode send to reply.
You MUST call the bash tool to run the actual test commands. NEVER generate test results from memory — always execute the real commands and report their actual output.`

	case "review":
		base += `

### Review Agent Override
Start directly with the review sequence: get diff → analyze → log findings → summarize.
Your final text response IS the reply — do NOT call muxcode send to reply.
Do NOT send a separate notify to edit — the bus auto-CC handles that.`
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
		return `### Build Examples

When the task specifies a command, run exactly that command:
` + "```" + `bash
./build.sh 2>&1
` + "```" + `

If no command is specified, run ` + "`./build.sh 2>&1`" + `. If that doesn't exist, try ` + "`make 2>&1`" + `.

Respond with a short summary. Do NOT call ` + "`muxcode send`" + `.`

	case "test":
		return `### Test Examples

When the task specifies a command, run exactly that command:
` + "```" + `bash
cd tools/muxcode-llm-harness && go test ./...
` + "```" + `

If no command is specified, run ` + "`./test.sh 2>&1`" + `. If that doesn't exist, try ` + "`go test -v ./... 2>&1`" + `.

Respond with a short summary of pass/fail counts. Do NOT call ` + "`muxcode send`" + `.
NEVER fabricate test results — always run the actual commands.`

	case "review":
		return `### Review Sequence

Follow these steps in order for every review request.

**Step 1 — Get the diff**
` + "```" + `bash
git diff 2>&1
git diff --cached 2>&1
` + "```" + `
If both diffs are empty, fall back to committed-but-unpushed changes:
` + "```" + `bash
git diff main...HEAD 2>&1
` + "```" + `
If still empty, report "No changes to review" and stop.

**Step 2 — Read changed files for context**
Use the Read tool on files with significant changes to understand intent.

**Step 3 — Analyze using the checklist**
Evaluate: correctness, security, performance, maintainability, tests.
Categorize each finding as must-fix, should-fix, or nit.

**Step 4 — Log detailed findings**
Write categorized findings to a temp file, then log:
` + "```" + `bash
cat > /tmp/muxcode-review-findings.txt << 'FINDINGS'
must-fix: file:line — description
should-fix: file:line — description
nit: file:line — description
FINDINGS
muxcode log review "X must-fix, Y should-fix, Z nits" --exit-code 0 --output-file /tmp/muxcode-review-findings.txt
` + "```" + `
Use ` + "`--exit-code 1`" + ` if there are any must-fix items.

**Step 5 — Provide your result summary**
Your text response is sent automatically — do NOT call ` + "`muxcode send`" + ` to reply.
Just write a concise one-line summary as your final text output:
- Clean: ` + "`Review: 0 must-fix, 1 should-fix, 2 nits — LGTM`" + `
- Issues: ` + "`Review: 1 must-fix, 2 should-fix, 0 nits — race condition in auth.go`" + `

**Important**: Do NOT send a separate notify to edit — the bus auto-CC handles that.`

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

	// Harness-specific definitions first (simplified for local LLMs),
	// then standard agent definitions.
	paths := []string{
		"agents/harness/" + name + ".md",
	}

	home, _ := os.UserHomeDir()
	if home != "" {
		paths = append(paths, home+"/.config/muxcode/agents/harness/"+name+".md")
	}

	paths = append(paths, ".claude/agents/"+name+".md")
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
	case "plan", "planner":
		return "planner"
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
	case "run", "runner":
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
	case "auto":
		return "autonomous-agent"
	case "prompt":
		return "prompt-agent"
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
