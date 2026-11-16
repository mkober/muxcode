package bus

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func fillInbox(t *testing.T, session, from, to string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if err := SendNoCC(session, NewMessage(from, to, "event", "note", "queued "+strconv.Itoa(i), "")); err != nil {
			t.Fatalf("send %d of %d to %s: %v", i+1, n, to, err)
		}
	}
}

func inboxFullAlerts(t *testing.T, session string) int {
	t.Helper()
	n := 0
	for _, m := range peekAll(t, session, "edit") {
		if m.Action == "inbox-full" {
			n++
		}
	}
	return n
}

// TestInboxCapRefusesPastFifty is MUX-180 Decision 1: a role's inbox holds at
// most InboxCap unconsumed messages. Reaching the cap raises one alert — a
// row and one message to edit — and any send past it, a response included, is
// refused with ErrInboxFull while nothing queued is dropped. Once the consumer
// drains it sends are accepted again, and a second fill is a new episode.
func TestInboxCapRefusesPastFifty(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)
	t.Setenv("MUXCODE_LIFECYCLE_LOG_DIR", t.TempDir())

	fillInbox(t, session, "daemon", "build", InboxCap)
	if rows := eventRows(t, session, "inbox-full"); len(rows) != 1 || !strings.HasPrefix(rows[0], "build:") {
		t.Errorf("reaching the cap must log one inbox-full row naming build, got %q", rows)
	}
	if n := inboxFullAlerts(t, session); n != 1 {
		t.Errorf("edit got %d inbox-full alerts on reaching the cap, want 1", n)
	}

	for _, m := range []Message{
		NewMessage("edit", "build", "request", "build", "run the build", ""),
		NewMessage("test", "build", "response", "test", "suite passed", ""),
	} {
		if err := SendNoCC(session, m); !errors.Is(err, ErrInboxFull) {
			t.Errorf("a %s past the cap: err %v, want ErrInboxFull", m.Type, err)
		}
	}
	if n := InboxCount(session, "build"); n != InboxCap {
		t.Errorf("build holds %d messages, want all %d still queued and nothing more", n, InboxCap)
	}
	if rows, alerts := eventRows(t, session, "inbox-full"), inboxFullAlerts(t, session); len(rows) != 1 || alerts != 1 {
		t.Errorf("refusals past the cap must not alert again: %d rows, %d alerts", len(rows), alerts)
	}

	if got, err := Receive(session, "build"); err != nil || len(got) != InboxCap {
		t.Fatalf("the consumer must receive all %d queued messages, got %d (err %v)", InboxCap, len(got), err)
	}
	if err := SendNoCC(session, NewMessage("edit", "build", "request", "build", "run the build", "")); err != nil {
		t.Fatalf("once drained the inbox must accept sends again: %v", err)
	}
	fillInbox(t, session, "daemon", "build", InboxCap-1)
	if rows := eventRows(t, session, "inbox-full"); len(rows) != 2 {
		t.Errorf("a second fill is a new episode: %d inbox-full rows, want 2", len(rows))
	}
}

// TestInboxCapAutoCCFillsEditAndAlerts (MUX-180 review): the auto-CC copy goes
// through the same capacity step as a direct send, so edit filled to the cap
// by CC copies alone logs its one inbox-full row — no alert message to itself
// — and after a drain, a refill by CC alone is a new episode with a second row.
func TestInboxCapAutoCCFillsEditAndAlerts(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)
	t.Setenv("MUXCODE_LIFECYCLE_LOG_DIR", t.TempDir())
	saved := autoCCLastSent
	t.Cleanup(func() { autoCCLastSent = saved })
	targets := []string{"test", "review", "deploy", "run"}
	sent := 0
	ccFill := func(n int) {
		for i := 0; i < n; i++ {
			autoCCLastSent = make(map[string]int64)
			to := targets[sent%len(targets)]
			sent++
			if err := Send(session, NewMessage("build", to, "event", "note", "cc "+strconv.Itoa(sent), "")); err != nil {
				t.Fatalf("send %d: %v", sent, err)
			}
		}
	}

	ccFill(InboxCap)
	if n := InboxCount(session, "edit"); n != InboxCap {
		t.Fatalf("edit holds %d after %d CC copies, want %d", n, InboxCap, InboxCap)
	}
	if rows := eventRows(t, session, "inbox-full"); len(rows) != 1 || !strings.HasPrefix(rows[0], "edit:") || inboxFullAlerts(t, session) != 0 {
		t.Errorf("want one inbox-full row naming edit and no alert to itself, got %q", rows)
	}
	ccFill(3)
	if n := InboxCount(session, "edit"); n != InboxCap {
		t.Errorf("CC copies took edit past its cap: %d", n)
	}

	if _, err := Receive(session, "edit"); err != nil {
		t.Fatal(err)
	}
	ccFill(InboxCap)
	if rows := eventRows(t, session, "inbox-full"); len(rows) != 2 {
		t.Errorf("a refill by CC alone after a drain is a new episode: %d edit rows, want 2", len(rows))
	}
}

// TestInboxCapHoldsUnderConcurrency (MUX-180 review): count and append happen
// under the inbox's capacity lock, so concurrent writers can never take an
// inbox past the cap. Ten senders racing for build's last two slots land
// exactly two; direct sends and CC copies racing for edit's last slot land
// exactly one; ten senders below the cap all land; nothing queued is lost.
func TestInboxCapHoldsUnderConcurrency(t *testing.T) {
	race := func(n int, send func(i int) error) (ok, full int) {
		errs := make(chan error, n)
		var wg sync.WaitGroup
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) { defer wg.Done(); errs <- send(i) }(i)
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			switch {
			case err == nil:
				ok++
			case errors.Is(err, ErrInboxFull):
				full++
			default:
				t.Errorf("unexpected send error: %v", err)
			}
		}
		return ok, full
	}

	t.Run("last two slots, direct senders", func(t *testing.T) {
		useTempBusDir(t)
		session := testSession(t)
		fillInbox(t, session, "daemon", "build", InboxCap-2)
		ok, full := race(10, func(i int) error {
			return SendNoCC(session, NewMessage("daemon", "build", "event", "note", "race "+strconv.Itoa(i), ""))
		})
		if ok != 2 || full != 8 || InboxCount(session, "build") != InboxCap {
			t.Errorf("landed %d, refused %d, build holds %d; want 2, 8 and %d", ok, full, InboxCount(session, "build"), InboxCap)
		}
	})
	t.Run("last slot, direct sends and CC copies", func(t *testing.T) {
		useTempBusDir(t)
		session := testSession(t)
		saved := autoCCLastSent
		t.Cleanup(func() { autoCCLastSent = saved })
		autoCCLastSent = make(map[string]int64)
		fillInbox(t, session, "plan", "edit", InboxCap-1)
		ccFrom := []string{"build", "test", "review"}
		race(8, func(i int) error {
			if i < len(ccFrom) {
				return Send(session, NewMessage(ccFrom[i], "run", "event", "note", "cc race "+strconv.Itoa(i), ""))
			}
			return SendNoCC(session, NewMessage("plan", "edit", "event", "note", "direct race "+strconv.Itoa(i), ""))
		})
		if n := InboxCount(session, "edit"); n != InboxCap {
			t.Errorf("edit holds %d after the race, want exactly %d", n, InboxCap)
		}
		if n := InboxCount(session, "run"); n != len(ccFrom) {
			t.Errorf("run holds %d, want every original (%d) delivered", n, len(ccFrom))
		}
	})
	t.Run("below the cap, all land", func(t *testing.T) {
		useTempBusDir(t)
		session := testSession(t)
		fillInbox(t, session, "daemon", "build", 10)
		ok, _ := race(10, func(i int) error {
			return SendNoCC(session, NewMessage("daemon", "build", "event", "note", "below "+strconv.Itoa(i), ""))
		})
		if ok != 10 || InboxCount(session, "build") != 20 {
			t.Errorf("landed %d of 10 below the cap, build holds %d", ok, InboxCount(session, "build"))
		}
	})
}

// TestInboxCapHoldsTheLock is the proof that count and append are one locked
// step: a send to an inbox waits while another holder has that inbox's
// capacity lock and lands once it is released, while a send to a different
// inbox does not wait. In-process races are too narrow to fail reliably
// without the lock, so TestInboxCapHoldsUnderConcurrency pins outcomes.
func TestInboxCapHoldsTheLock(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)
	unlock, err := lockInboxCap(session, "build")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- SendNoCC(session, NewMessage("daemon", "build", "event", "note", "waits", "")) }()
	select {
	case err := <-done:
		unlock()
		t.Fatalf("a send to build completed (%v) while its capacity lock was held", err)
	case <-time.After(300 * time.Millisecond):
	}
	if err := SendNoCC(session, NewMessage("daemon", "test", "event", "note", "another inbox", "")); err != nil {
		t.Errorf("a send to another inbox must not wait: %v", err)
	}
	unlock()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("the send failed once the lock was released: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the send never completed after the lock was released")
	}
}

// TestInboxCapPartialConsumeIsCoordinated (MUX-180 review): a partial consume
// takes the inbox away and writes the rest back, so it holds the same
// capacity lock as a send — it waits while the lock is held, exactly as a send
// does. Racing sends during a partial consume never take a full inbox past the
// cap nor lose a queued message; a full drain lets sends in again.
func TestInboxCapPartialConsumeIsCoordinated(t *testing.T) {
	t.Run("waits on the capacity lock", func(t *testing.T) {
		useTempBusDir(t)
		session := testSession(t)
		fillInbox(t, session, "daemon", "build", 3)
		target := peekAll(t, session, "build")[0].ID
		unlock, err := lockInboxCap(session, "build")
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 2)
		go func() { _, err := ConsumeByID(session, "build", target); done <- err }()
		go func() {
			_, err := ReceiveMatchingFunc(session, "build", func(m Message) bool { return m.Payload == "queued 1" })
			done <- err
		}()
		select {
		case <-done:
			unlock()
			t.Fatal("a partial consume completed while the capacity lock was held")
		case <-time.After(300 * time.Millisecond):
		}
		unlock()
		for i := 0; i < 2; i++ {
			select {
			case err := <-done:
				if err != nil {
					t.Errorf("partial consume failed once the lock was released: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("a partial consume never completed after the lock was released")
			}
		}
	})
	t.Run("racing sends never overflow", func(t *testing.T) {
		useTempBusDir(t)
		session := testSession(t)
		fillInbox(t, session, "daemon", "build", InboxCap)
		queued := peekAll(t, session, "build")
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = ReceiveMatchingFunc(session, "build", func(m Message) bool { return m.ID == queued[0].ID })
		}()
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				_ = SendNoCC(session, NewMessage("daemon", "build", "event", "note", "racer "+strconv.Itoa(i), ""))
			}(i)
		}
		wg.Wait()
		after := peekAll(t, session, "build")
		if len(after) > InboxCap {
			t.Fatalf("build holds %d after a partial consume raced sends, past the cap", len(after))
		}
		have := map[string]bool{}
		for _, m := range after {
			have[m.ID] = true
		}
		for _, m := range queued[1:] {
			if !have[m.ID] {
				t.Errorf("queued message %s was lost", m.ID)
			}
		}
	})
	t.Run("full drain lets sends in again", func(t *testing.T) {
		useTempBusDir(t)
		session := testSession(t)
		fillInbox(t, session, "daemon", "build", InboxCap)
		if _, err := Receive(session, "build"); err != nil {
			t.Fatal(err)
		}
		fillInbox(t, session, "daemon", "build", InboxCap)
	})
}

// TestInboxCapFailsClosedWithoutTheLock (MUX-180 review): the cap is a hard
// bound, so a capacity lock that cannot be taken writes nothing — a send and a
// partial consume both fail, not ErrInboxFull — and once the obstruction is
// gone the same send is admitted.
func TestInboxCapFailsClosedWithoutTheLock(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)
	fillInbox(t, session, "daemon", "build", 1)
	held := peekAll(t, session, "build")[0].ID
	obstruction := filepath.Join(BusDir(session), "lock", "inbox-cap-build.lock")
	_ = os.Remove(obstruction) // the lock file the fill created; a directory takes its place
	if err := os.MkdirAll(obstruction, 0o755); err != nil {
		t.Fatal(err)
	}
	err := SendNoCC(session, NewMessage("daemon", "build", "event", "note", "blocked", ""))
	if err == nil || errors.Is(err, ErrInboxFull) {
		t.Errorf("a send with no capacity lock: err %v, want a lock error", err)
	}
	if _, err := ConsumeByID(session, "build", held); err == nil {
		t.Error("a partial consume with no capacity lock must fail, not run unlocked")
	}
	if n := InboxCount(session, "build"); n != 1 {
		t.Errorf("build holds %d, want the one queued message and nothing written", n)
	}
	if err := os.Remove(obstruction); err != nil {
		t.Fatal(err)
	}
	if err := SendNoCC(session, NewMessage("daemon", "build", "event", "note", "admitted", "")); err != nil {
		t.Errorf("with the lock available again the send must land: %v", err)
	}
}

// TestInboxCapEpisodeResetsAfterOneConsumed (MUX-180 review): a full inbox
// that loses one message and fills again is a new episode — a second row and,
// for a role other than edit, a second alert — whether it refills by direct
// sends or by auto-CC copies; repeated attempts while it stays full add none.
func TestInboxCapEpisodeResetsAfterOneConsumed(t *testing.T) {
	t.Run("direct sends", func(t *testing.T) {
		useTempBusDir(t)
		session := testSession(t)
		t.Setenv("MUXCODE_LIFECYCLE_LOG_DIR", t.TempDir())
		fillInbox(t, session, "daemon", "build", InboxCap)
		for i := 0; i < 3; i++ {
			_ = SendNoCC(session, NewMessage("daemon", "build", "event", "note", "at the cap", ""))
		}
		if rows, alerts := eventRows(t, session, "inbox-full"), inboxFullAlerts(t, session); len(rows) != 1 || alerts != 1 {
			t.Fatalf("attempts at the cap must not alert again: %d rows, %d alerts", len(rows), alerts)
		}
		if _, err := ConsumeByID(session, "build", peekAll(t, session, "build")[0].ID); err != nil {
			t.Fatal(err)
		}
		fillInbox(t, session, "daemon", "build", 1)
		if rows, alerts := eventRows(t, session, "inbox-full"), inboxFullAlerts(t, session); len(rows) != 2 || alerts != 2 {
			t.Errorf("50, one consumed, 50 again is a new episode: %d rows, %d alerts, want 2 and 2", len(rows), alerts)
		}
	})
	t.Run("auto-CC copies", func(t *testing.T) {
		useTempBusDir(t)
		session := testSession(t)
		t.Setenv("MUXCODE_LIFECYCLE_LOG_DIR", t.TempDir())
		saved := autoCCLastSent
		t.Cleanup(func() { autoCCLastSent = saved })
		cc := func(i int) {
			autoCCLastSent = make(map[string]int64)
			if err := Send(session, NewMessage("build", "run", "event", "note", "cc "+strconv.Itoa(i), "")); err != nil {
				t.Fatal(err)
			}
		}
		fillInbox(t, session, "plan", "edit", InboxCap-1)
		cc(0)
		if rows := eventRows(t, session, "inbox-full"); len(rows) != 1 {
			t.Fatalf("CC filling edit: %d rows, want 1", len(rows))
		}
		if _, err := ConsumeByID(session, "edit", peekAll(t, session, "edit")[0].ID); err != nil {
			t.Fatal(err)
		}
		cc(1)
		if rows := eventRows(t, session, "inbox-full"); len(rows) != 2 {
			t.Errorf("edit 50, one consumed, refilled by CC: %d rows, want 2", len(rows))
		}
	})
}

// TestInboxCapEditAlertsItselfByRowOnly: edit's own full inbox is logged, but
// no alert message is sent to it — the message would meet the cap it reports.
func TestInboxCapEditAlertsItselfByRowOnly(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)
	t.Setenv("MUXCODE_LIFECYCLE_LOG_DIR", t.TempDir())
	fillInbox(t, session, "plan", "edit", InboxCap)
	if rows := eventRows(t, session, "inbox-full"); len(rows) != 1 || !strings.HasPrefix(rows[0], "edit:") {
		t.Errorf("want one inbox-full row naming edit, got %q", rows)
	}
	if n, alerts := InboxCount(session, "edit"), inboxFullAlerts(t, session); n != InboxCap || alerts != 0 {
		t.Errorf("edit holds %d (%d alerts), want %d and no alert to itself", n, alerts, InboxCap)
	}
}

// TestInboxCapControls: a healthy inbox below the cap writes no row; the
// startup bootstrap is bound like every send — refused by a full inbox with
// nothing queued lost, delivered below the cap — so the agent launches and
// drains what is queued; and an auto-CC copy lands while edit is below the
// cap and is skipped at it, the original still delivered.
func TestInboxCapControls(t *testing.T) {
	t.Run("healthy inbox", func(t *testing.T) {
		useTempBusDir(t)
		session := testSession(t)
		t.Setenv("MUXCODE_LIFECYCLE_LOG_DIR", t.TempDir())
		fillInbox(t, session, "daemon", "build", 10)
		if rows := eventRows(t, session, "inbox-full"); len(rows) != 0 {
			t.Errorf("an inbox of 10 logged inbox-full: %q", rows)
		}
	})
	t.Run("startup bootstrap meets the cap", func(t *testing.T) {
		useTempBusDir(t)
		session := testSession(t)
		fillInbox(t, session, "daemon", "build", InboxCap)
		if err := SendNoCC(session, NewMessage("build", "build", "request", "startup", "restore context", "")); !errors.Is(err, ErrInboxFull) {
			t.Errorf("a startup into a full inbox: err %v, want ErrInboxFull", err)
		}
		if n := InboxCount(session, "build"); n != InboxCap {
			t.Errorf("build holds %d, want the %d queued messages intact", n, InboxCap)
		}
	})
	t.Run("startup bootstrap below the cap", func(t *testing.T) {
		useTempBusDir(t)
		session := testSession(t)
		fillInbox(t, session, "daemon", "build", 10)
		if err := SendNoCC(session, NewMessage("build", "build", "request", "startup", "restore context", "")); err != nil {
			t.Errorf("a startup below the cap must land: %v", err)
		}
	})
	t.Run("auto-CC respects edit's cap", func(t *testing.T) {
		useTempBusDir(t)
		session := testSession(t)
		saved := autoCCLastSent
		t.Cleanup(func() { autoCCLastSent = saved })
		fillInbox(t, session, "plan", "edit", InboxCap-1)
		autoCCLastSent = make(map[string]int64)
		if err := Send(session, NewMessage("build", "test", "request", "test", "run the suite", "")); err != nil {
			t.Fatal(err)
		}
		if n := InboxCount(session, "edit"); n != InboxCap {
			t.Fatalf("below the cap the CC copy must land: edit holds %d, want %d", n, InboxCap)
		}
		autoCCLastSent = make(map[string]int64)
		if err := Send(session, NewMessage("build", "test", "request", "test", "run it again", "")); err != nil {
			t.Fatalf("the original must still be delivered: %v", err)
		}
		if n := InboxCount(session, "edit"); n != InboxCap {
			t.Errorf("a CC copy took edit past its cap: %d", n)
		}
		if n := InboxCount(session, "test"); n != 2 {
			t.Errorf("test holds %d, want both originals", n)
		}
	})
}
