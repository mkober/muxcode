package bus

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrNoConsumer reports a message refused because nothing can read the inbox
// it is addressed to (MUX-145) — see RoleHasConsumer.
var ErrNoConsumer = errors.New("no consumer for the role's inbox")

// RoleHasConsumer reports whether anything can read role's inbox, role being
// the inbox a send lands in (WindowForRole of the addressee). A consumer is a
// window for the role, a live inbox listener (--poll or --wait), or a running
// headless agent — the harness, whose PID marker the prompt agent also writes
// (PromptAgentAlive); being enabled is configuration, not a reader. An
// unreadable or empty window list — no tmux server, or no such session yet, as
// at session launch before the first window exists; a live session always has
// one — is indeterminate and counts as a consumer, so a tmux blip never
// refuses a session's sends.
//
// A spawn worker always counts: its seed is sent before its window exists and
// the spawn registry owns its lifecycle. A mode role (research, auto) gets no
// such exception — the user's decision of 2026-11-16 — so one never cycled to
// is refused; mode cycling swaps whole windows, so its named hold window
// exists from the first cycle on, shown or held.
//
// MUX-180 reuses this for notify suppression and workflow routing.
func RoleHasConsumer(session, role string) bool {
	if session == "" || IsSpawnRole(role) {
		return true
	}
	if IsPolling(session, role) || IsWaiting(session, role) || IsHarnessActive(session, role) {
		return true
	}
	names, err := TmuxListWindowNames(session)
	return err != nil || len(names) == 0 || RoleHasWindow(names, role)
}

// noConsumerMarkerPath marks a role whose sends are being refused, so the
// refusal's lifecycle row is written once per episode.
func noConsumerMarkerPath(session, role string) string {
	return filepath.Join(BusDir(session), "no-consumer-"+role+".marker")
}

// consumerGuardEnv set to 0 turns the refusal off — a rollback valve, and the
// opt-out for integration fixtures that play an agent by reading its inbox
// from a script, which no window, listener or headless agent shows.
const consumerGuardEnv = "MUXCODE_CONSUMER_GUARD"

// refuseWithoutConsumer returns an ErrNoConsumer error when nothing can read
// inboxRole, else nil. A self-send — the startup bootstrap, the one that
// reaches Send — is never refused. The send-no-consumer row and the stderr
// line are written once per role while the condition holds (the caller's own
// error carries every refusal): the marker is created by the first
// refusal and removed by the first send that finds a consumer, because the
// daemon's analyze route is refused every edit-stabilization cycle and a row
// per message would be MUX-180's noise under a new name.
func refuseWithoutConsumer(session string, m Message, inboxRole string) error {
	if os.Getenv(consumerGuardEnv) == "0" {
		return nil
	}
	if m.From == m.To || RoleHasConsumer(session, inboxRole) {
		_ = os.Remove(noConsumerMarkerPath(session, inboxRole))
		return nil
	}
	if f, err := os.OpenFile(noConsumerMarkerPath(session, inboxRole), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644); err == nil {
		_ = f.Close()
		LogLifecycle(session, "warn", "bus", "send-no-consumer", fmt.Sprintf(
			"%s: no window, inbox listener or headless agent — refusing messages to it until one exists (first: %s→%s:%s)",
			inboxRole, m.From, m.To, m.Action))
		fmt.Fprintf(os.Stderr, "  [send] REFUSED %s→%s:%s — %s has no consumer (logged once until one exists)\n",
			m.From, m.To, m.Action, inboxRole)
	}
	return fmt.Errorf("%s→%s:%s: %s has no window, inbox listener or headless agent: %w",
		m.From, m.To, m.Action, inboxRole, ErrNoConsumer)
}
