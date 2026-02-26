package bus

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAddSubscription(t *testing.T) {
	dir := t.TempDir()
	session := filepath.Base(dir)
	t.Setenv("BUS_SESSION", session)
	os.Setenv("BUS_SESSION", session)
	// Point BusDir to temp by symlinking
	busDir := BusDir(session)
	os.MkdirAll(busDir, 0755)
	defer os.RemoveAll(busDir)

	sub, err := AddSubscription(session, Subscription{
		Event:   "build",
		Outcome: "success",
		Notify:  "docs",
		Message: "Build passed: ${command}",
	})
	if err != nil {
		t.Fatalf("AddSubscription: %v", err)
	}
	if sub.ID == "" {
		t.Error("expected non-empty ID")
	}
	if sub.CreatedAt == 0 {
		t.Error("expected non-zero CreatedAt")
	}
	if !sub.Enabled {
		t.Error("expected Enabled=true")
	}
	if sub.Action != "notify" {
		t.Errorf("expected default action 'notify', got %q", sub.Action)
	}
}

func TestAddSubscription_DefaultMessage(t *testing.T) {
	dir := t.TempDir()
	session := filepath.Base(dir)
	busDir := BusDir(session)
	os.MkdirAll(busDir, 0755)
	defer os.RemoveAll(busDir)

	sub, err := AddSubscription(session, Subscription{
		Event:   "test",
		Outcome: "failure",
		Notify:  "edit",
	})
	if err != nil {
		t.Fatalf("AddSubscription: %v", err)
	}
	if sub.Message != "${event} ${outcome}: ${command}" {
		t.Errorf("expected default message template, got %q", sub.Message)
	}
}

func TestAddSubscription_InvalidRole(t *testing.T) {
	dir := t.TempDir()
	session := filepath.Base(dir)
	busDir := BusDir(session)
	os.MkdirAll(busDir, 0755)
	defer os.RemoveAll(busDir)

	_, err := AddSubscription(session, Subscription{
		Event:   "build",
		Outcome: "success",
		Notify:  "nonexistent-role",
	})
	if err == nil {
		t.Fatal("expected error for unknown role")
	}
	if !strings.Contains(err.Error(), "unknown notify role") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestAddSubscription_InvalidEvent(t *testing.T) {
	dir := t.TempDir()
	session := filepath.Base(dir)
	busDir := BusDir(session)
	os.MkdirAll(busDir, 0755)
	defer os.RemoveAll(busDir)

	_, err := AddSubscription(session, Subscription{
		Event:   "invalid",
		Outcome: "success",
		Notify:  "edit",
	})
	if err == nil {
		t.Fatal("expected error for invalid event")
	}
	if !strings.Contains(err.Error(), "invalid event") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestAddSubscription_InvalidOutcome(t *testing.T) {
	dir := t.TempDir()
	session := filepath.Base(dir)
	busDir := BusDir(session)
	os.MkdirAll(busDir, 0755)
	defer os.RemoveAll(busDir)

	_, err := AddSubscription(session, Subscription{
		Event:   "build",
		Outcome: "maybe",
		Notify:  "edit",
	})
	if err == nil {
		t.Fatal("expected error for invalid outcome")
	}
	if !strings.Contains(err.Error(), "invalid outcome") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestReadWriteSubscriptions(t *testing.T) {
	dir := t.TempDir()
	session := filepath.Base(dir)
	busDir := BusDir(session)
	os.MkdirAll(busDir, 0755)
	defer os.RemoveAll(busDir)

	entries := []Subscription{
		{ID: "sub-1", Event: "build", Outcome: "success", Notify: "docs", Enabled: true},
		{ID: "sub-2", Event: "test", Outcome: "failure", Notify: "edit", Enabled: false},
	}

	if err := WriteSubscriptions(session, entries); err != nil {
		t.Fatalf("WriteSubscriptions: %v", err)
	}

	read, err := ReadSubscriptions(session)
	if err != nil {
		t.Fatalf("ReadSubscriptions: %v", err)
	}
	if len(read) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(read))
	}
	if read[0].ID != "sub-1" || read[1].ID != "sub-2" {
		t.Errorf("unexpected IDs: %s, %s", read[0].ID, read[1].ID)
	}
}

func TestReadSubscriptions_Empty(t *testing.T) {
	dir := t.TempDir()
	session := filepath.Base(dir)
	busDir := BusDir(session)
	os.MkdirAll(busDir, 0755)
	defer os.RemoveAll(busDir)

	// File doesn't exist
	subs, err := ReadSubscriptions(session)
	if err != nil {
		t.Fatalf("ReadSubscriptions: %v", err)
	}
	if len(subs) != 0 {
		t.Errorf("expected 0 entries, got %d", len(subs))
	}
}

func TestRemoveSubscription(t *testing.T) {
	dir := t.TempDir()
	session := filepath.Base(dir)
	busDir := BusDir(session)
	os.MkdirAll(busDir, 0755)
	defer os.RemoveAll(busDir)

	entries := []Subscription{
		{ID: "sub-1", Event: "build", Outcome: "success", Notify: "docs", Enabled: true},
		{ID: "sub-2", Event: "test", Outcome: "failure", Notify: "edit", Enabled: true},
	}
	WriteSubscriptions(session, entries)

	if err := RemoveSubscription(session, "sub-1"); err != nil {
		t.Fatalf("RemoveSubscription: %v", err)
	}

	read, _ := ReadSubscriptions(session)
	if len(read) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(read))
	}
	if read[0].ID != "sub-2" {
		t.Errorf("expected sub-2, got %s", read[0].ID)
	}
}

func TestRemoveSubscription_NotFound(t *testing.T) {
	dir := t.TempDir()
	session := filepath.Base(dir)
	busDir := BusDir(session)
	os.MkdirAll(busDir, 0755)
	defer os.RemoveAll(busDir)

	WriteSubscriptions(session, nil)

	err := RemoveSubscription(session, "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent ID")
	}
}

func TestSetSubscriptionEnabled(t *testing.T) {
	dir := t.TempDir()
	session := filepath.Base(dir)
	busDir := BusDir(session)
	os.MkdirAll(busDir, 0755)
	defer os.RemoveAll(busDir)

	entries := []Subscription{
		{ID: "sub-1", Event: "build", Outcome: "success", Notify: "docs", Enabled: true},
	}
	WriteSubscriptions(session, entries)

	if err := SetSubscriptionEnabled(session, "sub-1", false); err != nil {
		t.Fatalf("SetSubscriptionEnabled: %v", err)
	}

	read, _ := ReadSubscriptions(session)
	if read[0].Enabled {
		t.Error("expected Enabled=false after disable")
	}

	if err := SetSubscriptionEnabled(session, "sub-1", true); err != nil {
		t.Fatalf("SetSubscriptionEnabled: %v", err)
	}

	read, _ = ReadSubscriptions(session)
	if !read[0].Enabled {
		t.Error("expected Enabled=true after enable")
	}
}

func TestSetSubscriptionEnabled_NotFound(t *testing.T) {
	dir := t.TempDir()
	session := filepath.Base(dir)
	busDir := BusDir(session)
	os.MkdirAll(busDir, 0755)
	defer os.RemoveAll(busDir)

	WriteSubscriptions(session, nil)

	err := SetSubscriptionEnabled(session, "nonexistent", true)
	if err == nil {
		t.Fatal("expected error for nonexistent ID")
	}
}

func TestMatchSubscriptions(t *testing.T) {
	subs := []Subscription{
		{ID: "1", Event: "build", Outcome: "success", Notify: "docs", Enabled: true},
		{ID: "2", Event: "build", Outcome: "failure", Notify: "edit", Enabled: true},
		{ID: "3", Event: "test", Outcome: "success", Notify: "docs", Enabled: true},
		{ID: "4", Event: "*", Outcome: "*", Notify: "watch", Enabled: true},
		{ID: "5", Event: "build", Outcome: "success", Notify: "analyze", Enabled: false},
		{ID: "6", Event: "*", Outcome: "failure", Notify: "edit", Enabled: true},
		{ID: "7", Event: "deploy", Outcome: "*", Notify: "watch", Enabled: true},
	}

	tests := []struct {
		name    string
		event   string
		outcome string
		wantIDs []string
	}{
		{"exact match", "build", "success", []string{"1", "4"}},
		{"exact failure", "build", "failure", []string{"2", "4", "6"}},
		{"test success", "test", "success", []string{"3", "4"}},
		{"test failure", "test", "failure", []string{"4", "6"}},
		{"deploy success", "deploy", "success", []string{"4", "7"}},
		{"deploy failure", "deploy", "failure", []string{"4", "6", "7"}},
		{"wildcard event match", "build", "success", []string{"1", "4"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matched := MatchSubscriptions(subs, tt.event, tt.outcome)
			if len(matched) != len(tt.wantIDs) {
				t.Errorf("expected %d matches, got %d", len(tt.wantIDs), len(matched))
				for _, m := range matched {
					t.Logf("  matched: %s (event=%s outcome=%s)", m.ID, m.Event, m.Outcome)
				}
				return
			}
			for i, m := range matched {
				if m.ID != tt.wantIDs[i] {
					t.Errorf("match[%d]: expected ID %s, got %s", i, tt.wantIDs[i], m.ID)
				}
			}
		})
	}
}

func TestMatchSubscriptions_DisabledSkipped(t *testing.T) {
	subs := []Subscription{
		{ID: "1", Event: "build", Outcome: "success", Notify: "docs", Enabled: false},
	}
	matched := MatchSubscriptions(subs, "build", "success")
	if len(matched) != 0 {
		t.Errorf("expected 0 matches for disabled sub, got %d", len(matched))
	}
}

func TestMatchSubscriptions_Empty(t *testing.T) {
	matched := MatchSubscriptions(nil, "build", "success")
	if len(matched) != 0 {
		t.Errorf("expected 0 matches for nil subs, got %d", len(matched))
	}
}

func TestExpandSubscriptionMessage(t *testing.T) {
	tests := []struct {
		name     string
		template string
		event    string
		outcome  string
		exitCode string
		command  string
		want     string
	}{
		{
			"all variables",
			"${event} ${outcome} (exit ${exit_code}): ${command}",
			"build", "success", "0", "go build",
			"build success (exit 0): go build",
		},
		{
			"default template",
			"${event} ${outcome}: ${command}",
			"test", "failure", "1", "go test ./...",
			"test failure: go test ./...",
		},
		{
			"no variables",
			"Build finished!",
			"build", "success", "0", "make",
			"Build finished!",
		},
		{
			"partial variables",
			"${event} done",
			"deploy", "success", "0", "cdk deploy",
			"deploy done",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExpandSubscriptionMessage(tt.template, tt.event, tt.outcome, tt.exitCode, tt.command)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFireSubscriptions(t *testing.T) {
	dir := t.TempDir()
	session := filepath.Base(dir)
	busDir := BusDir(session)
	os.MkdirAll(filepath.Join(busDir, "inbox"), 0755)
	defer os.RemoveAll(busDir)

	// Create inbox files for target roles
	touchFile(InboxPath(session, "docs"))
	touchFile(InboxPath(session, "watch"))
	touchFile(InboxPath(session, "edit"))

	entries := []Subscription{
		{ID: "sub-1", Event: "build", Outcome: "success", Notify: "docs", Action: "notify", Message: "Build passed: ${command}", Enabled: true},
		{ID: "sub-2", Event: "*", Outcome: "*", Notify: "watch", Action: "notify", Message: "${event} ${outcome}", Enabled: true},
		{ID: "sub-3", Event: "build", Outcome: "failure", Notify: "edit", Action: "alert", Message: "Build failed!", Enabled: true},
	}
	WriteSubscriptions(session, entries)

	// Create log file
	touchFile(LogPath(session))

	count, err := FireSubscriptions(session, "build", "build", "success", "0", "go build")
	if err != nil {
		t.Fatalf("FireSubscriptions: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 fired, got %d", count)
	}

	// Verify fire counts were incremented
	updated, _ := ReadSubscriptions(session)
	for _, s := range updated {
		switch s.ID {
		case "sub-1":
			if s.FireCount != 1 {
				t.Errorf("sub-1: expected FireCount=1, got %d", s.FireCount)
			}
		case "sub-2":
			if s.FireCount != 1 {
				t.Errorf("sub-2: expected FireCount=1, got %d", s.FireCount)
			}
		case "sub-3":
			if s.FireCount != 0 {
				t.Errorf("sub-3: expected FireCount=0, got %d", s.FireCount)
			}
		}
	}
}

func TestFireSubscriptions_NoMatch(t *testing.T) {
	dir := t.TempDir()
	session := filepath.Base(dir)
	busDir := BusDir(session)
	os.MkdirAll(busDir, 0755)
	defer os.RemoveAll(busDir)

	entries := []Subscription{
		{ID: "sub-1", Event: "test", Outcome: "failure", Notify: "edit", Enabled: true, Message: "test failed"},
	}
	WriteSubscriptions(session, entries)

	count, err := FireSubscriptions(session, "build", "build", "success", "0", "go build")
	if err != nil {
		t.Fatalf("FireSubscriptions: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 fired, got %d", count)
	}
}

func TestFireSubscriptions_Empty(t *testing.T) {
	dir := t.TempDir()
	session := filepath.Base(dir)
	busDir := BusDir(session)
	os.MkdirAll(busDir, 0755)
	defer os.RemoveAll(busDir)

	// No subscriptions file
	count, err := FireSubscriptions(session, "build", "build", "success", "0", "go build")
	if err != nil {
		t.Fatalf("FireSubscriptions: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 fired, got %d", count)
	}
}

func TestFormatSubscriptionList(t *testing.T) {
	entries := []Subscription{
		{ID: "sub-1", Event: "build", Outcome: "success", Notify: "docs", Action: "notify", Enabled: true, FireCount: 3},
		{ID: "sub-2", Event: "*", Outcome: "*", Notify: "watch", Action: "notify", Enabled: false, FireCount: 0},
	}

	// Enabled only
	out := FormatSubscriptionList(entries, false)
	if !strings.Contains(out, "sub-1") {
		t.Error("expected sub-1 in output")
	}
	if strings.Contains(out, "sub-2") {
		t.Error("did not expect sub-2 in enabled-only output")
	}

	// Show all
	out = FormatSubscriptionList(entries, true)
	if !strings.Contains(out, "sub-1") {
		t.Error("expected sub-1 in output")
	}
	if !strings.Contains(out, "sub-2") {
		t.Error("expected sub-2 in --all output")
	}
}

func TestFormatSubscriptionList_Empty(t *testing.T) {
	out := FormatSubscriptionList(nil, false)
	if !strings.Contains(out, "No enabled subscriptions") {
		t.Errorf("expected empty message, got: %s", out)
	}

	out = FormatSubscriptionList(nil, true)
	if !strings.Contains(out, "No subscriptions") {
		t.Errorf("expected empty message, got: %s", out)
	}
}

func TestAddSubscription_WildcardEvent(t *testing.T) {
	dir := t.TempDir()
	session := filepath.Base(dir)
	busDir := BusDir(session)
	os.MkdirAll(busDir, 0755)
	defer os.RemoveAll(busDir)

	sub, err := AddSubscription(session, Subscription{
		Event:   "*",
		Outcome: "*",
		Notify:  "watch",
	})
	if err != nil {
		t.Fatalf("AddSubscription with wildcards: %v", err)
	}
	if sub.Event != "*" || sub.Outcome != "*" {
		t.Errorf("expected wildcard event/outcome, got %s/%s", sub.Event, sub.Outcome)
	}
}
