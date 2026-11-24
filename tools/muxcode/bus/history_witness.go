package bus

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// The transcript witness (MUX-185, option 1). A `hook` history row names the
// provider transcript record of the call it observed — tool_use_id and
// transcript_path, copied from the hook payload by ProcessBashHook — and the
// reader accepts it as observed evidence only when that record exists, under
// the provider's own transcript root, for the same shell command, finished in
// the foreground, with the exit code the row claims.
//
// This is corroboration, not a boundary: the transcript is the same uid's file
// and a process that appends a consistent record to it as well defeats it. It
// removes the one-line forgery — a row appended by hand, or a crafted payload
// piped into `muxcode hook bash` — which is the adversary D1 names. The
// address is never taken from the row unchecked: a transcript_path outside the
// provider's root is rejected unread, or a forger would name a file of its own.
//
// Facts parsed from a transcript are cached by canonical path plus id, never a
// row's acceptance, so every row is compared against them on every read. A
// miss is cached with the file's size and re-scanned once the file grows.

// witnessKind names which provider's transcript format a path holds.
type witnessKind int

const (
	witnessNone witnessKind = iota
	witnessClaude
	witnessCodex
)

// witnessFacts is what a transcript records about one tool call.
type witnessFacts struct {
	found    bool
	command  string
	exitCode string // "" when the call has no final exit status
	nonFinal string // why exitCode is empty: background, interrupted, no exit code
}

type cachedWitness struct {
	facts witnessFacts
	size  int64
}

var witnessCache = struct {
	sync.Mutex
	m map[string]cachedWitness
}{m: map[string]cachedWitness{}}

// transcriptRoots returns the provider transcript directories a witness may
// live under, each resolved through EvalSymlinks; an unresolvable root is
// skipped. A configured CLAUDE_CONFIG_DIR or CODEX_HOME adds to the defaults
// rather than replacing them, since the daemon and an agent may disagree on it.
func transcriptRoots() map[string]witnessKind {
	roots := map[string]witnessKind{}
	add := func(dir string, kind witnessKind) {
		if dir == "" {
			return
		}
		if canon, err := filepath.EvalSymlinks(dir); err == nil {
			roots[canon] = kind
		}
	}
	add(joinIfSet(os.Getenv("CLAUDE_CONFIG_DIR"), "projects"), witnessClaude)
	add(joinIfSet(os.Getenv("CODEX_HOME"), "sessions"), witnessCodex)
	if home, err := os.UserHomeDir(); err == nil {
		add(filepath.Join(home, ".claude", "projects"), witnessClaude)
		add(filepath.Join(home, ".codex", "sessions"), witnessCodex)
	}
	return roots
}

func joinIfSet(dir, sub string) string {
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, sub)
}

// locateWitness resolves a row's transcript_path and returns it canonical with
// its provider, or a rejection reason. The path is never opened before it is
// found under a root.
func locateWitness(path string) (string, witnessKind, string) {
	if !filepath.IsAbs(path) {
		return "", witnessNone, "transcript_path is not absolute"
	}
	canon, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", witnessNone, "transcript unreadable"
	}
	for root, kind := range transcriptRoots() {
		if strings.HasPrefix(canon, root+string(filepath.Separator)) {
			return canon, kind, ""
		}
	}
	return "", witnessNone, fmt.Sprintf("transcript_path %s outside the provider transcript root", path)
}

// corroborateHookRow returns "" when a hook row's witness confirms it, else
// the reason it does not.
func corroborateHookRow(e ConsoleEntry) string {
	if e.ToolUseID == "" || e.TranscriptPath == "" {
		return reasonNoWitness
	}
	canon, kind, reason := locateWitness(e.TranscriptPath)
	if reason != "" {
		return reason
	}
	facts := witnessFactsFor(kind, canon, e.ToolUseID)
	switch {
	case !facts.found:
		return "tool_use_id not in transcript"
	case facts.command != e.Command:
		return "command differs from transcript"
	case facts.exitCode == "":
		return "call not final in transcript: " + facts.nonFinal
	case facts.exitCode != rowExitCode(e):
		return fmt.Sprintf("exit code %s, transcript says %s", rowExitCode(e), facts.exitCode)
	case e.Outcome != HookOutcome(facts.exitCode):
		return fmt.Sprintf("outcome %s contradicts exit code %s", e.Outcome, facts.exitCode)
	}
	return ""
}

// reasonNoWitness is the one refusal a row may survive, as evidence of last
// resort: it names no transcript at all — a legacy row, or one from a road with
// no witness. Every other refusal is a row its own witness contradicts.
const reasonNoWitness = "no witness"

// rowExitCode reads exit_code whether it was written as a string or a number.
func rowExitCode(e ConsoleEntry) string {
	return strings.Trim(strings.TrimSpace(string(e.ExitCode)), `"`)
}

func witnessFactsFor(kind witnessKind, canon, id string) witnessFacts {
	info, err := os.Stat(canon)
	if err != nil {
		return witnessFacts{}
	}
	key := canon + "\x00" + id
	witnessCache.Lock()
	c, ok := witnessCache.m[key]
	witnessCache.Unlock()
	if ok && (c.facts.found && c.facts.exitCode != "" || c.size == info.Size()) {
		return c.facts
	}
	var facts witnessFacts
	switch kind {
	case witnessClaude:
		facts = scanClaudeWitness(canon, id)
	case witnessCodex:
		facts = scanCodexWitness(canon, id)
	}
	witnessCache.Lock()
	witnessCache.m[key] = cachedWitness{facts: facts, size: info.Size()}
	witnessCache.Unlock()
	return facts
}

// eachLineWith streams path and calls fn for every line containing needle,
// holding one line in memory — a transcript grows for the whole session.
func eachLineWith(path string, needle []byte, fn func([]byte)) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 64*1024)
	for {
		line, rerr := r.ReadBytes('\n')
		if bytes.Contains(line, needle) {
			fn(line)
		}
		if rerr != nil {
			return
		}
	}
}

var claudeExitLineRe = regexp.MustCompile(`^Exit code (-?\d+)`)

// scanClaudeWitness reads a Claude session transcript: the assistant record's
// Bash tool_use carries the command, the user record's tool_result carries
// is_error and its text, and toolUseResult says whether the call was sent to
// the background or interrupted. A success records no exit code, so exit 0 is
// inferred only from affirmative evidence: is_error present and false, and a
// toolUseResult object with no backgroundTaskId key at all (null included) and
// interrupted present and false. A failure's text leads with "Exit code N".
// Anything missing or malformed is not final.
func scanClaudeWitness(path, id string) witnessFacts {
	var facts witnessFacts
	result := false
	eachLineWith(path, []byte(id), func(line []byte) {
		var rec struct {
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
			ToolUseResult json.RawMessage `json:"toolUseResult"`
		}
		if json.Unmarshal(line, &rec) != nil {
			return
		}
		var items []struct {
			Type      string          `json:"type"`
			ID        string          `json:"id"`
			Name      string          `json:"name"`
			Input     json.RawMessage `json:"input"`
			ToolUseID string          `json:"tool_use_id"`
			IsError   *bool           `json:"is_error"`
			Content   json.RawMessage `json:"content"`
		}
		if json.Unmarshal(rec.Message.Content, &items) != nil {
			return
		}
		for _, it := range items {
			switch {
			case it.Type == "tool_use" && it.ID == id && it.Name == "Bash":
				var in struct {
					Command string `json:"command"`
				}
				_ = json.Unmarshal(it.Input, &in)
				facts.found, facts.command = true, in.Command
			case it.Type == "tool_result" && it.ToolUseID == id:
				result = true
				facts.exitCode, facts.nonFinal = claudeResultExit(it.IsError, it.Content, rec.ToolUseResult)
			}
		}
	})
	if !result {
		facts.exitCode, facts.nonFinal = "", "no result"
	}
	return facts
}

func claudeResultExit(isError *bool, content, toolUseResult json.RawMessage) (string, string) {
	if isError == nil {
		return "", "no is_error"
	}
	if *isError {
		if m := claudeExitLineRe.FindStringSubmatch(toolResultText(content)); m != nil {
			return m[1], ""
		}
		return "", "no exit code"
	}
	var r map[string]json.RawMessage
	if json.Unmarshal(toolUseResult, &r) != nil || r == nil {
		return "", "no completion record"
	}
	if _, bg := r["backgroundTaskId"]; bg {
		return "", "background"
	}
	var interrupted *bool
	if raw, ok := r["interrupted"]; !ok || json.Unmarshal(raw, &interrupted) != nil || interrupted == nil {
		return "", "no interrupted flag"
	}
	if *interrupted {
		return "", "interrupted"
	}
	return "0", ""
}

// toolResultText reads a tool_result's content, a string or text blocks.
func toolResultText(content json.RawMessage) string {
	var s string
	if json.Unmarshal(content, &s) == nil {
		return s
	}
	var blocks []struct {
		Text string `json:"text"`
	}
	_ = json.Unmarshal(content, &blocks)
	var b strings.Builder
	for _, bl := range blocks {
		b.WriteString(bl.Text)
	}
	return b.String()
}

// scanCodexWitness reads a Codex rollout: the latest item_completed record for
// id carries the argv — the shell's -c string is the command, unwrapped of the
// PII scrub muxcode may have added — and a numeric exit_code, which is
// required: a status alone is not a final exit code here, unlike the hook's
// own transcriptItemVerdict fallback.
func scanCodexWitness(path, id string) witnessFacts {
	var facts witnessFacts
	eachLineWith(path, []byte(`"id":"`+id+`"`), func(line []byte) {
		if !bytes.Contains(line, []byte(`"item_completed"`)) {
			return
		}
		var rec struct {
			Payload struct {
				Item struct {
					ID       string          `json:"id"`
					Command  json.RawMessage `json:"command"`
					ExitCode *int            `json:"exit_code"`
				} `json:"item"`
			} `json:"payload"`
		}
		if json.Unmarshal(line, &rec) != nil || rec.Payload.Item.ID != id {
			return
		}
		facts = witnessFacts{found: true}
		facts.command, _ = UnwrapScrub(codexShellCommand(rec.Payload.Item.Command))
		if code := rec.Payload.Item.ExitCode; code != nil {
			facts.exitCode = strconv.Itoa(*code)
		} else {
			facts.nonFinal = "no exit_code"
		}
	})
	return facts
}

// codexShellCommand reads a rollout's command: an argv whose last word is the
// shell's -c string, or a bare string.
func codexShellCommand(raw json.RawMessage) string {
	var argv []string
	if json.Unmarshal(raw, &argv) == nil {
		if len(argv) == 0 {
			return ""
		}
		return argv[len(argv)-1]
	}
	var s string
	_ = json.Unmarshal(raw, &s)
	return s
}

var rejectedRows = struct {
	sync.Mutex
	seen map[string]bool
}{seen: map[string]bool{}}

// logHistoryRowRejected writes one history-row-rejected lifecycle row per
// distinct refusal: the reader runs every executor tick, and a row refused
// once is refused identically on every later read.
func logHistoryRowRejected(session, role string, e ConsoleEntry, reason string) {
	file := HistoryPath(session, role)
	key := fmt.Sprintf("%s\x00%d\x00%s\x00%s", file, e.TS, e.ToolUseID, reason)
	rejectedRows.Lock()
	dup := rejectedRows.seen[key]
	rejectedRows.seen[key] = true
	rejectedRows.Unlock()
	if dup {
		return
	}
	LogLifecycle(session, "warn", "daemon", "history-row-rejected",
		fmt.Sprintf("%s row ts=%d role=%s: %s", file, e.TS, role, reason))
}
