package bus

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// WebhookConfig holds configuration for the webhook HTTP server.
type WebhookConfig struct {
	Host    string
	Port    int
	Token   string
	Session string
}

// SendRequest is the JSON body for POST /send.
type SendRequest struct {
	To      string `json:"to"`
	Action  string `json:"action"`
	Payload string `json:"payload"`
	Type    string `json:"type"`
	ReplyTo string `json:"reply_to"`
}

// WebhookResponse is the JSON response for all webhook endpoints.
type WebhookResponse struct {
	OK      bool   `json:"ok"`
	ID      string `json:"id,omitempty"`
	Error   string `json:"error,omitempty"`
	Session string `json:"session,omitempty"`
	Uptime  int64  `json:"uptime_seconds,omitempty"`
}

// ServeWebhook starts the HTTP server in the foreground.
// It blocks until the context is cancelled or the server is shut down.
func ServeWebhook(ctx context.Context, cfg WebhookConfig) error {
	startTime := time.Now()

	mux := http.NewServeMux()
	mux.HandleFunc("/send", makeSendHandler(cfg, startTime))
	mux.HandleFunc("/health", makeHealthHandler(cfg, startTime))

	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	server := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	// Write PID file
	if err := WriteWebhookPid(cfg.Session, cfg.Port, os.Getpid()); err != nil {
		return fmt.Errorf("writing PID file: %w", err)
	}

	// Clean up PID file on shutdown
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
		_ = os.Remove(WebhookPidPath(cfg.Session))
	}()

	fmt.Printf("Webhook server listening on %s\n", addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		_ = os.Remove(WebhookPidPath(cfg.Session))
		return err
	}
	return nil
}

// makeSendHandler returns an http.HandlerFunc for POST /send.
func makeSendHandler(cfg WebhookConfig, startTime time.Time) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, WebhookResponse{
				OK:    false,
				Error: "method not allowed, use POST",
			})
			return
		}

		// Auth check
		if cfg.Token != "" {
			auth := r.Header.Get("Authorization")
			if !strings.HasPrefix(auth, "Bearer ") || strings.TrimPrefix(auth, "Bearer ") != cfg.Token {
				writeJSON(w, http.StatusUnauthorized, WebhookResponse{
					OK:    false,
					Error: "unauthorized",
				})
				return
			}
		}

		// Limit request body to 64 KB to prevent abuse
		r.Body = http.MaxBytesReader(w, r.Body, 64*1024)

		// Parse body
		var req SendRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, WebhookResponse{
				OK:    false,
				Error: "invalid JSON: " + err.Error(),
			})
			return
		}

		// Validate required fields
		if req.To == "" {
			writeJSON(w, http.StatusBadRequest, WebhookResponse{
				OK:    false,
				Error: "missing required field: to",
			})
			return
		}
		if req.Action == "" {
			writeJSON(w, http.StatusBadRequest, WebhookResponse{
				OK:    false,
				Error: "missing required field: action",
			})
			return
		}
		if req.Payload == "" {
			writeJSON(w, http.StatusBadRequest, WebhookResponse{
				OK:    false,
				Error: "missing required field: payload",
			})
			return
		}

		// Default type
		if req.Type == "" {
			req.Type = "request"
		}

		// Validate target role
		if !IsKnownRole(req.To) {
			writeJSON(w, http.StatusBadRequest, WebhookResponse{
				OK:    false,
				Error: fmt.Sprintf("unknown role '%s'", req.To),
			})
			return
		}

		// Check send policy
		if deny := CheckSendPolicy("webhook", req.To); deny != "" {
			writeJSON(w, http.StatusForbidden, WebhookResponse{
				OK:    false,
				Error: deny,
			})
			return
		}

		// Create and send message
		msg := NewMessage("webhook", req.To, req.Type, req.Action, req.Payload, req.ReplyTo)
		if err := Send(cfg.Session, msg); err != nil {
			writeJSON(w, http.StatusInternalServerError, WebhookResponse{
				OK:    false,
				Error: "send failed: " + err.Error(),
			})
			return
		}

		// Notify target agent
		_ = Notify(cfg.Session, req.To)

		writeJSON(w, http.StatusOK, WebhookResponse{
			OK: true,
			ID: msg.ID,
		})
	}
}

// makeHealthHandler returns an http.HandlerFunc for GET /health.
func makeHealthHandler(cfg WebhookConfig, startTime time.Time) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, WebhookResponse{
				OK:    false,
				Error: "method not allowed, use GET",
			})
			return
		}

		writeJSON(w, http.StatusOK, WebhookResponse{
			OK:      true,
			Session: cfg.Session,
			Uptime:  int64(time.Since(startTime).Seconds()),
		})
	}
}

// writeJSON encodes a response as JSON and writes it to the ResponseWriter.
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// WriteWebhookPid writes the webhook PID file with format "port:pid".
func WriteWebhookPid(session string, port, pid int) error {
	path := WebhookPidPath(session)
	return os.WriteFile(path, []byte(fmt.Sprintf("%d:%d", port, pid)), 0600)
}

// ReadWebhookPid reads the webhook PID file and returns (port, pid, error).
func ReadWebhookPid(session string) (int, int, error) {
	data, err := os.ReadFile(WebhookPidPath(session))
	if err != nil {
		return 0, 0, err
	}

	parts := strings.SplitN(strings.TrimSpace(string(data)), ":", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid PID file format")
	}

	port, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, fmt.Errorf("invalid port in PID file: %w", err)
	}

	pid, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, fmt.Errorf("invalid PID in PID file: %w", err)
	}

	return port, pid, nil
}

// IsWebhookRunning checks if a webhook process is running for the session.
func IsWebhookRunning(session string) bool {
	_, pid, err := ReadWebhookPid(session)
	if err != nil {
		return false
	}
	return CheckProcAlive(pid)
}

// StopWebhookProcess reads the PID file, sends SIGTERM, and removes the PID file.
func StopWebhookProcess(session string) error {
	_, pid, err := ReadWebhookPid(session)
	if err != nil {
		return fmt.Errorf("no webhook running: %w", err)
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		_ = os.Remove(WebhookPidPath(session))
		return fmt.Errorf("process %d not found: %w", pid, err)
	}

	if err := proc.Signal(syscall.SIGTERM); err != nil {
		// Process may have already exited
		_ = os.Remove(WebhookPidPath(session))
		return fmt.Errorf("sending signal to %d: %w", pid, err)
	}

	_ = os.Remove(WebhookPidPath(session))
	return nil
}

// WebhookStatus returns a human-readable status string for the webhook server.
func WebhookStatus(session string) string {
	port, pid, err := ReadWebhookPid(session)
	if err != nil {
		return "Webhook: not running"
	}

	if !CheckProcAlive(pid) {
		_ = os.Remove(WebhookPidPath(session))
		return "Webhook: not running (stale PID file cleaned)"
	}

	return fmt.Sprintf("Webhook: running on 127.0.0.1:%d (PID %d)", port, pid)
}
