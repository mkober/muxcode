package bus

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// ExitSighting is one Claude agent a session's daemon found down. The
// on-disk shape (one JSON object per line in BusDir()/agent-exits.jsonl) is
// read by every other session's daemon, so a field rename is a breaking
// change across concurrently running binaries.
type ExitSighting struct {
	Session string `json:"session"`
	Role    string `json:"role"`
	At      int64  `json:"at"`
}

// MassExit is a burst of Claude agent exits inside one correlation window,
// at most one sighting per (session, role).
type MassExit struct {
	Sightings []ExitSighting
	Window    int64
}

const (
	agentExitsFile        = "agent-exits.jsonl"
	agentExitsRetainSecs  = 3600
	massExitWindowEnv     = "MUXCODE_MASS_EXIT_WINDOW_SECS"
	defaultMassExitWindow = 60
)

// MassExitWindowSecs is the correlation window: two or more Claude agents
// down within it are one mass exit. MUXCODE_MASS_EXIT_WINDOW_SECS overrides
// the 60s default; a non-positive or unparsable value keeps the default.
func MassExitWindowSecs() int64 {
	if v := os.Getenv(massExitWindowEnv); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			return n
		}
	}
	return defaultMassExitWindow
}

// RecordAgentExit appends a sighting of role down at `at` to session's
// exit log, dropping rows older than an hour so the file stays bounded.
// Only the session's own daemon writes the file; other daemons only read it.
func RecordAgentExit(session, role string, at int64) error {
	path := filepath.Join(BusDir(session), agentExitsFile)
	kept := []ExitSighting{}
	for _, s := range readAgentExits(path) {
		if at-s.At < agentExitsRetainSecs {
			kept = append(kept, s)
		}
	}
	kept = append(kept, ExitSighting{Session: session, Role: role, At: at})

	var b strings.Builder
	for _, s := range kept {
		line, err := json.Marshal(s)
		if err != nil {
			return err
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	return atomicWriteFile(path, []byte(b.String()))
}

// readAgentExits returns the parsable rows of one exit log; a missing file
// or a malformed line contributes nothing.
func readAgentExits(path string) []ExitSighting {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var out []ExitSighting
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var s ExitSighting
		if json.Unmarshal(sc.Bytes(), &s) == nil && s.Role != "" && s.Session != "" {
			out = append(out, s)
		}
	}
	return out
}

// RecentAgentExits gathers the sightings in (now-window, now] from every
// session whose bus directory is visible (DiscoverSessions, current session
// included).
func RecentAgentExits(currentSession string, now, window int64) ([]ExitSighting, error) {
	sessions, err := DiscoverSessions(currentSession, false)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(sessions))
	for _, rs := range sessions {
		names = append(names, rs.Name)
	}
	return AgentExitsIn(names, now, window), nil
}

// AgentExitsIn returns the named sessions' sightings in (now-window, now],
// sorted by time, then session, then role.
func AgentExitsIn(sessions []string, now, window int64) []ExitSighting {
	var out []ExitSighting
	for _, sess := range sessions {
		for _, s := range readAgentExits(filepath.Join(BusDir(sess), agentExitsFile)) {
			if s.At > now-window && s.At <= now {
				out = append(out, s)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].At != out[j].At {
			return out[i].At < out[j].At
		}
		if out[i].Session != out[j].Session {
			return out[i].Session < out[j].Session
		}
		return out[i].Role < out[j].Role
	})
	return out
}

// DetectMassExit reports a mass exit when the sightings name at least two
// distinct (session, role) pairs. A role sighted repeatedly — one agent
// failing its restarts — counts once, so it can never correlate with itself.
func DetectMassExit(sightings []ExitSighting, window int64) (MassExit, bool) {
	seen := map[string]bool{}
	m := MassExit{Window: window}
	for _, s := range sightings {
		key := s.Session + "\x00" + s.Role
		if seen[key] {
			continue
		}
		seen[key] = true
		m.Sightings = append(m.Sightings, s)
	}
	return m, len(m.Sightings) >= 2
}

// Sessions returns the distinct sessions in the burst, sorted.
func (m MassExit) Sessions() []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range m.Sightings {
		if !seen[s.Session] {
			seen[s.Session] = true
			out = append(out, s.Session)
		}
	}
	sort.Strings(out)
	return out
}

// Detail is the one-line lifecycle detail: count, window, then every
// session's roles as `session: role, role`.
func (m MassExit) Detail() string {
	bySession := map[string][]string{}
	for _, s := range m.Sightings {
		bySession[s.Session] = append(bySession[s.Session], s.Role)
	}
	var parts []string
	for _, sess := range m.Sessions() {
		roles := bySession[sess]
		sort.Strings(roles)
		parts = append(parts, sess+": "+strings.Join(roles, ", "))
	}
	return fmt.Sprintf("%d Claude agents down within %ds across %d session(s) — %s",
		len(m.Sightings), m.Window, len(m.Sessions()), strings.Join(parts, "; "))
}

// FormatMassExitAlert is the event body sent to edit.
func FormatMassExitAlert(m MassExit) string {
	return fmt.Sprintf("[mass-agent-exit] %s. These exits are correlated, not independent faults — "+
		"look for one external cause (Claude Code update, OS termination). Per-role restart proceeds "+
		"as usual: muxcode lifecycle show --event mass-agent-exit", m.Detail())
}
