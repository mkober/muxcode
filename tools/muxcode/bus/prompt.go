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
	b.WriteString(fmt.Sprintf("**You are the %s agent.** You are part of a multi-agent tmux session. Use the message bus to communicate with other agents.\n\n", role))

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

	// Git conventions — applies to all agents that may create commits
	b.WriteString("### Git Conventions\n")
	b.WriteString("- Do NOT add a `Co-Authored-By` trailer to commit messages\n\n")

	// Protocol — all agents are woken by the daemon when messages arrive
	b.WriteString("### Protocol\n")
	b.WriteString("- **Do NOT poll for messages.** The daemon process automatically detects when you have unread messages and wakes you by typing \"You have new messages\" at your prompt. ")
	b.WriteString("Just process your messages, reply, and go idle — you will be woken when new work arrives.\n")
	b.WriteString("- When prompted with \"You have new messages\", immediately run `muxcode inbox` and act on every message without asking\n")
	b.WriteString("- After completing each task, run `muxcode inbox --peek` to check for new messages before going idle\n")
	b.WriteString("- Reply to requests with `--type response --reply-to <id>`\n")
	b.WriteString("- Save important learnings to memory after completing tasks\n")
	b.WriteString("- Never wait for human input — process all requests autonomously\n\n")

	// Non-hook provider instructions: since OpenCode TUI and local LLM agents
	// don't have PreToolUse/PostToolUse hooks, they must send bus messages
	// manually after build/test/deploy commands instead of relying on chains.
	// Only show instructions relevant to the agent's actual role to avoid
	// confusing the LLM about its identity.
	provider := ResolveProvider(role)
	if !provider.SupportsHooks() && role != "edit" {
		b.WriteString("### Manual Bus Messaging (no hook support)\n")
		b.WriteString("Your AI CLI does not support automatic hooks, so you must send bus messages manually after completing tasks.\n\n")

		switch role {
		case "build":
			b.WriteString("**After build commands** (`./build.sh`, `make`, `pnpm build`, etc.):\n")
			b.WriteString("```bash\n# On success:\nmuxcode send edit build \"Build succeeded\" --type response --reply-to <id>\n")
			b.WriteString("# On failure:\nmuxcode send edit build \"Build FAILED: <error summary>\" --type response --reply-to <id>\n```\n\n")
		case "test":
			b.WriteString("**After test commands** (`pnpm test`, `jest`, `pytest`, `go test`, etc.):\n")
			b.WriteString("```bash\n# On success:\nmuxcode send edit test \"Tests passed\" --type response --reply-to <id>\n")
			b.WriteString("# On failure:\nmuxcode send edit test \"Tests FAILED: <error summary>\" --type response --reply-to <id>\n```\n\n")
		case "deploy":
			b.WriteString("**After deploy commands** (`cdk deploy`, `terraform apply`, etc.):\n")
			b.WriteString("```bash\n# On success:\nmuxcode send edit deploy \"Deploy succeeded\" --type response --reply-to <id>\n")
			b.WriteString("# On failure:\nmuxcode send edit deploy \"Deploy FAILED: <error summary>\" --type response --reply-to <id>\n```\n\n")
		case "analyze":
			b.WriteString("**After completing analysis**, always reply to the edit agent:\n")
			b.WriteString("```bash\nmuxcode send edit response \"<analysis summary>\" --type response --reply-to <id>\n```\n\n")
		default:
			// For review, commit, watch, and other non-build/test/deploy roles,
			// give generic reply instructions instead of irrelevant build/test examples.
			b.WriteString("**After completing a task**, reply to the requester (usually `edit`):\n")
			b.WriteString("```bash\nmuxcode send edit response \"<result summary>\" --type response --reply-to <id>\n```\n\n")
		}

		b.WriteString("These messages replace the automatic hook-driven chains that Claude Code agents use. ")
		b.WriteString("Always send a result message so the edit agent knows your task is complete.\n\n")

		// Console history logging — non-hook agents must log manually so the
		// left-pane console view has data to render.
		b.WriteString("### Console History Logging\n")
		b.WriteString("After running commands, log the result so the console dashboard (left pane) updates.\n")
		b.WriteString("Write command output to a temp file, then call `muxcode log`:\n\n")

		switch role {
		case "build":
			b.WriteString("```bash\n# Capture output to temp file, then log:\n")
			b.WriteString("tmpfile=$(mktemp /tmp/muxcode-log-XXXXXX.txt)\n")
			b.WriteString("./build.sh 2>&1 | tee \"$tmpfile\"; exit_code=${PIPESTATUS[0]}\n")
			b.WriteString("muxcode log build \"Build summary\" --exit-code \"$exit_code\" --command \"./build.sh\" --output-file \"$tmpfile\"\n")
			b.WriteString("rm -f \"$tmpfile\"\n```\n\n")
		case "test":
			b.WriteString("```bash\n# Capture output to temp file, then log:\n")
			b.WriteString("tmpfile=$(mktemp /tmp/muxcode-log-XXXXXX.txt)\n")
			b.WriteString("<test command> 2>&1 | tee \"$tmpfile\"; exit_code=${PIPESTATUS[0]}\n")
			b.WriteString("muxcode log test \"Test summary\" --exit-code \"$exit_code\" --command \"<test command>\" --output-file \"$tmpfile\"\n")
			b.WriteString("rm -f \"$tmpfile\"\n```\n\n")
		case "deploy":
			b.WriteString("```bash\n# Capture output to temp file, then log:\n")
			b.WriteString("tmpfile=$(mktemp /tmp/muxcode-log-XXXXXX.txt)\n")
			b.WriteString("<deploy command> 2>&1 | tee \"$tmpfile\"; exit_code=${PIPESTATUS[0]}\n")
			b.WriteString("muxcode log deploy \"Deploy summary\" --exit-code \"$exit_code\" --command \"<deploy command>\" --output-file \"$tmpfile\"\n")
			b.WriteString("rm -f \"$tmpfile\"\n```\n\n")
		case "review":
			b.WriteString("```bash\n# After completing a review, log the findings:\n")
			b.WriteString("tmpfile=$(mktemp /tmp/muxcode-log-XXXXXX.txt)\n")
			b.WriteString("echo \"<review findings summary>\" > \"$tmpfile\"\n")
			b.WriteString("muxcode log review \"Review summary\" --exit-code 0 --output-file \"$tmpfile\"\n")
			b.WriteString("rm -f \"$tmpfile\"\n```\n\n")
		default:
			b.WriteString("```bash\n# Log task output:\n")
			b.WriteString("tmpfile=$(mktemp /tmp/muxcode-log-XXXXXX.txt)\n")
			b.WriteString("echo \"<output>\" > \"$tmpfile\"\n")
			b.WriteString(fmt.Sprintf("muxcode log %s \"Task summary\" --exit-code 0 --output-file \"$tmpfile\"\n", role))
			b.WriteString("rm -f \"$tmpfile\"\n```\n\n")
		}

		b.WriteString("**Always log before sending your response message.** The console polls every 5 seconds and will pick up the entry.\n\n")
	}

	// Send restrictions from policy — only for hook-supporting providers.
	// Non-hook providers need to manually chain, so restrictions don't apply.
	cfg := Config()
	if cfg.SendPolicy != nil && provider.SupportsHooks() {
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
