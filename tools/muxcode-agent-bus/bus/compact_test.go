package bus

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDefaultCompactThresholds(t *testing.T) {
	th := DefaultCompactThresholds()
	if th.SizeBytes != 512*1024 {
		t.Errorf("SizeBytes = %d, want %d", th.SizeBytes, 512*1024)
	}
	if th.MinAge != 2*time.Hour {
		t.Errorf("MinAge = %v, want %v", th.MinAge, 2*time.Hour)
	}
}

func TestCheckRoleCompaction_BelowSizeThreshold(t *testing.T) {
	session := testSession(t)
	th := CompactThresholds{SizeBytes: 1024 * 1024, MinAge: 1 * time.Hour}

	// No files exist — total is 0 bytes
	alert := CheckRoleCompaction(session, "build", th)
	if alert != nil {
		t.Errorf("expected nil alert for empty files, got %+v", alert)
	}
}

func TestCheckRoleCompaction_RecentlyCompacted(t *testing.T) {
	session := testSession(t)
	th := CompactThresholds{SizeBytes: 100, MinAge: 2 * time.Hour}

	// Create large enough history file
	histPath := HistoryPath(session, "build")
	writeTestFile(t, histPath, 200)

	// Set last compact to 30 minutes ago — below MinAge
	meta := &SessionMeta{
		StartTS:       time.Now().Add(-3 * time.Hour).Unix(),
		CompactCount:  1,
		LastCompactTS: time.Now().Add(-30 * time.Minute).Unix(),
	}
	if err := WriteSessionMeta(session, "build", meta); err != nil {
		t.Fatalf("WriteSessionMeta: %v", err)
	}

	alert := CheckRoleCompaction(session, "build", th)
	if alert != nil {
		t.Errorf("expected nil alert for recently compacted, got %+v", alert)
	}
}

func TestCheckRoleCompaction_Alert(t *testing.T) {
	session := testSession(t)
	th := CompactThresholds{SizeBytes: 100, MinAge: 1 * time.Hour}

	// Create files exceeding size threshold
	histPath := HistoryPath(session, "build")
	writeTestFile(t, histPath, 200)

	// Set last compact to 3 hours ago — exceeds MinAge
	meta := &SessionMeta{
		StartTS:       time.Now().Add(-5 * time.Hour).Unix(),
		CompactCount:  1,
		LastCompactTS: time.Now().Add(-3 * time.Hour).Unix(),
	}
	if err := WriteSessionMeta(session, "build", meta); err != nil {
		t.Fatalf("WriteSessionMeta: %v", err)
	}

	alert := CheckRoleCompaction(session, "build", th)
	if alert == nil {
		t.Fatal("expected alert, got nil")
	}
	if alert.Role != "build" {
		t.Errorf("role = %q, want %q", alert.Role, "build")
	}
	if alert.HistoryBytes != 200 {
		t.Errorf("HistoryBytes = %d, want 200", alert.HistoryBytes)
	}
	if alert.TotalBytes < 200 {
		t.Errorf("TotalBytes = %d, want >= 200", alert.TotalBytes)
	}
	if alert.HoursSinceCompact < 2.9 {
		t.Errorf("HoursSinceCompact = %f, want >= 2.9", alert.HoursSinceCompact)
	}
	if alert.Message == "" {
		t.Error("expected non-empty message")
	}
}

func TestCheckRoleCompaction_NeverCompacted(t *testing.T) {
	session := testSession(t)
	th := CompactThresholds{SizeBytes: 100, MinAge: 1 * time.Hour}

	// Create files exceeding size threshold
	histPath := HistoryPath(session, "build")
	writeTestFile(t, histPath, 200)

	// Session meta with no compaction, started 3 hours ago
	meta := &SessionMeta{
		StartTS:       time.Now().Add(-3 * time.Hour).Unix(),
		CompactCount:  0,
		LastCompactTS: 0,
	}
	if err := WriteSessionMeta(session, "build", meta); err != nil {
		t.Fatalf("WriteSessionMeta: %v", err)
	}

	alert := CheckRoleCompaction(session, "build", th)
	if alert == nil {
		t.Fatal("expected alert for never-compacted role with large context")
	}
	if alert.HoursSinceCompact < 2.9 {
		t.Errorf("HoursSinceCompact = %f, want >= 2.9 (should use session start time)", alert.HoursSinceCompact)
	}
}

func TestCheckRoleCompaction_NoSessionMeta(t *testing.T) {
	session := testSession(t)
	th := CompactThresholds{SizeBytes: 100, MinAge: 1 * time.Hour}

	// Create files exceeding size threshold
	histPath := HistoryPath(session, "build")
	writeTestFile(t, histPath, 200)

	// No session meta at all — hoursSinceLastCompact returns 999.0
	alert := CheckRoleCompaction(session, "build", th)
	if alert == nil {
		t.Fatal("expected alert for role with no session meta and large context")
	}
	if alert.HoursSinceCompact < 100 {
		t.Errorf("HoursSinceCompact = %f, want large value for missing meta", alert.HoursSinceCompact)
	}
}

func TestCheckCompaction_MultipleRoles(t *testing.T) {
	session := testSession(t)
	th := CompactThresholds{SizeBytes: 100, MinAge: 1 * time.Hour}

	// Create large files for build and test
	writeTestFile(t, HistoryPath(session, "build"), 200)
	writeTestFile(t, HistoryPath(session, "test"), 300)

	alerts := CheckCompaction(session, th)
	if len(alerts) < 2 {
		t.Errorf("expected at least 2 alerts, got %d", len(alerts))
	}

	roles := make(map[string]bool)
	for _, a := range alerts {
		roles[a.Role] = true
	}
	if !roles["build"] {
		t.Error("expected alert for build")
	}
	if !roles["test"] {
		t.Error("expected alert for test")
	}
}

func TestCheckCompaction_NoAlerts(t *testing.T) {
	session := testSession(t)
	th := DefaultCompactThresholds()

	// No files — everything is 0 bytes
	alerts := CheckCompaction(session, th)
	if len(alerts) != 0 {
		t.Errorf("expected no alerts for clean session, got %d", len(alerts))
	}
}

func TestFormatCompactAlert(t *testing.T) {
	alert := CompactAlert{
		Role:              "edit",
		TotalBytes:        640 * 1024,
		MemoryBytes:       180 * 1024,
		HistoryBytes:      360 * 1024,
		LogBytes:          100 * 1024,
		HoursSinceCompact: 2.5,
		Message:           "test message",
	}

	out := FormatCompactAlert(alert)
	if !strings.Contains(out, "COMPACT RECOMMENDED: edit") {
		t.Error("missing header")
	}
	if !strings.Contains(out, "640 KB") {
		t.Error("missing total size")
	}
	if !strings.Contains(out, "180 KB") {
		t.Error("missing memory size")
	}
	if !strings.Contains(out, "360 KB") {
		t.Error("missing history size")
	}
	if !strings.Contains(out, "100 KB") {
		t.Error("missing log size")
	}
	if !strings.Contains(out, "2h 30m ago") {
		t.Error("missing time since compact")
	}
	if !strings.Contains(out, "muxcode-agent-bus session compact") {
		t.Error("missing action instruction")
	}
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		bytes int64
		want  string
	}{
		{0, "0 B"},
		{500, "500 B"},
		{1024, "1 KB"},
		{1536, "2 KB"},
		{512 * 1024, "512 KB"},
		{1024 * 1024, "1.0 MB"},
		{1536 * 1024, "1.5 MB"},
		{10 * 1024 * 1024, "10.0 MB"},
	}

	for _, tt := range tests {
		got := formatBytes(tt.bytes)
		if got != tt.want {
			t.Errorf("formatBytes(%d) = %q, want %q", tt.bytes, got, tt.want)
		}
	}
}

func TestFormatHours(t *testing.T) {
	tests := []struct {
		hours float64
		want  string
	}{
		{0, "0m"},
		{0.5, "30m"},
		{1.0, "1h"},
		{1.5, "1h 30m"},
		{2.0, "2h"},
		{2.5, "2h 30m"},
		{24.0, "24h"},
	}

	for _, tt := range tests {
		got := formatHours(tt.hours)
		if got != tt.want {
			t.Errorf("formatHours(%f) = %q, want %q", tt.hours, got, tt.want)
		}
	}
}

func TestCompactAlertKey(t *testing.T) {
	alert := CompactAlert{Role: "build"}
	if got := CompactAlertKey(alert); got != "compact:build" {
		t.Errorf("CompactAlertKey = %q, want %q", got, "compact:build")
	}

	alert2 := CompactAlert{Role: "edit"}
	if got := CompactAlertKey(alert2); got != "compact:edit" {
		t.Errorf("CompactAlertKey = %q, want %q", got, "compact:edit")
	}
}

func TestFilterNewCompactAlerts(t *testing.T) {
	alerts := []CompactAlert{
		{Role: "build", TotalBytes: 1000, Message: "build alert"},
		{Role: "test", TotalBytes: 2000, Message: "test alert"},
	}

	lastSeen := make(map[string]int64)

	// First call: all alerts are new
	fresh := FilterNewCompactAlerts(alerts, lastSeen, 600)
	if len(fresh) != 2 {
		t.Errorf("first call: got %d fresh, want 2", len(fresh))
	}

	// Second call immediately: all should be filtered
	fresh = FilterNewCompactAlerts(alerts, lastSeen, 600)
	if len(fresh) != 0 {
		t.Errorf("second call: got %d fresh, want 0", len(fresh))
	}
}

func TestFilterNewCompactAlerts_CooldownExpired(t *testing.T) {
	alerts := []CompactAlert{
		{Role: "build", TotalBytes: 1000, Message: "build alert"},
	}

	lastSeen := map[string]int64{
		"compact:build": time.Now().Unix() - 700, // 700s ago, cooldown is 600s
	}

	fresh := FilterNewCompactAlerts(alerts, lastSeen, 600)
	if len(fresh) != 1 {
		t.Errorf("got %d fresh, want 1 (cooldown expired)", len(fresh))
	}
}

func TestFileSize_Missing(t *testing.T) {
	size := fileSize("/nonexistent/path/to/file")
	if size != 0 {
		t.Errorf("fileSize(missing) = %d, want 0", size)
	}
}

func TestFileSize_Existing(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "test.txt")
	if err := os.WriteFile(path, make([]byte, 256), 0644); err != nil {
		t.Fatal(err)
	}
	size := fileSize(path)
	if size != 256 {
		t.Errorf("fileSize = %d, want 256", size)
	}
}

func TestCheckRoleCompaction_SizeMet_TimeMet(t *testing.T) {
	// Both thresholds met — should alert
	session := testSession(t)
	th := CompactThresholds{SizeBytes: 50, MinAge: 30 * time.Minute}

	writeTestFile(t, HistoryPath(session, "build"), 100)

	meta := &SessionMeta{
		StartTS:       time.Now().Add(-2 * time.Hour).Unix(),
		CompactCount:  1,
		LastCompactTS: time.Now().Add(-1 * time.Hour).Unix(),
	}
	if err := WriteSessionMeta(session, "build", meta); err != nil {
		t.Fatal(err)
	}

	alert := CheckRoleCompaction(session, "build", th)
	if alert == nil {
		t.Fatal("expected alert when both thresholds met")
	}
}

func TestCheckRoleCompaction_SizeMet_TimeNotMet(t *testing.T) {
	// Size met but recently compacted — should not alert
	session := testSession(t)
	th := CompactThresholds{SizeBytes: 50, MinAge: 2 * time.Hour}

	writeTestFile(t, HistoryPath(session, "build"), 100)

	meta := &SessionMeta{
		StartTS:       time.Now().Add(-3 * time.Hour).Unix(),
		CompactCount:  1,
		LastCompactTS: time.Now().Add(-30 * time.Minute).Unix(),
	}
	if err := WriteSessionMeta(session, "build", meta); err != nil {
		t.Fatal(err)
	}

	alert := CheckRoleCompaction(session, "build", th)
	if alert != nil {
		t.Errorf("expected no alert when time threshold not met, got %+v", alert)
	}
}

func TestCheckRoleCompaction_SizeNotMet_TimeMet(t *testing.T) {
	// Time met but small files — should not alert
	session := testSession(t)
	th := CompactThresholds{SizeBytes: 1024 * 1024, MinAge: 1 * time.Hour}

	// Only a tiny file
	writeTestFile(t, HistoryPath(session, "build"), 10)

	meta := &SessionMeta{
		StartTS:       time.Now().Add(-5 * time.Hour).Unix(),
		CompactCount:  1,
		LastCompactTS: time.Now().Add(-3 * time.Hour).Unix(),
	}
	if err := WriteSessionMeta(session, "build", meta); err != nil {
		t.Fatal(err)
	}

	alert := CheckRoleCompaction(session, "build", th)
	if alert != nil {
		t.Errorf("expected no alert when size threshold not met, got %+v", alert)
	}
}

// writeTestFile creates a file of the given size at the specified path.
func writeTestFile(t *testing.T, path string, size int) {
	t.Helper()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", dir, err)
	}
	if err := os.WriteFile(path, make([]byte, size), 0644); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}
