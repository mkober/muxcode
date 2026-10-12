package daemon

import (
	"fmt"
	"os"
	"testing"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// TestMain redirects lifecycle logging into a temp dir for the whole package,
// and confines the mass-exit scan to the test's own session so no test reads
// the exit logs of live sessions on the machine. See the equivalent in package
// bus for why the lifecycle redirect is required.
//
// BUS_SESSION is pinned to a session no live bus uses: run from an agent pane
// the suite inherits the real one, and ResolveProviderCLI ranks that session's
// runtime overrides above the per-role env a test sets. On 2026-10-12 a live
// review=codex and commit/run=claude override failed
// TestCodexApprovalRoleEligible_ExcludesNonCodexProviders and
// TestCheckAgentHealth_NonClaudeDeathsNotCorrelated. A test needing its own
// session still sets it with t.Setenv.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "muxcode-lifecycle-test-")
	if err != nil {
		panic(err)
	}
	os.Setenv("MUXCODE_LIFECYCLE_LOG_DIR", dir)
	os.Setenv("BUS_SESSION", fmt.Sprintf("daemon-test-isolated-%d", os.Getpid()))
	defaultRecentAgentExits = func(session string, now, window int64) ([]bus.ExitSighting, error) {
		return bus.AgentExitsIn([]string{session}, now, window), nil
	}

	code := m.Run()

	os.RemoveAll(dir)
	os.Exit(code)
}
