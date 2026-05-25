package bus

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

// Default dedup window: suppress duplicate messages within this period.
const defaultDedupSecs = 30

// DedupWindowSecs returns the dedup window from MUXCODE_DEDUP_WINDOW env var,
// or the default (30s). Set to 0 to disable dedup.
func DedupWindowSecs() int64 {
	if v := os.Getenv("MUXCODE_DEDUP_WINDOW"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err == nil && n >= 0 {
			return n
		}
	}
	return defaultDedupSecs
}

// IsDuplicateMessage checks the session log for a recent message with the same
// (from, to, action, type) tuple within the dedup window. Returns true if a
// duplicate is found. System actions are never deduped.
func IsDuplicateMessage(session string, m Message) bool {
	window := DedupWindowSecs()
	if window == 0 {
		return false
	}

	// System actions repeat naturally — never suppress them
	if isSystemAction(m.Action) {
		return false
	}

	// Responses are replies to specific requests — never suppress them.
	// Without this, two consecutive test→edit responses get deduped because
	// (from, to, action, type) matches even though they answer different requests.
	if m.Type == "response" {
		return false
	}

	cutoff := time.Now().Unix() - window
	return hasDuplicateInLog(LogPath(session), m.From, m.To, m.Action, m.Type, cutoff)
}

// hasDuplicateInLog reads the tail of the log file and checks for matching entries.
// Only scans the last ~8KB to keep it fast (typically covers 30-60s of messages).
func hasDuplicateInLog(logPath, from, to, action, msgType string, cutoff int64) bool {
	f, err := os.Open(logPath)
	if err != nil {
		return false
	}
	defer f.Close()

	// Seek to tail: last 8KB covers ~50-100 messages
	const tailSize = 8192
	info, err := f.Stat()
	if err != nil {
		return false
	}
	offset := info.Size() - tailSize
	if offset < 0 {
		offset = 0
	}
	if offset > 0 {
		if _, err := f.Seek(offset, 0); err != nil {
			return false
		}
	}

	scanner := bufio.NewScanner(f)
	// If we seeked mid-line, skip the partial first line
	if offset > 0 {
		scanner.Scan()
	}

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		msg, err := DecodeMessage(line)
		if err != nil {
			continue
		}
		// Skip old messages
		if msg.TS < cutoff {
			continue
		}
		// Match on (from, to, action, type)
		if msg.From == from && msg.To == to && msg.Action == action && msg.Type == msgType {
			return true
		}
	}
	return false
}

// dedupLockPath returns the path for the session-level dedup lock file.
func dedupLockPath(session string) string {
	return filepath.Join(BusDir(session), "dedup.lock")
}

// sendIfNotDuplicate is the shared implementation for atomic dedup+send.
// It acquires a file lock, checks for duplicates, and sends via the given
// sendFn, eliminating the TOCTOU race between IsDuplicateMessage and send.
// Returns (sent bool, err error). If duplicate, sent is false and err is nil.
func sendIfNotDuplicate(session string, m Message, sendFn func(string, Message) error) (bool, error) {
	// Acquire dedup lock
	lockPath := dedupLockPath(session)
	if err := os.MkdirAll(filepath.Dir(lockPath), 0755); err != nil {
		return false, err
	}
	lf, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		// Lock unavailable — fall through without dedup protection
		if IsDuplicateMessage(session, m) {
			return false, nil
		}
		return true, sendFn(session, m)
	}
	defer lf.Close()

	if err := syscall.Flock(int(lf.Fd()), syscall.LOCK_EX); err != nil {
		// Lock failed — fall through without dedup protection
		if IsDuplicateMessage(session, m) {
			return false, nil
		}
		return true, sendFn(session, m)
	}
	defer syscall.Flock(int(lf.Fd()), syscall.LOCK_UN)

	// Under lock: check + send atomically
	if IsDuplicateMessage(session, m) {
		return false, nil
	}
	if err := sendFn(session, m); err != nil {
		return false, err
	}
	return true, nil
}

// SendIfNotDuplicate atomically checks for duplicates and sends via Send()
// under a file lock. Returns (sent bool, err error).
func SendIfNotDuplicate(session string, m Message) (bool, error) {
	return sendIfNotDuplicate(session, m, Send)
}

// SendNoCCIfNotDuplicate atomically checks for duplicates and sends via
// SendNoCC() under a file lock. Returns (sent bool, err error).
func SendNoCCIfNotDuplicate(session string, m Message) (bool, error) {
	return sendIfNotDuplicate(session, m, SendNoCC)
}

// HasPendingInboxRequest checks if the target's inbox already contains a
// request message with the same (from, action, payload) tuple. Prevents
// stacking duplicate requests when the agent hasn't consumed the inbox yet.
// The payload check ensures that legitimately different requests with the
// same action (e.g. sequential builds with different context) are not
// suppressed — only true duplicates (exact same payload) are caught.
// Only checks request-type messages — responses and events are never deduped.
func HasPendingInboxRequest(session, to, from, action, payload string) bool {
	// Resolve hosted roles to their host inbox
	inboxRole := WindowForRole(to)
	msgs, err := Peek(session, inboxRole)
	if err != nil || len(msgs) == 0 {
		return false
	}
	for _, m := range msgs {
		if m.Type == "request" && m.From == from && m.Action == action && m.Payload == payload {
			return true
		}
	}
	return false
}

// HasInFlightTaskForRole checks if there is an in-flight task targeting
// the given role and action. This indicates the agent has already consumed
// the message and is actively working on it — sending another identical
// request would create a duplicate prompt injection (especially for
// non-hook providers like OpenCode where messages are injected via send-keys).
func HasInFlightTaskForRole(session, to, action string) bool {
	tasks, err := ListTasks(session, TaskInFlight)
	if err != nil || len(tasks) == 0 {
		return false
	}
	for _, t := range tasks {
		if t.To == to && t.Action == action {
			return true
		}
	}
	return false
}

// FindInFlightTask returns the first in-flight task matching (to, action),
// or an empty task and false if none found. Used by --wait reattachment.
func FindInFlightTask(session, to, action string) (Task, bool) {
	tasks, err := ListTasks(session, TaskInFlight)
	if err != nil || len(tasks) == 0 {
		return Task{}, false
	}
	for _, t := range tasks {
		if t.To == to && t.Action == action {
			return t, true
		}
	}
	return Task{}, false
}
