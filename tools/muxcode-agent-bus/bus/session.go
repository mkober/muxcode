package bus

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// SessionMeta holds ephemeral session metadata for an agent role.
type SessionMeta struct {
	StartTS       int64 `json:"start_ts"`
	CompactCount  int   `json:"compact_count"`
	LastCompactTS int64 `json:"last_compact_ts"`
}

// SessionDir returns the session metadata directory for a bus session.
func SessionDir(session string) string {
	return filepath.Join(BusDir(session), "session")
}

// SessionMetaPath returns the session metadata file path for a role.
func SessionMetaPath(session, role string) string {
	return filepath.Join(SessionDir(session), role+".json")
}

// ReadSessionMeta reads session metadata for a role. Returns nil, nil if not found.
func ReadSessionMeta(session, role string) (*SessionMeta, error) {
	data, err := os.ReadFile(SessionMetaPath(session, role))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var meta SessionMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, err
	}
	return &meta, nil
}

// WriteSessionMeta writes session metadata for a role, creating the directory if needed.
func WriteSessionMeta(session, role string, meta *SessionMeta) error {
	dir := SessionDir(session)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	data, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	return os.WriteFile(SessionMetaPath(session, role), data, 0644)
}

// InitSessionMeta creates session metadata with StartTS=now if it does not exist.
// Idempotent — returns existing metadata if already present.
func InitSessionMeta(session, role string) (*SessionMeta, error) {
	existing, err := ReadSessionMeta(session, role)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return existing, nil
	}

	meta := &SessionMeta{
		StartTS: time.Now().Unix(),
	}
	if err := WriteSessionMeta(session, role, meta); err != nil {
		return nil, err
	}
	return meta, nil
}

// CompactSession saves a session summary to memory and updates session metadata.
func CompactSession(session, role, summary string) error {
	meta, err := InitSessionMeta(session, role)
	if err != nil {
		return err
	}

	if err := AppendMemory("Session Summary", summary, role); err != nil {
		return err
	}

	meta.CompactCount++
	meta.LastCompactTS = time.Now().Unix()
	return WriteSessionMeta(session, role, meta)
}

// SessionUptime returns the duration since the session started.
func SessionUptime(meta *SessionMeta) time.Duration {
	return time.Since(time.Unix(meta.StartTS, 0))
}

// ResumeContext reads the role's memory, filters to "Session Summary" entries,
// takes the last 3, and formats them as a markdown prompt block.
func ResumeContext(role string) (string, error) {
	content, err := ReadMemory(role)
	if err != nil {
		return "", err
	}
	if content == "" {
		return "", nil
	}

	entries := ParseMemoryEntries(content, role)
	var summaries []MemoryEntry
	for _, e := range entries {
		if e.Section == "Session Summary" {
			summaries = append(summaries, e)
		}
	}

	if len(summaries) == 0 {
		return "", nil
	}

	// Take last 3
	if len(summaries) > 3 {
		summaries = summaries[len(summaries)-3:]
	}

	var b strings.Builder
	b.WriteString("## Session Resume\n\n")
	b.WriteString("Previous session summaries (most recent last):\n\n")
	for _, s := range summaries {
		b.WriteString(fmt.Sprintf("### %s\n", s.Timestamp))
		b.WriteString(s.Content)
		b.WriteString("\n\n")
	}
	return b.String(), nil
}

// FormatSessionStatus formats session metadata as a human-readable status string.
func FormatSessionStatus(meta *SessionMeta, role string, msgCount int) string {
	var b strings.Builder

	uptime := SessionUptime(meta)
	hours := int(uptime.Hours())
	minutes := int(uptime.Minutes()) % 60

	b.WriteString(fmt.Sprintf("Session: %s\n", role))
	b.WriteString(fmt.Sprintf("Uptime:  %dh %dm\n", hours, minutes))
	b.WriteString(fmt.Sprintf("Compacts: %d\n", meta.CompactCount))

	if meta.LastCompactTS > 0 {
		last := time.Unix(meta.LastCompactTS, 0)
		b.WriteString(fmt.Sprintf("Last compact: %s\n", last.Format("2006-01-02 15:04")))
	}

	b.WriteString(fmt.Sprintf("Messages: %d\n", msgCount))

	return b.String()
}
