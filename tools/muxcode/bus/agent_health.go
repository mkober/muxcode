package bus

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

// agentHealthExcludedRoles lists roles that should never be auto-restarted.
// webhook: managed separately, not a tmux-based agent.
//
// edit left this list in MUX-126. The exclusion guarded the pane the user types
// into, and what still guards it: a restart fires only on a pane already at a
// bare shell prompt for three consecutive checks, never on a busy or live
// agent; it resumes the conversation with the full launch flags instead of the
// flagless bare `claude --resume` it used to take by hand (2026-08-31); the
// restart cap and down/restarting alerts apply as for any role; and
// MUXCODE_EDIT_AUTO_RESTART_DISABLE=1 restores the exclusion. A LIVE edit is
// still never torn down — see NeverReloadLive.
var agentHealthExcludedRoles = map[string]bool{
	"webhook": true,
}

// editAutoRestartDisableEnv opts edit back out of health monitoring.
const editAutoRestartDisableEnv = "MUXCODE_EDIT_AUTO_RESTART_DISABLE"

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
// since the agent is intentionally down during the reload cycle, and for edit
// when MUXCODE_EDIT_AUTO_RESTART_DISABLE=1.
func IsAgentHealthExcluded(session, role string) bool {
	if IsReloading(session, role) {
		return true
	}
	if role == "edit" && os.Getenv(editAutoRestartDisableEnv) == "1" {
		return true
	}
	return agentHealthExcludedRoles[role]
}

// NeverReloadLive reports whether a watchdog must leave a role's LIVE agent
// running rather than tear it down and relaunch it: every health-excluded
// role, and edit always. Restarting a dead edit resumes its conversation; a
// reload of a live one starts fresh and discards the session the user is
// working in, so a live edit is alerted on, never reloaded (MUX-136).
func NeverReloadLive(session, role string) bool {
	return role == "edit" || IsAgentHealthExcluded(session, role)
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

// SinceLastAgentExit returns the part of a pane capture after its last exit
// banner — the output of whatever launched since — or the whole capture when
// it holds none. A banner printed by a session that has since exited is no
// evidence about the one running now.
func SinceLastAgentExit(content string) string {
	banners := agentExitBannerPattern.FindAllStringIndex(content, -1)
	if len(banners) == 0 {
		return content
	}
	return content[banners[len(banners)-1][1]:]
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
	content, err := captureResumePane(target)
	if err != nil {
		return "", false
	}
	return ScrapeResumeSessionID(content)
}

// captureResumePane is the one capture every resume scrape reads, shared with
// RestartLocalAgent so the -J the scrape depends on cannot drift between them.
func captureResumePane(target string) (string, error) {
	return TmuxOutput("capture-pane", "-t", target, "-p", "-J", "-S", fmt.Sprintf("-%d", resumeCaptureLines))
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

// ResumeAuto is the resume id `agent launch --resume` carries when no id was
// given: the launcher resolves it with FindResumeID. Never a valid UUID.
const ResumeAuto = "auto"

// Resume id sources, recorded as source=<value> on the lifecycle row.
const (
	ResumeSourcePane       = "pane"
	ResumeSourceTranscript = "transcript"
)

// FindResumeID resolves the session a resume continues, pane-first: the exit
// banner in target names the exact session that died, so it outranks the
// cwd's transcripts (TranscriptIDForCwd, consulted for agentRole in session).
// ok false carries why both declined, for the caller's fresh-launch lifecycle
// row. An empty target skips the pane.
func FindResumeID(target, session, agentRole, cwd, agentName string) (id, source, reason string, ok bool) {
	paneReason := "no pane to scrape"
	if target != "" {
		if id, ok := CaptureResumeSessionID(target); ok {
			return id, ResumeSourcePane, "", true
		}
		paneReason = "no resume banner in pane " + target
	}
	id, err := TranscriptIDForCwd(session, agentRole, cwd, agentName)
	if err != nil {
		return "", "", paneReason + "; transcript: " + err.Error(), false
	}
	return id, ResumeSourceTranscript, "", true
}

// TranscriptIDForCwd's declines; each means the caller launches fresh.
var (
	ErrTranscriptNone      = errors.New("no transcript for this agent")
	ErrTranscriptAmbiguous = errors.New("more than one transcript for this agent")
	ErrTranscriptSharedCwd = errors.New("cwd is not a worktree this agent owns alone")
)

// claudeProjectsDir is where Claude Code keeps one transcript directory per
// cwd: $CLAUDE_CONFIG_DIR/projects, else ~/.claude/projects.
var claudeProjectsDir = func() string {
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, "projects")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", "projects")
}

var nonAlphanumeric = regexp.MustCompile(`[^a-zA-Z0-9]`)

// claudeProjectDirName encodes cwd the way Claude Code names its transcript
// directory: symlinks resolved, then every non-alphanumeric byte a dash. The
// resolution is load-bearing on macOS, where Claude records a /var/folders/…
// cwd under its real /private/var/folders/… path.
func claudeProjectDirName(cwd string) (string, error) {
	resolved, err := resolvedPath(cwd)
	if err != nil {
		return "", err
	}
	return nonAlphanumeric.ReplaceAllString(resolved, "-"), nil
}

// resolvedPath is path made absolute with its symlinks resolved.
func resolvedPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

// spawnOwnsWorktree reports whether session's spawn registry proves cwd is
// agentRole's private worktree: exactly one entry claims cwd as its worktree,
// and that entry is agentRole. The registry is the only record tying a
// directory to one agent; agent-definition identity is not, because a spawn
// worker launched with the edit definition shares code-editor with edit.
func spawnOwnsWorktree(session, agentRole, cwd string) bool {
	if session == "" || agentRole == "" {
		return false
	}
	want, err := resolvedPath(cwd)
	if err != nil {
		return false
	}
	entries, err := ReadSpawnEntries(session)
	if err != nil {
		return false
	}
	claims, mine := 0, false
	for _, e := range entries {
		if e.Worktree == "" {
			continue
		}
		if got, err := resolvedPath(e.Worktree); err != nil || got != want {
			continue
		}
		claims++
		mine = mine || e.SpawnRole == agentRole
	}
	return mine && claims == 1
}

// agentSettingScanLines bounds the read for a transcript's agent: Claude Code
// writes its agent-setting record at the head of the file.
const agentSettingScanLines = 20

// TranscriptIDForCwd is the resume fallback for a pane with no exit banner:
// the session id of the one transcript in cwd's Claude project directory that
// records agentName. It applies only where cwd identifies the agent uniquely —
// a spawn worktree session's registry assigns to agentRole alone
// (spawnOwnsWorktree) — and declines with ErrTranscriptSharedCwd everywhere
// else, because roles share a cwd: plan, edit and commit all run at the repo
// root, and a lone transcript there may be a privileged peer's conversation
// even when it records the same agent (MUX-139 Decision 1). Within an owned
// worktree it still declines — ErrTranscriptNone or ErrTranscriptAmbiguous —
// rather than pick: a transcript recording no agent or another agent never
// qualifies, and a worker relaunched fresh leaves several of its own.
func TranscriptIDForCwd(session, agentRole, cwd, agentName string) (string, error) {
	if agentName == "" {
		return "", fmt.Errorf("%w: the role has no agent name to match", ErrTranscriptNone)
	}
	if !spawnOwnsWorktree(session, agentRole, cwd) {
		return "", fmt.Errorf("%w: %s is not %s's own spawn worktree", ErrTranscriptSharedCwd, cwd, agentRole)
	}
	name, err := claudeProjectDirName(cwd)
	if err != nil {
		return "", fmt.Errorf("resolving cwd %s: %w", cwd, err)
	}
	dir := filepath.Join(claudeProjectsDir(), name)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("%w: %s does not exist", ErrTranscriptNone, dir)
	}
	if err != nil {
		return "", err
	}
	var ids []string
	for _, e := range entries {
		id, isTranscript := strings.CutSuffix(e.Name(), ".jsonl")
		if !isTranscript || !e.Type().IsRegular() || !ValidResumeSessionID(id) {
			continue
		}
		if transcriptAgent(filepath.Join(dir, e.Name())) == agentName {
			ids = append(ids, id)
		}
	}
	switch len(ids) {
	case 0:
		return "", fmt.Errorf("%w: no transcript under %s records agent %s", ErrTranscriptNone, dir, agentName)
	case 1:
		return ids[0], nil
	}
	return "", fmt.Errorf("%w: %d transcripts under %s record agent %s", ErrTranscriptAmbiguous, len(ids), dir, agentName)
}

// transcriptAgent returns the agentSetting a transcript records, or "".
func transcriptAgent(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	r := bufio.NewReader(f)
	for i := 0; i < agentSettingScanLines; i++ {
		line, err := r.ReadBytes('\n')
		var rec struct {
			AgentSetting string `json:"agentSetting"`
		}
		if json.Unmarshal(line, &rec) == nil && rec.AgentSetting != "" {
			return rec.AgentSetting
		}
		if err != nil {
			return ""
		}
	}
	return ""
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
