package bus

import (
	"errors"
	"fmt"
)

// ErrResumeRefused wraps every refusal ResumeAgent returns before touching the
// pane, so a caller can tell "nothing was typed" from a failed relaunch.
var ErrResumeRefused = errors.New("resume refused")

// resumeAgentAlive and resumeStopAgent stand in for the real-tmux liveness
// probe and graceful exit in tests.
var (
	resumeAgentAlive = IsAgentAlive
	resumeStopAgent  = func(session, role string) error { return GracefulStop(session, role, false) }
)

// ResumeAgent relaunches role's Claude Code agent into the session its pane's
// exit banner offers — `muxcode resume <role>`, the manual form of the
// daemon's RestartLocalAgent, sharing its scrape-then-relaunch body. It is not
// `muxcode session resume`, which restores memory summaries.
//
// Refused, before any keystroke and wrapping ErrResumeRefused: an unknown role;
// a hosted role (docs, pr-read), whose pane belongs to its host — resuming it
// would probe the hosted role's provider and marker while typing into the
// host's pane, so the refusal names the host's resume instead; a role with no
// window; a non-Claude provider, since a command named resume
// must not silently start fresh (`muxcode reload` is that road); a reload
// already in progress; and a live agent unless force. With force a live agent
// is exited first so Claude draws the banner the scrape reads — a live TUI
// shows none. A pane with no usable banner relaunches fresh with the full flag
// set, never a flagless resume.
//
// The role's reload marker is held for the whole relaunch so the daemon's
// health sweep cannot restart the same pane concurrently, and is removed on
// every return path. Lifecycle rows carry source "manual" and the actor.
func ResumeAgent(session, role string, force bool, actor string) error {
	if !IsKnownRole(role) {
		return fmt.Errorf("%w: unknown role %q", ErrResumeRefused, role)
	}
	if IsHostedRole(role) {
		host := WindowForRole(role)
		return fmt.Errorf("%w: %s runs inside %s's pane — use `muxcode resume %s`", ErrResumeRefused, role, host, host)
	}
	names, err := TmuxListWindowNames(session)
	if err != nil {
		return fmt.Errorf("%w: cannot list windows in session %s: %v", ErrResumeRefused, session, err)
	}
	if !RoleWindowPresent(names, role) {
		return fmt.Errorf("%w: role %s has no window in session %s", ErrResumeRefused, role, session)
	}
	if !IsClaudeTUI(ResolveProvider(role)) {
		return fmt.Errorf("%w: %s runs on %s, which has no session to resume — use `muxcode reload %s` for a fresh start",
			ErrResumeRefused, role, ResolveProviderCLI(role), role)
	}
	if IsReloading(session, role) {
		return fmt.Errorf("%w: a reload of %s is in progress", ErrResumeRefused, role)
	}
	alive := resumeAgentAlive(session, role)
	if alive && !force {
		return fmt.Errorf("%w: %s is running — resume relaunches a dead agent; pass --force to exit it first", ErrResumeRefused, role)
	}

	if err := writeReloadMarker(session, role); err != nil {
		return fmt.Errorf("holding off the health sweep: %w", err)
	}
	defer clearReloadMarker(session, role)

	if alive {
		if err := resumeStopAgent(session, role); err != nil {
			return fmt.Errorf("exiting live %s before resume: %w", role, err)
		}
	}
	return scrapeAndRelaunch(session, role, ReloadTarget(session, role), "manual", actor)
}
