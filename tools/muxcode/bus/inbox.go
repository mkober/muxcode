package bus

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// IsAutoCCRole returns true if messages from this role are auto-CC'd to edit.
func IsAutoCCRole(role string) bool {
	return GetAutoCC()[role]
}

// autoCCLastSent tracks the last CC time per role for rate limiting.
var autoCCLastSent = make(map[string]int64)

// autoCCWindowSecs is the minimum interval between CC messages from the same role.
const autoCCWindowSecs int64 = 60

// shouldAutoCC returns true if a CC from this role should be delivered to edit.
// Rate-limits to one CC per role per 60-second window.
func shouldAutoCC(from string) bool {
	now := time.Now().Unix()
	if last, ok := autoCCLastSent[from]; ok && now-last < autoCCWindowSecs {
		return false
	}
	autoCCLastSent[from] = now
	return true
}

// Send appends a message to the recipient's inbox and the session log.
// Messages from build, test, and review are automatically CC'd to edit.
func Send(session string, m Message) error {
	return sendMessage(session, m, true)
}

// SendNoCC appends a message to the recipient's inbox and the session log
// without auto-CC to edit. Use for chain intermediate messages, analyst
// notifications, and subscription fan-out where CC would be redundant.
func SendNoCC(session string, m Message) error {
	return sendMessage(session, m, false)
}

// isLoopingSelfSend reports whether a message is an accidental self-addressed
// message (from == to) that would create a notification loop. Such a message
// lands in the sender's own inbox as an "actionable" request, the daemon wakes
// the agent to act on it, the agent can't meaningfully complete its own
// request, and it re-surfaces on every idle cycle.
//
// The "startup" action is EXEMPT: PreLaunchSetup intentionally seeds each
// agent's inbox with a self-addressed startup request and relies on the
// daemon's re-wake (HasActionableMessages) to keep waking the agent until it
// consumes the message and restores context. That is the one legitimate
// self-send; everything else is an addressing mistake.
func isLoopingSelfSend(m Message) bool {
	return m.From != "" && m.From == m.To && m.Action != "startup"
}

// sendMessage is the shared implementation for Send and SendNoCC.
func sendMessage(session string, m Message, autoCC bool) error {
	// Drop accidental self-addressed messages at the source so the loop is
	// impossible for all providers (non-hook providers already discarded these
	// at wake-up time; Claude/hook agents had no such guard). The startup
	// bootstrap self-send is exempt — see isLoopingSelfSend.
	if isLoopingSelfSend(m) {
		fmt.Fprintf(os.Stderr, "  [send] dropping self-addressed message %s→%s:%s (self-sends are not delivered)\n",
			m.From, m.To, m.Action)
		return nil
	}

	// Git mutations are user-initiated — enforced HERE, at the one function every
	// send funnels through, not only at the `muxcode send` CLI. The CLI is just
	// one of 30+ callers: daemon event chains, subscriptions, hooks, and the
	// webhook HTTP endpoint all reach the inbox through this path and would
	// otherwise sail straight past a CLI-only gate. The webhook is the sharpest
	// of them — an HTTP POST could order a commit and a push.
	//
	// No legitimate chain or subscription sends a `commit` action (verified in
	// DefaultConfig), so nothing routine is broken by refusing them here.
	// Requests only: a response echoing the action label back to the commit agent
	// (the reply template does exactly that) is not actionable, and refusing it
	// would strand commit's tracked task.
	if m.Type == "request" {
		if deny := CheckCommitAuthority(m.From, m.To, m.Action); deny != "" {
			fmt.Fprintf(os.Stderr, "  [send] REFUSED %s→%s:%s — %s\n", m.From, m.To, m.Action, deny)
			LogLifecycle(session, "warn", "bus", "commit-authority-refused", m.From)
			return fmt.Errorf("%s", deny)
		}
	}

	data, err := EncodeMessage(m)
	if err != nil {
		return err
	}
	line := append(data[:len(data):len(data)], '\n')

	// Resolve hosted roles: deliver to the host agent's inbox.
	// The message retains the original To field so the host knows the context.
	inboxRole := WindowForRole(m.To)

	// Ensure inbox directory exists
	inboxDir := filepath.Dir(InboxPath(session, inboxRole))
	if err := os.MkdirAll(inboxDir, 0755); err != nil {
		return err
	}

	// Guard against duplicate requests: prevent identical requests from
	// stacking in the agent's inbox. This is the primary dedup gate and
	// covers ALL send paths (CLI, daemon, chains, subscriptions).
	//
	// Two checks:
	// 1. Inbox check: is the same (from, action) request already pending?
	// 2. Task check: is the agent already working on a task with same (to, action)?
	//
	// Skipped for system actions (loop-detected, compact-recommended, etc.)
	// which naturally repeat and should never be suppressed.
	if m.Type == "request" && !isSystemAction(m.Action) {
		if HasPendingInboxRequest(session, m.To, m.From, m.Action, m.Payload) {
			fmt.Fprintf(os.Stderr, "  [send] suppressing duplicate request %s→%s:%s (identical request already in inbox)\n",
				m.From, m.To, m.Action)
			return nil
		}
		if HasInFlightTaskForRole(session, m.To, m.Action) {
			fmt.Fprintf(os.Stderr, "  [send] suppressing duplicate request %s→%s:%s (in-flight task exists)\n",
				m.From, m.To, m.Action)
			return nil
		}
	}

	// Guard against duplicate replies: if this message is a reply to a task
	// that is already completed (e.g. the daemon sent a synthetic response
	// via idle-task-rescue, and the real agent sends a late reply), skip
	// delivery to avoid the requester receiving conflicting responses.
	// Check BEFORE writing to inbox so nothing is written anywhere.
	if m.ReplyTo != "" {
		if t, err := ReadTask(session, m.ReplyTo); err == nil && t.Status == TaskCompleted {
			fmt.Fprintf(os.Stderr, "  [send] suppressing duplicate reply to already-completed task %s from %s\n", m.ReplyTo, m.From)
			return nil
		}
	}

	// Append to recipient inbox (host inbox for hosted roles)
	if err := appendToFile(InboxPath(session, inboxRole), line); err != nil {
		return err
	}

	// Auto-CC to edit: copy messages from auto-CC roles when not already going to edit.
	// Rate-limited to 1 CC per role per 60s to prevent context pressure on edit.
	if autoCC && IsAutoCCRole(m.From) && m.To != "edit" && inboxRole != "edit" {
		if shouldAutoCC(m.From) {
			if err := appendToFile(InboxPath(session, "edit"), line); err != nil {
				fmt.Fprintf(os.Stderr, "warning: auto-CC to edit failed: %v\n", err)
			}
		}
	}

	// Delivery tracking: create "sent" status, mark original as "responded" on reply
	if err := CreateDeliveryStatus(session, m); err != nil {
		fmt.Fprintf(os.Stderr, "warning: delivery status creation failed: %v\n", err)
	}
	if m.ReplyTo != "" {
		MarkResponded(session, m.ReplyTo, m.ID)
	}

	// Append to log
	logDir := filepath.Dir(LogPath(session))
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return err
	}
	return appendToFile(LogPath(session), line)
}

// FindMessageByID searches the session log for a message with the given ID.
// Scans the log in reverse for efficiency since recent messages are at the end.
// Returns the message and true if found, or an empty message and false if not.
func FindMessageByID(session, msgID string) (Message, bool) {
	data, err := os.ReadFile(LogPath(session))
	if err != nil {
		return Message{}, false
	}
	// Scan lines in reverse (most recent first)
	lines := bytes.Split(data, []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		line := bytes.TrimSpace(lines[i])
		if len(line) == 0 {
			continue
		}
		// Quick check before full parse
		if !bytes.Contains(line, []byte(msgID)) {
			continue
		}
		msg, err := DecodeMessage(line)
		if err != nil {
			continue
		}
		if msg.ID == msgID {
			return msg, true
		}
	}
	return Message{}, false
}

// Receive reads and consumes all messages from a role's inbox.
// Uses atomic rename to avoid losing messages.
func Receive(session, role string) ([]Message, error) {
	inbox := InboxPath(session, role)
	consuming := inbox + ".consuming"

	// Atomic rename: move inbox to consuming file
	if err := os.Rename(inbox, consuming); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	// Touch new empty inbox
	if err := touchFile(inbox); err != nil {
		// Non-fatal: inbox will be recreated on next send
		_ = err
	}

	// Read and parse consuming file
	msgs, err := readMessages(consuming)

	// Remove consuming file regardless of read errors
	_ = os.Remove(consuming)

	// Mark all consumed messages as delivered
	for _, m := range msgs {
		MarkDelivered(session, m.ID)
	}

	// Clear notification state — agent has consumed all messages, start fresh.
	clearNotifiedIDs(session, role)

	return msgs, err
}

// ReceiveFrom reads and consumes only messages from a specific sender,
// leaving messages from other senders in the inbox.
func ReceiveFrom(session, role, fromRole string) ([]Message, error) {
	inbox := InboxPath(session, role)
	consuming := inbox + ".consuming"

	// Atomic rename: move inbox to consuming file
	if err := os.Rename(inbox, consuming); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	// Touch new empty inbox (new messages can arrive while we filter)
	if err := touchFile(inbox); err != nil {
		_ = err
	}

	// Read all messages from consuming file
	all, err := readMessages(consuming)
	_ = os.Remove(consuming)
	if err != nil {
		return nil, err
	}

	// Split into matched (from target) and unmatched (from others)
	var matched, rest []Message
	for _, m := range all {
		if m.From == fromRole {
			matched = append(matched, m)
		} else {
			rest = append(rest, m)
		}
	}

	// Mark consumed messages as delivered
	for _, m := range matched {
		MarkDelivered(session, m.ID)
	}

	// Mark consumed message IDs as notified (partial consumption —
	// only these messages are consumed, not the entire inbox).
	if len(matched) > 0 {
		ids := make([]string, 0, len(matched))
		for _, m := range matched {
			ids = append(ids, m.ID)
		}
		addNotifiedIDs(session, role, ids)
	}

	// Write unmatched messages back to inbox (prepend before any new arrivals)
	if len(rest) > 0 {
		var buf []byte
		for _, m := range rest {
			data, encErr := EncodeMessage(m)
			if encErr != nil {
				continue
			}
			buf = append(buf, data...)
			buf = append(buf, '\n')
		}
		// Read any new messages that arrived since the rename
		newData, _ := os.ReadFile(inbox)
		// Prepend rest + append new arrivals
		combined := append(buf, newData...)
		if writeErr := os.WriteFile(inbox, combined, 0644); writeErr != nil {
			// Best effort: try appending instead
			_ = appendToFile(inbox, buf)
		}
	}

	return matched, nil
}

// ReceiveFromFunc reads and consumes only messages where matchFn(m.From)
// returns true, leaving other messages in the inbox. Used by --wait to
// accept responses from both a hosted role and its host agent.
func ReceiveFromFunc(session, role string, matchFn func(string) bool) ([]Message, error) {
	inbox := InboxPath(session, role)
	consuming := inbox + ".consuming"

	if err := os.Rename(inbox, consuming); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	if err := touchFile(inbox); err != nil {
		_ = err
	}

	all, err := readMessages(consuming)
	_ = os.Remove(consuming)
	if err != nil {
		return nil, err
	}

	var matched, rest []Message
	for _, m := range all {
		if matchFn(m.From) {
			matched = append(matched, m)
		} else {
			rest = append(rest, m)
		}
	}

	// Mark consumed messages as delivered
	for _, m := range matched {
		MarkDelivered(session, m.ID)
	}

	// Mark consumed message IDs as notified (partial consumption —
	// only these messages are consumed, not the entire inbox).
	if len(matched) > 0 {
		ids := make([]string, 0, len(matched))
		for _, m := range matched {
			ids = append(ids, m.ID)
		}
		addNotifiedIDs(session, role, ids)
	}

	if len(rest) > 0 {
		var buf []byte
		for _, m := range rest {
			data, encErr := EncodeMessage(m)
			if encErr != nil {
				continue
			}
			buf = append(buf, data...)
			buf = append(buf, '\n')
		}
		newData, _ := os.ReadFile(inbox)
		combined := append(buf, newData...)
		if writeErr := os.WriteFile(inbox, combined, 0644); writeErr != nil {
			_ = appendToFile(inbox, buf)
		}
	}

	return matched, nil
}

// Peek reads messages from a role's inbox without consuming them.
func Peek(session, role string) ([]Message, error) {
	return readMessages(InboxPath(session, role))
}

// HasMessages returns true if the role's inbox has messages.
func HasMessages(session, role string) bool {
	info, err := os.Stat(InboxPath(session, role))
	if err != nil {
		return false
	}
	return info.Size() > 0
}

// HasActionableMessages returns true if the role's inbox contains at least one
// message that requires action (i.e. a "request" type). Response and event
// messages are informational — they don't require the agent to do anything.
// Use this instead of HasMessages() for wake-up decisions to prevent echo loops
// where agents keep acknowledging each other's responses.
func HasActionableMessages(session, role string) bool {
	msgs, err := Peek(session, role)
	if err != nil || len(msgs) == 0 {
		return false
	}
	for _, m := range msgs {
		// Accidental self-addressed requests never warrant a wake-up — they
		// would loop forever since the agent can't complete its own request.
		// (The startup self-send is exempt — see isLoopingSelfSend.)
		if m.Type != "request" || isLoopingSelfSend(m) {
			continue
		}
		// A request is actionable for this role ONLY if the role is its primary
		// destination. Auto-CC copies a request addressed to another agent (e.g.
		// test→review) verbatim into edit's inbox — the copy keeps To="review".
		// Such a CC is informational, not work edit can complete; counting it as
		// actionable made the daemon re-wake edit indefinitely for a request it
		// can never respond to. WindowForRole matches the delivery routing used
		// in sendMessage, so a genuinely-addressed (or hosted) request still
		// counts while a CC does not.
		if WindowForRole(m.To) != role {
			continue
		}
		return true
	}
	return false
}

// InboxCount returns the number of messages in a role's inbox.
func InboxCount(session, role string) int {
	data, err := os.ReadFile(InboxPath(session, role))
	if err != nil {
		return 0
	}
	count := 0
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) > 0 {
			count++
		}
	}
	return count
}

// AppendToInbox writes a message directly to a role's inbox without
// delivery tracking, auto-CC, or notify. Used to restore unconsumed
// messages after filtered consumption.
func AppendToInbox(session, role string, m Message) error {
	data, err := EncodeMessage(m)
	if err != nil {
		return err
	}
	line := append(data[:len(data):len(data)], '\n')
	return appendToFile(InboxPath(session, role), line)
}

// appendToFile appends data to a file, creating it if necessary.
func appendToFile(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(data)
	return err
}

// touchFile creates an empty file if it doesn't exist.
func touchFile(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	return f.Close()
}

// readMessages reads and parses all JSONL messages from a file.
func readMessages(path string) ([]Message, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var msgs []Message
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		m, err := DecodeMessage(line)
		if err != nil {
			continue // skip malformed lines
		}
		msgs = append(msgs, m)
	}
	return msgs, scanner.Err()
}
