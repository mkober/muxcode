package bus

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// DeliveryStatus represents the lifecycle state of a message.
type DeliveryStatus struct {
	ID          string `json:"id"`
	Status      string `json:"status"` // sent, delivered, acked, responded, expired
	SentAt      int64  `json:"sent_at"`
	DeliveredAt int64  `json:"delivered_at"`
	ResponseID  string `json:"response_id"`

	// Receipt fields (delivery-acknowledgement). A receipt is a POSITIVE signal
	// of receipt, in contrast to the old optimistic notified-IDs marker which
	// only recorded that the daemon SENT a wake-up.
	AckedAt     int64  `json:"acked_at,omitempty"`     // when the receipt was written
	AckedBy     string `json:"acked_by,omitempty"`     // role that received it
	ReceiptKind string `json:"receipt_kind,omitempty"` // ReceiptKindAck | ReceiptKindDelivered
}

// Delivery status constants.
const (
	StatusSent      = "sent"
	StatusDelivered = "delivered"
	StatusAcked     = "acked"
	StatusResponded = "responded"
	StatusExpired   = "expired"
)

// Receipt kinds distinguish HOW a message's receipt was obtained.
const (
	// ReceiptKindAck is a true consume-ack: the agent's OWN runtime read the
	// message out of its inbox (Claude via `muxcode inbox`, the local harness's
	// AgentLoop, or a --wait sender consuming its reply). A positive signal the
	// agent actually received the message.
	ReceiptKindAck = "ack"
	// ReceiptKindDelivered is a verified-inject receipt: the daemon injected the
	// payload into a non-hook TUI (OpenCode/Codex) and confirmed the text landed,
	// but the agent's runtime never consumed the inbox in-process. Weaker than an
	// ack — it confirms the pane received the text, not that the agent read it.
	ReceiptKindDelivered = "delivered"
)

// DeliveryDir returns the delivery status directory path for a session.
func DeliveryDir(session string) string {
	return filepath.Join(BusDir(session), "delivery")
}

// DeliveryPath returns the delivery status file path for a message ID.
func DeliveryPath(session, msgID string) string {
	return filepath.Join(DeliveryDir(session), msgID+".status")
}

// CreateDeliveryStatus writes the initial "sent" status file for a message.
// Called by Send() after appending the message to the recipient's inbox.
// The delivery directory is created by Init() at session start.
func CreateDeliveryStatus(session string, m Message) error {
	ds := DeliveryStatus{
		ID:     m.ID,
		Status: StatusSent,
		SentAt: m.TS,
	}
	return writeDeliveryStatus(session, ds)
}

// MarkResponded updates the original message's delivery status to "responded"
// and records the response message ID. Called by Send() when ReplyTo is set.
func MarkResponded(session, originalID, responseID string) {
	if originalID == "" {
		return
	}
	ds, err := ReadDeliveryStatus(session, originalID)
	if err != nil {
		return // no status file
	}
	ds.Status = StatusResponded
	ds.ResponseID = responseID
	_ = writeDeliveryStatus(session, ds)
}

// WriteReceipt records that a message was received, keyed by message ID. ackedBy
// is the role that received it; kind is ReceiptKindAck (true consume) or
// ReceiptKindDelivered (verified inject). It extends the existing per-message
// delivery-status file, creating a minimal one if the message predates delivery
// tracking or its status was GC'd, so a receipt is never lost just because the
// "sent" status is missing. A receipt never regresses a message already marked
// responded (a reply already implies receipt).
func WriteReceipt(session, msgID, ackedBy, kind string) {
	ds, err := ReadDeliveryStatus(session, msgID)
	if err != nil {
		ds = DeliveryStatus{ID: msgID}
	}
	ds.AckedAt = time.Now().Unix()
	ds.AckedBy = ackedBy
	ds.ReceiptKind = kind
	// Advance lifecycle status without regressing past a recorded response.
	if ds.Status != StatusResponded {
		if kind == ReceiptKindAck {
			ds.Status = StatusAcked
		} else if ds.Status == StatusSent || ds.Status == "" {
			ds.Status = StatusDelivered
		}
	}
	_ = writeDeliveryStatus(session, ds)
}

// ReadReceipt returns a message's delivery status and whether it carries a
// receipt (AckedAt set). Used by the daemon's receipt-gap backstop and by the
// delivery decisions that replace the notified-IDs marker.
func ReadReceipt(session, msgID string) (DeliveryStatus, bool) {
	ds, err := ReadDeliveryStatus(session, msgID)
	if err != nil {
		return DeliveryStatus{}, false
	}
	return ds, ds.AckedAt > 0
}

// ReceiptGap returns messages still sitting in role's inbox that carry no
// receipt and have waited longer than olderThan. A non-empty gap means the
// agent's self-poll has consumed nothing recently — its poll loop or delivery
// sidecar may be dead. This is the positive-signal replacement for pane-scrape
// wedge detection. Self-addressed messages are ignored (they never warrant
// delivery and would otherwise register a permanent gap).
func ReceiptGap(session, role string, olderThan time.Duration) []Message {
	msgs, err := Peek(session, role)
	if err != nil || len(msgs) == 0 {
		return nil
	}
	cutoff := time.Now().Add(-olderThan).Unix()
	var gap []Message
	for _, m := range msgs {
		if isLoopingSelfSend(m) {
			continue
		}
		if m.TS > cutoff {
			continue // too fresh to count as stuck
		}
		if _, acked := ReadReceipt(session, m.ID); acked {
			continue // already receipted (shouldn't linger in the inbox, but be safe)
		}
		gap = append(gap, m)
	}
	return gap
}

// ReadDeliveryStatus reads the delivery status for a message ID.
func ReadDeliveryStatus(session, msgID string) (DeliveryStatus, error) {
	data, err := os.ReadFile(DeliveryPath(session, msgID))
	if err != nil {
		return DeliveryStatus{}, err
	}
	var ds DeliveryStatus
	if err := json.Unmarshal(data, &ds); err != nil {
		return DeliveryStatus{}, err
	}
	return ds, nil
}

// ListDeliveryStatuses returns all delivery status entries for a session.
func ListDeliveryStatuses(session string) ([]DeliveryStatus, error) {
	dir := DeliveryDir(session)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var statuses []DeliveryStatus
	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var ds DeliveryStatus
		if err := json.Unmarshal(data, &ds); err != nil {
			continue
		}
		statuses = append(statuses, ds)
	}
	return statuses, nil
}

// CleanExpiredDeliveries removes delivery status files older than maxAge.
// Uses SentAt from the JSON payload (not file mtime) because status
// transitions update mtime, making mtime unreliable for age checks.
// Called by the daemon during periodic cleanup.
func CleanExpiredDeliveries(session string, maxAge time.Duration) int {
	dir := DeliveryDir(session)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}

	cutoff := time.Now().Add(-maxAge).Unix()
	cleaned := 0
	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var ds DeliveryStatus
		if err := json.Unmarshal(data, &ds); err != nil {
			// Malformed file — remove it
			if os.Remove(path) == nil {
				cleaned++
			}
			continue
		}
		if ds.SentAt < cutoff {
			if os.Remove(path) == nil {
				cleaned++
			}
		}
	}
	return cleaned
}

// FormatDeliveryStatus returns a human-readable string for a delivery status.
func FormatDeliveryStatus(ds DeliveryStatus) string {
	s := ds.ID + "  " + ds.Status
	if ds.DeliveredAt > 0 {
		latency := time.Duration(ds.DeliveredAt-ds.SentAt) * time.Second
		s += fmt.Sprintf("  (delivered in %s)", latency)
	}
	if ds.AckedAt > 0 {
		receipt := "  receipt=" + ds.ReceiptKind
		if ds.AckedBy != "" {
			receipt += "(" + ds.AckedBy + ")"
		}
		s += receipt
	}
	if ds.ResponseID != "" {
		s += "  response=" + ds.ResponseID
	}
	return s
}

// writeDeliveryStatus writes a delivery status to its file.
func writeDeliveryStatus(session string, ds DeliveryStatus) error {
	data, err := json.Marshal(ds)
	if err != nil {
		return err
	}
	return os.WriteFile(DeliveryPath(session, ds.ID), data, 0644)
}

// AckDeliveryTogglePath is the marker file whose presence activates the
// receipt-based delivery cutover at runtime. The daemon's ackDeliveryActive()
// re-reads it every poll, so touching / removing it flips the cutover (and rolls
// it back) instantly without restarting the daemon — unlike the startup-only
// MUXCODE_DELIVERY_ACK env var. MUXCODE_DELIVERY_ACK_DISABLE still hard-overrides
// this marker to force the old path.
func AckDeliveryTogglePath(session string) string {
	return filepath.Join(BusDir(session), "delivery-ack.on")
}

// AckDeliveryToggleOn reports whether the runtime cutover marker file is present.
func AckDeliveryToggleOn(session string) bool {
	_, err := os.Stat(AckDeliveryTogglePath(session))
	return err == nil
}

// SetAckDeliveryToggle creates (on) or removes (off) the runtime cutover marker.
// Removing an absent marker is not an error, so `off` is idempotent.
func SetAckDeliveryToggle(session string, on bool) error {
	path := AckDeliveryTogglePath(session)
	if on {
		return os.WriteFile(path, []byte(time.Now().Format(time.RFC3339)+"\n"), 0644)
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
