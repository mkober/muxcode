package bus

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Restart verification statuses. Preparing, pending and stop-pending are owned
// by the daemon; the rest are terminal. Preparing covers the stop of a live
// agent before its relaunch: the record exists before the agent is stopped,
// so a restarter that dies between the stop and the relaunch leaves the daemon
// a record rather than a silently dead agent. A preparing record never armed
// within restartPrepareTimeoutSecs is abandoned, which hands the role back to
// the health sweep. Verified, stopped and exited are the outcomes the
// restarting CLI reads back.
const (
	RestartVerifyPreparing   = "preparing"
	RestartVerifyPending     = "pending"
	RestartVerifyStopPending = "stop-pending"
	RestartVerifyVerified    = "verified"
	RestartVerifyStopped     = "stopped"
	RestartVerifyExited      = "exited"
	RestartVerifyAbandoned   = "abandoned"
)

// Restart verification timing: the daemon accepts a relaunch once its pane has
// shown a ready Claude composer, with no definition-unavailable banner, for
// RestartVerifySettleSecs; one still unproven after RestartVerifyTimeoutSecs is
// stopped. Terminal records nobody collected are pruned after
// restartVerifyPruneSecs. The stop a preparing record covers takes at most
// ~12s (GracefulStop), so one still unarmed after restartPrepareTimeoutSecs
// belongs to a restarter that died.
const (
	RestartVerifySettleSecs   = 5
	RestartVerifyTimeoutSecs  = 90
	restartVerifyPruneSecs    = 600
	restartPrepareTimeoutSecs = 60
)

// RestartVerification is the durable record of an operator-restarted Claude
// agent's definition check (MUX-139 Phase 5). It lives on disk, not in the
// restarting process, because that process is a TUI modal or a CLI call that
// can be closed or killed mid-restart: written before anything is typed, it
// hands the check to the daemon, which owns it until a terminal status —
// retrying a failed containment stop every tick — whether or not the
// restarting process is still there to read the outcome.
type RestartVerification struct {
	Role         string `json:"role"`
	Status       string `json:"status"`
	RelaunchedAt int64  `json:"relaunched_at"`
	ReadyAt      int64  `json:"ready_at,omitempty"`
	UpdatedAt    int64  `json:"updated_at"`
	Detail       string `json:"detail,omitempty"`
}

// Active reports whether the daemon still owns the record.
func (v RestartVerification) Active() bool {
	return v.Status == RestartVerifyPreparing || v.Status == RestartVerifyPending || v.Status == RestartVerifyStopPending
}

func restartVerifyDir(session string) string {
	return filepath.Join(BusDir(session), "restart-verify")
}

func restartVerifyPath(session, role string) string {
	return filepath.Join(restartVerifyDir(session), role+".json")
}

// WriteRestartVerification stores v atomically, stamping UpdatedAt.
func WriteRestartVerification(session string, v RestartVerification) error {
	if err := os.MkdirAll(restartVerifyDir(session), 0o755); err != nil {
		return err
	}
	v.UpdatedAt = time.Now().Unix()
	return atomicWriteJSON(restartVerifyPath(session, v.Role), v)
}

// ReadRestartVerification returns role's record; ok is false when none exists.
func ReadRestartVerification(session, role string) (v RestartVerification, ok bool, err error) {
	data, err := os.ReadFile(restartVerifyPath(session, role))
	if errors.Is(err, os.ErrNotExist) {
		return v, false, nil
	}
	if err != nil {
		return v, false, err
	}
	if err := json.Unmarshal(data, &v); err != nil {
		return v, false, fmt.Errorf("restart verification for %s: %w", role, err)
	}
	return v, true, nil
}

// ListRestartVerifications returns every readable record in the session.
func ListRestartVerifications(session string) []RestartVerification {
	entries, err := os.ReadDir(restartVerifyDir(session))
	if err != nil {
		return nil
	}
	var out []RestartVerification
	for _, e := range entries {
		role, isJSON := strings.CutSuffix(e.Name(), ".json")
		if !isJSON {
			continue
		}
		if v, ok, err := ReadRestartVerification(session, role); err == nil && ok {
			out = append(out, v)
		}
	}
	return out
}

// RestartVerificationActive reports whether the daemon owns role's
// post-restart check — the health sweep leaves such a role to it.
func RestartVerificationActive(session, role string) bool {
	v, ok, err := ReadRestartVerification(session, role)
	return err == nil && ok && v.Active()
}

// ClearRestartVerification removes role's record.
func ClearRestartVerification(session, role string) {
	_ = os.Remove(restartVerifyPath(session, role))
}

// RestartVerificationPrunable reports whether a terminal record has gone
// uncollected long enough to delete.
func RestartVerificationPrunable(v RestartVerification, now int64) bool {
	return !v.Active() && now-v.UpdatedAt >= restartVerifyPruneSecs
}

// AdvanceRestartVerification is one daemon step over a pending record, given
// the agent's liveness and a pane capture (or its error). It returns the next
// record and whether the agent must be stopped; the caller performs the stop
// and records stopped or stop-pending. Every road to "verified" needs positive
// evidence — a ready composer drawn since the last exit, banner-free for the
// settle period — so startup text, a blank pane or a failed capture only wait,
// and a relaunch still unproven at the timeout is stopped, never passed. A
// banner at any point stops it. An agent dead at the timeout has nothing left
// running to contain and closes as exited.
//
// A preparing record has nothing relaunched to check: it only waits, timed
// from UpdatedAt, and is abandoned once restartPrepareTimeoutSecs pass unarmed.
func AdvanceRestartVerification(v RestartVerification, now int64, alive bool, content string, captureErr error) (RestartVerification, bool) {
	switch v.Status {
	case RestartVerifyStopPending:
		return v, true
	case RestartVerifyPreparing:
		if now-v.UpdatedAt >= restartPrepareTimeoutSecs {
			v.Status = RestartVerifyAbandoned
			v.Detail = "restarter never armed the check after stopping the agent — handed back to the health sweep"
		}
		return v, false
	}
	timedOut := now-v.RelaunchedAt >= RestartVerifyTimeoutSecs
	switch {
	case captureErr != nil:
		v.Detail = "pane capture failed: " + captureErr.Error()
		v.ReadyAt = 0
		if timedOut {
			v.Detail = "pane unreadable for the whole verification window — " + v.Detail
			return v, true
		}
		return v, false
	case PaneShowsDefinitionlessAgent(SinceLastAgentExit(content)):
		v.Detail = "definition-unavailable banner after operator restart"
		return v, true
	case !alive:
		v.ReadyAt = 0
		if timedOut {
			v.Status = RestartVerifyExited
			v.Detail = "agent not running at the end of the verification window"
		}
		return v, false
	case !resumedSessionReady(content):
		v.ReadyAt = 0
		if timedOut {
			v.Detail = "no ready Claude session within the verification window"
			return v, true
		}
		return v, false
	}
	if v.ReadyAt == 0 {
		v.ReadyAt = now
	}
	if now-v.ReadyAt >= RestartVerifySettleSecs {
		v.Status = RestartVerifyVerified
		v.Detail = ""
	}
	return v, false
}

// Errors for a restart whose definition check did not end verified.
var (
	// ErrRestartedUnrestricted: the relaunch could not be proven to carry its
	// definition and the agent was stopped (MUX-136).
	ErrRestartedUnrestricted = errors.New("restarted without a verified agent definition — stopped")
	// ErrRestartStopPending: as above, but stopping it has not succeeded yet —
	// it may still be running; the daemon keeps retrying.
	ErrRestartStopPending = errors.New("restarted without a verified agent definition — stop FAILED, may still be running; the daemon keeps retrying")
	// ErrRestartVerifyPending: the outcome was not in by the wait deadline;
	// the daemon still owns the check.
	ErrRestartVerifyPending = errors.New("definition check still pending — handed to the daemon")
)

// awaitRestartVerification waits for the daemon's verdict on role's record
// and collects a terminal one. Active at the deadline, the record is left for
// the daemon and an error says so — a pass is reported only for "verified".
func awaitRestartVerification(session, role string) error {
	deadline := time.Now().Add(restartVerifyWait)
	for {
		v, ok, err := ReadRestartVerification(session, role)
		switch {
		case err != nil:
			return fmt.Errorf("reading %s's restart verification: %w", role, err)
		case !ok:
			return fmt.Errorf("%s's restart verification record vanished before an outcome", role)
		case v.Status == RestartVerifyVerified:
			ClearRestartVerification(session, role)
			return nil
		case v.Status == RestartVerifyStopped:
			ClearRestartVerification(session, role)
			return fmt.Errorf("%w: %s (%s)", ErrRestartedUnrestricted, role, v.Detail)
		case v.Status == RestartVerifyExited:
			ClearRestartVerification(session, role)
			return fmt.Errorf("agent %s did not stay up after restart (%s)", role, v.Detail)
		case !time.Now().Before(deadline) && v.Status == RestartVerifyStopPending:
			return fmt.Errorf("%w: %s (%s)", ErrRestartStopPending, role, v.Detail)
		case !time.Now().Before(deadline):
			return fmt.Errorf("%w: %s", ErrRestartVerifyPending, role)
		}
		time.Sleep(restartVerifyPoll)
	}
}
