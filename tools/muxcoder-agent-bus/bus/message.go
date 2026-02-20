package bus

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
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

// NewMsgID generates a unique message ID: {unix_ts}-{from}-{4hex}.
func NewMsgID(from string) string {
	ts := time.Now().Unix()
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	h := hex.EncodeToString(b)
	return fmt.Sprintf("%d-%s-%s", ts, from, h[:8])
}

// NewMessage creates a new Message with auto-generated ID and timestamp.
func NewMessage(from, to, msgType, action, payload, replyTo string) Message {
	return Message{
		ID:      NewMsgID(from),
		TS:      time.Now().Unix(),
		From:    from,
		To:      to,
		Type:    msgType,
		Action:  action,
		Payload: payload,
		ReplyTo: replyTo,
	}
}

// EncodeMessage serializes a Message to compact JSON bytes.
func EncodeMessage(m Message) ([]byte, error) {
	return json.Marshal(m)
}

// DecodeMessage deserializes a JSON line into a Message.
func DecodeMessage(line []byte) (Message, error) {
	var m Message
	err := json.Unmarshal(line, &m)
	return m, err
}

// FormatMessage returns a human-readable representation of a Message.
func FormatMessage(m Message) string {
	t := time.Unix(m.TS, 0)
	s := fmt.Sprintf("--- Message from %s at %s ---\n", m.From, t.Format("15:04:05"))
	s += fmt.Sprintf("Type: %s  Action: %s\n", m.Type, m.Action)
	s += fmt.Sprintf("Content: %s\n", m.Payload)
	if m.ReplyTo != "" {
		s += fmt.Sprintf("Reply to: %s\n", m.ReplyTo)
	}
	s += fmt.Sprintf("To reply: muxcoder-agent-bus send %s <action> \"<message>\" --type response --reply-to %s\n", m.From, m.ID)
	return s
}
