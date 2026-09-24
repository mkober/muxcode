package bus

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

// Message represents a bus message between agents.
//
// GraphRun and GraphNode carry a graph dispatch's provenance, and exist so the
// runtime backstop can tell a graph node's request from the user's agent's.
// Every graph send is From="daemon", which NormalizeBusRole maps to "edit" —
// the default commit authority — so before these fields a graph-dispatched
// commit cleared CheckCommitAuthority as if edit had asked for it, and the
// whole weight of the guarantee fell on the gate (MUX-144 Defect C). They are
// omitempty, so messages written before the fields existed still decode.
//
// The Origin fields are what a recipient reads to trace a prompt back to a run
// or a person. Send derives them itself and nothing else sets them — see
// stampMessageOrigin.
type Message struct {
	ID      string `json:"id"`
	TS      int64  `json:"ts"`
	From    string `json:"from"`
	To      string `json:"to"`
	Type    string `json:"type"`
	Action  string `json:"action"`
	Payload string `json:"payload"`
	ReplyTo string `json:"reply_to"`

	GraphRun  string `json:"graph_run,omitempty"`
	GraphNode string `json:"graph_node,omitempty"`

	OriginRun       string `json:"origin_run,omitempty"`
	OriginCreatedBy string `json:"origin_created_by,omitempty"`
	OriginRunState  string `json:"origin_run_state,omitempty"`
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
	s += formatMessageOrigin(m)
	s += fmt.Sprintf("Content: %s\n", m.Payload)
	if m.ReplyTo != "" {
		s += fmt.Sprintf("Reply to: %s\n", m.ReplyTo)
	}
	// The reply target is normalized because non-agent sender identities
	// ("daemon" — graph executor dispatch, daemon requests) are not valid
	// send targets: an agent that followed a raw "muxcode send daemon ..."
	// instruction got "unknown role" and its response was never recorded.
	// Response correlation runs on --reply-to, so routing the reply to the
	// normalized role (daemon → edit) loses nothing.
	s += fmt.Sprintf("To reply: muxcode send %s <action> \"<message>\" --type response --reply-to %s\n", NormalizeBusRole(m.From), m.ID)
	return s
}
