package bus

import (
	"fmt"
	"math/rand"
	"os"
	"strings"
	"testing"
	"time"
)

func TestParseSchedule_EveryDuration(t *testing.T) {
	tests := []struct {
		input    string
		expected time.Duration
	}{
		{"@every 30s", 30 * time.Second},
		{"@every 5m", 5 * time.Minute},
		{"@every 1h", time.Hour},
		{"@every 2h30m", 2*time.Hour + 30*time.Minute},
	}

	for _, tt := range tests {
		s, err := ParseSchedule(tt.input)
		if err != nil {
			t.Errorf("ParseSchedule(%q): %v", tt.input, err)
			continue
		}
		if s.Interval != tt.expected {
			t.Errorf("ParseSchedule(%q) = %v, want %v", tt.input, s.Interval, tt.expected)
		}
	}
}

func TestParseSchedule_Aliases(t *testing.T) {
	tests := []struct {
		input    string
		expected time.Duration
	}{
		{"@hourly", time.Hour},
		{"@daily", 24 * time.Hour},
		{"@half-hourly", 30 * time.Minute},
		{"@HOURLY", time.Hour},     // case insensitive
		{"@Daily", 24 * time.Hour}, // case insensitive
	}

	for _, tt := range tests {
		s, err := ParseSchedule(tt.input)
		if err != nil {
			t.Errorf("ParseSchedule(%q): %v", tt.input, err)
			continue
		}
		if s.Interval != tt.expected {
			t.Errorf("ParseSchedule(%q) = %v, want %v", tt.input, s.Interval, tt.expected)
		}
	}
}

func TestParseSchedule_Errors(t *testing.T) {
	tests := []struct {
		input string
		errRe string
	}{
		{"@every 5s", "below minimum"},
		{"@every 1ms", "below minimum"},
		{"@every", "empty duration"},
		{"@every ", "empty duration"},
		{"@every abc", "invalid duration"},
		{"invalid", "unsupported schedule format"},
		{"", "unsupported schedule format"},
		{"5m", "unsupported schedule format"},
	}

	for _, tt := range tests {
		_, err := ParseSchedule(tt.input)
		if err == nil {
			t.Errorf("ParseSchedule(%q): expected error containing %q, got nil", tt.input, tt.errRe)
			continue
		}
		if !strings.Contains(err.Error(), tt.errRe) {
			t.Errorf("ParseSchedule(%q): error %q does not contain %q", tt.input, err, tt.errRe)
		}
	}
}

func TestCronDue(t *testing.T) {
	now := time.Now().Unix()

	tests := []struct {
		name     string
		entry    CronEntry
		now      int64
		expected bool
	}{
		{
			name: "never run, enabled",
			entry: CronEntry{
				Schedule:  "@every 5m",
				Enabled:   true,
				LastRunTS: 0,
			},
			now:      now,
			expected: true,
		},
		{
			name: "disabled",
			entry: CronEntry{
				Schedule:  "@every 5m",
				Enabled:   false,
				LastRunTS: 0,
			},
			now:      now,
			expected: false,
		},
		{
			name: "ran recently, not due",
			entry: CronEntry{
				Schedule:  "@every 5m",
				Enabled:   true,
				LastRunTS: now - 60, // 1 minute ago
			},
			now:      now,
			expected: false,
		},
		{
			name: "ran long ago, due",
			entry: CronEntry{
				Schedule:  "@every 5m",
				Enabled:   true,
				LastRunTS: now - 600, // 10 minutes ago
			},
			now:      now,
			expected: true,
		},
		{
			name: "exactly at interval boundary",
			entry: CronEntry{
				Schedule:  "@every 5m",
				Enabled:   true,
				LastRunTS: now - 300, // exactly 5 minutes ago
			},
			now:      now,
			expected: true,
		},
		{
			name: "invalid schedule",
			entry: CronEntry{
				Schedule:  "invalid",
				Enabled:   true,
				LastRunTS: 0,
			},
			now:      now,
			expected: false,
		},
	}

	for _, tt := range tests {
		result := CronDue(tt.entry, tt.now)
		if result != tt.expected {
			t.Errorf("%s: CronDue() = %v, want %v", tt.name, result, tt.expected)
		}
	}
}

func TestReadWriteCronEntries(t *testing.T) {
	session := fmt.Sprintf("test-cron-rw-%d", rand.Int())
	memDir := t.TempDir()
	t.Cleanup(func() { _ = Cleanup(session) })

	if err := Init(session, memDir); err != nil {
		t.Fatalf("Init: %v", err)
	}

	// Initially empty
	entries, err := ReadCronEntries(session)
	if err != nil {
		t.Fatalf("ReadCronEntries: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}

	// Write entries
	testEntries := []CronEntry{
		{ID: "c1", Schedule: "@every 5m", Target: "build", Action: "build", Message: "test1", Enabled: true},
		{ID: "c2", Schedule: "@hourly", Target: "test", Action: "test", Message: "test2", Enabled: false},
	}
	if err := WriteCronEntries(session, testEntries); err != nil {
		t.Fatalf("WriteCronEntries: %v", err)
	}

	// Read back
	entries, err = ReadCronEntries(session)
	if err != nil {
		t.Fatalf("ReadCronEntries: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].ID != "c1" || entries[1].ID != "c2" {
		t.Errorf("unexpected entry IDs: %s, %s", entries[0].ID, entries[1].ID)
	}
}

func TestReadCronEntries_NotExist(t *testing.T) {
	entries, err := ReadCronEntries("nonexistent-session-12345")
	if err != nil {
		t.Fatalf("ReadCronEntries: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
}

func TestAddCronEntry(t *testing.T) {
	session := fmt.Sprintf("test-cron-add-%d", rand.Int())
	memDir := t.TempDir()
	t.Cleanup(func() { _ = Cleanup(session) })

	if err := Init(session, memDir); err != nil {
		t.Fatalf("Init: %v", err)
	}

	entry, err := AddCronEntry(session, CronEntry{
		Schedule: "@every 5m",
		Target:   "build",
		Action:   "build",
		Message:  "Run build",
	})
	if err != nil {
		t.Fatalf("AddCronEntry: %v", err)
	}

	if entry.ID == "" {
		t.Error("expected non-empty ID")
	}
	if entry.CreatedAt == 0 {
		t.Error("expected non-zero CreatedAt")
	}
	if !entry.Enabled {
		t.Error("expected Enabled to be true")
	}

	// Verify persisted
	entries, err := ReadCronEntries(session)
	if err != nil {
		t.Fatalf("ReadCronEntries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].ID != entry.ID {
		t.Errorf("expected ID %s, got %s", entry.ID, entries[0].ID)
	}
}

func TestAddCronEntry_InvalidSchedule(t *testing.T) {
	session := fmt.Sprintf("test-cron-invsched-%d", rand.Int())
	memDir := t.TempDir()
	t.Cleanup(func() { _ = Cleanup(session) })
	_ = Init(session, memDir)

	_, err := AddCronEntry(session, CronEntry{
		Schedule: "invalid",
		Target:   "build",
		Action:   "build",
		Message:  "test",
	})
	if err == nil {
		t.Fatal("expected error for invalid schedule")
	}
}

func TestAddCronEntry_InvalidTarget(t *testing.T) {
	session := fmt.Sprintf("test-cron-invtarget-%d", rand.Int())
	memDir := t.TempDir()
	t.Cleanup(func() { _ = Cleanup(session) })
	_ = Init(session, memDir)

	_, err := AddCronEntry(session, CronEntry{
		Schedule: "@every 5m",
		Target:   "nonexistent-role",
		Action:   "build",
		Message:  "test",
	})
	if err == nil {
		t.Fatal("expected error for unknown target role")
	}
}

func TestRemoveCronEntry(t *testing.T) {
	session := fmt.Sprintf("test-cron-rm-%d", rand.Int())
	memDir := t.TempDir()
	t.Cleanup(func() { _ = Cleanup(session) })
	_ = Init(session, memDir)

	e1, _ := AddCronEntry(session, CronEntry{Schedule: "@every 5m", Target: "build", Action: "build", Message: "1"})
	_, _ = AddCronEntry(session, CronEntry{Schedule: "@hourly", Target: "test", Action: "test", Message: "2"})

	if err := RemoveCronEntry(session, e1.ID); err != nil {
		t.Fatalf("RemoveCronEntry: %v", err)
	}

	entries, _ := ReadCronEntries(session)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Action != "test" {
		t.Errorf("expected remaining entry to be 'test', got %s", entries[0].Action)
	}
}

func TestRemoveCronEntry_NotFound(t *testing.T) {
	session := fmt.Sprintf("test-cron-rmnf-%d", rand.Int())
	memDir := t.TempDir()
	t.Cleanup(func() { _ = Cleanup(session) })
	_ = Init(session, memDir)

	err := RemoveCronEntry(session, "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent entry")
	}
}

func TestSetCronEnabled(t *testing.T) {
	session := fmt.Sprintf("test-cron-enable-%d", rand.Int())
	memDir := t.TempDir()
	t.Cleanup(func() { _ = Cleanup(session) })
	_ = Init(session, memDir)

	entry, _ := AddCronEntry(session, CronEntry{Schedule: "@every 5m", Target: "build", Action: "build", Message: "test"})

	// Disable
	if err := SetCronEnabled(session, entry.ID, false); err != nil {
		t.Fatalf("SetCronEnabled(false): %v", err)
	}
	entries, _ := ReadCronEntries(session)
	if entries[0].Enabled {
		t.Error("expected entry to be disabled")
	}

	// Re-enable
	if err := SetCronEnabled(session, entry.ID, true); err != nil {
		t.Fatalf("SetCronEnabled(true): %v", err)
	}
	entries, _ = ReadCronEntries(session)
	if !entries[0].Enabled {
		t.Error("expected entry to be enabled")
	}
}

func TestSetCronEnabled_NotFound(t *testing.T) {
	session := fmt.Sprintf("test-cron-ennf-%d", rand.Int())
	memDir := t.TempDir()
	t.Cleanup(func() { _ = Cleanup(session) })
	_ = Init(session, memDir)

	err := SetCronEnabled(session, "nonexistent", true)
	if err == nil {
		t.Fatal("expected error for nonexistent entry")
	}
}

func TestUpdateLastRun(t *testing.T) {
	session := fmt.Sprintf("test-cron-lastrun-%d", rand.Int())
	memDir := t.TempDir()
	t.Cleanup(func() { _ = Cleanup(session) })
	_ = Init(session, memDir)

	entry, _ := AddCronEntry(session, CronEntry{Schedule: "@every 5m", Target: "build", Action: "build", Message: "test"})

	now := time.Now().Unix()
	if err := UpdateLastRun(session, entry.ID, now); err != nil {
		t.Fatalf("UpdateLastRun: %v", err)
	}

	entries, _ := ReadCronEntries(session)
	if entries[0].LastRunTS != now {
		t.Errorf("expected LastRunTS=%d, got %d", now, entries[0].LastRunTS)
	}
	if entries[0].RunCount != 1 {
		t.Errorf("expected RunCount=1, got %d", entries[0].RunCount)
	}

	// Update again
	now2 := now + 300
	if err := UpdateLastRun(session, entry.ID, now2); err != nil {
		t.Fatalf("UpdateLastRun (2nd): %v", err)
	}
	entries, _ = ReadCronEntries(session)
	if entries[0].RunCount != 2 {
		t.Errorf("expected RunCount=2, got %d", entries[0].RunCount)
	}
}

func TestUpdateLastRun_NotFound(t *testing.T) {
	session := fmt.Sprintf("test-cron-lrnf-%d", rand.Int())
	memDir := t.TempDir()
	t.Cleanup(func() { _ = Cleanup(session) })
	_ = Init(session, memDir)

	err := UpdateLastRun(session, "nonexistent", time.Now().Unix())
	if err == nil {
		t.Fatal("expected error for nonexistent entry")
	}
}

func TestCronHistory(t *testing.T) {
	session := fmt.Sprintf("test-cron-hist-%d", rand.Int())
	memDir := t.TempDir()
	t.Cleanup(func() { _ = Cleanup(session) })
	_ = Init(session, memDir)

	// Touch history file
	_ = touchFile(CronHistoryPath(session))

	h1 := CronHistoryEntry{CronID: "c1", TS: 1000, MessageID: "m1", Target: "build", Action: "build"}
	h2 := CronHistoryEntry{CronID: "c2", TS: 2000, MessageID: "m2", Target: "test", Action: "test"}
	h3 := CronHistoryEntry{CronID: "c1", TS: 3000, MessageID: "m3", Target: "build", Action: "build"}

	for _, h := range []CronHistoryEntry{h1, h2, h3} {
		if err := AppendCronHistory(session, h); err != nil {
			t.Fatalf("AppendCronHistory: %v", err)
		}
	}

	// Read all
	all, err := ReadCronHistory(session, "")
	if err != nil {
		t.Fatalf("ReadCronHistory (all): %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 history entries, got %d", len(all))
	}

	// Filter by cron ID
	filtered, err := ReadCronHistory(session, "c1")
	if err != nil {
		t.Fatalf("ReadCronHistory (filtered): %v", err)
	}
	if len(filtered) != 2 {
		t.Fatalf("expected 2 history entries for c1, got %d", len(filtered))
	}
}

func TestCronHistory_NotExist(t *testing.T) {
	entries, err := ReadCronHistory("nonexistent-session-67890", "")
	if err != nil {
		t.Fatalf("ReadCronHistory: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
}

func TestExecuteCron(t *testing.T) {
	session := fmt.Sprintf("test-cron-exec-%d", rand.Int())
	memDir := t.TempDir()
	t.Cleanup(func() { _ = Cleanup(session) })
	_ = Init(session, memDir)

	entry := CronEntry{
		ID:       "test-entry",
		Schedule: "@every 5m",
		Target:   "build",
		Action:   "build",
		Message:  "Run scheduled build",
		Enabled:  true,
	}

	msgID, err := ExecuteCron(session, entry)
	if err != nil {
		t.Fatalf("ExecuteCron: %v", err)
	}
	if msgID == "" {
		t.Error("expected non-empty message ID")
	}

	// Verify message in target inbox
	msgs, err := Peek(session, "build")
	if err != nil {
		t.Fatalf("Peek: %v", err)
	}
	if len(msgs) == 0 {
		t.Fatal("expected at least 1 message in build inbox")
	}

	last := msgs[len(msgs)-1]
	if last.From != "cron" {
		t.Errorf("expected from=cron, got %s", last.From)
	}
	if last.To != "build" {
		t.Errorf("expected to=build, got %s", last.To)
	}
	if last.Action != "build" {
		t.Errorf("expected action=build, got %s", last.Action)
	}
}

func TestFormatCronList(t *testing.T) {
	entries := []CronEntry{
		{ID: "c1", Schedule: "@every 5m", Target: "build", Action: "build", Enabled: true, RunCount: 3},
		{ID: "c2", Schedule: "@hourly", Target: "test", Action: "test", Enabled: false, RunCount: 0},
	}

	// showAll=false: only enabled
	out := FormatCronList(entries, false)
	if !strings.Contains(out, "c1") {
		t.Error("expected c1 in output")
	}
	if strings.Contains(out, "c2") {
		t.Error("expected c2 to be hidden when showAll=false")
	}

	// showAll=true: all entries
	out = FormatCronList(entries, true)
	if !strings.Contains(out, "c1") {
		t.Error("expected c1 in output")
	}
	if !strings.Contains(out, "c2") {
		t.Error("expected c2 in output when showAll=true")
	}
	if !strings.Contains(out, "disabled") {
		t.Error("expected 'disabled' status in output")
	}
}

func TestFormatCronList_Empty(t *testing.T) {
	out := FormatCronList(nil, false)
	if !strings.Contains(out, "No enabled cron entries") {
		t.Errorf("unexpected output for empty list: %s", out)
	}

	out = FormatCronList(nil, true)
	if !strings.Contains(out, "No cron entries") {
		t.Errorf("unexpected output for empty --all list: %s", out)
	}
}

func TestFormatCronHistory(t *testing.T) {
	entries := []CronHistoryEntry{
		{CronID: "c1", TS: 1700000000, MessageID: "m1", Target: "build", Action: "build"},
	}
	out := FormatCronHistory(entries)
	if !strings.Contains(out, "build") {
		t.Error("expected 'build' in output")
	}
	if !strings.Contains(out, "m1") {
		t.Error("expected 'm1' in output")
	}
}

func TestFormatCronHistory_Empty(t *testing.T) {
	out := FormatCronHistory(nil)
	if !strings.Contains(out, "No cron history") {
		t.Errorf("unexpected output: %s", out)
	}
}

func TestCronPath(t *testing.T) {
	p := CronPath("mysession")
	if !strings.Contains(p, "cron.jsonl") {
		t.Errorf("expected cron.jsonl in path, got %s", p)
	}
}

func TestCronHistoryPath(t *testing.T) {
	p := CronHistoryPath("mysession")
	if !strings.Contains(p, "cron-history.jsonl") {
		t.Errorf("expected cron-history.jsonl in path, got %s", p)
	}
}

func TestInit_CreatesCronFile(t *testing.T) {
	session := fmt.Sprintf("test-init-cron-%d", rand.Int())
	memDir := t.TempDir()
	t.Cleanup(func() { _ = Cleanup(session) })

	if err := Init(session, memDir); err != nil {
		t.Fatalf("Init: %v", err)
	}

	if _, err := os.Stat(CronPath(session)); err != nil {
		t.Errorf("cron.jsonl: %v", err)
	}
}
