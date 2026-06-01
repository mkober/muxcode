package bus

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestReadServeState(t *testing.T) {
	tmpDir := t.TempDir()
	session := "test-serve"
	busDir := filepath.Join(tmpDir, "muxcode-bus-"+session)
	if err := os.MkdirAll(busDir, 0755); err != nil {
		t.Fatalf("failed to create bus dir: %v", err)
	}

	// Override BusDir for this test
	SetBusDirBase(tmpDir)
	defer ResetBusDirBase()

	// No state file — should return nil
	state := ReadServeState(session)
	if state != nil {
		t.Error("expected nil when state file doesn't exist")
	}

	// Write a valid state file
	serveState := ServeState{
		Servers: []ServerEntry{
			{
				Name:    "vite",
				Command: "pnpm dev",
				Port:    5173,
				PID:     12345,
				URL:     "http://localhost:5173/",
				Status:  "running",
			},
			{
				Name:    "api",
				Command: "go run .",
				Port:    8080,
				PID:     12346,
				URL:     "http://localhost:8080/",
				Status:  "stopped",
			},
		},
	}
	data, _ := json.Marshal(serveState)
	statePath := filepath.Join(busDir, "serve-state.json")
	if err := os.WriteFile(statePath, data, 0644); err != nil {
		t.Fatalf("failed to write state file: %v", err)
	}

	// Read it back
	state = ReadServeState(session)
	if state == nil {
		t.Fatal("expected non-nil state")
	}
	if len(state.Servers) != 2 {
		t.Fatalf("expected 2 servers, got %d", len(state.Servers))
	}

	// RunningServers should filter to only running
	running := state.RunningServers()
	if len(running) != 1 {
		t.Fatalf("expected 1 running server, got %d", len(running))
	}
	if running[0].Name != "vite" {
		t.Errorf("expected running server name 'vite', got %q", running[0].Name)
	}
}

func TestIsViteServer(t *testing.T) {
	tests := []struct {
		name   string
		entry  ServerEntry
		expect bool
	}{
		{
			name:   "vite by name",
			entry:  ServerEntry{Name: "vite", Command: "pnpm dev", Port: 5173},
			expect: true,
		},
		{
			name:   "sveltekit by name",
			entry:  ServerEntry{Name: "sveltekit", Command: "pnpm dev", Port: 5173},
			expect: true,
		},
		{
			name:   "vite by command",
			entry:  ServerEntry{Name: "frontend", Command: "npx vite", Port: 3000},
			expect: true,
		},
		{
			name:   "vite by pnpm dev",
			entry:  ServerEntry{Name: "app", Command: "pnpm dev", Port: 3000},
			expect: true,
		},
		{
			name:   "vite by default port 5173",
			entry:  ServerEntry{Name: "app", Command: "node server.js", Port: 5173},
			expect: true,
		},
		{
			name:   "vite by default port 5174",
			entry:  ServerEntry{Name: "app", Command: "node server.js", Port: 5174},
			expect: true,
		},
		{
			name:   "astro by port 4321 — not vite (uses Vite internally but is not a Vite server)",
			entry:  ServerEntry{Name: "app", Command: "node server.js", Port: 4321},
			expect: false,
		},
		{
			name:   "go server not vite",
			entry:  ServerEntry{Name: "api", Command: "go run .", Port: 8080},
			expect: false,
		},
		{
			name:   "flask not vite",
			entry:  ServerEntry{Name: "backend", Command: "flask run", Port: 5000},
			expect: false,
		},
		{
			name:   "django not vite",
			entry:  ServerEntry{Name: "django", Command: "python manage.py runserver", Port: 8000},
			expect: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.entry.IsViteServer()
			if got != tt.expect {
				t.Errorf("IsViteServer() = %v, want %v (name=%q cmd=%q port=%d)",
					got, tt.expect, tt.entry.Name, tt.entry.Command, tt.entry.Port)
			}
		})
	}
}

func TestRunningServersNil(t *testing.T) {
	var s *ServeState
	running := s.RunningServers()
	if running != nil {
		t.Error("expected nil from nil ServeState")
	}
}

func TestReadServeStateMalformed(t *testing.T) {
	tmpDir := t.TempDir()
	session := "test-malformed"
	busDir := filepath.Join(tmpDir, "muxcode-bus-"+session)
	if err := os.MkdirAll(busDir, 0755); err != nil {
		t.Fatalf("failed to create bus dir: %v", err)
	}

	SetBusDirBase(tmpDir)
	defer ResetBusDirBase()

	// Write invalid JSON
	statePath := filepath.Join(busDir, "serve-state.json")
	if err := os.WriteFile(statePath, []byte("not json"), 0644); err != nil {
		t.Fatalf("failed to write state file: %v", err)
	}

	state := ReadServeState(session)
	if state != nil {
		t.Error("expected nil for malformed JSON")
	}
}
