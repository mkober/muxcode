package bus

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAddSubscription(t *testing.T) {
	useTempBusDir(t)

	session := "test-add-sub"
	t.Setenv("BUS_SESSION", session)
	busDir := BusDir(session)
	os.MkdirAll(busDir, 0755)

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
	useTempBusDir(t)

	session := "test-default-msg"
	busDir := BusDir(session)
	os.MkdirAll(busDir, 0755)

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
	useTempBusDir(t)

	session := "test-invalid-role"
	busDir := BusDir(session)
	os.MkdirAll(busDir, 0755)

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
	useTempBusDir(t)

	session := "test-invalid-event"
	busDir := BusDir(session)
	os.MkdirAll(busDir, 0755)

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
	useTempBusDir(t)

	session := "test-invalid-outcome"
	busDir := BusDir(session)
	os.MkdirAll(busDir, 0755)

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
	useTempBusDir(t)

	session := "test-rw-subs"
	busDir := BusDir(session)
	os.MkdirAll(busDir, 0755)

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
	useTempBusDir(t)

	session := "test-read-empty"
	busDir := BusDir(session)
	os.MkdirAll(busDir, 0755)

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
	useTempBusDir(t)

	session := "test-remove-sub"
	busDir := BusDir(session)
	os.MkdirAll(busDir, 0755)

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
	useTempBusDir(t)

	session := "test-remove-notfound"
	busDir := BusDir(session)
	os.MkdirAll(busDir, 0755)

	WriteSubscriptions(session, nil)

	err := RemoveSubscription(session, "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent ID")
	}
}

func TestSetSubscriptionEnabled(t *testing.T) {
	useTempBusDir(t)

	session := "test-set-enabled"
	busDir := BusDir(session)
	os.MkdirAll(busDir, 0755)

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
	useTempBusDir(t)

	session := "test-enabled-notfound"
	busDir := BusDir(session)
	os.MkdirAll(busDir, 0755)

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
			matched := MatchSubscriptions(subs, tt.event, tt.outcome, nil)
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
	matched := MatchSubscriptions(subs, "build", "success", nil)
	if len(matched) != 0 {
		t.Errorf("expected 0 matches for disabled sub, got %d", len(matched))
	}
}

func TestMatchSubscriptions_Empty(t *testing.T) {
	matched := MatchSubscriptions(nil, "build", "success", nil)
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
			got := ExpandSubscriptionMessage(tt.template, tt.event, tt.outcome, tt.exitCode, tt.command, nil)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFireSubscriptions(t *testing.T) {
	useTempBusDir(t)

	session := "test-fire-subs"
	busDir := BusDir(session)
	os.MkdirAll(filepath.Join(busDir, "inbox"), 0755)

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

	count, err := FireSubscriptions(session, "build", "build", "success", "0", "go build", nil)
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
	useTempBusDir(t)

	session := "test-fire-nomatch"
	busDir := BusDir(session)
	os.MkdirAll(busDir, 0755)

	entries := []Subscription{
		{ID: "sub-1", Event: "test", Outcome: "failure", Notify: "edit", Enabled: true, Message: "test failed"},
	}
	WriteSubscriptions(session, entries)

	count, err := FireSubscriptions(session, "build", "build", "success", "0", "go build", nil)
	if err != nil {
		t.Fatalf("FireSubscriptions: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 fired, got %d", count)
	}
}

func TestFireSubscriptions_Empty(t *testing.T) {
	useTempBusDir(t)

	session := "test-fire-empty"
	busDir := BusDir(session)
	os.MkdirAll(busDir, 0755)

	// No subscriptions file
	count, err := FireSubscriptions(session, "build", "build", "success", "0", "go build", nil)
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
	useTempBusDir(t)

	session := "test-wildcard-event"
	busDir := BusDir(session)
	os.MkdirAll(busDir, 0755)

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

// --- Phase 4: subscription conditions ---

func TestMatchSubscriptions_WithConditions(t *testing.T) {
	subs := []Subscription{
		{
			ID: "1", Event: "build", Outcome: "success", Notify: "deploy",
			Enabled: true,
			Conditions: map[string]any{
				"files_match":  "lib/**/*.ts",
				"branch_match": "^main$",
			},
		},
		{
			ID: "2", Event: "build", Outcome: "success", Notify: "docs",
			Enabled: true,
			// No conditions — always matches
		},
	}

	// Context matches conditions
	ctx := &ChainContext{
		ChangedFiles: []string{"lib/constructs/stack.ts"},
		Branch:       "main",
	}
	matched := MatchSubscriptions(subs, "build", "success", ctx)
	if len(matched) != 2 {
		t.Errorf("expected 2 matches with matching context, got %d", len(matched))
	}

	// Context doesn't match — wrong branch
	ctx2 := &ChainContext{
		ChangedFiles: []string{"lib/constructs/stack.ts"},
		Branch:       "feat/test",
	}
	matched2 := MatchSubscriptions(subs, "build", "success", ctx2)
	if len(matched2) != 1 {
		t.Errorf("expected 1 match with non-matching branch, got %d", len(matched2))
	}
	if matched2[0].ID != "2" {
		t.Errorf("expected unconditional sub '2', got %q", matched2[0].ID)
	}
}

func TestMatchSubscriptions_ConditionsNoContext(t *testing.T) {
	// When ctx is nil, conditions are skipped — all match
	subs := []Subscription{
		{
			ID: "1", Event: "build", Outcome: "success", Notify: "deploy",
			Enabled: true,
			Conditions: map[string]any{
				"branch_match": "^main$",
			},
		},
	}

	matched := MatchSubscriptions(subs, "build", "success", nil)
	if len(matched) != 1 {
		t.Errorf("nil ctx should skip conditions, expected 1 match, got %d", len(matched))
	}
}

func TestMatchSubscriptions_ConditionsEnvSet(t *testing.T) {
	subs := []Subscription{
		{
			ID: "1", Event: "test", Outcome: "failure", Notify: "edit",
			Enabled: true,
			Conditions: map[string]any{
				"env_set": "NOTIFY_ON_FAILURE",
			},
		},
	}

	ctx := &ChainContext{}

	// Env not set
	t.Setenv("NOTIFY_ON_FAILURE", "")
	matched := MatchSubscriptions(subs, "test", "failure", ctx)
	if len(matched) != 0 {
		t.Errorf("expected 0 matches with unset env, got %d", len(matched))
	}

	// Env set
	t.Setenv("NOTIFY_ON_FAILURE", "1")
	matched = MatchSubscriptions(subs, "test", "failure", ctx)
	if len(matched) != 1 {
		t.Errorf("expected 1 match with set env, got %d", len(matched))
	}
}

func TestExpandSubscriptionMessage_WithContext(t *testing.T) {
	ctx := &ChainContext{
		ChangedFiles: []string{"lib/stack.ts", "bin/app.ts"},
		Branch:       "feat/deploy",
	}

	template := "${event} ${outcome} on ${branch}: ${changed_files}"
	got := ExpandSubscriptionMessage(template, "build", "success", "0", "make", ctx)
	want := "build success on feat/deploy: lib/stack.ts, bin/app.ts"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestExpandSubscriptionMessage_NilContext(t *testing.T) {
	template := "Event on ${branch} with ${changed_files}"
	got := ExpandSubscriptionMessage(template, "build", "success", "0", "make", nil)
	want := "Event on  with "
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestAddSubscription_WithConditions(t *testing.T) {
	useTempBusDir(t)

	session := "test-add-sub-cond"
	busDir := BusDir(session)
	os.MkdirAll(busDir, 0755)

	sub, err := AddSubscription(session, Subscription{
		Event:   "build",
		Outcome: "success",
		Notify:  "deploy",
		Message: "Deploy on ${branch}",
		Conditions: map[string]any{
			"files_match":  "lib/**/*.ts",
			"branch_match": "^main$",
		},
	})
	if err != nil {
		t.Fatalf("AddSubscription with conditions: %v", err)
	}
	if len(sub.Conditions) != 2 {
		t.Errorf("expected 2 conditions, got %d", len(sub.Conditions))
	}

	// Verify conditions survive read/write roundtrip
	subs, err := ReadSubscriptions(session)
	if err != nil {
		t.Fatalf("ReadSubscriptions: %v", err)
	}
	if len(subs) != 1 {
		t.Fatalf("expected 1 subscription, got %d", len(subs))
	}
	if len(subs[0].Conditions) != 2 {
		t.Errorf("conditions not persisted: expected 2, got %d", len(subs[0].Conditions))
	}
	if subs[0].Conditions["files_match"] != "lib/**/*.ts" {
		t.Errorf("files_match condition not preserved: %v", subs[0].Conditions["files_match"])
	}
}

func TestFireSubscriptions_WithConditions(t *testing.T) {
	useTempBusDir(t)

	session := "test-fire-cond"
	busDir := BusDir(session)
	os.MkdirAll(filepath.Join(busDir, "inbox"), 0755)

	touchFile(InboxPath(session, "deploy"))
	touchFile(InboxPath(session, "docs"))
	touchFile(LogPath(session))

	entries := []Subscription{
		{
			ID: "sub-1", Event: "build", Outcome: "success", Notify: "deploy",
			Action: "deploy", Message: "Deploy infra", Enabled: true,
			Conditions: map[string]any{"files_match": "lib/**/*.ts"},
		},
		{
			ID: "sub-2", Event: "build", Outcome: "success", Notify: "docs",
			Action: "notify", Message: "Build passed", Enabled: true,
			// No conditions — always fires
		},
	}
	WriteSubscriptions(session, entries)

	// Context with matching files
	ctx := &ChainContext{ChangedFiles: []string{"lib/stack.ts"}, Branch: "main"}
	count, err := FireSubscriptions(session, "build", "build", "success", "0", "make", ctx)
	if err != nil {
		t.Fatalf("FireSubscriptions: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 fired (both match), got %d", count)
	}
}

func TestFireSubscriptions_ConditionsFilter(t *testing.T) {
	useTempBusDir(t)

	session := "test-fire-cond-filter"
	busDir := BusDir(session)
	os.MkdirAll(filepath.Join(busDir, "inbox"), 0755)

	touchFile(InboxPath(session, "deploy"))
	touchFile(InboxPath(session, "docs"))
	touchFile(LogPath(session))

	entries := []Subscription{
		{
			ID: "sub-1", Event: "build", Outcome: "success", Notify: "deploy",
			Action: "deploy", Message: "Deploy infra", Enabled: true,
			Conditions: map[string]any{"files_match": "lib/**/*.ts"},
		},
		{
			ID: "sub-2", Event: "build", Outcome: "success", Notify: "docs",
			Action: "notify", Message: "Build passed", Enabled: true,
		},
	}
	WriteSubscriptions(session, entries)

	// Context without matching files — only unconditional sub fires
	ctx := &ChainContext{ChangedFiles: []string{"README.md"}, Branch: "main"}
	count, err := FireSubscriptions(session, "build", "build", "success", "0", "make", ctx)
	if err != nil {
		t.Fatalf("FireSubscriptions: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 fired (only unconditional), got %d", count)
	}
}
