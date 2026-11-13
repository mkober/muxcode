package bus

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestBusBaseEnvPairs pins which bus-base inputs reach the tmux session
// environment: BusDir recomputes the directory from MUXCODE_BUS_BASE and
// XDG_STATE_HOME, so MUXCODE_BUS_DIR alone left later panes on another bus.
func TestBusBaseEnvPairs(t *testing.T) {
	base, xdg := t.TempDir(), t.TempDir()
	t.Setenv(BusDirBaseEnv, base)
	t.Setenv("XDG_STATE_HOME", xdg)
	got := busBaseEnvPairs()
	want := [][2]string{{BusDirBaseEnv, base}, {"XDG_STATE_HOME", xdg}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("busBaseEnvPairs() = %v, want %v", got, want)
	}

	// Negative control: unset and relative values are never exported.
	t.Setenv(BusDirBaseEnv, "")
	t.Setenv("XDG_STATE_HOME", "relative/state")
	if got := busBaseEnvPairs(); len(got) != 0 {
		t.Errorf("busBaseEnvPairs() = %v, want none for unset/relative values", got)
	}
}

// TestMigrateRepointsProcLogPaths: proc.jsonl entries store absolute log paths
// under the legacy directory; after the move they must name the new one, and
// entries outside the legacy directory stay as they were.
func TestMigrateRepointsProcLogPaths(t *testing.T) {
	t.Run("rename", func(t *testing.T) { migrateRepointsProcLogPaths(t, false) })
	t.Run("cross-device copy", func(t *testing.T) { migrateRepointsProcLogPaths(t, true) })
}

// TestMigrateFailedRenameRestoresProcLogPaths: the rewrite happens before the
// rename, so a rename that fails must put the source's paths back.
func TestMigrateFailedRenameRestoresProcLogPaths(t *testing.T) {
	t.Setenv("MUXCODE_LIFECYCLE_LOG_DIR", t.TempDir())
	saved := renameDir
	renameDir = func(a, b string) error { return os.ErrPermission }
	t.Cleanup(func() { renameDir = saved })
	oldDir := filepath.Join(t.TempDir(), "muxcode-bus-restore")
	entry := ProcEntry{ID: "proc-1", LogFile: filepath.Join(oldDir, "proc", "proc-1.log")}
	b, _ := json.Marshal(entry)
	writeFile(t, filepath.Join(oldDir, "proc.jsonl"), string(b)+"\n")

	if _, err := migrateBusDirAt("restore", oldDir, filepath.Join(t.TempDir(), "bus")); err == nil {
		t.Fatal("migration succeeded despite a failing rename")
	}
	data, err := os.ReadFile(filepath.Join(oldDir, "proc.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var got ProcEntry
	if err := json.Unmarshal(data, &got); err != nil || got.LogFile != entry.LogFile {
		t.Errorf("source LogFile = %q (%v), want %q restored", got.LogFile, err, entry.LogFile)
	}
}

func migrateRepointsProcLogPaths(t *testing.T, crossDevice bool) {
	if crossDevice {
		saved := renameDir
		renameDir = exdevRename
		t.Cleanup(func() { renameDir = saved })
	}
	t.Setenv("MUXCODE_LIFECYCLE_LOG_DIR", t.TempDir())
	oldDir := filepath.Join(t.TempDir(), "muxcode-bus-procs")
	base := filepath.Join(t.TempDir(), "bus")
	foreign := "/elsewhere/proc-x.log"
	entries := []ProcEntry{
		{ID: "proc-1", Command: "sleep 1", Status: "running", LogFile: filepath.Join(oldDir, "proc", "proc-1.log")},
		{ID: "proc-2", Command: "true", Status: "done", LogFile: foreign},
	}
	var lines string
	for _, e := range entries {
		b, _ := json.Marshal(e)
		lines += string(b) + "\n"
	}
	writeFile(t, filepath.Join(oldDir, "proc.jsonl"), lines)
	writeFile(t, filepath.Join(oldDir, "proc", "proc-1.log"), "out\n")

	if got, err := migrateBusDirAt("procs", oldDir, base); err != nil || got != BusDirMigrated {
		t.Fatalf("migration = %v, %v", got, err)
	}
	newDir := filepath.Join(base, "muxcode-bus-procs")
	data, err := os.ReadFile(filepath.Join(newDir, "proc.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var got []ProcEntry
	for _, l := range splitLines(string(data)) {
		var e ProcEntry
		if err := json.Unmarshal([]byte(l), &e); err != nil {
			t.Fatalf("proc.jsonl line %q: %v", l, err)
		}
		got = append(got, e)
	}
	if len(got) != 2 {
		t.Fatalf("entries = %d, want 2", len(got))
	}
	if want := filepath.Join(newDir, "proc", "proc-1.log"); got[0].LogFile != want {
		t.Errorf("LogFile = %q, want %q", got[0].LogFile, want)
	}
	if _, err := os.Stat(got[0].LogFile); err != nil {
		t.Errorf("repointed log does not exist: %v", err)
	}
	if got[1].LogFile != foreign {
		t.Errorf("foreign LogFile rewritten to %q", got[1].LogFile)
	}
	if left, _ := filepath.Glob(filepath.Join(newDir, "proc.jsonl.migrating-*")); len(left) != 0 {
		t.Errorf("temp file left behind: %v", left)
	}
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			if i > start {
				out = append(out, s[start:i])
			}
			start = i + 1
		}
	}
	return out
}
