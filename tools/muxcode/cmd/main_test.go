package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMain redirects lifecycle logging and CODEX_HOME into a temp dir for the
// whole package. See the equivalent in package bus for why this is required.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "muxcode-lifecycle-test-")
	if err != nil {
		panic(err)
	}
	os.Setenv("MUXCODE_LIFECYCLE_LOG_DIR", dir)
	os.Setenv("CODEX_HOME", filepath.Join(dir, "codex-home"))

	code := m.Run()

	os.RemoveAll(dir)
	os.Exit(code)
}
