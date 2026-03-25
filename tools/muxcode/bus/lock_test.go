package bus

import "testing"

func TestLockUnlock(t *testing.T) {
	session := testSession(t)

	if err := Lock(session, "build"); err != nil {
		t.Fatalf("Lock: %v", err)
	}
	if !IsLocked(session, "build") {
		t.Error("expected locked after Lock")
	}

	if err := Unlock(session, "build"); err != nil {
		t.Fatalf("Unlock: %v", err)
	}
	if IsLocked(session, "build") {
		t.Error("expected unlocked after Unlock")
	}
}

func TestUnlock_NotLocked(t *testing.T) {
	session := testSession(t)

	if err := Unlock(session, "build"); err != nil {
		t.Errorf("Unlock when not locked: %v", err)
	}
}

func TestIsLocked_NoSession(t *testing.T) {
	if IsLocked("nonexistent-session-xyz", "build") {
		t.Error("expected false for nonexistent session")
	}
}
