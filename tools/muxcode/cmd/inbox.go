package cmd

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// Inbox handles the "muxcode inbox" subcommand.
// Usage: muxcode inbox [--peek] [--raw] [--role ROLE] [--wait [TIMEOUT]] [--from ROLE] [--poll [TIMEOUT]] [--loop]
func Inbox(args []string) {
	// Pre-process args: inject default timeout (600) for --wait/--poll when
	// given without a value. Go's flag package requires Int flags to have an
	// argument, so bare "--poll" or "--wait" would fail with exit code 2.
	args = injectOptionalIntDefaults(args, map[string]string{
		"wait": "600",
		"poll": "600",
	})

	fs := flag.NewFlagSet("inbox", flag.ExitOnError)
	peek := fs.Bool("peek", false, "read without consuming messages")
	raw := fs.Bool("raw", false, "output raw JSONL")
	role := fs.String("role", "", "override role (default: auto-detect)")
	wait := fs.Int("wait", 0, "poll until messages arrive (timeout in seconds, default 600 if flag given)")
	poll := fs.Int("poll", 0, "watch trigger file for new messages (timeout in seconds, default 600 if flag given)")
	loop := fs.Bool("loop", false, "loop forever on --poll timeout instead of exiting (reduces restart noise)")
	from := fs.String("from", "", "only consume messages from this role (leave others in inbox)")
	fs.Parse(args)

	session := bus.BusSession()
	r := *role
	if r == "" {
		r = bus.BusRole()
	}

	waitTimeout := *wait
	pollTimeout := *poll

	if pollTimeout > 0 && waitTimeout > 0 {
		fmt.Fprintf(os.Stderr, "Error: --poll and --wait are mutually exclusive\n")
		os.Exit(1)
	}

	if pollTimeout > 0 {
		inboxPoll(session, r, *from, *raw, pollTimeout, *loop)
		return
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

// inboxPoll watches the trigger file for changes and reads inbox when notified.
// This replaces the send-keys wake-up mechanism — the agent starts this as a
// background Bash tool and it returns when messages arrive, which Claude Code
// sees as a tool result. No text injection into the pane, no TOCTOU race.
//
// The poll also checks the inbox directly on each tick to catch messages that
// arrived before the poll started or without a trigger file update (e.g. from
// hooks or direct Send() calls that predate the trigger mechanism).
func inboxPoll(session, role, from string, raw bool, timeout int, loop bool) {
	// Claim the polling marker so Notify() skips send-keys for our role.
	//
	// The claim is exclusive, and losing it is fatal: every tick below consumes
	// destructively (readInbox with peek=false -> bus.Receive) and writes an
	// "acked" receipt. A second loop on the same role would race the first for
	// each message, and whichever loop won would print it to a pipe that no
	// agent runtime is reading — the message is gone from the inbox, the
	// receipt tells the daemon it was delivered, and checkPollHealth's
	// receipt-gap backstop stays quiet. Exiting keeps the incumbent listener
	// the single consumer.
	if !bus.SetPolling(session, role) {
		fmt.Fprintf(os.Stderr,
			"An inbox listener is already running for %s — exiting rather than double-consuming\n", role)
		return
	}
	defer bus.ClearPolling(session, role)

	triggerPath := bus.TriggerNotifyPath(session, role)

	// Record initial trigger file mtime
	var lastMtime int64
	if info, err := os.Stat(triggerPath); err == nil {
		lastMtime = info.ModTime().UnixNano()
	}

	const pollInterval = 2 // seconds
	for {
		for elapsed := 0; elapsed < timeout; elapsed += pollInterval {
			time.Sleep(time.Duration(pollInterval) * time.Second)

			// Check trigger file for mtime change
			triggered := false
			if info, err := os.Stat(triggerPath); err == nil {
				mtime := info.ModTime().UnixNano()
				if mtime != lastMtime {
					lastMtime = mtime
					triggered = true
				}
			}

			// Also check inbox directly (catches pre-existing messages)
			if !triggered && !bus.HasMessages(session, role) {
				continue
			}

			msgs := readInbox(session, role, from, false)
			if len(msgs) == 0 {
				continue
			}

			printMessages(msgs, raw)
			return
		}

		// Timeout reached with no messages
		if !loop {
			fmt.Fprintf(os.Stderr, "No messages within %ds\n", timeout)
			return
		}
		// --loop: silently restart the poll cycle
	}
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

// injectOptionalIntDefaults pre-processes args so that Int flags can be used
// without a value (e.g. "--poll" becomes "--poll 600"). For each flag name in
// defaults, if the flag appears in args without a following integer value, the
// default is inserted. Handles both "-flag" and "--flag" forms, plus "="-style
// ("--flag=VAL" is left as-is since Go's flag package handles it natively).
func injectOptionalIntDefaults(args []string, defaults map[string]string) []string {
	out := make([]string, 0, len(args)+len(defaults))
	for i := 0; i < len(args); i++ {
		a := args[i]
		out = append(out, a)

		// Strip leading dashes to get the flag name
		name := strings.TrimLeft(a, "-")

		// Skip "="-style values (--poll=300) — already has a value
		if strings.Contains(name, "=") {
			continue
		}

		def, ok := defaults[name]
		if !ok {
			continue
		}

		// Check if the next arg is a valid integer (i.e. the value was provided)
		if i+1 < len(args) {
			if _, err := strconv.Atoi(args[i+1]); err == nil {
				continue // next arg is the value, leave it alone
			}
		}

		// No value follows — inject the default
		out = append(out, def)
	}
	return out
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
