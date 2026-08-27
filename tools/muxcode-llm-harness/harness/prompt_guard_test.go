package harness

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

const guardRun = "wf-1787778000-coding-pr"
const guardCmd = "muxcode graph approve " + guardRun + " commit-gate"

func TestCheckApproveGuard_NamedTargetsAllowed(t *testing.T) {
	cases := []string{
		"approve commit-gate",                         // node id named
		"approve the gate called \"commit-gate\" now", // quoted node id
		"approve " + guardRun,                         // full run id
		"approve run wf-1787778000 please",            // >= 8-char run prefix
		"approve COMMIT-GATE",                         // case-insensitive
	}
	for _, text := range cases {
		if reason := checkApproveGuard("prompt", text, guardCmd); reason != "" {
			t.Errorf("%q must allow the approve, got: %s", text, reason)
		}
	}
}

func TestCheckApproveGuard_UnnamedRefused(t *testing.T) {
	cases := []string{
		"approve whatever is waiting", // the spec's verbatim negative control
		"approve everything",
		"approve it",
		// Substring bait: "gate" appears inside commit-gate but names
		// nothing — token matching must be exact for node ids.
		"approve the gate",
		// Short run prefix must not qualify.
		"approve wf-17",
	}
	for _, text := range cases {
		if reason := checkApproveGuard("prompt", text, guardCmd); reason == "" {
			t.Errorf("%q must refuse the approve", text)
		}
	}
}

func TestCheckApproveGuard_ScopeLimits(t *testing.T) {
	// Non-approve graph commands pass untouched.
	if r := checkApproveGuard("prompt", "what is waiting", "muxcode graph list"); r != "" {
		t.Errorf("graph list must not be guarded: %s", r)
	}
	// Other roles are outside the guard's scope.
	if r := checkApproveGuard("build", "approve whatever", guardCmd); r != "" {
		t.Errorf("non-prompt roles must not be guarded: %s", r)
	}
	// A path-prefixed binary still matches the approve shape.
	cmd := "/usr/local/bin/muxcode graph approve " + guardRun + " commit-gate"
	if r := checkApproveGuard("prompt", "approve nothing in particular", cmd); r == "" {
		t.Error("path-prefixed approve must still be guarded")
	}
	// A malformed approve is left for the CLI's usage error.
	if r := checkApproveGuard("prompt", "approve", "muxcode graph approve "+guardRun); r != "" {
		t.Errorf("malformed approve is the CLI's problem: %s", r)
	}
}

// TestFilter_ApproveGuardWired proves the guard runs on the real Check
// path with the batch text the loop hands over — not just as a helper.
func TestFilter_ApproveGuardWired(t *testing.T) {
	mkCall := func(cmd string) ToolCall {
		args, _ := json.Marshal(map[string]string{"command": cmd})
		return ToolCall{Function: FunctionCall{Name: "bash", Arguments: args}}
	}

	f := NewFilter("prompt")
	f.TaskText = "approve whatever is waiting"
	res := f.Check(mkCall(guardCmd))
	if !res.Blocked || !strings.Contains(res.Reason, "NAME the gate") {
		t.Errorf("unnamed approve must block through Check: %+v", res)
	}

	// Positive control: a filter that blocks every approve proves nothing.
	f = NewFilter("prompt")
	f.TaskText = "approve commit-gate on the coding run"
	if res := f.Check(mkCall(guardCmd)); res.Blocked {
		t.Errorf("named approve must pass through Check: %+v", res)
	}
}

// TestFalseSuccessGuardHelpers pins the code-level backstop plan asked
// for: a denied tool result is detected, an honest failure response is
// left alone, and a success-shaped summary over a denial would be
// rewritten (observed live: qwen3:4b answered "succeeded" for a
// profile-denied command, twice).
func TestFalseSuccessGuardHelpers(t *testing.T) {
	denied := "Error: command not allowed by tool profile: date"
	if got := firstDenialLine("some output\n" + denied + "\nmore"); got != denied {
		t.Errorf("firstDenialLine = %q, want the denial line", got)
	}
	if got := firstDenialLine("run wf-1 complete\n3 nodes done"); got != "" {
		t.Errorf("clean output must carry no denial, got %q", got)
	}
	if got := enforceDenialPrefix("succeeded", denied); !strings.HasPrefix(got, "BLOCKED:") {
		t.Errorf("a success-shaped summary over a denial must be rewritten, got %q", got)
	}
	// The negation bait (plan's catch): "no errors" defeated the old
	// substring heuristic — the prefix rule must rewrite it too.
	if got := enforceDenialPrefix("launched, completed with no errors", denied); !strings.HasPrefix(got, "BLOCKED:") {
		t.Errorf("negation wording must not suppress the guard, got %q", got)
	}
	honest := "BLOCKED: approval requires a named gate"
	if got := enforceDenialPrefix(honest, denied); got != honest {
		t.Errorf("an honest BLOCKED response must pass untouched, got %q", got)
	}
	clean := "started run wf-123"
	if got := enforceDenialPrefix(clean, ""); got != clean {
		t.Errorf("no denial means no rewrite, got %q", got)
	}
}

// TestDenialTrackerRecovery pins the recovery rule: a denial latches,
// a clean re-run of the SAME command shape clears it, and a clean run
// of a DIFFERENT command clears nothing (live 2026-08-27: graph create
// failed validation, the fixed retry succeeded, and the answer still
// wore the stale BLOCKED banner).
func TestDenialTrackerRecovery(t *testing.T) {
	bash := func(cmd string) ToolCall {
		return ToolCall{Function: FunctionCall{
			Name:      "bash",
			Arguments: json.RawMessage(`{"command":` + strconv.Quote(cmd) + `}`),
		}}
	}
	create := "muxcode graph create --json '{...}'"

	var d denialTracker
	d.observe(bash(create), "Error: graph \"x\" failed validation; not written")
	if d.line == "" {
		t.Fatal("a validation failure must latch the denial")
	}
	d.observe(bash("ls -la"), "total 8")
	if d.line == "" {
		t.Error("an unrelated success must NOT clear the denial — ls after a denied commit is not recovery")
	}
	d.observe(bash(create), `Created project graph "x" at .muxcode/graphs/x.json`)
	if d.line != "" {
		t.Errorf("a clean re-run of the denied command must clear the latch, still holds %q", d.line)
	}

	// A recovered batch can latch a NEW denial afterward.
	d.observe(bash("muxcode graph run x"), "REFUSED: prompt role lacks authority")
	if d.line == "" {
		t.Error("a fresh denial after recovery must latch again")
	}
}

// TestRequestTaskText pins the review catch: only request payloads are
// user-authored — a system event in the same batch that names a gate id
// must not let the guard treat that id as user-named.
func TestRequestTaskText(t *testing.T) {
	msgs := []Message{
		{Type: "request", Payload: "approve whatever is waiting"},
		{Type: "event", Payload: "gate commit-gate waiting on run " + guardRun},
		{Type: "response", Payload: "previous answer mentioning " + guardRun},
	}
	text := requestTaskText(msgs)
	if text != "approve whatever is waiting" {
		t.Fatalf("system payloads leaked into the guard text: %q", text)
	}
	if reason := checkApproveGuard("prompt", text, guardCmd); reason == "" {
		t.Error("an event-named gate must still refuse — the user never named it")
	}
}
