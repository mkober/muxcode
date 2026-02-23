package bus

import (
	"fmt"
	"strings"
)

// SharedPrompt generates the common Agent Coordination system prompt for a role.
// This replaces the duplicated markdown section across all agent files.
func SharedPrompt(role string) string {
	var b strings.Builder

	b.WriteString("## Agent Coordination\n\n")
	b.WriteString("You are part of a multi-agent tmux session. Use the message bus to communicate with other agents.\n\n")

	// Check Messages
	b.WriteString("### Check Messages\n")
	b.WriteString("```bash\nmuxcode-agent-bus inbox\n```\n\n")

	// Send Messages
	b.WriteString("### Send Messages\n")
	b.WriteString("```bash\nmuxcode-agent-bus send <target> <action> \"<short single-line message>\"\n```\n")
	b.WriteString("Targets: edit, build, test, review, deploy, run, commit, analyze, docs, research\n\n")
	b.WriteString("**CRITICAL: All `send` messages MUST be short, single-line strings with NO newlines.** ")
	b.WriteString("The `Bash(muxcode-agent-bus *)` permission glob does NOT match newlines — ")
	b.WriteString("any multi-line command will trigger a permission prompt and block the agent.\n\n")

	// Memory
	b.WriteString("### Memory\n")
	b.WriteString("```bash\nmuxcode-agent-bus memory context          # read shared + own memory\n")
	b.WriteString("muxcode-agent-bus memory write \"<section>\" \"<text>\"  # save learnings\n```\n\n")

	// Protocol
	b.WriteString("### Protocol\n")
	b.WriteString("- When prompted with \"You have new messages\", immediately run `muxcode-agent-bus inbox` and act on every message without asking\n")
	b.WriteString("- Reply to requests with `--type response --reply-to <id>`\n")
	b.WriteString("- Save important learnings to memory after completing tasks\n")
	b.WriteString("- Never wait for human input — process all requests autonomously\n\n")

	// Send restrictions from policy
	cfg := Config()
	if cfg.SendPolicy != nil {
		if policy, ok := cfg.SendPolicy[role]; ok && len(policy.Deny) > 0 {
			b.WriteString("### Send Restrictions\n")
			for _, denied := range policy.Deny {
				b.WriteString(fmt.Sprintf("- **Do NOT send messages to %s** — the hook-driven chain handles this automatically\n", denied))
			}
			b.WriteString("\n")
		}
	}

	return b.String()
}
