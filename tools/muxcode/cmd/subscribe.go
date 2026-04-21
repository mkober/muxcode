package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// Subscribe handles the "muxcode subscribe" subcommand.
func Subscribe(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "Usage: muxcode subscribe <add|list|remove|enable|disable> [args...]\n")
		os.Exit(1)
	}

	subcmd := args[0]
	subArgs := args[1:]

	switch subcmd {
	case "add":
		subscribeAdd(subArgs)
	case "list":
		subscribeList(subArgs)
	case "remove":
		subscribeRemove(subArgs)
	case "enable":
		subscribeEnable(subArgs)
	case "disable":
		subscribeDisable(subArgs)
	default:
		fmt.Fprintf(os.Stderr, "Unknown subscribe subcommand: %s\n", subcmd)
		fmt.Fprintf(os.Stderr, "Usage: muxcode subscribe <add|list|remove|enable|disable> [args...]\n")
		os.Exit(1)
	}
}

// subscribeAdd handles: subscribe add <event> <outcome> <notify> [--conditions JSON] [message...]
func subscribeAdd(args []string) {
	if len(args) < 3 {
		fmt.Fprintf(os.Stderr, "Usage: muxcode subscribe add <event> <outcome> <notify> [--conditions JSON] [message]\n")
		fmt.Fprintf(os.Stderr, "  event:      build, test, deploy, or * (all)\n")
		fmt.Fprintf(os.Stderr, "  outcome:    success, failure, or * (any)\n")
		fmt.Fprintf(os.Stderr, "  notify:     agent role to notify\n")
		fmt.Fprintf(os.Stderr, "  conditions: JSON object (e.g. '{\"files_match\":\"lib/**/*.ts\"}')\n")
		fmt.Fprintf(os.Stderr, "  message:    template (supports ${event}, ${outcome}, ${exit_code}, ${command}, ${branch}, ${changed_files})\n")
		os.Exit(1)
	}

	event := args[0]
	outcome := args[1]
	notify := args[2]
	remaining := args[3:]

	var conditions map[string]any
	var messageParts []string

	for i := 0; i < len(remaining); i++ {
		if remaining[i] == "--conditions" {
			if i+1 >= len(remaining) {
				fmt.Fprintf(os.Stderr, "Error: --conditions requires a JSON value\n")
				os.Exit(1)
			}
			i++
			if err := json.Unmarshal([]byte(remaining[i]), &conditions); err != nil {
				fmt.Fprintf(os.Stderr, "Error: invalid conditions JSON: %v\n", err)
				os.Exit(1)
			}
		} else {
			messageParts = append(messageParts, remaining[i])
		}
	}

	message := strings.Join(messageParts, " ")

	session := bus.BusSession()

	entry, err := bus.AddSubscription(session, bus.Subscription{
		Event:      event,
		Outcome:    outcome,
		Notify:     notify,
		Message:    message,
		Conditions: conditions,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error adding subscription: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Added subscription: %s\n", entry.ID)
	fmt.Printf("  Event: %s  Outcome: %s  Notify: %s\n", entry.Event, entry.Outcome, entry.Notify)
	if len(entry.Conditions) > 0 {
		condJSON, _ := json.Marshal(entry.Conditions)
		fmt.Printf("  Conditions: %s\n", condJSON)
	}
	fmt.Printf("  Message: %s\n", entry.Message)
}

// subscribeList handles: subscribe list [--all]
func subscribeList(args []string) {
	showAll := false
	for _, arg := range args {
		switch arg {
		case "--all":
			showAll = true
		default:
			fmt.Fprintf(os.Stderr, "Unknown flag: %s\n", arg)
			fmt.Fprintf(os.Stderr, "Usage: muxcode subscribe list [--all]\n")
			os.Exit(1)
		}
	}

	session := bus.BusSession()
	entries, err := bus.ReadSubscriptions(session)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading subscriptions: %v\n", err)
		os.Exit(1)
	}

	fmt.Print(bus.FormatSubscriptionList(entries, showAll))
}

// subscribeRemove handles: subscribe remove <id>
func subscribeRemove(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "Usage: muxcode subscribe remove <id>\n")
		os.Exit(1)
	}

	session := bus.BusSession()
	if err := bus.RemoveSubscription(session, args[0]); err != nil {
		fmt.Fprintf(os.Stderr, "Error removing subscription: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Removed subscription: %s\n", args[0])
}

// subscribeEnable handles: subscribe enable <id>
func subscribeEnable(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "Usage: muxcode subscribe enable <id>\n")
		os.Exit(1)
	}

	session := bus.BusSession()
	if err := bus.SetSubscriptionEnabled(session, args[0], true); err != nil {
		fmt.Fprintf(os.Stderr, "Error enabling subscription: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Enabled subscription: %s\n", args[0])
}

// subscribeDisable handles: subscribe disable <id>
func subscribeDisable(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "Usage: muxcode subscribe disable <id>\n")
		os.Exit(1)
	}

	session := bus.BusSession()
	if err := bus.SetSubscriptionEnabled(session, args[0], false); err != nil {
		fmt.Fprintf(os.Stderr, "Error disabling subscription: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Disabled subscription: %s\n", args[0])
}
