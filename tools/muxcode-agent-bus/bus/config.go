package bus

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// KnownRoles lists all valid agent roles.
// Extended at runtime via MUXCODE_ROLES env var (comma-separated).
var KnownRoles = []string{
	"edit", "build", "test", "review",
	"deploy", "run", "commit", "analyze",
	"docs", "research",
}

// splitLeftWindows lists windows that have a dedicated tool in the left pane.
// muxcode.sh always puts the agent in pane 1 (right) for all windows,
// so this map is used only for informational purposes.
// Override via MUXCODE_SPLIT_LEFT env var (space-separated).
var splitLeftWindows = map[string]bool{
	"edit":    true,
	"build":   true,
	"test":    true,
	"review":  true,
	"deploy":  true,
	"analyze": true,
	"commit":  true,
}

func init() {
	// Extend KnownRoles from env
	if extra := os.Getenv("MUXCODE_ROLES"); extra != "" {
		for _, r := range strings.Split(extra, ",") {
			r = strings.TrimSpace(r)
			if r != "" && !IsKnownRole(r) {
				KnownRoles = append(KnownRoles, r)
			}
		}
	}

	// Override split-left windows from env
	if v := os.Getenv("MUXCODE_SPLIT_LEFT"); v != "" {
		splitLeftWindows = make(map[string]bool)
		for _, w := range strings.Fields(v) {
			splitLeftWindows[w] = true
		}
	}
}

// IsSplitLeft returns true if the window has a left pane (agent in pane 1).
func IsSplitLeft(window string) bool {
	return splitLeftWindows[window]
}

// AgentPane returns the tmux pane number where the agent runs for a window.
// muxcode.sh always splits horizontally and launches the agent in pane 1
// (the right pane) for all windows, so this always returns "1".
func AgentPane(window string) string {
	return "1"
}

// PaneTarget returns the tmux pane target string for a window's agent.
func PaneTarget(session, window string) string {
	return session + ":" + window + "." + AgentPane(window)
}

// BusSession returns the current bus session name.
// Checks BUS_SESSION env, SESSION env, tmux session name, then defaults to "default".
func BusSession() string {
	if v := os.Getenv("BUS_SESSION"); v != "" {
		return v
	}
	if v := os.Getenv("SESSION"); v != "" {
		return v
	}
	if v := tmuxVar("#S"); v != "" {
		return v
	}
	return "default"
}

// BusRole returns the current agent role.
// Checks AGENT_ROLE env, BUS_ROLE env, tmux window name, then defaults to "unknown".
func BusRole() string {
	if v := os.Getenv("AGENT_ROLE"); v != "" {
		return v
	}
	if v := os.Getenv("BUS_ROLE"); v != "" {
		return v
	}
	if v := tmuxVar("#W"); v != "" {
		return v
	}
	return "unknown"
}

// BusDir returns the bus directory for a session.
// Uses /tmp directly (not os.TempDir) for compatibility with bash scripts
// that hardcode /tmp/muxcode-bus-{SESSION}/.
func BusDir(session string) string {
	return "/tmp/muxcode-bus-" + session
}

// InboxPath returns the inbox file path for a role in a session.
func InboxPath(session, role string) string {
	return filepath.Join(BusDir(session), "inbox", role+".jsonl")
}

// LockPath returns the lock file path for a role in a session.
func LockPath(session, role string) string {
	return filepath.Join(BusDir(session), "lock", role+".lock")
}

// LogPath returns the log file path for a session.
func LogPath(session string) string {
	return filepath.Join(BusDir(session), "log.jsonl")
}

// MemoryDir returns the memory directory path.
// Uses BUS_MEMORY_DIR env if set, otherwise defaults to ".muxcode/memory".
func MemoryDir() string {
	if v := os.Getenv("BUS_MEMORY_DIR"); v != "" {
		return v
	}
	return filepath.Join(".muxcode", "memory")
}

// MemoryPath returns the memory file path for a role.
func MemoryPath(role string) string {
	if role == "shared" {
		return filepath.Join(MemoryDir(), "shared.md")
	}
	return filepath.Join(MemoryDir(), role+".md")
}

// BuildHistoryPath returns the build history JSONL file path for a session.
func BuildHistoryPath(session string) string {
	return filepath.Join(BusDir(session), "build-history.jsonl")
}

// TestHistoryPath returns the test history JSONL file path for a session.
func TestHistoryPath(session string) string {
	return filepath.Join(BusDir(session), "test-history.jsonl")
}

// HistoryPath returns the history JSONL file path for any role in a session.
func HistoryPath(session, role string) string {
	return filepath.Join(BusDir(session), role+"-history.jsonl")
}

// TriggerFile returns the analyze trigger file path for a session.
// Uses /tmp directly for compatibility with bash hooks.
func TriggerFile(session string) string {
	return "/tmp/muxcode-analyze-" + session + ".trigger"
}

// IsKnownRole checks if a role is in the known roles list.
func IsKnownRole(role string) bool {
	for _, r := range KnownRoles {
		if r == role {
			return true
		}
	}
	return false
}

// tmuxVar runs tmux display-message to get a variable value.
// Uses TMUX_PANE to target the correct pane, so queries like #W return
// the window where the process is running rather than the active window.
func tmuxVar(format string) string {
	args := []string{"display-message"}
	if pane := os.Getenv("TMUX_PANE"); pane != "" {
		args = append(args, "-t", pane)
	}
	args = append(args, "-p", format)
	out, err := exec.Command("tmux", args...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
