package bus

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func memEntry(section, ts, body string) string {
	return "\n## " + section + "\n_" + ts + "_\n\n" + body + "\n"
}

func writeMemFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

// MUX-223 Phase 2: stranded `.muxcode/memory` directories below the root are
// found — nested ones too — while the root's own memory, its keep directory
// and `.git` are never reported (negative control).
func TestFindStrandedMemory(t *testing.T) {
	root := t.TempDir()
	memDir := filepath.Join(root, ".muxcode", "memory")
	writeMemFile(t, filepath.Join(memDir, "edit.md"), memEntry("Root", "2026-11-13 10:00", "root"))
	writeMemFile(t, filepath.Join(root, ".muxcode", "stranded-memory", "x", ".muxcode", "memory", "edit.md"), "kept")
	writeMemFile(t, filepath.Join(root, ".git", ".muxcode", "memory", "edit.md"), "git internals")
	writeMemFile(t, filepath.Join(root, "tools", "muxcode", ".muxcode", "memory", "edit.md"), "a")
	writeMemFile(t, filepath.Join(root, "tools", "muxcode", ".muxcode", "memory", "edit", "2026-11-12.md"), "b")
	writeMemFile(t, filepath.Join(root, "tools", "muxcode", "bus", ".muxcode", "memory", "edit.md"), "c")

	got, err := FindStrandedMemory(root, memDir)
	if err != nil {
		t.Fatal(err)
	}
	var rels []string
	for _, s := range got {
		rels = append(rels, s.Rel+"="+strings.Join(s.Files, ","))
	}
	want := []string{
		"tools/muxcode/.muxcode/memory=edit/2026-11-12.md,edit.md", // WalkDir's lexical order: "edit" < "edit.md"
		"tools/muxcode/bus/.muxcode/memory=edit.md",
	}
	if strings.Join(rels, " ") != strings.Join(want, " ") {
		t.Errorf("stranded = %v, want %v", rels, want)
	}
}

// MUX-223 Decision 2: a stranded file is merged entry by entry — sections in
// time order, an exact duplicate skipped, a heading inside a body kept with
// its entry — after the root file is backed up byte for byte; a file the root
// lacks is created; the original directory is moved intact, never deleted; a
// second scan finds nothing to merge.
func TestMergeStrandedMemory(t *testing.T) {
	root := t.TempDir()
	memDir := filepath.Join(root, ".muxcode", "memory")
	rootEdit := memEntry("Session Summary", "2026-11-13 09:00", "ROOT-A") +
		memEntry("Session Summary", "2026-11-13 16:00", "SHARED-DUP")
	writeMemFile(t, filepath.Join(memDir, "edit.md"), rootEdit)

	stranded := filepath.Join(root, "tools", "muxcode", "bus", ".muxcode", "memory")
	strandedEdit := memEntry("Session Summary", "2026-11-13 17:03", "STRANDED-LATE\n## Heading inside the body\nmore") +
		memEntry("Session Summary", "2026-11-13 16:00", "SHARED-DUP") +
		memEntry("Session Summary", "2026-11-13 08:00", "STRANDED-EARLY")
	writeMemFile(t, filepath.Join(stranded, "edit.md"), strandedEdit)
	writeMemFile(t, filepath.Join(stranded, "edit", "2026-11-12.md"), memEntry("Old", "2026-11-12 12:00", "ARCHIVED"))

	found, err := FindStrandedMemory(root, memDir)
	if err != nil || len(found) != 1 {
		t.Fatalf("found %+v, %v", found, err)
	}
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	results, err := MergeStrandedMemory(memDir, found, now)
	if err != nil {
		t.Fatal(err)
	}

	merged, _ := os.ReadFile(filepath.Join(memDir, "edit.md"))
	order := []string{"STRANDED-EARLY", "ROOT-A", "SHARED-DUP", "STRANDED-LATE", "Heading inside the body"}
	last := -1
	for _, s := range order {
		i := strings.Index(string(merged), s)
		if i <= last {
			t.Fatalf("merged edit.md out of order at %q:\n%s", s, merged)
		}
		last = i
	}
	if strings.Count(string(merged), "SHARED-DUP") != 1 {
		t.Errorf("the duplicate entry was merged twice:\n%s", merged)
	}
	if got := ParseMemoryEntries(string(merged), "edit"); len(got) < 4 {
		t.Errorf("merged file parses to %d entries, want the four distinct ones", len(got))
	}
	if a, _ := os.ReadFile(filepath.Join(memDir, "edit", "2026-11-12.md")); !strings.Contains(string(a), "ARCHIVED") {
		t.Errorf("the stranded archive was not merged into the root's archive: %q", a)
	}

	fm := map[string]FileMerge{}
	for _, f := range results[0].Files {
		fm[f.File] = f
	}
	if e := fm["edit.md"]; e.Added != 2 || e.Duplicates != 1 || e.Backup == "" {
		t.Errorf("edit.md merge = %+v, want +2, 1 duplicate, a backup", e)
	}
	if b, _ := os.ReadFile(fm["edit.md"].Backup); string(b) != rootEdit {
		t.Errorf("backup %q differs from the root file before the merge", b)
	}
	if fm["edit/2026-11-12.md"].Backup != "" {
		t.Error("a file the root lacked was given a backup")
	}

	kept := filepath.Join(StrandedMemoryKeepDir(memDir), "tools", "muxcode", "bus", ".muxcode", "memory")
	if results[0].KeptAt != kept {
		t.Errorf("kept at %q, want %q", results[0].KeptAt, kept)
	}
	if b, _ := os.ReadFile(filepath.Join(kept, "edit.md")); string(b) != strandedEdit {
		t.Errorf("the kept original was altered: %q", b)
	}
	if _, err := os.Stat(stranded); !os.IsNotExist(err) {
		t.Errorf("the stranded directory is still in place: %v", err)
	}
	if again, err := FindStrandedMemory(root, memDir); err != nil || len(again) != 0 {
		t.Errorf("after the merge the scan found %+v, %v; want none", again, err)
	}
	t.Setenv(MemoryDirEnv, memDir)
	if roles, _ := ListMemoryRoles(); strings.Contains(strings.Join(roles, ","), "stranded") {
		t.Errorf("the keep directory reads as a memory role: %v", roles)
	}
}

// Two stranded directories feeding the same root file in one run: the backup
// is the root file before the run, not the first merge's output — on
// 2026-11-17 this repository's edit.md backup held stranded entries the root
// never had. Both directories' entries land.
func TestMergeStrandedMemory_BackupIsThePreRunFile(t *testing.T) {
	root := t.TempDir()
	memDir := filepath.Join(root, ".muxcode", "memory")
	orig := memEntry("Session Summary", "2026-11-13 09:00", "ROOT-ORIGINAL")
	writeMemFile(t, filepath.Join(memDir, "edit.md"), orig)
	writeMemFile(t, filepath.Join(root, "a", ".muxcode", "memory", "edit.md"), memEntry("S", "2026-11-13 10:00", "FROM-A"))
	writeMemFile(t, filepath.Join(root, "b", ".muxcode", "memory", "edit.md"), memEntry("S", "2026-11-13 11:00", "FROM-B"))
	found, _ := FindStrandedMemory(root, memDir)
	results, err := MergeStrandedMemory(memDir, found, time.Now())
	if err != nil || len(results) != 2 {
		t.Fatalf("results %+v, %v", results, err)
	}
	for _, r := range results {
		if b, _ := os.ReadFile(r.Files[0].Backup); string(b) != orig {
			t.Errorf("%s: backup %q, want the root file before the run", r.Rel, b)
		}
	}
	merged, _ := os.ReadFile(filepath.Join(memDir, "edit.md"))
	for _, s := range []string{"ROOT-ORIGINAL", "FROM-A", "FROM-B"} {
		if !strings.Contains(string(merged), s) {
			t.Errorf("merged edit.md lacks %s:\n%s", s, merged)
		}
	}
}

// MUX-223 review should-fix: two separate merge invocations in the same second,
// with an append between them, each back up the root file as it stood when
// that invocation started — not one shared write-once directory keeping the
// first run's copy. TestMergeStrandedMemory_BackupIsThePreRunFile is the
// one-invocation control.
func TestMergeStrandedMemory_SameSecondRunsBackUpTheirOwnStart(t *testing.T) {
	root := t.TempDir()
	memDir := filepath.Join(root, ".muxcode", "memory")
	t.Setenv(MemoryDirEnv, memDir)
	orig := memEntry("Session Summary", "2026-11-13 09:00", "ROOT-ORIGINAL")
	writeMemFile(t, filepath.Join(memDir, "edit.md"), orig)
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)

	writeMemFile(t, filepath.Join(root, "a", ".muxcode", "memory", "edit.md"), memEntry("S", "2026-11-13 10:00", "FROM-A"))
	found, _ := FindStrandedMemory(root, memDir)
	first, err := MergeStrandedMemory(memDir, found, now)
	if err != nil || len(first) != 1 {
		t.Fatalf("first merge %+v, %v", first, err)
	}
	if err := AppendMemory("Session Summary", "BETWEEN-RUNS", "edit"); err != nil {
		t.Fatal(err)
	}
	beforeSecond, _ := os.ReadFile(filepath.Join(memDir, "edit.md"))

	writeMemFile(t, filepath.Join(root, "b", ".muxcode", "memory", "edit.md"), memEntry("S", "2026-11-13 11:00", "FROM-B"))
	found, _ = FindStrandedMemory(root, memDir)
	second, err := MergeStrandedMemory(memDir, found, now)
	if err != nil || len(second) != 1 {
		t.Fatalf("second merge %+v, %v", second, err)
	}

	b1, b2 := first[0].Files[0].Backup, second[0].Files[0].Backup
	if filepath.Dir(b1) == filepath.Dir(b2) {
		t.Errorf("two invocations shared the backup directory %s", filepath.Dir(b1))
	}
	if got, _ := os.ReadFile(b1); string(got) != orig {
		t.Errorf("first run's backup %q, want the original file", got)
	}
	if got, _ := os.ReadFile(b2); string(got) != string(beforeSecond) {
		t.Errorf("second run's backup %q, want the file as it stood at its start %q", got, beforeSecond)
	}
}

// MUX-223 review must-fix: a summary appended while a merge holds the root file
// read must survive the merge's replace. The hook starts the append inside the
// window and gives it 300 ms: unlocked, it lands at once and the rename then
// drops it; locked, it waits for the merge to finish and appends after.
func TestMergeStrandedMemory_ConcurrentAppendSurvives(t *testing.T) {
	root := t.TempDir()
	memDir := filepath.Join(root, ".muxcode", "memory")
	t.Setenv(MemoryDirEnv, memDir)
	writeMemFile(t, filepath.Join(memDir, "edit.md"), memEntry("Session Summary", "2026-11-13 09:00", "ROOT"))
	writeMemFile(t, filepath.Join(root, "sub", ".muxcode", "memory", "edit.md"), memEntry("S", "2026-11-13 10:00", "STRANDED"))

	appended := make(chan error, 1)
	strandedMergeHook = func(string) {
		go func() { appended <- AppendMemory("Session Summary", "LIVE-APPEND", "edit") }()
		select {
		case err := <-appended:
			appended <- err
		case <-time.After(300 * time.Millisecond):
		}
	}
	t.Cleanup(func() { strandedMergeHook = nil })

	found, _ := FindStrandedMemory(root, memDir)
	if _, err := MergeStrandedMemory(memDir, found, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := <-appended; err != nil {
		t.Fatal(err)
	}
	merged, _ := os.ReadFile(filepath.Join(memDir, "edit.md"))
	for _, s := range []string{"ROOT", "STRANDED", "LIVE-APPEND"} {
		if !strings.Contains(string(merged), s) {
			t.Errorf("edit.md lost %s across a concurrent append:\n%s", s, merged)
		}
	}
}

// Control: two merges of the same stranded directory at once — the second
// waits on the lock, finds the directory already kept and skips it — leave
// each entry once and one kept original, with no error.
func TestMergeStrandedMemory_ConcurrentMerges(t *testing.T) {
	root := t.TempDir()
	memDir := filepath.Join(root, ".muxcode", "memory")
	writeMemFile(t, filepath.Join(memDir, "edit.md"), memEntry("Session Summary", "2026-11-13 09:00", "ROOT"))
	writeMemFile(t, filepath.Join(root, "sub", ".muxcode", "memory", "edit.md"), memEntry("S", "2026-11-13 10:00", "STRANDED"))
	found, _ := FindStrandedMemory(root, memDir)

	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			_, err := MergeStrandedMemory(memDir, found, time.Now())
			errs <- err
		}()
	}
	for i := 0; i < 2; i++ {
		if err := <-errs; err != nil {
			t.Errorf("merge %d: %v", i, err)
		}
	}
	merged, _ := os.ReadFile(filepath.Join(memDir, "edit.md"))
	if strings.Count(string(merged), "STRANDED") != 1 || strings.Count(string(merged), "ROOT") != 1 {
		t.Errorf("concurrent merges duplicated or lost entries:\n%s", merged)
	}
	kept, _ := filepath.Glob(filepath.Join(StrandedMemoryKeepDir(memDir), "sub*"))
	if len(kept) != 1 {
		t.Errorf("kept originals %v, want exactly one", kept)
	}
}

// MUX-223 review must-fix: under BUS_MEMORY_DIR overrides the scanner skips
// the real destination and keep directory exactly. External: the checkout
// root's own memory and kept originals are never reported, a nested stranded
// directory is. `<root>/memory`: the scan still runs over the whole checkout.
func TestFindStrandedMemory_Overrides(t *testing.T) {
	root := t.TempDir()
	writeMemFile(t, filepath.Join(root, ".muxcode", "memory", "edit.md"), "root memory")
	writeMemFile(t, filepath.Join(root, ".muxcode", "stranded-memory", "old", ".muxcode", "memory", "edit.md"), "kept")
	writeMemFile(t, filepath.Join(root, "a", "b", ".muxcode", "memory", "edit.md"), "stranded")

	for _, tc := range []struct{ name, memDir string }{
		{"external", filepath.Join(t.TempDir(), "external-memory")},
		{"root/memory", filepath.Join(root, "memory")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := FindStrandedMemory(root, tc.memDir)
			if err != nil {
				t.Fatal(err)
			}
			var rels []string
			for _, s := range got {
				rels = append(rels, s.Rel)
			}
			if strings.Join(rels, ",") != filepath.Join("a", "b", ".muxcode", "memory") {
				t.Errorf("stranded = %v, want only a/b/.muxcode/memory", rels)
			}
		})
	}
}

// Merging a stranded file whose every entry the root already holds writes
// nothing and takes no backup.
func TestMergeStrandedMemory_AllDuplicatesWritesNothing(t *testing.T) {
	root := t.TempDir()
	memDir := filepath.Join(root, ".muxcode", "memory")
	same := memEntry("Session Summary", "2026-11-13 09:00", "SAME")
	writeMemFile(t, filepath.Join(memDir, "edit.md"), same)
	writeMemFile(t, filepath.Join(root, "sub", ".muxcode", "memory", "edit.md"), same)
	found, _ := FindStrandedMemory(root, memDir)
	results, err := MergeStrandedMemory(memDir, found, time.Now())
	if err != nil || len(results) != 1 {
		t.Fatalf("results %+v, %v", results, err)
	}
	if f := results[0].Files[0]; f.Added != 0 || f.Duplicates != 1 || f.Backup != "" {
		t.Errorf("merge = %+v, want 0 added, 1 duplicate, no backup", f)
	}
	if b, _ := os.ReadFile(filepath.Join(memDir, "edit.md")); string(b) != same {
		t.Errorf("the root file was rewritten: %q", b)
	}
}
