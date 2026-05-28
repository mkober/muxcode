package cmd

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// Send handles the "muxcode send" subcommand.
// Usage: muxcode send <to> <action> "<payload>" [--type TYPE] [--reply-to ID] [--no-notify] [--force] [--wait] [--track]
func Send(args []string) {
	if len(args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: muxcode send <to> <action> \"<payload>\" [--type TYPE] [--reply-to ID] [--no-notify] [--force] [--wait] [--track]\n")
		os.Exit(1)
	}

	to := args[0]
	action := args[1]

	// Scan all remaining args for flags first, then determine payload source.
	payload := ""
	msgType := "request"
	replyTo := ""
	noNotify := false
	force := false
	wait := false
	track := false
	payloadSet := false

	remaining := args[2:]
	for i := 0; i < len(remaining); i++ {
		switch remaining[i] {
		case "--type":
			if i+1 >= len(remaining) {
				fmt.Fprintf(os.Stderr, "Error: --type requires a value\n")
				os.Exit(1)
			}
			i++
			msgType = remaining[i]
		case "--reply-to":
			if i+1 >= len(remaining) {
				fmt.Fprintf(os.Stderr, "Error: --reply-to requires a value\n")
				os.Exit(1)
			}
			i++
			replyTo = remaining[i]
		case "--no-notify":
			noNotify = true
		case "--force":
			force = true
		case "--wait":
			wait = true
		case "--track":
			track = true
		default:
			if strings.HasPrefix(remaining[i], "--") {
				fmt.Fprintf(os.Stderr, "Unknown flag: %s\n", remaining[i])
				os.Exit(1)
			}
			// First non-flag argument is the payload
			if !payloadSet {
				payload = remaining[i]
				payloadSet = true
			} else {
				fmt.Fprintf(os.Stderr, "Unexpected argument: %s\n", remaining[i])
				os.Exit(1)
			}
		}
	}

	// --wait and --track are mutually exclusive
	if wait && track {
		fmt.Fprintf(os.Stderr, "Error: --wait and --track are mutually exclusive\n")
		os.Exit(1)
	}

	if !payloadSet {
		fmt.Fprintf(os.Stderr, "Error: payload is required\n")
		os.Exit(1)
	}

	// Validate payload content
	for _, w := range validatePayload(payload) {
		fmt.Fprintf(os.Stderr, "Warning: %s\n", w)
	}

	// Validate target role
	if !bus.IsKnownRole(to) {
		fmt.Fprintf(os.Stderr, "Error: unknown role '%s'. Known roles: %s\n", to, strings.Join(bus.KnownRoles, ", "))
		os.Exit(1)
	}

	session := bus.BusSession()
	from := bus.BusRole()

	// Check send policy (hard error)
	if deny := bus.CheckSendPolicy(from, to); deny != "" {
		fmt.Fprintf(os.Stderr, "Error: %s\n", deny)
		os.Exit(1)
	}

	// Pre-commit safeguard: block sends to commit agent unless all agents are idle
	if to == "commit" && isCommitAction(action) && !force {
		if err := bus.PreCommitCheck(session); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %s\n", err)
			os.Exit(1)
		}
	}

	// Pre-send inbox check: if sending a request, check if the sender already
	// has unread responses from the target role. This prevents redundant requests
	// when hook chains have already produced results (e.g., build→test→review
	// auto-chained, and edit tries to manually send another build request).
	if msgType == "request" && !force {
		if consumed := consumeExistingResponses(session, from, to, action); consumed {
			return
		}
	}

	// Pre-send task dedup: if there's already an in-flight task for the same
	// (to, action), reattach --wait to the existing task instead of sending a
	// duplicate. This handles the common case where --wait was killed by Bash
	// tool timeout and the agent retries the same request. Without this, the
	// duplicate message gets injected into the agent's TUI, wasting tokens.
	// Skip for --force (explicit override) and non-request messages.
	// For --track, just report the existing task and return (no polling).
	if msgType == "request" && !force && track {
		if existing, found := bus.FindInFlightTask(session, to, action); found {
			fmt.Printf("In-flight task for %s:%s already exists (sent %ds ago) — already tracking\n",
				to, action, time.Now().Unix()-existing.SentAt)
			return
		}
	}
	if msgType == "request" && !force && wait {
		if existing, found := bus.FindInFlightTask(session, to, action); found {
			fmt.Printf("In-flight task for %s:%s already exists (sent %ds ago) — reattaching --wait\n",
				to, action, time.Now().Unix()-existing.SentAt)
			bus.SetWaiting(session, from)
			responded, responsePayload := waitForResponse(session, from, to, existing.ID)
			if responded {
				ds, err := bus.ReadDeliveryStatus(session, existing.ID)
				if err == nil && ds.ResponseID != "" {
					bus.CompleteTask(session, existing.ID, ds.ResponseID)
				} else {
					bus.CompleteTask(session, existing.ID, "")
				}
				logWaitResponseToHistory(session, to, action, responsePayload)
			} else {
				bus.TimeoutTask(session, existing.ID)
			}
			go func() {
				time.Sleep(5 * time.Second)
				bus.ClearWaiting(session, from)
			}()
			return
		}
	}

	msg := bus.NewMessage(from, to, msgType, action, payload, replyTo)

	// Atomic dedup check + send under file lock to avoid TOCTOU race
	sent, err := bus.SendIfNotDuplicate(session, msg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error sending message: %v\n", err)
		os.Exit(1)
	}
	if !sent {
		fmt.Printf("Suppressed duplicate %s:%s to %s (within dedup window)\n", msgType, action, to)
		return
	}

	if !noNotify {
		// For modal roles, auto-open the modal (or spawn headless if no client).
		// The modal agent reads its inbox on startup — no send-keys needed.
		if bus.IsModalRole(to) {
			_ = bus.OpenOrSpawn(session, to, "")
		} else {
			_ = bus.Notify(session, to)
		}
		// Also notify edit when auto-CC fires (message from build/test/review
		// to a non-edit target). The daemon skips edit to prevent duplicates,
		// so cmd/send.go is responsible for all edit notifications.
		if bus.IsAutoCCRole(from) && to != "edit" && bus.WindowForRole(to) != "edit" {
			_ = bus.Notify(session, "edit")
		}
	}

	fmt.Printf("Sent %s:%s to %s\n", msgType, action, to)

	// --track: create a task for tracking but return immediately without blocking.
	// The daemon's checkTrackedTasks() auto-completes the task when the response
	// arrives, and checkInboxes() wakes the sender when the response hits their
	// inbox. This lets the sender continue working on other tasks.
	if track {
		waitTimeout := resolveWaitTimeout()
		_ = bus.CreateTask(session, msg, waitTimeout)
		fmt.Printf("Tracking task %s — response will arrive in inbox\n", msg.ID)
		return
	}

	// --wait: poll own inbox until a response from the target arrives or timeout.
	// Set a waiting marker so Notify() skips send-keys for our role — the
	// --wait loop is already polling and send-keys would interrupt the
	// running Bash tool in Claude Code's TUI.
	if wait {
		// Create task entry for orchestrator tracking
		waitTimeout := resolveWaitTimeout()
		_ = bus.CreateTask(session, msg, waitTimeout)

		bus.SetWaiting(session, from)
		responded, responsePayload := waitForResponse(session, from, to, msg.ID)

		// Update task status based on outcome
		if responded {
			// Task completed — find the response message ID from delivery status
			ds, err := bus.ReadDeliveryStatus(session, msg.ID)
			if err == nil && ds.ResponseID != "" {
				bus.CompleteTask(session, msg.ID, ds.ResponseID)
			} else {
				bus.CompleteTask(session, msg.ID, "")
			}

			// Log to console history so the target role's left-pane view updates.
			// Non-hook providers don't have PostToolUse hooks, and the daemon's
			// checkNonHookTasks won't see the task (it's already completed). Without
			// this, the console stays empty for roles like review.
			logWaitResponseToHistory(session, to, action, responsePayload)
		} else {
			bus.TimeoutTask(session, msg.ID)
		}

		// Keep the waiting marker alive briefly after --wait completes.
		// Between --wait finishing and the agent's next tool call, the agent
		// pane shows ❯ momentarily. The grace period prevents unnecessary
		// display-message notifications during this window.
		go func() {
			time.Sleep(5 * time.Second)
			bus.ClearWaiting(session, from)
		}()
	}
}

// waitForResponse watches the delivery status file for the sent message,
// waiting for it to transition to "responded". This avoids racing with
// the background --poll loop which also reads the inbox — previously both
// competed for the same inbox file, causing --wait to miss responses.
//
// When a response is detected, attempts to consume the response message
// from the inbox for display. If the background --poll already consumed
// it (and printed it as a task result), prints a short confirmation.
//
// Timeout is controlled by MUXCODE_INBOX_POLL_TIMEOUT (default 600s).
// Returns true if a response was received, false on timeout.
func waitForResponse(session, role, target, msgID string) (bool, string) {
	timeout := resolveWaitTimeout()

	// For hosted roles, also accept responses from the host agent
	host := bus.WindowForRole(target)
	acceptFrom := func(from string) bool {
		return from == target || from == host
	}

	// Poll delivery status at 500ms — just stat + read a small JSON file,
	// no inbox locking or contention with --poll.
	const pollMs = 500
	deadline := time.Now().Add(time.Duration(timeout) * time.Second)

	var polls int
	for time.Now().Before(deadline) {
		time.Sleep(time.Duration(pollMs) * time.Millisecond)
		polls++

		// Primary: check delivery status (set by MarkResponded when --reply-to is used)
		ds, err := bus.ReadDeliveryStatus(session, msgID)
		if err == nil && ds.Status == bus.StatusResponded {
			// Response detected — try to consume it from inbox for display.
			// The background --poll may have already consumed it, which is fine.
			if bus.HasMessages(session, role) {
				msgs, err := bus.ReceiveFromFunc(session, role, acceptFrom)
				if err == nil && len(msgs) > 0 {
					fmt.Println()
					var payload string
					for _, m := range msgs {
						fmt.Print(bus.FormatMessage(m))
						fmt.Println()
						if payload == "" {
							payload = m.Payload
						}
					}
					return true, payload
				}
			}

			// Response was already consumed by --poll — print confirmation
			fmt.Printf("\nResponse from %s received (delivered via poll)\n", target)
			return true, ""
		}

		// Fallback: peek inbox for responses without --reply-to (every ~2.5s).
		// This handles agents that send responses without linking them to the
		// original request — the delivery status never transitions, but the
		// response message is in the inbox.
		if polls%5 == 0 {
			if msgs, err := bus.Peek(session, role); err == nil {
				for _, m := range msgs {
					if (m.From == target || m.From == host) && m.Type == "response" {
						consumed, cErr := bus.ReceiveFromFunc(session, role, acceptFrom)
						if cErr == nil && len(consumed) > 0 {
							fmt.Println()
							var payload string
							for _, c := range consumed {
								fmt.Print(bus.FormatMessage(c))
								fmt.Println()
								if payload == "" {
									payload = c.Payload
								}
							}
							// Best-effort: mark the original request as responded
							// so the task lifecycle completes normally.
							bus.MarkResponded(session, msgID, consumed[0].ID)
							return true, payload
						}
						break // already attempted consume
					}
				}
			}
		}
	}

	fmt.Fprintf(os.Stderr, "\nNo response from %s within %ds — check: muxcode inbox --peek\n", target, timeout)
	return false, ""
}

// resolveWaitTimeout returns the --wait timeout in seconds.
func resolveWaitTimeout() int {
	timeout := 600
	if v := os.Getenv("MUXCODE_INBOX_POLL_TIMEOUT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			timeout = n
		}
	}
	return timeout
}

// logWaitResponseToHistory writes a console history entry for the target role
// when --wait receives a response. Non-hook providers don't have PostToolUse
// hooks to log history, and the daemon's checkNonHookTasks skips tasks that
// are already completed by --wait. Without this, left-pane console views for
// non-hook agents (e.g. review on Codex) remain empty.
func logWaitResponseToHistory(session, role, action, payload string) {
	// Skip research — the research agent self-logs findings via `muxcode log`
	// with richer metadata. The daemon also skips research in logTaskToConsoleHistory.
	if role == "research" {
		return
	}

	// If payload wasn't captured (consumed by --poll), try to recover it
	// from the session log via the delivery status.
	if payload == "" {
		tasks, err := bus.ListTasks(session, bus.TaskCompleted)
		if err == nil {
			for _, t := range tasks {
				if t.To == role && t.ResponseID != "" {
					if msg, ok := bus.FindMessageByID(session, t.ResponseID); ok {
						payload = msg.Payload
					}
					break
				}
			}
		}
	}
	if payload == "" {
		return
	}

	// Build summary from payload — first line or truncated
	summary := action
	if len(payload) > 200 {
		if idx := strings.Index(payload, "\n"); idx > 0 && idx < 200 {
			summary = payload[:idx]
		} else {
			summary = payload[:200] + "..."
		}
	} else {
		summary = payload
	}

	// Determine exit code heuristically from the action and payload
	exitCode := "0"
	if action == "error" || strings.Contains(strings.ToLower(payload), "failed") ||
		strings.Contains(strings.ToLower(payload), "error:") {
		exitCode = "1"
	}

	outcome := "success"
	if exitCode != "0" {
		outcome = "failure"
	}

	entry := bus.HookHistoryEntry{
		TS:       time.Now().Unix(),
		Command:  action,
		ExitCode: exitCode,
		Outcome:  outcome,
		Output:   payload,
		Summary:  summary,
	}

	historyPath := bus.HistoryPath(session, role)
	_ = bus.WriteHookHistory(historyPath, entry, 100)
}

// isCommitAction returns true for actions that trigger actual git commits.
// Read-only operations (status, log, diff, pr-read) are not blocked.
func isCommitAction(action string) bool {
	switch action {
	case "commit", "stage", "push", "merge", "rebase", "tag":
		return true
	}
	return false
}

// consumeExistingResponses checks if the sender's inbox already has unread
// responses from the target role matching the requested action. If so,
// consumes and prints them, returning true (caller should skip sending).
// This prevents redundant requests when hook-driven chains have already
// delivered results (e.g., build→test→review auto-chains).
//
// TOCTOU note: the peek→consume sequence is not atomic. If another process
// consumes the response between peek and ReceiveFromFunc, the function
// returns false and falls through to normal send — a benign race.
func consumeExistingResponses(session, from, to, action string) bool {
	msgs, err := bus.Peek(session, from)
	if err != nil || len(msgs) == 0 {
		return false
	}

	// Also accept responses from the host agent for hosted roles
	host := bus.WindowForRole(to)
	hasMatch := false
	for _, m := range msgs {
		if m.Type != "response" {
			continue
		}
		if m.From != to && m.From != host {
			continue
		}
		// Only match responses with the same action as the request
		if m.Action == action {
			hasMatch = true
			break
		}
	}

	if !hasMatch {
		return false
	}

	// Consume matching responses from inbox
	acceptFrom := func(sender string) bool {
		return sender == to || sender == host
	}
	consumed, err := bus.ReceiveFromFunc(session, from, acceptFrom)
	if err != nil || len(consumed) == 0 {
		return false
	}

	// Separate matching responses from non-matching ones
	var matching, other []bus.Message
	for _, m := range consumed {
		if m.Type == "response" && m.Action == action {
			matching = append(matching, m)
		} else {
			other = append(other, m)
		}
	}

	// Put non-matching messages back in the inbox (direct write, no Send
	// to avoid reordering, duplicate delivery tracking, or notify retrigger)
	for _, m := range other {
		_ = bus.AppendToInbox(session, from, m)
	}

	fmt.Printf("Found %d existing %s response(s) from %s in inbox — skipping send:\n", len(matching), action, to)
	for _, m := range matching {
		fmt.Println()
		fmt.Print(bus.FormatMessage(m))
	}
	fmt.Println()
	return true
}

// validatePayload returns warning strings for payload issues.
func validatePayload(payload string) []string {
	var warnings []string
	if strings.Contains(payload, "\n") {
		warnings = append(warnings, "payload contains newlines — this may break allowedTools glob matching")
	}
	if len(payload) > 500 {
		warnings = append(warnings, fmt.Sprintf("payload is %d chars (>500) — consider using shorter messages", len(payload)))
	}
	return warnings
}
