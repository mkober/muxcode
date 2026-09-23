package bus

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// The 2026-09-22 dps-data-services-pipelines pane: Codex 0.155.0 drew its
// self-update prompt under the banner, and the startup wake's Enter chose the
// pre-selected "Update now". codexUpdateBlockAt places the highlight on
// option n, as Codex redraws it after an arrow key.
func codexUpdateBlockAt(n int) string {
	options := []string{
		"1. Update now (runs `npm install -g @openai/codex`)",
		"2. Skip",
		"3. Skip until next version",
	}
	var b strings.Builder
	b.WriteString("  ✨ Update available! 0.155.0 -> 0.155.1\n")
	b.WriteString("  Release notes: https://github.com/openai/codex/releases/latest\n")
	for i, o := range options {
		mark := " "
		if i+1 == n {
			mark = "›"
		}
		fmt.Fprintf(&b, "%s %s\n", mark, o)
	}
	b.WriteString("  Press enter to continue\n")
	return b.String()
}

const codexComposerTail = "› Ask Codex to do anything\n" +
	"  ? for shortcuts\n" +
	"  gpt-5.6-luna medium · ~/repo\n"

// stubUpdatePromptPane fakes a pane showing above + the update prompt with the
// highlight on start. When moves is true, Down and Up move the highlight the
// way Codex does; when false the highlight stays put, the case in which Enter
// must never be sent.
func stubUpdatePromptPane(t *testing.T, above string, start int, moves bool) *[][]string {
	t.Helper()
	calls := stubInjectionPane(t, "", nil)
	at := start
	tmuxRunner = func(args ...string) error {
		*calls = append(*calls, args)
		if moves && len(args) == 4 && args[0] == "send-keys" {
			switch {
			case args[3] == "Down" && at < 3:
				at++
			case args[3] == "Up" && at > 1:
				at--
			}
		}
		return nil
	}
	tmuxOutputRunner = func(args ...string) (string, error) {
		if len(args) > 0 && args[0] == "capture-pane" {
			return above + codexUpdateBlockAt(at), nil
		}
		return "", nil
	}
	return calls
}

// keyNames reduces recorded send-keys calls to their key argument.
func keyNames(calls [][]string) []string {
	var out []string
	for _, c := range calls {
		if len(c) > 0 && c[0] == "send-keys" {
			out = append(out, c[len(c)-1])
		}
	}
	return out
}

// The order case is load-bearing: the trust and update prompts end on the same
// line, so an accepted trust prompt left in scrollback would otherwise claim
// the update prompt drawn below it and answer it with Enter.
func TestCodexUpdatePromptLive_TailAnchoredAndOrdered(t *testing.T) {
	live := codexBanner + codexUpdateBlockAt(1)
	if !codexUpdatePromptLive(live) {
		t.Error("a live update prompt must be detected")
	}
	if codexUpdatePromptLive(live + codexComposerTail) {
		t.Error("an answered update prompt in scrollback must not count as live")
	}
	if codexUpdatePromptLive(codexComposerPane) {
		t.Error("a composer with no update prompt must not count")
	}

	trustThenUpdate := codexBanner + codexTrustBlock + codexUpdateBlockAt(1)
	if !codexUpdatePromptLive(trustThenUpdate) {
		t.Error("an update prompt drawn after an accepted trust prompt must be live")
	}
	if codexTrustPromptLive(trustThenUpdate) {
		t.Error("an accepted trust prompt must not claim the update prompt's tail")
	}

	updateThenTrust := codexBanner + codexUpdateBlockAt(1) + codexTrustBlock
	if !codexTrustPromptLive(updateThenTrust) {
		t.Error("a trust prompt drawn after a skipped update prompt must be live")
	}
	if codexUpdatePromptLive(updateThenTrust) {
		t.Error("a skipped update prompt must not claim the trust prompt's tail")
	}
}

// Before the fix the box-drawing test read this pane as PaneIdle, so AutoAccept
// logged agent-ready and typed the startup wake into the prompt.
func TestCodexClassifyPane_UpdatePromptIsNotIdle(t *testing.T) {
	p := &CodexProvider{}
	for name, pane := range map[string]string{
		"after banner":       codexBanner + codexUpdateBlockAt(1),
		"after trust prompt": codexBanner + codexTrustBlock + codexUpdateBlockAt(1),
	} {
		if got := p.ClassifyPane(pane); got != PaneUpdatePrompt {
			t.Errorf("%s: ClassifyPane = %v, want PaneUpdatePrompt", name, got)
		}
	}
	if got := p.ClassifyPane(codexBanner + codexUpdateBlockAt(1) + codexComposerTail); got != PaneIdle {
		t.Errorf("answered update prompt: ClassifyPane = %v, want PaneIdle", got)
	}
}

func TestSkipCodexUpdate_ConfirmsOnlyOnSkip(t *testing.T) {
	cases := []struct {
		name    string
		start   int
		moves   bool
		want    []string
		wantErr bool
	}{
		{"from update now", 1, true, []string{"Down", "Enter"}, false},
		{"already on skip", 2, true, []string{"Enter"}, false},
		{"from skip until next version", 3, true, []string{"Up", "Enter"}, false},
		{"highlight never moves", 1, false, []string{"Down", "Down"}, true},
	}
	for _, c := range cases {
		calls := stubUpdatePromptPane(t, codexBanner, c.start, c.moves)
		err := SkipCodexUpdate("s:build.1")
		if (err != nil) != c.wantErr {
			t.Errorf("%s: SkipCodexUpdate error = %v, want error %v", c.name, err, c.wantErr)
		}
		if got := keyNames(*calls); strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("%s: keys = %v, want %v", c.name, got, c.want)
		}
	}
}

// Negative control: with no live update prompt there is nothing to answer, and
// an Enter would submit whatever the composer holds.
func TestSkipCodexUpdate_NoPromptPressesNothing(t *testing.T) {
	calls := stubInjectionPane(t, codexComposerPane, nil)
	if err := SkipCodexUpdate("s:build.1"); err == nil {
		t.Error("SkipCodexUpdate on a composer must return an error")
	}
	if keys := keyNames(*calls); len(keys) != 0 {
		t.Errorf("no update prompt, no keys — sent %v", keys)
	}
}

func TestCodexAcceptStartup_UpdatePromptSkips(t *testing.T) {
	calls := stubUpdatePromptPane(t, codexBanner, 1, true)
	if (&CodexProvider{}).AcceptStartup("s", "s:build.1", PaneUpdatePrompt) {
		t.Error("an update prompt is not the end of startup")
	}
	if got := keyNames(*calls); strings.Join(got, ",") != "Down,Enter" {
		t.Errorf("update prompt must be skipped with Down then Enter, sent %v", got)
	}
}

// The incident road: the startup wake found the prompt. It must be skipped and
// the wake deferred, never typed into the prompt as its answer.
func TestCodexSendWakeUp_UpdatePromptSkippedAndDefers(t *testing.T) {
	session := "inject-codex-update"
	injectionTestSession(t, session)
	calls := stubUpdatePromptPane(t, codexBanner, 1, true)

	err := (&CodexProvider{hooks: true}).SendWakeUp(session, "build", true)
	if !errors.Is(err, ErrInjectionSkipped) {
		t.Fatalf("update prompt must defer with ErrInjectionSkipped, got %v", err)
	}
	if got := keyNames(*calls); strings.Join(got, ",") != "Down,Enter" {
		t.Errorf("update prompt must be skipped with Down then Enter, sent %v", got)
	}
	if strings.Contains(strings.Join(sentKeys(*calls), "\n"), WakeSentence) {
		t.Error("the wake sentence must not be typed into the update prompt")
	}
	if n := countLifecycleEvents(t, session, "update-prompt"); n != 1 {
		t.Errorf("update-prompt lifecycle rows = %d, want 1", n)
	}
}

// A skip that cannot be verified still defers the payload and presses no
// Enter — the prompt waits for a person rather than installing software.
func TestCodexSendWakeUp_UnverifiedSkipPressesNoEnter(t *testing.T) {
	session := "inject-codex-update-stuck"
	injectionTestSession(t, session)
	calls := stubUpdatePromptPane(t, codexBanner, 1, false)
	sendTestRequest(t, session, "build", "MSG-CODEX-UPDATE")

	err := (&CodexProvider{}).SendWakeUp(session, "build", true)
	if !errors.Is(err, ErrInjectionSkipped) {
		t.Fatalf("unverified skip must defer with ErrInjectionSkipped, got %v", err)
	}
	for _, k := range keyNames(*calls) {
		if k == "Enter" {
			t.Fatalf("Enter sent with Update now still highlighted: %v", keyNames(*calls))
		}
	}
	if strings.Contains(strings.Join(sentKeys(*calls), "\n"), "MSG-CODEX-UPDATE") {
		t.Error("the payload must not be typed into the update prompt")
	}
	if msgs, _ := Peek(session, "build"); len(msgs) != 1 {
		t.Errorf("deferred message must stay in the inbox, have %d", len(msgs))
	}
	if n := countLifecycleEvents(t, session, "update-skip-failed"); n != 1 {
		t.Errorf("update-skip-failed lifecycle rows = %d, want 1", n)
	}
}
