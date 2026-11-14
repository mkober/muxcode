package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// TestMain redirects lifecycle logging, the state root (bus directories) and
// global memory into a temp dir for the whole package. See the equivalent in
// package bus for why this is required.
//
// No test here logs or reads memory today, so nothing currently leaks. The
// pins are present so that stays true by construction: the leak returns
// silently the moment a tui test touches a code path that logs or calls
// bus.Init, and the failure would land in the user's real install rather than
// in this package.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "muxcode-lifecycle-test-")
	if err != nil {
		panic(err)
	}
	os.Setenv("MUXCODE_LIFECYCLE_LOG_DIR", dir)
	os.Setenv("XDG_STATE_HOME", dir+"/state")
	os.Setenv(bus.GlobalMemoryDirEnv, filepath.Join(dir, "global-memory"))

	code := m.Run()

	os.RemoveAll(dir)
	os.Exit(code)
}

// TestMainIsolatesGlobalMemory is the negative control for the global memory
// pin above (MUX-224): unpinned, GlobalMemoryDir is the real
// ~/.config/muxcode/memory and this fails.
func TestMainIsolatesGlobalMemory(t *testing.T) {
	root := filepath.Clean(os.TempDir()) + string(os.PathSeparator)
	if p := bus.GlobalMemoryDir(); !strings.HasPrefix(p, root) {
		t.Errorf("global memory %q is outside the run's temp root %s — TestMain's pin is missing", p, root)
	}
}
