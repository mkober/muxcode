package bus

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// IsHarnessActive returns true if a local LLM harness is running for the given role.
// It reads the harness PID marker file and validates the process is alive.
// Stale markers (dead PIDs) are cleaned up automatically.
func IsHarnessActive(session, role string) bool {
	path := HarnessMarkerPath(session, role)
	data, err := os.ReadFile(path)
	if err != nil {
		return false // common case: no marker file
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		fmt.Fprintf(os.Stderr, "  [harness] invalid PID in %s: %q — removing\n", path, strings.TrimSpace(string(data)))
		_ = os.Remove(path)
		return false
	}
	if !CheckProcAlive(pid) {
		fmt.Fprintf(os.Stderr, "  [harness] stale PID %d in %s — removing\n", pid, path)
		_ = os.Remove(path)
		return false
	}
	return true
}

// notifiedSizePath returns the path to the marker file that records the inbox
// size at the time of the last notification. Used to deduplicate Notify calls.
func notifiedSizePath(session, role string) string {
	return filepath.Join(BusDir(session), "notified-"+role+".size")
}

// notifyCooldown is the minimum interval between notifications for the same role.
// Even if the inbox size changes, a notification within this window is suppressed.
// This is a defense-in-depth against rapid-fire duplicates if file locking fails.
const notifyCooldown = 2 * time.Second

// sendKeysCooldown is the minimum interval between send-keys notifications for
// the same agent. After delivering a send-keys wake-up, further send-keys are
// suppressed for this duration — falling back to display-message instead.
//
// This prevents the inter-tool-call race: the ❯ prompt appears briefly between
// consecutive tool calls (~100-200ms), and IsAgentIdle() fires send-keys into
// an agent that's about to start its next tool execution. Claude Code interprets
// the injected text as user input and interrupts the running tool.
//
// 10 seconds covers the typical tool-call chain (inbox read → execute → reply →
// inbox peek = 3-8s). The watcher's passive-retry mechanism re-sends via
// send-keys once the agent is truly idle and the cooldown has expired.
const sendKeysCooldown = 10 * time.Second

// lockNotify acquires a per-role file lock for notification deduplication.
// Returns an unlock function. If lock acquisition fails, returns a no-op
// (graceful degradation — old behavior without locking).
func lockNotify(session, role string) func() {
	lockPath := filepath.Join(BusDir(session), "lock", "notify-"+role+".lock")
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return func() {}
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return func() {}
	}
	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}
}

// alreadyNotified returns true if the inbox size matches the last notified size,
// or if the marker was written within the cooldown window (defense-in-depth).
// This prevents duplicate tmux display-message when Notify is called from
// multiple sources (cmd/send.go, watcher, subscriptions) for the same unread messages.
func alreadyNotified(session, role string) bool {
	inboxPath := InboxPath(session, role)
	info, err := os.Stat(inboxPath)
	if err != nil {
		return false
	}
	currentSize := info.Size()
	if currentSize == 0 {
		return true // nothing to notify about
	}

	data, err := os.ReadFile(notifiedSizePath(session, role))
	if err != nil {
		return false
	}
	lastSize, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return false
	}
	if currentSize == lastSize {
		return true
	}

	// Defense-in-depth: if the marker was written recently (within cooldown),
	// suppress even though the size differs. This catches TOCTOU races where
	// two callers both pass the size check before either writes the marker.
	markerPath := notifiedSizePath(session, role)
	markerInfo, err := os.Stat(markerPath)
	if err != nil {
		return false
	}
	return time.Since(markerInfo.ModTime()) < notifyCooldown
}

// IsNotifiedCurrent returns true if the dedup marker matches the current inbox
// size — meaning a prior Notify call already ran for this exact inbox state.
// Used by the watcher startup path to avoid re-notifying when the initial
// send-keys notification from cmd/send.go already succeeded.
func IsNotifiedCurrent(session, role string) bool {
	return alreadyNotified(session, role)
}

// ClearNotified removes the notification dedup marker for a role, allowing
// the next Notify call to proceed even if a prior notification (e.g. a
// display-message that Claude Code can't see) already marked the inbox as
// notified. Used by the watcher's startup notification path to ensure agents
// get a send-keys wake-up after reaching idle for the first time.
func ClearNotified(session, role string) {
	_ = os.Remove(notifiedSizePath(session, role))
}

// SetWaiting creates a marker file indicating that a --wait polling loop is
// active for the given role. While the marker exists, Notify() skips send-keys
// notifications to avoid interrupting the running Bash tool.
func SetWaiting(session, role string) {
	_ = os.WriteFile(WaitingMarkerPath(session, role), []byte(strconv.Itoa(os.Getpid())), 0644)
}

// ClearWaiting removes the --wait marker for a role.
func ClearWaiting(session, role string) {
	_ = os.Remove(WaitingMarkerPath(session, role))
}

// IsWaiting returns true if the given role has an active --wait polling loop.
// Validates that the PID in the marker is still alive to prevent stale markers
// from permanently suppressing notifications.
func IsWaiting(session, role string) bool {
	data, err := os.ReadFile(WaitingMarkerPath(session, role))
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		_ = os.Remove(WaitingMarkerPath(session, role))
		return false
	}
	if !CheckProcAlive(pid) {
		_ = os.Remove(WaitingMarkerPath(session, role))
		return false
	}
	return true
}

// IsSendKeysCoolingDown returns true if a send-keys notification was recently
// delivered to this role (within sendKeysCooldown window). During cooldown,
// Notify falls back to display-message to avoid interrupting tool execution.
func IsSendKeysCoolingDown(session, role string) bool {
	data, err := os.ReadFile(SendKeysMarkerPath(session, role))
	if err != nil {
		return false
	}
	ts, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return false
	}
	return time.Since(time.Unix(ts, 0)) < sendKeysCooldown
}

// markSendKeys records the current time as the last send-keys delivery for a role.
func markSendKeys(session, role string) {
	_ = os.WriteFile(SendKeysMarkerPath(session, role),
		[]byte(strconv.FormatInt(time.Now().Unix(), 10)), 0644)
}

// SetPassiveNotify creates a marker indicating the last notification for a role
// was passive (display-message). The watcher checks this to retry with send-keys
// once the agent becomes idle.
func SetPassiveNotify(session, role string) {
	_ = os.WriteFile(PassiveNotifyMarkerPath(session, role), []byte("1"), 0644)
}

// ClearPassiveNotify removes the passive notification marker for a role.
// Called after a successful send-keys notification.
func ClearPassiveNotify(session, role string) {
	_ = os.Remove(PassiveNotifyMarkerPath(session, role))
}

// HasPassiveNotify returns true if the last notification for a role was passive.
func HasPassiveNotify(session, role string) bool {
	_, err := os.Stat(PassiveNotifyMarkerPath(session, role))
	return err == nil
}

// markNotified records the current inbox size as the last notified size.
func markNotified(session, role string) {
	inboxPath := InboxPath(session, role)
	info, err := os.Stat(inboxPath)
	if err != nil {
		return
	}
	_ = os.WriteFile(notifiedSizePath(session, role), []byte(strconv.FormatInt(info.Size(), 10)), 0644)
}

// idlePromptChar is the Unicode prompt character (❯) used by Claude Code
// when idle and waiting for input. Used to detect whether an agent pane is
// at the idle prompt vs actively executing.
const idlePromptChar = "\u276f"

// IsAgentIdle returns true if the agent's tmux pane is sitting at the idle
// prompt (❯). It captures the last 8 lines of the pane and checks whether
// ANY line is an exact match for the idle prompt character.
//
// Claude Code renders decorative UI elements (borders, "? for shortcuts")
// below the ❯ prompt, so the last non-empty line is often NOT the prompt.
// Scanning all captured lines avoids false negatives from this footer.
//
// Returns false on any error (no tmux, session doesn't exist, etc.) — safe
// to call unconditionally.
func IsAgentIdle(session, role string) bool {
	target := PaneTarget(session, role)
	cmd := exec.Command("tmux", "capture-pane", "-t", target, "-p", "-S", "-8")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	lines := strings.Split(string(out), "\n")
	// Scan all lines — the ❯ prompt may not be the last non-empty line
	// due to Claude Code's decorative footer (borders, help text).
	// When the agent is active, ❯ only appears as part of a longer line
	// (e.g. "❯ You have new messages") which won't match the exact check.
	for _, line := range lines {
		if strings.TrimSpace(line) == idlePromptChar {
			return true
		}
	}
	return false
}

// notifySendKeys injects "You have new messages" + Enter into the agent's
// tmux pane via send-keys. This wakes up idle Claude Code agents that are
// sitting at the ❯ prompt. The shared agent prompt instructs them to run
// `muxcode inbox` when they see this text.
//
// Text and Enter are sent as separate tmux send-keys calls with a brief
// delay between them. Claude Code's TUI can drop the Enter key when it
// arrives in the same pty write as the preceding text characters.
//
// ONLY safe when the agent is confirmed idle (at ❯). Active agents must
// use display-message instead — send-keys to an active pane disrupts
// tool execution, pollutes conversation history, and causes stalls.
func notifySendKeys(session, role string) error {
	target := PaneTarget(session, role)

	// Step 0: clear any residual input so the text starts at column 0.
	// Escape dismisses any completion popups/overlays, C-u clears the line.
	esc := exec.Command("tmux", "send-keys", "-t", target, "Escape")
	_ = esc.Run()
	time.Sleep(50 * time.Millisecond)
	clr := exec.Command("tmux", "send-keys", "-t", target, "C-u")
	_ = clr.Run()
	time.Sleep(50 * time.Millisecond)

	// Step 1: send the text
	cmd := exec.Command("tmux", "send-keys", "-t", target, "You have new messages")
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "  [notify] send-keys text for %s failed: %v\n", role, err)
		return nil
	}

	// Step 2: poll the pane until the text appears, then send Enter.
	// This avoids blind timing delays — we confirm the TUI has rendered
	// the text before submitting. Polls up to 10 times (50ms intervals,
	// ~500ms max) which handles both fast steady-state and slow startup.
	const needle = "You have new messages"
	for i := 0; i < 10; i++ {
		time.Sleep(50 * time.Millisecond)
		out, err := exec.Command("tmux", "capture-pane", "-t", target, "-p", "-S", "-3").Output()
		if err == nil && strings.Contains(string(out), needle) {
			break
		}
	}

	// Step 3: send Enter
	cmd = exec.Command("tmux", "send-keys", "-t", target, "Enter")
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "  [notify] send-keys Enter for %s failed: %v\n", role, err)
	}
	return nil
}

// notifyIdleSendKeys wraps notifySendKeys with the same deduplication logic
// used by notifyDisplayMessage (file lock + inbox size + cooldown).
func notifyIdleSendKeys(session, role string) error {
	if err := exec.Command("tmux", "has-session", "-t", session).Run(); err != nil {
		return nil
	}

	unlock := lockNotify(session, role)
	defer unlock()

	if alreadyNotified(session, role) {
		return nil
	}

	// Re-check cooldown under lock. The caller checks IsSendKeysCoolingDown()
	// before entering this function, but two concurrent callers can both pass
	// that check before either writes the marker. This second check inside the
	// lock prevents the "You have new messagesYou have new messages" race.
	if IsSendKeysCoolingDown(session, role) {
		return nil
	}

	markNotified(session, role)
	ClearPassiveNotify(session, role)
	markSendKeys(session, role)
	return notifySendKeys(session, role)
}

// Notify sends a tmux notification to an agent's pane.
//
// Dual-path strategy:
//   - Harness panes: skipped entirely (they poll inbox directly)
//   - Waiting roles (active --wait loop): skipped — the loop already polls
//     the inbox and send-keys would interrupt the running Bash tool
//   - Idle agents (at ❯ prompt, all roles including edit): uses send-keys to
//     wake them up. The shared agent prompt instructs them to run
//     `muxcode inbox` on seeing "You have new messages".
//   - Active agents (all roles including edit): uses display-message (passive
//     status bar flash) to avoid disrupting in-progress tool execution
//
// Deduplicates: skips if the inbox hasn't changed since the last notification.
func Notify(session, role string) error {
	// Hosted roles (docs, research, pr-read) resolve to their host agent.
	// The message is already in the host's inbox; notify the host's pane.
	role = WindowForRole(role)

	// Always write the trigger file — safe, non-intrusive, no tmux dependency.
	// Agents running `muxcode inbox --poll` detect this via stat() polling.
	// Written before the has-session guard because it's a pure file write that
	// works regardless of tmux state.
	writeTriggerNotify(session, role)

	// Guard: verify the tmux session exists before any tmux commands.
	// Without this, tmux display-message silently falls through to the
	// current pane, causing test/demo sessions to leak notifications
	// into the user's live session.
	if err := exec.Command("tmux", "has-session", "-t", session).Run(); err != nil {
		return nil // session doesn't exist — nothing to notify
	}

	// Skip harness panes — the harness polls inbox directly
	if IsHarnessActive(session, role) {
		return nil
	}

	// Skip send-keys when the role has an active --wait polling loop.
	// The --wait already polls the inbox every 2s, so send-keys is redundant
	// and dangerous — it can interrupt the running Bash tool in Claude Code's
	// TUI, causing the agent to lose its execution flow.
	if IsWaiting(session, role) {
		return nil
	}

	// Skip send-keys when the role has an active --poll loop.
	// The poll loop watches the trigger file we just wrote — send-keys is
	// redundant and risks the inter-tool-call interruption race.
	if IsPolling(session, role) {
		return nil
	}

	// All roles (including edit): wake idle agents via send-keys,
	// fall back to display-message for active agents.
	// Edit was previously excluded (display-message only) because the user
	// types there. But when Claude Code is idle at ❯, send-keys is safe
	// and necessary — display-message is only visible to the human and
	// Claude Code never sees it, so responses go unnoticed after --wait
	// times out.
	//
	// Send-keys cooldown: after delivering send-keys, suppress further
	// send-keys for sendKeysCooldown to prevent the inter-tool-call race.
	// The ❯ prompt appears briefly between tool calls (~100-200ms), and
	// IsAgentIdle() would fire send-keys into an agent about to start its
	// next tool. The watcher's passive-retry re-sends once truly idle.
	if IsAgentIdle(session, role) && !IsSendKeysCoolingDown(session, role) {
		return notifyIdleSendKeys(session, role)
	}
	return notifyDisplayMessage(session, role)
}

// writeTriggerNotify writes the current unix timestamp to the role's trigger
// file. This is always safe — no pane interaction, no TOCTOU race. Agents
// running `muxcode inbox --poll` detect the mtime change and read their inbox.
func writeTriggerNotify(session, role string) {
	ts := strconv.FormatInt(time.Now().UnixNano(), 10)
	_ = os.WriteFile(TriggerNotifyPath(session, role), []byte(ts), 0644)
}

// SetPolling creates a marker file indicating that a --poll loop is active
// for the given role. Stores the PID for stale-marker detection.
func SetPolling(session, role string) {
	_ = os.WriteFile(PollingMarkerPath(session, role), []byte(strconv.Itoa(os.Getpid())), 0644)
}

// ClearPolling removes the --poll marker for a role.
func ClearPolling(session, role string) {
	_ = os.Remove(PollingMarkerPath(session, role))
}

// IsPolling returns true if the given role has an active --poll loop.
// Validates that the PID in the marker is still alive to prevent stale markers
// from permanently suppressing send-keys notifications.
func IsPolling(session, role string) bool {
	data, err := os.ReadFile(PollingMarkerPath(session, role))
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		_ = os.Remove(PollingMarkerPath(session, role))
		return false
	}
	if !CheckProcAlive(pid) {
		_ = os.Remove(PollingMarkerPath(session, role))
		return false
	}
	return true
}

// notifyDisplayMessage sends a passive notification via tmux display-message
// (status bar flash). Used for active agents (not at idle prompt — send-keys
// would interrupt tool execution). All roles including edit use this path
// when the agent is busy.
//
// Best-effort: errors are logged but not returned, since the message is
// already in the inbox and will be seen on the next inbox read.
func notifyDisplayMessage(session, role string) error {
	if err := exec.Command("tmux", "has-session", "-t", session).Run(); err != nil {
		return nil
	}

	unlock := lockNotify(session, role)
	defer unlock()

	if alreadyNotified(session, role) {
		return nil
	}

	markNotified(session, role)

	SetPassiveNotify(session, role)

	// Passive: display-message shows in the tmux status bar.
	// -d 5000 keeps it visible for 5 seconds (default is often too brief).
	// Target the window (session:role) so the message appears on the correct pane.
	// This does NOT inject text into the pane — safe at all times.
	msg := notifyText(session, role)
	target := fmt.Sprintf("%s:%s", session, WindowForRole(role))
	cmd := exec.Command("tmux", "display-message", "-t", target, "-d", "5000",
		fmt.Sprintf("\U0001f4ec %s [%s] %s", session, role, msg))
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "  [notify] display-message for %s failed: %v\n", role, err)
	}
	return nil
}

// notifyText builds the notification string, including a summary of the
// most recent unread message when available.
func notifyText(session, role string) string {
	msgs, err := Peek(session, role)
	if err != nil || len(msgs) == 0 {
		return "You have new messages. Run: muxcode inbox"
	}

	last := msgs[len(msgs)-1]
	payload := last.Payload
	if len(payload) > 100 {
		payload = payload[:100] + "\u2026"
	}

	return fmt.Sprintf("[%s \u2192 %s] %s \u2192 Run: muxcode inbox", last.From, last.Action, payload)
}
