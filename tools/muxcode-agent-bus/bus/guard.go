package bus

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"
)

// Pre-compiled regexes for command normalization (avoid recompiling on every call).
var (
	cmdNormCdRe    = regexp.MustCompile(`^cd\s+\S+\s*&&\s*`)
	cmdNormEnvRe   = regexp.MustCompile(`^([A-Z_][A-Z0-9_]*=[^\s]*\s+)+`)
	cmdNormBashRe  = regexp.MustCompile(`^bash\s+-c\s+`)
	cmdNormSpaceRe = regexp.MustCompile(`\s+`)
)

// HistoryEntry represents a single entry from a role's history JSONL file.
type HistoryEntry struct {
	TS       int64  `json:"ts"`
	Command  string `json:"command"`
	Summary  string `json:"summary"`
	ExitCode string `json:"exit_code"`
	Outcome  string `json:"outcome"`
	Output   string `json:"output"`
}

// LoopAlert describes a detected loop for an agent.
type LoopAlert struct {
	Role    string `json:"role"`
	Type    string `json:"type"`     // "command" or "message"
	Count   int    `json:"count"`    // number of repetitions
	Command string `json:"command"`  // repeated command (command loops)
	Peer    string `json:"peer"`     // other agent (message loops)
	Action  string `json:"action"`   // repeated action (message loops)
	Window  int64  `json:"window_s"` // time window in seconds
	Message string `json:"message"`  // human-readable description
}

// ReadHistory reads the last `limit` entries from a role's history JSONL file.
// Returns nil for missing or empty files.
func ReadHistory(session, role string, limit int) []HistoryEntry {
	data, err := os.ReadFile(HistoryPath(session, role))
	if err != nil {
		return nil
	}

	var all []HistoryEntry
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var entry HistoryEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			continue
		}
		all = append(all, entry)
	}

	if limit > 0 && len(all) > limit {
		all = all[len(all)-limit:]
	}

	return all
}

// normalizeCommand strips common prefixes and suffixes from a command string
// to prevent false negatives from trivially different command forms.
func normalizeCommand(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return ""
	}

	// Strip "cd ... &&" prefix
	cmd = cmdNormCdRe.ReplaceAllString(cmd, "")

	// Strip env var prefixes (FOO=bar)
	cmd = cmdNormEnvRe.ReplaceAllString(cmd, "")

	// Strip leading "bash -c "
	cmd = cmdNormBashRe.ReplaceAllString(cmd, "")

	// Strip trailing 2>&1
	cmd = strings.TrimSuffix(strings.TrimSpace(cmd), "2>&1")

	// Collapse whitespace
	cmd = cmdNormSpaceRe.ReplaceAllString(strings.TrimSpace(cmd), " ")

	return cmd
}

// DetectCommandLoop checks history entries for consecutive command failures.
// Returns an alert if the same normalized command failed >= threshold times
// within windowSecs of the most recent entry.
func DetectCommandLoop(entries []HistoryEntry, threshold int, windowSecs int64) *LoopAlert {
	if len(entries) == 0 || threshold < 1 {
		return nil
	}

	// Walk from most recent backward
	last := entries[len(entries)-1]
	if last.Outcome != "failure" {
		return nil
	}

	normalizedCmd := normalizeCommand(last.Command)
	if normalizedCmd == "" {
		return nil
	}

	count := 1
	earliest := last.TS

	for i := len(entries) - 2; i >= 0; i-- {
		e := entries[i]
		if e.Outcome != "failure" {
			break
		}
		if normalizeCommand(e.Command) != normalizedCmd {
			break
		}
		// Check time window from most recent entry
		if windowSecs > 0 && (last.TS-e.TS) > windowSecs {
			break
		}
		count++
		earliest = e.TS
	}

	if count < threshold {
		return nil
	}

	elapsed := last.TS - earliest
	return &LoopAlert{
		Type:    "command",
		Count:   count,
		Command: normalizedCmd,
		Window:  elapsed,
		Message: fmt.Sprintf("%s failed %dx in %s", normalizedCmd, count, formatDuration(elapsed)),
	}
}

// DetectMessageLoop checks log messages for repetitive patterns involving a role.
// Detects both repeated identical messages and ping-pong patterns.
// Only counts "request" type messages — "response" and "event" types are expected
// to repeat across chain cycles (build→test→review) and are not loops.
// Returns an alert if any pattern repeats >= threshold times within windowSecs.
func DetectMessageLoop(messages []Message, role string, threshold int, windowSecs int64) *LoopAlert {
	if len(messages) == 0 || threshold < 1 {
		return nil
	}

	now := messages[len(messages)-1].TS

	// Filter to request messages within the time window.
	// Responses and events repeat naturally across chain cycles and are not loops.
	// Watcher-originated messages are system-generated traffic (file-change events,
	// loop alerts, compaction alerts) — they repeat during active editing and are
	// not agent-to-agent loops.
	var recent []Message
	for _, m := range messages {
		if m.Type != "request" {
			continue
		}
		if m.From == "watcher" {
			continue
		}
		if windowSecs <= 0 || (now-m.TS) <= windowSecs {
			recent = append(recent, m)
		}
	}

	if len(recent) < threshold {
		return nil
	}

	// Check for repeated (from, to, action) tuples
	type tuple struct {
		from, to, action string
	}
	counts := make(map[tuple]int)
	for _, m := range recent {
		if m.From != role && m.To != role {
			continue
		}
		key := tuple{m.From, m.To, m.Action}
		counts[key]++
	}

	for key, count := range counts {
		if count >= threshold {
			peer := key.to
			if peer == role {
				peer = key.from
			}
			elapsed := now - recent[0].TS
			return &LoopAlert{
				Type:    "message",
				Count:   count,
				Peer:    peer,
				Action:  key.action,
				Window:  elapsed,
				Message: fmt.Sprintf("%s <-> %s action:%s repeated %dx in %s", key.from, key.to, key.action, count, formatDuration(elapsed)),
			}
		}
	}

	// Check for ping-pong: alternating A->B / B->A with same action
	for i := 0; i < len(recent)-1; i++ {
		a := recent[i]
		if a.From != role && a.To != role {
			continue
		}
		// Count alternating pairs starting from this message
		pongCount := 1
		action := a.Action
		prevFrom := a.From
		prevTo := a.To
		for j := i + 1; j < len(recent); j++ {
			b := recent[j]
			if b.Action != action {
				break
			}
			// Expect flip: prev.To == b.From and prev.From == b.To
			if b.From == prevTo && b.To == prevFrom {
				pongCount++
				prevFrom = b.From
				prevTo = b.To
			} else if b.From == prevFrom && b.To == prevTo {
				// Same direction — still a repetition
				pongCount++
			} else {
				break
			}
		}

		if pongCount >= threshold {
			peer := a.To
			if peer == role {
				peer = a.From
			}
			elapsed := now - a.TS
			return &LoopAlert{
				Type:    "message",
				Count:   pongCount,
				Peer:    peer,
				Action:  action,
				Window:  elapsed,
				Message: fmt.Sprintf("ping-pong %s <-> %s action:%s %dx in %s", role, peer, action, pongCount, formatDuration(elapsed)),
			}
		}
	}

	return nil
}

// CheckLoops runs all loop detection for a single role.
func CheckLoops(session, role string) []LoopAlert {
	var alerts []LoopAlert

	// Command loop detection (history file)
	entries := ReadHistory(session, role, 20)
	if alert := DetectCommandLoop(entries, 3, 300); alert != nil {
		alert.Role = role
		alerts = append(alerts, *alert)
	}

	// Message loop detection (log.jsonl)
	messages := readLogForRole(session, role, 50)
	if alert := DetectMessageLoop(messages, role, 4, 300); alert != nil {
		alert.Role = role
		alerts = append(alerts, *alert)
	}

	return alerts
}

// CheckAllLoops runs loop detection for all known roles.
func CheckAllLoops(session string) []LoopAlert {
	var alerts []LoopAlert
	for _, role := range KnownRoles {
		alerts = append(alerts, CheckLoops(session, role)...)
	}
	return alerts
}

// FormatAlerts formats loop alerts as human-readable text.
func FormatAlerts(alerts []LoopAlert) string {
	if len(alerts) == 0 {
		return "No loops detected.\n"
	}

	var b strings.Builder
	for _, a := range alerts {
		b.WriteString(fmt.Sprintf("\u26a0 LOOP DETECTED: %s\n", a.Role))
		b.WriteString(fmt.Sprintf("  Type: %s\n", a.Type))
		if a.Type == "command" {
			b.WriteString(fmt.Sprintf("  Command: %s (failed %dx in %s)\n", a.Command, a.Count, formatDuration(a.Window)))
			b.WriteString("  Action: Check build window \u2014 agent may be stuck\n")
		} else {
			b.WriteString(fmt.Sprintf("  Peer: %s  Action: %s (%dx in %s)\n", a.Peer, a.Action, a.Count, formatDuration(a.Window)))
			b.WriteString("  Action: Agents may be in a retry loop\n")
		}
		b.WriteString("\n")
	}
	return b.String()
}

// FormatAlertsJSON formats loop alerts as a JSON array.
func FormatAlertsJSON(alerts []LoopAlert) (string, error) {
	if alerts == nil {
		alerts = []LoopAlert{}
	}
	data, err := json.MarshalIndent(alerts, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// formatDuration converts seconds to a human-readable duration string.
func formatDuration(secs int64) string {
	if secs < 60 {
		return fmt.Sprintf("%ds", secs)
	}
	m := secs / 60
	s := secs % 60
	if s == 0 {
		return fmt.Sprintf("%dm", m)
	}
	return fmt.Sprintf("%dm%ds", m, s)
}

// AlertKey returns a dedup key for a loop alert.
func AlertKey(a LoopAlert) string {
	if a.Type == "command" {
		return fmt.Sprintf("%s:command:%s", a.Role, a.Command)
	}
	return fmt.Sprintf("%s:message:%s:%s", a.Role, a.Peer, a.Action)
}

// FilterNewAlerts filters alerts that haven't been seen within cooldownSecs.
// Updates the lastSeen map with current timestamps for new alerts.
func FilterNewAlerts(alerts []LoopAlert, lastSeen map[string]int64, cooldownSecs int64) []LoopAlert {
	now := time.Now().Unix()
	var fresh []LoopAlert
	for _, a := range alerts {
		key := AlertKey(a)
		if ts, ok := lastSeen[key]; ok && (now-ts) < cooldownSecs {
			continue
		}
		lastSeen[key] = now
		fresh = append(fresh, a)
	}
	return fresh
}
