package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMain redirects lifecycle logging, CODEX_HOME and the state root (bus
// directories) into a temp dir for the whole package. See the equivalent in
// package bus for why this is required.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "muxcode-lifecycle-test-")
	if err != nil {
		panic(err)
	}
	os.Setenv("MUXCODE_LIFECYCLE_LOG_DIR", dir)
	os.Setenv("CODEX_HOME", filepath.Join(dir, "codex-home"))
	os.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state"))

	code := m.Run()

	os.RemoveAll(dir)
	os.Exit(code)
}
