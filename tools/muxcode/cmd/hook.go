package cmd

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// Hook handles the "muxcode hook" subcommand.
// Usage: muxcode hook <bash|guard|analyze|inbox-poll|stop|comment-block>
func Hook(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "Usage: muxcode hook <bash|guard|analyze|inbox-poll|stop|comment-block>\n")
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
	case "stop":
		hookStop()
	case "comment-block":
		hookCommentBlock()
	default:
		fmt.Fprintf(os.Stderr, "Unknown hook: %s\nAvailable: bash, guard, analyze, inbox-poll, stop, comment-block\n", subcmd)
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

	// Atlassian write authority applies to EVERY role, not just those with
	// delegation guard rules. Jira and Confluence are shared systems the user's
	// team sees, and roles like docs, api, and pr-read have no guard rules yet
	// still inherit `Bash(muxcode *)` from the "bus" tool group — so gating this
	// behind HasGuardRules would leave them able to write.
	role := bus.BusRole()
	if !bus.HasGuardRules(role) && !bus.HasAtlassianAuthorityLimit(role) {
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
	if err != nil {
		return
	}

	// Atlassian MCP guard: an MCP tool carries no bash command, so it must be
	// gated on the tool name before the command paths below.
	if decision := bus.CheckAtlassianMCPGuard(role, ev.ToolName); decision != nil && decision.Blocked {
		fmt.Println(bus.FormatGuardBlock(decision.Reason))
		return
	}

	// Bash command guard: delegation of build/test/git/deploy/etc.
	if ev.ToolInput.Command != "" {
		// Atlassian writes first — checked for every role, whereas CheckGuard
		// only has rules for edit and plan.
		if decision := bus.CheckAtlassianCommandGuard(role, ev.ToolInput.Command); decision != nil && decision.Blocked {
			fmt.Println(bus.FormatGuardBlock(decision.Reason))
			return
		}
		if decision := bus.CheckGuard(role, ev.ToolInput.Command); decision != nil && decision.Blocked {
			fmt.Println(bus.FormatGuardBlock(decision.Reason))
		}
		return
	}

	// Write/Edit/NotebookEdit file guard: documentation under docs/ must be
	// authored by the plan agent, not written directly in the edit window.
	filePath := ev.ToolInput.FilePath
	if filePath == "" {
		filePath = ev.ToolInput.NotebookPath
	}
	if filePath != "" {
		if decision := bus.CheckDocFileGuard(role, filePath); decision != nil && decision.Blocked {
			fmt.Println(bus.FormatGuardBlock(decision.Reason))
		}
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

// hookCommentBlock implements the PostToolUse Write|Edit hook that enforces the
// code-comments skill's structural rule: rationale lives at the boundary, never
// wedged between statements.
//
// It exists because the skill alone did not hold. Loaded once at session start,
// it sat among thousands of tokens of standing instructions and was not in the
// working set twenty tool calls into writing code — the rule was known and still
// broken. A hook fires whether or not it is remembered, which is the only
// property that matters here.
//
// Only the text the edit introduced is scanned, never the whole file, so an
// author is told about the block they just wrote and never about pre-existing
// ones in a file they merely touched.
func hookCommentBlock() {
	if bus.BusSession() == "" {
		return
	}

	provider := bus.ResolveProvider(bus.BusRole())
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

	// Edit carries the replacement in NewString; Write carries the whole file
	// in Content.
	text := ev.ToolInput.NewString
	if text == "" {
		text = ev.ToolInput.Content
	}

	findings := bus.ScanCommentBlocks(ev.ToolInput.FilePath, text)
	if len(findings) == 0 {
		return
	}

	fmt.Println(bus.FormatGuardBlock(bus.FormatCommentBlockReason(ev.ToolInput.FilePath, findings)))
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

// hookStop implements the Stop hook: it keeps a Claude agent's self-poll
// listener alive across turns. When the agent finishes a turn and no
// `muxcode inbox --poll` (or `--wait`) listener is running, it blocks the stop
// and instructs the agent to re-launch the background poll — the single point
// of reliability for Claude delivery under the receipt model.
//
// Registered globally in ~/.claude/settings.json, so it fires for every Claude
// Code session. It no-ops immediately outside a muxcode session (BusSession
// empty) and for non-hook providers — matching every other muxcode hook.
func hookStop() {
	session := bus.BusSession()
	if session == "" {
		return // not in a muxcode session — global hook, stay silent
	}
	role := bus.BusRole()

	// Gate: only Claude (hook providers) self-poll via a background Bash tool.
	// The harness self-polls in-process (Phase 3); OpenCode/Codex get
	// verified-inject delivery (Phase 4) — neither re-launches via this hook.
	provider := bus.ResolveProvider(role)
	if !provider.SupportsHooks() {
		return
	}

	// Read the Stop event (best-effort) for the stop_hook_active loop guard.
	stopHookActive := false
	if data, err := io.ReadAll(os.Stdin); err == nil && len(data) > 0 {
		if ev, err := bus.ParseToolEvent(data); err == nil {
			stopHookActive = ev.StopHookActive
		}
	}

	// Kill switch: MUXCODE_DELIVERY_ACK_DISABLE turns off the receipt/self-poll
	// path entirely (rollback valve during rollout). Phase 5 extends the same
	// env to the daemon cutover.
	disabled := os.Getenv("MUXCODE_DELIVERY_ACK_DISABLE") != ""

	// A listener is alive if a --poll or --wait loop is currently running.
	listenerAlive := bus.IsPolling(session, role) || bus.IsWaiting(session, role)

	// Only demand a relaunch when there is actually something to deliver —
	// otherwise a quiet session turns into a relaunch treadmill (see
	// DecideStopHook). Request-type messages only: a response-only inbox is not
	// work waiting on a listener.
	inboxPending := bus.HasActionableMessages(session, role)

	action := bus.DecideStopHook(listenerAlive, stopHookActive, disabled, inboxPending)
	if action.Block {
		fmt.Println(bus.FormatStopBlock(action.Reason))
	}
}
