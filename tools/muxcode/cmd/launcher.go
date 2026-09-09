package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mkober/muxcode/tools/muxcode/bus"
	"github.com/mkober/muxcode/tools/muxcode/tui"
)

// RunLauncher handles the "muxcode launch" subcommand (or bare "muxcode" invocation).
// Usage:
//
//	muxcode                          # interactive project picker
//	muxcode <path>                   # launch with project path
//	muxcode <path> <name>            # launch with project path and session name
//	muxcode launch                   # explicit subcommand
//	muxcode launch <path> [<name>]   # explicit with args
//	muxcode launch --auto-accept <session> <win1> <win2> ...  # internal: dismiss startup prompts
func RunLauncher(args []string) {
	// Handle internal flags (launched as detached processes by LaunchSession)
	if len(args) >= 2 && args[0] == "--auto-accept" {
		session := args[1]
		windows := args[2:]
		bus.AutoAccept(session, windows)
		return
	}
	if len(args) >= 1 && args[0] == "--resize" {
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: muxcode launch --resize <session>")
			os.Exit(1)
		}
		bus.ResizeWindows(args[1])
		return
	}

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

	fmt.Print(launchBanner(projectDir, sessionName, tui.TermWidth()))

	// Attach to existing session if already running
	if bus.TmuxHasSession(sessionName) {
		fmt.Printf("  %sSession already running — attaching...%s\n", tui.Yellow, tui.RST)
		fmt.Println()
		if err := bus.AttachToSession(sessionName); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if err := bus.LaunchSession(cfg, projectDir, sessionName); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// launchBanner renders the project/session header in the palette the
// provider and reload modals use: a bold purple label with the value in
// foreground, two-space indent, blank lines above and below.
//
// Values are fitted to width because this renders inside the New Session
// display-popup, whose width comes from the popup config rather than the
// content — an unclamped absolute project path wrapped outside the popup
// border. Width must come from tui.TermWidth (stty), not tput, which in a
// popup reports the session's stale COLUMNS.
func launchBanner(projectDir, session string, width int) string {
	rows := []struct{ label, value string }{
		{"Project:", abbrevHome(projectDir)},
		{"Session:", session},
	}
	avail := width - len("  Project:  ")
	if avail < 8 {
		avail = 8
	}
	var b strings.Builder
	b.WriteString("\n")
	for _, r := range rows {
		fmt.Fprintf(&b, "  %s%s%s%s  %s%s%s\n",
			tui.Bold, tui.Purple, r.label, tui.RST,
			tui.FG, fitPath(r.value, avail), tui.RST)
	}
	b.WriteString("\n")
	return b.String()
}

// fitPath shortens a value to w columns from the left, because the tail
// of a project path is the part that identifies it — truncating the
// right drops the project name and keeps only "/Users/...".
func fitPath(s string, w int) string {
	r := []rune(s)
	if len(r) <= w || w < 2 {
		return s
	}
	return "…" + string(r[len(r)-(w-1):])
}

// abbrevHome renders a path under the user's home as ~, the form the
// popup has room for.
//
// The prefix must end on a separator: a bare string prefix also matches
// a sibling directory, rendering /Users/alice-backup/proj as
// ~-backup/proj for a user whose home is /Users/alice.
func abbrevHome(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if p == home {
		return "~"
	}
	prefix := strings.TrimSuffix(home, string(filepath.Separator)) + string(filepath.Separator)
	if rest := strings.TrimPrefix(p, prefix); rest != p {
		return "~" + string(filepath.Separator) + rest
	}
	return p
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
