package bus

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// CronEntry represents a scheduled task in the cron system.
type CronEntry struct {
	ID        string `json:"id"`
	Schedule  string `json:"schedule"`
	Target    string `json:"target"`
	Action    string `json:"action"`
	Message   string `json:"message"`
	Enabled   bool   `json:"enabled"`
	CreatedAt int64  `json:"created_at"`
	LastRunTS int64  `json:"last_run_ts"`
	RunCount  int    `json:"run_count"`
}

// CronSchedule holds a parsed interval duration.
type CronSchedule struct {
	Interval time.Duration
}

// CronHistoryEntry records a single cron execution.
type CronHistoryEntry struct {
	CronID    string `json:"cron_id"`
	TS        int64  `json:"ts"`
	MessageID string `json:"message_id"`
	Target    string `json:"target"`
	Action    string `json:"action"`
}

// minCronInterval is the minimum allowed cron interval (30 seconds).
const minCronInterval = 30 * time.Second

// ParseSchedule parses a schedule string into a CronSchedule.
// Supported formats:
//   - "@every 30s", "@every 5m", "@every 1h", "@every 2h30m"
//   - "@hourly" (1h), "@daily" (24h), "@half-hourly" (30m)
//
// Case-insensitive. Minimum interval is 30s.
func ParseSchedule(s string) (CronSchedule, error) {
	lower := strings.ToLower(strings.TrimSpace(s))

	switch lower {
	case "@hourly":
		return CronSchedule{Interval: time.Hour}, nil
	case "@daily":
		return CronSchedule{Interval: 24 * time.Hour}, nil
	case "@half-hourly":
		return CronSchedule{Interval: 30 * time.Minute}, nil
	}

	if lower == "@every" || strings.HasPrefix(lower, "@every ") {
		durStr := strings.TrimPrefix(lower, "@every")
		durStr = strings.TrimSpace(durStr)
		if durStr == "" {
			return CronSchedule{}, fmt.Errorf("empty duration in @every")
		}
		d, err := time.ParseDuration(durStr)
		if err != nil {
			return CronSchedule{}, fmt.Errorf("invalid duration %q: %v", durStr, err)
		}
		if d < minCronInterval {
			return CronSchedule{}, fmt.Errorf("interval %v is below minimum %v", d, minCronInterval)
		}
		return CronSchedule{Interval: d}, nil
	}

	return CronSchedule{}, fmt.Errorf("unsupported schedule format: %q", s)
}

// CronDue returns true if a cron entry is due for execution at the given time.
func CronDue(entry CronEntry, now int64) bool {
	if !entry.Enabled {
		return false
	}
	sched, err := ParseSchedule(entry.Schedule)
	if err != nil {
		return false
	}
	intervalSecs := int64(sched.Interval / time.Second)
	if intervalSecs <= 0 {
		return false
	}

	// Never run before: due immediately
	if entry.LastRunTS == 0 {
		return true
	}

	return now-entry.LastRunTS >= intervalSecs
}

// ExecuteCron sends a bus message for a cron entry and returns the message ID.
func ExecuteCron(session string, entry CronEntry) (string, error) {
	msg := NewMessage("cron", entry.Target, "request", entry.Action, entry.Message, "")
	if err := Send(session, msg); err != nil {
		return "", fmt.Errorf("sending cron message: %v", err)
	}
	return msg.ID, nil
}

// ReadCronEntries reads all cron entries from the cron JSONL file.
func ReadCronEntries(session string) ([]CronEntry, error) {
	data, err := os.ReadFile(CronPath(session))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var entries []CronEntry
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var e CronEntry
		if err := json.Unmarshal(line, &e); err != nil {
			continue // skip malformed lines
		}
		entries = append(entries, e)
	}
	return entries, scanner.Err()
}

// WriteCronEntries overwrites the cron JSONL file with the given entries.
// TODO: Add file-level locking to prevent read-modify-write races between
// the watcher (UpdateLastRun) and CLI (add/remove/enable/disable).
// Low risk today — matches existing bus patterns and worst case is one
// extra or missed cron firing.
func WriteCronEntries(session string, entries []CronEntry) error {
	var buf bytes.Buffer
	for _, e := range entries {
		data, err := json.Marshal(e)
		if err != nil {
			return err
		}
		buf.Write(data)
		buf.WriteByte('\n')
	}
	return os.WriteFile(CronPath(session), buf.Bytes(), 0644)
}

// AddCronEntry validates and appends a new cron entry. Returns the entry with
// generated ID and CreatedAt fields populated.
func AddCronEntry(session string, entry CronEntry) (CronEntry, error) {
	// Validate schedule
	if _, err := ParseSchedule(entry.Schedule); err != nil {
		return CronEntry{}, fmt.Errorf("invalid schedule: %v", err)
	}

	// Validate target
	if !IsKnownRole(entry.Target) {
		return CronEntry{}, fmt.Errorf("unknown target role: %s", entry.Target)
	}

	entry.ID = NewMsgID("cron")
	entry.CreatedAt = time.Now().Unix()
	entry.Enabled = true

	entries, err := ReadCronEntries(session)
	if err != nil {
		return CronEntry{}, err
	}

	entries = append(entries, entry)
	if err := WriteCronEntries(session, entries); err != nil {
		return CronEntry{}, err
	}
	return entry, nil
}

// RemoveCronEntry removes a cron entry by ID.
func RemoveCronEntry(session, id string) error {
	entries, err := ReadCronEntries(session)
	if err != nil {
		return err
	}

	found := false
	var kept []CronEntry
	for _, e := range entries {
		if e.ID == id {
			found = true
			continue
		}
		kept = append(kept, e)
	}

	if !found {
		return fmt.Errorf("cron entry not found: %s", id)
	}

	return WriteCronEntries(session, kept)
}

// SetCronEnabled enables or disables a cron entry by ID.
func SetCronEnabled(session, id string, enabled bool) error {
	entries, err := ReadCronEntries(session)
	if err != nil {
		return err
	}

	found := false
	for i, e := range entries {
		if e.ID == id {
			entries[i].Enabled = enabled
			found = true
			break
		}
	}

	if !found {
		return fmt.Errorf("cron entry not found: %s", id)
	}

	return WriteCronEntries(session, entries)
}

// UpdateLastRun updates the last run timestamp and increments run count for a cron entry.
func UpdateLastRun(session, id string, ts int64) error {
	entries, err := ReadCronEntries(session)
	if err != nil {
		return err
	}

	found := false
	for i, e := range entries {
		if e.ID == id {
			entries[i].LastRunTS = ts
			entries[i].RunCount++
			found = true
			break
		}
	}

	if !found {
		return fmt.Errorf("cron entry not found: %s", id)
	}

	return WriteCronEntries(session, entries)
}

// AppendCronHistory appends a history entry to the cron history JSONL file.
func AppendCronHistory(session string, entry CronHistoryEntry) error {
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	return appendToFile(CronHistoryPath(session), append(data, '\n'))
}

// ReadCronHistory reads cron history entries, optionally filtered by cron ID.
// Pass empty cronID to read all entries.
func ReadCronHistory(session, cronID string) ([]CronHistoryEntry, error) {
	data, err := os.ReadFile(CronHistoryPath(session))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var entries []CronHistoryEntry
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var e CronHistoryEntry
		if err := json.Unmarshal(line, &e); err != nil {
			continue
		}
		if cronID != "" && e.CronID != cronID {
			continue
		}
		entries = append(entries, e)
	}
	return entries, scanner.Err()
}

// FormatCronList formats cron entries as a human-readable table.
// When showAll is false, only enabled entries are shown.
func FormatCronList(entries []CronEntry, showAll bool) string {
	var b strings.Builder

	var filtered []CronEntry
	for _, e := range entries {
		if showAll || e.Enabled {
			filtered = append(filtered, e)
		}
	}

	if len(filtered) == 0 {
		if showAll {
			b.WriteString("No cron entries.\n")
		} else {
			b.WriteString("No enabled cron entries. Use --all to see disabled entries.\n")
		}
		return b.String()
	}

	b.WriteString(fmt.Sprintf("%-40s %-14s %-10s %-10s %-8s %s\n",
		"ID", "Schedule", "Target", "Action", "Status", "Runs"))
	b.WriteString(strings.Repeat("-", 100) + "\n")

	for _, e := range filtered {
		status := "enabled"
		if !e.Enabled {
			status = "disabled"
		}
		b.WriteString(fmt.Sprintf("%-40s %-14s %-10s %-10s %-8s %d\n",
			e.ID, e.Schedule, e.Target, e.Action, status, e.RunCount))
	}

	return b.String()
}

// FormatCronHistory formats cron history entries as a human-readable table.
func FormatCronHistory(entries []CronHistoryEntry) string {
	var b strings.Builder

	if len(entries) == 0 {
		b.WriteString("No cron history.\n")
		return b.String()
	}

	b.WriteString(fmt.Sprintf("%-20s %-10s %-10s %s\n",
		"Time", "Target", "Action", "Message ID"))
	b.WriteString(strings.Repeat("-", 80) + "\n")

	for _, e := range entries {
		t := time.Unix(e.TS, 0).Format("2006-01-02 15:04:05")
		b.WriteString(fmt.Sprintf("%-20s %-10s %-10s %s\n",
			t, e.Target, e.Action, e.MessageID))
	}

	return b.String()
}
