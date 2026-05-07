package bus

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// KnownRoles lists all valid agent roles.
// Extended at runtime via MUXCODE_ROLES env var (comma-separated).
var KnownRoles = []string{
	"plan", "edit", "build", "test", "review",
	"deploy", "run", "commit", "analyze",
	"docs", "research", "watch", "pr-read",
	"webhook", "api", "auto", "serve",
}

// splitLeftWindows lists windows that have a dedicated tool in the left pane.
// muxcode.sh always puts the agent in pane 1 (right) for all windows,
// so this map is used only for informational purposes.
// Override via MUXCODE_SPLIT_LEFT env var (space-separated).
var splitLeftWindows = map[string]bool{
	"plan":    true,
	"edit":    true,
	"build":   true,
	"test":    true,
	"review":  true,
	"deploy":  true,
	"analyze": true,
	"commit":  true,
	"watch":   true,
	"serve":   true,
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
	var role string
	if v := os.Getenv("AGENT_ROLE"); v != "" {
		role = v
	} else if v := os.Getenv("BUS_ROLE"); v != "" {
		role = v
	} else if v := tmuxVar("#W"); v != "" {
		role = v
	} else {
		return "unknown"
	}
	return NormalizeBusRole(role)
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

// RuntimeConfigDir returns the runtime config directory path for a session.
// Override files (per-role CLI/model runtime overrides) are stored here.
// These files are ephemeral — they live under /tmp/ and are cleaned up with the session.
func RuntimeConfigDir(session string) string {
	return filepath.Join(BusDir(session), "config")
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
// Deprecated: Use DaemonPidPath instead.
func WatcherPidPath(session string) string {
	return filepath.Join(BusDir(session), "watcher.pid")
}

// DaemonPidPath returns the path to the daemon PID file.
func DaemonPidPath(session string) string {
	return filepath.Join(BusDir(session), "daemon.pid")
}

// WaitingMarkerPath returns the path to a marker file that indicates the given
// role has an active --wait polling loop. While this marker exists, Notify()
// skips display-message notifications because --wait is already polling the inbox.
func WaitingMarkerPath(session, role string) string {
	return filepath.Join(BusDir(session), "waiting-"+role+".marker")
}

// PollingMarkerPath returns the path to a marker file indicating that a
// --poll loop is active for the given role. While this marker exists,
// Notify() skips display-message — the poll loop watches the trigger file instead.
func PollingMarkerPath(session, role string) string {
	return filepath.Join(BusDir(session), "polling-"+role+".marker")
}

// staleMarkerAge returns the duration after which a waiting/polling marker
// is considered stale and should be cleaned up. This prevents stale markers
// from blocking notifications if a process crashes during --wait or --poll.
const staleMarkerAge = 10 * time.Minute

// CleanStaleMarkers removes waiting and polling markers that are older than
// staleMarkerAge. This should be called during bus initialization to prevent
// stale markers from blocking notifications.
func CleanStaleMarkers(session string) error {
	busDir := BusDir(session)
	entries, err := os.ReadDir(busDir)
	if err != nil {
		return err
	}

	now := time.Now()
	for _, entry := range entries {
		name := entry.Name()
		// Only check waiting-*.marker and polling-*.marker files
		if !strings.HasSuffix(name, ".marker") {
			continue
		}
		if !strings.HasPrefix(name, "waiting-") && !strings.HasPrefix(name, "polling-") {
			continue
		}

		path := filepath.Join(busDir, name)
		info, err := os.Stat(path)
		if err != nil {
			continue // skip if can't stat
		}
		if now.Sub(info.ModTime()) > staleMarkerAge {
			// Marker is stale - remove it
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				// Log but continue
				continue
			}
		}
	}
	return nil
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

// AgentHeartbeatPath returns the path to the last heartbeat timestamp file for the agent.
func AgentHeartbeatPath(session string) string {
	return filepath.Join(BusDir(session), "agent-last-heartbeat")
}

// AgentCurrentStoryPath returns the path to the file tracking the current Jira story.
func AgentCurrentStoryPath(session string) string {
	return filepath.Join(BusDir(session), "agent-current-story")
}

// AgentPhasePath returns the path to the file tracking the agent's current phase.
func AgentPhasePath(session string) string {
	return filepath.Join(BusDir(session), "agent-phase")
}

// AgentStoriesDonePath returns the path to the file tracking completed story count.
func AgentStoriesDonePath(session string) string {
	return filepath.Join(BusDir(session), "agent-stories-done")
}

// AgentHeartbeatInterval returns the configured heartbeat interval in seconds.
// Uses MUXCODE_AGENT_HEARTBEAT env var, defaulting to 1800 (30 minutes).
// Returns 0 if heartbeat is disabled.
func AgentHeartbeatInterval() int {
	if v := os.Getenv("MUXCODE_AGENT_HEARTBEAT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return 1800
}

// TriggerFile returns the analyze trigger file path for a session.
// Uses /tmp directly for compatibility with bash hooks.
func TriggerFile(session string) string {
	return "/tmp/muxcode-analyze-" + session + ".trigger"
}

// EditDiffHashPath returns the path for the edit diff hash state file.
// Used by the daemon to track file changes for non-hook edit providers.
func EditDiffHashPath(session string) string {
	return filepath.Join(BusDir(session), "edit-diff-hash")
}

// hostedRoles maps roles that share another agent's window and inbox.
// Messages sent to a hosted role are delivered to the host's inbox.
// The host agent sees the original "To" field to distinguish the request context.
var hostedRoles = map[string]string{
	"docs":    "plan",
	"pr-read": "commit",
}

// modeRoles maps roles that share a window via mode cycling.
// Unlike hostedRoles, mode roles have their own independent inboxes.
// The value is the host window name (for PaneTarget resolution).
var modeRoles = map[string]string{
	"auto":     "auto",
	"research": "research",
}

// WindowForRole returns the tmux window name where a role runs.
// Hosted roles return their host window; all others return themselves.
func WindowForRole(role string) string {
	if host, ok := hostedRoles[role]; ok {
		return host
	}
	if host, ok := modeRoles[role]; ok {
		return host
	}
	return role
}

// IsModeRole returns true if the role shares a window via mode cycling.
func IsModeRole(role string) bool {
	_, ok := modeRoles[role]
	return ok
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

// NormalizeBusRole maps legacy role aliases back to canonical bus role names.
// Canonical names match tmux window names: commit, analyze, run.
// Legacy aliases (git, analyst, runner) are accepted for backward compatibility.
// Unknown roles are returned unchanged.
func NormalizeBusRole(role string) string {
	switch role {
	case "analyst":
		return "analyze"
	case "git":
		return "commit"
	case "runner":
		return "run"
	case "planner":
		return "plan"
	case "agent":
		return "auto"
	case "daemon":
		// The daemon process is not an agent — route replies to edit.
		return "edit"
	default:
		return role
	}
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
