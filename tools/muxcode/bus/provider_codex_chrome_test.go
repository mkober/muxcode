package bus

import (
	"strings"
	"testing"
)

// ruleLine158 reconstructs the exact payload that closed graph nodes `build`
// and `test` on 2026-09-08 at 20:31:51 and 20:32:49 — the horizontal rule codex
// draws between turns, scraped as the agents' answers 33s and 22s after
// dispatch. The pin is the incident, not an invented shape.
func ruleLine158() string { return strings.Repeat("─", 158) }

func TestIsRuleLine(t *testing.T) {
	for _, ok := range []string{ruleLine158(), "───", "  ────  ", "━━━━", "———"} {
		if !isRuleLine(ok) {
			t.Errorf("isRuleLine(%q) = false, want true", shortForLog(ok))
		}
	}
	// Negative control: prose must never read as a rule, or every real reply
	// starting with punctuation would be swallowed.
	for _, no := range []string{"", "   ", "─ build passed", "EXIT=0", "go test ./bus passed", "-- not box drawing"} {
		if isRuleLine(no) {
			t.Errorf("isRuleLine(%q) = true, want false", no)
		}
	}
}

func TestLooksLikeProviderChromeAcceptsRuleLine(t *testing.T) {
	if !LooksLikeProviderChrome(ruleLine158()) {
		t.Error("the 20:31:51 rule-line payload must drop as provider chrome")
	}
	// Negative control: 6b53863's guard must still pass composed text, including
	// the launch-refusal alert that names a banner phrase.
	for _, real := range []string{
		"Build succeeded EXIT=0",
		"agent plan has no definition; relaunch: muxcode agent launch plan",
	} {
		if LooksLikeProviderChrome(real) {
			t.Errorf("LooksLikeProviderChrome(%q) = true, want false", real)
		}
	}
}

func TestLooksLikeNonResultRejectsRuleLine(t *testing.T) {
	if !LooksLikeNonResult(ruleLine158()) {
		t.Error("a rule line is not the result of any work and must not reach console history")
	}
	if LooksLikeNonResult("go test ./bus passed EXIT=0") {
		t.Error("a genuine result must survive LooksLikeNonResult")
	}
}

func TestLooksLikeWorkingLine(t *testing.T) {
	if !LooksLikeWorkingLine("• Working (24s • esc to interrupt) · /ps to view") {
		t.Error("codex bullet-Working line must read as working")
	}
	if !LooksLikeWorkingLine("✻ Cooking… (13s · ↑ 2.1k tokens · esc to interrupt)") {
		t.Error("claude spinner counter must read as working")
	}
	if LooksLikeWorkingLine("Build succeeded EXIT=0") {
		t.Error("a real result must not read as working")
	}
}

// TestDetectTaskCompletionWorkingLineIsActive is the inverted Phase 1
// characterization: before MUX-154 Phase 2 this pane completed the task with
// the progress line as its summary.
func TestDetectTaskCompletionWorkingLineIsActive(t *testing.T) {
	p := &CodexProvider{}
	pane := strings.Join([]string{
		"• Ran ./build.sh",
		"• Working (13s • esc to interrupt)",
		"› ",
	}, "\n")
	completed, _, summary := p.DetectTaskCompletion("s", "build", pane)
	if completed {
		t.Errorf("a running turn must not complete a task (summary=%q)", summary)
	}
}

// TestDetectTaskCompletionRuleAboveComposerHolds pins the incident itself: the
// composer is visible and the nearest non-empty line above it is the rule.
func TestDetectTaskCompletionRuleAboveComposerHolds(t *testing.T) {
	p := &CodexProvider{}
	pane := strings.Join([]string{
		"some earlier turn output",
		ruleLine158(),
		"› ",
	}, "\n")
	completed, _, summary := p.DetectTaskCompletion("s", "build", pane)
	if completed {
		t.Errorf("chrome above the composer must not complete a task, got summary=%q", shortForLog(summary))
	}
	if summary != "" {
		t.Errorf("summary = %q, want empty", shortForLog(summary))
	}
}

// TestDetectTaskCompletionGenuineSendCompletes is the negative control that
// stops the guard going inert: a real reply must still complete and chain.
func TestDetectTaskCompletionGenuineSendCompletes(t *testing.T) {
	p := &CodexProvider{}
	pane := strings.Join([]string{
		"go test ./bus passed EXIT=0",
		"Sent response:response to edit",
		"› ",
	}, "\n")
	completed, errored, summary := p.DetectTaskCompletion("s", "test", pane)
	if !completed {
		t.Fatal("a genuine send must complete the task")
	}
	if errored {
		t.Error("a successful send must not report errored")
	}
	if !strings.Contains(summary, "EXIT=0") {
		t.Errorf("summary = %q, want the composed result line", summary)
	}
}

// TestDetectTaskCompletionRealLineAboveComposerCompletes keeps the composer
// branch alive: it is chrome that must hold it, not the branch itself.
func TestDetectTaskCompletionRealLineAboveComposerCompletes(t *testing.T) {
	p := &CodexProvider{}
	pane := strings.Join([]string{
		"Review: 0 must-fix, 1 nit. EXIT=0",
		ruleLine158(),
		"› ",
	}, "\n")
	completed, _, summary := p.DetectTaskCompletion("s", "review", pane)
	if !completed {
		t.Fatal("a composed line above the composer must still complete the task")
	}
	if !strings.Contains(summary, "must-fix") {
		t.Errorf("summary = %q, want the composed line, not the rule", shortForLog(summary))
	}
}

// TestIsClaudeThinkingUnchanged is the cross-provider negative control: folding
// the shared signature must not alter Claude's classifier.
func TestIsClaudeThinkingUnchanged(t *testing.T) {
	if !isClaudeThinking("✻ Cooking… (13s · ↑ 2.1k tokens · esc to interrupt)") {
		t.Error("claude working line must still read as thinking")
	}
	if isClaudeThinking("✻ Cooked for 1m 47s") {
		t.Error("claude recap line must still read as idle, or the daemon never delivers")
	}
}

func shortForLog(s string) string {
	if len(s) > 40 {
		return s[:40] + "…"
	}
	return s
}
