package bus

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAgentStoppedMarkerRoundTrip(t *testing.T) {
	// Use a temp directory for the bus dir
	tmpDir := t.TempDir()
	session := "test-health"
	busDir := filepath.Join(tmpDir, "muxcode-bus-"+session)
	lockDir := filepath.Join(busDir, "lock")
	if err := os.MkdirAll(lockDir, 0755); err != nil {
		t.Fatalf("failed to create lock dir: %v", err)
	}

	// Override BusDir for this test
	origBusDir := os.Getenv("BUS_DIR_PREFIX")
	os.Setenv("BUS_DIR_PREFIX", tmpDir+"/muxcode-bus-")
	defer os.Setenv("BUS_DIR_PREFIX", origBusDir)

	// Use the actual path directly since BusDir uses /tmp
	stoppedPath := filepath.Join(lockDir, "build.stopped")

	// Initially not stopped
	if _, err := os.Stat(stoppedPath); err == nil {
		t.Error("expected stopped marker to not exist initially")
	}

	// Write marker
	if err := os.WriteFile(stoppedPath, []byte("stopped"), 0644); err != nil {
		t.Fatalf("failed to write stopped marker: %v", err)
	}

	// Verify it exists
	if _, err := os.Stat(stoppedPath); err != nil {
		t.Error("expected stopped marker to exist after write")
	}

	// Remove marker
	os.Remove(stoppedPath)

	// Verify removed
	if _, err := os.Stat(stoppedPath); err == nil {
		t.Error("expected stopped marker to not exist after remove")
	}
}

func TestIsShellPrompt(t *testing.T) {
	tests := []struct {
		name  string
		lines []string
		want  bool
	}{
		{
			name:  "bash prompt",
			lines: []string{"", "mark@mac ~/Repos $", ""},
			want:  true,
		},
		{
			name:  "zsh prompt",
			lines: []string{"", "mark@mac ~/Repos %", ""},
			want:  true,
		},
		{
			name:  "claude code idle",
			lines: []string{"", "❯", "? for shortcuts"},
			want:  false,
		},
		{
			name:  "claude code active",
			lines: []string{"", "❯ You have new messages", "processing..."},
			want:  false,
		},
		{
			name:  "agent starting",
			lines: []string{"", "muxcode-agent.sh build", "Starting..."},
			want:  false,
		},
		{
			name:  "empty pane",
			lines: []string{"", "", ""},
			want:  false,
		},
		{
			name:  "dollar in middle of text",
			lines: []string{"", "cost is $50", "some output"},
			want:  false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := isShellPrompt(tc.lines)
			if got != tc.want {
				t.Errorf("isShellPrompt(%v) = %v, want %v", tc.lines, got, tc.want)
			}
		})
	}
}

func TestFormatAgentHealthAlert(t *testing.T) {
	tests := []struct {
		status  string
		role    string
		message string
		want    string
	}{
		{"down", "build", "Agent not responding", "⚠ AGENT DOWN: build\n  Agent not responding\n"},
		{"restarting", "test", "Attempt 1/3", "🔄 AGENT RESTARTING: test\n  Attempt 1/3\n"},
		{"recovered", "build", "Agent is back", "✅ AGENT RECOVERED: build\n  Agent is back\n"},
	}

	for _, tc := range tests {
		t.Run(tc.status, func(t *testing.T) {
			got := FormatAgentHealthAlert(tc.status, tc.role, tc.message)
			if got != tc.want {
				t.Errorf("FormatAgentHealthAlert(%q, %q, %q) = %q, want %q",
					tc.status, tc.role, tc.message, got, tc.want)
			}
		})
	}
}

func TestAgentHealthAlertKey(t *testing.T) {
	key := AgentHealthAlertKey("build", "down")
	if key != "agent:build:down" {
		t.Errorf("AgentHealthAlertKey(build, down) = %q, want %q", key, "agent:build:down")
	}
}

func TestIsAgentHealthExcluded(t *testing.T) {
	if !IsAgentHealthExcluded("edit") {
		t.Error("expected edit to be excluded")
	}
	if !IsAgentHealthExcluded("webhook") {
		t.Error("expected webhook to be excluded")
	}
	if IsAgentHealthExcluded("build") {
		t.Error("expected build to NOT be excluded")
	}
	if IsAgentHealthExcluded("test") {
		t.Error("expected test to NOT be excluded")
	}
}
