package tui

import (
	"os"
	"testing"
)

// TestMain redirects lifecycle logging into a temp dir for the whole package.
// See the equivalent in package bus for why this is required.
//
// No test here logs today, so nothing currently leaks. The pin is present so
// that stays true by construction: the leak returns silently the moment a tui
// test touches a code path that logs, and the failure would land in the user's
// real install rather than in this package.
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
