package bus

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

// LauncherConfig holds configuration for launching a muxcode tmux session.
type LauncherConfig struct {
	ProjectsDir string            // MUXCODE_PROJECTS_DIR (default: $HOME)
	ScanDepth   int               // MUXCODE_SCAN_DEPTH (default: 3)
	Windows     []string          // MUXCODE_WINDOWS
	RoleMap     map[string]string // MUXCODE_ROLE_MAP (optional, e.g. custom=mycustomrole)
	SplitLeft   []string          // MUXCODE_SPLIT_LEFT
	ShellInit   string            // MUXCODE_SHELL_INIT
	Editor      string            // MUXCODE_EDITOR (default: nvim)
	NvimAppName string            // MUXCODE_NVIM_APPNAME (default: muxcode/nvim)
	AgentCLI    string            // MUXCODE_AGENT_CLI (default: claude)
}

// DefaultLauncherConfig returns a LauncherConfig with default values.
func DefaultLauncherConfig() *LauncherConfig {
	home, _ := os.UserHomeDir()
	return &LauncherConfig{
		ProjectsDir: home,
		ScanDepth:   3,
		Windows:     []string{"plan", "edit", "build", "test", "review", "deploy", "run", "watch", "commit", "analyze"},
		RoleMap:     map[string]string{},
		SplitLeft:   []string{"plan", "edit", "build", "test", "review", "deploy", "run", "analyze", "commit", "watch"},
		ShellInit:   "",
		Editor:      "nvim",
		NvimAppName: "muxcode/nvim",
		AgentCLI:    "claude",
	}
}

// LoadLauncherConfig loads configuration from environment, applying defaults.
func LoadLauncherConfig() *LauncherConfig {
	cfg := DefaultLauncherConfig()

	if v := os.Getenv("MUXCODE_PROJECTS_DIR"); v != "" {
		cfg.ProjectsDir = v
	}
	if v := os.Getenv("MUXCODE_SCAN_DEPTH"); v != "" {
		if n := parseInt(v, 3); n > 0 {
			cfg.ScanDepth = n
		}
	}
	if v := os.Getenv("MUXCODE_WINDOWS"); v != "" {
		cfg.Windows = strings.Fields(v)
	}
	if v := os.Getenv("MUXCODE_ROLE_MAP"); v != "" {
		cfg.RoleMap = ParseRoleMap(v)
	}
	if v := os.Getenv("MUXCODE_SPLIT_LEFT"); v != "" {
		cfg.SplitLeft = strings.Fields(v)
	}
	if v := os.Getenv("MUXCODE_SHELL_INIT"); v != "" {
		cfg.ShellInit = v
	}
	if v := os.Getenv("MUXCODE_EDITOR"); v != "" {
		cfg.Editor = v
	}
	if v := os.Getenv("MUXCODE_NVIM_APPNAME"); v != "" {
		cfg.NvimAppName = v
	}
	if v := os.Getenv("MUXCODE_AGENT_CLI"); v != "" {
		cfg.AgentCLI = v
	}

	return cfg
}

// ParseRoleMap parses a space-separated role map string like "run=runner commit=git analyze=analyst".
func ParseRoleMap(s string) map[string]string {
	m := make(map[string]string)
	for _, mapping := range strings.Fields(s) {
		parts := strings.SplitN(mapping, "=", 2)
		if len(parts) == 2 {
			m[parts[0]] = parts[1]
		}
	}
	return m
}

// AgentRole returns the agent role for a window name, using the role map.
func (c *LauncherConfig) AgentRole(window string) string {
	if role, ok := c.RoleMap[window]; ok {
		return role
	}
	return window
}

// IsSplitLeftWindow returns true if the window should have a split-left layout.
func (c *LauncherConfig) IsSplitLeftWindow(window string) bool {
	for _, w := range c.SplitLeft {
		if w == window {
			return true
		}
	}
	return false
}

// HasConsoleView returns true if the window has a built-in console view.
func HasConsoleView(window string) bool {
	switch window {
	case "build", "test", "review", "deploy", "run", "watch", "commit", "analyze", "api":
		return true
	}
	return false
}

// CapitalizeWindow capitalizes the first letter of a window name.
func CapitalizeWindow(name string) string {
	if name == "" {
		return ""
	}
	return strings.ToUpper(name[:1]) + name[1:]
}

// filterString returns a copy of the slice with all occurrences of val removed.
func filterString(slice []string, val string) []string {
	out := make([]string, 0, len(slice))
	for _, s := range slice {
		if s != val {
			out = append(out, s)
		}
	}
	return out
}

// parseInt parses a string to int with a fallback default.
func parseInt(s string, fallback int) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return fallback
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// SetupPath ensures local bins are in PATH (display-popup skips shell profile).
func SetupPath() {
	home, _ := os.UserHomeDir()
	localBin := filepath.Join(home, ".local", "bin")

	if runtime.GOOS == "darwin" {
		os.Setenv("PATH", "/opt/homebrew/bin:/opt/homebrew/sbin:"+localBin+":"+os.Getenv("PATH"))
	} else {
		os.Setenv("PATH", localBin+":"+os.Getenv("PATH"))
	}
}

// LaunchSession creates a muxcode tmux session with all configured windows.
func LaunchSession(cfg *LauncherConfig, projectDir, session string) error {
	// Set BUS_SESSION for all bus operations
	os.Setenv("BUS_SESSION", session)

	// Log session start
	LogLifecycle(session, "info", "launcher", "session-start",
		fmt.Sprintf("Project: %s", projectDir))

	// Kill existing session
	TmuxKillSession(session) // ignore error

	// Clear stale session-created hook
	TmuxUnsetGlobalHook("session-created") // ignore error

	// Clean up stale preview temp files
	os.Remove("/tmp/muxcode-preview-" + session + ".tmp")

	// Initialize bus
	if err := Init(session, ""); err != nil {
		return fmt.Errorf("bus init: %w", err)
	}
	LogLifecycle(session, "info", "launcher", "bus-init", "")

	// Kill stale daemon/monitor processes
	killStaleProcesses(session)

	// Start daemon and monitor as detached background processes
	daemonPID, err := startDetachedProcess("muxcode", "watch", session)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to start daemon: %v\n", err)
	} else {
		LogLifecycle(session, "info", "launcher", "daemon-start",
			fmt.Sprintf("PID: %d", daemonPID))
	}

	monitorPID, err := startDetachedProcess("muxcode", "watch", "--monitor", session)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to start monitor: %v\n", err)
	} else {
		LogLifecycle(session, "info", "launcher", "monitor-start",
			fmt.Sprintf("PID: %d", monitorPID))
	}

	// Ensure Ollama if needed
	EnsureOllama()

	// Capture client dimensions when inside tmux
	var clientW, clientH int
	if IsInsideTmux() {
		clientW, clientH, _ = TmuxClientDimensions()
	}

	// Create tmux session with first window
	firstWin := cfg.Windows[0]
	if err := TmuxNewSession(session, firstWin, projectDir, clientW, clientH); err != nil {
		return fmt.Errorf("new session: %w", err)
	}
	TmuxSetEnv(session, "BUS_SESSION", session)
	TmuxSetEnv(session, "MUXCODE", "1")

	LogLifecycle(session, "info", "launcher", "session-create",
		fmt.Sprintf("Windows: %s", strings.Join(cfg.Windows, " ")))

	// Create first window content
	agentLauncher := "muxcode agent launch"
	if err := createWindowContent(cfg, session, firstWin, projectDir, agentLauncher); err != nil {
		return fmt.Errorf("first window: %w", err)
	}

	// Create remaining windows
	for _, win := range cfg.Windows[1:] {
		if err := TmuxNewWindow(session, win, projectDir); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to create window %s: %v\n", win, err)
			continue
		}
		if err := createWindowContent(cfg, session, win, projectDir, agentLauncher); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to setup window %s: %v\n", win, err)
		}
	}

	// Select edit window and agent pane
	if err := TmuxSelectWindow(session, "edit"); err != nil {
		TmuxSelectWindow(session, firstWin) // fallback
	}
	TmuxSelectPane(session + ":edit.1") // ignore error

	fmt.Printf("  Session '%s' ready\n\n", session)

	// Configure status bar
	ConfigureStatusBar(session)

	// Register cleanup hook
	TmuxSetHook(session, "session-closed",
		fmt.Sprintf("run-shell 'muxcode cleanup %s'", session))

	// Start background window resize as detached process (survives syscall.Exec)
	startDetachedProcess("muxcode", "launch", "--resize", session)

	// Start auto-accept as a detached process (survives syscall.Exec)
	autoAcceptArgs := append([]string{"launch", "--auto-accept", session}, cfg.Windows...)
	autoAcceptPID, err := startDetachedProcess("muxcode", autoAcceptArgs...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to start auto-accept: %v\n", err)
	} else {
		LogLifecycle(session, "info", "launcher", "auto-accept-start",
			fmt.Sprintf("PID: %d", autoAcceptPID))
	}

	LogLifecycle(session, "info", "launcher", "session-ready", "")

	// Attach or switch to session
	return attachToSession(session)
}

// createWindowContent sets up the content (panes) for a window.
func createWindowContent(cfg *LauncherConfig, session, win, projectDir, agentLauncher string) error {
	target := session + ":" + win
	role := cfg.AgentRole(win)

	if win == "edit" {
		// Edit window: editor (left) + agent (right)
		sendInit(cfg, target)
		sendEditorCommand(cfg, target)
		TmuxSplitWindow(target, projectDir)
		sendInit(cfg, target+".1")
		sendCommand(target+".1", agentLauncher+" edit")
		TmuxSelectPane(target + ".0")
	} else if win == "plan" {
		// Plan window: Neovim with last-edited doc (left) + agent (right)
		sendInit(cfg, target)
		openedDoc := sendPlanEditorCommand(cfg, target, projectDir)
		TmuxSplitWindow(target, projectDir)
		sendInit(cfg, target+".1")
		sendCommand(target+".1", agentLauncher+" plan")
		TmuxSelectPane(target + ".0")

		// Send startup context message so agent reads the opened doc
		if openedDoc != "" {
			_ = SendNoCC(session, Message{
				ID:      NewMsgID("launcher"),
				TS:      time.Now().Unix(),
				From:    "launcher",
				To:      "plan",
				Type:    "request",
				Action:  "context",
				Payload: "Read " + openedDoc + " for initial context — this file is open in Neovim in the left pane.",
			})
		}
	} else if cfg.IsSplitLeftWindow(win) {
		// Split-left: console (left) + agent (right)
		sendInit(cfg, target)
		if HasConsoleView(win) {
			sendCommand(target, "muxcode console "+win)
		}
		TmuxSplitWindow(target, projectDir)
		sendInit(cfg, target+".1")
		sendCommand(target+".1", agentLauncher+" "+role)
		TmuxSelectPane(target + ".1")
	} else {
		// Standard: terminal (left) + agent (right)
		sendInit(cfg, target)
		TmuxSplitWindow(target, projectDir)
		sendInit(cfg, target+".1")
		sendCommand(target+".1", agentLauncher+" "+role)
		TmuxSelectPane(target + ".1")
	}

	// Set display name for the status bar label (used by #{@display-name} format).
	// Per-window user option — follows the window object across swap-window operations.
	TmuxSetWindowOption(target, "@display-name", CapitalizeWindow(win))

	return nil
}

// sendInit sends the shell init command to a pane if configured.
func sendInit(cfg *LauncherConfig, target string) {
	if cfg.ShellInit != "" {
		TmuxSendKeys(target, cfg.ShellInit)
		time.Sleep(100 * time.Millisecond)
		TmuxSendEnter(target)
	}
}

// sendEditorCommand sends the editor launch command to the edit pane.
func sendEditorCommand(cfg *LauncherConfig, target string) {
	cmd := fmt.Sprintf("MUXCODE=1 NVIM_APPNAME=%s %s", cfg.NvimAppName, cfg.Editor)
	TmuxSendKeys(target, cmd)
	time.Sleep(100 * time.Millisecond)
	TmuxSendEnter(target)
}

// sendPlanEditorCommand sends the editor launch command for the plan window.
// Opens the last-edited doc file in Neovim, falling back to docs/ directory.
// Returns the opened file path (relative to project root), or "" if fallback to docs/.
func sendPlanEditorCommand(cfg *LauncherConfig, target, projectDir string) string {
	lastDoc := ""

	// Primary: most recently modified .md file in docs/ (reflects actual plan agent edits)
	lastDoc = findRecentMarkdown(projectDir)

	// Fallback: last committed doc change via git log (for fresh checkouts where mtimes are reset)
	if lastDoc == "" {
		out, err := exec.Command("git", "-C", projectDir, "log", "-1",
			"--diff-filter=M", "--name-only", "--pretty=format:", "--", "docs/").Output()
		if err == nil {
			lines := strings.Split(strings.TrimSpace(string(out)), "\n")
			if len(lines) > 0 && lines[0] != "" {
				candidate := filepath.Join(projectDir, lines[0])
				if _, err := os.Stat(candidate); err == nil {
					lastDoc = lines[0]
				}
			}
		}
	}

	var cmd string
	if lastDoc != "" {
		cmd = fmt.Sprintf("MUXCODE=1 NVIM_APPNAME=%s %s %s", cfg.NvimAppName, cfg.Editor, lastDoc)
	} else {
		cmd = fmt.Sprintf("MUXCODE=1 NVIM_APPNAME=%s %s docs/", cfg.NvimAppName, cfg.Editor)
	}
	TmuxSendKeys(target, cmd)
	time.Sleep(100 * time.Millisecond)
	TmuxSendEnter(target)
	return lastDoc
}

// findRecentMarkdown finds the most recently modified .md file under docs/.
// Returns the path relative to projectDir, or "" if none found.
func findRecentMarkdown(projectDir string) string {
	docsDir := filepath.Join(projectDir, "docs")
	var newest string
	var newestTime time.Time

	filepath.Walk(docsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip errors
		}
		if info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(info.Name(), ".md") {
			return nil
		}
		if info.ModTime().After(newestTime) {
			newestTime = info.ModTime()
			newest = path
		}
		return nil
	})

	if newest == "" {
		return ""
	}
	rel, err := filepath.Rel(projectDir, newest)
	if err != nil {
		return ""
	}
	return rel
}

// sendCommand sends a command string to a tmux pane.
// Uses separate send-keys + Enter with 100ms delay (critical timing constraint).
func sendCommand(target, command string) {
	TmuxSendKeys(target, command)
	time.Sleep(100 * time.Millisecond)
	TmuxSendEnter(target)
}

// killStaleProcesses kills stale daemon/monitor processes from previous sessions.
func killStaleProcesses(session string) {
	// Anchor patterns with $ to avoid "watch SESSION" matching "watch --monitor SESSION"
	patterns := []struct {
		pattern string
		label   string
	}{
		{"muxcode watch --monitor " + session + "$", "monitor"},
		{"muxcode watch " + session + "$", "daemon"},
	}
	for _, p := range patterns {
		cmd := exec.Command("pkill", "-f", p.pattern)
		if cmd.Run() == nil {
			LogLifecycle(session, "info", "launcher", "stale-kill",
				fmt.Sprintf("Killed stale %s for %s", p.label, session))
		}
	}
	// Let old processes exit before starting new ones
	time.Sleep(100 * time.Millisecond)
}

// startDetachedProcess starts a process in a new session (detached from terminal).
func startDetachedProcess(name string, args ...string) (int, error) {
	binPath, err := exec.LookPath(name)
	if err != nil {
		return 0, err
	}
	cmd := exec.Command(binPath, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	pid := cmd.Process.Pid
	// Don't Wait() — process is detached
	go cmd.Wait() // collect zombie when done
	return pid, nil
}

// EnsureOllama starts Ollama if any role is configured for local LLM.
func EnsureOllama() {
	ollamaURL := os.Getenv("MUXCODE_OLLAMA_URL")
	if ollamaURL == "" {
		ollamaURL = "http://localhost:11434"
	}

	// Check if any role needs local LLM
	needsOllama := false
	for _, env := range os.Environ() {
		parts := strings.SplitN(env, "=", 2)
		if len(parts) == 2 && strings.HasPrefix(parts[0], "MUXCODE_") &&
			strings.HasSuffix(parts[0], "_CLI") && parts[1] == "local" {
			needsOllama = true
			break
		}
	}
	if !needsOllama {
		return
	}

	// Already running?
	client := &http.Client{Timeout: 2 * time.Second}
	if resp, err := client.Get(ollamaURL + "/api/tags"); err == nil {
		resp.Body.Close()
		return
	}

	// Start Ollama in background
	ollamaPath, err := exec.LookPath("ollama")
	if err != nil {
		fmt.Fprintf(os.Stderr, "  Warning: Ollama not installed but MUXCODE_*_CLI=local is set (agents will fall back to Claude Code)\n")
		return
	}

	fmt.Println("  Starting Ollama...")
	cmd := exec.Command(ollamaPath, "serve")
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Start()

	// Poll up to 10s
	for i := 0; i < 20; i++ {
		time.Sleep(500 * time.Millisecond)
		if resp, err := client.Get(ollamaURL + "/api/tags"); err == nil {
			resp.Body.Close()
			fmt.Println("  Ollama ready")
			return
		}
	}
	fmt.Fprintf(os.Stderr, "  Warning: Ollama did not become ready in 10s (agents will fall back to Claude Code)\n")
}

// ResizeWindows resizes all windows in a session after a 1s delay.
// Run as a detached process so it survives the parent's syscall.Exec into tmux.
func ResizeWindows(session string) {
	time.Sleep(1 * time.Second)
	indices, err := TmuxListWindowIndices(session)
	if err != nil {
		return
	}
	for _, idx := range indices {
		TmuxResizeWindow(session + ":" + idx)
	}
}

// PaneState represents the detected state of an agent pane.
type PaneState int

const (
	PaneNotReady     PaneState = iota // Agent still loading
	PaneTrustPrompt                   // "trust this folder" prompt
	PaneBypassPrompt                  // "Bypass Permissions" prompt
	PaneIdle                          // Agent at ❯ prompt (ready)
)

// ClassifyPane determines the state of an agent pane from its captured content.
// Delegates to ClaudeCodeProvider as the default implementation.
// For provider-specific classification, use provider.ClassifyPane().
func ClassifyPane(content string) PaneState {
	p := &ClaudeCodeProvider{}
	return p.ClassifyPane(content)
}

// NeedsWakeUp returns true if the window should receive a startup wake-up message.
func NeedsWakeUp(window string) bool {
	return window == "edit" || window == "analyze"
}

// AutoAccept polls agent panes and dismisses startup prompts.
func AutoAccept(session string, windows []string) {
	accepted := make(map[string]bool)
	woken := make(map[string]bool)

	for attempt := 0; attempt < 30; attempt++ {
		allDone := true
		for _, win := range windows {
			if accepted[win] {
				continue
			}

			pane := session + ":" + win + ".1"
			content, err := TmuxCapturePaneLines(pane, 50)
			if err != nil {
				allDone = false
				continue
			}

			provider := ResolveProvider(win)
			state := provider.ClassifyPane(content)

			switch state {
			case PaneTrustPrompt:
				provider.AcceptStartup(session, pane, state)
				LogLifecycle(session, "info", "auto-accept", "trust-prompt", win)
				allDone = false // bypass prompt may follow

			case PaneBypassPrompt:
				provider.AcceptStartup(session, pane, state)
				LogLifecycle(session, "info", "auto-accept", "bypass-prompt", win)
				accepted[win] = true

			case PaneIdle:
				// Agent at idle prompt — past all startup prompts
				LogLifecycle(session, "info", "auto-accept", "agent-ready", win)
				accepted[win] = true

				// Wake edit and analyze agents
				if NeedsWakeUp(win) && !woken[win] {
					woken[win] = true
					time.Sleep(1 * time.Second) // stabilization delay

					// Non-hook providers (Codex, OpenCode) don't understand
					// the generic "You have new messages" text. Use their
					// SendWakeUp() method which reads the inbox and injects
					// the actual message content with explicit instructions.
					if !provider.SupportsHooks() {
						if err := provider.SendWakeUp(session, win); err != nil {
							LogLifecycle(session, "warn", "auto-accept", "startup-wake-failed", win+": "+err.Error())
						} else {
							LogLifecycle(session, "info", "auto-accept", "startup-wake-provider", win)
						}
					} else {
						// Claude Code agents understand "You have new messages"
						// Re-capture to check for existing wake text
						freshContent, err := TmuxCapturePaneLines(pane, 50)
						if err != nil {
							continue
						}

						if strings.Contains(freshContent, "You have new messages") {
							TmuxSendEnter(pane)
							LogLifecycle(session, "info", "auto-accept", "startup-wake-enter", win)
						} else {
							TmuxSendKeys(pane, "You have new messages")
							// Poll for text to appear
							for poll := 0; poll < 10; poll++ {
								time.Sleep(100 * time.Millisecond)
								cap, err := TmuxCapturePaneLines(pane, 3)
								if err != nil {
									break
								}
								if strings.Contains(cap, "You have new messages") {
									break
								}
							}
							TmuxSendEnter(pane)
							LogLifecycle(session, "info", "auto-accept", "startup-wake-full", win)
						}
					}
				}

			default:
				allDone = false
			}
		}
		if allDone {
			break
		}
		time.Sleep(2 * time.Second)
	}
	LogLifecycle(session, "info", "auto-accept", "complete", "All agents accepted")
}

// attachToSession attaches or switches to a tmux session.
// Uses syscall.Exec to replace the current process.
func attachToSession(session string) error {
	tmuxPath, err := exec.LookPath("tmux")
	if err != nil {
		return fmt.Errorf("tmux not found: %w", err)
	}

	var args []string
	if IsInsideTmux() {
		args = []string{"tmux", "switch-client", "-t", session}
	} else {
		args = []string{"tmux", "attach-session", "-t", session}
	}

	return syscall.Exec(tmuxPath, args, os.Environ())
}

// ScanProjects finds git project directories under the given base directories.
// dirs is a comma-separated list of directories to scan. maxDepth limits traversal.
func ScanProjects(dirs string, maxDepth int) []string {
	var projects []string
	for _, dir := range strings.Split(dirs, ",") {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			continue
		}
		if _, err := os.Stat(dir); err != nil {
			continue
		}
		// Walk looking for .git directories
		filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return filepath.SkipDir
			}
			// Calculate depth relative to base
			rel, _ := filepath.Rel(dir, path)
			depth := strings.Count(rel, string(os.PathSeparator))
			if depth > maxDepth {
				return filepath.SkipDir
			}
			if d.IsDir() && d.Name() == ".git" {
				projects = append(projects, filepath.Dir(path))
				return filepath.SkipDir
			}
			return nil
		})
	}
	return projects
}

// PickProject runs fzf for interactive project selection.
// Returns the selected path or "" if the user cancelled.
func PickProject(projects []string) (string, error) {
	input := strings.Join(projects, "\n")

	// Determine fzf mode based on tmux context
	var fzfArgs []string
	if os.Getenv("TMUX_POPUP") != "" {
		fzfArgs = []string{"--layout=reverse"}
	} else if os.Getenv("TMUX") != "" {
		fzfArgs = []string{"--tmux", "center,60%,50%"}
	} else {
		fzfArgs = []string{"--height=40%"}
	}
	fzfArgs = append(fzfArgs,
		"--prompt", "  Project: ",
		"--reverse", "--border",
		"--header", "Select a project · ESC to cancel",
		"--bind", "esc:abort",
	)

	fzfPath, err := exec.LookPath("fzf")
	if err != nil {
		// Fall back to numbered list if fzf not available
		return pickProjectFallback(projects)
	}

	cmd := exec.Command(fzfPath, fzfArgs...)
	cmd.Stdin = strings.NewReader(input)
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		// fzf exits non-zero on ESC/cancel
		return "", nil
	}
	return strings.TrimSpace(string(out)), nil
}

// pickProjectFallback is a simple numbered list picker when fzf is unavailable.
func pickProjectFallback(projects []string) (string, error) {
	fmt.Println("Select a project:")
	for i, p := range projects {
		fmt.Printf("  %d) %s\n", i+1, p)
	}
	fmt.Print("Enter number (or q to cancel): ")

	var input string
	fmt.Scanln(&input)
	if input == "q" || input == "" {
		return "", nil
	}
	idx := parseInt(input, 0) - 1
	if idx < 0 || idx >= len(projects) {
		return "", fmt.Errorf("invalid selection: %s", input)
	}
	return projects[idx], nil
}

// Powerline and Dracula theme constants for status bar.
const (
	pwrLeft      = "\ue0b0" //
	pwrRight     = "\ue0b2" //
	pwrThinRight = "\ue0b3" //
)

// TransformStatusRight applies Dracula theme transformations to a tmux status-right string.
func TransformStatusRight(sr string) string {
	// Remove powerline arrows
	sr = strings.ReplaceAll(sr, pwrThinRight, "")
	sr = strings.ReplaceAll(sr, pwrRight, "")
	// Strip green arrow color block and unused music segment
	sr = strings.ReplaceAll(sr, "#[fg=#00ff00, bg=#282a36] ", "")
	sr = strings.ReplaceAll(sr, "#[fg=#282a36, bg=#00ff00] #(~/dotfiles/tmux_scripts/music.sh) ", "")
	// Restyle date: tab-color bg
	sr = strings.ReplaceAll(sr, "#[fg=#6272a4, bg=#282a36]",
		"#[fg=#44475a, bg=#282a36]"+pwrRight+"#[fg=#f8f8f2, bg=#44475a]")
	// Restyle time: comment-color bg
	sr = strings.ReplaceAll(sr, "#[fg=#50fa7b]",
		"#[fg=#6272a4, bg=#44475a]"+pwrRight+"#[fg=#f8f8f2, bg=#6272a4]")
	// Add padding around date and time
	sr = strings.Replace(sr, "%b", " %b", 1)
	sr = strings.Replace(sr, "'%y", "'%y ", 1)
	sr = strings.Replace(sr, "%H:%M", " %H:%M:%S ", 1)
	return sr
}

// TransformStatusLeft replaces the window icon with a hamburger menu icon.
func TransformStatusLeft(sl string) string {
	return strings.Replace(sl, "❐", "☰", 1)
}

// WindowStatusFormat returns the Dracula-themed window-status-format string.
// Uses #{@display-name} (per-window user option) instead of #() shell commands
// for instant synchronous rendering. No #{?} conditional — index 0 hiding is
// handled by per-window format overrides in mode.go.
// (tmux #{?} conditionals break when #[] style escapes contain commas.)
func WindowStatusFormat() string {
	return "#[fg=#282a36,bg=#44475a]" + pwrLeft +
		"#[fg=#f8f8f2,bg=#44475a] F#I #{@display-name} " +
		"#[fg=#44475a,bg=#282a36]" + pwrLeft
}

// WindowStatusCurrentFormat returns the Dracula-themed window-status-current-format string.
func WindowStatusCurrentFormat() string {
	return "#[fg=#282a36,bg=#50fa7b]" + pwrLeft +
		"#[fg=#282a36,bg=#50fa7b] F#I*" +
		"#[fg=#282a36,bg=#50fa7b,bold] #{@display-name} " +
		"#[fg=#50fa7b,bg=#282a36]" + pwrLeft
}

// ConfigureStatusBar configures the tmux status bar with Dracula theme.
func ConfigureStatusBar(session string) {
	// --- status-right ---
	sr, err := TmuxShowOption("-gv", "status-right")
	if err == nil && sr != "" {
		TmuxSetOption(session, "status-right", TransformStatusRight(sr))
	}

	// --- window-status-format ---
	// window-status-format is a window option — must use -g (global window default).
	// -t session doesn't work for window options.
	TmuxSetGlobalOption("window-status-format", WindowStatusFormat())
	TmuxSetGlobalOption("window-status-current-format", WindowStatusCurrentFormat())

	// --- status-left hamburger ---
	sl, err := TmuxShowOption("-gv", "status-left")
	if err == nil && sl != "" {
		TmuxSetOption(session, "status-left", TransformStatusLeft(sl))
	}
}
