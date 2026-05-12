package bus

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

// --- Phase 1: Data structures and evidence collection ---

// DiagnosticReport is the top-level result of a diagnose run for a single role.
type DiagnosticReport struct {
	Role        string              `json:"role"`
	Session     string              `json:"session"`
	Timestamp   int64               `json:"timestamp"`
	AgentState  AgentStateEvidence  `json:"agent_state"`
	InboxState  InboxStateEvidence  `json:"inbox_state"`
	NotifyState NotifyStateEvidence `json:"notify_state"`
	DaemonState DaemonStateEvidence `json:"daemon_state"`
	Timeline    []TimelineEvent     `json:"timeline"`
	Findings    []DiagnosticFinding `json:"findings"`
}

// AgentStateEvidence captures the current state of the agent process and pane.
type AgentStateEvidence struct {
	IsIdle          bool   `json:"is_idle"`
	IsAlive         bool   `json:"is_alive"`
	IsStopped       bool   `json:"is_stopped"`
	IsReloading     bool   `json:"is_reloading"`
	Provider        string `json:"provider"`
	SupportsHooks   bool   `json:"supports_hooks"`
	HasPendingInput    bool   `json:"has_pending_input"`
	IsWindowFocused    bool   `json:"is_window_focused"`
	WiderCaptureIdle   bool   `json:"wider_capture_idle"`
	PaneLastLine       string `json:"pane_last_line"`
}

// InboxStateEvidence captures inbox contents and message ages.
type InboxStateEvidence struct {
	MessageCount     int              `json:"message_count"`
	ActionableCount  int              `json:"actionable_count"`
	OldestMessageAge int64            `json:"oldest_message_age_secs"`
	NewestMessageAge int64            `json:"newest_message_age_secs"`
	Messages         []MessageSummary `json:"messages"`
}

// MessageSummary is a compact representation of an inbox message for diagnostics.
type MessageSummary struct {
	ID      string `json:"id"`
	From    string `json:"from"`
	Type    string `json:"type"`
	Action  string `json:"action"`
	AgeSecs int64  `json:"age_secs"`
	Preview string `json:"preview"`
}

// NotifyStateEvidence captures the notification pipeline state for a role.
type NotifyStateEvidence struct {
	NotifiedIDCount  int   `json:"notified_id_count"`
	UnnotifiedCount  int   `json:"unnotified_count"`
	MarkerAge        int64 `json:"marker_age_secs"`
	IsMarkerStale    bool  `json:"is_marker_stale"`
	TriggerNotifyAge int64 `json:"trigger_notify_age_secs"`
	IsPolling        bool  `json:"is_polling"`
	IsWaiting        bool  `json:"is_waiting"`
}

// DaemonStateEvidence captures daemon health from the keepalive file.
type DaemonStateEvidence struct {
	IsAlive          bool  `json:"is_alive"`
	KeepaliveAge     int64 `json:"keepalive_age_secs"`
	IsKeepaliveStale bool  `json:"is_keepalive_stale"`
}

// DiagnosticFinding describes a detected issue with severity and remediation.
type DiagnosticFinding struct {
	Severity    string   `json:"severity"` // "critical", "warning", "info"
	FailureMode string   `json:"failure_mode"`
	Summary     string   `json:"summary"`
	Evidence    []string `json:"evidence"`
	Remediation []string `json:"remediation"`
}

// --- Phase 2: Timeline event ---

// TimelineEvent represents a lifecycle event with gap annotation.
type TimelineEvent struct {
	Timestamp int64  `json:"timestamp"`
	Event     string `json:"event"`
	Detail    string `json:"detail"`
	GapSecs   int64  `json:"gap_secs,omitempty"`
	GapNote   string `json:"gap_note,omitempty"`
}

// --- Evidence collectors ---

// CollectAgentState gathers agent process and pane state for a role.
func CollectAgentState(session, role string) AgentStateEvidence {
	provider := ResolveProvider(role)
	ev := AgentStateEvidence{
		IsAlive:         IsAgentAlive(session, role),
		IsStopped:       IsAgentStopped(session, role),
		IsReloading:     IsReloading(session, role),
		Provider:        provider.Name(),
		SupportsHooks:   provider.SupportsHooks(),
		HasPendingInput: HasPendingInput(session, role),
	}

	// Only check idle if alive (avoids misleading false for dead agents)
	if ev.IsAlive {
		ev.IsIdle = IsAgentIdle(session, role)
	}

	// Check window focus state
	ev.IsWindowFocused = IsWindowFocused(session, role)

	// Capture last pane line for display and idle detection fallback
	target := PaneTarget(session, role)
	content, err := TmuxCapturePaneLines(target, 5)
	if err != nil {
		ev.PaneLastLine = "(pane not accessible)"
	} else {
		lines := strings.Split(strings.TrimSpace(content), "\n")
		if len(lines) > 0 {
			ev.PaneLastLine = strings.TrimSpace(lines[len(lines)-1])
		}
	}

	// Wider capture (full pane) to detect ❯ that the standard 8-line
	// IsAgentIdle check may have missed (e.g. status bar overlay below prompt).
	// Used by checkIdleDetectionFailure to distinguish genuine "active" from
	// narrow-capture false negative.
	if !ev.IsIdle && ev.IsAlive {
		wideContent, err := TmuxCapturePaneLines(target, 200)
		if err == nil {
			ev.WiderCaptureIdle = PaneHasIdlePrompt(wideContent)
		}
	}

	return ev
}

// CollectInboxState gathers inbox message counts and ages for a role.
func CollectInboxState(session, role string) InboxStateEvidence {
	msgs, _ := Peek(session, role)
	now := time.Now().Unix()
	ev := InboxStateEvidence{
		MessageCount: len(msgs),
	}

	if len(msgs) == 0 {
		return ev
	}

	var summaries []MessageSummary
	for _, m := range msgs {
		if m.Type == "request" {
			ev.ActionableCount++
		}
		age := now - m.TS
		preview := m.Payload
		if len(preview) > 60 {
			preview = preview[:60] + "..."
		}
		summaries = append(summaries, MessageSummary{
			ID:      m.ID,
			From:    m.From,
			Type:    m.Type,
			Action:  m.Action,
			AgeSecs: age,
			Preview: preview,
		})
	}
	ev.Messages = summaries

	// Oldest and newest message ages
	ev.OldestMessageAge = now - msgs[0].TS
	ev.NewestMessageAge = now - msgs[len(msgs)-1].TS

	return ev
}

// CollectNotifyState gathers notification pipeline state for a role.
func CollectNotifyState(session, role string) NotifyStateEvidence {
	ev := NotifyStateEvidence{
		MarkerAge:        -1,
		TriggerNotifyAge: -1,
	}

	// Count notified IDs
	notifiedIDs := readNotifiedIDs(session, role)
	ev.NotifiedIDCount = len(notifiedIDs)

	// Count unnotified messages
	unnotified := UnnotifiedMessages(session, role)
	ev.UnnotifiedCount = len(unnotified)

	// Check marker age
	markerPath := notifiedIDsPath(session, role)
	if info, err := os.Stat(markerPath); err == nil {
		age := int64(time.Since(info.ModTime()).Seconds())
		ev.MarkerAge = age
		ev.IsMarkerStale = age > 15
	}

	// Check trigger-notify file age
	triggerPath := TriggerNotifyPath(session, role)
	if info, err := os.Stat(triggerPath); err == nil {
		ev.TriggerNotifyAge = int64(time.Since(info.ModTime()).Seconds())
	}

	// Polling and waiting state
	ev.IsPolling = IsPolling(session, role)
	ev.IsWaiting = IsWaiting(session, role)

	return ev
}

// CollectDaemonState gathers daemon health from the keepalive file.
func CollectDaemonState(session string) DaemonStateEvidence {
	ev := DaemonStateEvidence{
		KeepaliveAge: -1,
	}

	ev.IsAlive = IsDaemonAlive(session, 30)

	data, err := os.ReadFile(DaemonKeepalivePath(session))
	if err == nil {
		var ts int64
		fmt.Sscanf(strings.TrimSpace(string(data)), "%d", &ts)
		if ts > 0 {
			ev.KeepaliveAge = time.Now().Unix() - ts
			ev.IsKeepaliveStale = ev.KeepaliveAge > 30
		}
	}

	return ev
}

// CollectEvidence orchestrates all evidence collectors for a role.
func CollectEvidence(session, role string) DiagnosticReport {
	return DiagnosticReport{
		Role:        role,
		Session:     session,
		Timestamp:   time.Now().Unix(),
		AgentState:  CollectAgentState(session, role),
		InboxState:  CollectInboxState(session, role),
		NotifyState: CollectNotifyState(session, role),
		DaemonState: CollectDaemonState(session),
	}
}

// --- Phase 2: Lifecycle timeline analysis ---

// roleRelevantEvents lists lifecycle event types that are relevant for
// per-role diagnosis. Events must mention the role in their detail.
var roleRelevantEvents = map[string]bool{
	"idle-wake":        true,
	"idle-transition":  true,
	"inbox-notify":     true,
	"startup-wake":     true,
	"idle-task-rescue": true,
	"agent-down":       true,
	"agent-restarting": true,
	"agent-recovered":  true,
	"compact-inject":   true,
	"reload-start":     true,
	"reload-complete":  true,
}

// BuildTimeline reads recent lifecycle events for a role and annotates gaps
// in the expected notification chain (inbox-notify -> idle-wake).
// Reads all lifecycle entries (no pre-limit) because role-relevant events
// may be sparse among entries for other roles.
func BuildTimeline(session, role string, limit int) []TimelineEvent {
	entries, err := FilterLifecycleLog(session, LifecycleFilterOpts{})
	if err != nil {
		return nil
	}

	var events []TimelineEvent
	for _, e := range entries {
		// Include events that mention this role or are globally relevant
		if !isRoleRelevantEvent(e, role) {
			continue
		}
		events = append(events, TimelineEvent{
			Timestamp: e.TS,
			Event:     e.Event,
			Detail:    e.Detail,
		})
	}

	// Trim to requested limit (keep most recent)
	if len(events) > limit {
		events = events[len(events)-limit:]
	}

	// Annotate gaps between expected event pairs
	annotateGaps(events, role)

	return events
}

// isRoleRelevantEvent returns true if a lifecycle entry is relevant to the
// given role. Checks if the event type is role-relevant and the detail
// mentions the role name.
func isRoleRelevantEvent(e LifecycleEntry, role string) bool {
	if !roleRelevantEvents[e.Event] {
		return false
	}
	return strings.Contains(e.Detail, role)
}

// annotateGaps scans the timeline for missing idle-wake events after
// inbox-notify events. If inbox-notify fires but no idle-wake follows within
// 10 seconds, a gap note is added.
func annotateGaps(events []TimelineEvent, role string) {
	for i := 0; i < len(events); i++ {
		if events[i].Event != "inbox-notify" {
			continue
		}
		notifyTS := events[i].Timestamp

		// Look for idle-wake within 10 seconds
		foundWake := false
		for j := i + 1; j < len(events); j++ {
			gap := events[j].Timestamp - notifyTS
			if gap > 10 {
				break // past the expected window
			}
			if events[j].Event == "idle-wake" {
				foundWake = true
				events[j].GapSecs = gap
				break
			}
		}

		if !foundWake {
			// Check if there's a subsequent event to show what happened instead
			if i+1 < len(events) {
				gap := events[i+1].Timestamp - notifyTS
				events[i+1].GapSecs = gap
				events[i+1].GapNote = fmt.Sprintf("expected idle-wake within 10s of inbox-notify, got %s after %ds", events[i+1].Event, gap)
			} else {
				// No subsequent event at all
				events[i].GapNote = "inbox-notify with no subsequent idle-wake"
			}
		}
	}
}

// CountRepeatedFailures counts consecutive inbox-notify events without a
// following idle-wake in the timeline. Used to quantify notification failures.
func CountRepeatedFailures(events []TimelineEvent) int {
	count := 0
	for i := 0; i < len(events); i++ {
		if events[i].Event != "inbox-notify" {
			continue
		}
		notifyTS := events[i].Timestamp

		foundWake := false
		for j := i + 1; j < len(events); j++ {
			gap := events[j].Timestamp - notifyTS
			if gap > 10 {
				break
			}
			if events[j].Event == "idle-wake" {
				foundWake = true
				break
			}
		}
		if !foundWake {
			count++
		}
	}
	return count
}

// --- Phase 3: Diagnostic checks ---

// DiagnosticCheck is a function that inspects a report and returns a finding
// if a known failure mode is detected. Returns nil if no issue found.
type DiagnosticCheck func(report *DiagnosticReport) *DiagnosticFinding

// diagnosticChecks is the ordered list of failure-mode detectors.
// They run in priority order — most critical first.
var diagnosticChecks = []DiagnosticCheck{
	checkDaemonDead,
	checkStaleNotifiedIDs,
	checkMissedSendKeys,
	checkIdleDetectionFailure,
	checkDaemonNotWaking,
	checkPostRestartWakeGap,
	checkProviderMismatch,
	checkReloadMarkerStuck,
	checkPendingInputBlocking,
	checkActiveWithStaleMessages,
	checkNoActionableMessages,
}

// RunDiagnostics executes all diagnostic checks and populates report.Findings.
func RunDiagnostics(report *DiagnosticReport) {
	for _, check := range diagnosticChecks {
		if finding := check(report); finding != nil {
			report.Findings = append(report.Findings, *finding)
		}
	}
}

// checkDaemonDead detects when the daemon keepalive is stale (>30s).
func checkDaemonDead(report *DiagnosticReport) *DiagnosticFinding {
	if !report.DaemonState.IsKeepaliveStale {
		return nil
	}
	evidence := []string{
		fmt.Sprintf("Keepalive age: %ds (stale threshold: 30s)", report.DaemonState.KeepaliveAge),
	}
	if report.DaemonState.KeepaliveAge < 0 {
		evidence = []string{"No keepalive file found — daemon may never have started"}
	}
	return &DiagnosticFinding{
		Severity:    "critical",
		FailureMode: "daemon-dead",
		Summary:     "Daemon is not running — no agent will receive notifications",
		Evidence:    evidence,
		Remediation: []string{
			"Restart daemon: muxcode watch &",
			"Check daemon logs: muxcode lifecycle show --event daemon-start",
		},
	}
}

// checkStaleNotifiedIDs detects when the agent is idle with unnotified
// messages and the notification marker is stale (>15s).
func checkStaleNotifiedIDs(report *DiagnosticReport) *DiagnosticFinding {
	if !report.AgentState.IsIdle {
		return nil
	}
	if report.NotifyState.UnnotifiedCount == 0 {
		return nil
	}
	if !report.NotifyState.IsMarkerStale {
		return nil
	}
	return &DiagnosticFinding{
		Severity:    "critical",
		FailureMode: "stale-notified-ids",
		Summary:     "Agent is idle with unnotified messages and stale notification marker",
		Evidence: []string{
			fmt.Sprintf("Agent is idle (at prompt)"),
			fmt.Sprintf("%d unnotified message(s) in inbox", report.NotifyState.UnnotifiedCount),
			fmt.Sprintf("Notification marker age: %ds (stale >15s)", report.NotifyState.MarkerAge),
		},
		Remediation: []string{
			fmt.Sprintf("Clear stale notification state: muxcode notify --clear %s", report.Role),
			fmt.Sprintf("Manual wake: tmux send-keys -t %s \"You have new messages\" Enter", report.Role),
		},
	}
}

// checkMissedSendKeys detects when idle-wake was logged but the agent is
// still idle with unconsumed inbox messages.
func checkMissedSendKeys(report *DiagnosticReport) *DiagnosticFinding {
	if !report.AgentState.IsIdle {
		return nil
	}
	if report.InboxState.ActionableCount == 0 {
		return nil
	}

	// Look for recent idle-wake in timeline
	hasRecentWake := false
	for _, ev := range report.Timeline {
		if ev.Event == "idle-wake" {
			hasRecentWake = true
			break
		}
	}
	if !hasRecentWake {
		return nil
	}

	return &DiagnosticFinding{
		Severity:    "warning",
		FailureMode: "missed-send-keys",
		Summary:     "Send-keys injection was logged but agent did not process it (TUI redraw race)",
		Evidence: []string{
			"idle-wake event found in timeline",
			fmt.Sprintf("Agent is still idle with %d actionable message(s)", report.InboxState.ActionableCount),
			"Send-keys may have been dropped by Claude Code TUI redraw",
		},
		Remediation: []string{
			fmt.Sprintf("Manual wake: tmux send-keys -t %s \"You have new messages\" Enter", report.Role),
			"Retry will happen automatically after 15s (notifyRetryInterval)",
		},
	}
}

// checkIdleDetectionFailure detects when the pane shows the idle prompt
// but IsAgentIdle returned false.
func checkIdleDetectionFailure(report *DiagnosticReport) *DiagnosticFinding {
	if report.AgentState.IsIdle {
		return nil // idle detection is working
	}
	if !report.AgentState.IsAlive {
		return nil // agent is dead, not an idle detection issue
	}

	// Check if wider capture found ❯ that the standard 8-line check missed,
	// or if the pane last line shows the idle prompt.
	widerFound := report.AgentState.WiderCaptureIdle
	lastLineFound := strings.Contains(report.AgentState.PaneLastLine, idlePromptChar)

	if !widerFound && !lastLineFound {
		return nil
	}

	evidence := []string{
		fmt.Sprintf("IsAgentIdle (8-line): false, IsAlive: true"),
		fmt.Sprintf("Provider: %s", report.AgentState.Provider),
	}
	if widerFound {
		evidence = append(evidence, "Wider capture (30 lines) found ❯ — narrow capture missed it")
	}
	if lastLineFound {
		evidence = append(evidence, fmt.Sprintf("Pane last line: %q", report.AgentState.PaneLastLine))
	}

	return &DiagnosticFinding{
		Severity:    "critical",
		FailureMode: "idle-detection-failure",
		Summary:     "Agent pane shows idle prompt but IsAgentIdle() returned false — daemon cannot deliver messages",
		Evidence:    evidence,
		Remediation: []string{
			"Daemon watchdog (30s) will force delivery via wider capture",
			fmt.Sprintf("Manual wake: tmux send-keys -t %s \"You have new messages\" Enter", report.Role),
		},
	}
}

// checkDaemonNotWaking detects when inbox-notify fires but no idle-wake
// follows within 10 seconds, and the agent is currently idle.
func checkDaemonNotWaking(report *DiagnosticReport) *DiagnosticFinding {
	if !report.AgentState.IsIdle {
		return nil
	}
	if report.InboxState.ActionableCount == 0 {
		return nil
	}

	failures := CountRepeatedFailures(report.Timeline)
	if failures == 0 {
		return nil
	}

	return &DiagnosticFinding{
		Severity:    "critical",
		FailureMode: "daemon-not-waking",
		Summary:     fmt.Sprintf("Daemon logged inbox-notify but never fired idle-wake (%d time(s))", failures),
		Evidence: []string{
			fmt.Sprintf("inbox-notify without idle-wake: %d occurrence(s) in timeline", failures),
			"Agent is idle with actionable messages",
			fmt.Sprintf("%d actionable message(s) in inbox", report.InboxState.ActionableCount),
		},
		Remediation: []string{
			fmt.Sprintf("Clear notification state: muxcode notify --clear %s", report.Role),
			"Restart daemon: muxcode watch &",
			fmt.Sprintf("Manual wake: tmux send-keys -t %s \"You have new messages\" Enter", report.Role),
		},
	}
}

// checkPostRestartWakeGap detects when idle-transition is logged after a
// restart but no subsequent idle-wake fires for pending messages.
func checkPostRestartWakeGap(report *DiagnosticReport) *DiagnosticFinding {
	if !report.AgentState.IsIdle {
		return nil
	}
	if report.InboxState.ActionableCount == 0 {
		return nil
	}

	// Look for idle-transition without subsequent idle-wake
	hasIdleTransition := false
	hasSubsequentWake := false
	for _, ev := range report.Timeline {
		if ev.Event == "idle-transition" {
			hasIdleTransition = true
			hasSubsequentWake = false // reset — look for wake after this transition
		}
		if hasIdleTransition && ev.Event == "idle-wake" {
			hasSubsequentWake = true
		}
	}

	if !hasIdleTransition || hasSubsequentWake {
		return nil
	}

	return &DiagnosticFinding{
		Severity:    "warning",
		FailureMode: "post-restart-wake-gap",
		Summary:     "Agent transitioned to idle after restart but was never woken for pending messages",
		Evidence: []string{
			"idle-transition logged but no idle-wake followed",
			fmt.Sprintf("Agent is idle with %d actionable message(s)", report.InboxState.ActionableCount),
		},
		Remediation: []string{
			fmt.Sprintf("Clear notification state: muxcode notify --clear %s", report.Role),
			fmt.Sprintf("Manual wake: tmux send-keys -t %s \"You have new messages\" Enter", report.Role),
		},
	}
}

// checkProviderMismatch detects when a non-hook provider agent has messages
// but the daemon may be skipping wake-up due to provider-specific cooldown.
func checkProviderMismatch(report *DiagnosticReport) *DiagnosticFinding {
	if report.AgentState.SupportsHooks {
		return nil // hook providers don't have this issue
	}
	if report.InboxState.ActionableCount == 0 {
		return nil
	}

	// Non-hook provider with pending messages — check if notification marker
	// is present (daemon did try) vs absent (daemon skipped)
	if report.NotifyState.MarkerAge >= 0 {
		return nil // daemon did notify — issue is elsewhere
	}

	return &DiagnosticFinding{
		Severity:    "warning",
		FailureMode: "provider-mismatch",
		Summary:     fmt.Sprintf("Non-hook provider (%s) has pending messages but no notification was attempted", report.AgentState.Provider),
		Evidence: []string{
			fmt.Sprintf("Provider: %s (hooks: false)", report.AgentState.Provider),
			fmt.Sprintf("%d actionable message(s) in inbox", report.InboxState.ActionableCount),
			"No notification marker found — daemon may not have attempted wake-up",
		},
		Remediation: []string{
			fmt.Sprintf("Check daemon provider handling for %s", report.AgentState.Provider),
			fmt.Sprintf("Manual wake via provider: muxcode notify %s", report.Role),
		},
	}
}

// checkReloadMarkerStuck detects a stale reload marker that prevents the
// daemon from monitoring the agent.
func checkReloadMarkerStuck(report *DiagnosticReport) *DiagnosticFinding {
	if !report.AgentState.IsReloading {
		return nil
	}

	return &DiagnosticFinding{
		Severity:    "critical",
		FailureMode: "reload-marker-stuck",
		Summary:     "Reload marker is present — daemon is skipping health checks for this agent",
		Evidence: []string{
			fmt.Sprintf("IsReloading: true for role %s", report.Role),
			"Daemon excludes reloading roles from health monitoring",
			"Agent may be stuck in a failed reload cycle",
		},
		Remediation: []string{
			fmt.Sprintf("Remove stale reload marker: rm %s", ReloadMarkerPath(report.Session, report.Role)),
			fmt.Sprintf("Restart agent: muxcode agent-health --start %s", report.Role),
		},
	}
}

// checkPendingInputBlocking detects when HasPendingInput is true, which
// prevents notification injection to avoid corrupting user input.
func checkPendingInputBlocking(report *DiagnosticReport) *DiagnosticFinding {
	if !report.AgentState.HasPendingInput {
		return nil
	}
	if report.InboxState.ActionableCount == 0 {
		return nil
	}

	return &DiagnosticFinding{
		Severity:    "warning",
		FailureMode: "pending-input-blocking",
		Summary:     "Text in input buffer is blocking notification injection",
		Evidence: []string{
			fmt.Sprintf("HasPendingInput: true"),
			fmt.Sprintf("Pane last line: %q", report.AgentState.PaneLastLine),
			fmt.Sprintf("%d actionable message(s) waiting", report.InboxState.ActionableCount),
		},
		Remediation: []string{
			"If user is typing: wait for them to submit (press Enter)",
			fmt.Sprintf("If stale output: clear with tmux send-keys -t %s C-u", report.Role),
		},
	}
}

// checkNoActionableMessages detects when inbox has messages but none are
// requests. This is informational — not a bug.
func checkNoActionableMessages(report *DiagnosticReport) *DiagnosticFinding {
	if report.InboxState.MessageCount == 0 {
		return nil
	}
	if report.InboxState.ActionableCount > 0 {
		return nil
	}

	return &DiagnosticFinding{
		Severity:    "info",
		FailureMode: "no-actionable-messages",
		Summary:     "Inbox has messages but none are actionable requests (all response/event type)",
		Evidence: []string{
			fmt.Sprintf("%d message(s) in inbox, 0 actionable", report.InboxState.MessageCount),
			"Only response and event messages present — no wake-up needed",
		},
		Remediation: []string{
			"No action needed — this is expected behavior",
			fmt.Sprintf("Consume stale inbox: muxcode inbox --role %s", report.Role),
		},
	}
}

// checkActiveWithStaleMessages detects when an agent appears "active" (not idle)
// but has unnotified actionable messages for a long time. Neither Notify() nor
// the daemon's checkIdleAgents delivers to non-idle agents, so messages pile up.
// This catches cases where IsAgentIdle is wrong and the wider capture also missed.
func checkActiveWithStaleMessages(report *DiagnosticReport) *DiagnosticFinding {
	if report.AgentState.IsIdle {
		return nil // idle detection works — other checks handle this
	}
	if !report.AgentState.IsAlive {
		return nil
	}
	if report.InboxState.ActionableCount == 0 {
		return nil
	}
	// Only flag if oldest unnotified message is >60s old
	if report.NotifyState.UnnotifiedCount == 0 {
		return nil
	}
	if report.InboxState.OldestMessageAge < 60 {
		return nil
	}
	// Skip if the idle detection failure check already caught it
	if report.AgentState.WiderCaptureIdle {
		return nil
	}

	return &DiagnosticFinding{
		Severity:    "warning",
		FailureMode: "active-with-stale-messages",
		Summary:     fmt.Sprintf("Agent appears active with %d unnotified message(s) for %ds — delivery blocked", report.NotifyState.UnnotifiedCount, report.InboxState.OldestMessageAge),
		Evidence: []string{
			fmt.Sprintf("IsAgentIdle: false (8-line and 30-line capture)"),
			fmt.Sprintf("%d actionable, %d unnotified, oldest: %ds ago", report.InboxState.ActionableCount, report.NotifyState.UnnotifiedCount, report.InboxState.OldestMessageAge),
			"Neither Notify() nor daemon checkIdleAgents delivers to non-idle agents",
		},
		Remediation: []string{
			"Agent may be genuinely busy — wait for it to finish",
			"If stuck, check pane for permission prompts or errors",
			fmt.Sprintf("Manual wake: tmux send-keys -t %s \"You have new messages\" Enter", report.Role),
			fmt.Sprintf("Restart: muxcode agent-health --start %s", report.Role),
		},
	}
}

// --- Phase 4: Output formatting ---

// Dracula color codes for terminal output.
const (
	diagColorReset   = "\033[0m"
	diagColorRed     = "\033[38;2;255;85;85m"   // Dracula red
	diagColorYellow  = "\033[38;2;241;250;140m" // Dracula yellow
	diagColorGreen   = "\033[38;2;80;250;123m"  // Dracula green
	diagColorCyan    = "\033[38;2;139;233;253m" // Dracula cyan
	diagColorPurple  = "\033[38;2;189;147;249m" // Dracula purple
	diagColorComment = "\033[38;2;98;114;164m"  // Dracula comment
	diagColorOrange  = "\033[38;2;255;184;108m" // Dracula orange
	diagColorBold    = "\033[1m"
	diagColorDim     = "\033[2m"
)

// FormatDiagnosticReport renders a human-readable diagnostic report with
// Dracula-themed colors and clear section headers.
func FormatDiagnosticReport(report *DiagnosticReport) string {
	var b strings.Builder

	// Agent state header
	b.WriteString(fmt.Sprintf("\n  %sAgent:%s %s%s%s (%s, hooks: %s)\n",
		diagColorComment, diagColorReset,
		diagColorCyan, report.Role, diagColorReset,
		report.AgentState.Provider,
		boolYesNo(report.AgentState.SupportsHooks)))

	// State line
	stateStr := "unknown"
	if report.AgentState.IsStopped {
		stateStr = fmt.Sprintf("%sstopped%s", diagColorRed, diagColorReset)
	} else if !report.AgentState.IsAlive {
		stateStr = fmt.Sprintf("%sdead%s", diagColorRed, diagColorReset)
	} else if report.AgentState.IsReloading {
		stateStr = fmt.Sprintf("%sreloading%s", diagColorYellow, diagColorReset)
	} else if report.AgentState.IsIdle {
		stateStr = fmt.Sprintf("%sidle%s (at %s prompt)", diagColorGreen, diagColorReset, idlePromptChar)
	} else if report.AgentState.WiderCaptureIdle {
		stateStr = fmt.Sprintf("%sactive%s (%s❯ found in wider capture — likely idle%s)",
			diagColorPurple, diagColorReset, diagColorYellow, diagColorReset)
	} else {
		stateStr = fmt.Sprintf("%sactive%s", diagColorPurple, diagColorReset)
	}
	b.WriteString(fmt.Sprintf("  %sState:%s %s\n", diagColorComment, diagColorReset, stateStr))

	healthStr := fmt.Sprintf("%salive%s", diagColorGreen, diagColorReset)
	if !report.AgentState.IsAlive {
		healthStr = fmt.Sprintf("%sdead%s", diagColorRed, diagColorReset)
	}
	b.WriteString(fmt.Sprintf("  %sHealth:%s %s\n", diagColorComment, diagColorReset, healthStr))

	// Inbox section
	b.WriteString(fmt.Sprintf("\n  %sInbox:%s %d message(s) (%d actionable)\n",
		diagColorComment, diagColorReset,
		report.InboxState.MessageCount, report.InboxState.ActionableCount))
	for _, m := range report.InboxState.Messages {
		b.WriteString(fmt.Sprintf("    %s[%s→%s]%s %s  %s(%ds ago)%s\n",
			diagColorPurple, m.From, m.Action, diagColorReset,
			m.Preview,
			diagColorComment, m.AgeSecs, diagColorReset))
	}

	// Notification state
	b.WriteString(fmt.Sprintf("\n  %sNotification state:%s\n", diagColorComment, diagColorReset))
	markerAgeStr := "no marker"
	if report.NotifyState.MarkerAge >= 0 {
		markerAgeStr = fmt.Sprintf("%ds", report.NotifyState.MarkerAge)
		if report.NotifyState.IsMarkerStale {
			markerAgeStr += fmt.Sprintf(" %s— STALE%s", diagColorRed, diagColorReset)
		}
	}
	b.WriteString(fmt.Sprintf("    Notified IDs: %d (marker age: %s)\n",
		report.NotifyState.NotifiedIDCount, markerAgeStr))
	b.WriteString(fmt.Sprintf("    Unnotified: %d message(s)\n", report.NotifyState.UnnotifiedCount))
	if report.NotifyState.TriggerNotifyAge >= 0 {
		b.WriteString(fmt.Sprintf("    Trigger file: %ds ago\n", report.NotifyState.TriggerNotifyAge))
	}
	if report.NotifyState.IsPolling {
		b.WriteString(fmt.Sprintf("    %sPolling: active%s\n", diagColorGreen, diagColorReset))
	}
	if report.NotifyState.IsWaiting {
		b.WriteString(fmt.Sprintf("    %sWaiting: active%s\n", diagColorGreen, diagColorReset))
	}

	// Daemon state
	daemonStr := fmt.Sprintf("%salive%s", diagColorGreen, diagColorReset)
	if !report.DaemonState.IsAlive {
		daemonStr = fmt.Sprintf("%sdead%s", diagColorRed, diagColorReset)
	}
	keepaliveStr := "unknown"
	if report.DaemonState.KeepaliveAge >= 0 {
		keepaliveStr = fmt.Sprintf("%ds ago", report.DaemonState.KeepaliveAge)
	}
	b.WriteString(fmt.Sprintf("\n  %sDaemon:%s %s (keepalive: %s)\n",
		diagColorComment, diagColorReset, daemonStr, keepaliveStr))

	// Timeline section
	if len(report.Timeline) > 0 {
		b.WriteString(fmt.Sprintf("\n  %sTimeline (last %d events):%s\n",
			diagColorComment, len(report.Timeline), diagColorReset))
		for _, ev := range report.Timeline {
			ts := time.Unix(ev.Timestamp, 0).Format("15:04:05")
			line := fmt.Sprintf("    %s%s%s  %-18s  %s",
				diagColorDim, ts, diagColorReset,
				ev.Event, ev.Detail)
			if ev.GapSecs > 0 {
				line += fmt.Sprintf("  %s(%ds gap)%s", diagColorYellow, ev.GapSecs, diagColorReset)
			}
			b.WriteString(line + "\n")
			if ev.GapNote != "" {
				b.WriteString(fmt.Sprintf("    %s--- %s ---%s\n",
					diagColorRed, ev.GapNote, diagColorReset))
			}
		}
	}

	// Findings section
	if len(report.Findings) == 0 {
		b.WriteString(fmt.Sprintf("\n  %s✅ No issues detected%s\n\n",
			diagColorGreen, diagColorReset))
	} else {
		for _, f := range report.Findings {
			icon, color := findingSeverityStyle(f.Severity)
			b.WriteString(fmt.Sprintf("\n  %s%s FINDING: %s (%s)%s\n",
				color, icon, f.Summary, f.Severity, diagColorReset))
			b.WriteString(fmt.Sprintf("     Failure mode: %s\n", f.FailureMode))
			if len(f.Evidence) > 0 {
				b.WriteString("     Evidence:\n")
				for _, e := range f.Evidence {
					b.WriteString(fmt.Sprintf("     %s- %s%s\n", diagColorComment, e, diagColorReset))
				}
			}
			if len(f.Remediation) > 0 {
				b.WriteString("     Remediation:\n")
				for i, r := range f.Remediation {
					b.WriteString(fmt.Sprintf("     %s%d. %s%s\n", diagColorCyan, i+1, r, diagColorReset))
				}
			}
		}
		b.WriteString("\n")
	}

	return b.String()
}

// FormatDiagnosticJSON serializes a report as indented JSON.
func FormatDiagnosticJSON(report *DiagnosticReport) string {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Sprintf(`{"error": %q}`, err.Error())
	}
	return string(data)
}

// FormatDiagnosticSummary produces a one-line summary for --all mode.
func FormatDiagnosticSummary(report *DiagnosticReport) string {
	status := fmt.Sprintf("%s✅%s", diagColorGreen, diagColorReset)
	detail := "healthy"

	criticalCount := 0
	warningCount := 0
	for _, f := range report.Findings {
		switch f.Severity {
		case "critical":
			criticalCount++
		case "warning":
			warningCount++
		}
	}

	if criticalCount > 0 {
		status = fmt.Sprintf("%s❌%s", diagColorRed, diagColorReset)
		detail = fmt.Sprintf("%d critical", criticalCount)
		if warningCount > 0 {
			detail += fmt.Sprintf(", %d warning", warningCount)
		}
	} else if warningCount > 0 {
		status = fmt.Sprintf("%s⚠%s", diagColorYellow, diagColorReset)
		detail = fmt.Sprintf("%d warning", warningCount)
	}

	aliveStr := "alive"
	if !report.AgentState.IsAlive {
		aliveStr = "dead"
	} else if report.AgentState.IsIdle {
		aliveStr = "idle"
	} else {
		aliveStr = "active"
	}

	return fmt.Sprintf("  %s  %-10s  %-8s  inbox:%-3d  %s",
		status, report.Role, aliveStr, report.InboxState.MessageCount, detail)
}

// DiagnosableRoles returns the list of roles that can be diagnosed.
// Excludes non-agent roles (webhook, api) that don't have tmux panes.
func DiagnosableRoles() []string {
	exclude := map[string]bool{
		"webhook": true,
		"api":     true,
		"auto":    true,
	}
	var roles []string
	for _, r := range KnownRoles {
		if !exclude[r] && !IsHostedRole(r) && !IsModeRole(r) {
			roles = append(roles, r)
		}
	}
	sort.Strings(roles)
	return roles
}

// --- Helpers ---

func boolYesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

func findingSeverityStyle(severity string) (icon, color string) {
	switch severity {
	case "critical":
		return "❌", diagColorRed
	case "warning":
		return "⚠ ", diagColorYellow
	case "info":
		return "ℹ️ ", diagColorCyan
	default:
		return "?", diagColorReset
	}
}
