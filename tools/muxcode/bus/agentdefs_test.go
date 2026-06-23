package bus

import (
	"os"
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
