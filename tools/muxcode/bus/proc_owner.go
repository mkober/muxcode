package bus

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

// procHolderBinary is the muxcode binary StartProc runs a command through so
// the proc holds an owner lock (ExecHoldingOwnerLock), or false when this
// process is not muxcode — a test binary — and the command runs bare,
// unrecorded. A var so tests can choose.
var procHolderBinary = func() (string, bool) {
	self, err := os.Executable()
	if err != nil || filepath.Base(self) != "muxcode" {
		return "", false
	}
	return self, true
}

// ExecHoldingOwnerLock takes the proc owner lock
// <ownersDir>/proc.<id>.<pid>.owner and execs argv in place, keeping the lock:
// a record lock belongs to the process, which exec does not change, and it
// lasts while the descriptor stays open — so the descriptor is opened without
// close-on-exec. The pid StartProc records is then the lock's holder, which
// the reaper proves through the kernel exactly as it proves a listener or a
// harness (MUX-184). A failed lock still execs: the proc runs unrecorded and is
// reported unproven rather than killed. Returns only when the exec fails.
func ExecHoldingOwnerLock(ownersDir, id string, argv []string) error {
	if len(argv) == 0 {
		return fmt.Errorf("no command to run")
	}
	path, err := exec.LookPath(argv[0])
	if err != nil {
		return err
	}
	lockPath := filepath.Join(ownersDir, ownerLockName("proc", id, os.Getpid()))
	if os.MkdirAll(ownersDir, 0755) == nil {
		if fd, err := syscall.Open(lockPath, syscall.O_CREAT|syscall.O_RDWR, 0644); err == nil {
			lk := syscall.Flock_t{Type: syscall.F_WRLCK, Whence: int16(io.SeekStart)}
			_, _, errno := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), syscall.F_SETFD, 0)
			if errno != 0 || syscall.FcntlFlock(uintptr(fd), syscall.F_SETLK, &lk) != nil {
				syscall.Close(fd)
			}
		}
	}
	return syscall.Exec(path, argv, os.Environ())
}
