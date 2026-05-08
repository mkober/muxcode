package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// Simulate handles the "muxcode simulate" subcommand.
func Simulate(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "Usage: muxcode simulate <notify-flood|stuck-agent> [flags]\n")
		os.Exit(1)
	}

	subcmd := args[0]
	subArgs := args[1:]

	switch subcmd {
	case "notify-flood":
		simulateNotifyFlood(subArgs)
	case "stuck-agent":
		simulateStuckAgent(subArgs)
	default:
		fmt.Fprintf(os.Stderr, "Unknown simulate subcommand: %s\n", subcmd)
		fmt.Fprintf(os.Stderr, "Usage: muxcode simulate <notify-flood|stuck-agent> [flags]\n")
		os.Exit(1)
	}
}

// simulateNotifyFlood sends N messages to a target role from various sources,
// captures the pane before and after, counts send-keys injections, and reports.
//
// Usage: muxcode simulate notify-flood [--target ROLE] [--count N] [--interval MS] [--sources s1,s2,...]
func simulateNotifyFlood(args []string) {
	target := "edit"
	count := 20
	intervalMs := 1500
	sources := []string{"build", "test", "review", "deploy"}

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--target":
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "Error: --target requires a value\n")
				os.Exit(1)
			}
			i++
			target = args[i]
		case "--count":
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "Error: --count requires a value\n")
				os.Exit(1)
			}
			i++
			v, err := strconv.Atoi(args[i])
			if err != nil || v < 1 {
				fmt.Fprintf(os.Stderr, "Error: --count must be a positive integer\n")
				os.Exit(1)
			}
			count = v
		case "--interval":
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "Error: --interval requires a value (milliseconds)\n")
				os.Exit(1)
			}
			i++
			v, err := strconv.Atoi(args[i])
			if err != nil || v < 0 {
				fmt.Fprintf(os.Stderr, "Error: --interval must be a non-negative integer (ms)\n")
				os.Exit(1)
			}
			intervalMs = v
		case "--sources":
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "Error: --sources requires a comma-separated list\n")
				os.Exit(1)
			}
			i++
			sources = strings.Split(args[i], ",")
		default:
			fmt.Fprintf(os.Stderr, "Unknown flag: %s\n", args[i])
			os.Exit(1)
		}
	}

	session := bus.BusSession()

	fmt.Printf("=== Notify Flood Simulation ===\n")
	fmt.Printf("Session:  %s\n", session)
	fmt.Printf("Target:   %s\n", target)
	fmt.Printf("Messages: %d\n", count)
	fmt.Printf("Interval: %dms\n", intervalMs)
	fmt.Printf("Sources:  %s\n", strings.Join(sources, ", "))
	fmt.Println()

	// Clear any existing notification state for clean measurement
	bus.ClearNotifiedIDs(session, target)

	// Capture pane content before flood
	paneBefore := capturePaneContent(session, target)

	// Send N messages from rotating sources
	fmt.Printf("Sending %d messages...\n", count)
	for i := 0; i < count; i++ {
		source := sources[i%len(sources)]
		payload := fmt.Sprintf("Flood message %d/%d from %s (simulation)", i+1, count, source)
		msg := bus.NewMessage(source, target, "request", "response", payload, "")
		if err := bus.Send(session, msg); err != nil {
			fmt.Fprintf(os.Stderr, "  Error sending message %d: %v\n", i+1, err)
		}
		if i < count-1 && intervalMs > 0 {
			time.Sleep(time.Duration(intervalMs) * time.Millisecond)
		}
	}

	fmt.Println("All messages sent.")

	// Wait a moment for daemon to process
	time.Sleep(3 * time.Second)

	// Capture pane content after flood
	paneAfter := capturePaneContent(session, target)

	// Count injections (new lines in the pane containing notification text)
	injections := countInjections(paneBefore, paneAfter)

	// Check inbox
	msgs, _ := bus.Peek(session, target)
	inboxCount := 0
	for _, m := range msgs {
		if strings.Contains(m.Payload, "(simulation)") {
			inboxCount++
		}
	}

	// Check unnotified messages
	unnotified := bus.UnnotifiedMessages(session, target)

	// Report results
	fmt.Println()
	fmt.Printf("=== Results ===\n")
	fmt.Printf("Messages sent:       %d\n", count)
	fmt.Printf("Messages in inbox:   %d/%d\n", inboxCount, count)
	fmt.Printf("Injections observed: %d\n", injections)
	fmt.Printf("Unnotified pending:  %d\n", len(unnotified))
	fmt.Println()

	// Assess
	if injections <= 2 {
		fmt.Printf("✓ PASS: %d injection(s) for %d messages (expected ≤2)\n", injections, count)
	} else {
		fmt.Printf("✗ FAIL: %d injections for %d messages (expected ≤2)\n", injections, count)
	}
	if inboxCount == count {
		fmt.Printf("✓ PASS: All %d messages present in inbox\n", count)
	} else {
		fmt.Printf("✗ FAIL: Only %d/%d messages in inbox\n", inboxCount, count)
	}
}

// simulateStuckAgent blocks an agent pane with sleep, sends messages during the
// block, unblocks, and verifies combined notification delivery.
//
// Usage: muxcode simulate stuck-agent [--target ROLE] [--block-duration SECS] [--message-interval MS] [--message-count N]
func simulateStuckAgent(args []string) {
	target := "deploy"
	blockDuration := 30
	messageIntervalMs := 5000
	messageCount := 12

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--target":
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "Error: --target requires a value\n")
				os.Exit(1)
			}
			i++
			target = args[i]
		case "--block-duration":
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "Error: --block-duration requires a value (seconds)\n")
				os.Exit(1)
			}
			i++
			v, err := strconv.Atoi(args[i])
			if err != nil || v < 1 {
				fmt.Fprintf(os.Stderr, "Error: --block-duration must be a positive integer\n")
				os.Exit(1)
			}
			blockDuration = v
		case "--message-interval":
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "Error: --message-interval requires a value (milliseconds)\n")
				os.Exit(1)
			}
			i++
			v, err := strconv.Atoi(args[i])
			if err != nil || v < 0 {
				fmt.Fprintf(os.Stderr, "Error: --message-interval must be a non-negative integer (ms)\n")
				os.Exit(1)
			}
			messageIntervalMs = v
		case "--message-count":
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "Error: --message-count requires a value\n")
				os.Exit(1)
			}
			i++
			v, err := strconv.Atoi(args[i])
			if err != nil || v < 1 {
				fmt.Fprintf(os.Stderr, "Error: --message-count must be a positive integer\n")
				os.Exit(1)
			}
			messageCount = v
		default:
			fmt.Fprintf(os.Stderr, "Unknown flag: %s\n", args[i])
			os.Exit(1)
		}
	}

	session := bus.BusSession()

	fmt.Printf("=== Stuck Agent Simulation ===\n")
	fmt.Printf("Session:          %s\n", session)
	fmt.Printf("Target:           %s\n", target)
	fmt.Printf("Block duration:   %ds\n", blockDuration)
	fmt.Printf("Message interval: %dms\n", messageIntervalMs)
	fmt.Printf("Message count:    %d\n", messageCount)
	fmt.Println()

	// Clear any existing notification state
	bus.ClearNotifiedIDs(session, target)

	// Capture pane before blocking
	paneBefore := capturePaneContent(session, target)

	// Block the agent by sending a sleep command via send-keys
	paneTarget := bus.PaneTarget(session, target)
	fmt.Printf("Blocking agent pane with sleep %d...\n", blockDuration)
	cmd := exec.Command("tmux", "send-keys", "-t", paneTarget, fmt.Sprintf("sleep %d &", blockDuration), "Enter")
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not block agent pane: %v\n", err)
		fmt.Fprintf(os.Stderr, "Continuing without blocking — results may not match expectations.\n\n")
	}

	// Wait a moment for the sleep to start
	time.Sleep(1 * time.Second)

	// Capture pane after blocking (baseline for injection counting)
	paneBlocked := capturePaneContent(session, target)

	// Send messages during the block period
	fmt.Printf("Sending %d messages during block...\n", messageCount)
	sources := []string{"build", "test", "review", "deploy"}
	for i := 0; i < messageCount; i++ {
		source := sources[i%len(sources)]
		payload := fmt.Sprintf("Stuck-agent message %d/%d from %s (simulation)", i+1, messageCount, source)
		msg := bus.NewMessage(source, target, "request", "response", payload, "")
		if err := bus.Send(session, msg); err != nil {
			fmt.Fprintf(os.Stderr, "  Error sending message %d: %v\n", i+1, err)
		}
		if i < messageCount-1 && messageIntervalMs > 0 {
			time.Sleep(time.Duration(messageIntervalMs) * time.Millisecond)
		}
	}

	// Count injections during block
	paneDuringBlock := capturePaneContent(session, target)
	injectionsDuringBlock := countInjections(paneBlocked, paneDuringBlock)

	fmt.Printf("Injections during block: %d\n", injectionsDuringBlock)

	// Wait for block to expire (remaining time)
	elapsed := time.Duration(messageCount) * time.Duration(messageIntervalMs) * time.Millisecond
	remaining := time.Duration(blockDuration)*time.Second - elapsed - 1*time.Second
	if remaining > 0 {
		fmt.Printf("Waiting %v for block to expire...\n", remaining.Round(time.Second))
		time.Sleep(remaining)
	}

	// Wait for daemon to detect idle transition
	fmt.Printf("Waiting for idle transition detection (up to 15s)...\n")
	time.Sleep(15 * time.Second)

	// Capture pane after unblock
	paneAfterUnblock := capturePaneContent(session, target)
	injectionsAfterUnblock := countInjections(paneBefore, paneAfterUnblock)

	// Check inbox
	msgs, _ := bus.Peek(session, target)
	inboxCount := 0
	for _, m := range msgs {
		if strings.Contains(m.Payload, "(simulation)") {
			inboxCount++
		}
	}

	// Check unnotified messages
	unnotified := bus.UnnotifiedMessages(session, target)

	// Report results
	fmt.Println()
	fmt.Printf("=== Results ===\n")
	fmt.Printf("Agent blocked for:       %ds\n", blockDuration)
	fmt.Printf("Messages sent:           %d\n", messageCount)
	fmt.Printf("Messages in inbox:       %d/%d\n", inboxCount, messageCount)
	fmt.Printf("Injections during block: %d\n", injectionsDuringBlock)
	fmt.Printf("Total injections:        %d\n", injectionsAfterUnblock)
	fmt.Printf("Unnotified pending:      %d\n", len(unnotified))
	fmt.Println()

	// Assess
	if injectionsDuringBlock == 0 {
		fmt.Printf("✓ PASS: Zero injections during block\n")
	} else {
		fmt.Printf("✗ FAIL: %d injection(s) during block (expected 0)\n", injectionsDuringBlock)
	}
	if injectionsAfterUnblock <= 2 {
		fmt.Printf("✓ PASS: %d total injection(s) after unblock (expected ≤2)\n", injectionsAfterUnblock)
	} else {
		fmt.Printf("✗ FAIL: %d total injections after unblock (expected ≤2)\n", injectionsAfterUnblock)
	}
	if inboxCount == messageCount {
		fmt.Printf("✓ PASS: All %d messages present in inbox\n", messageCount)
	} else {
		fmt.Printf("✗ FAIL: Only %d/%d messages in inbox\n", inboxCount, messageCount)
	}
}

// capturePaneContent captures the current visible content of a tmux pane.
// Returns the captured text. Empty string on error.
func capturePaneContent(session, role string) string {
	target := bus.PaneTarget(session, role)
	out, err := exec.Command("tmux", "capture-pane", "-t", target, "-p").Output()
	if err != nil {
		return ""
	}
	return string(out)
}

// countInjections counts the number of notification injection lines that
// appeared between two pane captures. Looks for notification patterns:
// - "You have N new messages:"
// - "New message from"
// - "You have new messages"
func countInjections(before, after string) int {
	// Get lines that are in 'after' but not in 'before'
	beforeLines := make(map[string]int)
	for _, line := range strings.Split(before, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			beforeLines[line]++
		}
	}

	count := 0
	for _, line := range strings.Split(after, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if isNotificationLine(line) {
			// Check if this line existed in the 'before' capture
			if beforeLines[line] > 0 {
				beforeLines[line]--
			} else {
				count++
			}
		}
	}
	return count
}

// isNotificationLine returns true if a pane line looks like a notification injection.
func isNotificationLine(line string) bool {
	// Strip the prompt character if present
	line = strings.TrimPrefix(line, "❯ ")
	line = strings.TrimPrefix(line, "❯ ")

	return strings.HasPrefix(line, "You have ") ||
		strings.HasPrefix(line, "New message from ") ||
		strings.Contains(line, "new messages:")
}
