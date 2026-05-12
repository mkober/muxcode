package bus

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// setupTestBusDir creates a temporary bus directory for testing and sets the
// override so BusDir uses it. Returns session name and cleanup function.
func setupTestBusDir(t *testing.T) (string, func()) {
	t.Helper()
	tmpDir := t.TempDir()
	SetBusDirBase(tmpDir)
	session := "test-diagnose"
	busDir := BusDir(session)
	os.MkdirAll(filepath.Join(busDir, "inbox"), 0755)
	os.MkdirAll(filepath.Join(busDir, "lock"), 0755)
	return session, func() { ResetBusDirBase() }
}

// writeTestInbox writes messages to a role's inbox for testing.
func writeTestInbox(t *testing.T, session, role string, msgs []Message) {
	t.Helper()
	path := InboxPath(session, role)
	os.MkdirAll(filepath.Dir(path), 0755)
	var lines []byte
	for _, m := range msgs {
		data, _ := json.Marshal(m)
		lines = append(lines, data...)
		lines = append(lines, '\n')
	}
	os.WriteFile(path, lines, 0644)
}

// writeTestKeepalive writes a keepalive timestamp for testing.
func writeTestKeepalive(t *testing.T, session string, ageSeconds int64) {
	t.Helper()
	ts := time.Now().Unix() - ageSeconds
	os.WriteFile(DaemonKeepalivePath(session), []byte(strconv.FormatInt(ts, 10)), 0644)
}

// --- Phase 1: Evidence collector tests ---

func TestCollectInboxState_Empty(t *testing.T) {
	session, cleanup := setupTestBusDir(t)
	defer cleanup()

	ev := CollectInboxState(session, "commit")
	if ev.MessageCount != 0 {
		t.Errorf("expected 0 messages, got %d", ev.MessageCount)
	}
	if ev.ActionableCount != 0 {
		t.Errorf("expected 0 actionable, got %d", ev.ActionableCount)
	}
}

func TestCollectInboxState_WithMessages(t *testing.T) {
	session, cleanup := setupTestBusDir(t)
	defer cleanup()

	msgs := []Message{
		{ID: "1", TS: time.Now().Unix() - 30, From: "plan", To: "commit", Type: "request", Action: "commit", Payload: "Stage and commit"},
		{ID: "2", TS: time.Now().Unix() - 10, From: "daemon", To: "commit", Type: "event", Action: "compact-recommended", Payload: "Context high"},
	}
	writeTestInbox(t, session, "commit", msgs)

	ev := CollectInboxState(session, "commit")
	if ev.MessageCount != 2 {
		t.Errorf("expected 2 messages, got %d", ev.MessageCount)
	}
	if ev.ActionableCount != 1 {
		t.Errorf("expected 1 actionable, got %d", ev.ActionableCount)
	}
	if ev.OldestMessageAge < 28 || ev.OldestMessageAge > 35 {
		t.Errorf("unexpected oldest age: %d", ev.OldestMessageAge)
	}
	if len(ev.Messages) != 2 {
		t.Errorf("expected 2 summaries, got %d", len(ev.Messages))
	}
}

func TestCollectNotifyState_NoMarker(t *testing.T) {
	session, cleanup := setupTestBusDir(t)
	defer cleanup()

	ev := CollectNotifyState(session, "commit")
	if ev.MarkerAge != -1 {
		t.Errorf("expected marker age -1, got %d", ev.MarkerAge)
	}
	if ev.IsMarkerStale {
		t.Error("expected marker not stale when no marker exists")
	}
	if ev.NotifiedIDCount != 0 {
		t.Errorf("expected 0 notified IDs, got %d", ev.NotifiedIDCount)
	}
}

func TestCollectNotifyState_WithMarker(t *testing.T) {
	session, cleanup := setupTestBusDir(t)
	defer cleanup()

	// Write notified IDs
	writeNotifiedIDs(session, "commit", map[string]bool{"msg-1": true, "msg-2": true})

	ev := CollectNotifyState(session, "commit")
	if ev.NotifiedIDCount != 2 {
		t.Errorf("expected 2 notified IDs, got %d", ev.NotifiedIDCount)
	}
	if ev.MarkerAge < 0 {
		t.Error("expected marker age >= 0 for fresh marker")
	}
}

func TestCollectNotifyState_StaleMarker(t *testing.T) {
	session, cleanup := setupTestBusDir(t)
	defer cleanup()

	// Write notified IDs, then backdate the file
	markerPath := notifiedIDsPath(session, "commit")
	os.WriteFile(markerPath, []byte("msg-1\n"), 0644)
	os.Chtimes(markerPath, time.Now().Add(-30*time.Second), time.Now().Add(-30*time.Second))

	ev := CollectNotifyState(session, "commit")
	if !ev.IsMarkerStale {
		t.Error("expected marker to be stale at 30s age")
	}
	if ev.MarkerAge < 28 {
		t.Errorf("expected marker age >= 28, got %d", ev.MarkerAge)
	}
}

func TestCollectDaemonState_Alive(t *testing.T) {
	session, cleanup := setupTestBusDir(t)
	defer cleanup()

	writeTestKeepalive(t, session, 3) // 3 seconds ago

	ev := CollectDaemonState(session)
	if !ev.IsAlive {
		t.Error("expected daemon alive with 3s keepalive")
	}
	if ev.KeepaliveAge < 2 || ev.KeepaliveAge > 5 {
		t.Errorf("unexpected keepalive age: %d", ev.KeepaliveAge)
	}
	if ev.IsKeepaliveStale {
		t.Error("expected keepalive not stale")
	}
}

func TestCollectDaemonState_Stale(t *testing.T) {
	session, cleanup := setupTestBusDir(t)
	defer cleanup()

	writeTestKeepalive(t, session, 60) // 60 seconds ago

	ev := CollectDaemonState(session)
	if ev.IsAlive {
		t.Error("expected daemon not alive with 60s keepalive")
	}
	if !ev.IsKeepaliveStale {
		t.Error("expected keepalive stale")
	}
}

func TestCollectDaemonState_Missing(t *testing.T) {
	session, cleanup := setupTestBusDir(t)
	defer cleanup()

	ev := CollectDaemonState(session)
	if ev.IsAlive {
		t.Error("expected daemon not alive with missing keepalive")
	}
	if ev.KeepaliveAge != -1 {
		t.Errorf("expected keepalive age -1, got %d", ev.KeepaliveAge)
	}
}

// --- Phase 2: Timeline tests ---

func TestBuildTimeline_AnnotatesGaps(t *testing.T) {
	events := []TimelineEvent{
		{Timestamp: 100, Event: "inbox-notify", Detail: "commit"},
		{Timestamp: 115, Event: "idle-task-rescue", Detail: "commit"},
	}
	annotateGaps(events, "commit")

	if events[1].GapNote == "" {
		t.Error("expected gap note on event after inbox-notify without idle-wake")
	}
	if events[1].GapSecs != 15 {
		t.Errorf("expected 15s gap, got %d", events[1].GapSecs)
	}
}

func TestBuildTimeline_NoGapWhenWakeFollows(t *testing.T) {
	events := []TimelineEvent{
		{Timestamp: 100, Event: "inbox-notify", Detail: "commit"},
		{Timestamp: 102, Event: "idle-wake", Detail: "commit"},
	}
	annotateGaps(events, "commit")

	if events[1].GapNote != "" {
		t.Errorf("expected no gap note, got %q", events[1].GapNote)
	}
	if events[1].GapSecs != 2 {
		t.Errorf("expected 2s gap, got %d", events[1].GapSecs)
	}
}

func TestCountRepeatedFailures(t *testing.T) {
	events := []TimelineEvent{
		{Timestamp: 100, Event: "inbox-notify", Detail: "commit"},
		{Timestamp: 130, Event: "idle-task-rescue", Detail: "commit"}, // 30s gap — no wake
		{Timestamp: 200, Event: "inbox-notify", Detail: "commit"},
		{Timestamp: 202, Event: "idle-wake", Detail: "commit"}, // OK
		{Timestamp: 300, Event: "inbox-notify", Detail: "commit"},
		// No wake follows — end of timeline
	}
	count := CountRepeatedFailures(events)
	if count != 2 {
		t.Errorf("expected 2 failures, got %d", count)
	}
}

func TestCountRepeatedFailures_AllOK(t *testing.T) {
	events := []TimelineEvent{
		{Timestamp: 100, Event: "inbox-notify", Detail: "commit"},
		{Timestamp: 102, Event: "idle-wake", Detail: "commit"},
		{Timestamp: 200, Event: "inbox-notify", Detail: "commit"},
		{Timestamp: 201, Event: "idle-wake", Detail: "commit"},
	}
	count := CountRepeatedFailures(events)
	if count != 0 {
		t.Errorf("expected 0 failures, got %d", count)
	}
}

// --- Phase 3: Diagnostic check tests ---

func TestCheckDaemonDead_Stale(t *testing.T) {
	report := &DiagnosticReport{
		Role: "commit",
		DaemonState: DaemonStateEvidence{
			IsAlive:          false,
			KeepaliveAge:     60,
			IsKeepaliveStale: true,
		},
	}
	finding := checkDaemonDead(report)
	if finding == nil {
		t.Fatal("expected finding for stale keepalive")
	}
	if finding.FailureMode != "daemon-dead" {
		t.Errorf("expected daemon-dead, got %s", finding.FailureMode)
	}
	if finding.Severity != "critical" {
		t.Errorf("expected critical, got %s", finding.Severity)
	}
}

func TestCheckDaemonDead_Alive(t *testing.T) {
	report := &DiagnosticReport{
		DaemonState: DaemonStateEvidence{
			IsAlive:          true,
			KeepaliveAge:     3,
			IsKeepaliveStale: false,
		},
	}
	finding := checkDaemonDead(report)
	if finding != nil {
		t.Error("expected no finding for alive daemon")
	}
}

func TestCheckStaleNotifiedIDs_Detected(t *testing.T) {
	report := &DiagnosticReport{
		Role:       "commit",
		AgentState: AgentStateEvidence{IsIdle: true},
		NotifyState: NotifyStateEvidence{
			UnnotifiedCount: 1,
			MarkerAge:       20,
			IsMarkerStale:   true,
		},
	}
	finding := checkStaleNotifiedIDs(report)
	if finding == nil {
		t.Fatal("expected finding for stale notified IDs")
	}
	if finding.FailureMode != "stale-notified-ids" {
		t.Errorf("expected stale-notified-ids, got %s", finding.FailureMode)
	}
}

func TestCheckStaleNotifiedIDs_NotIdle(t *testing.T) {
	report := &DiagnosticReport{
		AgentState: AgentStateEvidence{IsIdle: false},
		NotifyState: NotifyStateEvidence{
			UnnotifiedCount: 1,
			IsMarkerStale:   true,
		},
	}
	finding := checkStaleNotifiedIDs(report)
	if finding != nil {
		t.Error("expected no finding when agent is not idle")
	}
}

func TestCheckMissedSendKeys_Detected(t *testing.T) {
	report := &DiagnosticReport{
		Role:       "commit",
		AgentState: AgentStateEvidence{IsIdle: true},
		InboxState: InboxStateEvidence{ActionableCount: 1},
		Timeline: []TimelineEvent{
			{Event: "idle-wake", Detail: "commit"},
		},
	}
	finding := checkMissedSendKeys(report)
	if finding == nil {
		t.Fatal("expected finding for missed send-keys")
	}
	if finding.FailureMode != "missed-send-keys" {
		t.Errorf("expected missed-send-keys, got %s", finding.FailureMode)
	}
}

func TestCheckMissedSendKeys_NotDetected_NoWake(t *testing.T) {
	report := &DiagnosticReport{
		AgentState: AgentStateEvidence{IsIdle: true},
		InboxState: InboxStateEvidence{ActionableCount: 1},
		Timeline:   []TimelineEvent{}, // no idle-wake
	}
	finding := checkMissedSendKeys(report)
	if finding != nil {
		t.Error("expected no finding without idle-wake in timeline")
	}
}

func TestCheckIdleDetectionFailure_Detected(t *testing.T) {
	report := &DiagnosticReport{
		Role: "commit",
		AgentState: AgentStateEvidence{
			IsIdle:       false,
			IsAlive:      true,
			PaneLastLine: "❯ ",
			Provider:     "claude",
		},
	}
	finding := checkIdleDetectionFailure(report)
	if finding == nil {
		t.Fatal("expected finding for idle detection failure")
	}
	if finding.FailureMode != "idle-detection-failure" {
		t.Errorf("expected idle-detection-failure, got %s", finding.FailureMode)
	}
}

func TestCheckIdleDetectionFailure_NotDetected_IsIdle(t *testing.T) {
	report := &DiagnosticReport{
		AgentState: AgentStateEvidence{
			IsIdle:       true,
			IsAlive:      true,
			PaneLastLine: "❯ ",
		},
	}
	finding := checkIdleDetectionFailure(report)
	if finding != nil {
		t.Error("expected no finding when IsIdle is true")
	}
}

func TestCheckDaemonNotWaking_Detected(t *testing.T) {
	report := &DiagnosticReport{
		Role:       "commit",
		AgentState: AgentStateEvidence{IsIdle: true},
		InboxState: InboxStateEvidence{ActionableCount: 2},
		Timeline: []TimelineEvent{
			{Timestamp: 100, Event: "inbox-notify", Detail: "commit"},
			{Timestamp: 130, Event: "idle-task-rescue", Detail: "commit"},
		},
	}
	finding := checkDaemonNotWaking(report)
	if finding == nil {
		t.Fatal("expected finding for daemon not waking")
	}
	if finding.FailureMode != "daemon-not-waking" {
		t.Errorf("expected daemon-not-waking, got %s", finding.FailureMode)
	}
	if finding.Severity != "critical" {
		t.Errorf("expected critical, got %s", finding.Severity)
	}
}

func TestCheckDaemonNotWaking_NotDetected_WakeFollows(t *testing.T) {
	report := &DiagnosticReport{
		AgentState: AgentStateEvidence{IsIdle: true},
		InboxState: InboxStateEvidence{ActionableCount: 1},
		Timeline: []TimelineEvent{
			{Timestamp: 100, Event: "inbox-notify", Detail: "commit"},
			{Timestamp: 102, Event: "idle-wake", Detail: "commit"},
		},
	}
	finding := checkDaemonNotWaking(report)
	if finding != nil {
		t.Error("expected no finding when idle-wake follows inbox-notify")
	}
}

func TestCheckPostRestartWakeGap_Detected(t *testing.T) {
	report := &DiagnosticReport{
		Role:       "commit",
		AgentState: AgentStateEvidence{IsIdle: true},
		InboxState: InboxStateEvidence{ActionableCount: 1},
		Timeline: []TimelineEvent{
			{Event: "idle-transition", Detail: "commit"},
			// No idle-wake follows
		},
	}
	finding := checkPostRestartWakeGap(report)
	if finding == nil {
		t.Fatal("expected finding for post-restart wake gap")
	}
	if finding.FailureMode != "post-restart-wake-gap" {
		t.Errorf("expected post-restart-wake-gap, got %s", finding.FailureMode)
	}
}

func TestCheckPostRestartWakeGap_NotDetected_WakeFollows(t *testing.T) {
	report := &DiagnosticReport{
		AgentState: AgentStateEvidence{IsIdle: true},
		InboxState: InboxStateEvidence{ActionableCount: 1},
		Timeline: []TimelineEvent{
			{Event: "idle-transition", Detail: "commit"},
			{Event: "idle-wake", Detail: "commit"},
		},
	}
	finding := checkPostRestartWakeGap(report)
	if finding != nil {
		t.Error("expected no finding when idle-wake follows idle-transition")
	}
}

func TestCheckProviderMismatch_Detected(t *testing.T) {
	report := &DiagnosticReport{
		Role: "commit",
		AgentState: AgentStateEvidence{
			SupportsHooks: false,
			Provider:      "opencode",
		},
		InboxState: InboxStateEvidence{ActionableCount: 1},
		NotifyState: NotifyStateEvidence{
			MarkerAge: -1, // no marker — daemon never tried
		},
	}
	finding := checkProviderMismatch(report)
	if finding == nil {
		t.Fatal("expected finding for provider mismatch")
	}
	if finding.FailureMode != "provider-mismatch" {
		t.Errorf("expected provider-mismatch, got %s", finding.FailureMode)
	}
}

func TestCheckProviderMismatch_NotDetected_HookProvider(t *testing.T) {
	report := &DiagnosticReport{
		AgentState: AgentStateEvidence{
			SupportsHooks: true,
			Provider:      "claude",
		},
		InboxState: InboxStateEvidence{ActionableCount: 1},
	}
	finding := checkProviderMismatch(report)
	if finding != nil {
		t.Error("expected no finding for hook provider")
	}
}

func TestCheckReloadMarkerStuck_Detected(t *testing.T) {
	report := &DiagnosticReport{
		Role:       "commit",
		Session:    "muxcode",
		AgentState: AgentStateEvidence{IsReloading: true},
	}
	finding := checkReloadMarkerStuck(report)
	if finding == nil {
		t.Fatal("expected finding for stuck reload marker")
	}
	if finding.FailureMode != "reload-marker-stuck" {
		t.Errorf("expected reload-marker-stuck, got %s", finding.FailureMode)
	}
	if finding.Severity != "critical" {
		t.Errorf("expected critical, got %s", finding.Severity)
	}
}

func TestCheckReloadMarkerStuck_NotDetected(t *testing.T) {
	report := &DiagnosticReport{
		AgentState: AgentStateEvidence{IsReloading: false},
	}
	finding := checkReloadMarkerStuck(report)
	if finding != nil {
		t.Error("expected no finding when not reloading")
	}
}

func TestCheckPendingInputBlocking_Detected(t *testing.T) {
	report := &DiagnosticReport{
		Role: "commit",
		AgentState: AgentStateEvidence{
			HasPendingInput: true,
			PaneLastLine:    "❯ some partial input",
		},
		InboxState: InboxStateEvidence{ActionableCount: 1},
	}
	finding := checkPendingInputBlocking(report)
	if finding == nil {
		t.Fatal("expected finding for pending input blocking")
	}
	if finding.FailureMode != "pending-input-blocking" {
		t.Errorf("expected pending-input-blocking, got %s", finding.FailureMode)
	}
}

func TestCheckPendingInputBlocking_NotDetected_NoInput(t *testing.T) {
	report := &DiagnosticReport{
		AgentState: AgentStateEvidence{HasPendingInput: false},
		InboxState: InboxStateEvidence{ActionableCount: 1},
	}
	finding := checkPendingInputBlocking(report)
	if finding != nil {
		t.Error("expected no finding when no pending input")
	}
}

func TestCheckNoActionableMessages_Detected(t *testing.T) {
	report := &DiagnosticReport{
		Role: "commit",
		InboxState: InboxStateEvidence{
			MessageCount:    2,
			ActionableCount: 0,
		},
	}
	finding := checkNoActionableMessages(report)
	if finding == nil {
		t.Fatal("expected finding for no actionable messages")
	}
	if finding.FailureMode != "no-actionable-messages" {
		t.Errorf("expected no-actionable-messages, got %s", finding.FailureMode)
	}
	if finding.Severity != "info" {
		t.Errorf("expected info severity, got %s", finding.Severity)
	}
}

func TestCheckNoActionableMessages_NotDetected_HasActionable(t *testing.T) {
	report := &DiagnosticReport{
		InboxState: InboxStateEvidence{
			MessageCount:    2,
			ActionableCount: 1,
		},
	}
	finding := checkNoActionableMessages(report)
	if finding != nil {
		t.Error("expected no finding when actionable messages exist")
	}
}

func TestCheckNoActionableMessages_NotDetected_EmptyInbox(t *testing.T) {
	report := &DiagnosticReport{
		InboxState: InboxStateEvidence{
			MessageCount:    0,
			ActionableCount: 0,
		},
	}
	finding := checkNoActionableMessages(report)
	if finding != nil {
		t.Error("expected no finding for empty inbox")
	}
}

// --- Phase 3: RunDiagnostics integration test ---

func TestRunDiagnostics_MultipleFindings(t *testing.T) {
	report := &DiagnosticReport{
		Role: "commit",
		AgentState: AgentStateEvidence{
			IsIdle:          true,
			IsAlive:         true,
			HasPendingInput: false,
		},
		InboxState: InboxStateEvidence{
			MessageCount:    3,
			ActionableCount: 0,
		},
		NotifyState: NotifyStateEvidence{
			UnnotifiedCount: 1,
			MarkerAge:       20,
			IsMarkerStale:   true,
		},
		DaemonState: DaemonStateEvidence{
			IsAlive:          true,
			KeepaliveAge:     3,
			IsKeepaliveStale: false,
		},
	}
	RunDiagnostics(report)

	// Should find: stale-notified-ids and no-actionable-messages
	if len(report.Findings) < 2 {
		t.Errorf("expected at least 2 findings, got %d", len(report.Findings))
	}

	modes := make(map[string]bool)
	for _, f := range report.Findings {
		modes[f.FailureMode] = true
	}
	if !modes["stale-notified-ids"] {
		t.Error("expected stale-notified-ids finding")
	}
	if !modes["no-actionable-messages"] {
		t.Error("expected no-actionable-messages finding")
	}
}

func TestRunDiagnostics_NoFindings(t *testing.T) {
	report := &DiagnosticReport{
		Role: "commit",
		AgentState: AgentStateEvidence{
			IsIdle:  true,
			IsAlive: true,
		},
		InboxState: InboxStateEvidence{
			MessageCount:    0,
			ActionableCount: 0,
		},
		DaemonState: DaemonStateEvidence{
			IsAlive:          true,
			KeepaliveAge:     3,
			IsKeepaliveStale: false,
		},
	}
	RunDiagnostics(report)

	if len(report.Findings) != 0 {
		t.Errorf("expected 0 findings for healthy agent, got %d", len(report.Findings))
		for _, f := range report.Findings {
			t.Logf("  unexpected: %s — %s", f.FailureMode, f.Summary)
		}
	}
}

// --- Phase 4: Formatting tests ---

func TestFormatDiagnosticReport_Structure(t *testing.T) {
	report := &DiagnosticReport{
		Role:      "commit",
		Timestamp: time.Now().Unix(),
		AgentState: AgentStateEvidence{
			IsIdle:        true,
			IsAlive:       true,
			Provider:      "claude",
			SupportsHooks: true,
			PaneLastLine:  "❯",
		},
		InboxState: InboxStateEvidence{
			MessageCount:    1,
			ActionableCount: 1,
			Messages: []MessageSummary{
				{ID: "1", From: "plan", Action: "commit", AgeSecs: 10, Preview: "Stage changes"},
			},
		},
		NotifyState: NotifyStateEvidence{
			NotifiedIDCount: 1,
			MarkerAge:       5,
		},
		DaemonState: DaemonStateEvidence{
			IsAlive:      true,
			KeepaliveAge: 3,
		},
		Findings: []DiagnosticFinding{
			{
				Severity:    "warning",
				FailureMode: "missed-send-keys",
				Summary:     "Test finding",
				Evidence:    []string{"evidence line"},
				Remediation: []string{"fix it"},
			},
		},
	}

	output := FormatDiagnosticReport(report)

	// Check key sections are present
	checks := []string{
		"Agent:", "commit", "claude",
		"State:", "idle",
		"Health:", "alive",
		"Inbox:", "1 message",
		"Notification state:",
		"Daemon:",
		"FINDING:", "missed-send-keys",
		"Evidence:", "evidence line",
		"Remediation:", "fix it",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Errorf("expected output to contain %q", check)
		}
	}
}

func TestFormatDiagnosticReport_NoFindings(t *testing.T) {
	report := &DiagnosticReport{
		Role: "commit",
		AgentState: AgentStateEvidence{
			IsIdle:   true,
			IsAlive:  true,
			Provider: "claude",
		},
		DaemonState: DaemonStateEvidence{IsAlive: true, KeepaliveAge: 3},
	}
	output := FormatDiagnosticReport(report)
	if !strings.Contains(output, "No issues detected") {
		t.Error("expected 'No issues detected' for empty findings")
	}
}

func TestFormatDiagnosticJSON_Valid(t *testing.T) {
	report := &DiagnosticReport{
		Role:      "commit",
		Timestamp: time.Now().Unix(),
		AgentState: AgentStateEvidence{
			IsIdle:   true,
			IsAlive:  true,
			Provider: "claude",
		},
		Findings: []DiagnosticFinding{
			{
				Severity:    "critical",
				FailureMode: "daemon-dead",
				Summary:     "Daemon is dead",
				Evidence:    []string{"keepalive stale"},
				Remediation: []string{"restart daemon"},
			},
		},
	}

	jsonStr := FormatDiagnosticJSON(report)

	// Verify it's valid JSON
	var parsed DiagnosticReport
	if err := json.Unmarshal([]byte(jsonStr), &parsed); err != nil {
		t.Fatalf("invalid JSON output: %v", err)
	}
	if parsed.Role != "commit" {
		t.Errorf("expected role commit, got %s", parsed.Role)
	}
	if len(parsed.Findings) != 1 {
		t.Errorf("expected 1 finding, got %d", len(parsed.Findings))
	}
}

func TestFormatDiagnosticSummary_Healthy(t *testing.T) {
	report := &DiagnosticReport{
		Role:       "commit",
		AgentState: AgentStateEvidence{IsIdle: true, IsAlive: true},
		InboxState: InboxStateEvidence{MessageCount: 0},
	}
	summary := FormatDiagnosticSummary(report)
	if !strings.Contains(summary, "commit") {
		t.Error("expected role in summary")
	}
	if !strings.Contains(summary, "healthy") {
		t.Error("expected 'healthy' in summary for no findings")
	}
}

func TestFormatDiagnosticSummary_Critical(t *testing.T) {
	report := &DiagnosticReport{
		Role:       "commit",
		AgentState: AgentStateEvidence{IsAlive: false},
		InboxState: InboxStateEvidence{MessageCount: 3},
		Findings: []DiagnosticFinding{
			{Severity: "critical", FailureMode: "daemon-dead"},
			{Severity: "warning", FailureMode: "stale-notified-ids"},
		},
	}
	summary := FormatDiagnosticSummary(report)
	if !strings.Contains(summary, "1 critical") {
		t.Error("expected '1 critical' in summary")
	}
	if !strings.Contains(summary, "1 warning") {
		t.Error("expected '1 warning' in summary")
	}
}

func TestDiagnosableRoles(t *testing.T) {
	roles := DiagnosableRoles()
	if len(roles) == 0 {
		t.Fatal("expected at least one diagnosable role")
	}

	// Should include main agent roles
	roleMap := make(map[string]bool)
	for _, r := range roles {
		roleMap[r] = true
	}
	for _, expected := range []string{"edit", "build", "test", "commit", "review"} {
		if !roleMap[expected] {
			t.Errorf("expected %s in diagnosable roles", expected)
		}
	}

	// Should exclude non-agent roles
	for _, excluded := range []string{"webhook", "api", "docs", "pr-read"} {
		if roleMap[excluded] {
			t.Errorf("expected %s excluded from diagnosable roles", excluded)
		}
	}
}

func TestBoolYesNo(t *testing.T) {
	if boolYesNo(true) != "yes" {
		t.Error("expected yes for true")
	}
	if boolYesNo(false) != "no" {
		t.Error("expected no for false")
	}
}
