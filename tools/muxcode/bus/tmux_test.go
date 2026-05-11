package bus

import (
	"fmt"
	"strings"
	"testing"
)

// capturedArgs collects tmux commands issued during tests.
type capturedArgs struct {
	calls [][]string
}

func (c *capturedArgs) run(args ...string) error {
	c.calls = append(c.calls, args)
	return nil
}

func (c *capturedArgs) output(args ...string) (string, error) {
	c.calls = append(c.calls, args)
	return "", nil
}

func setupTmuxCapture(t *testing.T) *capturedArgs {
	t.Helper()
	cap := &capturedArgs{}
	origRun := tmuxRunner
	origQuiet := tmuxQuietRunner
	origOutput := tmuxOutputRunner
	tmuxRunner = cap.run
	tmuxQuietRunner = cap.run
	tmuxOutputRunner = cap.output
	t.Cleanup(func() {
		tmuxRunner = origRun
		tmuxQuietRunner = origQuiet
		tmuxOutputRunner = origOutput
	})
	return cap
}

func TestTmuxNewSession(t *testing.T) {
	cap := setupTmuxCapture(t)
	TmuxNewSession("test-session", "edit", "/tmp/project", 200, 50)
	if len(cap.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(cap.calls))
	}
	args := cap.calls[0]
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "new-session -d -s test-session -n edit -c /tmp/project -x 200 -y 50") {
		t.Errorf("unexpected args: %v", args)
	}
}

func TestTmuxNewSession_NoSize(t *testing.T) {
	cap := setupTmuxCapture(t)
	TmuxNewSession("s", "w", "/dir", 0, 0)
	args := cap.calls[0]
	for _, a := range args {
		if a == "-x" || a == "-y" {
			t.Errorf("should not have size flags: %v", args)
		}
	}
}

func TestTmuxNewWindow(t *testing.T) {
	cap := setupTmuxCapture(t)
	TmuxNewWindow("s", "build", "/dir")
	args := cap.calls[0]
	expected := []string{"new-window", "-t", "s", "-n", "build", "-c", "/dir"}
	if len(args) != len(expected) {
		t.Fatalf("expected %v, got %v", expected, args)
	}
	for i := range expected {
		if args[i] != expected[i] {
			t.Errorf("arg[%d]: expected %q, got %q", i, expected[i], args[i])
		}
	}
}

func TestTmuxSplitWindow(t *testing.T) {
	cap := setupTmuxCapture(t)
	TmuxSplitWindow("s:edit", "/dir")
	args := cap.calls[0]
	if args[0] != "split-window" || args[1] != "-h" || args[3] != "s:edit" {
		t.Errorf("unexpected args: %v", args)
	}
}

func TestTmuxSendKeys(t *testing.T) {
	cap := setupTmuxCapture(t)
	TmuxSendKeys("s:edit.1", "hello world")
	args := cap.calls[0]
	if args[0] != "send-keys" || args[2] != "s:edit.1" || args[3] != "hello world" {
		t.Errorf("unexpected args: %v", args)
	}
}

func TestTmuxSendEnter(t *testing.T) {
	cap := setupTmuxCapture(t)
	TmuxSendEnter("s:edit.1")
	args := cap.calls[0]
	if args[3] != "Enter" {
		t.Errorf("expected Enter, got %q", args[3])
	}
}

func TestTmuxSetEnv(t *testing.T) {
	cap := setupTmuxCapture(t)
	TmuxSetEnv("s", "BUS_SESSION", "myproject")
	args := cap.calls[0]
	expected := []string{"set-environment", "-t", "s", "BUS_SESSION", "myproject"}
	for i := range expected {
		if args[i] != expected[i] {
			t.Errorf("arg[%d]: expected %q, got %q", i, expected[i], args[i])
		}
	}
}

func TestTmuxSetHook(t *testing.T) {
	cap := setupTmuxCapture(t)
	TmuxSetHook("s", "session-closed", "run-shell 'cleanup s'")
	args := cap.calls[0]
	if args[0] != "set-hook" || args[4] != "run-shell 'cleanup s'" {
		t.Errorf("unexpected args: %v", args)
	}
}

func TestTmuxSelectPane(t *testing.T) {
	cap := setupTmuxCapture(t)
	TmuxSelectPane("s:edit.0")
	args := cap.calls[0]
	if args[0] != "select-pane" || args[2] != "s:edit.0" {
		t.Errorf("unexpected args: %v", args)
	}
}

func TestTmuxSelectWindow(t *testing.T) {
	cap := setupTmuxCapture(t)
	TmuxSelectWindow("s", "build")
	args := cap.calls[0]
	if args[0] != "select-window" || args[2] != "s:build" {
		t.Errorf("unexpected args: %v", args)
	}
}

func TestTmuxSetOption(t *testing.T) {
	cap := setupTmuxCapture(t)
	TmuxSetOption("s", "status-right", "value")
	args := cap.calls[0]
	if args[0] != "set-option" || args[2] != "s" || args[3] != "status-right" || args[4] != "value" {
		t.Errorf("unexpected args: %v", args)
	}
}

func TestTmuxSetGlobalOption(t *testing.T) {
	cap := setupTmuxCapture(t)
	TmuxSetGlobalOption("window-status-format", "val")
	args := cap.calls[0]
	if args[0] != "set-option" || args[1] != "-g" || args[2] != "window-status-format" || args[3] != "val" {
		t.Errorf("unexpected args: %v", args)
	}
}

func TestTmuxKillSession(t *testing.T) {
	cap := setupTmuxCapture(t)
	TmuxKillSession("s")
	args := cap.calls[0]
	if args[0] != "kill-session" || args[2] != "s" {
		t.Errorf("unexpected args: %v", args)
	}
}

func TestTmuxListWindowIndices(t *testing.T) {
	origOutput := tmuxOutputRunner
	tmuxOutputRunner = func(args ...string) (string, error) {
		return "0\n1\n2\n3", nil
	}
	t.Cleanup(func() { tmuxOutputRunner = origOutput })

	indices, err := TmuxListWindowIndices("s")
	if err != nil {
		t.Fatal(err)
	}
	if len(indices) != 4 {
		t.Errorf("expected 4 indices, got %d", len(indices))
	}
}

func TestTmuxUnsetGlobalHook(t *testing.T) {
	cap := setupTmuxCapture(t)
	TmuxUnsetGlobalHook("session-created")
	args := cap.calls[0]
	if args[0] != "set-hook" || args[1] != "-gu" || args[2] != "session-created" {
		t.Errorf("unexpected args: %v", args)
	}
}

func TestTmuxListSessions(t *testing.T) {
	origOutput := tmuxOutputRunner
	tmuxOutputRunner = func(args ...string) (string, error) {
		return "project-a\nproject-b\nproject-c", nil
	}
	t.Cleanup(func() { tmuxOutputRunner = origOutput })

	sessions, err := TmuxListSessions()
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 3 {
		t.Errorf("expected 3 sessions, got %d", len(sessions))
	}
	if sessions[0] != "project-a" || sessions[1] != "project-b" || sessions[2] != "project-c" {
		t.Errorf("unexpected sessions: %v", sessions)
	}
}

func TestTmuxListSessions_Empty(t *testing.T) {
	origOutput := tmuxOutputRunner
	tmuxOutputRunner = func(args ...string) (string, error) {
		return "", nil
	}
	t.Cleanup(func() { tmuxOutputRunner = origOutput })

	sessions, err := TmuxListSessions()
	if err != nil {
		t.Fatal(err)
	}
	if sessions != nil {
		t.Errorf("expected nil sessions, got %v", sessions)
	}
}

func TestQuitSession_MultipleSessions(t *testing.T) {
	// Mock: current session is "project-b", two others exist
	var runCalls [][]string
	origRun := tmuxRunner
	origQuiet := tmuxQuietRunner
	origOutput := tmuxOutputRunner

	tmuxRunner = func(args ...string) error {
		runCalls = append(runCalls, args)
		return nil
	}
	tmuxQuietRunner = func(args ...string) error {
		runCalls = append(runCalls, args)
		return nil
	}
	tmuxOutputRunner = func(args ...string) (string, error) {
		if args[0] == "display-message" {
			return "project-b", nil
		}
		if args[0] == "list-sessions" {
			return "project-a\nproject-b\nproject-c", nil
		}
		return "", nil
	}
	t.Cleanup(func() {
		tmuxRunner = origRun
		tmuxQuietRunner = origQuiet
		tmuxOutputRunner = origOutput
	})

	if err := QuitSession(); err != nil {
		t.Fatal(err)
	}

	// Should have: switch-client -l, then kill-session -t project-b
	if len(runCalls) < 2 {
		t.Fatalf("expected at least 2 run calls, got %d: %v", len(runCalls), runCalls)
	}

	// First call: switch-client -l
	first := runCalls[0]
	if first[0] != "switch-client" || first[1] != "-l" {
		t.Errorf("expected switch-client -l, got %v", first)
	}

	// Second call: kill-session -t project-b
	second := runCalls[1]
	if second[0] != "kill-session" || second[2] != "project-b" {
		t.Errorf("expected kill-session -t project-b, got %v", second)
	}
}

func TestTmuxIsWindowActive_Active(t *testing.T) {
	origOutput := tmuxOutputRunner
	tmuxOutputRunner = func(args ...string) (string, error) {
		// Verify correct target format
		joined := strings.Join(args, " ")
		if !strings.Contains(joined, "display-message -t myproject:commit -p #{window_active}") {
			t.Errorf("unexpected args: %v", args)
		}
		return "1", nil
	}
	t.Cleanup(func() { tmuxOutputRunner = origOutput })

	if !TmuxIsWindowActive("myproject", "commit") {
		t.Error("should return true when window_active is 1")
	}
}

func TestTmuxIsWindowActive_Inactive(t *testing.T) {
	origOutput := tmuxOutputRunner
	tmuxOutputRunner = func(args ...string) (string, error) {
		return "0", nil
	}
	t.Cleanup(func() { tmuxOutputRunner = origOutput })

	if TmuxIsWindowActive("myproject", "commit") {
		t.Error("should return false when window_active is 0")
	}
}

func TestTmuxIsWindowActive_Error(t *testing.T) {
	origOutput := tmuxOutputRunner
	tmuxOutputRunner = func(args ...string) (string, error) {
		return "", fmt.Errorf("no tmux server")
	}
	t.Cleanup(func() { tmuxOutputRunner = origOutput })

	if TmuxIsWindowActive("myproject", "commit") {
		t.Error("should return false on error")
	}
}

func TestTmuxClearInput(t *testing.T) {
	cap := setupTmuxCapture(t)
	TmuxClearInput("s:commit.1")
	if len(cap.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(cap.calls))
	}
	args := cap.calls[0]
	if args[0] != "send-keys" || args[2] != "s:commit.1" || args[3] != "C-u" {
		t.Errorf("unexpected args: %v", args)
	}
}

func TestQuitSession_OnlySession(t *testing.T) {
	// Mock: current session is "project-a", no others
	var runCalls [][]string
	origRun := tmuxRunner
	origQuiet := tmuxQuietRunner
	origOutput := tmuxOutputRunner

	tmuxRunner = func(args ...string) error {
		runCalls = append(runCalls, args)
		return nil
	}
	tmuxQuietRunner = func(args ...string) error {
		runCalls = append(runCalls, args)
		return nil
	}
	tmuxOutputRunner = func(args ...string) (string, error) {
		if args[0] == "display-message" {
			return "project-a", nil
		}
		if args[0] == "list-sessions" {
			return "project-a", nil
		}
		return "", nil
	}
	t.Cleanup(func() {
		tmuxRunner = origRun
		tmuxQuietRunner = origQuiet
		tmuxOutputRunner = origOutput
	})

	if err := QuitSession(); err != nil {
		t.Fatal(err)
	}

	// Should detach with -E to kill after
	if len(runCalls) != 1 {
		t.Fatalf("expected 1 run call, got %d: %v", len(runCalls), runCalls)
	}

	call := runCalls[0]
	if call[0] != "detach-client" || call[1] != "-E" {
		t.Errorf("expected detach-client -E, got %v", call)
	}
	if !strings.Contains(call[2], "kill-session") || !strings.Contains(call[2], "project-a") {
		t.Errorf("expected -E command to kill project-a, got %q", call[2])
	}
}
