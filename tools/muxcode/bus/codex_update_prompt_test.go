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

// The 2026-09-23 muxcode road: the pre-type capture shows the composer, and
// Codex draws the update prompt only after the text is typed. lateAfter is the
// number of composer captures before the prompt appears; a large value is the
// negative control in which it never does.
func stubLateUpdatePrompt(t *testing.T, lateAfter int) *[][]string {
	t.Helper()
	calls := stubUpdatePromptPane(t, codexBanner, 1, true)
	prompt := tmuxOutputRunner
	captures := 0
	tmuxOutputRunner = func(args ...string) (string, error) {
		if len(args) > 0 && args[0] == "capture-pane" {
			captures++
			if captures <= lateAfter {
				return codexComposerPane, nil
			}
		}
		return prompt(args...)
	}
	return calls
}

func TestCodexSendWakeUp_LateUpdatePromptWithholdsEnter(t *testing.T) {
	for _, road := range []struct {
		name    string
		hooks   bool
		payload string
	}{
		{"hook road", true, WakeSentence},
		{"scrape road", false, "MSG-CODEX-LATE-UPDATE"},
	} {
		session := "inject-codex-late-" + strings.ReplaceAll(road.name, " ", "-")
		injectionTestSession(t, session)
		if !road.hooks {
			sendTestRequest(t, session, "build", road.payload)
		}
		calls := stubLateUpdatePrompt(t, 1)

		err := (&CodexProvider{hooks: road.hooks}).SendWakeUp(session, "build", true)
		if !errors.Is(err, ErrInjectionSkipped) {
			t.Fatalf("%s: late update prompt must defer with ErrInjectionSkipped, got %v", road.name, err)
		}
		keys := keyNames(*calls)
		if len(keys) != 3 || !strings.Contains(keys[0], road.payload) || keys[1] != "Down" || keys[2] != "Enter" {
			t.Errorf("%s: want payload, then Skip (Down, Enter) and no submitting Enter; sent %v", road.name, keys)
		}
		if n := countLifecycleEvents(t, session, "enter-withheld"); n != 1 {
			t.Errorf("%s: enter-withheld lifecycle rows = %d, want 1", road.name, n)
		}
		if !road.hooks {
			if msgs, _ := Peek(session, "build"); len(msgs) != 1 {
				t.Errorf("%s: withheld message must stay in the inbox, have %d", road.name, len(msgs))
			}
		}
	}
}

// The review's residual window: the prompt arrives after the Enter, hiding the
// composer. Verification must not read the hidden text as delivered — the
// message stays queued and the prompt is skipped.
func TestCodexSendWakeUp_PromptAfterEnterKeepsMessage(t *testing.T) {
	session := "inject-codex-after-enter"
	injectionTestSession(t, session)
	sendTestRequest(t, session, "build", "MSG-CODEX-AFTER-ENTER")
	calls := stubLateUpdatePrompt(t, 2)

	if err := (&CodexProvider{}).SendWakeUp(session, "build", true); err != nil {
		t.Fatalf("the Enter was sent, so SendWakeUp returns nil; got %v", err)
	}
	keys := keyNames(*calls)
	if len(keys) != 4 || keys[1] != "Enter" || keys[2] != "Down" || keys[3] != "Enter" {
		t.Errorf("want payload, Enter, then Skip (Down, Enter); sent %v", keys)
	}
	if msgs, _ := Peek(session, "build"); len(msgs) != 1 {
		t.Errorf("a message hidden by a late prompt must stay in the inbox, have %d", len(msgs))
	}
}

// Negative control: no prompt ever arrives, so the guard must not withhold the
// Enter that submits the text.
func TestCodexSendWakeUp_NoLatePromptSubmits(t *testing.T) {
	session := "inject-codex-no-late"
	injectionTestSession(t, session)
	calls := stubLateUpdatePrompt(t, 1<<30)

	if err := (&CodexProvider{hooks: true}).SendWakeUp(session, "build", true); err != nil {
		t.Fatalf("composer throughout must inject cleanly, got %v", err)
	}
	if got := keyNames(*calls); strings.Join(got, ",") != WakeSentence+",Enter" {
		t.Errorf("want the wake sentence then Enter, sent %v", got)
	}
	if n := countLifecycleEvents(t, session, "enter-withheld"); n != 0 {
		t.Errorf("enter-withheld lifecycle rows = %d, want 0", n)
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
