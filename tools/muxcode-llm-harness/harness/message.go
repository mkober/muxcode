package harness

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Message represents a bus message between agents.
type Message struct {
	ID      string `json:"id"`
	TS      int64  `json:"ts"`
	From    string `json:"from"`
	To      string `json:"to"`
	Type    string `json:"type"`
	Action  string `json:"action"`
	Payload string `json:"payload"`
	ReplyTo string `json:"reply_to"`
}

// ParseMessages parses JSONL output (one JSON object per line) into messages.
func ParseMessages(jsonlOutput string) ([]Message, error) {
	var msgs []Message
	for _, line := range strings.Split(jsonlOutput, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var m Message
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			return nil, fmt.Errorf("parsing message line: %w", err)
		}
		msgs = append(msgs, m)
	}
	return msgs, nil
}

// FormatTask formats a batch of messages as a structured task for the LLM.
func FormatTask(msgs []Message) string {
	if len(msgs) == 0 {
		return ""
	}

	var b strings.Builder
	for i, m := range msgs {
		if i > 0 {
			b.WriteString("\n---\n\n")
		}
		b.WriteString("## Task")
		if len(msgs) > 1 {
			b.WriteString(fmt.Sprintf(" %d", i+1))
		}
		b.WriteString("\n\n")
		b.WriteString(fmt.Sprintf("- **Action**: %s\n", m.Action))
		b.WriteString(fmt.Sprintf("- **From**: %s\n", m.From))
		b.WriteString(fmt.Sprintf("- **Instructions**: %s\n", m.Payload))
	}
	b.WriteString("\nExecute this task now using your available tools. Do NOT run `muxcode-agent-bus inbox`.\n")
	return b.String()
}
