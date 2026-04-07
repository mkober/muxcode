package bus

import (
	"testing"
	"time"
)

func TestCreateDeliveryStatus(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)

	msg := NewMessage("edit", "build", "request", "compile", "build it", "")
	if err := CreateDeliveryStatus(session, msg); err != nil {
		t.Fatalf("CreateDeliveryStatus: %v", err)
	}

	ds, err := ReadDeliveryStatus(session, msg.ID)
	if err != nil {
		t.Fatalf("ReadDeliveryStatus: %v", err)
	}
	if ds.ID != msg.ID {
		t.Errorf("ID = %q, want %q", ds.ID, msg.ID)
	}
	if ds.Status != StatusSent {
		t.Errorf("Status = %q, want %q", ds.Status, StatusSent)
	}
	if ds.SentAt != msg.TS {
		t.Errorf("SentAt = %d, want %d", ds.SentAt, msg.TS)
	}
}

func TestMarkDelivered(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)

	msg := NewMessage("edit", "build", "request", "compile", "build it", "")
	if err := CreateDeliveryStatus(session, msg); err != nil {
		t.Fatalf("CreateDeliveryStatus: %v", err)
	}

	MarkDelivered(session, msg.ID)

	ds, err := ReadDeliveryStatus(session, msg.ID)
	if err != nil {
		t.Fatalf("ReadDeliveryStatus: %v", err)
	}
	if ds.Status != StatusDelivered {
		t.Errorf("Status = %q, want %q", ds.Status, StatusDelivered)
	}
	if ds.DeliveredAt == 0 {
		t.Error("DeliveredAt should be set")
	}
}

func TestMarkDelivered_Idempotent(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)

	msg := NewMessage("edit", "build", "request", "compile", "build it", "")
	if err := CreateDeliveryStatus(session, msg); err != nil {
		t.Fatalf("CreateDeliveryStatus: %v", err)
	}

	MarkDelivered(session, msg.ID)
	ds1, _ := ReadDeliveryStatus(session, msg.ID)

	// Second call should not change anything (already past "sent")
	MarkDelivered(session, msg.ID)
	ds2, _ := ReadDeliveryStatus(session, msg.ID)

	if ds1.DeliveredAt != ds2.DeliveredAt {
		t.Errorf("DeliveredAt changed: %d -> %d", ds1.DeliveredAt, ds2.DeliveredAt)
	}
}

func TestMarkDelivered_NoStatusFile(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)

	// Should not panic — gracefully handles missing status file
	MarkDelivered(session, "nonexistent-id")
}

func TestMarkResponded(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)

	orig := NewMessage("edit", "build", "request", "compile", "build it", "")
	if err := CreateDeliveryStatus(session, orig); err != nil {
		t.Fatalf("CreateDeliveryStatus: %v", err)
	}

	resp := NewMessage("build", "edit", "response", "compile", "done", orig.ID)
	MarkResponded(session, orig.ID, resp.ID)

	ds, err := ReadDeliveryStatus(session, orig.ID)
	if err != nil {
		t.Fatalf("ReadDeliveryStatus: %v", err)
	}
	if ds.Status != StatusResponded {
		t.Errorf("Status = %q, want %q", ds.Status, StatusResponded)
	}
	if ds.ResponseID != resp.ID {
		t.Errorf("ResponseID = %q, want %q", ds.ResponseID, resp.ID)
	}
}

func TestMarkResponded_EmptyOriginalID(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)

	// Should be a no-op — empty original ID means no reply-to
	MarkResponded(session, "", "some-response-id")

	// No file should be created
	statuses, _ := ListDeliveryStatuses(session)
	if len(statuses) != 0 {
		t.Errorf("expected no statuses, got %d", len(statuses))
	}
}

func TestListDeliveryStatuses(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)

	for i := 0; i < 3; i++ {
		msg := NewMessage("edit", "build", "request", "compile", "go", "")
		if err := CreateDeliveryStatus(session, msg); err != nil {
			t.Fatalf("CreateDeliveryStatus %d: %v", i, err)
		}
	}

	statuses, err := ListDeliveryStatuses(session)
	if err != nil {
		t.Fatalf("ListDeliveryStatuses: %v", err)
	}
	if len(statuses) != 3 {
		t.Errorf("got %d statuses, want 3", len(statuses))
	}
}

func TestListDeliveryStatuses_EmptyDir(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)

	statuses, err := ListDeliveryStatuses(session)
	if err != nil {
		t.Fatalf("ListDeliveryStatuses: %v", err)
	}
	if len(statuses) != 0 {
		t.Errorf("got %d statuses, want 0", len(statuses))
	}
}

func TestCleanExpiredDeliveries(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)

	msg := NewMessage("edit", "build", "request", "compile", "go", "")
	// Backdate SentAt to 2 hours ago so cleanup treats it as expired
	msg.TS = time.Now().Add(-2 * time.Hour).Unix()
	if err := CreateDeliveryStatus(session, msg); err != nil {
		t.Fatalf("CreateDeliveryStatus: %v", err)
	}

	cleaned := CleanExpiredDeliveries(session, 1*time.Hour)
	if cleaned != 1 {
		t.Errorf("cleaned = %d, want 1", cleaned)
	}

	// Should be gone
	_, err := ReadDeliveryStatus(session, msg.ID)
	if err == nil {
		t.Error("expected error reading cleaned status")
	}
}

func TestCleanExpiredDeliveries_KeepsRecent(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)

	msg := NewMessage("edit", "build", "request", "compile", "go", "")
	if err := CreateDeliveryStatus(session, msg); err != nil {
		t.Fatalf("CreateDeliveryStatus: %v", err)
	}

	cleaned := CleanExpiredDeliveries(session, 1*time.Hour)
	if cleaned != 0 {
		t.Errorf("cleaned = %d, want 0 (file is recent)", cleaned)
	}
}

func TestSendCreatesDeliveryStatus(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)

	msg := NewMessage("edit", "build", "request", "compile", "build it", "")
	if err := Send(session, msg); err != nil {
		t.Fatalf("Send: %v", err)
	}

	ds, err := ReadDeliveryStatus(session, msg.ID)
	if err != nil {
		t.Fatalf("ReadDeliveryStatus: %v", err)
	}
	if ds.Status != StatusSent {
		t.Errorf("Status = %q, want %q", ds.Status, StatusSent)
	}
}

func TestReceiveMarksDelivered(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)

	msg := NewMessage("edit", "build", "request", "compile", "build it", "")
	if err := Send(session, msg); err != nil {
		t.Fatalf("Send: %v", err)
	}

	_, err := Receive(session, "build")
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}

	ds, err := ReadDeliveryStatus(session, msg.ID)
	if err != nil {
		t.Fatalf("ReadDeliveryStatus: %v", err)
	}
	if ds.Status != StatusDelivered {
		t.Errorf("Status = %q, want %q", ds.Status, StatusDelivered)
	}
}

func TestSendWithReplyToMarksResponded(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)

	// Original message
	orig := NewMessage("edit", "build", "request", "compile", "build it", "")
	if err := Send(session, orig); err != nil {
		t.Fatalf("Send original: %v", err)
	}

	// Response with ReplyTo
	resp := NewMessage("build", "edit", "response", "compile", "done", orig.ID)
	if err := Send(session, resp); err != nil {
		t.Fatalf("Send response: %v", err)
	}

	ds, err := ReadDeliveryStatus(session, orig.ID)
	if err != nil {
		t.Fatalf("ReadDeliveryStatus: %v", err)
	}
	if ds.Status != StatusResponded {
		t.Errorf("Status = %q, want %q", ds.Status, StatusResponded)
	}
	if ds.ResponseID != resp.ID {
		t.Errorf("ResponseID = %q, want %q", ds.ResponseID, resp.ID)
	}
}

func TestFormatDeliveryStatus(t *testing.T) {
	ds := DeliveryStatus{
		ID:          "123-edit-abcd1234",
		Status:      StatusDelivered,
		SentAt:      1000,
		DeliveredAt: 1003,
	}
	s := FormatDeliveryStatus(ds)
	if s == "" {
		t.Error("FormatDeliveryStatus returned empty string")
	}
}
