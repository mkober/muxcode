package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

func TestSplitLines_Basic(t *testing.T) {
	data := []byte("line1\nline2\nline3\n")
	lines := splitLines(data)
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(lines))
	}
	if string(lines[0]) != "line1" {
		t.Errorf("line 0: got %q, want %q", string(lines[0]), "line1")
	}
	if string(lines[2]) != "line3" {
		t.Errorf("line 2: got %q, want %q", string(lines[2]), "line3")
	}
}

func TestSplitLines_NoTrailingNewline(t *testing.T) {
	data := []byte("line1\nline2")
	lines := splitLines(data)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}
	if string(lines[1]) != "line2" {
		t.Errorf("line 1: got %q, want %q", string(lines[1]), "line2")
	}
}

func TestSplitLines_EmptyLines(t *testing.T) {
	data := []byte("line1\n\nline3\n")
	lines := splitLines(data)
	if len(lines) != 2 {
		t.Fatalf("expected 2 non-empty lines, got %d", len(lines))
	}
	if string(lines[0]) != "line1" {
		t.Errorf("line 0: got %q, want %q", string(lines[0]), "line1")
	}
	if string(lines[1]) != "line3" {
		t.Errorf("line 1: got %q, want %q", string(lines[1]), "line3")
	}
}

func TestSplitLines_Empty(t *testing.T) {
	lines := splitLines([]byte{})
	if len(lines) != 0 {
		t.Fatalf("expected 0 lines, got %d", len(lines))
	}
}

func TestSplitLines_SingleNewline(t *testing.T) {
	lines := splitLines([]byte("\n"))
	if len(lines) != 0 {
		t.Fatalf("expected 0 lines, got %d", len(lines))
	}
}

// TestRunLogWritesSelfReportedSource drives the real `muxcode log` path and
// asserts the row it produces declares itself a self-report.
//
// This is what closes MUX-148 Decision 4's bypass: cmd/log.go used to write the
// JSONL directly with no source, so a row an agent authored about itself — exit
// code included — was indistinguishable from one the runtime observed. Asserting
// the constant rather than the write shape is the point; a test that checks the
// fields it just wrote itself would pass no matter what runLog did.
func TestRunLogWritesSelfReportedSource(t *testing.T) {
	dir := t.TempDir()
	bus.SetBusDirBase(dir)
	t.Cleanup(bus.ResetBusDirBase)

	session := "log-source-pin"
	t.Setenv("BUS_SESSION", session)
	if err := bus.Init(session, filepath.Join(dir, "memory")); err != nil {
		t.Fatalf("Init: %v", err)
	}

	if err := runLog([]string{"review", "0 must-fix", "--exit-code", "0"}, nil); err != nil {
		t.Fatalf("runLog: %v", err)
	}

	entries := bus.ReadConsoleEntries(bus.HistoryPath(session, "review"), 0)
	if len(entries) != 1 {
		t.Fatalf("wrote %d entries, want 1", len(entries))
	}
	if entries[0].Source != bus.SourceSelfReported {
		t.Errorf("source = %q, want %q — muxcode log must not look observed",
			entries[0].Source, bus.SourceSelfReported)
	}
	if entries[0].Outcome != bus.OutcomeSuccess {
		t.Errorf("outcome = %q, want success", entries[0].Outcome)
	}
}

func TestLogEntryFormat(t *testing.T) {
	// Test that a log entry written via the file append path has correct structure
	dir := t.TempDir()
	path := filepath.Join(dir, "review-history.jsonl")

	entry := map[string]interface{}{
		"ts":        1234567890,
		"summary":   "0 must-fix, 1 should-fix, 2 nits",
		"exit_code": "0",
		"command":   "",
		"output":    "",
		"outcome":   "success",
	}
	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	f.Write(append(data, '\n'))
	f.Close()

	// Read back and verify
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	var decoded map[string]interface{}
	if err := json.Unmarshal(splitLines(content)[0], &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if decoded["outcome"] != "success" {
		t.Errorf("outcome = %q, want %q", decoded["outcome"], "success")
	}
	if decoded["summary"] != "0 must-fix, 1 should-fix, 2 nits" {
		t.Errorf("summary = %q, want %q", decoded["summary"], "0 must-fix, 1 should-fix, 2 nits")
	}
	if decoded["exit_code"] != "0" {
		t.Errorf("exit_code = %q, want %q", decoded["exit_code"], "0")
	}
}

func TestLogEntryOutcome_Failure(t *testing.T) {
	// Verify outcome derivation: non-zero exit code → failure
	entry := map[string]interface{}{
		"ts":        1234567890,
		"summary":   "2 must-fix, 0 should-fix, 0 nits",
		"exit_code": "1",
		"command":   "",
		"output":    "",
		"outcome":   "failure",
	}
	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded map[string]interface{}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if decoded["outcome"] != "failure" {
		t.Errorf("outcome = %q, want %q", decoded["outcome"], "failure")
	}
}

func TestSplitLines_LargeInput(t *testing.T) {
	// Verify splitLines handles many lines correctly
	var parts []string
	for i := 0; i < 200; i++ {
		parts = append(parts, `{"ts":`+itoa(i)+`}`)
	}
	data := []byte(strings.Join(parts, "\n") + "\n")
	lines := splitLines(data)
	if len(lines) != 200 {
		t.Errorf("expected 200 lines, got %d", len(lines))
	}
}

func TestLogOutputStdin(t *testing.T) {
	// Exercise the actual runLog() codepath with --output-stdin.
	session := "test-log-stdin"
	t.Setenv("BUS_SESSION", session)
	defer os.RemoveAll(bus.BusDir(session))

	stdinContent := "Must-fix:\n- cmd/log.go:42 missing nil check\nShould-fix:\n- cmd/log.go:50 add context to error\nNits:\n- cmd/log.go:10 import order\n"
	stdin := strings.NewReader(stdinContent)

	args := []string{"review", "1 must-fix, 1 should-fix, 1 nits", "--exit-code", "1", "--output-stdin"}
	if err := runLog(args, stdin); err != nil {
		t.Fatalf("runLog: %v", err)
	}

	// Read back the history entry written by runLog
	historyPath := bus.HistoryPath(session, "review")
	content, err := os.ReadFile(historyPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	var decoded map[string]interface{}
	if err := json.Unmarshal(splitLines(content)[0], &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	gotOutput, ok := decoded["output"].(string)
	if !ok {
		t.Fatalf("output field is not a string: %T", decoded["output"])
	}

	// Verify trailing newlines were trimmed
	if strings.HasSuffix(gotOutput, "\n") {
		t.Errorf("output should not have trailing newline, got %q", gotOutput)
	}

	// Verify multi-line content is preserved
	if !strings.Contains(gotOutput, "Must-fix:") {
		t.Errorf("output should contain 'Must-fix:', got %q", gotOutput)
	}
	if !strings.Contains(gotOutput, "Should-fix:") {
		t.Errorf("output should contain 'Should-fix:', got %q", gotOutput)
	}
	if !strings.Contains(gotOutput, "Nits:") {
		t.Errorf("output should contain 'Nits:', got %q", gotOutput)
	}

	// Verify newlines within content are preserved
	lines := strings.Split(gotOutput, "\n")
	if len(lines) != 6 {
		t.Errorf("expected 6 lines in output, got %d: %q", len(lines), gotOutput)
	}

	// Verify outcome is failure (exit code 1 = must-fix found)
	if decoded["outcome"] != "failure" {
		t.Errorf("outcome = %q, want %q", decoded["outcome"], "failure")
	}
}

func TestLogOutputFile(t *testing.T) {
	// Exercise the actual runLog() codepath with --output-file.
	session := "test-log-file"
	t.Setenv("BUS_SESSION", session)
	defer os.RemoveAll(bus.BusDir(session))

	// Write multi-line findings to a temp file (simulates Write tool output)
	dir := t.TempDir()
	findingsPath := filepath.Join(dir, "review-findings.txt")
	findingsContent := "Should-fix:\n- log_test.go:273 missing actual Log() codepath test\nNits:\n- styles.go:63 TruncateAnsi unbounded scan\n- model.go:179 use VisibleWidth for consistency\n"
	if err := os.WriteFile(findingsPath, []byte(findingsContent), 0644); err != nil {
		t.Fatalf("WriteFile findings: %v", err)
	}

	args := []string{"review", "0 must-fix, 1 should-fix, 2 nits", "--exit-code", "0", "--output-file", findingsPath}
	if err := runLog(args, strings.NewReader("")); err != nil {
		t.Fatalf("runLog: %v", err)
	}

	// Read back the history entry written by runLog
	historyPath := bus.HistoryPath(session, "review")
	content, err := os.ReadFile(historyPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	var decoded map[string]interface{}
	if err := json.Unmarshal(splitLines(content)[0], &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	gotOutput, ok := decoded["output"].(string)
	if !ok {
		t.Fatalf("output field is not a string: %T", decoded["output"])
	}

	// Verify trailing newlines were trimmed
	if strings.HasSuffix(gotOutput, "\n") {
		t.Errorf("output should not have trailing newline, got %q", gotOutput)
	}

	// Verify multi-line content is preserved
	if !strings.Contains(gotOutput, "Should-fix:") {
		t.Errorf("output should contain 'Should-fix:', got %q", gotOutput)
	}
	if !strings.Contains(gotOutput, "Nits:") {
		t.Errorf("output should contain 'Nits:', got %q", gotOutput)
	}

	// Verify newlines within content are preserved
	lines := strings.Split(gotOutput, "\n")
	if len(lines) != 5 {
		t.Errorf("expected 5 lines in output, got %d: %q", len(lines), gotOutput)
	}

	// Verify outcome
	if decoded["outcome"] != "success" {
		t.Errorf("outcome = %q, want %q", decoded["outcome"], "success")
	}
}

func TestRunLogErrors(t *testing.T) {
	// Exercise runLog error paths directly.
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"too few args", []string{"review"}, "usage:"},
		{"missing exit-code value", []string{"review", "summary", "--exit-code"}, "--exit-code requires a value"},
		{"missing output value", []string{"review", "summary", "--output"}, "--output requires a value"},
		{"missing command value", []string{"review", "summary", "--command"}, "--command requires a value"},
		{"missing output-file path", []string{"review", "summary", "--output-file"}, "--output-file requires a path"},
		{"unknown flag", []string{"review", "summary", "--bogus"}, "unknown flag: --bogus"},
		{"mutually exclusive", []string{"review", "summary", "--output", "x", "--output-stdin"}, "mutually exclusive"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := runLog(tt.args, nil)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %q, want substring %q", err.Error(), tt.want)
			}
		})
	}
}

func TestIsPipe_RegularFile(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "test")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	defer f.Close()
	if isPipe(f) {
		t.Error("expected isPipe=false for regular file")
	}
}

func TestIsPipe_StringsReader(t *testing.T) {
	r := strings.NewReader("hello")
	if isPipe(r) {
		t.Error("expected isPipe=false for strings.Reader")
	}
}

func TestIsPipe_RealPipe(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()
	if !isPipe(r) {
		t.Error("expected isPipe=true for pipe read end")
	}
}

// itoa is a simple int-to-string helper to avoid importing strconv in tests.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	if neg {
		s = "-" + s
	}
	return s
}
