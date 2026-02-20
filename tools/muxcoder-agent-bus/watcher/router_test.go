package watcher

import (
	"fmt"
	"math/rand"
	"testing"

	"github.com/mkober/muxcoder/tools/muxcoder-agent-bus/bus"
)

func testSession(t *testing.T) string {
	t.Helper()
	session := fmt.Sprintf("test-router-%d", rand.Int())
	if err := bus.Init(session, t.TempDir()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { _ = bus.Cleanup(session) })
	return session
}

func TestRouteFile_TestFile(t *testing.T) {
	session := testSession(t)
	RouteFile(session, "src/foo.test.ts")

	msgs, err := bus.Receive(session, "test")
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("test inbox got %d messages, want 1", len(msgs))
	}
	if msgs[0].To != "test" {
		t.Errorf("To = %q, want %q", msgs[0].To, "test")
	}
}

func TestRouteFile_SpecFile(t *testing.T) {
	session := testSession(t)
	RouteFile(session, "src/foo.spec.js")

	msgs, err := bus.Receive(session, "test")
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("test inbox got %d messages, want 1", len(msgs))
	}
}

func TestRouteFile_CdkFile(t *testing.T) {
	session := testSession(t)
	RouteFile(session, "lib/constructs/my-stack.ts")

	msgs, err := bus.Receive(session, "deploy")
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("deploy inbox got %d messages, want 1", len(msgs))
	}
	if msgs[0].To != "deploy" {
		t.Errorf("To = %q, want %q", msgs[0].To, "deploy")
	}
}

func TestRouteFile_SourceTS(t *testing.T) {
	session := testSession(t)
	RouteFile(session, "src/handler.ts")

	msgs, err := bus.Receive(session, "build")
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("build inbox got %d messages, want 1", len(msgs))
	}
	if msgs[0].To != "build" {
		t.Errorf("To = %q, want %q", msgs[0].To, "build")
	}
}

func TestRouteFile_SourcePy(t *testing.T) {
	session := testSession(t)
	RouteFile(session, "resources/lambda/main.py")

	msgs, err := bus.Receive(session, "build")
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("build inbox got %d messages, want 1", len(msgs))
	}
}

func TestRouteFile_Unmatched(t *testing.T) {
	session := testSession(t)
	RouteFile(session, "README.md")

	// Check that no agent received a message
	for _, role := range bus.KnownRoles {
		if bus.HasMessages(session, role) {
			t.Errorf("unexpected message in %s inbox for README.md", role)
		}
	}
}
