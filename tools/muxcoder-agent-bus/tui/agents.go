package tui

import (
	"crypto/md5"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/mkober/muxcoder/tools/muxcoder-agent-bus/bus"
)

// AgentStatus holds the detected state of a single agent window.
type AgentStatus struct {
	Window      string
	Status      string // IDLE, READY, ACTIVE, ERROR
	StatusColor string // ANSI color code
	Cost        string // "$X.XX" or dash
	Tokens      string // "12.3k" or dash
	Snippet     string // last line of output (50 chars max)
}

var (
	errorRe  = regexp.MustCompile(`(?m)^(Error|ERROR|error:|FATAL|PANIC|Traceback)|FAILED|command not found|permission denied|No such file or directory|segmentation fault|core dumped|exit (code |status )[1-9]`)
	promptRe = regexp.MustCompile(`[>$]`)
	costRe   = regexp.MustCompile(`\$[0-9]+\.[0-9]{2,}`)
	tokensRe = regexp.MustCompile(`(?i)[0-9]+\.?[0-9]*k\s+(tokens|context|tok)`)
	rawTokRe = regexp.MustCompile(`[0-9][0-9,]+\s+tokens`)
	compRe   = regexp.MustCompile(`(?i)[0-9]+\.?[0-9]*k`)
)

// CapturePane captures the last N lines from a tmux pane.
func CapturePane(session, target string, lines int) string {
	out, err := exec.Command("tmux", "capture-pane", "-t", target, "-p", "-S", fmt.Sprintf("-%d", lines)).Output()
	if err != nil {
		return ""
	}
	return string(out)
}

// CapturePaneExtended captures 200 lines of scrollback for cost/token extraction.
func CapturePaneExtended(session, target string) string {
	out, err := exec.Command("tmux", "capture-pane", "-t", target, "-p", "-S", "-200").Output()
	if err != nil {
		return ""
	}
	return string(out)
}

// DetectStatus determines the status of an agent and returns the status plus
// a new hash for change detection on the next poll.
func DetectStatus(window, output, prevHash string) (AgentStatus, string) {
	st := AgentStatus{
		Window:      window,
		Status:      "IDLE",
		StatusColor: Dim,
		Cost:        "\u2014",
		Tokens:      "\u2014",
	}

	h := fmt.Sprintf("%x", md5.Sum([]byte(output)))

	if strings.TrimSpace(output) == "" {
		return st, h
	}

	// Snippet: last non-empty line, capped at 50 chars.
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")
	snippet := ""
	for i := len(lines) - 1; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed != "" {
			snippet = trimmed
			break
		}
	}
	if len([]rune(snippet)) > 50 {
		snippet = string([]rune(snippet)[:50])
	}
	st.Snippet = snippet

	// Error detection
	if errorRe.MatchString(output) {
		st.Status = "ERROR"
		st.StatusColor = Red
		return st, h
	}

	// Activity: hash changed since last poll
	if h != prevHash && prevHash != "" {
		st.Status = "ACTIVE"
		st.StatusColor = Cyan
		return st, h
	}

	// Ready: prompt characters present
	if promptRe.MatchString(output) {
		st.Status = "READY"
		st.StatusColor = Green
		return st, h
	}

	return st, h
}

// ExtractCost returns the latest cost value from scrollback (without "$").
func ExtractCost(scrollback string) string {
	matches := costRe.FindAllString(scrollback, -1)
	if len(matches) == 0 {
		return ""
	}
	return strings.TrimPrefix(matches[len(matches)-1], "$")
}

// ExtractTokens returns a compact token count from scrollback.
func ExtractTokens(scrollback string) string {
	// Pattern 1: "X.Xk tokens" / "Xk context" / "Xk tok"
	match := tokensRe.FindAllString(scrollback, -1)
	if len(match) > 0 {
		last := match[len(match)-1]
		kMatch := compRe.FindString(last)
		if kMatch != "" {
			return kMatch
		}
	}

	// Pattern 2: raw number followed by "tokens"
	rawMatch := rawTokRe.FindAllString(scrollback, -1)
	if len(rawMatch) > 0 {
		last := rawMatch[len(rawMatch)-1]
		numRe := regexp.MustCompile(`[0-9,]+`)
		numStr := numRe.FindString(last)
		numStr = strings.ReplaceAll(numStr, ",", "")
		raw, err := strconv.Atoi(numStr)
		if err == nil {
			return RawToCompact(raw)
		}
	}

	return ""
}

// PaneTarget returns the tmux pane target for a given window.
// Delegates to bus.PaneTarget for consolidated pane targeting logic.
func PaneTarget(session, window string) string {
	return bus.PaneTarget(session, window)
}

// RawToCompact converts a raw token count to compact form (e.g. 12300 -> "12.3k").
func RawToCompact(raw int) string {
	if raw >= 1000 {
		return fmt.Sprintf("%.1fk", float64(raw)/1000.0)
	}
	return strconv.Itoa(raw)
}

// TokensToRaw converts a compact token string to a raw integer (e.g. "12.3k" -> 12300).
func TokensToRaw(compact string) int {
	lower := strings.ToLower(compact)
	if strings.HasSuffix(lower, "k") {
		numStr := strings.TrimSuffix(lower, "k")
		f, err := strconv.ParseFloat(numStr, 64)
		if err != nil {
			return 0
		}
		return int(f * 1000)
	}
	numStr := strings.ReplaceAll(compact, ",", "")
	v, err := strconv.Atoi(numStr)
	if err != nil {
		return 0
	}
	return v
}
