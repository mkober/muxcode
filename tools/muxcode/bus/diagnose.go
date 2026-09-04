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
	IsIdle           bool   `json:"is_idle"`
	IsAlive          bool   `json:"is_alive"`
	IsStopped        bool   `json:"is_stopped"`
	IsReloading      bool   `json:"is_reloading"`
	Provider         string `json:"provider"`
	SupportsHooks    bool   `json:"supports_hooks"`
	HasPendingInput  bool   `json:"has_pending_input"`
	IsWindowFocused  bool   `json:"is_window_focused"`
	WiderCaptureIdle bool   `json:"wider_capture_idle"`
	PaneLastLine     string `json:"pane_last_line"`
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
//
// AckDelivery and the receipt fields describe the delivery model actually in
// force. Without them diagnose read every session through the pre-cutover
// pane-scrape model and mistook its own blind spot for a fault — see
// AckDeliveryActive.
type NotifyStateEvidence struct {
	NotifiedIDCount  int   `json:"notified_id_count"`
	UnnotifiedCount  int   `json:"unnotified_count"`
	MarkerAge        int64 `json:"marker_age_secs"`
	IsMarkerStale    bool  `json:"is_marker_stale"`
	TriggerNotifyAge int64 `json:"trigger_notify_age_secs"`
	IsPolling        bool  `json:"is_polling"`
	IsWaiting        bool  `json:"is_waiting"`
	AckDelivery      bool  `json:"ack_delivery_active"`
	ReceiptGapCount  int   `json:"receipt_gap_count"`
	ReceiptGapAge    int64 `json:"receipt_gap_oldest_secs"`
}

// DaemonStateEvidence captures daemon health from the keepalive file, plus
// the build identity the daemon recorded at startup set against the binary
// running diagnose. DaemonBuild is nil when the daemon never recorded one.
type DaemonStateEvidence struct {
	IsAlive          bool  `json:"is_alive"`
	KeepaliveAge     int64 `json:"keepalive_age_secs"`
	IsKeepaliveStale bool  `json:"is_keepalive_stale"`
	DaemonBuild      *Info `json:"daemon_build,omitempty"`
	InstalledBuild   Info  `json:"installed_build"`
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
	// IsAgentIdle check may have missed (e.g. long parked text wrapping the
	// prompt out of the window). Used by checkIdleDetectionFailure to
	// distinguish genuine "active" from narrow-capture false negative.
	if !ev.IsIdle && ev.IsAlive {
		wideContent, err := TmuxCapturePaneLines(target, widePaneCaptureLines)
		if err == nil {
			ev.WiderCaptureIdle = PaneShowsRecoverableIdle(wideContent)
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
		// Self-addressed messages (from == to, non-startup) are filtered from
		// the daemon's wake-up path and never loop — don't count them as
		// actionable here, or diagnose would falsely report a stuck inbox.
		if m.Type == "request" && !isLoopingSelfSend(m) {
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

	// Receipt evidence — the positive delivery signal under the cutover. The
	// notified-IDs marker above only records that the daemon TRIED; a receipt
	// records that the agent actually got it. Reading only the marker is how a
	// notified-but-never-consumed message registered as fully delivered.
	ev.AckDelivery = AckDeliveryActive(session)
	gap := ReceiptGap(session, role, diagnoseReceiptGapSecs*time.Second)
	ev.ReceiptGapCount = len(gap)
	if len(gap) > 0 {
		now := time.Now().Unix()
		for _, m := range gap {
			if age := now - m.TS; age > ev.ReceiptGapAge {
				ev.ReceiptGapAge = age
			}
		}
	}

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

	ev.InstalledBuild = BuildInfo()
	if build, ok := ReadDaemonVersion(session); ok {
		ev.DaemonBuild = &build
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
//
// Every wake-family name below must be one the code actually emits. The
// original list carried "idle-wake" and "startup-wake" and nothing else from
// that family, which left the successful-delivery events invisible to the
// timeline while the failures stayed visible — the timeline could only ever
// render a delivery pipeline that was losing.
var roleRelevantEvents = map[string]bool{
	"idle-wake":                true,
	"idle-response-wake":       true,
	"idle-combined-wake":       true,
	"idle-task-retry":          true,
	"idle-transition":          true,
	"inbox-notify":             true,
	"startup-wake":             true,
	"startup-wake-full":        true,
	"startup-wake-enter":       true,
	"startup-wake-provider":    true,
	"startup-wake-failed":      true,
	"wake-full":                true,
	"wake-enter":               true,
	"wake-provider":            true,
	"wake-failed":              true,
	"watchdog-force-deliver":   true,
	"delivery-gap":             true,
	"idle-task-rescue":         true,
	"agent-down":               true,
	"agent-restarting":         true,
	"agent-recovered":          true,
	"compact-inject":           true,
	"reload-start":             true,
	"reload-complete":          true,
	"permission-blocked":       true,
	"permission-block-cleared": true,
	"agent-definitionless":     true,
	"definition-reload":        true,
	"definition-reload-giveup": true,
	"definition-restored":      true,
	"launch-refused":           true,
	"agent-down-snapshot":      true,
}

// wakeEvents are the lifecycle events that count as "the agent was woken" when
// resolving an inbox-notify. Delivery has more than one emitter — the idle
// sweep, the combined deferred delivery, the startup path, the mode-switch
// path, the force-deliver watchdog — and matching only the literal "idle-wake"
// treated every one of the others as a miss.
var wakeEvents = map[string]bool{
	"idle-wake":              true,
	"idle-response-wake":     true,
	"idle-combined-wake":     true,
	"idle-task-retry":        true,
	"idle-task-rescue":       true,
	"startup-wake":           true,
	"startup-wake-full":      true,
	"startup-wake-enter":     true,
	"startup-wake-provider":  true,
	"wake-full":              true,
	"wake-enter":             true,
	"wake-provider":          true,
	"watchdog-force-deliver": true,
}

// isWakeEvent reports whether a timeline event represents a delivery to the
// agent, satisfying a preceding inbox-notify.
func isWakeEvent(event string) bool {
	return wakeEvents[event]
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

	// Annotate gaps between expected event pairs. Skipped under the delivery-ack
	// cutover: there, no daemon wake is EXPECTED to follow an inbox-notify at
	// all (the agent pulls its own inbox and receipts are the delivery
	// evidence), so every notify would be annotated as a failure on a healthy
	// session. That is precisely the false evidence the report used to render.
	if !AckDeliveryActive(session) {
		annotateGaps(events, role)
	}

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

		// Look for a wake within 10 seconds
		foundWake := false
		for j := i + 1; j < len(events); j++ {
			gap := events[j].Timestamp - notifyTS
			if gap > 10 {
				break // past the expected window
			}
			if isWakeEvent(events[j].Event) {
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
				events[i+1].GapNote = fmt.Sprintf("expected a wake within 10s of inbox-notify, got %s after %ds", events[i+1].Event, gap)
			} else {
				// No subsequent event at all
				events[i].GapNote = "inbox-notify with no subsequent wake"
			}
		}
	}
}

// CountRepeatedFailures counts inbox-notify events with no following wake in
// the timeline. Used to quantify notification failures.
//
// Only meaningful when a daemon wake is the expected delivery mechanism —
// callers must gate on the delivery model (see checkDaemonNotWaking), or a
// healthy post-cutover session reads as one failure per notify.
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
			if isWakeEvent(events[j].Event) {
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

// diagnoseReceiptGapSecs is how long an inbox message may sit without a receipt
// before diagnose counts it as un-delivered. Matches the daemon's own
// pollHealthGapSecs so the two agree on what "stuck" means.
const diagnoseReceiptGapSecs = 45

// diagnoseStuckInboxSecs is the age past which an unconsumed actionable message
// is treated as a hard anomaly that the report must explain. Deliberately well
// above the receipt-gap threshold: an agent legitimately mid-turn will consume
// its inbox long before this.
const diagnoseStuckInboxSecs = 120

// diagnosticChecks is the ordered list of failure-mode detectors.
// They run in priority order — most critical first.
//
// checkUnexplainedEvidence MUST stay last: it is the verdict-consistency
// backstop and reads the findings the earlier checks produced.
var diagnosticChecks = []DiagnosticCheck{
	checkDaemonDead,
	checkAgentDown,
	checkStaleNotifiedIDs,
	checkMissedSendKeys,
	checkIdleDetectionFailure,
	checkReceiptGap,
	checkDaemonNotWaking,
	checkPostRestartWakeGap,
	checkProviderMismatch,
	checkReloadMarkerStuck,
	checkPendingInputBlocking,
	checkActiveWithStaleMessages,
	checkNoActionableMessages,
	checkDaemonVersionMismatch,
	checkUnexplainedEvidence,
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
			fmt.Sprintf("Force-deliver pending inbox: muxcode deliver %s --force", report.Role),
			fmt.Sprintf("Or clear stale notification state: muxcode notify --clear %s", report.Role),
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

	// Look for a recent wake in the timeline
	hasRecentWake := false
	for _, ev := range report.Timeline {
		if isWakeEvent(ev.Event) {
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
			fmt.Sprintf("Force-deliver pending inbox: muxcode deliver %s --force", report.Role),
			"Retry will happen automatically after 15s (notifyRetryInterval)",
		},
	}
}

// PaneShowsRecoverableIdle reports whether captured pane content shows an agent
// genuinely parked at a prompt the daemon could deliver to — not a prompt
// rendered mid-turn.
//
// The thinking check is the load-bearing half, and omitting it is not a
// cosmetic gap. Claude Code renders ❯ in its input box at ALL times, including
// while a turn runs, so a bare prompt scan reports every WORKING agent as
// "likely idle". That fired a false critical idle-detection-failure whose own
// remediation — `muxcode deliver <role> --force` — injects into the running
// turn and kills it ("Interrupted · What should Claude do instead?"). The
// diagnostic thus manufactured the breakage it claimed to have found, and the
// wreckage read as fresh evidence for the phantom.
//
// Mirrors ClaudeCodeProvider.IsIdle's order (thinking first, then prompt scan)
// so diagnose and the daemon share one definition of "idle" instead of
// diagnose re-implementing half of it and drifting.
//
// Prompt scan runs over the FULL content (the ❯ may have scrolled past the live
// region after a big tool-output block), but the thinking check runs only over
// the live TAIL. "Thinking" is a CURRENT-STATE property: Claude Code's live
// activity — the "✻ Fermenting…" spinner and its "· esc to interrupt ·" counter
// — always renders in the bottom few lines, just above the composer and footer.
// Judging it over the whole capture false-positived on scrollback: a completed
// turn, a quoted footer, or the agent's own output literally discussing "esc to
// interrupt" (the plan agent writing about idle detection did exactly this) all
// match the thinking signatures even though the agent is idle NOW. That made the
// daemon's recoverable-idle watchdog never fire on the very parked-input wedge it
// exists to rescue — the wide capture that let it find a scrolled ❯ also swept up
// stale thinking text. Anchoring the thinking check to the tail keeps the
// scrolled-prompt reach without letting history masquerade as the present.
func PaneShowsRecoverableIdle(content string) bool {
	return PaneHasIdlePrompt(content) && !isClaudeThinking(paneLiveTail(content))
}

// recoverableIdleTailLines bounds how much of the pane bottom the thinking check
// in PaneShowsRecoverableIdle considers. Claude Code's persistent bottom UI —
// live spinner, composer, footer — spans only a handful of lines; a genuinely
// working agent's spinner sits ~5-6 lines above the footer. Ten lines covers
// that live region with margin while excluding the scrollback above it.
const recoverableIdleTailLines = 10

// paneLiveTail returns the last recoverableIdleTailLines non-blank-trimmed lines
// of content — the live bottom region of the pane — after dropping trailing
// blank padding that tmux capture-pane appends. Used to scope the thinking check
// to the current state rather than the full scrollback.
func paneLiveTail(content string) string {
	lines := strings.Split(content, "\n")
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) > recoverableIdleTailLines {
		lines = lines[len(lines)-recoverableIdleTailLines:]
	}
	return strings.Join(lines, "\n")
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

	// Fire ONLY on the wide capture, which now applies the provider's own
	// thinking check (PaneShowsRecoverableIdle). The old second trigger —
	// strings.Contains(PaneLastLine, "❯") — was a bare substring scan of a
	// single line with no thinking check at all, so it re-opened the same false
	// positive through the back door. It is kept below purely as evidence: the
	// 200-line capture already subsumes the last line, so it adds no signal of
	// its own, only noise that could fire on a ❯ appearing mid-line in tool
	// output.
	widerFound := report.AgentState.WiderCaptureIdle
	lastLineFound := strings.Contains(report.AgentState.PaneLastLine, idlePromptChar)

	if !widerFound {
		return nil
	}

	evidence := []string{
		fmt.Sprintf("IsAgentIdle (8-line): false, IsAlive: true"),
		fmt.Sprintf("Provider: %s", report.AgentState.Provider),
	}
	evidence = append(evidence, fmt.Sprintf(
		"Wider capture (%d lines) found an idle prompt with no thinking indicator — narrow capture missed it",
		widePaneCaptureLines))
	if lastLineFound {
		evidence = append(evidence, fmt.Sprintf("Pane last line: %q", report.AgentState.PaneLastLine))
	}

	return &DiagnosticFinding{
		Severity:    "critical",
		FailureMode: "idle-detection-failure",
		Summary:     "Agent pane shows idle prompt but IsAgentIdle() returned false — daemon cannot deliver messages",
		Evidence:    evidence,
		Remediation: []string{
			"Daemon watchdog (15s) will force delivery via wider capture",
			fmt.Sprintf("Force-deliver now: muxcode deliver %s --force", report.Role),
		},
	}
}

// checkDaemonNotWaking detects when inbox-notify fires but no idle-wake
// follows within 10 seconds, and the agent is currently idle.
func checkDaemonNotWaking(report *DiagnosticReport) *DiagnosticFinding {
	// Under the delivery-ack cutover the daemon does not wake idle agents at
	// all — they pull their own inboxes — so "inbox-notify with no wake after
	// it" is the normal shape, not a fault. checkReceiptGap covers this model.
	if report.NotifyState.AckDelivery {
		return nil
	}
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
			fmt.Sprintf("Force-deliver pending inbox: muxcode deliver %s", report.Role),
			fmt.Sprintf("Clear notification state: muxcode notify --clear %s", report.Role),
			"Restart daemon: muxcode watch &",
		},
	}
}

// checkPostRestartWakeGap detects when idle-transition is logged after a
// restart but no subsequent idle-wake fires for pending messages.
func checkPostRestartWakeGap(report *DiagnosticReport) *DiagnosticFinding {
	// Same delivery-model gate as checkDaemonNotWaking: idle-transition is
	// emitted only by the pane-scrape idle sweep, which the cutover bypasses.
	if report.NotifyState.AckDelivery {
		return nil
	}
	if !report.AgentState.IsIdle {
		return nil
	}
	if report.InboxState.ActionableCount == 0 {
		return nil
	}

	// Look for idle-transition without a subsequent wake. Matching only the
	// literal "idle-wake" here fails the opposite way to checkMissedSendKeys:
	// a delivery under any other name reads as "no wake followed", so the check
	// reports a gap that did not happen.
	hasIdleTransition := false
	hasSubsequentWake := false
	for _, ev := range report.Timeline {
		if ev.Event == "idle-transition" {
			hasIdleTransition = true
			hasSubsequentWake = false // reset — look for wake after this transition
		}
		if hasIdleTransition && isWakeEvent(ev.Event) {
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
			fmt.Sprintf("Force-deliver pending inbox: muxcode deliver %s", report.Role),
			fmt.Sprintf("Clear notification state: muxcode notify --clear %s", report.Role),
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
			fmt.Sprintf("Force-deliver (clears parked input first): muxcode deliver %s --force", report.Role),
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
	if report.InboxState.OldestMessageAge < 60 {
		return nil
	}
	// Skip if the idle detection failure check already caught it
	if report.AgentState.WiderCaptureIdle {
		return nil
	}

	// Notified-but-still-queued is the STRONGER signature, not a reason to stay
	// quiet. This check used to require UnnotifiedCount > 0, which excluded
	// exactly the wedge it exists to catch: once the daemon records a notify,
	// the message leaves the unnotified set, so an agent that was notified and
	// then never consumed the message scored zero and the check returned nil.
	// The report rendered a stale marker in red and an "active" agent sitting on
	// old messages, and still printed "No issues detected".
	notified := report.InboxState.ActionableCount - report.NotifyState.UnnotifiedCount
	if notified < 0 {
		notified = 0
	}

	severity := "warning"
	summary := fmt.Sprintf("Agent appears active with %d actionable message(s) unconsumed for %ds — delivery blocked",
		report.InboxState.ActionableCount, report.InboxState.OldestMessageAge)
	evidence := []string{
		"IsAgentIdle: false (8-line and wide capture)",
		fmt.Sprintf("%d actionable, %d unnotified, %d notified-but-unconsumed, oldest: %ds ago",
			report.InboxState.ActionableCount, report.NotifyState.UnnotifiedCount, notified, report.InboxState.OldestMessageAge),
		"Neither Notify() nor daemon checkIdleAgents delivers to non-idle agents",
	}
	if notified > 0 {
		// The daemon believes it delivered; the inbox says otherwise.
		severity = "critical"
		evidence = append(evidence,
			fmt.Sprintf("%d message(s) marked notified but still queued — the wake was recorded, not received", notified))
		if report.NotifyState.IsMarkerStale {
			evidence = append(evidence,
				fmt.Sprintf("Notified-IDs marker is stale (%ds) — no delivery attempt since", report.NotifyState.MarkerAge))
		}
	}
	if report.NotifyState.ReceiptGapCount > 0 {
		severity = "critical"
		evidence = append(evidence,
			fmt.Sprintf("%d message(s) carry no delivery receipt (oldest %ds)",
				report.NotifyState.ReceiptGapCount, report.NotifyState.ReceiptGapAge))
	}

	return &DiagnosticFinding{
		Severity:    severity,
		FailureMode: "active-with-stale-messages",
		Summary:     summary,
		Evidence:    evidence,
		Remediation: []string{
			"Agent may be genuinely busy — check the pane before forcing",
			"If stuck, check pane for permission prompts or errors",
			fmt.Sprintf("Force-deliver pending inbox: muxcode deliver %s --force", report.Role),
			fmt.Sprintf("Restart: muxcode agent-health --start %s", report.Role),
		},
	}
}

// checkAgentDown detects an agent whose process is gone without an intentional
// stop or an in-flight reload explaining it.
//
// There was no such check at all: every other detector either required IsIdle
// (never true for a dead agent, since CollectAgentState only probes idle when
// alive) or bailed out on !IsAlive. So `muxcode diagnose <role>` on a crashed
// agent rendered "Health: dead" in red and then reported no issues, exit 0.
func checkAgentDown(report *DiagnosticReport) *DiagnosticFinding {
	if report.AgentState.IsAlive {
		return nil
	}
	if report.AgentState.IsStopped {
		return nil // intentionally stopped — not a fault
	}
	if report.AgentState.IsReloading {
		return nil // expected gap in the stop→reconfigure→relaunch cycle
	}

	evidence := []string{
		fmt.Sprintf("Agent process for %q is not running", report.Role),
		fmt.Sprintf("Provider: %s", report.AgentState.Provider),
	}
	if report.InboxState.ActionableCount > 0 {
		evidence = append(evidence, fmt.Sprintf(
			"%d actionable message(s) waiting — undeliverable until the agent is back",
			report.InboxState.ActionableCount))
	}
	if report.AgentState.PaneLastLine != "" {
		evidence = append(evidence, fmt.Sprintf("Pane last line: %q", report.AgentState.PaneLastLine))
	}

	return &DiagnosticFinding{
		Severity:    "critical",
		FailureMode: "agent-down",
		Summary:     "Agent process is not running and was not stopped or reloaded",
		Evidence:    evidence,
		Remediation: []string{
			fmt.Sprintf("Restart: muxcode agent-health --start %s", report.Role),
			"Check why it exited: muxcode lifecycle show --event agent-down",
			"Inbox is on disk and survives — messages redeliver after restart",
		},
	}
}

// checkReceiptGap detects messages sitting in the inbox with no delivery
// receipt under the delivery-ack cutover. This is the positive-signal
// counterpart to the old wake-event timeline analysis: instead of inferring
// delivery from a daemon log line, it reports what the agent actually
// acknowledged. Mirrors the daemon's own checkPollHealth backstop.
func checkReceiptGap(report *DiagnosticReport) *DiagnosticFinding {
	if !report.NotifyState.AckDelivery {
		return nil // receipts are not the delivery evidence in this mode
	}
	if report.NotifyState.ReceiptGapCount == 0 {
		return nil
	}
	if !report.AgentState.IsAlive {
		return nil // checkAgentDown owns this — a dead agent receipts nothing
	}

	severity := "warning"
	if report.NotifyState.ReceiptGapAge >= diagnoseStuckInboxSecs {
		severity = "critical"
	}

	evidence := []string{
		fmt.Sprintf("%d message(s) un-receipted for over %ds (oldest: %ds)",
			report.NotifyState.ReceiptGapCount, diagnoseReceiptGapSecs, report.NotifyState.ReceiptGapAge),
		"A receipt records that the agent read the message — not that the daemon sent it",
	}
	if report.AgentState.SupportsHooks {
		evidence = append(evidence,
			"Hook provider: the agent's own `muxcode inbox --poll --loop` listener should have acked this")
	} else {
		evidence = append(evidence, fmt.Sprintf(
			"Non-hook provider (%s): delivery relies on verified pane injection",
			report.AgentState.Provider))
	}

	return &DiagnosticFinding{
		Severity:    severity,
		FailureMode: "receipt-gap",
		Summary: fmt.Sprintf("%d message(s) carry no delivery receipt — self-poll or delivery sidecar may be down",
			report.NotifyState.ReceiptGapCount),
		Evidence: evidence,
		Remediation: []string{
			fmt.Sprintf("Force-deliver pending inbox: muxcode deliver %s --force", report.Role),
			"Check the agent's background inbox listener is running",
			"Inspect a specific message's receipt: muxcode track <msg-id>",
		},
	}
}

// failureModeVersionMismatch names the finding raised when the session
// daemon runs a different build from the binary diagnosing it.
const failureModeVersionMismatch = "binary-daemon-version-mismatch"

// checkDaemonVersionMismatch detects a live daemon running a different build
// from this binary — the state every session is in between `make install`
// and `upgrade-daemons`, and the one a session stays in when that rollout
// fails or is skipped: fixes in the installed binary are not live for it. A
// daemon that recorded no identity predates the stamp and is reported the
// same way. Warning, not critical — the daemon is running, just old. Silent
// when the daemon is down (daemon-dead owns that) or when no installed
// identity was collected (a report built without CollectDaemonState).
func checkDaemonVersionMismatch(report *DiagnosticReport) *DiagnosticFinding {
	ds := report.DaemonState
	if !ds.IsAlive || ds.InstalledBuild.Version == "" {
		return nil
	}
	if ds.DaemonBuild != nil && ds.DaemonBuild.SameBuild(ds.InstalledBuild) {
		return nil
	}

	installed := ds.InstalledBuild
	evidence := []string{
		fmt.Sprintf("Installed binary: %s (%s, built %s)", installed.Version, installed.Commit, installed.Date),
	}
	var summary string
	switch {
	case ds.DaemonBuild == nil:
		summary = fmt.Sprintf("Daemon recorded no version — it predates the stamped binary %s", installed.Version)
		evidence = append(evidence, "No daemon.version file — the daemon was launched from a binary older than the version stamp")
	case ds.DaemonBuild.Version == installed.Version:
		summary = fmt.Sprintf("Daemon runs a different build of %s than the installed binary", installed.Version)
		evidence = append(evidence, fmt.Sprintf("Daemon build: %s (%s, built %s)", ds.DaemonBuild.Version, ds.DaemonBuild.Commit, ds.DaemonBuild.Date))
	default:
		summary = fmt.Sprintf("Daemon runs %s but the installed binary is %s — this session is not on the current code", ds.DaemonBuild.Version, installed.Version)
		evidence = append(evidence, fmt.Sprintf("Daemon build: %s (%s, built %s)", ds.DaemonBuild.Version, ds.DaemonBuild.Commit, ds.DaemonBuild.Date))
	}

	return &DiagnosticFinding{
		Severity:    "warning",
		FailureMode: failureModeVersionMismatch,
		Summary:     summary,
		Evidence:    evidence,
		Remediation: []string{
			"Roll the installed binary out to running daemons: muxcode upgrade-daemons",
			"Or rebuild and roll out in one step: ./build.sh",
		},
	}
}

// checkUnexplainedEvidence is the verdict-consistency backstop, and it is the
// reason this file can no longer produce a clean verdict over a broken agent.
//
// Every other check answers "is this specific failure mode present?". None
// answered "does the state I just rendered actually add up to healthy?", so a
// report could gather red evidence, print it, match no known pattern, and
// conclude "No issues detected" with exit 0. That happened three sessions
// running, each time from a different missing detector — which is the tell that
// the bug was structural rather than one absent pattern.
//
// So this asserts the invariant directly: if the agent is holding actionable
// work it has not consumed, SOMETHING is wrong, and diagnose must say so even
// when it cannot name the mode. An honest "unexplained" beats a false clean —
// the caller is asking whether to trust this agent, and "I don't know" and "all
// good" are not the same answer.
//
// Deliberately narrow to stay quiet on healthy sessions: it fires only on
// unconsumed actionable messages past diagnoseStuckInboxSecs, well beyond any
// legitimate mid-turn window, and only when no earlier check spoke.
//
// A version-mismatch warning does not count as having spoken. It is true of
// every session between an install and its rollout, so letting it satisfy
// this check would reopen the false-clean hole for exactly the window in
// which stale daemon code is the likeliest cause of a stuck inbox.
func checkUnexplainedEvidence(report *DiagnosticReport) *DiagnosticFinding {
	for _, f := range report.Findings {
		if f.FailureMode == failureModeVersionMismatch {
			continue
		}
		if f.Severity == "critical" || f.Severity == "warning" {
			return nil // the state is already explained
		}
	}
	if report.InboxState.ActionableCount == 0 {
		return nil
	}
	if report.InboxState.OldestMessageAge < diagnoseStuckInboxSecs {
		return nil
	}

	evidence := []string{
		fmt.Sprintf("%d actionable message(s) unconsumed, oldest %ds ago (threshold %ds)",
			report.InboxState.ActionableCount, report.InboxState.OldestMessageAge, diagnoseStuckInboxSecs),
		fmt.Sprintf("Agent state: alive=%s idle=%s provider=%s",
			boolYesNo(report.AgentState.IsAlive), boolYesNo(report.AgentState.IsIdle),
			report.AgentState.Provider),
		"No known failure mode matched — diagnose cannot explain why these are still queued",
	}
	if report.NotifyState.ReceiptGapCount > 0 {
		evidence = append(evidence, fmt.Sprintf("%d message(s) un-receipted", report.NotifyState.ReceiptGapCount))
	}
	if report.NotifyState.IsMarkerStale {
		evidence = append(evidence, fmt.Sprintf("Notified-IDs marker stale (%ds)", report.NotifyState.MarkerAge))
	}

	return &DiagnosticFinding{
		Severity:    "critical",
		FailureMode: "unexplained-stuck-inbox",
		Summary:     "Messages are stuck with no matching failure mode — diagnose has a coverage gap here",
		Evidence:    evidence,
		Remediation: []string{
			fmt.Sprintf("Force-deliver pending inbox: muxcode deliver %s --force", report.Role),
			fmt.Sprintf("Inspect raw evidence: muxcode diagnose %s --json", report.Role),
			"Please report this state — a new detector is needed for it",
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
		stateStr = fmt.Sprintf("%sactive%s (%sidle prompt in wider capture, not thinking — likely stuck%s)",
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
	deliveryModel := "pane-scrape (delivery-ack rolled back)"
	if report.NotifyState.AckDelivery {
		deliveryModel = "receipt-ack + agent self-poll"
	}
	b.WriteString(fmt.Sprintf("    Delivery model: %s\n", deliveryModel))
	markerAgeStr := "no marker"
	if report.NotifyState.MarkerAge >= 0 {
		markerAgeStr = fmt.Sprintf("%ds", report.NotifyState.MarkerAge)
		// Only red when staleness has a consequence. An old marker on a drained
		// inbox just means nothing has needed delivering lately; painting that
		// red taught readers to discount the colour on the reports where it
		// mattered.
		if report.NotifyState.IsMarkerStale && report.InboxState.ActionableCount > 0 {
			markerAgeStr += fmt.Sprintf(" %s— STALE%s", diagColorRed, diagColorReset)
		} else if report.NotifyState.IsMarkerStale {
			markerAgeStr += fmt.Sprintf(" %s(stale, inbox drained)%s", diagColorComment, diagColorReset)
		}
	}
	b.WriteString(fmt.Sprintf("    Notified IDs: %d (marker age: %s)\n",
		report.NotifyState.NotifiedIDCount, markerAgeStr))
	b.WriteString(fmt.Sprintf("    Unnotified: %d message(s)\n", report.NotifyState.UnnotifiedCount))
	if report.NotifyState.ReceiptGapCount > 0 {
		b.WriteString(fmt.Sprintf("    %sUn-receipted: %d message(s) (oldest %ds)%s\n",
			diagColorRed, report.NotifyState.ReceiptGapCount, report.NotifyState.ReceiptGapAge, diagColorReset))
	}
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
	versionStr := ""
	if report.DaemonState.DaemonBuild != nil {
		versionStr = ", version: " + report.DaemonState.DaemonBuild.Version
	}
	b.WriteString(fmt.Sprintf("\n  %sDaemon:%s %s (keepalive: %s%s)\n",
		diagColorComment, diagColorReset, daemonStr, keepaliveStr, versionStr))

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
