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

// AgentStatus represents the current state of an agent.
type AgentStatus struct {
	Role       string `json:"role"`
	Locked     bool   `json:"locked"`
	InboxCount int    `json:"inbox_count"`
	LastMsgTS  int64  `json:"last_msg_ts"`
	LastAction string `json:"last_action"`
	LastPeer   string `json:"last_peer"`
	LastDir    string `json:"last_dir"` // "sent" or "recv"
}

// GetAgentStatus returns the current status for a single agent role.
func GetAgentStatus(session, role string) AgentStatus {
	status := AgentStatus{
		Role:   role,
		Locked: IsLocked(session, role),
	}
	status.InboxCount = InboxCount(session, role)

	// Find the last log entry involving this role
	msgs := readLogForRole(session, role, 1)
	if len(msgs) > 0 {
		last := msgs[len(msgs)-1]
		status.LastMsgTS = last.TS
		status.LastAction = last.Action
		if last.To == role {
			status.LastPeer = last.From
			status.LastDir = "recv"
		} else {
			status.LastPeer = last.To
			status.LastDir = "sent"
		}
	}

	return status
}

// GetAllAgentStatus returns status for all known agent roles.
func GetAllAgentStatus(session string) []AgentStatus {
	statuses := make([]AgentStatus, 0, len(KnownRoles))
	for _, role := range KnownRoles {
		statuses = append(statuses, GetAgentStatus(session, role))
	}
	return statuses
}

// FormatStatusTable formats agent statuses as a human-readable table.
func FormatStatusTable(statuses []AgentStatus) string {
	var b strings.Builder

	// Header
	b.WriteString(fmt.Sprintf("%-12s %-6s %-6s %s\n", "ROLE", "STATE", "INBOX", "LAST ACTIVITY"))

	for _, s := range statuses {
		state := "idle"
		if s.Locked {
			state = "busy"
		}

		activity := "\u2014"
		if s.LastMsgTS > 0 {
			t := time.Unix(s.LastMsgTS, 0).Format("15:04")
			arrow := "\u2190" // recv
			if s.LastDir == "sent" {
				arrow = "\u2192" // sent
			}
			activity = fmt.Sprintf("%s %s %s:%s", t, arrow, s.LastPeer, s.LastAction)
		}

		b.WriteString(fmt.Sprintf("%-12s %-6s %-6d %s\n", s.Role, state, s.InboxCount, activity))
	}

	return b.String()
}

// ReadLogHistory reads messages from the session log involving a role.
// Returns the last `limit` messages where From == role or To == role.
func ReadLogHistory(session, role string, limit int) []Message {
	return readLogForRole(session, role, limit)
}

// FormatHistory formats messages as a human-readable history listing.
func FormatHistory(messages []Message, role string) string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf("--- Message history for %s (last %d) ---\n", role, len(messages)))

	for _, m := range messages {
		t := time.Unix(m.TS, 0).Format("15:04")
		b.WriteString(fmt.Sprintf("%s  %s \u2192 %s  [%s:%s] %s\n", t, m.From, m.To, m.Type, m.Action, m.Payload))
	}

	return b.String()
}

// ExtractContext reads the last N messages involving a role and formats them
// as a markdown block suitable for prompt injection.
func ExtractContext(session, role string, limit int) (string, error) {
	msgs := readLogForRole(session, role, limit)
	if len(msgs) == 0 {
		return "", nil
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("## Recent activity for %s\n\n", role))

	for _, m := range msgs {
		t := time.Unix(m.TS, 0).Format("15:04")
		if m.From == role {
			b.WriteString(fmt.Sprintf("- %s [%s to %s] %s\n", t, m.Type, m.To, m.Payload))
		} else {
			b.WriteString(fmt.Sprintf("- %s [%s from %s] %s\n", t, m.Type, m.From, m.Payload))
		}
	}

	return b.String(), nil
}

// FormatStatusJSON formats agent statuses as a JSON array.
func FormatStatusJSON(statuses []AgentStatus) (string, error) {
	data, err := json.MarshalIndent(statuses, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// readLogForRole reads the session log and returns the last `limit` messages
// involving the specified role (as sender or receiver).
func readLogForRole(session, role string, limit int) []Message {
	data, err := os.ReadFile(LogPath(session))
	if err != nil {
		return nil
	}

	var all []Message
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		m, err := DecodeMessage(line)
		if err != nil {
			continue
		}
		if m.From == role || m.To == role {
			all = append(all, m)
		}
	}

	// Return last `limit` entries
	if limit > 0 && len(all) > limit {
		all = all[len(all)-limit:]
	}

	return all
}
