package daemon

import (
	"fmt"
	"math/rand"
	"testing"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// testSession creates an isolated bus session for daemon tests.
func testSession(t *testing.T) string {
	t.Helper()
	t.Setenv("MUXCODE_LIFECYCLE_LOG_DIR", t.TempDir())
	session := fmt.Sprintf("test-daemon-%d", rand.Int())
	if err := bus.Init(session, t.TempDir()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { _ = bus.Cleanup(session) })
	return session
}
