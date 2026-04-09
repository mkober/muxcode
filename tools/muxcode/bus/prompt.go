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
	b.WriteString("```bash\nmuxcode inbox\n```\n\n")

	// Send Messages
	b.WriteString("### Send Messages\n")
	b.WriteString("```bash\nmuxcode send <target> <action> \"<short single-line message>\"\n```\n")
	b.WriteString("Targets: edit, build, test, review, deploy, run, commit, analyze, docs, research, watch, pr-read\n\n")
	b.WriteString("**CRITICAL: All `send` messages MUST be short, single-line strings with NO newlines.** ")
	b.WriteString("The `Bash(muxcode *)` permission glob does NOT match newlines — ")
	b.WriteString("any multi-line command will trigger a permission prompt and block the agent.\n\n")

	// Memory
	b.WriteString("### Memory\n")
	b.WriteString("```bash\nmuxcode memory context          # read shared + own memory\n")
	b.WriteString("muxcode memory write \"<section>\" \"<text>\"  # save learnings\n```\n\n")

	// Skills
	b.WriteString("### Skills\n")
	b.WriteString("```bash\nmuxcode skill list --role <role>\n")
	b.WriteString("muxcode skill search <query>\n")
	b.WriteString("muxcode skill load <name>\n")
	b.WriteString("muxcode skill create <name> <desc> [--roles r1,r2] [--tags t1,t2] <body>\n```\n\n")

	// Session Management
	b.WriteString("### Session Management\n")
	b.WriteString("```bash\nmuxcode session status           # check session uptime and compact count\n")
	b.WriteString("muxcode session compact \"<summary>\"  # save session summary to memory\n```\n\n")
	b.WriteString("**When to compact**: After completing a major task or when your session has been running for a long time. ")
	b.WriteString("Summaries are automatically restored on restart.\n\n")
	b.WriteString("**Combined compact**: When the user says \"compact\", when you receive a `compact-recommended` alert, ")
	b.WriteString("or whenever you decide to compact, always do both steps together:\n")
	b.WriteString("1. Save context to memory: `muxcode session compact \"<summary of key work, decisions, and state>\"`\n")
	b.WriteString("2. Trigger conversation compression: run `muxcode compact` in the background — ")
	b.WriteString("it waits for the agent to go idle, then injects `/compact` via tmux send-keys.\n")
	b.WriteString("   ```bash\n   muxcode compact  # run in background (Bash run_in_background=true)\n   ```\n\n")
	b.WriteString("This preserves learnings across sessions (step 1) and keeps the current session lean (step 2). ")
	b.WriteString("**Important**: Do NOT output `/compact` as text — it is a built-in slash command that only works when typed at the `❯` prompt. ")
	b.WriteString("The `muxcode compact` command handles this automatically.\n\n")

	// Output visibility — critical for tmux-based monitoring
	b.WriteString("### Output Visibility\n")
	b.WriteString("Claude Code's TUI collapses tool calls into terse summaries like \"Ran 5 bash commands\". ")
	b.WriteString("Since your tmux pane is monitored by the console and by other agents via `tmux capture-pane`, ")
	b.WriteString("you MUST produce visible text output so observers can tell what you are doing:\n")
	b.WriteString("- **Before** running commands, briefly state what you are about to do\n")
	b.WriteString("- **After** each significant command, report the key results as text (not just the tool output)\n")
	b.WriteString("- **Never** run a batch of commands silently — intersperse text explaining progress\n")
	b.WriteString("- On failure, always restate the error message and what went wrong in your text response\n")
	b.WriteString("- On success, summarize what was accomplished (e.g. \"Deployed 3 stacks, 12 resources updated, no errors\")\n\n")

	// Protocol — edit agent uses background polling; all others are woken by the watcher
	b.WriteString("### Protocol\n")
	if role == "edit" {
		b.WriteString("- **Message polling**: after processing inbox messages (or when idle), start `muxcode inbox --poll` as a background Bash tool (timeout: 300s). ")
		b.WriteString("It watches for new messages and returns them when they arrive. Process them immediately, then restart the poll.\n")
		b.WriteString("- If the poll times out with no messages, restart it — this keeps you responsive while idle.\n")
		b.WriteString("- **IMPORTANT**: Always start polling immediately on session start, even if there are no messages to process.\n")
	} else {
		b.WriteString("- **Do NOT poll for messages.** The watcher process automatically detects when you have unread messages and wakes you by typing \"You have new messages\" at your prompt. ")
		b.WriteString("Just process your messages, reply, and go idle — you will be woken when new work arrives.\n")
	}
	b.WriteString("- When prompted with \"You have new messages\", immediately run `muxcode inbox` and act on every message without asking\n")
	b.WriteString("- After completing each task, run `muxcode inbox --peek` to check for new messages before going idle\n")
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
