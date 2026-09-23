package bus

import (
	"errors"
	"strings"
	"testing"
)

// claudeTrustPane renders Claude Code's folder-trust prompt with the options in
// order and the highlight on option n, as Claude redraws it after an arrow key.
// The new layout is the 2026-09-23 app-tenant-platform pane: "No, exit" first
// and pre-selected.
func claudeTrustPane(options []string, n int) string {
	var b strings.Builder
	b.WriteString("Accessing workspace:\n\n/Users/u/Repos/app\n\n")
	b.WriteString("Quick safety check: Is this a project you created or one you trust?\n\n")
	b.WriteString("Claude Code'll be able to read, edit, and execute files here.\n\nSecurity guide\n\n")
	for i, o := range options {
		mark := " "
		if i+1 == n {
			mark = "❯"
		}
		b.WriteString(mark + " " + o + "\n")
	}
	b.WriteString("\nEnter to confirm · Esc to cancel\n")
	return b.String()
}

var (
	claudeTrustNewLayout = []string{"No, exit", "Yes, I trust this folder"}
	claudeTrustOldLayout = []string{"1. Yes, proceed", "2. No, exit"}
)

// stubClaudeTrustPane fakes the prompt with the highlight on start; when moves
// is true Down advances it the way Claude does.
func stubClaudeTrustPane(t *testing.T, options []string, start int, moves bool) *[][]string {
	t.Helper()
	calls := stubInjectionPane(t, "", nil)
	at := start
	tmuxRunner = func(args ...string) error {
		*calls = append(*calls, args)
		if moves && len(args) == 4 && args[0] == "send-keys" && args[3] == "Down" && at < len(options) {
			at++
		}
		return nil
	}
	tmuxOutputRunner = func(args ...string) (string, error) {
		if len(args) > 0 && args[0] == "capture-pane" {
			return claudeTrustPane(options, at), nil
		}
		return "", nil
	}
	return calls
}

func TestClaudeClassifyPane_NewTrustLayout(t *testing.T) {
	if got := (&ClaudeCodeProvider{}).ClassifyPane(claudeTrustPane(claudeTrustNewLayout, 1)); got != PaneTrustPrompt {
		t.Errorf("ClassifyPane = %v, want PaneTrustPrompt", got)
	}
}

func TestAcceptClaudeTrust_ConfirmsOnlyOnYes(t *testing.T) {
	cases := []struct {
		name    string
		options []string
		start   int
		moves   bool
		want    string
		wantErr bool
	}{
		{"new layout, No pre-selected", claudeTrustNewLayout, 1, true, "Down,Enter", false},
		{"old layout, Yes pre-selected", claudeTrustOldLayout, 1, true, "Enter", false},
		{"highlight never moves", claudeTrustNewLayout, 1, false, "Down,Down", true},
	}
	for _, c := range cases {
		calls := stubClaudeTrustPane(t, c.options, c.start, c.moves)
		err := AcceptClaudeTrust("s:edit.1")
		if (err != nil) != c.wantErr {
			t.Errorf("%s: error = %v, want error %v", c.name, err, c.wantErr)
		}
		if got := strings.Join(keyNames(*calls), ","); got != c.want {
			t.Errorf("%s: keys = %s, want %s", c.name, got, c.want)
		}
	}
}

// Negative control: no highlighted option means nothing to confirm, and an
// Enter could land on "No, exit".
func TestAcceptClaudeTrust_NoHighlightPressesNothing(t *testing.T) {
	calls := stubInjectionPane(t, "Accessing workspace:\n\n  No, exit\n  Yes, I trust this folder\n", nil)
	if err := AcceptClaudeTrust("s:edit.1"); err == nil {
		t.Error("an unreadable highlight must return an error")
	}
	if keys := keyNames(*calls); len(keys) != 0 {
		t.Errorf("no highlight, no keys — sent %v", keys)
	}
}

// The daemon-relaunch road: no AutoAccept pass runs, so the injection gate must
// answer the prompt and defer rather than let the wake's Escape or Enter choose
// cancel or "No, exit".
func TestCaptureInjectionTarget_ClaudeTrustPromptAcceptedAndDefers(t *testing.T) {
	session := "inject-claude-trust"
	injectionTestSession(t, session)
	calls := stubClaudeTrustPane(t, claudeTrustNewLayout, 1, true)

	_, err := captureInjectionTarget(session, "s:edit.1", "edit")
	if !errors.Is(err, ErrInjectionSkipped) {
		t.Fatalf("trust prompt must defer with ErrInjectionSkipped, got %v", err)
	}
	if got := strings.Join(keyNames(*calls), ","); got != "Down,Enter" {
		t.Errorf("trust prompt must be answered Down then Enter, sent %s", got)
	}
	if n := countLifecycleEvents(t, session, "trust-prompt"); n != 1 {
		t.Errorf("trust-prompt lifecycle rows = %d, want 1", n)
	}
}

// Negative control: the prompt's text in scrollback above a live composer is an
// answered prompt, and the gate must let the injection through untouched.
func TestCaptureInjectionTarget_AnsweredClaudeTrustPasses(t *testing.T) {
	session := "inject-claude-trust-answered"
	injectionTestSession(t, session)
	pane := claudeTrustPane(claudeTrustNewLayout, 2) + "\n❯ \n  ⏵⏵ bypass permissions on\n"
	calls := stubInjectionPane(t, pane, nil)

	if _, err := captureInjectionTarget(session, "s:edit.1", "edit"); err != nil {
		t.Fatalf("an answered trust prompt must not block injection, got %v", err)
	}
	if keys := keyNames(*calls); len(keys) != 0 {
		t.Errorf("no live prompt, no keys — sent %v", keys)
	}
}

// The incident road: AutoAccept's AcceptStartup on the new layout must move to
// Yes before confirming.
func TestClaudeAcceptStartup_TrustMovesToYes(t *testing.T) {
	calls := stubClaudeTrustPane(t, claudeTrustNewLayout, 1, true)
	if (&ClaudeCodeProvider{}).AcceptStartup("s", "s:edit.1", PaneTrustPrompt) {
		t.Error("the trust prompt is not the end of startup — bypass may follow")
	}
	if got := strings.Join(keyNames(*calls), ","); got != "Down,Enter" {
		t.Errorf("trust prompt must be answered Down then Enter, sent %s", got)
	}
}
