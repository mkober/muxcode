package cmd

import (
	"os"
	"testing"
)

// TestMain redirects lifecycle logging into a temp dir for the whole package.
// See the equivalent in package bus for why this is required.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "muxcode-lifecycle-test-")
	if err != nil {
		panic(err)
	}
	os.Setenv("MUXCODE_LIFECYCLE_LOG_DIR", dir)

	code := m.Run()

	os.RemoveAll(dir)
	os.Exit(code)
}
