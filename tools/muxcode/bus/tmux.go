package bus

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
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

// TmuxHasSession checks if a tmux session with the given name exists.
func TmuxHasSession(session string) bool {
	return TmuxRunQuiet("has-session", "-t", session) == nil
}

// SessionIdleSeconds returns the number of seconds since the most recent tmux
// client activity (keyboard input) across all clients attached to the session,
// or -1 when no client is attached or the query fails. tmux's client_activity
// tracks real user input — background pane output and status-bar refreshes do
// not bump it — so branch-time uses it to pause accumulation while the user is
// idle (away from the keyboard) even though the session is still attached.
func SessionIdleSeconds(session string) int64 {
	out, err := TmuxOutput("list-clients", "-t", session, "-F", "#{client_activity}")
	if err != nil {
		return -1
	}
	return idleSecondsFromActivity(out, time.Now().Unix())
}

// idleSecondsFromActivity parses tmux client_activity timestamps (one epoch per
// line) and returns seconds since the newest, or -1 when none are present.
func idleSecondsFromActivity(out string, now int64) int64 {
	newest := int64(-1)
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if ts, err := strconv.ParseInt(line, 10, 64); err == nil && ts > newest {
			newest = ts
		}
	}
	if newest < 0 {
		return -1
	}
	if idle := now - newest; idle > 0 {
		return idle
	}
	return 0
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

// TmuxSendLiteral injects text into a pane as literal characters:
// `send-keys -t <target> -l -- <text>`. The two flags guard different
// failure modes: -l stops key-name interpretation ("Enter" stays five
// characters), while -- ends flag parsing — without it a payload whose
// first character is a dash (a bullet line, a quoted CLI flag) is
// rejected by tmux as `invalid flag -` and the message is never
// delivered (MUX-104; -l alone does NOT prevent this). Every
// dynamic-payload injection must go through here — TmuxSendKeys stays
// for fixed key names like Enter and C-u, which must not be literal.
func TmuxSendLiteral(target, text string) error {
	return TmuxRun("send-keys", "-t", target, "-l", "--", text)
}

// TmuxSendEscape sends Escape to a tmux pane. Used before injecting a wake-up to
// dismiss any Claude Code overlay (the periodic "How is Claude doing this
// session?" feedback survey, autocomplete popups, etc.) that would otherwise
// consume the subsequent Enter keystroke instead of submitting the injected text.
func TmuxSendEscape(target string) error {
	return TmuxSendKeys(target, "Escape")
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

// TmuxIsWindowActive returns true if the given window is the active window
// in the specified session (i.e. the user is currently viewing it).
func TmuxIsWindowActive(session, window string) bool {
	target := session + ":" + window
	out, err := TmuxOutput("display-message", "-t", target, "-p", "#{window_active}")
	if err != nil {
		return false
	}
	return out == "1"
}

// TmuxSessionAttached returns true if the session has at least one attached
// client. A detached session (e.g. a background subsession) has an "active
// window" in tmux terms, but no user can actually be typing into it —
// callers use this to distinguish real user focus from a meaningless
// active-window flag on a detached session.
func TmuxSessionAttached(session string) bool {
	out, err := TmuxOutput("display-message", "-t", session, "-p", "#{session_attached}")
	if err != nil {
		return false
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	return err == nil && n > 0
}

// TmuxClearInput clears any text in the agent's input buffer before injecting
// new text via send-keys. Used to clear stale agent output left at the prompt.
//
// Sends a robust key sequence rather than a bare C-u: C-u alone kills only from
// the cursor to the start of the line, so it misses text after the cursor and
// does not reliably empty Claude Code's input box. The sequence is:
//
//	C-e  move cursor to end of input
//	C-u  kill from cursor (now end) back to start — clears the whole line
//	C-a  move to start (cleanup for any residual)
//	C-k  kill from start to end — clears anything C-u left behind
//
// C-u is retained in the sequence both for its clearing effect and because the
// notification path's failure handling keys off a C-u send failure to hold the
// notification for the next cycle.
func TmuxClearInput(target string) error {
	return TmuxRun("send-keys", "-t", target, "C-e", "C-u", "C-a", "C-k")
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

// TerminalDimensions returns the size of the controlling terminal via
// `stty size` on /dev/tty. It exists for launches from a plain terminal
// (no tmux client yet): without real dimensions, new-session falls back
// to tmux's 80x24 default, and the first attach then rescales every
// pane proportionally — a fixed 14-row control pane split from a 24-row
// window opens at over half the real screen (user-reported 2026-08-26).
func TerminalDimensions() (width, height int, err error) {
	tty, err := os.Open("/dev/tty")
	if err != nil {
		return 0, 0, err
	}
	defer tty.Close()
	cmd := exec.Command("stty", "size")
	cmd.Stdin = tty
	out, err := cmd.Output()
	if err != nil {
		return 0, 0, err
	}
	return parseSttySize(string(out))
}

// parseSttySize parses `stty size` output ("rows cols") into (width, height).
func parseSttySize(out string) (width, height int, err error) {
	fields := strings.Fields(strings.TrimSpace(out))
	if len(fields) != 2 {
		return 0, 0, fmt.Errorf("unexpected stty size output %q", out)
	}
	rows, err1 := strconv.Atoi(fields[0])
	cols, err2 := strconv.Atoi(fields[1])
	if err1 != nil || err2 != nil || rows <= 0 || cols <= 0 {
		return 0, 0, fmt.Errorf("unexpected stty size output %q", out)
	}
	return cols, rows, nil
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

// TmuxResizeWindowToSize resizes a window to explicit dimensions. Used for
// windows in detached sessions, where -A has no connected client to fit to and
// is therefore a no-op.
func TmuxResizeWindowToSize(target string, width, height int) error {
	return TmuxRun("resize-window", "-t", target,
		"-x", strconv.Itoa(width), "-y", strconv.Itoa(height))
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
