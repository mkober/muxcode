package bus

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestGetAgentStatus_Idle(t *testing.T) {
	session := testSession(t)

	status := GetAgentStatus(session, "build")
	if status.Role != "build" {
		t.Errorf("role = %q, want %q", status.Role, "build")
	}
	if status.Locked {
		t.Error("expected not locked")
	}
	if status.InboxCount != 0 {
		t.Errorf("inbox_count = %d, want 0", status.InboxCount)
	}
	if status.LastMsgTS != 0 {
		t.Errorf("last_msg_ts = %d, want 0", status.LastMsgTS)
	}
}

func TestGetAgentStatus_Locked(t *testing.T) {
	session := testSession(t)

	if err := Lock(session, "build"); err != nil {
		t.Fatalf("Lock: %v", err)
	}

	status := GetAgentStatus(session, "build")
	if !status.Locked {
		t.Error("expected locked")
	}
}

func TestGetAgentStatus_WithMessages(t *testing.T) {
	session := testSession(t)

	msg := NewMessage("edit", "build", "request", "compile", "build it", "")
	if err := Send(session, msg); err != nil {
		t.Fatalf("Send: %v", err)
	}

	status := GetAgentStatus(session, "build")
	if status.InboxCount != 1 {
		t.Errorf("inbox_count = %d, want 1", status.InboxCount)
	}
	if status.LastMsgTS == 0 {
		t.Error("expected non-zero last_msg_ts")
	}
	if status.LastAction != "compile" {
		t.Errorf("last_action = %q, want %q", status.LastAction, "compile")
	}
	if status.LastPeer != "edit" {
		t.Errorf("last_peer = %q, want %q", status.LastPeer, "edit")
	}
	if status.LastDir != "recv" {
		t.Errorf("last_dir = %q, want %q", status.LastDir, "recv")
	}
}

func TestGetAgentStatus_SentMessage(t *testing.T) {
	session := testSession(t)

	msg := NewMessage("build", "test", "request", "test", "run tests", "")
	if err := Send(session, msg); err != nil {
		t.Fatalf("Send: %v", err)
	}

	status := GetAgentStatus(session, "build")
	if status.LastAction != "test" {
		t.Errorf("last_action = %q, want %q", status.LastAction, "test")
	}
	// When build sent the message, LastPeer should be the target (test)
	if status.LastPeer != "test" {
		t.Errorf("last_peer = %q, want %q", status.LastPeer, "test")
	}
	if status.LastDir != "sent" {
		t.Errorf("last_dir = %q, want %q", status.LastDir, "sent")
	}
}

func TestGetAllAgentStatus(t *testing.T) {
	session := testSession(t)

	statuses := GetAllAgentStatus(session)
	if len(statuses) != len(KnownRoles) {
		t.Fatalf("got %d statuses, want %d", len(statuses), len(KnownRoles))
	}

	// All should be idle with no messages
	for _, s := range statuses {
		if s.Locked {
			t.Errorf("role %s: expected not locked", s.Role)
		}
	}
}

func TestFormatStatusTable(t *testing.T) {
	statuses := []AgentStatus{
		{Role: "edit", Locked: false, InboxCount: 0},
		{Role: "build", Locked: true, InboxCount: 2, LastMsgTS: 1700000000, LastAction: "compile", LastPeer: "edit", LastDir: "recv"},
	}

	table := FormatStatusTable(statuses)
	if !strings.Contains(table, "ROLE") {
		t.Error("missing header")
	}
	if !strings.Contains(table, "edit") {
		t.Error("missing edit row")
	}
	if !strings.Contains(table, "build") {
		t.Error("missing build row")
	}
	if !strings.Contains(table, "busy") {
		t.Error("missing busy state")
	}
	if !strings.Contains(table, "idle") {
		t.Error("missing idle state")
	}
	if !strings.Contains(table, "\u2014") {
		t.Error("missing dash for no-activity role")
	}
	if !strings.Contains(table, "\u2190") {
		t.Error("missing recv arrow for build")
	}
}

func TestFormatStatusTable_SentArrow(t *testing.T) {
	statuses := []AgentStatus{
		{Role: "build", Locked: false, InboxCount: 0, LastMsgTS: 1700000000, LastAction: "test", LastPeer: "test", LastDir: "sent"},
	}

	table := FormatStatusTable(statuses)
	if !strings.Contains(table, "\u2192") {
		t.Error("missing sent arrow")
	}
	if !strings.Contains(table, "test:test") {
		t.Error("missing peer:action")
	}
}

func TestReadLogHistory(t *testing.T) {
	session := testSession(t)

	// Send 5 messages involving build
	for i := 0; i < 5; i++ {
		msg := NewMessage("edit", "build", "request", "compile", "msg", "")
		if err := Send(session, msg); err != nil {
			t.Fatalf("Send %d: %v", i, err)
		}
	}

	// Send a message not involving build
	msg := NewMessage("edit", "test", "request", "test", "unrelated", "")
	if err := Send(session, msg); err != nil {
		t.Fatalf("Send unrelated: %v", err)
	}

	msgs := ReadLogHistory(session, "build", 20)
	if len(msgs) != 5 {
		t.Errorf("got %d messages, want 5", len(msgs))
	}
}

func TestReadLogHistory_Limit(t *testing.T) {
	session := testSession(t)

	for i := 0; i < 10; i++ {
		msg := NewMessage("edit", "build", "request", "compile", "msg", "")
		if err := Send(session, msg); err != nil {
			t.Fatalf("Send %d: %v", i, err)
		}
	}

	msgs := ReadLogHistory(session, "build", 3)
	if len(msgs) != 3 {
		t.Errorf("got %d messages, want 3 (limited)", len(msgs))
	}
}

func TestReadLogHistory_Empty(t *testing.T) {
	session := testSession(t)

	msgs := ReadLogHistory(session, "build", 20)
	if len(msgs) != 0 {
		t.Errorf("got %d messages, want 0", len(msgs))
	}
}

func TestReadLogHistory_BothDirections(t *testing.T) {
	session := testSession(t)

	// Message to build
	msg1 := NewMessage("edit", "build", "request", "compile", "build it", "")
	if err := Send(session, msg1); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// Message from build
	msg2 := NewMessage("build", "edit", "response", "compile", "done", "")
	if err := Send(session, msg2); err != nil {
		t.Fatalf("Send: %v", err)
	}

	msgs := ReadLogHistory(session, "build", 20)
	if len(msgs) != 2 {
		t.Errorf("got %d messages, want 2", len(msgs))
	}
}

func TestFormatHistory(t *testing.T) {
	msgs := []Message{
		{TS: 1700000000, From: "edit", To: "build", Type: "request", Action: "compile", Payload: "build it"},
		{TS: 1700000060, From: "build", To: "edit", Type: "response", Action: "compile", Payload: "done"},
	}

	out := FormatHistory(msgs, "build")
	if !strings.Contains(out, "Message history for build") {
		t.Error("missing header")
	}
	if !strings.Contains(out, "edit") {
		t.Error("missing edit reference")
	}
	if !strings.Contains(out, "build it") {
		t.Error("missing payload")
	}
	if !strings.Contains(out, "\u2192") {
		t.Error("missing arrow")
	}
}

func TestExtractContext(t *testing.T) {
	session := testSession(t)

	msg1 := NewMessage("edit", "build", "request", "compile", "build it", "")
	if err := Send(session, msg1); err != nil {
		t.Fatalf("Send: %v", err)
	}

	msg2 := NewMessage("build", "edit", "response", "compile", "done", "")
	if err := Send(session, msg2); err != nil {
		t.Fatalf("Send: %v", err)
	}

	ctx, err := ExtractContext(session, "build", 20)
	if err != nil {
		t.Fatalf("ExtractContext: %v", err)
	}

	if !strings.Contains(ctx, "## Recent activity for build") {
		t.Error("missing markdown header")
	}
	if !strings.Contains(ctx, "request from edit") {
		t.Error("missing incoming message context")
	}
	if !strings.Contains(ctx, "response to edit") {
		t.Error("missing outgoing message context")
	}
}

func TestExtractContext_Empty(t *testing.T) {
	session := testSession(t)

	ctx, err := ExtractContext(session, "build", 20)
	if err != nil {
		t.Fatalf("ExtractContext: %v", err)
	}
	if ctx != "" {
		t.Errorf("expected empty context, got %q", ctx)
	}
}

func TestFormatStatusJSON(t *testing.T) {
	statuses := []AgentStatus{
		{Role: "edit", Locked: false, InboxCount: 0},
		{Role: "build", Locked: true, InboxCount: 2},
	}

	out, err := FormatStatusJSON(statuses)
	if err != nil {
		t.Fatalf("FormatStatusJSON: %v", err)
	}

	// Verify it's valid JSON
	var parsed []AgentStatus
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(parsed) != 2 {
		t.Errorf("got %d entries, want 2", len(parsed))
	}
	if parsed[0].Role != "edit" {
		t.Errorf("first role = %q, want %q", parsed[0].Role, "edit")
	}
	if !parsed[1].Locked {
		t.Error("second entry should be locked")
	}
}

func TestPreCommitCheck_Clean(t *testing.T) {
	session := testSession(t)

	// All agents idle, no messages — should pass
	if err := PreCommitCheck(session); err != nil {
		t.Errorf("expected nil error for clean state, got: %v", err)
	}
}

func TestPreCommitCheck_PendingInbox(t *testing.T) {
	session := testSession(t)

	// Send a message to the build agent's inbox (leave it unread)
	msg := NewMessage("edit", "build", "request", "build", "build it", "")
	if err := Send(session, msg); err != nil {
		t.Fatalf("Send: %v", err)
	}

	err := PreCommitCheck(session)
	if err == nil {
		t.Fatal("expected error for pending inbox, got nil")
	}
	if !strings.Contains(err.Error(), "build: 1 pending message(s)") {
		t.Errorf("error should mention build pending message, got: %v", err)
	}
	if !strings.Contains(err.Error(), "cannot commit") {
		t.Errorf("error should contain 'cannot commit', got: %v", err)
	}
}

func TestPreCommitCheck_BusyAgent(t *testing.T) {
	session := testSession(t)

	// Lock the test agent
	if err := Lock(session, "test"); err != nil {
		t.Fatalf("Lock: %v", err)
	}

	err := PreCommitCheck(session)
	if err == nil {
		t.Fatal("expected error for busy agent, got nil")
	}
	if !strings.Contains(err.Error(), "test: busy") {
		t.Errorf("error should mention test busy, got: %v", err)
	}
}

func TestPreCommitCheck_RunningProc(t *testing.T) {
	session := testSession(t)

	// Write a running proc entry owned by build
	entries := []ProcEntry{
		{
			ID:        "proc-1",
			PID:       99999,
			Command:   "./build.sh",
			Owner:     "build",
			Status:    "running",
			StartedAt: time.Now().Unix(),
		},
	}
	if err := WriteProcEntries(session, entries); err != nil {
		t.Fatalf("WriteProcEntries: %v", err)
	}

	err := PreCommitCheck(session)
	if err == nil {
		t.Fatal("expected error for running proc, got nil")
	}
	if !strings.Contains(err.Error(), "build: background process running (./build.sh)") {
		t.Errorf("error should mention running proc, got: %v", err)
	}
}

func TestPreCommitCheck_ExcludesEditCommitWatch(t *testing.T) {
	session := testSession(t)

	// Lock excluded roles — should still pass
	for _, role := range []string{"edit", "commit", "watch"} {
		if err := Lock(session, role); err != nil {
			t.Fatalf("Lock %s: %v", role, err)
		}
	}

	// Send messages to excluded roles
	for _, role := range []string{"edit", "commit", "watch"} {
		msg := NewMessage("test", role, "request", "info", "test msg", "")
		if err := Send(session, msg); err != nil {
			t.Fatalf("Send to %s: %v", role, err)
		}
	}

	if err := PreCommitCheck(session); err != nil {
		t.Errorf("expected nil error when only excluded roles have issues, got: %v", err)
	}
}

func TestPreCommitCheck_MultipleIssues(t *testing.T) {
	session := testSession(t)

	// Pending inbox on build
	msg := NewMessage("edit", "build", "request", "build", "build it", "")
	if err := Send(session, msg); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// Busy review agent
	if err := Lock(session, "review"); err != nil {
		t.Fatalf("Lock: %v", err)
	}

	err := PreCommitCheck(session)
	if err == nil {
		t.Fatal("expected error for multiple issues, got nil")
	}
	errStr := err.Error()
	if !strings.Contains(errStr, "build") {
		t.Errorf("error should mention build, got: %v", err)
	}
	if !strings.Contains(errStr, "review: busy") {
		t.Errorf("error should mention review busy, got: %v", err)
	}
}

func TestPreCommitCheck_FinishedProcsIgnored(t *testing.T) {
	session := testSession(t)

	// Write a finished proc entry — should not block
	entries := []ProcEntry{
		{
			ID:         "proc-done",
			PID:        12345,
			Command:    "./build.sh",
			Owner:      "build",
			Status:     "exited",
			ExitCode:   0,
			StartedAt:  time.Now().Unix() - 60,
			FinishedAt: time.Now().Unix(),
		},
	}
	if err := WriteProcEntries(session, entries); err != nil {
		t.Fatalf("WriteProcEntries: %v", err)
	}

	if err := PreCommitCheck(session); err != nil {
		t.Errorf("expected nil error when only finished procs exist, got: %v", err)
	}
}
