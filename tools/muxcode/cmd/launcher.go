package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// RunLauncher handles the "muxcode launch" subcommand (or bare "muxcode" invocation).
// Usage:
//
//	muxcode                          # interactive project picker
//	muxcode <path>                   # launch with project path
//	muxcode <path> <name>            # launch with project path and session name
//	muxcode launch                   # explicit subcommand
//	muxcode launch <path> [<name>]   # explicit with args
func RunLauncher(args []string) {
	// Check tmux dependency
	if _, err := exec.LookPath("tmux"); err != nil {
		fmt.Fprintln(os.Stderr, "Error: tmux is required")
		os.Exit(1)
	}

	// Load config (must happen before LoadLauncherConfig reads env)
	// Use empty projectDir — we don't know it yet; resolution uses cwd
	bus.LoadShellConfig("")

	cfg := bus.LoadLauncherConfig()
	bus.SetupPath()

	// Resolve project directory
	var projectDir string
	if len(args) >= 1 {
		// Explicit path argument
		absDir, err := filepath.Abs(args[0])
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		if info, err := os.Stat(absDir); err != nil || !info.IsDir() {
			fmt.Fprintf(os.Stderr, "Error: not a directory: %s\n", absDir)
			os.Exit(1)
		}
		projectDir = absDir
	} else {
		// Interactive project picker
		projectDir = pickProject(cfg)
		if projectDir == "" {
			os.Exit(0) // user cancelled
		}
	}

	// Resolve session name
	sessionName := filepath.Base(projectDir)
	if len(args) >= 2 {
		sessionName = args[1]
	}

	// Change to project directory (agents resolve paths relative to it)
	os.Chdir(projectDir)

	// Re-load config from project dir (may have .muxcode/config)
	bus.LoadShellConfig(projectDir)

	fmt.Println()
	fmt.Printf("  Project:  %s\n", projectDir)
	fmt.Printf("  Session:  %s\n", sessionName)
	fmt.Println()

	if err := bus.LaunchSession(cfg, projectDir, sessionName); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// pickProject runs the interactive project picker using fzf.
func pickProject(cfg *bus.LauncherConfig) string {
	projects := bus.ScanProjects(cfg.ProjectsDir, cfg.ScanDepth)
	if len(projects) == 0 {
		fmt.Fprintf(os.Stderr, "No git projects found in %s\n", cfg.ProjectsDir)
		os.Exit(1)
	}

	selected, err := bus.PickProject(projects)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	return selected
}
