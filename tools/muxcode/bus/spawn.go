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
	ID         string `json:"id"`
	Role       string `json:"role"`       // base role, e.g. "research"
	SpawnRole  string `json:"spawn_role"` // bus role + window name, e.g. "spawn-a1b2c3d4"
	Owner      string `json:"owner"`      // requesting agent, e.g. "edit"
	Task       string `json:"task"`       // task description
	Status     string `json:"status"`     // "running", "completed", "stopped"
	Window     string `json:"window"`     // tmux window name (= SpawnRole)
	StartedAt  int64  `json:"started_at"`
	FinishedAt int64  `json:"finished_at"`
	Notified   bool   `json:"notified"`
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
// an agent. Returns the SpawnEntry for the new spawn.
func StartSpawn(session, role, task, owner string) (SpawnEntry, error) {
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

	// Find agent launcher script
	launcher, err := findAgentLauncher()
	if err != nil {
		return SpawnEntry{}, fmt.Errorf("finding agent launcher: %v", err)
	}

	// Create tmux window
	createCmd := exec.Command("tmux", "new-window", "-t", session, "-n", spawnRole)
	if err := createCmd.Run(); err != nil {
		return SpawnEntry{}, fmt.Errorf("creating tmux window: %v", err)
	}

	// Split horizontally (agent in pane 1, consistent with all windows)
	splitCmd := exec.Command("tmux", "split-window", "-h", "-t", session+":"+spawnRole)
	if err := splitCmd.Run(); err != nil {
		return SpawnEntry{}, fmt.Errorf("splitting window: %v", err)
	}

	// Launch agent in pane 1
	launchStr := fmt.Sprintf("AGENT_ROLE=%s %s %s", spawnRole, launcher, role)
	launchCmd := exec.Command("tmux", "send-keys", "-t", session+":"+spawnRole+".1", launchStr, "Enter")
	if err := launchCmd.Run(); err != nil {
		return SpawnEntry{}, fmt.Errorf("launching agent: %v", err)
	}

	// Persist entry
	entries, err := ReadSpawnEntries(session)
	if err != nil {
		return SpawnEntry{}, err
	}
	entries = append(entries, entry)
	if err := WriteSpawnEntries(session, entries); err != nil {
		return SpawnEntry{}, err
	}

	// Async: wait 2s then notify spawn to read inbox
	go func() {
		time.Sleep(2 * time.Second)
		_ = Notify(session, spawnRole)
	}()

	return entry, nil
}

// StopSpawn kills the tmux window for a spawn and marks it stopped.
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

		// Window is gone — mark completed
		entries[i].Status = "completed"
		entries[i].FinishedAt = time.Now().Unix()
		changed = true
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

// CleanFinishedSpawns removes all non-running spawn entries and their inbox files.
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
		removed++
	}

	if err := WriteSpawnEntries(session, kept); err != nil {
		return removed, err
	}

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

	b.WriteString(fmt.Sprintf("%-36s %-12s %-12s %-10s %-10s %s\n",
		"ID", "ROLE", "SPAWN-ROLE", "STATUS", "OWNER", "TASK"))
	b.WriteString(strings.Repeat("-", 110) + "\n")

	for _, e := range filtered {
		task := e.Task
		if len(task) > 40 {
			task = task[:37] + "..."
		}
		b.WriteString(fmt.Sprintf("%-36s %-12s %-12s %-10s %-10s %s\n",
			e.ID, e.Role, e.SpawnRole, e.Status, e.Owner, task))
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
	b.WriteString(fmt.Sprintf("  Started:    %s\n", time.Unix(entry.StartedAt, 0).Format("2006-01-02 15:04:05")))

	if entry.FinishedAt > 0 {
		b.WriteString(fmt.Sprintf("  Finished:   %s\n", time.Unix(entry.FinishedAt, 0).Format("2006-01-02 15:04:05")))
		duration := time.Duration(entry.FinishedAt-entry.StartedAt) * time.Second
		b.WriteString(fmt.Sprintf("  Duration:   %s\n", duration))
	}

	return b.String()
}

// findAgentLauncher locates the muxcode-agent.sh script.
// Checks: ~/.config/muxcode/scripts/, ~/.local/bin/, PATH.
func findAgentLauncher() (string, error) {
	// Check user config dir
	home, _ := os.UserHomeDir()
	candidates := []string{
		filepath.Join(home, ".config", "muxcode", "scripts", "muxcode-agent.sh"),
		filepath.Join(home, ".local", "bin", "muxcode-agent.sh"),
	}

	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}

	// Check PATH
	if p, err := exec.LookPath("muxcode-agent.sh"); err == nil {
		return p, nil
	}

	return "", fmt.Errorf("muxcode-agent.sh not found in ~/.config/muxcode/scripts/, ~/.local/bin/, or PATH")
}
