package bus

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func setupWebhookTest(t *testing.T) (WebhookConfig, func()) {
	t.Helper()
	session := fmt.Sprintf("test-webhook-%d", rand.Int())
	memDir := t.TempDir()
	if err := Init(session, memDir); err != nil {
		t.Fatalf("Init: %v", err)
	}
	cfg := WebhookConfig{
		Host:    "127.0.0.1",
		Port:    9090,
		Session: session,
	}
	return cfg, func() { _ = Cleanup(session) }
}

func TestWebhookSendHandler_ValidRequest(t *testing.T) {
	cfg, cleanup := setupWebhookTest(t)
	defer cleanup()

	handler := makeSendHandler(cfg, time.Now())

	body := `{"to":"build","action":"build","payload":"Run tests"}`
	req := httptest.NewRequest(http.MethodPost, "/send", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp WebhookResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.OK {
		t.Error("expected ok=true")
	}
	if resp.ID == "" {
		t.Error("expected non-empty message ID")
	}

	// Verify message was delivered to inbox
	msgs, err := Peek(cfg.Session, "build")
	if err != nil {
		t.Fatalf("Peek: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("inbox count = %d, want 1", len(msgs))
	}
	if msgs[0].From != "webhook" {
		t.Errorf("from = %q, want %q", msgs[0].From, "webhook")
	}
	if msgs[0].Action != "build" {
		t.Errorf("action = %q, want %q", msgs[0].Action, "build")
	}
}

func TestWebhookSendHandler_DefaultType(t *testing.T) {
	cfg, cleanup := setupWebhookTest(t)
	defer cleanup()

	handler := makeSendHandler(cfg, time.Now())

	body := `{"to":"build","action":"build","payload":"Run tests"}`
	req := httptest.NewRequest(http.MethodPost, "/send", bytes.NewBufferString(body))
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	msgs, _ := Peek(cfg.Session, "build")
	if len(msgs) != 1 {
		t.Fatalf("inbox count = %d, want 1", len(msgs))
	}
	if msgs[0].Type != "request" {
		t.Errorf("type = %q, want %q", msgs[0].Type, "request")
	}
}

func TestWebhookSendHandler_CustomType(t *testing.T) {
	cfg, cleanup := setupWebhookTest(t)
	defer cleanup()

	handler := makeSendHandler(cfg, time.Now())

	body := `{"to":"build","action":"notify","payload":"CI done","type":"event"}`
	req := httptest.NewRequest(http.MethodPost, "/send", bytes.NewBufferString(body))
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	msgs, _ := Peek(cfg.Session, "build")
	if len(msgs) != 1 {
		t.Fatalf("inbox count = %d, want 1", len(msgs))
	}
	if msgs[0].Type != "event" {
		t.Errorf("type = %q, want %q", msgs[0].Type, "event")
	}
}

func TestWebhookSendHandler_MissingTo(t *testing.T) {
	cfg, cleanup := setupWebhookTest(t)
	defer cleanup()

	handler := makeSendHandler(cfg, time.Now())

	body := `{"action":"build","payload":"Run tests"}`
	req := httptest.NewRequest(http.MethodPost, "/send", bytes.NewBufferString(body))
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	var resp WebhookResponse
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.OK {
		t.Error("expected ok=false")
	}
	if resp.Error != "missing required field: to" {
		t.Errorf("error = %q, want 'missing required field: to'", resp.Error)
	}
}

func TestWebhookSendHandler_MissingAction(t *testing.T) {
	cfg, cleanup := setupWebhookTest(t)
	defer cleanup()

	handler := makeSendHandler(cfg, time.Now())

	body := `{"to":"build","payload":"Run tests"}`
	req := httptest.NewRequest(http.MethodPost, "/send", bytes.NewBufferString(body))
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestWebhookSendHandler_MissingPayload(t *testing.T) {
	cfg, cleanup := setupWebhookTest(t)
	defer cleanup()

	handler := makeSendHandler(cfg, time.Now())

	body := `{"to":"build","action":"build"}`
	req := httptest.NewRequest(http.MethodPost, "/send", bytes.NewBufferString(body))
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestWebhookSendHandler_UnknownRole(t *testing.T) {
	cfg, cleanup := setupWebhookTest(t)
	defer cleanup()

	handler := makeSendHandler(cfg, time.Now())

	body := `{"to":"nonexistent","action":"build","payload":"Run tests"}`
	req := httptest.NewRequest(http.MethodPost, "/send", bytes.NewBufferString(body))
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	var resp WebhookResponse
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if !bytes.Contains([]byte(resp.Error), []byte("unknown role")) {
		t.Errorf("error = %q, want 'unknown role' substring", resp.Error)
	}
}

func TestWebhookSendHandler_InvalidJSON(t *testing.T) {
	cfg, cleanup := setupWebhookTest(t)
	defer cleanup()

	handler := makeSendHandler(cfg, time.Now())

	req := httptest.NewRequest(http.MethodPost, "/send", bytes.NewBufferString("not json"))
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestWebhookSendHandler_WrongMethod(t *testing.T) {
	cfg, cleanup := setupWebhookTest(t)
	defer cleanup()

	handler := makeSendHandler(cfg, time.Now())

	req := httptest.NewRequest(http.MethodGet, "/send", nil)
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestWebhookSendHandler_AuthRequired(t *testing.T) {
	cfg, cleanup := setupWebhookTest(t)
	defer cleanup()
	cfg.Token = "secret123"

	handler := makeSendHandler(cfg, time.Now())

	body := `{"to":"build","action":"build","payload":"Run tests"}`

	// No auth header
	req := httptest.NewRequest(http.MethodPost, "/send", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	handler(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("no auth: status = %d, want %d", w.Code, http.StatusUnauthorized)
	}

	// Wrong token
	req = httptest.NewRequest(http.MethodPost, "/send", bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer wrongtoken")
	w = httptest.NewRecorder()
	handler(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token: status = %d, want %d", w.Code, http.StatusUnauthorized)
	}

	// Correct token
	req = httptest.NewRequest(http.MethodPost, "/send", bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer secret123")
	w = httptest.NewRecorder()
	handler(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("correct token: status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}
}

func TestWebhookSendHandler_NoAuthWhenTokenEmpty(t *testing.T) {
	cfg, cleanup := setupWebhookTest(t)
	defer cleanup()
	// cfg.Token is "" by default

	handler := makeSendHandler(cfg, time.Now())

	body := `{"to":"build","action":"build","payload":"Run tests"}`
	req := httptest.NewRequest(http.MethodPost, "/send", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestWebhookHealthHandler(t *testing.T) {
	cfg, cleanup := setupWebhookTest(t)
	defer cleanup()

	startTime := time.Now().Add(-60 * time.Second) // 60s ago
	handler := makeHealthHandler(cfg, startTime)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp WebhookResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.OK {
		t.Error("expected ok=true")
	}
	if resp.Session != cfg.Session {
		t.Errorf("session = %q, want %q", resp.Session, cfg.Session)
	}
	if resp.Uptime < 59 {
		t.Errorf("uptime = %d, want >= 59", resp.Uptime)
	}
}

func TestWebhookHealthHandler_WrongMethod(t *testing.T) {
	cfg, cleanup := setupWebhookTest(t)
	defer cleanup()

	handler := makeHealthHandler(cfg, time.Now())

	req := httptest.NewRequest(http.MethodPost, "/health", nil)
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestWebhookPidFile(t *testing.T) {
	session := fmt.Sprintf("test-webhook-pid-%d", rand.Int())
	memDir := t.TempDir()
	if err := Init(session, memDir); err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer func() { _ = Cleanup(session) }()

	// Write PID file
	if err := WriteWebhookPid(session, 9090, 12345); err != nil {
		t.Fatalf("WriteWebhookPid: %v", err)
	}

	// Read it back
	port, pid, err := ReadWebhookPid(session)
	if err != nil {
		t.Fatalf("ReadWebhookPid: %v", err)
	}
	if port != 9090 {
		t.Errorf("port = %d, want 9090", port)
	}
	if pid != 12345 {
		t.Errorf("pid = %d, want 12345", pid)
	}

	// Clean up
	_ = os.Remove(WebhookPidPath(session))
}

func TestReadWebhookPid_NotExists(t *testing.T) {
	_, _, err := ReadWebhookPid("nonexistent-session")
	if err == nil {
		t.Fatal("expected error for nonexistent PID file")
	}
}

func TestWebhookStatus_NotRunning(t *testing.T) {
	status := WebhookStatus("nonexistent-session")
	if status != "Webhook: not running" {
		t.Errorf("status = %q, want 'Webhook: not running'", status)
	}
}

func TestWebhookSendHandler_ReplyTo(t *testing.T) {
	cfg, cleanup := setupWebhookTest(t)
	defer cleanup()

	handler := makeSendHandler(cfg, time.Now())

	body := `{"to":"build","action":"build","payload":"Run tests","reply_to":"1234-edit-abcd"}`
	req := httptest.NewRequest(http.MethodPost, "/send", bytes.NewBufferString(body))
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	msgs, _ := Peek(cfg.Session, "build")
	if len(msgs) != 1 {
		t.Fatalf("inbox count = %d, want 1", len(msgs))
	}
	if msgs[0].ReplyTo != "1234-edit-abcd" {
		t.Errorf("reply_to = %q, want %q", msgs[0].ReplyTo, "1234-edit-abcd")
	}
}

func TestWebhookIsKnownRole(t *testing.T) {
	// Verify "webhook" is in KnownRoles
	if !IsKnownRole("webhook") {
		t.Error("expected 'webhook' to be a known role")
	}
}
