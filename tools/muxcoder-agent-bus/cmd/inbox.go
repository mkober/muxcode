package cmd

import (
	"flag"
	"fmt"
	"os"

	"github.com/mkober/muxcoder/tools/muxcoder-agent-bus/bus"
)

// Inbox handles the "muxcoder-agent-bus inbox" subcommand.
func Inbox(args []string) {
	fs := flag.NewFlagSet("inbox", flag.ExitOnError)
	peek := fs.Bool("peek", false, "read without consuming messages")
	raw := fs.Bool("raw", false, "output raw JSONL")
	role := fs.String("role", "", "override role (default: auto-detect)")
	fs.Parse(args)

	session := bus.BusSession()
	r := *role
	if r == "" {
		r = bus.BusRole()
	}

	var msgs []bus.Message
	var err error

	if *peek {
		msgs, err = bus.Peek(session, r)
	} else {
		msgs, err = bus.Receive(session, r)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading inbox: %v\n", err)
		os.Exit(1)
	}

	if len(msgs) == 0 {
		return
	}

	for _, m := range msgs {
		if *raw {
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
