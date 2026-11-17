package bus

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// memoryRootRepo builds a scratch git checkout with one commit and the
// subdirectories a/b and c, unsets BUS_MEMORY_DIR so the git road resolves,
// and returns its root with symlinks resolved (macOS temp dirs sit behind
// /var → /private/var).
func memoryRootRepo(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(root))
	runTestGit(t, root, nil, "init")
	runTestGit(t, root, nil, "commit", "--allow-empty", "-m", "init")
	for _, d := range []string{"a/b", "c"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv(MemoryDirEnv, "")
	return root
}

// chdirTest changes the working directory for the rest of the test.
func chdirTest(t *testing.T, dir string) {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })
}

// MUX-223: a write from a subdirectory lands in the checkout root's
// .muxcode/memory, not beside the caller, and a read from a sibling
// subdirectory finds it. Before, the relative default stranded the summary in
// a/b/.muxcode/memory, where restore from anywhere else never looked.
func TestProjectMemory_SubdirectoryWritesAtRoot(t *testing.T) {
	root := memoryRootRepo(t)
	chdirTest(t, filepath.Join(root, "a", "b"))
	if err := AppendMemory("Session Summary", "summary SUBDIR-WRITE", "edit"); err != nil {
		t.Fatalf("AppendMemory from a subdirectory: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, ".muxcode", "memory", "edit.md"))
	if err != nil || !strings.Contains(string(data), "SUBDIR-WRITE") {
		t.Fatalf("root memory %q, %v; want the subdirectory's write", data, err)
	}
	if _, err := os.Stat(filepath.Join(root, "a", "b", ".muxcode")); !os.IsNotExist(err) {
		t.Errorf("a memory tree was created beside the caller: %v", err)
	}

	chdirTest(t, filepath.Join(root, "c"))
	got, err := ReadContextFull("edit", 1, false)
	if err != nil || !strings.Contains(got, "SUBDIR-WRITE") {
		t.Errorf("memory context from a sibling subdirectory = %q, %v; want the write", got, err)
	}
}

// MUX-223 Decision 1: a worktree (a spawn worker's) resolves the main
// checkout's root — the parent of --git-common-dir — not its own toplevel.
func TestProjectMemory_WorktreeSharesMainRoot(t *testing.T) {
	root := memoryRootRepo(t)
	wt := filepath.Join(filepath.Dir(root), filepath.Base(root)+"-wt")
	runTestGit(t, root, nil, "worktree", "add", "--detach", wt)
	t.Cleanup(func() { _ = os.RemoveAll(wt) })
	chdirTest(t, wt)
	dir, err := ProjectMemoryDir()
	if want := filepath.Join(root, ".muxcode", "memory"); err != nil || dir != want {
		t.Errorf("ProjectMemoryDir in a worktree = %q, %v; want the main checkout's %q", dir, err, want)
	}
}

// MUX-223 Decision 1, step 3: outside any checkout with BUS_MEMORY_DIR unset,
// a write fails naming BUS_MEMORY_DIR and creates nothing beside the caller,
// and memory context returns global memory alone — never a cwd-relative path.
func TestProjectMemory_OutsideProjectFailsWrites(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))
	t.Setenv(MemoryDirEnv, "")
	chdirTest(t, dir)
	if err := AppendGlobalMemory("Note", "global GLOBAL-ONLY", "edit"); err != nil {
		t.Fatal(err)
	}

	err = AppendMemory("Session Summary", "nowhere", "edit")
	if !errors.Is(err, ErrNoMemoryRoot) || !strings.Contains(err.Error(), MemoryDirEnv) {
		t.Errorf("AppendMemory outside a project = %v; want ErrNoMemoryRoot naming %s", err, MemoryDirEnv)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("a failed write created %v in the working directory", entries)
	}
	if MemoryPath("edit") != "" || MemoryArchiveDir("edit") != "" {
		t.Errorf("path helpers resolved %q, %q with no root; want empty", MemoryPath("edit"), MemoryArchiveDir("edit"))
	}
	got, err := ReadContextFull("edit", 1, true)
	if err != nil || !strings.Contains(got, "GLOBAL-ONLY") || strings.Contains(got, "# edit Memory") {
		t.Errorf("memory context outside a project = %q, %v; want global memory only", got, err)
	}
}

// MUX-223 negative control: an explicit BUS_MEMORY_DIR wins inside a checkout,
// and the root's own memory directory is left untouched.
func TestProjectMemory_ExplicitEnvWins(t *testing.T) {
	root := memoryRootRepo(t)
	explicit := filepath.Join(t.TempDir(), "explicit-memory")
	t.Setenv(MemoryDirEnv, explicit)
	chdirTest(t, filepath.Join(root, "a", "b"))
	if err := AppendMemory("Note", "EXPLICIT-WRITE", "edit"); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(explicit, "edit.md")); err != nil || !strings.Contains(string(data), "EXPLICIT-WRITE") {
		t.Errorf("explicit memory %q, %v; want the write", data, err)
	}
	if _, err := os.Stat(filepath.Join(root, ".muxcode")); !os.IsNotExist(err) {
		t.Errorf("the checkout root's memory was written despite %s: %v", MemoryDirEnv, err)
	}
}

// MUX-223: a session exports the main checkout's root found from its project
// directory, even launched on a subdirectory; a project outside git exports
// its own directory; an inherited value is honoured outside a muxcode pane and
// ignored inside one (another session's export).
func TestLaunchMemoryDir(t *testing.T) {
	root := memoryRootRepo(t)
	t.Setenv("MUXCODE", "")
	launch := func(dir string) string {
		t.Helper()
		got, err := LaunchMemoryDir(dir)
		if err != nil {
			t.Fatalf("LaunchMemoryDir(%s): %v", dir, err)
		}
		return got
	}
	if got, want := launch(filepath.Join(root, "a")), filepath.Join(root, ".muxcode", "memory"); got != want {
		t.Errorf("LaunchMemoryDir(subdirectory) = %q, want %q", got, want)
	}
	plain, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := launch(plain), filepath.Join(plain, ".muxcode", "memory"); got != want {
		t.Errorf("LaunchMemoryDir(non-git project) = %q, want %q", got, want)
	}

	t.Setenv(MemoryDirEnv, "/explicit/memory")
	if got := launch(root); got != "/explicit/memory" {
		t.Errorf("an explicit %s outside a pane = %q, want it honoured", MemoryDirEnv, got)
	}
	t.Setenv(MemoryDirEnv, "custom-memory")
	if _, err := LaunchMemoryDir(root); !errors.Is(err, ErrRelativeMemoryDir) {
		t.Errorf("a relative %s at launch = %v, want ErrRelativeMemoryDir", MemoryDirEnv, err)
	}
	t.Setenv("MUXCODE", "1")
	if got, want := launch(root), filepath.Join(root, ".muxcode", "memory"); got != want {
		t.Errorf("an inherited %s inside a muxcode pane = %q, want the project's %q", MemoryDirEnv, got, want)
	}
}

// MUX-223 review must-fix: a relative BUS_MEMORY_DIR resolved per caller split
// memory again — repo/a and repo/c wrote two trees. It is refused from every
// directory, inside a checkout and outside one, and nothing is created beside
// any caller. Negative control: the same value made absolute is honoured.
func TestProjectMemory_RelativeOverrideRefused(t *testing.T) {
	root := memoryRootRepo(t)
	outside, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(MemoryDirEnv, "custom-memory")
	for _, dir := range []string{filepath.Join(root, "a"), filepath.Join(root, "c"), outside} {
		chdirTest(t, dir)
		if _, err := ProjectMemoryDir(); !errors.Is(err, ErrRelativeMemoryDir) {
			t.Errorf("from %s: ProjectMemoryDir = %v, want ErrRelativeMemoryDir", dir, err)
		}
		if err := AppendMemory("Note", "RELATIVE", "edit"); !errors.Is(err, ErrRelativeMemoryDir) || !strings.Contains(err.Error(), MemoryDirEnv) {
			t.Errorf("from %s: AppendMemory = %v, want ErrRelativeMemoryDir naming %s", dir, err, MemoryDirEnv)
		}
		if _, err := os.Stat(filepath.Join(dir, "custom-memory")); !os.IsNotExist(err) {
			t.Errorf("a relative override created memory beside the caller in %s: %v", dir, err)
		}
	}

	abs := filepath.Join(t.TempDir(), "custom-memory")
	t.Setenv(MemoryDirEnv, abs)
	if got, err := ProjectMemoryDir(); err != nil || got != abs {
		t.Errorf("absolute override = %q, %v; want %q", got, err, abs)
	}
}

// MUX-223 review must-fix: a spawn worker's worktree resolves memory in the
// main checkout, outside its Codex workspace, so the sandbox grants that
// directory — the directory alone, never the checkout or the worktree. An
// explicit BUS_MEMORY_DIR outside the repo is granted the same way.
// TestCodexMuxcodeStateRoots_DropsRelativePaths is the no-root control.
func TestCodexMuxcodeStateRoots_GrantProjectMemory(t *testing.T) {
	codexTestHome(t)
	root := memoryRootRepo(t)
	wt := filepath.Join(filepath.Dir(root), filepath.Base(root)+"-wt")
	runTestGit(t, root, nil, "worktree", "add", "--detach", wt)
	t.Cleanup(func() { _ = os.RemoveAll(wt) })
	chdirTest(t, wt)

	want := filepath.Join(root, ".muxcode", "memory")
	roots := muxcodeStateRoots()
	found := false
	for _, r := range roots {
		found = found || r == want
		if r == root || r == wt {
			t.Errorf("a checkout was granted whole: %q in %v", r, roots)
		}
	}
	if !found {
		t.Errorf("worktree state roots %v lack the main checkout's memory %q", roots, want)
	}

	external := filepath.Join(t.TempDir(), "external-memory")
	t.Setenv(MemoryDirEnv, external)
	found = false
	for _, r := range muxcodeStateRoots() {
		found = found || r == external
	}
	if !found {
		t.Errorf("an explicit external %s %q was not granted", MemoryDirEnv, external)
	}
}
