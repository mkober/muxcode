package bus

import (
	"strings"
	"testing"
)

// stubTmuxRunner captures every TmuxRun argv and restores the original.
func stubTmuxRunner(t *testing.T) *[][]string {
	t.Helper()
	orig := tmuxRunner
	var calls [][]string
	tmuxRunner = func(args ...string) error {
		calls = append(calls, args)
		return nil
	}
	t.Cleanup(func() { tmuxRunner = orig })
	return &calls
}

// TmuxSendLiteral is the single argv producer for every dynamic-payload
// injection (notify, opencode, codex) — these tests are argv-level
// because the MUX-104 failure is in tmux's argument parsing: a mocked
// runner that only checked the payload string would pass while broken.
func TestTmuxSendLiteral_ArgvShape(t *testing.T) {
	calls := stubTmuxRunner(t)

	if err := TmuxSendLiteral("sess:build.1", "- bullet payload"); err != nil {
		t.Fatalf("TmuxSendLiteral: %v", err)
	}
	if len(*calls) != 1 {
		t.Fatalf("expected 1 tmux call, got %d", len(*calls))
	}
	got := (*calls)[0]
	want := []string{"send-keys", "-t", "sess:build.1", "-l", "--", "- bullet payload"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("argv = %q, want %q", got, want)
	}
}

// A payload beginning with one or two dashes must arrive verbatim as the
// argument after the -- separator — not stripped, not truncated.
func TestTmuxSendLiteral_DashPayloadsIntact(t *testing.T) {
	payloads := []string{
		"- Build succeeded: ./build.sh exit 0",
		"--render-once is the flag to use",
		"-l looks like a flag but is content",
	}
	for _, p := range payloads {
		calls := stubTmuxRunner(t)
		if err := TmuxSendLiteral("t", p); err != nil {
			t.Fatalf("TmuxSendLiteral(%q): %v", p, err)
		}
		got := (*calls)[0]
		if got[len(got)-1] != p {
			t.Errorf("payload mangled: got %q, want %q", got[len(got)-1], p)
		}
		if got[len(got)-2] != "--" {
			t.Errorf("payload %q not preceded by --: %q", p, got)
		}
	}
}

// Multi-line and unicode payloads pass through unchanged.
func TestTmuxSendLiteral_MultilineAndUnicode(t *testing.T) {
	payload := "line one\n- line two ✓ ⚑ ↺\nline three"
	calls := stubTmuxRunner(t)
	if err := TmuxSendLiteral("t", payload); err != nil {
		t.Fatalf("TmuxSendLiteral: %v", err)
	}
	got := (*calls)[0]
	if got[len(got)-1] != payload {
		t.Errorf("payload changed: got %q", got[len(got)-1])
	}
}
