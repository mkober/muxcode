package daemon

import (
	"testing"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// autoInboxCount returns how many messages are queued for the auto role.
func autoInboxCount(t *testing.T, session string) int {
	t.Helper()
	msgs, err := bus.Peek(session, "auto")
	if err != nil {
		t.Fatalf("Peek auto: %v", err)
	}
	return len(msgs)
}

// The default window set does not include auto, so most sessions have no auto
// agent. Heartbeating one anyway leaves an un-consumed request that trips the
// receipt-gap backstop and draws force-deliver retries that cannot succeed
// against a window that isn't there.
func TestCheckHeartbeat_SkipsWhenNoAutoWindow(t *testing.T) {
	session := testSession(t)
	d := New(session, 5, 8)
	d.heartbeatInterval = 1800
	d.lastHeartbeatCheck = 0 // clear interval guard so it would otherwise fire
	d.windowExists = func(_, _ string) bool { return false }

	d.checkHeartbeat()

	if n := autoInboxCount(t, session); n != 0 {
		t.Errorf("heartbeat must not fire at a role with no window; auto inbox = %d", n)
	}
}

func TestCheckHeartbeat_FiresWhenAutoWindowExists(t *testing.T) {
	session := testSession(t)
	d := New(session, 5, 8)
	d.heartbeatInterval = 1800
	d.lastHeartbeatCheck = 0
	d.windowExists = func(_, _ string) bool { return true }

	d.checkHeartbeat()

	msgs, err := bus.Peek(session, "auto")
	if err != nil {
		t.Fatalf("Peek auto: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 heartbeat message, got %d", len(msgs))
	}
	if msgs[0].Action != "heartbeat" {
		t.Errorf("expected action heartbeat, got %q", msgs[0].Action)
	}
}

// The interval guard still applies once a window exists.
func TestCheckHeartbeat_RespectsInterval(t *testing.T) {
	session := testSession(t)
	d := New(session, 5, 8)
	d.heartbeatInterval = 1800
	d.lastHeartbeatCheck = 0
	d.windowExists = func(_, _ string) bool { return true }

	d.checkHeartbeat() // fires
	d.checkHeartbeat() // within interval — must not fire again

	if n := autoInboxCount(t, session); n != 1 {
		t.Errorf("expected exactly 1 heartbeat within the interval, got %d", n)
	}
}

// Disabled heartbeat (interval 0) never fires, window or not.
func TestCheckHeartbeat_DisabledWhenIntervalZero(t *testing.T) {
	session := testSession(t)
	d := New(session, 5, 8)
	d.heartbeatInterval = 0
	d.lastHeartbeatCheck = 0
	d.windowExists = func(_, _ string) bool { return true }

	d.checkHeartbeat()

	if n := autoInboxCount(t, session); n != 0 {
		t.Errorf("heartbeat must not fire when disabled; auto inbox = %d", n)
	}
}
