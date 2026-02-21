package cmd

import (
	"flag"
	"fmt"
	"os"

	"github.com/mkober/muxcode/tools/muxcode-agent-bus/bus"
)

// Init handles the "muxcode-agent-bus init" subcommand.
func Init(args []string) {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	memoryDir := fs.String("memory-dir", "", "override memory directory path")
	fs.Parse(args)

	session := bus.BusSession()
	if err := bus.Init(session, *memoryDir); err != nil {
		fmt.Fprintf(os.Stderr, "Error initializing bus: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Bus initialized: %s\n", bus.BusDir(session))
}
