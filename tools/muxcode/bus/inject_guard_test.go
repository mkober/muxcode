package bus

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// Panes from the 2026-09-09 is-advising-gateway incident, with the user's
// prompt and paths made generic. Codex 0.153.4 draws its directory-trust
// prompt inline under the banner; the wake-up typed into it made Codex quit,
// and the next wake-up landed in the shell underneath.
const (
	codexBanner = "╭────────────────────────────────────────────╮\n" +
		"│ >_ OpenAI Codex (v0.153.4)                 │\n" +
		"│ model:     loading   /model to change      │\n" +
		"│ directory: ~/repo                          │\n" +
		"╰────────────────────────────────────────────╯\n" +
		"› Ask Codex to do anything\n" +
		"  ? for shortcuts\n"

	codexTrustBlock = "> You are in /home/dev/repo\n" +
		"  Do you trust the contents of this directory? Working with untrusted contents comes with higher risk of prompt injection. Trusting the directory allows\n" +
		"  project-local config, hooks, and exec policies to load.\n" +
		"› 1. Yes, continue\n" +
		"  2. No, quit\n" +
		"  Press enter to continue\n"

	codexTrustPromptPane = codexBanner + codexTrustBlock

	codexComposerPane = codexBanner + codexTrustBlock +
		"› Ask Codex to do anything\n" +
		"  ? for shortcuts\n" +
		"  gpt-5.6-luna medium · ~/repo\n"

	codexShellPane = codexTrustBlock +
		"dev@host /home/dev/repo (main)\n" +
		"->\n" +
		"dev@host /home/dev/repo (main)\n" +
		"->  You have new messages\n" +
		"-bash: You: command not found\n" +
		"dev@host /home/dev/repo (main)\n" +
		"-> "
)

// stubInjectionPane routes every capture-pane through content (or captErr)
// and records every tmux command, so the guard runs without a live session.
func stubInjectionPane(t *testing.T, content string, captErr error) *[][]string {
	t.Helper()
	origRun, origQuiet, origOut, origDelay := tmuxRunner, tmuxQuietRunner, tmuxOutputRunner, injectVerifyDelay
	var calls [][]string
	tmuxRunner = func(args ...string) error {
		calls = append(calls, args)
		return nil
	}
	tmuxQuietRunner = func(args ...string) error { return nil }
	tmuxOutputRunner = func(args ...string) (string, error) {
		if len(args) > 0 && args[0] == "capture-pane" {
			return content, captErr
		}
		return "", nil
	}
	injectVerifyDelay = 0
	t.Cleanup(func() {
		tmuxRunner, tmuxQuietRunner, tmuxOutputRunner, injectVerifyDelay = origRun, origQuiet, origOut, origDelay
	})
	return &calls
}

func sentKeys(calls [][]string) []string {
	var out []string
	for _, c := range calls {
		if len(c) > 0 && c[0] == "send-keys" {
			out = append(out, strings.Join(c, " "))
		}
	}
	return out
}

func injectionTestSession(t *testing.T, session string) {
	t.Helper()
	t.Setenv("BUS_SESSION", session)
	t.Setenv("MUXCODE_LIFECYCLE_LOG_DIR", t.TempDir())
	if err := Init(session, t.TempDir()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(BusDir(session)) })
}

func TestCaptureInjectionTarget_RefusesShellPrompt(t *testing.T) {
	session := "inject-guard-shell"
	injectionTestSession(t, session)
	calls := stubInjectionPane(t, codexShellPane, nil)

	_, err := captureInjectionTarget(session, session+":build.1", "build")
	if !errors.Is(err, ErrInjectionSkipped) {
		t.Fatalf("shell prompt must refuse with ErrInjectionSkipped, got %v", err)
	}
	if keys := sentKeys(*calls); len(keys) != 0 {
		t.Errorf("a refusal must type nothing, sent %v", keys)
	}
	if n := countLifecycleEvents(t, session, "injection-refused"); n != 1 {
		t.Errorf("injection-refused lifecycle rows = %d, want 1", n)
	}
}

// Negative control: a composer is not a shell — the capture comes back and
// nothing is logged.
func TestCaptureInjectionTarget_ComposerProceeds(t *testing.T) {
	session := "inject-guard-ok"
	injectionTestSession(t, session)
	stubInjectionPane(t, codexComposerPane, nil)

	content, err := captureInjectionTarget(session, session+":build.1", "build")
	if err != nil || content != codexComposerPane {
		t.Fatalf("composer must proceed with its capture, got err=%v", err)
	}
	if n := countLifecycleEvents(t, session, "injection-refused"); n != 0 {
		t.Errorf("no refusal expected, lifecycle rows = %d", n)
	}
}

// An unreadable pane is exactly the pane not to type into: the guard refuses
// as a plain failure, not the skip sentinel, so the daemon's receipt-gap
// recovery counts it as its one attempt (TestCheckPollHealth_RecoversOncePerGap)
// instead of re-arming every poll. Force is no exception — the daemon's
// automatic recoveries pass force too — so a forced wake-up refuses as well.
func TestCaptureInjectionTarget_CaptureFailureRefuses(t *testing.T) {
	session := "inject-guard-capfail"
	injectionTestSession(t, session)
	calls := stubInjectionPane(t, "", errors.New("no server running"))

	_, err := captureInjectionTarget(session, session+":build.1", "build")
	if err == nil || errors.Is(err, ErrInjectionSkipped) {
		t.Fatalf("capture failure must refuse as a plain failure, not a skip, got %v", err)
	}
	if keys := sentKeys(*calls); len(keys) != 0 {
		t.Errorf("a refusal must type nothing, sent %v", keys)
	}
	if n := countLifecycleEvents(t, session, "injection-refused"); n != 1 {
		t.Errorf("injection-refused lifecycle rows = %d, want 1", n)
	}

	sendTestRequest(t, session, "build", "MSG-CAPFAIL")
	if err := (&CodexProvider{}).SendWakeUp(session, "build", true); err == nil || errors.Is(err, ErrInjectionSkipped) {
		t.Fatalf("a forced scrape-road wake-up must also refuse an unreadable pane, got %v", err)
	}
	if keys := sentKeys(*calls); len(keys) != 0 {
		t.Errorf("nothing may be typed blind, sent %v", keys)
	}
	if msgs, _ := Peek(session, "build"); len(msgs) != 1 {
		t.Errorf("the refused payload must stay in the inbox, have %d", len(msgs))
	}
}

// The review's bypass: a dead Claude pane keeps the composer it drew before
// dying above the shell prompt that replaced it. A ❯ anywhere must not vouch
// for the pane — only the bottom line decides. Negative controls: a live
// composer as the bottom line, and a live composer over its status bar.
func TestCaptureInjectionTarget_ShellUnderStaleComposerRefused(t *testing.T) {
	session := "inject-guard-stale"
	injectionTestSession(t, session)

	stubInjectionPane(t, "❯ run the build\n⏺ Build passed\n❯ \ndev@host ~/repo (main)\n$ ", nil)
	if _, err := captureInjectionTarget(session, session+":run.1", "run"); !errors.Is(err, ErrInjectionSkipped) {
		t.Fatalf("shell under a stale composer must refuse, got %v", err)
	}

	for _, live := range []string{"❯ run the build\n⏺ Build passed\n❯ ", "❯ \n▶▶ bypass permissions on (shift+tab to cycle)"} {
		stubInjectionPane(t, live, nil)
		if _, err := captureInjectionTarget(session, session+":run.1", "run"); err != nil {
			t.Errorf("live composer %q must proceed, got %v", live, err)
		}
	}
}

func TestCodexAcceptStartup_TrustPromptPressesEnter(t *testing.T) {
	calls := stubInjectionPane(t, "", nil)
	p := &CodexProvider{}

	if p.AcceptStartup("s", "s:build.1", PaneTrustPrompt) {
		t.Error("a trust prompt is not the end of startup")
	}
	if keys := sentKeys(*calls); len(keys) != 1 || keys[0] != "send-keys -t s:build.1 Enter" {
		t.Errorf("trust prompt must be answered with exactly one Enter, sent %v", keys)
	}

	*calls = nil
	if !p.AcceptStartup("s", "s:build.1", PaneIdle) {
		t.Error("PaneIdle ends startup")
	}
	if p.AcceptStartup("s", "s:build.1", PaneNotReady) {
		t.Error("PaneNotReady is not the end of startup")
	}
	if keys := sentKeys(*calls); len(keys) != 0 {
		t.Errorf("no startup prompt, no keys — sent %v", keys)
	}
}

// The hook road is where the incident happened: the fixed wake sentence was
// typed with nothing checked first.
func TestCodexSendWakeUp_HookRoadRefusesShell(t *testing.T) {
	session := "inject-codex-shell"
	injectionTestSession(t, session)
	calls := stubInjectionPane(t, codexShellPane, nil)

	err := (&CodexProvider{hooks: true}).SendWakeUp(session, "build", true)
	if !errors.Is(err, ErrInjectionSkipped) {
		t.Fatalf("shell pane must refuse with ErrInjectionSkipped, got %v", err)
	}
	if keys := sentKeys(*calls); len(keys) != 0 {
		t.Errorf("nothing may be typed into a shell, sent %v", keys)
	}
}

func TestCodexSendWakeUp_HookRoadAnswersTrustPromptAndDefers(t *testing.T) {
	session := "inject-codex-trust"
	injectionTestSession(t, session)
	calls := stubInjectionPane(t, codexTrustPromptPane, nil)

	err := (&CodexProvider{hooks: true}).SendWakeUp(session, "build", true)
	if !errors.Is(err, ErrInjectionSkipped) {
		t.Fatalf("trust prompt must defer with ErrInjectionSkipped, got %v", err)
	}
	keys := sentKeys(*calls)
	if len(keys) != 1 || !strings.HasSuffix(keys[0], " Enter") {
		t.Fatalf("trust prompt must receive exactly one Enter, sent %v", keys)
	}
	if strings.Contains(strings.Join(keys, "\n"), WakeSentence) {
		t.Error("the wake sentence must not be typed into the trust prompt")
	}
	if n := countLifecycleEvents(t, session, "trust-prompt"); n != 1 {
		t.Errorf("trust-prompt auto-accept lifecycle rows = %d, want 1", n)
	}
}

// Scrape road (no hooks): the PAYLOAD is what gets typed, and the refusal
// must leave the message in the inbox for the next cycle.
func TestCodexSendWakeUp_ScrapeRoadRefusesShellAndKeepsInbox(t *testing.T) {
	session := "inject-codex-scrape"
	injectionTestSession(t, session)
	calls := stubInjectionPane(t, codexShellPane, nil)
	sendTestRequest(t, session, "build", "MSG-CODEX-SHELL")

	err := (&CodexProvider{}).SendWakeUp(session, "build", true)
	if !errors.Is(err, ErrInjectionSkipped) {
		t.Fatalf("shell pane must refuse with ErrInjectionSkipped, got %v", err)
	}
	if keys := sentKeys(*calls); len(keys) != 0 {
		t.Errorf("nothing may be typed into a shell, sent %v", keys)
	}
	if msgs, _ := Peek(session, "build"); len(msgs) != 1 {
		t.Errorf("refused message must stay in the inbox, have %d", len(msgs))
	}
}

// The relaunch path end to end: the first wake-up finds the trust prompt and
// answers it; the next finds the composer and injects.
func TestCodexSendWakeUp_TrustPromptThenComposerInjects(t *testing.T) {
	session := "inject-codex-transition"
	injectionTestSession(t, session)
	p := &CodexProvider{hooks: true}

	calls := stubInjectionPane(t, codexTrustPromptPane, nil)
	if err := p.SendWakeUp(session, "build", true); !errors.Is(err, ErrInjectionSkipped) {
		t.Fatalf("first wake-up must answer the prompt and defer, got %v", err)
	}
	if keys := sentKeys(*calls); len(keys) != 1 || !strings.HasSuffix(keys[0], " Enter") {
		t.Fatalf("first wake-up must send exactly one Enter, sent %v", keys)
	}

	calls = stubInjectionPane(t, codexComposerPane, nil)
	if err := p.SendWakeUp(session, "build", true); err != nil {
		t.Fatalf("second wake-up must inject at the composer, got %v", err)
	}
	if keys := sentKeys(*calls); len(keys) != 2 || !strings.Contains(keys[0], "-l -- "+WakeSentence) {
		t.Errorf("second wake-up must type the wake sentence then Enter, sent %v", keys)
	}
}

// Positive control: at the composer the hook road types the sentence, then
// Enter — the guard must not swallow a legitimate wake-up.
func TestCodexSendWakeUp_HookRoadInjectsAtComposer(t *testing.T) {
	session := "inject-codex-ok"
	injectionTestSession(t, session)
	calls := stubInjectionPane(t, codexComposerPane, nil)

	if err := (&CodexProvider{hooks: true}).SendWakeUp(session, "build", true); err != nil {
		t.Fatalf("composer must accept the wake-up, got %v", err)
	}
	keys := sentKeys(*calls)
	if len(keys) != 2 || !strings.Contains(keys[0], "-l -- "+WakeSentence) || !strings.HasSuffix(keys[1], " Enter") {
		t.Errorf("expected the wake sentence then Enter, sent %v", keys)
	}
}

// The scrape road types message CONTENT; into a shell that is command
// execution. The refusal must also leave the message in the inbox.
func TestOpenCodeSendWakeUp_RefusesShellAndKeepsInbox(t *testing.T) {
	session := "inject-opencode-shell"
	injectionTestSession(t, session)
	calls := stubInjectionPane(t, codexShellPane, nil)
	sendTestRequest(t, session, "run", "MSG-SHELL")

	err := (&OpenCodeProvider{}).SendWakeUp(session, "run", true)
	if !errors.Is(err, ErrInjectionSkipped) {
		t.Fatalf("shell pane must refuse with ErrInjectionSkipped, got %v", err)
	}
	if keys := sentKeys(*calls); len(keys) != 0 {
		t.Errorf("nothing may be typed into a shell, sent %v", keys)
	}
	if msgs, _ := Peek(session, "run"); len(msgs) != 1 {
		t.Errorf("refused message must stay in the inbox, have %d", len(msgs))
	}
}

func TestOpenCodeSendWakeUp_InjectsAtComposer(t *testing.T) {
	session := "inject-opencode-ok"
	injectionTestSession(t, session)
	calls := stubInjectionPane(t, "╭──────╮\n│ >    │\n╰──────╯", nil)
	sendTestRequest(t, session, "run", "MSG-OK")

	if err := (&OpenCodeProvider{}).SendWakeUp(session, "run", true); err != nil {
		t.Fatalf("composer must accept the wake-up, got %v", err)
	}
	joined := strings.Join(sentKeys(*calls), "\n")
	if !strings.Contains(joined, "-l -- ") || !strings.Contains(joined, "do the thing") {
		t.Errorf("expected the payload typed literally, sent %q", joined)
	}
}

// Claude's wake path is reached by `deliver --force`, which skips the idle
// gate — the one road that can type into a dead Claude pane.
func TestSendWakeUpWithText_ClaudeRefusesShell(t *testing.T) {
	session := "inject-claude-shell"
	injectionTestSession(t, session)
	calls := stubInjectionPane(t, "dev@host ~/repo (main)\n$ ", nil)

	err := SendWakeUpWithText(session, "run", &ClaudeCodeProvider{}, WakeSentence, true)
	if !errors.Is(err, ErrInjectionSkipped) {
		t.Fatalf("shell pane must refuse with ErrInjectionSkipped, got %v", err)
	}
	if keys := sentKeys(*calls); len(keys) != 0 {
		t.Errorf("nothing may be typed into a shell, sent %v", keys)
	}

	calls = stubInjectionPane(t, "❯ ", nil)
	if err := SendWakeUpWithText(session, "run", &ClaudeCodeProvider{}, WakeSentence, true); err != nil {
		t.Fatalf("idle composer must accept the wake-up, got %v", err)
	}
	joined := strings.Join(sentKeys(*calls), "\n")
	if !strings.Contains(joined, "-l -- "+WakeSentence) || !strings.Contains(joined, " Enter") {
		t.Errorf("expected the sentence then Enter at the composer, sent %q", joined)
	}
}
