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

// writeTestLifecycleLog writes lifecycle entries verbatim, preserving their
// timestamps. LogLifecycle always stamps time.Now(), so gap analysis — which is
// entirely about the spacing between events — cannot be exercised through it.
func writeTestLifecycleLog(t *testing.T, session string, entries []LifecycleEntry) {
	t.Helper()
	path := LifecycleLogPath(session)
	os.MkdirAll(filepath.Dir(path), 0755)
	var buf []byte
	for _, e := range entries {
		data, err := json.Marshal(e)
		if err != nil {
			t.Fatalf("marshal lifecycle entry: %v", err)
		}
		buf = append(buf, data...)
		buf = append(buf, '\n')
	}
	if err := os.WriteFile(path, buf, 0644); err != nil {
		t.Fatalf("write lifecycle log: %v", err)
	}
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

func TestCollectDaemonState_ReadsRecordedBuild(t *testing.T) {
	session, cleanup := setupTestBusDir(t)
	defer cleanup()
	writeTestKeepalive(t, session, 2)

	ev := CollectDaemonState(session)
	if ev.DaemonBuild != nil {
		t.Errorf("expected no daemon build before the daemon records one, got %+v", *ev.DaemonBuild)
	}
	if ev.InstalledBuild.Version == "" {
		t.Error("installed build must always be collected")
	}

	recorded := Info{Version: "v0.0.1-test", Commit: "0000000", Date: "2026-01-01T00:00:00Z"}
	if err := WriteDaemonVersion(session, recorded); err != nil {
		t.Fatal(err)
	}
	ev = CollectDaemonState(session)
	if ev.DaemonBuild == nil || *ev.DaemonBuild != recorded {
		t.Errorf("expected recorded build %+v, got %+v", recorded, ev.DaemonBuild)
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

// TestCheckDaemonVersionMismatch covers the three stale shapes and, as
// negative controls, the three states in which the finding must stay quiet —
// a detector that always fires would pass the positive cases alone.
func TestCheckDaemonVersionMismatch(t *testing.T) {
	installed := Info{Version: "v0.2.0", Commit: "def5678", Date: "2026-09-02T13:00:00Z"}
	older := Info{Version: "v0.1.0", Commit: "abc1234", Date: "2026-09-01T10:00:00Z"}
	rebuilt := Info{Version: "v0.2.0", Commit: "def5678", Date: "2026-09-02T12:00:00Z"}
	same := installed

	cases := []struct {
		name       string
		state      DaemonStateEvidence
		want       bool
		summaryHas string
	}{
		{"stale version", DaemonStateEvidence{IsAlive: true, DaemonBuild: &older, InstalledBuild: installed}, true, "v0.1.0 but the installed binary is v0.2.0"},
		{"same version different build", DaemonStateEvidence{IsAlive: true, DaemonBuild: &rebuilt, InstalledBuild: installed}, true, "different build of v0.2.0"},
		{"unstamped daemon", DaemonStateEvidence{IsAlive: true, InstalledBuild: installed}, true, "recorded no version"},
		{"same build", DaemonStateEvidence{IsAlive: true, DaemonBuild: &same, InstalledBuild: installed}, false, ""},
		{"daemon dead", DaemonStateEvidence{IsAlive: false, IsKeepaliveStale: true, DaemonBuild: &older, InstalledBuild: installed}, false, ""},
		{"no installed evidence", DaemonStateEvidence{IsAlive: true, DaemonBuild: &older}, false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := checkDaemonVersionMismatch(&DiagnosticReport{Role: "build", DaemonState: c.state})
			if !c.want {
				if f != nil {
					t.Fatalf("expected no finding, got %s: %s", f.FailureMode, f.Summary)
				}
				return
			}
			if f == nil {
				t.Fatal("expected a finding")
			}
			if f.FailureMode != "binary-daemon-version-mismatch" {
				t.Errorf("failure mode %q", f.FailureMode)
			}
			if f.Severity != "warning" {
				t.Errorf("severity %q, want warning — the daemon is running, just old", f.Severity)
			}
			if !strings.Contains(f.Summary, c.summaryHas) {
				t.Errorf("summary %q lacks %q", f.Summary, c.summaryHas)
			}
			if !strings.Contains(strings.Join(f.Remediation, "\n"), "muxcode upgrade-daemons") {
				t.Errorf("remediation should name upgrade-daemons: %v", f.Remediation)
			}
		})
	}
}

// TestRunDiagnostics_VersionMismatchOnly runs the full check list over a
// healthy agent under a stale daemon (exactly the mismatch warning) and the
// identical report under a current daemon (nothing) — the negative control.
func TestRunDiagnostics_VersionMismatchOnly(t *testing.T) {
	installed := Info{Version: "v0.2.0", Commit: "def5678", Date: "2026-09-02T13:00:00Z"}
	older := Info{Version: "v0.1.0", Commit: "abc1234", Date: "2026-09-01T10:00:00Z"}
	healthy := func(build Info) *DiagnosticReport {
		return &DiagnosticReport{
			Role:        "commit",
			AgentState:  AgentStateEvidence{IsIdle: true, IsAlive: true},
			DaemonState: DaemonStateEvidence{IsAlive: true, KeepaliveAge: 3, DaemonBuild: &build, InstalledBuild: installed},
		}
	}

	stale := healthy(older)
	RunDiagnostics(stale)
	if len(stale.Findings) != 1 || stale.Findings[0].FailureMode != "binary-daemon-version-mismatch" {
		t.Errorf("expected exactly the mismatch finding, got %+v", stale.Findings)
	}

	current := healthy(installed)
	RunDiagnostics(current)
	if len(current.Findings) != 0 {
		t.Errorf("matching builds must produce no finding, got %+v", current.Findings)
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
	// WiderCaptureIdle carries the signal now: it means "the 200-line capture
	// found an idle prompt AND no thinking indicator" (PaneShowsRecoverableIdle).
	//
	// This fixture previously relied on PaneLastLine alone, which cannot happen
	// in production — CollectAgentState always computes WiderCaptureIdle when
	// !IsIdle && IsAlive, and a pane whose last line is "❯ " is necessarily
	// inside that capture. The only real state matching the old fixture was a
	// THINKING agent, and firing a critical finding on one is the false positive
	// this detector now exists to avoid. The assertion is unchanged; only the
	// fixture moved to the field that actually carries the meaning.
	report := &DiagnosticReport{
		Role: "commit",
		AgentState: AgentStateEvidence{
			IsIdle:           false,
			IsAlive:          true,
			WiderCaptureIdle: true,
			PaneLastLine:     "❯ ",
			Provider:         "claude",
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

func TestPaneShowsRecoverableIdle_ThinkingAgentIsNotRecoverable(t *testing.T) {
	// Captured verbatim from a live plan agent that diagnose reported as
	// "❯ found in wider capture — likely idle" and flagged CRITICAL
	// idle-detection-failure. The agent was thinking the whole time; ❯ is
	// Claude Code's input box, which renders during a turn as well as at rest.
	//
	// The finding's own remediation is `deliver --force`, which injects into
	// the running turn and kills it — so the false positive did not merely
	// mislead, it destroyed the work it was called to rescue.
	content := "⏺ Now updating the spec status to reflect that Phase 1 is underway:\n" +
		"\n" +
		"  Reading 1 file…\n" +
		"  ⎿  docs/requirements/drafts/PBP1-4917-canvas-course-faculty-reconcile-report.md\n" +
		"\n" +
		"✻ Fermenting… (39s · ↓ 2.7k tokens · thinking with xhigh effort)\n" +
		"\n" +
		"─────────────────────────────── planner ──\n" +
		"❯ \n" +
		"──────────────────────────────────────────\n" +
		"  ⏵⏵ bypass permissions on (shift+tab to cycle) · esc to interrupt · ← for agents\n"

	if PaneShowsRecoverableIdle(content) {
		t.Error("a thinking agent must NOT be reported as a recoverable idle prompt — " +
			"this is the false positive that drove force-delivery into a running turn")
	}
}

func TestPaneShowsRecoverableIdle_ParkedTextWedgeIsRecoverable(t *testing.T) {
	// The genuine wedge diagnose exists to catch: no thinking indicator, and a
	// prompt carrying dropped-Enter residue. The agent is idle and deliverable,
	// so this must still report true — the fix must not blind the detector.
	content := "✻ Cooked for 1m 47s\n" +
		"\n" +
		"─────────────────────────────── planner ──\n" +
		"❯ New message from edit [request:update-docs]: fix the NCAT prefix\n" +
		"──────────────────────────────────────────\n" +
		"  ⏵⏵ bypass permissions on (shift+tab to cycle) · ← for agents\n"

	if !PaneShowsRecoverableIdle(content) {
		t.Error("a parked-text wedge at an idle prompt must still be detected")
	}
}

func TestPaneShowsRecoverableIdle_NoPromptIsNotRecoverable(t *testing.T) {
	content := "⏺ Running the build…\n  ⎿  $ ./build.sh\n"
	if PaneShowsRecoverableIdle(content) {
		t.Error("pane with no idle prompt must not be reported as recoverable idle")
	}
}

func TestPaneShowsRecoverableIdle_StaleThinkingInScrollbackIsRecoverable(t *testing.T) {
	// The wedge that stranded the plan agent: the daemon's watchdog uses a WIDE
	// (200-line) capture to find a ❯ that scrolled past the narrow window, but
	// that same wide window sweeps up thinking signatures from SCROLLBACK — a
	// completed turn's spinner, or, here, the agent's own output literally
	// discussing "esc to interrupt" while writing about idle detection. Those are
	// history, not the current state; the live tail is a clean idle prompt. This
	// must be recoverable (true) — before the tail-scoped thinking check it
	// returned false and the watchdog never fired, so the pending message sat
	// undelivered until a human ran `deliver --force`.
	content := "" +
		"⏺ The daemon reads 'esc to interrupt' as a thinking indicator.\n" +
		"  ⎿  isClaudeThinking matches 'esc to interrupt' and '… · ' counters.\n" +
		"✻ Fermenting… (2m 56s · 11.4k tokens · esc to interrupt)\n" + // stale spinner in scrollback
		"⏺ Done. The spec now documents the F6 toggle.\n" +
		"  ⎿  docs/requirements/drafts/refactor-agent.md\n" +
		"\n" +
		"  Several lines of completed output here.\n" +
		"  More completed output pushing the live region down.\n" +
		"  Yet more so the stale spinner is well above the tail.\n" +
		"  And a bit more padding to clear the tail window.\n" +
		"  Final line of the recap block.\n" +
		"✻ Sautéed for 1m 12s\n" + // past-tense recap: idle now
		"─────────────────────────────── planner ──\n" +
		"❯ Delegate phase 1 implementation to edit\n" + // parked-input wedge at the live prompt
		"──────────────────────────────────────────\n" +
		"  ⏵⏵ bypass permissions on (shift+tab to cycle) · ← for agents\n"

	if !PaneShowsRecoverableIdle(content) {
		t.Error("stale thinking text in scrollback must not block recovery when the " +
			"live tail is a clean idle prompt — this is the plan-agent wedge the " +
			"watchdog could not rescue")
	}
}

// --- False-clean-verdict regression tests ---
//
// Each test below pairs the failing case with a negative control, because the
// bug being fixed was itself a diagnostic that could not fail: the report
// gathered evidence, rendered it red, and still concluded "No issues detected".
// A regression test with no control would reproduce that same defect one level
// up.

// TestAckDeliveryActive_Precedence pins the rollback-valve order. Diagnose and
// the daemon now share this one definition; if it drifts, diagnose starts
// reading a healthy session through the wrong delivery model again.
func TestAckDeliveryActive_Precedence(t *testing.T) {
	session, cleanup := setupTestBusDir(t)
	defer cleanup()

	t.Run("DefaultOn", func(t *testing.T) {
		if !AckDeliveryActive(session) {
			t.Error("expected receipt-based delivery ON by default")
		}
	})

	t.Run("RuntimeMarkerOff", func(t *testing.T) {
		if err := SetAckDeliveryOff(session, true); err != nil {
			t.Fatalf("SetAckDeliveryOff: %v", err)
		}
		defer SetAckDeliveryOff(session, false)
		if AckDeliveryActive(session) {
			t.Error("expected OFF with runtime rollback marker present")
		}
	})

	t.Run("EnvKillSwitchBeatsDefault", func(t *testing.T) {
		t.Setenv("MUXCODE_DELIVERY_ACK_DISABLE", "1")
		if AckDeliveryActive(session) {
			t.Error("expected OFF with MUXCODE_DELIVERY_ACK_DISABLE set")
		}
	})

	t.Run("EnvOptInBeatsRuntimeMarker", func(t *testing.T) {
		if err := SetAckDeliveryOff(session, true); err != nil {
			t.Fatalf("SetAckDeliveryOff: %v", err)
		}
		defer SetAckDeliveryOff(session, false)
		t.Setenv("MUXCODE_DELIVERY_ACK", "on")
		if !AckDeliveryActive(session) {
			t.Error("expected explicit env opt-in to override the runtime marker")
		}
	})
}

// TestIsWakeEvent_CoversRealEmitters guards the name-mismatch that manufactured
// the false evidence. Every name asserted here is one the daemon or mode
// switcher actually emits; matching only the literal "idle-wake" scored all of
// them as missed deliveries.
func TestIsWakeEvent_CoversRealEmitters(t *testing.T) {
	emitted := []string{
		"idle-wake", "idle-response-wake", "idle-combined-wake",
		"idle-task-retry", "idle-task-rescue",
		"startup-wake", "startup-wake-full", "startup-wake-enter", "startup-wake-provider",
		"wake-full", "wake-enter", "wake-provider",
		"watchdog-force-deliver",
	}
	for _, ev := range emitted {
		if !isWakeEvent(ev) {
			t.Errorf("emitted wake event %q not recognized — notify would score as a miss", ev)
		}
	}
	// Negative control: a non-delivery event must not satisfy a notify, or the
	// gap analysis would never detect a real failure.
	for _, ev := range []string{"inbox-notify", "idle-transition", "wake-failed", "agent-down"} {
		if isWakeEvent(ev) {
			t.Errorf("%q must not count as a successful wake", ev)
		}
	}
}

// TestAnnotateGaps_AcceptsCombinedWake is the direct regression for the
// fabricated red lines: idle-combined-wake IS a delivery, so no gap should be
// annotated after it.
func TestAnnotateGaps_AcceptsCombinedWake(t *testing.T) {
	events := []TimelineEvent{
		{Timestamp: 100, Event: "inbox-notify", Detail: "commit"},
		{Timestamp: 103, Event: "idle-combined-wake", Detail: "commit: 2 messages"},
	}
	annotateGaps(events, "commit")

	if events[1].GapNote != "" {
		t.Errorf("idle-combined-wake is a delivery — expected no gap note, got %q", events[1].GapNote)
	}
	if events[1].GapSecs != 3 {
		t.Errorf("expected 3s gap recorded on the wake, got %d", events[1].GapSecs)
	}
}

// TestBuildTimeline_NoFabricatedGapsUnderAckDelivery reproduces the reported
// symptom end-to-end: a healthy session under the default delivery model must
// not render "expected a wake" lines. Under the cutover no daemon wake follows
// an inbox-notify at all, so annotating one per notify painted every healthy
// session red.
func TestBuildTimeline_NoFabricatedGapsUnderAckDelivery(t *testing.T) {
	session, cleanup := setupTestBusDir(t)
	defer cleanup()
	// Own log dir so entries from other tests using this session name cannot
	// bleed in and change the gap count under test.
	t.Setenv("MUXCODE_LIFECYCLE_LOG_DIR", t.TempDir())

	now := time.Now().Unix()
	var entries []LifecycleEntry
	for i := 0; i < 5; i++ {
		entries = append(entries, LifecycleEntry{
			TS: now - int64(300-i*30), Level: "info", Source: "daemon",
			Session: session, Event: "inbox-notify", Detail: "commit",
		})
	}
	writeTestLifecycleLog(t, session, entries)

	t.Run("AckDeliveryOn_NoGapsAnnotated", func(t *testing.T) {
		events := BuildTimeline(session, "commit", 20)
		if len(events) == 0 {
			t.Fatal("expected inbox-notify events in the timeline")
		}
		for _, ev := range events {
			if ev.GapNote != "" {
				t.Errorf("fabricated gap under receipt-based delivery: %q", ev.GapNote)
			}
		}
	})

	// Negative control: with the cutover rolled back, a daemon wake IS expected
	// after each notify, so the same log must annotate gaps. Without this the
	// test above would pass even if gap annotation were deleted outright.
	t.Run("AckDeliveryOff_GapsStillAnnotated", func(t *testing.T) {
		t.Setenv("MUXCODE_DELIVERY_ACK", "off")
		events := BuildTimeline(session, "commit", 20)
		gaps := 0
		for _, ev := range events {
			if ev.GapNote != "" {
				gaps++
			}
		}
		if gaps == 0 {
			t.Error("expected gap annotation under pane-scrape delivery — control failed, annotation may be dead")
		}
	})
}

// TestCheckDaemonNotWaking_SilentUnderAckDelivery pins the other half: the
// finding that consumed the fabricated evidence must not fire under a model
// where no daemon wake is expected.
func TestCheckDaemonNotWaking_SilentUnderAckDelivery(t *testing.T) {
	timeline := []TimelineEvent{
		{Timestamp: 100, Event: "inbox-notify", Detail: "commit"},
		{Timestamp: 200, Event: "inbox-notify", Detail: "commit"},
	}
	base := func(ack bool) *DiagnosticReport {
		return &DiagnosticReport{
			Role:        "commit",
			AgentState:  AgentStateEvidence{IsIdle: true, IsAlive: true},
			InboxState:  InboxStateEvidence{MessageCount: 1, ActionableCount: 1},
			NotifyState: NotifyStateEvidence{AckDelivery: ack},
			Timeline:    timeline,
		}
	}
	if f := checkDaemonNotWaking(base(true)); f != nil {
		t.Errorf("expected silence under receipt-based delivery, got %s", f.FailureMode)
	}
	// Negative control: the check must still fire in the model it was written for.
	if f := checkDaemonNotWaking(base(false)); f == nil {
		t.Error("expected daemon-not-waking under pane-scrape delivery — control failed")
	}
}

// TestCheckActiveWithStaleMessages_NotifiedButUnconsumed is the exact reported
// state: an active agent holding old actionable messages that the daemon
// already marked notified. UnnotifiedCount is 0 BY CONSTRUCTION once a notify
// is recorded, and the old gate required it to be > 0 — so the one wedge this
// check exists to catch was the one case it stayed silent for.
func TestCheckActiveWithStaleMessages_NotifiedButUnconsumed(t *testing.T) {
	report := &DiagnosticReport{
		Role: "test",
		AgentState: AgentStateEvidence{
			IsIdle: false, IsAlive: true, WiderCaptureIdle: false,
			Provider: "claude", SupportsHooks: true,
		},
		InboxState: InboxStateEvidence{
			MessageCount: 2, ActionableCount: 2, OldestMessageAge: 900,
		},
		NotifyState: NotifyStateEvidence{
			UnnotifiedCount: 0, // every message already marked notified
			NotifiedIDCount: 2,
			MarkerAge:       800,
			IsMarkerStale:   true,
			AckDelivery:     true,
		},
	}
	finding := checkActiveWithStaleMessages(report)
	if finding == nil {
		t.Fatal("expected a finding: agent active, messages notified but never consumed")
	}
	if finding.FailureMode != "active-with-stale-messages" {
		t.Errorf("expected active-with-stale-messages, got %s", finding.FailureMode)
	}
	if finding.Severity != "critical" {
		t.Errorf("notified-but-unconsumed is confirmed stuck delivery, expected critical, got %s", finding.Severity)
	}
}

// TestCheckActiveWithStaleMessages_QuietWhenFresh is the control: a busy agent
// with a recently-arrived message is normal and must stay unreported.
func TestCheckActiveWithStaleMessages_QuietWhenFresh(t *testing.T) {
	report := &DiagnosticReport{
		Role:       "test",
		AgentState: AgentStateEvidence{IsIdle: false, IsAlive: true},
		InboxState: InboxStateEvidence{MessageCount: 1, ActionableCount: 1, OldestMessageAge: 5},
	}
	if f := checkActiveWithStaleMessages(report); f != nil {
		t.Errorf("expected silence for a freshly-delivered message, got %s", f.FailureMode)
	}
}

func TestCheckAgentDown_Detected(t *testing.T) {
	report := &DiagnosticReport{
		Role:       "build",
		AgentState: AgentStateEvidence{IsAlive: false, Provider: "claude"},
		InboxState: InboxStateEvidence{MessageCount: 1, ActionableCount: 1},
	}
	finding := checkAgentDown(report)
	if finding == nil {
		t.Fatal("expected a finding for a dead agent")
	}
	if finding.Severity != "critical" {
		t.Errorf("expected critical, got %s", finding.Severity)
	}
}

// TestCheckAgentDown_ExpectedAbsences: a stopped or reloading agent is down on
// purpose. Without these the check would fire during every hot reload.
func TestCheckAgentDown_ExpectedAbsences(t *testing.T) {
	for _, tc := range []struct {
		name  string
		state AgentStateEvidence
	}{
		{"Stopped", AgentStateEvidence{IsAlive: false, IsStopped: true}},
		{"Reloading", AgentStateEvidence{IsAlive: false, IsReloading: true}},
		{"Alive", AgentStateEvidence{IsAlive: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			report := &DiagnosticReport{Role: "build", AgentState: tc.state}
			if f := checkAgentDown(report); f != nil {
				t.Errorf("expected no finding, got %s", f.FailureMode)
			}
		})
	}
}

func TestCheckReceiptGap(t *testing.T) {
	mk := func(ack bool, count int, age int64) *DiagnosticReport {
		return &DiagnosticReport{
			Role:        "review",
			AgentState:  AgentStateEvidence{IsAlive: true, SupportsHooks: true, Provider: "claude"},
			NotifyState: NotifyStateEvidence{AckDelivery: ack, ReceiptGapCount: count, ReceiptGapAge: age},
		}
	}

	t.Run("CriticalWhenOld", func(t *testing.T) {
		f := checkReceiptGap(mk(true, 2, diagnoseStuckInboxSecs+10))
		if f == nil {
			t.Fatal("expected receipt-gap finding")
		}
		if f.Severity != "critical" {
			t.Errorf("expected critical for a long gap, got %s", f.Severity)
		}
	})

	t.Run("WarningWhenRecent", func(t *testing.T) {
		f := checkReceiptGap(mk(true, 1, diagnoseReceiptGapSecs+1))
		if f == nil {
			t.Fatal("expected receipt-gap finding")
		}
		if f.Severity != "warning" {
			t.Errorf("expected warning for a short gap, got %s", f.Severity)
		}
	})

	t.Run("SilentWithoutCutover", func(t *testing.T) {
		if f := checkReceiptGap(mk(false, 3, 999)); f != nil {
			t.Error("receipts are not the delivery evidence under pane-scrape delivery")
		}
	})

	t.Run("SilentWhenNoGap", func(t *testing.T) {
		if f := checkReceiptGap(mk(true, 0, 0)); f != nil {
			t.Error("expected silence with every message receipted")
		}
	})
}

// TestRunDiagnostics_NeverCleanWithStuckInbox is the invariant this whole fix
// exists to establish, stated independently of any single detector: if an agent
// is sitting on unconsumed actionable work, diagnose must NOT report a clean
// bill of health. Three separate sessions produced a false clean from three
// different missing detectors — so the guarantee has to be asserted at the
// verdict, not at each pattern.
func TestRunDiagnostics_NeverCleanWithStuckInbox(t *testing.T) {
	// The reported state, verbatim: agent wedged active, marker stale, messages
	// long unconsumed, daemon healthy.
	report := &DiagnosticReport{
		Role: "test",
		AgentState: AgentStateEvidence{
			IsIdle: false, IsAlive: true, Provider: "opencode", SupportsHooks: false,
		},
		InboxState: InboxStateEvidence{
			MessageCount: 3, ActionableCount: 3, OldestMessageAge: 3600,
		},
		NotifyState: NotifyStateEvidence{
			NotifiedIDCount: 3, UnnotifiedCount: 0,
			MarkerAge: 3000, IsMarkerStale: true, AckDelivery: true,
		},
		DaemonState: DaemonStateEvidence{IsAlive: true, KeepaliveAge: 2},
	}
	RunDiagnostics(report)

	if len(report.Findings) == 0 {
		t.Fatal("false clean verdict: agent wedged with a stuck inbox and diagnose reported nothing")
	}
	hasCritical := false
	for _, f := range report.Findings {
		if f.Severity == "critical" {
			hasCritical = true
		}
	}
	// Exit code is derived from critical severity — a warning-only verdict would
	// still exit 0 and read as success to any caller scripting against it.
	if !hasCritical {
		t.Errorf("expected a critical finding so diagnose exits non-zero; got %d non-critical finding(s)", len(report.Findings))
	}
}

// TestCheckUnexplainedEvidence guards the backstop itself.
func TestCheckUnexplainedEvidence(t *testing.T) {
	stuck := func() *DiagnosticReport {
		return &DiagnosticReport{
			Role:       "run",
			AgentState: AgentStateEvidence{IsAlive: true, IsIdle: true, Provider: "claude"},
			InboxState: InboxStateEvidence{
				MessageCount: 1, ActionableCount: 1,
				OldestMessageAge: diagnoseStuckInboxSecs + 60,
			},
		}
	}

	t.Run("FiresWhenNothingElseExplains", func(t *testing.T) {
		f := checkUnexplainedEvidence(stuck())
		if f == nil {
			t.Fatal("expected the backstop to fire on an unexplained stuck inbox")
		}
		if f.Severity != "critical" {
			t.Errorf("expected critical so the verdict is not silently exit 0, got %s", f.Severity)
		}
	})

	t.Run("DefersToAnExistingFinding", func(t *testing.T) {
		r := stuck()
		r.Findings = []DiagnosticFinding{{Severity: "warning", FailureMode: "some-known-mode"}}
		if f := checkUnexplainedEvidence(r); f != nil {
			t.Error("backstop must stay quiet once a real detector has explained the state")
		}
	})

	t.Run("VersionMismatchDoesNotExplain", func(t *testing.T) {
		r := stuck()
		r.Findings = []DiagnosticFinding{{Severity: "warning", FailureMode: "binary-daemon-version-mismatch"}}
		if f := checkUnexplainedEvidence(r); f == nil {
			t.Error("a stale daemon is not an account of a stuck inbox — backstop must still fire")
		}
	})

	// End to end through the registry: a stuck inbox under a stale daemon
	// yields BOTH findings, so the warning never buys a false-clean verdict.
	t.Run("BothFindingsThroughRunDiagnostics", func(t *testing.T) {
		installed := Info{Version: "v0.2.0", Commit: "def5678", Date: "2026-09-02T13:00:00Z"}
		older := Info{Version: "v0.1.0", Commit: "abc1234", Date: "2026-09-01T10:00:00Z"}
		r := stuck()
		r.AgentState.SupportsHooks = true
		r.DaemonState = DaemonStateEvidence{IsAlive: true, KeepaliveAge: 2, DaemonBuild: &older, InstalledBuild: installed}
		RunDiagnostics(r)
		modes := map[string]string{}
		for _, f := range r.Findings {
			modes[f.FailureMode] = f.Severity
		}
		if modes["binary-daemon-version-mismatch"] != "warning" {
			t.Errorf("expected the mismatch warning, got %+v", r.Findings)
		}
		if modes["unexplained-stuck-inbox"] != "critical" {
			t.Errorf("expected the critical backstop alongside the mismatch, got %+v", r.Findings)
		}
	})

	t.Run("IgnoresInfoOnlyFindings", func(t *testing.T) {
		r := stuck()
		r.Findings = []DiagnosticFinding{{Severity: "info", FailureMode: "no-actionable-messages"}}
		if f := checkUnexplainedEvidence(r); f == nil {
			t.Error("an info note does not explain a stuck inbox — backstop should still fire")
		}
	})

	t.Run("QuietOnHealthyAgent", func(t *testing.T) {
		r := &DiagnosticReport{
			Role:       "run",
			AgentState: AgentStateEvidence{IsAlive: true, IsIdle: true},
			InboxState: InboxStateEvidence{MessageCount: 0, ActionableCount: 0},
		}
		if f := checkUnexplainedEvidence(r); f != nil {
			t.Errorf("backstop must not fire on a drained inbox, got %s", f.FailureMode)
		}
	})

	t.Run("QuietWhenInboxIsFresh", func(t *testing.T) {
		r := stuck()
		r.InboxState.OldestMessageAge = 10
		if f := checkUnexplainedEvidence(r); f != nil {
			t.Error("a just-arrived message is not a wedge — backstop must stay quiet")
		}
	})
}

// TestDiagnosticChecks_BackstopRegisteredLast pins the wiring, not just the
// function. checkUnexplainedEvidence reads the findings earlier checks produced,
// so it is correct ONLY in last position — and a unit test that calls it
// directly cannot see it being dropped from the registry or reordered.
func TestDiagnosticChecks_BackstopRegisteredLast(t *testing.T) {
	if len(diagnosticChecks) == 0 {
		t.Fatal("no diagnostic checks registered")
	}
	last := diagnosticChecks[len(diagnosticChecks)-1]
	// Compare behaviour, not pointers: a report the backstop fires on must be
	// answered by whatever sits last.
	stuck := &DiagnosticReport{
		Role:       "run",
		AgentState: AgentStateEvidence{IsAlive: true, IsIdle: true},
		InboxState: InboxStateEvidence{
			MessageCount: 1, ActionableCount: 1,
			OldestMessageAge: diagnoseStuckInboxSecs + 60,
		},
	}
	f := last(stuck)
	if f == nil || f.FailureMode != "unexplained-stuck-inbox" {
		t.Error("checkUnexplainedEvidence must be registered last — it reads prior findings")
	}
}

// TestCheckPostRestartWakeGap_AcceptsCombinedWake covers the inverse of the
// main defect: matching only the literal "idle-wake" made this check report a
// gap after a delivery that actually happened under another name.
func TestCheckPostRestartWakeGap_AcceptsCombinedWake(t *testing.T) {
	report := &DiagnosticReport{
		Role:        "commit",
		AgentState:  AgentStateEvidence{IsIdle: true, IsAlive: true},
		InboxState:  InboxStateEvidence{MessageCount: 1, ActionableCount: 1},
		NotifyState: NotifyStateEvidence{AckDelivery: false},
		Timeline: []TimelineEvent{
			{Timestamp: 100, Event: "idle-transition", Detail: "commit"},
			{Timestamp: 104, Event: "idle-combined-wake", Detail: "commit: 1 messages"},
		},
	}
	if f := checkPostRestartWakeGap(report); f != nil {
		t.Errorf("idle-combined-wake IS the wake — expected no gap finding, got %s", f.FailureMode)
	}

	// Negative control: with no wake of any name, the gap is real.
	report.Timeline = []TimelineEvent{{Timestamp: 100, Event: "idle-transition", Detail: "commit"}}
	if f := checkPostRestartWakeGap(report); f == nil {
		t.Error("expected post-restart-wake-gap when no wake followed — control failed")
	}
}

// TestCheckMissedSendKeys_AcceptsCombinedWake covers the same root cause where
// it fails the other way: this check needs to FIND a wake, so an unrecognized
// name made it silently under-report a genuine dropped injection.
func TestCheckMissedSendKeys_AcceptsCombinedWake(t *testing.T) {
	report := &DiagnosticReport{
		Role:       "commit",
		AgentState: AgentStateEvidence{IsIdle: true, IsAlive: true},
		InboxState: InboxStateEvidence{MessageCount: 1, ActionableCount: 1},
		Timeline: []TimelineEvent{
			{Timestamp: 100, Event: "idle-combined-wake", Detail: "commit: 1 messages"},
		},
	}
	if f := checkMissedSendKeys(report); f == nil {
		t.Error("a logged idle-combined-wake with the message still queued is a missed injection")
	}
}

// TestWakeEvents_AreTimelineRelevant pins the coupling between the two maps.
//
// A name in wakeEvents but missing from roleRelevantEvents is invisible: the
// timeline filter drops it before annotateGaps ever runs, so the wake cannot
// satisfy its inbox-notify and the gap is fabricated anyway — the original bug,
// regenerated silently by an incomplete edit. The two lists must not drift.
func TestWakeEvents_AreTimelineRelevant(t *testing.T) {
	for ev := range wakeEvents {
		if !roleRelevantEvents[ev] {
			t.Errorf("wake event %q is not in roleRelevantEvents — it is filtered out of the timeline, so it can never satisfy an inbox-notify", ev)
		}
	}
}

// TestBuildTimeline_CombinedWakeSurvivesFilter proves the coupling end-to-end
// rather than by inspection: a real wake must reach the timeline AND clear the
// gap, through the same filter+annotate path the report uses.
func TestBuildTimeline_CombinedWakeSurvivesFilter(t *testing.T) {
	session, cleanup := setupTestBusDir(t)
	defer cleanup()
	t.Setenv("MUXCODE_LIFECYCLE_LOG_DIR", t.TempDir())
	t.Setenv("MUXCODE_DELIVERY_ACK", "off") // gap analysis only runs pre-cutover

	now := time.Now().Unix()
	writeTestLifecycleLog(t, session, []LifecycleEntry{
		{TS: now - 100, Level: "info", Source: "daemon", Session: session,
			Event: "inbox-notify", Detail: "commit"},
		{TS: now - 97, Level: "info", Source: "daemon", Session: session,
			Event: "idle-combined-wake", Detail: "commit: 2 messages"},
	})

	events := BuildTimeline(session, "commit", 20)

	sawWake := false
	for _, ev := range events {
		if ev.Event == "idle-combined-wake" {
			sawWake = true
		}
		if ev.GapNote != "" {
			t.Errorf("wake was delivered 3s after notify — unexpected gap note %q", ev.GapNote)
		}
	}
	if !sawWake {
		t.Fatal("idle-combined-wake was filtered out of the timeline — it can never clear a gap")
	}
}
