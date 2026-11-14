package bus

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
)

// ErrLockHeld reports that another live process holds a pid lock.
var ErrLockHeld = errors.New("lock held by another process")

// heldPidLocks names the locks this process holds, so LockHolderPid never
// opens one: closing that descriptor would release this process's own lock.
var (
	heldPidLocksMu sync.Mutex
	heldPidLocks   = map[string]bool{}
)

func setPidLockHeld(path string, held bool) {
	heldPidLocksMu.Lock()
	defer heldPidLocksMu.Unlock()
	if held {
		heldPidLocks[path] = true
	} else {
		delete(heldPidLocks, path)
	}
}

func pidLockHeldHere(path string) bool {
	heldPidLocksMu.Lock()
	defer heldPidLocksMu.Unlock()
	return heldPidLocks[path]
}

// AcquireInstanceLock makes this process the single instance behind lockPath
// and publishes it as the owner of ownerPath, holding both for its lifetime.
// lockPath keeps the flock every muxcode version takes, so an older and a
// newer binary still exclude each other — flock and record locks are
// independent on Linux, so a record lock alone let two daemons share one bus.
// ownerPath carries the record lock LockHolderPid asks the kernel about
// (MUX-161). Either failing unwinds both; ErrLockHeld means another process is
// the instance.
func AcquireInstanceLock(lockPath, ownerPath string) (release func(), err error) {
	unlockInstance, err := acquireFlock(lockPath)
	if err != nil {
		return nil, err
	}
	unlockOwner, err := AcquirePidLock(ownerPath)
	if err != nil {
		unlockInstance()
		return nil, err
	}
	return func() { unlockOwner(); unlockInstance() }, nil
}

// acquireFlock takes the non-blocking exclusive flock on path and writes this
// process's pid into it, exactly as every earlier daemon did.
func acquireFlock(path string) (release func(), err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrLockHeld
		}
		return nil, fmt.Errorf("flock %s: %w", path, err)
	}
	release = func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}
	if err := publishPid(f); err != nil {
		release()
		return nil, fmt.Errorf("write pid to %s: %w", path, err)
	}
	return release, nil
}

// AcquirePidLock takes an exclusive POSIX record lock (fcntl F_SETLK) on path
// for this process's lifetime and returns its release. The kernel records the
// owner and LockHolderPid asks it, so a holder's identity never rests on the
// file's contents — before MUX-161's review a flock holder was named by the
// pid written in the file, which a new holder had not yet overwritten, so a
// reused pid could be killed. The pid is still written, for a human reading
// the file; a failure to write it is returned, though discovery never reads
// it. Record locks are per process and dropped when this process closes any
// descriptor of the file, so nothing else in the holder may open it. Returns
// ErrLockHeld when another process holds it.
func AcquirePidLock(path string) (release func(), err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, err
	}
	lk := wholeFileLock(syscall.F_WRLCK)
	if err := syscall.FcntlFlock(f.Fd(), syscall.F_SETLK, &lk); err != nil {
		f.Close()
		if errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EACCES) {
			return nil, ErrLockHeld
		}
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	setPidLockHeld(path, true)
	release = func() {
		unlock := wholeFileLock(syscall.F_UNLCK)
		_ = syscall.FcntlFlock(f.Fd(), syscall.F_SETLK, &unlock)
		f.Close()
		setPidLockHeld(path, false)
	}
	if err := publishPid(f); err != nil {
		release()
		return nil, fmt.Errorf("write pid to %s: %w", path, err)
	}
	return release, nil
}

// LockHolderPid asks the kernel which process holds path's record lock
// (fcntl F_GETLK) and returns its pid, or 0 when the lock is free, the file is
// missing, or the holder is this process — whose own lock never conflicts.
// It takes no lock, so it can never delay a starting holder.
func LockHolderPid(path string) int {
	if pidLockHeldHere(path) {
		return 0
	}
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	lk := wholeFileLock(syscall.F_WRLCK)
	if err := syscall.FcntlFlock(f.Fd(), syscall.F_GETLK, &lk); err != nil {
		return 0
	}
	if lk.Type == syscall.F_UNLCK || lk.Pid <= 0 {
		return 0
	}
	return int(lk.Pid)
}

func wholeFileLock(typ int16) syscall.Flock_t {
	return syscall.Flock_t{Type: typ, Whence: int16(io.SeekStart)}
}

func publishPid(f *os.File) error {
	if err := f.Truncate(0); err != nil {
		return err
	}
	_, err := f.WriteAt([]byte(strconv.Itoa(os.Getpid())), 0)
	return err
}
