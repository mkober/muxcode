package bus

import (
	"encoding/json"
	"os"
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
		{nil, true},
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

	expectedRoles := []string{"build", "test", "review", "deploy", "run", "commit", "watch", "analyze", "api", "agent"}
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
	if len(roles) != 10 {
		t.Errorf("ConsoleRoles() = %d roles, want 10", len(roles))
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
