package cmd

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// Hook handles the "muxcode hook" subcommand.
// Usage: muxcode hook <bash|guard|analyze|inbox-poll|stop|prompt-submit|comment-block|record>
func Hook(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "Usage: muxcode hook <bash|guard|analyze|inbox-poll|stop|prompt-submit|comment-block|record>\n")
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
	case "prompt-submit":
		hookPromptSubmit()
	case "comment-block":
		hookCommentBlock()
	case "record":
		hookRecord()
	default:
		fmt.Fprintf(os.Stderr, "Unknown hook: %s\nAvailable: bash, guard, analyze, inbox-poll, stop, prompt-submit, comment-block, record\n", subcmd)
		os.Exit(1)
	}
}

// hookSession returns the bus session a hook subprocess belongs to, or "" when
// muxcode did not launch it; every subcommand no-ops on "".
//
// It reads the raw BUS_SESSION variable rather than bus.BusSession(), whose
// tmux-name and "default" fallbacks would make a developer's own claude or
// codex a muxcode agent by accident. The project-scope .codex/hooks.json
// (MUX-159) fires for any codex opened in the repo, so without this gate a
// hand-run session would write into a bus directory nobody reads and answer
// Stop and guard decisions meant for an agent.
func hookSession() string {
	return os.Getenv("BUS_SESSION")
}

// hookBash implements the PostToolUse Bash hook (replaces muxcode-bash-hook.sh).
// Detects build/test/deploy/git commands, writes history, and fires the one
// chain bus.ChainEvent names for the call — none for a bus command, a git or
// deploy-diff call, or a passing test precheck.
// Only fires for providers on the hook road (Claude Code, hook-road Codex).
// Scrape-road providers skip this entirely — they rely on system prompt
// instructions for bus messaging instead of hook-driven chains; the provider
// gate below also protects against a misconfigured hook registration.
func hookBash() {
	session := hookSession()
	if session == "" {
		return
	}
	role := bus.BusRole()

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
	if result.Chain == "" {
		return
	}
	exitCode := ev.GetExitCode()
	triggerChain(session, role, result.Chain, bus.HookOutcome(exitCode), exitCode, ev.ToolInput.Command, bus.BuildChainContext(ev))
}

// triggerChain fires the event chain and analyst notifications.
// This mirrors the logic in cmd/chain.go but called inline.
//
// A graph run that owns this role's work suppresses the chain entirely: the
// chain routes succession off the bash exit code alone, blind to who asked,
// so a graph's build node used to detonate a second ungated build→test→review
// beside the graph's own parked nodes — the chain's test running in whatever
// tree the agent sat in rather than the run's worktree. See
// bus.GraphOwnsRunningSendNode for why the firing role, not the chain's
// target, is the right provenance key.
//
// The workflow guard at the top never re-fires a chain already in or past its
// target state: it breaks the test→review→test loop where review completion
// made the test agent re-run tests, which requested another review.
func triggerChain(session, from, eventType, outcome, exitCode, command string, ctx *bus.ChainContext) {
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

	if runID, nodeID, owned := bus.GraphOwnsRunningSendNode(session, from); owned {
		bus.LogLifecycle(session, "info", "hook", "chain-suppressed",
			fmt.Sprintf("%s chain not fired — graph run %s owns %s as node %s", eventType, runID, from, nodeID))
		return
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

// hookGuard implements the PreToolUse hook: the session, role and provider
// gates, then bus.GuardDecisionFor's one rule set, then the denial in the
// provider's dialect (FormatGuardBlockFor) with a `guard-denied` lifecycle row
// naming role, tool and reason, so every refusal is attributable. Only fires
// for providers on the hook road; scrape-road OpenCode agents use
// permission.bash deny rules in their agent config instead. The role gate
// admits any limit, not just delegation rules: Atlassian write authority and
// the hook-road evidence rule apply to roles that have none.
func hookGuard() {
	session := hookSession()
	if session == "" {
		return
	}

	role := bus.BusRole()
	if !bus.HasGuardRules(role) && !bus.HasAtlassianAuthorityLimit(role) && !bus.HasEvidenceGuard(role) {
		return
	}

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

	if d := bus.GuardDecisionFor(role, ev); d != nil && d.Blocked {
		fmt.Println(bus.FormatGuardBlockFor(provider, d.Reason))
		bus.LogLifecycle(session, "info", "hook", "guard-denied",
			fmt.Sprintf("%s: %s — %s", role, ev.ToolName, firstLine(d.Reason, 160)))
	}
}

// firstLine trims s to its first line and at most max runes, for log rows.
func firstLine(s string, max int) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if r := []rune(s); len(r) > max {
		return string(r[:max]) + "…"
	}
	return s
}

// hookAnalyze implements the PostToolUse Write/Edit hook
// (replaces muxcode-analyze-hook.sh). On Codex it fires for apply_patch, once
// per path the patch names. Only fires for providers on the hook road.
func hookAnalyze() {
	session := hookSession()
	if session == "" {
		return
	}

	// Gate: skip for scrape-road providers
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

	if len(ev.ToolInput.PatchPaths) <= 1 {
		bus.ProcessAnalyzeHook(session, window, ev)
		return
	}
	for _, p := range ev.ToolInput.PatchPaths {
		evp := *ev
		evp.ToolInput.FilePath = p
		bus.ProcessAnalyzeHook(session, window, &evp)
	}
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
	if hookSession() == "" {
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
	session := hookSession()
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

// hookStop implements the Stop hook.
//
// For a self-polling agent (Claude) it keeps the background listener alive
// across turns: when the agent finishes a turn and no `muxcode inbox --poll`
// (or `--wait`) listener is running, it blocks the stop and instructs the
// agent to re-launch the background poll — the single point of reliability
// for Claude delivery under the receipt model.
//
// For a hook-road agent with no listener (Codex, MUX-159) the Stop hook IS
// delivery: a pending request is consumed from inside the agent's own process
// (a true ack receipt) and returned as the block reason, which Codex feeds
// back as the next prompt. Each delivery consumes what it delivers, so the
// continuation's own Stop finds nothing and the agent idles — no
// stop_hook_active guard is needed to bound it.
//
// Registered globally in ~/.claude/settings.json and in the project-scope
// .codex/hooks.json, so it fires for every session of either CLI. It no-ops
// immediately outside a muxcode session (BUS_SESSION unset) and for
// scrape-road providers — matching every other muxcode hook.
//
// MUXCODE_DELIVERY_ACK_DISABLE is the rollback valve that turns the
// receipt/self-poll path off entirely. A relaunch is demanded only when an
// actionable request is actually waiting — otherwise a quiet session becomes
// a relaunch treadmill (see DecideStopHook); a response-only inbox is not
// work waiting on a listener.
func hookStop() {
	session := hookSession()
	if session == "" {
		return
	}
	role := bus.BusRole()

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

	if !provider.SelfPollsInbox() {
		if action := bus.CodexStopDelivery(session, role); action.Block {
			fmt.Println(bus.FormatStopBlock(action.Reason))
		}
		return
	}

	disabled := os.Getenv("MUXCODE_DELIVERY_ACK_DISABLE") != ""
	listenerAlive := bus.IsPolling(session, role) || bus.IsWaiting(session, role)
	inboxPending := bus.HasActionableMessages(session, role)

	action := bus.DecideStopHook(listenerAlive, stopHookActive, disabled, inboxPending)
	if action.Block {
		fmt.Println(bus.FormatStopBlock(action.Reason))
	}
}

// hookPromptSubmit implements the UserPromptSubmit hook for hook-road agents
// without a listener (Codex, MUX-159). The daemon wakes such an agent by
// typing only the fixed sentence; when that prompt is submitted this hook
// consumes the inbox in the agent's own process and returns the messages as
// additional context — never as the prompt itself (MUX-009). Any other
// prompt passes untouched.
func hookPromptSubmit() {
	session := hookSession()
	if session == "" {
		return
	}
	role := bus.BusRole()

	provider := bus.ResolveProvider(role)
	if !provider.SupportsHooks() || provider.SelfPollsInbox() {
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

	if context, ok := bus.CodexPromptSubmitContext(session, role, ev.Prompt); ok {
		fmt.Println(bus.FormatPromptContext(context))
	}
}

// hookRecord appends the raw event to <bus dir>/hook-capture.jsonl — a spike
// aid for recording a CLI's real hook payloads before a road is integrated,
// which is exactly when no provider gate can be trusted, so it has none.
func hookRecord() {
	session := hookSession()
	if session == "" {
		return
	}
	data, err := io.ReadAll(os.Stdin)
	if err != nil || len(data) == 0 {
		return
	}
	path := filepath.Join(bus.BusDir(session), "hook-capture.jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(bytes.TrimRight(data, "\n"), '\n'))
}
