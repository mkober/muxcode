package bus

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The bug this file pins: a bus response payload — often an agent's launch
// banner — was recorded as a passing command, so consoles showed green builds
// and tests for runs that never happened.

func TestNewBusResponseEntry_LaunchBannerRejected(t *testing.T) {
	// The exact payload observed being recorded as a passing test run.
	banner := "muxcode agent launch test\n" +
		"The default interactive shell is now zsh.\n" +
		"To update your account to use zsh, please run `chsh -s /bin/zsh`.\n" +
		"LSPs are disabled"

	if _, ok := NewBusResponseEntry("test", banner, false); ok {
		t.Fatal("launch banner was accepted as a history entry; it must be rejected outright")
	}
}

func TestNewBusResponseEntry_NeverRecordsSuccess(t *testing.T) {
	// Arbitrary chat text with no failure keywords — the old heuristic scanned
	// for "failed"/"error:" and called everything else a success.
	payloads := []string{
		"Reviewed the changes and everything looks reasonable to me.",
		"Done.",
		"I have completed the task successfully.",
		"All tests pass.",
	}

	for _, payload := range payloads {
		entry, ok := NewBusResponseEntry("test", payload, false)
		if !ok {
			t.Fatalf("payload %q was rejected; expected an unverified entry", payload)
		}
		if entry.Outcome == OutcomeSuccess {
			t.Errorf("payload %q recorded outcome=success; synthesized entries must never claim success", payload)
		}
		if entry.Outcome != OutcomeUnknown {
			t.Errorf("payload %q recorded outcome=%q, want %q", payload, entry.Outcome, OutcomeUnknown)
		}
		if entry.ExitCode != "" {
			t.Errorf("payload %q recorded exit code %q; no command ran, so there is no exit code", payload, entry.ExitCode)
		}
	}
}

func TestNewBusResponseEntry_ActionNeverBecomesCommand(t *testing.T) {
	entry, ok := NewBusResponseEntry("review", "Looked over the diff, nothing jumped out.", false)
	if !ok {
		t.Fatal("expected entry to be recorded")
	}
	if entry.Command != "" {
		t.Errorf("Command = %q, want empty — a bus action must never render as an executed shell command", entry.Command)
	}
	if entry.Action != "review" {
		t.Errorf("Action = %q, want %q", entry.Action, "review")
	}
	if entry.Source != SourceBusResponse {
		t.Errorf("Source = %q, want %q", entry.Source, SourceBusResponse)
	}
}

func TestNewBusResponseEntry_DetectedFailureKeepsVerdict(t *testing.T) {
	// Over-reporting failure is safe and useful; only success may not be guessed.
	entry, ok := NewBusResponseEntry("build", "compilation aborted", true)
	if !ok {
		t.Fatal("expected entry to be recorded")
	}
	if entry.Outcome != OutcomeFailure || entry.ExitCode != "1" {
		t.Errorf("errored entry = (%q, %q), want (%q, \"1\")", entry.Outcome, entry.ExitCode, OutcomeFailure)
	}
}

func TestNewBusResponseEntry_SummaryHasNoNewline(t *testing.T) {
	entry, ok := NewBusResponseEntry("run", "Ran the script.\nSecond line.\nThird line.", false)
	if !ok {
		t.Fatal("expected entry to be recorded")
	}
	if entry.Summary != "Ran the script." {
		t.Errorf("Summary = %q, want first line only — an embedded newline corrupts the console row", entry.Summary)
	}
}

func TestLooksLikeNonResult(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		want    bool
	}{
		{"empty", "", true},
		{"whitespace only", "   \n\n  ", true},
		{"launch banner first line", "muxcode agent launch build\nsomething else", true},
		{"reasoning chatter", "Thought: 242ms", true},
		{"all chrome", "LSPs are disabled\nesc to interrupt", true},
		{"real result", "=== RUN TestFoo\n--- PASS: TestFoo", false},
		// A genuine result that merely quotes chrome further down is kept:
		// rejecting it would lose a real verdict, the costlier mistake.
		{"result quoting chrome later", "go test ./... failed\nLSPs are disabled", false},
		// The codex status line and tool-call render observed reaching a
		// peer's inbox and being paraphrased back as an answer.
		{"codex status line", "• Working (12s • esc to interrupt) · 1 background terminal running · /ps to view · /stop to close", true},
		{"truncated tool echo", "└ set -o pipefail; ./test.sh 2>&1 | awk '…", true},
		// Controls for the truncation rule: the prefix alone and the
		// ellipsis alone must both be kept, or ordinary prose is eaten.
		{"bullet prose without ellipsis", "• Build failed in bus/intent.go:412", false},
		{"ellipsis without render prefix", "waiting for the daemon…", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := LooksLikeNonResult(tc.payload); got != tc.want {
				t.Errorf("LooksLikeNonResult(%q) = %v, want %v", tc.payload, got, tc.want)
			}
		})
	}
}

// A chrome payload must be dropped at the send chokepoint, whatever road
// it arrives by — but only as a response or event. Requests are exempt,
// and that exemption is the negative control: without it a resolver that
// dropped everything would pass the first assertion.
func TestDropsAsProviderChrome(t *testing.T) {
	chrome := "• Working (12s • esc to interrupt) · /ps to view · /stop to close"
	real := "Build failed: bus/intent.go:412 undefined: SpecChoice"

	cases := []struct {
		name string
		m    Message
		want bool
	}{
		{"chrome response", Message{Type: "response", Payload: chrome}, true},
		{"chrome event", Message{Type: "event", Payload: chrome}, true},
		{"chrome request is exempt", Message{Type: "request", Payload: chrome}, false},
		{"real response is kept", Message{Type: "response", Payload: real}, false},
		{"real request is kept", Message{Type: "request", Payload: real}, false},
		// The regression this guard shipped with: a real alert naming a
		// banner phrase. Console history rejects it as non-evidence; the
		// inbox must deliver it, or a refused launch goes unreported.
		{"launch refusal alert is kept", Message{Type: "event", Payload: "plan: definition \"plan.md\" resolved at no tier — refusing to launch without it. Restore the definition (make install) and relaunch: muxcode agent launch plan"}, false},
		{"empty payload is kept", Message{Type: "response", Payload: ""}, false},
		// A real message that merely quotes a status line survives.
		{"result quoting chrome", Message{Type: "response", Payload: "build failed\n• Working (2s • esc to interrupt)"}, false},
		// Structure, not vocabulary: a genuine one-line reply that names
		// the words but does not OPEN with a render glyph must survive.
		{"prose naming chrome words", Message{Type: "response", Payload: "the pane sat at esc to interrupt for 3 minutes"}, false},
		{"prose naming a slash hint", Message{Type: "response", Payload: "tell the user /stop to close the terminal"}, false},
		// The render glyph is a heuristic: agents do compose bulleted
		// replies. This is verbatim what the build agent sent on
		// 2026-09-08 — it opens with the status bullet and must still be
		// delivered, because it carries the actual verdict.
		{"composed bulleted reply", Message{Type: "response", Payload: "• Build succeeded with no compile errors (EXIT=0). The test request was already in flight."}, false},
		{"composed bulleted list", Message{Type: "response", Payload: "• fixed the import\n• reran the suite"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := dropsAsProviderChrome(tc.m); got != tc.want {
				t.Errorf("dropsAsProviderChrome(%+v) = %v, want %v", tc.m, got, tc.want)
			}
		})
	}
}

// A dropped chrome reply must surface as ErrSendChrome, never nil. The
// daemon's completion path treats a nil return as delivered and calls
// CompleteTask, so a silent drop would close the tracked task on work that
// never ran — the very false completion the drop exists to prevent.
func TestSendChromeReturnsSuppressionError(t *testing.T) {
	session := "chrome-drop-" + strings.ReplaceAll(t.Name(), "/", "-")
	if err := os.MkdirAll(filepath.Join(BusDir(session), "inbox"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(BusDir(session)) })

	chrome := NewMessage("test", "edit", "response", "response",
		"• Working (12s • esc to interrupt) · /ps to view", "some-request-id")
	if err := Send(session, chrome); !errors.Is(err, ErrSendChrome) {
		t.Errorf("Send(chrome) = %v, want ErrSendChrome so the task is not completed", err)
	}
	if msgs, _ := Peek(session, "edit"); len(msgs) != 0 {
		t.Errorf("chrome reached the inbox: %+v", msgs)
	}

	real := NewMessage("test", "edit", "response", "response", "suite passed; EXIT=0", "some-request-id")
	if err := Send(session, real); err != nil {
		t.Fatalf("a real reply must still send: %v", err)
	}
	if msgs, _ := Peek(session, "edit"); len(msgs) != 1 {
		t.Errorf("real reply did not reach the inbox: %+v", msgs)
	}
}

// --- Read side: the console must not render a verdict-free entry as a pass ---

func consoleEntryFromJSON(t *testing.T, raw string) ConsoleEntry {
	t.Helper()
	var e ConsoleEntry
	if err := json.Unmarshal([]byte(raw), &e); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return e
}

func TestConsoleEntry_SynthesizedEntryIsNotPass(t *testing.T) {
	e := consoleEntryFromJSON(t, `{"ts":1,"action":"test","source":"bus-response","exit_code":"","outcome":"unknown","output":"some reply"}`)

	if e.IsPass() {
		t.Error("IsPass() = true for a synthesized entry; it must never count as a pass")
	}
	if e.IsFail() {
		t.Error("IsFail() = true for a synthesized entry; it claims nothing, so it is not a failure either")
	}
	if !e.IsUnverified() {
		t.Error("IsUnverified() = false for a synthesized entry")
	}
	if got, want := e.Label(), "bus:test"; got != want {
		t.Errorf("Label() = %q, want %q", got, want)
	}
}

func TestConsoleEntry_EmptyExitCodeIsNotPass(t *testing.T) {
	// The original defect on the read side: an entry with no exit code at all
	// counted as a pass, so anything that never ran a command rendered green.
	e := consoleEntryFromJSON(t, `{"ts":1,"summary":"observed something","exit_code":""}`)

	if e.IsPass() {
		t.Error("IsPass() = true for an entry with no exit code; absence of a verdict is not a pass")
	}
	if !e.IsUnverified() {
		t.Error("IsUnverified() = false for an entry with no exit code")
	}
}

func TestConsoleEntry_RealEntriesStayAuthoritative(t *testing.T) {
	pass := consoleEntryFromJSON(t, `{"ts":1,"command":"./test.sh","exit_code":"0","outcome":"success"}`)
	if !pass.IsPass() {
		t.Error("a real self-logged pass must still render as a pass")
	}
	if pass.IsUnverified() {
		t.Error("a real self-logged pass must not be treated as unverified")
	}
	if got, want := pass.Label(), "./test.sh"; got != want {
		t.Errorf("Label() = %q, want %q", got, want)
	}

	fail := consoleEntryFromJSON(t, `{"ts":2,"command":"./build.sh","exit_code":"1","outcome":"failure"}`)
	if !fail.IsFail() {
		t.Error("a real self-logged failure must still render as a failure")
	}
	if fail.IsPass() {
		t.Error("a real failure must not render as a pass")
	}

	// Numeric exit codes are also used in the wild.
	numeric := consoleEntryFromJSON(t, `{"ts":3,"command":"./test.sh","exit_code":0,"outcome":"success"}`)
	if !numeric.IsPass() {
		t.Error("numeric exit_code 0 must render as a pass")
	}
}

func TestCountOutcomes_SegregatesUnverified(t *testing.T) {
	entries := []ConsoleEntry{
		consoleEntryFromJSON(t, `{"ts":1,"command":"./test.sh","exit_code":"0"}`),
		consoleEntryFromJSON(t, `{"ts":2,"command":"./test.sh","exit_code":"1"}`),
		consoleEntryFromJSON(t, `{"ts":3,"action":"test","source":"bus-response","exit_code":"","outcome":"unknown"}`),
		consoleEntryFromJSON(t, `{"ts":4,"action":"test","source":"bus-response","exit_code":"","outcome":"unknown"}`),
	}

	pass, fail, unverified := CountOutcomes(entries)
	if pass != 1 || fail != 1 || unverified != 2 {
		t.Errorf("CountOutcomes() = (pass %d, fail %d, unverified %d), want (1, 1, 2)", pass, fail, unverified)
	}
	// The reported bug was "total 5 pass 5 fail 0" over one real run.
	if pass > 1 {
		t.Error("unverified entries leaked into the pass count — the fabricated-green bug")
	}
}

func TestHookOutcome_AbsentExitCodeIsUnknown(t *testing.T) {
	if got := HookOutcome(""); got != OutcomeUnknown {
		t.Errorf("HookOutcome(\"\") = %q, want %q", got, OutcomeUnknown)
	}
	if got := HookOutcome("0"); got != OutcomeSuccess {
		t.Errorf("HookOutcome(\"0\") = %q, want %q", got, OutcomeSuccess)
	}
	if got := HookOutcome("2"); got != OutcomeFailure {
		t.Errorf("HookOutcome(\"2\") = %q, want %q", got, OutcomeFailure)
	}
}
