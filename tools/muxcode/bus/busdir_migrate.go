package bus

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
)

// BusDirMigration is the outcome of MigrateLegacyBusDir.
type BusDirMigration int

const (
	// BusDirNoLegacy: no /tmp directory to move, or the base is overridden.
	BusDirNoLegacy BusDirMigration = iota
	// BusDirMigrated: the /tmp directory was moved into place.
	BusDirMigrated
	// BusDirConflict: both directories exist; the new one is used and the
	// old one is left for the user.
	BusDirConflict
)

// Seams for tests: where legacy directories live, whether migration runs (it
// never does in a test binary, where a session named like a live one would take
// its state), and the first rename, so a test can force the EXDEV copy road.
var (
	legacyBusDirRoot = LegacyBusDirBase
	migrationEnabled = func() bool { return !testing.Testing() }
	renameDir        = os.Rename
)

// LegacyBusDir returns where a session's bus directory lived before MUX-208.
func LegacyBusDir(session string) string {
	return filepath.Join(legacyBusDirRoot, "muxcode-bus-"+session)
}

type sessionMigration struct {
	mu   sync.Mutex
	done bool
}

var (
	migrationsMu sync.Mutex
	migrations   = map[string]*sessionMigration{}
)

// migrateLegacyBusDirOnce runs MigrateLegacyBusDir the first time a process
// resolves a session, so the first new-binary process after an install moves
// the session — not whichever happens to write an empty bus first. Concurrent
// resolutions of the session wait for the move to finish. A failed move
// panics rather than return a path a caller would initialize beside the
// stranded state, and is not remembered, so the next resolution retries.
// MigrateLegacyBusDir never calls BusDir, so holding the session lock across it
// cannot deadlock. A conflict is not logged here: every process would log it.
func migrateLegacyBusDirOnce(session string) {
	migrationsMu.Lock()
	m := migrations[session]
	if m == nil {
		m = &sessionMigration{}
		migrations[session] = m
	}
	migrationsMu.Unlock()

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.done {
		return
	}
	if _, err := MigrateLegacyBusDir(session); err != nil {
		panic(fmt.Sprintf("muxcode: %v — the session's state is still in %s; fix the cause and retry (MUX-208)",
			err, LegacyBusDir(session)))
	}
	m.done = true
}

// MigrateLegacyBusDir moves /tmp/muxcode-bus-{session} to BusDir(session)
// when the old directory exists and the new one does not, logging one
// bus-dir-migrated row. Nothing moves on an overridden base (tests,
// MUXCODE_BUS_BASE) or inside a test binary.
func MigrateLegacyBusDir(session string) (BusDirMigration, error) {
	if session == "" || busDirBaseOverridden() || !migrationEnabled() {
		return BusDirNoLegacy, nil
	}
	base, err := busDirBase()
	if err != nil {
		return BusDirNoLegacy, err
	}
	return migrateBusDirAt(session, LegacyBusDir(session), base)
}

// migrateBusDirAt moves oldDir into base/muxcode-bus-{session}. Every decision
// past "is there a legacy directory" is taken under a flock beside the
// destination, so exactly one process moves a session and none mistakes a
// move in progress for a conflict. The move is a rename on one device; across
// devices (Linux's tmpfs /tmp) it copies into a hidden staging directory and
// renames that into place, so the destination never exists half-copied. A
// failed copy leaves the legacy directory intact and no destination at all.
func migrateBusDirAt(session, oldDir, base string) (BusDirMigration, error) {
	if !isDir(oldDir) {
		return BusDirNoLegacy, nil
	}
	if err := os.MkdirAll(base, 0o700); err != nil {
		return BusDirNoLegacy, fmt.Errorf("create bus base %s: %w", base, err)
	}
	lock, err := os.OpenFile(filepath.Join(base, ".muxcode-bus-"+session+".migrate.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return BusDirNoLegacy, fmt.Errorf("open migration lock: %w", err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return BusDirNoLegacy, fmt.Errorf("lock migration: %w", err)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)

	newDir := filepath.Join(base, "muxcode-bus-"+session)
	if !isDir(oldDir) {
		return BusDirNoLegacy, nil
	}
	if pathExists(newDir) {
		return BusDirConflict, nil
	}
	if err := moveDir(session, oldDir, newDir); err != nil {
		LogLifecycle(session, "error", "bus", "bus-dir-migrate-failed",
			fmt.Sprintf("Could not move %s to %s: %v", oldDir, newDir, err))
		return BusDirNoLegacy, fmt.Errorf("migrate %s to %s: %w", oldDir, newDir, err)
	}
	LogLifecycle(session, "info", "bus", "bus-dir-migrated",
		fmt.Sprintf("Moved %s to %s (MUX-208)", oldDir, newDir))
	return BusDirMigrated, nil
}

// rewriteProcLogPaths repoints the LogFile of every proc.jsonl entry under
// from to to, in the proc.jsonl inside dir. Entries store absolute paths, so
// after the move they would name a directory that no longer exists and
// ProcLogTail and the exit-code scan would read nothing. moveDir calls it
// before the destination is published, so no other process can read or update
// the registry mid-rewrite; the write is a temp file renamed over the original,
// never a truncation in place. It reads and writes the file directly: BusDir
// would re-enter the migration it is called from.
func rewriteProcLogPaths(dir, from, to string) error {
	path := filepath.Join(dir, "proc.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	prefix := from + string(filepath.Separator)
	var out bytes.Buffer
	changed := false
	for _, line := range bytes.Split(data, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var e ProcEntry
		if json.Unmarshal(line, &e) == nil && strings.HasPrefix(e.LogFile, prefix) {
			e.LogFile = filepath.Join(to, strings.TrimPrefix(e.LogFile, prefix))
			if b, err := json.Marshal(e); err == nil {
				line, changed = b, true
			}
		}
		out.Write(line)
		out.WriteByte('\n')
	}
	if !changed {
		return nil
	}
	tmp := fmt.Sprintf("%s.migrating-%d", path, os.Getpid())
	if err := os.WriteFile(tmp, out.Bytes(), 0o644); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// ReportLegacyBusDirConflict migrates session and logs a bus-dir-conflict row
// when a /tmp directory is left beside the one in use. Session launch and
// daemon start call it, once per session unlike BusDir, and must stop on its
// error: the state is still in /tmp.
func ReportLegacyBusDirConflict(session string) error {
	outcome, err := MigrateLegacyBusDir(session)
	if err != nil {
		return err
	}
	if outcome == BusDirConflict {
		base, _ := busDirBase()
		LogLifecycle(session, "warn", "bus", "bus-dir-conflict",
			fmt.Sprintf("Both %s and %s exist; using the latter. The /tmp copy is left for you to inspect or remove (MUX-208)",
				LegacyBusDir(session), filepath.Join(base, "muxcode-bus-"+session)))
	}
	return nil
}

// moveDir renames src to dst, or across devices copies src into a staging
// directory beside dst and renames it into place. The legacy directory is
// removed only after dst is published; a leftover from that removal is logged
// and left, since the move itself succeeded.
func moveDir(session, src, dst string) error {
	repointFailed := func(dir, from, to string, err error) {
		LogLifecycle(session, "warn", "bus", "bus-dir-migrate-proc-paths-failed",
			fmt.Sprintf("Could not repoint proc.jsonl log paths in %s: %v", dir, err))
	}
	if err := rewriteProcLogPaths(src, src, dst); err != nil {
		repointFailed(src, src, dst, err)
	}
	err := renameDir(src, dst)
	if err == nil {
		return nil
	}
	if rerr := rewriteProcLogPaths(src, dst, src); rerr != nil {
		repointFailed(src, dst, src, rerr)
	}
	if !errors.Is(err, syscall.EXDEV) {
		return err
	}
	staging := filepath.Join(filepath.Dir(dst), fmt.Sprintf(".%s.migrating-%d", filepath.Base(dst), os.Getpid()))
	os.RemoveAll(staging)
	if err := copyTree(src, staging); err != nil {
		os.RemoveAll(staging)
		return err
	}
	if err := rewriteProcLogPaths(staging, src, dst); err != nil {
		repointFailed(staging, src, dst, err)
	}
	if err := os.Rename(staging, dst); err != nil {
		os.RemoveAll(staging)
		return err
	}
	if err := os.RemoveAll(src); err != nil {
		LogLifecycle(session, "warn", "bus", "bus-dir-migrate-cleanup-failed",
			fmt.Sprintf("Moved to %s but could not remove %s: %v", dst, src, err))
	}
	return nil
}

// copyTree copies directories, regular files and symlinks with their modes;
// sockets and FIFOs are skipped, since nothing in a bus directory needs one.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			return os.MkdirAll(target, info.Mode().Perm())
		case info.Mode()&fs.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		case info.Mode().IsRegular():
			return copyFileMode(path, target, info.Mode().Perm())
		}
		return nil
	})
}

func copyFileMode(src, dst string, mode fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func pathExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}
