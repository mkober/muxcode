package bus

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// InboxCap is the most unconsumed messages a role's inbox holds — the user's
// Decision 1 for MUX-180. A healthy role never meets it: its consumer drains
// the inbox. It bounds what the consumer check (RoleHasConsumer) passes or
// cannot decide: a window whose agent is dead or wedged, a consumer that went
// away after messages queued, an unreadable window list. On 2026-09-11 a
// windowless analyze held 109.
const InboxCap = 50

// ErrInboxFull reports a message not written because the inbox it was bound
// for already holds InboxCap unconsumed messages. Nothing queued was dropped.
var ErrInboxFull = errors.New("inbox at its cap")

// appendCapped is the only road a message takes into an inbox — the
// addressee's and the auto-CC copy's alike. Under the inbox's capacity lock
// it counts, refuses at InboxCap (ErrInboxFull) or appends, so concurrent
// writers in any process can never take an inbox past the cap; the count is
// read under the same lock that guards the append. Every message type and
// every sender is bound, the startup bootstrap included — a full inbox is
// drained by its consumer once the agent is up, not widened for it.
//
// The episode is managed under the same lock, so it stays in step with the
// count: an append that finds the inbox below the cap ends any inbox-full
// episode — 50, one consumed, 50 again is a new episode — and reaching or
// meeting the cap opens one, writing its row once. Only the alert to edit is
// sent after the lock is released, since it is itself a send.
//
// The lock is required: if it cannot be taken nothing is written and the
// error is returned, because the cap is a hard bound, not a heuristic.
func appendCapped(session, role string, line []byte) error {
	n, opened, err := appendUnderCap(session, role, line)
	if opened {
		alertEditInboxFull(session, role, n)
	}
	return err
}

// appendUnderCap appends line to role's inbox unless it already holds
// InboxCap messages, returning the count after the attempt and whether this
// call opened an inbox-full episode.
func appendUnderCap(session, role string, line []byte) (n int, opened bool, err error) {
	unlock, err := lockInboxCap(session, role)
	if err != nil {
		return 0, false, fmt.Errorf("%s's inbox capacity lock: %w", role, err)
	}
	defer unlock()
	n = InboxCount(session, role)
	if n >= InboxCap {
		return n, openInboxFullEpisode(session, role, n),
			fmt.Errorf("%s's inbox holds %d unconsumed messages, its cap: %w", role, n, ErrInboxFull)
	}
	EndEpisode(session, role, "inbox-full")
	if err := appendToFile(InboxPath(session, role), line); err != nil {
		return n, false, err
	}
	n++
	if n >= InboxCap {
		return n, openInboxFullEpisode(session, role, n), nil
	}
	return n, false, nil
}

// withInboxCapLock runs fn holding role's capacity lock. A partial consume
// takes the inbox away and writes the unmatched rest back; holding the lock
// across both keeps a send from being admitted against the emptied inbox and
// the restore then taking it past the cap. It fails closed like a send.
func withInboxCapLock(session, role string, fn func() error) error {
	unlock, err := lockInboxCap(session, role)
	if err != nil {
		return fmt.Errorf("%s's inbox capacity lock: %w", role, err)
	}
	defer unlock()
	return fn()
}

// lockInboxCap takes role's inbox capacity lock, beside the inbox rather than
// on it: appendToFile and Receive open the inbox itself.
func lockInboxCap(session, role string) (func(), error) {
	dir := filepath.Join(BusDir(session), "lock")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "inbox-cap-"+role+".lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}

// openInboxFullEpisode writes the inbox-full row once per episode, reporting
// whether this call opened it. Called under the capacity lock.
func openInboxFullEpisode(session, role string, n int) bool {
	return NoteEpisode(session, role, "inbox-full", fmt.Sprintf(
		"%s: %d unconsumed messages, the cap of %d — messages to it are refused until it drains", role, n, InboxCap))
}

// alertEditInboxFull tells edit once per episode that role's inbox is full.
// Edit's own full inbox gets the row alone: a message about it would meet the
// cap it reports.
func alertEditInboxFull(session, role string, n int) {
	if role == "edit" {
		return
	}
	_ = SendNoCC(session, NewMessage("daemon", "edit", "event", "inbox-full", fmt.Sprintf(
		"%s's inbox holds %d unconsumed messages, its cap — messages to it are refused until its consumer drains it. Check the agent: muxcode diagnose %s",
		role, n, role), ""))
}
