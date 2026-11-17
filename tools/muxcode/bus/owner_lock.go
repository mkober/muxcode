package bus

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// OwnerLockDir holds one record-lock file per long-lived process a session
// starts beyond its daemon and monitor (MUX-184): `<family>.<role>.<pid>.owner`,
// role "-" when the family has none. The harness module writes the same
// layout with its own copy of the lock code, so the name format is a
// cross-module contract.
//
// The file proves ownership the way the daemon's and monitor's owner locks do
// (MUX-161): the kernel answers which process holds it (LockHolderPid), so a
// reused pid can never be named and no `ps` is needed. A file whose holder is
// gone is free and says nothing.
func OwnerLockDir(session string) string {
	return filepath.Join(BusDir(session), "lock", "owners")
}

// Owner families with a record lock in OwnerLockDir.
const (
	OwnerFamilyHarness  = "harness"
	OwnerFamilyListener = "listener"
	OwnerFamilyWebhook  = "webhook"
)

func ownerLockName(family, role string, pid int) string {
	if role == "" {
		role = "-"
	}
	return fmt.Sprintf("%s.%s.%d.owner", family, role, pid)
}

// parseOwnerLockName is ownerLockName's inverse; ok is false for any other
// file, so a stray name is never read as a record.
func parseOwnerLockName(name string) (family, role string, pid int, ok bool) {
	base, found := strings.CutSuffix(name, ".owner")
	if !found {
		return "", "", 0, false
	}
	parts := strings.Split(base, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" {
		return "", "", 0, false
	}
	pid, err := strconv.Atoi(parts[2])
	if err != nil || pid <= 0 {
		return "", "", 0, false
	}
	return parts[0], parts[1], pid, true
}

// HoldOwnerLock records the calling process as one of session's family
// processes for its lifetime and returns the release, which also removes the
// file. A failure is not fatal — the process runs unrecorded, which a reaper
// reports as unproven rather than kills — so the release is always callable.
func HoldOwnerLock(session, family, role string) (release func()) {
	path := filepath.Join(OwnerLockDir(session), ownerLockName(family, role, os.Getpid()))
	unlock, err := AcquirePidLock(path)
	if err != nil {
		return func() {}
	}
	return func() {
		unlock()
		_ = os.Remove(path)
	}
}
