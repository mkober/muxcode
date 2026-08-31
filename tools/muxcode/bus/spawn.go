package bus

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// SpawnEntry represents a tracked spawned agent session.
type SpawnEntry struct {
	ID          string `json:"id"`
	Role        string `json:"role"`       // base role, e.g. "research"
	SpawnRole   string `json:"spawn_role"` // bus role + window name, e.g. "spawn-a1b2c3d4"
	Owner       string `json:"owner"`      // requesting agent, e.g. "edit"
	Task        string `json:"task"`       // task description
	Status      string `json:"status"`     // "running", "completed", "stopped"
	Window      string `json:"window"`     // tmux window name (= SpawnRole)
	StartedAt   int64  `json:"started_at"`
	FinishedAt  int64  `json:"finished_at"`
	Notified    bool   `json:"notified"`
	Worktree    string `json:"worktree,omitempty"`     // worktree directory path
	WorktreeRef string `json:"worktree_ref,omitempty"` // git ref used (commit SHA)
}

// ReadSpawnEntries reads all spawn entries from the spawn JSONL file.
func ReadSpawnEntries(session string) ([]SpawnEntry, error) {
	data, err := os.ReadFile(SpawnPath(session))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var entries []SpawnEntry
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var e SpawnEntry
		if err := json.Unmarshal(line, &e); err != nil {
			continue // skip malformed lines
		}
		entries = append(entries, e)
	}
	return entries, scanner.Err()
}

// WriteSpawnEntries overwrites the spawn JSONL file with the given entries.
func WriteSpawnEntries(session string, entries []SpawnEntry) error {
	var buf bytes.Buffer
	for _, e := range entries {
		data, err := json.Marshal(e)
		if err != nil {
			return err
		}
		buf.Write(data)
		buf.WriteByte('\n')
	}
	return os.WriteFile(SpawnPath(session), buf.Bytes(), 0644)
}

// GetSpawnEntry returns a single spawn entry by ID.
func GetSpawnEntry(session, id string) (SpawnEntry, error) {
	entries, err := ReadSpawnEntries(session)
	if err != nil {
		return SpawnEntry{}, err
	}

	for _, e := range entries {
		if e.ID == id {
			return e, nil
		}
	}
	return SpawnEntry{}, fmt.Errorf("spawn not found: %s", id)
}

// UpdateSpawnEntry applies a mutation function to a spawn entry by ID.
func UpdateSpawnEntry(session, id string, fn func(*SpawnEntry)) error {
	entries, err := ReadSpawnEntries(session)
	if err != nil {
		return err
	}

	found := false
	for i, e := range entries {
		if e.ID == id {
			fn(&entries[i])
			found = true
			break
		}
	}

	if !found {
		return fmt.Errorf("spawn not found: %s", id)
	}

	return WriteSpawnEntries(session, entries)
}

// StartSpawn creates a tmux window, seeds the inbox with the task, and launches
// an agent. When useWorktree is true, the spawn gets its own git worktree at
// the current HEAD commit for filesystem isolation. Returns the SpawnEntry.
func StartSpawn(session, role, task, owner string, useWorktree bool) (SpawnEntry, error) {
	// Generate spawn ID and extract 8-hex suffix for compact window name
	fullID := NewMsgID("spawn")
	parts := strings.Split(fullID, "-")
	suffix := parts[len(parts)-1] // 8-hex suffix
	spawnRole := "spawn-" + suffix

	entry := SpawnEntry{
		ID:        fullID,
		Role:      role,
		SpawnRole: spawnRole,
		Owner:     owner,
		Task:      task,
		Status:    "running",
		Window:    spawnRole,
		StartedAt: time.Now().Unix(),
	}

	// Create worktree if requested
	if useWorktree {
		wtPath, wtRef, err := createSpawnWorktree(session, spawnRole)
		if err != nil {
			// Fall back to shared CWD with warning
			fmt.Fprintf(os.Stderr, "Warning: worktree creation failed (%v), using shared CWD\n", err)
		} else {
			entry.Worktree = wtPath
			entry.WorktreeRef = wtRef
		}
	}

	// Ensure inbox directory exists and touch inbox file for spawn role
	inboxDir := filepath.Dir(InboxPath(session, spawnRole))
	if err := os.MkdirAll(inboxDir, 0755); err != nil {
		return SpawnEntry{}, fmt.Errorf("creating inbox dir: %v", err)
	}
	if err := touchFile(InboxPath(session, spawnRole)); err != nil {
		return SpawnEntry{}, fmt.Errorf("touching inbox: %v", err)
	}

	// Seed inbox with task message
	msg := NewMessage(owner, spawnRole, "request", "spawn-task", task, "")
	if err := Send(session, msg); err != nil {
		return SpawnEntry{}, fmt.Errorf("seeding inbox: %v", err)
	}

	// Find the muxcode binary (agent launch is now native Go)
	launcher, err := findMuxcodeBinary()
	if err != nil {
		return SpawnEntry{}, fmt.Errorf("finding muxcode binary: %v", err)
	}

	// Create tmux window
	createCmd := exec.Command("tmux", "new-window", "-t", session, "-n", spawnRole)
	if err := createCmd.Run(); err != nil {
		return SpawnEntry{}, fmt.Errorf("creating tmux window: %v", err)
	}
	// Status-bar label: the spawn id says nothing to a human scanning tabs
	// (user request 2026-08-28) — the window keeps its id name for
	// targeting while the bar reads "Worker".
	TmuxSetWindowOption(session+":"+spawnRole, "@display-name", "Worker")
	TmuxSetWindowOption(session+":"+spawnRole, "@display-name-upper", "WORKER")

	// Split horizontally (agent in pane 1, consistent with all windows)
	splitCmd := exec.Command("tmux", "split-window", "-h", "-t", session+":"+spawnRole)
	if err := splitCmd.Run(); err != nil {
		return SpawnEntry{}, fmt.Errorf("splitting window: %v", err)
	}

	// Worker console in pane 0 — view only, a failure must not block the spawn
	_ = exec.Command("tmux", "select-pane", "-t", session+":"+spawnRole+".0", "-T", "CONSOLE").Run()
	sendKeysThenEnter(session+":"+spawnRole+".0", fmt.Sprintf("%s console %s", launcher, spawnRole))

	// Launch agent in pane 1 — cd into worktree if set.
	// AGENT_ROLE must be the spawn-specific role (e.g. "spawn-edit-1") so the
	// agent reads from its own inbox, not the base role's inbox.
	var launchStr string
	if entry.Worktree != "" {
		launchStr = fmt.Sprintf("cd %s && AGENT_ROLE=%s %s agent launch %s", entry.Worktree, spawnRole, launcher, role)
	} else {
		launchStr = fmt.Sprintf("AGENT_ROLE=%s %s agent launch %s", spawnRole, launcher, role)
	}
	if err := sendKeysThenEnter(session+":"+spawnRole+".1", launchStr); err != nil {
		return SpawnEntry{}, fmt.Errorf("launching agent: %v", err)
	}

	go wakeSpawnedAgent(session, spawnRole)

	// Persist entry
	entries, err := ReadSpawnEntries(session)
	if err != nil {
		return SpawnEntry{}, err
	}
	entries = append(entries, entry)
	if err := WriteSpawnEntries(session, entries); err != nil {
		return SpawnEntry{}, err
	}

	return entry, nil
}

// sendKeysThenEnter types text and presses Enter as two pty writes with a
// settle delay — text and Enter in one write is the documented
// dropped-Enter pitfall (PR #50 Copilot flagged the one-call form here).
func sendKeysThenEnter(target, text string) error {
	if err := exec.Command("tmux", "send-keys", "-t", target, "-l", "--", text).Run(); err != nil {
		return err
	}
	time.Sleep(100 * time.Millisecond)
	return exec.Command("tmux", "send-keys", "-t", target, "Enter").Run()
}

// wakeSpawnedAgent delivers a spawned agent's first turn: a fresh agent
// never types by itself, so the seeded task sits in an inbox nothing
// delivers (live gap, MUX-120 — a graph worker idled 4.5min until a
// manual deliver --force). Reuses wakeAfterReload's prompt-ready wait;
// async because the daemon's map fan-out must not stall the keepalive
// past the monitor threshold. A caller that exits immediately (CLI
// spawn) cuts the goroutine short and no daemon backstop covers spawn
// roles — checkPollHealth iterates static KnownRoles — so that path
// remains open and is tracked in MUX-120. This replaced a fixed 2s Notify
// which fired while the agent was still initializing: that fell to the
// displayMessage path and poisoned the notified dedup for 30s, actively
// suppressing later wakes.
func wakeSpawnedAgent(session, spawnRole string) {
	LogLifecycle(session, "info", "daemon", "spawn-wake", spawnRole)
	wakeAfterReload(session, spawnRole)
}

// StopSpawn kills the tmux window for a spawn, cleans up the worktree, and marks it stopped.
func StopSpawn(session, id string) error {
	entry, err := GetSpawnEntry(session, id)
	if err != nil {
		return err
	}

	if entry.Status != "running" {
		return fmt.Errorf("spawn %s is not running (status: %s)", id, entry.Status)
	}

	// Kill the tmux window
	killCmd := exec.Command("tmux", "kill-window", "-t", session+":"+entry.Window)
	_ = killCmd.Run() // ignore error if window already gone

	// Clean up worktree
	if err := removeSpawnWorktree(entry.Worktree); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: worktree cleanup failed for %s: %v\n", entry.SpawnRole, err)
	}

	// Update entry
	return UpdateSpawnEntry(session, id, func(e *SpawnEntry) {
		e.Status = "stopped"
		e.FinishedAt = time.Now().Unix()
	})
}

// CheckSpawnWindow checks if a tmux window exists for a spawn entry.
func CheckSpawnWindow(session, window string) bool {
	cmd := exec.Command("tmux", "list-windows", "-t", session, "-F", "#{window_name}")
	out, err := cmd.Output()
	if err != nil {
		return false
	}

	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(line) == window {
			return true
		}
	}
	return false
}

// RefreshSpawnStatus checks all running spawns and updates their status.
// Returns the list of entries that transitioned from running to completed.
func RefreshSpawnStatus(session string) ([]SpawnEntry, error) {
	entries, err := ReadSpawnEntries(session)
	if err != nil {
		return nil, err
	}

	var completed []SpawnEntry
	changed := false

	for i, e := range entries {
		if e.Status != "running" {
			continue
		}

		if CheckSpawnWindow(session, e.Window) {
			continue
		}

		// Window is gone — mark completed and clean up worktree
		entries[i].Status = "completed"
		entries[i].FinishedAt = time.Now().Unix()
		changed = true

		if err := removeSpawnWorktree(e.Worktree); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: worktree cleanup failed for %s: %v\n", e.SpawnRole, err)
		}

		completed = append(completed, entries[i])
	}

	if changed {
		if err := WriteSpawnEntries(session, entries); err != nil {
			return completed, err
		}
	}

	return completed, nil
}

// GetSpawnResult returns the last message sent FROM a spawn role in the session log.
// The spawned agent naturally sends bus messages back to the owner — the last one
// serves as the result.
func GetSpawnResult(session, spawnRole string) (Message, bool) {
	msgs := readLogForRole(session, spawnRole, 0) // 0 = no limit

	// Find the last message FROM the spawn role
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].From == spawnRole {
			return msgs[i], true
		}
	}
	return Message{}, false
}

// CleanFinishedSpawns removes all non-running spawn entries, their inbox files,
// and any remaining worktrees. Also prunes orphaned worktree directories.
func CleanFinishedSpawns(session string) (int, error) {
	entries, err := ReadSpawnEntries(session)
	if err != nil {
		return 0, err
	}

	var kept []SpawnEntry
	removed := 0
	for _, e := range entries {
		if e.Status == "running" {
			kept = append(kept, e)
			continue
		}
		// Remove spawn inbox file
		_ = os.Remove(InboxPath(session, e.SpawnRole))
		// Clean up worktree if present
		if e.Worktree != "" {
			_ = removeSpawnWorktree(e.Worktree)
		}
		removed++
	}

	if err := WriteSpawnEntries(session, kept); err != nil {
		return removed, err
	}

	// Prune orphaned worktree directories under the spawn base dir
	pruneOrphanedWorktrees(session, kept)

	return removed, nil
}

// FormatSpawnList formats spawn entries as a human-readable table.
// When showAll is false, only running entries are shown.
func FormatSpawnList(entries []SpawnEntry, showAll bool) string {
	var b strings.Builder

	var filtered []SpawnEntry
	for _, e := range entries {
		if showAll || e.Status == "running" {
			filtered = append(filtered, e)
		}
	}

	if len(filtered) == 0 {
		if showAll {
			b.WriteString("No spawns.\n")
		} else {
			b.WriteString("No running spawns. Use --all to see finished spawns.\n")
		}
		return b.String()
	}

	b.WriteString(fmt.Sprintf("%-36s %-12s %-12s %-10s %-10s %-8s %s\n",
		"ID", "ROLE", "SPAWN-ROLE", "STATUS", "OWNER", "WORKTREE", "TASK"))
	b.WriteString(strings.Repeat("-", 120) + "\n")

	for _, e := range filtered {
		task := e.Task
		if len(task) > 40 {
			task = task[:37] + "..."
		}
		wt := "shared"
		if e.Worktree != "" {
			wt = "yes"
		}
		b.WriteString(fmt.Sprintf("%-36s %-12s %-12s %-10s %-10s %-8s %s\n",
			e.ID, e.Role, e.SpawnRole, e.Status, e.Owner, wt, task))
	}

	return b.String()
}

// FormatSpawnStatus formats a single spawn entry as a detailed status report.
func FormatSpawnStatus(entry SpawnEntry) string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf("Spawn: %s\n", entry.ID))
	b.WriteString(fmt.Sprintf("  Role:       %s\n", entry.Role))
	b.WriteString(fmt.Sprintf("  Spawn Role: %s\n", entry.SpawnRole))
	b.WriteString(fmt.Sprintf("  Status:     %s\n", entry.Status))
	b.WriteString(fmt.Sprintf("  Owner:      %s\n", entry.Owner))
	b.WriteString(fmt.Sprintf("  Window:     %s\n", entry.Window))
	b.WriteString(fmt.Sprintf("  Task:       %s\n", entry.Task))
	if entry.Worktree != "" {
		b.WriteString(fmt.Sprintf("  Worktree:   %s\n", entry.Worktree))
		b.WriteString(fmt.Sprintf("  Ref:        %s\n", entry.WorktreeRef))
	} else {
		b.WriteString("  Worktree:   shared (main working directory)\n")
	}
	b.WriteString(fmt.Sprintf("  Started:    %s\n", time.Unix(entry.StartedAt, 0).Format("2006-01-02 15:04:05")))

	if entry.FinishedAt > 0 {
		b.WriteString(fmt.Sprintf("  Finished:   %s\n", time.Unix(entry.FinishedAt, 0).Format("2006-01-02 15:04:05")))
		duration := time.Duration(entry.FinishedAt-entry.StartedAt) * time.Second
		b.WriteString(fmt.Sprintf("  Duration:   %s\n", duration))
	}

	return b.String()
}

// SpawnWorktreeBase returns the base directory for spawn worktrees.
func SpawnWorktreeBase(session string) string {
	return filepath.Join(os.TempDir(), "muxcode-spawn-"+session)
}

// createSpawnWorktree creates a git worktree for a spawn agent.
// Returns (worktree path, commit SHA, error).
func createSpawnWorktree(session, spawnRole string) (string, string, error) {
	// Get current HEAD commit
	refOut, err := exec.Command("git", "rev-parse", "HEAD").Output()
	if err != nil {
		return "", "", fmt.Errorf("git rev-parse HEAD: %v", err)
	}
	ref := strings.TrimSpace(string(refOut))

	// Create worktree directory
	base := SpawnWorktreeBase(session)
	wtPath := filepath.Join(base, spawnRole)
	if err := os.MkdirAll(base, 0755); err != nil {
		return "", "", fmt.Errorf("creating worktree base dir: %v", err)
	}

	// git worktree add --detach <path> HEAD
	cmd := exec.Command("git", "worktree", "add", "--detach", wtPath, "HEAD")
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", "", fmt.Errorf("git worktree add: %v (%s)", err, strings.TrimSpace(string(out)))
	}

	return wtPath, ref, nil
}

// removeSpawnWorktree cleans up a spawn's git worktree.
// Returns nil if worktreePath is empty (no worktree to remove).
func removeSpawnWorktree(worktreePath string) error {
	if worktreePath == "" {
		return nil
	}

	// Try git worktree remove --force first
	cmd := exec.Command("git", "worktree", "remove", "--force", worktreePath)
	if err := cmd.Run(); err != nil {
		// Fallback: rm -rf if git worktree remove fails
		if err2 := os.RemoveAll(worktreePath); err2 != nil {
			return fmt.Errorf("git worktree remove failed (%v) and os.RemoveAll failed (%v)", err, err2)
		}
		// Prune stale worktree references after manual removal
		_ = exec.Command("git", "worktree", "prune").Run()
	}
	return nil
}

// pruneOrphanedWorktrees removes any worktree directories under the spawn base
// that don't correspond to a running spawn entry.
func pruneOrphanedWorktrees(session string, runningEntries []SpawnEntry) {
	base := SpawnWorktreeBase(session)
	dirEntries, err := os.ReadDir(base)
	if err != nil {
		return // base dir doesn't exist or isn't readable
	}

	// Build set of active worktree paths
	active := make(map[string]bool)
	for _, e := range runningEntries {
		if e.Worktree != "" {
			active[e.Worktree] = true
		}
	}

	for _, d := range dirEntries {
		if !d.IsDir() {
			continue
		}
		dirPath := filepath.Join(base, d.Name())
		if active[dirPath] {
			continue
		}
		// Orphaned — remove
		_ = removeSpawnWorktree(dirPath)
	}

	// Prune git worktree references
	_ = exec.Command("git", "worktree", "prune").Run()
}

// findMuxcodeBinary locates the muxcode binary.
// Checks: ~/.local/bin/, PATH, then the current executable path.
func findMuxcodeBinary() (string, error) {
	home, _ := os.UserHomeDir()

	// Check common install location
	candidate := filepath.Join(home, ".local", "bin", "muxcode")
	if _, err := os.Stat(candidate); err == nil {
		return candidate, nil
	}

	// Check PATH
	if p, err := exec.LookPath("muxcode"); err == nil {
		return p, nil
	}

	// Fall back to the current executable
	if exe, err := os.Executable(); err == nil {
		return exe, nil
	}

	return "", fmt.Errorf("muxcode binary not found in ~/.local/bin/, PATH, or as current executable")
}
