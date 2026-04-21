package bus

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// tmuxRunner is the function used to execute tmux commands.
// Override in tests to capture args without invoking tmux.
var tmuxRunner = defaultTmuxRun

// tmuxQuietRunner executes tmux commands with stderr suppressed.
// Override in tests to capture args without invoking tmux.
var tmuxQuietRunner = defaultTmuxRunQuiet

// tmuxOutputRunner is the function used to execute tmux commands and capture output.
var tmuxOutputRunner = defaultTmuxOutput

func defaultTmuxRun(args ...string) error {
	cmd := exec.Command("tmux", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func defaultTmuxRunQuiet(args ...string) error {
	cmd := exec.Command("tmux", args...)
	cmd.Stdout = os.Stdout
	// stderr intentionally not set — discarded to suppress "no server running" noise
	return cmd.Run()
}

func defaultTmuxOutput(args ...string) (string, error) {
	out, err := exec.Command("tmux", args...).Output()
	return strings.TrimSpace(string(out)), err
}

// TmuxRun executes a tmux command.
func TmuxRun(args ...string) error {
	return tmuxRunner(args...)
}

// TmuxRunQuiet executes a tmux command with stderr suppressed.
// Use for cleanup commands that are expected to fail (e.g. no server running).
func TmuxRunQuiet(args ...string) error {
	return tmuxQuietRunner(args...)
}

// TmuxOutput executes a tmux command and returns its stdout.
func TmuxOutput(args ...string) (string, error) {
	return tmuxOutputRunner(args...)
}

// TmuxKillSession kills a tmux session by name, ignoring errors.
// Uses TmuxRunQuiet to suppress "no server running" stderr noise.
func TmuxKillSession(session string) error {
	return TmuxRunQuiet("kill-session", "-t", session)
}

// TmuxNewSession creates a new detached tmux session.
// width/height of 0 means no size constraint.
func TmuxNewSession(session, firstWindow, dir string, width, height int) error {
	args := []string{"new-session", "-d", "-s", session, "-n", firstWindow, "-c", dir}
	if width > 0 && height > 0 {
		args = append(args, "-x", strconv.Itoa(width), "-y", strconv.Itoa(height))
	}
	return TmuxRun(args...)
}

// TmuxNewWindow creates a new window in a session.
func TmuxNewWindow(session, window, dir string) error {
	return TmuxRun("new-window", "-t", session, "-n", window, "-c", dir)
}

// TmuxSplitWindow splits a pane horizontally.
func TmuxSplitWindow(target, dir string) error {
	return TmuxRun("split-window", "-h", "-t", target, "-c", dir)
}

// TmuxSendKeys sends keys to a tmux pane.
func TmuxSendKeys(target string, keys ...string) error {
	args := append([]string{"send-keys", "-t", target}, keys...)
	return TmuxRun(args...)
}

// TmuxSendEnter sends Enter to a tmux pane.
func TmuxSendEnter(target string) error {
	return TmuxSendKeys(target, "Enter")
}

// TmuxSelectPane selects a pane.
func TmuxSelectPane(target string) error {
	return TmuxRun("select-pane", "-t", target)
}

// TmuxSelectWindow selects a window in a session.
func TmuxSelectWindow(session, window string) error {
	return TmuxRun("select-window", "-t", session+":"+window)
}

// TmuxSetEnv sets a session environment variable.
func TmuxSetEnv(session, key, value string) error {
	return TmuxRun("set-environment", "-t", session, key, value)
}

// TmuxSetOption sets a tmux option on a target.
func TmuxSetOption(target, key, value string) error {
	return TmuxRun("set-option", "-t", target, key, value)
}

// TmuxSetGlobalOption sets a global tmux option.
func TmuxSetGlobalOption(key, value string) error {
	return TmuxRun("set-option", "-g", key, value)
}

// TmuxSetWindowOption sets a per-window tmux option.
func TmuxSetWindowOption(target, key, value string) error {
	return TmuxRun("set-option", "-w", "-t", target, key, value)
}

// TmuxSetHook sets a tmux session hook.
func TmuxSetHook(session, hook, cmd string) error {
	return TmuxRun("set-hook", "-t", session, hook, cmd)
}

// TmuxUnsetGlobalHook unsets a global tmux hook.
// Uses TmuxRunQuiet to suppress "no server running" stderr noise.
func TmuxUnsetGlobalHook(hook string) error {
	return TmuxRunQuiet("set-hook", "-gu", hook)
}

// TmuxCapturePaneLines captures the last N lines from a tmux pane.
func TmuxCapturePaneLines(target string, lines int) (string, error) {
	return TmuxOutput("capture-pane", "-t", target, "-p", "-S", fmt.Sprintf("-%d", lines))
}

// TmuxClientDimensions returns the current tmux client dimensions.
func TmuxClientDimensions() (width, height int, err error) {
	wStr, err := TmuxOutput("display-message", "-p", "#{client_width}")
	if err != nil {
		return 0, 0, err
	}
	hStr, err := TmuxOutput("display-message", "-p", "#{client_height}")
	if err != nil {
		return 0, 0, err
	}
	w, err := strconv.Atoi(wStr)
	if err != nil {
		return 0, 0, fmt.Errorf("parse client width %q: %w", wStr, err)
	}
	h, err := strconv.Atoi(hStr)
	if err != nil {
		return 0, 0, fmt.Errorf("parse client height %q: %w", hStr, err)
	}
	return w, h, nil
}

// TmuxShowOption returns the value of a tmux option.
// scope is "-g" for global or "-t <target>" for session-level.
func TmuxShowOption(scope, key string) (string, error) {
	args := []string{"show-options"}
	if scope != "" {
		args = append(args, strings.Fields(scope)...)
	}
	args = append(args, "-v", key)
	return TmuxOutput(args...)
}

// TmuxListWindowIndices returns the window indices for a session.
func TmuxListWindowIndices(session string) ([]string, error) {
	out, err := TmuxOutput("list-windows", "-t", session, "-F", "#I")
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	return strings.Split(out, "\n"), nil
}

// TmuxResizeWindow resizes a window to fit the client.
func TmuxResizeWindow(target string) error {
	return TmuxRun("resize-window", "-t", target, "-A")
}

// TmuxSwitchClient switches the current tmux client to a session.
func TmuxSwitchClient(session string) error {
	return TmuxRun("switch-client", "-t", session)
}

// TmuxAttachSession attaches to a tmux session.
func TmuxAttachSession(session string) error {
	return TmuxRun("attach-session", "-t", session)
}

// IsInsideTmux returns true if the current process is running inside tmux.
func IsInsideTmux() bool {
	return os.Getenv("TMUX") != ""
}

// TmuxListSessions returns the names of all tmux sessions.
func TmuxListSessions() ([]string, error) {
	out, err := TmuxOutput("list-sessions", "-F", "#{session_name}")
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	return strings.Split(out, "\n"), nil
}

// TmuxCurrentSession returns the name of the current tmux session.
func TmuxCurrentSession() (string, error) {
	return TmuxOutput("display-message", "-p", "#{session_name}")
}

// TmuxDetachClient detaches the current tmux client.
func TmuxDetachClient() error {
	return TmuxRun("detach-client")
}

// QuitSession gracefully quits the current muxcode session.
// If other sessions exist, switches to the last/next one and kills the current.
// If this is the only session, detaches the client and kills in background.
func QuitSession() error {
	current, err := TmuxCurrentSession()
	if err != nil {
		return fmt.Errorf("get current session: %w", err)
	}

	sessions, err := TmuxListSessions()
	if err != nil {
		return fmt.Errorf("list sessions: %w", err)
	}

	// Count other sessions
	var others []string
	for _, s := range sessions {
		if s != current {
			others = append(others, s)
		}
	}

	if len(others) > 0 {
		// Switch to last session (most recent), fall back to next
		if err := TmuxRunQuiet("switch-client", "-l"); err != nil {
			TmuxRunQuiet("switch-client", "-n")
		}
		// Kill the session we just left
		return TmuxKillSession(current)
	}

	// Only session — detach first so the terminal stays open, then kill
	// Use run-shell to kill after detach completes
	return TmuxRun("detach-client", "-E",
		fmt.Sprintf("tmux kill-session -t %q 2>/dev/null", current))
}

// TmuxBuildArgs builds a tmux command argument list (for testing).
// This is a convenience function that returns the args as a slice.
func TmuxBuildArgs(subcmd string, args ...string) []string {
	return append([]string{subcmd}, args...)
}
