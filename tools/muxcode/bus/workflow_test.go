package bus

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkflowStatePath(t *testing.T) {
	useTempBusDir(t)
	path := WorkflowStatePath("test-sess")
	if !filepath.IsAbs(path) {
		t.Errorf("expected absolute path, got %s", path)
	}
	if filepath.Base(path) != "workflow-state.json" {
		t.Errorf("expected workflow-state.json, got %s", filepath.Base(path))
	}
}

func TestReadWorkflowStateEmpty(t *testing.T) {
	useTempBusDir(t)
	entry := ReadWorkflowState("nonexistent-session")
	if entry.State != StateIdle {
		t.Errorf("expected StateIdle, got %s", entry.State)
	}
}

func TestTransitionWorkflow(t *testing.T) {
	useTempBusDir(t)
	session := "test-wf"
	os.MkdirAll(filepath.Join(BusDir(session), "lock"), 0755)

	// Initial transition from idle to editing
	changed := TransitionWorkflow(session, StateEditing, "hook:analyze:edit")
	if !changed {
		t.Error("expected state change from idle to editing")
	}

	entry := ReadWorkflowState(session)
	if entry.State != StateEditing {
		t.Errorf("expected editing, got %s", entry.State)
	}
	if entry.PrevState != StateIdle {
		t.Errorf("expected prev=idle, got %s", entry.PrevState)
	}
	if entry.Trigger != "hook:analyze:edit" {
		t.Errorf("expected trigger hook:analyze:edit, got %s", entry.Trigger)
	}

	// Forward transition: editing → building
	changed = TransitionWorkflow(session, StateBuilding, "hook:bash:build")
	if !changed {
		t.Error("expected state change from editing to building")
	}

	entry = ReadWorkflowState(session)
	if entry.State != StateBuilding {
		t.Errorf("expected building, got %s", entry.State)
	}
	if entry.PrevState != StateEditing {
		t.Errorf("expected prev=editing, got %s", entry.PrevState)
	}

	// Same state — no change
	changed = TransitionWorkflow(session, StateBuilding, "hook:bash:build-retry")
	if changed {
		t.Error("expected no state change for same state")
	}
}

func TestTransitionWorkflowRegression(t *testing.T) {
	useTempBusDir(t)
	session := "test-wf-regress"
	os.MkdirAll(filepath.Join(BusDir(session), "lock"), 0755)

	// Build up to reviewed state with outcomes
	TransitionWorkflow(session, StateEditing, "hook:analyze:edit")
	TransitionWorkflow(session, StateBuilding, "hook:bash:build")
	TransitionWorkflow(session, StateTesting, "chain:build:success",
		WithOutcome("build", "success"))
	TransitionWorkflow(session, StateReviewing, "chain:test:success",
		WithOutcome("test", "success"))
	TransitionWorkflow(session, StateReviewed, "daemon:review-complete",
		WithOutcome("review", "lgtm"))

	entry := ReadWorkflowState(session)
	if entry.BuildOutcome != "success" || entry.TestOutcome != "success" || entry.ReviewOutcome != "lgtm" {
		t.Error("outcomes not accumulated correctly before regression")
	}

	// Regress to editing — should clear all outcomes
	changed := TransitionWorkflow(session, StateEditing, "hook:analyze:edit")
	if !changed {
		t.Error("expected state change on regression")
	}

	entry = ReadWorkflowState(session)
	if entry.State != StateEditing {
		t.Errorf("expected editing, got %s", entry.State)
	}
	if entry.BuildOutcome != "" || entry.TestOutcome != "" || entry.ReviewOutcome != "" || entry.DeployOutcome != "" {
		t.Error("outcomes should be cleared on regression to editing")
	}
}

func TestTransitionWorkflowWithFiles(t *testing.T) {
	useTempBusDir(t)
	session := "test-wf-files"
	os.MkdirAll(filepath.Join(BusDir(session), "lock"), 0755)

	files := []string{"bus/hook.go", "bus/config.go", "cmd/hook.go"}
	TransitionWorkflow(session, StateEditing, "hook:analyze:edit", WithFiles(files))

	entry := ReadWorkflowState(session)
	if entry.FilesChanged != 3 {
		t.Errorf("expected 3 files changed, got %d", entry.FilesChanged)
	}
	if len(entry.LastFiles) != 3 {
		t.Errorf("expected 3 last files, got %d", len(entry.LastFiles))
	}
}

func TestTransitionWorkflowWithOutcome(t *testing.T) {
	useTempBusDir(t)
	session := "test-wf-outcome"
	os.MkdirAll(filepath.Join(BusDir(session), "lock"), 0755)

	TransitionWorkflow(session, StateEditing, "hook:analyze:edit")
	TransitionWorkflow(session, StateBuilding, "hook:bash:build")
	TransitionWorkflow(session, StateTesting, "chain:build:success",
		WithOutcome("build", "success"))

	entry := ReadWorkflowState(session)
	if entry.BuildOutcome != "success" {
		t.Errorf("expected build outcome success, got %q", entry.BuildOutcome)
	}
	if entry.TestOutcome != "" {
		t.Errorf("expected empty test outcome, got %q", entry.TestOutcome)
	}
}

func TestFormatWorkflowState(t *testing.T) {
	entry := WorkflowStateEntry{
		State:        StateTesting,
		Trigger:      "chain:build:success",
		FilesChanged: 3,
		BuildOutcome: "success",
	}

	result := FormatWorkflowState(entry)
	if result == "" {
		t.Error("expected non-empty formatted state")
	}
	if !strings.Contains(result, "testing") {
		t.Errorf("expected 'testing' in result: %s", result)
	}
	if !strings.Contains(result, "chain:build:success") {
		t.Errorf("expected trigger in result: %s", result)
	}
}

func TestFormatWorkflowStateCompact(t *testing.T) {
	entry := WorkflowStateEntry{
		State:        StateTesting,
		Trigger:      "chain:build:success",
		FilesChanged: 3,
	}

	result := FormatWorkflowStateCompact(entry, 80)
	if result == "" {
		t.Error("expected non-empty compact state")
	}
	// Should contain the bullet and state name (with ANSI codes)
	if !strings.Contains(result, "testing") {
		t.Errorf("expected 'testing' in compact result: %s", result)
	}
}

func TestWorkflowStateJSON(t *testing.T) {
	entry := WorkflowStateEntry{
		State:     StateBuilding,
		PrevState: StateEditing,
		Trigger:   "hook:bash:build",
	}

	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var decoded WorkflowStateEntry
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if decoded.State != StateBuilding {
		t.Errorf("expected building, got %s", decoded.State)
	}
	if decoded.PrevState != StateEditing {
		t.Errorf("expected editing, got %s", decoded.PrevState)
	}
}

// appendInboxMessage writes a message straight into a role's inbox file,
// bypassing Send so tests control exactly what sits unconsumed.
func appendInboxMessage(t *testing.T, session, role string, m Message) {
	t.Helper()
	data, err := EncodeMessage(m)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	f, err := os.OpenFile(InboxPath(session, role), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("open inbox: %v", err)
	}
	defer f.Close()
	if _, err := f.Write(append(data, '\n')); err != nil {
		t.Fatalf("write inbox: %v", err)
	}
}

func TestNewestMessageIDFrom(t *testing.T) {
	useTempBusDir(t)
	session := "test-newest-msg"
	os.MkdirAll(filepath.Join(BusDir(session), "inbox"), 0755)
	os.MkdirAll(filepath.Join(BusDir(session), "lock"), 0755)

	if id := NewestMessageIDFrom(session, "edit", "review"); id != "" {
		t.Errorf("expected empty ID with empty inbox, got %q", id)
	}

	// A CC copy (review→test) in edit's inbox is not a completion report
	cc := NewMessage("review", "test", "response", "review", "review of test task", "test-req-1")
	appendInboxMessage(t, session, "edit", cc)
	if id := NewestMessageIDFrom(session, "edit", "review"); id != "" {
		t.Errorf("CC copy addressed to test must not match, got %q", id)
	}

	m1 := NewMessage("review", "edit", "response", "review", "LGTM", "req-1")
	appendInboxMessage(t, session, "edit", m1)
	if id := NewestMessageIDFrom(session, "edit", "review"); id != m1.ID {
		t.Errorf("expected %q, got %q", m1.ID, id)
	}

	// A second completion supersedes the first
	m2 := NewMessage("review", "edit", "response", "review", "LGTM again", "req-2")
	appendInboxMessage(t, session, "edit", m2)
	if id := NewestMessageIDFrom(session, "edit", "review"); id != m2.ID {
		t.Errorf("expected newest %q, got %q", m2.ID, id)
	}

	// Later mail from another sender does not change the newest review ID
	b := NewMessage("build", "edit", "response", "build", "Build OK", "req-3")
	appendInboxMessage(t, session, "edit", b)
	if id := NewestMessageIDFrom(session, "edit", "review"); id != m2.ID {
		t.Errorf("unrelated sender changed result: expected %q, got %q", m2.ID, id)
	}
	if id := NewestMessageIDFrom(session, "edit", "deploy"); id != "" {
		t.Errorf("expected empty ID for sender with no messages, got %q", id)
	}
}

func TestReviewedMarkerRoundtrip(t *testing.T) {
	useTempBusDir(t)
	session := "test-reviewed-marker"
	os.MkdirAll(BusDir(session), 0755)

	if got := ReadReviewedMarker(session); got != "" {
		t.Errorf("expected empty marker before first write, got %q", got)
	}
	if err := WriteReviewedMarker(session, "12345-review-abcd1234"); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	if got := ReadReviewedMarker(session); got != "12345-review-abcd1234" {
		t.Errorf("expected marker roundtrip, got %q", got)
	}
	if err := WriteReviewedMarker(session, "12399-review-ffff0000"); err != nil {
		t.Fatalf("overwrite marker: %v", err)
	}
	if got := ReadReviewedMarker(session); got != "12399-review-ffff0000" {
		t.Errorf("expected overwritten marker, got %q", got)
	}
}
