package bus

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

// MarkResponded updates the original message's delivery status to "responded",
// records the response message ID, and drains the now-answered request from the
// recipient's inbox.
//
// The drain lives HERE, not at the call sites, because there is more than one
// way a request becomes "responded":
//   - Send() when a reply carries ReplyTo.
//   - cmd/send.go's --wait fallback, which correlates a response that was sent
//     WITHOUT ReplyTo back to the request it was waiting on.
//
// The second path has no reply message to hang a drain off, so a drain wired
// only into Send() left those rows actionable forever — the agent kept being
// re-woken for finished work. That is the same shape as the original defect this
// whole fix addresses (one path honoring an invariant, another quietly not), so
// the invariant "marked responded implies drained" is enforced at the single
// choke point every path already goes through.
//
// The recipient is resolved from the ORIGINAL request's To field rather than
// from whoever sent the reply, so a mis-attributed or stale correlation still
// drains the right inbox. Best-effort throughout: a request whose log entry has
// been rotated away simply isn't drained.
func MarkResponded(session, originalID, responseID string) {
	if originalID == "" {
		return
	}

	// Status update — skipped when the status file is missing (GC'd or predates
	// delivery tracking), but that must not skip the drain below.
	if ds, err := ReadDeliveryStatus(session, originalID); err == nil {
		ds.Status = StatusResponded
		ds.ResponseID = responseID
		_ = writeDeliveryStatus(session, ds)
	}

	if original, ok := FindMessageByID(session, originalID); ok {
		_, _ = ConsumeByID(session, WindowForRole(original.To), originalID)
	}
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

// hasReceipt is the single definition of "this message was received", shared by
// every read-side consumer (ReadReceipt, and through it ReceiptGap).
//
// Two ways a message can prove receipt:
//   - AckedAt is set — an explicit receipt was written on consume.
//   - Status is responded — the recipient REPLIED to it. A reply is strictly
//     stronger evidence than a consume-ack: the agent didn't just read the
//     message, it finished the work and answered.
//
// The second clause exists because MarkResponded (the reply path) records the
// response without setting AckedAt. Without it, a request that was answered but
// whose inbox row was never consumed reads as un-receipted forever, so
// ReceiptGap counts it permanently and the daemon's backstop re-drives delivery
// and alerts `delivery-gap` for work that is already done.
//
// WriteReceipt has always honored this invariant on the WRITE side ("a reply
// already implies receipt" — it refuses to regress a responded message). This
// keeps the READ side in agreement; the two disagreeing was the defect.
func hasReceipt(ds DeliveryStatus) bool {
	return ds.AckedAt > 0 || ds.Status == StatusResponded
}

// ReadReceipt returns a message's delivery status and whether it carries a
// receipt (see hasReceipt). Used by the daemon's receipt-gap backstop and by the
// delivery decisions that replace the notified-IDs marker.
func ReadReceipt(session, msgID string) (DeliveryStatus, bool) {
	ds, err := ReadDeliveryStatus(session, msgID)
	if err != nil {
		return DeliveryStatus{}, false
	}
	return ds, hasReceipt(ds)
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
		// Already receipted — either consume-acked, or answered (a reply proves
		// the agent got it). An answered request can still be sitting here if its
		// row was never consumed; counting it would make the gap permanent and
		// keep the backstop re-driving delivery for finished work.
		if _, acked := ReadReceipt(session, m.ID); acked {
			continue
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

// AckDeliveryOffMarkerPath is the runtime ROLLBACK marker for the receipt-based
// delivery cutover. The cutover is ON by default (see the daemon's
// ackDeliveryActive); the PRESENCE of this marker reverts a single session to the
// old pane-scrape delivery path. The daemon re-reads it every poll, so writing /
// removing it rolls back (and restores) the cutover instantly without a daemon
// restart — unlike the startup-only MUXCODE_DELIVERY_ACK env var.
// MUXCODE_DELIVERY_ACK_DISABLE is a stronger env-level kill switch that forces the
// old path regardless of this marker.
func AckDeliveryOffMarkerPath(session string) string {
	return filepath.Join(BusDir(session), "delivery-ack.off")
}

// AckDeliveryToggledOff reports whether the runtime rollback marker is present —
// i.e. the session has been reverted from the default receipt-based delivery to
// the old pane-scrape path via `muxcode delivery-ack off`.
func AckDeliveryToggledOff(session string) bool {
	_, err := os.Stat(AckDeliveryOffMarkerPath(session))
	return err == nil
}

// AckDeliveryActive reports whether receipt-based delivery (the delivery-ack
// cutover) is the session's active delivery model. It is the single definition
// shared by the daemon — which bypasses the pane-scrape machinery when this is
// true — and by diagnose, which must read the resulting evidence the same way.
//
// Diagnose had no notion of the cutover at all, and that gap produced a
// user-visible false verdict. `idle-wake` is emitted only by checkIdleAgents,
// the very function the cutover bypasses, so under the default configuration
// every inbox-notify looked like a wake that never came: the timeline rendered
// a red "expected idle-wake, got none" line per notify on a perfectly healthy
// session, while the findings list stayed empty and the exit code stayed 0.
// Two readings of "how does delivery work" that could drift apart were the
// defect; there is now one.
//
// Precedence matches the documented rollback valves: the env kill switch forces
// the old path, an explicit env opt-in/out pins the choice, and the restart-free
// runtime marker decides otherwise. Default is ON.
func AckDeliveryActive(session string) bool {
	if os.Getenv("MUXCODE_DELIVERY_ACK_DISABLE") != "" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("MUXCODE_DELIVERY_ACK"))) {
	case "0", "false", "no", "off":
		return false
	case "1", "true", "yes", "on":
		return true
	}
	return !AckDeliveryToggledOff(session)
}

// SetAckDeliveryOff creates (off=true) or removes (off=false) the runtime
// rollback marker. Removing an absent marker is not an error, so restoring the
// default (`on`) is idempotent.
func SetAckDeliveryOff(session string, off bool) error {
	path := AckDeliveryOffMarkerPath(session)
	if off {
		return os.WriteFile(path, []byte(time.Now().Format(time.RFC3339)+"\n"), 0644)
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
