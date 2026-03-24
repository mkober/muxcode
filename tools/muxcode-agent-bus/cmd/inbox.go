package cmd

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/mkober/muxcode/tools/muxcode-agent-bus/bus"
)

// Inbox handles the "muxcode-agent-bus inbox" subcommand.
// Usage: muxcode-agent-bus inbox [--peek] [--raw] [--role ROLE] [--wait [TIMEOUT]] [--from ROLE]
func Inbox(args []string) {
	fs := flag.NewFlagSet("inbox", flag.ExitOnError)
	peek := fs.Bool("peek", false, "read without consuming messages")
	raw := fs.Bool("raw", false, "output raw JSONL")
	role := fs.String("role", "", "override role (default: auto-detect)")
	wait := fs.Int("wait", 0, "poll until messages arrive (timeout in seconds, default 600 if flag given)")
	from := fs.String("from", "", "only consume messages from this role (leave others in inbox)")
	fs.Parse(args)

	session := bus.BusSession()
	r := *role
	if r == "" {
		r = bus.BusRole()
	}

	// --wait with no value gets parsed as 0; treat as default 600s.
	// Distinguish "not given" (0) from "given without value" by checking args.
	waitTimeout := *wait
	if waitTimeout == 0 {
		// Check if --wait was explicitly passed (with or without a value)
		for _, a := range args {
			if a == "--wait" {
				waitTimeout = 600
				break
			}
		}
	}

	if waitTimeout > 0 {
		inboxWait(session, r, *from, *raw, waitTimeout)
		return
	}

	msgs := readInbox(session, r, *from, *peek)

	if len(msgs) == 0 {
		return
	}

	printMessages(msgs, *raw)
}

// readInbox reads messages from the inbox, optionally filtering by sender.
func readInbox(session, role, from string, peek bool) []bus.Message {
	var msgs []bus.Message
	var err error

	if from != "" && !peek {
		// Filter by sender — only consume messages from the specified role
		host := bus.WindowForRole(from)
		acceptFrom := func(f string) bool {
			return f == from || f == host
		}
		msgs, err = bus.ReceiveFromFunc(session, role, acceptFrom)
	} else if peek {
		msgs, err = bus.Peek(session, role)
		// If --from specified with --peek, filter in-memory
		if from != "" && len(msgs) > 0 {
			host := bus.WindowForRole(from)
			var filtered []bus.Message
			for _, m := range msgs {
				if m.From == from || m.From == host {
					filtered = append(filtered, m)
				}
			}
			msgs = filtered
		}
	} else {
		msgs, err = bus.Receive(session, role)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading inbox: %v\n", err)
		os.Exit(1)
	}

	return msgs
}

// inboxWait polls the inbox until messages arrive or timeout is reached.
func inboxWait(session, role, from string, raw bool, timeout int) {
	// Set waiting marker so Notify() skips send-keys for our role
	bus.SetWaiting(session, role)
	defer bus.ClearWaiting(session, role)

	const pollInterval = 2 // seconds
	for elapsed := 0; elapsed < timeout; elapsed += pollInterval {
		time.Sleep(time.Duration(pollInterval) * time.Second)

		if !bus.HasMessages(session, role) {
			continue
		}

		msgs := readInbox(session, role, from, false)
		if len(msgs) == 0 {
			continue
		}

		printMessages(msgs, raw)
		return
	}

	fmt.Fprintf(os.Stderr, "No messages within %ds\n", timeout)
}

// printMessages formats and prints messages to stdout.
func printMessages(msgs []bus.Message, raw bool) {
	for _, m := range msgs {
		if raw {
			data, err := bus.EncodeMessage(m)
			if err != nil {
				continue
			}
			fmt.Println(string(data))
		} else {
			fmt.Print(bus.FormatMessage(m))
			fmt.Println()
		}
	}
}
