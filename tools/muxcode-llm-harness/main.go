package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"muxcode-llm-harness/harness"
)

func main() {
	if len(os.Args) < 3 || os.Args[1] != "run" {
		fmt.Fprintf(os.Stderr, "Usage: muxcode-llm-harness run <role> [--model MODEL] [--url URL] [--max-turns N]\n")
		os.Exit(1)
	}

	cfg := harness.DefaultConfig()
	cfg.Role = os.Args[2]

	// Apply per-role model override (MUXCODE_{ROLE}_MODEL → MUXCODE_OLLAMA_MODEL → default)
	cfg.OllamaModel = harness.RoleModel(cfg.Role)

	// Parse optional flags (--model overrides per-role and global env)
	args := os.Args[3:]
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--model":
			if i+1 < len(args) {
				cfg.OllamaModel = args[i+1]
				i++
			}
		case "--url":
			if i+1 < len(args) {
				cfg.OllamaURL = args[i+1]
				i++
			}
		case "--max-turns":
			if i+1 < len(args) {
				if n, err := strconv.Atoi(args[i+1]); err == nil {
					cfg.MaxTurns = n
				}
				i++
			}
		}
	}

	// Signal handling for clean shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Fprintf(os.Stderr, "[harness] Shutting down...\n")
		cancel()
	}()

	if err := harness.Run(ctx, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "[harness] Error: %v\n", err)
		os.Exit(1)
	}
}
