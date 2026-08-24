package bus

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Auto-clear (MUX-103) injects Claude Code's /clear into episodic agents
// (review, plan, commit, run, ...) after their work completes. Each bus request
// is self-contained and cross-task state lives in muxcode memory, so retained
// conversation context between tasks is dead weight that compounds input-token
// cost every turn. Off by default; enrollment is explicit per role via
// MUXCODE_AUTO_CLEAR_ROLES.

// autoClearExcluded lists roles that must never be cleared: edit holds the user
// conversation, auto holds the autonomous loop state. Applied both at config
// parse (AutoClearRoles) and inside AutoClearEligible, so a config listing them
// still cannot clear them.
var autoClearExcluded = map[string]bool{"edit": true, "auto": true}

// autoClearIsIdle reports whether the role's pane is at an idle prompt.
// Injectable so unit tests can drive the verdict without a live tmux pane.
var autoClearIsIdle = func(session, role string) bool {
	return ResolveProvider(role).IsIdle(session, role)
}

// autoClearInject performs the /clear injection into a pane. Injectable for
// tests. Mirrors the /compact path: clear residual input, then text and Enter
// as separate send-keys calls with delays (the dropped-Enter pitfall).
var autoClearInject = func(target string) error {
	_ = exec.Command("tmux", "send-keys", "-t", target, "Escape").Run()
	time.Sleep(100 * time.Millisecond)
	_ = exec.Command("tmux", "send-keys", "-t", target, "C-u").Run()
	time.Sleep(100 * time.Millisecond)
	if err := exec.Command("tmux", "send-keys", "-t", target, "/clear").Run(); err != nil {
		return fmt.Errorf("send /clear: %w", err)
	}
	time.Sleep(200 * time.Millisecond)
	_ = exec.Command("tmux", "send-keys", "-t", target, "Enter").Run()
	return nil
}

// autoClearSetting resolves a config key through the standard chain:
// environment variable first, then the shell config file.
func autoClearSetting(key string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	if v, ok := GetShellConfig("")[key]; ok {
		return v
	}
	return ""
}

// AutoClearRoles returns the roles enrolled for auto-clear via
// MUXCODE_AUTO_CLEAR_ROLES (comma-separated). Default empty = feature off.
// Entries are normalized to their window role (pr-read → commit), deduped, and
// filtered: unknown roles and the hard-excluded edit/auto are dropped.
func AutoClearRoles() []string {
	raw := autoClearSetting("MUXCODE_AUTO_CLEAR_ROLES")
	if raw == "" {
		return nil
	}
	seen := make(map[string]bool)
	var roles []string
	for _, part := range strings.Split(raw, ",") {
		role := WindowForRole(strings.ToLower(strings.TrimSpace(part)))
		if role == "" || !IsKnownRole(role) || autoClearExcluded[role] || seen[role] {
			continue
		}
		seen[role] = true
		roles = append(roles, role)
	}
	return roles
}

// AutoClearQuietSecs returns the quiet window in seconds that must elapse
// after a task completes before /clear is injected. Configurable via
// MUXCODE_AUTO_CLEAR_QUIET_SECS; default 60.
func AutoClearQuietSecs() int64 {
	if v := autoClearSetting("MUXCODE_AUTO_CLEAR_QUIET_SECS"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n >= 0 {
			return n
		}
	}
	return 60
}

// ReadAutoClearMarker returns the unix time of the role's last auto-clear,
// or 0 if the role has never been cleared.
func ReadAutoClearMarker(session, role string) int64 {
	data, err := os.ReadFile(AutoClearMarkerPath(session, role))
	if err != nil {
		return 0
	}
	n, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// WriteAutoClearMarker records now as the role's last auto-clear time.
func WriteAutoClearMarker(session, role string) error {
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	return os.WriteFile(AutoClearMarkerPath(session, role), []byte(ts+"\n"), 0644)
}

// AutoClearEligible evaluates the guard matrix for clearing a role's
// conversation. Every guard is a safety condition that must hold at the moment
// of injection; the completion trigger and quiet window live in AutoClearDue.
// Returns false with a human-readable reason when any guard blocks.
func AutoClearEligible(session, role string) (bool, string) {
	role = WindowForRole(strings.ToLower(strings.TrimSpace(role)))
	if !IsKnownRole(role) {
		return false, fmt.Sprintf("unknown role %q", role)
	}
	if autoClearExcluded[role] {
		return false, role + " is hard-excluded from clearing"
	}
	if _, err := os.Stat(HarnessMarkerPath(session, role)); err == nil {
		return false, "harness pane (no TUI conversation to clear)"
	}
	if cli := ResolveProviderCLI(role); cli != "claude" {
		return false, fmt.Sprintf("provider %s (/clear is a Claude Code built-in)", cli)
	}
	if IsReloading(session, role) {
		return false, "reload in progress"
	}
	if active, err := ActiveModeRole(session, role); err == nil && active != role {
		return false, fmt.Sprintf("window cycled to %s mode", active)
	}
	if HasActionableMessages(session, role) {
		return false, "pending actionable inbox"
	}
	if hasLiveInFlightTask(session, role) {
		return false, "in-flight task"
	}
	if !autoClearIsIdle(session, role) {
		return false, "agent not idle"
	}
	return true, ""
}

// AutoClearDue reports whether completed work makes role due for an
// auto-clear: some task completed after the last clear marker, and the quiet
// window has elapsed since. Returns the completion time for logging context.
func AutoClearDue(session, role string, now, quietSecs int64) (bool, int64) {
	role = WindowForRole(strings.ToLower(strings.TrimSpace(role)))
	completed := LastTaskCompletion(session, role)
	if completed == 0 {
		return false, 0
	}
	if completed <= ReadAutoClearMarker(session, role) {
		return false, completed
	}
	if now-completed < quietSecs {
		return false, completed
	}
	return true, completed
}

// LastTaskCompletion returns the unix time of the most recent completed work
// addressed to role (or a hosted role its window fronts), or 0 if none.
//
// Both completion stores are consulted because they cover different paths:
// tasks record --wait/--track delegations, while responded delivery statuses
// also cover chain requests (SendNoCC) that never create a task — the main way
// roles like review receive work. The response time comes from the response
// message ID's timestamp prefix; statuses whose timestamp cannot advance the
// running maximum skip the log scan that resolves their recipient.
func LastTaskCompletion(session, role string) int64 {
	window := WindowForRole(role)
	var latest int64
	if tasks, err := ListTasks(session, TaskCompleted); err == nil {
		for _, t := range tasks {
			if WindowForRole(t.To) == window && t.ResponseAt > latest {
				latest = t.ResponseAt
			}
		}
	}
	if statuses, err := ListDeliveryStatuses(session); err == nil {
		for _, ds := range statuses {
			if ds.Status != StatusResponded || ds.ResponseID == "" {
				continue
			}
			ts := msgIDTimestamp(ds.ResponseID)
			if ts <= latest {
				continue
			}
			if orig, ok := FindMessageByID(session, ds.ID); ok && WindowForRole(orig.To) == window {
				latest = ts
			}
		}
	}
	return latest
}

// msgIDTimestamp parses the unix-timestamp prefix of a message ID
// ("{ts}-{role}-{hash}"). Returns 0 for malformed IDs.
func msgIDTimestamp(id string) int64 {
	i := strings.IndexByte(id, '-')
	if i <= 0 {
		return 0
	}
	n, err := strconv.ParseInt(id[:i], 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// ClearAgent injects /clear into the role's pane, writes the per-role marker
// that gates re-fire, and logs an auto-clear lifecycle event. Callers must
// check AutoClearEligible first; this performs the injection unconditionally.
func ClearAgent(session, role, source, trigger string) error {
	role = WindowForRole(strings.ToLower(strings.TrimSpace(role)))
	target := PaneTarget(session, role)
	if err := autoClearInject(target); err != nil {
		LogLifecycle(session, "error", source, "auto-clear-failed",
			fmt.Sprintf("role=%s trigger=%s: %v", role, trigger, err))
		return err
	}
	if err := WriteAutoClearMarker(session, role); err != nil {
		return fmt.Errorf("write auto-clear marker: %w", err)
	}
	LogLifecycle(session, "info", source, "auto-clear",
		fmt.Sprintf("role=%s trigger=%s", role, trigger))
	return nil
}

// hasLiveInFlightTask reports whether any non-expired, unanswered in-flight
// task is addressed to role's window. Expired tasks are ignored for the same
// reason as in the dedup guard: a task stuck in-flight past its timeout must
// not block forever.
//
// A reply implies completion — the same invariant hasReceipt enforces for
// receipts. Bare request sends default to --track, and only the daemon's
// checkTrackedTasks flips a tracked task to completed, so a task whose request
// was already answered can sit "in-flight" for up to a poll cycle (or the full
// 600s timeout when no daemon runs). Counting it would block the clear for
// work that is already done, so a task with a responded delivery status is
// treated as finished here.
func hasLiveInFlightTask(session, role string) bool {
	tasks, err := ListTasks(session, TaskInFlight)
	if err != nil || len(tasks) == 0 {
		return false
	}
	window := WindowForRole(role)
	now := time.Now().Unix()
	for _, t := range tasks {
		if WindowForRole(t.To) != window || TaskExpired(t, now) {
			continue
		}
		if ds, err := ReadDeliveryStatus(session, t.ID); err == nil && ds.Status == StatusResponded {
			continue
		}
		return true
	}
	return false
}
