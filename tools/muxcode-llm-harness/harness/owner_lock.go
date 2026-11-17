package harness

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

// holdOwnerLock records this process as one of its session's harness
// processes for its lifetime: an exclusive record lock on
// <busDir>/lock/owners/harness.<role>.<pid>.owner. The muxcode reaper asks
// the kernel which process holds it, so the proof survives the daemon that
// started the harness and can never name a reused pid (MUX-184). The layout
// is muxcode's bus.OwnerLockDir contract; this module cannot import bus.
// The release unlocks and removes the file; a failure leaves the harness
// unrecorded, which the reaper reports as unproven rather than kills.
func holdOwnerLock(busDir, role string) (release func()) {
	path := filepath.Join(busDir, "lock", "owners", fmt.Sprintf("harness.%s.%d.owner", role, os.Getpid()))
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return func() {}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return func() {}
	}
	lk := syscall.Flock_t{Type: syscall.F_WRLCK, Whence: int16(io.SeekStart)}
	if err := syscall.FcntlFlock(f.Fd(), syscall.F_SETLK, &lk); err != nil {
		f.Close()
		return func() {}
	}
	return func() {
		f.Close()
		_ = os.Remove(path)
	}
}
