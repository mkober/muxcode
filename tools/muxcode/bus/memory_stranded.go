package bus

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"
)

// StrandedMemory is a `.muxcode/memory` directory below a project's root
// other than the root's own — written by a muxcode before MUX-223, whose
// memory path was relative to the caller's working directory.
type StrandedMemory struct {
	Dir   string   // absolute path of the stranded memory directory
	Rel   string   // Dir relative to the project root
	Files []string // its regular files, relative to Dir
}

// FileMerge reports one stranded file merged into the root's memory.
type FileMerge struct {
	File       string // relative to the memory directory
	Added      int    // entries new to the root's file
	Duplicates int    // entries the root's file already held, skipped
	Backup     string // the root file's copy taken before the write, if it existed
}

// StrandedMergeResult reports one stranded directory merged and kept.
type StrandedMergeResult struct {
	Rel    string
	Files  []FileMerge
	KeptAt string // where the untouched original now lives
}

// StrandedMemoryKeepDir is where merged originals and root backups are kept
// for a memory directory: `.muxcode/stranded-memory`, beside it rather than
// inside it, where a subdirectory would read as a role's archive.
func StrandedMemoryKeepDir(memDir string) string {
	return filepath.Join(filepath.Dir(memDir), "stranded-memory")
}

// FindStrandedMemory lists the `.muxcode/memory` directories below root. It
// skips exactly: memDir and its keep directory, wherever an override puts
// them; the checkout root's own `.muxcode/memory` and `.muxcode/stranded-memory`,
// which are never stranded, under an external override too; `.git` and
// `node_modules`. It skips no parent of those, so memDir at `<root>/memory`
// hides nothing, and it never follows a symlink.
func FindStrandedMemory(root, memDir string) ([]StrandedMemory, error) {
	root = realPath(root)
	skip := map[string]bool{}
	for _, d := range []string{
		memDir, StrandedMemoryKeepDir(memDir),
		filepath.Join(root, ".muxcode", "memory"), filepath.Join(root, ".muxcode", "stranded-memory"),
	} {
		skip[realPath(d)] = true
	}
	var found []StrandedMemory
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == root {
				return err
			}
			return fs.SkipDir
		}
		if !d.IsDir() {
			return nil
		}
		switch {
		case path != root && (d.Name() == ".git" || d.Name() == "node_modules" || skip[filepath.Clean(path)]):
			return fs.SkipDir
		case d.Name() == "memory" && filepath.Base(filepath.Dir(path)) == ".muxcode":
			s, err := strandedAt(root, path)
			if err != nil {
				return err
			}
			found = append(found, s)
			return fs.SkipDir
		}
		return nil
	})
	return found, err
}

// realPath resolves symlinks in p, or in its nearest existing parent when p
// does not exist yet, so a path compares equal to the same directory reached
// through a symlink (macOS's /var → /private/var).
func realPath(p string) string {
	p = filepath.Clean(p)
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	if parent := filepath.Dir(p); parent != p {
		return filepath.Join(realPath(parent), filepath.Base(p))
	}
	return p
}

func strandedAt(root, dir string) (StrandedMemory, error) {
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return StrandedMemory{}, err
	}
	s := StrandedMemory{Dir: dir, Rel: rel}
	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return err
		}
		f, err := filepath.Rel(dir, path)
		if err == nil {
			s.Files = append(s.Files, f)
		}
		return err
	})
	return s, err
}

// MergeStrandedMemory applies the user's Decision 2 (MUX-223) to each stranded
// directory: every file is merged entry by entry into the same path under
// memDir — sections in time order, exact duplicates skipped — after the root's
// existing file is copied to this run's own `<keep>/backup-<stamp>-*/`; then the untouched
// original directory is moved to `<keep>/<Rel>`. Nothing is deleted. A file
// adding no entry leaves the root's file unwritten; a non-`.md` file moves
// with its directory unmerged.
//
// The whole run holds the project memory lock (withMemoryLock), the one
// AppendMemory and its rotation take, so a summary appended mid-merge waits
// rather than being overwritten by the merged file — Decision 2's "no summary
// lost". A directory a concurrent merge already moved is skipped.
func MergeStrandedMemory(memDir string, stranded []StrandedMemory, now time.Time) (results []StrandedMergeResult, err error) {
	err = withMemoryLock(memDir, func() error {
		results, err = mergeStrandedLocked(memDir, stranded, now)
		return err
	})
	return results, err
}

func mergeStrandedLocked(memDir string, stranded []StrandedMemory, now time.Time) ([]StrandedMergeResult, error) {
	keep := StrandedMemoryKeepDir(memDir)
	stamp := now.Format("20060102-150405")
	backups := &backupRun{parent: keep, prefix: "backup-" + stamp + "-"}
	var results []StrandedMergeResult
	for _, s := range stranded {
		if _, err := os.Lstat(s.Dir); os.IsNotExist(err) {
			continue
		}
		r := StrandedMergeResult{Rel: s.Rel}
		for _, f := range s.Files {
			if !strings.HasSuffix(f, ".md") {
				continue
			}
			fm, err := mergeStrandedFile(memDir, backups, s.Dir, f)
			if err != nil {
				return results, fmt.Errorf("%s: %w", filepath.Join(s.Rel, f), err)
			}
			r.Files = append(r.Files, fm)
		}
		dest := filepath.Join(keep, s.Rel)
		if _, err := os.Lstat(dest); err == nil {
			dest += "-" + stamp
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
			return results, err
		}
		if err := os.Rename(s.Dir, dest); err != nil {
			return results, fmt.Errorf("keep %s: %w", s.Rel, err)
		}
		r.KeptAt = dest
		results = append(results, r)
	}
	return results, nil
}

// backupRun is one merge invocation's backup directory, created on its first
// backup with a unique suffix: named by the second alone, two invocations in
// the same second shared it, and the second's write-once backup kept the
// first's pre-run file instead of its own.
type backupRun struct {
	parent, prefix, dir string
}

func (b *backupRun) path(file string) (string, error) {
	if b.dir == "" {
		if err := os.MkdirAll(b.parent, 0755); err != nil {
			return "", err
		}
		dir, err := os.MkdirTemp(b.parent, b.prefix)
		if err != nil {
			return "", err
		}
		b.dir = dir
	}
	return filepath.Join(b.dir, file), nil
}

func mergeStrandedFile(memDir string, backups *backupRun, srcDir, file string) (FileMerge, error) {
	fm := FileMerge{File: file}
	src, err := os.ReadFile(filepath.Join(srcDir, file))
	if err != nil {
		return fm, err
	}
	dst := filepath.Join(memDir, file)
	old, err := os.ReadFile(dst)
	exists := err == nil
	if err != nil && !os.IsNotExist(err) {
		return fm, err
	}
	merged, added, dups := mergeMemoryText(string(old), string(src))
	fm.Added, fm.Duplicates = added, dups
	if added == 0 {
		return fm, nil
	}
	if strandedMergeHook != nil {
		strandedMergeHook(file)
	}
	if exists {
		if fm.Backup, err = backups.path(file); err != nil {
			return fm, err
		}
		if err := writeBackupOnce(fm.Backup, old); err != nil {
			return fm, err
		}
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return fm, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), filepath.Base(dst)+".merging-*")
	if err != nil {
		return fm, err
	}
	if _, err := tmp.WriteString(merged); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return fm, err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return fm, err
	}
	if err := os.Chmod(tmp.Name(), 0644); err != nil {
		os.Remove(tmp.Name())
		return fm, err
	}
	return fm, os.Rename(tmp.Name(), dst)
}

// strandedMergeHook, when set by a test, runs after a root file is read and
// before it is replaced — the window an unlocked append would be lost in.
var strandedMergeHook func(file string)

// withMemoryLock runs fn holding an exclusive flock on memDir's
// `.memory.lock`, the one lock serializing every replace of a project memory
// file with AppendMemory and its rotation. Memory readers list only `.md`
// files, so the lock file is invisible to them.
func withMemoryLock(memDir string, fn func() error) error {
	if err := os.MkdirAll(memDir, 0755); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(memDir, ".memory.lock"), os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("lock project memory: %w", err)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return fn()
}

// writeBackupOnce writes a root file's pre-merge copy unless this run already
// took one: two stranded directories feeding the same root file share the
// run's backup directory, and the second copy would be the first merge's
// output, not the original.
func writeBackupOnce(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if os.IsExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

var memoryTimestampLine = regexp.MustCompile(`^_(\d{4}-\d{2}-\d{2} \d{2}:\d{2})_$`)

// memoryBlock is one AppendMemory entry, raw: its `## ` line, its timestamp
// line and body.
type memoryBlock struct{ raw, ts string }

// splitMemoryBlocks splits AppendMemory's format into the text before the
// first entry and its entries. An entry starts at a `## ` line followed by a
// timestamp line; any other `## ` line is body text of the entry above, so a
// heading inside a summary stays with it.
func splitMemoryBlocks(text string) (preamble string, blocks []memoryBlock) {
	lines := strings.Split(text, "\n")
	var pre []string
	cur := -1
	for i, line := range lines {
		if strings.HasPrefix(line, "## ") && i+1 < len(lines) {
			if m := memoryTimestampLine.FindStringSubmatch(lines[i+1]); m != nil {
				blocks = append(blocks, memoryBlock{raw: line, ts: m[1]})
				cur = len(blocks) - 1
				continue
			}
		}
		if cur < 0 {
			pre = append(pre, line)
		} else {
			blocks[cur].raw += "\n" + line
		}
	}
	for i := range blocks {
		blocks[i].raw = strings.TrimRight(blocks[i].raw, "\n")
	}
	return strings.TrimSpace(strings.Join(pre, "\n")), blocks
}

// mergeMemoryText merges src's entries into dst's: the union in timestamp
// order (dst's first among equals), exact duplicates — by trimmed text —
// skipped. dst's preamble is kept; src's preamble counts as one entry-less
// block only if dst lacks it, so nothing src held is dropped.
func mergeMemoryText(dst, src string) (merged string, added, dups int) {
	dstPre, blocks := splitMemoryBlocks(dst)
	srcPre, srcBlocks := splitMemoryBlocks(src)
	seen := map[string]bool{}
	for _, b := range blocks {
		seen[strings.TrimSpace(b.raw)] = true
	}
	for _, b := range srcBlocks {
		key := strings.TrimSpace(b.raw)
		if seen[key] {
			dups++
			continue
		}
		seen[key] = true
		blocks = append(blocks, b)
		added++
	}
	pre := dstPre
	if srcPre != "" && !strings.Contains(dstPre, srcPre) {
		if pre != "" {
			pre += "\n\n"
		}
		pre += srcPre
		added++
	}
	sort.SliceStable(blocks, func(i, j int) bool { return blocks[i].ts < blocks[j].ts })
	var b strings.Builder
	if pre != "" {
		b.WriteString(pre + "\n")
	}
	for _, blk := range blocks {
		b.WriteString("\n" + blk.raw + "\n")
	}
	return b.String(), added, dups
}
