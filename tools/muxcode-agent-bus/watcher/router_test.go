package watcher

import (
	"fmt"
	"math/rand"
	"testing"

	"github.com/mkober/muxcode/tools/muxcode-agent-bus/bus"
)

// testSession creates an isolated bus session for watcher tests.
func testSession(t *testing.T) string {
	t.Helper()
	session := fmt.Sprintf("test-router-%d", rand.Int())
	if err := bus.Init(session, t.TempDir()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { _ = bus.Cleanup(session) })
	return session
}
