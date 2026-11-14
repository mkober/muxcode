package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// TestMain redirects lifecycle logging, CODEX_HOME, the state root (bus
// directories) and session memory into a temp dir for the whole package, and confines the mass-exit scan to the test's own session so
// no test reads the exit logs of live sessions on the machine. See the
// equivalent in package bus for why both redirects are required.
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
	os.Setenv("CODEX_HOME", filepath.Join(dir, "codex-home"))
	os.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state"))
	os.Setenv("BUS_MEMORY_DIR", filepath.Join(dir, "memory"))
	defaultRecentAgentExits = func(session string, now, window int64) ([]bus.ExitSighting, error) {
		return bus.AgentExitsIn([]string{session}, now, window), nil
	}

	code := m.Run()

	os.RemoveAll(dir)
	os.Exit(code)
}

// TestMainIsolatesBusAndMemory is the negative control for the pins above
// (MUX-137): a test that sets nothing resolves its bus and its memory into
// this run's temp root. Removing either pin fails it.
func TestMainIsolatesBusAndMemory(t *testing.T) {
	t.Setenv(bus.BusDirBaseEnv, "")
	root := filepath.Clean(os.TempDir()) + string(os.PathSeparator)
	for name, p := range map[string]string{"bus base": bus.BusDirBase(), "memory": bus.MemoryDir()} {
		if !strings.HasPrefix(p, root) {
			t.Errorf("%s %q is outside the run's temp root %s — TestMain's pin is missing", name, p, root)
		}
	}
}
