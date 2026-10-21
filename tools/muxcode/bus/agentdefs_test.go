package bus

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func TestReadAgentDefHash_RoundtripAndEmpty(t *testing.T) {
	base := t.TempDir()
	SetBusDirBase(base)
	defer ResetBusDirBase()

	session := "tdef"
	role := "build"
	if err := os.MkdirAll(BusDir(session), 0o755); err != nil {
		t.Fatal(err)
	}

	// No stamp yet → empty.
	if got := ReadAgentDefHash(session, role); got != "" {
		t.Fatalf("ReadAgentDefHash before stamp: got %q, want empty", got)
	}

	// Write a stamp via the path helper and read it back.
	want := "deadbeefcafe"
	if err := os.WriteFile(agentDefHashPath(session, role), []byte(want), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := ReadAgentDefHash(session, role); got != want {
		t.Fatalf("ReadAgentDefHash roundtrip: got %q, want %q", got, want)
	}
}

func TestAgentDefHash_Deterministic(t *testing.T) {
	// AgentDefHash may be empty if no definition resolves in this environment;
	// when present it must be a stable 64-char hex string.
	h1 := AgentDefHash("build")
	h2 := AgentDefHash("build")
	if h1 != h2 {
		t.Fatalf("AgentDefHash not deterministic: %q vs %q", h1, h2)
	}
	if h1 != "" && len(h1) != 64 {
		t.Fatalf("AgentDefHash length: got %d, want 64 (hash=%q)", len(h1), h1)
	}
}

func TestResolvedAgentFileForRole_UnknownRole(t *testing.T) {
	if got := ResolvedAgentFileForRole("nonexistent-role"); got != "" {
		t.Fatalf("ResolvedAgentFileForRole(unknown): got %q, want empty", got)
	}
}

func TestStampAgentDefHash_NoDefIsNoop(t *testing.T) {
	base := t.TempDir()
	SetBusDirBase(base)
	defer ResetBusDirBase()

	session := "tdef2"
	role := "nonexistent-role" // AgentDefHash → "" so stamp is a no-op
	if err := os.MkdirAll(BusDir(session), 0o755); err != nil {
		t.Fatal(err)
	}

	StampAgentDefHash(session, role)
	if got := ReadAgentDefHash(session, role); got != "" {
		t.Fatalf("StampAgentDefHash for unknown role wrote %q, want no stamp", got)
	}
}

const codexIdlePane = "• Build succeeded. EXIT=0\n\n› Ask Codex to do anything\n"

// fakeReloadPane answers capture-pane with content and err at the tmux boundary.
func fakeReloadPane(t *testing.T, content string, err error) {
	t.Helper()
	orig := tmuxOutputRunner
	tmuxOutputRunner = func(args ...string) (string, error) {
		if len(args) > 0 && args[0] == "capture-pane" {
			return content, err
		}
		return orig(args...)
	}
	t.Cleanup(func() { tmuxOutputRunner = orig })
}

// Codex's IsIdle is constantly false, so gating on it deferred every Codex
// reload forever: an idle pane must read safe. Everything else must not — a
// failed capture says nothing, and an approval prompt parks a live turn with no
// working indicator.
func TestAutoReloadSafe_CodexPaneStates(t *testing.T) {
	cases := []struct {
		name, pane, reason string
		err                error
	}{
		{name: "idle", pane: codexIdlePane},
		{name: "working", pane: "• Working (12s • esc to interrupt)\n", reason: "agent working"},
		{name: "approval prompt", pane: codexApprovalPromptPane, reason: "prompt awaiting input"},
		{name: "capture failed", err: errors.New("no such pane"), reason: "pane unreadable"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			session := sandboxTestEnv(t)
			t.Setenv("MUXCODE_BUILD_CLI", "codex")
			fakeReloadPane(t, c.pane, c.err)
			ok, reason := AutoReloadSafe(session, "build")
			if ok != (c.reason == "") || !strings.Contains(reason, c.reason) {
				t.Errorf("AutoReloadSafe = %v %q, want safe=%v reason %q", ok, reason, c.reason == "", c.reason)
			}
		})
	}
}

func TestAutoReloadSafe_PendingWorkBlocks(t *testing.T) {
	cases := map[string]func(session string) error{
		"pending request": func(s string) error {
			return Send(s, NewMessage("edit", "build", "request", "build", "go", ""))
		},
		"in-flight task": func(s string) error {
			return CreateTask(s, NewMessage("edit", "build", "request", "build", "go", ""), 600)
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			session := sandboxTestEnv(t)
			t.Setenv("MUXCODE_BUILD_CLI", "codex")
			fakeReloadPane(t, codexIdlePane, nil)
			if err := setup(session); err != nil {
				t.Fatalf("setup: %v", err)
			}
			if ok, _ := AutoReloadSafe(session, "build"); ok {
				t.Errorf("%s did not block the reload", name)
			}
		})
	}
}
