package bus

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProcPaths(t *testing.T) {
	session := "test-session"

	p := ProcDir(session)
	if !strings.HasSuffix(p, "/proc") {
		t.Errorf("ProcDir: expected path ending in /proc, got %s", p)
	}

	p = ProcPath(session)
	if !strings.HasSuffix(p, "proc.jsonl") {
		t.Errorf("ProcPath: expected path ending in proc.jsonl, got %s", p)
	}

	p = ProcLogPath(session, "my-id")
	if !strings.HasSuffix(p, "proc/my-id.log") {
		t.Errorf("ProcLogPath: expected path ending in proc/my-id.log, got %s", p)
	}
}

func TestReadWriteProcEntries(t *testing.T) {
	session := fmt.Sprintf("test-proc-rw-%d", rand.Int())
	memDir := t.TempDir()
	t.Cleanup(func() { _ = Cleanup(session) })

	if err := Init(session, memDir); err != nil {
		t.Fatalf("Init: %v", err)
	}

	// Initially empty
	entries, err := ReadProcEntries(session)
	if err != nil {
		t.Fatalf("ReadProcEntries: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}

	// Write entries
	testEntries := []ProcEntry{
		{ID: "p1", PID: 12345, Command: "sleep 10", Owner: "build", Status: "running", StartedAt: time.Now().Unix()},
		{ID: "p2", PID: 12346, Command: "echo done", Owner: "test", Status: "exited", ExitCode: 0, StartedAt: time.Now().Unix()},
	}
	if err := WriteProcEntries(session, testEntries); err != nil {
		t.Fatalf("WriteProcEntries: %v", err)
	}

	// Read back
	entries, err = ReadProcEntries(session)
	if err != nil {
		t.Fatalf("ReadProcEntries: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].ID != "p1" || entries[1].ID != "p2" {
		t.Errorf("unexpected entry IDs: %s, %s", entries[0].ID, entries[1].ID)
	}
}

func TestReadProcEntries_NotExist(t *testing.T) {
	entries, err := ReadProcEntries("nonexistent-session-proc-12345")
	if err != nil {
		t.Fatalf("ReadProcEntries: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
}

func TestGetProcEntry(t *testing.T) {
	session := fmt.Sprintf("test-proc-get-%d", rand.Int())
	memDir := t.TempDir()
	t.Cleanup(func() { _ = Cleanup(session) })
	_ = Init(session, memDir)

	testEntries := []ProcEntry{
		{ID: "p1", PID: 100, Command: "cmd1", Owner: "build", Status: "running"},
		{ID: "p2", PID: 200, Command: "cmd2", Owner: "test", Status: "exited"},
	}
	_ = WriteProcEntries(session, testEntries)

	e, err := GetProcEntry(session, "p2")
	if err != nil {
		t.Fatalf("GetProcEntry: %v", err)
	}
	if e.PID != 200 {
		t.Errorf("expected PID 200, got %d", e.PID)
	}

	_, err = GetProcEntry(session, "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent entry")
	}
}

func TestUpdateProcEntry(t *testing.T) {
	session := fmt.Sprintf("test-proc-update-%d", rand.Int())
	memDir := t.TempDir()
	t.Cleanup(func() { _ = Cleanup(session) })
	_ = Init(session, memDir)

	testEntries := []ProcEntry{
		{ID: "p1", PID: 100, Status: "running"},
	}
	_ = WriteProcEntries(session, testEntries)

	err := UpdateProcEntry(session, "p1", func(e *ProcEntry) {
		e.Status = "exited"
		e.ExitCode = 0
		e.FinishedAt = time.Now().Unix()
	})
	if err != nil {
		t.Fatalf("UpdateProcEntry: %v", err)
	}

	e, _ := GetProcEntry(session, "p1")
	if e.Status != "exited" {
		t.Errorf("expected status 'exited', got %s", e.Status)
	}
	if e.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", e.ExitCode)
	}
}

func TestUpdateProcEntry_NotFound(t *testing.T) {
	session := fmt.Sprintf("test-proc-upnf-%d", rand.Int())
	memDir := t.TempDir()
	t.Cleanup(func() { _ = Cleanup(session) })
	_ = Init(session, memDir)

	err := UpdateProcEntry(session, "nonexistent", func(e *ProcEntry) {
		e.Status = "exited"
	})
	if err == nil {
		t.Fatal("expected error for nonexistent entry")
	}
}

func TestRemoveProcEntry(t *testing.T) {
	session := fmt.Sprintf("test-proc-rm-%d", rand.Int())
	memDir := t.TempDir()
	t.Cleanup(func() { _ = Cleanup(session) })
	_ = Init(session, memDir)

	testEntries := []ProcEntry{
		{ID: "p1", Status: "running"},
		{ID: "p2", Status: "exited"},
	}
	_ = WriteProcEntries(session, testEntries)

	if err := RemoveProcEntry(session, "p1"); err != nil {
		t.Fatalf("RemoveProcEntry: %v", err)
	}

	entries, _ := ReadProcEntries(session)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].ID != "p2" {
		t.Errorf("expected remaining entry p2, got %s", entries[0].ID)
	}
}

func TestRemoveProcEntry_NotFound(t *testing.T) {
	session := fmt.Sprintf("test-proc-rmnf-%d", rand.Int())
	memDir := t.TempDir()
	t.Cleanup(func() { _ = Cleanup(session) })
	_ = Init(session, memDir)

	err := RemoveProcEntry(session, "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent entry")
	}
}

func TestStartProc(t *testing.T) {
	session := fmt.Sprintf("test-proc-start-%d", rand.Int())
	memDir := t.TempDir()
	t.Cleanup(func() { _ = Cleanup(session) })
	_ = Init(session, memDir)

	entry, err := StartProc(session, "echo hello", "/tmp", "build")
	if err != nil {
		t.Fatalf("StartProc: %v", err)
	}

	if entry.ID == "" {
		t.Error("expected non-empty ID")
	}
	if entry.PID <= 0 {
		t.Errorf("expected positive PID, got %d", entry.PID)
	}
	if entry.Status != "running" {
		t.Errorf("expected status 'running', got %s", entry.Status)
	}
	if entry.Owner != "build" {
		t.Errorf("expected owner 'build', got %s", entry.Owner)
	}
	if entry.Command != "echo hello" {
		t.Errorf("expected command 'echo hello', got %s", entry.Command)
	}
	if entry.StartedAt == 0 {
		t.Error("expected non-zero StartedAt")
	}

	// Verify persisted
	entries, _ := ReadProcEntries(session)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}

	// Wait for the process to complete
	time.Sleep(500 * time.Millisecond)

	// Verify log file was created
	if _, err := os.Stat(entry.LogFile); err != nil {
		t.Errorf("log file not found: %v", err)
	}
}

func TestCheckProcAlive(t *testing.T) {
	// Current process should be alive
	if !CheckProcAlive(os.Getpid()) {
		t.Error("expected current process to be alive")
	}

	// Non-existent PID should not be alive
	if CheckProcAlive(999999999) {
		t.Error("expected non-existent PID to not be alive")
	}

	// Invalid PID
	if CheckProcAlive(0) {
		t.Error("expected PID 0 to return false")
	}
	if CheckProcAlive(-1) {
		t.Error("expected PID -1 to return false")
	}
}

func TestRefreshProcStatus(t *testing.T) {
	session := fmt.Sprintf("test-proc-refresh-%d", rand.Int())
	memDir := t.TempDir()
	t.Cleanup(func() { _ = Cleanup(session) })
	_ = Init(session, memDir)

	// Start a short-lived process
	entry, err := StartProc(session, "echo hello-refresh", "/tmp", "build")
	if err != nil {
		t.Fatalf("StartProc: %v", err)
	}

	// Wait for it to finish
	time.Sleep(1 * time.Second)

	completed, err := RefreshProcStatus(session)
	if err != nil {
		t.Fatalf("RefreshProcStatus: %v", err)
	}

	if len(completed) != 1 {
		t.Fatalf("expected 1 completed entry, got %d", len(completed))
	}
	if completed[0].ID != entry.ID {
		t.Errorf("expected completed ID %s, got %s", entry.ID, completed[0].ID)
	}
	if completed[0].Status != "exited" {
		t.Errorf("expected status 'exited', got %s", completed[0].Status)
	}
	if completed[0].ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", completed[0].ExitCode)
	}

	// Verify persisted
	e, _ := GetProcEntry(session, entry.ID)
	if e.Status != "exited" {
		t.Errorf("persisted status: expected 'exited', got %s", e.Status)
	}
}

func TestRefreshProcStatus_FailedProcess(t *testing.T) {
	session := fmt.Sprintf("test-proc-fail-%d", rand.Int())
	memDir := t.TempDir()
	t.Cleanup(func() { _ = Cleanup(session) })
	_ = Init(session, memDir)

	// Start a process that exits with non-zero
	_, err := StartProc(session, "exit 42", "/tmp", "build")
	if err != nil {
		t.Fatalf("StartProc: %v", err)
	}

	time.Sleep(1 * time.Second)

	completed, err := RefreshProcStatus(session)
	if err != nil {
		t.Fatalf("RefreshProcStatus: %v", err)
	}

	if len(completed) != 1 {
		t.Fatalf("expected 1 completed, got %d", len(completed))
	}
	if completed[0].Status != "failed" {
		t.Errorf("expected status 'failed', got %s", completed[0].Status)
	}
	if completed[0].ExitCode != 42 {
		t.Errorf("expected exit code 42, got %d", completed[0].ExitCode)
	}
}

func TestRefreshProcStatus_NoRunning(t *testing.T) {
	session := fmt.Sprintf("test-proc-norun-%d", rand.Int())
	memDir := t.TempDir()
	t.Cleanup(func() { _ = Cleanup(session) })
	_ = Init(session, memDir)

	// Write a non-running entry
	_ = WriteProcEntries(session, []ProcEntry{
		{ID: "p1", Status: "exited"},
	})

	completed, err := RefreshProcStatus(session)
	if err != nil {
		t.Fatalf("RefreshProcStatus: %v", err)
	}
	if len(completed) != 0 {
		t.Errorf("expected 0 completed, got %d", len(completed))
	}
}

func TestExtractExitCode(t *testing.T) {
	tmpDir := t.TempDir()

	// Successful exit
	f1 := filepath.Join(tmpDir, "success.log")
	os.WriteFile(f1, []byte("some output\nEXIT_CODE:0\n"), 0644)
	code, ok := extractExitCode(f1)
	if !ok {
		t.Error("expected exit code to be found")
	}
	if code != 0 {
		t.Errorf("expected 0, got %d", code)
	}

	// Failed exit
	f2 := filepath.Join(tmpDir, "fail.log")
	os.WriteFile(f2, []byte("error output\nEXIT_CODE:1\n"), 0644)
	code, ok = extractExitCode(f2)
	if !ok {
		t.Error("expected exit code to be found")
	}
	if code != 1 {
		t.Errorf("expected 1, got %d", code)
	}

	// No sentinel
	f3 := filepath.Join(tmpDir, "nosent.log")
	os.WriteFile(f3, []byte("just output\n"), 0644)
	_, ok = extractExitCode(f3)
	if ok {
		t.Error("expected exit code not to be found")
	}

	// Non-existent file
	_, ok = extractExitCode(filepath.Join(tmpDir, "nope.log"))
	if ok {
		t.Error("expected exit code not to be found for missing file")
	}

	// Empty file
	f5 := filepath.Join(tmpDir, "empty.log")
	os.WriteFile(f5, []byte(""), 0644)
	_, ok = extractExitCode(f5)
	if ok {
		t.Error("expected exit code not to be found for empty file")
	}
}

func TestCleanFinished(t *testing.T) {
	session := fmt.Sprintf("test-proc-clean-%d", rand.Int())
	memDir := t.TempDir()
	t.Cleanup(func() { _ = Cleanup(session) })
	_ = Init(session, memDir)

	// Create log files
	log1 := ProcLogPath(session, "p1")
	log2 := ProcLogPath(session, "p2")
	log3 := ProcLogPath(session, "p3")
	_ = os.WriteFile(log1, []byte("log1"), 0644)
	_ = os.WriteFile(log2, []byte("log2"), 0644)
	_ = os.WriteFile(log3, []byte("log3"), 0644)

	testEntries := []ProcEntry{
		{ID: "p1", Status: "running", LogFile: log1},
		{ID: "p2", Status: "exited", LogFile: log2},
		{ID: "p3", Status: "failed", LogFile: log3},
	}
	_ = WriteProcEntries(session, testEntries)

	removed, err := CleanFinished(session)
	if err != nil {
		t.Fatalf("CleanFinished: %v", err)
	}
	if removed != 2 {
		t.Errorf("expected 2 removed, got %d", removed)
	}

	entries, _ := ReadProcEntries(session)
	if len(entries) != 1 {
		t.Fatalf("expected 1 remaining entry, got %d", len(entries))
	}
	if entries[0].ID != "p1" {
		t.Errorf("expected remaining entry p1, got %s", entries[0].ID)
	}

	// Log files for finished entries should be removed
	if _, err := os.Stat(log2); !os.IsNotExist(err) {
		t.Error("expected log2 to be removed")
	}
	if _, err := os.Stat(log3); !os.IsNotExist(err) {
		t.Error("expected log3 to be removed")
	}
	// Running entry's log should still exist
	if _, err := os.Stat(log1); err != nil {
		t.Error("expected log1 to still exist")
	}
}

func TestFormatProcList(t *testing.T) {
	entries := []ProcEntry{
		{ID: "p1", PID: 12345, Status: "running", Owner: "build", StartedAt: time.Now().Unix(), Command: "./build.sh"},
		{ID: "p2", PID: 12346, Status: "exited", Owner: "deploy", StartedAt: time.Now().Unix(), Command: "cdk deploy"},
	}

	// showAll=false: only running
	out := FormatProcList(entries, false)
	if !strings.Contains(out, "p1") {
		t.Error("expected p1 in output")
	}
	if strings.Contains(out, "p2") {
		t.Error("expected p2 to be hidden when showAll=false")
	}

	// showAll=true: all entries
	out = FormatProcList(entries, true)
	if !strings.Contains(out, "p1") {
		t.Error("expected p1 in output")
	}
	if !strings.Contains(out, "p2") {
		t.Error("expected p2 in output when showAll=true")
	}
}

func TestFormatProcList_Empty(t *testing.T) {
	out := FormatProcList(nil, false)
	if !strings.Contains(out, "No running processes") {
		t.Errorf("unexpected output for empty list: %s", out)
	}

	out = FormatProcList(nil, true)
	if !strings.Contains(out, "No processes") {
		t.Errorf("unexpected output for empty --all list: %s", out)
	}
}

func TestFormatProcList_LongCommand(t *testing.T) {
	entries := []ProcEntry{
		{ID: "p1", PID: 123, Status: "running", Owner: "build", StartedAt: time.Now().Unix(),
			Command: "this is a very long command that should be truncated for display purposes in the table"},
	}
	out := FormatProcList(entries, true)
	if !strings.Contains(out, "...") {
		t.Error("expected truncated command with ellipsis")
	}
}

func TestFormatProcStatus(t *testing.T) {
	entry := ProcEntry{
		ID:         "p1",
		PID:        12345,
		Status:     "exited",
		Owner:      "build",
		Command:    "./build.sh",
		Dir:        "/home/user/project",
		StartedAt:  1700000000,
		FinishedAt: 1700000060,
		ExitCode:   0,
		LogFile:    "/tmp/test.log",
	}

	out := FormatProcStatus(entry)

	checks := []string{"p1", "12345", "exited", "build", "./build.sh", "/home/user/project", "/tmp/test.log", "Exit:     0", "Duration:"}
	for _, check := range checks {
		if !strings.Contains(out, check) {
			t.Errorf("expected %q in output, got:\n%s", check, out)
		}
	}
}

func TestFormatProcStatus_Running(t *testing.T) {
	entry := ProcEntry{
		ID:        "p1",
		PID:       12345,
		Status:    "running",
		Owner:     "build",
		Command:   "./build.sh",
		Dir:       "/tmp",
		StartedAt: time.Now().Unix(),
		LogFile:   "/tmp/test.log",
	}

	out := FormatProcStatus(entry)

	// Running process should not show Exit or Finished
	if strings.Contains(out, "Exit:") {
		t.Error("running process should not show exit code")
	}
	if strings.Contains(out, "Finished:") {
		t.Error("running process should not show finished time")
	}
}

func TestInit_CreatesProcDir(t *testing.T) {
	session := fmt.Sprintf("test-init-proc-%d", rand.Int())
	memDir := t.TempDir()
	t.Cleanup(func() { _ = Cleanup(session) })

	if err := Init(session, memDir); err != nil {
		t.Fatalf("Init: %v", err)
	}

	if _, err := os.Stat(ProcDir(session)); err != nil {
		t.Errorf("proc dir: %v", err)
	}
	if _, err := os.Stat(ProcPath(session)); err != nil {
		t.Errorf("proc.jsonl: %v", err)
	}
}
