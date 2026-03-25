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
	TransitionWorkflow(session, StateReviewed, "watcher:review-complete",
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

func TestHasNewMessageFrom(t *testing.T) {
	useTempBusDir(t)
	session := "test-has-msg"
	os.MkdirAll(filepath.Join(BusDir(session), "inbox"), 0755)
	os.MkdirAll(filepath.Join(BusDir(session), "lock"), 0755)

	// No messages — should return false
	if HasNewMessageFrom(session, "edit", "review") {
		t.Error("expected false with empty inbox")
	}

	// Add a message from review
	msg := NewMessage("review", "edit", "response", "review", "LGTM", "req-1")
	data, _ := EncodeMessage(msg)
	os.WriteFile(InboxPath(session, "edit"), append(data, '\n'), 0644)

	if !HasNewMessageFrom(session, "edit", "review") {
		t.Error("expected true with review message in inbox")
	}

	// Check for message from a different sender
	if HasNewMessageFrom(session, "edit", "build") {
		t.Error("expected false for build sender")
	}
}
