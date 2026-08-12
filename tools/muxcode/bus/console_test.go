package bus

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWordWrap(t *testing.T) {
	tests := []struct {
		text  string
		width int
		want  []string
	}{
		{"", 40, nil},
		{"hello", 40, []string{"hello"}},
		{"hello world", 40, []string{"hello world"}},
		{"hello world", 5, []string{"hello", "world"}},
		{"the quick brown fox jumps over the lazy dog", 20, []string{
			"the quick brown fox",
			"jumps over the lazy",
			"dog",
		}},
		{"superlongwordthatcannotbreak", 10, []string{"superlongwordthatcannotbreak"}},
		{"a b c d e", 3, []string{"a b", "c d", "e"}},
	}

	for _, tt := range tests {
		got := WordWrap(tt.text, tt.width)
		if len(got) != len(tt.want) {
			t.Errorf("WordWrap(%q, %d) = %v, want %v", tt.text, tt.width, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("WordWrap(%q, %d)[%d] = %q, want %q", tt.text, tt.width, i, got[i], tt.want[i])
			}
		}
	}
}

func TestFormatTimestamp(t *testing.T) {
	// Zero/negative returns placeholder
	if got := FormatTimestamp(0); got != "??? ?? ??:??:??" {
		t.Errorf("FormatTimestamp(0) = %q, want placeholder", got)
	}
	if got := FormatTimestamp(-1); got != "??? ?? ??:??:??" {
		t.Errorf("FormatTimestamp(-1) = %q, want placeholder", got)
	}

	// Valid timestamp should produce formatted string
	got := FormatTimestamp(1700000000)
	if len(got) < 10 {
		t.Errorf("FormatTimestamp(1700000000) = %q, too short", got)
	}
}

func TestFormatTimeOnly(t *testing.T) {
	if got := FormatTimeOnly(0); got != "" {
		t.Errorf("FormatTimeOnly(0) = %q, want empty", got)
	}

	got := FormatTimeOnly(1700000000)
	if len(got) != 8 { // HH:MM:SS
		t.Errorf("FormatTimeOnly(1700000000) = %q, want HH:MM:SS format", got)
	}
}

func TestStripANSI(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello", "hello"},
		{"\033[38;5;141mhello\033[0m", "hello"},
		{"\033[2mfoo\033[0m bar \033[38;5;80mbaz\033[0m", "foo bar baz"},
		{"no escapes here", "no escapes here"},
	}

	for _, tt := range tests {
		got := StripANSI(tt.input)
		if got != tt.want {
			t.Errorf("StripANSI(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestSeparator(t *testing.T) {
	got := Separator(5)
	if got != "─────" {
		t.Errorf("Separator(5) = %q, want 5 dashes", got)
	}
	if len(Separator(0)) != 0 {
		t.Errorf("Separator(0) should be empty")
	}
}

func TestConsoleEntryExitCodeStr(t *testing.T) {
	tests := []struct {
		name     string
		exitCode json.RawMessage
		want     string
	}{
		{"nil", nil, ""},
		{"null", json.RawMessage(`null`), ""},
		{"string zero", json.RawMessage(`"0"`), "0"},
		{"string one", json.RawMessage(`"1"`), "1"},
		{"number zero", json.RawMessage(`0`), "0"},
		{"number one", json.RawMessage(`1`), "1"},
		{"number 127", json.RawMessage(`127`), "127"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &ConsoleEntry{ExitCode: tt.exitCode}
			got := e.ExitCodeStr()
			if got != tt.want {
				t.Errorf("ExitCodeStr() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestConsoleEntryIsPass(t *testing.T) {
	tests := []struct {
		exitCode json.RawMessage
		want     bool
	}{
		// A missing exit code is NOT a pass. This case previously expected
		// true, which is the defect itself: any entry that never ran a command
		// — including a bus reply synthesized from an agent's launch banner —
		// had no exit code and so rendered green. It is now unverified; see
		// TestConsoleEntry_EmptyExitCodeIsNotPass in history_provenance_test.go.
		{nil, false},
		{json.RawMessage(`"0"`), true},
		{json.RawMessage(`0`), true},
		{json.RawMessage(`"1"`), false},
		{json.RawMessage(`1`), false},
	}

	for _, tt := range tests {
		e := &ConsoleEntry{ExitCode: tt.exitCode}
		if got := e.IsPass(); got != tt.want {
			t.Errorf("IsPass() with exit_code=%s = %v, want %v", string(tt.exitCode), got, tt.want)
		}
	}
}

func TestReadConsoleEntries(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test-history.jsonl")

	// Write test entries
	entries := []map[string]interface{}{
		{"ts": 1000, "command": "make build", "exit_code": "0", "summary": "build ok"},
		{"ts": 2000, "command": "make test", "exit_code": "1", "summary": "tests failed"},
		{"ts": 3000, "command": "make build", "exit_code": "0", "summary": "build ok 2"},
	}

	var lines []string
	for _, e := range entries {
		data, _ := json.Marshal(e)
		lines = append(lines, string(data))
	}
	os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0644)

	// Read all
	got := ReadConsoleEntries(path, 0)
	if len(got) != 3 {
		t.Fatalf("ReadConsoleEntries(all) = %d entries, want 3", len(got))
	}
	if got[0].TS != 1000 {
		t.Errorf("first entry TS = %d, want 1000", got[0].TS)
	}
	if got[2].Summary != "build ok 2" {
		t.Errorf("last entry summary = %q, want %q", got[2].Summary, "build ok 2")
	}

	// Read with limit
	got = ReadConsoleEntries(path, 2)
	if len(got) != 2 {
		t.Fatalf("ReadConsoleEntries(limit=2) = %d entries, want 2", len(got))
	}
	if got[0].TS != 2000 {
		t.Errorf("limited first entry TS = %d, want 2000", got[0].TS)
	}

	// Missing file
	got = ReadConsoleEntries(filepath.Join(dir, "missing.jsonl"), 0)
	if got != nil {
		t.Errorf("ReadConsoleEntries(missing) = %v, want nil", got)
	}
}

func TestReadConsoleEntriesWithMalformedLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mixed.jsonl")

	content := `{"ts":1000,"command":"ok","exit_code":"0"}
not valid json
{"ts":2000,"command":"ok2","exit_code":"0"}
`
	os.WriteFile(path, []byte(content), 0644)

	got := ReadConsoleEntries(path, 0)
	if len(got) != 2 {
		t.Errorf("ReadConsoleEntries with malformed lines = %d, want 2", len(got))
	}
}

func TestReadAnalyzeEntries(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "log.jsonl")

	entries := []map[string]interface{}{
		{"ts": 1000, "from": "analyze", "type": "response", "action": "notify", "payload": "found issue", "to": "edit"},
		{"ts": 2000, "from": "edit", "type": "request", "action": "review", "payload": "please review"},
		{"ts": 3000, "from": "analyze", "type": "response", "action": "notify", "payload": "another finding", "to": "edit"},
		{"ts": 4000, "from": "analyze", "type": "request", "action": "ask", "payload": "question"},
	}

	var lines []string
	for _, e := range entries {
		data, _ := json.Marshal(e)
		lines = append(lines, string(data))
	}
	os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0644)

	got := ReadAnalyzeEntries(path, 0)
	if len(got) != 2 {
		t.Fatalf("ReadAnalyzeEntries = %d entries, want 2 (only analyze responses)", len(got))
	}
	if got[0].Payload != "found issue" {
		t.Errorf("first analyze entry payload = %q, want %q", got[0].Payload, "found issue")
	}
	if got[1].Payload != "another finding" {
		t.Errorf("second analyze entry payload = %q, want %q", got[1].Payload, "another finding")
	}
}

func TestDefaultConsoleConfigs(t *testing.T) {
	configs := DefaultConsoleConfigs()

	expectedRoles := []string{"build", "test", "review", "deploy", "run", "commit", "watch", "analyze", "api", "auto", "research", "serve"}
	for _, role := range expectedRoles {
		cfg, ok := configs[role]
		if !ok {
			t.Errorf("missing config for role %q", role)
			continue
		}
		if cfg.Title == "" {
			t.Errorf("role %q has empty title", role)
		}
		if cfg.EmptyMsg == "" {
			t.Errorf("role %q has empty EmptyMsg", role)
		}
		if cfg.Renderer == nil {
			t.Errorf("role %q has nil renderer", role)
		}
		if cfg.MaxRecent <= 0 {
			t.Errorf("role %q has non-positive MaxRecent: %d", role, cfg.MaxRecent)
		}
	}
}

func TestConsoleRoles(t *testing.T) {
	roles := ConsoleRoles()
	if len(roles) != 12 {
		t.Errorf("ConsoleRoles() = %d roles, want 12", len(roles))
	}
}

func TestRenderConsoleBuildEmpty(t *testing.T) {
	// Use a session that won't have any history files
	output := RenderConsole("build", "nonexistent-test-session-xyz", 80)
	if !strings.Contains(output, "no builds yet") {
		t.Errorf("empty build console should contain 'no builds yet', got: %q", output)
	}
}

func TestRenderConsoleBuildWithEntries(t *testing.T) {
	// Create a temp bus dir with history
	dir := t.TempDir()
	session := "test-console-build"
	busDir := filepath.Join(dir, "muxcode-bus-"+session)
	os.MkdirAll(busDir, 0755)

	histPath := filepath.Join(busDir, "build-history.jsonl")
	entries := []map[string]interface{}{
		{"ts": 1700000000, "command": "make build", "exit_code": "0", "output": "Build succeeded", "summary": "ok"},
		{"ts": 1700001000, "command": "make build", "exit_code": "1", "output": "Error: missing dep", "errors": "missing dep error", "summary": "failed"},
		{"ts": 1700002000, "command": "make build", "exit_code": "0", "output": "Build succeeded again", "summary": "ok"},
	}

	var lines []string
	for _, e := range entries {
		data, _ := json.Marshal(e)
		lines = append(lines, string(data))
	}
	os.WriteFile(histPath, []byte(strings.Join(lines, "\n")+"\n"), 0644)

	// Override BusDir by setting env - use the temp dir structure
	// Since BusDir uses /tmp/muxcode-bus-{session}, we need to create there
	realBusDir := "/tmp/muxcode-bus-" + session
	os.MkdirAll(realBusDir, 0755)
	defer os.RemoveAll(realBusDir)

	realHistPath := filepath.Join(realBusDir, "build-history.jsonl")
	os.WriteFile(realHistPath, []byte(strings.Join(lines, "\n")+"\n"), 0644)

	output := RenderConsole("build", session, 80)

	// Verify key elements in output
	if !strings.Contains(output, "total") {
		t.Error("build output should contain 'total'")
	}
	if !strings.Contains(output, "pass") {
		t.Error("build output should contain 'pass'")
	}
	if !strings.Contains(output, "fail") {
		t.Error("build output should contain 'fail'")
	}
	if !strings.Contains(output, "recent builds") {
		t.Error("build output should contain 'recent builds'")
	}
	if !strings.Contains(output, "make build") {
		t.Error("build output should contain 'make build'")
	}
	if !strings.Contains(output, "completed successfully") {
		t.Error("build output should contain 'completed successfully' for last passing build")
	}
	// Should show previous failure since last build passed but there were failures
	if !strings.Contains(output, "Last errors") {
		t.Error("build output should contain 'Last errors' section for previous failure")
	}
}

func TestRenderConsoleUnknownRole(t *testing.T) {
	output := RenderConsole("nonexistent", "test", 80)
	if !strings.Contains(output, "Unknown role") {
		t.Errorf("unknown role should produce error message, got: %q", output)
	}
}

func TestRenderConsoleWatchEmpty(t *testing.T) {
	output := RenderConsole("watch", "nonexistent-test-session-xyz", 80)
	if !strings.Contains(output, "no events yet") {
		t.Errorf("empty watch console should contain 'no events yet', got: %q", output)
	}
}

func TestRenderConsoleAnalyzeEmpty(t *testing.T) {
	output := RenderConsole("analyze", "nonexistent-test-session-xyz", 80)
	if !strings.Contains(output, "no findings yet") {
		t.Errorf("empty analyze console should contain 'no findings yet', got: %q", output)
	}
	if !strings.Contains(output, "waiting for analyst agent") {
		t.Errorf("empty analyze console should contain 'waiting for analyst agent', got: %q", output)
	}
}

func TestRenderConsoleAPIEmpty(t *testing.T) {
	// API reads from .muxcode/api/history.jsonl (project-local)
	// In test context this won't exist
	output := RenderConsole("api", "test", 80)
	if !strings.Contains(output, "no requests yet") {
		t.Errorf("empty api console should contain 'no requests yet', got: %q", output)
	}
}

func TestConsoleHeader(t *testing.T) {
	header := ConsoleHeader("Build", 5, 80)
	if !strings.Contains(header, "Build") {
		t.Error("header should contain title")
	}
	if !strings.Contains(header, "every 5s") {
		t.Error("header should contain interval")
	}
	if !strings.Contains(header, "─") {
		t.Error("header should contain separator")
	}
}

func TestContainsAny(t *testing.T) {
	if !containsAny("error in build", "error", "fail") {
		t.Error("should match 'error'")
	}
	if !containsAny("build failed", "error", "fail") {
		t.Error("should match 'fail'")
	}
	if containsAny("all good", "error", "fail") {
		t.Error("should not match")
	}
}

func TestHttpMethodColor(t *testing.T) {
	if httpMethodColor("GET") != ColorGreen {
		t.Error("GET should be green")
	}
	if httpMethodColor("DELETE") != ColorRed {
		t.Error("DELETE should be red")
	}
	if httpMethodColor("UNKNOWN") != ColorDim {
		t.Error("unknown method should be dim")
	}
}

func TestHttpStatusColor(t *testing.T) {
	if httpStatusColor(200) != ColorGreen {
		t.Error("200 should be green")
	}
	if httpStatusColor(301) != ColorCyan {
		t.Error("301 should be cyan")
	}
	if httpStatusColor(404) != ColorYellow {
		t.Error("404 should be yellow")
	}
	if httpStatusColor(500) != ColorRed {
		t.Error("500 should be red")
	}
}

func TestAgentDuration(t *testing.T) {
	tests := []struct {
		secs int64
		want string
	}{
		{0, "0s"},
		{30, "30s"},
		{59, "59s"},
		{60, "1m 0s"},
		{90, "1m 30s"},
		{3599, "59m 59s"},
		{3600, "1h 0m"},
		{7260, "2h 1m"},
	}

	for _, tt := range tests {
		got := agentDuration(tt.secs)
		if got != tt.want {
			t.Errorf("agentDuration(%d) = %q, want %q", tt.secs, got, tt.want)
		}
	}
}

func TestReadAutonomousAgentStatus_Empty(t *testing.T) {
	// Use a session name that won't exist
	s := ReadAutonomousAgentStatus("nonexistent-agent-test-xyz")
	if s.CurrentStory != "" {
		t.Errorf("empty CurrentStory = %q, want empty", s.CurrentStory)
	}
	if s.Phase != "" {
		t.Errorf("empty Phase = %q, want empty", s.Phase)
	}
	if s.StoriesDone != 0 {
		t.Errorf("empty StoriesDone = %d, want 0", s.StoriesDone)
	}
	if s.LastHeartbeat != 0 {
		t.Errorf("empty LastHeartbeat = %d, want 0", s.LastHeartbeat)
	}
}

func TestReadAutonomousAgentStatus_WithFiles(t *testing.T) {
	dir := t.TempDir()
	SetBusDirBase(dir)
	defer ResetBusDirBase()

	session := "test-agent-status"
	busDir := BusDir(session)
	os.MkdirAll(busDir, 0755)

	// Write state files
	os.WriteFile(AgentCurrentStoryPath(session), []byte("PROJ-123"), 0644)
	os.WriteFile(AgentPhasePath(session), []byte("implementation"), 0644)
	os.WriteFile(AgentStoriesDonePath(session), []byte("3"), 0644)
	os.WriteFile(AgentHeartbeatPath(session), []byte("1700000000"), 0644)

	s := ReadAutonomousAgentStatus(session)
	if s.CurrentStory != "PROJ-123" {
		t.Errorf("CurrentStory = %q, want PROJ-123", s.CurrentStory)
	}
	if s.Phase != "implementation" {
		t.Errorf("Phase = %q, want implementation", s.Phase)
	}
	if s.StoriesDone != 3 {
		t.Errorf("StoriesDone = %d, want 3", s.StoriesDone)
	}
	if s.LastHeartbeat != 1700000000 {
		t.Errorf("LastHeartbeat = %d, want 1700000000", s.LastHeartbeat)
	}
}

func TestFormatAutonomousAgentStatus(t *testing.T) {
	s := AutonomousAgentStatus{
		CurrentStory: "PROJ-456",
		Phase:        "requirements",
		StoriesDone:  2,
	}

	output := FormatAutonomousAgentStatus(s)
	if !strings.Contains(output, "PROJ-456") {
		t.Error("output should contain story key")
	}
	if !strings.Contains(output, "requirements") {
		t.Error("output should contain phase")
	}
	if !strings.Contains(output, "2") {
		t.Error("output should contain stories done count")
	}
}

func TestFormatAutonomousAgentStatus_Empty(t *testing.T) {
	s := AutonomousAgentStatus{}
	output := FormatAutonomousAgentStatus(s)
	if !strings.Contains(output, "(none)") {
		t.Error("empty status should show (none) for story")
	}
	if !strings.Contains(output, "idle") {
		t.Error("empty status should show idle for phase")
	}
}

func TestRenderConsoleAgentEmpty(t *testing.T) {
	output := RenderConsole("auto", "nonexistent-agent-test-xyz", 80)
	if !strings.Contains(output, "no activity yet") {
		t.Errorf("empty agent console should contain 'no activity yet', got: %q", output)
	}
	if !strings.Contains(output, "waiting for autonomous agent") {
		t.Errorf("empty agent console should contain waiting message, got: %q", output)
	}
	// Should include status header
	if !strings.Contains(output, "Story:") {
		t.Errorf("agent console should contain status header with 'Story:', got: %q", output)
	}
	if !strings.Contains(output, "Phase:") {
		t.Errorf("agent console should contain status header with 'Phase:', got: %q", output)
	}
}

// TestRenderConsoleRunWithProcs verifies that background processes spawned via
// `muxcode proc start` surface in the run console — including when there is no
// command-execution history (the run agent's silent-proc scenario).
func TestRenderConsoleRunWithProcs(t *testing.T) {
	session := "test-console-run-procs"
	_ = Init(session, t.TempDir())
	t.Cleanup(func() { _ = Cleanup(session) })

	if err := os.MkdirAll(ProcDir(session), 0755); err != nil {
		t.Fatalf("mkdir proc dir: %v", err)
	}

	// A finished process owned by the run agent, with captured output.
	id := "1700000000-proc-abcd1234"
	logFile := ProcLogPath(session, id)
	os.WriteFile(logFile, []byte("starting integration test\nstep 1 ok\nstep 2 ok\nEXIT_CODE:0\n"), 0644)

	entry := ProcEntry{
		ID:         id,
		PID:        999999, // not alive → display uses stored terminal status
		Command:    "bash scripts/test-student-data.sh",
		Dir:        "/tmp",
		Owner:      "run",
		Status:     "exited",
		ExitCode:   0,
		StartedAt:  1700000000,
		FinishedAt: 1700000012,
		LogFile:    logFile,
	}
	if err := WriteProcEntries(session, []ProcEntry{entry}); err != nil {
		t.Fatalf("WriteProcEntries: %v", err)
	}

	output := RenderConsole("run", session, 80)

	if !strings.Contains(output, "background processes") {
		t.Errorf("run console should show 'background processes' section, got: %q", output)
	}
	if !strings.Contains(output, "bash scripts/test-student-data.sh") {
		t.Errorf("run console should show the proc command, got: %q", output)
	}
	if !strings.Contains(output, "abcd1234") {
		t.Errorf("run console should show the short proc id, got: %q", output)
	}
	if !strings.Contains(output, "step 2 ok") {
		t.Errorf("run console should show the proc output tail, got: %q", output)
	}
	if strings.Contains(output, "EXIT_CODE:0") {
		t.Errorf("run console should strip the EXIT_CODE sentinel from output, got: %q", output)
	}
	// With no command history but a tracked proc, the empty placeholder must
	// not be shown — the proc section stands in for it.
	if strings.Contains(output, "no executions yet") {
		t.Errorf("run console with procs should not show 'no executions yet', got: %q", output)
	}
}

// TestRenderConsoleRunProcOwnerFilter verifies procs owned by other roles do
// not leak into the run console.
func TestRenderConsoleRunProcOwnerFilter(t *testing.T) {
	session := "test-console-run-procfilter"
	_ = Init(session, t.TempDir())
	t.Cleanup(func() { _ = Cleanup(session) })

	if err := os.MkdirAll(ProcDir(session), 0755); err != nil {
		t.Fatalf("mkdir proc dir: %v", err)
	}

	id := "1700000000-proc-deadbeef"
	logFile := ProcLogPath(session, id)
	os.WriteFile(logFile, []byte("deploy log\nEXIT_CODE:0\n"), 0644)
	entry := ProcEntry{
		ID: id, PID: 999999, Command: "cdk deploy SomeStack", Owner: "deploy",
		Status: "exited", ExitCode: 0, StartedAt: 1700000000, FinishedAt: 1700000005, LogFile: logFile,
	}
	if err := WriteProcEntries(session, []ProcEntry{entry}); err != nil {
		t.Fatalf("WriteProcEntries: %v", err)
	}

	output := RenderConsole("run", session, 80)
	if strings.Contains(output, "cdk deploy SomeStack") {
		t.Errorf("run console must not show deploy-owned procs, got: %q", output)
	}
	if !strings.Contains(output, "no executions yet") {
		t.Errorf("run console with no owned procs/history should show empty placeholder, got: %q", output)
	}
}

// writeOwnedProc is a test helper: writes a single finished proc owned by the
// given role, with the given command and log output, and returns the proc id.
func writeOwnedProc(t *testing.T, session, owner, command, output string) string {
	t.Helper()
	if err := os.MkdirAll(ProcDir(session), 0755); err != nil {
		t.Fatalf("mkdir proc dir: %v", err)
	}
	id := "1700000000-proc-" + owner + "01"
	logFile := ProcLogPath(session, id)
	os.WriteFile(logFile, []byte(output+"\nEXIT_CODE:0\n"), 0644)
	entry := ProcEntry{
		ID: id, PID: 999999, Command: command, Owner: owner,
		Status: "exited", ExitCode: 0, StartedAt: 1700000000, FinishedAt: 1700000010, LogFile: logFile,
	}
	if err := WriteProcEntries(session, []ProcEntry{entry}); err != nil {
		t.Fatalf("WriteProcEntries: %v", err)
	}
	return id
}

// TestRenderConsoleWatchWithProcs verifies the watch console surfaces its
// background processes with the same section as the run console.
func TestRenderConsoleWatchWithProcs(t *testing.T) {
	session := "test-console-watch-procs"
	_ = Init(session, t.TempDir())
	t.Cleanup(func() { _ = Cleanup(session) })

	writeOwnedProc(t, session, "watch", "bash scripts/tail-logs.sh /aws/lambda/fn", "streaming CloudWatch events")

	output := RenderConsole("watch", session, 80)
	if !strings.Contains(output, "background processes") {
		t.Errorf("watch console should show 'background processes', got: %q", output)
	}
	if !strings.Contains(output, "tail-logs.sh") {
		t.Errorf("watch console should show the derived proc name, got: %q", output)
	}
	if !strings.Contains(output, "streaming CloudWatch events") {
		t.Errorf("watch console should show the proc output tail, got: %q", output)
	}
	if strings.Contains(output, "no events yet") {
		t.Errorf("watch console with procs should not show empty placeholder, got: %q", output)
	}
}

// TestRenderConsoleCommitWithProcs verifies the commit console surfaces its
// background processes alongside the git status section.
func TestRenderConsoleCommitWithProcs(t *testing.T) {
	session := "test-console-commit-procs"
	_ = Init(session, t.TempDir())
	t.Cleanup(func() { _ = Cleanup(session) })

	writeOwnedProc(t, session, "commit", "bash scripts/push-release.sh v1.2.0", "pushing tags to origin")

	output := RenderConsole("commit", session, 80)
	if !strings.Contains(output, "background processes") {
		t.Errorf("commit console should show 'background processes', got: %q", output)
	}
	if !strings.Contains(output, "push-release.sh") {
		t.Errorf("commit console should show the derived proc name, got: %q", output)
	}
	if !strings.Contains(output, "pushing tags to origin") {
		t.Errorf("commit console should show the proc output tail, got: %q", output)
	}
}

// TestRenderConsoleWatchProcOwnerFilter verifies non-watch procs don't leak in.
func TestRenderConsoleWatchProcOwnerFilter(t *testing.T) {
	session := "test-console-watch-procfilter"
	_ = Init(session, t.TempDir())
	t.Cleanup(func() { _ = Cleanup(session) })

	writeOwnedProc(t, session, "run", "bash scripts/only-run.sh", "run output")

	output := RenderConsole("watch", session, 80)
	if strings.Contains(output, "only-run.sh") {
		t.Errorf("watch console must not show run-owned procs, got: %q", output)
	}
	if !strings.Contains(output, "no events yet") {
		t.Errorf("watch console with no owned procs should show empty placeholder, got: %q", output)
	}
}

// runTestGit executes a git command in dir with a hermetic identity/config so
// the caller's global gitconfig (hooks, signing) cannot influence the result.
func runTestGit(t *testing.T, dir string, extraEnv []string, args ...string) {
	t.Helper()
	base := []string{
		"-c", "user.name=Test",
		"-c", "user.email=test@example.com",
		"-c", "commit.gpgsign=false",
		"-c", "core.hooksPath=",
	}
	cmd := exec.Command("git", append(base, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), extraEnv...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// makeBranchRepo builds a temp repo with one commit per branch name, dated
// oldest-first in slice order, and chdirs into it for the duration of the test.
func makeBranchRepo(t *testing.T, branches []string) string {
	t.Helper()
	dir := t.TempDir()
	runTestGit(t, dir, nil, "init")

	for i, b := range branches {
		date := fmt.Sprintf("2026-01-01 %02d:00:00 +0000", i)
		env := []string{"GIT_AUTHOR_DATE=" + date, "GIT_COMMITTER_DATE=" + date}
		runTestGit(t, dir, nil, "checkout", "-b", b)
		if err := os.WriteFile(filepath.Join(dir, b+".txt"), []byte("x"), 0644); err != nil {
			t.Fatalf("write file: %v", err)
		}
		runTestGit(t, dir, nil, "add", ".")
		runTestGit(t, dir, env, "commit", "-m", "commit on "+b)
	}

	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })
	return dir
}

// TestRenderRecentBranchesOrderAndCurrent pins newest-first ordering and that
// the checked-out branch is the one marked.
func TestRenderRecentBranchesOrderAndCurrent(t *testing.T) {
	makeBranchRepo(t, []string{"old-feature", "mid-feature", "new-feature"})

	out := StripANSI(renderRecentBranches("new-feature"))
	if !strings.Contains(out, "recent branches") {
		t.Fatalf("expected section header, got: %q", out)
	}

	iNew := strings.Index(out, "new-feature")
	iMid := strings.Index(out, "mid-feature")
	iOld := strings.Index(out, "old-feature")
	if iNew < 0 || iMid < 0 || iOld < 0 {
		t.Fatalf("all three branches should be listed, got: %q", out)
	}
	if !(iNew < iMid && iMid < iOld) {
		t.Errorf("branches must be newest-first, got: %q", out)
	}

	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, "-feature") {
			continue
		}
		marked := strings.Contains(line, "*")
		isCurrent := strings.Contains(line, "new-feature")
		if marked != isCurrent {
			t.Errorf("only the current branch may be marked, offending line: %q", line)
		}
	}
}

// TestRenderRecentBranchesCapsAtLimit verifies the list is capped at
// recentBranchLimit and that it drops the OLDEST branches, not the newest.
func TestRenderRecentBranchesCapsAtLimit(t *testing.T) {
	var branches []string
	for i := 0; i < recentBranchLimit+2; i++ {
		branches = append(branches, fmt.Sprintf("branch-%02d", i))
	}
	makeBranchRepo(t, branches)

	out := StripANSI(renderRecentBranches(branches[len(branches)-1]))

	rows := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "branch-") {
			rows++
		}
	}
	if rows != recentBranchLimit {
		t.Errorf("expected %d branch rows, got %d: %q", recentBranchLimit, rows, out)
	}
	// branch-00 and branch-01 are the two oldest and must be the ones dropped.
	if strings.Contains(out, "branch-00") || strings.Contains(out, "branch-01") {
		t.Errorf("oldest branches should be dropped, not newest, got: %q", out)
	}
	if !strings.Contains(out, "branch-11") {
		t.Errorf("newest branch must be retained, got: %q", out)
	}
}

// TestRenderRecentBranchesEmptyRepo verifies a repo with no commits renders
// nothing rather than an empty header block.
func TestRenderRecentBranchesEmptyRepo(t *testing.T) {
	dir := t.TempDir()
	runTestGit(t, dir, nil, "init")

	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	if out := renderRecentBranches(""); out != "" {
		t.Errorf("repo with no commits should render nothing, got: %q", out)
	}
}
