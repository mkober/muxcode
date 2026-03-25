package bus

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseToolEvent(t *testing.T) {
	raw := `{"tool_input":{"command":"go build .","description":"Build project"},"exit_code":0}`
	ev, err := ParseToolEvent([]byte(raw))
	if err != nil {
		t.Fatalf("ParseToolEvent: %v", err)
	}
	if ev.ToolInput.Command != "go build ." {
		t.Errorf("Command = %q, want %q", ev.ToolInput.Command, "go build .")
	}
	if ev.ToolInput.Description != "Build project" {
		t.Errorf("Description = %q, want %q", ev.ToolInput.Description, "Build project")
	}
}

func TestParseToolEvent_FilePath(t *testing.T) {
	raw := `{"tool_input":{"file_path":"/foo/bar.go","new_string":"hello"}}`
	ev, err := ParseToolEvent([]byte(raw))
	if err != nil {
		t.Fatalf("ParseToolEvent: %v", err)
	}
	if ev.ToolInput.FilePath != "/foo/bar.go" {
		t.Errorf("FilePath = %q, want %q", ev.ToolInput.FilePath, "/foo/bar.go")
	}
	if ev.ToolInput.NewString != "hello" {
		t.Errorf("NewString = %q, want %q", ev.ToolInput.NewString, "hello")
	}
}

func TestGetExitCode_TopLevel(t *testing.T) {
	raw := `{"tool_input":{"command":"ls"},"exit_code":"1"}`
	ev, _ := ParseToolEvent([]byte(raw))
	if got := ev.GetExitCode(); got != "1" {
		t.Errorf("GetExitCode = %q, want %q", got, "1")
	}
}

func TestGetExitCode_NumericTopLevel(t *testing.T) {
	raw := `{"tool_input":{"command":"ls"},"exit_code":2}`
	ev, _ := ParseToolEvent([]byte(raw))
	if got := ev.GetExitCode(); got != "2" {
		t.Errorf("GetExitCode = %q, want %q", got, "2")
	}
}

func TestGetExitCode_FromResponse(t *testing.T) {
	raw := `{"tool_input":{"command":"ls"},"tool_response":{"exit_code":"1","stdout":"error"}}`
	ev, _ := ParseToolEvent([]byte(raw))
	if got := ev.GetExitCode(); got != "1" {
		t.Errorf("GetExitCode = %q, want %q", got, "1")
	}
}

func TestGetExitCode_Interrupted(t *testing.T) {
	raw := `{"tool_input":{"command":"ls"},"tool_response":{"interrupted":true}}`
	ev, _ := ParseToolEvent([]byte(raw))
	if got := ev.GetExitCode(); got != "1" {
		t.Errorf("GetExitCode = %q, want %q", got, "1")
	}
}

func TestGetExitCode_StderrError(t *testing.T) {
	raw := `{"tool_input":{"command":"ls"},"tool_response":{"stderr":"Error: something failed"}}`
	ev, _ := ParseToolEvent([]byte(raw))
	if got := ev.GetExitCode(); got != "1" {
		t.Errorf("GetExitCode = %q, want %q", got, "1")
	}
}

func TestGetExitCode_Default(t *testing.T) {
	raw := `{"tool_input":{"command":"ls"}}`
	ev, _ := ParseToolEvent([]byte(raw))
	if got := ev.GetExitCode(); got != "0" {
		t.Errorf("GetExitCode = %q, want %q", got, "0")
	}
}

func TestGetOutput_StringResponse(t *testing.T) {
	raw := `{"tool_input":{"command":"ls"},"tool_response":"line1\nline2\nline3"}`
	ev, _ := ParseToolEvent([]byte(raw))
	out := ev.GetOutput(10, 1000)
	if !strings.Contains(out, "line1") {
		t.Errorf("GetOutput missing line1: %q", out)
	}
}

func TestGetOutput_ObjectResponse(t *testing.T) {
	raw := `{"tool_input":{"command":"ls"},"tool_response":{"stdout":"output here"}}`
	ev, _ := ParseToolEvent([]byte(raw))
	out := ev.GetOutput(10, 1000)
	if out != "output here" {
		t.Errorf("GetOutput = %q, want %q", out, "output here")
	}
}

func TestGetOutput_Truncation(t *testing.T) {
	long := strings.Repeat("x", 200)
	raw := `{"tool_input":{"command":"ls"},"tool_response":"` + long + `"}`
	ev, _ := ParseToolEvent([]byte(raw))
	out := ev.GetOutput(10, 50)
	if len(out) > 50 {
		t.Errorf("GetOutput length %d > 50", len(out))
	}
	if !strings.HasSuffix(out, "...") {
		t.Errorf("GetOutput should end with ...: %q", out)
	}
}

func TestGetOutput_LastNLines(t *testing.T) {
	lines := "line_01\\nline_02\\nline_03\\nline_04\\nline_05\\nline_06\\nline_07\\nline_08\\nline_09\\nline_10"
	raw := `{"tool_input":{"command":"ls"},"tool_response":"` + lines + `"}`
	ev, err := ParseToolEvent([]byte(raw))
	if err != nil {
		t.Fatalf("ParseToolEvent: %v", err)
	}
	out := ev.GetOutput(3, 1000)
	if strings.Contains(out, "line_01") {
		t.Errorf("GetOutput(3,...) should not contain line_01: %q", out)
	}
	if !strings.Contains(out, "line_10") {
		t.Errorf("GetOutput(3,...) should contain line_10: %q", out)
	}
}

func TestHookOutcome(t *testing.T) {
	tests := []struct {
		exitCode string
		want     string
	}{
		{"0", "success"},
		{"1", "failure"},
		{"127", "failure"},
		{"", "unknown"},
	}
	for _, tt := range tests {
		if got := HookOutcome(tt.exitCode); got != tt.want {
			t.Errorf("HookOutcome(%q) = %q, want %q", tt.exitCode, got, tt.want)
		}
	}
}

func TestClassifyCommand(t *testing.T) {
	tests := []struct {
		command string
		want    CommandType
	}{
		{"./build.sh", CmdBuild},
		{"go build .", CmdBuild},
		{"pnpm run build", CmdBuild},
		{"make", CmdBuild},
		{"make install", CmdBuild},
		{"cargo build --release", CmdBuild},
		{"./test.sh", CmdTest},
		{"go test ./...", CmdTest},
		{"jest --watch", CmdTest},
		{"pytest -v", CmdTest},
		{"npx jest", CmdTest},
		{"go vet ./...", CmdTest},
		{"cdk diff", CmdDeploy},
		{"cdk synth --all", CmdBuild}, // cdk*synth matches build first; shell script sets both flags
		{"terraform plan", CmdDeploy},
		{"cdk deploy", CmdDeployApply},
		{"terraform apply", CmdDeployApply},
		{"git commit -m 'test'", CmdGit},
		{"git push origin main", CmdGit},
		{"gh pr create --title foo", CmdGit},
		{"muxcode-agent-bus send build build", CmdBus},
		{"agent-bus inbox", CmdBus},
		{"ls -la", CmdUnknown},
		{"echo hello", CmdUnknown},
	}
	for _, tt := range tests {
		if got := ClassifyCommand(tt.command); got != tt.want {
			t.Errorf("ClassifyCommand(%q) = %d, want %d", tt.command, got, tt.want)
		}
	}
}

func TestClassifyCommand_CdPrefix(t *testing.T) {
	if got := ClassifyCommand("cd /foo && go build ."); got != CmdBuild {
		t.Errorf("ClassifyCommand with cd prefix = %d, want CmdBuild", got)
	}
}

func TestClassifyCommand_EnvPrefix(t *testing.T) {
	if got := ClassifyCommand("FOO=bar go test ./..."); got != CmdTest {
		t.Errorf("ClassifyCommand with env prefix = %d, want CmdTest", got)
	}
}

func TestClassifyCommand_BashWrapper(t *testing.T) {
	if got := ClassifyCommand("bash ./build.sh"); got != CmdBuild {
		t.Errorf("ClassifyCommand bash wrapper = %d, want CmdBuild", got)
	}
}

func TestStripCommandPrefix(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"cd /foo && go build .", "go build ."},
		{"FOO=bar go test", "go test"},
		{"FOO=bar BAZ=1 go test", "go test"},
		{"go build .", "go build ."},
	}
	for _, tt := range tests {
		if got := stripCommandPrefix(tt.input); got != tt.want {
			t.Errorf("stripCommandPrefix(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestExtractErrors(t *testing.T) {
	output := "line1\nerror: something failed\nline3\nFAIL: test_foo\nline5"
	result := ExtractErrors(output, 10, 1000)
	if !strings.Contains(result, "error: something failed") {
		t.Errorf("ExtractErrors missing error line: %q", result)
	}
	if !strings.Contains(result, "FAIL: test_foo") {
		t.Errorf("ExtractErrors missing FAIL line: %q", result)
	}
	if strings.Contains(result, "line1") {
		t.Errorf("ExtractErrors should not contain non-error line: %q", result)
	}
}

func TestExtractErrors_SkipsExitCode(t *testing.T) {
	output := "Exit code: 1\nerror: real error"
	result := ExtractErrors(output, 10, 1000)
	if strings.Contains(result, "Exit code:") {
		t.Errorf("ExtractErrors should skip 'Exit code:' lines: %q", result)
	}
	if !strings.Contains(result, "real error") {
		t.Errorf("ExtractErrors missing real error: %q", result)
	}
}

func TestExtractErrors_MaxLines(t *testing.T) {
	output := "error: 1\nerror: 2\nerror: 3\nerror: 4\nerror: 5"
	result := ExtractErrors(output, 2, 1000)
	lines := strings.Split(result, "\n")
	if len(lines) > 2 {
		t.Errorf("ExtractErrors(maxLines=2) returned %d lines", len(lines))
	}
}

func TestExtractGitSummary(t *testing.T) {
	tests := []struct {
		cmd    string
		output string
		want   string
	}{
		{"git commit -m test", "[main abc1234] test commit", "[main abc1234] test commit"},
		{"git push origin main", "origin/main..abc1234f", "origin/main..abc1234f"},
		{"git push", "no match here", "push"},
		{"gh pr create --title test", "https://github.com/foo/bar/pull/42", "https://github.com/foo/bar/pull/42"},
		{"gh pr merge 42", "", "pr merge"},
		{"gh pr close 42", "", "pr close"},
		{"gh release create v1.0", "", "release create"},
		{"ls -la", "whatever", ""},
	}
	for _, tt := range tests {
		if got := ExtractGitSummary(tt.cmd, tt.output); got != tt.want {
			t.Errorf("ExtractGitSummary(%q, %q) = %q, want %q", tt.cmd, tt.output, got, tt.want)
		}
	}
}

func TestCheckEditGuard_Blocked(t *testing.T) {
	tests := []struct {
		command  string
		contains string
	}{
		{"git status", "git commands are prohibited"},
		{"git commit -m test", "git commands are prohibited"},
		{"gh pr view", "PR read commands"},
		{"gh pr create --title foo", "PR/release mutations"},
		{"gh release create v1", "PR/release mutations"},
		{"gh issue list", "gh commands are prohibited"},
		{"./build.sh", "Build commands"},
		{"go build .", "Build commands"},
		{"make", "Build commands"},
		{"make install", "Build commands"},
		{"pnpm build", "Build commands"},
		{"jest --watch", "Test commands"},
		{"go test ./...", "Test commands"},
		{"pytest -v", "Test commands"},
		{"cdk diff", "Deploy commands"},
		{"npx cdk synth", "Deploy commands"},
		{"terraform plan", "Deploy commands"},
		{"aws logs tail", "Log tailing"},
		{"tail -f /var/log/app.log", "Log tailing"},
		{"kubectl logs pod-1", "Log tailing"},
	}
	for _, tt := range tests {
		d := CheckEditGuard(tt.command)
		if d == nil {
			t.Errorf("CheckEditGuard(%q) = nil, want blocked", tt.command)
			continue
		}
		if !d.Blocked {
			t.Errorf("CheckEditGuard(%q).Blocked = false", tt.command)
		}
		if !strings.Contains(d.Reason, tt.contains) {
			t.Errorf("CheckEditGuard(%q).Reason missing %q: %q", tt.command, tt.contains, d.Reason)
		}
	}
}

func TestCheckEditGuard_Allowed(t *testing.T) {
	allowed := []string{
		"ls -la",
		"echo hello",
		"muxcode-agent-bus send build build test",
		"cat /tmp/foo",
		"which node",
	}
	for _, cmd := range allowed {
		if d := CheckEditGuard(cmd); d != nil {
			t.Errorf("CheckEditGuard(%q) = blocked, want allowed", cmd)
		}
	}
}

func TestCheckEditGuard_CdPrefix(t *testing.T) {
	d := CheckEditGuard("cd /foo && go build .")
	if d == nil || !d.Blocked {
		t.Error("CheckEditGuard with cd prefix should block go build")
	}
}

func TestCheckEditGuard_EnvPrefix(t *testing.T) {
	d := CheckEditGuard("envName=prod cdk diff")
	if d == nil || !d.Blocked {
		t.Error("CheckEditGuard with env prefix should block cdk")
	}
}

func TestFormatGuardBlock(t *testing.T) {
	result := FormatGuardBlock("test reason")
	var obj map[string]string
	if err := json.Unmarshal([]byte(result), &obj); err != nil {
		t.Fatalf("FormatGuardBlock output is not valid JSON: %v", err)
	}
	if obj["decision"] != "block" {
		t.Errorf("decision = %q, want %q", obj["decision"], "block")
	}
	if obj["reason"] != "test reason" {
		t.Errorf("reason = %q, want %q", obj["reason"], "test reason")
	}
}

func TestShouldPollInbox(t *testing.T) {
	tests := []struct {
		command string
		want    bool
	}{
		{"muxcode-agent-bus send build build test", true},
		{"agent-bus send test test run", true},
		{"muxcode-agent-bus send edit notify hello", false},        // self
		{"muxcode-agent-bus send build build --type event", false}, // fire-and-forget
		{"muxcode-agent-bus send build build --no-notify", false},  // no-notify
		{"muxcode-agent-bus inbox", false},                         // not a send
		{"ls -la", false},
		{"echo hello", false},
	}
	for _, tt := range tests {
		if got := ShouldPollInbox(tt.command); got != tt.want {
			t.Errorf("ShouldPollInbox(%q) = %v, want %v", tt.command, got, tt.want)
		}
	}
}

func TestWriteHookHistory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test-history.jsonl")

	entry := HookHistoryEntry{
		TS:       1000,
		Command:  "go build .",
		ExitCode: "0",
		Outcome:  "success",
	}

	if err := WriteHookHistory(path, entry, 100); err != nil {
		t.Fatalf("WriteHookHistory: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	if !strings.Contains(string(data), `"command":"go build ."`) {
		t.Errorf("history file missing command: %s", data)
	}
}

func TestWriteHookHistory_Rotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test-history.jsonl")

	// Write 5 entries, rotate at 3
	for i := 0; i < 5; i++ {
		entry := HookHistoryEntry{
			TS:       int64(1000 + i),
			Command:  "cmd",
			ExitCode: "0",
			Outcome:  "success",
		}
		if err := WriteHookHistory(path, entry, 3); err != nil {
			t.Fatalf("WriteHookHistory %d: %v", i, err)
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 3 {
		t.Errorf("expected 3 lines after rotation, got %d", len(lines))
	}

	// Should contain the last 3 entries (ts: 1002, 1003, 1004)
	if !strings.Contains(string(data), `"ts":1002`) {
		t.Error("missing ts:1002 after rotation")
	}
	if strings.Contains(string(data), `"ts":1000`) {
		t.Error("ts:1000 should have been rotated out")
	}
}

func TestProcessBashHook_Build(t *testing.T) {
	useTempBusDir(t)

	session := "test-bash-build"
	t.Setenv("BUS_SESSION", session)

	busDir := BusDir(session)
	os.MkdirAll(busDir, 0755)

	raw := `{"tool_input":{"command":"./build.sh","description":"Build"},"exit_code":0,"tool_response":{"stdout":"Build OK"}}`
	ev, _ := ParseToolEvent([]byte(raw))

	result := ProcessBashHook(session, "build", ev)
	if result.CommandType != CmdBuild {
		t.Errorf("CommandType = %d, want CmdBuild", result.CommandType)
	}
	if !result.Logged {
		t.Error("expected Logged = true")
	}

	histPath := filepath.Join(busDir, "build-history.jsonl")
	data, err := os.ReadFile(histPath)
	if err != nil {
		t.Fatalf("build history not written: %v", err)
	}
	if !strings.Contains(string(data), "./build.sh") {
		t.Error("build history missing command")
	}
}

func TestProcessBashHook_BusCommand(t *testing.T) {
	raw := `{"tool_input":{"command":"muxcode-agent-bus send build build test"},"exit_code":0}`
	ev, _ := ParseToolEvent([]byte(raw))

	result := ProcessBashHook("test-session", "edit", ev)
	// Bus commands are skipped early — CommandType stays at zero (CmdUnknown)
	if result.Logged {
		t.Error("bus commands should not be logged")
	}
	// Verify ClassifyCommand itself returns CmdBus
	if got := ClassifyCommand("muxcode-agent-bus send build build test"); got != CmdBus {
		t.Errorf("ClassifyCommand for bus command = %d, want CmdBus", got)
	}
}

func TestProcessBashHook_Runner(t *testing.T) {
	useTempBusDir(t)

	session := "test-bash-runner"
	t.Setenv("BUS_SESSION", session)

	busDir := BusDir(session)
	os.MkdirAll(busDir, 0755)

	raw := `{"tool_input":{"command":"curl http://example.com"},"exit_code":0,"tool_response":{"stdout":"OK"}}`
	ev, _ := ParseToolEvent([]byte(raw))

	// Unknown command on non-run role: not logged
	result := ProcessBashHook(session, "edit", ev)
	if result.Logged {
		t.Error("unknown command on edit role should not be logged")
	}

	// Unknown command on run role: logged
	result = ProcessBashHook(session, "run", ev)
	if !result.Logged {
		t.Error("unknown command on run role should be logged")
	}

	histPath := filepath.Join(busDir, "run-history.jsonl")
	if _, err := os.Stat(histPath); os.IsNotExist(err) {
		t.Error("run history file not created")
	}
}

func TestProcessAnalyzeHook_TriggerFile(t *testing.T) {
	useTempBusDir(t)

	session := "test-analyze-trigger"

	busDir := BusDir(session)
	os.MkdirAll(filepath.Join(busDir, "inbox"), 0755)

	raw := `{"tool_input":{"file_path":"/foo/bar.go","new_string":"hello world"}}`
	ev, _ := ParseToolEvent([]byte(raw))

	result := ProcessAnalyzeHook(session, "build", ev)
	if result.FilePath != "/foo/bar.go" {
		t.Errorf("FilePath = %q, want /foo/bar.go", result.FilePath)
	}
	if !result.TriggerWritten {
		t.Error("expected TriggerWritten = true")
	}

	triggerPath := TriggerFile(session)
	data, _ := os.ReadFile(triggerPath)
	if !strings.Contains(string(data), "/foo/bar.go") {
		t.Error("trigger file missing file path")
	}
}

func TestProcessAnalyzeHook_SkipsAgentFiles(t *testing.T) {
	raw := `{"tool_input":{"file_path":"/foo/.claude/settings.json"}}`
	ev, _ := ParseToolEvent([]byte(raw))

	result := ProcessAnalyzeHook("test-session", "edit", ev)
	if result.TriggerWritten {
		t.Error(".claude/ files should be skipped")
	}
}

func TestProcessAnalyzeHook_SkipsMuxcodeFiles(t *testing.T) {
	raw := `{"tool_input":{"file_path":"/foo/.muxcode/config"}}`
	ev, _ := ParseToolEvent([]byte(raw))

	result := ProcessAnalyzeHook("test-session", "edit", ev)
	if result.TriggerWritten {
		t.Error(".muxcode/ files should be skipped")
	}
}

func TestProcessAnalyzeHook_EmptyPath(t *testing.T) {
	raw := `{"tool_input":{}}`
	ev, _ := ParseToolEvent([]byte(raw))

	result := ProcessAnalyzeHook("test-session", "edit", ev)
	if result.FilePath != "" {
		t.Errorf("empty input should have empty FilePath, got %q", result.FilePath)
	}
}

func TestIsEnvVarName(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"FOO", true},
		{"FOO_BAR", true},
		{"A1", true},
		{"envName", true}, // camelCase for CDK compatibility
		{"Foo", true},
		{"foo", true},
		{"", false},
		{"FOO-BAR", false},
		{"FOO BAR", false},
	}
	for _, tt := range tests {
		if got := isEnvVarName(tt.input); got != tt.want {
			t.Errorf("isEnvVarName(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestFirstNonEmptyLine(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello\nworld", "hello"},
		{"\n\nhello", "hello"},
		{"  \n  \nhello", "hello"},
		{"", ""},
		{"\n\n", ""},
	}
	for _, tt := range tests {
		if got := firstNonEmptyLine(tt.input); got != tt.want {
			t.Errorf("firstNonEmptyLine(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestInterfaceToString(t *testing.T) {
	tests := []struct {
		input interface{}
		want  string
	}{
		{nil, ""},
		{"hello", "hello"},
		{float64(42), "42"},
		{1, "1"},
	}
	for _, tt := range tests {
		if got := interfaceToString(tt.input); got != tt.want {
			t.Errorf("interfaceToString(%v) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
