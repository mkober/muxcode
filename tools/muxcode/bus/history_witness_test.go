package bus

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// claudeCall is one Bash call recorded in a scratch Claude transcript.
type claudeCall struct {
	id, command             string
	exit                    int
	background, interrupted bool
}

// claudeTranscript writes calls as a Claude session transcript under a scratch
// CLAUDE_CONFIG_DIR. One per test: the root moves with CLAUDE_CONFIG_DIR.
func claudeTranscript(t *testing.T, calls ...claudeCall) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	path := filepath.Join(dir, "projects", "-repo", "session.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, claudeRecords(calls...), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

// claudeRecords encodes calls in the transcript shape verified live on
// 2026-11-24 (MUX-185 Phase 1): an assistant tool_use record holding the
// command, and a user tool_result record whose success carries no exit code.
func claudeRecords(calls ...claudeCall) []byte {
	var b strings.Builder
	for _, c := range calls {
		use := map[string]any{"type": "assistant", "message": map[string]any{"content": []any{
			map[string]any{"type": "tool_use", "id": c.id, "name": "Bash", "input": map[string]any{"command": c.command}},
		}}}
		item := map[string]any{"type": "tool_result", "tool_use_id": c.id, "is_error": false, "content": "ok"}
		var toolUseResult any = map[string]any{"stdout": "ok", "stderr": "", "interrupted": c.interrupted, "isImage": false}
		if c.background {
			toolUseResult.(map[string]any)["backgroundTaskId"] = "bg1"
		}
		if c.exit != 0 {
			item["is_error"], item["content"] = true, fmt.Sprintf("Exit code %d\nboom", c.exit)
			toolUseResult = fmt.Sprintf("Error: Exit code %d\nboom", c.exit)
		}
		result := map[string]any{"type": "user", "message": map[string]any{"content": []any{item}}, "toolUseResult": toolUseResult}
		for _, rec := range []any{use, result} {
			data, _ := json.Marshal(rec)
			b.Write(append(data, '\n'))
		}
	}
	return []byte(b.String())
}

// hookRow builds the row ProcessBashHook would write for c, claiming exit.
func hookRow(ts int64, transcript string, c claudeCall, exit string) HookHistoryEntry {
	return HookHistoryEntry{TS: ts, Command: c.command, ExitCode: exit, Outcome: HookOutcome(exit),
		ToolUseID: c.id, TranscriptPath: transcript}
}

func consoleRow(e HookHistoryEntry) ConsoleEntry {
	return ConsoleEntry{TS: e.TS, Command: e.Command, ExitCode: json.RawMessage(`"` + e.ExitCode + `"`),
		Outcome: e.Outcome, Source: SourceHook, ToolUseID: e.ToolUseID, TranscriptPath: e.TranscriptPath}
}

var (
	callOK      = claudeCall{id: "toolu_ok", command: "./build.sh"}
	callFail    = claudeCall{id: "toolu_fail", command: "./build.sh", exit: 3}
	callBg      = claudeCall{id: "toolu_bg", command: "bash slow.sh", background: true}
	callIntr    = claudeCall{id: "toolu_intr", command: "./test.sh", interrupted: true}
	allClaudeOK = []claudeCall{callOK, callFail, callBg, callIntr}
)

// TestCorroborateHookRow: each way a row can disagree with its witness is
// refused with a reason naming it, beside the genuine success and failure the
// same transcript confirms.
func TestCorroborateHookRow(t *testing.T) {
	path := claudeTranscript(t, allClaudeOK...)
	outside := filepath.Join(t.TempDir(), "session.jsonl")
	data, _ := os.ReadFile(path)
	if err := os.WriteFile(outside, data, 0644); err != nil {
		t.Fatal(err)
	}
	numeric := consoleRow(hookRow(1, path, callOK, "0"))
	numeric.ExitCode = json.RawMessage(`0`)
	failClaimsSuccess := consoleRow(hookRow(1, path, callFail, "3"))
	failClaimsSuccess.Outcome = OutcomeSuccess
	okClaimsFailure := consoleRow(hookRow(1, path, callOK, "0"))
	okClaimsFailure.Outcome = OutcomeFailure
	cases := []struct {
		name string
		row  ConsoleEntry
		want string
	}{
		{"genuine success", consoleRow(hookRow(1, path, callOK, "0")), ""},
		{"genuine failure", consoleRow(hookRow(1, path, callFail, "3")), ""},
		{"numeric exit code", numeric, ""},
		{"exit code disagrees", consoleRow(hookRow(1, path, callFail, "0")), "transcript says 3"},
		{"genuine exit 3 relabelled success", failClaimsSuccess, "outcome success contradicts exit code 3"},
		{"genuine exit 0 relabelled failure", okClaimsFailure, "outcome failure contradicts exit code 0"},
		{"command disagrees", consoleRow(hookRow(1, path, claudeCall{id: "toolu_ok", command: "./test.sh"}, "0")), "command differs"},
		{"unknown id", consoleRow(hookRow(1, path, claudeCall{id: "toolu_none", command: "./build.sh"}, "0")), "not in transcript"},
		{"background call", consoleRow(hookRow(1, path, callBg, "0")), "background"},
		{"interrupted call", consoleRow(hookRow(1, path, callIntr, "0")), "interrupted"},
		{"no witness", consoleRow(hookRow(1, "", callOK, "0")), "no witness"},
		{"transcript outside the root", consoleRow(hookRow(1, outside, callOK, "0")), "outside the provider transcript root"},
		{"relative transcript", consoleRow(hookRow(1, "projects/-repo/session.jsonl", callOK, "0")), "not absolute"},
	}
	for _, tc := range cases {
		got := corroborateHookRow(tc.row)
		if tc.want == "" && got != "" {
			t.Errorf("%s: refused (%s), want accepted", tc.name, got)
		}
		if tc.want != "" && !strings.Contains(got, tc.want) {
			t.Errorf("%s: reason %q, want one naming %q", tc.name, got, tc.want)
		}
	}
}

// TestWitnessCacheComparesEveryRow: a warm cache holds the transcript's facts,
// never a verdict — a forged row reusing a verified id with another exit code,
// command or transcript is still refused, while the genuine row is accepted on
// every read.
func TestWitnessCacheComparesEveryRow(t *testing.T) {
	path := claudeTranscript(t, callOK)
	genuine := consoleRow(hookRow(1, path, callOK, "0"))
	for i := 0; i < 2; i++ {
		if r := corroborateHookRow(genuine); r != "" {
			t.Fatalf("read %d: genuine row refused: %s", i+1, r)
		}
	}
	copied := filepath.Join(t.TempDir(), "session.jsonl")
	data, _ := os.ReadFile(path)
	if err := os.WriteFile(copied, data, 0644); err != nil {
		t.Fatal(err)
	}
	forgeries := map[string]ConsoleEntry{
		"exit code":  consoleRow(hookRow(2, path, callOK, "1")),
		"command":    consoleRow(hookRow(2, path, claudeCall{id: "toolu_ok", command: "./deploy.sh"}, "0")),
		"transcript": consoleRow(hookRow(2, copied, callOK, "0")),
	}
	for name, row := range forgeries {
		if r := corroborateHookRow(row); r == "" {
			t.Errorf("forged row with a different %s accepted from a warm cache", name)
		}
	}
	if r := corroborateHookRow(genuine); r != "" {
		t.Errorf("genuine row refused after the forgeries: %s", r)
	}
}

// TestWitnessSeesCallsWrittenAfterAMiss: a miss is cached against the file's
// size, so a record the provider appends after the first read is found.
func TestWitnessSeesCallsWrittenAfterAMiss(t *testing.T) {
	path := claudeTranscript(t, callOK)
	late := consoleRow(hookRow(1, path, callFail, "3"))
	if r := corroborateHookRow(late); !strings.Contains(r, "not in transcript") {
		t.Fatalf("before append: reason %q, want not in transcript", r)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(claudeRecords(callFail)); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if r := corroborateHookRow(late); r != "" {
		t.Errorf("after append: refused (%s), want the new record found", r)
	}
}

// TestClaudeWitnessRequiresAffirmativeCompletion: exit 0 is inferred only from
// a well-formed foreground result. Each malformed record below — no or null
// is_error, no or null toolUseResult, a background key holding null, no,
// null or non-boolean interrupted — sits beside a well-formed success in the
// same transcript.
func TestClaudeWitnessRequiresAffirmativeCompletion(t *testing.T) {
	path := claudeTranscript(t, callOK)
	record := func(id string, item map[string]any, tur string) string {
		use, _ := json.Marshal(map[string]any{"type": "assistant", "message": map[string]any{"content": []any{
			map[string]any{"type": "tool_use", "id": id, "name": "Bash", "input": map[string]any{"command": "./build.sh"}},
		}}})
		item["type"], item["tool_use_id"], item["content"] = "tool_result", id, "ok"
		res, _ := json.Marshal(map[string]any{"type": "user", "message": map[string]any{"content": []any{item}}})
		if tur != "" {
			res = append(res[:len(res)-1], []byte(`,"toolUseResult":`+tur+`}`)...)
		}
		return string(use) + "\n" + string(res) + "\n"
	}
	cases := map[string]string{
		"toolu_noerr":     record("toolu_noerr", map[string]any{}, `{"stdout":"ok","interrupted":false}`),
		"toolu_notur":     record("toolu_notur", map[string]any{"is_error": false}, ""),
		"toolu_nulltur":   record("toolu_nulltur", map[string]any{"is_error": false}, `null`),
		"toolu_strtur":    record("toolu_strtur", map[string]any{"is_error": false}, `"done"`),
		"toolu_bgnull":    record("toolu_bgnull", map[string]any{"is_error": false}, `{"stdout":"","interrupted":false,"backgroundTaskId":null}`),
		"toolu_nointr":    record("toolu_nointr", map[string]any{"is_error": false}, `{"stdout":"ok"}`),
		"toolu_badintr":   record("toolu_badintr", map[string]any{"is_error": false}, `{"stdout":"ok","interrupted":"no"}`),
		"toolu_nullintr":  record("toolu_nullintr", map[string]any{"is_error": false}, `{"stdout":"ok","interrupted":null}`),
		"toolu_nullerr":   record("toolu_nullerr", map[string]any{"is_error": nil}, `{"stdout":"ok","interrupted":false}`),
		"toolu_errnoline": record("toolu_errnoline", map[string]any{"is_error": true}, `"Error: killed"`),
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatal(err)
	}
	for _, lines := range cases {
		if _, err := f.WriteString(lines); err != nil {
			t.Fatal(err)
		}
	}
	f.Close()
	for id := range cases {
		row := consoleRow(hookRow(1, path, claudeCall{id: id, command: "./build.sh"}, "0"))
		if r := corroborateHookRow(row); !strings.Contains(r, "not final") {
			t.Errorf("%s: reason %q, want the call refused as not final", id, r)
		}
	}
	if r := corroborateHookRow(consoleRow(hookRow(1, path, callOK, "0"))); r != "" {
		t.Errorf("well-formed success refused beside them: %s", r)
	}
}

// TestCorroborateCodexRow reads the captured Codex rollout: the item's argv
// carries the command and its exit_code the verdict. A completed record with
// no exit_code is not final, beside the captured record that has one.
func TestCorroborateCodexRow(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	path := filepath.Join(home, "sessions", "2026", "rollout.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(codexTranscriptPath(t))
	if err != nil {
		t.Fatal(err)
	}
	noExit := `{"type":"event_msg","payload":{"type":"item_completed","item":{"id":"exec-noexit","type":"CommandExecution",` +
		`"status":"completed","command":["/bin/bash","-lc","./build.sh"]}}}` + "\n"
	if err := os.WriteFile(path, append(data, noExit...), 0644); err != nil {
		t.Fatal(err)
	}
	if r := corroborateHookRow(consoleRow(hookRow(1, path, claudeCall{id: "exec-noexit", command: "./build.sh"}, "0"))); !strings.Contains(r, "no exit_code") {
		t.Errorf("completed codex record without exit_code: reason %q, want it refused", r)
	}
	call := claudeCall{id: "exec-a7766c59-1518-44cf-8196-848e25be3c91", command: "echo spike-ok; exit 3"}
	if r := corroborateHookRow(consoleRow(hookRow(1, path, call, "3"))); r != "" {
		t.Errorf("genuine codex row refused: %s", r)
	}
	if r := corroborateHookRow(consoleRow(hookRow(1, path, call, "0"))); !strings.Contains(r, "transcript says 3") {
		t.Errorf("codex row claiming 0: reason %q, want the disagreement named", r)
	}
}

// TestDeriveSendOutcomeIgnoresContradictedRow is the verdict the reader feeds:
// a lone forged success citing a genuine background call, with a reply that
// carries no sentinel, decides nothing — the node holds. The genuine completed
// success and the honest self-report beside it still decide.
func TestDeriveSendOutcomeIgnoresContradictedRow(t *testing.T) {
	now := time.Now().Unix()
	bgBuild := claudeCall{id: "toolu_bgbuild", command: "./build.sh", background: true}
	path := claudeTranscript(t, callOK, bgBuild)
	cases := []struct {
		name string
		row  HookHistoryEntry
		want string
	}{
		{"forged success citing a background call", hookRow(now, path, bgBuild, "0"), OutcomeUnknown},
		{"genuine completed success", hookRow(now, path, callOK, "0"), OutcomeSuccess},
		{"honest self-reported failure", HookHistoryEntry{TS: now, Command: "./build.sh", ExitCode: "1",
			Outcome: OutcomeFailure, Source: SourceSelfReported}, OutcomeFailure},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			useTempBusDir(t)
			writeRows(t, "build", tc.row)
			resp := NewMessage("build", "edit", "response", "response", "done", "")
			if err := Send(runTestSession, resp); err != nil {
				t.Fatal(err)
			}
			n := &Node{ID: "a", Role: "build", Action: "build"}
			got, _ := deriveSendOutcome(runTestSession, n, &GraphNodeStatus{StartedAt: now - 1}, Task{ResponseID: resp.ID})
			if got != tc.want {
				t.Errorf("outcome = %q, want %q", got, tc.want)
			}
		})
	}
}

// writeRows appends entries to role's history through the real writer.
func writeRows(t *testing.T, role string, rows ...HookHistoryEntry) {
	t.Helper()
	if err := os.MkdirAll(BusDir(runTestSession), 0755); err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if err := WriteHookHistory(HistoryPath(runTestSession, role), r, 100); err != nil {
			t.Fatal(err)
		}
	}
}

// rejectionsFor returns the history-row-rejected details naming file.
func rejectionsFor(t *testing.T, file string) []string {
	t.Helper()
	entries, err := ReadLifecycleLog(runTestSession)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if e.Event == "history-row-rejected" && strings.Contains(e.Detail, file) {
			out = append(out, e.Detail)
		}
	}
	return out
}

// TestReaderNegativeControls is Phase 4's control list at the reader, each
// case beside the one that must still pass.
func TestReaderNegativeControls(t *testing.T) {
	now := time.Now().Unix()

	t.Run("genuine hook failure outranks a newer forged success", func(t *testing.T) {
		useTempBusDir(t)
		path := claudeTranscript(t, callFail)
		forged := HookHistoryEntry{TS: now + 1, Command: "./build.sh", ExitCode: "0", Outcome: OutcomeSuccess}
		writeRows(t, "build", hookRow(now, path, callFail, "3"), forged)
		row, ok := latestAuthoritativeRow(runTestSession, "build", now-1)
		if !ok || row.Outcome != OutcomeFailure {
			t.Fatalf("row = (%+v, %v), want the witnessed failure", row, ok)
		}
		if got := rejectionsFor(t, HistoryPath(runTestSession, "build")); len(got) != 1 || !strings.Contains(got[0], "no witness") {
			t.Errorf("rejection rows = %q, want one naming the missing witness", got)
		}
	})

	t.Run("a refusal is logged once however often it is read", func(t *testing.T) {
		useTempBusDir(t)
		forged := HookHistoryEntry{TS: now, Command: "./build.sh", ExitCode: "0", Outcome: OutcomeSuccess}
		writeRows(t, "deploy", forged)
		for i := 0; i < 3; i++ {
			latestAuthoritativeRow(runTestSession, "deploy", now-1)
		}
		if got := rejectionsFor(t, HistoryPath(runTestSession, "deploy")); len(got) != 1 {
			t.Errorf("rejection rows = %d, want 1", len(got))
		}
	})

	t.Run("forged success citing a genuine background call", func(t *testing.T) {
		useTempBusDir(t)
		path := claudeTranscript(t, callBg, callOK)
		writeRows(t, "run", hookRow(now, path, callOK, "0"), hookRow(now+1, path, callBg, "0"))
		row, ok := latestAuthoritativeRow(runTestSession, "run", now-1)
		if !ok || row.ToolUseID != callOK.id {
			t.Errorf("row = (%+v, %v), want the older completed success over the newer background-citing row", row, ok)
		}
		if got := rejectionsFor(t, HistoryPath(runTestSession, "run")); len(got) != 1 || !strings.Contains(got[0], "background") {
			t.Errorf("rejection rows = %q, want one naming the background call", got)
		}
	})

	t.Run("a contradicted row alone is no evidence", func(t *testing.T) {
		path := claudeTranscript(t, callOK, callFail, callBg)
		outside := filepath.Join(t.TempDir(), "session.jsonl")
		data, _ := os.ReadFile(path)
		if err := os.WriteFile(outside, data, 0644); err != nil {
			t.Fatal(err)
		}
		relabelled := hookRow(now, path, callFail, "3")
		relabelled.Outcome = OutcomeSuccess
		lone := map[string]HookHistoryEntry{
			"exit code mismatch":    hookRow(now, path, callFail, "0"),
			"outcome relabelled":    relabelled,
			"background call":       hookRow(now, path, callBg, "0"),
			"outside the root":      hookRow(now, outside, callOK, "0"),
			"unknown tool_use_id":   hookRow(now, path, claudeCall{id: "toolu_none", command: "./build.sh"}, "0"),
			"genuine success (ctl)": hookRow(now, path, callOK, "0"),
		}
		for name, r := range lone {
			useTempBusDir(t)
			writeRows(t, "build", r)
			row, ok := latestAuthoritativeRow(runTestSession, "build", now-1)
			if name == "genuine success (ctl)" {
				if !ok || row.Outcome != OutcomeSuccess || row.Source != SourceHook {
					t.Errorf("%s: row = (%+v, %v), want it accepted as hook", name, row, ok)
				}
				continue
			}
			if ok {
				t.Errorf("%s: returned as evidence: %+v", name, row)
			}
		}
	})

	t.Run("self-report still stands alone on a witness-less road", func(t *testing.T) {
		useTempBusDir(t)
		writeRows(t, "test", HookHistoryEntry{TS: now, Command: "pnpm test", ExitCode: "0",
			Outcome: OutcomeSuccess, Source: SourceSelfReported})
		row, ok := latestAuthoritativeRow(runTestSession, "test", now-1)
		if !ok || row.Outcome != OutcomeSuccess {
			t.Errorf("row = (%+v, %v), want the self-report as last resort", row, ok)
		}
	})

	t.Run("legacy hook row inside the window ranks below a self-report", func(t *testing.T) {
		useTempBusDir(t)
		legacy := HookHistoryEntry{TS: now + 1, Command: "./build.sh", ExitCode: "0", Outcome: OutcomeSuccess}
		writeRows(t, "build", HookHistoryEntry{TS: now, Command: "./build.sh", ExitCode: "1",
			Outcome: OutcomeFailure, Source: SourceSelfReported}, legacy)
		row, ok := latestAuthoritativeRow(runTestSession, "build", now-1)
		if !ok || row.Outcome != OutcomeFailure {
			t.Errorf("row = (%+v, %v), want the self-report over the unwitnessed legacy row", row, ok)
		}
	})

	t.Run("an unwitnessed row alone is last resort, relabelled", func(t *testing.T) {
		useTempBusDir(t)
		writeRows(t, "build", HookHistoryEntry{TS: now, Command: "./build.sh", ExitCode: "0", Outcome: OutcomeSuccess})
		row, ok := latestAuthoritativeRow(runTestSession, "build", now-1)
		if !ok || row.Source != SourceSelfReported {
			t.Errorf("row = (%+v, %v), want it returned with its demotion visible as self-reported", row, ok)
		}
	})

	t.Run("legacy hook row before since is never read", func(t *testing.T) {
		useTempBusDir(t)
		writeRows(t, "commit", HookHistoryEntry{TS: now - 100, Command: "git push", ExitCode: "0", Outcome: OutcomeSuccess})
		if row, ok := latestAuthoritativeRow(runTestSession, "commit", now-1); ok {
			t.Errorf("row before since returned: %+v", row)
		}
		if got := rejectionsFor(t, HistoryPath(runTestSession, "commit")); len(got) != 0 {
			t.Errorf("a row never read was logged rejected: %q", got)
		}
	})
}
