package cmd

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// Hook handles the "muxcode hook" subcommand.
// Usage: muxcode hook <bash|guard|analyze|inbox-poll>
func Hook(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "Usage: muxcode hook <bash|guard|analyze|inbox-poll>\n")
		os.Exit(1)
	}

	subcmd := args[0]
	switch subcmd {
	case "bash":
		hookBash()
	case "guard":
		hookGuard()
	case "analyze":
		hookAnalyze()
	case "inbox-poll":
		hookInboxPoll()
	default:
		fmt.Fprintf(os.Stderr, "Unknown hook: %s\nAvailable: bash, guard, analyze, inbox-poll\n", subcmd)
		os.Exit(1)
	}
}

// hookBash implements the PostToolUse Bash hook (replaces muxcode-bash-hook.sh).
// Detects build/test/deploy/git commands, writes history, triggers chains.
// Only fires for providers that support hooks (Claude Code). Non-hook providers
// (OpenCode TUI, local LLM) skip this entirely — they rely on system prompt
// instructions for bus messaging instead of hook-driven chains.
func hookBash() {
	session := bus.BusSession()
	if session == "" {
		return
	}
	role := bus.BusRole()

	// Gate: skip hook processing for providers that don't support hooks.
	// OpenCode TUI and local LLM agents never fire PostToolUse hooks, but
	// this guard protects against misconfigured hook registrations.
	provider := bus.ResolveProvider(role)
	if !provider.SupportsHooks() {
		return
	}

	data, err := io.ReadAll(os.Stdin)
	if err != nil || len(data) == 0 {
		return
	}

	ev, err := bus.ParseToolEvent(data)
	if err != nil || ev.ToolInput.Command == "" {
		return
	}

	result := bus.ProcessBashHook(session, role, ev)

	// Trigger chains for build, test, and deploy commands
	exitCode := ev.GetExitCode()
	outcome := bus.HookOutcome(exitCode)
	command := ev.ToolInput.Command

	// Build chain context from tool event for condition evaluation
	var ctx *bus.ChainContext
	switch result.CommandType {
	case bus.CmdBuild, bus.CmdTest, bus.CmdDeployApply:
		ctx = bus.BuildChainContext(ev)
	}

	switch result.CommandType {
	case bus.CmdBuild:
		triggerChain(session, role, "build", outcome, exitCode, command, ctx)
	case bus.CmdTest:
		triggerChain(session, role, "test", outcome, exitCode, command, ctx)
	case bus.CmdDeployApply:
		triggerChain(session, role, "deploy", outcome, exitCode, command, ctx)
	case bus.CmdUnknown:
		// Run and watch agents execute arbitrary commands — trigger their chains
		switch role {
		case "run", "runner":
			if ctx == nil {
				ctx = bus.BuildChainContext(ev)
			}
			triggerChain(session, role, "run", outcome, exitCode, command, ctx)
		case "watch":
			if ctx == nil {
				ctx = bus.BuildChainContext(ev)
			}
			triggerChain(session, role, "watch", outcome, exitCode, command, ctx)
		}
	}
	// CmdDeploy (diff/plan without apply) — no chain trigger
	// CmdGit — no chain trigger
}

// triggerChain fires the event chain and analyst notifications.
// This mirrors the logic in cmd/chain.go but called inline.
func triggerChain(session, from, eventType, outcome, exitCode, command string, ctx *bus.ChainContext) {
	// Workflow guard: prevent re-triggering when already in or past target state.
	// This breaks the test→review→test loop where review completion causes the
	// test agent to re-run tests, which re-triggers another review request.
	state := bus.ReadWorkflowState(session).State
	switch eventType {
	case "test":
		if outcome == "success" && (state == bus.StateReviewing || state == bus.StateReviewed) {
			return
		}
	case "build":
		if outcome == "success" && (state == bus.StateTesting || state == bus.StateReviewing || state == bus.StateReviewed) {
			return
		}
	case "deploy":
		if outcome == "success" && (state == bus.StateRunning || state == bus.StateWatching) {
			return
		}
	case "run":
		if outcome == "success" && state == bus.StateWatching {
			return
		}
	}

	action := bus.ResolveChain(eventType, outcome, ctx)
	if action == nil {
		return
	}

	message := bus.ExpandMessageWithContext(action.Message, exitCode, command, ctx)
	msg := bus.NewMessage(from, action.SendTo, action.Type, action.Action, message, "")

	// Atomic dedup check + send under file lock
	sent, err := bus.SendNoCCIfNotDuplicate(session, msg)
	if err != nil || !sent {
		return
	}
	_ = bus.Notify(session, action.SendTo)

	// Workflow: transition on chain outcomes
	switch eventType {
	case "build":
		if outcome == "success" {
			bus.TransitionWorkflow(session, bus.StateTesting, "chain:build:success",
				bus.WithOutcome("build", "success"))
		} else {
			bus.TransitionWorkflow(session, bus.StateBuildFail, "chain:build:failure",
				bus.WithOutcome("build", "failure"))
		}
	case "test":
		if outcome == "success" {
			bus.TransitionWorkflow(session, bus.StateReviewing, "chain:test:success",
				bus.WithOutcome("test", "success"))
		} else {
			bus.TransitionWorkflow(session, bus.StateTestFail, "chain:test:failure",
				bus.WithOutcome("test", "failure"))
		}
	case "deploy":
		if outcome == "success" {
			bus.TransitionWorkflow(session, bus.StateRunning, "chain:deploy:success",
				bus.WithOutcome("deploy", "success"))
		} else {
			bus.TransitionWorkflow(session, bus.StateDeployFail, "chain:deploy:failure",
				bus.WithOutcome("deploy", "failure"))
		}
	case "run":
		if outcome == "success" {
			bus.TransitionWorkflow(session, bus.StateWatching, "chain:run:success",
				bus.WithOutcome("run", "success"))
		} else {
			bus.TransitionWorkflow(session, bus.StateRunFail, "chain:run:failure",
				bus.WithOutcome("run", "failure"))
		}
	case "watch":
		if outcome == "success" {
			bus.TransitionWorkflow(session, bus.StateIdle, "chain:watch:success",
				bus.WithOutcome("watch", "success"))
		} else {
			bus.TransitionWorkflow(session, bus.StateWatchFail, "chain:watch:failure",
				bus.WithOutcome("watch", "failure"))
		}
	}

	// Notify analyst if configured
	if bus.ChainShouldNotifyAnalyst(eventType, outcome) && action.SendTo != "analyze" {
		var analystMsg string
		switch outcome {
		case "success":
			analystMsg = fmt.Sprintf("%s succeeded: %s", capitalize(eventType), command)
		case "failure":
			analystMsg = fmt.Sprintf("%s FAILED (exit %s): %s", capitalize(eventType), exitCode, command)
		case "unknown":
			analystMsg = fmt.Sprintf("%s completed (exit code unknown): %s", capitalize(eventType), command)
		}
		if analystMsg != "" {
			aMsg := bus.NewMessage(from, "analyze", "event", "notify", analystMsg, "")
			_ = bus.SendNoCC(session, aMsg)
		}
	}

	// Fire event subscriptions (pass context for condition evaluation)
	bus.FireSubscriptions(session, from, eventType, outcome, exitCode, command, ctx)
}

// hookGuard implements the PreToolUse Bash hook for role-aware command blocking.
// Enforces delegation rules for roles with guard rules (edit, plan, etc.).
// Only fires for providers that support hooks. Non-hook providers (OpenCode TUI)
// use permission.bash deny rules in their agent config instead.
func hookGuard() {
	session := bus.BusSession()
	if session == "" {
		return
	}

	// Check if this role has guard rules — skip roles without enforcement
	role := bus.BusRole()
	if !bus.HasGuardRules(role) {
		return
	}

	// Gate: skip guard for providers that don't support hooks.
	// OpenCode agents use permission.bash deny rules in .opencode/agents/<role>.md
	// instead of PreToolUse hook interception.
	provider := bus.ResolveProvider(role)
	if !provider.SupportsHooks() {
		return
	}

	data, err := io.ReadAll(os.Stdin)
	if err != nil || len(data) == 0 {
		return
	}

	ev, err := bus.ParseToolEvent(data)
	if err != nil || ev.ToolInput.Command == "" {
		return
	}

	decision := bus.CheckGuard(role, ev.ToolInput.Command)
	if decision != nil && decision.Blocked {
		fmt.Println(bus.FormatGuardBlock(decision.Reason))
	}
}

// hookAnalyze implements the PostToolUse Write/Edit hook
// (replaces muxcode-analyze-hook.sh).
// Only fires for providers that support hooks.
func hookAnalyze() {
	session := bus.BusSession()
	if session == "" {
		return
	}

	// Gate: skip for non-hook providers
	window := bus.BusRole()
	provider := bus.ResolveProvider(window)
	if !provider.SupportsHooks() {
		return
	}

	data, err := io.ReadAll(os.Stdin)
	if err != nil || len(data) == 0 {
		return
	}

	ev, err := bus.ParseToolEvent(data)
	if err != nil {
		return
	}

	bus.ProcessAnalyzeHook(session, window, ev)
}

// hookInboxPoll implements the PostToolUse Bash hook for inbox polling
// (replaces muxcode-inbox-poll.sh).
// Only fires for providers that support hooks.
func hookInboxPoll() {
	session := bus.BusSession()
	if session == "" {
		return
	}

	// Only run on the edit window
	window := bus.BusRole()
	if window != "edit" {
		return
	}

	// Gate: skip for non-hook providers
	provider := bus.ResolveProvider(window)
	if !provider.SupportsHooks() {
		return
	}

	data, err := io.ReadAll(os.Stdin)
	if err != nil || len(data) == 0 {
		return
	}

	ev, err := bus.ParseToolEvent(data)
	if err != nil || ev.ToolInput.Command == "" {
		return
	}

	if !bus.ShouldPollInbox(ev.ToolInput.Command) {
		return
	}

	timeoutSec := 120
	if v := os.Getenv("MUXCODE_INBOX_POLL_TIMEOUT"); v != "" {
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil && n > 0 {
			timeoutSec = n
		}
	}

	result := bus.PollInbox(session, time.Duration(timeoutSec)*time.Second, 2*time.Second)
	fmt.Println(result)
}
