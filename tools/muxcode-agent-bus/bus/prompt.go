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
	b.WriteString("Targets: edit, build, test, review, deploy, run, commit, analyze, docs, research, watch, pr-read\n\n")
	b.WriteString("**CRITICAL: All `send` messages MUST be short, single-line strings with NO newlines.** ")
	b.WriteString("The `Bash(muxcode-agent-bus *)` permission glob does NOT match newlines — ")
	b.WriteString("any multi-line command will trigger a permission prompt and block the agent.\n\n")

	// Memory
	b.WriteString("### Memory\n")
	b.WriteString("```bash\nmuxcode-agent-bus memory context          # read shared + own memory\n")
	b.WriteString("muxcode-agent-bus memory write \"<section>\" \"<text>\"  # save learnings\n```\n\n")

	// Skills
	b.WriteString("### Skills\n")
	b.WriteString("```bash\nmuxcode-agent-bus skill list --role <role>\n")
	b.WriteString("muxcode-agent-bus skill search <query>\n")
	b.WriteString("muxcode-agent-bus skill load <name>\n")
	b.WriteString("muxcode-agent-bus skill create <name> <desc> [--roles r1,r2] [--tags t1,t2] <body>\n```\n\n")

	// Session Management
	b.WriteString("### Session Management\n")
	b.WriteString("```bash\nmuxcode-agent-bus session status           # check session uptime and compact count\n")
	b.WriteString("muxcode-agent-bus session compact \"<summary>\"  # save session summary to memory\n```\n\n")
	b.WriteString("**When to compact**: After completing a major task or when your session has been running for a long time. ")
	b.WriteString("Summaries are automatically restored on restart.\n\n")
	b.WriteString("**Combined compact**: When the user says \"compact\", when you receive a `compact-recommended` alert, ")
	b.WriteString("or whenever you decide to compact, always do both steps together:\n")
	b.WriteString("1. Save context to memory: `muxcode-agent-bus session compact \"<summary of key work, decisions, and state>\"`\n")
	b.WriteString("2. Trigger conversation compression: run `muxcode-compact.sh` in the background — ")
	b.WriteString("it waits for the agent to go idle, then injects `/compact` via tmux send-keys.\n")
	b.WriteString("   ```bash\n   muxcode-compact.sh  # run in background (Bash run_in_background=true)\n   ```\n\n")
	b.WriteString("This preserves learnings across sessions (step 1) and keeps the current session lean (step 2). ")
	b.WriteString("**Important**: Do NOT output `/compact` as text — it is a built-in slash command that only works when typed at the `❯` prompt. ")
	b.WriteString("The `muxcode-compact.sh` script handles this automatically.\n\n")

	// Protocol
	b.WriteString("### Protocol\n")
	b.WriteString("- When prompted with \"You have new messages\", immediately run `muxcode-agent-bus inbox` and act on every message without asking\n")
	b.WriteString("- After completing each task, run `muxcode-agent-bus inbox --peek` to check for new messages before going idle\n")
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
