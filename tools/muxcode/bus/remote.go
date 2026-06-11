package bus

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// RemoteSession describes a discovered muxcode session with its state.
type RemoteSession struct {
	Name       string `json:"name"`
	BusDir     string `json:"bus_dir"`
	TmuxAlive  bool   `json:"tmux_alive"`
	AgentCount int    `json:"agent_count"` // number of inbox files found
	LogSize    int64  `json:"log_size"`
	ProjectDir string `json:"project_dir"` // from session meta, if available
}

// DiscoverSessions finds all muxcode bus directories in /tmp and checks
// whether each has a live tmux session. Returns sessions sorted by name,
// excluding the current session if excludeSelf is true.
func DiscoverSessions(currentSession string, excludeSelf bool) ([]RemoteSession, error) {
	tmpDir := "/tmp"
	if busDirOverride != "" {
		tmpDir = busDirOverride
	}

	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", tmpDir, err)
	}

	var sessions []RemoteSession
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, "muxcode-bus-") {
			continue
		}
		sessionName := strings.TrimPrefix(name, "muxcode-bus-")
		if excludeSelf && sessionName == currentSession {
			continue
		}

		rs := RemoteSession{
			Name:   sessionName,
			BusDir: filepath.Join(tmpDir, name),
		}

		// Check tmux liveness
		rs.TmuxAlive = TmuxHasSession(sessionName)

		// Count inbox files
		inboxDir := filepath.Join(rs.BusDir, "inbox")
		if inboxEntries, err := os.ReadDir(inboxDir); err == nil {
			rs.AgentCount = len(inboxEntries)
		}

		// Log file size
		logPath := filepath.Join(rs.BusDir, "log.jsonl")
		if info, err := os.Stat(logPath); err == nil {
			rs.LogSize = info.Size()
		}

		// Project dir from tmux session start directory
		if rs.TmuxAlive {
			if dir, err := TmuxOutput("display-message", "-t", sessionName, "-p", "#{session_path}"); err == nil && dir != "" {
				rs.ProjectDir = dir
			}
		}

		sessions = append(sessions, rs)
	}

	return sessions, nil
}

// FormatSessionList formats discovered sessions as a human-readable table.
func FormatSessionList(sessions []RemoteSession, currentSession string) string {
	if len(sessions) == 0 {
		return "No muxcode sessions found.\n"
	}

	var b strings.Builder

	b.WriteString(fmt.Sprintf("\n  %-25s %-8s %-8s %-10s %s\n",
		"SESSION", "STATUS", "AGENTS", "LOG SIZE", "PROJECT"))
	b.WriteString("  " + strings.Repeat("─", 75) + "\n")

	for _, s := range sessions {
		status := "dead"
		if s.TmuxAlive {
			status = "alive"
		}

		marker := " "
		if s.Name == currentSession {
			marker = "*"
		}

		logSize := formatBytes(s.LogSize)

		project := s.ProjectDir
		if project == "" {
			project = "—"
		}
		// Shorten home dir prefix
		if home, err := os.UserHomeDir(); err == nil {
			project = strings.Replace(project, home, "~", 1)
		}

		b.WriteString(fmt.Sprintf(" %s%-25s %-8s %-8d %-10s %s\n",
			marker, s.Name, status, s.AgentCount, logSize, project))
	}
	b.WriteByte('\n')

	return b.String()
}

// RemoteAgentCapture captures the last N lines from a remote agent's tmux pane.
func RemoteAgentCapture(session, role string, lines int) (string, error) {
	target := PaneTarget(session, role)
	return TmuxCapturePaneLines(target, lines)
}

// RemoteAgentIsIdle checks if a remote agent is showing the idle prompt.
func RemoteAgentIsIdle(session, role string) bool {
	output, err := RemoteAgentCapture(session, role, 8)
	if err != nil {
		return false
	}
	return strings.Contains(output, "❯")
}

// RemoteInboxSummary returns a summary of a remote agent's inbox.
type RemoteInboxSummary struct {
	Role       string           `json:"role"`
	Count      int              `json:"count"`
	Actionable int              `json:"actionable"`
	Messages   []MessageSummary `json:"messages"`
}

// GetRemoteInbox reads and summarizes a remote agent's inbox.
func GetRemoteInbox(session, role string) RemoteInboxSummary {
	summary := RemoteInboxSummary{Role: role}

	msgs, err := Peek(session, role)
	if err != nil || len(msgs) == 0 {
		return summary
	}

	now := time.Now().Unix()
	summary.Count = len(msgs)
	for _, m := range msgs {
		if m.Type == "request" {
			summary.Actionable++
		}
		preview := m.Payload
		if len(preview) > 80 {
			preview = preview[:77] + "..."
		}
		summary.Messages = append(summary.Messages, MessageSummary{
			ID:      m.ID,
			From:    m.From,
			Type:    m.Type,
			Action:  m.Action,
			AgeSecs: now - m.TS,
			Preview: preview,
		})
	}

	return summary
}

// FormatRemoteInbox formats a remote inbox summary for display.
func FormatRemoteInbox(summary RemoteInboxSummary) string {
	var b strings.Builder

	if summary.Count == 0 {
		b.WriteString(fmt.Sprintf("  %s: empty inbox\n", summary.Role))
		return b.String()
	}

	b.WriteString(fmt.Sprintf("  %s: %d messages (%d actionable)\n",
		summary.Role, summary.Count, summary.Actionable))

	for _, m := range summary.Messages {
		age := formatDurationLong(m.AgeSecs)
		b.WriteString(fmt.Sprintf("    [%s] %s→%s %s:%s  %s ago  %s\n",
			m.ID[:8], m.From, summary.Role, m.Type, m.Action, age, m.Preview))
	}

	return b.String()
}

// formatDurationLong formats seconds as a human-readable duration
// including hours for longer durations.
func formatDurationLong(secs int64) string {
	switch {
	case secs < 60:
		return fmt.Sprintf("%ds", secs)
	case secs < 3600:
		return fmt.Sprintf("%dm%ds", secs/60, secs%60)
	default:
		return fmt.Sprintf("%dh%dm", secs/3600, (secs%3600)/60)
	}
}

// RemoteOverview generates a combined status + inbox overview for all agents
// in a remote session. This is the main entry point for "muxcode remote status".
func RemoteOverview(session string) string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf("\n  Session: %s\n", session))
	if TmuxHasSession(session) {
		b.WriteString("  Status:  alive\n")
	} else {
		b.WriteString("  Status:  dead (bus data available)\n")
	}
	b.WriteString("\n")

	// Agent status table
	statuses := GetAllAgentStatus(session)
	b.WriteString("  " + strings.Repeat("─", 75) + "\n")
	b.WriteString(fmt.Sprintf("  %-12s %-10s %-6s %-8s %-6s %s\n",
		"ROLE", "PROVIDER", "STATE", "HEALTH", "INBOX", "LAST ACTIVITY"))
	b.WriteString("  " + strings.Repeat("─", 75) + "\n")

	for _, s := range statuses {
		state := "idle"
		if s.Locked {
			state = "busy"
		}

		health := s.Health
		if health == "" {
			health = "—"
		}

		activity := "—"
		if s.LastMsgTS > 0 {
			t := time.Unix(s.LastMsgTS, 0).Format("15:04")
			arrow := "←" // recv
			if s.LastDir == "sent" {
				arrow = "→"
			}
			activity = fmt.Sprintf("%s %s %s:%s", t, arrow, s.LastPeer, s.LastAction)
		}

		provider := s.Provider
		if provider == "" {
			provider = "—"
		}

		b.WriteString(fmt.Sprintf("  %-12s %-10s %-6s %-8s %-6d %s\n",
			s.Role, provider, state, health, s.InboxCount, activity))
	}
	b.WriteString("\n")

	// Inbox summaries for agents with messages
	hasInbox := false
	for _, s := range statuses {
		if s.InboxCount > 0 {
			if !hasInbox {
				b.WriteString("  Pending Inboxes\n")
				b.WriteString("  " + strings.Repeat("─", 75) + "\n")
				hasInbox = true
			}
			summary := GetRemoteInbox(session, s.Role)
			b.WriteString(FormatRemoteInbox(summary))
		}
	}
	if !hasInbox {
		b.WriteString("  All inboxes empty\n")
	}
	b.WriteByte('\n')

	return b.String()
}

// TmuxListWindowNames returns the window names for a tmux session.
func TmuxListWindowNames(session string) ([]string, error) {
	out, err := TmuxOutput("list-windows", "-t", session, "-F", "#{window_name}")
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	return strings.Split(out, "\n"), nil
}
