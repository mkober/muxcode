package tui

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/mkober/muxcode/tools/muxcode-agent-bus/bus"
)

// logEntry is a minimal struct for parsing log.jsonl lines.
type logEntry struct {
	TS     int64  `json:"ts"`
	From   string `json:"from"`
	To     string `json:"to"`
	Type   string `json:"type"`
	Action string `json:"action"`
}

// RenderBus returns lines of ANSI-colored text showing bus state.
// inner is the usable width between box borders.
func RenderBus(session string, inner int) []string {
	busDir := bus.BusDir(session)
	if _, err := os.Stat(busDir); os.IsNotExist(err) {
		return []string{
			fmt.Sprintf("  %s(bus not initialized)%s", Comment, RST),
		}
	}

	var lines []string

	// Build individual inbox entries, then wrap into lines that fit.
	var entries []string
	for _, role := range bus.KnownRoles {
		count := bus.InboxCount(session, role)
		locked := ""
		if bus.IsLocked(session, role) {
			locked = "*"
		}

		color := Comment
		if count > 0 {
			color = Yellow
		}
		entries = append(entries, fmt.Sprintf("%s%s:%d%s%s", color, role, count, locked, RST))
	}

	// Wrap entries into lines that fit within inner width.
	// inner-2 leaves a 2-char right margin so text doesn't butt against the ║ border.
	currentLine := "  "
	currentVis := 2 // left indent matching inner-2 right margin
	for i, entry := range entries {
		entryVis := VisibleWidth(entry)
		sep := " "
		if i == 0 {
			sep = ""
		}
		needed := len(sep) + entryVis
		if currentVis+needed > inner-2 && currentVis > 2 {
			lines = append(lines, currentLine)
			currentLine = "  " + entry
			currentVis = 2 + entryVis
		} else {
			currentLine += sep + entry
			currentVis += needed
		}
	}
	if currentVis > 2 {
		lines = append(lines, currentLine)
	}

	// Last 3 log entries
	logPath := bus.LogPath(session)
	logLines := tailFile(logPath, 3)
	if len(logLines) > 0 {
		lines = append(lines, fmt.Sprintf("  %sRecent:%s", Comment, RST))
		for _, raw := range logLines {
			var entry logEntry
			if err := json.Unmarshal([]byte(raw), &entry); err != nil {
				lines = append(lines, fmt.Sprintf("  %s  (parse error)%s", Comment, RST))
				continue
			}
			ts := time.Unix(entry.TS, 0).Format("15:04:05")
			formatted := fmt.Sprintf("  %s %s->%s %s:%s", ts, entry.From, entry.To, entry.Type, entry.Action)
			lines = append(lines, fmt.Sprintf("  %s%s%s", Comment, formatted, RST))
		}
	} else {
		lines = append(lines, fmt.Sprintf("  %s(no activity)%s", Comment, RST))
	}

	return lines
}

// tailFile reads the last n non-empty lines from a file.
func tailFile(path string, n int) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	var allLines []string
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := scanner.Text()
		if line != "" {
			allLines = append(allLines, line)
		}
	}

	if len(allLines) <= n {
		return allLines
	}
	return allLines[len(allLines)-n:]
}
