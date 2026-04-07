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
	Status      string `json:"status"` // sent, delivered, responded, expired
	SentAt      int64  `json:"sent_at"`
	DeliveredAt int64  `json:"delivered_at"`
	ResponseID  string `json:"response_id"`
}

// Delivery status constants.
const (
	StatusSent      = "sent"
	StatusDelivered = "delivered"
	StatusResponded = "responded"
	StatusExpired   = "expired"
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

// MarkDelivered updates a message's delivery status to "delivered".
// Called by Receive() when messages are consumed from the inbox.
func MarkDelivered(session, msgID string) {
	ds, err := ReadDeliveryStatus(session, msgID)
	if err != nil {
		return // no status file — message predates delivery tracking
	}
	if ds.Status != StatusSent {
		return // already advanced past sent
	}
	ds.Status = StatusDelivered
	ds.DeliveredAt = time.Now().Unix()
	_ = writeDeliveryStatus(session, ds)
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
// Called by the watcher during periodic cleanup.
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
