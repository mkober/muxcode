package bus

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
)

// IsAutoCCRole returns true if messages from this role are auto-CC'd to edit.
func IsAutoCCRole(role string) bool {
	return GetAutoCC()[role]
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

// sendMessage is the shared implementation for Send and SendNoCC.
func sendMessage(session string, m Message, autoCC bool) error {
	data, err := EncodeMessage(m)
	if err != nil {
		return err
	}
	line := append(data[:len(data):len(data)], '\n')

	// Ensure inbox directory exists
	inboxDir := filepath.Dir(InboxPath(session, m.To))
	if err := os.MkdirAll(inboxDir, 0755); err != nil {
		return err
	}

	// Append to recipient inbox
	if err := appendToFile(InboxPath(session, m.To), line); err != nil {
		return err
	}

	// Auto-CC to edit: copy messages from auto-CC roles when not already going to edit
	if autoCC && IsAutoCCRole(m.From) && m.To != "edit" {
		if err := appendToFile(InboxPath(session, "edit"), line); err != nil {
			fmt.Fprintf(os.Stderr, "warning: auto-CC to edit failed: %v\n", err)
		}
	}

	// Append to log
	logDir := filepath.Dir(LogPath(session))
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return err
	}
	return appendToFile(LogPath(session), line)
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

	return msgs, err
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
