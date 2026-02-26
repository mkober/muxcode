package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/mkober/muxcode/tools/muxcode-agent-bus/bus"
)

// Chain handles the "muxcode-agent-bus chain" subcommand.
// Usage: muxcode-agent-bus chain <event_type> <outcome> [--exit-code N] [--command CMD] [--no-notify] [--dry-run]
// Exit codes: 0 = sent, 1 = error, 2 = no chain configured
func Chain(args []string) {
	if len(args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: muxcode-agent-bus chain <event_type> <outcome> [--exit-code N] [--command CMD] [--no-notify] [--dry-run]\n")
		os.Exit(1)
	}

	eventType := args[0]
	outcome := args[1]
	remaining := args[2:]

	exitCode := ""
	command := ""
	noNotify := false
	dryRun := false

	for i := 0; i < len(remaining); i++ {
		switch remaining[i] {
		case "--exit-code":
			if i+1 >= len(remaining) {
				fmt.Fprintf(os.Stderr, "Error: --exit-code requires a value\n")
				os.Exit(1)
			}
			i++
			exitCode = remaining[i]
		case "--command":
			if i+1 >= len(remaining) {
				fmt.Fprintf(os.Stderr, "Error: --command requires a value\n")
				os.Exit(1)
			}
			i++
			command = remaining[i]
		case "--no-notify":
			noNotify = true
		case "--dry-run":
			dryRun = true
		default:
			fmt.Fprintf(os.Stderr, "Unknown flag: %s\n", remaining[i])
			os.Exit(1)
		}
	}

	// Look up chain action
	action := bus.ResolveChain(eventType, outcome)
	if action == nil {
		os.Exit(2) // no chain configured
	}

	session := bus.BusSession()
	from := bus.BusRole()
	message := bus.ExpandMessage(action.Message, exitCode, command)

	if dryRun {
		fmt.Printf("chain: %s %s -> send %s:%s to %s: %s\n",
			eventType, outcome, action.Type, action.Action, action.SendTo, message)
		if bus.ChainNotifyAnalyst(eventType) && action.SendTo != "analyze" {
			fmt.Printf("chain: notify analyst: %s %s: %s\n", eventType, outcome, command)
		}
		// Show subscription fan-out in dry-run
		subs, _ := bus.ReadSubscriptions(session)
		matched := bus.MatchSubscriptions(subs, eventType, outcome)
		if len(matched) > 0 {
			fmt.Printf("chain: %d subscription(s) would fire:\n", len(matched))
			for _, s := range matched {
				payload := bus.ExpandSubscriptionMessage(s.Message, eventType, outcome, exitCode, command)
				fmt.Printf("  -> %s:%s to %s: %s\n", "event", s.Action, s.Notify, payload)
			}
		}
		return
	}

	// Send the chain message
	msg := bus.NewMessage(from, action.SendTo, action.Type, action.Action, message, "")
	if err := bus.Send(session, msg); err != nil {
		fmt.Fprintf(os.Stderr, "Error sending chain message: %v\n", err)
		os.Exit(1)
	}

	if !noNotify {
		_ = bus.Notify(session, action.SendTo)
		// Auto-CC notification for edit
		if bus.IsAutoCCRole(from) && action.SendTo != "edit" {
			_ = bus.Notify(session, "edit")
		}
	}

	fmt.Printf("Sent %s:%s to %s\n", action.Type, action.Action, action.SendTo)

	// Notify analyst if configured — skip when chain action already targets analyze
	if bus.ChainNotifyAnalyst(eventType) && action.SendTo != "analyze" {
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
			if err := bus.Send(session, aMsg); err != nil {
				fmt.Fprintf(os.Stderr, "warning: analyst notification failed: %v\n", err)
			}
			// No tmux notify for analyst events (--no-notify equivalent)
		}
	}

	// Fire event subscriptions (fan-out beyond primary chain target)
	if !noNotify {
		fired, err := bus.FireSubscriptions(session, from, eventType, outcome, exitCode, command)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: subscription fan-out error: %v\n", err)
		}
		if fired > 0 {
			fmt.Printf("Notified %d subscriber(s)\n", fired)
		}
	}
}

// capitalize returns the string with the first letter uppercased.
func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
