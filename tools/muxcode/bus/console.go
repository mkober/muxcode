package bus

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Dracula color ANSI codes used by all console views.
const (
	ColorPurple = "\033[38;5;141m"
	ColorCyan   = "\033[38;5;117m"
	ColorGreen  = "\033[38;5;80m"
	ColorPink   = "\033[38;5;212m"
	ColorRed    = "\033[38;5;203m"
	ColorYellow = "\033[38;5;228m"
	ColorOrange = "\033[38;5;215m"
	ColorDim    = "\033[2m"
	ColorReset  = "\033[0m"
)

// Layout constants matching the shell scripts.
const (
	Pad         = "   "       // 3-space left padding
	ContPad     = "     "     // 5-space continuation indent
	EntryPad    = "         " // 9-space entry payload indent
	RightMargin = 2
)

// ConsoleEntry is a flexible JSONL entry that accommodates all role history formats.
// Fields are optional — each role uses a different subset.
type ConsoleEntry struct {
	TS          int64           `json:"ts"`
	Command     string          `json:"command"`
	Summary     string          `json:"summary"`
	Description string          `json:"description"`
	Changes     string          `json:"changes"`
	ExitCode    json.RawMessage `json:"exit_code"`
	Outcome     string          `json:"outcome"`
	Output      string          `json:"output"`
	Errors      string          `json:"errors"`
	// API-specific fields
	Method       string `json:"method"`
	URL          string `json:"url"`
	Status       int    `json:"status"`
	DurationMS   int    `json:"duration_ms"`
	Collection   string `json:"collection"`
	Request      string `json:"request"`
	ResponseBody string `json:"response_body"`
	// Watch/analyze fields
	Message string `json:"message"`
	Action  string `json:"action"`
	Payload string `json:"payload"`
	From    string `json:"from"`
	To      string `json:"to"`
	Type    string `json:"type"`
}

// ExitCodeStr returns the exit code as a string, handling both string and numeric JSON values.
func (e *ConsoleEntry) ExitCodeStr() string {
	if e.ExitCode == nil {
		return ""
	}
	s := strings.Trim(string(e.ExitCode), " \t\n")
	if s == "" || s == "null" {
		return ""
	}
	// Try as string first (strip quotes)
	var str string
	if err := json.Unmarshal(e.ExitCode, &str); err == nil {
		return str
	}
	// Try as number
	var num float64
	if err := json.Unmarshal(e.ExitCode, &num); err == nil {
		return strconv.Itoa(int(num))
	}
	return s
}

// IsPass returns true if exit code is "0" or empty.
func (e *ConsoleEntry) IsPass() bool {
	ec := e.ExitCodeStr()
	return ec == "0" || ec == ""
}

// ReadConsoleEntries reads and parses a JSONL file, returning the last `limit` entries.
func ReadConsoleEntries(path string, limit int) []ConsoleEntry {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	var all []ConsoleEntry
	scanner := bufio.NewScanner(bytes.NewReader(data))
	// Increase scanner buffer for large output fields
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var entry ConsoleEntry
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

// ReadAnalyzeEntries reads log.jsonl and filters for analyze agent response messages.
func ReadAnalyzeEntries(logPath string, limit int) []ConsoleEntry {
	data, err := os.ReadFile(logPath)
	if err != nil {
		return nil
	}

	var all []ConsoleEntry
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var entry ConsoleEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			continue
		}
		if entry.From == "analyze" && entry.Type == "response" {
			all = append(all, entry)
		}
	}

	if limit > 0 && len(all) > limit {
		all = all[len(all)-limit:]
	}
	return all
}

// TerminalWidth returns the current terminal width, defaulting to 80.
func TerminalWidth() int {
	cmd := exec.Command("tput", "cols")
	cmd.Stdin = os.Stdin
	out, err := cmd.Output()
	if err != nil {
		return 80
	}
	w, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil || w < 20 {
		return 80
	}
	return w
}

// TerminalHeight returns the current terminal height, defaulting to 50.
func TerminalHeight() int {
	cmd := exec.Command("tput", "lines")
	cmd.Stdin = os.Stdin
	out, err := cmd.Output()
	if err != nil {
		return 50
	}
	h, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil || h < 10 {
		return 50
	}
	return h
}

// TruncateToHeight truncates rendered output to fit within maxLines.
// It counts newlines and cuts at the limit, appending a dim "…" indicator
// if content was truncated. The caller should account for header lines
// when computing maxLines.
func TruncateToHeight(text string, maxLines int) string {
	if maxLines <= 0 {
		return text
	}
	lines := strings.Split(text, "\n")
	if len(lines) <= maxLines {
		return text
	}
	truncated := strings.Join(lines[:maxLines-1], "\n")
	return truncated + "\n" + Pad + ColorDim + "…" + ColorReset + "\n"
}

// WordWrap wraps text to the given width, breaking on spaces.
func WordWrap(text string, width int) []string {
	if width <= 0 {
		width = 80
	}
	var lines []string
	var line string

	for _, word := range strings.Fields(text) {
		if line == "" {
			line = word
		} else if len(line)+1+len(word) <= width {
			line += " " + word
		} else {
			lines = append(lines, line)
			line = word
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}

// FormatTimestamp formats a Unix timestamp to "Mon DD HH:MM:SS".
func FormatTimestamp(ts int64) string {
	if ts <= 0 {
		return "??? ?? ??:??:??"
	}
	return time.Unix(ts, 0).Format("Jan 02 15:04:05")
}

// FormatTimeOnly formats a Unix timestamp to "HH:MM:SS".
func FormatTimeOnly(ts int64) string {
	if ts <= 0 {
		return ""
	}
	return time.Unix(ts, 0).Format("15:04:05")
}

// StripANSI removes ANSI escape sequences from a string.
var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

func StripANSI(s string) string {
	return ansiRe.ReplaceAllString(s, "")
}

// Separator returns a horizontal rule of '─' characters.
func Separator(width int) string {
	return strings.Repeat("─", width)
}

// ConsoleConfig holds per-role rendering configuration.
type ConsoleConfig struct {
	Title       string // Display title (e.g. "Build", "Test")
	EmptyMsg    string // Message when no entries
	RecentLabel string // Label for recent entries section
	MaxRecent   int    // Number of recent entries to show (default 15)
	Renderer    func(cfg *ConsoleConfig, session string, width int) string
}

// DefaultConsoleConfigs returns the per-role console configurations.
func DefaultConsoleConfigs() map[string]*ConsoleConfig {
	return map[string]*ConsoleConfig{
		"build": {
			Title:       "Build",
			EmptyMsg:    "no builds yet",
			RecentLabel: "recent builds",
			MaxRecent:   15,
			Renderer:    renderBuildTest,
		},
		"test": {
			Title:       "Test",
			EmptyMsg:    "no tests yet",
			RecentLabel: "recent tests",
			MaxRecent:   15,
			Renderer:    renderBuildTest,
		},
		"review": {
			Title:       "Review",
			EmptyMsg:    "no reviews yet",
			RecentLabel: "recent reviews",
			MaxRecent:   15,
			Renderer:    renderReview,
		},
		"deploy": {
			Title:       "Deploy",
			EmptyMsg:    "no deployments yet",
			RecentLabel: "recent deployments",
			MaxRecent:   15,
			Renderer:    renderDeployRunner,
		},
		"run": {
			Title:       "Run",
			EmptyMsg:    "no executions yet",
			RecentLabel: "recent executions",
			MaxRecent:   15,
			Renderer:    renderDeployRunner,
		},
		"commit": {
			Title:       "Commit",
			EmptyMsg:    "no git operations yet",
			RecentLabel: "recent operations",
			MaxRecent:   15,
			Renderer:    renderCommit,
		},
		"watch": {
			Title:       "Watch",
			EmptyMsg:    "no events yet",
			RecentLabel: "",
			MaxRecent:   25,
			Renderer:    renderWatch,
		},
		"analyze": {
			Title:       "Analyze",
			EmptyMsg:    "no findings yet",
			RecentLabel: "recent findings",
			MaxRecent:   15,
			Renderer:    renderAnalyze,
		},
		"api": {
			Title:       "API",
			EmptyMsg:    "no requests yet",
			RecentLabel: "recent requests",
			MaxRecent:   15,
			Renderer:    renderAPI,
		},
		"agent": {
			Title:       "Agent",
			EmptyMsg:    "no activity yet",
			RecentLabel: "recent activity",
			MaxRecent:   25,
			Renderer:    renderAgent,
		},
	}
}

// RenderConsole generates the full terminal output for a role's console view.
func RenderConsole(role, session string, width int) string {
	configs := DefaultConsoleConfigs()
	cfg, ok := configs[role]
	if !ok {
		return fmt.Sprintf("%s%sUnknown role: %s%s\n", Pad, ColorRed, role, ColorReset)
	}
	return cfg.Renderer(cfg, session, width)
}

// ConsoleRoles returns all supported console role names.
func ConsoleRoles() []string {
	configs := DefaultConsoleConfigs()
	roles := make([]string, 0, len(configs))
	for r := range configs {
		roles = append(roles, r)
	}
	return roles
}

// --- Layout helpers ---

// ConsoleHeader renders the title bar with role name, timestamp, and separator.
func ConsoleHeader(title string, interval int, width int) string {
	sepWidth := width - len(Pad) - RightMargin
	if sepWidth < 10 {
		sepWidth = 10
	}
	now := time.Now().Format("15:04:05")
	var b strings.Builder
	b.WriteString(fmt.Sprintf("%s%s%s%s  %s%s%s  %s(every %ds)%s\n",
		Pad, ColorPurple, title, ColorReset,
		ColorDim, now, ColorReset,
		ColorDim, interval, ColorReset))
	b.WriteString(fmt.Sprintf("%s%s%s%s\n", Pad, ColorDim, Separator(sepWidth), ColorReset))
	b.WriteString("\n")
	return b.String()
}

// countNonEmpty returns the number of non-empty strings in the slice.
func countNonEmpty(lines []string) int {
	n := 0
	for _, l := range lines {
		if l != "" {
			n++
		}
	}
	return n
}

// limitFileList renders a tab-separated name-status list (git diff output),
// capping at maxFiles entries with a "… +N more files" overflow indicator.
// The formatLine callback receives (status, file) and returns the formatted line.
func limitFileList(raw string, maxFiles int, formatLine func(status, file string) string) string {
	lines := strings.Split(raw, "\n")
	var b strings.Builder
	shown := 0
	for _, line := range lines {
		if line == "" {
			continue
		}
		if shown >= maxFiles {
			remaining := countNonEmpty(lines[shown:])
			if remaining > 0 {
				b.WriteString(fmt.Sprintf("%s%s… +%d more files%s\n", ContPad, ColorDim, remaining, ColorReset))
			}
			break
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) == 2 {
			b.WriteString(formatLine(parts[0], parts[1]))
		}
		shown++
	}
	return b.String()
}

func emptyBlock(msg string) string {
	return fmt.Sprintf("%s%s%s%s\n", Pad, ColorDim, msg, ColorReset)
}

func summaryLine(total, pass, fail int) string {
	return fmt.Sprintf("%s%stotal%s %s%d%s  %spass%s %s%d%s  %sfail%s %s%d%s\n",
		Pad, ColorDim, ColorReset,
		ColorCyan, total, ColorReset,
		ColorDim, ColorReset,
		ColorGreen, pass, ColorReset,
		ColorDim, ColorReset,
		ColorRed, fail, ColorReset)
}

func calcContentWidth(paneWidth int) int {
	w := paneWidth - len(Pad) - RightMargin
	if w < 20 {
		w = 20
	}
	return w
}

func calcEntryContentWidth(paneWidth int) int {
	w := paneWidth - len(EntryPad) - RightMargin
	if w < 20 {
		w = 20
	}
	return w
}

// --- Renderers ---

// renderBuildTest handles build and test roles (nearly identical logic).
func renderBuildTest(cfg *ConsoleConfig, session string, width int) string {
	path := HistoryPath(session, strings.ToLower(cfg.Title))
	entries := ReadConsoleEntries(path, 0) // read all for stats
	cw := calcContentWidth(width)
	ecw := calcEntryContentWidth(width)

	var b strings.Builder

	if len(entries) == 0 {
		b.WriteString(emptyBlock(cfg.EmptyMsg))
		return b.String()
	}

	total := len(entries)
	pass := 0
	for _, e := range entries {
		if e.IsPass() {
			pass++
		}
	}
	fail := total - pass

	b.WriteString(summaryLine(total, pass, fail))
	b.WriteString("\n")

	// Recent entries
	b.WriteString(fmt.Sprintf("%s%s%s%s\n", Pad, ColorCyan, cfg.RecentLabel, ColorReset))
	recent := entries
	offset := 0
	if len(recent) > cfg.MaxRecent {
		offset = len(recent) - cfg.MaxRecent
		recent = recent[offset:]
	}

	for i, e := range recent {
		num := offset + i + 1
		ts := FormatTimestamp(e.TS)
		numLabel := fmt.Sprintf("#%-3d", num)
		ec := e.ExitCodeStr()

		if e.IsPass() {
			b.WriteString(fmt.Sprintf("%s%s%s%s %s%s%s  %sOK%s    %s\n",
				ContPad, ColorDim, numLabel, ColorReset,
				ColorDim, ts, ColorReset,
				ColorGreen, ColorReset, e.Command))
		} else {
			b.WriteString(fmt.Sprintf("%s%s%s%s %s%s%s  %sFAIL%s  %s  %sexit %s%s\n",
				ContPad, ColorDim, numLabel, ColorReset,
				ColorDim, ts, ColorReset,
				ColorRed, ColorReset, e.Command,
				ColorDim, ec, ColorReset))
		}

		// Detail line: prefer changes (build) or description
		detail := e.Changes
		if detail == "" {
			detail = e.Description
		}
		if detail != "" {
			for _, wline := range WordWrap(detail, ecw) {
				b.WriteString(fmt.Sprintf("%s%s↳ %s%s\n", EntryPad, ColorDim, wline, ColorReset))
			}
		}
	}
	b.WriteString("\n")

	// Last entry output
	last := entries[len(entries)-1]
	lastEC := last.ExitCodeStr()
	lastTS := FormatTimestamp(last.TS)

	displayOutput := last.Output
	if lastEC != "0" && last.Errors != "" {
		displayOutput = last.Errors
	}

	if displayOutput != "" || lastEC != "0" {
		if lastEC == "0" || lastEC == "" {
			b.WriteString(fmt.Sprintf("%s%s⏺ %s completed successfully%s  %s%s%s\n\n",
				Pad, ColorGreen, cfg.Title, ColorReset, ColorDim, lastTS, ColorReset))
		} else {
			b.WriteString(fmt.Sprintf("%s%s⏺ %s failed%s  %s%s%s\n",
				Pad, ColorRed, cfg.Title, ColorReset, ColorDim, lastTS, ColorReset))
			b.WriteString(fmt.Sprintf("%s%scmd%s  %s  %sexit %s%s\n\n",
				ContPad, ColorDim, ColorReset, last.Command, ColorDim, lastEC, ColorReset))
		}

		if displayOutput != "" {
			lineCount := 0
			wrapWidth := cw - len(ContPad) + len(Pad)
			for _, rawLine := range strings.Split(displayOutput, "\n") {
				oline := strings.TrimSpace(StripANSI(rawLine))
				if oline == "" || strings.HasPrefix(oline, "Exit code:") {
					continue
				}
				if lastEC != "0" {
					for _, wline := range WordWrap(oline, wrapWidth) {
						b.WriteString(fmt.Sprintf("%s%s- %s%s\n", ContPad, ColorRed, wline, ColorReset))
					}
				} else {
					if lineCount == 0 {
						for _, wline := range WordWrap(oline, cw) {
							b.WriteString(fmt.Sprintf("%s%s%s%s\n", Pad, ColorCyan, wline, ColorReset))
						}
					} else {
						for _, wline := range WordWrap(oline, wrapWidth) {
							b.WriteString(fmt.Sprintf("%s%s- %s%s\n", ContPad, ColorDim, wline, ColorReset))
						}
					}
				}
				lineCount++
				if lineCount >= 20 {
					break
				}
			}
			if lineCount == 0 && lastEC != "0" {
				b.WriteString(fmt.Sprintf("%s%s- No error details captured%s\n", ContPad, ColorDim, ColorReset))
			}
		}
		if lastEC == "0" || lastEC == "" {
			b.WriteString(fmt.Sprintf("%s%s- No errors or warnings%s\n", ContPad, ColorDim, ColorReset))
		}
		b.WriteString("\n")
	}

	// Previous failure (when latest passed and there were failures)
	if (lastEC == "0" || lastEC == "") && fail > 0 {
		var prevFail *ConsoleEntry
		for i := len(entries) - 2; i >= 0; i-- {
			if !entries[i].IsPass() {
				prevFail = &entries[i]
				break
			}
		}
		if prevFail != nil {
			pfTS := FormatTimestamp(prevFail.TS)
			pfEC := prevFail.ExitCodeStr()
			pfDisplay := prevFail.Errors
			if pfDisplay == "" {
				pfDisplay = prevFail.Output
			}

			b.WriteString(fmt.Sprintf("%s%s⏺ Last errors%s  %s%s%s\n",
				Pad, ColorYellow, ColorReset, ColorDim, pfTS, ColorReset))
			b.WriteString(fmt.Sprintf("%s%scmd%s  %s  %sexit %s%s\n",
				ContPad, ColorDim, ColorReset, prevFail.Command, ColorDim, pfEC, ColorReset))

			if pfDisplay != "" {
				pfLineCount := 0
				wrapWidth := cw - len(ContPad) + len(Pad)
				for _, rawLine := range strings.Split(pfDisplay, "\n") {
					oline := strings.TrimSpace(StripANSI(rawLine))
					if oline == "" || strings.HasPrefix(oline, "Exit code:") {
						continue
					}
					for _, wline := range WordWrap(oline, wrapWidth) {
						b.WriteString(fmt.Sprintf("%s%s- %s%s\n", ContPad, ColorYellow, wline, ColorReset))
					}
					pfLineCount++
					if pfLineCount >= 20 {
						break
					}
				}
			}
			b.WriteString("\n")
		}
	}

	return b.String()
}

// renderReview handles the review role.
func renderReview(cfg *ConsoleConfig, session string, width int) string {
	path := HistoryPath(session, "review")
	entries := ReadConsoleEntries(path, 0)
	cw := calcContentWidth(width)
	ecw := calcEntryContentWidth(width)

	var b strings.Builder

	if len(entries) == 0 {
		b.WriteString(emptyBlock(cfg.EmptyMsg))
		return b.String()
	}

	total := len(entries)
	clean := 0
	for _, e := range entries {
		if e.IsPass() {
			clean++
		}
	}
	issues := total - clean

	b.WriteString(fmt.Sprintf("%s%stotal%s %s%d%s  %sclean%s %s%d%s  %sissues%s %s%d%s\n",
		Pad, ColorDim, ColorReset,
		ColorCyan, total, ColorReset,
		ColorDim, ColorReset,
		ColorGreen, clean, ColorReset,
		ColorDim, ColorReset,
		ColorRed, issues, ColorReset))
	b.WriteString("\n")

	// Recent reviews
	b.WriteString(fmt.Sprintf("%s%s%s%s\n", Pad, ColorCyan, cfg.RecentLabel, ColorReset))
	recent := entries
	offset := 0
	if len(recent) > cfg.MaxRecent {
		offset = len(recent) - cfg.MaxRecent
		recent = recent[offset:]
	}

	mustFixRe := regexp.MustCompile(`(\d+)\s*must-fix`)
	shouldFixRe := regexp.MustCompile(`(\d+)\s*should-fix`)
	nitRe := regexp.MustCompile(`(\d+)\s*nit`)

	for i, e := range recent {
		num := offset + i + 1
		ts := FormatTimestamp(e.TS)
		numLabel := fmt.Sprintf("#%-3d", num)

		if e.IsPass() {
			b.WriteString(fmt.Sprintf("%s%s%s%s %s%s%s  %sOK%s",
				ContPad, ColorDim, numLabel, ColorReset,
				ColorDim, ts, ColorReset,
				ColorGreen, ColorReset))
			if e.Summary != "" {
				b.WriteString(fmt.Sprintf("    %s", e.Summary))
			}
			b.WriteString("\n")
		} else {
			b.WriteString(fmt.Sprintf("%s%s%s%s %s%s%s  %sISSUES%s",
				ContPad, ColorDim, numLabel, ColorReset,
				ColorDim, ts, ColorReset,
				ColorRed, ColorReset))

			// Extract issue counts from summary
			summary := e.Summary
			parts := []string{}
			if m := mustFixRe.FindStringSubmatch(summary); len(m) > 1 {
				parts = append(parts, fmt.Sprintf("%s%s must-fix%s", ColorRed, m[1], ColorReset))
			}
			if m := shouldFixRe.FindStringSubmatch(summary); len(m) > 1 {
				parts = append(parts, fmt.Sprintf("%s%s should-fix%s", ColorYellow, m[1], ColorReset))
			}
			if m := nitRe.FindStringSubmatch(summary); len(m) > 1 {
				parts = append(parts, fmt.Sprintf("%s%s nit%s", ColorCyan, m[1], ColorReset))
			}
			if len(parts) > 0 {
				b.WriteString("  " + strings.Join(parts, " "))
			}
			b.WriteString("\n")
		}
	}
	b.WriteString("\n")

	// Last review output
	last := entries[len(entries)-1]
	displayOutput := last.Output
	if displayOutput == "" {
		displayOutput = last.Summary
	}

	if displayOutput != "" {
		lastEC := last.ExitCodeStr()
		if lastEC == "0" || lastEC == "" {
			b.WriteString(fmt.Sprintf("%s%s⏺ Review completed%s\n\n", Pad, ColorGreen, ColorReset))
		} else {
			b.WriteString(fmt.Sprintf("%s%s⏺ Review found issues%s\n\n", Pad, ColorRed, ColorReset))
		}

		lineCount := 0
		wrapWidth := cw - len(ContPad) + len(Pad)
		for _, rawLine := range strings.Split(displayOutput, "\n") {
			oline := strings.TrimSpace(StripANSI(rawLine))
			if oline == "" {
				continue
			}
			if lineCount == 0 {
				for _, wline := range WordWrap(oline, cw) {
					b.WriteString(fmt.Sprintf("%s%s%s%s\n", Pad, ColorCyan, wline, ColorReset))
				}
			} else {
				for _, wline := range WordWrap(oline, wrapWidth) {
					b.WriteString(fmt.Sprintf("%s%s- %s%s\n", ContPad, ColorDim, wline, ColorReset))
				}
			}
			lineCount++
			if lineCount >= 20 {
				break
			}
		}
		b.WriteString("\n")
	}

	_ = ecw
	return b.String()
}

// renderDeployRunner handles deploy and run roles.
func renderDeployRunner(cfg *ConsoleConfig, session string, width int) string {
	roleName := strings.ToLower(cfg.Title)
	path := HistoryPath(session, roleName)
	entries := ReadConsoleEntries(path, 0)
	cw := calcContentWidth(width)
	ecw := calcEntryContentWidth(width)

	var b strings.Builder

	if len(entries) == 0 {
		b.WriteString(emptyBlock(cfg.EmptyMsg))
		return b.String()
	}

	total := len(entries)
	pass := 0
	for _, e := range entries {
		if e.IsPass() {
			pass++
		}
	}
	fail := total - pass

	b.WriteString(summaryLine(total, pass, fail))
	b.WriteString("\n")

	// Recent entries
	b.WriteString(fmt.Sprintf("%s%s%s%s\n", Pad, ColorCyan, cfg.RecentLabel, ColorReset))
	recent := entries
	offset := 0
	if len(recent) > cfg.MaxRecent {
		offset = len(recent) - cfg.MaxRecent
		recent = recent[offset:]
	}

	for i, e := range recent {
		num := offset + i + 1
		ts := FormatTimestamp(e.TS)
		numLabel := fmt.Sprintf("#%-3d", num)
		ec := e.ExitCodeStr()

		if e.IsPass() {
			b.WriteString(fmt.Sprintf("%s%s%s%s %s%s%s  %sOK%s    %s\n",
				ContPad, ColorDim, numLabel, ColorReset,
				ColorDim, ts, ColorReset,
				ColorGreen, ColorReset, e.Command))
		} else {
			b.WriteString(fmt.Sprintf("%s%s%s%s %s%s%s  %sFAIL%s  %s  %sexit %s%s\n",
				ContPad, ColorDim, numLabel, ColorReset,
				ColorDim, ts, ColorReset,
				ColorRed, ColorReset, e.Command,
				ColorDim, ec, ColorReset))
		}

		detail := e.Description
		if detail != "" {
			for _, wline := range WordWrap(detail, ecw) {
				b.WriteString(fmt.Sprintf("%s%s↳ %s%s\n", EntryPad, ColorDim, wline, ColorReset))
			}
		}
	}
	b.WriteString("\n")

	// Last output — prefer errors field on failure
	last := entries[len(entries)-1]
	lastEC := last.ExitCodeStr()
	displayOutput := last.Output
	if lastEC != "0" && lastEC != "" && last.Errors != "" {
		displayOutput = last.Errors
	}
	if displayOutput != "" {
		if lastEC == "0" || lastEC == "" {
			successLabel := cfg.Title + " succeeded"
			if cfg.Title == "Run" {
				successLabel = "Execution completed"
			}
			b.WriteString(fmt.Sprintf("%s%s⏺ %s%s\n\n", Pad, ColorGreen, successLabel, ColorReset))
		} else {
			failLabel := cfg.Title + " failed"
			b.WriteString(fmt.Sprintf("%s%s⏺ %s%s\n\n", Pad, ColorRed, failLabel, ColorReset))
		}

		lineCount := 0
		for _, rawLine := range strings.Split(displayOutput, "\n") {
			oline := strings.TrimSpace(StripANSI(rawLine))
			if oline == "" {
				continue
			}
			if lineCount == 0 {
				for _, wline := range WordWrap(oline, cw) {
					if lastEC != "0" && lastEC != "" {
						b.WriteString(fmt.Sprintf("%s%s%s%s\n", Pad, ColorRed, wline, ColorReset))
					} else {
						b.WriteString(fmt.Sprintf("%s%s%s%s\n", Pad, ColorCyan, wline, ColorReset))
					}
				}
			} else {
				wrapWidth := cw - len(ContPad) + len(Pad)
				color := ColorDim
				if lastEC != "0" && lastEC != "" {
					color = ColorRed
				}
				for _, wline := range WordWrap(oline, wrapWidth) {
					b.WriteString(fmt.Sprintf("%s%s- %s%s\n", ContPad, color, wline, ColorReset))
				}
			}
			lineCount++
			if lineCount >= 20 {
				break
			}
		}
		b.WriteString("\n")
	}

	_ = ecw
	return b.String()
}

// renderCommit handles the commit role (git status + history).
func renderCommit(cfg *ConsoleConfig, session string, width int) string {
	cw := calcContentWidth(width)
	ecw := calcEntryContentWidth(width)
	sepWidth := width - len(Pad) - RightMargin
	if sepWidth < 10 {
		sepWidth = 10
	}

	var b strings.Builder

	// ── Git status section ──
	b.WriteString(renderGitStatus())

	// Separator between git status and history
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("%s%s%s%s\n", Pad, ColorDim, Separator(sepWidth), ColorReset))
	b.WriteString("\n")

	// ── Commit history section ──
	path := HistoryPath(session, "commit")
	entries := ReadConsoleEntries(path, 0)

	if len(entries) == 0 {
		b.WriteString(emptyBlock(cfg.EmptyMsg))
		return b.String()
	}

	total := len(entries)
	pass := 0
	for _, e := range entries {
		if e.IsPass() {
			pass++
		}
	}
	fail := total - pass

	b.WriteString(summaryLine(total, pass, fail))
	b.WriteString("\n")

	b.WriteString(fmt.Sprintf("%s%s%s%s\n", Pad, ColorCyan, cfg.RecentLabel, ColorReset))
	recent := entries
	offset := 0
	if len(recent) > cfg.MaxRecent {
		offset = len(recent) - cfg.MaxRecent
		recent = recent[offset:]
	}

	for i, e := range recent {
		num := offset + i + 1
		ts := FormatTimestamp(e.TS)
		numLabel := fmt.Sprintf("#%-3d", num)
		ec := e.ExitCodeStr()

		if e.IsPass() {
			b.WriteString(fmt.Sprintf("%s%s%s%s %s%s%s  %sOK%s    %s\n",
				ContPad, ColorDim, numLabel, ColorReset,
				ColorDim, ts, ColorReset,
				ColorGreen, ColorReset, e.Command))
		} else {
			b.WriteString(fmt.Sprintf("%s%s%s%s %s%s%s  %sFAIL%s  %s  %sexit %s%s\n",
				ContPad, ColorDim, numLabel, ColorReset,
				ColorDim, ts, ColorReset,
				ColorRed, ColorReset, e.Command,
				ColorDim, ec, ColorReset))
		}

		// Prefer summary over description for commit role
		detail := e.Summary
		if detail == "" {
			detail = e.Description
		}
		if detail != "" {
			firstLine := true
			for _, wline := range WordWrap(detail, ecw) {
				if firstLine {
					b.WriteString(fmt.Sprintf("%s%s↳ %s%s\n", EntryPad, ColorDim, wline, ColorReset))
					firstLine = false
				} else {
					b.WriteString(fmt.Sprintf("%s%s  %s%s\n", EntryPad, ColorDim, wline, ColorReset))
				}
			}
		}
	}
	b.WriteString("\n")

	// Last operation output
	last := entries[len(entries)-1]
	lastEC := last.ExitCodeStr()
	if last.Output != "" {
		if lastEC == "0" || lastEC == "" {
			b.WriteString(fmt.Sprintf("%s%s⏺ Operation completed:%s\n\n", Pad, ColorGreen, ColorReset))
		} else {
			b.WriteString(fmt.Sprintf("%s%s⏺ Operation failed:%s\n\n", Pad, ColorRed, ColorReset))
		}

		firstLine := true
		lineCount := 0
		for _, rawLine := range strings.Split(last.Output, "\n") {
			oline := strings.TrimSpace(StripANSI(rawLine))
			if oline == "" {
				continue
			}
			if firstLine {
				for _, wline := range WordWrap(oline, cw) {
					b.WriteString(fmt.Sprintf("%s%s%s%s\n", Pad, ColorCyan, wline, ColorReset))
				}
				firstLine = false
			} else {
				wrapWidth := cw - 2
				for _, wline := range WordWrap(oline, wrapWidth) {
					b.WriteString(fmt.Sprintf("%s%s- %s%s\n", ContPad, ColorDim, wline, ColorReset))
				}
			}
			lineCount++
			if lineCount >= 20 {
				break
			}
		}
		b.WriteString("\n")
	}

	// Last failure detail (no output captured)
	if lastEC != "0" && lastEC != "" && last.Output == "" {
		b.WriteString(fmt.Sprintf("%s%slast failure%s\n", Pad, ColorRed, ColorReset))
		b.WriteString(fmt.Sprintf("%s%scmd%s   %s\n", ContPad, ColorDim, ColorReset, last.Command))
		b.WriteString(fmt.Sprintf("%s%sexit%s  %s\n", ContPad, ColorDim, ColorReset, lastEC))
	}

	return b.String()
}

// renderGitStatus generates the git status section for the commit console view.
func renderGitStatus() string {
	var b strings.Builder

	// Branch info
	branch := gitCmd("branch", "--show-current")
	if branch != "" {
		b.WriteString(fmt.Sprintf("%s%sbranch%s  %s\n", Pad, ColorCyan, ColorReset, branch))

		upstream := gitCmd("rev-parse", "--abbrev-ref", "@{upstream}")
		if upstream != "" {
			ahead := gitCmd("rev-list", "--count", "@{upstream}..HEAD")
			behind := gitCmd("rev-list", "--count", "HEAD..@{upstream}")
			aheadN, _ := strconv.Atoi(ahead)
			behindN, _ := strconv.Atoi(behind)
			if aheadN > 0 || behindN > 0 {
				b.WriteString(fmt.Sprintf("%s%sremote%s  ↑%d ↓%d (%s)\n",
					Pad, ColorCyan, ColorReset, aheadN, behindN, upstream))
			}
		}
		b.WriteString("\n")
	}

	// Staged files (cap to avoid overflow)
	staged := gitCmd("diff", "--cached", "--name-status")
	if staged != "" {
		b.WriteString(fmt.Sprintf("%s%sstaged%s\n", Pad, ColorGreen, ColorReset))
		b.WriteString(limitFileList(staged, 10, func(status, file string) string {
			return fmt.Sprintf("%s%s%s%s  %s\n", ContPad, ColorGreen, status, ColorReset, file)
		}))
		b.WriteString("\n")
	}

	// Unstaged changes (cap to avoid overflow)
	unstaged := gitCmd("diff", "--name-status")
	if unstaged != "" {
		b.WriteString(fmt.Sprintf("%s%smodified%s\n", Pad, ColorPink, ColorReset))
		b.WriteString(limitFileList(unstaged, 10, func(status, file string) string {
			return fmt.Sprintf("%s%s%s%s  %s\n", ContPad, ColorPink, status, ColorReset, file)
		}))
		b.WriteString("\n")
	}

	// Untracked files (cap to avoid overflow)
	untracked := gitCmd("ls-files", "--others", "--exclude-standard")
	if untracked != "" {
		b.WriteString(fmt.Sprintf("%s%suntracked%s\n", Pad, ColorDim, ColorReset))
		shown := 0
		maxFiles := 10
		fileLines := strings.Split(untracked, "\n")
		for _, file := range fileLines {
			if file == "" {
				continue
			}
			if shown >= maxFiles {
				remaining := countNonEmpty(fileLines[shown:])
				if remaining > 0 {
					b.WriteString(fmt.Sprintf("%s%s… +%d more files%s\n", ContPad, ColorDim, remaining, ColorReset))
				}
				break
			}
			b.WriteString(fmt.Sprintf("%s%s?%s  %s\n", ContPad, ColorDim, ColorReset, file))
			shown++
		}
		b.WriteString("\n")
	}

	// Clean tree
	if staged == "" && unstaged == "" && untracked == "" {
		b.WriteString(fmt.Sprintf("%s%sclean working tree%s\n", Pad, ColorGreen, ColorReset))
		b.WriteString("\n")
	}

	// Last commit (cap file list to avoid overflow)
	lastCommit := gitCmd("log", "-1", "--format=%h %s")
	if lastCommit != "" {
		b.WriteString(fmt.Sprintf("%s%slast commit%s  %s\n", Pad, ColorCyan, ColorReset, lastCommit))
		lastFiles := gitCmd("diff-tree", "--no-commit-id", "--name-status", "-r", "HEAD")
		if lastFiles != "" {
			b.WriteString(limitFileList(lastFiles, 10, func(status, file string) string {
				color := ColorYellow
				if strings.HasPrefix(status, "A") {
					color = ColorGreen
				} else if strings.HasPrefix(status, "D") {
					color = ColorRed
				}
				return fmt.Sprintf("%s%s%s%s  %s\n", ContPad, color, status, ColorReset, file)
			}))
		}
		b.WriteString("\n")
	}

	return b.String()
}

// renderWatch handles the watch role.
func renderWatch(cfg *ConsoleConfig, session string, width int) string {
	path := HistoryPath(session, "watch")
	entries := ReadConsoleEntries(path, cfg.MaxRecent)

	contIndent := 13 // PAD + "HH:MM:SS" + "  " = 3+8+2
	maxWidth := width - contIndent - RightMargin
	if maxWidth < 20 {
		maxWidth = 20
	}

	var b strings.Builder

	if len(entries) == 0 {
		b.WriteString(emptyBlock(cfg.EmptyMsg))
		return b.String()
	}

	b.WriteString(fmt.Sprintf("%s%s%d entries%s\n\n", Pad, ColorDim, len(entries), ColorReset))

	for _, e := range entries {
		ts := FormatTimeOnly(e.TS)
		text := e.Summary
		if text == "" {
			text = e.Message
		}

		// Determine color based on content keywords
		color := ColorCyan
		lower := strings.ToLower(text)
		switch {
		case containsAny(lower, "error", "fail", "crash", "panic", "fatal"):
			color = ColorRed
		case containsAny(lower, "warn"):
			color = ColorOrange
		case containsAny(lower, "success", "ok", "healthy", "running"):
			color = ColorGreen
		}

		lines := WordWrap(text, maxWidth)
		if len(lines) > 0 {
			b.WriteString(fmt.Sprintf("%s%s%s%s  %s%s%s\n",
				Pad, ColorDim, ts, ColorReset, color, lines[0], ColorReset))
			indent := Pad + strings.Repeat(" ", 10) // match timestamp+gap width
			for _, wline := range lines[1:] {
				b.WriteString(fmt.Sprintf("%s%s%s%s\n", indent, color, wline, ColorReset))
			}
		}
	}

	return b.String()
}

// renderAnalyze handles the analyze role (reads from log.jsonl, not history).
func renderAnalyze(cfg *ConsoleConfig, session string, width int) string {
	logPath := LogPath(session)
	entries := ReadAnalyzeEntries(logPath, 0)
	ecw := calcEntryContentWidth(width)

	var b strings.Builder

	if len(entries) == 0 {
		b.WriteString(emptyBlock(cfg.EmptyMsg))
		b.WriteString(fmt.Sprintf("%s%swaiting for analyst agent...%s\n", Pad, ColorDim, ColorReset))
		return b.String()
	}

	total := len(entries)
	b.WriteString(fmt.Sprintf("%s%sfindings%s %s%d%s\n\n", Pad, ColorDim, ColorReset, ColorCyan, total, ColorReset))

	// Latest finding (full payload)
	latest := entries[len(entries)-1]
	if latest.Payload != "" {
		b.WriteString(fmt.Sprintf("%s%slatest finding%s\n", Pad, ColorCyan, ColorReset))
		for _, rawLine := range strings.Split(latest.Payload, "\n") {
			line := strings.TrimSpace(rawLine)
			if line == "" {
				continue
			}
			for _, wline := range WordWrap(line, ecw) {
				b.WriteString(fmt.Sprintf("%s%s%s%s\n", ContPad, ColorDim, wline, ColorReset))
			}
		}
		b.WriteString("\n")
	}

	// Recent findings
	b.WriteString(fmt.Sprintf("%s%s%s%s\n", Pad, ColorCyan, cfg.RecentLabel, ColorReset))
	recent := entries
	offset := 0
	if len(recent) > cfg.MaxRecent {
		offset = len(recent) - cfg.MaxRecent
		recent = recent[offset:]
	}

	for i, e := range recent {
		num := offset + i + 1
		ts := FormatTimestamp(e.TS)
		numLabel := fmt.Sprintf("#%-3d", num)
		toAgent := e.To
		if toAgent == "" {
			toAgent = "?"
		}

		b.WriteString(fmt.Sprintf("%s%s%s%s %s%s%s  %s → %s\n",
			ContPad, ColorDim, numLabel, ColorReset,
			ColorDim, ts, ColorReset,
			e.Action, toAgent))

		if e.Payload != "" {
			firstLine := strings.TrimSpace(strings.Split(e.Payload, "\n")[0])
			if firstLine != "" {
				for _, wline := range WordWrap(firstLine, ecw) {
					b.WriteString(fmt.Sprintf("%s%s↳ %s%s\n", EntryPad, ColorDim, wline, ColorReset))
				}
			}
		}
	}

	return b.String()
}

// renderAPI handles the api role with HTTP-specific formatting.
func renderAPI(cfg *ConsoleConfig, session string, width int) string {
	path := ".muxcode/api/history.jsonl" // project-local, not in bus dir
	entries := ReadConsoleEntries(path, 0)
	ecw := calcEntryContentWidth(width)

	var b strings.Builder

	if len(entries) == 0 {
		b.WriteString(emptyBlock(cfg.EmptyMsg))

		// Show environment and collection counts
		envCount := countFiles(".muxcode/api/environments", ".json")
		colCount := countFiles(".muxcode/api/collections", ".json")
		if envCount > 0 || colCount > 0 {
			b.WriteString(fmt.Sprintf("\n%s%senvs%s %s%d%s  %scollections%s %s%d%s\n",
				Pad, ColorDim, ColorReset, ColorCyan, envCount, ColorReset,
				ColorDim, ColorReset, ColorCyan, colCount, ColorReset))
		}
		return b.String()
	}

	total := len(entries)
	ok := 0
	for _, e := range entries {
		if e.Status >= 200 && e.Status < 400 {
			ok++
		}
	}
	errCount := total - ok

	b.WriteString(fmt.Sprintf("%s%stotal%s %s%d%s  %sok%s %s%d%s  %serr%s %s%d%s\n",
		Pad, ColorDim, ColorReset,
		ColorCyan, total, ColorReset,
		ColorDim, ColorReset,
		ColorGreen, ok, ColorReset,
		ColorDim, ColorReset,
		ColorRed, errCount, ColorReset))

	// Environment and collection counts
	envCount := countFiles(".muxcode/api/environments", ".json")
	colCount := countFiles(".muxcode/api/collections", ".json")
	b.WriteString(fmt.Sprintf("%s%senvs%s %s%d%s  %scollections%s %s%d%s\n",
		Pad, ColorDim, ColorReset, ColorCyan, envCount, ColorReset,
		ColorDim, ColorReset, ColorCyan, colCount, ColorReset))
	b.WriteString("\n")

	// Recent requests
	b.WriteString(fmt.Sprintf("%s%s%s%s\n", Pad, ColorCyan, cfg.RecentLabel, ColorReset))
	recent := entries
	offset := 0
	if len(recent) > cfg.MaxRecent {
		offset = len(recent) - cfg.MaxRecent
		recent = recent[offset:]
	}

	for i, e := range recent {
		num := offset + i + 1
		ts := FormatTimestamp(e.TS)
		numLabel := fmt.Sprintf("#%-3d", num)
		methodColor := httpMethodColor(e.Method)
		statusColor := httpStatusColor(e.Status)
		dur := fmt.Sprintf("%dms", e.DurationMS)

		b.WriteString(fmt.Sprintf("%s%s%s%s %s%s%s  %s%-6s%s %s%d%s %s%s%s\n",
			ContPad, ColorDim, numLabel, ColorReset,
			ColorDim, ts, ColorReset,
			methodColor, e.Method, ColorReset,
			statusColor, e.Status, ColorReset,
			ColorDim, dur, ColorReset))

		// URL (word-wrapped)
		for _, wline := range WordWrap(e.URL, ecw) {
			b.WriteString(fmt.Sprintf("%s%s%s%s\n", EntryPad, ColorDim, wline, ColorReset))
		}

		// Collection/request name
		if e.Collection != "" && e.Request != "" {
			detail := e.Collection + "/" + e.Request
			firstLine := true
			for _, wline := range WordWrap(detail, ecw) {
				if firstLine {
					b.WriteString(fmt.Sprintf("%s%s↳ %s%s\n", EntryPad, ColorDim, wline, ColorReset))
					firstLine = false
				} else {
					b.WriteString(fmt.Sprintf("%s%s  %s%s\n", EntryPad, ColorDim, wline, ColorReset))
				}
			}
		}
	}
	b.WriteString("\n")

	// Last request detail — show endpoint and response body
	last := entries[len(entries)-1]
	if last.ResponseBody != "" {
		methodColor := httpMethodColor(last.Method)
		statusColor := httpStatusColor(last.Status)
		b.WriteString(fmt.Sprintf("%s%slast response%s\n", Pad, ColorCyan, ColorReset))
		b.WriteString(fmt.Sprintf("%s%s%s%s %s%s%d%s %s%s%s\n",
			ContPad, methodColor, last.Method, ColorReset,
			statusColor, "", last.Status, ColorReset,
			ColorDim, last.URL, ColorReset))

		// Response body (word-wrapped, capped at 20 lines for the console)
		maxLines := 20
		lines := 0
		for _, bodyLine := range strings.Split(last.ResponseBody, "\n") {
			for _, wline := range WordWrap(bodyLine, ecw) {
				b.WriteString(fmt.Sprintf("%s%s%s%s\n", EntryPad, ColorDim, wline, ColorReset))
				lines++
				if lines >= maxLines {
					b.WriteString(fmt.Sprintf("%s%s... (truncated)%s\n", EntryPad, ColorDim, ColorReset))
					goto doneBody
				}
			}
		}
	doneBody:
		b.WriteString("\n")
	}

	// Average response time
	totalDur := 0
	for _, e := range entries {
		totalDur += e.DurationMS
	}
	avg := totalDur / total
	b.WriteString(fmt.Sprintf("%s%savg response time%s %s%dms%s\n",
		Pad, ColorDim, ColorReset, ColorCyan, avg, ColorReset))

	return b.String()
}

// AutonomousAgentStatus holds the autonomous agent's current state, read from state files.
type AutonomousAgentStatus struct {
	CurrentStory  string // Jira key (e.g. "PROJ-123") or empty
	Phase         string // "requirements", "implementation", "waiting", "idle"
	StoriesDone   int    // count of completed stories this session
	LastHeartbeat int64  // Unix timestamp of last heartbeat
	SessionStart  int64  // Unix timestamp of session start (from bus dir mtime)
}

// ReadAutonomousAgentStatus reads the agent's state files and returns the current status.
func ReadAutonomousAgentStatus(session string) AutonomousAgentStatus {
	var s AutonomousAgentStatus

	if data, err := os.ReadFile(AgentCurrentStoryPath(session)); err == nil {
		s.CurrentStory = strings.TrimSpace(string(data))
	}
	if data, err := os.ReadFile(AgentPhasePath(session)); err == nil {
		s.Phase = strings.TrimSpace(string(data))
	}
	if data, err := os.ReadFile(AgentStoriesDonePath(session)); err == nil {
		if n, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
			s.StoriesDone = n
		}
	}
	if data, err := os.ReadFile(AgentHeartbeatPath(session)); err == nil {
		if ts, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64); err == nil {
			s.LastHeartbeat = ts
		}
	}

	// Session start: use bus dir creation time as proxy
	if info, err := os.Stat(BusDir(session)); err == nil {
		s.SessionStart = info.ModTime().Unix()
	}

	return s
}

// FormatAutonomousAgentStatus returns a human-readable status summary for CLI output.
func FormatAutonomousAgentStatus(s AutonomousAgentStatus) string {
	var b strings.Builder

	// Story line
	if s.CurrentStory != "" {
		b.WriteString(fmt.Sprintf("Story: %s\n", s.CurrentStory))
	} else {
		b.WriteString("Story: (none)\n")
	}

	// Phase line
	if s.Phase != "" {
		b.WriteString(fmt.Sprintf("Phase: %s\n", s.Phase))
	} else {
		b.WriteString("Phase: idle\n")
	}

	// Stories done
	b.WriteString(fmt.Sprintf("Stories done: %d\n", s.StoriesDone))

	// Heartbeat
	if s.LastHeartbeat > 0 {
		ago := time.Now().Unix() - s.LastHeartbeat
		b.WriteString(fmt.Sprintf("Last heartbeat: %s ago\n", agentDuration(ago)))
	} else {
		b.WriteString("Last heartbeat: (none)\n")
	}

	// Uptime
	if s.SessionStart > 0 {
		uptime := time.Now().Unix() - s.SessionStart
		b.WriteString(fmt.Sprintf("Uptime: %s\n", agentDuration(uptime)))
	}

	return b.String()
}

// agentDuration formats seconds into a human-readable "Xh Ym" or "Xm Ys" string.
func agentDuration(secs int64) string {
	if secs < 60 {
		return fmt.Sprintf("%ds", secs)
	}
	if secs < 3600 {
		return fmt.Sprintf("%dm %ds", secs/60, secs%60)
	}
	hours := secs / 3600
	mins := (secs % 3600) / 60
	return fmt.Sprintf("%dh %dm", hours, mins)
}

// renderAgentStatusHeader renders the status block at the top of the agent console.
func renderAgentStatusHeader(session string, width int) string {
	s := ReadAutonomousAgentStatus(session)
	sepWidth := width - len(Pad) - RightMargin
	if sepWidth < 10 {
		sepWidth = 10
	}

	var b strings.Builder

	// Story + phase
	storyLabel := "(idle)"
	if s.CurrentStory != "" {
		storyLabel = s.CurrentStory
	}
	phaseLabel := "idle"
	if s.Phase != "" {
		phaseLabel = s.Phase
	}
	b.WriteString(fmt.Sprintf("%s%sStory:%s %s%s%s  %sPhase:%s %s%s%s\n",
		Pad, ColorDim, ColorReset,
		ColorCyan, storyLabel, ColorReset,
		ColorDim, ColorReset,
		ColorPurple, phaseLabel, ColorReset))

	// Stories done + heartbeat + uptime
	heartbeat := "(none)"
	if s.LastHeartbeat > 0 {
		ago := time.Now().Unix() - s.LastHeartbeat
		heartbeat = agentDuration(ago) + " ago"
	}
	uptime := ""
	if s.SessionStart > 0 {
		ut := time.Now().Unix() - s.SessionStart
		uptime = agentDuration(ut)
	}
	b.WriteString(fmt.Sprintf("%s%sDone:%s %s%d%s  %sHeartbeat:%s %s%s%s",
		Pad, ColorDim, ColorReset,
		ColorGreen, s.StoriesDone, ColorReset,
		ColorDim, ColorReset,
		ColorDim, heartbeat, ColorReset))
	if uptime != "" {
		b.WriteString(fmt.Sprintf("  %sUptime:%s %s%s%s", ColorDim, ColorReset, ColorDim, uptime, ColorReset))
	}
	b.WriteString("\n")

	b.WriteString(fmt.Sprintf("%s%s%s%s\n\n", Pad, ColorDim, Separator(sepWidth), ColorReset))

	return b.String()
}

// renderAgent handles the agent role — shows bus message activity (delegations
// sent/received) by reading from log.jsonl filtered for the agent role.
// Includes a status header showing current story, phase, heartbeat, and uptime.
func renderAgent(cfg *ConsoleConfig, session string, width int) string {
	logPath := LogPath(session)
	entries := readAgentEntries(logPath, 0)
	ecw := calcEntryContentWidth(width)

	var b strings.Builder

	// Status header always shown
	b.WriteString(renderAgentStatusHeader(session, width))

	if len(entries) == 0 {
		b.WriteString(emptyBlock(cfg.EmptyMsg))
		b.WriteString(fmt.Sprintf("%s%swaiting for autonomous agent...%s\n", Pad, ColorDim, ColorReset))
		return b.String()
	}

	total := len(entries)
	sent := 0
	recv := 0
	for _, e := range entries {
		if e.From == "agent" {
			sent++
		} else {
			recv++
		}
	}

	b.WriteString(fmt.Sprintf("%s%stotal%s %s%d%s  %ssent%s %s%d%s  %sreceived%s %s%d%s\n\n",
		Pad, ColorDim, ColorReset,
		ColorCyan, total, ColorReset,
		ColorDim, ColorReset,
		ColorPurple, sent, ColorReset,
		ColorDim, ColorReset,
		ColorGreen, recv, ColorReset))

	// Recent activity
	b.WriteString(fmt.Sprintf("%s%s%s%s\n", Pad, ColorCyan, cfg.RecentLabel, ColorReset))
	recent := entries
	offset := 0
	if len(recent) > cfg.MaxRecent {
		offset = len(recent) - cfg.MaxRecent
		recent = recent[offset:]
	}

	for i, e := range recent {
		num := offset + i + 1
		ts := FormatTimestamp(e.TS)
		numLabel := fmt.Sprintf("#%-3d", num)

		// Direction indicator: → sent, ← received
		direction := "←"
		dirColor := ColorGreen
		peer := e.From
		if e.From == "agent" {
			direction = "→"
			dirColor = ColorPurple
			peer = e.To
		}

		b.WriteString(fmt.Sprintf("%s%s%s%s %s%s%s  %s%s%s %s  %s%s%s\n",
			ContPad, ColorDim, numLabel, ColorReset,
			ColorDim, ts, ColorReset,
			dirColor, direction, ColorReset,
			peer,
			ColorDim, e.Action, ColorReset))

		// Message preview (first line, word-wrapped)
		msg := e.Message
		if msg == "" {
			msg = e.Payload
		}
		if msg != "" {
			firstLine := strings.TrimSpace(strings.Split(msg, "\n")[0])
			if firstLine != "" {
				for _, wline := range WordWrap(firstLine, ecw) {
					b.WriteString(fmt.Sprintf("%s%s↳ %s%s\n", EntryPad, ColorDim, wline, ColorReset))
				}
			}
		}
	}

	return b.String()
}

// readAgentEntries reads log.jsonl and filters for messages involving the agent role.
func readAgentEntries(logPath string, limit int) []ConsoleEntry {
	data, err := os.ReadFile(logPath)
	if err != nil {
		return nil
	}

	var all []ConsoleEntry
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var entry ConsoleEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			continue
		}
		if entry.From == "agent" || entry.To == "agent" {
			all = append(all, entry)
		}
	}

	if limit > 0 && len(all) > limit {
		all = all[len(all)-limit:]
	}
	return all
}

// --- Helpers ---

func gitCmd(args ...string) string {
	cmd := exec.Command("git", args...)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func httpMethodColor(method string) string {
	switch method {
	case "GET":
		return ColorGreen
	case "POST":
		return ColorCyan
	case "PUT":
		return ColorYellow
	case "PATCH":
		return ColorOrange
	case "DELETE":
		return ColorRed
	default:
		return ColorDim
	}
}

func httpStatusColor(status int) string {
	switch {
	case status >= 200 && status < 300:
		return ColorGreen
	case status >= 300 && status < 400:
		return ColorCyan
	case status >= 400 && status < 500:
		return ColorYellow
	case status >= 500:
		return ColorRed
	default:
		return ColorDim
	}
}

func countFiles(dir, ext string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	count := 0
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ext) {
			count++
		}
	}
	return count
}
