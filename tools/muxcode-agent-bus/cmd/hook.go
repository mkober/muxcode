package cmd

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/mkober/muxcode/tools/muxcode-agent-bus/bus"
)

// Hook handles the "muxcode-agent-bus hook" subcommand.
// Usage: muxcode-agent-bus hook <bash|guard|analyze|inbox-poll>
func Hook(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "Usage: muxcode-agent-bus hook <bash|guard|analyze|inbox-poll>\n")
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
func hookBash() {
	session := bus.BusSession()
	if session == "" {
		return
	}
	role := bus.BusRole()

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

	switch result.CommandType {
	case bus.CmdBuild:
		triggerChain(session, role, "build", outcome, exitCode, command)
	case bus.CmdTest:
		triggerChain(session, role, "test", outcome, exitCode, command)
	case bus.CmdDeployApply:
		triggerChain(session, role, "deploy", outcome, exitCode, command)
	}
	// CmdDeploy (diff/plan without apply) — no chain trigger
	// CmdGit — no chain trigger
}

// triggerChain fires the event chain and analyst notifications.
// This mirrors the logic in cmd/chain.go but called inline.
func triggerChain(session, from, eventType, outcome, exitCode, command string) {
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
	}

	action := bus.ResolveChain(eventType, outcome)
	if action == nil {
		return
	}

	message := bus.ExpandMessage(action.Message, exitCode, command)
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
		if outcome != "success" {
			bus.TransitionWorkflow(session, bus.StateDeployFail, "chain:deploy:failure",
				bus.WithOutcome("deploy", "failure"))
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

	// Fire event subscriptions
	bus.FireSubscriptions(session, from, eventType, outcome, exitCode, command)
}

// hookGuard implements the PreToolUse Bash hook for the edit window
// (replaces muxcode-edit-guard.sh).
func hookGuard() {
	session := bus.BusSession()
	if session == "" {
		return
	}

	// Only run on the edit window
	window := bus.BusRole()
	if window != "edit" {
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

	decision := bus.CheckEditGuard(ev.ToolInput.Command)
	if decision != nil && decision.Blocked {
		fmt.Println(bus.FormatGuardBlock(decision.Reason))
	}
}

// hookAnalyze implements the PostToolUse Write/Edit hook
// (replaces muxcode-analyze-hook.sh).
func hookAnalyze() {
	session := bus.BusSession()
	if session == "" {
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

	window := bus.BusRole()
	bus.ProcessAnalyzeHook(session, window, ev)
}

// hookInboxPoll implements the PostToolUse Bash hook for inbox polling
// (replaces muxcode-inbox-poll.sh).
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
