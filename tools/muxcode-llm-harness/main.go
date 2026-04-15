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
		fmt.Fprintf(os.Stderr, "Usage: muxcode-llm-harness run <role> [--model MODEL] [--url URL] [--max-turns N] [--tui]\n")
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
		case "--tui":
			cfg.TUI = true
		}
	}

	// Signal handling for clean shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	if cfg.TUI {
		// TUI mode: render loop on main goroutine, harness in background.
		// errCh communicates the harness exit status without a data race.
		tui := harness.NewTUISink(cfg.Role, cfg.OllamaModel)
		cfg.UserInput = tui.SubmitCh() // wire TUI input → harness loop
		errCh := make(chan error, 1)

		go func() {
			err := harness.Run(ctx, cfg, tui)
			if err != nil {
				tui.Emit(harness.Event{Kind: harness.EventError, Message: fmt.Sprintf("Fatal: %v", err)})
			}
			errCh <- err
			tui.Close()
		}()

		tui.RunLoop(ctx)

		// RunLoop exited — either ctx was cancelled (signal), harness
		// closed the TUI (fatal error), or user quit (Ctrl+C/Ctrl+D).
		// Cancel ctx so harness.Run stops and writes to errCh.
		cancel()

		if err := <-errCh; err != nil {
			os.Exit(1)
		}
	} else {
		// Headless mode: stderr logging (original behavior)
		if err := harness.Run(ctx, cfg, nil); err != nil {
			fmt.Fprintf(os.Stderr, "[harness] Error: %v\n", err)
			os.Exit(1)
		}
	}
}
