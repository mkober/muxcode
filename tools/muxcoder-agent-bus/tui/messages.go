package tui

import (
	"regexp"
	"strings"
	"time"
)

// MessageBuffer is a rolling buffer of recent inter-agent messages.
type MessageBuffer struct {
	messages []string
	maxSize  int
}

var msgPatternRe = regexp.MustCompile(`(?i)(Message from|SendMessage|broadcast|→|muxcoder-agent-bus send|muxcoder-agent-bus inbox|agent-bus send|agent-bus inbox|Sent\s.*to )`)

// NewMessageBuffer creates a new message buffer with the given capacity.
func NewMessageBuffer(size int) *MessageBuffer {
	return &MessageBuffer{
		messages: make([]string, 0, size),
		maxSize:  size,
	}
}

// Add appends a message to the buffer, trimming to maxSize.
func (mb *MessageBuffer) Add(msg string) {
	mb.messages = append(mb.messages, msg)
	if len(mb.messages) > mb.maxSize {
		mb.messages = mb.messages[len(mb.messages)-mb.maxSize:]
	}
}

// Messages returns the current messages in the buffer.
func (mb *MessageBuffer) Messages() []string {
	result := make([]string, len(mb.messages))
	copy(result, mb.messages)
	return result
}

// ScanMessages scans pane output for inter-agent message patterns and adds
// any matches to the buffer.
func (mb *MessageBuffer) ScanMessages(window, output string) {
	ts := time.Now().Format("15:04")
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if msgPatternRe.MatchString(trimmed) {
			short := trimmed
			if len([]rune(short)) > 60 {
				short = string([]rune(short)[:60])
			}
			mb.Add(ts + "  " + window + ": " + short)
		}
	}
}
