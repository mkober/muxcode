package bus

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// ServeState represents the state file written by the serve agent.
type ServeState struct {
	Servers []ServerEntry `json:"servers"`
}

// ServerEntry represents a single managed dev server.
type ServerEntry struct {
	Name      string `json:"name"`
	Command   string `json:"command"`
	Port      int    `json:"port"`
	PID       int    `json:"pid"`
	URL       string `json:"url"`
	StartedAt int64  `json:"started_at"`
	Restarts  int    `json:"restarts"`
	Status    string `json:"status"` // "running", "stopped", "failed"
}

// ServeStatePath returns the path to the serve state file.
func ServeStatePath(session string) string {
	return filepath.Join(BusDir(session), "serve-state.json")
}

// ReadServeState reads the serve state file. Returns nil if the file
// doesn't exist or can't be parsed (not an error — the serve agent may
// not be running).
func ReadServeState(session string) *ServeState {
	data, err := os.ReadFile(ServeStatePath(session))
	if err != nil {
		return nil
	}
	var state ServeState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil
	}
	return &state
}

// RunningServers returns only the servers with status "running".
// Returns nil if the receiver is nil — caller must check before use.
func (s *ServeState) RunningServers() []ServerEntry {
	if s == nil {
		return nil
	}
	var running []ServerEntry
	for _, srv := range s.Servers {
		if srv.Status == "running" {
			running = append(running, srv)
		}
	}
	return running
}

// IsViteServer returns true if the server appears to be a Vite dev server
// based on the name, command, or default port. Excludes Astro (port 4321)
// which uses Vite internally but is not a Vite server itself.
func (s *ServerEntry) IsViteServer() bool {
	if s.Name == "vite" || s.Name == "svelte" || s.Name == "sveltekit" {
		return true
	}
	// Check common Vite commands
	for _, pattern := range []string{"vite", "npx vite", "pnpm dev", "npm run dev", "yarn dev"} {
		if strings.Contains(s.Command, pattern) {
			return true
		}
	}
	// Default Vite ports (4321 is Astro — skip it)
	if s.Port == 5173 || s.Port == 5174 {
		return true
	}
	return false
}

