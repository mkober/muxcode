package bus

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

// agentHealthExcludedRoles lists roles that should never be auto-restarted.
// edit: user's interactive session.
// webhook: managed separately, not a tmux-based agent.
var agentHealthExcludedRoles = map[string]bool{
	"edit":    true,
	"webhook": true,
}

// AgentStoppedPath returns the marker file path that suppresses auto-restart
// for a role. Written by "agent-health --stop", cleared by "--start".
func AgentStoppedPath(session, role string) string {
	return filepath.Join(BusDir(session), "lock", role+".stopped")
}

// MarkAgentStopped writes a stopped marker to prevent auto-restart.
func MarkAgentStopped(session, role string) error {
	return os.WriteFile(AgentStoppedPath(session, role), []byte("stopped"), 0644)
}

// ClearAgentStopped removes the stopped marker, allowing auto-restart.
func ClearAgentStopped(session, role string) {
	_ = os.Remove(AgentStoppedPath(session, role))
}

// IsAgentStopped returns true if a stopped marker exists for the role.
func IsAgentStopped(session, role string) bool {
	_, err := os.Stat(AgentStoppedPath(session, role))
	return err == nil
}

// IsAgentHealthExcluded returns true if a role should be excluded from
// automatic health monitoring (never auto-restarted).
// Also returns true while a reload is in progress (reload marker exists),
// since the agent is intentionally down during the reload cycle.
func IsAgentHealthExcluded(session, role string) bool {
	if IsReloading(session, role) {
		return true
	}
	return agentHealthExcludedRoles[role]
}

// RoleHasWindow reports whether the tmux window backing a role appears in
// names, as returned by TmuxListWindowNames.
//
// This is the DEFINITE-liveness counterpart to IsAgentAlive. IsAgentAlive
// fail-safes to "alive" when a pane cannot be captured, which is indeterminate
// for a role that was never launched: a session without an "auto" window makes
// IsAgentAlive("auto") report alive even though no such agent exists. Callers
// that must not act on a phantom role (sending it work, alarming that it never
// consumed) need a signal that can actually say "no", which is this.
//
// Hosted and mode roles resolve to their host window via WindowForRole.
//
// Taking the window list as a parameter lets a caller sweeping many roles pay
// for one tmux call instead of one per role. A caller that could not read the
// list must treat that as indeterminate rather than "no windows exist" — see
// Daemon.roleHasWindow.
func RoleHasWindow(names []string, role string) bool {
	want := WindowForRole(role)
	for _, name := range names {
		if name == want {
			return true
		}
	}
	return false
}

// IsAgentAlive checks whether an agent's tmux pane is running an AI CLI
// session or a local LLM harness, as opposed to having crashed back to a
// bare shell prompt.
//
// Detection strategy:
//  1. Harness marker PID alive → alive (provider-independent catch-all)
//  2. Delegate to provider for CLI-specific alive detection
func IsAgentAlive(session, role string) bool {
	// 1. Harness check — provider-independent catch-all for local LLM agents
	if IsHarnessActive(session, role) {
		return true
	}

	// 2. Delegate to provider for CLI-specific alive detection
	provider := ResolveProvider(role)
	return provider.IsAlive(session, role)
}

// isShellPrompt returns true if the captured pane lines indicate a bare shell
// prompt (agent has exited). Checks that the last non-empty line ends with
// a known prompt suffix ('$', '%', '>', '->') and that no ❯ appears anywhere.
func isShellPrompt(lines []string) bool {
	// Find last non-empty line
	lastNonEmpty := ""
	for i := len(lines) - 1; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed != "" {
			lastNonEmpty = trimmed
			break
		}
	}

	if lastNonEmpty == "" {
		return false
	}

	// Check for ❯ anywhere — if present, agent is alive
	for _, line := range lines {
		if strings.Contains(line, idlePromptChar) {
			return false
		}
	}

	return hasShellPromptSuffix(lastNonEmpty)
}

// hasShellPromptSuffix reports whether a trimmed line ends the way a shell
// prompt does: bash ($), zsh (%), root (#), the custom arrow (->), or a
// standalone ">" on a short line (≤10 chars) so command output ending in ">"
// does not match.
//
// Shared by the health probe and the injection guard so the two agree on what
// a prompt looks like. Root (#) was missing until 2026-09-11: `root@host:/#`
// read as an agent, so the guard that exists to stop a payload running in a
// dead agent's shell let it run in the one shell where that costs most.
func hasShellPromptSuffix(line string) bool {
	switch {
	case strings.HasSuffix(line, "$"), strings.HasSuffix(line, "%"), strings.HasSuffix(line, "#"),
		strings.HasSuffix(line, "->"):
		return true
	case strings.HasSuffix(line, ">") && len(line) <= 10:
		return true
	}
	return false
}

// agentExitBanner is the line Claude Code prints as it tears down, above the
// `claude --resume <id>` command it offers.
const agentExitBanner = "Resume this session with:"

// paneShowsAgentExit reports whether a capture caught the agent between its
// exit banner and the shell's first prompt — the gap hasShellPromptSuffix
// cannot see, having no prompt yet to match, where a payload lands in a tty
// bash inherits and submits as a command (2026-09-17, muxcode edit pane).
//
// Only a composer BELOW the last banner clears the pane. Below, because the
// dying agent drew one above it moments earlier, so an anywhere-in-the-capture
// test clears the very frame this catches — the MUX-164 bypass again; last,
// because a capture spanning exit/relaunch/exit has a composer under its first
// banner and nothing under its newest. Matching is whitespace-stripped: a
// narrow pane soft-wraps the banner mid-phrase and capture carries no -J to
// rejoin it, while stripping preserves the order the test depends on.
func paneShowsAgentExit(content string) bool {
	stripped := stripWhitespace(content)
	at := strings.LastIndex(stripped, stripWhitespace(agentExitBanner))
	if at < 0 {
		return false
	}
	return !strings.Contains(stripped[at:], idlePromptChar)
}

// resumeCommand is the command Claude Code offers under agentExitBanner.
const resumeCommand = "claude --resume"

// resumeSessionIDPattern matches a whole token, so a truncated, overlong or
// prompt-extended id is rejected rather than trimmed into a UUID.
var resumeSessionIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

var (
	agentExitBannerPattern = regexp.MustCompile(whitespaceTolerant(agentExitBanner))
	resumeCommandPattern   = regexp.MustCompile(`^\s*` + whitespaceTolerant(resumeCommand) + `[ \t]+`)
)

// resumeCaptureLines bounds the capture to the exit banner and the prompt
// beneath it, with room for a narrow pane's wrapping.
const resumeCaptureLines = 20

// CaptureResumeSessionID captures target with soft-wrapped lines joined
// (`capture-pane -J`) and scrapes it with ScrapeResumeSessionID. The join is
// what lets a wrapped id through: the parser never rejoins across a newline.
func CaptureResumeSessionID(target string) (id string, ok bool) {
	content, err := TmuxOutput("capture-pane", "-t", target, "-p", "-J", "-S", fmt.Sprintf("-%d", resumeCaptureLines))
	if err != nil {
		return "", false
	}
	return ScrapeResumeSessionID(content)
}

// ValidResumeSessionID reports whether id is a whole Claude Code session UUID,
// the only shape `muxcode agent launch --resume` passes through.
func ValidResumeSessionID(id string) bool {
	return resumeSessionIDPattern.MatchString(id)
}

// ScrapeResumeSessionID extracts the session id Claude Code offers in its exit
// banner (`Resume this session with: claude --resume <id>`) from a pane
// capture. ok is false when no id is found — callers must launch fresh rather
// than resume with an empty id (MUX-126).
//
// Only the LAST banner is read, and a malformed last banner yields not-found
// rather than falling back to an earlier one: an earlier banner belongs to a
// previous session, and resuming it would restore the wrong conversation. The
// capture must be taken before any relaunch keystroke, which types over the
// banner, and with -J (CaptureResumeSessionID): the id must be one whole token
// on the command's line. A capture without -J cannot tell a soft wrap from a
// hard newline, so a split id fails closed — joining it once let a truncated
// id borrow hex from the shell prompt below and yield an id Claude never
// offered. The banner and command tolerate wrapping; they only recognize.
func ScrapeResumeSessionID(content string) (id string, ok bool) {
	banners := agentExitBannerPattern.FindAllStringIndex(content, -1)
	if len(banners) == 0 {
		return "", false
	}
	tail := banners[len(banners)-1][1]
	cmd := resumeCommandPattern.FindStringIndex(content[tail:])
	if cmd == nil {
		return "", false
	}
	rest := content[tail+cmd[1]:]
	if end := strings.IndexFunc(rest, unicode.IsSpace); end >= 0 {
		rest = rest[:end]
	}
	if !resumeSessionIDPattern.MatchString(rest) {
		return "", false
	}
	return rest, true
}

// whitespaceTolerant builds a regexp matching s with any whitespace, including
// a soft-wrap newline, between its characters.
func whitespaceTolerant(s string) string {
	var parts []string
	for _, r := range stripWhitespace(s) {
		parts = append(parts, regexp.QuoteMeta(string(r)))
	}
	return strings.Join(parts, `\s*`)
}

// stripWhitespace removes every space, tab and newline so a match survives the
// soft wrap a narrow pane inserts mid-phrase.
func stripWhitespace(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
}

// FormatAgentHealthAlert formats an agent health alert message.
func FormatAgentHealthAlert(status, role, message string) string {
	var b strings.Builder
	switch status {
	case "down":
		b.WriteString(fmt.Sprintf("⚠ AGENT DOWN: %s\n", role))
	case "restarting":
		b.WriteString(fmt.Sprintf("🔄 AGENT RESTARTING: %s\n", role))
	case "recovered":
		b.WriteString(fmt.Sprintf("✅ AGENT RECOVERED: %s\n", role))
	default:
		b.WriteString(fmt.Sprintf("ℹ AGENT %s: %s\n", strings.ToUpper(status), role))
	}
	if message != "" {
		b.WriteString(fmt.Sprintf("  %s\n", message))
	}
	return b.String()
}

// AgentHealthAlertKey returns a dedup key for an agent health alert.
func AgentHealthAlertKey(role, status string) string {
	return fmt.Sprintf("agent:%s:%s", role, status)
}
