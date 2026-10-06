package daemon

import (
	"os"
	"testing"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// TestMain redirects lifecycle logging into a temp dir for the whole package,
// and confines the mass-exit scan to the test's own session so no test reads
// the exit logs of live sessions on the machine. See the equivalent in package
// bus for why the lifecycle redirect is required.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "muxcode-lifecycle-test-")
	if err != nil {
		panic(err)
	}
	os.Setenv("MUXCODE_LIFECYCLE_LOG_DIR", dir)
	defaultRecentAgentExits = func(session string, now, window int64) ([]bus.ExitSighting, error) {
		return bus.AgentExitsIn([]string{session}, now, window), nil
	}

	code := m.Run()

	os.RemoveAll(dir)
	os.Exit(code)
}
