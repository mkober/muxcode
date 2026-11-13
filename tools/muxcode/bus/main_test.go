package bus

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMain redirects lifecycle logging into a temp dir for the whole package.
//
// Lifecycle logging is a side effect of a great many code paths under test, and
// it writes one persistent file per session name into ~/.config/muxcode/logs.
// Tests use synthetic session names ("test-cron-exec", "test-<random>"), so a
// full run deposits thousands of stray log files in the user's real install —
// 41,789 of them had accumulated before this redirect existed.
//
// CODEX_HOME is redirected for the same reason: a Codex launch path records
// its working directory as trusted in $CODEX_HOME/config.toml
// (EnsureCodexProjectTrust), which must never be the user's real one.
//
// XDG_STATE_HOME is redirected so a test that resolves a bus directory without
// SetBusDirBase lands in this temp root, never the real ~/.local/state/muxcode
// (MUX-208) — the same leak MUX-137 found against /tmp.
//
// The gate authority reads fixed paths (the cwd's .muxcode/config and the real
// home config), so it is pinned to none: a user's own
// MUXCODE_GATE_AUTHORITY_ROLES grant would otherwise flip every test that
// expects an agent refused. Tests needing a value use pinGateAuthorityConfig.
func TestMain(m *testing.M) {
	gateAuthorityConfigPaths = func() []string { return nil }
	dir, err := os.MkdirTemp("", "muxcode-lifecycle-test-")
	if err != nil {
		panic(err)
	}
	os.Setenv("MUXCODE_LIFECYCLE_LOG_DIR", dir)
	os.Setenv("CODEX_HOME", filepath.Join(dir, "codex-home"))
	os.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state"))

	code := m.Run()

	// Explicit cleanup: os.Exit does not run deferred functions.
	os.RemoveAll(dir)
	os.Exit(code)
}
