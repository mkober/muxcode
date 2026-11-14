package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// TestMain redirects lifecycle logging, CODEX_HOME, the state root (bus
// directories) and session memory into a temp dir for the whole package. See
// the equivalent in package bus for why this is required.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "muxcode-lifecycle-test-")
	if err != nil {
		panic(err)
	}
	os.Setenv("MUXCODE_LIFECYCLE_LOG_DIR", dir)
	os.Setenv("CODEX_HOME", filepath.Join(dir, "codex-home"))
	os.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state"))
	os.Setenv("BUS_MEMORY_DIR", filepath.Join(dir, "memory"))

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
