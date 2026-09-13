package bus

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func codexFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "codex-hooks", name))
	if err != nil {
		t.Fatalf("fixture %s: %v", name, err)
	}
	return data
}

func codexEvent(t *testing.T, name string) *ToolEvent {
	t.Helper()
	ev, err := ParseToolEvent(codexFixture(t, name))
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	return ev
}

func codexTranscriptPath(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("testdata", "codex-hooks", "transcript.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func fastTranscript(t *testing.T) {
	t.Helper()
	oldR, oldD := codexTranscriptRetries, codexTranscriptRetryDelay
	codexTranscriptRetries, codexTranscriptRetryDelay = 1, 0
	t.Cleanup(func() { codexTranscriptRetries, codexTranscriptRetryDelay = oldR, oldD })
}

// codexBusEnv gives a delivery test its own bus dir with the inbox and
// delivery directories present.
func codexBusEnv(t *testing.T) string {
	t.Helper()
	SetBusDirBase(t.TempDir())
	t.Cleanup(ResetBusDirBase)
	session := "codex-hook-delivery"
	for _, d := range []string{"inbox", "delivery"} {
		if err := os.MkdirAll(filepath.Join(BusDir(session), d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return session
}

// --- Parser dialect (every fixture) ---

func TestParseToolEvent_CodexBashShape(t *testing.T) {
	ev := codexEvent(t, "pre-tool-use-bash.json")
	if ev.HookEventName != "PreToolUse" || ev.ToolName != "Bash" {
		t.Errorf("event/tool = %q/%q", ev.HookEventName, ev.ToolName)
	}
	if ev.ToolInput.Command != "echo spike-ok; exit 3" {
		t.Errorf("command = %q", ev.ToolInput.Command)
	}
	if ev.ToolUseID != "exec-a7766c59-1518-44cf-8196-848e25be3c91" || ev.TranscriptPath == "" {
		t.Errorf("tool_use_id/transcript = %q/%q", ev.ToolUseID, ev.TranscriptPath)
	}
	if ev.ToolInput.Patch != "" || len(ev.ToolInput.PatchPaths) != 0 {
		t.Error("a Bash event must carry no patch")
	}
}

func TestParseToolEvent_CodexApplyPatchShape(t *testing.T) {
	ev := codexEvent(t, "pre-tool-use-apply-patch.json")
	if ev.ToolName != "apply_patch" {
		t.Fatalf("tool = %q", ev.ToolName)
	}
	if ev.ToolInput.Command != "" {
		t.Errorf("apply_patch must not read as a shell command, got %q", ev.ToolInput.Command)
	}
	if !strings.HasPrefix(ev.ToolInput.Patch, "*** Begin Patch") {
		t.Errorf("patch = %q", ev.ToolInput.Patch)
	}
	want := "/home/dev/repo/spike-hook-test.txt"
	if len(ev.ToolInput.PatchPaths) != 1 || ev.ToolInput.PatchPaths[0] != want {
		t.Errorf("patch paths = %v", ev.ToolInput.PatchPaths)
	}
	if ev.ToolInput.FilePath != want {
		t.Errorf("FilePath = %q, want the first patched path", ev.ToolInput.FilePath)
	}
	del := codexEvent(t, "post-tool-use-apply-patch-delete.json")
	if len(del.ToolInput.PatchPaths) != 1 || del.ToolInput.PatchPaths[0] != want {
		t.Errorf("delete patch paths = %v", del.ToolInput.PatchPaths)
	}
}

func TestParseToolEvent_StopAndPromptFields(t *testing.T) {
	stop := codexEvent(t, "stop.json")
	if stop.HookEventName != "Stop" || stop.StopHookActive || stop.LastAssistantMessage != "DONE MANGO-4471" {
		t.Errorf("stop = %+v", stop)
	}
	cont := codexEvent(t, "stop-continuation.json")
	if !cont.StopHookActive {
		t.Error("continuation Stop must carry stop_hook_active=true")
	}
	ups := codexEvent(t, "user-prompt-submit-wake.json")
	if ups.HookEventName != "UserPromptSubmit" || !IsWakeSentence(ups.Prompt) {
		t.Errorf("prompt = %q", ups.Prompt)
	}
	other := codexEvent(t, "user-prompt-submit.json")
	if IsWakeSentence(other.Prompt) {
		t.Error("an ordinary prompt read as the wake sentence")
	}
	for _, name := range []string{"session-start.json", "session-end.json"} {
		if _, err := ParseToolEvent(codexFixture(t, name)); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestParseToolEvent_ArgvCommand(t *testing.T) {
	ev, err := ParseToolEvent([]byte(`{"tool_name":"Bash","tool_input":{"command":["/bin/bash","-lc","make build"]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if ev.ToolInput.Command != "make build" {
		t.Errorf("argv command = %q, want the script", ev.ToolInput.Command)
	}
	ev, err = ParseToolEvent([]byte(`{"tool_name":"Bash","tool_input":{"command":["ls","-la"]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if ev.ToolInput.Command != "ls -la" {
		t.Errorf("argv command = %q, want joined", ev.ToolInput.Command)
	}
}

// TestParseToolEvent_ClaudeShapeUnchanged pins the Claude dialect through the
// shared parser: object tool_response, its default exit code, its output.
func TestParseToolEvent_ClaudeShapeUnchanged(t *testing.T) {
	ev, err := ParseToolEvent([]byte(`{"hook_event_name":"PostToolUse","tool_name":"Bash","tool_input":{"command":"go test ./...","description":"run tests"},"tool_response":{"stdout":"ok","stderr":"","interrupted":false}}`))
	if err != nil {
		t.Fatal(err)
	}
	if ev.ToolInput.Command != "go test ./..." || ev.ToolInput.Description != "run tests" {
		t.Errorf("input = %+v", ev.ToolInput)
	}
	if ev.codexShaped() {
		t.Error("an object tool_response is Claude's shape")
	}
	if code := ev.GetExitCode(); code != "0" {
		t.Errorf("Claude default exit code = %q, want 0", code)
	}
	if out := ev.GetOutput(10, 100); out != "ok" {
		t.Errorf("output = %q", out)
	}
	w, err := ParseToolEvent([]byte(`{"tool_name":"Write","tool_input":{"file_path":"docs/x.md","content":"# hi"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if w.ToolInput.FilePath != "docs/x.md" || w.ToolInput.Content != "# hi" {
		t.Errorf("write input = %+v", w.ToolInput)
	}
}

// --- Exit codes ---

func TestGetExitCode_CodexBashFromTranscript(t *testing.T) {
	fastTranscript(t)
	ev := codexEvent(t, "post-tool-use-bash-exit3.json")
	ev.TranscriptPath = codexTranscriptPath(t)
	if code := ev.GetExitCode(); code != "3" {
		t.Errorf("exit code = %q, want 3 (from transcript item_completed)", code)
	}
	if HookOutcome(ev.GetExitCode()) != OutcomeFailure {
		t.Error("exit 3 must be a failure")
	}
	ok := codexEvent(t, "post-tool-use-bash-ok.json")
	ok.TranscriptPath = codexTranscriptPath(t)
	if code := ok.GetExitCode(); code != "0" || HookOutcome(code) != OutcomeSuccess {
		t.Errorf("passing command: exit code = %q", code)
	}
}

// Negative control: with no transcript to consult, a Codex shell command has
// no status at all — unknown, never the Claude default of success.
func TestGetExitCode_CodexBashWithoutTranscriptIsUnknown(t *testing.T) {
	fastTranscript(t)
	ev := codexEvent(t, "post-tool-use-bash-ok.json")
	ev.TranscriptPath = filepath.Join(t.TempDir(), "missing.jsonl")
	if code := ev.GetExitCode(); code != "" {
		t.Errorf("exit code = %q, want \"\"", code)
	}
	if HookOutcome(ev.GetExitCode()) != OutcomeUnknown {
		t.Error("a status-less Codex command must be unknown, not success")
	}
}

// Negative control: Bash stdout is the command's own output. A failing build
// that prints "Exit code: 0" must not become a pass when the transcript is
// missing — the text line is trusted only for apply_patch.
func TestGetExitCode_CodexBashMisleadingStdoutStaysUnknown(t *testing.T) {
	fastTranscript(t)
	ev := codexEvent(t, "post-tool-use-bash-exit3.json")
	ev.ToolResponse = json.RawMessage(`"build failed\nExit code: 0\n"`)
	ev.TranscriptPath = filepath.Join(t.TempDir(), "missing.jsonl")
	if code := ev.GetExitCode(); code != "" {
		t.Errorf("exit code = %q, want \"\" (stdout is not a status)", code)
	}
	if HookOutcome(ev.GetExitCode()) == OutcomeSuccess {
		t.Error("misleading stdout produced a success")
	}
}

func TestGetExitCode_CodexApplyPatchFromResponseText(t *testing.T) {
	fastTranscript(t)
	ev := codexEvent(t, "post-tool-use-apply-patch-add.json")
	ev.TranscriptPath = filepath.Join(t.TempDir(), "missing.jsonl")
	if code := ev.GetExitCode(); code != "0" {
		t.Errorf("exit code = %q, want 0 from the \"Exit code:\" line", code)
	}
	if out := ev.GetOutput(10, 200); !strings.Contains(out, "Success. Updated the following files") {
		t.Errorf("output = %q", out)
	}
}

func TestCodexExitCodeFromTranscript(t *testing.T) {
	fastTranscript(t)
	path := codexTranscriptPath(t)
	cases := map[string]string{
		"exec-a7766c59-1518-44cf-8196-848e25be3c91": "3",
		"exec-c27f8b3f-be35-4131-abfb-6457250b2e04": "0",
		"exec-3d87f75c-c688-4b91-bc7b-3f1387dab31d": "0", // FileChange: status, no exit_code
	}
	for id, want := range cases {
		got, ok := CodexExitCodeFromTranscript(path, id)
		if !ok || got != want {
			t.Errorf("%s: (%q, %v), want (%q, true)", id, got, ok, want)
		}
	}
	if _, ok := CodexExitCodeFromTranscript(path, "exec-unknown"); ok {
		t.Error("unknown tool_use_id resolved")
	}
	if _, ok := CodexExitCodeFromTranscript(filepath.Join(t.TempDir(), "nope"), "exec-a7766c59-1518-44cf-8196-848e25be3c91"); ok {
		t.Error("missing transcript resolved")
	}
}

// TestScanTranscriptForItem_PrecedenceAndBoundaries pins the streaming
// reader (PR #78 review should-fix): the LATEST parseable item_completed
// for an id wins over an earlier one, exit_code wins over status within a
// record, a malformed later candidate is skipped rather than deciding, an
// unrelated id never matches, and the final line counts without a
// trailing newline.
func TestScanTranscriptForItem_PrecedenceAndBoundaries(t *testing.T) {
	rec := func(id, status, exit string) string {
		if exit != "" {
			return `{"type":"item_completed","payload":{"item":{"id":"` + id + `","status":"` + status + `","exit_code":` + exit + `}}}`
		}
		return `{"type":"item_completed","payload":{"item":{"id":"` + id + `","status":"` + status + `"}}}`
	}
	content := strings.Join([]string{
		`{"type":"item_started","payload":{"item":{"id":"exec-1"}}}`,
		rec("exec-1", "failed", ""),
		rec("exec-2", "completed", "7"),
		rec("exec-1", "completed", ""),
		rec("exec-3", "failed", ""),
		`{"type":"item_completed","payload":{"item":{"id":"exec-3","status":"completed"`,
		rec("exec-5", "completed", ""),
		rec("exec-5", "in_progress", ""),
		// A record wider than the reader's 64 KiB buffer must arrive whole.
		`{"type":"item_completed","payload":{"item":{"id":"exec-6","status":"failed","aggregated_output":"` + strings.Repeat("x", 70*1024) + `"}}}`,
		rec("exec-4", "completed", ""),
	}, "\n")
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	want := map[string]string{
		"exec-1": "0", // later record wins
		"exec-2": "7", // exit_code beats status
		"exec-3": "1", // malformed later candidate skipped
		"exec-4": "0", // last line, no trailing newline
		"exec-6": "1", // record wider than the 64 KiB read buffer
	}
	for id, w := range want {
		got, ok := scanTranscriptForItem(path, id)
		if !ok || got != w {
			t.Errorf("%s: (%q, %v), want (%q, true)", id, got, ok, w)
		}
	}
	if _, ok := scanTranscriptForItem(path, "exec-9"); ok {
		t.Error("unrelated id resolved")
	}
	if got, ok := scanTranscriptForItem(path, "exec-5"); ok {
		t.Errorf("a later record with no verdict must clear the earlier success, got %q", got)
	}
}

func TestParsePatchPaths(t *testing.T) {
	patch := "*** Begin Patch\n*** Update File: a/b.go\n@@\n-x\n+y\n*** Add File: c.txt\n+hi\n*** Delete File: d.txt\n*** Update File: a/b.go\n*** Move to: e.txt\n*** End Patch"
	got := ParsePatchPaths(patch)
	want := []string{"a/b.go", "c.txt", "d.txt", "e.txt"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("paths = %v, want %v", got, want)
	}
	if ParsePatchPaths("") != nil {
		t.Error("empty patch yields paths")
	}
}

// --- Answers ---

func TestFormatGuardBlockFor(t *testing.T) {
	var codex struct {
		Out map[string]string `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(FormatGuardBlockFor(&CodexProvider{}, "no")), &codex); err != nil {
		t.Fatal(err)
	}
	if codex.Out["hookEventName"] != "PreToolUse" || codex.Out["permissionDecision"] != "deny" || codex.Out["permissionDecisionReason"] != "no" {
		t.Errorf("codex deny = %+v", codex.Out)
	}
	var claude map[string]string
	if err := json.Unmarshal([]byte(FormatGuardBlockFor(&ClaudeCodeProvider{}, "no")), &claude); err != nil {
		t.Fatal(err)
	}
	if claude["decision"] != "block" || claude["reason"] != "no" {
		t.Errorf("claude block = %+v", claude)
	}
}

func TestFormatPromptContext(t *testing.T) {
	var out struct {
		Out map[string]string `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(FormatPromptContext("ctx")), &out); err != nil {
		t.Fatal(err)
	}
	if out.Out["hookEventName"] != "UserPromptSubmit" || out.Out["additionalContext"] != "ctx" {
		t.Errorf("prompt context = %+v", out.Out)
	}
}

func TestIsWakeSentence(t *testing.T) {
	for _, s := range []string{"You have new messages", "you have new messages.", "  You have new messages!  "} {
		if !IsWakeSentence(s) {
			t.Errorf("%q should be the wake sentence", s)
		}
	}
	for _, s := range []string{"", "You have new messages, please run the build", "new messages"} {
		if IsWakeSentence(s) {
			t.Errorf("%q should not be the wake sentence", s)
		}
	}
}

// --- Delivery through hooks ---

func TestCodexStopDelivery_DeliversRequestWithAckReceipt(t *testing.T) {
	session := codexBusEnv(t)
	req := NewMessage("edit", "build", "request", "build", "run ./build.sh and report", "")
	if err := SendNoCC(session, req); err != nil {
		t.Fatal(err)
	}
	action := CodexStopDelivery(session, "build")
	if !action.Block {
		t.Fatal("a pending request must block the stop")
	}
	if !strings.Contains(action.Reason, "run ./build.sh and report") {
		t.Errorf("reason lacks the payload: %q", action.Reason)
	}
	if !strings.Contains(action.Reason, "To reply: muxcode send edit") || !strings.Contains(action.Reason, req.ID) {
		t.Errorf("reason lacks the reply instruction: %q", action.Reason)
	}
	if msgs, _ := Peek(session, "build"); len(msgs) != 0 {
		t.Errorf("inbox still holds %d messages after delivery", len(msgs))
	}
	ds, acked := ReadReceipt(session, req.ID)
	if !acked || ds.ReceiptKind != ReceiptKindAck || ds.AckedBy != "build" {
		t.Errorf("receipt = %+v (acked=%v), want a true ack by build", ds, acked)
	}
	// Delivered once: the continuation's own Stop finds nothing.
	if CodexStopDelivery(session, "build").Block {
		t.Error("a second Stop re-delivered consumed work")
	}
}

func TestCodexStopDelivery_NothingPending(t *testing.T) {
	session := codexBusEnv(t)
	if CodexStopDelivery(session, "build").Block {
		t.Error("an empty inbox blocked the stop")
	}
}

// MUX-009 negative control: a response alone never becomes a prompt — the
// Stop hook says nothing and leaves it where it is.
func TestCodexStopDelivery_ResponseOnlyNeverPrompts(t *testing.T) {
	session := codexBusEnv(t)
	resp := NewMessage("test", "build", "response", "test", "Tests passed", "")
	if err := SendNoCC(session, resp); err != nil {
		t.Fatal(err)
	}
	if action := CodexStopDelivery(session, "build"); action.Block {
		t.Fatalf("a response-only inbox blocked the stop with %q", action.Reason)
	}
	if msgs, _ := Peek(session, "build"); len(msgs) != 1 {
		t.Errorf("response consumed without a request: inbox = %d", len(msgs))
	}
}

// The send road already refuses chrome (ErrSendChrome) and drops self-sends,
// so the two artifacts are planted straight into the inbox file — the way a
// stale row or an older binary would leave them — to exercise the consume-side
// filter itself.
func TestCodexStopDelivery_DropsSelfLoopAndChrome(t *testing.T) {
	session := codexBusEnv(t)
	f, err := os.OpenFile(InboxPath(session, "build"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []Message{
		NewMessage("build", "build", "request", "build", "self-addressed loop artifact", ""),
		NewMessage("build", "build", "response", "startup", "self-addressed startup reply", "boot-id"),
		NewMessage("edit", "build", "request", "build", "real work", ""),
		NewMessage("test", "build", "response", "test", "───────────────────────────────", ""),
	} {
		line, _ := json.Marshal(m)
		if _, err := f.Write(append(line, '\n')); err != nil {
			t.Fatal(err)
		}
	}
	_ = f.Close()
	action := CodexStopDelivery(session, "build")
	if !action.Block || !strings.Contains(action.Reason, "real work") {
		t.Fatalf("real work not delivered: %+v", action)
	}
	if strings.Contains(action.Reason, "self-addressed loop artifact") {
		t.Error("self-addressed request delivered")
	}
	if strings.Contains(action.Reason, "self-addressed startup reply") {
		t.Error("self-addressed startup reply delivered — the 2026-09-09 test-agent echo")
	}
	if strings.Contains(action.Reason, "───") {
		t.Error("chrome response delivered")
	}
}

func TestCodexPromptSubmitContext(t *testing.T) {
	session := codexBusEnv(t)
	if _, ok := CodexPromptSubmitContext(session, "build", "please run make"); ok {
		t.Error("an ordinary prompt was expanded")
	}
	ctx, ok := CodexPromptSubmitContext(session, "build", WakeSentence)
	if !ok || ctx != codexNothingPendingContext {
		t.Errorf("empty inbox: (%q, %v)", ctx, ok)
	}

	req := NewMessage("edit", "build", "request", "build", "build it", "")
	resp := NewMessage("test", "build", "response", "test", "tests green", "")
	for _, m := range []Message{req, resp} {
		if err := SendNoCC(session, m); err != nil {
			t.Fatal(err)
		}
	}
	ctx, ok = CodexPromptSubmitContext(session, "build", "You have new messages.")
	if !ok || !strings.Contains(ctx, "build it") || !strings.Contains(ctx, "tests green") {
		t.Errorf("wake expansion = (%q, %v)", ctx, ok)
	}
	if msgs, _ := Peek(session, "build"); len(msgs) != 0 {
		t.Errorf("inbox not consumed by prompt-submit: %d left", len(msgs))
	}
	for _, m := range []Message{req, resp} {
		if ds, acked := ReadReceipt(session, m.ID); !acked || ds.ReceiptKind != ReceiptKindAck {
			t.Errorf("%s: receipt = %+v acked=%v", m.ID, ds, acked)
		}
	}
}

// --- Graph pin: a codex hook row is authoritative ---

// TestCodexHookRow_IsAuthoritativeForGraph: the row hook bash writes for a
// codex build carries the transcript's real exit code, so deriveSendOutcome's
// latestAuthoritativeRow routes on it — failure to fix, success with no hold.
func TestCodexHookRow_IsAuthoritativeForGraph(t *testing.T) {
	fastTranscript(t)
	SetBusDirBase(t.TempDir())
	t.Cleanup(ResetBusDirBase)
	session := "codex-graph-pin"
	if err := os.MkdirAll(BusDir(session), 0o755); err != nil {
		t.Fatal(err)
	}
	since := time.Now().Unix() - 1

	failing := codexEvent(t, "post-tool-use-bash-exit3.json")
	failing.ToolInput.Command = "./build.sh"
	failing.TranscriptPath = codexTranscriptPath(t)
	res := ProcessBashHook(session, "build", failing)
	if res.CommandType != CmdBuild || !res.Logged {
		t.Fatalf("failing build not logged: %+v", res)
	}
	row, ok := latestAuthoritativeRow(session, "build", since)
	if !ok || row.Outcome != OutcomeFailure {
		t.Fatalf("failing build row = (%+v, %v), want an authoritative failure", row, ok)
	}

	passing := codexEvent(t, "post-tool-use-bash-ok.json")
	passing.ToolInput.Command = "./build.sh"
	passing.TranscriptPath = codexTranscriptPath(t)
	if res := ProcessBashHook(session, "build", passing); !res.Logged {
		t.Fatal("passing build not logged")
	}
	row, ok = latestAuthoritativeRow(session, "build", since)
	if !ok || row.Outcome != OutcomeSuccess {
		t.Fatalf("passing build row = (%+v, %v), want an authoritative success", row, ok)
	}

	// Negative control: no transcript, no status — the row is unknown and
	// latestAuthoritativeRow refuses it rather than calling it a pass.
	blind := codexEvent(t, "post-tool-use-bash-ok.json")
	blind.ToolInput.Command = "./build.sh"
	blind.TranscriptPath = filepath.Join(t.TempDir(), "missing.jsonl")
	_ = ProcessBashHook(session, "build", blind)
	row, _ = latestAuthoritativeRow(session, "build", since)
	if row.Outcome != OutcomeSuccess {
		t.Errorf("a status-less row outranked the real success row: %+v", row)
	}
	entries := ReadConsoleEntries(HistoryPath(session, "build"), 0)
	if len(entries) != 3 || entries[2].Outcome != OutcomeUnknown {
		t.Errorf("blind row should be recorded as unknown: %+v", entries)
	}
}

// --- Guard road ---

// codexEventWith builds a codex PreToolUse event the way the integration
// script's ev_cmd does — the fixture with tool_input.command (and, when
// given, tool_name) replaced — so the parser stays in the loop.
func codexEventWith(t *testing.T, fixture, toolName, command string) *ToolEvent {
	t.Helper()
	var raw map[string]any
	if err := json.Unmarshal(codexFixture(t, fixture), &raw); err != nil {
		t.Fatal(err)
	}
	if toolName != "" {
		raw["tool_name"] = toolName
	}
	raw["tool_input"] = map[string]any{"command": command}
	data, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	ev, err := ParseToolEvent(data)
	if err != nil {
		t.Fatalf("parse %s: %v", fixture, err)
	}
	return ev
}

// TestGuardDecisionFor_CodexPayloads pins the guard road on codex-shaped
// events, family by family, allow before deny: Bash delegation, the hook-road
// evidence rule, the doc-file guard over EVERY apply_patch path (docs named
// second, where a first-path-only check passes), the plan exemption, and an
// Atlassian MCP write from a role with no delegation rules. One denial is then
// rendered in both dialects: the decision is the rule set's, the provider's
// only say is the JSON shape. The last case is MUX-157's characterization — a
// test-role source patch passes unexamined until its Phase 2 adds the rule.
func TestGuardDecisionFor_CodexPayloads(t *testing.T) {
	t.Setenv("MUXCODE_ATLASSIAN_AUTHORITY_ROLES", "plan")
	bash := func(role, cmd string) *GuardDecision {
		return GuardDecisionFor(role, codexEventWith(t, "pre-tool-use-bash.json", "", cmd))
	}
	patch := func(role string, files ...string) *GuardDecision {
		var b strings.Builder
		b.WriteString("*** Begin Patch\n")
		for _, f := range files {
			b.WriteString("*** Update File: " + f + "\n+x\n")
		}
		b.WriteString("*** End Patch")
		return GuardDecisionFor(role, codexEventWith(t, "pre-tool-use-apply-patch.json", "", b.String()))
	}
	bundled := "muxcode send edit ack \"x\" --type response --reply-to 1-edit-a\n./build.sh\nmuxcode send edit build-result \"ok\" --type response --reply-to 1-edit-b"
	mcpWrite := GuardDecisionFor("build", codexEventWith(t, "pre-tool-use-bash.json", "mcp__claude_ai_Atlassian__editJiraIssue", ""))

	cases := []struct {
		name string
		got  *GuardDecision
		want string
	}{
		{"edit bash allowed", bash("edit", "echo hi"), ""},
		{"edit git commit denied", bash("edit", "git commit -m x"), "BLOCKED"},
		{"build lone build allowed", bash("build", "./build.sh 2>&1"), ""},
		{"build bundled build denied", bash("build", bundled), "only statement"},
		{"edit source patch allowed", patch("edit", "src/main.go"), ""},
		{"edit docs patched second denied", patch("edit", "src/main.go", "docs/architecture.md"), "plan agent"},
		{"plan docs patch allowed", patch("plan", "docs/architecture.md"), ""},
		{"build atlassian mcp write denied", mcpWrite, "BLOCKED"},
		{"test source patch passes unexamined (MUX-157 Phase 2 inverts this)", patch("test", "bus/x.go"), ""},
	}
	for _, c := range cases {
		blocked := c.got != nil && c.got.Blocked
		switch {
		case c.want == "" && blocked:
			t.Errorf("%s: denied: %s", c.name, c.got.Reason)
		case c.want != "" && !blocked:
			t.Errorf("%s: allowed, want a denial mentioning %q", c.name, c.want)
		case c.want != "" && !strings.Contains(c.got.Reason, c.want):
			t.Errorf("%s: reason %q lacks %q", c.name, c.got.Reason, c.want)
		}
	}

	d := patch("edit", "src/main.go", "docs/architecture.md")
	if d == nil || !d.Blocked {
		t.Fatal("no denial to render in both dialects")
	}
	var codex struct {
		Out map[string]string `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(FormatGuardBlockFor(&CodexProvider{}, d.Reason)), &codex); err != nil {
		t.Fatal(err)
	}
	var claude map[string]string
	if err := json.Unmarshal([]byte(FormatGuardBlockFor(&ClaudeCodeProvider{}, d.Reason)), &claude); err != nil {
		t.Fatal(err)
	}
	if codex.Out["permissionDecision"] != "deny" || codex.Out["permissionDecisionReason"] != d.Reason || claude["decision"] != "block" || claude["reason"] != d.Reason {
		t.Errorf("one decision, two dialects: codex=%+v claude=%+v", codex.Out, claude)
	}
}
