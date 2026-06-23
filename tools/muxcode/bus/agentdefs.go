package bus

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
)

// Agent-definition tracking. The daemon's agent-defs watchdog
// (checkAgentDefs) auto-reloads a running agent when its resolved definition
// file changes on disk, so editing + reinstalling an agent definition takes
// effect without a manual `muxcode reload`.
//
// The ground truth of "what definition the running agent loaded" is stamped to
// a per-session marker file by RunAgentLaunch at the moment of (re)launch. The
// watchdog compares that stamp against the current on-disk hash. Using a stamp
// file (rather than in-memory state) means the signal survives daemon restarts
// — critical because `./build.sh` reinstalls the defs and then restarts the
// daemon via `upgrade-daemons`, so a change can land while the daemon is down.

// ResolvedAgentFileForRole returns the path of the agent definition file the
// given role launches from, using the same 3-tier resolution as launch
// (.claude/agents → ~/.config/muxcode/agents → install defaults). Returns ""
// if the role has no associated agent file or none can be resolved.
func ResolvedAgentFileForRole(role string) string {
	name := AgentFileName(role)
	if name == "" {
		return ""
	}
	path, _ := ResolveAgentFile(name, resolveInstallDir())
	return path
}

// AgentDefHash returns the SHA-256 hex hash of the role's resolved agent
// definition file contents. Returns "" if the file can't be resolved or read.
func AgentDefHash(role string) string {
	path := ResolvedAgentFileForRole(role)
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

// agentDefHashPath returns the per-session marker file that records the
// definition hash the currently-running agent for role launched with.
func agentDefHashPath(session, role string) string {
	return filepath.Join(BusDir(session), fmt.Sprintf("agentdef-%s.hash", role))
}

// StampAgentDefHash records the current resolved definition hash for role,
// marking the definition the agent process is launching with. Called from
// RunAgentLaunch so every launch path (initial launch, reload, health restart)
// is covered by a single stamp point. No-op if the hash can't be computed.
func StampAgentDefHash(session, role string) {
	h := AgentDefHash(role)
	if h == "" {
		return
	}
	// Non-fatal: a stamp failure must never block an agent launch. Record it to
	// the lifecycle log (discoverable via `muxcode lifecycle show`) rather than
	// stderr — this runs in RunAgentLaunch just before exec, where stderr is
	// effectively invisible. A silently-broken stamp would disable auto-reload
	// for this role until its next restart re-stamps.
	if err := os.WriteFile(agentDefHashPath(session, role), []byte(h), 0o644); err != nil {
		LogLifecycle(session, "warn", "launch", "agent-def-stamp-fail",
			fmt.Sprintf("%s: %v", role, err))
	}
}

// ReadAgentDefHash returns the stamped definition hash for role, or "" if no
// stamp exists yet.
func ReadAgentDefHash(session, role string) string {
	data, err := os.ReadFile(agentDefHashPath(session, role))
	if err != nil {
		return ""
	}
	return string(data)
}
