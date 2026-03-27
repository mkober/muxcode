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
	"docs", "research", "watch", "pr-read",
	"webhook", "api",
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
	"watch":   true,
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

// PaneTarget returns the tmux pane target string for a role's agent.
// Hosted roles resolve to their host window (e.g. "docs" → "edit").
func PaneTarget(session, role string) string {
	window := WindowForRole(role)
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

// busDirOverride, when non-empty, replaces the default /tmp base for BusDir.
// Used by tests to isolate bus directories in t.TempDir() instead of polluting
// the real /tmp/muxcode-bus-* namespace. Set via SetBusDirBase / ResetBusDirBase.
var busDirOverride string

// SetBusDirBase overrides the base directory for BusDir. When set, BusDir
// returns baseDir/muxcode-bus-{session} instead of /tmp/muxcode-bus-{session}.
// Intended for tests only — production code should never call this.
func SetBusDirBase(baseDir string) {
	busDirOverride = baseDir
}

// ResetBusDirBase clears the BusDir override, restoring default /tmp behavior.
func ResetBusDirBase() {
	busDirOverride = ""
}

// BusDir returns the bus directory for a session.
// Uses /tmp directly (not os.TempDir) for compatibility with bash scripts
// that hardcode /tmp/muxcode-bus-{SESSION}/.
func BusDir(session string) string {
	if busDirOverride != "" {
		return filepath.Join(busDirOverride, "muxcode-bus-"+session)
	}
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

// MemoryArchiveDir returns the archive directory for a role's memory files.
func MemoryArchiveDir(role string) string {
	return filepath.Join(MemoryDir(), role)
}

// MemoryArchivePath returns the archive file path for a role on a given date.
func MemoryArchivePath(role, date string) string {
	return filepath.Join(MemoryArchiveDir(role), date+".md")
}

// GlobalMemoryDir returns the user-level global memory directory path.
func GlobalMemoryDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "muxcode", "memory")
}

// GlobalMemoryPath returns the global memory file path for a role.
func GlobalMemoryPath(role string) string {
	if role == "shared" {
		return filepath.Join(GlobalMemoryDir(), "shared.md")
	}
	return filepath.Join(GlobalMemoryDir(), role+".md")
}

// GlobalMemoryArchiveDir returns the global archive directory for a role.
func GlobalMemoryArchiveDir(role string) string {
	return filepath.Join(GlobalMemoryDir(), role)
}

// GlobalMemoryArchivePath returns the global archive file path for a role on a date.
func GlobalMemoryArchivePath(role, date string) string {
	return filepath.Join(GlobalMemoryArchiveDir(role), date+".md")
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

// SkillsDir returns the project-local skills directory path.
// Uses BUS_SKILLS_DIR env if set, otherwise defaults to ".muxcode/skills".
func SkillsDir() string {
	if v := os.Getenv("BUS_SKILLS_DIR"); v != "" {
		return v
	}
	return filepath.Join(".muxcode", "skills")
}

// UserSkillsDir returns the user-level skills directory path.
// Uses MUXCODE_CONFIG_DIR env if set, otherwise defaults to "~/.config/muxcode/skills".
func UserSkillsDir() string {
	if v := os.Getenv("MUXCODE_CONFIG_DIR"); v != "" {
		return filepath.Join(v, "skills")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "muxcode", "skills")
}

// ContextDir returns the project-local context directory path.
// Uses BUS_CONTEXT_DIR env if set, otherwise defaults to ".muxcode/context.d".
func ContextDir() string {
	if v := os.Getenv("BUS_CONTEXT_DIR"); v != "" {
		return v
	}
	return filepath.Join(".muxcode", "context.d")
}

// UserContextDir returns the user-level context directory path.
// Uses MUXCODE_CONFIG_DIR env if set, otherwise defaults to "~/.config/muxcode/context.d".
func UserContextDir() string {
	if v := os.Getenv("MUXCODE_CONFIG_DIR"); v != "" {
		return filepath.Join(v, "context.d")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "muxcode", "context.d")
}

// CronPath returns the cron entries JSONL file path for a session.
func CronPath(session string) string {
	return filepath.Join(BusDir(session), "cron.jsonl")
}

// CronHistoryPath returns the cron execution history JSONL file path for a session.
func CronHistoryPath(session string) string {
	return filepath.Join(BusDir(session), "cron-history.jsonl")
}

// ProcDir returns the process log directory path for a session.
func ProcDir(session string) string {
	return filepath.Join(BusDir(session), "proc")
}

// ProcPath returns the process entries JSONL file path for a session.
func ProcPath(session string) string {
	return filepath.Join(BusDir(session), "proc.jsonl")
}

// ProcLogPath returns the log file path for a specific process in a session.
func ProcLogPath(session, id string) string {
	return filepath.Join(ProcDir(session), id+".log")
}

// SpawnPath returns the spawn entries JSONL file path for a session.
func SpawnPath(session string) string {
	return filepath.Join(BusDir(session), "spawn.jsonl")
}

// ModalDir returns the modal PID directory path for a session.
func ModalDir(session string) string {
	return filepath.Join(BusDir(session), "modals")
}

// ModalPidPath returns the PID file path for a named modal in a session.
func ModalPidPath(session, name string) string {
	return filepath.Join(ModalDir(session), name+".pid")
}

// WebhookPidPath returns the webhook PID file path for a session.
func WebhookPidPath(session string) string {
	return filepath.Join(BusDir(session), "webhook.pid")
}

// WatcherPidPath returns the path to the watcher PID file.
func WatcherPidPath(session string) string {
	return filepath.Join(BusDir(session), "watcher.pid")
}

// WaitingMarkerPath returns the path to a marker file that indicates the given
// role has an active --wait polling loop. While this marker exists, Notify()
// skips send-keys notifications because --wait is already polling the inbox.
func WaitingMarkerPath(session, role string) string {
	return filepath.Join(BusDir(session), "waiting-"+role+".marker")
}

// PassiveNotifyMarkerPath returns the path to a marker indicating the last
// notification for a role was passive (display-message, invisible to Claude Code).
// The watcher uses this to retry with send-keys once the agent becomes idle.
func PassiveNotifyMarkerPath(session, role string) string {
	return filepath.Join(BusDir(session), "passive-notify-"+role+".marker")
}

// SendKeysMarkerPath returns the path to a marker recording the Unix timestamp
// of the last send-keys notification for a role. Used to enforce a cooldown
// between send-keys deliveries, preventing the inter-tool-call race where the
// ❯ prompt appears briefly between tool calls and IsAgentIdle fires send-keys
// into an agent that's about to start its next tool execution.
func SendKeysMarkerPath(session, role string) string {
	return filepath.Join(BusDir(session), "sendkeys-"+role+".ts")
}

// PollingMarkerPath returns the path to a marker file indicating that a
// --poll loop is active for the given role. While this marker exists,
// Notify() skips send-keys — the poll loop watches the trigger file instead.
func PollingMarkerPath(session, role string) string {
	return filepath.Join(BusDir(session), "polling-"+role+".marker")
}

// TriggerNotifyPath returns the path to the trigger file that signals new
// messages for a role. Notify() writes a timestamp here; `muxcode inbox --poll`
// watches it for changes. This replaces send-keys as the notification mechanism
// — the agent reads its own trigger file when ready, eliminating the TOCTOU
// race where send-keys interrupts agents between tool calls.
func TriggerNotifyPath(session, role string) string {
	return filepath.Join(BusDir(session), "trigger-"+role+".notify")
}

// SubscriptionPath returns the subscriptions JSONL file path for a session.
func SubscriptionPath(session string) string {
	return filepath.Join(BusDir(session), "subscriptions.jsonl")
}

// OllamaHealthPath returns the Ollama health state file path for a session.
func OllamaHealthPath(session string) string {
	return filepath.Join(BusDir(session), "ollama-health.json")
}

// HarnessMarkerPath returns the harness PID marker file path for a role in a session.
func HarnessMarkerPath(session, role string) string {
	return filepath.Join(BusDir(session), "harness-"+role+".pid")
}

// TriggerFile returns the analyze trigger file path for a session.
// Uses /tmp directly for compatibility with bash hooks.
func TriggerFile(session string) string {
	return "/tmp/muxcode-analyze-" + session + ".trigger"
}

// hostedRoles maps roles that share another agent's window and inbox.
// Messages sent to a hosted role are delivered to the host's inbox.
// The host agent sees the original "To" field to distinguish the request context.
var hostedRoles = map[string]string{
	"docs":     "edit",
	"research": "edit",
	"pr-read":  "commit",
}

// WindowForRole returns the tmux window name where a role runs.
// Hosted roles return their host window; all others return themselves.
func WindowForRole(role string) string {
	if host, ok := hostedRoles[role]; ok {
		return host
	}
	return role
}

// IsHostedRole returns true if the role runs inside another agent's window.
func IsHostedRole(role string) bool {
	_, ok := hostedRoles[role]
	return ok
}

// IsSpawnRole returns true if the role is a spawn-prefixed role (e.g. "spawn-a1b2c3d4").
func IsSpawnRole(role string) bool {
	return strings.HasPrefix(role, "spawn-")
}

// IsKnownRole checks if a role is in the known roles list or is a spawn role.
func IsKnownRole(role string) bool {
	if IsSpawnRole(role) {
		return true
	}
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
