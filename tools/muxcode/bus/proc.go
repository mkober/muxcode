package bus

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
)

// procScriptExts are file suffixes recognized as script names when deriving a
// process display name from a command line.
var procScriptExts = []string{".sh", ".bash", ".zsh", ".py", ".js", ".ts", ".rb", ".pl", ".go"}

// procInterpreters are program names that run a script passed as an argument;
// when one leads a command, the script argument becomes the process name.
var procInterpreters = map[string]bool{
	"bash": true, "sh": true, "zsh": true, "python": true, "python3": true,
	"node": true, "ruby": true, "perl": true, "go": true,
}

// procShellKeywords are leading tokens of a bare inline shell construct that
// has no single script to name.
var procShellKeywords = map[string]bool{
	"for": true, "while": true, "until": true, "if": true,
	"case": true, "{": true, "(": true, "do": true, "then": true,
}

// envAssignRe matches a leading environment assignment token (FOO=bar).
var envAssignRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

// ProcName derives a short, human-friendly name for a background process from
// its command — typically the script being run (e.g. "test-foo.sh" from
// "bash scripts/test-foo.sh --env dev"). It falls back to the invoked program
// name, or "(inline)" for a bare shell construct with no script.
//
// Order matters: the inline-construct check runs before any script scan, so a
// script named only as loop data (e.g. "for f in a.sh b.sh") is reported as
// "(inline)" rather than mistaken for the executed script.
func ProcName(command string) string {
	fields := strings.Fields(command)
	// Skip leading environment assignments (FOO=bar bash script.sh).
	for len(fields) > 0 && envAssignRe.MatchString(fields[0]) {
		fields = fields[1:]
	}
	if len(fields) == 0 {
		return "(none)"
	}

	head := fields[0]

	// 1. Bare inline shell construct — no single script to name.
	if procShellKeywords[head] {
		return "(inline)"
	}

	// 2. Interpreter → prefer a script argument, else the first non-flag
	//    argument (module or subcommand, e.g. "python -m pytest" → "pytest",
	//    "go run main.go" → "main.go").
	if procInterpreters[filepath.Base(head)] {
		firstArg := ""
		for _, f := range fields[1:] {
			if strings.HasPrefix(f, "-") {
				continue
			}
			if base, ok := scriptBase(f); ok {
				return base
			}
			if firstArg == "" {
				firstArg = filepath.Base(f)
			}
		}
		if firstArg != "" {
			return firstArg
		}
	}

	// 3. Direct script execution (./foo.sh, scripts/foo.sh).
	if base, ok := scriptBase(head); ok {
		return base
	}

	// 4. Fallback: the invoked program name.
	return filepath.Base(head)
}

// scriptBase returns the basename of tok and true when tok ends in a known
// script extension. Extension matching is case-insensitive (".SH" matches);
// the returned basename preserves the original case.
func scriptBase(tok string) (string, bool) {
	lower := strings.ToLower(tok)
	for _, ext := range procScriptExts {
		if strings.HasSuffix(lower, ext) {
			return filepath.Base(tok), true
		}
	}
	return "", false
}

// ProcEntry represents a tracked background process.
type ProcEntry struct {
	ID         string `json:"id"`
	PID        int    `json:"pid"`
	Command    string `json:"command"`
	Dir        string `json:"dir"`
	Owner      string `json:"owner"`
	Status     string `json:"status"`
	ExitCode   int    `json:"exit_code"`
	StartedAt  int64  `json:"started_at"`
	FinishedAt int64  `json:"finished_at"`
	LogFile    string `json:"log_file"`
	Notified   bool   `json:"notified"`
}

// exitCodeRe matches the EXIT_CODE sentinel appended to log files.
var exitCodeRe = regexp.MustCompile(`^EXIT_CODE:(\d+)$`)

// ReadProcEntries reads all process entries from the proc JSONL file.
func ReadProcEntries(session string) ([]ProcEntry, error) {
	data, err := os.ReadFile(ProcPath(session))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var entries []ProcEntry
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var e ProcEntry
		if err := json.Unmarshal(line, &e); err != nil {
			continue // skip malformed lines
		}
		entries = append(entries, e)
	}
	return entries, scanner.Err()
}

// WriteProcEntries overwrites the proc JSONL file with the given entries.
func WriteProcEntries(session string, entries []ProcEntry) error {
	var buf bytes.Buffer
	for _, e := range entries {
		data, err := json.Marshal(e)
		if err != nil {
			return err
		}
		buf.Write(data)
		buf.WriteByte('\n')
	}
	return os.WriteFile(ProcPath(session), buf.Bytes(), 0644)
}

// GetProcEntry returns a single process entry by ID.
func GetProcEntry(session, id string) (ProcEntry, error) {
	entries, err := ReadProcEntries(session)
	if err != nil {
		return ProcEntry{}, err
	}

	for _, e := range entries {
		if e.ID == id {
			return e, nil
		}
	}
	return ProcEntry{}, fmt.Errorf("process not found: %s", id)
}

// UpdateProcEntry applies a mutation function to a process entry by ID.
func UpdateProcEntry(session, id string, fn func(*ProcEntry)) error {
	entries, err := ReadProcEntries(session)
	if err != nil {
		return err
	}

	found := false
	for i, e := range entries {
		if e.ID == id {
			fn(&entries[i])
			found = true
			break
		}
	}

	if !found {
		return fmt.Errorf("process not found: %s", id)
	}

	return WriteProcEntries(session, entries)
}

// RemoveProcEntry removes a process entry by ID.
func RemoveProcEntry(session, id string) error {
	entries, err := ReadProcEntries(session)
	if err != nil {
		return err
	}

	found := false
	var kept []ProcEntry
	for _, e := range entries {
		if e.ID == id {
			found = true
			continue
		}
		kept = append(kept, e)
	}

	if !found {
		return fmt.Errorf("process not found: %s", id)
	}

	return WriteProcEntries(session, kept)
}

// StartProc launches a background process and tracks it in the proc JSONL file.
// The command is wrapped with an exit code sentinel for reliable status detection.
func StartProc(session, command, dir, owner string) (ProcEntry, error) {
	id := NewMsgID("proc")
	logFile := ProcLogPath(session, id)

	// Ensure proc directory exists
	if err := os.MkdirAll(ProcDir(session), 0755); err != nil {
		return ProcEntry{}, fmt.Errorf("creating proc dir: %v", err)
	}

	// Create log file
	lf, err := os.Create(logFile)
	if err != nil {
		return ProcEntry{}, fmt.Errorf("creating log file: %v", err)
	}

	// Wrap command in a subshell to capture exit code reliably.
	// The subshell ensures that even `exit N` commands don't prevent
	// the EXIT_CODE sentinel from being written.
	wrapped := fmt.Sprintf("(%s); echo EXIT_CODE:$? >> %s", command, logFile)

	cmd := exec.Command("sh", "-c", wrapped)
	cmd.Dir = dir
	cmd.Stdout = lf
	cmd.Stderr = lf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		lf.Close()
		return ProcEntry{}, fmt.Errorf("starting process: %v", err)
	}
	lf.Close()

	// Detach: let the process run independently
	go cmd.Wait()

	entry := ProcEntry{
		ID:        id,
		PID:       cmd.Process.Pid,
		Command:   command,
		Dir:       dir,
		Owner:     owner,
		Status:    "running",
		ExitCode:  -1,
		StartedAt: time.Now().Unix(),
		LogFile:   logFile,
	}

	entries, err := ReadProcEntries(session)
	if err != nil {
		return ProcEntry{}, err
	}
	entries = append(entries, entry)
	if err := WriteProcEntries(session, entries); err != nil {
		return ProcEntry{}, err
	}

	return entry, nil
}

// CheckProcAlive tests whether a process with the given PID is still running.
func CheckProcAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil
}

// RefreshProcStatus checks all running processes and updates their status.
// Returns the list of entries that transitioned from running to a terminal state.
func RefreshProcStatus(session string) ([]ProcEntry, error) {
	entries, err := ReadProcEntries(session)
	if err != nil {
		return nil, err
	}

	var completed []ProcEntry
	changed := false

	for i, e := range entries {
		if e.Status != "running" {
			continue
		}

		if CheckProcAlive(e.PID) {
			continue
		}

		// Process is no longer running — extract exit code from log
		exitCode := -1
		if code, ok := extractExitCode(e.LogFile); ok {
			exitCode = code
		}

		entries[i].ExitCode = exitCode
		entries[i].FinishedAt = time.Now().Unix()

		if exitCode == 0 {
			entries[i].Status = "exited"
		} else {
			entries[i].Status = "failed"
		}

		changed = true
		completed = append(completed, entries[i])
	}

	if changed {
		if err := WriteProcEntries(session, entries); err != nil {
			return completed, err
		}
	}

	return completed, nil
}

// extractExitCode reads the last non-empty lines of a log file looking for
// the EXIT_CODE sentinel. Returns the exit code and true if found.
func extractExitCode(logFile string) (int, bool) {
	data, err := os.ReadFile(logFile)
	if err != nil {
		return -1, false
	}

	// Scan from the end for the sentinel
	lines := strings.Split(string(data), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		m := exitCodeRe.FindStringSubmatch(line)
		if m != nil {
			var code int
			fmt.Sscanf(m[1], "%d", &code)
			return code, true
		}
		// Only check last few non-empty lines
		break
	}
	return -1, false
}

// ProcDisplayStatus returns the effective status of a process entry for
// display, performing a read-only liveness check without persisting changes.
// A "running" entry whose process has already died (but has not yet been
// reconciled by the daemon or a `proc list`/`status` call) is reported with
// its terminal status derived from the log's EXIT_CODE sentinel. This lets the
// console reflect completion immediately without mutating the proc JSONL from
// the render path (which the daemon owns).
func ProcDisplayStatus(e ProcEntry) (status string, exitCode int) {
	if e.Status != "running" {
		return e.Status, e.ExitCode
	}
	if CheckProcAlive(e.PID) {
		return "running", -1
	}
	// Process is gone but not yet reconciled — derive terminal status.
	if code, ok := extractExitCode(e.LogFile); ok {
		if code == 0 {
			return "exited", 0
		}
		return "failed", code
	}
	return "exited", -1
}

// ProcLogTail returns up to the last n non-empty lines of a process log,
// stripping the trailing EXIT_CODE sentinel. ANSI stripping is left to the
// caller so it can be reused for both console rendering and CLI output.
func ProcLogTail(logFile string, n int) []string {
	data, err := os.ReadFile(logFile)
	if err != nil {
		return nil
	}

	var lines []string
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimRight(raw, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		if exitCodeRe.MatchString(strings.TrimSpace(line)) {
			continue
		}
		lines = append(lines, line)
	}

	if n > 0 && len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

// StopProc sends SIGTERM to a running process and updates its status.
func StopProc(session, id string) error {
	entry, err := GetProcEntry(session, id)
	if err != nil {
		return err
	}

	if entry.Status != "running" {
		return fmt.Errorf("process %s is not running (status: %s)", id, entry.Status)
	}

	// Send SIGTERM to the process group
	if err := syscall.Kill(-entry.PID, syscall.SIGTERM); err != nil {
		// Try killing just the process if group kill fails
		if err := syscall.Kill(entry.PID, syscall.SIGTERM); err != nil {
			return fmt.Errorf("sending SIGTERM to PID %d: %v", entry.PID, err)
		}
	}

	return UpdateProcEntry(session, id, func(e *ProcEntry) {
		e.Status = "stopped"
		e.FinishedAt = time.Now().Unix()
	})
}

// CleanFinished removes all non-running process entries and their log files.
func CleanFinished(session string) (int, error) {
	entries, err := ReadProcEntries(session)
	if err != nil {
		return 0, err
	}

	var kept []ProcEntry
	removed := 0
	for _, e := range entries {
		if e.Status == "running" {
			kept = append(kept, e)
			continue
		}
		// Remove log file
		_ = os.Remove(e.LogFile)
		removed++
	}

	if err := WriteProcEntries(session, kept); err != nil {
		return removed, err
	}

	return removed, nil
}

// FormatProcList formats process entries as a human-readable table.
// When showAll is false, only running entries are shown.
func FormatProcList(entries []ProcEntry, showAll bool) string {
	var b strings.Builder

	var filtered []ProcEntry
	for _, e := range entries {
		if showAll || e.Status == "running" {
			filtered = append(filtered, e)
		}
	}

	if len(filtered) == 0 {
		if showAll {
			b.WriteString("No processes.\n")
		} else {
			b.WriteString("No running processes. Use --all to see finished processes.\n")
		}
		return b.String()
	}

	b.WriteString(fmt.Sprintf("%-36s %-8s %-10s %-10s %-10s %s\n",
		"ID", "PID", "STATUS", "OWNER", "STARTED", "COMMAND"))
	b.WriteString(strings.Repeat("-", 100) + "\n")

	for _, e := range filtered {
		started := time.Unix(e.StartedAt, 0).Format("15:04:05")
		cmd := e.Command
		if len(cmd) > 40 {
			cmd = cmd[:37] + "..."
		}
		b.WriteString(fmt.Sprintf("%-36s %-8d %-10s %-10s %-10s %s\n",
			e.ID, e.PID, e.Status, e.Owner, started, cmd))
	}

	return b.String()
}

// FormatProcStatus formats a single process entry as a detailed status report.
func FormatProcStatus(entry ProcEntry) string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf("Process: %s\n", entry.ID))
	b.WriteString(fmt.Sprintf("  PID:      %d\n", entry.PID))
	b.WriteString(fmt.Sprintf("  Status:   %s\n", entry.Status))
	b.WriteString(fmt.Sprintf("  Owner:    %s\n", entry.Owner))
	b.WriteString(fmt.Sprintf("  Command:  %s\n", entry.Command))
	b.WriteString(fmt.Sprintf("  Dir:      %s\n", entry.Dir))
	b.WriteString(fmt.Sprintf("  Started:  %s\n", time.Unix(entry.StartedAt, 0).Format("2006-01-02 15:04:05")))

	if entry.FinishedAt > 0 {
		b.WriteString(fmt.Sprintf("  Finished: %s\n", time.Unix(entry.FinishedAt, 0).Format("2006-01-02 15:04:05")))
		duration := time.Duration(entry.FinishedAt-entry.StartedAt) * time.Second
		b.WriteString(fmt.Sprintf("  Duration: %s\n", duration))
	}

	if entry.Status != "running" {
		b.WriteString(fmt.Sprintf("  Exit:     %d\n", entry.ExitCode))
	}

	b.WriteString(fmt.Sprintf("  Log:      %s\n", entry.LogFile))

	return b.String()
}
